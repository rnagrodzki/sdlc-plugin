package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
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
		"style", "tasks",
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

// TestPlanPrepare_UserPromptForwarded verifies UserPrompt flows through to
// buildExplorePack (surfaced in the written manifest's userPromptLength)
// instead of the previously hardcoded empty string.
func TestPlanPrepare_UserPromptForwarded(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	const prompt = "fix the login bug"
	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, UserPrompt: prompt})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if out.ExplorePack.ManifestPath == nil {
		t.Fatalf("ExplorePack.ManifestPath is nil: %+v", out.ExplorePack)
	}
	data, err := os.ReadFile(*out.ExplorePack.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if got, want := manifest["userPromptLength"], float64(len(prompt)); got != want {
		t.Errorf("manifest.userPromptLength = %v, want %v", got, want)
	}
}

// TestPlanPrepare_EmptyUserPromptBackwardCompatible verifies that omitting
// UserPrompt behaves identically to the prior hardcoded-empty-string
// behavior (manifest userPromptLength stays 0).
func TestPlanPrepare_EmptyUserPromptBackwardCompatible(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if out.ExplorePack.ManifestPath == nil {
		t.Fatalf("ExplorePack.ManifestPath is nil: %+v", out.ExplorePack)
	}
	data, err := os.ReadFile(*out.ExplorePack.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if got := manifest["userPromptLength"]; got != float64(0) {
		t.Errorf("manifest.userPromptLength = %v, want 0 for empty UserPrompt", got)
	}
}

// TestPlanPrepare_PlanTemplate verifies planTemplate.path is populated when
// .sdlc/plan-template.md exists under mainRoot.
func TestPlanPrepare_PlanTemplate(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	sdlcDir := filepath.Join(dir, paths.DataDir)
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

	writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), ""+
		"[plan.guardrails.no-secrets]\n"+
		"description = \"Never commit secrets\"\n"+
		"\n"+
		"[plan.guardrails.test-coverage]\n"+
		"description = \"Cover new branches\"\n")

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

// TestPlanPrepare_StyleAndTasksDefaults verifies loadPlanStyle and
// loadPlanTasks's zero-config defaults surface through PlanPrepareOut:
// standard verbosity, technical audience, no narrative rules, no required
// fields, and the "full" contract shape.
func TestPlanPrepare_StyleAndTasksDefaults(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if out.Style.Verbosity != "standard" {
		t.Errorf("Style.Verbosity = %q, want standard", out.Style.Verbosity)
	}
	if out.Style.Audience != "technical" {
		t.Errorf("Style.Audience = %q, want technical", out.Style.Audience)
	}
	if len(out.Style.NarrativeRules) != 0 {
		t.Errorf("Style.NarrativeRules = %v, want empty", out.Style.NarrativeRules)
	}
	if len(out.Style.Instructions) != 0 {
		t.Errorf("Style.Instructions = %v, want empty", out.Style.Instructions)
	}
	if len(out.Tasks.RequiredFields) != 0 {
		t.Errorf("Tasks.RequiredFields = %v, want empty", out.Tasks.RequiredFields)
	}
	if out.Tasks.ContractShape != "full" {
		t.Errorf("Tasks.ContractShape = %q, want full", out.Tasks.ContractShape)
	}
}

// TestPlanPrepare_StyleAndTasksPopulated verifies planStyle (from
// .sdlc-v2/local.toml) and plan.tasks (from .sdlc-v2/config.toml) values
// round-trip into PlanPrepareOut.Style and PlanPrepareOut.Tasks unchanged.
func TestPlanPrepare_StyleAndTasksPopulated(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), ""+
		"[planStyle]\n"+
		"verbosity = \"detailed\"\n"+
		"audience = \"business\"\n"+
		"narrativeRules = [\"Lead with impact\", \"Avoid jargon\"]\n"+
		"instructions = [\"Cite file:line for every claim about existing code.\"]\n")

	writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), ""+
		"[plan.tasks]\n"+
		"requiredFields = [\"Owner\", \"Rollback\"]\n"+
		"contractShape = \"minimal\"\n")

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if out.Style.Verbosity != "detailed" {
		t.Errorf("Style.Verbosity = %q, want detailed", out.Style.Verbosity)
	}
	if out.Style.Audience != "business" {
		t.Errorf("Style.Audience = %q, want business", out.Style.Audience)
	}
	wantRules := []string{"Lead with impact", "Avoid jargon"}
	if !reflect.DeepEqual(out.Style.NarrativeRules, wantRules) {
		t.Errorf("Style.NarrativeRules = %v, want %v", out.Style.NarrativeRules, wantRules)
	}
	wantInstructions := []string{"Cite file:line for every claim about existing code."}
	if !reflect.DeepEqual(out.Style.Instructions, wantInstructions) {
		t.Errorf("Style.Instructions = %v, want %v", out.Style.Instructions, wantInstructions)
	}
	wantFields := []string{"Owner", "Rollback"}
	if !reflect.DeepEqual(out.Tasks.RequiredFields, wantFields) {
		t.Errorf("Tasks.RequiredFields = %v, want %v", out.Tasks.RequiredFields, wantFields)
	}
	if out.Tasks.ContractShape != "minimal" {
		t.Errorf("Tasks.ContractShape = %q, want minimal", out.Tasks.ContractShape)
	}
}

// TestPlanPrepare_StyleInstructionsFiltering verifies loadPlanStyle drops
// non-string and blank instructions entries and trims the survivors, mirroring
// how narrativeRules is filtered but with the added trim/blank-drop step.
func TestPlanPrepare_StyleInstructionsFiltering(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), ""+
		"[planStyle]\n"+
		"instructions = [\"A\", \"  \", 3, \" B \"]\n")

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	want := []string{"A", "B"}
	if !reflect.DeepEqual(out.Style.Instructions, want) {
		t.Errorf("Style.Instructions = %v, want %v (non-strings and blank entries dropped, survivors trimmed)", out.Style.Instructions, want)
	}
}

// TestLocalTemplatePlanStyleKeysMatchSchema verifies every active [planStyle]
// key shipped in localTemplate is declared as a property of
// $defs.planStyleSection in the local-config JSON Schema, and that the
// schema declares "instructions" even though the template only ships it as a
// commented-out example (so it would not otherwise be caught by the
// key-parity loop below).
func TestLocalTemplatePlanStyleKeysMatchSchema(t *testing.T) {
	var local map[string]any
	if err := toml.Unmarshal([]byte(localTemplate), &local); err != nil {
		t.Fatalf("localTemplate is not valid TOML: %v", err)
	}
	planStyle, ok := local["planStyle"].(map[string]any)
	if !ok {
		t.Fatal("localTemplate has no [planStyle] table")
	}

	schemaPath := "../../plugins/sdlc/schemas/sdlc-local.schema.json"
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%s): %v", schemaPath, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	defs, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatal("schema has no $defs")
	}
	section, ok := defs["planStyleSection"].(map[string]any)
	if !ok {
		t.Fatal("schema has no $defs.planStyleSection")
	}
	properties, ok := section["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no $defs.planStyleSection.properties")
	}

	for key := range planStyle {
		if _, ok := properties[key]; !ok {
			t.Errorf("[planStyle] key %q in localTemplate is not a property of $defs.planStyleSection in the schema", key)
		}
	}

	if _, ok := properties["instructions"]; !ok {
		t.Error(`$defs.planStyleSection.properties has no "instructions" property`)
	}
}

// TestPlanPrepare_TasksRequiredFieldsDedup verifies requiredFields entries
// that duplicate one of the five fields already guaranteed by the task
// contract's fixed shape (Complexity, Risk, Files, Verify, Depends on) are
// silently dropped, while non-overlapping custom fields are retained.
func TestPlanPrepare_TasksRequiredFieldsDedup(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), ""+
		"[plan.tasks]\n"+
		"requiredFields = [\"Complexity\", \"Risk\", \"Files\", \"Verify\", \"Depends on\", \"Owner\", \"Rollback\"]\n")

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	want := []string{"Owner", "Rollback"}
	if !reflect.DeepEqual(out.Tasks.RequiredFields, want) {
		t.Errorf("Tasks.RequiredFields = %v, want %v (core 5 fields deduped)", out.Tasks.RequiredFields, want)
	}
}

// TestPlanPrepare_TasksContractShapeEnum verifies every documented
// contractShape enum value (full, minimal, none) is accepted and
// round-trips through PlanPrepareOut.Tasks.ContractShape unchanged.
func TestPlanPrepare_TasksContractShapeEnum(t *testing.T) {
	for _, shape := range []string{"full", "minimal", "none"} {
		t.Run(shape, func(t *testing.T) {
			dir := t.TempDir()
			initGitFixture(t, dir)
			gitCommit(t, dir, "initial")

			writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), fmt.Sprintf(
				"[plan.tasks]\ncontractShape = %q\n", shape))

			out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true})
			if err != nil {
				t.Fatalf("planPrepareCore: %v", err)
			}
			if out.Tasks.ContractShape != shape {
				t.Errorf("Tasks.ContractShape = %q, want %q", out.Tasks.ContractShape, shape)
			}
		})
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
// validation, the flat FromOpenspecResult shape, and the ref-stamp
// relocation: planPrepareCore only computes a PENDING count and must not
// touch tasks.md on disk (plan mode's no-tracked-file-write contract); the
// stamp itself is applied later by execute_state({action:"init"}), and only
// then does a second planPrepareCore report 0 pending (idempotent).
func TestPlanPrepare_FromOpenspec_ValidChange(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), "")

	changeDir := filepath.Join(dir, "openspec", "changes", "add-widget")
	tasksContent := "- [ ] First task\n- [x] Second task <!-- ref:existing-ref -->\n"
	writeOpenspecFixtureChange(t, changeDir, tasksContent)

	// Step 1: planPrepareCore computes the pending ref stamps but writes
	// nothing to disk.
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

	// The first task line has no ref comment (1 pending); the second
	// already has one and must not be double-counted.
	if out.OpenspecContext.TasksUpdated != 1 {
		t.Errorf("OpenspecContext.TasksUpdated = %d, want 1 (pending)", out.OpenspecContext.TasksUpdated)
	}
	if len(out.OpenspecContext.Tasks) != 2 {
		t.Fatalf("len(OpenspecContext.Tasks) = %d, want 2", len(out.OpenspecContext.Tasks))
	}
	if out.OpenspecContext.Tasks[1].Ref != "existing-ref" {
		t.Errorf("Tasks[1].Ref = %q, want existing-ref (from inline comment)", out.OpenspecContext.Tasks[1].Ref)
	}

	unchanged, err := os.ReadFile(filepath.Join(changeDir, "tasks.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != tasksContent {
		t.Errorf("tasks.md changed on disk after planPrepareCore; want untouched (plan mode must not write tracked files): got %q, want %q", string(unchanged), tasksContent)
	}

	// Step 2: execute_state({action:"init"}) is what actually stamps the
	// file, keyed off the plan document's "**Source:**" header.
	planPath := filepath.Join(dir, "plan.md")
	planContent := "# Widget Implementation Plan\n\n**Goal:** Add a widget\n**Source:** openspec/changes/add-widget/\n"
	writeFile(t, planPath, planContent)

	if _, err := executeState(dir, dir, ExecuteStateIn{
		Action:   "init",
		Branch:   "feat/add-widget",
		Quality:  "standard",
		PlanPath: planPath,
	}, fixedClock(testNow)); err != nil {
		t.Fatalf("execute_state init: %v", err)
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

	// Step 3: now that execute's init has stamped the file, a fresh
	// planPrepareCore run must report 0 pending — genuinely idempotent.
	out2, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, FromOpenspec: "add-widget"})
	if err != nil {
		t.Fatalf("planPrepareCore (2nd run): %v", err)
	}
	if out2.OpenspecContext.TasksUpdated != 0 {
		t.Errorf("2nd run OpenspecContext.TasksUpdated = %d, want 0 (idempotent, already stamped by execute init)", out2.OpenspecContext.TasksUpdated)
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

// TestOpenspecChangeFromPlan verifies the plan-document "**Source:**" header
// parser that execute_state's init handler uses to find which openspec
// change to ref-stamp. It returns "" whenever there is nothing safe to act
// on: no header, the unfilled "[TBD]" placeholder, or a non-openspec source.
// A bare change-name segment (e.g. "..") is returned verbatim — traversal
// safety is deliberately NOT duplicated here; the init call site gates the
// result through isSafeChangeName before using it (see execActionInit).
func TestOpenspecChangeFromPlan(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"valid header with trailing slash", "# Plan\n\n**Source:** openspec/changes/add-widget/\n", "add-widget"},
		{"valid header without trailing slash", "**Source:** openspec/changes/add-widget\n", "add-widget"},
		{"no header at all", "# Plan\n\n**Goal:** something\n", ""},
		{"unfilled TBD placeholder", "**Source:** [TBD]\n", ""},
		{"non-openspec source", "**Source:** conversation context\n", ""},
		{"traversal-shaped segment returned raw", "**Source:** openspec/changes/../\n", ".."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := openspecChangeFromPlan(tc.content)
			if got != tc.want {
				t.Errorf("openspecChangeFromPlan(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}

	// Close the loop: the traversal-shaped segment above is exactly what
	// isSafeChangeName must reject, so execActionInit's
	// `change != "" && isSafeChangeName(change)` guard does nothing with it.
	if isSafeChangeName("..") {
		t.Error(`isSafeChangeName("..") = true, want false — init's traversal guard would not fire`)
	}
}

// TestStampTaskRefs_WriteOnceIdempotent verifies stampTaskRefs (called by
// execute_state's init handler) only writes when a task line actually gains
// a ref comment, and reports 0 on a second call against the already-stamped
// file.
func TestStampTaskRefs_WriteOnceIdempotent(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.md")
	original := "- [ ] First task\n- [x] Second task <!-- ref:existing-ref -->\n"
	if err := os.WriteFile(tasksPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	updated, err := stampTaskRefs(tasksPath)
	if err != nil {
		t.Fatalf("stampTaskRefs: %v", err)
	}
	if updated != 1 {
		t.Errorf("updated = %d, want 1", updated)
	}

	updated2, err := stampTaskRefs(tasksPath)
	if err != nil {
		t.Fatalf("stampTaskRefs (2nd call): %v", err)
	}
	if updated2 != 0 {
		t.Errorf("2nd call updated = %d, want 0 (write-once, idempotent)", updated2)
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
	sdlcDir := filepath.Join(dir, paths.DataDir)
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

	raw, err := os.ReadFile(filepath.Join(dir, paths.DataDir, paths.RunsSubdir, stateFilesAfter[0]))
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

// readSoleStateDoc reads the single surviving plan-<slug>-*.json state file
// under root and unmarshals it, failing the test if zero or more than one
// file exists.
func readSoleStateDoc(t *testing.T, root string) map[string]any {
	t.Helper()
	files := listStateFiles(t, root)
	if len(files) != 1 {
		t.Fatalf("state files = %v, want exactly 1", files)
	}
	raw, err := os.ReadFile(filepath.Join(root, paths.DataDir, paths.RunsSubdir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// TestPlanPrepare_CreationIntent_FirstCallThenResolveTemplate verifies the
// first plan_prepare call writes creationIntent {userPrompt, timestamp}
// only, and a following resolveTemplate:true call reuses the same run (same
// runId, one state file) and adds scope, routing and flags.
func TestPlanPrepare_CreationIntent_FirstCallThenResolveTemplate(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	const prompt = "fix the login bug"
	first, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, UserPrompt: prompt})
	if err != nil {
		t.Fatalf("planPrepareCore (first call): %v", err)
	}
	if first.RunID == "" {
		t.Fatal("RunID empty after first call")
	}
	if first.Next != planPrepareNextFirst {
		t.Errorf("Next = %q, want %q", first.Next, planPrepareNextFirst)
	}
	doc := readSoleStateDoc(t, dir)
	firstIntent, ok := doc["creationIntent"].(map[string]any)
	if !ok {
		t.Fatalf("creationIntent missing after first call: %v", doc)
	}
	if got := planTestKeys(firstIntent); !reflect.DeepEqual(got, []string{"timestamp", "userPrompt"}) {
		t.Errorf("first-call creationIntent keys = %v, want [timestamp userPrompt]", got)
	}
	if firstIntent["userPrompt"] != prompt {
		t.Errorf("first-call creationIntent.userPrompt = %v, want %q", firstIntent["userPrompt"], prompt)
	}
	integrity, _ := doc["planIntegrity"].(map[string]any)
	if _, ok := integrity["skillInvoked"]; !ok {
		t.Fatalf("planIntegrity.skillInvoked missing after first call: %v", doc)
	}

	second, err := planPrepareCore(dir, dir, PlanPrepareIn{
		SkipConfigCheck: true, ResolveTemplate: true, UserPrompt: prompt, FileCount: 2,
	})
	if err != nil {
		t.Fatalf("planPrepareCore (resolveTemplate call): %v", err)
	}
	if second.RunID != first.RunID {
		t.Errorf("resolveTemplate RunID = %q, want %q (same run)", second.RunID, first.RunID)
	}
	if second.Next != planPrepareNextTmpl {
		t.Errorf("Next = %q, want %q", second.Next, planPrepareNextTmpl)
	}

	doc = readSoleStateDoc(t, dir) // still exactly one file
	integrity, _ = doc["planIntegrity"].(map[string]any)
	if _, ok := integrity["skillInvoked"]; !ok {
		t.Errorf("planIntegrity.skillInvoked missing after resolveTemplate call: %v", doc)
	}

	intent, ok := doc["creationIntent"].(map[string]any)
	if !ok {
		t.Fatalf("creationIntent missing or wrong type after resolveTemplate call: %v", doc["creationIntent"])
	}
	if intent["userPrompt"] != prompt {
		t.Errorf("creationIntent.userPrompt = %v, want %q", intent["userPrompt"], prompt)
	}
	if intent["scope"] != "lightweight" {
		t.Errorf("creationIntent.scope = %v, want %q (2 files -> lightweight routing)", intent["scope"], "lightweight")
	}
	if s, ok := intent["routing"].(string); !ok || s == "" {
		t.Errorf("creationIntent.routing = %v, want a non-empty string", intent["routing"])
	}
	if s, ok := intent["timestamp"].(string); !ok || s == "" {
		t.Errorf("creationIntent.timestamp = %v, want a non-empty string", intent["timestamp"])
	}
	flags, ok := intent["flags"].(map[string]any)
	if !ok {
		t.Fatalf("creationIntent.flags missing: %v", intent)
	}
	wantFlags := map[string]any{
		"fromOpenspec": "", "fromOpenspecDirect": false, "openspecInlineGenerate": false,
		"lightweight": false, "fileCount": float64(2),
	}
	if !reflect.DeepEqual(flags, wantFlags) {
		t.Errorf("creationIntent.flags = %v, want %v", flags, wantFlags)
	}
}

// ---------------------------------------------------------------------------
// plan_prepare run selection, resume mode, guardrails file
// ---------------------------------------------------------------------------

func planTestKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func planTestRunsDir(root string) string {
	return filepath.Join(root, paths.DataDir, paths.RunsSubdir)
}

// planTestSeedRun writes a plan state file <runID>.json with data, and an
// evidence directory holding one file.
func planTestSeedRun(t *testing.T, root, runID string, data map[string]any) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(planTestRunsDir(root), runID+".json"), string(raw))
	writeFile(t, filepath.Join(planTestRunsDir(root), runID+".evidence", "note.md"), "old evidence")
}

func planTestActiveData() map[string]any {
	return map[string]any{"planIntegrity": map[string]any{"skillInvoked": "2020-01-01T00:00:00Z"}}
}

func planTestGitRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	if branch != "main" {
		runGit(t, dir, "checkout", "-b", branch)
	}
	return dir
}

// planTestTemplateRepo is planTestGitRepo on main with a project plan
// template, so template resolution does not depend on the shipped default.
func planTestTemplateRepo(t *testing.T) string {
	t.Helper()
	dir := planTestGitRepo(t, "main")
	writeProjectPlanTemplate(t, dir, planTemplateResolveFixture)
	return dir
}

// TestPlanPrepare_FirstCallStartsNewRunAndPrunesOld verifies a first call
// with an older run on the branch returns a new runId, and removes the
// older run's state file and .evidence/ directory.
func TestPlanPrepare_FirstCallStartsNewRunAndPrunesOld(t *testing.T) {
	dir := planTestGitRepo(t, "main")
	const oldRun = "plan-main-20200101T000000Z"
	planTestSeedRun(t, dir, oldRun, planTestActiveData())

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if out.RunID == "" || out.RunID == oldRun {
		t.Fatalf("RunID = %q, want a new run ID", out.RunID)
	}
	if _, err := os.Stat(filepath.Join(planTestRunsDir(dir), oldRun+".evidence")); !os.IsNotExist(err) {
		t.Errorf("old evidence dir still exists (stat err = %v)", err)
	}
	if files := listStateFiles(t, dir); !reflect.DeepEqual(files, []string{out.RunID + ".json"}) {
		t.Errorf("state files = %v, want only %s.json", files, out.RunID)
	}
	if _, err := os.Stat(out.GuardrailsFile); err != nil {
		t.Errorf("guardrails file not written: %v", err)
	}
}

// TestPlanPrepare_ResolveTemplateWithoutRunCreatesRun verifies a
// resolveTemplate:true call with no active run creates the run itself.
func TestPlanPrepare_ResolveTemplateWithoutRunCreatesRun(t *testing.T) {
	dir := planTestGitRepo(t, "main")

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true, UserPrompt: "p", FileCount: 12})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if out.RunID == "" {
		t.Fatal("RunID empty")
	}
	doc := readSoleStateDoc(t, dir)
	integrity, _ := doc["planIntegrity"].(map[string]any)
	if _, ok := integrity["skillInvoked"]; !ok {
		t.Errorf("planIntegrity.skillInvoked missing: %v", doc)
	}
	intent, _ := doc["creationIntent"].(map[string]any)
	if _, ok := intent["flags"]; !ok {
		t.Errorf("creationIntent.flags missing: %v", doc)
	}
	want := filepath.Join(planTestRunsDir(dir), out.RunID+".evidence", "guardrails.md")
	if out.GuardrailsFile != want {
		t.Errorf("GuardrailsFile = %q, want %q", out.GuardrailsFile, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("guardrails.md missing: %v", err)
	}
}

// TestPlanPrepare_ExactSlugRunSelection verifies that on branch feat an
// active plan-feat-x-* run is reused by neither resolveTemplate nor resume.
func TestPlanPrepare_ExactSlugRunSelection(t *testing.T) {
	dir := planTestGitRepo(t, "feat")
	const other = "plan-feat-x-20200101T000000Z"
	planTestSeedRun(t, dir, other, planTestActiveData())

	_, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, Resume: true})
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("resume err = %T %v, want *mcpserver.DomainError", err, err)
	}

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if out.RunID == other || !strings.HasPrefix(out.RunID, "plan-feat-2") {
		t.Errorf("RunID = %q, want a new plan-feat-<ts> run", out.RunID)
	}
	if _, err := os.Stat(filepath.Join(planTestRunsDir(dir), other+".json")); err != nil {
		t.Errorf("other branch's run was touched: %v", err)
	}
}

// TestPlanPrepare_ResumeReusesActiveRun verifies resume:true reuses the
// active run without a state write, applies the saved userPrompt and flags
// over the input, skips the explore pack, and returns the resume next text.
func TestPlanPrepare_ResumeReusesActiveRun(t *testing.T) {
	dir := planTestTemplateRepo(t)
	const prompt = "saved prompt"

	if _, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, UserPrompt: prompt}); err != nil {
		t.Fatal(err)
	}
	prev, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true, UserPrompt: prompt, FileCount: 12})
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(planTestRunsDir(dir), prev.RunID+".json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}

	in := PlanPrepareIn{SkipConfigCheck: true, Resume: true, UserPrompt: "different", FileCount: 1, Lightweight: true}
	out, err := planPrepareCore(dir, dir, in)
	if err != nil {
		t.Fatalf("planPrepareCore (resume): %v", err)
	}
	if out.RunID != prev.RunID {
		t.Errorf("RunID = %q, want %q", out.RunID, prev.RunID)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("state file changed on resume:\nbefore=%s\nafter=%s", before, after)
	}
	if out.Template == nil {
		t.Fatal("Template nil on resume")
	}
	if out.Template.Routing.FileCount != 12 || out.Template.PipelineMode != "full" {
		t.Errorf("routing = %+v, want saved fileCount 12 / full", out.Template.Routing)
	}
	wantNext := planPrepareResumeNext(prev.RunID)
	if out.Next != wantNext || out.Template.Next != wantNext {
		t.Errorf("Next = %q, Template.Next = %q, want %q", out.Next, out.Template.Next, wantNext)
	}
	if out.ExplorePack != (ExplorePack{}) {
		t.Errorf("ExplorePack = %+v, want zero value", out.ExplorePack)
	}

	_, eff, err := selectPlanRun(dir, dir, in)
	if err != nil {
		t.Fatal(err)
	}
	if eff.UserPrompt != prompt || eff.FileCount != 12 || eff.Lightweight || !eff.ResolveTemplate {
		t.Errorf("effective input = %+v, want saved prompt/flags and resolveTemplate", eff)
	}
}

// TestPlanPrepare_ResumeWithoutCreationIntentUsesInput verifies resume on
// an active run with no creationIntent keeps the input values.
func TestPlanPrepare_ResumeWithoutCreationIntentUsesInput(t *testing.T) {
	dir := planTestTemplateRepo(t)
	planTestSeedRun(t, dir, "plan-main-20200101T000000Z", planTestActiveData())

	in := PlanPrepareIn{SkipConfigCheck: true, Resume: true, UserPrompt: "input prompt", FileCount: 12}
	out, err := planPrepareCore(dir, dir, in)
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if out.RunID != "plan-main-20200101T000000Z" {
		t.Errorf("RunID = %q", out.RunID)
	}
	if out.Template == nil || out.Template.Routing.FileCount != 12 {
		t.Errorf("Template = %+v, want input fileCount 12", out.Template)
	}
	_, eff, err := selectPlanRun(dir, dir, in)
	if err != nil {
		t.Fatal(err)
	}
	if eff.UserPrompt != "input prompt" {
		t.Errorf("UserPrompt = %q, want input value", eff.UserPrompt)
	}
}

// TestPlanPrepare_ResumeImpliesResolveTemplate verifies resume:true alone
// returns the same template fields as resume:true, resolveTemplate:true.
func TestPlanPrepare_ResumeImpliesResolveTemplate(t *testing.T) {
	dir := planTestTemplateRepo(t)
	planTestSeedRun(t, dir, "plan-main-20200101T000000Z", planTestActiveData())

	a, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, Resume: true, FileCount: 5})
	if err != nil {
		t.Fatal(err)
	}
	b, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, Resume: true, ResolveTemplate: true, FileCount: 5})
	if err != nil {
		t.Fatal(err)
	}
	if a.Template == nil || !reflect.DeepEqual(a.Template, b.Template) {
		t.Errorf("templates differ:\nresume=%+v\nresume+resolveTemplate=%+v", a.Template, b.Template)
	}
}

// TestPlanPrepare_ResumeNoActiveRun verifies resume:true fails with a
// DomainError when there is no run, or the latest run is done.
func TestPlanPrepare_ResumeNoActiveRun(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		"no run": func(t *testing.T, dir string) {},
		"done run": func(t *testing.T, dir string) {
			planTestSeedRun(t, dir, "plan-main-20200101T000000Z", map[string]any{
				"planIntegrity": map[string]any{"skillInvoked": "x", "done": "y"},
			})
		},
	}
	for name, seed := range cases {
		t.Run(name, func(t *testing.T) {
			dir := planTestGitRepo(t, "main")
			seed(t, dir)
			_, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, Resume: true})
			var de *mcpserver.DomainError
			if !errors.As(err, &de) {
				t.Fatalf("err = %T %v, want *mcpserver.DomainError", err, err)
			}
			if de.Msg != "no active plan run on branch main" {
				t.Errorf("Msg = %q", de.Msg)
			}
			if de.Suggestion != "call plan_prepare without resume to start a new plan run" {
				t.Errorf("Suggestion = %q", de.Suggestion)
			}
		})
	}
}

// TestPlanPrepare_GuardrailsFileFormat verifies guardrails.md for configured
// guardrails (multi-line description, a line starting with #) and for none.
func TestPlanPrepare_GuardrailsFileFormat(t *testing.T) {
	t.Run("configured", func(t *testing.T) {
		dir := planTestGitRepo(t, "main")
		writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), ""+
			"[plan.guardrails.no-new-deps]\n"+
			"severity = \"error\"\n"+
			"description = \"Ask before adding a third-party dependency.\"\n"+
			"\n"+
			"[plan.guardrails.prefer-existing-helpers]\n"+
			"severity = \"warning\"\n"+
			"description = \"Reuse helpers in internal/fsx and internal/state\\nbefore adding new ones.\\n# not a heading\"\n")

		out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true})
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(out.GuardrailsFile)
		if err != nil {
			t.Fatal(err)
		}
		want := "# Active plan guardrails (2)\n\n" +
			"## no-new-deps (error)\n" +
			"> Ask before adding a third-party dependency.\n\n" +
			"## prefer-existing-helpers (warning)\n" +
			"> Reuse helpers in internal/fsx and internal/state\n" +
			"> before adding new ones.\n" +
			"> # not a heading\n"
		if string(got) != want {
			t.Errorf("guardrails.md =\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("empty", func(t *testing.T) {
		dir := planTestGitRepo(t, "main")
		out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true})
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(out.GuardrailsFile)
		if err != nil {
			t.Fatal(err)
		}
		if want := "# Active plan guardrails (0)\n\nNo plan guardrails configured.\n"; string(got) != want {
			t.Errorf("guardrails.md = %q, want %q", got, want)
		}
	})
	t.Run("newlines in id and severity", func(t *testing.T) {
		got := renderGuardrailsMarkdown([]map[string]any{{"id": "a\nb", "severity": "err\r\nor", "description": "d"}})
		if want := "# Active plan guardrails (1)\n\n## a b (err or)\n> d\n"; got != want {
			t.Errorf("render = %q, want %q", got, want)
		}
	})
}

// TestPlanPrepare_ErrorSites verifies the InfraError paths of run selection
// and the guardrails write.
func TestPlanPrepare_ErrorSites(t *testing.T) {
	infra := func(t *testing.T, err error, msgPrefix string) {
		t.Helper()
		var ie *mcpserver.InfraError
		if !errors.As(err, &ie) {
			t.Fatalf("err = %T %v, want *mcpserver.InfraError", err, err)
		}
		if !strings.HasPrefix(ie.Msg, msgPrefix) {
			t.Errorf("Msg = %q, want prefix %q", ie.Msg, msgPrefix)
		}
		if ie.Suggestion == "" {
			t.Error("Suggestion empty")
		}
		if ie.Cause == nil {
			t.Error("Cause nil")
		}
	}
	blockRuns := func(t *testing.T) string {
		dir := planTestGitRepo(t, "main")
		writeFile(t, planTestRunsDir(dir), "blocker")
		return dir
	}

	t.Run("ENOTDIR first call", func(t *testing.T) {
		dir := blockRuns(t)
		_, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true})
		infra(t, err, "plan state write failed: ")
	})
	t.Run("ENOTDIR resolveTemplate", func(t *testing.T) {
		dir := blockRuns(t)
		_, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
		infra(t, err, "plan state read failed: ")
	})
	t.Run("EISDIR guardrails.md", func(t *testing.T) {
		dir := planTestGitRepo(t, "main")
		const run = "plan-main-20200101T000000Z"
		planTestSeedRun(t, dir, run, planTestActiveData())
		writeFile(t, filepath.Join(planTestRunsDir(dir), run+".evidence", "guardrails.md", "x"), "x")
		_, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
		infra(t, err, "guardrails file write failed: ")
	})
	t.Run("corrupt JSON", func(t *testing.T) {
		dir := planTestGitRepo(t, "main")
		p := filepath.Join(planTestRunsDir(dir), "plan-main-20200101T000000Z.json")
		writeFile(t, p, "{not json")
		_, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
		infra(t, err, "plan state read failed: "+p)
	})
}

// TestPlanPrepare_OutsideGitNoRun verifies that outside git no run is
// tracked: runId and guardrailsFile are empty (rendered "(none)").
func TestPlanPrepare_OutsideGitNoRun(t *testing.T) {
	dir := t.TempDir()
	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if out.RunID != "" || out.GuardrailsFile != "" {
		t.Errorf("RunID = %q, GuardrailsFile = %q, want both empty", out.RunID, out.GuardrailsFile)
	}
	if _, err := os.Stat(planTestRunsDir(dir)); !os.IsNotExist(err) {
		t.Errorf("runs dir created outside git (stat err = %v)", err)
	}
}

// ---------------------------------------------------------------------------
// plan_mark structured-data marker tests (guardrailResults, criticalDecisions)
// ---------------------------------------------------------------------------

// TestPlanMark_GuardrailResults_AppendOnly verifies plan_mark({marker:
// "guardrailResults", data:{results:[...]}}) appends to st.Data
// ["guardrailResults"] across repeated calls without touching planIntegrity.
func TestPlanMark_GuardrailResults_AppendOnly(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	if _, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true}); err != nil {
		t.Fatalf("planPrepareCore (seed): %v", err)
	}

	out1, err := planMark(dir, dir, PlanMarkIn{
		Marker: "guardrailResults",
		Data: map[string]any{"results": []any{
			map[string]any{"id": "G1", "status": "pass", "detail": "ok"},
		}},
	})
	if err != nil {
		t.Fatalf("planMark(guardrailResults) #1: %v", err)
	}
	if !out1.OK {
		t.Error("planMark(guardrailResults) #1 .OK = false, want true")
	}

	out2, err := planMark(dir, dir, PlanMarkIn{
		Marker: "guardrailResults",
		Data: map[string]any{"results": []any{
			map[string]any{"id": "G2", "status": "fail", "detail": "missing file"},
		}},
	})
	if err != nil {
		t.Fatalf("planMark(guardrailResults) #2: %v", err)
	}
	if !out2.OK {
		t.Error("planMark(guardrailResults) #2 .OK = false, want true")
	}

	doc := readSoleStateDoc(t, dir)
	results, ok := doc["guardrailResults"].([]any)
	if !ok {
		t.Fatalf("guardrailResults missing or wrong type: %v", doc["guardrailResults"])
	}
	if len(results) != 2 {
		t.Fatalf("len(guardrailResults) = %d, want 2 (append-only across both calls)", len(results))
	}
	first, _ := results[0].(map[string]any)
	if first["id"] != "G1" {
		t.Errorf("guardrailResults[0].id = %v, want G1", first["id"])
	}
	second, _ := results[1].(map[string]any)
	if second["id"] != "G2" {
		t.Errorf("guardrailResults[1].id = %v, want G2", second["id"])
	}

	// The existing integrity marker flow must be entirely unaffected: no
	// planIntegrity key was ever created for a structured-data marker.
	integrity, _ := doc["planIntegrity"].(map[string]any)
	if _, ok := integrity["guardrailResults"]; ok {
		t.Errorf("planIntegrity unexpectedly gained a guardrailResults key: %v", integrity)
	}
}

// TestPlanMark_CriticalDecisions_AppendOnly mirrors
// TestPlanMark_GuardrailResults_AppendOnly for the "criticalDecisions"
// marker (data key "decisions" instead of "results").
func TestPlanMark_CriticalDecisions_AppendOnly(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	if _, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true}); err != nil {
		t.Fatalf("planPrepareCore (seed): %v", err)
	}

	if _, err := planMark(dir, dir, PlanMarkIn{
		Marker: "criticalDecisions",
		Data: map[string]any{"decisions": []any{
			map[string]any{"key": "template", "choice": "shipped-default", "reason": "no project override"},
		}},
	}); err != nil {
		t.Fatalf("planMark(criticalDecisions) #1: %v", err)
	}
	if _, err := planMark(dir, dir, PlanMarkIn{
		Marker: "criticalDecisions",
		Data: map[string]any{"decisions": []any{
			map[string]any{"key": "routing", "choice": "lightweight", "reason": "2 files"},
		}},
	}); err != nil {
		t.Fatalf("planMark(criticalDecisions) #2: %v", err)
	}

	doc := readSoleStateDoc(t, dir)
	decisions, ok := doc["criticalDecisions"].([]any)
	if !ok {
		t.Fatalf("criticalDecisions missing or wrong type: %v", doc["criticalDecisions"])
	}
	if len(decisions) != 2 {
		t.Fatalf("len(criticalDecisions) = %d, want 2 (append-only across both calls)", len(decisions))
	}
}

// TestPlanMark_ExistingIntegrityMarkers_IgnoreData verifies Data is accepted
// but ignored for the pre-existing timestamp markers — passing it must not
// change planIntegrity's stamped-timestamp behavior.
func TestPlanMark_ExistingIntegrityMarkers_IgnoreData(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	if _, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true}); err != nil {
		t.Fatalf("planPrepareCore (seed): %v", err)
	}

	out, err := planMark(dir, dir, PlanMarkIn{
		Marker: "critiqueRan",
		Data:   map[string]any{"results": []any{map[string]any{"id": "should-be-ignored"}}},
	})
	if err != nil {
		t.Fatalf("planMark(critiqueRan, with Data): %v", err)
	}
	if !out.OK {
		t.Error("planMark(critiqueRan, with Data).OK = false, want true")
	}

	doc := readSoleStateDoc(t, dir)
	integrity, ok := doc["planIntegrity"].(map[string]any)
	if !ok {
		t.Fatalf("planIntegrity missing or wrong type: %v", doc["planIntegrity"])
	}
	if s, ok := integrity["critiqueRan"].(string); !ok || s == "" {
		t.Errorf("planIntegrity.critiqueRan = %v, want a non-empty timestamp string", integrity["critiqueRan"])
	}
	if _, ok := doc["results"]; ok {
		t.Errorf("Data leaked into a top-level %q key: %v", "results", doc["results"])
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
// plan template resolution tests
// ---------------------------------------------------------------------------

// planTemplateResolveFixture is a project-override plan template exercising
// every buildSkeletonMarkdown body-selection branch: a plain section, an
// OpenSpec-conditional section, a section with an unrecognized condition,
// and the one step-5-owned section ("Verification Scorecard"). It also
// carries a Discovery Questions and a Verification Patterns block for the
// bullet-extraction assertions.
const planTemplateResolveFixture = `# Plan Template

## Required Sections

- Summary
- OpenSpec Sync <!-- conditional: source matches openspec/changes/ -->
- Weird Condition <!-- conditional: bogus-condition -->
- Verification Scorecard

## Discovery Questions

- What is the goal?
- What are the risks?

## Verification Patterns

- Run go test ./...
- Run go vet ./...
`

// TestPlanTemplateResolve_ProjectOverride verifies that a project-override
// template at .sdlc/plan-template.md becomes the active template (instead
// of the shipped default), that its discovery questions and verification
// patterns are extracted verbatim, and that the skeleton markdown emits one
// "## <name>" heading per declared section in declaration order.
func TestPlanTemplateResolve_ProjectOverride(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	templatePath := writeProjectPlanTemplate(t, dir, planTemplateResolveFixture)

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if out.Template == nil {
		t.Fatal("Template = nil, want populated (project override present)")
	}
	if out.Template.ActiveTemplatePath != templatePath {
		t.Errorf("ActiveTemplatePath = %q, want %q", out.Template.ActiveTemplatePath, templatePath)
	}

	wantQuestions := []string{"What is the goal?", "What are the risks?"}
	if !reflect.DeepEqual(out.Template.DiscoveryQuestions, wantQuestions) {
		t.Errorf("DiscoveryQuestions = %v, want %v", out.Template.DiscoveryQuestions, wantQuestions)
	}
	wantPatterns := []string{"Run go test ./...", "Run go vet ./..."}
	if !reflect.DeepEqual(out.Template.VerificationPatterns, wantPatterns) {
		t.Errorf("VerificationPatterns = %v, want %v", out.Template.VerificationPatterns, wantPatterns)
	}

	// Skeleton heading order must match the template's declared section order.
	md := out.Template.SkeletonMarkdown
	names := []string{"Summary", "OpenSpec Sync", "Weird Condition", "Verification Scorecard"}
	prevIdx := -1
	for _, name := range names {
		idx := strings.Index(md, "## "+name)
		if idx == -1 {
			t.Fatalf("skeleton markdown missing heading %q: %s", name, md)
		}
		if idx <= prevIdx {
			t.Errorf("heading %q out of order in skeleton markdown", name)
		}
		prevIdx = idx
	}
}

// TestPlanTemplateResolve_ConditionOpenspec verifies OpenSpec-conditional
// section bodies in both directions: with fromOpenspecDirect=true the
// section is left as "[TBD]" (still to be written); otherwise it is marked
// not applicable.
func TestPlanTemplateResolve_ConditionOpenspec(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	writeProjectPlanTemplate(t, dir, planTemplateResolveFixture)

	outActive, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true, FromOpenspecDirect: true})
	if err != nil {
		t.Fatalf("planPrepareCore (openspec active): %v", err)
	}
	if body := sectionBody(t, outActive.Template.SkeletonMarkdown, "OpenSpec Sync"); body != "[TBD]" {
		t.Errorf("OpenSpec Sync body (fromOpenspecDirect=true) = %q, want [TBD]", body)
	}

	outInactive, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
	if err != nil {
		t.Fatalf("planPrepareCore (openspec inactive): %v", err)
	}
	want := "Not applicable — no OpenSpec change"
	if body := sectionBody(t, outInactive.Template.SkeletonMarkdown, "OpenSpec Sync"); body != want {
		t.Errorf("OpenSpec Sync body (no openspec flags) = %q, want %q", body, want)
	}
}

// TestPlanTemplateResolve_ConditionUnknown verifies a section whose
// condition does not start with the OpenSpec prefix gets the
// not-recognized message, quoting the condition string verbatim.
func TestPlanTemplateResolve_ConditionUnknown(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	writeProjectPlanTemplate(t, dir, planTemplateResolveFixture)

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	want := fmt.Sprintf("Not applicable — condition %q not recognized", "bogus-condition")
	if body := sectionBody(t, out.Template.SkeletonMarkdown, "Weird Condition"); body != want {
		t.Errorf("Weird Condition body = %q, want %q", body, want)
	}
}

// TestPlanTemplateResolve_Lightweight verifies that Lightweight=true alone
// (independent of routing) marks step-5-owned sections ("Verification
// Scorecard") not applicable.
func TestPlanTemplateResolve_Lightweight(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	writeProjectPlanTemplate(t, dir, planTemplateResolveFixture)

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true, Lightweight: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	want := "Not applicable — lightweight plan"
	if body := sectionBody(t, out.Template.SkeletonMarkdown, "Verification Scorecard"); body != want {
		t.Errorf("Verification Scorecard body = %q, want %q", body, want)
	}
}

// TestPlanTemplateResolve_Routing1File verifies computeComplexityRouting's
// single-file boundary: a lone file routes to "skip" unless lightweight is
// explicitly requested, in which case it routes to "lightweight".
func TestPlanTemplateResolve_Routing1File(t *testing.T) {
	cases := []struct {
		name        string
		lightweight bool
		wantMode    string
	}{
		{"not lightweight", false, "skip"},
		{"lightweight", true, "lightweight"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			routing := computeComplexityRouting(1, tc.lightweight)
			if routing.FileCount != 1 {
				t.Errorf("FileCount = %d, want 1", routing.FileCount)
			}
			if routing.PipelineMode != tc.wantMode {
				t.Errorf("PipelineMode = %q, want %q", routing.PipelineMode, tc.wantMode)
			}
		})
	}
}

// TestPlanTemplateResolve_Routing4Files verifies computeComplexityRouting's
// upper bands: 2-3 files route to "lightweight", 4+ files route to "full".
func TestPlanTemplateResolve_Routing4Files(t *testing.T) {
	cases := []struct {
		name      string
		fileCount int
		wantMode  string
	}{
		{"3 files (lightweight band)", 3, "lightweight"},
		{"4 files (full)", 4, "full"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			routing := computeComplexityRouting(tc.fileCount, false)
			if routing.PipelineMode != tc.wantMode {
				t.Errorf("PipelineMode = %q, want %q", routing.PipelineMode, tc.wantMode)
			}
		})
	}
}

// TestPlanTemplateResolve_DefaultFallback verifies that with no project
// override present, the active template resolves to the shipped default
// found under ~/.claude/plugins. resolveSkillTemplate walks the real home
// directory and is cached process-wide (sync.Once), so it cannot be faked
// hermetically here; when the shipped default is not discoverable in the
// current environment (e.g. a CI runner with no plugins installed), the
// test skips rather than asserting a false result.
func TestPlanTemplateResolve_DefaultFallback(t *testing.T) {
	want := resolveSkillTemplate("plan-template-default.md")
	if want == nil {
		t.Skip("shipped default plan template not discoverable under ~/.claude/plugins in this environment")
	}

	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if out.Template == nil {
		t.Fatal("Template = nil, want populated (shipped default resolvable)")
	}
	if out.Template.ActiveTemplatePath != *want {
		t.Errorf("ActiveTemplatePath = %q, want shipped default %q", out.Template.ActiveTemplatePath, *want)
	}
}

// TestPlanTemplateResolve_UnreadableFallback verifies that a project
// template which fails to parse (no "## Required Sections" heading, so
// parseTemplateRequiredSectionsFull yields zero sections) falls back to the
// shipped default exactly like a read error does, and records the
// fallback warning. Skip-gated for the same reason as DefaultFallback: the
// shipped default must be discoverable under ~/.claude/plugins.
func TestPlanTemplateResolve_UnreadableFallback(t *testing.T) {
	want := resolveSkillTemplate("plan-template-default.md")
	if want == nil {
		t.Skip("shipped default plan template not discoverable under ~/.claude/plugins in this environment")
	}

	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	writeProjectPlanTemplate(t, dir, "# Plan Template\n\nNo required sections heading here.\n")

	out, err := planPrepareCore(dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if out.Template == nil {
		t.Fatal("Template = nil, want populated (shipped default resolvable)")
	}
	if out.Template.ActiveTemplatePath != *want {
		t.Errorf("ActiveTemplatePath = %q, want shipped default %q", out.Template.ActiveTemplatePath, *want)
	}
	wantWarning := "Project template unreadable — fell back to shipped default"
	found := false
	for _, w := range out.Template.Warnings {
		if w == wantWarning {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Warnings = %v, want to contain %q", out.Template.Warnings, wantWarning)
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

// writeProjectPlanTemplate writes content as the project-override plan
// template at the fixed path (<dir>/paths.DataDir/plan-template.md) that
// planPrepareCore and buildTemplateResolution both read, returning the
// full path.
func writeProjectPlanTemplate(t *testing.T, dir, content string) string {
	t.Helper()
	sdlcDir := filepath.Join(dir, paths.DataDir)
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	templatePath := filepath.Join(sdlcDir, "plan-template.md")
	if err := os.WriteFile(templatePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return templatePath
}

// sectionBody extracts the body text of a "## <name>" section from
// buildSkeletonMarkdown's output: the text between that section's heading
// and the next "## " heading, or end of string for the last section.
func sectionBody(t *testing.T, markdown, name string) string {
	t.Helper()
	marker := "## " + name + "\n\n"
	idx := strings.Index(markdown, marker)
	if idx == -1 {
		t.Fatalf("section %q not found in skeleton markdown: %s", name, markdown)
	}
	rest := markdown[idx+len(marker):]
	if end := strings.Index(rest, "\n\n##"); end != -1 {
		return rest[:end]
	}
	return strings.TrimRight(rest, "\n")
}

// listStateFiles lists the basenames of files under <root>/.sdlc/execution/.
func listStateFiles(t *testing.T, root string) []string {
	t.Helper()
	dir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
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
