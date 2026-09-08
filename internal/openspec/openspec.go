// Package openspec bridges to the external `openspec` CLI (see
// https://github.com/Fission-AI/OpenSpec). It never reimplements the CLI's
// own internals — task counting, spec parsing, status derivation — all of
// that data is taken verbatim from `openspec list --json` output. The one
// piece of logic that IS reimplemented here is branch-to-change matching,
// because that is sdlc-utilities' own feature, not something the openspec
// CLI itself provides.
//
// Mirrors scripts/lib/openspec.js:1-30 (detectActiveChanges and friends),
// blending its filesystem-based `present` check with the CLI-availability
// (`cliAvailable`) check used by validateChangeStrict/runArchive/
// getRequirementInventory elsewhere in the source file.
package openspec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
)

// Change is a single OpenSpec change as reported by `openspec list --json`.
// Field names/shape mirror the CLI's own JSON output (changes[].name,
// completedTasks, totalTasks, status) rather than the source script's
// analyzeChange() shape, since Detect trusts the CLI instead of
// recomputing these values from the filesystem.
type Change struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	CompletedTasks int    `json:"completedTasks"`
	TotalTasks     int    `json:"totalTasks"`
}

// Info is the result of Detect: the OpenSpec state of a project root.
type Info struct {
	// Installed reports whether the `openspec` binary is present on PATH.
	Installed bool
	// Initialized reports whether openspec/config.yaml exists under root.
	Initialized bool
	// Changes is the list of active changes reported by the CLI. Empty
	// when Installed or Initialized is false.
	Changes []Change
	// BranchMatch is the Change (a pointer into Changes) whose name
	// matches the current git branch, or nil if none matched. Matching is
	// two-stage, mirroring the source: a branch-slug match first
	// (matchBranch), falling back to a changed-files match
	// (matchChangedFiles) when the slug match finds nothing.
	BranchMatch *Change
}

// BranchPrefixRe strips a conventional-commit-style branch prefix before
// slug matching, mirroring the source's
// `branch.toLowerCase().replace(/^(feat|fix|chore|refactor|docs)\//, ”)`.
//
// Exported (Task 37 Ruling A consolidation): this is the same pattern
// tools/plan.go's own detectActiveChanges duplicated as an unexported var
// of the same name; rather than carry two byte-identical regexes, the
// relocated DetectActiveChanges below also references this one.
var BranchPrefixRe = regexp.MustCompile(`^(feat|fix|chore|refactor|docs)/`)

// changeListOutput is the top-level shape of `openspec list --json`.
type changeListOutput struct {
	Changes []Change `json:"changes"`
}

// Detect shells out to the external `openspec` CLI to determine whether
// OpenSpec is installed, whether root has been initialized (openspec/
// config.yaml present), the list of active changes, and — best effort —
// which change (if any) matches the current git branch name.
//
// Detect degrades gracefully rather than escalating errors: an absent
// `openspec` binary surfaces as Installed=false with a nil error (matching
// the source's ENOENT-is-not-an-error handling in validateChangeStrict/
// runArchive/getRequirementInventory), and a CLI or git failure after that
// point simply leaves the corresponding Info field at its zero value. The
// only error Detect itself returns is for an inaccessible root directory.
func Detect(root string) (*Info, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("openspec: root %q: %w", root, err)
	}

	info := &Info{}

	if _, err := os.Stat(filepath.Join(root, "openspec", "config.yaml")); err == nil {
		info.Initialized = true
	}

	out, runErr := execx.Run("openspec", []string{"list", "--json"}, execx.Options{Dir: root})
	if runErr != nil {
		if errors.Is(runErr, exec.ErrNotFound) {
			// Binary not on PATH: not installed, not an error.
			info.Installed = false
		} else {
			// Binary present but the command failed (e.g. root not an
			// OpenSpec project): still installed, no changes to report.
			info.Installed = true
		}
		return info, nil
	}
	info.Installed = true

	if !info.Initialized {
		return info, nil
	}

	changes, parseErr := parseChangeList(out)
	if parseErr != nil {
		// Unexpected output shape: degrade rather than fail Detect.
		return info, nil
	}
	info.Changes = changes

	if branch, err := gitx.CurrentBranch(root); err == nil && branch != "" {
		info.BranchMatch = matchBranch(branch, info.Changes)
		if info.BranchMatch == nil && len(info.Changes) > 0 {
			info.BranchMatch = matchChangedFiles(root, branch, info.Changes)
		}
	}

	return info, nil
}

// parseChangeList parses `openspec list --json` stdout into a Change slice.
func parseChangeList(out string) ([]Change, error) {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil, nil
	}
	var parsed changeListOutput
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil, err
	}
	return parsed.Changes, nil
}

// matchChangedFiles mirrors scripts/lib/openspec.js:206-223, the fallback
// that runs when slug matching (matchBranch) finds nothing: diff the current
// branch's committed files against the repo's base branch, and if exactly
// one active change's `openspec/changes/<name>/` directory was touched,
// treat that as the match. Skipped when branch is itself the base branch.
// Any git failure degrades to "no match" rather than an error, matching the
// source's outer try/catch around the whole branch-matching block.
func matchChangedFiles(root, branch string, changes []Change) *Change {
	base := detectBaseBranchSafe(root)
	if branch == base {
		return nil
	}

	out, err := execx.Run("git", []string{"diff", "--name-only", base + "...HEAD"}, execx.Options{Dir: root})
	if err != nil {
		return nil
	}

	hits := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for i := range changes {
			if strings.HasPrefix(line, "openspec/changes/"+changes[i].Name+"/") {
				hits[changes[i].Name] = true
			}
		}
	}
	if len(hits) != 1 {
		return nil
	}
	var name string
	for n := range hits {
		name = n
	}
	for i := range changes {
		if changes[i].Name == name {
			return &changes[i]
		}
	}
	return nil
}

// detectBaseBranchSafe mirrors scripts/lib/git.js's detectBaseBranchSafe:
// try the remote's HEAD symref first (`origin/HEAD` stripped of the
// `refs/remotes/origin/` prefix), then `main`, then `master`; return "main"
// if none of those resolve. Never errors — matching the "Safe" variant's
// swallow-everything contract.
func detectBaseBranchSafe(root string) string {
	if out, err := execx.Run("git", []string{"symbolic-ref", "refs/remotes/origin/HEAD"}, execx.Options{Dir: root}); err == nil {
		ref := strings.TrimSpace(out)
		if name := strings.TrimPrefix(ref, "refs/remotes/origin/"); name != "" && name != ref {
			return name
		}
	}
	for _, candidate := range []string{"main", "master"} {
		if out, err := execx.Run("git", []string{"rev-parse", "--verify", candidate}, execx.Options{Dir: root}); err == nil && strings.TrimSpace(out) != "" {
			return candidate
		}
	}
	return "main"
}

// matchBranch mirrors scripts/lib/openspec.js's slug-matching block
// (detectActiveChanges, branch-slug section): strip a conventional-commit
// branch prefix, then match the resulting slug against each change name
// either exactly or as a `/`- or `-`-bounded substring. Returns a pointer
// into changes so callers can distinguish "matched" from "no match" without
// an extra found flag.
func matchBranch(branch string, changes []Change) *Change {
	if len(changes) == 0 {
		return nil
	}
	branchSlug := BranchPrefixRe.ReplaceAllString(strings.ToLower(branch), "")
	for i := range changes {
		nameSlug := strings.ToLower(changes[i].Name)
		if branchSlug == nameSlug {
			return &changes[i]
		}
		pattern := `(^|[/-])` + regexp.QuoteMeta(nameSlug) + `($|[/-])`
		if regexp.MustCompile(pattern).MatchString(branchSlug) {
			return &changes[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Filesystem-walking OpenSpec status (Task 37 Ruling A relocation)
//
// The functions and types below were moved verbatim from
// internal/tools/plan.go, where they were unexported and re-implemented
// (independently of Detect above) for plan_prepare's --from-openspec
// support: lib/openspec.js's detectActiveChanges() reads OpenSpec state
// directly off the filesystem (proposal.md/design.md/tasks.md presence,
// spec .md counts, task-checkbox progress) rather than shelling out to the
// `openspec` CLI the way Detect does above. internal/hooks' session-start
// handler (Task 37) needs this same filesystem-walking shape — Stage,
// DeltaSpecCount, Present, SpecsCount — none of which Detect's Info/Change
// types carry, so this is a deliberate architectural fork from Detect, not
// a naming collision: two different OpenSpec status representations
// (CLI-trusting vs. filesystem-walking) coexist in this package on purpose.
//
// This is a pure relocation: no logic changed. tools/plan.go now holds
// type aliases (OpenspecChangeInfo = openspec.OpenspecChangeInfo, etc.) and
// calls DetectActiveChanges/AnalyzeChange/CountMdFiles/IsSafeChangeName so
// its own call sites keep compiling unchanged.
// ---------------------------------------------------------------------------

// OpenspecChangeInfo mirrors lib/openspec.js's analyzeChange() result shape.
type OpenspecChangeInfo struct {
	Name           string  `json:"name"`
	Stage          *string `json:"stage"`
	DeltaSpecCount int     `json:"deltaSpecCount"`
	HasProposal    bool    `json:"hasProposal"`
	HasDesign      bool    `json:"hasDesign"`
	HasTasks       bool    `json:"hasTasks"`
	TasksDone      int     `json:"tasksDone"`
	TasksTotal     int     `json:"tasksTotal"`
}

// OpenspecAuthoritative is added to OpenspecInfo only when present is true.
type OpenspecAuthoritative struct {
	Path       string `json:"path"`
	SpecsCount int    `json:"specsCount"`
}

// OpenspecInfo mirrors lib/openspec.js's detectActiveChanges() result shape.
type OpenspecInfo struct {
	Present       bool                   `json:"present"`
	SpecsCount    int                    `json:"specsCount"`
	ActiveChanges []OpenspecChangeInfo   `json:"activeChanges"`
	BranchMatch   *string                `json:"branchMatch"`
	Authoritative *OpenspecAuthoritative `json:"authoritative,omitempty"`
}

// IsSafeChangeName rejects path-traversal-unsafe OpenSpec change names,
// mirroring lib/openspec.js's isValidChangeName.
func IsSafeChangeName(name string) bool {
	return name != "" &&
		!strings.Contains(name, "/") &&
		!strings.Contains(name, "\\") &&
		!strings.Contains(name, "..") &&
		!strings.Contains(name, "\x00")
}

// CountMdFiles recursively counts .md files under dir, mirroring
// lib/openspec.js's countMdFiles (returns 0 for a missing directory).
func CountMdFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if e.IsDir() {
			count += CountMdFiles(full)
		} else if strings.HasSuffix(e.Name(), ".md") {
			count++
		}
	}
	return count
}

// DeriveStage mirrors lib/openspec.js's deriveStage.
func DeriveStage(hasTasks bool, tasksDone, tasksTotal int) string {
	if !hasTasks || tasksTotal == 0 {
		return "spec-in-progress"
	}
	if tasksDone == 0 {
		return "ready-for-plan"
	}
	if tasksDone >= tasksTotal {
		return "tasks-complete"
	}
	return "implementation-in-progress"
}

// fsFileExists reports whether path exists and is not a directory. A local,
// unexported duplicate of tools.fileExists (internal/tools/version.go) —
// that helper is unexported and unreachable from this package; duplicating
// a two-line os.Stat check mirrors the repo's existing tolerance for this
// exact kind of small cross-package duplication (see also
// tools/jira.go/internal/links' independent ~/.sdlc-cache/jira path copies).
func fsFileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// fsNonEmptyLines splits s on newlines and returns the non-blank,
// trimmed lines. A local, unexported duplicate of tools.nonEmptyLines
// (internal/tools/commit.go) for the same reason as fsFileExists above.
func fsNonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// taskCheckboxRe matches a Markdown task checkbox line. It is the counting
// half of lib/openspec.js's parseTasks regex, duplicated here (rather than
// moved) because parseTasks itself carries unrelated machinery (TaskEntry,
// computeRef, extractInlineRef) that AnalyzeChange does not need — only the
// done/total counts. Byte-identical pattern to tools/plan.go's taskLineRe.
var taskCheckboxRe = regexp.MustCompile(`^([ \t]*)- \[([ xX])\] (.*)$`)

// countTasks scans content for Markdown task checkboxes and returns the
// number done vs. total, mirroring the counting half of lib/openspec.js's
// parseTasks (used by AnalyzeChange for tasksDone/tasksTotal).
func countTasks(content string) (done, total int) {
	for _, raw := range strings.Split(content, "\n") {
		m := taskCheckboxRe.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		total++
		if strings.ToLower(m[2]) == "x" {
			done++
		}
	}
	return done, total
}

// AnalyzeChange mirrors lib/openspec.js's analyzeChange.
func AnalyzeChange(changeDir, name string) OpenspecChangeInfo {
	hasProposal := fsFileExists(filepath.Join(changeDir, "proposal.md"))
	hasDesign := fsFileExists(filepath.Join(changeDir, "design.md"))
	tasksPath := filepath.Join(changeDir, "tasks.md")
	hasTasks := fsFileExists(tasksPath)

	deltaSpecCount := CountMdFiles(filepath.Join(changeDir, "specs"))

	tasksDone, tasksTotal := 0, 0
	if hasTasks {
		if content, err := os.ReadFile(tasksPath); err == nil {
			tasksDone, tasksTotal = countTasks(string(content))
		}
	}

	stage := DeriveStage(hasTasks, tasksDone, tasksTotal)
	return OpenspecChangeInfo{
		Name:           name,
		Stage:          &stage,
		DeltaSpecCount: deltaSpecCount,
		HasProposal:    hasProposal,
		HasDesign:      hasDesign,
		HasTasks:       hasTasks,
		TasksDone:      tasksDone,
		TasksTotal:     tasksTotal,
	}
}

// slugBoundaryMatch reports whether nameSlug appears in branchSlug bounded by
// '/', '-', or a string edge, mirroring DetectActiveChanges's slugRe.
func slugBoundaryMatch(branchSlug, nameSlug string) bool {
	pattern := `(^|[/-])` + regexp.QuoteMeta(nameSlug) + `($|[/-])`
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(branchSlug)
}

// DetectActiveChanges mirrors lib/openspec.js's detectActiveChanges: the
// filesystem-walking OpenSpec status check (see package doc comment above
// for how this differs from Detect).
func DetectActiveChanges(contentRoot string) OpenspecInfo {
	configPath := filepath.Join(contentRoot, "openspec", "config.yaml")
	if !fsFileExists(configPath) {
		return OpenspecInfo{ActiveChanges: []OpenspecChangeInfo{}}
	}

	specsCount := CountMdFiles(filepath.Join(contentRoot, "openspec", "specs"))

	changesDir := filepath.Join(contentRoot, "openspec", "changes")
	activeChanges := []OpenspecChangeInfo{}
	if entries, err := os.ReadDir(changesDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() || e.Name() == "archive" {
				continue
			}
			changeDir := filepath.Join(changesDir, e.Name())
			if !fsFileExists(filepath.Join(changeDir, "proposal.md")) {
				continue
			}
			activeChanges = append(activeChanges, AnalyzeChange(changeDir, e.Name()))
		}
	}

	var branchMatch *string
	branch, branchErr := gitx.CurrentBranch(contentRoot)
	if branchErr == nil && branch != "" && len(activeChanges) > 0 {
		branchSlug := BranchPrefixRe.ReplaceAllString(strings.ToLower(branch), "")
		for _, change := range activeChanges {
			nameSlug := strings.ToLower(change.Name)
			if branchSlug == nameSlug || slugBoundaryMatch(branchSlug, nameSlug) {
				m := change.Name
				branchMatch = &m
				break
			}
		}

		if branchMatch == nil {
			if base, err := gitx.DefaultBranch(contentRoot); err == nil && branch != base {
				if diffOut, err := gitx.Diff(contentRoot, gitx.DiffOpts{Base: base, NameOnly: true}); err == nil {
					hits := map[string]bool{}
					for _, f := range fsNonEmptyLines(diffOut) {
						for _, change := range activeChanges {
							if strings.HasPrefix(f, "openspec/changes/"+change.Name+"/") {
								hits[change.Name] = true
							}
						}
					}
					if len(hits) == 1 {
						for name := range hits {
							n := name
							branchMatch = &n
						}
					}
				}
			}
		}
	}

	return OpenspecInfo{
		Present:       true,
		SpecsCount:    specsCount,
		ActiveChanges: activeChanges,
		BranchMatch:   branchMatch,
	}
}
