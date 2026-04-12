package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const apiBase = "https://api.github.com"

// Client is a GitHub API client for a single PAT.
type Client struct {
	token  string
	http   *http.Client
}

// New creates a GitHub API client authenticated with the given PAT.
func New(pat string) *Client {
	return &Client{
		token: pat,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
}

// do executes an authenticated GitHub API request with retry logic.
// Retries on 429 (rate limit), 500/502/503 (server errors), and secondary rate limits.
func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, []byte, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	backoff := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	for attempt := 0; attempt <= 3; attempt++ {
		if bodyReader != nil {
			if seeker, ok := bodyReader.(io.Seeker); ok {
				seeker.Seek(0, io.SeekStart)
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, apiBase+path, bodyReader)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.http.Do(req)
		if err != nil {
			if attempt == 3 {
				return nil, nil, err
			}
			time.Sleep(backoff[attempt])
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		switch {
		case resp.StatusCode == 429:
			wait := 60 * time.Second
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.Atoi(ra); err == nil {
					wait = time.Duration(secs) * time.Second
				}
			}
			slog.Warn("github rate limited", "retry_after", wait)
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(wait):
			}
			continue

		case resp.StatusCode == 403 && strings.Contains(string(respBody), "secondary rate limit"):
			slog.Warn("github secondary rate limit hit, waiting 60s")
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(60 * time.Second):
			}
			continue

		case resp.StatusCode >= 500 && attempt < 3:
			slog.Warn("github server error", "status", resp.StatusCode, "attempt", attempt+1)
			time.Sleep(backoff[attempt])
			continue
		}

		return resp, respBody, nil
	}
	return nil, nil, fmt.Errorf("github api: max retries exceeded")
}

// DeployKey represents a GitHub repository deploy key.
type DeployKey struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Key      string `json:"key"`
	ReadOnly bool   `json:"read_only"`
}

// AddDeployKey registers an SSH public key as a deploy key on the given repo.
func (c *Client) AddDeployKey(ctx context.Context, owner, repo, title, pubKey string) (*DeployKey, error) {
	resp, body, err := c.do(ctx, "POST",
		fmt.Sprintf("/repos/%s/%s/keys", owner, repo),
		map[string]any{"title": title, "key": pubKey, "read_only": false},
	)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("add deploy key: status %d: %s", resp.StatusCode, body)
	}
	var dk DeployKey
	if err := json.Unmarshal(body, &dk); err != nil {
		return nil, err
	}
	return &dk, nil
}

// DeleteDeployKey removes a deploy key from the given repo.
func (c *Client) DeleteDeployKey(ctx context.Context, owner, repo string, keyID int64) error {
	resp, body, err := c.do(ctx, "DELETE",
		fmt.Sprintf("/repos/%s/%s/keys/%d", owner, repo, keyID),
		nil,
	)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("delete deploy key: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// Issue represents a GitHub issue.
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	HTMLURL string `json:"html_url"`
}


// CreateIssue opens a new issue on the given repo.
func (c *Client) CreateIssue(ctx context.Context, owner, repo, title, body string, labels []string) (*Issue, error) {
	resp, respBody, err := c.do(ctx, "POST",
		fmt.Sprintf("/repos/%s/%s/issues", owner, repo),
		map[string]any{"title": title, "body": body, "labels": labels},
	)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create issue: status %d: %s", resp.StatusCode, respBody)
	}
	var issue Issue
	if err := json.Unmarshal(respBody, &issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

// GetIssue fetches a single issue.
func (c *Client) GetIssue(ctx context.Context, owner, repo string, number int) (*Issue, error) {
	resp, body, err := c.do(ctx, "GET",
		fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number),
		nil,
	)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get issue: status %d", resp.StatusCode)
	}
	var issue Issue
	return &issue, json.Unmarshal(body, &issue)
}

// CloseIssue closes an issue. Handles 422 (already closed) gracefully.
func (c *Client) CloseIssue(ctx context.Context, owner, repo string, number int, comment string) error {
	if comment != "" {
		c.do(ctx, "POST", //nolint — best effort comment
			fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number),
			map[string]any{"body": comment},
		)
	}
	resp, body, err := c.do(ctx, "PATCH",
		fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number),
		map[string]any{"state": "closed"},
	)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnprocessableEntity {
		// 422 = already closed, treat as success
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("close issue: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// ReopenIssue reopens a closed issue.
func (c *Client) ReopenIssue(ctx context.Context, owner, repo string, number int) error {
	resp, body, err := c.do(ctx, "PATCH",
		fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number),
		map[string]any{"state": "open"},
	)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("reopen issue: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// PR represents a GitHub pull request.
type PR struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
	MergeCommitSHA string `json:"merge_commit_sha"`
}

// CreatePR opens a new pull request.
func (c *Client) CreatePR(ctx context.Context, owner, repo, title, body, head, base string) (*PR, error) {
	resp, respBody, err := c.do(ctx, "POST",
		fmt.Sprintf("/repos/%s/%s/pulls", owner, repo),
		map[string]any{"title": title, "body": body, "head": head, "base": base},
	)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("create pr: status %d: %s", resp.StatusCode, respBody)
	}
	var pr PR
	return &pr, json.Unmarshal(respBody, &pr)
}

// RequestCopilotReview requests Copilot review on a PR.
func (c *Client) RequestCopilotReview(ctx context.Context, owner, repo string, prNumber int) error {
	resp, body, err := c.do(ctx, "POST",
		fmt.Sprintf("/repos/%s/%s/pulls/%d/requested_reviewers", owner, repo, prNumber),
		map[string]any{"reviewers": []string{"copilot-pull-request-reviewer[bot]"}},
	)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("request copilot review: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// MergePR squash-merges a pull request.
func (c *Client) MergePR(ctx context.Context, owner, repo string, prNumber int, commitTitle string) (string, error) {
	resp, body, err := c.do(ctx, "PUT",
		fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", owner, repo, prNumber),
		map[string]any{"merge_method": "squash", "commit_title": commitTitle},
	)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("merge pr: status %d: %s", resp.StatusCode, body)
	}
	var result struct {
		SHA string `json:"sha"`
	}
	json.Unmarshal(body, &result)
	return result.SHA, nil
}

// DeleteBranch deletes a git branch.
func (c *Client) DeleteBranch(ctx context.Context, owner, repo, branch string) error {
	resp, body, err := c.do(ctx, "DELETE",
		fmt.Sprintf("/repos/%s/%s/git/refs/heads/%s", owner, repo, branch),
		nil,
	)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("delete branch: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// ListOpenPRs returns open PRs targeting the given base branch.
func (c *Client) ListOpenPRs(ctx context.Context, owner, repo, base string) ([]PR, error) {
	resp, body, err := c.do(ctx, "GET",
		fmt.Sprintf("/repos/%s/%s/pulls?state=open&base=%s&per_page=50", owner, repo, base),
		nil,
	)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list prs: status %d", resp.StatusCode)
	}
	var prs []PR
	return prs, json.Unmarshal(body, &prs)
}

// PRReview represents a single review on a PR.
type PRReview struct {
	State string `json:"state"` // APPROVED, COMMENTED, CHANGES_REQUESTED, DISMISSED
	User  struct {
		Login string `json:"login"`
	} `json:"user"`
}

// ListPRReviews returns all reviews for a PR.
func (c *Client) ListPRReviews(ctx context.Context, owner, repo string, prNumber int) ([]PRReview, error) {
	resp, body, err := c.do(ctx, "GET",
		fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", owner, repo, prNumber),
		nil,
	)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list reviews: status %d", resp.StatusCode)
	}
	var reviews []PRReview
	return reviews, json.Unmarshal(body, &reviews)
}

// AddIssueComment posts a comment on a GitHub issue.
func (c *Client) AddIssueComment(ctx context.Context, owner, repo string, number int, body string) error {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number)
	payload := map[string]string{"body": body}
	resp, respBody, err := c.do(ctx, http.MethodPost, path, payload)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("add issue comment: status %d: %.200s", resp.StatusCode, respBody)
	}
	return nil
}
