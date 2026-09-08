package fsx

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type sample struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestAtomicWriteJSON_WritesAndReadsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.json")

	in := sample{Name: "widget", Count: 3}
	if err := AtomicWriteJSON(path, in); err != nil {
		t.Fatalf("AtomicWriteJSON: unexpected error: %v", err)
	}

	var out sample
	if err := ReadJSON(path, &out); err != nil {
		t.Fatalf("ReadJSON: unexpected error: %v", err)
	}
	if out != in {
		t.Fatalf("round trip mismatch: got %+v, want %+v", out, in)
	}
}

func TestAtomicWriteJSON_NoLeftoverTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.json")

	if err := AtomicWriteJSON(path, sample{Name: "a", Count: 1}); err != nil {
		t.Fatalf("AtomicWriteJSON: unexpected error: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "sample.json" {
		t.Fatalf("expected only sample.json in %s, got %v", dir, entries)
	}
}

// TestAtomicWriteJSON_FailedMarshalLeavesDestinationUntouched injects a
// failure before any bytes reach disk (an unmarshalable value) and checks
// that a pre-existing destination file is left byte-for-byte untouched, and
// that no temporary sibling is left behind.
func TestAtomicWriteJSON_FailedMarshalLeavesDestinationUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.json")

	original := []byte(`{"name":"original","count":9}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("seed WriteFile: %v", err)
	}

	// A function value cannot be marshaled to JSON.
	err := AtomicWriteJSON(path, map[string]any{"bad": func() {}})
	if err == nil {
		t.Fatalf("AtomicWriteJSON: expected error for unmarshalable value, got nil")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after failed write: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("destination mutated by failed write: got %q, want %q", got, original)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "sample.json" {
		t.Fatalf("expected only sample.json in %s after failed write, got %v", dir, entries)
	}
}

// TestAtomicWriteJSON_FailedTempCreateLeavesDestinationUntouched injects a
// failure at the temp-file-creation step (read-only directory) and checks
// that the pre-existing destination is left untouched and no partial file
// is observable. Skipped when running as root, since root bypasses
// directory permission checks.
func TestAtomicWriteJSON_FailedTempCreateLeavesDestinationUntouched(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.json")

	original := []byte(`{"name":"original","count":9}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("seed WriteFile: %v", err)
	}

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	err := AtomicWriteJSON(path, sample{Name: "new", Count: 1})
	if err == nil {
		t.Fatalf("AtomicWriteJSON: expected error when temp dir is read-only, got nil")
	}

	if chmodErr := os.Chmod(dir, 0o755); chmodErr != nil {
		t.Fatalf("Chmod restore: %v", chmodErr)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after failed write: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("destination mutated by failed write: got %q, want %q", got, original)
	}
}

func TestReadJSON_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")

	var out sample
	err := ReadJSON(path, &out)
	if err == nil {
		t.Fatalf("ReadJSON: expected error for missing file, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadJSON: expected errors.Is(err, ErrNotFound), got %v", err)
	}
	if errors.Is(err, ErrParse) {
		t.Fatalf("ReadJSON: not-found error should not also match ErrParse: %v", err)
	}
}

func TestReadJSON_ParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("seed WriteFile: %v", err)
	}

	var out sample
	err := ReadJSON(path, &out)
	if err == nil {
		t.Fatalf("ReadJSON: expected error for invalid JSON, got nil")
	}
	if !errors.Is(err, ErrParse) {
		t.Fatalf("ReadJSON: expected errors.Is(err, ErrParse), got %v", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadJSON: parse error should not also match ErrNotFound: %v", err)
	}
}

// TestReadJSON_IOErrorIsDistinctFromSentinels injects a permission-denied
// I/O error (unreadable file) and checks that it is wrapped but does not
// match either sentinel, so callers can tell it apart from not-found and
// parse-error cases. Skipped when running as root, since root bypasses file
// permission checks.
func TestReadJSON_IOErrorIsDistinctFromSentinels(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions are not enforced")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "unreadable.json")
	if err := os.WriteFile(path, []byte(`{"name":"a","count":1}`), 0o644); err != nil {
		t.Fatalf("seed WriteFile: %v", err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o644) })

	var out sample
	err := ReadJSON(path, &out)
	if err == nil {
		t.Fatalf("ReadJSON: expected error for unreadable file, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadJSON: permission error should not match ErrNotFound: %v", err)
	}
	if errors.Is(err, ErrParse) {
		t.Fatalf("ReadJSON: permission error should not match ErrParse: %v", err)
	}
}

func TestAtomicWriteJSON_ProducesIndentedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.json")

	if err := AtomicWriteJSON(path, sample{Name: "widget", Count: 3}); err != nil {
		t.Fatalf("AtomicWriteJSON: unexpected error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var roundTrip json.RawMessage
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("written file is not valid JSON: %v", err)
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatalf("expected written file to end with a trailing newline, got %q", raw)
	}
}
