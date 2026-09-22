package paths

import (
	"path/filepath"
	"testing"
)

// TestHistoryDir pins the durable history directory to <root>/.sdlc-v2/history.
// Several packages (internal/hooks, internal/tools) join this path to build a
// history.FileWriter; they must agree on it or a deferred item written by one
// is invisible to the other.
func TestHistoryDir(t *testing.T) {
	root := filepath.Join("/tmp", "repo")
	want := filepath.Join(root, DataDir, HistorySubdir)

	if got := HistoryDir(root); got != want {
		t.Errorf("HistoryDir(%q) = %q, want %q", root, got, want)
	}
	if HistorySubdir != "history" {
		t.Errorf("HistorySubdir = %q, want %q", HistorySubdir, "history")
	}
	if got, want := HistoryDir(root), filepath.Join(ProjectDir(root), HistorySubdir); got != want {
		t.Errorf("HistoryDir must sit under ProjectDir: got %q, want %q", got, want)
	}
}
