package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
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
	RCNext  string `json:"rcNext,omitempty"`
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

// VersionConfigInfo describes the resolved version config section.
type VersionConfigInfo struct {
	Mode          string `json:"mode"`
	VersionFile   string `json:"versionFile"`
	FileType      string `json:"fileType"`
	TagPrefix     string `json:"tagPrefix"`
	Changelog     bool   `json:"changelog"`
	ChangelogFile string `json:"changelogFile"`
	TicketPrefix  string `json:"ticketPrefix,omitempty"`
	PreRelease    string `json:"preRelease,omitempty"`
}

// DivergenceInfo describes a divergence between the file version and
// the highest remote tag.
type DivergenceInfo struct {
	FileVersion string `json:"fileVersion"`
	TagVersion  string `json:"tagVersion"`
	Message     string `json:"message"`
}

// VersionPrepareOut is the output for the version_prepare tool.
type VersionPrepareOut struct {
	Errors              []string                    `json:"errors"`
	Warnings            []string                    `json:"warnings"`
	Flow                string                      `json:"flow"`
	CurrentBranch       string                      `json:"currentBranch"`
	ConfigPresent       bool                        `json:"configPresent"`
	VersionConfig       *VersionConfigInfo          `json:"versionConfig,omitempty"`
	ProposedConfig      map[string]any              `json:"proposedConfig,omitempty"`
	VersionSource       *VersionSourceInfo          `json:"versionSource"`
	BumpOptions         []VersionBumpOption         `json:"bumpOptions"`
	Tags                VersionTagInfo              `json:"tags"`
	CommitsSinceTag     []string                    `json:"commitsSinceTag"`
	ConventionalSummary *VersionConventionalSummary `json:"conventionalSummary"`
	ChangelogExists     bool                        `json:"changelogExists"`
	Idempotency         VersionIdempotency          `json:"idempotency"`
	DirtyFiles          []string                    `json:"dirtyFiles"`
	HasDirtyFiles       bool                        `json:"hasDirtyFiles"`
	DefaultBranch       string                      `json:"defaultBranch"`
	OnDefaultBranch     bool                        `json:"onDefaultBranch"`
	VersionDivergence   *DivergenceInfo             `json:"versionDivergence,omitempty"`
	ExistingRCs         map[string][]string         `json:"existingRCs,omitempty"`
	Summary             string                      `json:"summary"`
	Actions             []string                    `json:"actions"`
	Next                string                      `json:"next"`
}

// versionPrepare is the core logic, separated for testability.
func versionPrepare(cfgRoot, gitRoot string, in VersionPrepareIn) (VersionPrepareOut, error) {
	out := VersionPrepareOut{
		Errors:          []string{},
		Warnings:        []string{},
		Flow:            "release",
		BumpOptions:     []VersionBumpOption{},
		CommitsSinceTag: []string{},
		DirtyFiles:      []string{},
		Actions:         []string{},
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

	// Read config.
	var versionFile string
	var fileType string
	var tagPrefixFromConfig string
	var changelogFile string

	cfg, cfgErr := config.Read(cfgRoot)
	if cfgErr == nil && cfg != nil && cfg.Version != nil {
		out.ConfigPresent = true
		vs := cfg.Version
		out.VersionConfig = &VersionConfigInfo{
			Mode:          vs.Mode,
			VersionFile:   vs.VersionFile,
			FileType:      vs.FileType,
			TagPrefix:     vs.TagPrefix,
			Changelog:     vs.Changelog,
			ChangelogFile: vs.ChangelogFile,
			TicketPrefix:  vs.TicketPrefix,
			PreRelease:    vs.PreRelease,
		}
		versionFile = vs.VersionFile
		fileType = vs.FileType
		tagPrefixFromConfig = vs.TagPrefix
		changelogFile = vs.ChangelogFile

		// Mode "tag" not supported yet.
		if vs.Mode == "tag" {
			out.Errors = append(out.Errors, "mode \"tag\" is not yet supported; use mode \"file\" or omit the mode field")
			return out, nil
		}
	}

	// Current branch.
	currentBranch, err := gitx.CurrentBranch(gitRoot)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("currentBranch: %s", err.Error()))
	}
	out.CurrentBranch = currentBranch

	// Default branch.
	defaultBranch, err := gitx.DefaultBranch(gitRoot)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("defaultBranch: %s", err.Error()))
	}
	out.DefaultBranch = defaultBranch
	out.OnDefaultBranch = defaultBranch != "" && currentBranch == defaultBranch

	// Dirty files.
	statusOut, err := gitx.Status(gitRoot)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("status: %s", err.Error()))
	} else if statusOut != "" {
		for _, line := range strings.Split(statusOut, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// Porcelain format: 2-char status + space + path.
			if len(line) > 3 {
				out.DirtyFiles = append(out.DirtyFiles, line[3:])
			} else {
				out.DirtyFiles = append(out.DirtyFiles, line)
			}
		}
	}
	out.HasDirtyFiles = len(out.DirtyFiles) > 0

	// Version source (config-driven: DetectAt).
	vf, err := version.DetectAt(cfgRoot, versionFile, fileType)
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("version detection failed: %s", err.Error()))
		return out, nil
	}
	out.VersionSource = &VersionSourceInfo{
		Path:    vf.Path,
		Type:    vf.Type,
		Version: vf.Version,
	}

	// Proposed config when config missing.
	if !out.ConfigPresent {
		relPath, relErr := filepath.Rel(cfgRoot, vf.Path)
		if relErr != nil {
			relPath = vf.Path
		}
		out.ProposedConfig = map[string]any{
			"mode":        "file",
			"versionFile": relPath,
			"fileType":    vf.Type,
			"changelog":   fileExists(filepath.Join(cfgRoot, "CHANGELOG.md")),
		}
	}

	// Fetch tags from remote (best effort).
	if fetchErr := gitx.FetchTags(gitRoot); fetchErr != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("fetchTags: %s", fetchErr.Error()))
	}

	// Tags (use TagList for release tags, AllSemverTags for RC scanning).
	releaseTags, err := gitx.TagList(gitRoot)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("tags: %s", err.Error()))
	}

	allTags, err := gitx.AllSemverTags(gitRoot)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("allTags: %s", err.Error()))
	}
	if allTags != nil {
		out.Tags.All = allTags
	}
	if len(allTags) > 0 {
		out.Tags.Latest = allTags[0]
	}

	// Tag prefix: config > detect from tags > "v" default.
	tagPrefix := tagPrefixFromConfig
	if tagPrefix == "" && len(allTags) > 0 {
		tagPrefix = detectTagPrefix(allTags)
	}
	out.Tags.TagPrefix = tagPrefix

	atHead, err := gitx.TagsAtHead(gitRoot)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("tagsAtHead: %s", err.Error()))
	} else if atHead != nil {
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

	// Bump base: max(fileVersion, highestRemoteTag).
	bumpBase := vf.Version
	highestTag := prReleaseHighestTagVersion(releaseTags, tagPrefix)
	if highestTag != "" && prReleaseSemverGreater(highestTag, bumpBase) {
		out.VersionDivergence = &DivergenceInfo{
			FileVersion: vf.Version,
			TagVersion:  highestTag,
			Message:     fmt.Sprintf("file version %s is behind remote tag %s; bump base uses tag version", vf.Version, highestTag),
		}
		out.Warnings = append(out.Warnings, out.VersionDivergence.Message)
		bumpBase = highestTag
	}

	// Bump options for standard levels, computed from bumpBase.
	bumpVF := &version.VersionFile{Version: bumpBase}
	existingRCs := make(map[string][]string)
	for _, level := range []string{"major", "minor", "patch"} {
		result, bErr := version.Bump(bumpVF, level)
		if bErr != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("bump %s: %s", level, bErr.Error()))
			continue
		}
		// Compute RC next.
		rcNum := prReleaseFindNextRC(allTags, tagPrefix, result)
		rcNext := result + "-rc" + strconv.Itoa(rcNum)

		opt := VersionBumpOption{
			Level:   level,
			Result:  result,
			Current: bumpBase,
			RCNext:  rcNext,
		}
		out.BumpOptions = append(out.BumpOptions, opt)

		// Collect existing RCs for this target.
		needle := tagPrefix + result + "-rc"
		var rcs []string
		for _, t := range allTags {
			if strings.HasPrefix(t, needle) {
				rcs = append(rcs, t)
			}
		}
		if len(rcs) > 0 {
			existingRCs[result] = rcs
		}
	}
	if len(existingRCs) > 0 {
		out.ExistingRCs = existingRCs
	}

	// Commits since last tag.
	latestForLog := ""
	if len(releaseTags) > 0 {
		latestForLog = releaseTags[0]
	}
	if latestForLog != "" {
		logOut, logErr := execx.Run("git", []string{
			"log", "--oneline", latestForLog + "..HEAD",
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

	// Changelog existence check (use config changelogFile when set).
	clFile := "CHANGELOG.md"
	if changelogFile != "" {
		clFile = changelogFile
	}
	changelogPath := filepath.Join(cfgRoot, clFile)
	out.ChangelogExists = fileExists(changelogPath)

	// Summary, actions, next.
	out.Summary, out.Actions, out.Next = versionPrepareSummary(out)

	return out, nil
}

// versionPrepareSummary derives summary text, action list, and next-step
// recommendation from the prepare output state.
func versionPrepareSummary(out VersionPrepareOut) (string, []string, string) {
	var parts []string
	var actions []string

	if out.VersionSource != nil {
		parts = append(parts, fmt.Sprintf("version %s from %s", out.VersionSource.Version, out.VersionSource.Type))
	}
	if out.VersionDivergence != nil {
		parts = append(parts, fmt.Sprintf("divergence: file=%s tag=%s", out.VersionDivergence.FileVersion, out.VersionDivergence.TagVersion))
	}
	if out.Idempotency.AlreadyBumped {
		parts = append(parts, fmt.Sprintf("already tagged at HEAD: %s", out.Idempotency.TagAtHead))
		return strings.Join(parts, "; "), actions, ""
	}
	if out.HasDirtyFiles {
		actions = append(actions, "commit or stash dirty files before releasing")
	}
	if !out.ConfigPresent {
		actions = append(actions, "add version config section to .sdlc-v2/config.json")
	}

	suggest := "patch"
	if out.ConventionalSummary != nil {
		suggest = out.ConventionalSummary.Suggest
	}
	parts = append(parts, fmt.Sprintf("conventional suggest: %s", suggest))

	next := ""
	if !out.Idempotency.AlreadyBumped && !out.HasDirtyFiles {
		next = fmt.Sprintf("version_apply {\"level\":\"%s\"}", suggest)
	}
	if out.HasDirtyFiles {
		next = "commit dirty files first"
	}

	return strings.Join(parts, "; "), actions, next
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
		`Gather comprehensive version context for the current project.

Returns: current version from the detected version file (config-driven via
version.DetectAt when a version config section exists, falling back to
root-directory probing), bump options for major/minor/patch with RC-next
candidates, all semver tags (fetched from remote first), conventional commit
analysis since the last tag, changelog status, idempotency detection (tag at
HEAD), dirty-file list, default-branch detection, version divergence between
the file and the highest remote tag, and existing RC tags per bump target.

When no version config section is found in .sdlc-v2/config.json, a
proposedConfig map is returned so the caller can offer to write it.

mode:"tag" in the config is rejected with an actionable error (not yet
supported).

Fields: errors, warnings, flow, currentBranch, configPresent, versionConfig,
proposedConfig, versionSource, bumpOptions (with rcNext), tags, commitsSinceTag,
conventionalSummary, changelogExists, idempotency, dirtyFiles, hasDirtyFiles,
defaultBranch, onDefaultBranch, versionDivergence, existingRCs, summary,
actions, next.`,
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
