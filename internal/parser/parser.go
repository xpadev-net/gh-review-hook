package parser

import (
	"regexp"
	"strings"
)

const (
	greptileStart = "<!-- greptile_comment -->"
	greptileEnd   = "<!-- /greptile_comment -->"
	confPrefix    = "<h3>Confidence Score:"
	h3Tag         = "<h3>"
	failedStart   = "<!-- greptile_failed_comments -->"
	promptAllOpen = "<details><summary>Prompt To Fix All With AI</summary>"
	detailsClose  = "</details>"
)

var (
	lastReviewedCommitRawRe  = regexp.MustCompile(`(?i)Last reviewed commit:\s*([0-9a-f]{7,40})`)
	lastReviewedCommitLinkRe = regexp.MustCompile(`(?i)Last reviewed commit:\s*\[[^\]]+\]\([^)]+/commit/([0-9a-f]{7,40})\)`)
)

// ReviewData is parsed review information independent of where the review lives
// (PR description block or issue comment body).
type ReviewData struct {
	ConfidenceSection  string
	Prompt             string
	LastReviewedCommit string
	Found              bool
}

// ExtractGreptileReview parses a PR body and returns the Confidence Score
// section (from <h3>Confidence Score: to before the next <h3> tag)
// and the "Prompt To Fix All With AI" content. If no Greptile markers are
// found, found is false. If Confidence Score is 5/5, prompt will be empty.
func ExtractGreptileReview(body string) (confidenceSection string, prompt string, found bool) {
	// Locate the Greptile block
	startIdx := strings.Index(body, greptileStart)
	endIdx := strings.Index(body, greptileEnd)
	if startIdx < 0 || endIdx < 0 || endIdx <= startIdx {
		return "", "", false
	}

	block := body[startIdx+len(greptileStart) : endIdx]

	// Extract Confidence Score section
	confidenceSection = extractConfidenceSection(block)

	// Extract Prompt To Fix All With AI
	prompt = extractPromptAll(block)

	return confidenceSection, prompt, true
}

// ExtractGreptileReviewComment parses a Greptile issue comment body.
// It extracts confidence/prompt sections the same way as ExtractGreptileReview
// and also extracts the "Last reviewed commit" SHA when present.
func ExtractGreptileReviewComment(body string) ReviewData {
	confidence := extractConfidenceSection(body)
	prompt := extractPromptAll(body)
	last := ExtractLastReviewedCommit(body)

	found := confidence != "" || prompt != ""
	return ReviewData{
		ConfidenceSection:  confidence,
		Prompt:             prompt,
		LastReviewedCommit: last,
		Found:              found,
	}
}

// ExtractLastReviewedCommit returns the commit SHA found in a
// "Last reviewed commit: ..." line. It supports both raw SHA and commit-link
// formats. Returns empty string when no marker exists.
func ExtractLastReviewedCommit(body string) string {
	if m := lastReviewedCommitLinkRe.FindStringSubmatch(body); len(m) == 2 {
		return strings.ToLower(m[1])
	}
	if m := lastReviewedCommitRawRe.FindStringSubmatch(body); len(m) == 2 {
		return strings.ToLower(m[1])
	}
	return ""
}

// IsCommitReviewed returns true if reviewedCommit is a prefix of currentSHA
// (to support both full and short SHAs).
func IsCommitReviewed(currentSHA, reviewedCommit string) bool {
	current := strings.ToLower(strings.TrimSpace(currentSHA))
	reviewed := strings.ToLower(strings.TrimSpace(reviewedCommit))
	if current == "" || reviewed == "" {
		return false
	}
	return strings.HasPrefix(current, reviewed)
}

// extractConfidenceSection extracts everything from <h3>Confidence Score:
// up to (but not including) the next <h3> tag, or up to <!-- greptile_failed_comments -->
// or end of block, whichever comes first.
func extractConfidenceSection(block string) string {
	idx := strings.Index(block, confPrefix)
	if idx < 0 {
		return ""
	}

	rest := block[idx:]

	// Find the end boundary: next <h3> tag, or <!-- greptile_failed_comments -->, whichever is closer
	endBoundary := len(rest)

	// Search for next <h3> after the confidence heading itself
	// Skip past the current <h3> tag to avoid matching it
	afterHeading := strings.Index(rest, ">")
	if afterHeading >= 0 {
		nextH3 := strings.Index(rest[afterHeading+1:], h3Tag)
		if nextH3 >= 0 {
			candidate := afterHeading + 1 + nextH3
			if candidate < endBoundary {
				endBoundary = candidate
			}
		}
	}

	failedIdx := strings.Index(rest, failedStart)
	if failedIdx >= 0 && failedIdx < endBoundary {
		endBoundary = failedIdx
	}

	return strings.TrimSpace(rest[:endBoundary])
}

// extractPromptAll extracts the content of the <details><summary>Prompt To Fix All With AI</summary>
// block. It must match "All" exactly (not the per-comment "Prompt To Fix With AI").
// Strips the <details> wrapper and outermost fenced code block markers if present.
func extractPromptAll(block string) string {
	idx := strings.Index(block, promptAllOpen)
	if idx < 0 {
		return ""
	}

	inner := block[idx+len(promptAllOpen):]

	// Find matching </details>
	closeIdx := strings.Index(inner, detailsClose)
	if closeIdx < 0 {
		return ""
	}

	content := inner[:closeIdx]

	// Strip outermost fenced code block markers (3+ backticks)
	content = stripFencedCodeBlock(content)

	return strings.TrimSpace(content)
}

// stripFencedCodeBlock removes the outermost fenced code block markers
// (3+ backticks, optionally with a language specifier on the opening line)
// from the content. Only strips if the content starts and ends with fence markers.
func stripFencedCodeBlock(s string) string {
	trimmed := strings.TrimSpace(s)
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return trimmed
	}

	firstLine := strings.TrimSpace(lines[0])
	lastLine := strings.TrimSpace(lines[len(lines)-1])

	// Check if first line is an opening fence (3+ backticks, optionally followed by language)
	openFence := extractFence(firstLine)
	if openFence == "" {
		return s
	}

	// Check if last line is a closing fence (3+ backticks only)
	closeFence := extractFence(lastLine)
	if closeFence == "" {
		return s
	}

	// The closing fence must be at least as long as the opening fence
	if len(closeFence) < len(openFence) {
		return s
	}

	// Strip the fence lines
	inner := strings.Join(lines[1:len(lines)-1], "\n")
	return inner
}

// extractFence returns the backtick prefix of a line if it is a valid fence
// (3+ backticks), or empty string if not.
func extractFence(line string) string {
	count := 0
	for _, ch := range line {
		if ch == '`' {
			count++
		} else {
			break
		}
	}
	if count >= 3 {
		return strings.Repeat("`", count)
	}
	return ""
}
