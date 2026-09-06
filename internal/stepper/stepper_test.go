package stepper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Additive test file: not in the task-33 fact sheet's Files list, but
// permitted as additive coverage for the package's core primitives.

func TestEnvelope_JSONKeyNames(t *testing.T) {
	// Task 46 (skills loop) branches on these key names literally — this
	// test guards against an accidental rename.
	env := Pending("/tmp/x.json", map[string]any{"n": 1}, map[string]any{"k": "v"})
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"status", "step", "llm_decision", "state_file", "progress", "ext"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("envelope JSON missing frozen key %q: %s", key, string(b))
		}
	}
	if _, ok := raw["error"]; ok {
		t.Errorf("error key should be omitted (omitempty) on a non-error envelope: %s", string(b))
	}
}

func TestNewError_IncludesErrorKey(t *testing.T) {
	env := NewError("/tmp/x.json", "boom")
	b, _ := json.Marshal(env)
	var raw map[string]any
	json.Unmarshal(b, &raw)
	if raw["error"] != "boom" {
		t.Errorf("error = %v, want boom", raw["error"])
	}
	if raw["status"] != "error" {
		t.Errorf("status = %v, want error", raw["status"])
	}
}

func TestNewStateFilePath(t *testing.T) {
	p1, err := NewStateFilePath("myskill")
	if err != nil {
		t.Fatalf("NewStateFilePath: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(p1), "myskill-") {
		t.Errorf("path %q does not start with skill prefix", p1)
	}
	if !strings.HasSuffix(p1, ".json") {
		t.Errorf("path %q does not end in .json", p1)
	}
	if filepath.Clean(filepath.Dir(p1)) != filepath.Clean(os.TempDir()) {
		t.Errorf("path %q not under os.TempDir() (%q)", p1, os.TempDir())
	}

	p2, err := NewStateFilePath("myskill")
	if err != nil {
		t.Fatalf("NewStateFilePath: %v", err)
	}
	if p1 == p2 {
		t.Errorf("expected distinct paths across calls, got %q twice", p1)
	}
}

func TestPollState_TimedOutAndWaitedSeconds(t *testing.T) {
	st := NewPollState("s", 10, 1)
	if st.TimedOut() {
		t.Errorf("freshly created state should not be timed out")
	}
	if st.WaitedSeconds() < 0 {
		t.Errorf("waited seconds should be non-negative, got %d", st.WaitedSeconds())
	}

	st.StartedAt = time.Now().Add(-20 * time.Second).Unix()
	if !st.TimedOut() {
		t.Errorf("state started 20s ago with 10s timeout should be timed out")
	}
	if st.WaitedSeconds() < 19 {
		t.Errorf("waited seconds = %d, want >= 19", st.WaitedSeconds())
	}
}

func TestSaveAndLoadPollState_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	want := NewPollState("s", 60, 5)
	want.Iteration = 3
	want.Exhausted = true

	if err := SavePollState(path, want); err != nil {
		t.Fatalf("SavePollState: %v", err)
	}
	got, err := LoadPollState(path)
	if err != nil {
		t.Fatalf("LoadPollState: %v", err)
	}
	if got != want {
		t.Errorf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestLoadPollState_MissingFile(t *testing.T) {
	_, err := LoadPollState(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err == nil {
		t.Fatal("expected error loading a nonexistent state file, got nil")
	}
}
