// internal/vercel/client.go
package vercel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const apiBase = "https://api.vercel.com"

// Deployment represents a Vercel deployment.
type Deployment struct {
	UID        string `json:"uid"`
	ReadyState string `json:"readyState"` // READY|ERROR|BUILDING|QUEUED|INITIALIZING|CANCELED
	Meta       struct {
		GitHubCommitSha string `json:"githubCommitSha"`
	} `json:"meta"`
}

// WaitForDeployment polls until a deployment matching commitSHA reaches READY or ERROR.
// Polls every 30s, max 20 times (10 min total). Returns the matching deployment or an error.
// If Vercel is not configured (token or projectID empty), returns nil, nil immediately.
func WaitForDeployment(ctx context.Context, token, projectID, commitSHA string) (*Deployment, error) {
	if token == "" || projectID == "" {
		return nil, nil // Vercel not configured
	}

	client := &http.Client{Timeout: 15 * time.Second}

	for attempt := 0; attempt < 20; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(30 * time.Second):
			}
		}

		deps, err := listDeployments(ctx, client, token, projectID)
		if err != nil {
			// Transient error: log and retry
			continue
		}

		for i := range deps {
			d := &deps[i]
			if !shaMatches(d.Meta.GitHubCommitSha, commitSHA) {
				continue
			}
			switch d.ReadyState {
			case "READY":
				return d, nil
			case "ERROR", "CANCELED":
				return d, fmt.Errorf("vercel: deployment %s state=%s", d.UID, d.ReadyState)
			}
			// Still building: break inner loop, wait next poll
			break
		}
	}
	return nil, fmt.Errorf("vercel: deployment for %.8s not ready after 10 minutes", commitSHA)
}

// shaMatches returns true if either SHA is a prefix of the other (handles short vs full SHA).
func shaMatches(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	shorter, longer := a, b
	if len(a) > len(b) {
		shorter, longer = b, a
	}
	return strings.HasPrefix(longer, shorter)
}

func listDeployments(ctx context.Context, client *http.Client, token, projectID string) ([]Deployment, error) {
	url := fmt.Sprintf("%s/v13/deployments?projectId=%s&limit=10", apiBase, projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vercel API error %d: %.200s", resp.StatusCode, body)
	}

	var result struct {
		Deployments []Deployment `json:"deployments"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("vercel: parse response: %w", err)
	}
	return result.Deployments, nil
}
