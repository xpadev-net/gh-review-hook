package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	apiBase          = "https://api.github.com"
	pollInterval     = 15 * time.Second
	pollTimeout      = 30 * time.Minute
	checksAppearWait = 60 * time.Second // max time to wait for at least one check to appear
)

// PR represents a GitHub Pull Request (partial fields).
type PR struct {
	Number int    `json:"number"`
	Body   string `json:"body"`
	Head   struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
}

// CheckRun represents a GitHub Check Run.
type CheckRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// CheckRunsResponse wraps the list check-runs API response.
type CheckRunsResponse struct {
	TotalCount int        `json:"total_count"`
	CheckRuns  []CheckRun `json:"check_runs"`
}

// CommitStatus represents a GitHub Commit Status.
type CommitStatus struct {
	State   string `json:"state"`
	Context string `json:"context"`
}

// CIResult contains the outcome of waiting for CI checks to complete.
type CIResult struct {
	AllGreen     bool     // true if all checks passed
	FailedChecks []string // names of checks that did not succeed (empty if AllGreen)
}

// FindPR finds an open PR for the given branch. Returns nil if no PR is found.
func FindPR(owner, repo, branch, token string) (*PR, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls?head=%s:%s&state=open", apiBase, owner, repo, owner, branch)
	var prs []PR
	if err := apiGet(url, token, &prs); err != nil {
		return nil, err
	}
	if len(prs) == 0 {
		return nil, nil
	}
	return &prs[0], nil
}

// GetPR fetches a PR by number.
func GetPR(owner, repo string, number int, token string) (*PR, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", apiBase, owner, repo, number)
	var pr PR
	if err := apiGet(url, token, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// GetCheckRuns fetches all check runs for a commit SHA, handling pagination.
func GetCheckRuns(owner, repo, sha, token string) ([]CheckRun, error) {
	var all []CheckRun
	page := 1
	for {
		url := fmt.Sprintf("%s/repos/%s/%s/commits/%s/check-runs?per_page=100&page=%d", apiBase, owner, repo, sha, page)
		var resp CheckRunsResponse
		if err := apiGet(url, token, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.CheckRuns...)
		if len(all) >= resp.TotalCount {
			break
		}
		page++
	}
	return all, nil
}

// GetStatuses fetches all commit statuses for a SHA, handling pagination.
func GetStatuses(owner, repo, sha, token string) ([]CommitStatus, error) {
	var all []CommitStatus
	page := 1
	for {
		url := fmt.Sprintf("%s/repos/%s/%s/commits/%s/statuses?per_page=100&page=%d", apiBase, owner, repo, sha, page)
		var statuses []CommitStatus
		if err := apiGet(url, token, &statuses); err != nil {
			return nil, err
		}
		if len(statuses) == 0 {
			break
		}
		all = append(all, statuses...)
		page++
	}
	return all, nil
}

// WaitForChecks polls until all CI checks on the given SHA are complete.
// Returns a CIResult indicating whether all checks passed and which failed.
// Times out after 30 minutes.
func WaitForChecks(owner, repo, sha, token string) (*CIResult, error) {
	start := time.Now()
	checksAppeared := false

	for {
		if time.Since(start) > pollTimeout {
			return nil, fmt.Errorf("timed out after 30 minutes waiting for CI checks to complete")
		}

		checkRuns, err := GetCheckRuns(owner, repo, sha, token)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch check runs: %w", err)
		}

		statuses, err := GetStatuses(owner, repo, sha, token)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch commit statuses: %w", err)
		}

		// Deduplicate statuses by context (API returns newest first)
		dedupStatuses := deduplicateStatuses(statuses)

		totalChecks := len(checkRuns) + len(dedupStatuses)

		// If no checks exist yet, wait for them to appear (up to checksAppearWait).
		// CI providers (CodeRabbit, GitHub Actions, etc.) take a few seconds to
		// register their check runs / statuses after a push.
		if totalChecks == 0 {
			if !checksAppeared && time.Since(start) < checksAppearWait {
				time.Sleep(pollInterval)
				continue
			}
			// No CI configured — treat as all green
			return &CIResult{AllGreen: true}, nil
		}
		checksAppeared = true

		// Check if all are completed
		allCompleted := true
		for _, cr := range checkRuns {
			if cr.Status != "completed" {
				allCompleted = false
				break
			}
		}
		if allCompleted {
			for _, s := range dedupStatuses {
				if s.State == "pending" {
					allCompleted = false
					break
				}
			}
		}

		if !allCompleted {
			time.Sleep(pollInterval)
			continue
		}

		// All completed — determine results
		result := &CIResult{AllGreen: true}

		for _, cr := range checkRuns {
			switch cr.Conclusion {
			case "success", "skipped", "neutral":
				// OK
			default:
				result.AllGreen = false
				result.FailedChecks = append(result.FailedChecks, cr.Name)
			}
		}

		for _, s := range dedupStatuses {
			if s.State != "success" {
				result.AllGreen = false
				result.FailedChecks = append(result.FailedChecks, s.Context)
			}
		}

		return result, nil
	}
}

// deduplicateStatuses keeps only the latest status per context.
// The API returns statuses newest-first, so the first occurrence per context is kept.
func deduplicateStatuses(statuses []CommitStatus) []CommitStatus {
	seen := make(map[string]bool)
	var result []CommitStatus
	for _, s := range statuses {
		if !seen[s.Context] {
			seen[s.Context] = true
			result = append(result, s)
		}
	}
	return result
}

// apiGet performs a GET request to the GitHub API with the given token and
// decodes the JSON response into dest.
func apiGet(url, token string, dest interface{}) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request for %s: %w", url, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed for %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("GitHub API returned HTTP %d for %s (failed to read body: %v)", resp.StatusCode, url, readErr)
		}
		return fmt.Errorf("GitHub API returned HTTP %d for %s: %s", resp.StatusCode, url, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("failed to decode JSON from %s: %w", url, err)
	}
	return nil
}
