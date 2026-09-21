package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// fsseam.go centralizes the three filesystem primitives used to move large
// JSON payloads (plan snapshots, commit manifests) out of the LLM's context
// and onto disk as file-path references. Tests substitute these package vars
// to simulate filesystem failures deterministically, without relying on
// platform-specific permission behavior.

// mkdirTempFunc matches os.MkdirTemp's signature.
var mkdirTempFunc = os.MkdirTemp

// writeFileFunc matches os.WriteFile's signature.
var writeFileFunc = os.WriteFile

// readFileFunc matches os.ReadFile's signature.
var readFileFunc = os.ReadFile

// writeTempJSON creates a fresh temp dir with the given prefix, marshals
// payload(path) to JSON and writes it to <dir>/<what>.json, returning that
// path. payload gets the final path so a document can embed its own location.
// On failure the dir is removed; on success it is left in place because the
// calling agent reads the file later.
func writeTempJSON(prefix, what string, payload func(path string) any) (string, error) {
	dir, err := mkdirTempFunc("", prefix)
	if err != nil {
		return "", fmt.Errorf("create %s temp dir: %w", what, err)
	}

	path := filepath.Join(dir, what+".json")
	data, err := json.Marshal(payload(path))
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("marshal %s: %w", what, err)
	}

	if err := writeFileFunc(path, data, 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("write %s file %q: %w", what, path, err)
	}

	return path, nil
}
