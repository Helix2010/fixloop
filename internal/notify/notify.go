// internal/notify/notify.go
package notify

import (
	"context"
	"database/sql"
	"fmt"
)

// Send inserts a notification row. projectID may be 0 (system-level notification).
// If a TG sender is wired later it will pick up rows with tg_sent=false.
func Send(ctx context.Context, db *sql.DB, userID, projectID int64, notifType, content string) error {
	var pid any
	if projectID != 0 {
		pid = projectID
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO notifications (user_id, project_id, type, content, tg_sent)
		 VALUES (?, ?, ?, ?, FALSE)`,
		userID, pid, notifType, content,
	)
	if err != nil {
		return fmt.Errorf("notify.Send: %w", err)
	}
	return nil
}
