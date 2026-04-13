# Gemini CLI Runner Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `gemini` as a third AI runner option backed by the globally-installed `@google/gemini-cli` binary.

**Architecture:** New `GeminiCLIRunner` struct in `internal/runner/` implementing the existing `Runner` interface; registered in the factory; frontend settings page gains a Gemini option in the runner dropdown and a conditional API key field.

**Tech Stack:** Go `os/exec`, `@google/gemini-cli` npm package (global install), Next.js/React settings page.

---

## File Structure

| File | Action | Purpose |
|------|--------|---------|
| `internal/runner/gemini_cli.go` | Create | `GeminiCLIRunner` struct + `Run` implementation |
| `internal/runner/runner.go` | Modify (line 20–27) | Add `case "gemini"` to factory switch |
| `frontend/src/app/projects/[id]/settings/page.tsx` | Modify (lines 543–578) | Gemini dropdown option + model hint + API key field |

---

## Task 1: GeminiCLIRunner — write test first

**Files:**
- Create: `internal/runner/gemini_cli_test.go`

- [ ] **Step 1: Create test file**

```go
package runner

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// TestGeminiCLIRunner_SkipIfNoBinary skips when the real gemini binary is absent
// (CI / dev machines without it). When it IS present, verifies the runner
// delegates correctly by using a minimal prompt that requires no file edits.
func TestGeminiCLIRunner_SkipIfNoBinary(t *testing.T) {
	if _, err := exec.LookPath("gemini"); err != nil {
		t.Skip("gemini binary not found, skipping integration test")
	}
	if os.Getenv("GEMINI_API_KEY") == "" {
		t.Skip("GEMINI_API_KEY not set, skipping integration test")
	}

	r := &GeminiCLIRunner{Model: "gemini-2.0-flash"}
	out, err := r.Run(context.Background(), t.TempDir(), "Reply with exactly: hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty output")
	}
}

// TestGeminiCLIRunner_CommandArgs verifies the struct fields are stored correctly.
func TestGeminiCLIRunner_CommandArgs(t *testing.T) {
	r := &GeminiCLIRunner{Model: "gemini-2.5-pro", APIKey: "test-key"}
	if r.Model != "gemini-2.5-pro" {
		t.Errorf("Model = %q, want %q", r.Model, "gemini-2.5-pro")
	}
	if r.APIKey != "test-key" {
		t.Errorf("APIKey = %q, want %q", r.APIKey, "test-key")
	}
}
```

- [ ] **Step 2: Run test — verify it fails (type undefined)**

```bash
cd /home/ubuntu/fy/work/fixloop
go test ./internal/runner/... 2>&1
```

Expected: `undefined: GeminiCLIRunner`

---

## Task 2: GeminiCLIRunner — implement

**Files:**
- Create: `internal/runner/gemini_cli.go`

- [ ] **Step 1: Create the file**

```go
package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// GeminiCLIRunner invokes the globally-installed `gemini` CLI in the repository directory.
// When APIKey is empty the CLI uses the server's GEMINI_API_KEY environment variable.
// When APIKey is set it is injected via GEMINI_API_KEY, overriding the server value.
type GeminiCLIRunner struct {
	Model  string // e.g. "gemini-2.5-pro"; empty = CLI default
	APIKey string // optional; leave empty to use server GEMINI_API_KEY env var
}

func (r *GeminiCLIRunner) Run(ctx context.Context, repoPath, prompt string) (string, error) {
	// --yolo            auto-approve all tool calls (no interactive prompts)
	// --output-format   text  clean text output only (no JSON envelope)
	// --prompt ""       triggers non-interactive/headless mode;
	//                   actual prompt is supplied via stdin and appended automatically
	args := []string{"--yolo", "--output-format", "text", "--prompt", ""}
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}

	cmd := exec.CommandContext(ctx, "gemini", args...)
	cmd.Dir = repoPath
	cmd.Stdin = strings.NewReader(prompt)
	if r.APIKey != "" {
		cmd.Env = append(os.Environ(), "GEMINI_API_KEY="+r.APIKey)
	}

	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gemini CLI: %w\nstderr: %s", err, errOut.String())
	}
	return out.String(), nil
}
```

- [ ] **Step 2: Run tests — verify both pass**

```bash
cd /home/ubuntu/fy/work/fixloop
go test ./internal/runner/... 2>&1
```

Expected: `ok  github.com/fixloop/fixloop/internal/runner`
(The integration test is skipped if binary/key absent; the struct test always passes.)

- [ ] **Step 3: Verify full build still passes**

```bash
cd /home/ubuntu/fy/work/fixloop
go build ./... 2>&1
```

Expected: no output (clean build).

- [ ] **Step 4: Commit**

```bash
git add internal/runner/gemini_cli.go internal/runner/gemini_cli_test.go
git commit -m "feat: add GeminiCLIRunner"
```

---

## Task 3: Register Gemini in runner factory

**Files:**
- Modify: `internal/runner/runner.go`

Current `New()` function (lines 19–28):

```go
func New(aiRunner, model, apiBase, apiKey string) (Runner, error) {
	switch aiRunner {
	case "claude", "":
		return &ClaudeCLIRunner{Model: model, APIKey: apiKey}, nil
	case "aider":
		return &AiderRunner{Model: model, APIBase: apiBase, APIKey: apiKey}, nil
	default:
		return nil, fmt.Errorf("runner: unknown ai_runner %q", aiRunner)
	}
}
```

- [ ] **Step 1: Add `case "gemini"` to the switch**

Replace the `switch` block with:

```go
func New(aiRunner, model, apiBase, apiKey string) (Runner, error) {
	switch aiRunner {
	case "claude", "":
		return &ClaudeCLIRunner{Model: model, APIKey: apiKey}, nil
	case "aider":
		return &AiderRunner{Model: model, APIBase: apiBase, APIKey: apiKey}, nil
	case "gemini":
		return &GeminiCLIRunner{Model: model, APIKey: apiKey}, nil
	default:
		return nil, fmt.Errorf("runner: unknown ai_runner %q", aiRunner)
	}
}
```

(`apiBase` is intentionally unused for Gemini — the CLI has no equivalent flag.)

- [ ] **Step 2: Verify build**

```bash
cd /home/ubuntu/fy/work/fixloop
go build ./... 2>&1
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/runner/runner.go
git commit -m "feat: register gemini runner in factory"
```

---

## Task 4: Frontend — Gemini option in settings UI

**Files:**
- Modify: `frontend/src/app/projects/[id]/settings/page.tsx`

The AI 引擎 section currently lives around lines 540–578. Three changes are needed.

- [ ] **Step 1: Add Gemini to the runner dropdown (around line 545)**

Find:
```tsx
                  <option value="claude">Claude（推荐）</option>
                  <option value="aider">Aider</option>
```
Replace with:
```tsx
                  <option value="claude">Claude（推荐）</option>
                  <option value="aider">Aider</option>
                  <option value="gemini">Gemini</option>
```

- [ ] **Step 2: Update model field hint and placeholder to be Gemini-aware (around lines 550–557)**

Find:
```tsx
              <Field
                label="模型"
                hint={form.ai_runner === 'claude' ? '留空使用默认 claude-opus-4-6' : '例如 deepseek-chat、gpt-4o'}
              >
                <input
                  className={inputClass}
                  value={form.ai_model}
                  onChange={set('ai_model')}
                  placeholder={form.ai_runner === 'claude' ? 'claude-opus-4-6' : 'gpt-4o'}
                />
              </Field>
```
Replace with:
```tsx
              <Field
                label="模型"
                hint={
                  form.ai_runner === 'claude' ? '留空使用默认 claude-opus-4-6' :
                  form.ai_runner === 'gemini' ? '留空使用默认 gemini-2.5-pro' :
                  '例如 deepseek-chat、gpt-4o'
                }
              >
                <input
                  className={inputClass}
                  value={form.ai_model}
                  onChange={set('ai_model')}
                  placeholder={
                    form.ai_runner === 'claude' ? 'claude-opus-4-6' :
                    form.ai_runner === 'gemini' ? 'gemini-2.5-pro' :
                    'gpt-4o'
                  }
                />
              </Field>
```

- [ ] **Step 3: Add conditional Gemini API key field (after the existing aider block, before `</Section>`)**

Find:
```tsx
            {form.ai_runner === 'aider' && (
              <>
                <Field label="API 基础地址" hint="OpenAI 兼容接口，例如 https://api.deepseek.com/v1">
                  <input className={inputClass} value={form.ai_api_base} onChange={set('ai_api_base')} placeholder="https://api.deepseek.com/v1" />
                </Field>
                <Field label="API 密钥（留空不修改）" hint="加密存储">
                  <input className={inputClass} type="password" value={form.ai_api_key} onChange={set('ai_api_key')} placeholder="••••••••" />
                </Field>
              </>
            )}
          </Section>
```
Replace with:
```tsx
            {form.ai_runner === 'aider' && (
              <>
                <Field label="API 基础地址" hint="OpenAI 兼容接口，例如 https://api.deepseek.com/v1">
                  <input className={inputClass} value={form.ai_api_base} onChange={set('ai_api_base')} placeholder="https://api.deepseek.com/v1" />
                </Field>
                <Field label="API 密钥（留空不修改）" hint="加密存储">
                  <input className={inputClass} type="password" value={form.ai_api_key} onChange={set('ai_api_key')} placeholder="••••••••" />
                </Field>
              </>
            )}
            {form.ai_runner === 'gemini' && (
              <Field
                label="Gemini API 密钥（可选）"
                hint="留空则使用服务器 GEMINI_API_KEY 环境变量。加密存储。"
              >
                <input className={inputClass} type="password" value={form.ai_api_key} onChange={set('ai_api_key')} placeholder="AIza...（留空不修改）" />
              </Field>
            )}
          </Section>
```

- [ ] **Step 4: Build frontend and check for TypeScript errors**

```bash
cd /home/ubuntu/fy/work/fixloop/frontend
npm run build 2>&1 | tail -20
```

Expected: build succeeds, route table shown, no TypeScript errors.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/app/projects/\[id\]/settings/page.tsx
git commit -m "feat: add Gemini runner option to project settings UI"
```

---

## Task 5: Install CLI, restart services, smoke test

- [ ] **Step 1: Install Gemini CLI globally**

```bash
npm install -g @google/gemini-cli
gemini --version
```

Expected: prints version like `0.37.1` (no error).

- [ ] **Step 2: Rebuild backend binary**

```bash
cd /home/ubuntu/fy/work/fixloop
go build -o /tmp/fixloop-server ./cmd/server/
```

Expected: no output (clean build).

- [ ] **Step 3: Restart backend**

```bash
kill $(lsof -ti:8080) 2>/dev/null
nohup /tmp/fixloop-server > /tmp/fixloop-backend.log 2>&1 &
sleep 2 && curl -s http://localhost:8080/health
```

Expected: `{"db":"ok","status":"ok"}`

- [ ] **Step 4: Restart frontend**

```bash
cd /home/ubuntu/fy/work/fixloop/frontend
OLD_PID=$(ss -tlnp | awk '/3100/{match($0,/pid=([0-9]+)/,a); print a[1]}')
[ -n "$OLD_PID" ] && kill -9 $OLD_PID 2>/dev/null
sudo systemctl restart fixloop-frontend
sleep 2
sudo systemctl status fixloop-frontend --no-pager | grep Active
```

Expected: `Active: active (running)`

- [ ] **Step 5: Smoke test in browser**

Open `https://dapp.predict.kim` → 任意项目 → 设置 → AI 引擎。
Verify:
- 引擎下拉有 "Gemini" 选项
- 选中 Gemini 后，模型 placeholder 变为 `gemini-2.5-pro`，hint 显示 `留空使用默认 gemini-2.5-pro`
- 出现「Gemini API 密钥（可选）」输入框
- 切换回 Claude/Aider 后，各自的字段恢复正常

- [ ] **Step 6: Commit ops note**

```bash
git add .
git commit -m "chore: install gemini CLI globally on server" --allow-empty
```

(Empty commit is fine if no tracked files changed from the npm install.)
