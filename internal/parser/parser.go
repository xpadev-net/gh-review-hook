package parser

import (
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

	codeRabbitPromptHeading = "Prompt for all review comments with AI agents"
	codeRabbitCompatHeading = "Prompt for AI Agents"
)

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

// ExtractCodeRabbitPrompt extracts the "Prompt for all review comments with AI agents"
// content from a CodeRabbit comment body.
// It supports both the current heading and legacy "Prompt for AI Agents" heading.
func ExtractCodeRabbitPrompt(body string) string {
	// Prefer the explicit "all review comments" heading.
	prompt := extractDetailsSectionContent(body, codeRabbitPromptHeading)
	if prompt != "" {
		return prompt
	}
	// Backward compatibility with older CodeRabbit heading.
	return extractDetailsSectionContent(body, codeRabbitCompatHeading)
}

func extractDetailsSectionContent(body, heading string) string {
	openTag := "<details"
	summaryStart := "<summary>"
	summaryEnd := "</summary>"

	searchFrom := 0
	for {
		detailsIdx := strings.Index(body[searchFrom:], openTag)
		if detailsIdx < 0 {
			return ""
		}
		detailsIdx += searchFrom

		summaryIdx := strings.Index(body[detailsIdx:], summaryStart)
		if summaryIdx < 0 {
			return ""
		}
		summaryIdx += detailsIdx

		summaryCloseIdx := strings.Index(body[summaryIdx:], summaryEnd)
		if summaryCloseIdx < 0 {
			return ""
		}
		summaryCloseIdx += summaryIdx

		summaryContent := strings.TrimSpace(body[summaryIdx+len(summaryStart) : summaryCloseIdx])
		if !strings.Contains(strings.ToLower(summaryContent), strings.ToLower(heading)) {
			searchFrom = summaryCloseIdx + len(summaryEnd)
			continue
		}

		contentStart := summaryCloseIdx + len(summaryEnd)
		rest := body[contentStart:]
		closeIdx := findMatchingDetailsClose(rest)
		if closeIdx < 0 {
			return ""
		}

		content := strings.TrimSpace(rest[:closeIdx])
		content = stripFencedCodeBlock(content)
		return strings.TrimSpace(content)
	}
}

// findMatchingDetailsClose finds the closing </details> for an outer <details> block.
// The input must start from immediately after the outer <summary>...</summary>.
func findMatchingDetailsClose(s string) int {
	const detailsOpenPrefix = "<details"

	depth := 1
	pos := 0
	for {
		nextOpen := strings.Index(s[pos:], detailsOpenPrefix)
		if nextOpen >= 0 {
			nextOpen += pos
		}

		nextClose := strings.Index(s[pos:], detailsClose)
		if nextClose < 0 {
			return -1
		}
		nextClose += pos

		if nextOpen >= 0 && nextOpen < nextClose {
			endOfOpen := strings.Index(s[nextOpen:], ">")
			if endOfOpen < 0 {
				return -1
			}
			depth++
			pos = nextOpen + endOfOpen + 1
			continue
		}

		depth--
		if depth == 0 {
			return nextClose
		}
		pos = nextClose + len(detailsClose)
	}
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
