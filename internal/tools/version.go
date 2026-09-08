package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/version"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// version_prepare
// ---------------------------------------------------------------------------

// VersionPrepareIn is the input for the version_prepare tool.
type VersionPrepareIn struct {
	SkipConfigCheck bool   `json:"skipConfigCheck"`
	SessionID       string `json:"sessionID"`
}

// VersionSourceInfo describes the detected version source.
type VersionSourceInfo struct {
	Path    string `json:"path"`
	Type    string `json:"type"`
	Version string `json:"version"`
}

// VersionBumpOption describes a single bump possibility.
type VersionBumpOption struct {
	Level   string `json:"level"`
	Result  string `json:"result"`
	Current string `json:"current"`
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

// VersionPrepareOut is the output for the version_prepare tool.
type VersionPrepareOut struct {
	Errors              []string                    `json:"errors"`
	Warnings            []string                    `json:"warnings"`
	Flow                string                      `json:"flow"`
	CurrentBranch       string                      `json:"currentBranch"`
	VersionSource       *VersionSourceInfo          `json:"versionSource"`
	BumpOptions         []VersionBumpOption         `json:"bumpOptions"`
	Tags                VersionTagInfo              `json:"tags"`
	CommitsSinceTag     []string                    `json:"commitsSinceTag"`
	ConventionalSummary *VersionConventionalSummary `json:"conventionalSummary"`
	ChangelogExists     bool                        `json:"changelogExists"`
	Idempotency         VersionIdempotency          `json:"idempotency"`
}

// versionPrepare is the core logic, separated for testability.
func versionPrepare(cfgRoot, gitRoot string, in VersionPrepareIn) (VersionPrepareOut, error) {
	out := VersionPrepareOut{
		Errors:          []string{},
		Warnings:        []string{},
		Flow:            "release",
		BumpOptions:     []VersionBumpOption{},
		CommitsSinceTag: []string{},
		Tags: VersionTagInfo{
			All:    []string{},
			AtHead: []string{},
		},
	}

	// Config check.
	if !in.SkipConfigCheck {
		if err := configmigrate.Verify(cfgRoot); err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("config check failed: %s", err.Error()))
		}
	}

	// Current branch.
	currentBranch, err := gitx.CurrentBranch(gitRoot)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("currentBranch: %s", err.Error()))
	}
	out.CurrentBranch = currentBranch

	// Version source.
	vf, err := version.Detect(cfgRoot)
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("version detection failed: %s", err.Error()))
		return out, nil
	}
	out.VersionSource = &VersionSourceInfo{
		Path:    vf.Path,
		Type:    vf.Type,
		Version: vf.Version,
	}

	// Bump options for standard levels.
	for _, level := range []string{"major", "minor", "patch"} {
		result, bErr := version.Bump(vf, level)
		if bErr != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("bump %s: %s", level, bErr.Error()))
			continue
		}
		out.BumpOptions = append(out.BumpOptions, VersionBumpOption{
			Level:   level,
			Result:  result,
			Current: vf.Version,
		})
	}

	// Tags.
	allTags, err := gitx.AllSemverTags(gitRoot)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("tags: %s", err.Error()))
	} else {
		out.Tags.All = allTags
		if len(allTags) > 0 {
			out.Tags.Latest = allTags[0]
		}
		// Detect tag prefix from existing tags.
		out.Tags.TagPrefix = detectTagPrefix(allTags)
	}

	atHead, err := gitx.TagsAtHead(gitRoot)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("tagsAtHead: %s", err.Error()))
	} else {
		out.Tags.AtHead = atHead
	}
	if out.Tags.All == nil {
		out.Tags.All = []string{}
	}
	if out.Tags.AtHead == nil {
		out.Tags.AtHead = []string{}
	}

	// Idempotency: already bumped if there is a semver tag at HEAD.
	if len(out.Tags.AtHead) > 0 {
		out.Idempotency.AlreadyBumped = true
		out.Idempotency.TagAtHead = out.Tags.AtHead[0]
	}

	// Commits since last tag.
	if out.Tags.Latest != "" {
		logOut, logErr := execx.Run("git", []string{
			"log", "--oneline", out.Tags.Latest + "..HEAD",
		}, execx.Options{Dir: gitRoot})
		if logErr != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("commitsSinceTag: %s", logErr.Error()))
		} else if logOut != "" {
			out.CommitsSinceTag = nonEmptyLines(logOut)
		}
	} else {
		// No tags: all commits.
		logOut, logErr := execx.Run("git", []string{
			"log", "--oneline",
		}, execx.Options{Dir: gitRoot})
		if logErr != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("commitsSinceTag: %s", logErr.Error()))
		} else if logOut != "" {
			out.CommitsSinceTag = nonEmptyLines(logOut)
		}
	}

	// Conventional commit summary.
	out.ConventionalSummary = analyzeConventionalCommits(out.CommitsSinceTag)

	// Changelog existence check.
	changelogPath := filepath.Join(cfgRoot, "CHANGELOG.md")
	out.ChangelogExists = fileExists(changelogPath)

	return out, nil
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

// ---------------------------------------------------------------------------
// version_apply
// ---------------------------------------------------------------------------

// VersionApplyIn is the input for the version_apply tool.
type VersionApplyIn struct {
	Level           string `json:"level"`
	Notes           string `json:"notes"`
	SkipConfigCheck bool   `json:"skipConfigCheck"`
	SessionID       string `json:"sessionID"`
}

// VersionApplyOut is the output for the version_apply tool.
type VersionApplyOut struct {
	PreviousVersion string `json:"previousVersion"`
	NewVersion      string `json:"newVersion"`
	VersionFile     string `json:"versionFile"`
	ChangelogFile   string `json:"changelogFile"`
	Changed         bool   `json:"changed"`
}

// versionApply is the core logic, separated for testability.
func versionApply(cfgRoot string, in VersionApplyIn) (VersionApplyOut, error) {
	if strings.TrimSpace(in.Level) == "" {
		return VersionApplyOut{}, &mcpserver.DataError{
			Msg: "level must not be empty (e.g. major, minor, patch, or an explicit semver string)",
		}
	}

	// Config check.
	if !in.SkipConfigCheck {
		if err := configmigrate.Verify(cfgRoot); err != nil {
			return VersionApplyOut{}, &mcpserver.DataError{
				Msg:   fmt.Sprintf("config check failed: %s", err.Error()),
				Cause: err,
			}
		}
	}

	report, err := version.Apply(cfgRoot, in.Level, in.Notes)
	if err != nil {
		return VersionApplyOut{}, &mcpserver.DataError{
			Msg:   fmt.Sprintf("version apply: %s", err.Error()),
			Cause: err,
		}
	}

	return VersionApplyOut{
		PreviousVersion: report.PreviousVersion,
		NewVersion:      report.NewVersion,
		VersionFile:     report.VersionFile,
		ChangelogFile:   report.ChangelogFile,
		Changed:         report.Changed,
	}, nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterVersionTools registers version_prepare and version_apply on the
// server.
func RegisterVersionTools(s *mcpserver.Server) {
	mcpserver.Register(s, "version_prepare",
		"Gather version context: current version, bump options, tags, conventional commit analysis, and changelog status.",
		func(ctx mcpserver.Ctx, in VersionPrepareIn) (VersionPrepareOut, error) {
			cfgRoot, err := worktree.MainRoot()
			if err != nil {
				return VersionPrepareOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve main root: %s", err.Error()),
					Cause: err,
				}
			}
			gitRoot, err := worktree.ActiveRoot()
			if err != nil {
				return VersionPrepareOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve active root: %s", err.Error()),
					Cause: err,
				}
			}
			return versionPrepare(cfgRoot, gitRoot, in)
		},
	)

	mcpserver.Register(s, "version_apply",
		"Bump the version file and optionally prepend a changelog entry. Accepts a bump keyword (major/minor/patch/...) or an explicit semver string.",
		func(ctx mcpserver.Ctx, in VersionApplyIn) (VersionApplyOut, error) {
			cfgRoot, err := worktree.MainRoot()
			if err != nil {
				return VersionApplyOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve main root: %s", err.Error()),
					Cause: err,
				}
			}
			return versionApply(cfgRoot, in)
		},
	)
}
