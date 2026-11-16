package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ageEntry sets the modification time of path to d ago.
func ageEntry(t *testing.T, path string, d time.Duration) {
	t.Helper()
	old := time.Now().Add(-d)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// mkScratchEntry makes a folder name under the scratch folder of root, aged d.
func mkScratchEntry(t *testing.T, root, name string, d time.Duration) string {
	t.Helper()
	path := filepath.Join(paths.ScratchDir(root), name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	ageEntry(t, path, d)
	return path
}

// TestScratchMkdirTemp_CreatesFolderUnderScratch pins the happy path: the
// scratch folder is made when absent, and the new folder sits directly in it
// with the given prefix.
func TestScratchMkdirTemp_CreatesFolderUnderScratch(t *testing.T) {
	root := t.TempDir()
	dir, err := scratchMkdirTemp(root, "sdlc-test-")
	if err != nil {
		t.Fatalf("scratchMkdirTemp: %v", err)
	}
	if got, want := filepath.Dir(dir), paths.ScratchDir(root); got != want {
		t.Errorf("parent = %q, want %q", got, want)
	}
	if !strings.HasPrefix(filepath.Base(dir), "sdlc-test-") {
		t.Errorf("dir = %q, want an sdlc-test-* folder", dir)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("dir %q is not a folder: %v", dir, err)
	}
}

// TestScratchMkdirTemp_Errors covers each error row of the contract: an empty
// root, and a root that is a file (MkdirAll of the scratch folder fails).
func TestScratchMkdirTemp_Errors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileScratch := paths.ScratchDir(file)

	cases := []struct {
		name       string
		root       string
		msg        string
		suggestion string
		wantCause  bool
	}{
		{
			name:       "empty root",
			root:       "",
			msg:        "scratch folder: no repo root",
			suggestion: "Run the tool inside a git worktree of a repo that has .sdlc-v2/, then retry.",
		},
		{
			name:       "root is a file",
			root:       file,
			msg:        "create scratch folder " + fileScratch + ": ",
			suggestion: "Check that " + fileScratch + " is a folder, not a file, with write permission and free space, then retry.",
			wantCause:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, err := scratchMkdirTemp(tc.root, "sdlc-test-")
			if dir != "" {
				t.Errorf("dir = %q, want empty on error", dir)
			}
			var ie *mcpserver.InfraError
			if !errors.As(err, &ie) {
				t.Fatalf("error = %T (%v), want *mcpserver.InfraError", err, err)
			}
			if !strings.HasPrefix(ie.Msg, tc.msg) {
				t.Errorf("Msg = %q, want prefix %q", ie.Msg, tc.msg)
			}
			if ie.Suggestion != tc.suggestion {
				t.Errorf("Suggestion = %q, want %q", ie.Suggestion, tc.suggestion)
			}
			if tc.wantCause && ie.Cause == nil {
				t.Error("Cause = nil, want the MkdirAll error")
			}
		})
	}
}

// TestScratchMkdirTemp_SweepsOwnPrefix pins the 24-hour sweep: an old folder
// with the same prefix goes; a young one, an old one with another prefix and
// an old file with the same prefix stay.
func TestScratchMkdirTemp_SweepsOwnPrefix(t *testing.T) {
	root := t.TempDir()
	stale := mkScratchEntry(t, root, "sdlc-test-old", 48*time.Hour)
	young := mkScratchEntry(t, root, "sdlc-test-young", time.Hour)
	other := mkScratchEntry(t, root, "sdlc-other-old", 48*time.Hour)
	oldFile := filepath.Join(paths.ScratchDir(root), "sdlc-test-file")
	if err := os.WriteFile(oldFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ageEntry(t, oldFile, 48*time.Hour)

	dir, err := scratchMkdirTemp(root, "sdlc-test-")
	if err != nil {
		t.Fatalf("scratchMkdirTemp: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("folder older than 24h with the same prefix should be removed, stat err = %v", err)
	}
	for _, keep := range []string{young, other, oldFile, dir} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("%s should stay: %v", filepath.Base(keep), err)
		}
	}
}

// TestScratchMkdirTemp_SweepReadDirFailure pins the first sweep failure row:
// a failed ReadDir stops the sweep, and the call still succeeds.
func TestScratchMkdirTemp_SweepReadDirFailure(t *testing.T) {
	root := t.TempDir()
	stale := mkScratchEntry(t, root, "sdlc-test-old", 48*time.Hour)

	orig := scratchReadDir
	scratchReadDir = func(string) ([]os.DirEntry, error) { return nil, os.ErrPermission }
	t.Cleanup(func() { scratchReadDir = orig })

	dir, err := scratchMkdirTemp(root, "sdlc-test-")
	if err != nil || dir == "" {
		t.Fatalf("scratchMkdirTemp = %q, %v; want success when the sweep cannot read the folder", dir, err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("old entry should stay when ReadDir fails: %v", err)
	}
}

// TestScratchMkdirTemp_SweepRemoveFailure pins the second sweep failure row:
// an entry that cannot be removed stays, the sweep continues to the next
// entry, and the call still succeeds.
func TestScratchMkdirTemp_SweepRemoveFailure(t *testing.T) {
	root := t.TempDir()
	stuck := mkScratchEntry(t, root, "sdlc-test-a-stuck", 48*time.Hour)
	gone := mkScratchEntry(t, root, "sdlc-test-b-gone", 48*time.Hour)

	orig := scratchRemoveAll
	scratchRemoveAll = func(path string) error {
		if path == stuck {
			return fmt.Errorf("simulated remove failure")
		}
		return os.RemoveAll(path)
	}
	t.Cleanup(func() { scratchRemoveAll = orig })

	dir, err := scratchMkdirTemp(root, "sdlc-test-")
	if err != nil || dir == "" {
		t.Fatalf("scratchMkdirTemp = %q, %v; want success when one remove fails", dir, err)
	}
	if _, err := os.Stat(stuck); err != nil {
		t.Errorf("entry whose remove failed should stay: %v", err)
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Errorf("sweep should continue past the failed entry, stat err = %v", err)
	}
}

// TestWriteTempJSON_ScratchErrorKeepsSuggestion pins that writeTempJSON wraps
// the scratchMkdirTemp error so scratchSuggestion still finds its Suggestion,
// and that scratchSuggestion falls back for any other error.
func TestWriteTempJSON_ScratchErrorKeepsSuggestion(t *testing.T) {
	_, err := writeTempJSON("", "sdlc-test-", "thing", func(string) any { return 1 })
	if err == nil {
		t.Fatal("expected an error for an empty root")
	}
	if !strings.Contains(err.Error(), "create thing temp dir: scratch folder: no repo root") {
		t.Errorf("error = %q, want the wrapped scratch error", err.Error())
	}
	want := "Run the tool inside a git worktree of a repo that has .sdlc-v2/, then retry."
	if got := scratchSuggestion(err, "fallback"); got != want {
		t.Errorf("scratchSuggestion = %q, want %q", got, want)
	}
	if got := scratchSuggestion(errors.New("plain"), "fallback"); got != "fallback" {
		t.Errorf("scratchSuggestion(plain) = %q, want the fallback", got)
	}
}
