package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/openspec"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ---------------------------------------------------------------------------
// openspec-check helpers
// ---------------------------------------------------------------------------

// osCheckFixture builds a git repository on a branch with one commit and a
// ship state whose step is in_progress. flags is stored as the flags object
// of the state. It returns the repository directory, the state file path and
// the branch.
func osCheckFixture(t *testing.T, step string, flags map[string]any) (dir, path, branch string) {
	t.Helper()
	dir = t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	branch = "feat/os-check"
	checkoutBranch(t, dir, branch)
	path = shipStateInitFixture(t, dir, branch)
	setSteps(t, path, "verify-openspec", "archive-openspec")
	setStepStatus(t, path, step, "in_progress", map[string]any{"startedAt": "2026-01-01T00:00:00Z"})
	osSetStateKeys(t, path, map[string]any{"flags": flags})
	return dir, path, branch
}

// osSetStateKeys sets root keys of the state file at path.
func osSetStateKeys(t *testing.T, path string, keys map[string]any) {
	t.Helper()
	data := readStateData(t, path)
	for k, v := range keys {
		data[k] = v
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// osStepEntry returns the steps[] entry named step in the state file.
func osStepEntry(t *testing.T, path, step string) map[string]any {
	t.Helper()
	for _, s := range readStateData(t, path)["steps"].([]any) {
		if sm, ok := s.(map[string]any); ok && sm["name"] == step {
			return sm
		}
	}
	t.Fatalf("no step %s in %s", step, path)
	return nil
}

// osCheck calls openspec-check through the dispatcher.
func osCheck(t *testing.T, dir, branch, step string, detail map[string]any) (shipOpenspecCheckOut, error) {
	t.Helper()
	d := map[string]any{"branch": branch}
	for k, v := range detail {
		d[k] = v
	}
	out, err := shipState(dir, dir, ShipStateIn{Action: "openspec-check", Step: step, Detail: d},
		fixedNow(time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)))
	if err != nil {
		return shipOpenspecCheckOut{}, err
	}
	res, ok := out.(shipOpenspecCheckOut)
	if !ok {
		t.Fatalf("openspec-check output = %T, want shipOpenspecCheckOut", out)
	}
	return res, nil
}

// osWriteChange writes proposal.md and one specs/<cap>/spec.md per cap into
// openspec/changes/<change>/ of dir.
func osWriteChange(t *testing.T, dir, change string, proposal bool, caps ...string) {
	t.Helper()
	base := filepath.Join(dir, "openspec", "changes", change)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if proposal {
		writeFile(t, filepath.Join(base, "proposal.md"), "# P\n")
	}
	for _, c := range caps {
		writeFile(t, filepath.Join(base, "specs", c, "spec.md"), "# S\n")
	}
}

func osStubValidate(t *testing.T, change, stdout string, exit int) {
	t.Helper()
	stubOpenspecCLI(t, map[string]openspecCLIStub{
		"validate " + change + " --strict": {stdout: stdout, exit: exit},
	})
}

func osNamed(change string) map[string]any { return map[string]any{"openspecChange": change} }

// Full texts of the overlap warnings.
const (
	osAbsentBaseWarning    = "overlap check skipped: openspecBaseHead is absent from the ship state"
	osMalformedBaseWarning = "overlap check skipped: openspecBaseHead is not a full commit sha"
)

// osAssertOpen checks that the step is still in_progress with no error.
func osAssertOpen(t *testing.T, path, step string) {
	t.Helper()
	e := osStepEntry(t, path, step)
	if e["status"] != "in_progress" {
		t.Errorf("step %s status = %v, want in_progress", step, e["status"])
	}
	if _, has := e["error"]; has {
		t.Errorf("step %s has error %v, want none", step, e["error"])
	}
}

// osAssertFailed checks the fail row of the step.
func osAssertFailed(t *testing.T, path, step, msg string) {
	t.Helper()
	e := osStepEntry(t, path, step)
	if e["status"] != "failed" || e["error"] != msg {
		t.Errorf("step %s = status %v error %v, want failed with %q", step, e["status"], e["error"], msg)
	}
}

// ---------------------------------------------------------------------------
// Outcome table
// ---------------------------------------------------------------------------

func TestShipStateOpenspecCheck_OutcomeOK(t *testing.T) {
	for _, step := range []string{"verify-openspec", "archive-openspec"} {
		t.Run(step, func(t *testing.T) {
			dir, path, branch := osCheckFixture(t, step, osNamed("add-widget"))
			osWriteChange(t, dir, "add-widget", true, "widget")
			osStubValidate(t, "add-widget", "Change 'add-widget' is valid", 0)

			out, err := osCheck(t, dir, branch, step, nil)
			if err != nil {
				t.Fatalf("openspec-check: %v", err)
			}
			wantNext := "Call complete-step for verify-openspec."
			if step == "archive-openspec" {
				wantNext = "Call openspec-tasks for archive-openspec."
			}
			if out.Change != "add-widget" || out.Outcome != "ok" || out.Next != wantNext {
				t.Errorf("out = %+v, want ok with next %q", out, wantNext)
			}
			if out.ValidateOutput != "Change 'add-widget' is valid" {
				t.Errorf("validateOutput = %q", out.ValidateOutput)
			}
			if out.Missing == nil || out.Overlaps == nil || out.Warnings == nil {
				t.Errorf("missing/overlaps/warnings = %v/%v/%v, want non-nil empty slices", out.Missing, out.Overlaps, out.Warnings)
			}
			osAssertOpen(t, path, step)
		})
	}
}

func TestShipStateOpenspecCheck_OutcomeArchived(t *testing.T) {
	for _, step := range []string{"verify-openspec", "archive-openspec"} {
		t.Run(step, func(t *testing.T) {
			dir, path, branch := osCheckFixture(t, step, osNamed("add-widget"))
			writeFile(t, filepath.Join(dir, "openspec", "changes", "archive", "2026-10-30-add-widget", "proposal.md"), "# P\n")
			stubOpenspecCLI(t, nil) // validate must not run: any call answers exit 1

			out, err := osCheck(t, dir, branch, step, nil)
			if err != nil {
				t.Fatalf("openspec-check: %v", err)
			}
			wantNext := "Call complete-step for verify-openspec."
			if step == "archive-openspec" {
				wantNext = "Call complete-step for archive-openspec with result already archived."
			}
			if out.Outcome != "archived" || out.Next != wantNext || out.ValidateOutput != "" {
				t.Errorf("out = %+v, want archived with next %q and no validate output", out, wantNext)
			}
			osAssertOpen(t, path, step)
		})
	}
}

// TestShipStateOpenspecCheck_ArchiveNeedsExactName checks that the date
// regex stops change "fix" from matching the archived change "add-fix".
func TestShipStateOpenspecCheck_ArchiveNeedsExactName(t *testing.T) {
	dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("fix"))
	writeFile(t, filepath.Join(dir, "openspec", "changes", "archive", "2026-01-01-add-fix", "proposal.md"), "# P\n")

	out, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	if out.Outcome != "missing-on-disk" {
		t.Errorf("outcome = %q, want missing-on-disk", out.Outcome)
	}
	osAssertFailed(t, path, "verify-openspec", out.Next)
}

func TestShipStateOpenspecCheck_OutcomeMissingOnDisk(t *testing.T) {
	dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	osSetStateKeys(t, path, map[string]any{"linkedPlan": map[string]any{"planFile": "/p/plan.md", "startedAt": "2026-01-01T00:00:00Z"}})

	out, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	want := `OpenSpec change "add-widget" named by the plan is missing on disk. ` +
		`If the plan has an **OpenSpec-Saved:** line, merge the OpenSpec PR (branch openspec/add-widget) first. ` +
		`Then update this branch from the default branch and run /sdlc:ship --plan /p/plan.md again. ` +
		`Otherwise, run /sdlc:ship --plan /p/plan.md again so ship_prepare materializes it, or remove the **Source:** line from the plan to ship without OpenSpec.`
	if out.Outcome != "missing-on-disk" || out.Next != want {
		t.Errorf("out = %+v, want missing-on-disk with next\n%s", out, want)
	}
	osAssertFailed(t, path, "verify-openspec", want)
	data := readStateData(t, path)
	if data["lastFailedStep"] != "verify-openspec" {
		t.Errorf("lastFailedStep = %v, want verify-openspec", data["lastFailedStep"])
	}
}

// TestShipStateOpenspecCheck_MissingOnDiskWithoutPlan pins the full text of
// the missing-on-disk message when the state has no linked plan: the
// <plan-file> placeholder stays.
func TestShipStateOpenspecCheck_MissingOnDiskWithoutPlan(t *testing.T) {
	dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))

	out, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	want := `OpenSpec change "add-widget" named by the plan is missing on disk. ` +
		`If the plan has an **OpenSpec-Saved:** line, merge the OpenSpec PR (branch openspec/add-widget) first. ` +
		`Then update this branch from the default branch and run /sdlc:ship --plan <plan-file> again. ` +
		`Otherwise, run /sdlc:ship --plan <plan-file> again so ship_prepare materializes it, or remove the **Source:** line from the plan to ship without OpenSpec.`
	if out.Outcome != "missing-on-disk" || out.Next != want {
		t.Errorf("out = %+v, want missing-on-disk with next\n%s", out, want)
	}
	osAssertFailed(t, path, "verify-openspec", want)
}

// TestShipStateOpenspecCheck_SpecsIsFile checks that a specs path that is a
// file reads as no specs (missing-artifacts), not as a read error.
func TestShipStateOpenspecCheck_SpecsIsFile(t *testing.T) {
	dir, _, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	osWriteChange(t, dir, "add-widget", true)
	writeFile(t, filepath.Join(dir, "openspec", "changes", "add-widget", "specs"), "not a folder")

	out, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	if out.Outcome != "missing-artifacts" || !reflect.DeepEqual(out.Missing, []string{"specs/*/spec.md"}) {
		t.Errorf("out = %+v, want missing-artifacts with specs/*/spec.md", out)
	}
}

// TestShipStateOpenspecCheck_ArchiveEntryIsFile checks that a file with the
// name of an archive folder is not an archived change.
func TestShipStateOpenspecCheck_ArchiveEntryIsFile(t *testing.T) {
	dir, _, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	writeFile(t, filepath.Join(dir, "openspec", "changes", "archive", "2026-10-30-add-widget"), "a file")

	out, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	if out.Outcome != "missing-on-disk" {
		t.Errorf("outcome = %q, want missing-on-disk", out.Outcome)
	}
}

// TestShipStateOpenspecCheck_ActiveAndArchived checks that an active folder
// wins over an archive folder of the same change: the change is checked,
// not reported as archived.
func TestShipStateOpenspecCheck_ActiveAndArchived(t *testing.T) {
	dir, _, branch := osCheckFixture(t, "archive-openspec", osNamed("add-widget"))
	osWriteChange(t, dir, "add-widget", true, "widget")
	writeFile(t, filepath.Join(dir, "openspec", "changes", "archive", "2026-10-30-add-widget", "proposal.md"), "# P\n")
	osStubValidate(t, "add-widget", "valid", 0)

	out, err := osCheck(t, dir, branch, "archive-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	if out.Outcome != "ok" || out.Next != "Call openspec-tasks for archive-openspec." {
		t.Errorf("out = %+v, want ok", out)
	}
}

func TestShipStateOpenspecCheck_OutcomeMissingArtifacts(t *testing.T) {
	cases := []struct {
		name     string
		proposal bool
		caps     []string
		missing  []string
	}{
		{"no proposal", false, []string{"widget"}, []string{"proposal.md"}},
		{"no specs", true, nil, []string{"specs/*/spec.md"}},
		{"neither", false, nil, []string{"proposal.md", "specs/*/spec.md"}},
	}
	for _, tc := range cases {
		for _, withPlan := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s plan=%v", tc.name, withPlan), func(t *testing.T) {
				dir, path, branch := osCheckFixture(t, "archive-openspec", osNamed("add-widget"))
				osWriteChange(t, dir, "add-widget", tc.proposal, tc.caps...)
				wantNext := "Author the missing artifacts in openspec/changes/add-widget/, then run /sdlc:ship --resume."
				if withPlan {
					osSetStateKeys(t, path, map[string]any{"linkedPlan": map[string]any{"planFile": "docs/p.md", "startedAt": "2026-01-01T00:00:00Z"}})
					wantNext = "Run /sdlc:plan on docs/p.md to author the missing artifacts, then run /sdlc:ship --resume."
				}
				stubOpenspecCLI(t, nil) // validate must not run

				out, err := osCheck(t, dir, branch, "archive-openspec", nil)
				if err != nil {
					t.Fatalf("openspec-check: %v", err)
				}
				if out.Outcome != "missing-artifacts" || !reflect.DeepEqual(out.Missing, tc.missing) || out.Next != wantNext {
					t.Errorf("out = %+v, want missing %v and next %q", out, tc.missing, wantNext)
				}
				osAssertFailed(t, path, "archive-openspec", `OpenSpec change "add-widget" is missing `+strings.Join(tc.missing, ", "))
			})
		}
	}
}

// TestShipStateOpenspecCheck_SpecFolderWithoutSpecFile checks that a specs
// folder with no spec.md inside does not count as a spec.
func TestShipStateOpenspecCheck_SpecFolderWithoutSpecFile(t *testing.T) {
	dir, _, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	osWriteChange(t, dir, "add-widget", true)
	if err := os.MkdirAll(filepath.Join(dir, "openspec", "changes", "add-widget", "specs", "widget"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	if out.Outcome != "missing-artifacts" || !reflect.DeepEqual(out.Missing, []string{"specs/*/spec.md"}) {
		t.Errorf("out = %+v, want missing-artifacts with specs/*/spec.md", out)
	}
}

func TestShipStateOpenspecCheck_OutcomeInvalid(t *testing.T) {
	dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	osWriteChange(t, dir, "add-widget", true, "widget")
	osStubValidate(t, "add-widget", "proposal.md: missing Why section", 1)

	out, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	if out.Outcome != "invalid" || out.ValidateOutput != "proposal.md: missing Why section" {
		t.Errorf("out = %+v, want invalid with the validate output", out)
	}
	if out.Next != "Fix the errors in validateOutput, then run /sdlc:ship --resume." {
		t.Errorf("next = %q", out.Next)
	}
	if len(out.Overlaps) != 0 {
		t.Errorf("overlaps = %v, want none", out.Overlaps)
	}
	// No openspecBaseHead in this state: one warning, no overlap check.
	if !reflect.DeepEqual(out.Warnings, []string{osAbsentBaseWarning}) {
		t.Errorf("warnings = %q, want [%q]", out.Warnings, osAbsentBaseWarning)
	}
	osAssertFailed(t, path, "verify-openspec", "openspec validate add-widget --strict failed")
}

// TestShipStateOpenspecCheck_AbsentBaseForms checks that a key that is
// absent, a JSON null and an empty string each give the absent-base warning
// and never reach git.
func TestShipStateOpenspecCheck_AbsentBaseForms(t *testing.T) {
	cases := map[string]func(t *testing.T, path string){
		"key absent":   func(*testing.T, string) {},
		"null":         func(t *testing.T, path string) { osSetStateKeys(t, path, map[string]any{"openspecBaseHead": nil}) },
		"empty string": func(t *testing.T, path string) { osSetStateKeys(t, path, map[string]any{"openspecBaseHead": ""}) },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
			osWriteChange(t, dir, "add-widget", true, "widget")
			setup(t, path)
			osStubValidate(t, "add-widget", "bad", 1)
			osNoGit(t)

			out, err := osCheck(t, dir, branch, "verify-openspec", nil)
			if err != nil {
				t.Fatalf("openspec-check: %v", err)
			}
			if !reflect.DeepEqual(out.Warnings, []string{osAbsentBaseWarning}) {
				t.Errorf("warnings = %q, want [%q]", out.Warnings, osAbsentBaseWarning)
			}
		})
	}
}

// osNoGit makes any git call of the overlap check fail the test.
func osNoGit(t *testing.T) {
	t.Helper()
	prev := shipCommitCheckGit
	shipCommitCheckGit = func(d string, args ...string) (string, error) {
		t.Errorf("git called with %v, want no git call", args)
		return "", nil
	}
	t.Cleanup(func() { shipCommitCheckGit = prev })
}

// ---------------------------------------------------------------------------
// Overlaps
// ---------------------------------------------------------------------------

// osGitHead returns HEAD of dir.
func osGitHead(t *testing.T, dir string) string { return gitOut(t, dir, "rev-parse", "HEAD") }

func TestShipStateOpenspecCheck_InvalidWithOverlaps(t *testing.T) {
	dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	osWriteChange(t, dir, "add-widget", true, "widget", "gadget")
	base := osGitHead(t, dir)
	osSetStateKeys(t, path, map[string]any{"openspecBaseHead": base})

	// An archive folder added after the base: one shared capability, one not.
	writeFile(t, filepath.Join(dir, "openspec", "changes", "archive", "2026-11-01-other-change", "specs", "widget", "spec.md"), "# S\n")
	writeFile(t, filepath.Join(dir, "openspec", "changes", "archive", "2026-11-01-other-change", "specs", "unrelated", "spec.md"), "# S\n")
	gitCommit(t, dir, "archive other")
	osStubValidate(t, "add-widget", "delta does not apply", 1)

	out, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	wantOverlaps := []shipSpecOverlap{{Spec: "openspec/specs/widget/spec.md", Change: "2026-11-01-other-change"}}
	if out.Outcome != "invalid" || !reflect.DeepEqual(out.Overlaps, wantOverlaps) {
		t.Fatalf("out = %+v, want invalid with overlaps %v", out, wantOverlaps)
	}
	wantNext := "Rebase the deltas of add-widget on the new text of openspec/specs/widget/spec.md, then run openspec validate add-widget --strict."
	if out.Next != wantNext {
		t.Errorf("next = %q, want %q", out.Next, wantNext)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", out.Warnings)
	}
}

// TestShipStateOpenspecCheck_OverlapsSortedAndDeduped feeds the git seam
// two archive folders for one spec, two files of one archive folder for one
// spec, and a second spec. Each pair of spec and folder is listed once,
// sorted by spec and then by folder, and next names each spec once.
func TestShipStateOpenspecCheck_OverlapsSortedAndDeduped(t *testing.T) {
	dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	osWriteChange(t, dir, "add-widget", true, "widget", "gadget")
	osSetStateKeys(t, path, map[string]any{"openspecBaseHead": strings.Repeat("c", 40)})
	osStubValidate(t, "add-widget", "delta does not apply", 1)
	diff := strings.Join([]string{
		"openspec/changes/archive/2026-11-03-b/specs/widget/spec.md",
		"openspec/changes/archive/2026-11-03-b/specs/widget/extra.md",
		"openspec/changes/archive/2026-11-01-a/specs/widget/spec.md",
		"openspec/changes/archive/2026-11-02-c/specs/gadget/spec.md",
	}, "\n")
	prev := shipCommitCheckGit
	shipCommitCheckGit = func(d string, args ...string) (string, error) { return diff, nil }
	t.Cleanup(func() { shipCommitCheckGit = prev })

	out, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	wantOverlaps := []shipSpecOverlap{
		{Spec: "openspec/specs/gadget/spec.md", Change: "2026-11-02-c"},
		{Spec: "openspec/specs/widget/spec.md", Change: "2026-11-01-a"},
		{Spec: "openspec/specs/widget/spec.md", Change: "2026-11-03-b"},
	}
	if !reflect.DeepEqual(out.Overlaps, wantOverlaps) {
		t.Errorf("overlaps = %+v, want %+v", out.Overlaps, wantOverlaps)
	}
	wantNext := "Rebase the deltas of add-widget on the new text of openspec/specs/gadget/spec.md, openspec/specs/widget/spec.md, then run openspec validate add-widget --strict."
	if out.Next != wantNext {
		t.Errorf("next = %q, want %q", out.Next, wantNext)
	}
}

// TestShipStateOpenspecCheck_OverlapDecoys adds, after the base commit, one
// path for each filter of the overlap check. Each one differs from a true
// overlap in one property only, so none of them is an overlap.
func TestShipStateOpenspecCheck_OverlapDecoys(t *testing.T) {
	dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	osWriteChange(t, dir, "add-widget", true, "widget")
	oldSpec := filepath.Join(dir, "openspec", "changes", "archive", "2026-10-01-old", "specs", "widget", "spec.md")
	writeFile(t, oldSpec, "# S\n")
	gitCommit(t, dir, "archive old")
	osSetStateKeys(t, path, map[string]any{"openspecBaseHead": osGitHead(t, dir)})

	archive := filepath.Join(dir, "openspec", "changes", "archive", "2026-11-02-x")
	writeFile(t, oldSpec, "# S changed\n")                                                                  // modified, not added
	writeFile(t, filepath.Join(archive, "proposal.md"), "# P\n")                                            // archive file outside specs/
	writeFile(t, filepath.Join(archive, "notes", "widget", "spec.md"), "# S\n")                             // not under specs/
	writeFile(t, filepath.Join(dir, "openspec", "specs", "widget", "spec.md"), "# S\n")                     // main spec, not archive
	writeFile(t, filepath.Join(dir, "openspec", "changes", "other", "specs", "widget", "spec.md"), "# S\n") // active change
	gitCommit(t, dir, "decoys")
	osStubValidate(t, "add-widget", "bad", 1)

	out, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	if len(out.Overlaps) != 0 || len(out.Warnings) != 0 {
		t.Errorf("overlaps = %+v, warnings = %q, want none", out.Overlaps, out.Warnings)
	}
	if out.Next != "Fix the errors in validateOutput, then run /sdlc:ship --resume." {
		t.Errorf("next = %q", out.Next)
	}
}

// TestShipStateOpenspecCheck_OverlapsOnlyForInvalid checks that an ok
// result runs no overlap check: overlaps stay empty even with a base sha.
func TestShipStateOpenspecCheck_OverlapsOnlyForInvalid(t *testing.T) {
	dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	osWriteChange(t, dir, "add-widget", true, "widget")
	osSetStateKeys(t, path, map[string]any{"openspecBaseHead": strings.Repeat("d", 40)})
	osStubValidate(t, "add-widget", "valid", 0)
	osNoGit(t)

	out, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	if out.Outcome != "ok" || len(out.Overlaps) != 0 || len(out.Warnings) != 0 {
		t.Errorf("out = %+v, want ok with no overlaps and no warnings", out)
	}
}

// TestShipStateOpenspecCheck_OwnSpecsUnreadable checks that a failed read of
// the specs of the change gives a warning, not a silent empty overlap list.
func TestShipStateOpenspecCheck_OwnSpecsUnreadable(t *testing.T) {
	dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	osWriteChange(t, dir, "add-widget", true, "widget")
	osSetStateKeys(t, path, map[string]any{"openspecBaseHead": strings.Repeat("e", 40)})
	osStubValidate(t, "add-widget", "bad", 1)
	specs := filepath.Join(dir, "openspec", "changes", "add-widget", "specs")
	prev := shipCommitCheckGit
	shipCommitCheckGit = func(d string, args ...string) (string, error) {
		// The probes of the outcome already ran: deny the specs folder now.
		osDenyReadDir(t, specs)
		return "", nil
	}
	t.Cleanup(func() { shipCommitCheckGit = prev })

	out, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	want := "overlap check skipped: read the specs of the change: open " + specs + ": permission denied"
	if !reflect.DeepEqual(out.Warnings, []string{want}) {
		t.Errorf("warnings = %q, want [%q]", out.Warnings, want)
	}
}

// osDenied is the error of a probe that file permissions refuse.
func osDenied(op, path string) error {
	return &os.PathError{Op: op, Path: path, Err: syscall.EACCES}
}

// osDenyReadDir makes the folder listing of the OpenSpec probes fail with
// permission denied for path. Other paths are listed as usual.
func osDenyReadDir(t *testing.T, path string) {
	t.Helper()
	prev := shipOpenspecReadDirFunc
	shipOpenspecReadDirFunc = func(p string) ([]os.DirEntry, error) {
		if p == path {
			return nil, osDenied("open", p)
		}
		return prev(p)
	}
	t.Cleanup(func() { shipOpenspecReadDirFunc = prev })
}

// osDenyStat makes the stat call of the OpenSpec probes fail with
// permission denied for path. Other paths are read as usual.
func osDenyStat(t *testing.T, path string) {
	t.Helper()
	prev := shipOpenspecStatFunc
	shipOpenspecStatFunc = func(p string) (os.FileInfo, error) {
		if p == path {
			return nil, osDenied("stat", p)
		}
		return prev(p)
	}
	t.Cleanup(func() { shipOpenspecStatFunc = prev })
}

// TestShipStateOpenspecCheck_ProbeErrors checks that a probe error other
// than a missing path is an InfraError that names the path and keeps the
// cause, and that it writes no fail row.
func TestShipStateOpenspecCheck_ProbeErrors(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string) string // returns the denied path
		deny  func(t *testing.T, path string)
		op    string
	}{
		{"change folder stat", func(t *testing.T, dir string) string {
			osWriteChange(t, dir, "add-widget", true, "widget")
			return filepath.Join(dir, "openspec", "changes", "add-widget")
		}, osDenyStat, "stat"},
		{"archive folder list", func(t *testing.T, dir string) string {
			return filepath.Join(dir, "openspec", "changes", "archive")
		}, osDenyReadDir, "open"},
		{"archive entry stat", func(t *testing.T, dir string) string {
			p := filepath.Join(dir, "openspec", "changes", "archive", "2026-10-30-add-widget")
			writeFile(t, filepath.Join(p, "proposal.md"), "# P\n")
			return p
		}, osDenyStat, "stat"},
		{"proposal stat", func(t *testing.T, dir string) string {
			osWriteChange(t, dir, "add-widget", true, "widget")
			return filepath.Join(dir, "openspec", "changes", "add-widget", "proposal.md")
		}, osDenyStat, "stat"},
		{"specs folder list", func(t *testing.T, dir string) string {
			osWriteChange(t, dir, "add-widget", true, "widget")
			return filepath.Join(dir, "openspec", "changes", "add-widget", "specs")
		}, osDenyReadDir, "open"},
		{"spec file stat", func(t *testing.T, dir string) string {
			osWriteChange(t, dir, "add-widget", true, "widget")
			return filepath.Join(dir, "openspec", "changes", "add-widget", "specs", "widget", "spec.md")
		}, osDenyStat, "stat"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, path, branch := osCheckFixture(t, "archive-openspec", osNamed("add-widget"))
			denied := tc.setup(t, dir)
			tc.deny(t, denied)
			stubOpenspecCLI(t, nil) // validate must not run

			_, err := osCheck(t, dir, branch, "archive-openspec", nil)
			var ie *mcpserver.InfraError
			if !errors.As(err, &ie) {
				t.Fatalf("err = %v, want InfraError", err)
			}
			wantMsg := "openspec-check: " + tc.op + " " + denied + ": permission denied"
			if ie.Msg != wantMsg || ie.Suggestion != "Fix read access to the path in the message, then call openspec-check again." {
				t.Errorf("msg = %q, suggestion = %q, want msg %q", ie.Msg, ie.Suggestion, wantMsg)
			}
			if !errors.Is(err, syscall.EACCES) {
				t.Errorf("err = %v, want cause EACCES", err)
			}
			osAssertOpen(t, path, "archive-openspec")
		})
	}
}

// TestShipStateOpenspecCheck_ArchiveBeforeBaseIsNoOverlap checks that an
// archive folder that exists at the base commit is not an overlap.
func TestShipStateOpenspecCheck_ArchiveBeforeBaseIsNoOverlap(t *testing.T) {
	dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	osWriteChange(t, dir, "add-widget", true, "widget")
	writeFile(t, filepath.Join(dir, "openspec", "changes", "archive", "2026-10-01-old", "specs", "widget", "spec.md"), "# S\n")
	gitCommit(t, dir, "archive old")
	osSetStateKeys(t, path, map[string]any{"openspecBaseHead": osGitHead(t, dir)})
	osStubValidate(t, "add-widget", "bad", 1)

	out, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	if len(out.Overlaps) != 0 || len(out.Warnings) != 0 {
		t.Errorf("overlaps = %v, warnings = %v, want none", out.Overlaps, out.Warnings)
	}
}

func TestShipStateOpenspecCheck_BadBaseHeadNeverReachesGit(t *testing.T) {
	cases := map[string]any{
		"malformed":     "HEAD~1; rm -rf",
		"short sha":     "abc123",
		"upper case":    strings.Repeat("A", 40),
		"not a string":  42,
		"option-shaped": "--output=/x",
		"41 hex":        strings.Repeat("a", 41),
		"63 hex":        strings.Repeat("a", 63),
		"65 hex":        strings.Repeat("a", 65),
	}
	for name, base := range cases {
		t.Run(name, func(t *testing.T) {
			dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
			osWriteChange(t, dir, "add-widget", true, "widget")
			osSetStateKeys(t, path, map[string]any{"openspecBaseHead": base})
			osStubValidate(t, "add-widget", "bad", 1)
			osNoGit(t)

			out, err := osCheck(t, dir, branch, "verify-openspec", nil)
			if err != nil {
				t.Fatalf("openspec-check: %v", err)
			}
			if out.Outcome != "invalid" || len(out.Overlaps) != 0 {
				t.Errorf("out = %+v, want invalid with no overlaps", out)
			}
			if !reflect.DeepEqual(out.Warnings, []string{osMalformedBaseWarning}) {
				t.Errorf("warnings = %q, want [%q]", out.Warnings, osMalformedBaseWarning)
			}
		})
	}
}

// TestShipStateOpenspecCheck_FullShaFormsReachGit checks that a SHA-1 (40
// hex) and a SHA-256 (64 hex) base both pass the pattern and reach git.
func TestShipStateOpenspecCheck_FullShaFormsReachGit(t *testing.T) {
	for _, n := range []int{40, 64} {
		t.Run(fmt.Sprintf("%d hex", n), func(t *testing.T) {
			base := strings.Repeat("b", n)
			dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
			osWriteChange(t, dir, "add-widget", true, "widget")
			osSetStateKeys(t, path, map[string]any{"openspecBaseHead": base})
			osStubValidate(t, "add-widget", "bad", 1)
			var gotArgs []string
			prev := shipCommitCheckGit
			shipCommitCheckGit = func(d string, args ...string) (string, error) { gotArgs = args; return "", nil }
			t.Cleanup(func() { shipCommitCheckGit = prev })

			out, err := osCheck(t, dir, branch, "verify-openspec", nil)
			if err != nil {
				t.Fatalf("openspec-check: %v", err)
			}
			wantArgs := []string{"diff", "--name-only", "--diff-filter=A", base + "..HEAD"}
			if !reflect.DeepEqual(gotArgs, wantArgs) {
				t.Errorf("git args = %q, want %q", gotArgs, wantArgs)
			}
			if len(out.Warnings) != 0 {
				t.Errorf("warnings = %q, want none", out.Warnings)
			}
		})
	}
}

func TestShipStateOpenspecCheck_GitDiffFailures(t *testing.T) {
	base := strings.Repeat("a", 40)
	cases := []struct {
		name    string
		gitErr  error
		warning string
	}{
		{"cap", fmt.Errorf("execx: git: %w", execx.ErrOutputCap),
			"overlap check skipped: git diff output over the limit of 52428800 bytes"},
		{"other", errors.New("bad revision"),
			"overlap check skipped: git diff " + base + "..HEAD: bad revision"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
			osWriteChange(t, dir, "add-widget", true, "widget")
			osSetStateKeys(t, path, map[string]any{"openspecBaseHead": base})
			osStubValidate(t, "add-widget", "bad", 1)
			prev := shipCommitCheckGit
			shipCommitCheckGit = func(d string, args ...string) (string, error) { return "", tc.gitErr }
			t.Cleanup(func() { shipCommitCheckGit = prev })

			out, err := osCheck(t, dir, branch, "verify-openspec", nil)
			if err != nil {
				t.Fatalf("openspec-check: %v", err)
			}
			if out.Outcome != "invalid" || len(out.Overlaps) != 0 {
				t.Errorf("out = %+v, want invalid with no overlaps", out)
			}
			if !reflect.DeepEqual(out.Warnings, []string{tc.warning}) {
				t.Errorf("warnings = %v, want [%q]", out.Warnings, tc.warning)
			}
			osAssertFailed(t, path, "verify-openspec", "openspec validate add-widget --strict failed")
		})
	}
}

// ---------------------------------------------------------------------------
// Input table
// ---------------------------------------------------------------------------

func TestShipStateOpenspecCheck_ChangeNameRows(t *testing.T) {
	long129 := strings.Repeat("a", 129)
	cases := []struct {
		name       string
		flags      map[string]any
		detail     map[string]any
		wantMsg    string
		suggestion string
	}{
		{"no change", map[string]any{}, nil,
			"openspec-check: no OpenSpec change named",
			`Call skip with reason "no active OpenSpec change".`},
		{"mismatch", osNamed("one"), map[string]any{"change": "two"},
			`openspec-check: detail.change "two" differs from the named change "one"`,
			`Omit detail.change, or pass "one".`},
		{"too long", osNamed(long129), nil,
			"openspec-check: change name is 129 bytes, limit 128 bytes",
			"Pass a change name of 128 bytes or fewer."},
		{"invalid name", osNamed("Bad_Name"), nil,
			`openspec-check: invalid change name "Bad_Name"`,
			"Pass a name of lower-case letters, digits and single hyphens."},
		{"invalid detail name", map[string]any{}, map[string]any{"change": "grp/demo"},
			`openspec-check: invalid change name "grp/demo"`,
			"Pass a name of lower-case letters, digits and single hyphens."},
		{"sync", osNamed("add-widget"), map[string]any{"sync": true},
			"openspec-check: detail.sync applies only to openspec-tasks",
			"Remove detail.sync."},
		// The key is refused whatever its value.
		{"sync false", osNamed("add-widget"), map[string]any{"sync": false},
			"openspec-check: detail.sync applies only to openspec-tasks",
			"Remove detail.sync."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, path, branch := osCheckFixture(t, "verify-openspec", tc.flags)
			_, err := osCheck(t, dir, branch, "verify-openspec", tc.detail)
			var de *mcpserver.DomainError
			if !errors.As(err, &de) {
				t.Fatalf("err = %v, want DomainError", err)
			}
			if de.Msg != tc.wantMsg || de.Suggestion != tc.suggestion {
				t.Errorf("msg = %q, suggestion = %q\nwant msg = %q, suggestion = %q", de.Msg, de.Suggestion, tc.wantMsg, tc.suggestion)
			}
			osAssertOpen(t, path, "verify-openspec")
		})
	}
}

// TestShipStateOpenspecCheck_NameOfExactLimitPasses checks that a 128-byte
// name passes the length row.
func TestShipStateOpenspecCheck_NameOfExactLimitPasses(t *testing.T) {
	name := strings.Repeat("a", 128)
	dir, _, branch := osCheckFixture(t, "verify-openspec", osNamed(name))

	out, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	if out.Outcome != "missing-on-disk" {
		t.Errorf("outcome = %q, want missing-on-disk (the name passed the length row)", out.Outcome)
	}
}

// TestShipStateOpenspecCheck_DetailChangeWhenNoneNamed checks that
// detail.change serves a change when flags.openspecChange is empty, and that
// the same name as the flag is not a mismatch.
func TestShipStateOpenspecCheck_DetailChangeWhenNoneNamed(t *testing.T) {
	dir, _, branch := osCheckFixture(t, "verify-openspec", map[string]any{"openspecChange": nil})
	osWriteChange(t, dir, "add-widget", true, "widget")
	osStubValidate(t, "add-widget", "valid", 0)

	out, err := osCheck(t, dir, branch, "verify-openspec", map[string]any{"change": "add-widget"})
	if err != nil || out.Change != "add-widget" || out.Outcome != "ok" {
		t.Fatalf("detail.change only: out = %+v, err = %v, want ok for add-widget", out, err)
	}

	dir, _, branch = osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	osWriteChange(t, dir, "add-widget", true, "widget")
	osStubValidate(t, "add-widget", "valid", 0)
	out, err = osCheck(t, dir, branch, "verify-openspec", map[string]any{"change": "add-widget"})
	if err != nil || out.Outcome != "ok" {
		t.Fatalf("same name twice: out = %+v, err = %v, want ok", out, err)
	}
}

func TestShipStateOpenspecCheck_StepRows(t *testing.T) {
	t.Run("other step", func(t *testing.T) {
		dir, _, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
		_, err := osCheck(t, dir, branch, "commit", nil)
		var de *mcpserver.DomainError
		if !errors.As(err, &de) {
			t.Fatalf("err = %v, want DomainError", err)
		}
		if de.Msg != `openspec-check: step "commit" has no OpenSpec check` ||
			de.Suggestion != "Call openspec-check with step verify-openspec or archive-openspec." {
			t.Errorf("msg = %q, suggestion = %q", de.Msg, de.Suggestion)
		}
	})
	t.Run("not in progress", func(t *testing.T) {
		dir, _, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
		// archive-openspec is pending in the fixture.
		_, err := osCheck(t, dir, branch, "archive-openspec", nil)
		var de *mcpserver.DomainError
		if !errors.As(err, &de) {
			t.Fatalf("err = %v, want DomainError", err)
		}
		if de.Msg != `openspec-check: step "archive-openspec" is pending, not in_progress` ||
			de.Suggestion != "Call begin-step for archive-openspec first, then call openspec-check again." {
			t.Errorf("msg = %q, suggestion = %q", de.Msg, de.Suggestion)
		}
	})
	t.Run("no step entry", func(t *testing.T) {
		dir, path, branch := osCheckFixture(t, "archive-openspec", osNamed("add-widget"))
		setSteps(t, path, "archive-openspec")
		_, err := osCheck(t, dir, branch, "verify-openspec", nil)
		osWantTasksErr(t, err, "data", "openspec-check: no verify-openspec step in the ship state",
			"Run ship_state read to list the steps. Call openspec-check only for a pipeline that has this step.")
	})
	t.Run("no ship state", func(t *testing.T) {
		dir, _, _ := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
		_, err := osCheck(t, dir, "feat/none", "verify-openspec", nil)
		osWantTasksErr(t, err, "data", `no ship state found for branch "feat/none"`,
			"Run ship_state init with detail.branch set to this branch before calling other ship_state actions.")
		if !errors.Is(err, errNoShipState) {
			t.Errorf("err = %v, want cause errNoShipState", err)
		}
	})
}

// TestShipStateOpenspec_WrongTypedDetail checks that a detail value of the
// wrong type is a DomainError in both actions, never read as absent.
func TestShipStateOpenspec_WrongTypedDetail(t *testing.T) {
	cases := []struct {
		name   string
		action string
		flags  map[string]any
		detail map[string]any
		msg    string
		sug    string
	}{
		{"check change number", "openspec-check", map[string]any{}, map[string]any{"change": 5},
			"openspec-check: detail.change must be a string, got float64",
			"Pass detail.change as a JSON string, or omit the key entirely. Pass the OpenSpec change name."},
		{"check change ignored when named", "openspec-check", osNamed("add-widget"), map[string]any{"change": true},
			"openspec-check: detail.change must be a string, got bool",
			"Pass detail.change as a JSON string, or omit the key entirely. Pass the OpenSpec change name."},
		{"check branch object", "openspec-check", osNamed("add-widget"), map[string]any{"branch": map[string]any{}},
			"openspec-check: detail.branch must be a string, got map[string]interface {}",
			"Pass detail.branch as a JSON string, or omit the key entirely. Pass the branch name, or omit it for the current branch."},
		{"tasks change number", "openspec-tasks", map[string]any{}, map[string]any{"change": 5},
			"openspec-tasks: detail.change must be a string, got float64",
			"Pass detail.change as a JSON string, or omit the key entirely. Pass the OpenSpec change name."},
		{"tasks sync string", "openspec-tasks", osNamed("add-widget"), map[string]any{"sync": "true"},
			"openspec-tasks: detail.sync must be a boolean, got string",
			"Pass detail.sync as the JSON boolean true or false (not a string or number), or omit the key entirely. true marks each open box done; omit it to count only."},
		{"tasks sync number", "openspec-tasks", osNamed("add-widget"), map[string]any{"sync": 1},
			"openspec-tasks: detail.sync must be a boolean, got float64",
			"Pass detail.sync as the JSON boolean true or false (not a string or number), or omit the key entirely. true marks each open box done; omit it to count only."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, path, branch := osCheckFixture(t, "archive-openspec", tc.flags)
			tasks := filepath.Join(dir, "openspec", "changes", "add-widget", "tasks.md")
			writeFile(t, tasks, "- [ ] a\n")
			d := map[string]any{"branch": branch}
			for k, v := range tc.detail {
				d[k] = v
			}
			// JSON numbers decode as float64, as they do from a real call.
			raw, _ := json.Marshal(d)
			var decoded map[string]any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			_, err := shipState(dir, dir, ShipStateIn{Action: tc.action, Step: "archive-openspec", Detail: decoded},
				fixedNow(time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)))
			osWantTasksErr(t, err, "domain", tc.msg, tc.sug)
			if got := osReadFile(t, tasks); got != "- [ ] a\n" {
				t.Errorf("tasks.md changed to %q", got)
			}
			osAssertOpen(t, path, "archive-openspec")
		})
	}
}

func TestShipStateOpenspecCheck_ValidateOutputCut(t *testing.T) {
	// "é" is 2 bytes. 16383 ASCII bytes and then "é" put the cap of 16384
	// bytes inside the character, so the cut moves back to byte 16383.
	split := strings.Repeat("x", 16383) + "é" + "tail"
	for _, tc := range []struct {
		name    string
		body    string
		exit    int
		want    string
		warning string
	}{
		{"exact limit", strings.Repeat("x", 16384), 0, strings.Repeat("x", 16384), ""},
		{"one over", strings.Repeat("x", 16385), 0,
			strings.Repeat("x", 16384) + "\n[validate output cut at 16384 of 16385 bytes]",
			"validateOutput is cut: openspec validate printed 16385 bytes, limit 16384 bytes"},
		{"character across the cap", split, 0,
			strings.Repeat("x", 16383) + "\n[validate output cut at 16383 of 16389 bytes]",
			"validateOutput is cut: openspec validate printed 16389 bytes, limit 16384 bytes"},
		{"cut on an invalid result", strings.Repeat("y", 16390), 1,
			strings.Repeat("y", 16384) + "\n[validate output cut at 16384 of 16390 bytes]",
			"validateOutput is cut: openspec validate printed 16390 bytes, limit 16384 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
			osWriteChange(t, dir, "add-widget", true, "widget")
			osStubValidate(t, "add-widget", tc.body, tc.exit)

			out, err := osCheck(t, dir, branch, "verify-openspec", nil)
			if err != nil {
				t.Fatalf("openspec-check: %v", err)
			}
			if out.ValidateOutput != tc.want {
				t.Errorf("validateOutput: len = %d, want %d, tail %q", len(out.ValidateOutput), len(tc.want), osTail(out.ValidateOutput))
			}
			if !utf8.ValidString(out.ValidateOutput) {
				t.Error("validateOutput is not valid UTF-8: the cut split a character")
			}
			var wantWarnings []string
			if tc.warning != "" {
				wantWarnings = append(wantWarnings, tc.warning)
			}
			if tc.exit != 0 {
				wantWarnings = append(wantWarnings, osAbsentBaseWarning)
			}
			if !reflect.DeepEqual(out.Warnings, append([]string{}, wantWarnings...)) {
				t.Errorf("warnings = %q, want %q", out.Warnings, wantWarnings)
			}
		})
	}
}

// osTail returns the last 60 bytes of s, for failure messages.
func osTail(s string) string {
	if len(s) <= 60 {
		return s
	}
	return s[len(s)-60:]
}

// TestShipStateOpenspecCheck_ValidateOutputTagNamesTheCap ties the
// jsonschema_description of ValidateOutput to shipOpenspecValidateMaxBytes,
// so the tag cannot go stale when the constant changes.
func TestShipStateOpenspecCheck_ValidateOutputTagNamesTheCap(t *testing.T) {
	f, ok := reflect.TypeOf(shipOpenspecCheckOut{}).FieldByName("ValidateOutput")
	if !ok {
		t.Fatal("no ValidateOutput field")
	}
	want := fmt.Sprintf("cut at %d bytes", shipOpenspecValidateMaxBytes)
	if tag := f.Tag.Get("jsonschema_description"); !strings.Contains(tag, want) {
		t.Errorf("tag = %q, want it to contain %q", tag, want)
	}
}

// ---------------------------------------------------------------------------
// Failures of the write and of the CLI
// ---------------------------------------------------------------------------

func TestShipStateOpenspecCheck_FailRowWriteFails(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		"missing-on-disk": func(*testing.T, string) {},
		"missing-artifacts": func(t *testing.T, dir string) {
			osWriteChange(t, dir, "add-widget", false, "widget")
		},
		"invalid": func(t *testing.T, dir string) {
			osWriteChange(t, dir, "add-widget", true, "widget")
			osStubValidate(t, "add-widget", "bad", 1)
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
			setup(t, dir)
			diskFull := errors.New("disk full")
			useShipStateWrite(t, func(*state.State) error { return diskFull })
			useShipHistoryAppend(t, func(string, history.RunRecord) error {
				t.Error("history row appended after a failed state write")
				return nil
			})

			_, err := osCheck(t, dir, branch, "verify-openspec", nil)
			var ie *mcpserver.InfraError
			if !errors.As(err, &ie) {
				t.Fatalf("err = %v, want InfraError", err)
			}
			if ie.Msg != "openspec-check: write ship state: disk full" ||
				ie.Suggestion != "Fix write access to .sdlc-v2/runs/, then call openspec-check again." {
				t.Errorf("msg = %q, suggestion = %q", ie.Msg, ie.Suggestion)
			}
			if !errors.Is(err, diskFull) {
				t.Errorf("err = %v, want cause diskFull", err)
			}
			osAssertOpen(t, path, "verify-openspec")
		})
	}
}

// TestShipStateOpenspecCheck_FirstFailHistoryWarnings checks the history row
// of the first fail: a failed append adds its warning after the overlap
// warning, and a second failing call appends no row and adds no warning.
func TestShipStateOpenspecCheck_FirstFailHistoryWarnings(t *testing.T) {
	dir, _, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	osWriteChange(t, dir, "add-widget", true, "widget")
	osStubValidate(t, "add-widget", "bad", 1)
	appends := 0
	useShipHistoryAppend(t, func(string, history.RunRecord) error {
		appends++
		return errors.New("history locked")
	})

	out, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("openspec-check: %v", err)
	}
	wantHistory := "Failure history row not written to .sdlc-v2/history/runs.jsonl: history locked" +
		`. To add it, call ship_state history_record with detail.skill "ship" and detail.outcome "failure".`
	if !reflect.DeepEqual(out.Warnings, []string{osAbsentBaseWarning, wantHistory}) {
		t.Errorf("warnings = %q, want [%q %q]", out.Warnings, osAbsentBaseWarning, wantHistory)
	}

	// The step is failed now; a resumed run begins it again and fails again.
	_, err = shipState(dir, dir, ShipStateIn{Action: "begin-step", Step: "verify-openspec", Detail: map[string]any{"branch": branch}},
		fixedNow(time.Date(2026, 1, 1, 0, 6, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("begin-step: %v", err)
	}
	again, err := osCheck(t, dir, branch, "verify-openspec", nil)
	if err != nil {
		t.Fatalf("second openspec-check: %v", err)
	}
	if !reflect.DeepEqual(again.Warnings, []string{osAbsentBaseWarning}) {
		t.Errorf("second warnings = %q, want only the overlap warning", again.Warnings)
	}
	if appends != 1 {
		t.Errorf("history appends = %d, want 1", appends)
	}
}

func TestShipStateOpenspecCheck_CLIMissing(t *testing.T) {
	dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	osWriteChange(t, dir, "add-widget", true, "widget")
	pathWithoutOpenspec(t)

	_, err := osCheck(t, dir, branch, "verify-openspec", nil)
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %v, want InfraError", err)
	}
	if ie.Msg != "openspec-check: "+openspec.ErrCLINotFound.Error() ||
		ie.Suggestion != "Install the openspec CLI, then call openspec-check again." {
		t.Errorf("msg = %q, suggestion = %q", ie.Msg, ie.Suggestion)
	}
	if !errors.Is(err, openspec.ErrCLINotFound) {
		t.Errorf("err = %v, want cause ErrCLINotFound", err)
	}
	osAssertOpen(t, path, "verify-openspec")
}

// TestShipStateOpenspecInfraError covers each arm of the InfraError of a
// failed validate call: missing CLI, output over the cap, and any other
// failure.
func TestShipStateOpenspecInfraError(t *testing.T) {
	capErr := fmt.Errorf("openspec validate add-widget --strict: %w", fmt.Errorf("execx: openspec: %w", execx.ErrOutputCap))
	otherErr := fmt.Errorf("openspec validate add-widget --strict: %w", errors.New("exec format error"))
	cases := []struct {
		name       string
		err        error
		sentinel   error
		suggestion string
	}{
		{"cli missing", openspec.ErrCLINotFound, openspec.ErrCLINotFound,
			"Install the openspec CLI, then call openspec-check again."},
		{"output cap", capErr, execx.ErrOutputCap,
			"Run openspec validate add-widget --strict in a terminal to read the output, fix the change, then call openspec-check again."},
		{"other", otherErr, otherErr,
			"Check the openspec CLI with openspec --version, then call openspec-check again."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := shipOpenspecInfraError("add-widget", tc.err)
			var ie *mcpserver.InfraError
			if !errors.As(err, &ie) {
				t.Fatalf("err = %v, want InfraError", err)
			}
			if ie.Msg != "openspec-check: "+tc.err.Error() || ie.Suggestion != tc.suggestion {
				t.Errorf("msg = %q, suggestion = %q, want suggestion %q", ie.Msg, ie.Suggestion, tc.suggestion)
			}
			if !errors.Is(err, tc.sentinel) {
				t.Errorf("err = %v, want cause %v", err, tc.sentinel)
			}
		})
	}
}

// TestShipStateOpenspecCheck_ValidateExecFails runs the check with a
// validate call that fails before the CLI exits. The InfraError gets the
// generic CLI Suggestion and writes no fail row.
func TestShipStateOpenspecCheck_ValidateExecFails(t *testing.T) {
	dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	osWriteChange(t, dir, "add-widget", true, "widget")
	// A file with no #! line and no machine code: exec fails with ENOEXEC.
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "openspec"), []byte("\x00\x01not a program"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := osCheck(t, dir, branch, "verify-openspec", nil)
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %v, want InfraError", err)
	}
	if !strings.HasPrefix(ie.Msg, "openspec-check: openspec validate add-widget --strict: ") ||
		ie.Suggestion != "Check the openspec CLI with openspec --version, then call openspec-check again." {
		t.Errorf("msg = %q, suggestion = %q", ie.Msg, ie.Suggestion)
	}
	if !errors.Is(err, syscall.ENOEXEC) {
		t.Errorf("err = %v, want cause ENOEXEC", err)
	}
	osAssertOpen(t, path, "verify-openspec")
}

// ---------------------------------------------------------------------------
// Parity with AnalyzeChange
// ---------------------------------------------------------------------------

// TestShipStateOpenspecCheck_ParityWithAnalyzeChange runs a change path that
// is a file, and one that is a link loop, through AnalyzeChange and through
// openspec-check. AnalyzeChange finds no artifacts there, and openspec-check
// reports missing-on-disk.
func TestShipStateOpenspecCheck_ParityWithAnalyzeChange(t *testing.T) {
	cases := map[string]func(t *testing.T, changePath string){
		"file": func(t *testing.T, p string) { writeFile(t, p, "not a folder") },
		"link loop": func(t *testing.T, p string) {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Base(p), p); err != nil {
				t.Skipf("UNTESTED on this host: a change path that is a link loop must read as missing-on-disk. "+
					"The host cannot create a symlink (%v); run this test on a host that can.", err)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
			changePath := filepath.Join(dir, "openspec", "changes", "add-widget")
			setup(t, changePath)

			info := openspec.AnalyzeChange(changePath, "add-widget")
			if info.HasProposal || info.DeltaSpecCount != 0 {
				t.Fatalf("AnalyzeChange = %+v, want no proposal and no delta specs", info)
			}
			out, err := osCheck(t, dir, branch, "verify-openspec", nil)
			if err != nil {
				t.Fatalf("openspec-check: %v", err)
			}
			if out.Outcome != "missing-on-disk" {
				t.Errorf("outcome = %q, want missing-on-disk", out.Outcome)
			}
			osAssertFailed(t, path, "verify-openspec", out.Next)
		})
	}
}

// ---------------------------------------------------------------------------
// openspec-tasks
// ---------------------------------------------------------------------------

// osTasks calls openspec-tasks through the dispatcher.
func osTasks(t *testing.T, dir, branch, step string, detail map[string]any) (shipOpenspecTasksOut, error) {
	t.Helper()
	d := map[string]any{"branch": branch}
	for k, v := range detail {
		d[k] = v
	}
	out, err := shipState(dir, dir, ShipStateIn{Action: "openspec-tasks", Step: step, Detail: d},
		fixedNow(time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)))
	if err != nil {
		return shipOpenspecTasksOut{}, err
	}
	res, ok := out.(shipOpenspecTasksOut)
	if !ok {
		t.Fatalf("openspec-tasks output = %T, want shipOpenspecTasksOut", out)
	}
	return res, nil
}

// osTasksFixture builds the openspec-check fixture on archive-openspec with
// the named change and writes content as its tasks.md. It returns the path of
// tasks.md.
func osTasksFixture(t *testing.T, content string) (dir, path, branch, tasks string) {
	t.Helper()
	dir, path, branch = osCheckFixture(t, "archive-openspec", osNamed("add-widget"))
	tasks = filepath.Join(dir, "openspec", "changes", "add-widget", "tasks.md")
	writeFile(t, tasks, content)
	return dir, path, branch, tasks
}

func osReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// osWantTasksErr checks the type, message and suggestion of err.
func osWantTasksErr(t *testing.T, err error, kind, msg, suggestion string) {
	t.Helper()
	var gotMsg, gotSug string
	switch kind {
	case "domain":
		var e *mcpserver.DomainError
		if !errors.As(err, &e) {
			t.Fatalf("err = %v, want DomainError", err)
		}
		gotMsg, gotSug = e.Msg, e.Suggestion
	case "data":
		var e *mcpserver.DataError
		if !errors.As(err, &e) {
			t.Fatalf("err = %v, want DataError", err)
		}
		gotMsg, gotSug = e.Msg, e.Suggestion
	case "infra":
		var e *mcpserver.InfraError
		if !errors.As(err, &e) {
			t.Fatalf("err = %v, want InfraError", err)
		}
		gotMsg, gotSug = e.Msg, e.Suggestion
	}
	if gotMsg != msg || gotSug != suggestion {
		t.Errorf("msg = %q, suggestion = %q\nwant msg = %q, suggestion = %q", gotMsg, gotSug, msg, suggestion)
	}
}

const osTasksSample = "# Tasks\n- [ ] 1.1 a\n  - [x] 1.2 b\n```\n- [ ] fenced\n```\n- [ ] 2.1 c\n"

func TestShipStateOpenspecTasks_CountOnlyWritesNothing(t *testing.T) {
	for name, detail := range map[string]map[string]any{"no sync key": nil, "sync false": {"sync": false}} {
		t.Run(name, func(t *testing.T) {
			dir, path, branch, tasks := osTasksFixture(t, osTasksSample)
			out, err := osTasks(t, dir, branch, "archive-openspec", detail)
			if err != nil {
				t.Fatalf("openspec-tasks: %v", err)
			}
			if out.Change != "add-widget" || out.Written || len(out.Changed) != 0 || out.Changed == nil {
				t.Errorf("out = %+v, want change add-widget, written false, changed []", out)
			}
			if out.Warnings == nil || len(out.Warnings) != 0 {
				t.Errorf("warnings = %#v, want a non-nil empty list", out.Warnings)
			}
			want := openspec.TaskCounts{Done: 1, Total: 3}
			if out.Before != want || out.After != want {
				t.Errorf("before = %+v after = %+v, want %+v", out.Before, out.After, want)
			}
			wantNext := "Ask for archive consent (skip the question under flags.auto). On consent, " +
				"call openspec-tasks with detail.sync true, then run openspec archive add-widget."
			if out.Next != wantNext {
				t.Errorf("next = %q, want %q", out.Next, wantNext)
			}
			if got := osReadFile(t, tasks); got != osTasksSample {
				t.Errorf("tasks.md changed to %q", got)
			}
			osAssertOpen(t, path, "archive-openspec")
		})
	}
}

func TestShipStateOpenspecTasks_CountOnlyNoOpenBox(t *testing.T) {
	dir, _, branch, _ := osTasksFixture(t, "- [x] a\n- [X] b\n")
	out, err := osTasks(t, dir, branch, "archive-openspec", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "Ask for archive consent (skip the question under flags.auto). On consent, run openspec archive add-widget."
	if out.Next != want {
		t.Errorf("next = %q, want %q", out.Next, want)
	}
}

func TestShipStateOpenspecTasks_Sync(t *testing.T) {
	dir, path, branch, tasks := osTasksFixture(t, "# T\n\n\n- [ ] a\n- [x] b\n- [ ] c")
	if err := os.Chmod(tasks, 0o640); err != nil {
		t.Fatal(err)
	}
	out, err := osTasks(t, dir, branch, "archive-openspec", map[string]any{"sync": true})
	if err != nil {
		t.Fatal(err)
	}
	wantChanged := []openspec.TaskLine{{Number: 4, Text: "- [x] a"}, {Number: 6, Text: "- [x] c"}}
	if !reflect.DeepEqual(out.Changed, wantChanged) {
		t.Errorf("changed = %+v, want %+v", out.Changed, wantChanged)
	}
	if !out.Written || out.Before != (openspec.TaskCounts{Done: 1, Total: 3}) || out.After != (openspec.TaskCounts{Done: 3, Total: 3}) {
		t.Errorf("out = %+v", out)
	}
	if out.Next != "Run openspec archive add-widget." {
		t.Errorf("next = %q", out.Next)
	}
	if got := osReadFile(t, tasks); got != "# T\n\n\n- [x] a\n- [x] b\n- [x] c" {
		t.Errorf("tasks.md = %q", got)
	}
	if info, _ := os.Stat(tasks); info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(tasks))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file %s left behind", e.Name())
		}
	}
	osAssertOpen(t, path, "archive-openspec")

	// Rerun: no open box, nothing written.
	again, err := osTasks(t, dir, branch, "archive-openspec", map[string]any{"sync": true})
	if err != nil {
		t.Fatal(err)
	}
	if again.Written || len(again.Changed) != 0 || again.Before.Total != again.Before.Done {
		t.Errorf("rerun = %+v, want no open box and no write", again)
	}
	count, err := osTasks(t, dir, branch, "archive-openspec", nil)
	if err != nil {
		t.Fatal(err)
	}
	if count.Before != (openspec.TaskCounts{Done: 3, Total: 3}) {
		t.Errorf("count after sync = %+v, want 3/3", count.Before)
	}
}

func TestShipStateOpenspecTasks_NoFileOrNoBox(t *testing.T) {
	const consentNext = "Ask for archive consent (skip the question under flags.auto). On consent, run openspec archive add-widget."
	const syncNext = "Run openspec archive add-widget."
	cases := []struct {
		name    string
		content string // "" removes tasks.md
		sync    bool
		warning string
		next    string
	}{
		{"absent, count", "", false, "tasks.md is absent: there is no box to count or sync", consentNext},
		{"absent, sync", "", true, "tasks.md is absent: there is no box to count or sync", syncNext},
		{"no box, count", "# T\ntext\n", false, "tasks.md has no task box: there is nothing to sync", consentNext},
		{"no box, sync", "# T\ntext\n", true, "tasks.md has no task box: there is nothing to sync", syncNext},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, _, branch, tasks := osTasksFixture(t, tc.content)
			if tc.content == "" {
				if err := os.Remove(tasks); err != nil {
					t.Fatal(err)
				}
			}
			var detail map[string]any
			if tc.sync {
				detail = map[string]any{"sync": true}
			}
			out, err := osTasks(t, dir, branch, "archive-openspec", detail)
			if err != nil {
				t.Fatal(err)
			}
			if out.Written || out.Before != (openspec.TaskCounts{}) || out.After != (openspec.TaskCounts{}) ||
				out.Changed == nil || len(out.Changed) != 0 {
				t.Errorf("out = %+v, want counts 0, no change, no write", out)
			}
			if !reflect.DeepEqual(out.Warnings, []string{tc.warning}) {
				t.Errorf("warnings = %q, want [%q]", out.Warnings, tc.warning)
			}
			if out.Next != tc.next {
				t.Errorf("next = %q, want %q", out.Next, tc.next)
			}
			if tc.content == "" {
				if _, err := os.Stat(tasks); !os.IsNotExist(err) {
					t.Errorf("tasks.md exists after the call: %v", err)
				}
			} else if got := osReadFile(t, tasks); got != tc.content {
				t.Errorf("tasks.md = %q", got)
			}
		})
	}
}

func TestShipStateOpenspecTasks_ChangeNameRows(t *testing.T) {
	long129 := strings.Repeat("a", 129)
	cases := []struct {
		name       string
		flags      map[string]any
		detail     map[string]any
		wantMsg    string
		suggestion string
	}{
		{"no change", map[string]any{}, nil,
			"openspec-tasks: no OpenSpec change named",
			`Call skip with reason "no active OpenSpec change".`},
		{"mismatch", osNamed("one"), map[string]any{"change": "two"},
			`openspec-tasks: detail.change "two" differs from the named change "one"`,
			`Omit detail.change, or pass "one".`},
		{"too long", osNamed(long129), nil,
			"openspec-tasks: change name is 129 bytes, limit 128 bytes",
			"Pass a change name of 128 bytes or fewer."},
		{"invalid name", osNamed("Bad_Name"), nil,
			`openspec-tasks: invalid change name "Bad_Name"`,
			"Pass a name of lower-case letters, digits and single hyphens."},
		{"invalid detail name", map[string]any{}, map[string]any{"change": "../x"},
			`openspec-tasks: invalid change name "../x"`,
			"Pass a name of lower-case letters, digits and single hyphens."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, path, branch := osCheckFixture(t, "archive-openspec", tc.flags)
			_, err := osTasks(t, dir, branch, "archive-openspec", tc.detail)
			osWantTasksErr(t, err, "domain", tc.wantMsg, tc.suggestion)
			osAssertOpen(t, path, "archive-openspec")
		})
	}
}

func TestShipStateOpenspecTasks_StepRows(t *testing.T) {
	t.Run("other step", func(t *testing.T) {
		dir, _, branch := osCheckFixture(t, "archive-openspec", osNamed("add-widget"))
		_, err := osTasks(t, dir, branch, "verify-openspec", nil)
		osWantTasksErr(t, err, "domain", `openspec-tasks: step "verify-openspec" has no task sync`,
			"Call openspec-tasks with step archive-openspec.")
	})
	t.Run("not in progress", func(t *testing.T) {
		dir, _, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
		_, err := osTasks(t, dir, branch, "archive-openspec", nil)
		osWantTasksErr(t, err, "domain", `openspec-tasks: step "archive-openspec" is pending, not in_progress`,
			"Call begin-step for archive-openspec first, then call openspec-tasks again.")
	})
}

func TestShipStateOpenspecTasks_SizeRows(t *testing.T) {
	pad := func(n int) string { return "- [ ] a\n" + strings.Repeat("x", n-len("- [ ] a\n")) }
	t.Run("exact limit", func(t *testing.T) {
		dir, _, branch, tasks := osTasksFixture(t, pad(tasksFileMaxBytes))
		out, err := osTasks(t, dir, branch, "archive-openspec", map[string]any{"sync": true})
		if err != nil {
			t.Fatal(err)
		}
		if !out.Written || out.Before.Total != 1 {
			t.Errorf("out = %+v", out)
		}
		if got := osReadFile(t, tasks); !strings.HasPrefix(got, "- [x] a\n") || len(got) != tasksFileMaxBytes {
			t.Errorf("tasks.md has %d bytes, prefix %q", len(got), got[:8])
		}
	})
	t.Run("over limit", func(t *testing.T) {
		content := pad(tasksFileMaxBytes + 1)
		dir, _, branch, tasks := osTasksFixture(t, content)
		_, err := osTasks(t, dir, branch, "archive-openspec", map[string]any{"sync": true})
		osWantTasksErr(t, err, "data", "tasks.md is 1048577 bytes, limit 1048576 bytes",
			"Split tasks.md below 1048576 bytes, then call openspec-tasks again.")
		if got := osReadFile(t, tasks); got != content {
			t.Error("tasks.md changed")
		}
	})
}

func TestShipStateOpenspecTasks_NotRegularFile(t *testing.T) {
	const want = "Replace openspec/changes/add-widget/tasks.md with a regular file."
	t.Run("symlink", func(t *testing.T) {
		dir, _, branch, tasks := osTasksFixture(t, "x")
		target := filepath.Join(dir, "elsewhere.md")
		writeFile(t, target, "- [ ] a\n")
		if err := os.Remove(tasks); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, tasks); err != nil {
			t.Skipf("UNTESTED on this host: a tasks.md that is a symlink must be refused with a DataError and its target left unchanged. "+
				"The host cannot create a symlink (%v); run this test on a host that can.", err)
		}
		_, err := osTasks(t, dir, branch, "archive-openspec", map[string]any{"sync": true})
		osWantTasksErr(t, err, "data", "tasks.md is not a regular file", want)
		if got := osReadFile(t, target); got != "- [ ] a\n" {
			t.Errorf("link target changed to %q", got)
		}
	})
	t.Run("folder", func(t *testing.T) {
		dir, _, branch, tasks := osTasksFixture(t, "x")
		if err := os.Remove(tasks); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(tasks, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := osTasks(t, dir, branch, "archive-openspec", nil)
		osWantTasksErr(t, err, "data", "tasks.md is not a regular file", want)
	})
}

func TestShipStateOpenspecTasks_OpenFence(t *testing.T) {
	content := "# T\n- [ ] a\n\n\n\n\n```\n- [ ] b\n"
	dir, path, branch, tasks := osTasksFixture(t, content)
	_, err := osTasks(t, dir, branch, "archive-openspec", map[string]any{"sync": true})
	osWantTasksErr(t, err, "data", "tasks.md has an open code fence at line 7",
		"Close the code fence at line 7 of tasks.md, then call openspec-tasks again.")
	if got := osReadFile(t, tasks); got != content {
		t.Error("tasks.md changed")
	}
	osAssertOpen(t, path, "archive-openspec")
}

func TestShipStateOpenspecTasks_ReadWriteSeams(t *testing.T) {
	const suggestion = "Fix access to openspec/changes/add-widget/tasks.md, then call openspec-tasks again."
	boom := errors.New("boom")
	t.Run("read fails", func(t *testing.T) {
		dir, _, branch, _ := osTasksFixture(t, "- [ ] a\n")
		old := openspecTasksRead
		openspecTasksRead = func(string) ([]byte, error) { return nil, boom }
		t.Cleanup(func() { openspecTasksRead = old })
		_, err := osTasks(t, dir, branch, "archive-openspec", nil)
		osWantTasksErr(t, err, "infra", "openspec-tasks: boom", suggestion)
		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want cause boom", err)
		}
	})
	t.Run("write fails", func(t *testing.T) {
		dir, path, branch, tasks := osTasksFixture(t, "- [ ] a\n")
		old := openspecTasksWrite
		openspecTasksWrite = func(string, []byte) error { return boom }
		t.Cleanup(func() { openspecTasksWrite = old })
		_, err := osTasks(t, dir, branch, "archive-openspec", map[string]any{"sync": true})
		osWantTasksErr(t, err, "infra", "openspec-tasks: boom", suggestion)
		if got := osReadFile(t, tasks); got != "- [ ] a\n" {
			t.Errorf("tasks.md = %q, want unchanged", got)
		}
		osAssertOpen(t, path, "archive-openspec")
	})
	t.Run("write seam not called for count only", func(t *testing.T) {
		dir, _, branch, _ := osTasksFixture(t, "- [ ] a\n")
		old := openspecTasksWrite
		openspecTasksWrite = func(string, []byte) error { t.Error("write called"); return nil }
		t.Cleanup(func() { openspecTasksWrite = old })
		if _, err := osTasks(t, dir, branch, "archive-openspec", nil); err != nil {
			t.Fatal(err)
		}
	})
}

// TestShipStateOpenspecTasks_ParityWithAnalyzeChange checks that the count of
// a file without a fence equals the count of AnalyzeChange.
func TestShipStateOpenspecTasks_ParityWithAnalyzeChange(t *testing.T) {
	dir, _, branch, _ := osTasksFixture(t, "- [ ] a\n  - [x] b\n- [X] c\n\t- [ ] d\n")
	osWriteChange(t, dir, "add-widget", true, "demo")
	out, err := osTasks(t, dir, branch, "archive-openspec", nil)
	if err != nil {
		t.Fatal(err)
	}
	info := openspec.AnalyzeChange(filepath.Join(dir, "openspec", "changes", "add-widget"), "add-widget")
	if out.Before.Done != info.TasksDone || out.Before.Total != info.TasksTotal {
		t.Errorf("before = %+v, AnalyzeChange = %d/%d", out.Before, info.TasksDone, info.TasksTotal)
	}
}

func TestShipStateOpenspecTasks_StateRows(t *testing.T) {
	t.Run("no step entry", func(t *testing.T) {
		dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
		setSteps(t, path, "verify-openspec")
		_, err := osTasks(t, dir, branch, "archive-openspec", nil)
		osWantTasksErr(t, err, "data", "openspec-tasks: no archive-openspec step in the ship state",
			"Run ship_state read to list the steps. Call openspec-tasks only for a pipeline that has this step.")
	})
	t.Run("no ship state", func(t *testing.T) {
		dir, _, _ := osCheckFixture(t, "archive-openspec", osNamed("add-widget"))
		_, err := osTasks(t, dir, "feat/none", "archive-openspec", nil)
		osWantTasksErr(t, err, "data", `no ship state found for branch "feat/none"`,
			"Run ship_state init with detail.branch set to this branch before calling other ship_state actions.")
		if !errors.Is(err, errNoShipState) {
			t.Errorf("err = %v, want cause errNoShipState", err)
		}
	})
}

// TestShipStateOpenspecTasks_ReadGrewPastCap checks the size guard after the
// read: a tasks.md that was small at Lstat and over the cap at read is
// refused and not written.
func TestShipStateOpenspecTasks_ReadGrewPastCap(t *testing.T) {
	dir, path, branch, tasks := osTasksFixture(t, "- [ ] a\n")
	old := openspecTasksRead
	openspecTasksRead = func(string) ([]byte, error) {
		return []byte("- [ ] a\n" + strings.Repeat("x", tasksFileMaxBytes)), nil
	}
	t.Cleanup(func() { openspecTasksRead = old })

	_, err := osTasks(t, dir, branch, "archive-openspec", map[string]any{"sync": true})
	osWantTasksErr(t, err, "data", "tasks.md is 1048584 bytes, limit 1048576 bytes",
		"Split tasks.md below 1048576 bytes, then call openspec-tasks again.")
	if got := osReadFile(t, tasks); got != "- [ ] a\n" {
		t.Errorf("tasks.md changed to %q", got)
	}
	osAssertOpen(t, path, "archive-openspec")
}

// TestShipStateOpenspecTasks_LstatFails checks that an Lstat error other than
// a missing file is an InfraError with its cause. Here the change folder is
// a file, so the Lstat of tasks.md fails with ENOTDIR.
func TestShipStateOpenspecTasks_LstatFails(t *testing.T) {
	dir, path, branch := osCheckFixture(t, "archive-openspec", osNamed("add-widget"))
	changeDir := filepath.Join(dir, "openspec", "changes", "add-widget")
	writeFile(t, changeDir, "not a folder")

	_, err := osTasks(t, dir, branch, "archive-openspec", nil)
	osWantTasksErr(t, err, "infra", "openspec-tasks: lstat "+filepath.Join(changeDir, "tasks.md")+": not a directory",
		"Fix access to openspec/changes/add-widget/tasks.md, then call openspec-tasks again.")
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("err = %v, want cause ENOTDIR", err)
	}
	osAssertOpen(t, path, "archive-openspec")
}

// TestShipStateOpenspecTasks_WriteFailsKeepsCause checks that the InfraError
// of a failed write keeps its cause.
func TestShipStateOpenspecTasks_WriteFailsKeepsCause(t *testing.T) {
	dir, _, branch, _ := osTasksFixture(t, "- [ ] a\n")
	boom := errors.New("boom")
	old := openspecTasksWrite
	openspecTasksWrite = func(string, []byte) error { return boom }
	t.Cleanup(func() { openspecTasksWrite = old })

	_, err := osTasks(t, dir, branch, "archive-openspec", map[string]any{"sync": true})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want cause boom", err)
	}
}

// TestOpenspecTasksWrite_DefaultSeam runs the real write seam. A rename that
// fails after the temp file exists leaves no temp file and keeps the target.
// A stat error other than a missing file is returned before any write, and a
// missing file takes the mode 0644.
func TestOpenspecTasksWrite_DefaultSeam(t *testing.T) {
	t.Run("rename fails", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "tasks.md")
		// A folder that is not empty: the rename of a file onto it fails.
		writeFile(t, filepath.Join(target, "keep.md"), "kept\n")

		err := openspecTasksWrite(target, []byte("- [x] a\n"))
		if err == nil || !strings.Contains(err.Error(), "fsx: rename temp file into place for "+target) {
			t.Fatalf("err = %v, want the rename error", err)
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.Contains(e.Name(), ".tmp-") {
				t.Errorf("temp file %s left behind", e.Name())
			}
		}
		if got := osReadFile(t, filepath.Join(target, "keep.md")); got != "kept\n" {
			t.Errorf("target content changed to %q", got)
		}
	})
	t.Run("stat fails", func(t *testing.T) {
		dir := t.TempDir()
		parent := filepath.Join(dir, "file")
		writeFile(t, parent, "x")
		err := openspecTasksWrite(filepath.Join(parent, "tasks.md"), []byte("- [x] a\n"))
		if !errors.Is(err, syscall.ENOTDIR) {
			t.Fatalf("err = %v, want the ENOTDIR stat error", err)
		}
	})
	t.Run("missing file takes 0644", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "tasks.md")
		if err := openspecTasksWrite(target, []byte("- [x] a\n")); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o644 {
			t.Errorf("stat = %v, %v, want mode 0644", info, err)
		}
	})
}

// TestShipCleanupOpenspecReport_StatSplit checks that the run-end report
// reads a missing folder, a path under a file and a link loop as not active,
// and gives a warning only for another stat error.
func TestShipCleanupOpenspecReport_StatSplit(t *testing.T) {
	data := func() map[string]any { return map[string]any{"flags": osNamed("add-widget")} }
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for name, statErr := range map[string]error{
		"missing":   &os.PathError{Op: "stat", Path: "p", Err: syscall.ENOENT},
		"not a dir": &os.PathError{Op: "stat", Path: "p", Err: syscall.ENOTDIR},
		"link loop": &os.PathError{Op: "stat", Path: "p", Err: syscall.ELOOP},
	} {
		t.Run(name, func(t *testing.T) {
			prev := shipOpenspecStatFunc
			shipOpenspecStatFunc = func(string) (os.FileInfo, error) { return nil, statErr }
			t.Cleanup(func() { shipOpenspecStatFunc = prev })
			report, warning := shipCleanupOpenspecReport(t.TempDir(), data(), now)
			want := map[string]any{"change": "add-widget", "stillActive": false, "path": "openspec/changes/add-widget/"}
			if warning != "" || !reflect.DeepEqual(report, want) {
				t.Errorf("report = %v, warning = %q, want %v and no warning", report, warning, want)
			}
		})
	}
	t.Run("denied", func(t *testing.T) {
		prev := shipOpenspecStatFunc
		shipOpenspecStatFunc = func(p string) (os.FileInfo, error) { return nil, osDenied("stat", "p") }
		t.Cleanup(func() { shipOpenspecStatFunc = prev })
		report, warning := shipCleanupOpenspecReport(t.TempDir(), data(), now)
		if report != nil || warning != "OpenSpec report skipped: stat openspec/changes/add-widget/: stat p: permission denied" {
			t.Errorf("report = %v, warning = %q", report, warning)
		}
	})
}
