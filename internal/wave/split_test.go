package wave

import (
	"errors"
	"reflect"
	"testing"
)

// --- Split -------------------------------------------------------------

func TestSplit_RefusesAtMaxDepth(t *testing.T) {
	_, err := Split([]Task{"1", "2"}, SplitOptions{SplitDepth: MaxSplitDepth})
	if err == nil {
		t.Fatalf("Split: expected error at SplitDepth == MaxSplitDepth, got nil")
	}

	var mse *MaxSplitDepthExceededError
	if !errors.As(err, &mse) {
		t.Fatalf("Split: expected *MaxSplitDepthExceededError, got %T: %v", err, err)
	}
	if mse.Depth != MaxSplitDepth || mse.MaxSplitDepth != MaxSplitDepth {
		t.Fatalf("Split: expected Depth=%d MaxSplitDepth=%d, got Depth=%d MaxSplitDepth=%d",
			MaxSplitDepth, MaxSplitDepth, mse.Depth, mse.MaxSplitDepth)
	}
	if !errors.Is(err, ErrMaxSplitDepthExceeded) {
		t.Fatalf("Split: expected errors.Is(err, ErrMaxSplitDepthExceeded), got %v", err)
	}
}

func TestSplit_RefusesBeyondMaxDepth(t *testing.T) {
	_, err := Split([]Task{"1", "2"}, SplitOptions{SplitDepth: MaxSplitDepth + 5})
	if err == nil {
		t.Fatalf("Split: expected error when SplitDepth exceeds MaxSplitDepth, got nil")
	}
	if !errors.Is(err, ErrMaxSplitDepthExceeded) {
		t.Fatalf("Split: expected errors.Is(err, ErrMaxSplitDepthExceeded), got %v", err)
	}
}

func TestSplit_AllowsUpToMaxDepth(t *testing.T) {
	waves, err := Split([]Task{"1", "2"}, SplitOptions{SplitDepth: MaxSplitDepth - 1})
	if err != nil {
		t.Fatalf("Split: unexpected error at SplitDepth == MaxSplitDepth-1: %v", err)
	}
	for _, w := range waves {
		if w.Depth != MaxSplitDepth {
			t.Fatalf("Split: expected resulting Depth %d, got %d", MaxSplitDepth, w.Depth)
		}
	}
}

func TestSplit_DeterministicSortedMidpointSplit(t *testing.T) {
	tasks := []Task{"5", "3", "1", "4", "2"} // unsorted, 5 elements -> ceil(5/2)=3/2
	waves, err := Split(tasks, SplitOptions{SplitDepth: 0})
	if err != nil {
		t.Fatalf("Split: unexpected error: %v", err)
	}
	if len(waves) != 2 {
		t.Fatalf("Split: expected exactly 2 waves, got %d", len(waves))
	}

	wantFirst := []Task{"1", "2", "3"}
	wantSecond := []Task{"4", "5"}
	if !reflect.DeepEqual(waves[0].Tasks, wantFirst) {
		t.Fatalf("Split: first half = %v, want %v", waves[0].Tasks, wantFirst)
	}
	if !reflect.DeepEqual(waves[1].Tasks, wantSecond) {
		t.Fatalf("Split: second half = %v, want %v", waves[1].Tasks, wantSecond)
	}
	if waves[0].Depth != 1 || waves[1].Depth != 1 {
		t.Fatalf("Split: expected both halves at depth 1, got %d and %d", waves[0].Depth, waves[1].Depth)
	}
}

func TestSplit_EmptyTasks(t *testing.T) {
	waves, err := Split([]Task{}, SplitOptions{SplitDepth: 0})
	if err != nil {
		t.Fatalf("Split: unexpected error: %v", err)
	}
	if len(waves) != 2 || len(waves[0].Tasks) != 0 || len(waves[1].Tasks) != 0 {
		t.Fatalf("Split: expected two empty halves, got %+v", waves)
	}
}

func TestSplit_SingleTask(t *testing.T) {
	waves, err := Split([]Task{"only"}, SplitOptions{SplitDepth: 0})
	if err != nil {
		t.Fatalf("Split: unexpected error: %v", err)
	}
	if len(waves) != 2 {
		t.Fatalf("Split: expected exactly 2 waves, got %d", len(waves))
	}
	if !reflect.DeepEqual(waves[0].Tasks, []Task{"only"}) {
		t.Fatalf("Split: first half = %v, want [only]", waves[0].Tasks)
	}
	if len(waves[1].Tasks) != 0 {
		t.Fatalf("Split: second half should be empty, got %v", waves[1].Tasks)
	}
}

func TestSplit_MissingIDsDoesNotAffectBoundary(t *testing.T) {
	tasks := []Task{"1", "2", "3", "4"}
	waves, err := Split(tasks, SplitOptions{SplitDepth: 0, MissingIDs: []Task{"1"}})
	if err != nil {
		t.Fatalf("Split: unexpected error: %v", err)
	}
	wantFirst := []Task{"1", "2"}
	wantSecond := []Task{"3", "4"}
	if !reflect.DeepEqual(waves[0].Tasks, wantFirst) || !reflect.DeepEqual(waves[1].Tasks, wantSecond) {
		t.Fatalf("Split: MissingIDs altered the split boundary: got %v / %v", waves[0].Tasks, waves[1].Tasks)
	}
}

// --- ValidStatuses / Summarize ------------------------------------------

func TestValidStatuses_ClosedEnum(t *testing.T) {
	want := map[string]bool{
		"DONE":               true,
		"DONE_WITH_CONCERNS": true,
		"NEEDS_CONTEXT":      true,
		"BLOCKED":            true,
		"FAILED":             true,
	}
	if !reflect.DeepEqual(ValidStatuses, want) {
		t.Fatalf("ValidStatuses = %v, want exactly %v", ValidStatuses, want)
	}
}

func TestSummarize_AcceptsAllValidStatuses(t *testing.T) {
	var results []StepResult
	for status := range ValidStatuses {
		results = append(results, StepResult{ID: status, Status: status})
	}

	summary, err := Summarize(results)
	if err != nil {
		t.Fatalf("Summarize: unexpected error: %v", err)
	}
	if len(summary.Violations) != 0 {
		t.Fatalf("Summarize: expected no violations for valid statuses, got %v", summary.Violations)
	}
}

func TestSummarize_RejectsStatusOutsideEnum(t *testing.T) {
	cases := []string{"done", "IN_PROGRESS", "", "SUCCESS", "COMPLETE"}
	for _, status := range cases {
		results := []StepResult{{ID: "1", Status: status}}
		summary, err := Summarize(results)
		if err != nil {
			t.Fatalf("Summarize(%q): unexpected error: %v", status, err)
		}
		if len(summary.Violations) != 1 {
			t.Fatalf("Summarize(%q): expected exactly one violation, got %v", status, summary.Violations)
		}
	}
}

func TestSummarize_PassesResultsThrough(t *testing.T) {
	results := []StepResult{{ID: "1", Status: "DONE"}, {ID: "2", Status: "FAILED"}}
	summary, err := Summarize(results)
	if err != nil {
		t.Fatalf("Summarize: unexpected error: %v", err)
	}
	if !reflect.DeepEqual(summary.Results, results) {
		t.Fatalf("Summarize: Results = %v, want %v", summary.Results, results)
	}
}
