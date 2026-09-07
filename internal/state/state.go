// Package state provides execution-state file utilities ported from the Node.js
// state.js shared library: filename grammar, branch slug helpers, file lookup
// (delimiter-aware mtime-newest), init/write with prune-on-write, and session
// stamping.
//
// The canonical state directory lives at <root>/.sdlc/execution/. Root is
// injected by callers so that no environment or git lookup is needed here.
//
// Filename format: <prefix>-<branchSlug>-<YYYYMMDDTHHmmssZ>.json
// Accepted prefixes: ship, execute, plan (parser also accepts commit).
package state

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// Branch slug
// ---------------------------------------------------------------------------

var nonAlphaHyphen = regexp.MustCompile(`[^a-zA-Z0-9-]`)

// SlugifyBranch converts a branch name to a filesystem-safe slug by replacing
// every character that is not alphanumeric or a hyphen with '-'.
// Example: "feat/my-feature" → "feat-my-feature".
func SlugifyBranch(branch string) string {
	return nonAlphaHyphen.ReplaceAllString(branch, "-")
}

// ---------------------------------------------------------------------------
// Filename parsing
// ---------------------------------------------------------------------------

// filenameRe matches the canonical state file basename:
//
//	<prefix>-<slug>-<YYYYMMDDTHHmmssZ>.json
var filenameRe = regexp.MustCompile(
	`^(ship|execute|plan|commit)-(.+)-(\d{8}T\d{6}Z)\.json$`,
)

// parsedFilename holds the components extracted from a state file basename.
type parsedFilename struct {
	Prefix    string
	Slug      string
	Timestamp string
}

// parseStateFilename breaks a state file basename into prefix, slug, and
// timestamp. Returns nil when the name does not match the grammar.
func parseStateFilename(name string) *parsedFilename {
	m := filenameRe.FindStringSubmatch(name)
	if m == nil {
		return nil
	}
	return &parsedFilename{Prefix: m[1], Slug: m[2], Timestamp: m[3]}
}

// ---------------------------------------------------------------------------
// State type
// ---------------------------------------------------------------------------

// State is the in-memory representation of an execution-state file.
// Data is a generic map to preserve unknown pipeline-specific fields on
// round-trip (matching the JS implementation's untyped JSON handling).
type State struct {
	// Path is the absolute filesystem path to the state file.
	Path string

	// Root is the project root directory (parent of .sdlc/).
	Root string

	// Prefix is "ship", "execute", "plan", or "commit".
	Prefix string

	// BranchSlug is the slugified branch name used in the filename.
	BranchSlug string

	// Data holds the parsed JSON contents of the state file.
	Data map[string]any
}

// stateDir returns the canonical execution state directory for a root.
func stateDir(root string) string {
	return filepath.Join(root, paths.DataDir, "execution")
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

// Init creates a new state file in <root>/.sdlc/execution/ with the filename
// <prefix>-<branchSlug>-<timestamp>.json and the provided initial data.
// The creating session's ID is stamped into Data["sessionId"]; an empty
// sessionID is stored as nil (matching the JS behaviour of null).
//
// Init does NOT prune pre-existing files for the same prefix+branch (matching
// the JS initState behaviour). Use Write for prune-on-write semantics.
func Init(root, prefix, branch, sessionID string) (*State, error) {
	dir := stateDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("state: mkdir %s: %w", dir, err)
	}

	slug := SlugifyBranch(branch)
	ts := time.Now().UTC().Format("20060102T150405Z")
	name := fmt.Sprintf("%s-%s-%s.json", prefix, slug, ts)
	path := filepath.Join(dir, name)

	data := make(map[string]any)
	// Stamp sessionId (nil when empty, mirroring JS null).
	if sessionID != "" {
		data["sessionId"] = sessionID
	} else {
		data["sessionId"] = nil
	}

	if err := fsx.AtomicWriteJSON(path, data); err != nil {
		return nil, fmt.Errorf("state: init %s: %w", path, err)
	}

	return &State{
		Path:       path,
		Root:       root,
		Prefix:     prefix,
		BranchSlug: slug,
		Data:       data,
	}, nil
}

// ---------------------------------------------------------------------------
// Find
// ---------------------------------------------------------------------------

// Find locates the most recent state file matching
// <prefix>-<branchSlug>-*.json in the execution state directory (by mtime,
// newest first), reads and parses its JSON contents, and returns a *State.
//
// Returns (nil, nil) when no matching file exists (mirrors the JS null
// return). Returns a non-nil error only on I/O or JSON-parse failures.
//
// Matching uses delimiter-aware prefix: strings.HasPrefix(name, prefix+"-"+slug+"-")
// to mirror the JS findStateFile behaviour exactly.
func Find(root, prefix, branch string) (*State, error) {
	dir := stateDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("state: readdir %s: %w", dir, err)
	}

	slug := SlugifyBranch(branch)
	pat := prefix + "-" + slug + "-"

	type candidate struct {
		path  string
		mtime time.Time
	}
	var candidates []candidate

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, pat) || !strings.HasSuffix(name, ".json") {
			continue
		}
		fp := filepath.Join(dir, name)
		info, err := e.Info()
		if err != nil {
			continue // skip unstat-able entries
		}
		candidates = append(candidates, candidate{path: fp, mtime: info.ModTime()})
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Sort by mtime descending (newest first).
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].mtime.After(candidates[j].mtime)
	})

	winner := candidates[0]
	var data map[string]any
	if err := fsx.ReadJSON(winner.path, &data); err != nil {
		return nil, fmt.Errorf("state: read %s: %w", winner.path, err)
	}

	return &State{
		Path:       winner.path,
		Root:       root,
		Prefix:     prefix,
		BranchSlug: slug,
		Data:       data,
	}, nil
}

// FindAny locates the most recent state file matching <prefix>-*.json in the
// execution state directory (by mtime, newest first), regardless of which
// branch produced it, reads and parses its JSON contents, and returns a
// *State.
//
// Unlike Find, FindAny is branch-agnostic: it matches on the bare prefix
// ("<prefix>-") rather than "<prefix>-<branchSlug>-", so it accepts the most
// recent file of a given prefix across all branches. This mirrors
// detectResumeState's behaviour in the JS source when called without a
// branch argument (scripts/lib/state.js), used by probes that intentionally
// look project-wide rather than per-branch.
//
// Returns (nil, nil) when no matching file exists (mirrors Find's no-match
// convention). Returns a non-nil error only on I/O or JSON-parse failures.
//
// BranchSlug on the returned State is derived from the winning file's own
// name (via parseStateFilename), not from any caller-supplied branch, since
// FindAny accepts files from any branch.
func FindAny(root, prefix string) (*State, error) {
	dir := stateDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("state: readdir %s: %w", dir, err)
	}

	pat := prefix + "-"

	type candidate struct {
		path  string
		mtime time.Time
	}
	var candidates []candidate

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, pat) || !strings.HasSuffix(name, ".json") {
			continue
		}
		fp := filepath.Join(dir, name)
		info, err := e.Info()
		if err != nil {
			continue // skip unstat-able entries
		}
		candidates = append(candidates, candidate{path: fp, mtime: info.ModTime()})
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Sort by mtime descending (newest first).
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].mtime.After(candidates[j].mtime)
	})

	winner := candidates[0]
	var data map[string]any
	if err := fsx.ReadJSON(winner.path, &data); err != nil {
		return nil, fmt.Errorf("state: read %s: %w", winner.path, err)
	}

	branchSlug := ""
	if pf := parseStateFilename(filepath.Base(winner.path)); pf != nil {
		branchSlug = pf.Slug
	}

	return &State{
		Path:       winner.path,
		Root:       root,
		Prefix:     prefix,
		BranchSlug: branchSlug,
		Data:       data,
	}, nil
}

// ---------------------------------------------------------------------------
// Write (prune-on-write)
// ---------------------------------------------------------------------------

// Write writes st.Data to st.Path atomically, after pruning all pre-existing
// state files for the same prefix+branchSlug (except st.Path itself).
//
// Pruning uses parseStateFilename for exact slug equality (matching the JS
// pruneStateFiles behaviour), which is stricter than Find's prefix match.
func Write(st *State) error {
	dir := stateDir(st.Root)

	// Prune pre-existing files for the same prefix+slug.
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("state: readdir for prune %s: %w", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		parsed := parseStateFilename(name)
		if parsed == nil {
			continue
		}
		if parsed.Prefix != st.Prefix {
			continue
		}
		if parsed.Slug != st.BranchSlug {
			continue
		}
		fp := filepath.Join(dir, name)
		if fp == st.Path {
			continue // don't prune ourselves
		}
		_ = os.Remove(fp) // best-effort
	}

	return fsx.AtomicWriteJSON(st.Path, st.Data)
}
