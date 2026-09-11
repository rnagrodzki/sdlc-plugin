package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// sha256File (unit)
// ---------------------------------------------------------------------------

func TestSha256File(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "plan.md")
	writeFile(t, p, "hello plan")

	got, err := sha256File(p)
	if err != nil {
		t.Fatalf("sha256File: unexpected error: %v", err)
	}
	sum := sha256.Sum256([]byte("hello plan"))
	want := hex.EncodeToString(sum[:])
	if got != want {
		t.Errorf("sha256File: got %q, want %q", got, want)
	}
}

func TestSha256File_MissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := sha256File(filepath.Join(dir, "does-not-exist.md"))
	if err == nil {
		t.Fatal("sha256File: expected error for missing file, got nil")
	}
}

// ---------------------------------------------------------------------------
// wave-start planHash comparison (KD-5, acceptance criteria)
// ---------------------------------------------------------------------------
//
// execActionInit stores planPath/planHash verbatim (via nilIfEmptyStr) on
// st.Data at init. wave-start re-derives the plan file's sha256 on every
// call and compares it against the stored hash before the wave is looked up
// or created — a mismatch must leave the wave untouched.

// planFileWithHash writes content to a plan file under dir and returns both
// the file's path and its sha256 hex digest, for seeding state fixtures.
func planFileWithHash(t *testing.T, dir, content string) (path, hash string) {
	t.Helper()
	path = filepath.Join(dir, "plan.md")
	writeFile(t, path, content)
	sum := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(sum[:])
}

func TestExecState_WaveStart_PlanHashMatch(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)
	planPath, hash := planFileWithHash(t, root, "plan v1")

	createExecState(t, root, "feat/test", map[string]any{
		"branch":   "feat/test",
		"planPath": planPath,
		"planHash": hash,
		"waves":    []any{},
		"context":  map[string]any{},
	})

	out, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-start",
		Branch: "feat/test",
		Wave:   intPtr(1),
	}, clock)
	if err != nil {
		t.Fatalf("wave-start: unexpected error for matching planHash: %v", err)
	}
	result, ok := out.(ExecWaveNarrationOut)
	if !ok {
		t.Fatalf("wave-start: expected ExecWaveNarrationOut, got %T", out)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("wave-start: Warnings = %v, want none for matching planHash", result.Warnings)
	}

	data := readExecState(t, root, "feat/test")
	waves, _ := data["waves"].([]any)
	if len(waves) != 1 {
		t.Fatalf("wave-start: expected 1 wave to be started, got %d", len(waves))
	}
	if issues, _ := data["issues"].([]any); len(issues) != 0 {
		t.Errorf("wave-start: issues = %v, want none for matching planHash", issues)
	}
}

func TestExecState_WaveStart_PlanHashMismatch(t *testing.T) {
	root := t.TempDir()
	clock := fixedClock(testNow)
	planPath, staleHash := planFileWithHash(t, root, "plan v1")
	// Mutate the plan file after init recorded staleHash — simulates the
	// plan changing mid-run.
	writeFile(t, planPath, "plan v2, edited after init")

	createExecState(t, root, "feat/test", map[string]any{
		"branch":   "feat/test",
		"planPath": planPath,
		"planHash": staleHash,
		"waves":    []any{},
		"context":  map[string]any{},
	})

	out, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-start",
		Branch: "feat/test",
		Wave:   intPtr(1),
	}, clock)
	if err != nil {
		t.Fatalf("wave-start: unexpected error for mismatched planHash: %v", err)
	}
	result, ok := out.(DriftLogOut)
	if !ok {
		t.Fatalf("wave-start: expected DriftLogOut, got %T (%v)", out, out)
	}
	if !result.Halt {
		t.Error("wave-start: Halt = false, want true for mismatched planHash")
	}
	if result.Reason != "plan hash mismatch" {
		t.Errorf("wave-start: Reason = %q, want %q", result.Reason, "plan hash mismatch")
	}

	data := readExecState(t, root, "feat/test")
	waves, _ := data["waves"].([]any)
	if len(waves) != 0 {
		t.Errorf("wave-start: waves = %v, want untouched empty slice — wave must not start on a drift halt", waves)
	}

	issues, _ := data["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("wave-start: expected 1 drift issue recorded, got %d", len(issues))
	}
	issue, ok := issues[0].(map[string]any)
	if !ok {
		t.Fatalf("wave-start: issue entry is %T, want map[string]any", issues[0])
	}
	if wave, _ := issue["wave"].(float64); int(wave) != 1 {
		t.Errorf("wave-start: issue wave = %v, want 1", issue["wave"])
	}
	if issue["category"] != "drift" {
		t.Errorf("wave-start: issue category = %v, want %q", issue["category"], "drift")
	}
	if issue["severity"] != "error" {
		t.Errorf("wave-start: issue severity = %v, want %q", issue["severity"], "error")
	}
	if issue["summary"] != "plan content changed since init" {
		t.Errorf("wave-start: issue summary = %v, want %q", issue["summary"], "plan content changed since init")
	}
}

func TestExecState_WaveStart_PlanHashEmpty(t *testing.T) {
	// No planHash recorded at all (pre-KD-5 state, or init without a plan
	// file) — the comparison must not run, and wave-start proceeds exactly
	// as it did before KD-5.
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"branch":  "feat/test",
		"waves":   []any{},
		"context": map[string]any{},
	})

	out, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-start",
		Branch: "feat/test",
		Wave:   intPtr(1),
	}, clock)
	if err != nil {
		t.Fatalf("wave-start: unexpected error with no planHash recorded: %v", err)
	}
	result, ok := out.(ExecWaveNarrationOut)
	if !ok {
		t.Fatalf("wave-start: expected ExecWaveNarrationOut, got %T", out)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("wave-start: Warnings = %v, want none when planHash is unset", result.Warnings)
	}

	data := readExecState(t, root, "feat/test")
	waves, _ := data["waves"].([]any)
	if len(waves) != 1 {
		t.Fatalf("wave-start: expected 1 wave to be started, got %d", len(waves))
	}
}

func TestExecState_WaveStart_PlanPathMissingFile(t *testing.T) {
	// planHash is recorded but the file it points at can no longer be read
	// (moved, deleted, permissions). This is not treated as drift — the
	// comparison is skipped, a warning is surfaced, and the wave still
	// starts normally.
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"branch":   "feat/test",
		"planPath": filepath.Join(root, "no-such-plan.md"),
		"planHash": "deadbeef",
		"waves":    []any{},
		"context":  map[string]any{},
	})

	out, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-start",
		Branch: "feat/test",
		Wave:   intPtr(1),
	}, clock)
	if err != nil {
		t.Fatalf("wave-start: unexpected error for missing planPath file: %v", err)
	}
	result, ok := out.(ExecWaveNarrationOut)
	if !ok {
		t.Fatalf("wave-start: expected ExecWaveNarrationOut, got %T", out)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("wave-start: Warnings = %v, want exactly 1 warning for an unreadable planPath", result.Warnings)
	}

	data := readExecState(t, root, "feat/test")
	waves, _ := data["waves"].([]any)
	if len(waves) != 1 {
		t.Fatalf("wave-start: expected wave to start despite missing planPath file, got %d waves", len(waves))
	}
	if issues, _ := data["issues"].([]any); len(issues) != 0 {
		t.Errorf("wave-start: issues = %v, want none — an unreadable file is a warning, not a drift issue", issues)
	}
}

func TestExecState_WaveStart_PlanHashSetNoPlanPath(t *testing.T) {
	// planHash was recorded but planPath is empty — init received a hash
	// without a path (or the field was cleared). Same treatment as a
	// missing file: skip with a warning, proceed normally.
	root := t.TempDir()
	clock := fixedClock(testNow)

	createExecState(t, root, "feat/test", map[string]any{
		"branch":   "feat/test",
		"planHash": "deadbeef",
		"waves":    []any{},
		"context":  map[string]any{},
	})

	out, err := executeState(root, root, ExecuteStateIn{
		Action: "wave-start",
		Branch: "feat/test",
		Wave:   intPtr(1),
	}, clock)
	if err != nil {
		t.Fatalf("wave-start: unexpected error for planHash with no planPath: %v", err)
	}
	result, ok := out.(ExecWaveNarrationOut)
	if !ok {
		t.Fatalf("wave-start: expected ExecWaveNarrationOut, got %T", out)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("wave-start: Warnings = %v, want exactly 1 warning when planPath is empty", result.Warnings)
	}

	data := readExecState(t, root, "feat/test")
	waves, _ := data["waves"].([]any)
	if len(waves) != 1 {
		t.Fatalf("wave-start: expected wave to start despite empty planPath, got %d waves", len(waves))
	}
}
