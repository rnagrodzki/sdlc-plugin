package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
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

// ---------------------------------------------------------------------------
// init
// ---------------------------------------------------------------------------

func TestShipState_Init(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/state-init")

	path := shipStateInitFixture(t, dir, "feat/state-init")

	wantDir := filepath.Join(dir, ".sdlc", "execution")
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
	if !ok || len(steps) != 7 {
		t.Fatalf("steps = %v, want a 7-entry scaffold", data["steps"])
	}
}

func TestShipState_Init_PrunesOrphans(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/prune-me")

	slug := state.SlugifyBranch("feat/prune-me")
	orphan := filepath.Join(dir, ".sdlc", "execution", fmt.Sprintf("ship-%s-20200101T000000Z.json", slug))
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
	todosOut, ok := out.(ShipTodosOut)
	if !ok {
		t.Fatalf("output = %#v, want ShipTodosOut", out)
	}
	if len(todosOut.Todos) == 0 {
		t.Error("Todos is empty, want a rendered todo list")
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
	if _, ok := out.(ShipTodosOut); !ok {
		t.Fatalf("output = %#v, want ShipTodosOut", out)
	}
	data := readStateData(t, path)
	step := findStepMap(t, data, "execute")
	if step["status"] != "completed" || step["result"] != "ok" {
		t.Errorf("step = %v, want status=completed result=ok", step)
	}

	if _, err := shipState(dir, dir, ShipStateIn{
		Action: "complete-step",
		Step:   "commit",
		Detail: map[string]any{"branch": "feat/complete-step", "outcome": "failure", "result": "boom"},
	}, fixedNow(time.Now())); err != nil {
		t.Fatalf("complete-step failure: %v", err)
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

func TestShipState_Cleanup_ValidTerminalDeletesFile(t *testing.T) {
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

	out, err := shipState(dir, dir, ShipStateIn{
		Action: "cleanup",
		Detail: map[string]any{"branch": "feat/cleanup-ok"},
	}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["valid"] != true || m["cleaned"] != true {
		t.Errorf("output = %#v, want {valid:true cleaned:true}", out)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("state file %s should have been deleted", path)
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

	execDir := filepath.Join(dir, ".sdlc", "execution")
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

	execDir := filepath.Join(dir, ".sdlc", "execution")
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

	execDir := filepath.Join(dir, ".sdlc", "execution")
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
	execDir := filepath.Join(dir, ".sdlc", "execution")
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

	writeFile(t, filepath.Join(dir, ".sdlc", "config.json"), `{"version": 5, "automation": {"mode": "confirm"}}`)

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

	writeFile(t, filepath.Join(dir, ".sdlc", "config.json"), `{}`)
	writeFile(t, filepath.Join(dir, ".sdlc", "local.json"), `{"automation": {"mode": "unattended"}}`)

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
	if _, ok := out.(ShipTodosOut); ok {
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
	if len(steps) != 7 {
		t.Errorf("steps = %v, want the original 7-entry scaffold preserved", steps)
	}
}

// ---------------------------------------------------------------------------
// error classification helpers
// ---------------------------------------------------------------------------

func isDomainError(err error) bool {
	var de *mcpserver.DomainError
	return errors.As(err, &de)
}
