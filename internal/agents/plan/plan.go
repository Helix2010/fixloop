// internal/plan/plan.go
package plan

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"text/template"
	"unicode"

	_ "embed"

	"github.com/fixloop/fixloop/internal/agentrun"
	"github.com/fixloop/fixloop/internal/config"
	"github.com/fixloop/fixloop/internal/crypto"
	"github.com/fixloop/fixloop/internal/runner"
)

//go:embed prompts/plan.txt
var planPromptTmpl string

var planTmpl = template.Must(template.New("plan").Parse(planPromptTmpl))

// DefaultPrompt returns the built-in plan prompt template text.
func DefaultPrompt() string { return planPromptTmpl }

// Agent runs the plan loop for a single project.
type Agent struct {
	DB  *sql.DB
	Cfg *config.Config
}

type projectConf struct {
	GitHub struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
	} `json:"github"`
	Test struct {
		StagingURL string `json:"staging_url"`
	} `json:"test"`
	AIRunner  string `json:"ai_runner"`
	AIModel   string `json:"ai_model"`
	AIAPIKey  string `json:"ai_api_key"`  // hex(AES encrypted)
	AIAPIBase string `json:"ai_api_base"` // for aider / custom endpoint
}

type scenarioSuggestion struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	ScenarioType string `json:"scenario_type"`
	Priority     int    `json:"priority"`
}

type promptVars struct {
	Owner        string
	Repo         string
	StagingURL   string
	PendingCount int
	RecentIssues string
	Count        int
}

func (a *Agent) Run(ctx context.Context, projectID int64, projectAgentID int64) {
	// Load from project_agents
	var planEnabled bool
	var promptOverrideDB, rulesDB sql.NullString
	err := a.DB.QueryRowContext(ctx,
		`SELECT enabled, prompt_override, rules FROM project_agents WHERE id = ?`, projectAgentID,
	).Scan(&planEnabled, &promptOverrideDB, &rulesDB)
	if err == nil && !planEnabled {
		slog.Info("plan: agent disabled in project_agents, skipping", "project_id", projectID)
		return
	}

	var cfgJSON string
	var configVersion int
	var status string
	if err := a.DB.QueryRowContext(ctx,
		`SELECT config, config_version, status FROM projects WHERE id = ? AND deleted_at IS NULL`,
		projectID,
	).Scan(&cfgJSON, &configVersion, &status); err != nil {
		slog.Error("plan: load project", "project_id", projectID, "err", err)
		return
	}
	if status != "active" {
		return
	}

	var pcfg projectConf
	if err := json.Unmarshal([]byte(cfgJSON), &pcfg); err != nil {
		slog.Error("plan: parse config", "project_id", projectID, "err", err)
		return
	}

	runID, err := agentrun.Start(ctx, a.DB, projectID, "plan", configVersion, projectAgentID)
	if err != nil {
		slog.Error("plan: start agentrun", "err", err)
		return
	}

	promptOverride := ""
	if promptOverrideDB.Valid {
		promptOverride = promptOverrideDB.String
	}
	rules := ""
	if rulesDB.Valid {
		rules = rulesDB.String
	}
	agentrun.WithRecover(runID, a.DB, func() {
		output, finalStatus := a.runPlan(ctx, projectID, runID, &pcfg, promptOverride, rules)
		_ = agentrun.Finish(ctx, a.DB, runID, finalStatus, output)
	})
}

func (a *Agent) runPlan(ctx context.Context, projectID, runID int64, pcfg *projectConf, promptOverride, rules string) (string, string) {
	var logBuf bytes.Buffer
	logf := func(msg string, args ...any) {
		line := fmt.Sprintf(msg, args...)
		logBuf.WriteString(line + "\n")
		slog.Info("plan: "+line, "project_id", projectID)
	}

	// Count pending backlog items
	var pendingCount int
	if err := a.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM backlog WHERE project_id = ? AND status = 'pending'`, projectID,
	).Scan(&pendingCount); err != nil {
		logf("ERROR: count pending backlog: %v", err)
		return logBuf.String(), "failed"
	}

	// Skip if backlog is healthy
	if pendingCount > 10 {
		logf("backlog healthy (%d pending), skipping", pendingCount)
		return logBuf.String(), "skipped"
	}

	// Get recent open issues for context
	var issueLines []string
	rows, err := a.DB.QueryContext(ctx,
		`SELECT title FROM issues WHERE project_id = ? AND status IN ('open','fixing')
		 ORDER BY created_at DESC LIMIT 5`,
		projectID,
	)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var t string
			if err := rows.Scan(&t); err == nil {
				issueLines = append(issueLines, "- "+t)
			}
		}
		_ = rows.Err() // informational context; log and continue
	}

	// Build AI runner
	apiKey := ""
	if pcfg.AIAPIKey != "" {
		if keyEnc, err := hex.DecodeString(pcfg.AIAPIKey); err == nil {
			if plain, err := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, keyEnc); err == nil {
				apiKey = string(plain)
			}
		}
	}
	model := pcfg.AIModel
	if model == "" {
		model = "claude-opus-4-6"
	}

	// Build prompt
	want := 5
	activeTmpl := planTmpl
	if promptOverride != "" {
		t, err := template.New("plan_override").Parse(promptOverride)
		if err != nil {
			logf("WARN: parse plan prompt override: %v — using default", err)
		} else {
			activeTmpl = t
		}
	}
	var buf bytes.Buffer
	if err := activeTmpl.Execute(&buf, promptVars{
		Owner:        pcfg.GitHub.Owner,
		Repo:         pcfg.GitHub.Repo,
		StagingURL:   pcfg.Test.StagingURL,
		PendingCount: pendingCount,
		RecentIssues: strings.Join(issueLines, "\n"),
		Count:        want,
	}); err != nil {
		logf("ERROR: build prompt: %v", err)
		return logBuf.String(), "failed"
	}
	if rules != "" {
		buf.WriteString("\n\n## Additional Rules\n")
		buf.WriteString(rules)
	}
	prompt := buf.String()

	logf("generating %d new backlog scenarios via AI", want)

	r, err := runner.New(pcfg.AIRunner, model, pcfg.AIAPIBase, apiKey)
	if err != nil {
		r = &runner.ClaudeCLIRunner{Model: model}
	}
	aiOut, err := r.Run(ctx, "", prompt)
	logBuf.WriteString("\n--- AI OUTPUT ---\n" + aiOut + "\n---\n")
	if err != nil {
		logf("ERROR: AI runner: %v", err)
		return logBuf.String(), "failed"
	}

	// Parse JSON array from response
	suggestions, err := parseScenarios(aiOut)
	if err != nil {
		logf("WARN: parse scenarios: %v", err)
		return logBuf.String(), "failed"
	}

	inserted := 0
	for _, s := range suggestions {
		if s.Title == "" {
			continue
		}
		if s.ScenarioType == "" {
			s.ScenarioType = "ui"
		}
		if s.Priority == 0 {
			s.Priority = 2
		}
		hash := titleHash(s.Title)
		_, err := a.DB.ExecContext(ctx,
			`INSERT IGNORE INTO backlog
			 (project_id, title, title_hash, description, scenario_type, priority, status, source)
			 VALUES (?, ?, ?, ?, ?, ?, 'pending', 'plan')`,
			projectID, s.Title, hash, s.Description, s.ScenarioType, s.Priority,
		)
		if err != nil {
			logf("WARN: insert backlog row: %v", err)
			continue
		}
		inserted++
	}

	logf("inserted %d new backlog scenarios", inserted)
	return logBuf.String(), "success"
}

func parseScenarios(raw string) ([]scenarioSuggestion, error) {
	start := strings.Index(raw, "[")
	end := strings.LastIndex(raw, "]")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array found in AI response")
	}
	var result []scenarioSuggestion
	if err := json.Unmarshal([]byte(raw[start:end+1]), &result); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	return result, nil
}

func titleHash(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || unicode.Is(unicode.Han, r) {
			b.WriteRune(r)
		}
	}
	h := sha1.Sum([]byte(b.String()))
	return fmt.Sprintf("%x", h)[:40]
}
