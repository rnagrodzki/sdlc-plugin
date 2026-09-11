package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
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
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
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
	if data["skill"] != "execute" {
		t.Errorf("skill = %v, want execute", data["skill"])
	}
	if data["planPath"] != "plan.md" {
		t.Errorf("planPath = %v, want plan.md", data["planPath"])
	}

	if m["migration"] != nil {
		t.Errorf("migration = %v, want nil for already-current config", m["migration"])
	}
	if _, statErr := os.Stat(filepath.Join(root, paths.DataDir, "config.json.bak")); statErr == nil {
		t.Error("config.json.bak written for already-current config; want zero extra I/O")
	}
}

// TestExecState_Init_MigratesStaleConfig covers the KD5 gate: init on a
// stale-but-migratable config auto-migrates in place (via
// configmigrate.MigrateWithBackup) instead of hard-failing, and surfaces the
// migration in the result.
func TestExecState_Init_MigratesStaleConfig(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{"schemaVersion": 4}`)
	clock := fixedClock(testNow)

	result, err := executeState(root, root, ExecuteStateIn{
		Action:  "init",
		Branch:  "feat/test",
		Quality: "standard",
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
	migration, ok := m["migration"].(*MigrationReport)
	if !ok || migration == nil {
		t.Fatalf("expected migration report in result, got %T: %v", m["migration"], m["migration"])
	}
	if migration.BackupPath == "" {
		t.Error("expected non-empty BackupPath in migration report")
	}
	if _, statErr := os.Stat(migration.BackupPath); statErr != nil {
		t.Errorf("backup file not found at %s: %v", migration.BackupPath, statErr)
	}

	// State was still created despite the auto-migration.
	readExecState(t, root, "feat/test")
}

// TestExecState_Init_MissingConfig covers the KD5 gate's missing-config
// case: a project that never ran /setup gets an actionable error naming
// /setup, not a silent proceed-on-defaults or a generic failure.
func TestExecState_Init_MissingConfig(t *testing.T) {
	root := t.TempDir()

	_, err := executeState(root, root, ExecuteStateIn{
		Action:  "init",
		Branch:  "feat/test",
		Quality: "standard",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if !errors.Is(err, configmigrate.ErrConfigMissing) {
		t.Errorf("expected ErrConfigMissing, got %v", err)
	}
	if !strings.Contains(err.Error(), "/setup") {
		t.Errorf("expected error to mention /setup, got %q", err.Error())
	}
	if _, ok := err.(*mcpserver.DataError); !ok {
		t.Errorf("expected DataError, got %T", err)
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
// wave nil guard (parity with JS --wave required); wave 0 is a valid
// pre-wave number and must be accepted, not rejected.
// ---------------------------------------------------------------------------

func TestExecState_WaveNilRejected(t *testing.T) {
	root := t.TempDir()
	createExecState(t, root, "main", map[string]any{})

	actions := []string{"wave-start", "wave-done", "wave-fail", "wave-committed", "wave-commit", "task-done", "task-fail"}
	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			_, err := executeState(root, root, ExecuteStateIn{
				Action: action,
				Branch: "main",
				TaskID: "T1", // needed for task-done/task-fail
			}, fixedClock(testNow))
			if err == nil {
				t.Fatal("expected error for wave=nil")
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

// TestExecState_WaveZeroAccepted confirms wave:0 (the documented pre-wave)
// is accepted by wave-start and wave-done, creating/completing a wave
// entry with number: 0 rather than being rejected as "not provided".
func TestExecState_WaveZeroAccepted(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-start",
		Branch: "feat/test",
		Wave:   intPtr(0),
	}, clock)
	if err != nil {
		t.Fatalf("wave-start wave=0: %v", err)
	}

	data := readExecState(t, root, "feat/test")
	waves, _ := data["waves"].([]any)
	if len(waves) != 1 {
		t.Fatalf("expected 1 wave, got %d", len(waves))
	}
	w := waves[0].(map[string]any)
	if w["number"] != float64(0) {
		t.Errorf("wave number = %v, want 0", w["number"])
	}
	if w["status"] != "in_progress" {
		t.Errorf("wave status = %v, want in_progress", w["status"])
	}

	_, err = executeState(root, root, ExecuteStateIn{
		Action: "wave-done",
		Branch: "feat/test",
		Wave:   intPtr(0),
		Status: "completed",
	}, clock)
	if err != nil {
		t.Fatalf("wave-done wave=0: %v", err)
	}

	data = readExecState(t, root, "feat/test")
	w = data["waves"].([]any)[0].(map[string]any)
	if w["number"] != float64(0) {
		t.Errorf("wave number = %v, want 0", w["number"])
	}
	if w["status"] != "completed" {
		t.Errorf("wave status = %v, want completed", w["status"])
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
		Wave:   intPtr(1),
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
		Wave:      intPtr(1),
		TasksJSON: tasksJSON,
		RunID:     "test-run-1",
	}, clock)
	if err != nil {
		t.Fatalf("wave-start with tasks: %v", err)
	}

	m, ok := result.(ExecWaveNarrationOut)
	if !ok {
		t.Fatalf("result = %T, want ExecWaveNarrationOut", result)
	}
	if m.RunID != "test-run-1" {
		t.Errorf("runId = %v, want test-run-1", m.RunID)
	}
	if len(m.FactSheets) == 0 {
		t.Error("expected factSheets in result")
	}
	if m.Summary == "" {
		t.Error("expected non-empty Summary in narration")
	}
	if m.Next == nil {
		t.Error("expected Next action in narration")
	}
}

func TestExecState_WaveStart_MissingState(t *testing.T) {
	root := t.TempDir()
	// Create the state dir but no state file.
	os.MkdirAll(filepath.Join(root, paths.DataDir, paths.RunsSubdir), 0o755)

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-start",
		Branch: "feat/test",
		Wave:   intPtr(1),
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
		Wave:      intPtr(1),
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
		Wave:      intPtr(1),
		Decisions: "not json",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for invalid decisions JSON")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected DomainError (arg error before state lookup), got %T", err)
	}
}

// TestExecState_WaveDone_IssueSummary confirms wave-done's response carries
// issueCount/issueHighlights once the state has recorded issues, and omits
// them (backward-compat: no key at all) when there are none.
func TestExecState_WaveDone_IssueSummary(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "in_progress", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	// No issues recorded yet: IssueCount should be 0.
	result, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-done",
		Branch: "feat/test",
		Wave:   intPtr(1),
	}, clock)
	if err != nil {
		t.Fatalf("wave-done: %v", err)
	}
	m, ok := result.(ExecWaveNarrationOut)
	if !ok {
		t.Fatalf("result = %T, want ExecWaveNarrationOut", result)
	}
	if m.IssueCount != 0 {
		t.Errorf("issueCount = %d, want 0 when no issues", m.IssueCount)
	}
	if m.Summary == "" {
		t.Error("expected non-empty Summary in narration")
	}
	if m.Next == nil {
		t.Error("expected Next action in narration")
	}

	// Fail a task, then re-complete the wave: the response must now surface
	// the accumulated issue.
	if _, err := executeState(root, root, ExecuteStateIn{
		Action:    "task-fail",
		Branch:    "feat/test",
		Wave:      intPtr(1),
		TaskID:    "T1",
		ErrorText: "boom",
	}, clock); err != nil {
		t.Fatalf("task-fail: %v", err)
	}
	result, err = executeState(root, root, ExecuteStateIn{
		Action: "wave-done",
		Branch: "feat/test",
		Wave:   intPtr(1),
		Status: "partial",
	}, clock)
	if err != nil {
		t.Fatalf("wave-done: %v", err)
	}
	m, ok = result.(ExecWaveNarrationOut)
	if !ok {
		t.Fatalf("result = %T, want ExecWaveNarrationOut", result)
	}
	if m.IssueCount != 1 {
		t.Errorf("issueCount = %v, want 1", m.IssueCount)
	}
	wantHighlight := "[error] Task T1 failed"
	if len(m.IssueHighlights) != 1 || m.IssueHighlights[0] != wantHighlight {
		t.Errorf("issueHighlights = %v, want [%q]", m.IssueHighlights, wantHighlight)
	}
}

// TestExecState_BackwardCompatNoIssuesArray confirms a state file written
// before issues[] existed still loads and accepts issue-recording actions
// without erroring — the acceptance criterion for this task.
func TestExecState_BackwardCompatNoIssuesArray(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "in_progress", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	data := readExecState(t, root, "feat/test")
	if _, ok := data["issues"]; ok {
		t.Fatal("fixture unexpectedly has an issues key")
	}

	if _, err := executeState(root, root, ExecuteStateIn{
		Action: "task-fail",
		Branch: "feat/test",
		Wave:   intPtr(1),
		TaskID: "T1",
	}, clock); err != nil {
		t.Fatalf("task-fail: %v", err)
	}

	data = readExecState(t, root, "feat/test")
	issues, _ := data["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want 1 entry", issues)
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
		Wave:   intPtr(1),
	}, clock)
	if err != nil {
		t.Fatalf("wave-fail: %v", err)
	}

	data := readExecState(t, root, "feat/test")
	w := data["waves"].([]any)[0].(map[string]any)
	if w["status"] != "failed" {
		t.Errorf("wave status = %v, want failed", w["status"])
	}
	if data["failedWave"] != float64(1) {
		t.Errorf("failedWave = %v (%T), want 1", data["failedWave"], data["failedWave"])
	}
	issues, _ := data["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want 1 entry", issues)
	}
	issue, _ := issues[0].(map[string]any)
	if issue["wave"] != float64(1) || issue["severity"] != "error" || issue["category"] != "wave-fail" {
		t.Errorf("issue = %v, want wave=1 severity=error category=wave-fail", issue)
	}
}

// TestExecState_WaveFail_TimedOutRecordsDetail confirms a timeout with no
// explicit error text still records a meaningful issue detail.
func TestExecState_WaveFail_TimedOutRecordsDetail(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "in_progress", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action:   "wave-fail",
		Branch:   "feat/test",
		Wave:     intPtr(1),
		TimedOut: true,
	}, clock)
	if err != nil {
		t.Fatalf("wave-fail: %v", err)
	}

	data := readExecState(t, root, "feat/test")
	issues, _ := data["issues"].([]any)
	issue, _ := issues[0].(map[string]any)
	if issue["detail"] != "timed out" {
		t.Errorf("issue detail = %v, want %q", issue["detail"], "timed out")
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
		Wave:   intPtr(1),
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
		Wave:   intPtr(1),
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
		Wave:   intPtr(1),
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
		Wave:   intPtr(1),
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
// wave-commit
// ---------------------------------------------------------------------------

// headSHA returns the full sha of HEAD in the git repo at dir.
func headSHA(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// commitCount returns the number of commits reachable from HEAD in the git
// repo at dir.
func commitCount(t *testing.T, dir string) int {
	t.Helper()
	cmd := exec.Command("git", "rev-list", "--count", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-list --count HEAD: %v", err)
	}
	n := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n); err != nil {
		t.Fatalf("parse commit count %q: %v", out, err)
	}
	return n
}

// seedExecStateCommitted creates a completed-wave exec state in dir (a git
// repo already carrying an initial commit) and commits the state file(s) so
// the working tree is clean before the test exercises wave-commit's git
// side effects in isolation.
func seedExecStateCommitted(t *testing.T, dir string, wave map[string]any) {
	t.Helper()
	createExecState(t, dir, "feat/test", map[string]any{
		"waves":   []any{wave},
		"context": map[string]any{},
	})
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "seed exec state")
}

func TestExecState_WaveCommit_Success(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	seedExecStateCommitted(t, dir, map[string]any{"number": 1, "status": "completed", "tasks": []any{}})

	beforeSHA := headSHA(t, dir)
	beforeCount := commitCount(t, dir)

	// Simulate wave work: one new file to be picked up by `git add -A`.
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("wave 1 work"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := executeState(dir, dir, ExecuteStateIn{
		Action:  "wave-commit",
		Branch:  "feat/test",
		Wave:    intPtr(1),
		Message: "Add narration payloads",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("wave-commit: %v", err)
	}

	out, ok := res.(ExecWaveCommitOut)
	if !ok {
		t.Fatalf("result type = %T, want ExecWaveCommitOut", res)
	}
	if !out.Committed {
		t.Error("Committed = false, want true")
	}
	if out.Idempotent {
		t.Error("Idempotent = true, want false")
	}
	if out.SHA == "" {
		t.Error("SHA is empty")
	}
	afterSHA := headSHA(t, dir)
	if out.SHA != afterSHA {
		t.Errorf("SHA = %q, want new HEAD %q", out.SHA, afterSHA)
	}
	if afterSHA == beforeSHA {
		t.Error("HEAD did not move: no commit was created")
	}
	if got, want := commitCount(t, dir), beforeCount+1; got != want {
		t.Errorf("commit count = %d, want %d", got, want)
	}
	if !strings.Contains(out.Summary, "(1 files)") {
		t.Errorf("Summary = %q, want to contain %q", out.Summary, "(1 files)")
	}
	if out.Next == nil || out.Next.ID != "wave-2" {
		t.Errorf("Next = %+v, want ID wave-2", out.Next)
	}
	if out.Next != nil && out.Next.Instruction != "Call wave-start for wave 2." {
		t.Errorf("Next.Instruction = %q", out.Next.Instruction)
	}

	// Commit message lands verbatim: no tool-added prefix.
	logCmd := exec.Command("git", "log", "-1", "--pretty=%B")
	logCmd.Dir = dir
	logOut, err := logCmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if got := strings.TrimSpace(string(logOut)); got != "Add narration payloads" {
		t.Errorf("commit message = %q, want %q", got, "Add narration payloads")
	}

	// feature.txt was staged and committed by wave-commit's own `git add -A`
	// + `git commit`, so it must not show up as a pending change afterward.
	// (The state file itself legitimately shows as modified post-commit:
	// execActionWaveCommit records committedSha via a plain state.Write
	// AFTER the git commit completes, so that bookkeeping write is never
	// part of the commit it describes — same trailing-write shape as the
	// manual wave-committed action.)
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusOut, err := statusCmd.Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if strings.Contains(string(statusOut), "feature.txt") {
		t.Errorf("feature.txt not committed, still pending: %q", statusOut)
	}

	// State records the new sha.
	data := readExecState(t, dir, "feat/test")
	w := data["waves"].([]any)[0].(map[string]any)
	if w["committedSha"] != out.SHA {
		t.Errorf("committedSha = %v, want %v", w["committedSha"], out.SHA)
	}
}

func TestExecState_WaveCommit_EmptyDiff(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	seedExecStateCommitted(t, dir, map[string]any{"number": 1, "status": "completed", "tasks": []any{}})

	beforeSHA := headSHA(t, dir)

	res, err := executeState(dir, dir, ExecuteStateIn{
		Action:  "wave-commit",
		Branch:  "feat/test",
		Wave:    intPtr(1),
		Message: "nothing changed",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("wave-commit: %v", err)
	}

	out, ok := res.(ExecWaveCommitOut)
	if !ok {
		t.Fatalf("result type = %T, want ExecWaveCommitOut", res)
	}
	if out.Committed {
		t.Error("Committed = true, want false")
	}
	if out.Reason != "nothing to commit" {
		t.Errorf("Reason = %q, want %q", out.Reason, "nothing to commit")
	}
	if got := headSHA(t, dir); got != beforeSHA {
		t.Errorf("HEAD moved to %q, want unchanged %q", got, beforeSHA)
	}
}

func TestExecState_WaveCommit_MissingMessage(t *testing.T) {
	root := t.TempDir()

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-commit",
		Branch: "feat/test",
		Wave:   intPtr(1),
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for missing message")
	}
	de, ok := err.(*mcpserver.DomainError)
	if !ok {
		t.Fatalf("expected DomainError, got %T", err)
	}
	if !strings.Contains(de.Msg, "message") {
		t.Errorf("error message = %q, want to mention message", de.Msg)
	}
}

func TestExecState_WaveCommit_BlankMessage(t *testing.T) {
	root := t.TempDir()

	_, err := executeState(root, root, ExecuteStateIn{
		Action:  "wave-commit",
		Branch:  "feat/test",
		Wave:    intPtr(1),
		Message: "   ",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for blank message")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected DomainError, got %T", err)
	}
}

func TestExecState_WaveCommit_WaveNotFound(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action:  "wave-commit",
		Branch:  "feat/test",
		Wave:    intPtr(1),
		Message: "msg",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for missing wave")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected DomainError, got %T", err)
	}
}

func TestExecState_WaveCommit_NotCompleted(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "in_progress", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action:  "wave-commit",
		Branch:  "feat/test",
		Wave:    intPtr(1),
		Message: "msg",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for non-completed wave")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected DomainError, got %T", err)
	}
}

func TestExecState_WaveCommit_CommitWavesDisabled(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "completed", "tasks": []any{}},
		},
		"context": map[string]any{},
	})
	if err := config.WriteSection(root, "execute", map[string]any{"commitWaves": false}); err != nil {
		t.Fatalf("write config: %v", err)
	}

	res, err := executeState(root, root, ExecuteStateIn{
		Action:  "wave-commit",
		Branch:  "feat/test",
		Wave:    intPtr(1),
		Message: "msg",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("wave-commit: %v", err)
	}

	out, ok := res.(ExecWaveCommitOut)
	if !ok {
		t.Fatalf("result type = %T, want ExecWaveCommitOut", res)
	}
	if out.Committed {
		t.Error("Committed = true, want false when commitWaves is disabled")
	}
	if out.Next == nil || !strings.Contains(out.Next.Instruction, "manually") {
		t.Errorf("Next = %+v, want a manual-commit instruction", out.Next)
	}

	// No committedSha should have been recorded.
	data := readExecState(t, root, "feat/test")
	w := data["waves"].([]any)[0].(map[string]any)
	if _, has := w["committedSha"]; has {
		t.Errorf("committedSha = %v, want unset", w["committedSha"])
	}
}

func TestExecState_WaveCommit_IdempotentResume(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	existingSHA := headSHA(t, dir)

	seedExecStateCommitted(t, dir, map[string]any{
		"number":       1,
		"status":       "completed",
		"committedSha": existingSHA,
		"tasks":        []any{},
	})
	// seedExecStateCommitted's own "seed exec state" commit moves HEAD past
	// existingSHA (existingSHA remains its ancestor); capture the resume
	// baseline AFTER seeding, not the pre-seed sha, so the "HEAD unchanged"
	// check below reflects what wave-commit itself is expected to leave
	// alone.
	beforeSHA := headSHA(t, dir)
	beforeCount := commitCount(t, dir)

	res, err := executeState(dir, dir, ExecuteStateIn{
		Action:  "wave-commit",
		Branch:  "feat/test",
		Wave:    intPtr(1),
		Message: "should not be used",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("wave-commit: %v", err)
	}

	out, ok := res.(ExecWaveCommitOut)
	if !ok {
		t.Fatalf("result type = %T, want ExecWaveCommitOut", res)
	}
	if !out.Committed {
		t.Error("Committed = false, want true for idempotent resume")
	}
	if !out.Idempotent {
		t.Error("Idempotent = false, want true")
	}
	if out.SHA != existingSHA {
		t.Errorf("SHA = %q, want existing %q", out.SHA, existingSHA)
	}
	if got := headSHA(t, dir); got != beforeSHA {
		t.Errorf("HEAD moved to %q, want unchanged %q", got, beforeSHA)
	}
	if got := commitCount(t, dir); got != beforeCount {
		t.Errorf("commit count = %d, want unchanged %d (no double-commit)", got, beforeCount)
	}
}

func TestExecState_WaveCommit_DivergedConflict(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	gitCommit(t, dir, "c2")
	divergedSHA := headSHA(t, dir)

	// Roll the branch back so divergedSHA is no longer an ancestor of HEAD.
	runGit(t, dir, "reset", "--hard", "HEAD~1")

	seedExecStateCommitted(t, dir, map[string]any{
		"number":       1,
		"status":       "completed",
		"committedSha": divergedSHA,
		"tasks":        []any{},
	})

	_, err := executeState(dir, dir, ExecuteStateIn{
		Action:  "wave-commit",
		Branch:  "feat/test",
		Wave:    intPtr(1),
		Message: "msg",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for diverged committedSha")
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
		Wave:         intPtr(1),
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

// TestExecState_TaskDone_DoneWithConcerns confirms status=DONE_WITH_CONCERNS
// appends a warning issue while the persisted task row still records
// status="completed" (the schema's task status enum has no
// DONE_WITH_CONCERNS member — the concern lives only in issues[]).
func TestExecState_TaskDone_DoneWithConcerns(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "in_progress", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action:    "task-done",
		Branch:    "feat/test",
		Wave:      intPtr(1),
		TaskID:    "T1",
		Status:    "DONE_WITH_CONCERNS",
		ErrorText: "left a TODO for follow-up",
	}, clock)
	if err != nil {
		t.Fatalf("task-done: %v", err)
	}

	data := readExecState(t, root, "feat/test")
	w := data["waves"].([]any)[0].(map[string]any)
	task := w["tasks"].([]any)[0].(map[string]any)
	if task["status"] != "completed" {
		t.Errorf("task status = %v, want completed (DONE_WITH_CONCERNS must not leak into the status enum)", task["status"])
	}

	issues, _ := data["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want 1 entry", issues)
	}
	issue, _ := issues[0].(map[string]any)
	if issue["taskId"] != "T1" || issue["severity"] != "warning" || issue["category"] != "done-with-concerns" {
		t.Errorf("issue = %v, want taskId=T1 severity=warning category=done-with-concerns", issue)
	}
	if issue["detail"] != "left a TODO for follow-up" {
		t.Errorf("issue detail = %v, want %q", issue["detail"], "left a TODO for follow-up")
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
		Wave:      intPtr(1),
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
	if data["failedTask"] != "T1" {
		t.Errorf("failedTask = %v, want T1", data["failedTask"])
	}
	issues, _ := data["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want 1 entry", issues)
	}
	issue, _ := issues[0].(map[string]any)
	if issue["taskId"] != "T1" || issue["severity"] != "error" || issue["category"] != "task-fail" {
		t.Errorf("issue = %v, want taskId=T1 severity=error category=task-fail", issue)
	}
	if issue["detail"] != "compilation error" {
		t.Errorf("issue detail = %v, want %q", issue["detail"], "compilation error")
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
		Wave:       intPtr(1),
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
	// A skipped-dependency task-fail is a consequence of a prior real
	// failure, not the root cause — it must not populate failedTask.
	if _, has := data["failedTask"]; has {
		t.Errorf("failedTask unexpectedly set by a skipped-dependency task-fail: %v", data["failedTask"])
	}
	issues, _ := data["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want 1 entry", issues)
	}
	issue, _ := issues[0].(map[string]any)
	if issue["severity"] != "error" || issue["category"] != "task-fail" {
		t.Errorf("issue = %v, want severity=error category=task-fail", issue)
	}
}

// TestExecState_TaskFail_SkippedDependencyDoesNotOverwriteFailedTask ensures
// that once a real failure has set failedTask, a later skipped-dependency
// task-fail (a downstream consequence, not a new root cause) leaves it
// pointing at the original failing task.
func TestExecState_TaskFail_SkippedDependencyDoesNotOverwriteFailedTask(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "in_progress", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	if _, err := executeState(root, root, ExecuteStateIn{
		Action:    "task-fail",
		Branch:    "feat/test",
		Wave:      intPtr(1),
		TaskID:    "T4",
		ErrorText: "root cause",
	}, clock); err != nil {
		t.Fatalf("task-fail T4: %v", err)
	}
	if _, err := executeState(root, root, ExecuteStateIn{
		Action:     "task-fail",
		Branch:     "feat/test",
		Wave:       intPtr(1),
		TaskID:     "T5",
		SkippedDep: true,
	}, clock); err != nil {
		t.Fatalf("task-fail T5: %v", err)
	}

	data := readExecState(t, root, "feat/test")
	if data["failedTask"] != "T4" {
		t.Errorf("failedTask = %v, want T4 (root cause, not the skipped dependent)", data["failedTask"])
	}
	issues, _ := data["issues"].([]any)
	if len(issues) != 2 {
		t.Fatalf("issues = %v, want 2 entries", issues)
	}
}

// ---------------------------------------------------------------------------
// task-context
// ---------------------------------------------------------------------------

func TestExecState_TaskContext_HappyPath(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"startedAt": testNow.UTC().Format(time.RFC3339),
		"waves":     []any{},
		"context": map[string]any{
			"planSummary":             "Build the widget API.",
			"filesAdded":              []any{"internal/widget/widget.go"},
			"filesModified":           []any{"internal/widget/registry.go"},
			"interfacesCreated":       []any{"Widget in internal/widget/widget.go"},
			"decisionsFromPriorWaves": []any{"Task 3: widgets are immutable."},
		},
	})

	// wave-start writes the fact sheet task-context will read back.
	tasksJSON := `[{"id":"1","name":"Build widget","description":"desc","contract":"WidgetNew() *Widget","acceptanceCriteria":["compiles"],"files":["internal/widget/widget.go"]}]`
	if _, err := executeState(root, root, ExecuteStateIn{
		Action:    "wave-start",
		Branch:    "feat/test",
		Wave:      intPtr(1),
		TasksJSON: tasksJSON,
		RunID:     "run-1",
	}, clock); err != nil {
		t.Fatalf("wave-start: %v", err)
	}

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "task-context",
		Branch: "feat/test",
		RunID:  "run-1",
		TaskID: "1",
	}, clock)
	if err != nil {
		t.Fatalf("task-context: %v", err)
	}

	out, ok := result.(TaskContextOut)
	if !ok {
		t.Fatalf("result = %T, want TaskContextOut", result)
	}
	if out.TaskID != "1" {
		t.Errorf("TaskID = %q, want %q", out.TaskID, "1")
	}
	for _, want := range []string{"# Task 1: Build widget", "## Contract", "WidgetNew() *Widget", "## Acceptance Criteria", "- compiles"} {
		if !strings.Contains(out.FactSheet, want) {
			t.Errorf("FactSheet missing %q; got:\n%s", want, out.FactSheet)
		}
	}
	for _, want := range []string{"Build the widget API.", "internal/widget/widget.go", "internal/widget/registry.go", "Widget in internal/widget/widget.go", "widgets are immutable"} {
		if !strings.Contains(out.PriorWaves, want) {
			t.Errorf("PriorWaves missing %q; got:\n%s", want, out.PriorWaves)
		}
	}
	if !strings.Contains(out.Verify, "task 1") {
		t.Errorf("Verify = %q, want it to name task 1", out.Verify)
	}
	if !strings.Contains(out.Verify, "VERIFY:") {
		t.Errorf("Verify = %q, want it to mention the VERIFY: canary", out.Verify)
	}
	if !strings.Contains(out.ReportBack, "wave-progress") || !strings.Contains(out.ReportBack, "task-done") {
		t.Errorf("ReportBack = %q, want wave-progress and task-done mentioned", out.ReportBack)
	}
	if !strings.Contains(out.ReportBack, `"1"`) {
		t.Errorf("ReportBack = %q, want the taskId interpolated", out.ReportBack)
	}
	if out.Truncated {
		t.Error("Truncated = true, want false for a small fact sheet")
	}
}

func TestExecState_TaskContext_NormalizesTaskID(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
	})

	tasksJSON := `[{"id":"7","name":"Task seven"}]`
	if _, err := executeState(root, root, ExecuteStateIn{
		Action:    "wave-start",
		Branch:    "feat/test",
		Wave:      intPtr(1),
		TasksJSON: tasksJSON,
		RunID:     "run-1",
	}, clock); err != nil {
		t.Fatalf("wave-start: %v", err)
	}

	// "T7" (plan-style ID) must resolve to the same fact sheet as "7".
	result, err := executeState(root, root, ExecuteStateIn{
		Action: "task-context",
		Branch: "feat/test",
		RunID:  "run-1",
		TaskID: "T7",
	}, clock)
	if err != nil {
		t.Fatalf("task-context: %v", err)
	}
	out := result.(TaskContextOut)
	if !strings.Contains(out.FactSheet, "Task seven") {
		t.Errorf("FactSheet = %q, want it to contain the task-7 fact sheet content", out.FactSheet)
	}
}

func TestExecState_TaskContext_MissingTaskID(t *testing.T) {
	root := t.TempDir()
	createExecState(t, root, "feat/test", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "task-context",
		Branch: "feat/test",
		RunID:  "run-1",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for missing taskId")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected DomainError, got %T", err)
	}
}

func TestExecState_TaskContext_UnknownTaskID_ListsValidIDs(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
	})

	tasksJSON := `[{"id":"1","name":"First"},{"id":"2","name":"Second"}]`
	if _, err := executeState(root, root, ExecuteStateIn{
		Action:    "wave-start",
		Branch:    "feat/test",
		Wave:      intPtr(1),
		TasksJSON: tasksJSON,
		RunID:     "run-1",
	}, clock); err != nil {
		t.Fatalf("wave-start: %v", err)
	}

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "task-context",
		Branch: "feat/test",
		RunID:  "run-1",
		TaskID: "99",
	}, clock)
	if err == nil {
		t.Fatal("expected error for unknown taskId")
	}
	de, ok := err.(*mcpserver.DomainError)
	if !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if !strings.Contains(de.Msg, "1") || !strings.Contains(de.Msg, "2") {
		t.Errorf("error message = %q, want it to list valid IDs 1 and 2", de.Msg)
	}
}

func TestExecState_TaskContext_UnknownRun_NoFactSheetsYet(t *testing.T) {
	root := t.TempDir()
	createExecState(t, root, "feat/test", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "task-context",
		Branch: "feat/test",
		RunID:  "never-started",
		TaskID: "1",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error when the run has no fact sheets")
	}
	de, ok := err.(*mcpserver.DomainError)
	if !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if !strings.Contains(de.Msg, "no fact sheets yet") {
		t.Errorf("error message = %q, want it to say no fact sheets yet", de.Msg)
	}
}

func TestExecState_TaskContext_MissingState(t *testing.T) {
	root := t.TempDir()
	_, err := executeState(root, root, ExecuteStateIn{
		Action: "task-context",
		Branch: "feat/no-such-branch",
		RunID:  "run-1",
		TaskID: "1",
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error when no execute state exists for the branch")
	}
	if _, ok := err.(*mcpserver.DataError); !ok {
		t.Errorf("expected DataError, got %T", err)
	}
}

func TestExecState_TaskContext_Truncation(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
	})

	// A huge acceptance-criteria list pushes the rendered fact sheet past
	// the 1 MiB cap.
	var criteria []string
	for i := 0; i < 40000; i++ {
		criteria = append(criteria, fmt.Sprintf(`"criterion number %d, padded so this fact sheet blows past the one mebibyte cap"`, i))
	}
	tasksJSON := fmt.Sprintf(`[{"id":"1","name":"Huge task","acceptanceCriteria":[%s]}]`, strings.Join(criteria, ","))
	if _, err := executeState(root, root, ExecuteStateIn{
		Action:    "wave-start",
		Branch:    "feat/test",
		Wave:      intPtr(1),
		TasksJSON: tasksJSON,
		RunID:     "run-1",
	}, clock); err != nil {
		t.Fatalf("wave-start: %v", err)
	}

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "task-context",
		Branch: "feat/test",
		RunID:  "run-1",
		TaskID: "1",
	}, clock)
	if err != nil {
		t.Fatalf("task-context: %v", err)
	}
	out := result.(TaskContextOut)
	if !out.Truncated {
		t.Fatal("Truncated = false, want true for an oversize fact sheet")
	}
	if len(out.FactSheet) > execTaskContextMaxBytes+len(execTaskContextTruncationNote) {
		t.Errorf("FactSheet length = %d, want <= cap + truncation note", len(out.FactSheet))
	}
	if !strings.HasSuffix(out.FactSheet, execTaskContextTruncationNote) {
		t.Error("FactSheet does not end with the truncation note")
	}
}

func TestExecState_TaskContext_TruncatesPriorWavesWhenItAloneOverflows(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	// A huge planSummary — not bounded by execSummarizePriorWaveCtx's
	// maxFiles/maxDecisions/maxInterfaces/maxTaskIds caps, which only bound
	// how many entries are kept, not each entry's length — can alone push
	// the serialized payload over the 1 MiB cap even though the fact sheet
	// itself stays tiny. The cap must catch this case too, not just an
	// oversize FactSheet.
	hugePlanSummary := strings.Repeat("x", 2<<20) // 2 MiB, alone over cap
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{},
		"context": map[string]any{
			"planSummary": hugePlanSummary,
		},
	})

	tasksJSON := `[{"id":"1","name":"Tiny task"}]`
	if _, err := executeState(root, root, ExecuteStateIn{
		Action:    "wave-start",
		Branch:    "feat/test",
		Wave:      intPtr(1),
		TasksJSON: tasksJSON,
		RunID:     "run-1",
	}, clock); err != nil {
		t.Fatalf("wave-start: %v", err)
	}

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "task-context",
		Branch: "feat/test",
		RunID:  "run-1",
		TaskID: "1",
	}, clock)
	if err != nil {
		t.Fatalf("task-context: %v", err)
	}
	out := result.(TaskContextOut)
	if !out.Truncated {
		t.Fatal("Truncated = false, want true for an oversize planSummary")
	}
	if !strings.HasSuffix(out.PriorWaves, execTaskContextTruncationNote) {
		t.Error("PriorWaves does not end with the truncation note")
	}
	if strings.Contains(out.FactSheet, execTaskContextTruncationNote) {
		t.Error("FactSheet was truncated, but it was never the oversize field here")
	}
	if !strings.Contains(out.FactSheet, "Tiny task") {
		t.Error("FactSheet should be intact and contain the tiny task's name")
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if len(raw) > execTaskContextMaxBytes+len(execTaskContextTruncationNote)*2 {
		t.Errorf("serialized payload length = %d, want roughly <= cap", len(raw))
	}
}

func TestExecState_TaskContext_RunIDFallsBackToDerivedID(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	// No explicit RunID passed to either call: both wave-start and
	// task-context must independently derive the same runID from
	// data["startedAt"] via execDeriveRunID, so task-context can find the
	// fact sheet wave-start actually wrote.
	createExecState(t, root, "feat/test", map[string]any{
		"startedAt": testNow.UTC().Format(time.RFC3339),
		"waves":     []any{},
		"context":   map[string]any{},
	})

	tasksJSON := `[{"id":"1","name":"Derived run task"}]`
	if _, err := executeState(root, root, ExecuteStateIn{
		Action:    "wave-start",
		Branch:    "feat/test",
		Wave:      intPtr(1),
		TasksJSON: tasksJSON,
	}, clock); err != nil {
		t.Fatalf("wave-start: %v", err)
	}

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "task-context",
		Branch: "feat/test",
		Wave:   intPtr(1),
		TaskID: "1",
	}, clock)
	if err != nil {
		t.Fatalf("task-context: %v", err)
	}
	out := result.(TaskContextOut)
	if !strings.Contains(out.FactSheet, "Derived run task") {
		t.Errorf("FactSheet = %q, want it to contain the fact sheet written under the derived runID", out.FactSheet)
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
	os.MkdirAll(filepath.Join(root, paths.DataDir, paths.RunsSubdir), 0o755)

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

// TestExecState_Cleanup_NoStartedAt_StampsTerminalWithoutTouchingDirs covers
// the no-startedAt branch of the AC: the state file must be preserved and
// stamped runStatus:"completed"/runCompletedAt, remain findable via
// state.Find, and no per-run/ledger directory cleanup may happen at all
// (runDirCleaned/ledgerDirCleaned both false) since there's no startedAt to
// derive a runID from.
func TestExecState_Cleanup_NoStartedAt_StampsTerminalWithoutTouchingDirs(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
		"issues":  []any{map[string]any{"severity": "warning", "category": "x", "summary": "y", "timestamp": "2025-06-15T12:00:00Z"}},
	})

	out, err := executeState(root, root, ExecuteStateIn{
		Action: "cleanup",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want map[string]any", out)
	}
	if m["runStatus"] != "completed" {
		t.Errorf("runStatus = %v, want %q", m["runStatus"], "completed")
	}
	if m["runCompletedAt"] != testNow.UTC().Format(time.RFC3339) {
		t.Errorf("runCompletedAt = %v, want %q", m["runCompletedAt"], testNow.UTC().Format(time.RFC3339))
	}
	if m["runDirCleaned"] != false || m["ledgerDirCleaned"] != false {
		t.Errorf("runDirCleaned/ledgerDirCleaned = %v/%v, want false/false (no startedAt)", m["runDirCleaned"], m["ledgerDirCleaned"])
	}

	// State file must still exist (not deleted) and carry the stamp.
	st, findErr := state.Find(root, "execute", "feat/test")
	if findErr != nil {
		t.Fatalf("find state after cleanup: %v", findErr)
	}
	if st == nil {
		t.Fatal("state file should be preserved (findable) after cleanup, not deleted")
	}
	if st.Data["runStatus"] != "completed" {
		t.Errorf("persisted runStatus = %v, want %q", st.Data["runStatus"], "completed")
	}
	if st.Data["runCompletedAt"] != testNow.UTC().Format(time.RFC3339) {
		t.Errorf("persisted runCompletedAt = %v, want %q", st.Data["runCompletedAt"], testNow.UTC().Format(time.RFC3339))
	}
	// Task 17's issues[] must survive the stamp-and-write untouched.
	issues, _ := st.Data["issues"].([]any)
	if len(issues) != 1 {
		t.Errorf("issues = %#v, want 1 pre-existing entry preserved", st.Data["issues"])
	}

	// Task 18: cleanup response carries a grouped issueSummary derived from
	// issues[]. Warning-only issues never set hardenSuggestion.
	is, ok := m["issueSummary"].(*IssueSummary)
	if !ok {
		t.Fatalf("issueSummary = %#v (%T), want *IssueSummary", m["issueSummary"], m["issueSummary"])
	}
	if is.Total != 1 {
		t.Errorf("issueSummary.Total = %d, want 1", is.Total)
	}
	if is.ByCategory["x"] != 1 {
		t.Errorf("issueSummary.ByCategory = %#v, want {x:1}", is.ByCategory)
	}
	if is.HardenSuggestion != "" {
		t.Errorf("issueSummary.HardenSuggestion = %q, want empty (no error-severity issue)", is.HardenSuggestion)
	}
	if is.Display == "" {
		t.Error("issueSummary.Display should be non-empty when issues are present")
	}
}

// TestExecState_Cleanup_IssueSummary_ErrorSetsHardenSuggestion proves the
// completion action's issueSummary includes a hardenSuggestion synthesized
// from error-severity issue summaries, and that it renders the same items
// through pipeline.IssueSummaryBlock as Display.
func TestExecState_Cleanup_IssueSummary_ErrorSetsHardenSuggestion(t *testing.T) {
	root := t.TempDir()

	createExecState(t, root, "feat/test", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
		"issues": []any{
			map[string]any{"wave": 2, "taskId": "T4", "severity": "error", "category": "task-fail", "summary": "build failed", "timestamp": "2025-06-15T12:00:00Z"},
			map[string]any{"severity": "warning", "category": "done-with-concerns", "summary": "flaky test", "timestamp": "2025-06-15T12:05:00Z"},
		},
	})

	out, err := executeState(root, root, ExecuteStateIn{
		Action: "cleanup",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	m := out.(map[string]any)

	is, ok := m["issueSummary"].(*IssueSummary)
	if !ok {
		t.Fatalf("issueSummary = %#v (%T), want *IssueSummary", m["issueSummary"], m["issueSummary"])
	}
	if is.Total != 2 {
		t.Errorf("issueSummary.Total = %d, want 2", is.Total)
	}
	if is.ByCategory["task-fail"] != 1 || is.ByCategory["done-with-concerns"] != 1 {
		t.Errorf("issueSummary.ByCategory = %#v, want {task-fail:1, done-with-concerns:1}", is.ByCategory)
	}
	if len(is.Items) != 2 || is.Items[0].TaskID != "T4" || is.Items[0].Wave != 2 {
		t.Errorf("issueSummary.Items = %#v, want first item {wave:2, taskId:T4, ...}", is.Items)
	}
	wantSuggestion := "Run /harden --failure-text 'build failed' to strengthen guardrails."
	if is.HardenSuggestion != wantSuggestion {
		t.Errorf("issueSummary.HardenSuggestion = %q, want %q", is.HardenSuggestion, wantSuggestion)
	}
	if !strings.Contains(is.Display, "[error] build failed") {
		t.Errorf("issueSummary.Display = %q, want it to contain the error item", is.Display)
	}
}

func TestExecState_Cleanup_Missing(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, paths.DataDir, paths.RunsSubdir), 0o755)

	out, err := executeState(root, root, ExecuteStateIn{
		Action: "cleanup",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatal("cleanup of missing state should succeed")
	}
	m, ok := out.(map[string]any)
	if !ok || len(m) != 0 {
		t.Errorf("output = %#v, want an empty map (nothing to clean up)", out)
	}
}

// TestExecState_Cleanup_WithStartedAt_RemovesRunAndLedgerDirsOnly proves the
// AC's positive directory-cleanup path: when startedAt is present, cleanup
// derives the runID (execNonDigitTRE-stripped, same as execDeriveRunID and
// execReapRunDirectories' live-run detection) and removes exactly that run's
// working directory and ledger directory — leaving an unrelated sibling
// run's directories untouched.
func TestExecState_Cleanup_WithStartedAt_RemovesRunAndLedgerDirsOnly(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)

	startedAt := "2025-06-01T00:00:00Z"
	runID := execNonDigitTRE.ReplaceAllString(startedAt, "") // "20250601T000000"
	otherRunID := "20250501T000000"

	createExecState(t, root, "feat/test", map[string]any{
		"waves":     []any{},
		"context":   map[string]any{},
		"startedAt": startedAt,
	})

	runDir := filepath.Join(stateDir, runID)
	ldgDir := ledgerDir(root, runID)
	otherRunDir := filepath.Join(stateDir, otherRunID)
	otherLedgerDir := ledgerDir(root, otherRunID)
	for _, d := range []string{runDir, ldgDir, otherRunDir, otherLedgerDir} {
		mkRunDir(t, d, testNow)
	}

	out, err := executeState(root, root, ExecuteStateIn{
		Action: "cleanup",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	m := out.(map[string]any)
	if m["runDirCleaned"] != true || m["ledgerDirCleaned"] != true {
		t.Errorf("runDirCleaned/ledgerDirCleaned = %v/%v, want true/true", m["runDirCleaned"], m["ledgerDirCleaned"])
	}

	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Errorf("run dir %s should have been removed: %v", runDir, err)
	}
	if _, err := os.Stat(ldgDir); !os.IsNotExist(err) {
		t.Errorf("ledger dir %s should have been removed: %v", ldgDir, err)
	}
	if _, err := os.Stat(otherRunDir); err != nil {
		t.Errorf("unrelated sibling run dir %s should NOT have been removed: %v", otherRunDir, err)
	}
	if _, err := os.Stat(otherLedgerDir); err != nil {
		t.Errorf("unrelated sibling ledger dir %s should NOT have been removed: %v", otherLedgerDir, err)
	}

	// State file must still be findable and stamped.
	st, findErr := state.Find(root, "execute", "feat/test")
	if findErr != nil || st == nil {
		t.Fatalf("state file should be preserved and findable after cleanup, findErr=%v st=%v", findErr, st)
	}
	if st.Data["runStatus"] != "completed" {
		t.Errorf("persisted runStatus = %v, want completed", st.Data["runStatus"])
	}

	// No issues[] seeded — issueSummary must be entirely absent, not a "0
	// issues" no-op value.
	if _, present := m["issueSummary"]; present {
		t.Errorf("issueSummary = %#v, want key absent when issues[] is empty", m["issueSummary"])
	}
}

// TestExecState_Cleanup_EmptyRunID_NeverRemoveAllsExecutionDir is the
// CRITICAL SAFETY test: filepath.Join(root, DataDir, RunsSubdir, "")
// resolves to the runs directory ITSELF (a trailing empty Join segment
// is a no-op), so if cleanup ever called os.RemoveAll with an empty/derived
// -empty runID it would wipe every run's data at once, not just one. This
// test proves that never happens, both when startedAt is entirely absent and
// when startedAt is present but strips to an empty runID.
func TestExecState_Cleanup_EmptyRunID_NeverRemoveAllsExecutionDir(t *testing.T) {
	cases := []struct {
		name string
		data map[string]any
	}{
		{
			name: "no startedAt field at all",
			data: map[string]any{"waves": []any{}, "context": map[string]any{}},
		},
		{
			name: "startedAt present but strips to empty runID",
			data: map[string]any{"waves": []any{}, "context": map[string]any{}, "startedAt": "----"},
		},
		{
			name: "startedAt explicitly empty string",
			data: map[string]any{"waves": []any{}, "context": map[string]any{}, "startedAt": ""},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			stateDir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)

			createExecState(t, root, "feat/test", tc.data)

			// Sentinel 1: another run's directory under the execution dir.
			// If cleanup ever RemoveAll'd the execution dir itself (the bug
			// this test guards against), this sentinel would vanish along
			// with everything else under stateDir.
			sentinelDir := filepath.Join(stateDir, "20250101T000000")
			sentinelFile := filepath.Join(sentinelDir, "marker.json")
			mkRunDir(t, sentinelDir, testNow)
			if err := os.WriteFile(sentinelFile, []byte(`{"marker":true}`), 0o644); err != nil {
				t.Fatalf("write sentinel file: %v", err)
			}

			// Sentinel 2: pins the SECOND RemoveAll site (ledger dir). If a
			// future refactor moved ledger removal outside the runID != ""
			// guard, ledgerDir(root, "") resolves to stateDir/ledger itself
			// (same trailing-empty-Join hazard) and would wipe every run's
			// ledger. Sentinel 1 alone would not catch that — no ledger dir
			// exists in that fixture, so RemoveAll on a missing path is a
			// silent no-op and sentinel 1 stays untouched either way.
			ledgerSentinelDir := ledgerDir(root, "20250101T000000")
			ledgerSentinelFile := filepath.Join(ledgerSentinelDir, "marker.json")
			mkRunDir(t, ledgerSentinelDir, testNow)
			if err := os.WriteFile(ledgerSentinelFile, []byte(`{"marker":true}`), 0o644); err != nil {
				t.Fatalf("write ledger sentinel file: %v", err)
			}

			out, err := executeState(root, root, ExecuteStateIn{
				Action: "cleanup",
				Branch: "feat/test",
			}, fixedClock(testNow))
			if err != nil {
				t.Fatalf("cleanup: %v", err)
			}
			m := out.(map[string]any)
			if m["runDirCleaned"] != false || m["ledgerDirCleaned"] != false {
				t.Errorf("runDirCleaned/ledgerDirCleaned = %v/%v, want false/false (no valid runID)", m["runDirCleaned"], m["ledgerDirCleaned"])
			}

			// The execution directory itself, and both sentinels inside it,
			// must be completely untouched.
			if _, err := os.Stat(sentinelFile); err != nil {
				t.Fatalf("sentinel file must survive cleanup with an empty runID, got: %v", err)
			}
			if _, err := os.Stat(ledgerSentinelFile); err != nil {
				t.Fatalf("ledger sentinel file must survive cleanup with an empty runID, got: %v", err)
			}
			if _, err := os.Stat(stateDir); err != nil {
				t.Fatalf("execution directory itself must survive cleanup with an empty runID, got: %v", err)
			}

			// State must still be stamped and findable.
			st, findErr := state.Find(root, "execute", "feat/test")
			if findErr != nil || st == nil {
				t.Fatalf("state file should be preserved and findable, findErr=%v st=%v", findErr, st)
			}
			if st.Data["runStatus"] != "completed" {
				t.Errorf("persisted runStatus = %v, want completed", st.Data["runStatus"])
			}
		})
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
	os.MkdirAll(filepath.Join(root, paths.DataDir, paths.RunsSubdir), 0o755)

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
	os.MkdirAll(filepath.Join(root, paths.DataDir, paths.RunsSubdir), 0o755)

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
	os.MkdirAll(filepath.Join(root, paths.DataDir, paths.RunsSubdir), 0o755)

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
	if _, ok := m["resumeBriefing"]; ok {
		t.Error("resumeBriefing should be absent when there is no state file to resume")
	}
}

// ---------------------------------------------------------------------------
// resume bearings briefing (read + resume-reset)
// ---------------------------------------------------------------------------

// execInFlightFixture seeds an in-flight execute state: wave 1 completed
// (with a committedSha), wave 2 in_progress with two stale tasks left over
// from a crash, and context.completedTaskIds recording T1 as already done.
// Mirrors the TestExecState_ResumeReset fixture so both tests describe the
// same resumable run.
func execInFlightFixture(t *testing.T, root, sha string) {
	t.Helper()
	createExecState(t, root, "feat/test", map[string]any{
		"plannedTaskIds": []any{"T1", "T2", "T3"},
		"waves": []any{
			map[string]any{
				"number":       1,
				"status":       "completed",
				"tasks":        []any{map[string]any{"id": "T1"}},
				"committedSha": sha,
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
		"context": map[string]any{
			"completedTaskIds": []any{"T1"},
		},
	})
}

func TestExecState_Read_InFlight_GitConfirmed(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	sha := headSHA(t, dir)

	execInFlightFixture(t, dir, sha)

	result, err := executeState(dir, dir, ExecuteStateIn{
		Action: "read",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	m := result.(map[string]any)
	briefing, ok := m["resumeBriefing"].(*ExecResumeBriefing)
	if !ok {
		t.Fatalf("resumeBriefing type = %T, want *ExecResumeBriefing", m["resumeBriefing"])
	}
	if !briefing.Resumable {
		t.Error("Resumable = false, want true")
	}
	if briefing.WavesDone != 1 || briefing.WavesRemaining != 1 {
		t.Errorf("WavesDone/WavesRemaining = %d/%d, want 1/1", briefing.WavesDone, briefing.WavesRemaining)
	}
	if briefing.GitCrossCheck != "confirmed" {
		t.Errorf("GitCrossCheck = %q, want confirmed", briefing.GitCrossCheck)
	}
	if len(briefing.GitMismatches) != 0 {
		t.Errorf("GitMismatches = %v, want none", briefing.GitMismatches)
	}
	if want := []string{"T2", "T3"}; !equalStringSlices(briefing.WillRedo, want) {
		t.Errorf("WillRedo = %v, want %v", briefing.WillRedo, want)
	}
	if want := []string{"T1"}; !equalStringSlices(briefing.WillSkip, want) {
		t.Errorf("WillSkip = %v, want %v", briefing.WillSkip, want)
	}
	if briefing.Next == nil || briefing.Next.ID != "resume-reset" {
		t.Errorf("Next = %+v, want ID resume-reset (mutation not yet applied by read)", briefing.Next)
	}
	if briefing.Summary == "" || briefing.Display == "" {
		t.Error("Summary/Display should be populated")
	}

	// read is a pure read: the original wave 2 tasks must be untouched.
	data := readExecState(t, dir, "feat/test")
	w2 := data["waves"].([]any)[1].(map[string]any)
	if len(w2["tasks"].([]any)) != 2 {
		t.Error("read must not mutate state — wave 2 tasks should remain")
	}
}

func TestExecState_Read_InFlight_GitMismatch_NeverHardErrors(t *testing.T) {
	// dir is not a git repo at all: execIsAncestor will fail to run git
	// cleanly, which must surface as a soft gitCrossCheck mismatch, never
	// as a read error.
	dir := t.TempDir()
	execInFlightFixture(t, dir, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

	result, err := executeState(dir, dir, ExecuteStateIn{
		Action: "read",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("read must never hard-fail on a git cross-check problem, got: %v", err)
	}

	m := result.(map[string]any)
	briefing := m["resumeBriefing"].(*ExecResumeBriefing)
	if !briefing.Resumable {
		t.Error("Resumable = false, want true even when the git cross-check itself fails")
	}
	if briefing.GitCrossCheck != "mismatch" {
		t.Errorf("GitCrossCheck = %q, want mismatch", briefing.GitCrossCheck)
	}
	if len(briefing.GitMismatches) == 0 {
		t.Error("GitMismatches should explain the unverifiable commit")
	}
}

func TestExecState_Read_InFlight_GitMismatch_NonAncestor(t *testing.T) {
	// Rebase/amend scenario: the recorded committedSha is a real, valid
	// commit — git can check it cleanly — but it is no longer an ancestor
	// of HEAD. This exercises execIsAncestor's clean (false, nil) branch,
	// distinct from TestExecState_Read_InFlight_GitMismatch_NeverHardErrors
	// above (which exercises the verify-error branch via a non-git dir).
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "c1")
	gitCommit(t, dir, "c2")
	staleSha := headSHA(t, dir)

	// Discard c2: staleSha still exists as a valid git object but is no
	// longer reachable from HEAD.
	runGit(t, dir, "reset", "--hard", "HEAD~1")

	execInFlightFixture(t, dir, staleSha)

	result, err := executeState(dir, dir, ExecuteStateIn{
		Action: "read",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("read must never hard-fail on a confirmed non-ancestor commit, got: %v", err)
	}

	m := result.(map[string]any)
	briefing := m["resumeBriefing"].(*ExecResumeBriefing)
	if !briefing.Resumable {
		t.Error("Resumable = false, want true even when the recorded commit is no longer an ancestor of HEAD")
	}
	if briefing.GitCrossCheck != "mismatch" {
		t.Errorf("GitCrossCheck = %q, want mismatch", briefing.GitCrossCheck)
	}
	if len(briefing.GitMismatches) != 1 {
		t.Fatalf("GitMismatches = %v, want exactly one entry", briefing.GitMismatches)
	}
	if !strings.Contains(briefing.GitMismatches[0], "not found in current git history") {
		t.Errorf("GitMismatches[0] = %q, want the confirmed-non-ancestor wording, not the verify-error wording", briefing.GitMismatches[0])
	}
	if strings.Contains(briefing.GitMismatches[0], "could not verify") {
		t.Errorf("GitMismatches[0] = %q, this is the verify-error branch's wording — non-ancestor case took the wrong path", briefing.GitMismatches[0])
	}
}

func TestExecState_Read_NotInFlight_AllWavesAndTasksComplete(t *testing.T) {
	root := t.TempDir()
	createExecState(t, root, "feat/test", map[string]any{
		"plannedTaskIds": []any{"T1"},
		"waves": []any{
			map[string]any{"number": 1, "status": "completed", "tasks": []any{map[string]any{"id": "T1"}}},
		},
		"context": map[string]any{
			"completedTaskIds": []any{"T1"},
		},
	})

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "read",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	m := result.(map[string]any)
	if _, ok := m["resumeBriefing"]; ok {
		t.Error("resumeBriefing should be absent once every recorded wave and planned task is complete")
	}
}

func TestExecState_ResumeReset_BriefingMatchesClearedTaskIds(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	sha := headSHA(t, dir)

	execInFlightFixture(t, dir, sha)

	result, err := executeState(dir, dir, ExecuteStateIn{
		Action: "resume-reset",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("resume-reset: %v", err)
	}

	m := result.(map[string]any)
	cleared := m["clearedTaskIds"].([]string)

	briefing, ok := m["resumeBriefing"].(*ExecResumeBriefing)
	if !ok {
		t.Fatalf("resumeBriefing type = %T, want *ExecResumeBriefing", m["resumeBriefing"])
	}
	sortedCleared := append([]string{}, cleared...)
	sort.Strings(sortedCleared)
	if !equalStringSlices(briefing.WillRedo, sortedCleared) {
		t.Errorf("WillRedo = %v, want it to match clearedTaskIds %v (mirror requirement)", briefing.WillRedo, sortedCleared)
	}
	if want := []string{"T1"}; !equalStringSlices(briefing.WillSkip, want) {
		t.Errorf("WillSkip = %v, want %v", briefing.WillSkip, want)
	}
	// resume-reset already applied the mutation, so Next should point
	// straight at wave-start, not back at resume-reset.
	if briefing.Next == nil || briefing.Next.ID != "wave-2" {
		t.Errorf("Next = %+v, want ID wave-2", briefing.Next)
	}
}

func TestExecState_ResumeReset_CrashedRun_NeverReportsFailure(t *testing.T) {
	root := t.TempDir()
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "failed", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "resume-reset",
		Branch: "feat/test",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("resume-reset on a failed wave must not error, got: %v", err)
	}

	m := result.(map[string]any)
	briefing, ok := m["resumeBriefing"].(*ExecResumeBriefing)
	if !ok {
		t.Fatalf("resumeBriefing type = %T, want *ExecResumeBriefing", m["resumeBriefing"])
	}
	if !briefing.Resumable {
		t.Error("Resumable = false, want true — a failed wave is resumable, not a dead end")
	}
	if briefing.Next == nil {
		t.Fatal("Next should not be nil for a failed wave")
	}
	if !strings.Contains(briefing.Next.Instruction, "failed") {
		t.Errorf("Next.Instruction = %q, want it to mention the failure", briefing.Next.Instruction)
	}
}

// equalStringSlices compares two string slices ignoring nil-vs-empty.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected DomainError for workerId traversal, got %T", err)
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
	stateDir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
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

// ---------------------------------------------------------------------------
// execReapRunDirectories: ledger/ subdirectories swept individually (F-rerun-3)
// ---------------------------------------------------------------------------

// mkRunDir creates a directory at path (including parents) with the given mtime.
func mkRunDir(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// TestExecReapRunDirectories_SkipsLedgerAsTopLevelEntry proves the top-level
// loop never treats "ledger" as a single per-run directory to RemoveAll —
// which, before the fix, could wipe every run's ledger data at once whenever
// the ledger/ directory's own mtime crossed the TTL.
func TestExecReapRunDirectories_SkipsLedgerAsTopLevelEntry(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	clock := fixedClock(testNow)
	stale := testNow.Add(-30 * 24 * time.Hour)

	// A stale top-level "ledger" directory that, under the old logic, would
	// be wiped wholesale by RemoveAll — even though it holds subdirectories
	// belonging to live runs.
	mkRunDir(t, filepath.Join(stateDir, "ledger"), stale)

	result := execReapRunDirectories(stateDir, 7, false, clock)

	deleted := result["deleted"].([]any)
	kept := result["kept"].([]any)
	for _, e := range append(append([]any{}, deleted...), kept...) {
		m := e.(map[string]any)
		if m["dir"] == "ledger" {
			t.Fatalf("top-level loop must skip 'ledger' entirely, found it in result: %v", m)
		}
	}
	if _, err := os.Stat(filepath.Join(stateDir, "ledger")); err != nil {
		t.Fatalf("ledger/ directory itself must never be removed by the top-level loop: %v", err)
	}
}

// TestExecReapRunDirectories_LedgerChildrenSweptIndividually verifies the
// dedicated second loop applies the same live-run and TTL rules per ledger
// child directory, and that a stale top-level RemoveAll can no longer wipe
// live ledger subdirectories.
func TestExecReapRunDirectories_LedgerChildrenSweptIndividually(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	clock := fixedClock(testNow)
	stale := testNow.Add(-30 * 24 * time.Hour)
	fresh := testNow.Add(-1 * time.Hour)

	liveRunID := "20250601T000000"
	staleRunID := "20250501T000000"
	freshRunID := "20250601T110000"

	// A live execute state file whose startedAt (once stripped of non
	// [0-9T] characters) matches liveRunID.
	createExecState(t, root, "live-branch", map[string]any{
		"startedAt": "2025-06-01T00:00:00Z",
	})

	// Live run's ledger subdir is stale by mtime but must be kept because a
	// matching state file exists.
	mkRunDir(t, filepath.Join(stateDir, "ledger", liveRunID), stale)
	// Stale run's ledger subdir has no matching state file and is past TTL —
	// must be removed individually, without touching ledger/ itself.
	mkRunDir(t, filepath.Join(stateDir, "ledger", staleRunID), stale)
	// TTL-fresh ledger subdir has no matching state file either, but must be
	// kept regardless because it's within the TTL window.
	mkRunDir(t, filepath.Join(stateDir, "ledger", freshRunID), fresh)

	// Creating children just bumped ledger/'s own mtime — force it stale
	// again so this test directly reproduces F-rerun-3: a stale top-level
	// ledger/ mtime must never RemoveAll the whole tree.
	if err := os.Chtimes(filepath.Join(stateDir, "ledger"), stale, stale); err != nil {
		t.Fatalf("chtimes ledger dir: %v", err)
	}

	result := execReapRunDirectories(stateDir, 7, false, clock)

	ledger, ok := result["ledger"].(map[string]any)
	if !ok {
		t.Fatalf("result must contain a 'ledger' key with deleted/kept structure, got: %v", result)
	}

	deletedDirs := map[string]string{}
	for _, e := range ledger["deleted"].([]any) {
		m := e.(map[string]any)
		deletedDirs[m["dir"].(string)] = m["reason"].(string)
	}
	keptDirs := map[string]string{}
	for _, e := range ledger["kept"].([]any) {
		m := e.(map[string]any)
		keptDirs[m["dir"].(string)] = m["reason"].(string)
	}

	if reason, ok := deletedDirs[staleRunID]; !ok || reason != "stale+state-file-gone" {
		t.Errorf("stale ledger subdir with no matching state file should be deleted, got deleted=%v kept=%v", deletedDirs, keptDirs)
	}
	if reason, ok := keptDirs[liveRunID]; !ok || reason != "state-file-exists" {
		t.Errorf("live run's ledger subdir should be kept as state-file-exists, got deleted=%v kept=%v", deletedDirs, keptDirs)
	}
	if reason, ok := keptDirs[freshRunID]; !ok || reason != "ttl-fresh" {
		t.Errorf("TTL-fresh ledger subdir should be kept regardless of state file match, got deleted=%v kept=%v", deletedDirs, keptDirs)
	}

	// Verify actual filesystem state matches the report.
	if _, err := os.Stat(filepath.Join(stateDir, "ledger", staleRunID)); !os.IsNotExist(err) {
		t.Errorf("stale ledger subdir should have been removed from disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "ledger", liveRunID)); err != nil {
		t.Errorf("live ledger subdir should remain on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "ledger", freshRunID)); err != nil {
		t.Errorf("fresh ledger subdir should remain on disk: %v", err)
	}
	// The ledger/ directory itself must never be removed as a unit.
	if _, err := os.Stat(filepath.Join(stateDir, "ledger")); err != nil {
		t.Errorf("ledger/ directory itself should remain on disk: %v", err)
	}
}

// TestExecReapRunDirectories_LedgerDryRun verifies dry-run mode reports
// ledger entries without touching the filesystem.
func TestExecReapRunDirectories_LedgerDryRun(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	clock := fixedClock(testNow)
	stale := testNow.Add(-30 * 24 * time.Hour)

	staleRunID := "20250501T000000"
	mkRunDir(t, filepath.Join(stateDir, "ledger", staleRunID), stale)

	result := execReapRunDirectories(stateDir, 7, true, clock)

	ledger := result["ledger"].(map[string]any)
	deleted := ledger["deleted"].([]any)
	if len(deleted) != 1 {
		t.Fatalf("dry-run should report the stale ledger subdir as deleted, got: %v", deleted)
	}
	m := deleted[0].(map[string]any)
	if m["dir"] != staleRunID {
		t.Errorf("dry-run deleted entry dir = %v, want %v", m["dir"], staleRunID)
	}
	if m["reason"] != "stale+state-file-gone" {
		t.Errorf("dry-run deleted entry reason = %v, want stale+state-file-gone", m["reason"])
	}

	// Dry-run must not touch the filesystem.
	if _, err := os.Stat(filepath.Join(stateDir, "ledger", staleRunID)); err != nil {
		t.Errorf("dry-run must not remove anything from disk: %v", err)
	}
}

// TestExecReapRunDirectories_LedgerKeyPresentWhenStateDirMissing verifies the
// "ledger" key is present with the deleted/kept structure even on the
// early-return path taken when stateDir does not exist yet (e.g. GC run
// before any execution has happened). Downstream consumers (Task 21) pass
// this result through unconditionally, so the shape must never omit the key.
func TestExecReapRunDirectories_LedgerKeyPresentWhenStateDirMissing(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, paths.DataDir, paths.RunsSubdir) // never created
	clock := fixedClock(testNow)

	result := execReapRunDirectories(stateDir, 7, false, clock)

	ledger, ok := result["ledger"].(map[string]any)
	if !ok {
		t.Fatalf("result must contain a 'ledger' key even when stateDir is missing, got: %v", result)
	}
	if deleted, ok := ledger["deleted"].([]any); !ok || len(deleted) != 0 {
		t.Errorf("ledger.deleted should be an empty slice, got: %v", ledger["deleted"])
	}
	if kept, ok := ledger["kept"].([]any); !ok || len(kept) != 0 {
		t.Errorf("ledger.kept should be an empty slice, got: %v", ledger["kept"])
	}
}

// ---------------------------------------------------------------------------
// Narration: wave-start
// ---------------------------------------------------------------------------

func TestExecState_WaveStart_Narration(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"startedAt": testNow.UTC().Format(time.RFC3339),
		"waves":     []any{},
		"context":   map[string]any{},
	})

	tasksJSON := `[{"id":"1","name":"Build API","complexity":"Standard"},{"id":"2","name":"Write tests","complexity":"Trivial"}]`
	result, err := executeState(root, root, ExecuteStateIn{
		Action:    "wave-start",
		Branch:    "feat/test",
		Wave:      intPtr(1),
		TasksJSON: tasksJSON,
		RunID:     "run-1",
	}, clock)
	if err != nil {
		t.Fatalf("wave-start: %v", err)
	}

	m, ok := result.(ExecWaveNarrationOut)
	if !ok {
		t.Fatalf("result = %T, want ExecWaveNarrationOut", result)
	}
	if !strings.Contains(m.Summary, "Wave 1 started with 2 tasks") {
		t.Errorf("Summary = %q, want contains 'Wave 1 started with 2 tasks'", m.Summary)
	}
	if m.Display == "" {
		t.Error("expected non-empty Display (full detail)")
	}
	if !strings.Contains(m.Display, "Wave 1") {
		t.Errorf("Display = %q, want contains 'Wave 1'", m.Display)
	}
	if m.Next == nil {
		t.Fatal("expected Next action")
	}
	if m.Next.EtaSeconds <= 0 {
		t.Errorf("Next.EtaSeconds = %d, want > 0", m.Next.EtaSeconds)
	}
	if m.Next.EtaBasis == "" {
		t.Error("expected non-empty Next.EtaBasis")
	}
}

func TestExecState_WaveStart_Concise(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
	})

	tasksJSON := `[{"id":"1","name":"Task A","complexity":"Complex"}]`
	result, err := executeState(root, root, ExecuteStateIn{
		Action:    "wave-start",
		Branch:    "feat/test",
		Wave:      intPtr(1),
		TasksJSON: tasksJSON,
		RunID:     "run-1",
		Detail:    "concise",
	}, clock)
	if err != nil {
		t.Fatalf("wave-start concise: %v", err)
	}

	m := result.(ExecWaveNarrationOut)
	if m.Summary == "" {
		t.Error("expected Summary even in concise mode")
	}
	if m.Display != "" {
		t.Errorf("Display = %q, want empty in concise mode", m.Display)
	}
}

func TestExecState_WaveStart_NoTasksJSON(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
	})

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-start",
		Branch: "feat/test",
		Wave:   intPtr(1),
	}, clock)
	if err != nil {
		t.Fatalf("wave-start no tasks: %v", err)
	}

	m := result.(ExecWaveNarrationOut)
	if !strings.Contains(m.Summary, "0 tasks") {
		t.Errorf("Summary = %q, want contains '0 tasks'", m.Summary)
	}
	if m.RunID != "" {
		t.Errorf("RunID = %q, want empty when no tasksJson", m.RunID)
	}
}

func TestExecState_WaveStart_InvalidDetail(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves":   []any{},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-start",
		Branch: "feat/test",
		Wave:   intPtr(1),
		Detail: "verbose",
	}, clock)
	if err == nil {
		t.Fatal("expected DomainError for invalid detail")
	}
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Errorf("err = %T, want *DomainError", err)
	}
}

// ---------------------------------------------------------------------------
// Narration: wave-done
// ---------------------------------------------------------------------------

func TestExecState_WaveDone_Narration(t *testing.T) {
	root := t.TempDir()
	startTime := testNow
	clock := fixedClock(startTime.Add(7 * time.Minute))

	createExecState(t, root, "feat/test", map[string]any{
		"startedAt": startTime.UTC().Format(time.RFC3339),
		"waves": []any{
			map[string]any{
				"number":    1,
				"status":    "in_progress",
				"startedAt": startTime.UTC().Format(time.RFC3339),
				"tasks": []any{
					map[string]any{"id": "T1", "name": "Task 1", "complexity": "Standard", "status": "completed"},
					map[string]any{"id": "T2", "name": "Task 2", "complexity": "Standard", "status": "completed"},
				},
			},
		},
		"context": map[string]any{},
	})

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-done",
		Branch: "feat/test",
		Wave:   intPtr(1),
	}, clock)
	if err != nil {
		t.Fatalf("wave-done: %v", err)
	}

	m := result.(ExecWaveNarrationOut)
	if !strings.Contains(m.Summary, "Wave 1 done") {
		t.Errorf("Summary = %q, want contains 'Wave 1 done'", m.Summary)
	}
	if !strings.Contains(m.Summary, "2/2 tasks succeeded") {
		t.Errorf("Summary = %q, want contains '2/2 tasks succeeded'", m.Summary)
	}
	if m.Display == "" {
		t.Error("expected non-empty Display")
	}
	if m.Timing == nil {
		t.Fatal("expected Timing info")
	}
	if m.Timing.StepSeconds <= 0 {
		t.Errorf("Timing.StepSeconds = %d, want > 0", m.Timing.StepSeconds)
	}
	if m.Timing.PipelineSeconds <= 0 {
		t.Errorf("Timing.PipelineSeconds = %d, want > 0", m.Timing.PipelineSeconds)
	}
	if m.Next == nil {
		t.Fatal("expected Next action")
	}
	if !strings.Contains(m.Next.Instruction, "wave-commit") {
		t.Errorf("Next.Instruction = %q, want contains 'wave-commit'", m.Next.Instruction)
	}
	if m.Next.ID != "wave-2" {
		t.Errorf("Next.ID = %q, want wave-2", m.Next.ID)
	}
}

func TestExecState_WaveDone_RecordsTiming(t *testing.T) {
	root := t.TempDir()
	startTime := testNow
	doneTime := startTime.Add(5 * time.Minute)
	clock := fixedClock(doneTime)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{
				"number":    1,
				"status":    "in_progress",
				"startedAt": startTime.UTC().Format(time.RFC3339),
				"tasks": []any{
					map[string]any{"id": "T1", "complexity": "Complex", "status": "completed"},
				},
			},
		},
		"context": map[string]any{},
	})

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-done",
		Branch: "feat/test",
		Wave:   intPtr(1),
	}, clock)
	if err != nil {
		t.Fatalf("wave-done: %v", err)
	}

	// Verify timing was recorded to TimingsStore under bucket key.
	timingsPath := filepath.Join(root, paths.DataDir, "timings.json")
	raw, err := os.ReadFile(timingsPath)
	if err != nil {
		t.Fatalf("read timings.json: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "wave:complex") {
		t.Errorf("timings.json = %s, want to contain 'wave:complex' key", content)
	}
}

func TestExecState_WaveDone_Concise(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow.Add(3 * time.Minute))

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{
				"number":    1,
				"status":    "in_progress",
				"startedAt": testNow.UTC().Format(time.RFC3339),
				"tasks":     []any{},
			},
		},
		"context": map[string]any{},
	})

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-done",
		Branch: "feat/test",
		Wave:   intPtr(1),
		Detail: "concise",
	}, clock)
	if err != nil {
		t.Fatalf("wave-done concise: %v", err)
	}

	m := result.(ExecWaveNarrationOut)
	if m.Summary == "" {
		t.Error("expected Summary in concise mode")
	}
	if m.Display != "" {
		t.Errorf("Display = %q, want empty in concise mode", m.Display)
	}
}

// ---------------------------------------------------------------------------
// Narration: wave-fail
// ---------------------------------------------------------------------------

func TestExecState_WaveFail_Narration(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "in_progress", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	result, err := executeState(root, root, ExecuteStateIn{
		Action:    "wave-fail",
		Branch:    "feat/test",
		Wave:      intPtr(1),
		ErrorText: "agent crashed",
	}, clock)
	if err != nil {
		t.Fatalf("wave-fail: %v", err)
	}

	m, ok := result.(ExecWaveNarrationOut)
	if !ok {
		t.Fatalf("result = %T, want ExecWaveNarrationOut", result)
	}
	if !strings.Contains(m.Summary, "Wave 1 failed (failure)") {
		t.Errorf("Summary = %q, want contains 'Wave 1 failed (failure)'", m.Summary)
	}
	if !strings.Contains(m.Summary, "agent crashed") {
		t.Errorf("Summary = %q, want contains 'agent crashed'", m.Summary)
	}
}

func TestExecState_WaveFail_Timeout(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{"number": 1, "status": "in_progress", "tasks": []any{}},
		},
		"context": map[string]any{},
	})

	result, err := executeState(root, root, ExecuteStateIn{
		Action:   "wave-fail",
		Branch:   "feat/test",
		Wave:     intPtr(1),
		TimedOut: true,
	}, clock)
	if err != nil {
		t.Fatalf("wave-fail timeout: %v", err)
	}

	m := result.(ExecWaveNarrationOut)
	if !strings.Contains(m.Summary, "timeout") {
		t.Errorf("Summary = %q, want contains 'timeout'", m.Summary)
	}
}

// ---------------------------------------------------------------------------
// Narration: task-done
// ---------------------------------------------------------------------------

func TestExecState_TaskDone_Narration(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{
				"number": 1,
				"status": "in_progress",
				"tasks": []any{
					map[string]any{"id": "T1", "status": "completed"},
				},
			},
		},
		"context": map[string]any{},
	})

	result, err := executeState(root, root, ExecuteStateIn{
		Action:     "task-done",
		Branch:     "feat/test",
		Wave:       intPtr(1),
		TaskID:     "T2",
		TaskName:   "Second task",
		Complexity: "Standard",
	}, clock)
	if err != nil {
		t.Fatalf("task-done: %v", err)
	}

	m, ok := result.(ExecTaskNarrationOut)
	if !ok {
		t.Fatalf("result = %T, want ExecTaskNarrationOut", result)
	}
	if !strings.Contains(m.Summary, "Task T2 done") {
		t.Errorf("Summary = %q, want contains 'Task T2 done'", m.Summary)
	}
	// After adding T2, wave has T1 (completed) + T2 (completed) = 2 tasks, 2 completed
	if !strings.Contains(m.Summary, "2/2 reported") {
		t.Errorf("Summary = %q, want contains '2/2 reported'", m.Summary)
	}
}

// ---------------------------------------------------------------------------
// Narration: task-fail
// ---------------------------------------------------------------------------

func TestExecState_TaskFail_Narration(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{
				"number": 1,
				"status": "in_progress",
				"tasks": []any{
					map[string]any{"id": "T1", "status": "completed"},
				},
			},
		},
		"context": map[string]any{},
	})

	result, err := executeState(root, root, ExecuteStateIn{
		Action:    "task-fail",
		Branch:    "feat/test",
		Wave:      intPtr(1),
		TaskID:    "T2",
		ErrorText: "compilation error",
	}, clock)
	if err != nil {
		t.Fatalf("task-fail: %v", err)
	}

	m, ok := result.(ExecTaskNarrationOut)
	if !ok {
		t.Fatalf("result = %T, want ExecTaskNarrationOut", result)
	}
	if !strings.Contains(m.Summary, "Task T2 failed") {
		t.Errorf("Summary = %q, want contains 'Task T2 failed'", m.Summary)
	}
	if !strings.Contains(m.Summary, "1 failed") {
		t.Errorf("Summary = %q, want contains '1 failed'", m.Summary)
	}
}

func TestExecState_TaskFail_Skipped_Narration(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{
			map[string]any{
				"number": 1,
				"status": "in_progress",
				"tasks":  []any{},
			},
		},
		"context": map[string]any{},
	})

	result, err := executeState(root, root, ExecuteStateIn{
		Action:     "task-fail",
		Branch:     "feat/test",
		Wave:       intPtr(1),
		TaskID:     "T3",
		SkippedDep: true,
	}, clock)
	if err != nil {
		t.Fatalf("task-fail skipped: %v", err)
	}

	m := result.(ExecTaskNarrationOut)
	if !strings.Contains(m.Summary, "Task T3 skipped") {
		t.Errorf("Summary = %q, want contains 'Task T3 skipped'", m.Summary)
	}
}

// ---------------------------------------------------------------------------
// Narration helpers
// ---------------------------------------------------------------------------

func TestExecMaxComplexityFromTasks(t *testing.T) {
	tests := []struct {
		tasks []any
		want  string
	}{
		{nil, ""},
		{[]any{map[string]any{"complexity": "Trivial"}}, "Trivial"},
		{[]any{
			map[string]any{"complexity": "Trivial"},
			map[string]any{"complexity": "Complex"},
		}, "Complex"},
		{[]any{
			map[string]any{"complexity": "Standard"},
			map[string]any{"complexity": "Trivial"},
		}, "Standard"},
		{[]any{map[string]any{"complexity": "unknown"}}, ""},
	}
	for _, tt := range tests {
		got := execMaxComplexityFromTasks(tt.tasks)
		if got != tt.want {
			t.Errorf("execMaxComplexityFromTasks(%v) = %q, want %q", tt.tasks, got, tt.want)
		}
	}
}

func TestWaveComplexityBucket(t *testing.T) {
	tests := []struct {
		complexity string
		want       string
	}{
		{"Trivial", "wave:trivial"},
		{"Standard", "wave:standard"},
		{"Complex", "wave:complex"},
		{"", "wave:unknown"},
		{"invalid", "wave:unknown"},
	}
	for _, tt := range tests {
		got := waveComplexityBucket(tt.complexity)
		if got != tt.want {
			t.Errorf("waveComplexityBucket(%q) = %q, want %q", tt.complexity, got, tt.want)
		}
	}
}

func TestExecCountWaveOutcomes(t *testing.T) {
	w := map[string]any{
		"tasks": []any{
			map[string]any{"id": "1", "status": "completed"},
			map[string]any{"id": "2", "status": "completed"},
			map[string]any{"id": "3", "status": "failed"},
			map[string]any{"id": "4", "status": "skipped-dependency"},
		},
	}
	completed, failed, total := execCountWaveOutcomes(w)
	if completed != 2 {
		t.Errorf("completed = %d, want 2", completed)
	}
	if failed != 2 {
		t.Errorf("failed = %d, want 2", failed)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
}

func TestExecWaveETA_FallbackToStatic(t *testing.T) {
	// nil store: should return static estimate.
	sec, basis := execWaveETA(nil, "wave:standard")
	if sec != 300 {
		t.Errorf("ETA seconds = %d, want 300", sec)
	}
	if basis != "static estimate" {
		t.Errorf("basis = %q, want 'static estimate'", basis)
	}
}

func TestExecDetailLevel(t *testing.T) {
	if got := execDetailLevel(ExecuteStateIn{Detail: "concise"}); got != "concise" {
		t.Errorf("got %q, want concise", got)
	}
	if got := execDetailLevel(ExecuteStateIn{Detail: "full"}); got != "full" {
		t.Errorf("got %q, want full", got)
	}
	if got := execDetailLevel(ExecuteStateIn{Detail: ""}); got != "full" {
		t.Errorf("got %q, want full (default)", got)
	}
}

func TestExecValidateDetail(t *testing.T) {
	if err := execValidateDetail(ExecuteStateIn{Detail: ""}); err != nil {
		t.Errorf("empty detail should be valid: %v", err)
	}
	if err := execValidateDetail(ExecuteStateIn{Detail: "concise"}); err != nil {
		t.Errorf("concise should be valid: %v", err)
	}
	if err := execValidateDetail(ExecuteStateIn{Detail: "full"}); err != nil {
		t.Errorf("full should be valid: %v", err)
	}
	if err := execValidateDetail(ExecuteStateIn{Detail: "verbose"}); err == nil {
		t.Error("verbose should be invalid")
	}
}

// ---------------------------------------------------------------------------
// wave-progress: lastCompletedTask pass-through
// ---------------------------------------------------------------------------

func TestExecState_WaveProgress_LastCompletedTask(t *testing.T) {
	root := t.TempDir()

	// Write with lastCompletedTask.
	_, err := executeState(root, root, ExecuteStateIn{
		Action:            "wave-progress",
		RunID:             "run-1",
		TaskID:            "T1",
		Phase:             "editing",
		LastCompletedTask: "T0",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("wave-progress write: %v", err)
	}

	// Read and verify.
	result, err := executeState(root, root, ExecuteStateIn{
		Action:       "wave-progress",
		RunID:        "run-1",
		ReadProgress: true,
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("wave-progress read: %v", err)
	}

	// The result is a *wave.Progress; extract via JSON round-trip.
	raw := fmt.Sprintf("%v", result)
	if !strings.Contains(raw, "T0") {
		t.Errorf("progress = %v, want to contain lastCompletedTask 'T0'", result)
	}
}

// ---------------------------------------------------------------------------
// log-cli
// ---------------------------------------------------------------------------

func TestExecState_LogCLI_AppendsEvidence(t *testing.T) {
	root := t.TempDir()

	result, err := executeState(root, root, ExecuteStateIn{
		Action:      "log-cli",
		Branch:      "feat/test",
		Wave:        intPtr(2),
		CLICommand:  "go test ./...",
		CLIExitCode: 0,
		CLIOutput:   "ok  	github.com/example/pkg	0.5s",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("log-cli: %v", err)
	}

	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result = %T, want map[string]any", result)
	}
	if m["ok"] != true || m["action"] != "log-cli" {
		t.Errorf("result = %v, want ok=true action=log-cli", m)
	}

	path := filepath.Join(root, paths.DataDir, "evidence", "cli-executions.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading evidence file: %v", err)
	}

	var entry CLIEvidenceEntry
	line := strings.TrimSpace(string(b))
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("unmarshal evidence entry: %v (line=%q)", err, line)
	}
	if entry.Pipeline != "execute" {
		t.Errorf("Pipeline = %q, want execute", entry.Pipeline)
	}
	if entry.Branch != "feat/test" {
		t.Errorf("Branch = %q, want feat/test", entry.Branch)
	}
	if entry.Wave == nil || *entry.Wave != 2 {
		t.Errorf("Wave = %v, want 2", entry.Wave)
	}
	if entry.Command != "go test ./..." {
		t.Errorf("Command = %q, want %q", entry.Command, "go test ./...")
	}
	if entry.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", entry.ExitCode)
	}
	if !strings.Contains(entry.OutputHead, "ok") {
		t.Errorf("OutputHead = %q, want to contain command output", entry.OutputHead)
	}
}

func TestExecState_LogCLI_BranchFallsBackToCurrent(t *testing.T) {
	root := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	_, err := executeState(root, root, ExecuteStateIn{
		Action:      "log-cli",
		CLICommand:  "echo hi",
		CLIExitCode: 0,
		CLIOutput:   "hi",
	}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("log-cli without explicit branch: %v", err)
	}

	path := filepath.Join(root, paths.DataDir, "evidence", "cli-executions.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading evidence file: %v", err)
	}
	var entry CLIEvidenceEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &entry); err != nil {
		t.Fatalf("unmarshal evidence entry: %v", err)
	}
	if entry.Branch == "" {
		t.Error("expected Branch to fall back to the current git branch, got empty string")
	}
}

// TestExecState_LogCLI_AppendFailure forces appendCLIEvidence to fail (a
// regular file sits where the evidence directory must be created) and
// confirms the handler wraps it as an *mcpserver.InfraError with a
// "log-cli: " prefixed message, matching the wrapping convention used by
// every other action in this dispatcher.
func TestExecState_LogCLI_AppendFailure(t *testing.T) {
	root := t.TempDir()

	evidenceDir := filepath.Join(root, paths.DataDir, "evidence")
	if err := os.MkdirAll(filepath.Dir(evidenceDir), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Pre-create a regular file where the "evidence" directory must go, so
	// os.MkdirAll inside appendCLIEvidence fails.
	if err := os.WriteFile(evidenceDir, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := executeState(root, root, ExecuteStateIn{
		Action:      "log-cli",
		Branch:      "feat/test",
		CLICommand:  "go build ./...",
		CLIExitCode: 1,
	}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error when evidence directory cannot be created")
	}

	var infraErr *mcpserver.InfraError
	if !errors.As(err, &infraErr) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	if !strings.HasPrefix(infraErr.Msg, "log-cli:") {
		t.Errorf("Msg = %q, want log-cli: prefix", infraErr.Msg)
	}
}
