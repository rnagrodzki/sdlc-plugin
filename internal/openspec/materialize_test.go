package openspec

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
)

// matPlan returns a plan whose **OpenSpec-Staging:** header names change.
func matPlan(change string) string {
	return "# Plan\n\n**Source:** user request\n**OpenSpec-Staging:** .sdlc-v2/openspec-staging/" + change + "/\n\n## Tasks\n"
}

// writeStaging writes files into the change's staging dir plus a stage.json
// that records each file's correct SHA-256, without calling the CLI.
func writeStaging(t *testing.T, root, change string, files []StageFile) string {
	t.Helper()
	dir := filepath.Join(root, ".sdlc-v2", "openspec-staging", change)
	m := StageManifest{Change: change, Schema: "spec-driven", ValidatedAt: "2026-10-01T12:00:00Z"}
	for _, f := range files {
		mustWrite(t, filepath.Join(dir, filepath.FromSlash(f.Path)), f.Content)
		m.Files = append(m.Files, StagedFile{Path: f.Path, SHA256: sha(f.Content)})
	}
	if err := fsx.AtomicWriteJSON(filepath.Join(dir, StageManifestFile), m); err != nil {
		t.Fatalf("write stage.json: %v", err)
	}
	return dir
}

// writeTarget writes files into openspec/changes/<change>/.
func writeTarget(t *testing.T, root, change string, files []StageFile) string {
	t.Helper()
	dir := filepath.Join(root, "openspec", "changes", change)
	mustWrite(t, filepath.Join(dir, ".openspec.yaml"), "schema: spec-driven\n")
	for _, f := range files {
		mustWrite(t, filepath.Join(dir, filepath.FromSlash(f.Path)), f.Content)
	}
	return dir
}

// snapshot returns every regular file under dir keyed by its slash path
// relative to dir. A missing dir returns nil.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

func stubCalls(t *testing.T, log string) int {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read stub log: %v", err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}

func assertMissing(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("%s exists (stat err %v), want missing", p, err)
	}
}

func TestStagedChangeFromPlan(t *testing.T) {
	cases := []struct {
		name   string
		plan   string
		change string
		ok     bool
	}{
		{"header with slash", matPlan("add-widget"), "add-widget", true},
		{"header without slash", "**OpenSpec-Staging:** .sdlc-v2/openspec-staging/add-widget\n", "add-widget", true},
		{"bad name still captured", matPlan("Bad_Name"), "Bad_Name", true},
		{"no header", "# Plan\n\n**Source:** x\n", "", false},
		{"other dir", "**OpenSpec-Staging:** tmp/add-widget/\n", "", false},
		{"not at line start", "see **OpenSpec-Staging:** .sdlc-v2/openspec-staging/add-widget/\n", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			change, ok := StagedChangeFromPlan(tc.plan)
			if change != tc.change || ok != tc.ok {
				t.Fatalf("StagedChangeFromPlan = (%q, %v), want (%q, %v)", change, ok, tc.change, tc.ok)
			}
		})
	}
}

// Rule 1.
func TestMaterialize_NoHeader(t *testing.T) {
	root, log := setupStageRepo(t)
	res, err := Materialize(root, "# Plan\n\nno staging header\n")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if res != (MaterializeResult{}) {
		t.Fatalf("result = %+v, want zero", res)
	}
	if n := stubCalls(t, log); n != 0 {
		t.Fatalf("stub calls = %d, want 0", n)
	}
	assertGitClean(t, root)
}

// Rule 2.
func TestMaterialize_InvalidChangeName(t *testing.T) {
	root, log := setupStageRepo(t)
	_, err := Materialize(root, matPlan("Bad_Name"))
	if !errors.Is(err, ErrInvalidChangeName) || !errors.Is(err, ErrMaterialize) {
		t.Fatalf("err = %v, want ErrInvalidChangeName and ErrMaterialize", err)
	}
	if n := stubCalls(t, log); n != 0 {
		t.Fatalf("stub calls = %d, want 0", n)
	}
	assertMissing(t, filepath.Join(root, "openspec", "changes"))
	assertGitClean(t, root)
}

// Rule 3.
func TestMaterialize_AlreadyNoStaging(t *testing.T) {
	root, log := setupStageRepo(t)
	target := writeTarget(t, root, "add-widget", stageFiles(false))
	before := snapshot(t, target)

	res, err := Materialize(root, matPlan("add-widget"))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if want := (MaterializeResult{Change: "add-widget", Materialized: "already"}); res != want {
		t.Fatalf("result = %+v, want %+v", res, want)
	}
	if after := snapshot(t, target); !reflect.DeepEqual(before, after) {
		t.Fatalf("target changed:\nbefore %v\nafter  %v", before, after)
	}
	if n := stubCalls(t, log); n != 0 {
		t.Fatalf("stub calls = %d, want 0", n)
	}
}

// Rule 4.
func TestMaterialize_AlreadySameSHA(t *testing.T) {
	root, log := setupStageRepo(t)
	target := writeTarget(t, root, "add-widget", stageFiles(true))
	staging := writeStaging(t, root, "add-widget", stageFiles(true))
	before := snapshot(t, target)

	res, err := Materialize(root, matPlan("add-widget"))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if want := (MaterializeResult{Change: "add-widget", Materialized: "already"}); res != want {
		t.Fatalf("result = %+v, want %+v", res, want)
	}
	assertMissing(t, staging)
	if after := snapshot(t, target); !reflect.DeepEqual(before, after) {
		t.Fatalf("target changed:\nbefore %v\nafter  %v", before, after)
	}
	if n := stubCalls(t, log); n != 0 {
		t.Fatalf("stub calls = %d, want 0", n)
	}
}

// Rule 5.
func TestMaterialize_TargetDiffers(t *testing.T) {
	cases := map[string]func(t *testing.T, target string){
		"content differs": func(t *testing.T, target string) {
			mustWrite(t, filepath.Join(target, "tasks.md"), "# Tasks (edited)\n")
		},
		"file missing in target": func(t *testing.T, target string) {
			if err := os.Remove(filepath.Join(target, "tasks.md")); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			root, log := setupStageRepo(t)
			target := writeTarget(t, root, "add-widget", stageFiles(false))
			mutate(t, target)
			staging := writeStaging(t, root, "add-widget", stageFiles(false))
			targetBefore, stagingBefore := snapshot(t, target), snapshot(t, staging)

			_, err := Materialize(root, matPlan("add-widget"))
			if !errors.Is(err, ErrMaterialize) || !strings.Contains(err.Error(), "openspec change add-widget already exists and differs from staging") {
				t.Fatalf("err = %v, want differs-from-staging ErrMaterialize", err)
			}
			if after := snapshot(t, target); !reflect.DeepEqual(targetBefore, after) {
				t.Fatalf("target changed: %v", after)
			}
			if after := snapshot(t, staging); !reflect.DeepEqual(stagingBefore, after) {
				t.Fatalf("staging changed: %v", after)
			}
			if n := stubCalls(t, log); n != 0 {
				t.Fatalf("stub calls = %d, want 0", n)
			}
		})
	}
}

// Rule 6.
func TestMaterialize_BothMissing(t *testing.T) {
	root, log := setupStageRepo(t)
	_, err := Materialize(root, matPlan("add-widget"))
	if !errors.Is(err, ErrMaterialize) {
		t.Fatalf("err = %v, want ErrMaterialize", err)
	}
	want := filepath.Join(root, ".sdlc-v2", "openspec-staging", "add-widget") + string(filepath.Separator)
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %q, want it to name %q", err, want)
	}
	if n := stubCalls(t, log); n != 0 {
		t.Fatalf("stub calls = %d, want 0", n)
	}
	assertMissing(t, filepath.Join(root, "openspec", "changes"))
}

// Rule 7.
func TestMaterialize_StagedFileHashMismatch(t *testing.T) {
	root, log := setupStageRepo(t)
	staging := writeStaging(t, root, "add-widget", stageFiles(false))
	mustWrite(t, filepath.Join(staging, "specs", "user-auth", "spec.md"), "# Spec (edited after validation)\n")

	_, err := Materialize(root, matPlan("add-widget"))
	if !errors.Is(err, ErrMaterialize) || !strings.Contains(err.Error(), "staged file specs/user-auth/spec.md changed after validation") {
		t.Fatalf("err = %v, want changed-after-validation ErrMaterialize", err)
	}
	assertMissing(t, filepath.Join(root, "openspec", "changes", "add-widget"))
	if n := stubCalls(t, log); n != 0 {
		t.Fatalf("stub calls = %d, want 0", n)
	}
	assertGitClean(t, root)
}

func TestMaterialize_RejectsUnsafeManifestPath(t *testing.T) {
	for _, bad := range []string{".openspec.yaml", "../escape.md", "/abs.md"} {
		t.Run(bad, func(t *testing.T) {
			root, log := setupStageRepo(t)
			staging := writeStaging(t, root, "add-widget", stageFiles(false))
			m := readManifest(t, root, "add-widget")
			m.Files = append(m.Files, StagedFile{Path: bad, SHA256: sha("x")})
			if err := fsx.AtomicWriteJSON(filepath.Join(staging, StageManifestFile), m); err != nil {
				t.Fatal(err)
			}

			_, err := Materialize(root, matPlan("add-widget"))
			if !errors.Is(err, ErrMaterialize) || !errors.Is(err, ErrPathNotAllowed) {
				t.Fatalf("err = %v, want ErrMaterialize and ErrPathNotAllowed", err)
			}
			assertMissing(t, filepath.Join(root, "openspec", "changes", "add-widget"))
			if n := stubCalls(t, log); n != 0 {
				t.Fatalf("stub calls = %d, want 0", n)
			}
		})
	}
}

// Rule 8, success; then a second start is a no-op (rule 3).
func TestMaterialize_Created(t *testing.T) {
	root, log := setupStageRepo(t)
	files := stageFiles(true)
	staging := writeStaging(t, root, "add-widget", files)

	res, err := Materialize(root, matPlan("add-widget"))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if want := (MaterializeResult{Change: "add-widget", Materialized: "created"}); res != want {
		t.Fatalf("result = %+v, want %+v", res, want)
	}
	if n := stubCalls(t, log); n != 2 {
		t.Fatalf("stub calls = %d, want 2 (new change, validate)", n)
	}

	target := filepath.Join(root, "openspec", "changes", "add-widget")
	got := snapshot(t, target)
	for _, f := range files {
		if got[f.Path] != f.Content {
			t.Fatalf("target %s = %q, want %q", f.Path, got[f.Path], f.Content)
		}
	}
	// The stub's `new change` writes an empty .openspec.yaml; it must survive.
	if meta, ok := got[".openspec.yaml"]; !ok || meta != "" {
		t.Fatalf(".openspec.yaml = (%q, present %v), want the stub's empty file", meta, ok)
	}
	assertMissing(t, staging)

	cached := strings.Fields(git(t, root, "diff", "--cached", "--name-only"))
	for _, f := range append([]StageFile{{Path: ".openspec.yaml"}}, files...) {
		want := "openspec/changes/add-widget/" + f.Path
		found := false
		for _, c := range cached {
			if c == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("git diff --cached --name-only = %v, missing %s", cached, want)
		}
	}

	before := snapshot(t, target)
	res, err = Materialize(root, matPlan("add-widget"))
	if err != nil {
		t.Fatalf("second Materialize: %v", err)
	}
	if res.Materialized != "already" {
		t.Fatalf("second result = %+v, want already", res)
	}
	if after := snapshot(t, target); !reflect.DeepEqual(before, after) {
		t.Fatalf("second start changed the target: %v", after)
	}
	if n := stubCalls(t, log); n != 2 {
		t.Fatalf("stub calls after second start = %d, want 2", n)
	}
}

// Rule 8, the CLI is missing from PATH: `new change` fails with
// ErrCLINotFound, nothing is created, staging is kept.
func TestMaterialize_CLINotFoundAtNewChange(t *testing.T) {
	root, _ := setupStageRepo(t)
	staging := writeStaging(t, root, "add-widget", stageFiles(true))
	stagingBefore := snapshot(t, staging)
	withStubPath(t) // PATH now holds only an empty dir: no openspec binary

	_, err := Materialize(root, matPlan("add-widget"))
	if !errors.Is(err, ErrCLINotFound) {
		t.Fatalf("err = %v, want ErrCLINotFound", err)
	}
	assertMissing(t, filepath.Join(root, "openspec", "changes", "add-widget"))
	if after := snapshot(t, staging); !reflect.DeepEqual(stagingBefore, after) {
		t.Fatalf("staging changed:\nbefore %v\nafter  %v", stagingBefore, after)
	}
}

// Rule 8, the CLI disappears between `new change` and `validate --strict`:
// the validate step returns ErrCLINotFound and the half-made target is
// rolled back. The stub deletes itself after `new change` to get there.
func TestMaterialize_CLINotFoundAtValidate(t *testing.T) {
	root, _ := setupStageRepo(t)
	staging := writeStaging(t, root, "add-widget", stageFiles(true))
	stagingBefore := snapshot(t, staging)
	stub := withStubPath(t)
	// PATH holds only the stub dir, so mkdir/rm are called by absolute path.
	script := `#!/bin/sh
if [ "$1" = new ]; then
  /bin/mkdir -p "openspec/changes/$3" && : > "openspec/changes/$3/.openspec.yaml"
  /bin/rm -f "$0"
  exit 0
fi
exit 1
`
	if err := os.WriteFile(filepath.Join(stub, "openspec"), []byte(script), 0o755); err != nil {
		t.Fatalf("write openspec stub: %v", err)
	}

	_, err := Materialize(root, matPlan("add-widget"))
	if !errors.Is(err, ErrCLINotFound) {
		t.Fatalf("err = %v, want ErrCLINotFound", err)
	}
	if _, statErr := os.Stat(filepath.Join(stub, "openspec")); !os.IsNotExist(statErr) {
		t.Fatalf("stub still present (stat err %v): `new change` did not run, so validate was not reached", statErr)
	}
	assertMissing(t, filepath.Join(root, "openspec", "changes", "add-widget"))
	if after := snapshot(t, staging); !reflect.DeepEqual(stagingBefore, after) {
		t.Fatalf("staging changed:\nbefore %v\nafter  %v", stagingBefore, after)
	}
}

// Rule 8, validation fails: target rolled back, staging kept.
func TestMaterialize_ValidateFailsRollsBack(t *testing.T) {
	root, log := setupStageRepo(t)
	t.Setenv("STUB_VALIDATE_EXIT", "1")
	staging := writeStaging(t, root, "add-widget", stageFiles(false))
	stagingBefore := snapshot(t, staging)

	_, err := Materialize(root, matPlan("add-widget"))
	if !errors.Is(err, ErrMaterialize) || !strings.Contains(err.Error(), "proposal.md missing Why section") {
		t.Fatalf("err = %v, want ErrMaterialize carrying the CLI output", err)
	}
	assertMissing(t, filepath.Join(root, "openspec", "changes", "add-widget"))
	if after := snapshot(t, staging); !reflect.DeepEqual(stagingBefore, after) {
		t.Fatalf("staging changed:\nbefore %v\nafter  %v", stagingBefore, after)
	}
	if n := stubCalls(t, log); n != 2 {
		t.Fatalf("stub calls = %d, want 2 (new change, validate)", n)
	}
	if out := strings.TrimSpace(git(t, root, "diff", "--cached", "--name-only")); out != "" {
		t.Fatalf("index not empty after rollback: %s", out)
	}
	assertGitClean(t, root)
}
