package hooks

import (
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// recordUserInput is the "record-user-input" hook handler
// (UserPromptSubmit). It runs the raw prompt through tools.CleanUserPrompt
// first — dropping a turn injected by the editor or the agent runtime (e.g.
// a <task-notification>) outright, and stripping envelope tags out of an
// otherwise-genuine prompt — before doing any state lookup, then appends the
// cleaned text to user-inputs.jsonl (kind "prompt") when a ship or execute
// run is active on this branch. It always returns Output{}: stdout on
// UserPromptSubmit would be added to Claude's context, so unlike
// pipeline-continue's advisory nudges, this hook has nothing to say back to
// the session.
func recordUserInput(ctx HookCtx, event Event) (Output, error) {
	silent := Output{}

	text, _ := event.Raw["prompt_text"].(string)
	if text == "" {
		text, _ = event.Raw["prompt"].(string)
	}
	if strings.TrimSpace(text) == "" {
		return silent, nil
	}

	cleaned, keep := tools.CleanUserPrompt(text)
	if !keep {
		return silent, nil
	}

	root, branch, ok := resolveRootBranch()
	if !ok {
		return silent, nil
	}

	pipelineName, step, wave, active := userInputRunContext(root, branch)
	if !active {
		return silent, nil
	}

	_ = tools.AppendUserInput(root, tools.UserInputEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Pipeline:  pipelineName,
		Step:      step,
		Wave:      wave,
		Branch:    branch,
		Text:      cleaned,
		Kind:      tools.UserInputKindPrompt,
		SessionID: ctx.SessionID,
	})

	return silent, nil
}

// userInputRunContext identifies which pipeline (if any) is active for
// branch and should have this user prompt attributed to it. It uses the
// same ship-priority/execute-fallback resolution resolvePipelineStepContext
// uses (evidence.go) — ship state, when present, always decides the
// outcome; execute state is only consulted when no ship state exists — but
// applies its own "active" rule, since a user-typed prompt matters in cases
// passive CLI/MCP recording does not:
//
//   - ship, advancing: active; step is the advancing step's display name.
//   - ship, not advancing, a step failed: active (the user is typing in
//     reaction to the failure); step is the first failed step's display
//     name.
//   - ship, not advancing, no step failed (every step completed, the run is
//     over): not active.
//   - execute, runStatus not "completed": active; wave is the highest
//     recorded wave number.
//   - execute, runStatus "completed": not active.
//   - no ship or execute state for branch: not active.
func userInputRunContext(root, branch string) (pipelineName, step string, wave *int, active bool) {
	if st, err := state.Find(root, "ship", branch); err == nil && st != nil {
		if adv := state.PipelineAdvancing(st.Data); adv.Advancing {
			return "ship", stepDisplayName(adv.Step), nil, true
		}
		if name := firstFailedStepName(st.Data); name != "" {
			return "ship", name, nil, true
		}
		return "", "", nil, false
	}

	if st, err := state.Find(root, "execute", branch); err == nil && st != nil {
		if status, _ := st.Data["runStatus"].(string); status == "completed" {
			return "", "", nil, false
		}
		return "execute", "", tools.ExecLastRecordedWaveNumber(st.Data), true
	}

	return "", "", nil, false
}

// firstFailedStepName returns the display name (stepDisplayName) of the
// first step in data["steps"] whose status is "failed", or "" when none
// failed or data carries no steps slice.
func firstFailedStepName(data map[string]any) string {
	stepsRaw, ok := data["steps"]
	if !ok {
		return ""
	}
	steps, ok := stepsRaw.([]any)
	if !ok {
		return ""
	}
	for _, raw := range steps {
		s, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if status, _ := s["status"].(string); status == "failed" {
			return stepDisplayName(s)
		}
	}
	return ""
}
