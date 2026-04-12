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
	agentH := &handlers.AgentHandler{DB: db, Scheduler: sched}
	dataH := &handlers.DataHandler{DB: db, Scheduler: sched}
	notifH := &handlers.NotificationHandler{DB: db}
	screenshotH := &handlers.ScreenshotHandler{R2: r2}
	adminH := &handlers.AdminHandler{DB: db, Cfg: cfg}
	webhookH := &handlers.WebhookHandler{DB: db, Scheduler: sched}

	// Public webhook — no auth required, validated by per-project token
	r.POST("/webhook/projects/:project_id/trigger", webhookH.Trigger)

	v1 := r.Group("/api/v1")
	v1.GET("/auth/github", authH.GitHubLogin)
	v1.GET("/auth/github/callback", authH.GitHubCallback)

	authed := v1.Group("/")
	authed.Use(middleware.Auth(cfg.JWTSecret))
	{
		authed.GET("/me", authH.UserInfo)
		authed.DELETE("/me", authH.DeleteMe)
		authed.POST("/me/tg-bind", authH.TGBind)

		authed.GET("/admin/tg-config", adminH.GetTGConfig)
		authed.PATCH("/admin/tg-config", adminH.PatchTGConfig)
		authed.POST("/admin/tg-config/verify", adminH.VerifyTGToken)
		authed.GET("/admin/tg-chats", adminH.GetTGChats)
		authed.GET("/admin/workspace", adminH.GetWorkspace)
		authed.POST("/admin/workspace/init", adminH.InitWorkspace)

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
			projects.GET("/screenshots/:run_id/*filename", screenshotH.Get)

			projects.GET("/deploy-key", projectH.GetDeployKey)
			projects.POST("/deploy-key/register", projectH.RegisterDeployKey)
			projects.POST("/deploy-key/confirm", projectH.ConfirmDeployKey)

			projects.GET("/prompt-defaults", projectH.GetPromptDefaults)
			projects.POST("/validate-prompt", projectH.ValidatePrompt)

			projects.GET("/agents", agentH.List)
			projects.POST("/agents", agentH.Create)
			projects.PATCH("/agents/:agent_id", agentH.Update)
			projects.DELETE("/agents/:agent_id", agentH.Delete)

			projects.POST("/webhook-tokens", webhookH.AddToken)
			projects.DELETE("/webhook-tokens/:token", webhookH.RemoveToken)
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
