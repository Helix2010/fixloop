// internal/api/handlers/notifications.go
package handlers

import (
	"database/sql"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fixloop/fixloop/internal/api/response"
)

type NotificationHandler struct {
	DB *sql.DB
}

type notifResp struct {
	ID        int64      `json:"id"`
	ProjectID *int64     `json:"project_id,omitempty"`
	Type      string     `json:"type"`
	Content   string     `json:"content"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
	TGSent    bool       `json:"tg_sent"`
	CreatedAt time.Time  `json:"created_at"`
}

func (h *NotificationHandler) List(c *gin.Context) {
	userID := c.GetInt64("user_id")
	page, perPage := parsePagination(c)

	var total int64
	_ = h.DB.QueryRowContext(c.Request.Context(),
		`SELECT COUNT(*) FROM notifications WHERE user_id = ?`, userID,
	).Scan(&total)

	rows, err := h.DB.QueryContext(c.Request.Context(),
		`SELECT id, project_id, type, content, read_at, tg_sent, created_at
		 FROM notifications WHERE user_id = ?
		 ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		userID, perPage, (page-1)*perPage,
	)
	if err != nil {
		response.Internal(c)
		return
	}
	defer rows.Close()

	var items []notifResp
	for rows.Next() {
		var n notifResp
		var pid sql.NullInt64
		if err := rows.Scan(&n.ID, &pid, &n.Type, &n.Content, &n.ReadAt, &n.TGSent, &n.CreatedAt); err != nil {
			response.Internal(c)
			return
		}
		if pid.Valid {
			n.ProjectID = &pid.Int64
		}
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		response.Internal(c)
		return
	}
	if items == nil {
		items = []notifResp{}
	}
	response.OKPaged(c, items, response.Pagination{Page: page, PerPage: perPage, Total: total})
}

func (h *NotificationHandler) ReadAll(c *gin.Context) {
	userID := c.GetInt64("user_id")
	_, err := h.DB.ExecContext(c.Request.Context(),
		`UPDATE notifications SET read_at = NOW() WHERE user_id = ? AND read_at IS NULL`,
		userID,
	)
	if err != nil {
		response.Internal(c)
		return
	}
	response.OK(c, gin.H{"ok": true})
}
