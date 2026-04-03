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

var allowedBotLogins = map[string]struct{}{
	"greptile-apps[bot]": {},
	"greptile-apps":      {},
}

type reviewObservation struct {
	data      parser.ReviewData
	timestamp time.Time
}

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

		_, _, allReviews := inspectCommentsDetailed(comments, time.Time{})
		matchingReview := latestMatchingReview(allReviews, headSHA)
		reviewCurrent := matchingReview != nil
		minTimestamp := headCommitTime
		if !triggerAt.IsZero() && triggerAt.After(minTimestamp) {
			minTimestamp = triggerAt
		}
		_, state, _ := inspectCommentsDetailed(comments, minTimestamp)
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
			return matchingReview, nil
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
	latestReview, state, _ := inspectCommentsDetailed(comments, minTimestamp)
	return latestReview, state
}

func inspectCommentsDetailed(comments []github.IssueComment, minTimestamp time.Time) (*parser.ReviewData, string, []reviewObservation) {
	var (
		latestReviewData   *parser.ReviewData
		latestReviewUpdate time.Time

		latestStatusUpdate time.Time
		status             = reactionStateIdle

		reviews []reviewObservation
	)

	for i := range comments {
		comment := comments[i]
		body := strings.TrimSpace(comment.Body)
		isGreptileActor := isAllowedGreptileActor(comment.User.Login)
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
		reviews = append(reviews, reviewObservation{data: review, timestamp: ts})
		if latestReviewData == nil || ts.After(latestReviewUpdate) {
			reviewCopy := review
			latestReviewData = &reviewCopy
			latestReviewUpdate = ts
		}
	}

	return latestReviewData, status, reviews
}

func commentTimestamp(comment github.IssueComment) time.Time {
	if !comment.UpdatedAt.IsZero() {
		return comment.UpdatedAt
	}
	return comment.CreatedAt
}

func isAllowedGreptileActor(login string) bool {
	_, ok := allowedBotLogins[strings.ToLower(strings.TrimSpace(login))]
	return ok
}

func latestMatchingReview(observations []reviewObservation, headSHA string) *parser.ReviewData {
	var (
		matched *parser.ReviewData
		latest  time.Time
	)
	for i := range observations {
		obs := observations[i]
		if !parser.IsCommitReviewed(headSHA, obs.data.LastReviewedCommit) {
			continue
		}
		if matched == nil || obs.timestamp.After(latest) {
			copy := obs.data
			matched = &copy
			latest = obs.timestamp
		}
	}
	return matched
}
