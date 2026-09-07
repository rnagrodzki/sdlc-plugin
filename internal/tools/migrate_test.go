package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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

	newConfig := filepath.Join(root, paths.DataDir, "config.json")
	data, err := os.ReadFile(newConfig)
	if err != nil {
		t.Fatalf("expected config.json copied, got err=%v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("expected valid JSON, got err=%v data=%q", err, data)
	}
	if got["a"] != float64(1) {
		t.Fatalf("expected config.json to contain merged key a=1, got %v", got)
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

	wantChanged := []string{paths.DataDir + "/config.json", paths.DataDir + "/jira-templates/"}
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

// TestMigrateImportMergesIntoScaffoldedEmptyJSON covers the bug this fix
// addresses: setup_init always seeds config.json/local.json as an empty {}
// before migrate ever runs, so a naive "file already exists" check made
// JSON import permanently unreachable. Key-level merge must still import
// legacy sections into that empty scaffold.
func TestMigrateImportMergesIntoScaffoldedEmptyJSON(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(newDir, "local.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "import"})
	if err != nil {
		t.Fatalf("migrate import: %v", err)
	}
	if len(out.Changed) != 1 || out.Changed[0] != paths.DataDir+"/local.json" {
		t.Fatalf("expected local.json reported changed, got %v", out.Changed)
	}

	data, err := os.ReadFile(filepath.Join(newDir, "local.json"))
	if err != nil {
		t.Fatalf("read merged local.json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("expected valid JSON, got err=%v data=%q", err, data)
	}
	ship, ok := got["ship"].(map[string]any)
	if !ok || ship["bump"] != "patch" {
		t.Fatalf("expected ship.bump=patch merged in, got %v", got)
	}
}

// TestMigrateImportNeverOverwritesExistingJSONKey ensures merge is
// additive-only: a key the destination already carries a real value for is
// left untouched, even though the same key exists in the legacy source.
func TestMigrateImportNeverOverwritesExistingJSONKey(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(newDir, "config.json"), []byte(`{"version":{"tagPrefix":"current"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := migrate(root, MigrateIn{Action: "import"})
	if err != nil {
		t.Fatalf("migrate import: %v", err)
	}
	if len(out.Changed) != 1 || out.Changed[0] != paths.DataDir+"/config.json" {
		t.Fatalf("expected config.json reported changed, got %v", out.Changed)
	}

	data, err := os.ReadFile(filepath.Join(newDir, "config.json"))
	if err != nil {
		t.Fatalf("read merged config.json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("expected valid JSON, got err=%v data=%q", err, data)
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
	if len(out.Changed) != 1 || out.Changed[0] != paths.DataDir+"/local.json" {
		t.Fatalf("expected would-import local.json reported, got %v", out.Changed)
	}

	if _, err := os.Stat(filepath.Join(root, paths.DataDir, "local.json")); !os.IsNotExist(err) {
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
