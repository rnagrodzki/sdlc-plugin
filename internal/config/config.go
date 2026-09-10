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

// VersionSection controls the version skill's behaviour via three
// independently toggleable release paths — tag, versionFile, changelog —
// that share only bump policy (PreRelease, PreReleasePolicy) and delivery
// Method. Each path is enabled or disabled explicitly; a missing
// sub-object means that path is disabled ({Enabled: false}), never an
// ambiguous default.
//
// Method controls how the versionFile and changelog paths deliver their
// writes when enabled. One of:
//   - "push" (default): commit and push directly to main.
//   - "pr": open a release PR instead of pushing directly to main.
//
// PreRelease is the default pre-release label applied when no explicit
// base bump or --pre is given.
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
// This is a breaking config shape: the old flat shape (top-level "mode",
// string "versionFile", "changelogMethod"/"changelog" boolean,
// "rcAutoContinue" boolean) is rejected outright by parseVersionSection —
// there is no backward-compat reader. Run /setup --only version to
// migrate.
type VersionSection struct {
	PreRelease       string                 `json:"preRelease"`
	PreReleasePolicy string                 `json:"preReleasePolicy"`
	Method           string                 `json:"method"`
	Tag              VersionTagConfig       `json:"tag"`
	VersionFile      VersionFileConfig      `json:"versionFile"`
	Changelog        VersionChangelogConfig `json:"changelog"`
}

// VersionTagConfig is the tag release path: creating a git tag (and GitHub
// Release) on version bump. Prefix is prepended to the tag name (e.g. "v").
type VersionTagConfig struct {
	Enabled bool   `json:"enabled"`
	Prefix  string `json:"prefix"`
}

// VersionFileConfig is the version-file release path: writing the bumped
// version into a tracked file. Path is relative to the main worktree root.
// FileType names its format (package.json, cargo.toml, pyproject.toml,
// pubspec.yaml, plugin.json, version-file).
type VersionFileConfig struct {
	Enabled  bool   `json:"enabled"`
	Path     string `json:"path"`
	FileType string `json:"fileType"`
}

// VersionChangelogConfig is the changelog release path: prepending a
// release entry to a changelog file. When Enabled is true and File is
// unset, File defaults to "CHANGELOG.md".
type VersionChangelogConfig struct {
	Enabled bool   `json:"enabled"`
	File    string `json:"file"`
}

// errOldVersionShape is returned by parseVersionSection when the raw
// version section still uses the pre-redesign flat shape.
var errOldVersionShape = errors.New(
	"config: version section uses the old flat shape (mode/versionFile string/changelogMethod/changelog/rcAutoContinue); " +
		"run /setup --only version to migrate to the new nested tag/versionFile/changelog shape",
)

// detectOldVersionShape reports whether raw carries any marker of the old
// flat VersionSection shape. mode, changelogMethod, and rcAutoContinue no
// longer exist in the new shape at all, so their mere presence is
// conclusive. versionFile and changelog exist in both shapes but with
// different value types — a string versionFile or boolean changelog is
// the old shape's signature; the new shape always uses nested objects.
func detectOldVersionShape(raw map[string]any) bool {
	if _, ok := raw["mode"]; ok {
		return true
	}
	if _, ok := raw["changelogMethod"]; ok {
		return true
	}
	if _, ok := raw["rcAutoContinue"]; ok {
		return true
	}
	if vf, ok := raw["versionFile"]; ok {
		if _, isString := vf.(string); isString {
			return true
		}
	}
	if cl, ok := raw["changelog"]; ok {
		if _, isBool := cl.(bool); isBool {
			return true
		}
	}
	return false
}

// parseVersionSection converts a raw JSON map into a VersionSection,
// applying documented defaults. Returns (nil, nil) when raw is nil
// (section absent from config.json), mirroring extractSection's
// absent-section semantics so callers can distinguish "no version section
// configured" from "version section configured with defaults". Returns an
// error when raw still uses the old flat shape, or when neither the tag
// nor versionFile path is enabled.
func parseVersionSection(raw map[string]any) (*VersionSection, error) {
	if raw == nil {
		return nil, nil
	}
	if detectOldVersionShape(raw) {
		return nil, errOldVersionShape
	}

	v := &VersionSection{}
	if s, ok := raw["preRelease"].(string); ok {
		v.PreRelease = s
	}
	if s, ok := raw["preReleasePolicy"].(string); ok {
		v.PreReleasePolicy = s
	}
	if s, ok := raw["method"].(string); ok {
		v.Method = s
	}
	if tagRaw, ok := raw["tag"].(map[string]any); ok {
		if b, ok := tagRaw["enabled"].(bool); ok {
			v.Tag.Enabled = b
		}
		if s, ok := tagRaw["prefix"].(string); ok {
			v.Tag.Prefix = s
		}
	}
	if vfRaw, ok := raw["versionFile"].(map[string]any); ok {
		if b, ok := vfRaw["enabled"].(bool); ok {
			v.VersionFile.Enabled = b
		}
		if s, ok := vfRaw["path"].(string); ok {
			v.VersionFile.Path = s
		}
		if s, ok := vfRaw["fileType"].(string); ok {
			v.VersionFile.FileType = s
		}
	}
	if clRaw, ok := raw["changelog"].(map[string]any); ok {
		if b, ok := clRaw["enabled"].(bool); ok {
			v.Changelog.Enabled = b
		}
		if s, ok := clRaw["file"].(string); ok {
			v.Changelog.File = s
		}
	}

	applyVersionDefaults(v)

	if !v.Tag.Enabled && !v.VersionFile.Enabled {
		return nil, fmt.Errorf(
			"config: version section requires at least one of tag.enabled or versionFile.enabled to be true",
		)
	}

	return v, nil
}

// applyVersionDefaults fills in zero-value fields with documented
// defaults: preReleasePolicy "continue-rc", method "push", and
// changelog.file "CHANGELOG.md" when changelog.enabled is true but no
// explicit file was given.
func applyVersionDefaults(v *VersionSection) {
	if v.PreReleasePolicy == "" {
		v.PreReleasePolicy = "continue-rc"
	}
	if v.Method == "" {
		v.Method = "push"
	}
	if v.Changelog.Enabled && v.Changelog.File == "" {
		v.Changelog.File = "CHANGELOG.md"
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

	versionSection, err := parseVersionSection(extractSection(projectRaw, "version"))
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Version: versionSection,
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
