// Package paths centralizes the SDLC data-directory constant so every
// internal package references a single definition instead of scattering
// ".sdlc-v2" literals across the tree.
package paths

import (
	"os"
	"path/filepath"
)

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

// HistorySubdir is the subdirectory (under DataDir) that holds the durable
// cross-run history store: the run log and the deferred-issue backlog. It
// outlives state-file GC, so several packages read and write it; they must
// join it through HistoryDir rather than repeating the literal.
const HistorySubdir = "history"

// Remaining DataDir entries. Each constant names one file or subdirectory
// directly under DataDir. Every entry the MCP server or hooks write to
// DataDir must appear in exactly one of LinkedStateEntries or
// UnlinkedStateEntries below — see TestStateEntryListsCoverEveryEntry.
const (
	ReportsSubdir          = "reports"
	EvidenceSubdir         = "evidence"
	LearningsSubdir        = "learnings"
	ReviewsSubdir          = "reviews"
	StateArtifactsSubdir   = "state"
	TimingsFile            = "timings.json"
	ConfigFile             = "config.toml"
	GitignoreFile          = ".gitignore"
	ReviewDimensionsSubdir = "review-dimensions"
	LocalConfigFile        = "local.toml"
	LegacyLocalJSONFile    = "local.json"
	LegacyConfigJSONFile   = "config.json"
	JiraTemplatesSubdir    = "jira-templates"
	LegacyExecutionSubdir  = "execution"
	OpenspecStagingSubdir  = "openspec-staging"
	PlanTemplateFile       = "plan-template.md"
	PRTemplateFile         = "pr-template.md"
	ScratchSubdir          = "scratch"
	BackupsSubdir          = "backups"
	// PreplanSubdir is the subdirectory (under DataDir) that holds the preplan
	// files, one <slug>.md file for each topic. A linked worktree links it to
	// the main worktree through LinkedStateEntries.
	PreplanSubdir = "preplan"
	// RunArchiveSubdir is the subdirectory (under DataDir) that holds archived
	// runs, one <runId> folder for each run. A linked worktree links it to the
	// main worktree through LinkedStateEntries.
	RunArchiveSubdir = "run-archive"
	// BakSuffix is a suffix pattern (matches "*.bak"), not a named entry, so
	// it is never a list member — see UnlinkedStateEntries.
	BakSuffix = ".bak"
)

// LinkedStateEntries are the run-generated DataDir entries that a linked
// worktree symlinks to the same entry under the main worktree's DataDir.
var LinkedStateEntries = []string{
	RunsSubdir, ReportsSubdir, HistorySubdir, EvidenceSubdir,
	LearningsSubdir, ReviewsSubdir, StateArtifactsSubdir, TimingsFile,
	PreplanSubdir, RunArchiveSubdir,
}

// UnlinkedStateEntries are the DataDir entries that are never linked: tracked
// config, user-written templates, legacy locations, and branch-local state.
var UnlinkedStateEntries = []string{
	ConfigFile, GitignoreFile, ReviewDimensionsSubdir, LocalConfigFile,
	LegacyLocalJSONFile, LegacyConfigJSONFile, JiraTemplatesSubdir, LegacyExecutionSubdir,
	OpenspecStagingSubdir, PlanTemplateFile, PRTemplateFile, ScratchSubdir, BackupsSubdir,
}

// ProjectDir returns the absolute path to the SDLC data directory for a
// given project root.
func ProjectDir(root string) string {
	return filepath.Join(root, DataDir)
}

// HistoryDir returns the absolute path to the durable history directory for a
// given project root. It is what internal/history.NewFileWriter expects; the
// individual file names inside it (runs.jsonl, deferred.json) belong to that
// package and are reached through FileWriter.RunsPath / FileWriter.DeferredPath.
func HistoryDir(root string) string {
	return filepath.Join(root, DataDir, HistorySubdir)
}

// CacheDir returns the user cache root shared with sdlc-launcher.sh:
// $SDLC_CACHE_DIR when set, else <home>/.sdlc-cache, else ./.sdlc-cache.
// Callers join a subdirectory (e.g. "jira") onto the result themselves;
// this function only resolves the shared root.
func CacheDir() string {
	if dir := os.Getenv("SDLC_CACHE_DIR"); dir != "" {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".sdlc-cache")
	}
	return filepath.Join(".", ".sdlc-cache")
}
