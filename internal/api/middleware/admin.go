package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AdminOnly restricts access to the first registered user (user_id == 1).
// This is appropriate for single-operator deployments where user 1 is always
// the deployer/admin. Requires Auth middleware to run first.
func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := c.Get("user_id")
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{
				"code": "UNAUTHORIZED", "message": "未登录",
			}})
			return
		}
		if userID.(int64) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{
				"code": "FORBIDDEN", "message": "仅限管理员访问",
			}})
			return
		}
		c.Next()
	}
}
