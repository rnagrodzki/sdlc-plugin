package hooks

import (
	"os"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ---------------------------------------------------------------------------
// preCompactSave / saveCompactRecovery
//
// stopStateSave (stop_hooks_test.go) shares the exact same saveCompactRecovery
// core, so the shared-core behaviors below (mtime-sourced savedAt,
// session-gate no-write, ship-priority, value-preserving flags.auto) are
// tested once here via preCompactSave rather than duplicated for both
// entry points; stop_hooks_test.go covers stopStateSave's own distinct
// fast-bail and the recovery-object field derivation.
// ---------------------------------------------------------------------------

func TestPreCompactSave_SavedAtFromMtimeNotNow(t *testing.T) {
	root := gitFixture(t, "feat/pcs-mtime")
	branch := "feat/pcs-mtime"
	newShipState(t, root, branch, "s1", []any{
		map[string]any{"name": "review", "status": "in_progress"},
	}, map[string]any{"auto": true})

	st, err := state.Find(root, "ship", branch)
	if err != nil || st == nil {
		t.Fatalf("state.Find: %v", err)
	}

	// A deliberately distinctive, far-past mtime: if savedAt were sourced
	// from time.Now() instead of this file's mtime, it would not match.
	past := time.Date(2020, 1, 2, 3, 4, 5, 6*int(time.Millisecond), time.UTC)
	if err := os.Chtimes(st.Path, past, past); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	out, err := preCompactSave(HookCtx{SessionID: "s1"}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)

	data, err := state.ConsumeRecoverySidecar(root, state.SlugifyBranch(branch))
	if err != nil {
		t.Fatalf("ConsumeRecoverySidecar: %v", err)
	}
	recovery, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("recovery = %v (%T), want map[string]any", data, data)
	}

	want := past.UTC().Format(savedAtFormat)
	if recovery["savedAt"] != want {
		t.Errorf("savedAt = %v, want %v (the state file's own mtime, not time.Now())", recovery["savedAt"], want)
	}
}

func TestPreCompactSave_SessionMismatch_NoWrite(t *testing.T) {
	root := gitFixture(t, "feat/pcs-mismatch")
	branch := "feat/pcs-mismatch"
	newShipState(t, root, branch, "state-session", []any{
		map[string]any{"name": "review", "status": "in_progress"},
	}, map[string]any{"auto": true})

	out, err := preCompactSave(HookCtx{SessionID: "different-session"}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)

	data, err := state.ConsumeRecoverySidecar(root, state.SlugifyBranch(branch))
	if err != nil {
		t.Fatalf("ConsumeRecoverySidecar: %v", err)
	}
	if data != nil {
		t.Errorf("recovery sidecar = %v, want nil — a denied session gate must write nothing", data)
	}
}

func TestPreCompactSave_ShipStateTakesPriorityOverExecute(t *testing.T) {
	root := gitFixture(t, "feat/pcs-priority")
	branch := "feat/pcs-priority"

	newShipState(t, root, branch, "s1", []any{
		map[string]any{"name": "review", "status": "in_progress"},
	}, map[string]any{"auto": true})

	est, err := state.Init(root, "execute", branch, "s1")
	if err != nil {
		t.Fatal(err)
	}
	est.Data["waves"] = []any{map[string]any{"status": "completed"}}
	if err := state.Write(est); err != nil {
		t.Fatal(err)
	}

	out, err := preCompactSave(HookCtx{SessionID: "s1"}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)

	data, err := state.ConsumeRecoverySidecar(root, state.SlugifyBranch(branch))
	if err != nil {
		t.Fatal(err)
	}
	recovery, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("recovery = %v (%T), want map[string]any", data, data)
	}
	if recovery["pipeline"] != "ship-sdlc" {
		t.Errorf("pipeline = %v, want ship-sdlc (ship state must win over execute)", recovery["pipeline"])
	}
}

func TestPreCompactSave_ValuePreservingAutoFlag(t *testing.T) {
	root := gitFixture(t, "feat/pcs-auto-preserve")
	branch := "feat/pcs-auto-preserve"
	newShipState(t, root, branch, "s1", []any{
		map[string]any{"name": "review", "status": "in_progress"},
	}, map[string]any{"auto": "yes", "preset": "default", "skip": []any{"lint"}})

	out, err := preCompactSave(HookCtx{SessionID: "s1"}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)

	data, err := state.ConsumeRecoverySidecar(root, state.SlugifyBranch(branch))
	if err != nil {
		t.Fatal(err)
	}
	recovery, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("recovery = %v (%T), want map[string]any", data, data)
	}
	flags, ok := recovery["flags"].(map[string]any)
	if !ok {
		t.Fatalf("recovery.flags = %v (%T), want map[string]any", recovery["flags"], recovery["flags"])
	}
	if flags["auto"] != "yes" {
		t.Errorf(`flags.auto = %v, want "yes" (value-preserving OR, not coerced to a bool)`, flags["auto"])
	}
	if flags["preset"] != "default" {
		t.Errorf("flags.preset = %v, want default", flags["preset"])
	}
	skip, ok := flags["skip"].([]any)
	if !ok || len(skip) != 1 || skip[0] != "lint" {
		t.Errorf("flags.skip = %v, want [\"lint\"]", flags["skip"])
	}
}

func TestPreCompactSave_NeitherShipNorExecute_Silent(t *testing.T) {
	root := gitFixture(t, "feat/pcs-none")
	branch := "feat/pcs-none"

	out, err := preCompactSave(HookCtx{SessionID: "s1"}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)

	data, err := state.ConsumeRecoverySidecar(root, state.SlugifyBranch(branch))
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		t.Errorf("recovery sidecar = %v, want nil (nothing to save)", data)
	}
}

func TestPreCompactSave_BranchDoesNotResolve_Silent(t *testing.T) {
	dir := realPath(t, t.TempDir())
	chdir(t, dir)

	out, err := preCompactSave(HookCtx{SessionID: "s1"}, Event{})
	if err != nil {
		t.Fatal(err)
	}
	assertSilent(t, out)
}
