package gitx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// gitRun executes a git command in dir, failing the test on error.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// initGitRepo creates a minimal git repo in dir with one commit on "main".
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "-c", "init.defaultBranch=main", "init")
	// Persist a repo-local identity: gitRun's GIT_AUTHOR_*/GIT_COMMITTER_*
	// env vars only cover the one process they're passed to (the "commit"
	// call below), not later calls made by production code under test (e.g.
	// CreateTag's plain execx.Run, which sets no such env vars and depends
	// on ambient git config). Without this, CreateTag's "git tag -a" fails
	// with exit 128 on any machine/CI runner where git can't auto-detect a
	// usable identity from the OS user (e.g. a blank GECOS field on a CI
	// runner account) — this was observed failing in CI while passing
	// locally on machines with a real full-name OS account.
	gitRun(t, dir, "config", "user.email", "test@test.com")
	gitRun(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "init.txt"), []byte("init\n"), 0644); err != nil {
		t.Fatalf("write init.txt: %v", err)
	}
	gitRun(t, dir, "add", "init.txt")
	gitRun(t, dir, "commit", "-m", "initial commit")
}

// newRepoWithBareOrigin creates a seed repo, clones it into a bare "origin",
// then clones that bare repo into a working directory with a usable
// identity configured. It centralizes the origin-bare-clone three-step
// scaffold (previously inlined per-test, e.g. TestHasUpstream_True) that
// several tests need to exercise behavior against a real remote. Returns the
// working clone's directory and the bare origin's directory.
func newRepoWithBareOrigin(t *testing.T) (clone, bareOrigin string) {
	t.Helper()
	seed := t.TempDir()
	initGitRepo(t, seed)

	bareOrigin = t.TempDir()
	gitRun(t, bareOrigin, "clone", "--bare", seed, ".")

	clone = t.TempDir()
	gitRun(t, clone, "clone", bareOrigin, ".")
	gitRun(t, clone, "config", "user.email", "test@test.com")
	gitRun(t, clone, "config", "user.name", "Test")
	return clone, bareOrigin
}

// newConflictingMerge creates a repo with two branches ("main" and
// otherBranch) that each modify the same line of the same file differently.
// It leaves "main" checked out; merging otherBranch into it is guaranteed to
// conflict.
func newConflictingMerge(t *testing.T) (dir, otherBranch string) {
	t.Helper()
	dir = t.TempDir()
	initGitRepo(t, dir)

	otherBranch = "feature"
	gitRun(t, dir, "checkout", "-b", otherBranch)
	if err := os.WriteFile(filepath.Join(dir, "init.txt"), []byte("feature change\n"), 0644); err != nil {
		t.Fatalf("write init.txt: %v", err)
	}
	gitRun(t, dir, "add", "init.txt")
	gitRun(t, dir, "commit", "-m", "feature change")

	gitRun(t, dir, "checkout", "main")
	if err := os.WriteFile(filepath.Join(dir, "init.txt"), []byte("main change\n"), 0644); err != nil {
		t.Fatalf("write init.txt: %v", err)
	}
	gitRun(t, dir, "add", "init.txt")
	gitRun(t, dir, "commit", "-m", "main change")

	return dir, otherBranch
}

// ---------------------------------------------------------------------------
// BaseBranch
// ---------------------------------------------------------------------------

func TestBaseBranch_Configured(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	got, err := BaseBranch(dir, "develop")
	if err != nil {
		t.Fatalf("BaseBranch: unexpected error: %v", err)
	}
	if got != "develop" {
		t.Fatalf("BaseBranch: got %q, want %q", got, "develop")
	}
}

func TestBaseBranch_ConfiguredIsTrimmed(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	got, err := BaseBranch(dir, "  develop  ")
	if err != nil {
		t.Fatalf("BaseBranch: unexpected error: %v", err)
	}
	if got != "develop" {
		t.Fatalf("BaseBranch: got %q, want %q", got, "develop")
	}
}

func TestBaseBranch_ConfiguredFlagShapeRejected(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	got, err := BaseBranch(dir, "  --upload-pack=evil  ")
	if err == nil {
		t.Fatalf("BaseBranch: got %q, nil error; want a flag-shape error", got)
	}
	if got != "" {
		t.Fatalf("BaseBranch: got %q on error, want empty string", got)
	}
	if !strings.Contains(err.Error(), "looks like a flag") {
		t.Fatalf("BaseBranch: error %q does not mention the flag shape", err.Error())
	}
}

func TestBaseBranch_EmptyFallsBackToDefaultBranch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	got, err := BaseBranch(dir, "")
	if err != nil {
		t.Fatalf("BaseBranch: unexpected error: %v", err)
	}
	want, err := DefaultBranch(dir)
	if err != nil {
		t.Fatalf("DefaultBranch: unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("BaseBranch: got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// FetchBranch
// ---------------------------------------------------------------------------

func TestFetchBranch_Success(t *testing.T) {
	clone, bareOrigin := newRepoWithBareOrigin(t)

	// Push a branch to the bare origin from a throwaway worktree clone, so
	// "clone" (which only fetched at clone time) doesn't have it yet.
	tmp := t.TempDir()
	gitRun(t, tmp, "clone", bareOrigin, ".")
	gitRun(t, tmp, "config", "user.email", "test@test.com")
	gitRun(t, tmp, "config", "user.name", "Test")
	gitRun(t, tmp, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(tmp, "feature.txt"), []byte("feature\n"), 0644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	gitRun(t, tmp, "add", "feature.txt")
	gitRun(t, tmp, "commit", "-m", "feature commit")
	gitRun(t, tmp, "push", "origin", "feature")

	if err := FetchBranch(clone, "origin", "feature"); err != nil {
		t.Fatalf("FetchBranch: unexpected error: %v", err)
	}

	// The clone's configured refspec updates the remote-tracking ref on a
	// targeted fetch; a failed/missing fetch would leave this unresolvable.
	gitRun(t, clone, "rev-parse", "--verify", "refs/remotes/origin/feature")
}

func TestFetchBranch_MissingBranch(t *testing.T) {
	clone, _ := newRepoWithBareOrigin(t)

	err := FetchBranch(clone, "origin", "does-not-exist")
	if err == nil {
		t.Fatal("FetchBranch(missing branch): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("FetchBranch(missing branch): error = %q, want mention of %q", err.Error(), "does-not-exist")
	}
}

func TestFetchBranch_InvalidRemote(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	err := FetchBranch(dir, "--upload-pack=x", "main")
	if err == nil {
		t.Fatal("FetchBranch(invalid remote): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "looks like a flag") {
		t.Fatalf("FetchBranch(invalid remote): error = %q, want mention of %q", err.Error(), "looks like a flag")
	}
}

func TestFetchBranch_InvalidBranch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	err := FetchBranch(dir, "origin", "--upload-pack=x")
	if err == nil {
		t.Fatal("FetchBranch(invalid branch): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "looks like a flag") {
		t.Fatalf("FetchBranch(invalid branch): error = %q, want mention of %q", err.Error(), "looks like a flag")
	}
}

// ---------------------------------------------------------------------------
// BehindCount
// ---------------------------------------------------------------------------

func TestBehindCount_CountsCommitsOnRef(t *testing.T) {
	clone, bareOrigin := newRepoWithBareOrigin(t)

	// Advance the bare origin's main branch by 2 commits via a throwaway
	// worktree clone, so "clone" (still at the seed commit) is behind it.
	tmp := t.TempDir()
	gitRun(t, tmp, "clone", bareOrigin, ".")
	gitRun(t, tmp, "config", "user.email", "test@test.com")
	gitRun(t, tmp, "config", "user.name", "Test")
	for i := 0; i < 2; i++ {
		name := filepath.Join(tmp, "behind"+strings.Repeat("x", i)+".txt")
		if err := os.WriteFile(name, []byte("x\n"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		gitRun(t, tmp, "add", ".")
		gitRun(t, tmp, "commit", "-m", "advance commit")
	}
	gitRun(t, tmp, "push", "origin", "main")

	if err := FetchBranch(clone, "origin", "main"); err != nil {
		t.Fatalf("FetchBranch: unexpected error: %v", err)
	}

	got, err := BehindCount(clone, "FETCH_HEAD")
	if err != nil {
		t.Fatalf("BehindCount: unexpected error: %v", err)
	}
	if got != 2 {
		t.Fatalf("BehindCount: got %d, want 2", got)
	}
}

func TestBehindCount_Zero(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	got, err := BehindCount(dir, "HEAD")
	if err != nil {
		t.Fatalf("BehindCount: unexpected error: %v", err)
	}
	if got != 0 {
		t.Fatalf("BehindCount: got %d, want 0", got)
	}
}

func TestBehindCount_InvalidRef(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	_, err := BehindCount(dir, "--upload-pack=x")
	if err == nil {
		t.Fatal("BehindCount(invalid ref): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "looks like a flag") {
		t.Fatalf("BehindCount(invalid ref): error = %q, want mention of %q", err.Error(), "looks like a flag")
	}
}

// ---------------------------------------------------------------------------
// Merge / MergeAbort / MergeInProgress / UnmergedFiles
// ---------------------------------------------------------------------------

func TestMerge_Conflict(t *testing.T) {
	dir, other := newConflictingMerge(t)

	conflict, err := Merge(dir, other)
	if err != nil {
		t.Fatalf("Merge: unexpected error: %v", err)
	}
	if !conflict {
		t.Fatal("Merge: got conflict=false, want true")
	}

	inProgress, err := MergeInProgress(dir)
	if err != nil {
		t.Fatalf("MergeInProgress: unexpected error: %v", err)
	}
	if !inProgress {
		t.Fatal("Merge: expected the conflicting merge to be left in progress")
	}
}

func TestMerge_CleanMerge(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	gitRun(t, dir, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feature\n"), 0644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	gitRun(t, dir, "add", "feature.txt")
	gitRun(t, dir, "commit", "-m", "feature commit")
	gitRun(t, dir, "checkout", "main")

	conflict, err := Merge(dir, "feature")
	if err != nil {
		t.Fatalf("Merge: unexpected error: %v", err)
	}
	if conflict {
		t.Fatal("Merge: got conflict=true, want false for a clean merge")
	}
}

func TestMerge_InvalidRef(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	_, err := Merge(dir, "--upload-pack=x")
	if err == nil {
		t.Fatal("Merge(invalid ref): expected error, got nil")
	}
	if !strings.Contains(err.Error(), "looks like a flag") {
		t.Fatalf("Merge(invalid ref): error = %q, want mention of %q", err.Error(), "looks like a flag")
	}
}

func TestMergeAbort_RestoresPreviousHEAD(t *testing.T) {
	dir, other := newConflictingMerge(t)

	beforeHead := gitRun(t, dir, "rev-parse", "HEAD")

	conflict, err := Merge(dir, other)
	if err != nil {
		t.Fatalf("Merge: unexpected error: %v", err)
	}
	if !conflict {
		t.Fatal("Merge: expected a conflict to set up this test")
	}

	if err := MergeAbort(dir); err != nil {
		t.Fatalf("MergeAbort: unexpected error: %v", err)
	}

	afterHead := gitRun(t, dir, "rev-parse", "HEAD")
	if afterHead != beforeHead {
		t.Fatalf("MergeAbort: HEAD = %q, want unchanged %q", afterHead, beforeHead)
	}

	inProgress, err := MergeInProgress(dir)
	if err != nil {
		t.Fatalf("MergeInProgress: unexpected error: %v", err)
	}
	if inProgress {
		t.Fatal("MergeAbort: merge still in progress after abort")
	}
}

func TestMergeInProgress_FalseWithoutMerge(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	got, err := MergeInProgress(dir)
	if err != nil {
		t.Fatalf("MergeInProgress: unexpected error: %v", err)
	}
	if got {
		t.Fatal("MergeInProgress: got true, want false with no merge in progress")
	}
}

func TestMergeInProgress_NotARepo(t *testing.T) {
	dir := t.TempDir()

	_, err := MergeInProgress(dir)
	if err == nil {
		t.Fatal("MergeInProgress outside repo: expected error, got nil")
	}
}

func TestUnmergedFiles_ListsConflictingPaths(t *testing.T) {
	dir, other := newConflictingMerge(t)

	conflict, err := Merge(dir, other)
	if err != nil {
		t.Fatalf("Merge: unexpected error: %v", err)
	}
	if !conflict {
		t.Fatal("Merge: expected a conflict to set up this test")
	}

	got, err := UnmergedFiles(dir)
	if err != nil {
		t.Fatalf("UnmergedFiles: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "init.txt" {
		t.Fatalf("UnmergedFiles: got %v, want [init.txt]", got)
	}
}

func TestUnmergedFiles_EmptyWithoutConflict(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	got, err := UnmergedFiles(dir)
	if err != nil {
		t.Fatalf("UnmergedFiles: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("UnmergedFiles: got %v, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// SplitDiffByFile — golden test (AC: identical file-slice boundaries)
// ---------------------------------------------------------------------------

const goldenDiff = `diff --git a/README.md b/README.md
index 1234567..abcdef0 100644
--- a/README.md
+++ b/README.md
@@ -1,3 +1,4 @@
 # My Project

 Some content.
+Added line.
diff --git a/internal/foo.go b/internal/foo.go
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/internal/foo.go
@@ -0,0 +1,5 @@
+package internal
+
+func Foo() string {
+	return "foo"
+}
diff --git a/old.txt b/new.txt
similarity index 90%
rename from old.txt
rename to new.txt
index 1234567..abcdef0 100644
--- a/old.txt
+++ b/new.txt
@@ -1,2 +1,2 @@
-old content
+new content
 shared line
`

func TestSplitDiffByFile_Golden(t *testing.T) {
	files := SplitDiffByFile(goldenDiff)

	if len(files) != 3 {
		t.Fatalf("SplitDiffByFile: got %d chunks, want 3", len(files))
	}

	// Verify file paths in order.
	wantPaths := []string{"README.md", "internal/foo.go", "new.txt"}
	for i, want := range wantPaths {
		if files[i].Path != want {
			t.Fatalf("SplitDiffByFile: chunk[%d].Path = %q, want %q", i, files[i].Path, want)
		}
	}

	// Verify concatenation reproduces the original diff byte-for-byte.
	var rebuilt strings.Builder
	for _, f := range files {
		rebuilt.WriteString(f.Content)
	}
	if rebuilt.String() != goldenDiff {
		t.Fatalf("SplitDiffByFile: concatenated chunks do not reproduce original diff\ngot:\n%s\nwant:\n%s", rebuilt.String(), goldenDiff)
	}
}

func TestSplitDiffByFile_EmptyInput(t *testing.T) {
	files := SplitDiffByFile("")
	if files != nil {
		t.Fatalf("SplitDiffByFile(\"\"): got %v, want nil", files)
	}
}

func TestSplitDiffByFile_NoDiffHeaders(t *testing.T) {
	files := SplitDiffByFile("some random text\nno diff headers here\n")
	if files != nil {
		t.Fatalf("SplitDiffByFile(no headers): got %v, want nil", files)
	}
}

func TestSplitDiffByFile_PreambleSkipped(t *testing.T) {
	input := "Preamble text\nSome metadata\n" + goldenDiff
	files := SplitDiffByFile(input)
	if len(files) != 3 {
		t.Fatalf("SplitDiffByFile with preamble: got %d chunks, want 3", len(files))
	}
	// Preamble is not included in any chunk.
	if strings.Contains(files[0].Content, "Preamble") {
		t.Fatal("SplitDiffByFile: first chunk should not contain preamble text")
	}
}

func TestSplitDiffByFile_SingleFile(t *testing.T) {
	single := "diff --git a/only.go b/only.go\nindex 0000..1111 100644\n--- a/only.go\n+++ b/only.go\n@@ -1 +1 @@\n-old\n+new\n"
	files := SplitDiffByFile(single)
	if len(files) != 1 {
		t.Fatalf("SplitDiffByFile(single): got %d chunks, want 1", len(files))
	}
	if files[0].Path != "only.go" {
		t.Fatalf("SplitDiffByFile(single): Path = %q, want %q", files[0].Path, "only.go")
	}
	if files[0].Content != single {
		t.Fatalf("SplitDiffByFile(single): Content mismatch")
	}
}

// ---------------------------------------------------------------------------
// DeriveWorkspace — pure function tests
// ---------------------------------------------------------------------------

func TestDeriveWorkspace_LinkedWorktree(t *testing.T) {
	got := DeriveWorkspace(true, "feature", "main")
	if got != "continue" {
		t.Fatalf("DeriveWorkspace(linked=true): got %q, want %q", got, "continue")
	}
}

func TestDeriveWorkspace_LinkedWorktreeOnDefaultBranch(t *testing.T) {
	// Even when the branch matches default, linked worktree returns "continue".
	got := DeriveWorkspace(true, "main", "main")
	if got != "continue" {
		t.Fatalf("DeriveWorkspace(linked=true, main==main): got %q, want %q", got, "continue")
	}
}

func TestDeriveWorkspace_MainWorktreeOnDefaultBranch(t *testing.T) {
	got := DeriveWorkspace(false, "main", "main")
	if got != "branch" {
		t.Fatalf("DeriveWorkspace(main on main): got %q, want %q", got, "branch")
	}
}

func TestDeriveWorkspace_MainWorktreeOnFeatureBranch(t *testing.T) {
	got := DeriveWorkspace(false, "feature", "main")
	if got != "continue" {
		t.Fatalf("DeriveWorkspace(feature on main): got %q, want %q", got, "continue")
	}
}

// ---------------------------------------------------------------------------
// Non-repo error tests (AC: all exported funcs degrade to error, not panic)
// ---------------------------------------------------------------------------

func TestCurrentBranch_NotARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := CurrentBranch(dir)
	if err == nil {
		t.Fatal("CurrentBranch outside repo: expected error, got nil")
	}
}

func TestDefaultBranch_NotARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := DefaultBranch(dir)
	if err == nil {
		t.Fatal("DefaultBranch outside repo: expected error, got nil")
	}
}

func TestStatus_NotARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := Status(dir)
	if err == nil {
		t.Fatal("Status outside repo: expected error, got nil")
	}
}

func TestDiff_NotARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := Diff(dir, DiffOpts{Base: "main"})
	if err == nil {
		t.Fatal("Diff outside repo: expected error, got nil")
	}
}

func TestCommitLog_NotARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := CommitLog(dir, "main")
	if err == nil {
		t.Fatal("CommitLog outside repo: expected error, got nil")
	}
}

func TestCommitCount_NotARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := CommitCount(dir, "main")
	if err == nil {
		t.Fatal("CommitCount outside repo: expected error, got nil")
	}
}

func TestTagList_NotARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := TagList(dir)
	if err == nil {
		t.Fatal("TagList outside repo: expected error, got nil")
	}
}

func TestTagsAtHead_NotARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := TagsAtHead(dir)
	if err == nil {
		t.Fatal("TagsAtHead outside repo: expected error, got nil")
	}
}

func TestAllSemverTags_NotARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := AllSemverTags(dir)
	if err == nil {
		t.Fatal("AllSemverTags outside repo: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// Fixture-repo tests
// ---------------------------------------------------------------------------

func TestCurrentBranch_InRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	got, err := CurrentBranch(dir)
	if err != nil {
		t.Fatalf("CurrentBranch: unexpected error: %v", err)
	}
	if got != "main" {
		t.Fatalf("CurrentBranch: got %q, want %q", got, "main")
	}
}

func TestDefaultBranch_FallbackToMain(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	got, err := DefaultBranch(dir)
	if err != nil {
		t.Fatalf("DefaultBranch: unexpected error: %v", err)
	}
	if got != "main" {
		t.Fatalf("DefaultBranch: got %q, want %q", got, "main")
	}
}

func TestDefaultBranch_OriginHEAD(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Set up a fake origin/HEAD symbolic ref.
	gitRun(t, dir, "remote", "add", "origin", "https://example.com/test/repo.git")
	gitRun(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	got, err := DefaultBranch(dir)
	if err != nil {
		t.Fatalf("DefaultBranch: unexpected error: %v", err)
	}
	if got != "main" {
		t.Fatalf("DefaultBranch: got %q, want %q", got, "main")
	}
}

func TestDefaultBranch_Master(t *testing.T) {
	dir := t.TempDir()
	// Init with master as default branch.
	gitRun(t, dir, "-c", "init.defaultBranch=master", "init")
	if err := os.WriteFile(filepath.Join(dir, "init.txt"), []byte("init\n"), 0644); err != nil {
		t.Fatalf("write init.txt: %v", err)
	}
	gitRun(t, dir, "add", "init.txt")
	gitRun(t, dir, "commit", "-m", "initial commit")

	got, err := DefaultBranch(dir)
	if err != nil {
		t.Fatalf("DefaultBranch: unexpected error: %v", err)
	}
	if got != "master" {
		t.Fatalf("DefaultBranch: got %q, want %q", got, "master")
	}
}

func TestStatus_CleanTree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	got, err := Status(dir)
	if err != nil {
		t.Fatalf("Status: unexpected error: %v", err)
	}
	if got != "" {
		t.Fatalf("Status: got %q, want empty for clean tree", got)
	}
}

func TestStatus_DirtyTree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("dirty\n"), 0644); err != nil {
		t.Fatalf("write dirty.txt: %v", err)
	}

	got, err := Status(dir)
	if err != nil {
		t.Fatalf("Status: unexpected error: %v", err)
	}
	if got == "" {
		t.Fatal("Status: expected non-empty output for dirty tree")
	}
	if !strings.Contains(got, "dirty.txt") {
		t.Fatalf("Status: output %q does not mention dirty.txt", got)
	}
}

// TestStatus_KeepsLeadingSpaceOfFirstEntry pins that the first porcelain
// entry keeps its leading status column. For a modified, unstaged file the
// line is " M <path>"; trimming it shifts the path by one character for
// every caller that reads the path from column 3.
func TestStatus_KeepsLeadingSpaceOfFirstEntry(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "init.txt"), []byte("changed\n"), 0644); err != nil {
		t.Fatalf("write init.txt: %v", err)
	}

	got, err := Status(dir)
	if err != nil {
		t.Fatalf("Status: unexpected error: %v", err)
	}
	if want := " M init.txt"; got != want {
		t.Fatalf("Status: got %q, want %q", got, want)
	}
}

func TestDiff_BranchContribution(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Create a feature branch with a change.
	gitRun(t, dir, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feature\n"), 0644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	gitRun(t, dir, "add", "feature.txt")
	gitRun(t, dir, "commit", "-m", "add feature")

	got, err := Diff(dir, DiffOpts{Base: "main"})
	if err != nil {
		t.Fatalf("Diff: unexpected error: %v", err)
	}
	if !strings.Contains(got, "feature.txt") {
		t.Fatalf("Diff: output does not mention feature.txt:\n%s", got)
	}
}

func TestDiff_NameOnly(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	gitRun(t, dir, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	gitRun(t, dir, "add", "a.txt", "b.txt")
	gitRun(t, dir, "commit", "-m", "add files")

	got, err := Diff(dir, DiffOpts{Base: "main", NameOnly: true})
	if err != nil {
		t.Fatalf("Diff: unexpected error: %v", err)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("Diff --name-only: got %d lines, want 2:\n%s", len(lines), got)
	}
}

// TestDiff_DefaultIsIndexVsWorktree pins that Diff with neither Base nor
// Cached compares the index to the working tree (plain `git diff`), so a
// staged-only change is not reported. Comparing against HEAD would also list
// staged changes as unstaged.
func TestDiff_DefaultIsIndexVsWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// staged.txt: new file, staged only.
	if err := os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("staged\n"), 0644); err != nil {
		t.Fatalf("write staged.txt: %v", err)
	}
	gitRun(t, dir, "add", "staged.txt")
	// init.txt: tracked file with an unstaged edit.
	if err := os.WriteFile(filepath.Join(dir, "init.txt"), []byte("edited\n"), 0644); err != nil {
		t.Fatalf("write init.txt: %v", err)
	}

	got, err := Diff(dir, DiffOpts{NameOnly: true})
	if err != nil {
		t.Fatalf("Diff: unexpected error: %v", err)
	}
	if got != "init.txt" {
		t.Fatalf("Diff --name-only: got %q, want %q", got, "init.txt")
	}
}

func TestCommitLog_InRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	gitRun(t, dir, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "log.txt"), []byte("log\n"), 0644); err != nil {
		t.Fatalf("write log.txt: %v", err)
	}
	gitRun(t, dir, "add", "log.txt")
	gitRun(t, dir, "commit", "-m", "add log file")

	got, err := CommitLog(dir, "main")
	if err != nil {
		t.Fatalf("CommitLog: unexpected error: %v", err)
	}
	if !strings.Contains(got, "add log file") {
		t.Fatalf("CommitLog: output %q does not contain commit message", got)
	}
}

func TestCommitCount_InRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	gitRun(t, dir, "checkout", "-b", "feature")
	for i := 0; i < 3; i++ {
		name := filepath.Join(dir, "count"+strings.Repeat("x", i)+".txt")
		if err := os.WriteFile(name, []byte("x\n"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		gitRun(t, dir, "add", ".")
		gitRun(t, dir, "commit", "-m", "commit")
	}

	got, err := CommitCount(dir, "main")
	if err != nil {
		t.Fatalf("CommitCount: unexpected error: %v", err)
	}
	if got != 3 {
		t.Fatalf("CommitCount: got %d, want 3", got)
	}
}

func TestTagList_InRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	gitRun(t, dir, "tag", "v1.0.0")
	// Make another commit and tag it.
	if err := os.WriteFile(filepath.Join(dir, "v2.txt"), []byte("v2\n"), 0644); err != nil {
		t.Fatalf("write v2.txt: %v", err)
	}
	gitRun(t, dir, "add", "v2.txt")
	gitRun(t, dir, "commit", "-m", "v2")
	gitRun(t, dir, "tag", "v2.0.0")
	// Add a non-semver tag that should be excluded.
	gitRun(t, dir, "tag", "release-candidate")
	// Add a pre-release tag that strict filter excludes.
	gitRun(t, dir, "tag", "v3.0.0-rc.1")

	tags, err := TagList(dir)
	if err != nil {
		t.Fatalf("TagList: unexpected error: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("TagList: got %v, want [v2.0.0 v1.0.0]", tags)
	}
	if tags[0] != "v2.0.0" || tags[1] != "v1.0.0" {
		t.Fatalf("TagList: got %v, want [v2.0.0 v1.0.0]", tags)
	}
}

func TestTagsAtHead_InRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	gitRun(t, dir, "tag", "v1.0.0")

	tags, err := TagsAtHead(dir)
	if err != nil {
		t.Fatalf("TagsAtHead: unexpected error: %v", err)
	}
	if len(tags) != 1 || tags[0] != "v1.0.0" {
		t.Fatalf("TagsAtHead: got %v, want [v1.0.0]", tags)
	}
}

func TestAllSemverTags_IncludesPreRelease(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	gitRun(t, dir, "tag", "v1.0.0")
	if err := os.WriteFile(filepath.Join(dir, "rc.txt"), []byte("rc\n"), 0644); err != nil {
		t.Fatalf("write rc.txt: %v", err)
	}
	gitRun(t, dir, "add", "rc.txt")
	gitRun(t, dir, "commit", "-m", "rc")
	gitRun(t, dir, "tag", "v1.1.0-rc.1")

	tags, err := AllSemverTags(dir)
	if err != nil {
		t.Fatalf("AllSemverTags: unexpected error: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("AllSemverTags: got %v, want 2 tags", tags)
	}
	// Both strict and pre-release tags should be present.
	found := map[string]bool{}
	for _, tag := range tags {
		found[tag] = true
	}
	if !found["v1.0.0"] || !found["v1.1.0-rc.1"] {
		t.Fatalf("AllSemverTags: got %v, want v1.0.0 and v1.1.0-rc.1", tags)
	}
}

// ---------------------------------------------------------------------------
// TagExists
// ---------------------------------------------------------------------------

func TestTagExists_True(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	gitRun(t, dir, "tag", "v1.0.0")

	got, err := TagExists(dir, "v1.0.0")
	if err != nil {
		t.Fatalf("TagExists: unexpected error: %v", err)
	}
	if !got {
		t.Fatal("TagExists: got false, want true for existing tag")
	}
}

func TestTagExists_False(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	got, err := TagExists(dir, "v9.9.9")
	if err != nil {
		t.Fatalf("TagExists: unexpected error: %v", err)
	}
	if got {
		t.Fatal("TagExists: got true, want false for missing tag")
	}
}

func TestTagExists_EmptyName(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	_, err := TagExists(dir, "")
	if err == nil {
		t.Fatal("TagExists(\"\"): expected error, got nil")
	}
}

func TestTagExists_NotARepo(t *testing.T) {
	dir := t.TempDir()

	_, err := TagExists(dir, "v1.0.0")
	if err == nil {
		t.Fatal("TagExists outside repo: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// CreateTag
// ---------------------------------------------------------------------------

func TestCreateTag_InRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	if err := CreateTag(dir, "v1.0.0", "release v1.0.0"); err != nil {
		t.Fatalf("CreateTag: unexpected error: %v", err)
	}

	// Verify the tag exists and is annotated (points to a tag object, not
	// directly to the commit).
	exists, err := TagExists(dir, "v1.0.0")
	if err != nil {
		t.Fatalf("TagExists after CreateTag: unexpected error: %v", err)
	}
	if !exists {
		t.Fatal("CreateTag: tag not found after creation")
	}

	objType := gitRun(t, dir, "cat-file", "-t", "v1.0.0")
	if objType != "tag" {
		t.Fatalf("CreateTag: object type = %q, want %q (annotated tag)", objType, "tag")
	}

	msg := gitRun(t, dir, "tag", "-l", "--format=%(contents:subject)", "v1.0.0")
	if msg != "release v1.0.0" {
		t.Fatalf("CreateTag: annotation subject = %q, want %q", msg, "release v1.0.0")
	}
}

func TestCreateTag_EmptyName(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	err := CreateTag(dir, "", "message")
	if err == nil {
		t.Fatal("CreateTag(\"\"): expected error, got nil")
	}
}

func TestCreateTag_Collision(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	if err := CreateTag(dir, "v1.0.0", "first"); err != nil {
		t.Fatalf("CreateTag: unexpected error on first create: %v", err)
	}

	err := CreateTag(dir, "v1.0.0", "second")
	if err == nil {
		t.Fatal("CreateTag: expected error on duplicate tag, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("CreateTag collision error = %q, want mention of %q", err.Error(), "already exists")
	}
}

func TestCreateTag_NotARepo(t *testing.T) {
	dir := t.TempDir()

	err := CreateTag(dir, "v1.0.0", "message")
	if err == nil {
		t.Fatal("CreateTag outside repo: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// FetchTags
// ---------------------------------------------------------------------------

func TestFetchTags_InRepo(t *testing.T) {
	// A no-remote repo would let FetchTags succeed as a no-op regardless of
	// whether it actually invoked "git fetch --tags --force" — so use a
	// real local "origin" with a tag the clone doesn't have yet, and assert
	// the tag is present after FetchTags runs.
	origin := t.TempDir()
	initGitRepo(t, origin)
	gitRun(t, origin, "tag", "v1.0.0")

	dir := t.TempDir()
	gitRun(t, dir, "clone", origin, ".")
	gitRun(t, dir, "config", "user.email", "test@test.com")
	gitRun(t, dir, "config", "user.name", "Test")

	// The clone already has v1.0.0 (clone fetches tags by default); add a
	// second tag to origin after cloning so FetchTags has something new to
	// pull.
	gitRun(t, origin, "tag", "v2.0.0")

	if err := FetchTags(dir); err != nil {
		t.Fatalf("FetchTags: unexpected error: %v", err)
	}

	exists, err := TagExists(dir, "v2.0.0")
	if err != nil {
		t.Fatalf("TagExists after FetchTags: unexpected error: %v", err)
	}
	if !exists {
		t.Fatal("FetchTags: v2.0.0 not fetched from origin")
	}
}

func TestFetchTags_NotARepo(t *testing.T) {
	dir := t.TempDir()

	if err := FetchTags(dir); err == nil {
		t.Fatal("FetchTags outside repo: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// HasUpstream
// ---------------------------------------------------------------------------

func TestHasUpstream_True(t *testing.T) {
	// Create a bare "origin" and clone it so the default branch tracks
	// origin/main automatically.
	origin := t.TempDir()
	initGitRepo(t, origin)
	// Convert to bare so it can serve as a remote.
	bare := t.TempDir()
	gitRun(t, bare, "clone", "--bare", origin, ".")

	clone := t.TempDir()
	gitRun(t, clone, "clone", bare, ".")
	gitRun(t, clone, "config", "user.email", "test@test.com")
	gitRun(t, clone, "config", "user.name", "Test")

	got, err := HasUpstream(clone)
	if err != nil {
		t.Fatalf("HasUpstream: unexpected error: %v", err)
	}
	if !got {
		t.Fatal("HasUpstream: got false, want true for tracked branch")
	}
}

func TestHasUpstream_NoUpstream(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	got, err := HasUpstream(dir)
	if err != nil {
		t.Fatalf("HasUpstream: unexpected error: %v", err)
	}
	if got {
		t.Fatal("HasUpstream: got true, want false for local-only branch")
	}
}

func TestHasUpstream_NotARepo(t *testing.T) {
	dir := t.TempDir()

	_, err := HasUpstream(dir)
	if err == nil {
		t.Fatal("HasUpstream outside repo: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// CommitsAhead
// ---------------------------------------------------------------------------

func TestCommitsAhead_Zero(t *testing.T) {
	// Clone a bare repo so the branch has an upstream, then make no further
	// commits — ahead count should be 0.
	origin := t.TempDir()
	initGitRepo(t, origin)
	bare := t.TempDir()
	gitRun(t, bare, "clone", "--bare", origin, ".")

	clone := t.TempDir()
	gitRun(t, clone, "clone", bare, ".")
	gitRun(t, clone, "config", "user.email", "test@test.com")
	gitRun(t, clone, "config", "user.name", "Test")

	got, err := CommitsAhead(clone)
	if err != nil {
		t.Fatalf("CommitsAhead: unexpected error: %v", err)
	}
	if got != 0 {
		t.Fatalf("CommitsAhead: got %d, want 0", got)
	}
}

func TestCommitsAhead_Positive(t *testing.T) {
	origin := t.TempDir()
	initGitRepo(t, origin)
	bare := t.TempDir()
	gitRun(t, bare, "clone", "--bare", origin, ".")

	clone := t.TempDir()
	gitRun(t, clone, "clone", bare, ".")
	gitRun(t, clone, "config", "user.email", "test@test.com")
	gitRun(t, clone, "config", "user.name", "Test")

	// Make 2 local commits ahead of the upstream.
	for i := 0; i < 2; i++ {
		name := filepath.Join(clone, "ahead"+strings.Repeat("x", i)+".txt")
		if err := os.WriteFile(name, []byte("x\n"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		gitRun(t, clone, "add", ".")
		gitRun(t, clone, "commit", "-m", "ahead commit")
	}

	got, err := CommitsAhead(clone)
	if err != nil {
		t.Fatalf("CommitsAhead: unexpected error: %v", err)
	}
	if got != 2 {
		t.Fatalf("CommitsAhead: got %d, want 2", got)
	}
}

func TestCommitsAhead_NotARepo(t *testing.T) {
	dir := t.TempDir()

	_, err := CommitsAhead(dir)
	if err == nil {
		t.Fatal("CommitsAhead outside repo: expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// HasRemote, RemoteTrackingSHA, Push, PushForceWithLease
// ---------------------------------------------------------------------------

// fakePushRun replaces pushRun for the test and records the git args.
func fakePushRun(t *testing.T, stderr string, exitCode int, err error) *[]string {
	t.Helper()
	var got []string
	orig := pushRun
	pushRun = func(cmd string, args []string, opt execx.Options) (string, string, int, error) {
		got = append([]string{cmd}, args...)
		return "", stderr, exitCode, err
	}
	t.Cleanup(func() { pushRun = orig })
	return &got
}

// pushKindRows is the stderr table of the push error kinds.
var pushKindRows = []struct {
	stderr string
	kind   string
}{
	{"fatal: 'nowhere' does not appear to be a git repository", PushKindNoRemote},
	{"fatal: No such remote 'x'", PushKindNoRemote},
	{"remote: Authentication failed for 'https://github.com/x/y.git/'", PushKindAuth},
	{"git@github.com: Permission denied (publickey).", PushKindAuth},
	{"fatal: could not read Username for 'https://github.com'", PushKindAuth},
	{" ! [rejected]        feature -> feature (fetch first)", PushKindRejected},
	{" ! [rejected]        feature -> feature (stale info)", PushKindRejected},
	{"Updates were rejected because of a non-fast-forward", PushKindRejected},
	{"fatal: unable to access 'https://x/': Could not resolve host: x", PushKindNetwork},
	{"ssh: connect to host x: Connection timed out", PushKindNetwork},
	{"fatal: unable to access 'https://x/': Failed to connect to x port 443: Connection refused", PushKindNetwork},
	{"error: something odd", PushKindOther},
	{"", PushKindOther},
	// A local file error prints "Permission denied" with no publickey: not auth.
	{"error: unable to create '.git/index.lock': Permission denied", PushKindOther},
	// HTTP status rows: the text also holds "unable to access" (network), and
	// the status row wins.
	{"fatal: unable to access 'https://x/': The requested URL returned error: 401", PushKindAuth},
	{"fatal: unable to access 'https://x/': The requested URL returned error: 403", PushKindAuth},
	{"fatal: unable to access 'https://x/': The requested URL returned error: 404", PushKindNoRemote},
	// Text that matches two rows pins the row order.
	{"fatal: 'x' does not appear to be a git repository\ngit@x: Permission denied (publickey).", PushKindNoRemote},
	{"remote: Authentication failed\n ! [rejected]        feature -> feature (fetch first)", PushKindAuth},
	{"fatal: Authentication failed\nfatal: unable to access 'https://x/': Could not resolve host: x", PushKindAuth},
	{" ! [rejected]        feature -> feature (non-fast-forward)\nssh: connect to host x: Connection timed out", PushKindRejected},
}

func TestPushKindTable(t *testing.T) {
	for _, row := range pushKindRows {
		t.Run(row.kind+"/"+row.stderr, func(t *testing.T) {
			fakePushRun(t, row.stderr, 1, nil)
			err := Push(t.TempDir(), "origin")
			var pe *PushError
			if !errors.As(err, &pe) || pe.Kind != row.kind {
				t.Fatalf("Push error = %v; want kind %q", err, row.kind)
			}
		})
	}
}

func TestPushArgs(t *testing.T) {
	got := fakePushRun(t, "", 0, nil)
	if err := Push(t.TempDir(), "origin"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if want := "git push -u origin HEAD"; strings.Join(*got, " ") != want {
		t.Fatalf("args = %q, want %q", strings.Join(*got, " "), want)
	}
	if err := PushForceWithLease(t.TempDir(), "origin", Lease{"feat/x", "abc123"}); err != nil {
		t.Fatalf("PushForceWithLease: %v", err)
	}
	want := []string{"git", "push", "--force-with-lease=refs/heads/feat/x:abc123", "-u", "origin", "HEAD"}
	if strings.Join(*got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %q, want %q", *got, want)
	}
}

func TestPushError_FirstLineAndRedaction(t *testing.T) {
	fakePushRun(t, "\nfatal: Authentication failed for 'https://user:tok123@github.com/x/y.git/'\nsecond line", 128, nil)
	err := Push(t.TempDir(), "origin")
	var pe *PushError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v", err)
	}
	want := "fatal: Authentication failed for 'https://***@github.com/x/y.git/'"
	if pe.Stderr != want {
		t.Fatalf("Stderr = %q, want %q", pe.Stderr, want)
	}
	if strings.Contains(err.Error(), "tok123") {
		t.Fatal("error leaks the URL credential")
	}
}

// TestFirstStderrLine_Redaction: the whole user info part of a URL is hidden,
// also when the password holds an unencoded '@'.
func TestFirstStderrLine_Redaction(t *testing.T) {
	rows := []struct{ in, want string }{
		{"fatal: https://user:p@ss@host/repo", "fatal: https://***@host/repo"},
		{"fatal: https://user:tok@host/repo", "fatal: https://***@host/repo"},
		{"fatal: https://host/repo by me@x", "fatal: https://host/repo by me@x"},
		{"a https://u:p@h1/r and https://v:q@h2/r", "a https://***@h1/r and https://***@h2/r"},
	}
	for _, row := range rows {
		if got := firstStderrLine(row.in); got != row.want {
			t.Errorf("firstStderrLine(%q) = %q, want %q", row.in, got, row.want)
		}
	}
}

// TestFirstStderrLine_Cap: a line of pushStderrMax runes is kept whole; a
// longer line is cut at pushStderrMax runes, never inside a character.
func TestFirstStderrLine_Cap(t *testing.T) {
	atCap := strings.Repeat("a", pushStderrMax)
	if got := firstStderrLine(atCap); got != atCap {
		t.Errorf("line at the cap: got %d runes, want it unchanged", len([]rune(got)))
	}
	overCap := strings.Repeat("a", pushStderrMax+1)
	if got, want := firstStderrLine(overCap), strings.Repeat("a", pushStderrMax)+"..."; got != want {
		t.Errorf("line over the cap: got %q, want %q", got, want)
	}
	multi := strings.Repeat("é", pushStderrMax+1)
	got := firstStderrLine(multi)
	if want := strings.Repeat("é", pushStderrMax) + "..."; got != want {
		t.Errorf("multi-byte line over the cap: got %q, want %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Errorf("multi-byte cut gave invalid UTF-8: %q", got)
	}
}

func TestPushError_RunFailureIsKindOther(t *testing.T) {
	fakePushRun(t, "", 0, errors.New("exec: git: not found"))
	err := Push(t.TempDir(), "origin")
	var pe *PushError
	if !errors.As(err, &pe) || pe.Kind != PushKindOther || !strings.Contains(pe.Stderr, "not found") {
		t.Fatalf("err = %v", err)
	}
	if pe.Cause == nil || pe.Cause.Error() != "exec: git: not found" {
		t.Fatalf("Cause = %v, want the run error", pe.Cause)
	}
}

// TestPushError_UnwrapKeepsTheRunError: errors.Is sees the run error through
// a *PushError.
func TestPushError_UnwrapKeepsTheRunError(t *testing.T) {
	runErr := errors.New("exec: git: not found")
	fakePushRun(t, "", 0, runErr)
	if err := Push(t.TempDir(), "origin"); !errors.Is(err, runErr) {
		t.Fatalf("errors.Is(err, runErr) = false; err = %v", err)
	}
}

// TestPushError_OutputCapIsNotAPushError: an output over the execx cap keeps
// execx.ErrOutputCap and is not turned into a soft push failure.
func TestPushError_OutputCapIsNotAPushError(t *testing.T) {
	fakePushRun(t, "", 0, fmt.Errorf("execx: git: %w", execx.ErrOutputCap))
	err := Push(t.TempDir(), "origin")
	if !errors.Is(err, execx.ErrOutputCap) {
		t.Fatalf("errors.Is(err, ErrOutputCap) = false; err = %v", err)
	}
	var pe *PushError
	if errors.As(err, &pe) {
		t.Fatalf("err = %v is a *PushError; want a plain error", err)
	}
}

func TestPushValidation(t *testing.T) {
	if err := Push(t.TempDir(), ""); err == nil || !strings.Contains(err.Error(), "remote is empty") {
		t.Fatalf("Push(\"\") error = %v", err)
	}
	if err := Push(t.TempDir(), "--delete"); err == nil || !strings.Contains(err.Error(), "looks like a flag") {
		t.Fatalf("Push(--delete) error = %v", err)
	}
	for _, remote := range []string{"https://x/y.git", "ext::sh -c x"} {
		if err := Push(t.TempDir(), remote); err == nil || !strings.Contains(err.Error(), "not a remote name") {
			t.Fatalf("Push(%q) error = %v; want a remote-name error", remote, err)
		}
	}
}

// TestPushForceWithLeaseValidation: every input of PushForceWithLease is
// checked before git runs.
func TestPushForceWithLeaseValidation(t *testing.T) {
	ran := fakePushRun(t, "", 0, nil)
	rows := []struct {
		name   string
		remote string
		lease  Lease
		want   string
	}{
		{"empty remote", "", Lease{"main", "abc123"}, "gitx: push force with lease: remote is empty"},
		{"empty branch", "origin", Lease{"", "abc123"}, "gitx: push force with lease: branch is empty"},
		{"empty sha", "origin", Lease{"main", ""}, "gitx: push force with lease: expected sha is empty"},
		{"flag-like remote", "--x", Lease{"main", "abc123"}, "looks like a flag"},
		{"flag-like branch", "origin", Lease{"--x", "abc123"}, "looks like a flag"},
		{"flag-like sha", "origin", Lease{"main", "--x"}, "looks like a flag"},
		{"URL remote", "https://x/y.git", Lease{"main", "abc123"}, "not a remote name"},
		{"branch with colon", "origin", Lease{"a:b", "abc123"}, "holds ':'"},
		{"sha not hex", "origin", Lease{"main", "feat/x"}, "is not a hex commit id"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			err := PushForceWithLease(t.TempDir(), row.remote, row.lease)
			if err == nil || !strings.Contains(err.Error(), row.want) {
				t.Fatalf("error = %v; want text %q", err, row.want)
			}
		})
	}
	if len(*ran) != 0 {
		t.Fatalf("git ran with %q; want no run for a bad input", *ran)
	}
}

func TestRemoteNameValidation(t *testing.T) {
	if _, err := HasRemote(t.TempDir(), "https://x/y.git"); err == nil || !strings.Contains(err.Error(), "not a remote name") {
		t.Fatalf("HasRemote(URL) error = %v", err)
	}
	if _, _, err := RemoteTrackingSHA(t.TempDir(), "ext::x", "main"); err == nil || !strings.Contains(err.Error(), "not a remote name") {
		t.Fatalf("RemoteTrackingSHA(transport) error = %v", err)
	}
}

// TestPushParity: the commit push and the pr push sort failures with the
// same kinds. Every kind row gives the same kind through Push and through
// PushForceWithLease.
func TestPushParity(t *testing.T) {
	for _, row := range pushKindRows {
		fakePushRun(t, row.stderr, 1, nil)
		var plain, lease *PushError
		if !errors.As(Push(t.TempDir(), "origin"), &plain) {
			t.Fatalf("%q: Push gave no PushError", row.stderr)
		}
		if !errors.As(PushForceWithLease(t.TempDir(), "origin", Lease{"b", "abc123"}), &lease) {
			t.Fatalf("%q: PushForceWithLease gave no PushError", row.stderr)
		}
		if plain.Kind != row.kind || lease.Kind != row.kind || plain.Stderr != lease.Stderr {
			t.Fatalf("%q: plain=%+v lease=%+v; want kind %q for both", row.stderr, plain, lease, row.kind)
		}
	}
}

func TestHasRemote(t *testing.T) {
	clone, _ := newRepoWithBareOrigin(t)
	if got, err := HasRemote(clone, "origin"); err != nil || !got {
		t.Fatalf("HasRemote(origin) = %v, %v; want true, nil", got, err)
	}
	if got, err := HasRemote(clone, "upstream"); err != nil || got {
		t.Fatalf("HasRemote(upstream) = %v, %v; want false, nil", got, err)
	}
	local := t.TempDir()
	initGitRepo(t, local)
	if got, err := HasRemote(local, "origin"); err != nil || got {
		t.Fatalf("HasRemote(no remotes) = %v, %v; want false, nil", got, err)
	}
	if _, err := HasRemote(clone, "--delete"); err == nil || !strings.Contains(err.Error(), "looks like a flag") {
		t.Fatalf("HasRemote(--delete) error = %v; want flag error", err)
	}
	if _, err := HasRemote(clone, ""); err == nil {
		t.Fatal("HasRemote(\"\"): expected error")
	}
	if _, err := HasRemote(t.TempDir(), "origin"); err == nil {
		t.Fatal("HasRemote outside a repository: expected error")
	}
}

func TestRemoteTrackingSHA(t *testing.T) {
	clone, _ := newRepoWithBareOrigin(t)
	head := gitRun(t, clone, "rev-parse", "HEAD")
	got, ok, err := RemoteTrackingSHA(clone, "origin", "main")
	if err != nil || !ok || got != head {
		t.Fatalf("RemoteTrackingSHA(main) = %q, %v, %v; want %q, true, nil", got, ok, err, head)
	}
	got, ok, err = RemoteTrackingSHA(clone, "origin", "no-such-branch")
	if err != nil || ok || got != "" {
		t.Fatalf("RemoteTrackingSHA(missing) = %q, %v, %v; want empty, false, nil", got, ok, err)
	}
	if _, _, err := RemoteTrackingSHA(clone, "origin", "--x"); err == nil {
		t.Fatal("RemoteTrackingSHA(--x): expected error")
	}
	if _, _, err := RemoteTrackingSHA(t.TempDir(), "origin", "main"); err == nil {
		t.Fatal("RemoteTrackingSHA outside a repository: expected error")
	}
}

// pushOtherCommit clones bare, adds a commit on branch and pushes it, as a
// second person would.
func pushOtherCommit(t *testing.T, bare, branch string) string {
	t.Helper()
	other := t.TempDir()
	gitRun(t, other, "clone", bare, ".")
	gitRun(t, other, "config", "user.email", "o@test.com")
	gitRun(t, other, "config", "user.name", "Other")
	gitRun(t, other, "checkout", branch)
	gitRun(t, other, "commit", "--allow-empty", "-m", "other work")
	gitRun(t, other, "push", "origin", branch)
	return gitRun(t, other, "rev-parse", "HEAD")
}

func TestPush_RealGitSetsUpstream(t *testing.T) {
	clone, bare := newRepoWithBareOrigin(t)
	gitRun(t, clone, "checkout", "-b", "feature")
	if err := Push(clone, "origin"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got := gitRun(t, bare, "rev-parse", "feature"); got != gitRun(t, clone, "rev-parse", "HEAD") {
		t.Fatalf("remote feature = %q, want HEAD", got)
	}
	if ok, err := HasUpstream(clone); err != nil || !ok {
		t.Fatalf("HasUpstream after Push = %v, %v; want true", ok, err)
	}
}

func TestPush_RealGitMissingRemote(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	var pe *PushError
	if err := Push(dir, "nowhere"); !errors.As(err, &pe) || pe.Kind != PushKindNoRemote {
		t.Fatalf("Push to a missing remote = %v; want kind %q", err, PushKindNoRemote)
	}
}

func TestPushForceWithLease_RealGitRebasedBranchPushes(t *testing.T) {
	clone, bare := newRepoWithBareOrigin(t)
	gitRun(t, clone, "checkout", "-b", "feature")
	gitRun(t, clone, "commit", "--allow-empty", "-m", "one")
	if err := Push(clone, "origin"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	gitRun(t, clone, "commit", "--amend", "--allow-empty", "-m", "one rewritten")
	var pe *PushError
	if err := Push(clone, "origin"); !errors.As(err, &pe) || pe.Kind != PushKindRejected {
		t.Fatalf("plain push of a rewritten branch = %v; want kind rejected", err)
	}
	sha, ok, err := RemoteTrackingSHA(clone, "origin", "feature")
	if err != nil || !ok {
		t.Fatalf("RemoteTrackingSHA = %q, %v, %v", sha, ok, err)
	}
	if err := PushForceWithLease(clone, "origin", Lease{"feature", sha}); err != nil {
		t.Fatalf("PushForceWithLease: %v", err)
	}
	if got := gitRun(t, bare, "rev-parse", "feature"); got != gitRun(t, clone, "rev-parse", "HEAD") {
		t.Fatalf("remote feature = %q, want HEAD", got)
	}
}

func TestPushForceWithLease_RealGitAnotherPersonPushed(t *testing.T) {
	clone, bare := newRepoWithBareOrigin(t)
	gitRun(t, clone, "checkout", "-b", "feature")
	gitRun(t, clone, "commit", "--allow-empty", "-m", "one")
	if err := Push(clone, "origin"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	sha, _, _ := RemoteTrackingSHA(clone, "origin", "feature")
	otherHead := pushOtherCommit(t, bare, "feature")

	gitRun(t, clone, "commit", "--amend", "--allow-empty", "-m", "one rewritten")
	err := PushForceWithLease(clone, "origin", Lease{"feature", sha})
	var pe *PushError
	if !errors.As(err, &pe) || pe.Kind != PushKindRejected {
		t.Fatalf("PushForceWithLease = %v; want kind rejected", err)
	}
	if got := gitRun(t, bare, "rev-parse", "feature"); got != otherHead {
		t.Fatalf("remote feature = %q, want the other commit %q", got, otherHead)
	}
}
