package tools

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// This file holds the format checker of a preplan topic file: the rules P1 to
// P11 for the new body of one section, and the parsers that read the summary
// fields of a section. Every function here works on text it is given and
// reads no file.

// preplanWriteContentMax is the largest section body preplan_write accepts.
const preplanWriteContentMax = 64 << 10

// The marker texts of a Decisions row and the fixed bodies of Non-goals and
// Flows. The section names come from plan_support.go (preplanGoalSection and
// the names next to it).
const (
	preplanDroppedPrefix = "Dropped:"           // starts the Decision cell of a dropped row
	preplanMovedPrefix   = "Moved:"             // starts the Decision cell of a moved row
	preplanKeptNote      = "Kept with warning:" // inside the Reason cell of a kept row
	preplanDroppedByNote = "Dropped by:"        // inside the Reason cell of a dropped row

	preplanNoneStated   = "None stated."
	preplanNoFlowChange = "No flow change."
)

// preplanWriteSections lists preplanSections plus "Open questions" and
// "Guardrail check". It is the one list of section names for the write.
var preplanWriteSections = append(slices.Clone(preplanSections),
	preplanOpenQuestionsSection, preplanGuardrailCheckSection)

// preplanAllowedClassDefs are the two classDef lines of
// plugins/sdlc/skills/preplan/SKILL.md ("Mermaid rule"). P10 compares each
// classDef line to them after it trims the leading spaces, and only the
// leading spaces: a tab or a trailing space makes the line differ.
var preplanAllowedClassDefs = []string{
	"classDef new fill:#1f7a3a,stroke:#0b3d1c,color:#ffffff,stroke-width:2px",
	"classDef changed fill:#8a6d00,stroke:#4a3a00,color:#ffffff,stroke-width:2px",
}

// preplanDecisionsHeaderLines are the two header lines of the ## Decisions
// table, without line ends. They come from preplanDecisionsHeader.
var preplanDecisionsHeaderLines = strings.Split(strings.TrimSuffix(preplanDecisionsHeader, "\n"), "\n")

// preplanSmallestCutRe matches a valid Smallest cut line: "Smallest cut: all."
// or "Smallest cut: #1, #3.". It does not check that the ids are live.
var preplanSmallestCutRe = regexp.MustCompile(`^Smallest cut: (all|#[0-9]+(?:, #[0-9]+)*)\.$`)

// preplanSmallestCutLabel starts every Smallest cut line. A line that starts
// with it in any letter case, but that preplanSmallestCutRe does not match, is
// a Smallest cut line that does not parse.
const preplanSmallestCutLabel = "Smallest cut"

// preplanLastCheckLabel comes before the time in a Guardrail check line.
// preplanCheckLine writes it and preplanLastCheckRe reads it.
const preplanLastCheckLabel = "Last check: "

// preplanLastCheckRe finds the time in the text of a Guardrail check line.
var preplanLastCheckRe = regexp.MustCompile(regexp.QuoteMeta(preplanLastCheckLabel) + `(\d{4}-\d{2}-\d{2}T\S+?)\.(?:\s|$)`)

// preplanFormatError names the broken rule. Its Error() text is
// `Section "<section>" breaks rule <rule>: <detail>`. The rule helpers return
// it with an empty Section, and preplanCheckSection sets the Section.
type preplanFormatError struct{ Section, Rule, Detail string }

// Error returns the text `Section "<section>" breaks rule <rule>: <detail>`.
func (e *preplanFormatError) Error() string {
	return fmt.Sprintf("Section \"%s\" breaks rule %s: %s", e.Section, e.Rule, e.Detail)
}

// preplanRuleError returns the error of a broken rule, with an empty Section.
// Its detail is Sprintf(format, args...).
func preplanRuleError(rule, format string, args ...any) *preplanFormatError {
	return &preplanFormatError{Rule: rule, Detail: fmt.Sprintf(format, args...)}
}

// preplanRuleSuggestion returns the Suggestion of a preplan_write call whose
// content breaks rule: the form that the rule accepts. A second call with a
// body of that form passes the rule. P11 accepts only an empty string. An
// unknown rule gives a general text.
func preplanRuleSuggestion(rule string) string {
	switch rule {
	case "P1":
		return fmt.Sprintf("Shorten the body to at most %d bytes%s", preplanWriteContentMax, preplanWriteAgain)
	case "P2":
		return "Remove each line that starts with " + strconv.Quote(preplanTitlePrefix) + " or " + strconv.Quote(preplanHeadingPrefix) +
			" outside a code fence: a section body holds no heading" + preplanWriteAgain
	case "P3":
		return "Close each code fence with a line that holds the same fence characters" + preplanWriteAgain
	case "P4":
		return fmt.Sprintf("Start the body with the two lines %q and %q, or send an empty string%s",
			preplanDecisionsHeaderLines[0], preplanDecisionsHeaderLines[1], preplanWriteAgain)
	case "P5":
		return "Give each row 3 cells. The # cell is a positive whole number, for example 4, and no two rows have the same one" + preplanWriteAgain
	case "P6":
		return fmt.Sprintf("Put at most one %q note in a Reason cell, and a %q note in the Reason cell of each row that starts with %q%s",
			preplanKeptNote, preplanDroppedByNote, preplanDroppedPrefix, preplanWriteAgain)
	case "P7":
		return "Under the table, write only blank lines and at most one line \"Smallest cut: all.\" or \"Smallest cut: #1, #3.\", " +
			"with ids of live rows in ascending order" + preplanWriteAgain
	case "P8":
		return fmt.Sprintf("Send the body %q, or start each line that is not blank with \"- \"%s", preplanNoneStated, preplanWriteAgain)
	case "P9":
		return "Start each line that is not blank with \"- \"" + preplanWriteAgain
	case "P10":
		return fmt.Sprintf("Send the body %q, or a body with a mermaid fence. A classDef line is one of: %s%s",
			preplanNoFlowChange, strings.Join(preplanAllowedClassDefs, "; "), preplanWriteAgain)
	case "P11":
		return "Call preplan_write again with section " + preplanGuardrailCheckSection +
			" and content as an empty string: it deletes the section. No other body is valid."
	}
	return "Fix the content" + preplanWriteAgain
}

// preplanCheckSection checks the new body of one section against the rules
// P1 to P11. It returns nil when the body passes, else the first broken rule:
// P1 (size), P2 and P3 (headings and fences, all sections), then the rules of
// the section. A section with no rule of its own (for example Goal) has P1 to
// P3 only. CRLF and LF line ends give the same result.
func preplanCheckSection(section, body string) *preplanFormatError {
	err := preplanCheckBody(section, body)
	if err != nil {
		err.Section = section
	}
	return err
}

// preplanCheckBody runs the rules of preplanCheckSection. The error that it
// returns has an empty Section.
func preplanCheckBody(section, body string) *preplanFormatError {
	if len(body) > preplanWriteContentMax {
		return preplanRuleError("P1", "the body is %d bytes, the limit is %d bytes", len(body), preplanWriteContentMax)
	}
	lines := preplanBodyLines(body)
	if err := preplanCheckFences(lines); err != nil {
		return err
	}
	switch section {
	case preplanDecisionsSection:
		return preplanCheckDecisions(lines)
	case preplanNonGoalsSection:
		return preplanCheckNonGoals(lines)
	case preplanOpenQuestionsSection:
		return preplanCheckBullets("P9", lines)
	case preplanFlowsSection:
		return preplanCheckFlows(lines)
	case preplanGuardrailCheckSection:
		return preplanCheckGuardrailSection(body)
	}
	return nil
}

// preplanBodyLines splits body into lines. A CRLF line end counts as an LF
// line end, so the rules see one form. A body that ends with a line end gives
// a last empty line.
func preplanBodyLines(body string) []string {
	return strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
}

// preplanBlank reports whether every line holds only white space.
func preplanBlank(lines []string) bool {
	return !slices.ContainsFunc(lines, func(l string) bool { return strings.TrimSpace(l) != "" })
}

// preplanCheckFences runs P2 and P3. The line numbers count from 1.
func preplanCheckFences(lines []string) *preplanFormatError {
	open, openLine := "", 0
	for i, line := range lines {
		if open == "" && (strings.HasPrefix(line, preplanTitlePrefix) || strings.HasPrefix(line, preplanHeadingPrefix)) {
			return preplanRuleError("P2", "line %d starts a heading", i+1)
		}
		next := preplanFenceStep(open, line)
		if open == "" && next != "" {
			openLine = i + 1
		}
		open = next
	}
	if open != "" {
		return preplanRuleError("P3", "the fence on line %d does not close", openLine)
	}
	return nil
}

// preplanFenceStep returns the fence state after line. open is the run that
// opened the current fence, or "" outside a fence. It holds the CommonMark
// close rule: a fence closes at a line that holds only a run of the same
// character, at least as long as the opening run, then spaces or tabs. Call
// it once for each line, in order.
func preplanFenceStep(open, line string) (next string) {
	run := preplanFenceRun(line)
	if open == "" {
		return run
	}
	if run != "" && run[0] == open[0] && len(run) >= len(open) &&
		strings.TrimRight(line[len(run):], " \t\r\n") == "" {
		return ""
	}
	return open
}

// preplanRowLive reports whether the Decision cell of a row starts a live
// proposal: it starts with neither "Dropped:" nor "Moved:".
func preplanRowLive(decision string) bool {
	return !strings.HasPrefix(decision, preplanDroppedPrefix) && !strings.HasPrefix(decision, preplanMovedPrefix)
}

// preplanSplitRow splits one table row into trimmed cells. The outer pipes are
// optional, and "\|" is text, not a cell border.
//
// It does not call splitTableRow (validators.go), which splits on every "|"
// and so cuts a cell that holds "\|". The Decision and Reason cells hold such
// text.
func preplanSplitRow(line string) []string {
	s := strings.TrimPrefix(strings.TrimSpace(line), "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '|':
			cur.WriteString(`\|`)
			i++
		case s[i] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(s[i])
		}
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

// preplanDecisionTable reads the ## Decisions table that starts the lines
// after any blank lines. rows holds the cells of each row below the header and
// separator lines. A row may start with spaces or tabs. after is the index of
// the first line that is not a row. ok is false when the two header lines are
// not there.
//
// It is strict on purpose: the two header lines must equal
// preplanDecisionsHeaderLines, as rule P4 asks. preplanHasDecisionRow
// (plan_support.go) is lenient on purpose: for the emptySections list of a
// file that may be edited by hand, any "|" line below a header and a
// separator counts as a row. The two can disagree on a damaged table; the
// dashboard summary then marks the section unread.
func preplanDecisionTable(lines []string) (rows [][]string, after int, ok bool) {
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	head := preplanDecisionsHeaderLines
	if i+len(head) > len(lines) {
		return nil, 0, false
	}
	for j, want := range head {
		if strings.TrimSpace(lines[i+j]) != want {
			return nil, 0, false
		}
	}
	for i += len(head); i < len(lines); i++ {
		row := strings.TrimLeft(lines[i], " \t")
		if !strings.HasPrefix(row, "|") {
			break
		}
		rows = append(rows, preplanSplitRow(row))
	}
	return rows, i, true
}

// preplanCheckDecisions runs P4 to P7. A blank body passes.
func preplanCheckDecisions(lines []string) *preplanFormatError {
	if preplanBlank(lines) {
		return nil
	}
	rows, after, ok := preplanDecisionTable(lines)
	if !ok {
		return preplanRuleError("P4", "the table header is missing: a body that is not blank starts with %q and %q",
			preplanDecisionsHeaderLines[0], preplanDecisionsHeaderLines[1])
	}
	live, err := preplanCheckRowIDs(rows)
	if err != nil {
		return err
	}
	if err := preplanCheckRowNotes(rows); err != nil {
		return err
	}
	return preplanCheckCutLines(lines[after:], after, live)
}

// preplanCheckRowIDs runs P5. On success live holds the "#" value of each row
// and whether the row is live.
func preplanCheckRowIDs(rows [][]string) (live map[int]bool, err *preplanFormatError) {
	live = map[int]bool{}
	for i, cells := range rows {
		if len(cells) != 3 {
			return nil, preplanRuleError("P5", "row %d has %d cells, want 3", i+1, len(cells))
		}
		id, ok := preplanParseID(cells[0])
		if !ok {
			return nil, preplanRuleError("P5", "row %d has \"#\" value %q", i+1, cells[0])
		}
		if _, seen := live[id]; seen {
			return nil, preplanRuleError("P5", "row %d repeats \"#\" value %d", i+1, id)
		}
		live[id] = preplanRowLive(cells[1])
	}
	return live, nil
}

// preplanParseID parses a positive whole number written with digits only.
func preplanParseID(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 || s != strconv.Itoa(n) {
		return 0, false
	}
	return n, true
}

// preplanCheckRowNotes runs P6. It returns the first failure.
func preplanCheckRowNotes(rows [][]string) *preplanFormatError {
	for i, cells := range rows {
		decision, reason := cells[1], cells[2]
		if n := strings.Count(reason, preplanKeptNote); n > 1 {
			return preplanRuleError("P6", "row %d has %d kept notes, the limit is 1", i+1, n)
		}
		if strings.HasPrefix(decision, preplanDroppedPrefix) && !strings.Contains(reason, preplanDroppedByNote) {
			return preplanRuleError("P6", "row %d starts with %q but its Reason has no %q", i+1, preplanDroppedPrefix, preplanDroppedByNote)
		}
	}
	return nil
}

// preplanCheckCutLines runs P7 on the lines after the table. offset is the
// index of the first of them in the body, for the line numbers. live holds
// the "#" value of each row and whether the row is live.
func preplanCheckCutLines(lines []string, offset int, live map[int]bool) *preplanFormatError {
	seen := false
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		m := preplanSmallestCutRe.FindStringSubmatch(line)
		switch {
		case m == nil:
			return preplanRuleError("P7", "line %d is not blank and is not a Smallest cut line", offset+i+1)
		case seen:
			return preplanRuleError("P7", "line %d is a second Smallest cut line", offset+i+1)
		}
		seen = true
		if err := preplanCheckCutIDs(m[1], offset+i+1, live); err != nil {
			return err
		}
	}
	return nil
}

// preplanCheckCutIDs runs P7 on the list of a Smallest cut line that
// preplanSmallestCutRe matched: "all", or ids like "#1, #3". lineNo is the
// line number for the error text.
func preplanCheckCutIDs(list string, lineNo int, live map[int]bool) *preplanFormatError {
	if list == "all" {
		return nil
	}
	prev := 0
	for _, field := range strings.Split(list, ", ") {
		id, ok := preplanParseID(strings.TrimPrefix(field, "#"))
		switch {
		case !ok:
			return preplanRuleError("P7", "line %d has the id %q", lineNo, field)
		case id <= prev:
			return preplanRuleError("P7", "%s is not in ascending order", field)
		case !live[id]:
			return preplanRuleError("P7", "%s is not a live row", field)
		}
		prev = id
	}
	return nil
}

// preplanCheckNonGoals runs P8: the whole body is "None stated.", or each
// line that is not blank is a bullet.
func preplanCheckNonGoals(lines []string) *preplanFormatError {
	if strings.TrimSpace(strings.Join(lines, "\n")) == preplanNoneStated {
		return nil
	}
	return preplanCheckBullets("P8", lines)
}

// preplanCheckBullets reports rule when a line that is not blank does not
// start with "- ".
func preplanCheckBullets(rule string, lines []string) *preplanFormatError {
	for i, line := range lines {
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "- ") {
			return preplanRuleError(rule, "line %d is not a bullet", i+1)
		}
	}
	return nil
}

// preplanCheckFlows runs P10: the body is "No flow change.", or it holds a
// mermaid fence. Each classDef line equals one of preplanAllowedClassDefs
// after its leading spaces go (see preplanBadClassDef). P10 does not check the
// text twin.
func preplanCheckFlows(lines []string) *preplanFormatError {
	if strings.TrimSpace(strings.Join(lines, "\n")) == preplanNoFlowChange {
		return nil
	}
	for i, line := range lines {
		if preplanBadClassDef(line) {
			return preplanRuleError("P10", "classDef on line %d is not allowed", i+1)
		}
	}
	if mermaid, _ := preplanMermaidLines(lines); !mermaid {
		return preplanRuleError("P10", "the body is not %q and holds no mermaid fence", preplanNoFlowChange)
	}
	return nil
}

// preplanBadClassDef reports whether line is a classDef line that differs
// from every line of preplanAllowedClassDefs. A line is a classDef line when
// it starts with "classDef" after its leading spaces and tabs. The compare
// trims the leading spaces only, so a tab, a trailing space or any other
// change makes the line differ.
func preplanBadClassDef(line string) bool {
	probe := strings.TrimLeft(line, " \t")
	if probe != "classDef" && !strings.HasPrefix(probe, "classDef ") {
		return false
	}
	return !slices.Contains(preplanAllowedClassDefs, strings.TrimLeft(line, " "))
}

// preplanCheckGuardrailSection runs P11: only an empty body is valid,
// because the write deletes this legacy section and never fills it. A body of
// white space only is not empty.
func preplanCheckGuardrailSection(body string) *preplanFormatError {
	if body != "" {
		return preplanRuleError("P11", "only an empty body deletes this section")
	}
	return nil
}

// preplanDecisionsSummary holds the proposal counts of a ## Decisions body.
// A kept row is also live, so Kept is part of Live. A Moved row is in no count.
type preplanDecisionsSummary struct {
	Live, Kept, Dropped int
	CutSet              bool // a valid "Smallest cut:" line exists
}

// preplanSummarizeDecisions counts the rows of a ## Decisions body that
// already passed P4 to P7. Live is every row that starts with neither
// "Dropped:" nor "Moved:". Kept is the live rows whose Reason cell holds
// "Kept with warning:". Dropped is the rows that start with "Dropped:". A body
// with no table header gives the zero value. A row with fewer than 3 cells
// (a row that did not pass P5) is in no count.
func preplanSummarizeDecisions(body string) preplanDecisionsSummary {
	var sum preplanDecisionsSummary
	lines := preplanBodyLines(body)
	rows, after, ok := preplanDecisionTable(lines)
	if !ok {
		return sum
	}
	for _, cells := range rows {
		if len(cells) < 3 {
			continue
		}
		switch decision := cells[1]; {
		case strings.HasPrefix(decision, preplanDroppedPrefix):
			sum.Dropped++
		case preplanRowLive(decision):
			sum.Live++
			if strings.Contains(cells[2], preplanKeptNote) {
				sum.Kept++
			}
		}
	}
	for _, line := range lines[after:] {
		if preplanSmallestCutRe.MatchString(strings.TrimSpace(line)) {
			sum.CutSet = true
		}
	}
	return sum
}

// preplanGoalSentence returns the first sentence of a ## Goal body: the text
// of the first paragraph up to and including the first ". ", else the whole
// first paragraph. The lines of a paragraph join with one space. A blank body
// gives "".
func preplanGoalSentence(body string) string {
	var para []string
	for _, line := range preplanBodyLines(body) {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(para) > 0 {
				break
			}
			continue
		}
		para = append(para, line)
	}
	text := strings.Join(para, " ")
	if i := strings.Index(text, ". "); i >= 0 {
		return text[:i+1]
	}
	return text
}

// preplanOpenQuestionCount counts the bullets of an ## Open questions body.
func preplanOpenQuestionCount(body string) int {
	bullets, _ := preplanBullets(body)
	return len(bullets)
}

// preplanBullets reads a body of "- " bullets. bullets holds the trimmed text
// of each line that starts with "- ", in order. other holds each line that is
// not blank and does not start with "- ", for example "* x", "  - x" or "1. x".
func preplanBullets(body string) (bullets, other []string) {
	for _, line := range preplanBodyLines(body) {
		if text, ok := strings.CutPrefix(line, "- "); ok {
			bullets = append(bullets, strings.TrimSpace(text))
		} else if strings.TrimSpace(line) != "" {
			other = append(other, line)
		}
	}
	return bullets, other
}

// preplanLastCheck returns the "Last check:" time, in UTC, of the first
// **Guardrail check:** line in head. ok is false when head has no such line,
// the line has no valid time, or the check record is the legacy
// "## Guardrail check" section, which is not a check line. It reads the time
// with dashboardParseTime, which accepts an RFC 3339 time with or without
// fractional seconds.
func preplanLastCheck(head string) (at time.Time, ok bool) {
	m := preplanGuardrailCheckRe.FindStringSubmatch(head)
	if m == nil {
		return time.Time{}, false
	}
	t := preplanLastCheckRe.FindStringSubmatch(strings.TrimSpace(m[1]))
	if t == nil {
		return time.Time{}, false
	}
	parsed, ok := dashboardParseTime(t[1])
	if !ok {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

// The rule ids of preplanStructureFindings, in the order of the findings.
const (
	preplanRuleOpenFence      = "open-fence"
	preplanRuleEmptySection   = "empty-section"
	preplanRuleDecisionsTable = "decisions-table"
	preplanRuleCutNotLive     = "cut-not-live"
	preplanRuleMovedNoAsk     = "moved-no-question"
	preplanRuleFlowNoProposal = "flow-no-proposal"
)

// preplanFlowMarkRe finds the class mark of a new or changed node in a mermaid
// line, for example "A2[Check]:::changed".
var preplanFlowMarkRe = regexp.MustCompile(`:::(?:new|changed)\b`)

// preplanStructureFinding is one structure finding of a topic file.
type preplanStructureFinding struct {
	Rule    string `json:"rule"`    // one of the preplanRule* constants
	Section string `json:"section"` // "## Goal" ... "## Decisions", "## Open questions", or "" for open-fence
	Row     string `json:"row"`     // "#3", or "" when the rule has no row
	Detail  string `json:"detail"`  // one plain sentence for the report
}

// preplanStructureFindings returns the structure findings of a topic file.
// It reads only data. It never returns nil.
//
// The rules run in this order, and the findings keep it: open-fence,
// empty-section (in preplanSections order), decisions-table, cut-not-live (by
// row id), moved-no-question, flow-no-proposal. The rules read the section
// spans of preplanSectionSpans, so a heading after an unclosed fence counts
// as absent, as in preplanEmptySectionsOf. CRLF and LF line ends give the same
// findings.
//
// When the ## Decisions table does not parse, decisions-table fires once, and
// cut-not-live and moved-no-question do not run. flow-no-proposal still runs,
// and it counts 0 live rows. When the table parses, decisions-table also fires
// once for each row with fewer than 3 cells (no count holds such a row) and
// once for each line after the table that starts with "Smallest cut" but does
// not parse. The rules moved-no-question and flow-no-proposal compare counts
// of rules, not matches of text. "Smallest cut: all." never fires
// cut-not-live.
func preplanStructureFindings(data []byte) []preplanStructureFinding {
	spans, openFence := preplanSectionSpans(data)
	return preplanStructureFindingsOf(data, spans, openFence)
}

// preplanStructureFindingsOf is preplanStructureFindings for spans and
// openFence that the caller already got from preplanSectionSpans(data).
func preplanStructureFindingsOf(data []byte, spans []preplanSpan, openFence string) []preplanStructureFinding {
	findings := []preplanStructureFinding{}
	body := func(name string) (text string, found bool) {
		i := preplanFindSpan(spans, name)
		if i < 0 {
			return "", false
		}
		return string(data[spans[i].bodyStart:spans[i].bodyEnd]), true
	}

	if openFence != "" {
		findings = append(findings, preplanStructureFinding{
			Rule: preplanRuleOpenFence,
			Detail: fmt.Sprintf("A code fence that starts with %s never closes. Each section after it counts as absent.",
				openFence),
		})
	}
	for _, heading := range preplanEmptySectionsOf(data, spans) {
		findings = append(findings, preplanStructureFinding{
			Rule:    preplanRuleEmptySection,
			Section: heading,
			Detail:  fmt.Sprintf("Section %s is empty.", heading),
		})
	}

	decisions, _ := body(preplanDecisionsSection)
	lines := preplanBodyLines(decisions)
	rows, after, tableOK := preplanDecisionTable(lines)
	decisionsHeading := preplanHeadingPrefix + preplanDecisionsSection
	if !tableOK && !preplanBlank(lines) {
		findings = append(findings, preplanStructureFinding{
			Rule:    preplanRuleDecisionsTable,
			Section: decisionsHeading,
			Detail:  "The " + decisionsHeading + " table does not parse. The cut and moved checks did not run.",
		})
	}

	live, moved := 0, 0
	for n, cells := range rows {
		switch {
		case len(cells) < 3:
			findings = append(findings, preplanStructureFinding{
				Rule:    preplanRuleDecisionsTable,
				Section: decisionsHeading,
				Row:     preplanRowLabel(cells),
				Detail: fmt.Sprintf("Table row %d has %s, not 3. The live, moved and cut checks skip it.",
					n+1, preplanCount(len(cells), "cell")),
			})
		case strings.HasPrefix(cells[1], preplanMovedPrefix):
			moved++
		case preplanRowLive(cells[1]):
			live++
		}
	}
	if tableOK {
		for _, line := range lines[after:] {
			text := strings.TrimSpace(line)
			if len(text) >= len(preplanSmallestCutLabel) && strings.EqualFold(text[:len(preplanSmallestCutLabel)], preplanSmallestCutLabel) &&
				!preplanSmallestCutRe.MatchString(text) {
				findings = append(findings, preplanStructureFinding{
					Rule:    preplanRuleDecisionsTable,
					Section: decisionsHeading,
					Detail:  fmt.Sprintf("The line %q does not parse. The cut check skips it.", text),
				})
			}
		}
		findings = append(findings, preplanCutFindings(rows, lines[after:])...)
		questions, _ := body(preplanOpenQuestionsSection)
		if bullets := preplanOpenQuestionCount(questions); moved > bullets {
			findings = append(findings, preplanStructureFinding{
				Rule:    preplanRuleMovedNoAsk,
				Section: preplanHeadingPrefix + preplanOpenQuestionsSection,
				Detail: fmt.Sprintf("%s with %s, but %s%s holds %s.",
					preplanCountVerb(moved, "row", "starts", "start"), preplanMovedPrefix,
					preplanHeadingPrefix, preplanOpenQuestionsSection, preplanCount(bullets, "bullet")),
			})
		}
	}

	if flows, found := body(preplanFlowsSection); found && live == 0 && preplanFlowsMarkNodes(flows) {
		flowsHeading := preplanHeadingPrefix + preplanFlowsSection
		findings = append(findings, preplanStructureFinding{
			Rule:    preplanRuleFlowNoProposal,
			Section: flowsHeading,
			Detail:  flowsHeading + " marks new or changed steps, but no live proposal exists.",
		})
	}
	return findings
}

// preplanRowLabel returns "#<id>" for a row whose first cell is a positive
// whole number, else "".
func preplanRowLabel(cells []string) string {
	if len(cells) > 0 {
		if id, ok := preplanParseID(cells[0]); ok {
			return "#" + strconv.Itoa(id)
		}
	}
	return ""
}

// preplanCount returns "1 row" or "2 rows": n and the noun, with an "s" after
// the noun for any n other than 1.
func preplanCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// preplanCountVerb returns preplanCount and the verb that agrees with it:
// "1 row starts" or "2 rows start".
func preplanCountVerb(n int, noun, one, many string) string {
	if n == 1 {
		return preplanCount(n, noun) + " " + one
	}
	return preplanCount(n, noun) + " " + many
}

// preplanCutFindings returns the cut-not-live findings. rows are the rows of
// the parsed ## Decisions table and tail holds the lines after the table. Only
// the first Smallest cut line counts. Each id of it that names no row, or a row
// that starts with "Dropped:" or "Moved:", gives one finding. The findings
// come in ascending row id, one for each id. A row with fewer than 3 cells, or
// with an id that is not a positive whole number, is no row. When two rows have
// the same id, the first one counts.
func preplanCutFindings(rows [][]string, tail []string) []preplanStructureFinding {
	var ids []int
	for _, line := range tail {
		m := preplanSmallestCutRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if m[1] != "all" {
			for _, field := range strings.Split(m[1], ", ") {
				if id, ok := preplanParseID(strings.TrimPrefix(field, "#")); ok {
					ids = append(ids, id)
				}
			}
		}
		break
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)

	decision := map[int]string{}
	for _, cells := range rows {
		if len(cells) < 3 {
			continue
		}
		if id, ok := preplanParseID(cells[0]); ok {
			if _, seen := decision[id]; !seen {
				decision[id] = cells[1]
			}
		}
	}

	var findings []preplanStructureFinding
	for _, id := range ids {
		row := "#" + strconv.Itoa(id)
		text, isRow := decision[id]
		var detail string
		switch {
		case !isRow:
			detail = fmt.Sprintf("Smallest cut names %s, but no row %s exists.", row, row)
		case preplanRowLive(text):
			continue
		default:
			marker := preplanDroppedPrefix
			if strings.HasPrefix(text, preplanMovedPrefix) {
				marker = preplanMovedPrefix
			}
			detail = fmt.Sprintf("Smallest cut names %s, but row %s starts with %s.", row, row, marker)
		}
		findings = append(findings, preplanStructureFinding{
			Rule:    preplanRuleCutNotLive,
			Section: preplanHeadingPrefix + preplanDecisionsSection,
			Row:     row,
			Detail:  detail,
		})
	}
	return findings
}

// preplanFlowsMarkNodes reports whether the body of ## Flows holds a node with
// a ":::new" or ":::changed" mark inside a mermaid fence. A mark in other
// text is no node.
func preplanFlowsMarkNodes(body string) bool {
	_, inside := preplanMermaidLines(preplanBodyLines(body))
	return slices.ContainsFunc(inside, preplanFlowMarkRe.MatchString)
}

// preplanMermaidLines walks lines with preplanFenceStep. found reports whether
// a fence opens with the info string "mermaid". inside holds the lines between
// the open line and the close line of each mermaid fence, in order; an
// unclosed mermaid fence holds every line after its open line.
func preplanMermaidLines(lines []string) (found bool, inside []string) {
	open, mermaid := "", false
	for _, line := range lines {
		next := preplanFenceStep(open, line)
		switch {
		case open == "" && next != "":
			mermaid = strings.TrimSpace(line[len(next):]) == "mermaid"
			found = found || mermaid
		case open != "" && next != "" && mermaid:
			inside = append(inside, line)
		}
		open = next
	}
	return found, inside
}
