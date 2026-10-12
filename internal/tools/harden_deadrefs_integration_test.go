//go:build integration

package tools

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// TestIntegration_PrepareOrchestratorHarden_ListsDeadReferences calls the
// registered prepare_orchestrator tool in harden mode on a temporary git
// repository whose guardrail cites a removed tool, a live tool, a tracked
// path and a missing path. The handler passes the server as the tool catalog
// and the real git ls-files reads the index, so the manifest on disk lists
// the removed tool and the missing path only, and has no "no tool catalog"
// or path skip note.
func TestIntegration_PrepareOrchestratorHarden_ListsDeadReferences(t *testing.T) {
	redirectTempDir(t)
	newDeadRefPlugin(t, map[string]string{"ship": deadRefShipSkill})
	dir := newOrchestratorRepo(t, ""+
		"[execute.guardrails.old-tool]\n"+
		"description = \"Call `harden_prepare()` and then `prepare_orchestrator()`. Read `.sdlc-v2/config.toml`, not `internal/gone.go`.\"\n")
	configPath := filepath.Join(paths.DataDir, paths.ConfigFile)
	if out, err := exec.Command("git", "-C", dir, "add", "--", configPath).CombinedOutput(); err != nil {
		t.Fatalf("git add %s: %v: %s", configPath, err, out)
	}

	res, text := callRegisteredPrepareOrchestratorText(t, map[string]any{
		"mode":            "harden",
		"skill":           "ship",
		"failureText":     "boom",
		"skipConfigCheck": true,
	})
	if res.IsError {
		t.Fatalf("prepare_orchestrator returned a tool error:\n%s", text)
	}

	surfaces, ok := readHardenManifest(t, manifestPathFromText(t, text))["surfaces"].(map[string]any)
	if !ok {
		t.Fatal("manifest surfaces is missing or not an object")
	}
	refs, ok := surfaces["deadReferences"].([]any)
	if !ok {
		t.Fatalf("surfaces.deadReferences = %#v, want an array", surfaces["deadReferences"])
	}
	var hits []string
	for _, r := range refs {
		ref, _ := r.(map[string]any)
		if ref["surface"] != "execute-guardrails" || ref["ruleId"] != "old-tool" {
			t.Errorf("unexpected rule identity: %v", ref)
		}
		hits = append(hits, fmt.Sprintf("%v:%v", ref["kind"], ref["token"]))
	}
	if want := []string{"tool:harden_prepare", "path:internal/gone.go"}; !slices.Equal(hits, want) {
		t.Errorf("dead references = %v, want %v", hits, want)
	}

	skips, ok := surfaces["deadReferenceSkips"].([]any)
	if !ok {
		t.Fatalf("surfaces.deadReferenceSkips = %#v, want an array", surfaces["deadReferenceSkips"])
	}
	notes := make([]string, 0, len(skips))
	for _, s := range skips {
		notes = append(notes, strings.TrimSpace(s.(string)))
	}
	if slices.Contains(notes, "tool and action checks skipped: no tool catalog") {
		t.Errorf("the registered handler must pass the tool catalog; skips = %v", notes)
	}
	for _, n := range notes {
		if strings.HasPrefix(n, "path check skipped") {
			t.Errorf("the real git ls-files must succeed in a repository; skips = %v", notes)
		}
	}
}

// TestIntegration_HardenGitLsFiles_OutsideRepository checks that the real
// hardenGitLsFiles returns an error for a directory that is not in a git
// repository.
func TestIntegration_HardenGitLsFiles_OutsideRepository(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	files, err := hardenGitLsFiles(dir)
	if err == nil {
		t.Errorf("hardenGitLsFiles outside a repository = %v, want an error", files)
	}
}

// TestIntegration_HardenDeadRefs_GitLsFilesListsTrackedFiles checks that the
// real hardenGitLsFiles returns the tracked paths of a repository exactly, in
// git's order, with a name that holds a space kept whole and no empty entry for
// the NUL that ends the git output, and that it returns no path for a
// repository with no tracked file.
func TestIntegration_HardenDeadRefs_GitLsFilesListsTrackedFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	files, err := hardenGitLsFiles(dir)
	if err != nil {
		t.Fatalf("hardenGitLsFiles on an empty repository: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("hardenGitLsFiles on an empty repository = %q, want no path", files)
	}

	writeFile(t, filepath.Join(dir, "a.txt"), "a")
	writeFile(t, filepath.Join(dir, "sub", "b c.txt"), "b")
	if out, err := exec.Command("git", "-C", dir, "add", "--", "a.txt", "sub/b c.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	files, err = hardenGitLsFiles(dir)
	if err != nil {
		t.Fatalf("hardenGitLsFiles: %v", err)
	}
	if want := []string{"a.txt", "sub/b c.txt"}; !slices.Equal(files, want) {
		t.Errorf("hardenGitLsFiles = %q, want %q", files, want)
	}
}
