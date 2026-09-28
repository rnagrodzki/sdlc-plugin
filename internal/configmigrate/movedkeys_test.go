package configmigrate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// fixtureConfig mirrors the shipped template: comments, a commented-out
// quality line, and other tables around [pr] and [execute].
const fixtureConfig = `# Project config
[project]
name = "demo"

[pr]
# Expected gh account for PR creation.
expectedAccount = "alice"
titlePattern = "conventional"

# ─── Execute behavior ───
[execute]
# Run unattended.
auto = false

# Skip the quality-tier prompt.
# quality = "balanced"

# Auto-approve waves containing a High-risk task.
highRiskAutoApprove = false

[execute.guardrails.no-skip]
severity = "error"
description = "auto = true must not be forced"
`

func setupProject(t *testing.T, config, local string) (root, cfgPath, localPath string) {
	t.Helper()
	root = t.TempDir()
	dir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath = filepath.Join(dir, "config.toml")
	localPath = filepath.Join(dir, "local.toml")
	if err := os.WriteFile(cfgPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if local != "" {
		if err := os.WriteFile(localPath, []byte(local), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, cfgPath, localPath
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func removeLine(t *testing.T, text, line string) string {
	t.Helper()
	if strings.Count(text, line+"\n") != 1 {
		t.Fatalf("fixture must contain %q exactly once", line)
	}
	return strings.Replace(text, line+"\n", "", 1)
}

func TestMigrateMovedKeys_NoDataDir(t *testing.T) {
	root := t.TempDir()
	moved, err := MigrateMovedKeys(root)
	if moved != nil || err != nil {
		t.Fatalf("want (nil, nil), got (%v, %v)", moved, err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatalf("expected nothing created, found %d entries", len(entries))
	}
}

func TestMigrateMovedKeys_NoMovedKeys(t *testing.T) {
	cfg := "# c\n[pr]\ntitlePattern = \"x\"\n\n[execute]\n# quality = \"balanced\"\n"
	local := "[github]\nexpectedAccount = \"bob\"\n"
	root, cfgPath, localPath := setupProject(t, cfg, local)

	moved, err := MigrateMovedKeys(root)
	if moved != nil || err != nil {
		t.Fatalf("want (nil, nil), got (%v, %v)", moved, err)
	}
	if got := readFile(t, cfgPath); got != cfg {
		t.Fatalf("config.toml changed:\n%s", got)
	}
	if got := readFile(t, localPath); got != local {
		t.Fatalf("local.toml changed:\n%s", got)
	}
}

func TestMigrateMovedKeys_ExpectedAccountCreatesLocal(t *testing.T) {
	cfg := "# c\n[pr]\n# note\nexpectedAccount = \"alice\"\ntitlePattern = \"x\"\n\n[other]\nk = 1\n"
	root, cfgPath, localPath := setupProject(t, cfg, "")

	moved, err := MigrateMovedKeys(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []string{"pr.expectedAccount -> [github] expectedAccount"}; !reflect.DeepEqual(moved, want) {
		t.Fatalf("moved = %v, want %v", moved, want)
	}
	if got, want := readFile(t, cfgPath), removeLine(t, cfg, `expectedAccount = "alice"`); got != want {
		t.Fatalf("config.toml:\n got: %q\nwant: %q", got, want)
	}
	if got, want := readFile(t, localPath), "[github]\nexpectedAccount = 'alice'\n"; got != want {
		t.Fatalf("local.toml:\n got: %q\nwant: %q", got, want)
	}
}

func TestMigrateMovedKeys_TemplateExecuteBlock(t *testing.T) {
	cfg := strings.Replace(fixtureConfig, "expectedAccount = \"alice\"\n", "", 1)
	root, cfgPath, localPath := setupProject(t, cfg, "")

	moved, err := MigrateMovedKeys(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"execute.auto -> [executePrefs] auto",
		"execute.highRiskAutoApprove -> [executePrefs] highRiskAutoApprove",
	}
	if !reflect.DeepEqual(moved, want) {
		t.Fatalf("moved = %v, want %v", moved, want)
	}
	wantCfg := removeLine(t, removeLine(t, cfg, "auto = false"), "highRiskAutoApprove = false")
	if got := readFile(t, cfgPath); got != wantCfg {
		t.Fatalf("config.toml:\n got: %q\nwant: %q", got, wantCfg)
	}
	gotCfg := readFile(t, cfgPath)
	for _, keep := range []string{"[execute]\n", "# quality = \"balanced\"\n", "# Run unattended.\n", `description = "auto = true must not be forced"`} {
		if !strings.Contains(gotCfg, keep) {
			t.Fatalf("config.toml lost %q", keep)
		}
	}
	if got, want := readFile(t, localPath), "[executePrefs]\nauto = false\nhighRiskAutoApprove = false\n"; got != want {
		t.Fatalf("local.toml:\n got: %q\nwant: %q", got, want)
	}
}

func TestMigrateMovedKeys_InsertsUnderExistingHeader(t *testing.T) {
	cfg := "[execute]\nauto = true\n"
	local := "# personal\n[ executePrefs ]  # note\nquality = \"full\"\n\n[github]\nexpectedAccount = \"bob\"\n"
	root, _, localPath := setupProject(t, cfg, local)

	if _, err := MigrateMovedKeys(root); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "# personal\n[ executePrefs ]  # note\nauto = true\nquality = \"full\"\n\n[github]\nexpectedAccount = \"bob\"\n"
	got := readFile(t, localPath)
	if got != want {
		t.Fatalf("local.toml:\n got: %q\nwant: %q", got, want)
	}
	if strings.Count(got, "executePrefs") != 1 {
		t.Fatalf("expected a single [executePrefs] header, got:\n%s", got)
	}
}

func TestMigrateMovedKeys_CommentedHeaderNotUsed(t *testing.T) {
	cfg := "[pr]\nexpectedAccount = \"alice\"\n"
	local := "# [github]\n# expectedAccount = \"x\"\n"
	root, _, localPath := setupProject(t, cfg, local)

	if _, err := MigrateMovedKeys(root); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := local + "\n[github]\nexpectedAccount = 'alice'\n"
	if got := readFile(t, localPath); got != want {
		t.Fatalf("local.toml:\n got: %q\nwant: %q", got, want)
	}
}

func TestMigrateMovedKeys_SameValueOnlyDeletesFromConfig(t *testing.T) {
	cfg := "[pr]\nexpectedAccount = \"alice\"\ntitlePattern = \"x\"\n"
	local := "[github]\nexpectedAccount = \"alice\"\n"
	root, cfgPath, localPath := setupProject(t, cfg, local)

	moved, err := MigrateMovedKeys(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(moved) != 1 {
		t.Fatalf("moved = %v, want one entry", moved)
	}
	if got, want := readFile(t, cfgPath), "[pr]\ntitlePattern = \"x\"\n"; got != want {
		t.Fatalf("config.toml:\n got: %q\nwant: %q", got, want)
	}
	if got := readFile(t, localPath); got != local {
		t.Fatalf("local.toml changed:\n%s", got)
	}
}

func TestMigrateMovedKeys_DifferentValueConflict(t *testing.T) {
	cfg := "[pr]\nexpectedAccount = \"alice\"\n"
	local := "[github]\nexpectedAccount = \"bob\"\n"
	root, cfgPath, localPath := setupProject(t, cfg, local)

	moved, err := MigrateMovedKeys(root)
	if err == nil {
		t.Fatalf("expected *MovedKeysErr, got moved=%v", moved)
	}
	want := "Cannot move personal settings from .sdlc-v2/config.toml to .sdlc-v2/local.toml automatically: " +
		".sdlc-v2/local.toml already has a different value for [github] expectedAccount\n" +
		"  pr.expectedAccount -> [github] expectedAccount"
	if err.Error() != want {
		t.Fatalf("Error():\n got: %q\nwant: %q", err.Error(), want)
	}
	if got, want := err.Suggestion(), "Move each listed key into .sdlc-v2/local.toml under the new section by hand, delete it from .sdlc-v2/config.toml, then run the command again."; got != want {
		t.Fatalf("Suggestion():\n got: %q\nwant: %q", got, want)
	}
	if readFile(t, cfgPath) != cfg || readFile(t, localPath) != local {
		t.Fatalf("files changed on conflict")
	}
}

func TestMigrateMovedKeys_InlineTableLayoutError(t *testing.T) {
	cfg := "pr = { expectedAccount = \"a\" }\n"
	root, cfgPath, localPath := setupProject(t, cfg, "")

	_, err := MigrateMovedKeys(root)
	if err == nil {
		t.Fatalf("expected *MovedKeysErr for inline table")
	}
	if err.Reason != "config.toml layout not supported for automatic edit" {
		t.Fatalf("Reason = %q", err.Reason)
	}
	if readFile(t, cfgPath) != cfg {
		t.Fatalf("config.toml changed")
	}
	if _, statErr := os.Stat(localPath); !os.IsNotExist(statErr) {
		t.Fatalf("local.toml must not be created, stat err = %v", statErr)
	}
}

func TestMigrateMovedKeys_InvalidLocal(t *testing.T) {
	cfg := "[pr]\nexpectedAccount = \"a\"\n"
	local := "[github\n"
	root, cfgPath, localPath := setupProject(t, cfg, local)

	_, err := MigrateMovedKeys(root)
	if err == nil || err.Reason != "local.toml is not valid TOML" {
		t.Fatalf("want invalid-local error, got %v", err)
	}
	if readFile(t, cfgPath) != cfg || readFile(t, localPath) != local {
		t.Fatalf("files changed")
	}
}

func TestMigrateMovedKeys_SecondRunIsNoop(t *testing.T) {
	root, cfgPath, localPath := setupProject(t, fixtureConfig, "")

	moved, err := MigrateMovedKeys(root)
	if err != nil || len(moved) != 3 {
		t.Fatalf("first run: moved=%v err=%v", moved, err)
	}
	cfgAfter, localAfter := readFile(t, cfgPath), readFile(t, localPath)

	moved, err = MigrateMovedKeys(root)
	if moved != nil || err != nil {
		t.Fatalf("second run: want (nil, nil), got (%v, %v)", moved, err)
	}
	if readFile(t, cfgPath) != cfgAfter || readFile(t, localPath) != localAfter {
		t.Fatalf("second run changed files")
	}
}

func TestTableHeader(t *testing.T) {
	cases := []struct {
		line     string
		name     string
		isHeader bool
	}{
		{"[execute]\n", "execute", true},
		{"[ github ]  # note\n", "github", true},
		{"[execute.guardrails]\n", "execute.guardrails", true},
		{"[[execute.x]]\n", "", true},
		{"# [github]\n", "", false},
		{"auto = false\n", "", false},
	}
	for _, c := range cases {
		name, isHeader := tableHeader(c.line)
		if name != c.name || isHeader != c.isHeader {
			t.Errorf("tableHeader(%q) = (%q, %v), want (%q, %v)", c.line, name, isHeader, c.name, c.isHeader)
		}
	}
}

func TestMovedKeysWarning(t *testing.T) {
	got := MovedKeysWarning([]string{
		"execute.auto -> [executePrefs] auto",
		"execute.highRiskAutoApprove -> [executePrefs] highRiskAutoApprove",
	})
	want := "Moved personal settings from .sdlc-v2/config.toml to .sdlc-v2/local.toml:\n" +
		"  execute.auto -> [executePrefs] auto\n" +
		"  execute.highRiskAutoApprove -> [executePrefs] highRiskAutoApprove\n" +
		".sdlc-v2/config.toml is now modified. Commit that change so teammates stop inheriting these settings."
	if got != want {
		t.Fatalf("warning:\n got: %q\nwant: %q", got, want)
	}
}

func TestMovedLocalKeys_ExactEntries(t *testing.T) {
	n := 0
	for _, inner := range movedLocalKeys {
		n += len(inner)
	}
	if n != 4 {
		t.Fatalf("movedLocalKeys has %d entries, want 4", n)
	}
}
