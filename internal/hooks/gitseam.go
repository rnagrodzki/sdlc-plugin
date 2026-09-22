package hooks

import (
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// The package-level vars below are internal/hooks's git/fs seam. Handlers
// that need the main worktree root, the active worktree root, the current
// branch, or a pipeline's state file resolve through these vars instead of
// calling worktree/gitx/state directly. Production code never reassigns
// them — they exist so tests can substitute an in-memory fake for the
// duration of a single test (save the old value, defer restoring it)
// instead of shelling out to real git or touching a real state file, which
// the no-real-fs-git-in-tests guardrail (error severity) requires for any
// new test code exercising them.
//
// Routed through these vars (keep this list in step with
// `grep -n 'mainRootFunc\|activeRootFunc\|currentBranchFunc\|findStateFunc' *.go`):
//
//   - resolveRootBranch (evidence.go) — mainRootFunc, currentBranchFunc
//   - resolveActiveWorktreeSafe (session_start.go) — activeRootFunc
//   - deferredBacklogPhase (session_start.go) — mainRootFunc
//   - gatedAdvancingShipState (block_askuserquestion.go) — mainRootFunc,
//     currentBranchFunc
//   - waveLiveness (wave_liveness.go) — findStateFunc
//
// Every other worktree.*/gitx.*/state.Find call site in this package (e.g.
// post_tool_validate.go, pre_compact_save.go, stop_hooks.go) is
// intentionally left calling the real packages directly — out of scope,
// unaffected.
//
// binarySkewPhase (session_start.go) is deliberately NOT routed here: it
// runs `git rev-parse` against the plugin's own checkout, not the project
// worktree, so none of these vars describe what it resolves. Its tests build
// a real repo under t.TempDir(), which no-real-fs-git-in-tests permits.
var (
	// mainRootFunc resolves the main worktree root. Defaults to
	// worktree.MainRoot.
	mainRootFunc = worktree.MainRoot
	// activeRootFunc resolves the active worktree root. Defaults to
	// worktree.ActiveRoot.
	activeRootFunc = worktree.ActiveRoot
	// currentBranchFunc resolves the current branch for a given directory.
	// Defaults to gitx.CurrentBranch.
	currentBranchFunc = gitx.CurrentBranch
	// findStateFunc looks up a pipeline's state file. Defaults to
	// state.Find.
	findStateFunc = state.Find
)
