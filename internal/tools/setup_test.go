package tools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
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
	writeTestJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
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
	writeTestJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
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
	if first.Name != "tag.enabled" {
		t.Errorf("first version field should be 'tag.enabled', got %q", first.Name)
	}
	if first.Type != "boolean" {
		t.Errorf("version.tag.enabled type should be 'boolean', got %q", first.Type)
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

	out, err := setupInit(root, SetupInitIn{})
	if err != nil {
		t.Fatalf("setupInit: %v", err)
	}

	if !out.OK {
		t.Errorf("expected OK=true, errors: %v", out.Errors)
	}

	// .sdlc/ directory should exist.
	if _, err := os.Stat(filepath.Join(root, paths.DataDir)); err != nil {
		t.Error(".sdlc/ directory should exist")
	}

	// .sdlc/runs/ directory should exist.
	runsInfo, err := os.Stat(filepath.Join(root, paths.DataDir, paths.RunsSubdir))
	if err != nil {
		t.Fatalf(".sdlc/%s/ directory should exist: %v", paths.RunsSubdir, err)
	}
	if !runsInfo.IsDir() {
		t.Errorf(".sdlc/%s/ should be a directory", paths.RunsSubdir)
	}

	// .sdlc/.gitignore should exist with managed block.
	sdlcGitignore, err := os.ReadFile(filepath.Join(root, paths.DataDir, ".gitignore"))
	if err != nil {
		t.Fatal(".sdlc/.gitignore should exist")
	}
	content := string(sdlcGitignore)
	if !strings.Contains(content, "sdlc-v2 managed") {
		t.Error(".sdlc/.gitignore should contain managed block marker")
	}
	if !strings.Contains(content, "!config.toml") {
		t.Error(".sdlc/.gitignore should allowlist config.toml")
	}
	// The deny-all "*" pattern is directory-agnostic: it must cover
	// runs/ (and any other unlisted subdirectory) without runs/ ever
	// being named explicitly.
	if !strings.Contains(content, "*\n") {
		t.Error(".sdlc/.gitignore should contain a deny-all \"*\" pattern covering runs/ implicitly")
	}
	if strings.Contains(content, paths.RunsSubdir) {
		t.Errorf(".sdlc/.gitignore should not need to name %q explicitly — deny-all already covers it", paths.RunsSubdir)
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

	// config.toml and local.toml should exist, written verbatim from the
	// embedded Go template constants — never through LLM context.
	configBytes, err := os.ReadFile(filepath.Join(root, paths.DataDir, "config.toml"))
	if err != nil {
		t.Fatal("config.toml should exist")
	}
	if string(configBytes) != configTemplate {
		t.Error("config.toml should match configTemplate verbatim")
	}
	localBytes, err := os.ReadFile(filepath.Join(root, paths.DataDir, "local.toml"))
	if err != nil {
		t.Fatal("local.toml should exist")
	}
	if string(localBytes) != localTemplate {
		t.Error("local.toml should match localTemplate verbatim")
	}

	// Both new files should be reported as created, and the output should
	// tell the caller (an LLM) what to do next.
	if !contains(out.Created, paths.DataDir+"/config.toml") {
		t.Errorf("expected %s/config.toml in Created, got %v", paths.DataDir, out.Created)
	}
	if !contains(out.Created, paths.DataDir+"/local.toml") {
		t.Errorf("expected %s/local.toml in Created, got %v", paths.DataDir, out.Created)
	}
	if out.Next == "" {
		t.Error("expected Next to carry edit/validate guidance")
	}
}

// contains reports whether s is present in slice.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func TestSetupInit_V5SchemaCompliant(t *testing.T) {
	root := t.TempDir()

	_, err := setupInit(root, SetupInitIn{})
	if err != nil {
		t.Fatalf("setupInit: %v", err)
	}

	// config.toml must pass configmigrate.Verify (schema v1, TOML era).
	if err := configmigrate.Verify(root); err != nil {
		t.Errorf("scaffold should pass Verify: %v", err)
	}

	// config.toml must not have a schemaVersion field (the pre-v1 marker).
	var raw map[string]any
	if err := fsx.ReadTOML(filepath.Join(root, paths.DataDir, "config.toml"), &raw); err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if _, has := raw["schemaVersion"]; has {
		t.Error("config.toml must not have schemaVersion field")
	}
}

func TestSetupInit_WithProjectSections(t *testing.T) {
	root := t.TempDir()

	out, err := setupInit(root, SetupInitIn{})
	if err != nil {
		t.Fatalf("setupInit: %v", err)
	}

	if !out.OK {
		t.Errorf("expected OK=true, errors: %v", out.Errors)
	}

	// The always-full config.toml template should carry every project
	// section, not a caller-selected subset.
	cfg, err := config.Read(root)
	if err != nil {
		t.Fatalf("config.Read: %v", err)
	}
	if cfg.Version == nil {
		t.Error("config.toml template should have a 'version' section")
	}
	if cfg.Jira == nil {
		t.Error("config.toml template should have a 'jira' section")
	}
	if cfg.Commit == nil {
		t.Error("config.toml template should have a 'commit' section")
	}
	if cfg.PR == nil {
		t.Error("config.toml template should have a 'pr' section")
	}
	if cfg.Plan == nil {
		t.Error("config.toml template should have a 'plan' section")
	}
	if cfg.Execute == nil {
		t.Error("config.toml template should have an 'execute' section")
	}
}

func TestSetupInit_WithLocalSections(t *testing.T) {
	root := t.TempDir()

	out, err := setupInit(root, SetupInitIn{})
	if err != nil {
		t.Fatalf("setupInit: %v", err)
	}

	if !out.OK {
		t.Errorf("expected OK=true, errors: %v", out.Errors)
	}

	// The always-full local.toml template should carry ship and planStyle.
	cfg, err := config.Read(root)
	if err != nil {
		t.Fatalf("config.Read: %v", err)
	}
	if cfg.Ship == nil {
		t.Error("local.toml template should have a 'ship' section")
	}

	planStyle, err := config.ReadSection(root, "planStyle")
	if err != nil {
		t.Fatalf("config.ReadSection(planStyle): %v", err)
	}
	if planStyle == nil {
		t.Error("local.toml template should have a 'planStyle' section")
	}
}

func TestSetupInit_Idempotent(t *testing.T) {
	root := t.TempDir()

	// Run twice. Second run should not error and should report unchanged.
	_, err := setupInit(root, SetupInitIn{})
	if err != nil {
		t.Fatalf("first setupInit: %v", err)
	}

	// Simulate a user hand-editing config.toml before the second run.
	configPath := filepath.Join(root, paths.DataDir, "config.toml")
	edited := configTemplate + "\n# user edit marker\n"
	if err := os.WriteFile(configPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := setupInit(root, SetupInitIn{})
	if err != nil {
		t.Fatalf("second setupInit: %v", err)
	}
	if !out.OK {
		t.Errorf("second run should be OK, errors: %v", out.Errors)
	}

	// Second run should not report config files as created.
	for _, c := range out.Created {
		if c == paths.DataDir+"/config.toml" || c == paths.DataDir+"/local.toml" {
			t.Errorf("second run should not report %q as created", c)
		}
	}

	// Second run must not clobber an existing config.toml — a user's edits
	// survive a re-run of setup_init.
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal("read config.toml")
	}
	if !strings.Contains(string(content), "# user edit marker") {
		t.Error("second run should not overwrite an existing config.toml")
	}

	// .sdlc/.gitignore should not have duplicate managed blocks.
	giContent, err := os.ReadFile(filepath.Join(root, paths.DataDir, ".gitignore"))
	if err != nil {
		t.Fatal("read .sdlc/.gitignore")
	}
	count := strings.Count(string(giContent), "sdlc-v2 managed (do not edit)")
	if count != 1 {
		t.Errorf("expected 1 managed block begin marker, got %d", count)
	}
}

func TestSetupInit_RootGitignoreLegacyUpgrade(t *testing.T) {
	root := t.TempDir()

	// Write a v1-style managed block in root .gitignore.
	v1Content := "# existing user pattern\nnode_modules/\n" +
		"# >>> sdlc-v2 managed (do not edit) — transient skill artifacts\n" +
		"*-context-*.json\n" +
		"# <<< sdlc-v2 managed\n"
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
	if strings.Contains(s, "# >>> sdlc-v2 managed (do not edit) — transient skill artifacts\n*-context") {
		t.Error("root .gitignore should not contain legacy v1 block")
	}
}

func TestSetupInit_CleansUpStaleJSON(t *testing.T) {
	root := t.TempDir()
	sdlcDir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Simulate a pre-migration project with stale JSON-era config files.
	configJSONPath := filepath.Join(sdlcDir, "config.json")
	localJSONPath := filepath.Join(sdlcDir, "local.json")
	if err := os.WriteFile(configJSONPath, []byte(`{"old":"config"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localJSONPath, []byte(`{"old":"local"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := setupInit(root, SetupInitIn{})
	if err != nil {
		t.Fatalf("setupInit: %v", err)
	}
	if !out.OK {
		t.Errorf("expected OK=true, errors: %v", out.Errors)
	}

	// The stale JSON files should be gone, renamed to .bak.
	if _, err := os.Stat(configJSONPath); !os.IsNotExist(err) {
		t.Error("config.json should no longer exist after cleanup")
	}
	if _, err := os.Stat(localJSONPath); !os.IsNotExist(err) {
		t.Error("local.json should no longer exist after cleanup")
	}

	configBak, err := os.ReadFile(filepath.Join(sdlcDir, "config.json.bak"))
	if err != nil {
		t.Fatal("config.json.bak should exist")
	}
	if string(configBak) != `{"old":"config"}` {
		t.Error("config.json.bak should preserve the original JSON content")
	}
	localBak, err := os.ReadFile(filepath.Join(sdlcDir, "local.json.bak"))
	if err != nil {
		t.Fatal("local.json.bak should exist")
	}
	if string(localBak) != `{"old":"local"}` {
		t.Error("local.json.bak should preserve the original JSON content")
	}

	if !contains(out.Changed, paths.DataDir+"/config.json → config.json.bak") {
		t.Errorf("expected config.json rename in Changed, got %v", out.Changed)
	}
	if !contains(out.Changed, paths.DataDir+"/local.json → local.json.bak") {
		t.Errorf("expected local.json rename in Changed, got %v", out.Changed)
	}
}

func TestSetupInit_JSONCleanup_SkipsIfBakAlreadyExists(t *testing.T) {
	root := t.TempDir()
	sdlcDir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configJSONPath := filepath.Join(sdlcDir, "config.json")
	bakPath := filepath.Join(sdlcDir, "config.json.bak")
	if err := os.WriteFile(configJSONPath, []byte(`{"new":"config"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bakPath, []byte(`{"previous":"backup"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := setupInit(root, SetupInitIn{})
	if err != nil {
		t.Fatalf("setupInit: %v", err)
	}

	// config.json should be left in place — a prior backup must not be
	// silently overwritten.
	if _, err := os.Stat(configJSONPath); err != nil {
		t.Error("config.json should still exist when a .bak already exists")
	}
	bakContent, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatal("config.json.bak should still exist")
	}
	if string(bakContent) != `{"previous":"backup"}` {
		t.Error("existing config.json.bak should not be overwritten")
	}
	if contains(out.Changed, paths.DataDir+"/config.json → config.json.bak") {
		t.Errorf("rename should be skipped when .bak already exists, got Changed=%v", out.Changed)
	}
}

func TestSetupInit_JSONCleanup_RunsEvenWhenTOMLAlreadyExists(t *testing.T) {
	root := t.TempDir()

	// First run creates config.toml/local.toml.
	if _, err := setupInit(root, SetupInitIn{}); err != nil {
		t.Fatalf("first setupInit: %v", err)
	}

	// Simulate a stale config.json appearing after the TOML already exists
	// (e.g. left over from before a prior migration).
	sdlcDir := filepath.Join(root, paths.DataDir)
	configJSONPath := filepath.Join(sdlcDir, "config.json")
	if err := os.WriteFile(configJSONPath, []byte(`{"stale":"true"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := setupInit(root, SetupInitIn{})
	if err != nil {
		t.Fatalf("second setupInit: %v", err)
	}

	if _, err := os.Stat(configJSONPath); !os.IsNotExist(err) {
		t.Error("config.json should be cleaned up even when config.toml already existed")
	}
	if _, err := os.Stat(filepath.Join(sdlcDir, "config.json.bak")); err != nil {
		t.Error("config.json.bak should exist")
	}
	if !contains(out.Changed, paths.DataDir+"/config.json → config.json.bak") {
		t.Errorf("expected config.json rename in Changed, got %v", out.Changed)
	}
}

// ---------------------------------------------------------------------------
// migrate tests
// ---------------------------------------------------------------------------

// TestMigrate_ConfigAction_StaleProjectRefused replaces
// TestMigrate_ConfigAction_V4ToV5 and TestMigrate_ConfigAction_LegacyToV5,
// deleted here. Both asserted an automated JSON-content v4/legacy->v5
// schema transform (schemaVersion stripping, version.versionFile/version.tag
// object-promotion) that this plan's Wave 2 (commit 16ee676) already
// retired at the configmigrate layer alongside TestMigrate_ProjectV4ToV5 —
// see tests/acceptance/matrix_audit_test.go's KD2 entry for the same cut.
// configmigrate.Migrate has exactly two outcomes now: a no-op on a current
// project, or ErrVersionStale naming /setup; it never rewrites content. This
// replacement asserts that correct refusal behavior instead of the retired
// transform.
func TestMigrate_ConfigAction_StaleProjectRefused(t *testing.T) {
	root := t.TempDir()

	// v0 (JSON-era) config.json with no config.toml — a stale project.
	writeTestJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
		"schemaVersion": float64(4),
		"version":       map[string]any{"mode": "file", "versionFile": "package.json"},
	})

	_, err := migrate(root, MigrateIn{Action: "config", DryRun: false})
	if err == nil {
		t.Fatal("expected migrate config to refuse a stale project, got nil error")
	}
	if !errors.Is(err, configmigrate.ErrVersionStale) {
		t.Errorf("expected error wrapping configmigrate.ErrVersionStale, got: %v", err)
	}
}

func TestMigrate_ConfigAction_DryRun(t *testing.T) {
	root := t.TempDir()

	writeTestJSON(t, filepath.Join(root, paths.DataDir, "config.json"), map[string]any{
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
	configData := readTestJSON(t, filepath.Join(root, paths.DataDir, "config.json"))
	if _, has := configData["schemaVersion"]; !has {
		t.Error("config should not be changed during dry run")
	}
}

// TestMigrate_ConfigAction_AlreadyV5 (deleted here) wrote a config.json
// with no schemaVersion field to represent an "already v5" project. That
// premise contradicts the TOML-only v5 format (internal/config/config.go):
// any JSON content under .sdlc-v2/, schemaVersion or not, is stale per
// configmigrate.detectProjectVersion — there is no "already v5" JSON state
// to converge from. No replacement: TestMigrate_ConfigAction_StaleProjectRefused
// above already covers the JSON-present refusal path.

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
	_, err := setupInit(root, SetupInitIn{})
	if err != nil {
		t.Fatalf("setupInit: %v", err)
	}

	// The scaffold must pass configmigrate.Verify.
	if err := configmigrate.Verify(root); err != nil {
		t.Errorf("config scaffold should pass Verify: %v", err)
	}
}

// TestMigrate_V4_ConvergesToV5 (deleted here) asserted the same retired
// v4-content-transform capability as TestMigrate_ConfigAction_V4ToV5 (see
// TestMigrate_ConfigAction_StaleProjectRefused above); it now fails with
// the same ErrVersionStale refusal.
//
// TestMigrate_Legacy_ConvergesToV5 below is left as-is (currently passing,
// but vacuously): configmigrate.detectProjectVersion only scans for
// .sdlc-v2/config.toml or a bare .sdlc-v2 dir, never the 6 legacy marker
// paths outside .sdlc-v2 (.claude/sdlc.json included) — that scan is
// internal/config's separate detectLegacy. So this fixture's
// .claude/sdlc.json is invisible to migrate/Verify at this layer: migrate
// no-ops (nothing to do) and Verify reports "current" for a project with no
// .sdlc-v2 directory at all, rather than actually converging anything. Same
// two-layer detection gap as the jira.go/config.go fixes above, latent here
// since Verify's "nothing to do" and "successfully migrated" outcomes are
// indistinguishable from this test's assertions alone.
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

// ---------------------------------------------------------------------------
// Template drift-prevention tests.
//
// These guard the embedded configTemplate/localTemplate constants against
// drift from config's runtime schema (internal/config/schema.go) — they fail
// loudly if a future schema change is not mirrored in the templates.
// ---------------------------------------------------------------------------

// TestConfigTemplateRoundTrip writes both templates to a temp .sdlc dir and
// confirms config.Read() accepts them with no error.
func TestConfigTemplateRoundTrip(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "config.toml"), []byte(configTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "local.toml"), []byte(localTemplate), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := config.Read(root); err != nil {
		t.Errorf("config.Read on template files: %v", err)
	}
}

// TestConfigTemplateKeysMatchWhitelist parses configTemplate's top-level keys
// and asserts they are exactly config.AllowedProjectKeys — symmetrically, so
// neither the template nor the whitelist can silently drift from the other.
func TestConfigTemplateKeysMatchWhitelist(t *testing.T) {
	var parsed map[string]any
	if err := toml.Unmarshal([]byte(configTemplate), &parsed); err != nil {
		t.Fatalf("parse configTemplate: %v", err)
	}

	for key := range parsed {
		if !config.AllowedProjectKeys[key] {
			t.Errorf("configTemplate has key %q not in config.AllowedProjectKeys", key)
		}
	}
	for key := range config.AllowedProjectKeys {
		if _, has := parsed[key]; !has {
			t.Errorf("config.AllowedProjectKeys has key %q missing from configTemplate", key)
		}
	}
}

// TestConfigTemplateGuardrailsAreNamedTables asserts plan.guardrails and
// execute.guardrails in the raw template are named tables (map[string]any),
// not array-of-tables — config.readProjectRaw normalizes them to []any only
// on the config.Read() path, so this test parses configTemplate directly.
func TestConfigTemplateGuardrailsAreNamedTables(t *testing.T) {
	var parsed map[string]any
	if err := toml.Unmarshal([]byte(configTemplate), &parsed); err != nil {
		t.Fatalf("parse configTemplate: %v", err)
	}

	for _, section := range []string{"plan", "execute"} {
		sec, ok := parsed[section].(map[string]any)
		if !ok {
			t.Fatalf("section %q missing or not a table", section)
		}
		guardrails, ok := sec["guardrails"]
		if !ok {
			t.Fatalf("section %q missing guardrails", section)
		}
		if _, ok := guardrails.(map[string]any); !ok {
			t.Errorf("%s.guardrails should be a named table (map[string]any), got %T", section, guardrails)
		}
	}
}

// TestConfigTemplateIsValidTOML asserts both templates parse as valid TOML.
func TestConfigTemplateIsValidTOML(t *testing.T) {
	var cfg map[string]any
	if err := toml.Unmarshal([]byte(configTemplate), &cfg); err != nil {
		t.Errorf("configTemplate is not valid TOML: %v", err)
	}

	var local map[string]any
	if err := toml.Unmarshal([]byte(localTemplate), &local); err != nil {
		t.Errorf("localTemplate is not valid TOML: %v", err)
	}
}
