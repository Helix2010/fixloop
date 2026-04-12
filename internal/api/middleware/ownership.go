package middleware

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// ProjectOwner checks that the :project_id route param belongs to the current user.
// Requires Auth middleware to have run first (sets "user_id").
func ProjectOwner(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := c.Get("user_id")
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{
				"code": "UNAUTHORIZED", "message": "未登录",
			}})
			return
		}

		projectID, err := strconv.ParseInt(c.Param("project_id"), 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": gin.H{
				"code": "NOT_FOUND", "message": "项目不存在",
			}})
			return
		}

		var count int
		err = db.QueryRowContext(c.Request.Context(),
			`SELECT COUNT(*) FROM projects WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
			projectID, userID.(int64),
		).Scan(&count)
		if err != nil || count == 0 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{
				"code": "FORBIDDEN", "message": "无权访问",
			}})
			return
		}

		c.Set("project_id", projectID)
		c.Next()
	}
}
