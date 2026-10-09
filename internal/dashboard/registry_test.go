package dashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// isolateCacheDir points paths.CacheDir() at a throwaway temp directory, so
// registry tests never touch the real developer's ~/.sdlc-cache.
func isolateCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SDLC_CACHE_DIR", dir)
	return dir
}

// mkRepoRoot creates a throwaway directory with a .sdlc-v2 subdirectory, so
// it passes Roots' "still has a .sdlc-v2 folder" check.
func mkRepoRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, paths.DataDir), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return root
}

// TestLogPath pins the server log path that Ensure spawns with and that a
// cache clear truncates: server.log in the dashboard directory of the cache.
func TestLogPath(t *testing.T) {
	dir := isolateCacheDir(t)
	if got, want := LogPath(), filepath.Join(dir, "dashboard", "server.log"); got != want {
		t.Errorf("LogPath() = %q, want %q", got, want)
	}
}

func hashedRootFile(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:])[:16] + ".json"
}

func TestRegisterRootWritesHashedFile(t *testing.T) {
	isolateCacheDir(t)
	root := mkRepoRoot(t)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	if err := RegisterRoot(root, now); err != nil {
		t.Fatalf("RegisterRoot: unexpected error: %v", err)
	}

	path := filepath.Join(rootsDir(), hashedRootFile(root))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var got Root
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	want := Root{Root: root, LastSeen: now}
	if got.Root != want.Root || !got.LastSeen.Equal(want.LastSeen) {
		t.Errorf("file contents = %+v, want %+v", got, want)
	}
}

func TestRegisterRootSameRootRefreshesSameFile(t *testing.T) {
	isolateCacheDir(t)
	root := mkRepoRoot(t)
	first := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	second := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)

	if err := RegisterRoot(root, first); err != nil {
		t.Fatalf("RegisterRoot (first): unexpected error: %v", err)
	}
	if err := RegisterRoot(root, second); err != nil {
		t.Fatalf("RegisterRoot (second): unexpected error: %v", err)
	}

	entries, err := os.ReadDir(rootsDir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("roots dir has %d entries, want 1 (two sessions of the same repo must write one file)", len(entries))
	}

	roots, err := Roots(second)
	if err != nil {
		t.Fatalf("Roots: unexpected error: %v", err)
	}
	if len(roots) != 1 || !roots[0].LastSeen.Equal(second) {
		t.Errorf("Roots() = %+v, want one entry with lastSeen %v", roots, second)
	}
}

func TestRootsNeverNilWhenRootsDirAbsent(t *testing.T) {
	isolateCacheDir(t)

	got, err := Roots(time.Now())
	if err != nil {
		t.Fatalf("Roots: unexpected error: %v", err)
	}
	if got == nil {
		t.Error("Roots() = nil, want a non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("Roots() = %+v, want empty", got)
	}
}

func TestRootsKeepsLiveRoot(t *testing.T) {
	isolateCacheDir(t)
	root := mkRepoRoot(t)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	if err := RegisterRoot(root, now); err != nil {
		t.Fatalf("RegisterRoot: unexpected error: %v", err)
	}

	got, err := Roots(now)
	if err != nil {
		t.Fatalf("Roots: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Root != root {
		t.Errorf("Roots() = %+v, want [{Root: %q}]", got, root)
	}
}

func TestRootsDropsRootWithNoDataDirAndDeletesFile(t *testing.T) {
	isolateCacheDir(t)
	// A root directory with no .sdlc-v2 folder: the repo was removed, or
	// never had one.
	root := t.TempDir()
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	if err := RegisterRoot(root, now); err != nil {
		t.Fatalf("RegisterRoot: unexpected error: %v", err)
	}
	path := filepath.Join(rootsDir(), hashedRootFile(root))

	got, err := Roots(now)
	if err != nil {
		t.Fatalf("Roots: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Roots() = %+v, want empty (root has no .sdlc-v2 folder)", got)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("file %s still exists, want it deleted", path)
	}
}

func TestRootsDropsStaleRootAndDeletesFile(t *testing.T) {
	isolateCacheDir(t)
	root := mkRepoRoot(t)
	registeredAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	checkAt := registeredAt.Add(8 * 24 * time.Hour) // older than 7 days

	if err := RegisterRoot(root, registeredAt); err != nil {
		t.Fatalf("RegisterRoot: unexpected error: %v", err)
	}
	path := filepath.Join(rootsDir(), hashedRootFile(root))

	got, err := Roots(checkAt)
	if err != nil {
		t.Fatalf("Roots: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Roots() = %+v, want empty (lastSeen older than 7 days)", got)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("file %s still exists, want it deleted", path)
	}
}

func TestReadServerRecordErrNoServerWhenAbsent(t *testing.T) {
	isolateCacheDir(t)

	_, err := ReadServerRecord()
	if !errors.Is(err, ErrNoServer) {
		t.Fatalf("ReadServerRecord error = %v, want ErrNoServer", err)
	}
}

func TestWriteAndReadServerRecordRoundTrip(t *testing.T) {
	isolateCacheDir(t)
	want := ServerRecord{
		PID:       4242,
		Port:      7385,
		Version:   "0.3.3",
		StartedAt: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC),
		URL:       "http://127.0.0.1:7385",
	}

	if err := WriteServerRecord(want); err != nil {
		t.Fatalf("WriteServerRecord: unexpected error: %v", err)
	}

	got, err := ReadServerRecord()
	if err != nil {
		t.Fatalf("ReadServerRecord: unexpected error: %v", err)
	}
	if got.PID != want.PID || got.Port != want.Port || got.Version != want.Version ||
		got.URL != want.URL || !got.StartedAt.Equal(want.StartedAt) {
		t.Errorf("ReadServerRecord() = %+v, want %+v", got, want)
	}
}

func TestRemoveServerRecordOnlyWhenPIDMatches(t *testing.T) {
	isolateCacheDir(t)
	rec := ServerRecord{PID: 111, Port: 7385, StartedAt: time.Now()}
	if err := WriteServerRecord(rec); err != nil {
		t.Fatalf("WriteServerRecord: unexpected error: %v", err)
	}

	if err := RemoveServerRecord(999); err != nil {
		t.Fatalf("RemoveServerRecord (wrong pid): unexpected error: %v", err)
	}
	if _, err := ReadServerRecord(); err != nil {
		t.Fatalf("ReadServerRecord after wrong-pid remove: unexpected error: %v", err)
	}

	if err := RemoveServerRecord(111); err != nil {
		t.Fatalf("RemoveServerRecord (correct pid): unexpected error: %v", err)
	}
	if _, err := ReadServerRecord(); !errors.Is(err, ErrNoServer) {
		t.Errorf("ReadServerRecord after correct-pid remove: err = %v, want ErrNoServer", err)
	}
}

func TestRemoveServerRecordNoFileIsNotError(t *testing.T) {
	isolateCacheDir(t)

	if err := RemoveServerRecord(123); err != nil {
		t.Errorf("RemoveServerRecord: unexpected error when no file exists: %v", err)
	}
}
