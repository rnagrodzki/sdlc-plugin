package hooks

import (
	"path/filepath"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
	"github.com/rnagrodzki/sdlc-plugin/internal/wave"
)

// touchProgressFunc is the seam over wave.TouchProgress: a package var so
// tests can fake the progress write (record the call, no real fs I/O)
// instead of invoking internal/wave's real, package-private fs-writer store.
var touchProgressFunc = wave.TouchProgress

// waveLiveness is the "wave-liveness" hook handler (PostToolUse, matcher
// Edit|Write). It stamps TaskProgress.UpdatedAt for the open wave task that
// owns the edited file, so Subject.Liveness tracks real activity instead of
// the five phase-transition heartbeats a worker emits over its whole life.
//
// Fail-open at every step: a missing path, an unresolvable root, no execute
// state, no runId, or an ambiguous file match are all silent no-ops. This
// hook never blocks a tool call and never writes to stdout.
func waveLiveness(ctx HookCtx, event Event) (Output, error) {
	silent := Output{ExitCode: 0}

	toolInput, _ := event.Raw["tool_input"].(map[string]any)
	filePath, _ := toolInput["file_path"].(string)
	if filePath == "" {
		filePath, _ = toolInput["path"].(string)
	}
	if filePath == "" {
		return silent, nil
	}

	root, branch, ok := resolveRootBranch()
	if !ok {
		return silent, nil
	}
	// findStateFunc (gitseam.go) rather than state.Find directly, so tests
	// can fake the execute state lookup without a real state file on disk.
	st, err := findStateFunc(root, "execute", branch)
	if err != nil || st == nil {
		return silent, nil
	}

	runID, filesByTask := tools.ExecOpenWaveTaskFiles(st.Data)
	if runID == "" || len(filesByTask) == 0 {
		return silent, nil
	}

	// State anchors to MainRoot (resolveRootBranch); the edited file lives in
	// the ACTIVE worktree, so the relative path is computed against the
	// package's never-failing active-worktree resolver.
	rel, rerr := filepath.Rel(resolveActiveWorktreeSafe(), filePath)
	if rerr != nil || strings.HasPrefix(rel, "..") {
		return silent, nil
	}
	rel = filepath.ToSlash(filepath.Clean(rel))

	taskID := wave.ResolveTaskForFile(filesByTask, rel)
	if taskID == "" {
		return silent, nil
	}
	_ = touchProgressFunc(root, runID, taskID) // advisory: never block
	return silent, nil
}
