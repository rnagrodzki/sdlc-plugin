package fsx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// mustSymlink creates a symlink at link that points to target, and fails the
// test when it cannot.
func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink %s -> %s: %v", link, target, err)
	}
}

// mustMkdir creates dir and all missing parents, and fails the test when it
// cannot.
func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
}

func TestMkdirAll(t *testing.T) {
	t.Run("normal path creates the directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "a", "b", "c")

		if err := MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll: unexpected error: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("expected directory at %s, got info=%v err=%v", path, info, err)
		}
	})

	t.Run("existing directory is not an error", func(t *testing.T) {
		path := t.TempDir()

		if err := MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll: unexpected error: %v", err)
		}
	})

	t.Run("path under a live link behaves like os.MkdirAll", func(t *testing.T) {
		dir := t.TempDir()
		liveDir := filepath.Join(dir, "liveDir")
		mustMkdir(t, liveDir)
		link := filepath.Join(dir, "link")
		mustSymlink(t, liveDir, link)

		if err := MkdirAll(filepath.Join(link, "sub", "deeper"), 0o755); err != nil {
			t.Fatalf("MkdirAll: unexpected error: %v", err)
		}
		if info, err := os.Stat(filepath.Join(liveDir, "sub", "deeper")); err != nil || !info.IsDir() {
			t.Fatalf("expected directory behind the link, got info=%v err=%v", info, err)
		}
	})

	t.Run("dangling link at the last element", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "state")
		target := filepath.Join(dir, "missing", "state")
		mustSymlink(t, target, link)

		err := MkdirAll(link, 0o755)

		var de *DanglingLinkError
		if !errors.As(err, &de) {
			t.Fatalf("expected *DanglingLinkError, got %T: %v", err, err)
		}
		if de.Link != link {
			t.Fatalf("Link: got %q, want %q", de.Link, link)
		}
		if de.Target != target {
			t.Fatalf("Target: got %q, want %q", de.Target, target)
		}
		if de.Remove {
			t.Fatalf("Remove: expected false for a folder link")
		}
		if !errors.Is(err, fs.ErrExist) {
			t.Fatalf("expected the os error as cause (fs.ErrExist), got %v", err)
		}
		if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("MkdirAll must not create the target: Lstat err=%v", statErr)
		}
	})

	t.Run("dangling link at a parent", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "state")
		target := filepath.Join(dir, "missing", "state")
		mustSymlink(t, target, link)

		err := MkdirAll(filepath.Join(link, "sub", "deeper"), 0o755)

		var de *DanglingLinkError
		if !errors.As(err, &de) {
			t.Fatalf("expected *DanglingLinkError, got %T: %v", err, err)
		}
		if de.Link != link || de.Target != target {
			t.Fatalf("got link=%q target=%q, want link=%q target=%q", de.Link, de.Target, link, target)
		}
		if !errors.Is(err, fs.ErrExist) {
			t.Fatalf("expected the os error as cause (fs.ErrExist), got %v", err)
		}
	})

	t.Run("relative target is returned absolute", func(t *testing.T) {
		dir := t.TempDir()
		mustMkdir(t, filepath.Join(dir, "wt"))
		link := filepath.Join(dir, "wt", "state")
		mustSymlink(t, filepath.Join("..", "main", "state"), link)

		err := MkdirAll(link, 0o755)

		var de *DanglingLinkError
		if !errors.As(err, &de) {
			t.Fatalf("expected *DanglingLinkError, got %T: %v", err, err)
		}
		if want := filepath.Join(dir, "main", "state"); de.Target != want {
			t.Fatalf("Target: got %q, want %q", de.Target, want)
		}
	})

	t.Run("regular file in the path returns the os error unchanged", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "file")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed WriteFile: %v", err)
		}

		for _, path := range []string{file, filepath.Join(file, "sub")} {
			want := os.MkdirAll(path, 0o755)
			if want == nil {
				t.Fatalf("os.MkdirAll(%s): expected an error", path)
			}

			got := MkdirAll(path, 0o755)

			var de *DanglingLinkError
			if errors.As(got, &de) {
				t.Fatalf("MkdirAll(%s): unexpected *DanglingLinkError: %v", path, got)
			}
			if got == nil || got.Error() != want.Error() {
				t.Fatalf("MkdirAll(%s): got %v, want %v", path, got, want)
			}
		}
	})

	t.Run("link loop returns the os error unchanged", func(t *testing.T) {
		dir := t.TempDir()
		a := filepath.Join(dir, "a")
		b := filepath.Join(dir, "b")
		mustSymlink(t, "b", a)
		mustSymlink(t, "a", b)

		want := os.MkdirAll(a, 0o755)
		if want == nil {
			t.Fatalf("os.MkdirAll(%s): expected an error", a)
		}

		got := MkdirAll(a, 0o755)

		var de *DanglingLinkError
		if errors.As(got, &de) {
			t.Fatalf("unexpected *DanglingLinkError for a link loop: %v", got)
		}
		if got == nil || got.Error() != want.Error() {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
}

func TestFindDanglingLink(t *testing.T) {
	t.Run("absolute target at the last element", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "link")
		target := filepath.Join(dir, "missing")
		mustSymlink(t, target, link)

		gotLink, gotTarget, ok := FindDanglingLink(link)

		if !ok || gotLink != link || gotTarget != target {
			t.Fatalf("got link=%q target=%q ok=%v, want link=%q target=%q ok=true", gotLink, gotTarget, ok, link, target)
		}
	})

	t.Run("relative target is joined to the link directory", func(t *testing.T) {
		dir := t.TempDir()
		mustMkdir(t, filepath.Join(dir, "sub"))
		link := filepath.Join(dir, "sub", "link")
		mustSymlink(t, filepath.Join("..", "missing", "x"), link)

		gotLink, gotTarget, ok := FindDanglingLink(link)

		want := filepath.Join(dir, "missing", "x")
		if !ok || gotLink != link || gotTarget != want {
			t.Fatalf("got link=%q target=%q ok=%v, want link=%q target=%q ok=true", gotLink, gotTarget, ok, link, want)
		}
	})

	t.Run("dangling link at a parent", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "link")
		target := filepath.Join(dir, "missing")
		mustSymlink(t, target, link)

		gotLink, gotTarget, ok := FindDanglingLink(filepath.Join(link, "a", "b"))

		if !ok || gotLink != link || gotTarget != target {
			t.Fatalf("got link=%q target=%q ok=%v, want link=%q target=%q ok=true", gotLink, gotTarget, ok, link, target)
		}
	})

	t.Run("chain returns the last link and its missing target", func(t *testing.T) {
		dir := t.TempDir()
		linkA := filepath.Join(dir, "A")
		linkB := filepath.Join(dir, "B")
		missing := filepath.Join(dir, "missing")
		mustSymlink(t, "B", linkA)
		mustSymlink(t, missing, linkB)

		gotLink, gotTarget, ok := FindDanglingLink(linkA)

		if !ok || gotLink != linkB || gotTarget != missing {
			t.Fatalf("got link=%q target=%q ok=%v, want link=%q target=%q ok=true", gotLink, gotTarget, ok, linkB, missing)
		}
	})

	t.Run("live link is not a hit", func(t *testing.T) {
		dir := t.TempDir()
		liveDir := filepath.Join(dir, "liveDir")
		mustMkdir(t, liveDir)
		link := filepath.Join(dir, "link")
		mustSymlink(t, liveDir, link)

		for _, path := range []string{link, filepath.Join(link, "missing", "deeper")} {
			if gotLink, gotTarget, ok := FindDanglingLink(path); ok {
				t.Fatalf("FindDanglingLink(%s): unexpected hit link=%q target=%q", path, gotLink, gotTarget)
			}
		}
	})

	t.Run("regular file is not a hit", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "file")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed WriteFile: %v", err)
		}

		if gotLink, gotTarget, ok := FindDanglingLink(filepath.Join(file, "sub")); ok {
			t.Fatalf("unexpected hit link=%q target=%q", gotLink, gotTarget)
		}
	})

	t.Run("path that does not exist and has no link above it", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "a", "b")

		if gotLink, gotTarget, ok := FindDanglingLink(path); ok {
			t.Fatalf("unexpected hit link=%q target=%q", gotLink, gotTarget)
		}
	})

	t.Run("link loop is not a hit", func(t *testing.T) {
		dir := t.TempDir()
		a := filepath.Join(dir, "a")
		b := filepath.Join(dir, "b")
		mustSymlink(t, "b", a)
		mustSymlink(t, "a", b)

		if gotLink, gotTarget, ok := FindDanglingLink(a); ok {
			t.Fatalf("unexpected hit link=%q target=%q", gotLink, gotTarget)
		}
	})

	t.Run("chain longer than the limit is not a hit", func(t *testing.T) {
		dir := t.TempDir()
		const count = maxLinkChain + 5
		for i := 0; i < count; i++ {
			next := filepath.Join(dir, fmt.Sprintf("l%d", i+1))
			if i == count-1 {
				next = filepath.Join(dir, "missing")
			}
			mustSymlink(t, next, filepath.Join(dir, fmt.Sprintf("l%d", i)))
		}

		if gotLink, gotTarget, ok := FindDanglingLink(filepath.Join(dir, "l0")); ok {
			t.Fatalf("unexpected hit link=%q target=%q", gotLink, gotTarget)
		}
	})
}

func TestDanglingLinkError(t *testing.T) {
	cause := fmt.Errorf("mkdir /x: %w", fs.ErrExist)

	tests := []struct {
		name         string
		remove       bool
		wantRecovery string
		wantError    string
	}{
		{
			name:         "folder link",
			remove:       false,
			wantRecovery: "Start a new session so the session-start hook repairs the link, or run: mkdir -p /main/.sdlc-v2/preplan",
			wantError:    "/wt/.sdlc-v2/preplan is a link to /main/.sdlc-v2/preplan, which does not exist. Start a new session so the session-start hook repairs the link, or run: mkdir -p /main/.sdlc-v2/preplan",
		},
		{
			name:         "file link",
			remove:       true,
			wantRecovery: "Remove the link, then try again: rm /wt/.sdlc-v2/preplan",
			wantError:    "/wt/.sdlc-v2/preplan is a link to /main/.sdlc-v2/preplan, which does not exist. Remove the link, then try again: rm /wt/.sdlc-v2/preplan",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &DanglingLinkError{
				Link:   "/wt/.sdlc-v2/preplan",
				Target: "/main/.sdlc-v2/preplan",
				Remove: tt.remove,
				Err:    cause,
			}

			if got := e.Recovery(); got != tt.wantRecovery {
				t.Fatalf("Recovery:\n got: %q\nwant: %q", got, tt.wantRecovery)
			}
			if got := e.Error(); got != tt.wantError {
				t.Fatalf("Error:\n got: %q\nwant: %q", got, tt.wantError)
			}
			if got := e.Unwrap(); got != cause {
				t.Fatalf("Unwrap: got %v, want %v", got, cause)
			}
			if !errors.Is(e, fs.ErrExist) {
				t.Fatalf("expected errors.Is(err, fs.ErrExist) through Unwrap")
			}
		})
	}
}
