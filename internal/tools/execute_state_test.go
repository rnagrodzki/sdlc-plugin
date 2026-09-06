package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// fixedClock returns a clock function that always returns the same time.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

var testNow = time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

// createExecState creates an execute state file with the given data for tests.
func createExecState(t *testing.T, root, branch string, data map[string]any) {
	t.Helper()
	st, err := state.Init(root, "execute", branch, "")
	if err != nil {
		t.Fatalf("create test state: %v", err)
	}
	for k, v := range data {
		st.Data[k] = v
	}
	if err := state.Write(st); err != nil {
		t.Fatalf("write test state: %v", err)
	}
}

// readExecState reads an execute state file's data.
func readExecState(t *testing.T, root, branch string) map[string]any {
	t.Helper()
	st, err := state.Find(root, "execute", branch)
	if err != nil {
		t.Fatalf("find state: %v", err)
	}
	if st == nil {
		t.Fatal("state file not found")
	}
	return st.Data
}

// ---------------------------------------------------------------------------
// init
// ---------------------------------------------------------------------------

func TestExecState_Init(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	result, err := executeState(root, root, ExecuteStateIn{
		Action:         "init",
		Branch:         "feat/test",
		Quality:        "standard",
		TotalTasks:     5,
		PlannedTaskIds: []string{"T1", "T2", "T3"},
		PlanPath:       "plan.md",
		PlanHash:       "abc123",
		SessionID:      "sess-1",
	}, clock)
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map result")
	}
	if _, ok := m["filePath"].(string); !ok {
		t.Fatal("expected filePath string in result")
	}

	data := readExecState(t, root, "feat/test")
	if data["quality"] != "standard" {
		t.Errorf("quality = %v, want standard", data["quality"])
	}
	if data["skill"] != "execute-plan-sdlc" {
		t.Errorf("skill = %v, want execute-plan-sdlc", data["skill"])
	}
	if data["planPath"] != "plan.md" {
		t.Errorf("planPath = %v, want plan.md", data["planPath"])
	}
}

func TestExecState_Init_MissingBranch(t *testing.T) {
	root := t.TempDir()
	_, err := executeState(root, root, ExecuteStateIn{
		Action:  "init",
		Quality: "standard",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for missing branch")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected DomainError, got %T", err)
	}
}

func TestExecState_Init_MissingQuality(t *testing.T) {
	root := t.TempDir()
	_, err := executeState(root, root, ExecuteStateIn{
		Action: "init",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for missing quality")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected DomainError, got %T", err)
	}
}

// ---------------------------------------------------------------------------
// wave < 1 guard (parity with JS --wave required)
// ---------------------------------------------------------------------------

func TestExecState_WaveZeroRejected(t *testing.T) {
	root := t.TempDir()
	createExecState(t, root, "main", map[string]any{})

	actions := []string{"wave-start", "wave-done", "wave-fail", "wave-committed", "task-done", "task-fail"}
	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			_, err := executeState(root, root, ExecuteStateIn{
				Action: action,
				Branch: "main",
				Wave:   0,
				TaskID: "T1", // needed for task-done/task-fail
			}, fixedClock(testNow))
			if err == nil {
				t.Fatal("expected error for wave=0")
			}
			de, ok := err.(*mcpserver.DomainError)
			if !ok {
				t.Fatalf("expected DomainError, got %T: %v", err, err)
			}
			if de.Msg != "--wave is required" {
				t.Errorf("msg = %q, want %q", de.Msg, "--wave is required")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// wave-start
// ---------------------------------------------------------------------------

func TestExecState_WaveStart(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-start",
		Branch: "feat/test",
		Wave:   1,
	}, clock)
	if err != nil {
		t.Fatalf("wave-start: %v", err)
	}

	data := readExecState(t, root, "feat/test")
	waves, _ := data["waves"].([]any)
	if len(waves) != 1 {
		t.Fatalf("expected 1 wave, got %d", len(waves))
	}
	w := waves[0].(map[string]any)
	if w["status"] != "in_progress" {
		t.Errorf("wave status = %v, want in_progress", w["status"])
	}
}

func TestExecState_WaveStart_WithTasks(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"startedAt": testNow.UTC().Format(time.RFC3339),
		"waves":     []any{},
		"context":   map[string]any{},
	})

	tasksJSON := `[{"id":"T1","name":"Task One","description":"Do thing"}]`

	result, err := executeState(root, root, ExecuteStateIn{
		Action:    "wave-start",
		Branch:    "feat/test",
		Wave:      1,
		TasksJSON: tasksJSON,
		RunID:     "test-run-1",
	}, clock)
	if err != nil {
		t.Fatalf("wave-start with tasks: %v", err)
	}

	m := result.(map[string]any)
	if m["runId"] != "test-run-1" {
		t.Errorf("runId = %v, want test-run-1", m["runId"])
	}
	sheets, ok := m["factSheets"].([]string)
	if !ok || len(sheets) == 0 {
		t.Error("expected factSheets in result")
	}
}

func TestExecState_WaveStart_MissingState(t *testing.T) {
	root := t.TempDir()
	// Create the state dir but no state file.
	os.MkdirAll(filepath.Join(root, ".sdlc", "execution"), 0o755)

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-start",
		Branch: "feat/test",
		Wave:   1,
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for missing state")
	}
	if _, ok := err.(*mcpserver.DataError); !ok {
		t.Errorf("expected DataError, got %T", err)
	}
}

// ---------------------------------------------------------------------------
// wave-done
// ---------------------------------------------------------------------------

func TestExecState_WaveDone(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "in_progress", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	decisions := `["Use REST API for integration"]`

	_, err := executeState(root, root, ExecuteStateIn{
		Action:    "wave-done",
		Branch:    "feat/test",
		Wave:      1,
		Status:    "completed",
		Decisions: decisions,
	}, clock)
	if err != nil {
		t.Fatalf("wave-done: %v", err)
	}

	data := readExecState(t, root, "feat/test")
	w := data["waves"].([]any)[0].(map[string]any)
	if w["status"] != "completed" {
		t.Errorf("wave status = %v, want completed", w["status"])
	}
	if _, ok := w["completedAt"]; !ok {
		t.Error("expected completedAt on wave")
	}

	ctx := data["context"].(map[string]any)
	decs, _ := ctx["decisionsFromPriorWaves"].([]any)
	if len(decs) != 1 || decs[0] != "Use REST API for integration" {
		t.Errorf("decisions = %v, want [Use REST API for integration]", decs)
	}
}

func TestExecState_WaveDone_InvalidDecisionsJSON(t *testing.T) {
	root := t.TempDir()
	// Parse error comes BEFORE state lookup.
	_, err := executeState(root, root, ExecuteStateIn{
		Action:    "wave-done",
		Branch:    "feat/test",
		Wave:      1,
		Decisions: "not json",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for invalid decisions JSON")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected DomainError (arg error before state lookup), got %T", err)
	}
}

// ---------------------------------------------------------------------------
// wave-fail
// ---------------------------------------------------------------------------

func TestExecState_WaveFail(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "in_progress", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-fail",
		Branch: "feat/test",
		Wave:   1,
	}, clock)
	if err != nil {
		t.Fatalf("wave-fail: %v", err)
	}

	data := readExecState(t, root, "feat/test")
	w := data["waves"].([]any)[0].(map[string]any)
	if w["status"] != "failed" {
		t.Errorf("wave status = %v, want failed", w["status"])
	}
}

// ---------------------------------------------------------------------------
// wave-committed
// ---------------------------------------------------------------------------

func TestExecState_WaveCommitted(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "completed", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-committed",
		Branch: "feat/test",
		Wave:   1,
		SHA:    "abc123",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("wave-committed: %v", err)
	}

	data := readExecState(t, root, "feat/test")
	w := data["waves"].([]any)[0].(map[string]any)
	if w["committedSha"] != "abc123" {
		t.Errorf("committedSha = %v, want abc123", w["committedSha"])
	}
}

func TestExecState_WaveCommitted_Idempotent(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{
				"number":       1,
				"status":       "completed",
				"committedSha": "abc123",
				"tasks":        []any{},
			},
		},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-committed",
		Branch: "feat/test",
		Wave:   1,
		SHA:    "abc123",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("idempotent commit should not error: %v", err)
	}
}

func TestExecState_WaveCommitted_Conflict(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{
				"number":       1,
				"status":       "completed",
				"committedSha": "abc123",
				"tasks":        []any{},
			},
		},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-committed",
		Branch: "feat/test",
		Wave:   1,
		SHA:    "def456",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for sha conflict")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected DomainError for sha conflict, got %T", err)
	}
}

func TestExecState_WaveCommitted_NotCompleted(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "in_progress", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-committed",
		Branch: "feat/test",
		Wave:   1,
		SHA:    "abc123",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for non-completed wave")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected DomainError, got %T", err)
	}
}

// ---------------------------------------------------------------------------
// task-done
// ---------------------------------------------------------------------------

func TestExecState_TaskDone(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "in_progress", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action:       "task-done",
		Branch:       "feat/test",
		Wave:         1,
		TaskID:       "T1",
		TaskName:     "Task One",
		FilesChanged: `["src/a.go","src/b.go"]`,
		FilesAdded:   `["src/a.go"]`,
	}, clock)
	if err != nil {
		t.Fatalf("task-done: %v", err)
	}

	data := readExecState(t, root, "feat/test")
	w := data["waves"].([]any)[0].(map[string]any)
	tasks := w["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	task := tasks[0].(map[string]any)
	if task["status"] != "completed" {
		t.Errorf("task status = %v, want completed", task["status"])
	}

	// Check context updated.
	ctx := data["context"].(map[string]any)
	fa := ctx["filesAdded"].([]any)
	fm := ctx["filesModified"].([]any)
	if len(fa) != 1 || fa[0] != "src/a.go" {
		t.Errorf("filesAdded = %v, want [src/a.go]", fa)
	}
	if len(fm) != 1 || fm[0] != "src/b.go" {
		t.Errorf("filesModified = %v, want [src/b.go]", fm)
	}

	ct := ctx["completedTaskIds"].([]any)
	if len(ct) != 1 || ct[0] != "T1" {
		t.Errorf("completedTaskIds = %v, want [T1]", ct)
	}
}

func TestExecState_TaskDone_FilesAddedSubsetViolation(t *testing.T) {
	root := t.TempDir()
	// Validation before state lookup: state not needed.
	_, err := executeState(root, root, ExecuteStateIn{
		Action:       "task-done",
		Branch:       "feat/test",
		TaskID:       "T1",
		FilesChanged: `["src/a.go"]`,
		FilesAdded:   `["src/b.go"]`,
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for filesAdded not subset of filesChanged")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected DomainError, got %T", err)
	}
}

// ---------------------------------------------------------------------------
// task-fail
// ---------------------------------------------------------------------------

func TestExecState_TaskFail(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "in_progress", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action:    "task-fail",
		Branch:    "feat/test",
		Wave:      1,
		TaskID:    "T1",
		ErrorText: "compilation error",
	}, clock)
	if err != nil {
		t.Fatalf("task-fail: %v", err)
	}

	data := readExecState(t, root, "feat/test")
	w := data["waves"].([]any)[0].(map[string]any)
	tasks := w["tasks"].([]any)
	task := tasks[0].(map[string]any)
	if task["status"] != "failed" {
		t.Errorf("task status = %v, want failed", task["status"])
	}
}

func TestExecState_TaskFail_SkippedDependency(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "in_progress", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action:     "task-fail",
		Branch:     "feat/test",
		Wave:       1,
		TaskID:     "T1",
		SkippedDep: true,
	}, clock)
	if err != nil {
		t.Fatalf("task-fail: %v", err)
	}

	data := readExecState(t, root, "feat/test")
	w := data["waves"].([]any)[0].(map[string]any)
	tasks := w["tasks"].([]any)
	task := tasks[0].(map[string]any)
	if task["status"] != "skipped-dependency" {
		t.Errorf("task status = %v, want skipped-dependency", task["status"])
	}
}

// ---------------------------------------------------------------------------
// context
// ---------------------------------------------------------------------------

func TestExecState_Context(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{},
		"context": map[string]any{
			"filesAdded": []any{"old.go"},
		},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "context",
		Branch: "feat/test",
		Data:   `{"filesAdded":["new.go"],"planSummary":"Updated plan"}`,
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("context: %v", err)
	}

	data := readExecState(t, root, "feat/test")
	ctx := data["context"].(map[string]any)

	// Arrays concatenated (deep merge).
	fa := ctx["filesAdded"].([]any)
	if len(fa) != 2 {
		t.Errorf("expected 2 filesAdded, got %d", len(fa))
	}

	// planSummary overwritten.
	if ctx["planSummary"] != "Updated plan" {
		t.Errorf("planSummary = %v, want Updated plan", ctx["planSummary"])
	}
}

func TestExecState_Context_UnknownKey(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "context",
		Branch: "feat/test",
		Data:   `{"unknownKey":["value"]}`,
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for unknown context key")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected DomainError, got %T", err)
	}
}

// ---------------------------------------------------------------------------
// read
// ---------------------------------------------------------------------------

func TestExecState_Read(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"quality": "thorough",
		"waves":   []any{},
		"context": map[string]any{},
	})

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "read",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map result")
	}
	if m["quality"] != "thorough" {
		t.Errorf("quality = %v, want thorough", m["quality"])
	}
}

func TestExecState_Read_Missing(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".sdlc", "execution"), 0o755)

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "read",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for missing state")
	}
	if _, ok := err.(*mcpserver.DataError); !ok {
		t.Errorf("expected DataError, got %T", err)
	}
}

// ---------------------------------------------------------------------------
// cleanup
// ---------------------------------------------------------------------------

func TestExecState_Cleanup(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "cleanup",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	// State file should be deleted.
	st, _ := state.Find(root, "execute", "feat/test")
	if st != nil {
		t.Error("state file should be deleted after cleanup")
	}
}

func TestExecState_Cleanup_Missing(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".sdlc", "execution"), 0o755)

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "cleanup",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatal("cleanup of missing state should succeed")
	}
}

// ---------------------------------------------------------------------------
// summarize-prior-wave-context
// ---------------------------------------------------------------------------

func TestExecState_SummarizePriorWaveContext(t *testing.T) {
	root := t.TempDir()

	// Create lots of items to test caps.
	files := make([]any, 30)
	for i := range files {
		files[i] = "f" + string(rune('A'+i%26)) + ".go"
	}

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{},
		"context": map[string]any{
			"planSummary":   "Build API",
			"filesAdded":    files,
			"filesModified": []any{"mod1.go"},
		},
	})

	result, err := executeState(root, root, ExecuteStateIn{
		Action:   "summarize-prior-wave-context",
		Branch:   "feat/test",
		MaxFiles: 5,
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	m := result.(map[string]any)
	if m["planSummary"] != "Build API" {
		t.Errorf("planSummary = %v, want Build API", m["planSummary"])
	}

	fa := m["filesAdded"].([]string)
	if len(fa) != 5 {
		t.Errorf("expected 5 filesAdded (capped), got %d", len(fa))
	}
}

// ---------------------------------------------------------------------------
// verify-completeness
// ---------------------------------------------------------------------------

func TestExecState_VerifyCompleteness_Complete(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"plannedTaskIds": []any{"1", "2"},
		"waves": []any{
			map[string]any{
				"number": 1,
				"status": "completed",
				"tasks": []any{
					map[string]any{"id": "T1", "status": "completed"},
					map[string]any{"id": "T2", "status": "completed"},
				},
			},
		},
		"context": map[string]any{},
	})

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "verify-completeness",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("verify-completeness: %v", err)
	}

	m := result.(map[string]any)
	if m["ok"] != true {
		t.Error("expected ok=true")
	}
}

func TestExecState_VerifyCompleteness_Incomplete(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"plannedTaskIds": []any{"1", "2", "3"},
		"waves": []any{
			map[string]any{
				"number": 1,
				"status": "completed",
				"tasks": []any{
					map[string]any{"id": "T1", "status": "completed"},
				},
			},
		},
		"context": map[string]any{},
	})

	// Snapshot state file bytes before the call.
	st, _ := state.Find(root, "execute", "feat/test")
	beforeBytes, _ := os.ReadFile(st.Path)

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "verify-completeness",
		Branch: "feat/test",
	}, fixedClock(testNow))

	if err == nil {
		t.Fatal("expected error for incomplete tasks")
	}
	if _, ok := err.(*mcpserver.DataError); !ok {
		t.Errorf("expected DataError (exit 65 parity), got %T", err)
	}

	// Verify state file unchanged (data-class AC).
	afterBytes, _ := os.ReadFile(st.Path)
	if string(beforeBytes) != string(afterBytes) {
		t.Error("state file should remain untouched on data-class failure")
	}
}

func TestExecState_VerifyCompleteness_NormalizeTaskID(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"plannedTaskIds": []any{"1", "2"},
		"waves": []any{
			map[string]any{
				"number": 1,
				"status": "completed",
				"tasks": []any{
					// T-prefix stripped for matching.
					map[string]any{"id": "T1", "status": "completed"},
					map[string]any{"id": "t2", "status": "failed"},
				},
			},
		},
		"context": map[string]any{},
	})

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "verify-completeness",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("verify-completeness with normalized IDs: %v", err)
	}

	m := result.(map[string]any)
	if m["ok"] != true {
		t.Error("expected ok=true after normalizing task IDs")
	}
}

// ---------------------------------------------------------------------------
// wave-progress
// ---------------------------------------------------------------------------

func TestExecState_WaveProgress_Write(t *testing.T) {
	root := t.TempDir()

	// Create execution dir for progress files.
	os.MkdirAll(filepath.Join(root, ".sdlc", "execution"), 0o755)

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-progress",
		RunID:  "test-run",
		TaskID: "T1",
		Phase:  "editing",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("wave-progress write: %v", err)
	}
}

func TestExecState_WaveProgress_Read(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".sdlc", "execution"), 0o755)

	// Write then read.
	_, _ = executeState(root, root, ExecuteStateIn{
		Action: "wave-progress",
		RunID:  "test-run",
		TaskID: "T1",
		Phase:  "started",
	}, fixedClock(testNow))

	result, err := executeState(root, root, ExecuteStateIn{
		Action:       "wave-progress",
		RunID:        "test-run",
		ReadProgress: true,
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("wave-progress read: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil progress result")
	}
}

// ---------------------------------------------------------------------------
// resume-reset
// ---------------------------------------------------------------------------

func TestExecState_ResumeReset(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{
				"number": 1,
				"status": "completed",
				"tasks":  []any{map[string]any{"id": "T1"}},
			},
			map[string]any{
				"number": 2,
				"status": "in_progress",
				"tasks": []any{
					map[string]any{"id": "T2"},
					map[string]any{"id": "T3"},
				},
				"completedAt": "2025-06-01T00:00:00Z",
			},
		},
		"context": map[string]any{},
	})

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "resume-reset",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("resume-reset: %v", err)
	}

	m := result.(map[string]any)
	resetWaves := m["resetWaves"].([]int)
	if len(resetWaves) != 1 || resetWaves[0] != 2 {
		t.Errorf("resetWaves = %v, want [2]", resetWaves)
	}

	cleared := m["clearedTaskIds"].([]string)
	if len(cleared) != 2 {
		t.Errorf("clearedTaskIds = %v, want 2 entries", cleared)
	}

	// Wave 2 tasks should be cleared.
	data := readExecState(t, root, "feat/test")
	w2 := data["waves"].([]any)[1].(map[string]any)
	tasks := w2["tasks"].([]any)
	if len(tasks) != 0 {
		t.Error("wave 2 tasks should be cleared after resume-reset")
	}
	if _, ok := w2["completedAt"]; ok {
		t.Error("wave 2 completedAt should be removed after resume-reset")
	}

	// Wave 1 (completed) should be untouched.
	w1 := data["waves"].([]any)[0].(map[string]any)
	w1tasks := w1["tasks"].([]any)
	if len(w1tasks) != 1 {
		t.Error("wave 1 tasks should be untouched")
	}
}

func TestExecState_ResumeReset_MissingState(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".sdlc", "execution"), 0o755)

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "resume-reset",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatal("resume-reset on missing state should succeed")
	}

	m := result.(map[string]any)
	if len(m["resetWaves"].([]int)) != 0 {
		t.Error("expected empty resetWaves")
	}
}

// ---------------------------------------------------------------------------
// wave-split
// ---------------------------------------------------------------------------

func TestExecState_WaveSplit(t *testing.T) {
	root := t.TempDir()

	result, err := executeState(root, root, ExecuteStateIn{
		Action:     "wave-split",
		Dispatched: `["T1","T2","T3","T4"]`,
		SplitDepth: 0,
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("wave-split: %v", err)
	}

	m := result.(map[string]any)
	halves, ok := m["halves"].([]map[string]any)
	if !ok || len(halves) != 2 {
		t.Fatalf("expected 2 halves, got %v", m["halves"])
	}

	// Each half should have tasks and depth.
	for _, h := range halves {
		if _, ok := h["tasks"]; !ok {
			t.Error("half missing tasks")
		}
		if _, ok := h["depth"]; !ok {
			t.Error("half missing depth")
		}
	}
}

// ---------------------------------------------------------------------------
// Ledger: checkin / checkout / status round-trip
// ---------------------------------------------------------------------------

func TestExecState_Ledger_RoundTrip(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	// Checkin.
	_, err := executeState(root, root, ExecuteStateIn{
		Action:   "ledger_checkin",
		RunID:    "run-1",
		WorkerID: "worker-A",
		StepID:   "step-1",
	}, clock)
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}

	// Status (worker active).
	result, err := executeState(root, root, ExecuteStateIn{
		Action:         "ledger_status",
		RunID:          "run-1",
		TimeoutSeconds: 300,
	}, clock)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	m := result.(map[string]any)
	workers := m["workers"].([]any)
	if len(workers) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(workers))
	}
	w := workers[0].(map[string]any)
	if w["status"] != "active" {
		t.Errorf("worker status = %v, want active", w["status"])
	}
	if w["stalled"] != false {
		t.Error("worker should not be stalled yet")
	}

	// Checkout.
	_, err = executeState(root, root, ExecuteStateIn{
		Action:   "ledger_checkout",
		RunID:    "run-1",
		WorkerID: "worker-A",
	}, clock)
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}

	// Status (worker done).
	result2, _ := executeState(root, root, ExecuteStateIn{
		Action: "ledger_status",
		RunID:  "run-1",
	}, clock)
	m2 := result2.(map[string]any)
	w2 := m2["workers"].([]any)[0].(map[string]any)
	if w2["status"] != "done" {
		t.Errorf("worker status after checkout = %v, want done", w2["status"])
	}

	// Checkout preserves checkinAt.
	fp := ledgerFilePath(root, "run-1", "worker-A")
	var data map[string]any
	if err := fsx.ReadJSON(fp, &data); err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if data["checkinAt"] == nil {
		t.Error("checkout should preserve checkinAt")
	}
	if data["stepId"] != "step-1" {
		t.Errorf("checkout should preserve stepId, got %v", data["stepId"])
	}
}

// ---------------------------------------------------------------------------
// Ledger: stall detection with injected clock
// ---------------------------------------------------------------------------

func TestExecState_Ledger_StallDetection(t *testing.T) {
	root := t.TempDir()
	checkinTime := testNow
	checkinClock := fixedClock(checkinTime)

	_, err := executeState(root, root, ExecuteStateIn{
		Action:   "ledger_checkin",
		RunID:    "run-1",
		WorkerID: "worker-A",
	}, checkinClock)
	if err != nil {
		t.Fatalf("checkin: %v", err)
	}

	// Status 10 minutes later with 5-minute timeout — should be stalled.
	laterClock := fixedClock(checkinTime.Add(10 * time.Minute))
	result, err := executeState(root, root, ExecuteStateIn{
		Action:         "ledger_status",
		RunID:          "run-1",
		TimeoutSeconds: 300, // 5 minutes
	}, laterClock)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	m := result.(map[string]any)
	stalledWorkers := m["stalledWorkers"].([]string)
	if len(stalledWorkers) != 1 || stalledWorkers[0] != "worker-A" {
		t.Errorf("stalledWorkers = %v, want [worker-A]", stalledWorkers)
	}

	w := m["workers"].([]any)[0].(map[string]any)
	if w["stalled"] != true {
		t.Error("worker should be marked stalled")
	}
}

// ---------------------------------------------------------------------------
// Ledger: concurrent checkins (per-worker files, no lost update)
// ---------------------------------------------------------------------------

func TestExecState_Ledger_ConcurrentCheckins(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	const numWorkers = 5
	var wg sync.WaitGroup
	errCh := make(chan error, numWorkers)

	for i := range numWorkers {
		wg.Add(1)
		go func(workerNum int) {
			defer wg.Done()
			_, err := executeState(root, root, ExecuteStateIn{
				Action:   "ledger_checkin",
				RunID:    "concurrent-run",
				WorkerID: fmt.Sprintf("worker-%d", workerNum),
			}, clock)
			if err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent checkin error: %v", err)
	}

	// All worker files should exist.
	for i := range numWorkers {
		fp := ledgerFilePath(root, "concurrent-run", fmt.Sprintf("worker-%d", i))
		if _, err := os.Stat(fp); err != nil {
			t.Errorf("worker-%d file missing: %v", i, err)
		}
	}

	// Status should show all workers.
	result, err := executeState(root, root, ExecuteStateIn{
		Action: "ledger_status",
		RunID:  "concurrent-run",
	}, clock)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	m := result.(map[string]any)
	workers := m["workers"].([]any)
	if len(workers) != numWorkers {
		t.Errorf("expected %d workers, got %d", numWorkers, len(workers))
	}
}

// ---------------------------------------------------------------------------
// Ledger: path traversal defense
// ---------------------------------------------------------------------------

func TestExecState_Ledger_PathTraversalDefense(t *testing.T) {
	root := t.TempDir()

	_, err := executeState(root, root, ExecuteStateIn{
		Action:   "ledger_checkin",
		RunID:    "../escape",
		WorkerID: "ok",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for path traversal in runId")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected DomainError, got %T", err)
	}

	_, err = executeState(root, root, ExecuteStateIn{
		Action:   "ledger_checkin",
		RunID:    "ok",
		WorkerID: "../escape",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for path traversal in workerId")
	}
}

// ---------------------------------------------------------------------------
// Ledger: status on missing directory
// ---------------------------------------------------------------------------

func TestExecState_Ledger_StatusMissingDir(t *testing.T) {
	root := t.TempDir()

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "ledger_status",
		RunID:  "nonexistent",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("status on missing dir should succeed: %v", err)
	}

	m := result.(map[string]any)
	workers := m["workers"].([]any)
	if len(workers) != 0 {
		t.Errorf("expected empty workers, got %d", len(workers))
	}
}

// ---------------------------------------------------------------------------
// unknown action
// ---------------------------------------------------------------------------

func TestExecState_UnknownAction(t *testing.T) {
	root := t.TempDir()
	_, err := executeState(root, root, ExecuteStateIn{
		Action: "does-not-exist",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected DomainError, got %T", err)
	}
}

// ---------------------------------------------------------------------------
// Helpers: deep merge
// ---------------------------------------------------------------------------

func TestExecDeepMerge(t *testing.T) {
	target := map[string]any{
		"key1": "val1",
		"arr":  []any{"a"},
		"nested": map[string]any{
			"n1": "v1",
		},
	}
	source := map[string]any{
		"key1": "overwritten",
		"arr":  []any{"b"},
		"nested": map[string]any{
			"n2": "v2",
		},
		"new": "value",
	}

	result := execDeepMerge(target, source).(map[string]any)

	if result["key1"] != "overwritten" {
		t.Error("scalar should overwrite")
	}
	arr := result["arr"].([]any)
	if len(arr) != 2 || arr[0] != "a" || arr[1] != "b" {
		t.Errorf("arrays should concatenate: %v", arr)
	}
	nested := result["nested"].(map[string]any)
	if nested["n1"] != "v1" || nested["n2"] != "v2" {
		t.Errorf("nested maps should merge: %v", nested)
	}
	if result["new"] != "value" {
		t.Error("new keys should appear")
	}
}

// ---------------------------------------------------------------------------
// Helpers: normalize task ID
// ---------------------------------------------------------------------------

func TestExecNormalizeTaskID(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"T1", "1"},
		{"t2", "2"},
		{"T10", "10"},
		{"ABC", "ABC"},
		{"Tabc", "Tabc"},
		{"1", "1"},
	}
	for _, tt := range tests {
		got := execNormalizeTaskID(tt.input)
		if got != tt.want {
			t.Errorf("normalizeTaskID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// gc: TTLDays=0 must pass through as immediate cutoff (Wave 10 fix)
// ---------------------------------------------------------------------------

func TestExecState_GC_TTLDaysZeroPassthrough(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, ".sdlc", "execution")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a state file old enough to be GC-eligible with any positive TTL
	// but should be deleted immediately when TTL=0.
	createExecState(t, root, "stale-branch", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
	})

	// TTLDays=nil (not provided) should use config/default (7 days),
	// keeping the just-created file.
	result, err := executeState(root, root, ExecuteStateIn{
		Action:  "gc",
		TTLDays: nil, // not provided
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("gc with nil TTLDays: %v", err)
	}
	m := result.(map[string]any)
	if m["ttlDays"] != 7 {
		t.Errorf("nil TTLDays should resolve to default 7, got %v", m["ttlDays"])
	}

	// TTLDays=ptr(0) (explicitly zero) must pass through as immediate cutoff,
	// NOT fall through to the 7-day default. This is the Wave 10 fix:
	// "state.GC's TTL:0 now means immediate cutoff — pass it through
	// literally, never re-add a zero-value guard."
	result2, err := executeState(root, root, ExecuteStateIn{
		Action:  "gc",
		TTLDays: intPtr(0),
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("gc with TTLDays=0: %v", err)
	}
	m2 := result2.(map[string]any)
	if m2["ttlDays"] != 0 {
		t.Errorf("explicit TTLDays=0 should pass through as 0, got %v (zero-value guard re-added)", m2["ttlDays"])
	}
}
