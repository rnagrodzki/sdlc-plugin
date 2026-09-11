package tools

import (
	"path/filepath"
	"testing"
)

// TestSkillTemplateIndex_PluginRoot exercises buildSkillTemplateIndex's two
// entry sources directly (bypassing the process-wide sync.Once cache via the
// resetSkillTemplateIndex test seam):
//
//   - CLAUDE_PLUGIN_ROOT set: templates shipped directly under
//     <root>/skills/plan/ (the dev/path-mode case — see the comment above
//     buildSkillTemplateIndex in plan.go) must appear in the returned index.
//   - CLAUDE_PLUGIN_ROOT unset and no ~/.claude/plugins to walk: the index
//     must come back empty rather than erroring or panicking.
//
// Both subtests point HOME at an empty t.TempDir() so the ~/.claude/plugins
// walk never sees whatever is actually installed on the machine running the
// test, keeping the assertions hermetic.
func TestSkillTemplateIndex_PluginRoot(t *testing.T) {
	t.Run("CLAUDE_PLUGIN_ROOT set finds plan templates", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("CLAUDE_PLUGIN_ROOT", filepath.Join("testdata", "plugins", "sdlc"))
		resetSkillTemplateIndex()

		idx := buildSkillTemplateIndex()

		wantDefault := filepath.Join("testdata", "plugins", "sdlc", "skills", "plan", "plan-template-default.md")
		if got, ok := idx["plan-template-default.md"]; !ok || got != wantDefault {
			t.Errorf("idx[%q] = (%q, %v), want (%q, true)", "plan-template-default.md", got, ok, wantDefault)
		}

		wantLens := filepath.Join("testdata", "plugins", "sdlc", "skills", "plan", "lens-risk-prompt.md")
		if got, ok := idx["lens-risk-prompt.md"]; !ok || got != wantLens {
			t.Errorf("idx[%q] = (%q, %v), want (%q, true)", "lens-risk-prompt.md", got, ok, wantLens)
		}
	})

	t.Run("no env var and no plugins walk returns empty index", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("CLAUDE_PLUGIN_ROOT", "")
		resetSkillTemplateIndex()

		idx := buildSkillTemplateIndex()

		if len(idx) != 0 {
			t.Errorf("buildSkillTemplateIndex() = %v, want empty index", idx)
		}
	})
}
