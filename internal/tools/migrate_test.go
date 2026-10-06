package tools

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

func TestMigrateImportCopiesFreshFiles(t *testing.T) {
	root := t.TempDir()

	oldDir := filepath.Join(root, paths.LegacyDataDir)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "config.json"), []byte(`{"jira":{"defaultProject":"A"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	jiraDir := filepath.Join(oldDir, "jira-templates")
	if err := os.MkdirAll(jiraDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jiraDir, "template.md"), []byte("template"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "import"})
	if err != nil {
		t.Fatalf("migrate import: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK, got %+v", out)
	}

	// The legacy source is JSON, but the merged destination is always TOML:
	// .sdlc-v2 config reads are TOML-only, so a JSON destination would never
	// be seen by any other reader.
	newConfig := filepath.Join(root, paths.DataDir, "config.toml")
	var got map[string]any
	if err := fsx.ReadTOML(newConfig, &got); err != nil {
		t.Fatalf("expected config.toml merged, got err=%v", err)
	}
	if jira, ok := got["jira"].(map[string]any); !ok || jira["defaultProject"] != "A" {
		t.Fatalf("expected config.toml to contain merged jira.defaultProject=A, got %v", got)
	}
	newTemplate := filepath.Join(root, paths.DataDir, "jira-templates", "template.md")
	if data, err := os.ReadFile(newTemplate); err != nil || string(data) != "template" {
		t.Fatalf("expected jira-templates/template.md copied, got err=%v data=%q", err, data)
	}

	// Source must remain untouched.
	if _, err := os.Stat(filepath.Join(oldDir, "config.json")); err != nil {
		t.Fatalf("expected source config.json to remain, got err=%v", err)
	}
	if _, err := os.Stat(jiraDir); err != nil {
		t.Fatalf("expected source jira-templates/ to remain, got err=%v", err)
	}

	wantChanged := []string{paths.DataDir + "/config.toml", paths.DataDir + "/jira-templates/"}
	if len(out.Changed) != len(wantChanged) {
		t.Fatalf("expected Changed=%v, got %v", wantChanged, out.Changed)
	}
}

// TestMigrateImportSkipsKeysNotAllowedInProjectConfig pins that import only
// merges keys config.toml allows. A legacy key such as schemaVersion (the
// pre-v5 marker) or a local-only section such as ship makes every later
// config read fail, so it is left out and reported in skippedKeys.
func TestMigrateImportSkipsKeysNotAllowedInProjectConfig(t *testing.T) {
	root := t.TempDir()

	oldDir := filepath.Join(root, paths.LegacyDataDir)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"schemaVersion":4,"ship":{"bump":"patch"},"jira":{"defaultProject":"OLD"}}`
	if err := os.WriteFile(filepath.Join(oldDir, "config.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "import"})
	if err != nil {
		t.Fatalf("migrate import: %v", err)
	}

	var got map[string]any
	if err := fsx.ReadTOML(filepath.Join(root, paths.DataDir, "config.toml"), &got); err != nil {
		t.Fatalf("read merged config.toml: %v", err)
	}
	if _, ok := got["jira"]; !ok {
		t.Errorf("expected allowed key jira merged, got %v", got)
	}
	for _, k := range []string{"schemaVersion", "ship"} {
		if _, ok := got[k]; ok {
			t.Errorf("config.toml holds %q, which config.toml does not allow: %v", k, got)
		}
	}
	if _, err := config.Read(root); err != nil {
		t.Errorf("config.Read after import: %v (imported config must stay readable)", err)
	}

	wantSkipped := []string{paths.DataDir + "/config.toml: schemaVersion", paths.DataDir + "/config.toml: ship"}
	if strings.Join(out.SkippedKeys, ",") != strings.Join(wantSkipped, ",") {
		t.Errorf("SkippedKeys = %v, want %v", out.SkippedKeys, wantSkipped)
	}
}

// TestMigrateImportOnlyDisallowedKeysChangesNothing pins that a legacy
// config holding only keys config.toml does not allow neither writes nor
// reports config.toml as changed.
func TestMigrateImportOnlyDisallowedKeysChangesNothing(t *testing.T) {
	root := t.TempDir()

	oldDir := filepath.Join(root, paths.LegacyDataDir)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "config.json"), []byte(`{"schemaVersion":4}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "import"})
	if err != nil {
		t.Fatalf("migrate import: %v", err)
	}
	if len(out.Changed) != 0 {
		t.Errorf("Changed = %v, want none", out.Changed)
	}
	if _, err := os.Stat(filepath.Join(root, paths.DataDir, "config.toml")); !os.IsNotExist(err) {
		t.Errorf("config.toml must not be written, stat err=%v", err)
	}
	if len(out.SkippedKeys) != 1 || out.SkippedKeys[0] != paths.DataDir+"/config.toml: schemaVersion" {
		t.Errorf("SkippedKeys = %v, want [%s/config.toml: schemaVersion]", out.SkippedKeys, paths.DataDir)
	}
}

func TestMigrateImportSkipsExistingNonJSONDestination(t *testing.T) {
	root := t.TempDir()

	oldDir := filepath.Join(root, paths.LegacyDataDir)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "pr-template.md"), []byte("legacy template"), 0o644); err != nil {
		t.Fatal(err)
	}

	newDir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "pr-template.md"), []byte("current template"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "import"})
	if err != nil {
		t.Fatalf("migrate import: %v", err)
	}
	if len(out.Changed) != 0 {
		t.Fatalf("expected no files copied when destination exists, got %v", out.Changed)
	}

	data, err := os.ReadFile(filepath.Join(newDir, "pr-template.md"))
	if err != nil || string(data) != "current template" {
		t.Fatalf("expected destination pr-template.md untouched, got err=%v data=%q", err, data)
	}
}

// TestMigrateImportMergesIntoScaffoldedEmptyDestination covers the bug this
// fix addresses: setup_init always seeds config.toml/local.toml as an empty
// scaffold before migrate ever runs, so a naive "file already exists" check
// made import permanently unreachable. Key-level merge must still import
// legacy sections into that empty scaffold.
func TestMigrateImportMergesIntoScaffoldedEmptyDestination(t *testing.T) {
	root := t.TempDir()

	oldDir := filepath.Join(root, paths.LegacyDataDir)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "local.json"), []byte(`{"ship":{"bump":"patch"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	newDir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "local.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "import"})
	if err != nil {
		t.Fatalf("migrate import: %v", err)
	}
	if len(out.Changed) != 1 || out.Changed[0] != paths.DataDir+"/local.toml" {
		t.Fatalf("expected local.toml reported changed, got %v", out.Changed)
	}

	var got map[string]any
	if err := fsx.ReadTOML(filepath.Join(newDir, "local.toml"), &got); err != nil {
		t.Fatalf("read merged local.toml: %v", err)
	}
	ship, ok := got["ship"].(map[string]any)
	if !ok || ship["bump"] != "patch" {
		t.Fatalf("expected ship.bump=patch merged in, got %v", got)
	}
}

// TestMigrateImportNeverOverwritesExistingDestinationKey ensures merge is
// additive-only: a key the destination already carries a real value for is
// left untouched, even though the same key exists in the legacy source.
func TestMigrateImportNeverOverwritesExistingDestinationKey(t *testing.T) {
	root := t.TempDir()

	oldDir := filepath.Join(root, paths.LegacyDataDir)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "config.json"), []byte(`{"version":{"tagPrefix":"legacy"},"jira":{"defaultProject":"OLD"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	newDir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "config.toml"), []byte("[version]\ntagPrefix = \"current\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "import"})
	if err != nil {
		t.Fatalf("migrate import: %v", err)
	}
	if len(out.Changed) != 1 || out.Changed[0] != paths.DataDir+"/config.toml" {
		t.Fatalf("expected config.toml reported changed, got %v", out.Changed)
	}

	var got map[string]any
	if err := fsx.ReadTOML(filepath.Join(newDir, "config.toml"), &got); err != nil {
		t.Fatalf("read merged config.toml: %v", err)
	}
	version, ok := got["version"].(map[string]any)
	if !ok || version["tagPrefix"] != "current" {
		t.Fatalf("expected existing version.tagPrefix=current preserved, got %v", got)
	}
	jira, ok := got["jira"].(map[string]any)
	if !ok || jira["defaultProject"] != "OLD" {
		t.Fatalf("expected jira key merged in from legacy source, got %v", got)
	}
	wantSkipped := paths.DataDir + "/config.toml: version (already set)"
	if len(out.SkippedKeys) != 1 || out.SkippedKeys[0] != wantSkipped {
		t.Errorf("SkippedKeys = %v, want [%s]", out.SkippedKeys, wantSkipped)
	}
}

// TestMigrateImportTOMLSource covers importing a legacy source file that is
// itself already TOML (e.g. an old plugin generation's directory that was
// migrated in place), rather than the more common legacy .json case.
func TestMigrateImportTOMLSource(t *testing.T) {
	root := t.TempDir()

	oldDir := filepath.Join(root, paths.LegacyDataDir)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "config.toml"), []byte("[jira]\ndefaultProject = \"NEW\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "import"})
	if err != nil {
		t.Fatalf("migrate import: %v", err)
	}
	if len(out.Changed) != 1 || out.Changed[0] != paths.DataDir+"/config.toml" {
		t.Fatalf("expected config.toml reported changed, got %v", out.Changed)
	}

	var got map[string]any
	if err := fsx.ReadTOML(filepath.Join(root, paths.DataDir, "config.toml"), &got); err != nil {
		t.Fatalf("read merged config.toml: %v", err)
	}
	jira, ok := got["jira"].(map[string]any)
	if !ok || jira["defaultProject"] != "NEW" {
		t.Fatalf("expected jira.defaultProject=NEW merged in from TOML source, got %v", got)
	}
}

// TestMigrateImportPrefersTOMLOverJSONSource covers legacyImportConfigFiles'
// documented precedence: when a legacy directory somehow holds both a .toml
// and a .json candidate for the same logical file, the .toml source wins
// and the .json fallback is never consulted.
func TestMigrateImportPrefersTOMLOverJSONSource(t *testing.T) {
	root := t.TempDir()

	oldDir := filepath.Join(root, paths.LegacyDataDir)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "config.toml"), []byte("[jira]\ndefaultProject = \"FROM_TOML\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "config.json"), []byte(`{"jira":{"defaultProject":"FROM_JSON"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "import"})
	if err != nil {
		t.Fatalf("migrate import: %v", err)
	}
	if len(out.Changed) != 1 || out.Changed[0] != paths.DataDir+"/config.toml" {
		t.Fatalf("expected exactly one config.toml change, got %v", out.Changed)
	}

	var got map[string]any
	if err := fsx.ReadTOML(filepath.Join(root, paths.DataDir, "config.toml"), &got); err != nil {
		t.Fatalf("read merged config.toml: %v", err)
	}
	jira, ok := got["jira"].(map[string]any)
	if !ok || jira["defaultProject"] != "FROM_TOML" {
		t.Fatalf("expected TOML source preferred over JSON, got %v", got)
	}
}

func TestMigrateImportDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()

	oldDir := filepath.Join(root, paths.LegacyDataDir)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "local.json"), []byte(`{"ship":{"bump":"patch"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "import", DryRun: true})
	if err != nil {
		t.Fatalf("migrate import dry-run: %v", err)
	}
	if !out.DryRun {
		t.Fatalf("expected DryRun=true in output, got %+v", out)
	}
	if len(out.Changed) != 1 || out.Changed[0] != paths.DataDir+"/local.toml" {
		t.Fatalf("expected would-import local.toml reported, got %v", out.Changed)
	}

	if _, err := os.Stat(filepath.Join(root, paths.DataDir, "local.toml")); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not write destination, stat err=%v", err)
	}
}

func TestMigrateImportMissingSourceIsNoop(t *testing.T) {
	root := t.TempDir()

	out, err := migrate(root, MigrateIn{Action: "import"})
	if err != nil {
		t.Fatalf("migrate import: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK even with no legacy source, got %+v", out)
	}
	if len(out.Changed) != 0 {
		t.Fatalf("expected no files copied when source is absent, got %v", out.Changed)
	}
}

func TestMigrateUnknownActionRejected(t *testing.T) {
	root := t.TempDir()
	if _, err := migrate(root, MigrateIn{Action: "jira_templates"}); err == nil {
		t.Fatal("expected error for removed jira_templates action, got nil")
	}
	if _, err := migrate(root, MigrateIn{Action: "learnings_log"}); err == nil {
		t.Fatal("expected error for removed learnings_log action, got nil")
	}
}

// writeLegacyFile writes name under the legacy data directory.
func writeLegacyFile(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, paths.LegacyDataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readDataFile returns the text of name under the data directory.
func readDataFile(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, paths.DataDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestMigrateImportReplacesUntouchedTemplateDefaults pins the normal /setup
// flow: setup_init writes the full templates first, then import runs. A
// legacy value replaces a key that still holds the template default, only
// that table's text changes (comments elsewhere survive), and whole numbers
// stay TOML integers.
func TestMigrateImportReplacesUntouchedTemplateDefaults(t *testing.T) {
	root := t.TempDir()
	if _, err := setupInit(root, SetupInitIn{}); err != nil {
		t.Fatalf("setupInit: %v", err)
	}
	writeLegacyFile(t, root, "config.json",
		`{"jira":{"defaultProject":"OLD"},"plan":{"guardrails":[{"id":"legacy-rule","severity":"warning","description":"Keep it small."}]}}`)
	writeLegacyFile(t, root, "local.json",
		`{"ship":{"bump":"minor","executeWaveInterval":90}}`)

	out, err := migrate(root, MigrateIn{Action: "import"})
	if err != nil {
		t.Fatalf("migrate import: %v", err)
	}
	wantChanged := []string{paths.DataDir + "/config.toml", paths.DataDir + "/local.toml"}
	if strings.Join(out.Changed, ",") != strings.Join(wantChanged, ",") {
		t.Errorf("Changed = %v, want %v", out.Changed, wantChanged)
	}
	if len(out.SkippedKeys) != 0 {
		t.Errorf("SkippedKeys = %v, want none", out.SkippedKeys)
	}

	cfg := readDataFile(t, root, "config.toml")
	if !strings.Contains(cfg, "[jira]\n# Default Jira project key (2–10 uppercase letters, e.g. \"PROJ\").\ndefaultProject = 'OLD'\n") {
		t.Errorf("jira.defaultProject not replaced in place:\n%s", cfg)
	}
	if !strings.Contains(cfg, "id = 'legacy-rule'") || strings.Contains(cfg, "test-coverage-required") {
		t.Errorf("plan not replaced by the legacy value:\n%s", cfg)
	}
	// Text outside the replaced tables is byte-for-byte the template.
	tmplHead := configTemplate[:strings.Index(configTemplate, "[jira]\n")]
	if !strings.HasPrefix(cfg, tmplHead) {
		t.Errorf("text before [jira] changed:\n%s", cfg)
	}
	commitToPR := configTemplate[strings.Index(configTemplate, "# Allowed Jira project keys"):strings.Index(configTemplate, "[plan.guardrails.test-coverage-required]")]
	if !strings.Contains(cfg, commitToPR) {
		t.Errorf("text between [jira] and [plan] changed:\n%s", cfg)
	}
	tmplTail := configTemplate[strings.Index(configTemplate, "[execute]\n"):]
	if !strings.HasSuffix(cfg, tmplTail) {
		t.Errorf("text from [execute] on changed:\n%s", cfg)
	}
	if _, err := config.Read(root); err != nil {
		t.Errorf("config.Read after import: %v", err)
	}

	local := readDataFile(t, root, "local.toml")
	if !strings.Contains(local, "bump = 'minor'") || !strings.Contains(local, "executeWaveInterval = 90\n") {
		t.Errorf("ship not replaced, or 90 not written as an integer:\n%s", local)
	}
	planStyle := localTemplate[strings.Index(localTemplate, "[planStyle]"):]
	if !strings.HasSuffix(local, planStyle) {
		t.Errorf("[planStyle] text changed:\n%s", local)
	}
}

// TestMigrateImportSkipsNonSectionKeys pins that a legacy top-level key whose
// value is not a table (e.g. the old local.json schema marker "version": 2)
// is not imported and is reported, so the import never needs a whole-file
// rewrite that would drop the template comments.
func TestMigrateImportSkipsNonSectionKeys(t *testing.T) {
	root := t.TempDir()
	if _, err := setupInit(root, SetupInitIn{}); err != nil {
		t.Fatalf("setupInit: %v", err)
	}
	writeLegacyFile(t, root, "local.json", `{"version":2,"ship":{"bump":"minor"}}`)

	out, err := migrate(root, MigrateIn{Action: "import"})
	if err != nil {
		t.Fatalf("migrate import: %v", err)
	}
	if len(out.Changed) != 1 || out.Changed[0] != paths.DataDir+"/local.toml" {
		t.Errorf("Changed = %v, want [%s/local.toml]", out.Changed, paths.DataDir)
	}
	want := paths.DataDir + "/local.toml: version (not a section)"
	if len(out.SkippedKeys) != 1 || out.SkippedKeys[0] != want {
		t.Errorf("SkippedKeys = %v, want [%s]", out.SkippedKeys, want)
	}
	local := readDataFile(t, root, "local.toml")
	if !strings.Contains(local, "bump = 'minor'") {
		t.Errorf("ship not replaced:\n%s", local)
	}
	head := localTemplate[:strings.Index(localTemplate, "[ship]\n")]
	tail := localTemplate[strings.Index(localTemplate, "[planStyle]"):]
	if !strings.HasPrefix(local, head) || !strings.HasSuffix(local, tail) {
		t.Errorf("comments outside [ship] changed:\n%s", local)
	}
	var got map[string]any
	if err := fsx.ReadTOML(filepath.Join(root, paths.DataDir, "local.toml"), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["version"]; ok {
		t.Errorf("local.toml holds the legacy version marker: %v", got)
	}
}

// TestMigrateImportKeepsUserChangedKey pins that a key the user changed from
// the template default is never replaced, and is reported in skippedKeys.
func TestMigrateImportKeepsUserChangedKey(t *testing.T) {
	root := t.TempDir()
	if _, err := setupInit(root, SetupInitIn{}); err != nil {
		t.Fatalf("setupInit: %v", err)
	}
	cfgPath := filepath.Join(root, paths.DataDir, "config.toml")
	edited := strings.Replace(configTemplate, "defaultProject = \"\"", "defaultProject = \"MINE\"", 1)
	if edited == configTemplate {
		t.Fatal("template has no defaultProject = \"\" line to edit")
	}
	if err := os.WriteFile(cfgPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLegacyFile(t, root, "config.json", `{"jira":{"defaultProject":"OLD"}}`)

	for _, dryRun := range []bool{true, false} {
		out, err := migrate(root, MigrateIn{Action: "import", DryRun: dryRun})
		if err != nil {
			t.Fatalf("migrate import (dryRun=%v): %v", dryRun, err)
		}
		if len(out.Changed) != 0 {
			t.Errorf("dryRun=%v: Changed = %v, want none", dryRun, out.Changed)
		}
		want := paths.DataDir + "/config.toml: jira (already set)"
		if len(out.SkippedKeys) != 1 || out.SkippedKeys[0] != want {
			t.Errorf("dryRun=%v: SkippedKeys = %v, want [%s]", dryRun, out.SkippedKeys, want)
		}
	}
	if got := readDataFile(t, root, "config.toml"); got != edited {
		t.Errorf("config.toml changed:\n%s", got)
	}
}

// commentLines returns the trimmed comment lines of s, in order, filtered
// with the same rule config.ErrWouldDropComments' caller uses: a line whose
// first non-blank character is "#".
func commentLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			out = append(out, l)
		}
	}
	return out
}

// TestMigrateImportKeepsEveryCommentLine verifies that importing a legacy
// value into a section of a commented local.toml keeps every comment line,
// in order — not just the text immediately around the changed keys —
// because the write splices only the changed key's value text in place.
func TestMigrateImportKeepsEveryCommentLine(t *testing.T) {
	root := t.TempDir()
	if _, err := setupInit(root, SetupInitIn{}); err != nil {
		t.Fatalf("setupInit: %v", err)
	}

	ship := shipSectionValues(t)
	ship["bump"] = "minor"
	legacyJSON, err := json.Marshal(map[string]any{"ship": ship})
	if err != nil {
		t.Fatalf("marshal legacy ship value: %v", err)
	}
	writeLegacyFile(t, root, "local.json", string(legacyJSON))

	out, err := migrate(root, MigrateIn{Action: "import"})
	if err != nil {
		t.Fatalf("migrate import: %v", err)
	}
	if len(out.Changed) != 1 || out.Changed[0] != paths.DataDir+"/local.toml" {
		t.Fatalf("Changed = %v, want [%s/local.toml]", out.Changed, paths.DataDir)
	}

	got := readDataFile(t, root, "local.toml")
	if !strings.Contains(got, "bump = 'minor'") {
		t.Errorf("legacy bump value not written:\n%s", got)
	}
	wantComments := commentLines(localTemplate)
	gotComments := commentLines(got)
	if len(wantComments) == 0 {
		t.Fatal("test fixture assumption broke: localTemplate has no comment lines")
	}
	if strings.Join(gotComments, "\n") != strings.Join(wantComments, "\n") {
		t.Errorf("comment lines changed.\n--- got ---\n%s\n--- want ---\n%s",
			strings.Join(gotComments, "\n"), strings.Join(wantComments, "\n"))
	}
}

// TestMigrateImportRefusesToDropComments verifies that when a destination
// section's layout cannot be spliced and the destination file has a comment
// line, import writes nothing and returns a *mcpserver.DomainError whose
// Suggestion names the section to edit by hand — rather than silently
// falling back to a whole-file rewrite that would delete every comment.
//
// defaults is tailored for this test rather than read from the shipped
// template: the shipped [ship] section is flat (no dotted sub-keys), so a
// destination laid out with one would never equal the real defaults, and
// importConfigFileMerge's "(already set)" filter would skip the write
// before it ever reached config.WriteFileSection. Passing a defaults value
// that matches this destination's actual (dotted) layout exercises the
// real write/refusal path that a hand-edited destination can reach once a
// future template gains a nested-table field.
func TestMigrateImportRefusesToDropComments(t *testing.T) {
	root := t.TempDir()
	content := "# kept\n[ship]\nretry.enabled = true\n"
	writeSDLCFile(t, root, "local.toml", content)
	writeLegacyFile(t, root, "local.json", `{"ship":{"retry":{"nested":{}}}}`)

	defaults := map[string]any{"ship": map[string]any{"retry": map[string]any{"enabled": true}}}
	rel, changed, skipped, err := importConfigFileMerge(root, "local.json", "local.toml", nil, defaults, false)
	if rel != "" || changed || skipped != nil {
		t.Errorf("rel=%q changed=%v skipped=%v, want all empty/false", rel, changed, skipped)
	}
	if !errors.Is(err, config.ErrWouldDropComments) {
		t.Fatalf("err = %v, want config.ErrWouldDropComments", err)
	}
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("err = %T, want *mcpserver.DomainError", err)
	}
	wantSuggestion := `Edit section ship in .sdlc-v2/local.toml by hand, then retry migrate with action "import".`
	if de.Suggestion != wantSuggestion {
		t.Errorf("Suggestion = %q, want %q", de.Suggestion, wantSuggestion)
	}

	got := readDataFile(t, root, "local.toml")
	if got != content {
		t.Errorf("destination changed despite the refusal:\n%s", got)
	}
}

// TestMigrateImportWriteFailureIsInfraError verifies writeErr's other branch:
// a config.WriteFileSection failure that is not ErrWouldDropComments (here
// the .sdlc-v2 directory is read-only, so the atomic write cannot create its
// temp file) surfaces as a *mcpserver.InfraError with the disk/permission
// recovery step, not as a DomainError.
func TestMigrateImportWriteFailureIsInfraError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory write permission")
	}
	root := t.TempDir()
	content := "[ship]\nbump = 'patch'\n"
	writeSDLCFile(t, root, "local.toml", content)
	writeLegacyFile(t, root, "local.json", `{"ship":{"bump":"minor"}}`)

	dataDir := filepath.Join(root, paths.DataDir)
	if err := os.Chmod(dataDir, 0o555); err != nil {
		t.Fatal(err)
	}
	// Registered after t.TempDir, so it runs first: RemoveAll needs write
	// permission on the directory.
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0o755) })

	defaults := map[string]any{"ship": map[string]any{"bump": "patch"}}
	rel, changed, skipped, err := importConfigFileMerge(root, "local.json", "local.toml", nil, defaults, false)
	if rel != "" || changed || skipped != nil {
		t.Errorf("rel=%q changed=%v skipped=%v, want all empty/false", rel, changed, skipped)
	}
	if err == nil {
		t.Fatal("err = nil, want the write failure")
	}
	if errors.Is(err, config.ErrWouldDropComments) {
		t.Fatalf("err = %v, must not be ErrWouldDropComments", err)
	}
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %T (%v), want *mcpserver.InfraError", err, err)
	}
	if !strings.HasPrefix(ie.Msg, "merge local.toml: ") {
		t.Errorf("Msg = %q, want prefix %q", ie.Msg, "merge local.toml: ")
	}
	wantSuggestion := "Check write permission and free disk space on " + paths.DataDir + `, then retry migrate with action "import".`
	if ie.Suggestion != wantSuggestion {
		t.Errorf("Suggestion = %q, want %q", ie.Suggestion, wantSuggestion)
	}
	if got := readDataFile(t, root, "local.toml"); got != content {
		t.Errorf("destination changed despite the failed write:\n%s", got)
	}
}
