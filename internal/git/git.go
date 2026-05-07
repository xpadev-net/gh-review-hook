package git

import (
	"errors"
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

// ConflictFiles performs a non-destructive merge simulation between
// origin/<headRef> and origin/<baseBranch> and returns the list of files that
// would conflict. Returns nil with no error when there are no conflicts.
// The working tree and index are never touched.
//
// Requires Git 2.40+. Errors are treated as soft failures by the caller.
func ConflictFiles(headRef, baseBranch string) ([]string, error) {
	if _, err := runGit("fetch", "origin", headRef, baseBranch); err != nil {
		return nil, fmt.Errorf("failed to fetch %s and %s from origin: %w", headRef, baseBranch, err)
	}

	stdout, exitCode, err := runGitWithExitCode(
		"merge-tree", "--write-tree", "--name-only", "--no-messages",
		"origin/"+headRef, "origin/"+baseBranch,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to run git merge-tree: %w", err)
	}

	switch exitCode {
	case 0:
		return nil, nil
	case 1:
		files := parseConflictOutput(stdout)
		if len(files) == 0 {
			return []string{"(unresolvable tree-level conflict)"}, nil
		}
		return files, nil
	default:
		return nil, fmt.Errorf("git merge-tree exited with code %d: %s", exitCode, strings.TrimSpace(stdout))
	}
}

// parseConflictOutput extracts conflicting file paths from git merge-tree
// --write-tree --name-only --no-messages stdout. Line 0 is the tree OID and
// is unconditionally skipped; remaining non-empty lines are file paths.
func parseConflictOutput(stdout string) []string {
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) < 2 {
		return nil
	}
	var files []string
	for _, line := range lines[1:] {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files
}

func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// runGitWithExitCode runs a git command and returns stdout, the process exit
// code, and any non-exit OS-level error separately. A non-zero exit code does
// NOT produce an error return value.
func runGitWithExitCode(args ...string) (stdout string, exitCode int, err error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(out), exitErr.ExitCode(), nil
		}
		return "", -1, err
	}
	return string(out), 0, nil
}
