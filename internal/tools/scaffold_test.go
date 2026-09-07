package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- scaffold_ci tests ---

// TestScaffoldCI_CreatesAllFiles verifies scaffold_ci writes all four
// destination files into an empty project root.
func TestScaffoldCI_CreatesAllFiles(t *testing.T) {
	root := t.TempDir()

	out, err := scaffoldCI(root, false)
	if err != nil {
		t.Fatalf("scaffoldCI: %v", err)
	}

	if len(out.Files) != 4 {
		t.Fatalf("expected 4 file reports, got %d", len(out.Files))
	}

	for _, f := range out.Files {
		if f.Action != "created" {
			t.Errorf("file %s: expected action 'created', got %q", f.Path, f.Action)
		}
		destPath := filepath.Join(root, f.Path)
		if !scaffoldFileExists(destPath) {
			t.Errorf("file %s: not written to disk at %s", f.Path, destPath)
		}
	}

	if len(out.Warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d: %v", len(out.Warnings), out.Warnings)
	}
}

// TestScaffoldCI_WrittenCJS_ConfigOrder verifies that written .cjs files
// have .sdlc/config.json before .claude/sdlc.json (AC 1).
func TestScaffoldCI_WrittenCJS_ConfigOrder(t *testing.T) {
	root := t.TempDir()

	_, err := scaffoldCI(root, false)
	if err != nil {
		t.Fatalf("scaffoldCI: %v", err)
	}

	cjsPaths := []string{
		filepath.Join(root, ".github", "scripts", "check-changelog.cjs"),
		filepath.Join(root, ".github", "scripts", "retag-release.cjs"),
	}

	for _, p := range cjsPaths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		content := string(data)

		sdlcIdx := strings.Index(content, ".sdlc/config.json")
		claudeIdx := strings.Index(content, ".claude/sdlc.json")

		if sdlcIdx < 0 {
			t.Errorf("%s: does not contain .sdlc/config.json", p)
			continue
		}
		if claudeIdx < 0 {
			t.Errorf("%s: does not contain .claude/sdlc.json", p)
			continue
		}
		if sdlcIdx >= claudeIdx {
			t.Errorf("%s: .sdlc/config.json (index %d) must appear before .claude/sdlc.json (index %d)",
				p, sdlcIdx, claudeIdx)
		}
	}
}

// TestScaffoldCI_SkipWithoutForce verifies that existing files are skipped
// when force=false.
func TestScaffoldCI_SkipWithoutForce(t *testing.T) {
	root := t.TempDir()

	// First run: create all files.
	_, err := scaffoldCI(root, false)
	if err != nil {
		t.Fatalf("scaffoldCI (first): %v", err)
	}

	// Second run without force: all should be skipped.
	out, err := scaffoldCI(root, false)
	if err != nil {
		t.Fatalf("scaffoldCI (second): %v", err)
	}

	for _, f := range out.Files {
		if f.Action != "skipped" {
			t.Errorf("file %s: expected action 'skipped', got %q", f.Path, f.Action)
		}
	}
}

// TestScaffoldCI_OverwriteWithForce verifies that existing files are
// overwritten when force=true.
func TestScaffoldCI_OverwriteWithForce(t *testing.T) {
	root := t.TempDir()

	// First run: create.
	_, err := scaffoldCI(root, false)
	if err != nil {
		t.Fatalf("scaffoldCI (first): %v", err)
	}

	// Second run with force: all should be overwritten.
	out, err := scaffoldCI(root, true)
	if err != nil {
		t.Fatalf("scaffoldCI (second): %v", err)
	}

	for _, f := range out.Files {
		if f.Action != "overwritten" {
			t.Errorf("file %s: expected action 'overwritten', got %q", f.Path, f.Action)
		}
	}
}

// TestScaffoldCI_LegacyMigration verifies that legacy .js files are migrated
// to .cjs when force=true.
func TestScaffoldCI_LegacyMigration(t *testing.T) {
	root := t.TempDir()

	// Create legacy .js files for the two entries that have LegacyDest.
	legacyPaths := []string{
		filepath.Join(root, ".github", "scripts", "retag-release.js"),
		filepath.Join(root, ".github", "scripts", "check-changelog.js"),
	}
	for _, p := range legacyPaths {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("// legacy\nconst RETAG_SCRIPT_VERSION = 1;\nconst CHECK_CHANGELOG_SCRIPT_VERSION = 1;\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Run without force: should report outdated.
	out, err := scaffoldCI(root, false)
	if err != nil {
		t.Fatalf("scaffoldCI (no-force): %v", err)
	}
	for _, f := range out.Files {
		if f.Path == filepath.Join(".github", "scripts", "retag-release.cjs") ||
			f.Path == filepath.Join(".github", "scripts", "check-changelog.cjs") {
			if f.Action != "outdated" {
				t.Errorf("file %s: expected 'outdated' without force, got %q", f.Path, f.Action)
			}
		}
	}

	// Run with force: should migrate.
	out, err = scaffoldCI(root, true)
	if err != nil {
		t.Fatalf("scaffoldCI (force): %v", err)
	}
	for _, f := range out.Files {
		if f.Path == filepath.Join(".github", "scripts", "retag-release.cjs") ||
			f.Path == filepath.Join(".github", "scripts", "check-changelog.cjs") {
			if f.Action != "migrated" {
				t.Errorf("file %s: expected 'migrated' with force, got %q", f.Path, f.Action)
			}
		}
	}

	// Verify legacy files are gone and new files exist.
	for _, p := range legacyPaths {
		if scaffoldFileExists(p) {
			t.Errorf("legacy file %s should have been deleted", p)
		}
	}
	for _, dest := range []string{
		filepath.Join(root, ".github", "scripts", "retag-release.cjs"),
		filepath.Join(root, ".github", "scripts", "check-changelog.cjs"),
	} {
		if !scaffoldFileExists(dest) {
			t.Errorf("new file %s should exist after migration", dest)
		}
	}
}

// --- verify_tag_ancestry tests ---

// initGitFixture creates a minimal git repo in dir with a commit, returning
// the commit hash.
func initGitFixture(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init", "-b", "main"},
		{"git", "config", "user.name", "test"},
		{"git", "config", "user.email", "test@test.com"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init setup %v: %s: %v", args, out, err)
		}
	}
}

func gitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	// Create a file to commit.
	f := filepath.Join(dir, msg+".txt")
	if err := os.WriteFile(f, []byte(msg), 0644); err != nil {
		t.Fatal(err)
	}
	cmds := [][]string{
		{"git", "add", "."},
		{"git", "commit", "-m", msg},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git commit %v: %s: %v", args, out, err)
		}
	}
}

func gitTag(t *testing.T, dir, tag string) {
	t.Helper()
	cmd := exec.Command("git", "tag", tag)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git tag %s: %s: %v", tag, out, err)
	}
}

// TestVerifyTagAncestry_PassOnAncestor verifies that a tag on an ancestor
// commit is reported as OK=true.
func TestVerifyTagAncestry_PassOnAncestor(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)

	// c1, tag v1.0.0, then c2 (HEAD).
	gitCommit(t, dir, "c1")
	gitTag(t, dir, "v1.0.0")
	gitCommit(t, dir, "c2")

	out, err := verifyTagAncestry(dir, "v1.0.0")
	if err != nil {
		t.Fatalf("verifyTagAncestry: %v", err)
	}
	if !out.OK {
		t.Errorf("expected OK=true, got false: %s", out.Details)
	}
}

// TestVerifyTagAncestry_FailOnNonAncestor verifies that a tag on a side branch
// commit is reported as OK=false.
func TestVerifyTagAncestry_FailOnNonAncestor(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)

	// c1 on main, branch off, tag on side branch, switch back to main.
	gitCommit(t, dir, "c1")

	cmd := exec.Command("git", "checkout", "-b", "side")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b side: %s: %v", out, err)
	}

	gitCommit(t, dir, "side-commit")
	gitTag(t, dir, "v2.0.0")

	cmd = exec.Command("git", "checkout", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout main: %s: %v", out, err)
	}

	gitCommit(t, dir, "c2-on-main")

	out, err := verifyTagAncestry(dir, "v2.0.0")
	if err != nil {
		t.Fatalf("verifyTagAncestry: %v", err)
	}
	if out.OK {
		t.Errorf("expected OK=false for non-ancestor tag, got true")
	}
	if !strings.Contains(out.Details, "not an ancestor") {
		t.Errorf("expected 'not an ancestor' in details, got: %s", out.Details)
	}
}

// TestVerifyTagAncestry_UnknownTag verifies that a non-existent tag is
// reported as OK=false.
func TestVerifyTagAncestry_UnknownTag(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "c1")

	out, err := verifyTagAncestry(dir, "v99.99.99")
	if err != nil {
		t.Fatalf("verifyTagAncestry: %v", err)
	}
	if out.OK {
		t.Errorf("expected OK=false for unknown tag, got true")
	}
	if !strings.Contains(out.Details, "does not exist") {
		t.Errorf("expected 'does not exist' in details, got: %s", out.Details)
	}
}

// TestVerifyTagAncestry_EmptyTag verifies that an empty tag returns OK=false.
func TestVerifyTagAncestry_EmptyTag(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "c1")

	out, err := verifyTagAncestry(dir, "")
	if err != nil {
		t.Fatalf("verifyTagAncestry: %v", err)
	}
	if out.OK {
		t.Errorf("expected OK=false for empty tag, got true")
	}
}

// --- openspec_enrich (enrichConfig) tests ---

// TestEnrichConfig_AppendNewBlock verifies that enrichConfig appends the
// managed block to a file without one.
func TestEnrichConfig_AppendNewBlock(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "openspec")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("name: test-project\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := enrichConfig(root, OpenspecEnrichIn{})
	if err != nil {
		t.Fatalf("enrichConfig: %v", err)
	}
	if out.Action != "append" {
		t.Errorf("expected action 'append', got %q", out.Action)
	}
	if !out.Changed {
		t.Error("expected Changed=true")
	}

	data, _ := os.ReadFile(configPath)
	content := string(data)
	if !strings.Contains(content, "BEGIN MANAGED BY sdlc-v2") {
		t.Error("written file does not contain managed block")
	}
}

// TestEnrichConfig_Unchanged verifies that enrichConfig is idempotent.
func TestEnrichConfig_Unchanged(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "openspec")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("name: test-project\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// First call: append.
	_, err := enrichConfig(root, OpenspecEnrichIn{})
	if err != nil {
		t.Fatalf("enrichConfig (first): %v", err)
	}

	// Second call: unchanged.
	out, err := enrichConfig(root, OpenspecEnrichIn{})
	if err != nil {
		t.Fatalf("enrichConfig (second): %v", err)
	}
	if out.Action != "unchanged" {
		t.Errorf("expected action 'unchanged', got %q", out.Action)
	}
	if out.Changed {
		t.Error("expected Changed=false")
	}
}

// TestEnrichConfig_SkipExistingContext verifies that enrichConfig refuses to
// inject when a top-level context: key already exists outside the managed block.
func TestEnrichConfig_SkipExistingContext(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "openspec")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("name: test-project\ncontext: existing stuff\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := enrichConfig(root, OpenspecEnrichIn{})
	if err != nil {
		t.Fatalf("enrichConfig: %v", err)
	}
	if out.Action != "skipped-existing-context" {
		t.Errorf("expected action 'skipped-existing-context', got %q", out.Action)
	}
	if out.Warning == "" {
		t.Error("expected non-empty warning")
	}
}

// TestEnrichConfig_Remove verifies that enrichConfig removes the managed block.
func TestEnrichConfig_Remove(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "openspec")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("name: test-project\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Append first.
	_, err := enrichConfig(root, OpenspecEnrichIn{})
	if err != nil {
		t.Fatalf("enrichConfig (append): %v", err)
	}

	// Remove.
	out, err := enrichConfig(root, OpenspecEnrichIn{Remove: true})
	if err != nil {
		t.Fatalf("enrichConfig (remove): %v", err)
	}
	if out.Action != "removed" {
		t.Errorf("expected action 'removed', got %q", out.Action)
	}
	if !out.Changed {
		t.Error("expected Changed=true")
	}

	data, _ := os.ReadFile(configPath)
	if strings.Contains(string(data), "BEGIN MANAGED BY sdlc-v2") {
		t.Error("managed block should have been removed")
	}
}

// TestEnrichConfig_MissingFile verifies error on missing config.yaml.
func TestEnrichConfig_MissingFile(t *testing.T) {
	root := t.TempDir()

	out, err := enrichConfig(root, OpenspecEnrichIn{})
	if err != nil {
		t.Fatalf("enrichConfig: %v", err)
	}
	if out.OK {
		t.Error("expected OK=false for missing file")
	}
	if out.Action != "missing" {
		t.Errorf("expected action 'missing', got %q", out.Action)
	}
}
