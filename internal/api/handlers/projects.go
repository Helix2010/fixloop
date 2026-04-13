package handlers

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	mysql "github.com/go-sql-driver/mysql"
	gossh "golang.org/x/crypto/ssh"

	"github.com/fixloop/fixloop/internal/api/response"
	"github.com/fixloop/fixloop/internal/config"
	"github.com/fixloop/fixloop/internal/crypto"
	githubclient "github.com/fixloop/fixloop/internal/github"
	"github.com/fixloop/fixloop/internal/ssrf"
)

// ProjectScheduler is the scheduler interface needed by the project handler.
// Kept as interface so tests can stub it out.
type ProjectScheduler interface {
	RegisterProject(projectID int64) error
	RemoveProject(projectID int64)
	TriggerRun(projectID int64, agentAlias string)
}

// ProjectHandler handles all /api/v1/projects routes.
type ProjectHandler struct {
	DB        *sql.DB
	Cfg       *config.Config
	Scheduler ProjectScheduler
}

// ---- stored config structs (what lives in the DB) ----

type storedGitHub struct {
	Owner         string `json:"owner"`
	Repo          string `json:"repo"`
	PAT           string `json:"pat"` // hex(AES-GCM encrypted)
	FixBaseBranch string `json:"fix_base_branch"`
}

type storedIssueTracker struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
}

type storedVercel struct {
	ProjectID     string `json:"project_id,omitempty"`
	Token         string `json:"token,omitempty"`          // hex(AES-GCM encrypted)
	StagingTarget string `json:"staging_target,omitempty"` // "preview"|"production"
}

type storedTest struct {
	StagingURL      string `json:"staging_url,omitempty"`
	StagingAuthType string `json:"staging_auth_type,omitempty"` // "none"|"basic"|"bearer"
	StagingAuth     string `json:"staging_auth,omitempty"`      // hex(AES-GCM encrypted JSON)
}

type storedS3 struct {
	Endpoint    string `json:"endpoint,omitempty"` // e.g. https://obs.cn-north-4.myhuaweicloud.com
	Bucket      string `json:"bucket,omitempty"`
	Region      string `json:"region,omitempty"` // e.g. cn-north-4
	AccessKeyID string `json:"access_key_id,omitempty"`
	SecretKey   string `json:"secret_key,omitempty"` // hex(AES-GCM encrypted)
}

type projectConfig struct {
	GitHub          storedGitHub       `json:"github"`
	IssueTracker    storedIssueTracker `json:"issue_tracker"`
	SSHPrivateKey   string             `json:"ssh_private_key"` // hex(AES-GCM encrypted)
	DeployKeyID     int64              `json:"deploy_key_id,omitempty"`
	Vercel          storedVercel       `json:"vercel,omitempty"`
	Test            storedTest         `json:"test,omitempty"`
	S3              storedS3           `json:"s3,omitempty"`
	AIRunner        string             `json:"ai_runner,omitempty"`
	AIModel         string             `json:"ai_model,omitempty"`
	AIAPIBase       string             `json:"ai_api_base,omitempty"`
	AIAPIKey        string             `json:"ai_api_key,omitempty"`    // hex(AES-GCM encrypted)
	NotifyEvents    []string           `json:"notify_events,omitempty"` // nil/empty = all enabled
	TGChatID        *int64             `json:"tg_chat_id,omitempty"`
	WebhookToken    string             `json:"webhook_token,omitempty"`  // legacy single token; migrated on first write
	WebhookTokens   []string           `json:"webhook_tokens,omitempty"` // multi-token list
	PromptOverrides struct {
		IssueAnalysis string `json:"issue_analysis,omitempty"`
	} `json:"prompt_overrides,omitempty"`
}

// ---- API request / response structs ----

type createProjectReq struct {
	Name   string `json:"name" binding:"required"`
	GitHub struct {
		Owner         string `json:"owner" binding:"required"`
		Repo          string `json:"repo" binding:"required"`
		PAT           string `json:"pat" binding:"required"`
		FixBaseBranch string `json:"fix_base_branch"`
	} `json:"github" binding:"required"`
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
		StagingAuth     string `json:"staging_auth"` // raw JSON string, will be encrypted
	} `json:"test"`
	S3 struct {
		Endpoint    string `json:"endpoint"`
		Bucket      string `json:"bucket"`
		Region      string `json:"region"`
		AccessKeyID string `json:"access_key_id"`
		SecretKey   string `json:"secret_key"`
	} `json:"s3"`
	AIRunner  string `json:"ai_runner"`
	AIModel   string `json:"ai_model"`
	AIAPIBase string `json:"ai_api_base"`
	AIAPIKey  string `json:"ai_api_key"`
}

type patchProjectReq struct {
	Name   *string `json:"name"`
	GitHub *struct {
		PAT           string `json:"pat"`             // leave empty to keep existing
		FixBaseBranch string `json:"fix_base_branch"` // leave empty to keep existing
	} `json:"github"`
	IssueTracker *struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
	} `json:"issue_tracker"`
	Test *struct {
		StagingURL      string `json:"staging_url"`
		StagingAuthType string `json:"staging_auth_type"`
		StagingAuth     string `json:"staging_auth"`
	} `json:"test"`
	Vercel *struct {
		ProjectID     string `json:"project_id"`
		Token         string `json:"token"`
		StagingTarget string `json:"staging_target"`
	} `json:"vercel"`
	S3 *struct {
		Endpoint    string `json:"endpoint"`
		Bucket      string `json:"bucket"`
		Region      string `json:"region"`
		AccessKeyID string `json:"access_key_id"`
		SecretKey   string `json:"secret_key"` // leave empty to keep existing
	} `json:"s3"`
	AIRunner        *string   `json:"ai_runner"`
	AIModel         *string   `json:"ai_model"`
	AIAPIBase       *string   `json:"ai_api_base"`
	AIAPIKey        *string   `json:"ai_api_key"`
	NotifyEvents    *[]string `json:"notify_events"`
	TGChatID        *int64    `json:"tg_chat_id"`
	PromptOverrides *struct {
		IssueAnalysis *string `json:"issue_analysis"`
	} `json:"prompt_overrides"`
}

type projectResp struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	GitHub struct {
		Owner         string `json:"owner"`
		Repo          string `json:"repo"`
		FixBaseBranch string `json:"fix_base_branch"`
	} `json:"github"`
	IssueTracker struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
	} `json:"issue_tracker"`
	Vercel struct {
		ProjectID     string `json:"project_id,omitempty"`
		StagingTarget string `json:"staging_target,omitempty"`
	} `json:"vercel,omitempty"`
	Test struct {
		StagingURL      string `json:"staging_url,omitempty"`
		StagingAuthType string `json:"staging_auth_type,omitempty"`
	} `json:"test,omitempty"`
	S3 struct {
		Endpoint    string `json:"endpoint,omitempty"`
		Bucket      string `json:"bucket,omitempty"`
		Region      string `json:"region,omitempty"`
		AccessKeyID string `json:"access_key_id,omitempty"`
	} `json:"s3,omitempty"`
	AIRunner        string        `json:"ai_runner,omitempty"`
	AIModel         string        `json:"ai_model,omitempty"`
	NotifyEvents    []string      `json:"notify_events"`
	TGChatID        *int64        `json:"tg_chat_id,omitempty"`
	WebhookTokens   []MaskedToken `json:"webhook_tokens,omitempty"`
	PromptOverrides struct {
		IssueAnalysis string `json:"issue_analysis,omitempty"`
	} `json:"prompt_overrides,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// projectRow holds a raw project DB row before config parsing.
type projectRow struct {
	name      string
	cfgJSON   string
	status    string
	createdAt time.Time
}

// ---- handlers ----

func (h *ProjectHandler) Create(c *gin.Context) {
	userID := c.GetInt64("user_id")
	ctx := c.Request.Context()

	var req createProjectReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	name, err := validateName(req.Name)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// SSRF: validate staging URL before doing any crypto/DB work
	if req.Test.StagingURL != "" {
		if err := ssrf.ValidateURL(req.Test.StagingURL); err != nil {
			response.BadRequest(c, "staging_url: "+err.Error())
			return
		}
	}

	patEnc, err := h.encryptStr(req.GitHub.PAT)
	if err != nil {
		response.Internal(c)
		return
	}

	kp, err := crypto.GenerateSSHKeyPair()
	if err != nil {
		response.Internal(c)
		return
	}
	sshKeyEnc, err := h.encryptBytes(kp.PrivateKeyPEM)
	if err != nil {
		response.Internal(c)
		return
	}

	var vercelTokenEnc string
	if req.Vercel.Token != "" {
		vercelTokenEnc, err = h.encryptStr(req.Vercel.Token)
		if err != nil {
			response.Internal(c)
			return
		}
	}
	var aiAPIKeyEnc string
	if req.AIAPIKey != "" {
		aiAPIKeyEnc, err = h.encryptStr(req.AIAPIKey)
		if err != nil {
			response.Internal(c)
			return
		}
	}
	var stagingAuthEnc string
	if req.Test.StagingAuth != "" {
		stagingAuthEnc, err = h.encryptStr(req.Test.StagingAuth)
		if err != nil {
			response.Internal(c)
			return
		}
	}
	var s3SecretEnc string
	if req.S3.SecretKey != "" {
		s3SecretEnc, err = h.encryptStr(req.S3.SecretKey)
		if err != nil {
			response.Internal(c)
			return
		}
	}

	cfg := projectConfig{
		GitHub: storedGitHub{
			Owner:         req.GitHub.Owner,
			Repo:          req.GitHub.Repo,
			PAT:           patEnc,
			FixBaseBranch: orDefault(req.GitHub.FixBaseBranch, "main"),
		},
		IssueTracker: storedIssueTracker{
			Owner: orDefault(req.IssueTracker.Owner, req.GitHub.Owner),
			Repo:  orDefault(req.IssueTracker.Repo, req.GitHub.Repo),
		},
		SSHPrivateKey: sshKeyEnc,
		Vercel: storedVercel{
			ProjectID:     req.Vercel.ProjectID,
			Token:         vercelTokenEnc,
			StagingTarget: req.Vercel.StagingTarget,
		},
		Test: storedTest{
			StagingURL:      req.Test.StagingURL,
			StagingAuthType: req.Test.StagingAuthType,
			StagingAuth:     stagingAuthEnc,
		},
		S3: storedS3{
			Endpoint:    req.S3.Endpoint,
			Bucket:      req.S3.Bucket,
			Region:      req.S3.Region,
			AccessKeyID: req.S3.AccessKeyID,
			SecretKey:   s3SecretEnc,
		},
		AIRunner:  orDefault(req.AIRunner, "claude"),
		AIModel:   orDefault(req.AIModel, "claude-opus-4-6"),
		AIAPIBase: req.AIAPIBase,
		AIAPIKey:  aiAPIKeyEnc,
	}

	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		response.Internal(c)
		return
	}

	res, err := h.DB.ExecContext(ctx,
		`INSERT INTO projects (user_id, name, config, status) VALUES (?, ?, ?, 'active')`,
		userID, name, string(cfgJSON),
	)
	if err != nil {
		if isDuplicateEntry(err) {
			response.Err(c, http.StatusConflict, "PROJECT_NAME_CONFLICT", "项目名称已存在")
			return
		}
		response.Internal(c)
		return
	}
	projectID, _ := res.LastInsertId()

	// Register deploy key; mark project error on failure so the user knows to fix PAT permissions.
	keyTitle := fmt.Sprintf("fixloop-project-%d", projectID)
	dk, err := githubclient.New(req.GitHub.PAT).AddDeployKey(ctx,
		req.GitHub.Owner, req.GitHub.Repo,
		keyTitle, strings.TrimSpace(string(kp.PublicKeyAuth)))
	if err != nil {
		_, _ = h.DB.ExecContext(ctx,
			`UPDATE projects SET status = 'error' WHERE id = ?`, projectID,
		)
		response.Err(c, http.StatusUnprocessableEntity, "DEPLOY_KEY_FAILED",
			fmt.Sprintf("项目已创建但 Deploy Key 注册失败: %v — 请检查 PAT 是否有 Administration 权限", err))
		return
	}

	cfg.DeployKeyID = dk.ID
	cfgJSON, _ = json.Marshal(cfg)
	if _, err := h.DB.ExecContext(ctx,
		`UPDATE projects SET config = ? WHERE id = ?`, string(cfgJSON), projectID,
	); err != nil {
		// deploy_key_id is lost — the key exists on GitHub but we can't clean it up later
		slog.Error("failed to persist deploy_key_id", "project_id", projectID, "err", err)
	}

	if err := h.seedBacklog(ctx, projectID); err != nil {
		slog.Warn("seed backlog failed", "project_id", projectID, "err", err)
	}
	if err := h.seedAgents(ctx, projectID); err != nil {
		slog.Warn("seed agents failed", "project_id", projectID, "err", err)
	}

	h.schedRegister(projectID)
	response.Created(c, buildProjectResp(projectID, name, "active", cfg, time.Now()))
}

func (h *ProjectHandler) List(c *gin.Context) {
	userID := c.GetInt64("user_id")

	rows, err := h.DB.QueryContext(c.Request.Context(),
		`SELECT id, name, config, status, created_at
		 FROM projects
		 WHERE user_id = ? AND deleted_at IS NULL
		 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		response.Internal(c)
		return
	}
	defer rows.Close()

	projects := []projectResp{}
	for rows.Next() {
		var (
			id        int64
			name      string
			cfgJSON   string
			status    string
			createdAt time.Time
		)
		if err := rows.Scan(&id, &name, &cfgJSON, &status, &createdAt); err != nil {
			response.Internal(c)
			return
		}
		var cfg projectConfig
		_ = json.Unmarshal([]byte(cfgJSON), &cfg)
		projects = append(projects, buildProjectResp(id, name, status, cfg, createdAt))
	}
	if err := rows.Err(); err != nil {
		response.Internal(c)
		return
	}

	response.OK(c, projects)
}

func (h *ProjectHandler) Get(c *gin.Context) {
	projectID := c.GetInt64("project_id")

	p, cfg, err := h.fetchProject(c, projectID)
	if err != nil {
		return
	}
	response.OK(c, buildProjectResp(projectID, p.name, p.status, cfg, p.createdAt))
}

func (h *ProjectHandler) Update(c *gin.Context) {
	projectID := c.GetInt64("project_id")

	var req patchProjectReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	p, cfg, err := h.fetchProject(c, projectID)
	if err != nil {
		return
	}

	name := p.name
	if req.Name != nil {
		if name, err = validateName(*req.Name); err != nil {
			response.BadRequest(c, err.Error())
			return
		}
	}
	if req.GitHub != nil {
		if req.GitHub.PAT != "" {
			enc, err := h.encryptStr(req.GitHub.PAT)
			if err != nil {
				response.Internal(c)
				return
			}
			cfg.GitHub.PAT = enc
		}
		if req.GitHub.FixBaseBranch != "" {
			cfg.GitHub.FixBaseBranch = req.GitHub.FixBaseBranch
		}
	}
	if req.IssueTracker != nil {
		if req.IssueTracker.Owner != "" {
			cfg.IssueTracker.Owner = req.IssueTracker.Owner
		}
		if req.IssueTracker.Repo != "" {
			cfg.IssueTracker.Repo = req.IssueTracker.Repo
		}
	}
	if req.Test != nil {
		if req.Test.StagingURL != "" {
			if err := ssrf.ValidateURL(req.Test.StagingURL); err != nil {
				response.BadRequest(c, "staging_url: "+err.Error())
				return
			}
			cfg.Test.StagingURL = req.Test.StagingURL
		}
		if req.Test.StagingAuthType != "" {
			cfg.Test.StagingAuthType = req.Test.StagingAuthType
		}
		if req.Test.StagingAuth != "" {
			enc, err := h.encryptStr(req.Test.StagingAuth)
			if err != nil {
				response.Internal(c)
				return
			}
			cfg.Test.StagingAuth = enc
		}
	}
	if req.Vercel != nil {
		cfg.Vercel.ProjectID = req.Vercel.ProjectID
		cfg.Vercel.StagingTarget = req.Vercel.StagingTarget
		if req.Vercel.Token != "" {
			enc, err := h.encryptStr(req.Vercel.Token)
			if err != nil {
				response.Internal(c)
				return
			}
			cfg.Vercel.Token = enc
		}
	}
	if req.S3 != nil {
		// Only overwrite non-empty fields so a partial PATCH (e.g. secret key rotation)
		// doesn't clear the other S3 fields.
		if req.S3.Endpoint != "" {
			cfg.S3.Endpoint = req.S3.Endpoint
		}
		if req.S3.Bucket != "" {
			cfg.S3.Bucket = req.S3.Bucket
		}
		if req.S3.Region != "" {
			cfg.S3.Region = req.S3.Region
		}
		if req.S3.AccessKeyID != "" {
			cfg.S3.AccessKeyID = req.S3.AccessKeyID
		}
		if req.S3.SecretKey != "" {
			enc, err := h.encryptStr(req.S3.SecretKey)
			if err != nil {
				response.Internal(c)
				return
			}
			cfg.S3.SecretKey = enc
		}
	}
	if req.AIRunner != nil {
		cfg.AIRunner = *req.AIRunner
	}
	if req.AIModel != nil {
		cfg.AIModel = *req.AIModel
	}
	if req.AIAPIBase != nil {
		cfg.AIAPIBase = *req.AIAPIBase
	}
	if req.AIAPIKey != nil && *req.AIAPIKey != "" {
		enc, err := h.encryptStr(*req.AIAPIKey)
		if err != nil {
			response.Internal(c)
			return
		}
		cfg.AIAPIKey = enc
	}
	if req.NotifyEvents != nil {
		cfg.NotifyEvents = *req.NotifyEvents
	}
	if req.TGChatID != nil {
		if *req.TGChatID == 0 {
			cfg.TGChatID = nil // 0 means "clear"
		} else {
			// Enforce uniqueness: a group can only be bound to one project.
			var conflictID int64
			err := h.DB.QueryRowContext(c.Request.Context(),
				`SELECT id FROM projects
				 WHERE CAST(JSON_EXTRACT(config, '$.tg_chat_id') AS SIGNED) = ?
				   AND id != ? AND deleted_at IS NULL LIMIT 1`,
				*req.TGChatID, projectID,
			).Scan(&conflictID)
			if err == nil {
				response.Err(c, http.StatusConflict, "TG_CHAT_ALREADY_BOUND", "该群组已关联到其他项目，请先解除原绑定")
				return
			}
			cfg.TGChatID = req.TGChatID
		}
	}
	if req.PromptOverrides != nil {
		if req.PromptOverrides.IssueAnalysis != nil {
			cfg.PromptOverrides.IssueAnalysis = *req.PromptOverrides.IssueAnalysis
		}
	}

	cfgJSON, _ := json.Marshal(cfg)
	if _, err = h.DB.ExecContext(c.Request.Context(),
		`UPDATE projects SET name = ?, config = ?, config_version = config_version + 1 WHERE id = ?`,
		name, string(cfgJSON), projectID,
	); err != nil {
		if isDuplicateEntry(err) {
			response.Err(c, http.StatusConflict, "PROJECT_NAME_CONFLICT", "项目名称已存在")
			return
		}
		response.Internal(c)
		return
	}

	response.OK(c, buildProjectResp(projectID, name, p.status, cfg, p.createdAt))
}

func (h *ProjectHandler) Delete(c *gin.Context) {
	projectID := c.GetInt64("project_id")

	res, err := h.DB.ExecContext(c.Request.Context(),
		`UPDATE projects SET deleted_at = NOW() WHERE id = ? AND deleted_at IS NULL`,
		projectID,
	)
	if err != nil {
		response.Internal(c)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "项目")
		return
	}

	h.schedRemove(projectID)
	c.JSON(http.StatusNoContent, nil)
}

func (h *ProjectHandler) Pause(c *gin.Context) {
	h.setStatus(c, "paused")
}

func (h *ProjectHandler) Resume(c *gin.Context) {
	h.setStatus(c, "active")
}

func (h *ProjectHandler) setStatus(c *gin.Context, newStatus string) {
	projectID := c.GetInt64("project_id")

	res, err := h.DB.ExecContext(c.Request.Context(),
		`UPDATE projects SET status = ? WHERE id = ? AND deleted_at IS NULL`,
		newStatus, projectID,
	)
	if err != nil {
		response.Internal(c)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "项目")
		return
	}

	if newStatus == "paused" {
		h.schedRemove(projectID)
	} else {
		h.schedRegister(projectID)
	}
	c.JSON(http.StatusNoContent, nil)
}

// ---- helpers ----

// fetchProject loads a project row and parses its config.
// On error it writes the response so callers can early-return on non-nil err.
func (h *ProjectHandler) fetchProject(c *gin.Context, projectID int64) (*projectRow, projectConfig, error) {
	var p projectRow
	err := h.DB.QueryRowContext(c.Request.Context(),
		`SELECT name, config, status, created_at FROM projects WHERE id = ? AND deleted_at IS NULL`,
		projectID,
	).Scan(&p.name, &p.cfgJSON, &p.status, &p.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		response.NotFound(c, "项目")
		return nil, projectConfig{}, err
	}
	if err != nil {
		response.Internal(c)
		return nil, projectConfig{}, err
	}
	var cfg projectConfig
	_ = json.Unmarshal([]byte(p.cfgJSON), &cfg)
	return &p, cfg, nil
}

func (h *ProjectHandler) encryptStr(s string) (string, error) {
	return h.encryptBytes([]byte(s))
}

func (h *ProjectHandler) encryptBytes(b []byte) (string, error) {
	enc, err := crypto.Encrypt(h.Cfg.AESKeyID, h.Cfg.AESKey, b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(enc), nil
}

// schedRegister registers project jobs; no-op when scheduler is not wired in.
func (h *ProjectHandler) schedRegister(projectID int64) {
	if h.Scheduler != nil {
		_ = h.Scheduler.RegisterProject(projectID)
	}
}

// schedRemove removes project jobs; no-op when scheduler is not wired in.
func (h *ProjectHandler) schedRemove(projectID int64) {
	if h.Scheduler != nil {
		h.Scheduler.RemoveProject(projectID)
	}
}

// seedBacklog inserts the three built-in seed scenarios in a single round-trip.
func (h *ProjectHandler) seedBacklog(ctx context.Context, projectID int64) error {
	s0 := "首页可访问（HTTP 200，无 JS crash）"
	s1 := "页面无 console ERROR"
	s2 := "核心交互元素可见（body 非空，无 500 页面）"
	_, err := h.DB.ExecContext(ctx,
		`INSERT IGNORE INTO backlog
		 (project_id, title, title_hash, description, scenario_type, priority, source) VALUES
		 (?, ?, ?, ?, 'ui', 1, 'seed'),
		 (?, ?, ?, ?, 'ui', 1, 'seed'),
		 (?, ?, ?, ?, 'ui', 1, 'seed')`,
		projectID, s0, titleHash(s0), "验证首页返回 HTTP 200 且无 JavaScript 崩溃",
		projectID, s1, titleHash(s1), "验证页面加载过程中无 console.error 输出",
		projectID, s2, titleHash(s2), "验证 body 非空且页面无服务器错误",
	)
	if err != nil {
		return fmt.Errorf("seed backlog: %w", err)
	}
	return nil
}

// seedAgents inserts the 4 built-in agent rows for a new project.
func (h *ProjectHandler) seedAgents(ctx context.Context, projectID int64) error {
	for _, row := range []struct {
		t, n, a string
		mins    int
	}{
		{"explore", "Explore Agent", "explore", 10},
		{"fix", "Fix Agent", "fix", 30},
		{"master", "Master Agent", "master", 10},
		{"plan", "Plan Agent", "plan", 10080},
	} {
		if _, err := h.DB.ExecContext(ctx,
			`INSERT IGNORE INTO project_agents (project_id, agent_type, name, alias, schedule_minutes)
             VALUES (?, ?, ?, ?, ?)`,
			projectID, row.t, row.n, row.a, row.mins,
		); err != nil {
			return err
		}
	}
	return nil
}

// buildProjectResp constructs the API response.
// Sensitive fields (PAT, keys, tokens) are never included.
func buildProjectResp(id int64, name, status string, cfg projectConfig, createdAt time.Time) projectResp {
	r := projectResp{
		ID:           id,
		Name:         name,
		Status:       status,
		AIRunner:     cfg.AIRunner,
		AIModel:      cfg.AIModel,
		NotifyEvents: cfg.NotifyEvents,
		TGChatID:     cfg.TGChatID,
		CreatedAt:    createdAt,
	}
	r.GitHub.Owner = cfg.GitHub.Owner
	r.GitHub.Repo = cfg.GitHub.Repo
	r.GitHub.FixBaseBranch = cfg.GitHub.FixBaseBranch
	r.IssueTracker.Owner = cfg.IssueTracker.Owner
	r.IssueTracker.Repo = cfg.IssueTracker.Repo
	r.Vercel.ProjectID = cfg.Vercel.ProjectID
	r.Vercel.StagingTarget = cfg.Vercel.StagingTarget
	r.Test.StagingURL = cfg.Test.StagingURL
	r.Test.StagingAuthType = cfg.Test.StagingAuthType
	r.S3.Endpoint = cfg.S3.Endpoint
	r.S3.Bucket = cfg.S3.Bucket
	r.S3.Region = cfg.S3.Region
	r.S3.AccessKeyID = cfg.S3.AccessKeyID
	r.PromptOverrides.IssueAnalysis = cfg.PromptOverrides.IssueAnalysis
	tokens := cfg.WebhookTokens
	if len(tokens) == 0 && cfg.WebhookToken != "" {
		tokens = []string{cfg.WebhookToken}
	}
	r.WebhookTokens = maskTokens(tokens)
	return r
}

// validateName trims whitespace and validates length, returning the trimmed name.
func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if len(name) == 0 || len(name) > 64 {
		return "", fmt.Errorf("project name must be 1-64 characters")
	}
	return name, nil
}

// isDuplicateEntry detects MySQL duplicate-key errors (error 1062).
func isDuplicateEntry(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

// titleHash returns the SHA-1 hex of the normalized title (lowercase, no punctuation/spaces).
// Used to deduplicate backlog scenarios by content rather than exact text.
func titleHash(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if !unicode.IsPunct(r) && !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	sum := sha1.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ConfirmDeployKey marks the deploy key as manually confirmed (user added it on GitHub themselves).
// POST /api/v1/projects/:project_id/deploy-key/confirm
func (h *ProjectHandler) ConfirmDeployKey(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	ctx := c.Request.Context()

	var cfgJSON string
	if err := h.DB.QueryRowContext(ctx, `SELECT config FROM projects WHERE id = ?`, projectID).Scan(&cfgJSON); err != nil {
		response.Internal(c)
		return
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(cfgJSON), &raw)
	raw["deploy_key_confirmed"], _ = json.Marshal(true)
	newCfg, _ := json.Marshal(raw)
	if _, err := h.DB.ExecContext(ctx, `UPDATE projects SET config = ? WHERE id = ?`, string(newCfg), projectID); err != nil {
		response.Internal(c)
		return
	}
	response.OK(c, gin.H{"confirmed": true})
}

// GetDeployKey returns the project's SSH public key and deploy key registration status.
// GET /api/v1/projects/:project_id/deploy-key
func (h *ProjectHandler) GetDeployKey(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	ctx := c.Request.Context()

	var cfgJSON string
	if err := h.DB.QueryRowContext(ctx, `SELECT config FROM projects WHERE id = ?`, projectID).Scan(&cfgJSON); err != nil {
		response.Internal(c)
		return
	}
	var cfg projectConfig
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		response.Internal(c)
		return
	}

	// Also check deploy_key_confirmed flag (set when user manually adds the key)
	var rawMap map[string]json.RawMessage
	_ = json.Unmarshal([]byte(cfgJSON), &rawMap)
	var deployKeyConfirmed bool
	if v, ok := rawMap["deploy_key_confirmed"]; ok {
		_ = json.Unmarshal(v, &deployKeyConfirmed)
	}

	pubKey, err := deriveSSHPublicKey(h.Cfg, cfg.SSHPrivateKey)
	if err != nil {
		response.Err(c, http.StatusUnprocessableEntity, "KEY_ERROR", "无法解析 SSH 密钥: "+err.Error())
		return
	}

	response.OK(c, gin.H{
		"public_key":    pubKey,
		"deploy_key_id": cfg.DeployKeyID,
		"registered":    cfg.DeployKeyID != 0 || deployKeyConfirmed,
	})
}

// RegisterDeployKey (re-)registers the project's SSH public key as a GitHub Deploy Key.
// POST /api/v1/projects/:project_id/deploy-key/register
func (h *ProjectHandler) RegisterDeployKey(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	ctx := c.Request.Context()

	var cfgJSON string
	if err := h.DB.QueryRowContext(ctx, `SELECT config FROM projects WHERE id = ?`, projectID).Scan(&cfgJSON); err != nil {
		response.Internal(c)
		return
	}
	var cfg projectConfig
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		response.Internal(c)
		return
	}

	pubKey, err := deriveSSHPublicKey(h.Cfg, cfg.SSHPrivateKey)
	if err != nil {
		response.Err(c, http.StatusUnprocessableEntity, "KEY_ERROR", "无法解析 SSH 密钥: "+err.Error())
		return
	}

	patEnc, err := hex.DecodeString(cfg.GitHub.PAT)
	if err != nil {
		response.Err(c, http.StatusUnprocessableEntity, "PAT_ERROR", "无法解析 PAT 格式")
		return
	}
	patBytes, err := crypto.Decrypt(map[byte][]byte{h.Cfg.AESKeyID: h.Cfg.AESKey}, patEnc)
	if err != nil {
		response.Err(c, http.StatusUnprocessableEntity, "PAT_ERROR", "无法解密 PAT")
		return
	}
	pat := string(patBytes)

	// Remove old deploy key from GitHub if we have its ID
	gh := githubclient.New(pat)
	if cfg.DeployKeyID != 0 {
		_ = gh.DeleteDeployKey(ctx, cfg.GitHub.Owner, cfg.GitHub.Repo, cfg.DeployKeyID)
	}

	keyTitle := fmt.Sprintf("fixloop-project-%d", projectID)
	dk, err := gh.AddDeployKey(ctx, cfg.GitHub.Owner, cfg.GitHub.Repo, keyTitle, strings.TrimSpace(pubKey))
	if err != nil {
		response.Err(c, http.StatusUnprocessableEntity, "DEPLOY_KEY_FAILED",
			fmt.Sprintf("注册 Deploy Key 失败: %v — 请确认 PAT 有 Administration 权限", err))
		return
	}

	// Persist new deploy_key_id via raw JSON patch to avoid touching other fields
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(cfgJSON), &raw)
	raw["deploy_key_id"], _ = json.Marshal(dk.ID)
	newCfg, _ := json.Marshal(raw)
	if _, err := h.DB.ExecContext(ctx, `UPDATE projects SET config = ? WHERE id = ?`, string(newCfg), projectID); err != nil {
		response.Internal(c)
		return
	}

	response.OK(c, gin.H{"deploy_key_id": dk.ID, "public_key": pubKey})
}

// deriveSSHPublicKey decrypts the stored private key and returns the authorized_keys public key string.
func deriveSSHPublicKey(cfg *config.Config, encHex string) (string, error) {
	enc, err := hex.DecodeString(encHex)
	if err != nil {
		return "", fmt.Errorf("hex decode: %w", err)
	}
	privPEM, err := crypto.Decrypt(map[byte][]byte{cfg.AESKeyID: cfg.AESKey}, enc)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	signer, err := gossh.ParsePrivateKey(privPEM)
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}
	return strings.TrimSpace(string(gossh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}
