// Package gitx provides git command helpers for the sdlc plugin.
//
// Every git invocation goes through internal/execx.Run — the single
// process-execution chokepoint for the plugin — so output capping and
// error handling are centralized there.
//
// All exported functions that touch a git repo degrade to a non-nil error
// (never panic) when called outside a git repository.
//
// Ports the git command inventory from scripts/lib/git.js:80-200 plus
// the tag/log helpers used by downstream tools (Tasks 24-36).
package gitx

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
)

// CurrentBranch returns the name of the currently checked-out branch.
// On detached HEAD it returns "HEAD". Outside a git repo it returns an error.
func CurrentBranch(dir string) (string, error) {
	out, err := execx.Run("git", []string{"branch", "--show-current"}, execx.Options{Dir: dir})
	if err != nil {
		return "", fmt.Errorf("gitx: current branch: %w", err)
	}
	if out == "" {
		return "HEAD", nil // detached HEAD: git exits 0 with empty output
	}
	return out, nil
}

// DefaultBranch auto-detects the repository's default base branch.
// It tries: origin/HEAD symbolic ref, then "main", then "master".
// Returns an error if no default branch can be detected.
func DefaultBranch(dir string) (string, error) {
	// Try origin/HEAD symbolic ref first.
	out, err := execx.Run("git", []string{"symbolic-ref", "refs/remotes/origin/HEAD"}, execx.Options{Dir: dir})
	if err == nil && out != "" {
		branch := strings.TrimPrefix(out, "refs/remotes/origin/")
		if branch != "" {
			return branch, nil
		}
	}

	// Fall back to verifying "main" then "master" locally.
	for _, candidate := range []string{"main", "master"} {
		_, err := execx.Run("git", []string{"rev-parse", "--verify", candidate}, execx.Options{Dir: dir})
		if err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("gitx: cannot auto-detect default branch")
}

// Status returns the porcelain status output of the working tree.
// An empty string with a nil error means a clean working tree.
func Status(dir string) (string, error) {
	out, err := execx.Run("git", []string{"status", "--porcelain"}, execx.Options{Dir: dir})
	if err != nil {
		return "", fmt.Errorf("gitx: status: %w", err)
	}
	return out, nil
}

// validateRef rejects refs that start with "-" to prevent argument injection.
// A ref like "-Rmalicious...HEAD" would be parsed as a git flag if passed as
// an argv token without validation.
func validateRef(ref, context string) error {
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("gitx: %s: ref %q looks like a flag (starts with '-')", context, ref)
	}
	return nil
}

// DiffOpts configures the Diff function.
type DiffOpts struct {
	// Base is the base ref for a three-dot range (base...HEAD).
	// When set, the diff shows what the current branch contributed
	// relative to the merge-base — the correct semantics for
	// branch-contribution diffs (git.js issue #239).
	Base string

	// Cached shows staged changes only (--cached).
	Cached bool

	// NameOnly restricts output to file names (--name-only).
	NameOnly bool

	// Stat shows diffstat output (--stat).
	Stat bool
}

// Diff returns the git diff output for the given options.
//
// When Base is set, a three-dot range (base...HEAD) is used.
// When Cached is true, staged changes are shown.
// When neither Base nor Cached is set, working-tree changes vs HEAD are shown.
func Diff(dir string, opts DiffOpts) (string, error) {
	args := []string{"diff"}

	if opts.NameOnly {
		args = append(args, "--name-only")
	}
	if opts.Stat {
		args = append(args, "--stat")
	}

	if opts.Cached {
		args = append(args, "--cached")
	} else if opts.Base != "" {
		if err := validateRef(opts.Base, "diff"); err != nil {
			return "", err
		}
		args = append(args, opts.Base+"...HEAD")
	} else {
		args = append(args, "HEAD")
	}

	out, err := execx.Run("git", args, execx.Options{Dir: dir})
	if err != nil {
		return "", fmt.Errorf("gitx: diff: %w", err)
	}
	return out, nil
}

// DeriveWorkspace determines whether the caller should create a new feature
// branch or continue on the current one. It is a pure function with no I/O.
//
// Returns "branch" when the current directory is the main worktree AND HEAD
// is the default branch (the caller should auto-create a feature branch).
// Returns "continue" in all other cases: a linked (non-main) worktree, or
// the main worktree already on a feature branch.
//
// Mirrors git.js:176 deriveWorkspace({inLinkedWorktree, currentBranch, defaultBranch}).
func DeriveWorkspace(inLinkedWorktree bool, currentBranch, defaultBranch string) string {
	if inLinkedWorktree {
		return "continue"
	}
	if currentBranch == defaultBranch {
		return "branch"
	}
	return "continue"
}

// CommitLog returns the one-line commit log between base and HEAD.
func CommitLog(dir, base string) (string, error) {
	if err := validateRef(base, "commit log"); err != nil {
		return "", err
	}
	out, err := execx.Run("git", []string{"log", "--oneline", base + "..HEAD"}, execx.Options{Dir: dir})
	if err != nil {
		return "", fmt.Errorf("gitx: commit log: %w", err)
	}
	return out, nil
}

// CommitCount returns the number of commits between base and HEAD.
func CommitCount(dir, base string) (int, error) {
	if err := validateRef(base, "commit count"); err != nil {
		return 0, err
	}
	out, err := execx.Run("git", []string{"rev-list", "--count", base + "..HEAD"}, execx.Options{Dir: dir})
	if err != nil {
		return 0, fmt.Errorf("gitx: commit count: %w", err)
	}
	var count int
	if _, err := fmt.Sscanf(out, "%d", &count); err != nil {
		return 0, fmt.Errorf("gitx: commit count: could not parse %q: %w", out, err)
	}
	return count, nil
}

// semverTagRe matches strict semver tags (no pre-release suffix).
var semverTagRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

// semverTagPreRe matches semver tags including optional pre-release suffix.
var semverTagPreRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)

// TagList returns all strict semver tags (no pre-release) sorted by
// descending version (latest first).
func TagList(dir string) ([]string, error) {
	out, err := execx.Run("git", []string{"tag", "--list", "--sort=-v:refname"}, execx.Options{Dir: dir})
	if err != nil {
		return nil, fmt.Errorf("gitx: tag list: %w", err)
	}
	return filterTags(out, semverTagRe), nil
}

// TagsAtHead returns strict semver tags (no pre-release) pointing at HEAD,
// sorted by descending version (latest first).
func TagsAtHead(dir string) ([]string, error) {
	out, err := execx.Run("git", []string{"tag", "--points-at", "HEAD", "--sort=-v:refname"}, execx.Options{Dir: dir})
	if err != nil {
		return nil, fmt.Errorf("gitx: tags at head: %w", err)
	}
	return filterTags(out, semverTagRe), nil
}

// AllSemverTags returns all semver tags including pre-release (e.g.
// v1.3.3-rc.1), sorted by descending version (latest first).
func AllSemverTags(dir string) ([]string, error) {
	out, err := execx.Run("git", []string{"tag", "--list", "--sort=-v:refname"}, execx.Options{Dir: dir})
	if err != nil {
		return nil, fmt.Errorf("gitx: all semver tags: %w", err)
	}
	return filterTags(out, semverTagPreRe), nil
}

// filterTags splits newline-delimited output and keeps lines matching re.
func filterTags(out string, re *regexp.Regexp) []string {
	if out == "" {
		return nil
	}
	var tags []string
	for _, tag := range strings.Split(out, "\n") {
		tag = strings.TrimSpace(tag)
		if re.MatchString(tag) {
			tags = append(tags, tag)
		}
	}
	return tags
}
