package config

import (
	"encoding/hex"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds all runtime configuration loaded from config.yaml.
type Config struct {
	// HTTP 监听端口，默认 "8080"
	Port string

	// MySQL DSN，含 parseTime=true
	DatabaseDSN string

	// AES-GCM 加密密钥（32 字节），用于加密数据库中的 PAT / API Key 等敏感字段
	AESKey []byte
	// AESKeyID 是密钥版本号，写入加密数据开头 1 字节，支持密钥轮换；当前固定为 1
	AESKeyID byte

	// JWT 签名密钥（至少 32 字节），用于签发登录 token
	JWTSecret []byte

	// GitHub OAuth App 凭据
	GitHubClientID     string
	GitHubClientSecret string
	// GitHubRedirectURL 必须与 OAuth App 中配置的 Callback URL 完全一致
	GitHubRedirectURL string

	// Sentry DSN；空字符串表示禁用错误监控
	SentryDSN string

	// AppBaseURL 对外访问地址，用于生成 OAuth 回调链接、通知跳转等
	AppBaseURL string

	// Telegram Bot Token；空字符串表示禁用 TG 推送
	// 也可在管理后台动态配置（加密存储于 system_config 表）
	TGBotToken string
	// TGBotUsername Bot 用户名（不含 @），用于生成绑定链接
	TGBotUsername string

	// WorkspaceDir 是 Agent 存放本地 Git clone 的根目录。
	// 布局：{WorkspaceDir}/{userID}/{projectID}/{agentAlias}/
	// 默认 /data/projects；目录不存在时可通过管理后台初始化。
	WorkspaceDir string

	// Cloudflare R2 对象存储；AccountID 为空时禁用服务级截图上传
	R2AccountID       string
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2BucketName      string
}

// yamlConfig mirrors the YAML structure exactly.
type yamlConfig struct {
	Server struct {
		Port string `yaml:"port"`
	} `yaml:"server"`
	Database struct {
		DSN string `yaml:"dsn"`
	} `yaml:"database"`
	Security struct {
		AESKey    string `yaml:"aes_key"`    // 64-char hex (32 bytes)
		JWTSecret string `yaml:"jwt_secret"` // at least 32 chars
	} `yaml:"security"`
	GitHub struct {
		ClientID     string `yaml:"client_id"`
		ClientSecret string `yaml:"client_secret"`
		RedirectURL  string `yaml:"redirect_url"`
	} `yaml:"github"`
	Sentry struct {
		DSN string `yaml:"dsn"`
	} `yaml:"sentry"`
	App struct {
		BaseURL string `yaml:"base_url"`
	} `yaml:"app"`
	TG struct {
		BotToken    string `yaml:"bot_token"`
		BotUsername string `yaml:"bot_username"`
	} `yaml:"tg"`
	Workspace struct {
		Dir string `yaml:"dir"`
	} `yaml:"workspace"`
	R2 struct {
		AccountID       string `yaml:"account_id"`
		AccessKeyID     string `yaml:"access_key_id"`
		SecretAccessKey string `yaml:"secret_access_key"`
		BucketName      string `yaml:"bucket_name"`
	} `yaml:"r2"`
}

// Load reads config.yaml (path from CONFIG_FILE env, default "config.yaml").
func Load() (*Config, error) {
	path := os.Getenv("CONFIG_FILE")
	if path == "" {
		path = "config.yaml"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}

	var y yamlConfig
	if err := yaml.Unmarshal(data, &y); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	aesKey, err := hex.DecodeString(y.Security.AESKey)
	if err != nil || len(aesKey) != 32 {
		return nil, fmt.Errorf("security.aes_key must be a 64-char hex string (32 bytes)")
	}

	jwtSecret := []byte(y.Security.JWTSecret)
	if len(jwtSecret) < 32 {
		return nil, fmt.Errorf("security.jwt_secret must be at least 32 characters")
	}

	if y.Database.DSN == "" {
		return nil, fmt.Errorf("database.dsn is required")
	}
	if y.GitHub.ClientID == "" || y.GitHub.ClientSecret == "" {
		return nil, fmt.Errorf("github.client_id and github.client_secret are required")
	}
	if y.GitHub.RedirectURL == "" {
		return nil, fmt.Errorf("github.redirect_url is required")
	}

	port := y.Server.Port
	if port == "" {
		port = "8080"
	}
	baseURL := y.App.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:" + port
	}
	workspaceDir := y.Workspace.Dir
	if workspaceDir == "" {
		workspaceDir = "/data/projects"
	}

	return &Config{
		Port:               port,
		DatabaseDSN:        y.Database.DSN,
		AESKey:             aesKey,
		AESKeyID:           1,
		JWTSecret:          jwtSecret,
		GitHubClientID:     y.GitHub.ClientID,
		GitHubClientSecret: y.GitHub.ClientSecret,
		GitHubRedirectURL:  y.GitHub.RedirectURL,
		SentryDSN:          y.Sentry.DSN,
		AppBaseURL:         baseURL,
		WorkspaceDir:      workspaceDir,
		TGBotToken:        y.TG.BotToken,
		TGBotUsername:     y.TG.BotUsername,
		R2AccountID:       y.R2.AccountID,
		R2AccessKeyID:     y.R2.AccessKeyID,
		R2SecretAccessKey: y.R2.SecretAccessKey,
		R2BucketName:      y.R2.BucketName,
	}, nil
}
