package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
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
// openspec/changes/<change>/. Warnings lists non-fatal problems, for example
// a change with no tasks.md.
type OpenspecSaveOut struct {
	Change        string   `json:"change"`
	Branch        string   `json:"branch"`
	BranchCreated bool     `json:"branchCreated"`
	Materialized  string   `json:"materialized"`
	RefsStamped   int      `json:"refsStamped"`
	StagedFiles   []string `json:"stagedFiles"`
	Warnings      []string `json:"warnings"`
	Summary       string   `json:"summary"`
	Next          string   `json:"next"`
}

// openspecSavedHeaderRe matches the plan header line openspec_save writes
// after a save: **OpenSpec-Saved:** openspec/changes/<change>/ (branch <b>).
// Group 1 is the change name, group 2 the optional branch name.
var openspecSavedHeaderRe = regexp.MustCompile(`(?m)^\*\*OpenSpec-Saved:\*\*[ \t]*openspec/changes/([^\s/]+)/?(?:[ \t]*\(branch[ \t]+([^\s)]+)\))?[ \t\r]*$`)

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
// the change name before any git command. It then applies the branch rule
// and the status guard before it creates the branch, so a failed guard
// changes nothing. A failure after the branch is created leaves the repo on
// that branch, and the error Suggestion names it. On success the change
// files are staged and the plan's **OpenSpec-Staging:** line is rewritten to
// **OpenSpec-Saved:**. A plan that already has the Saved line goes to
// openspecSaveAlready.
func openspecSave(workDir string, in OpenspecSaveIn) (OpenspecSaveOut, error) {
	plan, err := openspecSaveReadPlan(in.PlanPath)
	if err != nil {
		return OpenspecSaveOut{}, err
	}
	change, savedBranch, saved, err := openspecSaveHeader(plan)
	if err != nil {
		return OpenspecSaveOut{}, err
	}
	if saved {
		return openspecSaveAlready(workDir, change, savedBranch)
	}

	target := "openspec/" + change
	create, err := openspecSaveGuards(workDir, change)
	if err != nil {
		return OpenspecSaveOut{}, err
	}
	if create {
		if _, err := openspecSaveGit(workDir, "switch", "-c", target); err != nil {
			return OpenspecSaveOut{}, openspecSaveGitInfraError("git switch -c "+target, err)
		}
	}

	out, err := openspecSaveApply(workDir, in.PlanPath, plan, change)
	if err != nil {
		if create {
			openspecSaveNoteBranch(err, target)
		}
		return OpenspecSaveOut{}, err
	}
	out.BranchCreated = create
	return out, nil
}

// openspecSaveReadPlan checks that path is absolute and returns the plan
// file content. A missing file is a DomainError; any other read failure is
// an InfraError.
func openspecSaveReadPlan(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", &mcpserver.DomainError{
			Msg:        fmt.Sprintf("openspec_save: planPath %q is not an absolute path", path),
			Suggestion: openspecSavePlanPathSuggestion,
		}
	}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", &mcpserver.DomainError{
			Msg:        "openspec_save: plan file does not exist: " + err.Error(),
			Suggestion: openspecSavePlanPathSuggestion + " Check that the file exists at that path.",
			Cause:      err,
		}
	case err != nil:
		return "", &mcpserver.InfraError{
			Msg:        "openspec_save: read plan: " + err.Error(),
			Suggestion: "Check that the plan path names a readable file and that you have read access to it, then call openspec_save again.",
			Cause:      err,
		}
	}
	return string(raw), nil
}

// openspecSaveHeader reads the change name from the plan header. saved is
// true for an **OpenSpec-Saved:** line; branch is then the branch the line
// names, or openspec/<change> when it names none. A plan with no header,
// with both headers, or with an invalid change name is a DomainError.
func openspecSaveHeader(plan string) (change, branch string, saved bool, err error) {
	stagedChange, hasStaging := openspec.StagedChangeFromPlan(plan)
	savedMatch := openspecSavedHeaderRe.FindStringSubmatch(plan)
	saved = savedMatch != nil
	switch {
	case !hasStaging && !saved:
		return "", "", false, &mcpserver.DomainError{
			Msg:        "openspec_save: the plan has no **OpenSpec-Staging:** or **OpenSpec-Saved:** header line",
			Suggestion: "Run /sdlc:plan with Create OpenSpec change first.",
		}
	case hasStaging && saved:
		return "", "", false, &mcpserver.DomainError{
			Msg:        "openspec_save: the plan has both an **OpenSpec-Staging:** and an **OpenSpec-Saved:** header line",
			Suggestion: "Keep one header line. Remove the Saved line if the change is not on the default branch yet.",
		}
	}

	change = stagedChange
	if saved {
		change = savedMatch[1]
	}
	if !openspec.ValidChangeName(change) {
		return "", "", false, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("openspec_save: invalid change name %q", change),
			Suggestion: "Rename the change to lowercase letters, digits, and single hyphens in the plan header.",
		}
	}
	branch = "openspec/" + change
	if saved && savedMatch[2] != "" {
		branch = savedMatch[2]
	}
	return change, branch, saved, nil
}

// openspecSaveChangeDir returns the repo-relative change dir with a
// trailing slash: openspec/changes/<change>/.
func openspecSaveChangeDir(change string) string {
	return "openspec/changes/" + change + "/"
}

// openspecSaveAlready handles a plan that already has the Saved line. It
// checks that the current branch is branch and that the change dir exists,
// then runs git add on the change dir again, so a change that was unstaged
// or edited after the first save is staged. An empty StagedFiles then means
// the change dir matches the last commit. The plan file is not changed.
func openspecSaveAlready(workDir, change, branch string) (OpenspecSaveOut, error) {
	changeDir := openspecSaveChangeDir(change)
	current, err := gitx.CurrentBranch(workDir)
	if err != nil {
		return OpenspecSaveOut{}, openspecSaveBranchInfraError(err)
	}
	if current != branch {
		return OpenspecSaveOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("openspec_save: the plan says the change is saved on branch %s, but the current branch is %s", branch, current),
			Suggestion: fmt.Sprintf("git switch %s, then run again. If the change pull request is merged already, there is nothing to save: create a new branch from the default branch and run /sdlc:ship.", branch),
		}
	}
	info, err := os.Stat(filepath.Join(workDir, filepath.FromSlash(changeDir)))
	switch {
	case errors.Is(err, os.ErrNotExist) || (err == nil && !info.IsDir()):
		return OpenspecSaveOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("openspec_save: the plan says the change is saved, but %s does not exist on branch %s", changeDir, branch),
			Suggestion: fmt.Sprintf("Restore %s on branch %s from the commit that added it, then call openspec_save again.", changeDir, branch),
		}
	case err != nil:
		return OpenspecSaveOut{}, &mcpserver.InfraError{
			Msg:        "openspec_save: stat " + changeDir + ": " + err.Error(),
			Suggestion: "Fix read access to " + changeDir + " and call openspec_save again.",
			Cause:      err,
		}
	}
	if _, err := openspecSaveGit(workDir, "add", "--", changeDir); err != nil {
		return OpenspecSaveOut{}, openspecSaveGitInfraError("git add "+changeDir, err)
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
		Materialized: openspec.MaterializedAlready,
		StagedFiles:  staged,
		Summary:      fmt.Sprintf("Change %s is already saved on branch %s. Staged files: %d.", change, branch, len(staged)),
		Next:         next,
	}, nil
}

// openspecSaveGuards applies the branch rule and the status guard. The save
// runs on the default branch (create is true: openspec/<change> must be
// created) or on openspec/<change> (create is false). Tracked files outside
// the change dir and .sdlc-v2/ must have no changes. A failed guard changes
// nothing.
func openspecSaveGuards(workDir, change string) (create bool, err error) {
	changeDir := openspecSaveChangeDir(change)
	target := "openspec/" + change
	current, err := gitx.CurrentBranch(workDir)
	if err != nil {
		return false, openspecSaveBranchInfraError(err)
	}
	def, err := gitx.DefaultBranch(workDir)
	if err != nil {
		return false, openspecSaveBranchInfraError(err)
	}

	if current != target {
		listed, err := openspecSaveGit(workDir, "branch", "--list", target)
		if err != nil {
			return false, openspecSaveGitInfraError("git branch --list", err)
		}
		if strings.TrimSpace(listed) != "" {
			return false, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("openspec_save: branch %s exists, but the current branch is %s", target, current),
				Suggestion: fmt.Sprintf("git switch %s, then run again.", target),
			}
		}
		if current != def {
			return false, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("openspec_save: the current branch %s is not the default branch %s or %s", current, def, target),
				Suggestion: "Switch to the default branch, then run again.",
			}
		}
		create = true
	}

	status, err := openspecSaveGit(workDir, "status", "--porcelain", "--untracked-files=no", "--",
		".", ":!.sdlc-v2/", ":!"+changeDir)
	if err != nil {
		return false, openspecSaveGitInfraError("git status", err)
	}
	if dirty := openspecSaveStatusPaths(status); len(dirty) > 0 {
		return false, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("openspec_save: %d tracked files outside %s have changes", len(dirty), changeDir),
			Suggestion: openspecSaveDirtySuggestion(dirty),
		}
	}
	return create, nil
}

// openspecSaveApply runs the writes of a save on the current branch:
// Materialize, the task ref stamp, git add on the change dir, and the plan
// header rewrite. The returned output has BranchCreated false; the caller
// sets it.
func openspecSaveApply(workDir, planPath, plan, change string) (OpenspecSaveOut, error) {
	changeDir := openspecSaveChangeDir(change)
	target := "openspec/" + change

	res, err := openspec.Materialize(workDir, plan)
	if err != nil {
		return OpenspecSaveOut{}, openspecSaveMaterializeError(err)
	}

	var warnings []string
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
		warnings = append(warnings, changeDir+"tasks.md does not exist, so no task refs were stamped.")
	}
	if _, err := openspecSaveGit(workDir, "add", "--", changeDir); err != nil {
		return OpenspecSaveOut{}, openspecSaveGitInfraError("git add "+changeDir, err)
	}

	savedLine := fmt.Sprintf("**OpenSpec-Saved:** %s (branch %s)", changeDir, target)
	rewritten, ok := openspec.ReplaceStagingHeader(plan, savedLine)
	if !ok {
		return OpenspecSaveOut{}, &mcpserver.DomainError{
			Msg:        "openspec_save: the plan has no **OpenSpec-Staging:** header line to rewrite",
			Suggestion: "Keep the **OpenSpec-Staging:** header on one line of its own, then call openspec_save again.",
		}
	}
	if err := openspecSaveWritePlan(planPath, rewritten); err != nil {
		return OpenspecSaveOut{}, &mcpserver.InfraError{
			Msg:        "openspec_save: write plan header: " + err.Error(),
			Suggestion: "The change is saved on the branch. Fix write access to the plan file and its folder, and call openspec_save again. It returns already.",
			Cause:      err,
		}
	}

	staged, err := openspecSaveStagedFiles(workDir, changeDir)
	if err != nil {
		return OpenspecSaveOut{}, err
	}
	return OpenspecSaveOut{
		Change:       change,
		Branch:       target,
		Materialized: res.Materialized,
		RefsStamped:  refs,
		StagedFiles:  staged,
		Warnings:     warnings,
		Summary:      fmt.Sprintf("Saved change %s on branch %s. Staged files: %d.", change, target, len(staged)),
		Next:         openspecSaveNextCommit,
	}, nil
}

// openspecSaveWritePlan replaces the plan file at path with content through
// fsx.AtomicWriteBytes, so a failed write leaves the old plan whole. The new
// file gets the permission bits of the old one. A symlinked plan path is
// resolved first, so the link stays a link.
func openspecSaveWritePlan(path, content string) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if err := fsx.AtomicWriteBytes(path, []byte(content)); err != nil {
		return err
	}
	return os.Chmod(path, info.Mode().Perm())
}

// openspecSaveNoteBranch adds to the Suggestion of a typed error that the
// save created and switched to branch, so the caller knows where the repo
// is. A retry on that branch continues the save.
func openspecSaveNoteBranch(err error, branch string) {
	note := fmt.Sprintf(" The save created and switched to branch %s. Stay on %s to call openspec_save again.", branch, branch)
	var de *mcpserver.DomainError
	if errors.As(err, &de) {
		de.Suggestion += note
		return
	}
	var ie *mcpserver.InfraError
	if errors.As(err, &ie) {
		ie.Suggestion += note
	}
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
// typed error that names openspec_save in its message and tells the caller
// to call openspec_save again.
func openspecSaveMaterializeError(err error) error {
	const again = "call openspec_save again"
	return materializeError(err, materializeErrorText{
		MsgPrefix:     "openspec_save: ",
		CLIMsgPrefix:  "openspec_save: ",
		CLISuggestion: openspecSaveCLISuggestion,
		RuleRetry:     again,
		InfraRetry:    again,
	})
}

// materializeErrorText is the caller wording materializeError puts into
// each error. execute_state init (mapMaterializeError) and openspec_save
// (openspecSaveMaterializeError) each pass their own.
type materializeErrorText struct {
	MsgPrefix     string // starts the message of a rule error and of an infrastructure error
	CLIMsgPrefix  string // starts the message of a missing-CLI error
	CLISuggestion string // the whole Suggestion of a missing-CLI error
	RuleRetry     string // ends the Suggestion of a rule error: "..., then <RuleRetry>."
	InfraRetry    string // ends the Suggestion of any other infrastructure error
}

// materializeError maps an openspec.Materialize failure to a typed error.
// A missing CLI (ErrCLINotFound) is an InfraError. An invalid change name
// (ErrInvalidChangeName) and every other rule failure (ErrMaterialize) are
// DomainErrors. Any other failure (filesystem errors, a failed git add) is
// an InfraError. See mapMaterializeError for the rule list.
func materializeError(err error, text materializeErrorText) error {
	switch {
	case errors.Is(err, openspec.ErrCLINotFound):
		return &mcpserver.InfraError{
			Msg:        text.CLIMsgPrefix + openspec.ErrCLINotFound.Error(),
			Suggestion: text.CLISuggestion,
			Cause:      err,
		}
	case errors.Is(err, openspec.ErrInvalidChangeName):
		return &mcpserver.DomainError{
			Msg:        text.MsgPrefix + err.Error(),
			Suggestion: openspecNameSuggestion,
			Cause:      err,
		}
	case errors.Is(err, openspec.ErrMaterialize):
		return &mcpserver.DomainError{
			Msg:        text.MsgPrefix + err.Error(),
			Suggestion: "Fix the staged change as the message describes — a conflicting openspec/changes/<change>/, a missing staging dir, a staged file edited after validation, or the validate output — then " + text.RuleRetry + ".",
			Cause:      err,
		}
	default:
		return &mcpserver.InfraError{
			Msg:        text.MsgPrefix + "openspec materialize: " + err.Error(),
			Suggestion: "Check that openspec/config.yaml exists in the active worktree and that the openspec CLI runs there, then " + text.InfraRetry + ".",
			Cause:      err,
		}
	}
}
