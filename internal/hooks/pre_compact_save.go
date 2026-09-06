package hooks

import (
	"fmt"
	"os"

	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// savedAtFormat renders a time the way JS's Date#toISOString() would:
// millisecond precision (e.g. "2024-01-01T00:00:00.000Z"). Go's
// time.RFC3339Nano would render nanosecond precision instead, so a literal
// port needs this explicit format string.
const savedAtFormat = "2006-01-02T15:04:05.000Z"

// preCompactSave is the "pre-compact-save" hook handler (PreCompact,
// matcher manual|auto), ported from hooks/pre-compact-save.js. event isn't
// consulted: this hook's entire behavior derives from the current branch's
// own state, not from anything in the PreCompact stdin envelope.
//
// preCompactSave and stopStateSave (stop_hooks.go) are ~99% identical —
// same recovery-object construction, same session-ownership gate, same
// sidecar write target — just triggered by different Claude Code lifecycle
// events. Both call the shared saveCompactRecovery core below rather than
// duplicating its ~110 lines.
func preCompactSave(ctx HookCtx, _ Event) (Output, error) {
	return saveCompactRecovery(ctx, "pre-compact-save")
}

// saveCompactRecovery is the shared core for preCompactSave and
// stopStateSave. It resolves the current branch's ship state (priority) or
// execute state (fallback), builds a recovery object mirroring the JS
// source's exact field derivation, gates the write on session ownership
// (state.HookEnforcementAllowed), and writes it to the per-branch
// compact-recovery sidecar (state.WriteRecoverySidecar).
//
// Every path — no root, no branch, no state file found, gate denied, write
// failure — degrades silently to Output{ExitCode: 0}: this hook family is
// advisory-only and must never block or crash a session. A denied gate or a
// write failure logs a diagnostic to os.Stderr only (no injectable stderr
// writer exists on the Handler contract, matching the same accepted pattern
// gatedAdvancingShipState uses for its own "not enforcing" diagnostic).
// hookName is used only to prefix that diagnostic ("pre-compact-save: ..."
// vs. "stop-state-save: ...").
func saveCompactRecovery(ctx HookCtx, hookName string) (Output, error) {
	silent := Output{ExitCode: 0}

	root, err := worktree.MainRoot()
	if err != nil {
		return silent, nil
	}
	branch, err := gitx.CurrentBranch(resolveActiveWorktreeSafe())
	if err != nil || branch == "" || branch == "HEAD" {
		return silent, nil
	}

	var (
		sourceData map[string]any
		sourcePath string
		recovery   map[string]any
	)

	if st, findErr := state.Find(root, "ship", branch); findErr == nil && st != nil {
		sourceData, sourcePath = st.Data, st.Path
		recovery = buildShipRecovery(st.Data, branch)
	} else if est, findErr := state.Find(root, "execute", branch); findErr == nil && est != nil {
		sourceData, sourcePath = est.Data, est.Path
		recovery = buildExecuteRecovery(est.Data, branch)
	} else {
		// Neither ship nor execute state exists (or both failed to
		// parse, treated the same as absent): nothing to save. This is
		// stop-state-save.js's explicit upfront fast-bail (source lines
		// 50-52) and pre-compact-save.js's natural fallthrough — same net
		// effect for both callers.
		return silent, nil
	}

	enforcement := state.HookEnforcementAllowed(sourceData, ctx.SessionID)
	if !enforcement.Allowed {
		fmt.Fprintf(os.Stderr, "%s: not writing — %s\n", hookName, enforcement.Reason)
		return silent, nil
	}

	// savedAt is sourced from the winning state file's own mtime, never
	// time.Now(). If the just-read file can't be stat'd a moment later,
	// there is nothing trustworthy to save — bail silently rather than
	// substitute a fabricated timestamp.
	info, statErr := os.Stat(sourcePath)
	if statErr != nil {
		return silent, nil
	}
	recovery["savedAt"] = info.ModTime().UTC().Format(savedAtFormat)

	slug := state.SlugifyBranch(branch)
	if writeErr := state.WriteRecoverySidecar(root, slug, recovery); writeErr != nil {
		fmt.Fprintf(os.Stderr, "%s: recovery sidecar write failed: %v\n", hookName, writeErr)
	}

	return silent, nil
}

// buildShipRecovery mirrors pre-compact-save.js/stop-state-save.js's
// ship-state recovery-object derivation. currentStep is the in_progress
// step's name/id (or, if none is in_progress, the LAST completed step's —
// the source reverses the steps array to find the first match from the
// end); reviewVerdict/deferredFindings come from whichever step's output
// carries them, with the last truthy value across the full forward scan
// winning. flags.auto/preset/skip are each a value-preserving OR (any-typed
// fields), not boolean coercions — a truthy non-boolean flags.auto value
// carries through verbatim rather than normalizing to true/false.
func buildShipRecovery(data map[string]any, branch string) map[string]any {
	var currentStep, reviewVerdict, deferredFindings any

	if steps, ok := data["steps"].([]any); ok {
		var inProgress, lastCompleted map[string]any
		for _, raw := range steps {
			s, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			status, _ := s["status"].(string)
			if status == "in_progress" && inProgress == nil {
				inProgress = s
			}
			if status == "completed" {
				lastCompleted = s // overwritten on every match => last one wins
			}
		}
		step := inProgress
		if step == nil {
			step = lastCompleted
		}
		if step != nil {
			currentStep = stepNameOrNil(step)
		}

		for _, raw := range steps {
			s, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			output, ok := s["output"].(map[string]any)
			if !ok {
				continue
			}
			if df := output["deferredFindings"]; jsTruthyAutoFlag(df) {
				deferredFindings = df
			}
			if v := output["verdict"]; jsTruthyAutoFlag(v) {
				reviewVerdict = v
			}
		}
	}

	flags, _ := data["flags"].(map[string]any)
	var preset any
	var auto any = false
	var skip any = []any{}
	if p := flags["preset"]; jsTruthyAutoFlag(p) {
		preset = p
	}
	if a := flags["auto"]; jsTruthyAutoFlag(a) {
		auto = a
	}
	if sk := flags["skip"]; jsTruthyAutoFlag(sk) {
		skip = sk
	}

	return map[string]any{
		"pipeline":         "ship-sdlc",
		"branch":           stringOrFallback(data["branch"], branch),
		"currentStep":      currentStep,
		"reviewVerdict":    reviewVerdict,
		"deferredFindings": deferredFindings,
		"flags": map[string]any{
			"preset": preset,
			"auto":   auto,
			"skip":   skip,
		},
	}
}

// buildExecuteRecovery mirrors pre-compact-save.js/stop-state-save.js's
// execute-state fallback recovery-object derivation.
func buildExecuteRecovery(data map[string]any, branch string) map[string]any {
	var totalWaves, completedWaves int
	if waves, ok := data["waves"].([]any); ok {
		totalWaves = len(waves)
		for _, raw := range waves {
			w, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if status, _ := w["status"].(string); status == "completed" {
				completedWaves++
			}
		}
	}

	var preset any
	if p := data["preset"]; jsTruthyAutoFlag(p) {
		preset = p
	}

	return map[string]any{
		"pipeline":       "execute-plan-sdlc",
		"branch":         stringOrFallback(data["branch"], branch),
		"completedWaves": completedWaves,
		"totalWaves":     totalWaves,
		"preset":         preset,
	}
}

// stepNameOrNil mirrors the source's `step.name || step.id || null`. This is
// a different fallback chain than pipeline_continue.go's stepDisplayName
// (which falls back to the literal string "unknown" for a human-readable
// nudge message) — here the JS source really does want null, not a display
// placeholder, when neither field is present.
func stepNameOrNil(step map[string]any) any {
	if name, ok := step["name"].(string); ok && name != "" {
		return name
	}
	if id, ok := step["id"].(string); ok && id != "" {
		return id
	}
	return nil
}

// stringOrFallback mirrors JS's `data.branch || branch`: an absent,
// non-string, or empty data.branch falls back to the resolved branch.
func stringOrFallback(v any, fallback string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}
