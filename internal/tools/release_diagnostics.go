// release_diagnostics.go holds shared version/release-diagnostics types and
// helpers (version source, bump options, tag info, conventional-commit
// summary, idempotency, config info, divergence) plus a few pure helper
// functions (versionSuggestedPreRelease, analyzeConventionalCommits,
// detectTagPrefix, fileExists). These no longer back a standalone MCP tool —
// the standalone version tool was removed and its prepare/apply
// responsibilities absorbed into the pr tool (internal/tools/pr.go), which
// is the sole consumer of the types and helpers here today.
package tools

import (
	"os"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
)

// VersionSourceInfo describes the detected version source.
type VersionSourceInfo struct {
	Path    string `json:"path"`
	Type    string `json:"type"`
	Version string `json:"version"`
}

// VersionBumpOption describes a single bump possibility.
//
// SuggestedPreRelease is "rc" when the version config's PreReleasePolicy
// says to suggest one for this bump target: "always-rc" always suggests
// one, "continue-rc" (the default) only when Result already has one or
// more existing RC tags (see PRPrepareOut.ExistingRCs) — i.e. this
// bump target is already mid-RC-train, so the safer default is another RC
// rather than a final release — and "never" never suggests one. Empty
// when there's no suggestion either way.
type VersionBumpOption struct {
	Level               string `json:"level"`
	Result              string `json:"result"`
	Current             string `json:"current"`
	RCNext              string `json:"rcNext,omitempty"`
	SuggestedPreRelease string `json:"suggestedPreRelease,omitempty"`
}

// VersionTagInfo holds tag-related information.
type VersionTagInfo struct {
	All       []string `json:"all"`
	AtHead    []string `json:"atHead"`
	Latest    string   `json:"latest"`
	TagPrefix string   `json:"tagPrefix"`
}

// VersionConventionalSummary holds conventional commit analysis.
type VersionConventionalSummary struct {
	Breaking int    `json:"breaking"`
	Feat     int    `json:"feat"`
	Fix      int    `json:"fix"`
	Other    int    `json:"other"`
	Total    int    `json:"total"`
	Suggest  string `json:"suggest"`
}

// VersionIdempotency holds idempotency check info.
type VersionIdempotency struct {
	AlreadyBumped bool   `json:"alreadyBumped"`
	TagAtHead     string `json:"tagAtHead,omitempty"`
}

// VersionConfigInfo describes the resolved version config section, mirroring
// config.VersionSection's three independently toggleable release paths
// (Tag, VersionFile, Changelog) plus the shared bump policy and delivery
// Method.
type VersionConfigInfo struct {
	PreRelease       string                        `json:"preRelease,omitempty"`
	PreReleasePolicy string                        `json:"preReleasePolicy"`
	Method           string                        `json:"method"`
	Tag              config.VersionTagConfig       `json:"tag"`
	VersionFile      config.VersionFileConfig      `json:"versionFile"`
	Changelog        config.VersionChangelogConfig `json:"changelog"`
}

// DivergenceInfo describes a divergence between the file version and
// the highest remote tag.
type DivergenceInfo struct {
	FileVersion string `json:"fileVersion"`
	TagVersion  string `json:"tagVersion"`
	Message     string `json:"message"`
}

// versionSuggestedPreRelease resolves the version config's PreReleasePolicy
// enum ("always-rc" | "continue-rc" | "never") plus whether the bump target
// already has one or more existing RC tags into a SuggestedPreRelease value
// for a VersionBumpOption: "rc" to suggest a release candidate, "" to
// suggest a final release. Pure and side-effect-free so it's unit-testable
// without any filesystem or git fixtures. An unrecognized policy value
// falls through with no suggestion, same as "never".
func versionSuggestedPreRelease(policy string, hasExistingRCs bool) string {
	switch policy {
	case "always-rc":
		return "rc"
	case "continue-rc":
		if hasExistingRCs {
			return "rc"
		}
	case "never":
		// never suggest RC
	}
	return ""
}

// analyzeConventionalCommits does a lightweight conventional-commit
// classification of oneline log entries.
func analyzeConventionalCommits(commits []string) *VersionConventionalSummary {
	if len(commits) == 0 {
		return &VersionConventionalSummary{Suggest: "patch"}
	}

	summary := &VersionConventionalSummary{}
	for _, line := range commits {
		// Oneline format: "<sha> <subject>"
		parts := strings.SplitN(line, " ", 2)
		subject := line
		if len(parts) == 2 {
			subject = parts[1]
		}
		lower := strings.ToLower(subject)

		switch {
		case strings.Contains(lower, "breaking change") || strings.Contains(lower, "!:"):
			summary.Breaking++
		case strings.HasPrefix(lower, "feat") && (len(lower) > 4 && (lower[4] == '(' || lower[4] == ':' || lower[4] == '!')):
			summary.Feat++
		case strings.HasPrefix(lower, "fix") && (len(lower) > 3 && (lower[3] == '(' || lower[3] == ':' || lower[3] == '!')):
			summary.Fix++
		default:
			summary.Other++
		}
	}
	summary.Total = len(commits)

	switch {
	case summary.Breaking > 0:
		summary.Suggest = "major"
	case summary.Feat > 0:
		summary.Suggest = "minor"
	default:
		summary.Suggest = "patch"
	}

	return summary
}

// detectTagPrefix detects whether tags use a "v" prefix.
func detectTagPrefix(tags []string) string {
	vCount := 0
	for _, t := range tags {
		if strings.HasPrefix(t, "v") {
			vCount++
		}
	}
	if len(tags) > 0 && vCount > len(tags)/2 {
		return "v"
	}
	return ""
}

// fileExists checks if a file exists (not a directory).
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
