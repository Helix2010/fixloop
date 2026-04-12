package playwright

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// StepAction represents a single test step from the backlog.
type StepAction struct {
	Action   string `json:"action"`
	URL      string `json:"url,omitempty"`
	Selector string `json:"selector,omitempty"`
	Value    string `json:"value,omitempty"`
	Name     string `json:"name,omitempty"`
	WaitMs   int    `json:"wait_ms,omitempty"`
	Text     string `json:"text,omitempty"`
}

// Result holds the outcome of a test run.
type Result struct {
	Passed      bool
	ErrorType   string   // "timeout"|"assertion"|"console_error"|"crash"
	ErrorMsg    string
	Screenshots []string // local file paths
}

// AuthConfig describes how to authenticate against the staging URL.
type AuthConfig struct {
	Type     string // "basic"|"header"|"cookie"
	Username string // basic
	Password string // basic
	Name     string // header/cookie
	Value    string // header/cookie
}

// Executor runs Playwright tests.
type Executor struct {
	PlaywrightBin string        // path to playwright binary (e.g. /tmp/pw-test/node_modules/.bin/playwright)
	StagingURL    string
	StagingAuth   *AuthConfig // nil = no auth
	ScreenshotDir string      // local dir for screenshots
	ProfileDir    string      // --user-data-dir (unused currently)
	Timeout       time.Duration // per-scenario timeout, default 60s
}

// nodeModulesDir returns the node_modules root from PlaywrightBin.
// e.g. /tmp/pw-test/node_modules/.bin/playwright → /tmp/pw-test/node_modules
func (e *Executor) nodeModulesDir() string {
	// .bin is inside node_modules
	return filepath.Dir(filepath.Dir(e.PlaywrightBin))
}

// timeout returns the effective timeout.
func (e *Executor) timeout() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return 60 * time.Second
}

// RunSteps renders steps to a temp .spec.js, runs playwright, parses JSON output.
func (e *Executor) RunSteps(ctx context.Context, steps []StepAction) (*Result, error) {
	tmpDir, err := os.MkdirTemp("", "pw-run-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	specFile := filepath.Join(tmpDir, "scenario.spec.js")
	configFile := filepath.Join(tmpDir, "playwright.config.js")
	resultsDir := filepath.Join(tmpDir, "test-results")

	if err := os.MkdirAll(resultsDir, 0755); err != nil {
		return nil, fmt.Errorf("create results dir: %w", err)
	}

	// Write spec
	specContent, err := e.renderSpec(steps)
	if err != nil {
		return nil, fmt.Errorf("render spec: %w", err)
	}
	if err := os.WriteFile(specFile, []byte(specContent), 0644); err != nil {
		return nil, fmt.Errorf("write spec: %w", err)
	}

	// Write config
	configContent := e.renderConfig(resultsDir)
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}

	// Run playwright
	runCtx, cancel := context.WithTimeout(ctx, e.timeout())
	defer cancel()

	cmd := exec.CommandContext(runCtx, "/usr/bin/node", e.PlaywrightBin,
		"test", specFile, "--reporter=json", "--config="+configFile)

	// Set browser path env
	homeDir, _ := os.UserHomeDir()
	browsersPath := filepath.Join(homeDir, ".cache", "ms-playwright")
	cmd.Env = append(os.Environ(), "PLAYWRIGHT_BROWSERS_PATH="+browsersPath)

	stdout, err := cmd.Output()
	// err may be non-nil even on test failure (exit code 1) — that's expected.
	// Only return error on non-exec failures.
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return nil, fmt.Errorf("run playwright: %w", err)
		}
	}

	return e.parseOutput(stdout)
}

// renderConfig returns a playwright.config.js for the run.
func (e *Executor) renderConfig(outputDir string) string {
	outputDirJSON, _ := json.Marshal(outputDir)

	var sb strings.Builder
	sb.WriteString("module.exports = {\n")
	sb.WriteString("  use: {\n")
	sb.WriteString("    headless: true,\n")
	sb.WriteString("    screenshot: 'only-on-failure',\n")

	if e.StagingAuth != nil && e.StagingAuth.Type == "basic" {
		usernameJSON, _ := json.Marshal(e.StagingAuth.Username)
		passwordJSON, _ := json.Marshal(e.StagingAuth.Password)
		sb.WriteString(fmt.Sprintf("    httpCredentials: { username: %s, password: %s },\n",
			usernameJSON, passwordJSON))
	}

	sb.WriteString("  },\n")
	sb.WriteString(fmt.Sprintf("  outputDir: %s,\n", outputDirJSON))
	sb.WriteString("  reporter: 'json',\n")
	sb.WriteString("};\n")
	return sb.String()
}

// renderSpec renders test steps into a .spec.js file.
func (e *Executor) renderSpec(steps []StepAction) (string, error) {
	// requirePath: the full path to playwright/test module, emitted as a JS string literal.
	requirePath := filepath.Join(e.nodeModulesDir(), "playwright", "test")

	var sb strings.Builder
	// Use jsLit() to emit JS string literals — these are safe because json.Marshal
	// escapes all special characters.
	sb.WriteString(fmt.Sprintf("const { test, expect } = require(%s);\n", jsLit(requirePath)))
	sb.WriteString("test('scenario', async ({ page }) => {\n")
	sb.WriteString("  const consoleErrors = [];\n")
	sb.WriteString("  page.on('console', msg => { if (msg.type() === 'error') consoleErrors.push(msg.text()); });\n")
	sb.WriteString(fmt.Sprintf("  const stagingURL = %s;\n", jsLit(e.StagingURL)))

	// Auth setup (header / cookie — basic is handled in config)
	if e.StagingAuth != nil {
		auth := e.StagingAuth
		switch auth.Type {
		case "header":
			sb.WriteString(fmt.Sprintf("  await page.setExtraHTTPHeaders({ [%s]: %s });\n",
				jsLit(auth.Name), jsLit(auth.Value)))
		case "cookie":
			sb.WriteString(fmt.Sprintf(
				"  await page.context().addCookies([{ name: %s, value: %s, url: stagingURL }]);\n",
				jsLit(auth.Name), jsLit(auth.Value)))
		}
	}

	// Render steps
	for i, step := range steps {
		code, err := renderStep(step, i)
		if err != nil {
			return "", fmt.Errorf("step %d: %w", i, err)
		}
		sb.WriteString("  " + code + "\n")
	}

	sb.WriteString("});\n")
	return sb.String(), nil
}

// jsLit returns a Go string as a JavaScript string literal using json.Marshal.
// json.Marshal produces a valid JSON string (double-quoted, all special chars escaped),
// which is also a valid JavaScript string literal. This prevents injection.
func jsLit(s string) string {
	b, _ := json.Marshal(s)
	return string(b) // e.g. "\"hello\\nworld\""
}

// renderStep converts a StepAction to a JS expression (without leading spaces).
func renderStep(step StepAction, index int) (string, error) {
	switch step.Action {
	case "goto":
		return fmt.Sprintf("await page.goto(%s);", jsLit(step.URL)), nil

	case "expect_visible":
		return fmt.Sprintf("await expect(page.locator(%s)).toBeVisible();", jsLit(step.Selector)), nil

	case "expect_not_visible":
		return fmt.Sprintf("await expect(page.locator(%s)).toBeHidden();", jsLit(step.Selector)), nil

	case "click":
		return fmt.Sprintf("await page.locator(%s).click();", jsLit(step.Selector)), nil

	case "fill":
		return fmt.Sprintf("await page.locator(%s).fill(%s);", jsLit(step.Selector), jsLit(step.Value)), nil

	case "expect_text":
		return fmt.Sprintf("await expect(page.locator(%s)).toContainText(%s);",
			jsLit(step.Selector), jsLit(step.Text)), nil

	case "expect_no_console_error":
		return "if (consoleErrors.length > 0) { throw new Error('console.error: ' + consoleErrors.join('; ')); }", nil

	case "screenshot":
		return fmt.Sprintf("await page.screenshot({ path: %s });",
			jsLit(fmt.Sprintf("screenshot-%d-%s.png", index, step.Name))), nil

	case "wait_ms":
		return fmt.Sprintf("await page.waitForTimeout(%d);", step.WaitMs), nil

	default:
		return "", fmt.Errorf("unknown action: %s", step.Action)
	}
}

// --- JSON reporter output structs ---

type pwOutput struct {
	Stats  pwStats   `json:"stats"`
	Suites []pwSuite `json:"suites"`
	Errors []pwError `json:"errors"`
}

type pwStats struct {
	Unexpected int `json:"unexpected"`
	Expected   int `json:"expected"`
}

type pwSuite struct {
	Title  string    `json:"title"`
	Suites []pwSuite `json:"suites"`
	Specs  []pwSpec  `json:"specs"`
}

type pwSpec struct {
	OK    bool     `json:"ok"`
	Tests []pwTest `json:"tests"`
}

type pwTest struct {
	Status  string     `json:"status"` // "expected" | "unexpected"
	Results []pwResult `json:"results"`
}

type pwResult struct {
	Status      string         `json:"status"` // "passed" | "failed"
	Error       *pwResultError `json:"error"`
	Attachments []pwAttachment `json:"attachments"`
}

type pwResultError struct {
	Message string `json:"message"`
}

type pwAttachment struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	Path        string `json:"path"`
}

type pwError struct {
	Message string `json:"message"`
}

// parseOutput parses playwright JSON reporter output from stdout.
func (e *Executor) parseOutput(stdout []byte) (*Result, error) {
	// Find JSON start (playwright may emit ANSI to stdout in some modes)
	idx := strings.Index(string(stdout), "{")
	if idx < 0 {
		return &Result{
			Passed:    false,
			ErrorType: "crash",
			ErrorMsg:  "no JSON output from playwright",
		}, nil
	}
	raw := stdout[idx:]

	var out pwOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return &Result{
			Passed:    false,
			ErrorType: "crash",
			ErrorMsg:  fmt.Sprintf("parse playwright output: %v; raw: %s", err, truncate(string(raw), 200)),
		}, nil
	}

	// Top-level errors (load/syntax errors — no suites)
	if len(out.Errors) > 0 && len(out.Suites) == 0 {
		msg := out.Errors[0].Message
		return &Result{
			Passed:    false,
			ErrorType: classifyError(msg),
			ErrorMsg:  msg,
		}, nil
	}

	// Collect failures and screenshots
	var errMsg string
	var attachments []pwAttachment
	passed := out.Stats.Unexpected == 0

	// Walk all specs recursively
	walkSuites(out.Suites, func(r pwResult) {
		if r.Status == "failed" && r.Error != nil && errMsg == "" {
			errMsg = r.Error.Message
		}
		attachments = append(attachments, r.Attachments...)
	})

	// Copy screenshots to ScreenshotDir
	screenshots, err := e.collectScreenshots(attachments)
	if err != nil {
		// Non-fatal — log but continue
		_ = err
	}

	result := &Result{
		Passed:      passed,
		Screenshots: screenshots,
	}
	if !passed {
		result.ErrorType = classifyError(errMsg)
		result.ErrorMsg = stripANSI(errMsg)
	}
	return result, nil
}

// walkSuites recursively visits all test results.
func walkSuites(suites []pwSuite, fn func(pwResult)) {
	for _, s := range suites {
		walkSuites(s.Suites, fn)
		for _, spec := range s.Specs {
			for _, t := range spec.Tests {
				for _, r := range t.Results {
					fn(r)
				}
			}
		}
	}
}

// collectScreenshots copies PNG attachments from the temp dir to ScreenshotDir.
func (e *Executor) collectScreenshots(attachments []pwAttachment) ([]string, error) {
	if e.ScreenshotDir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(e.ScreenshotDir, 0755); err != nil {
		return nil, err
	}

	var paths []string
	for _, att := range attachments {
		if att.ContentType != "image/png" || att.Path == "" {
			continue
		}
		dst := filepath.Join(e.ScreenshotDir, filepath.Base(att.Path))
		if err := copyFile(att.Path, dst); err == nil {
			paths = append(paths, dst)
		}
	}
	return paths, nil
}

// classifyError maps an error message to an error type.
func classifyError(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "timeout"):
		return "timeout"
	case strings.Contains(msg, "console.error"):
		return "console_error"
	case strings.Contains(lower, "expect"):
		return "assertion"
	default:
		return "crash"
	}
}

// copyFile copies src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// stripANSI removes ANSI escape codes from a string.
func stripANSI(s string) string {
	// Simple removal of ESC[ sequences
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// Skip until 'm'
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- Seed checks ---

// RunSeedCheck runs one of three hardcoded seed checks.
//
//	0 = "首页可访问" → HTTP 200 + no JS crash
//	1 = "页面无 console ERROR" → no console.error
//	2 = "核心交互元素可见" → body non-empty + no 500
func (e *Executor) RunSeedCheck(ctx context.Context, checkIndex int) (*Result, error) {
	var steps []StepAction
	switch checkIndex {
	case 0:
		// 首页可访问: navigate and ensure page loaded (no crash)
		steps = []StepAction{
			{Action: "goto", URL: e.StagingURL},
			{Action: "expect_visible", Selector: "body"},
		}
	case 1:
		// 页面无 console ERROR
		steps = []StepAction{
			{Action: "goto", URL: e.StagingURL},
			{Action: "expect_no_console_error"},
		}
	case 2:
		// 核心交互元素可见: body non-empty + no 500
		steps = []StepAction{
			{Action: "goto", URL: e.StagingURL},
			{Action: "expect_visible", Selector: "body"},
			{Action: "expect_not_visible", Selector: "body:has-text('500')"},
		}
	default:
		return nil, fmt.Errorf("unknown seed check index: %d", checkIndex)
	}
	return e.RunSteps(ctx, steps)
}

// --- MySQL advisory locks ---

// AcquireLock acquires a MySQL advisory lock for the given project.
// Returns true if the lock was acquired, false if already held by another connection.
func AcquireLock(db *sql.DB, projectID int64) (bool, error) {
	lockName := fmt.Sprintf("pw_%d", projectID)
	var result int
	err := db.QueryRow("SELECT GET_LOCK(?, 0)", lockName).Scan(&result)
	if err != nil {
		return false, fmt.Errorf("GET_LOCK: %w", err)
	}
	return result == 1, nil
}

// ReleaseLock releases a MySQL advisory lock for the given project.
func ReleaseLock(db *sql.DB, projectID int64) error {
	lockName := fmt.Sprintf("pw_%d", projectID)
	_, err := db.Exec("SELECT RELEASE_LOCK(?)", lockName)
	if err != nil {
		return fmt.Errorf("RELEASE_LOCK: %w", err)
	}
	return nil
}
