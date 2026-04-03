package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDeduplicateStatuses(t *testing.T) {
	tests := []struct {
		name  string
		input []CommitStatus
		want  []CommitStatus
	}{
		{
			name:  "empty slice",
			input: []CommitStatus{},
			want:  nil,
		},
		{
			name:  "single status",
			input: []CommitStatus{{State: "success", Context: "ci/build"}},
			want:  []CommitStatus{{State: "success", Context: "ci/build"}},
		},
		{
			name: "duplicate contexts keeps first",
			input: []CommitStatus{
				{State: "success", Context: "ci/build"},
				{State: "failure", Context: "ci/build"},
			},
			want: []CommitStatus{{State: "success", Context: "ci/build"}},
		},
		{
			name: "multiple distinct contexts",
			input: []CommitStatus{
				{State: "success", Context: "ci/build"},
				{State: "pending", Context: "ci/lint"},
			},
			want: []CommitStatus{
				{State: "success", Context: "ci/build"},
				{State: "pending", Context: "ci/lint"},
			},
		},
		{
			name: "mixed duplicates",
			input: []CommitStatus{
				{State: "success", Context: "ci/build"},
				{State: "pending", Context: "ci/lint"},
				{State: "failure", Context: "ci/build"},
				{State: "error", Context: "ci/lint"},
			},
			want: []CommitStatus{
				{State: "success", Context: "ci/build"},
				{State: "pending", Context: "ci/lint"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deduplicateStatuses(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("index %d: got %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// apiBaseMu guards mutations of the package-level apiBase variable.
// Tests must not use t.Parallel() when calling withTestServer.
var apiBaseMu sync.Mutex

// withTestServer sets apiBase to the test server URL and restores it on cleanup.
func withTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(handler)
	apiBaseMu.Lock()
	originalBase := apiBase
	apiBase = ts.URL
	t.Cleanup(func() {
		apiBase = originalBase
		ts.Close()
		apiBaseMu.Unlock()
	})
	return ts
}

func TestApiGet_Success(t *testing.T) {
	type testPayload struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	var gotAuth, gotAccept string
	mux := http.NewServeMux()
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testPayload{Name: "hello", Value: 42})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	var dest testPayload
	err := apiGet(ts.URL+"/test", "my-token", &dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotAuth != "Bearer my-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer my-token")
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q, want %q", gotAccept, "application/vnd.github+json")
	}
	if dest.Name != "hello" || dest.Value != 42 {
		t.Errorf("decoded = %+v, want {Name:hello Value:42}", dest)
	}
}

func TestApiGet_HTTPError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	var dest struct{}
	err := apiGet(ts.URL+"/missing", "token", &dest)
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestApiGet_InvalidJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "not json")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	var dest struct{ Name string }
	err := apiGet(ts.URL+"/bad", "token", &dest)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestApiPost_Success(t *testing.T) {
	type requestBody struct {
		Body string `json:"body"`
	}
	type responseBody struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}

	var (
		gotMethod      string
		gotAuth        string
		gotAccept      string
		gotContentType string
		gotPayload     requestBody
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotPayload)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(responseBody{ID: 123, Body: "ok"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	var dest responseBody
	err := apiPost(ts.URL+"/test", "my-token", strings.NewReader(`{"body":"hello"}`), &dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want %q", gotMethod, http.MethodPost)
	}
	if gotAuth != "Bearer my-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer my-token")
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept = %q, want %q", gotAccept, "application/vnd.github+json")
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", gotContentType, "application/json")
	}
	if gotPayload.Body != "hello" {
		t.Errorf("payload body = %q, want %q", gotPayload.Body, "hello")
	}
	if dest.ID != 123 || dest.Body != "ok" {
		t.Errorf("decoded = %+v, want {ID:123 Body:ok}", dest)
	}
}

func TestApiPost_HTTPError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	var dest struct{}
	err := apiPost(ts.URL+"/bad", "token", strings.NewReader(`{}`), &dest)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

func TestFindPR(t *testing.T) {
	tests := []struct {
		name       string
		response   string
		statusCode int
		wantNil    bool
		wantNumber int
		wantErr    bool
	}{
		{
			name:       "found PR",
			response:   `[{"number":42,"body":"test","head":{"sha":"abc","ref":"feat"}}]`,
			statusCode: 200,
			wantNumber: 42,
		},
		{
			name:       "no PRs",
			response:   `[]`,
			statusCode: 200,
			wantNil:    true,
		},
		{
			name:       "API error",
			response:   `{"message":"error"}`,
			statusCode: 500,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.String()
				w.WriteHeader(tt.statusCode)
				fmt.Fprint(w, tt.response)
			})
			withTestServer(t, mux)

			pr, err := FindPR("myowner", "myrepo", "feat-branch", "token")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Verify URL for all non-error cases (found and not-found use the same URL)
			if !strings.HasPrefix(gotPath, "/repos/myowner/myrepo/pulls") {
				t.Errorf("request path = %q, want prefix /repos/myowner/myrepo/pulls", gotPath)
			}
			if !strings.Contains(gotPath, "head=myowner:feat-branch") {
				t.Errorf("request URL %q missing head=myowner:feat-branch query param", gotPath)
			}
			if !strings.Contains(gotPath, "state=open") {
				t.Errorf("request URL %q missing state=open query param", gotPath)
			}
			if tt.wantNil {
				if pr != nil {
					t.Fatalf("expected nil PR, got %+v", pr)
				}
				return
			}
			if pr.Number != tt.wantNumber {
				t.Errorf("PR number = %d, want %d", pr.Number, tt.wantNumber)
			}
		})
	}
}

func TestGetPR(t *testing.T) {
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"number":99,"body":"pr body","head":{"sha":"def456","ref":"my-branch"}}`)
	})
	withTestServer(t, mux)

	pr, err := GetPR("owner", "repo", 99, "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr.Number != 99 {
		t.Errorf("PR number = %d, want 99", pr.Number)
	}
	if pr.Body != "pr body" {
		t.Errorf("PR body = %q, want %q", pr.Body, "pr body")
	}
	if pr.Head.SHA != "def456" {
		t.Errorf("PR head SHA = %q, want %q", pr.Head.SHA, "def456")
	}
	if gotPath != "/repos/owner/repo/pulls/99" {
		t.Errorf("request path = %q, want %q", gotPath, "/repos/owner/repo/pulls/99")
	}
}

func TestGetPRComments_SinglePage(t *testing.T) {
	callCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if want := "/repos/owner/repo/issues/42/comments"; r.URL.Path != want {
			t.Errorf("request path = %q, want %q", r.URL.Path, want)
		}
		w.Header().Set("Content-Type", "application/json")
		callCount++
		if callCount == 1 {
			fmt.Fprint(w, `[{"id":1,"body":"first","user":{"login":"greptile-apps[bot]"},"reactions":{"+1":1,"eyes":0},"created_at":"2026-04-01T13:00:00Z","updated_at":"2026-04-01T13:01:00Z"}]`)
			return
		}
		fmt.Fprint(w, `[]`)
	})
	withTestServer(t, mux)

	comments, err := GetPRComments("owner", "repo", 42, "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("got %d comments, want 1", len(comments))
	}
	if comments[0].ID != 1 {
		t.Errorf("comments[0].ID = %d, want 1", comments[0].ID)
	}
	if comments[0].User.Login != "greptile-apps[bot]" {
		t.Errorf("comments[0].User.Login = %q, want %q", comments[0].User.Login, "greptile-apps[bot]")
	}
	if comments[0].Reactions.PlusOne != 1 {
		t.Errorf("comments[0].Reactions.PlusOne = %d, want 1", comments[0].Reactions.PlusOne)
	}
}

func TestGetPRComments_Pagination(t *testing.T) {
	var pages []string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if want := "/repos/owner/repo/issues/42/comments"; r.URL.Path != want {
			t.Errorf("request path = %q, want %q", r.URL.Path, want)
		}
		pages = append(pages, r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		if len(pages) == 1 {
			fmt.Fprint(w, `[{"id":1,"body":"page1","user":{"login":"a"},"reactions":{"+1":0,"eyes":1},"created_at":"2026-04-01T13:00:00Z","updated_at":"2026-04-01T13:00:00Z"}]`)
		} else {
			fmt.Fprint(w, `[]`)
		}
	})
	withTestServer(t, mux)

	comments, err := GetPRComments("owner", "repo", 42, "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("got %d comments, want 1", len(comments))
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 API calls for pagination, got %d", len(pages))
	}
	if pages[0] != "1" {
		t.Errorf("first request page = %q, want %q", pages[0], "1")
	}
	if pages[1] != "2" {
		t.Errorf("second request page = %q, want %q", pages[1], "2")
	}
}

func TestCreatePRComment(t *testing.T) {
	var (
		gotPath    string
		gotMethod  string
		gotPayload string
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		body, _ := io.ReadAll(r.Body)
		gotPayload = string(body)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":99,"body":"@greptile review","user":{"login":"xpadev"},"reactions":{"+1":0,"eyes":0},"created_at":"2026-04-01T13:00:00Z","updated_at":"2026-04-01T13:00:00Z"}`)
	})
	withTestServer(t, mux)

	comment, err := CreatePRComment("owner", "repo", 42, "@greptile review", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/repos/owner/repo/issues/42/comments" {
		t.Errorf("request path = %q, want %q", gotPath, "/repos/owner/repo/issues/42/comments")
	}
	if gotMethod != http.MethodPost {
		t.Errorf("request method = %q, want %q", gotMethod, http.MethodPost)
	}
	if !strings.Contains(gotPayload, `"body":"@greptile review"`) {
		t.Errorf("request payload = %q, expected JSON body field", gotPayload)
	}
	if comment.ID != 99 {
		t.Errorf("comment.ID = %d, want 99", comment.ID)
	}
	if comment.Body != "@greptile review" {
		t.Errorf("comment.Body = %q, want %q", comment.Body, "@greptile review")
	}
}

func TestGetCommitTimestamp(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if want := "/repos/owner/repo/commits/sha123"; r.URL.Path != want {
			t.Errorf("request path = %q, want %q", r.URL.Path, want)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"commit":{"author":{"date":"2026-04-01T10:00:00Z"},"committer":{"date":"2026-04-01T10:01:00Z"}}}`)
	})
	withTestServer(t, mux)

	ts, err := GetCommitTimestamp("owner", "repo", "sha123", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := ts.Format(time.RFC3339), "2026-04-01T10:01:00Z"; got != want {
		t.Errorf("timestamp = %q, want %q", got, want)
	}
}

func TestGetCheckRuns_SinglePage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if want := "/repos/owner/repo/commits/sha123/check-runs"; r.URL.Path != want {
			t.Errorf("request path = %q, want %q", r.URL.Path, want)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"total_count":2,"check_runs":[{"name":"build","status":"completed","conclusion":"success"},{"name":"lint","status":"completed","conclusion":"failure"}]}`)
	})
	withTestServer(t, mux)

	runs, err := GetCheckRuns("owner", "repo", "sha123", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d check runs, want 2", len(runs))
	}
	if runs[0].Name != "build" {
		t.Errorf("runs[0].Name = %q, want %q", runs[0].Name, "build")
	}
	if runs[1].Conclusion != "failure" {
		t.Errorf("runs[1].Conclusion = %q, want %q", runs[1].Conclusion, "failure")
	}
}

func TestGetCheckRuns_Pagination(t *testing.T) {
	var pages []string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if want := "/repos/owner/repo/commits/sha123/check-runs"; r.URL.Path != want {
			t.Errorf("request path = %q, want %q", r.URL.Path, want)
		}
		pages = append(pages, r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		if len(pages) == 1 {
			fmt.Fprint(w, `{"total_count":3,"check_runs":[{"name":"a","status":"completed","conclusion":"success"},{"name":"b","status":"completed","conclusion":"success"}]}`)
		} else {
			fmt.Fprint(w, `{"total_count":3,"check_runs":[{"name":"c","status":"completed","conclusion":"success"}]}`)
		}
	})
	withTestServer(t, mux)

	runs, err := GetCheckRuns("owner", "repo", "sha123", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d check runs, want 3", len(runs))
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 API calls for pagination, got %d", len(pages))
	}
	if pages[0] != "1" {
		t.Errorf("first request page = %q, want %q", pages[0], "1")
	}
	if pages[1] != "2" {
		t.Errorf("second request page = %q, want %q", pages[1], "2")
	}
}

func TestGetCheckRuns_TerminatesOnEmptyPage(t *testing.T) {
	callCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if want := "/repos/owner/repo/commits/sha123/check-runs"; r.URL.Path != want {
			t.Errorf("request path = %q, want %q", r.URL.Path, want)
		}
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			fmt.Fprint(w, `{"total_count":3,"check_runs":[{"name":"a","status":"completed","conclusion":"success"}]}`)
		} else {
			fmt.Fprint(w, `{"total_count":3,"check_runs":[]}`)
		}
	})
	withTestServer(t, mux)

	runs, err := GetCheckRuns("owner", "repo", "sha123", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d check runs, want 1 (should stop on empty page)", len(runs))
	}
	if callCount != 2 {
		t.Errorf("expected 2 API calls, got %d", callCount)
	}
}

func TestGetStatuses_TerminatesOnEmpty(t *testing.T) {
	callCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if want := "/repos/owner/repo/commits/sha123/statuses"; r.URL.Path != want {
			t.Errorf("request path = %q, want %q", r.URL.Path, want)
		}
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			fmt.Fprint(w, `[{"state":"success","context":"ci/build"},{"state":"pending","context":"ci/lint"}]`)
		} else {
			fmt.Fprint(w, `[]`)
		}
	})
	withTestServer(t, mux)

	statuses, err := GetStatuses("owner", "repo", "sha123", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("got %d statuses, want 2", len(statuses))
	}
}

func TestGetStatuses_Pagination(t *testing.T) {
	var pages []string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if want := "/repos/owner/repo/commits/sha123/statuses"; r.URL.Path != want {
			t.Errorf("request path = %q, want %q", r.URL.Path, want)
		}
		pages = append(pages, r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		if len(pages) == 1 {
			fmt.Fprint(w, `[{"state":"success","context":"ci/build"}]`)
		} else {
			fmt.Fprint(w, `[]`)
		}
	})
	withTestServer(t, mux)

	statuses, err := GetStatuses("owner", "repo", "sha123", "token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 API calls for pagination, got %d", len(pages))
	}
	if pages[0] != "1" {
		t.Errorf("first request page = %q, want %q", pages[0], "1")
	}
	if pages[1] != "2" {
		t.Errorf("second request page = %q, want %q", pages[1], "2")
	}
}
