package handlers

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fixloop/fixloop/internal/config"
	"github.com/fixloop/fixloop/internal/crypto"
)

// tgBotUsernameRe enforces Telegram bot username rules: 5-32 chars, alphanumeric + underscore.
var tgBotUsernameRe = regexp.MustCompile(`^[A-Za-z0-9_]{5,32}$`)

// AdminHandler handles system-level admin settings.
type AdminHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

type tgConfigResp struct {
	Configured  bool   `json:"configured"`
	BotUsername string `json:"bot_username"`
}

// GetTGConfig returns current TG bot configuration status.
// GET /api/v1/admin/tg-config
func (h *AdminHandler) GetTGConfig(c *gin.Context) {
	resp := tgConfigResp{}

	rows, err := h.DB.QueryContext(c.Request.Context(),
		`SELECT key_name, value FROM system_config WHERE key_name IN ('tg_bot_token', 'tg_bot_username')`,
	)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"data": resp})
		return
	}
	defer rows.Close()
	for rows.Next() {
		var key, val string
		if rows.Scan(&key, &val) != nil {
			continue
		}
		switch key {
		case "tg_bot_token":
			resp.Configured = val != ""
		case "tg_bot_username":
			resp.BotUsername = val
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

type patchTGConfigReq struct {
	BotToken    string `json:"bot_token"`
	BotUsername string `json:"bot_username"`
}

// PatchTGConfig saves TG bot configuration to system_config.
// PATCH /api/v1/admin/tg-config
func (h *AdminHandler) PatchTGConfig(c *gin.Context) {
	var req patchTGConfigReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_JSON"})
		return
	}

	if req.BotToken != "" {
		enc, err := crypto.Encrypt(h.Cfg.AESKeyID, h.Cfg.AESKey, []byte(req.BotToken))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "ENCRYPT_FAILED"})
			return
		}
		if _, err := h.DB.ExecContext(c.Request.Context(),
			`INSERT INTO system_config (key_name, value) VALUES ('tg_bot_token', ?)
			 ON DUPLICATE KEY UPDATE value = VALUES(value)`,
			hex.EncodeToString(enc),
		); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
			return
		}
	}

	if req.BotUsername != "" {
		if !tgBotUsernameRe.MatchString(req.BotUsername) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"code":    "INVALID_USERNAME",
				"message": "bot_username 只能包含字母、数字、下划线，长度 5-32 位",
			}})
			return
		}
		if _, err := h.DB.ExecContext(c.Request.Context(),
			`INSERT INTO system_config (key_name, value) VALUES ('tg_bot_username', ?)
			 ON DUPLICATE KEY UPDATE value = VALUES(value)`,
			req.BotUsername,
		); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_ERROR"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"saved": true}})
}

// VerifyTGToken calls Telegram's getMe API to validate a bot token.
// POST /api/v1/admin/tg-config/verify
func (h *AdminHandler) VerifyTGToken(c *gin.Context) {
	var req struct {
		BotToken string `json:"bot_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.BotToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "MISSING_TOKEN", "message": "bot_token 不能为空"}})
		return
	}

	tgCtx, tgCancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer tgCancel()
	tgReq, _ := http.NewRequestWithContext(tgCtx, http.MethodGet,
		fmt.Sprintf("https://api.telegram.org/bot%s/getMe", req.BotToken), nil)
	resp, err := http.DefaultClient.Do(tgReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"code": "TG_UNREACHABLE", "message": "无法连接 Telegram API，请检查网络"}})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var tgResp struct {
		OK     bool `json:"ok"`
		Result struct {
			ID        int64  `json:"id"`
			FirstName string `json:"first_name"`
			Username  string `json:"username"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &tgResp); err != nil || !tgResp.OK {
		msg := tgResp.Description
		if msg == "" {
			msg = "Token 无效"
		}
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": gin.H{"code": "INVALID_TOKEN", "message": msg}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"bot_id":       tgResp.Result.ID,
		"bot_name":     tgResp.Result.FirstName,
		"bot_username": tgResp.Result.Username,
	}})
}

// GetWorkspace returns the configured workspace directory and its status.
// GET /api/v1/admin/workspace
func (h *AdminHandler) GetWorkspace(c *gin.Context) {
	dir := h.Cfg.WorkspaceDir
	info := workspaceStat(dir)
	c.JSON(http.StatusOK, gin.H{"data": info})
}

// InitWorkspace creates the workspace directory (and parents) if it doesn't exist,
// then verifies read/write access.
// POST /api/v1/admin/workspace/init
func (h *AdminHandler) InitWorkspace(c *gin.Context) {
	dir := h.Cfg.WorkspaceDir
	if err := os.MkdirAll(dir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("创建目录失败: %v", err)})
		return
	}
	info := workspaceStat(dir)
	c.JSON(http.StatusOK, gin.H{"data": info})
}

type workspaceInfo struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	Readable  bool   `json:"readable"`
	Writable  bool   `json:"writable"`
	DiskTotal uint64 `json:"disk_total"` // bytes
	DiskFree  uint64 `json:"disk_free"`  // bytes
	DiskUsed  uint64 `json:"disk_used"`  // bytes; usage under this dir
}

// workspaceStatCache avoids repeated full directory walks on every request.
var (
	wsCache    workspaceInfo
	wsCacheDir string
	wsCacheExp time.Time
	wsCacheMu  sync.Mutex
)

func workspaceStat(dir string) workspaceInfo {
	wsCacheMu.Lock()
	if dir == wsCacheDir && time.Now().Before(wsCacheExp) {
		cached := wsCache
		wsCacheMu.Unlock()
		return cached
	}
	wsCacheMu.Unlock()

	// Compute outside the lock — dirSize walks the whole tree and can be slow.
	info := computeWorkspaceStat(dir)

	wsCacheMu.Lock()
	wsCache = info
	wsCacheDir = dir
	wsCacheExp = time.Now().Add(5 * time.Minute)
	wsCacheMu.Unlock()
	return info
}

func computeWorkspaceStat(dir string) workspaceInfo {
	info := workspaceInfo{Path: dir}

	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return info
	}
	info.Exists = true

	// Check readable by listing
	if _, err := os.ReadDir(dir); err == nil {
		info.Readable = true
	}

	if f, err := os.CreateTemp(dir, ".fixloop_write_test*"); err == nil {
		f.Close()
		_ = os.Remove(f.Name())
		info.Writable = true
	}

	// Disk usage of the filesystem containing dir
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) == nil {
		info.DiskTotal = st.Blocks * uint64(st.Bsize)
		info.DiskFree = st.Bfree * uint64(st.Bsize)
	}

	// Rough du for dir itself
	info.DiskUsed = dirSize(dir)
	return info
}

type tgChatResp struct {
	ChatID           int64   `json:"chat_id"`
	Title            string  `json:"title"`
	ChatType         string  `json:"chat_type"`
	BoundProjectID   *int64  `json:"bound_project_id,omitempty"`
	BoundProjectName *string `json:"bound_project_name,omitempty"`
}

// GetTGChats returns known group chats where the bot is active, annotated with
// which project (if any) each chat is already bound to.
// GET /api/v1/admin/tg-chats
func (h *AdminHandler) GetTGChats(c *gin.Context) {
	rows, err := h.DB.QueryContext(c.Request.Context(),
		`SELECT k.chat_id, k.title, k.chat_type,
		        p.id   AS bound_project_id,
		        p.name AS bound_project_name
		 FROM tg_known_chats k
		 LEFT JOIN projects p
		        ON CAST(JSON_EXTRACT(p.config, '$.tg_chat_id') AS SIGNED) = k.chat_id
		       AND p.deleted_at IS NULL
		 WHERE k.active = 1
		 ORDER BY k.title`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "query failed"})
		return
	}
	defer rows.Close()
	var chats []tgChatResp
	for rows.Next() {
		var item tgChatResp
		var boundID sql.NullInt64
		var boundName sql.NullString
		if rows.Scan(&item.ChatID, &item.Title, &item.ChatType, &boundID, &boundName) != nil {
			continue
		}
		if boundID.Valid {
			item.BoundProjectID = &boundID.Int64
			s := boundName.String
			item.BoundProjectName = &s
		}
		chats = append(chats, item)
	}
	if err := rows.Err(); err != nil {
		c.JSON(http.StatusOK, gin.H{"data": []tgChatResp{}})
		return
	}
	if chats == nil {
		chats = []tgChatResp{}
	}
	c.JSON(http.StatusOK, gin.H{"data": chats})
}

func dirSize(path string) uint64 {
	var size uint64
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			size += uint64(fi.Size())
		}
		return nil
	})
	return size
}
