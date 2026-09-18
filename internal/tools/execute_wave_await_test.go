package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/wave"
)

// Default derived thresholds at shipmeta's built-in defaults
// (waveIntervalSeconds=60, waveTimeoutSeconds=1800), matching every test
// below that doesn't override them on the state file:
//
//	heartbeatTimeout = 10 * 60  = 600s
//	reclaimGrace     = max(5*60, 300) = 300s
//	totalTimeout     = 1800s
const (
	waveAwaitTestHeartbeat = 600 * time.Second
	waveAwaitTestGrace     = 300 * time.Second
	waveAwaitTestTotal     = 1800 * time.Second
)

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

// waveAwaitTs formats an offset from testNow using the same layout the
// handler itself uses, so fixtures and assertions never drift apart.
func waveAwaitTs(offset time.Duration) string {
	return waveAwaitFormat(testNow.Add(offset))
}

// planned builds one w["planned"] entry.
func waveAwaitPlannedEntry(id string) map[string]any {
	return map[string]any{"id": id, "name": "Task " + id, "files": []any{}}
}

// closedRow builds one w["tasks"] entry (a closed manifest row).
func waveAwaitClosedRow(id, status string) map[string]any {
	return map[string]any{"id": id, "status": status}
}

// openRow builds one w["tasks"] entry left in_progress by a redispatch.
func waveAwaitOpenRow(id string) map[string]any {
	return map[string]any{"id": id, "status": "in_progress"}
}

// waveAwaitManifest assembles a wave map with planned/tasks arrays.
func waveAwaitManifest(waveNum int, planned []map[string]any, tasks []map[string]any) map[string]any {
	plannedAny := make([]any, len(planned))
	for i, p := range planned {
		plannedAny[i] = p
	}
	tasksAny := make([]any, len(tasks))
	for i, t := range tasks {
		tasksAny[i] = t
	}
	return map[string]any{"number": waveNum, "status": "in_progress", "planned": plannedAny, "tasks": tasksAny}
}

func waveAwaitStoreServerState(t *testing.T, root, runID, taskID string, s wave.ServerTaskState) {
	t.Helper()
	if err := wave.StoreServerState(root, runID, taskID, s); err != nil {
		t.Fatalf("store server state for %s: %v", taskID, err)
	}
}

// waveAwaitWriteProgress writes a task's worker-owned progress file
// directly (bypassing wave.UpdateProgress, which stamps time.Now() and so
// cannot produce a deterministic UpdatedAt for tests).
func waveAwaitWriteProgress(t *testing.T, root, runID, taskID string, tp wave.TaskProgress) {
	t.Helper()
	dir := filepath.Join(root, paths.DataDir, paths.RunsSubdir, runID, "progress")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir progress dir: %v", err)
	}
	if err := fsx.AtomicWriteJSON(filepath.Join(dir, taskID+".json"), tp); err != nil {
		t.Fatalf("write progress for %s: %v", taskID, err)
	}
}

func waveAwaitLoadServerState(t *testing.T, root, runID, taskID string) wave.ServerTaskState {
	t.Helper()
	s, found, err := wave.LoadServerState(root, runID, taskID)
	if err != nil {
		t.Fatalf("load server state for %s: %v", taskID, err)
	}
	if !found {
		t.Fatalf("server state for %s not found", taskID)
	}
	return s
}

// ---------------------------------------------------------------------------
// Input validation
// ---------------------------------------------------------------------------

func TestExecActionWaveAwait_RequiresRunID(t *testing.T) {
	root := t.TempDir()
	_, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", Wave: intPtr(1)}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for missing runId")
	}
	de, ok := err.(*mcpserver.DomainError)
	if !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if de.Suggestion == "" {
		t.Error("expected non-empty Suggestion (guardrail mcp-error-has-suggestion)")
	}
}

func TestExecActionWaveAwait_RequiresWave(t *testing.T) {
	root := t.TempDir()
	_, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: "run1"}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for missing wave")
	}
	de, ok := err.(*mcpserver.DomainError)
	if !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if de.Suggestion == "" {
		t.Error("expected non-empty Suggestion (guardrail mcp-error-has-suggestion)")
	}
}

func TestExecActionWaveAwait_WaveNotFound(t *testing.T) {
	root := t.TempDir()
	createExecState(t, root, "feat/test", map[string]any{"waves": []any{}})

	_, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: "run1", Wave: intPtr(1)}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected error for missing wave entry")
	}
	de, ok := err.(*mcpserver.DataError)
	if !ok {
		t.Fatalf("expected DataError, got %T: %v", err, err)
	}
	if de.Suggestion == "" {
		t.Error("expected non-empty Suggestion (guardrail mcp-error-has-suggestion)")
	}
}

// ---------------------------------------------------------------------------
// Healthy / queued / recorded bucketing
// ---------------------------------------------------------------------------

func TestExecActionWaveAwait_HealthyOpenTaskStaysPending(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1, []map[string]any{waveAwaitPlannedEntry("1")}, nil)},
	})
	waveAwaitStoreServerState(t, root, runID, "1", wave.ServerTaskState{
		DispatchedAt:     waveAwaitTs(-30 * time.Second),
		WorkerName:       "worker-1",
		ContextFetchedAt: waveAwaitTs(-25 * time.Second),
		Attempt:          1,
	})
	waveAwaitWriteProgress(t, root, runID, "1", wave.TaskProgress{
		Phase: "editing", UpdatedAt: waveAwaitTs(-10 * time.Second),
	})

	out, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("execActionWaveAwait: %v", err)
	}
	if out.Status != "pending" {
		t.Errorf("status = %q, want pending", out.Status)
	}
	if out.Next == "" {
		t.Error("Next must be present on every status")
	}
	prog := out.Progress.(waveAwaitProgress)
	if !containsStr(prog.Open, "1") {
		t.Errorf("expected task 1 in open bucket, got %+v", prog)
	}
	assertNonNilBuckets(t, prog)
	assertNonNilExt(t, out)
}

func TestExecActionWaveAwait_QueuedTaskNotYetDispatched(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1, []map[string]any{waveAwaitPlannedEntry("1")}, nil)},
	})
	// No server state at all for task 1: never dispatched.

	out, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("execActionWaveAwait: %v", err)
	}
	if out.Status != "pending" {
		t.Errorf("status = %q, want pending", out.Status)
	}
	prog := out.Progress.(waveAwaitProgress)
	if !containsStr(prog.Queued, "1") {
		t.Errorf("expected task 1 in queued bucket, got %+v", prog)
	}
	if !strings.Contains(out.Next, "Dispatch task(s) 1") {
		t.Errorf("expected next to instruct dispatch of queued task, got %q", out.Next)
	}
}

func TestExecActionWaveAwait_DoneWhenAllRecorded(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1,
			[]map[string]any{waveAwaitPlannedEntry("1"), waveAwaitPlannedEntry("2")},
			[]map[string]any{waveAwaitClosedRow("1", "completed"), waveAwaitClosedRow("2", "failed")},
		)},
	})

	out, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("execActionWaveAwait: %v", err)
	}
	if out.Status != "done" {
		t.Errorf("status = %q, want done", out.Status)
	}
	if out.Next == "" {
		t.Error("Next must be present on every status")
	}
	if !strings.Contains(out.Next, "wave-done") {
		t.Errorf("expected done next to mention wave-done, got %q", out.Next)
	}
	prog := out.Progress.(waveAwaitProgress)
	if len(prog.Recorded) != 2 {
		t.Errorf("expected 2 recorded tasks, got %+v", prog.Recorded)
	}
	if len(prog.Open) != 0 || len(prog.Queued) != 0 || len(prog.Stalled) != 0 {
		t.Errorf("expected empty open/queued/stalled, got %+v", prog)
	}
}

func TestExecActionWaveAwait_RedispatchedRowReopensAsOpen(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	// Task 1 was closed once (failed) then task-redispatch reopened its row
	// to in_progress — the later row in the tasks[] array must win.
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1,
			[]map[string]any{waveAwaitPlannedEntry("1")},
			[]map[string]any{waveAwaitOpenRow("1")},
		)},
	})
	waveAwaitStoreServerState(t, root, runID, "1", wave.ServerTaskState{
		DispatchedAt: waveAwaitTs(-10 * time.Second), WorkerName: "worker-1", Attempt: 2,
	})

	out, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("execActionWaveAwait: %v", err)
	}
	if out.Status != "pending" {
		t.Errorf("status = %q, want pending (row reopened, must be reported open)", out.Status)
	}
	prog := out.Progress.(waveAwaitProgress)
	if !containsStr(prog.Open, "1") {
		t.Errorf("expected task 1 back in open bucket after redispatch, got %+v", prog)
	}
}

// ---------------------------------------------------------------------------
// Reclaim: stamp on first unhealthy sighting
// ---------------------------------------------------------------------------

func TestExecActionWaveAwait_NeverStartedTriggersReclaimStamp(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1, []map[string]any{waveAwaitPlannedEntry("1")}, nil)},
	})
	waveAwaitStoreServerState(t, root, runID, "1", wave.ServerTaskState{
		DispatchedAt: waveAwaitTs(-700 * time.Second), // > 600s heartbeatTimeout
		WorkerName:   "worker-1",
		// ContextFetchedAt empty: never-started.
		Attempt: 1,
	})

	out, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("execActionWaveAwait: %v", err)
	}
	if out.Status != "pending" {
		t.Errorf("status = %q, want pending", out.Status)
	}
	reclaims := out.Ext["reclaimRequests"].([]waveAwaitReclaimRequest)
	if len(reclaims) != 1 {
		t.Fatalf("expected 1 reclaim request, got %d: %+v", len(reclaims), reclaims)
	}
	if reclaims[0].WorkerName != "worker-1" || !containsStr(reclaims[0].TaskIDs, "1") {
		t.Errorf("unexpected reclaim request: %+v", reclaims[0])
	}
	if !strings.Contains(out.Next, "SendMessage") {
		t.Errorf("expected next to instruct SendMessage relay, got %q", out.Next)
	}

	s := waveAwaitLoadServerState(t, root, runID, "1")
	if s.ReclaimRequestedAt == "" {
		t.Error("expected reclaimRequestedAt to be stamped on the server file")
	}
}

func TestExecActionWaveAwait_StalledTriggersReclaimStamp(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1, []map[string]any{waveAwaitPlannedEntry("1")}, nil)},
	})
	waveAwaitStoreServerState(t, root, runID, "1", wave.ServerTaskState{
		DispatchedAt:     waveAwaitTs(-1500 * time.Second),
		WorkerName:       "worker-1",
		ContextFetchedAt: waveAwaitTs(-1490 * time.Second), // started fine
		Attempt:          1,
	})
	waveAwaitWriteProgress(t, root, runID, "1", wave.TaskProgress{
		Phase: "editing", UpdatedAt: waveAwaitTs(-700 * time.Second), // stale > 600s
	})

	out, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("execActionWaveAwait: %v", err)
	}
	prog := out.Progress.(waveAwaitProgress)
	if !containsStr(prog.Stalled, "1") {
		t.Errorf("expected task 1 in stalled bucket, got %+v", prog)
	}
	s := waveAwaitLoadServerState(t, root, runID, "1")
	if s.ReclaimRequestedAt == "" {
		t.Error("expected reclaimRequestedAt to be stamped")
	}
}

// TestExecActionWaveAwait_DuplicateCallsDontRestartGrace verifies that a
// second wave-await call while a reclaim is already outstanding does not
// re-stamp reclaimRequestedAt (which would restart the grace window) and
// does not re-emit ext.reclaimRequests.
func TestExecActionWaveAwait_DuplicateCallsDontRestartGrace(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1, []map[string]any{waveAwaitPlannedEntry("1")}, nil)},
	})
	waveAwaitStoreServerState(t, root, runID, "1", wave.ServerTaskState{
		DispatchedAt: waveAwaitTs(-700 * time.Second), // > 600s heartbeatTimeout
		WorkerName:   "worker-1",
		Attempt:      1,
	})

	in := ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}
	out1, err := execActionWaveAwait(root, in, fixedClock(testNow))
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}
	stampedAt := waveAwaitLoadServerState(t, root, runID, "1").ReclaimRequestedAt
	if stampedAt == "" {
		t.Fatal("expected reclaim stamp after call 1")
	}

	// Call again 30s later (still well within the 120s grace window),
	// threading the returned state_file back in like the skill would.
	in.StateFile = *out1.StateFile
	out2, err := execActionWaveAwait(root, in, fixedClock(testNow.Add(30*time.Second)))
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}

	stampedAt2 := waveAwaitLoadServerState(t, root, runID, "1").ReclaimRequestedAt
	if stampedAt2 != stampedAt {
		t.Errorf("reclaim stamp changed across duplicate calls: %q -> %q (grace window double-consumed)", stampedAt, stampedAt2)
	}
	reclaims2 := out2.Ext["reclaimRequests"].([]waveAwaitReclaimRequest)
	if len(reclaims2) != 0 {
		t.Errorf("expected no re-emitted reclaimRequests on duplicate call, got %+v", reclaims2)
	}
}

// ---------------------------------------------------------------------------
// Reclaim resolution: reply vs. no-reply
// ---------------------------------------------------------------------------

// TestExecActionWaveAwait_ReclaimReplyIsTreatedAsProofOfLifeNotFailure pins
// the new contract: a worker that answers its reclaim is never a failure.
// The reply itself is the liveness proof — the stamp is cleared and the
// same attempt (same Attempt, no TaskStop, no task-fail, no retry consumed)
// simply keeps going.
func TestExecActionWaveAwait_ReclaimReplyIsTreatedAsProofOfLifeNotFailure(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1, []map[string]any{waveAwaitPlannedEntry("1")}, nil)},
	})
	waveAwaitStoreServerState(t, root, runID, "1", wave.ServerTaskState{
		DispatchedAt:       waveAwaitTs(-500 * time.Second),
		WorkerName:         "worker-1",
		ContextFetchedAt:   waveAwaitTs(-490 * time.Second),
		ReclaimRequestedAt: waveAwaitTs(-50 * time.Second),
		Attempt:            1,
	})
	// Worker reported back after the reclaim request was stamped.
	waveAwaitWriteProgress(t, root, runID, "1", wave.TaskProgress{
		Phase: "reporting", UpdatedAt: waveAwaitTs(-10 * time.Second),
		AcceptanceDone: []int{0, 1}, FilesTouched: []string{"internal/tools/x.go"},
		LastCompletedTask: "did the thing", Blocker: "unclear next step",
	})

	out, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("execActionWaveAwait: %v", err)
	}
	if out.Status != "pending" {
		t.Errorf("status = %q, want pending (task still open, just reclaimed)", out.Status)
	}
	failures := out.Ext["failed"].([]WaveAwaitFailure)
	if len(failures) != 0 {
		t.Fatalf("expected 0 failures — a reply is proof of life, not a failure, got %d: %+v", len(failures), failures)
	}
	prog := out.Progress.(waveAwaitProgress)
	if !containsStr(prog.Open, "1") {
		t.Errorf("expected task 1 back in the open bucket after a reclaim reply, got %+v", prog)
	}
	if containsStr(prog.Stalled, "1") {
		t.Errorf("task 1 must not still be reported stalled after replying, got %+v", prog)
	}

	if strings.Contains(out.Next, "TaskStop") {
		t.Errorf("next must not order TaskStop for a subject that replied, got %q", out.Next)
	}
	if !strings.Contains(out.Next, "wave-await") {
		t.Errorf("expected next to order another wave-await call, got %q", out.Next)
	}

	s := waveAwaitLoadServerState(t, root, runID, "1")
	if s.ReclaimRequestedAt != "" {
		t.Errorf("expected reclaim stamp cleared after a reply, still %q", s.ReclaimRequestedAt)
	}
	if s.Attempt != 1 {
		t.Errorf("Attempt must be unchanged by a reclaim reply, got %d, want 1", s.Attempt)
	}
}

func TestExecActionWaveAwait_ReclaimNoReplyAfterGraceFailsWithoutResumeFrom(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1, []map[string]any{waveAwaitPlannedEntry("1")}, nil)},
	})
	waveAwaitStoreServerState(t, root, runID, "1", wave.ServerTaskState{
		DispatchedAt:       waveAwaitTs(-1500 * time.Second),
		WorkerName:         "worker-1",
		ContextFetchedAt:   waveAwaitTs(-1490 * time.Second),
		ReclaimRequestedAt: waveAwaitTs(-310 * time.Second), // > 300s grace
		Attempt:            3,                               // ceiling: no retries left
	})
	// No progress update since before the reclaim stamp: never replied.
	waveAwaitWriteProgress(t, root, runID, "1", wave.TaskProgress{
		Phase: "editing", UpdatedAt: waveAwaitTs(-600 * time.Second),
	})

	out, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("execActionWaveAwait: %v", err)
	}
	failures := out.Ext["failed"].([]WaveAwaitFailure)
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %d: %+v", len(failures), failures)
	}
	f := failures[0]
	if f.Cause != "STALLED_NO_REPLY" {
		t.Errorf("cause = %q, want STALLED_NO_REPLY", f.Cause)
	}
	if f.RetriesLeft != 0 {
		t.Errorf("retriesLeft = %d, want 0 (attempt 3, ceiling)", f.RetriesLeft)
	}
	if out.Status != "done" {
		t.Errorf("status = %q, want done (only task is terminal, nothing left to supervise)", out.Status)
	}
	if !strings.Contains(out.Next, "out of retries") {
		t.Errorf("expected escalation wording for terminal failure, got %q", out.Next)
	}

	// Stamp must NOT be cleared on NO_REPLY (clearing would let a later
	// call re-stamp and restart the grace window).
	s := waveAwaitLoadServerState(t, root, runID, "1")
	if s.ReclaimRequestedAt == "" {
		t.Error("expected reclaim stamp to remain set after STALLED_NO_REPLY")
	}
}

// ---------------------------------------------------------------------------
// Timeout: short-circuits reclaim entirely
// ---------------------------------------------------------------------------

func TestExecActionWaveAwait_TimeoutShortCircuitsReclaim(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1, []map[string]any{waveAwaitPlannedEntry("1"), waveAwaitPlannedEntry("2")}, nil)},
	})
	// Task 1: well past totalTimeout (1800s), with a stale reclaim stamp
	// already set from a previous call — timeout must win outright, no
	// reclaim harvesting or grace-expiry check performed.
	waveAwaitStoreServerState(t, root, runID, "1", wave.ServerTaskState{
		DispatchedAt:       waveAwaitTs(-2000 * time.Second),
		WorkerName:         "worker-1",
		ContextFetchedAt:   waveAwaitTs(-1990 * time.Second),
		ReclaimRequestedAt: waveAwaitTs(-1000 * time.Second),
		Attempt:            1,
	})
	// Task 2: healthy, dispatched recently, independent totalTimeout not
	// exceeded — proves termination is per-task, not a wave-level budget.
	waveAwaitStoreServerState(t, root, runID, "2", wave.ServerTaskState{
		DispatchedAt:     waveAwaitTs(-10 * time.Second),
		WorkerName:       "worker-2",
		ContextFetchedAt: waveAwaitTs(-5 * time.Second),
		Attempt:          1,
	})
	waveAwaitWriteProgress(t, root, runID, "2", wave.TaskProgress{
		Phase: "editing", UpdatedAt: waveAwaitTs(-2 * time.Second),
	})

	out, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("execActionWaveAwait: %v", err)
	}
	failures := out.Ext["failed"].([]WaveAwaitFailure)
	if len(failures) != 1 || failures[0].TaskID != "1" || failures[0].Cause != "TIMEOUT" {
		t.Fatalf("expected single TIMEOUT failure for task 1, got %+v", failures)
	}
	reclaims := out.Ext["reclaimRequests"].([]waveAwaitReclaimRequest)
	if len(reclaims) != 0 {
		t.Errorf("expected no reclaim attempt on timeout, got %+v", reclaims)
	}
	if idx := strings.Index(out.Next, "TaskStop"); idx != 0 && !strings.HasPrefix(out.Next, "TaskStop") {
		// TaskStop must still be the ordering instruction for this failure,
		// even though the underlying cause bypassed reclaim.
		t.Errorf("expected next to open with TaskStop for TIMEOUT failure, got %q", out.Next)
	}
	if out.Status != "pending" {
		t.Errorf("status = %q, want pending (task 2 still healthy and open)", out.Status)
	}
	prog := out.Progress.(waveAwaitProgress)
	if !containsStr(prog.Open, "2") {
		t.Errorf("expected task 2 to remain in open bucket, got %+v", prog)
	}

	// Reclaim stamp on task 1 must be left untouched by the timeout branch.
	s := waveAwaitLoadServerState(t, root, runID, "1")
	if s.ReclaimRequestedAt != waveAwaitTs(-1000*time.Second) {
		t.Errorf("expected reclaim stamp untouched by timeout branch, got %q", s.ReclaimRequestedAt)
	}
}

// ---------------------------------------------------------------------------
// Retry ceiling / redispatch ordering
// ---------------------------------------------------------------------------

func TestExecActionWaveAwait_RetriesLeftKeepsWavePendingWithRedispatchOrdering(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1, []map[string]any{waveAwaitPlannedEntry("4")}, nil)},
	})
	waveAwaitStoreServerState(t, root, runID, "4", wave.ServerTaskState{
		DispatchedAt:       waveAwaitTs(-500 * time.Second),
		WorkerName:         "worker-run7-3",
		ContextFetchedAt:   waveAwaitTs(-490 * time.Second),
		ReclaimRequestedAt: waveAwaitTs(-310 * time.Second), // > 300s grace, never answered
		Attempt:            2,                               // retriesLeft = 1
	})
	waveAwaitWriteProgress(t, root, runID, "4", wave.TaskProgress{
		Phase: "editing", UpdatedAt: waveAwaitTs(-400 * time.Second), // older than the stamp: no reply
	})

	out, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("execActionWaveAwait: %v", err)
	}
	if out.Status != "pending" {
		t.Fatalf("status = %q, want pending", out.Status)
	}
	failures := out.Ext["failed"].([]WaveAwaitFailure)
	if len(failures) != 1 || failures[0].RetriesLeft != 1 {
		t.Fatalf("expected 1 failure with retriesLeft=1, got %+v", failures)
	}

	stop := strings.Index(out.Next, "TaskStop")
	fail := strings.Index(out.Next, "task-fail")
	redispatch := strings.Index(out.Next, "task-redispatch")
	dispatch := strings.Index(out.Next, "dispatch task 4 again")
	again := strings.Index(out.Next, "call wave-await again")
	if stop < 0 || fail < 0 || redispatch < 0 || dispatch < 0 || again < 0 {
		t.Fatalf("expected TaskStop -> task-fail -> task-redispatch -> dispatch -> wave-await ordering, got %q", out.Next)
	}
	if !(stop < fail && fail < redispatch && redispatch < dispatch && dispatch < again) {
		t.Errorf("next-instruction ordering violated: stop=%d fail=%d redispatch=%d dispatch=%d again=%d in %q",
			stop, fail, redispatch, dispatch, again, out.Next)
	}
	if !strings.Contains(out.Next, "Do NOT run the wave gates yet") {
		t.Errorf("expected 'Do NOT run the wave gates yet', got %q", out.Next)
	}
}

func TestExecActionWaveAwait_TerminalFailureRunsGatesWhenNothingElseOpen(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1,
			[]map[string]any{waveAwaitPlannedEntry("3"), waveAwaitPlannedEntry("4")},
			[]map[string]any{waveAwaitClosedRow("3", "completed")},
		)},
	})
	waveAwaitStoreServerState(t, root, runID, "4", wave.ServerTaskState{
		DispatchedAt:     waveAwaitTs(-2500 * time.Second), // > totalTimeout
		WorkerName:       "worker-run7-8",
		ContextFetchedAt: waveAwaitTs(-2490 * time.Second),
		Attempt:          3,
	})

	out, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("execActionWaveAwait: %v", err)
	}
	if out.Status != "done" {
		t.Errorf("status = %q, want done", out.Status)
	}
	failures := out.Ext["failed"].([]WaveAwaitFailure)
	if len(failures) != 1 || failures[0].Cause != "TIMEOUT" || failures[0].RetriesLeft != 0 {
		t.Fatalf("unexpected failures: %+v", failures)
	}
	if !strings.Contains(out.Next, "wave-done") {
		t.Errorf("expected done next to instruct running the wave gates, got %q", out.Next)
	}
	if !strings.Contains(out.Next, "out of retries") {
		t.Errorf("expected terminal-failure escalation wording, got %q", out.Next)
	}
}

// ---------------------------------------------------------------------------
// Batch-as-subject classification
// ---------------------------------------------------------------------------

func TestExecActionWaveAwait_BatchClassifiedAsSingleSubject(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	// B1 already closed (completed); B2 is the batch's current, open
	// member. Both share batchId "batch-1"; B1 is batchIndex 0.
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1,
			[]map[string]any{waveAwaitPlannedEntry("B1"), waveAwaitPlannedEntry("B2")},
			[]map[string]any{waveAwaitClosedRow("B1", "completed")},
		)},
	})
	waveAwaitStoreServerState(t, root, runID, "B1", wave.ServerTaskState{
		DispatchedAt:     waveAwaitTs(-400 * time.Second),
		WorkerName:       "worker-batch",
		BatchID:          "batch-1",
		BatchIndex:       0,
		ContextFetchedAt: waveAwaitTs(-390 * time.Second),
		Attempt:          1,
	})
	waveAwaitStoreServerState(t, root, runID, "B2", wave.ServerTaskState{
		DispatchedAt:     waveAwaitTs(-400 * time.Second),
		WorkerName:       "worker-batch",
		BatchID:          "batch-1",
		BatchIndex:       1,
		ContextFetchedAt: waveAwaitTs(-390 * time.Second),
		Attempt:          1,
	})
	// B1 finished a while ago; B2 (the live member) just heartbeated —
	// this must protect the whole subject from being misclassified as
	// stalled even though B1's own updatedAt (if it had one) would be
	// old. Batch liveness = max across every member.
	waveAwaitWriteProgress(t, root, runID, "B1", wave.TaskProgress{
		Phase: "reporting", UpdatedAt: waveAwaitTs(-395 * time.Second),
	})
	waveAwaitWriteProgress(t, root, runID, "B2", wave.TaskProgress{
		Phase: "editing", UpdatedAt: waveAwaitTs(-5 * time.Second),
	})

	out, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("execActionWaveAwait: %v", err)
	}
	if out.Status != "pending" {
		t.Errorf("status = %q, want pending", out.Status)
	}
	prog := out.Progress.(waveAwaitProgress)
	if !containsStr(prog.Open, "B2") {
		t.Errorf("expected B2 (the batch's only open member) in open bucket, got %+v", prog)
	}
	if containsStr(prog.Open, "B1") || containsStr(prog.Stalled, "B1") {
		t.Errorf("B1 is already recorded and must not be reclassified, got %+v", prog)
	}
	reclaims := out.Ext["reclaimRequests"].([]waveAwaitReclaimRequest)
	if len(reclaims) != 0 {
		t.Errorf("expected no reclaim (batch is live via B2), got %+v", reclaims)
	}
}

func TestExecActionWaveAwait_StalledBatchReclaimsAllOpenMembersTogether(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	// A two-member batch, both open, both stalled: one reclaim request
	// must be emitted for the whole subject, naming both task IDs.
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1,
			[]map[string]any{waveAwaitPlannedEntry("B1"), waveAwaitPlannedEntry("B2")}, nil,
		)},
	})
	for i, id := range []string{"B1", "B2"} {
		waveAwaitStoreServerState(t, root, runID, id, wave.ServerTaskState{
			DispatchedAt:     waveAwaitTs(-1500 * time.Second),
			WorkerName:       "worker-batch",
			BatchID:          "batch-2",
			BatchIndex:       i,
			ContextFetchedAt: waveAwaitTs(-1490 * time.Second),
			Attempt:          1,
		})
		waveAwaitWriteProgress(t, root, runID, id, wave.TaskProgress{
			Phase: "editing", UpdatedAt: waveAwaitTs(-700 * time.Second), // stale
		})
	}

	out, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("execActionWaveAwait: %v", err)
	}
	reclaims := out.Ext["reclaimRequests"].([]waveAwaitReclaimRequest)
	if len(reclaims) != 1 {
		t.Fatalf("expected exactly one reclaim request for the whole batch subject, got %d: %+v", len(reclaims), reclaims)
	}
	if !containsStr(reclaims[0].TaskIDs, "B1") || !containsStr(reclaims[0].TaskIDs, "B2") {
		t.Errorf("expected reclaim request to name both open members, got %+v", reclaims[0])
	}

	// Both members' server files must carry the stamp so a later call sees
	// it regardless of which one sorts first by BatchIndex.
	s1 := waveAwaitLoadServerState(t, root, runID, "B1")
	s2 := waveAwaitLoadServerState(t, root, runID, "B2")
	if s1.ReclaimRequestedAt == "" || s2.ReclaimRequestedAt == "" {
		t.Errorf("expected both batch members stamped, got B1=%q B2=%q", s1.ReclaimRequestedAt, s2.ReclaimRequestedAt)
	}
}

// TestExecActionWaveAwait_ClosedBatchIndexZeroStillLoadedForLiveness targets
// the states-loading fix directly: BuildSubjects needs server state loaded
// for EVERY planned task ID (open and closed), not just open ones, because
// Subject.ContextFetchedAt/DispatchedAt are sourced from batchIndex 0's
// server state (see wave.BuildSubjects), and batchIndex 0 can be a member
// whose manifest row already closed. B1 (batchIndex 0) is closed and carries
// ContextFetchedAt; B2 (batchIndex 1) is open and has never fetched its own
// context and has no progress file at all. If B1's server state were not
// loaded (the pre-fix bug), B2 would be classified as a solo subject whose
// own ContextFetchedAt is empty, yielding "never-started" instead of the
// correct "stalled" verdict driven by the batch's shared ContextFetchedAt
// and merged liveness — and the reclaim stamp would land only on B2, never
// on B1.
func TestExecActionWaveAwait_ClosedBatchIndexZeroStillLoadedForLiveness(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1,
			[]map[string]any{waveAwaitPlannedEntry("B1"), waveAwaitPlannedEntry("B2")},
			[]map[string]any{waveAwaitClosedRow("B1", "completed")},
		)},
	})
	waveAwaitStoreServerState(t, root, runID, "B1", wave.ServerTaskState{
		DispatchedAt:     waveAwaitTs(-1500 * time.Second),
		WorkerName:       "worker-batch3",
		BatchID:          "batch-3",
		BatchIndex:       0,
		ContextFetchedAt: waveAwaitTs(-1490 * time.Second),
		Attempt:          1,
	})
	waveAwaitWriteProgress(t, root, runID, "B1", wave.TaskProgress{
		Phase: "reporting", UpdatedAt: waveAwaitTs(-700 * time.Second),
	})
	// B2: open, batchIndex 1, never fetched its own context, no progress
	// file at all — classified solo this alone reads as never-started.
	waveAwaitStoreServerState(t, root, runID, "B2", wave.ServerTaskState{
		DispatchedAt: waveAwaitTs(-500 * time.Second),
		WorkerName:   "worker-batch3",
		BatchID:      "batch-3",
		BatchIndex:   1,
		Attempt:      1,
	})

	in := ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}
	out, err := execActionWaveAwait(root, in, fixedClock(testNow))
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}
	prog := out.Progress.(waveAwaitProgress)
	if !containsStr(prog.Stalled, "B2") {
		t.Errorf("expected B2 classified stalled via batch-shared ContextFetchedAt, got %+v — a never-started verdict here means the closed batchIndex-0 member was not loaded", prog)
	}
	reclaims := out.Ext["reclaimRequests"].([]waveAwaitReclaimRequest)
	if len(reclaims) != 1 || !containsStr(reclaims[0].TaskIDs, "B2") || containsStr(reclaims[0].TaskIDs, "B1") {
		t.Fatalf("expected exactly one reclaim request naming only B2 (the subject's only open member), got %+v", reclaims)
	}

	b1 := waveAwaitLoadServerState(t, root, runID, "B1")
	if b1.ReclaimRequestedAt == "" {
		t.Error("expected B1's server file (closed but batchIndex 0) to be stamped along with the rest of the batch")
	}
	stampedAt := b1.ReclaimRequestedAt

	// Second call 30s later, threading state_file, must not re-stamp or
	// re-emit the reclaim request while still within grace.
	in.StateFile = *out.StateFile
	out2, err := execActionWaveAwait(root, in, fixedClock(testNow.Add(30*time.Second)))
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}
	reclaims2 := out2.Ext["reclaimRequests"].([]waveAwaitReclaimRequest)
	if len(reclaims2) != 0 {
		t.Errorf("expected no re-emitted reclaimRequests on duplicate call, got %+v", reclaims2)
	}
	b1Again := waveAwaitLoadServerState(t, root, runID, "B1")
	if b1Again.ReclaimRequestedAt != stampedAt {
		t.Errorf("B1 stamp changed across duplicate call: %q -> %q", stampedAt, b1Again.ReclaimRequestedAt)
	}
}

// ---------------------------------------------------------------------------
// No wall-clock loop budget (KD10)
// ---------------------------------------------------------------------------

func TestExecActionWaveAwait_NoWallClockLoopBudget(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1, []map[string]any{waveAwaitPlannedEntry("1")}, nil)},
	})
	waveAwaitStoreServerState(t, root, runID, "1", wave.ServerTaskState{
		DispatchedAt:     waveAwaitTs(-10 * time.Second), // fresh, well under its own totalTimeout
		WorkerName:       "worker-1",
		ContextFetchedAt: waveAwaitTs(-5 * time.Second),
		Attempt:          2, // a redispatch's fresh dispatchedAt
	})
	waveAwaitWriteProgress(t, root, runID, "1", wave.TaskProgress{
		Phase: "editing", UpdatedAt: waveAwaitTs(-2 * time.Second),
	})

	// Simulate a resume-state file whose own StartedAt is already far
	// beyond one waveTimeout (1800s) — if wave-await gated status on its
	// own loop budget, this call would incorrectly time out even though
	// task 1's own dispatchedAt is fresh.
	in := ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}
	first, err := execActionWaveAwait(root, in, fixedClock(testNow.Add(-2000*time.Second)))
	if err != nil {
		t.Fatalf("seed call: %v", err)
	}
	in.StateFile = *first.StateFile

	out, err := execActionWaveAwait(root, in, fixedClock(testNow))
	if err != nil {
		t.Fatalf("execActionWaveAwait: %v", err)
	}
	if out.Status != "pending" {
		t.Errorf("status = %q, want pending — task's own totalTimeout is independent of the poll loop's elapsed time", out.Status)
	}
	prog := out.Progress.(waveAwaitProgress)
	if prog.WaitedSeconds < 1900 {
		t.Errorf("expected WaitedSeconds to reflect the long-running loop (~2000s), got %d", prog.WaitedSeconds)
	}
	if !containsStr(prog.Open, "1") {
		t.Errorf("expected task 1 still open and healthy despite the loop's long elapsed time, got %+v", prog)
	}
}

// ---------------------------------------------------------------------------
// JSON contract: non-null slices
// ---------------------------------------------------------------------------

func TestExecActionWaveAwait_EmptyBucketsSerializeAsEmptyArraysNotNull(t *testing.T) {
	root := t.TempDir()
	runID := "run1"
	createExecState(t, root, "feat/test", map[string]any{
		"waves": []any{waveAwaitManifest(1, nil, nil)},
	})

	out, err := execActionWaveAwait(root, ExecuteStateIn{Branch: "feat/test", RunID: runID, Wave: intPtr(1)}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("execActionWaveAwait: %v", err)
	}
	if out.Status != "done" {
		t.Fatalf("status = %q, want done (empty wave)", out.Status)
	}

	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	progress := decoded["progress"].(map[string]any)
	for _, key := range []string{"open", "queued", "stalled", "recorded"} {
		v, ok := progress[key]
		if !ok || v == nil {
			t.Errorf("progress.%s must be [] not null/absent, got %v (raw=%s)", key, v, raw)
		}
		if arr, ok := v.([]any); !ok || arr == nil {
			t.Errorf("progress.%s must decode as a non-nil array, got %T %v", key, v, v)
		}
	}

	ext := decoded["ext"].(map[string]any)
	for _, key := range []string{"failed", "reclaimRequests"} {
		v, ok := ext[key]
		if !ok || v == nil {
			t.Errorf("ext.%s must be [] not null/absent, got %v (raw=%s)", key, v, raw)
		}
	}

	if decoded["next"] == nil || decoded["next"] == "" {
		t.Error("next must always be present (guardrail mcp-output-drives-behavior)")
	}
}

// TestWaveAwaitHarvest_SlicesNeverNil pins the same invariant the deleted
// TestExecActionWaveAwait_ResumeFromSlicesNeverNull used to check indirectly
// through a full wave-await payload — now checked directly against the
// function that actually owns the normalization, since a reclaim reply no
// longer builds a wave-await ResumeFrom at all (task-fail is the only
// caller left, see execute_state.go).
func TestWaveAwaitHarvest_SlicesNeverNil(t *testing.T) {
	rf := waveAwaitHarvest(wave.TaskProgress{})
	if rf.AcceptanceDone == nil || rf.FilesTouched == nil {
		t.Errorf("harvest must normalize nil slices to empty, got %+v", rf)
	}
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

func assertNonNilBuckets(t *testing.T, prog waveAwaitProgress) {
	t.Helper()
	if prog.Open == nil || prog.Queued == nil || prog.Stalled == nil || prog.Recorded == nil {
		t.Errorf("expected all progress buckets to be non-nil slices, got %+v", prog)
	}
}

func assertNonNilExt(t *testing.T, out WaveAwaitOut) {
	t.Helper()
	if out.Ext["failed"] == nil || out.Ext["reclaimRequests"] == nil {
		t.Errorf("expected ext.failed and ext.reclaimRequests to be non-nil, got %+v", out.Ext)
	}
}
