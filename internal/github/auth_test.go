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

func TestGetToken_GhNotInstalled(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("CODEX_SANDBOX", "")
	t.Setenv("PATH", t.TempDir())

	_, err := GetToken()
	if err == nil {
		t.Fatal("expected error when gh is not on PATH")
	}
	if !strings.Contains(err.Error(), "install gh CLI") {
		t.Errorf("message should suggest installing gh CLI: %v", err)
	}
	if strings.Contains(err.Error(), "gh auth login") {
		t.Errorf("message should not suggest gh auth login when gh is not installed: %v", err)
	}
	if strings.Contains(err.Error(), "Codex sandbox") {
		t.Errorf("message should not mention sandbox when CODEX_SANDBOX is not set: %v", err)
	}
}

func TestGetToken_GhNotInstalled_Sandbox(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("CODEX_SANDBOX", "1")
	t.Setenv("PATH", t.TempDir())

	_, err := GetToken()
	if err == nil {
		t.Fatal("expected error when gh is not on PATH")
	}
	if !strings.Contains(err.Error(), "install gh CLI") {
		t.Errorf("message should suggest installing gh CLI: %v", err)
	}
	if !strings.Contains(err.Error(), "Codex sandbox") {
		t.Errorf("message should mention sandbox: %v", err)
	}
}

func TestTokenNotFoundMessage_Sandbox(t *testing.T) {
	t.Setenv("CODEX_SANDBOX", "1")
	msg := tokenNotFoundMessage()
	if !strings.Contains(msg, "Codex sandbox") {
		t.Errorf("message missing sandbox note: %q", msg)
	}
}

func TestGhNotInstalledMessage(t *testing.T) {
	t.Setenv("CODEX_SANDBOX", "")
	msg := ghNotInstalledMessage()
	if !strings.Contains(msg, "install gh CLI") {
		t.Errorf("message should suggest installing gh CLI: %q", msg)
	}
	if strings.Contains(msg, "Codex sandbox") {
		t.Errorf("message should not mention sandbox when CODEX_SANDBOX is not set: %q", msg)
	}
}

func TestGhNotInstalledMessage_Sandbox(t *testing.T) {
	t.Setenv("CODEX_SANDBOX", "1")
	msg := ghNotInstalledMessage()
	if !strings.Contains(msg, "install gh CLI") {
		t.Errorf("message should suggest installing gh CLI: %q", msg)
	}
	if !strings.Contains(msg, "Codex sandbox") {
		t.Errorf("message missing sandbox note: %q", msg)
	}
}
