package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// readExploreManifest runs buildExplorePack, removes its temp dir when the
// test ends, and returns the decoded manifest.
func readExploreManifest(t *testing.T, mainRoot, contentRoot, fromOpenspec string) exploreManifest {
	t.Helper()
	redirectTempManifests(t)
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

// TestPlanExplorePrepare_RemovesStaleExploreDirs pins the explore-dir
// cleanup: each call removes sdlc-explore-* directories older than 24h, keeps
// younger ones and the one it just wrote, and leaves other directories alone,
// including an old sdlc-commit-manifest-* one.
func TestPlanExplorePrepare_RemovesStaleExploreDirs(t *testing.T) {
	root := redirectTempManifests(t)
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	build := func() string {
		t.Helper()
		pack := buildExplorePack(dir, dir, "", "")
		if pack.Error != nil || pack.OutDir == nil || pack.ManifestPath == nil {
			t.Fatalf("buildExplorePack: pack = %+v", pack)
		}
		return *pack.OutDir
	}
	age := func(path string, d time.Duration) {
		t.Helper()
		old := time.Now().Add(-d)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	stale := build()
	age(stale, 48*time.Hour)
	recent := build()
	age(recent, time.Hour)
	other := filepath.Join(root, commitManifestPrefix+"old")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	age(other, 48*time.Hour)

	current := build()

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("explore dir older than 24h should be removed, stat err = %v", err)
	}
	for _, keep := range []string{recent, current, other} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("%s should be kept: %v", filepath.Base(keep), err)
		}
	}
	if _, err := os.Stat(filepath.Join(current, "manifest.json")); err != nil {
		t.Errorf("current manifest must stay readable: %v", err)
	}
}

// TestPlanExplorePrepare_ScopeHintsFromCapabilitySpecs pins that OpenSpec
// scope hints come from the change's proposal.md and every delta spec the
// openspec CLI reports for the change, including ones nested below
// <capability>/ (at any depth — the CLI is the enumeration source, not a
// one-level directory walk).
func TestPlanExplorePrepare_ScopeHintsFromCapabilitySpecs(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	change := filepath.Join(dir, "openspec", "changes", "add-widget")
	writeRepoFile(t, change, "proposal.md", "Touches `internal/proposal/a.go`.\n")
	writeRepoFile(t, change, "specs/widget/spec.md", "Change `internal/widget/render.go`.\n")
	writeRepoFile(t, change, "specs/notes.md", "See `internal/notes/n.go`.\n")
	writeRepoFile(t, change, "specs/deep/nested/spec.md", "Now read: `internal/deep/x.go`.\n")
	proposal := filepath.Join(change, "proposal.md")
	widgetSpec := filepath.Join(change, "specs", "widget", "spec.md")
	notesSpec := filepath.Join(change, "specs", "notes.md")
	deepSpec := filepath.Join(change, "specs", "deep", "nested", "spec.md")

	stubOpenspecCLI(t, map[string]openspecCLIStub{
		"status --change add-widget --json": {stdout: openspecStatusStubJSON(t, "add-widget", map[string][]string{
			"proposal": {proposal},
			"specs":    {widgetSpec, notesSpec, deepSpec},
		})},
	})

	m := readExploreManifest(t, dir, dir, "add-widget")

	got := map[string]bool{}
	for _, f := range m.ScopeHintFiles {
		got[f] = true
	}
	for _, want := range []string{"internal/proposal/a.go", "internal/widget/render.go", "internal/notes/n.go", "internal/deep/x.go"} {
		if !got[want] {
			t.Errorf("scopeHintFiles = %v, missing %q", m.ScopeHintFiles, want)
		}
	}
}

// TestNestedDeltaSpecs verifies getOpenSpecPaths reads a delta spec nested
// arbitrarily deep under specs/ (e.g. specs/identity/user-auth/spec.md), and
// that a missing openspec CLI yields no OpenSpec hints without erroring.
func TestNestedDeltaSpecs(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	change := filepath.Join(dir, "openspec", "changes", "add-auth")
	writeRepoFile(t, change, "proposal.md", "# Proposal\n")
	writeRepoFile(t, change, "specs/identity/user-auth/spec.md", "Touches `internal/auth/login.go`.\n")
	proposal := filepath.Join(change, "proposal.md")
	authSpec := filepath.Join(change, "specs", "identity", "user-auth", "spec.md")

	statusJSON := openspecStatusStubJSON(t, "add-auth", map[string][]string{
		"proposal": {proposal},
		"specs":    {authSpec},
	})

	t.Run("CLI available", func(t *testing.T) {
		stubOpenspecCLI(t, map[string]openspecCLIStub{
			"status --change add-auth --json": {stdout: statusJSON},
		})

		m := readExploreManifest(t, dir, dir, "add-auth")

		got := map[string]bool{}
		for _, f := range m.ScopeHintFiles {
			got[f] = true
		}
		if !got["internal/auth/login.go"] {
			t.Errorf("scopeHintFiles = %v, missing nested delta spec hint %q", m.ScopeHintFiles, "internal/auth/login.go")
		}
	})

	t.Run("CLI unavailable", func(t *testing.T) {
		pathWithoutOpenspec(t)

		m := readExploreManifest(t, dir, dir, "add-auth")

		for _, f := range m.ScopeHintFiles {
			if f == "internal/auth/login.go" {
				t.Errorf("scopeHintFiles = %v, should have no OpenSpec hints when the CLI is unavailable", m.ScopeHintFiles)
			}
		}
	})
}

// TestPlanExplorePrepare_RelativePlansDirectoryResolvesFromWorkspaceRoot pins
// that a relative plansDirectory in the project settings resolves against the
// workspace (main worktree) root, not the process working directory.
func TestPlanExplorePrepare_RelativePlansDirectoryResolvesFromWorkspaceRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no ~/.claude: no global setting, no fallback dir
	root := t.TempDir()
	writeRepoFile(t, root, ".claude/settings.json", `{"plansDirectory": "docs/plans"}`)
	writeRepoFile(t, root, "docs/plans/2026-10-01-widget.md", "# plan\n")
	// Run from a different directory, so a cwd-relative lookup finds nothing.
	t.Chdir(t.TempDir())

	m := readExploreManifest(t, root, root, "")

	if len(m.RecentPlans) != 1 || m.RecentPlans[0] != "2026-10-01-widget.md" {
		t.Errorf("recentPlans = %v, want [2026-10-01-widget.md]", m.RecentPlans)
	}
}
