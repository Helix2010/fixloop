// internal/api/handlers/screenshots.go
package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/fixloop/fixloop/internal/api/response"
	"github.com/fixloop/fixloop/internal/storage"
)

type ScreenshotHandler struct {
	R2 *storage.R2Client
}

func (h *ScreenshotHandler) Get(c *gin.Context) {
	if h.R2 == nil || h.R2.Disabled() {
		response.NotFound(c, "截图")
		return
	}
	projectID := c.GetInt64("project_id")
	runID := c.Param("run_id")
	// *filename wildcard includes a leading slash in Gin; strip it.
	filename := strings.TrimPrefix(c.Param("filename"), "/")

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
