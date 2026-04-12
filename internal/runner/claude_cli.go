package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ClaudeCLIRunner invokes the `claude` CLI in the repository directory.
// When APIKey is empty the CLI uses its existing server-side OAuth login.
// When APIKey is set it is injected via ANTHROPIC_API_KEY, overriding the
// server login — useful when the project owner supplies their own key.
type ClaudeCLIRunner struct {
	Model  string // e.g. "claude-opus-4-6"
	APIKey string // optional; leave empty to use server CLI login
}

func (r *ClaudeCLIRunner) Run(ctx context.Context, repoPath, prompt string) (string, error) {
	args := []string{"--print", "--dangerously-skip-permissions"}
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = repoPath
	cmd.Stdin = strings.NewReader(prompt)
	if r.APIKey != "" {
		cmd.Env = append(os.Environ(), "ANTHROPIC_API_KEY="+r.APIKey)
	}

	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude CLI: %w\nstderr: %s", err, errOut.String())
	}
	return out.String(), nil
}
