package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

func TestList_AbsentRunsDir_ReturnsEmptyNonNilSlice(t *testing.T) {
	root := t.TempDir()
	// No .sdlc-v2/runs/ directory at all.

	got, err := List(root)
	if err != nil {
		t.Fatalf("List: unexpected error %v", err)
	}
	if got.States == nil {
		t.Fatalf("List.States = nil, want non-nil empty slice")
	}
	if len(got.States) != 0 {
		t.Fatalf("List.States = %v, want empty", got.States)
	}
	if got.Skipped != 0 {
		t.Fatalf("List.Skipped = %d, want 0", got.Skipped)
	}
}

func TestList_NewestFirstByFilenameTimestamp(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Filename timestamps order: old < mid < new. Chtimes mtimes are set in
	// the OPPOSITE order, so a test that passed by sorting on mtime instead
	// of the filename timestamp would fail here.
	files := []struct {
		name  string
		mtime time.Time
	}{
		{"ship-main-20260101T100000Z.json", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)},
		{"execute-main-20260102T100000Z.json", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
		{"plan-main-20260103T100000Z.json", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, f := range files {
		fp := filepath.Join(dir, f.name)
		if err := os.WriteFile(fp, []byte(`{"ok":true}`), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", f.name, err)
		}
		if err := os.Chtimes(fp, f.mtime, f.mtime); err != nil {
			t.Fatalf("Chtimes %s: %v", f.name, err)
		}
	}

	got, err := List(root)
	if err != nil {
		t.Fatalf("List: unexpected error %v", err)
	}
	if len(got.States) != 3 {
		t.Fatalf("List.States has %d entries, want 3", len(got.States))
	}

	wantOrder := []string{
		"plan-main-20260103T100000Z.json",
		"execute-main-20260102T100000Z.json",
		"ship-main-20260101T100000Z.json",
	}
	for i, want := range wantOrder {
		if got := filepath.Base(got.States[i].Path); got != want {
			t.Fatalf("States[%d] = %q, want %q", i, got, want)
		}
	}
	if got.Skipped != 0 {
		t.Fatalf("List.Skipped = %d, want 0", got.Skipped)
	}
}

func TestList_SkipsTempFilesAndDirectories(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	goodName := "ship-main-20260101T100000Z.json"
	if err := os.WriteFile(filepath.Join(dir, goodName), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatalf("WriteFile good: %v", err)
	}

	// A temp file left behind by fsx.AtomicWriteBytes mid-write: it carries
	// a valid prefix+slug+timestamp but an extra ".tmp-<rand>" suffix after
	// ".json", so it does not match filenameRe at all.
	tmpName := "execute-main-20260102T100000Z.json.tmp-abc123"
	if err := os.WriteFile(filepath.Join(dir, tmpName), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatalf("WriteFile tmp: %v", err)
	}

	// A plain directory, and a .evidence directory whose name would
	// otherwise look like a plan run's evidence folder.
	if err := os.MkdirAll(filepath.Join(dir, "some-subdir"), 0o755); err != nil {
		t.Fatalf("MkdirAll subdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "plan-main-20260103T100000Z.evidence"), 0o755); err != nil {
		t.Fatalf("MkdirAll evidence dir: %v", err)
	}

	got, err := List(root)
	if err != nil {
		t.Fatalf("List: unexpected error %v", err)
	}
	if len(got.States) != 1 {
		t.Fatalf("List.States has %d entries, want 1 (only %q): %+v", len(got.States), goodName, got.States)
	}
	if filepath.Base(got.States[0].Path) != goodName {
		t.Fatalf("States[0] = %q, want %q", filepath.Base(got.States[0].Path), goodName)
	}
	// Temp files and directories are skipped silently: they never matched
	// filenameRe, so they must not inflate Skipped.
	if got.Skipped != 0 {
		t.Fatalf("List.Skipped = %d, want 0 (temp files/dirs don't count)", got.Skipped)
	}
}

func TestList_UnparsableJSON_CountsInSkipped(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	goodName := "ship-main-20260101T100000Z.json"
	if err := os.WriteFile(filepath.Join(dir, goodName), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatalf("WriteFile good: %v", err)
	}

	// Matches filenameRe exactly, but the contents are not valid JSON.
	corruptName := "execute-main-20260102T100000Z.json"
	if err := os.WriteFile(filepath.Join(dir, corruptName), []byte(`{not valid json`), 0o644); err != nil {
		t.Fatalf("WriteFile corrupt: %v", err)
	}

	got, err := List(root)
	if err != nil {
		t.Fatalf("List: unexpected error %v", err)
	}
	if len(got.States) != 1 {
		t.Fatalf("List.States has %d entries, want 1: %+v", len(got.States), got.States)
	}
	if filepath.Base(got.States[0].Path) != goodName {
		t.Fatalf("States[0] = %q, want %q", filepath.Base(got.States[0].Path), goodName)
	}
	if got.Skipped != 1 {
		t.Fatalf("List.Skipped = %d, want 1 (corrupt file matched the name but failed to parse)", got.Skipped)
	}
}

func TestList_ReadDirError_ReturnsError(t *testing.T) {
	root := t.TempDir()
	// Make runs/'s parent (.sdlc-v2) a file where a directory is expected,
	// so ReadDir on runs/ fails with something other than not-exist.
	dataDir := filepath.Join(root, paths.DataDir)
	if err := os.WriteFile(dataDir, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := List(root)
	if err == nil {
		t.Fatalf("List: expected error when runs/'s parent is a regular file, got result %+v", got)
	}
}

func TestList_PopulatesStateFields(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	name := "plan-feat-my-feature-20260101T100000Z.json"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"marker":"x"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := List(root)
	if err != nil {
		t.Fatalf("List: unexpected error %v", err)
	}
	if len(got.States) != 1 {
		t.Fatalf("List.States has %d entries, want 1", len(got.States))
	}

	st := got.States[0]
	if st.Prefix != "plan" {
		t.Fatalf("Prefix = %q, want %q", st.Prefix, "plan")
	}
	if st.BranchSlug != "feat-my-feature" {
		t.Fatalf("BranchSlug = %q, want %q", st.BranchSlug, "feat-my-feature")
	}
	if st.Root != root {
		t.Fatalf("Root = %q, want %q", st.Root, root)
	}
	if marker, _ := st.Data["marker"].(string); marker != "x" {
		t.Fatalf("Data[marker] = %v, want %q", st.Data["marker"], "x")
	}
}
