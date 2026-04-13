// internal/agents/generic/generic.go
package generic

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/fixloop/fixloop/internal/agentrun"
	"github.com/fixloop/fixloop/internal/agents/shared"
	"github.com/fixloop/fixloop/internal/config"
	"github.com/fixloop/fixloop/internal/crypto"
	"github.com/fixloop/fixloop/internal/gitops"
	"github.com/fixloop/fixloop/internal/runner"
)

// Agent runs a fully prompt-driven AI agent for a project.
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
	SSHPrivateKey string `json:"ssh_private_key"`
	AIRunner      string `json:"ai_runner"`
	AIModel       string `json:"ai_model"`
	AIAPIBase     string `json:"ai_api_base"`
	AIAPIKey      string `json:"ai_api_key"`
}

func (a *Agent) Run(ctx context.Context, projectID, projectAgentID int64) {
	var agentName, agentAlias string
	var promptOverride, rules sql.NullString
	var enabled bool
	var dailyLimit int
	err := a.DB.QueryRowContext(ctx,
		`SELECT name, alias, prompt_override, rules, enabled, daily_limit
		 FROM project_agents WHERE id = ?`,
		projectAgentID,
	).Scan(&agentName, &agentAlias, &promptOverride, &rules, &enabled, &dailyLimit)
	if err != nil {
		slog.Error("generic: load agent config", "project_agent_id", projectAgentID, "err", err)
		return
	}
	if !enabled {
		slog.Info("generic: agent disabled", "project_agent_id", projectAgentID)
		return
	}
	if dailyLimit <= 0 {
		dailyLimit = 10
	}
	if !promptOverride.Valid || promptOverride.String == "" {
		slog.Warn("generic: no prompt configured, skipping", "project_agent_id", projectAgentID)
		return
	}

	var cfgJSON string
	var configVersion int
	var userID int64
	var status string
	if err := a.DB.QueryRowContext(ctx,
		`SELECT user_id, config, config_version, status FROM projects WHERE id = ? AND deleted_at IS NULL`,
		projectID,
	).Scan(&userID, &cfgJSON, &configVersion, &status); err != nil {
		slog.Error("generic: load project", "project_id", projectID, "err", err)
		return
	}
	if status != "active" {
		return
	}
	var pcfg projectConf
	if err := json.Unmarshal([]byte(cfgJSON), &pcfg); err != nil {
		slog.Error("generic: parse config", "project_id", projectID, "err", err)
		return
	}

	if shared.ExceedsDailyLimit(ctx, a.DB, projectID, "generic", dailyLimit) {
		slog.Info("generic: daily run limit reached", "project_agent_id", projectAgentID)
		return
	}

	runID, err := agentrun.Start(ctx, a.DB, projectID, "generic", configVersion, projectAgentID)
	if err != nil {
		slog.Error("generic: start agentrun", "project_id", projectID, "err", err)
		return
	}

	var output bytes.Buffer
	finalStatus := "success"
	agentrun.WithRecover(runID, a.DB, func() {
		a.runGeneric(ctx, projectID, userID, runID, agentName, agentAlias,
			promptOverride.String, rules, pcfg, &output, &finalStatus)
		_ = agentrun.Finish(ctx, a.DB, runID, finalStatus, output.String())
	})
}

func (a *Agent) runGeneric(
	ctx context.Context,
	projectID, userID, runID int64,
	agentName, agentAlias, prompt string,
	rules sql.NullString,
	pcfg projectConf,
	output *bytes.Buffer,
	finalStatus *string,
) {
	logf := func(msg string, args ...any) {
		line := fmt.Sprintf(msg, args...)
		output.WriteString(line + "\n")
		slog.Info("generic: "+line, "project_id", projectID, "run_id", runID, "alias", agentAlias)
	}

	sshKeyEnc, err := hex.DecodeString(pcfg.SSHPrivateKey)
	if err != nil {
		logf("错误：SSH 密钥解码失败：%v", err)
		*finalStatus = "failed"
		return
	}
	sshKey, err := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, sshKeyEnc)
	if err != nil {
		logf("错误：SSH 密钥解密失败：%v", err)
		*finalStatus = "failed"
		return
	}

	patEnc, err := hex.DecodeString(pcfg.GitHub.PAT)
	if err != nil {
		logf("错误：PAT 解码失败：%v", err)
		*finalStatus = "failed"
		return
	}
	pat, err := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, patEnc)
	if err != nil {
		logf("错误：PAT 解密失败：%v", err)
		*finalStatus = "failed"
		return
	}

	baseBranch := pcfg.GitHub.FixBaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}
	repoPath := gitops.AgentRepoPath(a.Cfg.WorkspaceDir, pcfg.GitHub.Owner, pcfg.GitHub.Repo, agentAlias)
	logf("准备本地仓库：%s", repoPath)
	if err := gitops.EnsureRepo(ctx, sshKey, pcfg.GitHub.Owner, pcfg.GitHub.Repo, repoPath, baseBranch); err != nil {
		logf("错误：仓库初始化失败：%v", err)
		*finalStatus = "failed"
		return
	}

	branchName := fmt.Sprintf("custom/%s", agentAlias)
	logf("切换到分支：%s", branchName)
	if err := gitops.EnsureBranch(ctx, sshKey, repoPath, branchName, baseBranch); err != nil {
		logf("错误：分支切换失败：%v", err)
		*finalStatus = "failed"
		return
	}

	dirTree := gitops.DirTree(repoPath, 3)
	finalPrompt := buildPrompt(prompt, rules, dirTree)
	output.WriteString("\n--- PROMPT ---\n" + finalPrompt + "\n--- END PROMPT ---\n")

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
		*finalStatus = "failed"
		return
	}

	logf("启动 AI（运行器=%s，Agent=%s）", pcfg.AIRunner, agentAlias)
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	aiOutput, err := r.Run(runCtx, repoPath, finalPrompt)
	output.WriteString("\n--- AI OUTPUT ---\n" + aiOutput + "\n--- END AI OUTPUT ---\n")
	if err != nil {
		logf("错误：AI 运行失败：%v", err)
		*finalStatus = "failed"
		return
	}

	hasChanges, err := gitops.HasChanges(ctx, repoPath)
	if err != nil {
		logf("错误：检查文件变更失败：%v", err)
		*finalStatus = "failed"
		return
	}
	if !hasChanges {
		logf("AI 未产生任何文件改动")
		*finalStatus = "skipped"
		return
	}

	commitMsg := fmt.Sprintf("chore(%s): automated run #%d", agentAlias, runID)
	prTitle := fmt.Sprintf("chore(%s): automated run #%d", agentAlias, runID)
	prBody := fmt.Sprintf("Automated changes by generic agent `%s` (run #%d).\n\n**AI Output:**\n%s",
		agentName, runID, truncate(aiOutput, 2000))
	// force=true so re-runs on the same branch overwrite cleanly
	prNumber, err := shared.CommitPushPR(ctx, a.DB, projectID,
		sshKey, pat, repoPath, branchName,
		commitMsg, prTitle, prBody,
		pcfg.GitHub.Owner, pcfg.GitHub.Repo, baseBranch, true)
	if err != nil {
		logf("错误：提交/推送/PR 失败：%v", err)
		*finalStatus = "failed"
		return
	}
	logf("自定义 Agent 运行完成，PR #%d", prNumber)
}

func buildPrompt(promptOverride string, rules sql.NullString, dirTree string) string {
	var sb bytes.Buffer
	sb.WriteString(promptOverride)
	if rules.Valid && rules.String != "" {
		sb.WriteString("\n\n## Rules\n")
		sb.WriteString(rules.String)
	}
	sb.WriteString("\n\n## Repository Structure\n")
	sb.WriteString(dirTree)
	return sb.String()
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "\n... (truncated)"
}
