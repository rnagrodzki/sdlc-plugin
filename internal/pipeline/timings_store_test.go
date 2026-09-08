package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

func TestTimingsStoreRecordAndEstimate(t *testing.T) {
	root := t.TempDir()
	store := NewTimingsStore(root)

	if err := store.Record("run-tests", 10*time.Second); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := store.Record("run-tests", 20*time.Second); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := store.Record("run-tests", 30*time.Second); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	est, ok := store.Estimate("run-tests")
	if !ok {
		t.Fatalf("Estimate() ok = false, want true")
	}
	if est.Samples != 3 {
		t.Errorf("Samples = %d, want 3", est.Samples)
	}
	if est.Seconds != 20 {
		t.Errorf("Seconds = %d, want 20 (median of 10,20,30)", est.Seconds)
	}
	if est.Basis != "median of 3 runs" {
		t.Errorf("Basis = %q, want %q", est.Basis, "median of 3 runs")
	}
}

func TestTimingsStoreEstimateEvenSampleCount(t *testing.T) {
	root := t.TempDir()
	store := NewTimingsStore(root)

	for _, d := range []time.Duration{10 * time.Second, 20 * time.Second, 30 * time.Second, 40 * time.Second} {
		if err := store.Record("build", d); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}

	est, ok := store.Estimate("build")
	if !ok {
		t.Fatalf("Estimate() ok = false, want true")
	}
	// median of 10,20,30,40 -> floor((20+30)/2) = 25
	if est.Seconds != 25 {
		t.Errorf("Seconds = %d, want 25", est.Seconds)
	}
}

func TestTimingsStoreRollingWindow(t *testing.T) {
	root := t.TempDir()
	store := NewTimingsStore(root)

	// Record 12 samples: 1s, 2s, ..., 12s. Only the last 10 (3..12) should
	// be retained.
	for i := 1; i <= 12; i++ {
		if err := store.Record("deploy", time.Duration(i)*time.Second); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}

	est, ok := store.Estimate("deploy")
	if !ok {
		t.Fatalf("Estimate() ok = false, want true")
	}
	if est.Samples != 10 {
		t.Errorf("Samples = %d, want 10 (rolling window cap)", est.Samples)
	}
	// Retained samples should be 3..12, median = floor((7+8)/2) = 7.
	if est.Seconds != 7 {
		t.Errorf("Seconds = %d, want 7", est.Seconds)
	}
}

func TestTimingsStoreEstimateNoSamples(t *testing.T) {
	root := t.TempDir()
	store := NewTimingsStore(root)

	_, ok := store.Estimate("never-recorded")
	if ok {
		t.Error("Estimate() ok = true, want false for a key with no samples")
	}
}

func TestTimingsStoreEstimateMissingFile(t *testing.T) {
	root := t.TempDir()
	store := NewTimingsStore(root)

	// No file has been written at all yet.
	if _, err := os.Stat(store.path); !os.IsNotExist(err) {
		t.Fatalf("expected no store file to exist yet, stat err = %v", err)
	}

	_, ok := store.Estimate("anything")
	if ok {
		t.Error("Estimate() ok = true, want false when store file is missing")
	}
}

func TestTimingsStoreCorruptFile(t *testing.T) {
	root := t.TempDir()
	store := NewTimingsStore(root)

	if err := os.MkdirAll(filepath.Dir(store.path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(store.path, []byte("not valid json{{{"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// Estimate must report ok=false, no error surfaced (no error return at all).
	_, ok := store.Estimate("run-tests")
	if ok {
		t.Error("Estimate() ok = true, want false against a corrupt store file")
	}

	// Record must recreate the file rather than failing.
	if err := store.Record("run-tests", 15*time.Second); err != nil {
		t.Fatalf("Record() error = %v, want nil (corrupt file should be recreated)", err)
	}

	est, ok := store.Estimate("run-tests")
	if !ok {
		t.Fatalf("Estimate() ok = false after recreate, want true")
	}
	if est.Seconds != 15 {
		t.Errorf("Seconds = %d, want 15", est.Seconds)
	}
}

func TestTimingsStoreHumanWaitStepsExcluded(t *testing.T) {
	root := t.TempDir()
	store := NewTimingsStore(root)

	if err := store.Record("await-remote-review", 3*time.Hour); err != nil {
		t.Fatalf("Record() error = %v, want nil for a human-wait key", err)
	}

	_, ok := store.Estimate("await-remote-review")
	if ok {
		t.Error("Estimate() ok = true, want false for a HumanWaitSteps key")
	}
}

func TestTimingsStorePath(t *testing.T) {
	root := t.TempDir()
	store := NewTimingsStore(root)

	want := filepath.Join(paths.ProjectDir(root), "timings.json")
	if store.path != want {
		t.Errorf("store.path = %q, want %q", store.path, want)
	}
}

func TestTimingsStoreAtomicWritePersists(t *testing.T) {
	root := t.TempDir()
	store := NewTimingsStore(root)

	if err := store.Record("ship", 5*time.Second); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	// Confirm no leftover temp files after the atomic write, and that the
	// real store file exists.
	entries, err := os.ReadDir(filepath.Dir(store.path))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "timings.json" {
		t.Errorf("directory entries = %v, want exactly [timings.json]", entries)
	}

	// A fresh store instance pointed at the same root should see the
	// persisted sample.
	reopened := NewTimingsStore(root)
	est, ok := reopened.Estimate("ship")
	if !ok {
		t.Fatalf("Estimate() ok = false, want true after reopening store")
	}
	if est.Seconds != 5 {
		t.Errorf("Seconds = %d, want 5", est.Seconds)
	}
}
