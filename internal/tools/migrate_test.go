package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

func TestMigrateImportCopiesFreshFiles(t *testing.T) {
	root := t.TempDir()

	oldDir := filepath.Join(root, paths.LegacyDataDir)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "config.json"), []byte(`{"a":1}`), 0o644); err != nil {
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
	if got["a"] != float64(1) {
		t.Fatalf("expected config.toml to contain merged key a=1, got %v", got)
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
