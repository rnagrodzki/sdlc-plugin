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
	if ver["mode"] != "file" {
		t.Error("version.mode should be 'file'")
	}
	if ver["versionFile"] != "package.json" {
		t.Error("version.versionFile should be 'package.json'")
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
	if ver["mode"] != "tag" {
		t.Error("version.mode should be 'tag'")
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
	expectedSteps := []string{"execute", "commit", "review", "version", "archive-openspec", "pr", "learnings-commit"}
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
	expected := []string{"execute", "commit", "version", "archive-openspec", "pr", "learnings-commit"}
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
	if ver["mode"] != "file" {
		t.Error("version.mode should be 'file'")
	}
	if ver["fileType"] != "package.json" {
		t.Error("version.fileType should be 'package.json'")
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
	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "schemas", "sdlc-config.schema.json"))
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
