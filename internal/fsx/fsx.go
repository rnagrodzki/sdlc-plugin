// Package fsx provides small filesystem helpers shared across the sdlc
// plugin's internal packages: atomic JSON and TOML writers, and JSON and
// TOML readers that distinguish not-found, parse, and I/O failures via
// wrapped sentinel errors.
package fsx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// ErrNotFound is wrapped into the error returned by ReadJSON and ReadTOML
// when the target file does not exist.
var ErrNotFound = errors.New("fsx: not found")

// ErrParse is wrapped into the error returned by ReadJSON and ReadTOML when
// the target file's contents are not valid JSON/TOML.
var ErrParse = errors.New("fsx: parse error")

// AtomicWriteJSON marshals v as indented JSON and writes it to path
// atomically: the encoded bytes are written to a temporary sibling file
// (<path>.tmp-<rand>) in the same directory, the sibling is closed and
// flushed, and only then renamed into place with os.Rename. Because rename
// replaces the destination in a single filesystem operation, a reader of
// path never observes a partially written file, and a failure at any step
// leaves any pre-existing path untouched.
func AtomicWriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("fsx: marshal %s: %w", path, err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("fsx: create temp file for %s: %w", path, err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("fsx: write temp file for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("fsx: close temp file for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("fsx: rename temp file into place for %s: %w", path, err)
	}
	return nil
}

// ReadJSON reads path and unmarshals its JSON contents into out. The
// returned error wraps ErrNotFound when path does not exist, ErrParse when
// the contents are not valid JSON, and the raw underlying error (still
// wrapped, so errors.Is/As still work against it) for any other I/O
// failure, so callers can distinguish the three cases.
func ReadJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("fsx: %s: %w", path, ErrNotFound)
		}
		return fmt.Errorf("fsx: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("fsx: %s: %w: %v", path, ErrParse, err)
	}
	return nil
}

// AtomicWriteTOML marshals v as TOML and writes it to path atomically,
// using the same temp-file-plus-rename sequence as AtomicWriteJSON: a
// reader of path never observes a partially written file, and a failure at
// any step leaves any pre-existing path untouched.
func AtomicWriteTOML(path string, v any) error {
	data, err := toml.Marshal(v)
	if err != nil {
		return fmt.Errorf("fsx: marshal %s: %w", path, err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("fsx: create temp file for %s: %w", path, err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("fsx: write temp file for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("fsx: close temp file for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("fsx: rename temp file into place for %s: %w", path, err)
	}
	return nil
}

// ReadTOML reads path and unmarshals its TOML contents into out. The
// returned error wraps ErrNotFound when path does not exist, ErrParse when
// the contents are not valid TOML, and the raw underlying error (still
// wrapped, so errors.Is/As still work against it) for any other I/O
// failure, so callers can distinguish the three cases — identical semantics
// to ReadJSON.
//
// After a successful unmarshal, out is normalized in place via
// normalizeTOMLTypes: the pelletier/go-toml/v2 decoder produces int64 for
// TOML integers, but every downstream config consumer that walks
// map[string]any expects float64 (the type encoding/json produces for JSON
// numbers). Normalizing here keeps that difference invisible to callers.
func ReadTOML(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("fsx: %s: %w", path, ErrNotFound)
		}
		return fmt.Errorf("fsx: read %s: %w", path, err)
	}
	if err := toml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("fsx: %s: %w: %v", path, ErrParse, err)
	}
	normalizeTOMLTypes(out)
	return nil
}

// normalizeTOMLTypes recursively walks v — a *map[string]any, map[string]any,
// or []any, as produced by decoding TOML into an any-typed destination — and
// rewrites every int64 value to float64 in place. Nested maps and slices are
// recursed into. Any other type (including typed struct destinations, which
// toml.Unmarshal populates directly without producing int64 values in the
// first place) is left untouched.
func normalizeTOMLTypes(v any) {
	switch val := v.(type) {
	case *map[string]any:
		if val != nil {
			normalizeTOMLMap(*val)
		}
	case map[string]any:
		normalizeTOMLMap(val)
	case []any:
		normalizeTOMLSlice(val)
	}
}

// normalizeTOMLMap rewrites int64 values to float64 in place across m,
// recursing into nested maps and slices.
func normalizeTOMLMap(m map[string]any) {
	for k, v := range m {
		m[k] = normalizeTOMLValue(v)
	}
}

// normalizeTOMLSlice rewrites int64 values to float64 in place across s,
// recursing into nested maps and slices.
func normalizeTOMLSlice(s []any) {
	for i, v := range s {
		s[i] = normalizeTOMLValue(v)
	}
}

// normalizeTOMLValue returns v with int64 rewritten to float64, recursing
// into nested map[string]any and []any values first.
func normalizeTOMLValue(v any) any {
	switch val := v.(type) {
	case int64:
		return float64(val)
	case map[string]any:
		normalizeTOMLMap(val)
		return val
	case []any:
		normalizeTOMLSlice(val)
		return val
	default:
		return val
	}
}
