package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// timingsFileName is the name of the rolling-timing-history file, stored
// under the project's SDLC data directory.
const timingsFileName = "timings.json"

// maxSamples is the size of the rolling window of durations retained per
// step key.
const maxSamples = 10

// TimingsStore persists a rolling window of the most recent step durations
// per key, used to derive ETA estimates for future runs.
//
// The store is best-effort: a corrupt or missing backing file must never
// fail a pipeline call. Record treats a corrupt file as empty and
// recreates it; Estimate treats a corrupt or missing file as "no samples"
// and reports ok=false. Neither surfaces a parse error to the caller.
type TimingsStore struct {
	path string
}

// NewTimingsStore returns a TimingsStore backed by
// <projectRoot>/.sdlc-v2/timings.json.
func NewTimingsStore(projectRoot string) *TimingsStore {
	return &TimingsStore{
		path: filepath.Join(paths.ProjectDir(projectRoot), timingsFileName),
	}
}

// timingsData is the on-disk shape: step key -> rolling window of sample
// durations in whole seconds, oldest first.
type timingsData map[string][]int

// load reads and parses the store file. A missing file or invalid JSON
// yields an empty dataset rather than an error, per the store's
// best-effort contract.
func (s *TimingsStore) load() timingsData {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return timingsData{}
	}
	var data timingsData
	if err := json.Unmarshal(raw, &data); err != nil || data == nil {
		return timingsData{}
	}
	return data
}

// save atomically writes data to the store file (temp file + rename),
// mirroring the atomic-write pattern used by WriteFactsheet in
// internal/wave/factsheet.go.
func (s *TimingsStore) save(data timingsData) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("pipeline: mkdir %s: %w", dir, err)
	}

	content, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("pipeline: marshal timings: %w", err)
	}

	tmp, err := os.CreateTemp(dir, timingsFileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("pipeline: create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("pipeline: write temp file %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("pipeline: close temp file %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("pipeline: rename temp into place for %s: %w", s.path, err)
	}
	return nil
}

// Record appends a duration sample for key, keeping only the most recent
// maxSamples (10) entries. Keys in HumanWaitSteps are silently ignored —
// they are never recorded. The write is atomic; if the existing store
// file is corrupt, it is discarded and recreated rather than surfaced as
// an error.
func (s *TimingsStore) Record(key string, d time.Duration) error {
	if HumanWaitSteps[key] {
		return nil
	}

	data := s.load()
	samples := append(data[key], int(d.Round(time.Second).Seconds()))
	if len(samples) > maxSamples {
		samples = samples[len(samples)-maxSamples:]
	}
	data[key] = samples

	return s.save(data)
}

// Estimate returns a duration estimate for key derived from the median of
// its recorded samples, with Basis describing the derivation as "median of
// N runs". ok is false when key has no recorded samples, the store file is
// missing or corrupt, or key is in HumanWaitSteps.
func (s *TimingsStore) Estimate(key string) (Estimate, bool) {
	if HumanWaitSteps[key] {
		return Estimate{}, false
	}

	samples := s.load()[key]
	if len(samples) == 0 {
		return Estimate{}, false
	}

	sorted := make([]int, len(samples))
	copy(sorted, samples)
	sort.Ints(sorted)

	return Estimate{
		Seconds: median(sorted),
		Samples: len(sorted),
		Basis:   fmt.Sprintf("median of %d runs", len(sorted)),
	}, true
}

// median returns the median of an already-sorted, non-empty slice of ints.
// For an even-length slice it returns the floor of the average of the two
// middle values.
func median(sorted []int) int {
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
