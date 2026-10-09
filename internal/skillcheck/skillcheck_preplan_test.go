// Package skillcheck cross-checks the preplan skill against the places that
// describe it: its frontmatter, the Flags table in docs/skills/preplan.md, and
// the live MCP registry. The argument-hint in plugins/sdlc/skills/preplan/SKILL.md
// is the canonical argument list. The Flags table in the docs must declare the
// same arguments.
//
// The Flags table of the preplan docs page has two columns (Flag, Meaning).
// The first cell of each row holds the argument as an inline code span. The
// argument can be a positional such as `[topic]` or a flag such as `--name`.
//
// The shared registry helpers live in skillcheck_flags_test.go. Every
// unexported identifier below is prefixed preplanSkills, and the one declared
// Test function is TestPreplanSkillParity.
package skillcheck

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// preplanSkillsArgTokenRe matches one argument in an argument-hint or in the
// first cell of a Flags table row: a bracketed positional such as [topic], or
// a --flag.
var preplanSkillsArgTokenRe = regexp.MustCompile(`\[[^\]]+\]|--[a-z][a-z0-9]*(?:-[a-z0-9]+)*`)

// preplanSkillsArgTokens returns the set of arguments found in text.
func preplanSkillsArgTokens(text string) map[string]bool {
	set := map[string]bool{}
	for _, tok := range preplanSkillsArgTokenRe.FindAllString(text, -1) {
		set[tok] = true
	}
	return set
}

// preplanSkillsFlagsTableArgs returns the arguments declared by the Flags
// table of the preplan docs page. It reads the first cell of every data row in
// the table under the "## Flags" heading. It fails the test when the heading,
// the table, or the "Flag" header cell is missing, so a layout change cannot
// pass on an empty set.
func preplanSkillsFlagsTableArgs(t *testing.T, docs string) map[string]bool {
	t.Helper()

	inSection, headerSeen := false, false
	args := map[string]bool{}
	for _, line := range strings.Split(docs, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			if inSection {
				break
			}
			inSection = strings.TrimSpace(strings.TrimLeft(trimmed, "#")) == "Flags" && strings.HasPrefix(trimmed, "## ")
			continue
		}
		if !inSection || !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := flagParitySplitCells(trimmed)
		if len(cells) < 3 {
			t.Fatalf("docs/skills/preplan.md Flags table row has %d cells, want at least 3 (leading pipe, Flag, Meaning): %q", len(cells), trimmed)
		}
		first := strings.TrimSpace(cells[1])
		switch {
		case strings.Trim(first, "-: ") == "":
			// Separator row such as |---|---|.
		case !headerSeen:
			if first != "Flag" {
				t.Fatalf("docs/skills/preplan.md Flags table header first cell = %q, want %q", first, "Flag")
			}
			headerSeen = true
		default:
			for tok := range preplanSkillsArgTokens(first) {
				args[tok] = true
			}
		}
	}
	if !headerSeen {
		t.Fatal("docs/skills/preplan.md has no table under a \"## Flags\" heading")
	}
	if len(args) == 0 {
		t.Fatal("docs/skills/preplan.md Flags table declares no arguments")
	}
	return args
}

// TestPreplanSkillParity checks that the preplan skill agrees with its docs
// page and with the live MCP registry.
func TestPreplanSkillParity(t *testing.T) {
	pluginRoot := executeSkillsRepoRoot(t)
	skill := executeSkillsReadFile(t, filepath.Join(pluginRoot, "skills", "preplan", "SKILL.md"))
	// docs/ sits beside plugins/ at the repository root.
	docsPath := filepath.Join(filepath.Dir(filepath.Dir(pluginRoot)), "docs", "skills", "preplan.md")
	docs := executeSkillsReadFile(t, docsPath)

	t.Run("frontmatter", func(t *testing.T) {
		if got := flagParityFrontmatterField(skill, "name"); got != "preplan" {
			t.Errorf("frontmatter name = %q, want %q (the skill directory name)", got, "preplan")
		}
		if got := flagParityFrontmatterField(skill, "user-invocable"); got != "true" {
			t.Errorf("frontmatter user-invocable = %q, want %q", got, "true")
		}
	})

	t.Run("docs_flags_table_agrees", func(t *testing.T) {
		hint := flagParityFrontmatterField(skill, "argument-hint")
		canonical := preplanSkillsArgTokens(hint)
		if len(canonical) == 0 {
			t.Fatalf("preplan/SKILL.md argument-hint %q holds no argument", hint)
		}
		flagParityAssertSameSet(t, "docs/skills/preplan.md Flags table", preplanSkillsFlagsTableArgs(t, docs), canonical)
	})

	t.Run("tools_and_actions_resolve", func(t *testing.T) {
		flagParityAssertToolsResolve(t, "preplan", "plan_support:preplan_context")
	})
}
