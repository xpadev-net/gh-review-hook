package git

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// EnsureClean checks that the working tree has no uncommitted changes and
// no unpushed commits. It returns (pass=true, nil) when the branch has no
// upstream tracking branch (never been pushed) — the caller should silently
// exit 0 in that case. It returns (pass=false, nil) when everything is clean
// and the caller should proceed. It returns (pass=false, err) when there are
// uncommitted or unpushed changes.
func EnsureClean() (pass bool, err error) {
	// Check for uncommitted changes
	out, err := runGit("status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("failed to run git status: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		return false, fmt.Errorf("uncommitted changes detected; please commit before running")
	}

	// Check if upstream tracking branch exists
	_, err = runGit("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil {
		// No upstream configured — nothing to review
		return true, nil
	}

	// Upstream exists — check for unpushed commits
	out, err = runGit("log", "@{upstream}..HEAD", "--oneline")
	if err != nil {
		return false, fmt.Errorf("failed to check unpushed commits: %w", err)
	}
	if strings.TrimSpace(out) != "" {
		return false, fmt.Errorf("unpushed commits detected; please push before running")
	}

	return false, nil
}

// CurrentBranch returns the name of the current branch.
func CurrentBranch() (string, error) {
	out, err := runGit("symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("failed to determine current branch: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// RemoteInfo parses the origin remote URL and returns the GitHub owner and repo.
// Supports SSH (git@github.com:owner/repo.git), HTTPS (https://github.com/owner/repo.git),
// and ssh:// (ssh://git@github.com/owner/repo.git) formats.
func RemoteInfo() (owner string, repo string, err error) {
	url, err := runGit("remote", "get-url", "origin")
	if err != nil {
		return "", "", fmt.Errorf("failed to get origin remote URL: %w", err)
	}
	url = strings.TrimSpace(url)

	owner, repo, err = parseRemoteURL(url)
	if err != nil {
		return "", "", fmt.Errorf("cannot parse origin remote URL %q: %w", url, err)
	}
	return owner, repo, nil
}

var (
	// git@github.com:owner/repo.git
	sshColonRe = regexp.MustCompile(`^git@github\.com:([^/]+)/(.+?)(?:\.git)?$`)
	// https://github.com/owner/repo.git or ssh://git@github.com/owner/repo.git
	slashRe = regexp.MustCompile(`(?:https?|ssh)://[^/]*github\.com/([^/]+)/(.+?)(?:\.git)?$`)
)

func parseRemoteURL(url string) (string, string, error) {
	if m := sshColonRe.FindStringSubmatch(url); m != nil {
		return m[1], m[2], nil
	}
	if m := slashRe.FindStringSubmatch(url); m != nil {
		return m[1], m[2], nil
	}
	return "", "", fmt.Errorf("unsupported URL format")
}

func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
