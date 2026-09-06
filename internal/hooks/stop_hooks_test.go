package hooks

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if recovery["pipeline"] != "ship-sdlc" {
		t.Errorf("pipeline = %v, want ship-sdlc", recovery["pipeline"])
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
	if recovery["pipeline"] != "execute-plan-sdlc" {
		t.Errorf("pipeline = %v, want execute-plan-sdlc", recovery["pipeline"])
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

func allPlanMarkers() map[string]any {
	return map[string]any{
		"skillInvoked":        "2024-01-01T00:00:00Z",
		"planFile":            "2024-01-01T00:00:01Z",
		"guardrailsEvaluated": "2024-01-01T00:00:02Z",
		"critiqueRan":         "2024-01-01T00:00:03Z",
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

func TestStopPlanIntegrity_AllMarkersPresent_ConsumesDeletesSilently(t *testing.T) {
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

	if _, statErr := os.Stat(st.Path); !os.IsNotExist(statErr) {
		t.Fatalf("plan state file should have been deleted, stat err = %v", statErr)
	}

	// Second consecutive invocation: state is gone, falls through to the
	// transcript-fallback path; no transcript_path in this event, so silent.
	out2, err2 := stopPlanIntegrity(HookCtx{}, Event{})
	if err2 != nil {
		t.Fatal(err2)
	}
	assertSilent(t, out2)
}

func TestStopPlanIntegrity_MissingMarker_WarnsDeletesNeverBlocks(t *testing.T) {
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

	if _, statErr := os.Stat(st.Path); !os.IsNotExist(statErr) {
		t.Fatalf("plan state file should have been deleted even on failure, stat err = %v", statErr)
	}
	if !strings.Contains(stderrText, "Missing checkpoints: critiqueRan") {
		t.Errorf("stderr = %q, want it to list critiqueRan as missing", stderrText)
	}
	if !strings.Contains(stderrText, "critiqueRan: Step 3 self-critique did not run") {
		t.Errorf("stderr = %q, want the critiqueRan description line", stderrText)
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

	if _, statErr := os.Stat(st.Path); !os.IsNotExist(statErr) {
		t.Fatalf("plan state file should have been deleted, stat err = %v", statErr)
	}
	if !strings.Contains(stderrText, "Missing checkpoints: planFile") {
		t.Errorf("stderr = %q, want planFile flagged despite the marker string being present", stderrText)
	}
	if !strings.Contains(stderrText, "planFilePath="+planFile) {
		t.Errorf("stderr = %q, want the planFilePath-annotated description", stderrText)
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
	path := filepath.Join(root, ".sdlc", "execution", ".stop-block-count-"+state.SlugifyBranch(branch)+".json")
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
