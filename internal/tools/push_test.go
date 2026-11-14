package tools

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
)

func pushGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@t.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@t.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// pushRepo makes a clone of a bare origin with one commit on main and a
// checked-out feature branch. It returns the clone and the bare origin.
func pushRepo(t *testing.T) (clone, bare string) {
	t.Helper()
	bare = t.TempDir()
	pushGit(t, bare, "init", "--bare", "-b", "main")
	clone = t.TempDir()
	pushGit(t, clone, "clone", bare, ".")
	pushGit(t, clone, "checkout", "-b", "main")
	pushGit(t, clone, "commit", "--allow-empty", "-m", "init")
	pushGit(t, clone, "push", "-u", "origin", "main")
	pushGit(t, clone, "remote", "set-head", "origin", "main")
	pushGit(t, clone, "checkout", "-b", "feature")
	pushGit(t, clone, "commit", "--allow-empty", "-m", "work")
	return clone, bare
}

func TestPushBranchBestEffort_Works(t *testing.T) {
	clone, bare := pushRepo(t)
	pushed, warning := pushBranchBestEffort(clone)
	if !pushed || warning != "" {
		t.Fatalf("got %v, %q; want true, empty", pushed, warning)
	}
	if got, want := pushGit(t, bare, "rev-parse", "feature"), pushGit(t, clone, "rev-parse", "HEAD"); got != want {
		t.Fatalf("remote feature = %s, want %s", got, want)
	}
}

func TestPushBranchBestEffort_DefaultBranch(t *testing.T) {
	clone, bare := pushRepo(t)
	pushGit(t, clone, "checkout", "main")
	before := pushGit(t, bare, "rev-parse", "main")
	pushGit(t, clone, "commit", "--allow-empty", "-m", "on main")
	pushed, warning := pushBranchBestEffort(clone)
	if pushed || warning != "push skipped: main is the default branch" {
		t.Fatalf("got %v, %q", pushed, warning)
	}
	if got := pushGit(t, bare, "rev-parse", "main"); got != before {
		t.Fatal("the default branch was pushed")
	}
}

func TestPushBranchBestEffort_NoOrigin(t *testing.T) {
	dir := t.TempDir()
	pushGit(t, dir, "init", "-b", "feature")
	pushGit(t, dir, "commit", "--allow-empty", "-m", "init")
	pushed, warning := pushBranchBestEffort(dir)
	if pushed || warning != "push skipped: no remote origin" {
		t.Fatalf("got %v, %q", pushed, warning)
	}
}

func TestPushBranchBestEffort_RealRejected(t *testing.T) {
	clone, bare := pushRepo(t)
	if pushed, warning := pushBranchBestEffort(clone); !pushed {
		t.Fatalf("first push: %v, %q", pushed, warning)
	}
	other := t.TempDir()
	pushGit(t, other, "clone", bare, ".")
	pushGit(t, other, "checkout", "feature")
	pushGit(t, other, "commit", "--allow-empty", "-m", "other")
	pushGit(t, other, "push", "origin", "feature")
	pushGit(t, clone, "commit", "--allow-empty", "-m", "local")
	pushed, warning := pushBranchBestEffort(clone)
	if pushed || !strings.HasPrefix(warning, "push failed (rejected): ") || !strings.HasSuffix(warning, ". The pr step pushes again.") {
		t.Fatalf("got %v, %q", pushed, warning)
	}
}

// TestPushParity: the best-effort push turns each failure kind into a
// warning and never an error. The pr column of the contract belongs to
// Task 11; its kinds come from the same gitx.PushError.
func TestPushParity(t *testing.T) {
	clone, _ := pushRepo(t)
	rows := []struct {
		kind, stderr string
	}{
		{gitx.PushKindNoRemote, "fatal: 'origin' does not appear to be a git repository"},
		{gitx.PushKindAuth, "fatal: Authentication failed for 'https://github.com/x/y.git/'"},
		{gitx.PushKindRejected, "! [rejected] feature -> feature (stale info)"},
		{gitx.PushKindNetwork, "fatal: unable to access 'https://x/': Could not resolve host: x"},
		{gitx.PushKindOther, "error: something odd"},
	}
	for _, row := range rows {
		t.Run(row.kind, func(t *testing.T) {
			pushed, warning := pushBestEffortWith(clone, func(string, string) error {
				return &gitx.PushError{Kind: row.kind, Stderr: row.stderr}
			})
			want := "push failed (" + row.kind + "): " + row.stderr + ". The pr step pushes again."
			if pushed || warning != want {
				t.Fatalf("got %v, %q; want false, %q", pushed, warning, want)
			}
		})
	}
	t.Run("plain error", func(t *testing.T) {
		pushed, warning := pushBestEffortWith(clone, func(string, string) error { return errors.New("boom") })
		if pushed || warning != "push failed (other): boom. The pr step pushes again." {
			t.Fatalf("got %v, %q", pushed, warning)
		}
	})
	t.Run("seam default", func(t *testing.T) {
		if pushBestEffort == nil {
			t.Fatal("pushBestEffort seam is nil")
		}
	})
}
