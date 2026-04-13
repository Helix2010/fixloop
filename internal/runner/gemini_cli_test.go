package runner

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

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
