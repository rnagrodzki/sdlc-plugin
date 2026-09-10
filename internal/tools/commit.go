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
	if statusOut != "" {
		for _, line := range strings.Split(statusOut, "\n") {
			if strings.HasPrefix(line, "?? ") {
				out.Untracked.Files = append(out.Untracked.Files, strings.TrimPrefix(line, "?? "))
			}
		}
	}
	if out.Untracked.Files == nil {
		out.Untracked.Files = []string{}
	}
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

	return out, nil
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
	SHA string `json:"sha"`
}

// commitApply is the core logic, separated for testability.
func commitApply(cfgRoot, gitRoot string, in CommitApplyIn) (CommitApplyOut, error) {
	if strings.TrimSpace(in.Message) == "" {
		return CommitApplyOut{}, &mcpserver.DataError{
			Msg: "commit message must not be empty",
		}
	}

	// Config check.
	if !in.SkipConfigCheck {
		if err := configmigrate.Verify(cfgRoot); err != nil {
			return CommitApplyOut{}, &mcpserver.DataError{
				Msg:   fmt.Sprintf("config check failed: %s", err.Error()),
				Cause: err,
			}
		}
	}

	// Stage all changes.
	_, err := execx.Run("git", []string{"add", "-A"}, execx.Options{Dir: gitRoot})
	if err != nil {
		return CommitApplyOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("git add: %s", err.Error()),
			Cause: err,
		}
	}

	// Check something is staged.
	stagedNames, err := execx.Run("git", []string{"diff", "--cached", "--name-only"}, execx.Options{Dir: gitRoot})
	if err != nil {
		return CommitApplyOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("git diff --cached: %s", err.Error()),
			Cause: err,
		}
	}
	if strings.TrimSpace(stagedNames) == "" {
		return CommitApplyOut{}, &mcpserver.DataError{
			Msg: "nothing to commit after staging",
		}
	}

	// Commit.
	_, err = execx.Run("git", []string{"commit", "-m", in.Message}, execx.Options{Dir: gitRoot})
	if err != nil {
		return CommitApplyOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("git commit: %s", err.Error()),
			Cause: err,
		}
	}

	// Read SHA.
	sha, err := execx.Run("git", []string{"rev-parse", "HEAD"}, execx.Options{Dir: gitRoot})
	if err != nil {
		return CommitApplyOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("git rev-parse HEAD: %s", err.Error()),
			Cause: err,
		}
	}

	return CommitApplyOut{SHA: strings.TrimSpace(sha)}, nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterCommitTools registers commit_prepare and commit_apply on the server.
func RegisterCommitTools(s *mcpserver.Server) {
	mcpserver.Register(s, "commit_prepare",
		"Gather commit context: staged/unstaged/untracked files, diffs, recent commits, commit config, and branch information.",
		func(ctx mcpserver.Ctx, in CommitPrepareIn) (CommitPrepareOut, error) {
			cfgRoot, err := worktree.MainRoot()
			if err != nil {
				return CommitPrepareOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve main root: %s", err.Error()),
					Cause: err,
				}
			}
			gitRoot, err := worktree.ActiveRoot()
			if err != nil {
				return CommitPrepareOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve active root: %s", err.Error()),
					Cause: err,
				}
			}
			return commitPrepare(cfgRoot, gitRoot, in)
		},
	)

	mcpserver.Register(s, "commit_apply",
		"Stage all changes and create a git commit with the given message, returning the commit SHA.",
		func(ctx mcpserver.Ctx, in CommitApplyIn) (CommitApplyOut, error) {
			cfgRoot, err := worktree.MainRoot()
			if err != nil {
				return CommitApplyOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve main root: %s", err.Error()),
					Cause: err,
				}
			}
			gitRoot, err := worktree.ActiveRoot()
			if err != nil {
				return CommitApplyOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve active root: %s", err.Error()),
					Cause: err,
				}
			}
			return commitApply(cfgRoot, gitRoot, in)
		},
	)
}
