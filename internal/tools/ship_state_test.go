package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/pipeline"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ---------------------------------------------------------------------------
// history seam helpers — the durable deferred.json writes go through the
// historyWriter var, so tests substitute an in-memory writer and never touch
// a real history directory.
// ---------------------------------------------------------------------------

// useMemHistory points historyWriter at a fresh MemWriter for one test and
// restores the previous writer afterwards. tools tests never call
// t.Parallel(), so swapping a package var is safe here.
func useMemHistory(t *testing.T) *history.MemWriter {
	t.Helper()
	mem := &history.MemWriter{}
	prev := historyWriter
	historyWriter = func(string) history.Writer { return mem }
	t.Cleanup(func() { historyWriter = prev })
	return mem
}

// failingHistoryWriter fails every AddDeferred while leaving ListDeferred
// working, so the best-effort persistence path can be exercised without the
// dedupe read failing first.
type failingHistoryWriter struct{ history.MemWriter }

func (f *failingHistoryWriter) AddDeferred(history.DeferredIssue) error {
	return errors.New("no space left on device")
}

// useFailingHistory points historyWriter at a writer whose AddDeferred
// always fails, for the "persist failure is surfaced, not swallowed" cases.
func useFailingHistory(t *testing.T) {
	t.Helper()
	prev := historyWriter
	historyWriter = func(string) history.Writer { return &failingHistoryWriter{} }
	t.Cleanup(func() { historyWriter = prev })
}

// listFailingHistoryWriter fails ListDeferred — an unreadable or corrupt
// deferred.json. persistDeferred returns before AddDeferred in that case,
// so this covers the earlier of its two failure exits.
type listFailingHistoryWriter struct{ history.MemWriter }

func (f *listFailingHistoryWriter) ListDeferred() ([]history.DeferredIssue, error) {
	return nil, errors.New("deferred.json is not readable")
}

// useListFailingHistory points historyWriter at a writer whose ListDeferred
// always fails.
func useListFailingHistory(t *testing.T) {
	t.Helper()
	prev := historyWriter
	historyWriter = func(string) history.Writer { return &listFailingHistoryWriter{} }
	t.Cleanup(func() { historyWriter = prev })
}

// fixedNow returns a now func() time.Time pinned to a stable instant, so
// timestamp-bearing assertions don't race real wall-clock time.
func fixedNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// shipStateInitFixture creates a git+ship-state fixture with a scaffolded
// pipeline (via the init action) and returns the resulting state file path.
func shipStateInitFixture(t *testing.T, dir, branch string) string {
	t.Helper()
	out, err := shipState(dir, dir, ShipStateIn{
		Action:    "init",
		Detail:    map[string]any{"branch": branch},
		SessionID: "sess-init",
	}, fixedNow(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("init output = %#v, want map[string]any", out)
	}
	path, _ := m["filePath"].(string)
	if path == "" {
		t.Fatalf("init output missing filePath: %#v", m)
	}
	return path
}

func readStateData(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state file %s: %v", path, err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("unmarshal state file %s: %v", path, err)
	}
	return data
}

func setStepStatus(t *testing.T, path, stepName, status string, extra map[string]any) {
	t.Helper()
	data := readStateData(t, path)
	steps, _ := data["steps"].([]any)
	for _, s := range steps {
		sm, ok := s.(map[string]any)
		if !ok || sm["name"] != stepName {
			continue
		}
		sm["status"] = status
		for k, v := range extra {
			sm[k] = v
		}
	}
	data["steps"] = steps
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// setSideEffectEntry seeds data["sideEffects"][step] in the state file with a
// journal entry, mirroring setStepStatus's read-mutate-write pattern.
func setSideEffectEntry(t *testing.T, path, step, kind, ref string) {
	t.Helper()
	data := readStateData(t, path)
	journal, _ := data["sideEffects"].(map[string]any)
	if journal == nil {
		journal = map[string]any{}
	}
	journal[step] = map[string]any{
		"kind":       kind,
		"ref":        ref,
		"verifiedAt": "2026-01-01T00:00:00Z",
	}
	data["sideEffects"] = journal
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// init
// ---------------------------------------------------------------------------

func TestShipState_Init(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/state-init")

	path := shipStateInitFixture(t, dir, "feat/state-init")

	wantDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
	if filepath.Dir(path) != wantDir {
		t.Errorf("state file dir = %q, want %q", filepath.Dir(path), wantDir)
	}

	data := readStateData(t, path)
	for _, k := range []string{"version", "startedAt", "branch", "worktree", "flags", "steps", "decisions", "deferredFindings", "sessionId"} {
		if _, ok := data[k]; !ok {
			t.Errorf("state file missing key %q", k)
		}
	}
	if data["branch"] != "feat/state-init" {
		t.Errorf("branch = %v, want feat/state-init", data["branch"])
	}
	if data["sessionId"] != "sess-init" {
		t.Errorf("sessionId = %v, want sess-init", data["sessionId"])
	}
	steps, ok := data["steps"].([]any)
	if !ok || len(steps) != 6 {
		t.Fatalf("steps = %v, want a 6-entry scaffold", data["steps"])
	}
}

func TestShipState_Init_PrunesOrphans(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/prune-me")

	slug := state.SlugifyBranch("feat/prune-me")
	orphan := filepath.Join(dir, paths.DataDir, paths.RunsSubdir, fmt.Sprintf("ship-%s-20200101T000000Z.json", slug))
	writeFile(t, orphan, `{"sessionId": null}`)

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "init",
		Detail: map[string]any{"branch": "feat/prune-me"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	m := out.(map[string]any)
	pruned, _ := m["prunedOrphans"].([]string)
	if len(pruned) != 1 || pruned[0] != orphan {
		t.Errorf("prunedOrphans = %v, want [%s]", pruned, orphan)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan file %s still exists after prune, stat err = %v, want os.IsNotExist", orphan, err)
	}
}

// ---------------------------------------------------------------------------
// start / complete
// ---------------------------------------------------------------------------

func TestShipState_StartComplete(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/start-complete")
	path := shipStateInitFixture(t, dir, "feat/start-complete")

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "start",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/start-complete"},
	}, fixedNow(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("start: %v", err)
	}

	data := readStateData(t, path)
	step := findStepMap(t, data, "execute")
	if step["status"] != "in_progress" {
		t.Errorf("status = %v, want in_progress", step["status"])
	}
	if step["startedAt"] == nil {
		t.Error("startedAt not stamped")
	}

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "complete",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/start-complete", "result": "all waves done"},
	}, fixedNow(time.Date(2026, 1, 2, 1, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("complete: %v", err)
	}

	data = readStateData(t, path)
	step = findStepMap(t, data, "execute")
	if step["status"] != "completed" {
		t.Errorf("status = %v, want completed", step["status"])
	}
	if step["result"] != "all waves done" {
		t.Errorf("result = %v, want %q", step["result"], "all waves done")
	}
	if step["completedAt"] == nil {
		t.Error("completedAt not stamped")
	}
}

func findStepMap(t *testing.T, data map[string]any, name string) map[string]any {
	t.Helper()
	steps, _ := data["steps"].([]any)
	for _, s := range steps {
		sm, ok := s.(map[string]any)
		if ok && sm["name"] == name {
			return sm
		}
	}
	t.Fatalf("step %q not found in %v", name, steps)
	return nil
}

// ---------------------------------------------------------------------------
// begin-step proceed-gate (R-b1)
// ---------------------------------------------------------------------------

func TestShipState_BeginStep_BlockedByPendingNonConditionalStep(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/gate-block")
	shipStateInitFixture(t, dir, "feat/gate-block")

	// "execute" (steps[0]) is still pending with no condition — must block
	// "commit" (steps[1]).
	_, err := shipState(dir, dir, ShipStateIn{
		Action: "begin-step",
		Step:   "commit",
		Detail: map[string]any{"branch": "feat/gate-block"},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("begin-step commit: want error (execute is still pending), got nil")
	}
	if !isDomainError(err) {
		t.Errorf("error = %v (%T), want a DomainError", err, err)
	}
}

func TestShipState_BeginStep_SkippedStepDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/gate-skip")
	path := shipStateInitFixture(t, dir, "feat/gate-skip")

	setStepStatus(t, path, "execute", "skipped", map[string]any{"completedAt": "2026-01-01T00:00:00Z"})

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "begin-step",
		Step:   "commit",
		Detail: map[string]any{"branch": "feat/gate-skip"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("begin-step commit: %v (skipped predecessor must not block)", err)
	}
	narrOut, ok := out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipStepNarrationOut", out)
	}
	if len(narrOut.Todos) == 0 {
		t.Error("Todos is empty, want a rendered todo list")
	}
	if narrOut.Summary == "" {
		t.Error("Summary is empty, want a narration summary")
	}
}

func TestShipState_BeginStep_ConditionalPendingDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/gate-conditional")
	path := shipStateInitFixture(t, dir, "feat/gate-conditional")

	// Mark execute/commit/review completed so we reach the conditional
	// steps: received-review (pending, has a condition key) must not block
	// commit-fixes from beginning.
	for _, name := range []string{"execute", "commit", "review"} {
		setStepStatus(t, path, name, "completed", map[string]any{"completedAt": "2026-01-01T00:00:00Z"})
	}

	data := readStateData(t, path)
	rr := findStepMap(t, data, "received-review")
	if _, hasCondition := rr["condition"]; !hasCondition {
		t.Fatal("received-review fixture is missing its condition key — InitialShipSteps() scaffold changed?")
	}

	_, err := shipState(dir, dir, ShipStateIn{
		Action: "begin-step",
		Step:   "commit-fixes",
		Detail: map[string]any{"branch": "feat/gate-conditional"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("begin-step commit-fixes: %v (conditional pending received-review must not block)", err)
	}
}

func TestShipState_BeginStep_InProgressPredecessorBlocks(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/gate-inprogress")
	path := shipStateInitFixture(t, dir, "feat/gate-inprogress")

	setStepStatus(t, path, "execute", "in_progress", map[string]any{"startedAt": "2026-01-01T00:00:00Z"})

	_, err := shipState(dir, dir, ShipStateIn{
		Action: "begin-step",
		Step:   "commit",
		Detail: map[string]any{"branch": "feat/gate-inprogress"},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("begin-step commit: want error (execute still in_progress), got nil")
	}
}

// TestShipState_BeginStep_FailedPredecessorBlocks completes the proceed-gate
// (R-b1) truth table: a "failed" predecessor step blocks exactly like
// "in_progress" does, and is never terminal-OK.
func TestShipState_BeginStep_FailedPredecessorBlocks(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/gate-failed")
	path := shipStateInitFixture(t, dir, "feat/gate-failed")

	setStepStatus(t, path, "execute", "failed", map[string]any{"error": "boom"})

	_, err := shipState(dir, dir, ShipStateIn{
		Action: "begin-step",
		Step:   "commit",
		Detail: map[string]any{"branch": "feat/gate-failed"},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("begin-step commit: want error (execute failed), got nil")
	}
}

// TestShipState_BeginStep_AlreadyDoneWhenJournalEntryExists verifies that
// begin-step reports AlreadyDone:true for a step that already has a
// verified sideEffects journal entry (written by ship_verify_side_effect),
// so a resumed pipeline knows it can skip redoing that step's side effect.
func TestShipState_BeginStep_AlreadyDoneWhenJournalEntryExists(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/already-done")
	path := shipStateInitFixture(t, dir, "feat/already-done")

	setSideEffectEntry(t, path, "execute", "sha", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "begin-step",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/already-done"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("begin-step execute: %v", err)
	}
	narrOut, ok := out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipStepNarrationOut", out)
	}
	if !narrOut.AlreadyDone {
		t.Error("AlreadyDone = false, want true (sideEffects journal has an entry for this step)")
	}
}

// TestShipState_BeginStep_AlreadyDoneFalseWithNoJournalEntry verifies
// AlreadyDone stays false (the common case) when no sideEffects journal
// entry exists for the step being begun.
func TestShipState_BeginStep_AlreadyDoneFalseWithNoJournalEntry(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/not-already-done")
	shipStateInitFixture(t, dir, "feat/not-already-done")

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "begin-step",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/not-already-done"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("begin-step execute: %v", err)
	}
	narrOut, ok := out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipStepNarrationOut", out)
	}
	if narrOut.AlreadyDone {
		t.Error("AlreadyDone = true, want false (no sideEffects journal entry exists)")
	}
}

// ---------------------------------------------------------------------------
// complete-step outcomes
// ---------------------------------------------------------------------------

func TestShipState_CompleteStep_SuccessAndFailure(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/complete-step")
	path := shipStateInitFixture(t, dir, "feat/complete-step")

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "complete-step",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/complete-step", "outcome": "success", "result": "ok"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("complete-step success: %v", err)
	}
	narr, ok := out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipStepNarrationOut", out)
	}
	if !strings.Contains(narr.Summary, "completed") {
		t.Errorf("success summary missing 'completed': %q", narr.Summary)
	}
	data := readStateData(t, path)
	step := findStepMap(t, data, "execute")
	if step["status"] != "completed" || step["result"] != "ok" {
		t.Errorf("step = %v, want status=completed result=ok", step)
	}

	failOut, err := shipState(dir, dir, ShipStateIn{
		Action: "complete-step",
		Step:   "commit",
		Detail: map[string]any{"branch": "feat/complete-step", "outcome": "failure", "result": "boom"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("complete-step failure: %v", err)
	}
	failNarr, ok := failOut.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("failure output = %#v, want ShipStepNarrationOut", failOut)
	}
	if !strings.Contains(failNarr.Summary, "failed") {
		t.Errorf("failure summary missing 'failed': %q", failNarr.Summary)
	}
	// Failed step is itself blocking, so Next must point back at it.
	if failNarr.Next == nil {
		t.Fatal("failure Next is nil, want step that failed")
	}
	if failNarr.Next.ID != "commit" {
		t.Errorf("failure Next.ID = %q, want 'commit'", failNarr.Next.ID)
	}
	data = readStateData(t, path)
	step = findStepMap(t, data, "commit")
	if step["status"] != "failed" {
		t.Errorf("status = %v, want failed", step["status"])
	}
	if step["error"] != "boom" {
		t.Errorf("error = %v, want %q (failure outcome must store under 'error', not 'result')", step["error"], "boom")
	}
	if _, hasResult := step["result"]; hasResult {
		t.Errorf("step unexpectedly has a 'result' key on a failure outcome: %v", step)
	}
}

func TestShipState_CompleteStep_InvalidOutcomeRejected(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/bad-outcome")
	shipStateInitFixture(t, dir, "feat/bad-outcome")

	_, err := shipState(dir, dir, ShipStateIn{
		Action: "complete-step",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/bad-outcome", "outcome": "maybe"},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("want error for outcome=maybe, got nil")
	}
}

// TestShipState_CompleteStep_IssueSummary confirms complete-step's response
// carries issueCount/issueHighlights once the state has recorded issues, and
// omits them (zero value) when there are none.
func TestShipState_CompleteStep_IssueSummary(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/complete-step-issues")
	shipStateInitFixture(t, dir, "feat/complete-step-issues")

	// No issues yet: complete-step response must not carry a count/highlights.
	out, err := shipState(dir, dir, ShipStateIn{
		Action: "complete-step",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/complete-step-issues", "outcome": "failure", "result": "boom"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("complete-step failure: %v", err)
	}
	narrOut, ok := out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipStepNarrationOut", out)
	}
	if narrOut.IssueCount != 0 || len(narrOut.IssueHighlights) != 0 {
		t.Errorf("IssueCount/IssueHighlights = %d/%v, want 0/empty (complete-step itself records no issue)", narrOut.IssueCount, narrOut.IssueHighlights)
	}

	// fail records an issue against "execute".
	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "fail",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/complete-step-issues", "error": "boom"},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("fail: %v", err)
	}

	// Now complete-step's response must surface the accumulated issue.
	out, err = shipState(dir, dir, ShipStateIn{
		Action: "complete-step",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/complete-step-issues", "outcome": "success"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("complete-step: %v", err)
	}
	narrOut, ok = out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipStepNarrationOut", out)
	}
	if narrOut.IssueCount != 1 {
		t.Errorf("IssueCount = %d, want 1", narrOut.IssueCount)
	}
	wantHighlight := "[error] Step execute failed"
	if len(narrOut.IssueHighlights) != 1 || narrOut.IssueHighlights[0] != wantHighlight {
		t.Errorf("IssueHighlights = %v, want [%q]", narrOut.IssueHighlights, wantHighlight)
	}
}

// ---------------------------------------------------------------------------
// skip / fail / decide / defer / read
// ---------------------------------------------------------------------------

func TestShipState_Skip(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/skip")
	path := shipStateInitFixture(t, dir, "feat/skip")

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "skip",
		Step:   "received-review",
		Detail: map[string]any{"branch": "feat/skip", "reason": "no findings"},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("skip: %v", err)
	}
	data := readStateData(t, path)
	step := findStepMap(t, data, "received-review")
	if step["status"] != "skipped" {
		t.Errorf("status = %v, want skipped", step["status"])
	}
	if step["reason"] != "no findings" {
		t.Errorf("reason = %v, want %q", step["reason"], "no findings")
	}
	if step["completedAt"] == nil {
		t.Error("completedAt not stamped for a skipped step")
	}
}

func TestShipState_Fail(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/fail")
	path := shipStateInitFixture(t, dir, "feat/fail")

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "fail",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/fail", "error": "wave 2 crashed"},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("fail: %v", err)
	}
	data := readStateData(t, path)
	step := findStepMap(t, data, "execute")
	if step["status"] != "failed" {
		t.Errorf("status = %v, want failed", step["status"])
	}
	if step["error"] != "wave 2 crashed" {
		t.Errorf("error = %v, want %q", step["error"], "wave 2 crashed")
	}
	if data["lastFailedStep"] != "execute" {
		t.Errorf("lastFailedStep = %v, want execute", data["lastFailedStep"])
	}
	issues, _ := data["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want 1 entry", issues)
	}
	issue, _ := issues[0].(map[string]any)
	if issue["step"] != "execute" || issue["severity"] != "error" || issue["category"] != "ship-fail" {
		t.Errorf("issue = %v, want step=execute severity=error category=ship-fail", issue)
	}
	if issue["detail"] != "wave 2 crashed" {
		t.Errorf("issue detail = %v, want %q", issue["detail"], "wave 2 crashed")
	}
	if issue["timestamp"] == nil || issue["timestamp"] == "" {
		t.Error("issue timestamp not stamped")
	}
}

// TestShipState_Fail_NoIssuesOnPreExistingStateFile confirms a ship state
// file written before issues[] existed still loads and fails cleanly,
// getting a fresh issues[] array rather than erroring.
func TestShipState_Fail_BackwardCompatNoIssuesArray(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/fail-no-issues")
	path := shipStateInitFixture(t, dir, "feat/fail-no-issues")

	// Simulate a pre-Task-17 state file: no "issues" key at all.
	data := readStateData(t, path)
	delete(data, "issues")
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "fail",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/fail-no-issues", "error": "boom"},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("fail: %v", err)
	}

	data = readStateData(t, path)
	issues, _ := data["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("issues = %v, want 1 entry", issues)
	}
}

// ---------------------------------------------------------------------------
// fail history row — shipHistoryAppendFunc and shipStateWriteFunc seams
// ---------------------------------------------------------------------------

// useShipHistoryAppend replaces shipHistoryAppendFunc for one test and
// restores it afterwards. tools tests never call t.Parallel(), so swapping a
// package var is safe here.
func useShipHistoryAppend(t *testing.T, fn func(root string, rec history.RunRecord) error) {
	t.Helper()
	prev := shipHistoryAppendFunc
	shipHistoryAppendFunc = fn
	t.Cleanup(func() { shipHistoryAppendFunc = prev })
}

// useShipStateWrite replaces shipStateWriteFunc for one test and restores it
// afterwards.
func useShipStateWrite(t *testing.T, fn func(st *state.State) error) {
	t.Helper()
	prev := shipStateWriteFunc
	shipStateWriteFunc = fn
	t.Cleanup(func() { shipStateWriteFunc = prev })
}

// recordShipHistoryAppends points shipHistoryAppendFunc at a recorder that
// returns appendErr (nil for success) and returns the records it saw.
func recordShipHistoryAppends(t *testing.T, appendErr error) *[]history.RunRecord {
	t.Helper()
	var rows []history.RunRecord
	useShipHistoryAppend(t, func(_ string, rec history.RunRecord) error {
		rows = append(rows, rec)
		return appendErr
	})
	return &rows
}

// shipFail runs the fail action for step on branch at the given instant.
func shipFail(dir, branch, step string, at time.Time) (any, error) {
	return shipState(dir, dir, ShipStateIn{
		Action: "fail",
		Step:   step,
		Detail: map[string]any{"branch": branch, "error": "boom"},
	}, fixedNow(at))
}

// TestShipState_Fail_HistoryRow_FirstFailAppendsOne pins the failure row's
// content and that the flag reaches disk. init stamps startedAt
// 2026-01-01T00:00:00Z, so a fail 30 minutes later gives 1800000 ms.
func TestShipState_Fail_HistoryRow_FirstFailAppendsOne(t *testing.T) {
	dir, path := deferFixture(t, "feat/fail-row")
	rows := recordShipHistoryAppends(t, nil)
	at := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)

	out, err := shipFail(dir, "feat/fail-row", "execute", at)
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	n, ok := out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipStepNarrationOut", out)
	}
	if len(n.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", n.Warnings)
	}

	want := history.RunRecord{
		Timestamp:  "2026-01-01T00:30:00Z",
		Skill:      "ship",
		Branch:     "feat/fail-row",
		Outcome:    "failure",
		DurationMs: 1800000,
		StartedAt:  "2026-01-01T00:00:00Z",
	}
	if len(*rows) != 1 || !reflect.DeepEqual((*rows)[0], want) {
		t.Fatalf("appended rows = %+v, want exactly [%+v]", *rows, want)
	}

	data := readStateData(t, path)
	if data["historyFailureRecorded"] != true {
		t.Errorf("historyFailureRecorded = %v, want true", data["historyFailureRecorded"])
	}
	if step := findStepMap(t, data, "execute"); step["status"] != "failed" {
		t.Errorf("step status = %v, want failed", step["status"])
	}
}

// TestShipState_Fail_HistoryRow_WritesRunsJSONL runs fail through the real
// FileWriter (the production seam value) to prove the row lands in runs.jsonl.
func TestShipState_Fail_HistoryRow_WritesRunsJSONL(t *testing.T) {
	dir, _ := deferFixture(t, "feat/fail-runs-file")

	if _, err := shipFail(dir, "feat/fail-runs-file", "execute", time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)); err != nil {
		t.Fatalf("fail: %v", err)
	}

	raw, err := os.ReadFile(history.NewFileWriter(historyDir(dir)).RunsPath())
	if err != nil {
		t.Fatalf("read runs.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("runs.jsonl lines = %d, want 1: %q", len(lines), raw)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("unmarshal row: %v", err)
	}
	if got["skill"] != "ship" || got["outcome"] != "failure" || got["branch"] != "feat/fail-runs-file" ||
		got["ts"] != "2026-01-01T00:30:00Z" || got["started_at"] != "2026-01-01T00:00:00Z" || got["duration_ms"] != float64(1800000) {
		t.Errorf("row = %v, want the failure row for feat/fail-runs-file", got)
	}
}

// TestShipState_Fail_HistoryRow_SecondFailAppendsNothing proves the flag stops
// a second row when the same run fails again, on the same step or another.
func TestShipState_Fail_HistoryRow_SecondFailAppendsNothing(t *testing.T) {
	dir, path := deferFixture(t, "feat/fail-twice")
	rows := recordShipHistoryAppends(t, nil)
	at := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)

	for _, step := range []string{"execute", "execute", "review"} {
		if _, err := shipFail(dir, "feat/fail-twice", step, at); err != nil {
			t.Fatalf("fail %s: %v", step, err)
		}
	}

	if len(*rows) != 1 {
		t.Errorf("appended rows = %d, want 1 for three fail calls in one run", len(*rows))
	}
	if readStateData(t, path)["historyFailureRecorded"] != true {
		t.Error("historyFailureRecorded not true after the fails")
	}
}

// TestShipState_Fail_HistoryRow_StateWriteFailure covers the first failure
// point: the state write fails, so the flag is not persisted and no row
// exists. A retry once the write works appends the one row.
func TestShipState_Fail_HistoryRow_StateWriteFailure(t *testing.T) {
	dir, path := deferFixture(t, "feat/fail-write")
	rows := recordShipHistoryAppends(t, nil)
	prevWrite := shipStateWriteFunc
	useShipStateWrite(t, func(*state.State) error { return errors.New("no space left on device") })
	at := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)

	_, err := shipFail(dir, "feat/fail-write", "execute", at)
	var infra *mcpserver.InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("err = %v, want *mcpserver.InfraError", err)
	}
	if infra.Suggestion == "" {
		t.Error("InfraError.Suggestion is empty, want a recovery hint")
	}
	if !strings.Contains(infra.Msg, "no space left on device") {
		t.Errorf("InfraError.Msg = %q, want it to carry the write error", infra.Msg)
	}

	if len(*rows) != 0 {
		t.Errorf("appended rows = %d after a state write failure, want 0", len(*rows))
	}
	data := readStateData(t, path)
	if _, set := data["historyFailureRecorded"]; set {
		t.Errorf("historyFailureRecorded = %v on disk, want absent after a failed write", data["historyFailureRecorded"])
	}
	if step := findStepMap(t, data, "execute"); step["status"] == "failed" {
		t.Error("step status is failed on disk after a failed write, want unchanged")
	}

	// A retry with a working write appends the row: the failed attempt left no flag.
	useShipStateWrite(t, prevWrite)
	if _, err := shipFail(dir, "feat/fail-write", "execute", at); err != nil {
		t.Fatalf("retry fail: %v", err)
	}
	if len(*rows) != 1 {
		t.Errorf("appended rows after retry = %d, want 1", len(*rows))
	}
}

// TestShipState_Fail_HistoryRow_AppendFailure covers the second failure
// point: the append fails after the state write. The action still succeeds,
// the step stays failed, the warning names the runs path, and a retry adds no
// second row.
func TestShipState_Fail_HistoryRow_AppendFailure(t *testing.T) {
	dir, path := deferFixture(t, "feat/fail-append")
	rows := recordShipHistoryAppends(t, errors.New("disk quota exceeded"))
	at := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)

	out, err := shipFail(dir, "feat/fail-append", "execute", at)
	if err != nil {
		t.Fatalf("fail must not return an error when the history append fails: %v", err)
	}
	n, ok := out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipStepNarrationOut", out)
	}
	if len(n.Warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly 1", n.Warnings)
	}
	for _, want := range []string{paths.DataDir + "/history/runs.jsonl", "disk quota exceeded", "history_record", `"failure"`} {
		if !strings.Contains(n.Warnings[0], want) {
			t.Errorf("warning = %q, want it to contain %q", n.Warnings[0], want)
		}
	}

	data := readStateData(t, path)
	if step := findStepMap(t, data, "execute"); step["status"] != "failed" {
		t.Errorf("step status = %v, want failed", step["status"])
	}
	if data["historyFailureRecorded"] != true {
		t.Errorf("historyFailureRecorded = %v, want true so a retry adds no second row", data["historyFailureRecorded"])
	}

	out, err = shipFail(dir, "feat/fail-append", "execute", at)
	if err != nil {
		t.Fatalf("retry fail: %v", err)
	}
	if retry := out.(ShipStepNarrationOut); len(retry.Warnings) != 0 {
		t.Errorf("retry warnings = %v, want none", retry.Warnings)
	}
	if len(*rows) != 1 {
		t.Errorf("append attempts = %d, want 1 (the retry must not append)", len(*rows))
	}
}

// TestShipFailDurationMs covers both branches of the duration helper.
func TestShipFailDurationMs(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)
	cases := []struct {
		name      string
		startedAt string
		want      int64
	}{
		{"parses", "2026-01-01T00:00:00Z", 1800000},
		{"empty", "", 0},
		{"not a timestamp", "yesterday", 0},
	}
	for _, c := range cases {
		if got := shipFailDurationMs(c.startedAt, now); got != c.want {
			t.Errorf("%s: shipFailDurationMs(%q) = %d, want %d", c.name, c.startedAt, got, c.want)
		}
	}
}

// TestShipState_Fail_HistoryRow_UnparseableStartedAtGivesZeroDuration proves
// the handler passes the state's startedAt through the helper: a state whose
// startedAt does not parse still yields a row, with duration_ms 0.
func TestShipState_Fail_HistoryRow_UnparseableStartedAtGivesZeroDuration(t *testing.T) {
	dir, path := deferFixture(t, "feat/fail-bad-start")
	rows := recordShipHistoryAppends(t, nil)

	data := readStateData(t, path)
	data["startedAt"] = "not-a-timestamp"
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	if _, err := shipFail(dir, "feat/fail-bad-start", "execute", time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if len(*rows) != 1 || (*rows)[0].DurationMs != 0 || (*rows)[0].StartedAt != "not-a-timestamp" {
		t.Errorf("rows = %+v, want one row with duration_ms 0", *rows)
	}
}

// TestShipStateSchema_HistoryFailureRecorded proves the published schema
// accepts a state with and without the flag, rejects a non-boolean flag, and
// accepts the state file the fail action actually writes.
func TestShipStateSchema_HistoryFailureRecorded(t *testing.T) {
	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc", "schemas", "ship-state.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	sch, err := jsonschema.NewCompiler().Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	validate := func(t *testing.T, doc map[string]any) error {
		t.Helper()
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal doc: %v", err)
		}
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
		if err != nil {
			t.Fatalf("unmarshal doc for schema validation: %v", err)
		}
		return sch.Validate(inst)
	}
	base := func(extra map[string]any) map[string]any {
		doc := map[string]any{
			"version":   float64(1),
			"startedAt": "2026-03-01T12:00:00Z",
			"branch":    "feat/schema-test",
			"flags":     map[string]any{},
			"steps":     []any{map[string]any{"name": "review", "status": "failed"}},
		}
		for k, v := range extra {
			doc[k] = v
		}
		return doc
	}

	if err := validate(t, base(nil)); err != nil {
		t.Errorf("state without the flag: want accepted, got %v", err)
	}
	for _, v := range []bool{true, false} {
		if err := validate(t, base(map[string]any{"historyFailureRecorded": v})); err != nil {
			t.Errorf("historyFailureRecorded=%v: want accepted, got %v", v, err)
		}
	}
	if err := validate(t, base(map[string]any{"historyFailureRecorded": "yes"})); err == nil {
		t.Error(`historyFailureRecorded="yes": want schema rejection, got nil`)
	}

	// The state file fail writes must validate, flag included.
	dir, path := deferFixture(t, "feat/schema-fail")
	recordShipHistoryAppends(t, nil)
	if _, err := shipFail(dir, "feat/schema-fail", "execute", time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if err := validate(t, readStateData(t, path)); err != nil {
		t.Errorf("state file written by fail: schema rejected it: %v", err)
	}
}

func TestShipState_Decide(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/decide")
	path := shipStateInitFixture(t, dir, "feat/decide")

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "decide",
		Step:   "review",
		Detail: map[string]any{"branch": "feat/decide", "text": "skip perf pass, low risk"},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("decide: %v", err)
	}
	data := readStateData(t, path)
	decisions, _ := data["decisions"].([]any)
	if len(decisions) != 1 {
		t.Fatalf("decisions = %v, want 1 entry", decisions)
	}
	d, _ := decisions[0].(map[string]any)
	if d["step"] != "review" || d["decision"] != "skip perf pass, low risk" {
		t.Errorf("decision entry = %v, want step=review decision=%q", d, "skip perf pass, low risk")
	}
}

// TestShipStateDecideAt confirms decide stamps each appended decision with
// "at", the injected clock's time formatted as RFC 3339 UTC — mirroring the
// clock-seam pattern shipStateDefer already follows.
func TestShipStateDecideAt(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/decide-at")
	path := shipStateInitFixture(t, dir, "feat/decide-at")

	fixedTime := time.Date(2026, 3, 1, 12, 30, 0, 0, time.UTC)
	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "decide",
		Step:   "review",
		Detail: map[string]any{"branch": "feat/decide-at", "text": "skip perf pass, low risk"},
	}, fixedNow(fixedTime)); err != nil {
		t.Fatalf("decide: %v", err)
	}
	data := readStateData(t, path)
	decisions, _ := data["decisions"].([]any)
	if len(decisions) != 1 {
		t.Fatalf("decisions = %v, want 1 entry", decisions)
	}
	d, _ := decisions[0].(map[string]any)
	wantAt := fixedTime.UTC().Format(time.RFC3339)
	if d["at"] != wantAt {
		t.Errorf("decision at = %v, want %q", d["at"], wantAt)
	}

	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc", "schemas", "ship-state.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	sch, err := jsonschema.NewCompiler().Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	minimalDoc := map[string]any{
		"version":   float64(1),
		"startedAt": "2026-03-01T12:00:00Z",
		"branch":    "feat/decide-at",
		"flags":     map[string]any{},
		"steps": []any{
			map[string]any{"name": "review", "status": "completed"},
		},
		"decisions": decisions,
	}
	raw, err := json.Marshal(minimalDoc)
	if err != nil {
		t.Fatalf("marshal minimal doc: %v", err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("unmarshal minimal doc for schema validation: %v", err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("ship state with decide-recorded 'at' failed schema validation: %v", err)
	}
}

func TestShipState_Defer(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/defer")
	path := shipStateInitFixture(t, dir, "feat/defer")

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{
			"branch": "feat/defer", "severity": "medium", "file": "internal/foo.go",
			"line": float64(42), "title": "unchecked error",
		},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("defer: %v", err)
	}
	data := readStateData(t, path)
	findings, _ := data["deferredFindings"].([]any)
	if len(findings) != 1 {
		t.Fatalf("deferredFindings = %v, want 1 entry", findings)
	}
	f, _ := findings[0].(map[string]any)
	if f["severity"] != "medium" || f["file"] != "internal/foo.go" || f["title"] != "unchecked error" {
		t.Errorf("finding = %v, want severity=medium file=internal/foo.go title=%q", f, "unchecked error")
	}
}

func TestShipState_Defer_RequiresFields(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/defer-missing")
	shipStateInitFixture(t, dir, "feat/defer-missing")

	_, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{"branch": "feat/defer-missing", "severity": "medium"},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("defer without file/title: want error, got nil")
	}
}

// deferFixture creates a git-backed ship state on branch and returns the
// repo dir plus the state-file path, so the defer-persistence cases below
// share one setup.
func deferFixture(t *testing.T, branch string) (dir, statePath string) {
	t.Helper()
	dir = t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, branch)
	return dir, shipStateInitFixture(t, dir, branch)
}

func TestShipState_Defer_PersistsToDeferredHistory(t *testing.T) {
	dir, path := deferFixture(t, "feat/defer-persist")
	mem := useMemHistory(t)
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{
			"branch": "feat/defer-persist", "severity": "High", "file": "internal/foo.go",
			"line": float64(42), "title": "unchecked error",
		},
	}, fixedNow(now))
	if err != nil {
		t.Fatalf("defer: %v", err)
	}

	if len(mem.Deferred) != 1 {
		t.Fatalf("deferred.json entries = %d, want 1", len(mem.Deferred))
	}
	got := mem.Deferred[0]
	want := history.DeferredIssue{
		ID:          "review-deferred-2026-03-04T05:06:07Z-1",
		Created:     "2026-03-04T05:06:07Z",
		Source:      history.SourceReviewBelowThreshold,
		Priority:    history.PriorityHigh,
		Description: "unchecked error",
		Status:      history.StatusOpen,
		Severity:    "high",
		File:        "internal/foo.go",
		Line:        42,
		Reason:      history.ReasonBelowThreshold,
	}
	if got != want {
		t.Errorf("deferred entry =\n %+v\nwant\n %+v", got, want)
	}

	// The state entry records the same normalized severity and the same
	// parsed line as deferred.json — one input must never leave two
	// durable records that disagree.
	f := deferredStateEntry(t, path, 0)
	if f["severity"] != "high" {
		t.Errorf("state finding severity = %v, want the normalized %q", f["severity"], "high")
	}
	if f["line"] != float64(42) {
		t.Errorf("state finding line = %#v, want 42", f["line"])
	}

	// A mutating call names the resource it created (the id a later
	// deferred_resolve needs) and where it landed.
	n, ok := out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipStepNarrationOut", out)
	}
	if !strings.Contains(n.Summary, want.ID) {
		t.Errorf("summary = %q, want it to name the generated id %q", n.Summary, want.ID)
	}
	if !strings.Contains(n.Summary, "deferred.json") {
		t.Errorf("summary = %q, want it to name the file written", n.Summary)
	}
}

// TestShipState_Defer_SourceOverride pins P4: a caller can name its own
// source instead of getting the hardcoded review-below-threshold value —
// received-review needs this to record its own wont-fix/disagree/
// needs-direction findings under a source that names it, not review.
func TestShipState_Defer_SourceOverride(t *testing.T) {
	dir, _ := deferFixture(t, "feat/defer-source-override")
	mem := useMemHistory(t)

	_, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{
			"branch": "feat/defer-source-override", "severity": "medium",
			"file": "internal/foo.go", "title": "wont fix this", "source": "received-review",
		},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("defer: %v", err)
	}
	if len(mem.Deferred) != 1 {
		t.Fatalf("deferred.json entries = %d, want 1", len(mem.Deferred))
	}
	if got := mem.Deferred[0].Source; got != history.SourceReceivedReview {
		t.Errorf("Source = %q, want %q", got, history.SourceReceivedReview)
	}
}

// TestShipState_Defer_StatelessFallback pins E10: a branch with no ship
// state file (never ran ship_state init) must still be able to defer a
// finding — history.json is the only durable store, so it becomes the
// sole target instead of failing outright.
func TestShipState_Defer_StatelessFallback(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/defer-stateless")
	mem := useMemHistory(t)

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{
			"branch": "feat/defer-stateless", "severity": "medium",
			"file": "internal/foo.go", "title": "unchecked error",
		},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("defer with no ship state: want success via the history-only fallback, got: %v", err)
	}
	if len(mem.Deferred) != 1 {
		t.Fatalf("deferred.json entries = %d, want 1", len(mem.Deferred))
	}
	if got := mem.Deferred[0].Source; got != history.SourceReviewBelowThreshold {
		t.Errorf("Source = %q, want the default %q", got, history.SourceReviewBelowThreshold)
	}

	n, ok := out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipStepNarrationOut", out)
	}
	if !strings.Contains(n.Summary, "deferred.json") {
		t.Errorf("summary = %q, want it to name the file written", n.Summary)
	}
}

// TestShipState_Defer_StatelessFallback_IDCountsExistingHistory pins the
// id-counter half of the stateless path: with no run-scoped
// deferredFindings slice to measure, the running count must come from
// history's own ListDeferred instead of resetting to 1 on every call.
func TestShipState_Defer_StatelessFallback_IDCountsExistingHistory(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/defer-stateless-count")
	mem := useMemHistory(t)

	detail := map[string]any{
		"branch": "feat/defer-stateless-count", "severity": "low",
		"file": "internal/foo.go", "title": "first",
	}
	if _, err := shipState(dir, dir, ShipStateIn{Action: "defer", Detail: detail}, fixedNow(time.Now())); err != nil {
		t.Fatalf("first defer: %v", err)
	}
	detail["title"] = "second"
	if _, err := shipState(dir, dir, ShipStateIn{Action: "defer", Detail: detail}, fixedNow(time.Now())); err != nil {
		t.Fatalf("second defer: %v", err)
	}

	if len(mem.Deferred) != 2 {
		t.Fatalf("deferred.json entries = %d, want 2", len(mem.Deferred))
	}
	if !strings.HasSuffix(mem.Deferred[0].ID, "-1") {
		t.Errorf("first id = %q, want it to end in -1", mem.Deferred[0].ID)
	}
	if !strings.HasSuffix(mem.Deferred[1].ID, "-2") {
		t.Errorf("second id = %q, want it to end in -2", mem.Deferred[1].ID)
	}
}

// deferredStateEntry reads deferredFindings[i] out of the state file at
// path. Several defer cases below assert on the run-scoped record as well
// as the durable one.
func deferredStateEntry(t *testing.T, path string, i int) map[string]any {
	t.Helper()
	findings, _ := readStateData(t, path)["deferredFindings"].([]any)
	if len(findings) <= i {
		t.Fatalf("deferredFindings = %v, want at least %d entries", findings, i+1)
	}
	f, ok := findings[i].(map[string]any)
	if !ok {
		t.Fatalf("deferredFindings[%d] = %#v, want an object", i, findings[i])
	}
	return f
}

// TestShipState_Defer_AcceptsInfoSeverity pins the severity the default
// threshold actually defers: reviewThreshold defaults to "low", so "info"
// is the severity ship review routes below threshold on a default run. It
// must be accepted by the handler and by ship-state.schema.json.
func TestShipState_Defer_AcceptsInfoSeverity(t *testing.T) {
	dir, path := deferFixture(t, "feat/defer-info")
	mem := useMemHistory(t)

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{
			"branch": "feat/defer-info", "severity": "info", "file": "a.go",
			"title": "naming could be clearer",
		},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("defer with severity info: %v", err)
	}
	if len(mem.Deferred) != 1 {
		t.Fatalf("deferred.json entries = %d, want 1", len(mem.Deferred))
	}
	if got := mem.Deferred[0].Severity; got != "info" {
		t.Errorf("deferred.json severity = %q, want %q", got, "info")
	}
	if got := mem.Deferred[0].Priority; got != history.PriorityLow {
		t.Errorf("priority = %q, want %q", got, history.PriorityLow)
	}
	if f := deferredStateEntry(t, path, 0); f["severity"] != "info" {
		t.Errorf("state finding severity = %v, want %q", f["severity"], "info")
	}
}

// TestShipState_Defer_RejectsUnknownSeverity covers the symmetric half of
// the reason validation: an unrecognised severity used to be stored raw in
// both records while priorityFromSeverity silently bucketed it as medium.
func TestShipState_Defer_RejectsUnknownSeverity(t *testing.T) {
	dir, path := deferFixture(t, "feat/defer-bad-severity")
	mem := useMemHistory(t)

	_, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{
			"branch": "feat/defer-bad-severity", "severity": "blocker", "file": "a.go",
			"title": "nit",
		},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal(`defer with severity "blocker": want error, got nil`)
	}
	if !isDomainError(err) {
		t.Errorf("error = %v (%T), want DomainError", err, err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "blocker") {
		t.Errorf("error message does not name the rejected value: %s", msg)
	}
	for _, want := range dimensions.ValidSeverities {
		if !strings.Contains(msg, want) {
			t.Errorf("error message does not name accepted severity %q: %s", want, msg)
		}
	}
	if len(mem.Deferred) != 0 {
		t.Errorf("deferred entries = %d, want 0 — a rejected call must not persist", len(mem.Deferred))
	}
	if findings, _ := readStateData(t, path)["deferredFindings"].([]any); len(findings) != 0 {
		t.Errorf("deferredFindings = %v, want 0 — a rejected call must not touch the state file", findings)
	}
}

// TestShipState_Defer_RejectsWrongTypedDetails covers the silent-coercion
// path: detailStr reads any non-string as "", which for these optional
// fields is indistinguishable from "omitted" — so a wrong-typed reason used
// to be recorded as below-threshold and a wrong-typed description as the
// title, both with a success narration.
func TestShipState_Defer_RejectsWrongTypedDetails(t *testing.T) {
	cases := map[string]map[string]any{
		"reason":      {"reason": float64(5)},
		"description": {"description": []any{"a", "b"}},
		"line":        {"line": "42"},
		"source":      {"source": float64(5)},
	}
	for key, extra := range cases {
		t.Run(key, func(t *testing.T) {
			dir, path := deferFixture(t, "feat/defer-bad-"+key)
			mem := useMemHistory(t)

			detail := map[string]any{
				"branch": "feat/defer-bad-" + key, "severity": "low",
				"file": "a.go", "title": "nit",
			}
			for k, v := range extra {
				detail[k] = v
			}
			_, err := shipState(dir, dir, ShipStateIn{Action: "defer", Detail: detail}, fixedNow(time.Now()))
			if err == nil {
				t.Fatalf("defer with wrong-typed detail.%s: want error, got nil", key)
			}
			if !isDomainError(err) {
				t.Errorf("error = %v (%T), want DomainError", err, err)
			}
			if !strings.Contains(err.Error(), "detail."+key) {
				t.Errorf("error message does not name the offending key: %s", err.Error())
			}
			if len(mem.Deferred) != 0 {
				t.Errorf("deferred entries = %d, want 0", len(mem.Deferred))
			}
			if findings, _ := readStateData(t, path)["deferredFindings"].([]any); len(findings) != 0 {
				t.Errorf("deferredFindings = %v, want 0", findings)
			}
		})
	}
}

// TestShipState_Defer_NullDetailsReadAsOmitted pins the other half of the
// type check: a JSON null is how a caller spells "no value", so it must
// take the omitted defaults rather than being rejected as a wrong type.
func TestShipState_Defer_NullDetailsReadAsOmitted(t *testing.T) {
	dir, path := deferFixture(t, "feat/defer-null-details")
	mem := useMemHistory(t)

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{
			"branch": "feat/defer-null-details", "severity": "low", "file": "a.go",
			"title": "nit", "reason": nil, "description": nil, "line": nil,
		},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("defer with null optional details: %v", err)
	}
	if len(mem.Deferred) != 1 {
		t.Fatalf("deferred.json entries = %d, want 1", len(mem.Deferred))
	}
	got := mem.Deferred[0]
	if got.Reason != history.ReasonBelowThreshold || got.Description != "nit" || got.Line != 0 {
		t.Errorf("entry = %+v, want the omitted defaults (reason=%s, description=title, line=0)",
			got, history.ReasonBelowThreshold)
	}
	if f := deferredStateEntry(t, path, 0); f["line"] != nil {
		t.Errorf("state finding line = %#v, want null", f["line"])
	}
}

func TestShipState_Defer_StoresValidReason(t *testing.T) {
	dir, _ := deferFixture(t, "feat/defer-reason")
	mem := useMemHistory(t)

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{
			"branch": "feat/defer-reason", "severity": "low", "file": "a.go",
			"title": "nit", "reason": history.ReasonBelowThreshold,
		},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("defer: %v", err)
	}
	if len(mem.Deferred) != 1 || mem.Deferred[0].Reason != history.ReasonBelowThreshold {
		t.Errorf("reason = %+v, want %q", mem.Deferred, history.ReasonBelowThreshold)
	}
}

func TestShipState_Defer_RejectsUnknownReason(t *testing.T) {
	dir, _ := deferFixture(t, "feat/defer-bad-reason")
	mem := useMemHistory(t)

	_, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{
			"branch": "feat/defer-bad-reason", "severity": "low", "file": "a.go",
			"title": "nit", "reason": "because-i-said-so",
		},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("defer with unknown reason: want error, got nil")
	}
	if !isDomainError(err) {
		t.Errorf("error = %v (%T), want DomainError", err, err)
	}
	if len(mem.Deferred) != 0 {
		t.Errorf("deferred entries = %d, want 0 — a rejected call must not persist", len(mem.Deferred))
	}
}

// TestShipState_Defer_UnknownReasonErrorNamesEveryAcceptedValue pins the
// message itself, not just the rejection: a caller that sees only the error
// text must learn the full accepted set from it (mcp-error-actionable).
func TestShipState_Defer_UnknownReasonErrorNamesEveryAcceptedValue(t *testing.T) {
	dir, _ := deferFixture(t, "feat/defer-invented-reason")
	useMemHistory(t)

	_, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{
			"branch": "feat/defer-invented-reason", "severity": "low", "file": "a.go",
			"title": "nit", "reason": "invented",
		},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal(`defer with reason "invented": want error, got nil`)
	}
	msg := err.Error()
	for _, want := range history.DeferredReasons() {
		if !strings.Contains(msg, want) {
			t.Errorf("error message does not name accepted value %q: %s", want, msg)
		}
	}
}

// TestShipState_Defer_OmittedReasonRecordsBelowThreshold covers the default:
// a caller that passes no reason is the below-threshold case, and the entry
// says so in both stores rather than carrying an empty field.
func TestShipState_Defer_OmittedReasonRecordsBelowThreshold(t *testing.T) {
	dir, path := deferFixture(t, "feat/defer-no-reason")
	mem := useMemHistory(t)

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{
			"branch": "feat/defer-no-reason", "severity": "low", "file": "a.go",
			"title": "nit",
		},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("defer without reason: %v", err)
	}
	if len(mem.Deferred) != 1 {
		t.Fatalf("deferred.json entries = %d, want 1", len(mem.Deferred))
	}
	if got := mem.Deferred[0].Reason; got != history.ReasonBelowThreshold {
		t.Errorf("deferred.json reason = %q, want %q", got, history.ReasonBelowThreshold)
	}

	findings, _ := readStateData(t, path)["deferredFindings"].([]any)
	if len(findings) != 1 {
		t.Fatalf("deferredFindings = %v, want 1 entry", findings)
	}
	f, _ := findings[0].(map[string]any)
	if f["reason"] != history.ReasonBelowThreshold {
		t.Errorf("state finding reason = %v, want %q", f["reason"], history.ReasonBelowThreshold)
	}
}

// TestShipState_Defer_DescriptionCarriesReasoning covers the needs-direction
// record: the deferring agent's own reasoning (the candidate approaches and
// the trade-off) is what a human reads later, so it must survive the write
// instead of being replaced by the finding title.
func TestShipState_Defer_DescriptionCarriesReasoning(t *testing.T) {
	dir, path := deferFixture(t, "feat/defer-description")
	mem := useMemHistory(t)
	const reasoning = "either widen the interface or add an adapter; trade-off: churn vs one more layer"

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{
			"branch": "feat/defer-description", "severity": "high", "file": "a.go",
			"title": "leaky abstraction", "reason": history.ReasonNeedsDirection,
			"description": reasoning,
		},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("defer with description: %v", err)
	}
	if len(mem.Deferred) != 1 {
		t.Fatalf("deferred.json entries = %d, want 1", len(mem.Deferred))
	}
	if got := mem.Deferred[0].Description; got != reasoning {
		t.Errorf("description = %q, want the caller's reasoning %q", got, reasoning)
	}
	if got := mem.Deferred[0].Reason; got != history.ReasonNeedsDirection {
		t.Errorf("reason = %q, want %q", got, history.ReasonNeedsDirection)
	}
	// The run-scoped entry carries it too: ship's harden step reads each
	// deferred finding's body from there, not from deferred.json.
	findings, _ := readStateData(t, path)["deferredFindings"].([]any)
	if len(findings) != 1 {
		t.Fatalf("deferredFindings = %v, want 1 entry", findings)
	}
	if entry, _ := findings[0].(map[string]any); entry["description"] != reasoning {
		t.Errorf("run-scoped description = %v, want %q", entry["description"], reasoning)
	}
}

// TestShipState_Defer_PersistFailureNamedInNarration also pins
// deferredPersistWarning's recovery instruction (not just the loss): a
// warning that only names what was lost, with no path back to a durable
// record, leaves the caller stuck. now is fixed so the generated id is
// predictable and can be asserted verbatim, the same way
// TestShipState_Defer_PersistsToDeferredHistory does for the success path.
func TestShipState_Defer_PersistFailureNamedInNarration(t *testing.T) {
	dir, path := deferFixture(t, "feat/defer-persist-fail")
	useFailingHistory(t)
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	const wantID = "review-deferred-2026-03-04T05:06:07Z-1"

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{
			"branch": "feat/defer-persist-fail", "severity": "medium",
			"file": "internal/foo.go", "title": "unchecked error",
		},
	}, fixedNow(now))
	if err != nil {
		t.Fatalf("defer must not fail when deferred.json is unwritable: %v", err)
	}

	n, ok := out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipStepNarrationOut", out)
	}
	if !strings.Contains(n.Summary, "deferred.json") || !strings.Contains(n.Summary, "no space left on device") {
		t.Errorf("summary = %q, want it to name the deferred.json write failure", n.Summary)
	}
	if !strings.Contains(n.Summary, "deferred_add") {
		t.Errorf("summary = %q, want it to name the ship_state deferred_add recovery action", n.Summary)
	}
	if !strings.Contains(n.Summary, wantID) {
		t.Errorf("summary = %q, want it to name the lost item's id %q so deferred_add can recreate it", n.Summary, wantID)
	}
	if !strings.Contains(n.Summary, "unchecked error") {
		t.Errorf("summary = %q, want it to name the lost item's description", n.Summary)
	}
	if !strings.Contains(n.Summary, "second entry") {
		t.Errorf("summary = %q, want it to warn that repeating the original call is not a recovery", n.Summary)
	}

	// The run-scoped write still happened — only the durable one was lost.
	data := readStateData(t, path)
	if findings, _ := data["deferredFindings"].([]any); len(findings) != 1 {
		t.Errorf("deferredFindings = %v, want 1 entry", findings)
	}
}

// TestShipState_Defer_ReadFailureNamedInNarration covers persistDeferred's
// earlier exit: the dedupe read of deferred.json fails, so AddDeferred is
// never reached. The call must still succeed and still surface the loss —
// the same contract as the write-failure case above.
func TestShipState_Defer_ReadFailureNamedInNarration(t *testing.T) {
	dir, path := deferFixture(t, "feat/defer-read-fail")
	useListFailingHistory(t)

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{
			"branch": "feat/defer-read-fail", "severity": "medium",
			"file": "internal/foo.go", "title": "unchecked error",
		},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("defer must not fail when deferred.json is unreadable: %v", err)
	}

	n, ok := out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipStepNarrationOut", out)
	}
	if !strings.Contains(n.Summary, "deferred.json") || !strings.Contains(n.Summary, "not readable") {
		t.Errorf("summary = %q, want it to name the deferred.json read failure", n.Summary)
	}

	// The run-scoped write still happened — only the durable one was lost.
	if findings, _ := readStateData(t, path)["deferredFindings"].([]any); len(findings) != 1 {
		t.Errorf("deferredFindings = %v, want 1 entry", findings)
	}
}

func TestPersistDeferred_SkipsDuplicateID(t *testing.T) {
	mem := useMemHistory(t)
	issue := history.DeferredIssue{ID: "review-deferred-x-1", Description: "same", Status: "open"}

	for i := 0; i < 3; i++ {
		if err := persistDeferred(t.TempDir(), issue); err != nil {
			t.Fatalf("persistDeferred call %d: %v", i, err)
		}
	}
	if len(mem.Deferred) != 1 {
		t.Errorf("entries for a re-entered write = %d, want 1", len(mem.Deferred))
	}
}

// TestShipStateSchema_DeferredFindingsEntry proves the published schema
// accepts exactly what the defer action writes. The handler and
// ship-state.schema.json carry the severity and reason vocabularies
// separately, so this is the test that keeps them in sync: every value
// dimensions.ValidSeverities and history.DeferredReasons() allow must
// validate, and an invalid one must be rejected by the enum — not merely
// filtered out by application code.
func TestShipStateSchema_DeferredFindingsEntry(t *testing.T) {
	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc", "schemas", "ship-state.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	sch, err := jsonschema.NewCompiler().Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	validate := func(t *testing.T, finding map[string]any) error {
		t.Helper()
		raw, err := json.Marshal(map[string]any{
			"version":   float64(1),
			"startedAt": "2026-03-01T12:00:00Z",
			"branch":    "feat/schema-test",
			"flags":     map[string]any{},
			"steps": []any{
				map[string]any{"name": "review", "status": "completed"},
			},
			"deferredFindings": []any{finding},
		})
		if err != nil {
			t.Fatalf("marshal doc: %v", err)
		}
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
		if err != nil {
			t.Fatalf("unmarshal doc for schema validation: %v", err)
		}
		return sch.Validate(inst)
	}

	entry := func(overrides map[string]any) map[string]any {
		f := map[string]any{
			"severity": "low", "file": "a.go", "line": float64(7),
			"title": "nit", "reason": history.ReasonBelowThreshold,
		}
		for k, v := range overrides {
			f[k] = v
		}
		return f
	}

	for _, sev := range dimensions.ValidSeverities {
		if err := validate(t, entry(map[string]any{"severity": sev})); err != nil {
			t.Errorf("severity %q: schema rejected a value the defer action accepts: %v", sev, err)
		}
	}
	for _, reason := range history.DeferredReasons() {
		if err := validate(t, entry(map[string]any{"reason": reason})); err != nil {
			t.Errorf("reason %q: schema rejected a value the defer action accepts: %v", reason, err)
		}
	}
	// reason is absent on entries written before the field existed.
	noReason := entry(nil)
	delete(noReason, "reason")
	if err := validate(t, noReason); err != nil {
		t.Errorf("entry without reason: want accepted (pre-existing state files have none), got %v", err)
	}
	if err := validate(t, entry(map[string]any{"line": nil})); err != nil {
		t.Errorf("entry with null line: want accepted, got %v", err)
	}
	// The enums must do the rejecting. "High" is the raw-case value the
	// handler now normalizes before writing.
	if err := validate(t, entry(map[string]any{"severity": "High"})); err == nil {
		t.Error(`severity "High": want schema rejection, got nil`)
	}
	if err := validate(t, entry(map[string]any{"reason": "because-i-said-so"})); err == nil {
		t.Error(`reason "because-i-said-so": want schema rejection, got nil`)
	}
}

func TestPriorityFromSeverity(t *testing.T) {
	// "trivial" and "nit" are NOT review severities (dimensions.ValidSeverities
	// is critical|high|medium|low|info) and no producer emits them, so they
	// take the unknown-value default like any other unrecognised string.
	cases := map[string]string{
		"critical": "high", "Critical": "high", "HIGH": "high", " high ": "high",
		"medium": "medium", "low": "low", "info": "low",
		"trivial": "medium", "nit": "medium",
		"": "medium", "banana": "medium",
	}
	for in, want := range cases {
		if got := priorityFromSeverity(in); got != want {
			t.Errorf("priorityFromSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShipState_Read(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/read")
	shipStateInitFixture(t, dir, "feat/read")

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "read",
		Detail: map[string]any{"branch": "feat/read"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	data, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want map[string]any", out)
	}
	if data["branch"] != "feat/read" {
		t.Errorf("branch = %v, want feat/read", data["branch"])
	}
	if _, ok := data["resumeBriefing"]; ok {
		t.Error("resumeBriefing should be absent on a freshly init'd pipeline — nothing has run yet")
	}
	style, ok := data["style"].(ChatStyle)
	if !ok {
		t.Fatalf("style = %#v, want ChatStyle", data["style"])
	}
	if style.Audience != "functional" {
		t.Errorf("style.audience = %q, want functional", style.Audience)
	}

	// read is a shallow copy: the state file on disk must not gain a
	// "style" key.
	st, findErr := state.Find(dir, "ship", "feat/read")
	if findErr != nil || st == nil {
		t.Fatalf("state.Find: %v", findErr)
	}
	if _, ok := st.Data["style"]; ok {
		t.Error(`state file on disk has a "style" key, want it absent (read must not mutate state)`)
	}
}

func TestShipState_Read_InFlight_FailedStepNeverReportsFailure(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/resume-failed")
	path := shipStateInitFixture(t, dir, "feat/resume-failed")

	// execute crashed/failed mid-step: status "failed", no completedAt.
	setStepStatus(t, path, "execute", "failed", map[string]any{
		"startedAt": "2026-01-01T00:05:00Z",
	})
	setSideEffectEntry(t, path, "execute", "sha", "abcdef1234567890abcdef1234567890abcdef12")

	readNow := time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC)
	result, err := shipState(dir, dir, ShipStateIn{
		Action: "read",
		Detail: map[string]any{"branch": "feat/resume-failed"},
	}, fixedNow(readNow))
	if err != nil {
		t.Fatalf("read on a failed step must not error, got: %v", err)
	}

	data, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want map[string]any", result)
	}
	briefing, ok := data["resumeBriefing"].(*ShipResumeBriefing)
	if !ok {
		t.Fatalf("resumeBriefing type = %T, want *ShipResumeBriefing", data["resumeBriefing"])
	}
	if !briefing.Resumable {
		t.Error("Resumable = false, want true — a failed step is resumable, not a dead end")
	}
	if briefing.LastStep != "execute" {
		t.Errorf("LastStep = %q, want execute", briefing.LastStep)
	}
	if briefing.LastStepStatus != "failed" {
		t.Errorf("LastStepStatus = %q, want failed", briefing.LastStepStatus)
	}
	if briefing.Timing == nil {
		t.Fatal("Timing should not be nil")
	}
	if briefing.Timing.StepSeconds != 300 {
		t.Errorf("Timing.StepSeconds = %d, want 300 (now - startedAt, no completedAt fallback)", briefing.Timing.StepSeconds)
	}
	if briefing.Timing.IdleSeconds != 300 {
		t.Errorf("Timing.IdleSeconds = %d, want 300 (idle since startedAt, completedAt unset on a failed step)", briefing.Timing.IdleSeconds)
	}
	if briefing.Timing.PipelineSeconds != 600 {
		t.Errorf("Timing.PipelineSeconds = %d, want 600", briefing.Timing.PipelineSeconds)
	}
	if len(briefing.SideEffects) != 1 || !strings.Contains(briefing.SideEffects[0], "abcdef1") || strings.Contains(briefing.SideEffects[0], "abcdef1234567890") {
		t.Errorf("SideEffects = %v, want one entry with the sha shortened to 7 chars", briefing.SideEffects)
	}
	if briefing.Next == nil {
		t.Error("Next should not be nil for an in-flight pipeline")
	}
	if briefing.Summary == "" || briefing.Display == "" {
		t.Error("Summary/Display should be populated")
	}

	// read is a pure read: the step must remain untouched on disk.
	onDisk := readStateData(t, path)
	steps, _ := onDisk["steps"].([]any)
	for _, s := range steps {
		sm, _ := s.(map[string]any)
		if sm["name"] == "execute" && sm["status"] != "failed" {
			t.Error("read must not mutate state — execute step status changed")
		}
	}
}

// A run that cleanup stamped completed is finished, even when one of its
// steps ended "failed" (failed is terminal for the cleanup contract). read
// must not offer to resume it.
func TestShipState_Read_CompletedRunWithFailedStepHasNoBriefing(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/done-failed")
	path := shipStateInitFixture(t, dir, "feat/done-failed")

	for _, s := range readStateData(t, path)["steps"].([]any) {
		name, _ := s.(map[string]any)["name"].(string)
		if name == "execute" {
			setStepStatus(t, path, name, "failed", map[string]any{"startedAt": "2026-01-01T00:05:00Z"})
			continue
		}
		setStepStatus(t, path, name, "skipped", nil)
	}

	now := fixedNow(time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC))
	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "cleanup",
		Detail: map[string]any{"branch": "feat/done-failed"},
	}, now); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if got := readStateData(t, path)["pipelineStatus"]; got != "completed" {
		t.Fatalf("pipelineStatus = %v, want completed (cleanup should have stamped the run)", got)
	}

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "read",
		Detail: map[string]any{"branch": "feat/done-failed"},
	}, now)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	data, _ := out.(map[string]any)
	if b, ok := data["resumeBriefing"]; ok {
		t.Errorf("resumeBriefing = %#v, want absent on a completed run", b)
	}
}

func TestShipState_Read_InFlight_InProgressStep(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/resume-inprogress")
	path := shipStateInitFixture(t, dir, "feat/resume-inprogress")

	setStepStatus(t, path, "execute", "in_progress", map[string]any{
		"startedAt": "2026-01-01T00:00:30Z",
	})

	readNow := time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)
	result, err := shipState(dir, dir, ShipStateIn{
		Action: "read",
		Detail: map[string]any{"branch": "feat/resume-inprogress"},
	}, fixedNow(readNow))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	data := result.(map[string]any)
	briefing, ok := data["resumeBriefing"].(*ShipResumeBriefing)
	if !ok {
		t.Fatalf("resumeBriefing type = %T, want *ShipResumeBriefing", data["resumeBriefing"])
	}
	if !briefing.Resumable {
		t.Error("Resumable = false, want true")
	}
	if briefing.LastStep != "execute" || briefing.LastStepStatus != "in_progress" {
		t.Errorf("LastStep/LastStepStatus = %q/%q, want execute/in_progress", briefing.LastStep, briefing.LastStepStatus)
	}
	if len(briefing.SideEffects) != 0 {
		t.Errorf("SideEffects = %v, want none recorded", briefing.SideEffects)
	}
}

// TestShipState_Read_ReportData exercises R9: the read action must attach
// report-ready aggregates (ShipReportData) computed server-side, so the
// calling LLM never re-derives step counts/duration/bump provenance from
// raw state. The fixture is seeded directly on disk (rather than through
// ship_prepare) because sources/versionCfg/binaryVersion are only ever
// written by shipPrepare, never by the "init" action shipStateInitFixture
// uses — reportData must work off whatever the state file actually holds.
func TestShipState_Read_ReportData(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/report-data")
	path := shipStateInitFixture(t, dir, "feat/report-data")

	data := readStateData(t, path)
	data["startedAt"] = "2026-01-01T00:00:00Z"
	data["flags"] = map[string]any{"bump": "minor"}
	data["sources"] = map[string]any{"bump": "config (version.preRelease)"}
	data["versionCfg"] = map[string]any{"preRelease": "beta", "preReleasePolicy": "default-rc"}
	data["binaryVersion"] = map[string]any{"pluginVersion": "0.1.1", "commit": "abcdef1", "buildTime": "2026-01-01T00:00:00Z"}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	setStepStatus(t, path, "execute", "completed", map[string]any{
		"startedAt":   "2026-01-01T00:00:00Z",
		"completedAt": "2026-01-01T00:05:00Z",
	})
	setStepStatus(t, path, "commit", "skipped", nil)
	setStepStatus(t, path, "review", "failed", map[string]any{
		"startedAt": "2026-01-01T00:06:00Z",
	})
	// received-review, commit-fixes, pr stay at their scaffolded "pending".

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "decide",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/report-data", "text": "used minor bump"},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "defer",
		Detail: map[string]any{
			"branch":   "feat/report-data",
			"severity": "low",
			"file":     "foo.go",
			"title":    "cleanup later",
		},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("defer: %v", err)
	}

	readNow := time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC)
	result, err := shipState(dir, dir, ShipStateIn{
		Action: "read",
		Detail: map[string]any{"branch": "feat/report-data"},
	}, fixedNow(readNow))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	out, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want map[string]any", result)
	}
	rd, ok := out["reportData"].(ShipReportData)
	if !ok {
		t.Fatalf("reportData type = %T, want ShipReportData", out["reportData"])
	}

	if rd.Version != "" {
		t.Errorf("Version = %q, want empty (no resolved release version tracked in ship state)", rd.Version)
	}
	if rd.Bump != "minor" {
		t.Errorf("Bump = %q, want minor", rd.Bump)
	}
	if rd.BumpSource != "config (version.preRelease)" {
		t.Errorf("BumpSource = %q, want %q", rd.BumpSource, "config (version.preRelease)")
	}
	if rd.PreRelease != "beta" {
		t.Errorf("PreRelease = %q, want beta", rd.PreRelease)
	}
	if rd.PreReleasePolicy != "default-rc" {
		t.Errorf("PreReleasePolicy = %q, want default-rc", rd.PreReleasePolicy)
	}
	if rd.StepsTotal != 6 {
		t.Errorf("StepsTotal = %d, want 6", rd.StepsTotal)
	}
	if rd.StepsCompleted != 1 {
		t.Errorf("StepsCompleted = %d, want 1", rd.StepsCompleted)
	}
	if rd.StepsSkipped != 1 {
		t.Errorf("StepsSkipped = %d, want 1", rd.StepsSkipped)
	}
	if rd.StepsFailed != 1 {
		t.Errorf("StepsFailed = %d, want 1", rd.StepsFailed)
	}
	if rd.StepsPending != 3 {
		t.Errorf("StepsPending = %d, want 3 (received-review, commit-fixes, pr)", rd.StepsPending)
	}
	if rd.Duration != "10m 00s" {
		t.Errorf("Duration = %q, want %q (startedAt to read's now, no pipelineCompletedAt)", rd.Duration, "10m 00s")
	}
	if len(rd.Decisions) != 1 || rd.Decisions[0] != "execute: used minor bump" {
		t.Errorf("Decisions = %v, want [\"execute: used minor bump\"]", rd.Decisions)
	}
	if rd.DeferredFindings != 1 {
		t.Errorf("DeferredFindings = %d, want 1", rd.DeferredFindings)
	}
	wantBinary := map[string]string{"pluginVersion": "0.1.1", "commit": "abcdef1", "buildTime": "2026-01-01T00:00:00Z"}
	if !reflect.DeepEqual(rd.BinaryVersion, wantBinary) {
		t.Errorf("BinaryVersion = %v, want %v", rd.BinaryVersion, wantBinary)
	}

	// read must remain a pure read: reportData is derived, never persisted.
	onDisk := readStateData(t, path)
	if _, ok := onDisk["reportData"]; ok {
		t.Error("reportData must not be persisted to the state file")
	}
}

// ---------------------------------------------------------------------------
// cleanup
// ---------------------------------------------------------------------------

func TestShipState_Cleanup_NoStateFile(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/cleanup-none")

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "cleanup",
		Detail: map[string]any{"branch": "feat/cleanup-none"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if m, ok := out.(map[string]any); !ok || len(m) != 0 {
		t.Errorf("output = %#v, want an empty map", out)
	}
}

func TestShipState_Cleanup_ValidTerminalStampsCompleted(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/cleanup-ok")
	path := shipStateInitFixture(t, dir, "feat/cleanup-ok")

	terminalStatus := map[string]string{
		"execute": "completed", "commit": "completed", "review": "completed",
		"received-review": "skipped", "commit-fixes": "skipped",
		"version": "completed", "pr": "completed",
	}
	for name, status := range terminalStatus {
		setStepStatus(t, path, name, status, map[string]any{"completedAt": "2026-01-01T00:00:00Z"})
	}

	fixedTime := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	out, err := shipState(dir, dir, ShipStateIn{
		Action: "cleanup",
		Detail: map[string]any{"branch": "feat/cleanup-ok"},
	}, fixedNow(fixedTime))
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["valid"] != true || m["cleaned"] != true {
		t.Errorf("output = %#v, want {valid:true cleaned:true, ...}", out)
	}
	wantCompletedAt := fixedTime.UTC().Format(time.RFC3339)
	if m["pipelineStatus"] != "completed" || m["pipelineCompletedAt"] != wantCompletedAt {
		t.Errorf("output pipelineStatus/pipelineCompletedAt = %v/%v, want completed/%s", m["pipelineStatus"], m["pipelineCompletedAt"], wantCompletedAt)
	}

	// The state file must be preserved (stamped terminal), never deleted —
	// so /harden and other later readers can still find it.
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("state file %s should be preserved (stamped, not deleted): %v", path, statErr)
	}
	st, findErr := state.Find(dir, "ship", "feat/cleanup-ok")
	if findErr != nil || st == nil {
		t.Fatalf("state should still be findable after cleanup, findErr=%v st=%v", findErr, st)
	}
	if st.Data["pipelineStatus"] != "completed" {
		t.Errorf("persisted pipelineStatus = %v, want completed", st.Data["pipelineStatus"])
	}
	if st.Data["pipelineCompletedAt"] != wantCompletedAt {
		t.Errorf("persisted pipelineCompletedAt = %v, want %s", st.Data["pipelineCompletedAt"], wantCompletedAt)
	}
}

func TestShipState_Cleanup_ViolationPreservesFile(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/cleanup-violation")
	path := shipStateInitFixture(t, dir, "feat/cleanup-violation")
	// Fresh scaffold: every step is a non-conditional "pending" — a contract
	// violation by construction (received-review/commit-fixes are the only
	// steps with a condition key, and those are still pending too, but
	// execute/commit/review/version/pr are bare pending and must violate).

	_, err := shipState(dir, dir, ShipStateIn{
		Action: "cleanup",
		Detail: map[string]any{"branch": "feat/cleanup-violation"},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("cleanup on a fresh (all-pending) pipeline: want a contract-violation error, got nil")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("state file %s should have been preserved on violation: %v", path, statErr)
	}
}

// ---------------------------------------------------------------------------
// cleanup-pipeline
// ---------------------------------------------------------------------------

func TestShipState_CleanupPipeline_Force(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/cleanup-pipeline-force")
	path := shipStateInitFixture(t, dir, "feat/cleanup-pipeline-force")
	// Leave the pipeline mid-flight (a violation state) — force must bypass
	// the contract check entirely and still reach the GC sweep.

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "cleanup-pipeline",
		Detail: map[string]any{"branch": "feat/cleanup-pipeline-force", "force": true},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("cleanup-pipeline force: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want map[string]any", out)
	}
	gc, ok := m["gc"].(map[string]any)
	if !ok {
		t.Fatalf("gc = %#v, want map[string]any", m["gc"])
	}
	if _, hasCommit := gc["commit"]; !hasCommit {
		t.Error("gc report missing 'commit' bucket on the force path")
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("force must not delete the in-flight state file: %v", statErr)
	}
}

// TestShipState_CleanupPipeline_ValidTerminalStampsThenRunsGC proves the
// non-force success path stamps pipelineStatus/pipelineCompletedAt via
// state.Write (never deletes the file) and still reaches the GC sweep
// afterward, same as the force and no-state-file paths.
func TestShipState_CleanupPipeline_ValidTerminalStampsThenRunsGC(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/cleanup-pipeline-ok")
	path := shipStateInitFixture(t, dir, "feat/cleanup-pipeline-ok")

	terminalStatus := map[string]string{
		"execute": "completed", "commit": "completed", "review": "completed",
		"received-review": "skipped", "commit-fixes": "skipped",
		"version": "completed", "pr": "completed",
	}
	for name, status := range terminalStatus {
		setStepStatus(t, path, name, status, map[string]any{"completedAt": "2026-01-01T00:00:00Z"})
	}

	fixedTime := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	out, err := shipState(dir, dir, ShipStateIn{
		Action: "cleanup-pipeline",
		Detail: map[string]any{"branch": "feat/cleanup-pipeline-ok"},
	}, fixedNow(fixedTime))
	if err != nil {
		t.Fatalf("cleanup-pipeline: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want map[string]any", out)
	}
	currentRun, ok := m["currentRun"].(map[string]any)
	if !ok {
		t.Fatalf("currentRun = %#v, want map[string]any", m["currentRun"])
	}
	wantCompletedAt := fixedTime.UTC().Format(time.RFC3339)
	if currentRun["cleaned"] != true || currentRun["pipelineStatus"] != "completed" || currentRun["pipelineCompletedAt"] != wantCompletedAt {
		t.Errorf("currentRun = %#v, want cleaned:true pipelineStatus:completed pipelineCompletedAt:%s", currentRun, wantCompletedAt)
	}
	gc, ok := m["gc"].(map[string]any)
	if !ok {
		t.Fatalf("gc = %#v, want map[string]any", m["gc"])
	}
	if _, hasCommit := gc["commit"]; !hasCommit {
		t.Error("gc report missing 'commit' bucket on the valid-terminal path")
	}

	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("state file %s should be preserved (stamped, not deleted): %v", path, statErr)
	}
	st, findErr := state.Find(dir, "ship", "feat/cleanup-pipeline-ok")
	if findErr != nil || st == nil {
		t.Fatalf("state should still be findable after cleanup-pipeline, findErr=%v st=%v", findErr, st)
	}
	if st.Data["pipelineStatus"] != "completed" {
		t.Errorf("persisted pipelineStatus = %v, want completed", st.Data["pipelineStatus"])
	}

	// No issues[] on this fixture — issueSummary must be entirely absent.
	if _, present := m["issueSummary"]; present {
		t.Errorf("issueSummary = %#v, want key absent when issues[] is empty", m["issueSummary"])
	}
}

// TestShipState_CleanupPipeline_IssueSummary proves the pipeline-completion
// action attaches a grouped issueSummary (with hardenSuggestion, since "fail"
// records an error-severity issue) when issues[] is non-empty on the stamped
// success path. "failed" is itself a terminal status, so failing one step
// satisfies shipValidatePipelineContract without any extra fixture seeding.
func TestShipState_CleanupPipeline_IssueSummary(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/cleanup-pipeline-issues")
	path := shipStateInitFixture(t, dir, "feat/cleanup-pipeline-issues")

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "fail",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/cleanup-pipeline-issues", "error": "wave 2 crashed"},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("fail: %v", err)
	}

	terminalStatus := map[string]string{
		"commit": "completed", "review": "completed",
		"received-review": "skipped", "commit-fixes": "skipped",
		"version": "completed", "pr": "completed",
	}
	for name, status := range terminalStatus {
		setStepStatus(t, path, name, status, map[string]any{"completedAt": "2026-01-01T00:00:00Z"})
	}

	fixedTime := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	out, err := shipState(dir, dir, ShipStateIn{
		Action: "cleanup-pipeline",
		Detail: map[string]any{"branch": "feat/cleanup-pipeline-issues"},
	}, fixedNow(fixedTime))
	if err != nil {
		t.Fatalf("cleanup-pipeline: %v", err)
	}
	m := out.(map[string]any)

	is, ok := m["issueSummary"].(*IssueSummary)
	if !ok {
		t.Fatalf("issueSummary = %#v (%T), want *IssueSummary", m["issueSummary"], m["issueSummary"])
	}
	if is.Total != 1 {
		t.Errorf("issueSummary.Total = %d, want 1", is.Total)
	}
	if is.ByCategory["ship-fail"] != 1 {
		t.Errorf("issueSummary.ByCategory = %#v, want {ship-fail:1}", is.ByCategory)
	}
	wantSuggestion := "Run /harden --failure-text 'Step execute failed' to strengthen guardrails."
	if is.HardenSuggestion != wantSuggestion {
		t.Errorf("issueSummary.HardenSuggestion = %q, want %q", is.HardenSuggestion, wantSuggestion)
	}
}

func TestShipState_CleanupPipeline_NoStateFileStillRunsGC(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/cleanup-pipeline-nostate")

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "cleanup-pipeline",
		Detail: map[string]any{"branch": "feat/cleanup-pipeline-nostate"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("cleanup-pipeline: %v", err)
	}
	m := out.(map[string]any)
	gc, ok := m["gc"].(map[string]any)
	if !ok {
		t.Fatalf("gc = %#v, want map[string]any", m["gc"])
	}
	if _, hasCommit := gc["commit"]; !hasCommit {
		t.Error("gc report missing 'commit' bucket on the no-state-file path")
	}
}

func TestShipState_CleanupPipeline_ViolationBlocksBeforeGC(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/cleanup-pipeline-violation")
	shipStateInitFixture(t, dir, "feat/cleanup-pipeline-violation")

	_, err := shipState(dir, dir, ShipStateIn{
		Action: "cleanup-pipeline",
		Detail: map[string]any{"branch": "feat/cleanup-pipeline-violation"},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("cleanup-pipeline on a fresh (all-pending) pipeline: want a contract-violation error, got nil")
	}
}

// planRunCleanupFixture is one cleanup-pipeline plan-run deletion fixture on
// disk: a ship state, a plan run with a populated .evidence directory, and
// (optionally) an execute state whose planPath links the plan run.
type planRunCleanupFixture struct {
	dir, branch, planRunPath, evidenceDir, runID string
}

// newPlanRunCleanupFixture builds the fixture. terminal stamps every ship
// step terminal (so cleanup-pipeline stamps instead of failing the
// contract); execPlanPath is the execute state's planPath ("" = no execute
// state at all; the linked plan file is <dir>/plans/feature.md).
func newPlanRunCleanupFixture(t *testing.T, branch string, terminal bool, execPlanPath func(dir string) string) planRunCleanupFixture {
	t.Helper()
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, branch)
	shipPath := shipStateInitFixture(t, dir, branch)
	if terminal {
		for name, status := range map[string]string{
			"execute": "completed", "commit": "completed", "review": "completed",
			"received-review": "skipped", "commit-fixes": "skipped",
			"version": "completed", "pr": "completed",
		} {
			setStepStatus(t, shipPath, name, status, map[string]any{"completedAt": "2026-01-01T00:00:00Z"})
		}
	}

	planFile := filepath.Join(dir, "plans", "feature.md")
	planSt, err := state.Init(dir, "plan", branch, "")
	if err != nil {
		t.Fatalf("init plan run: %v", err)
	}
	planSt.Data["planFilePath"] = planFile
	if err := state.Write(planSt); err != nil {
		t.Fatalf("write plan run: %v", err)
	}
	runID := state.RunID(planSt)
	evidenceDir := state.EvidenceDir(dir, runID)
	writeFile(t, filepath.Join(evidenceDir, "critique.md"), "evidence")

	if execPlanPath != nil {
		createExecState(t, dir, branch, map[string]any{"branch": branch, "planPath": execPlanPath(dir)})
	}
	return planRunCleanupFixture{dir: dir, branch: branch, planRunPath: planSt.Path, evidenceDir: evidenceDir, runID: runID}
}

// linkedPlanFile is the execPlanPath that links the fixture's plan run.
func linkedPlanFile(dir string) string { return filepath.Join(dir, "plans", "feature.md") }

// writeShipReport writes this ship run's report through the real report
// action and returns its path.
func (f planRunCleanupFixture) writeShipReport(t *testing.T, format string) string {
	t.Helper()
	out, err := shipState(f.dir, f.dir, ShipStateIn{
		Action: "report",
		Detail: map[string]any{"branch": f.branch, "write": true, "format": format},
	}, fixedNow(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("report write: %v", err)
	}
	rep, ok := out.(ShipRunReportOut)
	if !ok || !rep.Written || rep.Path == "" {
		t.Fatalf("report output = %#v, want a written report with a path", out)
	}
	return rep.Path
}

func (f planRunCleanupFixture) cleanupPipeline(t *testing.T, detail map[string]any) (ShipPlanRunCleanup, error) {
	t.Helper()
	d := map[string]any{"branch": f.branch}
	for k, v := range detail {
		d[k] = v
	}
	out, err := shipState(f.dir, f.dir, ShipStateIn{Action: "cleanup-pipeline", Detail: d},
		fixedNow(time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		return ShipPlanRunCleanup{}, err
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want map[string]any", out)
	}
	pr, ok := m["planRun"].(ShipPlanRunCleanup)
	if !ok {
		t.Fatalf("planRun = %#v (%T), want ShipPlanRunCleanup", m["planRun"], m["planRun"])
	}
	return pr, nil
}

func (f planRunCleanupFixture) assertKept(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(f.planRunPath); err != nil {
		t.Errorf("plan run file %s must be kept: %v", f.planRunPath, err)
	}
	if _, err := os.Stat(filepath.Join(f.evidenceDir, "critique.md")); err != nil {
		t.Errorf("evidence dir %s must be kept: %v", f.evidenceDir, err)
	}
}

func (f planRunCleanupFixture) assertDeleted(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(f.planRunPath); !os.IsNotExist(err) {
		t.Errorf("plan run file %s must be deleted, stat err = %v", f.planRunPath, err)
	}
	if _, err := os.Stat(f.evidenceDir); !os.IsNotExist(err) {
		t.Errorf("evidence dir %s must be deleted, stat err = %v", f.evidenceDir, err)
	}
}

// TestCleanupPipelineDeletesReportedPlanRun covers cleanup-pipeline's
// plan-run deletion step: it runs only after the stamp, only for the plan
// run linked through the execute state, and only once the ship report for
// this ship run is on disk. force and contract violations delete nothing.
func TestCleanupPipelineDeletesReportedPlanRun(t *testing.T) {
	for _, format := range []string{"md", "json"} {
		t.Run("report written ("+format+") deletes plan run and evidence", func(t *testing.T) {
			f := newPlanRunCleanupFixture(t, "feat/planrun-reported-"+format, true, linkedPlanFile)
			reportPath := f.writeShipReport(t, format)
			if want := "ship-20260101T000000-report." + format; filepath.Base(reportPath) != want {
				t.Fatalf("report file = %s, want %s", filepath.Base(reportPath), want)
			}

			pr, err := f.cleanupPipeline(t, nil)
			if err != nil {
				t.Fatalf("cleanup-pipeline: %v", err)
			}
			want := ShipPlanRunCleanup{Deleted: true, RunID: f.runID, ExploreSummaryCount: intPtr(0), ReviewRoundsCount: intPtr(0)}
			if !reflect.DeepEqual(pr, want) {
				t.Errorf("planRun = %#v, want %#v", pr, want)
			}
			f.assertDeleted(t)
			if _, err := os.Stat(reportPath); err != nil {
				t.Errorf("report %s must survive cleanup-pipeline: %v", reportPath, err)
			}
			st, _ := state.Find(f.dir, "ship", f.branch)
			if st == nil || st.Data["pipelineStatus"] != "completed" {
				t.Errorf("ship state must be stamped completed, got %#v", st)
			}
		})
	}

	t.Run("report not written keeps plan run", func(t *testing.T) {
		f := newPlanRunCleanupFixture(t, "feat/planrun-noreport", true, linkedPlanFile)

		pr, err := f.cleanupPipeline(t, nil)
		if err != nil {
			t.Fatalf("cleanup-pipeline: %v", err)
		}
		if want := (ShipPlanRunCleanup{Reason: "report not written"}); !reflect.DeepEqual(pr, want) {
			t.Errorf("planRun = %#v, want %#v", pr, want)
		}
		f.assertKept(t)
	})

	t.Run("no execute state is no linked plan run", func(t *testing.T) {
		f := newPlanRunCleanupFixture(t, "feat/planrun-noexec", true, nil)
		f.writeShipReport(t, "md")

		pr, err := f.cleanupPipeline(t, nil)
		if err != nil {
			t.Fatalf("cleanup-pipeline: %v", err)
		}
		if want := (ShipPlanRunCleanup{Reason: "no linked plan run"}); !reflect.DeepEqual(pr, want) {
			t.Errorf("planRun = %#v, want %#v", pr, want)
		}
		f.assertKept(t)
	})

	t.Run("execute planPath matching no plan run is no linked plan run", func(t *testing.T) {
		f := newPlanRunCleanupFixture(t, "feat/planrun-unlinked", true, func(dir string) string {
			return filepath.Join(dir, "plans", "other.md")
		})
		f.writeShipReport(t, "md")

		pr, err := f.cleanupPipeline(t, nil)
		if err != nil {
			t.Fatalf("cleanup-pipeline: %v", err)
		}
		if want := (ShipPlanRunCleanup{Reason: "no linked plan run"}); !reflect.DeepEqual(pr, want) {
			t.Errorf("planRun = %#v, want %#v", pr, want)
		}
		f.assertKept(t)
	})

	t.Run("force does not bypass the gate and deletes nothing", func(t *testing.T) {
		f := newPlanRunCleanupFixture(t, "feat/planrun-force", true, linkedPlanFile)
		f.writeShipReport(t, "md")

		pr, err := f.cleanupPipeline(t, map[string]any{"force": true})
		if err != nil {
			t.Fatalf("cleanup-pipeline force: %v", err)
		}
		if want := (ShipPlanRunCleanup{Reason: "run not stamped"}); !reflect.DeepEqual(pr, want) {
			t.Errorf("planRun = %#v, want %#v", pr, want)
		}
		f.assertKept(t)
	})

	t.Run("contract violation deletes nothing", func(t *testing.T) {
		f := newPlanRunCleanupFixture(t, "feat/planrun-violation", false, linkedPlanFile)
		f.writeShipReport(t, "md")

		if _, err := f.cleanupPipeline(t, nil); err == nil {
			t.Fatal("cleanup-pipeline on an all-pending pipeline: want a contract-violation error, got nil")
		}
		f.assertKept(t)
	})

	t.Run("ship state without startedAt deletes nothing", func(t *testing.T) {
		f := newPlanRunCleanupFixture(t, "feat/planrun-nostart", true, linkedPlanFile)
		// A report named with the "wave-0" fallback id must not count.
		writeFile(t, filepath.Join(f.dir, paths.DataDir, "reports", "ship-wave-0-report.md"), "report")
		st, err := state.Find(f.dir, "ship", f.branch)
		if err != nil || st == nil {
			t.Fatalf("find ship state: st=%v err=%v", st, err)
		}
		delete(st.Data, "startedAt")
		if err := state.Write(st); err != nil {
			t.Fatalf("write ship state: %v", err)
		}

		pr, err := f.cleanupPipeline(t, nil)
		if err != nil {
			t.Fatalf("cleanup-pipeline: %v", err)
		}
		if want := (ShipPlanRunCleanup{Reason: "report not written"}); !reflect.DeepEqual(pr, want) {
			t.Errorf("planRun = %#v, want %#v", pr, want)
		}
		f.assertKept(t)
	})
}

// ---------------------------------------------------------------------------
// cleanup-pipeline: explorer summary copy before the plan-run delete
// ---------------------------------------------------------------------------

// useShipExploreSummary replaces shipExploreSummaryFunc for one test and
// restores it afterwards.
func useShipExploreSummary(t *testing.T, fn func(root, runID string) ([]ExploreSummaryEntry, error)) {
	t.Helper()
	prev := shipExploreSummaryFunc
	shipExploreSummaryFunc = fn
	t.Cleanup(func() { shipExploreSummaryFunc = prev })
}

// useShipRemoveEvidence replaces shipRemoveEvidenceFunc for one test and
// restores it afterwards.
func useShipRemoveEvidence(t *testing.T, fn func(path string) error) {
	t.Helper()
	prev := shipRemoveEvidenceFunc
	shipRemoveEvidenceFunc = fn
	t.Cleanup(func() { shipRemoveEvidenceFunc = prev })
}

// writeExplorer writes one explorer evidence file with n findings into the
// fixture's evidence folder.
func (f planRunCleanupFixture) writeExplorer(t *testing.T, name, status string, n int) {
	t.Helper()
	id := exploreWriterPrefix + name
	evidenceWriteRaw(t, f.dir, f.runID, id, evidenceWriterFile{
		WriterID: id, Status: status, Items: exploreSummaryItems(n),
	})
}

// shipStateOnDisk reads the ship state file of the fixture's branch.
func (f planRunCleanupFixture) shipStateOnDisk(t *testing.T) map[string]any {
	t.Helper()
	st, err := state.Find(f.dir, "ship", f.branch)
	if err != nil || st == nil {
		t.Fatalf("find ship state: st=%v err=%v", st, err)
	}
	return st.Data
}

// wantExploreEntry is one planExploreSummary entry as the state file stores
// it after a JSON round trip: numbers are float64 and lists are []any. The
// finding texts come from exploreSummaryItems.
func wantExploreEntry(name, status string, total, top int) map[string]any {
	items := make([]any, top)
	for i := range items {
		items[i] = map[string]any{
			"summary": fmt.Sprintf("finding %d", i+1),
			"ref":     fmt.Sprintf("pkg/file.go:%d", i+1),
		}
	}
	return map[string]any{"name": name, "status": status, "total": float64(total), "top": items}
}

// shipStateSchemaValidator compiles ship-state.schema.json and returns a
// function that validates one state document against it.
func shipStateSchemaValidator(t *testing.T) func(doc map[string]any) error {
	t.Helper()
	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc", "schemas", "ship-state.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	sch, err := jsonschema.NewCompiler().Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return func(doc map[string]any) error {
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal doc: %v", err)
		}
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
		if err != nil {
			t.Fatalf("unmarshal doc for schema validation: %v", err)
		}
		return sch.Validate(inst)
	}
}

// TestShipState_CleanupPipeline_ExploreSummary_StoredBeforeDelete covers the
// copy: after cleanup deletes the plan run, the ship state file holds the
// summary in the ExploreSummaryEntry shape, and the file validates against
// the schema.
func TestShipState_CleanupPipeline_ExploreSummary_StoredBeforeDelete(t *testing.T) {
	f := newPlanRunCleanupFixture(t, "feat/planrun-summary-stored", true, linkedPlanFile)
	f.writeExplorer(t, "zeta", "running", 1)
	f.writeExplorer(t, "auth-flow", "done", 7)
	f.writeShipReport(t, "md")

	pr, err := f.cleanupPipeline(t, nil)
	if err != nil {
		t.Fatalf("cleanup-pipeline: %v", err)
	}
	if want := (ShipPlanRunCleanup{Deleted: true, RunID: f.runID, ExploreSummaryCount: intPtr(2), ReviewRoundsCount: intPtr(0)}); !reflect.DeepEqual(pr, want) {
		t.Errorf("planRun = %#v, want %#v", pr, want)
	}
	f.assertDeleted(t)

	data := f.shipStateOnDisk(t)
	want := []any{
		wantExploreEntry("auth-flow", "done", 7, 5),
		wantExploreEntry("zeta", "running", 1, 1),
	}
	if got := data[shipPlanExploreSummaryKey]; !reflect.DeepEqual(got, any(want)) {
		t.Errorf("stored planExploreSummary = %#v, want %#v", got, want)
	}
	if data["pipelineStatus"] != "completed" {
		t.Errorf("pipelineStatus = %v, want completed", data["pipelineStatus"])
	}
	if err := shipStateSchemaValidator(t)(data); err != nil {
		t.Errorf("ship state written by cleanup-pipeline: schema rejected it: %v", err)
	}
}

// TestShipState_CleanupPipeline_ExploreSummary_WriteComesBeforeDelete pins
// the order: when the ship state write runs, the evidence folder and the
// plan state file still exist, and the summary is already in the state.
func TestShipState_CleanupPipeline_ExploreSummary_WriteComesBeforeDelete(t *testing.T) {
	f := newPlanRunCleanupFixture(t, "feat/planrun-summary-order", true, linkedPlanFile)
	f.writeExplorer(t, "auth-flow", "done", 2)
	f.writeShipReport(t, "md")

	var calls int
	var evidenceExisted, planRunExisted, summarySet bool
	useShipStateWrite(t, func(st *state.State) error {
		calls++
		_, evErr := os.Stat(f.evidenceDir)
		_, prErr := os.Stat(f.planRunPath)
		evidenceExisted, planRunExisted = evErr == nil, prErr == nil
		_, summarySet = st.Data[shipPlanExploreSummaryKey]
		return state.Write(st)
	})

	pr, err := f.cleanupPipeline(t, nil)
	if err != nil {
		t.Fatalf("cleanup-pipeline: %v", err)
	}
	if !pr.Deleted {
		t.Fatalf("planRun = %#v, want Deleted", pr)
	}
	if calls != 1 {
		t.Fatalf("shipStateWriteFunc calls = %d, want 1", calls)
	}
	if !evidenceExisted || !planRunExisted {
		t.Errorf("at the ship state write: evidence dir exists = %v, plan run file exists = %v, want both true", evidenceExisted, planRunExisted)
	}
	if !summarySet {
		t.Error("the ship state must hold planExploreSummary when it is written")
	}
}

// TestShipState_CleanupPipeline_ExploreSummary_NoExplorersStoresEmptyList
// covers a plan run with no explorer files: the key is stored as [], never
// null and never absent.
func TestShipState_CleanupPipeline_ExploreSummary_NoExplorersStoresEmptyList(t *testing.T) {
	f := newPlanRunCleanupFixture(t, "feat/planrun-summary-empty", true, linkedPlanFile)
	f.writeShipReport(t, "md")

	pr, err := f.cleanupPipeline(t, nil)
	if err != nil {
		t.Fatalf("cleanup-pipeline: %v", err)
	}
	if !pr.Deleted {
		t.Fatalf("planRun = %#v, want Deleted", pr)
	}
	data := f.shipStateOnDisk(t)
	got, present := data[shipPlanExploreSummaryKey]
	list, isList := got.([]any)
	if !present || !isList || len(list) != 0 {
		t.Errorf("stored planExploreSummary = %#v (present=%v), want an empty []", got, present)
	}
	if err := shipStateSchemaValidator(t)(data); err != nil {
		t.Errorf("ship state with an empty summary: schema rejected it: %v", err)
	}
}

// TestShipState_CleanupPipeline_ExploreSummary_ReadFailsKeepsPlanRun covers
// the first failure point: the summary read fails. Nothing is deleted, the
// ship state holds no summary, and the stamp from before stays.
func TestShipState_CleanupPipeline_ExploreSummary_ReadFailsKeepsPlanRun(t *testing.T) {
	f := newPlanRunCleanupFixture(t, "feat/planrun-summary-readfail", true, linkedPlanFile)
	f.writeShipReport(t, "md")

	useShipExploreSummary(t, func(string, string) ([]ExploreSummaryEntry, error) {
		return nil, errors.New("evidence unreadable")
	})
	writes := 0
	useShipStateWrite(t, func(st *state.State) error {
		writes++
		return state.Write(st)
	})

	pr, err := f.cleanupPipeline(t, nil)
	if err != nil {
		t.Fatalf("cleanup-pipeline: %v", err)
	}
	want := ShipPlanRunCleanup{Reason: "explorer summary and review rounds not saved: evidence unreadable. Fix the cause and call cleanup-pipeline again."}
	if !reflect.DeepEqual(pr, want) {
		t.Errorf("planRun = %#v, want %#v", pr, want)
	}
	f.assertKept(t)
	if writes != 0 {
		t.Errorf("shipStateWriteFunc calls = %d, want 0 after a failed summary read", writes)
	}
	data := f.shipStateOnDisk(t)
	if _, present := data[shipPlanExploreSummaryKey]; present {
		t.Errorf("planExploreSummary must be absent after a failed read, got %#v", data[shipPlanExploreSummaryKey])
	}
	if data["pipelineStatus"] != "completed" {
		t.Errorf("pipelineStatus = %v, want completed (the stamp is written before the copy)", data["pipelineStatus"])
	}
}

// TestShipState_CleanupPipeline_ExploreSummary_WriteFailsKeepsPlanRun covers
// the second failure point: the ship state write fails. Nothing is deleted
// and the file on disk holds no summary.
func TestShipState_CleanupPipeline_ExploreSummary_WriteFailsKeepsPlanRun(t *testing.T) {
	f := newPlanRunCleanupFixture(t, "feat/planrun-summary-writefail", true, linkedPlanFile)
	f.writeExplorer(t, "auth-flow", "done", 2)
	f.writeShipReport(t, "md")

	useShipStateWrite(t, func(*state.State) error { return errors.New("disk full") })

	pr, err := f.cleanupPipeline(t, nil)
	if err != nil {
		t.Fatalf("cleanup-pipeline: %v", err)
	}
	want := ShipPlanRunCleanup{Reason: "explorer summary and review rounds not saved: disk full. Fix the cause and call cleanup-pipeline again."}
	if !reflect.DeepEqual(pr, want) {
		t.Errorf("planRun = %#v, want %#v", pr, want)
	}
	f.assertKept(t)
	data := f.shipStateOnDisk(t)
	if _, present := data[shipPlanExploreSummaryKey]; present {
		t.Errorf("planExploreSummary must be absent after a failed write, got %#v", data[shipPlanExploreSummaryKey])
	}
	if data["pipelineStatus"] != "completed" {
		t.Errorf("pipelineStatus = %v, want completed (the stamp is written before the copy)", data["pipelineStatus"])
	}
}

// TestShipState_CleanupPipeline_ExploreSummary_DeleteFailsKeepsSummary covers
// the third failure point: the evidence delete fails after the copy. The
// summary stays in the ship state, the plan run stays, and the reason is the
// remove reason, not the summary reason.
func TestShipState_CleanupPipeline_ExploreSummary_DeleteFailsKeepsSummary(t *testing.T) {
	f := newPlanRunCleanupFixture(t, "feat/planrun-summary-deletefail", true, linkedPlanFile)
	f.writeExplorer(t, "auth-flow", "done", 2)
	f.writeShipReport(t, "md")

	useShipRemoveEvidence(t, func(string) error { return errors.New("evidence busy") })

	pr, err := f.cleanupPipeline(t, nil)
	if err != nil {
		t.Fatalf("cleanup-pipeline: %v", err)
	}
	if want := (ShipPlanRunCleanup{Reason: "remove failed: evidence busy", ExploreSummaryCount: intPtr(1), ReviewRoundsCount: intPtr(0)}); !reflect.DeepEqual(pr, want) {
		t.Errorf("planRun = %#v, want %#v", pr, want)
	}
	f.assertKept(t)
	data := f.shipStateOnDisk(t)
	want := []any{wantExploreEntry("auth-flow", "done", 2, 2)}
	if got := data[shipPlanExploreSummaryKey]; !reflect.DeepEqual(got, any(want)) {
		t.Errorf("stored planExploreSummary = %#v, want %#v", got, want)
	}
}

// TestShipState_CleanupPipeline_ExploreSummary_RetryKeepsStoredList seeds the
// state a failed earlier delete leaves: a stored non-empty list, no evidence
// folder, and the plan state file. A new cleanup-pipeline call reads [] from
// the missing folder, and it must not replace the stored list with it.
func TestShipState_CleanupPipeline_ExploreSummary_RetryKeepsStoredList(t *testing.T) {
	f := newPlanRunCleanupFixture(t, "feat/planrun-summary-retry", true, linkedPlanFile)
	stored := []any{wantExploreEntry("auth-flow", "done", 2, 2)}
	st, err := state.Find(f.dir, "ship", f.branch)
	if err != nil || st == nil {
		t.Fatalf("find ship state: st=%v err=%v", st, err)
	}
	st.Data[shipPlanExploreSummaryKey] = stored
	if err := state.Write(st); err != nil {
		t.Fatalf("seed ship state: %v", err)
	}
	if err := os.RemoveAll(f.evidenceDir); err != nil {
		t.Fatalf("remove evidence dir: %v", err)
	}
	f.writeShipReport(t, "md")

	pr, err := f.cleanupPipeline(t, nil)
	if err != nil {
		t.Fatalf("cleanup-pipeline: %v", err)
	}
	if want := (ShipPlanRunCleanup{Deleted: true, RunID: f.runID, ExploreSummaryCount: intPtr(1), ReviewRoundsCount: intPtr(0)}); !reflect.DeepEqual(pr, want) {
		t.Errorf("planRun = %#v, want %#v", pr, want)
	}
	f.assertDeleted(t)
	if got := f.shipStateOnDisk(t)[shipPlanExploreSummaryKey]; !reflect.DeepEqual(got, any(stored)) {
		t.Errorf("stored planExploreSummary = %#v, want the seeded list %#v", got, stored)
	}
}

// wantReviewRound is one plan review round as the state file stores it after a
// JSON round trip: numbers are float64 and lists are []any.
func wantReviewRound(round int, status string, found, fixed int) map[string]any {
	return map[string]any{
		"round": float64(round), "mergedStatus": status,
		"found": float64(found), "fixed": float64(fixed),
		"lenses": []any{map[string]any{"name": "structure", "verdict": status}},
	}
}

// writeReviewRounds stores rounds in the "reviewRounds" key of the fixture's
// plan run.
func (f planRunCleanupFixture) writeReviewRounds(t *testing.T, rounds []any) {
	t.Helper()
	planRun, err := state.FindPlanRunByPlanFile(f.dir, linkedPlanFile(f.dir))
	if err != nil || planRun == nil {
		t.Fatalf("find plan run: st=%v err=%v", planRun, err)
	}
	planRun.Data["reviewRounds"] = rounds
	if err := state.Write(planRun); err != nil {
		t.Fatalf("write plan run: %v", err)
	}
}

// TestShipState_CleanupPipeline_ReviewRounds_StoredBeforeDelete covers the
// copy: after cleanup deletes the plan run, the ship state file holds the
// plan review rounds beside the explorer summary, and the file validates
// against the schema.
func TestShipState_CleanupPipeline_ReviewRounds_StoredBeforeDelete(t *testing.T) {
	f := newPlanRunCleanupFixture(t, "feat/planrun-rounds-stored", true, linkedPlanFile)
	f.writeExplorer(t, "auth-flow", "done", 2)
	rounds := []any{wantReviewRound(1, planStatusIssuesFound, 3, 2), wantReviewRound(2, planStatusApproved, 0, 0)}
	f.writeReviewRounds(t, rounds)
	f.writeShipReport(t, "md")

	pr, err := f.cleanupPipeline(t, nil)
	if err != nil {
		t.Fatalf("cleanup-pipeline: %v", err)
	}
	if want := (ShipPlanRunCleanup{Deleted: true, RunID: f.runID, ExploreSummaryCount: intPtr(1), ReviewRoundsCount: intPtr(2)}); !reflect.DeepEqual(pr, want) {
		t.Errorf("planRun = %#v, want %#v", pr, want)
	}
	f.assertDeleted(t)

	data := f.shipStateOnDisk(t)
	if got := data[shipPlanReviewRoundsKey]; !reflect.DeepEqual(got, any(rounds)) {
		t.Errorf("stored planReviewRounds = %#v, want %#v", got, rounds)
	}
	want := []any{wantExploreEntry("auth-flow", "done", 2, 2)}
	if got := data[shipPlanExploreSummaryKey]; !reflect.DeepEqual(got, any(want)) {
		t.Errorf("stored planExploreSummary = %#v, want %#v", got, want)
	}
	if err := shipStateSchemaValidator(t)(data); err != nil {
		t.Errorf("ship state with review rounds: schema rejected it: %v", err)
	}
}

// TestShipState_CleanupPipeline_ReviewRounds_AbsentWhenPlanHasNone covers a
// plan run with no review rounds: the key is not stored, so a ship page shows
// explorers only.
func TestShipState_CleanupPipeline_ReviewRounds_AbsentWhenPlanHasNone(t *testing.T) {
	for name, rounds := range map[string][]any{"no key": nil, "empty list": {}} {
		t.Run(name, func(t *testing.T) {
			f := newPlanRunCleanupFixture(t, "feat/planrun-rounds-none-"+strings.ReplaceAll(name, " ", "-"), true, linkedPlanFile)
			if rounds != nil {
				f.writeReviewRounds(t, rounds)
			}
			f.writeShipReport(t, "md")

			pr, err := f.cleanupPipeline(t, nil)
			if err != nil {
				t.Fatalf("cleanup-pipeline: %v", err)
			}
			if !pr.Deleted {
				t.Fatalf("planRun = %#v, want Deleted", pr)
			}
			if got, present := f.shipStateOnDisk(t)[shipPlanReviewRoundsKey]; present {
				t.Errorf("planReviewRounds must be absent, got %#v", got)
			}
		})
	}
}

// TestShipState_CleanupPipeline_ReviewRounds_RetryKeepsStoredList seeds the
// state a failed earlier delete leaves: stored rounds in the ship state and a
// plan run that holds no rounds. A new cleanup-pipeline call must keep the
// stored list.
func TestShipState_CleanupPipeline_ReviewRounds_RetryKeepsStoredList(t *testing.T) {
	f := newPlanRunCleanupFixture(t, "feat/planrun-rounds-retry", true, linkedPlanFile)
	stored := []any{wantReviewRound(1, planStatusIssuesFound, 3, 3)}
	st, err := state.Find(f.dir, "ship", f.branch)
	if err != nil || st == nil {
		t.Fatalf("find ship state: st=%v err=%v", st, err)
	}
	st.Data[shipPlanReviewRoundsKey] = stored
	if err := state.Write(st); err != nil {
		t.Fatalf("seed ship state: %v", err)
	}
	f.writeShipReport(t, "md")

	pr, err := f.cleanupPipeline(t, nil)
	if err != nil {
		t.Fatalf("cleanup-pipeline: %v", err)
	}
	if want := (ShipPlanRunCleanup{Deleted: true, RunID: f.runID, ExploreSummaryCount: intPtr(0), ReviewRoundsCount: intPtr(1)}); !reflect.DeepEqual(pr, want) {
		t.Errorf("planRun = %#v, want %#v", pr, want)
	}
	f.assertDeleted(t)
	if got := f.shipStateOnDisk(t)[shipPlanReviewRoundsKey]; !reflect.DeepEqual(got, any(stored)) {
		t.Errorf("stored planReviewRounds = %#v, want the seeded list %#v", got, stored)
	}
}

// TestShipStateSchema_PlanReviewRounds covers the optional planReviewRounds
// key: absent, empty and filled lists validate; a wrong shape does not.
func TestShipStateSchema_PlanReviewRounds(t *testing.T) {
	validate := shipStateSchemaValidator(t)
	base := func(extra map[string]any) map[string]any {
		doc := map[string]any{
			"version":   float64(1),
			"startedAt": "2026-03-01T12:00:00Z",
			"branch":    "feat/schema-test",
			"flags":     map[string]any{},
			"steps":     []any{map[string]any{"name": "review", "status": "completed"}},
		}
		for k, v := range extra {
			doc[k] = v
		}
		return doc
	}

	accepted := map[string]any{
		"absent":     nil,
		"empty list": []any{},
		"one round":  []any{wantReviewRound(1, planStatusIssuesFound, 3, 2)},
		"two rounds": []any{wantReviewRound(1, planStatusIssuesFound, 3, 2), wantReviewRound(2, planStatusApproved, 0, 0)},
		"with findings": []any{map[string]any{
			"round": float64(1), "mergedStatus": planStatusIssuesFound, "found": float64(1), "fixed": float64(1),
			"lenses":   []any{},
			"findings": []any{map[string]any{"id": "F-1", "fixed": true}},
		}},
	}
	for name, v := range accepted {
		extra := map[string]any{}
		if v != nil {
			extra[shipPlanReviewRoundsKey] = v
		}
		if err := validate(base(extra)); err != nil {
			t.Errorf("%s: want accepted, got %v", name, err)
		}
	}

	rejected := map[string]any{
		"not a list":    map[string]any{"round": float64(1)},
		"null":          nil,
		"entry is text": []any{"round 1"},
		"entry is list": []any{[]any{}},
	}
	for name, v := range rejected {
		if err := validate(base(map[string]any{shipPlanReviewRoundsKey: v})); err == nil {
			t.Errorf("%s: want schema rejection, got nil", name)
		}
	}
}

// TestShipStateSchema_PlanExploreSummary covers the optional planExploreSummary
// key: absent, empty and filled lists validate; a wrong shape does not.
func TestShipStateSchema_PlanExploreSummary(t *testing.T) {
	validate := shipStateSchemaValidator(t)
	base := func(extra map[string]any) map[string]any {
		doc := map[string]any{
			"version":   float64(1),
			"startedAt": "2026-03-01T12:00:00Z",
			"branch":    "feat/schema-test",
			"flags":     map[string]any{},
			"steps":     []any{map[string]any{"name": "review", "status": "completed"}},
		}
		for k, v := range extra {
			doc[k] = v
		}
		return doc
	}

	accepted := map[string]any{
		"absent":      nil,
		"empty list":  []any{},
		"full entry":  []any{wantExploreEntry("auth-flow", "done", 7, 5)},
		"empty top":   []any{map[string]any{"name": "zeta", "status": "unreadable", "total": float64(0), "top": []any{}}},
		"two entries": []any{wantExploreEntry("a", "done", 1, 1), wantExploreEntry("b", "running", 0, 0)},
	}
	for name, v := range accepted {
		extra := map[string]any{}
		if v != nil {
			extra[shipPlanExploreSummaryKey] = v
		}
		if err := validate(base(extra)); err != nil {
			t.Errorf("%s: want accepted, got %v", name, err)
		}
	}

	rejected := map[string]any{
		"not a list":    map[string]any{"name": "a"},
		"null":          nil,
		"missing total": []any{map[string]any{"name": "a", "status": "done", "top": []any{}}},
		"missing top":   []any{map[string]any{"name": "a", "status": "done", "total": float64(1)}},
		"string total":  []any{map[string]any{"name": "a", "status": "done", "total": "1", "top": []any{}}},
		"entry is text": []any{"auth-flow"},
	}
	for name, v := range rejected {
		if err := validate(base(map[string]any{shipPlanExploreSummaryKey: v})); err == nil {
			t.Errorf("%s: want schema rejection, got nil", name)
		}
	}
}

// ---------------------------------------------------------------------------
// gc: dry-run and real sweep
// ---------------------------------------------------------------------------

func TestShipState_GC_DryRun_ClassifiesAndDropsCommit(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	execDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
	staleShip := filepath.Join(execDir, "ship-dead-branch-20200101T000000Z.json")
	writeFile(t, staleShip, `{}`)
	setStateFileMtime(t, staleShip, 30*24*time.Hour)

	freshExecute := filepath.Join(execDir, "execute-main-20260901T000000Z.json")
	writeFile(t, freshExecute, `{}`)
	setStateFileMtime(t, freshExecute, 1*time.Hour)

	// commit-prefixed files must be silently dropped from dry-run output.
	commitFile := filepath.Join(execDir, "commit-dead-branch-20200101T000000Z.json")
	writeFile(t, commitFile, `{}`)
	setStateFileMtime(t, commitFile, 30*24*time.Hour)

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "gc",
		Detail: map[string]any{"dryRun": true},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("gc dry-run: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want map[string]any", out)
	}
	if m["dryRun"] != true {
		t.Errorf("dryRun = %v, want true", m["dryRun"])
	}
	if _, hasCommit := m["commit"]; hasCommit {
		t.Error("dry-run output must not include a 'commit' bucket")
	}
	shipBucket, _ := m["ship"].(map[string]any)
	wouldDelete, _ := shipBucket["wouldDelete"].([]any)
	if len(wouldDelete) != 1 {
		t.Errorf("ship.wouldDelete = %v, want 1 entry (stale dead-branch file)", wouldDelete)
	}
	executeBucket, _ := m["execute"].(map[string]any)
	wouldKeep, _ := executeBucket["wouldKeep"].([]any)
	if len(wouldKeep) != 1 {
		t.Errorf("execute.wouldKeep = %v, want 1 entry (ttl-fresh file)", wouldKeep)
	}
}

func TestShipState_GC_RealRunIncludesCommitBucket(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	execDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
	staleCommit := filepath.Join(execDir, "commit-dead-branch-20200101T000000Z.json")
	writeFile(t, staleCommit, `{}`)
	setStateFileMtime(t, staleCommit, 30*24*time.Hour)

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "gc",
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	rpt, ok := out.(ShipStateGCReport)
	if !ok {
		t.Fatalf("output = %#v, want ShipStateGCReport", out)
	}
	if rpt.TTLDays != 7 {
		t.Errorf("TTLDays = %d, want 7 (default)", rpt.TTLDays)
	}
	if !sliceContainsStr(rpt.Commit.Deleted, staleCommit) {
		t.Errorf("Commit.Deleted = %v, want it to include %s", rpt.Commit.Deleted, staleCommit)
	}
	if _, statErr := os.Stat(staleCommit); !os.IsNotExist(statErr) {
		t.Errorf("stale commit file %s should have been deleted by the real run", staleCommit)
	}
}

func TestShipState_GC_TTLDaysZeroIsLiteral(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	execDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
	f := filepath.Join(execDir, "ship-dead-branch-20200101T000000Z.json")
	writeFile(t, f, `{}`)
	setStateFileMtime(t, f, 1*time.Second)

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "gc",
		Detail: map[string]any{"ttlDays": float64(0)},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	rpt := out.(ShipStateGCReport)
	if rpt.TTLDays != 0 {
		t.Errorf("TTLDays = %d, want 0 (explicit zero must survive, not be upgraded to the 7-day default)", rpt.TTLDays)
	}
	if !sliceContainsStr(rpt.Ship.Deleted, f) {
		t.Errorf("Ship.Deleted = %v, want it to include %s (ttlDays=0 means nothing is ttl-fresh)", rpt.Ship.Deleted, f)
	}
}

// A dry run must predict exactly what the real run then deletes. Two cases
// used to differ: a TTL-fresh file of a gone branch (the real run deletes
// it) and a stale, non-newest file of a live branch (the real run deletes
// it too).
func TestShipState_GC_DryRunMatchesRealRun(t *testing.T) {
	t.Setenv("SDLC_EXPLORE_TMPDIR_OVERRIDE", t.TempDir())
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/live")

	runs := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
	fixture := []struct {
		name string
		age  time.Duration
	}{
		{"ship-dead-branch-20260901T000000Z.json", 1 * time.Hour},        // gone branch, fresh
		{"ship-dead-branch-20200101T000000Z.json", 30 * 24 * time.Hour},  // gone branch, stale
		{"execute-feat-live-20200101T000000Z.json", 30 * 24 * time.Hour}, // live branch, stale, older
		{"execute-feat-live-20200201T000000Z.json", 20 * 24 * time.Hour}, // live branch, stale, newest
		{"plan-feat-live-20260901T000000Z.json", 1 * time.Hour},          // live branch, fresh
	}
	for _, f := range fixture {
		p := filepath.Join(runs, f.name)
		writeFile(t, p, `{}`)
		setStateFileMtime(t, p, f.age)
	}

	dry, err := shipState(dir, dir, ShipStateIn{
		Action: "gc",
		Detail: map[string]any{"dryRun": true},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("gc dry-run: %v", err)
	}
	dm, _ := dry.(map[string]any)
	wouldDelete := map[string]string{} // file -> reason
	for _, prefix := range []string{"ship", "execute", "plan"} {
		b, _ := dm[prefix].(map[string]any)
		list, _ := b["wouldDelete"].([]any)
		for _, e := range list {
			em, _ := e.(map[string]any)
			wouldDelete[em["file"].(string)] = em["reason"].(string)
		}
	}

	realOut, err := shipState(dir, dir, ShipStateIn{Action: "gc"}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	rpt := realOut.(ShipStateGCReport)
	deleted := map[string]bool{}
	for _, b := range []ShipGCBucket{rpt.Ship, rpt.Execute, rpt.Plan} {
		for _, p := range b.Deleted {
			deleted[filepath.Base(p)] = true
		}
	}

	want := map[string]string{
		"ship-dead-branch-20260901T000000Z.json":  "branch-gone",
		"ship-dead-branch-20200101T000000Z.json":  "stale+branch-gone",
		"execute-feat-live-20200101T000000Z.json": "stale+superseded",
	}
	if len(deleted) != len(want) {
		t.Errorf("real run deleted %v, want exactly %v", deleted, want)
	}
	for name, reason := range want {
		if !deleted[name] {
			t.Errorf("real run did not delete %s", name)
		}
		if got, ok := wouldDelete[name]; !ok || got != reason {
			t.Errorf("dry run wouldDelete[%s] = %q (listed: %v), want reason %q", name, got, ok, reason)
		}
	}
	if len(wouldDelete) != len(deleted) {
		t.Errorf("dry run wouldDelete = %v, real run deleted = %v; they must match", wouldDelete, deleted)
	}
}

// ---------------------------------------------------------------------------
// migrate
// ---------------------------------------------------------------------------

func TestShipState_Migrate(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	oldSlug := state.SlugifyBranch("feat/old-name")
	execDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
	oldPath := filepath.Join(execDir, fmt.Sprintf("ship-%s-20200101T000000Z.json", oldSlug))
	writeFile(t, oldPath, `{"branch": "feat/old-name"}`)

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "migrate",
		Detail: map[string]any{"from": "feat/old-name", "to": "feat/new-name"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["migrated"] != true {
		t.Errorf("output = %#v, want {migrated:true}", out)
	}

	newSlug := state.SlugifyBranch("feat/new-name")
	newPath := filepath.Join(execDir, fmt.Sprintf("ship-%s-20200101T000000Z.json", newSlug))
	if _, statErr := os.Stat(newPath); statErr != nil {
		t.Errorf("renamed file %s should exist: %v", newPath, statErr)
	}
	if _, statErr := os.Stat(oldPath); !os.IsNotExist(statErr) {
		t.Errorf("old file %s should no longer exist", oldPath)
	}
}

func TestShipState_Migrate_RequiresFromAndTo(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	_, err := shipState(dir, dir, ShipStateIn{
		Action: "migrate",
		Detail: map[string]any{"from": "feat/only-from"},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("migrate without 'to': want error, got nil")
	}
}

// ---------------------------------------------------------------------------
// next (Go-native, KD14)
// ---------------------------------------------------------------------------

func TestShipState_Next_ReturnsFirstBlockingStep(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/next")
	path := shipStateInitFixture(t, dir, "feat/next")
	setStepStatus(t, path, "execute", "completed", map[string]any{"completedAt": "2026-01-01T00:00:00Z"})

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "next",
		Detail: map[string]any{"branch": "feat/next"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	nextOut, ok := out.(ShipNextOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipNextOut", out)
	}
	if nextOut.Step != "commit" {
		t.Errorf("Step = %q, want %q (execute is done, commit is the next bare-pending step)", nextOut.Step, "commit")
	}
	if nextOut.Automation != "confirm" {
		t.Errorf("Automation = %q, want %q (no automation config present)", nextOut.Automation, "confirm")
	}
}

func TestShipState_Next_EmptyWhenPipelineComplete(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/next-done")
	path := shipStateInitFixture(t, dir, "feat/next-done")

	for _, name := range []string{"execute", "commit", "review", "received-review", "commit-fixes", "version", "pr"} {
		setStepStatus(t, path, name, "completed", map[string]any{"completedAt": "2026-01-01T00:00:00Z"})
	}

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "next",
		Detail: map[string]any{"branch": "feat/next-done"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	nextOut := out.(ShipNextOut)
	if nextOut.Step != "" {
		t.Errorf("Step = %q, want empty (pipeline complete is not an error)", nextOut.Step)
	}
}

// TestShipState_Next_MatchesBeginStepAcceptance is the AC3 consistency test:
// "next returns the same step that begin-step would accept next, with the
// automation mode resolved from a fixture config." It writes a config
// fixture, chains next -> begin-step on the same fixture/state, asserts
// begin-step accepts exactly the step next named, and asserts next's
// Automation field reflects the fixture config.
func TestShipState_Next_MatchesBeginStepAcceptance(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/next-matches-beginstep")
	path := shipStateInitFixture(t, dir, "feat/next-matches-beginstep")
	setStepStatus(t, path, "execute", "completed", map[string]any{"completedAt": "2026-01-01T00:00:00Z"})

	// "automation" is a local section (internal/config/config.go
	// ProjectSections), read from local.toml, not config.toml.
	writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), "[automation]\nmode = \"confirm\"\n")

	nextOut, err := shipState(dir, dir, ShipStateIn{
		Action: "next",
		Detail: map[string]any{"branch": "feat/next-matches-beginstep"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	nout := nextOut.(ShipNextOut)
	step := nout.Step
	if step == "" {
		t.Fatal("next returned an empty step, want a concrete next step")
	}
	if nout.Automation != "confirm" {
		t.Errorf("Automation = %q, want %q (from fixture v5 config)", nout.Automation, "confirm")
	}

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "begin-step",
		Step:   step,
		Detail: map[string]any{"branch": "feat/next-matches-beginstep"},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("begin-step %q (the step next named): %v, want acceptance", step, err)
	}
}

func TestShipState_Next_HonorsAutomationConfig(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/next-automation")
	shipStateInitFixture(t, dir, "feat/next-automation")

	writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), "")
	writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), "[automation]\nmode = \"unattended\"\n")

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "next",
		Detail: map[string]any{"branch": "feat/next-automation"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	nextOut := out.(ShipNextOut)
	if nextOut.Automation != "auto" {
		t.Errorf("Automation = %q, want %q (mode:unattended)", nextOut.Automation, "auto")
	}
}

// ---------------------------------------------------------------------------
// todos (Go-native, KD16)
// ---------------------------------------------------------------------------

func TestShipState_Todos(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/todos")
	path := shipStateInitFixture(t, dir, "feat/todos")
	setStepStatus(t, path, "execute", "in_progress", map[string]any{"startedAt": "2026-01-01T00:00:00Z"})

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "todos",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/todos"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("todos: %v", err)
	}
	todosOut, ok := out.(ShipTodosOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipTodosOut", out)
	}
	if len(todosOut.Todos) == 0 {
		t.Fatal("Todos is empty")
	}
}

// ---------------------------------------------------------------------------
// --state-file dual resolution path (loadShipStateOrExit equivalent)
// ---------------------------------------------------------------------------

func TestShipState_StateFileParam_BypassesBranchResolution(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/state-file-param")
	path := shipStateInitFixture(t, dir, "feat/state-file-param")

	// No "branch" in Detail at all — only stateFile. If branch resolution
	// were still attempted, this would resolve to the checked-out branch
	// anyway in this fixture, so it wouldn't distinguish "bypassed" from
	// "coincidentally consistent."
	out, err := shipState(dir, dir, ShipStateIn{
		Action: "begin-step",
		Step:   "commit",
		Detail: map[string]any{"stateFile": path},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("begin-step commit via stateFile: want proceed-gate error (execute still pending), got nil")
	}
	if _, ok := out.(ShipStepNarrationOut); ok {
		t.Fatal("expected no output on a gated begin-step")
	}

	// Prove it for real: pass a workDir with no git repo at all, where
	// branch auto-detection would fail outright. If stateFile genuinely
	// bypasses branch resolution, the call still reaches the same
	// proceed-gate domain error instead of an infra error from git.
	nonGitDir := t.TempDir()
	out2, err2 := shipState(dir, nonGitDir, ShipStateIn{
		Action: "begin-step",
		Step:   "commit",
		Detail: map[string]any{"stateFile": path},
	}, fixedNow(time.Now()))
	if err2 == nil {
		t.Fatal("begin-step commit via stateFile (non-git workDir): want proceed-gate error, got nil")
	}
	if !isDomainError(err2) {
		t.Fatalf("begin-step commit via stateFile (non-git workDir): err = %v, want a DomainError (proceed-gate), not a branch-resolution failure", err2)
	}
	if _, ok := out2.(ShipTodosOut); ok {
		t.Fatal("expected no output on a gated begin-step")
	}
}

// ---------------------------------------------------------------------------
// sessionId / ClaimSession compatibility (AC2)
// ---------------------------------------------------------------------------

// TestShipState_SessionID_ClaimSessionCompatible verifies init's sessionId
// stamp and state.ClaimSession (Task 9/10's session-locking primitive) write
// through the exact same "sessionId" data key, so a hook or another tool
// using state.ClaimSession directly on a ship_state-initialized file is
// read-compatible with what "read"/"todos"/etc. observe afterward.
func TestShipState_SessionID_ClaimSessionCompatible(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/claim-session")
	shipStateInitFixture(t, dir, "feat/claim-session")

	st, err := state.Find(dir, "ship", "feat/claim-session")
	if err != nil || st == nil {
		t.Fatalf("state.Find: %v (st=%v)", err, st)
	}
	if st.Data["sessionId"] != "sess-init" {
		t.Fatalf("sessionId = %v, want sess-init (from the init fixture)", st.Data["sessionId"])
	}

	if err := state.ClaimSession(st, "sess-reclaimed"); err != nil {
		t.Fatalf("ClaimSession: %v", err)
	}

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "read",
		Detail: map[string]any{"branch": "feat/claim-session"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	data := out.(map[string]any)
	if data["sessionId"] != "sess-reclaimed" {
		t.Errorf("sessionId = %v, want sess-reclaimed (ClaimSession's write must be visible to ship_state's own read path)", data["sessionId"])
	}
	// Steps must be untouched by the claim.
	steps, _ := data["steps"].([]any)
	if len(steps) != 6 {
		t.Errorf("steps = %v, want the original 6-entry scaffold preserved", steps)
	}
}

// ---------------------------------------------------------------------------
// narration: JSON shape
// ---------------------------------------------------------------------------

// TestShipStepNarrationOut_JSONFlatShape verifies the anonymous embedding of
// pipeline.Narration produces flat JSON (summary/display/timing/next at the
// top level, not nested under a "Narration" key).
func TestShipStepNarrationOut_JSONFlatShape(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/json-shape")
	shipStateInitFixture(t, dir, "feat/json-shape")

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "begin-step",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/json-shape"},
	}, fixedNow(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("begin-step: %v", err)
	}

	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var flat map[string]any
	if err := json.Unmarshal(raw, &flat); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"summary", "display", "next", "todos"} {
		if _, ok := flat[key]; !ok {
			t.Errorf("missing top-level key %q in JSON: %s", key, string(raw))
		}
	}
	if _, ok := flat["Narration"]; ok {
		t.Error("Narration is nested as a sub-object instead of being embedded flat")
	}
}

// ---------------------------------------------------------------------------
// narration: timing recording
// ---------------------------------------------------------------------------

func TestShipState_CompleteStep_RecordsTiming(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/timing-record")
	path := shipStateInitFixture(t, dir, "feat/timing-record")

	startTime := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	setStepStatus(t, path, "execute", "in_progress", map[string]any{
		"startedAt": startTime.Format(time.RFC3339),
	})

	completeTime := startTime.Add(42 * time.Second)
	out, err := shipState(dir, dir, ShipStateIn{
		Action: "complete-step",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/timing-record", "outcome": "success"},
	}, fixedNow(completeTime))
	if err != nil {
		t.Fatalf("complete-step: %v", err)
	}
	narr, ok := out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipStepNarrationOut", out)
	}
	if narr.Timing == nil {
		t.Fatal("Timing is nil, want timing info")
	}
	if narr.Timing.StepSeconds != 42 {
		t.Errorf("StepSeconds = %d, want 42", narr.Timing.StepSeconds)
	}

	// Verify the timings store has the recorded sample.
	ts := pipeline.NewTimingsStore(dir)
	est, ok := ts.Estimate("ship:execute")
	if !ok {
		t.Fatal("ship:execute not recorded in timings store")
	}
	if est.Seconds != 42 {
		t.Errorf("ship:execute estimate = %d, want 42", est.Seconds)
	}
}

func TestShipState_CompleteStep_AwaitRemoteReviewNotRecorded(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/no-record-await")
	path := shipStateInitFixture(t, dir, "feat/no-record-await")

	// Add await-remote-review to the steps.
	data := readStateData(t, path)
	steps, _ := data["steps"].([]any)
	steps = append(steps, map[string]any{
		"name":      "await-remote-review",
		"status":    "in_progress",
		"startedAt": time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})
	data["steps"] = steps
	raw, marshalErr := json.Marshal(data)
	if marshalErr != nil {
		t.Fatalf("marshal: %v", marshalErr)
	}
	if writeErr := os.WriteFile(path, raw, 0o644); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	completeTime := time.Date(2026, 1, 2, 0, 10, 0, 0, time.UTC)
	_, err := shipState(dir, dir, ShipStateIn{
		Action: "complete-step",
		Step:   "await-remote-review",
		Detail: map[string]any{"branch": "feat/no-record-await", "outcome": "success"},
	}, fixedNow(completeTime))
	if err != nil {
		t.Fatalf("complete-step: %v", err)
	}

	ts := pipeline.NewTimingsStore(dir)
	if _, ok := ts.Estimate("ship:await-remote-review"); ok {
		t.Error("ship:await-remote-review was recorded, want it excluded (HumanWaitSteps guard)")
	}
}

// ---------------------------------------------------------------------------
// narration: detail field
// ---------------------------------------------------------------------------

func TestShipState_DetailConcise_OmitsDisplayOnNonBoundary(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/detail-concise")
	shipStateInitFixture(t, dir, "feat/detail-concise")

	// Non-boundary action (skip) with detail="concise" should omit Display.
	out, err := shipState(dir, dir, ShipStateIn{
		Action: "skip",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/detail-concise", "detail": "concise", "reason": "test"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("skip concise: %v", err)
	}
	narr, ok := out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipStepNarrationOut", out)
	}
	if narr.Summary == "" {
		t.Error("Summary is empty, want a narration summary even in concise mode")
	}
	if narr.Display != "" {
		t.Error("Display should be empty in concise mode for non-boundary actions")
	}
}

func TestShipState_DetailConcise_BoundaryAlwaysIncludesDisplay(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/detail-boundary")
	shipStateInitFixture(t, dir, "feat/detail-boundary")

	// Boundary action (begin-step) always includes Display even with concise.
	out, err := shipState(dir, dir, ShipStateIn{
		Action: "begin-step",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/detail-boundary", "detail": "concise"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("begin-step concise: %v", err)
	}
	narr := out.(ShipStepNarrationOut)
	if narr.Display == "" {
		t.Error("Display should not be empty for boundary action even in concise mode")
	}
}

func TestShipState_DetailInvalid_DomainError(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/detail-invalid")
	shipStateInitFixture(t, dir, "feat/detail-invalid")

	_, err := shipState(dir, dir, ShipStateIn{
		Action: "skip",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/detail-invalid", "detail": "verbose"},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("want DomainError for invalid detail value, got nil")
	}
	if !isDomainError(err) {
		t.Errorf("error = %v (%T), want DomainError", err, err)
	}
}

// ---------------------------------------------------------------------------
// narration: next step in complete-step
// ---------------------------------------------------------------------------

func TestShipState_CompleteStep_ReturnsNextStep(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/next-return")
	path := shipStateInitFixture(t, dir, "feat/next-return")

	setStepStatus(t, path, "execute", "in_progress", map[string]any{
		"startedAt": time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "complete-step",
		Step:   "execute",
		Detail: map[string]any{"branch": "feat/next-return", "outcome": "success"},
	}, fixedNow(time.Date(2026, 1, 2, 0, 1, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("complete-step: %v", err)
	}
	narr := out.(ShipStepNarrationOut)
	if narr.Next == nil {
		t.Fatal("Next is nil, want next step")
	}
	if narr.Next.ID != "commit" {
		t.Errorf("Next.ID = %q, want %q", narr.Next.ID, "commit")
	}
	if narr.Next.Instruction == "" {
		t.Error("Next.Instruction is empty")
	}
}

func TestShipState_CompleteStep_NilNextAtPipelineEnd(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/nil-next")
	path := shipStateInitFixture(t, dir, "feat/nil-next")

	for _, name := range []string{"execute", "commit", "review", "version"} {
		setStepStatus(t, path, name, "completed", map[string]any{
			"completedAt": "2026-01-01T00:00:00Z",
			"startedAt":   "2026-01-01T00:00:00Z",
		})
	}
	// received-review and commit-fixes are conditional, stay pending.
	setStepStatus(t, path, "pr", "in_progress", map[string]any{
		"startedAt": time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
	})

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "complete-step",
		Step:   "pr",
		Detail: map[string]any{"branch": "feat/nil-next", "outcome": "success"},
	}, fixedNow(time.Date(2026, 1, 2, 0, 1, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("complete-step: %v", err)
	}
	narr := out.(ShipStepNarrationOut)
	if narr.Next != nil {
		t.Errorf("Next = %+v, want nil (pipeline complete)", narr.Next)
	}
}

// ---------------------------------------------------------------------------
// history_record
// ---------------------------------------------------------------------------

func TestShipState_HistoryRecord_Success(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	out, err := shipState(root, root, ShipStateIn{
		Action: "history_record",
		Detail: map[string]any{
			"skill":   "ship",
			"outcome": "success",
			"ts":      "2026-06-15T12:00:00Z",
			"branch":  "feat/test",
		},
	}, fixedNow(now))
	if err != nil {
		t.Fatalf("history_record: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want map[string]any", out)
	}
	if m["ok"] != true {
		t.Errorf("ok = %v, want true", m["ok"])
	}
	if m["ts"] != "2026-06-15T12:00:00Z" {
		t.Errorf("ts = %v, want 2026-06-15T12:00:00Z", m["ts"])
	}

	// Verify runs.jsonl was written.
	runsPath := filepath.Join(root, paths.DataDir, "history", "runs.jsonl")
	if _, statErr := os.Stat(runsPath); statErr != nil {
		t.Errorf("runs.jsonl should exist: %v", statErr)
	}
}

func TestShipState_HistoryRecord_MissingDetail(t *testing.T) {
	root := t.TempDir()
	_, err := shipState(root, root, ShipStateIn{
		Action: "history_record",
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("history_record with nil detail: want error, got nil")
	}
	if !isDomainError(err) {
		t.Errorf("error = %v (%T), want DomainError", err, err)
	}
}

func TestShipState_HistoryRecord_MissingSkill(t *testing.T) {
	root := t.TempDir()
	_, err := shipState(root, root, ShipStateIn{
		Action: "history_record",
		Detail: map[string]any{
			"outcome": "success",
		},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("history_record without skill: want error, got nil")
	}
	if !isDomainError(err) {
		t.Errorf("error = %v (%T), want DomainError", err, err)
	}
}

// ---------------------------------------------------------------------------
// deferred_add
// ---------------------------------------------------------------------------

func TestShipState_DeferredAdd_Success(t *testing.T) {
	root := t.TempDir()

	out, err := shipState(root, root, ShipStateIn{
		Action: "deferred_add",
		Detail: map[string]any{
			"id":          "issue-1",
			"description": "Fix flaky test",
		},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("deferred_add: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want map[string]any", out)
	}
	if m["ok"] != true {
		t.Errorf("ok = %v, want true", m["ok"])
	}
	if m["id"] != "issue-1" {
		t.Errorf("id = %v, want issue-1", m["id"])
	}
}

func TestShipState_DeferredAdd_MissingID(t *testing.T) {
	root := t.TempDir()
	_, err := shipState(root, root, ShipStateIn{
		Action: "deferred_add",
		Detail: map[string]any{
			"description": "Fix flaky test",
		},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("deferred_add without id: want error, got nil")
	}
	if !isDomainError(err) {
		t.Errorf("error = %v (%T), want DomainError", err, err)
	}
}

func TestShipState_DeferredAdd_MissingDescription(t *testing.T) {
	root := t.TempDir()
	_, err := shipState(root, root, ShipStateIn{
		Action: "deferred_add",
		Detail: map[string]any{
			"id": "issue-2",
		},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("deferred_add without description: want error, got nil")
	}
	if !isDomainError(err) {
		t.Errorf("error = %v (%T), want DomainError", err, err)
	}
}

// ---------------------------------------------------------------------------
// deferred_list
// ---------------------------------------------------------------------------

func TestShipState_DeferredList_Empty(t *testing.T) {
	root := t.TempDir()

	out, err := shipState(root, root, ShipStateIn{
		Action: "deferred_list",
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("deferred_list: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want map[string]any", out)
	}
	openCount, _ := m["openCount"].(int)
	if openCount != 0 {
		t.Errorf("openCount = %d, want 0", openCount)
	}
}

func TestShipState_DeferredList_AfterAdd(t *testing.T) {
	root := t.TempDir()

	for _, id := range []string{"issue-a", "issue-b"} {
		if _, err := shipState(root, root, ShipStateIn{
			Action: "deferred_add",
			Detail: map[string]any{
				"id":          id,
				"description": "Issue " + id,
			},
		}, fixedNow(time.Now())); err != nil {
			t.Fatalf("deferred_add %s: %v", id, err)
		}
	}

	out, err := shipState(root, root, ShipStateIn{
		Action: "deferred_list",
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("deferred_list: %v", err)
	}
	m := out.(map[string]any)
	openCount, _ := m["openCount"].(int)
	if openCount != 2 {
		t.Errorf("openCount = %d, want 2", openCount)
	}
}

// ---------------------------------------------------------------------------
// deferred_propose_followups
// ---------------------------------------------------------------------------

func TestShipState_DeferredProposeFollowups_Empty(t *testing.T) {
	root := t.TempDir()

	out, err := shipState(root, root, ShipStateIn{
		Action: "deferred_propose_followups",
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("deferred_propose_followups: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want map[string]any", out)
	}
	openCount, _ := m["openCount"].(int)
	if openCount != 0 {
		t.Errorf("openCount = %d, want 0", openCount)
	}
	if _, hasGroups := m["groups"]; !hasGroups {
		t.Error("missing 'groups' key in output")
	}
	if _, hasDisplay := m["display"]; !hasDisplay {
		t.Error("missing 'display' key in output")
	}
}

func TestShipState_DeferredProposeFollowups_WithData(t *testing.T) {
	root := t.TempDir()

	issues := []map[string]any{
		{"id": "high-1", "description": "Critical bug", "priority": "high"},
		{"id": "med-1", "description": "Minor cleanup", "priority": "medium"},
	}
	for _, issue := range issues {
		if _, err := shipState(root, root, ShipStateIn{
			Action: "deferred_add",
			Detail: issue,
		}, fixedNow(time.Now())); err != nil {
			t.Fatalf("deferred_add %v: %v", issue["id"], err)
		}
	}

	out, err := shipState(root, root, ShipStateIn{
		Action: "deferred_propose_followups",
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("deferred_propose_followups: %v", err)
	}
	m := out.(map[string]any)
	openCount, _ := m["openCount"].(int)
	if openCount != 2 {
		t.Errorf("openCount = %d, want 2", openCount)
	}
	display, _ := m["display"].(string)
	if display == "" {
		t.Error("display is empty, want a formatted summary")
	}
}

// ---------------------------------------------------------------------------
// deferred_resolve
// ---------------------------------------------------------------------------

func TestShipState_DeferredResolve_Success(t *testing.T) {
	root := t.TempDir()

	// Add an issue first.
	if _, err := shipState(root, root, ShipStateIn{
		Action: "deferred_add",
		Detail: map[string]any{
			"id":          "resolve-me",
			"description": "Will be resolved",
		},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("deferred_add: %v", err)
	}

	// Resolve it.
	out, err := shipState(root, root, ShipStateIn{
		Action: "deferred_resolve",
		Detail: map[string]any{"id": "resolve-me"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("deferred_resolve: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("output = %#v, want map[string]any", out)
	}
	if m["ok"] != true {
		t.Errorf("ok = %v, want true", m["ok"])
	}
	if m["id"] != "resolve-me" {
		t.Errorf("id = %v, want resolve-me", m["id"])
	}

	// Verify via list that the issue is no longer open.
	listOut, err := shipState(root, root, ShipStateIn{
		Action: "deferred_list",
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("deferred_list: %v", err)
	}
	lm := listOut.(map[string]any)
	openCount, _ := lm["openCount"].(int)
	if openCount != 0 {
		t.Errorf("openCount after resolve = %d, want 0", openCount)
	}
}

func TestShipState_DeferredResolve_NotFound(t *testing.T) {
	root := t.TempDir()

	_, err := shipState(root, root, ShipStateIn{
		Action: "deferred_resolve",
		Detail: map[string]any{"id": "nonexistent"},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("deferred_resolve nonexistent: want error, got nil")
	}
	if !isDomainError(err) {
		t.Errorf("error = %v (%T), want DomainError", err, err)
	}
}

// ---------------------------------------------------------------------------
// error classification helpers
// ---------------------------------------------------------------------------

func isDomainError(err error) bool {
	var de *mcpserver.DomainError
	return errors.As(err, &de)
}

// ---------------------------------------------------------------------------
// log-cli
// ---------------------------------------------------------------------------

func TestShipState_LogCLI_AppendsEvidence(t *testing.T) {
	root := t.TempDir()

	result, err := shipState(root, root, ShipStateIn{
		Action: "log-cli",
		Detail: map[string]any{
			"branch":     "release/1.2",
			"step":       "commit",
			"command":    "git commit -m \"fix\"",
			"exitCode":   1,
			"outputHead": "error: nothing to commit",
		},
	}, fixedNow(time.Now()))
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
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &entry); err != nil {
		t.Fatalf("unmarshal evidence entry: %v", err)
	}
	if entry.Pipeline != "ship" {
		t.Errorf("Pipeline = %q, want ship", entry.Pipeline)
	}
	if entry.Step != "commit" {
		t.Errorf("Step = %q, want commit", entry.Step)
	}
	if entry.Branch != "release/1.2" {
		t.Errorf("Branch = %q, want release/1.2", entry.Branch)
	}
	if entry.Command != "git commit -m \"fix\"" {
		t.Errorf("Command = %q, want the git commit invocation", entry.Command)
	}
	if entry.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", entry.ExitCode)
	}
	if entry.OutputHead != "error: nothing to commit" {
		t.Errorf("OutputHead = %q, want the stubbed error text", entry.OutputHead)
	}
	if entry.Wave != nil {
		t.Errorf("Wave = %v, want nil (ship pipeline has no wave concept)", entry.Wave)
	}
}

// TestShipState_LogCLI_MissingExitCodeDefaultsToZero exercises
// detailIntPtr's nil-vs-set distinction from the caller's side: when
// "exitCode" is absent from Detail entirely (as opposed to explicitly
// present and 0), shipStateLogCLI must not error and must record ExitCode 0.
func TestShipState_LogCLI_MissingExitCodeDefaultsToZero(t *testing.T) {
	root := t.TempDir()

	_, err := shipState(root, root, ShipStateIn{
		Action: "log-cli",
		Detail: map[string]any{
			"branch":  "main",
			"command": "echo hi",
		},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("log-cli without exitCode: %v", err)
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
	if entry.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 when omitted from Detail", entry.ExitCode)
	}
}

// TestShipState_LogCLI_NoBranchNoGitRepo confirms branch resolution failure
// (no explicit branch in Detail, workDir not a git repo) propagates
// unwrapped as the *mcpserver.DomainError execResolveBranch produces,
// rather than being swallowed or re-wrapped as an InfraError.
func TestShipState_LogCLI_NoBranchNoGitRepo(t *testing.T) {
	root := t.TempDir()

	_, err := shipState(root, root, ShipStateIn{
		Action: "log-cli",
		Detail: map[string]any{"command": "echo hi"},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("expected error when branch cannot be resolved")
	}
	if !isDomainError(err) {
		t.Errorf("error = %v (%T), want DomainError", err, err)
	}
}

// TestShipState_LogCLI_AppendFailure forces appendCLIEvidence to fail (a
// regular file sits where the evidence directory must be created) and
// confirms shipStateLogCLI wraps it as an *mcpserver.InfraError with a
// "log-cli: " prefixed message.
func TestShipState_LogCLI_AppendFailure(t *testing.T) {
	root := t.TempDir()

	evidenceDir := filepath.Join(root, paths.DataDir, "evidence")
	if err := os.MkdirAll(filepath.Dir(evidenceDir), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(evidenceDir, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := shipState(root, root, ShipStateIn{
		Action: "log-cli",
		Detail: map[string]any{"branch": "main", "command": "go vet ./..."},
	}, fixedNow(time.Now()))
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

// shipStateDispatcherActions parses ship_state.go and returns every case
// label of the shipState dispatcher switch — the one switch on in.Action
// that has a default clause. The detail-level validation switch above it
// switches on in.Action too, but has no default, so it is skipped.
//
// Reading the labels from the source instead of restating them keeps the
// two tests below honest: adding a case to the dispatcher without touching
// the enum tag or the unknown-action hint fails them.
func shipStateDispatcherActions(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "ship_state.go", nil, 0)
	if err != nil {
		t.Fatalf("parse ship_state.go: %v", err)
	}

	var actions []string
	ast.Inspect(f, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		sel, ok := sw.Tag.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Action" {
			return true
		}
		hasDefault := false
		for _, stmt := range sw.Body.List {
			if cc, isCase := stmt.(*ast.CaseClause); isCase && cc.List == nil {
				hasDefault = true
			}
		}
		if !hasDefault {
			return true
		}
		for _, stmt := range sw.Body.List {
			cc, isCase := stmt.(*ast.CaseClause)
			if !isCase {
				continue
			}
			for _, expr := range cc.List {
				bl, isLit := expr.(*ast.BasicLit)
				if !isLit || bl.Kind != token.STRING {
					continue
				}
				name, uerr := strconv.Unquote(bl.Value)
				if uerr != nil {
					t.Fatalf("unquote case label %s: %v", bl.Value, uerr)
				}
				actions = append(actions, name)
			}
		}
		return true
	})

	if len(actions) == 0 {
		t.Fatal("found no dispatcher case labels in ship_state.go")
	}
	return actions
}

// TestShipStateActionEnumCoversDispatcher pins the jsonschema enum tag on
// ShipStateIn.Action to the dispatcher's own case labels. Without this the
// schema can advertise fewer actions than the tool accepts, which is how
// next, todos and the five history/deferred actions stayed hidden from
// callers while ship/SKILL.md called them.
func TestShipStateActionEnumCoversDispatcher(t *testing.T) {
	field, ok := reflect.TypeOf(ShipStateIn{}).FieldByName("Action")
	if !ok {
		t.Fatal("ShipStateIn has no Action field")
	}

	tagged := map[string]bool{}
	for _, part := range strings.Split(field.Tag.Get("jsonschema"), ",") {
		if v, found := strings.CutPrefix(strings.TrimSpace(part), "enum="); found {
			tagged[v] = true
		}
	}
	if len(tagged) == 0 {
		t.Fatal("Action has no jsonschema enum= entries")
	}

	actions := shipStateDispatcherActions(t)
	for _, a := range actions {
		if !tagged[a] {
			t.Errorf("dispatcher accepts action %q but the enum tag omits it", a)
		}
		delete(tagged, a)
	}
	for extra := range tagged {
		t.Errorf("enum tag advertises action %q that the dispatcher does not accept", extra)
	}

	desc := field.Tag.Get("jsonschema_description")
	for _, a := range actions {
		if !strings.Contains(desc, a) {
			t.Errorf("Action description omits accepted action %q", a)
		}
	}
}

// TestShipStateUnknownActionHintListsEveryAction pins the default case's
// recovery text to the dispatcher's case labels, so a caller who mistypes
// an action name is shown a complete list rather than a partial one.
func TestShipStateUnknownActionHintListsEveryAction(t *testing.T) {
	_, err := shipState(t.TempDir(), t.TempDir(), ShipStateIn{Action: "defered_add"}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("expected an error for an unknown action")
	}
	if got := errorClassOf(err); got != "domain" {
		t.Errorf("error class = %q, want domain", got)
	}

	hint := suggestionOf(err)
	for _, a := range shipStateDispatcherActions(t) {
		if !strings.Contains(hint, a) {
			t.Errorf("unknown-action hint omits accepted action %q: %s", a, hint)
		}
	}
}

// TestShipStateGCRejectsMistypedDryRun covers the guard that keeps a
// mistyped flag from turning a dry run into a real delete: detailBool
// reports false for any non-bool, so without the guard {"dryRun":"true"}
// would fall through and sweep the state directory.
func TestShipStateGCRejectsMistypedDryRun(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	stale := filepath.Join(dir, paths.DataDir, paths.RunsSubdir, "ship-dead-branch-20200101T000000Z.json")
	writeFile(t, stale, `{}`)
	setStateFileMtime(t, stale, 30*24*time.Hour)

	_, err := shipState(dir, dir, ShipStateIn{
		Action: "gc",
		Detail: map[string]any{"dryRun": "true"},
	}, fixedNow(time.Now()))
	if err == nil {
		t.Fatal("expected an error for a string dryRun value")
	}
	if got := errorClassOf(err); got != "domain" {
		t.Errorf("error class = %q, want domain", got)
	}
	var domainErr *mcpserver.DomainError
	if errors.As(err, &domainErr) && !strings.Contains(domainErr.Msg, "detail.dryRun") {
		t.Errorf("Msg = %q, want it to name detail.dryRun", domainErr.Msg)
	}
	if hint := suggestionOf(err); !strings.Contains(hint, "detail.dryRun") {
		t.Errorf("Suggestion = %q, want it to name detail.dryRun", hint)
	}

	if _, statErr := os.Stat(stale); statErr != nil {
		t.Errorf("stale state file was deleted despite the rejected dryRun: %v", statErr)
	}
}

// ---------------------------------------------------------------------------
// healing_record
// ---------------------------------------------------------------------------

// healingCall runs one healing_record call on branch with detail and returns
// the narration summary.
func healingCall(t *testing.T, dir, branch string, detail map[string]any) string {
	t.Helper()
	d := map[string]any{"branch": branch}
	for k, v := range detail {
		d[k] = v
	}
	out, err := shipState(dir, dir, ShipStateIn{Action: "healing_record", Detail: d},
		fixedNow(time.Date(2026, 9, 30, 1, 2, 3, 0, time.UTC)))
	if err != nil {
		t.Fatalf("healing_record %v: %v", detail, err)
	}
	n, ok := out.(ShipHealingRecordOut)
	if !ok {
		t.Fatalf("healing_record output = %T, want ShipHealingRecordOut", out)
	}
	return n.Summary
}

// TestShipStateHealingRecord_EchoesRecord pins the response fields a caller
// uses to check what was stored: kind, written, and the record itself with
// its generated recordedAt. A duplicate echoes the stored record with
// written:false. The kinds other than fix-progress are terminal: next is nil.
func TestShipStateHealingRecord_EchoesRecord(t *testing.T) {
	branch := "feat/heal-echo"
	dir, path := deferFixture(t, branch)
	at := time.Date(2026, 9, 30, 1, 2, 3, 0, time.UTC)
	call := func() ShipHealingRecordOut {
		t.Helper()
		d := healingFixedDetail(map[string]any{"branch": branch})
		out, err := shipState(dir, dir, ShipStateIn{Action: "healing_record", Detail: d}, fixedNow(at))
		if err != nil {
			t.Fatalf("healing_record: %v", err)
		}
		n, ok := out.(ShipHealingRecordOut)
		if !ok {
			t.Fatalf("healing_record output = %T, want ShipHealingRecordOut", out)
		}
		return n
	}

	first := call()
	if first.Kind != "fixed" || !first.Written {
		t.Errorf("first call kind=%q written=%v, want fixed/true", first.Kind, first.Written)
	}
	want := map[string]any{
		"origin": "local-review", "severity": "high", "file": "a.go", "line": 42,
		"title": "unchecked error", "recordedAt": "2026-09-30T01:02:03Z",
	}
	if !reflect.DeepEqual(first.Record, want) {
		t.Errorf("record = %#v, want %#v", first.Record, want)
	}
	stored, _ := healingData(t, path)["fixed"].([]any)
	if len(stored) != 1 {
		t.Fatalf("fixed has %d records, want 1", len(stored))
	}
	if s, _ := stored[0].(map[string]any); s["recordedAt"] != first.Record["recordedAt"] {
		t.Errorf("stored recordedAt = %v, echoed %v — want equal", s["recordedAt"], first.Record["recordedAt"])
	}

	at = at.Add(time.Hour)
	dup := call()
	if dup.Written {
		t.Error("duplicate call written = true, want false")
	}
	if dup.Record["title"] != "unchecked error" || dup.Record["recordedAt"] != "2026-09-30T01:02:03Z" {
		t.Errorf("duplicate record = %v, want the stored record echoed", dup.Record)
	}
	if first.Next != nil || dup.Next != nil {
		t.Errorf("fixed next = %+v / %+v, want nil", first.Next, dup.Next)
	}
	for _, d := range []map[string]any{
		{"kind": "review-total", "total": float64(3), "dimensions": float64(2)},
		healingHardenedDetail(nil),
	} {
		d["branch"] = branch
		out, err := shipState(dir, dir, ShipStateIn{Action: "healing_record", Detail: d}, fixedNow(at))
		if err != nil {
			t.Fatalf("healing_record %s: %v", d["kind"], err)
		}
		if n, _ := out.(ShipHealingRecordOut); n.Next != nil {
			t.Errorf("%s next = %+v, want nil", d["kind"], n.Next)
		}
	}
}

// healingData reads data.healing back from the state file.
func healingData(t *testing.T, path string) map[string]any {
	t.Helper()
	h, _ := readStateData(t, path)["healing"].(map[string]any)
	return h
}

func healingFixedDetail(overrides map[string]any) map[string]any {
	d := map[string]any{
		"kind": "fixed", "origin": "local-review", "severity": "high",
		"file": "a.go", "line": float64(42), "title": "unchecked error",
	}
	for k, v := range overrides {
		d[k] = v
	}
	return d
}

func healingHardenedDetail(overrides map[string]any) map[string]any {
	d := map[string]any{
		"kind": "hardened", "phase": "done", "trigger": "review cluster: unchecked errors",
		"classification": "user-code",
		"applied": []any{map[string]any{
			"surface": "plan-guardrails", "action": "add", "targetFile": "/wt/.sdlc-v2/config.toml",
		}},
		"skipped": float64(1),
	}
	for k, v := range overrides {
		d[k] = v
	}
	return d
}

func healingFixProgressDetail(overrides map[string]any) map[string]any {
	d := healingFixedDetail(map[string]any{"kind": "fix-progress", "status": "queued"})
	for k, v := range overrides {
		d[k] = v
	}
	return d
}

// fixProgressCall runs one fix-progress healing_record call on branch at the
// time at and returns the full response.
func fixProgressCall(t *testing.T, dir, branch string, at time.Time, overrides map[string]any) ShipHealingRecordOut {
	t.Helper()
	d := healingFixProgressDetail(overrides)
	d["branch"] = branch
	out, err := shipState(dir, dir, ShipStateIn{Action: "healing_record", Detail: d}, fixedNow(at))
	if err != nil {
		t.Fatalf("healing_record fix-progress %v: %v", overrides, err)
	}
	n, ok := out.(ShipHealingRecordOut)
	if !ok {
		t.Fatalf("healing_record output = %T, want ShipHealingRecordOut", out)
	}
	return n
}

// seedFixProgress sets data.healing.fixProgress in the state file to v.
func seedFixProgress(t *testing.T, path string, v any) {
	t.Helper()
	data := readStateData(t, path)
	data["healing"] = map[string]any{"fixProgress": v}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// wantFixNext checks the next field of a fix-progress response.
func wantFixNext(t *testing.T, out ShipHealingRecordOut, instruction string) {
	t.Helper()
	if out.Next == nil {
		t.Fatalf("next = nil, want id %q instruction %q", "continue-fix-pass", instruction)
	}
	if out.Next.ID != "continue-fix-pass" || out.Next.Instruction != instruction {
		t.Errorf("next = %+v, want id %q instruction %q", *out.Next, "continue-fix-pass", instruction)
	}
}

// TestShipStateHealingRecord_FixProgressTransitions walks the upsert rows of
// the fix-progress decisions table: new key, new status, same status (a new
// severity included), a stored final status against queued or fixing, and
// deferred over failed. Every row checks the echoed record against the
// record as stored, written or not.
func TestShipStateHealingRecord_FixProgressTransitions(t *testing.T) {
	t1 := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 10, 10, 10, 2, 13, 0, time.UTC)
	cases := []struct {
		name        string
		stored      string // "" = no stored record
		incoming    string
		wantStatus  string
		wantWritten bool
		wantNarr    string
		wantNext    string
	}{
		{"new key", "", "fixing", "fixing", true, ": recorded", "Status stored. Continue the fix pass."},
		{"new status", "queued", "fixing", "fixing", true, ": recorded", "Status stored. Continue the fix pass."},
		{"same status with a new severity", "fixing", "fixing", "fixing", false, ": already recorded — no change", "No change needed. Continue the fix pass."},
		{"deferred over failed", "failed", "deferred", "failed", false, ": kept failed", "The fix keeps status failed. Continue the fix pass."},
		{"failed over deferred", "deferred", "failed", "failed", true, ": recorded", "Status stored. Continue the fix pass."},
		{"queued over fixed", "fixed", "queued", "fixed", false, ": kept fixed", "The fix keeps status fixed. Continue the fix pass."},
		{"fixing over failed", "failed", "fixing", "failed", false, ": kept failed", "The fix keeps status failed. Continue the fix pass."},
		{"queued over deferred", "deferred", "queued", "deferred", false, ": kept deferred", "The fix keeps status deferred. Continue the fix pass."},
		{"failed over fixed", "fixed", "failed", "failed", true, ": recorded", "Status stored. Continue the fix pass."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			branch := "feat/heal-fix-progress"
			dir, path := deferFixture(t, branch)
			// A record with another key stays untouched in every case.
			fixProgressCall(t, dir, branch, t1, map[string]any{"title": "other finding", "status": "queued"})
			if tc.stored != "" {
				fixProgressCall(t, dir, branch, t1, map[string]any{"status": tc.stored, "severity": "low"})
			}
			out := fixProgressCall(t, dir, branch, t2, map[string]any{"status": tc.incoming})
			if !strings.HasSuffix(out.Summary, tc.wantNarr) {
				t.Errorf("summary = %q, want suffix %q", out.Summary, tc.wantNarr)
			}
			if out.Kind != "fix-progress" || out.Written != tc.wantWritten {
				t.Errorf("kind=%q written=%v, want fix-progress/%v", out.Kind, out.Written, tc.wantWritten)
			}
			wantFixNext(t, out, tc.wantNext)

			progress, _ := healingData(t, path)["fixProgress"].([]any)
			if len(progress) != 2 {
				t.Fatalf("fixProgress has %d records, want 2: %v", len(progress), progress)
			}
			other, _ := progress[0].(map[string]any)
			if other["title"] != "other finding" || other["status"] != "queued" {
				t.Errorf("unrelated record = %v, want title %q status queued", other, "other finding")
			}
			wantFirst, wantUpdated, wantSeverity := t1.Format(time.RFC3339), t2.Format(time.RFC3339), "high"
			switch {
			case tc.stored == "":
				wantFirst = t2.Format(time.RFC3339)
			case !tc.wantWritten:
				wantUpdated, wantSeverity = t1.Format(time.RFC3339), "low"
			}
			want := map[string]any{
				"origin": "local-review", "severity": wantSeverity, "file": "a.go", "line": float64(42),
				"title": "unchecked error", "status": tc.wantStatus, "firstAt": wantFirst, "updatedAt": wantUpdated,
			}
			if rec, _ := progress[1].(map[string]any); !reflect.DeepEqual(rec, want) {
				t.Errorf("stored record = %#v, want %#v", rec, want)
			}
			// The echo is the record as stored. A written record still holds
			// the Go int line it was built with; a stored one was read back
			// from JSON and holds a float64.
			echo := maps.Clone(out.Record)
			if n, ok := echo["line"].(int); ok {
				echo["line"] = float64(n)
			}
			if !reflect.DeepEqual(echo, want) {
				t.Errorf("echoed record = %#v, want the record as stored %#v", out.Record, want)
			}
		})
	}
}

// TestShipStateHealingRecord_FixProgressNoLine pins the upsert key for a
// finding with no line: an omitted line on both calls matches the stored
// null line, so the second call updates the one record.
func TestShipStateHealingRecord_FixProgressNoLine(t *testing.T) {
	branch := "feat/heal-fix-no-line"
	dir, path := deferFixture(t, branch)
	fixProgressCall(t, dir, branch, time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC), map[string]any{"line": nil, "status": "queued"})
	out := fixProgressCall(t, dir, branch, time.Date(2026, 10, 10, 10, 1, 0, 0, time.UTC), map[string]any{"line": nil, "status": "fixing"})
	if !out.Written || !strings.HasSuffix(out.Summary, ": recorded") {
		t.Errorf("second call written=%v summary=%q, want an update", out.Written, out.Summary)
	}
	progress, _ := healingData(t, path)["fixProgress"].([]any)
	if len(progress) != 1 {
		t.Fatalf("fixProgress has %d records, want 1: %v", len(progress), progress)
	}
	rec, _ := progress[0].(map[string]any)
	if line, present := rec["line"]; !present || line != nil {
		t.Errorf("line = %#v (present %v), want a stored null", line, present)
	}
	if rec["status"] != "fixing" || rec["firstAt"] != "2026-10-10T10:00:00Z" || rec["updatedAt"] != "2026-10-10T10:01:00Z" {
		t.Errorf("record = %v, want status fixing, the first firstAt and the new updatedAt", rec)
	}
}

// TestShipStateHealingRecord_FixProgressBadFirstAt pins the replace path
// when the stored record has no usable firstAt: the record takes the time of
// the call for both firstAt and updatedAt.
func TestShipStateHealingRecord_FixProgressBadFirstAt(t *testing.T) {
	for name, firstAt := range map[string]any{"absent": nil, "empty": "", "not a string": float64(7)} {
		t.Run(name, func(t *testing.T) {
			branch := "feat/heal-fix-first-at"
			dir, path := deferFixture(t, branch)
			stored := map[string]any{
				"origin": "local-review", "severity": "high", "file": "a.go", "line": float64(42),
				"title": "unchecked error", "status": "queued", "updatedAt": "2026-10-10T09:00:00Z",
			}
			if firstAt != nil {
				stored["firstAt"] = firstAt
			}
			seedFixProgress(t, path, []any{stored})
			out := fixProgressCall(t, dir, branch, time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC), map[string]any{"status": "fixing"})
			if !out.Written {
				t.Fatalf("written = false, want true (%s)", out.Summary)
			}
			progress, _ := healingData(t, path)["fixProgress"].([]any)
			rec, _ := progress[0].(map[string]any)
			if rec["firstAt"] != "2026-10-10T10:00:00Z" || rec["updatedAt"] != "2026-10-10T10:00:00Z" {
				t.Errorf("record = %v, want firstAt and updatedAt at the call time", rec)
			}
		})
	}
}

// TestShipStateHealingRecord_FixProgressCap pins the 200-record cap: a new key
// is rejected, an existing key still updates, and an entry that is not an
// object is kept as it is and counts toward the cap.
func TestShipStateHealingRecord_FixProgressCap(t *testing.T) {
	branch := "feat/heal-fix-cap"
	dir, path := deferFixture(t, branch)
	seeded := []any{"not an object"}
	for i := 1; len(seeded) < healingFixProgressMax; i++ {
		seeded = append(seeded, map[string]any{
			"origin": "local-review", "severity": "low", "file": "a.go", "line": float64(i),
			"title": "unchecked error", "status": "queued",
			"firstAt": "2026-10-10T09:00:00Z", "updatedAt": "2026-10-10T09:00:00Z",
		})
	}
	seedFixProgress(t, path, seeded)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	d := healingFixProgressDetail(map[string]any{"branch": branch, "line": float64(9999)})
	_, err = shipState(dir, dir, ShipStateIn{Action: "healing_record", Detail: d}, fixedNow(time.Now()))
	if !isDomainError(err) {
		t.Fatalf("error = %v (%T), want DomainError", err, err)
	}
	if want := "healing_record: data.healing.fixProgress holds 200 records — the cap for one run"; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if want := "Do not retry. Continue the fix pass without more fix-progress calls for this run."; suggestionOf(err) != want {
		t.Errorf("suggestion = %q, want %q", suggestionOf(err), want)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("state file changed although the call was rejected")
	}

	// An existing key at the cap still updates.
	out := fixProgressCall(t, dir, branch, time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC),
		map[string]any{"line": float64(1), "status": "fixed"})
	if !out.Written {
		t.Fatalf("update at the cap: written = false, want true (%s)", out.Summary)
	}
	progress, _ := healingData(t, path)["fixProgress"].([]any)
	if len(progress) != healingFixProgressMax {
		t.Fatalf("fixProgress has %d records, want %d", len(progress), healingFixProgressMax)
	}
	if progress[0] != "not an object" {
		t.Errorf("non-object entry = %#v, want it kept unchanged", progress[0])
	}
	if rec, _ := progress[1].(map[string]any); rec["status"] != "fixed" || rec["firstAt"] != "2026-10-10T09:00:00Z" {
		t.Errorf("updated record = %v, want status fixed and the stored firstAt", rec)
	}
}

// TestShipStateHealingRecord_FixProgressNotAList pins the DataError for a
// damaged data.healing: a list key that is not a list, or a data.healing
// that is not an object. The error names the state file and the repair, and
// nothing is written.
func TestShipStateHealingRecord_FixProgressNotAList(t *testing.T) {
	cases := []struct {
		name    string
		healing any
		detail  map[string]any
		key     string // "" = data.healing itself
		kind    string
	}{
		{"fixProgress", map[string]any{"fixProgress": "oops"}, healingFixProgressDetail(nil), "fixProgress", "fix-progress"},
		{"fixed", map[string]any{"fixed": map[string]any{}}, healingFixedDetail(nil), "fixed", "fixed"},
		{"hardened", map[string]any{"hardened": "oops"}, healingHardenedDetail(nil), "hardened", "hardened"},
		{"healing", []any{"oops"}, healingFixProgressDetail(nil), "", "fix-progress"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			branch := "feat/heal-fix-damaged"
			dir, path := deferFixture(t, branch)
			data := readStateData(t, path)
			data["healing"] = tc.healing
			raw, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			d := maps.Clone(tc.detail)
			d["branch"] = branch
			_, err = shipState(dir, dir, ShipStateIn{Action: "healing_record", Detail: d}, fixedNow(time.Now()))
			var de *mcpserver.DataError
			if !errors.As(err, &de) {
				t.Fatalf("error = %v (%T), want DataError", err, err)
			}
			field, shape, repair := "data.healing."+tc.key, "is not a list", "[]"
			if tc.key == "" {
				field, shape, repair = "data.healing", "is not an object", "{}"
			}
			file := filepath.Base(path)
			if !strings.HasPrefix(de.Msg, "healing_record: "+field+" in ") ||
				!strings.HasSuffix(de.Msg, file+" "+shape+" — the ship state is damaged") {
				t.Errorf("message = %q, want %s, the state file %s and %q", de.Msg, field, file, shape)
			}
			wantSugg := "Do not retry. Continue without more " + tc.kind + " calls in this run. Tell the user to repair the ship state file "
			wantRepair := file + ": set " + field + " to " + repair + " or remove the key."
			if !strings.HasPrefix(de.Suggestion, wantSugg) || !strings.HasSuffix(de.Suggestion, wantRepair) {
				t.Errorf("suggestion = %q, want prefix %q and suffix %q", de.Suggestion, wantSugg, wantRepair)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(raw, after) {
				t.Error("state file changed although the call failed")
			}
		})
	}
}

// TestShipStateHealingRecord_FixProgressNull pins that a stored null is not a
// list either: the call fails with the same DataError and writes nothing.
func TestShipStateHealingRecord_FixProgressNull(t *testing.T) {
	branch := "feat/heal-fix-null"
	dir, path := deferFixture(t, branch)
	seedFixProgress(t, path, nil)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d := healingFixProgressDetail(map[string]any{"branch": branch})
	_, err = shipState(dir, dir, ShipStateIn{Action: "healing_record", Detail: d}, fixedNow(time.Now()))
	var de *mcpserver.DataError
	if !errors.As(err, &de) {
		t.Fatalf("error = %v (%T), want DataError", err, err)
	}
	if de.Suggestion == "" {
		t.Error("DataError has no Suggestion")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("state file changed although the call failed")
	}
}

// TestShipStateHealingRecord_FixProgressNoLiveRun pins that fix-progress
// writes nothing without a live run and tells the fix pass to stop calling.
func TestShipStateHealingRecord_FixProgressNoLiveRun(t *testing.T) {
	const want = "No live ship run, so the status is not stored. Continue without fix-progress calls."

	t.Run("no ship state", func(t *testing.T) {
		dir := t.TempDir()
		initGitFixture(t, dir)
		gitCommit(t, dir, "initial")
		checkoutBranch(t, dir, "feat/heal-fix-none")
		out := fixProgressCall(t, dir, "feat/heal-fix-none", time.Now(), nil)
		if out.Written || !strings.HasSuffix(out.Summary, healingNarrNoLiveRun) {
			t.Errorf("written=%v summary=%q, want false and the no-live-run narration", out.Written, out.Summary)
		}
		wantFixNext(t, out, want)
		if st, err := state.Find(dir, "ship", "feat/heal-fix-none"); err != nil || st != nil {
			t.Errorf("state.Find = %v, %v — want no ship state created", st, err)
		}
	})

	t.Run("pipelineCompletedAt set", func(t *testing.T) {
		branch := "feat/heal-fix-done"
		dir, path := deferFixture(t, branch)
		data := readStateData(t, path)
		data["pipelineCompletedAt"] = "2026-10-10T00:00:00Z"
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		out := fixProgressCall(t, dir, branch, time.Now(), nil)
		if out.Written {
			t.Error("written = true, want false for a completed run")
		}
		wantFixNext(t, out, want)
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(raw, after) {
			t.Error("state file changed — a completed run must not be written")
		}
	})
}

// TestShipStateHealingRecord_FixProgressLeavesFixed pins that fix-progress
// writes change neither data.healing.fixed nor reportData.reviewLedger.fixed.
func TestShipStateHealingRecord_FixProgressLeavesFixed(t *testing.T) {
	branch := "feat/heal-fix-ledger"
	dir, path := deferFixture(t, branch)
	healingCall(t, dir, branch, map[string]any{"kind": "review-total", "total": float64(4), "dimensions": float64(2)})
	healingCall(t, dir, branch, healingFixedDetail(nil))
	fixedBefore := healingData(t, path)["fixed"]

	at := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	fixProgressCall(t, dir, branch, at, map[string]any{"status": "fixed"})
	fixProgressCall(t, dir, branch, at, map[string]any{"title": "second", "status": "fixed"})
	fixProgressCall(t, dir, branch, at, map[string]any{"title": "third", "origin": "pr-comment", "status": "fixed"})

	if after := healingData(t, path)["fixed"]; !reflect.DeepEqual(after, fixedBefore) {
		t.Errorf("healing.fixed = %v, want it unchanged: %v", after, fixedBefore)
	}
	out, err := shipState(dir, dir, ShipStateIn{Action: "read", Detail: map[string]any{"branch": branch}}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	rd, ok := out.(map[string]any)["reportData"].(ShipReportData)
	if !ok {
		t.Fatalf("reportData missing or wrong type: %#v", out)
	}
	if rd.ReviewLedger == nil || rd.ReviewLedger.Fixed != 1 {
		t.Errorf("ReviewLedger = %+v, want Fixed 1 (fix-progress records do not count)", rd.ReviewLedger)
	}
	if progress, _ := rd.Healing["fixProgress"].([]any); len(progress) != 3 {
		t.Errorf("Healing.fixProgress = %v, want the 3 records verbatim", rd.Healing["fixProgress"])
	}
}

// TestShipStateHealingRecord_FixFinalInStatuses pins that every final fix
// status is an accepted fix status.
func TestShipStateHealingRecord_FixFinalInStatuses(t *testing.T) {
	for _, s := range healingFixFinal {
		if !slices.Contains(healingFixStatuses, s) {
			t.Errorf("final status %q is not in healingFixStatuses %v", s, healingFixStatuses)
		}
	}
}

func TestShipStateHealingRecord_ReviewTotalSetsAndReplaces(t *testing.T) {
	branch := "feat/heal-total"
	dir, path := deferFixture(t, branch)

	summary := healingCall(t, dir, branch, map[string]any{"kind": "review-total", "total": float64(14), "dimensions": float64(5)})
	if !strings.Contains(summary, "recorded") {
		t.Errorf("summary = %q, want it to say recorded", summary)
	}
	rt, _ := healingData(t, path)["reviewTotal"].(map[string]any)
	if rt["total"] != float64(14) || rt["dimensions"] != float64(5) || rt["recordedAt"] != "2026-09-30T01:02:03Z" {
		t.Fatalf("reviewTotal = %v, want total 14, dimensions 5, recordedAt 2026-09-30T01:02:03Z", rt)
	}

	// A second call replaces the value; zero is a valid total.
	healingCall(t, dir, branch, map[string]any{"kind": "review-total", "total": float64(0), "dimensions": float64(3)})
	rt, _ = healingData(t, path)["reviewTotal"].(map[string]any)
	if rt["total"] != float64(0) || rt["dimensions"] != float64(3) {
		t.Errorf("reviewTotal after second call = %v, want total 0, dimensions 3", rt)
	}
}

func TestShipStateHealingRecord_FixedAppendsAndDedupes(t *testing.T) {
	branch := "feat/heal-fixed"
	dir, path := deferFixture(t, branch)

	if s := healingCall(t, dir, branch, healingFixedDetail(map[string]any{"severity": "HIGH"})); !strings.HasSuffix(s, ": recorded") {
		t.Errorf("first fixed summary = %q, want suffix %q", s, ": recorded")
	}
	// Same (origin, file, line, title) with a different severity is a duplicate.
	if s := healingCall(t, dir, branch, healingFixedDetail(map[string]any{"severity": "low"})); !strings.Contains(s, "already recorded — no change") {
		t.Errorf("duplicate fixed summary = %q, want %q", s, "already recorded — no change")
	}
	// Any change to the natural key is a new record.
	healingCall(t, dir, branch, healingFixedDetail(map[string]any{"line": float64(43)}))
	healingCall(t, dir, branch, healingFixedDetail(map[string]any{"origin": "pr-comment"}))
	healingCall(t, dir, branch, healingFixedDetail(map[string]any{"file": "b.go"}))
	healingCall(t, dir, branch, healingFixedDetail(map[string]any{"title": "other"}))
	// An absent line is its own key, and repeating it is a duplicate after the
	// JSON round trip stores it as null.
	healingCall(t, dir, branch, healingFixedDetail(map[string]any{"line": nil}))
	if s := healingCall(t, dir, branch, healingFixedDetail(map[string]any{"line": nil})); !strings.Contains(s, "already recorded — no change") {
		t.Errorf("duplicate no-line fixed summary = %q, want %q", s, "already recorded — no change")
	}

	fixed, _ := healingData(t, path)["fixed"].([]any)
	if len(fixed) != 6 {
		t.Fatalf("fixed has %d records, want 6: %v", len(fixed), fixed)
	}
	first, _ := fixed[0].(map[string]any)
	want := map[string]any{
		"origin": "local-review", "severity": "high", "file": "a.go", "line": float64(42),
		"title": "unchecked error", "recordedAt": "2026-09-30T01:02:03Z",
	}
	if !reflect.DeepEqual(first, want) {
		t.Errorf("first fixed record = %v, want %v (severity lowercased, duplicate did not overwrite it)", first, want)
	}
	last, _ := fixed[5].(map[string]any)
	if v, ok := last["line"]; !ok || v != nil {
		t.Errorf("no-line record line = %v (present %v), want stored null", v, ok)
	}
}

// TestShipStateHealingRecord_HardenedTransitions walks every row of the
// hardened upsert table: stored record for the trigger x incoming phase.
func TestShipStateHealingRecord_HardenedTransitions(t *testing.T) {
	cases := []struct {
		name      string
		stored    string // "" = no stored record
		incoming  string
		wantPhase string
		wantNarr  string
	}{
		{"none+started", "", "started", "started", ": recorded"},
		{"none+done", "", "done", "done", ": recorded"},
		{"started+done", "started", "done", "done", "replaced started record"},
		{"started+started", "started", "started", "started", "already recorded — no change"},
		{"done+started", "done", "started", "done", "already recorded — no change"},
		{"done+done", "done", "done", "done", "already recorded — no change"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			branch := "feat/heal-hardened"
			dir, path := deferFixture(t, branch)
			// An unrelated trigger stays untouched in every case.
			healingCall(t, dir, branch, healingHardenedDetail(map[string]any{"trigger": "other trigger", "phase": "started"}))
			if tc.stored != "" {
				healingCall(t, dir, branch, healingHardenedDetail(map[string]any{
					"phase": tc.stored, "applied": []any{}, "skipped": float64(0),
				}))
			}
			summary := healingCall(t, dir, branch, healingHardenedDetail(map[string]any{"phase": tc.incoming}))
			if !strings.HasSuffix(summary, tc.wantNarr) {
				t.Errorf("summary = %q, want suffix %q", summary, tc.wantNarr)
			}

			hardened, _ := healingData(t, path)["hardened"].([]any)
			if len(hardened) != 2 {
				t.Fatalf("hardened has %d records, want 2 (one per trigger): %v", len(hardened), hardened)
			}
			other, _ := hardened[0].(map[string]any)
			if other["trigger"] != "other trigger" || other["phase"] != "started" {
				t.Errorf("unrelated record = %v, want trigger %q phase started", other, "other trigger")
			}
			rec, _ := hardened[1].(map[string]any)
			if rec["phase"] != tc.wantPhase {
				t.Errorf("stored phase = %v, want %s", rec["phase"], tc.wantPhase)
			}
			// Which record survived: the stored one (applied empty, skipped 0)
			// unless the incoming one was appended or replaced it.
			incomingKept := tc.stored == "" || tc.wantNarr == "replaced started record"
			applied, _ := rec["applied"].([]any)
			if incomingKept && (len(applied) != 1 || rec["skipped"] != float64(1)) {
				t.Errorf("record = %v, want the incoming record (1 applied, skipped 1)", rec)
			}
			if !incomingKept && (len(applied) != 0 || rec["skipped"] != float64(0)) {
				t.Errorf("record = %v, want the stored record unchanged (0 applied, skipped 0)", rec)
			}
		})
	}
}

// TestShipStateHealingRecord_EmptyAppliedRecorded pins that a harden run that
// changed nothing is still recorded.
func TestShipStateHealingRecord_EmptyAppliedRecorded(t *testing.T) {
	branch := "feat/heal-empty-applied"
	dir, path := deferFixture(t, branch)
	healingCall(t, dir, branch, healingHardenedDetail(map[string]any{"applied": []any{}, "skipped": float64(0)}))
	hardened, _ := healingData(t, path)["hardened"].([]any)
	if len(hardened) != 1 {
		t.Fatalf("hardened = %v, want one record", hardened)
	}
	rec, _ := hardened[0].(map[string]any)
	if applied, ok := rec["applied"].([]any); !ok || len(applied) != 0 {
		t.Errorf("applied = %#v, want an empty array", rec["applied"])
	}
}

func TestShipStateHealingRecord_Rejections(t *testing.T) {
	surfaceIDs := healingSurfaceIDs()
	cases := []struct {
		name      string
		detail    map[string]any
		msgWants  []string
		suggWants []string
	}{
		{"missing kind", map[string]any{}, []string{"detail.kind"}, healingKinds},
		{"unknown kind", map[string]any{"kind": "patched"}, []string{"patched"}, healingKinds},
		{"wrong-typed kind", map[string]any{"kind": float64(1)}, []string{"detail.kind"}, healingKinds},

		{"review-total missing total", map[string]any{"kind": "review-total", "dimensions": float64(1)}, []string{"detail.total"}, nil},
		{"review-total missing dimensions", map[string]any{"kind": "review-total", "total": float64(1)}, []string{"detail.dimensions"}, nil},
		{"review-total negative total", map[string]any{"kind": "review-total", "total": float64(-1), "dimensions": float64(1)}, []string{"detail.total"}, nil},
		{"review-total fractional total", map[string]any{"kind": "review-total", "total": 1.5, "dimensions": float64(1)}, []string{"detail.total"}, nil},
		{"review-total string dimensions", map[string]any{"kind": "review-total", "total": float64(1), "dimensions": "5"}, []string{"detail.dimensions"}, nil},

		{"fixed missing origin", healingFixedDetail(map[string]any{"origin": nil}), []string{"detail.origin"}, nil},
		{"fixed missing severity", healingFixedDetail(map[string]any{"severity": nil}), []string{"detail.severity"}, nil},
		{"fixed missing file", healingFixedDetail(map[string]any{"file": nil}), []string{"detail.file"}, nil},
		{"fixed empty title", healingFixedDetail(map[string]any{"title": ""}), []string{"detail.title"}, nil},
		{"fixed unknown origin", healingFixedDetail(map[string]any{"origin": "ci"}), []string{"ci"}, healingOrigins},
		{"fixed unknown severity", healingFixedDetail(map[string]any{"severity": "blocker"}), []string{"blocker"}, dimensions.ValidSeverities},
		{"fixed non-integer line", healingFixedDetail(map[string]any{"line": "42"}), []string{"detail.line"}, nil},

		{"hardened missing phase", healingHardenedDetail(map[string]any{"phase": nil}), []string{"detail.phase"}, nil},
		{"hardened unknown phase", healingHardenedDetail(map[string]any{"phase": "finished"}), []string{"finished"}, healingPhases},
		{"hardened missing trigger", healingHardenedDetail(map[string]any{"trigger": nil}), []string{"detail.trigger"}, nil},
		{"hardened missing classification", healingHardenedDetail(map[string]any{"classification": nil}), []string{"detail.classification"}, nil},
		{"hardened missing applied", healingHardenedDetail(map[string]any{"applied": nil}), []string{"detail.applied"}, nil},
		{"hardened applied not array", healingHardenedDetail(map[string]any{"applied": "none"}), []string{"detail.applied"}, nil},
		{"hardened applied entry not object", healingHardenedDetail(map[string]any{"applied": []any{"x"}}), []string{"detail.applied[0]"}, nil},
		{
			"hardened applied missing targetFile",
			healingHardenedDetail(map[string]any{"applied": []any{map[string]any{"surface": "plan-guardrails", "action": "add"}}}),
			[]string{"detail.applied[0].targetFile"}, nil,
		},
		{
			"hardened unknown surface",
			healingHardenedDetail(map[string]any{"applied": []any{map[string]any{"surface": "readme", "action": "add", "targetFile": "/x"}}}),
			[]string{"readme"}, surfaceIDs,
		},
		{"hardened missing skipped", healingHardenedDetail(map[string]any{"skipped": nil}), []string{"detail.skipped"}, nil},
		{"hardened negative skipped", healingHardenedDetail(map[string]any{"skipped": float64(-2)}), []string{"detail.skipped"}, nil},

		{"fixed line 0", healingFixedDetail(map[string]any{"line": float64(0)}), []string{"detail.line must be >= 1, got 0"}, []string{"omit it"}},
		{"fixed negative line", healingFixedDetail(map[string]any{"line": float64(-3)}), []string{"detail.line must be >= 1, got -3"}, nil},

		{"fix-progress missing status", healingFixProgressDetail(map[string]any{"status": nil}),
			[]string{`detail.status is required for kind "fix-progress"`}, healingFixStatuses},
		{"fix-progress unknown status", healingFixProgressDetail(map[string]any{"status": "done"}),
			[]string{`detail.status "done" is not a recognised fix status`}, healingFixStatuses},
		{"fix-progress wrong-typed status", healingFixProgressDetail(map[string]any{"status": float64(1)}),
			[]string{"detail.status 1 is not a recognised fix status"}, healingFixStatuses},
		{"fix-progress line 0", healingFixProgressDetail(map[string]any{"line": float64(0)}), []string{"detail.line must be >= 1, got 0"}, nil},
		{"fix-progress missing origin", healingFixProgressDetail(map[string]any{"origin": nil}), []string{"detail.origin", `"fix-progress"`}, nil},
		{"fix-progress unknown severity", healingFixProgressDetail(map[string]any{"severity": "blocker"}), []string{"blocker"}, dimensions.ValidSeverities},
		{"fix-progress missing file", healingFixProgressDetail(map[string]any{"file": nil}), []string{"detail.file"}, nil},
		{"fix-progress empty title", healingFixProgressDetail(map[string]any{"title": ""}), []string{"detail.title"}, nil},
	}

	branch := "feat/heal-reject"
	dir, path := deferFixture(t, branch)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := map[string]any{"branch": branch}
			for k, v := range tc.detail {
				if v == nil {
					continue // nil in the table means "omit the key"
				}
				d[k] = v
			}
			_, err := shipState(dir, dir, ShipStateIn{Action: "healing_record", Detail: d}, fixedNow(time.Now()))
			if err == nil {
				t.Fatal("want DomainError, got nil")
			}
			if !isDomainError(err) {
				t.Fatalf("error = %v (%T), want DomainError", err, err)
			}
			msg := err.Error()
			if !strings.HasPrefix(msg, "healing_record: ") {
				t.Errorf("message %q does not start with the action name", msg)
			}
			for _, w := range tc.msgWants {
				if !strings.Contains(msg, w) {
					t.Errorf("message %q does not name %q", msg, w)
				}
			}
			sugg := suggestionOf(err)
			for _, w := range tc.suggWants {
				if !strings.Contains(sugg, w) {
					t.Errorf("suggestion %q does not list accepted value %q", sugg, w)
				}
			}
		})
	}
	if h, ok := readStateData(t, path)["healing"]; ok {
		t.Errorf("data.healing = %v, want absent — rejected calls must not write", h)
	}
}

func TestShipStateHealingRecord_NoLiveRun(t *testing.T) {
	t.Run("no ship state", func(t *testing.T) {
		dir := t.TempDir()
		initGitFixture(t, dir)
		gitCommit(t, dir, "initial")
		checkoutBranch(t, dir, "feat/heal-none")
		for _, detail := range []map[string]any{
			{"kind": "review-total", "total": float64(3), "dimensions": float64(2)},
			healingFixedDetail(nil),
			healingHardenedDetail(nil),
			healingFixProgressDetail(nil),
		} {
			s := healingCall(t, dir, "feat/heal-none", detail)
			if !strings.Contains(s, "no live ship run on this branch — healing not recorded") {
				t.Errorf("summary = %q, want the no-live-run narration", s)
			}
		}
		if st, err := state.Find(dir, "ship", "feat/heal-none"); err != nil || st != nil {
			t.Errorf("state.Find = %v, %v — want no ship state created", st, err)
		}
	})

	t.Run("pipelineCompletedAt set", func(t *testing.T) {
		branch := "feat/heal-done"
		dir, path := deferFixture(t, branch)
		data := readStateData(t, path)
		data["pipelineCompletedAt"] = "2026-09-30T00:00:00Z"
		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		s := healingCall(t, dir, branch, healingFixedDetail(nil))
		if !strings.Contains(s, "no live ship run on this branch — healing not recorded") {
			t.Errorf("summary = %q, want the no-live-run narration", s)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Error("state file changed — a completed run must not be written")
		}
	})
}

// TestShipStateHealingRecord_SchemaAcceptsWrittenRecords validates the
// data.healing the handler actually wrote against ship-state.schema.json,
// and checks that the schema's enums match the Go sets the handler checks.
func TestShipStateHealingRecord_SchemaAcceptsWrittenRecords(t *testing.T) {
	branch := "feat/heal-schema"
	dir, path := deferFixture(t, branch)
	healingCall(t, dir, branch, map[string]any{"kind": "review-total", "total": float64(4), "dimensions": float64(2)})
	healingCall(t, dir, branch, healingFixedDetail(nil))
	healingCall(t, dir, branch, healingFixedDetail(map[string]any{"line": nil, "origin": "pr-comment"}))
	healingCall(t, dir, branch, healingHardenedDetail(map[string]any{"phase": "started", "applied": []any{}, "skipped": float64(0)}))
	var applied []any
	for _, id := range healingSurfaceIDs() {
		applied = append(applied, map[string]any{"surface": id, "action": "add", "targetFile": "/x"})
	}
	healingCall(t, dir, branch, healingHardenedDetail(map[string]any{"trigger": "every surface", "applied": applied}))
	healingCall(t, dir, branch, healingFixProgressDetail(nil))
	healingCall(t, dir, branch, healingFixProgressDetail(map[string]any{"status": "fixing"}))
	healingCall(t, dir, branch, healingFixProgressDetail(map[string]any{"line": nil, "origin": "pr-comment", "status": "deferred"}))
	healing := healingData(t, path)
	if progress, _ := healing["fixProgress"].([]any); len(progress) != 2 {
		t.Fatalf("fixProgress = %v, want 2 records", healing["fixProgress"])
	}

	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc", "schemas", "ship-state.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	sch, err := jsonschema.NewCompiler().Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	validate := func(h any) error {
		raw, err := json.Marshal(map[string]any{
			"version": float64(1), "startedAt": "2026-03-01T12:00:00Z", "branch": branch,
			"flags": map[string]any{}, "steps": []any{map[string]any{"name": "review", "status": "completed"}},
			"healing": h,
		})
		if err != nil {
			t.Fatalf("marshal doc: %v", err)
		}
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
		if err != nil {
			t.Fatalf("unmarshal doc: %v", err)
		}
		return sch.Validate(inst)
	}
	if err := validate(healing); err != nil {
		t.Errorf("schema rejected data.healing written by healing_record: %v", err)
	}
	bad := map[string]any{"fixed": []any{map[string]any{
		"origin": "ci", "severity": "high", "file": "a.go", "title": "t", "recordedAt": "2026-09-30T01:02:03Z",
	}}}
	if err := validate(bad); err == nil {
		t.Error(`origin "ci": want schema rejection, got nil`)
	}
	goodFix := func() map[string]any {
		return map[string]any{
			"origin": "local-review", "severity": "high", "file": "a.go", "line": float64(4), "title": "t",
			"status": "queued", "firstAt": "2026-10-10T10:00:00Z", "updatedAt": "2026-10-10T10:00:00Z",
		}
	}
	if err := validate(map[string]any{"fixProgress": []any{goodFix()}}); err != nil {
		t.Fatalf("schema rejected a valid fixProgress record: %v", err)
	}
	for name, edit := range map[string]func(map[string]any){
		"unknown status":  func(m map[string]any) { m["status"] = "done" },
		"missing firstAt": func(m map[string]any) { delete(m, "firstAt") },
		"missing status":  func(m map[string]any) { delete(m, "status") },
		"extra field":     func(m map[string]any) { m["recordedAt"] = "2026-10-10T10:00:00Z" },
	} {
		rec := goodFix()
		edit(rec)
		if err := validate(map[string]any{"fixProgress": []any{rec}}); err == nil {
			t.Errorf("fixProgress %s: want schema rejection, got nil", name)
		}
	}

	// Enum sync: the schema's enums must equal the Go sets.
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties struct {
			Healing struct {
				Properties struct {
					Fixed struct {
						Items struct {
							Properties struct {
								Origin   struct{ Enum []string } `json:"origin"`
								Severity struct{ Enum []string } `json:"severity"`
							} `json:"properties"`
						} `json:"items"`
					} `json:"fixed"`
					FixProgress struct {
						MaxItems int `json:"maxItems"`
						Items    struct {
							Properties struct {
								Origin   struct{ Enum []string } `json:"origin"`
								Severity struct{ Enum []string } `json:"severity"`
								Status   struct{ Enum []string } `json:"status"`
							} `json:"properties"`
						} `json:"items"`
					} `json:"fixProgress"`
					Hardened struct {
						Items struct {
							Properties struct {
								Phase   struct{ Enum []string } `json:"phase"`
								Applied struct {
									Items struct {
										Properties struct {
											Surface struct{ Enum []string } `json:"surface"`
										} `json:"properties"`
									} `json:"items"`
								} `json:"applied"`
							} `json:"properties"`
						} `json:"items"`
					} `json:"hardened"`
				} `json:"properties"`
			} `json:"healing"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	fp := doc.Properties.Healing.Properties.Fixed.Items.Properties
	xp := doc.Properties.Healing.Properties.FixProgress.Items.Properties
	hp := doc.Properties.Healing.Properties.Hardened.Items.Properties
	if got := doc.Properties.Healing.Properties.FixProgress.MaxItems; got != healingFixProgressMax {
		t.Errorf("schema fixProgress.maxItems = %d, want %d (healingFixProgressMax)", got, healingFixProgressMax)
	}
	for name, pair := range map[string][2][]string{
		"fixed.origin":         {fp.Origin.Enum, healingOrigins},
		"fixed.severity":       {fp.Severity.Enum, dimensions.ValidSeverities},
		"fixProgress.origin":   {xp.Origin.Enum, healingOrigins},
		"fixProgress.severity": {xp.Severity.Enum, dimensions.ValidSeverities},
		"fixProgress.status":   {xp.Status.Enum, healingFixStatuses},
		"hardened.phase":       {hp.Phase.Enum, healingPhases},
		"applied.surface":      {hp.Applied.Items.Properties.Surface.Enum, healingSurfaceIDs()},
	} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("schema enum %s = %v, want %v (the Go set the handler checks)", name, pair[0], pair[1])
		}
	}
}

// TestShipStateHealingRecord_ReadReportsLedger drives the whole path: healing
// records and deferrals written through the tool, then read's reportData.
func TestShipStateHealingRecord_ReadReportsLedger(t *testing.T) {
	branch := "feat/heal-read"
	dir, _ := deferFixture(t, branch)
	useMemHistory(t)

	read := func() ShipReportData {
		t.Helper()
		out, err := shipState(dir, dir, ShipStateIn{Action: "read", Detail: map[string]any{"branch": branch}}, fixedNow(time.Now()))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		rd, ok := out.(map[string]any)["reportData"].(ShipReportData)
		if !ok {
			t.Fatalf("reportData missing or wrong type: %#v", out)
		}
		return rd
	}

	rd := read()
	if rd.ReviewLedger != nil || rd.ReviewLedgerNote != "review did not run or its total was not recorded" {
		t.Errorf("before review-total: ledger = %v, note = %q; want nil and the not-recorded note", rd.ReviewLedger, rd.ReviewLedgerNote)
	}
	if rd.Healing == nil || len(rd.Healing) != 0 {
		t.Errorf("Healing = %#v, want an empty non-nil map", rd.Healing)
	}

	healingCall(t, dir, branch, map[string]any{"kind": "review-total", "total": float64(5), "dimensions": float64(3)})
	healingCall(t, dir, branch, healingFixedDetail(nil))
	healingCall(t, dir, branch, healingFixedDetail(map[string]any{"origin": "pr-comment"}))
	if _, err := shipState(dir, dir, ShipStateIn{Action: "defer", Detail: map[string]any{
		"branch": branch, "severity": "low", "file": "c.go", "title": "later", "reason": "needs-direction",
	}}, fixedNow(time.Now())); err != nil {
		t.Fatalf("defer: %v", err)
	}

	rd = read()
	want := &ShipReviewLedger{Total: 5, Fixed: 1, DeferredByReason: map[string]int{"needs-direction": 1}, Unaccounted: 3}
	if !reflect.DeepEqual(rd.ReviewLedger, want) {
		t.Errorf("ReviewLedger = %+v, want %+v", rd.ReviewLedger, want)
	}
	if rd.ReviewLedgerNote != "" {
		t.Errorf("ReviewLedgerNote = %q, want empty once a total is recorded", rd.ReviewLedgerNote)
	}
	if fixed, _ := rd.Healing["fixed"].([]any); len(fixed) != 2 {
		t.Errorf("Healing.fixed = %v, want both records verbatim", rd.Healing["fixed"])
	}
}

// ---------------------------------------------------------------------------
// duplicate step names (hand-built states only: ship_prepare never repeats a name)
// ---------------------------------------------------------------------------

// setSteps replaces data["steps"] in the state file with one pending entry
// per name, in order.
func setSteps(t *testing.T, path string, names ...string) {
	t.Helper()
	data := readStateData(t, path)
	steps := make([]any, 0, len(names))
	for _, n := range names {
		steps = append(steps, map[string]any{"name": n, "kind": "tracked", "status": "pending"})
	}
	data["steps"] = steps
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// stepStatuses returns "name:status" for each steps[] entry, in order.
func stepStatuses(t *testing.T, path string) []string {
	t.Helper()
	var out []string
	for _, s := range readStateData(t, path)["steps"].([]any) {
		sm := s.(map[string]any)
		out = append(out, fmt.Sprintf("%v:%v", sm["name"], sm["status"]))
	}
	return out
}

// TestShipState_DuplicateStepName_ActionsTargetFirstUnfinishedEntry drives
// a pipeline with commit twice through begin-step, complete-step, skip and
// fail, and checks that each action changes only the first commit entry that
// is not completed or skipped, and that the summary names its position.
func TestShipState_DuplicateStepName_ActionsTargetFirstUnfinishedEntry(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	branch := "feat/dup-step"
	checkoutBranch(t, dir, branch)
	path := shipStateInitFixture(t, dir, branch)
	setSteps(t, path, "commit", "harden", "commit", "pr")

	call := func(action, step string, detail map[string]any) ShipStepNarrationOut {
		t.Helper()
		d := map[string]any{"branch": branch, "detail": "concise"}
		for k, v := range detail {
			d[k] = v
		}
		out, err := shipState(dir, dir, ShipStateIn{Action: action, Step: step, Detail: d}, fixedNow(time.Now()))
		if err != nil {
			t.Fatalf("%s %s: %v", action, step, err)
		}
		return out.(ShipStepNarrationOut)
	}

	call("begin-step", "commit", nil)
	if out := call("complete-step", "commit", map[string]any{"result": "first"}); !strings.Contains(out.Summary, "(1 of 4)") {
		t.Errorf("complete-step summary = %q, want position (1 of 4)", out.Summary)
	}
	call("begin-step", "harden", nil)
	call("complete-step", "harden", nil)

	out := call("skip", "commit", map[string]any{"reason": "nothing to commit"})
	if !strings.Contains(out.Summary, "(3 of 4)") {
		t.Errorf("skip summary = %q, want position (3 of 4)", out.Summary)
	}
	want := []string{"commit:completed", "harden:completed", "commit:skipped", "pr:pending"}
	if got := stepStatuses(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("steps after skip = %v, want %v", got, want)
	}
	first := findStepMap(t, readStateData(t, path), "commit")
	if first["result"] != "first" {
		t.Errorf("first commit result = %v, want %q (skip must not touch it)", first["result"], "first")
	}
	if _, has := first["reason"]; has {
		t.Errorf("first commit has a skip reason: %v", first)
	}
}

// TestShipState_DuplicateStepName_FailThenRetryTargetsSameEntry checks that
// fail and the following begin-step act on the first commit while it is
// failed, not on the later pending commit.
func TestShipState_DuplicateStepName_FailThenRetryTargetsSameEntry(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	branch := "feat/dup-step-fail"
	checkoutBranch(t, dir, branch)
	path := shipStateInitFixture(t, dir, branch)
	setSteps(t, path, "commit", "harden", "commit")

	for _, action := range []string{"begin-step", "fail", "begin-step"} {
		if _, err := shipState(dir, dir, ShipStateIn{
			Action: action, Step: "commit",
			Detail: map[string]any{"branch": branch, "error": "boom", "detail": "concise"},
		}, fixedNow(time.Now())); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	want := []string{"commit:in_progress", "harden:pending", "commit:pending"}
	if got := stepStatuses(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
}

// TestShipState_DuplicateStepName_AlreadyDonePerOccurrence checks that a
// journal entry for the first commit does not mark the second commit as
// already done, and that a "commit#2" entry does.
func TestShipState_DuplicateStepName_AlreadyDonePerOccurrence(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	branch := "feat/dup-step-journal"
	checkoutBranch(t, dir, branch)
	path := shipStateInitFixture(t, dir, branch)
	setSteps(t, path, "commit", "commit")
	setStepStatus(t, path, "commit", "completed", map[string]any{"completedAt": "2026-01-01T00:00:00Z"})
	// setStepStatus changes every entry with the name; reset the second one.
	data := readStateData(t, path)
	data["steps"].([]any)[1].(map[string]any)["status"] = "pending"
	raw, _ := json.Marshal(data)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	setSideEffectEntry(t, path, "commit", "sha", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

	begin := func() bool {
		t.Helper()
		out, err := shipState(dir, dir, ShipStateIn{
			Action: "begin-step", Step: "commit", Detail: map[string]any{"branch": branch},
		}, fixedNow(time.Now()))
		if err != nil {
			t.Fatalf("begin-step: %v", err)
		}
		return out.(ShipStepNarrationOut).AlreadyDone
	}
	if begin() {
		t.Error(`AlreadyDone = true for the second commit, want false (only sideEffects["commit"] exists)`)
	}
	setSideEffectEntry(t, path, "commit#2", "sha", "feedfacefeedfacefeedfacefeedfacefeedface")
	if !begin() {
		t.Error(`AlreadyDone = false for the second commit, want true (sideEffects["commit#2"] exists)`)
	}
}

// ---------------------------------------------------------------------------
// commit-check
// ---------------------------------------------------------------------------

// commitCheckFixture builds a git repository on branch with one commit, a
// ship state whose commit step is in_progress, and returns the repository
// directory and the ship state file path. The ship state lives under the
// data directory inside the repository, so every commit-check call also
// proves that the data directory is not staged.
func commitCheckFixture(t *testing.T, branch string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, branch)
	path := shipStateInitFixture(t, dir, branch)
	setStepStatus(t, path, "execute", "completed", map[string]any{"completedAt": "2026-01-01T00:00:00Z"})
	setStepStatus(t, path, "commit", "in_progress", map[string]any{"startedAt": "2026-01-01T00:00:00Z"})
	return dir, path
}

// runCommitCheck calls the commit-check action through the dispatcher with
// root and workDir both set to dir.
func runCommitCheck(t *testing.T, dir, branch string) (ShipCommitCheckOut, error) {
	t.Helper()
	out, err := shipState(dir, dir, ShipStateIn{
		Action: "commit-check",
		Detail: map[string]any{"branch": branch},
	}, fixedNow(time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)))
	if err != nil {
		return ShipCommitCheckOut{}, err
	}
	res, ok := out.(ShipCommitCheckOut)
	if !ok {
		t.Fatalf("commit-check output = %T, want ShipCommitCheckOut", out)
	}
	return res, nil
}

// gitOut runs git in dir and returns its trimmed stdout.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := execRun(dir, "git", args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(out)
}

// commitStepEntry returns the commit step entry of the state file at path.
func commitStepEntry(t *testing.T, path string) map[string]any {
	t.Helper()
	for _, s := range readStateData(t, path)["steps"].([]any) {
		if sm, ok := s.(map[string]any); ok && sm["name"] == "commit" {
			return sm
		}
	}
	t.Fatalf("no commit step in %s", path)
	return nil
}

// writeExecuteWaves writes an execute state for branch under root whose
// waves list is waves.
func writeExecuteWaves(t *testing.T, root, branch string, waves []any) {
	t.Helper()
	st, err := state.Init(root, "execute", branch, "sess-exec")
	if err != nil {
		t.Fatalf("init execute state: %v", err)
	}
	st.Data["waves"] = waves
	if err := state.Write(st); err != nil {
		t.Fatalf("write execute state: %v", err)
	}
}

// TestShipState_CommitCheck_DirtyStagesAndKeepsStep checks that a dirty tree is staged without the data directory, stores commitBaseHead, and leaves the commit step in_progress.
func TestShipState_CommitCheck_DirtyStagesAndKeepsStep(t *testing.T) {
	branch := "feat/cc-dirty"
	dir, path := commitCheckFixture(t, branch)
	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	writeFile(t, filepath.Join(dir, "b.txt"), "b")
	head := gitOut(t, dir, "rev-parse", "HEAD")

	out, err := runCommitCheck(t, dir, branch)
	if err != nil {
		t.Fatalf("commit-check: %v", err)
	}
	if out.Clean || out.StagedCount != 2 || out.StepCompleted || out.Result != "" {
		t.Errorf("out = %+v, want clean=false stagedCount=2 stepCompleted=false result=\"\"", out)
	}
	if out.Next != "Changes are staged. Dispatch the commit agent, then 7c2, then complete-step." {
		t.Errorf("next = %q", out.Next)
	}
	staged := gitOut(t, dir, "diff", "--cached", "--name-only")
	if staged != "a.txt\nb.txt" {
		t.Errorf("staged = %q, want a.txt and b.txt only (no %s path)", staged, paths.DataDir)
	}
	data := readStateData(t, path)
	if data["commitBaseHead"] != head {
		t.Errorf("commitBaseHead = %v, want %s", data["commitBaseHead"], head)
	}
	if got := commitStepEntry(t, path)["status"]; got != "in_progress" {
		t.Errorf("commit status = %v, want in_progress", got)
	}
}

// TestShipState_CommitCheck_CleanCountsWaveCommits checks that a clean tree at commitBaseHead counts committed waves, records one decision, and completes the commit step.
func TestShipState_CommitCheck_CleanCountsWaveCommits(t *testing.T) {
	branch := "feat/cc-waves"
	dir, path := commitCheckFixture(t, branch)
	writeExecuteWaves(t, dir, branch, []any{
		map[string]any{"number": 1, "committedSha": "aaaaaaa"},
		map[string]any{"number": 2, "committedSha": ""},
		map[string]any{"number": 3, "committedSha": "bbbbbbb"},
		"not a wave object",
	})

	out, err := runCommitCheck(t, dir, branch)
	if err != nil {
		t.Fatalf("commit-check: %v", err)
	}
	wantResult := "nothing to commit: execute committed 2 wave commit(s)"
	if !out.Clean || out.StagedCount != 0 || out.WaveCommits != 2 || !out.StepCompleted || out.Result != wantResult {
		t.Errorf("out = %+v, want clean, waveCommits=2, stepCompleted, result %q", out, wantResult)
	}
	if out.Next != "Commit step completed. Skip 7c2 and 7d. Go to the next step." {
		t.Errorf("next = %q", out.Next)
	}
	if len(out.Todos) == 0 || out.Display == "" {
		t.Errorf("todos/display missing: todos=%d display=%q", len(out.Todos), out.Display)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", out.Warnings)
	}
	step := commitStepEntry(t, path)
	if step["status"] != "completed" || step["result"] != wantResult {
		t.Errorf("commit step = %v, want completed with result %q", step, wantResult)
	}
	if step["completedAt"] != "2026-01-01T00:05:00Z" {
		t.Errorf("commit step completedAt = %v, want the call time 2026-01-01T00:05:00Z", step["completedAt"])
	}
	data := readStateData(t, path)
	decisions, _ := data["decisions"].([]any)
	if len(decisions) != 1 {
		t.Fatalf("decisions = %v, want 1 entry", decisions)
	}
	d := decisions[0].(map[string]any)
	if d["step"] != "commit" || d["decision"] != wantResult {
		t.Errorf("decision = %v, want step commit and decision %q", d, wantResult)
	}
	if _, ok := data["sideEffects"]; ok {
		t.Errorf("sideEffects = %v, want absent for a nothing-to-commit step", data["sideEffects"])
	}
	if data["commitBaseHead"] != gitOut(t, dir, "rev-parse", "HEAD") {
		t.Errorf("commitBaseHead = %v, want HEAD", data["commitBaseHead"])
	}
}

// TestShipCommitCheckOut_DisplayIsRaw checks that the display field is tagged
// to render verbatim, so the multi-line progress block is not code-fenced.
func TestShipCommitCheckOut_DisplayIsRaw(t *testing.T) {
	field, ok := reflect.TypeOf(ShipCommitCheckOut{}).FieldByName("Display")
	if !ok {
		t.Fatal("ShipCommitCheckOut has no Display field")
	}
	if got := field.Tag.Get("render"); got != "raw" {
		t.Errorf(`Display render tag = %q, want "raw"`, got)
	}
}

// TestShipState_CommitCheck_CleanNoExecuteState checks the clean-tree result when the branch has no execute state.
func TestShipState_CommitCheck_CleanNoExecuteState(t *testing.T) {
	branch := "feat/cc-clean"
	dir, path := commitCheckFixture(t, branch)

	out, err := runCommitCheck(t, dir, branch)
	if err != nil {
		t.Fatalf("commit-check: %v", err)
	}
	want := "nothing to commit: the working tree is clean"
	if out.WaveCommits != 0 || out.Result != want || !out.StepCompleted {
		t.Errorf("out = %+v, want waveCommits=0 result %q", out, want)
	}
	if got := commitStepEntry(t, path)["result"]; got != want {
		t.Errorf("stored result = %v, want %q", got, want)
	}
	if !strings.HasPrefix(out.Result, commitNothingPrefix) {
		t.Errorf("result %q does not start with commitNothingPrefix", out.Result)
	}
}

// TestShipState_CommitCheck_UnreadableExecuteStateWarns checks that an unreadable execute state gives 0 wave commits and a warning.
func TestShipState_CommitCheck_UnreadableExecuteStateWarns(t *testing.T) {
	branch := "feat/cc-badexec"
	dir, _ := commitCheckFixture(t, branch)
	bad := filepath.Join(dir, paths.DataDir, paths.RunsSubdir,
		fmt.Sprintf("execute-%s-20260101T000000Z.json", state.SlugifyBranch(branch)))
	writeFile(t, bad, "{not json")

	out, err := runCommitCheck(t, dir, branch)
	if err != nil {
		t.Fatalf("commit-check: %v", err)
	}
	if out.WaveCommits != 0 || out.Result != "nothing to commit: the working tree is clean" {
		t.Errorf("out = %+v, want waveCommits=0 and the clean-tree result", out)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "execute state not read") {
		t.Errorf("warnings = %v, want one execute-state warning", out.Warnings)
	}
}

// TestShipState_CommitCheck_LandedCommitAfterInterrupt checks that later calls keep commitBaseHead and that a clean tree past it journals HEAD and completes the step.
func TestShipState_CommitCheck_LandedCommitAfterInterrupt(t *testing.T) {
	branch := "feat/cc-landed"
	dir, path := commitCheckFixture(t, branch)
	base := gitOut(t, dir, "rev-parse", "HEAD")

	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	if _, err := runCommitCheck(t, dir, branch); err != nil {
		t.Fatalf("first commit-check: %v", err)
	}
	mustRun(t, dir, "git", "commit", "-m", "first")

	// A later dirty call keeps the stored base.
	writeFile(t, filepath.Join(dir, "b.txt"), "b")
	out, err := runCommitCheck(t, dir, branch)
	if err != nil {
		t.Fatalf("second commit-check: %v", err)
	}
	if out.Clean || out.StagedCount != 1 {
		t.Errorf("second out = %+v, want dirty with 1 staged path", out)
	}
	if got := readStateData(t, path)["commitBaseHead"]; got != base {
		t.Errorf("commitBaseHead after second call = %v, want unchanged %s", got, base)
	}
	mustRun(t, dir, "git", "commit", "-m", "second")
	head := gitOut(t, dir, "rev-parse", "HEAD")

	out, err = runCommitCheck(t, dir, branch)
	if err != nil {
		t.Fatalf("third commit-check: %v", err)
	}
	want := "committed " + head[:7]
	if !out.Clean || !out.StepCompleted || out.Result != want {
		t.Errorf("third out = %+v, want clean, completed, result %q", out, want)
	}
	if out.Next != "Commit step completed from the landed commit. Skip 7c2 and 7d. Go to the next step." {
		t.Errorf("next = %q", out.Next)
	}
	data := readStateData(t, path)
	if data["commitBaseHead"] != base {
		t.Errorf("commitBaseHead = %v, want unchanged %s", data["commitBaseHead"], base)
	}
	entry, ok := data["sideEffects"].(map[string]any)["commit"].(map[string]any)
	if !ok || entry["kind"] != "sha" || entry["ref"] != head {
		t.Errorf("sideEffects.commit = %v, want kind sha ref %s", data["sideEffects"], head)
	}
	if decisions, _ := data["decisions"].([]any); len(decisions) != 0 {
		t.Errorf("decisions = %v, want none for a landed commit", decisions)
	}
	if step := commitStepEntry(t, path); step["status"] != "completed" || step["result"] != want || step["completedAt"] != "2026-01-01T00:05:00Z" {
		t.Errorf("commit step = %v, want completed with result %q and completedAt 2026-01-01T00:05:00Z", step, want)
	}
}

// TestShipState_CommitCheck_StepNotInProgress checks that a commit step that is not in_progress gives a DomainError before any staging.
func TestShipState_CommitCheck_StepNotInProgress(t *testing.T) {
	branch := "feat/cc-pending"
	dir, path := commitCheckFixture(t, branch)
	setStepStatus(t, path, "commit", "pending", nil)
	writeFile(t, filepath.Join(dir, "a.txt"), "a")

	_, err := runCommitCheck(t, dir, branch)
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v, want DomainError", err)
	}
	if de.Suggestion != "Call begin-step for commit first, then call commit-check again." {
		t.Errorf("suggestion = %q", de.Suggestion)
	}
	if staged := gitOut(t, dir, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("staged = %q, want nothing staged before the step check passes", staged)
	}
	if _, ok := readStateData(t, path)["commitBaseHead"]; ok {
		t.Error("commitBaseHead set, want absent after a rejected call")
	}
}

// TestShipState_CommitCheck_NoCommitStep checks that a pipeline with no commit step gives a DataError.
func TestShipState_CommitCheck_NoCommitStep(t *testing.T) {
	branch := "feat/cc-nostep"
	dir, path := commitCheckFixture(t, branch)
	setSteps(t, path, "execute", "review")

	_, err := runCommitCheck(t, dir, branch)
	var de *mcpserver.DataError
	if !errors.As(err, &de) || de.Suggestion == "" {
		t.Fatalf("err = %v, want DataError with a Suggestion", err)
	}
}

// TestShipState_CommitCheck_GitFailures checks that each failed git call gives an InfraError and stores no commitBaseHead.
func TestShipState_CommitCheck_GitFailures(t *testing.T) {
	for _, sub := range []string{"add", "diff", "rev-parse"} {
		t.Run(sub, func(t *testing.T) {
			branch := "feat/cc-git-" + sub
			dir, path := commitCheckFixture(t, branch)
			writeFile(t, filepath.Join(dir, "a.txt"), "a")
			prev := shipCommitCheckGit
			shipCommitCheckGit = func(d string, args ...string) (string, error) {
				if args[0] == sub {
					return "", errors.New("git exploded")
				}
				return prev(d, args...)
			}
			t.Cleanup(func() { shipCommitCheckGit = prev })

			_, err := runCommitCheck(t, dir, branch)
			var ie *mcpserver.InfraError
			if !errors.As(err, &ie) {
				t.Fatalf("err = %v, want InfraError", err)
			}
			if ie.Suggestion != "Check the repository state with git status, then call commit-check again." {
				t.Errorf("suggestion = %q", ie.Suggestion)
			}
			if !strings.Contains(ie.Msg, "git "+sub) {
				t.Errorf("msg = %q, want it to name git %s", ie.Msg, sub)
			}
			if _, ok := readStateData(t, path)["commitBaseHead"]; ok {
				t.Error("commitBaseHead set, want absent after a git failure")
			}
			// A failure after git add leaves the staging in place.
			wantStaged := "a.txt"
			if sub == "add" {
				wantStaged = ""
			}
			if staged := gitOut(t, dir, "diff", "--cached", "--name-only"); staged != wantStaged {
				t.Errorf("staged = %q, want %q", staged, wantStaged)
			}
		})
	}
}

// TestShipState_CommitCheck_NotAGitRepository checks that a work directory outside git gives an InfraError.
func TestShipState_CommitCheck_NotAGitRepository(t *testing.T) {
	branch := "feat/cc-nogit"
	root, _ := commitCheckFixture(t, branch)
	workDir := t.TempDir()

	_, err := shipState(root, workDir, ShipStateIn{
		Action: "commit-check",
		Detail: map[string]any{"branch": branch},
	}, fixedNow(time.Now()))
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) || ie.Suggestion == "" {
		t.Fatalf("err = %v, want InfraError with a Suggestion", err)
	}
}

// TestShipState_CommitCheck_WriteFailureDirty checks that a failed state write on a dirty tree keeps the staging and persists no commitBaseHead.
func TestShipState_CommitCheck_WriteFailureDirty(t *testing.T) {
	branch := "feat/cc-wfail-dirty"
	dir, path := commitCheckFixture(t, branch)
	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	useShipStateWrite(t, func(*state.State) error { return errors.New("disk full") })

	_, err := runCommitCheck(t, dir, branch)
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) || ie.Suggestion == "" {
		t.Fatalf("err = %v, want InfraError with a Suggestion", err)
	}
	// Staging ran before the write, so it stays; the state file is unchanged.
	if staged := gitOut(t, dir, "diff", "--cached", "--name-only"); staged != "a.txt" {
		t.Errorf("staged = %q, want a.txt", staged)
	}
	if _, ok := readStateData(t, path)["commitBaseHead"]; ok {
		t.Error("commitBaseHead persisted, want absent after a failed write")
	}
}

// TestShipState_CommitCheck_WriteFailureClean checks that a failed state write on a clean tree persists no decision, no commitBaseHead, and no step change.
func TestShipState_CommitCheck_WriteFailureClean(t *testing.T) {
	branch := "feat/cc-wfail-clean"
	dir, path := commitCheckFixture(t, branch)
	useShipStateWrite(t, func(*state.State) error { return errors.New("disk full") })

	_, err := runCommitCheck(t, dir, branch)
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) || ie.Suggestion == "" {
		t.Fatalf("err = %v, want InfraError with a Suggestion", err)
	}
	data := readStateData(t, path)
	if _, ok := data["commitBaseHead"]; ok {
		t.Error("commitBaseHead persisted, want absent after a failed write")
	}
	if decisions, _ := data["decisions"].([]any); len(decisions) != 0 {
		t.Errorf("decisions = %v, want none after a failed write", decisions)
	}
	if got := commitStepEntry(t, path)["status"]; got != "in_progress" {
		t.Errorf("commit status = %v, want in_progress after a failed write", got)
	}
}

// TestShipState_CommitCheck_DirtyWithStoredBaseSkipsWrite checks that a dirty call with a stored commitBaseHead writes no state.
func TestShipState_CommitCheck_DirtyWithStoredBaseSkipsWrite(t *testing.T) {
	branch := "feat/cc-nowrite"
	dir, _ := commitCheckFixture(t, branch)
	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	if _, err := runCommitCheck(t, dir, branch); err != nil {
		t.Fatalf("first commit-check: %v", err)
	}
	writes := 0
	useShipStateWrite(t, func(*state.State) error { writes++; return nil })

	out, err := runCommitCheck(t, dir, branch)
	if err != nil {
		t.Fatalf("second commit-check: %v", err)
	}
	if out.Clean || writes != 0 {
		t.Errorf("clean=%v writes=%d, want a dirty result and no state write", out.Clean, writes)
	}
}

// TestShipState_CommitCheck_ShortSHA checks that shortSHA, which the landed
// commit result uses, cuts a long sha to 7 characters and keeps a short one.
func TestShipState_CommitCheck_ShortSHA(t *testing.T) {
	if got := shortSHA("0123456789abcdef"); got != "0123456" {
		t.Errorf("long sha = %q, want 0123456", got)
	}
	if got := shortSHA("abc"); got != "abc" {
		t.Errorf("short sha = %q, want abc", got)
	}
}

// commitCheckValidateSchema validates the ship state file at path against
// the published ship-state.schema.json.
func commitCheckValidateSchema(t *testing.T, path string) {
	t.Helper()
	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc", "schemas", "ship-state.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	sch, err := jsonschema.NewCompiler().Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state file %s: %v", path, err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("unmarshal state file for schema validation: %v", err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("state file written by commit-check: schema rejected it: %v", err)
	}
}

// TestShipStateSchema_CommitCheckWrites checks that every state file that
// commit-check writes passes ship-state.schema.json: the dirty call that
// stores commitBaseHead, the landed-commit call that journals a sha side
// effect, and the nothing-to-commit call that appends a decision.
func TestShipStateSchema_CommitCheckWrites(t *testing.T) {
	t.Run("dirty then landed commit", func(t *testing.T) {
		branch := "feat/cc-schema-landed"
		dir, path := commitCheckFixture(t, branch)
		writeFile(t, filepath.Join(dir, "a.txt"), "a")
		if _, err := runCommitCheck(t, dir, branch); err != nil {
			t.Fatalf("dirty commit-check: %v", err)
		}
		if _, ok := readStateData(t, path)["commitBaseHead"].(string); !ok {
			t.Fatal("dirty commit-check stored no commitBaseHead")
		}
		commitCheckValidateSchema(t, path)

		mustRun(t, dir, "git", "commit", "-m", "landed")
		out, err := runCommitCheck(t, dir, branch)
		if err != nil {
			t.Fatalf("landed commit-check: %v", err)
		}
		if !out.StepCompleted || !strings.HasPrefix(out.Result, "committed ") {
			t.Fatalf("landed out = %+v, want a completed step with a committed result", out)
		}
		if _, ok := readStateData(t, path)["sideEffects"].(map[string]any)["commit"]; !ok {
			t.Fatal("landed commit-check journaled no commit side effect")
		}
		commitCheckValidateSchema(t, path)
	})
	t.Run("nothing to commit", func(t *testing.T) {
		branch := "feat/cc-schema-clean"
		dir, path := commitCheckFixture(t, branch)
		out, err := runCommitCheck(t, dir, branch)
		if err != nil {
			t.Fatalf("commit-check: %v", err)
		}
		if !out.StepCompleted || !strings.HasPrefix(out.Result, commitNothingPrefix) {
			t.Fatalf("out = %+v, want a completed nothing-to-commit step", out)
		}
		commitCheckValidateSchema(t, path)
	})
}

// TestShipState_CommitCheck_NonStringBaseHead checks that a commitBaseHead
// that is not a string gives a DataError before any staging, and that the
// state file keeps the bad value instead of a new HEAD.
func TestShipState_CommitCheck_NonStringBaseHead(t *testing.T) {
	branch := "feat/cc-badbase"
	dir, path := commitCheckFixture(t, branch)
	data := readStateData(t, path)
	data["commitBaseHead"] = 42
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	writeFile(t, filepath.Join(dir, "a.txt"), "a")

	_, err = runCommitCheck(t, dir, branch)
	var de *mcpserver.DataError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v (%T), want *mcpserver.DataError", err, err)
	}
	if !strings.Contains(de.Msg, "commitBaseHead") || de.Suggestion == "" {
		t.Errorf("DataError = %+v, want a message naming commitBaseHead and a suggestion", de)
	}
	if staged := gitOut(t, dir, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("staged = %q, want nothing staged", staged)
	}
	if got := readStateData(t, path)["commitBaseHead"]; got != float64(42) {
		t.Errorf("commitBaseHead = %v, want the bad value kept", got)
	}
	if got := commitStepEntry(t, path)["status"]; got != "in_progress" {
		t.Errorf("commit status = %v, want in_progress", got)
	}
}

// TestShipState_CommitCheck_BranchUnresolved checks that a missing
// detail.branch in a work directory outside git gives a DomainError.
func TestShipState_CommitCheck_BranchUnresolved(t *testing.T) {
	dir := t.TempDir()

	_, err := shipState(dir, dir, ShipStateIn{Action: "commit-check"}, fixedNow(time.Now()))
	var de *mcpserver.DomainError
	if !errors.As(err, &de) || de.Suggestion == "" {
		t.Fatalf("err = %v, want DomainError with a Suggestion", err)
	}
	if !strings.Contains(de.Msg, "could not determine branch") {
		t.Errorf("msg = %q, want the branch resolution failure", de.Msg)
	}
}

// TestShipState_CommitCheck_NoShipState checks that a branch with no ship
// state gives a DataError and stages nothing.
func TestShipState_CommitCheck_NoShipState(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	writeFile(t, filepath.Join(dir, "a.txt"), "a")

	_, err := runCommitCheck(t, dir, "feat/cc-nostate")
	var de *mcpserver.DataError
	if !errors.As(err, &de) || de.Suggestion == "" {
		t.Fatalf("err = %v, want DataError with a Suggestion", err)
	}
	if !errors.Is(err, errNoShipState) {
		t.Errorf("err = %v, want the no-ship-state cause", err)
	}
	if staged := gitOut(t, dir, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("staged = %q, want nothing staged", staged)
	}
}

// TestShipState_CommitCheck_ToolSurface checks that the registered ship_state
// tool lists commit-check in its action enum and describes its inputs, side
// effects and errors.
func TestShipState_CommitCheck_ToolSurface(t *testing.T) {
	s := mcpserver.New("test", "0.0.0-test")
	RegisterShipStateTools(s)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	resp, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var tool *mcp.Tool
	for _, candidate := range resp.Tools {
		if candidate.Name == "ship_state" {
			tool = candidate
		}
	}
	if tool == nil {
		t.Fatal(`no "ship_state" tool registered`)
	}

	var line string
	for _, l := range strings.Split(tool.Description, "\n") {
		if strings.HasPrefix(l, "- commit-check: ") {
			line = l
		}
	}
	if line == "" {
		t.Fatal("tool description has no commit-check line")
	}
	for _, want := range []string{
		"Requires: (none). Optional: detail.branch.",
		"staging", "commitBaseHead", "decide", "complete-step", "side-effect journal",
		"git InfraError", "step-state DomainError",
		commitNothingPrefix + ": execute committed N wave commit(s)",
		commitNothingPrefix + ": the working tree is clean",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("commit-check description %q does not contain %q", line, want)
		}
	}

	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("marshal input schema: %v", err)
	}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal input schema: %v", err)
	}
	found := false
	for _, a := range schema.Properties["action"].Enum {
		if a == "commit-check" {
			found = true
		}
	}
	if !found {
		t.Errorf("action enum = %v, want it to contain commit-check", schema.Properties["action"].Enum)
	}
}

// ---------------------------------------------------------------------------
// linkedPlan: complete-step / complete of execute saves the plan run times
// ---------------------------------------------------------------------------

const (
	linkedPlanStart = "2026-10-10T08:20:00Z"
	linkedPlanDone  = "2026-10-10T09:40:00Z"
)

// newLinkedPlanFixture builds a ship run whose execute state links a plan
// run (plan file linkedPlanFile). integrity replaces the plan state
// planIntegrity; nil leaves the plan state as state.Init made it.
func newLinkedPlanFixture(t *testing.T, branch string, integrity map[string]any) planRunCleanupFixture {
	t.Helper()
	f := newPlanRunCleanupFixture(t, branch, false, linkedPlanFile)
	if integrity != nil {
		f.setPlanIntegrity(t, integrity)
	}
	return f
}

// setPlanIntegrity rewrites planIntegrity in the fixture's plan state file.
func (f planRunCleanupFixture) setPlanIntegrity(t *testing.T, integrity map[string]any) {
	t.Helper()
	data := readStateData(t, f.planRunPath)
	data["planIntegrity"] = integrity
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal plan state: %v", err)
	}
	if err := os.WriteFile(f.planRunPath, raw, 0o644); err != nil {
		t.Fatalf("write plan state %s: %v", f.planRunPath, err)
	}
}

// completeStep runs one complete-step or complete call on the fixture's ship
// run and returns the narration output.
func (f planRunCleanupFixture) completeStep(t *testing.T, action, step string, detail map[string]any) ShipStepNarrationOut {
	t.Helper()
	d := map[string]any{"branch": f.branch}
	for k, v := range detail {
		d[k] = v
	}
	out, err := shipState(f.dir, f.dir, ShipStateIn{Action: action, Step: step, Detail: d},
		fixedNow(time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("%s %s: %v", action, step, err)
	}
	narr, ok := out.(ShipStepNarrationOut)
	if !ok {
		t.Fatalf("%s output = %#v, want ShipStepNarrationOut", action, out)
	}
	return narr
}

func TestShipState_LinkedPlan_SavedOnExecuteSuccess(t *testing.T) {
	for _, action := range []string{"complete-step", "complete"} {
		t.Run(action, func(t *testing.T) {
			f := newLinkedPlanFixture(t, "feat/linked-plan-"+action, map[string]any{
				"skillInvoked": linkedPlanStart,
				"done":         linkedPlanDone,
			})

			narr := f.completeStep(t, action, "execute", nil)
			if len(narr.Warnings) != 0 {
				t.Errorf("warnings = %v, want none", narr.Warnings)
			}

			data := f.shipStateOnDisk(t)
			want := map[string]any{
				"planFile":    filepath.Clean(linkedPlanFile(f.dir)),
				"startedAt":   linkedPlanStart,
				"completedAt": linkedPlanDone,
			}
			if got := data[shipLinkedPlanKey]; !reflect.DeepEqual(got, any(want)) {
				t.Errorf("linkedPlan = %#v, want %#v", got, want)
			}
			if step := findStepMap(t, data, "execute"); step["status"] != "completed" {
				t.Errorf("execute status = %v, want completed", step["status"])
			}
			if err := shipStateSchemaValidator(t)(data); err != nil {
				t.Errorf("ship state with linkedPlan: schema rejected it: %v", err)
			}
		})
	}
}

func TestShipState_LinkedPlan_CompletedAtAbsent(t *testing.T) {
	cases := map[string]map[string]any{
		"no done mark":          {"skillInvoked": linkedPlanStart},
		"done is not RFC 3339":  {"skillInvoked": linkedPlanStart, "done": "yesterday"},
		"done is not a string":  {"skillInvoked": linkedPlanStart, "done": true},
		"done is an empty text": {"skillInvoked": linkedPlanStart, "done": ""},
	}
	for name, integrity := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLinkedPlanFixture(t, "feat/linked-plan-no-done", integrity)

			narr := f.completeStep(t, "complete-step", "execute", nil)
			if len(narr.Warnings) != 0 {
				t.Errorf("warnings = %v, want none", narr.Warnings)
			}

			data := f.shipStateOnDisk(t)
			want := map[string]any{
				"planFile":  filepath.Clean(linkedPlanFile(f.dir)),
				"startedAt": linkedPlanStart,
			}
			if got := data[shipLinkedPlanKey]; !reflect.DeepEqual(got, any(want)) {
				t.Errorf("linkedPlan = %#v, want %#v (no completedAt)", got, want)
			}
			if err := shipStateSchemaValidator(t)(data); err != nil {
				t.Errorf("ship state with linkedPlan: schema rejected it: %v", err)
			}
		})
	}
}

func TestShipState_LinkedPlan_SecondCompleteStepKeepsFirst(t *testing.T) {
	f := newLinkedPlanFixture(t, "feat/linked-plan-keep", map[string]any{
		"skillInvoked": linkedPlanStart,
		"done":         linkedPlanDone,
	})
	f.completeStep(t, "complete-step", "execute", nil)
	first := f.shipStateOnDisk(t)[shipLinkedPlanKey]

	f.setPlanIntegrity(t, map[string]any{
		"skillInvoked": "2026-10-10T11:00:00Z",
		"done":         "2026-10-10T12:00:00Z",
	})
	narr := f.completeStep(t, "complete-step", "execute", nil)
	if len(narr.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", narr.Warnings)
	}

	if got := f.shipStateOnDisk(t)[shipLinkedPlanKey]; !reflect.DeepEqual(got, first) {
		t.Errorf("linkedPlan after the second call = %#v, want the first %#v", got, first)
	}
}

func TestShipState_LinkedPlan_NoSaveNoWarning(t *testing.T) {
	integrity := map[string]any{"skillInvoked": linkedPlanStart, "done": linkedPlanDone}

	t.Run("no execute state", func(t *testing.T) {
		f := newPlanRunCleanupFixture(t, "feat/linked-plan-no-exec", false, nil)
		f.setPlanIntegrity(t, integrity)
		narr := f.completeStep(t, "complete-step", "execute", nil)
		assertNoLinkedPlan(t, f, narr)
	})
	t.Run("execute state without planPath", func(t *testing.T) {
		f := newPlanRunCleanupFixture(t, "feat/linked-plan-no-path", false, func(string) string { return "" })
		f.setPlanIntegrity(t, integrity)
		narr := f.completeStep(t, "complete-step", "execute", nil)
		assertNoLinkedPlan(t, f, narr)
	})
	t.Run("no plan state for the planPath", func(t *testing.T) {
		f := newPlanRunCleanupFixture(t, "feat/linked-plan-no-plan", false, func(dir string) string {
			return filepath.Join(dir, "plans", "other.md")
		})
		f.setPlanIntegrity(t, integrity)
		narr := f.completeStep(t, "complete-step", "execute", nil)
		assertNoLinkedPlan(t, f, narr)
	})
	t.Run("step is not execute (complete-step)", func(t *testing.T) {
		f := newLinkedPlanFixture(t, "feat/linked-plan-other-step", integrity)
		narr := f.completeStep(t, "complete-step", "commit", nil)
		assertNoLinkedPlan(t, f, narr)
	})
	t.Run("step is not execute (complete)", func(t *testing.T) {
		f := newLinkedPlanFixture(t, "feat/linked-plan-other-step-legacy", integrity)
		narr := f.completeStep(t, "complete", "commit", nil)
		assertNoLinkedPlan(t, f, narr)
	})
	t.Run("outcome failure", func(t *testing.T) {
		f := newLinkedPlanFixture(t, "feat/linked-plan-failure", integrity)
		narr := f.completeStep(t, "complete-step", "execute", map[string]any{"outcome": "failure", "result": "boom"})
		assertNoLinkedPlan(t, f, narr)
		if step := findStepMap(t, f.shipStateOnDisk(t), "execute"); step["status"] != "failed" {
			t.Errorf("execute status = %v, want failed", step["status"])
		}
	})
}

// assertNoLinkedPlan checks that a call saved no linkedPlan and returned no
// warning.
func assertNoLinkedPlan(t *testing.T, f planRunCleanupFixture, narr ShipStepNarrationOut) {
	t.Helper()
	if len(narr.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", narr.Warnings)
	}
	if got, has := f.shipStateOnDisk(t)[shipLinkedPlanKey]; has {
		t.Errorf("linkedPlan = %#v, want the key absent", got)
	}
}

func TestShipState_LinkedPlan_StartTimeNotRFC3339(t *testing.T) {
	const wantWarning = "plan times not saved: the linked plan run has no start time."
	cases := map[string]map[string]any{
		"skillInvoked absent":          {"done": linkedPlanDone},
		"skillInvoked not RFC 3339":    {"skillInvoked": "yesterday", "done": linkedPlanDone},
		"skillInvoked not a string":    {"skillInvoked": 42, "done": linkedPlanDone},
		"planIntegrity has no content": {},
	}
	for name, integrity := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLinkedPlanFixture(t, "feat/linked-plan-bad-start", integrity)

			narr := f.completeStep(t, "complete-step", "execute", nil)
			if !reflect.DeepEqual(narr.Warnings, []string{wantWarning}) {
				t.Errorf("warnings = %v, want [%q]", narr.Warnings, wantWarning)
			}

			data := f.shipStateOnDisk(t)
			if got, has := data[shipLinkedPlanKey]; has {
				t.Errorf("linkedPlan = %#v, want the key absent", got)
			}
			if step := findStepMap(t, data, "execute"); step["status"] != "completed" {
				t.Errorf("execute status = %v, want completed", step["status"])
			}
		})
	}
}

// TestShipState_LinkedPlan_PlanStateReadErrorStillCompletesStep forces a read
// error from the plan state lookup: the step still completes, and the output
// carries the warning.
func TestShipState_LinkedPlan_PlanStateReadErrorStillCompletesStep(t *testing.T) {
	for _, action := range []string{"complete-step", "complete"} {
		t.Run(action, func(t *testing.T) {
			f := newLinkedPlanFixture(t, "feat/linked-plan-plan-read-"+action, map[string]any{
				"skillInvoked": linkedPlanStart,
				"done":         linkedPlanDone,
			})
			prev := shipFindPlanRunByPlanFile
			shipFindPlanRunByPlanFile = func(string, string) (*state.State, error) {
				return nil, errors.New("state: readdir runs: permission denied")
			}
			t.Cleanup(func() { shipFindPlanRunByPlanFile = prev })

			narr := f.completeStep(t, action, "execute", nil)

			want := []string{"plan times not saved: state: readdir runs: permission denied. The dashboard falls back to the history join."}
			if !reflect.DeepEqual(narr.Warnings, want) {
				t.Errorf("warnings = %v, want %v", narr.Warnings, want)
			}
			data := f.shipStateOnDisk(t)
			if got, has := data[shipLinkedPlanKey]; has {
				t.Errorf("linkedPlan = %#v, want the key absent", got)
			}
			if step := findStepMap(t, data, "execute"); step["status"] != "completed" {
				t.Errorf("execute status = %v, want completed", step["status"])
			}
		})
	}
}

// TestShipState_LinkedPlan_LookupErrorStillCompletesStep corrupts the execute
// state file: state.Find returns a read error, the step still completes, and
// the output carries the warning.
func TestShipState_LinkedPlan_LookupErrorStillCompletesStep(t *testing.T) {
	for _, action := range []string{"complete-step", "complete"} {
		t.Run(action, func(t *testing.T) {
			f := newLinkedPlanFixture(t, "feat/linked-plan-lookup-"+action, map[string]any{
				"skillInvoked": linkedPlanStart,
				"done":         linkedPlanDone,
			})
			execSt, err := state.Find(f.dir, "execute", f.branch)
			if err != nil || execSt == nil {
				t.Fatalf("find execute state: st=%v err=%v", execSt, err)
			}
			if err := os.WriteFile(execSt.Path, []byte("{not json"), 0o644); err != nil {
				t.Fatalf("corrupt execute state: %v", err)
			}

			narr := f.completeStep(t, action, "execute", nil)

			if len(narr.Warnings) != 1 {
				t.Fatalf("warnings = %v, want exactly one", narr.Warnings)
			}
			w := narr.Warnings[0]
			if !strings.HasPrefix(w, "plan times not saved: state: read ") ||
				!strings.HasSuffix(w, ". The dashboard falls back to the history join.") {
				t.Errorf("warning = %q, want the plan-times lookup warning", w)
			}
			data := f.shipStateOnDisk(t)
			if got, has := data[shipLinkedPlanKey]; has {
				t.Errorf("linkedPlan = %#v, want the key absent", got)
			}
			if step := findStepMap(t, data, "execute"); step["status"] != "completed" {
				t.Errorf("execute status = %v, want completed", step["status"])
			}
		})
	}
}
