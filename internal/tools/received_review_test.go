package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
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
	if len(out.AlwaysFixSeverities) != 0 {
		t.Errorf("AlwaysFixSeverities = %q, want empty", out.AlwaysFixSeverities)
	}
	if out.Next != receivedReviewNextPR {
		t.Errorf("Next = %q, want %q", out.Next, receivedReviewNextPR)
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

// TestReceivedReviewPrepare_InvalidPR pins that a negative pr fails with a
// DomainError before any git or gh command runs.
func TestReceivedReviewPrepare_InvalidPR(t *testing.T) {
	dir := t.TempDir() // not a git repo: git must not run either
	cleanup := stubGH(t, failingGH)
	defer cleanup()

	for _, pr := range []int{-1, -3} {
		_, err := receivedReviewPrepare(dir, dir, ReceivedReviewIn{PR: pr})
		var domErr *mcpserver.DomainError
		if !errors.As(err, &domErr) {
			t.Fatalf("pr %d: err = %v (%T), want *mcpserver.DomainError", pr, err, err)
		}
		const wantMsg = "pr must be a positive integer, or 0 to skip the PR overview"
		if domErr.Msg != wantMsg {
			t.Errorf("pr %d: Msg = %q, want %q", pr, domErr.Msg, wantMsg)
		}
		if !strings.Contains(domErr.Suggestion, "omit pr") {
			t.Errorf("pr %d: Suggestion = %q, want it to say omit pr", pr, domErr.Suggestion)
		}
	}
}

// TestReceivedReviewPrepare_NoPR pins the no-PR run: with pr omitted or 0
// the tool runs no git or gh command (the folder is not a git repo and gh
// fails on every call) and returns only style, alwaysFixSeverities and the
// no-PR next text.
func TestReceivedReviewPrepare_NoPR(t *testing.T) {
	for _, in := range []ReceivedReviewIn{{}, {PR: 0}} {
		dir := t.TempDir()
		cleanup := stubGH(t, failingGH)
		out, err := receivedReviewPrepare(dir, dir, in)
		cleanup()
		if err != nil {
			t.Fatalf("pr %d: unexpected error: %v", in.PR, err)
		}
		if out.PR != (receivedReviewPR{}) {
			t.Errorf("PR = %+v, want the zero value", out.PR)
		}
		if out.View != "" || out.Checks != "" {
			t.Errorf("View = %q, Checks = %q, want both empty", out.View, out.Checks)
		}
		if out.Style.Audience != "functional" {
			t.Errorf("Style.Audience = %q, want functional", out.Style.Audience)
		}
		if len(out.AlwaysFixSeverities) != 0 {
			t.Errorf("AlwaysFixSeverities = %q, want empty", out.AlwaysFixSeverities)
		}
		if len(out.Warnings) != 0 {
			t.Errorf("Warnings = %q, want none", out.Warnings)
		}
		if out.Next != receivedReviewNextNoPR {
			t.Errorf("Next = %q, want %q", out.Next, receivedReviewNextNoPR)
		}
		if !strings.Contains(out.Next, "skipped") {
			t.Errorf("Next = %q, want it to say the overview was skipped", out.Next)
		}
	}
}

// callRegisteredReceivedReviewPrepare calls the registered
// received_review_prepare tool through an in-memory MCP client, from the
// current directory, and returns the rendered Markdown text.
func callRegisteredReceivedReviewPrepare(t *testing.T, args map[string]any) string {
	t.Helper()
	s := mcpserver.New("test", "0.0.0-test")
	RegisterReceivedReviewTools(s)

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

	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "received_review_prepare", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("CallTool: no content")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	if res.IsError {
		t.Fatalf("received_review_prepare failed: %s", text.Text)
	}
	return text.Text
}

// TestReceivedReviewPrepare_RenderedAlwaysFixSeverities calls the registered
// received_review_prepare tool with no pr argument and pins the rendered
// alwaysFixSeverities field: "(none)" with no local.toml, and the stored
// values when .sdlc-v2/local.toml sets the list.
func TestReceivedReviewPrepare_RenderedAlwaysFixSeverities(t *testing.T) {
	tests := []struct {
		name    string
		project string // .sdlc-v2/local.toml; empty: no file
		want    []string
	}{
		{name: "no local file", want: []string{"- alwaysFixSeverities: (none)"}},
		{
			name:    "stored list",
			project: "[receivedReview]\nalwaysFixSeverities = [\"High\", \"info\"]\n",
			want:    []string{"alwaysFixSeverities:", "  - high", "  - info"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(config.UserConfigPathEnv, filepath.Join(t.TempDir(), "user.toml"))
			dir := t.TempDir()
			mustRun(t, dir, "git", "init")
			if tt.project != "" {
				writeFile(t, filepath.Join(dir, paths.DataDir, paths.LocalConfigFile), tt.project)
			}
			cleanup := stubGH(t, failingGH)
			defer cleanup()
			t.Chdir(dir)

			text := callRegisteredReceivedReviewPrepare(t, map[string]any{})
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Errorf("rendered text missing %q:\n%s", want, text)
				}
			}
			if !strings.Contains(text, receivedReviewNextNoPR) {
				t.Errorf("rendered text missing the no-PR next text:\n%s", text)
			}
		})
	}
}

// TestReceivedReviewPrepare_AlwaysFixSeverities drives each row of the
// receivedReview.alwaysFixSeverities outcome table through a no-PR run.
// Every case points the user-level file at its own temp path, so no case
// reads the real home folder.
func TestReceivedReviewPrepare_AlwaysFixSeverities(t *testing.T) {
	const key = "Local receivedReview.alwaysFixSeverities"
	const fallback = "using [] (every will-fix finding asks)."
	tests := []struct {
		name         string
		user         string // user-level local.toml; empty: no file
		project      string // .sdlc-v2/local.toml; empty: no file
		want         []string
		wantWarnings []string
	}{
		{name: "no local file", want: []string{}},
		{name: "no receivedReview section", project: "[review]\nscope = \"all\"\n", want: []string{}},
		{
			// config.ReadSection skips a non-table receivedReview value, so
			// the tool sees no section: an empty list and no warning.
			name:    "receivedReview not a table",
			project: "receivedReview = \"high\"\n",
			want:    []string{},
		},
		{name: "section with no key", project: "[receivedReview]\n", want: []string{}},
		{name: "empty list", project: "[receivedReview]\nalwaysFixSeverities = []\n", want: []string{}},
		{name: "valid list", project: "[receivedReview]\nalwaysFixSeverities = [\"high\", \"info\"]\n", want: []string{"high", "info"}},
		{name: "mixed case", project: "[receivedReview]\nalwaysFixSeverities = [\"High\"]\n", want: []string{"high"}},
		{
			name:         "duplicate",
			project:      "[receivedReview]\nalwaysFixSeverities = [\"high\", \"high\"]\n",
			want:         []string{"high"},
			wantWarnings: []string{key + ` lists "high" more than once; kept once.`},
		},
		{
			name:         "duplicate after lowercasing",
			project:      "[receivedReview]\nalwaysFixSeverities = [\"High\", \"high\"]\n",
			want:         []string{"high"},
			wantWarnings: []string{key + ` lists "high" more than once; kept once.`},
		},
		{
			name:         "string not list",
			project:      "[receivedReview]\nalwaysFixSeverities = \"high\"\n",
			want:         []string{},
			wantWarnings: []string{key + ` has wrong type string; expected a list of strings such as ["high", "critical"]; ` + fallback},
		},
		{
			// TOML decoding turns every integer into float64 (fsx.DecodeTOML).
			name:         "non-string item",
			project:      "[receivedReview]\nalwaysFixSeverities = [\"high\", 3]\n",
			want:         []string{},
			wantWarnings: []string{key + ` item 3 has wrong type float64; expected a string such as "high"; ` + fallback},
		},
		{
			name:         "unknown item",
			project:      "[receivedReview]\nalwaysFixSeverities = [\"urgent\"]\n",
			want:         []string{},
			wantWarnings: []string{key + ` "urgent" is not in allowed values [critical high medium low info]; ` + fallback},
		},
		{
			// The warning names the value as the user wrote it.
			name:         "unknown item keeps original case",
			project:      "[receivedReview]\nalwaysFixSeverities = [\"Urgent\"]\n",
			want:         []string{},
			wantWarnings: []string{key + ` "Urgent" is not in allowed values [critical high medium low info]; ` + fallback},
		},
		{
			// An unknown item drops the whole list, so the duplicate warning
			// is not reported: only the one unknown-item warning remains.
			name:         "duplicate then unknown gives only the unknown-item warning",
			project:      "[receivedReview]\nalwaysFixSeverities = [\"high\", \"high\", \"urgent\"]\n",
			want:         []string{},
			wantWarnings: []string{key + ` "urgent" is not in allowed values [critical high medium low info]; ` + fallback},
		},
		{name: "user-level file only", user: "[receivedReview]\nalwaysFixSeverities = [\"medium\"]\n", want: []string{"medium"}},
		{
			name:    "project file wins",
			user:    "[receivedReview]\nalwaysFixSeverities = [\"high\"]\n",
			project: "[receivedReview]\nalwaysFixSeverities = [\"low\"]\n",
			want:    []string{"low"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userPath := filepath.Join(t.TempDir(), "user.toml")
			t.Setenv(config.UserConfigPathEnv, userPath)
			if tt.user != "" {
				writeFile(t, userPath, tt.user)
			}
			dir := t.TempDir()
			if tt.project != "" {
				writeFile(t, filepath.Join(dir, paths.DataDir, paths.LocalConfigFile), tt.project)
			}
			cleanup := stubGH(t, failingGH)
			defer cleanup()

			out, err := receivedReviewPrepare(dir, dir, ReceivedReviewIn{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !sameStrings(out.AlwaysFixSeverities, tt.want) {
				t.Errorf("AlwaysFixSeverities = %q, want %q", out.AlwaysFixSeverities, tt.want)
			}
			if !sameStrings(out.Warnings, tt.wantWarnings) {
				t.Errorf("Warnings = %q, want %q", out.Warnings, tt.wantWarnings)
			}
		})
	}
}

// TestReceivedReviewPrepare_SeverityReadError pins that a local file that
// cannot be parsed fails with an InfraError, for a no-PR run and for a PR
// run, before any git or gh command runs. The message names the file.
func TestReceivedReviewPrepare_SeverityReadError(t *testing.T) {
	tests := []struct {
		name      string
		userLevel bool // true: malformed user-level file; false: malformed .sdlc-v2/local.toml
		pr        int
	}{
		{name: "project file no PR", pr: 0},
		{name: "project file PR", pr: 42},
		{name: "user-level file no PR", userLevel: true, pr: 0},
		{name: "user-level file PR", userLevel: true, pr: 42},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userPath := filepath.Join(t.TempDir(), "user.toml")
			t.Setenv(config.UserConfigPathEnv, userPath)
			dir := setupGitRepoWithRemote(t, "https://github.com/owner/repo.git")
			badPath := filepath.Join(dir, paths.DataDir, paths.LocalConfigFile)
			if tt.userLevel {
				badPath = userPath
			}
			writeFile(t, badPath, "[receivedReview\nalwaysFixSeverities = [\"high\"\n")
			cleanup := stubGH(t, failingGH)
			defer cleanup()

			_, err := receivedReviewPrepare(dir, dir, ReceivedReviewIn{PR: tt.pr})
			var infra *mcpserver.InfraError
			if !errors.As(err, &infra) {
				t.Fatalf("err = %v (%T), want *mcpserver.InfraError", err, err)
			}
			if !strings.HasPrefix(infra.Msg, "read [receivedReview] section of "+config.LocalFilesLabel+": ") {
				t.Errorf("Msg = %q, want the read [receivedReview] prefix", infra.Msg)
			}
			if !strings.Contains(infra.Msg, badPath) {
				t.Errorf("Msg = %q, want it to name %s", infra.Msg, badPath)
			}
			if infra.Suggestion != receivedReviewReadSuggestion {
				t.Errorf("Suggestion = %q, want %q", infra.Suggestion, receivedReviewReadSuggestion)
			}
		})
	}
}

// sameStrings reports whether a and b hold the same items in the same order.
// A nil and an empty slice are equal.
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestReceivedReviewPrepare_SameReadErrorAsReviewPrepare pins that
// received_review_prepare and review_prepare fail the same way on a
// .sdlc-v2/local.toml they cannot read: both return an InfraError whose
// message names the file.
func TestReceivedReviewPrepare_SameReadErrorAsReviewPrepare(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, localPath string)
	}{
		{name: "malformed TOML", setup: func(t *testing.T, localPath string) {
			writeFile(t, localPath, "[receivedReview\n")
		}},
		{name: "directory at the path", setup: func(t *testing.T, localPath string) {
			if err := os.MkdirAll(localPath, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", localPath, err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			localPath := filepath.Join(dir, paths.DataDir, paths.LocalConfigFile)
			tt.setup(t, localPath)
			cleanup := stubGH(t, failingGH)
			defer cleanup()

			_, rrErr := receivedReviewPrepare(dir, dir, ReceivedReviewIn{})
			_, rErr := reviewPrepare(dir, dir, ReviewPrepareIn{})
			for name, err := range map[string]error{"received_review_prepare": rrErr, "review_prepare": rErr} {
				var infra *mcpserver.InfraError
				if !errors.As(err, &infra) {
					t.Errorf("%s: err = %v (%T), want *mcpserver.InfraError", name, err, err)
					continue
				}
				if !strings.Contains(infra.Msg, localPath) {
					t.Errorf("%s: Msg = %q, want it to name %s", name, infra.Msg, localPath)
				}
			}
			// The received_review_prepare Suggestion must be true for bad
			// TOML and for a directory at the path.
			var infra *mcpserver.InfraError
			if errors.As(rrErr, &infra) && infra.Suggestion != receivedReviewReadSuggestion {
				t.Errorf("received_review_prepare: Suggestion = %q, want %q", infra.Suggestion, receivedReviewReadSuggestion)
			}
		})
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
