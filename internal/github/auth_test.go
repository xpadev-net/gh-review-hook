package github

import "testing"

func TestGetToken_FromEnv(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_test123")

	token, err := GetToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "ghp_test123" {
		t.Errorf("token = %q, want %q", token, "ghp_test123")
	}
}

func TestGetToken_EmptyEnv(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")

	// With empty env var, GetToken falls back to `gh auth token`.
	// In test/CI environments, this should fail.
	_, err := GetToken()
	if err == nil {
		t.Skip("gh CLI is authenticated; skipping error-path test")
	}
}
