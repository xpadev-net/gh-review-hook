package git

import (
	"reflect"
	"testing"
)

func TestParseConflictOutput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "empty output",
			input: "",
			want:  nil,
		},
		{
			name:  "OID only (clean merge)",
			input: "abc123def456abc123def456abc123def456abc123\n",
			want:  nil,
		},
		{
			name:  "OID plus one conflicting file",
			input: "abc123def456abc123def456abc123def456abc123\ninternal/git/git.go\n",
			want:  []string{"internal/git/git.go"},
		},
		{
			name:  "OID plus multiple conflicting files",
			input: "abc123def456abc123def456abc123def456abc123\ninternal/git/git.go\ncmd/main.go\n",
			want:  []string{"internal/git/git.go", "cmd/main.go"},
		},
		{
			name:  "OID plus file with spaces in name",
			input: "abc123def456abc123def456abc123def456abc123\npath/to/file with spaces.go\n",
			want:  []string{"path/to/file with spaces.go"},
		},
		{
			name:  "blank lines between paths are skipped",
			input: "abc123def456abc123def456abc123def456abc123\nfoo.go\n\nbar.go\n",
			want:  []string{"foo.go", "bar.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseConflictOutput(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseConflictOutput(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{
			name:      "SSH colon format",
			input:     "git@github.com:owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
		},
		{
			name:      "SSH colon without .git suffix",
			input:     "git@github.com:owner/repo",
			wantOwner: "owner",
			wantRepo:  "repo",
		},
		{
			name:      "HTTPS format",
			input:     "https://github.com/owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
		},
		{
			name:      "HTTPS without .git suffix",
			input:     "https://github.com/owner/repo",
			wantOwner: "owner",
			wantRepo:  "repo",
		},
		{
			name:      "ssh:// protocol format",
			input:     "ssh://git@github.com/owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
		},
		{
			name:      "ssh:// protocol without .git suffix",
			input:     "ssh://git@github.com/owner/repo",
			wantOwner: "owner",
			wantRepo:  "repo",
		},
		{
			name:      "HTTP format",
			input:     "http://github.com/owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
		},
		{
			name:    "unsupported format",
			input:   "gitlab.com:owner/repo.git",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "bare local path",
			input:   "/local/path/to/repo",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, err := parseRemoteURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got owner=%q repo=%q", owner, repo)
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
		})
	}
}
