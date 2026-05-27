package github

import (
	"strings"
	"testing"
)

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

	_, err := GetToken()
	if err == nil {
		t.Skip("gh CLI is authenticated; skipping error-path test")
	}
}

func TestGetToken_SandboxMessage(t *testing.T) {
	t.Setenv("CODEX_SANDBOX", "1")
	t.Setenv("GITHUB_TOKEN", "")

	_, err := GetToken()
	if err == nil {
		t.Skip("gh CLI is authenticated; skipping error-path test")
	}
	if !strings.Contains(err.Error(), "Codex sandbox") {
		t.Errorf("error message missing sandbox note: %v", err)
	}
}

func TestTokenNotFoundMessage_Sandbox(t *testing.T) {
	t.Setenv("CODEX_SANDBOX", "1")
	msg := tokenNotFoundMessage()
	if !strings.Contains(msg, "Codex sandbox") {
		t.Errorf("message missing sandbox note: %q", msg)
	}
}
