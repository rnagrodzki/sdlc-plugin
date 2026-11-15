package tools

import (
	"regexp"
	"strings"
)

// inlineSeverityRe matches a leading "[level]" tag in any letter case.
var inlineSeverityRe = regexp.MustCompile(`(?i)^\s*\[(critical|high|medium|low|minor)\]`)

// inlineSeverityPrefix reads a leading "[level]" in any letter case.
// minor -> low. Unknown tag or no tag -> "".
func inlineSeverityPrefix(comment string) string {
	m := inlineSeverityRe.FindStringSubmatch(comment)
	if m == nil {
		return ""
	}
	level := strings.ToLower(m[1])
	if level == "minor" {
		level = "low"
	}
	return level
}

// higherSeverity returns the higher of a and b by severityRank (review.go).
// An empty string has no rank entry, so it ranks below every level.
func higherSeverity(a, b string) string {
	if severityRank[b] > severityRank[a] {
		return b
	}
	return a
}
