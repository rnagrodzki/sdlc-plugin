package tools

import (
	"os/exec"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
)

// ---------------------------------------------------------------------------
// classifyCommentThreads — pure-function unit tests (no gh, no git)
// ---------------------------------------------------------------------------

// TestClassifyCommentThreads_ThreeWayClassification exercises the core
// classification algorithm directly against mock comment data covering all
// three statuses in one PR, plus a root comment authored by the PR author
// (which must be excluded from the result entirely).
func TestClassifyCommentThreads_ThreeWayClassification(t *testing.T) {
	const login = "octocat"

	comments := []ghx.PRReviewComment{
		// Root #1: no replies at all -> outstanding.
		{ID: 1, Path: "a.go", Line: 5, Login: "reviewer1", Body: "fix this", InReplyToID: 0},

		// Root #2: only the original reviewer replied to themselves -> self-replied.
		{ID: 2, Path: "b.go", Line: 9, Login: "reviewer2", Body: "consider X", InReplyToID: 0},
		{ID: 3, Path: "b.go", Line: 9, Login: "reviewer2", Body: "actually nvm", InReplyToID: 2},

		// Root #4: the PR author replied -> replied.
		{ID: 4, Path: "c.go", Line: 1, Login: "reviewer3", Body: "please rename", InReplyToID: 0},
		{ID: 5, Path: "c.go", Line: 1, Login: login, Body: "done", InReplyToID: 4},

		// Root #6: authored by the PR author themselves -> excluded entirely.
		{ID: 6, Path: "d.go", Line: 2, Login: login, Body: "note to self", InReplyToID: 0},
	}

	threads := classifyCommentThreads(comments, login)

	if len(threads) != 3 {
		t.Fatalf("got %d threads, want 3 (root #6 must be excluded): %+v", len(threads), threads)
	}

	byID := make(map[int]CommentThread, len(threads))
	for _, th := range threads {
		byID[th.ID] = th
	}

	if _, excluded := byID[6]; excluded {
		t.Errorf("thread for root comment authored by login (id 6) must be excluded, got %+v", byID[6])
	}

	outstanding, ok := byID[1]
	if !ok {
		t.Fatal("missing thread for root id 1")
	}
	if outstanding.Status != "outstanding" || outstanding.ReplyCount != 0 {
		t.Errorf("root 1: got status=%q replyCount=%d, want status=outstanding replyCount=0", outstanding.Status, outstanding.ReplyCount)
	}
	if outstanding.Reviewer != "reviewer1" || outstanding.Path != "a.go" || outstanding.Line != 5 || outstanding.Body != "fix this" {
		t.Errorf("root 1: unexpected fields: %+v", outstanding)
	}

	selfReplied, ok := byID[2]
	if !ok {
		t.Fatal("missing thread for root id 2")
	}
	if selfReplied.Status != "self-replied" || selfReplied.ReplyCount != 1 {
		t.Errorf("root 2: got status=%q replyCount=%d, want status=self-replied replyCount=1", selfReplied.Status, selfReplied.ReplyCount)
	}

	replied, ok := byID[4]
	if !ok {
		t.Fatal("missing thread for root id 4")
	}
	if replied.Status != "replied" || replied.ReplyCount != 1 {
		t.Errorf("root 4: got status=%q replyCount=%d, want status=replied replyCount=1", replied.Status, replied.ReplyCount)
	}
}

// TestClassifyCommentThreads_Empty confirms an empty comment list yields no
// threads without panicking.
func TestClassifyCommentThreads_Empty(t *testing.T) {
	threads := classifyCommentThreads(nil, "octocat")
	if len(threads) != 0 {
		t.Errorf("got %d threads, want 0", len(threads))
	}
}

// TestClassifyCommentThreads_ReplyFromThirdParty confirms that a reply from
// neither the original reviewer nor the PR author still yields
// "self-replied" (no reply *from the author* is what distinguishes
// "replied"), not a fourth, undefined status.
func TestClassifyCommentThreads_ReplyFromThirdParty(t *testing.T) {
	comments := []ghx.PRReviewComment{
		{ID: 1, Login: "reviewer1", Body: "fix this", InReplyToID: 0},
		{ID: 2, Login: "bystander", Body: "same here", InReplyToID: 1},
	}
	threads := classifyCommentThreads(comments, "octocat")
	if len(threads) != 1 {
		t.Fatalf("got %d threads, want 1", len(threads))
	}
	if threads[0].Status != "self-replied" {
		t.Errorf("got status %q, want self-replied", threads[0].Status)
	}
}

// ---------------------------------------------------------------------------
// receivedReviewVerify — validation and end-to-end wiring
// ---------------------------------------------------------------------------

func TestReceivedReviewVerify_InvalidPR(t *testing.T) {
	_, err := receivedReviewVerify(t.TempDir(), t.TempDir(), ReceivedReviewVerifyIn{PR: 0})
	if err == nil {
		t.Fatal("expected error for invalid PR number")
	}
}

// setupGitRepoWithRemote creates a real git repo (needed because
// receivedReviewVerify shells out to `git remote get-url origin` directly,
// not through ghx) with the given origin remote URL, and returns its path.
func setupGitRepoWithRemote(t *testing.T, remoteURL string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("remote", "add", "origin", remoteURL)
	return dir
}

// TestReceivedReviewVerify_EndToEnd drives the full function (git remote
// detection, default-login resolution via gh, single batched comment fetch,
// and three-way classification) against a stubbed gh binary and a real
// throwaway git repo, mirroring the fixture from
// TestClassifyCommentThreads_ThreeWayClassification.
func TestReceivedReviewVerify_EndToEnd(t *testing.T) {
	dir := setupGitRepoWithRemote(t, "https://github.com/owner/repo.git")

	script := `#!/bin/sh
case "$2" in
  user)
    echo "octocat"
    ;;
  *)
    printf '{"id":1,"path":"a.go","line":5,"login":"reviewer1","body":"fix this","in_reply_to_id":null}\n'
    printf '{"id":2,"path":"b.go","line":9,"login":"reviewer2","body":"consider X","in_reply_to_id":null}\n'
    printf '{"id":3,"path":"b.go","line":9,"login":"reviewer2","body":"actually nvm","in_reply_to_id":2}\n'
    printf '{"id":4,"path":"c.go","line":1,"login":"reviewer3","body":"please rename","in_reply_to_id":null}\n'
    printf '{"id":5,"path":"c.go","line":1,"login":"octocat","body":"done","in_reply_to_id":4}\n'
    printf '{"id":6,"path":"d.go","line":2,"login":"octocat","body":"note to self","in_reply_to_id":null}\n'
    ;;
esac
`
	cleanup := stubGH(t, script)
	defer cleanup()

	out, err := receivedReviewVerify(dir, dir, ReceivedReviewVerifyIn{PR: 42})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out.PR.Owner != "owner" || out.PR.Repo != "repo" || out.PR.Number != 42 {
		t.Errorf("unexpected PR metadata: %+v", out.PR)
	}
	if out.Total != 3 {
		t.Errorf("got Total=%d, want 3 (root #6, authored by the resolved login, must be excluded)", out.Total)
	}
	if out.Outstanding != 2 {
		t.Errorf("got Outstanding=%d, want 2 (outstanding + self-replied)", out.Outstanding)
	}
	if out.Replied != 1 {
		t.Errorf("got Replied=%d, want 1", out.Replied)
	}
	if out.Outstanding+out.Replied != out.Total {
		t.Errorf("Outstanding+Replied (%d) != Total (%d)", out.Outstanding+out.Replied, out.Total)
	}
}

// TestReceivedReviewVerify_ExplicitLogin confirms an explicitly supplied
// Login is used as-is, without shelling out to `gh api user`.
func TestReceivedReviewVerify_ExplicitLogin(t *testing.T) {
	dir := setupGitRepoWithRemote(t, "git@github.com:owner/repo.git")

	// If receivedReviewVerify ever called `gh api user` despite Login being
	// set, this stub would answer with a login that does not match any
	// comment author, which would silently change the classification below
	// and fail the assertions — a use as a canary rather than a hard error.
	script := `#!/bin/sh
case "$2" in
  user)
    echo "wrong-login"
    ;;
  *)
    printf '{"id":1,"path":"a.go","line":1,"login":"reviewer1","body":"fix this","in_reply_to_id":null}\n'
    printf '{"id":2,"path":"a.go","line":1,"login":"author","body":"done","in_reply_to_id":1}\n'
    ;;
esac
`
	cleanup := stubGH(t, script)
	defer cleanup()

	out, err := receivedReviewVerify(dir, dir, ReceivedReviewVerifyIn{PR: 7, Login: "author"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Total != 1 || out.Replied != 1 || out.Outstanding != 0 {
		t.Errorf("unexpected classification with explicit login: Total=%d Replied=%d Outstanding=%d", out.Total, out.Replied, out.Outstanding)
	}
}
