package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ---------------------------------------------------------------------------
// ship_prepare tests
// ---------------------------------------------------------------------------

// checkoutBranch creates and switches to a new branch, avoiding the
// "You are on the default branch" warning that a main-branch fixture would
// otherwise trigger (gitx.DefaultBranch falls back to the local "main" branch
// when no origin remote exists).
func checkoutBranch(t *testing.T, dir, name string) {
	t.Helper()
	cmd := exec.Command("git", "checkout", "-b", name)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b %s: %s: %v", name, out, err)
	}
}

// TestShipPrepare_StateInit verifies the happy-path shape: zero errors, a
// merged flags/sources map, and a state file written with the fixed 7-entry
// step scaffold plus the fields cmdInit/initState stamp (version, startedAt,
// branch, worktree, flags, steps, decisions, deferredFindings, sessionId).
func TestShipPrepare_StateInit(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/my-feature")

	out, err := shipPrepare(dir, dir, ShipPrepareIn{
		SkipConfigCheck: true,
		HasPlan:         false,
		SessionID:       "sess-123",
	})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("Errors = %v, want empty", out.Errors)
	}
	if out.StateFile == "" {
		t.Fatal("StateFile is empty, want a written path")
	}
	if out.Branch != "feat/my-feature" {
		t.Errorf("Branch = %q, want %q", out.Branch, "feat/my-feature")
	}
	if out.Worktree != dir {
		t.Errorf("Worktree = %q, want %q", out.Worktree, dir)
	}
	if len(out.PrunedOrphans) != 0 {
		t.Errorf("PrunedOrphans = %v, want empty (no pre-existing state files)", out.PrunedOrphans)
	}

	wantStateDir := filepath.Join(dir, ".sdlc", "execution")
	if filepath.Dir(out.StateFile) != wantStateDir {
		t.Errorf("StateFile dir = %q, want %q", filepath.Dir(out.StateFile), wantStateDir)
	}

	// Default-derived flags/sources sanity check.
	if src, ok := out.Sources["steps"]; !ok || src != "default" {
		t.Errorf("Sources[steps] = %q, want %q", src, "default")
	}
	if v, _ := out.Flags["bump"].(string); v != "patch" {
		t.Errorf("Flags[bump] = %v, want %q", out.Flags["bump"], "patch")
	}

	// Verify the on-disk state file shape.
	raw, err := os.ReadFile(out.StateFile)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("unmarshal state file: %v", err)
	}

	wantKeys := []string{
		"version", "startedAt", "branch", "worktree", "flags", "steps",
		"decisions", "deferredFindings", "sessionId",
	}
	for _, k := range wantKeys {
		if _, ok := data[k]; !ok {
			t.Errorf("state file missing key: %s", k)
		}
	}
	if data["sessionId"] != "sess-123" {
		t.Errorf("sessionId = %v, want %q", data["sessionId"], "sess-123")
	}
	if data["branch"] != "feat/my-feature" {
		t.Errorf("branch = %v, want %q", data["branch"], "feat/my-feature")
	}
	steps, ok := data["steps"].([]any)
	if !ok || len(steps) != 7 {
		t.Fatalf("steps = %v, want a 7-entry array", data["steps"])
	}
	first, _ := steps[0].(map[string]any)
	if first["name"] != "execute" || first["status"] != "pending" {
		t.Errorf("steps[0] = %v, want {name: execute, status: pending}", first)
	}
}

// TestShipPrepare_NoSessionID verifies that an empty SessionID is stamped as
// JSON null (matching lib/state.js's initState, not omitted or "").
func TestShipPrepare_NoSessionID(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/no-session")

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("Errors = %v, want empty", out.Errors)
	}

	raw, err := os.ReadFile(out.StateFile)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("unmarshal state file: %v", err)
	}
	if v, ok := data["sessionId"]; !ok || v != nil {
		t.Errorf("sessionId = %v, want JSON null", v)
	}
}

// TestShipPrepare_StepsFromConfig verifies that ship.steps[] configured in
// .sdlc/local.json is picked up with Sources["steps"] == "config" (ship is
// not a project section, so its config lives in local.json, not config.json).
func TestShipPrepare_StepsFromConfig(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/config-steps")

	writeFile(t, filepath.Join(dir, ".sdlc", "local.json"), `{"ship": {"steps": ["commit", "review"]}}`)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("Errors = %v, want empty", out.Errors)
	}
	if src := out.Sources["steps"]; src != "config" {
		t.Errorf("Sources[steps] = %q, want %q", src, "config")
	}
	steps, _ := out.Flags["steps"].([]string)
	if len(steps) != 2 || steps[0] != "commit" || steps[1] != "review" {
		t.Errorf("Flags[steps] = %v, want [commit review]", out.Flags["steps"])
	}
}

// TestShipPrepare_RebaseConfigMalformedValuePassesThrough verifies that a
// config `ship.rebase` value which is neither bool nor string (a number,
// here) is still passed through verbatim into Flags["rebase"], with
// Sources["rebase"] == "config" — matching ship.js's mergeFlags, which does
// `merged.rebase = cfg.rebase` unconditionally with no type gate. Before the
// fix, Go's type-switch had no default case, so Flags["rebase"] was left
// unset (absent from the map) while Sources["rebase"] was still stamped
// "config" — a merged/sources disagreement that this test locks against
// regressing.
func TestShipPrepare_RebaseConfigMalformedValuePassesThrough(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/config-rebase")

	writeFile(t, filepath.Join(dir, ".sdlc", "local.json"), `{"ship": {"rebase": 42}}`)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("Errors = %v, want empty", out.Errors)
	}
	if src := out.Sources["rebase"]; src != "config" {
		t.Errorf("Sources[rebase] = %q, want %q", src, "config")
	}
	if v, ok := out.Flags["rebase"]; !ok {
		t.Error("Flags[rebase] is absent, want the malformed config value passed through verbatim")
	} else if f, _ := v.(float64); f != 42 {
		t.Errorf("Flags[rebase] = %v, want 42", v)
	}
}

// TestShipPrepare_PrunedOrphans verifies that pre-existing ship-<slug>-*.json
// state files for the same branch are reported in PrunedOrphans and actually
// removed from disk.
func TestShipPrepare_PrunedOrphans(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/orphans")

	execDir := filepath.Join(dir, ".sdlc", "execution")
	orphan := filepath.Join(execDir, "ship-feat-orphans-20200101T000000Z.json")
	writeFile(t, orphan, `{"sessionId": null}`)
	// Decoy: a different (longer) slug that merely starts with the same
	// prefix. Must NOT be reported or pruned — only exact slug matches
	// ("feat-orphans", not "feat-orphans-x") are in state.Write's prune set.
	decoy := filepath.Join(execDir, "ship-feat-orphans-x-20200101T000000Z.json")
	writeFile(t, decoy, `{"sessionId": null}`)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("Errors = %v, want empty", out.Errors)
	}
	if len(out.PrunedOrphans) != 1 || out.PrunedOrphans[0] != orphan {
		t.Errorf("PrunedOrphans = %v, want [%s]", out.PrunedOrphans, orphan)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan file %s still exists, want removed", orphan)
	}
	if _, err := os.Stat(decoy); err != nil {
		t.Errorf("decoy file %s should still exist, got stat error: %v", decoy, err)
	}
}

// TestShipPrepare_OnDefaultBranchWarning verifies the default-branch warning
// fires when staying on "main" (gitx.DefaultBranch resolves to the local
// "main" branch since the fixture has no origin remote).
func TestShipPrepare_OnDefaultBranchWarning(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	found := false
	for _, w := range out.Warnings {
		if w == `You are on the default branch "main". Ship pipelines should run on feature branches.` {
			found = true
		}
	}
	if !found {
		t.Errorf("Warnings = %v, want a default-branch warning", out.Warnings)
	}
}

// TestShipPrepare_KD5Gate verifies the config-version gate uses the soft
// style (matching plan.go/commit.go): nil Go error, a minimal errors-only
// payload, and no state file written.
func TestShipPrepare_KD5Gate(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/kd5")

	writeFile(t, filepath.Join(dir, ".sdlc", "config.json"), `{"schemaVersion": 1}`)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: false})
	if err != nil {
		t.Fatalf("shipPrepare: %v (KD5 gate must return nil error with Errors populated)", err)
	}
	if len(out.Errors) == 0 {
		t.Fatal("Errors is empty, want a config-version error")
	}
	if out.StateFile != "" {
		t.Errorf("StateFile = %q, want empty (KD5 gate must not init state)", out.StateFile)
	}

	entries, _ := os.ReadDir(filepath.Join(dir, ".sdlc", "execution"))
	if len(entries) != 0 {
		t.Errorf("execution dir has %d entries, want 0 (no state file written)", len(entries))
	}
}

// TestShipPrepare_ExecuteWithoutPlan verifies the execute-without-plan
// validation: hasPlan + "execute" in the resolved step list, but no plan
// file, is an error (source: ship.js R72/C19, #505).
func TestShipPrepare_ExecuteWithoutPlan(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/no-plan")

	out, err := shipPrepare(dir, dir, ShipPrepareIn{
		SkipConfigCheck: true,
		HasPlan:         true,
		Steps:           []string{"execute", "commit"},
		PlanFile:        "",
	})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if len(out.Errors) == 0 {
		t.Fatal("Errors is empty, want an execute-without-plan error")
	}
	if out.StateFile != "" {
		t.Errorf("StateFile = %q, want empty (validation errors block state init)", out.StateFile)
	}
}

// TestShipPrepare_InvalidStep verifies that a CLI-supplied unrecognized step
// is a hard error, while the reserved terminal step "cleanup" is always
// rejected regardless of source.
func TestShipPrepare_InvalidStep(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/bad-step")

	out, err := shipPrepare(dir, dir, ShipPrepareIn{
		SkipConfigCheck: true,
		Steps:           []string{"commit", "cleanup"},
	})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if len(out.Errors) == 0 {
		t.Fatal("Errors is empty, want a reserved-step error for \"cleanup\"")
	}
}

// ---------------------------------------------------------------------------
// ship --gc tests
// ---------------------------------------------------------------------------

func intPtr(v int) *int { return &v }

// setStateFileMtime backdates a state file's mtime by age, used to simulate
// TTL expiry without waiting on real wall-clock time.
func setStateFileMtime(t *testing.T, path string, age time.Duration) {
	t.Helper()
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("Chtimes %s: %v", path, err)
	}
}

// TestShipGC_DefaultTTL verifies that with neither --ttl-days nor
// config.state.gc.ttlDays supplied, gc resolves ttlDays to the default (7)
// and prunes a stale state file belonging to a branch that no longer exists,
// while keeping a fresh one for the still-current branch.
func TestShipGC_DefaultTTL(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	execDir := filepath.Join(dir, ".sdlc", "execution")
	stale := filepath.Join(execDir, "ship-dead-branch-20200101T000000Z.json")
	writeFile(t, stale, `{"sessionId": null}`)
	setStateFileMtime(t, stale, 30*24*time.Hour)

	fresh := filepath.Join(execDir, "ship-main-20260901T100000Z.json")
	writeFile(t, fresh, `{"sessionId": null}`)
	setStateFileMtime(t, fresh, 1*time.Hour)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, Gc: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if out.Action != "gc" {
		t.Fatalf("Action = %q, want %q", out.Action, "gc")
	}
	if out.Report == nil {
		t.Fatal("Report is nil, want a populated gc report")
	}
	if out.Report.TtlDays != 7 {
		t.Errorf("TtlDays = %d, want 7 (default)", out.Report.TtlDays)
	}
	if len(out.Report.Ship.Deleted) != 1 || out.Report.Ship.Deleted[0] != stale {
		t.Errorf("Ship.Deleted = %v, want [%s]", out.Report.Ship.Deleted, stale)
	}
	if len(out.Report.Ship.Kept) != 1 || out.Report.Ship.Kept[0] != fresh {
		t.Errorf("Ship.Kept = %v, want [%s]", out.Report.Ship.Kept, fresh)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale file %s should have been deleted", stale)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh file %s should still exist: %v", fresh, err)
	}
}

// TestShipGC_CLITTLDaysOverridesConfig verifies --ttl-days (CLI) wins over
// config.state.gc.ttlDays.
func TestShipGC_CLITTLDaysOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	writeFile(t, filepath.Join(dir, ".sdlc", "local.json"), `{"state": {"gc": {"ttlDays": 30}}}`)

	execDir := filepath.Join(dir, ".sdlc", "execution")
	f := filepath.Join(execDir, "ship-dead-branch-20200101T000000Z.json")
	writeFile(t, f, `{"sessionId": null}`)
	setStateFileMtime(t, f, 2*24*time.Hour) // 2 days old: stale under ttl=1, fresh under ttl=30

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, Gc: true, TtlDays: intPtr(1)})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if out.Report == nil {
		t.Fatal("Report is nil")
	}
	if out.Report.TtlDays != 1 {
		t.Errorf("TtlDays = %d, want 1 (CLI must win over config's 30)", out.Report.TtlDays)
	}
	if len(out.Report.Ship.Deleted) != 1 || out.Report.Ship.Deleted[0] != f {
		t.Errorf("Ship.Deleted = %v, want [%s] (CLI ttl=1 makes a 2-day-old dead-branch file stale)", out.Report.Ship.Deleted, f)
	}
}

// TestShipGC_CLITTLDaysZeroMeansImmediate verifies --ttl-days 0 is taken
// literally (instant expiry), not silently upgraded to the 7-day default.
// ship.js's ttlDays resolution uses `typeof cli.ttlDays === 'number'`, which
// is true for 0, and gcStateFiles/gcTempdirs compute ttlMs = ttlDays *
// 86400000 = 0, so ageMs < ttlMs is never true: nothing is ttl-fresh and
// only branch-liveness decides. This guards internal/state/gc.go's GC
// against reintroducing a "TTL==0 means apply the 7-day default" fallback,
// which would silently contradict Report.TtlDays == 0.
func TestShipGC_CLITTLDaysZeroMeansImmediate(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	execDir := filepath.Join(dir, ".sdlc", "execution")
	deadBranch := filepath.Join(execDir, "ship-dead-branch-20200101T000000Z.json")
	writeFile(t, deadBranch, `{"sessionId": null}`)
	setStateFileMtime(t, deadBranch, 1*time.Second) // 1 second old: fresh under any real TTL, stale only under ttl=0

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, Gc: true, TtlDays: intPtr(0)})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if out.Report == nil {
		t.Fatal("Report is nil")
	}
	if out.Report.TtlDays != 0 {
		t.Errorf("TtlDays = %d, want 0 (CLI --ttl-days 0 must survive, not be upgraded to the 7-day default)", out.Report.TtlDays)
	}
	if len(out.Report.Ship.Deleted) != 1 || out.Report.Ship.Deleted[0] != deadBranch {
		t.Errorf("Ship.Deleted = %v, want [%s] (ttl=0 means nothing is ttl-fresh; dead-branch file must be pruned immediately)", out.Report.Ship.Deleted, deadBranch)
	}
}

// TestShipGC_CLITTLDaysZeroLiveBranchCutoffMath complements
// TestShipGC_CLITTLDaysZeroMeansImmediate, which only ever exercises a
// dead-branch file: state.GC's `branchDeleted` path deletes unconditionally,
// bypassing TTL entirely, so that test alone would still pass even if the
// TTL==0 cutoff arithmetic itself regressed (e.g. TTL silently defaulting
// back to 7 days). This test uses a live branch instead, with two state
// files in the same slug group: the newest is always kept regardless of TTL
// (state.GC's "never delete the newest file for a live branch" rule), so the
// only file whose fate actually depends on the TTL value is the second,
// slightly older one — 1 hour old, well inside a 7-day default (would be
// kept) but outside a true TTL=0 cutoff of "now" (must be deleted). If
// GC ever re-defaults TTL==0 to 7 days, this file would wrongly survive and
// the assertion below would catch it.
func TestShipGC_CLITTLDaysZeroLiveBranchCutoffMath(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	// Stay on the default branch ("main"): it is live per `git branch --list`.
	liveSlug := state.SlugifyBranch("main")

	execDir := filepath.Join(dir, ".sdlc", "execution")
	newest := filepath.Join(execDir, fmt.Sprintf("ship-%s-20200101T000000Z.json", liveSlug))
	writeFile(t, newest, `{"sessionId": null}`)
	setStateFileMtime(t, newest, 1*time.Second)

	older := filepath.Join(execDir, fmt.Sprintf("ship-%s-19990101T000000Z.json", liveSlug))
	writeFile(t, older, `{"sessionId": null}`)
	setStateFileMtime(t, older, 1*time.Hour)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, Gc: true, TtlDays: intPtr(0)})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if out.Report == nil {
		t.Fatal("Report is nil")
	}
	if out.Report.TtlDays != 0 {
		t.Errorf("TtlDays = %d, want 0", out.Report.TtlDays)
	}
	if !sliceContainsStr(out.Report.Ship.Kept, newest) {
		t.Errorf("Ship.Kept = %v, want it to include newest file %s (always kept for a live branch)", out.Report.Ship.Kept, newest)
	}
	if !sliceContainsStr(out.Report.Ship.Deleted, older) {
		t.Errorf("Ship.Deleted = %v, want it to include %s (1h old must not survive a true ttl=0 cutoff, even though it would survive a wrongly-defaulted 7-day TTL)", out.Report.Ship.Deleted, older)
	}
}

// TestShipGC_ConfigTTLDaysUsedWhenNoCLI verifies config.state.gc.ttlDays is
// used when --ttl-days is not supplied.
func TestShipGC_ConfigTTLDaysUsedWhenNoCLI(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	writeFile(t, filepath.Join(dir, ".sdlc", "local.json"), `{"state": {"gc": {"ttlDays": 1}}}`)

	execDir := filepath.Join(dir, ".sdlc", "execution")
	f := filepath.Join(execDir, "ship-dead-branch-20200101T000000Z.json")
	writeFile(t, f, `{"sessionId": null}`)
	setStateFileMtime(t, f, 2*24*time.Hour)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, Gc: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if out.Report == nil {
		t.Fatal("Report is nil")
	}
	if out.Report.TtlDays != 1 {
		t.Errorf("TtlDays = %d, want 1 (from config.state.gc.ttlDays)", out.Report.TtlDays)
	}
	if len(out.Report.Ship.Deleted) != 1 || out.Report.Ship.Deleted[0] != f {
		t.Errorf("Ship.Deleted = %v, want [%s]", out.Report.Ship.Deleted, f)
	}
}

// TestShipGC_KnownBranchesFromGit verifies knownBranches is resolved via a
// real `git branch --list` shell-out (not just the currently checked-out
// branch): a state file for a branch that exists locally but is not
// currently checked out must be kept, while one for a branch that does not
// exist at all must be pruned once stale.
func TestShipGC_KnownBranchesFromGit(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	cmd := exec.Command("git", "branch", "feat/other-branch")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch feat/other-branch: %s: %v", out, err)
	}
	// Still on "main" — feat/other-branch exists locally but is not checked out.

	execDir := filepath.Join(dir, ".sdlc", "execution")
	liveSlug := state.SlugifyBranch("feat/other-branch")
	liveFile := filepath.Join(execDir, fmt.Sprintf("ship-%s-20200101T000000Z.json", liveSlug))
	writeFile(t, liveFile, `{"sessionId": null}`)
	setStateFileMtime(t, liveFile, 30*24*time.Hour)

	deadFile := filepath.Join(execDir, "ship-totally-gone-20200101T000000Z.json")
	writeFile(t, deadFile, `{"sessionId": null}`)
	setStateFileMtime(t, deadFile, 30*24*time.Hour)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, Gc: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if out.Report == nil {
		t.Fatal("Report is nil")
	}
	if !sliceContainsStr(out.Report.Ship.Kept, liveFile) {
		t.Errorf("Ship.Kept = %v, want it to include %s (branch exists locally, even if not checked out)", out.Report.Ship.Kept, liveFile)
	}
	if !sliceContainsStr(out.Report.Ship.Deleted, deadFile) {
		t.Errorf("Ship.Deleted = %v, want it to include %s (no such branch exists)", out.Report.Ship.Deleted, deadFile)
	}
}

// TestShipGC_BucketsByPrefix verifies stale, dead-branch files across all
// four state prefixes are each routed into their own report bucket.
func TestShipGC_BucketsByPrefix(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	execDir := filepath.Join(dir, ".sdlc", "execution")
	files := map[string]string{
		"ship":    filepath.Join(execDir, "ship-gone-20200101T000000Z.json"),
		"execute": filepath.Join(execDir, "execute-gone-20200101T000000Z.json"),
		"plan":    filepath.Join(execDir, "plan-gone-20200101T000000Z.json"),
		"commit":  filepath.Join(execDir, "commit-gone-20200101T000000Z.json"),
	}
	for _, p := range files {
		writeFile(t, p, `{"sessionId": null}`)
		setStateFileMtime(t, p, 30*24*time.Hour)
	}

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, Gc: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if out.Report == nil {
		t.Fatal("Report is nil")
	}
	checks := map[string]ShipGCBucket{
		"ship":    out.Report.Ship,
		"execute": out.Report.Execute,
		"plan":    out.Report.Plan,
		"commit":  out.Report.Commit,
	}
	for prefix, bucket := range checks {
		want := files[prefix]
		if len(bucket.Deleted) != 1 || bucket.Deleted[0] != want {
			t.Errorf("%s.Deleted = %v, want [%s]", prefix, bucket.Deleted, want)
		}
	}
}

// TestShipGC_ExploreTempdirsSweep verifies the exploreTempdirs bucket sweeps
// sdlc-explore-* directories under SDLC_EXPLORE_TMPDIR_OVERRIDE, applying
// the same TTL/branch-liveness rule as state files.
func TestShipGC_ExploreTempdirsSweep(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	tmpDir := t.TempDir()
	t.Setenv("SDLC_EXPLORE_TMPDIR_OVERRIDE", tmpDir)

	deadDir := filepath.Join(tmpDir, "sdlc-explore-dead-branch-abc123")
	if err := os.MkdirAll(deadDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	setStateFileMtime(t, deadDir, 30*24*time.Hour)

	liveSlug := state.SlugifyBranch("main")
	liveDir := filepath.Join(tmpDir, fmt.Sprintf("sdlc-explore-%s-def456", liveSlug))
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	setStateFileMtime(t, liveDir, 30*24*time.Hour)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, Gc: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if out.Report == nil {
		t.Fatal("Report is nil")
	}
	if !sliceContainsStr(out.Report.ExploreTempdirs.Deleted, deadDir) {
		t.Errorf("ExploreTempdirs.Deleted = %v, want it to include %s", out.Report.ExploreTempdirs.Deleted, deadDir)
	}
	if !sliceContainsStr(out.Report.ExploreTempdirs.Kept, liveDir) {
		t.Errorf("ExploreTempdirs.Kept = %v, want it to include %s", out.Report.ExploreTempdirs.Kept, liveDir)
	}
	if _, err := os.Stat(deadDir); !os.IsNotExist(err) {
		t.Errorf("dead-branch tempdir %s should have been removed", deadDir)
	}
	if _, err := os.Stat(liveDir); err != nil {
		t.Errorf("live-branch tempdir %s should still exist: %v", liveDir, err)
	}
}

// TestShipGC_ExploreTempdirsEmptyIsNotNull verifies that when the tempdir
// sweep finds zero sdlc-explore-* directories, ExploreTempdirs.Deleted/Kept
// serialize as JSON [] rather than null. gcTempdirs (internal/state/gc.go)
// builds both slices by append from a nil zero-value and returns nil, nil on
// a ReadDir error, so a naive pass-through would leak that nil into the
// report — diverging from ship.js's gcTempdirs, which always emits
// {deleted: [], kept: []}.
func TestShipGC_ExploreTempdirsEmptyIsNotNull(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	// Empty override dir: gcTempdirs's ReadDir succeeds but finds nothing,
	// so both returned slices stay nil.
	tmpDir := t.TempDir()
	t.Setenv("SDLC_EXPLORE_TMPDIR_OVERRIDE", tmpDir)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, Gc: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if out.Report == nil {
		t.Fatal("Report is nil")
	}
	if out.Report.ExploreTempdirs.Deleted == nil {
		t.Error("ExploreTempdirs.Deleted is nil, want non-nil empty slice (would serialize as null)")
	}
	if out.Report.ExploreTempdirs.Kept == nil {
		t.Error("ExploreTempdirs.Kept is nil, want non-nil empty slice (would serialize as null)")
	}

	b, err := json.Marshal(out.Report)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(b), "null") {
		t.Errorf("marshaled report contains null: %s", b)
	}
}

// TestShipGC_NoStateWritten verifies gc mode never initializes a ship state
// file (it is a pure prune-and-report short-circuit).
func TestShipGC_NoStateWritten(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, Gc: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if out.StateFile != "" {
		t.Errorf("StateFile = %q, want empty (gc must not init ship state)", out.StateFile)
	}
}

// TestShipGC_RespectsKD5Gate verifies gc mode does NOT bypass the
// config-version gate: ship.js's main() runs ensureConfigVersion
// unconditionally before ever checking cli.gc.
func TestShipGC_RespectsKD5Gate(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	writeFile(t, filepath.Join(dir, ".sdlc", "config.json"), `{"schemaVersion": 1}`)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: false, Gc: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v (KD5 gate must return nil error)", err)
	}
	if len(out.Errors) == 0 {
		t.Fatal("Errors is empty, want a config-version error (KD5 gate must run before the gc short-circuit)")
	}
	if out.Action == "gc" {
		t.Errorf("Action = %q, want empty (KD5 gate must short-circuit before gc runs)", out.Action)
	}
}

// ---------------------------------------------------------------------------
// ship_verify_side_effect tests
// ---------------------------------------------------------------------------

// TestShipVerifySideEffect_NoSideEffectStep verifies steps with no configured
// side effect (e.g. "commit") always report landed:true, reason:no-side-effect.
func TestShipVerifySideEffect_NoSideEffectStep(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	out, err := shipVerifySideEffect(dir, ShipVerifySideEffectIn{Step: "commit"})
	if err != nil {
		t.Fatalf("shipVerifySideEffect: %v", err)
	}
	if !out.Landed {
		t.Error("Landed = false, want true")
	}
	if out.Reason != "no-side-effect" {
		t.Errorf("Reason = %q, want %q", out.Reason, "no-side-effect")
	}
	if out.SideEffect != "" {
		t.Errorf("SideEffect = %q, want empty", out.SideEffect)
	}
}

// TestShipVerifySideEffect_VersionTagPresent verifies the "version" step
// reports landed:true when the expected tag exists.
func TestShipVerifySideEffect_VersionTagPresent(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	gitTag(t, dir, "v1.2.3")

	out, err := shipVerifySideEffect(dir, ShipVerifySideEffectIn{Step: "version", Expected: "v1.2.3"})
	if err != nil {
		t.Fatalf("shipVerifySideEffect: %v", err)
	}
	if !out.Landed {
		t.Error("Landed = false, want true")
	}
	if out.SideEffect != "tag" {
		t.Errorf("SideEffect = %q, want %q", out.SideEffect, "tag")
	}
	if out.Expected == nil || *out.Expected != "v1.2.3" {
		t.Errorf("Expected = %v, want v1.2.3", out.Expected)
	}
}

// TestShipVerifySideEffect_JSONShape locks in ShipVerifySideEffectOut's
// custom MarshalJSON against the two payload shapes ship.js's
// verifySideEffect emit() actually produces (scripts/skill/ship.js's
// verifySideEffect): has-side-effect always includes "expected" (a string or
// JSON null, never omitted), while no-side-effect omits "expected" and
// "sideEffect" entirely. Asserting on struct field values alone (as the
// other tests in this section do) would not catch a regression where
// `omitempty` on Expected *string silently drops the "expected" key when it
// is nil — this test decodes the actual marshaled bytes into a
// map[string]any to catch exactly that class of bug.
func TestShipVerifySideEffect_JSONShape(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	gitTag(t, dir, "v1.2.3")

	decode := func(t *testing.T, out ShipVerifySideEffectOut) map[string]any {
		t.Helper()
		b, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("json.Unmarshal(%s): %v", b, err)
		}
		return m
	}

	t.Run("has-side-effect no --expected", func(t *testing.T) {
		out, err := shipVerifySideEffect(dir, ShipVerifySideEffectIn{Step: "version"})
		if err != nil {
			t.Fatalf("shipVerifySideEffect: %v", err)
		}
		m := decode(t, out)
		v, ok := m["expected"]
		if !ok {
			t.Fatal(`"expected" key missing, want it present with value null`)
		}
		if v != nil {
			t.Errorf(`"expected" = %v, want null`, v)
		}
	})

	t.Run("has-side-effect with --expected", func(t *testing.T) {
		out, err := shipVerifySideEffect(dir, ShipVerifySideEffectIn{Step: "version", Expected: "v1.2.3"})
		if err != nil {
			t.Fatalf("shipVerifySideEffect: %v", err)
		}
		m := decode(t, out)
		if v, ok := m["expected"]; !ok || v != "v1.2.3" {
			t.Errorf(`"expected" = %v (present=%v), want "v1.2.3"`, v, ok)
		}
	})

	t.Run("no-side-effect", func(t *testing.T) {
		out, err := shipVerifySideEffect(dir, ShipVerifySideEffectIn{Step: "commit"})
		if err != nil {
			t.Fatalf("shipVerifySideEffect: %v", err)
		}
		m := decode(t, out)
		if _, ok := m["expected"]; ok {
			t.Errorf(`"expected" key present = %v, want it absent entirely`, m["expected"])
		}
		if _, ok := m["sideEffect"]; ok {
			t.Errorf(`"sideEffect" key present = %v, want it absent entirely`, m["sideEffect"])
		}
	})
}

// TestShipVerifySideEffect_VersionTagMissing verifies the "version" step
// reports landed:false when the expected tag does not exist.
func TestShipVerifySideEffect_VersionTagMissing(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	gitTag(t, dir, "v1.2.3")

	out, err := shipVerifySideEffect(dir, ShipVerifySideEffectIn{Step: "version", Expected: "v9.9.9"})
	if err != nil {
		t.Fatalf("shipVerifySideEffect: %v", err)
	}
	if out.Landed {
		t.Error("Landed = true, want false")
	}
	if out.Expected == nil || *out.Expected != "v9.9.9" {
		t.Errorf("Expected = %v, want v9.9.9", out.Expected)
	}
}
