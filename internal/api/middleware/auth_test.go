package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/fixloop/fixloop/internal/api/middleware"
)

func makeToken(t *testing.T, secret []byte, userID int64, login string, exp time.Time) string {
	t.Helper()
	type testClaims struct {
		Sub   int64  `json:"sub"`
		Login string `json:"login"`
		jwt.RegisteredClaims
	}
	claims := testClaims{
		Sub:   userID,
		Login: login,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return tok
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := []byte("test-secret-32-bytes-long-padding!")

	r := gin.New()
	r.Use(middleware.Auth(secret))
	r.GET("/me", func(c *gin.Context) {
		uid := c.MustGet("user_id").(int64)
		c.JSON(200, gin.H{"user_id": uid})
	})

	tok := makeToken(t, secret, 42, "alice", time.Now().Add(time.Hour))
	req := httptest.NewRequest("GET", "/me", nil)
	req.AddCookie(&http.Cookie{Name: "fixloop_session", Value: tok})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
}

func TestAuthMiddleware_MissingCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := []byte("test-secret-32-bytes-long-padding!")

	r := gin.New()
	r.Use(middleware.Auth(secret))
	r.GET("/me", func(c *gin.Context) { c.JSON(200, nil) })

	req := httptest.NewRequest("GET", "/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := []byte("test-secret-32-bytes-long-padding!")

	r := gin.New()
	r.Use(middleware.Auth(secret))
	r.GET("/me", func(c *gin.Context) { c.JSON(200, nil) })

	tok := makeToken(t, secret, 1, "bob", time.Now().Add(-time.Hour))
	req := httptest.NewRequest("GET", "/me", nil)
	req.AddCookie(&http.Cookie{Name: "fixloop_session", Value: tok})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
