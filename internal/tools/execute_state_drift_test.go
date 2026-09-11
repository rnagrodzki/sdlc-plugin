package tools

import (
	"path/filepath"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// drift-log action
// ---------------------------------------------------------------------------

func TestExecState_DriftLog_InvalidSeverity(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	createExecState(t, root, "feat/drift", map[string]any{
		"branch":     "feat/drift",
		"totalTasks": 10,
	})
	clock := fixedClock(testNow)

	_, err := executeState(root, root, ExecuteStateIn{
		Action:        "drift-log",
		Branch:        "feat/drift",
		DriftSeverity: "critical",
		DriftSummary:  "something broke",
	}, clock)
	if err == nil {
		t.Fatal("expected error for invalid severity")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
}

func TestExecState_DriftLog_EmptySummary(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	createExecState(t, root, "feat/drift", map[string]any{
		"branch":     "feat/drift",
		"totalTasks": 10,
	})
	clock := fixedClock(testNow)

	_, err := executeState(root, root, ExecuteStateIn{
		Action:        "drift-log",
		Branch:        "feat/drift",
		DriftSeverity: "warning",
		DriftSummary:  "   ",
	}, clock)
	if err == nil {
		t.Fatal("expected error for empty summary")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
}

func TestExecState_DriftLog_BelowThreshold(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	createExecState(t, root, "feat/drift", map[string]any{
		"branch":     "feat/drift",
		"totalTasks": 10,
	})
	clock := fixedClock(testNow)

	// Log one error — threshold with defaults: max(2, ceil(0.15*10))=max(2,2)=2.
	result, err := executeState(root, root, ExecuteStateIn{
		Action:        "drift-log",
		Branch:        "feat/drift",
		DriftSeverity: "error",
		DriftSummary:  "test drift",
		DriftDetail:   "detail here",
		Wave:          intPtr(1),
		TaskID:        "3",
	}, clock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(DriftLogOut)
	if !ok {
		t.Fatalf("expected DriftLogOut, got %T", result)
	}
	if !out.Logged {
		t.Error("expected Logged=true")
	}
	if out.Halt {
		t.Error("expected Halt=false below threshold")
	}
	if out.Threshold != 2 {
		t.Errorf("expected threshold=2, got %d", out.Threshold)
	}
	if out.DriftCount["error"] != 1 {
		t.Errorf("expected driftCount[error]=1, got %d", out.DriftCount["error"])
	}

	// Verify issue persisted to state.
	data := readExecState(t, root, "feat/drift")
	issues, _ := data["issues"].([]any)
	if len(issues) == 0 {
		t.Fatal("expected at least one issue in state")
	}
}

func TestExecState_DriftLog_RateTermDominates(t *testing.T) {
	// totalTasks=20 with defaults: ceil(0.15*20)=3 > floor 2, threshold=3.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	createExecState(t, root, "feat/drift", map[string]any{
		"branch":     "feat/drift",
		"totalTasks": 20,
	})
	clock := fixedClock(testNow)

	// Log one error — should be below threshold of 3.
	result, err := executeState(root, root, ExecuteStateIn{
		Action:        "drift-log",
		Branch:        "feat/drift",
		DriftSeverity: "error",
		DriftSummary:  "drift 1",
	}, clock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := result.(DriftLogOut)
	if out.Threshold != 3 {
		t.Errorf("expected threshold=3 (rate term dominates), got %d", out.Threshold)
	}
	if out.Halt {
		t.Error("expected Halt=false, only 1 error vs threshold 3")
	}
}

func TestExecState_DriftLog_ExactlyAtThreshold_NoHalt(t *testing.T) {
	// Boundary: error count == threshold must NOT halt.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	createExecState(t, root, "feat/drift", map[string]any{
		"branch":     "feat/drift",
		"totalTasks": 10,
	})
	clock := fixedClock(testNow)

	// Threshold = max(2, ceil(0.15*10)) = 2. Log 2 errors to hit exactly.
	for i := 0; i < 2; i++ {
		_, err := executeState(root, root, ExecuteStateIn{
			Action:        "drift-log",
			Branch:        "feat/drift",
			DriftSeverity: "error",
			DriftSummary:  "drift error",
		}, clock)
		if err != nil {
			t.Fatalf("unexpected error on call %d: %v", i, err)
		}
	}

	// Read final state to verify count.
	data := readExecState(t, root, "feat/drift")
	counts := countDriftIssues(data)
	if counts["error"] != 2 {
		t.Fatalf("expected 2 errors, got %d", counts["error"])
	}

	// The second call should have returned halt=false (2 == threshold 2).
	// Re-invoke to check a 2-error state: log an info so we can inspect
	// without adding another error.
	result, err := executeState(root, root, ExecuteStateIn{
		Action:        "drift-log",
		Branch:        "feat/drift",
		DriftSeverity: "info",
		DriftSummary:  "just info",
	}, clock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := result.(DriftLogOut)
	if out.Halt {
		t.Error("expected Halt=false when error count equals threshold")
	}
	if out.DriftCount["error"] != 2 {
		t.Errorf("expected driftCount[error]=2, got %d", out.DriftCount["error"])
	}
}

func TestExecState_DriftLog_HaltOnExceed(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	createExecState(t, root, "feat/drift", map[string]any{
		"branch":     "feat/drift",
		"totalTasks": 10,
	})
	clock := fixedClock(testNow)

	// Threshold = 2. Log 3 errors to exceed.
	var lastResult any
	for i := 0; i < 3; i++ {
		result, err := executeState(root, root, ExecuteStateIn{
			Action:        "drift-log",
			Branch:        "feat/drift",
			DriftSeverity: "error",
			DriftSummary:  "drift error",
			TaskID:        "t1",
			Wave:          intPtr(1),
		}, clock)
		if err != nil {
			t.Fatalf("unexpected error on call %d: %v", i, err)
		}
		lastResult = result
	}

	out, ok := lastResult.(DriftLogOut)
	if !ok {
		t.Fatalf("expected DriftLogOut, got %T", lastResult)
	}
	if !out.Halt {
		t.Error("expected Halt=true when error count exceeds threshold")
	}
	if !out.Logged {
		t.Error("expected Logged=true even on halt")
	}
	if out.Threshold != 2 {
		t.Errorf("expected threshold=2, got %d", out.Threshold)
	}
	if out.DriftCount["error"] != 3 {
		t.Errorf("expected driftCount[error]=3, got %d", out.DriftCount["error"])
	}
	if out.Reason == "" {
		t.Error("expected non-empty Reason on halt")
	}

	// Verify all 3 issues persisted.
	data := readExecState(t, root, "feat/drift")
	issues, _ := data["issues"].([]any)
	if len(issues) != 3 {
		t.Errorf("expected 3 issues in state, got %d", len(issues))
	}
}

func TestExecState_DriftLog_WarningsDoNotTriggerHalt(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	createExecState(t, root, "feat/drift", map[string]any{
		"branch":     "feat/drift",
		"totalTasks": 10,
	})
	clock := fixedClock(testNow)

	// Log 5 warnings — only errors count toward halt threshold.
	var lastResult any
	for i := 0; i < 5; i++ {
		result, err := executeState(root, root, ExecuteStateIn{
			Action:        "drift-log",
			Branch:        "feat/drift",
			DriftSeverity: "warning",
			DriftSummary:  "some warning",
		}, clock)
		if err != nil {
			t.Fatalf("unexpected error on call %d: %v", i, err)
		}
		lastResult = result
	}

	out := lastResult.(DriftLogOut)
	if out.Halt {
		t.Error("warnings should not trigger halt")
	}
	if out.DriftCount["warning"] != 5 {
		t.Errorf("expected driftCount[warning]=5, got %d", out.DriftCount["warning"])
	}
	if out.DriftCount["error"] != 0 {
		t.Errorf("expected driftCount[error]=0, got %d", out.DriftCount["error"])
	}
}

func TestExecState_DriftLog_ConfigWiring(t *testing.T) {
	// Verify drift-log actually reads DriftConfig from config.json rather than
	// always falling back to compiled defaults.
	// Custom config: maxErrorRate=0.5, minErrorFloor=1 → threshold = max(1, ceil(0.5*10)) = 5.
	// Automation section lives in local.json, not config.json.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	writeFile(t, filepath.Join(root, paths.DataDir, "local.json"), `{
		"automation": {
			"drift": {
				"maxErrorRate": 0.5,
				"minErrorFloor": 1
			}
		}
	}`)
	createExecState(t, root, "feat/drift", map[string]any{
		"branch":     "feat/drift",
		"totalTasks": 10,
	})
	clock := fixedClock(testNow)

	result, err := executeState(root, root, ExecuteStateIn{
		Action:        "drift-log",
		Branch:        "feat/drift",
		DriftSeverity: "error",
		DriftSummary:  "test drift",
	}, clock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := result.(DriftLogOut)
	if out.Threshold != 5 {
		t.Errorf("expected threshold=5 from custom config (0.5*10), got %d", out.Threshold)
	}
}

func TestCountDriftIssues(t *testing.T) {
	data := map[string]any{
		"issues": []any{
			map[string]any{"category": "drift", "severity": "error"},
			map[string]any{"category": "drift", "severity": "warning"},
			map[string]any{"category": "drift", "severity": "info"},
			map[string]any{"category": "drift", "severity": "error"},
			map[string]any{"category": "other", "severity": "error"},
			map[string]any{"category": "drift", "severity": "warning"},
		},
	}
	counts := countDriftIssues(data)
	if counts["error"] != 2 {
		t.Errorf("expected error=2, got %d", counts["error"])
	}
	if counts["warning"] != 2 {
		t.Errorf("expected warning=2, got %d", counts["warning"])
	}
	if counts["info"] != 1 {
		t.Errorf("expected info=1, got %d", counts["info"])
	}
}

func TestCountDriftIssues_Empty(t *testing.T) {
	counts := countDriftIssues(map[string]any{})
	if counts["error"] != 0 || counts["warning"] != 0 || counts["info"] != 0 {
		t.Errorf("expected all zeros, got %v", counts)
	}
}
