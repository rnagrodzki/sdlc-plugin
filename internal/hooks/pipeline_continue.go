package hooks

import "fmt"

// pipelineContinue is the "pipeline-continue" hook handler (PostToolUse,
// matcher Bash|TodoWrite), ported from hooks/pipeline-continue.js. Unlike
// block-askuserquestion-auto's binary deny gate, this is a 3-way branch: it
// nudges Claude to keep working an active ship-sdlc pipeline via
// additionalContext, or stays silent.
//
// Shares the same root/branch resolution and PipelineAdvancing/
// HookEnforcementAllowed gate as block-askuserquestion-auto
// (gatedAdvancingShipState, in block_askuserquestion.go) — never
// re-implemented here.
func pipelineContinue(ctx HookCtx, event Event) (Output, error) {
	silent := Output{ExitCode: 0}

	data, adv, ok := gatedAdvancingShipState("pipeline-continue", ctx.SessionID)
	if !ok {
		return silent, nil
	}

	total := 0
	if steps, isArr := data["steps"].([]any); isArr {
		total = len(steps)
	}
	name := stepDisplayName(adv.Step)
	status, _ := adv.Step["status"].(string)

	if status == "in_progress" {
		// Mode-independent: fires regardless of flags.auto.
		msg := fmt.Sprintf(
			"Ship pipeline: step %d of %d (%s) is in_progress. Continue executing this step — do not end the response turn. Next action: record the step result and advance to the next step.",
			adv.Index+1, total, name,
		)
		return pipelineContinueOutput(msg), nil
	}

	// Step is the first non-terminal pending step (not in_progress): only
	// nudge to advance in --auto mode. This is deliberately the LOOSE
	// truthy check (flags.auto merely truthy), not the strict ===true check
	// block-askuserquestion-auto.go uses — a real, source-intentional
	// divergence, not something to normalize away.
	flags, _ := data["flags"].(map[string]any)
	if !jsTruthyAutoFlag(flags["auto"]) {
		return silent, nil
	}

	msg := fmt.Sprintf(
		"Ship pipeline: the next step is step %d of %d (%s), pending. In --auto mode, advance to it now — do not end the response turn. Next action: begin the next step.",
		adv.Index+1, total, name,
	)
	return pipelineContinueOutput(msg), nil
}

func pipelineContinueOutput(additionalContext string) Output {
	return Output{JSON: map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "PostToolUse",
			"additionalContext": additionalContext,
		},
	}, ExitCode: 0}
}

// stepDisplayName mirrors source line 90's step.name || step.id || "unknown"
// fallback chain.
func stepDisplayName(step map[string]any) string {
	if name, ok := step["name"].(string); ok && name != "" {
		return name
	}
	if id, ok := step["id"].(string); ok && id != "" {
		return id
	}
	return "unknown"
}

// jsTruthyAutoFlag mirrors JS truthiness for the small set of JSON-decoded
// scalar types flags.auto can take: nil -> false, bool -> itself,
// string -> non-empty, numeric -> non-zero. Objects/arrays (never a real
// flags.auto value in practice) fall through to true, matching plain JS
// truthiness. This is a deliberate 4th small unexported copy of this repo's
// existing per-package "JS truthiness" helpers (internal/discovery.jsTruthy,
// internal/dimensions.jsFalsy, internal/tools.jsFalsyLocal) rather than a
// cross-package import of any of them — see task fact sheet.
func jsTruthyAutoFlag(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	}
	return true
}
