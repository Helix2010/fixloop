package handlers

import (
	"database/sql"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fixloop/fixloop/internal/api/response"
)

var aliasRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// AgentHandler handles /api/v1/projects/:project_id/agents routes.
type AgentHandler struct {
	DB        *sql.DB
	Scheduler ProjectScheduler
}

type agentResp struct {
	ID              int64     `json:"id"`
	AgentType       string    `json:"agent_type"`
	Name            string    `json:"name"`
	Alias           string    `json:"alias"`
	PromptOverride  *string   `json:"prompt_override,omitempty"`
	Rules           *string   `json:"rules,omitempty"`
	ScheduleMinutes int       `json:"schedule_minutes"`
	DailyLimit      int       `json:"daily_limit"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
}

// List returns all agents for a project.
func (h *AgentHandler) List(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	rows, err := h.DB.QueryContext(c.Request.Context(),
		`SELECT id, agent_type, name, alias, prompt_override, rules, schedule_minutes, daily_limit, enabled, created_at
         FROM project_agents WHERE project_id = ? ORDER BY id ASC`,
		projectID,
	)
	if err != nil {
		response.Internal(c)
		return
	}
	defer rows.Close()

	var agents []agentResp
	for rows.Next() {
		var a agentResp
		var promptOverride, rules sql.NullString
		if err := rows.Scan(&a.ID, &a.AgentType, &a.Name, &a.Alias,
			&promptOverride, &rules, &a.ScheduleMinutes, &a.DailyLimit, &a.Enabled, &a.CreatedAt); err != nil {
			response.Internal(c)
			return
		}
		applyNullableFields(&a, promptOverride, rules)
		agents = append(agents, a)
	}
	if agents == nil {
		agents = []agentResp{}
	}
	response.OK(c, agents)
}

// Create adds a generic agent to the project.
func (h *AgentHandler) Create(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	var req struct {
		Name            string `json:"name" binding:"required"`
		Alias           string `json:"alias" binding:"required"`
		PromptOverride  string `json:"prompt_override" binding:"required"`
		Rules           string `json:"rules"`
		ScheduleMinutes int    `json:"schedule_minutes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if !aliasRe.MatchString(req.Alias) {
		response.BadRequest(c, "alias 格式无效，须为小写字母/数字/连字符，长度 1-32")
		return
	}
	if req.ScheduleMinutes < 10 {
		req.ScheduleMinutes = 60 // default for generic
	}

	res, err := h.DB.ExecContext(c.Request.Context(),
		`INSERT INTO project_agents (project_id, agent_type, name, alias, prompt_override, rules, schedule_minutes)
         VALUES (?, 'generic', ?, ?, ?, ?, ?)`,
		projectID, req.Name, req.Alias, req.PromptOverride, nullableStr(req.Rules), req.ScheduleMinutes,
	)
	if err != nil {
		if isDuplicateEntry(err) {
			response.Err(c, http.StatusConflict, "AGENT_CONFLICT", "名称或别名已存在")
			return
		}
		response.Internal(c)
		return
	}
	agentID, _ := res.LastInsertId()

	h.schedReRegister(projectID)

	var agent agentResp
	var promptOverride, rules sql.NullString
	_ = h.DB.QueryRowContext(c.Request.Context(),
		`SELECT id, agent_type, name, alias, prompt_override, rules, schedule_minutes, daily_limit, enabled, created_at
         FROM project_agents WHERE id = ?`, agentID,
	).Scan(&agent.ID, &agent.AgentType, &agent.Name, &agent.Alias,
		&promptOverride, &rules, &agent.ScheduleMinutes, &agent.DailyLimit, &agent.Enabled, &agent.CreatedAt)
	applyNullableFields(&agent, promptOverride, rules)
	response.Created(c, agent)
}

// Update patches any agent's fields.
func (h *AgentHandler) Update(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	agentID, err := strconv.ParseInt(c.Param("agent_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "无效的 agent_id")
		return
	}
	var req struct {
		Name            *string `json:"name"`
		Alias           *string `json:"alias"`
		PromptOverride  *string `json:"prompt_override"`
		Rules           *string `json:"rules"`
		ScheduleMinutes *int    `json:"schedule_minutes"`
		DailyLimit      *int    `json:"daily_limit"`
		Enabled         *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.Alias != nil && !aliasRe.MatchString(*req.Alias) {
		response.BadRequest(c, "alias 格式无效")
		return
	}
	if req.ScheduleMinutes != nil && *req.ScheduleMinutes < 10 {
		response.BadRequest(c, "schedule_minutes 最小为 10")
		return
	}

	setClauses := []string{}
	args := []interface{}{}
	if req.Name != nil {
		setClauses = append(setClauses, "name = ?")
		args = append(args, *req.Name)
	}
	if req.Alias != nil {
		setClauses = append(setClauses, "alias = ?")
		args = append(args, *req.Alias)
	}
	if req.PromptOverride != nil {
		setClauses = append(setClauses, "prompt_override = ?")
		args = append(args, nullableStr(*req.PromptOverride))
	}
	if req.Rules != nil {
		setClauses = append(setClauses, "rules = ?")
		args = append(args, nullableStr(*req.Rules))
	}
	if req.ScheduleMinutes != nil {
		setClauses = append(setClauses, "schedule_minutes = ?")
		args = append(args, *req.ScheduleMinutes)
	}
	if req.DailyLimit != nil {
		if *req.DailyLimit < 0 {
			response.BadRequest(c, "daily_limit 不能为负数")
			return
		}
		setClauses = append(setClauses, "daily_limit = ?")
		args = append(args, *req.DailyLimit)
	}
	if req.Enabled != nil {
		setClauses = append(setClauses, "enabled = ?")
		v := 0
		if *req.Enabled {
			v = 1
		}
		args = append(args, v)
	}
	if len(setClauses) == 0 {
		response.BadRequest(c, "没有要更新的字段")
		return
	}

	query := "UPDATE project_agents SET " + strings.Join(setClauses, ", ") + " WHERE id = ? AND project_id = ?"
	args = append(args, agentID, projectID)
	res, err := h.DB.ExecContext(c.Request.Context(), query, args...)
	if err != nil {
		if isDuplicateEntry(err) {
			response.Err(c, http.StatusConflict, "AGENT_CONFLICT", "名称或别名已存在")
			return
		}
		response.Internal(c)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		response.NotFound(c, "Agent")
		return
	}

	h.schedReRegister(projectID)

	var agent agentResp
	var promptOverride, rules sql.NullString
	_ = h.DB.QueryRowContext(c.Request.Context(),
		`SELECT id, agent_type, name, alias, prompt_override, rules, schedule_minutes, daily_limit, enabled, created_at
         FROM project_agents WHERE id = ?`, agentID,
	).Scan(&agent.ID, &agent.AgentType, &agent.Name, &agent.Alias,
		&promptOverride, &rules, &agent.ScheduleMinutes, &agent.DailyLimit, &agent.Enabled, &agent.CreatedAt)
	applyNullableFields(&agent, promptOverride, rules)
	response.OK(c, agent)
}

// Delete removes a generic agent (built-in agents cannot be deleted).
func (h *AgentHandler) Delete(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	agentID, err := strconv.ParseInt(c.Param("agent_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "无效的 agent_id")
		return
	}

	var agentType string
	err = h.DB.QueryRowContext(c.Request.Context(),
		`SELECT agent_type FROM project_agents WHERE id = ? AND project_id = ?`,
		agentID, projectID,
	).Scan(&agentType)
	if err == sql.ErrNoRows {
		response.NotFound(c, "Agent")
		return
	}
	if err != nil {
		response.Internal(c)
		return
	}
	if agentType != "generic" {
		response.Err(c, http.StatusForbidden, "BUILTIN_AGENT", "内置 Agent 不可删除")
		return
	}

	if _, err := h.DB.ExecContext(c.Request.Context(),
		`DELETE FROM project_agents WHERE id = ? AND project_id = ?`,
		agentID, projectID,
	); err != nil {
		response.Internal(c)
		return
	}

	h.schedReRegister(projectID)
	c.JSON(http.StatusNoContent, nil)
}

func (h *AgentHandler) schedReRegister(projectID int64) {
	if h.Scheduler != nil {
		_ = h.Scheduler.RegisterProject(projectID)
	}
}

// nullableStr returns nil interface{} for empty strings (stored as NULL in DB).
func nullableStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// applyNullableFields populates prompt_override and rules from nullable DB columns.
func applyNullableFields(a *agentResp, promptOverride, rules sql.NullString) {
	if promptOverride.Valid {
		a.PromptOverride = &promptOverride.String
	}
	if rules.Valid {
		a.Rules = &rules.String
	}
}
