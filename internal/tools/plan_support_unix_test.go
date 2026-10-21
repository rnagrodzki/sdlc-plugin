//go:build unix

package tools

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestPlanPreplanCheck_NamedPipeRefused checks the default arm of the
// file-mode check of preplan_check: a named pipe is neither a symlink nor a
// folder, so the message calls it "not a regular file". The pipe is never
// opened, so the call does not block.
func TestPlanPreplanCheck_NamedPipeRefused(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sdlc-v2", "preplan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(dir, "auth-flow.md")
	if err := syscall.Mkfifo(pipe, 0o644); err != nil {
		t.Skipf("named pipe not supported: %v", err)
	}
	noPreplanCheckWrite(t)

	out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if got := errorClassOf(err); got != "domain" {
		t.Errorf("error class = %q, want domain (err %v)", got, err)
	}
	if want := "preplan_check: " + pipe + " is not a regular file"; err.Error() != want {
		t.Errorf("err.Error() = %q, want %q", err.Error(), want)
	}
	if want := "Make " + pipe + " a regular file (replace the link with the file it points to), then call preplan_check again."; suggestionOf(err) != want {
		t.Errorf("Suggestion = %q, want %q", suggestionOf(err), want)
	}
	if out.GuardrailCheckLine != "" || out.PreplanFile != "" {
		t.Errorf("output = %+v, want empty", out)
	}
}
