// internal/agents/master/master.go
package master

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fixloop/fixloop/internal/agentrun"
	"github.com/fixloop/fixloop/internal/config"
	"github.com/fixloop/fixloop/internal/crypto"
	githubclient "github.com/fixloop/fixloop/internal/github"
	"github.com/fixloop/fixloop/internal/notify"
	"github.com/fixloop/fixloop/internal/playwright"
	"github.com/fixloop/fixloop/internal/ssrf"
	"github.com/fixloop/fixloop/internal/storage"
	"github.com/fixloop/fixloop/internal/vercel"
)

const playwrightBin = "/tmp/pw-test/node_modules/.bin/playwright"

// Agent runs the master loop for a single project.
type Agent struct {
	DB  *sql.DB
	Cfg *config.Config
	R2  *storage.R2Client
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
	Vercel struct {
		ProjectID     string `json:"project_id"`
		Token         string `json:"token"`
		StagingTarget string `json:"staging_target"`
	} `json:"vercel"`
	Test struct {
		StagingURL      string `json:"staging_url"`
		StagingAuthType string `json:"staging_auth_type"`
		StagingAuth     string `json:"staging_auth"`
	} `json:"test"`
}

type prRow struct {
	id           int64
	issueID      sql.NullInt64
	githubNumber int
	branch       string
	createdAt    time.Time
}

func (a *Agent) Run(ctx context.Context, projectID int64, projectAgentID int64) {
	// Load from project_agents
	var masterEnabled bool
	dbErr := a.DB.QueryRowContext(ctx,
		`SELECT enabled FROM project_agents WHERE id = ?`, projectAgentID,
	).Scan(&masterEnabled)
	if dbErr == nil && !masterEnabled {
		slog.Info("master: agent disabled in project_agents, skipping", "project_id", projectID)
		return
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
		slog.Error("master: load project", "project_id", projectID, "err", err)
		return
	}
	if status != "active" {
		return
	}

	var pcfg projectConf
	if err := json.Unmarshal([]byte(cfgJSON), &pcfg); err != nil {
		slog.Error("master: parse config", "project_id", projectID, "err", err)
		return
	}

	runID, err := agentrun.Start(ctx, a.DB, projectID, "master", configVersion, projectAgentID)
	if err != nil {
		slog.Error("master: start agentrun", "project_id", projectID, "err", err)
		return
	}

	agentrun.WithRecover(runID, a.DB, func() {
		output, finalStatus := a.runMaster(ctx, projectID, userID, runID, &pcfg)
		if err := agentrun.Finish(ctx, a.DB, runID, finalStatus, output); err != nil {
			slog.Error("master: finish agentrun", "run_id", runID, "err", err)
		}
	})
}

func (a *Agent) runMaster(ctx context.Context, projectID, userID, runID int64, pcfg *projectConf) (string, string) {
	var logBuf bytes.Buffer
	logf := func(msg string, args ...any) {
		line := fmt.Sprintf(msg, args...)
		logBuf.WriteString(line + "\n")
		slog.Info("master: "+line, "project_id", projectID, "run_id", runID)
	}

	// Step 1: Reset timed-out fixing issues
	a.resetTimedOutIssues(ctx, projectID)

	// Step 2: Find oldest open PR
	pr, err := a.findOldestOpenPR(ctx, projectID)
	if err != nil {
		logf("find open PR: %v", err)
		return logBuf.String(), "failed"
	}
	if pr == nil {
		logf("no open PRs, nothing to do")
		return logBuf.String(), "skipped"
	}
	logf("processing PR #%d (branch %s)", pr.githubNumber, pr.branch)

	// Decrypt PAT
	patEnc, err := hex.DecodeString(pcfg.GitHub.PAT)
	if err != nil {
		logf("ERROR: decode PAT hex: %v", err)
		return logBuf.String(), "failed"
	}
	pat, err := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, patEnc)
	if err != nil {
		logf("ERROR: decrypt PAT: %v", err)
		return logBuf.String(), "failed"
	}
	gh := githubclient.New(string(pat))

	// Step 3: Check reviews
	reviews, err := gh.ListPRReviews(ctx, pcfg.GitHub.Owner, pcfg.GitHub.Repo, pr.githubNumber)
	if err != nil {
		logf("WARN: list PR reviews: %v", err)
	}

	mergeable := false
	for _, r := range reviews {
		if r.State == "APPROVED" {
			mergeable = true
			break
		}
		if strings.Contains(strings.ToLower(r.User.Login), "copilot") && r.State == "COMMENTED" {
			mergeable = true
			break
		}
	}

	// 24h review timeout check
	if !mergeable && time.Since(pr.createdAt) > 24*time.Hour {
		logf("PR #%d awaiting review > 24h, notifying", pr.githubNumber)
		_ = notify.Send(ctx, a.DB, userID, projectID, "review_timeout",
			fmt.Sprintf("🔔 PR #%d 等待 review 超过 24h，请检查", pr.githubNumber),
		)
		return logBuf.String(), "skipped"
	}
	if !mergeable {
		logf("PR #%d not yet mergeable, waiting", pr.githubNumber)
		return logBuf.String(), "skipped"
	}

	// Step 4: Squash merge
	mergeTitle := fmt.Sprintf("fix: squash merge PR #%d", pr.githubNumber)
	mergeSHA, err := gh.MergePR(ctx, pcfg.GitHub.Owner, pcfg.GitHub.Repo, pr.githubNumber, mergeTitle)
	if err != nil {
		logf("ERROR: merge PR #%d: %v", pr.githubNumber, err)
		return logBuf.String(), "failed"
	}
	shortSHA := mergeSHA
	if len(shortSHA) > 8 {
		shortSHA = shortSHA[:8]
	}
	logf("merged PR #%d, SHA=%s", pr.githubNumber, shortSHA)

	// Update prs.status = merged
	_, _ = a.DB.ExecContext(ctx,
		`UPDATE prs SET status = 'merged', merged_at = NOW(), merged_by = 'Master Agent' WHERE id = ?`, pr.id)

	_ = notify.Send(ctx, a.DB, userID, projectID, "pr_merged",
		fmt.Sprintf("✅ PR #%d 已 merge", pr.githubNumber),
	)

	// Step 5: Wait for Vercel deployment (if configured)
	if pcfg.Vercel.ProjectID != "" && pcfg.Vercel.Token != "" {
		logf("waiting for Vercel deployment (SHA=%s)...", shortSHA)
		vercelTokenEnc, _ := hex.DecodeString(pcfg.Vercel.Token)
		vercelToken, err := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, vercelTokenEnc)
		if err != nil {
			logf("ERROR: decrypt vercel token: %v", err)
			return logBuf.String(), "failed"
		}
		dep, err := vercel.WaitForDeployment(ctx, string(vercelToken), pcfg.Vercel.ProjectID, mergeSHA)
		if err != nil {
			logf("ERROR: vercel deploy: %v", err)
			_ = notify.Send(ctx, a.DB, userID, projectID, "deploy_failed",
				fmt.Sprintf("🔥 PR #%d Vercel 部署失败/超时: %v", pr.githubNumber, err),
			)
			if pr.issueID.Valid {
				a.reopenIssue(ctx, pr.issueID.Int64, pr.id)
			}
			return logBuf.String(), "failed"
		}
		if dep != nil {
			logf("Vercel deployment READY: %s", dep.UID)
		}
	}

	// Step 6: Playwright acceptance test
	if pcfg.Test.StagingURL == "" {
		logf("no staging_url, closing issue without acceptance test")
		if pr.issueID.Valid {
			a.closeIssueSuccess(ctx, projectID, userID, pr, pcfg, gh)
		}
		if err := gh.DeleteBranch(ctx, pcfg.GitHub.Owner, pcfg.GitHub.Repo, pr.branch); err != nil {
			logf("WARN: delete branch %s: %v", pr.branch, err)
		}
		return logBuf.String(), "success"
	}

	if err := ssrf.ValidateHostname(extractHostname(pcfg.Test.StagingURL)); err != nil {
		logf("WARN: staging_url SSRF check failed: %v", err)
		return logBuf.String(), "failed"
	}

	locked, err := playwright.AcquireLock(a.DB, projectID)
	if err != nil || !locked {
		logf("WARN: playwright lock busy, skipping acceptance test this run")
		return logBuf.String(), "skipped"
	}
	defer playwright.ReleaseLock(a.DB, projectID) //nolint:errcheck

	screenshotDir := filepath.Join("/tmp/screenshots", fmt.Sprintf("%d/%d", userID, projectID))
	_ = os.MkdirAll(screenshotDir, 0755)
	defer os.RemoveAll(screenshotDir)

	var authCfg *playwright.AuthConfig
	if pcfg.Test.StagingAuth != "" {
		authEnc, _ := hex.DecodeString(pcfg.Test.StagingAuth)
		authJSON, _ := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, authEnc)
		authCfg = parseAuthConfig(authJSON)
	}

	exec := &playwright.Executor{
		PlaywrightBin: playwrightBin,
		StagingURL:    pcfg.Test.StagingURL,
		StagingAuth:   authCfg,
		ScreenshotDir: screenshotDir,
	}

	// Run 3 seed checks
	passed := true
	var failMsg string
	for i := 0; i < 3; i++ {
		result, err := exec.RunSeedCheck(ctx, i)
		if err != nil {
			passed = false
			failMsg = fmt.Sprintf("seed check %d error: %v", i, err)
			logf("acceptance seed check %d ERROR: %v", i, err)
			break
		}
		if result != nil && !result.Passed {
			passed = false
			failMsg = result.ErrorMsg
			logf("acceptance seed check %d FAILED: %s", i, failMsg)
			break
		}
	}

	if passed {
		logf("acceptance test PASSED")
		if pr.issueID.Valid {
			a.closeIssueSuccess(ctx, projectID, userID, pr, pcfg, gh)
		}
		if err := gh.DeleteBranch(ctx, pcfg.GitHub.Owner, pcfg.GitHub.Repo, pr.branch); err != nil {
			logf("WARN: delete branch %s: %v", pr.branch, err)
		}
		return logBuf.String(), "success"
	}

	logf("acceptance test FAILED: %s", failMsg)
	if pr.issueID.Valid {
		a.acceptanceFailure(ctx, projectID, userID, pr, failMsg)
	}
	return logBuf.String(), "failed"
}

func (a *Agent) closeIssueSuccess(ctx context.Context, projectID, userID int64, pr *prRow, pcfg *projectConf, gh *githubclient.Client) {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		slog.Error("master: begin close tx", "err", err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx,
		`UPDATE issues SET status = 'closed', closed_at = NOW() WHERE id = ?`,
		pr.issueID.Int64,
	); err != nil {
		slog.Error("master: close issue in tx", "err", err)
		return
	}
	if _, err = tx.ExecContext(ctx,
		`UPDATE backlog SET status = 'pending', last_tested_at = NULL
		 WHERE project_id = ? AND related_issue_id = ? AND status = 'failed'`,
		projectID, pr.issueID.Int64,
	); err != nil {
		slog.Error("master: reset backlog in tx", "err", err)
		return
	}
	if err = tx.Commit(); err != nil {
		slog.Error("master: commit close tx", "err", err)
		return
	}
	committed = true

	var ghIssueNumber int
	_ = a.DB.QueryRowContext(ctx, `SELECT github_number FROM issues WHERE id = ?`, pr.issueID.Int64).Scan(&ghIssueNumber)
	if ghIssueNumber > 0 {
		_ = gh.CloseIssue(ctx, pcfg.IssueTracker.Owner, pcfg.IssueTracker.Repo, ghIssueNumber,
			fmt.Sprintf("✅ Automatically fixed and verified. Fix PR: #%d", pr.githubNumber),
		)
	}
	_ = notify.Send(ctx, a.DB, userID, projectID, "issue_closed",
		fmt.Sprintf("🎉 Issue #%d 已关闭，线上验收通过", ghIssueNumber),
	)
}

func (a *Agent) acceptanceFailure(ctx context.Context, projectID, userID int64, pr *prRow, failMsg string) {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		slog.Error("master: begin failure tx", "err", err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var acceptFailures int
	if err := tx.QueryRowContext(ctx,
		`SELECT accept_failures FROM issues WHERE id = ? FOR UPDATE`,
		pr.issueID.Int64,
	).Scan(&acceptFailures); err != nil {
		slog.Error("master: lock issue row", "err", err)
		return
	}

	newStatus := "open"
	if acceptFailures+1 >= 5 {
		newStatus = "needs-human"
	}
	if _, err = tx.ExecContext(ctx,
		`UPDATE issues SET status = ?, accept_failures = accept_failures + 1, fixing_since = NULL WHERE id = ?`,
		newStatus, pr.issueID.Int64,
	); err != nil {
		slog.Error("master: update issue on failure", "err", err)
		return
	}
	if _, err = tx.ExecContext(ctx, `UPDATE prs SET status = 'closed' WHERE id = ?`, pr.id); err != nil {
		slog.Error("master: close pr on failure", "err", err)
		return
	}
	if err = tx.Commit(); err != nil {
		slog.Error("master: commit failure tx", "err", err)
		return
	}
	committed = true

	var ghIssueNumber int
	_ = a.DB.QueryRowContext(ctx, `SELECT github_number FROM issues WHERE id = ?`, pr.issueID.Int64).Scan(&ghIssueNumber)

	if newStatus == "needs-human" {
		_ = notify.Send(ctx, a.DB, userID, projectID, "needs_human",
			fmt.Sprintf("⚠️ Issue #%d 验收失败 %d 次，需人工介入", ghIssueNumber, acceptFailures+1),
		)
	} else {
		_ = notify.Send(ctx, a.DB, userID, projectID, "acceptance_failed",
			fmt.Sprintf("⚠️ Issue #%d 验收失败，已回滚到 open: %.200s", ghIssueNumber, failMsg),
		)
	}
}

func (a *Agent) resetTimedOutIssues(ctx context.Context, projectID int64) {
	_, err := a.DB.ExecContext(ctx,
		`UPDATE issues SET status = 'open', fixing_since = NULL
		 WHERE project_id = ? AND status = 'fixing'
		   AND fixing_since < NOW() - INTERVAL 2 HOUR
		   AND id NOT IN (
		     SELECT issue_id FROM prs
		     WHERE project_id = ? AND status = 'open' AND issue_id IS NOT NULL
		   )`,
		projectID, projectID,
	)
	if err != nil {
		slog.Warn("master: reset timed out issues", "project_id", projectID, "err", err)
	}
}

func (a *Agent) findOldestOpenPR(ctx context.Context, projectID int64) (*prRow, error) {
	var pr prRow
	err := a.DB.QueryRowContext(ctx,
		`SELECT id, issue_id, github_number, branch, created_at FROM prs
		 WHERE project_id = ? AND status = 'open'
		 ORDER BY created_at ASC LIMIT 1`,
		projectID,
	).Scan(&pr.id, &pr.issueID, &pr.githubNumber, &pr.branch, &pr.createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &pr, err
}

func (a *Agent) reopenIssue(ctx context.Context, issueID, prID int64) {
	_, _ = a.DB.ExecContext(ctx,
		`UPDATE issues SET status = 'open', fixing_since = NULL WHERE id = ? AND status = 'fixing'`,
		issueID,
	)
	_, _ = a.DB.ExecContext(ctx, `UPDATE prs SET status = 'closed' WHERE id = ?`, prID)
}

func parseAuthConfig(raw []byte) *playwright.AuthConfig {
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return &playwright.AuthConfig{
		Type:     m["type"],
		Username: m["username"],
		Password: m["password"],
		Name:     m["name"],
		Value:    m["value"],
	}
}

func extractHostname(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/:"); i >= 0 {
		s = s[:i]
	}
	return s
}
