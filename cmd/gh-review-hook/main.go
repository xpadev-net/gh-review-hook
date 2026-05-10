package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
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

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) > 1 && os.Args[1] == "install" {
		return runInstall()
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
			return 0
		}
	}

	// Step 4: Wait for CI checks to complete
	ciResult, err := github.WaitForChecks(owner, repo, pr.Head.SHA, token, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 1
	}

	// Detect presence of a Greptile check/context using CI result's seen contexts.
	// If none exists among observed check names/contexts, skip Greptile extraction.
	skipGreptile := false
	hasGreptile := false
	for _, c := range ciResult.SeenContexts {
		if strings.Contains(strings.ToLower(c), "greptile") {
			hasGreptile = true
			break
		}
	}
	if !hasGreptile {
		// No greptile context observed; skip Greptile extraction.
		skipGreptile = true
		fmt.Fprintln(os.Stderr, "[Greptile] no Greptile CI status observed; skipping Greptile description extraction")
	}

	// Step 5: Wait for Greptile to update PR description
	if !skipGreptile {
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
	if !skipGreptile {
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
	allComments := append(issueComments, reviewComments...)

	var codeRabbitPrompts []string
	seenPrompts := make(map[string]bool)
	for _, comment := range allComments {
		commentTime := comment.CreatedAt
		if comment.UpdatedAt.After(commentTime) {
			commentTime = comment.UpdatedAt
		}
		if commentTime.Before(headCommitTime) {
			continue
		}
		p := parser.ExtractCodeRabbitPrompt(comment.Body)
		if p == "" || seenPrompts[p] {
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

	// Step 11: Check for merge conflicts with target branch
	if latestPR.Head.Ref != "" && latestPR.Base.Ref != "" {
		conflictFiles, err := git.ConflictFiles(latestPR.Head.Ref, latestPR.Base.Ref)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: merge conflict check failed: %v\n", err)
		} else if len(conflictFiles) > 0 {
			var sb strings.Builder
			sb.WriteString("Merge conflicts detected with target branch '")
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

	if len(feedbackParts) > 0 {
		fmt.Fprintln(os.Stderr, strings.Join(feedbackParts, "\n---\n"))
		return 2
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
