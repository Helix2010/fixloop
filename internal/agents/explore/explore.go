package explore

import (
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
	"github.com/fixloop/fixloop/internal/agents/shared"
	"github.com/fixloop/fixloop/internal/config"
	"github.com/fixloop/fixloop/internal/crypto"
	githubclient "github.com/fixloop/fixloop/internal/github"
	"github.com/fixloop/fixloop/internal/playwright"
	"github.com/fixloop/fixloop/internal/ssrf"
	"github.com/fixloop/fixloop/internal/storage"
)

const playwrightBin = "/tmp/pw-test/node_modules/.bin/playwright"

// Agent runs the explore loop for a single project.
type Agent struct {
	DB  *sql.DB
	Cfg *config.Config
	R2  *storage.R2Client // nil = no screenshot upload
}

// projectConf is the minimal subset of stored project config needed here.
type projectConf struct {
	GitHub struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		PAT   string `json:"pat"` // hex(AES encrypted)
	} `json:"github"`
	IssueTracker struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
	} `json:"issue_tracker"`
	SSHPrivateKey string `json:"ssh_private_key"`
	Test          struct {
		StagingURL      string `json:"staging_url"`
		StagingAuthType string `json:"staging_auth_type"`
		StagingAuth     string `json:"staging_auth"` // hex(AES encrypted JSON)
	} `json:"test"`
}

type backlogRow struct {
	id          int64
	title       string
	description string
	testSteps   []byte // raw JSON, may be nil for seed scenarios
	source      string // 'seed'|'plan'|'auto_expand'
	priority    int
}

// Run executes the explore-agent for projectID.
func (a *Agent) Run(ctx context.Context, projectID int64, projectAgentID int64) {
	// Load from project_agents
	var exploreEnabled bool
	var agentRules sql.NullString
	var dailyLimit int
	err := a.DB.QueryRowContext(ctx,
		`SELECT enabled, rules, daily_limit FROM project_agents WHERE id = ?`, projectAgentID,
	).Scan(&exploreEnabled, &agentRules, &dailyLimit)
	if err == nil && !exploreEnabled {
		slog.Info("explore: agent disabled in project_agents, skipping", "project_id", projectID)
		return
	}
	if dailyLimit <= 0 {
		dailyLimit = 30 // safety default
	}
	priorityRules := ""
	if agentRules.Valid {
		priorityRules = agentRules.String
	}

	// Load project config
	var (
		cfgJSON       string
		configVersion int
		userID        int64
		status        string
	)
	err = a.DB.QueryRowContext(ctx,
		`SELECT user_id, config, config_version, status FROM projects WHERE id = ? AND deleted_at IS NULL`,
		projectID,
	).Scan(&userID, &cfgJSON, &configVersion, &status)
	if err != nil {
		slog.Error("explore: load project failed", "project_id", projectID, "err", err)
		return
	}
	if status != "active" {
		slog.Info("explore: project not active, skipping", "project_id", projectID)
		return
	}

	var pcfg projectConf
	if err := json.Unmarshal([]byte(cfgJSON), &pcfg); err != nil {
		slog.Error("explore: parse project config failed", "project_id", projectID, "err", err)
		return
	}

	// staging_url required
	if pcfg.Test.StagingURL == "" {
		slog.Info("explore: no staging_url, skipping", "project_id", projectID)
		return
	}

	// SSRF re-check (防 DNS rebinding)
	if err := ssrf.ValidateHostname(ssrf.ExtractHostname(pcfg.Test.StagingURL)); err != nil {
		slog.Warn("explore: staging_url SSRF check failed", "project_id", projectID, "err", err)
		return
	}

	// Start agent_run record
	runID, err := agentrun.Start(ctx, a.DB, projectID, "explore", configVersion, projectAgentID)
	if err != nil {
		slog.Error("explore: start agent_run failed", "project_id", projectID, "err", err)
		return
	}

	var output strings.Builder
	finalStatus := "success"

	agentrun.WithRecover(runID, a.DB, func() {
		a.runLoop(ctx, projectID, userID, runID, configVersion, pcfg, priorityRules, dailyLimit, &output, &finalStatus)
		_ = agentrun.Finish(ctx, a.DB, runID, finalStatus, output.String())
	})
}

func (a *Agent) runLoop(
	ctx context.Context,
	projectID, userID, runID int64,
	configVersion int,
	pcfg projectConf,
	priorityRules string,
	dailyLimit int,
	output *strings.Builder,
	finalStatus *string,
) {
	logf := func(msg string, args ...any) {
		line := fmt.Sprintf(msg, args...)
		output.WriteString(line + "\n")
		slog.Info(line, "project_id", projectID, "run_id", runID)
	}

	// Acquire Playwright lock (one Playwright instance per project at a time)
	db := a.DB
	got, err := playwright.AcquireLock(db, projectID)
	if err != nil || !got {
		logf("探索：Playwright 锁被占用，跳过本次运行")
		*finalStatus = "skipped"
		return
	}
	defer playwright.ReleaseLock(db, projectID)

	// Daily run limit check — skipped for manually forced runs
	if shared.ExceedsDailyLimit(ctx, db, projectID, "explore", dailyLimit) {
		logf("探索：已达每日运行上限（%d）", dailyLimit)
		*finalStatus = "skipped"
		return
	}

	// Decrypt PAT
	pat, err := a.decryptHex(pcfg.GitHub.PAT)
	if err != nil {
		logf("探索：PAT 解密失败：%v", err)
		*finalStatus = "failed"
		return
	}

	// Decrypt staging auth if present
	var auth *playwright.AuthConfig
	if pcfg.Test.StagingAuth != "" {
		authJSON, err := a.decryptHex(pcfg.Test.StagingAuth)
		if err == nil {
			auth = shared.ParseAuthConfig(authJSON)
		}
	}

	// Prepare screenshot dir
	screenshotDir := fmt.Sprintf("/tmp/screenshots/%d/%d/%d", userID, projectID, runID)
	if err := os.MkdirAll(screenshotDir, 0750); err != nil {
		logf("探索：截图目录创建失败：%v", err)
	}
	defer os.RemoveAll(screenshotDir)

	// Pick up to 5 pending backlog scenarios
	scenarios, err := a.pickScenarios(ctx, projectID, 5)
	if err != nil {
		logf("探索：获取测试场景失败：%v", err)
		*finalStatus = "failed"
		return
	}
	if len(scenarios) == 0 {
		logf("探索：暂无待测场景")
		return
	}

	logf("探索：开始运行 %d 个测试场景", len(scenarios))

	ghClient := githubclient.New(string(pat))
	bugsFound := 0

	for _, sc := range scenarios {
		if err := ctx.Err(); err != nil {
			break
		}

		logf("场景 %d：%s", sc.id, sc.title)

		exec := &playwright.Executor{
			PlaywrightBin: playwrightBin,
			StagingURL:    pcfg.Test.StagingURL,
			StagingAuth:   auth,
			ScreenshotDir: screenshotDir,
			Timeout:       90 * time.Second,
		}

		var result *playwright.Result
		if sc.source == "seed" {
			result, err = exec.RunSeedCheck(ctx, seedCheckIndex(sc.title))
		} else {
			var steps []playwright.StepAction
			if len(sc.testSteps) > 0 {
				_ = json.Unmarshal(sc.testSteps, &steps)
			}
			result, err = exec.RunSteps(ctx, steps)
		}

		if err != nil {
			logf("场景 %d 执行出错：%v", sc.id, err)
			_ = a.markScenario(ctx, sc.id, "tested") // don't fail on executor error
			continue
		}

		if result.Passed {
			logf("场景 %d：通过 ✓", sc.id)
			_ = a.markScenario(ctx, sc.id, "tested")
			continue
		}

		// Bug found — upload screenshots and open Issue
		logf("场景 %d 失败：%s：%s", sc.id, result.ErrorType, result.ErrorMsg)
		bugsFound++

		screenshotURL := a.uploadScreenshots(ctx, result.Screenshots, userID, projectID, runID, sc.id, 0)

		priority := classifyPriority(sc.title, result.ErrorType, result.ErrorMsg, priorityRules)
		issueTitle := fmt.Sprintf("[AutoTest][P%d] %s", priority, sc.title)
		issueBody := buildIssueBody(sc, pcfg.Test.StagingURL, result, screenshotURL, runID)
		issue, err := ghClient.CreateIssue(ctx,
			pcfg.IssueTracker.Owner, pcfg.IssueTracker.Repo,
			issueTitle, issueBody, nil,
		)
		if err != nil {
			logf("探索：创建 Issue 失败：%v", err)
			_ = a.markScenario(ctx, sc.id, "failed")
			continue
		}

		_ = a.recordIssue(ctx, projectID, sc.id, issue.Number, issueTitle, priority)
		_ = a.markScenario(ctx, sc.id, "failed")
		logf("探索：已创建 Issue #%d", issue.Number)
	}

	logf("探索完成，发现 %d 个问题", bugsFound)

	// Auto-trigger plan if backlog is running low
	go a.checkBacklogThreshold(projectID)
}

// pickScenarios returns up to limit pending scenarios ordered by priority ASC, last_tested_at ASC (NULL first).
func (a *Agent) pickScenarios(ctx context.Context, projectID int64, limit int) ([]backlogRow, error) {
	rows, err := a.DB.QueryContext(ctx,
		`SELECT id, title, description, test_steps, source, priority
		 FROM backlog
		 WHERE project_id = ? AND status = 'pending'
		 ORDER BY priority ASC, last_tested_at ASC
		 LIMIT ?`,
		projectID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []backlogRow
	for rows.Next() {
		var sc backlogRow
		var testSteps []byte
		if err := rows.Scan(&sc.id, &sc.title, &sc.description, &testSteps, &sc.source, &sc.priority); err != nil {
			return nil, err
		}
		sc.testSteps = testSteps
		result = append(result, sc)
	}
	return result, rows.Err()
}

func (a *Agent) markScenario(ctx context.Context, scenarioID int64, status string) error {
	_, err := a.DB.ExecContext(ctx,
		`UPDATE backlog SET status = ?, last_tested_at = NOW() WHERE id = ?`,
		status, scenarioID,
	)
	return err
}

func (a *Agent) recordIssue(ctx context.Context, projectID, scenarioID int64, ghNumber int, title string, priority int) error {
	titleHash := titleHash(title)
	_, err := a.DB.ExecContext(ctx,
		`INSERT IGNORE INTO issues
		 (project_id, github_number, title, title_hash, priority, status)
		 VALUES (?, ?, ?, ?, ?, 'open')`,
		projectID, ghNumber, title, titleHash, priority,
	)
	return err
}

func (a *Agent) decryptHex(enc string) ([]byte, error) {
	raw, err := hex.DecodeString(enc)
	if err != nil {
		return nil, err
	}
	return crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, raw)
}

func (a *Agent) uploadScreenshots(ctx context.Context, paths []string, userID, projectID, runID, scenarioID int64, step int) string {
	if a.R2 == nil || a.R2.Disabled() || len(paths) == 0 {
		return ""
	}
	key := storage.KeyForScreenshot(userID, projectID, runID, scenarioID, step, time.Now())
	if err := a.R2.Upload(ctx, key, paths[0]); err != nil {
		slog.Warn("explore: screenshot upload failed", "err", err)
		return ""
	}
	// Return proxy URL — frontend will serve via /api/v1/projects/:id/screenshots/:run_id/:file
	return fmt.Sprintf("/api/v1/projects/%d/screenshots/%d/%s", projectID, runID, filepath.Base(key))
}

func (a *Agent) checkBacklogThreshold(projectID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var pendingCount int
	_ = a.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM backlog WHERE project_id = ? AND status = 'pending'`,
		projectID,
	).Scan(&pendingCount)

	if pendingCount < 5 {
		slog.Info("explore: backlog low, plan-agent trigger recommended", "project_id", projectID, "pending", pendingCount)
		// plan-agent will be triggered by scheduler; just log for now
	}
}

// ---- helpers ----

func seedCheckIndex(title string) int {
	t := strings.ToLower(title)
	switch {
	case strings.Contains(t, "console"):
		return 1
	case strings.Contains(t, "核心") || strings.Contains(t, "body"):
		return 2
	default:
		return 0
	}
}

// classifyPriority returns an integer priority (1=highest).
// If priorityRules is non-empty, each non-blank line is parsed as:
//
//	P<n>: keyword1, keyword2, ...
//
// The combined text of scenarioTitle+" "+errorType+" "+errorMsg is matched
// case-insensitively against each keyword; the first matching level wins.
// Including scenarioTitle lets business-level rules (e.g. "P1: 用户无法登录")
// match against the scenario description as well as the raw Playwright error.
// Falls back to hardcoded logic when no rules are configured or no rule matches.
func classifyPriority(scenarioTitle, errorType, errorMsg, priorityRules string) int {
	if priorityRules != "" {
		if len(errorMsg) > 2000 {
			errorMsg = errorMsg[:2000]
		}
		combined := strings.ToLower(scenarioTitle + " " + errorType + " " + errorMsg)
		for _, line := range strings.Split(priorityRules, "\n") {
			line = strings.TrimSpace(line)
			// expect format:  P<digit>: kw1, kw2, ...
			if len(line) < 4 || (line[0] != 'P' && line[0] != 'p') {
				continue
			}
			colonIdx := strings.Index(line, ":")
			if colonIdx < 2 {
				continue
			}
			levelStr := strings.TrimSpace(line[1:colonIdx])
			level := 0
			for _, ch := range levelStr {
				if ch >= '0' && ch <= '9' {
					level = level*10 + int(ch-'0')
				} else {
					level = -1
					break
				}
			}
			if level <= 0 {
				continue
			}
			keywords := strings.Split(line[colonIdx+1:], ",")
			for _, kw := range keywords {
				kw = strings.ToLower(strings.TrimSpace(kw))
				if kw != "" && strings.Contains(combined, kw) {
					return level
				}
			}
		}
	}
	// Default fallback
	switch errorType {
	case "crash":
		return 1
	default:
		return 2
	}
}

func buildIssueBody(sc backlogRow, stagingURL string, result *playwright.Result, screenshotURL string, runID int64) string {
	var sb strings.Builder
	sb.WriteString("## Bug Report\n\n")
	fmt.Fprintf(&sb, "**Scenario:** %s\n", sc.title)
	fmt.Fprintf(&sb, "**Staging URL:** %s\n", stagingURL)
	fmt.Fprintf(&sb, "**Error Type:** %s\n", result.ErrorType)
	sb.WriteString("**Error Message:**\n```\n")
	sb.WriteString(result.ErrorMsg)
	sb.WriteString("\n```\n\n")
	if screenshotURL != "" {
		fmt.Fprintf(&sb, "**Screenshot:** %s\n\n", screenshotURL)
	}
	if sc.description != "" {
		fmt.Fprintf(&sb, "**Description:** %s\n\n", sc.description)
	}
	fmt.Fprintf(&sb, "**Detected at:** %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&sb, "\n---\n*Auto-detected by FixLoop explore-agent (run #%d)*\n", runID)
	return sb.String()
}

func titleHash(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	norm := b.String()
	if len(norm) > 40 {
		norm = norm[:40]
	}
	return fmt.Sprintf("%-40s", norm)
}
