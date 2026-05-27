package github

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

func sandboxNote() string {
	if os.Getenv("CODEX_SANDBOX") != "" {
		return "\nNote: running inside Codex sandbox — try running outside the sandbox instead"
	}
	return ""
}

func ghNotInstalledMessage() string {
	return "GitHub token not found: set GITHUB_TOKEN or install gh CLI (https://cli.github.com)" + sandboxNote()
}

func tokenNotFoundMessage() string {
	return "GitHub token not found: set GITHUB_TOKEN or run 'gh auth login'" + sandboxNote()
}

// GetToken resolves a GitHub API token. It checks the GITHUB_TOKEN environment
// variable first, then falls back to running "gh auth token".
func GetToken() (string, error) {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token, nil
	}

	if _, err := exec.LookPath("gh"); err != nil {
		return "", errors.New(ghNotInstalledMessage())
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
