package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// linkedWorktree returns a temp worktree whose runs/ folder is a symlink to
// the runs/ folder of main, as git worktree setup does.
func linkedWorktree(t *testing.T, main string) string {
	t.Helper()
	linked := t.TempDir()
	if err := os.MkdirAll(filepath.Join(linked, paths.DataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(main, paths.DataDir, paths.RunsSubdir), filepath.Join(linked, paths.DataDir, paths.RunsSubdir)); err != nil {
		t.Fatal(err)
	}
	return linked
}

// TestResolveDisplayRoot checks that ResolveDisplayRoot matches a cleaned repo
// path against the registered roots and rejects unknown, empty, parent and
// subfolder paths.
func TestResolveDisplayRoot(t *testing.T) {
	a := dashRoot(t)
	b := dashRoot(t)

	cases := []struct {
		name   string
		roots  []string
		repo   string
		want   string
		wantOK bool
	}{
		{"exact match", []string{a, b}, b, b, true},
		{"unclean path is cleaned", []string{a}, a + "/", a, true},
		{"dot segments are cleaned", []string{a}, filepath.Join(a, "sub") + "/..", a, true},
		{"unknown repo", []string{a}, b, "", false},
		{"empty repo", []string{a}, "", "", false},
		{"no roots", nil, a, "", false},
		{"subfolder of a root", []string{a}, filepath.Join(a, "sub"), "", false},
		{"parent of a root", []string{a}, filepath.Dir(a), "", false},
		{"empty root entry is dropped", []string{"", a}, a, a, true},
		{"duplicate roots match once", []string{a, a}, a, a, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolveDisplayRoot(tc.roots, tc.repo)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("ResolveDisplayRoot(%v, %q) = (%q, %v), want (%q, %v)", tc.roots, tc.repo, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestResolveDisplayRoot_LinkedWorktree checks that a linked worktree root
// resolves to its main root, that the linked path itself is not a display root,
// and that a main root listed in roots is returned as spelled.
func TestResolveDisplayRoot_LinkedWorktree(t *testing.T) {
	main := dashRoot(t)
	linked := linkedWorktree(t, main)
	realMain, err := filepath.EvalSymlinks(main)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("linked root alone resolves to its main root", func(t *testing.T) {
		got, ok := ResolveDisplayRoot([]string{linked}, realMain)
		if !ok || got != realMain {
			t.Errorf("ResolveDisplayRoot([linked], main) = (%q, %v), want (%q, true)", got, ok, realMain)
		}
	})

	t.Run("the linked path itself is not a display root", func(t *testing.T) {
		if got, ok := ResolveDisplayRoot([]string{linked}, linked); ok {
			t.Errorf("ResolveDisplayRoot([linked], linked) = (%q, true), want not ok", got)
		}
	})

	t.Run("main root spelled in roots wins over the derived path", func(t *testing.T) {
		got, ok := ResolveDisplayRoot([]string{linked, main}, main)
		if !ok || got != filepath.Clean(main) {
			t.Errorf("ResolveDisplayRoot([linked, main], main) = (%q, %v), want (%q, true)", got, ok, filepath.Clean(main))
		}
	})
}
