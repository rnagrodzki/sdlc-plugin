package tools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// commit_prepare tests
// ---------------------------------------------------------------------------

// TestCommitPrepare_KeySet verifies that CommitPrepareOut marshals exactly
// the top-level and nested key sets expected by the commit orchestrator.
func TestCommitPrepare_KeySet(t *testing.T) {
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
		"lastCommitMessage", "wipSquash", "branchGuard",
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
}

// TestCommitPrepare_NilSlicesSerializeAsArrays verifies that all slice
// fields serialize as JSON arrays (not null).
func TestCommitPrepare_NilSlicesSerializeAsArrays(t *testing.T) {
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

	// Create a new file to commit.
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}

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
// version_apply tests
// ---------------------------------------------------------------------------

// TestVersionApply_BumpMinor verifies version_apply bumps a package.json
// version and creates CHANGELOG.md.
func TestVersionApply_BumpMinor(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)

	// Write package.json with version 1.0.0.
	pkg := `{"name": "test", "version": "1.0.0"}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommit(t, dir, "initial")

	out, err := versionApply(dir, VersionApplyIn{
		Level:           "minor",
		Notes:           "## Changes\n- Added feature X",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("versionApply: %v", err)
	}

	if out.PreviousVersion != "1.0.0" {
		t.Errorf("expected previous 1.0.0, got %s", out.PreviousVersion)
	}
	if out.NewVersion != "1.1.0" {
		t.Errorf("expected new 1.1.0, got %s", out.NewVersion)
	}
	if !out.Changed {
		t.Error("expected Changed=true")
	}
	if out.VersionFile == "" {
		t.Error("expected non-empty VersionFile")
	}
	if out.ChangelogFile == "" {
		t.Error("expected non-empty ChangelogFile")
	}

	// Verify CHANGELOG.md exists and contains the version.
	cl, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("read CHANGELOG.md: %v", err)
	}
	if !strings.Contains(string(cl), "1.1.0") {
		t.Error("CHANGELOG.md does not contain version 1.1.0")
	}
}

// TestVersionApply_Idempotent verifies that a second Apply with the same
// explicit version is a no-op.
func TestVersionApply_Idempotent(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)

	pkg := `{"name": "test", "version": "1.0.0"}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommit(t, dir, "initial")

	// First apply.
	out1, err := versionApply(dir, VersionApplyIn{
		Level:           "minor",
		Notes:           "release notes",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("first versionApply: %v", err)
	}
	if !out1.Changed {
		t.Fatal("first call should have Changed=true")
	}

	// Second apply with same explicit version.
	out2, err := versionApply(dir, VersionApplyIn{
		Level:           "1.1.0",
		Notes:           "release notes",
		SkipConfigCheck: true,
	})
	if err != nil {
		t.Fatalf("second versionApply: %v", err)
	}
	if out2.Changed {
		t.Error("second call should have Changed=false (idempotent)")
	}
}

// ---------------------------------------------------------------------------
// config check (AC4) tests
// ---------------------------------------------------------------------------

// TestCommitPrepare_ConfigCheckFailsWithoutSkip verifies that with a stale
// config, commit_prepare fails unless skipConfigCheck is set.
func TestCommitPrepare_ConfigCheckFailsWithoutSkip(t *testing.T) {
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
// version_prepare tests
// ---------------------------------------------------------------------------

// TestVersionPrepare_Basic verifies version_prepare returns expected
// structure with a version file present.
func TestVersionPrepare_Basic(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)

	pkg := `{"name": "test", "version": "2.3.4"}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommit(t, dir, "initial")
	gitTag(t, dir, "v2.3.4")
	gitCommit(t, dir, "feat: new feature")

	out, err := versionPrepare(dir, dir, VersionPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("versionPrepare: %v", err)
	}

	if out.Flow != "release" {
		t.Errorf("expected flow 'release', got %q", out.Flow)
	}
	if out.VersionSource == nil {
		t.Fatal("expected non-nil VersionSource")
	}
	if out.VersionSource.Version != "2.3.4" {
		t.Errorf("expected version 2.3.4, got %s", out.VersionSource.Version)
	}
	if len(out.BumpOptions) != 3 {
		t.Errorf("expected 3 bump options, got %d", len(out.BumpOptions))
	}
	if len(out.CommitsSinceTag) == 0 {
		t.Error("expected commits since tag")
	}
	if out.ConventionalSummary == nil {
		t.Fatal("expected non-nil ConventionalSummary")
	}
	if out.ConventionalSummary.Suggest != "minor" {
		t.Errorf("expected suggested bump 'minor' (feat commit), got %q", out.ConventionalSummary.Suggest)
	}
}

// ---------------------------------------------------------------------------
// WIP squash detection tests
// ---------------------------------------------------------------------------

// TestCommitPrepare_WipSquashDetection verifies WIP commit detection on
// a feature branch.
func TestCommitPrepare_WipSquashDetection(t *testing.T) {
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
