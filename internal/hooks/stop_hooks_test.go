package hooks

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// captureStderr temporarily redirects os.Stderr for the duration of fn and
// returns everything written to it. Safe here because no test in this
// package uses t.Parallel(), so the process-wide os.Stderr is never
// contended for.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stderr: %v", err)
	}
	return string(out)
}

// ---------------------------------------------------------------------------
// stopStateSave
//
// Shares saveCompactRecovery's core with preCompactSave; pre_compact_save_test.go
// covers the shared mechanics (mtime-sourced savedAt, session-gate no-write,
// ship-priority, value-preserving flags.auto) via preCompactSave, so these
// tests focus on stopStateSave's own recovery-object field derivation.
// ---------------------------------------------------------------------------

func TestStopStateSave_NeitherShipNorExecute_Silent(t *testing.T) {
	root := gitFixture(t, "feat/sss-none")
	branch := "feat/sss-none"

	out, err := stopStateSave(HookCtx{SessionID: "s1"}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)

	data, err := state.ConsumeRecoverySidecar(root, state.SlugifyBranch(branch))
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		t.Errorf("recovery sidecar = %v, want nil (nothing to save)", data)
	}
}

func TestStopStateSave_ShipState_DerivesRecoveryFields(t *testing.T) {
	root := gitFixture(t, "feat/sss-ship")
	branch := "feat/sss-ship"
	newShipState(t, root, branch, "s1", []any{
		map[string]any{"name": "review", "status": "completed", "output": map[string]any{"verdict": "approved"}},
		map[string]any{"name": "pr", "status": "in_progress"},
	}, map[string]any{"auto": true, "preset": "default"})

	out, err := stopStateSave(HookCtx{SessionID: "s1"}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)

	data, err := state.ConsumeRecoverySidecar(root, state.SlugifyBranch(branch))
	if err != nil {
		t.Fatal(err)
	}
	recovery, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("recovery = %v (%T), want map[string]any", data, data)
	}
	if recovery["pipeline"] != "ship" {
		t.Errorf("pipeline = %v, want ship", recovery["pipeline"])
	}
	if recovery["currentStep"] != "pr" {
		t.Errorf("currentStep = %v, want pr (in_progress step wins over the last completed step)", recovery["currentStep"])
	}
	if recovery["reviewVerdict"] != "approved" {
		t.Errorf("reviewVerdict = %v, want approved (last truthy value found scanning steps[].output)", recovery["reviewVerdict"])
	}
}

func TestStopStateSave_LastCompletedFallbackAndTruthyWinsAcrossSteps(t *testing.T) {
	root := gitFixture(t, "feat/sss-fallback")
	branch := "feat/sss-fallback"
	// No in_progress step at all: currentStep must fall back to the *last*
	// completed step (JS: [...steps].reverse().find(status === 'completed')),
	// not the first. deferredFindings is set only on the first step and must
	// survive the second step's output (which lacks it) — a later falsy/absent
	// value must not clobber an earlier truthy one. reviewVerdict is set on
	// both steps, so the second (later) truthy value must win.
	newShipState(t, root, branch, "s1", []any{
		map[string]any{"name": "plan", "status": "completed", "output": map[string]any{
			"deferredFindings": "flag-A",
			"verdict":          "approved",
		}},
		map[string]any{"name": "review", "status": "completed", "output": map[string]any{
			"verdict": "changes-requested",
		}},
	}, map[string]any{"auto": true})

	out, err := stopStateSave(HookCtx{SessionID: "s1"}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)

	data, err := state.ConsumeRecoverySidecar(root, state.SlugifyBranch(branch))
	if err != nil {
		t.Fatal(err)
	}
	recovery, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("recovery = %v (%T), want map[string]any", data, data)
	}
	if recovery["currentStep"] != "review" {
		t.Errorf("currentStep = %v, want review (last completed step, no in_progress step present)", recovery["currentStep"])
	}
	if recovery["reviewVerdict"] != "changes-requested" {
		t.Errorf("reviewVerdict = %v, want changes-requested (later truthy value wins over an earlier one)", recovery["reviewVerdict"])
	}
	if recovery["deferredFindings"] != "flag-A" {
		t.Errorf("deferredFindings = %v, want flag-A (an earlier truthy value must survive a later step with no such output)", recovery["deferredFindings"])
	}
}

func TestStopStateSave_ExecuteFallback_DerivesWaveFields(t *testing.T) {
	root := gitFixture(t, "feat/sss-execute")
	branch := "feat/sss-execute"

	est, err := state.Init(root, "execute", branch, "s1")
	if err != nil {
		t.Fatal(err)
	}
	est.Data["waves"] = []any{
		map[string]any{"status": "completed"},
		map[string]any{"status": "pending"},
	}
	est.Data["preset"] = "fast"
	if err := state.Write(est); err != nil {
		t.Fatal(err)
	}

	out, err := stopStateSave(HookCtx{SessionID: "s1"}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)

	data, err := state.ConsumeRecoverySidecar(root, state.SlugifyBranch(branch))
	if err != nil {
		t.Fatal(err)
	}
	recovery, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("recovery = %v (%T), want map[string]any", data, data)
	}
	if recovery["pipeline"] != "execute" {
		t.Errorf("pipeline = %v, want execute", recovery["pipeline"])
	}
	// The sidecar round-trips through JSON, so Go ints decode back as float64.
	if recovery["totalWaves"] != float64(2) {
		t.Errorf("totalWaves = %v, want 2", recovery["totalWaves"])
	}
	if recovery["completedWaves"] != float64(1) {
		t.Errorf("completedWaves = %v, want 1", recovery["completedWaves"])
	}
	if recovery["preset"] != "fast" {
		t.Errorf("preset = %v, want fast", recovery["preset"])
	}
}

// ---------------------------------------------------------------------------
// stopPlanIntegrity
// ---------------------------------------------------------------------------

// allPlanMarkers returns a planIntegrity map with all four
// requiredPlanMarkers plus "done" set — i.e. a plan that has declared
// itself finished (via plan_mark({marker: "done"})) with every checkpoint
// present. Callers exercising the "plan still running" (no "done") path
// must delete("done") from the returned map first.
func allPlanMarkers() map[string]any {
	return map[string]any{
		"skillInvoked":        "2024-01-01T00:00:00Z",
		"planFile":            "2024-01-01T00:00:01Z",
		"guardrailsEvaluated": "2024-01-01T00:00:02Z",
		"critiqueRan":         "2024-01-01T00:00:03Z",
		"done":                "2024-01-01T00:00:04Z",
	}
}

func newPlanState(t *testing.T, root, branch string, planIntegrity map[string]any, planFilePath string) *state.State {
	t.Helper()
	st, err := state.Init(root, "plan", branch, "")
	if err != nil {
		t.Fatal(err)
	}
	if planIntegrity != nil {
		st.Data["planIntegrity"] = planIntegrity
	}
	if planFilePath != "" {
		st.Data["planFilePath"] = planFilePath
	}
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}
	return st
}

// writePlanRun writes the plan run file <runID>.json with data into the runs
// folder of root and returns its path. A test that needs two runs of one
// branch uses it, because newPlanState (state.Init) names a run by the
// second, so two runs created in one second share one file.
func writePlanRun(t *testing.T, root, runID string, data map[string]any) string {
	t.Helper()
	dir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	mustMkdirAll(t, dir)
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, runID+".json")
	mustWriteFile(t, path, string(raw))
	return path
}

func TestStopPlanIntegrity_AllMarkersPresent_KeepsStateSilently(t *testing.T) {
	root := gitFixture(t, "feat/plan-pass")
	branch := "feat/plan-pass"

	planFile := filepath.Join(root, "plans", "my-plan.md")
	mustMkdirAll(t, filepath.Join(root, "plans"))
	mustWriteFile(t, planFile, "# Plan\n\nSome content.\n")

	st := newPlanState(t, root, branch, allPlanMarkers(), planFile)

	out, err := stopPlanIntegrity(HookCtx{}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)

	if _, statErr := os.Stat(st.Path); statErr != nil {
		t.Fatalf("plan state file should survive a done Stop (deletion is owned elsewhere), stat err = %v", statErr)
	}

	// Second consecutive invocation: the state file is still there and still
	// done, so the same silent, no-op path is taken again.
	out2, err2 := stopPlanIntegrity(HookCtx{}, Event{})
	if err2 != nil {
		t.Fatal(err2)
	}
	assertSilent(t, out2)
	if _, statErr := os.Stat(st.Path); statErr != nil {
		t.Fatalf("plan state file should still exist after a second Stop, stat err = %v", statErr)
	}
}

func TestStopPlanIntegrity_MissingMarker_WarnsKeepsNeverBlocks(t *testing.T) {
	root := gitFixture(t, "feat/plan-missing")
	branch := "feat/plan-missing"

	planFile := filepath.Join(root, "plans", "my-plan.md")
	mustMkdirAll(t, filepath.Join(root, "plans"))
	mustWriteFile(t, planFile, "# Plan\n\nSome content.\n")

	markers := allPlanMarkers()
	delete(markers, "critiqueRan")
	st := newPlanState(t, root, branch, markers, planFile)

	var out Output
	stderrText := captureStderr(t, func() {
		var err error
		out, err = stopPlanIntegrity(HookCtx{}, Event{})
		if err != nil {
			t.Fatal(err)
		}
	})
	assertSilent(t, out)

	if _, statErr := os.Stat(st.Path); statErr != nil {
		t.Fatalf("plan state file should survive a done Stop even with missing markers, stat err = %v", statErr)
	}
	if !strings.Contains(stderrText, "Missing checkpoints: critiqueRan") {
		t.Errorf("stderr = %q, want it to list critiqueRan as missing", stderrText)
	}
	if !strings.Contains(stderrText, "critiqueRan: Step 3 self-critique did not run") {
		t.Errorf("stderr = %q, want the critiqueRan description line", stderrText)
	}
}

// TestStopPlanIntegrity_CreationIntentUserPrompt_RicherWarning verifies that
// when the plan state file carries a creationIntent.userPrompt (written by
// plan_prepare's resolveTemplate call — see writeCreationIntent in
// internal/tools/plan.go), the stop hook's warning names the originating
// request instead of the generic header, while the rest of the aggregated
// warning (missing-checkpoint list, description lines) is unchanged.
func TestStopPlanIntegrity_CreationIntentUserPrompt_RicherWarning(t *testing.T) {
	root := gitFixture(t, "feat/plan-intent")
	branch := "feat/plan-intent"

	planFile := filepath.Join(root, "plans", "my-plan.md")
	mustMkdirAll(t, filepath.Join(root, "plans"))
	mustWriteFile(t, planFile, "# Plan\n\nSome content.\n")

	markers := allPlanMarkers()
	delete(markers, "critiqueRan")
	st := newPlanState(t, root, branch, markers, planFile)
	st.Data["creationIntent"] = map[string]any{
		"userPrompt": "fix the login bug",
		"scope":      "lightweight",
		"routing":    "2 files detected — lightweight pipeline",
		"timestamp":  "2024-01-01T00:00:00Z",
	}
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}

	var out Output
	stderrText := captureStderr(t, func() {
		var err error
		out, err = stopPlanIntegrity(HookCtx{}, Event{})
		if err != nil {
			t.Fatal(err)
		}
	})
	assertSilent(t, out)

	if !strings.Contains(stderrText, "plan for 'fix the login bug' had incomplete checkpoints") {
		t.Errorf("stderr = %q, want the creationIntent.userPrompt-enriched header", stderrText)
	}
	if !strings.Contains(stderrText, "Missing checkpoints: critiqueRan") {
		t.Errorf("stderr = %q, want it to still list critiqueRan as missing", stderrText)
	}
	if !strings.Contains(stderrText, "critiqueRan: Step 3 self-critique did not run") {
		t.Errorf("stderr = %q, want the critiqueRan description line", stderrText)
	}
}

// TestStopPlanIntegrity_NoCreationIntent_GenericWarning verifies the
// pre-existing generic header is unchanged when creationIntent is absent
// (e.g. a plan state file created before this enrichment, or a plan_prepare
// call that never passed resolveTemplate:true).
func TestStopPlanIntegrity_NoCreationIntent_GenericWarning(t *testing.T) {
	root := gitFixture(t, "feat/plan-no-intent")
	branch := "feat/plan-no-intent"

	planFile := filepath.Join(root, "plans", "my-plan.md")
	mustMkdirAll(t, filepath.Join(root, "plans"))
	mustWriteFile(t, planFile, "# Plan\n\nSome content.\n")

	markers := allPlanMarkers()
	delete(markers, "critiqueRan")
	newPlanState(t, root, branch, markers, planFile)

	stderrText := captureStderr(t, func() {
		if _, err := stopPlanIntegrity(HookCtx{}, Event{}); err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(stderrText, "WARNING: Plan presented with incomplete plan execution.") {
		t.Errorf("stderr = %q, want the generic header when creationIntent is absent", stderrText)
	}
	if strings.Contains(stderrText, "had incomplete checkpoints") {
		t.Errorf("stderr = %q, did not want the enriched header when creationIntent is absent", stderrText)
	}
}

func TestStopPlanIntegrity_EmptyPlanFileOnDisk_FlagsPlanFileWithPath(t *testing.T) {
	root := gitFixture(t, "feat/plan-empty-file")
	branch := "feat/plan-empty-file"

	planFile := filepath.Join(root, "plans", "empty-plan.md")
	mustMkdirAll(t, filepath.Join(root, "plans"))
	mustWriteFile(t, planFile, "")

	st := newPlanState(t, root, branch, allPlanMarkers(), planFile)

	var out Output
	stderrText := captureStderr(t, func() {
		var err error
		out, err = stopPlanIntegrity(HookCtx{}, Event{})
		if err != nil {
			t.Fatal(err)
		}
	})
	assertSilent(t, out)

	if _, statErr := os.Stat(st.Path); statErr != nil {
		t.Fatalf("plan state file should survive a done Stop, stat err = %v", statErr)
	}
	if !strings.Contains(stderrText, "Missing checkpoints: planFile") {
		t.Errorf("stderr = %q, want planFile flagged despite the marker string being present", stderrText)
	}
	if !strings.Contains(stderrText, "planFilePath="+planFile) {
		t.Errorf("stderr = %q, want the planFilePath-annotated description", stderrText)
	}
}

func TestStopPlanIntegrity_NoDoneMarker_KeepsFileSilently(t *testing.T) {
	root := gitFixture(t, "feat/plan-not-done")
	branch := "feat/plan-not-done"

	planFile := filepath.Join(root, "plans", "my-plan.md")
	mustMkdirAll(t, filepath.Join(root, "plans"))
	mustWriteFile(t, planFile, "# Plan\n\nSome content.\n")

	markers := allPlanMarkers()
	delete(markers, "done")
	st := newPlanState(t, root, branch, markers, planFile)

	var out Output
	stderrText := captureStderr(t, func() {
		var err error
		out, err = stopPlanIntegrity(HookCtx{}, Event{})
		if err != nil {
			t.Fatal(err)
		}
	})
	assertSilent(t, out)

	if _, statErr := os.Stat(st.Path); statErr != nil {
		t.Fatalf("plan state file should still exist (no %q marker — plan still running), stat err = %v", "done", statErr)
	}
	if stderrText != "" {
		t.Errorf("stderr = %q, want empty (no evaluation happens before %q is stamped)", stderrText, "done")
	}
}

func TestStopPlanIntegrity_NoDoneMarker_EvenWithMissingRequiredMarkers_KeepsFileSilently(t *testing.T) {
	// Proves the "done" gate short-circuits BEFORE the requiredPlanMarkers
	// check runs at all: a plan missing critiqueRan (which would normally
	// warn once evaluated) produces no warning and no deletion as long as
	// "done" is absent.
	root := gitFixture(t, "feat/plan-not-done-missing")
	branch := "feat/plan-not-done-missing"

	markers := allPlanMarkers()
	delete(markers, "done")
	delete(markers, "critiqueRan")
	st := newPlanState(t, root, branch, markers, "")

	var out Output
	stderrText := captureStderr(t, func() {
		var err error
		out, err = stopPlanIntegrity(HookCtx{}, Event{})
		if err != nil {
			t.Fatal(err)
		}
	})
	assertSilent(t, out)

	if _, statErr := os.Stat(st.Path); statErr != nil {
		t.Fatalf("plan state file should still exist, stat err = %v", statErr)
	}
	if stderrText != "" {
		t.Errorf("stderr = %q, want empty", stderrText)
	}
}

func TestStopPlanIntegrity_NoPlanIntegrityData_KeepsFileSilently(t *testing.T) {
	// A plan state file with no planIntegrity map at all (e.g. freshly
	// initialized, before any plan_mark call) is treated the same as "no
	// done marker": kept, silent, no warning.
	root := gitFixture(t, "feat/plan-empty-integrity")
	branch := "feat/plan-empty-integrity"

	st := newPlanState(t, root, branch, nil, "")

	var out Output
	stderrText := captureStderr(t, func() {
		var err error
		out, err = stopPlanIntegrity(HookCtx{}, Event{})
		if err != nil {
			t.Fatal(err)
		}
	})
	assertSilent(t, out)

	if _, statErr := os.Stat(st.Path); statErr != nil {
		t.Fatalf("plan state file should still exist, stat err = %v", statErr)
	}
	if stderrText != "" {
		t.Errorf("stderr = %q, want empty", stderrText)
	}
}

func TestStopPlanIntegrity_NoState_TranscriptShowsPlanMode_Warns(t *testing.T) {
	root := gitFixture(t, "feat/plan-transcript-warn")

	transcriptPath := filepath.Join(root, "transcript.jsonl")
	mustWriteFile(t, transcriptPath, "some preceding lines\nPlan mode is active for this turn\nmore lines\n")

	var out Output
	stderrText := captureStderr(t, func() {
		var err error
		out, err = stopPlanIntegrity(HookCtx{}, Event{Raw: map[string]any{"transcript_path": transcriptPath}})
		if err != nil {
			t.Fatal(err)
		}
	})
	assertSilent(t, out)

	want := "no plan integrity state for branch feat/plan-transcript-warn"
	if !strings.Contains(stderrText, want) {
		t.Errorf("stderr = %q, want substring %q", stderrText, want)
	}
}

func TestStopPlanIntegrity_NoState_TranscriptWithoutPlanMode_NoWarning(t *testing.T) {
	root := gitFixture(t, "feat/plan-transcript-quiet")

	transcriptPath := filepath.Join(root, "transcript.jsonl")
	mustWriteFile(t, transcriptPath, "ordinary transcript content, nothing plan-mode related\n")

	var out Output
	stderrText := captureStderr(t, func() {
		var err error
		out, err = stopPlanIntegrity(HookCtx{}, Event{Raw: map[string]any{"transcript_path": transcriptPath}})
		if err != nil {
			t.Fatal(err)
		}
	})
	assertSilent(t, out)
	if strings.Contains(stderrText, "plan-integrity") {
		t.Errorf("stderr = %q, want no warning when transcript lacks the marker text", stderrText)
	}
}

func TestStopPlanIntegrity_NoStateNoTranscriptPath_Silent(t *testing.T) {
	gitFixture(t, "feat/plan-nothing")
	out, err := stopPlanIntegrity(HookCtx{}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}

func TestStopPlanIntegrity_BranchDoesNotResolve_SkipsEvenTranscriptFallback(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)

	transcriptPath := filepath.Join(dir, "transcript.txt")
	mustWriteFile(t, transcriptPath, "... Plan mode is active ...")

	var out Output
	stderrText := captureStderr(t, func() {
		var err error
		out, err = stopPlanIntegrity(HookCtx{}, Event{Raw: map[string]any{"transcript_path": transcriptPath}})
		if err != nil {
			t.Fatal(err)
		}
	})
	assertSilent(t, out)
	if strings.Contains(stderrText, "plan-integrity") {
		t.Errorf("stderr = %q, want no warning — branch never resolved (not a git repo)", stderrText)
	}
}

// ---------------------------------------------------------------------------
// stopPlanIntegrity: preplan session (whole-transcript scan)
// ---------------------------------------------------------------------------

// preplanCommandRecordFixture is the slash-command record of a preplan
// invocation. It repeats the production marker text on purpose, so a change
// of preplanTranscriptMarkers breaks a test.
const preplanCommandRecordFixture = "<command-name>/sdlc:preplan</command-name>"

// preplanSkillCallFixture is the Skill tool call record of a preplan
// invocation. It holds the second production marker.
const preplanSkillCallFixture = `{"type":"tool_use","name":"Skill","input":{"skill":"sdlc:preplan"}}`

// planModeLineFixture is the text that makes the hook consider a warning.
const planModeLineFixture = "Plan mode is active for this turn\n"

// preplanFillerFixture is more than transcriptTailLimit bytes of text with
// no preplan marker. It pushes a record out of the 64 KiB tail.
var preplanFillerFixture = strings.Repeat("filler line with no marker\n", 3000)

// preplanHookWarns runs stopPlanIntegrity on a branch with no plan state and
// a transcript file that holds content. It reports whether the plan-integrity
// warning reached stderr.
func preplanHookWarns(t *testing.T, content string) bool {
	t.Helper()
	root := gitFixture(t, "feat/preplan-scan")

	transcriptPath := filepath.Join(root, "transcript.jsonl")
	mustWriteFile(t, transcriptPath, content)

	var out Output
	stderrText := captureStderr(t, func() {
		var err error
		out, err = stopPlanIntegrity(HookCtx{}, Event{Raw: map[string]any{"transcript_path": transcriptPath}})
		if err != nil {
			t.Fatal(err)
		}
	})
	assertSilent(t, out)
	return strings.Contains(stderrText, "no plan integrity state for branch feat/preplan-scan")
}

// lowerTranscriptScanMax sets transcriptPreplanScanMax to limit for the
// test and restores the old value on cleanup.
func lowerTranscriptScanMax(t *testing.T, limit int64) {
	t.Helper()
	orig := transcriptPreplanScanMax
	transcriptPreplanScanMax = limit
	t.Cleanup(func() { transcriptPreplanScanMax = orig })
}

// lowerTranscriptScanBufSize sets transcriptScanBufSize to size for the test
// and restores the old value on cleanup.
func lowerTranscriptScanBufSize(t *testing.T, size int) {
	t.Helper()
	orig := transcriptScanBufSize
	transcriptScanBufSize = size
	t.Cleanup(func() { transcriptScanBufSize = orig })
}

// TestStopPlanIntegrity_PreplanCommandRecordBeyondTail_NoWarning pins that a
// preplan command record outside the 64 KiB tail suppresses the warning.
func TestStopPlanIntegrity_PreplanCommandRecordBeyondTail_NoWarning(t *testing.T) {
	content := "first record of the transcript\n" + preplanCommandRecordFixture + "\n" + preplanFillerFixture + planModeLineFixture
	if preplanHookWarns(t, content) {
		t.Error("warning shown, want none: the transcript holds a preplan command record")
	}
}

// TestStopPlanIntegrity_PreplanSkillCallBeyondTail_NoWarning pins that a
// Skill tool call for sdlc:preplan outside the tail suppresses the warning.
func TestStopPlanIntegrity_PreplanSkillCallBeyondTail_NoWarning(t *testing.T) {
	content := "first record of the transcript\n" + preplanSkillCallFixture + "\n" + preplanFillerFixture + planModeLineFixture
	if preplanHookWarns(t, content) {
		t.Error("warning shown, want none: the transcript holds a preplan Skill call")
	}
}

// TestStopPlanIntegrity_PreplanAbsentBeyondTail_Warns pins that the same
// transcript without a preplan record still warns.
func TestStopPlanIntegrity_PreplanAbsentBeyondTail_Warns(t *testing.T) {
	content := "first record of the transcript\n" + preplanFillerFixture + planModeLineFixture
	if !preplanHookWarns(t, content) {
		t.Error("no warning shown, want the plan-integrity warning: no preplan record")
	}
}

// TestStopPlanIntegrity_PreplanMarkerSplitAcrossBuffers_NoWarning pins that a
// marker split across two read buffers matches. The buffer size is 16 bytes
// and the marker starts at byte 10, so it crosses the boundaries at 16, 32
// and 48.
func TestStopPlanIntegrity_PreplanMarkerSplitAcrossBuffers_NoWarning(t *testing.T) {
	lowerTranscriptScanBufSize(t, 16)
	content := "0123456789" + preplanCommandRecordFixture + "\n" + planModeLineFixture
	if preplanHookWarns(t, content) {
		t.Error("warning shown, want none: the marker is split across read buffers")
	}
}

// TestStopPlanIntegrity_PreplanMarkerEndsPastScanBound_Warns pins that a
// marker which ends 1 byte after the scan bound does not match.
func TestStopPlanIntegrity_PreplanMarkerEndsPastScanBound_Warns(t *testing.T) {
	const prefix = "pad\n"
	lowerTranscriptScanMax(t, int64(len(prefix)+len(preplanCommandRecordFixture)-1))
	content := prefix + preplanCommandRecordFixture + "\n" + planModeLineFixture
	if !preplanHookWarns(t, content) {
		t.Error("no warning shown, want the plan-integrity warning: the marker ends past the scan bound")
	}
}

// TestStopPlanIntegrity_PreplanMarkerEndsAtScanBound_NoWarning pins that a
// marker which ends exactly at the scan bound matches.
func TestStopPlanIntegrity_PreplanMarkerEndsAtScanBound_NoWarning(t *testing.T) {
	const prefix = "pad\n"
	lowerTranscriptScanMax(t, int64(len(prefix)+len(preplanCommandRecordFixture)))
	content := prefix + preplanCommandRecordFixture + "\n" + planModeLineFixture
	if preplanHookWarns(t, content) {
		t.Error("warning shown, want none: the marker ends at the scan bound")
	}
}

// preplanBoundaryCases place the command record next to a read-buffer
// boundary. Each start function gets the boundary offset and the marker
// length, so no position is a literal. The "ends 1 byte after" case is also
// the case where the last byte of the marker is the first byte of the next
// read.
var preplanBoundaryCases = []struct {
	name  string
	start func(boundary, markerLen int) int
}{
	{"ends 1 byte before boundary", func(b, n int) int { return b - 1 - n }},
	{"ends at boundary", func(b, n int) int { return b - n }},
	{"ends 1 byte after boundary", func(b, n int) int { return b + 1 - n }},
	{"ends 2 bytes after boundary", func(b, n int) int { return b + 2 - n }},
	{"starts 1 byte before boundary", func(b, _ int) int { return b - 1 }},
	{"starts at boundary", func(b, _ int) int { return b }},
	{"starts 1 byte after boundary", func(b, _ int) int { return b + 1 }},
}

// TestStopPlanIntegrity_PreplanMarkerAtBufferBoundaries_NoWarning pins that a
// marker matches wherever it lies next to a read-buffer boundary. It catches
// an off-by-one in the length of the bytes kept between two reads.
func TestStopPlanIntegrity_PreplanMarkerAtBufferBoundaries_NoWarning(t *testing.T) {
	const size = 16
	lowerTranscriptScanBufSize(t, size)
	boundary := 4 * size
	for _, tc := range preplanBoundaryCases {
		t.Run(tc.name, func(t *testing.T) {
			padding := strings.Repeat("x", tc.start(boundary, len(preplanCommandRecordFixture)))
			content := padding + preplanCommandRecordFixture + "\n" + planModeLineFixture
			if preplanHookWarns(t, content) {
				t.Errorf("warning shown, want none: the marker is placed so that %s", tc.name)
			}
		})
	}
}

// TestStopPlanIntegrity_PreplanMarkerAtByteZero_NoWarning pins that a marker
// at byte 0 of the transcript matches.
func TestStopPlanIntegrity_PreplanMarkerAtByteZero_NoWarning(t *testing.T) {
	content := preplanCommandRecordFixture + "\n" + planModeLineFixture
	if preplanHookWarns(t, content) {
		t.Error("warning shown, want none: the marker is at byte 0")
	}
}

// TestStopPlanIntegrity_PreplanMarkerOnLastLineNoLineFeed_NoWarning pins that
// a marker on the last line matches when the file has no final line feed.
func TestStopPlanIntegrity_PreplanMarkerOnLastLineNoLineFeed_NoWarning(t *testing.T) {
	content := planModeLineFixture + preplanCommandRecordFixture
	if preplanHookWarns(t, content) {
		t.Error("warning shown, want none: the marker is on the last line")
	}
}

// TestTranscriptHasPreplan_EmptyFile pins that an empty transcript file has
// no preplan record.
func TestTranscriptHasPreplan_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	mustWriteFile(t, path, "")
	if transcriptHasPreplan(path, transcriptPreplanScanMax) {
		t.Error("transcriptHasPreplan = true, want false for an empty file")
	}
}

// TestTranscriptHasPreplan_MissingPath pins that a path that does not exist
// returns false. The open error is not an error value of the hook: the Stop
// hook exits 0 on every path.
func TestTranscriptHasPreplan_MissingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-transcript.jsonl")
	if transcriptHasPreplan(path, transcriptPreplanScanMax) {
		t.Error("transcriptHasPreplan = true, want false for a missing path")
	}
}

// TestTranscriptHasPreplan_FolderPath pins that a folder path returns false.
// The open of a folder works and the first read fails.
func TestTranscriptHasPreplan_FolderPath(t *testing.T) {
	if transcriptHasPreplan(t.TempDir(), transcriptPreplanScanMax) {
		t.Error("transcriptHasPreplan = true, want false for a folder path")
	}
}

// TestLongestPreplanMarker_IsCommandRecordLength pins the buffer overlap of
// the scan: the longest marker is the command record.
func TestLongestPreplanMarker_IsCommandRecordLength(t *testing.T) {
	if got, want := longestPreplanMarker(), len(preplanCommandRecordFixture); got != want {
		t.Errorf("longestPreplanMarker() = %d, want %d", got, want)
	}
}

// BenchmarkTranscriptHasPreplan scans a 64 MiB transcript with no marker, the
// worst case of the scan bound. It is a measure, not a gate.
func BenchmarkTranscriptHasPreplan(b *testing.B) {
	const total = 64 << 20
	line := strings.Repeat(`{"type":"assistant","message":"lorem ipsum dolor sit amet"}`+"\n", 1<<14)
	path := filepath.Join(b.TempDir(), "transcript.jsonl")
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	for written := 0; written < total; written += len(line) {
		if _, err := f.WriteString(line); err != nil {
			b.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}

	b.SetBytes(total)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if transcriptHasPreplan(path, transcriptPreplanScanMax) {
			b.Fatal("transcriptHasPreplan = true, want false: the file has no marker")
		}
	}
}

// ---------------------------------------------------------------------------
// stopPlanIntegrity: evidence directory survival (findPlanState uses
// state.LatestPlanRun; planIntegrityFromState never deletes a done run's
// state file or its state.EvidenceDir — see that function's doc comment)
// ---------------------------------------------------------------------------

// newPlanStateWithEvidence behaves like newPlanState but additionally
// creates the run's evidence directory (state.EvidenceDir, keyed off
// state.RunID(st)) with a sentinel file inside, so tests can assert whether
// stopPlanIntegrity left it alone.
func newPlanStateWithEvidence(t *testing.T, root, branch string, planIntegrity map[string]any) (st *state.State, evidenceDir string) {
	t.Helper()
	st = newPlanState(t, root, branch, planIntegrity, "")
	evidenceDir = state.EvidenceDir(root, state.RunID(st))
	mustMkdirAll(t, evidenceDir)
	mustWriteFile(t, filepath.Join(evidenceDir, "sentinel.txt"), "evidence")
	return st, evidenceDir
}

// TestStopPlanIntegrityKeepsDoneRun verifies a done plan run's state file and
// evidence directory both survive the Stop hook — planIntegrityFromState no
// longer deletes either; ship's cleanup-pipeline step or GC owns that later.
func TestStopPlanIntegrityKeepsDoneRun(t *testing.T) {
	root := gitFixture(t, "feat/plan-evidence-done")
	branch := "feat/plan-evidence-done"

	st, evidenceDir := newPlanStateWithEvidence(t, root, branch, allPlanMarkers())

	out, err := stopPlanIntegrity(HookCtx{}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)

	if _, statErr := os.Stat(st.Path); statErr != nil {
		t.Fatalf("plan state file should survive a done Stop, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(evidenceDir); statErr != nil {
		t.Fatalf("evidence dir should survive a done Stop alongside the state file, stat err = %v", statErr)
	}
}

func TestStopPlanIntegrity_NotDone_KeepsStateFileAndEvidenceDir(t *testing.T) {
	root := gitFixture(t, "feat/plan-evidence-notdone")
	branch := "feat/plan-evidence-notdone"

	markers := allPlanMarkers()
	delete(markers, "done")
	st, evidenceDir := newPlanStateWithEvidence(t, root, branch, markers)

	out, err := stopPlanIntegrity(HookCtx{}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)

	if _, statErr := os.Stat(st.Path); statErr != nil {
		t.Fatalf("plan state file should still exist (no %q marker — plan still running), stat err = %v", "done", statErr)
	}
	if _, statErr := os.Stat(evidenceDir); statErr != nil {
		t.Fatalf("evidence dir should still exist (no %q marker — plan still running), stat err = %v", "done", statErr)
	}
}

// TestStopPlanIntegrity_ExactSlugMatch_IgnoresSuperstringSlugRun proves
// findPlanState's switch from state.Find (prefix match — "plan-feat-" would
// also match a "plan-feat-x-..." filename) to state.LatestPlanRun (exact
// slug match). Branch "feat" has an older, done, exact-slug run; a
// "feat-x" run — a different branch's — is newer but must never be selected.
// Selection is asserted directly via findPlanState (rather than via
// deletion, which planIntegrityFromState no longer performs for a done run);
// both runs' files are then confirmed to survive the Stop hook untouched.
func TestStopPlanIntegrity_ExactSlugMatch_IgnoresSuperstringSlugRun(t *testing.T) {
	root := gitFixture(t, "feat")

	dir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	mustMkdirAll(t, dir)

	writeRun := func(name string) (path, evidenceDir string) {
		t.Helper()
		path = filepath.Join(dir, name)
		raw, err := json.Marshal(map[string]any{"planIntegrity": allPlanMarkers()})
		if err != nil {
			t.Fatal(err)
		}
		mustWriteFile(t, path, string(raw))
		runID := strings.TrimSuffix(name, ".json")
		evidenceDir = filepath.Join(dir, runID+".evidence")
		mustMkdirAll(t, evidenceDir)
		mustWriteFile(t, filepath.Join(evidenceDir, "sentinel.txt"), "evidence")
		return path, evidenceDir
	}

	// Exact match for branch "feat", older timestamp.
	featPath, featEvidence := writeRun("plan-feat-20260929T110000Z.json")
	// A different branch's run ("feat-x"), newer timestamp — state.Find's old
	// prefix-matching bug would have picked this one instead.
	featXPath, featXEvidence := writeRun("plan-feat-x-20260929T120000Z.json")

	if st := findPlanState("feat", ""); st == nil || st.Path != featPath {
		t.Fatalf("findPlanState(%q).Path = %+v, want %q", "feat", st, featPath)
	}

	out, err := stopPlanIntegrity(HookCtx{}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)

	for _, p := range []string{featPath, featEvidence, featXPath, featXEvidence} {
		if _, statErr := os.Stat(p); statErr != nil {
			t.Fatalf("%s must survive a done Stop untouched, stat err = %v", p, statErr)
		}
	}
}

// TestFindPlanState_CorruptStateFile_ReturnsNil proves a corrupt plan state
// file (state.LatestPlanRun's LoadRun call fails to decode it) still
// degrades to findPlanState returning nil, never propagating an error to
// stopPlanIntegrity's caller.
func TestFindPlanState_CorruptStateFile_ReturnsNil(t *testing.T) {
	root := gitFixture(t, "feat/plan-corrupt")
	branch := "feat/plan-corrupt"

	dir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	mustMkdirAll(t, dir)
	mustWriteFile(t, filepath.Join(dir, "plan-feat-plan-corrupt-20260929T110000Z.json"), "{not json")

	if st := findPlanState(branch, ""); st != nil {
		t.Fatalf("findPlanState = %+v, want nil for a corrupt state file", st)
	}

	out, err := stopPlanIntegrity(HookCtx{}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}

// TestFindPlanState_BySession pins the run choice of findPlanState: the run
// of the session wins over a newer run of another session, and every other
// case degrades to the newest run or to nil.
func TestFindPlanState_BySession(t *testing.T) {
	branch := "feat/plan-by-session"
	slug := state.SlugifyBranch(branch)
	olderID := "plan-" + slug + "-20261026T100000Z"
	newerID := "plan-" + slug + "-20261026T110000Z"

	t.Run("the run of this session wins over a newer run", func(t *testing.T) {
		root := gitFixture(t, branch)
		olderPath := writePlanRun(t, root, olderID, map[string]any{"sessionId": "s-1", "planIntegrity": allPlanMarkers()})
		writePlanRun(t, root, newerID, map[string]any{"sessionId": "s-2", "planIntegrity": allPlanMarkers()})

		if st := findPlanState(branch, "s-1"); st == nil || st.Path != olderPath {
			t.Fatalf("findPlanState(%q, %q) = %+v, want path %q", branch, "s-1", st, olderPath)
		}
	})

	t.Run("no run of this session: the newest run wins", func(t *testing.T) {
		root := gitFixture(t, branch)
		writePlanRun(t, root, olderID, map[string]any{"sessionId": "s-1", "planIntegrity": allPlanMarkers()})
		newerPath := writePlanRun(t, root, newerID, map[string]any{"sessionId": "s-2", "planIntegrity": allPlanMarkers()})

		if st := findPlanState(branch, "s-3"); st == nil || st.Path != newerPath {
			t.Fatalf("findPlanState(%q, %q) = %+v, want path %q", branch, "s-3", st, newerPath)
		}
	})

	t.Run("empty session ID: the newest run wins", func(t *testing.T) {
		root := gitFixture(t, branch)
		writePlanRun(t, root, olderID, map[string]any{"sessionId": "s-1", "planIntegrity": allPlanMarkers()})
		newerPath := writePlanRun(t, root, newerID, map[string]any{"sessionId": "s-2", "planIntegrity": allPlanMarkers()})

		if st := findPlanState(branch, ""); st == nil || st.Path != newerPath {
			t.Fatalf("findPlanState(%q, %q) = %+v, want path %q", branch, "", st, newerPath)
		}
	})

	t.Run("runs path is a regular file: nil", func(t *testing.T) {
		root := gitFixture(t, branch)
		mustMkdirAll(t, filepath.Join(root, paths.DataDir))
		mustWriteFile(t, filepath.Join(root, paths.DataDir, paths.RunsSubdir), "not a folder")

		if st := findPlanState(branch, "s-1"); st != nil {
			t.Fatalf("findPlanState = %+v, want nil", st)
		}
	})
}

// TestStopPlanIntegrity_ChecksRunOfSession proves the stop-plan-integrity
// hook passes the session_id of its stdin envelope to findPlanState: the
// same two runs give a warning for the session of the run with a missing
// checkpoint and silence for the other session. It goes through Run with a
// real JSON envelope.
func TestStopPlanIntegrity_ChecksRunOfSession(t *testing.T) {
	branch := "feat/plan-stop-session"
	slug := state.SlugifyBranch(branch)

	root := gitFixture(t, branch)
	planFile := filepath.Join(root, "plans", "my-plan.md")
	mustMkdirAll(t, filepath.Join(root, "plans"))
	mustWriteFile(t, planFile, "# Plan\n\nSome content.\n")

	incomplete := allPlanMarkers()
	delete(incomplete, "critiqueRan")
	writePlanRun(t, root, "plan-"+slug+"-20261026T100000Z", map[string]any{
		"sessionId":     "s-1",
		"planFilePath":  planFile,
		"planIntegrity": incomplete,
	})
	writePlanRun(t, root, "plan-"+slug+"-20261026T110000Z", map[string]any{
		"sessionId":     "s-2",
		"planFilePath":  planFile,
		"planIntegrity": allPlanMarkers(),
	})

	for _, tc := range []struct {
		sessionID   string
		wantWarning bool
	}{
		{sessionID: "s-1", wantWarning: true},
		{sessionID: "s-2", wantWarning: false},
	} {
		t.Run("session "+tc.sessionID, func(t *testing.T) {
			stdin := `{"hook_event_name":"Stop","session_id":"` + tc.sessionID + `"}`
			var out bytes.Buffer
			stderrText := captureStderr(t, func() {
				if code := Run("stop-plan-integrity", strings.NewReader(stdin), &out); code != 0 {
					t.Errorf("exit code = %d, want 0", code)
				}
			})
			if out.Len() != 0 {
				t.Errorf("stdout = %q, want empty", out.String())
			}
			if got := strings.Contains(stderrText, "Missing checkpoints: critiqueRan"); got != tc.wantWarning {
				t.Errorf("warning for critiqueRan = %v, want %v; stderr = %q", got, tc.wantWarning, stderrText)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// stopPipelineContinue
// ---------------------------------------------------------------------------

type blockCounterFixture struct {
	StepName string `json:"stepName"`
	Count    int    `json:"count"`
}

// readBlockCounter reads the on-disk block-count sidecar directly, using the
// documented {stepName, count} JSON shape (internal/state/sidecar.go) —
// state.StepBlockCount can't be used for a read-only peek since calling it
// mutates the counter.
func readBlockCounter(t *testing.T, root, branch string) (blockCounterFixture, bool) {
	t.Helper()
	path := filepath.Join(root, paths.DataDir, paths.RunsSubdir, ".stop-block-count-"+state.SlugifyBranch(branch)+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return blockCounterFixture{}, false
		}
		t.Fatalf("read block-count sidecar: %v", err)
	}
	var bc blockCounterFixture
	if err := json.Unmarshal(raw, &bc); err != nil {
		t.Fatalf("unmarshal block-count sidecar: %v", err)
	}
	return bc, true
}

func TestStopPipelineContinue_StopHookActiveTrue_ShortCircuits(t *testing.T) {
	root := gitFixture(t, "feat/spc-active")
	branch := "feat/spc-active"
	newShipState(t, root, branch, "s1", []any{
		map[string]any{"name": "review", "status": "in_progress"},
	}, map[string]any{"auto": true})

	out, err := stopPipelineContinue(HookCtx{SessionID: "s1"}, Event{Raw: map[string]any{"stop_hook_active": true}})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}

func TestStopPipelineContinue_StopHookActiveNonBoolTruthy_DoesNotShortCircuit(t *testing.T) {
	root := gitFixture(t, "feat/spc-active-string")
	branch := "feat/spc-active-string"
	newShipState(t, root, branch, "s1", []any{
		map[string]any{"name": "review", "status": "in_progress"},
	}, map[string]any{"auto": true})

	out, err := stopPipelineContinue(HookCtx{SessionID: "s1"}, Event{Raw: map[string]any{"stop_hook_active": "true"}})
	mustBlock(t, out, err, "step 1 of 1 (review) is in_progress")
}

func TestStopPipelineContinue_StrictAutoCheck(t *testing.T) {
	pendingSteps := func() []any {
		return []any{
			map[string]any{"name": "review", "status": "completed"},
			map[string]any{"name": "pr", "status": "pending"},
		}
	}
	inProgressSteps := func() []any {
		return []any{
			map[string]any{"name": "review", "status": "in_progress"},
		}
	}

	t.Run("pending blocks when auto strictly true", func(t *testing.T) {
		root := gitFixture(t, "feat/spc-auto-true")
		newShipState(t, root, "feat/spc-auto-true", "s1", pendingSteps(), map[string]any{"auto": true})
		out, err := stopPipelineContinue(HookCtx{SessionID: "s1"}, Event{})
		mustBlock(t, out, err, "step 2 of 2 (pr) is pending")
	})

	t.Run("pending stays silent on truthy non-boolean auto (strict check)", func(t *testing.T) {
		root := gitFixture(t, "feat/spc-auto-truthy")
		newShipState(t, root, "feat/spc-auto-truthy", "s1", pendingSteps(), map[string]any{"auto": "yes"})
		out, err := stopPipelineContinue(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("pending stays silent when auto false", func(t *testing.T) {
		root := gitFixture(t, "feat/spc-auto-false")
		newShipState(t, root, "feat/spc-auto-false", "s1", pendingSteps(), map[string]any{"auto": false})
		out, err := stopPipelineContinue(HookCtx{SessionID: "s1"}, Event{})
		if err != nil {
			t.Fatal(err)
		}
		assertSilent(t, out)
	})

	t.Run("in_progress blocks even when auto is false (mode-independent)", func(t *testing.T) {
		root := gitFixture(t, "feat/spc-inprog-false")
		newShipState(t, root, "feat/spc-inprog-false", "s1", inProgressSteps(), map[string]any{"auto": false})
		out, err := stopPipelineContinue(HookCtx{SessionID: "s1"}, Event{})
		mustBlock(t, out, err, "step 1 of 1 (review) is in_progress")
	})
}

func TestStopPipelineContinue_SessionMismatch_Silent(t *testing.T) {
	root := gitFixture(t, "feat/spc-mismatch")
	newShipState(t, root, "feat/spc-mismatch", "state-session", []any{
		map[string]any{"name": "review", "status": "in_progress"},
	}, map[string]any{"auto": true})
	out, err := stopPipelineContinue(HookCtx{SessionID: "different"}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}

func TestStopPipelineContinue_NotAdvancing_Silent(t *testing.T) {
	root := gitFixture(t, "feat/spc-not-advancing")
	newShipState(t, root, "feat/spc-not-advancing", "s1", []any{
		map[string]any{"name": "review", "status": "completed"},
	}, map[string]any{"auto": true})
	out, err := stopPipelineContinue(HookCtx{SessionID: "s1"}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}

func TestStopPipelineContinue_BlockCountCapThenMarksFailedOnce(t *testing.T) {
	root := gitFixture(t, "feat/spc-cap")
	branch := "feat/spc-cap"
	steps := []any{
		map[string]any{"name": "review", "status": "in_progress"},
		map[string]any{"name": "pr", "status": "pending"},
	}
	newShipState(t, root, branch, "s1", steps, map[string]any{"auto": true})

	// Calls 1-3: consecutive blocks on the same unchanged step. The sidecar
	// count increments 1 -> 2 -> 3, and the step is never touched.
	for i := 1; i <= 3; i++ {
		out, err := stopPipelineContinue(HookCtx{SessionID: "s1"}, Event{})
		mustBlock(t, out, err, "step 1 of 2 (review) is in_progress")

		bc, ok := readBlockCounter(t, root, branch)
		if !ok {
			t.Fatalf("call %d: block-count sidecar missing", i)
		}
		if bc.StepName != "review" || bc.Count != i {
			t.Fatalf("call %d: sidecar = %+v, want stepName=review count=%d", i, bc, i)
		}

		st, err := state.Find(root, "ship", branch)
		if err != nil || st == nil {
			t.Fatalf("call %d: state.Find: %v", i, err)
		}
		firstStep := st.Data["steps"].([]any)[0].(map[string]any)
		if status, _ := firstStep["status"].(string); status != "in_progress" {
			t.Fatalf("call %d: step status = %q, want still in_progress (not yet capped)", i, status)
		}
	}

	// Call 4: cap already reached (3) — does NOT block, marks the step
	// failed instead, and deletes the sidecar.
	out, err := stopPipelineContinue(HookCtx{SessionID: "s1"}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)

	if _, ok := readBlockCounter(t, root, branch); ok {
		t.Fatal("call 4: block-count sidecar should have been deleted on cap exhaustion")
	}

	st, err := state.Find(root, "ship", branch)
	if err != nil || st == nil {
		t.Fatalf("state.Find after cap: %v", err)
	}
	firstStep := st.Data["steps"].([]any)[0].(map[string]any)
	if status, _ := firstStep["status"].(string); status != "failed" {
		t.Fatalf("step status = %q, want failed", status)
	}
	if reason, _ := firstStep["failedReason"].(string); reason != "block-cap-exhausted" {
		t.Fatalf("failedReason = %q, want block-cap-exhausted", reason)
	}

	// Call 5: the failed step blocks further advancing ("pr" isn't literally
	// named "cleanup"), so this call is silent and leaves the step untouched
	// — the mark-failed only ever happens once.
	out5, err5 := stopPipelineContinue(HookCtx{SessionID: "s1"}, Event{})
	if err5 != nil {
		t.Fatal(err5)
	}
	assertSilent(t, out5)

	st2, err := state.Find(root, "ship", branch)
	if err != nil || st2 == nil {
		t.Fatalf("state.Find after call 5: %v", err)
	}
	firstStep2 := st2.Data["steps"].([]any)[0].(map[string]any)
	if reason, _ := firstStep2["failedReason"].(string); reason != "block-cap-exhausted" {
		t.Fatalf("failedReason after call 5 = %q, want unchanged block-cap-exhausted", reason)
	}
}

func TestStopPipelineContinue_BlockCountResetsOnStepChange(t *testing.T) {
	root := gitFixture(t, "feat/spc-reset")
	branch := "feat/spc-reset"

	newShipState(t, root, branch, "s1", []any{
		map[string]any{"name": "review", "status": "in_progress"},
		map[string]any{"name": "pr", "status": "pending"},
	}, map[string]any{"auto": true})

	for i := 1; i <= 2; i++ {
		out, err := stopPipelineContinue(HookCtx{SessionID: "s1"}, Event{})
		mustBlock(t, out, err, "step 1 of 2 (review) is in_progress")
	}
	bc, ok := readBlockCounter(t, root, branch)
	if !ok || bc.StepName != "review" || bc.Count != 2 {
		t.Fatalf("sidecar after 2 review blocks = (%+v, ok=%v), want review/2", bc, ok)
	}

	// Advance the pipeline: "review" completes, "pr" becomes in_progress — a
	// step-name change must reset the counter to a fresh budget, not inherit
	// review's count toward the cap.
	st, err := state.Find(root, "ship", branch)
	if err != nil || st == nil {
		t.Fatalf("state.Find: %v", err)
	}
	steps := st.Data["steps"].([]any)
	steps[0].(map[string]any)["status"] = "completed"
	steps[1].(map[string]any)["status"] = "in_progress"
	if err := state.Write(st); err != nil {
		t.Fatal(err)
	}

	out, err := stopPipelineContinue(HookCtx{SessionID: "s1"}, Event{})
	mustBlock(t, out, err, "step 2 of 2 (pr) is in_progress")

	bc2, ok2 := readBlockCounter(t, root, branch)
	if !ok2 || bc2.StepName != "pr" || bc2.Count != 1 {
		t.Fatalf("sidecar after step change = (%+v, ok=%v), want pr/1 (fresh budget)", bc2, ok2)
	}
}
