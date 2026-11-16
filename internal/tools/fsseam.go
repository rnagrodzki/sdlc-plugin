package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// fsseam.go centralizes the filesystem primitives used to move large
// JSON payloads (plan snapshots, commit manifests) out of the LLM's context
// and onto disk as file-path references under .sdlc-v2/scratch/ of the main
// worktree. Tests substitute these package vars
// to simulate filesystem failures deterministically, without relying on
// platform-specific permission behavior.

// mkdirTempFunc matches os.MkdirTemp's signature.
var mkdirTempFunc = os.MkdirTemp

// writeFileFunc matches os.WriteFile's signature.
var writeFileFunc = os.WriteFile

// readFileFunc matches os.ReadFile's signature.
var readFileFunc = os.ReadFile

// maxCheckedFileBytes bounds the read of a file that a tool checks before it
// writes: config.toml for the guardrail check of setup_write_sections, the
// current mirror for dimensions_render_instructions, and each installed CI
// file that scaffold_ci, setup_prepare and validate (ci_script_drift) read for
// its version. A real file of any of these kinds holds a few KiB.
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

// scratchReadDir matches os.ReadDir's signature. The scratch sweep
// (removeStaleTempDirs) lists the scratch folder through it.
var scratchReadDir = os.ReadDir

// scratchRemoveAll matches os.RemoveAll's signature. The scratch sweep
// (removeStaleTempDirs) removes each old entry through it.
var scratchRemoveAll = os.RemoveAll

// scratchMkdirTemp makes paths.ScratchDir(root), removes its prefix folders
// older than paths.ScratchMaxAge (best effort, as removeStaleTempDirs), then
// calls mkdirTempFunc(paths.ScratchDir(root), prefix). root is the main
// worktree root. An empty root or a failed MkdirAll returns an InfraError
// whose Suggestion a caller can reuse through scratchSuggestion.
func scratchMkdirTemp(root, prefix string) (string, error) {
	if root == "" {
		// InfraError, not DomainError: every handler resolves root from the
		// server's working directory (worktree.MainRoot), never from input,
		// so an empty root is an environment fault with no cause to wrap.
		return "", &mcpserver.InfraError{
			Msg:        "scratch folder: no repo root",
			Suggestion: "Run the tool inside a git worktree of a repo that has .sdlc-v2/, then retry.",
		}
	}
	dir := paths.ScratchDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", &mcpserver.InfraError{
			Msg:        fmt.Sprintf("create scratch folder %s: %s", dir, err.Error()),
			Suggestion: fmt.Sprintf("Check that %s is a folder, not a file, with write permission and free space, then retry.", dir),
			Cause:      err,
		}
	}
	removeStaleTempDirs(dir, prefix, "", time.Now())
	return mkdirTempFunc(dir, prefix)
}

// scratchSuggestion returns the Suggestion of the scratchMkdirTemp error
// wrapped in err, or fallback when err holds no such error.
func scratchSuggestion(err error, fallback string) string {
	var ie *mcpserver.InfraError
	if errors.As(err, &ie) && ie.Suggestion != "" {
		return ie.Suggestion
	}
	return fallback
}

// writeTempJSON creates a fresh folder with the given prefix under
// paths.ScratchDir(root) (see scratchMkdirTemp), marshals payload(path) to
// JSON and writes it to <dir>/<what>.json, returning that path. payload gets
// the final path so a document can embed its own location. On failure the dir
// is removed; on success it is left in place because the calling agent reads
// the file later.
func writeTempJSON(root, prefix, what string, payload func(path string) any) (string, error) {
	dir, err := scratchMkdirTemp(root, prefix)
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
