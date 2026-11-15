package tools

import (
	"regexp"
	"slices"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
)

// inlineSeverityRe matches a leading "[level]" tag in any letter case.
var inlineSeverityRe = regexp.MustCompile(`(?i)^\s*\[(critical|high|medium|low|minor)\]`)

// inlineSeverityRank orders the severity vocabulary. An empty string ranks 0.
var inlineSeverityRank = map[string]int{
	"critical": 4,
	"high":     3,
	"medium":   2,
	"low":      1,
}

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
	if !slices.Contains(dimensions.ValidSeverities, level) {
		return ""
	}
	return level
}

// higherSeverity returns the higher of a and b by the rank
// critical=4, high=3, medium=2, low=1, ""=0.
func higherSeverity(a, b string) string {
	if inlineSeverityRank[b] > inlineSeverityRank[a] {
		return b
	}
	return a
}
