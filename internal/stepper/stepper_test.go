package stepper

import (
	"encoding/json"
	"errors"
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
	dir := filepath.Join(t.TempDir(), "nested", "scratch")
	p1, err := NewStateFilePath(dir, "myskill")
	if err != nil {
		t.Fatalf("NewStateFilePath: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(p1), "myskill-") {
		t.Errorf("path %q does not start with skill prefix", p1)
	}
	if !strings.HasSuffix(p1, ".json") {
		t.Errorf("path %q does not end in .json", p1)
	}
	if filepath.Clean(filepath.Dir(p1)) != filepath.Clean(dir) {
		t.Errorf("path %q not under dir %q", p1, dir)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Errorf("dir %q not created: %v", dir, err)
	}

	p2, err := NewStateFilePath(dir, "myskill")
	if err != nil {
		t.Fatalf("NewStateFilePath: %v", err)
	}
	if p1 == p2 {
		t.Errorf("expected distinct paths across calls, got %q twice", p1)
	}
}

// writeAgedFile writes a file at dir/name and sets its modification time to
// age before now.
func writeAgedFile(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	mt := time.Now().Add(-age)
	if err := os.Chtimes(p, mt, mt); err != nil {
		t.Fatalf("chtimes %s: %v", p, err)
	}
	return p
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestNewStateFilePath_SweepsOldOwnFiles(t *testing.T) {
	dir := t.TempDir()
	old1 := writeAgedFile(t, dir, "myskill-aaa.json", 25*time.Hour)
	old2 := writeAgedFile(t, dir, "myskill-bbb.json", 48*time.Hour)
	young := writeAgedFile(t, dir, "myskill-ccc.json", time.Hour)
	otherSkill := writeAgedFile(t, dir, "otherskill-ddd.json", 48*time.Hour)
	notJSON := writeAgedFile(t, dir, "myskill-eee.txt", 48*time.Hour)
	oldDir := filepath.Join(dir, "myskill-fff.json")
	if err := os.Mkdir(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(oldDir, mt, mt)

	if _, err := NewStateFilePath(dir, "myskill"); err != nil {
		t.Fatalf("NewStateFilePath: %v", err)
	}
	for _, p := range []string{old1, old2} {
		if exists(p) {
			t.Errorf("old own file %s not removed", p)
		}
	}
	for _, p := range []string{young, otherSkill, notJSON, oldDir} {
		if !exists(p) {
			t.Errorf("%s removed, want kept", p)
		}
	}
}

// Sweep failure default row "remove of one old entry fails": that entry
// stays, the sweep continues, and the call still returns a path.
func TestNewStateFilePath_RemoveFailureKeepsGoing(t *testing.T) {
	dir := t.TempDir()
	stuck := writeAgedFile(t, dir, "myskill-aaa.json", 48*time.Hour)
	other1 := writeAgedFile(t, dir, "myskill-bbb.json", 48*time.Hour)
	other2 := writeAgedFile(t, dir, "myskill-ccc.json", 48*time.Hour)

	orig := removeStateFile
	t.Cleanup(func() { removeStateFile = orig })
	removeStateFile = func(p string) error {
		if p == stuck {
			return errors.New("injected remove failure")
		}
		return orig(p)
	}

	p, err := NewStateFilePath(dir, "myskill")
	if err != nil {
		t.Fatalf("NewStateFilePath: %v", err)
	}
	if p == "" {
		t.Fatal("NewStateFilePath returned an empty path")
	}
	if !exists(stuck) {
		t.Errorf("%s removed, want kept after the injected failure", stuck)
	}
	for _, o := range []string{other1, other2} {
		if exists(o) {
			t.Errorf("old file %s not removed after another remove failed", o)
		}
	}
}

// Sweep failure default row "ReadDir of the folder fails": the sweep stops,
// and the call succeeds.
func TestNewStateFilePath_ReadDirFailureStillReturnsPath(t *testing.T) {
	dir := t.TempDir()
	old := writeAgedFile(t, dir, "myskill-aaa.json", 48*time.Hour)

	orig := readStateDir
	t.Cleanup(func() { readStateDir = orig })
	readStateDir = func(string) ([]os.DirEntry, error) {
		return nil, errors.New("injected read failure")
	}

	p, err := NewStateFilePath(dir, "myskill")
	if err != nil {
		t.Fatalf("NewStateFilePath: %v", err)
	}
	if filepath.Dir(p) != dir {
		t.Errorf("path %q not under %q", p, dir)
	}
	if !exists(old) {
		t.Errorf("%s removed although ReadDir failed", old)
	}
}

func TestNewStateFilePath_MkdirFailureWrapsErrStateFolder(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(file, "scratch")
	p, err := NewStateFilePath(dir, "myskill")
	if !errors.Is(err, ErrStateFolder) {
		t.Fatalf("err = %v, want one that wraps ErrStateFolder", err)
	}
	if p != "" {
		t.Errorf("path = %q, want empty on error", p)
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("err %q does not name the folder %q", err, dir)
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
