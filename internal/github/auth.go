package github

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// GetToken resolves a GitHub API token. It checks the GITHUB_TOKEN environment
// variable first, then falls back to running "gh auth token".
func GetToken() (string, error) {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token, nil
	}

	cmd := exec.Command("gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("GitHub token not found: set GITHUB_TOKEN or run 'gh auth login'")
	}

	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", fmt.Errorf("GitHub token not found: set GITHUB_TOKEN or run 'gh auth login'")
	}
	return token, nil
}
