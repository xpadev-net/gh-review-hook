package github

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

var apiBase = "https://api.github.com"
var apiHTTPClient = &http.Client{Timeout: 30 * time.Second}
var httpRetryBaseWait = 2 * time.Second

const (
	pollInterval     = 15 * time.Second
	pollTimeout      = 30 * time.Minute
	checksAppearWait = 60 * time.Second // max time to wait for at least one check to appear

	httpMaxRetries    = 3
	maxRetryAfterWait = 5 * time.Minute // cap on server-supplied Retry-After to prevent hour-long hangs
)

// retryableHTTPError is returned when an HTTP response has a retryable status code.
type retryableHTTPError struct {
	statusCode int
	retryAfter time.Duration // suggested wait from Retry-After / X-RateLimit-Reset; 0 if absent
	msg        string
}

func (e *retryableHTTPError) Error() string { return e.msg }

// isRetryableErr returns true if err represents a transient failure worth retrying for GET requests.
func isRetryableErr(err error) bool {
	var rhe *retryableHTTPError
	if errors.As(err, &rhe) {
		return true
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Timeout()
	}
	return false
}

// isPostRetryableErr returns true only for 429 rate-limit errors, which are safe to
// retry for POST requests. 5xx and network errors are excluded because the server
// may have already processed the request.
func isPostRetryableErr(err error) bool {
	var rhe *retryableHTTPError
	if errors.As(err, &rhe) {
		return rhe.statusCode == http.StatusTooManyRequests
	}
	return false
}

// parseRetryAfter reads the Retry-After (delta-seconds or HTTP-date per RFC 7231 §7.1.3)
// or X-RateLimit-Reset (unix timestamp) header and returns the suggested wait duration,
// capped at maxRetryAfterWait. Returns 0 if neither header is present or parseable.
func parseRetryAfter(h http.Header) time.Duration {
	if v := h.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return min(time.Duration(secs)*time.Second, maxRetryAfterWait)
		}
		// Also handle HTTP-date format per RFC 7231 §7.1.3.
		if t, err := http.ParseTime(v); err == nil {
			if d := time.Until(t); d > 0 {
				return min(d, maxRetryAfterWait)
			}
		}
	}
	if v := h.Get("X-RateLimit-Reset"); v != "" {
		if unix, err := strconv.ParseInt(v, 10, 64); err == nil {
			if d := time.Until(time.Unix(unix, 0)); d > 0 {
				return min(d, maxRetryAfterWait)
			}
		}
	}
	return 0
}

// PR represents a GitHub Pull Request (partial fields).
type PR struct {
	Number int    `json:"number"`
	Body   string `json:"body"`
	Head   struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

// CommentReactions represents the subset of reactions used by this tool.
type CommentReactions struct {
	PlusOne int `json:"+1"`
	Eyes    int `json:"eyes"`
}

// IssueComment represents a GitHub issue comment on a PR.
type IssueComment struct {
	ID        int64            `json:"id"`
	Body      string           `json:"body"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
	Reactions CommentReactions `json:"reactions"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

// Commit represents the subset of commit data needed for timing checks.
type Commit struct {
	Commit struct {
		Author struct {
			Date time.Time `json:"date"`
		} `json:"author"`
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
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
	SeenContexts []string // list of check run names and status contexts observed
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

// GetPRComments fetches all issue comments on a PR, handling pagination.
func GetPRComments(owner, repo string, number int, token string) ([]IssueComment, error) {
	return getComments[IssueComment](fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments?per_page=100&page=", apiBase, owner, repo, number), token)
}

// GetReviewComments fetches all PR review comments, handling pagination.
func GetReviewComments(owner, repo string, number int, token string) ([]IssueComment, error) {
	return getComments[IssueComment](fmt.Sprintf("%s/repos/%s/%s/pulls/%d/comments?per_page=100&page=", apiBase, owner, repo, number), token)
}

func getComments[T any](baseURL, token string) ([]T, error) {
	var all []T
	page := 1
	for {
		url := fmt.Sprintf("%s%d", baseURL, page)
		var comments []T
		if err := apiGet(url, token, &comments); err != nil {
			return nil, err
		}
		if len(comments) == 0 {
			break
		}
		all = append(all, comments...)
		page++
	}
	return all, nil
}

// CreatePRComment posts an issue comment on a PR.
func CreatePRComment(owner, repo string, number int, body, token string) (*IssueComment, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", apiBase, owner, repo, number)
	payload, err := json.Marshal(struct {
		Body string `json:"body"`
	}{
		Body: body,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode PR comment payload: %w", err)
	}

	var comment IssueComment
	if err := apiPost(url, token, bytes.NewReader(payload), &comment); err != nil {
		return nil, err
	}
	return &comment, nil
}

// GetCommitTimestamp fetches commit metadata and returns its timestamp.
// It prefers committer date, falling back to author date when needed.
func GetCommitTimestamp(owner, repo, sha, token string) (time.Time, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/commits/%s", apiBase, owner, repo, sha)
	var commit Commit
	if err := apiGet(url, token, &commit); err != nil {
		return time.Time{}, err
	}
	if !commit.Commit.Committer.Date.IsZero() {
		return commit.Commit.Committer.Date, nil
	}
	if !commit.Commit.Author.Date.IsZero() {
		return commit.Commit.Author.Date, nil
	}
	return time.Time{}, fmt.Errorf("commit %s has no author/committer timestamp", sha)
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

		// Record seen check names and status contexts to avoid refetching later.
		var seen []string
		for _, cr := range dedupCheckRuns {
			seen = append(seen, cr.Name)
		}
		for _, s := range dedupStatuses {
			seen = append(seen, s.Context)
		}
		result.SeenContexts = seen

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
// decodes the JSON response into dest. Retries up to httpMaxRetries times on
// transient failures (timeouts, 429, 5xx).
func apiGet(url, token string, dest interface{}) error {
	var lastErr error
	for attempt := 0; attempt <= httpMaxRetries; attempt++ {
		if attempt > 0 {
			wait := httpRetryBaseWait * time.Duration(1<<(attempt-1))
			var rhe *retryableHTTPError
			if errors.As(lastErr, &rhe) && rhe.retryAfter > wait {
				wait = rhe.retryAfter
			}
			time.Sleep(wait)
		}
		lastErr = doAPIGet(url, token, dest)
		if lastErr == nil || !isRetryableErr(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

func doAPIGet(rawURL, token string, dest interface{}) error {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request for %s: %w", rawURL, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := apiHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed for %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		msg := fmt.Sprintf("GitHub API returned HTTP %d for %s", resp.StatusCode, rawURL)
		if readErr == nil {
			msg += ": " + string(body)
		}
		if isRetryableStatusCode(resp.StatusCode) {
			return &retryableHTTPError{statusCode: resp.StatusCode, retryAfter: parseRetryAfter(resp.Header), msg: msg}
		}
		return errors.New(msg)
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("failed to decode JSON from %s: %w", rawURL, err)
	}
	return nil
}

// apiPost performs a POST request to the GitHub API with the given token and
// decodes the JSON response into dest. Retries up to httpMaxRetries times on
// 429 rate-limit responses only; 5xx and network errors are not retried to
// avoid duplicate side effects on non-idempotent requests.
func apiPost(rawURL, token string, body io.Reader, dest interface{}) error {
	// POST bodies may only be read once; read into memory so retries can replay.
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return fmt.Errorf("failed to read request body for %s: %w", rawURL, err)
		}
	}

	var lastErr error
	for attempt := 0; attempt <= httpMaxRetries; attempt++ {
		if attempt > 0 {
			wait := httpRetryBaseWait * time.Duration(1<<(attempt-1))
			var rhe *retryableHTTPError
			if errors.As(lastErr, &rhe) && rhe.retryAfter > wait {
				wait = rhe.retryAfter
			}
			time.Sleep(wait)
		}
		var r io.Reader
		if bodyBytes != nil {
			r = bytes.NewReader(bodyBytes)
		}
		lastErr = doAPIPost(rawURL, token, r, dest)
		if lastErr == nil || !isPostRetryableErr(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

func doAPIPost(rawURL, token string, body io.Reader, dest interface{}) error {
	req, err := http.NewRequest("POST", rawURL, body)
	if err != nil {
		return fmt.Errorf("failed to create request for %s: %w", rawURL, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := apiHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed for %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(resp.Body)
		msg := fmt.Sprintf("GitHub API returned HTTP %d for %s", resp.StatusCode, rawURL)
		if readErr == nil {
			msg += ": " + string(respBody)
		}
		if isRetryableStatusCode(resp.StatusCode) {
			return &retryableHTTPError{statusCode: resp.StatusCode, retryAfter: parseRetryAfter(resp.Header), msg: msg}
		}
		return errors.New(msg)
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("failed to decode JSON from %s: %w", rawURL, err)
	}
	return nil
}

// isRetryableStatusCode returns true for HTTP status codes that indicate a
// transient server-side failure.
func isRetryableStatusCode(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}
