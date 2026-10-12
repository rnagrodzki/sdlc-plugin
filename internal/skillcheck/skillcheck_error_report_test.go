// Package skillcheck cross-checks the error-report skill
// (plugins/sdlc/skills/error-report/SKILL.md) against the tool that feeds it:
// prepare_orchestrator in error_report mode runs the duplicate-issue search
// and returns similarIssues and similarIssuesNote, so the skill must read
// them and must not run a search of its own.
//
// The shared pin helper hardenSkillsAssertOnce lives in
// skillcheck_harden_test.go. The one declared Test function is
// TestErrorReportSkillParity.
package skillcheck

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestErrorReportSkillParity pins the sentences of the error-report skill
// that read the duplicate-issue search result of prepare_orchestrator, and
// checks that the skill no longer runs gh issue list itself. Each pin is a
// whole sentence that occurs once, so deleting its site fails the test.
func TestErrorReportSkillParity(t *testing.T) {
	skill := executeSkillsReadFile(t, filepath.Join(executeSkillsRepoRoot(t), "skills", "error-report", "SKILL.md"))

	t.Run("search_result_is_read_from_prepare_orchestrator", func(t *testing.T) {
		for _, want := range []string{
			// The return-shape sentence of the prepare_orchestrator step.
			"Keep `similarIssues` and `similarIssuesNote` for Step 6b.",
			// The sentence of the duplicate-search step that reads the list.
			"Use `similarIssues` from the Step 4 result.",
			// The note is printed before any other output of that step.
			"If `similarIssuesNote` is set, print it first.",
			// An empty list goes on with no question.
			"**If `similarIssues` is empty:** go to Step 7. Ask no question.",
			// The return shape names the Markdown form of an empty list.
			"always present; empty (shown as (none)) when nothing matches or the search was skipped",
			// A failed gh search leaves the list empty, not a tool error.
			"The call succeeds, `similarIssues` is empty (shown as `(none)`), and `similarIssuesNote`",
		} {
			hardenSkillsAssertOnce(t, "error-report/SKILL.md", skill, want)
		}
	})

	t.Run("no_skill_side_search", func(t *testing.T) {
		if strings.Contains(skill, "gh issue list") {
			t.Error("error-report/SKILL.md still runs gh issue list; the duplicate search step reads similarIssues")
		}
	})
}
