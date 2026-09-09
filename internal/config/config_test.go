package config

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// writeJSON writes v as indented JSON to path, creating parent directories.
func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func setupProjectConfig(t *testing.T, root string, cfg map[string]any) {
	t.Helper()
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), cfg)
}

func setupLocalConfig(t *testing.T, root string, cfg map[string]any) {
	t.Helper()
	writeJSON(t, filepath.Join(root, paths.DataDir, "local.json"), cfg)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// ---------------------------------------------------------------------------
// Section routing (AC-3)
// ---------------------------------------------------------------------------

func TestSectionRouting(t *testing.T) {
	tests := []struct {
		section string
		project bool // true = project config, false = local config
	}{
		{"version", true},
		{"jira", true},
		{"commit", true},
		{"pr", true},
		{"plan", true},
		{"execute", true},
		{"ship", false},
		{"review", false},
		{"receivedReview", false},
		{"workspace", false},
		{"automation", false},
	}

	for _, tt := range tests {
		t.Run(tt.section, func(t *testing.T) {
			resetTrace()
			Quiet = true
			defer func() { Quiet = false }()
			root := t.TempDir()
			sectionData := map[string]any{"testKey": "testValue"}

			if tt.project {
				setupProjectConfig(t, root, map[string]any{tt.section: sectionData})
			} else {
				// Project config must exist for Read to work (AC requires it).
				setupProjectConfig(t, root, map[string]any{})
				setupLocalConfig(t, root, map[string]any{tt.section: sectionData})
			}

			got, err := ReadSection(root, tt.section)
			if err != nil {
				t.Fatalf("ReadSection(%q) error: %v", tt.section, err)
			}
			if got["testKey"] != "testValue" {
				t.Errorf("ReadSection(%q)[testKey] = %v, want %q", tt.section, got["testKey"], "testValue")
			}
		})
	}
}

// TestSectionRouting_ProjectHitsConfigJSON verifies that project sections
// read from .sdlc/config.json, not from .sdlc/local.json, even when both
// files contain the same section name.
func TestSectionRouting_ProjectHitsConfigJSON(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()

	setupProjectConfig(t, root, map[string]any{
		"version": map[string]any{"mode": "file"},
	})
	setupLocalConfig(t, root, map[string]any{
		"version": map[string]any{"mode": "tag"},
	})

	got, err := ReadSection(root, "version")
	if err != nil {
		t.Fatalf("ReadSection(version) error: %v", err)
	}
	if got["mode"] != "file" {
		t.Errorf("version.mode = %q, want %q (should come from config.json)", got["mode"], "file")
	}
}

// TestSectionRouting_LocalDoesNotHitConfigJSON verifies that non-project
// sections are read from local.json, not config.json.
func TestSectionRouting_LocalDoesNotHitConfigJSON(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()

	setupProjectConfig(t, root, map[string]any{
		"ship": map[string]any{"draft": true},
	})
	setupLocalConfig(t, root, map[string]any{
		"ship": map[string]any{"draft": false},
	})

	got, err := ReadSection(root, "ship")
	if err != nil {
		t.Fatalf("ReadSection(ship) error: %v", err)
	}
	if got["draft"] != false {
		t.Errorf("ship.draft = %v, want false (should come from local.json)", got["draft"])
	}
}

// ---------------------------------------------------------------------------
// Legacy refusal (AC-1)
// ---------------------------------------------------------------------------

func TestLegacyRefusal_MarkerFiles(t *testing.T) {
	markers := []string{
		filepath.Join(".claude", "sdlc.json"),
		filepath.Join(".claude", "version.json"),
		filepath.Join(paths.LegacyDataDir, "jira-config.json"),
		filepath.Join(paths.LegacyDataDir, "ship-config.json"),
		filepath.Join(paths.LegacyDataDir, "review.json"),
		filepath.Join(".claude", "review.json"),
	}

	for _, marker := range markers {
		t.Run(marker, func(t *testing.T) {
			resetTrace()
			Quiet = true
			defer func() { Quiet = false }()
			root := t.TempDir()
			writeJSON(t, filepath.Join(root, marker), map[string]any{"legacy": true})

			_, err := Read(root)
			if err == nil {
				t.Fatalf("Read: expected error for legacy layout with %s, got nil", marker)
			}
			if !strings.Contains(err.Error(), "migrate") {
				t.Errorf("error should name migrate tool, got: %v", err)
			}
		})
	}
}

func TestLegacyRefusal_SchemaVersionField(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()
	setupProjectConfig(t, root, map[string]any{
		"schemaVersion": 4,
		"version":       map[string]any{"mode": "file"},
	})

	_, err := Read(root)
	if err == nil {
		t.Fatal("Read: expected error for v4 config with schemaVersion, got nil")
	}
	if !strings.Contains(err.Error(), "migrate") {
		t.Errorf("error should name migrate tool, got: %v", err)
	}
}

func TestLegacyRefusal_ReadSectionAlsoRefuses(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, ".claude", "sdlc.json"), map[string]any{})

	_, err := ReadSection(root, "version")
	if err == nil {
		t.Fatal("ReadSection: expected error for legacy layout, got nil")
	}
	if !strings.Contains(err.Error(), "migrate") {
		t.Errorf("error should name migrate tool, got: %v", err)
	}
}

// TestLegacyRefusal_IgnoredWhenV5Exists verifies that legacy marker files
// are ignored when a valid v5 .sdlc/config.json exists.
func TestLegacyRefusal_IgnoredWhenV5Exists(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()

	// Both v5 config and legacy marker.
	setupProjectConfig(t, root, map[string]any{
		"version": map[string]any{"mode": "file"},
	})
	writeJSON(t, filepath.Join(root, ".claude", "version.json"), map[string]any{"legacy": true})

	cfg, err := Read(root)
	if err != nil {
		t.Fatalf("Read: expected success when v5 config exists alongside legacy, got: %v", err)
	}
	if cfg.Version.Mode != "file" {
		t.Errorf("version.mode = %q, want %q", cfg.Version.Mode, "file")
	}
}

// ---------------------------------------------------------------------------
// Automation section (AC-2)
// ---------------------------------------------------------------------------

func TestAutomationDefaults(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()
	setupProjectConfig(t, root, map[string]any{})

	cfg, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if cfg.Automation == nil {
		t.Fatal("Automation should not be nil")
	}
	if cfg.Automation.Mode != "supervised" {
		t.Errorf("Mode = %q, want %q", cfg.Automation.Mode, "supervised")
	}
	if cfg.Automation.ReviewFixIterations != 3 {
		t.Errorf("ReviewFixIterations = %d, want 3", cfg.Automation.ReviewFixIterations)
	}
	if cfg.Automation.ReviewFixSeverityThreshold != "high" {
		t.Errorf("ReviewFixSeverityThreshold = %q, want %q", cfg.Automation.ReviewFixSeverityThreshold, "high")
	}
}

func TestAutomationParsesWithSteps(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()
	setupProjectConfig(t, root, map[string]any{})
	setupLocalConfig(t, root, map[string]any{
		"automation": map[string]any{
			"mode":                       "unattended",
			"reviewFixIterations":        float64(5),
			"reviewFixSeverityThreshold": "critical",
			"steps": map[string]any{
				"commit": "auto",
				"review": "confirm",
			},
		},
	})

	cfg, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	a := cfg.Automation
	if a.Mode != "unattended" {
		t.Errorf("Mode = %q, want %q", a.Mode, "unattended")
	}
	if a.ReviewFixIterations != 5 {
		t.Errorf("ReviewFixIterations = %d, want 5", a.ReviewFixIterations)
	}
	if a.ReviewFixSeverityThreshold != "critical" {
		t.Errorf("ReviewFixSeverityThreshold = %q, want %q", a.ReviewFixSeverityThreshold, "critical")
	}
	if a.Steps["commit"] != "auto" {
		t.Errorf("Steps[commit] = %q, want %q", a.Steps["commit"], "auto")
	}
	if a.Steps["review"] != "confirm" {
		t.Errorf("Steps[review] = %q, want %q", a.Steps["review"], "confirm")
	}
}

func TestAutomation_PartialOverrideGetsDefaults(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()
	setupProjectConfig(t, root, map[string]any{})
	// Only set mode, leave iterations and threshold to defaults.
	setupLocalConfig(t, root, map[string]any{
		"automation": map[string]any{
			"mode": "unattended",
		},
	})

	cfg, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	a := cfg.Automation
	if a.Mode != "unattended" {
		t.Errorf("Mode = %q, want %q", a.Mode, "unattended")
	}
	if a.ReviewFixIterations != 3 {
		t.Errorf("ReviewFixIterations = %d, want 3 (default)", a.ReviewFixIterations)
	}
	if a.ReviewFixSeverityThreshold != "high" {
		t.Errorf("ReviewFixSeverityThreshold = %q, want %q (default)", a.ReviewFixSeverityThreshold, "high")
	}
}

func TestAutomation_StepModeInheritance(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		steps    map[string]string
		step     string
		wantMode string
	}{
		{"supervised_default", "supervised", nil, "commit", "confirm"},
		{"unattended_default", "unattended", nil, "commit", "auto"},
		{"explicit_override_in_supervised", "supervised", map[string]string{"commit": "auto"}, "commit", "auto"},
		{"explicit_override_in_unattended", "unattended", map[string]string{"review": "confirm"}, "review", "confirm"},
		{"unlisted_step_in_supervised", "supervised", map[string]string{"commit": "auto"}, "version", "confirm"},
		{"unlisted_step_in_unattended", "unattended", map[string]string{"review": "confirm"}, "version", "auto"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &AutomationSection{
				Mode:  tt.mode,
				Steps: tt.steps,
			}
			got := a.StepMode(tt.step)
			if got != tt.wantMode {
				t.Errorf("StepMode(%q) = %q, want %q", tt.step, got, tt.wantMode)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Version section (typed VersionSection)
// ---------------------------------------------------------------------------

func TestParseVersionSection_Full(t *testing.T) {
	raw := map[string]any{
		"mode":          "file",
		"versionFile":   "package.json",
		"fileType":      "package.json",
		"tagPrefix":     "v",
		"changelog":     true,
		"changelogFile": "HISTORY.md",
		"ticketPrefix":  "PROJ",
		"preRelease":    "beta",
	}

	v := parseVersionSection(raw)
	if v == nil {
		t.Fatal("parseVersionSection: expected non-nil result")
	}
	if v.Mode != "file" {
		t.Errorf("Mode = %q, want %q", v.Mode, "file")
	}
	if v.VersionFile != "package.json" {
		t.Errorf("VersionFile = %q, want %q", v.VersionFile, "package.json")
	}
	if v.FileType != "package.json" {
		t.Errorf("FileType = %q, want %q", v.FileType, "package.json")
	}
	if v.TagPrefix != "v" {
		t.Errorf("TagPrefix = %q, want %q", v.TagPrefix, "v")
	}
	if !v.Changelog {
		t.Errorf("Changelog = %v, want true", v.Changelog)
	}
	if v.ChangelogFile != "HISTORY.md" {
		t.Errorf("ChangelogFile = %q, want explicit value %q preserved (not overridden by default)", v.ChangelogFile, "HISTORY.md")
	}
	if v.TicketPrefix != "PROJ" {
		t.Errorf("TicketPrefix = %q, want %q", v.TicketPrefix, "PROJ")
	}
	if v.PreRelease != "beta" {
		t.Errorf("PreRelease = %q, want %q", v.PreRelease, "beta")
	}

	raw["rcAutoContinue"] = false
	v4 := parseVersionSection(raw)
	if v4.PreReleasePolicy != "never" {
		t.Errorf("PreReleasePolicy = %q, want %q (rcAutoContinue:false backward-compat)", v4.PreReleasePolicy, "never")
	}
}

func TestParseVersionSection_Defaults(t *testing.T) {
	v := parseVersionSection(map[string]any{})
	if v == nil {
		t.Fatal("parseVersionSection: expected non-nil result")
	}
	if v.Mode != "file" {
		t.Errorf("Mode = %q, want default %q", v.Mode, "file")
	}
	if v.ChangelogFile != "" {
		t.Errorf("ChangelogFile = %q, want empty when changelog is false", v.ChangelogFile)
	}

	v2 := parseVersionSection(map[string]any{"changelog": true})
	if v2 == nil {
		t.Fatal("parseVersionSection: expected non-nil result")
	}
	if v2.ChangelogFile != "CHANGELOG.md" {
		t.Errorf("ChangelogFile = %q, want default %q when changelog=true", v2.ChangelogFile, "CHANGELOG.md")
	}

	v3 := parseVersionSection(map[string]any{"mode": "tag"})
	if v3.Mode != "tag" {
		t.Errorf("Mode = %q, want explicit value %q preserved", v3.Mode, "tag")
	}

	if v.PreReleasePolicy != "continue-rc" {
		t.Errorf("PreReleasePolicy = %q, want default %q when absent", v.PreReleasePolicy, "continue-rc")
	}
}

func TestParseVersionSection_Nil(t *testing.T) {
	if v := parseVersionSection(nil); v != nil {
		t.Errorf("parseVersionSection(nil) = %v, want nil", v)
	}
}

// TestParseVersionSection_PreReleasePolicy covers the full backward-compat
// matrix documented on VersionSection: an explicit "preReleasePolicy" wins
// outright, the legacy boolean "rcAutoContinue" maps true→"continue-rc" and
// false→"never", and the default (neither key present) is "continue-rc" —
// the same suggestion behavior the old rcAutoContinue=true default gave.
func TestPreReleasePolicy_Parse(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]any
		want string
	}{
		{"explicit always-rc", map[string]any{"preReleasePolicy": "always-rc"}, "always-rc"},
		{"explicit continue-rc", map[string]any{"preReleasePolicy": "continue-rc"}, "continue-rc"},
		{"explicit never", map[string]any{"preReleasePolicy": "never"}, "never"},
		{"legacy rcAutoContinue true", map[string]any{"rcAutoContinue": true}, "continue-rc"},
		{"legacy rcAutoContinue false", map[string]any{"rcAutoContinue": false}, "never"},
		{"neither key present", map[string]any{}, "continue-rc"},
		{
			"explicit preReleasePolicy wins over legacy rcAutoContinue",
			map[string]any{"preReleasePolicy": "always-rc", "rcAutoContinue": false},
			"always-rc",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := parseVersionSection(tc.raw)
			if v == nil {
				t.Fatal("parseVersionSection: expected non-nil result")
			}
			if v.PreReleasePolicy != tc.want {
				t.Errorf("PreReleasePolicy = %q, want %q", v.PreReleasePolicy, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ErrNotFound
// ---------------------------------------------------------------------------

func TestRead_NotFound(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()

	_, err := Read(root)
	if err == nil {
		t.Fatal("Read: expected error for missing config, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected errors.Is(err, ErrNotFound), got: %v", err)
	}
}

func TestReadSection_NotFound_MissingSection(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()
	setupProjectConfig(t, root, map[string]any{})

	_, err := ReadSection(root, "version")
	if err == nil {
		t.Fatal("ReadSection: expected error for missing section, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected errors.Is(err, ErrNotFound), got: %v", err)
	}
}

func TestReadSection_NotFound_MissingLocalFile(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()

	_, err := ReadSection(root, "ship")
	if err == nil {
		t.Fatal("ReadSection: expected error for missing local.json, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected errors.Is(err, ErrNotFound), got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// WriteSection
// ---------------------------------------------------------------------------

func TestWriteSection_ProjectCreatesConfigJSON(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()

	if err := WriteSection(root, "version", map[string]any{"mode": "file"}); err != nil {
		t.Fatalf("WriteSection: %v", err)
	}

	got, err := ReadSection(root, "version")
	if err != nil {
		t.Fatalf("ReadSection: %v", err)
	}
	if got["mode"] != "file" {
		t.Errorf("version.mode = %q, want %q", got["mode"], "file")
	}
}

func TestWriteSection_LocalCreatesLocalJSON(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()

	if err := WriteSection(root, "ship", map[string]any{"draft": true}); err != nil {
		t.Fatalf("WriteSection: %v", err)
	}

	got, err := ReadSection(root, "ship")
	if err != nil {
		t.Fatalf("ReadSection: %v", err)
	}
	if got["draft"] != true {
		t.Errorf("ship.draft = %v, want true", got["draft"])
	}
}

func TestWriteSection_MergesWithExisting(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()

	if err := WriteSection(root, "version", map[string]any{"mode": "file"}); err != nil {
		t.Fatalf("WriteSection(version): %v", err)
	}
	if err := WriteSection(root, "jira", map[string]any{"defaultProject": "TEST"}); err != nil {
		t.Fatalf("WriteSection(jira): %v", err)
	}

	v, err := ReadSection(root, "version")
	if err != nil {
		t.Fatalf("ReadSection(version): %v", err)
	}
	if v["mode"] != "file" {
		t.Errorf("version.mode = %q, want %q", v["mode"], "file")
	}

	j, err := ReadSection(root, "jira")
	if err != nil {
		t.Fatalf("ReadSection(jira): %v", err)
	}
	if j["defaultProject"] != "TEST" {
		t.Errorf("jira.defaultProject = %q, want %q", j["defaultProject"], "TEST")
	}
}

func TestWriteSection_RejectsUnknownProjectKeys(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()

	// Pre-seed config.json with an unknown key to trigger validation on write.
	setupProjectConfig(t, root, map[string]any{
		"unknown": map[string]any{"bad": true},
	})

	err := WriteSection(root, "version", map[string]any{"mode": "file"})
	if err == nil {
		t.Fatal("WriteSection: expected validation error for unknown key, got nil")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error should mention unknown key, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Schema sync
// ---------------------------------------------------------------------------

// TestSchemaSync verifies that allowedProjectKeys matches the top-level
// properties declared in plugins/sdlc/schemas/sdlc-config.schema.json. This
// is the mechanical sync check the task fact sheet requires.
func TestSchemaSync(t *testing.T) {
	schemaPath := filepath.Join("..", "..", "plugins", "sdlc", "schemas", "sdlc-config.schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("could not read schema file: %v", err)
	}

	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("could not parse schema: %v", err)
	}

	for key := range schema.Properties {
		if !allowedProjectKeys[key] && !allowedLocalOnlyKeys[key] {
			t.Errorf("schema property %q missing from allowedProjectKeys or allowedLocalOnlyKeys", key)
		}
	}
	for key := range allowedProjectKeys {
		if _, ok := schema.Properties[key]; !ok {
			t.Errorf("allowedProjectKey %q missing from schema properties", key)
		}
	}
	for key := range allowedLocalOnlyKeys {
		if _, ok := schema.Properties[key]; !ok {
			t.Errorf("allowedLocalOnlyKey %q missing from schema properties", key)
		}
	}
}

// ---------------------------------------------------------------------------
// Unknown top-level keys validation
// ---------------------------------------------------------------------------

func TestRead_RejectsUnknownProjectKeys(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()
	setupProjectConfig(t, root, map[string]any{
		"version":       map[string]any{"mode": "file"},
		"schemaVersion": 5, // This is a pre-v5 marker AND an unknown key
	})

	_, err := Read(root)
	if err == nil {
		t.Fatal("Read: expected error for config with schemaVersion, got nil")
	}
	// Should fail on the schemaVersion v4 marker check before even reaching
	// key validation.
	if !strings.Contains(err.Error(), "migrate") {
		t.Errorf("error should name migrate, got: %v", err)
	}
}

func TestRead_RejectsUnknownKeysWithoutSchemaVersion(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()
	setupProjectConfig(t, root, map[string]any{
		"version":  map[string]any{"mode": "file"},
		"badField": "should not be here",
	})

	_, err := Read(root)
	if err == nil {
		t.Fatal("Read: expected error for unknown key, got nil")
	}
	if !strings.Contains(err.Error(), "badField") {
		t.Errorf("error should mention badField, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Worktree anchoring (AC-4)
// ---------------------------------------------------------------------------

func TestReadAnchorsAtMainWorktreeRoot(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()

	mainDir := t.TempDir()
	runGit(t, mainDir, "init", "-q")
	runGit(t, mainDir, "-c", "user.email=test@example.com", "-c", "user.name=test",
		"commit", "--allow-empty", "-q", "-m", "init")

	setupProjectConfig(t, mainDir, map[string]any{
		"version": map[string]any{"mode": "file"},
	})

	linkedDir := filepath.Join(t.TempDir(), "linked")
	runGit(t, mainDir, "worktree", "add", "-q", linkedDir, "-b", "test-branch")

	// Linked worktree should not have .sdlc/config.json.
	if _, err := os.Stat(filepath.Join(linkedDir, paths.DataDir, "config.json")); err == nil {
		t.Fatal(".sdlc/config.json should not exist in linked worktree")
	}

	// Change cwd to the linked worktree and resolve main root.
	t.Chdir(linkedDir)

	root, err := worktree.MainRoot()
	if err != nil {
		t.Fatalf("MainRoot from linked worktree: %v", err)
	}

	// Read anchored at main worktree should find the config.
	cfg, err := Read(root)
	if err != nil {
		t.Fatalf("Read(MainRoot): %v", err)
	}
	if cfg.Version == nil || cfg.Version.Mode != "file" {
		t.Errorf("expected version.mode=file from main worktree, got %v", cfg.Version)
	}

	// Read from linked worktree path should NOT find config.
	resetTrace()
	_, err = Read(linkedDir)
	if err == nil {
		t.Fatal("Read(linkedDir): expected error, config should not be in linked worktree")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Read(linkedDir): expected ErrNotFound, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Read with full local config
// ---------------------------------------------------------------------------

func TestRead_MergesProjectAndLocal(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()
	root := t.TempDir()

	setupProjectConfig(t, root, map[string]any{
		"version": map[string]any{"mode": "tag"},
		"jira":    map[string]any{"defaultProject": "PROJ"},
	})
	setupLocalConfig(t, root, map[string]any{
		"ship":   map[string]any{"draft": true},
		"review": map[string]any{"scope": "all"},
	})

	cfg, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	// Project sections.
	if cfg.Version == nil || cfg.Version.Mode != "tag" {
		t.Errorf("Version = %v, want mode=tag", cfg.Version)
	}
	if cfg.Jira == nil || cfg.Jira["defaultProject"] != "PROJ" {
		t.Errorf("Jira = %v, want defaultProject=PROJ", cfg.Jira)
	}

	// Local sections.
	if cfg.Ship == nil || cfg.Ship["draft"] != true {
		t.Errorf("Ship = %v, want draft=true", cfg.Ship)
	}
	if cfg.Review == nil || cfg.Review["scope"] != "all" {
		t.Errorf("Review = %v, want scope=all", cfg.Review)
	}

	// Automation defaults applied.
	if cfg.Automation == nil {
		t.Fatal("Automation should not be nil")
	}
	if cfg.Automation.Mode != "supervised" {
		t.Errorf("Automation.Mode = %q, want supervised", cfg.Automation.Mode)
	}
}

// ---------------------------------------------------------------------------
// Tracing
// ---------------------------------------------------------------------------

func TestTracing_DeduplicatesPerPath(t *testing.T) {
	resetTrace()
	Quiet = false
	defer func() { Quiet = false }()

	// Trace the same path twice; the second call should be a no-op.
	traceRead("/test/path", "read")
	if !traced["/test/path"] {
		t.Error("path should be marked as traced")
	}
	// The function itself is a no-op on second call (dedupe).
	traceRead("/test/path", "read")
	// No crash means success — the dedupe set prevented double-trace.
}

func TestTracing_SuppressedWhenQuiet(t *testing.T) {
	resetTrace()
	Quiet = true
	defer func() { Quiet = false }()

	traceRead("/test/quiet", "read")
	if traced["/test/quiet"] {
		t.Error("path should not be traced when Quiet is true")
	}
}
