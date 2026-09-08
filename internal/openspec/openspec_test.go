package openspec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// stubBinary writes an executable shell script named name into dir, printing
// stdout and exiting with exitCode. Used to stand in for the external
// `openspec` and `git` binaries so tests never depend on either being
// actually installed. Uses only the `printf`/`exit` shell builtins (no `cat`
// or heredoc) so it works even when PATH is scoped down to just the stub
// directory — an external `cat` would otherwise fail to resolve.
func stubBinary(t *testing.T, dir, name, stdout string, exitCode int) {
	t.Helper()
	escaped := strings.ReplaceAll(stdout, "'", `'\''`)
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '%s'\nexit %d\n", escaped, exitCode)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", name, err)
	}
}

// withStubPath replaces PATH with a fresh empty directory (returned) so
// tests are isolated from whatever is actually installed on the host.
// Callers populate it via stubBinary before invoking Detect.
func withStubPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	return dir
}

// stubGitDispatch writes a `git` stub whose output depends on its
// arguments, keyed by the space-joined argument list (e.g.
// "symbolic-ref refs/remotes/origin/HEAD", "rev-parse --verify main",
// "diff --name-only main...HEAD"). Any invocation not present in cases
// exits 1 with no output. Needed for the changed-files branch-match
// fallback, which issues several different git commands in one Detect call
// (unlike the single-command stubBinary used elsewhere in this file).
func stubGitDispatch(t *testing.T, dir string, cases map[string]string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("args=\"$*\"\n")
	b.WriteString("case \"$args\" in\n")
	for pattern, stdout := range cases {
		escaped := strings.ReplaceAll(stdout, "'", `'\''`)
		fmt.Fprintf(&b, "  '%s') printf '%%s\\n' '%s'; exit 0 ;;\n", pattern, escaped)
	}
	b.WriteString("  *) exit 1 ;;\n")
	b.WriteString("esac\n")
	path := filepath.Join(dir, "git")
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		t.Fatalf("write git dispatch stub: %v", err)
	}
}

func initRoot(t *testing.T, withConfig bool) string {
	t.Helper()
	root := t.TempDir()
	if withConfig {
		if err := os.MkdirAll(filepath.Join(root, "openspec"), 0o755); err != nil {
			t.Fatalf("mkdir openspec: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, "openspec", "config.yaml"), []byte("version: 1\n"), 0o644); err != nil {
			t.Fatalf("write config.yaml: %v", err)
		}
	}
	return root
}

// ---------------------------------------------------------------------------
// AC1: absent `openspec` binary degrades to "not installed", no error escalation
// ---------------------------------------------------------------------------

func TestDetect_AbsentBinary_NotInitialized(t *testing.T) {
	withStubPath(t) // empty PATH: neither openspec nor git resolve
	root := initRoot(t, false)

	info, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if info.Installed {
		t.Fatalf("Detect: Installed = true, want false (binary absent)")
	}
	if info.Initialized {
		t.Fatalf("Detect: Initialized = true, want false (no config.yaml)")
	}
	if info.Changes != nil {
		t.Fatalf("Detect: Changes = %v, want nil", info.Changes)
	}
	if info.BranchMatch != nil {
		t.Fatalf("Detect: BranchMatch = %v, want nil", info.BranchMatch)
	}
}

func TestDetect_AbsentBinary_Initialized(t *testing.T) {
	withStubPath(t) // empty PATH
	root := initRoot(t, true)

	info, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if info.Installed {
		t.Fatalf("Detect: Installed = true, want false (binary absent)")
	}
	if !info.Initialized {
		t.Fatalf("Detect: Initialized = false, want true (config.yaml present)")
	}
	if info.Changes != nil {
		t.Fatalf("Detect: Changes = %v, want nil", info.Changes)
	}
}

// ---------------------------------------------------------------------------
// Installed but not initialized: binary present, no openspec/config.yaml
// ---------------------------------------------------------------------------

func TestDetect_InstalledNotInitialized(t *testing.T) {
	dir := withStubPath(t)
	stubBinary(t, dir, "openspec", `{"changes":[{"name":"some-change","completedTasks":0,"totalTasks":2,"status":"in-progress"}]}`, 0)
	root := initRoot(t, false)

	info, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if !info.Installed {
		t.Fatalf("Detect: Installed = false, want true (binary present)")
	}
	if info.Initialized {
		t.Fatalf("Detect: Initialized = true, want false (no config.yaml)")
	}
	if info.Changes != nil {
		t.Fatalf("Detect: Changes = %v, want nil when not initialized", info.Changes)
	}
}

// ---------------------------------------------------------------------------
// AC2: change list + branch-match parsing reproduces source output for
// recorded CLI fixtures.
// ---------------------------------------------------------------------------

// changeListFixture is a recorded `openspec list --json` fixture: a
// two-change payload including fields (lastModified, root) that Change does
// not model, verifying they are ignored rather than causing a parse error.
const changeListFixture = `{
  "changes": [
    {
      "name": "add-foo-bar",
      "completedTasks": 1,
      "totalTasks": 3,
      "lastModified": "2026-01-01T00:00:00Z",
      "status": "in-progress"
    },
    {
      "name": "cleanup-baz",
      "completedTasks": 2,
      "totalTasks": 2,
      "lastModified": "2026-01-02T00:00:00Z",
      "status": "complete"
    }
  ],
  "root": {"path": "/repo"}
}`

func TestDetect_ChangeList(t *testing.T) {
	dir := withStubPath(t)
	stubBinary(t, dir, "openspec", changeListFixture, 0)
	stubBinary(t, dir, "git", "main", 0)
	root := initRoot(t, true)

	info, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if !info.Installed || !info.Initialized {
		t.Fatalf("Detect: Installed=%v Initialized=%v, want both true", info.Installed, info.Initialized)
	}
	if len(info.Changes) != 2 {
		t.Fatalf("Detect: got %d changes, want 2", len(info.Changes))
	}

	want := []Change{
		{Name: "add-foo-bar", Status: "in-progress", CompletedTasks: 1, TotalTasks: 3},
		{Name: "cleanup-baz", Status: "complete", CompletedTasks: 2, TotalTasks: 2},
	}
	for i, w := range want {
		got := info.Changes[i]
		if got.Name != w.Name || got.Status != w.Status || got.CompletedTasks != w.CompletedTasks || got.TotalTasks != w.TotalTasks {
			t.Fatalf("Detect: Changes[%d] = %+v, want %+v", i, got, w)
		}
	}

	// branch "main" matches neither change.
	if info.BranchMatch != nil {
		t.Fatalf("Detect: BranchMatch = %+v, want nil for branch %q", info.BranchMatch, "main")
	}
}

func TestDetect_BranchMatch_ExactSlug(t *testing.T) {
	dir := withStubPath(t)
	stubBinary(t, dir, "openspec", changeListFixture, 0)
	stubBinary(t, dir, "git", "feat/add-foo-bar", 0)
	root := initRoot(t, true)

	info, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if info.BranchMatch == nil {
		t.Fatalf("Detect: BranchMatch = nil, want match for branch feat/add-foo-bar")
	}
	if info.BranchMatch.Name != "add-foo-bar" {
		t.Fatalf("Detect: BranchMatch.Name = %q, want %q", info.BranchMatch.Name, "add-foo-bar")
	}
}

func TestDetect_BranchMatch_BoundedSubstring(t *testing.T) {
	dir := withStubPath(t)
	stubBinary(t, dir, "openspec", changeListFixture, 0)
	// Branch is not an exact slug match but contains the change name as a
	// '-'-bounded substring, mirroring the source's slugRe fallback.
	stubBinary(t, dir, "git", "jane-cleanup-baz-wip", 0)
	root := initRoot(t, true)

	info, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if info.BranchMatch == nil {
		t.Fatalf("Detect: BranchMatch = nil, want bounded-substring match")
	}
	if info.BranchMatch.Name != "cleanup-baz" {
		t.Fatalf("Detect: BranchMatch.Name = %q, want %q", info.BranchMatch.Name, "cleanup-baz")
	}
}

func TestDetect_BranchMatch_NoMatch(t *testing.T) {
	dir := withStubPath(t)
	stubBinary(t, dir, "openspec", changeListFixture, 0)
	stubBinary(t, dir, "git", "unrelated-branch", 0)
	root := initRoot(t, true)

	info, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if info.BranchMatch != nil {
		t.Fatalf("Detect: BranchMatch = %+v, want nil", info.BranchMatch)
	}
}

// ---------------------------------------------------------------------------
// Changed-files fallback (openspec.js:206-223): when the branch-slug match
// finds nothing, fall back to diffing committed files against the base
// branch and matching on a uniquely-touched openspec/changes/<name>/ dir.
// ---------------------------------------------------------------------------

func TestDetect_BranchMatch_ChangedFilesFallback(t *testing.T) {
	dir := withStubPath(t)
	stubBinary(t, dir, "openspec", changeListFixture, 0)
	stubGitDispatch(t, dir, map[string]string{
		"branch --show-current":                 "unrelated-branch",
		"symbolic-ref refs/remotes/origin/HEAD": "",
		"rev-parse --verify main":               "abc123",
		"diff --name-only main...HEAD":          "openspec/changes/add-foo-bar/tasks.md\nREADME.md",
	})
	root := initRoot(t, true)

	info, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if info.BranchMatch == nil {
		t.Fatalf("Detect: BranchMatch = nil, want changed-files fallback match")
	}
	if info.BranchMatch.Name != "add-foo-bar" {
		t.Fatalf("Detect: BranchMatch.Name = %q, want %q", info.BranchMatch.Name, "add-foo-bar")
	}
}

func TestDetect_BranchMatch_ChangedFilesFallback_Ambiguous(t *testing.T) {
	dir := withStubPath(t)
	stubBinary(t, dir, "openspec", changeListFixture, 0)
	stubGitDispatch(t, dir, map[string]string{
		"branch --show-current":                 "unrelated-branch",
		"symbolic-ref refs/remotes/origin/HEAD": "",
		"rev-parse --verify main":               "abc123",
		// Touches both changes' directories: ambiguous, must not guess.
		"diff --name-only main...HEAD": "openspec/changes/add-foo-bar/tasks.md\nopenspec/changes/cleanup-baz/tasks.md",
	})
	root := initRoot(t, true)

	info, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if info.BranchMatch != nil {
		t.Fatalf("Detect: BranchMatch = %+v, want nil for ambiguous changed-files hit", info.BranchMatch)
	}
}

func TestDetect_BranchMatch_ChangedFilesFallback_SkippedOnBaseBranch(t *testing.T) {
	dir := withStubPath(t)
	stubBinary(t, dir, "openspec", changeListFixture, 0)
	stubGitDispatch(t, dir, map[string]string{
		"branch --show-current":                 "main",
		"symbolic-ref refs/remotes/origin/HEAD": "",
		"rev-parse --verify main":               "abc123",
		// No "diff --name-only ..." case registered: if Detect called it
		// anyway, the stub's default (*) branch exits 1 and matchChangedFiles
		// degrades to nil either way, so this also guards against a
		// regression that stops honouring the branch==base skip.
	})
	root := initRoot(t, true)

	info, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if info.BranchMatch != nil {
		t.Fatalf("Detect: BranchMatch = %+v, want nil when branch is the base branch", info.BranchMatch)
	}
}

func TestDetect_BranchMatch_ChangedFilesFallback_BaseDetectionFails(t *testing.T) {
	dir := withStubPath(t)
	stubBinary(t, dir, "openspec", changeListFixture, 0)
	// symbolic-ref and both rev-parse candidates fail (unregistered ->
	// default exit 1): detectBaseBranchSafe must degrade to "main" rather
	// than propagating an error, matching the source's try/catch-to-'main'.
	stubGitDispatch(t, dir, map[string]string{
		"branch --show-current":        "unrelated-branch",
		"diff --name-only main...HEAD": "openspec/changes/cleanup-baz/tasks.md",
	})
	root := initRoot(t, true)

	info, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if info.BranchMatch == nil {
		t.Fatalf("Detect: BranchMatch = nil, want fallback match using defaulted base branch %q", "main")
	}
	if info.BranchMatch.Name != "cleanup-baz" {
		t.Fatalf("Detect: BranchMatch.Name = %q, want %q", info.BranchMatch.Name, "cleanup-baz")
	}
}

// ---------------------------------------------------------------------------
// Malformed CLI output degrades gracefully rather than erroring.
// ---------------------------------------------------------------------------

func TestDetect_MalformedJSON(t *testing.T) {
	dir := withStubPath(t)
	stubBinary(t, dir, "openspec", "not json", 0)
	root := initRoot(t, true)

	info, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: unexpected error: %v", err)
	}
	if !info.Installed {
		t.Fatalf("Detect: Installed = false, want true (binary ran successfully)")
	}
	if info.Changes != nil {
		t.Fatalf("Detect: Changes = %v, want nil on malformed output", info.Changes)
	}
}

// ---------------------------------------------------------------------------
// Detect itself errors only for an inaccessible root.
// ---------------------------------------------------------------------------

func TestDetect_InaccessibleRoot(t *testing.T) {
	withStubPath(t)
	_, err := Detect(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatalf("Detect: expected error for inaccessible root, got nil")
	}
}
