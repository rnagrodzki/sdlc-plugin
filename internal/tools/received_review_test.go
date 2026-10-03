package tools

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
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

// ---------------------------------------------------------------------------
// writeReplyBodies
// ---------------------------------------------------------------------------

func TestWriteReplyBodies_HappyPath(t *testing.T) {
	root := t.TempDir()

	out, err := writeReplyBodies(root, ReceivedReviewVerifyIn{
		WriteReplyBodies: true,
		Content:          "## Reply to thread 1\n\nDone.\n",
	})
	if err != nil {
		t.Fatalf("writeReplyBodies: %v", err)
	}
	if out.Next == "" {
		t.Error("expected Next to be populated")
	}

	outPath := filepath.Join(root, paths.DataDir, "state", "artifacts", "received-review-reply-bodies.md")
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != "## Reply to thread 1\n\nDone.\n" {
		t.Errorf("written content = %q, want the passed content verbatim", got)
	}
}

func TestWriteReplyBodies_EmptyContent_Errors(t *testing.T) {
	root := t.TempDir()

	_, err := writeReplyBodies(root, ReceivedReviewVerifyIn{WriteReplyBodies: true})
	if err == nil {
		t.Fatal("expected error when content is empty")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected *mcpserver.DomainError, got %T: %v", err, err)
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
	if out.Next == "" {
		t.Error("Next must be populated")
	}
	if !strings.Contains(out.Next, "Outstanding") {
		t.Errorf("got Next=%q, want guidance about outstanding threads (Outstanding=%d)", out.Next, out.Outstanding)
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
	if strings.Contains(out.Next, "Outstanding") {
		t.Errorf("got Next=%q, want no outstanding-threads guidance when Outstanding=0", out.Next)
	}
}

// TestReceivedReviewVerify_CurrentLoginFailure_Suggestion confirms that when
// ghx.CurrentLogin fails (no Login supplied, gh not authenticated), the
// resulting InfraError carries a recovery Suggestion rather than leaving
// callers to guess — mirroring ghx.AuthProbe's "gh auth login" guidance and
// noting the Login input field lets callers bypass this lookup entirely.
func TestReceivedReviewVerify_CurrentLoginFailure_Suggestion(t *testing.T) {
	dir := setupGitRepoWithRemote(t, "https://github.com/owner/repo.git")

	cleanup := stubGH(t, "#!/bin/sh\nexit 1\n")
	defer cleanup()

	_, err := receivedReviewVerify(dir, dir, ReceivedReviewVerifyIn{PR: 1})
	if err == nil {
		t.Fatal("expected error when gh api user fails")
	}

	var infraErr *mcpserver.InfraError
	if !errors.As(err, &infraErr) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	if infraErr.Suggestion == "" {
		t.Error("expected non-empty Suggestion for recoverable gh-auth failure")
	}
	if !strings.Contains(infraErr.Suggestion, "gh auth login") {
		t.Errorf("got Suggestion=%q, want gh auth login guidance", infraErr.Suggestion)
	}
	if !strings.Contains(infraErr.Suggestion, "login") {
		t.Errorf("got Suggestion=%q, want it to mention the login input field as a bypass", infraErr.Suggestion)
	}
}

// TestReceivedReviewPrepare_ChecksExitCodes drives received_review_prepare
// against a stubbed gh whose `gh pr checks` exits with each code gh uses.
// gh pr checks exits 1 when a check failed and 8 when one is pending, so the
// rows it printed must reach the caller: those are exactly the cases the
// received-review skill has to report. Only a non-zero exit with no rows is
// gh's own failure, and it becomes a warning while the call still succeeds.
func TestReceivedReviewPrepare_ChecksExitCodes(t *testing.T) {
	tests := []struct {
		name         string
		checks       string // body of the `checks)` case in the stub
		wantChecks   string
		wantWarnings []string // substrings, one per expected warning
	}{
		{
			name:       "failed check exit 1",
			checks:     `printf 'lint\tfail\t30s\thttps://x\n'; exit 1`,
			wantChecks: "lint\tfail\t30s\thttps://x",
		},
		{
			name:       "pending check exit 8",
			checks:     `printf 'build\tpending\t1m\thttps://x\n'; exit 8`,
			wantChecks: "build\tpending\t1m\thttps://x",
		},
		{
			name:       "all pass exit 0",
			checks:     `printf 'build\tpass\t1m\thttps://x\n'`,
			wantChecks: "build\tpass\t1m\thttps://x",
		},
		{
			name:         "gh error exit 1 no rows",
			checks:       `echo "no checks reported on the 'feat' branch" >&2; exit 1`,
			wantWarnings: []string{"gh pr checks 42: exit 1: no checks reported on the 'feat' branch"},
		},
		{
			name:         "unexpected exit code",
			checks:       `printf 'build\tpass\t1m\thttps://x\n'; echo boom >&2; exit 2`,
			wantWarnings: []string{"gh pr checks 42: exit 2: boom"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupGitRepoWithRemote(t, "https://github.com/owner/repo.git")
			script := "#!/bin/sh\ncase \"$2\" in\n  view)\n    echo 'title: T'\n    ;;\n  checks)\n    " + tt.checks + "\n    ;;\nesac\n"
			cleanup := stubGH(t, script)
			defer cleanup()

			out, err := receivedReviewPrepare(dir, dir, ReceivedReviewIn{PR: 42})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.View != "title: T" {
				t.Errorf("View = %q, want %q", out.View, "title: T")
			}
			if out.Checks != tt.wantChecks {
				t.Errorf("Checks = %q, want %q", out.Checks, tt.wantChecks)
			}
			if len(out.Warnings) != len(tt.wantWarnings) {
				t.Fatalf("Warnings = %q, want %d warning(s) matching %q", out.Warnings, len(tt.wantWarnings), tt.wantWarnings)
			}
			for i, w := range tt.wantWarnings {
				if !strings.Contains(out.Warnings[i], w) {
					t.Errorf("Warnings[%d] = %q, want it to contain %q", i, out.Warnings[i], w)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// receivedReviewPrepare — main paths
// ---------------------------------------------------------------------------

// failingGH is a fake gh that fails on every call. A test that expects an
// error raised before gh runs installs it, so a gh call would change the
// error class or message.
const failingGH = "#!/bin/sh\necho \"gh must not run: $*\" >&2\nexit 3\n"

// TestReceivedReviewPrepare_Success pins the output of a call where both gh
// commands succeed: PR identity parsed from origin, gh's text, and no
// warnings.
func TestReceivedReviewPrepare_Success(t *testing.T) {
	dir := setupGitRepoWithRemote(t, "git@github.com:owner/repo.git")
	script := "#!/bin/sh\n" +
		"[ \"$3\" = \"7\" ] || { echo \"unexpected pr: $*\" >&2; exit 3; }\n" +
		"case \"$2\" in\n" +
		"  view) printf 'title:\\tAdd widgets\\nstate:\\tOPEN\\n' ;;\n" +
		"  checks) printf 'build\\tpass\\t1m\\thttps://x\\n' ;;\n" +
		"  *) echo \"unexpected gh args: $*\" >&2; exit 3 ;;\n" +
		"esac\n"
	cleanup := stubGH(t, script)
	defer cleanup()

	out, err := receivedReviewPrepare(dir, dir, ReceivedReviewIn{PR: 7})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.PR != (receivedReviewPR{Number: 7, Owner: "owner", Repo: "repo"}) {
		t.Errorf("PR = %+v, want {7 owner repo}", out.PR)
	}
	if out.Version != 1 {
		t.Errorf("Version = %d, want 1", out.Version)
	}
	if out.PluginVersion == "" {
		t.Error("PluginVersion is empty")
	}
	if out.Timestamp == "" {
		t.Error("Timestamp is empty")
	}
	if out.View != "title:\tAdd widgets\nstate:\tOPEN" {
		t.Errorf("View = %q, want gh pr view's trimmed output", out.View)
	}
	if out.Checks != "build\tpass\t1m\thttps://x" {
		t.Errorf("Checks = %q, want gh pr checks' trimmed output", out.Checks)
	}
	if out.Warnings != nil {
		t.Errorf("Warnings = %q, want nil", out.Warnings)
	}
}

// TestReceivedReviewPrepare_Style verifies that received_review_prepare
// attaches the plugin-wide communication style, defaulting to a functional
// audience when no [style] config section is present.
func TestReceivedReviewPrepare_Style(t *testing.T) {
	dir := setupGitRepoWithRemote(t, "git@github.com:owner/repo.git")
	script := "#!/bin/sh\n" +
		"case \"$2\" in\n" +
		"  view) printf 'title:\\tAdd widgets\\nstate:\\tOPEN\\n' ;;\n" +
		"  checks) printf 'build\\tpass\\t1m\\thttps://x\\n' ;;\n" +
		"  *) echo \"unexpected gh args: $*\" >&2; exit 3 ;;\n" +
		"esac\n"
	cleanup := stubGH(t, script)
	defer cleanup()

	out, err := receivedReviewPrepare(dir, dir, ReceivedReviewIn{PR: 7})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Style.Audience != "functional" {
		t.Errorf("Style.Audience = %q, want functional", out.Style.Audience)
	}
}

// TestReceivedReviewPrepare_ConfigGate pins that a JSON-era config.json
// without config.toml fails with a DataError before any gh command runs.
func TestReceivedReviewPrepare_ConfigGate(t *testing.T) {
	dir := setupGitRepoWithRemote(t, "https://github.com/owner/repo.git")
	writeFile(t, filepath.Join(dir, paths.DataDir, "config.json"), `{"schemaVersion": 4}`)
	cleanup := stubGH(t, failingGH)
	defer cleanup()

	_, err := receivedReviewPrepare(dir, dir, ReceivedReviewIn{PR: 42})
	var dataErr *mcpserver.DataError
	if !errors.As(err, &dataErr) {
		t.Fatalf("err = %v (%T), want *mcpserver.DataError", err, err)
	}
	if !strings.HasPrefix(dataErr.Msg, "config-version:") {
		t.Errorf("Msg = %q, want it to start with config-version:", dataErr.Msg)
	}
	if !strings.Contains(dataErr.Suggestion, "/setup") {
		t.Errorf("Suggestion = %q, want it to name /setup", dataErr.Suggestion)
	}
}

// TestReceivedReviewPrepare_InvalidPR pins that pr 0 or negative fails with a
// DomainError before any git or gh command runs.
func TestReceivedReviewPrepare_InvalidPR(t *testing.T) {
	for _, pr := range []int{0, -3} {
		dir := t.TempDir() // not a git repo: git must not run either
		_, err := receivedReviewPrepare(dir, dir, ReceivedReviewIn{PR: pr})
		var domErr *mcpserver.DomainError
		if !errors.As(err, &domErr) {
			t.Fatalf("pr %d: err = %v (%T), want *mcpserver.DomainError", pr, err, err)
		}
		if domErr.Msg != "pr must be a positive integer" {
			t.Errorf("pr %d: Msg = %q, want %q", pr, domErr.Msg, "pr must be a positive integer")
		}
	}
}

// TestReceivedReviewPrepare_RemoteErrors pins the two InfraErrors raised
// while reading owner/repo from origin: no origin remote, and an origin URL
// with no owner/repo path. Neither runs gh.
func TestReceivedReviewPrepare_RemoteErrors(t *testing.T) {
	tests := []struct {
		name      string
		remoteURL string // empty: no origin remote
		wantMsg   string
	}{
		{name: "no origin remote", wantMsg: "get git remote URL:"},
		{name: "unparsable remote", remoteURL: "https://example.com/", wantMsg: "parse remote owner/repo:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dir string
			if tt.remoteURL == "" {
				dir = t.TempDir()
				mustRun(t, dir, "git", "init")
			} else {
				dir = setupGitRepoWithRemote(t, tt.remoteURL)
			}
			cleanup := stubGH(t, failingGH)
			defer cleanup()

			_, err := receivedReviewPrepare(dir, dir, ReceivedReviewIn{PR: 42})
			var infra *mcpserver.InfraError
			if !errors.As(err, &infra) {
				t.Fatalf("err = %v (%T), want *mcpserver.InfraError", err, err)
			}
			if !strings.HasPrefix(infra.Msg, tt.wantMsg) {
				t.Errorf("Msg = %q, want it to start with %q", infra.Msg, tt.wantMsg)
			}
		})
	}
}

// TestReceivedReviewPrepare_ViewFailure pins that a failing gh pr view fails
// the whole call with an InfraError naming the PR, unlike gh pr checks.
func TestReceivedReviewPrepare_ViewFailure(t *testing.T) {
	dir := setupGitRepoWithRemote(t, "https://github.com/owner/repo.git")
	script := "#!/bin/sh\n" +
		"case \"$2\" in\n" +
		"  view) echo 'GraphQL: Could not resolve to a PullRequest with the number of 9999.' >&2; exit 1 ;;\n" +
		"  *) printf 'build\\tpass\\t1m\\thttps://x\\n' ;;\n" +
		"esac\n"
	cleanup := stubGH(t, script)
	defer cleanup()

	_, err := receivedReviewPrepare(dir, dir, ReceivedReviewIn{PR: 9999})
	var infra *mcpserver.InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("err = %v (%T), want *mcpserver.InfraError", err, err)
	}
	if !strings.HasPrefix(infra.Msg, "gh pr view 9999:") {
		t.Errorf("Msg = %q, want it to start with \"gh pr view 9999:\"", infra.Msg)
	}
	if !strings.Contains(infra.Suggestion, "gh auth status") {
		t.Errorf("Suggestion = %q, want it to mention gh auth status", infra.Suggestion)
	}
}
