package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var apiBase = "https://api.github.com"

const (
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

// IssueComment represents a GitHub issue comment (used for PR timeline comments).
type IssueComment struct {
	Body string `json:"body"`
}

// ReviewComment represents a GitHub pull request review comment.
type ReviewComment struct {
	Body string `json:"body"`
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

// GetIssueCommentBodies fetches all issue comment bodies for a PR (paginated).
func GetIssueCommentBodies(owner, repo string, number int, token string) ([]string, error) {
	var bodies []string
	page := 1
	for {
		url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments?per_page=100&page=%d", apiBase, owner, repo, number, page)
		var comments []IssueComment
		if err := apiGet(url, token, &comments); err != nil {
			return nil, err
		}
		if len(comments) == 0 {
			break
		}
		for _, c := range comments {
			if c.Body != "" {
				bodies = append(bodies, c.Body)
			}
		}
		page++
	}
	return bodies, nil
}

// GetReviewCommentBodies fetches all PR review comment bodies (paginated).
func GetReviewCommentBodies(owner, repo string, number int, token string) ([]string, error) {
	var bodies []string
	page := 1
	for {
		url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/comments?per_page=100&page=%d", apiBase, owner, repo, number, page)
		var comments []ReviewComment
		if err := apiGet(url, token, &comments); err != nil {
			return nil, err
		}
		if len(comments) == 0 {
			break
		}
		for _, c := range comments {
			if c.Body != "" {
				bodies = append(bodies, c.Body)
			}
		}
		page++
	}
	return bodies, nil
}

// GetPRCommentBodies fetches PR issue comments and review comments and combines them.
func GetPRCommentBodies(owner, repo string, number int, token string) ([]string, error) {
	issueBodies, err := GetIssueCommentBodies(owner, repo, number, token)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch issue comments: %w", err)
	}
	reviewBodies, err := GetReviewCommentBodies(owner, repo, number, token)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch review comments: %w", err)
	}
	return append(issueBodies, reviewBodies...), nil
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
		if len(resp.CheckRuns) == 0 || len(all) >= resp.TotalCount {
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
// If logw is non-nil, status changes are logged to it as they occur.
func WaitForChecks(owner, repo, sha, token string, logw io.Writer) (*CIResult, error) {
	logf := func(format string, a ...any) {
		if logw != nil {
			fmt.Fprintf(logw, format, a...)
		}
	}

	start := time.Now()
	checksAppeared := false

	// Track previous status of each check for change detection
	prevCheckRuns := make(map[string]string) // name → status string
	prevStatuses := make(map[string]string)  // context → state

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

		// Deduplicate by name/context (API returns newest first)
		dedupCheckRuns := deduplicateCheckRuns(checkRuns)
		dedupStatuses := deduplicateStatuses(statuses)

		totalChecks := len(dedupCheckRuns) + len(dedupStatuses)

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

		// Log status changes for check runs
		for _, cr := range dedupCheckRuns {
			current := checkRunStatusString(cr)
			if prev, ok := prevCheckRuns[cr.Name]; ok {
				if prev != current {
					logf("[CI] %s: %s → %s\n", cr.Name, prev, current)
				}
			} else {
				logf("[CI] %s: %s\n", cr.Name, current)
			}
			prevCheckRuns[cr.Name] = current
		}

		// Log status changes for commit statuses
		for _, s := range dedupStatuses {
			if prev, ok := prevStatuses[s.Context]; ok {
				if prev != s.State {
					logf("[CI] %s: %s → %s\n", s.Context, prev, s.State)
				}
			} else {
				logf("[CI] %s: %s\n", s.Context, s.State)
			}
			prevStatuses[s.Context] = s.State
		}

		// Check if all are completed
		allCompleted := true
		for _, cr := range dedupCheckRuns {
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

		for _, cr := range dedupCheckRuns {
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

// checkRunStatusString returns a human-readable status string for a CheckRun.
func checkRunStatusString(cr CheckRun) string {
	if cr.Status == "completed" && cr.Conclusion != "" {
		return fmt.Sprintf("%s (%s)", cr.Status, cr.Conclusion)
	}
	return cr.Status
}

// deduplicateCheckRuns keeps only the latest check run per name.
// The API returns newest-first, so the first occurrence per name is kept.
func deduplicateCheckRuns(runs []CheckRun) []CheckRun {
	seen := make(map[string]bool)
	var result []CheckRun
	for _, cr := range runs {
		if !seen[cr.Name] {
			seen[cr.Name] = true
			result = append(result, cr)
		}
	}
	return result
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
