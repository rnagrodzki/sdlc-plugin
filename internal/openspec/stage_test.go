package openspec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
)

// stageStatusJSON is the `openspec status --change <c> --json` payload the
// stub prints, in the spec-driven schema's artifact order.
const stageStatusJSON = `{"changeName":"add-widget","schemaName":"spec-driven","artifactPaths":{` +
	`"proposal":{"outputPath":"proposal.md","existingOutputPaths":[]},` +
	`"specs":{"outputPath":"specs/**/*.md","existingOutputPaths":[]},` +
	`"design":{"outputPath":"design.md","existingOutputPaths":[]},` +
	`"tasks":{"outputPath":"tasks.md","existingOutputPaths":[]}},` +
	`"artifacts":[` +
	`{"id":"proposal","outputPath":"proposal.md","status":"ready","requires":[]},` +
	`{"id":"specs","outputPath":"specs/**/*.md","status":"blocked","requires":["proposal"]},` +
	`{"id":"design","outputPath":"design.md","status":"blocked","requires":["proposal"]},` +
	`{"id":"tasks","outputPath":"tasks.md","status":"blocked","requires":["specs","design"]}]}`

// stubOpenspecStage writes an `openspec` stub into dir that dispatches on its
// subcommand. Every call appends its working directory to $STUB_LOG, so a
// test can check that each temp dir the code used was removed afterwards.
//   - new change <c>: creates openspec/changes/<c>/.openspec.yaml in cwd
//   - status: prints stageStatusJSON
//   - instructions <a>: prints guidance derived from <a>
//   - validate <c>: fails unless the staged proposal.md and .openspec.yaml
//     are present in cwd; fails with "missing target spec <cap>" when
//     $STUB_REQUIRE_SPEC is set and openspec/specs/<cap>/spec.md is absent in
//     cwd; exits $STUB_VALIDATE_EXIT (default 0) otherwise
func stubOpenspecStage(t *testing.T, dir string) {
	t.Helper()
	script := `#!/bin/sh
[ -n "$STUB_LOG" ] && pwd >> "$STUB_LOG"
case "$1" in
  new)
    mkdir -p "openspec/changes/$3" && : > "openspec/changes/$3/.openspec.yaml"
    echo "Created change '$3'"
    exit 0 ;;
  status)
    printf '%s\n' '` + stageStatusJSON + `'
    exit 0 ;;
  instructions)
    printf '{"artifactId":"%s","outputPath":"x","template":"T-%s","instruction":"I-%s","context":"ctx","rules":["r-%s"]}\n' "$2" "$2" "$2" "$2"
    exit 0 ;;
  validate)
    if [ ! -f "openspec/changes/$2/proposal.md" ] || [ ! -f "openspec/changes/$2/.openspec.yaml" ]; then
      echo "Change '$2' is missing files"
      exit 1
    fi
    if [ -n "$STUB_REQUIRE_SPEC" ] && [ ! -f "openspec/specs/$STUB_REQUIRE_SPEC/spec.md" ]; then
      echo "missing target spec $STUB_REQUIRE_SPEC"
      exit 1
    fi
    code="${STUB_VALIDATE_EXIT:-0}"
    if [ "$code" != 0 ]; then
      echo "Change '$2' has issues: proposal.md missing Why section"
      exit "$code"
    fi
    echo "Change '$2' is valid"
    exit 0 ;;
esac
exit 1
`
	if err := os.WriteFile(filepath.Join(dir, "openspec"), []byte(script), 0o755); err != nil {
		t.Fatalf("write openspec stub: %v", err)
	}
}

// setupStageRepo creates a real git repo in a temp dir holding
// openspec/config.yaml and the managed .sdlc-v2/.gitignore (the same
// selective ignores the plugin writes), commits it, and puts the openspec
// stub in front of the real PATH. Returns the repo root and the stub log
// path.
func setupStageRepo(t *testing.T) (root, log string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root = t.TempDir()
	mustWrite(t, filepath.Join(root, "openspec", "config.yaml"), "schema: spec-driven\n")
	mustWrite(t, filepath.Join(root, ".sdlc-v2", ".gitignore"),
		"# >>> sdlc-v2 managed (do not edit) — selective ignores\n*\n!.gitignore\n!config.toml\n!review-dimensions/\n!review-dimensions/**\n# <<< sdlc-v2 managed\n")
	git(t, root, "init", "-q")
	gitCommitAll(t, root)

	stub := t.TempDir()
	stubOpenspecStage(t, stub)
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
	log = filepath.Join(t.TempDir(), "stub.log")
	t.Setenv("STUB_LOG", log)
	t.Setenv("STUB_VALIDATE_EXIT", "0")
	return root, log
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func assertGitClean(t *testing.T, root string) {
	t.Helper()
	if out := git(t, root, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("git status --porcelain not empty:\n%s", out)
	}
}

// assertTempDirsRemoved checks that every directory the stub ran in is gone
// and that the stub ran exactly wantCalls times (0 means no CLI call).
func assertTempDirsRemoved(t *testing.T, log string, wantCalls int) {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read stub log: %v", err)
	}
	var dirs []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			dirs = append(dirs, line)
		}
	}
	if len(dirs) != wantCalls {
		t.Fatalf("openspec stub calls = %d (%v), want %d", len(dirs), dirs, wantCalls)
	}
	for _, d := range dirs {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Fatalf("temp dir %s still exists (stat err %v)", d, err)
		}
	}
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

var fixedNow = func() time.Time { return time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC) }

func stageFiles(withDesign bool) []StageFile {
	files := []StageFile{
		{Path: "proposal.md", Content: "# Proposal\n"},
		{Path: "specs/user-auth/spec.md", Content: "# Spec\n"},
		{Path: "tasks.md", Content: "# Tasks\n"},
	}
	if withDesign {
		files = append(files, StageFile{Path: "design.md", Content: "# Design\n"})
	}
	return files
}

func readManifest(t *testing.T, root, change string) StageManifest {
	t.Helper()
	var m StageManifest
	if err := fsx.ReadJSON(filepath.Join(root, ".sdlc-v2", "openspec-staging", change, "stage.json"), &m); err != nil {
		t.Fatalf("read stage.json: %v", err)
	}
	return m
}

func TestStage_InvalidChangeName(t *testing.T) {
	root, log := setupStageRepo(t)

	_, err := Stage(root, root, "grp/demo", stageFiles(false), "", fixedNow)
	if !errors.Is(err, ErrInvalidChangeName) {
		t.Fatalf("Stage err = %v, want ErrInvalidChangeName", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".sdlc-v2", "openspec-staging")); !os.IsNotExist(err) {
		t.Fatalf("staging root exists after a rejected name (stat err %v)", err)
	}
	assertTempDirsRemoved(t, log, 0)
	assertGitClean(t, root)
}

func TestStage_PathNotAllowed(t *testing.T) {
	cases := []struct {
		name, path string
		cliCalls   int // 0: rejected by syntax before any CLI call; 2: new + status
	}{
		{"traversal", "../config.toml", 0},
		{"absolute", "/etc/passwd", 0},
		{"unclean", "specs/./a/spec.md", 0},
		{"no glob match", "notes/readme.txt", 2},
		{"wrong extension", "specs/user-auth/spec.txt", 2},
		{"nested exact name", "foo/tasks.md", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, log := setupStageRepo(t)
			files := append(stageFiles(false), StageFile{Path: tc.path, Content: "x"})

			_, err := Stage(root, root, "add-widget", files, "", fixedNow)
			if !errors.Is(err, ErrPathNotAllowed) {
				t.Fatalf("Stage err = %v, want ErrPathNotAllowed", err)
			}
			if !strings.Contains(err.Error(), tc.path) {
				t.Fatalf("error %q does not name the path %q", err, tc.path)
			}
			if _, err := os.Stat(filepath.Join(root, ".sdlc-v2", "openspec-staging")); !os.IsNotExist(err) {
				t.Fatalf("staging root exists after a rejected path (stat err %v)", err)
			}
			assertTempDirsRemoved(t, log, tc.cliCalls)
			assertGitClean(t, root)
		})
	}
}

// writeStageManifestRaw writes raw as the stage.json of change under root.
func writeStageManifestRaw(t *testing.T, root, change, raw string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(StagingDirRel(change)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir staging dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, StageManifestFile), []byte(raw), 0o644); err != nil {
		t.Fatalf("write stage.json: %v", err)
	}
}

func TestStagedPlanPath(t *testing.T) {
	cases := []struct {
		name string
		raw  string // "" means no stage.json
		want string
	}{
		{"missing stage.json", "", ""},
		{"corrupt stage.json", "not json", ""},
		{"no planPath", `{"change":"add-widget","files":[]}`, ""},
		{"planPath of the wrong type", `{"change":"add-widget","planPath":1,"files":[]}`, ""},
		{"bad file path", `{"change":"add-widget","planPath":"/plans/a.md","files":[{"path":"../x.md"}]}`, ""},
		{"planPath set", `{"change":"add-widget","planPath":"/plans/a.md","files":[{"path":"proposal.md"}]}`, "/plans/a.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.raw != "" {
				writeStageManifestRaw(t, root, "add-widget", tc.raw)
			}
			if got := stagedPlanPath(root, "add-widget"); got != tc.want {
				t.Errorf("stagedPlanPath = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckStageInput_Owner(t *testing.T) {
	const owned = `{"change":"add-widget","planPath":"/plans/a.md","files":[]}`
	cases := []struct {
		name     string
		raw      string // "" means no stage.json
		planPath string
		wantErr  bool
	}{
		{"same path", owned, "/plans/a.md", false},
		{"same path after Clean", owned, "/plans/./a.md", false},
		{"other path", owned, "/plans/b.md", true},
		{"missing stage.json", "", "/plans/b.md", false},
		{"corrupt stage.json", "not json", "/plans/b.md", false},
		{"empty planPath", owned, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.raw != "" {
				writeStageManifestRaw(t, root, "add-widget", tc.raw)
			}
			err := checkStageInput(root, "add-widget", stageFiles(false), tc.planPath)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("checkStageInput err = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrStagedByOtherPlan) {
				t.Fatalf("checkStageInput err = %v, want ErrStagedByOtherPlan", err)
			}
			var staged *StagedByOtherPlanError
			if !errors.As(err, &staged) || staged.PlanPath != "/plans/a.md" {
				t.Fatalf("errors.As PlanPath = %+v, want /plans/a.md (err %v)", staged, err)
			}
			if want := "openspec stage: staged by another plan: /plans/a.md"; err.Error() != want {
				t.Errorf("err.Error() = %q, want %q", err.Error(), want)
			}
		})
	}
}

func TestStage_StagedByOtherPlanWritesNothing(t *testing.T) {
	root, log := setupStageRepo(t)
	const owned = `{"change":"add-widget","planPath":"/plans/a.md","files":[]}`
	writeStageManifestRaw(t, root, "add-widget", owned)

	_, err := Stage(root, root, "add-widget", stageFiles(false), "/plans/b.md", fixedNow)
	if !errors.Is(err, ErrStagedByOtherPlan) {
		t.Fatalf("Stage err = %v, want ErrStagedByOtherPlan", err)
	}
	dir := filepath.Join(root, filepath.FromSlash(StagingDirRel("add-widget")))
	got, err := os.ReadFile(filepath.Join(dir, StageManifestFile))
	if err != nil || string(got) != owned {
		t.Errorf("stage.json = %q (err %v), want it unchanged", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "proposal.md")); !os.IsNotExist(err) {
		t.Errorf("a refused Stage wrote proposal.md (stat err %v)", err)
	}
	assertTempDirsRemoved(t, log, 0)
}

func TestStage_DuplicatePathRejected(t *testing.T) {
	root, _ := setupStageRepo(t)
	files := append(stageFiles(false), StageFile{Path: "tasks.md", Content: "again"})
	if _, err := Stage(root, root, "add-widget", files, "", fixedNow); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("Stage err = %v, want ErrPathNotAllowed", err)
	}
}

func TestStage_ValidWritesFilesAndManifest(t *testing.T) {
	root, log := setupStageRepo(t)
	files := stageFiles(true)

	res, err := Stage(root, root, "add-widget", files, "/plans/p.md", fixedNow)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if res.Replaced || res.PlanPath != "/plans/p.md" {
		t.Fatalf("Replaced = %v, PlanPath = %q; want false, /plans/p.md", res.Replaced, res.PlanPath)
	}
	if !res.Valid {
		t.Fatalf("Valid = false, output %q", res.ValidateOutput)
	}
	if !strings.Contains(res.ValidateOutput, "is valid") {
		t.Fatalf("ValidateOutput = %q, want the CLI output", res.ValidateOutput)
	}
	if res.StagingDir != ".sdlc-v2/openspec-staging/add-widget/" {
		t.Fatalf("StagingDir = %q", res.StagingDir)
	}

	stagingDir := filepath.Join(root, ".sdlc-v2", "openspec-staging", "add-widget")
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(stagingDir, filepath.FromSlash(f.Path)))
		if err != nil || string(got) != f.Content {
			t.Fatalf("staged %s = %q (err %v), want %q", f.Path, got, err, f.Content)
		}
	}
	if _, err := os.Stat(filepath.Join(stagingDir, ".openspec.yaml")); !os.IsNotExist(err) {
		t.Fatalf(".openspec.yaml must never be staged (stat err %v)", err)
	}

	m := readManifest(t, root, "add-widget")
	if m.Change != "add-widget" || m.Schema != "spec-driven" || m.PlanPath != "/plans/p.md" {
		t.Fatalf("manifest header = %+v", m)
	}
	if m.ValidatedAt != "2026-10-01T12:00:00Z" {
		t.Fatalf("ValidatedAt = %q, want the fixed clock in RFC3339 UTC", m.ValidatedAt)
	}
	if len(m.Files) != len(files) {
		t.Fatalf("manifest files = %v, want %d entries", m.Files, len(files))
	}
	for i, f := range files {
		if m.Files[i].Path != f.Path || m.Files[i].SHA256 != sha(f.Content) {
			t.Fatalf("manifest file %d = %+v, want {%s %s}", i, m.Files[i], f.Path, sha(f.Content))
		}
	}
	if len(res.Files) != len(files) || res.Files[0] != m.Files[0] {
		t.Fatalf("result files %v do not match manifest %v", res.Files, m.Files)
	}

	// new + status + validate, all in one temp dir that is now gone.
	assertTempDirsRemoved(t, log, 3)
	assertGitClean(t, root)
	if _, err := os.Stat(filepath.Join(root, "openspec", "changes")); !os.IsNotExist(err) {
		t.Fatalf("openspec/changes created in the repo (stat err %v)", err)
	}
}

func TestStage_RestageReplacesDir(t *testing.T) {
	root, _ := setupStageRepo(t)
	if _, err := Stage(root, root, "add-widget", stageFiles(true), "", fixedNow); err != nil {
		t.Fatalf("first Stage: %v", err)
	}
	design := filepath.Join(root, ".sdlc-v2", "openspec-staging", "add-widget", "design.md")
	if _, err := os.Stat(design); err != nil {
		t.Fatalf("design.md not staged: %v", err)
	}

	res, err := Stage(root, root, "add-widget", stageFiles(false), "", fixedNow)
	if err != nil {
		t.Fatalf("restage: %v", err)
	}
	if !res.Replaced {
		t.Fatal("restage Replaced = false, want true")
	}
	if _, err := os.Stat(design); !os.IsNotExist(err) {
		t.Fatalf("design.md still present after a restage without it (stat err %v)", err)
	}
	for _, f := range readManifest(t, root, "add-widget").Files {
		if f.Path == "design.md" {
			t.Fatalf("manifest still lists design.md: %+v", f)
		}
	}
}

func TestStage_InvalidChangeReportsCLIOutput(t *testing.T) {
	root, log := setupStageRepo(t)
	t.Setenv("STUB_VALIDATE_EXIT", "1")

	res, err := Stage(root, root, "add-widget", stageFiles(false), "", fixedNow)
	if err != nil {
		t.Fatalf("Stage: %v (a failed validation is a result, not an error)", err)
	}
	if res.Valid {
		t.Fatalf("Valid = true, want false")
	}
	if !strings.Contains(res.ValidateOutput, "has issues") {
		t.Fatalf("ValidateOutput = %q, want the CLI output", res.ValidateOutput)
	}
	if m := readManifest(t, root, "add-widget"); m.ValidatedAt != "" {
		t.Fatalf("ValidatedAt = %q, want empty after a failed validation", m.ValidatedAt)
	}
	assertTempDirsRemoved(t, log, 3)
	assertGitClean(t, root)
	if _, err := os.Stat(filepath.Join(root, "openspec", "changes")); !os.IsNotExist(err) {
		t.Fatalf("openspec/changes created in the repo (stat err %v)", err)
	}
}

// gitCommitAll stages every change under root and commits it, so a test can
// add files to the repo and still assert a clean `git status --porcelain`.
// The commit may be empty: git does not track an empty directory.
func gitCommitAll(t *testing.T, root string) {
	t.Helper()
	git(t, root, "add", "-A")
	git(t, root, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "commit")
}

// TestStage_CopiesTargetSpecs checks that Stage puts the current spec of each
// capability that a staged delta names into the temp dir before validate runs,
// and that the copy keeps the bytes unchanged.
func TestStage_CopiesTargetSpecs(t *testing.T) {
	t.Run("validate sees the current spec", func(t *testing.T) {
		root, log := setupStageRepo(t)
		mustWrite(t, filepath.Join(root, "openspec", "specs", "user-auth", "spec.md"), "# user-auth\n")
		gitCommitAll(t, root)
		t.Setenv("STUB_REQUIRE_SPEC", "user-auth")

		res, err := Stage(root, root, "add-widget", stageFiles(true), "", fixedNow)
		if err != nil {
			t.Fatalf("Stage: %v", err)
		}
		if !res.Valid {
			t.Fatalf("Valid = false, output %q (validate must find the copied spec)", res.ValidateOutput)
		}
		assertTempDirsRemoved(t, log, 3)
		assertGitClean(t, root)
	})

	t.Run("only the delta capabilities are copied", func(t *testing.T) {
		root, _ := setupStageRepo(t)
		mustWrite(t, filepath.Join(root, "openspec", "specs", "user-auth", "spec.md"), "# user-auth\n")
		mustWrite(t, filepath.Join(root, "openspec", "specs", "billing", "spec.md"), "# billing\n")
		gitCommitAll(t, root)
		t.Setenv("STUB_REQUIRE_SPEC", "billing")

		res, err := Stage(root, root, "add-widget", stageFiles(false), "", fixedNow)
		if err != nil {
			t.Fatalf("Stage: %v", err)
		}
		if res.Valid || !strings.Contains(res.ValidateOutput, "missing target spec billing") {
			t.Fatalf("Valid = %v, output %q, want validate to miss the billing spec that no delta names", res.Valid, res.ValidateOutput)
		}
	})

	t.Run("copy is byte equal and skips other path shapes", func(t *testing.T) {
		root := t.TempDir()
		tmp := t.TempDir()
		// CRLF, a non-UTF-8 byte, a NUL byte and no trailing newline.
		content := "# user-auth\r\n\xff\x00 end"
		specs := filepath.Join(root, "openspec", "specs")
		mustWrite(t, filepath.Join(specs, "user-auth", "spec.md"), content)
		mustWrite(t, filepath.Join(specs, "user-auth", "other.md"), "other")
		mustWrite(t, filepath.Join(specs, "a", "b", "spec.md"), "deep")
		mustWrite(t, filepath.Join(specs, "spec.md"), "top")
		files := []StageFile{
			{Path: "proposal.md", Content: "# Proposal\n"},
			{Path: "specs/user-auth/spec.md", Content: "delta"},
			{Path: "specs/user-auth/other.md", Content: "delta"},
			{Path: "specs/a/b/spec.md", Content: "delta"},
			{Path: "specs/spec.md", Content: "delta"},
		}

		if err := copyTargetSpecs(root, tmp, files); err != nil {
			t.Fatalf("copyTargetSpecs: %v", err)
		}

		got, err := os.ReadFile(filepath.Join(tmp, "openspec", "specs", "user-auth", "spec.md"))
		if err != nil || string(got) != content {
			t.Fatalf("copied spec = %q (err %v), want %q", got, err, content)
		}
		for _, skipped := range []string{"user-auth/other.md", "a", "spec.md"} {
			p := filepath.Join(tmp, "openspec", "specs", filepath.FromSlash(skipped))
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Fatalf("%s copied into the temp dir (stat err %v)", skipped, err)
			}
		}
	})
}

// TestStage_NewCapabilityNoTarget checks that a staged spec with no current
// spec copies nothing and is not an error.
func TestStage_NewCapabilityNoTarget(t *testing.T) {
	root, log := setupStageRepo(t)

	res, err := Stage(root, root, "add-widget", stageFiles(true), "", fixedNow)
	if err != nil {
		t.Fatalf("Stage: %v (a new capability has no target spec)", err)
	}
	if !res.Valid {
		t.Fatalf("Valid = false, output %q", res.ValidateOutput)
	}
	assertTempDirsRemoved(t, log, 3)
	assertGitClean(t, root)

	tmp := t.TempDir()
	if err := copyTargetSpecs(root, tmp, stageFiles(false)); err != nil {
		t.Fatalf("copyTargetSpecs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "openspec")); !os.IsNotExist(err) {
		t.Fatalf("temp dir holds openspec/ after a copy with no target (stat err %v)", err)
	}
}

// TestStage_TargetSpecTooLarge checks that a current spec over 1 MiB, or one
// that cannot be read, stops Stage with an error that wraps ErrTargetSpec,
// before anything is written: no staging dir (so no stage.json and no
// validatedAt) and no change in the repo. Only the over-size case wraps
// ErrTargetSpecTooLarge. A spec of exactly 1 MiB is copied.
func TestStage_TargetSpecTooLarge(t *testing.T) {
	specPath := func(root string) string {
		return filepath.Join(root, "openspec", "specs", "user-auth", "spec.md")
	}
	cases := []struct {
		name     string
		setup    func(t *testing.T, root string)
		lock     bool // chmod 000 on the spec after the commit, restored after Stage
		want     string
		tooLarge bool
	}{
		{name: "over 1 MiB", setup: func(t *testing.T, root string) {
			mustWrite(t, specPath(root), strings.Repeat("a", maxTargetSpecBytes+1))
		}, want: "is over 1 MiB", tooLarge: true},
		{name: "read error", setup: func(t *testing.T, root string) {
			// A directory in place of the file: open works, read fails.
			if err := os.MkdirAll(specPath(root), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
		}, want: "read "},
		{name: "open permission denied", setup: func(t *testing.T, root string) {
			// chmod 000 on the file: open fails with EACCES, not not-exist.
			mustWrite(t, specPath(root), "# Spec\n")
		}, lock: true, want: "read "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.lock && os.Geteuid() == 0 {
				t.Skip("root ignores file permissions")
			}
			root, log := setupStageRepo(t)
			tc.setup(t, root)
			gitCommitAll(t, root)
			if tc.lock {
				if err := os.Chmod(specPath(root), 0); err != nil {
					t.Fatalf("chmod 000: %v", err)
				}
			}

			_, err := Stage(root, root, "add-widget", stageFiles(false), "", fixedNow)
			if tc.lock {
				if cerr := os.Chmod(specPath(root), 0o644); cerr != nil {
					t.Fatalf("restore mode: %v", cerr)
				}
			}
			if !errors.Is(err, ErrTargetSpec) {
				t.Fatalf("Stage err = %v, want ErrTargetSpec", err)
			}
			if got := errors.Is(err, ErrTargetSpecTooLarge); got != tc.tooLarge {
				t.Fatalf("errors.Is(err, ErrTargetSpecTooLarge) = %v, want %v (err %v)", got, tc.tooLarge, err)
			}
			if !strings.Contains(err.Error(), specPath(root)) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name the spec path and %q", err, tc.want)
			}
			if _, err := os.Stat(filepath.Join(root, ".sdlc-v2", "openspec-staging")); !os.IsNotExist(err) {
				t.Fatalf("staging root exists after a failed copy (stat err %v)", err)
			}
			assertTempDirsRemoved(t, log, 2) // new + status; validate never ran
			assertGitClean(t, root)
		})
	}

	t.Run("write into temp dir fails", func(t *testing.T) {
		// tmp is a regular file, so the directory for the copy cannot be
		// created. The writeFile error is returned as is: it is not a
		// target-spec error.
		root := t.TempDir()
		mustWrite(t, specPath(root), "# Spec\n")
		tmp := filepath.Join(t.TempDir(), "not-a-dir")
		mustWrite(t, tmp, "")

		err := copyTargetSpecs(root, tmp, stageFiles(false))
		if err == nil {
			t.Fatal("copyTargetSpecs err = nil, want the writeFile error")
		}
		if errors.Is(err, ErrTargetSpec) {
			t.Fatalf("copyTargetSpecs err = %v, want a write error that does not wrap ErrTargetSpec", err)
		}
		if !strings.Contains(err.Error(), "openspec: create dir for ") {
			t.Fatalf("copyTargetSpecs err = %q, want the writeFile create-dir error", err)
		}
	})

	t.Run("exactly 1 MiB is copied", func(t *testing.T) {
		root := t.TempDir()
		tmp := t.TempDir()
		content := strings.Repeat("a", maxTargetSpecBytes)
		mustWrite(t, specPath(root), content)

		if err := copyTargetSpecs(root, tmp, stageFiles(false)); err != nil {
			t.Fatalf("copyTargetSpecs: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(tmp, "openspec", "specs", "user-auth", "spec.md"))
		if err != nil || string(got) != content {
			t.Fatalf("copied spec has %d bytes (err %v), want %d", len(got), err, len(content))
		}
	})
}

// TestStage_RealCLIOmittedScenario runs the real openspec CLI on a MODIFIED
// delta that drops a scenario of the current spec. Stage must report
// valid false with the CLI's "omits scenario(s)" message and record no
// validatedAt. The same delta with the scenario restored must pass. The test
// skips when the openspec CLI is not on PATH.
func TestStage_RealCLIOmittedScenario(t *testing.T) {
	if _, err := exec.LookPath("openspec"); err != nil {
		t.Skip("openspec not on PATH; install it with npm i -g @fission-ai/openspec (CI installs a pinned version in .github/workflows/test.yml)")
	}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "openspec", "config.yaml"), "schema: spec-driven\n")
	current := "# widget Specification\n\n" +
		"## Purpose\nWidgets show data to the user on the dashboard page.\n\n" +
		"## Requirements\n\n" +
		"### Requirement: Dispatch metadata\nThe system SHALL record dispatch metadata for each task.\n\n" +
		"#### Scenario: First\n- **WHEN** a task is dispatched\n- **THEN** the metadata is recorded\n\n" +
		"#### Scenario: Second\n- **WHEN** a task is redispatched\n- **THEN** the attempt count grows\n"
	specFile := filepath.Join(root, "openspec", "specs", "widget", "spec.md")
	mustWrite(t, specFile, current)

	first := "#### Scenario: First\n- **WHEN** a task is dispatched\n- **THEN** the metadata is recorded\n"
	second := "\n#### Scenario: Second\n- **WHEN** a task is redispatched\n- **THEN** the attempt count grows\n"
	delta := func(scenarios string) string {
		return "## MODIFIED Requirements\n\n" +
			"### Requirement: Dispatch metadata\nThe system SHALL record dispatch metadata and the worker name for each task.\n\n" +
			scenarios
	}
	files := func(spec string) []StageFile {
		return []StageFile{
			{Path: "proposal.md", Content: "## Why\nThe widget metadata needs the worker name so that a reader can see which worker ran a task.\n\n" +
				"## What Changes\n- Record the worker name in the dispatch metadata.\n\n" +
				"## Capabilities\n\n### Modified Capabilities\n- `widget`: record the worker name\n\n## Impact\n- None.\n"},
			{Path: "specs/widget/spec.md", Content: spec},
			{Path: "design.md", Content: "## Context\nThe dispatch metadata gets one more field.\n\n## Decisions\n- Store the worker name next to the attempt count.\n"},
			{Path: "tasks.md", Content: "## 1. Work\n- [ ] 1.1 Record the worker name\n"},
		}
	}

	res, err := Stage(root, root, "add-widget", files(delta(first)), "", fixedNow)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if res.Valid {
		t.Fatalf("Valid = true, want false for a delta that drops a current scenario; output %q", res.ValidateOutput)
	}
	if !strings.Contains(res.ValidateOutput, "omits scenario(s)") {
		t.Fatalf("ValidateOutput = %q, want it to contain %q", res.ValidateOutput, "omits scenario(s)")
	}
	if m := readManifest(t, root, "add-widget"); m.ValidatedAt != "" {
		t.Fatalf("ValidatedAt = %q, want empty after a failed validation", m.ValidatedAt)
	}
	if got, err := os.ReadFile(specFile); err != nil || string(got) != current {
		t.Fatalf("current spec changed (err %v): %q", err, got)
	}
	if _, err := os.Stat(filepath.Join(root, "openspec", "changes")); !os.IsNotExist(err) {
		t.Fatalf("openspec/changes created in the repo (stat err %v)", err)
	}

	res, err = Stage(root, root, "add-widget", files(delta(first+second)), "", fixedNow)
	if err != nil {
		t.Fatalf("Stage with the scenario restored: %v", err)
	}
	if !res.Valid {
		t.Fatalf("Valid = false with the scenario restored; output %q", res.ValidateOutput)
	}
}

func TestPrepareInstructions(t *testing.T) {
	root, log := setupStageRepo(t)

	schema, guides, err := PrepareInstructions(root, "add-widget")
	if err != nil {
		t.Fatalf("PrepareInstructions: %v", err)
	}
	if schema != "spec-driven" {
		t.Fatalf("schema = %q", schema)
	}
	wantIDs := []string{"proposal", "specs", "design", "tasks"}
	if len(guides) != len(wantIDs) {
		t.Fatalf("guides = %+v, want %d", guides, len(wantIDs))
	}
	for i, id := range wantIDs {
		g := guides[i]
		if g.ID != id {
			t.Fatalf("guide %d ID = %q, want %q (status order)", i, g.ID, id)
		}
		if g.Template != "T-"+id || g.Instruction != "I-"+id || g.Context != "ctx" ||
			len(g.Rules) != 1 || g.Rules[0] != "r-"+id {
			t.Fatalf("guide %s instructions = %+v", id, g)
		}
	}
	if guides[1].OutputPath != "specs/**/*.md" {
		t.Fatalf("specs OutputPath = %q, want the status value", guides[1].OutputPath)
	}
	if got := strings.Join(guides[3].Requires, ","); got != "specs,design" {
		t.Fatalf("tasks Requires = %q", got)
	}

	// new + status + one instructions call per artifact.
	assertTempDirsRemoved(t, log, 2+len(wantIDs))
	assertGitClean(t, root)
	if _, err := os.Stat(filepath.Join(root, ".sdlc-v2", "openspec-staging")); !os.IsNotExist(err) {
		t.Fatalf("PrepareInstructions wrote under the repo (stat err %v)", err)
	}
}

func TestPrepareInstructions_InvalidChangeName(t *testing.T) {
	root, log := setupStageRepo(t)
	if _, _, err := PrepareInstructions(root, "Bad_Name"); !errors.Is(err, ErrInvalidChangeName) {
		t.Fatalf("err = %v, want ErrInvalidChangeName", err)
	}
	assertTempDirsRemoved(t, log, 0)
}

func TestMatchOutputPath(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"proposal.md", "proposal.md", true},
		{"tasks.md", "foo/tasks.md", false},
		{"specs/**/*.md", "specs/spec.md", true},
		{"specs/**/*.md", "specs/user-auth/spec.md", true},
		{"specs/**/*.md", "specs/a/b/c.md", true},
		{"specs/**/*.md", "specs/user-auth/spec.txt", false},
		{"specs/**/*.md", "other/user-auth/spec.md", false},
		{"specs/**/*.md", "specs", false},
		{"*.md", "a/b.md", false},
		{"**/*.md", "b.md", true},
		{"**", "a/b/c", true},
	}
	for _, tc := range cases {
		if got := matchOutputPath(tc.pattern, tc.path); got != tc.want {
			t.Errorf("matchOutputPath(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

// TestStage_WritesUnderStagingRoot checks that Stage with two different roots
// writes the files and stage.json under the staging root, writes nothing under
// the active root's staging dir, and reads the current spec from the active
// root (the stub's validate fails when that spec was not copied).
func TestStage_WritesUnderStagingRoot(t *testing.T) {
	active, log := setupStageRepo(t)
	stagingRoot := t.TempDir()
	mustWrite(t, filepath.Join(active, "openspec", "specs", "user-auth", "spec.md"), "# Current user-auth spec\n")
	t.Setenv("STUB_REQUIRE_SPEC", "user-auth")
	files := stageFiles(true)

	res, err := Stage(active, stagingRoot, "add-widget", files, "/plans/p.md", fixedNow)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if !res.Valid {
		t.Fatalf("Valid = false, output %q (the current spec was not read from the active root)", res.ValidateOutput)
	}
	if res.StagingDir != ".sdlc-v2/openspec-staging/add-widget/" {
		t.Fatalf("StagingDir = %q", res.StagingDir)
	}
	dir := filepath.Join(stagingRoot, filepath.FromSlash(StagingDirRel("add-widget")))
	got := snapshot(t, dir)
	for _, f := range files {
		if got[f.Path] != f.Content {
			t.Fatalf("staged %s = %q, want %q", f.Path, got[f.Path], f.Content)
		}
	}
	m := readManifest(t, stagingRoot, "add-widget")
	if m.PlanPath != "/plans/p.md" || m.ValidatedAt == "" || len(m.Files) != len(files) {
		t.Fatalf("stage.json = %+v, want planPath /plans/p.md, validatedAt set, %d files", m, len(files))
	}
	assertMissing(t, filepath.Join(active, ".sdlc-v2", "openspec-staging"))
	assertTempDirsRemoved(t, log, 3)
}

// TestStage_OwnerCheckUsesStagingRoot checks that the owner check reads
// stage.json under the staging root: an owner there refuses the stage, and an
// owner under the active root only is not read.
func TestStage_OwnerCheckUsesStagingRoot(t *testing.T) {
	const owned = `{"change":"add-widget","planPath":"/plans/a.md","files":[]}`

	t.Run("owner under staging root refuses", func(t *testing.T) {
		active, log := setupStageRepo(t)
		stagingRoot := t.TempDir()
		writeStageManifestRaw(t, stagingRoot, "add-widget", owned)

		_, err := Stage(active, stagingRoot, "add-widget", stageFiles(false), "/plans/b.md", fixedNow)
		var staged *StagedByOtherPlanError
		if !errors.As(err, &staged) || staged.PlanPath != "/plans/a.md" {
			t.Fatalf("Stage err = %v, want StagedByOtherPlanError for /plans/a.md", err)
		}
		assertMissing(t, filepath.Join(active, ".sdlc-v2", "openspec-staging"))
		assertTempDirsRemoved(t, log, 0)
	})

	t.Run("owner under active root only is not read", func(t *testing.T) {
		active, _ := setupStageRepo(t)
		stagingRoot := t.TempDir()
		writeStageManifestRaw(t, active, "add-widget", owned)
		activeDir := filepath.Join(active, filepath.FromSlash(StagingDirRel("add-widget")))
		before := snapshot(t, activeDir)

		res, err := Stage(active, stagingRoot, "add-widget", stageFiles(false), "/plans/b.md", fixedNow)
		if err != nil {
			t.Fatalf("Stage: %v (the owner check must not read the active root)", err)
		}
		if res.Replaced {
			t.Fatalf("Replaced = true, want false: the staging root had no staging dir")
		}
		if m := readManifest(t, stagingRoot, "add-widget"); m.PlanPath != "/plans/b.md" {
			t.Fatalf("stage.json planPath = %q, want /plans/b.md", m.PlanPath)
		}
		if after := snapshot(t, activeDir); !reflect.DeepEqual(before, after) {
			t.Fatalf("active-root staging changed:\nbefore %v\nafter  %v", before, after)
		}
	})
}

// TestStage_MissingArtifact checks that a stage without a schema artifact is
// a validation result: Valid false, one "missing artifact <outputPath>" line
// for each gap, the files still staged, and no validatedAt.
func TestStage_MissingArtifact(t *testing.T) {
	root, _ := setupStageRepo(t)
	files := []StageFile{{Path: "proposal.md", Content: "# Proposal\n"}}

	res, err := Stage(root, root, "add-widget", files, "/plans/p.md", fixedNow)
	if err != nil {
		t.Fatalf("Stage: %v (a missing artifact is a result, not an error)", err)
	}
	if res.Valid {
		t.Fatalf("Valid = true, want false; output %q", res.ValidateOutput)
	}
	wantLines := []string{"missing artifact specs/**/*.md", "missing artifact design.md", "missing artifact tasks.md", "Change 'add-widget' is valid"}
	if got := strings.Split(res.ValidateOutput, "\n"); !reflect.DeepEqual(got, wantLines) {
		t.Fatalf("ValidateOutput lines = %q, want %q", got, wantLines)
	}
	m := readManifest(t, root, "add-widget")
	if m.ValidatedAt != "" {
		t.Fatalf("ValidatedAt = %q, want empty with a missing artifact", m.ValidatedAt)
	}
	if len(m.Files) != 1 || m.Files[0].Path != "proposal.md" {
		t.Fatalf("manifest files = %+v, want proposal.md only", m.Files)
	}
}

// TestStage_ConcurrentOwners runs two Stage calls for one change with two plan
// paths at the same time (run it under -race). Exactly one succeeds; the other
// gets *StagedByOtherPlanError or ErrStagingLocked, and the staging belongs to
// the winner.
func TestStage_ConcurrentOwners(t *testing.T) {
	root, _ := setupStageRepo(t)
	plans := []string{"/plans/a.md", "/plans/b.md"}
	errs := make([]error, len(plans))
	start := make(chan struct{})
	done := make(chan int)
	for i, p := range plans {
		go func(i int, p string) {
			<-start
			_, errs[i] = Stage(root, root, "add-widget", stageFiles(true), p, fixedNow)
			done <- i
		}(i, p)
	}
	close(start)
	<-done
	<-done

	winner := -1
	for i, err := range errs {
		if err == nil {
			if winner != -1 {
				t.Fatalf("both stages succeeded, want one")
			}
			winner = i
			continue
		}
		var staged *StagedByOtherPlanError
		if !errors.As(err, &staged) && !errors.Is(err, ErrStagingLocked) {
			t.Fatalf("stage %d err = %v, want StagedByOtherPlanError or ErrStagingLocked", i, err)
		}
	}
	if winner == -1 {
		t.Fatalf("no stage succeeded: %v", errs)
	}
	if m := readManifest(t, root, "add-widget"); m.PlanPath != plans[winner] {
		t.Fatalf("stage.json planPath = %q, want the winner %q", m.PlanPath, plans[winner])
	}
	assertMissing(t, StagingLockPath(root, "add-widget"))
}

// stagingParent returns <root>/.sdlc-v2/openspec-staging.
func stagingParent(root string) string {
	return filepath.Join(root, ".sdlc-v2", "openspec-staging")
}

// writeLock creates the lock of add-widget under root with mtime at.
func writeLock(t *testing.T, root string, at time.Time) string {
	t.Helper()
	lock := StagingLockPath(root, "add-widget")
	mustWrite(t, lock, "pid 1\n")
	if err := os.Chtimes(lock, at, at); err != nil {
		t.Fatalf("chtimes lock: %v", err)
	}
	return lock
}

// TestStage_Lock covers the lock rows: a stale lock is taken over, a fresh
// lock or a stray folder of the lock name refuses with ErrStagingLocked, a
// lock create error is a *StageIOError, and a fresh repo gets the folder.
func TestStage_Lock(t *testing.T) {
	t.Run("lock age at the limit", func(t *testing.T) {
		root, _ := setupStageRepo(t)
		lock := writeLock(t, root, fixedNow().Add(-stagingLockStaleAfter))
		res, err := Stage(root, root, "add-widget", stageFiles(true), "", fixedNow)
		if err != nil || !res.Valid {
			t.Fatalf("Stage = %+v, %v; want a valid stage after the stale lock", res, err)
		}
		assertMissing(t, lock)
	})

	t.Run("lock age just below", func(t *testing.T) {
		root, _ := setupStageRepo(t)
		lock := writeLock(t, root, fixedNow().Add(-stagingLockStaleAfter+time.Second))
		_, err := Stage(root, root, "add-widget", stageFiles(true), "", fixedNow)
		if !errors.Is(err, ErrStagingLocked) || !strings.Contains(err.Error(), lock) {
			t.Fatalf("Stage err = %v, want ErrStagingLocked naming %s", err, lock)
		}
		assertMissing(t, filepath.Join(stagingParent(root), "add-widget"))
		if _, err := os.Stat(lock); err != nil {
			t.Fatalf("the fresh lock of another stage was deleted: %v", err)
		}
	})

	t.Run("name clash", func(t *testing.T) {
		root, _ := setupStageRepo(t)
		if err := os.MkdirAll(StagingLockPath(root, "add-widget"), 0o755); err != nil {
			t.Fatalf("mkdir stray lock folder: %v", err)
		}
		_, err := Stage(root, root, "add-widget", stageFiles(true), "", time.Now)
		if !errors.Is(err, ErrStagingLocked) {
			t.Fatalf("Stage err = %v, want ErrStagingLocked", err)
		}
	})

	t.Run("lock create other error", func(t *testing.T) {
		root, _ := setupStageRepo(t)
		orig := stageOpenLock
		t.Cleanup(func() { stageOpenLock = orig })
		stageOpenLock = func(string, int, os.FileMode) (*os.File, error) { return nil, os.ErrPermission }
		_, err := Stage(root, root, "add-widget", stageFiles(true), "", fixedNow)
		var ioErr *StageIOError
		if !errors.As(err, &ioErr) || ioErr.Op != "lock" || !errors.Is(err, os.ErrPermission) {
			t.Fatalf("Stage err = %v, want a lock StageIOError wrapping ErrPermission", err)
		}
		if want := "openspec stage: lock " + StagingLockPath(root, "add-widget") + ": "; !strings.HasPrefix(err.Error(), want) {
			t.Fatalf("err = %q, want prefix %q", err, want)
		}
		assertMissing(t, filepath.Join(stagingParent(root), "add-widget"))
	})

	t.Run("fresh repo", func(t *testing.T) {
		root, _ := setupStageRepo(t)
		assertMissing(t, stagingParent(root))
		res, err := Stage(root, root, "add-widget", stageFiles(true), "", fixedNow)
		if err != nil || !res.Valid {
			t.Fatalf("Stage = %+v, %v; want a valid stage", res, err)
		}
		entries, err := os.ReadDir(stagingParent(root))
		if err != nil {
			t.Fatalf("read staging parent: %v", err)
		}
		if len(entries) != 1 || entries[0].Name() != "add-widget" {
			t.Fatalf("staging parent entries = %v, want only add-widget (no lock, temp or old folder)", entries)
		}
	})
}

// stageSeamFixture stages add-widget once (so a staging to keep exists),
// then returns the root, the staging dir and its snapshot.
func stageSeamFixture(t *testing.T) (root, stagingDir string, before map[string]string) {
	t.Helper()
	root, _ = setupStageRepo(t)
	if _, err := Stage(root, root, "add-widget", stageFiles(true), "/plans/p.md", fixedNow); err != nil {
		t.Fatalf("first Stage: %v", err)
	}
	stagingDir = filepath.Join(stagingParent(root), "add-widget")
	return root, stagingDir, snapshot(t, stagingDir)
}

// assertOnlyStaging checks that the staging parent holds only the add-widget
// folder plus the names in extra: no lock and no temp or old folder.
func assertOnlyStaging(t *testing.T, root string, extra ...string) {
	t.Helper()
	entries, err := os.ReadDir(stagingParent(root))
	if err != nil {
		t.Fatalf("read staging parent: %v", err)
	}
	want := append([]string{"add-widget"}, extra...)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if len(got) != len(want) {
		t.Fatalf("staging parent entries = %v, want %v", got, want)
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			found = found || g == w
		}
		if !found {
			t.Fatalf("staging parent entries = %v, want %v", got, want)
		}
	}
}

// TestStage_IOFailures forces each IO failure row through its seam and checks
// the result of the row.
func TestStage_IOFailures(t *testing.T) {
	newFiles := []StageFile{
		{Path: "proposal.md", Content: "# New proposal\n"},
		{Path: "specs/user-auth/spec.md", Content: "# Spec\n"},
		{Path: "design.md", Content: "# Design\n"},
		{Path: "tasks.md", Content: "# Tasks\n"},
	}

	t.Run("crash leftovers", func(t *testing.T) {
		root, _ := setupStageRepo(t)
		parent := stagingParent(root)
		for _, d := range []string{".tmp-add-widget-1", ".old-add-widget-2", ".tmp-add-widget-x-3"} {
			mustWrite(t, filepath.Join(parent, d, "proposal.md"), "left\n")
		}
		res, err := Stage(root, root, "add-widget", stageFiles(true), "", fixedNow)
		if err != nil || !res.Valid {
			t.Fatalf("Stage = %+v, %v; want a valid stage", res, err)
		}
		// .tmp-add-widget-x-3 belongs to change add-widget-x and stays.
		assertOnlyStaging(t, root, ".tmp-add-widget-x-3")
	})

	t.Run("leftover delete fails", func(t *testing.T) {
		root, stagingDir, before := stageSeamFixture(t)
		leftover := filepath.Join(stagingParent(root), ".tmp-add-widget-1")
		mustWrite(t, filepath.Join(leftover, "proposal.md"), "left\n")
		orig := stageRemoveAll
		t.Cleanup(func() { stageRemoveAll = orig })
		stageRemoveAll = func(p string) error {
			if p == leftover {
				return os.ErrPermission
			}
			return orig(p)
		}
		_, err := Stage(root, root, "add-widget", newFiles, "/plans/p.md", fixedNow)
		var ioErr *StageIOError
		if !errors.As(err, &ioErr) || ioErr.Op != "remove" || ioErr.Path != leftover {
			t.Fatalf("Stage err = %v, want a remove StageIOError for %s", err, leftover)
		}
		if want := "openspec stage: remove " + leftover + ": "; !strings.HasPrefix(err.Error(), want) {
			t.Fatalf("err = %q, want prefix %q", err, want)
		}
		if after := snapshot(t, stagingDir); !reflect.DeepEqual(before, after) {
			t.Fatalf("staging changed:\nbefore %v\nafter  %v", before, after)
		}
		assertOnlyStaging(t, root, ".tmp-add-widget-1")
	})

	t.Run("temp write fails", func(t *testing.T) {
		root, stagingDir, before := stageSeamFixture(t)
		orig := stageWriteFile
		t.Cleanup(func() { stageWriteFile = orig })
		stageWriteFile = func(p string, data []byte, perm os.FileMode) error {
			if filepath.Base(p) == StageManifestFile {
				return os.ErrPermission
			}
			return orig(p, data, perm)
		}
		_, err := Stage(root, root, "add-widget", newFiles, "/plans/p.md", fixedNow)
		var ioErr *StageIOError
		if !errors.As(err, &ioErr) || ioErr.Op != "write" || filepath.Base(ioErr.Path) != StageManifestFile {
			t.Fatalf("Stage err = %v, want a write StageIOError for stage.json", err)
		}
		if !strings.HasPrefix(err.Error(), "openspec stage: write ") {
			t.Fatalf("err = %q, want prefix %q", err, "openspec stage: write ")
		}
		if after := snapshot(t, stagingDir); !reflect.DeepEqual(before, after) {
			t.Fatalf("staging changed:\nbefore %v\nafter  %v", before, after)
		}
		assertOnlyStaging(t, root)
	})

	t.Run("first rename fails", func(t *testing.T) {
		root, stagingDir, before := stageSeamFixture(t)
		orig := stageRename
		t.Cleanup(func() { stageRename = orig })
		calls := 0
		stageRename = func(from, to string) error {
			calls++
			if calls == 1 {
				return os.ErrPermission
			}
			return orig(from, to)
		}
		_, err := Stage(root, root, "add-widget", newFiles, "/plans/p.md", fixedNow)
		var ioErr *StageIOError
		if !errors.As(err, &ioErr) || ioErr.Op != "rename" || ioErr.Path != stagingDir {
			t.Fatalf("Stage err = %v, want a rename StageIOError for %s", err, stagingDir)
		}
		if after := snapshot(t, stagingDir); !reflect.DeepEqual(before, after) {
			t.Fatalf("staging changed:\nbefore %v\nafter  %v", before, after)
		}
		assertOnlyStaging(t, root)
	})

	t.Run("second rename fails", func(t *testing.T) {
		root, stagingDir, before := stageSeamFixture(t)
		orig := stageRename
		t.Cleanup(func() { stageRename = orig })
		calls := 0
		stageRename = func(from, to string) error {
			calls++
			if calls == 2 {
				return os.ErrPermission
			}
			return orig(from, to)
		}
		_, err := Stage(root, root, "add-widget", newFiles, "/plans/p.md", fixedNow)
		var ioErr *StageIOError
		if !errors.As(err, &ioErr) || ioErr.Op != "rename" || ioErr.To != stagingDir {
			t.Fatalf("Stage err = %v, want a rename StageIOError into %s", err, stagingDir)
		}
		if calls != 3 {
			t.Fatalf("stageRename calls = %d, want 3 (aside, into place, move back)", calls)
		}
		if after := snapshot(t, stagingDir); !reflect.DeepEqual(before, after) {
			t.Fatalf("staging not moved back:\nbefore %v\nafter  %v", before, after)
		}
		assertOnlyStaging(t, root)
	})

	t.Run("old delete fails", func(t *testing.T) {
		root, stagingDir, _ := stageSeamFixture(t)
		orig := stageRemoveAll
		t.Cleanup(func() { stageRemoveAll = orig })
		oldPrefix := filepath.Join(stagingParent(root), ".old-add-widget-")
		stageRemoveAll = func(p string) error {
			if strings.HasPrefix(p, oldPrefix) {
				return os.ErrPermission
			}
			return orig(p)
		}
		res, err := Stage(root, root, "add-widget", newFiles, "/plans/p.md", fixedNow)
		if err != nil || !res.Valid || !res.Replaced {
			t.Fatalf("Stage = %+v, %v; want a valid replacing stage", res, err)
		}
		if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "old staging "+oldPrefix) || !strings.Contains(res.Warnings[0], " not deleted: ") {
			t.Fatalf("Warnings = %q, want one old-staging line", res.Warnings)
		}
		if got := snapshot(t, stagingDir)["proposal.md"]; got != "# New proposal\n" {
			t.Fatalf("staged proposal.md = %q, want the new content", got)
		}
		// The next stage removes the old folder as a crash leftover.
		stageRemoveAll = orig
		if _, err := Stage(root, root, "add-widget", newFiles, "/plans/p.md", fixedNow); err != nil {
			t.Fatalf("next Stage: %v", err)
		}
		assertOnlyStaging(t, root)
	})

	t.Run("lock delete fails", func(t *testing.T) {
		root, _ := setupStageRepo(t)
		orig := stageRemoveLock
		t.Cleanup(func() { stageRemoveLock = orig })
		stageRemoveLock = func(string) error { return os.ErrPermission }
		res, err := Stage(root, root, "add-widget", stageFiles(true), "", fixedNow)
		if err != nil || !res.Valid {
			t.Fatalf("Stage = %+v, %v; want a valid stage", res, err)
		}
		lock := StagingLockPath(root, "add-widget")
		want := "lock " + lock + " not deleted: " + os.ErrPermission.Error()
		if len(res.Warnings) != 1 || res.Warnings[0] != want {
			t.Fatalf("Warnings = %q, want [%q]", res.Warnings, want)
		}
	})
}

func TestReadStageManifest(t *testing.T) {
	// manifestOfSize returns a valid stage.json of exactly n bytes.
	manifestOfSize := func(n int) string {
		base := `{"change":"add-widget","planPath":"/plans/a.md","files":[{"path":"proposal.md"}]}`
		return base[:len(base)-1] + strings.Repeat(" ", n-len(base)) + "}"
	}

	t.Run("manifest at the cap", func(t *testing.T) {
		root := t.TempDir()
		writeStageManifestRaw(t, root, "add-widget", manifestOfSize(stageManifestMaxBytes))
		m, err := ReadStageManifest(filepath.Join(root, filepath.FromSlash(StagingDirRel("add-widget"))))
		if err != nil || m.PlanPath != "/plans/a.md" {
			t.Fatalf("ReadStageManifest = %+v, %v; want planPath /plans/a.md", m, err)
		}
	})

	t.Run("manifest over the cap", func(t *testing.T) {
		root := t.TempDir()
		writeStageManifestRaw(t, root, "add-widget", manifestOfSize(stageManifestMaxBytes+1))
		_, err := ReadStageManifest(filepath.Join(root, filepath.FromSlash(StagingDirRel("add-widget"))))
		if !errors.Is(err, ErrStageManifestTooLarge) {
			t.Fatalf("ReadStageManifest err = %v, want ErrStageManifestTooLarge", err)
		}
		if want := "stage.json is 1048577 bytes, limit 1048576 bytes"; !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want it to contain %q", err, want)
		}
		// An oversized stage.json names no owner for the stage owner check.
		if got := stagedPlanPath(root, "add-widget"); got != "" {
			t.Fatalf("stagedPlanPath = %q, want empty", got)
		}
	})

	t.Run("missing stage.json", func(t *testing.T) {
		if _, err := ReadStageManifest(t.TempDir()); err == nil {
			t.Fatal("ReadStageManifest err = nil, want a read error")
		}
	})
}

func TestMissingFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "proposal.md"), "# P\n")
	mustWrite(t, filepath.Join(dir, "specs", "a", "spec.md"), "# S\n")
	if err := os.MkdirAll(filepath.Join(dir, "design.md"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest := StageManifest{Files: []StagedFile{
		{Path: "proposal.md"}, {Path: "design.md"}, {Path: "specs/a/spec.md"}, {Path: "tasks.md"},
	}}
	if got, want := MissingFiles(manifest, dir), []string{"design.md", "tasks.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("MissingFiles = %q, want %q", got, want)
	}
	if got := MissingFiles(StageManifest{Files: []StagedFile{{Path: "proposal.md"}}}, dir); got != nil {
		t.Fatalf("MissingFiles = %q, want nil", got)
	}
}

func TestSamePlanPath(t *testing.T) {
	dir := t.TempDir()
	plan := filepath.Join(dir, "p.md")
	mustWrite(t, plan, "# Plan\n")
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(plan, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"same path", "/r/p.md", "/r/p.md", true},
		{"dot segment", "/r/x/../p.md", "/r/p.md", true},
		{"link", link, plan, true},
		{"missing file", "/r/gone.md", "/r/gone.md", true},
		{"other file", "/r/a.md", "/r/b.md", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SamePlanPath(tc.a, tc.b); got != tc.want {
				t.Errorf("SamePlanPath(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
