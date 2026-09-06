package wave

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	Phase     string `json:"phase"`
	UpdatedAt string `json:"updatedAt"`
}

// Progress is the per-run progress marker, serialised as JSON to
// <root>/.sdlc/execution/<runID>/progress.json. Byte-shape-compatible
// with the Node source's per-wave marker (wave-progress.js).
type Progress struct {
	Tasks map[string]TaskProgress `json:"tasks"`
}

// progressPath returns the absolute path of a run's progress marker.
func progressPath(root, runID string) string {
	return filepath.Join(executionDir(root, runID), "progress.json")
}

// ReadProgress reads and parses a run's progress marker. If the file is
// missing, unreadable, or does not contain a valid JSON object, an empty
// Progress is returned — ReadProgress never returns an error for I/O or
// parse problems, matching wave-progress.js::readMarkerFile's never-throw
// contract. The only error it returns is ErrBadRunID.
func ReadProgress(root, runID string) (*Progress, error) {
	if err := validateRunID(runID); err != nil {
		return nil, err
	}

	p := &Progress{Tasks: map[string]TaskProgress{}}
	fp := progressPath(root, runID)

	err := fsx.ReadJSON(fp, p)
	if err != nil {
		// Swallow not-found and parse errors — return empty progress.
		if errors.Is(err, fsx.ErrNotFound) || errors.Is(err, fsx.ErrParse) {
			return &Progress{Tasks: map[string]TaskProgress{}}, nil
		}
		// Other I/O errors also swallowed per the Node source contract.
		return &Progress{Tasks: map[string]TaskProgress{}}, nil
	}
	// Normalize nil Tasks map.
	if p.Tasks == nil {
		p.Tasks = map[string]TaskProgress{}
	}
	return p, nil
}

// UpdateProgress merges a single task's phase into the run's progress
// marker and writes it atomically via fsx.AtomicWriteJSON. It validates
// runID and phase before any path construction or file I/O.
func UpdateProgress(root, runID, taskID, phase string) error {
	if err := validateRunID(runID); err != nil {
		return err
	}
	if !validPhases[phase] {
		return fmt.Errorf("phase %q not in bounded enum (started|reading|editing|verifying|reporting): %w", phase, ErrBadPhase)
	}

	dir := executionDir(root, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("wave: mkdir %s: %w", dir, err)
	}

	fp := progressPath(root, runID)

	// Read existing progress (swallowing errors, same as ReadProgress).
	existing := &Progress{Tasks: map[string]TaskProgress{}}
	if err := fsx.ReadJSON(fp, existing); err == nil {
		if existing.Tasks == nil {
			existing.Tasks = map[string]TaskProgress{}
		}
	}

	existing.Tasks[taskID] = TaskProgress{
		Phase:     phase,
		UpdatedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
	}

	return fsx.AtomicWriteJSON(fp, existing)
}
