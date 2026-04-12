package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-co-op/gocron/v2"
)

// ForcedRunKey is a context key used to indicate a manually triggered run
// that should bypass per-agent daily run limits.
type forcedRunKey struct{}

// ForcedRunKey exported value for use in agents.
var ForcedRunKey = forcedRunKey{}

type AgentFunc func(ctx context.Context, projectID int64, agentType string, projectAgentID int64)

type Scheduler struct {
	s         gocron.Scheduler
	agentFunc AgentFunc
	db        *sql.DB
}

func New(db *sql.DB, agentFn AgentFunc) (*Scheduler, error) {
	s, err := gocron.NewScheduler()
	if err != nil {
		return nil, fmt.Errorf("create gocron scheduler: %w", err)
	}
	return &Scheduler{s: s, agentFunc: agentFn, db: db}, nil
}

func (s *Scheduler) Start() {
	s.s.Start()
	slog.Info("scheduler started")
}

func (s *Scheduler) Stop() error {
	return s.s.Shutdown()
}

// RegisterProject queries project_agents for all enabled agents and registers
// one job per agent. Uses DB-stored schedule_minutes. Idempotent.
func (s *Scheduler) RegisterProject(projectID int64) error {
	tag := projectTag(projectID)
	s.s.RemoveByTags(tag)

	rows, err := s.db.Query(
		`SELECT id, agent_type, alias, schedule_minutes FROM project_agents
         WHERE project_id = ? AND enabled = 1`,
		projectID,
	)
	if err != nil {
		return fmt.Errorf("query project_agents: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var agentID int64
		var agentType, alias string
		var mins int
		if err := rows.Scan(&agentID, &agentType, &alias, &mins); err != nil {
			return err
		}
		if mins < 1 {
			mins = 10 // safety floor
		}
		jobTag := fmt.Sprintf("project-%d-%s", projectID, alias)
		_, err := s.s.NewJob(
			gocron.DurationJob(time.Duration(mins)*time.Minute),
			gocron.NewTask(func(pid int64, at string, aid int64) {
				s.agentFunc(context.Background(), pid, at, aid)
			}, projectID, agentType, agentID),
			gocron.WithTags(tag, jobTag),
			gocron.WithSingletonMode(gocron.LimitModeReschedule),
		)
		if err != nil {
			return fmt.Errorf("register agent job %s: %w", alias, err)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	slog.Info("registered project jobs", "project_id", projectID)
	return nil
}

func (s *Scheduler) RemoveProject(projectID int64) {
	s.s.RemoveByTags(projectTag(projectID))
	slog.Info("removed project jobs", "project_id", projectID)
}

// LoadActiveProjects registers jobs for all active projects. Call once on startup.
func (s *Scheduler) LoadActiveProjects(db *sql.DB) error {
	rows, err := db.Query(
		`SELECT id FROM projects WHERE status = 'active' AND deleted_at IS NULL`,
	)
	if err != nil {
		return fmt.Errorf("query active projects: %w", err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if err := s.RegisterProject(id); err != nil {
			slog.Error("failed to register project jobs on startup", "project_id", id, "err", err)
		}
		count++
	}
	slog.Info("loaded active projects into scheduler", "count", count)
	return rows.Err()
}

func projectTag(projectID int64) string {
	return fmt.Sprintf("project-%d", projectID)
}

// TriggerRun fires an agent job immediately. agentAlias can be the alias from project_agents.
func (s *Scheduler) TriggerRun(projectID int64, agentAlias string) {
	var agentID int64
	var agentType string
	err := s.db.QueryRow(
		`SELECT id, agent_type FROM project_agents
         WHERE project_id = ? AND alias = ? LIMIT 1`,
		projectID, agentAlias,
	).Scan(&agentID, &agentType)
	if err != nil {
		slog.Warn("TriggerRun: agent not found", "project_id", projectID, "alias", agentAlias)
		return
	}
	ctx := context.WithValue(context.Background(), ForcedRunKey, true)
	go s.agentFunc(ctx, projectID, agentType, agentID)
}
