package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// readExploreManifest runs buildExplorePack, removes its temp dir when the
// test ends, and returns the decoded manifest.
func readExploreManifest(t *testing.T, mainRoot, contentRoot, fromOpenspec string) exploreManifest {
	t.Helper()
	pack := buildExplorePack(mainRoot, contentRoot, fromOpenspec, "")
	if pack.OutDir != nil {
		outDir := *pack.OutDir
		t.Cleanup(func() { _ = os.RemoveAll(outDir) })
	}
	if pack.Error != nil || pack.ManifestPath == nil {
		t.Fatalf("buildExplorePack: pack = %+v", pack)
	}
	data, err := os.ReadFile(*pack.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m exploreManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return m
}

// TestPlanExplorePrepare_ScopeHintsFromCapabilitySpecs pins that OpenSpec
// scope hints come from the change's specs/<capability>/spec.md files (the
// OpenSpec layout) as well as from proposal.md and any top-level specs/*.md.
func TestPlanExplorePrepare_ScopeHintsFromCapabilitySpecs(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	change := filepath.Join(dir, "openspec", "changes", "add-widget")
	writeRepoFile(t, change, "proposal.md", "Touches `internal/proposal/a.go`.\n")
	writeRepoFile(t, change, "specs/widget/spec.md", "Change `internal/widget/render.go`.\n")
	writeRepoFile(t, change, "specs/notes.md", "See `internal/notes/n.go`.\n")
	writeRepoFile(t, change, "specs/deep/nested/spec.md", "Not read: `internal/deep/x.go`.\n")

	m := readExploreManifest(t, dir, dir, "add-widget")

	got := map[string]bool{}
	for _, f := range m.ScopeHintFiles {
		got[f] = true
	}
	for _, want := range []string{"internal/proposal/a.go", "internal/widget/render.go", "internal/notes/n.go"} {
		if !got[want] {
			t.Errorf("scopeHintFiles = %v, missing %q", m.ScopeHintFiles, want)
		}
	}
	if got["internal/deep/x.go"] {
		t.Errorf("scopeHintFiles = %v, should not read specs nested below <capability>/", m.ScopeHintFiles)
	}
}
