package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/getsentry/sentry-go"

	api "github.com/fixloop/fixloop/internal/api"
	"github.com/fixloop/fixloop/internal/agentrun"
	"github.com/fixloop/fixloop/internal/config"
	"github.com/fixloop/fixloop/internal/db"
	"github.com/fixloop/fixloop/internal/agents/explore"
	"github.com/fixloop/fixloop/internal/agents/fix"
	"github.com/fixloop/fixloop/internal/agents/generic"
	"github.com/fixloop/fixloop/internal/agents/master"
	"github.com/fixloop/fixloop/internal/agents/plan"
	"github.com/fixloop/fixloop/internal/scheduler"
	"github.com/fixloop/fixloop/internal/storage"
	"github.com/fixloop/fixloop/internal/tgbot"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// Single-instance enforcement via file lock
	lockPath := "/var/run/fixloop.pid"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		// Fallback to /tmp for dev environments
		lockPath = "/tmp/fixloop.pid"
		lockFile, err = os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			slog.Error("cannot create lock file", "err", err)
			os.Exit(1)
		}
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		slog.Error("another fixloop instance is already running")
		os.Exit(1)
	}
	defer lockFile.Close()

	// Cancellable context wired to OS signals for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}

	if cfg.SentryDSN != "" {
		if err := sentry.Init(sentry.ClientOptions{Dsn: cfg.SentryDSN}); err != nil {
			slog.Warn("sentry init failed", "err", err)
		}
	}

	database, err := db.Open(cfg.DatabaseDSN)
	if err != nil {
		slog.Error("database connection failed", "err", err, "unexpected", true)
		os.Exit(1)
	}
	defer database.Close()

	_, filename, _, _ := runtime.Caller(0)
	projectRoot := filepath.Join(filepath.Dir(filename), "../..")
	migrationsPath := filepath.Join(projectRoot, "backend/migrations")
	if err := db.Migrate(cfg.DatabaseDSN, migrationsPath); err != nil {
		slog.Error("migration failed", "err", err, "unexpected", true)
		os.Exit(1)
	}

	// Clean up zombie runs from previous process
	if err := agentrun.AbandonZombies(database); err != nil {
		slog.Warn("zombie cleanup failed", "err", err)
	}

	var r2Client *storage.R2Client
	if cfg.R2AccountID != "" {
		r2Client, err = storage.NewR2Client(cfg.R2AccountID, cfg.R2AccessKeyID, cfg.R2SecretAccessKey, cfg.R2BucketName)
		if err != nil {
			slog.Warn("R2 storage init failed; screenshots disabled", "err", err)
		} else {
			slog.Info("R2 storage connected", "bucket", cfg.R2BucketName)
		}
	}

	exploreAgent := &explore.Agent{DB: database, Cfg: cfg}
	fixAgent := &fix.Agent{DB: database, Cfg: cfg}
	masterAgent := &master.Agent{DB: database, Cfg: cfg, R2: r2Client}
	planAgent := &plan.Agent{DB: database, Cfg: cfg}
	genericAgent := &generic.Agent{DB: database, Cfg: cfg}

	sched, err := scheduler.New(database, func(ctx context.Context, projectID int64, agentType string, projectAgentID int64) {
		switch agentType {
		case "explore":
			exploreAgent.Run(ctx, projectID, projectAgentID)
		case "fix":
			fixAgent.Run(ctx, projectID, projectAgentID)
		case "master":
			masterAgent.Run(ctx, projectID, projectAgentID)
		case "plan":
			planAgent.Run(ctx, projectID, projectAgentID)
		case "generic":
			genericAgent.Run(ctx, projectID, projectAgentID)
		default:
			slog.Warn("unknown agent type", "project_id", projectID, "agent_type", agentType)
		}
	})
	if err != nil {
		slog.Error("scheduler init failed", "err", err)
		os.Exit(1)
	}

	// Register all active projects before starting
	if err := sched.LoadActiveProjects(database); err != nil {
		slog.Warn("failed to load active projects into scheduler", "err", err)
	}
	sched.Start()
	defer func() {
		if err := sched.Stop(); err != nil {
			slog.Warn("scheduler shutdown error", "err", err)
		}
	}()

	tgBot, tgErr := tgbot.New(cfg, database, r2Client)
	if tgErr != nil {
		slog.Warn("tgbot init failed", "err", tgErr)
	}
	if tgBot != nil {
		tgBot.SetScheduler(sched)
		go tgBot.Run(ctx)
	}

	r := api.NewRouter(database, cfg, sched, r2Client)
	slog.Info("starting fixloop server", "port", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		slog.Error("server failed", "err", err, "unexpected", true)
		os.Exit(1)
	}
}
