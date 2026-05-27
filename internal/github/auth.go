package github

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

func isCodexSandbox() bool {
	return os.Getenv("CODEX_SANDBOX") != ""
}

func tokenNotFoundMessage() string {
	msg := "GitHub token not found: set GITHUB_TOKEN or run 'gh auth login'"
	if isCodexSandbox() {
		msg += "\nNote: running inside Codex sandbox — try running outside the sandbox instead"
	}
	return msg
}

// GetToken resolves a GitHub API token. It checks the GITHUB_TOKEN environment
// variable first, then falls back to running "gh auth token".
func GetToken() (string, error) {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token, nil
	}

	if _, err := exec.LookPath("gh"); err != nil {
		return "", errors.New(tokenNotFoundMessage())
	}

	cmd := exec.Command("gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New(tokenNotFoundMessage())
	}

	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", errors.New(tokenNotFoundMessage())
	}
	return token, nil
}
