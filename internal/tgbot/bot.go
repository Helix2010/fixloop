// internal/tgbot/bot.go
package tgbot

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/fixloop/fixloop/internal/config"
	"github.com/fixloop/fixloop/internal/crypto"
	githubclient "github.com/fixloop/fixloop/internal/github"
	"github.com/fixloop/fixloop/internal/gitops"
	"github.com/fixloop/fixloop/internal/runner"
	"github.com/fixloop/fixloop/internal/storage"
)

//go:embed prompts/issue_analysis.txt
var issueAnalysisPromptTmpl string

var issueAnalysisTmpl = template.Must(template.New("issue_analysis").Parse(issueAnalysisPromptTmpl))

// DefaultIssueAnalysisPrompt returns the built-in issue analysis prompt template text.
func DefaultIssueAnalysisPrompt() string { return issueAnalysisPromptTmpl }

// BotScheduler is the subset of the scheduler needed by the bot.
type BotScheduler interface {
	TriggerRun(projectID int64, agentAlias string)
}

// Bot wraps the Telegram bot API and handles commands and notifications.
type Bot struct {
	api           *tgbotapi.BotAPI
	db            *sql.DB
	cfg           *config.Config
	r2            *storage.R2Client // optional; nil = R2 disabled
	scheduler     BotScheduler      // optional; nil = can't trigger immediately
	knownChatSeen sync.Map          // chatID(int64) → "title|type|active" — dedup DB writes
}

// New creates a Bot. Returns nil, nil if no token is configured (TG disabled).
// If config.yaml token is empty, falls back to the encrypted token stored in
// system_config by the admin UI.
func New(cfg *config.Config, db *sql.DB, r2 *storage.R2Client) (*Bot, error) {
	token := cfg.TGBotToken
	if token == "" && db != nil {
		var encHex string
		if err := db.QueryRowContext(context.Background(),
			`SELECT value FROM system_config WHERE key_name = 'tg_bot_token'`,
		).Scan(&encHex); err == nil && encHex != "" {
			if enc, err := hex.DecodeString(encHex); err == nil {
				if plain, err := crypto.Decrypt(map[byte][]byte{cfg.AESKeyID: cfg.AESKey}, enc); err == nil {
					token = string(plain)
				}
			}
		}
	}
	if token == "" {
		return nil, nil
	}
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("tgbot: init: %w", err)
	}
	slog.Info("tgbot: connected", "username", api.Self.UserName)
	return &Bot{api: api, db: db, cfg: cfg, r2: r2}, nil
}

// SetScheduler wires the scheduler so commands like /fix can trigger runs immediately.
func (b *Bot) SetScheduler(s BotScheduler) {
	b.scheduler = s
}

// Run starts the long-poll loop and notification sender. Blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) {
	go b.sendPendingLoop(ctx)
	b.pollLoop(ctx)
}

func (b *Bot) pollLoop(ctx context.Context) {
	offset := b.loadOffset(ctx)
	u := tgbotapi.NewUpdate(offset)
	u.Timeout = 30
	u.AllowedUpdates = []string{"message", "my_chat_member"}

	updates := b.api.GetUpdatesChan(u)
	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			b.saveOffset(ctx, update.UpdateID+1)
			if update.MyChatMember != nil {
				chat := update.MyChatMember.Chat
				if chat.Type == "group" || chat.Type == "supergroup" || chat.Type == "channel" {
					newStatus := update.MyChatMember.NewChatMember.Status
					active := 1
					if newStatus == "left" || newStatus == "kicked" {
						active = 0
					}
					b.upsertKnownChat(ctx, chat.ID, chat.Title, chat.Type, active)
				}
			}
			if update.Message == nil {
				continue
			}
			msg := update.Message
			if msg.Chat.Type != "private" {
				b.upsertKnownChat(ctx, msg.Chat.ID, msg.Chat.Title, msg.Chat.Type, 1)
			}
			slog.Info("tgbot: received message", "chat_type", msg.Chat.Type, "is_command", msg.IsCommand(), "cmd", msg.Command(), "has_photo", len(msg.Photo) > 0, "caption", msg.Caption)
			isPrivate := msg.Chat.Type == "private"
			if msg.IsCommand() {
				// In groups, respond to commands directed at this bot (@username suffix)
				// or commands that make sense in a group context.
				// In private, respond to all commands.
				if !isPrivate {
					botUsername := b.api.Self.UserName
					cmdEntity := msg.Entities[0]
					rawCmd := msg.Text[cmdEntity.Offset : cmdEntity.Offset+cmdEntity.Length]
					directedAtBot := strings.HasSuffix(rawCmd, "@"+botUsername)
					groupAllowed := map[string]bool{
						"issue": true, "fix": true, "run": true,
						"status": true, "issues": true, "pause": true, "resume": true,
					}
					if !directedAtBot && !groupAllowed[msg.Command()] {
						continue
					}
				}
				b.handleCommand(ctx, msg)
			} else if len(msg.Photo) > 0 {
				// Photo with /issue caption (private or group)
				caption := strings.TrimSpace(msg.Caption)
				if strings.HasPrefix(caption, "/issue") {
					senderChatID := msg.Chat.ID
					if msg.Chat.Type != "private" && msg.From != nil {
						senderChatID = msg.From.ID
					}
					var args string
					if rest := strings.SplitN(caption, " ", 2); len(rest) == 2 {
						args = rest[1]
					}
					b.cmdSubmitIssue(ctx, msg.Chat.ID, senderChatID, args, msg)
				}
			} else if isPrivate {
				// Plain text in private chat: treat as an issue submission attempt
				b.handlePlainText(ctx, msg)
			}
		}
	}
}

func (b *Bot) handleCommand(ctx context.Context, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID // reply target (group or private)
	cmd := msg.Command()
	args := strings.TrimSpace(msg.CommandArguments())

	// In groups, msg.Chat.ID is the group ID, not the user's personal chat ID.
	// tg_chat_id in users table stores the personal chat ID (== msg.From.ID).
	// Use senderID for user lookup; chatID for replies.
	senderID := chatID
	if msg.Chat.Type != "private" && msg.From != nil {
		senderID = msg.From.ID
	}

	switch cmd { //nolint:gocritic
	case "start":
		b.cmdStart(ctx, chatID, args)
	case "status":
		b.cmdStatus(ctx, chatID, senderID)
	case "issues":
		b.cmdIssues(ctx, chatID, senderID, args)
	case "issue":
		// In groups, resolve sender by msg.From.ID (private chat ID == user ID in TG)
		senderChatID := chatID
		if msg.Chat.Type != "private" && msg.From != nil {
			senderChatID = msg.From.ID
		}
		b.cmdSubmitIssue(ctx, chatID, senderChatID, args, msg)
	case "run":
		b.cmdRun(ctx, chatID, senderID, args)
	case "fix":
		b.cmdRunAlias(ctx, chatID, senderID, args, "fix")
	case "explore":
		b.cmdRunAlias(ctx, chatID, senderID, args, "explore")
	case "pause":
		b.cmdSetStatus(ctx, chatID, senderID, args, "paused")
	case "resume":
		b.cmdSetStatus(ctx, chatID, senderID, args, "active")
	default:
		b.send(chatID, "未知命令。支持: /status /issues /issue /fix /explore /run /pause /resume")
	}
}

func (b *Bot) cmdStart(ctx context.Context, chatID int64, token string) {
	if token == "" {
		b.send(chatID, "欢迎使用 FixLoop Bot！请在 Dashboard 获取绑定链接。")
		return
	}
	key := "tg_bind_" + token
	var userIDStr string
	err := b.db.QueryRowContext(ctx,
		`SELECT value FROM system_config WHERE key_name = ? AND updated_at > NOW() - INTERVAL 10 MINUTE`,
		key,
	).Scan(&userIDStr)
	if err != nil {
		b.send(chatID, "❌ 绑定 token 无效或已过期，请重新从 Dashboard 获取。")
		return
	}
	userID, _ := strconv.ParseInt(userIDStr, 10, 64)
	if userID == 0 {
		b.send(chatID, "❌ 绑定失败，请重试。")
		return
	}
	if _, err = b.db.ExecContext(ctx, `UPDATE users SET tg_chat_id = ? WHERE id = ?`, chatID, userID); err != nil {
		b.send(chatID, "❌ 数据库错误，请稍后重试。")
		return
	}
	_, _ = b.db.ExecContext(ctx, `DELETE FROM system_config WHERE key_name = ?`, key)
	b.send(chatID, "✅ 绑定成功！你将收到所有项目的通知。\n发送 /status 查看项目概况。")
}

func (b *Bot) cmdStatus(ctx context.Context, chatID, senderID int64) {
	userID := b.chatToUserID(ctx, senderID)
	if userID == 0 {
		b.send(chatID, "请先在 Dashboard 绑定 Telegram 账号。")
		return
	}
	rows, err := b.db.QueryContext(ctx,
		`SELECT p.name, p.status,
		        (SELECT COUNT(*) FROM issues i WHERE i.project_id = p.id AND i.status IN ('open','fixing')) AS open_issues,
		        (SELECT COUNT(*) FROM prs pr WHERE pr.project_id = p.id AND pr.status = 'open') AS open_prs
		 FROM projects p WHERE p.user_id = ? AND p.deleted_at IS NULL
		 ORDER BY p.created_at ASC`,
		userID,
	)
	if err != nil {
		b.send(chatID, "查询失败，请稍后重试。")
		return
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var name, status string
		var openIssues, openPRs int
		if err := rows.Scan(&name, &status, &openIssues, &openPRs); err != nil {
			continue
		}
		icon := "🟢"
		if status == "paused" {
			icon = "⏸"
		} else if status == "error" {
			icon = "🔴"
		}
		lines = append(lines, fmt.Sprintf("%s %s — %d issues, %d PRs", icon, name, openIssues, openPRs))
	}
	if len(lines) == 0 {
		b.send(chatID, "暂无项目。请在 Dashboard 创建。")
		return
	}
	b.send(chatID, "📊 项目状态\n\n"+strings.Join(lines, "\n"))
}

func (b *Bot) cmdIssues(ctx context.Context, chatID, senderID int64, projectName string) {
	userID := b.chatToUserID(ctx, senderID)
	if userID == 0 {
		b.send(chatID, "请先绑定账号。")
		return
	}
	if projectName == "" {
		b.send(chatID, "用法: /issues <项目名>")
		return
	}
	var projectID int64
	if err := b.db.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE user_id = ? AND name = ? AND deleted_at IS NULL`,
		userID, projectName,
	).Scan(&projectID); err != nil {
		b.send(chatID, fmt.Sprintf("找不到项目 %q", projectName))
		return
	}
	rows, err := b.db.QueryContext(ctx,
		`SELECT github_number, title, status, fix_attempts FROM issues
		 WHERE project_id = ? AND status IN ('open','fixing','needs-human')
		 ORDER BY priority ASC, created_at DESC LIMIT 10`,
		projectID,
	)
	if err != nil {
		b.send(chatID, "查询失败。")
		return
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var num, attempts int
		var title, status string
		if err := rows.Scan(&num, &title, &status, &attempts); err != nil {
			continue
		}
		icon := "🐛"
		if status == "fixing" {
			icon = "🔧"
		} else if status == "needs-human" {
			icon = "⚠️"
		}
		lines = append(lines, fmt.Sprintf("%s #%d %s (attempts: %d)", icon, num, title, attempts))
	}
	if len(lines) == 0 {
		b.send(chatID, fmt.Sprintf("✅ %s 暂无 open issues", projectName))
		return
	}
	b.send(chatID, strings.Join(lines, "\n"))
}

func (b *Bot) cmdRun(ctx context.Context, chatID, senderID int64, args string) {
	userID := b.chatToUserID(ctx, senderID)
	if userID == 0 {
		b.send(chatID, "请先绑定账号。")
		return
	}
	parts := strings.Fields(args)
	if len(parts) < 2 {
		b.send(chatID, "用法: /run <agent> <项目名>\nagent: fix | explore | plan | master\n\n快捷方式: /fix <项目名>  /explore <项目名>")
		return
	}
	agentAlias, projectName := parts[0], parts[1]
	allowed := map[string]bool{"fix": true, "explore": true, "plan": true, "master": true}
	if !allowed[agentAlias] {
		b.send(chatID, "agent 必须是 fix/explore/plan/master")
		return
	}
	b.triggerAgent(ctx, chatID, userID, projectName, agentAlias)
}

// cmdRunAlias handles shortcut commands like /fix and /explore.
// If args is empty and the user has exactly one project, uses that project.
func (b *Bot) cmdRunAlias(ctx context.Context, chatID, senderID int64, args, agentAlias string) {
	userID := b.chatToUserID(ctx, senderID)
	if userID == 0 {
		b.send(chatID, "请先绑定账号。")
		return
	}
	projectName := strings.TrimSpace(args)
	if projectName == "" {
		// Try to find the only active project
		rows, err := b.db.QueryContext(ctx,
			`SELECT name FROM projects WHERE user_id = ? AND status = 'active' AND deleted_at IS NULL LIMIT 2`,
			userID,
		)
		if err != nil {
			b.send(chatID, "查询项目失败，请稍后重试。")
			return
		}
		var names []string
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil {
				names = append(names, n)
			}
		}
		rows.Close()
		switch len(names) {
		case 0:
			b.send(chatID, "没有找到活跃的项目，请先在 Dashboard 创建并激活项目。")
			return
		case 1:
			projectName = names[0]
		default:
			b.send(chatID, fmt.Sprintf("你有多个项目，请指定：/%s <项目名>", agentAlias))
			return
		}
	}
	b.triggerAgent(ctx, chatID, userID, projectName, agentAlias)
}

func (b *Bot) triggerAgent(ctx context.Context, chatID, userID int64, projectName, agentAlias string) {
	var projectID int64
	var status string
	if err := b.db.QueryRowContext(ctx,
		`SELECT id, status FROM projects WHERE user_id = ? AND name = ? AND deleted_at IS NULL`,
		userID, projectName,
	).Scan(&projectID, &status); err != nil {
		b.send(chatID, fmt.Sprintf("找不到项目 %q", projectName))
		return
	}
	if status != "active" {
		b.send(chatID, fmt.Sprintf("项目 %s 未激活 (status=%s)，请先 /resume %s", projectName, status, projectName))
		return
	}
	if b.scheduler != nil {
		b.scheduler.TriggerRun(projectID, agentAlias)
		b.send(chatID, fmt.Sprintf("✅ 已触发 %s-agent → %s", agentAlias, projectName))
	} else {
		b.send(chatID, fmt.Sprintf("⚠️ 调度器未就绪，无法立即触发。请稍后重试。"))
	}
}

func (b *Bot) cmdSetStatus(ctx context.Context, chatID, senderID int64, projectName, newStatus string) {
	userID := b.chatToUserID(ctx, senderID)
	if userID == 0 {
		b.send(chatID, "请先绑定账号。")
		return
	}
	if projectName == "" {
		cmd := "pause"
		if newStatus == "active" {
			cmd = "resume"
		}
		b.send(chatID, fmt.Sprintf("用法: /%s <项目名>", cmd))
		return
	}
	res, err := b.db.ExecContext(ctx,
		`UPDATE projects SET status = ? WHERE user_id = ? AND name = ? AND deleted_at IS NULL`,
		newStatus, userID, projectName,
	)
	if err != nil {
		b.send(chatID, "更新失败，请稍后重试。")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		b.send(chatID, fmt.Sprintf("找不到项目 %q", projectName))
		return
	}
	icon := "⏸"
	word := "已暂停"
	if newStatus == "active" {
		icon = "▶️"
		word = "已恢复"
	}
	b.send(chatID, fmt.Sprintf("%s 项目 %s %s", icon, projectName, word))
}

// handlePlainText handles non-command private messages.
// If the message looks like an issue report, guide the user to use /issue.
func (b *Bot) handlePlainText(ctx context.Context, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	userID := b.chatToUserID(ctx, chatID)
	if userID == 0 {
		b.send(chatID, "请先在 Dashboard 绑定 Telegram 账号，然后发送 /start 开始使用。")
		return
	}
	// Auto-detect: single project → use it directly; multiple → ask to specify
	rows, err := b.db.QueryContext(ctx,
		`SELECT name FROM projects WHERE user_id = ? AND deleted_at IS NULL AND status = 'active' ORDER BY created_at ASC`,
		userID,
	)
	if err != nil {
		return
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			names = append(names, n)
		}
	}
	text := strings.TrimSpace(msg.Text)
	if len(names) == 1 {
		b.send(chatID, fmt.Sprintf("收到！提交 issue 到项目 %s，请发送：\n\n/issue %s %s", names[0], names[0], text))
	} else if len(names) > 1 {
		b.send(chatID, fmt.Sprintf("请用 /issue 命令提交：\n\n/issue <项目名> <问题描述>\n\n你的项目：%s", strings.Join(names, "、")))
	} else {
		b.send(chatID, "暂无活跃项目。请在 Dashboard 创建并激活项目。")
	}
}

// projectS3Cfg holds the S3 section of a project's config JSON.
// Defined as a named type so it can be shared between pcfg and s3ClientFor.
type projectS3Cfg struct {
	Endpoint    string `json:"endpoint"`
	Bucket      string `json:"bucket"`
	Region      string `json:"region"`
	AccessKeyID string `json:"access_key_id"`
	SecretKey   string `json:"secret_key"` // hex(AES encrypted)
}

// s3ClientFor constructs an S3 client from the project's config on each call.
// Returns nil if the project has no S3 config or decryption fails.
func (b *Bot) s3ClientFor(projectID int64, cfg *projectS3Cfg) *storage.S3Client {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKeyID == "" || cfg.SecretKey == "" {
		return nil
	}
	secret, err := b.decryptHexStr(cfg.SecretKey)
	if err != nil {
		slog.Warn("tgbot: s3 secret decrypt failed", "project_id", projectID, "err", err)
		return nil
	}
	s3c, err := storage.NewS3Client(cfg.Endpoint, cfg.Bucket, cfg.Region, cfg.AccessKeyID, secret)
	if err != nil {
		slog.Warn("tgbot: s3 client init failed", "project_id", projectID, "err", err)
		return nil
	}
	return s3c
}

// decryptHexStr decodes a hex-encoded AES-GCM ciphertext and returns the plaintext.
func (b *Bot) decryptHexStr(hexStr string) (string, error) {
	enc, err := hex.DecodeString(hexStr)
	if err != nil {
		return "", err
	}
	plain, err := crypto.Decrypt(map[byte][]byte{b.cfg.AESKeyID: b.cfg.AESKey}, enc)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// cmdSubmitIssue handles /issue <project> <title>.
// chatID is where to reply; senderChatID is used to look up the user (differs in groups).
// msg is the original Telegram message, used to extract an attached photo if present.
func (b *Bot) cmdSubmitIssue(ctx context.Context, chatID, senderChatID int64, args string, msg *tgbotapi.Message) {
	slog.Info("tgbot: cmdSubmitIssue", "chat_id", chatID, "sender_chat_id", senderChatID, "has_photo", len(msg.Photo) > 0, "args", args)
	userID := b.chatToUserID(ctx, senderChatID)
	if userID == 0 {
		b.send(chatID, "请先在 Dashboard 绑定 Telegram 账号。")
		return
	}
	idx := strings.IndexByte(args, ' ')
	if idx < 0 {
		b.send(chatID, "用法: /issue <项目名> <问题描述>")
		return
	}
	projectName := strings.TrimSpace(args[:idx])
	rawDesc := strings.TrimSpace(args[idx+1:])
	if rawDesc == "" {
		b.send(chatID, "用法: /issue <项目名> <问题描述>")
		return
	}

	// Load project config
	var cfgJSON string
	var projectID int64
	if err := b.db.QueryRowContext(ctx,
		`SELECT id, config FROM projects WHERE user_id = ? AND name = ? AND deleted_at IS NULL`,
		userID, projectName,
	).Scan(&projectID, &cfgJSON); err != nil {
		b.send(chatID, fmt.Sprintf("找不到项目 %q，请检查项目名称。", projectName))
		return
	}

	var pcfg struct {
		GitHub struct {
			Owner         string `json:"owner"`
			Repo          string `json:"repo"`
			PAT           string `json:"pat"`
			SSHPrivateKey string `json:"ssh_private_key"` // hex(AES encrypted)
			FixBaseBranch string `json:"fix_base_branch"`
		} `json:"github"`
		IssueTracker struct {
			Owner string `json:"owner"`
			Repo  string `json:"repo"`
		} `json:"issue_tracker"`
		Test struct {
			StagingURL string `json:"staging_url"`
		} `json:"test"`
		S3              projectS3Cfg `json:"s3"`
		AIRunner        string       `json:"ai_runner"`
		AIModel         string       `json:"ai_model"`
		AIAPIBase       string       `json:"ai_api_base"`
		AIAPIKey        string       `json:"ai_api_key"` // hex(AES encrypted)
		PromptOverrides struct {
			IssueAnalysis string `json:"issue_analysis,omitempty"`
		} `json:"prompt_overrides,omitempty"`
	}
	if err := json.Unmarshal([]byte(cfgJSON), &pcfg); err != nil || pcfg.IssueTracker.Owner == "" {
		b.send(chatID, "项目 GitHub 配置不完整，请在设置页补全。")
		return
	}

	pat, err := b.decryptHexStr(pcfg.GitHub.PAT)
	if err != nil || pat == "" {
		b.send(chatID, "GitHub PAT 未配置或解密失败，请在项目设置页填写。")
		return
	}

	// Decrypt AI key early so we can determine the runner type before starting I/O.
	aiAPIKey, _ := b.decryptHexStr(pcfg.AIAPIKey)
	model := pcfg.AIModel
	if model == "" {
		model = "claude-opus-4-6"
	}
	// CLI runner is used when no explicit API key is provided for Claude.
	// Only the CLI runner needs a local screenshot file (it reads it via the Read tool).
	useCliRunner := (pcfg.AIRunner == "claude" || pcfg.AIRunner == "") && aiAPIKey == ""

	b.send(chatID, "🔍 正在结合截图和源码进行 AI 分析，请稍候（通常需要 1~2 分钟）…")

	gh := githubclient.New(pat)

	// Download screenshot bytes from Telegram (needed for both CDN upload and CLI analysis).
	var imgData []byte
	if len(msg.Photo) > 0 {
		largest := msg.Photo[len(msg.Photo)-1]
		if tgFile, err := b.api.GetFile(tgbotapi.FileConfig{FileID: largest.FileID}); err == nil {
			if resp, err := http.Get(tgFile.Link(b.api.Token)); err == nil { //nolint:noctx
				imgData, _ = io.ReadAll(resp.Body)
				resp.Body.Close()
			}
		}
	}

	// Run CDN upload and git clone concurrently — both are long network operations
	// and are independent of each other.
	var (
		screenshotPath string // local temp path for Claude CLI Read tool
		screenshotURL  string // GitHub CDN URL to embed in issue markdown
		repoPath       string
		wg             sync.WaitGroup
	)

	if len(imgData) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ts := time.Now().UnixMilli()
			// Only write to disk when the CLI runner will use it.
			if useCliRunner {
				tmpPath := fmt.Sprintf("/tmp/fixloop-issue-%d-%d.jpg", projectID, ts)
				if os.WriteFile(tmpPath, imgData, 0600) == nil {
					screenshotPath = tmpPath
				}
			}
			// Try project-level S3 first, then fall back to server-level R2.
			key := fmt.Sprintf("issue-%d-%d.jpg", projectID, ts)
			if s3c := b.s3ClientFor(projectID, &pcfg.S3); s3c != nil {
				if err := s3c.UploadBytes(ctx, key, imgData, "image/jpeg"); err != nil {
					slog.Warn("tgbot: project s3 upload failed", "err", err)
				} else {
					screenshotURL = s3c.PublicURL(key)
				}
			}
			if screenshotURL == "" && !b.r2.Disabled() {
				fname := "screenshots/" + key
				if err := b.r2.UploadBytes(ctx, fname, imgData, "image/jpeg"); err != nil {
					slog.Warn("tgbot: r2 upload failed", "err", err)
				} else if presignURL, err := b.r2.PresignURL(ctx, fname, 7*24*time.Hour); err != nil {
					slog.Warn("tgbot: r2 presign failed", "err", err)
				} else {
					screenshotURL = presignURL
				}
			}
		}()
	}

	if pcfg.GitHub.SSHPrivateKey != "" {
		if sshKey, err := b.decryptHexStr(pcfg.GitHub.SSHPrivateKey); err == nil {
			baseBranch := pcfg.GitHub.FixBaseBranch
			if baseBranch == "" {
				baseBranch = "main"
			}
			rp := gitops.RepoPath(b.cfg.WorkspaceDir, pcfg.GitHub.Owner, pcfg.GitHub.Repo)
			cloneCtx, cloneCancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cloneCancel()
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := gitops.EnsureRepo(cloneCtx, []byte(sshKey), pcfg.GitHub.Owner, pcfg.GitHub.Repo, rp, baseBranch); err == nil {
					repoPath = rp
				} else {
					slog.Warn("tgbot: issue analysis: EnsureRepo failed", "err", err)
				}
			}()
		}
	}

	wg.Wait()
	if screenshotPath != "" {
		defer os.Remove(screenshotPath)
	}

	// Build analysis prompt from template (project override takes priority)
	activeTmpl := issueAnalysisTmpl
	if pcfg.PromptOverrides.IssueAnalysis != "" {
		if t, err := template.New("issue_analysis_override").Parse(pcfg.PromptOverrides.IssueAnalysis); err == nil {
			activeTmpl = t
		} else {
			slog.Warn("tgbot: parse issue_analysis prompt override failed, using default", "err", err)
		}
	}
	var promptBuf bytes.Buffer
	_ = activeTmpl.Execute(&promptBuf, struct {
		Repo           string
		StagingURL     string
		Description    string
		ScreenshotPath string
		ScreenshotURL  string
	}{
		Repo:           fmt.Sprintf("%s/%s", pcfg.IssueTracker.Owner, pcfg.IssueTracker.Repo),
		StagingURL:     pcfg.Test.StagingURL,
		Description:    rawDesc,
		ScreenshotPath: screenshotPath,
		ScreenshotURL:  screenshotURL,
	})

	analyzeCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	r, err := runner.New(pcfg.AIRunner, model, pcfg.AIAPIBase, aiAPIKey)
	if err != nil {
		r = &runner.ClaudeCLIRunner{Model: model}
	}
	aiOutput, _ := r.Run(analyzeCtx, repoPath, promptBuf.String())

	issueBody := strings.TrimSpace(aiOutput)
	if issueBody == "" {
		issueBody = fmt.Sprintf("由 Telegram 用户通过 FixLoop Bot 提交。\n\n---\n%s", rawDesc)
	}

	title := rawDesc
	if nl := strings.IndexByte(rawDesc, '\n'); nl > 0 {
		title = strings.TrimSpace(rawDesc[:nl])
	}
	if len(title) > 120 {
		title = title[:120]
	}

	issue, err := gh.CreateIssue(ctx, pcfg.IssueTracker.Owner, pcfg.IssueTracker.Repo, title, issueBody, []string{"bug"})
	if err != nil {
		slog.Warn("tgbot: create github issue failed", "err", err)
		b.send(chatID, fmt.Sprintf("❌ 创建 GitHub Issue 失败：%v", err))
		return
	}

	hash := fmt.Sprintf("%x", sha1.Sum([]byte(title)))
	_, _ = b.db.ExecContext(ctx,
		`INSERT IGNORE INTO issues (project_id, github_number, title, title_hash, priority, status)
		 VALUES (?, ?, ?, ?, 2, 'open')`,
		projectID, issue.Number, title, hash,
	)

	b.send(chatID, fmt.Sprintf("✅ Issue #%d 已创建并附上 AI 分析报告：\n%s", issue.Number, issue.HTMLURL))
}

// sendPendingLoop polls every 30s for unsent notifications.
func (b *Bot) sendPendingLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.sendPending(ctx)
		}
	}
}

func (b *Bot) sendPending(ctx context.Context) {
	// Fetch unsent notifications. Prefer project-level tg_chat_id from config JSON;
	// fall back to the user's bound chat. Rows without any chat are skipped.
	rows, err := b.db.QueryContext(ctx,
		`SELECT n.id, n.project_id, u.tg_chat_id, p.config, n.content
		 FROM notifications n
		 JOIN users u ON u.id = n.user_id
		 LEFT JOIN projects p ON p.id = n.project_id AND p.deleted_at IS NULL
		 WHERE n.tg_sent = FALSE
		 ORDER BY n.created_at ASC LIMIT 50`,
	)
	if err != nil {
		return
	}
	defer rows.Close()

	var sent []int64
	for rows.Next() {
		var id int64
		var projectID *int64
		var userChatID *int64
		var cfgJSON *string
		var content string
		if err := rows.Scan(&id, &projectID, &userChatID, &cfgJSON, &content); err != nil {
			continue
		}

		// Determine which chat to send to.
		chatID := resolveChat(userChatID, cfgJSON)
		if chatID == 0 {
			// No chat configured anywhere — mark sent to avoid re-processing.
			sent = append(sent, id)
			continue
		}

		if len(content) > 4000 {
			content = content[:4000] + "…"
		}
		if _, err := b.api.Send(tgbotapi.NewMessage(chatID, content)); err != nil {
			slog.Warn("tgbot: send notification failed", "id", id, "err", err)
			continue
		}
		sent = append(sent, id)
	}

	if len(sent) == 0 {
		return
	}
	args := make([]any, len(sent))
	for i, id := range sent {
		args[i] = id
	}
	placeholders := strings.Repeat(",?", len(sent))[1:]
	_, _ = b.db.ExecContext(ctx,
		`UPDATE notifications SET tg_sent = TRUE WHERE id IN (`+placeholders+`)`,
		args...,
	)
}

// resolveChat returns the effective Telegram chat ID for a notification.
// It prefers the project-level tg_chat_id from the config JSON, then falls
// back to the user's bound chat. Returns 0 if no chat is available.
func resolveChat(userChatID *int64, cfgJSON *string) int64 {
	if cfgJSON != nil {
		var cfg struct {
			TGChatID *int64 `json:"tg_chat_id"`
		}
		if err := json.Unmarshal([]byte(*cfgJSON), &cfg); err == nil && cfg.TGChatID != nil {
			return *cfg.TGChatID
		}
	}
	if userChatID != nil {
		return *userChatID
	}
	return 0
}

func (b *Bot) chatToUserID(ctx context.Context, chatID int64) int64 {
	var userID int64
	_ = b.db.QueryRowContext(ctx,
		`SELECT id FROM users WHERE tg_chat_id = ? AND deleted_at IS NULL`, chatID,
	).Scan(&userID)
	return userID
}

func (b *Bot) send(chatID int64, text string) {
	if _, err := b.api.Send(tgbotapi.NewMessage(chatID, text)); err != nil {
		slog.Warn("tgbot: send failed", "chat_id", chatID, "err", err)
	}
}

func (b *Bot) loadOffset(ctx context.Context) int {
	var v string
	_ = b.db.QueryRowContext(ctx, `SELECT value FROM system_config WHERE key_name = 'tg_last_update_id'`).Scan(&v)
	n, _ := strconv.Atoi(v)
	return n
}

func (b *Bot) saveOffset(ctx context.Context, offset int) {
	_, _ = b.db.ExecContext(ctx,
		`INSERT INTO system_config (key_name, value) VALUES ('tg_last_update_id', ?)
		 ON DUPLICATE KEY UPDATE value = VALUES(value)`,
		strconv.Itoa(offset),
	)
}

func (b *Bot) upsertKnownChat(ctx context.Context, chatID int64, title, chatType string, active int) {
	if b.db == nil {
		return
	}
	// Deduplicate: skip DB write if nothing changed since last time we upserted this chat.
	key := fmt.Sprintf("%s|%s|%d", title, chatType, active)
	if prev, ok := b.knownChatSeen.Load(chatID); ok && prev.(string) == key {
		return
	}
	_, _ = b.db.ExecContext(ctx,
		`INSERT INTO tg_known_chats (chat_id, title, chat_type, active)
		 VALUES (?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE title = VALUES(title), chat_type = VALUES(chat_type), active = VALUES(active)`,
		chatID, title, chatType, active,
	)
	b.knownChatSeen.Store(chatID, key)
}
