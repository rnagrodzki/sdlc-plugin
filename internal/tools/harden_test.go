package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ---------------------------------------------------------------------------
// KD5 config-version gate
// ---------------------------------------------------------------------------

func TestHardenPrepare_KD5Gate(t *testing.T) {
	root := t.TempDir()
	sdlcDir := filepath.Join(root, ".sdlc")
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A stale schemaVersion (below configmigrate.CurrentSchemaVersion) is a
	// genuine migration-needed condition (a merely-absent config.json is not).
	if err := os.WriteFile(filepath.Join(sdlcDir, "config.json"), []byte(`{"schemaVersion": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship-sdlc",
		SkipConfigCheck: false,
	})
	if err == nil {
		t.Fatal("expected an error from the KD5 config-version gate, got nil")
	}
	var dataErr *mcpserver.DataError
	if !errorsAsDataError(err, &dataErr) {
		t.Fatalf("expected *mcpserver.DataError, got %T: %v", err, err)
	}
}

func TestHardenPrepare_SkipConfigCheckBypassesGate(t *testing.T) {
	root := t.TempDir()
	sdlcDir := filepath.Join(root, ".sdlc")
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdlcDir, "config.json"), []byte(`{"schemaVersion": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship-sdlc",
		SkipConfigCheck: true,
	})
	// A schemaVersion-stamped config.json also unconditionally trips
	// config.ReadSection's own permanent pre-v5 rejection (independent of
	// the KD5 gate), so guardrail pre-flight still fails downstream — this
	// fixture cannot reach a full manifest either way. What SkipConfigCheck
	// controls is specifically whether hardenPrepare's own explicit
	// configmigrate.Verify call (the KD5 gate) contributes a *DataError
	// prefixed "config-version:"; assert that specific error is bypassed.
	if err == nil {
		t.Fatal("expected pre-flight to still fail on the legacy config, got nil")
	}
	if _, isDataErr := err.(*mcpserver.DataError); isDataErr {
		t.Fatalf("expected the KD5 config-version DataError to be bypassed, got one anyway: %v", err)
	}
	if containsSubstr(err.Error(), "config-version:") {
		t.Fatalf("expected no config-version gate message with SkipConfigCheck=true, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// R19 --from-issue / --failure-text
// ---------------------------------------------------------------------------

func TestHardenPrepare_MutualExclusion(t *testing.T) {
	root := t.TempDir()
	_, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		FromIssue:       "42",
		Skill:           "ship-sdlc",
		SkipConfigCheck: true,
	})
	if err == nil {
		t.Fatal("expected mutual-exclusion error, got nil")
	}
	var domainErr *mcpserver.DomainError
	if !errorsAsDomainError(err, &domainErr) {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
}

func TestHardenPrepare_FromIssueInvalidNumber(t *testing.T) {
	root := t.TempDir()
	_, err := hardenPrepare(root, root, HardenPrepareIn{
		FromIssue:       "not-a-number",
		Skill:           "ship-sdlc",
		SkipConfigCheck: true,
	})
	if err == nil {
		t.Fatal("expected invalid-issue-number error, got nil")
	}
	var domainErr *mcpserver.DomainError
	if !errorsAsDomainError(err, &domainErr) {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// Required fields
// ---------------------------------------------------------------------------

func TestHardenPrepare_MissingRequiredFields(t *testing.T) {
	root := t.TempDir()
	_, err := hardenPrepare(root, root, HardenPrepareIn{SkipConfigCheck: true})
	if err == nil {
		t.Fatal("expected a missing-required-field error, got nil")
	}
	var domainErr *mcpserver.DomainError
	if !errorsAsDomainError(err, &domainErr) {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
	if !containsSubstr(domainErr.Msg, "failureText") || !containsSubstr(domainErr.Msg, "skill") {
		t.Fatalf("expected message to mention both missing fields, got: %s", domainErr.Msg)
	}
}

// ---------------------------------------------------------------------------
// R16 pre-flight
// ---------------------------------------------------------------------------

func TestHardenPrepare_PreflightGuardrailFailureAbortsNoManifest(t *testing.T) {
	root := t.TempDir()
	writeConfigSection(t, root, "plan", map[string]any{
		"guardrails": []map[string]any{
			{"id": "Bad_ID", "description": "desc"},
		},
	})

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship-sdlc",
		SkipConfigCheck: true,
	})
	if err == nil {
		t.Fatal("expected a pre-flight validation error, got nil")
	}
	var domainErr *mcpserver.DomainError
	if !errorsAsDomainError(err, &domainErr) {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
	if !containsSubstr(domainErr.Msg, "existing-plan-guardrails") {
		t.Fatalf("expected existing-plan-guardrails prefix in message, got: %s", domainErr.Msg)
	}
	if out.ManifestPath != "" {
		t.Fatalf("expected no manifest path on pre-flight abort, got %q", out.ManifestPath)
	}
}

func TestHardenPrepare_PreflightDimensionFailureAbortsNoManifest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".sdlc", "review-dimensions", "bad.md"), "no frontmatter here\n")

	_, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship-sdlc",
		SkipConfigCheck: true,
	})
	if err == nil {
		t.Fatal("expected a pre-flight validation error, got nil")
	}
	var domainErr *mcpserver.DomainError
	if !errorsAsDomainError(err, &domainErr) {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
	if !containsSubstr(domainErr.Msg, "existing-review-dimension bad.md") {
		t.Fatalf("expected existing-review-dimension bad.md prefix in message, got: %s", domainErr.Msg)
	}
}

// ---------------------------------------------------------------------------
// Manifest field fidelity
// ---------------------------------------------------------------------------

func TestHardenPrepare_ManifestFieldFidelity(t *testing.T) {
	root := t.TempDir()

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "  boom happened  ",
		Skill:           "  ship-sdlc  ",
		Step:            "  step-1  ",
		Operation:       "  do-thing  ",
		ExitCode:        " ", // raw non-empty (a single space) — must NOT collapse to null
		ErrorType:       "  timeout  ",
		UserIntent:      "  fix it please  ",
		ArgsString:      "  --flag value  ",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	manifest := readHardenManifest(t, out.ManifestPath)

	failure := manifest["failure"].(map[string]any)
	if failure["text"] != "  boom happened  " {
		t.Errorf("failure.text = %q, want raw (untrimmed) value", failure["text"])
	}
	if failure["skill"] != "ship-sdlc" {
		t.Errorf("failure.skill = %q, want trimmed", failure["skill"])
	}
	if failure["step"] != "step-1" {
		t.Errorf("failure.step = %q, want trimmed", failure["step"])
	}
	if failure["operation"] != "do-thing" {
		t.Errorf("failure.operation = %q, want trimmed", failure["operation"])
	}
	if failure["exitCode"] != " " {
		t.Errorf("failure.exitCode = %v, want raw single-space string (not null, not trimmed)", failure["exitCode"])
	}
	if failure["errorType"] != "timeout" {
		t.Errorf("failure.errorType = %q, want trimmed", failure["errorType"])
	}
	if failure["userIntent"] != "  fix it please  " {
		t.Errorf("failure.userIntent = %q, want raw (untrimmed) value", failure["userIntent"])
	}
	if failure["argsString"] != "  --flag value  " {
		t.Errorf("failure.argsString = %q, want raw (untrimmed) value", failure["argsString"])
	}

	if manifest["classification_hint"] != nil {
		t.Errorf("classification_hint = %v, want nil (no --from-issue)", manifest["classification_hint"])
	}
	if manifest["pluginRepoUrl"] != hardenPluginRepoURL {
		t.Errorf("pluginRepoUrl = %v, want %q", manifest["pluginRepoUrl"], hardenPluginRepoURL)
	}

	errs, ok := manifest["errors"].([]any)
	if !ok {
		t.Fatalf("errors field is not an array: %T", manifest["errors"])
	}
	// This test's temp root has no plugin installation, so
	// resolveErrorReportSkill legitimately soft-fails and contributes one
	// "error-report-skill" entry regardless of the fixture under test (see
	// TestResolveErrorReportSkill_SoftFailsWhenAbsent) — filter it out
	// before asserting on the surfaces this test actually exercises.
	if remaining := filterOutSurface(errs, "error-report-skill"); len(remaining) != 0 {
		t.Errorf("expected no non-error-report-skill errors, got %+v", remaining)
	}

	surfaces := manifest["surfaces"].(map[string]any)
	for _, key := range []string{"planGuardrails", "executeGuardrails", "reviewDimensions", "copilotInstructions"} {
		list, ok := surfaces[key].([]any)
		if !ok {
			t.Fatalf("surfaces.%s is not an array: %T", key, surfaces[key])
		}
		if len(list) != 0 {
			t.Errorf("surfaces.%s expected empty, got %+v", key, list)
		}
	}
}

func TestHardenPrepare_ExitCodeEmptyIsNull(t *testing.T) {
	root := t.TempDir()
	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship-sdlc",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	failure := manifest["failure"].(map[string]any)
	if failure["exitCode"] != nil {
		t.Errorf("exitCode = %v, want nil when not supplied", failure["exitCode"])
	}
}

// ---------------------------------------------------------------------------
// Surfaces: guardrails / review dimensions / copilot instructions
// ---------------------------------------------------------------------------

func TestHardenPrepare_LoadsGuardrailSurfaces(t *testing.T) {
	root := t.TempDir()
	// writeConfigSection replaces the whole config.json per call, so both
	// sections must be written together in one file, not via two calls.
	writeFile(t, filepath.Join(root, ".sdlc", "config.json"), `{
		"plan": {"guardrails": [
			{"id": "no-console-log", "severity": "warning", "description": "Avoid console.log in production code."}
		]},
		"execute": {"guardrails": [
			{"id": "no-severity-guardrail", "description": "Missing severity should default to error."}
		]}
	}`)

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship-sdlc",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	surfaces := manifest["surfaces"].(map[string]any)

	plan := surfaces["planGuardrails"].([]any)
	if len(plan) != 1 {
		t.Fatalf("expected 1 plan guardrail, got %+v", plan)
	}
	g := plan[0].(map[string]any)
	if g["id"] != "no-console-log" || g["severity"] != "warning" || g["description"] != "Avoid console.log in production code." {
		t.Errorf("unexpected plan guardrail: %+v", g)
	}

	exec := surfaces["executeGuardrails"].([]any)
	if len(exec) != 1 {
		t.Fatalf("expected 1 execute guardrail, got %+v", exec)
	}
	eg := exec[0].(map[string]any)
	if eg["id"] != "no-severity-guardrail" || eg["severity"] != "error" {
		t.Errorf("expected id passed through and severity defaulted to 'error', got %+v", eg)
	}
}

func TestHardenPrepare_LoadsReviewDimensionsMetadataOnly(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".sdlc", "review-dimensions", "security.md"),
		"---\n"+
			"name: security-review\n"+
			"severity: high\n"+
			"description: Check for security issues in the diff.\n"+
			"triggers:\n"+
			"  - \"**/*.go\"\n"+
			"model: opus\n"+
			"---\n"+
			"# Full body content\n\nThis should NOT appear in the manifest (metadata-only, RULING 1).\n")

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship-sdlc",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	surfaces := manifest["surfaces"].(map[string]any)
	dims := surfaces["reviewDimensions"].([]any)
	if len(dims) != 1 {
		t.Fatalf("expected 1 review dimension, got %+v", dims)
	}
	d := dims[0].(map[string]any)
	if d["name"] != "security-review" || d["severity"] != "high" || d["model"] != "opus" {
		t.Errorf("unexpected review dimension metadata: %+v", d)
	}
	triggers, ok := d["triggers"].([]any)
	if !ok || len(triggers) != 1 || triggers[0] != "**/*.go" {
		t.Errorf("unexpected triggers: %+v", d["triggers"])
	}
	// RULING 1: metadata-only. No rendered/body content, no extra keys.
	allowed := map[string]bool{"name": true, "severity": true, "description": true, "triggers": true, "model": true, "path": true}
	for k := range d {
		if !allowed[k] {
			t.Errorf("unexpected key %q in reviewDimensions entry (metadata-only surface): %+v", k, d)
		}
	}
	if got, ok := d["path"].(string); !ok || got == "" {
		t.Errorf("expected non-empty path, got %v", d["path"])
	}
}

func TestHardenPrepare_LoadsCopilotInstructions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".github", "instructions", "go-style.instructions.md"),
		"---\napplyTo: \"**/*.go\"\n---\nBody content.\n")

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship-sdlc",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	surfaces := manifest["surfaces"].(map[string]any)
	insts := surfaces["copilotInstructions"].([]any)
	if len(insts) != 1 {
		t.Fatalf("expected 1 copilot instruction, got %+v", insts)
	}
	inst := insts[0].(map[string]any)
	if inst["applyTo"] != "**/*.go" || inst["name"] != "go-style" {
		t.Errorf("unexpected copilot instruction: %+v", inst)
	}
}

func TestHardenPrepare_CopilotInstructionsToleratesMissingFrontmatter(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".github", "instructions", "no-fm.instructions.md"), "Just a body, no frontmatter.\n")

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship-sdlc",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	surfaces := manifest["surfaces"].(map[string]any)
	insts := surfaces["copilotInstructions"].([]any)
	if len(insts) != 1 {
		t.Fatalf("expected 1 copilot instruction despite missing frontmatter, got %+v", insts)
	}
	inst := insts[0].(map[string]any)
	if inst["applyTo"] != "" || inst["name"] != "no-fm" {
		t.Errorf("unexpected copilot instruction for missing-frontmatter file: %+v", inst)
	}
	// Missing frontmatter is not an error for copilot-instructions (unlike
	// review-dimensions). Filter out the environment-dependent
	// error-report-skill soft-fail (see TestResolveErrorReportSkill_SoftFailsWhenAbsent).
	errs := manifest["errors"].([]any)
	if remaining := filterOutSurface(errs, "error-report-skill"); len(remaining) != 0 {
		t.Errorf("expected no surface-load errors for missing frontmatter, got %+v", remaining)
	}
}

// ---------------------------------------------------------------------------
// Pipeline state (state.FindAny — branch-agnostic, RULING 4)
// ---------------------------------------------------------------------------

func TestHardenPrepare_PipelineStateIsBranchAgnostic(t *testing.T) {
	root := t.TempDir()

	// A ship-state file for a DIFFERENT branch than any branch this test
	// process is on — state.FindAny must still find it (no branch filter).
	st, err := state.Init(root, "ship", "some-other-branch", "")
	if err != nil {
		t.Fatalf("state.Init: %v", err)
	}
	st.Data["paused"] = true
	st.Data["currentStep"] = "step-3"
	st.Data["lastFailedStep"] = "step-2"
	if err := state.Write(st); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	execSt, err := state.Init(root, "execute", "yet-another-branch", "")
	if err != nil {
		t.Fatalf("state.Init: %v", err)
	}
	execSt.Data["failedTask"] = "task-7"
	execSt.Data["failedWave"] = float64(2)
	if err := state.Write(execSt); err != nil {
		t.Fatalf("state.Write: %v", err)
	}

	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship-sdlc",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	pipeline := manifest["pipeline"].(map[string]any)

	shipState := pipeline["shipState"].(map[string]any)
	if shipState["paused"] != true {
		t.Errorf("shipState.paused = %v, want true", shipState["paused"])
	}
	if shipState["currentStep"] != "step-3" {
		t.Errorf("shipState.currentStep = %v, want step-3", shipState["currentStep"])
	}
	if shipState["lastFailedStep"] != "step-2" {
		t.Errorf("shipState.lastFailedStep = %v, want step-2", shipState["lastFailedStep"])
	}

	executeState := pipeline["executeState"].(map[string]any)
	if executeState["failedTask"] != "task-7" {
		t.Errorf("executeState.failedTask = %v, want task-7", executeState["failedTask"])
	}
	if executeState["failedWave"] != float64(2) {
		t.Errorf("executeState.failedWave = %v, want 2", executeState["failedWave"])
	}
}

func TestHardenPrepare_PipelineStateAbsentIsNull(t *testing.T) {
	root := t.TempDir()
	out, err := hardenPrepare(root, root, HardenPrepareIn{
		FailureText:     "boom",
		Skill:           "ship-sdlc",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	manifest := readHardenManifest(t, out.ManifestPath)
	pipeline := manifest["pipeline"].(map[string]any)
	if pipeline["shipState"] != nil {
		t.Errorf("shipState = %v, want nil with no state files", pipeline["shipState"])
	}
	if pipeline["executeState"] != nil {
		t.Errorf("executeState = %v, want nil with no state files", pipeline["executeState"])
	}
}

// ---------------------------------------------------------------------------
// Pure-function helpers
// ---------------------------------------------------------------------------

func TestIssueLabelNames_MixedStringAndObjectLabels(t *testing.T) {
	raw := []any{
		"good-first-issue",
		map[string]any{"name": "mcp-failure", "color": "ff0000"},
		map[string]any{"noNameField": true},
		42, // not a string or {name} object — silently dropped
	}
	got := issueLabelNames(raw)
	want := []string{"good-first-issue", "mcp-failure"}
	if len(got) != len(want) {
		t.Fatalf("issueLabelNames = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("issueLabelNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if !containsStr(got, "mcp-failure") {
		t.Error("expected containsStr to find mcp-failure label")
	}
}

func TestAnySliceToStrings(t *testing.T) {
	if got := anySliceToStrings([]any{"a", "b", 3, "c"}); len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("anySliceToStrings dropped-non-string case = %+v", got)
	}
	if got := anySliceToStrings("not-an-array"); len(got) != 0 {
		t.Errorf("anySliceToStrings(non-array) = %+v, want empty", got)
	}
	if got := anySliceToStrings(nil); len(got) != 0 {
		t.Errorf("anySliceToStrings(nil) = %+v, want empty", got)
	}
}

func TestGuardrailsPreflight_PassesWithNoConfig(t *testing.T) {
	root := t.TempDir()
	if errs := guardrailsPreflight(root); len(errs) != 0 {
		t.Errorf("expected no pre-flight errors with no config, got %+v", errs)
	}
}

func TestDimensionsPreflight_PassesWithNoDir(t *testing.T) {
	root := t.TempDir()
	if errs := dimensionsPreflight(root); len(errs) != 0 {
		t.Errorf("expected no pre-flight errors with no dimensions dir, got %+v", errs)
	}
}

func TestResolveErrorReportSkill_SoftFailsWhenAbsent(t *testing.T) {
	var errs []surfaceLoadError
	got := resolveErrorReportSkill(&errs)
	// This repo has no skills/ directory yet (confirmed during investigation),
	// so this must soft-fail: empty string, one recorded error, no panic.
	if got != "" {
		t.Errorf("resolveErrorReportSkill = %q, want empty string when the target file does not exist", got)
	}
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 surface load error, got %+v", errs)
	}
	if errs[0].Surface != "error-report-skill" {
		t.Errorf("errs[0].Surface = %q, want error-report-skill", errs[0].Surface)
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func errorsAsDataError(err error, target **mcpserver.DataError) bool {
	de, ok := err.(*mcpserver.DataError)
	if !ok {
		return false
	}
	*target = de
	return true
}

func errorsAsDomainError(err error, target **mcpserver.DomainError) bool {
	de, ok := err.(*mcpserver.DomainError)
	if !ok {
		return false
	}
	*target = de
	return true
}

func containsSubstr(s, substr string) bool {
	return len(s) >= len(substr) && (substr == "" || indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// filterOutSurface drops every {surface,message} entry (decoded as
// map[string]any) whose "surface" field equals the given name. Used to
// exclude the environment-dependent error-report-skill soft-fail (this test
// binary has no real plugin installation on disk) from assertions about the
// surfaces a given test fixture actually exercises.
func filterOutSurface(errs []any, surface string) []any {
	var out []any
	for _, e := range errs {
		m, ok := e.(map[string]any)
		if !ok || m["surface"] != surface {
			out = append(out, e)
		}
	}
	return out
}

func readHardenManifest(t *testing.T, path string) map[string]any {
	t.Helper()
	if path == "" {
		t.Fatal("manifest path is empty")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	return m
}

// keep time import used even if a future edit trims a test that needed it directly.
var _ = time.Now
