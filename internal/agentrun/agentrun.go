package agentrun

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/acarl005/stripansi"
)

// Start inserts a new agent_run row with status='running' and returns its ID.
func Start(ctx context.Context, db *sql.DB, projectID int64, agentType string, configVersion int, projectAgentID int64) (runID int64, err error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO agent_runs (project_id, agent_type, config_version, status, started_at, project_agent_id)
		 VALUES (?, ?, ?, 'running', NOW(), NULLIF(?, 0))`,
		projectID, agentType, configVersion, projectAgentID,
	)
	if err != nil {
		return 0, fmt.Errorf("agentrun.Start: %w", err)
	}
	runID, err = res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("agentrun.Start LastInsertId: %w", err)
	}
	return runID, nil
}

// extractSummary returns the last meaningful line of the output, capped at 200 runes.
func extractSummary(output string) string {
	s := strings.TrimSpace(output)
	for s != "" {
		i := strings.LastIndexByte(s, '\n')
		var line string
		if i < 0 {
			line = strings.TrimSpace(s)
			s = ""
		} else {
			line = strings.TrimSpace(s[i+1:])
			s = s[:i]
		}
		if line == "" {
			continue
		}
		// Strip common log prefixes like "explore: " or "master: "
		if idx := strings.Index(line, ": "); idx > 0 && idx < 20 {
			line = line[idx+2:]
		}
		if utf8.RuneCountInString(line) > 200 {
			runes := []rune(line)
			line = string(runes[:200]) + "…"
		}
		return line
	}
	return ""
}

// Finish updates the agent_run status and stores the (ANSI-stripped) output.
// Both writes happen inside a single transaction.
func Finish(ctx context.Context, db *sql.DB, runID int64, status string, output string) error {
	output = stripansi.Strip(output)
	summary := extractSummary(output)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("agentrun.Finish begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	_, err = tx.ExecContext(ctx,
		`UPDATE agent_runs SET status=?, finished_at=NOW(), summary=? WHERE id=?`,
		status, summary, runID,
	)
	if err != nil {
		return fmt.Errorf("agentrun.Finish update: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO agent_run_outputs (run_id, output) VALUES (?, ?)
		 ON DUPLICATE KEY UPDATE output=VALUES(output)`,
		runID, output,
	)
	if err != nil {
		return fmt.Errorf("agentrun.Finish insert output: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("agentrun.Finish commit: %w", err)
	}
	return nil
}

// AbandonZombies marks any run that has been 'running' for more than one hour as 'abandoned'.
func AbandonZombies(db *sql.DB) error {
	res, err := db.Exec(
		`UPDATE agent_runs SET status='abandoned', finished_at=NOW()
		 WHERE status='running' AND started_at < NOW() - INTERVAL 1 HOUR`,
	)
	if err != nil {
		return fmt.Errorf("agentrun.AbandonZombies: %w", err)
	}
	n, _ := res.RowsAffected()
	slog.Info("agentrun.AbandonZombies", "abandoned", n)
	return nil
}

// WithRecover runs fn in the current goroutine, recovering from any panic.
// If a panic occurs it logs an error and calls Finish with status="failed".
func WithRecover(runID int64, db *sql.DB, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("agentrun panic recovered",
				"run_id", runID,
				"unexpected", true,
				"panic", fmt.Sprintf("%v", r),
			)
			_ = Finish(context.Background(), db, runID, "failed", fmt.Sprintf("panic: %v", r))
		}
	}()
	fn()
}
