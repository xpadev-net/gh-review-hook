package main

import "testing"

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
