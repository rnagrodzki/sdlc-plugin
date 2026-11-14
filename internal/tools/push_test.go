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

func TestPushCurrentBranch_Works(t *testing.T) {
	clone, bare := pushRepo(t)
	pushed, warning := pushCurrentBranch(clone)
	if !pushed || warning != "" {
		t.Fatalf("got %v, %q; want true, empty", pushed, warning)
	}
	if got, want := pushGit(t, bare, "rev-parse", "feature"), pushGit(t, clone, "rev-parse", "HEAD"); got != want {
		t.Fatalf("remote feature = %s, want %s", got, want)
	}
}

func TestPushCurrentBranch_DefaultBranch(t *testing.T) {
	clone, bare := pushRepo(t)
	pushGit(t, clone, "checkout", "main")
	before := pushGit(t, bare, "rev-parse", "main")
	pushGit(t, clone, "commit", "--allow-empty", "-m", "on main")
	pushed, warning := pushCurrentBranch(clone)
	if pushed || warning != "push skipped: main is the default branch" {
		t.Fatalf("got %v, %q", pushed, warning)
	}
	if got := pushGit(t, bare, "rev-parse", "main"); got != before {
		t.Fatal("the default branch was pushed")
	}
}

func TestPushCurrentBranch_NoOrigin(t *testing.T) {
	dir := t.TempDir()
	pushGit(t, dir, "init", "-b", "feature")
	pushGit(t, dir, "commit", "--allow-empty", "-m", "init")
	pushed, warning := pushCurrentBranch(dir)
	if pushed || warning != "push skipped: no remote origin" {
		t.Fatalf("got %v, %q", pushed, warning)
	}
}

func TestPushCurrentBranch_RealRejected(t *testing.T) {
	clone, bare := pushRepo(t)
	if pushed, warning := pushCurrentBranch(clone); !pushed {
		t.Fatalf("first push: %v, %q", pushed, warning)
	}
	other := t.TempDir()
	pushGit(t, other, "clone", bare, ".")
	pushGit(t, other, "checkout", "feature")
	pushGit(t, other, "commit", "--allow-empty", "-m", "other")
	pushGit(t, other, "push", "origin", "feature")
	pushGit(t, clone, "commit", "--allow-empty", "-m", "local")
	pushed, warning := pushCurrentBranch(clone)
	if pushed || !strings.HasPrefix(warning, "push failed (rejected): ") || !strings.HasSuffix(warning, ". The commit is kept and is not on the remote.") {
		t.Fatalf("got %v, %q", pushed, warning)
	}
}

// fakePushCalls returns pushCalls for a feature branch with a known default
// branch and an origin remote, and a push that works.
func fakePushCalls() pushCalls {
	return pushCalls{
		currentBranch: func(string) (string, error) { return "feature", nil },
		defaultBranch: func(string) (string, error) { return "main", nil },
		hasRemote:     func(string, string) (bool, error) { return true, nil },
		push:          func(string, string) error { return nil },
	}
}

// TestPushParity: the best-effort push turns each failure kind into a
// warning and never an error. The pr_apply push sorts failures with the same
// gitx.PushError kinds.
func TestPushParity(t *testing.T) {
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
			g := fakePushCalls()
			g.push = func(string, string) error { return &gitx.PushError{Kind: row.kind, Stderr: row.stderr} }
			pushed, warning := pushCurrentBranchWith("/repo", g)
			want := "push failed (" + row.kind + "): " + row.stderr + ". The commit is kept and is not on the remote."
			if pushed || warning != want {
				t.Fatalf("got %v, %q; want false, %q", pushed, warning, want)
			}
		})
	}
	t.Run("plain error", func(t *testing.T) {
		g := fakePushCalls()
		g.push = func(string, string) error { return errors.New("boom") }
		pushed, warning := pushCurrentBranchWith("/repo", g)
		if pushed || warning != "push failed (other): boom. The commit is kept and is not on the remote." {
			t.Fatalf("got %v, %q", pushed, warning)
		}
	})
	t.Run("seam default is pushCurrentBranch", func(t *testing.T) {
		clone, bare := pushRepo(t)
		pushed, warning := pushBestEffort(clone)
		if !pushed || warning != "" {
			t.Fatalf("got %v, %q; want true, empty", pushed, warning)
		}
		if got, want := pushGit(t, bare, "rev-parse", "feature"), pushGit(t, clone, "rev-parse", "HEAD"); got != want {
			t.Fatalf("remote feature = %s, want %s", got, want)
		}
	})
}

// TestPushCurrentBranchLookupFailures: a failed git lookup before the push
// names the lookup in the warning, and its error text is capped and has URL
// credentials hidden.
func TestPushCurrentBranchLookupFailures(t *testing.T) {
	secret := errors.New("fatal: https://user:tok123@host/x.git: " + strings.Repeat("x", 400))
	noPush := func(t *testing.T) func(string, string) error {
		return func(string, string) error { t.Fatal("push must not run"); return nil }
	}
	t.Run("current branch fails", func(t *testing.T) {
		g := fakePushCalls()
		g.currentBranch = func(string) (string, error) { return "", secret }
		g.push = noPush(t)
		pushed, warning := pushCurrentBranchWith("/repo", g)
		if pushed || !strings.HasPrefix(warning, "push skipped: cannot read the current branch: fatal: https://***@host/x.git: ") {
			t.Fatalf("got %v, %q", pushed, warning)
		}
		if strings.Contains(warning, "tok123") || !strings.HasSuffix(warning, "...") {
			t.Fatalf("warning not redacted or not capped: %q", warning)
		}
	})
	t.Run("remote lookup fails", func(t *testing.T) {
		g := fakePushCalls()
		g.hasRemote = func(string, string) (bool, error) { return false, errors.New("not a git repository") }
		g.push = noPush(t)
		pushed, warning := pushCurrentBranchWith("/repo", g)
		if want := "push skipped: cannot read remote origin: not a git repository"; pushed || warning != want {
			t.Fatalf("got %v, %q; want false, %q", pushed, warning, want)
		}
	})
	t.Run("default branch lookup fails: the push runs", func(t *testing.T) {
		g := fakePushCalls()
		g.defaultBranch = func(string) (string, error) { return "", errors.New("gitx: cannot auto-detect default branch") }
		var ran bool
		g.push = func(string, string) error { ran = true; return nil }
		pushed, warning := pushCurrentBranchWith("/repo", g)
		want := "default-branch check skipped: gitx: cannot auto-detect default branch. The branch was pushed."
		if !ran || !pushed || warning != want {
			t.Fatalf("got ran=%v %v, %q; want true, %q", ran, pushed, warning, want)
		}
	})
	t.Run("default branch lookup fails and the push fails", func(t *testing.T) {
		g := fakePushCalls()
		g.defaultBranch = func(string) (string, error) { return "", errors.New("no default") }
		g.push = func(string, string) error { return &gitx.PushError{Kind: gitx.PushKindAuth, Stderr: "denied"} }
		pushed, warning := pushCurrentBranchWith("/repo", g)
		want := "default-branch check skipped: no default. push failed (auth): denied. The commit is kept and is not on the remote."
		if pushed || warning != want {
			t.Fatalf("got %v, %q; want false, %q", pushed, warning, want)
		}
	})
	t.Run("current branch fails in a real directory that is not a repository", func(t *testing.T) {
		pushed, warning := pushCurrentBranch(t.TempDir())
		if pushed || !strings.HasPrefix(warning, "push skipped: cannot read the current branch: ") {
			t.Fatalf("got %v, %q", pushed, warning)
		}
	})
}
