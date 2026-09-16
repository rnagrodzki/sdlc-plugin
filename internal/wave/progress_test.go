package wave

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// UpdateProgress — per-task file, pure write
// ---------------------------------------------------------------------------

func TestUpdateProgress_WritesPerTaskFile(t *testing.T) {
	root := t.TempDir()
	runID := "run1"

	if err := UpdateProgress(root, runID, "3", "editing", ""); err != nil {
		t.Fatalf("UpdateProgress: unexpected error: %v", err)
	}

	wantPath := taskProgressPath(root, runID, "3")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected per-task file at %s: %v", wantPath, err)
	}

	// No shared/aggregate file should be touched by a write.
	if _, err := os.Stat(legacyProgressPath(root, runID)); !os.IsNotExist(err) {
		t.Fatalf("UpdateProgress must not write the legacy progress.json path, stat err = %v", err)
	}
}

func TestUpdateProgress_RejectsBadPhase(t *testing.T) {
	root := t.TempDir()
	err := UpdateProgress(root, "run1", "1", "not-a-real-phase", "")
	if err == nil {
		t.Fatalf("UpdateProgress: expected error for invalid phase, got nil")
	}
}

// ---------------------------------------------------------------------------
// UpdateProgress — idempotent overwrite
// ---------------------------------------------------------------------------

func TestUpdateProgress_IdempotentOverwrite(t *testing.T) {
	root := t.TempDir()
	runID := "run1"

	if err := UpdateProgress(root, runID, "1", "started", ""); err != nil {
		t.Fatalf("UpdateProgress first: %v", err)
	}
	if err := UpdateProgress(root, runID, "1", "editing", ""); err != nil {
		t.Fatalf("UpdateProgress second: %v", err)
	}
	if err := UpdateProgress(root, runID, "1", "reporting", ""); err != nil {
		t.Fatalf("UpdateProgress third: %v", err)
	}

	p, err := ReadProgress(root, runID)
	if err != nil {
		t.Fatalf("ReadProgress: %v", err)
	}
	if len(p.Tasks) != 1 {
		t.Fatalf("expected exactly 1 task entry after repeated overwrite, got %d: %+v", len(p.Tasks), p.Tasks)
	}
	if got := p.Tasks["1"].Phase; got != "reporting" {
		t.Fatalf("expected final phase %q, got %q", "reporting", got)
	}
}

// ---------------------------------------------------------------------------
// ReadProgress — aggregation across per-task files
// ---------------------------------------------------------------------------

func TestReadProgress_AggregatesAllTaskFiles(t *testing.T) {
	root := t.TempDir()
	runID := "run1"

	if err := UpdateProgress(root, runID, "1", "started", ""); err != nil {
		t.Fatalf("UpdateProgress 1: %v", err)
	}
	if err := UpdateProgress(root, runID, "2", "editing", ""); err != nil {
		t.Fatalf("UpdateProgress 2: %v", err)
	}
	if err := UpdateProgress(root, runID, "3", "reporting", ""); err != nil {
		t.Fatalf("UpdateProgress 3: %v", err)
	}

	p, err := ReadProgress(root, runID)
	if err != nil {
		t.Fatalf("ReadProgress: %v", err)
	}
	if len(p.Tasks) != 3 {
		t.Fatalf("expected 3 aggregated task entries, got %d: %+v", len(p.Tasks), p.Tasks)
	}
	wantPhases := map[string]string{"1": "started", "2": "editing", "3": "reporting"}
	for taskID, wantPhase := range wantPhases {
		tp, ok := p.Tasks[taskID]
		if !ok {
			t.Errorf("missing task %q in aggregated progress", taskID)
			continue
		}
		if tp.Phase != wantPhase {
			t.Errorf("task %q: phase = %q, want %q", taskID, tp.Phase, wantPhase)
		}
		if tp.UpdatedAt == "" {
			t.Errorf("task %q: updatedAt is empty", taskID)
		}
	}
}

func TestReadProgress_MissingRunReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	p, err := ReadProgress(root, "no-such-run")
	if err != nil {
		t.Fatalf("ReadProgress: unexpected error: %v", err)
	}
	if len(p.Tasks) != 0 {
		t.Fatalf("expected empty Tasks for missing run, got %+v", p.Tasks)
	}
}

func TestReadProgress_SkipsCorruptTaskFile(t *testing.T) {
	root := t.TempDir()
	runID := "run1"

	if err := UpdateProgress(root, runID, "1", "started", ""); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	// Hand-write a corrupt sibling per-task file.
	dir := progressDir(root, runID)
	if err := os.WriteFile(filepath.Join(dir, "2.json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	p, err := ReadProgress(root, runID)
	if err != nil {
		t.Fatalf("ReadProgress: unexpected error on corrupt sibling file: %v", err)
	}
	if _, ok := p.Tasks["1"]; !ok {
		t.Fatalf("expected healthy task %q to survive corrupt sibling, got %+v", "1", p.Tasks)
	}
	if _, ok := p.Tasks["2"]; ok {
		t.Fatalf("expected corrupt task %q to be skipped, got entry %+v", "2", p.Tasks["2"])
	}
}

// ---------------------------------------------------------------------------
// ReadProgress — legacy progress.json merged at lower priority
// ---------------------------------------------------------------------------

func TestReadProgress_MergesLegacyFileAtLowerPriority(t *testing.T) {
	root := t.TempDir()
	runID := "run1"

	// Hand-write a legacy single-file marker with two tasks.
	dir := executionDir(root, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacyContent := `{"tasks":{"1":{"phase":"started","updatedAt":"2026-01-01T00:00:00.000Z"},"2":{"phase":"reading","updatedAt":"2026-01-01T00:00:00.000Z"}}}`
	if err := os.WriteFile(legacyProgressPath(root, runID), []byte(legacyContent), 0o644); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	// Per-task file for task 2 overrides the legacy entry.
	if err := UpdateProgress(root, runID, "2", "verifying", ""); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	p, err := ReadProgress(root, runID)
	if err != nil {
		t.Fatalf("ReadProgress: %v", err)
	}

	// Task 1 comes only from the legacy file.
	if got := p.Tasks["1"].Phase; got != "started" {
		t.Errorf("task 1: phase = %q, want %q (from legacy)", got, "started")
	}
	// Task 2 is present in both; the per-task file must win.
	if got := p.Tasks["2"].Phase; got != "verifying" {
		t.Errorf("task 2: phase = %q, want %q (per-task file must win over legacy)", got, "verifying")
	}
}

func TestReadProgress_NoLegacyFileIsFine(t *testing.T) {
	root := t.TempDir()
	runID := "run1"

	if err := UpdateProgress(root, runID, "1", "started", ""); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	p, err := ReadProgress(root, runID)
	if err != nil {
		t.Fatalf("ReadProgress: unexpected error with no legacy file: %v", err)
	}
	if len(p.Tasks) != 1 {
		t.Fatalf("expected 1 task entry, got %d: %+v", len(p.Tasks), p.Tasks)
	}
}

// ---------------------------------------------------------------------------
// Concurrent writes — different taskIDs, both entries present
// ---------------------------------------------------------------------------

func TestUpdateProgress_ConcurrentDifferentTaskIDs(t *testing.T) {
	root := t.TempDir()
	runID := "concurrent-run"

	const numTasks = 8
	var wg sync.WaitGroup
	errCh := make(chan error, numTasks)

	for i := range numTasks {
		wg.Add(1)
		go func(taskNum int) {
			defer wg.Done()
			taskID := fmt.Sprintf("%d", taskNum)
			if err := UpdateProgress(root, runID, taskID, "editing", ""); err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent UpdateProgress error: %v", err)
	}

	p, err := ReadProgress(root, runID)
	if err != nil {
		t.Fatalf("ReadProgress: %v", err)
	}
	if len(p.Tasks) != numTasks {
		t.Fatalf("expected %d task entries after concurrent writes, got %d: %+v", numTasks, len(p.Tasks), p.Tasks)
	}
	for i := range numTasks {
		taskID := fmt.Sprintf("%d", i)
		tp, ok := p.Tasks[taskID]
		if !ok {
			t.Errorf("missing task %q after concurrent writes", taskID)
			continue
		}
		if tp.Phase != "editing" {
			t.Errorf("task %q: phase = %q, want %q", taskID, tp.Phase, "editing")
		}
	}
}

// ---------------------------------------------------------------------------
// UpdateProgress: LastCompletedTask
// ---------------------------------------------------------------------------

func TestUpdateProgress_LastCompletedTask(t *testing.T) {
	root := t.TempDir()
	runID := "20250615T120000"

	if err := UpdateProgress(root, runID, "1", "started", "T0"); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	p, err := ReadProgress(root, runID)
	if err != nil {
		t.Fatalf("ReadProgress: %v", err)
	}

	tp, ok := p.Tasks["1"]
	if !ok {
		t.Fatal("task 1 not found in progress")
	}
	if tp.LastCompletedTask != "T0" {
		t.Errorf("LastCompletedTask = %q, want T0", tp.LastCompletedTask)
	}
}

func TestUpdateProgress_PreservesLastCompletedTask(t *testing.T) {
	root := t.TempDir()
	runID := "20250615T120000"

	// First write with lastCompletedTask.
	if err := UpdateProgress(root, runID, "1", "started", "T0"); err != nil {
		t.Fatalf("UpdateProgress 1: %v", err)
	}
	// Second write without lastCompletedTask — should preserve T0.
	if err := UpdateProgress(root, runID, "1", "editing", ""); err != nil {
		t.Fatalf("UpdateProgress 2: %v", err)
	}

	p, _ := ReadProgress(root, runID)
	tp := p.Tasks["1"]
	if tp.LastCompletedTask != "T0" {
		t.Errorf("LastCompletedTask = %q, want T0 (preserved)", tp.LastCompletedTask)
	}
	if tp.Phase != "editing" {
		t.Errorf("Phase = %q, want editing", tp.Phase)
	}
}

// ---------------------------------------------------------------------------
// UpdateProgress: StartedAt preserved across phase updates
// ---------------------------------------------------------------------------

func TestUpdateProgress_PreservesStartedAt(t *testing.T) {
	root := t.TempDir()
	runID := "20250615T120000"

	if err := UpdateProgress(root, runID, "1", "started", ""); err != nil {
		t.Fatalf("UpdateProgress 1: %v", err)
	}
	p, _ := ReadProgress(root, runID)
	firstStartedAt := p.Tasks["1"].StartedAt
	if firstStartedAt == "" {
		t.Fatal("StartedAt should be set on first write")
	}

	// Small delay to ensure time changes.
	time.Sleep(5 * time.Millisecond)

	if err := UpdateProgress(root, runID, "1", "editing", ""); err != nil {
		t.Fatalf("UpdateProgress 2: %v", err)
	}
	p, _ = ReadProgress(root, runID)
	if p.Tasks["1"].StartedAt != firstStartedAt {
		t.Errorf("StartedAt changed: %q -> %q", firstStartedAt, p.Tasks["1"].StartedAt)
	}
	if p.Tasks["1"].UpdatedAt == firstStartedAt {
		t.Error("UpdatedAt should change on second write")
	}
}

// ---------------------------------------------------------------------------
// UpdateProgress: structured-milestone fields (AcceptanceDone, FilesTouched,
// Blocker)
// ---------------------------------------------------------------------------

func TestUpdateProgress_WritesStructuredFields(t *testing.T) {
	root := t.TempDir()
	runID := "run1"

	err := UpdateProgress(root, runID, "1", "editing", "", ProgressFields{
		AcceptanceDone: []int{0, 2, 3},
		FilesTouched:   []string{"a.go", "b.go"},
		Blocker:        "waiting on review",
	})
	if err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	p, err := ReadProgress(root, runID)
	if err != nil {
		t.Fatalf("ReadProgress: %v", err)
	}
	tp, ok := p.Tasks["1"]
	if !ok {
		t.Fatal("task 1 not found in progress")
	}
	if got, want := tp.AcceptanceDone, []int{0, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("AcceptanceDone = %v, want %v", got, want)
	}
	if got, want := tp.FilesTouched, []string{"a.go", "b.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("FilesTouched = %v, want %v", got, want)
	}
	if tp.Blocker != "waiting on review" {
		t.Errorf("Blocker = %q, want %q", tp.Blocker, "waiting on review")
	}
}

func TestUpdateProgress_PreservesStructuredFieldsWhenOmitted(t *testing.T) {
	root := t.TempDir()
	runID := "run1"

	if err := UpdateProgress(root, runID, "1", "editing", "", ProgressFields{
		AcceptanceDone: []int{0},
		FilesTouched:   []string{"a.go"},
		Blocker:        "blocked",
	}); err != nil {
		t.Fatalf("UpdateProgress first: %v", err)
	}

	// Second write with no ProgressFields at all — plain positional call,
	// exercising the variadic-omitted path new callers don't need to touch.
	if err := UpdateProgress(root, runID, "1", "verifying", ""); err != nil {
		t.Fatalf("UpdateProgress second: %v", err)
	}

	p, _ := ReadProgress(root, runID)
	tp := p.Tasks["1"]
	if got, want := tp.AcceptanceDone, []int{0}; !reflect.DeepEqual(got, want) {
		t.Errorf("AcceptanceDone changed: got %v, want preserved %v", got, want)
	}
	if got, want := tp.FilesTouched, []string{"a.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("FilesTouched changed: got %v, want preserved %v", got, want)
	}
	if tp.Blocker != "blocked" {
		t.Errorf("Blocker changed: got %q, want preserved %q", tp.Blocker, "blocked")
	}
	if tp.Phase != "verifying" {
		t.Errorf("Phase = %q, want %q", tp.Phase, "verifying")
	}
}

func TestUpdateProgress_OverwritesAcceptanceDoneOnNextCall(t *testing.T) {
	root := t.TempDir()
	runID := "run1"

	if err := UpdateProgress(root, runID, "1", "editing", "", ProgressFields{AcceptanceDone: []int{0}}); err != nil {
		t.Fatalf("UpdateProgress first: %v", err)
	}
	if err := UpdateProgress(root, runID, "1", "verifying", "", ProgressFields{AcceptanceDone: []int{0, 1, 2}}); err != nil {
		t.Fatalf("UpdateProgress second: %v", err)
	}

	p, _ := ReadProgress(root, runID)
	if got, want := p.Tasks["1"].AcceptanceDone, []int{0, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("AcceptanceDone = %v, want %v", got, want)
	}
}
