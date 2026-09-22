package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// commit_prepare tests
// ---------------------------------------------------------------------------

// redirectTempManifests points the fsseam's mkdirTempFunc at a t.TempDir()
// root for the duration of t. commitPrepare always ends by writing its
// manifest through mkdirTempFunc("", "sdlc-commit-manifest-"); without this
// redirect the directory lands in os.TempDir(), nothing removes it, and every
// `go test` run leaks one sdlc-commit-manifest-* directory per test that calls
// commitPrepare with the real fsseam installed.
//
// Unlike installFakeFS this keeps real file I/O, so tests that assert on
// git-driven behaviour are unaffected — only the manifest's destination moves.
// It returns the root so a test can list what the code under test left behind.
func redirectTempManifests(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	orig := mkdirTempFunc
	mkdirTempFunc = func(dir, pattern string) (string, error) {
		if dir == "" {
			dir = root
		}
		return os.MkdirTemp(dir, pattern)
	}
	t.Cleanup(func() { mkdirTempFunc = orig })
	return root
}

// tempEntries returns the names inside root, the directory that
// redirectTempManifests returned.
func tempEntries(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read temp root %q: %v", root, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestCommitPrepare_KeySet verifies that CommitPrepareOut marshals exactly
// the top-level and nested key sets expected by the commit orchestrator.
func TestCommitPrepare_KeySet(t *testing.T) {
	redirectTempManifests(t)
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	// Stage a change so we get staged info.
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "hello.txt")

	out, err := commitPrepare(dir, dir, CommitPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("commitPrepare: %v", err)
	}

	// Marshal to map for key-set verification.
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Top-level keys.
	expectedTopKeys := []string{
		"errors", "warnings", "currentBranch", "defaultBranch",
		"onDefaultBranch", "flags", "migration", "commitConfig",
		"staged", "unstaged", "untracked", "recentCommits",
		"lastCommitMessage", "wipSquash", "branchGuard", "next",
		"manifestPath",
	}
	for _, k := range expectedTopKeys {
		if _, ok := m[k]; !ok {
			t.Errorf("missing top-level key: %s", k)
		}
	}

	// Staged nested keys.
	staged, ok := m["staged"].(map[string]any)
	if !ok {
		t.Fatal("staged is not an object")
	}
	for _, k := range []string{"files", "fileCount", "diff", "diffStat", "diffTruncated", "truncatedFiles"} {
		if _, ok := staged[k]; !ok {
			t.Errorf("missing staged key: %s", k)
		}
	}

	// Unstaged nested keys.
	unstaged, ok := m["unstaged"].(map[string]any)
	if !ok {
		t.Fatal("unstaged is not an object")
	}
	for _, k := range []string{"files", "fileCount", "hasChanges"} {
		if _, ok := unstaged[k]; !ok {
			t.Errorf("missing unstaged key: %s", k)
		}
	}

	// Untracked nested keys.
	untracked, ok := m["untracked"].(map[string]any)
	if !ok {
		t.Fatal("untracked is not an object")
	}
	for _, k := range []string{"files", "fileCount"} {
		if _, ok := untracked[k]; !ok {
			t.Errorf("missing untracked key: %s", k)
		}
	}

	// Flags nested keys.
	flags, ok := m["flags"].(map[string]any)
	if !ok {
		t.Fatal("flags is not an object")
	}
	for _, k := range []string{"noStash", "scope", "type", "amend", "auto", "noSquashWip", "skipConfigCheck", "forceDefaultBranch"} {
		if _, ok := flags[k]; !ok {
			t.Errorf("missing flags key: %s", k)
		}
	}

	// WipSquash nested keys.
	wipSquash, ok := m["wipSquash"].(map[string]any)
	if !ok {
		t.Fatal("wipSquash is not an object")
	}
	for _, k := range []string{"commits", "stagedClean"} {
		if _, ok := wipSquash[k]; !ok {
			t.Errorf("missing wipSquash key: %s", k)
		}
	}

	// BranchGuard nested keys.
	branchGuard, ok := m["branchGuard"].(map[string]any)
	if !ok {
		t.Fatal("branchGuard is not an object")
	}
	if _, ok := branchGuard["ok"]; !ok {
		t.Error("missing branchGuard key: ok")
	}

	if out.Next != "Call commit_apply with the prepared payload." {
		t.Errorf("Next: got %q", out.Next)
	}
}

// TestCommitPrepare_ManifestPath verifies that commit_prepare writes its
// entire result to disk via the fsseam and returns a readable ManifestPath
// instead of relying on the caller to round-trip the full JSON payload
// through its own context.
func TestCommitPrepare_ManifestPath(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	// The manifest write/read goes through the fsseam (mkdirTempFunc,
	// writeFileFunc, readFileFunc) — install fakes so this test never
	// touches the real filesystem for that path. dir/initGitFixture above
	// is the real git repo fixture commitPrepare needs to run git commands
	// against; it is unrelated to the fsseam and out of scope here.
	installFakeFS(t)

	out, err := commitPrepare(dir, dir, CommitPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("commitPrepare: %v", err)
	}

	if out.ManifestPath == "" {
		t.Fatal("expected non-empty ManifestPath")
	}

	raw, err := readFileFunc(out.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest file %q: %v", out.ManifestPath, err)
	}

	var manifest CommitPrepareOut
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest file %q: %v", out.ManifestPath, err)
	}
	if manifest.CurrentBranch != out.CurrentBranch {
		t.Errorf("manifest CurrentBranch = %q, want %q", manifest.CurrentBranch, out.CurrentBranch)
	}
	if manifest.ManifestPath != out.ManifestPath {
		t.Errorf("manifest ManifestPath = %q, want %q", manifest.ManifestPath, out.ManifestPath)
	}
}

// TestCommitPrepare_ManifestWriteFailure verifies that when the fsseam's
// mkdirTempFunc fails, commit_prepare soft-fails: it appends a warning and
// leaves ManifestPath empty, consistent with the rest of this function's
// all-soft-fail error style (it never returns a non-nil error).
func TestCommitPrepare_ManifestWriteFailure(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	origMkdirTemp := mkdirTempFunc
	mkdirTempFunc = func(string, string) (string, error) {
		return "", os.ErrPermission
	}
	defer func() { mkdirTempFunc = origMkdirTemp }()

	out, err := commitPrepare(dir, dir, CommitPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("commitPrepare returned an error, want soft-fail: %v", err)
	}
	if out.ManifestPath != "" {
		t.Errorf("ManifestPath = %q, want empty on write failure", out.ManifestPath)
	}
	found := false
	for _, w := range out.Warnings {
		if strings.Contains(w, "manifestPath") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Warnings = %v, want one mentioning manifestPath", out.Warnings)
	}
}

// TestCommitPrepare_ManifestFileWriteFailure covers the second failure mode in
// writeCommitManifest: the temp directory is created but the file write fails.
// TestCommitPrepare_ManifestWriteFailure above only fails mkdirTempFunc, so
// without this case the writeFileFunc branch never runs.
func TestCommitPrepare_ManifestFileWriteFailure(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	installFakeFS(t)
	origWriteFile := writeFileFunc
	writeFileFunc = func(string, []byte, os.FileMode) error {
		return os.ErrPermission
	}
	t.Cleanup(func() { writeFileFunc = origWriteFile })

	out, err := commitPrepare(dir, dir, CommitPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("commitPrepare returned an error, want soft-fail: %v", err)
	}
	if out.ManifestPath != "" {
		t.Errorf("ManifestPath = %q, want empty when the manifest file write fails", out.ManifestPath)
	}
	found := false
	for _, w := range out.Warnings {
		if strings.Contains(w, "manifestPath") && strings.Contains(w, "write manifest file") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Warnings = %v, want one naming manifestPath and the failed file write", out.Warnings)
	}
}

// TestCommitPrepare_ManifestFileWriteFailureRemovesTempDir runs the failed
// manifest write against a real temp root: the sdlc-commit-manifest-* dir that
// was created must be removed, not left behind empty.
func TestCommitPrepare_ManifestFileWriteFailureRemovesTempDir(t *testing.T) {
	root := redirectTempManifests(t)
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	origWriteFile := writeFileFunc
	writeFileFunc = func(string, []byte, os.FileMode) error {
		return os.ErrPermission
	}
	t.Cleanup(func() { writeFileFunc = origWriteFile })

	out, err := commitPrepare(dir, dir, CommitPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("commitPrepare returned an error, want soft-fail: %v", err)
	}
	if out.ManifestPath != "" {
		t.Errorf("ManifestPath = %q, want empty when the manifest file write fails", out.ManifestPath)
	}
	if left := tempEntries(t, root); len(left) != 0 {
		t.Errorf("temp root still holds %v after a failed manifest write, want it empty", left)
	}
}

// TestCommitPrepare_NilSlicesSerializeAsArrays verifies that all slice
// fields serialize as JSON arrays (not null).
func TestCommitPrepare_NilSlicesSerializeAsArrays(t *testing.T) {
	redirectTempManifests(t)
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	out, err := commitPrepare(dir, dir, CommitPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("commitPrepare: %v", err)
	}

	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	raw := string(data)

	// These slice fields must never be null.
	sliceFields := []string{
		`"errors":[]`, `"warnings":[]`, `"recentCommits":[`,
	}
	for _, pattern := range sliceFields {
		if !strings.Contains(raw, pattern) {
			// Check for null.
			fieldName := strings.Split(pattern, ":")[0] + `:null`
			if strings.Contains(raw, fieldName) {
				t.Errorf("field %s serialized as null, expected array", strings.Split(pattern, ":")[0])
			}
		}
	}
}

// TestCommitPrepare_NoStagedFiles verifies that with nothing staged,
// errors contains the expected message but no Go error is returned.
func TestCommitPrepare_NoStagedFiles(t *testing.T) {
	redirectTempManifests(t)
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	out, err := commitPrepare(dir, dir, CommitPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("commitPrepare: %v", err)
	}

	found := false
	for _, e := range out.Errors {
		if strings.Contains(e, "no files staged") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'no files staged' in errors, got: %v", out.Errors)
	}
	if out.Next != "Fix the errors above, then call commit_prepare again." {
		t.Errorf("Next: got %q", out.Next)
	}
}

// ---------------------------------------------------------------------------
// commit_apply tests
// ---------------------------------------------------------------------------

// TestCommitApply_Happy verifies commit_apply creates a commit and returns
// the SHA matching HEAD.
func TestCommitApply_Happy(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	// Create a new file and stage it: commit_apply commits what is already
	// staged, but never stages an untracked file itself.
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "new.txt")

	out, err := commitApply(dir, dir, CommitApplyIn{
		Message:         "feat: add new file",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("commitApply: %v", err)
	}

	if out.SHA == "" {
		t.Fatal("expected non-empty SHA")
	}
	if out.SkippedUntrackedPaths == nil || len(out.SkippedUntrackedPaths) != 0 {
		t.Errorf("SkippedUntrackedPaths: want empty non-nil slice, got %#v", out.SkippedUntrackedPaths)
	}
	if !strings.Contains(out.Summary, "Committed "+out.SHA[:7]) {
		t.Errorf("Summary should name the short SHA, got %q", out.Summary)
	}

	// Verify SHA matches HEAD.
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	headOut, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	headSHA := strings.TrimSpace(string(headOut))
	if out.SHA != headSHA {
		t.Errorf("SHA mismatch: got %s, HEAD is %s", out.SHA, headSHA)
	}

	// Verify clean working tree.
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	statusOut, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if strings.TrimSpace(string(statusOut)) != "" {
		t.Errorf("expected clean working tree, got: %s", statusOut)
	}
}

// TestCommitApply_EmptyMessage verifies commit_apply rejects empty messages.
func TestCommitApply_EmptyMessage(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := commitApply(dir, dir, CommitApplyIn{
		Message:         "",
		SkipConfigCheck: true,
	})
	if err == nil {
		t.Fatal("expected error for empty message")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected 'empty' in error, got: %s", err.Error())
	}
}

// TestCommitApply_NothingToCommit verifies commit_apply fails when the
// working tree is clean.
func TestCommitApply_NothingToCommit(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	_, err := commitApply(dir, dir, CommitApplyIn{
		Message:         "feat: empty commit",
		SkipConfigCheck: true,
	})
	if err == nil {
		t.Fatal("expected error for nothing to commit")
	}
	if !strings.Contains(err.Error(), "nothing to commit") {
		t.Errorf("expected 'nothing to commit' in error, got: %s", err.Error())
	}
}

// TestCommitApply_WorktreeUnchangedOnFailure verifies the "on failure
// worktree unchanged" AC by checking status is identical before and after
// a failed commit.
func TestCommitApply_WorktreeUnchangedOnFailure(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	// Get status before.
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	beforeOut, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status before: %v", err)
	}

	// Attempt commit with empty message.
	_, _ = commitApply(dir, dir, CommitApplyIn{
		Message:         "",
		SkipConfigCheck: true,
	})

	// Get status after.
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	afterOut, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status after: %v", err)
	}

	if string(beforeOut) != string(afterOut) {
		t.Errorf("worktree changed after failed commit:\nbefore: %s\nafter: %s", beforeOut, afterOut)
	}
}

// ---------------------------------------------------------------------------
// commit_apply scoped staging tests
//
// These use a real git repository under t.TempDir (permitted by the
// no-real-fs-git-in-tests guardrail). user.name, user.email and
// commit.gpgsign are pinned repo-locally so nothing depends on the host's git
// config.
// ---------------------------------------------------------------------------

// newCommitApplyRepo creates a repository with one commit that tracks
// initial.txt and keep.txt.
func newCommitApplyRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	initGitFixture(t, dir)
	runGit(t, dir, "config", "commit.gpgsign", "false")
	gitCommit(t, dir, "initial")
	writeRepoFile(t, dir, "keep.txt", "keep")
	runGit(t, dir, "add", "keep.txt")
	runGit(t, dir, "commit", "-m", "add keep.txt")
	return dir
}

// writeRepoFile writes content to rel under dir, creating parent directories.
func writeRepoFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// gitOutTrim runs git in dir and returns trimmed stdout, failing the test on
// error.
func gitOutTrim(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// applyCommit calls commitApply with a fixed message and fails the test on error.
func applyCommit(t *testing.T, dir string) CommitApplyOut {
	t.Helper()
	out, err := commitApply(dir, dir, CommitApplyIn{Message: "chore: scoped commit", SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("commitApply: %v", err)
	}
	return out
}

// TestCommitApply_ScopedStaging_LeavesRuntimeStateUntracked pins issue #47b:
// with one intended tracked change and an untracked .sdlc-v2/runs/x.json, the
// commit holds only the intended change and the untracked file is neither
// staged nor deleted.
func TestCommitApply_ScopedStaging_LeavesRuntimeStateUntracked(t *testing.T) {
	dir := newCommitApplyRepo(t)
	writeRepoFile(t, dir, "initial.txt", "changed")
	writeRepoFile(t, dir, filepath.Join(paths.DataDir, "runs", "x.json"), "{}")

	out := applyCommit(t, dir)

	if got := gitOutTrim(t, dir, "show", "--name-only", "--format=", "HEAD"); got != "initial.txt" {
		t.Errorf("commit should hold only initial.txt, got:\n%s", got)
	}
	if got := gitOutTrim(t, dir, "ls-files", "--others", "--exclude-standard"); got != paths.DataDir+"/runs/x.json" {
		t.Errorf("runtime file should still be untracked, ls-files --others got:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, paths.DataDir, "runs", "x.json")); err != nil {
		t.Errorf("untracked runtime file must not be deleted: %v", err)
	}
	// git collapses the wholly untracked directory to one entry (".sdlc-v2/").
	if len(out.SkippedUntrackedPaths) != 1 || !strings.HasPrefix(out.SkippedUntrackedPaths[0], paths.DataDir+"/") {
		t.Errorf("SkippedUntrackedPaths should name the runtime directory, got %#v", out.SkippedUntrackedPaths)
	}
}

// TestCommitApply_UntrackedFileSkippedAndReported verifies an untracked file
// outside the staged/tracked set is left untracked and named in the output,
// and that the Summary does not claim it was committed.
func TestCommitApply_UntrackedFileSkippedAndReported(t *testing.T) {
	dir := newCommitApplyRepo(t)
	writeRepoFile(t, dir, "initial.txt", "changed")
	writeRepoFile(t, dir, "stray.txt", "stray")

	out := applyCommit(t, dir)

	if got := gitOutTrim(t, dir, "show", "--name-only", "--format=", "HEAD"); got != "initial.txt" {
		t.Errorf("commit should hold only initial.txt, got:\n%s", got)
	}
	if len(out.SkippedUntrackedPaths) != 1 || out.SkippedUntrackedPaths[0] != "stray.txt" {
		t.Errorf("SkippedUntrackedPaths: want [stray.txt], got %#v", out.SkippedUntrackedPaths)
	}
	if !strings.Contains(out.Summary, "stray.txt") || !strings.Contains(out.Summary, "NOT committed") {
		t.Errorf("Summary must name the skipped file and say it was not committed, got %q", out.Summary)
	}
	if got := gitOutTrim(t, dir, "ls-files", "--others", "--exclude-standard"); got != "stray.txt" {
		t.Errorf("stray.txt should still be untracked, got:\n%s", got)
	}
}

// TestCommitApply_CommitsTrackedDeletion verifies a tracked file deleted from
// the working tree is staged as a deletion by the explicit path list.
func TestCommitApply_CommitsTrackedDeletion(t *testing.T) {
	dir := newCommitApplyRepo(t)
	if err := os.Remove(filepath.Join(dir, "initial.txt")); err != nil {
		t.Fatal(err)
	}

	applyCommit(t, dir)

	if got := gitOutTrim(t, dir, "show", "--name-status", "--format=", "HEAD"); got != "D\tinitial.txt" {
		t.Errorf("commit should delete initial.txt, got:\n%s", got)
	}
	if got := gitOutTrim(t, dir, "ls-files", "initial.txt"); got != "" {
		t.Errorf("initial.txt should no longer be tracked, got %q", got)
	}
}

// TestCommitApply_StagedRenameCommitsBothSides verifies a staged rename (git
// mv) plus an unstaged tracked edit commits all three changes. It also pins
// that already-staged paths are not passed to git add, which fails with
// "pathspec did not match" on a path staged as deleted.
func TestCommitApply_StagedRenameCommitsBothSides(t *testing.T) {
	dir := newCommitApplyRepo(t)
	runGit(t, dir, "mv", "initial.txt", "renamed.txt")
	writeRepoFile(t, dir, "keep.txt", "edited")

	applyCommit(t, dir)

	// git lists changes in path order: initial.txt, keep.txt, renamed.txt.
	want := "D\tinitial.txt\nM\tkeep.txt\nA\trenamed.txt"
	if got := gitOutTrim(t, dir, "show", "--name-status", "--no-renames", "--format=", "HEAD"); got != want {
		t.Errorf("commit should hold the rename's two sides and the edit.\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestCommitApply_OnlyUntracked_ErrorNamesPathspec verifies the "something is
// staged" guard still fires when the pathspec matches nothing, that its text
// names the (empty) pathspec and the skipped untracked file, and that the
// error carries a recovery suggestion.
func TestCommitApply_OnlyUntracked_ErrorNamesPathspec(t *testing.T) {
	dir := newCommitApplyRepo(t)
	writeRepoFile(t, dir, "stray.txt", "stray")
	headBefore := gitOutTrim(t, dir, "rev-parse", "HEAD")

	_, err := commitApply(dir, dir, CommitApplyIn{Message: "chore: nothing", SkipConfigCheck: true})
	if err == nil {
		t.Fatal("expected an error when only untracked files exist")
	}
	for _, want := range []string{"nothing to commit", "pathspec (none)", "stray.txt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q, got: %s", want, err.Error())
		}
	}
	var de *mcpserver.DataError
	if !errors.As(err, &de) || strings.TrimSpace(de.Suggestion) == "" {
		t.Errorf("error should be a *mcpserver.DataError with a non-empty Suggestion, got %#v", err)
	}
	if got := gitOutTrim(t, dir, "rev-parse", "HEAD"); got != headBefore {
		t.Errorf("HEAD moved on a failed commit: %s -> %s", headBefore, got)
	}
	if got := gitOutTrim(t, dir, "ls-files", "--others", "--exclude-standard"); got != "stray.txt" {
		t.Errorf("stray.txt should still be untracked, got:\n%s", got)
	}
}

// TestCommitApply_TrackedDataDirChangeNotStaged verifies a tracked file under
// .sdlc-v2/ (config.toml is tracked by design) is never staged by this tool:
// it stays a pending working-tree modification, and the output says so instead
// of claiming nothing was left behind.
func TestCommitApply_TrackedDataDirChangeNotStaged(t *testing.T) {
	dir := newCommitApplyRepo(t)
	cfg := trackDataDirConfig(t, dir)
	writeRepoFile(t, dir, cfg, "a = 2\n")
	writeRepoFile(t, dir, "initial.txt", "changed")

	out := applyCommit(t, dir)

	if got := gitOutTrim(t, dir, "show", "--name-only", "--format=", "HEAD"); got != "initial.txt" {
		t.Errorf("commit should hold only initial.txt, got:\n%s", got)
	}
	if got := gitOutTrim(t, dir, "status", "--porcelain"); got != "M "+cfg && got != " M "+cfg {
		t.Errorf("%s should remain a pending modification, git status got %q", cfg, got)
	}
	if len(out.SkippedTrackedPaths) != 1 || out.SkippedTrackedPaths[0] != cfg {
		t.Errorf("SkippedTrackedPaths: want [%s], got %#v", cfg, out.SkippedTrackedPaths)
	}
	if !strings.Contains(out.Summary, cfg) || !strings.Contains(out.Summary, "NOT committed") {
		t.Errorf("Summary must name the skipped tracked path and say it was not committed, got %q", out.Summary)
	}
	for _, want := range []string{cfg, "git add", "Do not report them as committed"} {
		if !strings.Contains(out.Next, want) {
			t.Errorf("Next should contain %q, got %q", want, out.Next)
		}
	}
}

// trackDataDirConfig commits a tracked .sdlc-v2/config.toml into the fixture
// repository and returns its repo-relative path.
func trackDataDirConfig(t *testing.T, dir string) string {
	t.Helper()
	cfg := filepath.Join(paths.DataDir, "config.toml")
	writeRepoFile(t, dir, cfg, "a = 1\n")
	runGit(t, dir, "add", cfg)
	runGit(t, dir, "commit", "-m", "track config")
	return cfg
}

// TestCommitApply_Next pins the two Next outcomes: the exact confirmation when
// the commit holds every change, and the git add plus retry instruction naming
// the untracked paths that were left out.
func TestCommitApply_Next(t *testing.T) {
	t.Run("nothing left out", func(t *testing.T) {
		dir := newCommitApplyRepo(t)
		writeRepoFile(t, dir, "initial.txt", "changed")

		out := applyCommit(t, dir)

		want := "Commit created and nothing was left out. Report the sha above as the committed change."
		if out.Next != want {
			t.Errorf("Next = %q, want %q", out.Next, want)
		}
	})

	t.Run("untracked path left out", func(t *testing.T) {
		dir := newCommitApplyRepo(t)
		writeRepoFile(t, dir, "initial.txt", "changed")
		writeRepoFile(t, dir, "stray.txt", "stray")

		out := applyCommit(t, dir)

		for _, want := range []string{"stray.txt", "git add", "call commit_apply again", "Do not report them as committed"} {
			if !strings.Contains(out.Next, want) {
				t.Errorf("Next should contain %q, got %q", want, out.Next)
			}
		}
		if strings.Contains(out.Next, "nothing was left out") {
			t.Errorf("Next must not claim nothing was left out, got %q", out.Next)
		}
		// The instruction belongs in Next, not in the descriptive Summary.
		if strings.Contains(out.Summary, "git add") {
			t.Errorf("Summary should stay descriptive, got %q", out.Summary)
		}
	})
}

// TestCommitApply_OnlyTrackedDataDirChange_ErrorNamesTrackedPath pins the
// nothing-to-commit error when the sole working-tree change is a tracked file
// under .sdlc-v2/: the error must name that path instead of reporting
// "(none)" everywhere.
func TestCommitApply_OnlyTrackedDataDirChange_ErrorNamesTrackedPath(t *testing.T) {
	dir := newCommitApplyRepo(t)
	cfg := trackDataDirConfig(t, dir)
	writeRepoFile(t, dir, cfg, "a = 2\n")
	headBefore := gitOutTrim(t, dir, "rev-parse", "HEAD")

	_, err := commitApply(dir, dir, CommitApplyIn{Message: "chore: nothing", SkipConfigCheck: true})
	if err == nil {
		t.Fatal("expected an error when the only change is a tracked .sdlc-v2 file")
	}
	for _, want := range []string{"nothing to commit", "tracked paths under " + paths.DataDir + "/ left out", cfg} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q, got: %s", want, err.Error())
		}
	}
	var de *mcpserver.DataError
	if !errors.As(err, &de) || strings.TrimSpace(de.Suggestion) == "" {
		t.Errorf("error should be a *mcpserver.DataError with a non-empty Suggestion, got %#v", err)
	}
	if got := gitOutTrim(t, dir, "rev-parse", "HEAD"); got != headBefore {
		t.Errorf("HEAD moved on a failed commit: %s -> %s", headBefore, got)
	}
}

// TestCommitApply_ScopeQueryFailuresAreInfraErrors pins the two read-only
// scope queries that run before the index is touched: both report an
// *mcpserver.InfraError naming the git command that failed.
func TestCommitApply_ScopeQueryFailuresAreInfraErrors(t *testing.T) {
	t.Run("git diff --name-only", func(t *testing.T) {
		// A directory that is not a git repository: the first query fails.
		dir := t.TempDir()

		_, err := commitApply(dir, dir, CommitApplyIn{Message: "chore: x", SkipConfigCheck: true})

		var ie *mcpserver.InfraError
		if !errors.As(err, &ie) {
			t.Fatalf("error should be a *mcpserver.InfraError, got %#v", err)
		}
		if !strings.Contains(ie.Msg, "git diff --name-only") {
			t.Errorf("error should name the failing command, got %q", ie.Msg)
		}
		if strings.TrimSpace(ie.Suggestion) == "" {
			t.Error("InfraError must carry a Suggestion")
		}
	})

	t.Run("git status", func(t *testing.T) {
		dir := newCommitApplyRepo(t)
		writeRepoFile(t, dir, "initial.txt", "changed")
		// git status rejects an invalid enum value for this key while
		// git diff --name-only ignores it, so only the second query fails.
		runGit(t, dir, "config", "status.showUntrackedFiles", "bogus")
		probe := exec.Command("git", "status", "--porcelain")
		probe.Dir = dir
		if probe.Run() == nil {
			t.Skip("this git accepts status.showUntrackedFiles=bogus; cannot force a status failure")
		}

		_, err := commitApply(dir, dir, CommitApplyIn{Message: "chore: x", SkipConfigCheck: true})

		var ie *mcpserver.InfraError
		if !errors.As(err, &ie) {
			t.Fatalf("error should be a *mcpserver.InfraError, got %#v", err)
		}
		if !strings.Contains(ie.Msg, "git status") {
			t.Errorf("error should name the failing command, got %q", ie.Msg)
		}
		if strings.TrimSpace(ie.Suggestion) == "" {
			t.Error("InfraError must carry a Suggestion")
		}
	})
}

// TestListPaths pins the inline path list at every boundary of
// maxListedPaths: empty, one, exactly the cap, and over the cap where the
// "and N more" tail must count the omitted entries.
func TestListPaths(t *testing.T) {
	makePaths := func(n int) []string {
		list := make([]string, n)
		for i := range list {
			list[i] = fmt.Sprintf("a%d", i)
		}
		return list
	}
	tenPaths := "a0, a1, a2, a3, a4, a5, a6, a7, a8, a9"

	tests := []struct {
		name  string
		count int
		want  string
	}{
		{"empty", 0, "(none)"},
		{"one", 1, "a0"},
		{"at the cap", 10, tenPaths},
		{"one over the cap", 11, tenPaths + ", and 1 more"},
		{"well over the cap", 15, tenPaths + ", and 5 more"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := listPaths(makePaths(tc.count)); got != tc.want {
				t.Errorf("listPaths(%d entries) = %q, want %q", tc.count, got, tc.want)
			}
		})
	}
}

// callRegisteredCommitApply calls the registered commit_apply tool over an
// in-memory MCP session and returns the rendered Markdown text.
func callRegisteredCommitApply(t *testing.T, message string) string {
	t.Helper()
	s := mcpserver.New("test", "0.0.0-test")
	RegisterCommitTools(s)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	res, err := c.CallTool(ctx, &mcp.CallToolParams{
		Name:      "commit_apply",
		Arguments: map[string]any{"message": message, "skipConfigCheck": true, "sessionID": ""},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError || len(res.Content) == 0 {
		t.Fatalf("commit_apply failed: isError=%v content=%v", res.IsError, res.Content)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	return text.Text
}

// TestCommitApply_RenderedOutputNamesSkippedPaths goes through the registered
// tool and its Markdown renderer (docs/mcp-output-contract.md): a skipped
// untracked file is listed under skippedUntrackedPaths, and an empty list
// renders as (none) instead of disappearing.
func TestCommitApply_RenderedOutputNamesSkippedPaths(t *testing.T) {
	t.Run("skipped path is listed", func(t *testing.T) {
		dir := newCommitApplyRepo(t)
		writeRepoFile(t, dir, "initial.txt", "changed")
		writeRepoFile(t, dir, "stray.txt", "stray")
		t.Chdir(dir)

		text := callRegisteredCommitApply(t, "chore: rendered")

		if head, _, _ := strings.Cut(text, "\n"); head != "# commit_apply — ok" {
			t.Errorf("first line = %q, want %q", head, "# commit_apply — ok")
		}
		for _, want := range []string{"## Summary", "skippedUntrackedPaths:", "- stray.txt", "NOT committed", "**Next:**", "git add"} {
			if !strings.Contains(text, want) {
				t.Errorf("rendered output should contain %q, got:\n%s", want, text)
			}
		}
	})

	t.Run("empty list renders as (none)", func(t *testing.T) {
		dir := newCommitApplyRepo(t)
		writeRepoFile(t, dir, "initial.txt", "changed")
		t.Chdir(dir)

		text := callRegisteredCommitApply(t, "chore: rendered")

		if !strings.Contains(text, "- skippedUntrackedPaths: (none)") {
			t.Errorf("empty skippedUntrackedPaths should render as (none), got:\n%s", text)
		}
		if !strings.Contains(text, "- skippedTrackedPaths: (none)") {
			t.Errorf("empty skippedTrackedPaths should render as (none), got:\n%s", text)
		}
	})
}

// TestCommitApply_PathNamesAreTakenLiterally verifies the pathspec is literal:
// a tracked file whose name has glob characters and a space is staged as that
// one file, and an untracked file its name would match as a glob is left alone.
func TestCommitApply_PathNamesAreTakenLiterally(t *testing.T) {
	dir := newCommitApplyRepo(t)
	writeRepoFile(t, dir, "[x] notes é.txt", "v1")
	runGit(t, dir, "add", "--", "[x] notes é.txt")
	runGit(t, dir, "commit", "-m", "add globby name")
	writeRepoFile(t, dir, "[x] notes é.txt", "v2")
	// Matches the glob "[x] notes é.txt" if the name were read as a pattern.
	writeRepoFile(t, dir, "x notes é.txt", "untracked lookalike")

	out := applyCommit(t, dir)

	if got := gitOutTrim(t, dir, "-c", "core.quotepath=false", "show", "--name-only", "--format=", "HEAD"); got != "[x] notes é.txt" {
		t.Errorf("commit should hold only the literal file, got:\n%s", got)
	}
	if len(out.SkippedUntrackedPaths) != 1 || !strings.Contains(out.SkippedUntrackedPaths[0], "x notes") {
		t.Errorf("lookalike should be reported as skipped, got %#v", out.SkippedUntrackedPaths)
	}
}

// ---------------------------------------------------------------------------
// config check (AC4) tests
// ---------------------------------------------------------------------------

// TestCommitPrepare_ConfigCheckFailsWithoutSkip verifies that with a stale
// config, commit_prepare fails unless skipConfigCheck is set.
func TestCommitPrepare_ConfigCheckFailsWithoutSkip(t *testing.T) {
	redirectTempManifests(t)
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	// Write a v4 config to trigger stale error.
	sdlcDir := filepath.Join(dir, paths.DataDir)
	if err := os.MkdirAll(sdlcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(sdlcDir, "config.json"),
		[]byte(`{"schemaVersion": 4}`),
		0644,
	); err != nil {
		t.Fatal(err)
	}

	// Without skip: should have config error.
	out, err := commitPrepare(dir, dir, CommitPrepareIn{SkipConfigCheck: false})
	if err != nil {
		t.Fatalf("commitPrepare: %v", err)
	}
	foundConfigErr := false
	for _, e := range out.Errors {
		if strings.Contains(e, "config check failed") {
			foundConfigErr = true
			break
		}
	}
	if !foundConfigErr {
		t.Errorf("expected config check error without skip, got errors: %v", out.Errors)
	}

	// With skip: no config error.
	out2, err := commitPrepare(dir, dir, CommitPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("commitPrepare with skip: %v", err)
	}
	for _, e := range out2.Errors {
		if strings.Contains(e, "config check failed") {
			t.Errorf("config check error should not appear with skip, got: %s", e)
		}
	}
}

// ---------------------------------------------------------------------------
// WIP squash detection tests
// ---------------------------------------------------------------------------

// TestCommitPrepare_WipSquashDetection verifies WIP commit detection on
// a feature branch.
func TestCommitPrepare_WipSquashDetection(t *testing.T) {
	redirectTempManifests(t)
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")

	// Create feature branch with a WIP commit.
	runGit(t, dir, "checkout", "-b", "feat/test-wip")
	if err := os.WriteFile(filepath.Join(dir, "wip.txt"), []byte("wip"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "wip(execute): work in progress")

	// Stage something for prepare.
	if err := os.WriteFile(filepath.Join(dir, "more.txt"), []byte("more"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "more.txt")

	out, err := commitPrepare(dir, dir, CommitPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("commitPrepare: %v", err)
	}

	if len(out.WipSquash.Commits) == 0 {
		t.Error("expected WIP commits to be detected")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// runGit runs a git command in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
	}
}
