package runner

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

func (r *AiderRunner) Run(ctx context.Context, repoPath, prompt string) (string, error) {
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
