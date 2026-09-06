package hooks

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// stop-state-save
// ---------------------------------------------------------------------------

// stopStateSave is the "stop-state-save" hook handler (Stop event), ported
// from hooks/stop-state-save.js. It shares its entire implementation with
// preCompactSave (pre_compact_save.go) via saveCompactRecovery — see that
// function's doc comment for the full behavior.
func stopStateSave(ctx HookCtx, _ Event) (Output, error) {
	return saveCompactRecovery(ctx, "stop-state-save")
}

// ---------------------------------------------------------------------------
// stop-plan-integrity
// ---------------------------------------------------------------------------

// requiredPlanMarkers is REQUIRED_MARKERS from stop-plan-integrity.js, in
// the exact order the source checks and reports them.
var requiredPlanMarkers = []string{"skillInvoked", "planFile", "guardrailsEvaluated", "critiqueRan"}

// planMarkerDescriptions is MARKER_DESCRIPTIONS from stop-plan-integrity.js.
var planMarkerDescriptions = map[string]string{
	"skillInvoked":        "plan-sdlc Step 0 (prepare) did not run",
	"planFile":            "plan file was not written or is empty",
	"guardrailsEvaluated": "Step 3 guardrail-compliance gate did not run",
	"critiqueRan":         "Step 3 self-critique did not run",
}

// transcriptTailLimit bounds how much of the transcript file's tail is
// scanned for the "Plan mode is active" marker (source: 64 KiB).
const transcriptTailLimit = 64 * 1024

// stopPlanIntegrity is the "stop-plan-integrity" hook handler (Stop event),
// ported from hooks/stop-plan-integrity.js. It never blocks — every path
// returns Output{ExitCode: 0}; its only effect is an advisory stderr
// warning when plan-sdlc's checkpoints look incomplete or absent.
//
// Branch resolution follows the same convention as the other three hooks
// and session_start.go (gitx.CurrentBranch(resolveActiveWorktreeSafe())): a
// resolution failure exits silently without attempting the transcript
// fallback below. This is distinct from — and evaluated before — the
// state-file lookup, whose own failure (no repo root, no matching state, or
// an unparseable state file) DOES fall through to the transcript fallback,
// using the already-resolved branch name in that warning's text.
func stopPlanIntegrity(ctx HookCtx, event Event) (Output, error) {
	silent := Output{ExitCode: 0}

	branch, err := gitx.CurrentBranch(resolveActiveWorktreeSafe())
	if err != nil || branch == "" || branch == "HEAD" {
		return silent, nil
	}

	if st := findPlanState(branch); st != nil {
		return planIntegrityFromState(st), nil
	}

	return planIntegrityFromTranscript(event, branch), nil
}

// findPlanState resolves the repo root and looks up the "plan" state file
// for branch. Any failure (no root, e.g. outside a git repo — mirroring the
// JS source's own try/catch around resolveMainWorktree() — or no matching
// state, or an unparseable state file per Ruling 4) degrades uniformly to
// "no state found" (nil), never propagating an error to the caller.
func findPlanState(branch string) *state.State {
	root, err := worktree.MainRoot()
	if err != nil {
		return nil
	}
	st, err := state.Find(root, "plan", branch)
	if err != nil || st == nil {
		return nil
	}
	return st
}

// planIntegrityFromState implements stop-plan-integrity.js's found-a-state
// branch: the plan marker state file is ALWAYS deleted (single-use,
// regardless of outcome), then REQUIRED_MARKERS are checked against
// data.planIntegrity (each must be present and string-valued), and
// data.planFilePath — if set — must stat to a non-empty file. Any missing
// or failing marker produces one aggregated warning to stderr; the hook
// itself never blocks.
func planIntegrityFromState(st *state.State) Output {
	silent := Output{ExitCode: 0}
	defer func() { _ = os.Remove(st.Path) }()

	if st.Data == nil {
		return silent
	}

	pi, _ := st.Data["planIntegrity"].(map[string]any)

	var missing []string
	for _, marker := range requiredPlanMarkers {
		v, ok := pi[marker]
		if !ok {
			missing = append(missing, marker)
			continue
		}
		if _, isString := v.(string); !isString {
			missing = append(missing, marker)
		}
	}

	planFilePath, _ := st.Data["planFilePath"].(string)
	if planFilePath != "" {
		info, statErr := os.Stat(planFilePath)
		if (statErr != nil || info.Size() == 0) && !containsString(missing, "planFile") {
			missing = append(missing, "planFile")
		}
	}

	if len(missing) == 0 {
		return silent
	}

	fmt.Fprint(os.Stderr, planIntegrityWarningText(missing, planFilePath))
	return silent
}

// planIntegrityWarningText renders stop-plan-integrity.js's aggregated
// warning verbatim: a header line, a "Missing checkpoints: a, b, ..." line,
// then one "- marker: description" line per missing marker in
// requiredPlanMarkers order. The planFile description is overridden to
// include the actual (non-empty, unwritten-or-empty) path when known.
func planIntegrityWarningText(missing []string, planFilePath string) string {
	var b strings.Builder
	b.WriteString("[plan-integrity] WARNING: Plan presented with incomplete plan-sdlc execution.\n")
	b.WriteString("  Missing checkpoints: " + strings.Join(missing, ", ") + "\n")
	for _, marker := range missing {
		desc := planMarkerDescriptions[marker]
		if marker == "planFile" && planFilePath != "" {
			desc = fmt.Sprintf("plan file was not written or is empty (planFilePath=%s)", planFilePath)
		}
		b.WriteString(fmt.Sprintf("  - %s: %s\n", marker, desc))
	}
	return b.String()
}

// containsString reports whether s is present in list.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// planIntegrityFromTranscript implements stop-plan-integrity.js's
// no-state-file fallback: it reads up to the last transcriptTailLimit bytes
// of the transcript file named by event.Raw["transcript_path"] and, if that
// text contains "Plan mode is active", warns to stderr that a plan was
// presented without any plan-sdlc integrity state for branch. Any missing
// transcript_path, or any I/O error opening/reading it, is a silent no-op.
func planIntegrityFromTranscript(event Event, branch string) Output {
	silent := Output{ExitCode: 0}

	transcriptPath, _ := event.Raw["transcript_path"].(string)
	if transcriptPath == "" {
		return silent
	}

	tail, ok := readFileTail(transcriptPath, transcriptTailLimit)
	if !ok {
		return silent
	}

	if strings.Contains(string(tail), "Plan mode is active") {
		fmt.Fprintf(os.Stderr, "[plan-integrity] WARNING: Plan presented but plan-sdlc was not invoked (no plan integrity state for branch %s). Quality gates may have been bypassed.\n", branch)
	}
	return silent
}

// readFileTail returns up to the last limit bytes of the file at path.
// Returns ok=false on any I/O error (open, stat, seek, or read).
func readFileTail(path string, limit int64) (tail []byte, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, false
	}

	offset := info.Size() - limit
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, false
	}

	buf, err := io.ReadAll(f)
	if err != nil {
		return nil, false
	}
	return buf, true
}

// ---------------------------------------------------------------------------
// stop-pipeline-continue
// ---------------------------------------------------------------------------

// stopPipelineContinue is the "stop-pipeline-continue" hook handler (Stop
// event), ported from hooks/stop-pipeline-continue.js. When the ship
// pipeline is advancing on an unfinished step this session owns, it blocks
// the Stop (flat {"decision":"block","reason":...} JSON) so the session
// keeps going — UNLESS the consecutive-block cap (state.StepBlockCount) has
// just been exhausted for that step, in which case it marks the step
// failed instead of blocking a 4th time.
//
// Steps 2/3/5 of the source (find the advancing ship state, then check
// session ownership via state.HookEnforcementAllowed) are delegated to
// gatedAdvancingShipState — the exact same helper block-askuserquestion-auto
// and pipeline-continue already use. This means step 4 (the strict
// auto/status check below) runs AFTER the enforcement check rather than
// before it, as the JS source does. This reordering is a disclosed,
// deliberate deviation: every combination of advancing/auto/status/
// enforcement still produces the identical final Output — the only
// possible difference is that the "not enforcing" stderr diagnostic can
// fire in one edge case (a pending, non-auto step with a denied gate) that
// the source would have short-circuited past silently first. That
// diagnostic isn't observable through this hook's Output/JSON contract.
func stopPipelineContinue(ctx HookCtx, event Event) (Output, error) {
	silent := Output{ExitCode: 0}

	// Step 1: strict stop_hook_active === true early-exit, checked directly
	// against the raw stdin envelope before any git/state I/O — this is not
	// a HookCtx field.
	if active, ok := event.Raw["stop_hook_active"].(bool); ok && active {
		return silent, nil
	}

	data, adv, ok := gatedAdvancingShipState("stop-pipeline-continue", ctx.SessionID)
	if !ok {
		return silent, nil
	}

	// Step 4: STRICT auto check (flags.auto === true) — never the loose
	// jsTruthyAutoFlag pipeline-continue.go uses for its nudge message. An
	// in_progress step always blocks; a pending step blocks only when auto
	// is strictly boolean true.
	flags, _ := data["flags"].(map[string]any)
	autoVal, isBool := flags["auto"].(bool)
	auto := isBool && autoVal

	status, _ := adv.Step["status"].(string)
	if status != "in_progress" && !auto {
		return silent, nil
	}

	stepName := stepDisplayName(adv.Step)

	// Step 6: consecutive-block cap. Needs root/branch again — not returned
	// by gatedAdvancingShipState — resolved fresh here, only on this path.
	root, err := worktree.MainRoot()
	if err != nil {
		return silent, nil
	}
	branch, err := gitx.CurrentBranch(resolveActiveWorktreeSafe())
	if err != nil || branch == "" || branch == "HEAD" {
		return silent, nil
	}
	slug := state.SlugifyBranch(branch)

	count, capped, err := state.StepBlockCount(root, slug, stepName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stop-pipeline-continue: block-count error — %v\n", err)
		return silent, nil
	}

	if capped {
		markStepFailedOnce(root, branch, stepName)
		fmt.Fprintf(os.Stderr, "stop-pipeline-continue: block cap (%d) exhausted for step %q — marking failed instead of blocking again\n", count, stepName)
		return silent, nil
	}

	total := 0
	if steps, isArr := data["steps"].([]any); isArr {
		total = len(steps)
	}
	statusWord := "is pending"
	if status == "in_progress" {
		statusWord = "is in_progress"
	}

	reason := fmt.Sprintf(
		"Ship pipeline step %d of %d (%s) %s and has not been completed. Record the step result and continue to the next pipeline step.",
		adv.Index+1, total, stepName, statusWord,
	)

	return Output{
		JSON: map[string]any{
			"decision": "block",
			"reason":   reason,
		},
		ExitCode: 0,
	}, nil
}

// markStepFailedOnce re-fetches a fresh "ship" state for branch, finds the
// step whose display name (stepDisplayName: name || id || "unknown")
// matches stepName, and — unless it is already "completed" — marks it
// status "failed" with failedReason "block-cap-exhausted", then writes the
// state back. Best-effort: any resolution failure or missing step is a
// silent no-op, matching this hook family's fail-open contract.
func markStepFailedOnce(root, branch, stepName string) {
	st, err := state.Find(root, "ship", branch)
	if err != nil || st == nil {
		return
	}
	steps, ok := st.Data["steps"].([]any)
	if !ok {
		return
	}
	for _, raw := range steps {
		s, ok := raw.(map[string]any)
		if !ok || stepDisplayName(s) != stepName {
			continue
		}
		if status, _ := s["status"].(string); status == "completed" {
			return
		}
		s["status"] = "failed"
		s["failedReason"] = "block-cap-exhausted"
		_ = state.Write(st)
		return
	}
}
