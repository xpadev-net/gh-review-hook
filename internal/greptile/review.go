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
	var trustedTriggerCommentID int64
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

		minStateTimestamp := headCommitTime
		if !triggerAt.IsZero() && triggerAt.After(minStateTimestamp) {
			minStateTimestamp = triggerAt
		}
		_, state, reviewCandidates := inspectCommentsDetailedWithTrust(comments, headCommitTime, minStateTimestamp, trustedTriggerCommentID)
		matchingReview := latestMatchingReview(reviewCandidates, headSHA)
		reviewCurrent := matchingReview != nil
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

		if !triggered && !reviewCurrent {
			posted, err := createPRCommentFn(owner, repo, prNumber, triggerCommentBody, token)
			if err != nil {
				return nil, fmt.Errorf("failed to trigger Greptile review: %w", err)
			}
			triggered = true
			triggerAt = headCommitTime
			if posted != nil {
				trustedTriggerCommentID = posted.ID
				if postedAt := commentStateTimestamp(*posted); !postedAt.IsZero() {
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
	return inspectCommentsDetailedWithTrust(comments, minTimestamp, minTimestamp, 0)
}

func inspectCommentsDetailedWithTrust(comments []github.IssueComment, minReviewTimestamp, minStateTimestamp time.Time, trustedTriggerCommentID int64) (*parser.ReviewData, string, []reviewObservation) {
	var (
		latestReviewData   *parser.ReviewData
		latestReviewUpdate time.Time

		latestStatusUpdate time.Time
		status             = reactionStateIdle

		reviews []reviewObservation
	)

	for i := range comments {
		comment := comments[i]
		isGreptileActor := isAllowedGreptileActor(comment.User.Login)
		isTrustedTriggerComment := trustedTriggerCommentID != 0 && comment.ID == trustedTriggerCommentID
		stateTS := commentStateTimestamp(comment)

		if (isGreptileActor || isTrustedTriggerComment) && (minStateTimestamp.IsZero() || !stateTS.Before(minStateTimestamp)) {
			if stateTS.After(latestStatusUpdate) || latestStatusUpdate.IsZero() {
				latestStatusUpdate = stateTS
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
		reviewTS := commentReviewTimestamp(comment)
		if !minReviewTimestamp.IsZero() && reviewTS.Before(minReviewTimestamp) {
			continue
		}
		reviews = append(reviews, reviewObservation{data: review, timestamp: reviewTS})
		if latestReviewData == nil || reviewTS.After(latestReviewUpdate) {
			reviewCopy := review
			latestReviewData = &reviewCopy
			latestReviewUpdate = reviewTS
		}
	}

	return latestReviewData, status, reviews
}

func commentStateTimestamp(comment github.IssueComment) time.Time {
	if !comment.CreatedAt.IsZero() {
		return comment.CreatedAt
	}
	return comment.UpdatedAt
}

func commentReviewTimestamp(comment github.IssueComment) time.Time {
	if comment.UpdatedAt.After(comment.CreatedAt) {
		return comment.UpdatedAt
	}
	return commentStateTimestamp(comment)
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
