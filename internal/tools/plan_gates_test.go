package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/discovery"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// ---------------------------------------------------------------------------
// plan_gates fixtures
// ---------------------------------------------------------------------------

// gateTask renders one plan task. Empty fields take a passing default; "-"
// omits the line or block.
type gateTask struct {
	Num        int
	Complexity string   // default Standard
	Depends    string   // default none
	Verify     string   // default tests; "-" omits the line
	Files      []string // bullet text without the leading "- "
	Contract   string   // Contract block body; default a fenced code block; "-" omits the block
}

const gateDefaultContract = "- shape (code):\n  ```go\n  func X()\n  ```\n- names: X\n"

func (g gateTask) render() string {
	var b strings.Builder
	or := func(v, def string) string {
		if v == "" {
			return def
		}
		return v
	}
	fmt.Fprintf(&b, "### Task %d: Task %d\n\n", g.Num, g.Num)
	fmt.Fprintf(&b, "**Complexity:** %s\n", or(g.Complexity, "Standard"))
	b.WriteString("**Risk:** Low\n")
	fmt.Fprintf(&b, "**Depends on:** %s\n", or(g.Depends, "none"))
	if g.Verify != "-" {
		fmt.Fprintf(&b, "**Verify:** %s\n", or(g.Verify, "tests"))
	}
	b.WriteString("\n**Files:**\n")
	for _, f := range g.Files {
		b.WriteString("- " + f + "\n")
	}
	if g.Contract != "-" {
		b.WriteString("\n**Contract:**\n" + or(g.Contract, gateDefaultContract))
	}
	b.WriteString("\n**Acceptance criteria:**\n- [ ] works\n\n")
	return b.String()
}

func gatePlan(tasks ...gateTask) string {
	var b strings.Builder
	b.WriteString("# Plan\n\n**Goal:** g\n**Architecture:** a\n**Source:** s\n**Verification:** v\n\n## Deviations & assumptions\n\nnone\n\n## Tasks\n\n")
	for _, t := range tasks {
		b.WriteString(t.render())
	}
	return b.String()
}

// runGates writes plan to root/plan.md and runs plan_gates against root.
func runGates(t *testing.T, root, plan string) []discovery.Finding {
	t.Helper()
	writeFile(t, filepath.Join(root, "plan.md"), plan)
	out, err := validate(root, ValidateIn{Action: "plan_gates", File: "plan.md"})
	if err != nil {
		t.Fatalf("validate plan_gates: %v", err)
	}
	return out.Findings
}

func gateMessages(findings []discovery.Finding, id string) []string {
	var msgs []string
	for _, f := range findingsByID(findings, id) {
		msgs = append(msgs, f.Message)
	}
	return msgs
}

func wantGateMessages(t *testing.T, findings []discovery.Finding, id string, want ...string) {
	t.Helper()
	got := gateMessages(findings, id)
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s findings = %q, want %q\nall findings: %+v", id, got, want, findings)
	}
}

func bt(p string) string { return "`" + p + "`" }

// ---------------------------------------------------------------------------
// G3 and G12
// ---------------------------------------------------------------------------

func TestPlanGates_G3DependencyOrder(t *testing.T) {
	cases := []struct {
		name string
		dep  string
		want []string
	}{
		{"earlier task passes", "Task 1", nil},
		{"later task fails", "Task 3", []string{"Task 2: depends on Task 3, which is not listed before it"}},
		{"same task fails", "Task 2", []string{"Task 2: depends on itself"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := runGates(t, t.TempDir(), gatePlan(gateTask{Num: 1}, gateTask{Num: 2, Depends: tc.dep}, gateTask{Num: 3}))
			wantGateMessages(t, f, "G3", tc.want...)
			wantGateMessages(t, f, "G12")
		})
	}
}

func TestPlanGates_G12NonexistentTarget(t *testing.T) {
	f := runGates(t, t.TempDir(), gatePlan(gateTask{Num: 1}, gateTask{Num: 2, Depends: "Task 9"}))
	wantGateMessages(t, f, "G12", "Task 2: depends on nonexistent Task 9")
	wantGateMessages(t, f, "G3")

	f = runGates(t, t.TempDir(), gatePlan(gateTask{Num: 1}, gateTask{Num: 2, Depends: "Task 1"}))
	wantGateMessages(t, f, "G12")
}

// ---------------------------------------------------------------------------
// G4
// ---------------------------------------------------------------------------

func TestPlanGates_G4SharedModifyPath(t *testing.T) {
	t.Run("dependency chain passes", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "a.go"), "package a\n")
		f := runGates(t, root, gatePlan(
			gateTask{Num: 1, Files: []string{"Modify: " + bt("a.go")}},
			gateTask{Num: 2, Depends: "Task 1"},
			gateTask{Num: 3, Depends: "Task 2", Files: []string{"Modify: " + bt("a.go")}},
		))
		wantGateMessages(t, f, "G4")
	})
	t.Run("no dependency fails", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "a.go"), "package a\n")
		f := runGates(t, root, gatePlan(
			gateTask{Num: 1, Files: []string{"Modify: " + bt("a.go")}},
			gateTask{Num: 2, Files: []string{"Modify: " + bt("a.go")}},
		))
		wantGateMessages(t, f, "G4", "Task 1 and Task 2 both modify a.go with no dependency path between them")
	})
	t.Run("three tasks, one dependency: each unlinked pair is reported", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "a.go"), "package a\n")
		f := runGates(t, root, gatePlan(
			gateTask{Num: 1, Files: []string{"Modify: " + bt("a.go")}},
			gateTask{Num: 2, Depends: "Task 1", Files: []string{"Modify: " + bt("a.go")}},
			gateTask{Num: 3, Files: []string{"Modify: " + bt("a.go")}},
		))
		wantGateMessages(t, f, "G4",
			"Task 1 and Task 3 both modify a.go with no dependency path between them",
			"Task 2 and Task 3 both modify a.go with no dependency path between them",
		)
	})
	t.Run("two shared paths are each reported", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "a.go"), "package a\n")
		writeFile(t, filepath.Join(root, "b.go"), "package b\n")
		f := runGates(t, root, gatePlan(
			gateTask{Num: 1, Files: []string{"Modify: " + bt("b.go"), "Modify: " + bt("a.go")}},
			gateTask{Num: 2, Files: []string{"Modify: " + bt("a.go"), "Modify: " + bt("b.go")}},
		))
		wantGateMessages(t, f, "G4",
			"Task 1 and Task 2 both modify a.go with no dependency path between them",
			"Task 1 and Task 2 both modify b.go with no dependency path between them",
		)
	})
	t.Run("one task listing a path twice is not a pair", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "a.go"), "package a\n")
		f := runGates(t, root, gatePlan(
			gateTask{Num: 1, Files: []string{"Modify: " + bt("a.go"), "Modify: " + bt("./a.go")}},
		))
		wantGateMessages(t, f, "G4")
	})
	t.Run("dot slash path is the same path", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "a.go"), "package a\n")
		f := runGates(t, root, gatePlan(
			gateTask{Num: 1, Files: []string{"Modify: " + bt("./a.go")}},
			gateTask{Num: 2, Files: []string{"Modify: " + bt("a.go")}},
		))
		wantGateMessages(t, f, "G4", "Task 1 and Task 2 both modify a.go with no dependency path between them")
	})
}

func TestPlanGates_TaskDependsOnPath(t *testing.T) {
	deps := map[int][]int{3: {2}, 2: {1}, 4: {4}}
	if !taskDependsOnPath(deps, 3, 1) {
		t.Error("3 -> 2 -> 1 must be a path")
	}
	if taskDependsOnPath(deps, 1, 3) {
		t.Error("1 has no dependency, so no path leads from 1 to 3")
	}
	if taskDependsOnPath(deps, 4, 1) {
		t.Error("a self loop must not reach another task")
	}
}

// ---------------------------------------------------------------------------
// G8
// ---------------------------------------------------------------------------

func TestPlanGates_G8VerifyField(t *testing.T) {
	cases := []struct {
		name   string
		verify string
		fail   bool
	}{
		{"absent", "-", true},
		{"empty", " ", true},
		{"invalid value", "flaky", true},
		{"valid value", "tests (go test ./x/ -run TestX), build", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// " " renders "**Verify:**" with no value: the next line is
			// the **Files:** label, so the field is present but empty.
			plan := gatePlan(gateTask{Num: 1, Verify: tc.verify})
			f := runGates(t, t.TempDir(), plan)
			got := gateMessages(f, "G8")
			if !tc.fail {
				wantGateMessages(t, f, "G8")
				return
			}
			if len(got) != 1 {
				t.Fatalf("G8 findings = %q, want 1", got)
			}
			pf3 := checkPF3(extractTasks(plan))
			if pf3.status != "fail" || !strings.Contains(pf3.message, "- "+got[0]+"\n") && !strings.HasSuffix(pf3.message, "- "+got[0]) {
				t.Fatalf("G8 message %q is not a PF3 line:\n%s", got[0], pf3.message)
			}
			if f := findingsByID(f, "G8")[0]; f.Severity != "warning" {
				t.Errorf("G8 severity = %q, want warning", f.Severity)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// G9
// ---------------------------------------------------------------------------

func TestPlanGates_G9TaskSize(t *testing.T) {
	files := func(n int) []string {
		var out []string
		for i := 1; i <= n; i++ {
			out = append(out, "Create: "+bt(fmt.Sprintf("f%d.go", i)))
		}
		return out
	}
	f := runGates(t, t.TempDir(), gatePlan(gateTask{Num: 1, Files: files(planGatesMaxTaskFiles)}))
	wantGateMessages(t, f, "G9")

	over := planGatesMaxTaskFiles + 1
	f = runGates(t, t.TempDir(), gatePlan(gateTask{Num: 1, Files: files(over)}))
	wantGateMessages(t, f, "G9", fmt.Sprintf("Task 1: touches %d files (limit %d)", over, planGatesMaxTaskFiles))
	if got := findingsByID(f, "G9")[0].Severity; got != "warning" {
		t.Errorf("G9 severity = %q, want warning", got)
	}

	// One path under Create and Modify counts once: the limit counts
	// distinct paths.
	atLimit := append(files(planGatesMaxTaskFiles), "Modify: "+bt("f1.go"), "Test: "+bt("./f2.go"))
	f = runGates(t, t.TempDir(), gatePlan(gateTask{Num: 1, Files: atLimit}))
	wantGateMessages(t, f, "G9")
}

func TestPlanGates_G9TrivialShare(t *testing.T) {
	const total = 10
	tasks := func(trivial int) []gateTask {
		var out []gateTask
		for i := 1; i <= total; i++ {
			c := "Standard"
			if i <= trivial {
				c = "Trivial"
			}
			out = append(out, gateTask{Num: i, Complexity: c})
		}
		return out
	}
	// The largest Trivial count whose share is not above the limit passes;
	// one more fails.
	atLimit := int(planGatesMaxTrivialShare * total)
	f := runGates(t, t.TempDir(), gatePlan(tasks(atLimit)...))
	wantGateMessages(t, f, "G9")

	f = runGates(t, t.TempDir(), gatePlan(tasks(atLimit+1)...))
	wantGateMessages(t, f, "G9", fmt.Sprintf("Plan: %d of %d tasks are Trivial (limit %d%%)", atLimit+1, total, int(planGatesMaxTrivialShare*100)))
}

// ---------------------------------------------------------------------------
// G10
// ---------------------------------------------------------------------------

func TestPlanGates_G10ModifyPaths(t *testing.T) {
	t.Run("created by an earlier task passes", func(t *testing.T) {
		f := runGates(t, t.TempDir(), gatePlan(
			gateTask{Num: 1, Files: []string{"Create: " + bt("new.go")}},
			gateTask{Num: 2, Depends: "Task 1", Files: []string{"Modify: " + bt("new.go")}},
		))
		wantGateMessages(t, f, "G10")
	})
	t.Run("created by a later task fails", func(t *testing.T) {
		f := runGates(t, t.TempDir(), gatePlan(
			gateTask{Num: 1},
			gateTask{Num: 2, Files: []string{"Modify: " + bt("new.go")}},
			gateTask{Num: 3, Files: []string{"Create: " + bt("new.go")}},
		))
		wantGateMessages(t, f, "G10", "Task 2: Modify path new.go does not exist")
	})
	cases := []struct {
		name string
		path string
		want string
	}{
		{"present file", "here.go", ""},
		{"folder", "sub/", "Task 1: Modify path sub is not a regular file"},
		{"parent escape", "../x.go", "Task 1: Modify path ../x.go is outside the project"},
		{"absolute", "/etc/hosts", "Task 1: Modify path /etc/hosts is outside the project"},
		{"absent", "gone.go", "Task 1: Modify path gone.go does not exist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "here.go"), "package x\n")
			if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			f := runGates(t, root, gatePlan(gateTask{Num: 1, Files: []string{"Modify: " + bt(tc.path) + " — change"}}))
			if tc.want == "" {
				wantGateMessages(t, f, "G10")
				return
			}
			wantGateMessages(t, f, "G10", tc.want)
			if got := findingsByID(f, "G10")[0]; got.Severity != "error" || got.Fix != "" {
				t.Errorf("G10 finding = %+v, want severity error and no fix", got)
			}
		})
	}
}

func TestPlanGates_G10ActiveRootOnly(t *testing.T) {
	mainDir, activeDir, planDir := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(activeDir, "only_active.go"), "package x\n")
	planPath := filepath.Join(planDir, "plan.md")
	writeFile(t, planPath, gatePlan(gateTask{Num: 1, Files: []string{"Modify: " + bt("only_active.go")}}))

	in := ValidateIn{Action: "plan_gates", File: planPath}
	root, err := validateRoot(mainDir, in, func() (string, error) { return activeDir, nil })
	if err != nil {
		t.Fatalf("validateRoot: %v", err)
	}
	out, err := validate(root, in)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	wantGateMessages(t, out.Findings, "G10")

	// The same plan against the main root reports the path: the active root
	// is what made the check pass.
	out, err = validate(mainDir, in)
	if err != nil {
		t.Fatalf("validate main: %v", err)
	}
	wantGateMessages(t, out.Findings, "G10", "Task 1: Modify path only_active.go does not exist")
}

// A Modify path whose parent is a file is a mistake in the plan text, so it
// is a G10 finding, and the other gates still report.
func TestPlanGates_G10ParentIsFileIsFinding(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "x")
	f := runGates(t, root, gatePlan(
		gateTask{Num: 1, Verify: "-", Files: []string{"Modify: " + bt("a.txt/x.go")}},
	))
	wantGateMessages(t, f, "G10", "Task 1: Modify path a.txt/x.go cannot be checked: a parent in the path is a file, not a folder")
	if got := findingsByID(f, "G10")[0].Severity; got != "error" {
		t.Errorf("G10 severity = %q, want error", got)
	}
	if len(findingsByID(f, "G8")) != 1 {
		t.Errorf("G8 must still report next to the G10 finding: %+v", f)
	}
}

func TestPlanGates_G10PathTooLongIsFinding(t *testing.T) {
	long := strings.Repeat("n", 300) + ".go"
	f := runGates(t, t.TempDir(), gatePlan(gateTask{Num: 1, Files: []string{"Modify: " + bt(long)}}))
	wantGateMessages(t, f, "G10", "Task 1: Modify path "+long+" cannot be checked: the path or one of its names is too long")
}

// A Modify path that cannot be checked for a reason the plan text does not
// cause (here: no search permission on a folder) is an InfraError that keeps
// the OS error as its cause.
func TestPlanGates_G10PermissionDeniedIsInfraError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores folder permissions")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	writeFile(t, filepath.Join(locked, "x.go"), "package x\n")
	writeFile(t, filepath.Join(root, "plan.md"), gatePlan(gateTask{Num: 1, Files: []string{"Modify: " + bt("locked/x.go")}}))
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	_, err := validate(root, ValidateIn{Action: "plan_gates", File: "plan.md"})
	var infra *mcpserver.InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("err = %v (%T), want *mcpserver.InfraError", err, err)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("err = %v, want the permission error as its cause", err)
	}
	var pe *fs.PathError
	if !errors.As(infra.Cause, &pe) {
		t.Errorf("Cause = %T, want *fs.PathError", infra.Cause)
	}
	if !strings.HasPrefix(infra.Msg, "plan_gates: cannot check Modify path locked/x.go: ") {
		t.Errorf("Msg = %q", infra.Msg)
	}
	wantSug := "Give this process read and search permission on each folder of locked/x.go (ls -ld on each parent shows it). Then retry plan_gates."
	if infra.Suggestion != wantSug {
		t.Errorf("Suggestion = %q, want %q", infra.Suggestion, wantSug)
	}
}

// Create is registered after the Modify loop of a task, so a task that
// creates and modifies one path reports the Modify path as absent.
func TestPlanGates_G10CreateAndModifyInOneTask(t *testing.T) {
	f := runGates(t, t.TempDir(), gatePlan(gateTask{Num: 1, Files: []string{"Create: " + bt("new.go"), "Modify: " + bt("new.go")}}))
	wantGateMessages(t, f, "G10", "Task 1: Modify path new.go does not exist")
}

func TestPlanGates_G10UnreadableFiles(t *testing.T) {
	plan := gatePlan(
		gateTask{Num: 1, Files: []string{"Modify: " + bt("Makefile") + " - add a target", "Modify: the helpers", "Test: run " + bt("go test ./...")}},
		gateTask{Num: 2, Files: []string{"Modify: " + bt("./Makefile")}},
	)
	plan += "### Task 3: Task 3\n\n**Complexity:** Standard\n**Risk:** Low\n**Depends on:** none\n**Verify:** tests\n\n**Acceptance criteria:**\n- [ ] works\n"
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Makefile"), "all:\n")
	f := runGates(t, root, plan)
	wantGateMessages(t, f, "G10",
		`Task 1: Files bullet "Modify: `+"`Makefile`"+` - add a target" names no path, so G4, G9 and G10 skip it. Write it as `+taskFileShape,
		`Task 1: Files bullet "Modify: the helpers" names no path, so G4, G9 and G10 skip it. Write it as `+taskFileShape,
		`Task 1: Files bullet "Test: run `+"`go test ./...`"+`" names no path, so G4, G9 and G10 skip it. Write it as `+taskFileShape,
		"Task 3: no **Files:** block, so G4, G9 and G10 do not check this task",
	)
	for _, x := range findingsByID(f, "G10") {
		if x.Severity != "warning" {
			t.Errorf("G10 finding %q: severity = %q, want warning", x.Message, x.Severity)
		}
	}
}

// ---------------------------------------------------------------------------
// G18 floor
// ---------------------------------------------------------------------------

func TestPlanGates_G18ContractVisual(t *testing.T) {
	cases := []struct {
		name     string
		contract string
		fail     bool
	}{
		{"indented fence", "- shape (code):\n    ```go\n    func X()\n    ```\n", false},
		{"indented table", "- shape (code):\n  | a | b |\n  |---|---|\n  | 1 | 2 |\n", false},
		{"mermaid block", "- shape:\n```mermaid\nflowchart LR\n  A --> B\n```\n", false},
		{"prose only", "- shape (code): a function that does X\n- names: X\n", true},
		{"fenced column-0 label then a table", "- shape:\n```text\n**Note:** not a field\n```\n| a |\n|---|\n", false},
		{"no contract", "-", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := runGates(t, t.TempDir(), gatePlan(gateTask{Num: 1, Contract: tc.contract}, gateTask{Num: 2}))
			if tc.fail {
				wantGateMessages(t, f, "G18", "Task 1: Contract has no code block, table or diagram")
				return
			}
			wantGateMessages(t, f, "G18")
		})
	}
}

func TestPlanGates_G18LastTaskStopsAtSection(t *testing.T) {
	plan := gatePlan(gateTask{Num: 1}, gateTask{Num: 2, Contract: "- shape (code): prose only\n"}) +
		"## Verification Scorecard\n\n| Check | Result |\n|---|---|\n| a | b |\n"
	f := runGates(t, t.TempDir(), plan)
	wantGateMessages(t, f, "G18", "Task 2: Contract has no code block, table or diagram")
}

func TestPlanGates_ContractBlockEnd(t *testing.T) {
	body := "### Task 1: x\n**Contract:**\n- shape: prose\n**Acceptance criteria:**\n| a |\n|---|\n"
	if contractHasVisual(body) {
		t.Error("the table after **Acceptance criteria:** is outside the Contract block")
	}
	if _, found := contractBlock("### Task 1: x\n**Files:**\n"); found {
		t.Error("a body with no **Contract:** line has no Contract block")
	}
}

func TestPlanGates_RawTaskBodies_UnclosedFenceAndRepeat(t *testing.T) {
	// An unclosed fence is not a fence, so the heading after it counts.
	got := rawTaskBodies("### Task 1: a\n```\nbody\n### Task 2: b\nbody2\n")
	if len(got) != 2 || !strings.HasPrefix(got[2], "### Task 2: b") {
		t.Fatalf("bodies = %q, want task 2 found after the unclosed fence", got)
	}
	// A repeated task number keeps its first body.
	got = rawTaskBodies("### Task 1: first\nA\n### Task 1: second\nB\n")
	if len(got) != 1 || !strings.Contains(got[1], "first") || strings.Contains(got[1], "second") {
		t.Fatalf("bodies = %q, want the first body of task 1", got)
	}
}

// With two "### Task 1" headings, the G18 floor reads only the first body
// and reports task 1 at most once.
func TestPlanGates_G18RepeatedTaskNumber(t *testing.T) {
	plan := gatePlan(gateTask{Num: 1}, gateTask{Num: 1, Contract: "- shape: prose only\n"})
	wantGateMessages(t, runGates(t, t.TempDir(), plan), "G18")

	plan = gatePlan(gateTask{Num: 1, Contract: "- shape: prose only\n"}, gateTask{Num: 1, Contract: "- shape: prose only\n"})
	wantGateMessages(t, runGates(t, t.TempDir(), plan), "G18", "Task 1: Contract has no code block, table or diagram")
}

func TestPlanGates_RawTaskBodies(t *testing.T) {
	content := "# P\n\n### Task 1: a\nbody1\n```\n### Task 9: inside a fence\n## not a section\n```\n### Task 2: b\nbody2\n## Verification Scorecard\ntail\n"
	got := rawTaskBodies(content)
	if len(got) != 2 {
		t.Fatalf("bodies = %q, want tasks 1 and 2 only", got)
	}
	if !strings.Contains(got[1], "### Task 9: inside a fence") || !strings.Contains(got[1], "## not a section") {
		t.Errorf("task 1 body must keep its fence: %q", got[1])
	}
	if strings.Contains(got[2], "tail") || strings.Contains(got[2], "Verification Scorecard") {
		t.Errorf("task 2 body must stop at the ## section: %q", got[2])
	}
}

// ---------------------------------------------------------------------------
// whole-plan behavior
// ---------------------------------------------------------------------------

func gateMixedPlan() string {
	return gatePlan(
		gateTask{Num: 1, Verify: "-", Files: []string{"Modify: " + bt("gone.go")}},
		gateTask{Num: 2, Depends: "Task 3, Task 9", Contract: "- shape: prose\n"},
		gateTask{Num: 3, Complexity: "Trivial"},
	)
}

func TestPlanGates_SortOrderAndIDs(t *testing.T) {
	f := runGates(t, t.TempDir(), gateMixedPlan())
	var ids []string
	for _, x := range f {
		ids = append(ids, x.ID)
		if !strings.HasSuffix(x.Path, "plan.md") {
			t.Errorf("finding %+v must carry the plan path", x)
		}
	}
	want := []string{"G3", "G8", "G10", "G12", "G18"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v\n%+v", ids, want, f)
	}
}

func TestPlanGates_NoTrailingNewline(t *testing.T) {
	plan := gateMixedPlan()
	with := runGates(t, t.TempDir(), plan)
	without := runGates(t, t.TempDir(), strings.TrimRight(plan, "\n"))
	strip := func(fs []discovery.Finding) []string {
		var out []string
		for _, f := range fs {
			out = append(out, f.ID+" "+f.Message)
		}
		return out
	}
	if !reflect.DeepEqual(strip(with), strip(without)) {
		t.Fatalf("findings differ:\nwith:    %q\nwithout: %q", strip(with), strip(without))
	}
}

// A file with no task heading checks no gate, so it is an error, not a
// clean pass.
func TestPlanGates_NotAPlanIsDomainError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "plan.md"), "# Notes\n\nno task heading here\n```\n### Task 1: in a fence\n```\n")
	_, err := validate(root, ValidateIn{Action: "plan_gates", File: "plan.md"})
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v (%T), want DomainError", err, err)
	}
	if !strings.HasPrefix(de.Msg, `plan_gates: no "### Task N: <title>" heading outside a code fence, so no gate was checked: `) {
		t.Errorf("Msg = %q", de.Msg)
	}
	if !strings.Contains(de.Suggestion, "Then retry plan_gates.") {
		t.Errorf("Suggestion = %q", de.Suggestion)
	}
}

func TestPlanGates_GateIDList(t *testing.T) {
	want := []string{"G3", "G4", "G8", "G9", "G10", "G12"}
	if !reflect.DeepEqual(planGatesGateIDs, want) {
		t.Fatalf("planGatesGateIDs = %v, want %v", planGatesGateIDs, want)
	}
}

func TestPlanGates_NotInBlockingChecks(t *testing.T) {
	for _, c := range planBlockingChecks(t.TempDir(), gateMixedPlan()) {
		if strings.HasPrefix(c.id, "G") {
			t.Errorf("planBlockingChecks holds gate check %s; plan_gates must stay out of the edit hook", c.id)
		}
	}
}

// ---------------------------------------------------------------------------
// Files parser
// ---------------------------------------------------------------------------

func TestPlanGates_ParseTaskFiles(t *testing.T) {
	body := "### Task 1: x\n\n**Files:**\n- Create: `new.go` — a `Foo` note\n- Modify: `./pkg/a.go`\n- Test: `pkg/a_test.go`\n- `loose.go` — no kind word\n- Delete: `old.go`\n- Modify: the `Foo` func in `b.go`\n- Modify: `internal/dir/`\n\n**Contract:**\n- `notafile.go`\n"
	got, unparsed, found := parseTaskFiles(body)
	if !found || len(unparsed) != 0 {
		t.Fatalf("found = %v, unparsed = %q, want a block with no unparsed bullet", found, unparsed)
	}
	want := []taskFile{
		{"Create", "new.go"},
		{"Modify", "pkg/a.go"},
		{"Test", "pkg/a_test.go"},
		{"", "loose.go"},
		{"", "old.go"},
		{"Modify", "b.go"},
		{"Modify", "internal/dir"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTaskFiles = %+v, want %+v", got, want)
	}
	wantPaths := []string{"new.go", "pkg/a.go", "pkg/a_test.go", "loose.go", "old.go", "b.go", "internal/dir"}
	if paths := waveComputeParseFiles(body); !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("waveComputeParseFiles = %v, want %v", paths, wantPaths)
	}
}

// The first path with an extension wins over an earlier slash word, so a
// bullet yields the same path that wave-compute read before the parser was
// shared. A slash path is used only when the bullet has no extension path.
func TestPlanGates_ParseTaskFiles_ExtensionPathFirst(t *testing.T) {
	body := "**Files:**\n- Modify: the `read/write` helper in `a.go`\n- Create: `docs/` — holds `b.md`\n- Modify: `./Makefile`\n- Modify: `Makefile`\n- see `notes`\n"
	got, unparsed, found := parseTaskFiles(body)
	want := []taskFile{{"Modify", "a.go"}, {"Create", "b.md"}, {"Modify", "Makefile"}}
	if !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTaskFiles = %+v (found %v), want %+v", got, found, want)
	}
	if wantUnparsed := []string{"Modify: `Makefile`"}; !reflect.DeepEqual(unparsed, wantUnparsed) {
		t.Errorf("unparsed = %q, want %q (a bullet with no kind word is not reported)", unparsed, wantUnparsed)
	}
	if _, _, found := parseTaskFiles("### Task 1: x\n**Verify:** tests\n"); found {
		t.Error("a body with no **Files:** line must report found = false")
	}
}

// ---------------------------------------------------------------------------
// input states: parity with plan_format, read limit, root
// ---------------------------------------------------------------------------

func TestPlanGates_InputStateParity(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "real.md"), gatePlan(gateTask{Num: 1}))
	if err := os.Symlink(filepath.Join(root, "real.md"), filepath.Join(root, "link.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "notes.md"), "# Notes\n\nno task heading\n")

	errRows := []struct {
		name string
		file string
		msg  func(action string) string
	}{
		{"file empty", "", func(a string) string { return a + ": file is required" }},
		{"path absent", "absent.md", func(a string) string { return a + ": file not found: " + filepath.Join(root, "absent.md") }},
		{"path is a folder", "folder", func(a string) string { return a + ": cannot read file: " }},
	}
	for _, row := range errRows {
		for _, action := range []string{"plan_format", "plan_gates"} {
			t.Run(row.name+"/"+action, func(t *testing.T) {
				_, err := validate(root, ValidateIn{Action: action, File: row.file})
				var de *mcpserver.DomainError
				if !errors.As(err, &de) {
					t.Fatalf("err = %v (%T), want DomainError", err, err)
				}
				if !strings.HasPrefix(de.Msg, row.msg(action)) {
					t.Fatalf("Msg = %q, want prefix %q", de.Msg, row.msg(action))
				}
				if de.Suggestion == "" {
					t.Error("DomainError must carry a Suggestion")
				}
			})
		}
	}

	for _, action := range []string{"plan_format", "plan_gates"} {
		t.Run("symlink is followed/"+action, func(t *testing.T) {
			if _, err := validate(root, ValidateIn{Action: action, File: "link.md"}); err != nil {
				t.Fatalf("validate %s via symlink: %v", action, err)
			}
		})
	}

	t.Run("not a plan", func(t *testing.T) {
		pf, err := validate(root, ValidateIn{Action: "plan_format", File: "notes.md"})
		if err != nil || len(findingsByID(pf.Findings, "PF2")) == 0 {
			t.Fatalf("plan_format must report PF2 for a file with no task heading: err=%v findings=%+v", err, pf.Findings)
		}
		var de *mcpserver.DomainError
		if _, err := validate(root, ValidateIn{Action: "plan_gates", File: "notes.md"}); !errors.As(err, &de) {
			t.Fatalf("plan_gates must return a DomainError for a file with no task heading: err=%v", err)
		}
	})

	t.Run("not-found hint names the root of the action", func(t *testing.T) {
		for action, want := range map[string]string{
			"plan_format": "A relative path resolves against the main worktree root.",
			"plan_gates":  "A relative path resolves against the active worktree root (the checkout of the current branch).",
		} {
			_, err := validate(root, ValidateIn{Action: action, File: "absent.md"})
			var de *mcpserver.DomainError
			if !errors.As(err, &de) || !strings.HasSuffix(de.Suggestion, want) {
				t.Errorf("%s: err = %v, want a Suggestion ending %q", action, err, want)
			}
		}
	})
}

func TestPlanGates_ReadLimit(t *testing.T) {
	// A task heading, so plan_gates checks the file and does not refuse it.
	const header = "### Task 1: t\n"
	line := strings.Repeat("x", 1023) + "\n"
	body := strings.Repeat(line, (maxPlanFileBytes-len(header))/len(line))
	exact := header + body + strings.Repeat("x", maxPlanFileBytes-len(header)-len(body))
	if len(exact) != maxPlanFileBytes {
		t.Fatalf("fixture is %d bytes, want %d", len(exact), maxPlanFileBytes)
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "exact.md"), exact)
	writeFile(t, filepath.Join(root, "over.md"), exact+"x")

	for _, action := range []string{"plan_format", "plan_style", "plan_gates"} {
		t.Run(action, func(t *testing.T) {
			if _, err := validate(root, ValidateIn{Action: action, File: "exact.md"}); err != nil {
				t.Fatalf("%s on an 8 MiB file: %v", action, err)
			}
			_, err := validate(root, ValidateIn{Action: action, File: "over.md"})
			var de *mcpserver.DomainError
			if !errors.As(err, &de) {
				t.Fatalf("err = %v (%T), want DomainError", err, err)
			}
			want := fmt.Sprintf("%s: file is %d bytes, over the %s limit (%d bytes): %s", action, maxPlanFileBytes+1, maxPlanFileText, maxPlanFileBytes, filepath.Join(root, "over.md"))
			if de.Msg != want {
				t.Errorf("Msg = %q, want %q", de.Msg, want)
			}
			if de.Suggestion == "" {
				t.Error("DomainError must carry a Suggestion")
			}
		})
	}
}

func TestPlanGates_ActionWiring(t *testing.T) {
	typ := reflect.TypeOf(ValidateIn{})
	f, _ := typ.FieldByName("Action")
	if !strings.Contains(f.Tag.Get("jsonschema"), "enum=plan_gates") || !strings.Contains(f.Tag.Get("jsonschema_description"), "plan_gates") {
		t.Errorf("Action tag does not name plan_gates: %q", f.Tag)
	}
	f, _ = typ.FieldByName("File")
	if !strings.Contains(f.Tag.Get("jsonschema_description"), "plan_format, plan_style and plan_gates") {
		t.Errorf("File description does not name plan_gates: %q", f.Tag.Get("jsonschema_description"))
	}
	_, err := validate(t.TempDir(), ValidateIn{Action: "nope"})
	var de *mcpserver.DomainError
	if !errors.As(err, &de) || !strings.Contains(de.Suggestion, "worktree_anchoring, plan_gates") {
		t.Errorf("unknown action error must list plan_gates: %v", err)
	}
}
