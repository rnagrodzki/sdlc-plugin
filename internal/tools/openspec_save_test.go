package tools

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/openspec"
)

// saveChange is the change name every openspec_save test stages.
const saveChange = "add-widget"

// saveTarget is the branch openspec_save creates for saveChange.
const saveTarget = "openspec/" + saveChange

// saveSavedLine is the header line openspec_save writes for saveChange.
const saveSavedLine = "**OpenSpec-Saved:** openspec/changes/add-widget/ (branch openspec/add-widget)"

// saveFixture builds a git repo on main with one commit, an `openspec` CLI
// stub on PATH, a staged change saveChange with tasks.md and proposal.md,
// and a plan file outside the repo with the Staging header. It returns the
// repo root and the plan path.
func saveFixture(t *testing.T) (root, planPath string) {
	t.Helper()
	root = t.TempDir()
	initGitFixture(t, root)
	gitCommit(t, root, "init")
	stubOpenspecForMaterialize(t)
	writeMaterializeStaging(t, root, saveChange, map[string]string{
		"tasks.md":    "- [ ] First task\n",
		"proposal.md": "# Proposal\n",
	})
	planPath = filepath.Join(t.TempDir(), "plan.md")
	writeFile(t, planPath, matPlanHeaders(saveChange))
	return root, planPath
}

// readSaveFile returns the content of path, failing the test on error.
func readSaveFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// failSaveGitOn replaces openspecSaveGit for the test so that the git
// subcommand sub fails and every other command runs for real.
func failSaveGitOn(t *testing.T, sub string) {
	t.Helper()
	orig := openspecSaveGit
	t.Cleanup(func() { openspecSaveGit = orig })
	openspecSaveGit = func(dir string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == sub {
			return "", errors.New("injected git " + sub + " failure")
		}
		return orig(dir, args...)
	}
}

// wantSaveDomain asserts err is a DomainError whose Suggestion contains
// suggestion.
func wantSaveDomain(t *testing.T, err error, suggestion string) *mcpserver.DomainError {
	t.Helper()
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("err = %T (%v), want *mcpserver.DomainError", err, err)
	}
	if de.Suggestion == "" || !strings.Contains(de.Suggestion, suggestion) {
		t.Fatalf("Suggestion = %q, want it to contain %q", de.Suggestion, suggestion)
	}
	return de
}

// wantSaveInfra asserts err is an InfraError whose Suggestion contains
// suggestion.
func wantSaveInfra(t *testing.T, err error, suggestion string) *mcpserver.InfraError {
	t.Helper()
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %T (%v), want *mcpserver.InfraError", err, err)
	}
	if ie.Suggestion == "" || !strings.Contains(ie.Suggestion, suggestion) {
		t.Fatalf("Suggestion = %q, want it to contain %q", ie.Suggestion, suggestion)
	}
	return ie
}

// assertSaveUntouched asserts that a failed guard left the repo on branch,
// left the plan as the original Staging plan, and created no change dir.
func assertSaveUntouched(t *testing.T, root, planPath, branch string) {
	t.Helper()
	if got := gitOutTrim(t, root, "branch", "--show-current"); got != branch {
		t.Errorf("current branch = %q, want %q", got, branch)
	}
	if got := readSaveFile(t, planPath); got != matPlanHeaders(saveChange) {
		t.Errorf("plan changed:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, "openspec", "changes", saveChange)); !os.IsNotExist(err) {
		t.Errorf("openspec/changes/%s exists, want no change dir (stat err %v)", saveChange, err)
	}
	if got := gitOutTrim(t, root, "branch", "--list", saveTarget); got != "" && branch != saveTarget {
		t.Errorf("branch %s exists after a failed guard: %q", saveTarget, got)
	}
}

// TestOpenspecSave_CreatesBranchAndSaves covers the save from the default
// branch: it creates openspec/<change>, materializes and stamps the change,
// stages it, and rewrites the plan header so Materialize finds no Staging
// line afterwards.
func TestOpenspecSave_CreatesBranchAndSaves(t *testing.T) {
	root, planPath := saveFixture(t)

	out, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("openspecSave: %v", err)
	}
	if out.Change != saveChange || out.Branch != saveTarget || !out.BranchCreated || out.Materialized != "created" {
		t.Errorf("out = %+v, want change %s, branch %s, branchCreated, materialized created", out, saveChange, saveTarget)
	}
	if out.RefsStamped != 1 {
		t.Errorf("RefsStamped = %d, want 1", out.RefsStamped)
	}
	for _, want := range []string{
		"openspec/changes/add-widget/.openspec.yaml",
		"openspec/changes/add-widget/proposal.md",
		"openspec/changes/add-widget/tasks.md",
	} {
		if !slices.Contains(out.StagedFiles, want) {
			t.Errorf("StagedFiles = %v, want it to contain %s", out.StagedFiles, want)
		}
	}
	if out.Next != openspecSaveNextCommit {
		t.Errorf("Next = %q, want %q", out.Next, openspecSaveNextCommit)
	}
	const wantSummary = "Saved change add-widget on branch openspec/add-widget. Staged files: 3."
	if out.Summary != wantSummary {
		t.Errorf("Summary = %q, want %q", out.Summary, wantSummary)
	}
	if got := gitOutTrim(t, root, "branch", "--show-current"); got != saveTarget {
		t.Errorf("current branch = %q, want %q", got, saveTarget)
	}
	tasks := readSaveFile(t, filepath.Join(root, "openspec", "changes", saveChange, "tasks.md"))
	if !strings.Contains(tasks, "<!-- ref:") {
		t.Errorf("tasks.md has no ref comment: %q", tasks)
	}
	if staged := gitOutTrim(t, root, "diff", "--cached", "--", "openspec/changes/add-widget/tasks.md"); !strings.Contains(staged, "<!-- ref:") {
		t.Errorf("the staged tasks.md has no ref comment: %q", staged)
	}

	plan := readSaveFile(t, planPath)
	if !strings.Contains(plan, saveSavedLine+"\n") || strings.Contains(plan, "**OpenSpec-Staging:**") {
		t.Errorf("plan header not rewritten:\n%s", plan)
	}
	if !strings.Contains(plan, "**Source:** openspec/changes/add-widget/\n") {
		t.Errorf("plan lost its other lines:\n%s", plan)
	}
	res, err := openspec.Materialize(root, plan)
	if err != nil || res != (openspec.MaterializeResult{}) {
		t.Errorf("Materialize(rewritten plan) = %+v, %v; want zero result, nil", res, err)
	}
}

// TestOpenspecSave_CRLFPlan covers a plan with CRLF line endings:
// StagedChangeFromPlan reads the Staging line, so the save must replace that
// line too. Without the match the save would report success and leave the
// Staging line in the plan.
func TestOpenspecSave_CRLFPlan(t *testing.T) {
	root, planPath := saveFixture(t)
	writeFile(t, planPath, strings.ReplaceAll(matPlanHeaders(saveChange), "\n", "\r\n"))

	out, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("openspecSave: %v", err)
	}
	if out.Materialized != "created" || out.Branch != saveTarget {
		t.Errorf("out = %+v, want materialized created on %s", out, saveTarget)
	}
	plan := readSaveFile(t, planPath)
	if !strings.Contains(plan, saveSavedLine+"\n") || strings.Contains(plan, "**OpenSpec-Staging:**") {
		t.Errorf("plan header not rewritten:\n%q", plan)
	}
	again, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	if err != nil || again.Materialized != "already" {
		t.Errorf("second save = %+v, %v; want materialized already", again, err)
	}
}

// TestOpenspecSave_ContinuesOnChangeBranch covers a save that starts on
// openspec/<change>: it saves there and does not create a branch.
func TestOpenspecSave_ContinuesOnChangeBranch(t *testing.T) {
	root, planPath := saveFixture(t)
	runGit(t, root, "switch", "-c", saveTarget)

	out, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("openspecSave: %v", err)
	}
	if out.BranchCreated || out.Branch != saveTarget || out.Materialized != "created" {
		t.Errorf("out = %+v, want branchCreated false on %s, materialized created", out, saveTarget)
	}
	if !strings.Contains(readSaveFile(t, planPath), saveSavedLine) {
		t.Error("plan header not rewritten")
	}
}

// TestOpenspecSave_DefaultBranchFromOriginHead covers a default branch other
// than main: origin/HEAD names trunk, and the save from trunk succeeds.
func TestOpenspecSave_DefaultBranchFromOriginHead(t *testing.T) {
	root, planPath := saveFixture(t)
	runGit(t, root, "branch", "-m", "trunk")
	runGit(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")

	out, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("openspecSave: %v", err)
	}
	if !out.BranchCreated || out.Branch != saveTarget {
		t.Errorf("out = %+v, want a new branch %s", out, saveTarget)
	}
	if got := gitOutTrim(t, root, "branch", "--show-current"); got != saveTarget {
		t.Errorf("current branch = %q, want %q", got, saveTarget)
	}
}

// TestOpenspecSave_AlreadySaved covers a plan with the Saved line: the
// second call returns already with the staged files and the exact summary,
// and after a commit it returns already with no staged file, the summary for
// zero files, and the nothing-staged next step.
func TestOpenspecSave_AlreadySaved(t *testing.T) {
	root, planPath := saveFixture(t)
	if _, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath}); err != nil {
		t.Fatalf("first openspecSave: %v", err)
	}
	plan := readSaveFile(t, planPath)

	out, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("second openspecSave: %v", err)
	}
	if out.Materialized != "already" || out.Branch != saveTarget || out.BranchCreated || out.RefsStamped != 0 {
		t.Errorf("out = %+v, want already on %s", out, saveTarget)
	}
	if len(out.StagedFiles) != 3 || out.Next != openspecSaveNextCommit {
		t.Errorf("StagedFiles = %v, Next = %q; want 3 files and the commit step", out.StagedFiles, out.Next)
	}
	const wantSummary = "Change add-widget is already saved on branch openspec/add-widget. Staged files: 3."
	if out.Summary != wantSummary {
		t.Errorf("Summary = %q, want %q", out.Summary, wantSummary)
	}
	if got := readSaveFile(t, planPath); got != plan {
		t.Errorf("an already call changed the plan:\n%s", got)
	}

	runGit(t, root, "commit", "-m", "docs: add change")
	out, err = openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("third openspecSave: %v", err)
	}
	if len(out.StagedFiles) != 0 {
		t.Errorf("StagedFiles = %#v, want no staged file", out.StagedFiles)
	}
	if out.Next != openspecSaveNextNothingStaged {
		t.Errorf("Next = %q, want %q", out.Next, openspecSaveNextNothingStaged)
	}
	const wantSummary0 = "Change add-widget is already saved on branch openspec/add-widget. Staged files: 0."
	if out.Summary != wantSummary0 {
		t.Errorf("Summary = %q, want %q", out.Summary, wantSummary0)
	}
}

// TestOpenspecSave_AlreadySavedBranchFallback covers a Saved line with no
// branch part: the output names openspec/<change>, in the branch field and in
// the summary.
func TestOpenspecSave_AlreadySavedBranchFallback(t *testing.T) {
	root := t.TempDir()
	initGitFixture(t, root)
	gitCommit(t, root, "init")
	planPath := filepath.Join(t.TempDir(), "plan.md")
	writeFile(t, planPath, "# Plan\n**OpenSpec-Saved:** openspec/changes/add-widget/\n")

	out, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("openspecSave: %v", err)
	}
	if out.Branch != saveTarget || out.Materialized != "already" {
		t.Errorf("out = %+v, want already on %s", out, saveTarget)
	}
	const wantSummary = "Change add-widget is already saved on branch openspec/add-widget. Staged files: 0."
	if out.Summary != wantSummary {
		t.Errorf("Summary = %q, want %q", out.Summary, wantSummary)
	}
}

// TestOpenspecSave_AlreadySavedNamedBranch covers a Saved line with a branch
// part: the output returns that branch as written, not openspec/<change>, even
// though the repo is on main.
func TestOpenspecSave_AlreadySavedNamedBranch(t *testing.T) {
	root := t.TempDir()
	initGitFixture(t, root)
	gitCommit(t, root, "init")
	planPath := filepath.Join(t.TempDir(), "plan.md")
	writeFile(t, planPath, "# Plan\n**OpenSpec-Saved:** openspec/changes/add-widget/ (branch openspec/other)\n")

	out, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("openspecSave: %v", err)
	}
	if out.Branch != "openspec/other" || out.Materialized != "already" || out.Change != saveChange {
		t.Errorf("out = %+v, want already for %s on openspec/other", out, saveChange)
	}
	const wantSummary = "Change add-widget is already saved on branch openspec/other. Staged files: 0."
	if out.Summary != wantSummary {
		t.Errorf("Summary = %q, want %q", out.Summary, wantSummary)
	}
}

// TestOpenspecSave_InputErrors covers each check that runs before any git
// command. workDir is not a git repo, so a check that ran git would return
// an InfraError instead of the DomainError the test wants.
func TestOpenspecSave_InputErrors(t *testing.T) {
	dir := t.TempDir()
	plan := func(name, content string) string {
		p := filepath.Join(dir, name)
		writeFile(t, p, content)
		return p
	}
	tests := []struct {
		name       string
		planPath   string
		suggestion string
	}{
		{"relative path", "plan.md", "Pass the absolute path of the plan file."},
		{"unreadable plan", filepath.Join(dir, "missing.md"), "Pass the absolute path of the plan file."},
		{"no header", plan("none.md", "# Plan\n"), "Run /sdlc:plan with Create OpenSpec change first."},
		{"both headers", plan("both.md", matPlanHeaders(saveChange)+saveSavedLine+"\n"), "Keep one header line."},
		{"invalid staged name", plan("bad.md", "**OpenSpec-Staging:** .sdlc-v2/openspec-staging/Bad_Name/\n"), "Rename the change to lowercase letters"},
		{"invalid saved name", plan("badsaved.md", "**OpenSpec-Saved:** openspec/changes/Bad_Name/\n"), "Rename the change to lowercase letters"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := openspecSave(dir, OpenspecSaveIn{PlanPath: tc.planPath})
			wantSaveDomain(t, err, tc.suggestion)
		})
	}
}

// TestOpenspecSave_BranchRule covers the branch guard: a feature branch and
// an existing openspec/<change> that is not current both fail with a
// DomainError and change nothing.
func TestOpenspecSave_BranchRule(t *testing.T) {
	t.Run("feature branch", func(t *testing.T) {
		root, planPath := saveFixture(t)
		runGit(t, root, "switch", "-c", "feat/x")
		_, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
		wantSaveDomain(t, err, "Switch to the default branch, then run again.")
		assertSaveUntouched(t, root, planPath, "feat/x")
	})
	for _, start := range []string{"main", "feat/x"} {
		t.Run("change branch exists, on "+start, func(t *testing.T) {
			root, planPath := saveFixture(t)
			runGit(t, root, "branch", saveTarget)
			if start != "main" {
				runGit(t, root, "switch", "-c", start)
			}
			_, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
			wantSaveDomain(t, err, "git switch openspec/add-widget, then run again.")
			if got := gitOutTrim(t, root, "branch", "--show-current"); got != start {
				t.Errorf("current branch = %q, want %q", got, start)
			}
			if got := readSaveFile(t, planPath); got != matPlanHeaders(saveChange) {
				t.Errorf("plan changed:\n%s", got)
			}
		})
	}
}

// TestOpenspecSave_StatusGuard covers the status guard: a tracked change
// outside the change dir fails and changes nothing, the Suggestion names at
// most 10 dirty paths and then "and N more", while tracked changes under
// .sdlc-v2/ and untracked files pass.
func TestOpenspecSave_StatusGuard(t *testing.T) {
	t.Run("dirty tracked file", func(t *testing.T) {
		root, planPath := saveFixture(t)
		writeFile(t, filepath.Join(root, "init.txt"), "changed")
		_, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
		de := wantSaveDomain(t, err, "init.txt: commit or stash them first.")
		if !strings.Contains(de.Msg, "1 tracked files") {
			t.Errorf("Msg = %q, want the dirty count", de.Msg)
		}
		assertSaveUntouched(t, root, planPath, "main")
	})
	// The Suggestion lists at most 10 paths. Exactly 10 dirty files add no
	// "and N more" text; each file past 10 adds to the N.
	for _, tc := range []struct {
		dirty     int
		wantExtra string
	}{
		{10, ""},
		{11, " and 1 more"},
		{12, " and 2 more"},
	} {
		t.Run(strconv.Itoa(tc.dirty)+" dirty files", func(t *testing.T) {
			root, planPath := saveFixture(t)
			var names []string // sorted, as git status prints them
			for i := 0; i < tc.dirty; i++ {
				name := "f" + string(rune('a'+i))
				gitCommit(t, root, name)
				names = append(names, name+".txt")
			}
			for _, name := range names {
				writeFile(t, filepath.Join(root, name), "changed")
			}
			_, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
			de := wantSaveDomain(t, err, "commit or stash them first.")
			wantSuggestion := strings.Join(names[:10], ", ") + tc.wantExtra + ": commit or stash them first."
			if de.Suggestion != wantSuggestion {
				t.Errorf("Suggestion = %q, want %q", de.Suggestion, wantSuggestion)
			}
			if wantMsg := strconv.Itoa(tc.dirty) + " tracked files outside"; !strings.Contains(de.Msg, wantMsg) {
				t.Errorf("Msg = %q, want it to contain %q", de.Msg, wantMsg)
			}
			assertSaveUntouched(t, root, planPath, "main")
		})
	}
	t.Run("excluded paths pass", func(t *testing.T) {
		root, planPath := saveFixture(t)
		writeFile(t, filepath.Join(root, ".sdlc-v2", "tracked.json"), "{}")
		runGit(t, root, "add", "-f", ".sdlc-v2/tracked.json")
		runGit(t, root, "commit", "-m", "track data file")
		writeFile(t, filepath.Join(root, ".sdlc-v2", "tracked.json"), `{"x":1}`)
		writeFile(t, filepath.Join(root, "untracked.txt"), "new")
		if _, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath}); err != nil {
			t.Fatalf("openspecSave: %v", err)
		}
	})
}

// TestOpenspecSaveStatusPaths covers each line shape the status parser
// handles: a modified path, a rename, and a line too short to hold a path.
func TestOpenspecSaveStatusPaths(t *testing.T) {
	got := openspecSaveStatusPaths(" M a.go\nR  old.go -> new.go\nM\n\nM  b/c.go")
	want := []string{"a.go", "new.go", "b/c.go"}
	if !slices.Equal(got, want) {
		t.Errorf("openspecSaveStatusPaths = %v, want %v", got, want)
	}
	if got := openspecSaveStatusPaths(""); len(got) != 0 {
		t.Errorf("openspecSaveStatusPaths(\"\") = %v, want nil", got)
	}
}

// TestOpenspecSave_BranchReadErrors covers the InfraError when the current
// branch or the default branch cannot be read.
func TestOpenspecSave_BranchReadErrors(t *testing.T) {
	const suggestion = "Run from a git repository that has a main or master branch"
	t.Run("not a git repo", func(t *testing.T) {
		dir := t.TempDir()
		planPath := filepath.Join(t.TempDir(), "plan.md")
		writeFile(t, planPath, matPlanHeaders(saveChange))
		_, err := openspecSave(dir, OpenspecSaveIn{PlanPath: planPath})
		wantSaveInfra(t, err, suggestion)
	})
	t.Run("no default branch", func(t *testing.T) {
		root := t.TempDir()
		initGitFixture(t, root)
		gitCommit(t, root, "init")
		runGit(t, root, "branch", "-m", "trunk")
		planPath := filepath.Join(t.TempDir(), "plan.md")
		writeFile(t, planPath, matPlanHeaders(saveChange))
		_, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
		wantSaveInfra(t, err, suggestion)
	})
}

// TestOpenspecSave_GitFailuresBeforeWrites covers a failed git read before
// any write: branch --list and status fail with an InfraError and change
// nothing.
func TestOpenspecSave_GitFailuresBeforeWrites(t *testing.T) {
	for _, sub := range []string{"branch", "status"} {
		t.Run(sub, func(t *testing.T) {
			root, planPath := saveFixture(t)
			failSaveGitOn(t, sub)
			_, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
			wantSaveInfra(t, err, openspecSaveGitSuggestion)
			assertSaveUntouched(t, root, planPath, "main")
		})
	}
}

// TestOpenspecSave_SwitchFails covers a failure of the first write, git
// switch -c: the repo stays on main and nothing else is written.
func TestOpenspecSave_SwitchFails(t *testing.T) {
	root, planPath := saveFixture(t)
	failSaveGitOn(t, "switch")
	_, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	wantSaveInfra(t, err, openspecSaveGitSuggestion)
	assertSaveUntouched(t, root, planPath, "main")
}

// TestOpenspecSave_MaterializeFails covers a failure of the second write:
// the branch exists, but with no staging dir and no change dir Materialize
// fails, and the plan keeps its Staging line.
func TestOpenspecSave_MaterializeFails(t *testing.T) {
	root, planPath := saveFixture(t)
	if err := os.RemoveAll(filepath.Join(root, ".sdlc-v2", "openspec-staging", saveChange)); err != nil {
		t.Fatal(err)
	}
	_, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	de := wantSaveDomain(t, err, "call openspec_save again")
	if !strings.HasPrefix(de.Msg, "openspec_save: ") || strings.Contains(de.Msg, "init") {
		t.Errorf("Msg = %q, want an openspec_save: prefix and no init wording", de.Msg)
	}
	if strings.Contains(de.Suggestion, "execute_state init") {
		t.Errorf("Suggestion = %q, must not name execute_state init", de.Suggestion)
	}
	if got := gitOutTrim(t, root, "branch", "--show-current"); got != saveTarget {
		t.Errorf("current branch = %q, want %q (the branch persists)", got, saveTarget)
	}
	if got := readSaveFile(t, planPath); got != matPlanHeaders(saveChange) {
		t.Errorf("plan changed:\n%s", got)
	}
}

// TestOpenspecSave_StampFails covers a failure of the third write: the
// change dir already exists (Materialize returns already) with a read-only
// tasks.md, so the ref stamp fails before git add and before the plan
// rewrite.
func TestOpenspecSave_StampFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	root, planPath := saveFixture(t)
	if err := os.RemoveAll(filepath.Join(root, ".sdlc-v2", "openspec-staging", saveChange)); err != nil {
		t.Fatal(err)
	}
	tasksPath := filepath.Join(root, "openspec", "changes", saveChange, "tasks.md")
	writeFile(t, tasksPath, "- [ ] First task\n")
	if err := os.Chmod(tasksPath, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(tasksPath, 0o644); err != nil {
			t.Errorf("restore mode of %s: %v", tasksPath, err)
		}
	})

	_, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	wantSaveInfra(t, err, "Fix write access to")
	if got := gitOutTrim(t, root, "diff", "--cached", "--name-only"); got != "" {
		t.Errorf("staged files = %q, want none (git add did not run)", got)
	}
	if got := readSaveFile(t, planPath); got != matPlanHeaders(saveChange) {
		t.Errorf("plan changed:\n%s", got)
	}
}

// TestOpenspecSave_NoTasksFile covers a change dir without tasks.md: the
// stamp is skipped with zero refs and the save succeeds as already.
func TestOpenspecSave_NoTasksFile(t *testing.T) {
	root, planPath := saveFixture(t)
	if err := os.RemoveAll(filepath.Join(root, ".sdlc-v2", "openspec-staging", saveChange)); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "openspec", "changes", saveChange, "proposal.md"), "# Proposal\n")

	out, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("openspecSave: %v", err)
	}
	if out.RefsStamped != 0 || out.Materialized != "already" {
		t.Errorf("out = %+v, want 0 refs and materialized already", out)
	}
	if !slices.Equal(out.StagedFiles, []string{"openspec/changes/add-widget/proposal.md"}) {
		t.Errorf("StagedFiles = %v, want proposal.md only", out.StagedFiles)
	}
}

// TestOpenspecSave_AddFails covers a failure of the fourth write, git add:
// the branch and the materialized change persist, and the plan keeps its
// Staging line.
func TestOpenspecSave_AddFails(t *testing.T) {
	root, planPath := saveFixture(t)
	failSaveGitOn(t, "add")
	_, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	wantSaveInfra(t, err, openspecSaveGitSuggestion)
	if got := gitOutTrim(t, root, "branch", "--show-current"); got != saveTarget {
		t.Errorf("current branch = %q, want %q", got, saveTarget)
	}
	if _, statErr := os.Stat(filepath.Join(root, "openspec", "changes", saveChange, "tasks.md")); statErr != nil {
		t.Errorf("tasks.md missing after materialize: %v", statErr)
	}
	if got := readSaveFile(t, planPath); got != matPlanHeaders(saveChange) {
		t.Errorf("plan changed:\n%s", got)
	}
}

// TestOpenspecSave_PlanWriteFailsThenRecovers covers a failure of the last
// write, the plan header: the change is staged on the branch, the plan keeps
// its Staging line, and a second call after the fix returns already and
// rewrites the header.
func TestOpenspecSave_PlanWriteFailsThenRecovers(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	root, planPath := saveFixture(t)
	if err := os.Chmod(planPath, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(planPath, 0o644); err != nil {
			t.Errorf("restore mode of %s: %v", planPath, err)
		}
	})

	_, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	wantSaveInfra(t, err, "The change is saved on the branch.")
	if got := gitOutTrim(t, root, "branch", "--show-current"); got != saveTarget {
		t.Errorf("current branch = %q, want %q", got, saveTarget)
	}
	if got := gitOutTrim(t, root, "diff", "--cached", "--name-only"); !strings.Contains(got, "openspec/changes/add-widget/tasks.md") {
		t.Errorf("staged files = %q, want the change files", got)
	}
	if got := readSaveFile(t, planPath); got != matPlanHeaders(saveChange) {
		t.Errorf("plan changed:\n%s", got)
	}

	if err := os.Chmod(planPath, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("second openspecSave: %v", err)
	}
	if out.Materialized != "already" || out.BranchCreated || out.RefsStamped != 0 {
		t.Errorf("out = %+v, want already on the existing branch with no new refs", out)
	}
	if !strings.Contains(readSaveFile(t, planPath), saveSavedLine) {
		t.Error("plan header not rewritten on the second call")
	}
}

// TestOpenspecSave_StagedFilesFails covers a failed git diff --cached, both
// after a save (the plan is already rewritten) and on the already path.
func TestOpenspecSave_StagedFilesFails(t *testing.T) {
	t.Run("after save", func(t *testing.T) {
		root, planPath := saveFixture(t)
		failSaveGitOn(t, "diff")
		_, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
		wantSaveInfra(t, err, openspecSaveGitSuggestion)
		if !strings.Contains(readSaveFile(t, planPath), saveSavedLine) {
			t.Error("plan header not rewritten before the failed diff")
		}
	})
	t.Run("already", func(t *testing.T) {
		root := t.TempDir()
		initGitFixture(t, root)
		gitCommit(t, root, "init")
		planPath := filepath.Join(t.TempDir(), "plan.md")
		writeFile(t, planPath, saveSavedLine+"\n")
		failSaveGitOn(t, "diff")
		_, err := openspecSave(root, OpenspecSaveIn{PlanPath: planPath})
		wantSaveInfra(t, err, openspecSaveGitSuggestion)
	})
}

// TestOpenspecSaveMaterializeError pins every arm of the wrapper: each
// Materialize failure keeps its error type, names openspec_save in the
// message, and never sends the caller to execute_state init.
func TestOpenspecSaveMaterializeError(t *testing.T) {
	cases := []struct {
		name           string
		err            error
		wantDomain     bool
		wantSuggestion string
	}{
		{"cli not found", openspec.ErrCLINotFound, false, openspecSaveCLISuggestion},
		{"invalid name", openspec.ErrInvalidChangeName, true, openspecNameSuggestion},
		{"rule error", openspec.ErrMaterialize, true, "call openspec_save again"},
		{"other failure", errors.New("disk full"), false, "then call openspec_save again."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := openspecSaveMaterializeError(tc.err)
			var msg, suggestion string
			var de *mcpserver.DomainError
			var ie *mcpserver.InfraError
			switch {
			case errors.As(got, &de):
				if !tc.wantDomain {
					t.Fatalf("err = DomainError, want InfraError")
				}
				msg, suggestion = de.Msg, de.Suggestion
			case errors.As(got, &ie):
				if tc.wantDomain {
					t.Fatalf("err = InfraError, want DomainError")
				}
				msg, suggestion = ie.Msg, ie.Suggestion
			default:
				t.Fatalf("err = %T, want a typed error", got)
			}
			if !strings.HasPrefix(msg, "openspec_save: ") || strings.Contains(msg, "init:") {
				t.Errorf("Msg = %q, want an openspec_save: prefix and no init: prefix", msg)
			}
			if !strings.Contains(suggestion, tc.wantSuggestion) {
				t.Errorf("Suggestion = %q, want it to contain %q", suggestion, tc.wantSuggestion)
			}
			if strings.Contains(suggestion, "execute_state init") || strings.Contains(suggestion, "Skip OpenSpec") {
				t.Errorf("Suggestion = %q, must not name execute_state init or Skip OpenSpec", suggestion)
			}
		})
	}
}
