package hooks

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// Deviation from the plan's literal Task 4 acceptance criterion 10 ("Tests
// use gitFixture(t, branch) and t.TempDir() ... matching the established
// internal/hooks test pattern"), recorded per the harden decision on
// no-real-fs-git-in-tests ("harden", decideId "no-real-fs-git-in-tests"):
//
// These tests fake mainRootFunc/activeRootFunc/currentBranchFunc/
// findStateFunc (gitseam.go) instead of shelling out to a real git
// repository via gitFixture(t, branch), and fake touchProgressFunc
// (wave_liveness.go's seam over wave.TouchProgress) instead of letting the
// hook perform a real fs write. No real git process runs, no real state
// JSON file is ever written or read, and no real progress JSON is ever
// written or read. All paths are literal strings under fake roots
// ("/fake/repo", "/fake/outside") — filepath.Rel/Clean are pure string
// operations, so no directory needs to exist on disk. Zero real fs or git
// I/O anywhere in this file.
const fakeRoot = "/fake/repo"

// withWaveLivenessFakes swaps the four gitseam.go vars for fakes that return
// root/branch/state deterministically, restoring the originals on cleanup.
// findState may be nil, meaning "no execute state" (nil, nil).
func withWaveLivenessFakes(t *testing.T, root, branch string, findState func(prefix string) (*state.State, error)) {
	t.Helper()
	oldMain, oldActive, oldBranch, oldFind := mainRootFunc, activeRootFunc, currentBranchFunc, findStateFunc
	mainRootFunc = func() (string, error) { return root, nil }
	activeRootFunc = func() (string, error) { return root, nil }
	currentBranchFunc = func(string) (string, error) { return branch, nil }
	findStateFunc = func(_, prefix, _ string) (*state.State, error) {
		if findState == nil {
			return nil, nil
		}
		return findState(prefix)
	}
	t.Cleanup(func() {
		mainRootFunc, activeRootFunc, currentBranchFunc, findStateFunc = oldMain, oldActive, oldBranch, oldFind
	})
}

// touchCall records one touchProgressFunc invocation.
type touchCall struct {
	root, runID, taskID string
}

// withTouchProgressRecorder swaps touchProgressFunc for a fake that appends
// to a slice instead of writing real progress JSON, restoring the original
// on cleanup. The returned pointer accumulates every call made during the
// test.
func withTouchProgressRecorder(t *testing.T) *[]touchCall {
	t.Helper()
	calls := &[]touchCall{}
	old := touchProgressFunc
	touchProgressFunc = func(root, runID, taskID string) error {
		*calls = append(*calls, touchCall{root: root, runID: runID, taskID: taskID})
		return nil
	}
	t.Cleanup(func() { touchProgressFunc = old })
	return calls
}

// waveOneTaskData builds an execute state Data payload with a single
// in-progress wave, a runId, and the given planned tasks (id -> files) plus
// any closed/completed task statuses.
func waveOneTaskData(runID string, planned map[string][]string, closedStatus map[string]string) map[string]any {
	plannedList := make([]any, 0, len(planned))
	ids := make([]string, 0, len(planned))
	for id := range planned {
		ids = append(ids, id)
	}
	for _, id := range ids {
		files := make([]any, 0, len(planned[id]))
		for _, f := range planned[id] {
			files = append(files, f)
		}
		plannedList = append(plannedList, map[string]any{
			"id":    id,
			"name":  id,
			"files": files,
		})
	}
	tasks := make([]any, 0, len(closedStatus))
	for id, status := range closedStatus {
		tasks = append(tasks, map[string]any{"id": id, "status": status})
	}
	return map[string]any{
		"waves": []any{
			map[string]any{
				"number":  float64(1),
				"status":  "in_progress",
				"runId":   runID,
				"planned": plannedList,
				"tasks":   tasks,
			},
		},
	}
}

func execStateWith(data map[string]any) func(prefix string) (*state.State, error) {
	return func(prefix string) (*state.State, error) {
		if prefix != "execute" {
			return nil, nil
		}
		return &state.State{Prefix: "execute", Data: data}, nil
	}
}

func editEvent(filePath string) Event {
	return Event{Raw: map[string]any{
		"tool_input": map[string]any{"file_path": filePath},
	}}
}

// assertWaveLivenessSilent checks the error and delegates the Output shape
// check to assertSilent (gate_hooks_test.go), plus its own ExitCode check
// (assertSilent doesn't check ExitCode since gated hooks may legitimately
// return non-zero).
func assertWaveLivenessSilent(t *testing.T, out Output, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("waveLiveness returned error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("waveLiveness ExitCode = %d, want 0", out.ExitCode)
	}
	assertSilent(t, out)
}

// TestWaveLiveness_TouchesOwningTaskOnly covers acceptance criterion 1: an
// Edit on a file uniquely claimed by an open task advances that task's
// progress, and criterion "nothing else": sibling tasks are untouched.
func TestWaveLiveness_TouchesOwningTaskOnly(t *testing.T) {
	data := waveOneTaskData("run1", map[string][]string{
		"T1": {"internal/x.go"},
		"T2": {"internal/y.go"},
	}, nil)
	withWaveLivenessFakes(t, fakeRoot, "feat/x", execStateWith(data))
	calls := withTouchProgressRecorder(t)

	out, err := waveLiveness(HookCtx{}, editEvent(filepath.Join(fakeRoot, "internal/x.go")))
	assertWaveLivenessSilent(t, out, err)

	if len(*calls) != 1 {
		t.Fatalf("expected exactly 1 touchProgressFunc call, got %+v", *calls)
	}
	got := (*calls)[0]
	if got.root != fakeRoot || got.runID != "run1" || got.taskID != "T1" {
		t.Fatalf("touchProgressFunc call = %+v, want {%s run1 T1}", got, fakeRoot)
	}
}

// TestWaveLiveness_NoOwner covers acceptance criterion 2: an Edit on a file
// not claimed by any open task writes nothing.
func TestWaveLiveness_NoOwner(t *testing.T) {
	data := waveOneTaskData("run1", map[string][]string{
		"T1": {"internal/x.go"},
	}, nil)
	withWaveLivenessFakes(t, fakeRoot, "feat/x", execStateWith(data))
	calls := withTouchProgressRecorder(t)

	out, err := waveLiveness(HookCtx{}, editEvent(filepath.Join(fakeRoot, "internal/unowned.go")))
	assertWaveLivenessSilent(t, out, err)

	if len(*calls) != 0 {
		t.Fatalf("expected no touchProgressFunc calls, got %+v", *calls)
	}
}

// TestWaveLiveness_AmbiguousFileOwner covers acceptance criterion 3: a file
// claimed by two open tasks is ambiguous and writes nothing.
func TestWaveLiveness_AmbiguousFileOwner(t *testing.T) {
	data := waveOneTaskData("run1", map[string][]string{
		"T1": {"internal/shared.go"},
		"T2": {"internal/shared.go"},
	}, nil)
	withWaveLivenessFakes(t, fakeRoot, "feat/x", execStateWith(data))
	calls := withTouchProgressRecorder(t)

	out, err := waveLiveness(HookCtx{}, editEvent(filepath.Join(fakeRoot, "internal/shared.go")))
	assertWaveLivenessSilent(t, out, err)

	if len(*calls) != 0 {
		t.Fatalf("expected no touchProgressFunc calls for ambiguous owner, got %+v", *calls)
	}
}

// TestWaveLiveness_ClosedTaskFileIgnored covers acceptance criterion 4: a
// file owned by a task whose wave row already closed (non in_progress
// status) writes nothing, because ExecOpenWaveTaskFiles excludes it.
func TestWaveLiveness_ClosedTaskFileIgnored(t *testing.T) {
	data := waveOneTaskData("run1", map[string][]string{
		"T1": {"internal/x.go"},
		"T4": {"internal/done.go"},
	}, map[string]string{"T4": "completed"})
	withWaveLivenessFakes(t, fakeRoot, "feat/x", execStateWith(data))
	calls := withTouchProgressRecorder(t)

	out, err := waveLiveness(HookCtx{}, editEvent(filepath.Join(fakeRoot, "internal/done.go")))
	assertWaveLivenessSilent(t, out, err)

	if len(*calls) != 0 {
		t.Fatalf("expected no touchProgressFunc calls for closed task, got %+v", *calls)
	}
}

// TestWaveLiveness_SilentBailCases covers acceptance criteria 5 and 6: a
// grab-bag of missing/failing inputs, each expected to fail open with a
// silent Output{ExitCode: 0} and no decision payload, and no progress write.
func TestWaveLiveness_SilentBailCases(t *testing.T) {
	t.Run("no tool_input", func(t *testing.T) {
		withWaveLivenessFakes(t, fakeRoot, "feat/x", execStateWith(waveOneTaskData("run1", map[string][]string{"T1": {"internal/x.go"}}, nil)))
		calls := withTouchProgressRecorder(t)
		out, err := waveLiveness(HookCtx{}, Event{Raw: map[string]any{}})
		assertWaveLivenessSilent(t, out, err)
		if len(*calls) != 0 {
			t.Fatalf("expected no touchProgressFunc calls, got %+v", *calls)
		}
	})

	t.Run("file_path and path both absent", func(t *testing.T) {
		withWaveLivenessFakes(t, fakeRoot, "feat/x", execStateWith(waveOneTaskData("run1", map[string][]string{"T1": {"internal/x.go"}}, nil)))
		calls := withTouchProgressRecorder(t)
		out, err := waveLiveness(HookCtx{}, Event{Raw: map[string]any{"tool_input": map[string]any{}}})
		assertWaveLivenessSilent(t, out, err)
		if len(*calls) != 0 {
			t.Fatalf("expected no touchProgressFunc calls, got %+v", *calls)
		}
	})

	t.Run("non-git directory (mainRootFunc fails)", func(t *testing.T) {
		withWaveLivenessFakes(t, fakeRoot, "feat/x", execStateWith(waveOneTaskData("run1", map[string][]string{"T1": {"internal/x.go"}}, nil)))
		calls := withTouchProgressRecorder(t)
		mainRootFunc = func() (string, error) { return "", errors.New("not a git repo") }
		out, err := waveLiveness(HookCtx{}, editEvent(filepath.Join(fakeRoot, "internal/x.go")))
		assertWaveLivenessSilent(t, out, err)
		if len(*calls) != 0 {
			t.Fatalf("expected no touchProgressFunc calls, got %+v", *calls)
		}
	})

	t.Run("absent execute state", func(t *testing.T) {
		withWaveLivenessFakes(t, fakeRoot, "feat/x", nil)
		calls := withTouchProgressRecorder(t)
		out, err := waveLiveness(HookCtx{}, editEvent(filepath.Join(fakeRoot, "internal/x.go")))
		assertWaveLivenessSilent(t, out, err)
		if len(*calls) != 0 {
			t.Fatalf("expected no touchProgressFunc calls, got %+v", *calls)
		}
	})

	t.Run("wave row with no runId", func(t *testing.T) {
		data := waveOneTaskData("", map[string][]string{"T1": {"internal/x.go"}}, nil)
		withWaveLivenessFakes(t, fakeRoot, "feat/x", execStateWith(data))
		calls := withTouchProgressRecorder(t)
		out, err := waveLiveness(HookCtx{}, editEvent(filepath.Join(fakeRoot, "internal/x.go")))
		assertWaveLivenessSilent(t, out, err)
		if len(*calls) != 0 {
			t.Fatalf("expected no touchProgressFunc calls, got %+v", *calls)
		}
	})
}

// TestWaveLiveness_FileOutsideActiveWorktree covers acceptance criterion 7:
// an edited path that resolves outside the active worktree root (filepath.Rel
// escapes with "..") writes nothing. Purely a string computation: neither
// fakeRoot nor the outside path needs to exist on disk.
func TestWaveLiveness_FileOutsideActiveWorktree(t *testing.T) {
	outside := filepath.Join(filepath.Dir(fakeRoot), "outside", "x.go")

	data := waveOneTaskData("run1", map[string][]string{"T1": {"../outside/x.go"}}, nil)
	withWaveLivenessFakes(t, fakeRoot, "feat/x", execStateWith(data))
	calls := withTouchProgressRecorder(t)

	out, err := waveLiveness(HookCtx{}, editEvent(outside))
	assertWaveLivenessSilent(t, out, err)

	if len(*calls) != 0 {
		t.Fatalf("expected no touchProgressFunc calls for out-of-worktree file, got %+v", *calls)
	}
}
