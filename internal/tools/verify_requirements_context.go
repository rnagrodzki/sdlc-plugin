package tools

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/openspec"
)

// verifyRequirementsContextAction is the plan_support action that returns the
// fixed facts of one requirements check. Each error message starts with it.
const verifyRequirementsContextAction = "verify_requirements_context"

// verifyValueMaxBytes is the size limit of verifyValue.
const verifyValueMaxBytes = 4096

// verifyRunAgain ends each verify_requirements_context Suggestion that a new
// run can fix (preplanRetry).
const verifyRunAgain = ", then run /sdlc:verify-requirements again."

// verifyNoFileSuggestion is the Suggestion of a topic with no topic file. It
// names no create step: the action never creates a topic file.
const verifyNoFileSuggestion = "Check the topic name against the files in .sdlc-v2/preplan/. For a file outside that folder, run /sdlc:verify-requirements --file <path>."

// The values of verifyKind.
const (
	verifyKindTopic = "topic"
	verifyKindFile  = "file"
	verifyKindJira  = "jira"
	verifyKindIssue = "issue"
)

// verifyJiraKeyRe matches a Jira key: the project key, a hyphen and the issue
// number with no leading zero.
var verifyJiraKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,31}-[1-9][0-9]{0,9}$`)

// verifyAnalysisChecks ends each Next: the checks that the skill runs itself.
const verifyAnalysisChecks = "the guardrail, meaning and OpenSpec checks."

// verifyRequirementsContextDescription returns the verify_requirements_context
// line of the plan_support tool description. The numbers come from the
// constants that the code uses; TestPreplanToolDescription pins the text.
func verifyRequirementsContextDescription() string {
	return "- verify_requirements_context: Return the fixed facts for one requirements check, and write nothing. " +
		"Requires verifyKind (topic, file, jira or issue) and verifyValue (at most " + strconv.Itoa(verifyValueMaxBytes) + " bytes). " +
		"topic, section, content and topicStatus return DomainError; other fields are ignored. " +
		"topic: finds the topic file as preplan_check does, never creates one (a missing file is a DomainError that points to no create step), " +
		"and returns structureFindings (rules " + strings.Join([]string{preplanRuleOpenFence, preplanRuleEmptySection, preplanRuleDecisionsTable, preplanRuleCutNotLive, preplanRuleMovedNoAsk, preplanRuleFlowNoProposal}, ", ") + ") and openQuestions. " +
		"file: checks that the path (a relative path starts at the active worktree) is a regular file of at most " + dashboardDeleteReadMaxText + ". " +
		"jira: checks the key format and returns jiraProject. " +
		"issue: verifyValue is 1 to 10 digits with no leading zero (stricter than the fromIssue input of prepare_orchestrator, which allows leading zeros); " +
		"runs gh issue view <n> --json title,body in the active worktree and returns issueTitle and issueBody as data; " +
		"a blank title with a blank body returns DomainError, and a blank body alone is accepted (fromIssue refuses a blank body). " +
		"summary holds the warnings: an open code fence (topic) and a hint when a file is a preplan topic file (file). " +
		"Every kind returns the plan guardrails with guardrailsStatus, and the OpenSpec main specs and active changes of the active worktree, each with its requirement headings; " +
		"a spec that cannot be read is listed in openspecWarnings. " +
		"A bad input, a missing file, a link or a file that is not regular, or an issue that does not exist returns DomainError; " +
		"two matching topic files or a file over " + dashboardDeleteReadMaxText + " returns DataError; " +
		"a list or read failure, a missing or unauthenticated gh, or bad gh output returns InfraError. Every error has a Suggestion."
}

// verifyFieldError returns a DomainError, so that nothing runs, when an
// action other than verify_requirements_context gets verifyKind or
// verifyValue, or when verify_requirements_context gets topic. It returns nil
// for every other call. planSupportCore calls it after preplanWriteFieldError,
// so section, content and topicStatus are refused first.
func verifyFieldError(in PlanSupportIn) error {
	if in.Action == verifyRequirementsContextAction {
		if in.Topic != "" {
			return &mcpserver.DomainError{
				Msg:        verifyRequirementsContextAction + ": topic is not used by this action",
				Suggestion: "Put the topic name in verifyValue and set verifyKind to topic.",
			}
		}
		return nil
	}
	for _, f := range []struct {
		name string
		sent bool
	}{
		{"verifyKind", in.VerifyKind != ""},
		{"verifyValue", in.VerifyValue != ""},
	} {
		if f.sent {
			return &mcpserver.DomainError{
				Msg:        fmt.Sprintf("%s: %s is a %s field", in.Action, f.name, verifyRequirementsContextAction),
				Suggestion: "Remove verifyKind and verifyValue, or use action " + verifyRequirementsContextAction + ".",
			}
		}
	}
	return nil
}

// verifyInputError checks verifyKind and verifyValue, in this order: the kind
// is one of the four values, the value is not blank, and the value is at most
// verifyValueMaxBytes bytes. Each error is a DomainError.
func verifyInputError(in PlanSupportIn) error {
	const action = verifyRequirementsContextAction
	switch in.VerifyKind {
	case verifyKindTopic, verifyKindFile, verifyKindJira, verifyKindIssue:
	default:
		return &mcpserver.DomainError{
			Msg:        action + ": verifyKind must be one of topic, file, jira, issue",
			Suggestion: "Set verifyKind to the kind of the input: topic, file, jira or issue.",
		}
	}
	if strings.TrimSpace(in.VerifyValue) == "" {
		return &mcpserver.DomainError{
			Msg:        action + ": verifyValue is required",
			Suggestion: "Set verifyValue to the topic name, the file path, the Jira key or the issue number.",
		}
	}
	if n := len(in.VerifyValue); n > verifyValueMaxBytes {
		return &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s: verifyValue is %d bytes; the limit is %d bytes", action, n, verifyValueMaxBytes),
			Suggestion: "Pass a shorter value: a topic name, a path, a Jira key or an issue number.",
		}
	}
	return nil
}

// planVerifyRequirementsContext returns the fixed facts of one requirements
// check. It reads only: it never calls preplanWriteBytes,
// setGuardrailCheckLine or an fsx.AtomicWrite function, and it never creates
// a topic file. The steps run in this order:
//
//  1. verifyInputError: the kind and the value (DomainError).
//  2. The branch of the kind reads its input (see verifyTopic, verifyJira).
//  3. loadGuardrails(mainRoot) gives the guardrails and guardrailsStatus. A
//     bad config does not fail the call: guardrailsStatus reads
//     "unavailable: <warning>", and summary names the warning.
//  4. verifyOpenspec(contentRoot) gives the OpenSpec lists of the active
//     worktree. A read failure is a warning, never an error.
func planVerifyRequirementsContext(mainRoot, contentRoot string, in PlanSupportIn) (PlanSupportOut, error) {
	if err := verifyInputError(in); err != nil {
		return PlanSupportOut{}, err
	}
	var (
		out PlanSupportOut
		err error
	)
	switch in.VerifyKind {
	case verifyKindTopic:
		out, err = verifyTopic(mainRoot, in.VerifyValue)
	case verifyKindJira:
		out, err = verifyJira(in.VerifyValue)
	case verifyKindFile:
		out, err = verifyFile(contentRoot, in.VerifyValue)
	default: // verifyKindIssue
		err = &mcpserver.DomainError{
			Msg:        verifyRequirementsContextAction + ": verifyKind issue is not built yet",
			Suggestion: "Use verifyKind topic, file or jira.",
		}
	}
	if err != nil {
		return PlanSupportOut{}, err
	}

	guardrails, warning := loadGuardrails(mainRoot)
	out.VerifyKind = in.VerifyKind
	out.Guardrails = guardrails
	out.GuardrailsStatus = "loaded: " + strconv.Itoa(len(guardrails))
	if warning != "" {
		text := preplanWarningText(warning)
		out.GuardrailsStatus = "unavailable: " + text
		out.Summary += " Guardrails unavailable: " + text + "."
	}

	status, mainSpecs, changes, warnings := verifyOpenspec(contentRoot)
	out.OpenspecStatus = status
	out.OpenspecMainSpecs = &mainSpecs
	out.OpenspecActiveChanges = &changes
	out.OpenspecWarnings = &warnings
	return out, nil
}

// verifyTopic finds and reads the topic file of topic under mainRoot, as
// preplan_check does (preplanReadTopicFile), and returns its structure
// findings and its open questions. A missing topic file gives a DomainError
// with verifyNoFileSuggestion: the Suggestions of preplan_check name a
// create step, which this action does not have. Every other error of the
// find step stays as it is.
func verifyTopic(mainRoot, topic string) (PlanSupportOut, error) {
	found, err := preplanReadTopicFile(verifyRequirementsContextAction, mainRoot, topic)
	if err != nil {
		var de *mcpserver.DomainError
		if errors.As(err, &de) && (de.Suggestion == preplanNoFileSuggestion || de.Suggestion == preplanGoneSuggestion) {
			return PlanSupportOut{}, &mcpserver.DomainError{Msg: de.Msg, Suggestion: verifyNoFileSuggestion, Cause: de.Cause}
		}
		return PlanSupportOut{}, err
	}
	findings := preplanStructureFindings(found.data)
	questions := verifyOpenQuestions(found.data)
	summary := fmt.Sprintf("Read %s: %d structure finding(s), %d open question(s).", found.name, len(findings), len(questions))
	if _, openFence := preplanSectionSpans(found.data); openFence != "" {
		summary += " " + preplanOpenFenceWarning(openFence)
	}
	return PlanSupportOut{
		Summary:           summary,
		Next:              "Read verifySource. Copy structureFindings and openQuestions into the report, then run " + verifyAnalysisChecks,
		VerifySource:      found.file,
		StructureChecks:   "ran",
		StructureFindings: &findings,
		OpenQuestions:     &questions,
	}, nil
}

// verifyOpenQuestions returns the text of each "- " bullet of the
// ## Open questions section of data, in file order. It is empty (never nil)
// when the section is absent, hidden by an open fence, or has no bullet.
func verifyOpenQuestions(data []byte) []string {
	questions := []string{}
	spans, _ := preplanSectionSpans(data)
	i := preplanFindSpan(spans, preplanOpenQuestionsSection)
	if i < 0 {
		return questions
	}
	for _, line := range preplanBodyLines(string(data[spans[i].bodyStart:spans[i].bodyEnd])) {
		if text, ok := strings.CutPrefix(line, "- "); ok {
			questions = append(questions, strings.TrimSpace(text))
		}
	}
	return questions
}

// verifyJira checks the format of a Jira key and returns the key and its
// project. It reads nothing: the skill reads the ticket.
func verifyJira(value string) (PlanSupportOut, error) {
	key := strings.TrimSpace(value)
	if !verifyJiraKeyRe.MatchString(key) {
		return PlanSupportOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s: %s is not a Jira key", verifyRequirementsContextAction, key),
			Suggestion: "Pass a key such as INT-4571: the project key, a hyphen and the issue number.",
		}
	}
	project, _, _ := strings.Cut(key, "-")
	return PlanSupportOut{
		Summary: fmt.Sprintf("Checked the Jira key %s of project %s. The tool does not read Jira.", key, project),
		Next: "Call jira with action load and key " + project + ". Then call mcp__atlassian__getJiraIssue with the cached cloudId, issueKey " + key +
			", fields summary and description, and responseContentFormat markdown. Then run " + verifyAnalysisChecks,
		VerifySource:    key,
		StructureChecks: "not run: the input is a Jira ticket",
		JiraProject:     project,
	}, nil
}

// verifyPreplanFirstLine is the start of the first line of a preplan topic
// file.
const verifyPreplanFirstLine = "# Preplan:"

// verifyFirstLineMaxBytes is how many bytes verifyFile reads to see the first
// line of a file. A longer first line counts as it is cut.
const verifyFirstLineMaxBytes = 4096

// verifyFile checks that value is a regular file of at most
// dashboardDeleteReadMax bytes. A relative value starts at contentRoot (the
// active worktree). It follows a link, because a requirements file can be a
// link. It reads only the first verifyFirstLineMaxBytes bytes, to find out if
// the file is a preplan topic file, and returns no file text.
//
//   - no file at the path, or a link whose target is absent: DomainError.
//   - a folder, a pipe, a device or a socket: DomainError.
//   - a file over the size limit: DataError.
//   - any other stat or read failure: InfraError.
func verifyFile(contentRoot, value string) (PlanSupportOut, error) {
	const action = verifyRequirementsContextAction
	path := strings.TrimSpace(value)
	if !filepath.IsAbs(path) {
		path = filepath.Join(contentRoot, path)
	}
	path = filepath.Clean(path)

	infraErr := func(err error) error {
		return &mcpserver.InfraError{
			Msg:        fmt.Sprintf("%s: cannot read %s: %s", action, path, verifyCause(err)),
			Suggestion: "Check the file permissions" + verifyRunAgain,
			Cause:      err,
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return PlanSupportOut{}, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("%s: no file at %s", action, path),
				Suggestion: "Check the path. A relative path starts at the active worktree: " + contentRoot + ".",
				Cause:      err,
			}
		}
		return PlanSupportOut{}, infraErr(err)
	}
	if info.IsDir() {
		return PlanSupportOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s: %s is a folder, not a file", action, path),
			Suggestion: "Pass the path of one requirements file.",
		}
	}
	if !info.Mode().IsRegular() {
		return PlanSupportOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s: %s is not a regular file", action, path),
			Suggestion: "Pass the path of one requirements file.",
		}
	}
	if info.Size() > dashboardDeleteReadMax {
		return PlanSupportOut{}, &mcpserver.DataError{
			Msg: fmt.Sprintf("%s: %s is %d bytes; the limit is %d bytes (%s)",
				action, path, info.Size(), dashboardDeleteReadMax, dashboardDeleteReadMaxText),
			Suggestion: "Split the requirements into smaller files and check each one.",
		}
	}

	head, err := verifyReadHead(path)
	if err != nil {
		return PlanSupportOut{}, infraErr(err)
	}
	summary := fmt.Sprintf("Checked the file %s: a regular file of %d bytes.", path, info.Size())
	if first, _, _ := strings.Cut(string(head), "\n"); strings.HasPrefix(first, verifyPreplanFirstLine) {
		name := strings.TrimSpace(strings.TrimPrefix(first, verifyPreplanFirstLine))
		summary += " This file is a preplan topic file. Run /sdlc:verify-requirements --topic " + name + " to also run the structure checks."
	}
	return PlanSupportOut{
		Summary:         summary,
		Next:            "Read verifySource with the Read tool, in pages when it is large. Then run " + verifyAnalysisChecks,
		VerifySource:    path,
		StructureChecks: "not run: the input is not a topic file",
	}, nil
}

// verifyReadHead returns the first verifyFirstLineMaxBytes bytes of the file
// at path, or fewer when the file is shorter.
func verifyReadHead(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head, err := io.ReadAll(io.LimitReader(f, verifyFirstLineMaxBytes))
	if err != nil {
		return nil, err
	}
	return head, nil
}

// verifyCause returns the text of err without the operation and the path of
// an *fs.PathError, for example "permission denied".
func verifyCause(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// The limits of the OpenSpec lists.
const (
	// verifyOpenspecMaxPaths is the most spec paths that the lists hold in
	// total, the main specs and the specs of the active changes together.
	verifyOpenspecMaxPaths = 1000
	// verifySpecMaxBytes is the most bytes that the action reads from one spec.
	verifySpecMaxBytes = 1 << 20
)

// verifyOpenspecAbsent is the openspecStatus of a repo with no OpenSpec.
const verifyOpenspecAbsent = "not present: openspec/config.yaml not found"

var (
	// verifyRequirementHeadingRe matches "### Requirement: <name>".
	verifyRequirementHeadingRe = regexp.MustCompile(`^### Requirement:[ \t]*(\S.*?)[ \t]*$`)
	// verifyDeltaSectionRe matches "## ADDED Requirements" and the other three.
	verifyDeltaSectionRe = regexp.MustCompile(`^##[ \t]+(ADDED|MODIFIED|REMOVED|RENAMED)[ \t]+Requirements[ \t]*$`)
)

// verifySpecHeadings returns the requirement headings of one spec, in file
// order, never nil. A delta spec (delta true) prefixes each heading with the
// section that holds it: "ADDED: <name>". A heading inside a code fence does
// not count.
func verifySpecHeadings(data []byte, delta bool) []string {
	headings := []string{}
	section, fence := "", ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			switch {
			case fence == "":
				fence = marker
			case fence == marker:
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if m := verifyDeltaSectionRe.FindStringSubmatch(line); m != nil {
			section = m[1]
			continue
		}
		if strings.HasPrefix(line, "## ") {
			section = ""
			continue
		}
		if m := verifyRequirementHeadingRe.FindStringSubmatch(line); m != nil {
			if delta && section != "" {
				headings = append(headings, section+": "+m[1])
			} else {
				headings = append(headings, m[1])
			}
		}
	}
	return headings
}

// verifySpecCollector lists spec files under contentRoot and reads their
// headings. It holds the shared budget of verifyOpenspecMaxPaths paths and the
// warning lines.
type verifySpecCollector struct {
	contentRoot string
	paths       int
	full        bool
	warnings    []string
}

func (c *verifySpecCollector) warn(rel string, cause string) {
	c.warnings = append(c.warnings, rel+": not read: "+cause)
}

// listDir returns the entry names of the folder rel (a slash path under the
// repo root), sorted. A folder that does not exist gives no names and no
// warning. Any other failure gives one warning and no names.
func (c *verifySpecCollector) listDir(rel string) []string {
	entries, err := os.ReadDir(filepath.Join(c.contentRoot, filepath.FromSlash(rel)))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			c.warn(rel, verifyCause(err))
		}
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// specsIn returns the specs of the folder dir (such as "openspec/specs"): one
// for each <dir>/<capability>/spec.md. A path past the budget adds one
// warning, once, and is not listed.
func (c *verifySpecCollector) specsIn(dir string, delta bool) []VerifyOpenspecSpec {
	specs := []VerifyOpenspecSpec{}
	for _, name := range c.listDir(dir) {
		rel := dir + "/" + name + "/spec.md"
		if _, err := os.Stat(filepath.Join(c.contentRoot, filepath.FromSlash(rel))); err != nil &&
			(errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)) {
			continue
		}
		if c.paths >= verifyOpenspecMaxPaths {
			if !c.full {
				c.full = true
				c.warnings = append(c.warnings, fmt.Sprintf("openspec: more than %d spec paths; the first %d are listed", verifyOpenspecMaxPaths, verifyOpenspecMaxPaths))
			}
			return specs
		}
		c.paths++
		specs = append(specs, c.readSpec(rel, delta))
	}
	return specs
}

// readSpec returns the spec at rel with its headings. A spec that is not a
// regular file, is over verifySpecMaxBytes, or cannot be read is listed with
// no headings and one warning.
func (c *verifySpecCollector) readSpec(rel string, delta bool) VerifyOpenspecSpec {
	spec := VerifyOpenspecSpec{Path: rel, Requirements: []string{}}
	path := filepath.Join(c.contentRoot, filepath.FromSlash(rel))
	info, err := os.Stat(path)
	if err != nil {
		c.warn(rel, verifyCause(err))
		return spec
	}
	if !info.Mode().IsRegular() {
		c.warn(rel, "not a regular file")
		return spec
	}
	if info.Size() > verifySpecMaxBytes {
		c.warn(rel, fmt.Sprintf("%d bytes; the limit is %d bytes", info.Size(), verifySpecMaxBytes))
		return spec
	}
	data, err := os.ReadFile(path)
	if err != nil {
		c.warn(rel, verifyCause(err))
		return spec
	}
	spec.Requirements = verifySpecHeadings(data, delta)
	return spec
}

// verifyOpenspec returns the OpenSpec status, the main specs and the active
// changes of the repo at contentRoot, each with its requirement headings, and
// the warning lines. It reads only. openspec.DetectActiveChanges gives the
// status and the names of the active changes (it skips archive and needs
// proposal.md). All four results are never nil. A repo with no
// openspec/config.yaml gives verifyOpenspecAbsent and empty lists.
func verifyOpenspec(contentRoot string) (string, []VerifyOpenspecSpec, []VerifyOpenspecChange, []string) {
	info := openspec.DetectActiveChanges(contentRoot)
	if !info.Present {
		return verifyOpenspecAbsent, []VerifyOpenspecSpec{}, []VerifyOpenspecChange{}, []string{}
	}
	c := &verifySpecCollector{contentRoot: contentRoot, warnings: []string{}}

	// DetectActiveChanges ignores a failure to list the changes folder, so
	// the action lists the folder itself to report the failure.
	c.listDir("openspec/changes")

	mainSpecs := c.specsIn("openspec/specs", false)

	names := make([]string, 0, len(info.ActiveChanges))
	for _, ch := range info.ActiveChanges {
		names = append(names, ch.Name)
	}
	sort.Strings(names)
	changes := make([]VerifyOpenspecChange, 0, len(names))
	for _, name := range names {
		base := "openspec/changes/" + name
		changes = append(changes, VerifyOpenspecChange{
			Name:     name,
			Proposal: base + "/proposal.md",
			Specs:    c.specsIn(base+"/specs", true),
		})
	}
	return "present", mainSpecs, changes, c.warnings
}
