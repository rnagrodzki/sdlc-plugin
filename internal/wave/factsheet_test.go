package wave

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			err := UpdateProgress(t.TempDir(), id, "1", "started")
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
	wantPath := filepath.Join(root, ".sdlc", "execution", runID, "task-14.md")
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

	dir := filepath.Join(root, ".sdlc", "execution", "run1")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover tmp file: %s", e.Name())
		}
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
	dir := filepath.Join(root, ".sdlc", "execution", "run1")
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
	dir := filepath.Join(root, ".sdlc", "execution", "run1")
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

	err := UpdateProgress(root, runID, "task-1", "started")
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

	if err := UpdateProgress(root, runID, "task-1", "started"); err != nil {
		t.Fatalf("UpdateProgress task-1: %v", err)
	}
	if err := UpdateProgress(root, runID, "task-2", "editing"); err != nil {
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

	if err := UpdateProgress(root, runID, "task-1", "started"); err != nil {
		t.Fatalf("UpdateProgress started: %v", err)
	}
	if err := UpdateProgress(root, runID, "task-1", "editing"); err != nil {
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
		err := UpdateProgress(root, "run1", "task-1", phase)
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
		err := UpdateProgress(root, "run1", "task-1", phase)
		if err != nil {
			t.Fatalf("UpdateProgress: unexpected error for valid phase %q: %v", phase, err)
		}
	}
}

func TestUpdateProgress_NoLeftoverTmpFiles(t *testing.T) {
	root := t.TempDir()
	if err := UpdateProgress(root, "run1", "task-1", "started"); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	dir := filepath.Join(root, ".sdlc", "execution", "run1")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") || strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("leftover tmp file: %s", e.Name())
		}
	}
}

func TestUpdateProgress_ByteShapeCompatible(t *testing.T) {
	root := t.TempDir()
	if err := UpdateProgress(root, "run1", "task-1", "started"); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	fp := filepath.Join(root, ".sdlc", "execution", "run1", "progress.json")
	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Must parse as {"tasks": {"task-1": {"phase": "...", "updatedAt": "..."}}}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal top-level: %v", err)
	}
	tasksRaw, ok := raw["tasks"]
	if !ok {
		t.Fatalf("ByteShape: missing 'tasks' key")
	}
	var tasks map[string]map[string]string
	if err := json.Unmarshal(tasksRaw, &tasks); err != nil {
		t.Fatalf("Unmarshal tasks: %v", err)
	}
	entry, ok := tasks["task-1"]
	if !ok {
		t.Fatalf("ByteShape: missing task-1")
	}
	if entry["phase"] != "started" {
		t.Fatalf("ByteShape: phase = %q, want started", entry["phase"])
	}
	if _, ok := entry["updatedAt"]; !ok {
		t.Fatalf("ByteShape: missing updatedAt")
	}
}
