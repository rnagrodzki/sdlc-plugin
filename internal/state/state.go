// Package state provides execution-state file utilities ported from the Node.js
// state.js shared library: filename grammar, branch slug helpers, file lookup
// (delimiter-aware mtime-newest), init/write with prune-on-write, and session
// stamping. The "Run helpers" section adds plan-run selection by exact run ID
// (RunID, LoadRun, LatestPlanRun, ActivePlanRun), plan-run lookup by plan
// file (FindPlanRunByPlanFile) and per-run evidence
// directories (EvidenceDir, PruneEvidenceDirs).
//
// The canonical state directory lives at <root>/.sdlc-v2/runs/. Root is
// injected by callers so that no environment or git lookup is needed here.
// For one release cycle, Find also falls back to the legacy
// <root>/.sdlc-v2/execution/ location when a run isn't found under runs/.
//
// Filename format: <prefix>-<branchSlug>-<YYYYMMDDTHHmmssZ>.json
// Accepted prefixes: ship, execute, plan (parser also accepts commit).
package state

import (
	"errors"
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

// timestampSuffixRe matches what follows "<prefix>-<slug>-" in a canonical
// state file basename. Find uses it to match the slug exactly.
var timestampSuffixRe = regexp.MustCompile(`^\d{8}T\d{6}Z\.json$`)

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

	// Root is the project root directory (parent of .sdlc-v2/).
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
	return filepath.Join(root, paths.DataDir, paths.RunsSubdir)
}

// legacyStateDir returns the pre-migration execution state directory for a
// root. It is consulted by Find as a one-release-cycle fallback for state
// files that haven't been moved to stateDir yet (see the "layout" action on
// the migrate tool).
func legacyStateDir(root string) string {
	return filepath.Join(root, paths.DataDir, paths.LegacyExecutionSubdir)
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

// Init creates a new state file in <root>/.sdlc-v2/runs/ with the filename
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
// Matching is exact on the slug: the name must be <prefix>-<slug>- followed
// only by the canonical timestamp and ".json". A plain prefix match would
// also accept another branch whose slug starts with this one ("feat-x-2"
// for "feat-x") and hand back that branch's state.
//
// Find checks stateDir (runs/) first; if no match is found there, it falls
// back to legacyStateDir (execution/) so runs created before the runs/
// migration remain discoverable for one release cycle.
func Find(root, prefix, branch string) (*State, error) {
	st, err := findInDir(stateDir(root), root, prefix, branch)
	if err != nil {
		return nil, err
	}
	if st != nil {
		return st, nil
	}
	return findInDir(legacyStateDir(root), root, prefix, branch)
}

// findInDir performs the actual <prefix>-<branchSlug>-*.json lookup within a
// single directory, by mtime newest-first. It is shared by Find across
// stateDir and legacyStateDir.
func findInDir(dir, root, prefix, branch string) (*State, error) {
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
		if !strings.HasPrefix(name, pat) || !timestampSuffixRe.MatchString(name[len(pat):]) {
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
// *State. Falls back to the legacy state directory (execution/) when no
// match is found under the current layout (runs/), mirroring Find's own
// legacy-dir fallback added in the same diff.
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
	st, err := findAnyInDir(stateDir(root), root, prefix)
	if err != nil {
		return nil, err
	}
	if st != nil {
		return st, nil
	}
	return findAnyInDir(legacyStateDir(root), root, prefix)
}

// findAnyInDir performs the branch-agnostic <prefix>-*.json lookup within a
// single directory, by mtime newest-first. Shared by FindAny across stateDir
// and legacyStateDir.
func findAnyInDir(dir, root, prefix string) (*State, error) {
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
// List
// ---------------------------------------------------------------------------

// ListResult is the result of listing every state file under a repo's
// <root>/.sdlc-v2/runs/ directory.
type ListResult struct {
	// States holds every state file that matches filenameRe, newest first
	// by filename timestamp. Never nil, even when runs/ is absent or empty.
	States []*State

	// Skipped counts entries whose name matches filenameRe but whose
	// contents fail to read or parse as JSON.
	Skipped int
}

// List returns every parsable state file under <root>/.sdlc-v2/runs/,
// newest first by filename timestamp.
//
// An entry whose name does not match filenameRe — a temp file left behind
// by fsx.AtomicWriteBytes (<name>.tmp-<rand>), a .evidence directory, any
// other directory or stray file — is skipped silently and does not count
// toward Skipped. An entry whose name does match filenameRe but whose
// contents fail to read or parse as JSON is omitted from States and counted
// in Skipped instead.
//
// Returns an empty, non-nil States slice and no error when runs/ does not
// exist. Returns a non-nil error only for another ReadDir failure of runs/
// (e.g. it exists as a regular file).
func List(root string) (ListResult, error) {
	dir := stateDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return ListResult{States: []*State{}}, nil
		}
		return ListResult{}, fmt.Errorf("state: readdir %s: %w", dir, err)
	}

	type candidate struct {
		name   string
		parsed *parsedFilename
	}
	var candidates []candidate

	for _, e := range entries {
		if e.IsDir() {
			continue // .evidence dirs and any other directories
		}
		parsed := parseStateFilename(e.Name())
		if parsed == nil {
			continue // temp file or other non-matching entry: skip silently
		}
		candidates = append(candidates, candidate{name: e.Name(), parsed: parsed})
	}

	// Sort newest first by filename timestamp (not mtime).
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].parsed.Timestamp > candidates[j].parsed.Timestamp
	})

	result := ListResult{States: []*State{}}
	for _, c := range candidates {
		path := filepath.Join(dir, c.name)
		var data map[string]any
		if err := fsx.ReadJSON(path, &data); err != nil {
			result.Skipped++
			continue
		}
		result.States = append(result.States, &State{
			Path:       path,
			Root:       root,
			Prefix:     c.parsed.Prefix,
			BranchSlug: c.parsed.Slug,
			Data:       data,
		})
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// Write (prune-on-write)
// ---------------------------------------------------------------------------

// Write writes st.Data to st.Path atomically, after pruning all pre-existing
// state files for the same prefix+branchSlug (except st.Path itself).
//
// Pruning uses parseStateFilename for exact slug equality, the same rule
// Find uses. One exception: a sibling "plan" run whose planIntegrity.done
// marker is set (isDonePlanRun) is kept rather than pruned, so a finished
// plan run survives a later /sdlc:plan on the same branch long enough for
// ship's report to read it. It is removed later by ship's cleanup-pipeline
// step or by GC's TTL sweep — Write and PruneEvidenceDirs no longer own its
// deletion. exec-* and ship-* siblings are unaffected.
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
		if parsed.Prefix == "plan" && isDonePlanRun(fp) {
			continue // kept for the ship report; cleanup-pipeline or GC removes it
		}
		_ = os.Remove(fp) // best-effort
	}

	return fsx.AtomicWriteJSON(st.Path, st.Data)
}

// isDonePlanRun reports whether the state file at path is a "plan" run whose
// data.planIntegrity.done marker is set. A missing file, an unreadable file,
// or corrupt JSON all return false, preserving today's prune behavior for
// anything that isn't verifiably a done plan run.
func isDonePlanRun(path string) bool {
	var data map[string]any
	if err := fsx.ReadJSON(path, &data); err != nil {
		return false
	}
	pi, _ := data["planIntegrity"].(map[string]any)
	_, hasDone := pi["done"]
	return hasDone
}

// ---------------------------------------------------------------------------
// Run helpers: run ID, evidence directory, run lookup by ID/branch, prune
// ---------------------------------------------------------------------------

// evidenceDirSuffix marks a plan run's evidence directory.
const evidenceDirSuffix = ".evidence"

// runSlugRe is the allowed form of the slug inside a runID (SlugifyBranch
// output): alphanumerics and hyphens only. LoadRun rejects any runID whose
// slug fails this check, closing off "." (e.g. "..") and other characters
// SlugifyBranch never itself produces.
var runSlugRe = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// ErrInvalidRunID is wrapped by LoadRun's error when runID fails validation
// (as opposed to a read or decode failure of a valid run's file).
var ErrInvalidRunID = errors.New("state: invalid run ID")

// removeAll is os.RemoveAll; tests replace it to simulate a failed remove.
var removeAll = os.RemoveAll

// RunID returns the run identifier of st: the state file basename without
// the ".json" extension (e.g. "plan-main-20260929T114125Z").
func RunID(st *State) string {
	return strings.TrimSuffix(filepath.Base(st.Path), ".json")
}

// EvidenceDir returns the per-run evidence directory for runID:
// <root>/.sdlc-v2/runs/<runID>.evidence.
func EvidenceDir(root, runID string) string {
	return filepath.Join(stateDir(root), runID+evidenceDirSuffix)
}

// LoadRun loads <root>/.sdlc-v2/runs/<runID>.json by its exact run ID.
//
// Before any path is joined or any file is read, LoadRun validates runID in
// three steps: filepath.Base(runID) must equal runID (rejecting any "/" or
// ".." path-traversal attempt), parseStateFilename(runID+".json") must match
// the state filename grammar with Prefix == "plan", and the parsed Slug must
// match runSlugRe. Any failing check returns a non-nil error without
// touching the filesystem.
//
// LoadRun returns (nil, nil) when the (validated) file does not exist, and a
// non-nil error for a rejected runID (wrapping ErrInvalidRunID), an
// unreadable file, or corrupt JSON.
func LoadRun(root, runID string) (*State, error) {
	if filepath.Base(runID) != runID {
		return nil, fmt.Errorf("state: invalid run ID %q: must be a bare filename component: %w", runID, ErrInvalidRunID)
	}

	name := runID + ".json"
	parsed := parseStateFilename(name)
	if parsed == nil {
		return nil, fmt.Errorf("state: invalid run ID %q: does not match the state filename grammar: %w", runID, ErrInvalidRunID)
	}
	if parsed.Prefix != "plan" {
		return nil, fmt.Errorf("state: invalid run ID %q: prefix %q, want %q: %w", runID, parsed.Prefix, "plan", ErrInvalidRunID)
	}
	if !runSlugRe.MatchString(parsed.Slug) {
		return nil, fmt.Errorf("state: invalid run ID %q: slug %q contains disallowed characters: %w", runID, parsed.Slug, ErrInvalidRunID)
	}

	path := filepath.Join(stateDir(root), name)
	var data map[string]any
	if err := fsx.ReadJSON(path, &data); err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("state: read %s: %w", path, err)
	}

	return &State{
		Path:       path,
		Root:       root,
		Prefix:     parsed.Prefix,
		BranchSlug: parsed.Slug,
		Data:       data,
	}, nil
}

// LatestPlanRun scans <root>/.sdlc-v2/runs/ for plan files whose parsed Slug
// equals SlugifyBranch(branch) exactly (not a prefix match — see Find, which
// would also match a superstring slug like "feat-x" when querying "feat" and
// so could return the wrong plan run), and loads the newest one by parsed
// timestamp. It applies no marker filter — see ActivePlanRun for that.
//
// The winning file is loaded through LoadRun, so there is a single decode
// path and a single runID validation for every plan run this package reads.
//
// Returns (nil, nil) when runs/ does not exist or no plan file has an exact
// slug match. Returns a non-nil error for another ReadDir failure (e.g.
// runs/ exists as a regular file) or when the winning file fails to load
// (e.g. corrupt JSON).
func LatestPlanRun(root, branch string) (*State, error) {
	dir := stateDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("state: readdir %s: %w", dir, err)
	}

	slug := SlugifyBranch(branch)

	var bestName, bestTimestamp string
	for _, e := range entries {
		parsed := parseStateFilename(e.Name())
		if parsed == nil || parsed.Prefix != "plan" || parsed.Slug != slug {
			continue
		}
		if bestName == "" || parsed.Timestamp > bestTimestamp {
			bestName = e.Name()
			bestTimestamp = parsed.Timestamp
		}
	}
	if bestName == "" {
		return nil, nil
	}

	return LoadRun(root, strings.TrimSuffix(bestName, ".json"))
}

// ActivePlanRun returns LatestPlanRun's run for branch only when it is
// mid-flight: planIntegrity.skillInvoked is set and planIntegrity.done is
// absent (the plan SKILL.md stamps "done" right before ExitPlanMode; see
// plan.go's validMarkers and stop_hooks.go's planIntegrityFromState for the
// same convention). Otherwise it returns (nil, nil) — including when
// LatestPlanRun itself returns (nil, nil). A LatestPlanRun error passes
// through unchanged.
func ActivePlanRun(root, branch string) (*State, error) {
	st, err := LatestPlanRun(root, branch)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, nil
	}

	integrity, _ := st.Data["planIntegrity"].(map[string]any)
	if _, hasSkillInvoked := integrity["skillInvoked"]; !hasSkillInvoked {
		return nil, nil
	}
	if _, hasDone := integrity["done"]; hasDone {
		return nil, nil
	}

	return st, nil
}

// FindPlanRunByPlanFile returns the newest plan run in <root>/.sdlc-v2/runs/
// whose data.planFilePath, cleaned, equals planPath, cleaned. It looks up by
// plan file and not by branch, because the plan run linked to an execute run
// is not always the branch's newest plan run. A relative planFilePath is
// joined to root before the compare (plan_mark normally stores it absolute).
//
// Every plan file is loaded through LoadRun, newest timestamp first. A file
// that fails to load is skipped: it cannot be confirmed as the match, and one
// corrupt unrelated run must not hide the right one.
//
// Returns (nil, nil) when planPath is empty, runs/ does not exist, or no plan
// run matches. Returns a non-nil error only for another ReadDir failure.
func FindPlanRunByPlanFile(root, planPath string) (*State, error) {
	if strings.TrimSpace(planPath) == "" {
		return nil, nil
	}
	want := filepath.Clean(planPath)

	dir := stateDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("state: readdir %s: %w", dir, err)
	}

	type candidate struct{ runID, timestamp string }
	var candidates []candidate
	for _, e := range entries {
		parsed := parseStateFilename(e.Name())
		if parsed == nil || parsed.Prefix != "plan" {
			continue
		}
		candidates = append(candidates, candidate{strings.TrimSuffix(e.Name(), ".json"), parsed.Timestamp})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].timestamp > candidates[j].timestamp
	})

	for _, c := range candidates {
		st, err := LoadRun(root, c.runID)
		if err != nil || st == nil {
			continue
		}
		got, _ := st.Data["planFilePath"].(string)
		if strings.TrimSpace(got) == "" {
			continue
		}
		if !filepath.IsAbs(got) {
			got = filepath.Join(root, got)
		}
		if filepath.Clean(got) == want {
			return st, nil
		}
	}
	return nil, nil
}

// PruneEvidenceDirs removes sibling <prefix>-<slug>-<ts>.evidence directories
// that share st's exact Prefix and Slug, keeping st's own evidence
// directory. It mirrors Write's prune-on-write loop but over directories
// instead of files, so it never touches the sibling .json state files that
// Write's own prune already owns.
//
// Like Write's own prune, a sibling "plan" run's evidence directory is kept
// rather than removed when its state file is a done run (isDonePlanRun) —
// see Write's doc comment for why.
//
// Best-effort, like the Write prune: a ReadDir failure (including runs/ not
// existing) or a removeAll failure for one directory is ignored, and
// PruneEvidenceDirs still attempts every other matching directory. It has no
// error return.
func PruneEvidenceDirs(st *State) {
	dir := stateDir(st.Root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	ownRunID := RunID(st)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, evidenceDirSuffix) {
			continue
		}
		runID := strings.TrimSuffix(name, evidenceDirSuffix)
		if runID == ownRunID {
			continue
		}
		parsed := parseStateFilename(runID + ".json")
		if parsed == nil || parsed.Prefix != st.Prefix || parsed.Slug != st.BranchSlug {
			continue
		}
		if parsed.Prefix == "plan" && isDonePlanRun(filepath.Join(dir, runID+".json")) {
			continue // evidence of a done plan run stays with its state file
		}
		_ = removeAll(filepath.Join(dir, name)) // best-effort
	}
}
