package ghx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// TestPRChecksWithExitCode_PreservesStdoutOnFailure checks the fix for the
// bug where every failed/pending `gh pr checks` result (non-zero exit) was
// misclassified as a generic infra error because the former PRChecks (via
// execx.Run) discarded stdout on any non-zero exit. PRChecksWithExitCode must return
// the check output alongside the real exit code instead.
func TestPRChecksWithExitCode_PreservesStdoutOnFailure(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nprintf 'lint\\tfail\\t30s\\thttps://x\\n'\nexit 1\n")
	defer cleanup()

	out, _, exitCode, err := PRChecksWithExitCode(".", 7)
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

// ── PRForBranch failure classification ──────────────────────────────────

// TestPRForBranch_NoPRLeavesErrorMessageEmpty pins that gh's plain "no PR
// for this branch" answer is not reported as a failure.
func TestPRForBranch_NoPRLeavesErrorMessageEmpty(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho 'no pull requests found for branch \"feature\"' >&2\nexit 1\n")
	defer cleanup()

	meta := PRForBranch(".")
	if meta.Exists {
		t.Error("Exists = true, want false")
	}
	if meta.ErrorMessage != "" {
		t.Errorf("ErrorMessage = %q, want empty for a branch without a PR", meta.ErrorMessage)
	}
}

// TestPRForBranch_GHFailureSetsErrorMessage pins that any other gh failure
// sets ErrorMessage, so callers can tell "no PR" from "could not check".
func TestPRForBranch_GHFailureSetsErrorMessage(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho 'HTTP 401: Bad credentials' >&2\nexit 1\n")
	defer cleanup()

	meta := PRForBranch(".")
	if meta.Exists {
		t.Error("Exists = true, want false")
	}
	if !strings.Contains(meta.ErrorMessage, "Bad credentials") {
		t.Errorf("ErrorMessage = %q, want gh's error text", meta.ErrorMessage)
	}
}

// TestPRForBranch_MissingGHSetsErrorMessage pins that a missing gh binary
// sets ErrorMessage instead of looking like "no PR".
func TestPRForBranch_MissingGHSetsErrorMessage(t *testing.T) {
	emptyDir := t.TempDir()
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", emptyDir)
	defer os.Setenv("PATH", origPath)

	meta := PRForBranch(".")
	if meta.Exists {
		t.Error("Exists = true, want false")
	}
	if meta.ErrorMessage == "" {
		t.Error("ErrorMessage is empty, want a gh-not-found message")
	}
}

// TestPRForBranch_MalformedJSONSetsErrorMessage pins that unparseable gh
// output is reported as a failure.
func TestPRForBranch_MalformedJSONSetsErrorMessage(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho 'not json'\n")
	defer cleanup()

	meta := PRForBranch(".")
	if meta.Exists {
		t.Error("Exists = true, want false")
	}
	if meta.ErrorMessage == "" {
		t.Error("ErrorMessage is empty, want a parse error")
	}
}

func TestPRReviewComments_EmptyPR(t *testing.T) {
	// `gh api ... --paginate --jq '...'` prints nothing when the comments
	// list is empty (no matches for the jq filter, not even "[]").
	cleanup := stubGH(t, "#!/bin/sh\n")
	defer cleanup()

	comments, err := PRReviewComments(".", "owner", "repo", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("got %d comments, want 0", len(comments))
	}
}

func TestPRReviewComments_SingleBatchedCall(t *testing.T) {
	// The stub records how many times it was invoked; PRReviewComments must
	// call gh exactly once regardless of how many comments/replies come
	// back — no N+1 per-comment follow-up calls.
	dir := t.TempDir()
	countFile := filepath.Join(dir, "calls")
	script := fmt.Sprintf(`#!/bin/sh
echo x >> %q
printf '{"id":1,"path":"a.go","line":10,"login":"reviewer","body":"root comment","in_reply_to_id":null}\n'
printf '{"id":2,"path":"a.go","line":10,"login":"author","body":"reply","in_reply_to_id":1}\n'
`, countFile)
	cleanup := stubGH(t, script)
	defer cleanup()

	comments, err := PRReviewComments(".", "owner", "repo", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("got %d comments, want 2", len(comments))
	}

	data, readErr := os.ReadFile(countFile)
	if readErr != nil {
		t.Fatalf("reading call-count file: %v", readErr)
	}
	calls := len(strings.Split(strings.TrimSpace(string(data)), "\n"))
	if calls != 1 {
		t.Errorf("gh invoked %d times, want exactly 1 (single batched call)", calls)
	}

	root := comments[0]
	if root.ID != 1 || root.Path != "a.go" || root.Line != 10 || root.Login != "reviewer" || root.InReplyToID != 0 {
		t.Errorf("unexpected root comment: %+v", root)
	}
	reply := comments[1]
	if reply.ID != 2 || reply.Login != "author" || reply.InReplyToID != 1 {
		t.Errorf("unexpected reply comment: %+v", reply)
	}
}

func TestPRReviewComments_InvalidPRNumber(t *testing.T) {
	_, err := PRReviewComments(".", "owner", "repo", 0)
	if err == nil {
		t.Fatal("expected error for invalid PR number")
	}
}

func TestPRReviewComments_InvalidOwner(t *testing.T) {
	_, err := PRReviewComments(".", "-invalid", "repo", 1)
	if err == nil {
		t.Fatal("expected error for invalid owner")
	}
}

// TestPRReviewComments_GHCommandError mirrors TestGHCommandError's pattern
// for PRView: a failing `gh api ... --paginate` invocation must surface as
// an error, not be silently swallowed into an empty result.
func TestPRReviewComments_GHCommandError(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nexit 1\n")
	defer cleanup()

	_, err := PRReviewComments(".", "owner", "repo", 42)
	if err == nil {
		t.Fatal("expected error from failing gh command")
	}
}

// TestPRReviewComments_MalformedJSON confirms a non-EOF JSON decode failure
// on the streamed --jq output (e.g. a truncated or corrupted object) is
// surfaced as an error rather than silently dropped.
func TestPRReviewComments_MalformedJSON(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nprintf '{\"id\":1,\"path\":\"a.go\"' \n")
	defer cleanup()

	_, err := PRReviewComments(".", "owner", "repo", 42)
	if err == nil {
		t.Fatal("expected error for malformed JSON output")
	}
}

// ── PRReviews ────────────────────────────────────────────────────────────

func TestPRReviews(t *testing.T) {
	script := "#!/bin/sh\n" +
		"[ \"$*\" = \"pr view 42 --json reviews\" ] || { echo \"unexpected gh args: $*\" >&2; exit 3; }\n" +
		"printf '%s\\n' '{\"reviews\":[" +
		"{\"author\":{\"login\":\"alice\"},\"state\":\"COMMENTED\",\"submittedAt\":\"2026-01-01T10:00:00Z\"}," +
		"{\"author\":{\"login\":\"copilot-pull-request-reviewer\"},\"state\":\"APPROVED\",\"submittedAt\":\"2026-01-01T11:00:00Z\"}" +
		"]}'\n"
	cleanup := stubGH(t, script)
	defer cleanup()

	reviews, err := PRReviews(".", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reviews) != 2 {
		t.Fatalf("got %d reviews, want 2", len(reviews))
	}
	if reviews[0].Login != "alice" || reviews[0].State != "COMMENTED" || reviews[0].SubmittedAt != "2026-01-01T10:00:00Z" {
		t.Errorf("unexpected first review: %+v", reviews[0])
	}
	if reviews[1].Login != "copilot-pull-request-reviewer" || reviews[1].State != "APPROVED" || reviews[1].SubmittedAt != "2026-01-01T11:00:00Z" {
		t.Errorf("unexpected second review: %+v", reviews[1])
	}
}

func TestPRReviews_EmptyOutput(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nexit 0\n")
	defer cleanup()

	reviews, err := PRReviews(".", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reviews == nil {
		t.Fatal("expected non-nil empty slice for empty gh output")
	}
	if len(reviews) != 0 {
		t.Errorf("got %d reviews, want 0", len(reviews))
	}
}

func TestPRReviews_NoReviews(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nprintf '{\"reviews\":[]}'\n")
	defer cleanup()

	reviews, err := PRReviews(".", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reviews == nil {
		t.Fatal("expected non-nil empty slice for a PR with no reviews")
	}
	if len(reviews) != 0 {
		t.Errorf("got %d reviews, want 0", len(reviews))
	}
}

func TestPRReviews_InvalidPRNumber(t *testing.T) {
	_, err := PRReviews(".", 0)
	if err == nil {
		t.Fatal("expected error for invalid PR number")
	}
}

// TestPRReviews_GHCommandError mirrors TestGHCommandError's pattern for
// PRView: a failing `gh pr view ... --json reviews` invocation must surface
// as an error, unwrapped, rather than being swallowed into an empty result.
func TestPRReviews_GHCommandError(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nexit 1\n")
	defer cleanup()

	_, err := PRReviews(".", 42)
	if err == nil {
		t.Fatal("expected error from failing gh command")
	}
}

func TestPRReviews_MalformedJSON(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho 'not json'\n")
	defer cleanup()

	_, err := PRReviews(".", 42)
	if err == nil {
		t.Fatal("expected error for malformed JSON output")
	}
	if !strings.Contains(err.Error(), "parse gh pr view output") {
		t.Errorf("expected parse error message, got: %v", err)
	}
}

func TestCurrentLogin(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho \"octocat\"\n")
	defer cleanup()

	login, err := CurrentLogin(".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if login != "octocat" {
		t.Errorf("got %q, want %q", login, "octocat")
	}
}

func TestCurrentLogin_Empty(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho \"\"\n")
	defer cleanup()

	_, err := CurrentLogin(".")
	if err == nil {
		t.Fatal("expected error for empty login")
	}
}

// TestCurrentLogin_GHCommandError mirrors TestGHCommandError's pattern: a
// failing `gh api user` invocation (e.g. not authenticated) must surface as
// an error.
func TestCurrentLogin_GHCommandError(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nexit 1\n")
	defer cleanup()

	_, err := CurrentLogin(".")
	if err == nil {
		t.Fatal("expected error from failing gh command")
	}
}

// ── RepoAccessProbe ─────────────────────────────────────────────────────

// repoAccessStub builds a gh stub for RepoAccessProbe: `gh auth status`
// reports one logged-in account (alice); `gh api` runs apiBody, which
// mimics what real `gh api repos/o/r -i --silent` prints. On a 4xx/5xx
// response real gh prints the status line and headers to stdout, prints
// "gh: <reason> (HTTP <code>)" to stderr, and exits 1.
func repoAccessStub(apiBody string) string {
	return "#!/bin/sh\n" +
		"if [ \"$1\" = auth ]; then\n" +
		"  echo '{\"hosts\":{\"github.com\":[{\"state\":\"success\",\"login\":\"alice\",\"active\":true}]}}'\n" +
		"  exit 0\n" +
		"fi\n" +
		apiBody
}

func TestRepoAccessProbe(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	intPtr := func(i int) *int { return &i }
	cases := []struct {
		name       string
		apiBody    string
		wantAccess *bool
		wantStatus *int
		wantErrSub string
	}{
		{
			name:       "200 is accessible",
			apiBody:    "printf 'HTTP/2.0 200 OK\\nContent-Type: application/json\\n\\n'\n",
			wantAccess: boolPtr(true),
			wantStatus: intPtr(200),
		},
		{
			name:       "404 is denied",
			apiBody:    "printf 'HTTP/2.0 404 Not Found\\nContent-Type: application/json\\n\\n'\necho 'gh: Not Found (HTTP 404)' >&2\nexit 1\n",
			wantAccess: boolPtr(false),
			wantStatus: intPtr(404),
		},
		{
			name:       "403 is denied",
			apiBody:    "printf 'HTTP/2.0 403 Forbidden\\n\\n'\necho 'gh: Resource not accessible by integration (HTTP 403)' >&2\nexit 1\n",
			wantAccess: boolPtr(false),
			wantStatus: intPtr(403),
		},
		{
			name:       "404 reported on stderr only is denied",
			apiBody:    "echo 'gh: Not Found (HTTP 404)' >&2\nexit 1\n",
			wantAccess: boolPtr(false),
			wantStatus: intPtr(404),
		},
		{
			name:       "network failure is unknown",
			apiBody:    "echo 'error connecting to api.github.com' >&2\necho 'check your internet connection or https://githubstatus.com' >&2\nexit 1\n",
			wantErrSub: "error connecting to api.github.com",
		},
		{
			name:       "server error is unknown",
			apiBody:    "printf 'HTTP/2.0 502 Bad Gateway\\n\\n'\necho 'gh: Bad Gateway (HTTP 502)' >&2\nexit 1\n",
			wantStatus: intPtr(502),
			wantErrSub: "HTTP 502",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cleanup := stubGH(t, repoAccessStub(tc.apiBody))
			defer cleanup()

			got := RepoAccessProbe(".", "acme", "widgets", "")

			switch {
			case tc.wantAccess == nil && got.Accessible != nil:
				t.Errorf("Accessible: got %v, want nil (unknown)", *got.Accessible)
			case tc.wantAccess != nil && got.Accessible == nil:
				t.Errorf("Accessible: got nil, want %v (ErrorMessage %q)", *tc.wantAccess, got.ErrorMessage)
			case tc.wantAccess != nil && *got.Accessible != *tc.wantAccess:
				t.Errorf("Accessible: got %v, want %v", *got.Accessible, *tc.wantAccess)
			}
			switch {
			case tc.wantStatus == nil && got.StatusCode != nil:
				t.Errorf("StatusCode: got %d, want nil", *got.StatusCode)
			case tc.wantStatus != nil && got.StatusCode == nil:
				t.Errorf("StatusCode: got nil, want %d", *tc.wantStatus)
			case tc.wantStatus != nil && *got.StatusCode != *tc.wantStatus:
				t.Errorf("StatusCode: got %d, want %d", *got.StatusCode, *tc.wantStatus)
			}
			if !strings.Contains(got.ErrorMessage, tc.wantErrSub) {
				t.Errorf("ErrorMessage: got %q, want it to contain %q", got.ErrorMessage, tc.wantErrSub)
			}
			if len(got.SuggestedAccounts) != 1 || got.SuggestedAccounts[0] != "alice" {
				t.Errorf("SuggestedAccounts: got %v, want [alice]", got.SuggestedAccounts)
			}
		})
	}
}
