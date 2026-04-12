package shared

import (
	"context"
	"database/sql"
	"fmt"

	githubclient "github.com/fixloop/fixloop/internal/github"
	"github.com/fixloop/fixloop/internal/gitops"
)

// CommitPushPR stages all changes, commits, pushes, and creates (or finds existing) PR.
// Returns the PR number and any error.
// If a PR already exists for the given branch in the prs table, skips PR creation.
// force=true uses --force-with-lease on push (for re-attempts on same branch).
func CommitPushPR(
	ctx context.Context,
	db *sql.DB,
	projectID int64,
	sshKey []byte,
	pat []byte,
	repoPath string,
	branchName string,
	commitMsg string,
	prTitle string,
	prBody string,
	ghOwner string,
	ghRepo string,
	baseBranch string,
	force bool,
) (prNumber int, err error) {
	if err := gitops.CommitAll(ctx, repoPath, commitMsg); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	if err := gitops.Push(ctx, sshKey, repoPath, branchName, force); err != nil {
		return 0, fmt.Errorf("push: %w", err)
	}

	gh := githubclient.New(string(pat))

	var existingPR int
	err = db.QueryRowContext(ctx,
		`SELECT github_number FROM prs WHERE project_id = ? AND branch = ? AND status = 'open' LIMIT 1`,
		projectID, branchName,
	).Scan(&existingPR)
	if err == nil {
		return existingPR, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("check existing PR: %w", err)
	}

	pr, err := gh.CreatePR(ctx, ghOwner, ghRepo, prTitle, prBody, branchName, baseBranch)
	if err != nil {
		return 0, fmt.Errorf("create PR: %w", err)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO prs (project_id, github_number, branch, status, title)
		 VALUES (?, ?, ?, 'open', ?)
		 ON DUPLICATE KEY UPDATE status='open', title=VALUES(title)`,
		projectID, pr.Number, branchName, prTitle,
	); err != nil {
		return 0, fmt.Errorf("insert prs: %w", err)
	}

	return pr.Number, nil
}
