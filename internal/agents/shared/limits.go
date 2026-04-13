package shared

import (
	"context"
	"database/sql"

	"github.com/fixloop/fixloop/internal/scheduler"
)

// ExceedsDailyLimit returns true if the agent has reached its daily run quota.
// Returns false for manually forced runs (scheduler.ForcedRunKey in ctx).
func ExceedsDailyLimit(ctx context.Context, db *sql.DB, projectID int64, agentType string, limit int) bool {
	if forced, _ := ctx.Value(scheduler.ForcedRunKey).(bool); forced {
		return false
	}
	var count int
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_runs
		 WHERE project_id = ? AND agent_type = ? AND started_at > NOW() - INTERVAL 24 HOUR`,
		projectID, agentType,
	).Scan(&count)
	return count >= limit
}
