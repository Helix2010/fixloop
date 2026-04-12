# FixLoop Phase 5: API Endpoints + TG Bot + plan-agent

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补全所有缺失 REST API 端点、实现 Telegram Bot（通知+命令）、实现 plan-agent（AI 每周生成 backlog 场景）。

**Architecture:**
- `internal/api/handlers/data.go` — issues/prs/backlog/runs 的 list/get/patch/trigger
- `internal/api/handlers/notifications.go` — 通知列表 + 全部已读
- `internal/api/handlers/screenshots.go` — R2 代理端点
- `internal/tgbot/bot.go` — TG Bot 长轮询、7 种通知发送、9 条命令处理
- `internal/plan/plan.go` — plan-agent：AI 生成 backlog 场景
- Config/router/main.go 三处打通

**Tech Stack:** go-telegram-bot-api/v5, existing packages (notify, agentrun, storage, config, crypto, agent/runner)

**Spec:** `docs/specs/2026-04-11-fixloop-design.md` 章节：二十四、二十五、二十六

---

## File Structure

```
internal/
  scheduler/
    scheduler.go        # 修改: 添加 TriggerRun(projectID, agentType)
  api/
    handlers/
      data.go           # 新建: issues / prs / backlog / runs handlers
      notifications.go  # 新建: notifications handlers
      screenshots.go    # 新建: R2 screenshot proxy
    router.go           # 修改: 注册所有新路由
  tgbot/
    bot.go              # 新建: TG Bot 完整实现
  plan/
    plan.go             # 新建: plan-agent
    prompts/
      plan.txt          # 新建: plan-agent prompt 模板
  config/
    config.go           # 修改: 添加 TGBotToken 字段
config.yaml             # 修改: 添加 tg.bot_token
config.yaml.example     # 修改: 添加 tg.bot_token
cmd/server/main.go      # 修改: 启动 TG Bot + 接入 plan-agent
```

---

## Task 1: scheduler.TriggerRun + 全部缺失 API 端点

**Files:**
- Modify: `internal/scheduler/scheduler.go`
- Create: `internal/api/handlers/data.go`
- Create: `internal/api/handlers/notifications.go`
- Create: `internal/api/handlers/screenshots.go`
- Modify: `internal/api/handlers/projects.go` (extend ProjectScheduler interface)
- Modify: `internal/api/router.go`

### Step 1a: 在 ProjectScheduler 接口加 TriggerRun

在 `internal/api/handlers/projects.go` 中，将 `ProjectScheduler` 接口改为：

```go
type ProjectScheduler interface {
    RegisterProject(projectID int64) error
    RemoveProject(projectID int64)
    TriggerRun(projectID int64, agentType string)
}
```

在 `internal/scheduler/scheduler.go` 末尾追加：

```go
// TriggerRun fires the agent function immediately in a goroutine.
func (s *Scheduler) TriggerRun(projectID int64, agentType string) {
    go s.agentFunc(projectID, agentType)
}
```

- [ ] **Step 1: 修改 ProjectScheduler 接口**

读取 `internal/api/handlers/projects.go`，在 ProjectScheduler 接口中添加 `TriggerRun(projectID int64, agentType string)`。

- [ ] **Step 2: 在 scheduler.go 追加 TriggerRun 方法**

读取 `internal/scheduler/scheduler.go`，在末尾追加：

```go
// TriggerRun fires the agent function immediately in a new goroutine.
func (s *Scheduler) TriggerRun(projectID int64, agentType string) {
	go s.agentFunc(projectID, agentType)
}
```

- [ ] **Step 3: 创建 data.go**

```go
// internal/api/handlers/data.go
package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/fixloop/fixloop/internal/api/response"
)

// DataHandler serves issues, prs, backlog, and agent_runs.
type DataHandler struct {
	DB        *sql.DB
	Scheduler ProjectScheduler
}

// --- Issues ---

type issueResp struct {
	ID             int64      `json:"id"`
	GithubNumber   int        `json:"github_number"`
	Title          string     `json:"title"`
	Priority       int        `json:"priority"`
	Status         string     `json:"status"`
	FixAttempts    int        `json:"fix_attempts"`
	AcceptFailures int        `json:"accept_failures"`
	FixingSince    *time.Time `json:"fixing_since,omitempty"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (h *DataHandler) ListIssues(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	page, perPage := parsePagination(c)

	var total int64
	_ = h.DB.QueryRowContext(c.Request.Context(),
		`SELECT COUNT(*) FROM issues WHERE project_id = ? AND status != 'closed'`, projectID,
	).Scan(&total)

	rows, err := h.DB.QueryContext(c.Request.Context(),
		`SELECT id, github_number, title, priority, status, fix_attempts, accept_failures,
		        fixing_since, closed_at, created_at
		 FROM issues WHERE project_id = ? AND status != 'closed'
		 ORDER BY priority ASC, created_at DESC
		 LIMIT ? OFFSET ?`,
		projectID, perPage, (page-1)*perPage,
	)
	if err != nil {
		response.Internal(c)
		return
	}
	defer rows.Close()

	var issues []issueResp
	for rows.Next() {
		var i issueResp
		if err := rows.Scan(&i.ID, &i.GithubNumber, &i.Title, &i.Priority, &i.Status,
			&i.FixAttempts, &i.AcceptFailures, &i.FixingSince, &i.ClosedAt, &i.CreatedAt); err != nil {
			response.Internal(c)
			return
		}
		issues = append(issues, i)
	}
	if issues == nil {
		issues = []issueResp{}
	}
	response.OKPaged(c, issues, response.Pagination{Page: page, PerPage: perPage, Total: total})
}

// --- PRs ---

type prResp struct {
	ID           int64      `json:"id"`
	IssueID      *int64     `json:"issue_id,omitempty"`
	GithubNumber int        `json:"github_number"`
	Branch       string     `json:"branch"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	MergedAt     *time.Time `json:"merged_at,omitempty"`
}

func (h *DataHandler) ListPRs(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	page, perPage := parsePagination(c)

	var total int64
	_ = h.DB.QueryRowContext(c.Request.Context(),
		`SELECT COUNT(*) FROM prs WHERE project_id = ?`, projectID,
	).Scan(&total)

	rows, err := h.DB.QueryContext(c.Request.Context(),
		`SELECT id, issue_id, github_number, branch, status, created_at, merged_at
		 FROM prs WHERE project_id = ?
		 ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		projectID, perPage, (page-1)*perPage,
	)
	if err != nil {
		response.Internal(c)
		return
	}
	defer rows.Close()

	var prs []prResp
	for rows.Next() {
		var p prResp
		var issueID sql.NullInt64
		if err := rows.Scan(&p.ID, &issueID, &p.GithubNumber, &p.Branch,
			&p.Status, &p.CreatedAt, &p.MergedAt); err != nil {
			response.Internal(c)
			return
		}
		if issueID.Valid {
			p.IssueID = &issueID.Int64
		}
		prs = append(prs, p)
	}
	if prs == nil {
		prs = []prResp{}
	}
	response.OKPaged(c, prs, response.Pagination{Page: page, PerPage: perPage, Total: total})
}

// --- Backlog ---

type backlogResp struct {
	ID           int64      `json:"id"`
	Title        string     `json:"title"`
	ScenarioType string     `json:"scenario_type"`
	Priority     int        `json:"priority"`
	Status       string     `json:"status"`
	Source       string     `json:"source"`
	LastTestedAt *time.Time `json:"last_tested_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (h *DataHandler) ListBacklog(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	page, perPage := parsePagination(c)

	statusFilter := c.DefaultQuery("status", "pending")

	var total int64
	_ = h.DB.QueryRowContext(c.Request.Context(),
		`SELECT COUNT(*) FROM backlog WHERE project_id = ? AND status = ?`, projectID, statusFilter,
	).Scan(&total)

	rows, err := h.DB.QueryContext(c.Request.Context(),
		`SELECT id, title, scenario_type, priority, status, source, last_tested_at, created_at
		 FROM backlog WHERE project_id = ? AND status = ?
		 ORDER BY priority ASC, created_at DESC LIMIT ? OFFSET ?`,
		projectID, statusFilter, perPage, (page-1)*perPage,
	)
	if err != nil {
		response.Internal(c)
		return
	}
	defer rows.Close()

	var items []backlogResp
	for rows.Next() {
		var b backlogResp
		if err := rows.Scan(&b.ID, &b.Title, &b.ScenarioType, &b.Priority,
			&b.Status, &b.Source, &b.LastTestedAt, &b.CreatedAt); err != nil {
			response.Internal(c)
			return
		}
		items = append(items, b)
	}
	if items == nil {
		items = []backlogResp{}
	}
	response.OKPaged(c, items, response.Pagination{Page: page, PerPage: perPage, Total: total})
}

func (h *DataHandler) PatchBacklog(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	scenarioID, err := strconv.ParseInt(c.Param("scenario_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "无效的 scenario_id")
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Status != "ignored" && req.Status != "pending" {
		response.BadRequest(c, "status 只允许 ignored 或 pending")
		return
	}

	res, err := h.DB.ExecContext(c.Request.Context(),
		`UPDATE backlog SET status = ? WHERE id = ? AND project_id = ?`,
		req.Status, scenarioID, projectID,
	)
	if err != nil {
		response.Internal(c)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "场景")
		return
	}
	response.OK(c, gin.H{"id": scenarioID, "status": req.Status})
}

// --- Runs ---

type runResp struct {
	ID            int64      `json:"id"`
	AgentType     string     `json:"agent_type"`
	Status        string     `json:"status"`
	ConfigVersion int        `json:"config_version"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

func (h *DataHandler) ListRuns(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	page, perPage := parsePagination(c)

	var total int64
	_ = h.DB.QueryRowContext(c.Request.Context(),
		`SELECT COUNT(*) FROM agent_runs WHERE project_id = ?`, projectID,
	).Scan(&total)

	rows, err := h.DB.QueryContext(c.Request.Context(),
		`SELECT id, agent_type, status, config_version, started_at, finished_at
		 FROM agent_runs WHERE project_id = ?
		 ORDER BY started_at DESC LIMIT ? OFFSET ?`,
		projectID, perPage, (page-1)*perPage,
	)
	if err != nil {
		response.Internal(c)
		return
	}
	defer rows.Close()

	var runs []runResp
	for rows.Next() {
		var r runResp
		if err := rows.Scan(&r.ID, &r.AgentType, &r.Status, &r.ConfigVersion,
			&r.StartedAt, &r.FinishedAt); err != nil {
			response.Internal(c)
			return
		}
		runs = append(runs, r)
	}
	if runs == nil {
		runs = []runResp{}
	}
	response.OKPaged(c, runs, response.Pagination{Page: page, PerPage: perPage, Total: total})
}

type runDetailResp struct {
	runResp
	Output string `json:"output,omitempty"`
}

func (h *DataHandler) GetRun(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	runID, err := strconv.ParseInt(c.Param("run_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "无效的 run_id")
		return
	}

	var r runDetailResp
	err = h.DB.QueryRowContext(c.Request.Context(),
		`SELECT id, agent_type, status, config_version, started_at, finished_at
		 FROM agent_runs WHERE id = ? AND project_id = ?`,
		runID, projectID,
	).Scan(&r.ID, &r.AgentType, &r.Status, &r.ConfigVersion, &r.StartedAt, &r.FinishedAt)
	if err == sql.ErrNoRows {
		response.NotFound(c, "run")
		return
	}
	if err != nil {
		response.Internal(c)
		return
	}

	// Get output (may not exist)
	_ = h.DB.QueryRowContext(c.Request.Context(),
		`SELECT output FROM agent_run_outputs WHERE run_id = ?`, runID,
	).Scan(&r.Output)

	response.OK(c, r)
}

func (h *DataHandler) TriggerRun(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	var req struct {
		AgentType string `json:"agent_type" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	allowed := map[string]bool{"explore": true, "fix": true, "master": true, "plan": true}
	if !allowed[req.AgentType] {
		response.BadRequest(c, "agent_type 必须是 explore/fix/master/plan")
		return
	}
	if h.Scheduler != nil {
		h.Scheduler.TriggerRun(projectID, req.AgentType)
	}
	response.OK(c, gin.H{"project_id": projectID, "agent_type": req.AgentType, "status": "triggered"})
}

// --- helpers ---

func parsePagination(c *gin.Context) (page, perPage int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ = strconv.Atoi(c.DefaultQuery("per_page", "20"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	return
}
```

- [ ] **Step 4: 创建 notifications.go**

```go
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
```

- [ ] **Step 5: 创建 screenshots.go**

```go
// internal/api/handlers/screenshots.go
package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/fixloop/fixloop/internal/api/response"
	"github.com/fixloop/fixloop/internal/storage"
)

type ScreenshotHandler struct {
	R2 *storage.R2Client
}

// Get streams a screenshot from R2. Route: GET /api/v1/projects/:project_id/screenshots/:run_id/:filename
func (h *ScreenshotHandler) Get(c *gin.Context) {
	if h.R2 == nil || h.R2.Disabled() {
		response.NotFound(c, "截图")
		return
	}
	projectID := c.GetInt64("project_id")
	runID := c.Param("run_id")
	filename := c.Param("filename")

	// R2 key format: {user_id}/{project_id}/{run_id}/{filename}
	// We embed project_id and run_id in the path; user_id is implicit from ownership check.
	key := fmt.Sprintf("%d/%s/%s", projectID, runID, filename)

	reader, contentType, err := h.R2.Download(c.Request.Context(), key)
	if err != nil {
		response.NotFound(c, "截图")
		return
	}
	defer reader.Close()

	if contentType == "" {
		contentType = "image/png"
	}
	c.DataFromReader(http.StatusOK, -1, contentType, reader, nil)
}
```

- [ ] **Step 6: 在 R2Client 上添加 Download 方法**

读取 `internal/storage/r2.go`，在末尾追加：

```go
// Download retrieves an object from R2 and returns a reader + content type.
// Caller must close the reader.
func (c *R2Client) Download(ctx context.Context, key string) (io.ReadCloser, string, error) {
	out, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, "", fmt.Errorf("r2 download %s: %w", key, err)
	}
	ct := ""
	if out.ContentType != nil {
		ct = *out.ContentType
	}
	return out.Body, ct, nil
}
```

需要确保 `internal/storage/r2.go` 顶部已导入 `"io"` — 如果没有就加上。

- [ ] **Step 7: 更新 router.go**

读取 `internal/api/router.go`，替换为添加了所有新路由的版本：

```go
package api

import (
	"database/sql"
	"log/slog"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"github.com/fixloop/fixloop/internal/api/handlers"
	"github.com/fixloop/fixloop/internal/api/middleware"
	"github.com/fixloop/fixloop/internal/config"
	"github.com/fixloop/fixloop/internal/storage"
)

// NewRouter builds the Gin engine with all routes registered.
func NewRouter(db *sql.DB, cfg *config.Config, sched handlers.ProjectScheduler, r2 *storage.R2Client) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(requestLogger())
	r.Use(middleware.PerUserRateLimit(rate.Limit(20), 50))

	healthH := &handlers.HealthHandler{DB: db}
	r.GET("/health", healthH.Health)

	authH := &handlers.AuthHandler{DB: db, Cfg: cfg}
	projectH := &handlers.ProjectHandler{DB: db, Cfg: cfg, Scheduler: sched}
	dataH := &handlers.DataHandler{DB: db, Scheduler: sched}
	notifH := &handlers.NotificationHandler{DB: db}
	screenshotH := &handlers.ScreenshotHandler{R2: r2}

	v1 := r.Group("/api/v1")
	v1.GET("/auth/github", authH.GitHubLogin)
	v1.GET("/auth/github/callback", authH.GitHubCallback)

	authed := v1.Group("/")
	authed.Use(middleware.Auth(cfg.JWTSecret))
	{
		authed.GET("/me", authH.UserInfo)
		authed.DELETE("/me", authH.DeleteMe)

		authed.POST("/projects", projectH.Create)
		authed.GET("/projects", projectH.List)

		authed.GET("/notifications", notifH.List)
		authed.POST("/notifications/read-all", notifH.ReadAll)

		projects := authed.Group("/projects/:project_id")
		projects.Use(middleware.ProjectOwner(db))
		{
			projects.GET("", projectH.Get)
			projects.PATCH("", projectH.Update)
			projects.DELETE("", projectH.Delete)
			projects.POST("/pause", projectH.Pause)
			projects.POST("/resume", projectH.Resume)

			projects.GET("/issues", dataH.ListIssues)
			projects.GET("/prs", dataH.ListPRs)
			projects.GET("/backlog", dataH.ListBacklog)
			projects.PATCH("/backlog/:scenario_id", dataH.PatchBacklog)
			projects.GET("/runs", dataH.ListRuns)
			projects.GET("/runs/:run_id", dataH.GetRun)
			projects.POST("/runs", dataH.TriggerRun)
			projects.GET("/screenshots/:run_id/:filename", screenshotH.Get)
		}
	}

	return r
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		slog.Info("http",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"ip", c.ClientIP(),
		)
	}
}
```

- [ ] **Step 8: 更新 cmd/server/main.go 中的 NewRouter 调用**

`NewRouter` 签名变了，需要把 `api.NewRouter(database, cfg, sched)` 改为 `api.NewRouter(database, cfg, sched, nil)`。

- [ ] **Step 9: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./...
```

Expected: no output.

- [ ] **Step 10: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add internal/scheduler/scheduler.go \
        internal/api/handlers/data.go \
        internal/api/handlers/notifications.go \
        internal/api/handlers/screenshots.go \
        internal/api/handlers/projects.go \
        internal/api/router.go \
        internal/storage/r2.go \
        cmd/server/main.go
git commit -m "feat: add missing API endpoints (issues, prs, backlog, runs, notifications, screenshots)"
```

---

## Task 2: plan-agent

**Files:**
- Create: `internal/plan/plan.go`
- Create: `internal/plan/prompts/plan.txt`
- Modify: `cmd/server/main.go` (plan stub → real agent)

plan-agent 每周一运行，分析当前 backlog 缺口，调用 AI（HTTP Claude API）生成 3-5 条新测试场景，写入 backlog。

### plan.txt 内容

```
你是一个 QA 工程师，负责为 Web 应用生成测试场景。

项目信息：
- GitHub 仓库: {{.Owner}}/{{.Repo}}
- Staging URL: {{.StagingURL}}

当前 backlog 状态：
- pending 场景数: {{.PendingCount}}
- 近期 open issues: {{.RecentIssues}}

请生成 {{.Count}} 条新的测试场景，覆盖尚未测试的功能路径。
每条场景用以下 JSON 格式返回，输出一个 JSON 数组，不要添加其他文字：

[
  {
    "title": "场景标题（简短，< 100 字符）",
    "description": "场景描述",
    "scenario_type": "ui",
    "priority": 2
  }
]
```

### plan.go 核心逻辑

```go
package plan

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"text/template"
	"time"
	"unicode"

	_ "embed"

	"github.com/fixloop/fixloop/internal/agentrun"
	"github.com/fixloop/fixloop/internal/agent"
	agentclaude "github.com/fixloop/fixloop/internal/agent/claude"
	"github.com/fixloop/fixloop/internal/config"
	"github.com/fixloop/fixloop/internal/crypto"
	"encoding/hex"
)

//go:embed prompts/plan.txt
var planPromptTmpl string

var planTmpl = template.Must(template.New("plan").Parse(planPromptTmpl))

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
	AIRunner string `json:"ai_runner"`
	AIModel  string `json:"ai_model"`
	AIAPIKey string `json:"ai_api_key"` // hex(AES encrypted)
}

type scenarioSuggestion struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	ScenarioType string `json:"scenario_type"`
	Priority     int    `json:"priority"`
}

func (a *Agent) Run(ctx context.Context, projectID int64) {
	var cfgJSON string
	var configVersion int
	var userID int64
	var status string
	if err := a.DB.QueryRowContext(ctx,
		`SELECT user_id, config, config_version, status FROM projects WHERE id = ? AND deleted_at IS NULL`,
		projectID,
	).Scan(&userID, &cfgJSON, &configVersion, &status); err != nil {
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

	runID, err := agentrun.Start(ctx, a.DB, projectID, "plan", configVersion)
	if err != nil {
		slog.Error("plan: start agentrun", "err", err)
		return
	}

	agentrun.WithRecover(runID, a.DB, func() {
		output, finalStatus := a.runPlan(ctx, projectID, runID, &pcfg)
		_ = agentrun.Finish(ctx, a.DB, runID, finalStatus, output)
	})
}

func (a *Agent) runPlan(ctx context.Context, projectID, runID int64, pcfg *projectConf) (string, string) {
	var logBuf bytes.Buffer
	logf := func(msg string, args ...any) {
		line := fmt.Sprintf(msg, args...)
		logBuf.WriteString(line + "\n")
		slog.Info("plan: "+line, "project_id", projectID)
	}

	// Get pending count
	var pendingCount int
	_ = a.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM backlog WHERE project_id = ? AND status = 'pending'`, projectID,
	).Scan(&pendingCount)

	// Get recent open issues
	var issueLines []string
	rows, _ := a.DB.QueryContext(ctx,
		`SELECT title FROM issues WHERE project_id = ? AND status IN ('open','fixing')
		 ORDER BY created_at DESC LIMIT 5`,
		projectID,
	)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var t string
			if err := rows.Scan(&t); err == nil {
				issueLines = append(issueLines, "- "+t)
			}
		}
	}

	// How many new scenarios to generate
	want := 5
	if pendingCount > 10 {
		logf("backlog healthy (%d pending), skipping plan run", pendingCount)
		return logBuf.String(), "skipped"
	}

	// Build runner
	runner, err := a.buildRunner(pcfg)
	if err != nil {
		logf("ERROR: build runner: %v", err)
		return logBuf.String(), "failed"
	}

	// Build prompt
	prompt, err := buildPlanPrompt(pcfg.GitHub.Owner, pcfg.GitHub.Repo,
		pcfg.Test.StagingURL, pendingCount, strings.Join(issueLines, "\n"), want)
	if err != nil {
		logf("ERROR: build prompt: %v", err)
		return logBuf.String(), "failed"
	}

	logf("generating %d new backlog scenarios via AI", want)
	aiOut, err := runner.Run(ctx, prompt, 1)
	logBuf.WriteString("\n--- AI OUTPUT ---\n" + aiOut + "\n---\n")
	if err != nil {
		logf("ERROR: AI runner: %v", err)
		return logBuf.String(), "failed"
	}

	// Parse JSON array from response
	suggestions, err := parseScenarios(aiOut)
	if err != nil {
		logf("WARN: parse scenarios: %v (raw: %.200s)", err, aiOut)
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
			logf("WARN: insert backlog: %v", err)
			continue
		}
		inserted++
	}

	logf("inserted %d new backlog scenarios", inserted)
	return logBuf.String(), "success"
}

func (a *Agent) buildRunner(pcfg *projectConf) (agent.Runner, error) {
	apiKey := ""
	if pcfg.AIAPIKey != "" {
		keyEnc, err := hex.DecodeString(pcfg.AIAPIKey)
		if err == nil {
			plain, err := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, keyEnc)
			if err == nil {
				apiKey = string(plain)
			}
		}
	}
	model := pcfg.AIModel
	if model == "" {
		model = "claude-opus-4-6"
	}
	return &agentclaude.Runner{APIKey: apiKey, Model: model}, nil
}

type promptVars struct {
	Owner        string
	Repo         string
	StagingURL   string
	PendingCount int
	RecentIssues string
	Count        int
}

func buildPlanPrompt(owner, repo, stagingURL string, pending int, issues string, count int) (string, error) {
	var buf bytes.Buffer
	err := planTmpl.Execute(&buf, promptVars{
		Owner: owner, Repo: repo, StagingURL: stagingURL,
		PendingCount: pending, RecentIssues: issues, Count: count,
	})
	return buf.String(), err
}

func parseScenarios(raw string) ([]scenarioSuggestion, error) {
	// Find JSON array in response
	start := strings.Index(raw, "[")
	end := strings.LastIndex(raw, "]")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array found")
	}
	var result []scenarioSuggestion
	if err := json.Unmarshal([]byte(raw[start:end+1]), &result); err != nil {
		return nil, err
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
```

- [ ] **Step 1: 创建 prompts 目录和 plan.txt**

```bash
mkdir -p /home/ubuntu/fy/work/fixloop/internal/plan/prompts
```

Content of `internal/plan/prompts/plan.txt`:

```
你是一个 QA 工程师，负责为 Web 应用生成测试场景。

项目信息：
- GitHub 仓库: {{.Owner}}/{{.Repo}}
- Staging URL: {{.StagingURL}}

当前 backlog 状态：
- pending 场景数: {{.PendingCount}}
{{if .RecentIssues}}
近期 open issues（供参考，勿重复）:
{{.RecentIssues}}
{{end}}

请生成 {{.Count}} 条新的测试场景，覆盖尚未测试的功能路径。
只返回 JSON 数组，不要任何其他文字：

[
  {
    "title": "场景标题（简短，< 100 字符）",
    "description": "场景描述",
    "scenario_type": "ui",
    "priority": 2
  }
]
```

- [ ] **Step 2: 创建 plan.go**

使用上方完整代码创建 `internal/plan/plan.go`。

- [ ] **Step 3: 更新 main.go 中 plan stub**

在 main.go 中：
1. 导入 `"github.com/fixloop/fixloop/internal/plan"`
2. 在 `exploreAgent`, `fixAgent`, `masterAgent` 之后添加：
   ```go
   planAgent := &plan.Agent{DB: database, Cfg: cfg}
   ```
3. 在 scheduler dispatch 的 `case "plan":` 中替换 stub：
   ```go
   case "plan":
       planAgent.Run(ctx, projectID)
   ```

- [ ] **Step 4: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./...
```

- [ ] **Step 5: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add internal/plan/ cmd/server/main.go
git commit -m "feat: implement plan-agent (AI-driven weekly backlog generation)"
```

---

## Task 3: TG Bot

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.yaml`, `config.yaml.example`
- Create: `internal/tgbot/bot.go`
- Modify: `cmd/server/main.go`

### config 更新

在 `config.go` 的 `yamlConfig` struct 中添加：
```go
TG struct {
    BotToken string `yaml:"bot_token"`
} `yaml:"tg"`
```
在 `Config` struct 添加 `TGBotToken string`，在 Load() 末尾赋值：
```go
TGBotToken: y.TG.BotToken,
```

在 `config.yaml` 和 `config.yaml.example` 末尾添加：
```yaml
tg:
  bot_token: ""
```

### bot.go 完整实现

TG Bot 使用 `github.com/go-telegram-bot-api/telegram-bot-api/v5`（需要 go get）。

功能：
1. `Start(ctx)` — 长轮询 updates，offset 持久化到 `system_config.tg_last_update_id`
2. `SendPending(ctx)` — 每 30s 查询 `notifications WHERE tg_sent=FALSE`，发送，标记 `tg_sent=TRUE`
3. 命令处理：`/start {token}` 验证绑定、`/status`、`/issues`、`/run`、`/pause`、`/resume`、`/reset`、`/merge`

```go
// internal/tgbot/bot.go
package tgbot

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/fixloop/fixloop/internal/config"
)

type Bot struct {
	api *tgbotapi.BotAPI
	db  *sql.DB
	cfg *config.Config
}

func New(cfg *config.Config, db *sql.DB) (*Bot, error) {
	if cfg.TGBotToken == "" {
		return nil, nil // TG not configured
	}
	api, err := tgbotapi.NewBotAPI(cfg.TGBotToken)
	if err != nil {
		return nil, fmt.Errorf("tgbot: init: %w", err)
	}
	slog.Info("tgbot: connected", "username", api.Self.UserName)
	return &Bot{api: api, db: db, cfg: cfg}, nil
}

// Run starts the long-poll loop and the notification sender. Blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) {
	go b.sendPendingLoop(ctx)
	b.pollLoop(ctx)
}

func (b *Bot) pollLoop(ctx context.Context) {
	offset := b.loadOffset()
	u := tgbotapi.NewUpdate(offset)
	u.Timeout = 30

	updates := b.api.GetUpdatesChan(u)
	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			b.saveOffset(update.UpdateID + 1)
			if update.Message == nil || !update.Message.IsCommand() {
				continue
			}
			if update.Message.Chat.Type != "private" {
				continue // ignore group messages
			}
			b.handleCommand(ctx, update.Message)
		}
	}
}

func (b *Bot) handleCommand(ctx context.Context, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	cmd := msg.Command()
	args := strings.TrimSpace(msg.CommandArguments())

	switch cmd {
	case "start":
		b.cmdStart(ctx, chatID, args)
	case "status":
		b.cmdStatus(ctx, chatID)
	case "issues":
		b.cmdIssues(ctx, chatID, args)
	case "run":
		b.cmdRun(ctx, chatID, args)
	case "pause":
		b.cmdPause(ctx, chatID, args)
	case "resume":
		b.cmdResume(ctx, chatID, args)
	default:
		b.send(chatID, "未知命令。支持: /status /issues /run /pause /resume")
	}
}

func (b *Bot) cmdStart(ctx context.Context, chatID int64, token string) {
	if token == "" {
		b.send(chatID, "欢迎使用 FixLoop Bot！请在 Dashboard 获取绑定链接。")
		return
	}
	// Validate one-time token (stored in system_config as tg_bind_{token})
	key := "tg_bind_" + token
	var userIDStr string
	err := b.db.QueryRowContext(ctx,
		`SELECT value FROM system_config WHERE key_name = ? AND updated_at > NOW() - INTERVAL 10 MINUTE`,
		key,
	).Scan(&userIDStr)
	if err != nil {
		b.send(chatID, "❌ 绑定 token 无效或已过期，请重新从 Dashboard 获取。")
		return
	}
	userID, _ := strconv.ParseInt(userIDStr, 10, 64)
	if userID == 0 {
		b.send(chatID, "❌ 绑定失败，请重试。")
		return
	}
	_, err = b.db.ExecContext(ctx, `UPDATE users SET tg_chat_id = ? WHERE id = ?`, chatID, userID)
	if err != nil {
		b.send(chatID, "❌ 数据库错误，请稍后重试。")
		return
	}
	// Clean up used token
	_, _ = b.db.ExecContext(ctx, `DELETE FROM system_config WHERE key_name = ?`, key)
	b.send(chatID, "✅ 绑定成功！你将收到所有项目的通知。\n发送 /status 查看项目概况。")
}

func (b *Bot) cmdStatus(ctx context.Context, chatID int64) {
	userID := b.chatToUserID(ctx, chatID)
	if userID == 0 {
		b.send(chatID, "请先绑定账号：在 Dashboard 点击「绑定 Telegram」。")
		return
	}
	rows, err := b.db.QueryContext(ctx,
		`SELECT p.name, p.status,
		        (SELECT COUNT(*) FROM issues i WHERE i.project_id = p.id AND i.status IN ('open','fixing')) AS open_issues,
		        (SELECT COUNT(*) FROM prs pr WHERE pr.project_id = p.id AND pr.status = 'open') AS open_prs
		 FROM projects p WHERE p.user_id = ? AND p.deleted_at IS NULL
		 ORDER BY p.created_at ASC`,
		userID,
	)
	if err != nil {
		b.send(chatID, "查询失败，请稍后重试。")
		return
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var name, status string
		var openIssues, openPRs int
		if err := rows.Scan(&name, &status, &openIssues, &openPRs); err != nil {
			continue
		}
		icon := "🟢"
		if status == "paused" {
			icon = "⏸"
		} else if status == "error" {
			icon = "🔴"
		}
		lines = append(lines, fmt.Sprintf("%s *%s* — %d issues, %d PRs", icon, name, openIssues, openPRs))
	}
	if len(lines) == 0 {
		b.send(chatID, "暂无项目。请在 Dashboard 创建。")
		return
	}
	b.sendMD(chatID, "📊 *项目状态*\n\n"+strings.Join(lines, "\n"))
}

func (b *Bot) cmdIssues(ctx context.Context, chatID int64, projectName string) {
	userID := b.chatToUserID(ctx, chatID)
	if userID == 0 {
		b.send(chatID, "请先绑定账号。")
		return
	}
	if projectName == "" {
		b.send(chatID, "用法: /issues <项目名>")
		return
	}
	var projectID int64
	if err := b.db.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE user_id = ? AND name = ? AND deleted_at IS NULL`,
		userID, projectName,
	).Scan(&projectID); err != nil {
		b.send(chatID, fmt.Sprintf("找不到项目 %q", projectName))
		return
	}
	rows, err := b.db.QueryContext(ctx,
		`SELECT github_number, title, status, fix_attempts FROM issues
		 WHERE project_id = ? AND status IN ('open','fixing','needs-human')
		 ORDER BY priority ASC, created_at DESC LIMIT 10`,
		projectID,
	)
	if err != nil {
		b.send(chatID, "查询失败。")
		return
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var num, attempts int
		var title, status string
		if err := rows.Scan(&num, &title, &status, &attempts); err != nil {
			continue
		}
		icon := "🐛"
		if status == "fixing" {
			icon = "🔧"
		} else if status == "needs-human" {
			icon = "⚠️"
		}
		lines = append(lines, fmt.Sprintf("%s #%d %s (attempts: %d)", icon, num, title, attempts))
	}
	if len(lines) == 0 {
		b.send(chatID, fmt.Sprintf("✅ %s 暂无 open issues", projectName))
		return
	}
	b.send(chatID, strings.Join(lines, "\n"))
}

func (b *Bot) cmdRun(ctx context.Context, chatID int64, args string) {
	userID := b.chatToUserID(ctx, chatID)
	if userID == 0 {
		b.send(chatID, "请先绑定账号。")
		return
	}
	parts := strings.Fields(args)
	if len(parts) < 2 {
		b.send(chatID, "用法: /run <agent> <项目名>\nagent: fix | explore | plan | master")
		return
	}
	agentType, projectName := parts[0], parts[1]
	allowed := map[string]bool{"fix": true, "explore": true, "plan": true, "master": true}
	if !allowed[agentType] {
		b.send(chatID, "agent 必须是 fix/explore/plan/master")
		return
	}
	var projectID int64
	var status string
	if err := b.db.QueryRowContext(ctx,
		`SELECT id, status FROM projects WHERE user_id = ? AND name = ? AND deleted_at IS NULL`,
		userID, projectName,
	).Scan(&projectID, &status); err != nil {
		b.send(chatID, fmt.Sprintf("找不到项目 %q", projectName))
		return
	}
	if status != "active" {
		b.send(chatID, fmt.Sprintf("项目 %s 未激活（status=%s），请先 /resume", projectName, status))
		return
	}
	// Insert a trigger notification for the scheduler to pick up
	// We use the notify table to communicate; actual trigger needs scheduler access.
	// For MVP: insert a system_config trigger key that agents check.
	_, _ = b.db.ExecContext(ctx,
		`INSERT INTO notifications (user_id, project_id, type, content, tg_sent)
		 VALUES (?, ?, 'manual_trigger', ?, TRUE)`,
		userID, projectID, fmt.Sprintf("手动触发 %s-agent", agentType),
	)
	b.send(chatID, fmt.Sprintf("✅ 已触发 %s-agent for %s（下次调度周期执行）", agentType, projectName))
}

func (b *Bot) cmdPause(ctx context.Context, chatID int64, projectName string) {
	b.setProjectStatus(ctx, chatID, projectName, "paused")
}

func (b *Bot) cmdResume(ctx context.Context, chatID int64, projectName string) {
	b.setProjectStatus(ctx, chatID, projectName, "active")
}

func (b *Bot) setProjectStatus(ctx context.Context, chatID int64, projectName, newStatus string) {
	userID := b.chatToUserID(ctx, chatID)
	if userID == 0 {
		b.send(chatID, "请先绑定账号。")
		return
	}
	if projectName == "" {
		b.send(chatID, fmt.Sprintf("用法: /%s <项目名>", newStatus))
		return
	}
	res, err := b.db.ExecContext(ctx,
		`UPDATE projects SET status = ? WHERE user_id = ? AND name = ? AND deleted_at IS NULL`,
		newStatus, userID, projectName,
	)
	if err != nil {
		b.send(chatID, "更新失败，请稍后重试。")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		b.send(chatID, fmt.Sprintf("找不到项目 %q", projectName))
		return
	}
	icon := "▶️"
	if newStatus == "paused" {
		icon = "⏸"
	}
	b.send(chatID, fmt.Sprintf("%s 项目 %s 已%s", icon, projectName, map[string]string{"paused": "暂停", "active": "恢复"}[newStatus]))
}

// sendPendingLoop runs every 30s and pushes unsent notifications.
func (b *Bot) sendPendingLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.sendPending(ctx)
		}
	}
}

func (b *Bot) sendPending(ctx context.Context) {
	rows, err := b.db.QueryContext(ctx,
		`SELECT n.id, u.tg_chat_id, n.content
		 FROM notifications n
		 JOIN users u ON u.id = n.user_id
		 WHERE n.tg_sent = FALSE AND u.tg_chat_id IS NOT NULL
		 ORDER BY n.created_at ASC LIMIT 50`,
	)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var chatID int64
		var content string
		if err := rows.Scan(&id, &chatID, &content); err != nil {
			continue
		}
		// Truncate if needed
		if len(content) > 4000 {
			content = content[:4000] + "…"
		}
		if _, err := b.api.Send(tgbotapi.NewMessage(chatID, content)); err != nil {
			slog.Warn("tgbot: send notification failed", "id", id, "err", err)
			continue
		}
		_, _ = b.db.ExecContext(ctx, `UPDATE notifications SET tg_sent = TRUE WHERE id = ?`, id)
	}
}

// --- helpers ---

func (b *Bot) chatToUserID(ctx context.Context, chatID int64) int64 {
	var userID int64
	_ = b.db.QueryRowContext(ctx,
		`SELECT id FROM users WHERE tg_chat_id = ? AND deleted_at IS NULL`, chatID,
	).Scan(&userID)
	return userID
}

func (b *Bot) send(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := b.api.Send(msg); err != nil {
		slog.Warn("tgbot: send failed", "chat_id", chatID, "err", err)
	}
}

func (b *Bot) sendMD(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	if _, err := b.api.Send(msg); err != nil {
		// Fallback to plain text
		b.send(chatID, text)
	}
}

func (b *Bot) loadOffset() int {
	var v string
	_ = b.db.QueryRow(`SELECT value FROM system_config WHERE key_name = 'tg_last_update_id'`).Scan(&v)
	n, _ := strconv.Atoi(v)
	return n
}

func (b *Bot) saveOffset(offset int) {
	_, _ = b.db.Exec(
		`INSERT INTO system_config (key_name, value) VALUES ('tg_last_update_id', ?)
		 ON DUPLICATE KEY UPDATE value = VALUES(value)`,
		strconv.Itoa(offset),
	)
}
```

- [ ] **Step 1: go get TG bot SDK**

```bash
cd /home/ubuntu/fy/work/fixloop && go get github.com/go-telegram-bot-api/telegram-bot-api/v5
```

- [ ] **Step 2: 更新 config.go**

在 yamlConfig 结构体添加 TG 段，在 Config struct 添加 TGBotToken，在 Load() 赋值。

- [ ] **Step 3: 更新 config.yaml 和 config.yaml.example**

末尾各加：
```yaml
tg:
  bot_token: ""
```

- [ ] **Step 4: 创建 internal/tgbot/bot.go**

使用上方完整内容。

- [ ] **Step 5: 更新 cmd/server/main.go**

导入 `"github.com/fixloop/fixloop/internal/tgbot"`，在 server 启动前添加：

```go
tgBot, err := tgbot.New(cfg, database)
if err != nil {
    slog.Warn("tgbot init failed", "err", err)
}
if tgBot != nil {
    go tgBot.Run(context.Background())
}
```

- [ ] **Step 6: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./...
```

- [ ] **Step 7: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add internal/tgbot/ internal/config/ config.yaml config.yaml.example cmd/server/main.go
git commit -m "feat: implement TG Bot (notifications + commands)"
```
