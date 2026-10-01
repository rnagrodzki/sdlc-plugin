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
	"errors"
	"fmt"
	"os/exec"
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

// BaseBranch returns configured, trimmed of surrounding whitespace, when it
// is non-empty. Otherwise it falls back to DefaultBranch(dir) to
// auto-detect the repository's base branch. It performs no I/O itself in
// the configured case — the trimmed value is returned as-is, without
// verifying that it names a real branch.
func BaseBranch(dir, configured string) (string, error) {
	trimmed := strings.TrimSpace(configured)
	if trimmed != "" {
		return trimmed, nil
	}
	return DefaultBranch(dir)
}

// Status returns the porcelain status output of the working tree.
// An empty string with a nil error means a clean working tree. Only
// trailing whitespace is trimmed: the first entry keeps its leading status
// column (e.g. " M a.go"), so every line has the path at column 3.
func Status(dir string) (string, error) {
	out, err := execx.Run("git", []string{"status", "--porcelain"}, execx.Options{Dir: dir, KeepLeadingSpace: true})
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
	// branch-contribution diffs.
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
// When neither Base nor Cached is set, unstaged changes are shown: the
// working tree compared to the index (plain `git diff`), so staged-only
// changes are excluded.
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

// BehindCount returns the number of commits HEAD is behind ref — the
// reverse of CommitCount's base..HEAD direction (HEAD..ref here).
func BehindCount(dir, ref string) (int, error) {
	if err := validateRef(ref, "behind count"); err != nil {
		return 0, err
	}
	out, err := execx.Run("git", []string{"rev-list", "--count", "HEAD.." + ref}, execx.Options{Dir: dir})
	if err != nil {
		return 0, fmt.Errorf("gitx: behind count: %w", err)
	}
	var count int
	if _, err := fmt.Sscanf(out, "%d", &count); err != nil {
		return 0, fmt.Errorf("gitx: behind count: could not parse %q: %w", out, err)
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

// isRepo reports whether dir is inside a git repository. It underlies
// TagExists's non-repo detection: "git rev-parse --verify refs/tags/<name>"
// exits with the same status (128) both when the ref is simply missing and
// when dir is not a repository at all, and execx.Run does not surface
// stderr text — so the two cases cannot be told apart from that command's
// result alone. A cheap "git rev-parse --git-dir" first resolves the
// ambiguity.
func isRepo(dir string) bool {
	_, err := execx.Run("git", []string{"rev-parse", "--git-dir"}, execx.Options{Dir: dir})
	return err == nil
}

// TagExists reports whether an exact-match tag named name exists in dir,
// via "git rev-parse --verify refs/tags/<name>". Returns (false, nil) when
// the tag simply does not exist. Returns a non-nil error for an empty name,
// a name that looks like a flag, or when dir is not a git repository.
func TagExists(dir, name string) (bool, error) {
	if name == "" {
		return false, fmt.Errorf("gitx: tag exists: tag name is empty")
	}
	if err := validateRef(name, "tag exists"); err != nil {
		return false, err
	}
	if !isRepo(dir) {
		return false, fmt.Errorf("gitx: tag exists: %q is not a git repository", dir)
	}
	_, err := execx.Run("git", []string{"rev-parse", "--verify", "refs/tags/" + name}, execx.Options{Dir: dir})
	return err == nil, nil
}

// CreateTag creates an annotated tag (git tag -a) named name at HEAD with
// message as its annotation. Returns an actionable error when name is
// empty, name already exists (collision — checked before attempting
// creation so the error names the conflicting tag rather than surfacing
// git's own exit status), or dir is not a git repository.
func CreateTag(dir, name, message string) error {
	if name == "" {
		return fmt.Errorf("gitx: create tag: tag name is empty")
	}
	if err := validateRef(name, "create tag"); err != nil {
		return err
	}

	exists, err := TagExists(dir, name)
	if err != nil {
		return fmt.Errorf("gitx: create tag: %w", err)
	}
	if exists {
		return fmt.Errorf("gitx: create tag: tag %q already exists", name)
	}

	if _, err := execx.Run("git", []string{"tag", "-a", name, "-m", message}, execx.Options{Dir: dir}); err != nil {
		return fmt.Errorf("gitx: create tag %q: %w", name, err)
	}
	return nil
}

// FetchTags fetches all tags from the configured remote(s), overwriting any
// local tag ref that has moved (git fetch --tags --force). Call this before
// enumerating tags (TagList, TagsAtHead, AllSemverTags, TagExists) whenever
// staleness in a multi-developer team would matter — none of those
// functions fetch on their own.
func FetchTags(dir string) error {
	if _, err := execx.Run("git", []string{"fetch", "--tags", "--force"}, execx.Options{Dir: dir}); err != nil {
		return fmt.Errorf("gitx: fetch tags: %w", err)
	}
	return nil
}

// PushSetUpstream pushes HEAD to remote and sets it as the current branch's
// upstream (git push -u <remote> HEAD). Returns an actionable error when
// remote is empty, looks like a flag, or the push itself fails (auth,
// network, rejected non-fast-forward, etc).
func PushSetUpstream(dir, remote string) error {
	if remote == "" {
		return fmt.Errorf("gitx: push set upstream: remote is empty")
	}
	if err := validateRef(remote, "push set upstream"); err != nil {
		return err
	}
	if _, err := execx.Run("git", []string{"push", "-u", remote, "HEAD"}, execx.Options{Dir: dir}); err != nil {
		return fmt.Errorf("gitx: push set upstream: %w", err)
	}
	return nil
}

// HasUpstream reports whether the current branch has an upstream configured,
// via "git rev-parse --abbrev-ref @{upstream}". Returns (false, nil) when no
// upstream is configured — that is the expected, non-error outcome for a
// freshly created local branch, signaled by git exiting 128. Returns
// (false, err) for any other failure, including when dir is not a git
// repository at all: "git rev-parse --abbrev-ref @{upstream}" also exits 128
// in that case, so isRepo is checked first to tell the two apart, mirroring
// TagExists.
func HasUpstream(dir string) (bool, error) {
	if !isRepo(dir) {
		return false, fmt.Errorf("gitx: has upstream: %q is not a git repository", dir)
	}
	_, err := execx.Run("git", []string{"rev-parse", "--abbrev-ref", "@{upstream}"}, execx.Options{Dir: dir})
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 128 {
		return false, nil
	}
	return false, fmt.Errorf("gitx: has upstream: %w", err)
}

// CommitsAhead returns the number of commits HEAD is ahead of its upstream
// (git rev-list @{upstream}..HEAD --count). Callers should confirm an
// upstream exists first (HasUpstream) — with none configured, the
// underlying git command fails and that failure is returned as-is here.
func CommitsAhead(dir string) (int, error) {
	out, err := execx.Run("git", []string{"rev-list", "--count", "@{upstream}..HEAD"}, execx.Options{Dir: dir})
	if err != nil {
		return 0, fmt.Errorf("gitx: commits ahead: %w", err)
	}
	var count int
	if _, err := fmt.Sscanf(out, "%d", &count); err != nil {
		return 0, fmt.Errorf("gitx: commits ahead: could not parse %q: %w", out, err)
	}
	return count, nil
}

// FetchBranch fetches branch from remote (git fetch <remote> <branch>).
// Returns an error naming the branch when it does not exist on remote.
func FetchBranch(dir, remote, branch string) error {
	if err := validateRef(remote, "fetch branch"); err != nil {
		return err
	}
	if err := validateRef(branch, "fetch branch"); err != nil {
		return err
	}
	if _, err := execx.Run("git", []string{"fetch", remote, branch}, execx.Options{Dir: dir}); err != nil {
		return fmt.Errorf("gitx: fetch branch %q from %q: %w", branch, remote, err)
	}
	return nil
}

// Merge merges ref into the current branch (git merge --no-edit <ref>).
//
// It uses execx.RunAllowExit rather than execx.Run because a merge conflict
// is expected, meaningful data (exit 1), not a process failure: discarding
// it the way Run does for any non-zero exit would make conflict detection
// indistinguishable from an unrelated git error. Exit 0 means the merge
// completed; exit 1 means it stopped on conflicts and left the merge in
// progress (conflict=true, err=nil) — the caller is expected to inspect
// UnmergedFiles and either resolve and commit, or call MergeAbort. Any other
// exit code is a genuine failure (e.g. an unknown ref) and is returned as an
// error instead.
func Merge(dir, ref string) (conflict bool, err error) {
	if err := validateRef(ref, "merge"); err != nil {
		return false, err
	}
	_, stderrText, exitCode, runErr := execx.RunAllowExit("git", []string{"merge", "--no-edit", ref}, execx.Options{Dir: dir})
	if runErr != nil {
		return false, fmt.Errorf("gitx: merge %q: %w", ref, runErr)
	}
	switch exitCode {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		if stderrText != "" {
			return false, fmt.Errorf("gitx: merge %q: exit %d: %s", ref, exitCode, stderrText)
		}
		return false, fmt.Errorf("gitx: merge %q: exit %d", ref, exitCode)
	}
}

// MergeAbort aborts an in-progress merge (git merge --abort), restoring the
// working tree to its pre-merge HEAD.
func MergeAbort(dir string) error {
	if _, err := execx.Run("git", []string{"merge", "--abort"}, execx.Options{Dir: dir}); err != nil {
		return fmt.Errorf("gitx: merge abort: %w", err)
	}
	return nil
}

// MergeInProgress reports whether a merge is currently in progress, via
// "git rev-parse -q --verify MERGE_HEAD". Returns (false, nil) when no merge
// is in progress — the expected, non-error outcome, signaled by git exiting
// 1. Returns (false, err) for any other failure, including when dir is not
// a git repository (exit 128).
func MergeInProgress(dir string) (bool, error) {
	_, err := execx.Run("git", []string{"rev-parse", "-q", "--verify", "MERGE_HEAD"}, execx.Options{Dir: dir})
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("gitx: merge in progress: %w", err)
}

// UnmergedFiles returns the paths with unresolved merge conflicts, via
// "git diff --name-only --diff-filter=U". Returns nil when there are none.
func UnmergedFiles(dir string) ([]string, error) {
	out, err := execx.Run("git", []string{"diff", "--name-only", "--diff-filter=U"}, execx.Options{Dir: dir})
	if err != nil {
		return nil, fmt.Errorf("gitx: unmerged files: %w", err)
	}
	if out == "" {
		return nil, nil
	}
	lines := strings.Split(out, "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}
