package greptile

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/xpadev/gh-review-hook/internal/github"
	"github.com/xpadev/gh-review-hook/internal/parser"
)

func TestInspectComments_PrefersLatestGreptileReview(t *testing.T) {
	comments := []github.IssueComment{
		{
			Body:      "<h3>Confidence Score: 2/5</h3><sub>Last reviewed commit: abc1234</sub>",
			UpdatedAt: time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC),
			User: struct {
				Login string `json:"login"`
			}{Login: "greptile-apps[bot]"},
			Reactions: github.CommentReactions{Eyes: 1},
		},
		{
			Body:      "<h3>Confidence Score: 5/5</h3><sub>Last reviewed commit: def5678</sub>",
			UpdatedAt: time.Date(2026, 4, 1, 11, 0, 0, 0, time.UTC),
			User: struct {
				Login string `json:"login"`
			}{Login: "greptile-apps[bot]"},
			Reactions: github.CommentReactions{PlusOne: 1},
		},
	}

	review, state, _ := inspectCommentsDetailed(comments, time.Time{})
	if review == nil {
		t.Fatal("expected latest review")
	}
	if review.LastReviewedCommit != "def5678" {
		t.Errorf("last reviewed commit = %q, want def5678", review.LastReviewedCommit)
	}
	if state != reactionStateReviewed {
		t.Errorf("state = %q, want %q", state, reactionStateReviewed)
	}
}

func TestInspectComments_RejectsSpoofedGreptileLogin(t *testing.T) {
	comments := []github.IssueComment{
		{
			Body:      "<h3>Confidence Score: 5/5</h3><sub>Last reviewed commit: abcdef1</sub>",
			UpdatedAt: time.Date(2026, 4, 1, 11, 0, 0, 0, time.UTC),
			User: struct {
				Login string `json:"login"`
			}{Login: "notgreptile-user"},
			Reactions: github.CommentReactions{PlusOne: 1},
		},
	}

	review, state, _ := inspectCommentsDetailed(comments, time.Time{})
	if review != nil {
		t.Fatalf("expected spoofed actor review to be ignored, got %+v", review)
	}
	if state != reactionStateIdle {
		t.Errorf("state = %q, want %q", state, reactionStateIdle)
	}
}

func TestLatestMatchingReview_SelectsMatchingCommitEvenIfNotLatestOverall(t *testing.T) {
	obs := []reviewObservation{
		{
			data: parser.ReviewData{
				ConfidenceSection:  "<h3>Confidence Score: 5/5</h3>",
				LastReviewedCommit: "abcdef1",
				Found:              true,
			},
			timestamp: time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			data: parser.ReviewData{
				ConfidenceSection:  "<h3>Confidence Score: 3/5</h3>",
				LastReviewedCommit: "deadbee",
				Found:              true,
			},
			timestamp: time.Date(2026, 4, 1, 12, 1, 0, 0, time.UTC),
		},
	}

	got := latestMatchingReview(obs, "abcdef123456")
	if got == nil {
		t.Fatal("expected matching review")
	}
	if got.LastReviewedCommit != "abcdef1" {
		t.Errorf("last reviewed commit = %q, want abcdef1", got.LastReviewedCommit)
	}
}

func TestWaitForReview_TriggersAndReturnsOnMatchingCommit(t *testing.T) {
	originalGet := getPRCommentsFn
	originalCreate := createPRCommentFn
	originalGetCommit := getCommitTimeFn
	originalSleep := sleepFn
	originalNow := nowFn
	t.Cleanup(func() {
		getPRCommentsFn = originalGet
		createPRCommentFn = originalCreate
		getCommitTimeFn = originalGetCommit
		sleepFn = originalSleep
		nowFn = originalNow
	})

	head := "abcdef1234567890"
	step := 0
	var triggerCalls int

	getPRCommentsFn = func(owner, repo string, number int, token string) ([]github.IssueComment, error) {
		step++
		switch step {
		case 1:
			return []github.IssueComment{
				{
					Body: "@greptile review",
					User: struct {
						Login string `json:"login"`
					}{Login: "xpadev"},
					Reactions: github.CommentReactions{Eyes: 0, PlusOne: 0},
				},
			}, nil
		default:
			return []github.IssueComment{
				{
					Body: `<h3>Confidence Score: 4/5</h3>
<sub>Last reviewed commit: abcdef1</sub>`,
					User: struct {
						Login string `json:"login"`
					}{Login: "greptile-apps[bot]"},
					Reactions: github.CommentReactions{Eyes: 0, PlusOne: 1},
					UpdatedAt: time.Date(2026, 4, 1, 13, 0, 0, 0, time.UTC),
				},
			}, nil
		}
	}
	createPRCommentFn = func(owner, repo string, number int, body, token string) (*github.IssueComment, error) {
		triggerCalls++
		return &github.IssueComment{
			ID:        1,
			Body:      body,
			CreatedAt: time.Date(2026, 4, 1, 12, 0, 1, 0, time.UTC),
		}, nil
	}
	getCommitTimeFn = func(owner, repo, sha, token string) (time.Time, error) {
		return time.Date(2026, 4, 1, 11, 59, 59, 0, time.UTC), nil
	}
	sleepFn = func(d time.Duration) {}
	nowTick := 0
	nowFn = func() time.Time {
		nowTick++
		return time.Date(2026, 4, 1, 12, 0, nowTick, 0, time.UTC)
	}

	var logs strings.Builder
	review, err := waitForReview("owner", "repo", 1, head, "token", &logs, time.Millisecond, 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggerCalls != 1 {
		t.Fatalf("trigger calls = %d, want 1", triggerCalls)
	}
	if review == nil {
		t.Fatal("expected review result")
	}
	if review.LastReviewedCommit != "abcdef1" {
		t.Errorf("last reviewed commit = %q, want abcdef1", review.LastReviewedCommit)
	}
	if !strings.Contains(logs.String(), "Triggered review") {
		t.Errorf("expected trigger log, got: %q", logs.String())
	}
}

func TestWaitForReview_DoesNotTriggerWhenAlreadyCurrent(t *testing.T) {
	originalGet := getPRCommentsFn
	originalCreate := createPRCommentFn
	originalGetCommit := getCommitTimeFn
	originalSleep := sleepFn
	originalNow := nowFn
	t.Cleanup(func() {
		getPRCommentsFn = originalGet
		createPRCommentFn = originalCreate
		getCommitTimeFn = originalGetCommit
		sleepFn = originalSleep
		nowFn = originalNow
	})

	getPRCommentsFn = func(owner, repo string, number int, token string) ([]github.IssueComment, error) {
		return []github.IssueComment{
			{
				Body: `<h3>Confidence Score: 5/5</h3>
<sub>Last reviewed commit: abcdef123456</sub>`,
				User: struct {
					Login string `json:"login"`
				}{Login: "greptile-apps[bot]"},
				UpdatedAt: time.Date(2026, 4, 1, 13, 0, 0, 0, time.UTC),
			},
		}, nil
	}
	createPRCommentFn = func(owner, repo string, number int, body, token string) (*github.IssueComment, error) {
		t.Fatal("createPRCommentFn should not be called when latest commit is already reviewed")
		return nil, nil
	}
	getCommitTimeFn = func(owner, repo, sha, token string) (time.Time, error) {
		return time.Date(2026, 4, 1, 11, 59, 59, 0, time.UTC), nil
	}
	sleepFn = func(d time.Duration) {}
	nowFn = func() time.Time { return time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC) }

	review, err := waitForReview("owner", "repo", 1, "abcdef1234567890", "token", nil, time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if review == nil {
		t.Fatal("expected review result")
	}
}

func TestInspectCommentsDetailed_UsesLatestReactionStateFromGreptile(t *testing.T) {
	comments := []github.IssueComment{
		{
			ID:        10,
			Body:      `<h3>Confidence Score: 4/5</h3><sub>Last reviewed commit: abcdef1</sub>`,
			CreatedAt: time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC),
			User: struct {
				Login string `json:"login"`
			}{Login: "greptile-apps[bot]"},
			Reactions: github.CommentReactions{Eyes: 1, PlusOne: 0},
		},
		{
			ID:        11,
			Body:      `<h3>Confidence Score: 4/5</h3><sub>Last reviewed commit: abcdef1</sub>`,
			CreatedAt: time.Date(2026, 4, 1, 10, 2, 0, 0, time.UTC),
			User: struct {
				Login string `json:"login"`
			}{Login: "greptile-apps[bot]"},
			Reactions: github.CommentReactions{Eyes: 0, PlusOne: 1},
		},
	}

	_, state, _ := inspectCommentsDetailed(comments, time.Time{})
	if state != reactionStateReviewed {
		t.Errorf("state = %q, want %q", state, reactionStateReviewed)
	}
}

func TestInspectCommentsDetailedWithTrust_IgnoresUntrustedTriggerComment(t *testing.T) {
	comments := []github.IssueComment{
		{
			ID:        100,
			Body:      "@greptile review",
			CreatedAt: time.Date(2026, 4, 1, 10, 1, 0, 0, time.UTC),
			User: struct {
				Login string `json:"login"`
			}{Login: "untrusted-user"},
			Reactions: github.CommentReactions{Eyes: 1},
		},
	}

	_, state, _ := inspectCommentsDetailedWithTrust(comments, time.Time{}, time.Time{}, 0)
	if state != reactionStateIdle {
		t.Errorf("state = %q, want %q", state, reactionStateIdle)
	}
}

func TestInspectCommentsDetailedWithTrust_UsesTrustedTriggerComment(t *testing.T) {
	comments := []github.IssueComment{
		{
			ID:        100,
			Body:      "@greptile review",
			CreatedAt: time.Date(2026, 4, 1, 10, 1, 0, 0, time.UTC),
			User: struct {
				Login string `json:"login"`
			}{Login: "xpadev"},
			Reactions: github.CommentReactions{Eyes: 1},
		},
	}

	_, state, _ := inspectCommentsDetailedWithTrust(comments, time.Time{}, time.Time{}, 100)
	if state != reactionStateInProgress {
		t.Errorf("state = %q, want %q", state, reactionStateInProgress)
	}
}

func TestInspectCommentsDetailed_IgnoresOldSignalsWithMinTimestamp(t *testing.T) {
	older := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	newer := older.Add(2 * time.Minute)

	comments := []github.IssueComment{
		{
			Body:      `<h3>Confidence Score: 4/5</h3><sub>Last reviewed commit: old1234</sub>`,
			CreatedAt: older,
			User: struct {
				Login string `json:"login"`
			}{Login: "greptile-apps[bot]"},
			Reactions: github.CommentReactions{PlusOne: 1},
		},
		{
			ID:        200,
			Body:      "@greptile review",
			CreatedAt: newer,
			User: struct {
				Login string `json:"login"`
			}{Login: "xpadev"},
			Reactions: github.CommentReactions{Eyes: 1},
		},
	}

	review, state, _ := inspectCommentsDetailedWithTrust(comments, newer, newer, 200)
	if review != nil {
		t.Fatalf("expected no review for minTimestamp filter, got %+v", review)
	}
	if state != reactionStateInProgress {
		t.Errorf("state = %q, want %q", state, reactionStateInProgress)
	}
}

func TestWaitForReview_TriggersWhenNoCurrentReviewDespiteEyesProgress(t *testing.T) {
	originalGet := getPRCommentsFn
	originalCreate := createPRCommentFn
	originalGetCommit := getCommitTimeFn
	originalSleep := sleepFn
	originalNow := nowFn
	t.Cleanup(func() {
		getPRCommentsFn = originalGet
		createPRCommentFn = originalCreate
		getCommitTimeFn = originalGetCommit
		sleepFn = originalSleep
		nowFn = originalNow
	})

	head := "abcdef1234567890"
	step := 0
	var triggerCalls int

	getPRCommentsFn = func(owner, repo string, number int, token string) ([]github.IssueComment, error) {
		step++
		switch step {
		case 1:
			return []github.IssueComment{
				{
					ID: 1,
					Body: `<h3>Confidence Score: 3/5</h3>
<sub>Last reviewed commit: old1234</sub>`,
					User: struct {
						Login string `json:"login"`
					}{Login: "greptile-apps[bot]"},
					CreatedAt: time.Date(2026, 4, 1, 12, 0, 1, 0, time.UTC),
					Reactions: github.CommentReactions{Eyes: 1},
				},
			}, nil
		default:
			return []github.IssueComment{
				{
					Body: `<h3>Confidence Score: 5/5</h3>
<sub>Last reviewed commit: abcdef1</sub>`,
					User: struct {
						Login string `json:"login"`
					}{Login: "greptile-apps[bot]"},
					CreatedAt: time.Date(2026, 4, 1, 12, 0, 2, 0, time.UTC),
					Reactions: github.CommentReactions{PlusOne: 1},
				},
			}, nil
		}
	}
	createPRCommentFn = func(owner, repo string, number int, body, token string) (*github.IssueComment, error) {
		triggerCalls++
		return &github.IssueComment{ID: 1, Body: body, CreatedAt: time.Date(2026, 4, 1, 12, 0, 1, 0, time.UTC)}, nil
	}
	getCommitTimeFn = func(owner, repo, sha, token string) (time.Time, error) {
		return time.Date(2026, 4, 1, 11, 59, 59, 0, time.UTC), nil
	}
	sleepFn = func(d time.Duration) {}
	nowTick := 0
	nowFn = func() time.Time {
		nowTick++
		return time.Date(2026, 4, 1, 12, 0, nowTick, 0, time.UTC)
	}

	review, err := waitForReview("owner", "repo", 1, head, "token", nil, time.Millisecond, 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggerCalls != 1 {
		t.Fatalf("trigger calls = %d, want 1 when no current review exists", triggerCalls)
	}
	if review == nil {
		t.Fatal("expected review result")
	}
}

func TestWaitForReview_TriggersEvenWithStaleEyesFromOldRound(t *testing.T) {
	originalGet := getPRCommentsFn
	originalCreate := createPRCommentFn
	originalGetCommit := getCommitTimeFn
	originalSleep := sleepFn
	originalNow := nowFn
	t.Cleanup(func() {
		getPRCommentsFn = originalGet
		createPRCommentFn = originalCreate
		getCommitTimeFn = originalGetCommit
		sleepFn = originalSleep
		nowFn = originalNow
	})

	head := "abcdef1234567890"
	step := 0
	var triggerCalls int

	getPRCommentsFn = func(owner, repo string, number int, token string) ([]github.IssueComment, error) {
		step++
		switch step {
		case 1:
			return []github.IssueComment{
				{
					Body: `<h3>Confidence Score: 3/5</h3>
<sub>Last reviewed commit: old1111</sub>`,
					User: struct {
						Login string `json:"login"`
					}{Login: "greptile-apps[bot]"},
					CreatedAt: time.Date(2026, 4, 1, 11, 0, 0, 0, time.UTC),
					Reactions: github.CommentReactions{PlusOne: 1},
				},
				{
					ID:   500,
					Body: "@greptile review",
					User: struct {
						Login string `json:"login"`
					}{Login: "xpadev"},
					CreatedAt: time.Date(2026, 4, 1, 11, 1, 0, 0, time.UTC),
					Reactions: github.CommentReactions{Eyes: 1},
				},
			}, nil
		default:
			return []github.IssueComment{
				{
					Body: `<h3>Confidence Score: 4/5</h3>
<sub>Last reviewed commit: abcdef1</sub>`,
					User: struct {
						Login string `json:"login"`
					}{Login: "greptile-apps[bot]"},
					CreatedAt: time.Date(2026, 4, 1, 12, 0, 2, 0, time.UTC),
					Reactions: github.CommentReactions{PlusOne: 1},
				},
			}, nil
		}
	}
	createPRCommentFn = func(owner, repo string, number int, body, token string) (*github.IssueComment, error) {
		triggerCalls++
		return &github.IssueComment{
			ID:        99,
			Body:      body,
			CreatedAt: time.Date(2026, 4, 1, 12, 0, 1, 0, time.UTC),
		}, nil
	}
	getCommitTimeFn = func(owner, repo, sha, token string) (time.Time, error) {
		return time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC), nil
	}
	sleepFn = func(d time.Duration) {}
	nowTick := 0
	nowFn = func() time.Time {
		nowTick++
		return time.Date(2026, 4, 1, 12, 0, nowTick, 0, time.UTC)
	}

	review, err := waitForReview("owner", "repo", 1, head, "token", nil, time.Millisecond, 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggerCalls != 1 {
		t.Fatalf("trigger calls = %d, want 1", triggerCalls)
	}
	if review == nil {
		t.Fatal("expected review result")
	}
	if review.LastReviewedCommit != "abcdef1" {
		t.Errorf("last reviewed commit = %q, want abcdef1", review.LastReviewedCommit)
	}
}

func TestWaitForReview_TriggersDespiteUntrustedEyesSignal(t *testing.T) {
	originalGet := getPRCommentsFn
	originalCreate := createPRCommentFn
	originalGetCommit := getCommitTimeFn
	originalSleep := sleepFn
	originalNow := nowFn
	t.Cleanup(func() {
		getPRCommentsFn = originalGet
		createPRCommentFn = originalCreate
		getCommitTimeFn = originalGetCommit
		sleepFn = originalSleep
		nowFn = originalNow
	})

	head := "abcdef1234567890"
	step := 0
	var triggerCalls int

	getPRCommentsFn = func(owner, repo string, number int, token string) ([]github.IssueComment, error) {
		step++
		switch step {
		case 1:
			return []github.IssueComment{
				{
					ID:   777,
					Body: "@greptile review",
					User: struct {
						Login string `json:"login"`
					}{Login: "someone-else"},
					CreatedAt: time.Date(2026, 4, 1, 12, 0, 1, 0, time.UTC),
					Reactions: github.CommentReactions{Eyes: 1},
				},
			}, nil
		default:
			return []github.IssueComment{
				{
					Body: `<h3>Confidence Score: 4/5</h3>
<sub>Last reviewed commit: abcdef1</sub>`,
					User: struct {
						Login string `json:"login"`
					}{Login: "greptile-apps[bot]"},
					CreatedAt: time.Date(2026, 4, 1, 12, 0, 2, 0, time.UTC),
					Reactions: github.CommentReactions{PlusOne: 1},
				},
			}, nil
		}
	}
	createPRCommentFn = func(owner, repo string, number int, body, token string) (*github.IssueComment, error) {
		triggerCalls++
		return &github.IssueComment{
			ID:        999,
			Body:      body,
			CreatedAt: time.Date(2026, 4, 1, 12, 0, 1, 500000000, time.UTC),
		}, nil
	}
	getCommitTimeFn = func(owner, repo, sha, token string) (time.Time, error) {
		return time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC), nil
	}
	sleepFn = func(d time.Duration) {}
	nowTick := 0
	nowFn = func() time.Time {
		nowTick++
		return time.Date(2026, 4, 1, 12, 0, nowTick, 0, time.UTC)
	}

	review, err := waitForReview("owner", "repo", 1, head, "token", nil, time.Millisecond, 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggerCalls != 1 {
		t.Fatalf("trigger calls = %d, want 1", triggerCalls)
	}
	if review == nil {
		t.Fatal("expected review result")
	}
}

func TestWaitForReview_TriggersBeforeGreptileBotAppears(t *testing.T) {
	originalGet := getPRCommentsFn
	originalCreate := createPRCommentFn
	originalGetCommit := getCommitTimeFn
	originalSleep := sleepFn
	originalNow := nowFn
	t.Cleanup(func() {
		getPRCommentsFn = originalGet
		createPRCommentFn = originalCreate
		getCommitTimeFn = originalGetCommit
		sleepFn = originalSleep
		nowFn = originalNow
	})

	head := "abcdef1234567890"
	step := 0
	var triggerCalls int

	getPRCommentsFn = func(owner, repo string, number int, token string) ([]github.IssueComment, error) {
		step++
		switch step {
		case 1:
			return []github.IssueComment{
				{
					ID:   1,
					Body: "@greptile review",
					User: struct {
						Login string `json:"login"`
					}{Login: "xpadev"},
					CreatedAt: time.Date(2026, 4, 1, 12, 0, 1, 0, time.UTC),
					Reactions: github.CommentReactions{Eyes: 1},
				},
			}, nil
		default:
			return []github.IssueComment{
				{
					Body: `<h3>Confidence Score: 5/5</h3>
<sub>Last reviewed commit: abcdef1</sub>`,
					User: struct {
						Login string `json:"login"`
					}{Login: "greptile-apps[bot]"},
					CreatedAt: time.Date(2026, 4, 1, 12, 0, 2, 0, time.UTC),
					Reactions: github.CommentReactions{PlusOne: 1},
				},
			}, nil
		}
	}
	createPRCommentFn = func(owner, repo string, number int, body, token string) (*github.IssueComment, error) {
		triggerCalls++
		return &github.IssueComment{ID: 1, Body: body, CreatedAt: time.Date(2026, 4, 1, 12, 0, 1, 0, time.UTC)}, nil
	}
	getCommitTimeFn = func(owner, repo, sha, token string) (time.Time, error) {
		return time.Date(2026, 4, 1, 11, 59, 59, 0, time.UTC), nil
	}
	sleepFn = func(d time.Duration) {}
	nowTick := 0
	nowFn = func() time.Time {
		nowTick++
		return time.Date(2026, 4, 1, 12, 0, nowTick, 0, time.UTC)
	}

	review, err := waitForReview("owner", "repo", 1, head, "token", nil, time.Millisecond, 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if triggerCalls != 1 {
		t.Fatalf("trigger calls = %d, want 1 when no trusted in-progress signal exists", triggerCalls)
	}
	if review == nil {
		t.Fatal("expected review result")
	}
}

func TestCommentStateTimestamp_PrefersCreatedAt(t *testing.T) {
	created := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	updated := created.Add(10 * time.Minute)
	got := commentStateTimestamp(github.IssueComment{
		CreatedAt: created,
		UpdatedAt: updated,
	})
	if !got.Equal(created) {
		t.Fatalf("timestamp = %s, want created_at %s", got, created)
	}
}

func TestCommentReviewTimestamp_PrefersUpdatedAtWhenEdited(t *testing.T) {
	created := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	updated := created.Add(10 * time.Minute)
	got := commentReviewTimestamp(github.IssueComment{
		CreatedAt: created,
		UpdatedAt: updated,
	})
	if !got.Equal(updated) {
		t.Fatalf("timestamp = %s, want updated_at %s", got, updated)
	}
}

func TestWaitForReview_HeadCommitTimeFailure(t *testing.T) {
	originalGet := getPRCommentsFn
	originalCreate := createPRCommentFn
	originalGetCommit := getCommitTimeFn
	originalSleep := sleepFn
	originalNow := nowFn
	t.Cleanup(func() {
		getPRCommentsFn = originalGet
		createPRCommentFn = originalCreate
		getCommitTimeFn = originalGetCommit
		sleepFn = originalSleep
		nowFn = originalNow
	})

	getCommitTimeFn = func(owner, repo, sha, token string) (time.Time, error) {
		return time.Time{}, fmt.Errorf("boom")
	}
	getPRCommentsFn = func(owner, repo string, number int, token string) ([]github.IssueComment, error) {
		t.Fatal("getPRCommentsFn should not be called when commit timestamp fetch fails")
		return nil, nil
	}
	createPRCommentFn = func(owner, repo string, number int, body, token string) (*github.IssueComment, error) {
		t.Fatal("createPRCommentFn should not be called when commit timestamp fetch fails")
		return nil, nil
	}
	sleepFn = func(d time.Duration) {}
	nowFn = func() time.Time { return time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC) }

	_, err := waitForReview("owner", "repo", 1, "abcdef123456", "token", nil, time.Millisecond, time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to fetch head commit timestamp") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForReviewInPRBody_ReturnsMatchingDescriptionReview(t *testing.T) {
	originalGetPR := getPRFn
	originalSleep := sleepFn
	originalNow := nowFn
	t.Cleanup(func() {
		getPRFn = originalGetPR
		sleepFn = originalSleep
		nowFn = originalNow
	})

	getPRFn = func(owner, repo string, number int, token string) (*github.PR, error) {
		pr := &github.PR{}
		pr.Body = `<!-- greptile_comment -->
<h3>Confidence Score: 4/5</h3>
Needs updates.
<details><summary>Prompt To Fix All With AI</summary>Fix it</details>
<sub>Last reviewed commit: abcdef1</sub>
<!-- /greptile_comment -->`
		return pr, nil
	}
	sleepFn = func(d time.Duration) {}
	nowFn = func() time.Time { return time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC) }

	review, err := waitForReviewInPRBody("owner", "repo", 1, "abcdef1234567890", "token", nil, time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if review == nil || !review.Found {
		t.Fatal("expected review result")
	}
	if review.LastReviewedCommit != "abcdef1" {
		t.Fatalf("last reviewed commit = %q, want %q", review.LastReviewedCommit, "abcdef1")
	}
}

func TestWaitForReviewInPRBody_TimesOutWhenNoMatchingCommit(t *testing.T) {
	originalGetPR := getPRFn
	originalSleep := sleepFn
	originalNow := nowFn
	t.Cleanup(func() {
		getPRFn = originalGetPR
		sleepFn = originalSleep
		nowFn = originalNow
	})

	getPRFn = func(owner, repo string, number int, token string) (*github.PR, error) {
		pr := &github.PR{}
		pr.Body = `<!-- greptile_comment -->
<h3>Confidence Score: 5/5</h3>
<sub>Last reviewed commit: deadbee</sub>
<!-- /greptile_comment -->`
		return pr, nil
	}
	sleepFn = func(d time.Duration) {}
	tick := 0
	nowFn = func() time.Time {
		tick++
		return time.Date(2026, 4, 1, 12, 0, tick, 0, time.UTC)
	}

	_, err := waitForReviewInPRBody("owner", "repo", 1, "abcdef1234567890", "token", nil, time.Millisecond, 2*time.Second)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, ErrReviewTimeout) {
		t.Fatalf("expected ErrReviewTimeout, got %v", err)
	}
}

func TestWaitForReviewInPRBody_FetchesPRImmediately(t *testing.T) {
	originalGetPR := getPRFn
	originalSleep := sleepFn
	originalNow := nowFn
	t.Cleanup(func() {
		getPRFn = originalGetPR
		sleepFn = originalSleep
		nowFn = originalNow
	})

	getPRCalls := 0
	getPRFn = func(owner, repo string, number int, token string) (*github.PR, error) {
		getPRCalls++
		pr := &github.PR{}
		pr.Body = `<!-- greptile_comment -->
<h3>Confidence Score: 4/5</h3>
<sub>Last reviewed commit: abcdef1</sub>
<!-- /greptile_comment -->`
		return pr, nil
	}
	sleepFn = func(d time.Duration) {}
	nowFn = func() time.Time { return time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC) }

	review, err := waitForReviewInPRBody("owner", "repo", 1, "abcdef1234567890", "token", nil, time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if review == nil || !review.Found {
		t.Fatal("expected review result from fetched PR body")
	}
	if getPRCalls != 1 {
		t.Fatalf("getPRFn called %d times, want 1 immediate fetch", getPRCalls)
	}
}

func TestWaitForReview_ReusesExistingTriggerComment(t *testing.T) {
	originalGet := getPRCommentsFn
	originalCreate := createPRCommentFn
	originalGetCommit := getCommitTimeFn
	originalSleep := sleepFn
	originalNow := nowFn
	t.Cleanup(func() {
		getPRCommentsFn = originalGet
		createPRCommentFn = originalCreate
		getCommitTimeFn = originalGetCommit
		sleepFn = originalSleep
		nowFn = originalNow
	})

	head := "abcdef1234567890"
	step := 0
	var triggerCalls int

	getPRCommentsFn = func(owner, repo string, number int, token string) ([]github.IssueComment, error) {
		step++
		switch step {
		case 1:
			return []github.IssueComment{
				{
					ID:        123,
					Body:      "@greptile review",
					CreatedAt: time.Date(2026, 4, 1, 12, 0, 1, 0, time.UTC),
					User: struct {
						Login string `json:"login"`
					}{Login: "greptile-apps[bot]"},
					Reactions: github.CommentReactions{Eyes: 1},
				},
			}, nil
		default:
			return []github.IssueComment{
				{
					ID:        123,
					Body:      "@greptile review",
					CreatedAt: time.Date(2026, 4, 1, 12, 0, 1, 0, time.UTC),
					User: struct {
						Login string `json:"login"`
					}{Login: "greptile-apps[bot]"},
					Reactions: github.CommentReactions{PlusOne: 1},
				},
				{
					Body: `<h3>Confidence Score: 5/5</h3>
<sub>Last reviewed commit: abcdef1</sub>`,
					CreatedAt: time.Date(2026, 4, 1, 12, 0, 2, 0, time.UTC),
					User: struct {
						Login string `json:"login"`
					}{Login: "greptile-apps[bot]"},
					Reactions: github.CommentReactions{PlusOne: 1},
				},
			}, nil
		}
	}
	createPRCommentFn = func(owner, repo string, number int, body, token string) (*github.IssueComment, error) {
		triggerCalls++
		return &github.IssueComment{ID: 999, Body: body}, nil
	}
	getCommitTimeFn = func(owner, repo, sha, token string) (time.Time, error) {
		return time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC), nil
	}
	sleepFn = func(d time.Duration) {}
	nowTick := 0
	nowFn = func() time.Time {
		nowTick++
		return time.Date(2026, 4, 1, 12, 0, nowTick, 0, time.UTC)
	}

	review, err := waitForReview("owner", "repo", 1, head, "token", nil, time.Millisecond, 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if review == nil {
		t.Fatal("expected review result")
	}
	if triggerCalls != 0 {
		t.Fatalf("trigger calls = %d, want 0 when existing trigger comment is present", triggerCalls)
	}
}
