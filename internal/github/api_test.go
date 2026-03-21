package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
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
		ts.Close()
		apiBase = originalBase
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
			if tt.wantNil {
				if pr != nil {
					t.Fatalf("expected nil PR, got %+v", pr)
				}
				return
			}
			if pr.Number != tt.wantNumber {
				t.Errorf("PR number = %d, want %d", pr.Number, tt.wantNumber)
			}
			if !strings.HasPrefix(gotPath, "/repos/myowner/myrepo/pulls") {
				t.Errorf("request path = %q, want prefix /repos/myowner/myrepo/pulls", gotPath)
			}
			if !strings.Contains(gotPath, "head=myowner:feat-branch") {
				t.Errorf("request URL %q missing head=myowner:feat-branch query param", gotPath)
			}
			if !strings.Contains(gotPath, "state=open") {
				t.Errorf("request URL %q missing state=open query param", gotPath)
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

func TestGetCheckRuns_SinglePage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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

func TestGetStatuses_TerminatesOnEmpty(t *testing.T) {
	callCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
