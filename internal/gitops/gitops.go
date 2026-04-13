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

// RepoPath returns the local clone path for a project's shared repo workspace.
// Layout: {workspaceDir}/{owner}/{repo}/repo/
func RepoPath(workspaceDir, owner, repo string) string {
	return filepath.Join(workspaceDir, owner, repo, "repo")
}

// AgentRepoPath returns the local clone path for a specific agent's workspace.
// Layout: {workspaceDir}/{owner}/{repo}/{agentAlias}/
// Each agent gets an isolated directory so concurrent runs don't conflict.
func AgentRepoPath(workspaceDir, owner, repo, agentAlias string) string {
	return filepath.Join(workspaceDir, owner, repo, agentAlias)
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
func EnsureBranch(ctx context.Context, sshKey []byte, repoPath, branchName, baseBranch string) error {
	keyFile, err := writeTempKey(sshKey)
	if err != nil {
		return err
	}
	defer os.Remove(keyFile)
	sshCmd := sshCommand(keyFile)

	out, _ := runGit(ctx, sshCmd, repoPath, "ls-remote", "--heads", "origin", branchName)
	if strings.Contains(out, "refs/heads/"+branchName) {
		if out, err := runGit(ctx, sshCmd, repoPath, "fetch", "origin", branchName); err != nil {
			return fmt.Errorf("gitops: fetch branch: %w\n%s", err, out)
		}
		if out, err := runGit(ctx, "", repoPath, "checkout", branchName); err != nil {
			return fmt.Errorf("gitops: checkout existing branch: %w\n%s", err, out)
		}
		if out, err := runGit(ctx, "", repoPath, "rebase", "origin/"+baseBranch); err != nil {
			_, _ = runGit(ctx, "", repoPath, "rebase", "--abort")
			return fmt.Errorf("gitops: rebase failed: %w\n%s", err, out)
		}
		return nil
	}
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

// ---- internal helpers ----

func writeTempKey(sshKey []byte) (string, error) {
	f, err := os.CreateTemp("", "fixloop-sshkey-*")
	if err != nil {
		return "", fmt.Errorf("gitops: create temp key file: %w", err)
	}
	name := f.Name()
	cleanup := func() { f.Close(); os.Remove(name) }

	if err := os.Chmod(name, 0600); err != nil {
		cleanup()
		return "", fmt.Errorf("gitops: chmod key file: %w", err)
	}
	if _, err := f.Write(sshKey); err != nil {
		cleanup()
		return "", fmt.Errorf("gitops: write key file: %w", err)
	}
	f.Close()
	return name, nil
}

func sshCommand(keyFile string) string {
	// StrictHostKeyChecking=accept-new: accept unknown hosts on first connect,
	// reject changed host keys (prevents MITM against known hosts).
	return fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=accept-new -o IdentitiesOnly=yes -o BatchMode=yes", keyFile)
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
