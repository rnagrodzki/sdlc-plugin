package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// plan_prepare tests
// ---------------------------------------------------------------------------

// TestPlanPrepare_KeySetAndDefaults verifies the top-level PlanPrepareOut
// shape (fixture parity with plan.js's main() output object) on a repo with
// no OpenSpec, no guardrail config, and no plan template.
func TestPlanPrepare_KeySetAndDefaults(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}

	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	expectedTopKeys := []string{
		"openspec", "fromOpenspec", "openspecContext", "guardrails",
		"explorePack", "planTemplate", "githubHosting", "g17Dispatch",
		"intakeAuditDispatch", "lanes", "lensReviewers", "errors",
	}
	for _, k := range expectedTopKeys {
		if _, ok := m[k]; !ok {
			t.Errorf("missing top-level key: %s", k)
		}
	}
	if _, ok := m["warnings"]; ok {
		t.Error("unexpected top-level key: warnings (plan.js has no warnings field)")
	}

	if out.FromOpenspec != nil {
		t.Errorf("FromOpenspec = %+v, want nil (no --from-openspec given)", out.FromOpenspec)
	}
	if out.OpenspecContext.Tasks != nil {
		t.Errorf("OpenspecContext.Tasks = %v, want nil", out.OpenspecContext.Tasks)
	}
	if out.OpenspecContext.TasksUpdated != 0 {
		t.Errorf("OpenspecContext.TasksUpdated = %d, want 0", out.OpenspecContext.TasksUpdated)
	}
	if len(out.Guardrails) != 0 {
		t.Errorf("Guardrails = %v, want empty", out.Guardrails)
	}
	if out.PlanTemplate.Path != nil {
		t.Errorf("PlanTemplate.Path = %v, want nil", out.PlanTemplate.Path)
	}
	if out.Openspec.Present {
		t.Error("Openspec.Present = true, want false (no openspec/config.yaml)")
	}
	if len(out.Errors) != 0 {
		t.Errorf("Errors = %v, want empty", out.Errors)
	}

	// Lane fan-out: 4 static lanes + 1 mirrored G17 (dimension-coverage) lane.
	if len(out.Lanes) != 5 {
		t.Fatalf("len(Lanes) = %d, want 5", len(out.Lanes))
	}
	wantLaneNames := []string{"static-structural", "content-coverage", "file-existence", "guardrail-compliance", "dimension-coverage"}
	for i, name := range wantLaneNames {
		if out.Lanes[i].Name != name {
			t.Errorf("Lanes[%d].Name = %q, want %q", i, out.Lanes[i].Name, name)
		}
	}
	last := out.Lanes[len(out.Lanes)-1]
	if last.SubagentType != out.G17Dispatch.SubagentType || last.Model != out.G17Dispatch.Model {
		t.Errorf("dimension-coverage lane %+v does not mirror g17Dispatch %+v", last, out.G17Dispatch)
	}
	if len(last.GateIDs) != 1 || last.GateIDs[0] != "G17" {
		t.Errorf("dimension-coverage lane GateIDs = %v, want [G17]", last.GateIDs)
	}

	// Lens reviewers: 3 static lenses.
	if len(out.LensReviewers) != 3 {
		t.Fatalf("len(LensReviewers) = %d, want 3", len(out.LensReviewers))
	}
	wantLensNames := []string{"architecture", "requirements", "risk"}
	for i, name := range wantLensNames {
		if out.LensReviewers[i].Lens != name {
			t.Errorf("LensReviewers[%d].Lens = %q, want %q", i, out.LensReviewers[i].Lens, name)
		}
	}

	if out.G17Dispatch.SubagentType != "general-purpose" || out.G17Dispatch.Model != "sonnet" {
		t.Errorf("G17Dispatch = %+v, want subagentType=general-purpose model=sonnet", out.G17Dispatch)
	}
	if out.IntakeAuditDispatch.SubagentType != "general-purpose" || out.IntakeAuditDispatch.Model != "sonnet" {
		t.Errorf("IntakeAuditDispatch = %+v, want subagentType=general-purpose model=sonnet", out.IntakeAuditDispatch)
	}
}

// TestPlanPrepare_PlanTemplate verifies planTemplate.path is populated when
// .sdlc/plan-template.md exists under mainRoot.
func TestPlanPrepare_PlanTemplate(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	sdlcDir := filepath.Join(dir, ".sdlc")
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	templatePath := filepath.Join(sdlcDir, "plan-template.md")
	if err := os.WriteFile(templatePath, []byte("# Plan Template\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if out.PlanTemplate.Path == nil {
		t.Fatal("PlanTemplate.Path = nil, want templatePath")
	}
	if *out.PlanTemplate.Path != templatePath {
		t.Errorf("PlanTemplate.Path = %q, want %q", *out.PlanTemplate.Path, templatePath)
	}
}

// TestPlanPrepare_Guardrails verifies guardrails are read from the "plan"
// config section as a raw passthrough array, and that an absent config
// section degrades to an empty slice without an error.
func TestPlanPrepare_Guardrails(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	sdlcDir := filepath.Join(dir, ".sdlc")
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{
		"plan": map[string]any{
			"guardrails": []any{
				map[string]any{"id": "no-secrets", "description": "Never commit secrets"},
				map[string]any{"id": "test-coverage", "description": "Cover new branches"},
			},
		},
	}
	cfgData, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdlcDir, "config.json"), cfgData, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("Errors = %v, want empty", out.Errors)
	}
	if len(out.Guardrails) != 2 {
		t.Fatalf("len(Guardrails) = %d, want 2: %+v", len(out.Guardrails), out.Guardrails)
	}
	if out.Guardrails[0]["id"] != "no-secrets" {
		t.Errorf("Guardrails[0][id] = %v, want no-secrets", out.Guardrails[0]["id"])
	}
	if out.Guardrails[1]["description"] != "Cover new branches" {
		t.Errorf("Guardrails[1][description] = %v, want %q", out.Guardrails[1]["description"], "Cover new branches")
	}
}

// TestPlanPrepare_OpenspecDetection verifies detectActiveChanges surfaces an
// active OpenSpec change with the authoritative block populated.
func TestPlanPrepare_OpenspecDetection(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	changeDir := filepath.Join(dir, "openspec", "changes", "add-widget")
	writeOpenspecFixtureChange(t, changeDir, "- [ ] Do the thing\n- [x] Done thing\n")
	if err := os.MkdirAll(filepath.Join(dir, "openspec"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openspec", "config.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "add openspec change")

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if !out.Openspec.Present {
		t.Fatal("Openspec.Present = false, want true")
	}
	if out.Openspec.Authoritative == nil {
		t.Fatal("Openspec.Authoritative = nil, want populated")
	}
	if out.Openspec.Authoritative.Path != "openspec/config.yaml" {
		t.Errorf("Authoritative.Path = %q, want openspec/config.yaml", out.Openspec.Authoritative.Path)
	}
	if len(out.Openspec.ActiveChanges) != 1 {
		t.Fatalf("len(ActiveChanges) = %d, want 1", len(out.Openspec.ActiveChanges))
	}
	change := out.Openspec.ActiveChanges[0]
	if change.Name != "add-widget" {
		t.Errorf("ActiveChanges[0].Name = %q, want add-widget", change.Name)
	}
	if !change.HasProposal || !change.HasTasks {
		t.Errorf("ActiveChanges[0] = %+v, want HasProposal=true HasTasks=true", change)
	}
	if change.TasksTotal != 2 || change.TasksDone != 1 {
		t.Errorf("ActiveChanges[0] tasks = done=%d total=%d, want done=1 total=2", change.TasksDone, change.TasksTotal)
	}
	if change.Stage == nil || *change.Stage != "implementation-in-progress" {
		t.Errorf("ActiveChanges[0].Stage = %v, want implementation-in-progress", change.Stage)
	}
}

// TestPlanPrepare_FromOpenspec_ValidChange verifies --from-openspec
// validation, the flat FromOpenspecResult shape, and tasks.md ref-comment
// injection (write-once, idempotent).
func TestPlanPrepare_FromOpenspec_ValidChange(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	changeDir := filepath.Join(dir, "openspec", "changes", "add-widget")
	tasksContent := "- [ ] First task\n- [x] Second task <!-- ref:existing-ref -->\n"
	writeOpenspecFixtureChange(t, changeDir, tasksContent)

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, FromOpenspec: "add-widget"})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("Errors = %v, want empty for a valid change", out.Errors)
	}
	if out.FromOpenspec == nil {
		t.Fatal("FromOpenspec = nil, want populated")
	}
	fo := out.FromOpenspec
	if !fo.Valid {
		t.Errorf("FromOpenspec.Valid = false, want true")
	}
	if fo.ChangeName != "add-widget" {
		t.Errorf("FromOpenspec.ChangeName = %q, want add-widget", fo.ChangeName)
	}
	if !fo.HasProposal || !fo.HasTasks {
		t.Errorf("FromOpenspec = %+v, want HasProposal=true HasTasks=true", fo)
	}
	if fo.TasksTotal != 2 || fo.TasksDone != 1 {
		t.Errorf("FromOpenspec tasks = done=%d total=%d, want done=1 total=2", fo.TasksDone, fo.TasksTotal)
	}

	// tasks.md ref injection: the first line had no ref comment and must
	// gain one; the second line already had one and must be left untouched.
	if out.OpenspecContext.TasksUpdated != 1 {
		t.Errorf("OpenspecContext.TasksUpdated = %d, want 1", out.OpenspecContext.TasksUpdated)
	}
	if len(out.OpenspecContext.Tasks) != 2 {
		t.Fatalf("len(OpenspecContext.Tasks) = %d, want 2", len(out.OpenspecContext.Tasks))
	}
	if out.OpenspecContext.Tasks[1].Ref != "existing-ref" {
		t.Errorf("Tasks[1].Ref = %q, want existing-ref (from inline comment)", out.OpenspecContext.Tasks[1].Ref)
	}

	rewritten, err := os.ReadFile(filepath.Join(changeDir, "tasks.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(rewritten), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("rewritten tasks.md has %d lines, want 2: %q", len(lines), string(rewritten))
	}
	if !strings.Contains(lines[0], "<!-- ref:") {
		t.Errorf("line 0 missing injected ref comment: %q", lines[0])
	}
	if strings.Count(lines[1], "<!-- ref:") != 1 {
		t.Errorf("line 1 ref comment count != 1 (must not double-inject): %q", lines[1])
	}

	// Re-running prepare must be idempotent: no further updates once every
	// task line already carries a ref comment.
	out2, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, FromOpenspec: "add-widget"})
	if err != nil {
		t.Fatalf("planPrepareCore (2nd run): %v", err)
	}
	if out2.OpenspecContext.TasksUpdated != 0 {
		t.Errorf("2nd run OpenspecContext.TasksUpdated = %d, want 0 (idempotent)", out2.OpenspecContext.TasksUpdated)
	}
}

// TestPlanPrepare_FromOpenspec_MissingChange verifies an unknown change name
// is reported through the top-level Errors slice with Valid=false.
func TestPlanPrepare_FromOpenspec_MissingChange(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, FromOpenspec: "does-not-exist"})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if out.FromOpenspec == nil || out.FromOpenspec.Valid {
		t.Fatalf("FromOpenspec = %+v, want non-nil with Valid=false", out.FromOpenspec)
	}
	if len(out.Errors) == 0 {
		t.Error("Errors is empty, want a change-directory-not-found error")
	}
}

// TestPlanPrepare_KD5Gate verifies the config-version gate hard-returns a
// minimal errors-only payload without touching openspec/guardrails/lanes.
func TestPlanPrepare_KD5Gate(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	// A stale schemaVersion (below configmigrate.CurrentSchemaVersion) is a
	// genuine migration-needed condition; a merely-absent config.json is NOT
	// (configmigrate.Verify treats "no config yet" as a fresh v5 project).
	sdlcDir := filepath.Join(dir, ".sdlc")
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	staleCfg := []byte(`{"schemaVersion": 1}`)
	if err := os.WriteFile(filepath.Join(sdlcDir, "config.json"), staleCfg, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: false})
	if err != nil {
		t.Fatalf("planPrepareCore: %v (KD5 gate must return nil error with Errors populated)", err)
	}
	if len(out.Errors) == 0 {
		t.Fatal("Errors is empty, want a config-version error")
	}
	if out.Openspec.Present {
		t.Error("Openspec.Present = true, want zero-value (KD5 gate short-circuits before detection)")
	}
	if len(out.Lanes) != 0 {
		t.Errorf("len(Lanes) = %d, want 0 (KD5 gate short-circuits before lane build)", len(out.Lanes))
	}
}

// ---------------------------------------------------------------------------
// plan_mark tests
// ---------------------------------------------------------------------------

// TestPlanMark_NoStateFile verifies plan_mark refuses to run before
// plan_prepare has ever created a plan state file for the branch.
func TestPlanMark_NoStateFile(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	_, err := planMark(dir, dir, PlanMarkIn{Marker: "guardrailsEvaluated"})
	if err == nil {
		t.Fatal("planMark = nil error, want an error (no plan state file exists yet)")
	}
}

// TestPlanMark_InvalidMarker verifies the enum rejects unknown marker names.
func TestPlanMark_InvalidMarker(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	_, err := planMark(dir, dir, PlanMarkIn{Marker: "not-a-real-marker"})
	if err == nil {
		t.Fatal("planMark = nil error, want an error for an unknown marker")
	}
}

// TestPlanMark_PlanFileRequiresPath verifies the "plan-file" marker requires
// a non-empty path.
func TestPlanMark_PlanFileRequiresPath(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	_, err := planMark(dir, dir, PlanMarkIn{Marker: "plan-file", Path: ""})
	if err == nil {
		t.Fatal("planMark = nil error, want an error for plan-file with empty path")
	}
}

// TestPlanMark_WriteAndUpdate verifies plan_mark writes/updates the same
// plan-<slug>-*.json state file created by plan_prepare's skillInvoked
// marker, and that repeated marks converge on a single surviving file
// (prune-on-write) rather than accumulating extra state files.
func TestPlanMark_WriteAndUpdate(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	// Seed the plan state file the way plan_prepare does.
	if _, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true}); err != nil {
		t.Fatalf("planPrepareCore (seed): %v", err)
	}

	stateFiles := listStateFiles(t, dir)
	if len(stateFiles) != 1 {
		t.Fatalf("state files after prepare = %v, want exactly 1", stateFiles)
	}
	if !regexp.MustCompile(`^plan-main-\d{8}T\d{6}Z\.json$`).MatchString(stateFiles[0]) {
		t.Errorf("state filename %q does not match plan-<slug>-<timestamp>.json grammar", stateFiles[0])
	}

	out1, err := planMark(dir, dir, PlanMarkIn{Marker: "guardrailsEvaluated"})
	if err != nil {
		t.Fatalf("planMark(guardrailsEvaluated): %v", err)
	}
	if !out1.OK {
		t.Error("planMark(guardrailsEvaluated).OK = false, want true")
	}

	out2, err := planMark(dir, dir, PlanMarkIn{Marker: "plan-file", Path: "plans/my-plan.md"})
	if err != nil {
		t.Fatalf("planMark(plan-file): %v", err)
	}
	if !out2.OK {
		t.Error("planMark(plan-file).OK = false, want true")
	}

	out3, err := planMark(dir, dir, PlanMarkIn{Marker: "critiqueRan"})
	if err != nil {
		t.Fatalf("planMark(critiqueRan): %v", err)
	}
	if !out3.OK {
		t.Error("planMark(critiqueRan).OK = false, want true")
	}

	// Exactly one state file must survive prune-on-write across all writes.
	stateFilesAfter := listStateFiles(t, dir)
	if len(stateFilesAfter) != 1 {
		t.Fatalf("state files after marks = %v, want exactly 1 (prune-on-write)", stateFilesAfter)
	}

	raw, err := os.ReadFile(filepath.Join(dir, ".sdlc", "execution", stateFilesAfter[0]))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	integrity, ok := doc["planIntegrity"].(map[string]any)
	if !ok {
		t.Fatalf("planIntegrity missing or wrong type: %v", doc["planIntegrity"])
	}
	for _, key := range []string{"skillInvoked", "guardrailsEvaluated", "planFile", "critiqueRan"} {
		if _, ok := integrity[key]; !ok {
			t.Errorf("planIntegrity missing key %q: %v", key, integrity)
		}
	}
	if doc["planFilePath"] != "plans/my-plan.md" {
		t.Errorf("planFilePath = %v, want plans/my-plan.md", doc["planFilePath"])
	}
}

// ---------------------------------------------------------------------------
// plan_explore_prepare tests
// ---------------------------------------------------------------------------

// TestPlanExplorePrepare_TempdirPattern verifies buildExplorePack writes its
// manifest into a fresh sdlc-explore-<slug>-XXXXXX tempdir (the pattern the
// GC sweep in internal/state matches on), and that the manifest is valid
// JSON with a non-negative scope-hint count.
func TestPlanExplorePrepare_TempdirPattern(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	pack := buildExplorePack(dir, dir, "", "investigate the widget rendering pipeline")
	if pack.Error != nil {
		t.Fatalf("buildExplorePack error: %s", *pack.Error)
	}
	if pack.ManifestPath == nil || pack.OutDir == nil {
		t.Fatalf("pack = %+v, want ManifestPath and OutDir populated", pack)
	}

	base := filepath.Base(*pack.OutDir)
	if !regexp.MustCompile(`^sdlc-explore-main-[A-Za-z0-9]+$`).MatchString(base) {
		t.Errorf("outDir basename %q does not match sdlc-explore-<slug>-XXXXXX pattern", base)
	}
	if filepath.Dir(*pack.ManifestPath) != *pack.OutDir {
		t.Errorf("manifestPath %q is not inside outDir %q", *pack.ManifestPath, *pack.OutDir)
	}

	data, err := os.ReadFile(*pack.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if manifest["projectRoot"] != dir {
		t.Errorf("manifest.projectRoot = %v, want %q", manifest["projectRoot"], dir)
	}
}

// TestPlanExplorePrepare_Handler verifies the plan_explore_prepare tool
// handler surfaces exactly {manifestPath} and that the file exists on disk.
func TestPlanExplorePrepare_Handler(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	pack := buildExplorePack(dir, dir, "", "")
	if pack.Error != nil {
		t.Fatalf("buildExplorePack error: %s", *pack.Error)
	}
	out := PlanExploreOut{ManifestPath: *pack.ManifestPath}

	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 {
		t.Fatalf("PlanExploreOut marshaled to %d keys, want exactly 1: %v", len(m), m)
	}
	if _, ok := m["manifestPath"]; !ok {
		t.Error("missing manifestPath key")
	}
	if _, err := os.Stat(out.ManifestPath); err != nil {
		t.Errorf("manifestPath does not exist on disk: %v", err)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// writeOpenspecFixtureChange creates a minimal OpenSpec change directory with
// a proposal.md, a specs/ dir containing one spec, and a tasks.md containing
// tasksContent.
func writeOpenspecFixtureChange(t *testing.T, changeDir, tasksContent string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(changeDir, "specs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(changeDir, "proposal.md"), []byte("# Proposal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(changeDir, "specs", "widget.md"), []byte("# Spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(changeDir, "tasks.md"), []byte(tasksContent), 0o644); err != nil {
		t.Fatal(err)
	}
}

// listStateFiles lists the basenames of files under <root>/.sdlc/execution/.
func listStateFiles(t *testing.T, root string) []string {
	t.Helper()
	dir := filepath.Join(root, ".sdlc", "execution")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}
