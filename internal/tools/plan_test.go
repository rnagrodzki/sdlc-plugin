package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// plan_prepare tests
// ---------------------------------------------------------------------------

// runPlanPrepare calls planPrepareCore with the fsseam's mkdirTempFunc
// redirected into a t.TempDir() (redirectTempManifests). A non-resume call
// builds an explore pack in a fresh sdlc-explore-* dir; without the redirect
// every test run leaves one such dir per call in the OS temp dir. Several
// calls in one test each get their own redirect; the cleanups restore the
// seam in reverse order.
func runPlanPrepare(t *testing.T, mainRoot, contentRoot string, in PlanPrepareIn) (PlanPrepareOut, error) {
	t.Helper()
	redirectTempManifests(t)
	return planPrepareCore(mainRoot, contentRoot, in)
}

// TestPlanPrepare_KeySetAndDefaults verifies the top-level PlanPrepareOut
// shape (fixture parity with plan.js's main() output object) on a repo with
// no OpenSpec, no guardrail config, and no plan template.
func TestPlanPrepare_KeySetAndDefaults(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true})
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
	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, UserPrompt: prompt})
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true})
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true})
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true})
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true})
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true})
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	want := []string{"A", "B"}
	if !reflect.DeepEqual(out.Style.Instructions, want) {
		t.Errorf("Style.Instructions = %v, want %v (non-strings and blank entries dropped, survivors trimmed)", out.Style.Instructions, want)
	}
}

// TestPlanStyle_MalformedConfigSurfacesError verifies a local.toml that does
// not parse is not silently treated as "no planStyle": plan_prepare lists the
// read error in Errors, and a checkpoint's Next carries a warning.
func TestPlanStyle_MalformedConfigSurfacesError(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	if _, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true}); err != nil {
		t.Fatalf("planPrepareCore (seed): %v", err)
	}

	writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), "[planStyle\ninstructions = [\n")

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	found := false
	for _, e := range out.Errors {
		if strings.HasPrefix(e, "Failed to read planStyle config: ") {
			found = true
		}
	}
	if !found {
		t.Errorf("Errors = %v, want a \"Failed to read planStyle config: \" entry", out.Errors)
	}

	mk, err := planMark(dir, dir, PlanMarkIn{Marker: "checkpoint", Data: map[string]any{"step": "3"}})
	if err != nil {
		t.Fatalf("planMark(checkpoint): %v", err)
	}
	if !strings.Contains(mk.Next, "Warning: Failed to read planStyle config: ") {
		t.Errorf("Next = %q, want a planStyle read warning", mk.Next)
	}
}

// TestPlanPrepare_ResumeOutsideGit_InfraError verifies a resume whose branch
// lookup fails returns an InfraError carrying the git error, not the
// "no active plan run" DomainError that would tell the caller to start over.
func TestPlanPrepare_ResumeOutsideGit_InfraError(t *testing.T) {
	dir := t.TempDir()
	_, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, Resume: true})
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %T %v, want *mcpserver.InfraError", err, err)
	}
	if ie.Cause == nil {
		t.Error("InfraError.Cause is nil, want the git error")
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true})
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

			out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true})
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true})
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
	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, FromOpenspec: "add-widget"})
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
	out2, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, FromOpenspec: "add-widget"})
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, FromOpenspec: "does-not-exist"})
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: false})
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

// TestPlanMark_BranchError verifies the branch error only fires when git
// cannot run in the active worktree, and its suggestion says so. A detached
// HEAD is not this error: it reads as branch "HEAD".
func TestPlanMark_BranchError(t *testing.T) {
	t.Run("not a git repository", func(t *testing.T) {
		dir := t.TempDir()
		_, err := planMark(dir, dir, PlanMarkIn{Marker: "guardrailsEvaluated"})
		var ie *mcpserver.InfraError
		if !errors.As(err, &ie) {
			t.Fatalf("err = %T %v, want *mcpserver.InfraError", err, err)
		}
		if ie.Msg != "could not determine current branch" {
			t.Errorf("Msg = %q", ie.Msg)
		}
		want := "Run plan_mark from inside a git repository or worktree (git branch --show-current must succeed there), then retry plan_mark."
		if ie.Suggestion != want {
			t.Errorf("Suggestion = %q, want %q", ie.Suggestion, want)
		}
	})
	t.Run("detached HEAD", func(t *testing.T) {
		dir := t.TempDir()
		initGitFixture(t, dir)
		gitCommit(t, dir, "initial")
		runGit(t, dir, "checkout", "--detach")
		_, err := planMark(dir, dir, PlanMarkIn{Marker: "guardrailsEvaluated"})
		var de *mcpserver.DomainError
		if !errors.As(err, &de) {
			t.Fatalf("err = %T %v, want *mcpserver.DomainError", err, err)
		}
		if !strings.Contains(de.Msg, `branch "HEAD"`) {
			t.Errorf("Msg = %q, want it to name branch \"HEAD\"", de.Msg)
		}
	})
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
	if _, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true}); err != nil {
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

// ---------------------------------------------------------------------------
// plan_mark plan-timing tests
// ---------------------------------------------------------------------------

// seedPlanTimingRun seeds a plan state file via planPrepareCore and returns
// the run's skillInvoked timestamp, parsed. Every plan-timing test starts
// from this same fixture.
func seedPlanTimingRun(t *testing.T, dir string) time.Time {
	t.Helper()
	if _, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true}); err != nil {
		t.Fatalf("planPrepareCore (seed): %v", err)
	}
	doc := readSoleStateDoc(t, dir)
	integrity, ok := doc["planIntegrity"].(map[string]any)
	if !ok {
		t.Fatalf("planIntegrity missing or wrong type: %v", doc["planIntegrity"])
	}
	skillInvoked, _ := integrity["skillInvoked"].(string)
	startedAt, err := time.Parse(time.RFC3339, skillInvoked)
	if err != nil {
		t.Fatalf("parse seeded skillInvoked %q: %v", skillInvoked, err)
	}
	return startedAt
}

// TestPlanMark_PlanTiming_ComputedFromMtimeAndSkillInvoked verifies that a
// plan_mark call with planFilePath set stamps data.planTiming from
// planIntegrity.skillInvoked and the plan file's own mtime — not from the
// time of the plan_mark call — and normalizes a relative planFilePath to an
// absolute, cleaned path.
func TestPlanMark_PlanTiming_ComputedFromMtimeAndSkillInvoked(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	startedAt := seedPlanTimingRun(t, dir)

	relPath := filepath.Join("docs", "plan.md")
	absPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte("# plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Set an mtime clearly later than startedAt but well before "now", so a
	// regression that used time.Now() instead of the file's mtime would be
	// caught by the exact-value assertions below.
	wantModTime := startedAt.Add(5 * time.Minute)
	if err := os.Chtimes(absPath, wantModTime, wantModTime); err != nil {
		t.Fatal(err)
	}

	out, err := planMark(dir, dir, PlanMarkIn{Marker: "plan-file", Path: relPath})
	if err != nil {
		t.Fatalf("planMark(plan-file): %v", err)
	}
	if !out.OK {
		t.Error("planMark(plan-file).OK = false, want true")
	}

	doc := readSoleStateDoc(t, dir)
	if doc["planFilePath"] != absPath {
		t.Errorf("planFilePath = %v, want absolute+cleaned %q", doc["planFilePath"], absPath)
	}
	timing, ok := doc["planTiming"].(map[string]any)
	if !ok {
		t.Fatalf("planTiming missing or wrong type: %v", doc["planTiming"])
	}
	wantStartedAt := startedAt.UTC().Format(time.RFC3339)
	if timing["startedAt"] != wantStartedAt {
		t.Errorf("planTiming.startedAt = %v, want %q", timing["startedAt"], wantStartedAt)
	}
	wantLastModified := wantModTime.UTC().Format(time.RFC3339)
	if timing["lastModifiedAt"] != wantLastModified {
		t.Errorf("planTiming.lastModifiedAt = %v, want %q", timing["lastModifiedAt"], wantLastModified)
	}
	wantDurationMs := float64(5 * time.Minute / time.Millisecond)
	if timing["durationMs"] != wantDurationMs {
		t.Errorf("planTiming.durationMs = %v, want %v", timing["durationMs"], wantDurationMs)
	}
}

// TestPlanMark_PlanTiming_NoPlanFilePath_LeavesUnchanged verifies a
// plan_mark call before any "plan-file" marker leaves planTiming absent and
// still succeeds.
func TestPlanMark_PlanTiming_NoPlanFilePath_LeavesUnchanged(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	seedPlanTimingRun(t, dir)

	out, err := planMark(dir, dir, PlanMarkIn{Marker: "guardrailsEvaluated"})
	if err != nil {
		t.Fatalf("planMark(guardrailsEvaluated): %v", err)
	}
	if !out.OK {
		t.Error("planMark(guardrailsEvaluated).OK = false, want true")
	}

	doc := readSoleStateDoc(t, dir)
	if _, present := doc["planTiming"]; present {
		t.Errorf("planTiming = %v, want absent (no planFilePath set)", doc["planTiming"])
	}
}

// TestPlanMark_PlanTiming_UnstatableFile_LeavesPreviousValue verifies that
// once planTiming has been computed, a later call whose plan file can no
// longer be stat'ed leaves planTiming (and planFilePath) at their previous
// values, and the marker call still succeeds.
func TestPlanMark_PlanTiming_UnstatableFile_LeavesPreviousValue(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	startedAt := seedPlanTimingRun(t, dir)

	relPath := filepath.Join("docs", "plan.md")
	absPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte("# plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantModTime := startedAt.Add(2 * time.Minute)
	if err := os.Chtimes(absPath, wantModTime, wantModTime); err != nil {
		t.Fatal(err)
	}
	if _, err := planMark(dir, dir, PlanMarkIn{Marker: "plan-file", Path: relPath}); err != nil {
		t.Fatalf("planMark(plan-file): %v", err)
	}
	before := readSoleStateDoc(t, dir)
	timingBefore, ok := before["planTiming"].(map[string]any)
	if !ok {
		t.Fatalf("planTiming missing or wrong type after plan-file: %v", before["planTiming"])
	}

	// The plan file disappears before the next marker call.
	if err := os.Remove(absPath); err != nil {
		t.Fatal(err)
	}

	out, err := planMark(dir, dir, PlanMarkIn{Marker: "critiqueRan"})
	if err != nil {
		t.Fatalf("planMark(critiqueRan): %v", err)
	}
	if !out.OK {
		t.Error("planMark(critiqueRan).OK = false, want true")
	}

	after := readSoleStateDoc(t, dir)
	if after["planFilePath"] != before["planFilePath"] {
		t.Errorf("planFilePath = %v, want unchanged %v", after["planFilePath"], before["planFilePath"])
	}
	timingAfter, ok := after["planTiming"].(map[string]any)
	if !ok {
		t.Fatalf("planTiming missing or wrong type after critiqueRan: %v", after["planTiming"])
	}
	if !reflect.DeepEqual(timingAfter, timingBefore) {
		t.Errorf("planTiming = %v, want unchanged %v", timingAfter, timingBefore)
	}
}

// TestPlanMark_PlanTiming_DoneAppendsHistoryRecord verifies the "done"
// marker appends one history.RunRecord to .sdlc-v2/history/runs.jsonl with
// skill "plan", outcome "done", the resolved absolute plan_file, and timing
// fields that match the plan file's last edit — not the time of the "done"
// call itself, even when "done" is marked well after the last edit.
func TestPlanMark_PlanTiming_DoneAppendsHistoryRecord(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	startedAt := seedPlanTimingRun(t, dir)

	relPath := filepath.Join("docs", "plan.md")
	absPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte("# plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantModTime := startedAt.Add(3 * time.Minute)
	if err := os.Chtimes(absPath, wantModTime, wantModTime); err != nil {
		t.Fatal(err)
	}
	if _, err := planMark(dir, dir, PlanMarkIn{Marker: "plan-file", Path: relPath}); err != nil {
		t.Fatalf("planMark(plan-file): %v", err)
	}

	// "done" is marked without touching the plan file again — simulates a
	// done call made well after the last edit.
	out, err := planMark(dir, dir, PlanMarkIn{Marker: "done"})
	if err != nil {
		t.Fatalf("planMark(done): %v", err)
	}
	if !out.OK {
		t.Error("planMark(done).OK = false, want true")
	}
	if len(out.Warnings) != 0 {
		t.Errorf("planMark(done).Warnings = %q, want empty", out.Warnings)
	}

	runs, err := history.NewFileWriter(historyDir(dir)).ReadRecentRuns(10)
	if err != nil {
		t.Fatalf("ReadRecentRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("history runs = %v, want exactly 1", runs)
	}
	rec := runs[0]
	if rec.Skill != "plan" || rec.Outcome != "done" || rec.Branch != "main" {
		t.Errorf("record skill/outcome/branch = %q/%q/%q, want plan/done/main", rec.Skill, rec.Outcome, rec.Branch)
	}
	if rec.PlanFile != absPath {
		t.Errorf("record.PlanFile = %q, want absolute+cleaned %q", rec.PlanFile, absPath)
	}
	wantStartedAt := startedAt.UTC().Format(time.RFC3339)
	if rec.StartedAt != wantStartedAt {
		t.Errorf("record.StartedAt = %q, want %q", rec.StartedAt, wantStartedAt)
	}
	wantLastModified := wantModTime.UTC().Format(time.RFC3339)
	if rec.LastModifiedAt != wantLastModified {
		t.Errorf("record.LastModifiedAt = %q, want %q (the plan file's mtime, not the done-call time)", rec.LastModifiedAt, wantLastModified)
	}
	wantDurationMs := int64(3 * time.Minute / time.Millisecond)
	if rec.DurationMs != wantDurationMs {
		t.Errorf("record.DurationMs = %d, want %d", rec.DurationMs, wantDurationMs)
	}
}

// TestPlanMark_PlanTiming_DoneHistoryWriteFailure_ReturnsWarning verifies
// that when the history append fails, "done" still returns ok:true and
// names the error in Warnings, rather than failing the call.
func TestPlanMark_PlanTiming_DoneHistoryWriteFailure_ReturnsWarning(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	startedAt := seedPlanTimingRun(t, dir)

	relPath := filepath.Join("docs", "plan.md")
	absPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte("# plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantModTime := startedAt.Add(time.Minute)
	if err := os.Chtimes(absPath, wantModTime, wantModTime); err != nil {
		t.Fatal(err)
	}
	if _, err := planMark(dir, dir, PlanMarkIn{Marker: "plan-file", Path: relPath}); err != nil {
		t.Fatalf("planMark(plan-file): %v", err)
	}

	// Force AppendRun to fail: pre-create a directory where runs.jsonl
	// should be a regular file. Not chmod — running as root ignores
	// permission bits, so a chmod-based failure would not reproduce in CI.
	hDir := historyDir(dir)
	if err := os.MkdirAll(filepath.Join(hDir, "runs.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := planMark(dir, dir, PlanMarkIn{Marker: "done"})
	if err != nil {
		t.Fatalf("planMark(done): %v", err)
	}
	if !out.OK {
		t.Error("planMark(done).OK = false, want true even when the history write fails")
	}
	if len(out.Warnings) == 0 {
		t.Error("planMark(done).Warnings = empty, want an error naming the history write failure")
	}
}

// TestPlanMark_PlanTiming_SecondDoneAppendsNewerRecord verifies a second
// "done" call for the same run (e.g. a revision after a rejected
// ExitPlanMode) appends a newer record rather than replacing the first, so
// readers take the latest one.
func TestPlanMark_PlanTiming_SecondDoneAppendsNewerRecord(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	startedAt := seedPlanTimingRun(t, dir)

	relPath := filepath.Join("docs", "plan.md")
	absPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte("# plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	firstModTime := startedAt.Add(time.Minute)
	if err := os.Chtimes(absPath, firstModTime, firstModTime); err != nil {
		t.Fatal(err)
	}
	if _, err := planMark(dir, dir, PlanMarkIn{Marker: "plan-file", Path: relPath}); err != nil {
		t.Fatalf("planMark(plan-file): %v", err)
	}
	if _, err := planMark(dir, dir, PlanMarkIn{Marker: "done"}); err != nil {
		t.Fatalf("planMark(done) #1: %v", err)
	}

	// The plan is revised after a rejected ExitPlanMode: the file is edited
	// again, later than the first "done".
	if err := os.WriteFile(absPath, []byte("# plan\n\nrevised\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	secondModTime := startedAt.Add(10 * time.Minute)
	if err := os.Chtimes(absPath, secondModTime, secondModTime); err != nil {
		t.Fatal(err)
	}
	if _, err := planMark(dir, dir, PlanMarkIn{Marker: "plan-file", Path: relPath}); err != nil {
		t.Fatalf("planMark(plan-file) #2: %v", err)
	}
	if _, err := planMark(dir, dir, PlanMarkIn{Marker: "done"}); err != nil {
		t.Fatalf("planMark(done) #2: %v", err)
	}

	runs, err := history.NewFileWriter(historyDir(dir)).ReadRecentRuns(10)
	if err != nil {
		t.Fatalf("ReadRecentRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("history runs = %v, want exactly 2", runs)
	}
	latest := runs[len(runs)-1]
	wantLastModified := secondModTime.UTC().Format(time.RFC3339)
	if latest.LastModifiedAt != wantLastModified {
		t.Errorf("latest record.LastModifiedAt = %q, want %q", latest.LastModifiedAt, wantLastModified)
	}
	wantDurationMs := int64(10 * time.Minute / time.Millisecond)
	if latest.DurationMs != wantDurationMs {
		t.Errorf("latest record.DurationMs = %d, want %d", latest.DurationMs, wantDurationMs)
	}
}

// TestPlanMark_PlanTiming_CheckpointBranchAlsoRefreshesTiming verifies the
// "checkpoint" marker branch also refreshes planTiming, not just the
// generic timestamp-marker branch.
func TestPlanMark_PlanTiming_CheckpointBranchAlsoRefreshesTiming(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	startedAt := seedPlanTimingRun(t, dir)

	relPath := filepath.Join("docs", "plan.md")
	absPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte("# plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantModTime := startedAt.Add(4 * time.Minute)
	if err := os.Chtimes(absPath, wantModTime, wantModTime); err != nil {
		t.Fatal(err)
	}
	if _, err := planMark(dir, dir, PlanMarkIn{Marker: "plan-file", Path: relPath}); err != nil {
		t.Fatalf("planMark(plan-file): %v", err)
	}

	// Edit the file again, then mark a checkpoint — the checkpoint branch
	// must pick up the new mtime too.
	laterModTime := startedAt.Add(6 * time.Minute)
	if err := os.Chtimes(absPath, laterModTime, laterModTime); err != nil {
		t.Fatal(err)
	}
	out, err := planMark(dir, dir, PlanMarkIn{
		Marker: "checkpoint",
		Data:   map[string]any{"step": "3"},
	})
	if err != nil {
		t.Fatalf("planMark(checkpoint): %v", err)
	}
	if !out.OK {
		t.Error("planMark(checkpoint).OK = false, want true")
	}

	doc := readSoleStateDoc(t, dir)
	timing, ok := doc["planTiming"].(map[string]any)
	if !ok {
		t.Fatalf("planTiming missing or wrong type after checkpoint: %v", doc["planTiming"])
	}
	wantLastModified := laterModTime.UTC().Format(time.RFC3339)
	if timing["lastModifiedAt"] != wantLastModified {
		t.Errorf("planTiming.lastModifiedAt = %v, want %q", timing["lastModifiedAt"], wantLastModified)
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
	first, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, UserPrompt: prompt})
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

	second, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true})
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true, UserPrompt: "p", FileCount: 12})
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

	_, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, Resume: true})
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("resume err = %T %v, want *mcpserver.DomainError", err, err)
	}

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
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

	if _, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, UserPrompt: prompt}); err != nil {
		t.Fatal(err)
	}
	prev, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true, UserPrompt: prompt, FileCount: 12})
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(planTestRunsDir(dir), prev.RunID+".json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}

	in := PlanPrepareIn{SkipConfigCheck: true, Resume: true, UserPrompt: "different", FileCount: 1, Lightweight: true}
	out, err := runPlanPrepare(t, dir, dir, in)
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
	out, err := runPlanPrepare(t, dir, dir, in)
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

	a, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, Resume: true, FileCount: 5})
	if err != nil {
		t.Fatal(err)
	}
	b, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, Resume: true, ResolveTemplate: true, FileCount: 5})
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
			_, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, Resume: true})
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

// TestPlanPrepareDoneRunNotResumed verifies plan_prepare({resolveTemplate:
// true}) does not resume a "done" run: state.ActivePlanRun excludes any run
// whose planIntegrity.done is set, so selectPlanRun's "no active run" branch
// fires and a fresh run replaces it — mirroring
// TestPlanPrepare_ResumeNoActiveRun's done-run case, but for the plain
// resolveTemplate:true path (no resume, so it must not error).
func TestPlanPrepareDoneRunNotResumed(t *testing.T) {
	dir := planTestGitRepo(t, "main")
	const doneRun = "plan-main-20200101T000000Z"
	planTestSeedRun(t, dir, doneRun, map[string]any{
		"planIntegrity":  map[string]any{"skillInvoked": "2020-01-01T00:00:00Z", "done": "2020-01-01T01:00:00Z"},
		"creationIntent": map[string]any{"userPrompt": "first run"},
	})

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true, UserPrompt: "second run"})
	if err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	if out.RunID == "" || out.RunID == doneRun {
		t.Fatalf("RunID = %q, want a new run ID distinct from the done run %q", out.RunID, doneRun)
	}

	// readSoleStateDoc also asserts the done run's own state file was pruned
	// (exactly one state file survives the new run's write).
	doc := readSoleStateDoc(t, dir)
	integrity, _ := doc["planIntegrity"].(map[string]any)
	if _, hasDone := integrity["done"]; hasDone {
		t.Error("surviving run's planIntegrity already has \"done\" — the done run was resumed instead of starting a new one")
	}
	intent, _ := doc["creationIntent"].(map[string]any)
	if intent["userPrompt"] != "second run" {
		t.Errorf("creationIntent.userPrompt = %v, want %q (a resumed run would keep the done run's prompt)", intent["userPrompt"], "second run")
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

		out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true})
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
	t.Run("array of tables form", func(t *testing.T) {
		// [[plan.guardrails]] passes validate, so plan_prepare must read it
		// too. Each entry carries its own id; file order is kept.
		dir := planTestGitRepo(t, "main")
		writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), ""+
			"[[plan.guardrails]]\n"+
			"id = \"prefer-existing-helpers\"\n"+
			"severity = \"warning\"\n"+
			"description = \"Reuse existing helpers.\"\n"+
			"\n"+
			"[[plan.guardrails]]\n"+
			"id = \"no-new-deps\"\n"+
			"severity = \"error\"\n"+
			"description = \"Ask before adding a third-party dependency.\"\n")

		out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true})
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(out.GuardrailsFile)
		if err != nil {
			t.Fatal(err)
		}
		want := "# Active plan guardrails (2)\n\n" +
			"## prefer-existing-helpers (warning)\n" +
			"> Reuse existing helpers.\n\n" +
			"## no-new-deps (error)\n" +
			"> Ask before adding a third-party dependency.\n"
		if string(got) != want {
			t.Errorf("guardrails.md =\n%s\nwant\n%s", got, want)
		}
	})
	t.Run("empty", func(t *testing.T) {
		dir := planTestGitRepo(t, "main")
		out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true})
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
		_, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true})
		infra(t, err, "plan state write failed: ")
	})
	t.Run("ENOTDIR resolveTemplate", func(t *testing.T) {
		dir := blockRuns(t)
		_, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
		infra(t, err, "plan state read failed: ")
	})
	t.Run("EISDIR guardrails.md", func(t *testing.T) {
		dir := planTestGitRepo(t, "main")
		const run = "plan-main-20200101T000000Z"
		planTestSeedRun(t, dir, run, planTestActiveData())
		writeFile(t, filepath.Join(planTestRunsDir(dir), run+".evidence", "guardrails.md", "x"), "x")
		_, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
		infra(t, err, "guardrails file write failed: ")
	})
	t.Run("corrupt JSON", func(t *testing.T) {
		dir := planTestGitRepo(t, "main")
		p := filepath.Join(planTestRunsDir(dir), "plan-main-20200101T000000Z.json")
		writeFile(t, p, "{not json")
		_, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
		infra(t, err, "plan state read failed: "+p)
	})
}

// TestPlanPrepare_OutsideGitNoRun verifies that outside git no run is
// tracked: runId and guardrailsFile are empty (rendered "(none)").
func TestPlanPrepare_OutsideGitNoRun(t *testing.T) {
	dir := t.TempDir()
	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
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

	if _, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true}); err != nil {
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

	if _, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true}); err != nil {
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

// TestPlanMarkCriticalDecisionsRejected verifies plan_mark normalizes every
// "criticalDecisions" entry: a caller-supplied "rejected" list of
// {option,why} is stored unchanged, a missing "rejected" defaults to an
// empty list, and "at" is always the call's own time (RFC 3339 UTC) — even
// when the caller supplied its own "at" value.
func TestPlanMarkCriticalDecisionsRejected(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	if _, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true}); err != nil {
		t.Fatalf("planPrepareCore (seed): %v", err)
	}

	before := time.Now().UTC()
	out, err := planMark(dir, dir, PlanMarkIn{
		Marker: "criticalDecisions",
		Data: map[string]any{"decisions": []any{
			map[string]any{
				"key":    "template",
				"choice": "shipped-default",
				"reason": "no project override",
				"rejected": []any{
					map[string]any{"option": "custom-template", "why": "no project config found"},
				},
				"at": "2000-01-01T00:00:00Z", // caller-supplied — must be overwritten
			},
			map[string]any{
				"key":    "routing",
				"choice": "lightweight",
				"reason": "2 files",
				// no "rejected" — must default to []
			},
		}},
	})
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("planMark(criticalDecisions): %v", err)
	}
	if !out.OK {
		t.Error("planMark(criticalDecisions).OK = false, want true")
	}

	doc := readSoleStateDoc(t, dir)
	decisions, ok := doc["criticalDecisions"].([]any)
	if !ok || len(decisions) != 2 {
		t.Fatalf("criticalDecisions = %v, want a 2-entry array", doc["criticalDecisions"])
	}

	first, _ := decisions[0].(map[string]any)
	rejected, ok := first["rejected"].([]any)
	if !ok || len(rejected) != 1 {
		t.Fatalf("decisions[0].rejected = %v, want the caller-supplied 1-entry list unchanged", first["rejected"])
	}
	rejectedEntry, _ := rejected[0].(map[string]any)
	if rejectedEntry["option"] != "custom-template" || rejectedEntry["why"] != "no project config found" {
		t.Errorf("decisions[0].rejected[0] = %v, want {option:custom-template, why:no project config found}", rejectedEntry)
	}
	if first["key"] != "template" || first["choice"] != "shipped-default" || first["reason"] != "no project override" {
		t.Errorf("decisions[0] lost its original fields: %v", first)
	}

	second, _ := decisions[1].(map[string]any)
	secondRejected, ok := second["rejected"].([]any)
	if !ok || len(secondRejected) != 0 {
		t.Errorf("decisions[1].rejected = %v, want an empty list (defaulted)", second["rejected"])
	}

	for i, entry := range []map[string]any{first, second} {
		atStr, ok := entry["at"].(string)
		if !ok {
			t.Fatalf("decisions[%d].at missing or wrong type: %v", i, entry["at"])
		}
		at, perr := time.Parse(time.RFC3339, atStr)
		if perr != nil {
			t.Fatalf("decisions[%d].at = %q is not RFC3339: %v", i, atStr, perr)
		}
		at = at.UTC()
		if at.Before(before.Add(-time.Second)) || at.After(after.Add(time.Second)) {
			t.Errorf("decisions[%d].at = %s, want between %s and %s (call time, not the caller-supplied value)", i, at, before, after)
		}
	}
}

// TestPlanMark_PlanTiming_StructuredDataBranchAlsoRefreshesTiming verifies
// the structured-data marker branch ("guardrailResults"/"criticalDecisions")
// also refreshes planTiming, not just the generic timestamp-marker branch
// and the checkpoint branch.
func TestPlanMark_PlanTiming_StructuredDataBranchAlsoRefreshesTiming(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	startedAt := seedPlanTimingRun(t, dir)

	relPath := filepath.Join("docs", "plan.md")
	absPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte("# plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantModTime := startedAt.Add(7 * time.Minute)
	if err := os.Chtimes(absPath, wantModTime, wantModTime); err != nil {
		t.Fatal(err)
	}
	if _, err := planMark(dir, dir, PlanMarkIn{Marker: "plan-file", Path: relPath}); err != nil {
		t.Fatalf("planMark(plan-file): %v", err)
	}

	out, err := planMark(dir, dir, PlanMarkIn{
		Marker: "guardrailResults",
		Data: map[string]any{"results": []any{
			map[string]any{"id": "G1", "status": "pass", "detail": "ok"},
		}},
	})
	if err != nil {
		t.Fatalf("planMark(guardrailResults): %v", err)
	}
	if !out.OK {
		t.Error("planMark(guardrailResults).OK = false, want true")
	}

	doc := readSoleStateDoc(t, dir)
	timing, ok := doc["planTiming"].(map[string]any)
	if !ok {
		t.Fatalf("planTiming missing or wrong type after guardrailResults: %v", doc["planTiming"])
	}
	wantLastModified := wantModTime.UTC().Format(time.RFC3339)
	if timing["lastModifiedAt"] != wantLastModified {
		t.Errorf("planTiming.lastModifiedAt = %v, want %q", timing["lastModifiedAt"], wantLastModified)
	}
}

// TestPlanMark_ExistingIntegrityMarkers_IgnoreData verifies Data is accepted
// but ignored for the pre-existing timestamp markers — passing it must not
// change planIntegrity's stamped-timestamp behavior.
func TestPlanMark_ExistingIntegrityMarkers_IgnoreData(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	if _, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true}); err != nil {
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

// readSoleStateFileBytes reads the single surviving plan-<slug>-*.json
// state file's raw bytes (unparsed), for exact before/after comparisons
// that a JSON round-trip through readSoleStateDoc could mask.
func readSoleStateFileBytes(t *testing.T, root string) []byte {
	t.Helper()
	files := listStateFiles(t, root)
	if len(files) != 1 {
		t.Fatalf("state files = %v, want exactly 1", files)
	}
	raw, err := os.ReadFile(filepath.Join(root, paths.DataDir, paths.RunsSubdir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// ---------------------------------------------------------------------------
// plan_mark "checkpoint" marker tests
// ---------------------------------------------------------------------------

// TestPlanMark_Checkpoint_ReplaceNotAppend verifies a "checkpoint" marker
// call stores a single PlanCheckpoint object (with updatedAt) at
// st.Data["checkpoint"], that a second call REPLACES it rather than
// appending (unlike guardrailResults/criticalDecisions, which append), that
// Next follows the "Checkpoint saved at step N. Continue step N." template,
// and that a non-checkpoint marker returns no Next.
func TestPlanMark_Checkpoint_ReplaceNotAppend(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	if _, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true}); err != nil {
		t.Fatalf("planPrepareCore (seed): %v", err)
	}

	out1, err := planMark(dir, dir, PlanMarkIn{
		Marker: "checkpoint",
		Data:   map[string]any{"step": "2", "iteration": float64(0), "expectedWriters": []any{}},
	})
	if err != nil {
		t.Fatalf("planMark(checkpoint) #1: %v", err)
	}
	if !out1.OK {
		t.Error("planMark(checkpoint) #1 .OK = false, want true")
	}
	if want := "Checkpoint saved at step 2. Continue step 2."; out1.Next != want {
		t.Errorf("Next #1 = %q, want %q", out1.Next, want)
	}

	doc := readSoleStateDoc(t, dir)
	cp1, ok := doc["checkpoint"].(map[string]any)
	if !ok {
		t.Fatalf("checkpoint missing or wrong type: %v", doc["checkpoint"])
	}
	if cp1["step"] != "2" {
		t.Errorf(`checkpoint.step = %v, want "2"`, cp1["step"])
	}
	if s, ok := cp1["updatedAt"].(string); !ok || s == "" {
		t.Errorf("checkpoint.updatedAt = %v, want a non-empty timestamp string", cp1["updatedAt"])
	}

	out2, err := planMark(dir, dir, PlanMarkIn{
		Marker: "checkpoint",
		Data: map[string]any{
			"step": "3", "iteration": float64(1),
			"expectedWriters": []any{"lane-static-structural-r1", "lane-content-coverage-r1"},
		},
	})
	if err != nil {
		t.Fatalf("planMark(checkpoint) #2: %v", err)
	}
	if want := "Checkpoint saved at step 3. Continue step 3."; out2.Next != want {
		t.Errorf("Next #2 = %q, want %q", out2.Next, want)
	}

	doc = readSoleStateDoc(t, dir)
	if _, isArray := doc["checkpoint"].([]any); isArray {
		t.Fatal("checkpoint stored as an array; want a single replaced object (no append)")
	}
	cp2, ok := doc["checkpoint"].(map[string]any)
	if !ok {
		t.Fatalf("checkpoint missing or wrong type after 2nd call: %v", doc["checkpoint"])
	}
	if cp2["step"] != "3" {
		t.Errorf(`checkpoint.step = %v, want "3" (replaced, not appended)`, cp2["step"])
	}
	wantWriters := []any{"lane-static-structural-r1", "lane-content-coverage-r1"}
	if !reflect.DeepEqual(cp2["expectedWriters"], wantWriters) {
		t.Errorf("checkpoint.expectedWriters = %v, want %v", cp2["expectedWriters"], wantWriters)
	}

	out3, err := planMark(dir, dir, PlanMarkIn{Marker: "critiqueRan"})
	if err != nil {
		t.Fatalf("planMark(critiqueRan): %v", err)
	}
	if out3.Next != "" {
		t.Errorf("Next for critiqueRan = %q, want empty (only checkpoint returns next)", out3.Next)
	}
}

// TestPlanMark_Checkpoint_NextIncludesStyleInstructions_ReadFresh verifies
// Next gains a " Follow the N custom plan instructions (style.instructions)."
// suffix once [planStyle].instructions is non-empty, and that the style is
// read fresh on every call: writing local.toml AFTER the first call
// still changes the very next call's Next.
func TestPlanMark_Checkpoint_NextIncludesStyleInstructions_ReadFresh(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	if _, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true}); err != nil {
		t.Fatalf("planPrepareCore (seed): %v", err)
	}

	data := map[string]any{"step": "3", "iteration": float64(0)}

	out1, err := planMark(dir, dir, PlanMarkIn{Marker: "checkpoint", Data: data})
	if err != nil {
		t.Fatalf("planMark(checkpoint) #1: %v", err)
	}
	if strings.Contains(out1.Next, "custom plan instructions") {
		t.Errorf("Next #1 = %q, want no custom-instructions suffix (no [planStyle] section yet)", out1.Next)
	}

	writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), ""+
		"[planStyle]\n"+
		"instructions = [\"Cite file:line for every claim.\", \"State the delta, not the plan.\"]\n")

	out2, err := planMark(dir, dir, PlanMarkIn{Marker: "checkpoint", Data: data})
	if err != nil {
		t.Fatalf("planMark(checkpoint) #2: %v", err)
	}
	if want := " Follow the 2 custom plan instructions (style.instructions)."; !strings.HasSuffix(out2.Next, want) {
		t.Errorf("Next #2 = %q, want suffix %q", out2.Next, want)
	}
}

// TestPlanMark_Checkpoint_DataErrors table-drives every row of the
// "checkpoint" marker's data-validation error table: each case must return
// a *mcpserver.DomainError with a non-empty Suggestion, matching Msg
// substring, and must write nothing to the plan state file.
func TestPlanMark_Checkpoint_DataErrors(t *testing.T) {
	manyWriters := make([]any, 33)
	for i := range manyWriters {
		manyWriters[i] = fmt.Sprintf("writer-%d", i)
	}

	tests := []struct {
		name    string
		data    map[string]any
		wantMsg string
	}{
		{
			name:    "data missing",
			data:    nil,
			wantMsg: `checkpoint needs data {step, iteration, expectedWriters}`,
		},
		{
			name:    "step missing",
			data:    map[string]any{"iteration": float64(0)},
			wantMsg: `checkpoint step "" is not valid`,
		},
		{
			name:    "step not in validCheckpointSteps",
			data:    map[string]any{"step": "9"},
			wantMsg: `checkpoint step "9" is not valid`,
		},
		{
			name:    "iteration not whole",
			data:    map[string]any{"step": "3", "iteration": float64(1.5)},
			wantMsg: `checkpoint iteration must be an integer >= 0`,
		},
		{
			name:    "iteration negative",
			data:    map[string]any{"step": "3", "iteration": float64(-1)},
			wantMsg: `checkpoint iteration must be an integer >= 0`,
		},
		{
			name:    "unknown key",
			data:    map[string]any{"step": "3", "bogus": "x"},
			wantMsg: `checkpoint data has unknown key "bogus"`,
		},
		{
			name:    "expectedWriters entry fails writerIDRe",
			data:    map[string]any{"step": "3", "expectedWriters": []any{"bad id!"}},
			wantMsg: `checkpoint expectedWriters[0] "bad id!" is not a valid writer ID`,
		},
		{
			name:    "expectedWriters not an array",
			data:    map[string]any{"step": "3", "expectedWriters": "lane-a"},
			wantMsg: `checkpoint expectedWriters must be a JSON array of writer IDs`,
		},
		{
			name:    "expectedWriters over max",
			data:    map[string]any{"step": "3", "expectedWriters": manyWriters},
			wantMsg: `checkpoint expectedWriters has 33 entries, max 32`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			initGitFixture(t, dir)
			gitCommit(t, dir, "initial")

			if _, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true}); err != nil {
				t.Fatalf("planPrepareCore (seed): %v", err)
			}
			before := readSoleStateFileBytes(t, dir)

			_, err := planMark(dir, dir, PlanMarkIn{Marker: "checkpoint", Data: tt.data})
			if err == nil {
				t.Fatal("planMark(checkpoint) = nil error, want an error")
			}
			var domainErr *mcpserver.DomainError
			if !errors.As(err, &domainErr) {
				t.Fatalf("error type = %T, want *mcpserver.DomainError: %v", err, err)
			}
			if !strings.Contains(domainErr.Msg, tt.wantMsg) {
				t.Errorf("Msg = %q, want substring %q", domainErr.Msg, tt.wantMsg)
			}
			if domainErr.Suggestion == "" {
				t.Error("Suggestion is empty, want non-empty")
			}

			after := readSoleStateFileBytes(t, dir)
			if string(before) != string(after) {
				t.Errorf("state file changed after a rejected checkpoint call:\nbefore: %s\nafter:  %s", before, after)
			}
		})
	}
}

// TestPlanMark_Checkpoint_UsesLatestPlanRunExactSlug verifies planMark's
// state lookup now goes through state.LatestPlanRun (exact slug match) for
// every marker, not state.Find's mtime-based, non-delimited prefix match: a
// checkpoint call on branch "feat" must resolve to the exact-slug
// plan-feat-*.json run and leave a newer plan-feat-x-*.json file (a
// different branch, "feat-x") byte-identical, even though Find's prefix
// match would have picked the newer, wrong file.
func TestPlanMark_Checkpoint_UsesLatestPlanRunExactSlug(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	cmd := exec.Command("git", "checkout", "-b", "feat")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b feat: %s: %v", out, err)
	}

	runsDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	const (
		newerSuperstring = "plan-feat-x-20260929T120000Z.json"
		olderExact       = "plan-feat-20260929T110000Z.json"
	)
	superstringContent := `{"marker":"must-not-change"}`
	if err := os.WriteFile(filepath.Join(runsDir, newerSuperstring), []byte(superstringContent), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", newerSuperstring, err)
	}
	if err := os.WriteFile(filepath.Join(runsDir, olderExact), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", olderExact, err)
	}

	out, err := planMark(dir, dir, PlanMarkIn{
		Marker: "checkpoint",
		Data:   map[string]any{"step": "3", "iteration": float64(1), "expectedWriters": []any{"lane-static-structural-r1"}},
	})
	if err != nil {
		t.Fatalf("planMark(checkpoint): %v", err)
	}
	if got := filepath.Base(out.Path); got != olderExact {
		t.Fatalf("planMark wrote %q, want %q (exact slug match, not Find's prefix match)", got, olderExact)
	}

	got, err := os.ReadFile(filepath.Join(runsDir, newerSuperstring))
	if err != nil {
		t.Fatalf("ReadFile %s: %v", newerSuperstring, err)
	}
	if string(got) != superstringContent {
		t.Errorf("plan-feat-x file changed: got %s, want %s (different branch, must stay untouched)", got, superstringContent)
	}
}

// TestPlanMark_InputSchema_ListsCheckpointEnum verifies the plan_mark input
// schema's "marker" field declares all of validMarkers (8 entries,
// including the new "checkpoint") as a jsonschema enum, keeping the MCP
// tool schema in sync with the marker set planMark actually accepts.
func TestPlanMark_InputSchema_ListsCheckpointEnum(t *testing.T) {
	f, ok := reflect.TypeOf(PlanMarkIn{}).FieldByName("Marker")
	if !ok {
		t.Fatal("PlanMarkIn has no Marker field")
	}
	tag := f.Tag.Get("jsonschema")
	for marker := range validMarkers {
		if !strings.Contains(tag, "enum="+marker) {
			t.Errorf("PlanMarkIn.Marker jsonschema tag missing enum=%s: %q", marker, tag)
		}
	}
	if want := 8; len(validMarkers) != want {
		t.Fatalf("len(validMarkers) = %d, want %d (update this test if the marker set intentionally grows)", len(validMarkers), want)
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
	redirectTempManifests(t) // keep the sdlc-explore-* dir out of the OS temp dir
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
	redirectTempManifests(t) // keep the sdlc-explore-* dir out of the OS temp dir
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
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

	outActive, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true, FromOpenspecDirect: true})
	if err != nil {
		t.Fatalf("planPrepareCore (openspec active): %v", err)
	}
	if body := sectionBody(t, outActive.Template.SkeletonMarkdown, "OpenSpec Sync"); body != "[TBD]" {
		t.Errorf("OpenSpec Sync body (fromOpenspecDirect=true) = %q, want [TBD]", body)
	}

	outInactive, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true, Lightweight: true})
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
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

	out, err := runPlanPrepare(t, dir, dir, PlanPrepareIn{SkipConfigCheck: true, ResolveTemplate: true})
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
