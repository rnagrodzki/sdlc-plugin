// Package skillcheck pins the way the two guardrail sub-flows write to
// config.toml: one "<section>.guardrails.<id>" key for each selected id, never
// a whole-table key. The declared Test functions are
// TestSetupSkillsGuardrailSubflowsWritePerID and
// TestSetupSkillNamesGuardrailRefusal.
package skillcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetupSkillsGuardrailSubflowsWritePerID pins that each guardrail
// sub-flow sends one "<section>.guardrails.<id>" key per selected guardrail.
// setup_write_sections refuses a whole-table key that drops an id, so a
// whole-table key in a sub-flow is a defect. The refusal text and the route
// text are pinned too. The refusal text is also pinned against the Go source,
// so a rename on either side fails here.
func TestSetupSkillsGuardrailSubflowsWritePerID(t *testing.T) {
	root := setupSkillsRepoRoot(t)
	const refusal = "would remove guardrails"
	goSrc, err := os.ReadFile(filepath.Join(root, "..", "..", "internal", "tools", "setup_write.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goSrc), refusal) {
		t.Errorf("internal/tools/setup_write.go lacks %q", refusal)
	}
	for _, tc := range []struct{ rel, section string }{
		{"skills/setup/setup-guardrails.md", "plan"},
		{"skills/setup/setup-execution-guardrails.md", "execute"},
	} {
		raw, err := os.ReadFile(filepath.Join(root, tc.rel))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, banned := range []string{`"` + tc.section + `.guardrails":`, "FULL_GUARDRAILS_TABLE"} {
			if strings.Contains(text, banned) {
				t.Errorf("%s still holds %q", tc.rel, banned)
			}
		}
		for _, want := range []string{`"` + tc.section + `.guardrails.<id1>":`, refusal, "Do not edit `config.toml` by hand"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q", tc.rel, want)
			}
		}
	}
}

// TestSetupSkillNamesGuardrailRefusal pins that setup/SKILL.md names the
// refusal of a whole-table guardrail write, and no longer gives
// plan-guardrails as an example of a whole-table write. The refusal text is
// pinned against the Go source in TestSetupSkillsGuardrailSubflowsWritePerID.
func TestSetupSkillNamesGuardrailRefusal(t *testing.T) {
	root := setupSkillsRepoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "skills", "setup", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	const refusal = "would remove guardrails"
	if !strings.Contains(text, refusal) {
		t.Errorf("setup/SKILL.md lacks %q", refusal)
	}
	const old = "`plan-guardrails`'s `configPath` is `plan.guardrails`"
	if strings.Contains(text, old) {
		t.Errorf("setup/SKILL.md still holds %q", old)
	}
}
