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

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/pipeline"
	"github.com/rnagrodzki/sdlc-plugin/internal/shipmeta"
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
// merged flags/sources map, and a state file written with the config-driven
// step scaffold (one entry per configured step — here the built-in default
// steps) plus the fields cmdInit/initState stamp (version, startedAt,
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

	wantStateDir := filepath.Join(dir, paths.DataDir, "execution")
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
	// Default steps come from shipmeta.ShipBuiltInDefaults.Steps (6 entries),
	// not the old fixed 7-step scaffold — the scaffold is now config-driven
	// (InitialShipStepsFromConfig), one entry per configured step.
	steps, ok := data["steps"].([]any)
	if !ok || len(steps) != 6 {
		t.Fatalf("steps = %v, want a 6-entry array", data["steps"])
	}
	first, _ := steps[0].(map[string]any)
	if first["name"] != "execute" || first["status"] != "pending" || first["kind"] != "tracked" {
		t.Errorf("steps[0] = %v, want {name: execute, status: pending, kind: tracked}", first)
	}

	if out.PipelineDisplay == "" {
		t.Error("PipelineDisplay is empty, want a rendered pipeline table")
	}
	if !strings.Contains(out.PipelineDisplay, "| execute |") {
		t.Errorf("PipelineDisplay = %q, want it to contain a row for %q", out.PipelineDisplay, "execute")
	}
}

// TestShipPrepare_StepScaffold_AllCanonicalSteps verifies that ship_prepare
// seeds one step entry per configured step, in configured order, correctly
// classified tracked/inline, and renders a matching PipelineDisplay table —
// exercising all 9 shipmeta.CanonicalSteps names at once (4 tracked, 5
// inline; "received-review"/"commit-fixes" are conditional-only and never
// appear in ship.steps[]/CanonicalSteps, so they cannot be exercised via
// config here).
func TestShipPrepare_StepScaffold_AllCanonicalSteps(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/all-canonical-steps")

	stepsJSON, err := json.Marshal(shipmeta.CanonicalSteps)
	if err != nil {
		t.Fatalf("marshal CanonicalSteps: %v", err)
	}
	writeFile(t, filepath.Join(dir, ".sdlc-v2", "local.json"),
		fmt.Sprintf(`{"ship": {"steps": %s}}`, stepsJSON))

	out, err := shipPrepare(dir, dir, ShipPrepareIn{
		SkipConfigCheck: true,
		SessionID:       "sess-all-steps",
	})
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

	steps, ok := data["steps"].([]any)
	if !ok || len(steps) != len(shipmeta.CanonicalSteps) {
		t.Fatalf("steps = %v, want a %d-entry array", data["steps"], len(shipmeta.CanonicalSteps))
	}

	trackedCount, inlineCount := 0, 0
	for i, raw := range steps {
		sm, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("steps[%d] not an object: %v", i, raw)
		}
		if sm["name"] != shipmeta.CanonicalSteps[i] {
			t.Errorf("steps[%d].name = %v, want %q (config order preserved)", i, sm["name"], shipmeta.CanonicalSteps[i])
		}
		if sm["status"] != "pending" {
			t.Errorf("steps[%d].status = %v, want %q", i, sm["status"], "pending")
		}
		wantKind := "inline"
		if shipmeta.IsTrackedShipStep(shipmeta.CanonicalSteps[i]) {
			wantKind = "tracked"
		}
		if sm["kind"] != wantKind {
			t.Errorf("steps[%d].kind = %v, want %q", i, sm["kind"], wantKind)
		}
		switch sm["kind"] {
		case "tracked":
			trackedCount++
		case "inline":
			inlineCount++
		}
	}
	if trackedCount != 4 || inlineCount != 5 {
		t.Errorf("tracked/inline split = %d/%d, want 4/5", trackedCount, inlineCount)
	}

	wantTable := pipeline.PipelineTable(configStepsFromScaffold(shipmeta.InitialShipStepsFromConfig(shipmeta.CanonicalSteps)))
	if out.PipelineDisplay != wantTable {
		t.Errorf("PipelineDisplay = %q, want %q", out.PipelineDisplay, wantTable)
	}
	for _, name := range shipmeta.CanonicalSteps {
		if !strings.Contains(out.PipelineDisplay, "| "+name+" |") {
			t.Errorf("PipelineDisplay missing row for %q:\n%s", name, out.PipelineDisplay)
		}
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

	writeFile(t, filepath.Join(dir, paths.DataDir, "local.json"), `{"ship": {"steps": ["commit", "review"]}}`)

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

	writeFile(t, filepath.Join(dir, paths.DataDir, "local.json"), `{"ship": {"rebase": 42}}`)

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

	execDir := filepath.Join(dir, paths.DataDir, "execution")
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

	// Steps excludes "pr" so this exercises only the informational warning,
	// not the KD-1 hard gate (TestShipPrepare_DefaultBranchPushHardGate
	// covers that — default steps include "pr" and would otherwise collide
	// with this test's default-branch setup).
	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, Steps: []string{"commit"}})
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

// TestShipPrepare_DefaultBranchPushHardGate verifies KD-1's server-side hard
// gate: running on main with the default steps (which include "pr", the
// step that performs the git push) returns a DomainError instead of the
// soft warning TestShipPrepare_OnDefaultBranchWarning exercises. No
// automation.push config can override this — the gate is unconditional.
func TestShipPrepare_DefaultBranchPushHardGate(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	_, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true})
	if err == nil {
		t.Fatal("shipPrepare: want DomainError for pr step on default branch, got nil error")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
}

// TestShipPrepare_DefaultBranchNoPRStepAllowed verifies the hard gate is
// scoped to the "pr" step specifically: a default-branch run that excludes
// "pr" from steps never pushes, so nothing is gated.
func TestShipPrepare_DefaultBranchNoPRStepAllowed(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	_, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, Steps: []string{"commit"}})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
}

// TestShipPrepare_FeatureBranchPushAllowed verifies the hard gate does not
// fire on a feature branch, even with the default steps (including "pr").
func TestShipPrepare_FeatureBranchPushAllowed(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feature/x")

	_, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
}

func TestIsDefaultBranch(t *testing.T) {
	cases := map[string]bool{
		"main":          true,
		"master":        true,
		"feature/x":     false,
		"":              false,
		"main-ish":      false,
		"trunk":         false,
		"release/1.0.0": false,
	}
	for branch, want := range cases {
		if got := isDefaultBranch(branch); got != want {
			t.Errorf("isDefaultBranch(%q) = %v, want %v", branch, got, want)
		}
	}
}

// TestMergeShipFlags_PushDefaultSupervised verifies
// pushFeatureBranchAutoApprove defaults to false under supervised mode (or
// when automation config is absent entirely) — KD-1's baseline.
func TestMergeShipFlags_PushDefaultSupervised(t *testing.T) {
	merged, sources := mergeShipFlags(ShipPrepareIn{}, map[string]any{}, map[string]any{}, map[string]any{})
	if v, _ := merged["pushFeatureBranchAutoApprove"].(bool); v != false {
		t.Errorf("pushFeatureBranchAutoApprove = %v, want false", v)
	}
	if sources["pushFeatureBranchAutoApprove"] != "default" {
		t.Errorf("sources[pushFeatureBranchAutoApprove] = %q, want %q", sources["pushFeatureBranchAutoApprove"], "default")
	}
}

// TestMergeShipFlags_PushUnattendedForcesTrue verifies automation.mode ==
// "unattended" forces pushFeatureBranchAutoApprove true when the config
// didn't explicitly set it, mirroring config.applyAutomationDefaults.
func TestMergeShipFlags_PushUnattendedForcesTrue(t *testing.T) {
	automationCfg := map[string]any{"mode": "unattended"}
	merged, _ := mergeShipFlags(ShipPrepareIn{}, map[string]any{}, map[string]any{}, automationCfg)
	if v, _ := merged["pushFeatureBranchAutoApprove"].(bool); v != true {
		t.Errorf("pushFeatureBranchAutoApprove = %v, want true", v)
	}
}

// TestMergeShipFlags_PushExplicitConfigTrue verifies an explicit
// automation.push.featureBranchAutoApprove: true survives under supervised
// mode and is attributed to "config".
func TestMergeShipFlags_PushExplicitConfigTrue(t *testing.T) {
	automationCfg := map[string]any{
		"push": map[string]any{"featureBranchAutoApprove": true},
	}
	merged, sources := mergeShipFlags(ShipPrepareIn{}, map[string]any{}, map[string]any{}, automationCfg)
	if v, _ := merged["pushFeatureBranchAutoApprove"].(bool); v != true {
		t.Errorf("pushFeatureBranchAutoApprove = %v, want true", v)
	}
	if sources["pushFeatureBranchAutoApprove"] != "config" {
		t.Errorf("sources[pushFeatureBranchAutoApprove] = %q, want %q", sources["pushFeatureBranchAutoApprove"], "config")
	}
}

// TestMergeShipFlags_PushExplicitFalseForcedTrueUnderUnattended verifies the
// known, accepted quirk (mirrors config.PushConfig's doc and Task 6's
// identical MaxWarningRate precedent): an explicit "false" is
// indistinguishable from "absent" via the bool zero value, so "unattended"
// mode still forces the value true even when config explicitly said false.
func TestMergeShipFlags_PushExplicitFalseForcedTrueUnderUnattended(t *testing.T) {
	automationCfg := map[string]any{
		"mode": "unattended",
		"push": map[string]any{"featureBranchAutoApprove": false},
	}
	merged, sources := mergeShipFlags(ShipPrepareIn{}, map[string]any{}, map[string]any{}, automationCfg)
	if v, _ := merged["pushFeatureBranchAutoApprove"].(bool); v != true {
		t.Errorf("pushFeatureBranchAutoApprove = %v, want true (unattended forces true despite explicit false)", v)
	}
	// sources still reports "config" since the key was explicitly present —
	// the mode-forced override happens after attribution, matching how
	// config.applyAutomationDefaults treats it as the resolved value, not a
	// fallback.
	if sources["pushFeatureBranchAutoApprove"] != "config" {
		t.Errorf("sources[pushFeatureBranchAutoApprove] = %q, want %q", sources["pushFeatureBranchAutoApprove"], "config")
	}
}

// TestMergeShipFlags_PushSupervisedExplicitFalseStaysFalse verifies the
// explicit-false case works normally under supervised mode (no forcing).
func TestMergeShipFlags_PushSupervisedExplicitFalseStaysFalse(t *testing.T) {
	automationCfg := map[string]any{
		"push": map[string]any{"featureBranchAutoApprove": false},
	}
	merged, _ := mergeShipFlags(ShipPrepareIn{}, map[string]any{}, map[string]any{}, automationCfg)
	if v, _ := merged["pushFeatureBranchAutoApprove"].(bool); v != false {
		t.Errorf("pushFeatureBranchAutoApprove = %v, want false", v)
	}
}

// TestShipPrepare_KD5Gate verifies the config-version gate's still-blocking
// case: schemaVersion 1 has no registered migration path to v5
// (projectMigrations only covers from 0/3/4), so configmigrate.
// MigrateWithBackup cannot auto-migrate it and the gate still short-circuits
// using the soft style (matching plan.go/commit.go): nil Go error, a
// minimal errors-only payload, and no state file written. This is distinct
// from TestShipPrepare_AutoMigratesStaleConfig, which covers a migratable
// stale config succeeding instead of failing.
func TestShipPrepare_KD5Gate(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/kd5")

	writeFile(t, filepath.Join(dir, paths.DataDir, "config.json"), `{"schemaVersion": 1}`)

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
	if out.Migration != nil {
		t.Errorf("Migration = %v, want nil on a failed migration attempt", out.Migration)
	}

	entries, _ := os.ReadDir(filepath.Join(dir, paths.DataDir, "execution"))
	if len(entries) != 0 {
		t.Errorf("execution dir has %d entries, want 0 (no state file written)", len(entries))
	}
}

// TestShipPrepare_AutoMigratesStaleConfig verifies the KD5 gate's new
// auto-migrate behavior: a stale-but-migratable config (schemaVersion 4, one
// step short of current) is migrated in place, a config.json.bak backup is
// written, and ship_prepare proceeds to initialize state normally instead of
// hard-failing — the acceptance-criteria case this task exists to add.
func TestShipPrepare_AutoMigratesStaleConfig(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/kd5-automigrate")

	writeFile(t, filepath.Join(dir, paths.DataDir, "config.json"), `{"schemaVersion": 4}`)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: false, SessionID: "sess-automigrate"})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("Errors = %v, want empty on successful auto-migration", out.Errors)
	}
	if out.StateFile == "" {
		t.Error("StateFile is empty, want state initialized despite the auto-migration")
	}
	if out.Migration == nil {
		t.Fatal("Migration is nil, want a populated MigrationReport")
	}
	if out.Migration.BackupPath == "" {
		t.Error("Migration.BackupPath is empty, want the .bak path")
	}
	if _, statErr := os.Stat(out.Migration.BackupPath); statErr != nil {
		t.Errorf("backup file not found at %s: %v", out.Migration.BackupPath, statErr)
	}
	if filepath.Base(out.Migration.BackupPath) != "config.json.bak" {
		t.Errorf("backup file = %q, want config.json.bak", filepath.Base(out.Migration.BackupPath))
	}

	backupRaw, err := os.ReadFile(out.Migration.BackupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !strings.Contains(string(backupRaw), `"schemaVersion": 4`) {
		t.Errorf("backup content = %q, want it to preserve the pre-migration schemaVersion 4 payload", backupRaw)
	}

	migratedRaw, err := os.ReadFile(filepath.Join(dir, paths.DataDir, "config.json"))
	if err != nil {
		t.Fatalf("read migrated config.json: %v", err)
	}
	if strings.Contains(string(migratedRaw), "schemaVersion") {
		t.Errorf("migrated config.json = %q, want schemaVersion field removed", migratedRaw)
	}
}

// TestShipPrepare_MissingConfig verifies the KD5 gate's missing-config case:
// a project that never ran /setup gets an actionable error naming /setup in
// the soft errors-only payload, rather than a generic or silent failure.
func TestShipPrepare_MissingConfig(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/kd5-missing")

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: false})
	if err != nil {
		t.Fatalf("shipPrepare: %v (KD5 gate must return nil error with Errors populated)", err)
	}
	if len(out.Errors) != 1 || !strings.Contains(out.Errors[0], "/setup") {
		t.Fatalf("Errors = %v, want a single error mentioning /setup", out.Errors)
	}
	if out.StateFile != "" {
		t.Errorf("StateFile = %q, want empty", out.StateFile)
	}
}

// TestShipPrepare_CurrentConfig_NoExtraIO verifies the KD5 gate's no-op
// case: an already-current config is not touched at all — no .bak backup
// and no Migration in the response.
func TestShipPrepare_CurrentConfig_NoExtraIO(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/kd5-current")

	configPath := filepath.Join(dir, paths.DataDir, "config.json")
	writeFile(t, configPath, `{}`)
	before, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config.json: %v", err)
	}

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: false, SessionID: "sess-current"})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("Errors = %v, want empty", out.Errors)
	}
	if out.Migration != nil {
		t.Errorf("Migration = %v, want nil for an already-current config", out.Migration)
	}
	if _, statErr := os.Stat(configPath + ".bak"); statErr == nil {
		t.Error("config.json.bak written for an already-current config; want zero extra I/O")
	}

	after, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config.json: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("config.json mtime changed (%v -> %v), want untouched", before.ModTime(), after.ModTime())
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

// TestShipPrepare_BumpWithoutPRStep verifies that a CLI-supplied --bump is
// rejected when the "pr" step is skipped: version diagnostics now run inside
// pr_prepare (the standalone "version" step no longer exists), so --bump has
// nothing to feed once "pr" drops out of the resolved step list.
func TestShipPrepare_BumpWithoutPRStep(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/bump-no-pr")

	out, err := shipPrepare(dir, dir, ShipPrepareIn{
		SkipConfigCheck: true,
		Steps:           []string{"commit"},
		Bump:            "minor",
	})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if len(out.Errors) == 0 {
		t.Fatal("Errors is empty, want a --bump-without-pr-step error")
	}
	found := false
	for _, e := range out.Errors {
		if strings.Contains(e, "pr step is skipped") && strings.Contains(e, `"pr"`) {
			found = true
		}
		if strings.Contains(e, "version step") {
			t.Errorf("error still references the removed version step: %q", e)
		}
	}
	if !found {
		t.Errorf("Errors = %v, want one referencing the skipped pr step", out.Errors)
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

	execDir := filepath.Join(dir, paths.DataDir, "execution")
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

	writeFile(t, filepath.Join(dir, paths.DataDir, "local.json"), `{"state": {"gc": {"ttlDays": 30}}}`)

	execDir := filepath.Join(dir, paths.DataDir, "execution")
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

	execDir := filepath.Join(dir, paths.DataDir, "execution")
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

	execDir := filepath.Join(dir, paths.DataDir, "execution")
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

	writeFile(t, filepath.Join(dir, paths.DataDir, "local.json"), `{"state": {"gc": {"ttlDays": 1}}}`)

	execDir := filepath.Join(dir, paths.DataDir, "execution")
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

	execDir := filepath.Join(dir, paths.DataDir, "execution")
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

	execDir := filepath.Join(dir, paths.DataDir, "execution")
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

	writeFile(t, filepath.Join(dir, paths.DataDir, "config.json"), `{"schemaVersion": 1}`)

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

// TestShipGC_AutoMigratesStaleConfig verifies gc mode threads the migration
// report through shipGC's dedicated return points: a stale-but-migratable
// config auto-migrates before gc runs, and the resulting MigrationReport is
// still present on the gc-shaped ShipPrepareOut (Action == "gc").
func TestShipGC_AutoMigratesStaleConfig(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	writeFile(t, filepath.Join(dir, paths.DataDir, "config.json"), `{"schemaVersion": 4}`)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: false, Gc: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if out.Action != "gc" {
		t.Errorf("Action = %q, want %q", out.Action, "gc")
	}
	if len(out.Errors) != 0 {
		t.Fatalf("Errors = %v, want empty on successful auto-migration", out.Errors)
	}
	if out.Migration == nil {
		t.Fatal("Migration is nil, want a populated MigrationReport threaded through gc mode")
	}
	if out.Migration.BackupPath == "" {
		t.Error("Migration.BackupPath is empty, want the .bak path")
	}
	if _, statErr := os.Stat(out.Migration.BackupPath); statErr != nil {
		t.Errorf("backup file not found at %s: %v", out.Migration.BackupPath, statErr)
	}
}

// ---------------------------------------------------------------------------
// mergeShipFlags tests
// ---------------------------------------------------------------------------

// TestMergeShipFlags_PreReleasePolicyAlwaysRC verifies that
// preReleasePolicy: "always-rc" in versionCfg overrides a non-cli bump
// (patch/minor/major) to "rc" when no explicit preRelease is set, and records
// the source as "config (version.preReleasePolicy)".
func TestMergeShipFlags_PreReleasePolicyAlwaysRC(t *testing.T) {
	versionCfg := map[string]any{"preReleasePolicy": "always-rc"}
	merged, sources := mergeShipFlags(ShipPrepareIn{}, map[string]any{}, versionCfg, map[string]any{})

	if b, ok := merged["bump"].(string); !ok || b != "rc" {
		t.Errorf("Flags[bump] = %v, want %q (overridden by preReleasePolicy)", merged["bump"], "rc")
	}
	if src := sources["bump"]; src != "config (version.preReleasePolicy)" {
		t.Errorf("Sources[bump] = %q, want %q", src, "config (version.preReleasePolicy)")
	}
}

// TestMergeShipFlags_PreReleasePolicyContinueRC_NoOverride verifies that
// preReleasePolicy: "continue-rc" does NOT override bump at ship time
// (continue-rc enforcement is deferred to pr_prepare diagnostics only).
func TestMergeShipFlags_PreReleasePolicyContinueRC_NoOverride(t *testing.T) {
	versionCfg := map[string]any{"preReleasePolicy": "continue-rc"}
	merged, sources := mergeShipFlags(ShipPrepareIn{}, map[string]any{}, versionCfg, map[string]any{})

	// Should resolve to the default bump (patch), not rc.
	if b, ok := merged["bump"].(string); !ok || b != "patch" {
		t.Errorf("Flags[bump] = %v, want %q (continue-rc must not override)", merged["bump"], "patch")
	}
	if src := sources["bump"]; src != "default" {
		t.Errorf("Sources[bump] = %q, want %q (policy did not fire)", src, "default")
	}
}

// TestMergeShipFlags_ExplicitPreReleaseTakesPrecedenceOverPolicy verifies
// that an explicit version.preRelease field takes precedence over
// version.preReleasePolicy, even when both are set.
func TestMergeShipFlags_ExplicitPreReleaseTakesPrecedenceOverPolicy(t *testing.T) {
	versionCfg := map[string]any{
		"preRelease":       "rc",
		"preReleasePolicy": "always-rc",
	}
	merged, sources := mergeShipFlags(ShipPrepareIn{}, map[string]any{}, versionCfg, map[string]any{})

	if b, ok := merged["bump"].(string); !ok || b != "rc" {
		t.Errorf("Flags[bump] = %v, want %q", merged["bump"], "rc")
	}
	// Explicit preRelease takes precedence over policy, so source should be
	// "config (version.preRelease)", not "config (version.preReleasePolicy)".
	if src := sources["bump"]; src != "config (version.preRelease)" {
		t.Errorf("Sources[bump] = %q, want %q (preRelease must take precedence over policy)", src, "config (version.preRelease)")
	}
}

// TestMergeShipFlags_PreReleasePolicyAlwaysRC_OverridesCLI verifies that
// preReleasePolicy: "always-rc" overrides an explicit CLI bump (e.g. "patch")
// to "rc" and records the source as enforced-over-cli.
func TestMergeShipFlags_PreReleasePolicyAlwaysRC_OverridesCLI(t *testing.T) {
	versionCfg := map[string]any{"preReleasePolicy": "always-rc"}
	merged, sources := mergeShipFlags(ShipPrepareIn{Bump: "patch"}, map[string]any{}, versionCfg, map[string]any{})

	if b, ok := merged["bump"].(string); !ok || b != "rc" {
		t.Errorf("Flags[bump] = %v, want %q (overridden by preReleasePolicy over cli)", merged["bump"], "rc")
	}
	if src := sources["bump"]; src != "config (version.preReleasePolicy enforced over cli)" {
		t.Errorf("Sources[bump] = %q, want %q", src, "config (version.preReleasePolicy enforced over cli)")
	}
}

// ---------------------------------------------------------------------------
// ship_verify_side_effect tests
// ---------------------------------------------------------------------------

// TestShipVerifySideEffect_NoSideEffectStep verifies steps with no configured
// side effect (e.g. "review") always report landed:true, reason:no-side-effect.
func TestShipVerifySideEffect_NoSideEffectStep(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "review"}, fixedNow(time.Now()))
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
		out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "commit"}, fixedNow(time.Now()))
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
		out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "commit", Expected: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}, fixedNow(time.Now()))
		if err != nil {
			t.Fatalf("shipVerifySideEffect: %v", err)
		}
		m := decode(t, out)
		if v, ok := m["expected"]; !ok || v != "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef" {
			t.Errorf(`"expected" = %v (present=%v), want "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"`, v, ok)
		}
	})

	t.Run("no-side-effect", func(t *testing.T) {
		out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "review"}, fixedNow(time.Now()))
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

// TestShipVerifySideEffect_VersionStepHasNoSideEffect verifies the "version"
// step — its release diagnostics now run inside the pr step's pr_prepare
// call, not as a standalone step — reports landed:true, reason
// "no-side-effect", regardless of Expected, since it no longer has an entry
// in shipStepSideEffects.
func TestShipVerifySideEffect_VersionStepHasNoSideEffect(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "version", Expected: "v9.9.9"}, fixedNow(time.Now()))
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

// ---------------------------------------------------------------------------
// ship_verify_side_effect: "pr" and "commit" kinds + sideEffects journal
// ---------------------------------------------------------------------------

// stubPRForBranch replaces the shipPRForBranch seam for the duration of the
// test, restoring the original (ghx.PRForBranch) on cleanup.
func stubPRForBranch(t *testing.T, meta ghx.PRMetadata) {
	t.Helper()
	orig := shipPRForBranch
	shipPRForBranch = func(string) ghx.PRMetadata { return meta }
	t.Cleanup(func() { shipPRForBranch = orig })
}

// TestShipVerifySideEffect_PRLanded verifies the "pr" step reports
// landed:true with sideEffect "pr" when a PR exists for the branch, and
// records {kind:"pr", ref:"#<number>"} in the ship state's sideEffects
// journal.
func TestShipVerifySideEffect_PRLanded(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/pr-landed")
	statePath := shipStateInitFixture(t, dir, "feat/pr-landed")
	stubPRForBranch(t, ghx.PRMetadata{Exists: true, Number: 141})

	fixedAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "pr"}, fixedNow(fixedAt))
	if err != nil {
		t.Fatalf("shipVerifySideEffect: %v", err)
	}
	if !out.Landed {
		t.Error("Landed = false, want true")
	}
	if out.SideEffect != "pr" {
		t.Errorf("SideEffect = %q, want %q", out.SideEffect, "pr")
	}

	data := readStateData(t, statePath)
	journal, ok := data["sideEffects"].(map[string]any)
	if !ok {
		t.Fatalf("sideEffects missing or wrong type: %#v", data["sideEffects"])
	}
	entry, ok := journal["pr"].(map[string]any)
	if !ok {
		t.Fatalf(`sideEffects["pr"] missing or wrong type: %#v`, journal["pr"])
	}
	if entry["kind"] != "pr" {
		t.Errorf(`kind = %v, want "pr"`, entry["kind"])
	}
	if entry["ref"] != "#141" {
		t.Errorf(`ref = %v, want "#141"`, entry["ref"])
	}
	if entry["verifiedAt"] != fixedAt.UTC().Format(time.RFC3339) {
		t.Errorf("verifiedAt = %v, want %v", entry["verifiedAt"], fixedAt.UTC().Format(time.RFC3339))
	}
}

// TestShipVerifySideEffect_PRNotFound verifies the "pr" step reports
// landed:false and writes no journal entry when no PR exists for the
// branch.
func TestShipVerifySideEffect_PRNotFound(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/pr-missing")
	statePath := shipStateInitFixture(t, dir, "feat/pr-missing")
	stubPRForBranch(t, ghx.PRMetadata{Exists: false})

	out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "pr"}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("shipVerifySideEffect: %v", err)
	}
	if out.Landed {
		t.Error("Landed = true, want false")
	}

	data := readStateData(t, statePath)
	if journal, ok := data["sideEffects"].(map[string]any); ok {
		if _, ok := journal["pr"]; ok {
			t.Errorf(`sideEffects["pr"] present, want no entry: %#v`, journal["pr"])
		}
	}
}

// TestShipVerifySideEffect_CommitSha_ExpectedMatch verifies the "commit"
// step reports landed:true and journals {kind:"sha", ref:<HEAD>} when
// Expected matches the current HEAD sha (the write-path: a caller who just
// produced a commit passes its own sha as Expected).
func TestShipVerifySideEffect_CommitSha_ExpectedMatch(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/sha-match")
	statePath := shipStateInitFixture(t, dir, "feat/sha-match")

	headSHA, err := shipHeadSHA(dir)
	if err != nil {
		t.Fatalf("shipHeadSHA: %v", err)
	}

	out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "commit", Expected: headSHA}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("shipVerifySideEffect: %v", err)
	}
	if !out.Landed {
		t.Error("Landed = false, want true")
	}
	if out.SideEffect != "sha" {
		t.Errorf("SideEffect = %q, want %q", out.SideEffect, "sha")
	}

	data := readStateData(t, statePath)
	journal := data["sideEffects"].(map[string]any)
	entry := journal["commit"].(map[string]any)
	if entry["kind"] != "sha" {
		t.Errorf(`kind = %v, want "sha"`, entry["kind"])
	}
	if entry["ref"] != headSHA {
		t.Errorf("ref = %v, want %v", entry["ref"], headSHA)
	}
}

// TestShipVerifySideEffect_CommitSha_ExpectedMismatch verifies landed:false
// (and no journal write) when Expected does not match HEAD.
func TestShipVerifySideEffect_CommitSha_ExpectedMismatch(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/sha-mismatch")
	statePath := shipStateInitFixture(t, dir, "feat/sha-mismatch")

	out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "commit", Expected: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("shipVerifySideEffect: %v", err)
	}
	if out.Landed {
		t.Error("Landed = true, want false")
	}

	data := readStateData(t, statePath)
	if journal, ok := data["sideEffects"].(map[string]any); ok {
		if _, ok := journal["commit"]; ok {
			t.Errorf(`sideEffects["commit"] present, want no entry: %#v`, journal["commit"])
		}
	}
}

// TestShipVerifySideEffect_CommitSha_NoBaseline verifies that a bare call
// (no Expected, no prior journal entry) reports landed:false rather than
// treating the ambient HEAD sha as verified — recording an unverified
// observation as "landed" would make begin-step's alreadyDone lie for a
// step that may never have actually run.
func TestShipVerifySideEffect_CommitSha_NoBaseline(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/sha-no-baseline")
	statePath := shipStateInitFixture(t, dir, "feat/sha-no-baseline")

	out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "commit"}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("shipVerifySideEffect: %v", err)
	}
	if out.Landed {
		t.Error("Landed = true, want false (no baseline to compare HEAD against)")
	}

	data := readStateData(t, statePath)
	if journal, ok := data["sideEffects"].(map[string]any); ok {
		if _, ok := journal["commit"]; ok {
			t.Errorf(`sideEffects["commit"] present, want no entry: %#v`, journal["commit"])
		}
	}
}

// TestShipVerifySideEffect_CommitSha_ResumeConfirmsJournal verifies the
// literal "checks HEAD sha vs the previously recorded sha" resume path: once
// a journal entry exists for "commit", a later bare call (no Expected)
// reports landed:true when HEAD still matches it, and landed:false once HEAD
// has moved on.
func TestShipVerifySideEffect_CommitSha_ResumeConfirmsJournal(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/sha-resume")
	statePath := shipStateInitFixture(t, dir, "feat/sha-resume")

	headSHA, err := shipHeadSHA(dir)
	if err != nil {
		t.Fatalf("shipHeadSHA: %v", err)
	}
	// Establish the baseline via the write path (Expected supplied).
	if _, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "commit", Expected: headSHA}, fixedNow(time.Now())); err != nil {
		t.Fatalf("shipVerifySideEffect (establish): %v", err)
	}

	// Resume check, same HEAD: confirmed.
	out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "commit"}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("shipVerifySideEffect (resume, same HEAD): %v", err)
	}
	if !out.Landed {
		t.Error("Landed = false, want true (HEAD matches journaled sha)")
	}

	// A new commit lands; resume check against the stale journal entry.
	gitCommit(t, dir, "second")
	out, err = shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "commit"}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("shipVerifySideEffect (resume, moved HEAD): %v", err)
	}
	if out.Landed {
		t.Error("Landed = true, want false (HEAD has moved past the journaled sha)")
	}

	data := readStateData(t, statePath)
	journal := data["sideEffects"].(map[string]any)
	entry := journal["commit"].(map[string]any)
	if entry["ref"] != headSHA {
		t.Errorf("ref = %v, want unchanged %v (only landed:true calls refresh the journal)", entry["ref"], headSHA)
	}
}

// TestShipStateSchema_SideEffectsKindEnum proves AC4's schema-level
// enforcement: ship-state.schema.json must accept a sideEffects entry with a
// valid kind ("pr"/"sha") and reject one with an unrecognized kind, via the
// enum restriction — not merely something application code happens to
// filter out.
func TestShipStateSchema_SideEffectsKindEnum(t *testing.T) {
	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc", "schemas", "ship-state.schema.json"))
	if err != nil {
		t.Fatal(err)
	}

	c := jsonschema.NewCompiler()
	sch, err := c.Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	baseState := func(sideEffects map[string]any) map[string]any {
		return map[string]any{
			"version":   float64(1),
			"startedAt": "2026-03-01T12:00:00Z",
			"branch":    "feat/schema-test",
			"flags":     map[string]any{},
			"steps": []any{
				map[string]any{"name": "execute", "status": "completed"},
			},
			"sideEffects": sideEffects,
		}
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

	t.Run("valid kind accepted", func(t *testing.T) {
		doc := baseState(map[string]any{
			"pr": map[string]any{
				"kind":       "pr",
				"ref":        "42",
				"verifiedAt": "2026-03-01T12:00:00Z",
			},
		})
		if err := validate(t, doc); err != nil {
			t.Errorf("expected valid sideEffects entry to pass schema validation, got: %v", err)
		}
	})

	t.Run("unknown kind rejected", func(t *testing.T) {
		doc := baseState(map[string]any{
			"version": map[string]any{
				"kind":       "bogus",
				"ref":        "v0.22.0",
				"verifiedAt": "2026-03-01T12:00:00Z",
			},
		})
		if err := validate(t, doc); err == nil {
			t.Error("expected schema validation to reject unknown sideEffects kind, got nil error")
		}
	})

	t.Run("release-intent kind rejected", func(t *testing.T) {
		// release-intent was removed from the enum when the standalone
		// version tool was retired (its side effects now record as
		// pr/sha only); prove the schema enforces the removal.
		doc := baseState(map[string]any{
			"version": map[string]any{
				"kind":       "release-intent",
				"ref":        "minor",
				"verifiedAt": "2026-03-01T12:00:00Z",
			},
		})
		if err := validate(t, doc); err == nil {
			t.Error("expected schema validation to reject release-intent sideEffects kind, got nil error")
		}
	})
}
