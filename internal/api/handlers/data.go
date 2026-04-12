// internal/api/handlers/data.go
package handlers

import (
	"database/sql"
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

// ---- Issues ----

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
		`SELECT COUNT(*) FROM issues WHERE project_id = ?`, projectID,
	).Scan(&total)

	rows, err := h.DB.QueryContext(c.Request.Context(),
		`SELECT id, github_number, title, priority, status, fix_attempts, accept_failures,
		        fixing_since, closed_at, created_at
		 FROM issues WHERE project_id = ?
		 ORDER BY
		   CASE WHEN status = 'closed' THEN 1 ELSE 0 END ASC,
		   priority ASC,
		   created_at DESC
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
	if err := rows.Err(); err != nil {
		response.Internal(c)
		return
	}
	if issues == nil {
		issues = []issueResp{}
	}
	response.OKPaged(c, issues, response.Pagination{Page: page, PerPage: perPage, Total: total})
}

// ---- PRs ----

type prResp struct {
	ID           int64      `json:"id"`
	IssueID      *int64     `json:"issue_id,omitempty"`
	GithubNumber int        `json:"github_number"`
	Branch       string     `json:"branch"`
	Title        string     `json:"title,omitempty"`
	Status       string     `json:"status"`
	MergedBy     string     `json:"merged_by,omitempty"`
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
		`SELECT p.id, p.issue_id, p.github_number, p.branch,
		        COALESCE(p.title, IF(i.id IS NOT NULL, CONCAT('fix: ', i.title, ' (#', i.github_number, ')'), NULL)) AS title,
		        p.status, p.merged_by, p.created_at, p.merged_at
		 FROM prs p
		 LEFT JOIN issues i ON p.issue_id = i.id
		 WHERE p.project_id = ?
		 ORDER BY p.created_at DESC LIMIT ? OFFSET ?`,
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
		var title, mergedBy sql.NullString
		if err := rows.Scan(&p.ID, &issueID, &p.GithubNumber, &p.Branch,
			&title, &p.Status, &mergedBy, &p.CreatedAt, &p.MergedAt); err != nil {
			response.Internal(c)
			return
		}
		if issueID.Valid {
			p.IssueID = &issueID.Int64
		}
		if title.Valid {
			p.Title = title.String
		}
		if mergedBy.Valid {
			p.MergedBy = mergedBy.String
		}
		prs = append(prs, p)
	}
	if err := rows.Err(); err != nil {
		response.Internal(c)
		return
	}
	if prs == nil {
		prs = []prResp{}
	}
	response.OKPaged(c, prs, response.Pagination{Page: page, PerPage: perPage, Total: total})
}

// ---- Backlog ----

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
	if err := rows.Err(); err != nil {
		response.Internal(c)
		return
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

// ---- Runs ----

type runResp struct {
	ID            int64      `json:"id"`
	AgentType     string     `json:"agent_type"`
	Status        string     `json:"status"`
	Summary       string     `json:"summary,omitempty"`
	ConfigVersion int        `json:"config_version"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

func (h *DataHandler) ListRuns(c *gin.Context) {
	projectID := c.GetInt64("project_id")
	page, perPage := parsePagination(c)
	agentTypeFilter := c.Query("agent_type") // optional: explore|fix|master|plan|generic
	statusFilter := c.Query("status")        // optional: running|success|failed|skipped|abandoned

	// Build dynamic WHERE clause
	where := "project_id = ?"
	args := []any{projectID}
	if agentTypeFilter != "" {
		where += " AND agent_type = ?"
		args = append(args, agentTypeFilter)
	}
	if statusFilter != "" {
		where += " AND status = ?"
		args = append(args, statusFilter)
	}

	var total int64
	_ = h.DB.QueryRowContext(c.Request.Context(),
		"SELECT COUNT(*) FROM agent_runs WHERE "+where, args...,
	).Scan(&total)

	pageArgs := append(args, perPage, (page-1)*perPage)
	rows, err := h.DB.QueryContext(c.Request.Context(),
		"SELECT id, agent_type, status, summary, config_version, started_at, finished_at"+
			" FROM agent_runs WHERE "+where+
			" ORDER BY started_at DESC LIMIT ? OFFSET ?",
		pageArgs...,
	)
	if err != nil {
		response.Internal(c)
		return
	}
	defer rows.Close()

	var runs []runResp
	for rows.Next() {
		var r runResp
		var summary sql.NullString
		if err := rows.Scan(&r.ID, &r.AgentType, &r.Status, &summary, &r.ConfigVersion,
			&r.StartedAt, &r.FinishedAt); err != nil {
			response.Internal(c)
			return
		}
		if summary.Valid {
			r.Summary = summary.String
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		response.Internal(c)
		return
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
	_ = h.DB.QueryRowContext(c.Request.Context(),
		`SELECT output FROM agent_run_outputs WHERE run_id = ?`, runID,
	).Scan(&r.Output)
	response.OK(c, r)
}

func (h *DataHandler) TriggerRun(c *gin.Context) {
	projectID := c.GetInt64("project_id")
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
	response.OK(c, gin.H{"project_id": projectID, "alias": req.Alias, "status": "triggered"})
}

// ---- helpers ----

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
