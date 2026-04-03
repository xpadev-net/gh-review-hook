package greptile

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/xpadev/gh-review-hook/internal/github"
	"github.com/xpadev/gh-review-hook/internal/parser"
)

const (
	reviewPollInterval = 15 * time.Second
	reviewPollTimeout  = 30 * time.Minute
	triggerCommentBody = "@greptile review"
)

const (
	reactionStateIdle       = "idle"
	reactionStateInProgress = "in_progress"
	reactionStateReviewed   = "reviewed"
)

var (
	getPRCommentsFn   = github.GetPRComments
	createPRCommentFn = github.CreatePRComment
	getCommitTimeFn   = github.GetCommitTimestamp
	sleepFn           = time.Sleep
	nowFn             = time.Now
)

// WaitForReview waits until a Greptile review comment exists for the current
// HEAD commit. If no up-to-date review exists, it triggers one by posting
// "@greptile review" once and then polls PR comments/reactions.
func WaitForReview(owner, repo string, prNumber int, headSHA, token string, logw io.Writer) (*parser.ReviewData, error) {
	return waitForReview(owner, repo, prNumber, headSHA, token, logw, reviewPollInterval, reviewPollTimeout)
}

func waitForReview(owner, repo string, prNumber int, headSHA, token string, logw io.Writer, pollInterval, pollTimeout time.Duration) (*parser.ReviewData, error) {
	logf := func(format string, a ...any) {
		if logw != nil {
			fmt.Fprintf(logw, format, a...)
		}
	}

	start := nowFn()
	triggered := false
	var triggerAt time.Time
	headCommitTime, err := getCommitTimeFn(owner, repo, headSHA, token)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch head commit timestamp: %w", err)
	}
	prevState := ""

	for {
		if nowFn().Sub(start) > pollTimeout {
			return nil, fmt.Errorf("timed out after %s waiting for Greptile review on latest commit", pollTimeout)
		}

		comments, err := getPRCommentsFn(owner, repo, prNumber, token)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch PR comments: %w", err)
		}

		latestReview, _ := inspectComments(comments, time.Time{})
		reviewCurrent := latestReview != nil && parser.IsCommitReviewed(headSHA, latestReview.LastReviewedCommit)
		minTimestamp := headCommitTime
		if !triggerAt.IsZero() && triggerAt.After(minTimestamp) {
			minTimestamp = triggerAt
		}
		_, state := inspectComments(comments, minTimestamp)
		if state != prevState {
			switch state {
			case reactionStateInProgress:
				logf("[Greptile] status: eyes (review in progress)\n")
			case reactionStateReviewed:
				logf("[Greptile] status: +1 (review completed signal)\n")
			}
			prevState = state
		}

		if reviewCurrent {
			return latestReview, nil
		}

		if !triggered && !reviewCurrent && state != reactionStateInProgress {
			posted, err := createPRCommentFn(owner, repo, prNumber, triggerCommentBody, token)
			if err != nil {
				return nil, fmt.Errorf("failed to trigger Greptile review: %w", err)
			}
			triggered = true
			triggerAt = headCommitTime
			if posted != nil {
				if postedAt := commentTimestamp(*posted); !postedAt.IsZero() {
					triggerAt = postedAt
				}
			}
			prevState = ""
			logf("[Greptile] Triggered review via comment: %s\n", triggerCommentBody)
		}

		sleepFn(pollInterval)
	}
}

func inspectComments(comments []github.IssueComment, minTimestamp time.Time) (*parser.ReviewData, string) {
	var (
		latestReviewData   *parser.ReviewData
		latestReviewUpdate time.Time

		latestStatusUpdate time.Time
		status             = reactionStateIdle
	)

	for i := range comments {
		comment := comments[i]
		login := strings.ToLower(comment.User.Login)
		body := strings.TrimSpace(comment.Body)
		isGreptileActor := strings.Contains(login, "greptile")
		isTriggerComment := strings.EqualFold(body, triggerCommentBody)
		ts := commentTimestamp(comment)
		if !minTimestamp.IsZero() && ts.Before(minTimestamp) {
			continue
		}

		if isGreptileActor || isTriggerComment {
			if ts.After(latestStatusUpdate) || latestStatusUpdate.IsZero() {
				latestStatusUpdate = ts
				status = reactionStateIdle
				if comment.Reactions.Eyes > 0 {
					status = reactionStateInProgress
				}
				if comment.Reactions.PlusOne > 0 {
					status = reactionStateReviewed
				}
			}
		}

		if !isGreptileActor {
			continue
		}
		review := parser.ExtractGreptileReviewComment(comment.Body)
		if !review.Found {
			continue
		}
		if latestReviewData == nil || ts.After(latestReviewUpdate) {
			reviewCopy := review
			latestReviewData = &reviewCopy
			latestReviewUpdate = ts
		}
	}

	return latestReviewData, status
}

func commentTimestamp(comment github.IssueComment) time.Time {
	if !comment.UpdatedAt.IsZero() {
		return comment.UpdatedAt
	}
	return comment.CreatedAt
}
