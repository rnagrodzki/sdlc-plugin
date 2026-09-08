package state

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GCOptions configures GC behavior.
type GCOptions struct {
	// TTL is the maximum age of a state file before it is eligible for
	// deletion. Taken literally, including zero: TTL==0 means no file is
	// "TTL-fresh" (matching scripts/lib/state.js's gcStateFiles/gcTempdirs,
	// where an explicit ttlDays of 0 makes ageMs<ttlMs always false). GC does
	// NOT substitute a default when TTL is the Go zero value — ship.js
	// resolves its own default (CLI > config > 7 days) before ever calling
	// into gcStateFiles/gcTempdirs, and callers here must do the same (see
	// ship.go's resolveGCTTLDays) rather than relying on this type to guess
	// "unset" vs. "explicitly zero", which Go's zero-value semantics cannot
	// distinguish.
	TTL time.Duration

	// BranchExists is called to check if a branch still exists.
	// If nil, all branches are treated as existing (no branch-pruning).
	BranchExists func(branch string) bool

	// TempDir overrides the directory scanned for sdlc-explore-* tempdirs
	// (see gcTempdirs). Empty means os.TempDir(). Exists so callers/tests can
	// point the sweep at an isolated directory instead of the real system
	// tempdir, mirroring scripts/lib/state.js's SDLC_EXPLORE_TMPDIR_OVERRIDE.
	TempDir string
}

// GCReport summarizes what GC did.
type GCReport struct {
	Deleted []string       `json:"deleted"` // paths of deleted state files
	Kept    []string       `json:"kept"`    // paths of kept state files
	Buckets map[string]int `json:"buckets"` // count of kept files per prefix

	// TempdirsDeleted/TempdirsKept are the sdlc-explore-* tempdirs removed or
	// retained by the same TTL/branch-liveness rule as the state files above.
	TempdirsDeleted []string `json:"tempdirsDeleted"`
	TempdirsKept    []string `json:"tempdirsKept"`
}

// GC garbage-collects state files and temp directories.
//
// Deletes: TTL-expired files (opt.TTL, taken literally — see its doc comment
// for the zero-value contract), files for deleted branches. Never deletes
// the newest file for a still-live branch.
func GC(root string, opt GCOptions) (*GCReport, error) {
	dir := stateDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("state: gc readdir %s: %w", dir, err)
		}
		// No state dir yet: nothing to group below, but the tempdir sweep
		// further down is independent of it and must still run.
		entries = nil
	}

	type fileEntry struct {
		path   string
		mtime  time.Time
		parsed *parsedFilename
	}

	// Group state files by prefix+slug.
	groups := map[string][]fileEntry{}

	for _, e := range entries {
		name := e.Name()
		// Skip sidecar files (dot-prefixed) and directories.
		if strings.HasPrefix(name, ".") || e.IsDir() {
			continue
		}
		if !strings.HasSuffix(name, ".json") {
			continue
		}

		parsed := parseStateFilename(name)
		if parsed == nil {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}

		fp := filepath.Join(dir, name)
		key := parsed.Prefix + "\x00" + parsed.Slug
		groups[key] = append(groups[key], fileEntry{
			path:   fp,
			mtime:  info.ModTime(),
			parsed: parsed,
		})
	}

	report := &GCReport{
		Deleted: []string{},
		Kept:    []string{},
		Buckets: map[string]int{},
	}

	cutoff := time.Now().Add(-opt.TTL)

	for _, files := range groups {
		// Sort by mtime descending (newest first).
		sort.Slice(files, func(i, j int) bool {
			return files[i].mtime.After(files[j].mtime)
		})

		slug := files[0].parsed.Slug
		prefix := files[0].parsed.Prefix

		// If BranchExists is provided and returns false, delete ALL files.
		branchDeleted := opt.BranchExists != nil && !opt.BranchExists(slug)

		for i, f := range files {
			switch {
			case branchDeleted:
				if err := os.Remove(f.path); err == nil {
					report.Deleted = append(report.Deleted, f.path)
				}
			case i == 0:
				// Always keep the newest file for a live branch.
				report.Kept = append(report.Kept, f.path)
				report.Buckets[prefix]++
			case f.mtime.Before(cutoff):
				if err := os.Remove(f.path); err == nil {
					report.Deleted = append(report.Deleted, f.path)
				}
			default:
				report.Kept = append(report.Kept, f.path)
				report.Buckets[prefix]++
			}
		}
	}

	// Clean up sdlc-explore-* temp directories, using the same TTL/
	// branch-liveness rule as the state files above (opt.TTL was already
	// defaulted at the top of this function).
	tempDir := opt.TempDir
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	report.TempdirsDeleted, report.TempdirsKept = gcTempdirs(tempDir, opt.TTL, opt.BranchExists)

	return report, nil
}

// exploreTempdirPrefix is the mkdtempSync-style prefix plan-explore.js uses
// for its per-invocation tempdirs: sdlc-explore-<branchSlug>-<random suffix>.
const exploreTempdirPrefix = "sdlc-explore-"

// gcTempdirs sweeps sdlc-explore-* directories from dir, applying the same
// rule as the state-file GC above (mirroring scripts/lib/state.js's
// gcTempdirs): a directory whose mtime is within ttl is kept ("ttl-fresh");
// otherwise it is kept if its embedded branch slug is still live per
// branchExists ("branch-exists" — a nil branchExists treats every branch as
// live, matching GCOptions.BranchExists' documented default), else removed
// ("stale+branch-gone"). A name whose branch slug can't be parsed out is
// kept unconditionally ("unparseable-name").
//
// branchSlug is derived by stripping the prefix and dropping the trailing
// mkdtempSync random-suffix segment (the last '-'-delimited part).
func gcTempdirs(dir string, ttl time.Duration, branchExists func(string) bool) (deleted, kept []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}

	cutoff := time.Now().Add(-ttl)

	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, exploreTempdirPrefix) {
			continue
		}
		full := filepath.Join(dir, name)

		afterPrefix := strings.TrimPrefix(name, exploreTempdirPrefix)
		parts := strings.Split(afterPrefix, "-")
		if len(parts) < 2 {
			kept = append(kept, full)
			continue
		}
		slug := strings.Join(parts[:len(parts)-1], "-")

		info, err := e.Info()
		if err != nil {
			continue
		}

		if info.ModTime().After(cutoff) {
			kept = append(kept, full)
			continue
		}
		if branchExists == nil || branchExists(slug) {
			kept = append(kept, full)
			continue
		}

		if err := os.RemoveAll(full); err == nil {
			deleted = append(deleted, full)
		}
	}

	return deleted, kept
}

// MigrateBranchSlug renames all state files from oldSlug to newSlug.
func MigrateBranchSlug(root, oldSlug, newSlug string) error {
	dir := stateDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("state: migrate readdir %s: %w", dir, err)
	}

	for _, e := range entries {
		name := e.Name()
		parsed := parseStateFilename(name)
		if parsed == nil {
			continue
		}
		if parsed.Slug != oldSlug {
			continue
		}

		newName := fmt.Sprintf("%s-%s-%s.json", parsed.Prefix, newSlug, parsed.Timestamp)
		oldPath := filepath.Join(dir, name)
		newPath := filepath.Join(dir, newName)

		if err := os.Rename(oldPath, newPath); err != nil {
			return fmt.Errorf("state: migrate rename %s → %s: %w", oldPath, newPath, err)
		}
	}

	return nil
}
