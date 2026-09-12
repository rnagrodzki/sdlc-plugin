package fsx

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
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

func TestAtomicWriteTOML_WritesAndReadsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.toml")

	in := sample{Name: "widget", Count: 3}
	if err := AtomicWriteTOML(path, in); err != nil {
		t.Fatalf("AtomicWriteTOML: unexpected error: %v", err)
	}

	var out sample
	if err := ReadTOML(path, &out); err != nil {
		t.Fatalf("ReadTOML: unexpected error: %v", err)
	}
	if out != in {
		t.Fatalf("round trip mismatch: got %+v, want %+v", out, in)
	}
}

func TestAtomicWriteTOML_NoLeftoverTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.toml")

	if err := AtomicWriteTOML(path, sample{Name: "a", Count: 1}); err != nil {
		t.Fatalf("AtomicWriteTOML: unexpected error: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "sample.toml" {
		t.Fatalf("expected only sample.toml in %s, got %v", dir, entries)
	}
}

// TestAtomicWriteTOML_FailedMarshalLeavesDestinationUntouched injects a
// failure before any bytes reach disk (an unmarshalable value) and checks
// that a pre-existing destination file is left byte-for-byte untouched, and
// that no temporary sibling is left behind.
func TestAtomicWriteTOML_FailedMarshalLeavesDestinationUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.toml")

	original := []byte("name = 'original'\ncount = 9\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("seed WriteFile: %v", err)
	}

	// A function value cannot be marshaled to TOML.
	err := AtomicWriteTOML(path, map[string]any{"bad": func() {}})
	if err == nil {
		t.Fatalf("AtomicWriteTOML: expected error for unmarshalable value, got nil")
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
	if len(entries) != 1 || entries[0].Name() != "sample.toml" {
		t.Fatalf("expected only sample.toml in %s after failed write, got %v", dir, entries)
	}
}

// TestAtomicWriteTOML_FailedTempCreateLeavesDestinationUntouched injects a
// failure at the temp-file-creation step (read-only directory) and checks
// that the pre-existing destination is left untouched and no partial file
// is observable. Skipped when running as root, since root bypasses
// directory permission checks.
func TestAtomicWriteTOML_FailedTempCreateLeavesDestinationUntouched(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.toml")

	original := []byte("name = 'original'\ncount = 9\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("seed WriteFile: %v", err)
	}

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	err := AtomicWriteTOML(path, sample{Name: "new", Count: 1})
	if err == nil {
		t.Fatalf("AtomicWriteTOML: expected error when temp dir is read-only, got nil")
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

func TestReadTOML_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.toml")

	var out sample
	err := ReadTOML(path, &out)
	if err == nil {
		t.Fatalf("ReadTOML: expected error for missing file, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadTOML: expected errors.Is(err, ErrNotFound), got %v", err)
	}
	if errors.Is(err, ErrParse) {
		t.Fatalf("ReadTOML: not-found error should not also match ErrParse: %v", err)
	}
}

func TestReadTOML_ParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(path, []byte("not valid toml{{{"), 0o644); err != nil {
		t.Fatalf("seed WriteFile: %v", err)
	}

	var out sample
	err := ReadTOML(path, &out)
	if err == nil {
		t.Fatalf("ReadTOML: expected error for invalid TOML, got nil")
	}
	if !errors.Is(err, ErrParse) {
		t.Fatalf("ReadTOML: expected errors.Is(err, ErrParse), got %v", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadTOML: parse error should not also match ErrNotFound: %v", err)
	}
}

// TestReadTOML_IOErrorIsDistinctFromSentinels injects a permission-denied
// I/O error (unreadable file) and checks that it is wrapped but does not
// match either sentinel, so callers can tell it apart from not-found and
// parse-error cases. Skipped when running as root, since root bypasses file
// permission checks.
func TestReadTOML_IOErrorIsDistinctFromSentinels(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions are not enforced")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "unreadable.toml")
	if err := os.WriteFile(path, []byte("name = 'a'\ncount = 1\n"), 0o644); err != nil {
		t.Fatalf("seed WriteFile: %v", err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o644) })

	var out sample
	err := ReadTOML(path, &out)
	if err == nil {
		t.Fatalf("ReadTOML: expected error for unreadable file, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadTOML: permission error should not match ErrNotFound: %v", err)
	}
	if errors.Is(err, ErrParse) {
		t.Fatalf("ReadTOML: permission error should not match ErrParse: %v", err)
	}
}

func TestAtomicWriteTOML_ProducesValidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.toml")

	if err := AtomicWriteTOML(path, sample{Name: "widget", Count: 3}); err != nil {
		t.Fatalf("AtomicWriteTOML: unexpected error: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var roundTrip map[string]any
	if err := toml.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("written file is not valid TOML: %v", err)
	}
}

// TestReadTOML_NormalizesInt64ToFloat64 verifies that ReadTOML's
// normalization step converts every int64 the TOML decoder produces
// (top-level, inside a nested table, and inside an array of tables) to
// float64, matching the type encoding/json produces for JSON numbers so
// downstream map[string]any consumers see one consistent numeric type
// regardless of which format the config was read from.
func TestReadTOML_NormalizesInt64ToFloat64(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.toml")

	raw := "count = 3\n\n" +
		"[nested]\n" +
		"value = 7\n\n" +
		"[[items]]\n" +
		"n = 1\n\n" +
		"[[items]]\n" +
		"n = 2\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("seed WriteFile: %v", err)
	}

	var out map[string]any
	if err := ReadTOML(path, &out); err != nil {
		t.Fatalf("ReadTOML: unexpected error: %v", err)
	}

	if v, ok := out["count"].(float64); !ok || v != 3 {
		t.Fatalf("count: expected float64(3), got %#v (%T)", out["count"], out["count"])
	}

	nested, ok := out["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested: expected map[string]any, got %#v", out["nested"])
	}
	if v, ok := nested["value"].(float64); !ok || v != 7 {
		t.Fatalf("nested.value: expected float64(7), got %#v (%T)", nested["value"], nested["value"])
	}

	items, ok := out["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items: expected []any of length 2, got %#v", out["items"])
	}
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("items[%d]: expected map[string]any, got %#v", i, item)
		}
		if _, ok := m["n"].(float64); !ok {
			t.Fatalf("items[%d].n: expected float64, got %#v (%T)", i, m["n"], m["n"])
		}
	}
}

func TestNormalizeTOMLTypes_HandlesPointerToMap(t *testing.T) {
	m := map[string]any{
		"a": int64(1),
		"b": []any{int64(2), int64(3)},
		"c": map[string]any{"d": int64(4)},
	}

	normalizeTOMLTypes(&m)

	if v, ok := m["a"].(float64); !ok || v != 1 {
		t.Fatalf("a: expected float64(1), got %#v (%T)", m["a"], m["a"])
	}
	b, ok := m["b"].([]any)
	if !ok || len(b) != 2 {
		t.Fatalf("b: expected []any of length 2, got %#v", m["b"])
	}
	for i, v := range b {
		if _, ok := v.(float64); !ok {
			t.Fatalf("b[%d]: expected float64, got %#v (%T)", i, v, v)
		}
	}
	c, ok := m["c"].(map[string]any)
	if !ok {
		t.Fatalf("c: expected map[string]any, got %#v", m["c"])
	}
	if v, ok := c["d"].(float64); !ok || v != 4 {
		t.Fatalf("c.d: expected float64(4), got %#v (%T)", c["d"], c["d"])
	}
}
