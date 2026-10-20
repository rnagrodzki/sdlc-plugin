package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
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

// maxCheckedFileBytes bounds the read of a file that a tool checks before it
// writes: config.toml for the guardrail check of setup_write_sections, and the
// current mirror for dimensions_render_instructions. A real file of either
// kind holds a few KiB.
const maxCheckedFileBytes = 1 << 20

// maxCheckedFileText is maxCheckedFileBytes as text for tool descriptions.
const maxCheckedFileText = "1 MiB"

// readFileError wraps a failed stat or read of path, done before any write, in
// an InfraError. what starts the message, for example "read mirror".
func readFileError(what, path string, cause error) error {
	return &mcpserver.InfraError{
		Msg:        fmt.Sprintf("%s %s: %s", what, path, cause.Error()),
		Suggestion: fmt.Sprintf("Check that %s is a readable file, then retry. Nothing was written.", path),
		Cause:      cause,
	}
}

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
