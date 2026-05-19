package main

import (
	"strings"
	"testing"
	"time"

	"github.com/xpadev-net/gh-review-hook/internal/github"
)

func TestParsePRURL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantOwner string
		wantRepo  string
		wantNum   int
		wantErr   bool
	}{
		{
			name:      "valid PR URL",
			input:     "https://github.com/owner/repo/pull/123",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantNum:   123,
		},
		{
			name:      "PR URL with query params",
			input:     "https://github.com/owner/repo/pull/456?diff=split",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantNum:   456,
		},
		{
			name:    "non-github host",
			input:   "https://gitlab.com/owner/repo/pull/123",
			wantErr: true,
		},
		{
			name:    "missing pull segment",
			input:   "https://github.com/owner/repo/issues/123",
			wantErr: true,
		},
		{
			name:    "non-numeric PR number",
			input:   "https://github.com/owner/repo/pull/abc",
			wantErr: true,
		},
		{
			name:    "too few path segments",
			input:   "https://github.com/owner/repo",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:      "trailing slash",
			input:     "https://github.com/owner/repo/pull/789/",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantNum:   789,
		},
		{
			name:      "trailing path segments (e.g. /files)",
			input:     "https://github.com/owner/repo/pull/101/files",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantNum:   101,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, num, err := parsePRURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got owner=%q repo=%q num=%d", owner, repo, num)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
			}
			if num != tt.wantNum {
				t.Errorf("num = %d, want %d", num, tt.wantNum)
			}
		})
	}
}

func TestActionableReviewCommentsByReviewID_KeepsCurrentRepliesWithTheirOwnReview(t *testing.T) {
	headTime := time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC)
	comments := []github.PullRequestReviewComment{
		newReviewComment(1, 100, "first current comment", headTime.Add(time.Minute)),
		newReviewComment(2, 100, "second current comment", headTime.Add(2*time.Minute)),
		newReviewComment(3, 200, "other review comment", headTime.Add(3*time.Minute)),
		newReviewComment(4, 100, "old comment", headTime.Add(-time.Minute)),
		newReviewComment(5, 100, "skipped comment", headTime.Add(4*time.Minute)),
		newReviewComment(6, 100, "   ", headTime.Add(5*time.Minute)),
		newReviewReply(7, 300, 1, "reply comment", headTime.Add(6*time.Minute)),
		newReviewCommentFrom(8, 100, "bot comment", "greptile-apps[bot]", headTime.Add(7*time.Minute)),
	}

	got := actionableReviewCommentsByReviewID(comments, headTime, map[int64]bool{5: true})

	if len(got[100]) != 2 {
		t.Fatalf("review 100 comments = %d, want 2", len(got[100]))
	}
	if got[100][0].ID != 1 || got[100][1].ID != 2 {
		t.Fatalf("review 100 comment IDs = [%d %d], want [1 2]", got[100][0].ID, got[100][1].ID)
	}
	if len(got[200]) != 1 || got[200][0].ID != 3 {
		t.Fatalf("review 200 comments = %+v, want only ID 3", got[200])
	}
	if len(got[300]) != 1 || got[300][0].ID != 7 {
		t.Fatalf("review 300 comments = %+v, want only reply ID 7", got[300])
	}
}

func TestFormatPullRequestReview_IncludesInlineComments(t *testing.T) {
	review := newPullRequestReview(100, "reviewer-bot", "COMMENTED", "summary body")
	comment := newReviewComment(1, 100, "inline feedback", time.Now())
	comment.Path = "src/example.go"
	comment.StartLine = intPtr(10)
	comment.Line = intPtr(12)

	got := formatPullRequestReview(review, []github.PullRequestReviewComment{comment})

	for _, want := range []string{
		"Review from reviewer-bot (COMMENTED):",
		"summary body",
		"Review comments:",
		"src/example.go:10-12: inline feedback",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted review missing %q:\n%s", want, got)
		}
	}
}

func TestFormatPullRequestReview_UsesCommentsWhenReviewBodyIsEmpty(t *testing.T) {
	review := newPullRequestReview(100, "reviewer-bot", "COMMENTED", "")
	comment := newReviewComment(1, 100, "inline feedback", time.Now())
	comment.Path = "src/example.go"
	comment.OriginalLine = intPtr(20)

	got := formatPullRequestReview(review, []github.PullRequestReviewComment{comment})

	if !strings.Contains(got, "Review from reviewer-bot (COMMENTED):") {
		t.Fatalf("formatted review missing header:\n%s", got)
	}
	if !strings.Contains(got, "src/example.go:20: inline feedback") {
		t.Fatalf("formatted review missing inline comment:\n%s", got)
	}
}

func TestReviewCommentLocation_UsesOriginalRangeForOutdatedComment(t *testing.T) {
	comment := newReviewComment(1, 100, "inline feedback", time.Now())
	comment.Path = "src/example.go"
	comment.OriginalStartLine = intPtr(18)
	comment.OriginalLine = intPtr(20)

	got := reviewCommentLocation(comment)

	if got != "src/example.go:18-20" {
		t.Fatalf("location = %q, want src/example.go:18-20", got)
	}
}

func newPullRequestReview(id int64, login, state, body string) github.PullRequestReview {
	submittedAt := time.Now()
	var review github.PullRequestReview
	review.ID = id
	review.User.Login = login
	review.State = state
	review.Body = body
	review.SubmittedAt = &submittedAt
	return review
}

func newReviewComment(id, reviewID int64, body string, createdAt time.Time) github.PullRequestReviewComment {
	return newReviewCommentFrom(id, reviewID, body, "commenter", createdAt)
}

func newReviewCommentFrom(id, reviewID int64, body, login string, createdAt time.Time) github.PullRequestReviewComment {
	var comment github.PullRequestReviewComment
	comment.ID = id
	comment.PullRequestReviewID = reviewID
	comment.Body = body
	comment.CreatedAt = createdAt
	comment.User.Login = login
	return comment
}

func newReviewReply(id, reviewID, parentID int64, body string, createdAt time.Time) github.PullRequestReviewComment {
	comment := newReviewComment(id, reviewID, body, createdAt)
	comment.InReplyToID = &parentID
	return comment
}

func intPtr(v int) *int {
	return &v
}
