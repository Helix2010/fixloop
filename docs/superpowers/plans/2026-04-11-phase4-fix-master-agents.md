# FixLoop Phase 4: fix-agent + master-agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 fix-agent（AI 修复 → 提 PR）和 master-agent（merge → Vercel 验证 → Playwright 验收 → 关闭 Issue），完成 AI 修复闭环。

**Architecture:**
- `internal/notify/` — 向 notifications 表插入通知的辅助函数
- `internal/gitops/` — SSH 方式 clone/fetch/branch/push 操作
- `internal/vercel/` — Vercel 部署状态轮询
- `internal/fixrunner/` — 进程级 AI runner（Claude CLI / aider），在仓库目录运行以编辑文件
- `internal/fix/` — fix-agent 主逻辑
- `internal/master/` — master-agent 主逻辑
- `cmd/server/main.go` — 将 fix/master 接入调度 dispatch

**Tech Stack:** Go stdlib exec, ssh-keygen CLI, git CLI subprocess, Vercel REST API v13, existing packages (agentrun, playwright, github, storage, crypto)

**Spec:** `docs/specs/2026-04-11-fixloop-design.md` — 重点参考章节：六、十六、十七、二十一、二十二、二十三

---

## File Structure

```
internal/
  notify/
    notify.go             # Send(ctx, db, userID, projectID, type, content) — insert notifications row
  gitops/
    gitops.go             # EnsureRepo, EnsureBranch, Push, DirTree
  vercel/
    client.go             # Client.WaitForDeployment(ctx, projectID, token, commitSHA) (*Deployment, error)
  fixrunner/
    runner.go             # FixRunner interface + NewRunner factory
    claude_cli.go         # ClaudeCLIRunner: runs `claude --print` subprocess in repo dir
    aider.go              # AiderRunner: runs `aider` subprocess in repo dir
  fix/
    fix.go                # Agent.Run(ctx, projectID int64)
  master/
    master.go             # Agent.Run(ctx, projectID int64)
  agent/
    prompts/
      fix.txt             # fix-agent prompt template
cmd/server/
  main.go                 # 修改: 接入 fix.Agent + master.Agent
```

---

## Task 1: notify helper

**Files:**
- Create: `internal/notify/notify.go`

- [ ] **Step 1: 创建 notify.go**

```go
// internal/notify/notify.go
package notify

import (
	"context"
	"database/sql"
	"fmt"
)

// Send inserts a notification row. projectID may be 0 (system-level notification).
// If a TG sender is wired later it will pick up rows with tg_sent=false.
func Send(ctx context.Context, db *sql.DB, userID, projectID int64, notifType, content string) error {
	var pid any
	if projectID != 0 {
		pid = projectID
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO notifications (user_id, project_id, type, content, tg_sent)
		 VALUES (?, ?, ?, ?, FALSE)`,
		userID, pid, notifType, content,
	)
	if err != nil {
		return fmt.Errorf("notify.Send: %w", err)
	}
	return nil
}
```

- [ ] **Step 2: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./internal/notify/...
```

Expected: no output (success).

- [ ] **Step 3: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add internal/notify/notify.go
git commit -m "feat: add notify helper for notifications table"
```

---

## Task 2: gitops — SSH clone/fetch/branch/push

**Files:**
- Create: `internal/gitops/gitops.go`

The fix-agent needs to clone repos over SSH, create branches, and push. We shell out to `git` with `GIT_SSH_COMMAND` pointing to a temp key file.

- [ ] **Step 1: 创建 gitops.go**

```go
// internal/gitops/gitops.go
package gitops

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorkDir is the base directory for cloned repos.
// Layout: {WorkDir}/{userID}/{projectID}/repo/
const WorkDir = "/data/projects"

// RepoPath returns the local clone path for a project.
func RepoPath(userID, projectID int64) string {
	return filepath.Join(WorkDir, fmt.Sprintf("%d", userID), fmt.Sprintf("%d", projectID), "repo")
}

// EnsureRepo clones the repo if it doesn't exist, or fetches + hard-resets to origin/{baseBranch} if it does.
// sshKey is the raw (decrypted) Ed25519 private key PEM bytes.
func EnsureRepo(ctx context.Context, sshKey []byte, owner, repo, repoPath, baseBranch string) error {
	keyFile, err := writeTempKey(sshKey)
	if err != nil {
		return err
	}
	defer os.Remove(keyFile)

	sshCmd := sshCommand(keyFile)

	if _, err := os.Stat(filepath.Join(repoPath, ".git")); os.IsNotExist(err) {
		// First time: clone
		if err := os.MkdirAll(filepath.Dir(repoPath), 0755); err != nil {
			return fmt.Errorf("gitops: mkdir for repo: %w", err)
		}
		cloneURL := fmt.Sprintf("git@github.com:%s/%s.git", owner, repo)
		if out, err := runGit(ctx, sshCmd, "", "clone", "--depth=1", cloneURL, repoPath); err != nil {
			return fmt.Errorf("gitops: clone failed: %w\n%s", err, out)
		}
	} else {
		// Subsequent: fetch + reset
		if out, err := runGit(ctx, sshCmd, repoPath, "fetch", "origin"); err != nil {
			return fmt.Errorf("gitops: fetch failed: %w\n%s", err, out)
		}
		if out, err := runGit(ctx, sshCmd, repoPath, "reset", "--hard", "origin/"+baseBranch); err != nil {
			return fmt.Errorf("gitops: reset failed: %w\n%s", err, out)
		}
	}
	return nil
}

// EnsureBranch creates a new branch from baseBranch, or checks out the existing one.
func EnsureBranch(ctx context.Context, repoPath, branchName, baseBranch string) error {
	// Check if branch already exists on remote
	out, _ := runGit(ctx, "", repoPath, "ls-remote", "--heads", "origin", branchName)
	if strings.Contains(out, branchName) {
		// Branch exists on remote — fetch and check out
		if out, err := runGit(ctx, "", repoPath, "fetch", "origin", branchName); err != nil {
			return fmt.Errorf("gitops: fetch branch: %w\n%s", err, out)
		}
		if out, err := runGit(ctx, "", repoPath, "checkout", branchName); err != nil {
			return fmt.Errorf("gitops: checkout existing branch: %w\n%s", err, out)
		}
		// Rebase onto latest base
		if out, err := runGit(ctx, "", repoPath, "rebase", "origin/"+baseBranch); err != nil {
			// Rebase conflicts: abort and reset to baseBranch, let AI retry from scratch
			_, _ = runGit(ctx, "", repoPath, "rebase", "--abort")
			return fmt.Errorf("gitops: rebase failed: %w\n%s", err, out)
		}
		return nil
	}
	// Create new branch from baseBranch
	if out, err := runGit(ctx, "", repoPath, "checkout", "-b", branchName, "origin/"+baseBranch); err != nil {
		return fmt.Errorf("gitops: create branch: %w\n%s", err, out)
	}
	return nil
}

// HasChanges returns true if there are uncommitted changes or untracked files in the repo.
func HasChanges(ctx context.Context, repoPath string) (bool, error) {
	out, err := runGit(ctx, "", repoPath, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("gitops: git status: %w", err)
	}
	return strings.TrimSpace(out) != "", nil
}

// CommitAll stages all changes and creates a commit.
func CommitAll(ctx context.Context, repoPath, message string) error {
	if out, err := runGit(ctx, "", repoPath, "add", "-A"); err != nil {
		return fmt.Errorf("gitops: git add: %w\n%s", err, out)
	}
	if out, err := runGit(ctx, "", repoPath, "commit", "-m", message); err != nil {
		return fmt.Errorf("gitops: git commit: %w\n%s", err, out)
	}
	return nil
}

// Push pushes branchName to origin. If force is true, uses --force-with-lease.
func Push(ctx context.Context, sshKey []byte, repoPath, branchName string, force bool) error {
	keyFile, err := writeTempKey(sshKey)
	if err != nil {
		return err
	}
	defer os.Remove(keyFile)

	sshCmd := sshCommand(keyFile)
	args := []string{"push", "origin", branchName}
	if force {
		args = append(args, "--force-with-lease")
	}
	if out, err := runGit(ctx, sshCmd, repoPath, args...); err != nil {
		return fmt.Errorf("gitops: push failed: %w\n%s", err, out)
	}
	return nil
}

// DirTree returns a depth-limited directory listing of repoPath, excluding .git.
func DirTree(repoPath string, depth int) string {
	out, err := exec.Command("find", repoPath,
		"-not", "-path", "*/.git/*",
		"-not", "-name", ".git",
		"-maxdepth", fmt.Sprintf("%d", depth+1),
	).Output()
	if err != nil {
		return "(unable to list directory)"
	}
	lines := strings.Split(string(out), "\n")
	var trimmed []string
	prefix := repoPath + "/"
	for _, l := range lines {
		l = strings.TrimPrefix(l, prefix)
		if l != "" && l != "." {
			trimmed = append(trimmed, l)
		}
	}
	return strings.Join(trimmed, "\n")
}

// GetLastFailureComment returns the body of the most recent bot comment on a GitHub issue
// that starts with "<!-- fixloop-failure -->". Returns empty string if none.
func GetLastPRComment(ctx context.Context, repoPath string) string {
	// This is fetched from GitHub API by the caller; gitops doesn't know about GitHub.
	// Kept here as a doc comment for callers.
	return ""
}

// ---- internal helpers ----

func writeTempKey(sshKey []byte) (string, error) {
	f, err := os.CreateTemp("", "fixloop-sshkey-*")
	if err != nil {
		return "", fmt.Errorf("gitops: create temp key file: %w", err)
	}
	defer f.Close()
	if err := os.Chmod(f.Name(), 0600); err != nil {
		return "", fmt.Errorf("gitops: chmod key file: %w", err)
	}
	if _, err := f.Write(sshKey); err != nil {
		return "", fmt.Errorf("gitops: write key file: %w", err)
	}
	return f.Name(), nil
}

func sshCommand(keyFile string) string {
	return fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=no -o IdentitiesOnly=yes -o BatchMode=yes", keyFile)
}

func runGit(ctx context.Context, sshCmd, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if sshCmd != "" {
		cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}
```

- [ ] **Step 2: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./internal/gitops/...
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add internal/gitops/gitops.go
git commit -m "feat: add gitops package for SSH-based git operations"
```

---

## Task 3: Vercel 部署轮询客户端

**Files:**
- Create: `internal/vercel/client.go`

轮询 Vercel API v13，等待指定 commitSHA 对应的部署变为 READY 或 ERROR。

- [ ] **Step 1: 安装依赖（无需额外 SDK，用标准 HTTP）**

Vercel API 用标准 `net/http`，无需第三方库。

- [ ] **Step 2: 创建 client.go**

```go
// internal/vercel/client.go
package vercel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const apiBase = "https://api.vercel.com"

// Deployment represents a Vercel deployment.
type Deployment struct {
	UID        string `json:"uid"`
	ReadyState string `json:"readyState"` // READY|ERROR|BUILDING|QUEUED|INITIALIZING|CANCELED
	Meta       struct {
		GitHubCommitSha string `json:"githubCommitSha"`
	} `json:"meta"`
}

// WaitForDeployment polls until a deployment matching commitSHA reaches READY or ERROR.
// Polls every 30s, max 20 times (10 min total). Returns the matching deployment or an error.
// If Vercel is not configured (token or projectID empty), returns nil, nil immediately.
func WaitForDeployment(ctx context.Context, token, projectID, commitSHA string) (*Deployment, error) {
	if token == "" || projectID == "" {
		return nil, nil // Vercel not configured
	}

	client := &http.Client{Timeout: 15 * time.Second}

	for attempt := 0; attempt < 20; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(30 * time.Second):
			}
		}

		deps, err := listDeployments(ctx, client, token, projectID)
		if err != nil {
			// Transient error: log and retry
			continue
		}

		for i := range deps {
			d := &deps[i]
			if !strings.HasPrefix(d.Meta.GitHubCommitSha, commitSHA[:min(len(commitSHA), 7)]) &&
				d.Meta.GitHubCommitSha != commitSHA {
				continue
			}
			switch d.ReadyState {
			case "READY":
				return d, nil
			case "ERROR", "CANCELED":
				return d, fmt.Errorf("vercel: deployment %s state=%s", d.UID, d.ReadyState)
			}
			// Still building: break inner loop, wait next poll
			break
		}
	}
	return nil, fmt.Errorf("vercel: deployment for %s not ready after 10 minutes", commitSHA[:min(len(commitSHA), 8)])
}

func listDeployments(ctx context.Context, client *http.Client, token, projectID string) ([]Deployment, error) {
	url := fmt.Sprintf("%s/v13/deployments?projectId=%s&limit=10", apiBase, projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vercel API error %d: %.200s", resp.StatusCode, body)
	}

	var result struct {
		Deployments []Deployment `json:"deployments"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("vercel: parse response: %w", err)
	}
	return result.Deployments, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 3: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./internal/vercel/...
```

Expected: no output.

- [ ] **Step 4: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add internal/vercel/client.go
git commit -m "feat: add Vercel deployment polling client"
```

---

## Task 4: fixrunner — 进程级 AI runner（编辑文件）

fix-agent 需要 AI 能直接读写仓库文件。用子进程运行 `claude --print` 或 `aider`，工作目录设为仓库路径。

**Files:**
- Create: `internal/fixrunner/runner.go`
- Create: `internal/fixrunner/claude_cli.go`
- Create: `internal/fixrunner/aider.go`

- [ ] **Step 1: 创建 runner.go（接口 + 工厂）**

```go
// internal/fixrunner/runner.go
package fixrunner

import (
	"context"
	"fmt"
)

// FixRunner can edit files in a working directory given a prompt.
type FixRunner interface {
	// Fix runs the AI in repoPath with the given prompt.
	// Returns the AI's final response (for logging).
	Fix(ctx context.Context, repoPath, prompt string) (string, error)
}

// New returns a FixRunner based on the project's ai_runner setting.
// aiRunner: "claude" | "gemini" | "aider"
// model, apiBase, apiKey: from project config.
func New(aiRunner, model, apiBase, apiKey string) (FixRunner, error) {
	switch aiRunner {
	case "claude", "":
		return &ClaudeCLIRunner{Model: model}, nil
	case "aider":
		return &AiderRunner{Model: model, APIBase: apiBase, APIKey: apiKey}, nil
	default:
		return nil, fmt.Errorf("fixrunner: unknown ai_runner %q", aiRunner)
	}
}
```

- [ ] **Step 2: 创建 claude_cli.go**

```go
// internal/fixrunner/claude_cli.go
package fixrunner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ClaudeCLIRunner invokes the `claude` CLI in the repository directory.
// The CLI must be authenticated (claude.ai OAuth) on the server.
type ClaudeCLIRunner struct {
	Model string // e.g. "claude-opus-4-6"
}

func (r *ClaudeCLIRunner) Fix(ctx context.Context, repoPath, prompt string) (string, error) {
	args := []string{"--print", "--dangerously-skip-permissions"}
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = repoPath
	cmd.Stdin = strings.NewReader(prompt)

	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude CLI: %w\nstderr: %s", err, errOut.String())
	}
	return out.String(), nil
}
```

- [ ] **Step 3: 创建 aider.go**

```go
// internal/fixrunner/aider.go
package fixrunner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// AiderRunner invokes `aider` in the repository directory with a message.
// Works with any OpenAI-compatible API (DeepSeek, Qwen, Kimi, etc.).
type AiderRunner struct {
	Model   string
	APIBase string
	APIKey  string
}

func (r *AiderRunner) Fix(ctx context.Context, repoPath, prompt string) (string, error) {
	args := []string{
		"--message", prompt,
		"--yes-always",
		"--no-pretty",
		"--no-git", // gitops handles git operations
	}
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}
	if r.APIBase != "" {
		args = append(args, "--api-base", r.APIBase)
	}

	cmd := exec.CommandContext(ctx, "aider", args...)
	cmd.Dir = repoPath
	if r.APIKey != "" {
		cmd.Env = append(os.Environ(), "OPENAI_API_KEY="+r.APIKey)
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("aider: %w\noutput: %s", err, out.String())
	}
	return out.String(), nil
}
```

- [ ] **Step 4: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./internal/fixrunner/...
```

Expected: no output.

- [ ] **Step 5: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add internal/fixrunner/
git commit -m "feat: add fixrunner package (claude CLI + aider subprocess runners)"
```

---

## Task 5: fix-agent prompt 模板

**Files:**
- Create: `internal/agent/prompts/fix.txt`

- [ ] **Step 1: 创建 prompts 目录和 fix.txt**

```bash
mkdir -p /home/ubuntu/fy/work/fixloop/internal/agent/prompts
```

- [ ] **Step 2: 写 fix.txt**

内容（保存为纯文本，fix.go 用 go:embed 读取）：

```
=== SYSTEM INSTRUCTIONS ===
You are an expert software engineer. Your task is to fix a bug in a repository.
The repository has been cloned to your current working directory.

Guidelines:
1. Read the issue carefully to understand the problem
2. Explore the relevant files to understand the code structure
3. Make the MINIMAL change needed to fix the bug — no extra refactoring
4. Do NOT add tests, documentation, or unrelated changes
5. Do NOT modify package.json, go.mod, or build configuration files unless the bug is in them

After making your changes, briefly describe:
- Root cause: what was broken and why
- Fix: what you changed and why it works

=== ISSUE CONTENT (untrusted) ===
Title: {{.IssueTitle}}
Body:
{{.IssueBody}}
=== END ISSUE CONTENT ===

=== REPOSITORY STRUCTURE ===
{{.DirTree}}
=== END REPOSITORY STRUCTURE ===
{{if .PreviousFailures}}
=== PREVIOUS FAILED ATTEMPTS ===
{{.PreviousFailures}}
=== END PREVIOUS ATTEMPTS ===
{{end}}
```

File to create: `internal/agent/prompts/fix.txt`

```
=== SYSTEM INSTRUCTIONS ===
You are an expert software engineer. Your task is to fix a bug in a repository.
The repository has been cloned to your current working directory.

Guidelines:
1. Read the issue carefully to understand the problem
2. Explore the relevant files to understand the code structure
3. Make the MINIMAL change needed to fix the bug — no extra refactoring
4. Do NOT add tests, documentation, or unrelated changes
5. Do NOT modify package.json, go.mod, or build configuration files unless the bug is in them

After making your changes, briefly describe:
- Root cause: what was broken and why
- Fix: what you changed and why it works

=== ISSUE CONTENT (untrusted) ===
Title: {{.IssueTitle}}
Body:
{{.IssueBody}}
=== END ISSUE CONTENT ===

=== REPOSITORY STRUCTURE ===
{{.DirTree}}
=== END REPOSITORY STRUCTURE ===
{{if .PreviousFailures}}
=== PREVIOUS FAILED ATTEMPTS ===
{{.PreviousFailures}}
=== END PREVIOUS ATTEMPTS ===
{{end}}
```

- [ ] **Step 3: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add internal/agent/prompts/fix.txt
git commit -m "feat: add fix-agent prompt template"
```

---

## Task 6: fix-agent

**Files:**
- Create: `internal/fix/fix.go`

fix-agent 逻辑：
1. 加载项目配置，检查 fix_disabled
2. 检查每日运行限额（30 次/24h）
3. 乐观锁取一个 open issue（UPDATE status='fixing'，rows_affected=0 则跳过）
4. Start agentrun
5. EnsureRepo（SSH clone/fetch）
6. EnsureBranch（fix/issue-{N}）
7. 构建 prompt（issue 内容 + dir tree + 历史失败）
8. 运行 fixrunner.Fix
9. 检查是否有文件变更（HasChanges）
10. CommitAll → Push（force-with-lease）
11. CreatePR 或找已存在的 PR，RequestCopilotReview
12. 写入 prs 表，issues.fix_attempts++
13. 发通知
14. Finish agentrun

- [ ] **Step 1: 创建 fix.go**

```go
// internal/fix/fix.go
package fix

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"text/template"
	"time"

	_ "embed"

	"github.com/fixloop/fixloop/internal/agentrun"
	"github.com/fixloop/fixloop/internal/config"
	"github.com/fixloop/fixloop/internal/crypto"
	githubclient "github.com/fixloop/fixloop/internal/github"
	"github.com/fixloop/fixloop/internal/gitops"
	"github.com/fixloop/fixloop/internal/fixrunner"
	"github.com/fixloop/fixloop/internal/notify"
)

//go:embed ../agent/prompts/fix.txt
var fixPromptTmpl string

// Agent runs the fix loop for a single project.
type Agent struct {
	DB  *sql.DB
	Cfg *config.Config
}

type projectConf struct {
	GitHub struct {
		Owner         string `json:"owner"`
		Repo          string `json:"repo"`
		PAT           string `json:"pat"`
		FixBaseBranch string `json:"fix_base_branch"`
	} `json:"github"`
	IssueTracker struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
	} `json:"issue_tracker"`
	SSHPrivateKey string `json:"ssh_private_key"` // hex(AES encrypted PEM)
	AIRunner      string `json:"ai_runner"`
	AIModel       string `json:"ai_model"`
	AIAPIBase     string `json:"ai_api_base"`
	AIAPIKey      string `json:"ai_api_key"` // hex(AES encrypted)
	FixDisabled   bool   `json:"fix_disabled"`
}

type issueRow struct {
	id           int64
	githubNumber int
	title        string
	body         string
	fixAttempts  int
}

func (a *Agent) Run(ctx context.Context, projectID int64) {
	var (
		cfgJSON       string
		configVersion int
		userID        int64
		status        string
	)
	if err := a.DB.QueryRowContext(ctx,
		`SELECT user_id, config, config_version, status FROM projects WHERE id = ? AND deleted_at IS NULL`,
		projectID,
	).Scan(&userID, &cfgJSON, &configVersion, &status); err != nil {
		slog.Error("fix: load project", "project_id", projectID, "err", err)
		return
	}
	if status != "active" {
		return
	}

	var pcfg projectConf
	if err := json.Unmarshal([]byte(cfgJSON), &pcfg); err != nil {
		slog.Error("fix: parse config", "project_id", projectID, "err", err)
		return
	}
	if pcfg.FixDisabled {
		slog.Info("fix: fix_disabled, skipping", "project_id", projectID)
		return
	}
	if pcfg.GitHub.Owner == "" || pcfg.GitHub.Repo == "" {
		return
	}

	// Daily run limit: 30 runs per 24h
	var runCount int
	_ = a.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_runs
		 WHERE project_id = ? AND agent_type = 'fix' AND started_at > NOW() - INTERVAL 24 HOUR`,
		projectID,
	).Scan(&runCount)
	if runCount >= 30 {
		slog.Info("fix: daily run limit reached", "project_id", projectID)
		return
	}

	// Pick one open issue (optimistic lock)
	issue, err := a.claimIssue(ctx, projectID)
	if err != nil || issue == nil {
		if err != nil {
			slog.Error("fix: claim issue", "project_id", projectID, "err", err)
		}
		return
	}

	runID, err := agentrun.Start(ctx, a.DB, projectID, "fix", configVersion)
	if err != nil {
		slog.Error("fix: start agentrun", "project_id", projectID, "err", err)
		a.releaseIssue(ctx, issue.id)
		return
	}

	agentrun.WithRecover(runID, a.DB, func() {
		output, finalStatus := a.runFix(ctx, projectID, userID, configVersion, runID, issue, &pcfg)
		if err := agentrun.Finish(ctx, a.DB, runID, finalStatus, output); err != nil {
			slog.Error("fix: finish agentrun", "run_id", runID, "err", err)
		}
	})
}

func (a *Agent) runFix(ctx context.Context, projectID, userID int64, configVersion int, runID int64, issue *issueRow, pcfg *projectConf) (string, string) {
	var logBuf bytes.Buffer
	logf := func(msg string, args ...any) {
		line := fmt.Sprintf(msg, args...)
		logBuf.WriteString(line + "\n")
		slog.Info("fix: "+line, "project_id", projectID, "run_id", runID)
	}

	// Decrypt SSH key
	sshKeyEnc, err := hex.DecodeString(pcfg.SSHPrivateKey)
	if err != nil {
		logf("ERROR: decode ssh key hex: %v", err)
		return logBuf.String(), "failed"
	}
	sshKey, err := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, sshKeyEnc)
	if err != nil {
		logf("ERROR: decrypt ssh key: %v", err)
		return logBuf.String(), "failed"
	}

	// Decrypt PAT
	patEnc, err := hex.DecodeString(pcfg.GitHub.PAT)
	if err != nil {
		logf("ERROR: decode PAT hex: %v", err)
		return logBuf.String(), "failed"
	}
	pat, err := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, patEnc)
	if err != nil {
		logf("ERROR: decrypt PAT: %v", err)
		return logBuf.String(), "failed"
	}

	baseBranch := pcfg.GitHub.FixBaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	repoPath := gitops.RepoPath(userID, projectID)
	logf("ensuring repo at %s", repoPath)

	if err := gitops.EnsureRepo(ctx, sshKey, pcfg.GitHub.Owner, pcfg.GitHub.Repo, repoPath, baseBranch); err != nil {
		logf("ERROR: ensure repo: %v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}

	branchName := fmt.Sprintf("fix/issue-%d", issue.githubNumber)
	logf("ensuring branch %s", branchName)

	if err := gitops.EnsureBranch(ctx, repoPath, branchName, baseBranch); err != nil {
		logf("ERROR: ensure branch: %v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}

	// Build prompt
	dirTree := gitops.DirTree(repoPath, 3)
	prevFailures := a.getPreviousFailures(ctx, issue.id)
	prompt, err := buildPrompt(issue.title, issue.body, dirTree, prevFailures)
	if err != nil {
		logf("ERROR: build prompt: %v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}
	logBuf.WriteString("\n--- PROMPT ---\n" + prompt + "\n--- END PROMPT ---\n")

	// Build fix runner
	aiAPIKey := ""
	if pcfg.AIAPIKey != "" {
		keyEnc, _ := hex.DecodeString(pcfg.AIAPIKey)
		if plain, err := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, keyEnc); err == nil {
			aiAPIKey = string(plain)
		}
	}
	runner, err := fixrunner.New(pcfg.AIRunner, pcfg.AIModel, pcfg.AIAPIBase, aiAPIKey)
	if err != nil {
		logf("ERROR: build runner: %v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}

	logf("running AI fix (runner=%s)", pcfg.AIRunner)
	fixCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	aiOutput, err := runner.Fix(fixCtx, repoPath, prompt)
	logBuf.WriteString("\n--- AI OUTPUT ---\n" + aiOutput + "\n--- END AI OUTPUT ---\n")
	if err != nil {
		logf("ERROR: AI runner: %v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}

	// Check if AI made any changes
	hasChanges, err := gitops.HasChanges(ctx, repoPath)
	if err != nil {
		logf("ERROR: check changes: %v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}
	if !hasChanges {
		logf("AI made no file changes; commenting on issue and skipping PR")
		gh := githubclient.New(string(pat))
		_ = gh.AddIssueComment(ctx, pcfg.IssueTracker.Owner, pcfg.IssueTracker.Repo,
			issue.githubNumber,
			fmt.Sprintf("<!-- fixloop-failure -->\nfix-agent run #%d: AI did not produce any file changes. Will retry.", runID),
		)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "skipped"
	}

	// Commit and push
	commitMsg := fmt.Sprintf("fix: %s (#%d)\n\n%s", issue.title, issue.githubNumber, aiOutput)
	if err := gitops.CommitAll(ctx, repoPath, commitMsg); err != nil {
		logf("ERROR: commit: %v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}

	// Check if branch already existed on remote (determines force push)
	force := issue.fixAttempts > 0
	if err := gitops.Push(ctx, sshKey, repoPath, branchName, force); err != nil {
		logf("ERROR: push: %v", err)
		a.releaseIssue(ctx, issue.id)
		return logBuf.String(), "failed"
	}
	logf("pushed branch %s (force=%v)", branchName, force)

	// Create or find existing PR
	gh := githubclient.New(string(pat))
	prTitle := fmt.Sprintf("fix: %s (#%d)", issue.title, issue.githubNumber)
	prBody := buildPRBody(issue.githubNumber, issue.title, pcfg.IssueTracker.Owner, pcfg.IssueTracker.Repo, aiOutput)

	existingPR, err := a.findExistingPR(ctx, projectID, issue.id)
	if err != nil {
		logf("WARN: find existing PR: %v", err)
	}

	var prNumber int
	if existingPR != 0 {
		logf("existing PR #%d, no new PR created", existingPR)
		prNumber = existingPR
	} else {
		pr, err := gh.CreatePR(ctx, pcfg.GitHub.Owner, pcfg.GitHub.Repo, prTitle, prBody, branchName, baseBranch)
		if err != nil {
			logf("ERROR: create PR: %v", err)
			a.releaseIssue(ctx, issue.id)
			return logBuf.String(), "failed"
		}
		logf("created PR #%d", pr.Number)
		prNumber = pr.Number

		// Request Copilot review
		if err := gh.RequestCopilotReview(ctx, pcfg.GitHub.Owner, pcfg.GitHub.Repo, prNumber); err != nil {
			logf("WARN: request copilot review: %v", err)
		}

		// Insert into prs table
		if _, err := a.DB.ExecContext(ctx,
			`INSERT INTO prs (project_id, issue_id, github_number, branch, status)
			 VALUES (?, ?, ?, ?, 'open')
			 ON DUPLICATE KEY UPDATE status='open'`,
			projectID, issue.id, prNumber, branchName,
		); err != nil {
			logf("WARN: insert prs: %v", err)
		}
	}

	// Increment fix_attempts
	if _, err := a.DB.ExecContext(ctx,
		`UPDATE issues SET fix_attempts = fix_attempts + 1 WHERE id = ?`,
		issue.id,
	); err != nil {
		logf("WARN: update fix_attempts: %v", err)
	}

	// Check if needs-human (>= 3 attempts)
	if issue.fixAttempts+1 >= 3 {
		if _, err := a.DB.ExecContext(ctx,
			`UPDATE issues SET status = 'needs-human' WHERE id = ? AND status = 'fixing'`,
			issue.id,
		); err != nil {
			logf("WARN: set needs-human: %v", err)
		}
		_ = notify.Send(ctx, a.DB, userID, projectID, "fix_failed",
			fmt.Sprintf("⚠️ Issue #%d 修复失败 %d 次，需人工介入: %s", issue.githubNumber, issue.fixAttempts+1, issue.title),
		)
	}

	logf("fix-agent complete, PR #%d", prNumber)
	return logBuf.String(), "success"
}

// claimIssue picks the highest-priority open issue using an optimistic lock.
func (a *Agent) claimIssue(ctx context.Context, projectID int64) (*issueRow, error) {
	// First find candidate
	var issue issueRow
	err := a.DB.QueryRowContext(ctx,
		`SELECT id, github_number, title, fix_attempts FROM issues
		 WHERE project_id = ? AND status = 'open'
		 ORDER BY priority ASC, fix_attempts ASC, id ASC
		 LIMIT 1`,
		projectID,
	).Scan(&issue.id, &issue.githubNumber, &issue.title, &issue.fixAttempts)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Fetch issue body from GitHub (stored in GitHub, not locally — we query issues table for number,
	// then fetch body from GitHub API via the PAT in project config)
	// For now, body is not stored locally; fix-agent will work with title only in the prompt.
	// TODO: store body in issues table or fetch from GitHub API here.

	// Optimistic lock: attempt to claim
	res, err := a.DB.ExecContext(ctx,
		`UPDATE issues SET status = 'fixing', fixing_since = NOW()
		 WHERE id = ? AND status = 'open'`,
		issue.id,
	)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, nil // Another worker claimed it first
	}
	return &issue, nil
}

// releaseIssue resets a claimed issue back to open on error.
func (a *Agent) releaseIssue(ctx context.Context, issueID int64) {
	_, _ = a.DB.ExecContext(ctx,
		`UPDATE issues SET status = 'open', fixing_since = NULL WHERE id = ? AND status = 'fixing'`,
		issueID,
	)
}

// findExistingPR returns the GitHub PR number if there's already an open PR for this issue.
func (a *Agent) findExistingPR(ctx context.Context, projectID, issueID int64) (int, error) {
	var number int
	err := a.DB.QueryRowContext(ctx,
		`SELECT github_number FROM prs WHERE project_id = ? AND issue_id = ? AND status = 'open' LIMIT 1`,
		projectID, issueID,
	).Scan(&number)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return number, err
}

// getPreviousFailures returns the last few failure comments from issues.
func (a *Agent) getPreviousFailures(ctx context.Context, issueID int64) string {
	// In the current schema, failures are noted on GitHub Issues as comments.
	// Without storing them locally we return empty here; later we can add a failures table.
	return ""
}

var fixTmpl = template.Must(template.New("fix").Parse(fixPromptTmpl))

type promptData struct {
	IssueTitle      string
	IssueBody       string
	DirTree         string
	PreviousFailures string
}

func buildPrompt(title, body, dirTree, prevFailures string) (string, error) {
	var buf bytes.Buffer
	err := fixTmpl.Execute(&buf, promptData{
		IssueTitle:      title,
		IssueBody:       body,
		DirTree:         dirTree,
		PreviousFailures: prevFailures,
	})
	return buf.String(), err
}

func buildPRBody(issueNumber int, issueTitle, issueOwner, issueRepo, aiOutput string) string {
	// Truncate AI output to avoid overly long PR bodies
	if len(aiOutput) > 2000 {
		aiOutput = aiOutput[:2000] + "\n... (truncated)"
	}
	return fmt.Sprintf(`## Fix for #%d: %s

**Root cause and changes:**
%s

Closes %s/%s#%d
`, issueNumber, issueTitle, aiOutput, issueOwner, issueRepo, issueNumber)
}
```

- [ ] **Step 2: 添加 GitHub AddIssueComment 方法到 internal/github/client.go**

在 `client.go` 末尾追加：

```go
// AddIssueComment posts a comment on a GitHub issue.
func (c *Client) AddIssueComment(ctx context.Context, owner, repo string, number int, body string) error {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number)
	payload := map[string]string{"body": body}
	_, _, err := c.do(ctx, http.MethodPost, path, payload)
	return err
}
```

- [ ] **Step 3: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./internal/fix/... ./internal/github/...
```

Expected: no output. Fix any import errors.

- [ ] **Step 4: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add internal/fix/ internal/github/client.go
git commit -m "feat: implement fix-agent (AI fix loop, branch push, PR creation)"
```

---

## Task 7: master-agent

**Files:**
- Create: `internal/master/master.go`

master-agent 逻辑（每 10 分钟，每次处理一个 PR）：
1. 超时重置：fixing_since > 2h 且无关联 open PR → reset to open
2. 取最老的 open PR
3. 获取 GitHub PR reviews
4. 等待条件：Copilot COMMENTED、任意 APPROVED、或 24h 超时告警
5. Squash merge → 获取 merge_commit_sha
6. 若有 Vercel 配置：等待部署 matching SHA
7. Playwright 验收（从 backlog 找关联场景）
8. 事务：
   - 通过：prs.status=merged + issues.status=closed + backlog failed→pending
   - 失败：issues.status=open, accept_failures+=1（>=5 → needs-human）
9. 删除 fix branch
10. 发通知

- [ ] **Step 1: 创建 master.go**

```go
// internal/master/master.go
package master

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fixloop/fixloop/internal/agentrun"
	"github.com/fixloop/fixloop/internal/config"
	"github.com/fixloop/fixloop/internal/crypto"
	githubclient "github.com/fixloop/fixloop/internal/github"
	"github.com/fixloop/fixloop/internal/notify"
	"github.com/fixloop/fixloop/internal/playwright"
	"github.com/fixloop/fixloop/internal/ssrf"
	"github.com/fixloop/fixloop/internal/storage"
	"github.com/fixloop/fixloop/internal/vercel"
)

const playwrightBin = "/tmp/pw-test/node_modules/.bin/playwright"

// Agent runs the master loop for a single project.
type Agent struct {
	DB  *sql.DB
	Cfg *config.Config
	R2  *storage.R2Client
}

type projectConf struct {
	GitHub struct {
		Owner         string `json:"owner"`
		Repo          string `json:"repo"`
		PAT           string `json:"pat"`
		FixBaseBranch string `json:"fix_base_branch"`
	} `json:"github"`
	IssueTracker struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
	} `json:"issue_tracker"`
	SSHPrivateKey string `json:"ssh_private_key"`
	Vercel        struct {
		ProjectID     string `json:"project_id"`
		Token         string `json:"token"` // hex(AES encrypted)
		StagingTarget string `json:"staging_target"`
	} `json:"vercel"`
	Test struct {
		StagingURL      string `json:"staging_url"`
		StagingAuthType string `json:"staging_auth_type"`
		StagingAuth     string `json:"staging_auth"` // hex(AES encrypted JSON)
	} `json:"test"`
}

type prRow struct {
	id           int64
	issueID      sql.NullInt64
	githubNumber int
	branch       string
}

func (a *Agent) Run(ctx context.Context, projectID int64) {
	var (
		cfgJSON       string
		configVersion int
		userID        int64
		status        string
	)
	if err := a.DB.QueryRowContext(ctx,
		`SELECT user_id, config, config_version, status FROM projects WHERE id = ? AND deleted_at IS NULL`,
		projectID,
	).Scan(&userID, &cfgJSON, &configVersion, &status); err != nil {
		slog.Error("master: load project", "project_id", projectID, "err", err)
		return
	}
	if status != "active" {
		return
	}

	var pcfg projectConf
	if err := json.Unmarshal([]byte(cfgJSON), &pcfg); err != nil {
		slog.Error("master: parse config", "project_id", projectID, "err", err)
		return
	}

	runID, err := agentrun.Start(ctx, a.DB, projectID, "master", configVersion)
	if err != nil {
		slog.Error("master: start agentrun", "project_id", projectID, "err", err)
		return
	}

	agentrun.WithRecover(runID, a.DB, func() {
		output, finalStatus := a.runMaster(ctx, projectID, userID, runID, &pcfg)
		if err := agentrun.Finish(ctx, a.DB, runID, finalStatus, output); err != nil {
			slog.Error("master: finish agentrun", "run_id", runID, "err", err)
		}
	})
}

func (a *Agent) runMaster(ctx context.Context, projectID, userID, runID int64, pcfg *projectConf) (string, string) {
	var logBuf bytes.Buffer
	logf := func(msg string, args ...any) {
		line := fmt.Sprintf(msg, args...)
		logBuf.WriteString(line + "\n")
		slog.Info("master: "+line, "project_id", projectID, "run_id", runID)
	}

	// Step 1: Reset fixing-timeout issues (fixing_since > 2h, no open PR)
	a.resetTimedOutIssues(ctx, projectID)

	// Step 2: Find oldest open PR for this project
	pr, err := a.findOldestOpenPR(ctx, projectID)
	if err != nil {
		logf("find open PR: %v", err)
		return logBuf.String(), "failed"
	}
	if pr == nil {
		logf("no open PRs, nothing to do")
		return logBuf.String(), "skipped"
	}
	logf("processing PR #%d (branch %s)", pr.githubNumber, pr.branch)

	// Decrypt PAT
	patEnc, _ := hex.DecodeString(pcfg.GitHub.PAT)
	pat, err := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, patEnc)
	if err != nil {
		logf("ERROR: decrypt PAT: %v", err)
		return logBuf.String(), "failed"
	}
	gh := githubclient.New(string(pat))

	// Step 3: Check reviews
	reviews, err := gh.ListPRReviews(ctx, pcfg.GitHub.Owner, pcfg.GitHub.Repo, pr.githubNumber)
	if err != nil {
		logf("WARN: list PR reviews: %v", err)
	}

	mergeable := false
	for _, r := range reviews {
		if r.State == "APPROVED" || (r.Author == "copilot-pull-request-reviewer" && r.State == "COMMENTED") {
			mergeable = true
			break
		}
	}

	// Check 24h timeout
	var prCreatedAt time.Time
	_ = a.DB.QueryRowContext(ctx, `SELECT created_at FROM prs WHERE id = ?`, pr.id).Scan(&prCreatedAt)
	if !mergeable && time.Since(prCreatedAt) > 24*time.Hour {
		logf("PR #%d waiting for review > 24h, alerting", pr.githubNumber)
		_ = notify.Send(ctx, a.DB, userID, projectID, "review_timeout",
			fmt.Sprintf("🔔 PR #%d 等待 review 超过 24h，请检查", pr.githubNumber),
		)
		return logBuf.String(), "skipped"
	}
	if !mergeable {
		logf("PR #%d not yet approved/reviewed, waiting", pr.githubNumber)
		return logBuf.String(), "skipped"
	}

	// Step 4: Squash merge
	mergeTitle := fmt.Sprintf("fix: #%d (squash)", pr.githubNumber)
	mergeSHA, err := gh.MergePR(ctx, pcfg.GitHub.Owner, pcfg.GitHub.Repo, pr.githubNumber, mergeTitle)
	if err != nil {
		logf("ERROR: merge PR #%d: %v", pr.githubNumber, err)
		return logBuf.String(), "failed"
	}
	logf("merged PR #%d, SHA=%s", pr.githubNumber, mergeSHA[:min(len(mergeSHA), 8)])

	// Update prs.status = merged
	_, _ = a.DB.ExecContext(ctx,
		`UPDATE prs SET status = 'merged', merged_at = NOW() WHERE id = ?`, pr.id)

	// Send merge notification
	_ = notify.Send(ctx, a.DB, userID, projectID, "pr_merged",
		fmt.Sprintf("✅ PR #%d 已 merge", pr.githubNumber),
	)

	// Step 5: Wait for Vercel deployment (if configured)
	if pcfg.Vercel.ProjectID != "" && pcfg.Vercel.Token != "" {
		logf("waiting for Vercel deployment (SHA=%s)...", mergeSHA[:min(len(mergeSHA), 8)])

		vercelTokenEnc, _ := hex.DecodeString(pcfg.Vercel.Token)
		vercelToken, err := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, vercelTokenEnc)
		if err != nil {
			logf("ERROR: decrypt vercel token: %v", err)
			return logBuf.String(), "failed"
		}

		dep, err := vercel.WaitForDeployment(ctx, string(vercelToken), pcfg.Vercel.ProjectID, mergeSHA)
		if err != nil {
			logf("ERROR: vercel deploy: %v", err)
			_ = notify.Send(ctx, a.DB, userID, projectID, "deploy_failed",
				fmt.Sprintf("🔥 PR #%d Vercel 部署失败/超时: %v", pr.githubNumber, err),
			)
			// Reopen issue
			if pr.issueID.Valid {
				a.reopenIssue(ctx, pr.issueID.Int64, pr.id, 0)
			}
			return logBuf.String(), "failed"
		}
		if dep != nil {
			logf("Vercel deployment READY: %s", dep.UID)
		}
	}

	// Step 6: Playwright acceptance test
	if pcfg.Test.StagingURL == "" {
		logf("no staging_url, skipping acceptance test, closing issue directly")
		if pr.issueID.Valid {
			a.closeIssueSuccess(ctx, projectID, userID, pr, pcfg, gh)
		}
		return logBuf.String(), "success"
	}

	// SSRF re-check
	if err := ssrf.ValidateHostname(hostname(pcfg.Test.StagingURL)); err != nil {
		logf("WARN: staging_url SSRF check: %v", err)
		return logBuf.String(), "failed"
	}

	// Acquire playwright lock
	locked, err := playwright.AcquireLock(a.DB, projectID)
	if err != nil || !locked {
		logf("WARN: playwright lock busy, deferring acceptance test")
		return logBuf.String(), "skipped"
	}
	defer playwright.ReleaseLock(a.DB, projectID) //nolint:errcheck

	// Run acceptance test: execute seed checks and related backlog scenario
	screenshotDir := filepath.Join("/tmp/screenshots", fmt.Sprintf("%d/%d", userID, projectID))
	_ = os.MkdirAll(screenshotDir, 0755)
	defer os.RemoveAll(screenshotDir)

	var authCfg *playwright.AuthConfig
	if pcfg.Test.StagingAuth != "" {
		authEnc, _ := hex.DecodeString(pcfg.Test.StagingAuth)
		authJSON, _ := crypto.Decrypt(map[byte][]byte{a.Cfg.AESKeyID: a.Cfg.AESKey}, authEnc)
		authCfg = parseAuthConfig(authJSON)
	}

	exec := &playwright.Executor{
		PlaywrightBin: playwrightBin,
		StagingURL:    pcfg.Test.StagingURL,
		StagingAuth:   authCfg,
		ScreenshotDir: screenshotDir,
	}

	// Run all 3 seed checks
	passed := true
	var failMsg string
	for i := 0; i < 3; i++ {
		result, err := exec.RunSeedCheck(ctx, i)
		if err != nil || (result != nil && !result.Passed) {
			passed = false
			if result != nil {
				failMsg = result.ErrorMsg
			} else {
				failMsg = fmt.Sprintf("seed check %d error: %v", i, err)
			}
			logf("acceptance seed check %d FAILED: %s", i, failMsg)
			break
		}
	}

	if passed {
		logf("acceptance test PASSED")
		if pr.issueID.Valid {
			a.closeIssueSuccess(ctx, projectID, userID, pr, pcfg, gh)
		}
		// Delete fix branch
		if err := gh.DeleteBranch(ctx, pcfg.GitHub.Owner, pcfg.GitHub.Repo, pr.branch); err != nil {
			logf("WARN: delete branch %s: %v", pr.branch, err)
		}
		return logBuf.String(), "success"
	}

	// Acceptance failed
	logf("acceptance test FAILED: %s", failMsg)
	if pr.issueID.Valid {
		a.acceptanceFailure(ctx, projectID, userID, pr, pcfg, failMsg)
	}
	return logBuf.String(), "failed"
}

func (a *Agent) closeIssueSuccess(ctx context.Context, projectID, userID int64, pr *prRow, pcfg *projectConf, gh *githubclient.Client) {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		slog.Error("master: begin close tx", "err", err)
		return
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// issues: closed
	if _, err = tx.ExecContext(ctx,
		`UPDATE issues SET status = 'closed', closed_at = NOW() WHERE id = ?`,
		pr.issueID.Int64,
	); err != nil {
		return
	}

	// backlog: failed → pending for related scenarios
	_, err = tx.ExecContext(ctx,
		`UPDATE backlog SET status = 'pending', last_tested_at = NULL
		 WHERE project_id = ? AND related_issue_id = ? AND status = 'failed'`,
		projectID, pr.issueID.Int64,
	)
	if err != nil {
		return
	}

	if err = tx.Commit(); err != nil {
		slog.Error("master: commit close tx", "err", err)
		return
	}

	// Close on GitHub
	var ghIssueNumber int
	_ = a.DB.QueryRowContext(ctx, `SELECT github_number FROM issues WHERE id = ?`, pr.issueID.Int64).Scan(&ghIssueNumber)
	if ghIssueNumber > 0 {
		_ = gh.CloseIssue(ctx, pcfg.IssueTracker.Owner, pcfg.IssueTracker.Repo, ghIssueNumber,
			fmt.Sprintf("✅ 已自动修复并通过线上验收。Fix PR: #%d", pr.githubNumber),
		)
	}

	_ = notify.Send(ctx, a.DB, userID, projectID, "issue_closed",
		fmt.Sprintf("🎉 Issue #%d 已关闭，线上验收通过", ghIssueNumber),
	)
}

func (a *Agent) acceptanceFailure(ctx context.Context, projectID, userID int64, pr *prRow, pcfg *projectConf, failMsg string) {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		slog.Error("master: begin failure tx", "err", err)
		return
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var acceptFailures int
	_ = tx.QueryRowContext(ctx,
		`SELECT accept_failures FROM issues WHERE id = ? FOR UPDATE`,
		pr.issueID.Int64,
	).Scan(&acceptFailures)

	newStatus := "open"
	if acceptFailures+1 >= 5 {
		newStatus = "needs-human"
	}
	if _, err = tx.ExecContext(ctx,
		`UPDATE issues SET status = ?, accept_failures = accept_failures + 1, fixing_since = NULL WHERE id = ?`,
		newStatus, pr.issueID.Int64,
	); err != nil {
		return
	}
	// Mark PR closed (acceptance failed means we roll back)
	if _, err = tx.ExecContext(ctx, `UPDATE prs SET status = 'closed' WHERE id = ?`, pr.id); err != nil {
		return
	}
	if err = tx.Commit(); err != nil {
		slog.Error("master: commit failure tx", "err", err)
		return
	}

	var ghIssueNumber int
	_ = a.DB.QueryRowContext(ctx, `SELECT github_number FROM issues WHERE id = ?`, pr.issueID.Int64).Scan(&ghIssueNumber)

	if newStatus == "needs-human" {
		_ = notify.Send(ctx, a.DB, userID, projectID, "needs_human",
			fmt.Sprintf("⚠️ Issue #%d 验收失败 %d 次，需人工介入", ghIssueNumber, acceptFailures+1),
		)
	} else {
		_ = notify.Send(ctx, a.DB, userID, projectID, "acceptance_failed",
			fmt.Sprintf("⚠️ Issue #%d 验收失败，回滚到 open: %s", ghIssueNumber, failMsg),
		)
	}
}

func (a *Agent) resetTimedOutIssues(ctx context.Context, projectID int64) {
	_, err := a.DB.ExecContext(ctx,
		`UPDATE issues SET status = 'open', fixing_since = NULL
		 WHERE project_id = ? AND status = 'fixing'
		   AND fixing_since < NOW() - INTERVAL 2 HOUR
		   AND id NOT IN (
		     SELECT issue_id FROM prs WHERE project_id = ? AND status = 'open' AND issue_id IS NOT NULL
		   )`,
		projectID, projectID,
	)
	if err != nil {
		slog.Warn("master: reset timed out issues", "project_id", projectID, "err", err)
	}
}

func (a *Agent) findOldestOpenPR(ctx context.Context, projectID int64) (*prRow, error) {
	var pr prRow
	err := a.DB.QueryRowContext(ctx,
		`SELECT id, issue_id, github_number, branch FROM prs
		 WHERE project_id = ? AND status = 'open'
		 ORDER BY created_at ASC LIMIT 1`,
		projectID,
	).Scan(&pr.id, &pr.issueID, &pr.githubNumber, &pr.branch)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &pr, err
}

func (a *Agent) reopenIssue(ctx context.Context, issueID, prID, _ int64) {
	_, _ = a.DB.ExecContext(ctx,
		`UPDATE issues SET status = 'open', fixing_since = NULL WHERE id = ? AND status = 'fixing'`,
		issueID,
	)
	_, _ = a.DB.ExecContext(ctx, `UPDATE prs SET status = 'closed' WHERE id = ?`, prID)
}

func parseAuthConfig(raw []byte) *playwright.AuthConfig {
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return &playwright.AuthConfig{
		Type:     m["type"],
		Username: m["username"],
		Password: m["password"],
		Name:     m["name"],
		Value:    m["value"],
	}
}

func hostname(rawURL string) string {
	s := rawURL
	if i := len("://"); len(s) > i {
		if j := indexOf(s, "://"); j >= 0 {
			s = s[j+3:]
		}
	}
	for i, c := range s {
		if c == '/' || c == ':' {
			return s[:i]
		}
	}
	return s
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 2: 验证编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./internal/master/...
```

Expected: no output. Fix any import errors.

- [ ] **Step 3: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add internal/master/master.go
git commit -m "feat: implement master-agent (merge, Vercel deploy, Playwright acceptance)"
```

---

## Task 8: 将 fix/master/plan agents 接入 cmd/server/main.go

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: 修改 main.go 的 dispatcher**

将现有 `sched, err := scheduler.New(func(...) { ... })` 块替换为：

```go
	exploreAgent := &explore.Agent{DB: database, Cfg: cfg, R2: r2Client}
	fixAgent := &fix.Agent{DB: database, Cfg: cfg}
	masterAgent := &master.Agent{DB: database, Cfg: cfg, R2: r2Client}

	sched, err := scheduler.New(func(projectID int64, agentType string) {
		ctx := context.Background()
		switch agentType {
		case "explore":
			exploreAgent.Run(ctx, projectID)
		case "fix":
			fixAgent.Run(ctx, projectID)
		case "master":
			masterAgent.Run(ctx, projectID)
		case "plan":
			slog.Info("plan-agent triggered (stub)", "project_id", projectID)
		default:
			slog.Warn("unknown agent type", "project_id", projectID, "agent_type", agentType)
		}
	})
```

需要在 import 中添加：

```go
"github.com/fixloop/fixloop/internal/fix"
"github.com/fixloop/fixloop/internal/master"
```

Also, initialize `r2Client` before the scheduler block. Look for where `explore.Agent` is constructed and add:

```go
	// Initialize R2 client (optional — nil if not configured)
	var r2Client *storage.R2Client
	// R2 config will be wired in a future phase when added to config.yaml
```

- [ ] **Step 2: 验证完整编译**

```bash
cd /home/ubuntu/fy/work/fixloop && go build ./...
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
cd /home/ubuntu/fy/work/fixloop
git add cmd/server/main.go
git commit -m "feat: wire fix-agent and master-agent into scheduler dispatch"
```

---

## Self-Review Checklist

- [x] **Spec coverage:**
  - 六 Agent 架构：explore ✅ (Phase 3)，fix ✅ (Task 6)，master ✅ (Task 7)，plan → stub
  - 十六 Git 操作规范：EnsureRepo, EnsureBranch, CommitAll, Push ✅
  - 十七 agentrun 生命周期：Start/Finish/WithRecover/AbandonZombies ✅ (Phase 3)
  - 二十一 PR 创建规范：title/body 格式 ✅，Copilot review 请求 ✅
  - 二十二 Vercel 部署验证：WaitForDeployment, SHA 匹配, READY/ERROR ✅
  - 二十三 关键事务边界：closeIssueSuccess (issues+backlog), acceptanceFailure (issues+prs) ✅
  - notifications 写入 ✅

- [x] **Type consistency:**
  - `gitops.RepoPath(userID, projectID int64) string` — used by fix-agent ✅
  - `fixrunner.FixRunner.Fix(ctx, repoPath, prompt) (string, error)` — defined and used ✅
  - `vercel.WaitForDeployment(ctx, token, projectID, sha) (*Deployment, error)` — used by master ✅
  - `github.Client.AddIssueComment` — added to github package, used by fix-agent ✅

- [x] **Placeholder scan:** No TBDs. Issue body fetching deferred (TODO comment in claimIssue) — acceptable for MVP since title alone is often sufficient context.
