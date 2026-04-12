package handlers

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

type HealthHandler struct {
	DB *sql.DB
}

// Health returns service health. GET /health
func (h *HealthHandler) Health(c *gin.Context) {
	dbStatus := "ok"
	if err := h.DB.PingContext(c.Request.Context()); err != nil {
		dbStatus = "error: " + err.Error()
	}

	status := "ok"
	httpStatus := http.StatusOK
	if dbStatus != "ok" {
		status = "degraded"
		httpStatus = http.StatusServiceUnavailable
	}

	c.JSON(httpStatus, gin.H{"status": status, "db": dbStatus})
}
