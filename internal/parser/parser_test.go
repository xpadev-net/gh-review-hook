package parser

import (
	"strings"
	"testing"
)

func TestExtractGreptileReview_FullContent(t *testing.T) {
	body := `Some PR description text.

<!-- greptile_comment -->

<h3>Greptile Summary</h3>

This PR refactors the form versioning system.

<h3>Confidence Score: 3/5</h3>

- Mostly safe to merge, but the missing query invalidations could cause stale data.
- The PR resolves the majority of prior review concerns cleanly.

<h3>Important Files Changed</h3>

| Filename | Overview |
|----------|----------|
| path/to/file.ts | Core snapshot logic refactored |

<h3>Sequence Diagram</h3>

(mermaid diagram)

<!-- greptile_failed_comments -->
<details><summary><h3>Comments Outside Diff (2)</h3></summary>

1. ` + "`path/to/file1.ts`" + `, line 59-61
   Some finding here.
   <details><summary>Prompt To Fix With AI</summary>
` + "````" + ` markdown
This is a single comment fix prompt.
` + "````" + `
   </details>

2. ` + "`path/to/file2.ts`" + `, line 247-256
   Another finding here.
   <details><summary>Prompt To Fix With AI</summary>
` + "````" + ` markdown
This is another single comment fix prompt.
` + "````" + `
   </details>

</details>
<!-- /greptile_failed_comments -->

<details><summary>Prompt To Fix All With AI</summary>

` + "`````" + ` markdown
This is a comment left during a code review.
Path: path/to/file1.ts
Line: 59-61
Comment: Missing query invalidation
How can I resolve this? If you propose a fix, please make it concise.

---

This is a comment left during a code review.
Path: path/to/file2.ts
Line: 247-256
Comment: Overly broad UPDATE
How can I resolve this? If you propose a fix, please make it concise.
` + "`````" + `

</details>

<sub>Last reviewed commit: abc123</sub>

<!-- /greptile_comment -->`

	confidenceSection, prompt, found := ExtractGreptileReview(body)

	if !found {
		t.Fatal("expected found=true, got false")
	}

	// Confidence section should start with <h3>Confidence Score: 3/5</h3>
	if !strings.HasPrefix(confidenceSection, "<h3>Confidence Score: 3/5</h3>") {
		t.Errorf("confidenceSection should start with confidence heading, got: %q", confidenceSection[:min(80, len(confidenceSection))])
	}

	// Confidence section should contain the explanation
	if !strings.Contains(confidenceSection, "missing query invalidations") {
		t.Error("confidenceSection should contain explanation text")
	}

	// Confidence section should NOT contain Important Files Changed
	if strings.Contains(confidenceSection, "Important Files Changed") {
		t.Error("confidenceSection should not contain Important Files Changed")
	}

	// Prompt should contain the combined findings
	if !strings.Contains(prompt, "path/to/file1.ts") {
		t.Error("prompt should contain file1 path")
	}
	if !strings.Contains(prompt, "path/to/file2.ts") {
		t.Error("prompt should contain file2 path")
	}

	// Prompt should NOT contain fence markers
	if strings.Contains(prompt, "`````") {
		t.Error("prompt should have fence markers stripped")
	}

	// Prompt should NOT contain "Prompt To Fix With AI" (individual, without "All")
	if strings.Contains(prompt, "This is a single comment fix prompt") {
		t.Error("prompt should only contain the 'All' block content, not individual comment prompts")
	}
}

func TestExtractGreptileReview_Confidence5of5_NoPrompt(t *testing.T) {
	// Simulates the case from connect-form#676: Confidence 5/5, no Prompt To Fix All block
	body := `PR description.

<!-- greptile_comment -->

<h3>Greptile Summary</h3>

This PR is safe to merge with no significant concerns.

<h3>Confidence Score: 5/5</h3>

All changes look good. Safe to merge.

<!-- greptile_failed_comments -->
<details><summary><h3>Comments Outside Diff (0)</h3></summary>

</details>
<!-- /greptile_failed_comments -->

<sub>Last reviewed commit: def456</sub>

<!-- /greptile_comment -->`

	confidenceSection, prompt, found := ExtractGreptileReview(body)

	if !found {
		t.Fatal("expected found=true, got false")
	}

	if !strings.Contains(confidenceSection, "5/5") {
		t.Errorf("confidenceSection should contain 5/5, got: %q", confidenceSection)
	}

	if !strings.Contains(confidenceSection, "Safe to merge") {
		t.Error("confidenceSection should contain explanation text")
	}

	if prompt != "" {
		t.Errorf("expected empty prompt for 5/5 confidence, got: %q", prompt)
	}
}

func TestExtractGreptileReview_NoGreptileMarkers(t *testing.T) {
	body := `Just a normal PR description with no Greptile content.`

	_, _, found := ExtractGreptileReview(body)

	if found {
		t.Fatal("expected found=false when no Greptile markers present")
	}
}

func TestExtractGreptileReview_EmptyGreptileBlock(t *testing.T) {
	body := `<!-- greptile_comment -->
<!-- /greptile_comment -->`

	confidenceSection, prompt, found := ExtractGreptileReview(body)

	if !found {
		t.Fatal("expected found=true when markers are present")
	}

	if confidenceSection != "" {
		t.Errorf("expected empty confidenceSection, got: %q", confidenceSection)
	}

	if prompt != "" {
		t.Errorf("expected empty prompt, got: %q", prompt)
	}
}

func TestExtractGreptileReview_OnlyIndividualPrompts_NoAll(t *testing.T) {
	// A case where there are per-comment "Prompt To Fix With AI" blocks
	// but no combined "Prompt To Fix All With AI" block
	body := `<!-- greptile_comment -->

<h3>Greptile Summary</h3>

Summary text.

<h3>Confidence Score: 4/5</h3>

Some concerns.

<!-- greptile_failed_comments -->
<details><summary><h3>Comments Outside Diff (1)</h3></summary>

1. ` + "`file.ts`" + `, line 10-20
   <details><summary>Prompt To Fix With AI</summary>
   Fix this individual thing.
   </details>

</details>
<!-- /greptile_failed_comments -->

<sub>Last reviewed commit: ghi789</sub>

<!-- /greptile_comment -->`

	confidenceSection, prompt, found := ExtractGreptileReview(body)

	if !found {
		t.Fatal("expected found=true")
	}

	if !strings.Contains(confidenceSection, "4/5") {
		t.Errorf("confidenceSection should contain 4/5, got: %q", confidenceSection)
	}

	// There is no "Prompt To Fix All With AI" block, so prompt should be empty
	if prompt != "" {
		t.Errorf("expected empty prompt when no 'All' block exists, got: %q", prompt)
	}
}

func TestExtractGreptileReview_ConfidenceSectionBoundary(t *testing.T) {
	// Test that confidence section stops at the next <h3> tag
	body := `<!-- greptile_comment -->

<h3>Confidence Score: 2/5</h3>

This is the confidence explanation.
It has multiple lines.

<h3>Important Files Changed</h3>

This should NOT be in the confidence section.

<!-- /greptile_comment -->`

	confidenceSection, _, found := ExtractGreptileReview(body)

	if !found {
		t.Fatal("expected found=true")
	}

	if !strings.Contains(confidenceSection, "confidence explanation") {
		t.Error("confidenceSection should contain explanation text")
	}

	if strings.Contains(confidenceSection, "Important Files Changed") {
		t.Error("confidenceSection should stop before next <h3> tag")
	}

	if strings.Contains(confidenceSection, "should NOT be") {
		t.Error("confidenceSection should not contain content after next <h3>")
	}
}

func TestExtractGreptileReview_MissingDetailsCloseTag(t *testing.T) {
	// Edge case: Prompt To Fix All With AI block exists but </details> is missing
	body := `<!-- greptile_comment -->

<h3>Confidence Score: 3/5</h3>

Some concerns.

<details><summary>Prompt To Fix All With AI</summary>

` + "`````" + ` markdown
Fix this thing.
` + "`````" + `

<!-- /greptile_comment -->`

	confidenceSection, prompt, found := ExtractGreptileReview(body)

	if !found {
		t.Fatal("expected found=true")
	}

	if !strings.Contains(confidenceSection, "3/5") {
		t.Errorf("confidenceSection should contain 3/5, got: %q", confidenceSection)
	}

	// No </details> found, so prompt extraction should return empty
	if prompt != "" {
		t.Errorf("expected empty prompt when </details> is missing, got: %q", prompt)
	}
}

func TestExtractGreptileReviewComment_WithRawLastReviewedCommit(t *testing.T) {
	body := `<h3>Greptile Summary</h3>

Summary.

<h3>Confidence Score: 4/5</h3>

Needs updates.

<details><summary>Prompt To Fix All With AI</summary>
` + "````" + ` markdown
Fix A.
` + "````" + `
</details>

<sub>Last reviewed commit: abcdef1234567890</sub>`

	got := ExtractGreptileReviewComment(body)
	if !got.Found {
		t.Fatal("expected found=true")
	}
	if !strings.Contains(got.ConfidenceSection, "4/5") {
		t.Errorf("confidence should include 4/5, got %q", got.ConfidenceSection)
	}
	if !strings.Contains(got.Prompt, "Fix A.") {
		t.Errorf("prompt should include Fix A., got %q", got.Prompt)
	}
	if got.LastReviewedCommit != "abcdef1234567890" {
		t.Errorf("last reviewed commit = %q, want %q", got.LastReviewedCommit, "abcdef1234567890")
	}
}

func TestExtractGreptileReviewComment_WithCommitLink(t *testing.T) {
	body := `<h3>Confidence Score: 5/5</h3>
Safe to merge.
<sub>Reviews (1): Last reviewed commit: ["msg"](https://github.com/o/r/commit/4D245E21138C010B523CDF1408C6272D4658C783)</sub>`

	got := ExtractGreptileReviewComment(body)
	if !got.Found {
		t.Fatal("expected found=true")
	}
	if got.LastReviewedCommit != "4d245e21138c010b523cdf1408c6272d4658c783" {
		t.Errorf("last reviewed commit = %q", got.LastReviewedCommit)
	}
}

func TestExtractGreptileReviewComment_NotGreptile(t *testing.T) {
	body := `Normal user comment`
	got := ExtractGreptileReviewComment(body)
	if got.Found {
		t.Fatal("expected found=false")
	}
}

func TestExtractLastReviewedCommit_NoMatch(t *testing.T) {
	if got := ExtractLastReviewedCommit("no commit marker"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestIsCommitReviewed(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		reviewed string
		want     bool
	}{
		{
			name:     "full sha exact",
			current:  "abcdef123456",
			reviewed: "abcdef123456",
			want:     true,
		},
		{
			name:     "short sha prefix",
			current:  "abcdef123456",
			reviewed: "abcdef1",
			want:     true,
		},
		{
			name:     "case insensitive",
			current:  "AbCdEf123456",
			reviewed: "abcdef1",
			want:     true,
		},
		{
			name:     "different sha",
			current:  "abcdef123456",
			reviewed: "1234567",
			want:     false,
		},
		{
			name:     "empty reviewed",
			current:  "abcdef123456",
			reviewed: "",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsCommitReviewed(tt.current, tt.reviewed); got != tt.want {
				t.Errorf("IsCommitReviewed(%q, %q) = %v, want %v", tt.current, tt.reviewed, got, tt.want)
			}
		})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
