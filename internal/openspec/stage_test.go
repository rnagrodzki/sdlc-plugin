package openspec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

	_, err := Stage(root, "grp/demo", stageFiles(false), "", fixedNow)
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

			_, err := Stage(root, "add-widget", files, "", fixedNow)
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

func TestStage_DuplicatePathRejected(t *testing.T) {
	root, _ := setupStageRepo(t)
	files := append(stageFiles(false), StageFile{Path: "tasks.md", Content: "again"})
	if _, err := Stage(root, "add-widget", files, "", fixedNow); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("Stage err = %v, want ErrPathNotAllowed", err)
	}
}

func TestStage_ValidWritesFilesAndManifest(t *testing.T) {
	root, log := setupStageRepo(t)
	files := stageFiles(true)

	res, err := Stage(root, "add-widget", files, "/plans/p.md", fixedNow)
	if err != nil {
		t.Fatalf("Stage: %v", err)
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
	if _, err := Stage(root, "add-widget", stageFiles(true), "", fixedNow); err != nil {
		t.Fatalf("first Stage: %v", err)
	}
	design := filepath.Join(root, ".sdlc-v2", "openspec-staging", "add-widget", "design.md")
	if _, err := os.Stat(design); err != nil {
		t.Fatalf("design.md not staged: %v", err)
	}

	if _, err := Stage(root, "add-widget", stageFiles(false), "", fixedNow); err != nil {
		t.Fatalf("restage: %v", err)
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

	res, err := Stage(root, "add-widget", stageFiles(false), "", fixedNow)
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

		res, err := Stage(root, "add-widget", stageFiles(false), "", fixedNow)
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

		res, err := Stage(root, "add-widget", stageFiles(false), "", fixedNow)
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

	res, err := Stage(root, "add-widget", stageFiles(false), "", fixedNow)
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
// validatedAt) and no change in the repo. A spec of exactly 1 MiB is copied.
func TestStage_TargetSpecTooLarge(t *testing.T) {
	specPath := func(root string) string {
		return filepath.Join(root, "openspec", "specs", "user-auth", "spec.md")
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
		want  string
	}{
		{"over 1 MiB", func(t *testing.T, root string) {
			mustWrite(t, specPath(root), strings.Repeat("a", maxTargetSpecBytes+1))
		}, "is over 1 MiB"},
		{"read error", func(t *testing.T, root string) {
			// A directory in place of the file: open works, read fails.
			if err := os.MkdirAll(specPath(root), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
		}, "read "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, log := setupStageRepo(t)
			tc.setup(t, root)
			gitCommitAll(t, root)

			_, err := Stage(root, "add-widget", stageFiles(false), "", fixedNow)
			if !errors.Is(err, ErrTargetSpec) {
				t.Fatalf("Stage err = %v, want ErrTargetSpec", err)
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
		t.Skip("openspec not on PATH")
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
			{Path: "tasks.md", Content: "## 1. Work\n- [ ] 1.1 Record the worker name\n"},
		}
	}

	res, err := Stage(root, "add-widget", files(delta(first)), "", fixedNow)
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

	res, err = Stage(root, "add-widget", files(delta(first+second)), "", fixedNow)
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
