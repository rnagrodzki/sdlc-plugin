package wave

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// runID validation — tested across all three exported functions
// ---------------------------------------------------------------------------

func TestValidateRunID_RejectsPathTraversal(t *testing.T) {
	bad := []string{
		"..",
		"../etc",
		"a/../b",
		"foo/bar",
		"/abs",
		"run id",
		"run\tid",
		"run\nid",
		"",
		"hello world",
		"a b",
		"run@id",
		"run;id",
	}
	for _, id := range bad {
		t.Run("WriteFactsheet/"+id, func(t *testing.T) {
			_, err := WriteFactsheet(t.TempDir(), id, Factsheet{ID: "1", Name: "t"})
			if err == nil {
				t.Fatalf("WriteFactsheet: expected error for runID %q, got nil", id)
			}
			if !errors.Is(err, ErrBadRunID) {
				t.Fatalf("WriteFactsheet: expected ErrBadRunID, got %v", err)
			}
		})
		t.Run("ReadProgress/"+id, func(t *testing.T) {
			_, err := ReadProgress(t.TempDir(), id)
			if err == nil {
				t.Fatalf("ReadProgress: expected error for runID %q, got nil", id)
			}
			if !errors.Is(err, ErrBadRunID) {
				t.Fatalf("ReadProgress: expected ErrBadRunID, got %v", err)
			}
		})
		t.Run("UpdateProgress/"+id, func(t *testing.T) {
			err := UpdateProgress(t.TempDir(), id, "1", "started", "")
			if err == nil {
				t.Fatalf("UpdateProgress: expected error for runID %q, got nil", id)
			}
			if !errors.Is(err, ErrBadRunID) {
				t.Fatalf("UpdateProgress: expected ErrBadRunID, got %v", err)
			}
		})
	}
}

func TestValidateRunID_AcceptsValid(t *testing.T) {
	good := []string{
		"abc",
		"ABC",
		"123",
		"a-b",
		"a_b",
		"20260905T210309690",
		"run-1_test-2",
	}
	for _, id := range good {
		root := t.TempDir()
		_, err := WriteFactsheet(root, id, Factsheet{ID: "1", Name: "t"})
		if err != nil {
			t.Fatalf("WriteFactsheet: unexpected error for valid runID %q: %v", id, err)
		}
	}
}

// ---------------------------------------------------------------------------
// WriteFactsheet — round trip, idempotency, normalizeTaskID
// ---------------------------------------------------------------------------

func TestWriteFactsheet_RoundTrip(t *testing.T) {
	root := t.TempDir()
	runID := "test-run-1"
	fs := Factsheet{
		ID:                 "14",
		Name:               "wave factsheets",
		Description:        "Ports task-factsheet.js",
		Contract:           "WriteFactsheet(root, runID string, fs Factsheet)",
		AcceptanceCriteria: []string{"runID validated", "atomic writes"},
		Files:              []string{"factsheet.go", "progress.go"},
		Upstream: &UpstreamSurfaces{
			FilesAdded:    []string{"split.go"},
			FilesModified: []string{"go.mod"},
			Interfaces:    []string{"Split in split.go"},
			Decisions:     []string{"Task 15 ruling"},
		},
	}

	path, err := WriteFactsheet(root, runID, fs)
	if err != nil {
		t.Fatalf("WriteFactsheet: unexpected error: %v", err)
	}

	// File should exist at the expected path.
	wantPath := filepath.Join(root, paths.DataDir, "execution", runID, "task-14.md")
	if path != wantPath {
		t.Fatalf("WriteFactsheet: path = %q, want %q", path, wantPath)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)

	// Verify key sections are present.
	for _, want := range []string{
		"# Task 14: wave factsheets",
		"## Notes (rationale)",
		"Ports task-factsheet.js",
		"## Contract",
		"## Acceptance Criteria",
		"- runID validated",
		"## Files",
		"- factsheet.go",
		"## Upstream Surfaces",
		"**Created:**",
		"- split.go",
		"**Decisions:**",
		"- Task 15 ruling",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("WriteFactsheet: content missing %q", want)
		}
	}
}

func TestWriteFactsheet_NormalizeTaskID(t *testing.T) {
	root := t.TempDir()
	fs := Factsheet{ID: "T7", Name: "test"}
	path, err := WriteFactsheet(root, "run1", fs)
	if err != nil {
		t.Fatalf("WriteFactsheet: %v", err)
	}
	if !strings.HasSuffix(path, "task-7.md") {
		t.Fatalf("WriteFactsheet: expected task-7.md, got %s", filepath.Base(path))
	}
}

func TestWriteFactsheet_Idempotent(t *testing.T) {
	root := t.TempDir()
	fs := Factsheet{ID: "1", Name: "idem"}

	path1, err := WriteFactsheet(root, "run1", fs)
	if err != nil {
		t.Fatalf("WriteFactsheet first: %v", err)
	}
	info1, _ := os.Stat(path1)

	path2, err := WriteFactsheet(root, "run1", fs)
	if err != nil {
		t.Fatalf("WriteFactsheet second: %v", err)
	}
	info2, _ := os.Stat(path2)

	if path1 != path2 {
		t.Fatalf("Idempotent: paths differ: %q vs %q", path1, path2)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatalf("Idempotent: mtime changed on identical rewrite")
	}
}

func TestWriteFactsheet_OverwritesOnChange(t *testing.T) {
	root := t.TempDir()
	fs1 := Factsheet{ID: "1", Name: "v1"}
	path, err := WriteFactsheet(root, "run1", fs1)
	if err != nil {
		t.Fatalf("WriteFactsheet v1: %v", err)
	}
	data1, _ := os.ReadFile(path)

	fs2 := Factsheet{ID: "1", Name: "v2"}
	_, err = WriteFactsheet(root, "run1", fs2)
	if err != nil {
		t.Fatalf("WriteFactsheet v2: %v", err)
	}
	data2, _ := os.ReadFile(path)

	if string(data1) == string(data2) {
		t.Fatalf("OverwritesOnChange: content should differ after update")
	}
	if !strings.Contains(string(data2), "v2") {
		t.Fatalf("OverwritesOnChange: updated content missing v2")
	}
}

func TestWriteFactsheet_NoLeftoverTmpFiles(t *testing.T) {
	root := t.TempDir()
	fs := Factsheet{ID: "1", Name: "clean"}
	_, err := WriteFactsheet(root, "run1", fs)
	if err != nil {
		t.Fatalf("WriteFactsheet: %v", err)
	}

	dir := filepath.Join(root, paths.DataDir, "execution", "run1")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover tmp file: %s", e.Name())
		}
	}
}

// ---------------------------------------------------------------------------
// ReadFactsheet / ListFactsheetIDs
// ---------------------------------------------------------------------------

func TestReadFactsheet_RoundTrip(t *testing.T) {
	root := t.TempDir()
	fs := Factsheet{ID: "14", Name: "wave factsheets", Contract: "some contract"}
	wantPath, err := WriteFactsheet(root, "run-1", fs)
	if err != nil {
		t.Fatalf("WriteFactsheet: %v", err)
	}

	path, content, err := ReadFactsheet(root, "run-1", "14")
	if err != nil {
		t.Fatalf("ReadFactsheet: %v", err)
	}
	if path != wantPath {
		t.Errorf("path = %q, want %q", path, wantPath)
	}
	if !strings.Contains(content, "# Task 14: wave factsheets") {
		t.Errorf("content missing task heading, got:\n%s", content)
	}
	if !strings.Contains(content, "some contract") {
		t.Errorf("content missing contract text, got:\n%s", content)
	}
}

func TestReadFactsheet_NormalizeTaskID(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteFactsheet(root, "run-1", Factsheet{ID: "T7", Name: "seven"}); err != nil {
		t.Fatalf("WriteFactsheet: %v", err)
	}

	// "7" (already normalized) and "T7" (plan-style ID) must both resolve
	// to the same file WriteFactsheet(ID: "T7", ...) produced.
	for _, id := range []string{"7", "T7", "t7"} {
		path, content, err := ReadFactsheet(root, "run-1", id)
		if err != nil {
			t.Fatalf("ReadFactsheet(%q): %v", id, err)
		}
		if !strings.Contains(content, "seven") {
			t.Errorf("ReadFactsheet(%q): content missing 'seven', got:\n%s", id, content)
		}
		if !strings.HasSuffix(path, "task-7.md") {
			t.Errorf("ReadFactsheet(%q): path = %q, want suffix task-7.md", id, path)
		}
	}
}

func TestReadFactsheet_NotFound(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteFactsheet(root, "run-1", Factsheet{ID: "1", Name: "only task"}); err != nil {
		t.Fatalf("WriteFactsheet: %v", err)
	}

	_, _, err := ReadFactsheet(root, "run-1", "99")
	if err == nil {
		t.Fatal("expected error for unknown taskID")
	}
	if !errors.Is(err, ErrFactsheetNotFound) {
		t.Fatalf("expected ErrFactsheetNotFound, got %v", err)
	}
}

func TestReadFactsheet_NoRunDirectory(t *testing.T) {
	root := t.TempDir()
	_, _, err := ReadFactsheet(root, "never-started", "1")
	if err == nil {
		t.Fatal("expected error for a run directory that was never created")
	}
	if !errors.Is(err, ErrFactsheetNotFound) {
		t.Fatalf("expected ErrFactsheetNotFound, got %v", err)
	}
}

func TestReadFactsheet_BadRunID(t *testing.T) {
	root := t.TempDir()
	_, _, err := ReadFactsheet(root, "../etc", "1")
	if err == nil {
		t.Fatal("expected error for a path-traversal runID")
	}
	if !errors.Is(err, ErrBadRunID) {
		t.Fatalf("expected ErrBadRunID, got %v", err)
	}
}

func TestReadFactsheet_LeavesFileUnchanged(t *testing.T) {
	root := t.TempDir()
	path, err := WriteFactsheet(root, "run-1", Factsheet{ID: "1", Name: "unchanged"})
	if err != nil {
		t.Fatalf("WriteFactsheet: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	if _, _, err := ReadFactsheet(root, "run-1", "1"); err != nil {
		t.Fatalf("ReadFactsheet: %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat after read: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("ReadFactsheet modified the file's mtime; it must be read-only")
	}
	if before.Size() != after.Size() {
		t.Error("ReadFactsheet changed the file's size; it must be read-only")
	}
}

func TestListFactsheetIDs_SortedAndDeduped(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"10", "2", "T1"} {
		if _, err := WriteFactsheet(root, "run-1", Factsheet{ID: id, Name: "task " + id}); err != nil {
			t.Fatalf("WriteFactsheet(%q): %v", id, err)
		}
	}

	ids, err := ListFactsheetIDs(root, "run-1")
	if err != nil {
		t.Fatalf("ListFactsheetIDs: %v", err)
	}
	want := []string{"1", "10", "2"} // lexical sort, matching sort.Strings
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ids[%d] = %q, want %q (full: %v)", i, ids[i], want[i], ids)
		}
	}
}

func TestListFactsheetIDs_MissingRunDirectory(t *testing.T) {
	root := t.TempDir()
	ids, err := ListFactsheetIDs(root, "never-started")
	if err != nil {
		t.Fatalf("ListFactsheetIDs: unexpected error for a missing run directory: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v, want empty", ids)
	}
}

func TestListFactsheetIDs_IgnoresTmpFiles(t *testing.T) {
	root := t.TempDir()
	if _, err := WriteFactsheet(root, "run-1", Factsheet{ID: "1", Name: "real"}); err != nil {
		t.Fatalf("WriteFactsheet: %v", err)
	}
	dir := filepath.Join(root, paths.DataDir, "execution", "run-1")
	if err := os.WriteFile(filepath.Join(dir, "task-2.abcd1234.tmp"), []byte("partial write"), 0o644); err != nil {
		t.Fatalf("write stray tmp file: %v", err)
	}

	ids, err := ListFactsheetIDs(root, "run-1")
	if err != nil {
		t.Fatalf("ListFactsheetIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != "1" {
		t.Fatalf("ids = %v, want [\"1\"] (tmp file must be excluded)", ids)
	}
}

func TestListFactsheetIDs_BadRunID(t *testing.T) {
	root := t.TempDir()
	_, err := ListFactsheetIDs(root, "../etc")
	if err == nil {
		t.Fatal("expected error for a path-traversal runID")
	}
	if !errors.Is(err, ErrBadRunID) {
		t.Fatalf("expected ErrBadRunID, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Progress — ReadProgress, UpdateProgress, round trip, merge
// ---------------------------------------------------------------------------

func TestReadProgress_MissingFile(t *testing.T) {
	root := t.TempDir()
	p, err := ReadProgress(root, "nonexistent")
	if err != nil {
		t.Fatalf("ReadProgress: unexpected error: %v", err)
	}
	if len(p.Tasks) != 0 {
		t.Fatalf("ReadProgress: expected empty tasks, got %v", p.Tasks)
	}
}

func TestReadProgress_CorruptFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, "execution", "run1")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "progress.json"), []byte("not json{"), 0o644)

	p, err := ReadProgress(root, "run1")
	if err != nil {
		t.Fatalf("ReadProgress: unexpected error on corrupt file: %v", err)
	}
	if len(p.Tasks) != 0 {
		t.Fatalf("ReadProgress: expected empty tasks on corrupt file, got %v", p.Tasks)
	}
}

func TestReadProgress_NullTasksField(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, "execution", "run1")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "progress.json"), []byte(`{"tasks":null}`), 0o644)

	p, err := ReadProgress(root, "run1")
	if err != nil {
		t.Fatalf("ReadProgress: unexpected error: %v", err)
	}
	if p.Tasks == nil {
		t.Fatalf("ReadProgress: Tasks map should be initialized, not nil")
	}
	if len(p.Tasks) != 0 {
		t.Fatalf("ReadProgress: expected empty tasks, got %v", p.Tasks)
	}
}

func TestUpdateProgress_RoundTrip(t *testing.T) {
	root := t.TempDir()
	runID := "run1"

	err := UpdateProgress(root, runID, "task-1", "started", "")
	if err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	p, err := ReadProgress(root, runID)
	if err != nil {
		t.Fatalf("ReadProgress: %v", err)
	}
	tp, ok := p.Tasks["task-1"]
	if !ok {
		t.Fatalf("ReadProgress: task-1 not found in tasks")
	}
	if tp.Phase != "started" {
		t.Fatalf("ReadProgress: phase = %q, want started", tp.Phase)
	}
	if tp.UpdatedAt == "" {
		t.Fatalf("ReadProgress: updatedAt is empty")
	}
}

func TestUpdateProgress_MergePreservesOtherTasks(t *testing.T) {
	root := t.TempDir()
	runID := "run1"

	if err := UpdateProgress(root, runID, "task-1", "started", ""); err != nil {
		t.Fatalf("UpdateProgress task-1: %v", err)
	}
	if err := UpdateProgress(root, runID, "task-2", "editing", ""); err != nil {
		t.Fatalf("UpdateProgress task-2: %v", err)
	}

	p, err := ReadProgress(root, runID)
	if err != nil {
		t.Fatalf("ReadProgress: %v", err)
	}
	if len(p.Tasks) != 2 {
		t.Fatalf("ReadProgress: expected 2 tasks, got %d", len(p.Tasks))
	}
	if p.Tasks["task-1"].Phase != "started" {
		t.Fatalf("ReadProgress: task-1 phase = %q, want started", p.Tasks["task-1"].Phase)
	}
	if p.Tasks["task-2"].Phase != "editing" {
		t.Fatalf("ReadProgress: task-2 phase = %q, want editing", p.Tasks["task-2"].Phase)
	}
}

func TestUpdateProgress_OverwritesSameTask(t *testing.T) {
	root := t.TempDir()
	runID := "run1"

	if err := UpdateProgress(root, runID, "task-1", "started", ""); err != nil {
		t.Fatalf("UpdateProgress started: %v", err)
	}
	if err := UpdateProgress(root, runID, "task-1", "editing", ""); err != nil {
		t.Fatalf("UpdateProgress editing: %v", err)
	}

	p, err := ReadProgress(root, runID)
	if err != nil {
		t.Fatalf("ReadProgress: %v", err)
	}
	if p.Tasks["task-1"].Phase != "editing" {
		t.Fatalf("ReadProgress: phase = %q, want editing", p.Tasks["task-1"].Phase)
	}
}

func TestUpdateProgress_RejectsInvalidPhase(t *testing.T) {
	root := t.TempDir()
	bad := []string{"", "running", "done", "STARTED", "Starting"}
	for _, phase := range bad {
		err := UpdateProgress(root, "run1", "task-1", phase, "")
		if err == nil {
			t.Fatalf("UpdateProgress: expected error for phase %q, got nil", phase)
		}
		if !errors.Is(err, ErrBadPhase) {
			t.Fatalf("UpdateProgress: expected ErrBadPhase for %q, got %v", phase, err)
		}
	}
}

func TestUpdateProgress_AcceptsAllValidPhases(t *testing.T) {
	root := t.TempDir()
	phases := []string{"started", "reading", "editing", "verifying", "reporting"}
	for _, phase := range phases {
		err := UpdateProgress(root, "run1", "task-1", phase, "")
		if err != nil {
			t.Fatalf("UpdateProgress: unexpected error for valid phase %q: %v", phase, err)
		}
	}
}

func TestUpdateProgress_NoLeftoverTmpFiles(t *testing.T) {
	root := t.TempDir()
	if err := UpdateProgress(root, "run1", "task-1", "started", ""); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	dir := filepath.Join(root, paths.DataDir, "execution", "run1")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") || strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("leftover tmp file: %s", e.Name())
		}
	}
}
