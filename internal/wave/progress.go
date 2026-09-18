package wave

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
)

// validPhases is the bounded phase enum for progress markers.  A free-text
// phase is rejected — same discipline as wave-progress.js::VALID_PHASES.
var validPhases = map[string]bool{
	"started":   true,
	"reading":   true,
	"editing":   true,
	"verifying": true,
	"reporting": true,
}

// ErrBadPhase is wrapped into the error returned by UpdateProgress when the
// supplied phase is not in the bounded enum.
var ErrBadPhase = errors.New("wave: invalid phase")

// TaskProgress is one task's entry inside a progress marker.
type TaskProgress struct {
	Phase             string   `json:"phase"`
	UpdatedAt         string   `json:"updatedAt"`
	StartedAt         string   `json:"startedAt,omitempty"`
	LastCompletedTask string   `json:"lastCompletedTask,omitempty"`
	AcceptanceDone    []int    `json:"acceptanceDone,omitempty"`
	FilesTouched      []string `json:"filesTouched,omitempty"`
	Blocker           string   `json:"blocker,omitempty"`
}

// ProgressFields carries the optional structured-milestone fields
// UpdateProgress's callers may set on a task's progress entry, on top of
// the always-present phase/lastCompletedTask. Passing a zero-value
// ProgressFields (or omitting the variadic argument to UpdateProgress
// entirely) leaves each field's previously-recorded value untouched —
// the same preserve-if-absent contract UpdateProgress already applies to
// LastCompletedTask, extended uniformly to these fields rather than
// special-cased per field.
type ProgressFields struct {
	// AcceptanceDone lists the 0-based indices, into the task's fact-sheet
	// acceptance criteria, that the worker has completed so far. A nil
	// slice preserves the existing value; callers that want to clear it
	// pass a non-nil empty slice.
	AcceptanceDone []int
	// FilesTouched lists files the worker has modified so far. A nil slice
	// preserves the existing value.
	FilesTouched []string
	// Blocker is a free-text reason the worker is currently blocked. An
	// empty string preserves the existing value.
	Blocker string
}

// Progress is the aggregated view of a run's progress markers, assembled by
// ReadProgress from every file under <root>/.sdlc-v2/runs/<runID>/progress/
// plus (at lower priority) the legacy single-file marker,
// <root>/.sdlc-v2/runs/<runID>/progress.json. The exported shape is
// unchanged from the single-file era so MCP callers (execute_state's
// wave-progress action) see no difference.
type Progress struct {
	Tasks map[string]TaskProgress `json:"tasks"`
}

// progressDir returns the absolute path of a run's per-task progress
// directory — one JSON file per task, named <taskID>.json. Writing here
// (rather than to one shared file) eliminates the concurrent-write race by
// construction: distinct tasks touch distinct files, so no cross-task
// locking is needed. Within a single task's file, UpdateProgress does read
// the existing record before writing the merged result back (a
// read-modify-write, not a blind overwrite) so fields set on an earlier
// call — StartedAt, LastCompletedTask, and any unset ProgressFields — carry
// forward across phase updates; the single-writer-per-file property keeps
// that read-modify-write race-free without locking. Each task's directory
// entry also has a sibling <taskID>.server.json file (see serverstate.go)
// holding server-owned dispatch/classification state; that file has its own,
// separate writer — the server, never the worker — so it is unaffected by
// this file's read-modify-write.
func progressDir(root, runID string) string {
	return filepath.Join(executionDir(root, runID), "progress")
}

// legacyProgressPath returns the absolute path of the pre-per-task-file
// progress marker. ReadProgress still merges it in (at lower priority than
// the per-task files) so a run that wrote progress before this format
// change doesn't lose those entries; nothing writes this path anymore.
func legacyProgressPath(root, runID string) string {
	return filepath.Join(executionDir(root, runID), "progress.json")
}

// taskProgressPath returns the absolute path of one task's progress file.
func taskProgressPath(root, runID, taskID string) string {
	return filepath.Join(progressDir(root, runID), taskID+".json")
}

// progressStore abstracts the filesystem operations UpdateProgress and
// TouchProgress perform on a task's progress file, so their tests can
// substitute an in-memory fake instead of real disk I/O — required by the
// no-real-fs-git-in-tests guardrail once new tests are added alongside
// them. ReadProgress predates this seam and is not routed through it: its
// tests are pre-existing and out of scope for the change that introduced
// this interface.
type progressStore interface {
	// readJSON loads path into out, mirroring fsx.ReadJSON's error
	// contract (wraps fsx.ErrNotFound when the path is absent).
	readJSON(path string, out any) error
	// writeJSON atomically writes v to path, mirroring
	// fsx.AtomicWriteJSON.
	writeJSON(path string, v any) error
	// mkdirAll ensures dir exists, mirroring os.MkdirAll.
	mkdirAll(dir string) error
}

// realProgressStore is the production progressStore, backed by fsx and os.
type realProgressStore struct{}

func (realProgressStore) readJSON(path string, out any) error {
	return fsx.ReadJSON(path, out)
}

func (realProgressStore) writeJSON(path string, v any) error {
	return fsx.AtomicWriteJSON(path, v)
}

func (realProgressStore) mkdirAll(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

// progressStoreImpl is the package-level progressStore used by
// UpdateProgress and TouchProgress. Tests may swap it for an in-memory
// fake for the duration of a single test (save the old value, defer
// restoring it); production code must never reassign it.
var progressStoreImpl progressStore = realProgressStore{}

// ReadProgress aggregates every per-task file under
// <root>/.sdlc-v2/runs/<runID>/progress/ into a single Progress, merging
// in the legacy single-file marker (if any) at lower priority — a taskID
// present in both is resolved in favor of the per-task file. Missing or
// unreadable files (legacy marker absent, progress/ directory absent, a
// single corrupt per-task file) are swallowed rather than surfaced: matches
// the pre-existing never-throw contract for I/O and parse problems. The
// only error ReadProgress returns is ErrBadRunID.
func ReadProgress(root, runID string) (*Progress, error) {
	if err := validateRunID(runID); err != nil {
		return nil, err
	}

	tasks := map[string]TaskProgress{}

	// Legacy marker first, at lower priority — per-task files below
	// overwrite any matching taskID.
	legacy := &Progress{}
	if err := fsx.ReadJSON(legacyProgressPath(root, runID), legacy); err == nil {
		for taskID, tp := range legacy.Tasks {
			tasks[taskID] = tp
		}
	}

	entries, err := os.ReadDir(progressDir(root, runID))
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".json") {
				continue
			}
			taskID := strings.TrimSuffix(name, ".json")

			var tp TaskProgress
			if err := fsx.ReadJSON(filepath.Join(progressDir(root, runID), name), &tp); err != nil {
				// Corrupt or unreadable per-task file: skip it, keep
				// aggregating the rest.
				continue
			}
			tasks[taskID] = tp
		}
	}

	return &Progress{Tasks: tasks}, nil
}

// UpdateProgress records a single task's phase as
// <runDir>/progress/<taskID>.json via fsx.AtomicWriteJSON. The file is
// read-modify-written so that StartedAt (set once on first write),
// LastCompletedTask, and the optional ProgressFields (AcceptanceDone,
// FilesTouched, Blocker) survive across phase updates. Each
// task's file is wholly owned by that task, so no cross-task race exists.
//
// fields is variadic so every existing 5-arg call site keeps compiling
// unchanged: omitting it (or passing a zero-value ProgressFields) writes
// only phase/lastCompletedTask, preserving whatever structured-milestone
// data was recorded on a prior call.
func UpdateProgress(root, runID, taskID, phase, lastCompletedTask string, fields ...ProgressFields) error {
	if err := validateRunID(runID); err != nil {
		return err
	}
	if !validPhases[phase] {
		return fmt.Errorf("phase %q not in bounded enum (started|reading|editing|verifying|reporting): %w", phase, ErrBadPhase)
	}

	var f ProgressFields
	if len(fields) > 0 {
		f = fields[0]
	}

	dir := progressDir(root, runID)
	if err := progressStoreImpl.mkdirAll(dir); err != nil {
		return fmt.Errorf("wave: mkdir %s: %w", dir, err)
	}

	// Read existing to preserve StartedAt and any unset optional fields.
	var existing TaskProgress
	_ = progressStoreImpl.readJSON(taskProgressPath(root, runID, taskID), &existing)

	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	tp := TaskProgress{
		Phase:     phase,
		UpdatedAt: now,
		StartedAt: existing.StartedAt,
	}
	if tp.StartedAt == "" {
		tp.StartedAt = now
	}
	if lastCompletedTask != "" {
		tp.LastCompletedTask = lastCompletedTask
	} else if existing.LastCompletedTask != "" {
		tp.LastCompletedTask = existing.LastCompletedTask
	}

	if f.AcceptanceDone != nil {
		tp.AcceptanceDone = f.AcceptanceDone
	} else {
		tp.AcceptanceDone = existing.AcceptanceDone
	}
	if f.FilesTouched != nil {
		tp.FilesTouched = f.FilesTouched
	} else {
		tp.FilesTouched = existing.FilesTouched
	}
	if f.Blocker != "" {
		tp.Blocker = f.Blocker
	} else {
		tp.Blocker = existing.Blocker
	}

	return progressStoreImpl.writeJSON(taskProgressPath(root, runID, taskID), tp)
}

// TouchProgress advances only UpdatedAt on a task's progress file,
// preserving every other recorded field. It is the liveness-only sibling of
// UpdateProgress: there is no phase argument, so the validPhases invariant
// is untouched. A missing file is created with Phase "started" so a worker
// that never called wave-progress still reports liveness.
func TouchProgress(root, runID, taskID string) error {
	if err := validateRunID(runID); err != nil {
		return err
	}

	dir := progressDir(root, runID)
	if err := progressStoreImpl.mkdirAll(dir); err != nil {
		return fmt.Errorf("wave: mkdir %s: %w", dir, err)
	}

	// Read existing to preserve every field except UpdatedAt. A missing or
	// corrupt file yields a zero-value TaskProgress, handled below the same
	// way UpdateProgress handles a first write.
	var existing TaskProgress
	_ = progressStoreImpl.readJSON(taskProgressPath(root, runID, taskID), &existing)

	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	tp := existing
	tp.UpdatedAt = now
	if tp.Phase == "" {
		tp.Phase = "started"
	}
	if tp.StartedAt == "" {
		tp.StartedAt = now
	}

	return progressStoreImpl.writeJSON(taskProgressPath(root, runID, taskID), tp)
}
