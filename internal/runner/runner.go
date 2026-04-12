package runner

import (
	"context"
	"fmt"
)

// Runner is the interface all AI backends implement.
// It is used by Fix Agent, Plan Agent, and Telegram Bot for any AI invocation.
type Runner interface {
	// Run invokes the AI in repoPath with the given prompt.
	// repoPath may be empty for tasks that don't require a local repo clone.
	// Returns the AI's final response text (for logging/storage).
	Run(ctx context.Context, repoPath, prompt string) (string, error)
}

// New returns a Runner based on the project's ai_runner config field.
// aiRunner: "claude" | "aider" (default: "claude")
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
