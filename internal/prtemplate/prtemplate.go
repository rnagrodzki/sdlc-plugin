// Package prtemplate is the Go port of scripts/lib/pr-template.js
// (sdlc-utilities plugin). It resolves and loads the PR template file,
// and provides placeholder validation for PR bodies against a template.
//
// Resolution precedence (matching the Node.js source exactly):
//
//  1. Canonical:  <root>/.sdlc-v2/pr-template.md
//  2. Deprecated: <root>/.claude/pr-template.md
//
// The Node.js source also emits a one-time stderr deprecation warning when
// only the legacy path exists. This port carries that information as the
// Template.Legacy field instead of writing to stderr — the caller decides
// whether to surface a warning.
//
// NOTE: The task prompt suggested also checking .github/pull_request_template.md
// and similar GitHub-standard locations. This is NOT what the Node.js source
// does — pr-template.js only checks the two paths above. To match the source
// faithfully, only those two paths are checked. See report for details.
//
// ValidateBody has no direct Node.js counterpart. The Node.js validate-body
// mode (pr.js --validate-body) validates embedded URLs/links, not template
// placeholders. ValidateBody here implements the placeholder/section-presence
// check implied by the pr SKILL.md contract: "All sections defined in
// the custom template must appear in the output." It checks that every
// ## Heading from the template is present in the PR body.
package prtemplate

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// conflictingPatterns lists release-marker substrings that a custom PR
// template must not contain. These markers are injected automatically by
// pr_apply's prReleaseInjectMarkers (internal/tools/pr.go) after the
// template-driven body is drafted — a custom template that already defines
// them would corrupt or duplicate the injected release metadata.
var conflictingPatterns = []string{
	"<!-- release-level:",
	"<!-- release-pre:",
	"<!-- release-notes-start",
	"<!-- release-notes-end",
}

// ValidateReleaseCompat checks a PR template's content for release markers
// that pr_apply injects automatically. Returns a non-nil error naming the
// first conflicting marker found; nil when the template is compatible.
func ValidateReleaseCompat(content string) error {
	for _, pat := range conflictingPatterns {
		if strings.Contains(content, pat) {
			return fmt.Errorf("custom PR template contains conflicting release marker %q — remove it; release markers are injected automatically by pr_apply", pat)
		}
	}
	return nil
}

// Template holds a resolved PR template's path and content.
type Template struct {
	// Path is the absolute path to the template file.
	Path string
	// Content is the UTF-8 content of the template file.
	Content string
	// Legacy is true when the template was found at the deprecated
	// .claude/pr-template.md location rather than the canonical
	// .sdlc-v2/pr-template.md.
	Legacy bool
	// Headings lists the ## headings extracted from the template,
	// in document order, without the leading "## ".
	Headings []string
}

// Resolve locates and loads the PR template for the given project root.
// Returns (nil, nil) when no template exists at either location.
// Returns a non-nil error only on I/O failures (file exists but unreadable).
//
// Resolution order matches scripts/lib/pr-template.js:
//  1. <root>/.sdlc-v2/pr-template.md   (canonical)
//  2. <root>/.claude/pr-template.md  (deprecated)
func Resolve(root string) (*Template, error) {
	canonical := filepath.Join(root, paths.DataDir, "pr-template.md")
	legacy := filepath.Join(root, ".claude", "pr-template.md")

	// Try canonical first. An empty file still counts as found
	// (matches Node.js fs.existsSync behavior).
	if content, found, err := readIfExists(canonical); err != nil {
		return nil, err
	} else if found {
		return newTemplate(canonical, content, false), nil
	}

	// Try legacy fallback.
	if content, found, err := readIfExists(legacy); err != nil {
		return nil, err
	} else if found {
		return newTemplate(legacy, content, true), nil
	}

	return nil, nil
}

// ValidateBody checks a PR body against the template and returns a list of
// validation issues. An empty/nil return means the body passes validation.
//
// Validation rules:
//  1. If the template is nil or has no headings, no validation is performed.
//  2. Each ## Heading from the template must appear as a line starting with
//     "## " followed by the exact heading text (trimmed) in the body.
//     Missing headings produce one issue each.
//
// This function has no direct Node.js counterpart — see package doc.
func ValidateBody(body string, t *Template) []string {
	if t == nil || len(t.Headings) == 0 {
		return nil
	}

	bodyHeadings := extractHeadings(body)
	bodySet := make(map[string]bool, len(bodyHeadings))
	for _, h := range bodyHeadings {
		bodySet[h] = true
	}

	var issues []string
	for _, h := range t.Headings {
		if !bodySet[h] {
			issues = append(issues, "missing section: ## "+h)
		}
	}
	return issues
}

// --- internal helpers ---

// readIfExists reads a file and returns (content, found, err).
// found is true when the file exists (even if empty).
// Returns ("", false, nil) if the file does not exist.
func readIfExists(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(data), true, nil
}

// extractHeadings scans text for lines starting with "## " and returns
// the heading text (trimmed, without the "## " prefix) in document order.
func extractHeadings(text string) []string {
	var headings []string
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "## ") {
			h := strings.TrimSpace(line[3:])
			if h != "" {
				headings = append(headings, h)
			}
		}
	}
	return headings
}

// newTemplate constructs a Template from the given path, content, and legacy flag.
func newTemplate(path, content string, legacy bool) *Template {
	return &Template{
		Path:     path,
		Content:  content,
		Legacy:   legacy,
		Headings: extractHeadings(content),
	}
}
