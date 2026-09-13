package hooks

import (
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// resolveRootBranch resolves the repo root and current branch the same way
// gatedAdvancingShipState (block_askuserquestion.go) and saveCompactRecovery
// (pre_compact_save.go) already do. ok is false when either resolution
// fails, or the branch is empty/detached (HEAD) — matching both of those
// handlers' silent-bail behavior on the same conditions.
func resolveRootBranch() (root, branch string, ok bool) {
	root, err := worktree.MainRoot()
	if err != nil {
		return "", "", false
	}
	branch, err = gitx.CurrentBranch(resolveActiveWorktreeSafe())
	if err != nil || branch == "" || branch == "HEAD" {
		return "", "", false
	}
	return root, branch, true
}

// resolvePipelineStepContext identifies which pipeline (if any) is active
// for branch, using the same ship-priority/execute-fallback resolution
// saveCompactRecovery uses (pre_compact_save.go) for its own recovery-object
// source. pipelineName is "ship", "execute", or "" when neither state file
// exists for branch. For "ship", step is the currently advancing step's
// display name (empty when the pipeline isn't actively advancing). For
// "execute", wave is the highest wave number recorded so far (nil if none
// yet). Session ownership (state.HookEnforcementAllowed) is deliberately
// NOT checked here: this is passive evidence recording, not an enforcement
// decision, so any session observing the branch's own state may record
// against it.
func resolvePipelineStepContext(root, branch string) (pipelineName, step string, wave *int) {
	if st, err := state.Find(root, "ship", branch); err == nil && st != nil {
		pipelineName = "ship"
		if adv := state.PipelineAdvancing(st.Data); adv.Advancing {
			step = stepDisplayName(adv.Step)
		}
		return
	}
	if st, err := state.Find(root, "execute", branch); err == nil && st != nil {
		pipelineName = "execute"
		wave = tools.ExecLastRecordedWaveNumber(st.Data)
		return
	}
	return "", "", nil
}
