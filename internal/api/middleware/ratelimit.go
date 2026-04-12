package middleware

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type userLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// PerUserRateLimit limits each user to r requests/sec with burst b.
// Uses user_id from context if authenticated, otherwise falls back to IP.
func PerUserRateLimit(r rate.Limit, b int) gin.HandlerFunc {
	mu := sync.Mutex{}
	limiters := make(map[string]*userLimiter)

	go func() {
		for range time.Tick(5 * time.Minute) {
			mu.Lock()
			for key, ul := range limiters {
				if time.Since(ul.lastSeen) > 10*time.Minute {
					delete(limiters, key)
				}
			}
			mu.Unlock()
		}
	}()

	getLimiter := func(key string) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()
		if ul, ok := limiters[key]; ok {
			ul.lastSeen = time.Now()
			return ul.limiter
		}
		ul := &userLimiter{limiter: rate.NewLimiter(r, b), lastSeen: time.Now()}
		limiters[key] = ul
		return ul.limiter
	}

	return func(c *gin.Context) {
		key := c.ClientIP()
		if uid, ok := c.Get("user_id"); ok {
			key = fmt.Sprintf("uid:%d", uid.(int64))
		}
		if !getLimiter(key).Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": gin.H{
				"code": "RATE_LIMITED", "message": "请求过于频繁，请稍后重试",
			}})
			return
		}
		c.Next()
	}
}
