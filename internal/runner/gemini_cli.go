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
	// --prompt "" triggers headless mode; the actual prompt is supplied via stdin
	// and appended automatically by the CLI.
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
	s := out.String()
	if strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("gemini CLI: empty output (prompt may not have been delivered)")
	}
	return s, nil
}
