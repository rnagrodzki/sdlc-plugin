package tools

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// preplanSkillTableSections returns the "## " names in the first cell of each
// data row of the section table in the preplan SKILL.md, in file order. It
// finds the table by its header row "| Section | Content |" and stops at the
// first line after it that is not a table row. It fails the test when the
// header row is missing, so a layout change cannot pass on an empty list.
func preplanSkillTableSections(t *testing.T, md string) []string {
	t.Helper()
	lines := strings.Split(md, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "| Section | Content |" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatal("SKILL.md has no \"| Section | Content |\" table header row")
	}
	var names []string
	for _, line := range lines[start:] {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		first := strings.TrimSpace(cells[0])
		if strings.Trim(first, "-: ") == "" {
			continue
		}
		names = append(names, strings.TrimPrefix(strings.Trim(first, "`"), preplanHeadingPrefix))
	}
	return names
}

// moveTableRow returns md with the line that starts with row moved to just
// before the line that starts with before. The set of rows stays the same, so
// only the order changes. It fails the test when a line is missing.
func moveTableRow(t *testing.T, md, row, before string) string {
	t.Helper()
	lines := strings.Split(md, "\n")
	var moved string
	rest := make([]string, 0, len(lines))
	for _, l := range lines {
		if moved == "" && strings.HasPrefix(l, row) {
			moved = l
			continue
		}
		rest = append(rest, l)
	}
	if moved == "" {
		t.Fatalf("SKILL.md has no line that starts with %q", row)
	}
	for i, l := range rest {
		if strings.HasPrefix(l, before) {
			out := append(append(append([]string(nil), rest[:i]...), moved), rest[i:]...)
			return strings.Join(out, "\n")
		}
	}
	t.Fatalf("SKILL.md has no line that starts with %q", before)
	return ""
}

// TestPreplanSkillSectionTableParity checks that the "## " names in the
// section table of the preplan SKILL.md equal preplanSections, in order,
// and that "## Open questions" is the one extra row, after them.
func TestPreplanSkillSectionTableParity(t *testing.T) {
	b, err := os.ReadFile("../../plugins/sdlc/skills/preplan/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	md := string(b)
	want := append(append([]string(nil), preplanSections...), "Open questions")

	if got := preplanSkillTableSections(t, md); !reflect.DeepEqual(got, want) {
		t.Errorf("SKILL.md section table = %v, want preplanSections then Open questions = %v", got, want)
	}

	for name, drifted := range map[string]string{
		"section removed": strings.Replace(md, "| `## Risk if wrong` | who or what fails if the change has a defect |\n", "", 1),
		"section renamed": strings.Replace(md, "| `## Non-goals` |", "| `## Out of scope` |", 1),
		"order swapped": strings.Replace(strings.Replace(strings.Replace(md,
			"| `## Goal` |", "| `## TMP` |", 1),
			"| `## Problem and benefit` |", "| `## Goal` |", 1),
			"| `## TMP` |", "| `## Problem and benefit` |", 1),
		"open questions moved up": moveTableRow(t, md, "| `## Open questions` |", "| `## Goal` |"),
	} {
		if drifted == md {
			t.Fatalf("%s: the drift edit changed nothing; update the test to the SKILL.md table", name)
		}
		if got := preplanSkillTableSections(t, drifted); reflect.DeepEqual(got, want) {
			t.Errorf("%s: the parity check did not see the change", name)
		}
	}
}

// TestPlanSkillPreplanSectionParity checks that the Preplan topic file block
// of the plan SKILL.md (Step 1) names each section of preplanSections as
// "`## <name>`". The block is the only reader of a topic file in the plan
// skill, so a section that preplan_context adds and the block does not name
// has no reader. The block ends at the first blank line after its start.
func TestPlanSkillPreplanSectionParity(t *testing.T) {
	b, err := os.ReadFile("../../plugins/sdlc/skills/plan/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	md := string(b)
	const start = "**Preplan topic file:**"
	from := strings.Index(md, start)
	if from < 0 {
		t.Fatalf("plan SKILL.md has no %q block", start)
	}
	block := md[from:]
	if end := strings.Index(block, "\n\n"); end >= 0 {
		block = block[:end]
	}
	for _, name := range preplanSections {
		if want := "`" + preplanHeadingPrefix + name + "`"; !strings.Contains(block, want) {
			t.Errorf("plan SKILL.md Preplan topic file block does not name %s", want)
		}
	}
}
