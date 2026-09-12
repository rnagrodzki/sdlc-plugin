package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/setupmeta"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// setup_prepare
// ---------------------------------------------------------------------------

// SetupPrepareIn is the input for the setup_prepare tool.
type SetupPrepareIn struct {
	SkipConfigCheck bool `json:"skipConfigCheck" jsonschema_description:"Skips the config-version auto-migration gate normally run before preflight checks. Set only when the caller has already verified or migrated the config."`
}

// sectionRow is a JSON-friendly projection of setupmeta.Section with
// camelCase tags.
type sectionRow struct {
	ID              string     `json:"id"`
	Label           string     `json:"label"`
	Purpose         string     `json:"purpose"`
	ConfigFile      string     `json:"configFile"`
	ConfigPath      string     `json:"configPath"`
	ConsumedBy      []string   `json:"consumedBy"`
	FilesModified   []string   `json:"filesModified"`
	Optional        bool       `json:"optional"`
	DelegatedTo     string     `json:"delegatedTo,omitempty"`
	ConfirmDetected bool       `json:"confirmDetected"`
	Fields          []fieldRow `json:"fields"`
}

// fieldRow is a JSON-friendly projection of setupmeta.Field with camelCase
// tags.
type fieldRow struct {
	Name                  string   `json:"name"`
	Label                 string   `json:"label"`
	Type                  string   `json:"type"`
	Options               []string `json:"options,omitempty"`
	Default               any      `json:"default,omitempty"`
	Description           string   `json:"description"`
	Min                   *int     `json:"min,omitempty"`
	Max                   *int     `json:"max,omitempty"`
	WhenStepInActiveSteps string   `json:"whenStepInActiveSteps,omitempty"`
}

// SetupPrepareOut is the output for the setup_prepare tool.
type SetupPrepareOut struct {
	OK             bool         `json:"ok"`
	NeedsMigration bool         `json:"needsMigration"`
	Sections       []sectionRow `json:"sections"`
	DefaultBranch  string       `json:"defaultBranch,omitempty"`
	RemoteOwner    string       `json:"remoteOwner,omitempty"`
}

// setupPrepare is the core logic, separated from the handler for testability.
func setupPrepare(root string, in SetupPrepareIn) (SetupPrepareOut, error) {
	// Check migration state (best-effort, never a tool error).
	needsMigration := false
	if !in.SkipConfigCheck {
		if err := configmigrate.Verify(root); err != nil {
			needsMigration = true
		}
	}

	// Convert setupmeta.Sections() to JSON-friendly rows.
	meta := setupmeta.Sections()
	rows := make([]sectionRow, len(meta))
	for i, s := range meta {
		fields := make([]fieldRow, len(s.Fields))
		for j, f := range s.Fields {
			fields[j] = fieldRow{
				Name:                  f.Name,
				Label:                 f.Label,
				Type:                  f.Type,
				Options:               f.Options,
				Default:               f.Default,
				Description:           f.Description,
				Min:                   f.Min,
				Max:                   f.Max,
				WhenStepInActiveSteps: f.WhenStepInActiveSteps,
			}
		}
		rows[i] = sectionRow{
			ID:              s.ID,
			Label:           s.Label,
			Purpose:         s.Purpose,
			ConfigFile:      s.ConfigFile,
			ConfigPath:      s.ConfigPath,
			ConsumedBy:      s.ConsumedBy,
			FilesModified:   s.FilesModified,
			Optional:        s.Optional,
			DelegatedTo:     s.DelegatedTo,
			ConfirmDetected: s.ConfirmDetected,
			Fields:          fields,
		}
	}

	// Best-effort runtime defaults: defaultBranch and remoteOwner.
	defaultBranch, _ := gitx.DefaultBranch(root)
	var remoteOwner string
	originURL, err := execx.Run("git", []string{"remote", "get-url", "origin"}, execx.Options{Dir: root})
	if err == nil && originURL != "" {
		owner, _, parseErr := ghx.ParseRemoteOwner(originURL)
		if parseErr == nil {
			remoteOwner = owner
		}
	}

	return SetupPrepareOut{
		OK:             true,
		NeedsMigration: needsMigration,
		Sections:       rows,
		DefaultBranch:  defaultBranch,
		RemoteOwner:    remoteOwner,
	}, nil
}

// ---------------------------------------------------------------------------
// setup_init
// ---------------------------------------------------------------------------

// SetupInitIn is the input for the setup_init tool. It takes no fields:
// setup_init always writes the complete config.toml/local.toml templates
// (every field, heavily commented) rather than seeding a caller-selected
// subset of sections — see configTemplate/localTemplate below.
type SetupInitIn struct{}

// SetupInitOut is the output for the setup_init tool.
type SetupInitOut struct {
	OK      bool     `json:"ok"`
	Created []string `json:"created"`
	Changed []string `json:"changed"`
	Next    string   `json:"next"`
	Errors  []string `json:"errors,omitempty"`
}

// Managed-block markers for .sdlc-v2/.gitignore.
const (
	sdlcGitignoreBegin = "# >>> sdlc-v2 managed (do not edit) — selective ignores"
	sdlcGitignoreEnd   = "# <<< sdlc-v2 managed"
)

// sdlcGitignorePatterns are the deny-all + allowlist patterns inside
// .sdlc-v2/.gitignore, mirroring SDLC_GITIGNORE_PATTERNS from the JS source.
var sdlcGitignorePatterns = []string{
	"*",
	"!.gitignore",
	"!config.toml",
	"!review-dimensions/",
	"!review-dimensions/**",
}

// Managed-block markers for root .gitignore (v3).
const (
	rootGitignoreBegin   = "# >>> sdlc-v2 managed v3 (do not edit) — transient skill artifacts"
	rootGitignoreEnd     = "# <<< sdlc-v2 managed"
	rootGitignoreBeginV2 = "# >>> sdlc-v2 managed v2 (do not edit) — transient skill artifacts and .sdlc/ runtime"
	rootGitignoreBeginV1 = "# >>> sdlc-v2 managed (do not edit) — transient skill artifacts"
)

// rootGitignorePatterns are the transient-artifact glob families managed
// in the consumer project root .gitignore.
var rootGitignorePatterns = []string{
	"*-context-*.json",
	"*-manifest-*.json",
	"*-prepare-*.json",
}

// ensureManagedBlock reads a gitignore file, strips any existing managed
// block (identified by beginMarker/endMarker or legacy markers), appends the
// new managed block, and writes the result. Returns "created", "updated", or
// "unchanged".
func ensureManagedBlock(path, beginMarker, endMarker string, patterns []string, legacyBeginMarkers []string) (string, error) {
	managedBlock := beginMarker + "\n" + strings.Join(patterns, "\n") + "\n" + endMarker

	existing := ""
	fileExisted := false
	data, err := os.ReadFile(path)
	if err == nil {
		existing = string(data)
		fileExisted = true
	}

	// Split into lines; strip trailing empty line from final newline.
	var lines []string
	if existing != "" {
		lines = strings.Split(existing, "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}

	// Build set of all begin markers (current + legacy) for removal.
	beginMarkers := map[string]bool{beginMarker: true}
	for _, m := range legacyBeginMarkers {
		beginMarkers[m] = true
	}

	// Build a set of managed patterns for legacy raw-pattern removal.
	patternSet := make(map[string]bool, len(patterns))
	for _, p := range patterns {
		patternSet[p] = true
	}

	// Remove existing managed blocks and legacy raw patterns.
	var otherLines []string
	insideBlock := false
	for _, line := range lines {
		if beginMarkers[line] {
			insideBlock = true
			continue
		}
		if line == endMarker {
			insideBlock = false
			continue
		}
		if insideBlock {
			continue
		}
		// Drop "other" lines matching managed patterns (legacy raw lines).
		if patternSet[strings.TrimSpace(line)] {
			continue
		}
		otherLines = append(otherLines, line)
	}

	// Normalize blank lines: trim leading/trailing, collapse consecutive.
	otherLines = normalizeBlankLines(otherLines)

	// Reconstruct: user lines + managed block.
	var next string
	if len(otherLines) > 0 {
		next = strings.Join(otherLines, "\n") + "\n" + managedBlock + "\n"
	} else {
		next = managedBlock + "\n"
	}

	if next == existing {
		return "unchanged", nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}

	if fileExisted {
		return "updated", nil
	}
	return "created", nil
}

// normalizeBlankLines trims leading/trailing blanks and collapses consecutive
// blank lines to at most one. Mirrors the JS normalizeBlankLines helper.
func normalizeBlankLines(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	isBlank := func(s string) bool {
		return strings.TrimSpace(s) == ""
	}

	// Trim leading blanks.
	start := 0
	for start < len(lines) && isBlank(lines[start]) {
		start++
	}
	// Trim trailing blanks.
	end := len(lines) - 1
	for end >= start && isBlank(lines[end]) {
		end--
	}
	if end < start {
		return nil
	}

	var out []string
	prevBlank := false
	for i := start; i <= end; i++ {
		blank := isBlank(lines[i])
		if blank {
			if prevBlank {
				continue
			}
			out = append(out, "")
			prevBlank = true
		} else {
			out = append(out, lines[i])
			prevBlank = false
		}
	}
	return out
}

// configTemplate is the complete .sdlc-v2/config.toml scaffold written
// verbatim by setup_init. It carries every project-level field (the
// AllowedProjectKeys whitelist in internal/config/schema.go: version, jira,
// commit, pr, plan, execute) with inline comments documenting valid values,
// defaults, and purpose, plus example guardrail entries. There is no
// interactive Q&A step any more — the user edits this file directly, then
// runs the validate tool. jira is left uncommented (with empty string
// values) rather than commented out: TestConfigTemplateKeysMatchWhitelist
// requires every AllowedProjectKeys entry to actually parse out of this
// template, not just be mentioned in a comment.
const configTemplate = `# ─── SDLC Project Configuration (v1) ─────────────────────────────
# Shared across the team — committed to version control.
# Edit this file directly, then run the validate tool to check.
# Delete sections you don't use.

# ─── Versioning ───────────────────────────────────────────────────

[version]
# Current pre-release tag (free-form string, e.g. "rc", "beta", "alpha")
preRelease = "rc"

# When to apply pre-release tags.
# Valid: "always-rc" | "continue-rc" | "never"
#   always-rc    — every release gets an rc tag
#   continue-rc  — only if already in rc
#   never        — skip pre-release, go straight to release
preReleasePolicy = "always-rc"

# How versions reach the remote.
# Valid: "push" | "pr"
#   push — direct push to default branch
#   pr   — create a pull request
method = "push"

[version.tag]
# Create git tags for releases.
enabled = true
# Tag prefix (e.g. "v" → "v1.2.3", "" → "1.2.3")
prefix = "v"

[version.versionFile]
# Write version number to a file on release.
enabled = true
# Path relative to repo root.
path = ""
# Valid: "package.json" | "plugin.json" | "version.txt" | "pyproject.toml"
fileType = ""

[version.changelog]
# Auto-generate changelog from conventional commits.
enabled = true
# Changelog file path relative to repo root.
file = "CHANGELOG.md"

# ─── Jira integration (optional — clear the fields below if not using Jira) ──────

[jira]
# Jira instance hostname (e.g. "mycompany.atlassian.net")
host = ""
# Jira project key (e.g. "PROJ")
projectKey = ""
# Custom field ID for Epic Link (find in Jira admin → custom fields)
epicFieldId = ""

# ─── Commit conventions ──────────────────────────────────────────

[commit]
# Enforce conventional commits (type(scope): description).
conventional = true
# Require scope in commit messages.
scopeRequired = false
# Allowed commit types.
allowedTypes = ["feat", "fix", "chore", "docs", "refactor", "test", "ci", "perf"]
# Allowed scopes (empty = any scope accepted).
allowedScopes = []

# ─── Pull request defaults ───────────────────────────────────────

[pr]
# PR body template (path relative to repo root, or empty for default).
template = ""
# Labels to apply to PRs created by /ship.
labels = []

# ─── Plan guardrails ─────────────────────────────────────────────
# Constraints enforced during plan creation.
# ID is the table key — TOML enforces uniqueness, no duplicates possible.
# severity: "error" (blocking, plan fails) or "warning" (advisory, shown but not blocking).
#
# Add your own guardrails as new [plan.guardrails.<your-id>] tables.
# Example:
#
# [plan.guardrails.my-custom-rule]
# severity = "warning"
# description = "Explain what this guardrail enforces."

[plan.guardrails.test-coverage-required]
severity = "error"
description = """
Every task that creates or modifies source code \
must include corresponding test cases."""

[plan.guardrails.no-ci-bypass]
severity = "error"
description = "Plans must not include steps that skip or disable CI checks."

# ─── Execute guardrails ──────────────────────────────────────────
# Constraints enforced during task execution.
# Same format as plan guardrails.

[execute.guardrails.independent-completion-reverification]
severity = "error"
description = """
Never record task-done from a self-report alone. \
Always independently confirm via git diff and re-run \
of build/vet/test commands."""
`

// localTemplate is the complete .sdlc-v2/local.toml scaffold written
// verbatim by setup_init. It is gitignored (personal preferences, not a
// team contract). automation is included as a commented-out example block
// — it's genuinely optional and off by default, unlike jira/plan/execute
// above which stay live so config.Read's project-key whitelist check
// always sees every AllowedProjectKeys entry actually present.
const localTemplate = `# ─── SDLC Local Configuration (v1) ───────────────────────────────
# Personal preferences — gitignored, not shared with the team.
# Edit this file directly, then run the validate tool to check.

# ─── Ship pipeline ───────────────────────────────────────────────

[ship]
# Auto-run full pipeline without confirmation prompts.
auto = true

# Default version bump.
# Valid: "major" | "minor" | "patch"
bump = "patch"

# Create PR as draft.
draft = false

# Review quality level.
# Valid: "balanced" | "full" | "minimal"
#   balanced — standard review depth
#   full     — thorough, slower review
#   minimal  — quick scan only
quality = "balanced"

# Rebase strategy before push/PR.
# Valid: "auto" | "always" | "never"
rebase = "auto"

# Minimum review severity to block ship.
# Valid: "low" | "medium" | "high" | "critical"
reviewThreshold = "low"

# Pipeline steps to execute during /ship (in order).
# Valid steps: "execute" | "commit" | "review" | "pr" | "verify-pipeline"
steps = ["execute", "commit", "review", "pr", "verify-pipeline"]

# Quick mode steps (subset of steps, for /ship --quick).
quick = ["execute", "commit", "review"]

# ─── Polling intervals (seconds) ─────────────────────────────────

# Execute wave polling.
executeWaveInterval = 60
executeWaveTimeout = 1800

# CI/CD pipeline verification polling.
verifyPipelineInterval = 60
verifyPipelineMaxIterations = 3
verifyPipelineTimeout = 1200

# Remote review (e.g. GitHub Copilot) polling.
awaitRemoteReviewers = []
awaitRemoteReviewInterval = 60
awaitRemoteReviewTimeout = 600

# ─── Plan narrative style ────────────────────────────────────────

[planStyle]
# Valid: "technical" | "executive" | "mixed"
audience = "technical"
# Valid: "terse" | "normal" | "verbose"
verbosity = "terse"
# Free-form rules for plan narrative generation.
narrativeRules = [
  "Give enough background so someone without prior context can judge the change.",
  "Use plain, simple English suited for non-native speakers.",
]

# ─── Automation (optional — delete if not using) ──────────────────

# [automation]
# # Valid: "full" | "supervised" | "off"
# mode = "supervised"
#
# [automation.drift]
# enabled = false
# intervalMinutes = 30
#
# [automation.report]
# enabled = false
# format = "markdown"
#
# [automation.push]
# enabled = false
# requireReview = true
`

// setupInit is the core logic, separated from the handler for testability.
func setupInit(root string, in SetupInitIn) (SetupInitOut, error) {
	created := []string{}
	changed := []string{}
	var errs []string

	sdlcDir := filepath.Join(root, paths.DataDir)

	// 1. Create .sdlc-v2/ directory.
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		return SetupInitOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("create %s directory: %s", paths.DataDir, err.Error()),
			Cause: err,
		}
	}

	// 1b. Create .sdlc-v2/runs/ directory. state.Init also creates this
	// lazily on first run, but scaffolding it eagerly here makes it
	// discoverable right after setup, matching the other managed
	// subdirectories setup owns (e.g. review-dimensions/).
	if err := os.MkdirAll(filepath.Join(sdlcDir, paths.RunsSubdir), 0o755); err != nil {
		return SetupInitOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("create %s/%s directory: %s", paths.DataDir, paths.RunsSubdir, err.Error()),
			Cause: err,
		}
	}

	// 2. Ensure .sdlc-v2/.gitignore with managed block.
	sdlcGitignorePath := filepath.Join(sdlcDir, ".gitignore")
	action, err := ensureManagedBlock(
		sdlcGitignorePath,
		sdlcGitignoreBegin,
		sdlcGitignoreEnd,
		sdlcGitignorePatterns,
		nil,
	)
	if err != nil {
		errs = append(errs, fmt.Sprintf("%s/.gitignore: %s", paths.DataDir, err.Error()))
	} else {
		switch action {
		case "created":
			created = append(created, paths.DataDir+"/.gitignore")
		case "updated":
			changed = append(changed, paths.DataDir+"/.gitignore")
		}
	}

	// 3. Ensure root .gitignore with managed block.
	rootGitignorePath := filepath.Join(root, ".gitignore")
	action, err = ensureManagedBlock(
		rootGitignorePath,
		rootGitignoreBegin,
		rootGitignoreEnd,
		rootGitignorePatterns,
		[]string{rootGitignoreBeginV1, rootGitignoreBeginV2},
	)
	if err != nil {
		errs = append(errs, fmt.Sprintf(".gitignore: %s", err.Error()))
	} else {
		switch action {
		case "created":
			created = append(created, ".gitignore")
		case "updated":
			changed = append(changed, ".gitignore")
		}
	}

	// 4. Write the complete config.toml/local.toml templates directly to
	//    disk — never through LLM context. Every field, with inline
	//    documentation, is dropped in one shot; there is no more
	//    per-section interactive seeding. Idempotent: an existing file is
	//    left untouched so a re-run of /setup never clobbers a user's
	//    edits.
	configPath := filepath.Join(sdlcDir, "config.toml")
	if _, statErr := os.Stat(configPath); statErr != nil {
		if err := os.WriteFile(configPath, []byte(configTemplate), 0o644); err != nil {
			errs = append(errs, fmt.Sprintf("config.toml: %s", err.Error()))
		} else {
			created = appendIfNew(created, paths.DataDir+"/config.toml")
		}
	}

	localPath := filepath.Join(sdlcDir, "local.toml")
	if _, statErr := os.Stat(localPath); statErr != nil {
		if err := os.WriteFile(localPath, []byte(localTemplate), 0o644); err != nil {
			errs = append(errs, fmt.Sprintf("local.toml: %s", err.Error()))
		} else {
			created = appendIfNew(created, paths.DataDir+"/local.toml")
		}
	}

	out := SetupInitOut{
		OK:      len(errs) == 0,
		Created: created,
		Changed: changed,
		Next:    "config.toml and local.toml created — instruct the user to edit them by hand, then run the validate tool.",
	}
	if len(errs) > 0 {
		out.Errors = errs
	}
	return out, nil
}

// appendIfNew appends s to slice only if not already present.
func appendIfNew(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterSetupTools registers setup_prepare and setup_init on the server.
func RegisterSetupTools(s *mcpserver.Server) {
	mcpserver.Register(s, "setup_prepare",
		"Returns the canonical section descriptors for setup, with per-section field metadata and runtime-detected defaults (defaultBranch, remoteOwner). Optionally checks config migration state.",
		func(ctx mcpserver.Ctx, in SetupPrepareIn) (SetupPrepareOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				// Fallback to cwd — setup must work before any context exists.
				root, err = os.Getwd()
				if err != nil {
					return SetupPrepareOut{}, &mcpserver.InfraError{
						Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
						Cause: err,
					}
				}
			}
			return setupPrepare(root, in)
		},
	)

	mcpserver.Register(s, "setup_init",
		"Creates the .sdlc-v2/ directory scaffold for a v1 (TOML) config: .sdlc-v2/.gitignore, root .gitignore managed block, config.toml, and local.toml. Writes the complete, heavily-commented templates directly to disk (never through LLM context) — every field is present, with inline docs and example guardrails. Idempotent: an existing config.toml/local.toml is left untouched. Instruct the user to edit the files by hand, then run the validate tool.",
		func(ctx mcpserver.Ctx, in SetupInitIn) (SetupInitOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				root, err = os.Getwd()
				if err != nil {
					return SetupInitOut{}, &mcpserver.InfraError{
						Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
						Cause: err,
					}
				}
			}
			return setupInit(root, in)
		},
	)
}
