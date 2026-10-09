package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/openspec"
)

// OpenspecSaveIn is the input for the openspec_save tool.
type OpenspecSaveIn struct {
	PlanPath string `json:"planPath" jsonschema_description:"Plain text absolute path of the plan file with an **OpenSpec-Staging:** header. Example: /Users/me/.claude/plans/add-widget.md"`
}

// OpenspecSaveOut is the output of the openspec_save tool. Materialized is
// "created" or "already". StagedFiles lists the staged paths under
// openspec/changes/<change>/.
type OpenspecSaveOut struct {
	Change        string   `json:"change"`
	Branch        string   `json:"branch"`
	BranchCreated bool     `json:"branchCreated"`
	Materialized  string   `json:"materialized"`
	RefsStamped   int      `json:"refsStamped"`
	StagedFiles   []string `json:"stagedFiles"`
	Summary       string   `json:"summary"`
	Next          string   `json:"next"`
}

// openspecSavedHeaderRe matches the plan header line openspec_save writes
// after a save: **OpenSpec-Saved:** openspec/changes/<change>/ (branch <b>).
// Group 1 is the change name, group 2 the optional branch name.
var openspecSavedHeaderRe = regexp.MustCompile(`(?m)^\*\*OpenSpec-Saved:\*\*[ \t]*openspec/changes/([^\s/]+)/?(?:[ \t]*\(branch[ \t]+([^\s)]+)\))?[ \t\r]*$`)

// openspecStagingLineRe matches the whole **OpenSpec-Staging:** header line,
// in the same shape openspec.StagedChangeFromPlan reads, so the save can
// replace it with the Saved line. A trailing carriage return of a CRLF plan
// is part of the match.
var openspecStagingLineRe = regexp.MustCompile(`(?m)^\*\*OpenSpec-Staging:\*\*[ \t]*\.sdlc-v2/openspec-staging/[^\s/]+/?[ \t\r]*$`)

// openspecSaveMaxListedPaths caps how many dirty paths the status guard
// names in its Suggestion.
const openspecSaveMaxListedPaths = 10

// Suggestions and next steps shared by several openspec_save return paths.
const (
	openspecSavePlanPathSuggestion = "Pass the absolute path of the plan file."
	openspecSaveGitSuggestion      = "Check git status, fix the repository state, and call openspec_save again."
	openspecSaveNextCommit         = "Run the commit skill with --type docs, then the pr skill."
	openspecSaveNextNothingStaged  = "Nothing is staged for the change. Run the pr skill if the branch has no pull request yet."
)

// openspecSaveGit runs one git command in dir and returns its output with
// trailing whitespace trimmed and leading whitespace kept. Tests replace it
// to make one git step fail.
var openspecSaveGit = func(dir string, args ...string) (string, error) {
	return execx.Run("git", args, execx.Options{Dir: dir, KeepLeadingSpace: true})
}

// openspecSave saves the OpenSpec change staged by the plan at in.PlanPath
// into openspec/changes/<change>/ on the branch openspec/<change>. Every git
// command and the change files use workDir. The plan file is read and
// rewritten at in.PlanPath. It checks the plan path, the header lines, and
// the change name before any git command. It then applies
// the branch rule and the status guard before it creates the branch, so a
// failed guard changes nothing. On success the change files are staged and
// the plan's **OpenSpec-Staging:** line is rewritten to **OpenSpec-Saved:**.
// A plan that already has the Saved line returns materialized "already".
func openspecSave(workDir string, in OpenspecSaveIn) (OpenspecSaveOut, error) {
	if !filepath.IsAbs(in.PlanPath) {
		return OpenspecSaveOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("openspec_save: planPath %q is not an absolute path", in.PlanPath),
			Suggestion: openspecSavePlanPathSuggestion,
		}
	}
	raw, err := os.ReadFile(in.PlanPath)
	if err != nil {
		return OpenspecSaveOut{}, &mcpserver.DomainError{
			Msg:        "openspec_save: read plan: " + err.Error(),
			Suggestion: openspecSavePlanPathSuggestion,
			Cause:      err,
		}
	}
	plan := string(raw)

	stagedChange, hasStaging := openspec.StagedChangeFromPlan(plan)
	savedMatch := openspecSavedHeaderRe.FindStringSubmatch(plan)
	hasSaved := savedMatch != nil
	switch {
	case !hasStaging && !hasSaved:
		return OpenspecSaveOut{}, &mcpserver.DomainError{
			Msg:        "openspec_save: the plan has no **OpenSpec-Staging:** or **OpenSpec-Saved:** header line",
			Suggestion: "Run /sdlc:plan with Create OpenSpec change first.",
		}
	case hasStaging && hasSaved:
		return OpenspecSaveOut{}, &mcpserver.DomainError{
			Msg:        "openspec_save: the plan has both an **OpenSpec-Staging:** and an **OpenSpec-Saved:** header line",
			Suggestion: "Keep one header line. Remove the Saved line if the change is not on the default branch yet.",
		}
	}

	change := stagedChange
	if hasSaved {
		change = savedMatch[1]
	}
	if !openspec.ValidChangeName(change) {
		return OpenspecSaveOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("openspec_save: invalid change name %q", change),
			Suggestion: "Rename the change to lowercase letters, digits, and single hyphens in the plan header.",
		}
	}
	changeDir := "openspec/changes/" + change + "/"
	target := "openspec/" + change

	if hasSaved {
		branch := target
		if savedMatch[2] != "" {
			branch = savedMatch[2]
		}
		staged, err := openspecSaveStagedFiles(workDir, changeDir)
		if err != nil {
			return OpenspecSaveOut{}, err
		}
		next := openspecSaveNextCommit
		if len(staged) == 0 {
			next = openspecSaveNextNothingStaged
		}
		return OpenspecSaveOut{
			Change:       change,
			Branch:       branch,
			Materialized: "already",
			StagedFiles:  staged,
			Summary:      fmt.Sprintf("Change %s is already saved on branch %s. Staged files: %d.", change, branch, len(staged)),
			Next:         next,
		}, nil
	}

	current, err := gitx.CurrentBranch(workDir)
	if err != nil {
		return OpenspecSaveOut{}, openspecSaveBranchInfraError(err)
	}
	def, err := gitx.DefaultBranch(workDir)
	if err != nil {
		return OpenspecSaveOut{}, openspecSaveBranchInfraError(err)
	}

	create := false
	if current != target {
		listed, err := openspecSaveGit(workDir, "branch", "--list", target)
		if err != nil {
			return OpenspecSaveOut{}, openspecSaveGitInfraError("git branch --list", err)
		}
		if strings.TrimSpace(listed) != "" {
			return OpenspecSaveOut{}, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("openspec_save: branch %s exists, but the current branch is %s", target, current),
				Suggestion: fmt.Sprintf("git switch %s, then run again.", target),
			}
		}
		if current != def {
			return OpenspecSaveOut{}, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("openspec_save: the current branch %s is not the default branch %s or %s", current, def, target),
				Suggestion: "Switch to the default branch, then run again.",
			}
		}
		create = true
	}

	status, err := openspecSaveGit(workDir, "status", "--porcelain", "--untracked-files=no", "--",
		".", ":!.sdlc-v2/", ":!"+changeDir)
	if err != nil {
		return OpenspecSaveOut{}, openspecSaveGitInfraError("git status", err)
	}
	if dirty := openspecSaveStatusPaths(status); len(dirty) > 0 {
		return OpenspecSaveOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("openspec_save: %d tracked files outside %s have changes", len(dirty), changeDir),
			Suggestion: openspecSaveDirtySuggestion(dirty),
		}
	}

	if create {
		if _, err := openspecSaveGit(workDir, "switch", "-c", target); err != nil {
			return OpenspecSaveOut{}, openspecSaveGitInfraError("git switch -c "+target, err)
		}
	}

	res, err := openspec.Materialize(workDir, plan)
	if err != nil {
		return OpenspecSaveOut{}, openspecSaveMaterializeError(err)
	}

	tasksPath := filepath.Join(workDir, "openspec", "changes", change, "tasks.md")
	refs, err := stampTaskRefs(tasksPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return OpenspecSaveOut{}, &mcpserver.InfraError{
				Msg:        "openspec_save: stamp task refs in " + tasksPath + ": " + err.Error(),
				Suggestion: "Fix write access to " + tasksPath + " and call openspec_save again.",
				Cause:      err,
			}
		}
		refs = 0
	}
	if _, err := openspecSaveGit(workDir, "add", "--", changeDir); err != nil {
		return OpenspecSaveOut{}, openspecSaveGitInfraError("git add "+changeDir, err)
	}

	savedLine := fmt.Sprintf("**OpenSpec-Saved:** %s (branch %s)", changeDir, target)
	rewritten := openspecStagingLineRe.ReplaceAllLiteralString(plan, savedLine)
	if err := os.WriteFile(in.PlanPath, []byte(rewritten), 0o644); err != nil {
		return OpenspecSaveOut{}, &mcpserver.InfraError{
			Msg:        "openspec_save: write plan header: " + err.Error(),
			Suggestion: "The change is saved on the branch. Fix write access to the plan file and call openspec_save again. It returns already.",
			Cause:      err,
		}
	}

	staged, err := openspecSaveStagedFiles(workDir, changeDir)
	if err != nil {
		return OpenspecSaveOut{}, err
	}
	return OpenspecSaveOut{
		Change:        change,
		Branch:        target,
		BranchCreated: create,
		Materialized:  res.Materialized,
		RefsStamped:   refs,
		StagedFiles:   staged,
		Summary:       fmt.Sprintf("Saved change %s on branch %s. Staged files: %d.", change, target, len(staged)),
		Next:          openspecSaveNextCommit,
	}, nil
}

// openspecSaveStagedFiles returns the staged paths under changeDir, from
// `git diff --cached --name-only`. The result holds no path when nothing is
// staged.
func openspecSaveStagedFiles(workDir, changeDir string) ([]string, error) {
	out, err := openspecSaveGit(workDir, "diff", "--cached", "--name-only", "--", changeDir)
	if err != nil {
		return nil, openspecSaveGitInfraError("git diff --cached", err)
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// openspecSaveStatusPaths returns the path of each `git status --porcelain`
// line. For a rename line ("R  old -> new") it returns the new path.
func openspecSaveStatusPaths(status string) []string {
	var paths []string
	for _, line := range strings.Split(status, "\n") {
		if len(line) < 4 {
			continue
		}
		p := line[3:]
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+len(" -> "):]
		}
		paths = append(paths, p)
	}
	return paths
}

// openspecSaveDirtySuggestion names up to openspecSaveMaxListedPaths dirty
// paths and the count of the rest, then tells the caller to commit or stash
// them.
func openspecSaveDirtySuggestion(paths []string) string {
	listed := paths
	extra := ""
	if len(paths) > openspecSaveMaxListedPaths {
		listed = paths[:openspecSaveMaxListedPaths]
		extra = fmt.Sprintf(" and %d more", len(paths)-openspecSaveMaxListedPaths)
	}
	return strings.Join(listed, ", ") + extra + ": commit or stash them first."
}

// openspecSaveBranchInfraError wraps a failure to read the current or the
// default branch.
func openspecSaveBranchInfraError(err error) error {
	return &mcpserver.InfraError{
		Msg:        "openspec_save: " + err.Error(),
		Suggestion: "Run from a git repository that has a main or master branch, or set origin/HEAD, then call openspec_save again.",
		Cause:      err,
	}
}

// openspecSaveGitInfraError wraps a failed git command named by what.
func openspecSaveGitInfraError(what string, err error) error {
	return &mcpserver.InfraError{
		Msg:        "openspec_save: " + what + ": " + err.Error(),
		Suggestion: openspecSaveGitSuggestion,
		Cause:      err,
	}
}

// openspecSaveCLISuggestion is the Suggestion for a missing OpenSpec CLI.
// The shared openspecCLISuggestion offers "Skip OpenSpec", a plan skill
// choice that does not exist for openspec_save.
const openspecSaveCLISuggestion = "Install the OpenSpec CLI (npm i -g @fission-ai/openspec), then call openspec_save again."

// openspecSaveMaterializeError maps an openspec.Materialize failure to a
// typed error that names openspec_save. mapMaterializeError words its
// message and suggestion for execute_state init. This wrapper removes the
// "init: " message prefix, replaces the "retry execute_state init" text of a
// rule error, ends the suggestion of any other infrastructure failure with
// "then call openspec_save again.", and replaces the suggestion for a missing
// OpenSpec CLI.
func openspecSaveMaterializeError(err error) error {
	const initPrefix = "init: "
	const initRetry = "retry execute_state init"
	const again = "call openspec_save again"
	mapped := mapMaterializeError(err)
	switch e := mapped.(type) {
	case *mcpserver.DomainError:
		e.Msg = "openspec_save: " + strings.TrimPrefix(e.Msg, initPrefix)
		e.Suggestion = strings.ReplaceAll(e.Suggestion, initRetry, again)
	case *mcpserver.InfraError:
		e.Msg = "openspec_save: " + strings.TrimPrefix(e.Msg, initPrefix)
		if errors.Is(err, openspec.ErrCLINotFound) {
			e.Suggestion = openspecSaveCLISuggestion
		} else {
			e.Suggestion = strings.ReplaceAll(e.Suggestion, "then retry.", "then "+again+".")
		}
	}
	return mapped
}
