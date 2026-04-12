package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	Sub   int64  `json:"sub"`
	Login string `json:"login"`
	jwt.RegisteredClaims
}

// Auth validates the JWT in the "fixloop_session" cookie.
// On success, sets "user_id" (int64) and "github_login" (string) in context.
func Auth(jwtSecret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		cookie, err := c.Cookie("fixloop_session")
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{
				"code": "UNAUTHORIZED", "message": "未登录",
			}})
			return
		}

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(cookie, claims, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return jwtSecret, nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{
				"code": "INVALID_TOKEN", "message": "token 无效或已过期",
			}})
			return
		}

		c.Set("user_id", claims.Sub)
		c.Set("github_login", claims.Login)
		c.Next()
	}
}

// IssueJWT signs a JWT and sets it as an httpOnly Secure cookie.
func IssueJWT(c *gin.Context, jwtSecret []byte, userID int64, login string, maxAge int) error {
	expiry := time.Now().Add(time.Duration(maxAge) * time.Second)
	claims := &Claims{
		Sub:   userID,
		Login: login,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiry),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(jwtSecret)
	if err != nil {
		return err
	}
	c.SetCookie("fixloop_session", signed, maxAge, "/", "", true, true)
	return nil
}
