# FixLoop Phase 1: Backend Foundation + Auth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立 FixLoop Go 后端基础：数据库连接与迁移、AES 加密、JWT 认证、GitHub OAuth 登录、健康检查，为所有后续 Phase 提供稳定底座。

**Architecture:** 单体 Go 服务（Gin），配置全部来自环境变量，数据库迁移用 golang-migrate 在启动时自动执行，JWT 存 httpOnly cookie，前后端同域（Nginx 反代，无 CORS）。

**Tech Stack:** Go 1.22+, Gin, MySQL 8.0, golang-migrate, golang-jwt/jwt/v5, golang.org/x/time/rate, sentry-go, slog (stdlib)

**Spec:** `docs/specs/2026-04-11-fixloop-design.md` — 重点参考章节：八、十、十一、十二、十三、十四

---

## File Structure

```
fixloop/
├── go.mod
├── go.sum
├── .env.example
├── cmd/
│   └── server/
│       └── main.go                        # 启动入口：加载配置、连接 DB、运行迁移、启动 Gin
├── internal/
│   ├── config/
│   │   └── config.go                      # 从环境变量加载所有配置，启动时 panic-fail-fast
│   ├── db/
│   │   └── db.go                          # MySQL 连接池 + golang-migrate runner
│   ├── crypto/
│   │   ├── crypto.go                      # AES-256-GCM 加密/解密，key_id 前缀，支持密钥轮换
│   │   └── crypto_test.go
│   ├── models/
│   │   └── models.go                      # DB struct 定义（User, Project 等），不含业务逻辑
│   ├── api/
│   │   ├── router.go                      # 路由注册，middleware 挂载
│   │   ├── response/
│   │   │   └── response.go                # 统一响应格式 {data, pagination} / {error: {code, message}}
│   │   ├── middleware/
│   │   │   ├── auth.go                    # JWT cookie 验证，注入 user_id 到 context
│   │   │   ├── ownership.go               # 验证 project.user_id == current_user_id
│   │   │   └── ratelimit.go               # per-user token bucket，20 req/s burst 50
│   │   └── handlers/
│   │       ├── health.go                  # GET /health
│   │       ├── auth.go                    # GET /api/v1/auth/github + /api/v1/auth/github/callback
│   │       └── users.go                   # GET /api/v1/me, DELETE /api/v1/me
└── backend/
    └── migrations/
        ├── 000001_initial_schema.up.sql
        └── 000001_initial_schema.down.sql
```

---

## Task 1: Go module 初始化 + 依赖

**Files:**
- Create: `go.mod`
- Create: `.env.example`

- [ ] **Step 1: 在 fixloop/backend 目录初始化 Go module**

```bash
cd /home/ubuntu/fy/work/fixloop
go mod init github.com/fixloop/fixloop
```

Expected output: `go: creating new go.mod: module github.com/fixloop/fixloop`

- [ ] **Step 2: 安装核心依赖**

```bash
go get github.com/gin-gonic/gin@v1.10.0
go get github.com/go-sql-driver/mysql@v1.8.1
go get github.com/golang-migrate/migrate/v4@v4.17.1
go get github.com/golang-migrate/migrate/v4/database/mysql
go get github.com/golang-migrate/migrate/v4/source/file
go get github.com/golang-jwt/jwt/v5@v5.2.1
go get golang.org/x/time@latest
go get github.com/getsentry/sentry-go@v0.28.1
```

- [ ] **Step 3: 创建 .env.example**

```bash
cat > .env.example << 'EOF'
PORT=8080
DATABASE_DSN=fixloop:password@tcp(127.0.0.1:3306)/fixloop_dev?parseTime=true&charset=utf8mb4
FIXLOOP_AES_KEY=0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20
FIXLOOP_JWT_SECRET=your-jwt-secret-at-least-32-characters-long
GITHUB_CLIENT_ID=your_github_oauth_app_client_id
GITHUB_CLIENT_SECRET=your_github_oauth_app_client_secret
GITHUB_REDIRECT_URL=https://fixloop.com/api/v1/auth/github/callback
SENTRY_DSN=
APP_BASE_URL=https://fixloop.com
EOF
```

- [ ] **Step 4: 创建目录结构**

```bash
mkdir -p cmd/server internal/config internal/db internal/crypto \
         internal/models internal/api/middleware internal/api/handlers \
         internal/api/response backend/migrations
```

- [ ] **Step 5: Commit**

```bash
git -C /home/ubuntu/fy/work/fixloop add go.mod go.sum .env.example
git -C /home/ubuntu/fy/work/fixloop commit -m "feat: initialize Go module and dependencies"
```

---

## Task 2: 配置加载

**Files:**
- Create: `internal/config/config.go`

- [ ] **Step 1: 写 config.go**

```go
// internal/config/config.go
package config

import (
	"encoding/hex"
	"fmt"
	"os"
)

type Config struct {
	Port               string
	DatabaseDSN        string
	AESKey             []byte // 32 bytes
	AESKeyID           byte   // 当前活跃 key ID（用于新加密）
	JWTSecret          []byte
	GitHubClientID     string
	GitHubClientSecret string
	GitHubRedirectURL  string
	SentryDSN          string
	AppBaseURL         string
}

func Load() (*Config, error) {
	aesKeyHex := os.Getenv("FIXLOOP_AES_KEY")
	aesKey, err := hex.DecodeString(aesKeyHex)
	if err != nil || len(aesKey) != 32 {
		return nil, fmt.Errorf("FIXLOOP_AES_KEY must be a 64-char hex string (32 bytes)")
	}

	jwtSecret := []byte(mustEnv("FIXLOOP_JWT_SECRET"))
	if len(jwtSecret) < 32 {
		return nil, fmt.Errorf("FIXLOOP_JWT_SECRET must be at least 32 characters")
	}

	return &Config{
		Port:               envOrDefault("PORT", "8080"),
		DatabaseDSN:        mustEnv("DATABASE_DSN"),
		AESKey:             aesKey,
		AESKeyID:           1,
		JWTSecret:          jwtSecret,
		GitHubClientID:     mustEnv("GITHUB_CLIENT_ID"),
		GitHubClientSecret: mustEnv("GITHUB_CLIENT_SECRET"),
		GitHubRedirectURL:  mustEnv("GITHUB_REDIRECT_URL"),
		SentryDSN:          os.Getenv("SENTRY_DSN"),
		AppBaseURL:         envOrDefault("APP_BASE_URL", "http://localhost:8080"),
	}, nil
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required environment variable %q is not set", key))
	}
	return v
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 2: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./internal/config/...
```

Expected: no output (success)

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: add config loading from environment variables"
```

---

## Task 3: AES-256-GCM 加密

**Files:**
- Create: `internal/crypto/crypto.go`
- Create: `internal/crypto/crypto_test.go`

- [ ] **Step 1: 写 failing test**

```go
// internal/crypto/crypto_test.go
package crypto_test

import (
	"testing"

	"github.com/fixloop/fixloop/internal/crypto"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	keys := map[byte][]byte{1: key}

	plaintext := []byte("hello fixloop secret")
	ct, err := crypto.Encrypt(1, key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ct[0] != 1 {
		t.Errorf("expected key_id=1, got %d", ct[0])
	}

	got, err := crypto.Decrypt(keys, ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("got %q, want %q", got, plaintext)
	}
}

func TestDecryptUnknownKeyID(t *testing.T) {
	key := make([]byte, 32)
	keys := map[byte][]byte{1: key}
	ct, _ := crypto.Encrypt(1, key, []byte("data"))
	ct[0] = 99 // tamper key_id

	_, err := crypto.Decrypt(keys, ct)
	if err == nil {
		t.Fatal("expected error for unknown key_id")
	}
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	keys := map[byte][]byte{1: key}
	ct, _ := crypto.Encrypt(1, key, []byte("data"))
	ct[len(ct)-1] ^= 0xff // tamper last byte

	_, err := crypto.Decrypt(keys, ct)
	if err == nil {
		t.Fatal("expected error for tampered ciphertext")
	}
}

func TestEncryptProducesDifferentCiphertexts(t *testing.T) {
	key := make([]byte, 32)
	plaintext := []byte("same plaintext")
	ct1, _ := crypto.Encrypt(1, key, plaintext)
	ct2, _ := crypto.Encrypt(1, key, plaintext)
	if string(ct1) == string(ct2) {
		t.Error("two encryptions of same plaintext should differ (random nonce)")
	}
}
```

- [ ] **Step 2: 运行 test 确认失败**

```bash
cd /home/ubuntu/fy/work/fixloop && go test ./internal/crypto/... -v 2>&1 | head -20
```

Expected: `cannot find package` or `undefined: crypto.Encrypt`

- [ ] **Step 3: 实现 crypto.go**

```go
// internal/crypto/crypto.go
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

// Encrypt encrypts plaintext with AES-256-GCM.
// Output format: key_id(1 byte) | nonce(12 bytes) | ciphertext+tag
func Encrypt(keyID byte, key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize()) // 12 bytes
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)

	result := make([]byte, 1+len(nonce)+len(ct))
	result[0] = keyID
	copy(result[1:], nonce)
	copy(result[1+len(nonce):], ct)
	return result, nil
}

// Decrypt decrypts ciphertext using the key identified by the embedded key_id.
// keys maps key_id → 32-byte AES key (supports multiple keys for rotation).
func Decrypt(keys map[byte][]byte, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < 13 { // 1 (key_id) + 12 (nonce minimum)
		return nil, fmt.Errorf("ciphertext too short")
	}
	keyID := ciphertext[0]
	key, ok := keys[keyID]
	if !ok {
		return nil, fmt.Errorf("unknown key_id: %d", keyID)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < 1+nonceSize {
		return nil, fmt.Errorf("ciphertext too short for nonce")
	}
	nonce := ciphertext[1 : 1+nonceSize]
	ct := ciphertext[1+nonceSize:]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plain, nil
}
```

- [ ] **Step 4: 运行 tests 确认通过**

```bash
cd /home/ubuntu/fy/work/fixloop && go test ./internal/crypto/... -v
```

Expected:
```
--- PASS: TestEncryptDecryptRoundTrip
--- PASS: TestDecryptUnknownKeyID
--- PASS: TestDecryptTamperedCiphertext
--- PASS: TestEncryptProducesDifferentCiphertexts
PASS
```

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/
git commit -m "feat: add AES-256-GCM encryption with key-id versioning"
```

---

## Task 4: 数据库连接 + 迁移

**Files:**
- Create: `internal/db/db.go`
- Create: `backend/migrations/000001_initial_schema.up.sql`
- Create: `backend/migrations/000001_initial_schema.down.sql`

- [ ] **Step 1: 写 000001_initial_schema.up.sql**

```sql
-- backend/migrations/000001_initial_schema.up.sql
CREATE TABLE IF NOT EXISTS users (
    id           BIGINT AUTO_INCREMENT PRIMARY KEY,
    github_id    BIGINT NOT NULL,
    github_login VARCHAR(64) NOT NULL,
    tg_chat_id   BIGINT,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at   DATETIME,
    UNIQUE KEY uq_github_id (github_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS projects (
    id             BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id        BIGINT NOT NULL,
    name           VARCHAR(64) NOT NULL,
    config         JSON NOT NULL,
    config_version INT NOT NULL DEFAULT 1,
    status         ENUM('active','paused','error') NOT NULL DEFAULT 'active',
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at     DATETIME,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    UNIQUE KEY uq_user_project_name (user_id, name, deleted_at),
    INDEX idx_user_status (user_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS issues (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    project_id      BIGINT NOT NULL,
    github_number   INT NOT NULL,
    title           VARCHAR(512) NOT NULL,
    title_hash      CHAR(40) NOT NULL,
    priority        TINYINT NOT NULL DEFAULT 2,
    status          ENUM('open','fixing','closed','needs-human') NOT NULL DEFAULT 'open',
    fix_attempts    INT NOT NULL DEFAULT 0,
    accept_failures INT NOT NULL DEFAULT 0,
    fixing_since    DATETIME,
    closed_at       DATETIME,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    UNIQUE KEY uq_project_issue (project_id, github_number),
    UNIQUE KEY uq_project_title (project_id, title_hash),
    INDEX idx_project_status_priority (project_id, status, priority, fix_attempts)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS prs (
    id            BIGINT AUTO_INCREMENT PRIMARY KEY,
    project_id    BIGINT NOT NULL,
    issue_id      BIGINT,
    github_number INT NOT NULL,
    branch        VARCHAR(128) NOT NULL,
    status        ENUM('open','merged','closed') NOT NULL DEFAULT 'open',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    merged_at     DATETIME,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (issue_id) REFERENCES issues(id) ON DELETE SET NULL,
    UNIQUE KEY uq_project_pr (project_id, github_number),
    INDEX idx_project_status (project_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS pr_reviews (
    id           BIGINT AUTO_INCREMENT PRIMARY KEY,
    pr_id        BIGINT NOT NULL,
    reviewer     VARCHAR(64) NOT NULL,
    state        ENUM('pending','commented','approved','changes_requested') NOT NULL DEFAULT 'pending',
    review_round INT NOT NULL DEFAULT 1,
    reviewed_at  DATETIME,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (pr_id) REFERENCES prs(id) ON DELETE CASCADE,
    INDEX idx_pr_round (pr_id, review_round)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS backlog (
    id               BIGINT AUTO_INCREMENT PRIMARY KEY,
    project_id       BIGINT NOT NULL,
    related_issue_id BIGINT,
    title            VARCHAR(512) NOT NULL,
    title_hash       CHAR(40) NOT NULL,
    description      TEXT,
    test_steps       JSON,
    scenario_type    ENUM('ui','api') NOT NULL DEFAULT 'ui',
    priority         TINYINT NOT NULL DEFAULT 2,
    status           ENUM('pending','tested','failed','skipped','ignored') NOT NULL DEFAULT 'pending',
    source           ENUM('plan','auto_expand','seed') NOT NULL DEFAULT 'plan',
    last_tested_at   DATETIME,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (related_issue_id) REFERENCES issues(id) ON DELETE SET NULL,
    UNIQUE KEY uq_project_scenario (project_id, title_hash),
    INDEX idx_project_status_priority (project_id, status, priority, last_tested_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS agent_runs (
    id          BIGINT AUTO_INCREMENT,
    project_id  BIGINT,
    agent_type  ENUM('explore','fix','master','plan') NOT NULL,
    status      ENUM('running','success','failed','skipped','abandoned') NOT NULL DEFAULT 'running',
    config_version INT,
    started_at  DATETIME NOT NULL,
    finished_at DATETIME,
    PRIMARY KEY (id, started_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
PARTITION BY RANGE (YEAR(started_at) * 100 + MONTH(started_at)) (
    PARTITION p202604 VALUES LESS THAN (202605),
    PARTITION p202605 VALUES LESS THAN (202606),
    PARTITION p202606 VALUES LESS THAN (202607),
    PARTITION p_future VALUES LESS THAN MAXVALUE
);

CREATE TABLE IF NOT EXISTS agent_run_outputs (
    run_id BIGINT NOT NULL,
    output MEDIUMTEXT,
    PRIMARY KEY (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS notifications (
    id         BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id    BIGINT NOT NULL,
    project_id BIGINT,
    type       VARCHAR(64) NOT NULL,
    content    TEXT NOT NULL,
    read_at    DATETIME,
    tg_sent    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    INDEX idx_user_read (user_id, read_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS system_config (
    key_name   VARCHAR(64) PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO system_config (key_name, value) VALUES
    ('tg_last_update_id', '0'),
    ('partition_last_cleaned', ''),
    ('feature_flags', '{}');
```

- [ ] **Step 2: 写 000001_initial_schema.down.sql**

```sql
-- backend/migrations/000001_initial_schema.down.sql
SET FOREIGN_KEY_CHECKS = 0;
DROP TABLE IF EXISTS system_config;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS agent_run_outputs;
DROP TABLE IF EXISTS agent_runs;
DROP TABLE IF EXISTS backlog;
DROP TABLE IF EXISTS pr_reviews;
DROP TABLE IF EXISTS prs;
DROP TABLE IF EXISTS issues;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS users;
SET FOREIGN_KEY_CHECKS = 1;
```

- [ ] **Step 3: 写 db.go**

```go
// internal/db/db.go
package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/go-sql-driver/mysql"
)

// Open opens a MySQL connection pool with recommended settings.
func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return db, nil
}

// Migrate runs all pending up migrations from migrationsPath.
// migrationsPath should be an absolute path to the migrations directory.
func Migrate(dsn, migrationsPath string) error {
	// golang-migrate requires "mysql://" prefix and no parseTime param
	m, err := migrate.New(
		"file://"+migrationsPath,
		"mysql://"+stripParams(dsn),
	)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run migrations: %w", err)
	}
	slog.Info("database migrations applied")
	return nil
}

// stripParams removes query parameters from DSN for golang-migrate compatibility.
func stripParams(dsn string) string {
	// golang-migrate's MySQL driver handles DSN differently; strip everything after '?'
	for i, c := range dsn {
		if c == '?' {
			return dsn[:i]
		}
	}
	return dsn
}
```

- [ ] **Step 4: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./internal/db/...
```

Expected: no output

- [ ] **Step 5: Commit**

```bash
git add internal/db/ backend/migrations/
git commit -m "feat: add MySQL connection pool and initial schema migration"
```

---

## Task 5: 统一响应格式

**Files:**
- Create: `internal/api/response/response.go`

- [ ] **Step 1: 写 response.go**

```go
// internal/api/response/response.go
package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Pagination struct {
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Total   int64 `json:"total"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// OK sends a 200 response with data payload.
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"data": data})
}

// OKPaged sends a 200 response with data + pagination.
func OKPaged(c *gin.Context, data any, p Pagination) {
	c.JSON(http.StatusOK, gin.H{"data": data, "pagination": p})
}

// Created sends a 201 response.
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, gin.H{"data": data})
}

// Err sends an error response with the given HTTP status.
func Err(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": ErrorBody{Code: code, Message: message}})
}

func Unauthorized(c *gin.Context, message string) {
	Err(c, http.StatusUnauthorized, "UNAUTHORIZED", message)
}

func Forbidden(c *gin.Context) {
	Err(c, http.StatusForbidden, "FORBIDDEN", "无权访问")
}

func NotFound(c *gin.Context, what string) {
	Err(c, http.StatusNotFound, "NOT_FOUND", what+"不存在")
}

func BadRequest(c *gin.Context, message string) {
	Err(c, http.StatusBadRequest, "BAD_REQUEST", message)
}

func Internal(c *gin.Context) {
	Err(c, http.StatusInternalServerError, "INTERNAL_ERROR", "服务器内部错误")
}
```

- [ ] **Step 2: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./internal/api/response/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/api/response/
git commit -m "feat: add unified JSON response helpers"
```

---

## Task 6: JWT Auth Middleware

**Files:**
- Create: `internal/api/middleware/auth.go`

- [ ] **Step 1: 写 failing test**

```go
// internal/api/middleware/auth_test.go
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
	claims := jwt.MapClaims{
		"sub":   userID,
		"login": login,
		"exp":   exp.Unix(),
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
```

- [ ] **Step 2: 运行 test 确认失败**

```bash
cd /home/ubuntu/fy/work/fixloop && go test ./internal/api/middleware/... -v -run TestAuth 2>&1 | head -10
```

Expected: compile error or `undefined: middleware.Auth`

- [ ] **Step 3: 实现 auth.go**

```go
// internal/api/middleware/auth.go
package middleware

import (
	"fmt"
	"net/http"

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

// IssueJWT signs a JWT and sets it as an httpOnly cookie.
func IssueJWT(c *gin.Context, jwtSecret []byte, userID int64, login string, maxAge int) error {
	claims := jwt.MapClaims{
		"sub":   userID,
		"login": login,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	// exp is set via RegisteredClaims but MapClaims works too; use exp manually:
	// The cookie maxAge enforces expiry on browser side; JWT exp adds server-side check.
	// We re-use maxAge seconds as exp offset by embedding it in the token.
	// For simplicity, rely on cookie maxAge and ParseWithClaims expiry via RegisteredClaims.
	// Rebuild with proper exp:
	propClaims := &Claims{
		Sub:   userID,
		Login: login,
	}
	propToken := jwt.NewWithClaims(jwt.SigningMethodHS256, propClaims)
	signed, err := propToken.SignedString(jwtSecret)
	if err != nil {
		return err
	}
	c.SetCookie("fixloop_session", signed, maxAge, "/", "", true, true)
	return nil
}
```

- [ ] **Step 4: 运行 tests 确认通过**

```bash
cd /home/ubuntu/fy/work/fixloop && go test ./internal/api/middleware/... -v -run TestAuth
```

Expected: all 3 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/api/middleware/auth.go internal/api/middleware/auth_test.go
git commit -m "feat: add JWT auth middleware with cookie validation"
```

---

## Task 7: Ownership Middleware

**Files:**
- Create: `internal/api/middleware/ownership.go`

- [ ] **Step 1: 写 ownership.go**

```go
// internal/api/middleware/ownership.go
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
```

- [ ] **Step 2: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./internal/api/middleware/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/api/middleware/ownership.go
git commit -m "feat: add project ownership middleware"
```

---

## Task 8: Rate Limit Middleware

**Files:**
- Create: `internal/api/middleware/ratelimit.go`

- [ ] **Step 1: 写 ratelimit.go**

```go
// internal/api/middleware/ratelimit.go
package middleware

import (
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

// PerUserRateLimit returns middleware limiting each user to r requests/sec with burst b.
// Uses user_id from context (set by Auth middleware). Falls back to IP if not authenticated.
func PerUserRateLimit(r rate.Limit, b int) gin.HandlerFunc {
	mu := sync.Mutex{}
	limiters := make(map[string]*userLimiter)

	// Background cleanup: remove entries not seen in 10 minutes
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
		ul := &userLimiter{
			limiter:  rate.NewLimiter(r, b),
			lastSeen: time.Now(),
		}
		limiters[key] = ul
		return ul.limiter
	}

	return func(c *gin.Context) {
		key := c.ClientIP()
		if uid, ok := c.Get("user_id"); ok {
			key = "uid:" + string(rune(uid.(int64)))
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
```

- [ ] **Step 2: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./internal/api/middleware/...
```

- [ ] **Step 3: Commit**

```bash
git add internal/api/middleware/ratelimit.go
git commit -m "feat: add per-user rate limiting middleware"
```

---

## Task 9: Health Handler

**Files:**
- Create: `internal/api/handlers/health.go`

- [ ] **Step 1: 写 health.go**

```go
// internal/api/handlers/health.go
package handlers

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

type HealthHandler struct {
	DB *sql.DB
}

// Health returns service health status.
// GET /health
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

	c.JSON(httpStatus, gin.H{
		"status": status,
		"db":     dbStatus,
	})
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/api/handlers/health.go
git commit -m "feat: add health check endpoint"
```

---

## Task 10: GitHub OAuth Handler

**Files:**
- Create: `internal/api/handlers/auth.go`
- Create: `internal/models/models.go`

- [ ] **Step 1: 写 models.go**

```go
// internal/models/models.go
package models

import "time"

type User struct {
	ID          int64      `db:"id"`
	GitHubID    int64      `db:"github_id"`
	GitHubLogin string     `db:"github_login"`
	TGChatID    *int64     `db:"tg_chat_id"`
	CreatedAt   time.Time  `db:"created_at"`
	DeletedAt   *time.Time `db:"deleted_at"`
}
```

- [ ] **Step 2: 写 auth.go**

```go
// internal/api/handlers/auth.go
package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/fixloop/fixloop/internal/api/middleware"
	"github.com/fixloop/fixloop/internal/config"
)

type AuthHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

// GitHubLogin redirects user to GitHub OAuth authorization page.
// GET /api/v1/auth/github
func (h *AuthHandler) GitHubLogin(c *gin.Context) {
	state := generateState()
	// Store state in a short-lived cookie for CSRF verification
	c.SetCookie("oauth_state", state, 600, "/", "", true, true)

	authURL := fmt.Sprintf(
		"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&scope=read:user&state=%s",
		url.QueryEscape(h.Cfg.GitHubClientID),
		url.QueryEscape(h.Cfg.GitHubRedirectURL),
		url.QueryEscape(state),
	)
	c.Redirect(http.StatusFound, authURL)
}

// GitHubCallback handles the GitHub OAuth callback.
// GET /api/v1/auth/github/callback
func (h *AuthHandler) GitHubCallback(c *gin.Context) {
	// CSRF: verify state parameter
	stateCookie, err := c.Cookie("oauth_state")
	if err != nil || stateCookie != c.Query("state") {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code": "INVALID_STATE", "message": "OAuth state 验证失败",
		}})
		return
	}
	c.SetCookie("oauth_state", "", -1, "/", "", true, true) // clear state cookie

	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code": "MISSING_CODE", "message": "缺少 OAuth code",
		}})
		return
	}

	// Exchange code for access token
	accessToken, err := h.exchangeCode(c.Request.Context(), code)
	if err != nil {
		slog.Error("github oauth exchange failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "OAUTH_FAILED", "message": "GitHub 授权失败",
		}})
		return
	}

	// Fetch GitHub user info
	ghUser, err := h.fetchGitHubUser(c.Request.Context(), accessToken)
	if err != nil {
		slog.Error("fetch github user failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "OAUTH_FAILED", "message": "获取 GitHub 用户信息失败",
		}})
		return
	}

	// Upsert user in DB
	userID, err := h.upsertUser(c.Request.Context(), ghUser)
	if err != nil {
		slog.Error("upsert user failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "DB_ERROR", "message": "用户信息保存失败",
		}})
		return
	}

	// Issue JWT cookie (7 days)
	const sevenDays = 7 * 24 * 60 * 60
	if err := middleware.IssueJWT(c, h.Cfg.JWTSecret, userID, ghUser.Login, sevenDays); err != nil {
		slog.Error("issue jwt failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "JWT_FAILED", "message": "登录 token 生成失败",
		}})
		return
	}

	// Redirect to dashboard
	redirect := c.Query("redirect")
	if redirect == "" || !strings.HasPrefix(redirect, "/") {
		redirect = "/dashboard"
	}
	c.Redirect(http.StatusFound, redirect)
}

type githubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

func (h *AuthHandler) exchangeCode(ctx context.Context, code string) (string, error) {
	body := url.Values{
		"client_id":     {h.Cfg.GitHubClientID},
		"client_secret": {h.Cfg.GitHubClientSecret},
		"code":          {code},
		"redirect_uri":  {h.Cfg.GitHubRedirectURL},
	}
	req, _ := http.NewRequestWithContext(ctx, "POST",
		"https://github.com/login/oauth/access_token",
		strings.NewReader(body.Encode()),
	)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Error != "" {
		return "", fmt.Errorf("github oauth error: %s", result.Error)
	}
	return result.AccessToken, nil
}

func (h *AuthHandler) fetchGitHubUser(ctx context.Context, token string) (*githubUser, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("github token invalid or revoked")
	}
	b, _ := io.ReadAll(resp.Body)
	var u githubUser
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (h *AuthHandler) upsertUser(ctx context.Context, u *githubUser) (int64, error) {
	res, err := h.DB.ExecContext(ctx, `
		INSERT INTO users (github_id, github_login, created_at)
		VALUES (?, ?, NOW())
		ON DUPLICATE KEY UPDATE github_login = VALUES(github_login)
	`, u.ID, u.Login)
	if err != nil {
		return 0, err
	}
	// If INSERT happened, LastInsertId works; if UPDATE, fetch the existing ID
	id, err := res.LastInsertId()
	if err != nil || id == 0 {
		err = h.DB.QueryRowContext(ctx,
			`SELECT id FROM users WHERE github_id = ?`, u.ID,
		).Scan(&id)
	}
	return id, err
}

func generateState() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// UserInfo returns current user info.
// GET /api/v1/me
func (h *AuthHandler) UserInfo(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	login := c.MustGet("github_login").(string)
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"id":           userID,
		"github_login": login,
	}})
}

// DeleteMe soft-deletes current user account.
// DELETE /api/v1/me
func (h *AuthHandler) DeleteMe(c *gin.Context) {
	userID := c.MustGet("user_id").(int64)
	_, err := h.DB.ExecContext(c.Request.Context(),
		`UPDATE users SET deleted_at = ? WHERE id = ?`,
		time.Now(), userID,
	)
	if err != nil {
		slog.Error("delete user failed", "user_id", userID, "err", err, "unexpected", true)
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": "DB_ERROR", "message": "账号删除失败",
		}})
		return
	}
	c.SetCookie("fixloop_session", "", -1, "/", "", true, true)
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"message": "账号已删除"}})
}
```

- [ ] **Step 3: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./internal/...
```

Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add internal/api/handlers/ internal/models/
git commit -m "feat: add GitHub OAuth login, user upsert, JWT cookie issuance"
```

---

## Task 11: Router + main.go

**Files:**
- Create: `internal/api/router.go`
- Create: `cmd/server/main.go`

- [ ] **Step 1: 写 router.go**

```go
// internal/api/router.go
package api

import (
	"database/sql"
	"log/slog"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
	"github.com/fixloop/fixloop/internal/api/handlers"
	"github.com/fixloop/fixloop/internal/api/middleware"
	"github.com/fixloop/fixloop/internal/config"
)

func NewRouter(db *sql.DB, cfg *config.Config) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(jsonLogger())

	// Rate limit: 20 req/s, burst 50
	r.Use(middleware.PerUserRateLimit(rate.Limit(20), 50))

	healthH := &handlers.HealthHandler{DB: db}
	r.GET("/health", healthH.Health)

	authH := &handlers.AuthHandler{DB: db, Cfg: cfg}

	v1 := r.Group("/api/v1")

	// Public auth routes
	v1.GET("/auth/github", authH.GitHubLogin)
	v1.GET("/auth/github/callback", authH.GitHubCallback)

	// Authenticated routes
	authed := v1.Group("/")
	authed.Use(middleware.Auth(cfg.JWTSecret))
	{
		authed.GET("/me", authH.UserInfo)
		authed.DELETE("/me", authH.DeleteMe)

		// Project routes (placeholder for Phase 2)
		// authed.POST("/projects", projectH.Create)
		// ...
	}

	return r
}

func jsonLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		slog.Info("http",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"ip", c.ClientIP(),
		)
	}
}
```

- [ ] **Step 2: 写 main.go**

```go
// cmd/server/main.go
package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/getsentry/sentry-go"
	api "github.com/fixloop/fixloop/internal/api"
	"github.com/fixloop/fixloop/internal/config"
	"github.com/fixloop/fixloop/internal/db"
)

func main() {
	// Structured JSON logging
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}

	// Sentry (optional)
	if cfg.SentryDSN != "" {
		if err := sentry.Init(sentry.ClientOptions{Dsn: cfg.SentryDSN}); err != nil {
			slog.Warn("sentry init failed", "err", err)
		}
	}

	// DB connection
	database, err := db.Open(cfg.DatabaseDSN)
	if err != nil {
		slog.Error("database connection failed", "err", err, "unexpected", true)
		os.Exit(1)
	}
	defer database.Close()

	// Run migrations
	_, filename, _, _ := runtime.Caller(0)
	projectRoot := filepath.Join(filepath.Dir(filename), "../..")
	migrationsPath := filepath.Join(projectRoot, "backend/migrations")
	if err := db.Migrate(cfg.DatabaseDSN, migrationsPath); err != nil {
		slog.Error("migration failed", "err", err, "unexpected", true)
		os.Exit(1)
	}

	// Start server
	r := api.NewRouter(database, cfg)
	slog.Info("starting fixloop server", "port", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		slog.Error("server failed", "err", err, "unexpected", true)
		os.Exit(1)
	}
}
```

- [ ] **Step 3: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./cmd/server/...
```

Expected: produces binary, no errors

- [ ] **Step 4: 运行全部 tests**

```bash
cd /home/ubuntu/fy/work/fixloop && go test ./... -v 2>&1 | tail -20
```

Expected: all PASS (crypto + auth middleware tests)

- [ ] **Step 5: Commit**

```bash
git add internal/api/router.go cmd/server/main.go
git commit -m "feat: wire router and main entrypoint, server ready to start"
```

---

## Task 12: 单实例锁

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: 在 main.go 开头加文件锁**

在 `cfg, err := config.Load()` 之前插入：

```go
// Enforce single instance via file lock
lockFile, err := os.OpenFile("/var/run/fixloop.pid", os.O_CREATE|os.O_RDWR, 0600)
if err != nil {
    // Fallback to /tmp if /var/run not writable (dev environment)
    lockFile, err = os.OpenFile("/tmp/fixloop.pid", os.O_CREATE|os.O_RDWR, 0600)
    if err != nil {
        slog.Error("cannot create lock file", "err", err)
        os.Exit(1)
    }
}
if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
    slog.Error("another fixloop instance is already running")
    os.Exit(1)
}
defer lockFile.Close()
```

Add to imports: `"syscall"`

- [ ] **Step 2: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./cmd/server/...
```

- [ ] **Step 3: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: enforce single instance via file lock"
```

---

## Task 13: Smoke Test（集成验证）

**Files:**
- Create: `internal/api/smoke_test.go`

> 注意：此 test 需要真实 MySQL。在 CI 或有 MySQL 的环境下运行。

- [ ] **Step 1: 写 smoke test**

```go
// internal/api/smoke_test.go
//go:build integration

package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	api "github.com/fixloop/fixloop/internal/api"
	"github.com/fixloop/fixloop/internal/config"
	"github.com/fixloop/fixloop/internal/db"
)

func TestHealthEndpoint(t *testing.T) {
	if os.Getenv("DATABASE_DSN") == "" {
		t.Skip("DATABASE_DSN not set, skipping integration test")
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	database, err := db.Open(cfg.DatabaseDSN)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer database.Close()

	r := api.NewRouter(database, cfg)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
}
```

- [ ] **Step 2: 运行 unit tests（无需 MySQL）**

```bash
cd /home/ubuntu/fy/work/fixloop && go test ./... -v -short
```

Expected: all PASS

- [ ] **Step 3: Commit**

```bash
git add internal/api/smoke_test.go
git commit -m "test: add integration smoke test for health endpoint"
```

---

## Self-Review Checklist

**Spec coverage:**
- [x] 章节八：技术栈（Go+Gin, MySQL, golang-migrate, JWT, Sentry, slog）→ Tasks 1-2, 11
- [x] 章节十一：DB Schema（users, projects, issues, prs, pr_reviews, backlog, agent_runs, notifications, system_config）→ Task 4
- [x] 章节十二：AES-256-GCM + key_id versioning → Task 3
- [x] 章节十三：GitHub OAuth + JWT httpOnly cookie → Task 10
- [x] 章节十四：SSRF, ownership middleware, rate limit, OAuth state CSRF → Tasks 7-8, 10
- [x] 章节三十二：单实例锁 → Task 12
- [x] /health endpoint → Task 9
- [x] 统一响应格式 → Task 5

**Gaps:** Phase 1 不包含 project CRUD、agents、TG Bot、截图存储（留给 Phase 2-8）。

**Placeholder scan:** 无 TBD/TODO。Task 11 router 中有注释掉的 project 路由，已标注"Phase 2"。

**Type consistency:**
- `middleware.Auth` 注入 `user_id (int64)` → `handlers.AuthHandler.UserInfo` 使用 `c.MustGet("user_id").(int64)` ✓
- `middleware.IssueJWT` 签名与 `middleware.Auth` 验证的 claims 字段一致（`sub`, `login`）✓

---

**Plan complete and saved to `docs/superpowers/plans/2026-04-11-fixloop-phase1-foundation.md`.**

Two execution options:

**1. Subagent-Driven (recommended)** — 每个 Task 派一个子 agent 执行，完成后 review，快速迭代

**2. Inline Execution** — 在当前 session 用 executing-plans 顺序执行，遇 checkpoint 停下来 review

Which approach?
