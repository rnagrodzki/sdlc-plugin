// Package fsx provides small filesystem helpers shared across the sdlc
// plugin's internal packages: an atomic JSON writer and a JSON reader that
// distinguishes not-found, parse, and I/O failures via wrapped sentinel
// errors.
package fsx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotFound is wrapped into the error returned by ReadJSON when the target
// file does not exist.
var ErrNotFound = errors.New("fsx: not found")

// ErrParse is wrapped into the error returned by ReadJSON when the target
// file's contents are not valid JSON.
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
