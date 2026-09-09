// Package config reads and writes the sdlc plugin's two configuration files
// — .sdlc-v2/config.json (project-level, committed) and .sdlc-v2/local.json
// (user-local, gitignored) — anchored at the main worktree root.
//
// This is a clean v5-only implementation: pre-v5 config layouts (individual
// per-section files, schemaVersion-stamped configs) are refused with an
// error naming the "migrate" tool. No legacy fallback readers exist here;
// legacy-to-v5 migration is the migrate engine's responsibility (see
// internal/configmigrate).
//
// Section routing follows the JS precedent (scripts/lib/config.js):
// ProjectSections (version, jira, commit, pr, plan, execute) live in
// config.json; all other sections (ship, review, receivedReview, workspace,
// automation, …) live in local.json.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ErrNotFound is returned when no config file exists at the expected path.
var ErrNotFound = errors.New("config: not found")

// ProjectSections is the set of section names that live in the project
// config (.sdlc-v2/config.json). All other sections live in the local config
// (.sdlc-v2/local.json).
var ProjectSections = map[string]bool{
	"version": true,
	"jira":    true,
	"commit":  true,
	"pr":      true,
	"plan":    true,
	"execute": true,
}

// Quiet suppresses config-read tracing to stderr when set to true.
// Safe to set before the first Read/ReadSection call. Tracing never
// writes to stdout — this binary is an MCP stdio server, and one stray
// stdout byte corrupts the protocol.
var Quiet bool

// traced tracks absolute paths that have already been traced this process
// to avoid duplicate log lines.
var traced = make(map[string]bool)

// resetTrace clears the per-path deduplication set. Used by tests.
func resetTrace() {
	traced = make(map[string]bool)
}

// traceRead emits a single-line trace to stderr for a config file access.
// Deduplicates per absolute path. Suppressed when Quiet is true.
func traceRead(absPath string, status string) {
	if Quiet {
		return
	}
	if traced[absPath] {
		return
	}
	traced[absPath] = true
	fmt.Fprintf(os.Stderr, "[sdlc:config] %s %s\n", status, absPath)
}

// legacyMarkers are relative paths (from main worktree root) whose
// existence signals a pre-v5 config layout.
var legacyMarkers = []string{
	filepath.Join(".claude", "sdlc.json"),
	filepath.Join(".claude", "version.json"),
	filepath.Join(paths.LegacyDataDir, "jira-config.json"),
	filepath.Join(paths.LegacyDataDir, "ship-config.json"),
	filepath.Join(paths.LegacyDataDir, "review.json"),
	filepath.Join(".claude", "review.json"),
}

// detectLegacy checks for pre-v5 config files. Returns an error naming
// the migrate tool when any legacy marker is found, nil otherwise.
func detectLegacy(mainRoot string) error {
	for _, marker := range legacyMarkers {
		p := filepath.Join(mainRoot, marker)
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf(
				"config: legacy config layout detected (%s exists); run migrate to upgrade to v5 format",
				marker,
			)
		}
	}
	return nil
}

// AutomationSection controls per-step automation behavior and review-fix
// loop settings.
//
// Mode determines the default for steps not listed in Steps:
//   - "supervised" (default): unlisted steps require confirmation ("confirm")
//   - "unattended": unlisted steps run automatically ("auto")
//
// ReviewFixIterations is the maximum number of review-fix iterations
// (default 3, matching verifyPipelineMaxIterations).
//
// ReviewFixSeverityThreshold is the minimum severity that triggers a fix
// attempt (default "high").
//
// Steps maps individual step names to "auto" or "confirm", overriding
// the Mode default for that step.
type AutomationSection struct {
	Mode                       string            `json:"mode"`
	ReviewFixIterations        int               `json:"reviewFixIterations"`
	ReviewFixSeverityThreshold string            `json:"reviewFixSeverityThreshold"`
	Steps                      map[string]string `json:"steps,omitempty"`
}

// StepMode returns the effective automation mode for the given step:
// the value from Steps if present, otherwise the default derived from Mode
// (supervised → "confirm", unattended → "auto").
func (a *AutomationSection) StepMode(step string) string {
	if a.Steps != nil {
		if m, ok := a.Steps[step]; ok {
			return m
		}
	}
	if a.Mode == "unattended" {
		return "auto"
	}
	return "confirm"
}

// VersionSection controls the version skill's behaviour: where the version
// number lives, how it's read, and whether a changelog is maintained.
//
// Mode determines whether the version is tracked in a file ("file",
// default) or via git tags ("tag").
//
// VersionFile is the path (relative to the main worktree root) to the file
// that stores the version number. FileType names its format
// (package.json, cargo.toml, pyproject.toml, pubspec.yaml, plugin.json,
// version-file).
//
// TagPrefix is prepended to git version tags (e.g. "v").
//
// Changelog toggles changelog maintenance on release. When true and
// ChangelogFile is unset, ChangelogFile defaults to "CHANGELOG.md".
//
// TicketPrefix filters commit messages for the changelog by Jira ticket
// prefix (e.g. "PROJ"). PreRelease is the default pre-release label applied
// when no explicit base bump or --pre is given.
//
// PreReleasePolicy controls whether version_prepare suggests a
// release-candidate build instead of a final release, when the caller
// hasn't said otherwise (e.g. under --auto). One of:
//   - "always-rc": always suggest an RC, regardless of whether the bump
//     target already has RC tags.
//   - "continue-rc" (default): suggest an RC only when the bump target
//     already has one or more existing RC tags — i.e. once a version has
//     an RC out, staying in RC mode is the safer default until something
//     explicitly asks for the final release.
//   - "never": never suggest an RC.
//
// For backward compatibility, config.json may still set the legacy boolean
// "rcAutoContinue" instead of "preReleasePolicy" — see parseVersionSection.
type VersionSection struct {
	Mode             string `json:"mode"`
	VersionFile      string `json:"versionFile"`
	FileType         string `json:"fileType"`
	TagPrefix        string `json:"tagPrefix"`
	Changelog        bool   `json:"changelog"`
	ChangelogFile    string `json:"changelogFile"`
	TicketPrefix     string `json:"ticketPrefix"`
	PreRelease       string `json:"preRelease"`
	PreReleasePolicy string `json:"preReleasePolicy"`
}

// parseVersionSection converts a raw JSON map into a VersionSection,
// applying documented defaults. Returns nil when raw is nil (section
// absent from config.json), mirroring extractSection's absent-section
// semantics so callers can distinguish "no version section configured"
// from "version section configured with defaults".
func parseVersionSection(raw map[string]any) *VersionSection {
	if raw == nil {
		return nil
	}
	v := &VersionSection{}
	if s, ok := raw["mode"].(string); ok {
		v.Mode = s
	}
	if s, ok := raw["versionFile"].(string); ok {
		v.VersionFile = s
	}
	if s, ok := raw["fileType"].(string); ok {
		v.FileType = s
	}
	if s, ok := raw["tagPrefix"].(string); ok {
		v.TagPrefix = s
	}
	if b, ok := raw["changelog"].(bool); ok {
		v.Changelog = b
	}
	if s, ok := raw["changelogFile"].(string); ok {
		v.ChangelogFile = s
	}
	if s, ok := raw["ticketPrefix"].(string); ok {
		v.TicketPrefix = s
	}
	if s, ok := raw["preRelease"].(string); ok {
		v.PreRelease = s
	}
	if s, ok := raw["preReleasePolicy"].(string); ok && s != "" {
		v.PreReleasePolicy = s
	} else if b, ok := raw["rcAutoContinue"].(bool); ok {
		// Backward compat: old boolean rcAutoContinue maps onto the new enum.
		if b {
			v.PreReleasePolicy = "continue-rc"
		} else {
			v.PreReleasePolicy = "never"
		}
	} else {
		v.PreReleasePolicy = "continue-rc"
	}
	applyVersionDefaults(v)
	return v
}

// applyVersionDefaults fills in zero-value fields with documented
// defaults: mode "file", and changelogFile "CHANGELOG.md" when changelog
// is enabled but no explicit path was given.
func applyVersionDefaults(v *VersionSection) {
	if v.Mode == "" {
		v.Mode = "file"
	}
	if v.Changelog && v.ChangelogFile == "" {
		v.ChangelogFile = "CHANGELOG.md"
	}
}

// Config is the merged view of .sdlc-v2/config.json (project-level sections)
// and .sdlc-v2/local.json (user-local sections).
type Config struct {
	// Project-level sections (from .sdlc-v2/config.json).
	Version *VersionSection
	Jira    map[string]any
	Commit  map[string]any
	PR      map[string]any
	Plan    map[string]any
	Execute map[string]any

	// Local sections (from .sdlc-v2/local.json).
	Ship           map[string]any
	Review         map[string]any
	ReceivedReview map[string]any
	Workspace      map[string]any
	Automation     *AutomationSection
}

// extractSection retrieves a named key from a parsed JSON map and returns
// it as map[string]any. Returns nil if the key is absent or the value is
// not a JSON object.
func extractSection(raw map[string]any, name string) map[string]any {
	if raw == nil {
		return nil
	}
	v, ok := raw[name]
	if !ok {
		return nil
	}
	section, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return section
}

// parseAutomation converts a raw JSON map into an AutomationSection.
func parseAutomation(raw map[string]any) *AutomationSection {
	a := &AutomationSection{}
	if v, ok := raw["mode"].(string); ok {
		a.Mode = v
	}
	if v, ok := raw["reviewFixIterations"].(float64); ok {
		a.ReviewFixIterations = int(v)
	}
	if v, ok := raw["reviewFixSeverityThreshold"].(string); ok {
		a.ReviewFixSeverityThreshold = v
	}
	if steps, ok := raw["steps"].(map[string]any); ok {
		a.Steps = make(map[string]string, len(steps))
		for k, v := range steps {
			if s, ok := v.(string); ok {
				a.Steps[k] = s
			}
		}
	}
	return a
}

// applyAutomationDefaults fills in zero-value fields with documented
// defaults: mode "supervised", reviewFixIterations 3,
// reviewFixSeverityThreshold "high".
func applyAutomationDefaults(a *AutomationSection) {
	if a.Mode == "" {
		a.Mode = "supervised"
	}
	if a.ReviewFixIterations == 0 {
		a.ReviewFixIterations = 3
	}
	if a.ReviewFixSeverityThreshold == "" {
		a.ReviewFixSeverityThreshold = "high"
	}
}

// readProjectRaw reads .sdlc-v2/config.json and returns its contents as a
// raw map. When config.json is missing, checks for legacy layout markers
// and returns either a legacy-refusal error (naming "migrate") or
// ErrNotFound. When config.json exists with a schemaVersion field (the v4
// marker), returns a legacy-refusal error.
func readProjectRaw(mainRoot string) (map[string]any, error) {
	projectPath := filepath.Join(mainRoot, paths.DataDir, "config.json")
	var raw map[string]any
	err := fsx.ReadJSON(projectPath, &raw)
	if err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			if legacyErr := detectLegacy(mainRoot); legacyErr != nil {
				return nil, legacyErr
			}
			return nil, fmt.Errorf("config: %s: %w", projectPath, ErrNotFound)
		}
		return nil, fmt.Errorf("config: %w", err)
	}

	// config.json exists — check for v4 marker.
	if _, hasSchemaVersion := raw["schemaVersion"]; hasSchemaVersion {
		return nil, fmt.Errorf(
			"config: %s has schemaVersion field (pre-v5 format); run migrate to upgrade",
			projectPath,
		)
	}

	traceRead(projectPath, "read")
	return raw, nil
}

// readLocalRaw reads .sdlc-v2/local.json and returns its contents as a raw
// map. Returns (nil, nil) when the file does not exist — missing local
// config is not an error.
func readLocalRaw(mainRoot string) (map[string]any, error) {
	localPath := filepath.Join(mainRoot, paths.DataDir, "local.json")
	var raw map[string]any
	err := fsx.ReadJSON(localPath, &raw)
	if err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			traceRead(localPath, "read-miss")
			return nil, nil
		}
		return nil, fmt.Errorf("config: %w", err)
	}
	traceRead(localPath, "read")
	return raw, nil
}

// Read loads the full merged configuration from .sdlc-v2/config.json (project
// sections) and .sdlc-v2/local.json (local sections) anchored at mainRoot.
//
// The mainRoot parameter should be the main worktree root, typically
// obtained via worktree.MainRoot(). This ensures that config reads anchor
// at the main worktree even when called from a linked worktree.
//
// Returns ErrNotFound when .sdlc-v2/config.json does not exist and no legacy
// layout is detected. Returns an error naming the "migrate" tool when a
// pre-v5 config layout is detected. Validates the project config's
// top-level keys against the v5 schema.
func Read(mainRoot string) (*Config, error) {
	projectRaw, err := readProjectRaw(mainRoot)
	if err != nil {
		return nil, err
	}

	if err := validateProjectKeys(projectRaw); err != nil {
		return nil, err
	}

	localRaw, err := readLocalRaw(mainRoot)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Version: parseVersionSection(extractSection(projectRaw, "version")),
		Jira:    extractSection(projectRaw, "jira"),
		Commit:  extractSection(projectRaw, "commit"),
		PR:      extractSection(projectRaw, "pr"),
		Plan:    extractSection(projectRaw, "plan"),
		Execute: extractSection(projectRaw, "execute"),

		Ship:           extractSection(localRaw, "ship"),
		Review:         extractSection(localRaw, "review"),
		ReceivedReview: extractSection(localRaw, "receivedReview"),
		Workspace:      extractSection(localRaw, "workspace"),
	}

	if autoRaw := extractSection(localRaw, "automation"); autoRaw != nil {
		cfg.Automation = parseAutomation(autoRaw)
	}
	if cfg.Automation == nil {
		cfg.Automation = &AutomationSection{}
	}
	applyAutomationDefaults(cfg.Automation)

	return cfg, nil
}

// ReadSection reads a single config section by name, routing to the
// appropriate file based on ProjectSections membership.
//
// For project sections (version, jira, commit, pr, plan, execute), reads
// .sdlc-v2/config.json. For all other sections, reads .sdlc-v2/local.json.
//
// Returns ErrNotFound when the file or section does not exist. Returns a
// legacy refusal error (naming "migrate") when the project config layout
// is pre-v5 and a project section is requested.
func ReadSection(mainRoot, name string) (map[string]any, error) {
	if ProjectSections[name] {
		projectRaw, err := readProjectRaw(mainRoot)
		if err != nil {
			return nil, err
		}
		section := extractSection(projectRaw, name)
		if section == nil {
			return nil, fmt.Errorf("config: section %q: %w", name, ErrNotFound)
		}
		return section, nil
	}

	// Local section.
	localPath := filepath.Join(mainRoot, paths.DataDir, "local.json")
	var localRaw map[string]any
	if err := fsx.ReadJSON(localPath, &localRaw); err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			return nil, fmt.Errorf("config: %s: %w", localPath, ErrNotFound)
		}
		return nil, fmt.Errorf("config: %w", err)
	}
	traceRead(localPath, "read")

	section := extractSection(localRaw, name)
	if section == nil {
		return nil, fmt.Errorf("config: section %q: %w", name, ErrNotFound)
	}
	return section, nil
}

// WriteSection writes a single config section, routing to the appropriate
// file based on ProjectSections membership. Uses read-merge-write to avoid
// clobbering other sections. Creates the .sdlc-v2 directory if needed.
//
// For project sections, validates the merged result against the v5 schema
// before writing. Writes use fsx.AtomicWriteJSON for crash safety.
func WriteSection(mainRoot, name string, v map[string]any) error {
	sdlcDir := filepath.Join(mainRoot, paths.DataDir)
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		return fmt.Errorf("config: create .sdlc-v2 dir: %w", err)
	}

	if ProjectSections[name] {
		configPath := filepath.Join(sdlcDir, "config.json")
		var existing map[string]any
		if err := fsx.ReadJSON(configPath, &existing); err != nil {
			if errors.Is(err, fsx.ErrNotFound) {
				existing = make(map[string]any)
			} else {
				return fmt.Errorf("config: %w", err)
			}
		}
		existing[name] = v
		if err := validateProjectKeys(existing); err != nil {
			return err
		}
		traceRead(configPath, "write")
		return fsx.AtomicWriteJSON(configPath, existing)
	}

	// Local section.
	localPath := filepath.Join(sdlcDir, "local.json")
	var existing map[string]any
	if err := fsx.ReadJSON(localPath, &existing); err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			existing = make(map[string]any)
		} else {
			return fmt.Errorf("config: %w", err)
		}
	}
	existing[name] = v
	traceRead(localPath, "write")
	return fsx.AtomicWriteJSON(localPath, existing)
}
