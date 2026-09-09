package ghx

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ── ParseRemoteOwner table test ─────────────────────────────────────────

func TestParseRemoteOwner(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		// HTTPS
		{
			name:      "https with .git",
			url:       "https://github.com/owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
		},
		{
			name:      "https without .git",
			url:       "https://github.com/owner/repo",
			wantOwner: "owner",
			wantRepo:  "repo",
		},
		// git@
		{
			name:      "git@ with .git",
			url:       "git@github.com:owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
		},
		{
			name:      "git@ without .git",
			url:       "git@github.com:owner/repo",
			wantOwner: "owner",
			wantRepo:  "repo",
		},
		// ssh://
		{
			name:      "ssh with .git",
			url:       "ssh://git@github.com/owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
		},
		{
			name:      "ssh without .git",
			url:       "ssh://git@github.com/owner/repo",
			wantOwner: "owner",
			wantRepo:  "repo",
		},
		// Error cases
		{
			name:    "empty string",
			url:     "",
			wantErr: true,
		},
		{
			name:    "unsupported scheme",
			url:     "ftp://github.com/owner/repo",
			wantErr: true,
		},
		{
			name:    "https missing repo",
			url:     "https://github.com/owner",
			wantErr: true,
		},
		{
			name:    "git@ missing colon",
			url:     "git@github.com/owner/repo",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, err := ParseRemoteOwner(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for URL %q, got owner=%q repo=%q", tt.url, owner, repo)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for URL %q: %v", tt.url, err)
			}
			if owner != tt.wantOwner {
				t.Errorf("owner: got %q, want %q", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo: got %q, want %q", repo, tt.wantRepo)
			}
		})
	}
}

// ── gh CLI command tests via PATH-stub ──────────────────────────────────

// stubGH creates a fake "gh" script in a temp directory that prints a
// known string or exits non-zero, then prepends that directory to PATH so
// execx.Run picks up the stub instead of the real gh binary.
// It returns a cleanup function that restores PATH.
func stubGH(t *testing.T, script string) func() {
	t.Helper()

	dir := t.TempDir()
	var name string
	if runtime.GOOS == "windows" {
		name = "gh.bat"
	} else {
		name = "gh"
	}

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing stub gh: %v", err)
	}

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", dir+string(os.PathListSeparator)+origPath)

	return func() {
		os.Setenv("PATH", origPath)
	}
}

func TestPRView(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho \"PR #42 title line\"\n")
	defer cleanup()

	out, err := PRView(".", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "PR #42 title line" {
		t.Errorf("got %q, want %q", out, "PR #42 title line")
	}
}

func TestPRChecks(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho \"All checks passed\"\n")
	defer cleanup()

	out, err := PRChecks(".", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "All checks passed" {
		t.Errorf("got %q, want %q", out, "All checks passed")
	}
}

// TestPRChecksWithExitCode_PreservesStdoutOnFailure checks the fix for the
// bug where every failed/pending `gh pr checks` result (non-zero exit) was
// misclassified as a generic infra error because PRChecks (via execx.Run)
// discards stdout on any non-zero exit. PRChecksWithExitCode must return
// the check output alongside the real exit code instead.
func TestPRChecksWithExitCode_PreservesStdoutOnFailure(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nprintf 'lint\\tfail\\t30s\\thttps://x\\n'\nexit 1\n")
	defer cleanup()

	out, exitCode, err := PRChecksWithExitCode(".", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 1 {
		t.Errorf("got exitCode %d, want 1", exitCode)
	}
	want := "lint\tfail\t30s\thttps://x"
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestIssueView(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho \"Issue PROJ-99 details\"\n")
	defer cleanup()

	out, err := IssueView(".", "PROJ-99")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "Issue PROJ-99 details" {
		t.Errorf("got %q, want %q", out, "Issue PROJ-99 details")
	}
}

func TestAuthStatus(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho \"Logged in as user\"\n")
	defer cleanup()

	out, err := AuthStatus(".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "Logged in as user" {
		t.Errorf("got %q, want %q", out, "Logged in as user")
	}
}

func TestGHCommandError(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nexit 1\n")
	defer cleanup()

	_, err := PRView(".", 1)
	if err == nil {
		t.Fatal("expected error from failing gh command")
	}
}

// ── Missing gh binary ───────────────────────────────────────────────────

func TestMissingGH_YieldsErrGHNotFound(t *testing.T) {
	// Point PATH to an empty directory so gh cannot be found.
	emptyDir := t.TempDir()
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", emptyDir)
	defer os.Setenv("PATH", origPath)

	_, err := PRView(".", 1)
	if err == nil {
		t.Fatal("expected error when gh is missing")
	}
	if !errors.Is(err, ErrGHNotFound) {
		t.Errorf("expected ErrGHNotFound in chain, got: %v", err)
	}
}
