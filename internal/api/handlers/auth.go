package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/fixloop/fixloop/internal/api/middleware"
	"github.com/fixloop/fixloop/internal/config"
)

type AuthHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

// GitHubLogin redirects to GitHub OAuth authorization page.
// GET /api/v1/auth/github
func (h *AuthHandler) GitHubLogin(c *gin.Context) {
	state := generateState()
	c.SetCookie("oauth_state", state, 600, "/", "", true, true)

	authURL := fmt.Sprintf(
		"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&scope=read:user&state=%s",
		url.QueryEscape(h.Cfg.GitHubClientID),
		url.QueryEscape(h.Cfg.GitHubRedirectURL),
		url.QueryEscape(state),
	)
	c.Redirect(http.StatusFound, authURL)
}

// GitHubCallback handles the GitHub OAuth callback.
// GET /api/v1/auth/github/callback
func (h *AuthHandler) GitHubCallback(c *gin.Context) {
	// CSRF: verify state
	stateCookie, err := c.Cookie("oauth_state")
	if err != nil || stateCookie != c.Query("state") || c.Query("state") == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code": "INVALID_STATE", "message": "OAuth state 验证失败",
		}})
		return
	}
	c.SetCookie("oauth_state", "", -1, "/", "", true, true)

	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code": "MISSING_CODE", "message": "缺少 OAuth code",
		}})
		return
	}

	accessToken, err := h.exchangeCode(c.Request.Context(), code)
	if err != nil {
		slog.Error("github oauth exchange failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "OAUTH_FAILED", "message": "GitHub 授权失败",
		}})
		return
	}

	ghUser, err := h.fetchGitHubUser(c.Request.Context(), accessToken)
	if err != nil {
		slog.Error("fetch github user failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "OAUTH_FAILED", "message": "获取 GitHub 用户信息失败",
		}})
		return
	}

	userID, err := h.upsertUser(c.Request.Context(), ghUser)
	if err != nil {
		slog.Error("upsert user failed", "err", err, "unexpected", true)
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "DB_ERROR", "message": "用户信息保存失败",
		}})
		return
	}

	const sevenDays = 7 * 24 * 60 * 60
	if err := middleware.IssueJWT(c, h.Cfg.JWTSecret, userID, ghUser.Login, sevenDays); err != nil {
		slog.Error("issue jwt failed", "err", err, "unexpected", true)
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "JWT_FAILED", "message": "登录 token 生成失败",
		}})
		return
	}

	redirect := c.Query("redirect")
	if redirect == "" || !strings.HasPrefix(redirect, "/") {
		redirect = "/dashboard"
	}
	c.Redirect(http.StatusFound, redirect)
}

// UserInfo returns current user. GET /api/v1/me
func (h *AuthHandler) UserInfo(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	var tgChatID *int64
	if err := h.DB.QueryRowContext(c.Request.Context(),
		`SELECT tg_chat_id FROM users WHERE id = ?`, userID,
	).Scan(&tgChatID); err != nil && err != sql.ErrNoRows {
		slog.Warn("UserInfo: failed to fetch tg_chat_id", "user_id", userID, "err", err)
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"id":           userID,
		"github_login": c.MustGet("github_login").(string),
		"tg_chat_id":   tgChatID,
	}})
}

// DeleteMe soft-deletes the current user. DELETE /api/v1/me
func (h *AuthHandler) DeleteMe(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	_, err := h.DB.ExecContext(c.Request.Context(),
		`UPDATE users SET deleted_at = ? WHERE id = ?`, time.Now(), userID,
	)
	if err != nil {
		slog.Error("delete user failed", "user_id", userID, "err", err, "unexpected", true)
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "DB_ERROR", "message": "账号删除失败",
		}})
		return
	}
	c.SetCookie("fixloop_session", "", -1, "/", "", true, true)
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"message": "账号已删除"}})
}

// TGBind generates a one-time Telegram bind token. POST /api/v1/me/tg-bind
func (h *AuthHandler) TGBind(c *gin.Context) {
	var botUsername string
	_ = h.DB.QueryRowContext(c.Request.Context(),
		`SELECT value FROM system_config WHERE key_name = 'tg_bot_username'`,
	).Scan(&botUsername)
	if botUsername == "" {
		botUsername = h.Cfg.TGBotUsername
	}
	if botUsername == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"code":    "TG_NOT_CONFIGURED",
			"message": "Telegram Bot 未配置，请联系管理员",
		}})
		return
	}
	userID := c.MustGet("user_id").(int64)

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "RAND_ERROR", "message": "生成 token 失败",
		}})
		return
	}
	token := fmt.Sprintf("%x", b)
	key := "tg_bind_" + token

	_, err := h.DB.ExecContext(c.Request.Context(),
		`INSERT INTO system_config (key_name, value) VALUES (?, ?)
		 ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = NOW()`,
		key, fmt.Sprintf("%d", userID),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "DB_ERROR", "message": "生成绑定 token 失败",
		}})
		return
	}

	tgURL := fmt.Sprintf("https://t.me/%s?start=%s", botUsername, token)
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"token":  token,
		"tg_url": tgURL,
	}})
}

type githubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

func (h *AuthHandler) exchangeCode(ctx context.Context, code string) (string, error) {
	body := url.Values{
		"client_id":     {h.Cfg.GitHubClientID},
		"client_secret": {h.Cfg.GitHubClientSecret},
		"code":          {code},
		"redirect_uri":  {h.Cfg.GitHubRedirectURL},
	}
	req, _ := http.NewRequestWithContext(ctx, "POST",
		"https://github.com/login/oauth/access_token",
		strings.NewReader(body.Encode()),
	)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Error != "" {
		return "", fmt.Errorf("github oauth error: %s", result.Error)
	}
	return result.AccessToken, nil
}

func (h *AuthHandler) fetchGitHubUser(ctx context.Context, token string) (*githubUser, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("github token invalid or revoked")
	}
	b, _ := io.ReadAll(resp.Body)
	var u githubUser
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (h *AuthHandler) upsertUser(ctx context.Context, u *githubUser) (int64, error) {
	res, err := h.DB.ExecContext(ctx, `
		INSERT INTO users (github_id, github_login, created_at)
		VALUES (?, ?, NOW())
		ON DUPLICATE KEY UPDATE github_login = VALUES(github_login)
	`, u.ID, u.Login)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil || id == 0 {
		err = h.DB.QueryRowContext(ctx,
			`SELECT id FROM users WHERE github_id = ?`, u.ID,
		).Scan(&id)
	}
	return id, err
}

func generateState() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}
