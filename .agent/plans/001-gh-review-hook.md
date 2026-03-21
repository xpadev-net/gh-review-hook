# gh-review-hook: Greptile Review Fetcher CLI

This ExecPlan is a living document. The sections `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` must be kept up to date as work proceeds. This document must be maintained in accordance with `.agent/PLANS.md`.


## Purpose / Big Picture

After this change, a developer (or an automated hook) can run a single command inside a Git repository that has an open GitHub Pull Request. The command will:

1. Verify the working tree is clean and commits are pushed:
   - If there are uncommitted changes, print a message to stderr and exit with code 1.
   - If the current branch has no upstream tracking branch (never been pushed), silently exit 0 — there is nothing to review.
   - If there are unpushed commits (upstream exists but HEAD is ahead), print a message to stderr and exit with code 1.
2. Find the PR associated with the current branch (or accept a PR number/URL as an argument). If no PR is found for the current branch, silently exit 0 — there is nothing to review yet.
3. Poll GitHub until every CI check (Check Runs and Commit Statuses) on the PR's head commit reports a completed state.
4. After all checks complete, wait 10 seconds for Greptile's bot to update the PR description with its review.
5. Fetch the PR description and extract: (a) the Confidence Score section (from `<h3>Confidence Score:` up to the next `<h3>` tag), and (b) the "Prompt To Fix All With AI" block content.
6. Determine the exit code and output channel based on whether there is actionable feedback:
   - Exit **2** (with feedback on stderr) when ANY of these are true: (a) CI has failed checks, or (b) Greptile review is present and Confidence Score is not "5/5".
   - Exit **0** (no output) when ALL of these are true: (a) CI has no failed checks (all green), AND (b) either no Greptile review is found, or Greptile review is present with Confidence Score "5/5".
   In other words: exit 0 means "nothing to fix", exit 2 means "here is what to fix" (stderr contains the details).

Exit code semantics (aligned with Claude Code hooks `TaskCompleted` spec):
- **0** = no issues, task is complete.
- **1** = precondition failure (dirty tree, unpushed commits, auth failure, timeout). The tool itself cannot proceed. Note: "no upstream" and "no PR found" are NOT precondition failures — they exit 0 because there is simply nothing to review.
- **2** = actionable feedback for the AI. stderr contains the feedback (CI failure names, Greptile findings). Claude Code will feed stderr back to the AI and re-run the task.

The intended use case is as a Claude Code "task completed" hook. Claude Code pushes changes, this tool waits for CI and Greptile. If exit 2, Claude Code reads stderr, fixes issues, and loops. If exit 0, the task is done.


## Progress

- [ ] Milestone 1: Project scaffolding (go.mod, directory structure, main.go entrypoint).
- [ ] Milestone 2: Git state checks (uncommitted changes, unpushed commits, branch detection, remote/owner/repo parsing).
- [ ] Milestone 3: GitHub authentication (GITHUB_TOKEN env var with gh CLI fallback).
- [ ] Milestone 4: PR detection (from argument or current branch via GitHub API).
- [ ] Milestone 5: CI polling (wait for all Check Runs and Commit Statuses to complete).
- [ ] Milestone 6: PR description parsing (extract Greptile review summary and Prompt To Fix All With AI).
- [ ] Milestone 7: End-to-end integration, CLI flags, error handling, and validation.


## Surprises & Discoveries

(No entries yet.)


## Decision Log

- Decision: Use Go standard library `net/http` and `encoding/json` for GitHub API calls instead of a third-party library like `google/go-github`.
  Rationale: The tool only needs a handful of API calls (list PRs, get PR, get check runs, get commit statuses). A dependency on `google/go-github` would be heavy for this narrow use case. Standard library keeps the binary small and dependencies minimal.
  Date/Author: 2026-03-22 / plan author

- Decision: Determine repository owner/repo by parsing the `origin` remote URL from `git remote get-url origin`, rather than relying on `gh` CLI for repo detection.
  Rationale: The tool should work without `gh` installed if GITHUB_TOKEN is set. Parsing the remote URL is straightforward and widely reliable.
  Date/Author: 2026-03-22 / plan author

- Decision: Extract Greptile content from the PR description body (the `body` field of the PR object returned by `GET /repos/{owner}/{repo}/pulls/{number}`), not from review comments or issue comments.
  Rationale: User confirmed that Greptile writes its review summary and "Prompt To Fix All With AI" block directly into the PR description (the PR body), not in comments.
  Date/Author: 2026-03-22 / plan author

- Decision: Exit code semantics aligned with Claude Code hooks `TaskCompleted` spec: 0 = no issues, 1 = precondition failure, 2 = actionable feedback for AI via stderr.
  Rationale: Per https://code.claude.com/docs/ja/hooks#taskcompleted, exit code 2 with stderr is how a hook feeds back to the AI. When CI fails or Confidence < 5/5, the tool exits 2 and writes feedback to stderr so Claude Code re-runs the task with that feedback. Exit 0 means nothing to fix. Exit 1 means the tool itself cannot proceed.
  Date/Author: 2026-03-22 / plan author (updated: originally exit 0 for all, then aligned with hooks spec)

- Decision: When no upstream tracking branch is configured (`@{u}` resolution fails), exit 0 silently instead of exit 1.
  Rationale: A branch with no upstream has never been pushed, so there is no PR to review. This is not an error condition — the tool simply has nothing to do. Exit 0 lets the Claude Code hook loop proceed without treating it as a precondition failure.
  Date/Author: 2026-03-22 / plan author (post-review update)

- Decision: When no PR is found for the current branch, exit 0 silently instead of exit 1.
  Rationale: After pushing code, the PR may not have been created yet (e.g., automated PR creation hasn't completed). Exiting with code 1 would incorrectly signal a tool failure. Exit 0 is the correct behavior because there is simply nothing to review yet, and the Claude Code hook should not loop on this condition.
  Date/Author: 2026-03-22 / plan author (post-review update)

- Decision: On CI failure, still fetch and output Greptile review alongside failed check names.
  Rationale: User wants both CI failure info and Greptile review content output together when both are available. This gives Claude Code maximum context for fixing issues. All feedback is combined into a single stderr output.
  Date/Author: 2026-03-22 / plan author

- Decision: Parse Greptile content using HTML comment markers `<!-- greptile_comment -->` and `<!-- /greptile_comment -->` rather than heuristic text matching.
  Rationale: User provided the exact format that Greptile uses in PR descriptions. The HTML comment markers are stable, machine-readable boundaries that make parsing reliable.
  Date/Author: 2026-03-22 / plan author

- Decision: Updated Greptile format specification from initial approximation to exact production format verified against xpadev-net/connect-form#677.
  Rationale: Examination of real PR data revealed: (1) individual comments each have their own `Prompt To Fix With AI` (without "All") blocks, (2) a single `Prompt To Fix All With AI` (with "All") block at the end contains all findings concatenated, (3) the content is wrapped in fenced code block markers (````` markdown), (4) sections include Sequence Diagram and a `<sub>Last reviewed commit:` footer. The parser must distinguish "All" from non-"All" prompt blocks and strip the fenced code block markers.
  Date/Author: 2026-03-22 / plan author

- Decision: `Prompt To Fix All With AI` block may be absent when Greptile has no actionable findings. Parser returns `found=true` with empty prompt in this case.
  Rationale: Verified against xpadev-net/connect-form#676 (Confidence 5/5, "safe to merge"). The block is entirely absent, not empty. The distinction between "no Greptile review" (found=false) and "reviewed but no issues" (found=true, prompt="") allows the caller to differentiate the two states.
  Date/Author: 2026-03-22 / plan author

- Decision: Output only the Confidence Score section and Prompt To Fix All With AI to stdout. Greptile Summary, Important Files Changed, Sequence Diagram, and Comments Outside Diff sections are all ignored.
  Rationale: User clarified that only the Confidence Score section and the combined prompt are needed for the Claude Code hook loop. The Summary and other sections contain contextual information not actionable by the AI fixer. Confidence Score 5/5 definitively indicates no issues.
  Date/Author: 2026-03-22 / plan author


## Outcomes & Retrospective

(No entries yet.)


## Context and Orientation

This is a brand-new repository at `/Users/xpadev/IdeaProjects/gh-review-hook` with no existing code. The only existing files are in `.agent/` (the ExecPlan authoring guide and this plan). We will create a Go module from scratch.

Key terms used in this plan:

- **Check Runs**: GitHub's newer CI status mechanism (used by GitHub Actions, etc.). Each check run has a `status` (queued, in_progress, completed) and a `conclusion` (success, failure, etc.). Retrieved via `GET /repos/{owner}/{repo}/commits/{ref}/check-runs`.
- **Commit Statuses**: GitHub's older CI status mechanism (used by some external CI providers). Each status has a `state` (pending, success, failure, error). Retrieved via `GET /repos/{owner}/{repo}/commits/{ref}/statuses`.
- **Greptile**: An AI code review bot that installs as a GitHub App. It updates the PR description with a review summary and a "Prompt To Fix All With AI" block. The content is wrapped in HTML comment markers `<!-- greptile_comment -->` ... `<!-- /greptile_comment -->`.
- **PR description / body**: The main body text of the Pull Request (the `body` field in the GitHub API's PR object), distinct from comments or reviews posted on the PR.
- **"Prompt To Fix All With AI"**: A collapsible HTML block that Greptile includes in the PR description. It is wrapped in `<details><summary>Prompt To Fix All With AI</summary>...</details>` and contains all review findings formatted as prompts for an AI coding assistant.

### Greptile PR Description Format

Greptile writes a block into the PR description body, wrapped in HTML comment markers. The exact structure observed in production (e.g., xpadev-net/connect-form#677) is:

    <!-- greptile_comment -->

    <h3>Greptile Summary</h3>
    ... review summary prose ...

    <h3>Confidence Score: N/5</h3>
    ... confidence explanation ...

    <h3>Important Files Changed</h3>
    | Filename | Overview |
    |----------|----------|
    | path/to/file.ts | description ... |
    </details>

    <h3>Sequence Diagram</h3>
    (mermaid diagram)

    <!-- greptile_failed_comments -->
    <details><summary><h3>Comments Outside Diff (N)</h3></summary>

    1. `path/to/file.ts`, line X-Y ([link](...))
       ... individual comment ...
       <details><summary>Prompt To Fix With AI</summary>
       ... prompt for this single comment ...
       </details>

    2. ... more comments, each with own Prompt To Fix With AI block ...

    </details>
    <!-- /greptile_failed_comments -->

    <details><summary>Prompt To Fix All With AI</summary>
    ... ALL review findings concatenated, separated by --- ...
    </details>

    <sub>Last reviewed commit: ...</sub>

    <!-- /greptile_comment -->

Key structural observations:
- The entire Greptile block is delimited by `<!-- greptile_comment -->` and `<!-- /greptile_comment -->`.
- Individual comments within "Comments Outside Diff" each have their own `<details><summary>Prompt To Fix With AI</summary>` (without "All").
- When Greptile has actionable findings, there is exactly one `<details><summary>Prompt To Fix All With AI</summary>` (with "All") near the end, containing all findings concatenated with `---` separators.
- **When Greptile has no actionable findings** (e.g., Confidence Score 5/5, "safe to merge"), the `Prompt To Fix All With AI` block is **entirely absent**. The structure goes directly from `<!-- /greptile_failed_comments -->` to `<sub>Last reviewed commit:` to `<!-- /greptile_comment -->`. This was verified against xpadev-net/connect-form#676.
- A `<sub>Last reviewed commit: ...</sub>` footer appears before the closing marker.

The parser must:
1. Locate the block between `<!-- greptile_comment -->` and `<!-- /greptile_comment -->`.
2. Within that block, extract the "Confidence Score section": search case-sensitively for the literal string `<h3>Confidence Score:` and extract everything from that point up to (but not including) the next `<h3>` tag, or up to `<!-- greptile_failed_comments -->` or `<!-- /greptile_comment -->` if no next `<h3>` exists. This gives the confidence score heading (e.g., `<h3>Confidence Score: 3/5</h3>`) and its explanation text below it. The Greptile Summary, Important Files Changed, Sequence Diagram, and Comments Outside Diff sections are all ignored.
3. Within that block, look for `<details><summary>Prompt To Fix All With AI</summary>` (case-sensitive, matching "All"). If multiple matches exist, use the first one. If found, extract the inner content up to the matching `</details>` as the "prompt" content. Strip the `<details>` wrapper. Then strip the outermost fenced code block markers if present: Greptile uses 5-backtick fences (````` markdown ... `````) but the parser should handle any fence of 3+ backticks. Strip the opening fence line (including any language specifier like "markdown") and the closing fence line. Preserve all content between them, including internal blank lines. Trim leading/trailing whitespace from the final result. If the `Prompt To Fix All With AI` block is not found, the prompt is empty (no actionable findings — Confidence Score is 5/5).
4. Return `found = true` as long as the `<!-- greptile_comment -->` and `<!-- /greptile_comment -->` markers were both present, even if the prompt is empty or the internal structure is malformed. If markers are present but the Confidence Score heading is missing, return `found = true` with empty `confidenceSection` and empty `prompt`. The caller distinguishes "no Greptile review" (found=false) from "Greptile reviewed but no issues" (found=true, prompt="").


## Plan of Work

The tool is a single Go binary called `gh-review-hook`. It has no subcommands. Its behavior is determined by the working directory (which must be inside a Git repo) and an optional positional argument (a PR number or GitHub PR URL).

The code is organized into a flat package structure inside `cmd/gh-review-hook/` for the entrypoint, with internal packages under `internal/` for the core logic:

    gh-review-hook/
      go.mod
      cmd/gh-review-hook/main.go      -- entrypoint, CLI arg parsing, orchestration
      internal/git/git.go             -- git operations (branch, remote, dirty check, unpushed check)
      internal/github/auth.go         -- token resolution (env var, gh CLI)
      internal/github/api.go          -- GitHub API client (PR lookup, check runs, statuses, PR body)
      internal/parser/parser.go       -- extract Greptile review summary and prompt from PR body
      internal/parser/parser_test.go  -- unit tests for parser

The flow in `main.go`:

1. Call `git.EnsureClean()` which runs `git status --porcelain` and checks for unpushed commits. If the first returns non-empty output, print "uncommitted changes detected; please commit before running" to stderr and `os.Exit(1)`. For unpushed commits: first run `git rev-parse --abbrev-ref --symbolic-full-name @{u}` to check if an upstream tracking branch is set. If this command fails (no upstream configured), silently pass (exit 0) — the branch has not been pushed yet and there is no PR to check. If an upstream exists, run `git log @{upstream}..HEAD --oneline`. If output is non-empty, print "unpushed commits detected; please push before running" to stderr and `os.Exit(1)`.

2. Resolve the GitHub token by calling `auth.GetToken()`, which checks `GITHUB_TOKEN` env var first, then runs `gh auth token` as a fallback. If neither works, print "GitHub token not found: set GITHUB_TOKEN or run 'gh auth login'" to stderr and exit 1.

3. Determine the PR. If a positional argument is given, parse it as a PR number (plain integer) or a GitHub PR URL (`https://github.com/{owner}/{repo}/pull/{number}` — query parameters like `?w=1` are stripped before parsing). When a URL is given, extract owner, repo, and number from the URL path. When a plain number is given, use owner/repo from `git.RemoteInfo()`. If no argument, call `git.CurrentBranch()` to get the branch name, call `git.RemoteInfo()` to parse owner/repo from the origin remote URL, then call `github.FindPR(owner, repo, branch, token)` which hits `GET /repos/{owner}/{repo}/pulls?head={owner}:{branch}&state=open` and returns the first match. If no PR is found, silently exit 0 — there is nothing to review yet (the PR may not have been created or an automated PR creation may not have completed). If the argument is neither a valid number nor a valid GitHub PR URL, print "invalid argument: expected PR number or GitHub PR URL" to stderr and exit 1.

4. Get the PR's head SHA via the PR object's `head.sha` field (already returned from step 3).

5. Poll CI status by calling `github.WaitForChecks(owner, repo, sha, token)` in a loop with 15-second intervals. Each iteration:
   a. Call `GET /repos/{owner}/{repo}/commits/{sha}/check-runs` and collect all check run objects.
   b. Call `GET /repos/{owner}/{repo}/commits/{sha}/statuses` and collect all status objects. Deduplicate statuses by `context` (keep only the latest per context, which is the first in the API response since they are returned newest-first).
   c. If any check run has `status != "completed"` or any status has `state == "pending"`, continue polling.
   d. If all are completed: check conclusions. CI is green only if every check run has `conclusion` in {"success", "skipped", "neutral"} AND every commit status has `state == "success"`. If any check run has `conclusion` in {"failure", "cancelled", "timed_out", "action_required"} OR any commit status has `state` in {"failure", "error"}, CI has failures. Check Runs and Commit Statuses are independent systems — a failure in either one marks CI as failed.
   e. Collect the names of all failed checks: for check runs, use the `name` field; for commit statuses, use the `context` field. Return a `CIResult` struct containing `AllGreen` (bool) and `FailedChecks` ([]string).
   f. Timeout after 30 minutes of polling. `WaitForChecks` returns an error. main.go prints the error message ("timed out after 30 minutes waiting for CI checks to complete") to stderr and exits 1.

6. Sleep 10 seconds to allow Greptile to update the PR description after CI completes.

7. Fetch the latest PR body by calling `GET /repos/{owner}/{repo}/pulls/{number}` and reading the `body` field.

8. Parse the PR body with `parser.ExtractGreptileReview(body)`. This function:
   a. Finds the block between `<!-- greptile_comment -->` and `<!-- /greptile_comment -->` markers.
   b. Extracts the "Confidence Score section": everything from `<h3>Confidence Score:` up to (but not including) the next `<h3>` tag, or up to `<!-- greptile_failed_comments -->` or `<!-- /greptile_comment -->` if no next `<h3>` exists. This includes the confidence score heading and its explanation text. All other sections (Greptile Summary, Important Files Changed, Sequence Diagram, Comments Outside Diff) are ignored.
   c. Looks for `<details><summary>Prompt To Fix All With AI</summary>` (with "All" — must not match the per-comment `Prompt To Fix With AI` blocks). If found, extracts the inner content up to the matching `</details>`, strips the `<details>` wrapper and fenced code block markers (````` markdown ... `````). This is the "prompt". If not found, prompt is empty.
   d. Returns the confidence section string, the prompt string, and a boolean indicating whether the Greptile markers were found.

9. Determine whether there is actionable feedback and choose the output channel and exit code:

   a. Collect feedback parts into a list, then join them with `---` separators and write to **stderr**:
      - Part 1 (if CI has failures): the failed check names in the format:
            CI failed checks:
            - check-name-1
            - check-name-2
      - Part 2 (if the Greptile block was found and Confidence Score is not 5/5): the Confidence Score section text. To determine if Confidence is 5/5, check if the `confidenceSection` string contains the literal substring `5/5` (e.g., `<h3>Confidence Score: 5/5</h3>`). If it does, skip this part.
      - Part 3 (if the prompt is non-empty): the Prompt To Fix All With AI content.
      Each part is separated from the next by a line containing only `---`. Parts that are absent are simply omitted (no empty separators).

   b. If any feedback parts were collected (CI failures OR Confidence < 5/5): write the joined string to **stderr** and exit **2**.

   c. If no feedback parts (CI green AND (Confidence 5/5 OR no Greptile review)): exit **0**. Nothing to fix.


## Concrete Steps

All commands are run from the repository root: `/Users/xpadev/IdeaProjects/gh-review-hook`.

### Step 1: Initialize Go module

    go mod init github.com/xpadev/gh-review-hook

Expected output:

    go: creating new go.mod: module github.com/xpadev/gh-review-hook

### Step 2: Create directory structure

    mkdir -p cmd/gh-review-hook internal/git internal/github internal/parser

### Step 3: Create internal/git/git.go

This file provides functions to interact with the local Git repository. All functions shell out to the `git` command.

Functions:
- `EnsureClean() (pass bool, err error)` — runs `git status --porcelain`. If output is non-empty, returns `(false, error)` with the message "uncommitted changes detected; please commit before running". Then runs `git rev-parse --abbrev-ref --symbolic-full-name @{u}` to check if an upstream tracking branch is configured. If this command fails (exit code != 0), the branch has no upstream — return `(true, nil)` to signal the caller should silently exit 0 (no upstream means the branch has never been pushed, so there is no PR to review). If an upstream exists, runs `git log @{upstream}..HEAD --oneline`. If output is non-empty, returns `(false, error)` with message "unpushed commits detected; please push before running". Returns `(false, nil)` if clean and ready to proceed.
- `CurrentBranch() (string, error)` — runs `git symbolic-ref --short HEAD` and returns the trimmed output.
- `RemoteInfo() (owner string, repo string, err error)` — runs `git remote get-url origin` and parses the result. Supports SSH (`git@github.com:owner/repo.git`) and HTTPS (`https://github.com/owner/repo.git`) formats. Also handles `ssh://git@github.com/owner/repo.git`. Strips the trailing `.git` suffix if present (works correctly if `.git` is already absent). Returns an error if the URL does not match any supported format or if owner/repo would be empty.

### Step 4: Create internal/github/auth.go

This file resolves a GitHub API token.

Function:
- `GetToken() (string, error)` — first checks `os.Getenv("GITHUB_TOKEN")`. If non-empty, returns it. Otherwise, runs `gh auth token` and returns the trimmed output. If both fail, returns an error with the message "GitHub token not found: set GITHUB_TOKEN or run 'gh auth login'".

### Step 5: Create internal/github/api.go

This file contains the GitHub API client functions. All functions accept an `owner`, `repo`, and `token` string and make HTTP requests to `https://api.github.com`. The token is sent as an `Authorization: Bearer {token}` header. All responses are parsed as JSON.

Types:

    type PR struct {
        Number int    `json:"number"`
        Body   string `json:"body"`
        Head   struct {
            SHA string `json:"sha"`
            Ref string `json:"ref"`
        } `json:"head"`
    }

    type CheckRun struct {
        Name       string `json:"name"`
        Status     string `json:"status"`
        Conclusion string `json:"conclusion"`
    }

    type CheckRunsResponse struct {
        TotalCount int        `json:"total_count"`
        CheckRuns  []CheckRun `json:"check_runs"`
    }

    type CommitStatus struct {
        State   string `json:"state"`
        Context string `json:"context"`
    }

    type CIResult struct {
        AllGreen     bool     // true when ALL check runs have conclusion in {success,skipped,neutral} AND ALL statuses have state "success"
        FailedChecks []string // names of failed check runs (Name field) and failed statuses (Context field); empty if AllGreen
    }

Functions:
- `FindPR(owner, repo, branch, token string) (*PR, error)` — calls `GET /repos/{owner}/{repo}/pulls?head={owner}:{branch}&state=open`. Returns the first PR if found, error if none.
- `GetPR(owner, repo string, number int, token string) (*PR, error)` — calls `GET /repos/{owner}/{repo}/pulls/{number}`.
- `GetCheckRuns(owner, repo, sha, token string) ([]CheckRun, error)` — calls `GET /repos/{owner}/{repo}/commits/{sha}/check-runs?per_page=100`. If the response `total_count` exceeds the number of returned check runs, follows pagination using `page` parameter (page=2, 3, ...) until all are collected. Returns an error if the HTTP status is not 200 or JSON decoding fails (error message includes the HTTP status code and URL).
- `GetStatuses(owner, repo, sha, token string) ([]CommitStatus, error)` — calls `GET /repos/{owner}/{repo}/commits/{sha}/statuses?per_page=100`. Paginates using the `page` parameter until an empty page is returned. Returns an error if the HTTP status is not 200 or JSON decoding fails (error message includes the HTTP status code and URL).
- `WaitForChecks(owner, repo, sha, token string) (*CIResult, error)` — orchestrates the polling loop described in the Plan of Work (step 5). The loop structure is: record start time, then repeat { call APIs, check if all completed, if not sleep 15 seconds }. The total timeout is 30 minutes (1800 seconds) measured from function entry (strict wall-clock time). If the elapsed time exceeds 30 minutes before starting a new poll iteration, return an error with message "timed out after 30 minutes waiting for CI checks to complete". On API errors, return the error immediately (no retry). Returns a `CIResult` on successful completion.

### Step 6: Create internal/parser/parser.go

This file extracts Greptile content from a PR description body string.

Function:
- `ExtractGreptileReview(body string) (confidenceSection string, prompt string, found bool)` — scans the body for:
  a. The Greptile block: locate the content between `<!-- greptile_comment -->` and `<!-- /greptile_comment -->` markers. Both markers must be present.
  b. The Confidence Score section: extract everything from `<h3>Confidence Score:` up to (but not including) the next `<h3>` tag, or up to `<!-- greptile_failed_comments -->` / `<!-- /greptile_comment -->` if no next `<h3>`. This is the `confidenceSection` return value. If the heading is not found within the block, `confidenceSection` is empty.
  c. The "Prompt To Fix All With AI" block: find `<details><summary>Prompt To Fix All With AI</summary>` (with "All" — not the per-comment `Prompt To Fix With AI` blocks). If multiple exist, use the first. Extract inner content up to the matching `</details>`. Strip the `<details>` wrapper, then the outermost fenced code block markers (3+ backticks, including language specifier), and trim whitespace. This is the `prompt` return value. If not found (Confidence 5/5 case), prompt is empty.
  d. If either Greptile marker is not found, return `found = false`.

### Step 7: Create internal/parser/parser_test.go

Unit tests for the parser using the exact Greptile format. Test cases:
- Body with full Greptile content (Confidence 3/5 + Prompt To Fix All With AI) — verifies confidenceSection is extracted (from `<h3>Confidence Score:` to before the next `<h3>` tag), prompt is extracted with fenced code block markers stripped.
- Body with Greptile content but Confidence 5/5 and no "Prompt To Fix All With AI" block (as seen in connect-form#676) — verifies confidenceSection is extracted, prompt is empty, `found = true`.
- Body with no Greptile markers at all — verifies `found = false`.
- Body with Greptile markers but empty content — verifies graceful handling, `found = true`.
- Body with both per-comment `Prompt To Fix With AI` blocks and the combined `Prompt To Fix All With AI` block — verifies only the "All" block is extracted as prompt, confidenceSection only contains the Confidence Score section.

### Step 8: Create cmd/gh-review-hook/main.go

This is the entrypoint that ties everything together. It:
1. Parses `os.Args` — if a positional argument is provided, determines if it is a number or a URL.
2. Calls the internal packages in the sequence described in Plan of Work.
3. All actionable feedback (CI failures, Greptile findings) goes to **stderr**. Precondition errors also go to stderr.
4. Exit codes: 0 = no issues (CI green, Confidence 5/5 or no Greptile review), 1 = precondition failure, 2 = actionable feedback written to stderr.

### Step 9: Build and test

    go build -o gh-review-hook ./cmd/gh-review-hook

Expected output: a binary named `gh-review-hook` in the repository root.

Run parser unit tests:

    go test ./internal/parser/...

Expected output:

    ok  	github.com/xpadev/gh-review-hook/internal/parser	...

To test manually (requires being inside a Git repo with a PR, GitHub token available):

    ./gh-review-hook

Or with a specific PR:

    ./gh-review-hook 42
    ./gh-review-hook https://github.com/owner/repo/pull/42


## Validation and Acceptance

The tool is validated by the following scenarios:

1. **Dirty working tree**: Create an uncommitted file, run `./gh-review-hook`. Expected: stderr prints "uncommitted changes detected; please commit before running" and exit code is 1.

2. **Unpushed commits (with upstream)**: On a branch that tracks a remote, make a commit without pushing, run `./gh-review-hook`. Expected: stderr prints "unpushed commits detected; please push before running" and exit code is 1.

2b. **No upstream configured**: On a newly created local branch that has never been pushed (no upstream tracking branch), run `./gh-review-hook`. Expected: no output, exit code is 0 (silently passes through — nothing to review).

3. **No PR found**: On a branch with no open PR, run `./gh-review-hook`. Expected: no output, exit code is 0 (silently passes through — nothing to review yet).

4. **CI fails, Greptile review with findings**: On a PR where CI fails and Greptile has findings (Confidence < 5/5), run `./gh-review-hook`. Expected: stderr prints failed check names, `---`, Confidence Score section, `---`, Prompt To Fix All With AI content. Exit code is 2.

5. **CI fails, no Greptile review**: On a PR where CI fails and no Greptile content is in the description, run `./gh-review-hook`. Expected: stderr prints failed check names. Exit code is 2.

6. **CI fails, Greptile Confidence 5/5**: On a PR where CI fails but Greptile has no issues (Confidence 5/5), run `./gh-review-hook`. Expected: stderr prints failed check names only (no Confidence Score section because 5/5 is not actionable). Exit code is 2.

7. **CI passes, Greptile review with findings**: On a PR where CI passes and Greptile has findings (Confidence < 5/5), run `./gh-review-hook`. Expected: stderr prints Confidence Score section, `---`, Prompt To Fix All With AI content. Exit code is 2.

8. **CI passes, Greptile review without findings**: On a PR where CI passes and Greptile reviewed but found no issues (Confidence 5/5, no Prompt To Fix All With AI block), run `./gh-review-hook`. Expected: no output. Exit code is 0.

9. **CI passes, no Greptile review**: On a PR where CI passes but no Greptile content is in the description at all (no `<!-- greptile_comment -->` markers), run `./gh-review-hook`. Expected: no output. Exit code is 0.

10. **PR number argument**: Run `./gh-review-hook 123`. Expected: tool uses PR #123 instead of auto-detecting from branch.

11. **Auth fallback**: Unset GITHUB_TOKEN, ensure `gh` is authenticated, run `./gh-review-hook`. Expected: tool works using `gh auth token`.

12. **Parser unit tests**: Run `go test ./internal/parser/...` and expect all tests to pass. These tests use hardcoded PR body strings matching the exact Greptile HTML comment format, including the "no findings" case where `Prompt To Fix All With AI` is absent.


## Idempotence and Recovery

The tool is read-only with respect to both the local repository and GitHub. It makes no modifications. Running it multiple times is safe. If the polling times out (30 minutes), the user can simply re-run the command.

If the tool is interrupted (Ctrl-C), no cleanup is needed since no state is written.

The 10-second wait after CI completion is a heuristic. If Greptile has not yet updated the PR description by that time, the tool reports "no review found" and the user can re-run.


## Interfaces and Dependencies

The only external dependency is the Go standard library. No third-party modules are required.

The tool shells out to two external commands:
- `git` (required) — for branch detection, dirty check, remote URL parsing.
- `gh` (optional) — only used as a fallback for authentication if GITHUB_TOKEN is not set.

Key interfaces and types in the Go code:

In `internal/github/api.go`:

    // CIResult contains the outcome of waiting for CI checks to complete.
    type CIResult struct {
        AllGreen     bool     // true if all checks passed
        FailedChecks []string // names of checks that did not succeed (empty if AllGreen)
    }

In `internal/parser/parser.go`:

    // ExtractGreptileReview parses a PR body and returns the Confidence Score
    // section (from <h3>Confidence Score: to before the next <h3> tag)
    // and the "Prompt To Fix All With AI" content. If no Greptile markers are
    // found, found is false. If Confidence Score is 5/5, prompt will be empty.
    func ExtractGreptileReview(body string) (confidenceSection string, prompt string, found bool)


## Artifacts and Notes

Example of what Greptile writes into a PR description (based on xpadev-net/connect-form#677):

    <!-- greptile_comment -->

    <h3>Greptile Summary</h3>

    This PR refactors the form versioning system to decouple snapshot
    creation from publication...

    Key items remaining:
    - **Missing query invalidations after full block reset** ...
    - **Activate endpoint uses overly broad UPDATE** ...

    <h3>Confidence Score: 3/5</h3>

    - Mostly safe to merge, but the missing query invalidations...

    <h3>Important Files Changed</h3>

    | Filename | Overview |
    |----------|----------|
    | path/to/file.ts | Core snapshot logic refactored... |
    </details>

    <h3>Sequence Diagram</h3>

    (mermaid sequenceDiagram block)

    <!-- greptile_failed_comments -->
    <details><summary><h3>Comments Outside Diff (23)</h3></summary>

    1. `path/to/file.ts`, line 413-432 ([link](...))
       <a href="#"><img alt="P2" ...></a> **Summary of finding**
       ... explanation ...
       <details><summary>Prompt To Fix With AI</summary>
       ````` markdown
       This is a comment left during a code review.
       Path: path/to/file.ts
       Line: 413-432
       Comment: ...
       How can I resolve this? If you propose a fix, please make it concise.
       `````
       </details>

    2. ... (more individual comments) ...

    </details>
    <!-- /greptile_failed_comments -->

    <details><summary>Prompt To Fix All With AI</summary>

    ````` markdown
    This is a comment left during a code review.
    Path: path/to/file1.ts
    Line: 59-61
    Comment: ...
    How can I resolve this? If you propose a fix, please make it concise.

    ---

    This is a comment left during a code review.
    Path: path/to/file2.ts
    Line: 247-256
    Comment: ...
    How can I resolve this? If you propose a fix, please make it concise.
    `````

    </details>

    <sub>Last reviewed commit: ["commit msg..."](https://github.com/.../commit/...)</sub>

    <!-- /greptile_comment -->

The parser identifies the block by the `<!-- greptile_comment -->` and `<!-- /greptile_comment -->` HTML comment markers. It extracts only the Confidence Score section and the Prompt To Fix All With AI content. All other sections (Summary, Important Files Changed, Sequence Diagram, Comments Outside Diff) are ignored.

Expected **stderr** output when CI passes and Greptile has findings (Confidence < 5/5) — exit code 2:

    <h3>Confidence Score: 3/5</h3>

    - Mostly safe to merge, but the missing query invalidations...
    - The PR resolves the majority of prior review concerns cleanly...

    ---
    This is a comment left during a code review.
    Path: path/to/file1.ts
    Line: 59-61
    Comment: ...
    How can I resolve this? If you propose a fix, please make it concise.

    ---

    This is a comment left during a code review.
    Path: path/to/file2.ts
    Line: 247-256
    Comment: ...
    How can I resolve this? If you propose a fix, please make it concise.

Expected output when CI passes and Greptile has no findings (Confidence 5/5) — exit code 0:

    (no output)

Expected **stderr** output when CI fails and Greptile has findings — exit code 2:

    CI failed checks:
    - build
    - test-unit
    ---
    <h3>Confidence Score: 3/5</h3>

    - Mostly safe to merge, but...

    ---
    This is a comment left during a code review.
    ...

Expected **stderr** output when CI fails but Greptile has no findings (Confidence 5/5) — exit code 2:

    CI failed checks:
    - build
    - test-unit

All actionable feedback goes to stderr (as required by Claude Code hooks). The Confidence Score section and prompt are separated by `---` lines. The prompt content is stripped of the `<details>` wrapper and the fenced code block markers (the ````` markdown fences).
