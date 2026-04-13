// internal/agents/fix/fix.go
package fix

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"text/template"
	"time"

	_ "embed"

	"github.com/fixloop/fixloop/internal/agentrun"
	"github.com/fixloop/fixloop/internal/agents/shared"
	"github.com/fixloop/fixloop/internal/config"
	"github.com/fixloop/fixloop/internal/crypto"
	githubclient "github.com/fixloop/fixloop/internal/github"
	"github.com/fixloop/fixloop/internal/gitops"
	"github.com/fixloop/fixloop/internal/notify"
	"github.com/fixloop/fixloop/internal/runner"
)

//go:embed prompts/fix.txt
var fixPromptTmpl string

var fixTmpl = template.Must(template.New("fix").Parse(fixPromptTmpl))

// DefaultPrompt returns the built-in fix prompt template text.
func DefaultPrompt() string { return fixPromptTmpl }

// Agent runs the fix loop for a single project.
type Agent struct {
	DB  *sql.DB
	Cfg *config.Config
}

type projectConf struct {
	GitHub struct {
		Owner         string `json:"owner"`
		Repo          string `json:"repo"`
		PAT           string `json:"pat"`
		FixBaseBranch string `json:"fix_base_branch"`
	} `json:"github"`
	IssueTracker struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
	} `json:"issue_tracker"`
	SSHPrivateKey string `json:"ssh_private_key"`
	AIRunner      string `json:"ai_runner"`
	AIModel       string `json:"ai_model"`
	AIAPIBase     string `json:"ai_api_base"`
	AIAPIKey      string `json:"ai_api_key"`
}

// fixRules holds parsed control directives extracted from the agent's rules field.
type fixRules struct {
	maxPriority int    // 0 = no filter; otherwise only pick issues with priority <= maxPriority
	maxAttempts int    // needs-human threshold (default 3)
	promptRules string // rules text with directives stripped, appended to prompt
}

// parseFixRules extracts MAX_PRIORITY and MAX_ATTEMPTS directives from the rules
// string. Directive lines are consumed and not included in promptRules.
// Format:  MAX_PRIORITY: <n>   — skip issues with priority > n (e.g. 2 = only P1/P2)
//
//	MAX_ATTEMPTS: <n>   — mark needs-human after n failed attempts
func parseFixRules(rules string) fixRules {
	fr := fixRules{maxPriority: 0, maxAttempts: 3}
	var promptLines []string
	for _, line := range strings.Split(rules, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(lower, "max_priority:") {
			if n, err := strconv.Atoi(strings.TrimSpace(line[len("max_priority:"):])); err == nil && n > 0 {
				fr.maxPriority = n
			}
			continue // directive line — don't pass to prompt
		}
		if strings.HasPrefix(lower, "max_attempts:") {
			if n, err := strconv.Atoi(strings.TrimSpace(line[len("max_attempts:"):])); err == nil && n > 0 {
				fr.maxAttempts = n
			}
			continue
		}
		promptLines = append(promptLines, line)
	}
	fr.promptRules = strings.TrimSpace(strings.Join(promptLines, "\n"))
	return fr
}

type issueRow struct {
	id           int64
	githubNumber int
	title        string
	fixAttempts  int
}

type promptData struct {
	IssueTitle       string
	IssueBody        string
	DirTree          string
	PreviousFailures string
}

func (a *Agent) Run(ctx context.Context, projectID int64, projectAgentID int64) {
	// Load from project_agents
	var fixEnabled bool
	var promptOverrideDB, rulesDB sql.NullString
	var dailyLimit int
	err := a.DB.QueryRowContext(ctx,
		`SELECT enabled, prompt_override, rules, daily_limit FROM project_agents WHERE id = ?`, projectAgentID,
	).Scan(&fixEnabled, &promptOverrideDB, &rulesDB, &dailyLimit)
	if err == nil && !fixEnabled {
		slog.Info("fix: agent disabled in project_agents, skipping", "project_id", projectID)
		return
	}
	if dailyLimit <= 0 {
		dailyLimit = 30
	}

	var (
		cfgJSON       string
		configVersion int
		userID        int64
		status        string
	)
	if err := a.DB.QueryRowContext(ctx,
		`SELECT user_id, config, config_version, status FROM projects WHERE id = ? AND deleted_at IS NULL`,
		projectID,
	).Scan(&userID, &cfgJSON, &configVersion, &status); err != nil {
		slog.Error("fix: load project", "project_id", projectID, "err", err)
		return
	}
	if status != "active" {
		return
	}

	var pcfg projectConf
	if err := json.Unmarshal([]byte(cfgJSON), &pcfg); err != nil {
		slog.Error("fix: parse config", "project_id", projectID, "err", err)
		return
	}
	if pcfg.GitHub.Owner == "" || pcfg.GitHub.Repo == "" {
		return
	}

	// Daily run limit
	if shared.ExceedsDailyLimit(ctx, a.DB, projectID, "fix", dailyLimit) {
		slog.Info("fix: daily run limit reached", "project_id", projectID)
		return
	}

	rules := ""
	if rulesDB.Valid {
		rules = rulesDB.String
	}
	fr := parseFixRules(rules)

	// Pick one open issue (optimistic lock)
	issue, err := a.claimIssue(ctx, projectID, fr)
	if err != nil || issue == nil {
		if err != nil {
			slog.Error("fix: claim issue", "project_id", projectID, "err", err)
		}
		return
	}

	runID, err := agentrun.Start(ctx, a.DB, projectID, "fix", configVersion, projectAgentID)
	if err != nil {
		slog.Error("fix: start agentrun", "project_id", projectID, "err", err)
		a.releaseIssue(ctx, issue.id)
		return
	}

	promptOverride := ""
	if promptOverrideDB.Valid {
		promptOverride = promptOverrideDB.String
	}
	agentrun.WithRecover(runID, a.DB, func() {
		output, finalStatus := a.runFix(ctx, projectID, userID, runID, issue, &pcfg, promptOverride, fr)
		if err := agentrun.Finish(ctx, a.DB, runID, finalStatus, output); err != nil {
			slog.Error("fix: finish agentrun", "run_id", runID, "err", err)
		}
	})
}

func (a *Agent) runFix(ctx context.Context, projectID, userID, runID int64, issue *issueRow, pcfg *projectConf, promptOverride string, fr fixRules) (string, string) {
	var logBuf bytes.Buffer
	logf := func(msg string, args ...any) {
		line := fmt.Sprintf(msg, args...)
		logBuf.WriteString(line + "\n")
		slog.Info("fix: "+line, "project_id", projectID, "run_id", runID)
	}

	// Decrypt SSH key
	sshKeyEnc, err := hex.DecodeString(pcfg.SSHPrivateKey)
	if err != nil {
		logf("错误：SSH 密钥解码失败：%v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}
	sshKey, err := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, sshKeyEnc)
	if err != nil {
		logf("错误：SSH 密钥解密失败：%v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}

	// Decrypt PAT
	patEnc, err := hex.DecodeString(pcfg.GitHub.PAT)
	if err != nil {
		logf("错误：PAT 解码失败：%v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}
	pat, err := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, patEnc)
	if err != nil {
		logf("错误：PAT 解密失败：%v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}

	baseBranch := pcfg.GitHub.FixBaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	repoPath := gitops.AgentRepoPath(a.Cfg.WorkspaceDir, pcfg.GitHub.Owner, pcfg.GitHub.Repo, "fix")
	logf("准备本地仓库：%s", repoPath)

	if err := gitops.EnsureRepo(ctx, sshKey, pcfg.GitHub.Owner, pcfg.GitHub.Repo, repoPath, baseBranch); err != nil {
		logf("错误：仓库初始化失败：%v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}

	branchName := fmt.Sprintf("fix/issue-%d", issue.githubNumber)
	logf("切换到分支：%s", branchName)

	if err := gitops.EnsureBranch(ctx, sshKey, repoPath, branchName, baseBranch); err != nil {
		logf("错误：分支切换失败：%v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}

	// Build prompt
	dirTree := gitops.DirTree(repoPath, 3)
	prompt, err := buildPrompt(issue.title, "", dirTree, "", promptOverride, fr.promptRules)
	if err != nil {
		logf("错误：构建提示词失败：%v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}
	logBuf.WriteString("\n--- PROMPT ---\n" + prompt + "\n--- END PROMPT ---\n")

	// Build fix runner (decrypt API key if present)
	aiAPIKey := ""
	if pcfg.AIAPIKey != "" {
		keyEnc, _ := hex.DecodeString(pcfg.AIAPIKey)
		if plain, err := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, keyEnc); err == nil {
			aiAPIKey = string(plain)
		}
	}
	r, err := runner.New(pcfg.AIRunner, pcfg.AIModel, pcfg.AIAPIBase, aiAPIKey)
	if err != nil {
		logf("错误：初始化运行器失败：%v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}

	logf("启动 AI 修复（运行器=%s）", pcfg.AIRunner)
	fixCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	aiOutput, err := r.Run(fixCtx, repoPath, prompt)
	logBuf.WriteString("\n--- AI OUTPUT ---\n" + aiOutput + "\n--- END AI OUTPUT ---\n")
	if err != nil {
		logf("错误：AI 运行失败：%v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}

	// Check if AI made any changes
	hasChanges, err := gitops.HasChanges(ctx, repoPath)
	if err != nil {
		logf("错误：检查文件变更失败：%v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}
	if !hasChanges {
		logf("AI 未产生任何文件改动，已在 Issue 上留言")
		gh := githubclient.New(string(pat))
		_ = gh.AddIssueComment(ctx,
			pcfg.IssueTracker.Owner, pcfg.IssueTracker.Repo,
			issue.githubNumber,
			fmt.Sprintf("<!-- fixloop-failure -->\nfix-agent run #%d: AI did not produce any file changes. Will retry.", runID),
		)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "skipped"
	}

	// Commit and push
	commitMsg := fmt.Sprintf("fix: %s (#%d)", issue.title, issue.githubNumber)
	if err := gitops.CommitAll(ctx, repoPath, commitMsg); err != nil {
		logf("错误：提交失败：%v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}

	force := issue.fixAttempts > 0
	if err := gitops.Push(ctx, sshKey, repoPath, branchName, force); err != nil {
		logf("错误：推送失败：%v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}
	logf("已推送分支 %s（force=%v）", branchName, force)

	// Create or find existing PR
	gh := githubclient.New(string(pat))
	existingPR, err := a.findExistingPR(ctx, projectID, issue.id)
	if err != nil {
		logf("警告：查找已有 PR 失败：%v", err)
	}

	var prNumber int
	if existingPR != 0 {
		logf("已存在 PR #%d，跳过创建", existingPR)
		prNumber = existingPR
	} else {
		prTitle := fmt.Sprintf("fix: %s (#%d)", issue.title, issue.githubNumber)
		prBody := buildPRBody(issue.githubNumber, issue.title,
			pcfg.IssueTracker.Owner, pcfg.IssueTracker.Repo, aiOutput)
		pr, err := gh.CreatePR(ctx, pcfg.GitHub.Owner, pcfg.GitHub.Repo, prTitle, prBody, branchName, baseBranch)
		if err != nil {
			logf("错误：创建 PR 失败：%v", err)
			a.releaseIssue(ctx, issue.id)
			return logBuf.String(), "failed"
		}
		logf("已创建 PR #%d", pr.Number)
		prNumber = pr.Number

		if err := gh.RequestCopilotReview(ctx, pcfg.GitHub.Owner, pcfg.GitHub.Repo, prNumber); err != nil {
			logf("警告：请求 Copilot review 失败：%v", err)
		}

		if _, err := a.DB.ExecContext(ctx,
			`INSERT INTO prs (project_id, issue_id, github_number, branch, status, title)
			 VALUES (?, ?, ?, ?, 'open', ?)
			 ON DUPLICATE KEY UPDATE status='open', title=VALUES(title)`,
			projectID, issue.id, prNumber, branchName, prTitle,
		); err != nil {
			logf("警告：插入 PR 记录失败：%v", err)
		}
	}

	// Increment fix_attempts
	if _, err := a.DB.ExecContext(ctx,
		`UPDATE issues SET fix_attempts = fix_attempts + 1 WHERE id = ?`,
		issue.id,
	); err != nil {
		logf("警告：更新修复次数失败：%v", err)
	}

	// Check if needs-human (configurable via MAX_ATTEMPTS rule, default 3)
	if issue.fixAttempts+1 >= fr.maxAttempts {
		if _, err := a.DB.ExecContext(ctx,
			`UPDATE issues SET status = 'needs-human' WHERE id = ? AND status = 'fixing'`,
			issue.id,
		); err != nil {
			logf("警告：设置需人工状态失败：%v", err)
		}
		_ = notify.Send(ctx, a.DB, userID, projectID, "fix_failed",
			fmt.Sprintf("⚠️ Issue #%d 修复失败 %d 次，需人工介入: %s", issue.githubNumber, issue.fixAttempts+1, issue.title),
		)
	}

	logf("修复完成，PR #%d", prNumber)
	return logBuf.String(), "success"
}

func (a *Agent) claimIssue(ctx context.Context, projectID int64, fr fixRules) (*issueRow, error) {
	var issue issueRow
	query := `SELECT id, github_number, title, fix_attempts FROM issues
	          WHERE project_id = ? AND status = 'open'`
	args := []interface{}{projectID}
	if fr.maxPriority > 0 {
		query += " AND priority <= ?"
		args = append(args, fr.maxPriority)
	}
	query += " ORDER BY priority ASC, fix_attempts ASC, id ASC LIMIT 1"
	err := a.DB.QueryRowContext(ctx, query, args...).Scan(
		&issue.id, &issue.githubNumber, &issue.title, &issue.fixAttempts)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	res, err := a.DB.ExecContext(ctx,
		`UPDATE issues SET status = 'fixing', fixing_since = NOW()
		 WHERE id = ? AND status = 'open'`,
		issue.id,
	)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, nil
	}
	return &issue, nil
}

func (a *Agent) releaseIssue(ctx context.Context, issueID int64) {
	_, _ = a.DB.ExecContext(ctx,
		`UPDATE issues SET status = 'open', fixing_since = NULL WHERE id = ? AND status = 'fixing'`,
		issueID,
	)
}

func (a *Agent) findExistingPR(ctx context.Context, projectID, issueID int64) (int, error) {
	var number int
	err := a.DB.QueryRowContext(ctx,
		`SELECT github_number FROM prs WHERE project_id = ? AND issue_id = ? AND status = 'open' LIMIT 1`,
		projectID, issueID,
	).Scan(&number)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return number, err
}

func buildPrompt(title, body, dirTree, prevFailures, promptOverride, rules string) (string, error) {
	tmpl := fixTmpl
	if promptOverride != "" {
		if t, err := template.New("fix_override").Parse(promptOverride); err == nil {
			tmpl = t
		} else {
			slog.Warn("fix: parse prompt override failed, using default", "err", err)
		}
	}
	var buf bytes.Buffer
	err := tmpl.Execute(&buf, promptData{
		IssueTitle:       title,
		IssueBody:        body,
		DirTree:          dirTree,
		PreviousFailures: prevFailures,
	})
	if err != nil {
		return "", err
	}
	if rules != "" {
		buf.WriteString("\n\n## Additional Rules\n")
		buf.WriteString(rules)
	}
	return buf.String(), nil
}

func buildPRBody(issueNumber int, issueTitle, issueOwner, issueRepo, aiOutput string) string {
	if len(aiOutput) > 2000 {
		aiOutput = aiOutput[:2000] + "\n... (truncated)"
	}
	return fmt.Sprintf(`## Fix for #%d: %s

**Root cause and changes:**
%s

Closes %s/%s#%d
`, issueNumber, issueTitle, aiOutput, issueOwner, issueRepo, issueNumber)
}
