package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/openspec"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/shipmeta"
)

// ---------------------------------------------------------------------------
// Action: openspec-check
// ---------------------------------------------------------------------------

const (
	// shipOpenspecChangeMaxLen is the longest change name, in bytes, that the
	// OpenSpec actions accept.
	shipOpenspecChangeMaxLen = 128
	// shipOpenspecValidateMaxBytes is the most validate output, in bytes, that
	// openspec-check returns. The jsonschema_description of
	// shipOpenspecCheckOut.ValidateOutput names this value; a test ties the
	// two together.
	shipOpenspecValidateMaxBytes = 16 << 10
	// shipOpenspecDiffCapBytes is the output cap of the git diff call. The
	// seam shipCommitCheckGit uses the execx default.
	shipOpenspecDiffCapBytes = 52428800
)

// Values of shipOpenspecCheckOut.Outcome.
const (
	shipOutcomeOK               = "ok"
	shipOutcomeArchived         = "archived"
	shipOutcomeMissingOnDisk    = "missing-on-disk"
	shipOutcomeMissingArtifacts = "missing-artifacts"
	shipOutcomeInvalid          = "invalid"
)

// shipOpenspecBaseHeadKey is the ship state key that ship_prepare fills with
// HEAD when an OpenSpec change is named. The key is absent on a dry run and
// when git rev-parse HEAD fails.
const shipOpenspecBaseHeadKey = "openspecBaseHead"

// shipOpenspecShaRe matches a full git object name, SHA-1 or SHA-256.
var shipOpenspecShaRe = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// shipSpecOverlap names one capability spec that an archived change edited
// after the run started.
type shipSpecOverlap struct {
	Spec   string `json:"spec" jsonschema_description:"Path of the main spec, openspec/specs/<capability>/spec.md."`
	Change string `json:"change" jsonschema_description:"Name of the archive folder (<YYYY-MM-DD>-<change>) of the archived change that edited the spec."`
}

// shipOpenspecCheckOut is the output of the openspec-check action.
type shipOpenspecCheckOut struct {
	Change         string            `json:"change" jsonschema_description:"Name of the OpenSpec change that was checked."`
	Outcome        string            `json:"outcome" jsonschema_description:"ok, archived, missing-on-disk, missing-artifacts, or invalid."`
	Missing        []string          `json:"missing" jsonschema_description:"Artifacts the change lacks; empty when none."`
	ValidateOutput string            `json:"validateOutput" jsonschema_description:"Output of openspec validate --strict, cut at 16384 bytes (at a character boundary, with a marker line and a warning); empty when validate did not run."`
	Overlaps       []shipSpecOverlap `json:"overlaps" jsonschema_description:"Filled only for outcome invalid: specs of this change that archived changes edited since openspecBaseHead. Empty for every other outcome, and when there is none."`
	Warnings       []string          `json:"warnings" jsonschema_description:"Problems that did not stop the call."`
	Next           string            `json:"next" jsonschema_description:"What the caller does next."`
}

// shipOpenspecCheckChangeName returns a DomainError when change is longer
// than shipOpenspecChangeMaxLen or is not a valid change name. action
// prefixes the message. A nil result means a path may be built from change.
// It is the one name rule of the ship state OpenSpec code.
func shipOpenspecCheckChangeName(action, change string) error {
	if len(change) > shipOpenspecChangeMaxLen {
		return &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s: change name is %d bytes, limit %d bytes", action, len(change), shipOpenspecChangeMaxLen),
			Suggestion: fmt.Sprintf("Pass a change name of %d bytes or fewer.", shipOpenspecChangeMaxLen),
		}
	}
	if !openspec.ValidChangeName(change) {
		return &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s: invalid change name %q", action, change),
			Suggestion: "Pass a name of lower-case letters, digits and single hyphens.",
		}
	}
	return nil
}

// shipOpenspecChangeDir returns the folder openspec/changes/<change> under
// workDir.
func shipOpenspecChangeDir(workDir, change string) string {
	return filepath.Join(workDir, "openspec", "changes", change)
}

// shipOpenspecChangeRel returns "openspec/changes/<change>/", the form of the
// change folder in messages.
func shipOpenspecChangeRel(change string) string {
	return "openspec/changes/" + change + "/"
}

// shipOpenspecReadBranch reads detail.branch for the OpenSpec actions. A
// value that is not a string is a DomainError.
func shipOpenspecReadBranch(action string, detail map[string]any) (string, error) {
	return shipDetailString(detail, action, "branch", "Pass the branch name, or omit it for the current branch.")
}

// shipOpenspecResolveChange returns the OpenSpec change that an OpenSpec
// action of the ship state acts on. action is the action name for the error
// text. The change is flags.openspecChange of data; detail.change names a
// change only when flags.openspecChange is empty. Each error is a
// DomainError: a detail.change that is not a string included. The change
// name passes shipOpenspecCheckChangeName before the caller builds a path
// from it.
func shipOpenspecResolveChange(action string, data, detail map[string]any) (string, error) {
	flags, _ := data["flags"].(map[string]any)
	named, _ := flags["openspecChange"].(string)
	given, err := shipDetailString(detail, action, "change", "Pass the OpenSpec change name.")
	if err != nil {
		return "", err
	}

	change := named
	switch {
	case named == "" && given == "":
		return "", &mcpserver.DomainError{
			Msg:        action + ": no OpenSpec change named",
			Suggestion: `Call skip with reason "no active OpenSpec change".`,
		}
	case named == "":
		change = given
	case given != "" && given != named:
		return "", &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s: detail.change %q differs from the named change %q", action, given, named),
			Suggestion: fmt.Sprintf("Omit detail.change, or pass %q.", named),
		}
	}

	if err := shipOpenspecCheckChangeName(action, change); err != nil {
		return "", err
	}
	return change, nil
}

// shipOpenspecStatFunc is the stat call of the OpenSpec probes. Tests replace
// it to force a stat error that file permissions would cause.
var shipOpenspecStatFunc = os.Stat

// shipOpenspecStat stats path. A missing path, a path under a file (ENOTDIR)
// and a link loop (ELOOP) return (nil, nil): openspec.AnalyzeChange reads them
// as absent, and these probes agree with it. Any other error (permission
// denied, an I/O fault) is returned, so the caller never reads it as absent.
func shipOpenspecStat(path string) (fs.FileInfo, error) {
	info, err := shipOpenspecStatFunc(path)
	switch {
	case err == nil:
		return info, nil
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR), errors.Is(err, syscall.ELOOP):
		return nil, nil
	}
	return nil, err
}

// shipOpenspecIsDir reports whether path is a folder. See shipOpenspecStat
// for the errors that count as no folder.
func shipOpenspecIsDir(path string) (bool, error) {
	info, err := shipOpenspecStat(path)
	return info != nil && info.IsDir(), err
}

// shipOpenspecIsFile reports whether path is a file that is not a folder.
func shipOpenspecIsFile(path string) (bool, error) {
	info, err := shipOpenspecStat(path)
	return info != nil && !info.IsDir(), err
}

// shipOpenspecReadDirFunc is the folder listing of the OpenSpec probes. Tests
// replace it to force a read error that file permissions would cause.
var shipOpenspecReadDirFunc = os.ReadDir

// shipOpenspecReadDir lists path. A missing folder, and a path that is a
// file or lies under a file (ENOTDIR), list nothing; any other error is
// returned.
func shipOpenspecReadDir(path string) ([]os.DirEntry, error) {
	entries, err := shipOpenspecReadDirFunc(path)
	if err != nil && (errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)) {
		return nil, nil
	}
	return entries, err
}

// shipOpenspecProbeError wraps a failed read of the change files in an
// InfraError. The error text of os names the path.
func shipOpenspecProbeError(action string, err error) error {
	return &mcpserver.InfraError{
		Msg:        action + ": " + err.Error(),
		Suggestion: fmt.Sprintf("Fix read access to the path in the message, then call %s again.", action),
		Cause:      err,
	}
}

// shipOpenspecArchived reports whether openspec/changes/archive of workDir
// holds a folder <YYYY-MM-DD>-<change>. The date prefix keeps the change "fix"
// from matching the archived change "add-fix". A missing archive folder is
// no archive; any other read error is returned.
func shipOpenspecArchived(workDir, change string) (bool, error) {
	re := regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}-` + regexp.QuoteMeta(change) + `$`)
	archiveDir := filepath.Join(workDir, "openspec", "changes", "archive")
	entries, err := shipOpenspecReadDir(archiveDir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if !re.MatchString(e.Name()) {
			continue
		}
		isDir, err := shipOpenspecIsDir(filepath.Join(archiveDir, e.Name()))
		if err != nil {
			return false, err
		}
		if isDir {
			return true, nil
		}
	}
	return false, nil
}

// shipOpenspecMissingArtifacts lists the required artifacts that the change
// folder lacks: proposal.md, and at least one specs/<capability>/spec.md. A
// read error that does not mean "absent" is returned, so it is never
// reported as a missing artifact.
func shipOpenspecMissingArtifacts(changeDir string) ([]string, error) {
	missing := []string{}
	hasProposal, err := shipOpenspecIsFile(filepath.Join(changeDir, "proposal.md"))
	if err != nil {
		return nil, err
	}
	if !hasProposal {
		missing = append(missing, "proposal.md")
	}
	entries, err := shipOpenspecReadDir(filepath.Join(changeDir, "specs"))
	if err != nil {
		return nil, err
	}
	hasSpec := false
	for _, e := range entries {
		isFile, err := shipOpenspecIsFile(filepath.Join(changeDir, "specs", e.Name(), "spec.md"))
		if err != nil {
			return nil, err
		}
		if isFile {
			hasSpec = true
			break
		}
	}
	if !hasSpec {
		missing = append(missing, "specs/*/spec.md")
	}
	return missing, nil
}

// shipOpenspecOwnCapabilities returns the names of the folders under
// specs/ of the change folder. A read error that does not mean "absent" is
// returned.
func shipOpenspecOwnCapabilities(changeDir string) (map[string]bool, error) {
	caps := map[string]bool{}
	entries, err := shipOpenspecReadDir(filepath.Join(changeDir, "specs"))
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		isDir, err := shipOpenspecIsDir(filepath.Join(changeDir, "specs", e.Name()))
		if err != nil {
			return nil, err
		}
		if isDir {
			caps[e.Name()] = true
		}
	}
	return caps, nil
}

// shipOpenspecOverlaps lists the specs of change that an archive folder added
// since the commit openspecBaseHead of data. It asks git for the archive
// folders added in <base>..HEAD, and keeps each one that holds specs/<cap>/
// for a capability of the change. An absent or malformed openspecBaseHead, a
// failed git call and a failed read of the specs of the change give no
// overlaps and one warning; git never gets a value that is not a full commit
// sha.
func shipOpenspecOverlaps(workDir, changeDir string, data map[string]any) ([]shipSpecOverlap, []string) {
	none := []shipSpecOverlap{}
	raw, present := data[shipOpenspecBaseHeadKey]
	if !present || raw == nil || raw == "" {
		return none, []string{"overlap check skipped: openspecBaseHead is absent from the ship state"}
	}
	base, isText := raw.(string)
	if !isText || !shipOpenspecShaRe.MatchString(base) {
		return none, []string{"overlap check skipped: openspecBaseHead is not a full commit sha"}
	}

	diffArgs := []string{"diff", "--name-only", "--diff-filter=A", base + "..HEAD"}
	out, err := shipCommitCheckGit(workDir, diffArgs...)
	if err != nil {
		if errors.Is(err, execx.ErrOutputCap) {
			return none, []string{fmt.Sprintf("overlap check skipped: git diff output over the limit of %d bytes", shipOpenspecDiffCapBytes)}
		}
		return none, []string{fmt.Sprintf("overlap check skipped: git diff %s..HEAD: %s", base, err.Error())}
	}

	own, err := shipOpenspecOwnCapabilities(changeDir)
	if err != nil {
		return none, []string{"overlap check skipped: read the specs of the change: " + err.Error()}
	}
	if len(own) == 0 {
		return none, nil
	}
	const archivePrefix = "openspec/changes/archive/"
	seen := map[shipSpecOverlap]bool{}
	overlaps := none
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, archivePrefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(line, archivePrefix), "/")
		// <folder>/specs/<cap>/<file>
		if len(parts) < 4 || parts[1] != "specs" || !own[parts[2]] {
			continue
		}
		// The archive folder name keeps its date: 2026-11-01-<change>.
		o := shipSpecOverlap{Spec: "openspec/specs/" + parts[2] + "/spec.md", Change: parts[0]}
		if !seen[o] {
			seen[o] = true
			overlaps = append(overlaps, o)
		}
	}
	sort.Slice(overlaps, func(i, j int) bool {
		if overlaps[i].Spec != overlaps[j].Spec {
			return overlaps[i].Spec < overlaps[j].Spec
		}
		return overlaps[i].Change < overlaps[j].Change
	})
	return overlaps, nil
}

// shipOpenspecTruncate cuts s at shipOpenspecValidateMaxBytes bytes or
// fewer, at the start of a UTF-8 character, and ends the cut text with a
// marker on a line of its own. cut is true when s was cut. A text of exactly
// the limit is not cut.
func shipOpenspecTruncate(s string) (text string, cut bool) {
	if len(s) <= shipOpenspecValidateMaxBytes {
		return s, false
	}
	n := shipOpenspecValidateMaxBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + fmt.Sprintf("\n[validate output cut at %d of %d bytes]", n, len(s)), true
}

// shipOpenspecMissingOnDiskMessage is the failure message for a named change
// that is on disk neither as an active folder nor as an archive folder.
// planFile is the linked plan file; empty keeps the <plan-file> placeholder.
func shipOpenspecMissingOnDiskMessage(change, planFile string) string {
	if planFile == "" {
		planFile = "<plan-file>"
	}
	return fmt.Sprintf("OpenSpec change %q named by the plan is missing on disk. "+
		"If the plan has an **OpenSpec-Saved:** line, merge the OpenSpec PR (branch openspec/%s) first. "+
		"Then update this branch from the default branch and run /sdlc:ship --plan %s again. "+
		"Otherwise, run /sdlc:ship --plan %s again so ship_prepare materializes it, or remove the **Source:** line from the plan to ship without OpenSpec.",
		change, change, planFile, planFile)
}

// shipLinkedPlanFile returns linkedPlan.planFile of the ship state data, or
// "" when it is absent.
func shipLinkedPlanFile(data map[string]any) string {
	linked, _ := data[shipLinkedPlanKey].(map[string]any)
	planFile, _ := linked["planFile"].(string)
	return planFile
}

// shipOpenspecInfraError wraps a failed openspec validate call in an
// InfraError. A missing CLI and an output over the execx cap each get their
// own Suggestion; both need a fix before a new call can pass. Any other
// failure gets the generic CLI check.
func shipOpenspecInfraError(change string, err error) error {
	suggestion := "Check the openspec CLI with openspec --version, then call openspec-check again."
	switch {
	case errors.Is(err, openspec.ErrCLINotFound):
		suggestion = "Install the openspec CLI, then call openspec-check again."
	case errors.Is(err, execx.ErrOutputCap):
		suggestion = fmt.Sprintf("Run openspec validate %s --strict in a terminal to read the output, fix the change, then call openspec-check again.", change)
	}
	return &mcpserver.InfraError{
		Msg:        "openspec-check: " + err.Error(),
		Suggestion: suggestion,
		Cause:      err,
	}
}

// shipStateOpenspecCheck checks the OpenSpec change of the run for the step
// verify-openspec or archive-openspec, which must be in_progress. The change
// is flags.openspecChange (see shipOpenspecResolveChange). Files are read from
// workDir, the active worktree. The outcome is the first row of this list
// that holds:
//   - archived: no active folder, and an archive folder <date>-<change>
//   - missing-on-disk: no active folder and no archive folder
//   - missing-artifacts: no proposal.md, or no specs/*/spec.md
//   - invalid: openspec validate <change> --strict exits non-zero
//   - ok: none of the above
//
// The three failing outcomes write the fail row of the step, as the fail
// action does, so the caller cannot skip the record. A failed write returns an
// InfraError and leaves the step in_progress. A read error of the change files
// that does not mean "absent" is an InfraError and writes nothing.
func shipStateOpenspecCheck(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	const action = "openspec-check"
	step := in.Step
	if step != shipmeta.StepVerifyOpenspec && step != shipmeta.StepArchiveOpenspec {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("openspec-check: step %q has no OpenSpec check", step),
			Suggestion: "Call openspec-check with step verify-openspec or archive-openspec.",
		}
	}
	branch, err := shipOpenspecReadBranch(action, in.Detail)
	if err != nil {
		return nil, err
	}
	st, err := shipResolveAndFind(branch, workDir, root)
	if err != nil {
		return nil, err
	}
	entry := shipFindStepEntry(st.Data, step)
	if entry == nil {
		return nil, &mcpserver.DataError{
			Msg:        fmt.Sprintf("openspec-check: no %s step in the ship state", step),
			Suggestion: "Run ship_state read to list the steps. Call openspec-check only for a pipeline that has this step.",
		}
	}
	if status, _ := entry["status"].(string); status != "in_progress" {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("openspec-check: step %q is %s, not in_progress", step, status),
			Suggestion: fmt.Sprintf("Call begin-step for %s first, then call openspec-check again.", step),
		}
	}
	change, err := shipOpenspecResolveChange(action, st.Data, in.Detail)
	if err != nil {
		return nil, err
	}
	if _, has := in.Detail["sync"]; has {
		return nil, &mcpserver.DomainError{
			Msg:        "openspec-check: detail.sync applies only to openspec-tasks",
			Suggestion: "Remove detail.sync.",
		}
	}

	out := shipOpenspecCheckOut{
		Change:   change,
		Missing:  []string{},
		Overlaps: []shipSpecOverlap{},
		Warnings: []string{},
	}
	completeNext := "Call complete-step for " + step + "."
	changeDir := shipOpenspecChangeDir(workDir, change)

	active, err := shipOpenspecIsDir(changeDir)
	if err != nil {
		return nil, shipOpenspecProbeError(action, err)
	}
	archived := false
	if !active {
		if archived, err = shipOpenspecArchived(workDir, change); err != nil {
			return nil, shipOpenspecProbeError(action, err)
		}
	}

	var failMsg string // set by a failing outcome: the message of the fail row
	switch {
	case archived:
		out.Outcome = shipOutcomeArchived
		out.Next = completeNext
		if step == shipmeta.StepArchiveOpenspec {
			out.Next = "Call complete-step for archive-openspec with result already archived."
		}
	case !active:
		out.Outcome = shipOutcomeMissingOnDisk
		failMsg = shipOpenspecMissingOnDiskMessage(change, shipLinkedPlanFile(st.Data))
		out.Next = failMsg
	default:
		missing, err := shipOpenspecMissingArtifacts(changeDir)
		if err != nil {
			return nil, shipOpenspecProbeError(action, err)
		}
		out.Missing = missing
		if len(out.Missing) > 0 {
			out.Outcome = shipOutcomeMissingArtifacts
			failMsg = fmt.Sprintf("OpenSpec change %q is missing %s", change, strings.Join(out.Missing, ", "))
			if planFile := shipLinkedPlanFile(st.Data); planFile != "" {
				out.Next = fmt.Sprintf("Run /sdlc:plan on %s to author the missing artifacts, then run /sdlc:ship --resume.", planFile)
			} else {
				out.Next = fmt.Sprintf("Author the missing artifacts in %s, then run /sdlc:ship --resume.", shipOpenspecChangeRel(change))
			}
			break
		}
		output, exit, vErr := openspec.ValidateStrict(workDir, change)
		if vErr != nil {
			return nil, shipOpenspecInfraError(change, vErr)
		}
		var cut bool
		out.ValidateOutput, cut = shipOpenspecTruncate(output)
		if cut {
			out.Warnings = append(out.Warnings, fmt.Sprintf("validateOutput is cut: openspec validate printed %d bytes, limit %d bytes", len(output), shipOpenspecValidateMaxBytes))
		}
		if exit == 0 {
			out.Outcome = shipOutcomeOK
			out.Next = completeNext
			if step == shipmeta.StepArchiveOpenspec {
				out.Next = "Call openspec-tasks for archive-openspec."
			}
			break
		}
		out.Outcome = shipOutcomeInvalid
		failMsg = fmt.Sprintf("openspec validate %s --strict failed", change)
		var overlapWarnings []string
		out.Overlaps, overlapWarnings = shipOpenspecOverlaps(workDir, changeDir, st.Data)
		out.Warnings = append(out.Warnings, overlapWarnings...)
		if len(out.Overlaps) > 0 {
			out.Next = fmt.Sprintf("Rebase the deltas of %s on the new text of %s, then run openspec validate %s --strict.",
				change, strings.Join(shipOpenspecOverlapSpecs(out.Overlaps), ", "), change)
		} else {
			out.Next = "Fix the errors in validateOutput, then run /sdlc:ship --resume."
		}
	}

	if failMsg == "" {
		return out, nil
	}
	failedAt := now()
	firstFail := shipApplyFail(st.Data, entry, step, failMsg, true, failedAt)
	if err := shipStateWriteFunc(st); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        "openspec-check: write ship state: " + err.Error(),
			Suggestion: "Fix write access to " + shipOpenspecRunsDir() + ", then call openspec-check again.",
			Cause:      err,
		}
	}
	if firstFail {
		out.Warnings = append(out.Warnings, shipAppendFailHistory(root, st.Data, failedAt)...)
	}
	return out, nil
}

// shipOpenspecOverlapSpecs returns the distinct spec paths of overlaps, in
// order.
func shipOpenspecOverlapSpecs(overlaps []shipSpecOverlap) []string {
	var specs []string
	for _, o := range overlaps {
		if len(specs) == 0 || specs[len(specs)-1] != o.Spec {
			specs = append(specs, o.Spec)
		}
	}
	return specs
}

// shipOpenspecRunsDir is the folder of the ship state files, for error text.
func shipOpenspecRunsDir() string {
	return filepath.ToSlash(filepath.Join(paths.DataDir, "runs")) + "/"
}

// ---------------------------------------------------------------------------
// Action: openspec-tasks
// ---------------------------------------------------------------------------

// tasksFileMaxBytes is the largest tasks.md, in bytes, that openspec-tasks
// reads. The action writes the file, so it caps the read.
const tasksFileMaxBytes = 1 << 20

// Test seams of openspec-tasks.
var (
	openspecTasksRead = os.ReadFile
	// openspecTasksWrite replaces path through a temp file and a rename. It
	// keeps the mode of the file that it replaces. Only a missing file takes
	// the default mode 0644; any other stat error is returned.
	openspecTasksWrite = func(path string, data []byte) error {
		perm := os.FileMode(0o644)
		info, err := os.Stat(path)
		switch {
		case err == nil:
			perm = info.Mode().Perm()
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
		return fsx.AtomicWriteBytesPerm(path, data, perm)
	}
)

// shipOpenspecTasksOut is the output of the openspec-tasks action.
type shipOpenspecTasksOut struct {
	Change   string              `json:"change" jsonschema_description:"Name of the OpenSpec change whose tasks.md was counted."`
	Before   openspec.TaskCounts `json:"before" jsonschema_description:"Boxes of tasks.md before the call, outside code fences."`
	After    openspec.TaskCounts `json:"after" jsonschema_description:"Boxes of tasks.md after the call; equal to before when detail.sync is not true."`
	Changed  []openspec.TaskLine `json:"changed" jsonschema_description:"Each line that the sync changed, with its line number; empty when none."`
	Written  bool                `json:"written" jsonschema_description:"True when the call replaced tasks.md."`
	Warnings []string            `json:"warnings" jsonschema_description:"Facts behind a count of 0: tasks.md is absent, or tasks.md has no task box. Empty when tasks.md has a box."`
	Next     string              `json:"next" jsonschema_description:"What the caller does next."`
}

// Warnings of openspec-tasks for a count of 0.
const (
	shipOpenspecTasksAbsent = "tasks.md is absent: there is no box to count or sync"
	shipOpenspecTasksNoBox  = "tasks.md has no task box: there is nothing to sync"
)

// shipStateOpenspecTasks implements the openspec-tasks action. It counts the
// task boxes of openspec/changes/<change>/tasks.md outside code fences. With
// detail.sync true it marks each open box done and replaces the file through
// a temp file. Without detail.sync it writes nothing. A tasks.md that is
// absent or has no box gives counts of 0, one warning, and writes nothing.
//
// The read policy differs from openspec.AnalyzeChange on purpose: a tasks.md
// that is a symlink or another non-regular file is a DataError, because the
// temp-file rename would replace the link instead of the file it points to.
// A tasks.md over tasksFileMaxBytes is a DataError, because the action reads
// all of it into memory to rewrite it.
func shipStateOpenspecTasks(root, workDir string, in ShipStateIn) (any, error) {
	const action = "openspec-tasks"
	step := in.Step
	if step != shipmeta.StepArchiveOpenspec {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("openspec-tasks: step %q has no task sync", step),
			Suggestion: "Call openspec-tasks with step archive-openspec.",
		}
	}
	branch, err := shipOpenspecReadBranch(action, in.Detail)
	if err != nil {
		return nil, err
	}
	st, err := shipResolveAndFind(branch, workDir, root)
	if err != nil {
		return nil, err
	}
	entry := shipFindStepEntry(st.Data, step)
	if entry == nil {
		return nil, &mcpserver.DataError{
			Msg:        fmt.Sprintf("openspec-tasks: no %s step in the ship state", step),
			Suggestion: "Run ship_state read to list the steps. Call openspec-tasks only for a pipeline that has this step.",
		}
	}
	if status, _ := entry["status"].(string); status != "in_progress" {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("openspec-tasks: step %q is %s, not in_progress", step, status),
			Suggestion: fmt.Sprintf("Call begin-step for %s first, then call openspec-tasks again.", step),
		}
	}
	change, err := shipOpenspecResolveChange(action, st.Data, in.Detail)
	if err != nil {
		return nil, err
	}
	sync, err := shipDetailBool(in.Detail, action, "sync", "true marks each open box done; omit it to count only.")
	if err != nil {
		return nil, err
	}

	out := shipOpenspecTasksOut{Change: change, Changed: []openspec.TaskLine{}, Warnings: []string{}}
	tasksPath := filepath.Join(shipOpenspecChangeDir(workDir, change), "tasks.md")
	display := shipOpenspecChangeRel(change) + "tasks.md"
	infra := func(err error) error {
		return &mcpserver.InfraError{
			Msg:        "openspec-tasks: " + err.Error(),
			Suggestion: fmt.Sprintf("Fix access to %s, then call openspec-tasks again.", display),
			Cause:      err,
		}
	}

	info, err := os.Lstat(tasksPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		out.Warnings = append(out.Warnings, shipOpenspecTasksAbsent)
		out.Next = shipOpenspecTasksNext(change, sync, false)
		return out, nil
	case err != nil:
		return nil, infra(err)
	case !info.Mode().IsRegular():
		return nil, &mcpserver.DataError{
			Msg:        "tasks.md is not a regular file",
			Suggestion: fmt.Sprintf("Replace %s with a regular file.", display),
		}
	case info.Size() > tasksFileMaxBytes:
		return nil, shipOpenspecTasksTooBig(info.Size())
	}
	raw, err := openspecTasksRead(tasksPath)
	if err != nil {
		return nil, infra(err)
	}
	if len(raw) > tasksFileMaxBytes {
		return nil, shipOpenspecTasksTooBig(int64(len(raw)))
	}

	synced, changed, before, after, err := openspec.SyncTasks(string(raw))
	var fenceErr *openspec.OpenFenceError
	if errors.As(err, &fenceErr) {
		return nil, &mcpserver.DataError{
			Msg:        fenceErr.Error(),
			Suggestion: fmt.Sprintf("Close the code fence at line %d of tasks.md, then call openspec-tasks again.", fenceErr.Line),
			Cause:      err,
		}
	}
	if err != nil {
		// SyncTasks reads only the text of tasks.md, so its error is about
		// what the file holds, not about file access.
		return nil, &mcpserver.DataError{
			Msg:        "openspec-tasks: parse tasks.md: " + err.Error(),
			Suggestion: fmt.Sprintf("Fix the text of %s at the place the message names, then call openspec-tasks again.", display),
			Cause:      err,
		}
	}
	out.Before = before
	out.After = before
	if before.Total == 0 {
		out.Warnings = append(out.Warnings, shipOpenspecTasksNoBox)
	}
	if sync && len(changed) > 0 {
		if err := openspecTasksWrite(tasksPath, []byte(synced)); err != nil {
			return nil, infra(err)
		}
		out.After = after
		out.Changed = changed
		out.Written = true
	}
	out.Next = shipOpenspecTasksNext(change, sync, before.Total > before.Done)
	return out, nil
}

// shipOpenspecTasksTooBig is the DataError of a tasks.md over the size cap.
func shipOpenspecTasksTooBig(size int64) error {
	return &mcpserver.DataError{
		Msg:        fmt.Sprintf("tasks.md is %d bytes, limit %d bytes", size, tasksFileMaxBytes),
		Suggestion: fmt.Sprintf("Split tasks.md below %d bytes, then call openspec-tasks again.", tasksFileMaxBytes),
	}
}

// shipOpenspecTasksNext is the next text of openspec-tasks. The archive
// consent gate stays in the skill, so a count without sync asks for consent.
func shipOpenspecTasksNext(change string, sync, hasOpen bool) string {
	const consent = "Ask for archive consent (skip the question under flags.auto). On consent, "
	switch {
	case sync:
		return "Run openspec archive " + change + "."
	case hasOpen:
		return consent + "call openspec-tasks with detail.sync true, then run openspec archive " + change + "."
	default:
		return consent + "run openspec archive " + change + "."
	}
}

// ---------------------------------------------------------------------------
// Run-end OpenSpec report of cleanup-pipeline
// ---------------------------------------------------------------------------

// shipCleanupOpenspecReport builds the run-end OpenSpec report for
// cleanup-pipeline. The change is flags.openspecChange of data. With no named
// change it returns (nil, ""). Otherwise it returns the report for the
// "openspec" output key, or a warning line when the report is skipped: an
// invalid change name (no stat runs) or a stat error that shipOpenspecStat
// does not read as absent. The stat goes through shipOpenspecStatFunc, the
// same seam and the same error split as the openspec-check probes. The change
// counts as still active when openspec/changes/<change>/ is a directory under
// workDir. When it is active, the function appends one decisions[] entry for
// step archive-openspec to data. The caller writes data.
func shipCleanupOpenspecReport(workDir string, data map[string]any, now time.Time) (map[string]any, string) {
	flags, _ := data["flags"].(map[string]any)
	change, _ := flags["openspecChange"].(string)
	if change == "" {
		return nil, ""
	}
	if shipOpenspecCheckChangeName("cleanup-pipeline", change) != nil {
		return nil, fmt.Sprintf("OpenSpec report skipped: invalid change name %q", change)
	}
	rel := shipOpenspecChangeRel(change)
	stillActive, err := shipOpenspecIsDir(shipOpenspecChangeDir(workDir, change))
	if err != nil {
		return nil, fmt.Sprintf("OpenSpec report skipped: stat %s: %s", rel, err.Error())
	}
	if stillActive {
		decisions, _ := data["decisions"].([]any)
		data["decisions"] = append(decisions, map[string]any{
			"step":     shipmeta.StepArchiveOpenspec,
			"decision": fmt.Sprintf("OpenSpec change %s is still active in %s. Archive it with openspec archive %s.", change, rel, change),
			"at":       now.UTC().Format(time.RFC3339),
		})
	}
	return map[string]any{"change": change, "stillActive": stillActive, "path": rel}, ""
}
