//go:build unix

package tools

import (
	"path/filepath"
	"syscall"
	"testing"
)

// TestDashboardSetPreplanStatus_NamedPipeRefused checks the default arm of the
// file-mode check: a named pipe is neither a symlink nor a folder, so the
// message calls it "not a regular file". The pipe is never opened, so the
// call does not block.
func TestDashboardSetPreplanStatus_NamedPipeRefused(t *testing.T) {
	root := t.TempDir()
	pipe := filepath.Join(dashDelPreplanDir(t, root), "topic.md")
	if err := syscall.Mkfifo(pipe, 0o644); err != nil {
		t.Skipf("named pipe not supported: %v", err)
	}
	setStatusNoWrite(t)

	out, err := DashboardSetPreplanStatus(root, "topic", "paused")
	dashDelWantErr(t, err, "domain", "Change the status by hand in the target file.")
	setStatusWantMessage(t, err, "The path .sdlc-v2/preplan/topic.md is not a regular file, not a topic file that the dashboard can rewrite")
	setStatusZero(t, out)
}
