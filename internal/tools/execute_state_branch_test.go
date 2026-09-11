package tools

import (
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ---------------------------------------------------------------------------
// execAssertBranch (unit)
// ---------------------------------------------------------------------------

func TestExecAssertBranch_Match(t *testing.T) {
	st := &state.State{Data: map[string]any{"branch": "feat/test"}}
	if err := execAssertBranch(st, "feat/test"); err != nil {
		t.Fatalf("execAssertBranch: unexpected error for matching branch: %v", err)
	}
}

func TestExecAssertBranch_Mismatch(t *testing.T) {
	st := &state.State{Data: map[string]any{"branch": "feat/original"}}
	err := execAssertBranch(st, "feat/other")
	if err == nil {
		t.Fatal("execAssertBranch: expected error for mismatched branch, got nil")
	}
	de, ok := err.(*mcpserver.DomainError)
	if !ok {
		t.Fatalf("execAssertBranch: expected *mcpserver.DomainError, got %T", err)
	}
	wantMsg := `branch changed mid-session: init recorded "feat/original", current is "feat/other"`
	if de.Error() != wantMsg {
		t.Errorf("execAssertBranch: message = %q, want %q", de.Error(), wantMsg)
	}
}

func TestExecAssertBranch_NoRecordedBranch(t *testing.T) {
	// A state file with no "branch" key (pre-KD-4 state) has nothing to
	// compare against — never asserted against, regardless of resolved.
	st := &state.State{Data: map[string]any{}}
	if err := execAssertBranch(st, "feat/anything"); err != nil {
		t.Fatalf("execAssertBranch: unexpected error when no branch was recorded: %v", err)
	}
}

// ---------------------------------------------------------------------------
// wave-start (acceptance criteria)
// ---------------------------------------------------------------------------
//
// A real branch mismatch can only reach execAssertBranch when execFindState
// still resolves to the same state file for the differing branch string —
// state.Find locates files by SlugifyBranch(branch), which collapses any
// non-alphanumeric run to a single hyphen. "feat/original" and
// "feat.original" both slugify to "feat-original", so they name the same
// state file while remaining distinct raw branch strings — exactly the
// mid-session drift (branch rename, ref reformatting) KD-4 guards against.
// A branch string that does not collide with the recorded slug fails earlier
// at execFindState with a DataError ("no state file found"), never reaching
// the assertion.

func TestExecState_WaveStart_BranchMismatch(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/original", map[string]any{
		"branch":  "feat/original",
		"waves":   []any{},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-start",
		Branch: "feat.original", // same slug as feat/original, different raw string
		Wave:   intPtr(1),
	}, clock)
	if err == nil {
		t.Fatal("wave-start: expected error for mismatched branch, got nil")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("wave-start: expected *mcpserver.DomainError, got %T: %v", err, err)
	}

	// The wave must not have been created/mutated by the rejected call.
	data := readExecState(t, root, "feat/original")
	waves, _ := data["waves"].([]any)
	if len(waves) != 0 {
		t.Errorf("wave-start: waves = %v, want untouched empty slice after rejected call", waves)
	}
}

func TestExecState_WaveStart_BranchMatch(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/original", map[string]any{
		"branch":  "feat/original",
		"waves":   []any{},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-start",
		Branch: "feat/original",
		Wave:   intPtr(1),
	}, clock)
	if err != nil {
		t.Fatalf("wave-start: unexpected error for matching branch: %v", err)
	}

	data := readExecState(t, root, "feat/original")
	waves, _ := data["waves"].([]any)
	if len(waves) != 1 {
		t.Fatalf("wave-start: expected 1 wave, got %d", len(waves))
	}
	w := waves[0].(map[string]any)
	if w["status"] != "in_progress" {
		t.Errorf("wave-start: wave status = %v, want in_progress", w["status"])
	}
}

// ---------------------------------------------------------------------------
// Other call sites — spot checks that the mirror was applied consistently
// (not just at wave-start).
// ---------------------------------------------------------------------------

func TestExecState_TaskDone_BranchMismatch(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/original", map[string]any{
		"branch": "feat/original",
		"waves": []any{
			map[string]any{"number": 1, "status": "in_progress", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "task-done",
		Branch: "feat.original", // same slug as feat/original, different raw string
		Wave:   intPtr(1),
		TaskID: "T1",
	}, clock)
	if err == nil {
		t.Fatal("task-done: expected error for mismatched branch, got nil")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("task-done: expected *mcpserver.DomainError, got %T: %v", err, err)
	}
}

func TestExecState_Read_BranchMismatch(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/original", map[string]any{
		"branch":  "feat/original",
		"waves":   []any{},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "read",
		Branch: "feat.original", // same slug as feat/original, different raw string
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("read: expected error for mismatched branch, got nil")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("read: expected *mcpserver.DomainError, got %T: %v", err, err)
	}
}

func TestExecState_VerifyCompleteness_BranchMismatch(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/original", map[string]any{
		"branch":         "feat/original",
		"plannedTaskIds": []any{"1"},
		"waves":          []any{},
		"context":        map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "verify-completeness",
		Branch: "feat.original", // same slug as feat/original, different raw string
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("verify-completeness: expected error for mismatched branch, got nil")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("verify-completeness: expected *mcpserver.DomainError, got %T: %v", err, err)
	}
}

// TestExecState_BranchMismatch_BackwardCompat covers a state file written
// before KD-4 (no "branch" key recorded at all, e.g. init predates this
// guardrail or the field was never persisted). Such state files must not be
// retroactively locked out — the assertion is a no-op when nothing was
// recorded to compare against.
func TestExecState_BranchMismatch_BackwardCompat(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/original", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-start",
		Branch: "feat/original",
		Wave:   intPtr(1),
	}, clock)
	if err != nil {
		t.Fatalf("wave-start: unexpected error for state file with no recorded branch: %v", err)
	}
}
