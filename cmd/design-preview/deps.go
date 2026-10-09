package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

// maxDependenciesBytes is the largest dependencies file LoadDependencies
// accepts. A bigger file is a mistake, not a design record.
const maxDependenciesBytes = 256 << 10

// dependencyIDPattern is the shape of a Dependency ID: the letter D and a number.
var dependencyIDPattern = regexp.MustCompile(`^D[0-9]+$`)

// Dependency is one record of design/dashboard/dependencies.json.
type Dependency struct {
	ID       string          `json:"id"`       // ^D[0-9]+$, unique in the file
	Kind     string          `json:"kind"`     // "data" | "state-write" | "flow"
	Need     string          `json:"need"`     // one sentence, not empty, one line
	Elements []string        `json:"elements"` // CSS selectors; may be empty
	Sample   json.RawMessage `json:"sample,omitempty"`
}

// LoadDependencies reads and validates path. The file root is a JSON array.
// Every error names path. An error for a single record also names the
// record, counted from 1 in file order.
func LoadDependencies(path string) ([]Dependency, error) {
	data, err := readDependenciesFile(path)
	if err != nil {
		return nil, err
	}

	// Decoding into RawMessage first checks the JSON syntax and lets the
	// root check run before the record decode, so a non-array root gets its
	// own error and not a type-mismatch error.
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: not valid JSON: %w", path, err)
	}
	if first := bytes.TrimLeft(raw, " \t\r\n"); len(first) == 0 || first[0] != '[' {
		return nil, fmt.Errorf("%s: root must be a JSON array", path)
	}

	var deps []Dependency
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&deps); err != nil {
		return nil, fmt.Errorf("%s: not valid JSON: %w", path, err)
	}

	seen := make(map[string]bool, len(deps))
	for i := range deps {
		if err := validateDependency(&deps[i], seen); err != nil {
			return nil, fmt.Errorf("%s: record %d: %w", path, i+1, err)
		}
		if deps[i].Elements == nil {
			deps[i].Elements = []string{}
		}
	}

	return deps, nil
}

// readDependenciesFile reads path with a size limit. It reports a missing
// file, a file over maxDependenciesBytes and any other read failure.
func readDependenciesFile(path string) ([]byte, error) {
	data, err := readBounded(path, maxDependenciesBytes)
	if err != nil {
		return nil, pathError(path, err)
	}

	return data, nil
}

// tooLargeError is the error of readBounded for a file over its limit.
type tooLargeError struct {
	max int64 // the limit in bytes
}

// Error gives "larger than <n> KiB" or "larger than <n> MiB".
func (e *tooLargeError) Error() string {
	if e.max >= 1<<20 && e.max%(1<<20) == 0 {
		return fmt.Sprintf("larger than %d MiB", e.max>>20)
	}

	return fmt.Sprintf("larger than %d KiB", e.max>>10)
}

// readBounded reads path and returns a *tooLargeError when the file holds
// more than max bytes. Other errors are the raw os errors; the caller names
// the file, for example with pathError.
func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Read one byte more than the limit: a longer result means the file is too large.
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, &tooLargeError{max: max}
	}

	return data, nil
}

// pathError turns a file failure into an error that names name once. A
// missing file gives "<name>: not found". An *os.PathError repeats the path,
// so only its cause is kept.
func pathError(name string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s: not found", name)
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}

	return fmt.Errorf("%s: %w", name, err)
}

// validateDependency checks one record and adds its ID to seen. The error
// holds the broken rule only. The caller adds the path and the record number.
func validateDependency(d *Dependency, seen map[string]bool) error {
	if !dependencyIDPattern.MatchString(d.ID) {
		return errors.New("id must match D<number>")
	}
	if seen[d.ID] {
		return fmt.Errorf("duplicate id %s", d.ID)
	}
	seen[d.ID] = true

	switch d.Kind {
	case "data", "state-write", "flow":
	default:
		return errors.New("kind must be data, state-write or flow")
	}

	if strings.TrimSpace(d.Need) == "" || strings.ContainsAny(d.Need, "\r\n") {
		return errors.New("need must be one non-empty line")
	}

	return nil
}
