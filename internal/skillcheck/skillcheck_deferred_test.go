// Package skillcheck cross-checks the deferred skill across the three files
// that describe it: plugins/sdlc/skills/deferred/SKILL.md, its companion
// reference.md, and the user documentation docs/skills/deferred.md. The flags
// in SKILL.md's argument-hint are the canonical set. Every other place that
// names a flag must agree with it, and every ship_state call in the skill
// files must resolve against the live MCP registry.
//
// The shared matching and registry helpers live in skillcheck_flags_test.go.
// Every unexported identifier below is prefixed deferredSkills, and the one
// declared Test function is TestDeferredSkillParity.
package skillcheck

import (
	"path/filepath"
	"regexp"
	"testing"
)

var (
	// deferredSkillsSlashSpanRe matches an inline code span holding a
	// /sdlc:deferred command. Submatch 1 is the text after the command name.
	deferredSkillsSlashSpanRe = regexp.MustCompile("`/sdlc:deferred(\\s[^`]*)?`")

	// deferredSkillsDocsCommandRe matches an indented (4 spaces) command line
	// in docs/skills/deferred.md, the way the docs show every command.
	// Submatch 1 is the text after the command name.
	deferredSkillsDocsCommandRe = regexp.MustCompile(`(?m)^ {4}/deferred((?:\s.*)?)$`)
)

// deferredSkillsFlagsOf returns the flags found in each submatch-1 text of re
// in content, one set per match.
func deferredSkillsFlagsOf(re *regexp.Regexp, content string) []map[string]bool {
	var sets []map[string]bool
	for _, m := range re.FindAllStringSubmatch(content, -1) {
		sets = append(sets, flagParityFlagSet(m[1]))
	}
	return sets
}

func TestDeferredSkillParity(t *testing.T) {
	skillsDir := filepath.Join(executeSkillsRepoRoot(t), "skills", "deferred")
	// docs/ sits beside plugins/ at the repository root.
	docsPath := filepath.Join(filepath.Dir(filepath.Dir(executeSkillsRepoRoot(t))), "docs", "skills", "deferred.md")

	skill := executeSkillsReadFile(t, filepath.Join(skillsDir, "SKILL.md"))
	reference := executeSkillsReadFile(t, filepath.Join(skillsDir, "reference.md"))
	docs := executeSkillsReadFile(t, docsPath)

	canonical := flagParityHintFlags(skill)
	if len(canonical) == 0 {
		t.Fatal("deferred/SKILL.md has no flags in its argument-hint frontmatter")
	}

	t.Run("frontmatter", func(t *testing.T) {
		if got := flagParityFrontmatterField(skill, "name"); got != "deferred" {
			t.Errorf("frontmatter name = %q, want %q (the skill directory name)", got, "deferred")
		}
		if got := flagParityFrontmatterField(skill, "user-invocable"); got != "true" {
			t.Errorf("frontmatter user-invocable = %q, want %q", got, "true")
		}
	})

	t.Run("skill_md_flags_agree", func(t *testing.T) {
		flagParityAssertSameSet(t, "deferred/SKILL.md description flags", flagParityFlagSet(flagParityFrontmatterField(skill, "description")), canonical)
		flagParityAssertSameSet(t, "deferred/SKILL.md Step 1 table flags", flagParityTableFlags(flagParityBody(skill)), canonical)

		// The usage line and every other /sdlc:deferred command in the body.
		usage := map[string]bool{}
		spans := deferredSkillsFlagsOf(deferredSkillsSlashSpanRe, flagParityBody(skill))
		if len(spans) == 0 {
			t.Fatal("found no `/sdlc:deferred ...` command spans in deferred/SKILL.md")
		}
		for _, set := range spans {
			flagParityAssertSubset(t, "deferred/SKILL.md command span", set, canonical)
			for f := range set {
				usage[f] = true
			}
		}
		flagParityAssertSameSet(t, "deferred/SKILL.md command spans", usage, canonical)
	})

	t.Run("reference_md_flags_agree", func(t *testing.T) {
		spans := deferredSkillsFlagsOf(deferredSkillsSlashSpanRe, reference)
		if len(spans) == 0 {
			t.Fatal("found no `/sdlc:deferred ...` command spans in deferred/reference.md")
		}
		for _, set := range spans {
			flagParityAssertSubset(t, "deferred/reference.md command span", set, canonical)
		}
	})

	t.Run("docs_flags_agree", func(t *testing.T) {
		commands := deferredSkillsFlagsOf(deferredSkillsDocsCommandRe, docs)
		if len(commands) == 0 {
			t.Fatal("found no indented /deferred command lines in docs/skills/deferred.md")
		}
		usage := map[string]bool{}
		for _, set := range commands {
			flagParityAssertSubset(t, "docs/skills/deferred.md command", set, canonical)
			for f := range set {
				usage[f] = true
			}
		}
		flagParityAssertSameSet(t, "docs/skills/deferred.md command lines", usage, canonical)
		flagParityAssertSameSet(t, "docs/skills/deferred.md flag table", flagParityTableFlags(docs), canonical)
	})

	t.Run("tools_and_actions_resolve", func(t *testing.T) {
		flagParityAssertToolsResolve(t, "deferred",
			"ship_state:deferred_list",
			"ship_state:deferred_resolve",
			"ship_state:deferred_propose_followups",
		)
	})
}
