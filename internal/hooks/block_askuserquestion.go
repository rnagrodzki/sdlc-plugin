package hooks

import (
	"fmt"
	"os"

	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// blockAskUserQuestionAuto is the "block-askuserquestion-auto" hook handler
// (PreToolUse, matcher AskUserQuestion), ported from
// hooks/block-askuserquestion-auto.js. It denies the AskUserQuestion tool
// call only when a ship-sdlc pipeline is actively advancing in --auto mode
// for the current session; every other case is a silent no-op (exit 0, no
// stdout) so this hook never interferes with an interactive session (C18).
//
// The gate is a flat AND-chain of four conditions evaluated in this exact
// order, each with early-exit-silent on failure: (1) a real branch resolves,
// (2) a ship state file exists for it and is advancing, (3) the calling
// session owns that state (HookEnforcementAllowed), (4) flags.auto is
// strictly the literal boolean true (not merely truthy).
func blockAskUserQuestionAuto(ctx HookCtx, event Event) (Output, error) {
	silent := Output{ExitCode: 0}

	data, _, ok := gatedAdvancingShipState("block-askuserquestion-auto", ctx.SessionID)
	if !ok {
		return silent, nil
	}

	// Strict check: flags.auto must literally be the boolean true. A missing
	// flags object, an absent auto key, false, or a truthy non-boolean (e.g.
	// a non-empty string) must all fall through to silent here — this is
	// deliberately NOT the same loose-truthy check pipeline-continue.go uses.
	flags, _ := data["flags"].(map[string]any)
	autoVal, isBool := flags["auto"].(bool)
	if !isBool || !autoVal {
		return silent, nil
	}

	return Output{JSON: map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": "Auto-mode ship pipeline is advancing (flags.auto=true). Do NOT pause for input — proceed on the documented default (auto-dispatch the fix path). See ship-sdlc R71/#477.",
		},
	}, ExitCode: 0}, nil
}

// gatedAdvancingShipState resolves the current branch's ship state via the
// exact root/branch split session_start.go's pipelineResumePhase already
// established (worktree.MainRoot for the state file, gitx.CurrentBranch over
// resolveActiveWorktreeSafe for the branch), then applies the shared
// state.PipelineAdvancing / state.HookEnforcementAllowed gate that both
// block-askuserquestion-auto and pipeline-continue perform identically
// before diverging into their own deny/nudge logic. ok is true only when a
// state file was found, is advancing, and the session is authorized to
// enforce it; data and adv are only meaningful when ok is true (data is
// still returned on an advancing-but-unauthorized result so callers never
// need a second state.Find).
func gatedAdvancingShipState(hookName, sessionID string) (data map[string]any, adv state.PipelineAdvancingResult, ok bool) {
	notAdvancing := state.PipelineAdvancingResult{Index: -1}

	root, err := worktree.MainRoot()
	if err != nil {
		return nil, notAdvancing, false
	}
	branch, err := gitx.CurrentBranch(resolveActiveWorktreeSafe())
	if err != nil || branch == "" || branch == "HEAD" {
		return nil, notAdvancing, false
	}

	st, err := state.Find(root, "ship", branch)
	if err != nil || st == nil {
		return nil, notAdvancing, false
	}

	adv = state.PipelineAdvancing(st.Data)
	if !adv.Advancing {
		return st.Data, adv, false
	}

	enforcement := state.HookEnforcementAllowed(st.Data, sessionID)
	if !enforcement.Allowed {
		// Debugging diagnostic only, not part of the JSON contract (Run has
		// no injectable stderr writer, so this goes straight to os.Stderr).
		fmt.Fprintf(os.Stderr, "[sdlc/hook %s] not enforcing — %s\n", hookName, enforcement.Reason)
		return st.Data, adv, false
	}

	return st.Data, adv, true
}
