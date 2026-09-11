package configmigrate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func writeJSON(t *testing.T, path string, data map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var data map[string]any
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return data
}

func stepsSlice(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i], _ = v.(string)
	}
	return out
}

// ---------------------------------------------------------------------------
// Verify tests
// ---------------------------------------------------------------------------

func TestVerify_V5_NoSchemaVersion(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"version": map[string]any{"mode": "file"},
	})
	if err := Verify(root); err != nil {
		t.Errorf("expected nil, got: %v", err)
	}
}

func TestVerify_FreshProject(t *testing.T) {
	root := t.TempDir()
	if err := Verify(root); err != nil {
		t.Errorf("expected nil for fresh project, got: %v", err)
	}
}

func TestVerify_VersionStale(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"schemaVersion": float64(3),
	})
	err := Verify(root)
	if !errors.Is(err, ErrVersionStale) {
		t.Errorf("expected ErrVersionStale, got: %v", err)
	}
	if !strings.Contains(err.Error(), "migrate") {
		t.Errorf("error message should name 'migrate' tool: %v", err)
	}
}

func TestVerify_VersionTooNew(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"schemaVersion": float64(99),
	})
	err := Verify(root)
	if !errors.Is(err, ErrVersionTooNew) {
		t.Errorf("expected ErrVersionTooNew, got: %v", err)
	}
}

func TestVerify_LegacyMarkers(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, ".claude", "version.json"), map[string]any{
		"mode": "file",
	})
	err := Verify(root)
	if !errors.Is(err, ErrVersionStale) {
		t.Errorf("expected ErrVersionStale for legacy markers, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Migration replay tests — golden fixture comparison
// ---------------------------------------------------------------------------

func TestMigrate_ProjectV4ToV5(t *testing.T) {
	root := t.TempDir()

	// v4 config.json: has schemaVersion, project sections.
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"schemaVersion": float64(4),
		"version":       map[string]any{"mode": "file", "versionFile": "package.json"},
		"jira":          map[string]any{"defaultProject": "PROJ"},
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	config := readJSON(t, filepath.Join(root, paths.DataDir, "config.json"))

	// Golden: no schemaVersion, sections preserved.
	if _, has := config["schemaVersion"]; has {
		t.Error("v5 config.json must not have schemaVersion")
	}
	ver := config["version"].(map[string]any)
	vf, ok := ver["versionFile"].(map[string]any)
	if !ok {
		t.Fatalf("version.versionFile should be an object, got %#v", ver["versionFile"])
	}
	if vf["enabled"] != true {
		t.Error("version.versionFile.enabled should be true")
	}
	if vf["path"] != "package.json" {
		t.Error("version.versionFile.path should be 'package.json'")
	}
	jira := config["jira"].(map[string]any)
	if jira["defaultProject"] != "PROJ" {
		t.Error("jira.defaultProject should be 'PROJ'")
	}
}

func TestMigrate_ProjectV0ToV5(t *testing.T) {
	root := t.TempDir()

	// Legacy .claude/sdlc.json (no config.json).
	writeJSON(t, filepath.Join(root, ".claude", "sdlc.json"), map[string]any{
		"$schema": "https://example.com/old-schema",
		"version": map[string]any{"mode": "tag", "tagPrefix": "v"},
		"jira":    map[string]any{"defaultProject": "TEST"},
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	config := readJSON(t, filepath.Join(root, paths.DataDir, "config.json"))

	// Golden: relocated, $schema stripped, no schemaVersion.
	if _, has := config["schemaVersion"]; has {
		t.Error("v5 config.json must not have schemaVersion")
	}
	if _, has := config["$schema"]; has {
		t.Error("$schema from legacy should be stripped during relocation")
	}
	ver := config["version"].(map[string]any)
	tag, ok := ver["tag"].(map[string]any)
	if !ok {
		t.Fatalf("version.tag should be an object, got %#v", ver["tag"])
	}
	if tag["enabled"] != true {
		t.Error("version.tag.enabled should be true")
	}
	if tag["prefix"] != "v" {
		t.Error("version.tag.prefix should be 'v'")
	}
	if _, has := ver["versionFile"]; has {
		t.Error("version.versionFile should be absent — old mode 'tag' maps to tag-only")
	}
}

// TestIngestLegacy_NestedSchemaStripped confirms a "$schema" key nested
// inside an individual section object (not just the top-level sdlc.json
// document) is stripped by ingestLegacy — covering the per-section
// delete(m, "$schema") calls for the projectCfg loop (version/jira/commit/
// pr/plan/execute) as well as the separate ship and review section blocks.
//
// This calls ingestLegacy directly (white-box) rather than through the
// public Migrate() entry point. Migrate() routes any project with an
// existing .claude/sdlc.json to relocateProjectConfig (only a top-level
// $schema strip, see migrations.go), not to ingestLegacy: detectProjectVersion
// treats .claude/sdlc.json's mere existence as "project config exists",
// so ingestLegacy's `!projectExists` precondition can only be true when
// .claude/sdlc.json is ABSENT — at which point ingestLegacy's own
// sdlc.json read fails and its nested-$schema-stripping block never runs.
// That makes this block unreachable through Migrate() in practice; this
// test exercises ingestLegacy's own logic directly instead of asserting
// behavior nothing in production can trigger.
func TestIngestLegacy_NestedSchemaStripped(t *testing.T) {
	root := t.TempDir()

	writeJSON(t, filepath.Join(root, ".claude", "sdlc.json"), map[string]any{
		"version": map[string]any{"mode": "file"},
		"jira": map[string]any{
			"$schema":        "https://example.com/jira-schema",
			"defaultProject": "TEST",
		},
		"ship": map[string]any{
			"$schema": "https://example.com/ship-schema",
			"preset":  "full",
		},
		"review": map[string]any{
			"$schema": "https://example.com/review-schema",
		},
	})

	ingested, err := ingestLegacy(root)
	if err != nil {
		t.Fatalf("ingestLegacy: %v", err)
	}
	if len(ingested) == 0 {
		t.Fatal("expected at least one ingested legacy file")
	}

	config := readJSON(t, filepath.Join(root, paths.DataDir, "config.json"))
	jira := config["jira"].(map[string]any)
	if _, has := jira["$schema"]; has {
		t.Error("nested jira.$schema should be stripped during ingestion")
	}
	if jira["defaultProject"] != "TEST" {
		t.Error("jira.defaultProject should survive $schema stripping")
	}

	local := readJSON(t, filepath.Join(root, paths.DataDir, "local.json"))
	ship := local["ship"].(map[string]any)
	if _, has := ship["$schema"]; has {
		t.Error("nested ship.$schema should be stripped during ingestion")
	}
	review := local["review"].(map[string]any)
	if _, has := review["$schema"]; has {
		t.Error("nested review.$schema should be stripped during ingestion")
	}
}

func TestMigrate_ProjectV0ToV5_ShipStrippedFromConfig(t *testing.T) {
	root := t.TempDir()

	// Legacy sdlc.json with ship section (gets relocated into config.json by
	// v0→v3, but v4→v5 must strip it and move to local.json).
	writeJSON(t, filepath.Join(root, ".claude", "sdlc.json"), map[string]any{
		"version": map[string]any{"mode": "file"},
		"jira":    map[string]any{"defaultProject": "PROJ"},
		"ship":    map[string]any{"preset": "full"},
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	// config.json must NOT contain ship (schema additionalProperties: false).
	config := readJSON(t, filepath.Join(root, paths.DataDir, "config.json"))
	if _, has := config["ship"]; has {
		t.Error("config.json must not contain ship section after v4→v5")
	}
	if _, has := config["schemaVersion"]; has {
		t.Error("config.json must not have schemaVersion")
	}

	// ship should be in local.json with preset migrated to steps.
	local := readJSON(t, filepath.Join(root, paths.DataDir, "local.json"))
	ship := local["ship"].(map[string]any)
	if _, has := ship["preset"]; has {
		t.Error("ship.preset should be removed (migrated to steps)")
	}
	steps := stepsSlice(ship, "steps")
	expectedSteps := []string{"execute", "commit", "review", "archive-openspec", "pr", "learnings-commit"}
	if !reflect.DeepEqual(steps, expectedSteps) {
		t.Errorf("ship.steps:\n  got:  %v\n  want: %v", steps, expectedSteps)
	}
}

func TestMigrate_ProjectV0ToV5_ShipAndReviewStripped_LocalPreserved(t *testing.T) {
	root := t.TempDir()

	// Legacy sdlc.json with ship + review.
	writeJSON(t, filepath.Join(root, ".claude", "sdlc.json"), map[string]any{
		"version": map[string]any{"mode": "file"},
		"ship":    map[string]any{"preset": "minimal"},
		"review":  map[string]any{"effort": "high"},
	})

	// Pre-existing local.json with its own ship section — should be kept.
	writeJSON(t, filepath.Join(root, paths.DataDir, "local.json"), map[string]any{
		"version": float64(1),
		"ship": map[string]any{
			"preset": "balanced",
		},
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	config := readJSON(t, filepath.Join(root, paths.DataDir, "config.json"))
	if _, has := config["ship"]; has {
		t.Error("config.json must not contain ship")
	}
	if _, has := config["review"]; has {
		t.Error("config.json must not contain review")
	}

	local := readJSON(t, filepath.Join(root, paths.DataDir, "local.json"))

	// Local's ship was from v1 preset "balanced" and went through local
	// migration chain — verify it was preserved (not overwritten by config's).
	ship := local["ship"].(map[string]any)
	steps := stepsSlice(ship, "steps")
	// "balanced" preset steps.
	expectedSteps := []string{
		"execute", "commit", "review", "archive-openspec", "pr", "learnings-commit",
	}
	if !reflect.DeepEqual(steps, expectedSteps) {
		t.Errorf("ship.steps should come from local v1 balanced preset:\n  got:  %v\n  want: %v", steps, expectedSteps)
	}

	// Review from config should be moved to local since local didn't have one.
	review := local["review"].(map[string]any)
	if review["effort"] != "high" {
		t.Error("review.effort should be 'high' (moved from config)")
	}
}

func TestMigrate_LocalV1ToV5_ShipPresetSkip(t *testing.T) {
	root := t.TempDir()

	// Provide a v5 config.json so project migration is skipped.
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"version": map[string]any{"mode": "file"},
	})

	// v1 local.json: ship with preset + skip.
	writeJSON(t, filepath.Join(root, paths.DataDir, "local.json"), map[string]any{
		"version": float64(1),
		"ship": map[string]any{
			"preset": "full",
			"skip":   []any{"review"},
		},
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	local := readJSON(t, filepath.Join(root, paths.DataDir, "local.json"))

	// Golden: no schemaVersion, no version integer, ship.steps expanded.
	if _, has := local["schemaVersion"]; has {
		t.Error("v5 local.json must not have schemaVersion")
	}
	if _, has := local["version"]; has {
		t.Error("v5 local.json must not have version integer")
	}

	ship := local["ship"].(map[string]any)
	if _, has := ship["preset"]; has {
		t.Error("ship.preset should be removed")
	}
	if _, has := ship["skip"]; has {
		t.Error("ship.skip should be removed")
	}

	steps := stepsSlice(ship, "steps")
	expected := []string{"execute", "commit", "archive-openspec", "pr", "learnings-commit"}
	if !reflect.DeepEqual(steps, expected) {
		t.Errorf("ship.steps:\n  got:  %v\n  want: %v", steps, expected)
	}
}

func TestMigrate_LocalV3ToV5_AwaitReviewBooleans(t *testing.T) {
	root := t.TempDir()

	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"version": map[string]any{"mode": "file"},
	})

	// v3 local.json: ship with awaitReview booleans and tunables.
	writeJSON(t, filepath.Join(root, paths.DataDir, "local.json"), map[string]any{
		"schemaVersion": float64(3),
		"ship": map[string]any{
			"steps":               []any{"execute", "commit", "pr"},
			"verifyPipeline":      true,
			"awaitReview":         true,
			"awaitReviewTimeout":  float64(300),
			"awaitReviewInterval": float64(30),
			"awaitReviewers":      []any{"user1"},
		},
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	local := readJSON(t, filepath.Join(root, paths.DataDir, "local.json"))

	if _, has := local["schemaVersion"]; has {
		t.Error("v5 local.json must not have schemaVersion")
	}

	ship := local["ship"].(map[string]any)

	// Boolean flags removed.
	for _, key := range []string{"verifyPipeline", "awaitReview"} {
		if _, has := ship[key]; has {
			t.Errorf("ship.%s should be removed", key)
		}
	}

	// Steps should include verify-pipeline and await-remote-review.
	steps := stepsSlice(ship, "steps")
	expectedSteps := []string{"execute", "commit", "pr", "verify-pipeline", "await-remote-review"}
	if !reflect.DeepEqual(steps, expectedSteps) {
		t.Errorf("ship.steps:\n  got:  %v\n  want: %v", steps, expectedSteps)
	}

	// Tunables renamed.
	if _, has := ship["awaitReviewTimeout"]; has {
		t.Error("awaitReviewTimeout should be renamed")
	}
	if ship["awaitRemoteReviewTimeout"] != float64(300) {
		t.Error("awaitRemoteReviewTimeout should be 300")
	}
	if ship["awaitRemoteReviewInterval"] != float64(30) {
		t.Error("awaitRemoteReviewInterval should be 30")
	}
	reviewers := ship["awaitRemoteReviewers"].([]any)
	if len(reviewers) != 1 || reviewers[0] != "user1" {
		t.Error("awaitRemoteReviewers should be [user1]")
	}
}

func TestMigrate_LocalV2ToV5_RenameVersion(t *testing.T) {
	root := t.TempDir()

	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"version": map[string]any{"mode": "file"},
	})

	// v2 local.json: has "version": 2 integer (not yet renamed to schemaVersion).
	writeJSON(t, filepath.Join(root, paths.DataDir, "local.json"), map[string]any{
		"version": float64(2),
		"ship": map[string]any{
			"steps": []any{"execute", "commit", "pr"},
		},
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	local := readJSON(t, filepath.Join(root, paths.DataDir, "local.json"))

	// Neither version integer nor schemaVersion should remain in v5.
	if _, has := local["schemaVersion"]; has {
		t.Error("v5 local.json must not have schemaVersion")
	}
	if _, has := local["version"]; has {
		t.Error("v5 local.json must not have version integer")
	}

	// Ship steps preserved.
	ship := local["ship"].(map[string]any)
	steps := stepsSlice(ship, "steps")
	expected := []string{"execute", "commit", "pr"}
	if !reflect.DeepEqual(steps, expected) {
		t.Errorf("ship.steps:\n  got:  %v\n  want: %v", steps, expected)
	}
}

func TestLocalMigrateV5ToV6_StripsVersionFromShipSteps(t *testing.T) {
	root := t.TempDir()

	// Provide a v6 config.json so project migration is skipped.
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"version": map[string]any{"mode": "file"},
	})

	// v5 local.json: ship.steps still carries the standalone "version" step
	// from before it was folded into pr. Real v5 files carry no schemaVersion
	// marker (removed by the v4→v5 step) — detectLocalVersion must sniff
	// ship.steps for "version" to tell this apart from a v6 file, since
	// neither v5 nor v6 local.json carries any version marker at all.
	writeJSON(t, filepath.Join(root, paths.DataDir, "local.json"), map[string]any{
		"ship": map[string]any{
			"steps": []any{"execute", "commit", "review", "version", "archive-openspec", "pr", "learnings-commit"},
		},
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	local := readJSON(t, filepath.Join(root, paths.DataDir, "local.json"))

	ship := local["ship"].(map[string]any)
	steps := stepsSlice(ship, "steps")
	expected := []string{"execute", "commit", "review", "archive-openspec", "pr", "learnings-commit"}
	if !reflect.DeepEqual(steps, expected) {
		t.Errorf("ship.steps:\n  got:  %v\n  want: %v", steps, expected)
	}
}

// ---------------------------------------------------------------------------
// Legacy ingestion tests
// ---------------------------------------------------------------------------

func TestMigrate_LegacyIngestion_VersionAndJira(t *testing.T) {
	root := t.TempDir()

	// Individual per-section legacy files.
	writeJSON(t, filepath.Join(root, ".claude", "version.json"), map[string]any{
		"$schema":     "https://example.com/old",
		"mode":        "file",
		"versionFile": "package.json",
		"fileType":    "package.json",
	})
	writeJSON(t, filepath.Join(root, paths.LegacyDataDir, "jira-config.json"), map[string]any{
		"$schema":        "https://example.com/old",
		"defaultProject": "MYPROJ",
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}
	if len(report.LegacyIngested) == 0 {
		t.Fatal("expected LegacyIngested to be non-empty")
	}

	config := readJSON(t, filepath.Join(root, paths.DataDir, "config.json"))

	if _, has := config["schemaVersion"]; has {
		t.Error("v5 config.json must not have schemaVersion")
	}
	if _, has := config["$schema"]; has {
		t.Error("$schema should be stripped from ingested sections")
	}

	ver := config["version"].(map[string]any)
	vf, ok := ver["versionFile"].(map[string]any)
	if !ok {
		t.Fatalf("version.versionFile should be an object, got %#v", ver["versionFile"])
	}
	if vf["enabled"] != true {
		t.Error("version.versionFile.enabled should be true")
	}
	if vf["path"] != "package.json" {
		t.Error("version.versionFile.path should be 'package.json'")
	}
	if vf["fileType"] != "package.json" {
		t.Error("version.versionFile.fileType should be 'package.json'")
	}
	if _, has := ver["tag"]; has {
		t.Error("version.tag should be absent — old mode 'file' maps to versionFile-only")
	}

	jira := config["jira"].(map[string]any)
	if jira["defaultProject"] != "MYPROJ" {
		t.Error("jira.defaultProject should be 'MYPROJ'")
	}
}

func TestMigrate_LegacyIngestion_ShipAndReview(t *testing.T) {
	root := t.TempDir()

	// Ship config with preset (needs migration to steps).
	writeJSON(t, filepath.Join(root, paths.LegacyDataDir, "ship-config.json"), map[string]any{
		"$schema": "https://example.com/old",
		"version": float64(1),
		"preset":  "minimal",
	})

	// Review config with defaults wrapper.
	writeJSON(t, filepath.Join(root, paths.LegacyDataDir, "review.json"), map[string]any{
		"$schema": "https://example.com/old",
		"defaults": map[string]any{
			"effort": "high",
		},
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	local := readJSON(t, filepath.Join(root, paths.DataDir, "local.json"))

	ship := local["ship"].(map[string]any)
	if _, has := ship["$schema"]; has {
		t.Error("$schema should be stripped")
	}
	if _, has := ship["version"]; has {
		t.Error("version should be stripped from ship")
	}
	if _, has := ship["preset"]; has {
		t.Error("preset should be removed (migrated to steps)")
	}

	steps := stepsSlice(ship, "steps")
	expected := []string{"execute", "commit", "pr", "learnings-commit"}
	if !reflect.DeepEqual(steps, expected) {
		t.Errorf("ship.steps:\n  got:  %v\n  want: %v", steps, expected)
	}

	review := local["review"].(map[string]any)
	if review["effort"] != "high" {
		t.Error("review.effort should be 'high' (from defaults)")
	}
}

func TestMigrate_LegacyIngestion_ClaudeReviewFallback(t *testing.T) {
	root := t.TempDir()

	// Only .claude/review.json (alternate location).
	writeJSON(t, filepath.Join(root, ".claude", "review.json"), map[string]any{
		"effort": "medium",
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	local := readJSON(t, filepath.Join(root, paths.DataDir, "local.json"))
	review := local["review"].(map[string]any)
	if review["effort"] != "medium" {
		t.Error("review.effort should be 'medium'")
	}
}

// ---------------------------------------------------------------------------
// Version-too-new test
// ---------------------------------------------------------------------------

func TestMigrate_VersionTooNew(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"schemaVersion": float64(99),
	})

	_, err := Migrate(root, Options{})
	if !errors.Is(err, ErrVersionTooNew) {
		t.Errorf("expected ErrVersionTooNew, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Concurrent lock test
// ---------------------------------------------------------------------------

func TestMigrate_ConcurrentLock(t *testing.T) {
	root := t.TempDir()

	// Create a config that needs migration.
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"schemaVersion": float64(4),
		"version":       map[string]any{"mode": "file"},
	})

	// Pre-create the lock file to simulate a concurrent migration.
	lockPath := filepath.Join(root, paths.DataDir, ".migration.lock")
	if err := os.WriteFile(lockPath, []byte("99999"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Migrate(root, Options{
		LockRetries:    2,
		LockRetryDelay: 5 * time.Millisecond,
	})
	if !errors.Is(err, ErrMigrationLocked) {
		t.Errorf("expected ErrMigrationLocked, got: %v", err)
	}
}

func TestMigrate_LockCleanedUp(t *testing.T) {
	root := t.TempDir()

	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"schemaVersion": float64(4),
		"version":       map[string]any{"mode": "file"},
	})

	_, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	lockPath := filepath.Join(root, paths.DataDir, ".migration.lock")
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file should be cleaned up after migration")
	}
}

// ---------------------------------------------------------------------------
// Fresh project — nothing to do
// ---------------------------------------------------------------------------

func TestMigrate_FreshProject(t *testing.T) {
	root := t.TempDir()

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if report.Migrated {
		t.Error("expected Migrated=false for fresh project")
	}
}

// ---------------------------------------------------------------------------
// Already v5 — no-op
// ---------------------------------------------------------------------------

func TestMigrate_AlreadyV5(t *testing.T) {
	root := t.TempDir()

	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"version": map[string]any{"mode": "file"},
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if report.Migrated {
		t.Error("expected Migrated=false for already-v5 config")
	}
}

// ---------------------------------------------------------------------------
// Schema validation — v5 fixtures round-trip against sdlc-config.schema.json
// ---------------------------------------------------------------------------

func TestV5Fixtures_SchemaValidation(t *testing.T) {
	// Load the schema.
	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc", "schemas", "sdlc-config.schema.json"))
	if err != nil {
		t.Fatal(err)
	}

	c := jsonschema.NewCompiler()
	sch, err := c.Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	// Each subtest sets up a pre-migration state, runs Migrate, reads the
	// resulting config.json, and validates it against the schema.
	tests := []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{
			name: "v4-to-v5",
			setup: func(t *testing.T, root string) {
				writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
					"schemaVersion": float64(4),
					"version":       map[string]any{"mode": "file"},
				})
			},
		},
		{
			name: "v4-to-v5-full-sections",
			setup: func(t *testing.T, root string) {
				writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
					"schemaVersion": float64(4),
					"version":       map[string]any{"mode": "tag", "tagPrefix": "v", "changelog": true},
					"jira":          map[string]any{"defaultProject": "PROJ"},
					"commit":        map[string]any{"allowedTypes": []any{"feat", "fix"}},
					"pr":            map[string]any{"titlePattern": "^[A-Z]+-\\d+"},
				})
			},
		},
		{
			name: "legacy-ingestion-version",
			setup: func(t *testing.T, root string) {
				writeJSON(t, filepath.Join(root, ".claude", "version.json"), map[string]any{
					"mode":        "file",
					"versionFile": "package.json",
					"fileType":    "package.json",
				})
			},
		},
		{
			name: "legacy-ingestion-unified",
			setup: func(t *testing.T, root string) {
				writeJSON(t, filepath.Join(root, ".claude", "sdlc.json"), map[string]any{
					"version": map[string]any{"mode": "file"},
					"jira":    map[string]any{"defaultProject": "TEST"},
				})
			},
		},
		{
			name: "v0-relocation",
			setup: func(t *testing.T, root string) {
				writeJSON(t, filepath.Join(root, ".claude", "sdlc.json"), map[string]any{
					"$schema": "https://example.com/old",
					"version": map[string]any{"mode": "file", "versionFile": "version.txt", "fileType": "version-file"},
				})
			},
		},
		{
			name: "v0-relocation-with-ship",
			setup: func(t *testing.T, root string) {
				writeJSON(t, filepath.Join(root, ".claude", "sdlc.json"), map[string]any{
					"version": map[string]any{"mode": "file"},
					"jira":    map[string]any{"defaultProject": "PROJ"},
					"ship":    map[string]any{"preset": "full"},
				})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.setup(t, root)

			_, err := Migrate(root, Options{})
			if err != nil {
				t.Fatalf("Migrate: %v", err)
			}

			configPath := filepath.Join(root, paths.DataDir, "config.json")
			f, err := os.Open(configPath)
			if err != nil {
				t.Fatalf("open config.json: %v", err)
			}
			defer f.Close()

			inst, err := jsonschema.UnmarshalJSON(f)
			if err != nil {
				t.Fatalf("unmarshal config.json for schema validation: %v", err)
			}

			if err := sch.Validate(inst); err != nil {
				t.Errorf("config.json failed schema validation:\n%v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Full migration chain: v1 → v5 with both project and local
// ---------------------------------------------------------------------------

func TestMigrate_FullChain_ProjectAndLocal(t *testing.T) {
	root := t.TempDir()

	// Project config at v4.
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"schemaVersion": float64(4),
		"version":       map[string]any{"mode": "file"},
		"jira":          map[string]any{"defaultProject": "PROJ"},
	})

	// Local config at v1 with both preset and awaitReview.
	writeJSON(t, filepath.Join(root, paths.DataDir, "local.json"), map[string]any{
		"version": float64(1),
		"ship": map[string]any{
			"preset":             "balanced",
			"verifyPipeline":     true,
			"awaitReview":        true,
			"awaitReviewTimeout": float64(600),
		},
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	// Verify project config.
	config := readJSON(t, filepath.Join(root, paths.DataDir, "config.json"))
	if _, has := config["schemaVersion"]; has {
		t.Error("v5 config.json must not have schemaVersion")
	}

	// Verify local config — full migration chain applied.
	local := readJSON(t, filepath.Join(root, paths.DataDir, "local.json"))
	if _, has := local["schemaVersion"]; has {
		t.Error("v5 local.json must not have schemaVersion")
	}
	if _, has := local["version"]; has {
		t.Error("v5 local.json must not have version integer")
	}

	ship := local["ship"].(map[string]any)

	// Preset "balanced" minus nothing = all balanced steps.
	// Then awaitReview booleans add verify-pipeline and await-remote-review.
	expectedSteps := []string{
		"execute", "commit", "review", "archive-openspec", "pr", "learnings-commit",
		"verify-pipeline", "await-remote-review",
	}
	steps := stepsSlice(ship, "steps")
	if !reflect.DeepEqual(steps, expectedSteps) {
		t.Errorf("ship.steps:\n  got:  %v\n  want: %v", steps, expectedSteps)
	}

	// Tunable renamed.
	if ship["awaitRemoteReviewTimeout"] != float64(600) {
		t.Error("awaitRemoteReviewTimeout should be 600")
	}
	if _, has := ship["awaitReviewTimeout"]; has {
		t.Error("awaitReviewTimeout should be renamed")
	}
}

// ---------------------------------------------------------------------------
// MigrateWithBackup
// ---------------------------------------------------------------------------

func TestMigrateWithBackup_MissingConfig(t *testing.T) {
	root := t.TempDir()

	changes, backupPath, err := MigrateWithBackup(root)
	if !errors.Is(err, ErrConfigMissing) {
		t.Fatalf("err = %v, want ErrConfigMissing", err)
	}
	if !strings.Contains(err.Error(), "/setup") {
		t.Errorf("error message = %q, want it to mention /setup", err.Error())
	}
	if changes != nil {
		t.Errorf("changes = %v, want nil", changes)
	}
	if backupPath != "" {
		t.Errorf("backupPath = %q, want empty", backupPath)
	}
}

func TestMigrateWithBackup_CurrentConfig_NoOp(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, paths.DataDir, "config.json")
	writeJSON(t, configPath, map[string]any{"version": map[string]any{"mode": "file"}})

	before, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config.json: %v", err)
	}

	changes, backupPath, err := MigrateWithBackup(root)
	if err != nil {
		t.Fatalf("MigrateWithBackup: %v", err)
	}
	if changes != nil {
		t.Errorf("changes = %v, want nil for an already-current config", changes)
	}
	if backupPath != "" {
		t.Errorf("backupPath = %q, want empty for an already-current config", backupPath)
	}
	if _, statErr := os.Stat(configPath + ".bak"); statErr == nil {
		t.Error("config.json.bak written for an already-current config; want zero extra I/O")
	}

	after, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config.json: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("config.json mtime changed (%v -> %v), want untouched", before.ModTime(), after.ModTime())
	}
}

func TestMigrateWithBackup_StaleConfig_MigratesAndBacksUp(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, paths.DataDir, "config.json")
	writeJSON(t, configPath, map[string]any{
		"schemaVersion": float64(4),
		"version":       map[string]any{"mode": "file"},
	})
	original, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}

	changes, backupPath, err := MigrateWithBackup(root)
	if err != nil {
		t.Fatalf("MigrateWithBackup: %v", err)
	}
	if len(changes) == 0 {
		t.Error("changes is empty, want at least one applied step label")
	}
	if backupPath != configPath+".bak" {
		t.Errorf("backupPath = %q, want %q", backupPath, configPath+".bak")
	}

	backupContent, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !reflect.DeepEqual(backupContent, original) {
		t.Errorf("backup content does not match pre-migration config.json:\n  backup: %s\n  original: %s", backupContent, original)
	}

	migrated := readJSON(t, configPath)
	if _, has := migrated["schemaVersion"]; has {
		t.Error("migrated config.json must not have schemaVersion")
	}
}

// TestMigrateWithBackup_V5Local_StripsVersionStep is a regression test for
// the gap where MigrateWithBackup short-circuited on Verify(config.json)
// alone: config.json is content-identical at v5 and v6 (its schema forbids
// a schemaVersion marker), so a real v5 project's local.json — which still
// carries the standalone "version" ship step — was silently never migrated,
// leaving a stale ship.steps that ship.go later hard-rejects.
func TestMigrateWithBackup_V5Local_StripsVersionStep(t *testing.T) {
	root := t.TempDir()
	// v6-equivalent config.json: no schemaVersion marker, Verify() reports
	// current on its own.
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"version": map[string]any{"mode": "file"},
	})
	// v5 local.json: no schemaVersion marker either, but ship.steps still
	// carries "version" — the only signal that it predates the v5->v6 step.
	localPath := filepath.Join(root, paths.DataDir, "local.json")
	writeJSON(t, localPath, map[string]any{
		"ship": map[string]any{
			"steps": []any{"execute", "commit", "review", "version", "archive-openspec", "pr", "learnings-commit"},
		},
	})

	changes, backupPath, err := MigrateWithBackup(root)
	if err != nil {
		t.Fatalf("MigrateWithBackup: %v", err)
	}
	if len(changes) == 0 {
		t.Error("changes is empty, want at least one applied step label")
	}
	if backupPath != localPath+".bak" {
		t.Errorf("backupPath = %q, want %q (config.json untouched, only local.json backed up)", backupPath, localPath+".bak")
	}
	if _, statErr := os.Stat(localPath + ".bak"); statErr != nil {
		t.Errorf("local.json.bak not written: %v", statErr)
	}

	local := readJSON(t, localPath)
	ship := local["ship"].(map[string]any)
	steps := stepsSlice(ship, "steps")
	expected := []string{"execute", "commit", "review", "archive-openspec", "pr", "learnings-commit"}
	if !reflect.DeepEqual(steps, expected) {
		t.Errorf("ship.steps:\n  got:  %v\n  want: %v", steps, expected)
	}
}

// TestMigrateWithBackup_V6Local_NoOp guards against over-eager detection:
// a marker-less local.json whose ship.steps no longer contains "version"
// (already v6, or born v6) must not be perpetually re-migrated just because
// it lacks a schemaVersion field.
func TestMigrateWithBackup_V6Local_NoOp(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"version": map[string]any{"mode": "file"},
	})
	localPath := filepath.Join(root, paths.DataDir, "local.json")
	writeJSON(t, localPath, map[string]any{
		"ship": map[string]any{
			"steps": []any{"execute", "commit", "review", "archive-openspec", "pr", "learnings-commit"},
		},
	})
	before, err := os.Stat(localPath)
	if err != nil {
		t.Fatalf("stat local.json: %v", err)
	}

	changes, backupPath, err := MigrateWithBackup(root)
	if err != nil {
		t.Fatalf("MigrateWithBackup: %v", err)
	}
	if changes != nil {
		t.Errorf("changes = %v, want nil for an already-current v6 local.json", changes)
	}
	if backupPath != "" {
		t.Errorf("backupPath = %q, want empty for an already-current v6 local.json", backupPath)
	}
	if _, statErr := os.Stat(localPath + ".bak"); statErr == nil {
		t.Error("local.json.bak written for an already-current local.json; want zero extra I/O")
	}
	after, err := os.Stat(localPath)
	if err != nil {
		t.Fatalf("stat local.json: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("local.json mtime changed (%v -> %v), want untouched", before.ModTime(), after.ModTime())
	}
}

// TestDetectLocalVersion_NoShipKey_TreatedAsCurrent is a regression test:
// a marker-less local.json with no "ship" key at all (valid — neither
// "ship" nor "ship.steps" is `required` by sdlc-local.schema.json) must not
// be misdetected as v1. The earlier fallback unconditionally returned
// (1, true) for any marker-less file once the ship.steps sniff found
// nothing to match, which caused MigrateWithBackup to replan and re-run the
// full 1->6 migration chain on every call for a project shaped this way.
func TestDetectLocalVersion_NoShipKey_TreatedAsCurrent(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, paths.DataDir, "local.json"), map[string]any{
		"reviewThreshold": "low",
	})

	ver, exists := detectLocalVersion(root)
	if !exists {
		t.Fatal("detectLocalVersion: exists = false, want true")
	}
	if ver != CurrentSchemaVersion {
		t.Errorf("detectLocalVersion = %d, want CurrentSchemaVersion (%d)", ver, CurrentSchemaVersion)
	}
}

// TestDetectLocalVersion_ShipWithoutSteps_TreatedAsCurrent covers the other
// half of the same gap: "ship" present but without a "steps" array (also
// valid per the schema — e.g. a project that only set reviewThreshold).
func TestDetectLocalVersion_ShipWithoutSteps_TreatedAsCurrent(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, paths.DataDir, "local.json"), map[string]any{
		"ship": map[string]any{"reviewThreshold": "medium"},
	})

	ver, exists := detectLocalVersion(root)
	if !exists {
		t.Fatal("detectLocalVersion: exists = false, want true")
	}
	if ver != CurrentSchemaVersion {
		t.Errorf("detectLocalVersion = %d, want CurrentSchemaVersion (%d)", ver, CurrentSchemaVersion)
	}
}

// TestMigrateWithBackup_ShipWithoutSteps_NoPerpetualRemigration is the
// end-to-end version of the two detectLocalVersion tests above: a
// marker-less local.json shaped this way must be a true MigrateWithBackup
// no-op — repeated calls must not keep producing local.json.bak files or
// re-running the migration chain.
func TestMigrateWithBackup_ShipWithoutSteps_NoPerpetualRemigration(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"version": map[string]any{"mode": "file"},
	})
	localPath := filepath.Join(root, paths.DataDir, "local.json")
	writeJSON(t, localPath, map[string]any{
		"ship": map[string]any{"reviewThreshold": "medium"},
	})

	for i := 0; i < 2; i++ {
		changes, backupPath, err := MigrateWithBackup(root)
		if err != nil {
			t.Fatalf("MigrateWithBackup call %d: %v", i, err)
		}
		if changes != nil {
			t.Errorf("call %d: changes = %v, want nil", i, changes)
		}
		if backupPath != "" {
			t.Errorf("call %d: backupPath = %q, want empty", i, backupPath)
		}
	}
	if _, statErr := os.Stat(localPath + ".bak"); statErr == nil {
		t.Error("local.json.bak written; want zero extra I/O for a ship-without-steps local.json")
	}
}

func TestMigrateWithBackup_VersionTooNew_PassesThroughUnchanged(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, paths.DataDir, "config.json")
	writeJSON(t, configPath, map[string]any{"schemaVersion": float64(99)})

	changes, backupPath, err := MigrateWithBackup(root)
	if !errors.Is(err, ErrVersionTooNew) {
		t.Fatalf("err = %v, want ErrVersionTooNew", err)
	}
	if changes != nil {
		t.Errorf("changes = %v, want nil", changes)
	}
	if backupPath != "" {
		t.Errorf("backupPath = %q, want empty (too-new config is never backed up)", backupPath)
	}
	if _, statErr := os.Stat(configPath + ".bak"); statErr == nil {
		t.Error("config.json.bak written for a too-new config; want no backup attempt")
	}
}

func TestMigrateWithBackup_LegacyOnly_IngestsWithoutBackup(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, ".claude", "sdlc.json"), map[string]any{
		"version": map[string]any{"mode": "file"},
	})

	changes, backupPath, err := MigrateWithBackup(root)
	if err != nil {
		t.Fatalf("MigrateWithBackup: %v", err)
	}
	if len(changes) == 0 {
		t.Error("changes is empty, want at least one legacy-ingested label")
	}
	// No pre-existing config.json to back up — ingestLegacy only writes a
	// fresh one, it never rewrites the legacy source in place.
	if backupPath != "" {
		t.Errorf("backupPath = %q, want empty (nothing to back up for a purely-legacy project)", backupPath)
	}

	configPath := filepath.Join(root, paths.DataDir, "config.json")
	if _, statErr := os.Stat(configPath); statErr != nil {
		t.Errorf("config.json not created by legacy ingestion: %v", statErr)
	}
}

// TestMigrateWithBackup_ReviewThresholdLow_SurvivesMigration is a regression
// test: reviewThreshold "low" is a known-good enum value in
// sdlc-local.schema.json (critical|high|medium|low). No migration step
// touches ship.reviewThreshold, but MigrateWithBackup's read-modify-write
// pass over local.json (via Migrate) must still carry it through unchanged
// and produce output that validates cleanly against the schema.
func TestMigrateWithBackup_ReviewThresholdLow_SurvivesMigration(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"schemaVersion": float64(4),
		"version":       map[string]any{"mode": "file"},
	})
	writeJSON(t, filepath.Join(root, paths.DataDir, "local.json"), map[string]any{
		"version": float64(1),
		"ship": map[string]any{
			"preset":          "balanced",
			"reviewThreshold": "low",
		},
	})

	_, _, err := MigrateWithBackup(root)
	if err != nil {
		t.Fatalf("MigrateWithBackup: %v", err)
	}

	local := readJSON(t, filepath.Join(root, paths.DataDir, "local.json"))
	ship, ok := local["ship"].(map[string]any)
	if !ok {
		t.Fatalf("local.json ship section = %v, want a map", local["ship"])
	}
	if ship["reviewThreshold"] != "low" {
		t.Errorf("ship.reviewThreshold = %v, want %q to survive migration unchanged", ship["reviewThreshold"], "low")
	}

	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc", "schemas", "sdlc-local.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	sch, err := c.Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	f, err := os.Open(filepath.Join(root, paths.DataDir, "local.json"))
	if err != nil {
		t.Fatalf("open local.json: %v", err)
	}
	defer f.Close()

	inst, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatalf("unmarshal local.json for schema validation: %v", err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("local.json failed schema validation:\n%v", err)
	}
}

// ---------------------------------------------------------------------------
// New tests for v4 schema coverage
// ---------------------------------------------------------------------------

func TestMigrate_ProjectV4_FullVersionShape(t *testing.T) {
	root := t.TempDir()

	// v4 config.json with full version shape
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"schemaVersion": float64(4),
		"version": map[string]any{
			"mode":          "tag",
			"tagPrefix":     "v",
			"changelog":     true,
			"changelogFile": "CHANGELOG.md",
			"preRelease":    "rc",
		},
		"jira": map[string]any{
			"defaultProject": "PROJ",
		},
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	config := readJSON(t, filepath.Join(root, paths.DataDir, "config.json"))

	// Golden: no schemaVersion, no $schema, nested version shape
	if _, has := config["schemaVersion"]; has {
		t.Error("v5 config.json must not have schemaVersion")
	}
	if _, has := config["$schema"]; has {
		t.Error("v5 config.json must not have $schema")
	}

	ver := config["version"].(map[string]any)

	// tag section should exist with enabled=true and prefix="v"
	tag, ok := ver["tag"].(map[string]any)
	if !ok {
		t.Fatalf("version.tag should be an object, got %#v", ver["tag"])
	}
	if tag["enabled"] != true {
		t.Error("version.tag.enabled should be true")
	}
	if tag["prefix"] != "v" {
		t.Error("version.tag.prefix should be 'v'")
	}

	// changelog section should exist with enabled=true and file="CHANGELOG.md"
	cl, ok := ver["changelog"].(map[string]any)
	if !ok {
		t.Fatalf("version.changelog should be an object, got %#v", ver["changelog"])
	}
	if cl["enabled"] != true {
		t.Error("version.changelog.enabled should be true")
	}
	if cl["file"] != "CHANGELOG.md" {
		t.Error("version.changelog.file should be 'CHANGELOG.md'")
	}

	// preRelease should be preserved
	if ver["preRelease"] != "rc" {
		t.Error("version.preRelease should be 'rc'")
	}

	// versionFile should not exist (mode was tag)
	if _, has := ver["versionFile"]; has {
		t.Error("version.versionFile should not exist when mode was 'tag'")
	}
}

func TestMigrate_LocalV4_FullShape(t *testing.T) {
	root := t.TempDir()

	// Provide v5 config.json so project migration is skipped
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"version": map[string]any{"mode": "file"},
	})

	// v4 local.json with full shape
	// Note: By v4, earlier migrations have already applied:
	// - preset/skip were migrated to steps in v1->v2
	// - awaitReview/verifyPipeline booleans were migrated to steps in v3->v4
	// - awaitReview* fields were renamed to awaitRemoteReview* in v3->v4
	// So v4 has no preset, skip, awaitReview, verifyPipeline booleans.
	writeJSON(t, filepath.Join(root, paths.DataDir, "local.json"), map[string]any{
		"schemaVersion": float64(4),
		"$schema":       "https://raw.githubusercontent.com/owner/sdlc-plugin/main/schemas/sdlc-local.schema.json",
		"ship": map[string]any{
			"steps":                       []any{"execute", "commit", "review", "version", "archive-openspec", "pr", "learnings-commit"},
			"quick":                       []any{"execute", "commit", "version", "pr"},
			"bump":                        "minor",
			"draft":                       true,
			"auto":                        false,
			"reviewThreshold":             "high",
			"awaitRemoteReviewTimeout":    float64(300),
			"awaitRemoteReviewInterval":   float64(30),
			"awaitRemoteReviewers":        []any{"reviewer1", "reviewer2"},
			"verifyPipelineTimeout":       float64(600),
			"verifyPipelineInterval":      float64(60),
			"verifyPipelineMaxIterations": float64(3),
		},
		"review": map[string]any{
			"scope": "all",
		},
		"receivedReview": map[string]any{
			"alwaysFixSeverities": []any{"critical", "high"},
		},
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	local := readJSON(t, filepath.Join(root, paths.DataDir, "local.json"))

	// Golden: no schemaVersion
	if _, has := local["schemaVersion"]; has {
		t.Error("v6 local.json must not have schemaVersion")
	}
	// $schema is preserved during migration (only stripped during legacy ingestion)
	if _, has := local["$schema"]; !has {
		t.Error("v6 local.json should preserve $schema if present")
	}

	ship := local["ship"].(map[string]any)

	// "version" should be removed from ship.steps (v5->v6 step) but NOT from ship.quick
	steps := stepsSlice(ship, "steps")
	expectedSteps := []string{"execute", "commit", "review", "archive-openspec", "pr", "learnings-commit"}
	if !reflect.DeepEqual(steps, expectedSteps) {
		t.Errorf("ship.steps:\n  got:  %v\n  want: %v (version removed)", steps, expectedSteps)
	}

	quick := stepsSlice(ship, "quick")
	expectedQuick := []string{"execute", "commit", "version", "pr"}
	if !reflect.DeepEqual(quick, expectedQuick) {
		t.Errorf("ship.quick:\n  got:  %v\n  want: %v (version preserved in quick)", quick, expectedQuick)
	}

	// Other ship fields should be preserved
	if ship["bump"] != "minor" {
		t.Error("ship.bump should be 'minor'")
	}
	if ship["draft"] != true {
		t.Error("ship.draft should be true")
	}
	if ship["auto"] != false {
		t.Error("ship.auto should be false")
	}
	if ship["reviewThreshold"] != "high" {
		t.Error("ship.reviewThreshold should be 'high'")
	}

	// awaitRemoteReview* fields should be preserved (already renamed in v3->v4)
	if ship["awaitRemoteReviewTimeout"] != float64(300) {
		t.Error("ship.awaitRemoteReviewTimeout should be 300")
	}
	if ship["awaitRemoteReviewInterval"] != float64(30) {
		t.Error("ship.awaitRemoteReviewInterval should be 30")
	}
	reviewers := ship["awaitRemoteReviewers"].([]any)
	if len(reviewers) != 2 || reviewers[0] != "reviewer1" {
		t.Error("ship.awaitRemoteReviewers should be preserved")
	}

	// Verify pipeline fields should be preserved
	if ship["verifyPipelineTimeout"] != float64(600) {
		t.Error("ship.verifyPipelineTimeout should be 600")
	}
	if ship["verifyPipelineInterval"] != float64(60) {
		t.Error("ship.verifyPipelineInterval should be 60")
	}
	if ship["verifyPipelineMaxIterations"] != float64(3) {
		t.Error("ship.verifyPipelineMaxIterations should be 3")
	}

	// review and receivedReview should be preserved
	review := local["review"].(map[string]any)
	if review["scope"] != "all" {
		t.Error("review.scope should be 'all'")
	}

	receivedReview := local["receivedReview"].(map[string]any)
	if len(receivedReview["alwaysFixSeverities"].([]any)) != 2 {
		t.Error("receivedReview.alwaysFixSeverities should be preserved")
	}
}

func TestMigrate_ProjectV4_SchemaRemovalNotApplied(t *testing.T) {
	root := t.TempDir()

	// v4 config.json with schemaVersion and $schema.
	// Note: $schema stripping only happens during legacy ingestion (v0→v3 relocation).
	// During v4→v5 migration, $schema is preserved (not stripped).
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"schemaVersion": float64(4),
		"$schema":       "https://raw.githubusercontent.com/rnagrodzki/sdlc-plugin/main/schema.json",
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	config := readJSON(t, filepath.Join(root, paths.DataDir, "config.json"))

	// Golden: schemaVersion stripped, $schema preserved (only legacy relocation strips $schema)
	if _, has := config["schemaVersion"]; has {
		t.Error("v5 config.json must not have schemaVersion")
	}
	// $schema is preserved during v4→v5 migration (only stripped during legacy ingestion)
	if _, has := config["$schema"]; !has {
		t.Error("$schema should be preserved during v4→v5 migration")
	}
	if config["$schema"] != "https://raw.githubusercontent.com/rnagrodzki/sdlc-plugin/main/schema.json" {
		t.Error("$schema URL should be preserved")
	}
}

func TestMigrate_FullChain_V4ProjectAndV4Local(t *testing.T) {
	root := t.TempDir()

	// v4 project config (already through v0→v3→v4 migrations)
	writeJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"schemaVersion": float64(4),
		"version": map[string]any{
			"mode":          "tag",
			"tagPrefix":     "v",
			"changelog":     true,
			"changelogFile": "CHANGELOG.md",
			"preRelease":    "rc",
		},
		"jira": map[string]any{
			"defaultProject": "TEST",
		},
	})

	// v4 local config (already through v1→v2→v3→v4 migrations)
	// By v4, preset/skip are gone (migrated to steps in v1→v2),
	// and awaitReview/verifyPipeline booleans are gone (migrated to steps in v3→v4).
	// But ship.steps may still include "version" which gets removed in v5→v6.
	writeJSON(t, filepath.Join(root, paths.DataDir, "local.json"), map[string]any{
		"schemaVersion": float64(4),
		"ship": map[string]any{
			"steps":                     []any{"execute", "commit", "review", "version", "archive-openspec", "pr", "learnings-commit"},
			"bump":                      "minor",
			"reviewThreshold":           "high",
			"awaitRemoteReviewTimeout":  float64(300),
			"awaitRemoteReviewInterval": float64(30),
		},
	})

	report, err := Migrate(root, Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	// Verify project config
	config := readJSON(t, filepath.Join(root, paths.DataDir, "config.json"))
	if _, has := config["schemaVersion"]; has {
		t.Error("v6 config.json must not have schemaVersion")
	}

	ver := config["version"].(map[string]any)

	// version.tag should exist with enabled=true and prefix="v"
	tag, ok := ver["tag"].(map[string]any)
	if !ok {
		t.Fatalf("version.tag should be an object, got %#v", ver["tag"])
	}
	if tag["enabled"] != true {
		t.Error("version.tag.enabled should be true")
	}
	if tag["prefix"] != "v" {
		t.Error("version.tag.prefix should be 'v'")
	}

	// version.changelog should exist
	cl, ok := ver["changelog"].(map[string]any)
	if !ok {
		t.Fatalf("version.changelog should be an object, got %#v", ver["changelog"])
	}
	if cl["enabled"] != true {
		t.Error("version.changelog.enabled should be true")
	}
	if cl["file"] != "CHANGELOG.md" {
		t.Error("version.changelog.file should be 'CHANGELOG.md'")
	}

	// preRelease should be preserved
	if ver["preRelease"] != "rc" {
		t.Error("version.preRelease should be 'rc'")
	}

	// Verify local config
	local := readJSON(t, filepath.Join(root, paths.DataDir, "local.json"))
	if _, has := local["schemaVersion"]; has {
		t.Error("v6 local.json must not have schemaVersion")
	}

	ship := local["ship"].(map[string]any)

	// "version" should be removed from ship.steps by v5→v6 migration
	steps := stepsSlice(ship, "steps")
	expectedSteps := []string{"execute", "commit", "review", "archive-openspec", "pr", "learnings-commit"}
	if !reflect.DeepEqual(steps, expectedSteps) {
		t.Errorf("ship.steps:\n  got:  %v\n  want: %v (version removed)", steps, expectedSteps)
	}

	// Other fields should be preserved
	if ship["bump"] != "minor" {
		t.Error("ship.bump should be 'minor'")
	}
	if ship["reviewThreshold"] != "high" {
		t.Error("ship.reviewThreshold should be 'high'")
	}
	if ship["awaitRemoteReviewTimeout"] != float64(300) {
		t.Error("ship.awaitRemoteReviewTimeout should be 300")
	}
	if ship["awaitRemoteReviewInterval"] != float64(30) {
		t.Error("ship.awaitRemoteReviewInterval should be 30")
	}

	// Validate both files against their schemas
	schemaPathConfig, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc", "schemas", "sdlc-config.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	schConfig, err := c.Compile(schemaPathConfig)
	if err != nil {
		t.Fatalf("compile config schema: %v", err)
	}

	fConfig, err := os.Open(filepath.Join(root, paths.DataDir, "config.json"))
	if err != nil {
		t.Fatalf("open config.json: %v", err)
	}
	defer fConfig.Close()

	instConfig, err := jsonschema.UnmarshalJSON(fConfig)
	if err != nil {
		t.Fatalf("unmarshal config.json for schema validation: %v", err)
	}
	if err := schConfig.Validate(instConfig); err != nil {
		t.Errorf("config.json failed schema validation:\n%v", err)
	}

	schemaPathLocal, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc", "schemas", "sdlc-local.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	c2 := jsonschema.NewCompiler()
	schLocal, err := c2.Compile(schemaPathLocal)
	if err != nil {
		t.Fatalf("compile local schema: %v", err)
	}

	fLocal, err := os.Open(filepath.Join(root, paths.DataDir, "local.json"))
	if err != nil {
		t.Fatalf("open local.json: %v", err)
	}
	defer fLocal.Close()

	instLocal, err := jsonschema.UnmarshalJSON(fLocal)
	if err != nil {
		t.Fatalf("unmarshal local.json for schema validation: %v", err)
	}
	if err := schLocal.Validate(instLocal); err != nil {
		t.Errorf("local.json failed schema validation:\n%v", err)
	}
}
