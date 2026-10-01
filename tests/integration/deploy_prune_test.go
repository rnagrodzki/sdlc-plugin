//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// scriptPath returns the absolute path to scripts/prune-cache-bin.sh,
// resolved the same way runTests resolves the repo root to build the sdlc
// binary (tests run from tests/integration, two levels below repo root).
func scriptPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(wd, "..", "..", "scripts", "prune-cache-bin.sh")
}

// runPrune runs scripts/prune-cache-bin.sh against dir/keep for the given
// os/arch and fails the test if the script exits non-zero.
func runPrune(t *testing.T, dir, keep, os_, arch string) {
	t.Helper()
	cmd := exec.Command("bash", scriptPath(t), dir, keep, os_, arch)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("prune-cache-bin.sh failed: %v\n%s", err, out)
	}
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestDeployPruneScript drives scripts/prune-cache-bin.sh (invoked by
// Taskfile.yml's deploy task after the new binary is copied and signed) as
// a real bash subprocess against a scratch directory, so a bug in the glob
// or the keep-path comparison shows up as a failing test instead of a
// silently wrong `task deploy` run.
func TestDeployPruneScript(t *testing.T) {
	t.Run("removes old binaries for the same os/arch, keeps current and other os/arch", func(t *testing.T) {
		dir := t.TempDir()
		old := filepath.Join(dir, "sdlc-0.1.0-darwin-arm64")
		oldSigned := old + ".signed"
		keep := filepath.Join(dir, "sdlc-0.2.0-darwin-arm64")
		otherArch := filepath.Join(dir, "sdlc-0.1.0-linux-amd64")

		for _, p := range []string{old, oldSigned, keep, otherArch} {
			mustWriteFile(t, p, "x")
		}

		runPrune(t, dir, keep, "darwin", "arm64")

		got := listDir(t, dir)
		want := map[string]bool{
			"sdlc-0.2.0-darwin-arm64": true,
			"sdlc-0.1.0-linux-amd64":  true,
		}
		if len(got) != len(want) {
			t.Fatalf("dir contents = %v, want exactly %v", got, want)
		}
		for _, name := range got {
			if !want[name] {
				t.Errorf("unexpected file left behind: %s (dir contents: %v)", name, got)
			}
		}
	})

	t.Run("empty dir is a no-op", func(t *testing.T) {
		dir := t.TempDir()
		keep := filepath.Join(dir, "sdlc-0.2.0-darwin-arm64")

		runPrune(t, dir, keep, "darwin", "arm64")

		if got := listDir(t, dir); len(got) != 0 {
			t.Errorf("dir contents = %v, want empty", got)
		}
	})

	t.Run("dir with only the keep file removes nothing", func(t *testing.T) {
		dir := t.TempDir()
		keep := filepath.Join(dir, "sdlc-0.2.0-darwin-arm64")
		mustWriteFile(t, keep, "x")

		runPrune(t, dir, keep, "darwin", "arm64")

		got := listDir(t, dir)
		if len(got) != 1 || got[0] != "sdlc-0.2.0-darwin-arm64" {
			t.Errorf("dir contents = %v, want only the keep file", got)
		}
	})
}
