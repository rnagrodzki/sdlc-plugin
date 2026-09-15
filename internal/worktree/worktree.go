// Package worktree resolves the main and active git worktree roots.
//
// It preserves the two-worktree model used by the JS implementation
// (scripts/lib/worktree.js): config/state anchor to the MAIN worktree, while
// content scans (e.g. openspec/changes, openspec/specs) use the ACTIVE
// worktree so they see files on the checked-out branch even inside a linked
// worktree.
//
// Layering rule: this package must never import internal/config or
// internal/state — both of those packages anchor themselves using this
// package, and an import in the other direction would create a cycle
// (enforced by TestNoForbiddenImports in worktree_test.go).
//
// Unlike the JS "Safe" helpers, MainRoot and ActiveRoot do not silently fall
// back to the current directory on failure — they return ("", err) so
// callers decide how to handle a non-repo working directory.
package worktree

import (
	"errors"
	"fmt"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
)

// MainRoot returns the absolute path of the main (primary) git worktree by
// parsing `git worktree list --porcelain`. The first "worktree <path>" entry
// in that output is always the main worktree.
//
// Returns ("", error) when the current directory is not inside a git repo or
// the output cannot be parsed.
func MainRoot() (string, error) {
	return mainRootIn("")
}

// mainRootIn is MainRoot with an explicit working directory, so tests can
// point it at a fixture repo without changing the process's cwd. An empty
// dir means "use the process's current working directory".
func mainRootIn(dir string) (string, error) {
	out, err := execx.Run("git", []string{"worktree", "list", "--porcelain"}, execx.Options{Dir: dir})
	if err != nil {
		return "", fmt.Errorf("worktree: could not determine main worktree: %w", err)
	}

	out = strings.TrimSpace(out)
	if out == "" {
		return "", errors.New("worktree: git worktree list returned empty output")
	}

	lines := strings.Split(out, "\n")
	for i, line := range lines {
		rest, ok := strings.CutPrefix(line, "worktree ")
		if !ok {
			continue
		}
		path := strings.TrimSpace(rest)
		if path == "" {
			return "", errors.New("worktree: could not parse main worktree path from git worktree list output")
		}
		// A bare worktree entry's "worktree <path>" line is immediately
		// followed by a literal "bare" line instead of the HEAD/branch lines
		// a normal (linked or main) entry carries. `git worktree list`
		// always lists the bare repository first when one is registered
		// (e.g. a `git clone --bare` used as the anchor for linked
		// worktrees), so naively returning the first "worktree " line here
		// anchors .sdlc-v2/ to the bare root instead of the actual
		// main/linked worktree. Skip past it and keep scanning for the
		// first non-bare entry.
		if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "bare" {
			continue
		}
		return path, nil
	}

	return "", errors.New("worktree: could not parse main worktree path from git worktree list output")
}

// IsBare reports whether the git worktree at dir is a bare repository (no
// working tree checked out), via `git rev-parse --is-bare-repository`. An
// empty dir means "use the process's current working directory".
//
// This is a defensive diagnostic, not part of MainRoot's own resolution:
// mainRootIn already skips bare entries when scanning `git worktree list`
// output, so MainRoot should never itself resolve to a bare path. Callers
// (e.g. the validate tool's worktree_anchoring check) use IsBare to surface
// a regression in that invariant rather than silently anchoring .sdlc-v2/ to
// a worktree with no working files.
func IsBare(dir string) (bool, error) {
	out, err := execx.Run("git", []string{"rev-parse", "--is-bare-repository"}, execx.Options{Dir: dir})
	if err != nil {
		return false, fmt.Errorf("worktree: could not determine bare status: %w", err)
	}
	return strings.TrimSpace(out) == "true", nil
}

// ActiveRoot returns the absolute path of the ACTIVE worktree's top level —
// the root of the working tree for the current checkout, even when invoked
// from a subdirectory or a linked worktree.
//
// Unlike MainRoot (which always walks back to the primary worktree, used for
// .sdlc-v2/ config/state anchoring), ActiveRoot resolves the tree on the active
// branch — correct for content scans that live on the active branch in a
// linked worktree.
//
// When not in a linked worktree, this returns the same path as MainRoot, so
// callers see contentRoot == projectRoot in the single-worktree case.
//
// Returns ("", error) when the current directory is not inside a git repo.
func ActiveRoot() (string, error) {
	return activeRootIn("")
}

// activeRootIn is ActiveRoot with an explicit working directory, so tests can
// point it at a fixture repo without changing the process's cwd. An empty
// dir means "use the process's current working directory".
func activeRootIn(dir string) (string, error) {
	out, err := execx.Run("git", []string{"rev-parse", "--show-toplevel"}, execx.Options{Dir: dir})
	if err != nil {
		return "", fmt.Errorf("worktree: could not determine active worktree: %w", err)
	}

	out = strings.TrimSpace(out)
	if out == "" {
		return "", errors.New("worktree: git rev-parse --show-toplevel returned empty output")
	}

	return out, nil
}
