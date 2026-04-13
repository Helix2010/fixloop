# Gemini CLI Runner Design

> **For agentic workers:** Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan.

**Goal:** Add `gemini` as a first-class AI runner option alongside `claude` and `aider`, backed by the `@google/gemini-cli` binary installed globally on the server.

**Architecture:** Mirror the existing `ClaudeCLIRunner` pattern — a new `GeminiCLIRunner` struct in the runner package, registered in the factory, with matching frontend UI in project settings.

**Tech Stack:** Go (`os/exec`), `gemini` CLI binary (global npm install), Next.js settings page.

---

## Backend

### `internal/runner/gemini_cli.go` (new file)

Struct:

```go
type GeminiCLIRunner struct {
    Model  string // e.g. "gemini-2.5-pro"; empty = CLI default
    APIKey string // optional; empty = rely on server GEMINI_API_KEY env var
}
```

`Run` implementation:

- Args: `["--yolo", "--output-format", "text", "--prompt", ""]`
  - `--yolo`: auto-approve all tool calls (equivalent to claude's `--dangerously-skip-permissions`)
  - `--output-format text`: clean text output (equivalent to claude's `--print`)
  - `--prompt ""`: triggers non-interactive/headless mode; actual prompt comes from stdin
- If `Model != ""`: append `["--model", r.Model]`
- Prompt delivered via `cmd.Stdin = strings.NewReader(prompt)` (mirrors ClaudeCLIRunner, avoids shell arg length limits on long prompts)
- If `APIKey != ""`: `cmd.Env = append(os.Environ(), "GEMINI_API_KEY="+r.APIKey)`
- `cmd.Dir = repoPath`
- On error: return `fmt.Errorf("gemini CLI: %w\nstderr: %s", err, errOut.String())`

### `internal/runner/runner.go`

Add to the `switch` in `New()`:

```go
case "gemini":
    return &GeminiCLIRunner{Model: model, APIKey: apiKey}, nil
```

No changes to the function signature or other callers — `apiBase` is ignored for Gemini (not applicable).

---

## Frontend

### `frontend/src/app/projects/[id]/settings/page.tsx`

**Runner dropdown** — add one option:

```tsx
<option value="gemini">Gemini</option>
```

**Model field hint/placeholder** — update to be runner-aware:

| Runner | Placeholder | Hint |
|--------|------------|------|
| `claude` | `claude-opus-4-6` | 留空使用默认 claude-opus-4-6 |
| `aider` | `gpt-4o` | 例如 deepseek-chat、gpt-4o |
| `gemini` | `gemini-2.5-pro` | 留空使用默认 gemini-2.5-pro |

**Conditional API key section** — add alongside the existing `claude` and `aider` blocks:

```tsx
{form.ai_runner === 'gemini' && (
  <Field
    label="Gemini API 密钥（可选）"
    hint="留空则使用服务器 GEMINI_API_KEY 环境变量。加密存储。"
  >
    <input
      className={inputClass}
      type="password"
      value={form.ai_api_key}
      onChange={set('ai_api_key')}
      placeholder="AIza...（留空不修改）"
    />
  </Field>
)}
```

`handleRunnerChange` already clears `ai_model`, `ai_api_base`, `ai_api_key` on runner switch — no changes needed.

No new DB fields, no new API endpoints. The existing `ai_runner` / `ai_model` / `ai_api_key` fields in `projectConfig` carry Gemini config unchanged.

---

## Installation (ops, not code)

Before deploying, run once on the server:

```bash
npm install -g @google/gemini-cli
gemini --version   # verify
```

---

## Out of Scope

- Server-level Gemini API key in `config.yaml` — per-project key + env var fallback is sufficient
- `api_base` support for Gemini — not applicable to the Gemini CLI
- Vertex AI / GCA auth — `GEMINI_API_KEY` (AI Studio) only
