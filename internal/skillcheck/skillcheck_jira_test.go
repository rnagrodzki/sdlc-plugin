// Package skillcheck cross-checks the jira skill
// (plugins/sdlc/skills/jira/SKILL.md) against the live MCP registry and pins
// its no-template behavior. The plugin ships no default Jira templates, so a
// new project has none. The skill used to stop every create on such a project;
// it now drafts from a fixed base structure and still runs every write gate.
// The skill text is the whole implementation of that rule, so these
// assertions keep it from being edited back out.
//
// The shared matching and registry helpers live in skillcheck_flags_test.go.
// Every unexported identifier below is prefixed jiraSkills, and the one
// declared Test function is TestJiraSkillParity.
package skillcheck

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// jiraSkillsOldStopRe matches the retired rule: a "No template" notice that
// stops the operation.
var jiraSkillsOldStopRe = regexp.MustCompile(`(?i)no template[^\n]*\n?[^\n]*stop the operation|stop the operation[^\n]*\n?[^\n]*no template`)

func TestJiraSkillParity(t *testing.T) {
	t.Run("tools_and_actions_resolve", func(t *testing.T) {
		flagParityAssertToolsResolve(t, "jira",
			"jira:check",
			"jira:load",
			"jira:templates",
			"jira:init-templates",
			"jira:copy-template",
			"jira:write-critique",
			"jira:write-approval",
			"jira:validate-body",
			"mcp_failure_record",
		)
	})

	t.Run("no_template_falls_back_to_base_structure", func(t *testing.T) {
		body := flagParityBody(executeSkillsReadFile(t,
			filepath.Join(executeSkillsRepoRoot(t), "skills", "jira", "SKILL.md")))

		for _, want := range []string{
			// The base structure, in the order it is drafted.
			"## Summary\n     - {what_and_why}",
			"## Context\n     - {background}",
			"## Acceptance Criteria\n     - [ ] {criterion}",
			// The user is told no template was found and where to add one.
			"No template for <type> — drafting the description from the base structure",
			"create .sdlc-v2/jira-templates/<type>.md",
			// Every write gate still runs for a base-structure description.
			"No gate is skipped because no template was found.",
			"Stop a create or edit only because the issue type has no template",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("jira/SKILL.md no longer states %q — the no-template fallback is incomplete", want)
			}
		}

		if loc := jiraSkillsOldStopRe.FindStringIndex(body); loc != nil {
			t.Errorf("jira/SKILL.md still stops on a missing template: %q", body[loc[0]:loc[1]])
		}
		if strings.Contains(body, "Free-form descriptions are prohibited") {
			t.Error(`jira/SKILL.md still says "Free-form descriptions are prohibited", which contradicts the base-structure fallback`)
		}
	})

	t.Run("init_templates_admits_copying_nothing", func(t *testing.T) {
		body := flagParityBody(executeSkillsReadFile(t,
			filepath.Join(executeSkillsRepoRoot(t), "skills", "jira", "SKILL.md")))

		for _, want := range []string{
			"Copies nothing when no default templates are found",
			"No default templates found — nothing copied.",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("jira/SKILL.md no longer states %q — --init-templates must say it copied nothing when no defaults exist", want)
			}
		}
	})
}
