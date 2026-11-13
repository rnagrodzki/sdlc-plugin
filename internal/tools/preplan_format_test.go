package tools

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fenced writes a code fence in a test string: the raw strings of this file
// cannot hold backticks, so a run of three apostrophes stands for a run of
// three backticks. Write a run of four or more backticks as an ordinary quoted
// string.
func fenced(s string) string { return strings.ReplaceAll(s, "'''", "```") }

// formatDecisionsHead is the header and separator lines of a ## Decisions
// table, with LF line ends. Test bodies add rows after it.
const formatDecisionsHead = "| # | Decision | Reason |\n|---|---|---|\n"

// formatRows returns formatDecisionsHead followed by the given rows.
func formatRows(rows ...string) string {
	return formatDecisionsHead + strings.Join(rows, "\n") + "\n"
}

// formatMermaid is a valid ## Flows body with a mermaid fence. Its line 6 is
// the classDef new line and its line 7 is the classDef changed line.
var formatMermaid = fenced(`'''mermaid
flowchart LR
  subgraph After
    A1[Start] --> A2[Check]:::changed
  end
  classDef new fill:#1f7a3a,stroke:#0b3d1c,color:#ffffff,stroke-width:2px
  classDef changed fill:#8a6d00,stroke:#4a3a00,color:#ffffff,stroke-width:2px
'''

After:
1. The user starts.
`)

// wantFormatPass fails the test when the body breaks a rule.
func wantFormatPass(t *testing.T, section, body string) {
	t.Helper()
	if err := preplanCheckSection(section, body); err != nil {
		t.Errorf("preplanCheckSection(%q) = %v, want nil\nbody: %q", section, err, body)
	}
}

// wantFormatError fails the test unless the body breaks rule and the Detail of
// the error holds detail.
func wantFormatError(t *testing.T, section, body, rule, detail string) {
	t.Helper()
	err := preplanCheckSection(section, body)
	if err == nil {
		t.Errorf("preplanCheckSection(%q) = nil, want rule %s\nbody: %q", section, rule, body)
		return
	}
	if err.Rule != rule || err.Section != section || !strings.Contains(err.Detail, detail) {
		t.Errorf("preplanCheckSection(%q) = {Section:%q Rule:%q Detail:%q}, want rule %s and detail %q\nbody: %q",
			section, err.Section, err.Rule, err.Detail, rule, detail, body)
	}
}

// formatRuleCase is one body for preplanCheckSection. rule is "" when the body
// passes. Each string of detail is part of the Detail of the error.
type formatRuleCase struct {
	name, section, body string
	rule                string
	detail              []string
}

// formatRuleCases is the table of TestPreplanFormatRules. The name of a case
// starts with "P<n> pass" or "P<n> fail" for the rule that it covers.
var formatRuleCases = []formatRuleCase{
	// P1: size
	{"P1 pass at the limit", "Goal", strings.Repeat("a", preplanWriteContentMax), "", nil},
	{"P1 fail over the limit", "Goal", strings.Repeat("a", preplanWriteContentMax+1), "P1",
		[]string{fmt.Sprintf("the body is %d bytes, the limit is %d bytes", preplanWriteContentMax+1, preplanWriteContentMax)}},
	{"P1 fail is checked before the other rules", "Decisions", "# H\n" + strings.Repeat("a", preplanWriteContentMax), "P1",
		[]string{fmt.Sprintf("the body is %d bytes", preplanWriteContentMax+4)}},

	// P2: headings outside a fence
	{"P2 pass heading inside a fence", "Goal", fenced("text\n'''\n# not a heading\n## nor this\n'''\n"), "", nil},
	{"P2 pass hash lines that are not headings", "Goal", "#tag\n### three hashes\n  # indented\n", "", nil},
	{"P2 fail H1", "Goal", "text\n\n# Title\n", "P2", []string{"line 3 starts a heading"}},
	{"P2 fail H2", "Problem and benefit", "## Goal\n", "P2", []string{"line 1 starts a heading"}},
	{"P2 fail heading after a closed fence", "Goal", fenced("'''\ncode\n'''\n## Late\n"), "P2", []string{"line 4 starts a heading"}},

	// P3: fences close
	{"P3 pass tilde fence", "Goal", "~~~\ncode\n~~~\n", "", nil},
	{"P3 pass long fence holds a short run", "Goal", "````\n```\n````\n", "", nil},
	{"P3 fail open fence", "Goal", fenced("text\n'''go\ncode\n"), "P3", []string{"the fence on line 2 does not close"}},
	{"P3 fail short run does not close", "Goal", "````\ncode\n```\n", "P3", []string{"the fence on line 1 does not close"}},

	// P4: Decisions table header
	{"P4 pass blank body", "Decisions", "\n\n", "", nil},
	{"P4 pass header only", "Decisions", "\n" + formatDecisionsHead, "", nil},
	{"P4 fail no header", "Decisions", "| 1 | A | B |\n", "P4", []string{"the table header is missing"}},
	{"P4 fail wrong header", "Decisions", "| # | Decision |\n|---|---|\n", "P4", []string{"the table header is missing"}},
	{"P4 fail text before the header", "Decisions", "Intro.\n" + formatDecisionsHead, "P4", []string{"the table header is missing"}},

	// P5: rows
	{"P5 pass rows", "Decisions", formatRows("| 1 | A | B |", "| 3 | C | D |"), "", nil},
	{"P5 pass escaped pipe is text", "Decisions", formatRows(`| 1 | a \| b | c |`), "", nil},
	{"P5 fail non-number", "Decisions", formatRows("| 1 | A | B |", "| x | C | D |"), "P5", []string{`row 2 has "#" value "x"`}},
	{"P5 fail zero", "Decisions", formatRows("| 0 | A | B |"), "P5", []string{`row 1 has "#" value "0"`}},
	{"P5 fail sign", "Decisions", formatRows("| +1 | A | B |"), "P5", []string{`row 1 has "#" value "+1"`}},
	{"P5 fail leading zero", "Decisions", formatRows("| 01 | A | B |"), "P5", []string{`row 1 has "#" value "01"`}},
	{"P5 fail repeated id", "Decisions", formatRows("| 1 | A | B |", "| 1 | C | D |"), "P5", []string{`row 2 repeats "#" value 1`}},
	{"P5 fail two cells", "Decisions", formatRows("| 1 | A |"), "P5", []string{"row 1 has 2 cells, want 3"}},
	{"P5 fail four cells", "Decisions", formatRows("| 1 | A | B | C |"), "P5", []string{"row 1 has 4 cells, want 3"}},

	// P6: notes
	{"P6 pass one kept note", "Decisions", formatRows("| 1 | A | R. Kept with warning: G1. |"), "", nil},
	{"P6 pass dropped row with its note", "Decisions", formatRows("| 1 | Dropped: A | R. Dropped by: G1. |"), "", nil},
	{"P6 fail two kept notes", "Decisions", formatRows("| 1 | A | x |", "| 2 | B | Kept with warning: G1. Kept with warning: G2. |"), "P6",
		[]string{"row 2 has 2 kept notes, the limit is 1"}},
	{"P6 fail dropped row without Dropped by", "Decisions", formatRows("| 1 | Dropped: A | R |"), "P6",
		[]string{`row 1 starts with "Dropped:" but its Reason has no "Dropped by:"`}},

	// P7: lines after the table
	{"P7 pass all", "Decisions", formatRows("| 1 | A | B |") + "\nSmallest cut: all.\n", "", nil},
	{"P7 pass ascending live ids", "Decisions", formatRows("| 1 | A | B |", "| 2 | Dropped: C | Dropped by: G. |", "| 3 | D | E |") + "\nSmallest cut: #1, #3.\n", "", nil},
	{"P7 pass one id", "Decisions", formatRows("| 2 | A | B |") + "Smallest cut: #2.\n", "", nil},
	{"P7 fail id is not live", "Decisions", formatRows("| 1 | A | B |", "| 4 | Moved: C | D |") + "\nSmallest cut: #1, #4.\n", "P7", []string{"#4 is not a live row"}},
	{"P7 fail id is not a row", "Decisions", formatRows("| 1 | A | B |") + "\nSmallest cut: #5.\n", "P7", []string{"#5 is not a live row"}},
	{"P7 fail descending ids", "Decisions", formatRows("| 1 | A | B |", "| 2 | C | D |") + "\nSmallest cut: #2, #1.\n", "P7", []string{"#1 is not in ascending order"}},
	{"P7 fail repeated id", "Decisions", formatRows("| 1 | A | B |") + "\nSmallest cut: #1, #1.\n", "P7", []string{"#1 is not in ascending order"}},
	{"P7 fail id with a leading zero", "Decisions", formatRows("| 1 | A | B |") + "Smallest cut: #01.\n", "P7", []string{`line 4 has the id "#01"`}},
	{"P7 fail id zero", "Decisions", formatRows("| 1 | A | B |") + "Smallest cut: #0.\n", "P7", []string{`line 4 has the id "#0"`}},
	{"P7 fail id too large for a number", "Decisions", formatRows("| 1 | A | B |") + "Smallest cut: #1, #99999999999999999999.\n", "P7",
		[]string{`line 4 has the id "#99999999999999999999"`}},
	{"P7 fail second cut line", "Decisions", formatRows("| 1 | A | B |") + "\nSmallest cut: all.\nSmallest cut: all.\n", "P7", []string{"line 6 is a second Smallest cut line"}},
	{"P7 fail text after the table", "Decisions", formatRows("| 1 | A | B |") + "\nNotes.\n", "P7", []string{"line 5 is not blank and is not a Smallest cut line"}},
	{"P7 fail row after a blank line", "Decisions", formatRows("| 1 | A | B |") + "\n| 2 | C | D |\n", "P7", []string{"line 5 is not blank"}},
	{"P7 fail wrong form", "Decisions", formatRows("| 1 | A | B |") + "Smallest cut: #1 and #2\n", "P7", []string{"line 4 is not blank"}},

	// P8: Non-goals
	{"P8 pass bullets", "Non-goals", "\n- One.\n\n- Two.\n", "", nil},
	{"P8 pass None stated", "Non-goals", "\nNone stated.\n", "", nil},
	{"P8 fail prose", "Non-goals", "Prose line.\n", "P8", []string{"line 1 is not a bullet"}},
	{"P8 fail None stated beside a bullet", "Non-goals", "None stated.\n- One.\n", "P8", []string{"line 1 is not a bullet"}},

	// P9: Open questions
	{"P9 pass bullets", "Open questions", "- A?\n- B?\n", "", nil},
	{"P9 fail prose", "Open questions", "- A?\nB?\n", "P9", []string{"line 2 is not a bullet"}},

	// P10: Flows
	{"P10 pass No flow change", "Flows", "\nNo flow change.\n", "", nil},
	{"P10 pass mermaid with allowed classDefs", "Flows", "\n### Flow 1\n\n" + formatMermaid, "", nil},
	{"P10 pass classDef lines indented with spaces", "Flows", strings.ReplaceAll(formatMermaid, "  classDef", "      classDef"), "", nil},
	{"P10 fail neither form", "Flows", "A diagram is not needed.\n", "P10", []string{`the body is not "No flow change." and holds no mermaid fence`}},
	{"P10 fail blank body", "Flows", "\n", "P10", []string{"holds no mermaid fence"}},
	{"P10 fail fence of another language", "Flows", fenced("'''text\nbox\n'''\n"), "P10", []string{"holds no mermaid fence"}},
	{"P10 fail classDef is not allowed", "Flows", fenced("'''mermaid\nflowchart LR\n  A --> B\n  classDef new fill:#fff\n'''\n"), "P10", []string{"classDef on line 4 is not allowed"}},
	{"P10 fail classDef is changed", "Flows", strings.Replace(formatMermaid, "#8a6d00", "#8a6d01", 1), "P10", []string{"classDef on line 7 is not allowed"}},
	{"P10 fail bare classDef", "Flows", fenced("'''mermaid\nclassDef\n'''\n"), "P10", []string{"classDef on line 2 is not allowed"}},
	{"P10 fail tab before classDef", "Flows", strings.Replace(formatMermaid, "  classDef new", "\tclassDef new", 1), "P10", []string{"classDef on line 6 is not allowed"}},
	{"P10 fail space after classDef line", "Flows", strings.Replace(formatMermaid, "stroke-width:2px\n  classDef changed", "stroke-width:2px \n  classDef changed", 1), "P10",
		[]string{"classDef on line 6 is not allowed"}},

	// P11: legacy Guardrail check section
	{"P11 pass empty body", "Guardrail check", "", "", nil},
	{"P11 fail blank body", "Guardrail check", "\n\n", "P11", []string{"only an empty body deletes this section"}},
	{"P11 fail body", "Guardrail check", "| Proposal | Result |\n", "P11", []string{"only an empty body deletes this section"}},

	// Sections with no rule of their own
	{"Goal has P1 to P3 only", "Goal", "Any prose. | not a table\n- not a bullet check\n", "", nil},
}

// TestPreplanFormatRules checks the rules P1 to P11. Each rule has one case
// that passes and one that fails. A failing case names the rule, and the
// Detail of its error holds the expected text.
func TestPreplanFormatRules(t *testing.T) {
	covered := map[string]bool{} // "P4 pass", "P4 fail", ...
	for _, tc := range formatRuleCases {
		if f := strings.Fields(tc.name); len(f) > 2 && strings.HasPrefix(f[0], "P") && (f[1] == "pass" || f[1] == "fail") {
			covered[f[0]+" "+f[1]] = true
			if (f[1] == "fail") != (tc.rule != "") || (tc.rule != "" && f[0] != tc.rule) {
				t.Errorf("case %q: its name and its rule %q disagree", tc.name, tc.rule)
			}
		}
		t.Run(tc.name, func(t *testing.T) {
			err := preplanCheckSection(tc.section, tc.body)
			if tc.rule == "" {
				if err != nil {
					t.Fatalf("preplanCheckSection(%q) = %v, want nil", tc.section, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("preplanCheckSection(%q) = nil, want rule %s", tc.section, tc.rule)
			}
			if err.Rule != tc.rule || err.Section != tc.section {
				t.Errorf("error = {%s %s}, want {%s %s}", err.Section, err.Rule, tc.section, tc.rule)
			}
			if want := `Section "` + tc.section + `" breaks rule ` + tc.rule + ": " + err.Detail; err.Error() != want {
				t.Errorf("Error() = %q, want %q", err.Error(), want)
			}
			if len(tc.detail) == 0 {
				t.Errorf("case %q has no expected detail; Detail = %q", tc.name, err.Detail)
			}
			for _, want := range tc.detail {
				if !strings.Contains(err.Detail, want) {
					t.Errorf("Detail = %q, want it to hold %q", err.Detail, want)
				}
			}
		})
	}
	for i := 1; i <= 11; i++ {
		for _, kind := range []string{"pass", "fail"} {
			if key := "P" + strconv.Itoa(i) + " " + kind; !covered[key] {
				t.Errorf("the table has no %q case", key)
			}
		}
	}
}

// formatDecisionsCase is one ## Decisions body and its expected counts.
type formatDecisionsCase struct {
	name, body string
	want       preplanDecisionsSummary
}

// formatDecisionsCases is the table of the decisions part of
// TestPreplanFormatSummary.
var formatDecisionsCases = []formatDecisionsCase{
	{"blank body", "", preplanDecisionsSummary{}},
	{"no header", "| 1 | A | B |\n", preplanDecisionsSummary{}},
	{"header only", formatDecisionsHead, preplanDecisionsSummary{}},
	{"live rows", formatRows("| 1 | A | B |", "| 2 | C | D |"), preplanDecisionsSummary{Live: 2}},
	{"dropped row", formatRows("| 1 | A | B |", "| 2 | Dropped: C | R. Dropped by: G. |"), preplanDecisionsSummary{Live: 1, Dropped: 1}},
	{"moved row counts in no count", formatRows("| 1 | A | B |", "| 2 | Moved: C | R |"), preplanDecisionsSummary{Live: 1}},
	{"kept row is live and kept", formatRows("| 1 | A | R. Kept with warning: G1. |", "| 2 | C | D |"), preplanDecisionsSummary{Live: 2, Kept: 1}},
	{"kept note on a dropped row is not kept", formatRows("| 1 | Dropped: A | Kept with warning: G1. Dropped by: G2. |"), preplanDecisionsSummary{Dropped: 1}},
	{"kept note on a moved row is not kept", formatRows("| 1 | Moved: A | Kept with warning: G1. |"), preplanDecisionsSummary{}},
	{"cut all", formatRows("| 1 | A | B |") + "\nSmallest cut: all.\n", preplanDecisionsSummary{Live: 1, CutSet: true}},
	{"cut ids", formatRows("| 1 | A | B |", "| 2 | C | D |") + "\nSmallest cut: #1, #2.\n", preplanDecisionsSummary{Live: 2, CutSet: true}},
	{"invalid cut line is not a cut", formatRows("| 1 | A | B |") + "\nSmallest cut: soon.\n", preplanDecisionsSummary{Live: 1}},
	{"escaped pipe stays in the cell", formatRows(`| 1 | a \| b | R |`), preplanDecisionsSummary{Live: 1}},
	{"leading blank line", "\n" + formatRows("| 1 | A | B |"), preplanDecisionsSummary{Live: 1}},
	{"short row is skipped", formatRows("| 1 | A |", "| 2 | C | D |"), preplanDecisionsSummary{Live: 1}},
	{"short dropped row is skipped", formatRows("| 1 | Dropped: A |", "| 2 | C | D |"), preplanDecisionsSummary{Live: 1}},
}

// formatTextCase is one body and the text that a parser returns for it.
type formatTextCase struct{ name, body, want string }

// formatGoalCases is the table of the goal part of TestPreplanFormatSummary.
var formatGoalCases = []formatTextCase{
	{"blank", "\n\n", ""},
	{"one sentence", "\nA user drafts ideas.\n", "A user drafts ideas."},
	{"first of two sentences", "First one. Second one.\n", "First one."},
	{"sentence break across a line", "First one.\nSecond one.\n", "First one."},
	{"no period: the whole paragraph", "A goal with no period\nthat runs on\n", "A goal with no period that runs on"},
	{"only the first paragraph", "First paragraph\n\nSecond paragraph. More.\n", "First paragraph"},
	{"period with no space after it does not cut", "Version 1.5 ships. Next.\n", "Version 1.5 ships."},
	{"leading blank lines and spaces", "\n\n   Spaced goal.  \n", "Spaced goal."},
	{"CRLF", "First one.\r\nSecond one.\r\n", "First one."},
}

// formatCountCase is one Open questions body and its bullet count.
type formatCountCase struct {
	name, body string
	want       int
}

// formatQuestionCases is the table of the open questions part of
// TestPreplanFormatSummary.
var formatQuestionCases = []formatCountCase{
	{"blank", "", 0},
	{"two bullets", "- A?\n\n- B?\n", 2},
	{"three bullets, CRLF", "- A?\r\n- B?\r\n- C?\r\n", 3},
	{"text is not a bullet", "A?\n-B?\n", 0},
}

// formatCheckLine is a valid check line with the time 2026-10-29T07:58:25Z.
const formatCheckLine = "**Guardrail check:** 57 guardrails, 3 proposals: 3 pass, 0 kept, 0 dropped. Last check: 2026-10-29T07:58:25Z."

// formatLastCheckCase is one head and the result of preplanLastCheck.
type formatLastCheckCase struct {
	name, head string
	want       time.Time
	ok         bool
}

// formatUTC is the time that formatCheckLine holds.
var formatUTC = time.Date(2026, 10, 29, 7, 58, 25, 0, time.UTC)

// formatLastCheckCases is the table of the last check part of
// TestPreplanFormatSummary.
var formatLastCheckCases = []formatLastCheckCase{
	{"check line", "**Status:** s\n\n" + formatCheckLine + "\n\n", formatUTC, true},
	{"time in another zone gives UTC", "**Guardrail check:** 1 guardrails, 0 proposals: 0 pass, 0 kept, 0 dropped. Last check: 2026-10-29T09:58:25+02:00.\n", formatUTC, true},
	{"time with fractional seconds", "**Guardrail check:** 1 guardrails, 0 proposals: 0 pass, 0 kept, 0 dropped. Last check: 2026-10-29T07:58:25.5Z.\n",
		formatUTC.Add(500 * time.Millisecond), true},
	{"guardrails unavailable suffix", formatCheckLine + " Guardrails unavailable: no config.\n", formatUTC, true},
	{"CRLF", "**Status:** s\r\n\r\n" + formatCheckLine + "\r\n", formatUTC, true},
	{"line with no end", formatCheckLine, formatUTC, true},
	{"first line wins", formatCheckLine + "\n**Guardrail check:** 1 guardrails, 0 proposals: 0 pass, 0 kept, 0 dropped. Last check: 2027-01-01T00:00:00Z.\n", formatUTC, true},
	{"no check line", "**Status:** s\n\n**Started:** 2026-10-29T07:44:07Z\n", time.Time{}, false},
	{"empty head", "", time.Time{}, false},
	{"legacy section is not a check line", "**Status:** s\n\n## Guardrail check\n\n| Proposal | Result |\n|---|---|\n| 1 | pass |\n", time.Time{}, false},
	{"check line with no time", "**Guardrail check:** 1 guardrails, 0 proposals: 0 pass, 0 kept, 0 dropped.\n", time.Time{}, false},
	{"check line with a bad time", "**Guardrail check:** x. Last check: 2026-13-45T99:00:00Z.\n", time.Time{}, false},
}

// TestPreplanFormatSummary checks the summary parsers: the Decisions counts,
// the Goal sentence, the open question count and the check line.
func TestPreplanFormatSummary(t *testing.T) {
	t.Run("decisions", func(t *testing.T) {
		for _, tc := range formatDecisionsCases {
			t.Run(tc.name, func(t *testing.T) {
				if got := preplanSummarizeDecisions(tc.body); got != tc.want {
					t.Errorf("preplanSummarizeDecisions = %+v, want %+v", got, tc.want)
				}
			})
		}
	})
	t.Run("goal", func(t *testing.T) {
		for _, tc := range formatGoalCases {
			t.Run(tc.name, func(t *testing.T) {
				if got := preplanGoalSentence(tc.body); got != tc.want {
					t.Errorf("preplanGoalSentence(%q) = %q, want %q", tc.body, got, tc.want)
				}
			})
		}
	})
	t.Run("open questions", func(t *testing.T) {
		for _, tc := range formatQuestionCases {
			t.Run(tc.name, func(t *testing.T) {
				if got := preplanOpenQuestionCount(tc.body); got != tc.want {
					t.Errorf("preplanOpenQuestionCount(%q) = %d, want %d", tc.body, got, tc.want)
				}
			})
		}
	})
	t.Run("last check", func(t *testing.T) {
		for _, tc := range formatLastCheckCases {
			t.Run(tc.name, func(t *testing.T) {
				got, ok := preplanLastCheck(tc.head)
				if ok != tc.ok || !got.Equal(tc.want) {
					t.Fatalf("preplanLastCheck = (%v, %v), want (%v, %v)", got, ok, tc.want, tc.ok)
				}
				if ok && got.Location() != time.UTC {
					t.Errorf("location = %v, want UTC", got.Location())
				}
			})
		}
	})
}

// formatCRLFBodies are the bodies that TestPreplanFormatCRLF reads in LF and
// in CRLF form. Passing and failing bodies are in the list.
var formatCRLFBodies = []struct{ section, body string }{
	{"Goal", fenced("A goal. Next.\n\n'''\ncode\n'''\n")},
	{"Goal", fenced("text\n'''\ncode\n")},
	{"Goal", "ok\n# Heading\n"},
	{"Decisions", formatRows("| 1 | A | B |", "| 2 | Dropped: C | Dropped by: G. |") + "\nSmallest cut: #1.\n"},
	{"Decisions", formatRows("| 1 | A | B |") + "\nSmallest cut: #9.\n"},
	{"Decisions", formatRows("| x | A | B |")},
	{"Non-goals", "- One.\n- Two.\n"},
	{"Non-goals", "None stated.\n"},
	{"Non-goals", "prose\n"},
	{"Open questions", "- A?\n- B?\n"},
	{"Open questions", "- A?\nB?\n"},
	{"Flows", "\n" + formatMermaid},
	{"Flows", strings.Replace(formatMermaid, "#8a6d00", "#000000", 1)},
	{"Flows", "No flow change.\n"},
	{"Guardrail check", "\n\n"},
	{"Guardrail check", "row\n"},
}

// TestPreplanFormatCRLF checks that a body with CRLF line ends gives the same
// result as the same body with LF line ends, for the rules and the parsers.
func TestPreplanFormatCRLF(t *testing.T) {
	for _, tc := range formatCRLFBodies {
		lf := tc.body
		crlf := strings.ReplaceAll(lf, "\n", "\r\n")
		t.Run(tc.section+"/"+strings.TrimSpace(lf[:min(len(lf), 20)]), func(t *testing.T) {
			if a, b := preplanCheckSection(tc.section, lf), preplanCheckSection(tc.section, crlf); !reflect.DeepEqual(a, b) {
				t.Errorf("check: LF gives %v, CRLF gives %v", a, b)
			}
			if a, b := preplanSummarizeDecisions(lf), preplanSummarizeDecisions(crlf); a != b {
				t.Errorf("decisions summary: LF gives %+v, CRLF gives %+v", a, b)
			}
			if a, b := preplanGoalSentence(lf), preplanGoalSentence(crlf); a != b {
				t.Errorf("goal: LF gives %q, CRLF gives %q", a, b)
			}
			if a, b := preplanOpenQuestionCount(lf), preplanOpenQuestionCount(crlf); a != b {
				t.Errorf("open questions: LF gives %d, CRLF gives %d", a, b)
			}
		})
	}

	const check = "**Status:** s\n\n" + formatCheckLine + "\n"
	at, ok := preplanLastCheck(check)
	atCRLF, okCRLF := preplanLastCheck(strings.ReplaceAll(check, "\n", "\r\n"))
	if !ok || !okCRLF || !at.Equal(atCRLF) {
		t.Errorf("last check: LF gives (%v, %v), CRLF gives (%v, %v)", at, ok, atCRLF, okCRLF)
	}
}

// TestPreplanFormatClassDefParity checks that preplanAllowedClassDefs equals
// the set of classDef lines of the preplan SKILL.md. It finds the lines by
// text, not by line number, so an edit above them cannot break the test.
func TestPreplanFormatClassDefParity(t *testing.T) {
	b, err := os.ReadFile("../../plugins/sdlc/skills/preplan/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "classDef ") {
			seen[line] = true
		}
	}
	var got []string
	for line := range seen {
		got = append(got, line)
	}
	want := slices.Clone(preplanAllowedClassDefs)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) == 0 {
		t.Fatal("SKILL.md holds no classDef line")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("classDef lines of SKILL.md = %q, want preplanAllowedClassDefs %q", got, want)
	}
}

// formatRealTopicFile is a topic file of the real shape: head lines, the
// seven skeleton sections, a Flows section with a mermaid fence, a fenced
// block that holds a "## " line, a Decisions table with a dropped, a moved and
// a kept row, a Smallest cut line and an Open questions section.
var formatRealTopicFile = fenced(`# Preplan: sample topic

**Status:** ready for plan

**Started:** 2026-10-29T07:44:07Z

**Guardrail check:** 57 guardrails, 4 proposals: 2 pass, 1 kept, 1 dropped. Last check: 2026-10-29T07:58:25Z.

## Goal

A user drafts many ideas with preplan, also in plan mode. A second sentence adds detail.

## Problem and benefit

Root problem: preplan does not work in plan mode.

'''text
## A heading inside a fence is text
'''

- Who gains: the developer who drafts several ideas.
- Target: 0 opened files.

## Users and effect

| User | What changes | What fails today |
|---|---|---|
| Developer | Drafts ideas in plan mode | The skill cannot write the file |

## Risk if wrong

| Failure | Who fails | Effect |
|---|---|---|
| The summary shows wrong content | Reader | A wrong idea is picked |

## Non-goals

- Offered, not picked: Remove the Preplans tab.
- Full topic file viewer in the dashboard.

## Flows

### Flow 1: draft an idea (proposal #1)

'''mermaid
flowchart LR
  subgraph Before
    B1[User runs preplan] --> B2[Skill edits file: refused]
  end
  subgraph After
    A1[User runs preplan] --> A2[Tool writes the section]:::changed
    A2 --> A3[Tool refuses bad content]:::new
  end
  classDef new fill:#1f7a3a,stroke:#0b3d1c,color:#ffffff,stroke-width:2px
  classDef changed fill:#8a6d00,stroke:#4a3a00,color:#ffffff,stroke-width:2px
'''

Before:
1. The user runs preplan.
2. The skill edits the file. Plan mode refuses.

After:
1. The user runs preplan.
2. The tool writes the section (changed).
3. The tool refuses bad content (new).

## Decisions

| # | Decision | Reason |
|---|---|---|
| 1 | A new action writes each section and checks its format. | Plan mode permits tool writes; see ` + "`plan/SKILL.md:220`" + `. |
| 2 | Dropped: Remove the Preplans tab. | Out of scope. Dropped by: G12. |
| 3 | Moved: Plan from several ideas. | Asked later. |
| 4 | The Activity tab gets a Preplans box. | Readers compare ideas. Kept with warning: G7. |

Smallest cut: #1, #4.

## Open questions

- Is the plan from several ideas part of this change?
`)

// TestPreplanFormatRealTopicFile checks that a valid topic file of the real
// shape passes every section, and that the parsers read it. It splits the file
// with preplanSectionSpans, as the dashboard summary will.
func TestPreplanFormatRealTopicFile(t *testing.T) {
	data := []byte(formatRealTopicFile)
	spans, open := preplanSectionSpans(data)
	if open != "" {
		t.Fatalf("open fence = %q, want none", open)
	}
	var names []string
	bodies := map[string]string{}
	for _, s := range spans {
		names = append(names, s.name)
		bodies[s.name] = string(data[s.bodyStart:s.bodyEnd])
		if err := preplanCheckSection(s.name, bodies[s.name]); err != nil {
			t.Errorf("section %q fails: %v", s.name, err)
		}
		if !slices.Contains(preplanWriteSections, s.name) {
			t.Errorf("section %q is not in preplanWriteSections", s.name)
		}
	}
	want := append(slices.Clone(preplanSections), "Open questions")
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("sections = %q, want %q", names, want)
	}

	if got, wantSum := preplanSummarizeDecisions(bodies["Decisions"]), (preplanDecisionsSummary{Live: 2, Kept: 1, Dropped: 1, CutSet: true}); got != wantSum {
		t.Errorf("decisions summary = %+v, want %+v", got, wantSum)
	}
	if got, wantGoal := preplanGoalSentence(bodies["Goal"]), "A user drafts many ideas with preplan, also in plan mode."; got != wantGoal {
		t.Errorf("goal = %q, want %q", got, wantGoal)
	}
	if got := preplanOpenQuestionCount(bodies["Open questions"]); got != 1 {
		t.Errorf("open questions = %d, want 1", got)
	}
	at, ok := preplanLastCheck(string(preplanHead(data)))
	if !ok || !at.Equal(formatUTC) {
		t.Errorf("last check = (%v, %v), want %v", at, ok, formatUTC)
	}

	// The same file with CRLF line ends passes too.
	crlf := strings.ReplaceAll(formatRealTopicFile, "\n", "\r\n")
	crlfSpans, _ := preplanSectionSpans([]byte(crlf))
	for _, s := range crlfSpans {
		if err := preplanCheckSection(s.name, crlf[s.bodyStart:s.bodyEnd]); err != nil {
			t.Errorf("CRLF section %q fails: %v", s.name, err)
		}
	}
}

// formatFenceStepCases is the table of TestPreplanFenceStep. A fence opens at
// a column-0 run of 3 or more backticks or tildes, and it closes at a line
// that holds only a run of the same character, at least as long, then spaces
// or tabs.
var formatFenceStepCases = []struct{ name, open, line, want string }{
	{"text outside a fence", "", "text\n", ""},
	{"backtick fence opens", "", "```\n", "```"},
	{"backtick fence opens with an info string", "", "```go\n", "```"},
	{"tilde fence opens", "", "~~~\n", "~~~"},
	{"long run opens", "", "~~~~~ x\n", "~~~~~"},
	{"two backticks do not open", "", "``\n", ""},
	{"indented run does not open", "", " ```\n", ""},
	{"line with no line end opens", "", "```", "```"},
	{"text inside a fence", "```", "code\n", "```"},
	{"heading inside a fence", "```", "## Heading\n", "```"},
	{"same run closes", "```", "```\n", ""},
	{"longer run closes", "```", "````\n", ""},
	{"CRLF closes", "```", "```\r\n", ""},
	{"trailing spaces and tabs close", "```", "``` \t\n", ""},
	{"tilde closes tilde", "~~~", "~~~\n", ""},
	{"other character does not close", "```", "~~~\n", "```"},
	{"shorter run does not close", "````", "```\n", "````"},
	{"run with an info string does not close", "```", "```go\n", "```"},
	{"run with text after it does not close", "```", "``` x\n", "```"},
	{"indented run does not close", "```", " ```\n", "```"},
}

// TestPreplanFenceStep checks the fence state after one line, and over a
// sequence of lines with nested runs.
func TestPreplanFenceStep(t *testing.T) {
	for _, tc := range formatFenceStepCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := preplanFenceStep(tc.open, tc.line); got != tc.want {
				t.Errorf("preplanFenceStep(%q, %q) = %q, want %q", tc.open, tc.line, got, tc.want)
			}
		})
	}

	t.Run("nested runs", func(t *testing.T) {
		// A ```` fence holds a ``` line: the short run opens nothing and closes nothing.
		lines := []struct{ line, want string }{
			{"````md\n", "````"},
			{"```\n", "````"},
			{"## text\n", "````"},
			{"```\n", "````"},
			{"````\n", ""},
			{"## outside\n", ""},
		}
		open := ""
		for i, l := range lines {
			open = preplanFenceStep(open, l.line)
			if open != l.want {
				t.Fatalf("after line %d (%q) open = %q, want %q", i+1, l.line, open, l.want)
			}
		}
	})
}

// TestPreplanFormatEdgeIndentedRow checks a Decisions table whose lines start
// with spaces or a tab. It reads like the plain table. A damaged indented row
// is named in the error detail.
func TestPreplanFormatEdgeIndentedRow(t *testing.T) {
	const cut = "\nSmallest cut: #1.\n"
	rows := []string{"| 1 | A | R. Kept with warning: G1. |", "| 2 | Dropped: B | Dropped by: G2. |"}
	plain := formatRows(rows...) + cut
	want := preplanDecisionsSummary{Live: 1, Kept: 1, Dropped: 1, CutSet: true}
	wantFormatPass(t, "Decisions", plain)
	if got := preplanSummarizeDecisions(plain); got != want {
		t.Fatalf("plain summary = %+v, want %+v", got, want)
	}

	for name, indent := range map[string]string{"two spaces": "  ", "four spaces": "    ", "tab": "\t"} {
		t.Run(name, func(t *testing.T) {
			head := indent + "| # | Decision | Reason |\n" + indent + "|---|---|---|\n"
			indented := head + indent + rows[0] + "\n" + indent + rows[1] + "\n" + cut
			wantFormatPass(t, "Decisions", indented)
			if got := preplanSummarizeDecisions(indented); got != want {
				t.Errorf("indented summary = %+v, want the plain summary %+v", got, want)
			}

			wantFormatError(t, "Decisions", head+indent+"| x | A | B |\n", "P5", `row 1 has "#" value "x"`)
			wantFormatError(t, "Decisions", head+indent+"| 1 | A |\n", "P5", "row 1 has 2 cells, want 3")
			wantFormatError(t, "Decisions", head+indent+rows[0]+"\n"+indent+"| 2 | Dropped: B | R |\n", "P6", "row 2")
			wantFormatError(t, "Decisions", head+indent+rows[0]+"\n"+indent+"Notes.\n", "P7", "line 4 is not blank")
			wantFormatError(t, "Decisions", indent+"| # | Decision |\n"+indent+"|---|---|\n", "P4", "the table header is missing")
		})
	}
}

// TestPreplanFormatEdgeNoTrailingLF checks bodies that end with no line feed.
// Each reads like the same body with a line feed. A damaged last line is
// named in the error detail.
func TestPreplanFormatEdgeNoTrailingLF(t *testing.T) {
	plain := formatRows("| 1 | A | B |") + "\nSmallest cut: #1.\n"
	noLF := strings.TrimSuffix(plain, "\n")
	wantFormatPass(t, "Decisions", plain)
	wantFormatPass(t, "Decisions", noLF)
	want := preplanDecisionsSummary{Live: 1, CutSet: true}
	for name, body := range map[string]string{"plain": plain, "no line feed": noLF} {
		if got := preplanSummarizeDecisions(body); got != want {
			t.Errorf("%s: summary = %+v, want %+v", name, got, want)
		}
	}

	headOnly := strings.TrimSuffix(formatDecisionsHead, "\n")
	wantFormatPass(t, "Decisions", headOnly)
	if got := preplanSummarizeDecisions(headOnly); got != (preplanDecisionsSummary{}) {
		t.Errorf("header only: summary = %+v, want the zero value", got)
	}
	if got, wantRows := preplanSummarizeDecisions(strings.TrimSuffix(formatRows("| 1 | A | B |"), "\n")), (preplanDecisionsSummary{Live: 1}); got != wantRows {
		t.Errorf("last row with no line feed: summary = %+v, want %+v", got, wantRows)
	}

	wantFormatError(t, "Decisions", strings.TrimSuffix(formatRows("| x | A | B |"), "\n"), "P5", `row 1 has "#" value "x"`)
	wantFormatError(t, "Decisions", formatRows("| 1 | A | B |")+"\nSmallest cut: #9.", "P7", "#9 is not a live row")
	wantFormatError(t, "Decisions", "| # | Decision | Reason |", "P4", "the table header is missing")

	for _, tc := range []struct{ section, body, rule, detail string }{
		{"Non-goals", "- One.", "", ""},
		{"Non-goals", "One.", "P8", "line 1 is not a bullet"},
		{"Open questions", "- A?\n- B?", "", ""},
		{"Open questions", "- A?\nB?", "P9", "line 2 is not a bullet"},
		{"Flows", strings.TrimSuffix(formatMermaid, "\n"), "", ""},
		{"Flows", strings.Replace(strings.TrimSuffix(formatMermaid, "\n"), "#8a6d00", "#8a6d01", 1), "P10", "classDef on line 7"},
		{"Guardrail check", "", "", ""},
		{"Guardrail check", "row", "P11", "only an empty body"},
	} {
		if tc.rule == "" {
			wantFormatPass(t, tc.section, tc.body)
		} else {
			wantFormatError(t, tc.section, tc.body, tc.rule, tc.detail)
		}
	}
	if got := preplanGoalSentence("A goal. More"); got != "A goal." {
		t.Errorf("goal with no line feed = %q, want %q", got, "A goal.")
	}
	if got := preplanOpenQuestionCount("- A?"); got != 1 {
		t.Errorf("open questions with no line feed = %d, want 1", got)
	}
}

// TestPreplanFormatEdgeUnclosedFence checks a code fence that does not close.
// P3 names the line that opened it, also when the last line has no line feed
// and when a "## " line inside the fence would otherwise be a heading. The
// closed form of each body passes, and preplanSectionSpans agrees on which
// fence is open.
func TestPreplanFormatEdgeUnclosedFence(t *testing.T) {
	flowsOpen := formatMermaid[:strings.Index(formatMermaid, "```\n\nAfter:")]
	for _, tc := range []struct{ name, section, body, rule, detail string }{
		{"closed, no line feed", "Goal", "text\n```\ncode\n```", "", ""},
		{"unclosed", "Goal", "text\n```\ncode\n", "P3", "the fence on line 2 does not close"},
		{"unclosed, no line feed", "Goal", "text\n```\ncode", "P3", "the fence on line 2 does not close"},
		{"opening line is the last line", "Goal", "text\n```", "P3", "the fence on line 2 does not close"},
		{"heading in the fence is code, not P2", "Goal", "```\n## Hidden\n", "P3", "the fence on line 1 does not close"},
		{"heading before the fence is still P2", "Goal", "## Seen\n```\ncode\n", "P2", "line 1 starts a heading"},
		{"tilde fence unclosed", "Goal", "~~~\ncode\n", "P3", "the fence on line 1 does not close"},
		{"closed by the wrong character", "Goal", "```\ncode\n~~~\n", "P3", "the fence on line 1 does not close"},
		{"Decisions: table then fence", "Decisions", formatRows("| 1 | A | B |") + "\n```\nnote\n", "P3", "the fence on line 5 does not close"},
		{"Flows: mermaid closed", "Flows", formatMermaid, "", ""},
		{"Flows: mermaid unclosed", "Flows", flowsOpen, "P3", "the fence on line 1 does not close"},
		{"Flows: mermaid closed by a shorter run", "Flows", "````mermaid\nflowchart LR\n```\n", "P3", "the fence on line 1 does not close"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.rule == "" {
				wantFormatPass(t, tc.section, tc.body)
			} else {
				wantFormatError(t, tc.section, tc.body, tc.rule, tc.detail)
			}
		})
	}

	// preplanSectionSpans reads the same fence as open or closed.
	if _, open := preplanSectionSpans([]byte("## Goal\n\ntext\n```\ncode\n")); open != "```" {
		t.Errorf("preplanSectionSpans open fence = %q, want ```", open)
	}
	if _, open := preplanSectionSpans([]byte("## Goal\n\ntext\n```\ncode\n```")); open != "" {
		t.Errorf("preplanSectionSpans open fence = %q, want none", open)
	}
}

// formatSplitRowCases is the table of TestPreplanSplitRow. Each case runs one
// branch of preplanSplitRow.
var formatSplitRowCases = []struct {
	name, row string
	want      []string
}{
	{"outer pipes", "| 1 | A | B |", []string{"1", "A", "B"}},
	{"no outer pipes", "1 | A | B", []string{"1", "A", "B"}},
	{"no closing pipe", "| 1 | A | B", []string{"1", "A", "B"}},
	{"escaped pipe stays in the cell", `| 1 | A \| B | R |`, []string{"1", `A \| B`, "R"}},
	{"last cell ends in an escaped pipe, no closing pipe", `| 1 | A | B \|`, []string{"1", "A", `B \|`}},
	{"trailing backslash is text", `| 1 | A | B \`, []string{"1", "A", `B \`}},
	{"empty line gives one empty cell", "", []string{""}},
}

// TestPreplanSplitRow checks the cells that preplanSplitRow returns for each
// branch of its closing-pipe and escape rules.
func TestPreplanSplitRow(t *testing.T) {
	for _, tc := range formatSplitRowCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := preplanSplitRow(tc.row); !slices.Equal(got, tc.want) {
				t.Errorf("preplanSplitRow(%q) = %q, want %q", tc.row, got, tc.want)
			}
		})
	}
}

// TestPreplanLastCheckReadsCheckLine feeds the line that preplanCheckLine
// writes into preplanLastCheck, with and without the guardrail warning, so
// the writer and the reader of the "Last check:" text cannot drift apart.
func TestPreplanLastCheckReadsCheckLine(t *testing.T) {
	at := time.Date(2026, 10, 29, 7, 58, 25, 0, time.UTC)
	setPreplanNow(t, at)
	for _, warning := range []string{"", "config not read"} {
		line := preplanCheckLine(57, 3, 1, 0, warning)
		got, ok := preplanLastCheck("**Status:** in progress\n\n" + line + "\n")
		if !ok || !got.Equal(at) {
			t.Errorf("preplanLastCheck(%q) = (%v, %v), want (%v, true)", line, got, ok, at)
		}
	}
}

// structureIdeaVerificationTopic is a copy of the topic file
// 2026-11-13-idea-verification.md of this change. It is a real file that
// passes every structure rule. Each backtick that is not part of a fence is a
// quoted string in the concatenation, because a raw string holds no backtick.
var structureIdeaVerificationTopic = fenced(`# Preplan: Idea verification

**Status:** ready for plan

**Started:** 2026-11-13T15:57:30Z

**Guardrail check:** 60 guardrails, 9 proposals: 8 pass, 1 kept, 0 dropped. Last check: 2026-11-13T16:23:58Z.

## Goal

A run with no dialogue challenges incoming requirements. The requirements come from a topic file, another requirements file, a Jira ticket or a GitHub issue. The run reports only findings: open questions, guardrail violations, inconsistencies between proposals, flows and decisions, and conflicts with OpenSpec. It changes nothing and asks no question.

## Problem and benefit

Root problem: requirements reach ` + "`" + `/sdlc:plan` + "`" + ` with gaps and contradictions. Nothing checks them before the plan run starts, so the plan run finds them late or does not find them.

Who gains: a person who has requirements in a topic file, another requirements file, a Jira ticket or a GitHub issue, and wants to start ` + "`" + `/sdlc:plan` + "`" + `.

Measure: the number of requirement problems that the check reports before a plan run starts.

Baseline: 0 for a run with no person, because no such check exists today.

Cost of no change: a plan run starts on requirements with gaps. The gaps show late, in the plan run or after it, or they stay.

## Users and effect

User: a person who has requirements in a topic file, another requirements file, a Jira ticket or a GitHub issue, and runs the new skill by hand, before ` + "`" + `/sdlc:plan` + "`" + `.

What changes: the person gets one report with the problems of the requirements. The person answers no question, and the source of the requirements stays as it is.

What fails today: the person must read the whole text to find gaps, or must start a preplan dialogue, which changes the status and the topic file. If the person does neither, the plan run starts with the gaps.

## Risk if wrong

Worst failure: the report shows a problem that does not exist. The person who runs the check loses time, or changes requirements that were correct.

Lesser failure: the report shows no problem, but the requirements hold one. The person starts the plan run with the gap, as today.

## Non-goals

- Offered, not picked: a headless mode of ` + "`" + `/sdlc:preplan` + "`" + `.
- Offered, not picked: ` + "`" + `/sdlc:plan` + "`" + ` starts the check at its start for each topic file.

## Flows

'''mermaid
flowchart LR
  subgraph Before
    B1["Person has requirements: topic file, requirements file, Jira ticket or GitHub issue"] --> B2["Person reads the text by hand, or does no check"]
    B2 --> B3["Person starts the plan run"]
  end
  subgraph After
    A1["Person has requirements: topic file, requirements file, Jira ticket or GitHub issue"] --> A2["Person runs /sdlc:verify-requirements"]:::new
    A2 --> A3["Skill reads the requirements, the plan guardrails and OpenSpec"]:::new
    A3 --> A4["Skill prints the report of findings"]:::new
    A4 --> A5["Person corrects the requirements, or starts the plan run"]:::changed
  end
  classDef new fill:#1f7a3a,stroke:#0b3d1c,color:#ffffff,stroke-width:2px
  classDef changed fill:#8a6d00,stroke:#4a3a00,color:#ffffff,stroke-width:2px
'''

Before:
1. The person has requirements in a topic file, a requirements file, a Jira ticket or a GitHub issue.
2. The person reads the text by hand, or does no check.
3. The person starts the plan run with ` + "`" + `/sdlc:plan` + "`" + `.

After:
1. The person has requirements in a topic file, a requirements file, a Jira ticket or a GitHub issue.
2. The person runs ` + "`" + `/sdlc:verify-requirements` + "`" + ` with one input and states its kind (new).
3. The skill reads the requirements, the plan guardrails and OpenSpec. It changes no file, no ticket and no issue, and asks no question (new).
4. The skill prints the report of findings (new).
5. The person corrects the requirements at their source, for a topic file in ` + "`" + `/sdlc:preplan` + "`" + `, or starts the plan run with ` + "`" + `/sdlc:plan` + "`" + ` (changed: the person decides with the report).

## Decisions

| # | Decision | Reason |
|---|---|---|
| 1 | A new skill verifies one topic file with no dialogue. It is separate from ` + "`" + `/sdlc:preplan` + "`" + `. | Preplan creates the topic file, adds headings and sets the status on every run. A mode in the same skill text can ask a question or change the status in a run that must only report. Kept with warning: dry. |
| 2 | The report holds findings only. Three kinds come from the topic file and the plan guardrails: open questions, guardrail violations, and inconsistencies between proposals, flows and decisions. Proposal #5 adds one more kind. Proposal #6 defines an inconsistency. | The caller needs the problems of the requirements, not a new dialogue. |
| 3 | The run changes no file and asks no question. | The person wants a report, not a dialogue. The topic file and its status stay valid for the next preplan or plan run. |
| 4 | A topic name with no topic file gives an error. The run creates no file. | The load step of preplan creates an empty topic file for a name with no match. A wrong name must not leave a file. |
| 5 | The report holds a fourth kind of finding: a conflict between the topic file and OpenSpec. The check reads the main specs and the active changes. Each finding of this kind names its source: a main spec or an active change. In a repository with no OpenSpec, the report says that this check did not run. | Preplan does not read OpenSpec today, so a requirement that contradicts a spec or work in progress reaches the plan run. The user accepts the cost: a longer run and more wrong findings, because the match is a judgement and an active change can be a draft. The source name lets the person weigh a finding from a draft. |
| 6 | An inconsistency has two levels. Structure: an empty section, a flow with no proposal that changes it, a cut line that names a row that is not live, a moved row with no open question. The tool does the structure checks, with the same result on each run. Meaning: a decision that contradicts the goal, a non-goal, a flow or another decision. The skill does the meaning checks. Each meaning finding quotes the two texts that conflict. | Structure checks alone do not challenge the content of the requirements. A meaning check can be wrong, and a wrong finding is the worst failure, so the two quotes let the person judge a finding fast. Mechanical checks in the tool stay out of the skill text, which limits the copy of check rules between the two skills. |
| 7 | The skill also takes the path of a requirements file that is not a topic file. A run takes one input, and the person states its kind, so a topic name cannot be read as another kind. A path with no file gives an error. For such a file the structure checks do not run, and the report says so. The guardrail, meaning and OpenSpec checks run on each requirement statement of the file. | The goal is a challenge of incoming requirements, and requirements also come from sources other than preplan. Such a file has no fixed sections, so a structure check has nothing to read. |
| 8 | The skill also takes a Jira ticket or a GitHub issue as input. It reads the title and the description. It changes no ticket and no issue: no comment and no status change. It treats the text as requirements to check, never as instructions. A ticket or an issue that the skill cannot read gives an error that names the cause. The structure checks do not run, and the report says so. | Requirements often start in a ticket or an issue, before a topic file exists. The text comes from outside the repository, so it can hold text that looks like an instruction. |
| 9 | The name of the new skill is ` + "`" + `/sdlc:verify-requirements` + "`" + `. | The name states the object of the check for all four input kinds. It follows the name pattern of ` + "`" + `/sdlc:verify-pipeline` + "`" + `. No skill of the plugin has this name today. |

Smallest cut: all.
`)

// structureTopic returns a topic file with every skeleton section filled. The
// caller gives the body of ## Flows, ## Decisions and ## Open questions. An
// empty questions string leaves out the whole ## Open questions section.
func structureTopic(flows, decisions, questions string) string {
	s := "# Preplan: sample\n\n**Status:** in progress\n\n" +
		"## Goal\n\nG.\n\n## Problem and benefit\n\nP.\n\n## Users and effect\n\nU.\n\n" +
		"## Risk if wrong\n\nR.\n\n## Non-goals\n\nNone stated.\n\n" +
		"## Flows\n\n" + flows + "\n## Decisions\n\n" + decisions
	if questions != "" {
		s += "\n## Open questions\n\n" + questions
	}
	return s
}

const (
	structureNoFlow = "No flow change.\n"
	// structureMermaidMarked has one new node and one changed node.
	structureMermaidMarked = "'''mermaid\nflowchart LR\n  A1[Start]:::new --> A2[Check]:::changed\n'''\n"
	structureMermaidPlain  = "'''mermaid\nflowchart LR\n  A1[Start] --> A2[Check]\n'''\n"
)

// structureRows returns a Decisions body: the table header, the given rows
// and the given tail (a Smallest cut line, or "").
func structureRows(tail string, rows ...string) string {
	body := formatRows(rows...)
	if tail != "" {
		body += "\n" + tail + "\n"
	}
	return body
}

// TestPreplanStructureFindings checks the 6 structure rules: each rule fires on
// its trigger fixture and stays quiet on the clean fixture, the order of the
// findings, the skip of the cut and moved rules for a table that does not
// parse, and the same findings for CRLF and for no final line end.
func TestPreplanStructureFindings(t *testing.T) {
	liveRow := "| 1 | A live proposal. | Reason. |"
	droppedRow := "| 2 | Dropped: Old idea. | Out of scope. Dropped by: G1. |"
	movedRow := func(id int) string {
		return fmt.Sprintf("| %d | Moved: Later idea %d. | Asked later. |", id, id)
	}
	sec := func(name string) string { return "## " + name }
	finding := func(rule, section, row, detail string) preplanStructureFinding {
		return preplanStructureFinding{Rule: rule, Section: section, Row: row, Detail: detail}
	}

	cases := []struct {
		name string
		data string
		want []preplanStructureFinding
	}{
		{"clean fixture", formatRealTopicFile, nil},
		{"clean fixture with the plain mermaid and no question section",
			structureTopic(fenced(structureMermaidPlain), structureRows("Smallest cut: all.", liveRow), ""), nil},
		{"no flow change with 0 live rows",
			structureTopic(structureNoFlow, structureRows("", droppedRow), ""), nil},

		// open-fence
		{"open-fence at the end of the file",
			structureTopic(structureNoFlow, structureRows("", liveRow), "- Q?\n\n'''text\nnever closes\n"),
			[]preplanStructureFinding{finding("open-fence", "", "",
				"A code fence that starts with ``` never closes. Each section after it counts as absent.")}},
		{"open-fence hides the sections after it",
			"# T\n\n## Goal\n\nG.\n\n## Problem and benefit\n\nP.\n\n## Users and effect\n\nU.\n\n" +
				"## Risk if wrong\n\n~~~~\nR.\n\n## Non-goals\n\nNone stated.\n",
			[]preplanStructureFinding{
				finding("open-fence", "", "", "A code fence that starts with ~~~~ never closes. Each section after it counts as absent."),
				finding("empty-section", sec("Non-goals"), "", "Section ## Non-goals is empty."),
				finding("empty-section", sec("Flows"), "", "Section ## Flows is empty."),
				finding("empty-section", sec("Decisions"), "", "Section ## Decisions is empty."),
			}},

		// empty-section
		{"empty-section blank body",
			strings.Replace(formatRealTopicFile, "## Risk if wrong\n\n| Failure | Who fails | Effect |\n|---|---|---|\n| The summary shows wrong content | Reader | A wrong idea is picked |\n\n",
				"## Risk if wrong\n\n   \n\n", 1),
			[]preplanStructureFinding{finding("empty-section", sec("Risk if wrong"), "", "Section ## Risk if wrong is empty.")}},
		{"empty-section absent heading",
			strings.Replace(formatRealTopicFile, "## Users and effect\n", "### Users and effect\n", 1),
			[]preplanStructureFinding{finding("empty-section", sec("Users and effect"), "", "Section ## Users and effect is empty.")}},
		{"empty-section table with no row",
			structureTopic(structureNoFlow, formatDecisionsHead, ""),
			[]preplanStructureFinding{finding("empty-section", sec("Decisions"), "", "Section ## Decisions is empty.")}},
		{"empty-section keeps the skeleton order",
			"# T\n\n## Decisions\n\n" + formatDecisionsHead + "\n## Goal\n\n",
			[]preplanStructureFinding{
				finding("empty-section", sec("Goal"), "", "Section ## Goal is empty."),
				finding("empty-section", sec("Problem and benefit"), "", "Section ## Problem and benefit is empty."),
				finding("empty-section", sec("Users and effect"), "", "Section ## Users and effect is empty."),
				finding("empty-section", sec("Risk if wrong"), "", "Section ## Risk if wrong is empty."),
				finding("empty-section", sec("Non-goals"), "", "Section ## Non-goals is empty."),
				finding("empty-section", sec("Flows"), "", "Section ## Flows is empty."),
				finding("empty-section", sec("Decisions"), "", "Section ## Decisions is empty."),
			}},

		// decisions-table
		{"decisions-table fires once and skips the cut and moved rules",
			structureTopic(structureNoFlow,
				"| Wrong | Header |\n|---|---|\n| 1 | Moved: A. | r |\n| 2 | Dropped: B. | r |\n\nSmallest cut: #9.\n", ""),
			[]preplanStructureFinding{finding("decisions-table", sec("Decisions"), "",
				"The ## Decisions table does not parse. The cut and moved checks did not run.")}},
		{"decisions-table with text and no table gives empty-section too",
			structureTopic(structureNoFlow, "TBD.\n", ""),
			[]preplanStructureFinding{
				finding("empty-section", sec("Decisions"), "", "Section ## Decisions is empty."),
				finding("decisions-table", sec("Decisions"), "",
					"The ## Decisions table does not parse. The cut and moved checks did not run."),
			}},
		{"decisions-table does not stop flow-no-proposal",
			structureTopic(fenced(structureMermaidMarked), "| Wrong | Header |\n|---|---|\n| 1 | A. | r |\n", ""),
			[]preplanStructureFinding{
				finding("decisions-table", sec("Decisions"), "",
					"The ## Decisions table does not parse. The cut and moved checks did not run."),
				finding("flow-no-proposal", sec("Flows"), "",
					"## Flows marks new or changed steps, but no live proposal exists."),
			}},

		{"decisions-table fires for a row with 2 cells",
			structureTopic(structureNoFlow, structureRows("Smallest cut: all.", liveRow, "| 2 | Only two |"), ""),
			[]preplanStructureFinding{finding("decisions-table", sec("Decisions"), "#2",
				"Table row 2 has 2 cells, not 3. The live, moved and cut checks skip it.")}},
		{"decisions-table for a row with 1 cell and no id has no row label",
			structureTopic(structureNoFlow, structureRows("", "| x |", liveRow), ""),
			[]preplanStructureFinding{finding("decisions-table", sec("Decisions"), "",
				"Table row 1 has 1 cell, not 3. The live, moved and cut checks skip it.")}},
		{"a short Moved row counts for no moved check",
			structureTopic(structureNoFlow, structureRows("", liveRow, "| 2 | Moved: Later. |"), ""),
			[]preplanStructureFinding{finding("decisions-table", sec("Decisions"), "#2",
				"Table row 2 has 2 cells, not 3. The live, moved and cut checks skip it.")}},
		{"a short live row counts as no live proposal",
			structureTopic(fenced(structureMermaidMarked), structureRows("", "| 1 | A live proposal. |"), ""),
			[]preplanStructureFinding{
				finding("decisions-table", sec("Decisions"), "#1", "Table row 1 has 2 cells, not 3. The live, moved and cut checks skip it."),
				finding("flow-no-proposal", sec("Flows"), "", "## Flows marks new or changed steps, but no live proposal exists."),
			}},
		{"a cut that names a short row names no row",
			structureTopic(structureNoFlow, structureRows("Smallest cut: #2.", liveRow, "| 2 | Short |"), ""),
			[]preplanStructureFinding{
				finding("decisions-table", sec("Decisions"), "#2", "Table row 2 has 2 cells, not 3. The live, moved and cut checks skip it."),
				finding("cut-not-live", sec("Decisions"), "#2", "Smallest cut names #2, but no row #2 exists."),
			}},
		{"decisions-table fires for a Smallest cut line with no colon",
			structureTopic(structureNoFlow, structureRows("Smallest cut #2", liveRow), ""),
			[]preplanStructureFinding{finding("decisions-table", sec("Decisions"), "",
				`The line "Smallest cut #2" does not parse. The cut check skips it.`)}},
		{"decisions-table fires for a Smallest cut line in lower case, then a good line counts",
			structureTopic(structureNoFlow, structureRows("smallest cut: #1\nSmallest cut: #5.", liveRow), ""),
			[]preplanStructureFinding{
				finding("decisions-table", sec("Decisions"), "", `The line "smallest cut: #1" does not parse. The cut check skips it.`),
				finding("cut-not-live", sec("Decisions"), "#5", "Smallest cut names #5, but no row #5 exists."),
			}},
		{"decisions-table quotes the trimmed line",
			structureTopic(structureNoFlow, structureRows("  SMALLEST CUT: all  ", liveRow), ""),
			[]preplanStructureFinding{finding("decisions-table", sec("Decisions"), "",
				`The line "SMALLEST CUT: all" does not parse. The cut check skips it.`)}},
		{"a line that does not start with Smallest cut is no cut line",
			structureTopic(structureNoFlow, structureRows("The Smallest cut is row 1.", liveRow), ""), nil},

		// cut-not-live
		{"cut-not-live dropped row (the example of the plan)",
			structureTopic(structureNoFlow, structureRows("Smallest cut: #1, #2.", liveRow, droppedRow), ""),
			[]preplanStructureFinding{finding("cut-not-live", sec("Decisions"), "#2",
				"Smallest cut names #2, but row #2 starts with Dropped:.")}},
		{"cut-not-live moved row with its question, and a missing row",
			structureTopic(structureNoFlow, structureRows("Smallest cut: #1, #3, #7.", liveRow, droppedRow, movedRow(3)), "- Q?\n"),
			[]preplanStructureFinding{
				finding("cut-not-live", sec("Decisions"), "#3", "Smallest cut names #3, but row #3 starts with Moved:."),
				finding("cut-not-live", sec("Decisions"), "#7", "Smallest cut names #7, but no row #7 exists."),
			}},
		{"cut-not-live comes in row id order, not number text order",
			structureTopic(structureNoFlow, structureRows("Smallest cut: #10, #9, #2.", liveRow), ""),
			[]preplanStructureFinding{
				finding("cut-not-live", sec("Decisions"), "#2", "Smallest cut names #2, but no row #2 exists."),
				finding("cut-not-live", sec("Decisions"), "#9", "Smallest cut names #9, but no row #9 exists."),
				finding("cut-not-live", sec("Decisions"), "#10", "Smallest cut names #10, but no row #10 exists."),
			}},
		{"cut-not-live never fires for Smallest cut all",
			structureTopic(structureNoFlow, structureRows("Smallest cut: all.", liveRow, droppedRow), ""), nil},
		{"cut-not-live does not fire for live ids",
			structureTopic(structureNoFlow, structureRows("Smallest cut: #1.", liveRow, droppedRow), ""), nil},

		// moved-no-question
		{"moved-no-question 2 rows and 1 bullet",
			structureTopic(structureNoFlow, structureRows("", liveRow, movedRow(2), movedRow(3)), "- Q?\n"),
			[]preplanStructureFinding{finding("moved-no-question", sec("Open questions"), "",
				"2 rows start with Moved:, but ## Open questions holds 1 bullet.")}},
		{"moved-no-question 1 row and an absent section",
			structureTopic(structureNoFlow, structureRows("", liveRow, movedRow(2)), ""),
			[]preplanStructureFinding{finding("moved-no-question", sec("Open questions"), "",
				"1 row starts with Moved:, but ## Open questions holds 0 bullets.")}},
		{"moved-no-question counts bullets, not text",
			structureTopic(structureNoFlow, structureRows("", liveRow, movedRow(2), movedRow(3)), "Moved: a question in prose.\n- One.\n- Two.\n"),
			nil},

		// flow-no-proposal
		{"flow-no-proposal when all rows are dropped",
			structureTopic(fenced(structureMermaidMarked), structureRows("", droppedRow), ""),
			[]preplanStructureFinding{finding("flow-no-proposal", sec("Flows"), "",
				"## Flows marks new or changed steps, but no live proposal exists.")}},
		{"flow-no-proposal with a moved row only and a changed node",
			structureTopic("'''mermaid\nflowchart LR\n  A[Check]:::changed\n'''\n", structureRows("", movedRow(1)), "- Q?\n"),
			[]preplanStructureFinding{finding("flow-no-proposal", sec("Flows"), "",
				"## Flows marks new or changed steps, but no live proposal exists.")}},
		{"flow-no-proposal does not fire with a live row",
			structureTopic(fenced(structureMermaidMarked), structureRows("", liveRow), ""), nil},
		{"flow-no-proposal does not fire for a mark outside a mermaid fence",
			structureTopic("The mark :::new is text.\n\n'''text\nA:::changed\n'''\n", structureRows("", droppedRow), ""), nil},
		{"flow-no-proposal does not fire for a longer class name",
			structureTopic("'''mermaid\nflowchart LR\n  A:::newer\n'''\n", structureRows("", droppedRow), ""), nil},

		// order
		{"findings keep rule order",
			structureTopic(fenced(structureMermaidMarked)+"\n",
				structureRows("Smallest cut: #2, #3.", droppedRow, movedRow(3), movedRow(4)), "- Q?\n"),
			[]preplanStructureFinding{
				finding("cut-not-live", sec("Decisions"), "#2", "Smallest cut names #2, but row #2 starts with Dropped:."),
				finding("cut-not-live", sec("Decisions"), "#3", "Smallest cut names #3, but row #3 starts with Moved:."),
				finding("moved-no-question", sec("Open questions"), "", "2 rows start with Moved:, but ## Open questions holds 1 bullet."),
				finding("flow-no-proposal", sec("Flows"), "", "## Flows marks new or changed steps, but no live proposal exists."),
			}},
		{"findings keep rule order with empty-section first",
			strings.Replace(structureTopic(structureNoFlow, structureRows("Smallest cut: #2.", liveRow, droppedRow), ""),
				"## Goal\n\nG.\n", "## Goal\n\n", 1),
			[]preplanStructureFinding{
				finding("empty-section", sec("Goal"), "", "Section ## Goal is empty."),
				finding("cut-not-live", sec("Decisions"), "#2", "Smallest cut names #2, but row #2 starts with Dropped:."),
			}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.want
			if want == nil {
				want = []preplanStructureFinding{}
			}
			text := fenced(tc.data)
			variants := []struct{ name, data string }{
				{"LF", text},
				{"CRLF", strings.ReplaceAll(text, "\n", "\r\n")},
				{"no final line end", strings.TrimRight(text, "\n")},
			}
			for _, v := range variants {
				got := preplanStructureFindings([]byte(v.data))
				if got == nil {
					t.Fatalf("%s: got nil, want a non-nil list", v.name)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: findings = %+v\nwant %+v", v.name, got, want)
				}
			}
		})
	}
}

// TestPreplanMermaidLines checks the fence walk of preplanMermaidLines: found
// and the lines inside each mermaid fence.
func TestPreplanMermaidLines(t *testing.T) {
	for _, tc := range []struct {
		name       string
		lines      []string
		wantFound  bool
		wantInside []string
	}{
		{"a closed mermaid fence", []string{"text", "```mermaid", "A:::new", "B", "```", "after"}, true, []string{"A:::new", "B"}},
		{"an unclosed mermaid fence holds every later line", []string{"```mermaid", "A", "", "B"}, true, []string{"A", "", "B"}},
		{"an empty mermaid fence", []string{"```mermaid", "```"}, true, nil},
		{"a fence that is not mermaid", []string{"```text", "A:::new", "```"}, false, nil},
		{"a fence with no info string", []string{"```", "A:::new", "```"}, false, nil},
		{"the info string in another case is not mermaid", []string{"```Mermaid", "A", "```"}, false, nil},
		{"spaces around the info string", []string{"~~~  mermaid  ", "A", "~~~"}, true, []string{"A"}},
		{"only the mermaid fence of two counts", []string{"```text", "x", "```", "~~~mermaid", "y", "~~~", "z"}, true, []string{"y"}},
		{"two mermaid fences", []string{"```mermaid", "a", "```", "```mermaid", "b", "```"}, true, []string{"a", "b"}},
		{"a shorter run inside the fence is a line", []string{"````mermaid", "a", "```", "b", "````"}, true, []string{"a", "```", "b"}},
		{"an indented fence is no fence", []string{"  ```mermaid", "A", "  ```"}, false, nil},
		{"no lines", nil, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found, inside := preplanMermaidLines(tc.lines)
			if found != tc.wantFound || !reflect.DeepEqual(inside, tc.wantInside) {
				t.Errorf("found, inside = %v, %q, want %v, %q", found, inside, tc.wantFound, tc.wantInside)
			}
		})
	}
}

// TestPreplanStructureFindingsRealTopicFile checks that a copy of the real
// topic file 2026-11-13-idea-verification.md gives an empty, non-nil list, in
// LF form and in CRLF form.
func TestPreplanStructureFindingsRealTopicFile(t *testing.T) {
	for name, data := range map[string]string{
		"LF":   structureIdeaVerificationTopic,
		"CRLF": strings.ReplaceAll(structureIdeaVerificationTopic, "\n", "\r\n"),
	} {
		got := preplanStructureFindings([]byte(data))
		if got == nil || len(got) != 0 {
			t.Errorf("%s: findings = %#v, want an empty, non-nil list", name, got)
		}
	}
}

// TestPreplanStructureFindingsEmptyInput checks the non-nil list and the
// fields of a finding for input with no section at all.
func TestPreplanStructureFindingsEmptyInput(t *testing.T) {
	for _, data := range [][]byte{nil, {}} {
		got := preplanStructureFindings(data)
		if len(got) != len(preplanSections) {
			t.Fatalf("findings = %+v, want one empty-section finding for each of %d sections", got, len(preplanSections))
		}
		for i, f := range got {
			if f.Rule != "empty-section" || f.Section != "## "+preplanSections[i] || f.Row != "" {
				t.Errorf("finding %d = %+v", i, f)
			}
		}
	}
}
