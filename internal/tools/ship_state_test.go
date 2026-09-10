package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/pipeline"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

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

	wantDir := filepath.Join(dir, paths.DataDir, "execution")
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
	orphan := filepath.Join(dir, paths.DataDir, "execution", fmt.Sprintf("ship-%s-20200101T000000Z.json", slug))
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

// ---------------------------------------------------------------------------
// gc: dry-run and real sweep
// ---------------------------------------------------------------------------

func TestShipState_GC_DryRun_ClassifiesAndDropsCommit(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	execDir := filepath.Join(dir, paths.DataDir, "execution")
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

	execDir := filepath.Join(dir, paths.DataDir, "execution")
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

	execDir := filepath.Join(dir, paths.DataDir, "execution")
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

// ---------------------------------------------------------------------------
// migrate
// ---------------------------------------------------------------------------

func TestShipState_Migrate(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	oldSlug := state.SlugifyBranch("feat/old-name")
	execDir := filepath.Join(dir, paths.DataDir, "execution")
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
// automation mode resolved from a fixture v5 config." It writes a config
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

	writeFile(t, filepath.Join(dir, paths.DataDir, "config.json"), `{"version": 5, "automation": {"mode": "confirm"}}`)

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

	writeFile(t, filepath.Join(dir, paths.DataDir, "config.json"), `{}`)
	writeFile(t, filepath.Join(dir, paths.DataDir, "local.json"), `{"automation": {"mode": "unattended"}}`)

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
