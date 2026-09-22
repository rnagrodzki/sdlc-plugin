package tools

import (
	"fmt"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/difftrunc"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// commit_prepare
// ---------------------------------------------------------------------------

// CommitPrepareIn is the input for the commit_prepare tool.
type CommitPrepareIn struct {
	SkipConfigCheck bool   `json:"skipConfigCheck" jsonschema_description:"Skips the config-version auto-migration gate normally run before gathering commit context. Set only when the caller has already verified or migrated the config."`
	SessionID       string `json:"sessionID" jsonschema_description:"Claude Code session ID. Reserved for future use; not currently read by commit_prepare."`
}

// CommitFlags mirrors the flag object from commit.js.
type CommitFlags struct {
	NoStash            bool    `json:"noStash"`
	Scope              *string `json:"scope"`
	Type               *string `json:"type"`
	Amend              bool    `json:"amend"`
	Auto               bool    `json:"auto"`
	NoSquashWip        bool    `json:"noSquashWip"`
	SkipConfigCheck    bool    `json:"skipConfigCheck"`
	ForceDefaultBranch bool    `json:"forceDefaultBranch"`
}

// CommitStagedInfo holds staged-files information.
type CommitStagedInfo struct {
	Files          []string `json:"files"`
	FileCount      int      `json:"fileCount"`
	Diff           string   `json:"diff"`
	DiffStat       string   `json:"diffStat"`
	DiffTruncated  bool     `json:"diffTruncated"`
	TruncatedFiles []string `json:"truncatedFiles"`
}

// CommitUnstagedInfo holds unstaged-files information.
type CommitUnstagedInfo struct {
	Files      []string `json:"files"`
	FileCount  int      `json:"fileCount"`
	HasChanges bool     `json:"hasChanges"`
}

// CommitUntrackedInfo holds untracked-files information.
type CommitUntrackedInfo struct {
	Files     []string `json:"files"`
	FileCount int      `json:"fileCount"`
}

// CommitWipSquash holds WIP squash detection results.
type CommitWipSquash struct {
	Commits     []string `json:"commits"`
	StagedClean bool     `json:"stagedClean"`
}

// CommitBranchGuard holds branch guard check results.
type CommitBranchGuard struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// CommitPrepareOut is the output for the commit_prepare tool.
type CommitPrepareOut struct {
	Errors            []string            `json:"errors"`
	Warnings          []string            `json:"warnings"`
	CurrentBranch     string              `json:"currentBranch"`
	DefaultBranch     string              `json:"defaultBranch"`
	OnDefaultBranch   bool                `json:"onDefaultBranch"`
	Flags             CommitFlags         `json:"flags"`
	Migration         *string             `json:"migration"`
	CommitConfig      map[string]any      `json:"commitConfig"`
	Staged            CommitStagedInfo    `json:"staged"`
	Unstaged          CommitUnstagedInfo  `json:"unstaged"`
	Untracked         CommitUntrackedInfo `json:"untracked"`
	RecentCommits     []string            `json:"recentCommits"`
	LastCommitMessage *string             `json:"lastCommitMessage"`
	WipSquash         CommitWipSquash     `json:"wipSquash"`
	BranchGuard       CommitBranchGuard   `json:"branchGuard"`
	Next              string              `json:"next"`
	ManifestPath      string              `json:"manifestPath" jsonschema_description:"Path to a JSON manifest file holding this entire result, written to disk so the caller (e.g. the commit skill dispatching sdlc:commit-orchestrator) can pass the path to a subagent instead of round-tripping the full JSON through its own context."`
}

// commitPrepare is the core logic, separated for testability.
func commitPrepare(cfgRoot, gitRoot string, in CommitPrepareIn) (CommitPrepareOut, error) {
	out := CommitPrepareOut{
		Errors:   []string{},
		Warnings: []string{},
		Flags: CommitFlags{
			SkipConfigCheck: in.SkipConfigCheck,
		},
		Staged: CommitStagedInfo{
			Files:          []string{},
			TruncatedFiles: []string{},
		},
		Unstaged: CommitUnstagedInfo{
			Files: []string{},
		},
		Untracked: CommitUntrackedInfo{
			Files: []string{},
		},
		RecentCommits: []string{},
		WipSquash: CommitWipSquash{
			Commits: []string{},
		},
	}

	// Config check (KD5: Verify only, no auto-migrate).
	if !in.SkipConfigCheck {
		if err := configmigrate.Verify(cfgRoot); err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("config check failed: %s", err.Error()))
		}
	}

	// Current branch.
	currentBranch, err := gitx.CurrentBranch(gitRoot)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("currentBranch: %s", err.Error()))
	}
	out.CurrentBranch = currentBranch

	// Default branch.
	defaultBranch, err := gitx.DefaultBranch(gitRoot)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("defaultBranch: %s", err.Error()))
	}
	out.DefaultBranch = defaultBranch

	out.OnDefaultBranch = currentBranch != "" && currentBranch == defaultBranch

	// Commit config section.
	commitCfg, err := config.ReadSection(cfgRoot, "commit")
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("commitConfig: %s", err.Error()))
		// commitConfig stays nil (serializes as null).
	} else {
		out.CommitConfig = commitCfg
	}

	// Staged files (cached diff, name-only).
	stagedNames, err := gitx.Diff(gitRoot, gitx.DiffOpts{Cached: true, NameOnly: true})
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("staged files: %s", err.Error()))
	}
	if stagedNames != "" {
		out.Staged.Files = nonEmptyLines(stagedNames)
	}
	out.Staged.FileCount = len(out.Staged.Files)

	// Staged diff (full).
	stagedDiff, err := gitx.Diff(gitRoot, gitx.DiffOpts{Cached: true})
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("staged diff: %s", err.Error()))
	}

	// Diff truncation.
	truncatedDiff := difftrunc.Truncate(stagedDiff, difftrunc.DefaultDiffMaxBytes, gitx.SplitDiffByFile)
	out.Staged.DiffTruncated = len(stagedDiff) > difftrunc.DefaultDiffMaxBytes
	out.Staged.Diff = truncatedDiff

	// Compute truncated files list.
	if out.Staged.DiffTruncated {
		out.Staged.TruncatedFiles = computeTruncatedFiles(stagedDiff, truncatedDiff)
	}

	// Staged diff stat.
	stagedStat, err := gitx.Diff(gitRoot, gitx.DiffOpts{Cached: true, Stat: true})
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("staged diffStat: %s", err.Error()))
	}
	out.Staged.DiffStat = stagedStat

	// Nothing staged: add error (matches JS behavior).
	if out.Staged.FileCount == 0 {
		out.Errors = append(out.Errors, "no files staged for commit")
	}

	// Unstaged files.
	unstagedNames, err := gitx.Diff(gitRoot, gitx.DiffOpts{NameOnly: true})
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("unstaged files: %s", err.Error()))
	}
	if unstagedNames != "" {
		out.Unstaged.Files = nonEmptyLines(unstagedNames)
	}
	out.Unstaged.FileCount = len(out.Unstaged.Files)
	out.Unstaged.HasChanges = out.Unstaged.FileCount > 0

	// Untracked files.
	statusOut, err := gitx.Status(gitRoot)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("status: %s", err.Error()))
	}
	out.Untracked.Files = untrackedPaths(statusOut)
	out.Untracked.FileCount = len(out.Untracked.Files)

	// Recent commits (last 15, oneline).
	recentRaw, err := execx.Run("git", []string{"log", "--oneline", "-15"}, execx.Options{Dir: gitRoot})
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("recentCommits: %s", err.Error()))
	}
	if recentRaw != "" {
		out.RecentCommits = nonEmptyLines(recentRaw)
	}

	// Last commit message.
	lastMsg, err := execx.Run("git", []string{"log", "-1", "--format=%s"}, execx.Options{Dir: gitRoot})
	if err == nil && lastMsg != "" {
		trimmed := strings.TrimSpace(lastMsg)
		out.LastCommitMessage = &trimmed
	}

	// WIP squash detection.
	out.WipSquash = detectWipSquash(gitRoot)

	// Branch guard (soft check, no config enforcement).
	out.BranchGuard = CommitBranchGuard{OK: true}

	if len(out.Errors) > 0 {
		out.Next = "Fix the errors above, then call commit_prepare again."
	} else {
		out.Next = "Call commit_apply with the prepared payload."
	}

	// Write the manifest to disk so callers (e.g. the commit skill, which
	// dispatches sdlc:commit-orchestrator) can hand a file path to a
	// subagent instead of round-tripping this entire JSON payload through
	// their own context. Mirrors the rest of this function's soft-fail
	// style: a write failure becomes a warning, not a hard error, and
	// ManifestPath is left empty.
	if manifestPath, err := writeCommitManifest(out); err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("manifestPath: %s", err.Error()))
	} else {
		out.ManifestPath = manifestPath
	}

	return out, nil
}

// writeCommitManifest marshals out (with ManifestPath already pointed at the
// file it is about to write) to JSON and writes it via the fsseam, returning
// the path.
func writeCommitManifest(out CommitPrepareOut) (string, error) {
	return writeTempJSON("sdlc-commit-manifest-", "manifest", func(path string) any {
		out.ManifestPath = path
		return out
	})
}

// detectWipSquash detects WIP commits on the current branch since
// divergence from upstream/default. Soft-fails to empty result.
func detectWipSquash(gitRoot string) CommitWipSquash {
	result := CommitWipSquash{
		Commits: []string{},
	}

	// Find fork point from upstream.
	upstream, err := execx.Run("git", []string{
		"rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}",
	}, execx.Options{Dir: gitRoot})
	if err != nil {
		// No upstream: try default branch.
		defBranch, defErr := gitx.DefaultBranch(gitRoot)
		if defErr != nil {
			return result
		}
		upstream = defBranch
	}
	upstream = strings.TrimSpace(upstream)
	if upstream == "" {
		return result
	}

	forkPoint, err := execx.Run("git", []string{"merge-base", upstream, "HEAD"}, execx.Options{Dir: gitRoot})
	if err != nil {
		return result
	}
	forkPoint = strings.TrimSpace(forkPoint)
	if forkPoint == "" {
		return result
	}

	// Log commits since fork point.
	logOut, err := execx.Run("git", []string{
		"log", "--format=%H\t%s", forkPoint + "..HEAD",
	}, execx.Options{Dir: gitRoot})
	if err != nil {
		return result
	}

	for _, line := range nonEmptyLines(logOut) {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			subject := parts[1]
			// WIP commits start with "wip(" or "wip:" (case-insensitive).
			lower := strings.ToLower(subject)
			if strings.HasPrefix(lower, "wip(") || strings.HasPrefix(lower, "wip:") {
				result.Commits = append(result.Commits, line)
			}
		}
	}

	// Staged clean: no staged changes means safe for squash rebase.
	stagedNames, err := gitx.Diff(gitRoot, gitx.DiffOpts{Cached: true, NameOnly: true})
	if err == nil {
		result.StagedClean = strings.TrimSpace(stagedNames) == ""
	}

	return result
}

// computeTruncatedFiles derives the list of files that were omitted by
// truncation, by comparing original and truncated diffs.
func computeTruncatedFiles(original, truncated string) []string {
	origChunks := gitx.SplitDiffByFile(original)
	truncChunks := gitx.SplitDiffByFile(truncated)

	included := make(map[string]bool)
	for _, fd := range truncChunks {
		included[fd.Path] = true
	}

	var omitted []string
	for _, fd := range origChunks {
		if !included[fd.Path] {
			omitted = append(omitted, fd.Path)
		}
	}
	if omitted == nil {
		omitted = []string{}
	}
	return omitted
}

// untrackedPaths returns the untracked entries ("?? <path>") from
// `git status --porcelain` output. A wholly untracked directory is one entry
// with a trailing slash, as git prints it. Never returns nil.
func untrackedPaths(statusOut string) []string {
	files := []string{}
	for _, line := range strings.Split(statusOut, "\n") {
		if strings.HasPrefix(line, "?? ") {
			files = append(files, strings.TrimPrefix(line, "?? "))
		}
	}
	return files
}

// nonEmptyLines splits s by newline and returns non-empty lines.
func nonEmptyLines(s string) []string {
	raw := strings.Split(strings.TrimSpace(s), "\n")
	var result []string
	for _, line := range raw {
		if line != "" {
			result = append(result, line)
		}
	}
	if result == nil {
		result = []string{}
	}
	return result
}

// ---------------------------------------------------------------------------
// commit_apply
// ---------------------------------------------------------------------------

// CommitApplyIn is the input for the commit_apply tool.
type CommitApplyIn struct {
	Message         string `json:"message" jsonschema_description:"Commit message to use for 'git commit -m'. Must not be empty."`
	SkipConfigCheck bool   `json:"skipConfigCheck" jsonschema_description:"Skips the config-version auto-migration gate normally run before staging and committing. Set only when the caller has already verified or migrated the config."`
	SessionID       string `json:"sessionID" jsonschema_description:"Claude Code session ID. Reserved for future use; not currently read by commit_apply."`
}

// CommitApplyOut is the output for the commit_apply tool.
type CommitApplyOut struct {
	SHA                   string   `json:"sha" jsonschema_description:"Full SHA of the commit that was created."`
	Summary               string   `json:"summary" jsonschema_description:"Plain-language result: the short SHA, how many files the commit holds, and which untracked paths were left out. Quote this instead of assuming everything in the working tree was committed."`
	SkippedUntrackedPaths []string `json:"skippedUntrackedPaths" jsonschema_description:"Untracked paths that commit_apply did NOT stage or commit; a wholly untracked directory is listed as dir/. commit_apply stages only tracked files that have changes, plus files already staged, and never anything under .sdlc-v2/. Renders as (none) when nothing was skipped. Never report these paths as committed: run git add on any that belong in the change, then commit again."`
}

// scopedStagePaths returns the explicit path list commit_apply stages: tracked
// files whose working-tree state differs from the index (modified or deleted).
// That is the tracked half of what commit_prepare lists, minus what is already
// staged. Files already in the index are left out on purpose: they need no
// staging, and git add fails with "pathspec did not match" on a path that is
// staged as deleted or renamed away. Untracked files are never in scope, and
// everything under paths.DataDir is dropped whatever git reports.
//
// -z keeps names with spaces, quotes, or non-ASCII bytes intact. Without it git
// wraps them in quotes, and the later git add would not match the file.
func scopedStagePaths(gitRoot string) ([]string, error) {
	out, err := execx.Run("git", []string{"diff", "--name-only", "-z"}, execx.Options{Dir: gitRoot})
	if err != nil {
		return nil, err
	}
	scoped := []string{}
	for _, p := range strings.Split(out, "\x00") {
		if p == "" || p == paths.DataDir || strings.HasPrefix(p, paths.DataDir+"/") {
			continue
		}
		scoped = append(scoped, p)
	}
	return scoped, nil
}

// maxListedPaths caps how many paths a message names inline. The full list
// always stays in SkippedUntrackedPaths.
const maxListedPaths = 10

// listPaths joins up to maxListedPaths entries for use inside a message, and
// returns "(none)" for an empty list.
func listPaths(list []string) string {
	if len(list) == 0 {
		return "(none)"
	}
	if len(list) <= maxListedPaths {
		return strings.Join(list, ", ")
	}
	return fmt.Sprintf("%s, and %d more", strings.Join(list[:maxListedPaths], ", "), len(list)-maxListedPaths)
}

// commitApplySummary builds the plain-language Summary for CommitApplyOut.
func commitApplySummary(sha string, fileCount int, skipped []string) string {
	short := sha
	if len(short) > 7 {
		short = short[:7]
	}
	summary := fmt.Sprintf("Committed %s with %d file(s).", short, fileCount)
	if len(skipped) == 0 {
		return summary + " No untracked paths were left out."
	}
	return summary + fmt.Sprintf(
		" %d untracked path(s) were NOT committed and are still untracked: %s. commit_apply stages only tracked changes and files already staged. Run git add on any that belong in this change, then commit again. Do not report them as committed.",
		len(skipped), listPaths(skipped))
}

// commitApply is the core logic, separated for testability.
//
// Staging is scoped, never tree-wide: it stages the explicit path list from
// scopedStagePaths and commits together with whatever is already staged.
// Untracked files are left alone and returned in SkippedUntrackedPaths.
func commitApply(cfgRoot, gitRoot string, in CommitApplyIn) (CommitApplyOut, error) {
	if strings.TrimSpace(in.Message) == "" {
		return CommitApplyOut{}, &mcpserver.DataError{
			Msg:        "commit message must not be empty",
			Suggestion: "Draft the commit message from the commit_prepare payload (or its manifest file) and call commit_apply again with a non-empty message.",
		}
	}

	// Config check.
	if !in.SkipConfigCheck {
		if err := configmigrate.Verify(cfgRoot); err != nil {
			return CommitApplyOut{}, &mcpserver.DataError{
				Msg:        fmt.Sprintf("config check failed: %s", err.Error()),
				Suggestion: "Run the sdlc migrate tool to bring the project config up to date, then retry commit_apply. Pass skipConfigCheck only when the mismatch is known and intentional.",
				Cause:      err,
			}
		}
	}

	// Resolve the scope before touching the index. Both queries are read-only,
	// so a failure here leaves the repository exactly as it was.
	scopedPaths, err := scopedStagePaths(gitRoot)
	if err != nil {
		return CommitApplyOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("git diff --name-only: %s", err.Error()),
			Suggestion: "Inspect the repository with git status — the index may be locked or corrupt. Resolve it, then retry commit_apply.",
			Cause:      err,
		}
	}
	statusOut, err := gitx.Status(gitRoot)
	if err != nil {
		return CommitApplyOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("git status: %s", err.Error()),
			Suggestion: "Inspect the repository with git status — the index may be locked or corrupt. Resolve it, then retry commit_apply.",
			Cause:      err,
		}
	}
	skipped := untrackedPaths(statusOut)

	// Stage the explicit path list, never the whole tree. --literal-pathspecs
	// stops a file name with glob characters or a leading ':' from being read as
	// a pattern that matches more than that one file. An empty list skips the
	// call: git add with no pathspec stages nothing. Paths outside scopedPaths
	// stay untracked and are reported in SkippedUntrackedPaths.
	if len(scopedPaths) > 0 {
		args := append([]string{"--literal-pathspecs", "add", "--"}, scopedPaths...)
		if _, err := execx.Run("git", args, execx.Options{Dir: gitRoot}); err != nil {
			return CommitApplyOut{}, &mcpserver.InfraError{
				Msg:        fmt.Sprintf("git add: %s", err.Error()),
				Suggestion: "Inspect the working tree with git status: an unresolved merge conflict, a lock file, or a permission problem blocks staging. Resolve it, then retry commit_apply.",
				Cause:      err,
			}
		}
	}

	// Check something is staged.
	stagedNames, err := execx.Run("git", []string{"diff", "--cached", "--name-only"}, execx.Options{Dir: gitRoot})
	if err != nil {
		return CommitApplyOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("git diff --cached: %s", err.Error()),
			Suggestion: "Inspect the repository with git status — the index may be locked or corrupt. Resolve it, then retry commit_apply.",
			Cause:      err,
		}
	}
	if strings.TrimSpace(stagedNames) == "" {
		return CommitApplyOut{}, &mcpserver.DataError{
			Msg: fmt.Sprintf("nothing to commit: pathspec %s produced no staged changes and nothing was staged before; untracked paths left out: %s",
				listPaths(scopedPaths), listPaths(skipped)),
			Suggestion: "commit_apply stages only tracked files that have changes, plus files already staged. It never stages untracked files or anything under .sdlc-v2/. If the change you want is in the untracked paths named above, run git add on them and call commit_apply again. Otherwise modify or add files first, then call commit_prepare again to build a fresh payload before retrying commit_apply.",
		}
	}

	// Commit.
	_, err = execx.Run("git", []string{"commit", "-m", in.Message}, execx.Options{Dir: gitRoot})
	if err != nil {
		return CommitApplyOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("git commit: %s", err.Error()),
			Suggestion: "Read the git output above: a failing commit hook or a missing user.name/user.email is the usual cause. Fix it, then retry commit_apply with the same message.",
			Cause:      err,
		}
	}

	// Read SHA.
	sha, err := execx.Run("git", []string{"rev-parse", "HEAD"}, execx.Options{Dir: gitRoot})
	if err != nil {
		return CommitApplyOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("git rev-parse HEAD: %s", err.Error()),
			Suggestion: "The commit may already have landed — run git log -1 to check before retrying commit_apply, so the same change is not committed twice.",
			Cause:      err,
		}
	}

	sha = strings.TrimSpace(sha)
	return CommitApplyOut{
		SHA:                   sha,
		Summary:               commitApplySummary(sha, len(nonEmptyLines(stagedNames)), skipped),
		SkippedUntrackedPaths: skipped,
	}, nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterCommitTools registers commit_prepare and commit_apply on the server.
func RegisterCommitTools(s *mcpserver.Server) {
	mcpserver.Register(s, "commit_prepare",
		"Gather commit context: staged/unstaged/untracked files, diffs, recent commits, commit config, and branch information. Also writes the full result as a JSON manifest into a new temp directory and returns its path as manifestPath (hand that path to sdlc:commit-orchestrator instead of the payload); if the write fails, manifestPath is empty and the reason is appended to warnings.",
		mcpserver.Annotations{
			Title:      "Prepare commit context",
			ReadOnly:   true,
			Idempotent: true,
			OpenWorld:  false,
		},
		func(ctx mcpserver.Ctx, in CommitPrepareIn) (CommitPrepareOut, error) {
			cfgRoot, err := worktree.MainRoot()
			if err != nil {
				return CommitPrepareOut{}, &mcpserver.InfraError{
					Msg:        fmt.Sprintf("resolve main root: %s", err.Error()),
					Suggestion: "Run commit_prepare from inside a git repository (or one of its worktrees) so the main root can be resolved, then retry.",
					Cause:      err,
				}
			}
			gitRoot, err := worktree.ActiveRoot()
			if err != nil {
				return CommitPrepareOut{}, &mcpserver.InfraError{
					Msg:        fmt.Sprintf("resolve active root: %s", err.Error()),
					Suggestion: "Run commit_prepare from inside a git working tree — the current directory is not one. Change into the repository, then retry.",
					Cause:      err,
				}
			}
			return commitPrepare(cfgRoot, gitRoot, in)
		},
	)

	mcpserver.Register(s, "commit_apply",
		"Create a git commit with the given message from the tracked changes in the working tree plus anything already staged, and return the commit SHA. Files are staged by an explicit path list, never by sweeping the whole tree: untracked files are not staged or deleted and are named in skippedUntrackedPaths, and nothing under .sdlc-v2/ is staged. git add any untracked file that belongs in the commit before calling.",
		mcpserver.Annotations{
			Title:       "Create a git commit",
			ReadOnly:    false,
			Destructive: true,
			Idempotent:  false,
			OpenWorld:   false,
		},
		func(ctx mcpserver.Ctx, in CommitApplyIn) (CommitApplyOut, error) {
			cfgRoot, err := worktree.MainRoot()
			if err != nil {
				return CommitApplyOut{}, &mcpserver.InfraError{
					Msg:        fmt.Sprintf("resolve main root: %s", err.Error()),
					Suggestion: "Run commit_apply from inside a git repository (or one of its worktrees) so the main root can be resolved, then retry.",
					Cause:      err,
				}
			}
			gitRoot, err := worktree.ActiveRoot()
			if err != nil {
				return CommitApplyOut{}, &mcpserver.InfraError{
					Msg:        fmt.Sprintf("resolve active root: %s", err.Error()),
					Suggestion: "Run commit_apply from inside a git working tree — the current directory is not one. Change into the repository, then retry.",
					Cause:      err,
				}
			}
			return commitApply(cfgRoot, gitRoot, in)
		},
	)
}
