package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/openspec"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// Action: openspec-check
// ---------------------------------------------------------------------------

const (
	// shipOpenspecChangeMaxLen is the longest change name, in bytes, that the
	// OpenSpec actions accept.
	shipOpenspecChangeMaxLen = 128
	// shipOpenspecValidateMaxBytes is the most validate output, in bytes, that
	// openspec-check returns.
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
// HEAD when an OpenSpec change is named.
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
	ValidateOutput string            `json:"validateOutput" jsonschema_description:"Output of openspec validate --strict, cut at 16384 bytes; empty when validate did not run."`
	Overlaps       []shipSpecOverlap `json:"overlaps" jsonschema_description:"Specs of this change that archived changes edited since openspecBaseHead; empty when none."`
	Warnings       []string          `json:"warnings" jsonschema_description:"Problems that did not stop the call."`
	Next           string            `json:"next" jsonschema_description:"What the caller does next."`
}

// shipOpenspecResolveChange returns the OpenSpec change that an OpenSpec
// action of the ship state acts on. action is the action name for the error
// text. The change is flags.openspecChange of data; detail.change names a
// change only when flags.openspecChange is empty. Each error is a
// DomainError. The change name is checked for length and for ValidChangeName
// before the caller builds a path from it.
func shipOpenspecResolveChange(action string, data, detail map[string]any) (string, error) {
	flags, _ := data["flags"].(map[string]any)
	named, _ := flags["openspecChange"].(string)
	given := detailStr(detail, "change")

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

	if len(change) > shipOpenspecChangeMaxLen {
		return "", &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s: change name is %d bytes, limit %d bytes", action, len(change), shipOpenspecChangeMaxLen),
			Suggestion: fmt.Sprintf("Pass a change name of %d bytes or fewer.", shipOpenspecChangeMaxLen),
		}
	}
	if !openspec.ValidChangeName(change) {
		return "", &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s: invalid change name %q", action, change),
			Suggestion: "Pass a name of lower-case letters, digits and single hyphens.",
		}
	}
	return change, nil
}

// shipOpenspecIsDir reports whether path is a folder. A file, a missing path
// and a link loop all count as no folder, as openspec.AnalyzeChange reads
// them.
func shipOpenspecIsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// shipOpenspecIsFile reports whether path is a file that is not a folder.
func shipOpenspecIsFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// shipOpenspecArchived reports whether openspec/changes/archive of workDir
// holds a folder <YYYY-MM-DD>-<change>. The date prefix keeps the change "fix"
// from matching the archived change "add-fix".
func shipOpenspecArchived(workDir, change string) bool {
	re := regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}-` + regexp.QuoteMeta(change) + `$`)
	entries, err := os.ReadDir(filepath.Join(workDir, "openspec", "changes", "archive"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if re.MatchString(e.Name()) && shipOpenspecIsDir(filepath.Join(workDir, "openspec", "changes", "archive", e.Name())) {
			return true
		}
	}
	return false
}

// shipOpenspecMissingArtifacts lists the required artifacts that the change
// folder lacks: proposal.md, and at least one specs/<capability>/spec.md.
func shipOpenspecMissingArtifacts(changeDir string) []string {
	missing := []string{}
	if !shipOpenspecIsFile(filepath.Join(changeDir, "proposal.md")) {
		missing = append(missing, "proposal.md")
	}
	hasSpec := false
	if entries, err := os.ReadDir(filepath.Join(changeDir, "specs")); err == nil {
		for _, e := range entries {
			if shipOpenspecIsFile(filepath.Join(changeDir, "specs", e.Name(), "spec.md")) {
				hasSpec = true
				break
			}
		}
	}
	if !hasSpec {
		missing = append(missing, "specs/*/spec.md")
	}
	return missing
}

// shipOpenspecOwnCapabilities returns the names of the folders under
// specs/ of the change folder.
func shipOpenspecOwnCapabilities(changeDir string) map[string]bool {
	caps := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(changeDir, "specs"))
	if err != nil {
		return caps
	}
	for _, e := range entries {
		if shipOpenspecIsDir(filepath.Join(changeDir, "specs", e.Name())) {
			caps[e.Name()] = true
		}
	}
	return caps
}

// shipOpenspecOverlaps lists the specs of change that an archive folder added
// since the commit openspecBaseHead of data. It asks git for the archive
// folders added in <base>..HEAD, and keeps each one that holds specs/<cap>/
// for a capability of the change. An absent or malformed openspecBaseHead,
// and a failed git call, give no overlaps and one warning; git never gets a
// value that is not a full commit sha.
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

	own := shipOpenspecOwnCapabilities(changeDir)
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

// shipOpenspecTruncate cuts s at shipOpenspecValidateMaxBytes bytes and ends
// the cut text with a marker. A text of exactly the limit is not cut.
func shipOpenspecTruncate(s string) string {
	if len(s) <= shipOpenspecValidateMaxBytes {
		return s
	}
	return s[:shipOpenspecValidateMaxBytes] + fmt.Sprintf("[validate output cut at %d bytes]", shipOpenspecValidateMaxBytes)
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

// shipOpenspecInfraError wraps a failed openspec call in an InfraError.
func shipOpenspecInfraError(err error) error {
	suggestion := "Check the openspec CLI with openspec --version, then call openspec-check again."
	if errors.Is(err, openspec.ErrCLINotFound) {
		suggestion = "Install the openspec CLI, then call openspec-check again."
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
// InfraError and leaves the step in_progress.
func shipStateOpenspecCheck(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	step := in.Step
	if step != "verify-openspec" && step != "archive-openspec" {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("openspec-check: step %q has no OpenSpec check", step),
			Suggestion: "Call openspec-check with step verify-openspec or archive-openspec.",
		}
	}
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
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
	change, err := shipOpenspecResolveChange("openspec-check", st.Data, in.Detail)
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
	changeDir := filepath.Join(workDir, "openspec", "changes", change)

	var failMsg string // set by a failing outcome: the message of the fail row
	switch {
	case !shipOpenspecIsDir(changeDir) && shipOpenspecArchived(workDir, change):
		out.Outcome = shipOutcomeArchived
		out.Next = completeNext
		if step == "archive-openspec" {
			out.Next = "Call complete-step for archive-openspec with result already archived."
		}
	case !shipOpenspecIsDir(changeDir):
		out.Outcome = shipOutcomeMissingOnDisk
		failMsg = shipOpenspecMissingOnDiskMessage(change, shipLinkedPlanFile(st.Data))
		out.Next = failMsg
	default:
		out.Missing = shipOpenspecMissingArtifacts(changeDir)
		if len(out.Missing) > 0 {
			out.Outcome = shipOutcomeMissingArtifacts
			failMsg = fmt.Sprintf("OpenSpec change %q is missing %s", change, strings.Join(out.Missing, ", "))
			if planFile := shipLinkedPlanFile(st.Data); planFile != "" {
				out.Next = fmt.Sprintf("Run /sdlc:plan on %s to author the missing artifacts, then run /sdlc:ship --resume.", planFile)
			} else {
				out.Next = fmt.Sprintf("Author the missing artifacts in openspec/changes/%s/, then run /sdlc:ship --resume.", change)
			}
			break
		}
		output, exit, vErr := openspec.ValidateStrict(workDir, change)
		if vErr != nil {
			return nil, shipOpenspecInfraError(vErr)
		}
		out.ValidateOutput = shipOpenspecTruncate(output)
		if exit == 0 {
			out.Outcome = shipOutcomeOK
			out.Next = completeNext
			if step == "archive-openspec" {
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
