// Shared check of the OpenSpec header lines of a plan file.
//
// ship_prepare, execute_state init and the plan validator all need to know
// whether a plan that names an OpenSpec change has its artifacts authored.
// The rule lives here, as one pure function, so the three callers agree.
package openspec

import (
	"regexp"
	"strings"
)

// Kinds of PlanHeaderProblem.
const (
	// PlanHeaderCreateNotAuthored: the plan has an **OpenSpec-Create:** line
	// and no **OpenSpec-Staging:** or **OpenSpec-Saved:** line. The plan skill
	// replaces the Create line with a Staging line only after it staged the
	// artifacts, so the artifacts do not exist.
	PlanHeaderCreateNotAuthored = "create-not-authored"
	// PlanHeaderAppendixTBD: the plan has a Create or Staging line, no
	// **OpenSpec-Saved:** line, and an ## OpenSpec Appendix whose body still
	// starts with the [TBD placeholder.
	PlanHeaderAppendixTBD = "appendix-tbd"
)

// SavedHeaderRe matches the plan header line openspec_save writes after a
// save: **OpenSpec-Saved:** openspec/changes/<change>/ (branch <b>).
// Group 1 is the change name, group 2 the optional branch name.
var SavedHeaderRe = regexp.MustCompile(`(?m)^\*\*OpenSpec-Saved:\*\*[ \t]*openspec/changes/([^\s/]+)/?(?:[ \t]*\(branch[ \t]+([^\s)]+)\))?[ \t\r]*$`)

// createHeaderRe matches the plan header line the plan skill writes when the
// user picks "Create OpenSpec change": **OpenSpec-Create:** <change>. Group 1
// is the change name as written.
var createHeaderRe = regexp.MustCompile(`(?m)^\*\*OpenSpec-Create:\*\*[ \t]*(\S+)[ \t\r]*$`)

// appendixHeadingRe matches the heading of the OpenSpec Appendix section.
var appendixHeadingRe = regexp.MustCompile(`^##[ \t]+OpenSpec Appendix[ \t\r]*$`)

// fenceOpenRe matches a line that opens or closes a code fence: up to three
// spaces of indent, then three or more backticks or tildes.
var fenceOpenRe = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")

// PlanHeaderProblem is one reason a plan that names an OpenSpec change is not
// ready to ship or execute.
type PlanHeaderProblem struct {
	// Kind is PlanHeaderCreateNotAuthored or PlanHeaderAppendixTBD.
	Kind string `json:"kind"`
	// Change is the change name from the Create or Staging line.
	Change string `json:"change"`
	// Line is the plan line that caused the problem: the Create line, or the
	// first line of the Appendix body.
	Line string `json:"line"`
}

// stripFences returns planContent with every line inside a code fence, and
// every fence line itself, replaced by an empty line. Line numbers and line
// breaks stay the same, so the (?m) header regexes match only text that is
// outside a fence. A fence closes at a line of the same character that is at
// least as long as the opening line; an unclosed fence runs to the end.
func stripFences(planContent string) string {
	lines := strings.Split(planContent, "\n")
	var fenceChar byte
	fenceLen := 0
	for i, line := range lines {
		m := fenceOpenRe.FindStringSubmatch(line)
		switch {
		case fenceLen == 0 && m != nil:
			fenceChar, fenceLen = m[1][0], len(m[1])
			lines[i] = ""
		case fenceLen > 0:
			if m != nil && m[1][0] == fenceChar && len(m[1]) >= fenceLen &&
				strings.TrimSpace(line[strings.Index(line, m[1])+len(m[1]):]) == "" {
				fenceLen = 0
			}
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

// CreateChangeFromPlan returns the change name from the plan's
// **OpenSpec-Create:** header. ok is false when the plan has no such header
// outside a code fence. The name is returned as written; it is not checked
// against ValidChangeName here.
func CreateChangeFromPlan(planContent string) (change string, ok bool) {
	m := createHeaderRe.FindStringSubmatch(stripFences(planContent))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// appendixFirstLine returns the first non-blank line of the body of the
// ## OpenSpec Appendix section of plan, trimmed. plan must already have its
// fenced lines blanked (see stripFences). The body ends at the next heading
// of level 1 or 2. ok is false when there is no Appendix heading or the body
// is empty.
func appendixFirstLine(plan string) (line string, ok bool) {
	inAppendix := false
	for _, l := range strings.Split(plan, "\n") {
		switch {
		case appendixHeadingRe.MatchString(l):
			inAppendix = true
		case inAppendix && (strings.HasPrefix(l, "# ") || strings.HasPrefix(l, "## ")):
			return "", false
		case inAppendix && strings.TrimSpace(l) != "":
			return strings.TrimSpace(l), true
		}
	}
	return "", false
}

// CheckPlanHeaders returns the reasons a plan that names an OpenSpec change
// is not ready to ship or execute. It returns nil for every other plan:
//
//   - a plan with no Create and no Staging line (a Source-only plan, or a
//     plan with no OpenSpec header)
//   - a plan with an **OpenSpec-Saved:** line (the change is on the branch)
//
// For any other plan it returns at most one problem. A Create line with no
// Staging line is PlanHeaderCreateNotAuthored. Otherwise a Create or Staging
// line with an Appendix body that starts with [TBD is PlanHeaderAppendixTBD.
// Text inside a code fence is ignored, and lines are read as the header
// regexes read them, so a header written in prose does not count.
func CheckPlanHeaders(planContent string) []PlanHeaderProblem {
	plan := stripFences(planContent)
	if SavedHeaderRe.MatchString(plan) {
		return nil
	}
	change, hasStaging := StagedChangeFromPlan(plan)
	createMatch := createHeaderRe.FindStringSubmatch(plan)
	hasCreate := createMatch != nil
	if !hasCreate && !hasStaging {
		return nil
	}
	if hasCreate && !hasStaging {
		return []PlanHeaderProblem{{
			Kind:   PlanHeaderCreateNotAuthored,
			Change: createMatch[1],
			Line:   strings.TrimSpace(createMatch[0]),
		}}
	}
	if first, ok := appendixFirstLine(plan); ok && strings.HasPrefix(first, "[TBD") {
		return []PlanHeaderProblem{{Kind: PlanHeaderAppendixTBD, Change: change, Line: first}}
	}
	return nil
}
