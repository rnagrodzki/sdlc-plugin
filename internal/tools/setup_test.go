package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func writeTestJSON(t *testing.T, path string, data map[string]any) {
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

func readTestJSON(t *testing.T, path string) map[string]any {
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

// ---------------------------------------------------------------------------
// setup_prepare tests
// ---------------------------------------------------------------------------

func TestSetupPrepare_ReturnsSections(t *testing.T) {
	root := t.TempDir()

	out, err := setupPrepare(root, SetupPrepareIn{})
	if err != nil {
		t.Fatalf("setupPrepare: %v", err)
	}

	if !out.OK {
		t.Error("expected OK=true")
	}
	if len(out.Sections) == 0 {
		t.Fatal("expected non-empty sections")
	}

	// Verify first section has expected structure.
	first := out.Sections[0]
	if first.ID == "" {
		t.Error("first section ID should not be empty")
	}
	if first.Label == "" {
		t.Error("first section Label should not be empty")
	}
}

func TestSetupPrepare_NeedsMigrationOnLegacy(t *testing.T) {
	root := t.TempDir()

	// Create a legacy config marker.
	writeTestJSON(t, filepath.Join(root, ".sdlc", "config.json"), map[string]any{
		"schemaVersion": float64(3),
	})

	out, err := setupPrepare(root, SetupPrepareIn{SkipConfigCheck: false})
	if err != nil {
		t.Fatalf("setupPrepare: %v", err)
	}

	if !out.NeedsMigration {
		t.Error("expected needsMigration=true for stale schema")
	}
}

func TestSetupPrepare_SkipConfigCheck(t *testing.T) {
	root := t.TempDir()

	// Create a legacy config marker.
	writeTestJSON(t, filepath.Join(root, ".sdlc", "config.json"), map[string]any{
		"schemaVersion": float64(3),
	})

	out, err := setupPrepare(root, SetupPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("setupPrepare: %v", err)
	}

	if out.NeedsMigration {
		t.Error("expected needsMigration=false when SkipConfigCheck is true")
	}
}

func TestSetupPrepare_SectionsHaveFields(t *testing.T) {
	root := t.TempDir()

	out, err := setupPrepare(root, SetupPrepareIn{})
	if err != nil {
		t.Fatalf("setupPrepare: %v", err)
	}

	// Find the "version" section and check it has fields.
	var versionSection *sectionRow
	for i := range out.Sections {
		if out.Sections[i].ID == "version" {
			versionSection = &out.Sections[i]
			break
		}
	}
	if versionSection == nil {
		t.Fatal("expected to find 'version' section")
	}
	if len(versionSection.Fields) == 0 {
		t.Error("version section should have fields")
	}

	// Verify first field has expected shape.
	first := versionSection.Fields[0]
	if first.Name != "mode" {
		t.Errorf("first version field should be 'mode', got %q", first.Name)
	}
	if first.Type != "enum" {
		t.Errorf("version.mode type should be 'enum', got %q", first.Type)
	}
}

func TestSetupPrepare_JSONSerializationCamelCase(t *testing.T) {
	root := t.TempDir()

	out, err := setupPrepare(root, SetupPrepareIn{})
	if err != nil {
		t.Fatalf("setupPrepare: %v", err)
	}

	// Marshal to JSON and verify camelCase keys.
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)

	// Check JSON uses camelCase keys, not PascalCase.
	if strings.Contains(s, `"ConfigFile"`) {
		t.Error("JSON should use camelCase 'configFile', found PascalCase 'ConfigFile'")
	}
	if !strings.Contains(s, `"configFile"`) {
		t.Error("JSON should contain camelCase 'configFile'")
	}
	if strings.Contains(s, `"NeedsMigration"`) {
		t.Error("JSON should use camelCase 'needsMigration', found PascalCase")
	}
}

// ---------------------------------------------------------------------------
// setup_init tests
// ---------------------------------------------------------------------------

func TestSetupInit_EmptyFixture_CreatesScaffold(t *testing.T) {
	root := t.TempDir()

	out, err := setupInit(root, SetupInitIn{Sections: []string{}})
	if err != nil {
		t.Fatalf("setupInit: %v", err)
	}

	if !out.OK {
		t.Errorf("expected OK=true, errors: %v", out.Errors)
	}

	// .sdlc/ directory should exist.
	if _, err := os.Stat(filepath.Join(root, ".sdlc")); err != nil {
		t.Error(".sdlc/ directory should exist")
	}

	// .sdlc/.gitignore should exist with managed block.
	sdlcGitignore, err := os.ReadFile(filepath.Join(root, ".sdlc", ".gitignore"))
	if err != nil {
		t.Fatal(".sdlc/.gitignore should exist")
	}
	content := string(sdlcGitignore)
	if !strings.Contains(content, "sdlc-utilities managed") {
		t.Error(".sdlc/.gitignore should contain managed block marker")
	}
	if !strings.Contains(content, "!config.json") {
		t.Error(".sdlc/.gitignore should allowlist config.json")
	}

	// Root .gitignore should exist with managed block.
	rootGitignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal("root .gitignore should exist")
	}
	rootContent := string(rootGitignore)
	if !strings.Contains(rootContent, "*-context-*.json") {
		t.Error("root .gitignore should contain transient artifact pattern")
	}

	// config.json and local.json should exist with empty objects.
	configData := readTestJSON(t, filepath.Join(root, ".sdlc", "config.json"))
	if len(configData) != 0 {
		t.Errorf("config.json should be empty object, got %v", configData)
	}
	localData := readTestJSON(t, filepath.Join(root, ".sdlc", "local.json"))
	if len(localData) != 0 {
		t.Errorf("local.json should be empty object, got %v", localData)
	}
}

func TestSetupInit_V5SchemaCompliant(t *testing.T) {
	root := t.TempDir()

	_, err := setupInit(root, SetupInitIn{Sections: []string{"version"}})
	if err != nil {
		t.Fatalf("setupInit: %v", err)
	}

	// config.json must pass configmigrate.Verify (no schemaVersion marker).
	if err := configmigrate.Verify(root); err != nil {
		t.Errorf("scaffold should pass Verify: %v", err)
	}

	// config.json must not have schemaVersion field.
	configData := readTestJSON(t, filepath.Join(root, ".sdlc", "config.json"))
	if _, has := configData["schemaVersion"]; has {
		t.Error("config.json must not have schemaVersion field")
	}
}

func TestSetupInit_WithProjectSections(t *testing.T) {
	root := t.TempDir()

	out, err := setupInit(root, SetupInitIn{Sections: []string{"version", "jira"}})
	if err != nil {
		t.Fatalf("setupInit: %v", err)
	}

	if !out.OK {
		t.Errorf("expected OK=true, errors: %v", out.Errors)
	}

	// config.json should have the seeded sections.
	configData := readTestJSON(t, filepath.Join(root, ".sdlc", "config.json"))
	if _, has := configData["version"]; !has {
		t.Error("config.json should have 'version' section")
	}
	if _, has := configData["jira"]; !has {
		t.Error("config.json should have 'jira' section")
	}
}

func TestSetupInit_WithLocalSections(t *testing.T) {
	root := t.TempDir()

	out, err := setupInit(root, SetupInitIn{Sections: []string{"ship", "review"}})
	if err != nil {
		t.Fatalf("setupInit: %v", err)
	}

	if !out.OK {
		t.Errorf("expected OK=true, errors: %v", out.Errors)
	}

	// local.json should have the seeded sections.
	localData := readTestJSON(t, filepath.Join(root, ".sdlc", "local.json"))
	if _, has := localData["ship"]; !has {
		t.Error("local.json should have 'ship' section")
	}
	if _, has := localData["review"]; !has {
		t.Error("local.json should have 'review' section")
	}
}

func TestSetupInit_Idempotent(t *testing.T) {
	root := t.TempDir()

	// Run twice. Second run should not error and should report unchanged.
	_, err := setupInit(root, SetupInitIn{Sections: []string{"version"}})
	if err != nil {
		t.Fatalf("first setupInit: %v", err)
	}

	out, err := setupInit(root, SetupInitIn{Sections: []string{"version"}})
	if err != nil {
		t.Fatalf("second setupInit: %v", err)
	}
	if !out.OK {
		t.Errorf("second run should be OK, errors: %v", out.Errors)
	}

	// Second run should not report config files as created.
	for _, c := range out.Created {
		if c == ".sdlc/config.json" || c == ".sdlc/local.json" {
			t.Errorf("second run should not report %q as created", c)
		}
	}

	// .sdlc/.gitignore should not have duplicate managed blocks.
	content, err := os.ReadFile(filepath.Join(root, ".sdlc", ".gitignore"))
	if err != nil {
		t.Fatal("read .sdlc/.gitignore")
	}
	count := strings.Count(string(content), "sdlc-utilities managed (do not edit)")
	if count != 1 {
		t.Errorf("expected 1 managed block begin marker, got %d", count)
	}
}

func TestSetupInit_RootGitignoreLegacyUpgrade(t *testing.T) {
	root := t.TempDir()

	// Write a v1-style managed block in root .gitignore.
	v1Content := "# existing user pattern\nnode_modules/\n" +
		"# >>> sdlc-utilities managed (do not edit) — transient skill artifacts\n" +
		"*-context-*.json\n" +
		"# <<< sdlc-utilities managed\n"
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(v1Content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := setupInit(root, SetupInitIn{})
	if err != nil {
		t.Fatalf("setupInit: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal("read .gitignore")
	}
	s := string(content)

	// Should have v3 marker now.
	if !strings.Contains(s, "managed v3") {
		t.Error("root .gitignore should be upgraded to v3 managed block")
	}
	// Should preserve user content.
	if !strings.Contains(s, "node_modules/") {
		t.Error("root .gitignore should preserve user content")
	}
	// Should not have legacy v1 begin marker.
	if strings.Contains(s, "# >>> sdlc-utilities managed (do not edit) — transient skill artifacts\n*-context") {
		t.Error("root .gitignore should not contain legacy v1 block")
	}
}

// ---------------------------------------------------------------------------
// migrate tests
// ---------------------------------------------------------------------------

func TestMigrate_ConfigAction_V4ToV5(t *testing.T) {
	root := t.TempDir()

	// v4 config.
	writeTestJSON(t, filepath.Join(root, ".sdlc", "config.json"), map[string]any{
		"schemaVersion": float64(4),
		"version":       map[string]any{"mode": "file", "versionFile": "package.json"},
		"jira":          map[string]any{"defaultProject": "PROJ"},
	})

	out, err := migrate(root, MigrateIn{Action: "config", DryRun: false})
	if err != nil {
		t.Fatalf("migrate config: %v", err)
	}

	if !out.OK {
		t.Error("expected OK=true")
	}
	if !strings.Contains(out.Result, "migrated") {
		t.Errorf("expected 'migrated' in result, got %q", out.Result)
	}
	if len(out.Changed) == 0 {
		t.Error("expected non-empty Changed list")
	}

	// Verify v5 result.
	configData := readTestJSON(t, filepath.Join(root, ".sdlc", "config.json"))
	if _, has := configData["schemaVersion"]; has {
		t.Error("config.json should not have schemaVersion after migration")
	}
	ver := configData["version"].(map[string]any)
	if ver["mode"] != "file" {
		t.Error("version.mode should be preserved")
	}
}

func TestMigrate_ConfigAction_LegacyToV5(t *testing.T) {
	root := t.TempDir()

	// Legacy .claude/sdlc.json.
	writeTestJSON(t, filepath.Join(root, ".claude", "sdlc.json"), map[string]any{
		"version": map[string]any{"mode": "tag", "tagPrefix": "v"},
		"jira":    map[string]any{"defaultProject": "TEST"},
	})

	out, err := migrate(root, MigrateIn{Action: "config", DryRun: false})
	if err != nil {
		t.Fatalf("migrate config: %v", err)
	}

	if !out.OK {
		t.Error("expected OK=true")
	}

	// Verify v5 result.
	configData := readTestJSON(t, filepath.Join(root, ".sdlc", "config.json"))
	if _, has := configData["schemaVersion"]; has {
		t.Error("v5 config.json should not have schemaVersion")
	}
	ver := configData["version"].(map[string]any)
	if ver["mode"] != "tag" {
		t.Error("version.mode should be 'tag'")
	}
}

func TestMigrate_ConfigAction_DryRun(t *testing.T) {
	root := t.TempDir()

	writeTestJSON(t, filepath.Join(root, ".sdlc", "config.json"), map[string]any{
		"schemaVersion": float64(4),
		"version":       map[string]any{"mode": "file"},
	})

	out, err := migrate(root, MigrateIn{Action: "config", DryRun: true})
	if err != nil {
		t.Fatalf("migrate config dry-run: %v", err)
	}

	if !out.OK {
		t.Error("expected OK=true")
	}
	if !out.DryRun {
		t.Error("expected DryRun=true")
	}
	if !strings.Contains(out.Result, "would-migrate") {
		t.Errorf("expected 'would-migrate' in result, got %q", out.Result)
	}

	// Config should not be changed.
	configData := readTestJSON(t, filepath.Join(root, ".sdlc", "config.json"))
	if _, has := configData["schemaVersion"]; !has {
		t.Error("config should not be changed during dry run")
	}
}

func TestMigrate_ConfigAction_AlreadyV5(t *testing.T) {
	root := t.TempDir()

	writeTestJSON(t, filepath.Join(root, ".sdlc", "config.json"), map[string]any{
		"version": map[string]any{"mode": "file"},
	})

	out, err := migrate(root, MigrateIn{Action: "config", DryRun: false})
	if err != nil {
		t.Fatalf("migrate config: %v", err)
	}

	if out.Result != "up-to-date" {
		t.Errorf("expected 'up-to-date', got %q", out.Result)
	}
}

func TestMigrate_JiraTemplates_Move(t *testing.T) {
	root := t.TempDir()

	// Create legacy jira-templates directory with a file.
	srcDir := filepath.Join(root, ".claude", "jira-templates")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "bug.md"), []byte("# Bug\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "jira_templates", DryRun: false})
	if err != nil {
		t.Fatalf("migrate jira_templates: %v", err)
	}

	if !out.OK {
		t.Error("expected OK=true")
	}
	if !strings.Contains(out.Result, "moved") {
		t.Errorf("expected 'moved' in result, got %q", out.Result)
	}

	// Source should be gone, destination should have the file.
	if migrateDirExists(srcDir) {
		t.Error("source directory should be removed")
	}
	dstFile := filepath.Join(root, ".sdlc", "jira-templates", "bug.md")
	if !migrateFileExists(dstFile) {
		t.Error("destination file should exist")
	}
}

func TestMigrate_JiraTemplates_Noop(t *testing.T) {
	root := t.TempDir()

	out, err := migrate(root, MigrateIn{Action: "jira_templates", DryRun: false})
	if err != nil {
		t.Fatalf("migrate jira_templates: %v", err)
	}

	if !strings.Contains(out.Result, "noop") {
		t.Errorf("expected 'noop' in result, got %q", out.Result)
	}
}

func TestMigrate_JiraTemplates_AlreadyMigrated(t *testing.T) {
	root := t.TempDir()

	// Only destination exists.
	dstDir := filepath.Join(root, ".sdlc", "jira-templates")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "jira_templates", DryRun: false})
	if err != nil {
		t.Fatalf("migrate jira_templates: %v", err)
	}

	if !strings.Contains(out.Result, "already-migrated") {
		t.Errorf("expected 'already-migrated' in result, got %q", out.Result)
	}
}

func TestMigrate_JiraTemplates_BothExist(t *testing.T) {
	root := t.TempDir()

	// Both source and destination exist.
	if err := os.MkdirAll(filepath.Join(root, ".claude", "jira-templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".sdlc", "jira-templates"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "jira_templates", DryRun: false})
	if err != nil {
		t.Fatalf("migrate jira_templates: %v", err)
	}

	if !strings.Contains(out.Result, "skip") {
		t.Errorf("expected 'skip' in result, got %q", out.Result)
	}
}

func TestMigrate_JiraTemplates_DryRun(t *testing.T) {
	root := t.TempDir()

	srcDir := filepath.Join(root, ".claude", "jira-templates")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "jira_templates", DryRun: true})
	if err != nil {
		t.Fatalf("migrate jira_templates dry-run: %v", err)
	}

	if !strings.Contains(out.Result, "would-move") {
		t.Errorf("expected 'would-move' in result, got %q", out.Result)
	}
	// Source should still exist.
	if !migrateDirExists(srcDir) {
		t.Error("source should still exist during dry run")
	}
}

func TestMigrate_LearningsLog_Move(t *testing.T) {
	root := t.TempDir()

	// Create legacy learnings log.
	srcDir := filepath.Join(root, ".claude", "learnings")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "log.md"), []byte("# Learnings\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "learnings_log", DryRun: false})
	if err != nil {
		t.Fatalf("migrate learnings_log: %v", err)
	}

	if !out.OK {
		t.Error("expected OK=true")
	}
	if !strings.Contains(out.Result, "moved") {
		t.Errorf("expected 'moved' in result, got %q", out.Result)
	}

	// Source should be gone, destination should have the file.
	srcFile := filepath.Join(srcDir, "log.md")
	if migrateFileExists(srcFile) {
		t.Error("source file should be removed")
	}
	dstFile := filepath.Join(root, ".sdlc", "learnings", "log.md")
	if !migrateFileExists(dstFile) {
		t.Error("destination file should exist")
	}
	content, _ := os.ReadFile(dstFile)
	if string(content) != "# Learnings\n" {
		t.Errorf("destination content mismatch: %q", string(content))
	}
}

func TestMigrate_LearningsLog_Noop(t *testing.T) {
	root := t.TempDir()

	out, err := migrate(root, MigrateIn{Action: "learnings_log", DryRun: false})
	if err != nil {
		t.Fatalf("migrate learnings_log: %v", err)
	}

	if !strings.Contains(out.Result, "noop") {
		t.Errorf("expected 'noop' in result, got %q", out.Result)
	}
}

func TestMigrate_LearningsLog_DryRun(t *testing.T) {
	root := t.TempDir()

	srcDir := filepath.Join(root, ".claude", "learnings")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "log.md"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "learnings_log", DryRun: true})
	if err != nil {
		t.Fatalf("migrate learnings_log dry-run: %v", err)
	}

	if !strings.Contains(out.Result, "would-move") {
		t.Errorf("expected 'would-move' in result, got %q", out.Result)
	}
	// Source should still exist.
	if !migrateFileExists(filepath.Join(srcDir, "log.md")) {
		t.Error("source should still exist during dry run")
	}
}

func TestMigrate_LearningsLog_AlreadyMigrated(t *testing.T) {
	root := t.TempDir()

	// Only destination exists.
	dstDir := filepath.Join(root, ".sdlc", "learnings")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "log.md"), []byte("# log"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "learnings_log"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.Result, "already-migrated") {
		t.Errorf("expected already-migrated result, got %q", out.Result)
	}
}

func TestMigrate_LearningsLog_BothExist(t *testing.T) {
	root := t.TempDir()

	// Both source and destination exist.
	srcDir := filepath.Join(root, ".claude", "learnings")
	dstDir := filepath.Join(root, ".sdlc", "learnings")
	for _, d := range []string{srcDir, dstDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "log.md"), []byte("# log"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := migrate(root, MigrateIn{Action: "learnings_log"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.Result, "skip") {
		t.Errorf("expected skip result, got %q", out.Result)
	}
}

func TestMigrate_UnknownAction(t *testing.T) {
	root := t.TempDir()

	_, err := migrate(root, MigrateIn{Action: "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(err.Error(), "unknown migrate action") {
		t.Errorf("error should mention unknown action, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// AC: setup_init + configmigrate.Verify convergence
// ---------------------------------------------------------------------------

func TestSetupInit_ThenVerify_Passes(t *testing.T) {
	root := t.TempDir()

	// setup_init on empty fixture.
	_, err := setupInit(root, SetupInitIn{Sections: []string{"version", "jira"}})
	if err != nil {
		t.Fatalf("setupInit: %v", err)
	}

	// The scaffold must pass configmigrate.Verify.
	if err := configmigrate.Verify(root); err != nil {
		t.Errorf("config scaffold should pass Verify: %v", err)
	}
}

// AC: migrate config converges legacy/v4 fixtures to v5.
func TestMigrate_V4_ConvergesToV5(t *testing.T) {
	root := t.TempDir()

	writeTestJSON(t, filepath.Join(root, ".sdlc", "config.json"), map[string]any{
		"schemaVersion": float64(4),
		"version":       map[string]any{"mode": "file"},
		"jira":          map[string]any{"defaultProject": "X"},
	})

	_, err := migrate(root, MigrateIn{Action: "config"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if err := configmigrate.Verify(root); err != nil {
		t.Errorf("v4->v5 migration should result in valid v5: %v", err)
	}
}

func TestMigrate_Legacy_ConvergesToV5(t *testing.T) {
	root := t.TempDir()

	writeTestJSON(t, filepath.Join(root, ".claude", "sdlc.json"), map[string]any{
		"version": map[string]any{"mode": "tag"},
		"jira":    map[string]any{"defaultProject": "Y"},
	})

	_, err := migrate(root, MigrateIn{Action: "config"})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if err := configmigrate.Verify(root); err != nil {
		t.Errorf("legacy->v5 migration should result in valid v5: %v", err)
	}
}
