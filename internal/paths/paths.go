// Package paths centralizes the SDLC data-directory constant so every
// internal package references a single definition instead of scattering
// ".sdlc-v2" literals across the tree.
package paths

import "path/filepath"

// DataDir is the project-level directory name that holds SDLC configuration,
// state, and execution artifacts.  All runtime path construction must use this
// constant (or ProjectDir) rather than a bare string literal.
const DataDir = ".sdlc-v2"

// LegacyDataDir is the directory name used by the previous plugin version.
// It exists so that migration / import logic can reference the old location
// without re-introducing a scattered literal.
const LegacyDataDir = ".sdlc"

// RunsSubdir is the subdirectory (under DataDir) that holds execution-state
// run files. It replaces the older "execution" subdirectory name; see
// internal/state for the legacy fallback that still reads the old location.
const RunsSubdir = "runs"

// ProjectDir returns the absolute path to the SDLC data directory for a
// given project root.
func ProjectDir(root string) string {
	return filepath.Join(root, DataDir)
}
