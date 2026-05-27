package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xpadev-net/gh-review-hook/internal/git"
	"github.com/xpadev-net/gh-review-hook/internal/github"
	"github.com/xpadev-net/gh-review-hook/internal/greptile"
	"github.com/xpadev-net/gh-review-hook/internal/parser"
)

// greptileUpdateDelay is the time to wait for Greptile to update the PR description
// after CI checks complete.
var greptileUpdateDelay = 10 * time.Second

func printHelp() {
	fmt.Println(`gh-review-hook - Claude Code Stop hook that checks PR readiness

Usage:
  gh-review-hook                          Auto-detect PR from current branch
  gh-review-hook <PR number>              Check a specific PR by number
  gh-review-hook <PR URL>                 Check a specific PR by URL
  gh-review-hook install                  Register as a Claude Code Stop hook
  gh-review-hook --help                   Show this help message

Options:
  -h, --help    Show this help message`)
}

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			return runInstall()
		case "--help", "-h", "-help", "help":
			printHelp()
			return 0
		}
	}

	// Step 1: Check working tree cleanliness
	noUpstream, err := git.EnsureClean()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}
	if noUpstream {
		// No upstream configured — nothing to review
		return 0
	}

	// Step 2: Resolve GitHub token
	token, err := github.GetToken()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	// Step 3: Determine PR
	var pr *github.PR
	var owner, repo string

	if len(os.Args) > 1 {
		arg := os.Args[1]
		pr, owner, repo, err = resolvePRFromArg(arg, token)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
	} else {
		pr, owner, repo, err = resolvePRFromBranch(token)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
		if pr == nil {
			// No PR found — nothing to review
			fmt.Fprintln(os.Stdout, "No open PR found for the current branch.")
			return 0
		}
	}

	// Step 4: Wait for CI checks to complete
	ciResult, err := github.WaitForChecks(owner, repo, pr.Head.SHA, token, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	// Step 4.5: Wait for Copilot requested review to complete
	if err := github.WaitForRequestedCopilotReview(owner, repo, pr.Number, token, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	// Detect presence of a Greptile check/context using CI result's seen contexts.
	// Only skip Greptile if we observed CI checks but none contain "greptile".
	hasGreptile := false
	for _, c := range ciResult.SeenContexts {
		if strings.Contains(strings.ToLower(c), "greptile") {
			hasGreptile = true
			break
		}
	}
	if len(ciResult.SeenContexts) > 0 && !hasGreptile {
		// Observed CI checks but no Greptile — skip Greptile extraction.
		fmt.Fprintln(os.Stderr, "[Greptile] no Greptile CI status observed; skipping Greptile description extraction")
	}

	// Step 5: Wait for Greptile to update PR description
	if hasGreptile {
		time.Sleep(greptileUpdateDelay)
	}

	// Step 6: Fetch latest PR body
	latestPR, err := github.GetPR(owner, repo, pr.Number, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch PR body: %v\n", err)
		return 1
	}

	// Step 8: Parse Greptile review
	confidenceSection, prompt, found := "", "", false
	if hasGreptile {
		confidenceSection, prompt, found = parser.ExtractGreptileReview(latestPR.Body)
		lastReviewedCommit := parser.ExtractLastReviewedCommit(latestPR.Body)
		if found && !parser.IsCommitReviewed(latestPR.Head.SHA, lastReviewedCommit) {
			found = false
			confidenceSection = ""
			prompt = ""
		}

		if !found {
			// Prefer PR description mode first; some repositories still publish the
			// canonical Greptile review in PR body updates.
			reviewData, err := greptile.WaitForReviewInPRBody(owner, repo, pr.Number, latestPR.Head.SHA, token, os.Stderr)
			if err != nil && !errors.Is(err, greptile.ErrReviewTimeout) {
				fmt.Fprintln(os.Stderr, err.Error())
				return 1
			}
			if reviewData == nil {
				if errors.Is(err, greptile.ErrReviewTimeout) {
					fmt.Fprintln(os.Stderr, "[Greptile] description review not found, falling back to comment mode")
				}
				reviewData, err = greptile.WaitForReview(owner, repo, pr.Number, latestPR.Head.SHA, token, os.Stderr)
				if err != nil {
					if !errors.Is(err, greptile.ErrReviewTimeout) {
						fmt.Fprintln(os.Stderr, err.Error())
						return 1
					}
				}
			}
			if reviewData != nil {
				confidenceSection = reviewData.ConfidenceSection
				prompt = reviewData.Prompt
				found = reviewData.Found
			}
		}
	}

	// Step 9: Determine output and exit code
	var feedbackParts []string

	// Part 1: CI failures
	if !ciResult.AllGreen {
		var sb strings.Builder
		sb.WriteString("CI failed checks:\n")
		for _, name := range ciResult.FailedChecks {
			sb.WriteString("- ")
			sb.WriteString(name)
			sb.WriteString("\n")
		}
		feedbackParts = append(feedbackParts, strings.TrimRight(sb.String(), "\n"))
	}

	// Part 2: Skip confidence section when score is 5/5
	// Part 3: Always include prompt when present (5/5 + prompt = not approved)
	is5of5 := found && strings.HasPrefix(confidenceSection, "<h3>Confidence Score: 5/5</h3>")

	if found && confidenceSection != "" && !is5of5 {
		feedbackParts = append(feedbackParts, confidenceSection)
	}

	if prompt != "" {
		feedbackParts = append(feedbackParts, prompt)
	}

	// Step 10: Fetch latest PR comments and parse CodeRabbit prompts
	headCommitTime, err := github.GetCommitTimestamp(owner, repo, latestPR.Head.SHA, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch head commit timestamp: %v\n", err)
		return 1
	}

	issueComments, err := github.GetPRComments(owner, repo, latestPR.Number, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch PR issue comments: %v\n", err)
		return 1
	}
	reviewComments, err := github.GetReviewComments(owner, repo, latestPR.Number, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch PR review comments: %v\n", err)
		return 1
	}
	var codeRabbitPrompts []string
	seenPrompts := make(map[string]bool)

	for _, comment := range issueComments {
		commentTime := comment.CreatedAt
		if comment.UpdatedAt.After(commentTime) {
			commentTime = comment.UpdatedAt
		}
		if !commentTime.After(headCommitTime) {
			continue
		}
		if strings.ToLower(strings.TrimSpace(comment.User.Login)) != "coderabbitai[bot]" {
			continue
		}
		p := parser.ExtractCodeRabbitPrompt(comment.Body)
		if p == "" {
			continue
		}
		if seenPrompts[p] {
			continue
		}
		seenPrompts[p] = true
		codeRabbitPrompts = append(codeRabbitPrompts, p)
	}

	// For inline review comments use only created_at — updated_at can be bumped by
	// GitHub remapping the comment to a newer commit or by reactions, neither of
	// which indicates new actionable content.
	for _, comment := range reviewComments {
		if !comment.CreatedAt.After(headCommitTime) {
			continue
		}
		if strings.ToLower(strings.TrimSpace(comment.User.Login)) != "coderabbitai[bot]" {
			continue
		}
		p := parser.ExtractCodeRabbitPrompt(comment.Body)
		if p == "" {
			continue
		}
		if seenPrompts[p] {
			continue
		}
		seenPrompts[p] = true
		codeRabbitPrompts = append(codeRabbitPrompts, p)
	}

	// CodeRabbit prompts are treated as actionable review comments independent of
	// Greptile's confidence score, so they are not gated by is5of5.
	for _, p := range codeRabbitPrompts {
		feedbackParts = append(feedbackParts, p)
	}

	// Regular issue comments newer than the head commit (skip already-handled bots)
	for _, comment := range issueComments {
		commentTime := comment.CreatedAt
		if comment.UpdatedAt.After(commentTime) {
			commentTime = comment.UpdatedAt
		}
		if !commentTime.After(headCommitTime) {
			continue
		}
		body := strings.TrimSpace(comment.Body)
		if body == "" {
			continue
		}
		// Skip the Greptile trigger comment — it may be posted by the caller's own
		// token (a human PAT) when waitForReview fires, so the poster's login won't
		// match the greptile-apps[bot] skip below.
		if strings.EqualFold(body, "@greptile review") {
			continue
		}
		loginLower := strings.ToLower(strings.TrimSpace(comment.User.Login))
		if loginLower == "coderabbitai[bot]" || loginLower == "greptile-apps[bot]" {
			continue
		}
		var sb strings.Builder
		sb.WriteString("Comment from ")
		sb.WriteString(comment.User.Login)
		sb.WriteString(":\n")
		sb.WriteString(body)
		feedbackParts = append(feedbackParts, sb.String())
	}

	// Step 11: Fetch and include unsupported PR reviews for the current HEAD commit
	reviews, err := github.GetPullRequestReviews(owner, repo, latestPR.Number, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch PR reviews: %v\n", err)
		return 1
	}
	reviewCommentsByReviewID := actionableReviewCommentsByReviewID(reviewComments, headCommitTime)
	feedbackParts = append(feedbackParts, pullRequestReviewFeedback(reviews, reviewCommentsByReviewID, latestPR.Head.SHA)...)

	// Step 12: Check base branch status using one shared fetch
	if latestPR.Head.Ref != "" && latestPR.Base.Ref != "" {
		if err := git.FetchRemoteRefs(latestPR.Head.Ref, latestPR.Base.Ref); err != nil {
			fmt.Fprintf(os.Stderr, "warning: base branch checks failed: %v\n", err)
		} else {
			behindCount, err := git.BehindBaseCountFromOrigin(latestPR.Head.Ref, latestPR.Base.Ref)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: base branch behind check failed: %v\n", err)
			} else if behindCount > 0 {
				feedbackParts = append(feedbackParts, formatBehindBaseBranchFeedback(latestPR.Base.Ref, behindCount))
			}

			conflictFiles, err := git.ConflictFilesFromOrigin(latestPR.Head.Ref, latestPR.Base.Ref)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: merge conflict check failed: %v\n", err)
			} else if len(conflictFiles) > 0 {
				var sb strings.Builder
				sb.WriteString("Merge conflicts detected with base branch '")
				sb.WriteString(latestPR.Base.Ref)
				sb.WriteString("':\n")
				for _, f := range conflictFiles {
					sb.WriteString("- ")
					sb.WriteString(f)
					sb.WriteString("\n")
				}
				feedbackParts = append(feedbackParts, strings.TrimRight(sb.String(), "\n"))
			}
		}
	}

	if len(feedbackParts) > 0 {
		fmt.Fprintln(os.Stderr, strings.Join(feedbackParts, "\n---\n"))
		return 2
	}

	return 0
}

func formatBehindBaseBranchFeedback(baseRef string, count int) string {
	commitWord := "commits"
	if count == 1 {
		commitWord = "commit"
	}
	return fmt.Sprintf(
		"PR is %d %s behind base branch '%s'. Please update this branch by merging base branch '%s' instead of rebasing to avoid rewriting history.",
		count,
		commitWord,
		baseRef,
		baseRef,
	)
}

func pullRequestReviewFeedback(reviews []github.PullRequestReview, reviewCommentsByReviewID map[int64][]github.PullRequestReviewComment, headSHA string) []string {
	var feedback []string
	reviewsByID := make(map[int64]github.PullRequestReview)
	for _, review := range reviews {
		reviewsByID[review.ID] = review
	}
	consumedReviewIDs := make(map[int64]bool)
	for _, review := range reviews {
		comments := reviewCommentsByReviewID[review.ID]
		if !isActionablePullRequestReview(review, comments, headSHA) {
			continue
		}
		feedback = append(feedback, formatPullRequestReview(review, comments))
		consumedReviewIDs[review.ID] = true
	}
	var reviewIDs []int64
	for reviewID, comments := range reviewCommentsByReviewID {
		if consumedReviewIDs[reviewID] {
			continue
		}
		if !shouldSurfaceStandaloneReviewComments(reviewsByID[reviewID], comments, headSHA) {
			continue
		}
		reviewIDs = append(reviewIDs, reviewID)
	}
	sort.Slice(reviewIDs, func(i, j int) bool {
		leftTime := earliestReviewCommentTime(reviewCommentsByReviewID[reviewIDs[i]])
		rightTime := earliestReviewCommentTime(reviewCommentsByReviewID[reviewIDs[j]])
		if leftTime.Equal(rightTime) {
			return reviewIDs[i] < reviewIDs[j]
		}
		return leftTime.Before(rightTime)
	})
	for _, reviewID := range reviewIDs {
		feedback = append(feedback, formatPullRequestReviewComments(reviewCommentsByReviewID[reviewID]))
	}
	return feedback
}

func shouldSurfaceStandaloneReviewComments(review github.PullRequestReview, comments []github.PullRequestReviewComment, headSHA string) bool {
	if len(comments) == 0 {
		return false
	}
	if review.ID == 0 {
		return true
	}
	if review.State == "DISMISSED" || isHandledReviewBot(review.User.Login) {
		return false
	}
	return review.CommitID != headSHA
}

func earliestReviewCommentTime(comments []github.PullRequestReviewComment) time.Time {
	if len(comments) == 0 {
		return time.Time{}
	}
	earliest := comments[0].CreatedAt
	for _, comment := range comments[1:] {
		if comment.CreatedAt.Before(earliest) {
			earliest = comment.CreatedAt
		}
	}
	return earliest
}

func isActionablePullRequestReview(review github.PullRequestReview, comments []github.PullRequestReviewComment, headSHA string) bool {
	if review.State == "PENDING" || review.SubmittedAt == nil {
		return false
	}
	if review.CommitID != headSHA {
		return false
	}
	if isHandledReviewBot(review.User.Login) {
		return false
	}
	// Only surface actionable feedback: CHANGES_REQUESTED (always) and COMMENTED
	// with a top-level body or inline comments. APPROVED/DISMISSED are not blocking.
	return review.State == "CHANGES_REQUESTED" || (review.State == "COMMENTED" && (review.Body != "" || len(comments) > 0))
}

func actionableReviewCommentsByReviewID(comments []github.PullRequestReviewComment, headCommitTime time.Time) map[int64][]github.PullRequestReviewComment {
	byReviewID := make(map[int64][]github.PullRequestReviewComment)
	for _, comment := range comments {
		if !comment.CreatedAt.After(headCommitTime) {
			continue
		}
		if isHandledReviewBot(comment.User.Login) {
			continue
		}
		if strings.TrimSpace(comment.Body) == "" {
			continue
		}
		// Keep replies under their own review. A reply to an old thread may be a
		// fresh review on the current HEAD, while the parent review is filtered out.
		byReviewID[comment.PullRequestReviewID] = append(byReviewID[comment.PullRequestReviewID], comment)
	}
	return byReviewID
}

func isHandledReviewBot(login string) bool {
	loginLower := strings.ToLower(strings.TrimSpace(login))
	return loginLower == "coderabbitai[bot]" || loginLower == "greptile-apps[bot]"
}

func formatPullRequestReview(review github.PullRequestReview, comments []github.PullRequestReviewComment) string {
	var sb strings.Builder
	sb.WriteString("Review from ")
	sb.WriteString(review.User.Login)
	sb.WriteString(" (")
	sb.WriteString(review.State)
	sb.WriteString("):")

	body := strings.TrimSpace(review.Body)
	if body != "" {
		sb.WriteString("\n")
		sb.WriteString(body)
	}
	if len(comments) > 0 {
		if body != "" {
			sb.WriteString("\n\n")
		} else {
			sb.WriteString("\n")
		}
		sb.WriteString("Review comments:")
		for _, comment := range comments {
			sb.WriteString("\n- ")
			location := reviewCommentLocation(comment)
			if location != "" {
				sb.WriteString(location)
				sb.WriteString(": ")
			}
			sb.WriteString(strings.TrimSpace(comment.Body))
		}
	}
	return sb.String()
}

func formatPullRequestReviewComments(comments []github.PullRequestReviewComment) string {
	var sb strings.Builder
	sb.WriteString("Review comments")
	if login := commonReviewCommentLogin(comments); login != "" {
		sb.WriteString(" from ")
		sb.WriteString(login)
	}
	sb.WriteString(":")
	for _, comment := range comments {
		sb.WriteString("\n- ")
		location := reviewCommentLocation(comment)
		if location != "" {
			sb.WriteString(location)
			sb.WriteString(": ")
		}
		sb.WriteString(strings.TrimSpace(comment.Body))
	}
	return sb.String()
}

func commonReviewCommentLogin(comments []github.PullRequestReviewComment) string {
	if len(comments) == 0 {
		return ""
	}
	login := strings.TrimSpace(comments[0].User.Login)
	if login == "" {
		return ""
	}
	for _, comment := range comments[1:] {
		if strings.TrimSpace(comment.User.Login) != login {
			return ""
		}
	}
	return login
}

func reviewCommentLocation(comment github.PullRequestReviewComment) string {
	if comment.Path == "" {
		return ""
	}
	line := reviewCommentLine(comment)
	if line == 0 {
		return comment.Path
	}
	if comment.StartLine != nil && *comment.StartLine != line {
		return fmt.Sprintf("%s:%d-%d", comment.Path, *comment.StartLine, line)
	}
	if comment.OriginalStartLine != nil && *comment.OriginalStartLine != line {
		return fmt.Sprintf("%s:%d-%d", comment.Path, *comment.OriginalStartLine, line)
	}
	return fmt.Sprintf("%s:%d", comment.Path, line)
}

func reviewCommentLine(comment github.PullRequestReviewComment) int {
	if comment.Line != nil {
		return *comment.Line
	}
	if comment.OriginalLine != nil {
		return *comment.OriginalLine
	}
	return 0
}

// resolvePRFromArg parses a PR number or GitHub PR URL from a CLI argument.
func resolvePRFromArg(arg, token string) (*github.PR, string, string, error) {
	// Try parsing as a plain integer
	if num, err := strconv.Atoi(arg); err == nil {
		owner, repo, err := git.RemoteInfo()
		if err != nil {
			return nil, "", "", err
		}
		pr, err := github.GetPR(owner, repo, num, token)
		if err != nil {
			return nil, "", "", fmt.Errorf("failed to fetch PR #%d: %w", num, err)
		}
		return pr, owner, repo, nil
	}

	// Try parsing as a GitHub PR URL
	owner, repo, num, err := parsePRURL(arg)
	if err != nil {
		return nil, "", "", fmt.Errorf("invalid argument %q: expected PR number or GitHub PR URL", arg)
	}
	pr, err := github.GetPR(owner, repo, num, token)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to fetch PR #%d: %w", num, err)
	}
	return pr, owner, repo, nil
}

// resolvePRFromBranch finds an open PR for the current branch.
func resolvePRFromBranch(token string) (*github.PR, string, string, error) {
	branch, err := git.CurrentBranch()
	if err != nil {
		return nil, "", "", err
	}
	owner, repo, err := git.RemoteInfo()
	if err != nil {
		return nil, "", "", err
	}
	pr, err := github.FindPR(owner, repo, branch, token)
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to search for PR: %w", err)
	}
	// pr may be nil if no PR found — caller handles this
	return pr, owner, repo, nil
}

// parsePRURL extracts owner, repo, and PR number from a GitHub PR URL.
// Only public github.com URLs are supported (GitHub Enterprise is not supported).
// Supports URLs like https://github.com/owner/repo/pull/123 (with optional query params).
func parsePRURL(rawURL string) (string, string, int, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", 0, err
	}
	if u.Host != "github.com" {
		return "", "", 0, fmt.Errorf("not a github.com URL")
	}

	// Path: /owner/repo/pull/123
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return "", "", 0, fmt.Errorf("URL path does not match /owner/repo/pull/number")
	}

	num, err := strconv.Atoi(parts[3])
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid PR number in URL: %s", parts[3])
	}

	return parts[0], parts[1], num, nil
}
