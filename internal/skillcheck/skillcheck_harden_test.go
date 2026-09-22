// Package skillcheck cross-checks the harden skill
// (plugins/sdlc/skills/harden/SKILL.md) against the live MCP registry and
// against itself: every tool and action it names must exist, and the flag
// list in its frontmatter description must agree with the flag list in its
// Step 0 body. Other skills dispatch harden with flags (received-review passes
// --auto), so a flag that only one of these places mentions is a dispatch that
// fails at runtime.
//
// The shared matching and registry helpers live in skillcheck_flags_test.go.
// Every unexported identifier below is prefixed hardenSkills, and the one
// declared Test function is TestHardenSkillParity.
package skillcheck

import (
	"path/filepath"
	"regexp"
	"testing"
)

// hardenSkillsFlagSentenceRe finds the two Step 0 sentences that list flags
// as a run of inline code spans: "Required flag: `--skill` ..." and
// "Optional: `--step`, `--operation`, ...".
var hardenSkillsFlagSentenceRe = regexp.MustCompile(`\b(?:Required flag|Optional):`)

// hardenSkillsProseFlags returns the flags in the code-span runs that follow
// "Required flag:" and "Optional:" in the SKILL.md body.
func hardenSkillsProseFlags(body string) map[string]bool {
	set := map[string]bool{}
	for _, m := range hardenSkillsFlagSentenceRe.FindAllStringIndex(body, -1) {
		for _, sp := range flagParitySpanChain(body, m[1]) {
			for f := range flagParityFlagSet(body[sp[0]:sp[1]]) {
				set[f] = true
			}
		}
	}
	return set
}

func TestHardenSkillParity(t *testing.T) {
	t.Run("tools_and_actions_resolve", func(t *testing.T) {
		flagParityAssertToolsResolve(t, "harden",
			"prepare_orchestrator",
			"learnings_log:read", "learnings_log:append", "learnings_log:remove",
			"validate:guardrails", "validate:dimensions",
			"dimensions_render_instructions",
		)
	})

	t.Run("flag_surface_agrees", func(t *testing.T) {
		content := executeSkillsReadFile(t, filepath.Join(executeSkillsRepoRoot(t), "skills", "harden", "SKILL.md"))
		body := flagParityBody(content)

		// The description names every flag harden accepts. Step 0 names the
		// same flags in a table and in two sentences. Both lists must match.
		description := flagParityFlagSet(flagParityFrontmatterField(content, "description"))
		step0 := flagParityTableFlags(body)
		for f := range hardenSkillsProseFlags(body) {
			step0[f] = true
		}
		if len(step0) == 0 {
			t.Fatal("found no flags in the Step 0 table or the Required flag / Optional sentences, so the flag-list matching no longer fits this skill's format")
		}
		flagParityAssertSameSet(t, "harden description flags (got) vs Step 0 flags (want)", description, step0)

		// argument-hint is the short form of the same list.
		flagParityAssertSubset(t, "harden argument-hint", flagParityHintFlags(content), description)

		// --auto is the flag other skills dispatch to harden (issue #51). The
		// flag lint reads the hint and the table, so both must carry it.
		if !flagParityHintFlags(content)["--auto"] {
			t.Error("harden argument-hint does not list --auto, which received-review dispatches to it")
		}
		if !flagParityTableFlags(body)["--auto"] {
			t.Error("harden Step 0 flag table has no --auto row, which received-review dispatches to it")
		}
	})
}
