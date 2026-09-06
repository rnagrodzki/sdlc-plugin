// Package branch provides branch name generation and validation for the
// sdlc plugin. It converts a task type and title into a slug-based branch
// name (e.g. "feat/add-login-page") and can guard that the current branch
// conforms to configured naming rules.
package branch

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
)

// Config holds branch naming configuration.
type Config struct {
	// Template for branch names. Default: "{type}/{slug}"
	Template string
	// AllowedTypes lists the branch type prefixes that are allowed.
	// If empty, all types are allowed.
	AllowedTypes []string
}

// nonAlphanumHyphen matches any character that is not alphanumeric or a
// hyphen, used to sanitise the title into a URL-safe slug.
var nonAlphanumHyphen = regexp.MustCompile(`[^a-z0-9-]`)

// multiHyphen collapses consecutive hyphens to a single one.
var multiHyphen = regexp.MustCompile(`-{2,}`)

// Slug converts a free-form title into a branch-name slug:
//   - lowercase
//   - non-alphanumeric characters replaced with hyphens
//   - consecutive hyphens collapsed
//   - leading/trailing hyphens trimmed
//   - max 50 characters (truncated at a word/hyphen boundary when possible)
func Slug(title string) string {
	s := strings.ToLower(title)
	s = nonAlphanumHyphen.ReplaceAllString(s, "-")
	s = multiHyphen.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 50 {
		s = truncateAtBoundary(s, 50)
	}
	return s
}

// truncateAtBoundary cuts s to maxLen, preferring to break at a hyphen
// boundary so words are not split mid-word. If the cut already lands on a
// word boundary (next char in the full string is '-' or the string ends),
// it keeps all maxLen characters. Otherwise it backtracks to the last
// hyphen within a reasonable window (15 chars). If no such hyphen exists,
// it hard-cuts at maxLen.
func truncateAtBoundary(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// If the next character after the cut is a hyphen (or the cut
	// is exactly the end), the cut lands cleanly on a word boundary.
	if len(s) == maxLen || s[maxLen] == '-' {
		return s[:maxLen]
	}
	cut := s[:maxLen]
	// Backtrack to the last hyphen within a reasonable window.
	lastHyphen := strings.LastIndex(cut, "-")
	if lastHyphen >= maxLen-15 {
		cut = cut[:lastHyphen]
	}
	return strings.TrimRight(cut, "-")
}

// defaultTemplate is the branch name template when none is configured.
const defaultTemplate = "{type}/{slug}"

// Resolve generates a branch name from config template + type + title.
// Template placeholders:
//
//	{type} — the branch type (e.g. "feat", "fix")
//	{slug} — slug derived from title
func Resolve(cfg Config, typ, title string) (string, error) {
	if typ == "" {
		return "", fmt.Errorf("branch: type must not be empty")
	}
	if title == "" {
		return "", fmt.Errorf("branch: title must not be empty")
	}
	if len(cfg.AllowedTypes) > 0 && !contains(cfg.AllowedTypes, typ) {
		return "", fmt.Errorf("branch: type %q is not allowed (allowed: %s)", typ, strings.Join(cfg.AllowedTypes, ", "))
	}

	tmpl := cfg.Template
	if tmpl == "" {
		tmpl = defaultTemplate
	}

	slug := Slug(title)
	name := strings.ReplaceAll(tmpl, "{type}", typ)
	name = strings.ReplaceAll(name, "{slug}", slug)
	return name, nil
}

// Guard checks if the current branch name is valid per config rules.
// Returns nil if valid, error describing the violation if not.
func Guard(dir string, cfg Config) error {
	branchName, err := gitx.CurrentBranch(dir)
	if err != nil {
		return fmt.Errorf("branch: %w", err)
	}
	if branchName == "HEAD" {
		return fmt.Errorf("branch: detached HEAD is not a valid branch")
	}
	if len(cfg.AllowedTypes) == 0 {
		return nil // nothing to enforce
	}

	for _, t := range cfg.AllowedTypes {
		prefix := t + "/"
		if strings.HasPrefix(branchName, prefix) {
			return nil
		}
	}
	return fmt.Errorf("branch: %q does not match any allowed type prefix (%s)",
		branchName, strings.Join(cfg.AllowedTypes, ", "))
}

// BranchGuardResult mirrors lib/branch-guard.js's validateExpectedBranch
// return shape. Active is false when no expected branch was configured
// (the guard did not run); OK is false only when Active is true and the
// current branch does not match the expected one.
type BranchGuardResult struct {
	OK             bool   `json:"ok"`
	CurrentBranch  string `json:"currentBranch,omitempty"`
	ExpectedBranch string `json:"expectedBranch,omitempty"`
	Active         bool   `json:"active"`
	Message        string `json:"message,omitempty"`
}

// ValidateExpectedBranch ports lib/branch-guard.js's validateExpectedBranch
// verbatim. When expectedBranch is empty the guard is inactive (Active:
// false, OK: true) — nothing is enforced. When active, it refuses a
// mismatch to avoid orphaning commits on the wrong branch (issues #347,
// #348, #349), matching the source's message text exactly.
func ValidateExpectedBranch(currentBranch, expectedBranch string) BranchGuardResult {
	if expectedBranch == "" {
		return BranchGuardResult{OK: true, CurrentBranch: currentBranch, Active: false}
	}
	if currentBranch == expectedBranch {
		return BranchGuardResult{OK: true, CurrentBranch: currentBranch, ExpectedBranch: expectedBranch, Active: true}
	}
	displayCurrent := currentBranch
	if displayCurrent == "" {
		displayCurrent = "(detached HEAD)"
	}
	message := fmt.Sprintf(
		"Branch mismatch: expected '%s' but current is '%s'. The pipeline is configured to operate on '%s'. Refusing to proceed to avoid orphaning commits on the wrong branch (issues #347, #348, #349).",
		expectedBranch, displayCurrent, expectedBranch,
	)
	return BranchGuardResult{OK: false, CurrentBranch: currentBranch, ExpectedBranch: expectedBranch, Active: true, Message: message}
}

// contains reports whether ss contains s.
func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
