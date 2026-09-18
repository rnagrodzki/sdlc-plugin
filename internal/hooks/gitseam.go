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
// Only the handlers that gained new tests as part of this change
// (resolveRootBranch in evidence.go, resolveActiveWorktreeSafe in
// session_start.go, gatedAdvancingShipState in block_askuserquestion.go,
// and the new waveLiveness hook) are routed through these vars. Every
// other worktree.*/gitx.*/state.Find call site in this package (e.g.
// post_tool_validate.go, pre_compact_save.go, stop_hooks.go) is
// intentionally left calling the real packages directly — out of scope for
// this change, unaffected by it.
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
