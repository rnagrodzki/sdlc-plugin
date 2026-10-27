package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
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
	if out.StagingRoot != r.main {
		t.Errorf("StagingRoot = %q, want the main worktree %q", out.StagingRoot, r.main)
	}
	absDir := filepath.Join(r.main, ".sdlc-v2", "openspec-staging", stagingChange) + string(filepath.Separator)
	if !strings.Contains(out.Summary, " in "+absDir+";") {
		t.Errorf("Summary = %q, want the absolute staging folder %s", out.Summary, absDir)
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

// saveMainWarning is the MainWarning the openspec_save handler builds when
// the main worktree resolution fails with "no main".
const saveMainWarning = "Main worktree not found (no main). Read only the staging folder of the active worktree."

// TestOpenspecSaveRun_StagingRoot: a set Staging root is read first. The
// active worktree has no staging folder, so the save succeeds only if it
// reads the folder under Staging. It adds no warning.
func TestOpenspecSaveRun_StagingRoot(t *testing.T) {
	root, planPath := saveRootsFixture(t)
	mainRoot := t.TempDir()
	writeMaterializeStaging(t, mainRoot, saveChange, map[string]string{"tasks.md": "- [ ] From main\n"})

	out, err := openspecSaveRun(openspecSaveDirs{Active: root, Staging: mainRoot}, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("openspecSaveRun: %v", err)
	}
	if out.Change != saveChange || out.Materialized != "created" {
		t.Errorf("out = %+v, want change %s, materialized created", out, saveChange)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", out.Warnings)
	}
	tasks := readSaveFile(t, filepath.Join(root, "openspec", "changes", saveChange, "tasks.md"))
	if !strings.Contains(tasks, "From main") {
		t.Errorf("tasks.md = %q, want the content staged under the staging root", tasks)
	}
	stagingDir := filepath.Join(mainRoot, ".sdlc-v2", "openspec-staging", saveChange)
	if _, err := os.Stat(stagingDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("staging folder under the staging root still exists (stat err %v), want it gone", err)
	}
}

// TestOpenspecSaveRun_EmptyStagingRoot: an empty Staging root means the
// active worktree is the staging root. It adds no warning.
func TestOpenspecSaveRun_EmptyStagingRoot(t *testing.T) {
	root, planPath := saveRootsFixture(t)
	writeMaterializeStaging(t, root, saveChange, map[string]string{"tasks.md": "- [ ] From active\n"})

	out, err := openspecSaveRun(openspecSaveDirs{Active: root}, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("openspecSaveRun: %v", err)
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

// TestOpenspecSaveRun_MainWarning: a successful save adds MainWarning to
// Warnings as the only entry.
func TestOpenspecSaveRun_MainWarning(t *testing.T) {
	root, planPath := saveRootsFixture(t)
	writeMaterializeStaging(t, root, saveChange, map[string]string{"tasks.md": "- [ ] From active\n"})

	out, err := openspecSaveRun(openspecSaveDirs{Active: root, MainWarning: saveMainWarning}, OpenspecSaveIn{PlanPath: planPath})
	if err != nil {
		t.Fatalf("openspecSaveRun: %v", err)
	}
	if len(out.Warnings) != 1 || out.Warnings[0] != saveMainWarning {
		t.Errorf("Warnings = %q, want [%q]", out.Warnings, saveMainWarning)
	}
}

// TestOpenspecSaveRun_MainWarningOnFailure: when the save fails because no
// staging folder exists under the active worktree, the error Suggestion
// carries MainWarning, so the caller learns that the main worktree was not
// read.
func TestOpenspecSaveRun_MainWarningOnFailure(t *testing.T) {
	root, planPath := saveRootsFixture(t)

	_, err := openspecSaveRun(openspecSaveDirs{Active: root, MainWarning: saveMainWarning}, OpenspecSaveIn{PlanPath: planPath})
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("err = %T %v, want *mcpserver.DomainError", err, err)
	}
	if !strings.Contains(de.Msg, "staging dir") {
		t.Errorf("Msg = %q, want the missing staging dir", de.Msg)
	}
	if !strings.HasSuffix(de.Suggestion, " "+saveMainWarning) {
		t.Errorf("Suggestion = %q, want it to end with %q", de.Suggestion, saveMainWarning)
	}
}

// fakeMainRoot replaces resolveMainRoot for the test.
func fakeMainRoot(t *testing.T, root string, err error) {
	t.Helper()
	orig := resolveMainRoot
	resolveMainRoot = func() (string, error) { return root, err }
	t.Cleanup(func() { resolveMainRoot = orig })
}

// callRegisteredTool runs the tool name, registered by register, over an
// in-memory MCP session in the process's current directory and returns the
// result and its first text content.
func callRegisteredTool(t *testing.T, register func(*mcpserver.Server), name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	s := mcpserver.New("test", "0.0.0-test")
	register(s)
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	return res, text.Text
}

// TestOpenspecSave_RegisteredFromLinkedWorktree runs the registered
// openspec_save tool in a linked worktree (on the branch openspec/<change>)
// of a real repo. The change is staged only under the main worktree, so the
// save succeeds only if the handler resolves the main worktree.
func TestOpenspecSave_RegisteredFromLinkedWorktree(t *testing.T) {
	main := t.TempDir()
	initGitFixture(t, main)
	gitCommit(t, main, "init")
	linked := filepath.Join(t.TempDir(), "wt")
	runGit(t, main, "worktree", "add", "-b", saveTarget, linked)
	stubOpenspecForMaterialize(t)
	writeMaterializeStaging(t, main, saveChange, map[string]string{"tasks.md": "- [ ] From main\n"})
	planPath := filepath.Join(t.TempDir(), "plan.md")
	writeFile(t, planPath, matPlanHeaders(saveChange))
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(linked))
	t.Chdir(linked)

	res, text := callRegisteredTool(t, RegisterOpenspecTools, "openspec_save", map[string]any{"planPath": planPath})
	if res.IsError {
		t.Fatalf("openspec_save failed:\n%s", text)
	}
	for _, want := range []string{"- materialized: created\n", "- branchCreated: false\n", "## Warnings\n(none)\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("result missing %q:\n%s", want, text)
		}
	}
	tasks := readSaveFile(t, filepath.Join(linked, "openspec", "changes", saveChange, "tasks.md"))
	if !strings.Contains(tasks, "From main") {
		t.Errorf("tasks.md = %q, want the content staged under the main worktree", tasks)
	}
	if _, err := os.Stat(filepath.Join(main, ".sdlc-v2", "openspec-staging", saveChange)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("staging folder under the main worktree still exists (stat err %v), want it gone", err)
	}
}

// TestOpenspecSave_RegisteredMainRootFailure checks the two failure paths of
// the main worktree resolution in the registered openspec_save handler: an
// output-cap error is an InfraError and saves nothing; any other error falls
// back to the staging folder of the active worktree with a warning.
func TestOpenspecSave_RegisteredMainRootFailure(t *testing.T) {
	t.Run("output cap is an infra error", func(t *testing.T) {
		root, planPath := saveRootsFixture(t)
		writeMaterializeStaging(t, root, saveChange, map[string]string{"tasks.md": "- [ ] From active\n"})
		t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
		t.Chdir(root)
		fakeMainRoot(t, "", fmt.Errorf("worktree: could not determine main worktree: %w", execx.ErrOutputCap))

		res, text := callRegisteredTool(t, RegisterOpenspecTools, "openspec_save", map[string]any{"planPath": planPath})
		if !res.IsError {
			t.Fatalf("openspec_save succeeded, want an infra error:\n%s", text)
		}
		if !strings.Contains(text, "infra") || !strings.Contains(text, "openspec_save: resolve main worktree") {
			t.Errorf("error text = %q, want an infra error that names the main worktree resolution", text)
		}
		if _, err := os.Stat(filepath.Join(root, "openspec", "changes", saveChange)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("change folder exists (stat err %v), want none", err)
		}
	})

	t.Run("other error falls back with a warning", func(t *testing.T) {
		root, planPath := saveRootsFixture(t)
		writeMaterializeStaging(t, root, saveChange, map[string]string{"tasks.md": "- [ ] From active\n"})
		t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
		t.Chdir(root)
		fakeMainRoot(t, "", errors.New("no main"))

		res, text := callRegisteredTool(t, RegisterOpenspecTools, "openspec_save", map[string]any{"planPath": planPath})
		if res.IsError {
			t.Fatalf("openspec_save failed:\n%s", text)
		}
		for _, want := range []string{"- materialized: created\n", saveMainWarning} {
			if !strings.Contains(text, want) {
				t.Errorf("result missing %q:\n%s", want, text)
			}
		}
	})
}

// TestPlanSupport_RegisteredStageMainRootFailure checks the two failure paths
// of the main worktree resolution in the registered plan_support handler for
// openspec_stage: an output-cap error is an InfraError and writes nothing;
// any other error writes the staging folder under the active worktree and
// adds a warning to the summary that names the error and that root.
func TestPlanSupport_RegisteredStageMainRootFailure(t *testing.T) {
	args := map[string]any{
		"action":     "openspec_stage",
		"changeName": stagingChange,
		"files": []map[string]any{
			{"path": "proposal.md", "content": "# Proposal\n"},
			{"path": "tasks.md", "content": stagingTasks},
		},
	}
	setup := func(t *testing.T, mainErr error) string {
		t.Helper()
		stubOpenspecForStage(t, 0)
		root := newOpenspecStageFixture(t, "")
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(real))
		t.Chdir(real)
		fakeMainRoot(t, "", mainErr)
		return real
	}

	t.Run("output cap is an infra error", func(t *testing.T) {
		root := setup(t, fmt.Errorf("worktree: could not determine main worktree: %w", execx.ErrOutputCap))
		res, text := callRegisteredTool(t, RegisterPlanSupportTools, "plan_support", args)
		if !res.IsError {
			t.Fatalf("openspec_stage succeeded, want an infra error:\n%s", text)
		}
		if !strings.Contains(text, "infra") || !strings.Contains(text, "plan_support: resolve main worktree") {
			t.Errorf("error text = %q, want an infra error that names the main worktree resolution", text)
		}
		if _, err := os.Stat(filepath.Join(root, ".sdlc-v2", "openspec-staging", stagingChange)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("staging folder exists (stat err %v), want none", err)
		}
	})

	t.Run("other error stages under the active worktree with a warning", func(t *testing.T) {
		root := setup(t, errors.New("no main"))
		res, text := callRegisteredTool(t, RegisterPlanSupportTools, "plan_support", args)
		if res.IsError {
			t.Fatalf("openspec_stage failed:\n%s", text)
		}
		for _, want := range []string{
			"Warning: Main worktree not found (no main). Used " + root + " as the main root.",
			"- stagingRoot: " + root + "\n",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("result missing %q:\n%s", want, text)
			}
		}
		if _, err := os.Stat(filepath.Join(root, ".sdlc-v2", "openspec-staging", stagingChange, openspec.StageManifestFile)); err != nil {
			t.Errorf("stage.json under the active worktree is missing: %v", err)
		}
	})
}
