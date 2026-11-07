package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
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
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "openspecBaseHead is absent") {
		t.Errorf("warnings = %v, want one absent-base warning", out.Warnings)
	}
	osAssertFailed(t, path, "verify-openspec", "openspec validate add-widget --strict failed")
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
	}
	for name, base := range cases {
		t.Run(name, func(t *testing.T) {
			dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
			osWriteChange(t, dir, "add-widget", true, "widget")
			osSetStateKeys(t, path, map[string]any{"openspecBaseHead": base})
			osStubValidate(t, "add-widget", "bad", 1)
			prev := shipCommitCheckGit
			shipCommitCheckGit = func(d string, args ...string) (string, error) {
				t.Errorf("git called with %v, want no git call", args)
				return "", nil
			}
			t.Cleanup(func() { shipCommitCheckGit = prev })

			out, err := osCheck(t, dir, branch, "verify-openspec", nil)
			if err != nil {
				t.Fatalf("openspec-check: %v", err)
			}
			if out.Outcome != "invalid" || len(out.Overlaps) != 0 {
				t.Errorf("out = %+v, want invalid with no overlaps", out)
			}
			if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "not a full commit sha") {
				t.Errorf("warnings = %v, want one malformed-base warning", out.Warnings)
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
}

func TestShipStateOpenspecCheck_ValidateOutputCut(t *testing.T) {
	marker := "[validate output cut at 16384 bytes]"
	for _, tc := range []struct {
		name string
		size int
		cut  bool
	}{{"exact limit", 16384, false}, {"one over", 16385, true}} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
			osWriteChange(t, dir, "add-widget", true, "widget")
			body := strings.Repeat("x", tc.size)
			osStubValidate(t, "add-widget", body, 0)

			out, err := osCheck(t, dir, branch, "verify-openspec", nil)
			if err != nil {
				t.Fatalf("openspec-check: %v", err)
			}
			want := body
			if tc.cut {
				want = body[:16384] + marker
			}
			if out.ValidateOutput != want {
				t.Errorf("validateOutput: len = %d, want %d (cut = %v)", len(out.ValidateOutput), len(want), tc.cut)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Failures of the write and of the CLI
// ---------------------------------------------------------------------------

func TestShipStateOpenspecCheck_FailRowWriteFails(t *testing.T) {
	dir, path, branch := osCheckFixture(t, "verify-openspec", osNamed("add-widget"))
	prev := shipStateWriteFunc
	shipStateWriteFunc = func(*state.State) error { return errors.New("disk full") }
	t.Cleanup(func() { shipStateWriteFunc = prev })

	_, err := osCheck(t, dir, branch, "verify-openspec", nil) // missing-on-disk: fail row
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %v, want InfraError", err)
	}
	if ie.Msg != "openspec-check: write ship state: disk full" ||
		ie.Suggestion != "Fix write access to .sdlc-v2/runs/, then call openspec-check again." {
		t.Errorf("msg = %q, suggestion = %q", ie.Msg, ie.Suggestion)
	}
	osAssertOpen(t, path, "verify-openspec")
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
	if ie.Suggestion != "Install the openspec CLI, then call openspec-check again." {
		t.Errorf("suggestion = %q", ie.Suggestion)
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
				t.Skipf("symlink: %v", err)
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
