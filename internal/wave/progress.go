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

// StallCause classifies why a task's heartbeat is unhealthy.
type StallCause string

const (
	// StallCauseNone means the heartbeat is healthy.
	StallCauseNone StallCause = ""
	// StallCauseStalled means the worker stopped sending heartbeat updates.
	StallCauseStalled StallCause = "stalled"
	// StallCauseTimeout means the task exceeded its total allowed runtime.
	StallCauseTimeout StallCause = "timeout"
)

// TaskProgress is one task's entry inside a progress marker.
type TaskProgress struct {
	Phase             string `json:"phase"`
	UpdatedAt         string `json:"updatedAt"`
	StartedAt         string `json:"startedAt,omitempty"`
	LastCompletedTask string `json:"lastCompletedTask,omitempty"`
}

// Progress is the aggregated view of a run's progress markers, assembled by
// ReadProgress from every file under <root>/.sdlc-v2/execution/<runID>/progress/
// plus (at lower priority) the legacy single-file marker,
// <root>/.sdlc-v2/execution/<runID>/progress.json. The exported shape is
// unchanged from the single-file era so MCP callers (execute_state's
// wave-progress action) see no difference.
type Progress struct {
	Tasks map[string]TaskProgress `json:"tasks"`
}

// progressDir returns the absolute path of a run's per-task progress
// directory — one JSON file per task, named <taskID>.json. Writing here
// (rather than to one shared file) eliminates the concurrent-write race by
// construction: distinct tasks touch distinct files, so no read-modify-write
// step or locking is needed.
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

// ReadProgress aggregates every per-task file under
// <root>/.sdlc-v2/execution/<runID>/progress/ into a single Progress, merging
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
// read-modify-written so that StartedAt (set once on first write) and
// LastCompletedTask survive across phase updates. Each task's file is
// wholly owned by that task, so no cross-task race exists.
func UpdateProgress(root, runID, taskID, phase, lastCompletedTask string) error {
	if err := validateRunID(runID); err != nil {
		return err
	}
	if !validPhases[phase] {
		return fmt.Errorf("phase %q not in bounded enum (started|reading|editing|verifying|reporting): %w", phase, ErrBadPhase)
	}

	dir := progressDir(root, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("wave: mkdir %s: %w", dir, err)
	}

	// Read existing to preserve StartedAt.
	var existing TaskProgress
	_ = fsx.ReadJSON(taskProgressPath(root, runID, taskID), &existing)

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

	return fsx.AtomicWriteJSON(taskProgressPath(root, runID, taskID), tp)
}

// ClassifyStall determines whether a task's heartbeat indicates a stall,
// a timeout, or is healthy. heartbeatTimeout is the max age of UpdatedAt
// before declaring stalled; totalTimeout is the max total runtime from
// StartedAt before declaring timeout. Timeout takes precedence when both
// conditions hold.
func ClassifyStall(tp TaskProgress, now time.Time, heartbeatTimeout, totalTimeout time.Duration) StallCause {
	if tp.UpdatedAt == "" {
		return StallCauseNone
	}
	updated, err := time.Parse("2006-01-02T15:04:05.000Z", tp.UpdatedAt)
	if err != nil {
		return StallCauseNone
	}

	// Timeout: total runtime exceeded.
	if totalTimeout > 0 && tp.StartedAt != "" {
		if started, sErr := time.Parse("2006-01-02T15:04:05.000Z", tp.StartedAt); sErr == nil {
			if now.Sub(started) > totalTimeout {
				return StallCauseTimeout
			}
		}
	}

	// Stall: heartbeat age exceeded.
	if heartbeatTimeout > 0 && now.Sub(updated) > heartbeatTimeout {
		return StallCauseStalled
	}

	return StallCauseNone
}
