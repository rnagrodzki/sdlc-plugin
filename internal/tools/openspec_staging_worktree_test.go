package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/openspec"
)

// stagingChange is the change name the worktree staging tests stage.
const stagingChange = "add-widget"

// stagingTasks is the tasks.md content the staged change holds.
const stagingTasks = "- [ ] First task\n"

// stagingRepo is a main worktree and two linked worktrees of one repository.
type stagingRepo struct {
	main string // the main worktree: holds the staging folder
	a    string // linked worktree that stages the change
	b    string // linked worktree that starts the plan
}

// newStagingRepo creates the main repo of newOpenspecStageFixture (it commits
// openspec/config.yaml, which Stage reads from the active worktree) and adds
// the linked worktrees A and B, each on its own branch. Every directory is
// under t.TempDir().
func newStagingRepo(t *testing.T) stagingRepo {
	t.Helper()
	main := newOpenspecStageFixture(t, "")
	base := t.TempDir()
	a := filepath.Join(base, "wt-a")
	b := filepath.Join(base, "wt-b")
	runGit(t, main, "worktree", "add", "-b", "feat/a", a)
	runGit(t, main, "worktree", "add", "-b", "feat/b", b)
	return stagingRepo{main: main, a: a, b: b}
}

// stagingPlan writes a plan file with both OpenSpec header lines in a folder
// outside every worktree and returns its path.
func stagingPlan(t *testing.T) string {
	t.Helper()
	planPath := filepath.Join(t.TempDir(), "plan.md")
	writeFile(t, planPath, matPlanHeaders(stagingChange))
	return planPath
}

// stageFromWorktree runs openspec_stage as a session in the linked worktree
// active, with main as the main worktree, and checks that the staging folder
// is under main and not under active.
func stageFromWorktree(t *testing.T, r stagingRepo, active, planPath string) {
	t.Helper()
	stubOpenspecForStage(t, 0)
	out, err := planSupportCore(r.main, active, PlanSupportIn{
		Action:     "openspec_stage",
		ChangeName: stagingChange,
		Files: []openspec.StageFile{
			{Path: "proposal.md", Content: "# Proposal\n"},
			{Path: "specs/widget/spec.md", Content: "# Spec\n"},
			{Path: "design.md", Content: "# Design\n"},
			{Path: "tasks.md", Content: stagingTasks},
		},
		PlanPath: planPath,
	})
	if err != nil {
		t.Fatalf("openspec_stage: %v", err)
	}
	if out.Valid == nil || !*out.Valid {
		t.Fatalf("Valid = %v, want true (validateOutput %q)", out.Valid, out.ValidateOutput)
	}
	if out.StagingDir != ".sdlc-v2/openspec-staging/add-widget/" {
		t.Errorf("StagingDir = %q, want the path relative to the main worktree", out.StagingDir)
	}
	mainManifest := filepath.Join(r.main, ".sdlc-v2", "openspec-staging", stagingChange, openspec.StageManifestFile)
	if _, err := os.Stat(mainManifest); err != nil {
		t.Fatalf("stage.json under the main worktree is missing: %v", err)
	}
	activeStaging := filepath.Join(active, ".sdlc-v2", "openspec-staging", stagingChange)
	if _, err := os.Stat(activeStaging); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("staging folder under the linked worktree exists (stat err %v), want none", err)
	}
}

// assertStagingMaterialized checks that the change folder exists in worktree
// and that no staging folder of the change is left under the given roots.
func assertStagingMaterialized(t *testing.T, worktree string, goneRoots ...string) {
	t.Helper()
	tasks := filepath.Join(worktree, "openspec", "changes", stagingChange, "tasks.md")
	if _, err := os.Stat(tasks); err != nil {
		t.Errorf("tasks.md missing in %s: %v", worktree, err)
	}
	for _, root := range goneRoots {
		dir := filepath.Join(root, ".sdlc-v2", "openspec-staging", stagingChange)
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("staging folder %s still exists (stat err %v), want it gone", dir, err)
		}
	}
}

// TestOpenspecStaging_SecondWorktree uses real git worktrees to check that a
// change staged in one linked worktree starts in another one.
func TestOpenspecStaging_SecondWorktree(t *testing.T) {
	t.Run("stage in A then execute init in B", func(t *testing.T) {
		r := newStagingRepo(t)
		planPath := stagingPlan(t)
		stageFromWorktree(t, r, r.a, planPath)
		stubOpenspecForMaterialize(t)

		result, err := executeState(r.main, r.b, ExecuteStateIn{
			Action:   "init",
			Branch:   "feat/b",
			Quality:  "standard",
			PlanPath: planPath,
		}, fixedClock(testNow))
		if err != nil {
			t.Fatalf("init: %v", err)
		}
		m, ok := result.(map[string]any)
		if !ok {
			t.Fatalf("result = %T, want map[string]any", result)
		}
		osp, ok := m["openspec"].(map[string]any)
		if !ok || osp["change"] != stagingChange || osp["materialized"] != "created" {
			t.Errorf("result openspec = %v, want {change:add-widget materialized:created}", m["openspec"])
		}
		assertStagingMaterialized(t, r.b, r.main)
		if _, err := os.Stat(filepath.Join(r.a, "openspec", "changes", stagingChange)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("change folder exists in worktree A (stat err %v), want none", err)
		}
	})

	t.Run("old plugin staging under B only", func(t *testing.T) {
		r := newStagingRepo(t)
		planPath := stagingPlan(t)
		stubOpenspecForMaterialize(t)
		writeMaterializeStaging(t, r.b, stagingChange, map[string]string{"tasks.md": stagingTasks})

		result, err := executeState(r.main, r.b, ExecuteStateIn{
			Action:   "init",
			Branch:   "feat/b",
			Quality:  "standard",
			PlanPath: planPath,
		}, fixedClock(testNow))
		if err != nil {
			t.Fatalf("init: %v", err)
		}
		m, ok := result.(map[string]any)
		if !ok {
			t.Fatalf("result = %T, want map[string]any", result)
		}
		osp, ok := m["openspec"].(map[string]any)
		if !ok || osp["change"] != stagingChange || osp["materialized"] != "created" {
			t.Errorf("result openspec = %v, want {change:add-widget materialized:created}", m["openspec"])
		}
		assertStagingMaterialized(t, r.b, r.b, r.main)
	})

	t.Run("stage in A then ship prepare in B", func(t *testing.T) {
		r := newStagingRepo(t)
		planPath := stagingPlan(t)
		stageFromWorktree(t, r, r.a, planPath)
		stubOpenspecForMaterialize(t)

		out, err := shipPrepare(r.main, r.b, ShipPrepareIn{SkipConfigCheck: true, PlanFile: planPath})
		if err != nil {
			t.Fatalf("shipPrepare: %v", err)
		}
		if len(out.Errors) != 0 {
			t.Fatalf("Errors = %v, want empty", out.Errors)
		}
		if out.Openspec == nil || *out.Openspec != (ShipOpenspecOut{Change: stagingChange, Materialized: "created"}) {
			t.Fatalf("Openspec = %+v, want {add-widget created}", out.Openspec)
		}
		assertStagingMaterialized(t, r.b, r.main)
	})
}

// saveRootsFixture builds a git repo on main with one commit, an `openspec`
// CLI stub on PATH, and a plan file outside the repo with the Staging header.
// It stages nothing. It returns the repo root and the plan path.
func saveRootsFixture(t *testing.T) (root, planPath string) {
	t.Helper()
	root = t.TempDir()
	initGitFixture(t, root)
	gitCommit(t, root, "init")
	stubOpenspecForMaterialize(t)
	planPath = filepath.Join(t.TempDir(), "plan.md")
	writeFile(t, planPath, matPlanHeaders(saveChange))
	return root, planPath
}

// TestOpenspecSaveRoots_MainRoot: a set mainRoot with no error is the staging
// root. The active worktree has no staging folder, so the save succeeds only
// if it reads the folder under mainRoot. It adds no warning.
func TestOpenspecSaveRoots_MainRoot(t *testing.T) {
	root, planPath := saveRootsFixture(t)
	mainRoot := t.TempDir()
	writeMaterializeStaging(t, mainRoot, saveChange, map[string]string{"tasks.md": "- [ ] From main\n"})

	out, err := openspecSaveRoots(mainRoot, nil, root, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("openspecSaveRoots: %v", err)
	}
	if out.Change != saveChange || out.Materialized != "created" {
		t.Errorf("out = %+v, want change %s, materialized created", out, saveChange)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", out.Warnings)
	}
	tasks := readSaveFile(t, filepath.Join(root, "openspec", "changes", saveChange, "tasks.md"))
	if !strings.Contains(tasks, "From main") {
		t.Errorf("tasks.md = %q, want the content staged under mainRoot", tasks)
	}
	stagingDir := filepath.Join(mainRoot, ".sdlc-v2", "openspec-staging", saveChange)
	if _, err := os.Stat(stagingDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("staging folder under mainRoot still exists (stat err %v), want it gone", err)
	}
}

// TestOpenspecSaveRoots_EmptyMainRoot: an empty mainRoot with no error means
// the active worktree is the staging root. It adds no warning.
func TestOpenspecSaveRoots_EmptyMainRoot(t *testing.T) {
	root, planPath := saveRootsFixture(t)
	writeMaterializeStaging(t, root, saveChange, map[string]string{"tasks.md": "- [ ] From active\n"})

	out, err := openspecSaveRoots("", nil, root, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("openspecSaveRoots: %v", err)
	}
	if out.Change != saveChange || out.Materialized != "created" {
		t.Errorf("out = %+v, want change %s, materialized created", out, saveChange)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", out.Warnings)
	}
	tasks := readSaveFile(t, filepath.Join(root, "openspec", "changes", saveChange, "tasks.md"))
	if !strings.Contains(tasks, "From active") {
		t.Errorf("tasks.md = %q, want the content staged under the active worktree", tasks)
	}
}

// TestOpenspecSaveRoots_MainRootError: a non-nil mainErr makes the active
// worktree the only staging root, even when mainRoot is set. The save adds
// one warning with the error text. A decoy folder under mainRoot proves that
// the save read the folder of the active worktree and left the decoy alone.
func TestOpenspecSaveRoots_MainRootError(t *testing.T) {
	root, planPath := saveRootsFixture(t)
	mainRoot := t.TempDir()
	writeMaterializeStaging(t, root, saveChange, map[string]string{"tasks.md": "- [ ] From active\n"})
	writeMaterializeStaging(t, mainRoot, saveChange, map[string]string{"tasks.md": "- [ ] From main\n"})

	out, err := openspecSaveRoots(mainRoot, errors.New("no main"), root, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("openspecSaveRoots: %v", err)
	}
	const wantWarning = "Main worktree not found (no main). Read only the staging folder of the active worktree."
	if len(out.Warnings) != 1 || out.Warnings[0] != wantWarning {
		t.Errorf("Warnings = %q, want [%q]", out.Warnings, wantWarning)
	}
	tasks := readSaveFile(t, filepath.Join(root, "openspec", "changes", saveChange, "tasks.md"))
	if !strings.Contains(tasks, "From active") || strings.Contains(tasks, "From main") {
		t.Errorf("tasks.md = %q, want the content staged under the active worktree", tasks)
	}
	activeStaging := filepath.Join(root, ".sdlc-v2", "openspec-staging", saveChange)
	if _, err := os.Stat(activeStaging); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("staging folder of the active worktree still exists (stat err %v), want it gone", err)
	}
	decoy := filepath.Join(mainRoot, ".sdlc-v2", "openspec-staging", saveChange, "tasks.md")
	if got := readSaveFile(t, decoy); got != "- [ ] From main\n" {
		t.Errorf("decoy under mainRoot = %q, want it unchanged", got)
	}
}
