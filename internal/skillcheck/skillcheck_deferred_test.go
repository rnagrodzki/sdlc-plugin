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
	"strings"
	"testing"
)

// deferredSkillsHardenReplayCmd is the command line that deferred triage
// prints for an item with source harden-pending. The harden skill prints the
// same line for a replay run.
const deferredSkillsHardenReplayCmd = `/sdlc:harden --failure-text "Pending rule edits: <id>" --skill deferred`

var (
	// deferredSkillsSlashSpanRe matches an inline code span holding a
	// /sdlc:deferred command. Submatch 1 is the text after the command name.
	deferredSkillsSlashSpanRe = regexp.MustCompile("`/sdlc:deferred(\\s[^`]*)?`")

	// deferredSkillsDocsCommandRe matches an indented (4 spaces) command line
	// in docs/skills/deferred.md, the way the docs show every command.
	// Submatch 1 is the text after the command name.
	deferredSkillsDocsCommandRe = regexp.MustCompile(`(?m)^ {4}/deferred((?:\s.*)?)$`)

	// deferredSkillsHardenReplayRe matches every harden replay command line,
	// whatever its item id and caller name. A file that prints the command
	// must print it in the one form of deferredSkillsHardenReplayCmd.
	deferredSkillsHardenReplayRe = regexp.MustCompile(`/sdlc:harden --failure-text "Pending rule edits: [^"]*" --skill [a-z-]+`)
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

	t.Run("harden_pending_branch", func(t *testing.T) {
		hardenSkill := executeSkillsReadFile(t, filepath.Join(executeSkillsRepoRoot(t), "skills", "harden", "SKILL.md"))
		// The harden skill wraps some lines, so compare on collapsed whitespace.
		hardenFlat := strings.Join(strings.Fields(hardenSkill), " ")
		cmd := deferredSkillsHardenReplayCmd

		// The triage flow: one branch, ahead of the drafting item, that prints
		// the command and files no issue.
		branch := "If the item's `source` is `harden-pending`, do not draft an issue. Do not run `gh issue create`."
		hardenSkillsAssertOnce(t, "deferred/reference.md", reference, branch)
		hardenSkillsAssertOnce(t, "deferred/reference.md", reference, cmd)
		hardenSkillsAssertOnce(t, "deferred/reference.md", reference, "Then resolve it: /sdlc:deferred --resolve <id>")
		hardenSkillsAssertOnce(t, "deferred/reference.md", reference, "Count the item as still open. Do not call `deferred_resolve`.")
		if b, d := strings.Index(reference, branch), strings.Index(reference, "1. **Draft.**"); b < 0 || d < 0 || b > d {
			t.Errorf("deferred/reference.md: the harden-pending branch (offset %d) must come before the Draft item (offset %d)", b, d)
		}

		// Option 2 must not close a harden-pending item, because resolving it
		// loses the rule edits. The branch sits in the option 2 section, ahead
		// of the generic resolve call, and points at the two lines of item 0.
		// It prints no copy of them, so the once-only pins above still hold.
		optionStart := strings.Index(reference, "### 4. Option 2")
		optionEnd := strings.Index(reference, "### 5. Close out")
		if optionStart < 0 || optionEnd < optionStart {
			t.Fatalf("deferred/reference.md: cannot find the option 2 section (start %d, end %d)", optionStart, optionEnd)
		}
		option2 := reference[optionStart:optionEnd]
		pendingBranch := "If the item's `source` is `harden-pending`, do not call `deferred_resolve`."
		genericResolve := "Call `ship_state({action:\"deferred_resolve\", detail:{id:\"<id>\"}})`."
		for _, want := range []string{
			pendingBranch,
			"Resolving it closes the item and loses the rule edits.",
			"Print the same two lines as item 0 of section 3.",
			"The second line tells the user to resolve the item after the harden run. Leave the item open and go to the next item.",
			genericResolve,
		} {
			hardenSkillsAssertOnce(t, "deferred/reference.md option 2", option2, want)
		}
		if b, g := strings.Index(option2, pendingBranch), strings.Index(option2, genericResolve); b < 0 || g < 0 || b > g {
			t.Errorf("deferred/reference.md option 2: the harden-pending branch (offset %d) must come before the generic resolve call (offset %d)", b, g)
		}

		// The harden skill prints the same command at both of its sites.
		hardenSkillsAssertOnce(t, "harden/SKILL.md", hardenFlat, "Deferred triage prints the command: `"+cmd+"`.")
		hardenSkillsAssertOnce(t, "harden/SKILL.md", hardenFlat, "A person replays the item later with `"+cmd+"`,")

		// No file may print a different form of the command.
		for name, text := range map[string]string{"deferred/reference.md": reference, "harden/SKILL.md": hardenSkill} {
			found := deferredSkillsHardenReplayRe.FindAllString(text, -1)
			if len(found) == 0 {
				t.Errorf("%s prints no harden replay command", name)
			}
			for _, got := range found {
				if got != cmd {
					t.Errorf("%s prints replay command %q, want %q", name, got, cmd)
				}
			}
		}

		hardenSkillsAssertOnce(t, "docs/skills/deferred.md", docs, "**Pending rule edits are not filed.**")

		// The docs say that resolving without filing also leaves such an item
		// open, in the same bullet. The bullet wraps, so compare on collapsed
		// whitespace.
		docsFlat := strings.Join(strings.Fields(docs), " ")
		resolveSentence := "The choice to resolve without filing also leaves such an item open and prints the same command."
		hardenSkillsAssertOnce(t, "docs/skills/deferred.md", docsFlat, resolveSentence)
		bullet := strings.Index(docsFlat, "**Pending rule edits are not filed.**")
		next := strings.Index(docsFlat, "**The durable write is best-effort.**")
		if s := strings.Index(docsFlat, resolveSentence); bullet < 0 || next < 0 || s < bullet || s > next {
			t.Errorf("docs/skills/deferred.md: the resolve-without-filing sentence (offset %d) must sit in the pending-edits bullet (offsets %d to %d)", s, bullet, next)
		}
	})

	t.Run("issue_title_is_first_line", func(t *testing.T) {
		// A description can have more than one line, so the issue title and
		// the picker label take only its first line.
		hardenSkillsAssertOnce(t, "deferred/reference.md", reference, "**Title:** the first line of the item's `description`.")
		hardenSkillsAssertOnce(t, "deferred/reference.md", reference, "labelled `[<id>] <first line of description>`.")
		hardenSkillsAssertOnce(t, "deferred/reference.md", reference, "`/ship` passes the finding body, so a `description` can have more than one line.")
	})

	t.Run("tools_and_actions_resolve", func(t *testing.T) {
		flagParityAssertToolsResolve(t, "deferred",
			"ship_state:deferred_list",
			"ship_state:deferred_resolve",
			"ship_state:deferred_propose_followups",
		)
	})
}
