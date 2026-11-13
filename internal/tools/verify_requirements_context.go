package tools

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
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
		err = &mcpserver.DomainError{
			Msg:        verifyRequirementsContextAction + ": verifyKind file is not built yet",
			Suggestion: "Use verifyKind topic or jira.",
		}
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
