package worktree

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenImports enforces the layering rule documented on the package: this
// package must never depend on internal/config or internal/state, since both
// of those packages anchor themselves using worktree.MainRoot/ActiveRoot and
// an import in the other direction would create a cycle.
var forbiddenImports = []string{"internal/config", "internal/state"}

// TestNoForbiddenImports is a lightweight depguard: it parses every non-test
// .go file in this package and fails if any import path contains one of the
// forbidden internal packages.
func TestNoForbiddenImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("could not read package directory: %v", err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("could not parse %s: %v", name, err)
		}

		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			for _, forbidden := range forbiddenImports {
				if strings.Contains(importPath, forbidden) {
					t.Errorf("%s imports %q, which violates the layering rule (must not depend on %s)", name, importPath, forbidden)
				}
			}
		}
	}
}

// runGit runs a git command for fixture setup only. This is test code, not
// package code, so it is exempt from the "shell out via internal/execx"
// contract that applies to worktree.go.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// realPath resolves symlinks so comparisons are stable on systems (e.g.
// macOS) where t.TempDir() lives under a symlinked path but git reports the
// physical path.
func realPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("could not resolve symlinks for %s: %v", path, err)
	}
	return resolved
}

// setupMainRepo creates a fresh git repo with one commit in dir, so git
// worktree/rev-parse commands have something to operate on.
func setupMainRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "-c", "user.email=worktree-test@example.com", "-c", "user.name=worktree-test", "commit", "--allow-empty", "-q", "-m", "init")
}

func TestMainRootIn_LinkedWorktree(t *testing.T) {
	mainDir := t.TempDir()
	setupMainRepo(t, mainDir)

	linkedDir := filepath.Join(t.TempDir(), "linked")
	runGit(t, mainDir, "worktree", "add", "-q", linkedDir, "-b", "feature-branch")

	got, err := mainRootIn(linkedDir)
	if err != nil {
		t.Fatalf("mainRootIn(%s) returned error: %v", linkedDir, err)
	}

	if realPath(t, got) != realPath(t, mainDir) {
		t.Errorf("mainRootIn(%s) = %q, want %q", linkedDir, got, mainDir)
	}
}

func TestMainRootIn_SingleWorktree(t *testing.T) {
	dir := t.TempDir()
	setupMainRepo(t, dir)

	got, err := mainRootIn(dir)
	if err != nil {
		t.Fatalf("mainRootIn(%s) returned error: %v", dir, err)
	}

	if realPath(t, got) != realPath(t, dir) {
		t.Errorf("mainRootIn(%s) = %q, want %q", dir, got, dir)
	}
}

// TestMainRootIn_BareWithLinkedWorktree reproduces the porcelain shape from
// a bare-clone-anchored worktree setup (e.g. CI checkouts that clone
// `--bare` and add worktrees from it): `git worktree list --porcelain`
// lists the bare repo first, followed by "bare" instead of HEAD/branch
// lines, then the linked worktree. mainRootIn must skip the bare entry and
// return the linked worktree's path, not the bare root.
func TestMainRootIn_BareWithLinkedWorktree(t *testing.T) {
	mainDir := t.TempDir()
	setupMainRepo(t, mainDir)
	branch := runGit(t, mainDir, "symbolic-ref", "--short", "HEAD")

	bareDir := filepath.Join(t.TempDir(), "repo.git")
	runGit(t, mainDir, "clone", "--bare", "-q", mainDir, bareDir)

	linkedDir := filepath.Join(t.TempDir(), "linked")
	runGit(t, bareDir, "worktree", "add", "-q", linkedDir, branch)

	got, err := mainRootIn(linkedDir)
	if err != nil {
		t.Fatalf("mainRootIn(%s) returned error: %v", linkedDir, err)
	}

	if realPath(t, got) != realPath(t, linkedDir) {
		t.Errorf("mainRootIn(%s) = %q, want %q (linked worktree, not bare root %q)", linkedDir, got, linkedDir, bareDir)
	}
}

// TestMainRootIn_BareOnly_NoLinkedWorktrees confirms that a bare-only
// registration (no linked worktrees exist yet) errors out rather than
// silently falling back to the bare path once it is skipped.
func TestMainRootIn_BareOnly_NoLinkedWorktrees(t *testing.T) {
	mainDir := t.TempDir()
	setupMainRepo(t, mainDir)

	bareDir := filepath.Join(t.TempDir(), "repo.git")
	runGit(t, mainDir, "clone", "--bare", "-q", mainDir, bareDir)

	got, err := mainRootIn(bareDir)
	if err == nil {
		t.Fatalf("mainRootIn(%s) = %q, nil; want an error (only a bare entry exists)", bareDir, got)
	}
	if got != "" {
		t.Errorf("mainRootIn(%s) = %q on error; want empty string", bareDir, got)
	}
}

func TestIsBare(t *testing.T) {
	mainDir := t.TempDir()
	setupMainRepo(t, mainDir)

	if bare, err := IsBare(mainDir); err != nil {
		t.Fatalf("IsBare(%s) returned error: %v", mainDir, err)
	} else if bare {
		t.Errorf("IsBare(%s) = true, want false for a normal worktree", mainDir)
	}

	bareDir := filepath.Join(t.TempDir(), "repo.git")
	runGit(t, mainDir, "clone", "--bare", "-q", mainDir, bareDir)

	if bare, err := IsBare(bareDir); err != nil {
		t.Fatalf("IsBare(%s) returned error: %v", bareDir, err)
	} else if !bare {
		t.Errorf("IsBare(%s) = false, want true for a bare repository", bareDir)
	}
}

func TestMainRootIn_NonRepo(t *testing.T) {
	dir := t.TempDir()

	got, err := mainRootIn(dir)
	if err == nil {
		t.Fatalf("mainRootIn(%s) = %q, nil; want an error", dir, got)
	}
	if got != "" {
		t.Errorf("mainRootIn(%s) = %q on error; want empty string", dir, got)
	}
}

func TestActiveRootIn_LinkedWorktree(t *testing.T) {
	mainDir := t.TempDir()
	setupMainRepo(t, mainDir)

	linkedDir := filepath.Join(t.TempDir(), "linked")
	runGit(t, mainDir, "worktree", "add", "-q", linkedDir, "-b", "active-feature-branch")

	got, err := activeRootIn(linkedDir)
	if err != nil {
		t.Fatalf("activeRootIn(%s) returned error: %v", linkedDir, err)
	}

	if realPath(t, got) != realPath(t, linkedDir) {
		t.Errorf("activeRootIn(%s) = %q, want %q", linkedDir, got, linkedDir)
	}
}

func TestActiveRootIn_NonRepo(t *testing.T) {
	dir := t.TempDir()

	got, err := activeRootIn(dir)
	if err == nil {
		t.Fatalf("activeRootIn(%s) = %q, nil; want an error", dir, got)
	}
	if got != "" {
		t.Errorf("activeRootIn(%s) = %q on error; want empty string", dir, got)
	}
}
