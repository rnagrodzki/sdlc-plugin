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

	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
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

	wantStateDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
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
// exercising all 10 shipmeta.CanonicalSteps names at once (4 tracked, 6
// inline, harden included; "received-review"/"commit-fixes" are conditional-only and never
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
	writeFile(t, filepath.Join(dir, ".sdlc-v2", "local.toml"),
		fmt.Sprintf("[ship]\nsteps = %s\n", stepsJSON))

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
	if trackedCount != 4 || inlineCount != 6 {
		t.Errorf("tracked/inline split = %d/%d, want 4/6", trackedCount, inlineCount)
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
// .sdlc-v2/local.toml is picked up with Sources["steps"] == "config" (ship is
// not a project section, so its config lives in local.toml, not config.toml).
func TestShipPrepare_StepsFromConfig(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/config-steps")

	writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), `[ship]
steps = ["commit", "review"]
`)

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

	writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), `[ship]
rebase = 42
`)

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

	execDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
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

// TestShipPushGateBaseBranch covers the KD-1 push gate's extension to a
// configured [git] baseBranch, alongside the existing hardcoded main/master
// coverage: the gate must fire the same *mcpserver.DomainError, with the
// same message/suggestion shape, whether it's triggered by main/master or by
// a configured baseBranch — and must NOT fire merely because a baseBranch is
// configured while the current branch is something else.
func TestShipPushGateBaseBranch(t *testing.T) {
	wantDomainErr := func(t *testing.T, err error, branch string) {
		t.Helper()
		if err == nil {
			t.Fatalf("shipPrepare: want DomainError for pr step on branch %q, got nil error", branch)
		}
		domainErr, ok := err.(*mcpserver.DomainError)
		if !ok {
			t.Fatalf("expected DomainError, got %T: %v", err, err)
		}
		wantMsg := fmt.Sprintf("ship cannot run the \"pr\" step on default branch %q — pushing to main/master is never auto-approved", branch)
		if domainErr.Msg != wantMsg {
			t.Errorf("Msg = %q, want %q", domainErr.Msg, wantMsg)
		}
		wantSuggestion := "Switch to a feature branch, or remove \"pr\" from --steps/ship.steps[] if you don't intend to push."
		if domainErr.Suggestion != wantSuggestion {
			t.Errorf("Suggestion = %q, want %q", domainErr.Suggestion, wantSuggestion)
		}
	}

	t.Run("configured baseBranch matching current branch fires the gate", func(t *testing.T) {
		dir := t.TempDir()
		initGitFixture(t, dir)
		gitCommit(t, dir, "initial")
		checkoutBranch(t, dir, "develop")
		writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), "[git]\nbaseBranch = \"develop\"\n")

		_, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true})
		wantDomainErr(t, err, "develop")
	})

	t.Run("main still blocked with no baseBranch configured", func(t *testing.T) {
		dir := t.TempDir()
		initGitFixture(t, dir)
		gitCommit(t, dir, "initial")

		_, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true})
		wantDomainErr(t, err, "main")
	})

	t.Run("configured baseBranch not matching current branch does not fire", func(t *testing.T) {
		dir := t.TempDir()
		initGitFixture(t, dir)
		gitCommit(t, dir, "initial")
		checkoutBranch(t, dir, "feature/x")
		writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), "[git]\nbaseBranch = \"develop\"\n")

		_, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true})
		if err != nil {
			t.Fatalf("shipPrepare: %v", err)
		}
	})
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

// TestShipPrepare_KD5Gate verifies the config-version gate's blocking case:
// a JSON-era config.json with no config.toml is stale by definition (the
// TOML migration removed JSON->TOML auto-migration entirely — see
// TestShipPrepare_StaleConfigRequiresSetup), so the gate short-circuits
// using the soft style (matching plan.go/commit.go): nil Go error, a
// minimal errors-only payload naming /setup, and no state file written.
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
	if !strings.Contains(out.Errors[0], "/setup") {
		t.Errorf("Errors[0] = %q, want it to mention /setup", out.Errors[0])
	}
	if out.StateFile != "" {
		t.Errorf("StateFile = %q, want empty (KD5 gate must not init state)", out.StateFile)
	}

	entries, _ := os.ReadDir(filepath.Join(dir, paths.DataDir, paths.RunsSubdir))
	if len(entries) != 0 {
		t.Errorf("execution dir has %d entries, want 0 (no state file written)", len(entries))
	}
}

// TestShipPrepare_StaleConfigRequiresSetup verifies the KD5 gate on a
// JSON-era config with no config.toml present: the TOML migration removed
// JSON->TOML auto-migration entirely (configmigrate no longer has any
// migration steps to run), so ship_prepare must report an actionable
// /setup error via the soft errors-only payload instead of silently
// migrating in place.
//
// This replaces the former TestShipPrepare_AutoMigratesStaleConfig, which
// asserted the pre-TOML behavior (auto-migrate with a config.json.bak
// backup, threaded through as a MigrationReport) — a capability that no
// longer exists once .sdlc-v2/config.toml is the only source of truth.
func TestShipPrepare_StaleConfigRequiresSetup(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/kd5-stale")

	// Legacy JSON-era config, no config.toml: must not be silently migrated.
	writeFile(t, filepath.Join(dir, paths.DataDir, "config.json"), `{"schemaVersion": 4}`)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: false, SessionID: "sess-stale"})
	if err != nil {
		t.Fatalf("shipPrepare: %v (KD5 gate must return nil error with Errors populated)", err)
	}
	if len(out.Errors) == 0 {
		t.Fatal("Errors is empty, want a config-version error naming /setup")
	}
	if !strings.Contains(out.Errors[0], "/setup") {
		t.Errorf("Errors[0] = %q, want it to mention /setup", out.Errors[0])
	}
	if out.StateFile != "" {
		t.Errorf("StateFile = %q, want empty (stale config must not init state)", out.StateFile)
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
// case: an already-current config is not touched at all — no .bak backup.
func TestShipPrepare_CurrentConfig_NoExtraIO(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/kd5-current")

	configPath := filepath.Join(dir, paths.DataDir, "config.toml")
	writeFile(t, configPath, "")
	writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), "")
	before, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config.toml: %v", err)
	}

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: false, SessionID: "sess-current"})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("Errors = %v, want empty", out.Errors)
	}
	if _, statErr := os.Stat(configPath + ".bak"); statErr == nil {
		t.Error("config.toml.bak written for an already-current config; want zero extra I/O")
	}

	after, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat config.toml: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("config.toml mtime changed (%v -> %v), want untouched", before.ModTime(), after.ModTime())
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

// TestShipPrepare_InvalidReviewThreshold verifies that a ship.reviewThreshold
// outside dimensions.ValidSeverities is a hard error that blocks state init,
// while every valid severity — "info" included — is accepted, and an unset
// value resolves to the "info" default.
func TestShipPrepare_InvalidReviewThreshold(t *testing.T) {
	cases := []struct {
		name      string
		config    string // [ship] body; empty means no reviewThreshold key
		wantErr   bool
		wantValue string
		wantSrc   string
	}{
		{name: "invalid", config: `reviewThreshold = "severe"`, wantErr: true},
		{name: "uppercase", config: `reviewThreshold = "LOW"`, wantErr: true},
		{name: "info", config: `reviewThreshold = "info"`, wantValue: "info", wantSrc: "config"},
		{name: "low", config: `reviewThreshold = "low"`, wantValue: "low", wantSrc: "config"},
		{name: "unset", config: "", wantValue: "info", wantSrc: "default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			initGitFixture(t, dir)
			gitCommit(t, dir, "initial")
			checkoutBranch(t, dir, "feat/threshold-"+tc.name)
			writeFile(t, filepath.Join(dir, ".sdlc-v2", "local.toml"),
				"[ship]\nsteps = [\"commit\", \"pr\"]\n"+tc.config+"\n")

			out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true})
			if err != nil {
				t.Fatalf("shipPrepare: %v", err)
			}

			var thresholdErrs []string
			for _, e := range out.Errors {
				if strings.Contains(e, "reviewThreshold") {
					thresholdErrs = append(thresholdErrs, e)
				}
			}
			if !tc.wantErr {
				if len(thresholdErrs) != 0 {
					t.Fatalf("Errors = %v, want no reviewThreshold error", out.Errors)
				}
				if got := out.Flags["reviewThreshold"]; got != tc.wantValue {
					t.Errorf("Flags[reviewThreshold] = %v, want %q", got, tc.wantValue)
				}
				if got := out.Sources["reviewThreshold"]; got != tc.wantSrc {
					t.Errorf("Sources[reviewThreshold] = %q, want %q", got, tc.wantSrc)
				}
				return
			}
			if len(thresholdErrs) == 0 {
				t.Fatalf("Errors = %v, want a reviewThreshold error", out.Errors)
			}
			for _, sev := range dimensions.ValidSeverities {
				if !strings.Contains(thresholdErrs[0], sev) {
					t.Errorf("error %q does not list valid severity %q", thresholdErrs[0], sev)
				}
			}
			if out.StateFile != "" {
				t.Errorf("StateFile = %q, want empty (validation errors block state init)", out.StateFile)
			}
			matches, _ := filepath.Glob(filepath.Join(dir, paths.DataDir, paths.RunsSubdir, "ship-*.json"))
			if len(matches) != 0 {
				t.Errorf("ship state files = %v, want none", matches)
			}
		})
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

// TestShipPrepare_ConditionalStepRejected verifies that the conditional
// steps "received-review" and "commit-fixes" are a hard error from every
// source — config ship.steps[], config ship.quick[] and --steps — and never
// a warning. Before the fix a config-sourced conditional step only warned,
// and ship_prepare seeded it as a "tracked" state entry that nothing ever
// dispatches.
func TestShipPrepare_ConditionalStepRejected(t *testing.T) {
	cases := []struct {
		name    string
		step    string
		config  string // local.toml body
		in      ShipPrepareIn
		wantSrc string
		label   string
	}{
		{
			name:    "config steps received-review",
			step:    "received-review",
			config:  "[ship]\nsteps = [\"commit\", \"received-review\"]\n",
			wantSrc: "config",
			label:   "steps[]",
		},
		{
			name:    "config steps commit-fixes",
			step:    "commit-fixes",
			config:  "[ship]\nsteps = [\"commit\", \"commit-fixes\"]\n",
			wantSrc: "config",
			label:   "steps[]",
		},
		{
			name:    "config quick received-review",
			step:    "received-review",
			config:  "[ship]\nquick = [\"commit\", \"received-review\"]\n",
			in:      ShipPrepareIn{Quick: true},
			wantSrc: "quick",
			label:   "steps[]",
		},
		{
			name:    "cli commit-fixes",
			step:    "commit-fixes",
			in:      ShipPrepareIn{Steps: []string{"commit", "commit-fixes"}},
			wantSrc: "cli",
			label:   "--steps",
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			initGitFixture(t, dir)
			gitCommit(t, dir, "initial")
			checkoutBranch(t, dir, fmt.Sprintf("feat/conditional-step-%d", i))
			if tc.config != "" {
				writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), tc.config)
			}

			in := tc.in
			in.SkipConfigCheck = true
			out, err := shipPrepare(dir, dir, in)
			if err != nil {
				t.Fatalf("shipPrepare: %v", err)
			}
			if got := out.Sources["steps"]; got != tc.wantSrc {
				t.Fatalf("Sources[steps] = %q, want %q", got, tc.wantSrc)
			}

			var stepErrs []string
			for _, e := range out.Errors {
				if strings.Contains(e, `"`+tc.step+`"`) {
					stepErrs = append(stepErrs, e)
				}
			}
			if len(stepErrs) != 1 {
				t.Fatalf("Errors = %v, want exactly one entry naming %q", out.Errors, tc.step)
			}
			if !strings.Contains(stepErrs[0], "conditional step") || !strings.Contains(stepErrs[0], "in "+tc.label) {
				t.Errorf("error %q, want it to call %q a conditional step in %s", stepErrs[0], tc.step, tc.label)
			}
			if !strings.Contains(stepErrs[0], "Valid values: "+strings.Join(shipmeta.ValidSteps, ", ")) {
				t.Errorf("error %q does not name the allowed steps", stepErrs[0])
			}
			for _, w := range out.Warnings {
				if strings.Contains(w, tc.step) {
					t.Errorf("Warnings contains %q, want it only in Errors", w)
				}
			}
			if out.StateFile != "" {
				t.Errorf("StateFile = %q, want empty (validation errors block state init)", out.StateFile)
			}
			matches, _ := filepath.Glob(filepath.Join(dir, paths.DataDir, paths.RunsSubdir, "ship-*.json"))
			if len(matches) != 0 {
				t.Errorf("ship state files = %v, want none", matches)
			}
		})
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

	execDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
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

	writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), `[state.gc]
ttlDays = 30
`)

	execDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
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

	execDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
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

	execDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
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

	writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), `[state.gc]
ttlDays = 1
`)

	execDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
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

	execDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
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

// TestGCBranchExistsFunc_GitFailureAssumesLive pins that a failed
// `git branch --list` (here: not a git repository) yields "every branch is
// live", not "every branch is gone". Otherwise one git failure makes gc
// delete every state file.
func TestGCBranchExistsFunc_GitFailureAssumesLive(t *testing.T) {
	dir := t.TempDir() // not a git repository: git branch --list fails

	branchExists := gcBranchExistsFunc(dir)
	if branchExists == nil {
		t.Fatal("gcBranchExistsFunc returned nil, want a non-nil func")
	}
	if !branchExists("any-branch") {
		t.Error(`branchExists("any-branch") = false after git failure, want true (unknown liveness must not delete state)`)
	}
}

// TestGCBranchExistsFunc_UnbornHeadAssumesLive pins that an empty branch
// list (a repository with no commits yet lists no branches) is treated as
// unknown liveness, not as "every branch is gone".
func TestGCBranchExistsFunc_UnbornHeadAssumesLive(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir) // no commit: HEAD is unborn, git branch --list prints nothing

	if !gcBranchExistsFunc(dir)("main") {
		t.Error(`branchExists("main") = false on an unborn HEAD, want true`)
	}
}

// TestShipGC_BranchListFailureKeepsStateFiles is the end-to-end guard for
// gcBranchExistsFunc's failure path: a stale state file must survive gc when
// branch liveness cannot be read.
func TestShipGC_BranchListFailureKeepsStateFiles(t *testing.T) {
	dir := t.TempDir() // not a git repository

	execDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
	f := filepath.Join(execDir, "ship-some-branch-20200101T000000Z.json")
	writeFile(t, f, `{"sessionId": null}`)
	setStateFileMtime(t, f, 30*24*time.Hour)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, Gc: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if out.Report == nil {
		t.Fatalf("Report is nil; errors: %v", out.Errors)
	}
	if len(out.Report.Ship.Deleted) != 0 {
		t.Errorf("Ship.Deleted = %v, want none (branch liveness unknown)", out.Report.Ship.Deleted)
	}
	if _, err := os.Stat(f); err != nil {
		t.Errorf("state file %s should still exist: %v", f, err)
	}
}

// TestShipGC_BucketsByPrefix verifies stale, dead-branch files across all
// four state prefixes are each routed into their own report bucket.
func TestShipGC_BucketsByPrefix(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	execDir := filepath.Join(dir, paths.DataDir, paths.RunsSubdir)
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
	if !strings.Contains(out.Errors[0], "/setup") {
		t.Errorf("Errors[0] = %q, want it to mention /setup", out.Errors[0])
	}
	if out.Action == "gc" {
		t.Errorf("Action = %q, want empty (KD5 gate must short-circuit before gc runs)", out.Action)
	}
}

// TestShipGC_StaleConfigRequiresSetup verifies gc mode does not bypass the
// KD5 gate's /setup error: a JSON-era config.json with no config.toml is
// stale by definition (JSON->TOML auto-migration no longer exists), so gc
// must report the error via the gc-shaped ShipPrepareOut with Action still
// empty, rather than proceeding.
//
// This replaces the former TestShipGC_AutoMigratesStaleConfig, which
// asserted the pre-TOML auto-migrate-with-backup behavior threaded through
// shipGC's return points.
func TestShipGC_StaleConfigRequiresSetup(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	writeFile(t, filepath.Join(dir, paths.DataDir, "config.json"), `{"schemaVersion": 4}`)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: false, Gc: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v (KD5 gate must return nil error)", err)
	}
	if len(out.Errors) == 0 {
		t.Fatal("Errors is empty, want a config-version error naming /setup")
	}
	if !strings.Contains(out.Errors[0], "/setup") {
		t.Errorf("Errors[0] = %q, want it to mention /setup", out.Errors[0])
	}
	if out.Action == "gc" {
		t.Errorf("Action = %q, want empty (KD5 gate must short-circuit before gc runs)", out.Action)
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
	merged, sources := mergeShipFlags(ShipPrepareIn{}, map[string]any{}, versionCfg)

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
	merged, sources := mergeShipFlags(ShipPrepareIn{}, map[string]any{}, versionCfg)

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
	merged, sources := mergeShipFlags(ShipPrepareIn{}, map[string]any{}, versionCfg)

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
	merged, sources := mergeShipFlags(ShipPrepareIn{Bump: "patch"}, map[string]any{}, versionCfg)

	if b, ok := merged["bump"].(string); !ok || b != "rc" {
		t.Errorf("Flags[bump] = %v, want %q (overridden by preReleasePolicy over cli)", merged["bump"], "rc")
	}
	if src := sources["bump"]; src != "config (version.preReleasePolicy enforced over cli)" {
		t.Errorf("Sources[bump] = %q, want %q", src, "config (version.preReleasePolicy enforced over cli)")
	}
}

// TestMergeShipFlags_PreReleasePolicyDefaultRC_NoCLI verifies that
// preReleasePolicy: "default-rc" overrides a non-cli bump to "rc" when no
// explicit CLI --bump was supplied, same as "always-rc" would.
func TestMergeShipFlags_PreReleasePolicyDefaultRC_NoCLI(t *testing.T) {
	versionCfg := map[string]any{"preReleasePolicy": "default-rc"}
	merged, sources := mergeShipFlags(ShipPrepareIn{}, map[string]any{}, versionCfg)

	if b, ok := merged["bump"].(string); !ok || b != "rc" {
		t.Errorf("Flags[bump] = %v, want %q (overridden by default-rc)", merged["bump"], "rc")
	}
	if src := sources["bump"]; src != "config (version.preReleasePolicy)" {
		t.Errorf("Sources[bump] = %q, want %q", src, "config (version.preReleasePolicy)")
	}
}

// TestMergeShipFlags_PreReleasePolicyDefaultRC_CLIOverrides verifies that
// preReleasePolicy: "default-rc" leaves an explicit CLI --bump patch alone
// (CLI wins under default-rc, unlike always-rc).
func TestMergeShipFlags_PreReleasePolicyDefaultRC_CLIOverrides(t *testing.T) {
	versionCfg := map[string]any{"preReleasePolicy": "default-rc"}
	merged, sources := mergeShipFlags(ShipPrepareIn{Bump: "patch"}, map[string]any{}, versionCfg)

	if b, ok := merged["bump"].(string); !ok || b != "patch" {
		t.Errorf("Flags[bump] = %v, want %q (default-rc must not override an explicit cli bump)", merged["bump"], "patch")
	}
	if src := sources["bump"]; src != "cli" {
		t.Errorf("Sources[bump] = %q, want %q", src, "cli")
	}
}

// TestMergeShipFlags_PreReleasePolicyDefaultRC_CLIMinor verifies that
// preReleasePolicy: "default-rc" leaves an explicit CLI --bump minor alone.
func TestMergeShipFlags_PreReleasePolicyDefaultRC_CLIMinor(t *testing.T) {
	versionCfg := map[string]any{"preReleasePolicy": "default-rc"}
	merged, sources := mergeShipFlags(ShipPrepareIn{Bump: "minor"}, map[string]any{}, versionCfg)

	if b, ok := merged["bump"].(string); !ok || b != "minor" {
		t.Errorf("Flags[bump] = %v, want %q (default-rc must not override an explicit cli bump)", merged["bump"], "minor")
	}
	if src := sources["bump"]; src != "cli" {
		t.Errorf("Sources[bump] = %q, want %q", src, "cli")
	}
}

// TestMergeShipFlags_ExecuteDispatchArgs_QualityAbsent verifies that when no
// --quality was supplied, executeDispatchArgs omits --quality entirely
// (rather than interpolating a bare/empty --quality flag) and still includes
// --wave-timeout/--wave-interval computed from the merged flags.
func TestMergeShipFlags_ExecuteDispatchArgs_QualityAbsent(t *testing.T) {
	cfg := map[string]any{"executeWaveTimeout": 900, "executeWaveInterval": 30}
	merged, _ := mergeShipFlags(ShipPrepareIn{}, cfg, map[string]any{})

	want := "--wave-timeout 900 --wave-interval 30"
	if got, _ := merged["executeDispatchArgs"].(string); got != want {
		t.Errorf("Flags[executeDispatchArgs] = %q, want %q", got, want)
	}
}

// TestMergeShipFlags_ExecuteDispatchArgs_QualityPresent verifies that when
// --quality was supplied via CLI, executeDispatchArgs leads with
// "--quality <value>" ahead of the wave-timeout/wave-interval flags.
func TestMergeShipFlags_ExecuteDispatchArgs_QualityPresent(t *testing.T) {
	cfg := map[string]any{"executeWaveTimeout": 900, "executeWaveInterval": 30}
	merged, _ := mergeShipFlags(ShipPrepareIn{Quality: "full"}, cfg, map[string]any{})

	want := "--quality full --wave-timeout 900 --wave-interval 30"
	if got, _ := merged["executeDispatchArgs"].(string); got != want {
		t.Errorf("Flags[executeDispatchArgs] = %q, want %q", got, want)
	}
}

// TestMergeShipFlags_CommitWaves_DefaultTrueNotForwarded verifies the fixed
// dead default (was false, must be true) and that an unconfigured
// ship.execute.commitWaves forwards no --commit-waves flag at all —
// execute's own top-level execute.commitWaves config must decide instead.
func TestMergeShipFlags_CommitWaves_DefaultTrueNotForwarded(t *testing.T) {
	merged, sources := mergeShipFlags(ShipPrepareIn{}, map[string]any{}, map[string]any{})

	if v, _ := merged["executeCommitWaves"].(bool); !v {
		t.Errorf("Flags[executeCommitWaves] = %v, want true", merged["executeCommitWaves"])
	}
	if src := sources["executeCommitWaves"]; src != "default" {
		t.Errorf("Sources[executeCommitWaves] = %q, want %q", src, "default")
	}
	if got, _ := merged["executeDispatchArgs"].(string); strings.Contains(got, "--commit-waves") {
		t.Errorf("executeDispatchArgs = %q, want no --commit-waves for an unconfigured default", got)
	}
}

// TestMergeShipFlags_CommitWaves_ConfigForwarded verifies that an explicitly
// configured ship.execute.commitWaves is both merged with source "config"
// and forwarded as --commit-waves in executeDispatchArgs.
func TestMergeShipFlags_CommitWaves_ConfigForwarded(t *testing.T) {
	cfg := map[string]any{"execute": map[string]any{"commitWaves": false}}
	merged, sources := mergeShipFlags(ShipPrepareIn{}, cfg, map[string]any{})

	if v, _ := merged["executeCommitWaves"].(bool); v {
		t.Errorf("Flags[executeCommitWaves] = %v, want false", merged["executeCommitWaves"])
	}
	if src := sources["executeCommitWaves"]; src != "config" {
		t.Errorf("Sources[executeCommitWaves] = %q, want %q", src, "config")
	}
	want := "--wave-timeout 1800 --wave-interval 60 --commit-waves false"
	if got, _ := merged["executeDispatchArgs"].(string); got != want {
		t.Errorf("executeDispatchArgs = %q, want %q", got, want)
	}
}

// TestMergeShipFlags_CommitWaves_InvalidType verifies the wrong-typed-value
// path: the dead default's fix must land here too (defaults to true, not
// false), and an invalid type never sets source to "config", so it is never
// forwarded to execute.
func TestMergeShipFlags_CommitWaves_InvalidType(t *testing.T) {
	cfg := map[string]any{"execute": map[string]any{"commitWaves": "nope"}}
	merged, sources := mergeShipFlags(ShipPrepareIn{}, cfg, map[string]any{})

	if v, _ := merged["executeCommitWaves"].(bool); !v {
		t.Errorf("Flags[executeCommitWaves] = %v, want true", merged["executeCommitWaves"])
	}
	if src := sources["executeCommitWaves"]; src != "default" {
		t.Errorf("Sources[executeCommitWaves] = %q, want %q", src, "default")
	}
	if invalid, _ := merged["commitWavesInvalidType"].(bool); !invalid {
		t.Error("Flags[commitWavesInvalidType] = false, want true")
	}
	if got, _ := merged["executeDispatchArgs"].(string); strings.Contains(got, "--commit-waves") {
		t.Errorf("executeDispatchArgs = %q, want no --commit-waves for an invalid-type config value", got)
	}
}

// TestShipPrepare_CommitWavesInvalidTypeWarns verifies the warning text at
// the shipPrepare level says "defaulting to true", matching the dead
// default's fix — it previously said "defaulting to false", which was wrong
// twice over: the merged default is true, and the warning must describe it.
func TestShipPrepare_CommitWavesInvalidTypeWarns(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	writeFile(t, filepath.Join(dir, paths.DataDir, "local.toml"), `[ship.execute]
commitWaves = "nope"
`)

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, Steps: []string{"commit"}})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	want := "execute.commitWaves in ship config is not a boolean — value ignored, defaulting to true. Set it to true or false explicitly."
	found := false
	for _, w := range out.Warnings {
		if w == want {
			found = true
		}
	}
	if !found {
		t.Errorf("Warnings = %v, want to contain %q", out.Warnings, want)
	}
}

// TestMergeShipFlags_ReviewThreshold_ConfigOverDefault pins the precedence of
// ship.reviewThreshold: a configured value wins and reports source "config";
// an absent or empty value falls back to the built-in default and reports
// source "default". The default's value is pinned separately by
// TestShippedReviewThresholdDefaultsAgree in internal/config.
func TestMergeShipFlags_ReviewThreshold_ConfigOverDefault(t *testing.T) {
	def := shipmeta.ShipBuiltInDefaults.ReviewThreshold
	cases := []struct {
		name       string
		cfg        map[string]any
		wantValue  string
		wantSource string
	}{
		{"configured value wins over the default", map[string]any{"reviewThreshold": "critical"}, "critical", "config"},
		{"absent key falls back to the default", map[string]any{}, def, "default"},
		{"empty string falls back to the default", map[string]any{"reviewThreshold": ""}, def, "default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged, sources := mergeShipFlags(ShipPrepareIn{}, tc.cfg, map[string]any{})
			if got, _ := merged["reviewThreshold"].(string); got != tc.wantValue {
				t.Errorf("Flags[reviewThreshold] = %q, want %q", got, tc.wantValue)
			}
			if got := sources["reviewThreshold"]; got != tc.wantSource {
				t.Errorf("Sources[reviewThreshold] = %q, want %q", got, tc.wantSource)
			}
		})
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

// TestShipVerifySideEffect_ExpectedEcho checks the Expected field the
// renderer prints: nil when the caller passed none, the caller's value when
// one was passed. The rendered shape itself (including the "(none)" line and
// the field staying visible) is pinned by tests/integration's
// TestShipVerifySideEffect_RenderedFields.
func TestShipVerifySideEffect_ExpectedEcho(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	t.Run("no expected value", func(t *testing.T) {
		out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "commit"}, fixedNow(time.Now()))
		if err != nil {
			t.Fatalf("shipVerifySideEffect: %v", err)
		}
		if out.Expected != nil {
			t.Errorf("Expected = %q, want nil", *out.Expected)
		}
	})

	t.Run("with expected value", func(t *testing.T) {
		const sha = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
		out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "commit", Expected: sha}, fixedNow(time.Now()))
		if err != nil {
			t.Fatalf("shipVerifySideEffect: %v", err)
		}
		if out.Expected == nil || *out.Expected != sha {
			t.Errorf("Expected = %v, want %q", out.Expected, sha)
		}
	})

	t.Run("no side effect leaves expected nil", func(t *testing.T) {
		out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "review"}, fixedNow(time.Now()))
		if err != nil {
			t.Fatalf("shipVerifySideEffect: %v", err)
		}
		if out.Expected != nil {
			t.Errorf("Expected = %q, want nil", *out.Expected)
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
	stubPRForBranch(t, ghx.PRMetadata{Exists: true, Number: 141, State: "OPEN"})

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

// TestShipVerifySideEffect_PRClosedOrMergedNotLanded pins that only an open
// PR counts as the pr step's side effect. With no open PR, gh pr view falls
// back to the branch's newest closed or merged PR; an older PR on a reused
// branch must not make a resumed pipeline skip opening a new one.
func TestShipVerifySideEffect_PRClosedOrMergedNotLanded(t *testing.T) {
	for _, prState := range []string{"CLOSED", "MERGED"} {
		t.Run(prState, func(t *testing.T) {
			dir := t.TempDir()
			initGitFixture(t, dir)
			gitCommit(t, dir, "initial")
			checkoutBranch(t, dir, "feat/pr-old")
			statePath := shipStateInitFixture(t, dir, "feat/pr-old")
			stubPRForBranch(t, ghx.PRMetadata{Exists: true, Number: 7, State: prState})

			out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "pr"}, fixedNow(time.Now()))
			if err != nil {
				t.Fatalf("shipVerifySideEffect: %v", err)
			}
			if out.Landed {
				t.Errorf("Landed = true for a %s PR, want false", prState)
			}

			data := readStateData(t, statePath)
			if journal, ok := data["sideEffects"].(map[string]any); ok {
				if _, ok := journal["pr"]; ok {
					t.Errorf(`sideEffects["pr"] present, want no entry: %#v`, journal["pr"])
				}
			}
		})
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

// TestShipVerifySideEffect_CommitSha_SecondCommitGetsOwnKey verifies that,
// with commit twice in steps[], verifying while the second commit is in
// progress writes sideEffects["commit#2"] and leaves the first commit's
// sideEffects["commit"] entry unchanged.
func TestShipVerifySideEffect_CommitSha_SecondCommitGetsOwnKey(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	branch := "feat/sha-second-commit"
	checkoutBranch(t, dir, branch)
	statePath := shipStateInitFixture(t, dir, branch)
	setSteps(t, statePath, "commit", "harden", "commit")

	firstSHA, err := shipHeadSHA(dir)
	if err != nil {
		t.Fatalf("shipHeadSHA: %v", err)
	}
	for _, c := range []struct{ action, step string }{
		{"begin-step", "commit"}, {"complete-step", "commit"},
		{"begin-step", "harden"}, {"complete-step", "harden"},
	} {
		if c.action == "complete-step" && c.step == "commit" {
			if _, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "commit", Expected: firstSHA}, fixedNow(time.Now())); err != nil {
				t.Fatalf("verify first commit: %v", err)
			}
		}
		if _, err := shipState(dir, dir, ShipStateIn{Action: c.action, Step: c.step, Detail: map[string]any{"branch": branch}}, fixedNow(time.Now())); err != nil {
			t.Fatalf("%s %s: %v", c.action, c.step, err)
		}
	}

	gitCommit(t, dir, "harden edits")
	secondSHA, err := shipHeadSHA(dir)
	if err != nil {
		t.Fatalf("shipHeadSHA: %v", err)
	}
	if _, err := shipState(dir, dir, ShipStateIn{Action: "begin-step", Step: "commit", Detail: map[string]any{"branch": branch}}, fixedNow(time.Now())); err != nil {
		t.Fatalf("begin-step second commit: %v", err)
	}
	out, err := shipVerifySideEffect(dir, dir, ShipVerifySideEffectIn{Step: "commit", Expected: secondSHA}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("verify second commit: %v", err)
	}
	if !out.Landed {
		t.Fatal("Landed = false, want true")
	}

	journal := readStateData(t, statePath)["sideEffects"].(map[string]any)
	if ref := journal["commit"].(map[string]any)["ref"]; ref != firstSHA {
		t.Errorf(`sideEffects["commit"].ref = %v, want first commit %v`, ref, firstSHA)
	}
	second, ok := journal["commit#2"].(map[string]any)
	if !ok {
		t.Fatalf(`sideEffects["commit#2"] missing: %#v`, journal)
	}
	if second["ref"] != secondSHA {
		t.Errorf(`sideEffects["commit#2"].ref = %v, want %v`, second["ref"], secondSHA)
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

// TestShipBuildReportData_ReviewLedger pins the review ledger arithmetic:
// fixed counts only local-review records, deferred counts every
// deferredFindings entry by reason (a missing reason is below-threshold),
// and unaccounted is total - fixed - deferred without clamping.
func TestShipBuildReportData_ReviewLedger(t *testing.T) {
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)

	t.Run("no review total", func(t *testing.T) {
		for name, data := range map[string]map[string]any{
			"no healing":            {},
			"healing without total": {"healing": map[string]any{"fixed": []any{map[string]any{"origin": "local-review"}}}},
			"non-integer total":     {"healing": map[string]any{"reviewTotal": map[string]any{"total": "7"}}},
		} {
			rd := shipBuildReportData(data, now)
			if rd.ReviewLedger != nil {
				t.Errorf("%s: ReviewLedger = %+v, want nil", name, rd.ReviewLedger)
			}
			if rd.ReviewLedgerNote != "review did not run or its total was not recorded" {
				t.Errorf("%s: ReviewLedgerNote = %q", name, rd.ReviewLedgerNote)
			}
			if rd.Healing == nil {
				t.Errorf("%s: Healing is nil, want a non-nil map", name)
			}
		}
		raw, err := json.Marshal(shipBuildReportData(map[string]any{}, now))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"reviewLedger":null`, `"healing":{}`, `"reviewLedgerNote":"review did not run or its total was not recorded"`} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("JSON %s does not contain %s", raw, want)
			}
		}
	})

	t.Run("counts", func(t *testing.T) {
		healing := map[string]any{
			"reviewTotal": map[string]any{"total": float64(10), "dimensions": float64(4)},
			"fixed": []any{
				map[string]any{"origin": "local-review", "title": "a"},
				map[string]any{"origin": "local-review", "title": "b"},
				map[string]any{"origin": "pr-comment", "title": "c"},
			},
		}
		data := map[string]any{
			"healing": healing,
			"deferredFindings": []any{
				map[string]any{"title": "d", "reason": "needs-direction"},
				map[string]any{"title": "e", "reason": "wont-fix"},
				map[string]any{"title": "f", "reason": "wont-fix"},
				map[string]any{"title": "g"}, // recorded before the reason field
			},
		}
		rd := shipBuildReportData(data, now)
		want := &ShipReviewLedger{
			Total: 10, Fixed: 2,
			DeferredByReason: map[string]int{"needs-direction": 1, "wont-fix": 2, "below-threshold": 1},
			Unaccounted:      4,
		}
		if rd.ReviewLedger == nil || rd.ReviewLedger.Total != want.Total || rd.ReviewLedger.Fixed != want.Fixed ||
			rd.ReviewLedger.Unaccounted != want.Unaccounted || fmt.Sprint(rd.ReviewLedger.DeferredByReason) != fmt.Sprint(want.DeferredByReason) {
			t.Errorf("ReviewLedger = %+v, want %+v", rd.ReviewLedger, want)
		}
		if rd.ReviewLedgerNote != "" {
			t.Errorf("ReviewLedgerNote = %q, want empty", rd.ReviewLedgerNote)
		}
		if fixed, _ := rd.Healing["fixed"].([]any); len(fixed) != 3 {
			t.Errorf("Healing = %v, want data.healing verbatim", rd.Healing)
		}
	})

	t.Run("negative unaccounted is not clamped", func(t *testing.T) {
		data := map[string]any{
			"healing": map[string]any{
				"reviewTotal": map[string]any{"total": 1},
				"fixed":       []any{map[string]any{"origin": "local-review"}, map[string]any{"origin": "local-review"}},
			},
			"deferredFindings": []any{map[string]any{"reason": "disagree"}},
		}
		rd := shipBuildReportData(data, now)
		if rd.ReviewLedger == nil || rd.ReviewLedger.Unaccounted != -2 {
			t.Errorf("ReviewLedger = %+v, want Unaccounted -2", rd.ReviewLedger)
		}
	})

	t.Run("zero total with nothing deferred", func(t *testing.T) {
		rd := shipBuildReportData(map[string]any{"healing": map[string]any{"reviewTotal": map[string]any{"total": float64(0)}}}, now)
		want := ShipReviewLedger{DeferredByReason: map[string]int{}}
		if rd.ReviewLedger == nil || rd.ReviewLedger.Total != 0 || rd.ReviewLedger.Unaccounted != 0 ||
			rd.ReviewLedger.DeferredByReason == nil || len(rd.ReviewLedger.DeferredByReason) != 0 {
			t.Errorf("ReviewLedger = %+v, want %+v", rd.ReviewLedger, want)
		}
	})
}

// ---------------------------------------------------------------------------
// ship_prepare: openspec change from the plan, and materialize
//
// These reuse execute_state_test.go's materialize fixtures
// (writeMaterializeStaging, stubOpenspecForMaterialize, matPlanHeaders).
// ---------------------------------------------------------------------------

// shipMaterializeFixture returns a git repo on a feature branch with a plan
// file holding planContent, plus the plan's path.
func shipMaterializeFixture(t *testing.T, planContent string) (dir, planPath string) {
	t.Helper()
	dir = t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/openspec")
	planPath = filepath.Join(dir, "plan.md")
	writeFile(t, planPath, planContent)
	return dir, planPath
}

// TestShipPrepareMaterialize_Created: a staged plan, not dry-run, creates
// openspec/changes/<c>/, stages it in git, reports materialized:"created",
// and still initializes ship state.
func TestShipPrepareMaterialize_Created(t *testing.T) {
	dir, planPath := shipMaterializeFixture(t, matPlanHeaders("add-widget"))
	stubOpenspecForMaterialize(t)
	writeMaterializeStaging(t, dir, "add-widget", map[string]string{"tasks.md": "- [ ] First task\n"})

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, PlanFile: planPath})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("Errors = %v, want empty", out.Errors)
	}
	if out.Openspec == nil || *out.Openspec != (ShipOpenspecOut{Change: "add-widget", Materialized: "created"}) {
		t.Fatalf("Openspec = %+v, want {add-widget created}", out.Openspec)
	}
	if _, err := os.Stat(filepath.Join(dir, "openspec", "changes", "add-widget", "tasks.md")); err != nil {
		t.Errorf("materialized tasks.md missing: %v", err)
	}
	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = dir
	staged, gitErr := cmd.Output()
	if gitErr != nil {
		t.Fatalf("git diff --cached: %v", gitErr)
	}
	if !strings.Contains(string(staged), "openspec/changes/add-widget/tasks.md") {
		t.Errorf("git index = %q, want openspec/changes/add-widget/tasks.md staged", staged)
	}
	if out.StateFile == "" {
		t.Error("StateFile is empty, want ship state initialized after materialize")
	}
}

// TestShipPrepareMaterialize_TargetDiffers: a Materialize failure (rule 5,
// target exists and differs from staging) lands in errors and no ship-state
// file is written.
func TestShipPrepareMaterialize_TargetDiffers(t *testing.T) {
	dir, planPath := shipMaterializeFixture(t, matPlanHeaders("add-widget"))
	targetDir := filepath.Join(dir, "openspec", "changes", "add-widget")
	writeFile(t, filepath.Join(targetDir, ".openspec.yaml"), "schema: spec-driven\n")
	writeFile(t, filepath.Join(targetDir, "tasks.md"), "- [ ] Different\n")
	writeMaterializeStaging(t, dir, "add-widget", map[string]string{"tasks.md": "- [ ] First task\n"})

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, PlanFile: planPath})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if len(out.Errors) != 1 || !strings.Contains(out.Errors[0], "already exists and differs from staging") {
		t.Fatalf("Errors = %v, want one materialize target-differs error", out.Errors)
	}
	if strings.HasPrefix(out.Errors[0], "init: ") {
		t.Errorf("Errors[0] = %q, want no execute_state \"init: \" prefix", out.Errors[0])
	}
	if out.StateFile != "" || out.Openspec != nil {
		t.Errorf("StateFile = %q, Openspec = %+v, want both empty", out.StateFile, out.Openspec)
	}
	st, findErr := state.Find(dir, "ship", "feat/openspec")
	if findErr != nil {
		t.Fatalf("state.Find: %v", findErr)
	}
	if st != nil {
		t.Fatalf("ship state file exists at %s after materialize error; want none", st.Path)
	}
}

// TestShipPrepareMaterialize_DryRun: dryRun reports materialized:"dry-run"
// and Materialize writes nothing — the target dir does not exist and the
// staging dir is untouched.
func TestShipPrepareMaterialize_DryRun(t *testing.T) {
	dir, planPath := shipMaterializeFixture(t, matPlanHeaders("add-widget"))
	writeMaterializeStaging(t, dir, "add-widget", map[string]string{"tasks.md": "- [ ] First task\n"})

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, PlanFile: planPath, DryRun: true})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("Errors = %v, want empty", out.Errors)
	}
	if out.Openspec == nil || *out.Openspec != (ShipOpenspecOut{Change: "add-widget", Materialized: "dry-run"}) {
		t.Fatalf("Openspec = %+v, want {add-widget dry-run}", out.Openspec)
	}
	if _, err := os.Stat(filepath.Join(dir, "openspec", "changes", "add-widget")); !os.IsNotExist(err) {
		t.Errorf("openspec/changes/add-widget stat err = %v, want not-exist (dry-run must not materialize)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".sdlc-v2", "openspec-staging", "add-widget", "tasks.md")); err != nil {
		t.Errorf("staging tasks.md stat err = %v, want it kept", err)
	}
}

// TestShipPrepareMaterialize_NoStagingHeader: a plan without an
// **OpenSpec-Staging:** header leaves Openspec nil, so the JSON output has
// no "openspec" key.
func TestShipPrepareMaterialize_NoStagingHeader(t *testing.T) {
	dir, planPath := shipMaterializeFixture(t, "# Plan\n\n**Source:** conversation context\n")

	out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, PlanFile: planPath})
	if err != nil {
		t.Fatalf("shipPrepare: %v", err)
	}
	if out.Openspec != nil {
		t.Fatalf("Openspec = %+v, want nil", out.Openspec)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), `"openspec"`) {
		t.Errorf("JSON output has an \"openspec\" key; want it omitted: %s", raw)
	}
}

// TestShipPrepareOpenspecChangeFromPlan covers openspecChange resolution:
// plan Source fills it when no input is given; an explicit input wins and
// warns when it differs; no input and no Source leaves it nil.
func TestShipPrepareOpenspecChangeFromPlan(t *testing.T) {
	sourceOnly := "# Plan\n\n**Source:** openspec/changes/add-widget/\n"
	mismatchWarning := `openspecChange "other-change" differs from plan Source "add-widget"; using "other-change"`

	cases := []struct {
		name        string
		plan        string
		input       string
		wantChange  any
		wantSource  string // "" means no sources entry
		wantWarning bool
	}{
		{name: "plan source", plan: sourceOnly, wantChange: "add-widget", wantSource: "plan"},
		{name: "input wins and warns", plan: sourceOnly, input: "other-change", wantChange: "other-change", wantSource: "cli", wantWarning: true},
		{name: "input matches plan", plan: sourceOnly, input: "add-widget", wantChange: "add-widget", wantSource: "cli"},
		{name: "no input no source", plan: "# Plan\n", wantChange: nil},
		{name: "unsafe plan source ignored", plan: "# Plan\n\n**Source:** openspec/changes/../\n", wantChange: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, planPath := shipMaterializeFixture(t, tc.plan)
			out, err := shipPrepare(dir, dir, ShipPrepareIn{SkipConfigCheck: true, PlanFile: planPath, OpenspecChange: tc.input})
			if err != nil {
				t.Fatalf("shipPrepare: %v", err)
			}
			if len(out.Errors) != 0 {
				t.Fatalf("Errors = %v, want empty", out.Errors)
			}
			if v, present := out.Flags["openspecChange"]; !present || v != tc.wantChange {
				t.Errorf("Flags[openspecChange] = %v (present %v), want %v", v, present, tc.wantChange)
			}
			if got := out.Sources["openspecChange"]; got != tc.wantSource {
				t.Errorf("Sources[openspecChange] = %q, want %q", got, tc.wantSource)
			}
			hasWarning := false
			for _, w := range out.Warnings {
				if w == mismatchWarning {
					hasWarning = true
				}
			}
			if hasWarning != tc.wantWarning {
				t.Errorf("mismatch warning present = %v, want %v; warnings = %v", hasWarning, tc.wantWarning, out.Warnings)
			}
		})
	}
}
