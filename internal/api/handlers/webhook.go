package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/fixloop/fixloop/internal/api/response"
)

// WebhookHandler handles unauthenticated webhook trigger and token management.
type WebhookHandler struct {
	DB        *sql.DB
	Scheduler ProjectScheduler
}

// Trigger is the public (no-auth) endpoint for external callers.
// POST /webhook/projects/:project_id/trigger
// Header: X-Webhook-Token: <token>
// Body:   {"alias": "fix"}
func (h *WebhookHandler) Trigger(c *gin.Context) {
	projectID, err := strconv.ParseInt(c.Param("project_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "无效的 project_id")
		return
	}

	token := c.GetHeader("X-Webhook-Token")
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing X-Webhook-Token header"})
		return
	}

	var cfgJSON, status string
	err = h.DB.QueryRowContext(c.Request.Context(),
		`SELECT config, status FROM projects WHERE id = ? AND deleted_at IS NULL`,
		projectID,
	).Scan(&cfgJSON, &status)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return
	}
	if err != nil {
		response.Internal(c)
		return
	}

	if !validateWebhookToken(cfgJSON, token) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	if status != "active" {
		c.JSON(http.StatusConflict, gin.H{"error": "project is not active"})
		return
	}

	var req struct {
		Alias string `json:"alias" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if h.Scheduler != nil {
		h.Scheduler.TriggerRun(projectID, req.Alias)
	}
	c.JSON(http.StatusOK, gin.H{"project_id": projectID, "alias": req.Alias, "status": "triggered"})
}

// AddToken generates a new webhook token and appends it to the project's token list.
// POST /api/v1/projects/:project_id/webhook-tokens
func (h *WebhookHandler) AddToken(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	ctx := c.Request.Context()

	raw, err := loadWebhookConfig(ctx, h.DB, projectID)
	if err != nil {
		response.Internal(c)
		return
	}

	tokens := migrateTokens(raw)
	newToken := generateToken()
	tokens = append(tokens, newToken)

	if err := saveWebhookTokens(ctx, h.DB, raw, tokens, projectID); err != nil {
		response.Internal(c)
		return
	}
	response.OK(c, gin.H{
		"new_token":      newToken,
		"webhook_tokens": maskTokens(tokens),
	})
}

// RemoveToken deletes a specific webhook token from the project's token list.
// DELETE /api/v1/projects/:project_id/webhook-tokens/:token_id
// token_id is the first 8 characters of the token (returned as MaskedToken.ID).
func (h *WebhookHandler) RemoveToken(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	tokenID := c.Param("token") // first 8 chars
	ctx := c.Request.Context()

	raw, err := loadWebhookConfig(ctx, h.DB, projectID)
	if err != nil {
		response.Internal(c)
		return
	}

	tokens := migrateTokens(raw)
	filtered := make([]string, 0, len(tokens))
	for _, t := range tokens {
		prefix := t
		if len(t) > 8 {
			prefix = t[:8]
		}
		if prefix != tokenID {
			filtered = append(filtered, t)
		}
	}

	if err := saveWebhookTokens(ctx, h.DB, raw, filtered, projectID); err != nil {
		response.Internal(c)
		return
	}
	response.OK(c, gin.H{"webhook_tokens": maskTokens(filtered)})
}

// validateWebhookToken checks whether token matches any token stored in cfgJSON.
// Accepts both the legacy webhook_token string and the new webhook_tokens array.
func validateWebhookToken(cfgJSON, token string) bool {
	var cfg struct {
		WebhookToken  string   `json:"webhook_token"`
		WebhookTokens []string `json:"webhook_tokens"`
	}
	_ = json.Unmarshal([]byte(cfgJSON), &cfg)

	tokenB := []byte(token)
	for _, t := range cfg.WebhookTokens {
		if subtle.ConstantTimeCompare([]byte(t), tokenB) == 1 {
			return true
		}
	}
	return cfg.WebhookToken != "" &&
		subtle.ConstantTimeCompare([]byte(cfg.WebhookToken), tokenB) == 1
}

// migrateTokens reads webhook_tokens from raw config, migrating legacy webhook_token if present.
// Always clears the legacy field from raw.
func migrateTokens(raw map[string]json.RawMessage) []string {
	var tokens []string
	if raw["webhook_tokens"] != nil {
		_ = json.Unmarshal(raw["webhook_tokens"], &tokens)
	}
	if len(tokens) == 0 && raw["webhook_token"] != nil {
		var legacy string
		_ = json.Unmarshal(raw["webhook_token"], &legacy)
		if legacy != "" {
			tokens = []string{legacy}
		}
	}
	delete(raw, "webhook_token")
	return tokens
}

func loadWebhookConfig(ctx context.Context, db *sql.DB, projectID int64) (map[string]json.RawMessage, error) {
	var cfgJSON string
	if err := db.QueryRowContext(ctx, `SELECT config FROM projects WHERE id = ?`, projectID).Scan(&cfgJSON); err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(cfgJSON), &raw)
	if raw == nil {
		raw = make(map[string]json.RawMessage)
	}
	return raw, nil
}

func saveWebhookTokens(ctx context.Context, db *sql.DB, raw map[string]json.RawMessage, tokens []string, projectID int64) error {
	tokensJSON, _ := json.Marshal(tokens)
	raw["webhook_tokens"] = tokensJSON
	newCfg, _ := json.Marshal(raw)
	_, err := db.ExecContext(ctx,
		`UPDATE projects SET config = ?, config_version = config_version + 1 WHERE id = ?`,
		string(newCfg), projectID)
	return err
}

func generateToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// MaskedToken is returned by the API instead of the raw token value.
// ID is the first 8 chars of the token (used for deletion).
// Masked is the display string with the middle obscured.
type MaskedToken struct {
	ID     string `json:"id"`
	Masked string `json:"masked"`
}

// maskTokens converts a list of raw tokens to MaskedToken display objects.
func maskTokens(tokens []string) []MaskedToken {
	if len(tokens) == 0 {
		return nil
	}
	out := make([]MaskedToken, len(tokens))
	for i, t := range tokens {
		suffix := ""
		if len(t) >= 6 {
			suffix = t[len(t)-6:]
		}
		prefix := t
		if len(t) > 8 {
			prefix = t[:8]
		}
		out[i] = MaskedToken{
			ID:     prefix,
			Masked: prefix + "••••••••" + suffix,
		}
	}
	return out
}
