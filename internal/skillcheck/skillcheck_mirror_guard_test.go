// Package skillcheck pins the way harden and setup-dimensions handle a Copilot
// mirror that dimensions_render_instructions refuses. The one declared Test
// function is TestHardenSkillsMirrorRefusalRoute.
package skillcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHardenSkillsMirrorRefusalRoute pins the /harden route for a refused
// mirror render, the setup-dimensions overwrite step, and the refusal text
// against the Go source, so a rename on either side fails here.
func TestHardenSkillsMirrorRefusalRoute(t *testing.T) {
	root := setupSkillsRepoRoot(t)
	read := func(rel string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	const refusal = "has hand-written content"
	if !strings.Contains(read("../../internal/tools/dimensions_render.go"), refusal) {
		t.Errorf("internal/tools/dimensions_render.go lacks %q", refusal)
	}
	harden := read("skills/harden/SKILL.md")
	for _, want := range []string{
		"| mirror render refused with `has hand-written content` |",
		"**Existing mirror already present:** each render ends the mirror with a marker line",
		"2. **On a render or delete error other than the hand-written refusal",
		"`; Copilot mirror skipped: hand-written content in {path}`",
	} {
		hardenSkillsAssertOnce(t, "harden/SKILL.md", harden, want)
	}
	dims := read("skills/setup/setup-dimensions.md")
	for _, want := range []string{"overwrite: true,", "| The error says `has hand-written content` |"} {
		if !strings.Contains(dims, want) {
			t.Errorf("setup-dimensions.md lacks %q", want)
		}
	}
}
