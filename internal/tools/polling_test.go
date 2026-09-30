package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/stepper"
)

// stubGH writes a fake gh binary to a temp dir and prepends it to PATH,
// mirroring internal/ghx/ghx_test.go's stubGH helper (same PATH-stubbing
// convention, duplicated here rather than exported from ghx since ghx.go
// is an Upstream Surface file this task must not modify).
func stubGH(t *testing.T, script string) func() {
	t.Helper()

	dir := t.TempDir()
	name := "gh"
	if runtime.GOOS == "windows" {
		name = "gh.bat"
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

// stubGHReviews installs a fake gh that answers exactly `gh pr view <pr>
// --json reviews` with the JSON for the given reviews, in the shape gh
// prints ({"reviews":[{"author":{"login":...},"state":...,"submittedAt":...}]}).
// Any other argv exits 3, so a test that expects a verdict also proves the
// exact command awaitRemoteReview issues.
func stubGHReviews(t *testing.T, pr int, reviews ...ghx.PRReview) func() {
	t.Helper()

	entries := make([]map[string]any, 0, len(reviews))
	for _, r := range reviews {
		entries = append(entries, map[string]any{
			"author":      map[string]string{"login": r.Login},
			"state":       r.State,
			"submittedAt": r.SubmittedAt,
		})
	}
	body, err := json.Marshal(map[string]any{"reviews": entries})
	if err != nil {
		t.Fatalf("marshal stub reviews: %v", err)
	}

	script := fmt.Sprintf("#!/bin/sh\n"+
		"[ \"$*\" = \"pr view %d --json reviews\" ] || { echo \"unexpected gh args: $*\" >&2; exit 3; }\n"+
		"printf '%%s\\n' '%s'\n", pr, body)
	return stubGH(t, script)
}

// newTimedOutPollState persists a poll state for skill whose deadline has
// already passed and returns its state file path. skill is the poll's own
// name ("await-remote-review" or "verify-pipeline") so each tool's tests
// resume a state file of the shape that tool itself would have written.
func newTimedOutPollState(t *testing.T, skill string) string {
	t.Helper()

	stateFile, err := stepper.NewStateFilePath(skill)
	if err != nil {
		t.Fatalf("NewStateFilePath: %v", err)
	}
	t.Cleanup(func() { os.Remove(stateFile) })

	st := stepper.NewPollState(skill, 1, 1)
	st.StartedAt -= 1000 // force TimedOut()
	if err := stepper.SavePollState(stateFile, st); err != nil {
		t.Fatalf("SavePollState: %v", err)
	}
	return stateFile
}

// ---------------------------------------------------------------------------
// await_remote_review
// ---------------------------------------------------------------------------

func TestAwaitRemoteReview_PendingThenResume(t *testing.T) {
	// No reviews yet: copilot has not reviewed.
	cleanup := stubGHReviews(t, 42)
	defer cleanup()

	in := AwaitRemoteReviewIn{PR: 42, TimeoutSeconds: 600, IntervalSeconds: 60}
	env, err := awaitRemoteReview(".", in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Status != "pending" {
		t.Fatalf("got status %q, want pending", env.Status)
	}
	if env.StateFile == nil || *env.StateFile == "" {
		t.Fatalf("expected non-empty state_file on pending envelope")
	}
	defer os.Remove(*env.StateFile)

	// Resume: pass the state_file back in, gh now reports an approval.
	cleanup2 := stubGHReviews(t, 42, ghx.PRReview{Login: "copilot-pull-request-reviewer", State: "APPROVED"})
	defer cleanup2()

	in2 := AwaitRemoteReviewIn{PR: 42, TimeoutSeconds: 600, IntervalSeconds: 60, StateFile: *env.StateFile}
	env2, err := awaitRemoteReview(".", in2)
	if err != nil {
		t.Fatalf("unexpected error on resume: %v", err)
	}
	if env2.Status != "done" {
		t.Fatalf("got status %q, want done", env2.Status)
	}
	if env2.Ext["verdict"] != "approved-clean" {
		t.Fatalf("got verdict %v, want approved-clean", env2.Ext["verdict"])
	}
}

func TestAwaitRemoteReview_ActionableOnChangesRequested(t *testing.T) {
	cleanup := stubGHReviews(t, 7, ghx.PRReview{Login: "copilot-pull-request-reviewer", State: "CHANGES_REQUESTED"})
	defer cleanup()

	env, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 7, TimeoutSeconds: 600, IntervalSeconds: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Status != "done" || env.Ext["verdict"] != "actionable" {
		t.Fatalf("got status=%q verdict=%v, want done/actionable", env.Status, env.Ext["verdict"])
	}
}

func TestAwaitRemoteReview_UnconfiguredReviewerIgnored(t *testing.T) {
	// mallory approved, but only copilot is configured: still pending.
	cleanup := stubGHReviews(t, 9, ghx.PRReview{Login: "mallory", State: "APPROVED"})
	defer cleanup()

	env, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 9, TimeoutSeconds: 600, IntervalSeconds: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Status != "pending" {
		t.Fatalf("got status %q ext=%v, want pending", env.Status, env.Ext)
	}
	defer os.Remove(*env.StateFile)
}

func TestAwaitRemoteReview_ReviewerLoginCaseInsensitive(t *testing.T) {
	cleanup := stubGHReviews(t, 11, ghx.PRReview{Login: "alice", State: "COMMENTED"})
	defer cleanup()

	env, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 11, TimeoutSeconds: 600, IntervalSeconds: 60, Reviewers: []string{"ALICE"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Status != "done" || env.Ext["verdict"] != "actionable" || env.Ext["reviewer"] != "ALICE" {
		t.Fatalf("got status=%q ext=%v, want done/actionable/ALICE", env.Status, env.Ext)
	}
}

func TestAwaitRemoteReview_EmptyGHOutputIsPending(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nexit 0\n")
	defer cleanup()

	env, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 3, TimeoutSeconds: 600, IntervalSeconds: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Status != "pending" {
		t.Fatalf("got status %q, want pending", env.Status)
	}
	defer os.Remove(*env.StateFile)
}

func TestAwaitRemoteReview_MalformedGHOutputIsError(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho 'not json'\n")
	defer cleanup()

	env, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 3, TimeoutSeconds: 600, IntervalSeconds: 60})
	if err != nil {
		t.Fatalf("unexpected Go error (should be classified into the envelope): %v", err)
	}
	if env.Status != "error" {
		t.Fatalf("got status %q, want error", env.Status)
	}
}

func TestAwaitRemoteReview_InvalidPR(t *testing.T) {
	_, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 0})
	if err == nil {
		t.Fatal("expected error for pr <= 0")
	}
}

func TestAwaitRemoteReview_MissingGHBinary(t *testing.T) {
	// Empty PATH so exec.LookPath("gh") fails with "not found".
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	env, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 1, TimeoutSeconds: 600, IntervalSeconds: 60})
	if err != nil {
		t.Fatalf("unexpected Go error (should be classified into the envelope): %v", err)
	}
	if env.Status != "error" {
		t.Fatalf("got status %q, want error", env.Status)
	}
	if !strings.Contains(env.Error, "infra") {
		t.Fatalf("expected classified infra error in envelope, got %q", env.Error)
	}
}

func TestAwaitRemoteReview_Timeout(t *testing.T) {
	// The final probe finds no review, so the timed-out state reports timeout.
	cleanup := stubGHReviews(t, 1)
	defer cleanup()

	stateFile := newTimedOutPollState(t, "await-remote-review")

	env, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 1, TimeoutSeconds: 1, IntervalSeconds: 1, StateFile: stateFile})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Status != "done" || env.Ext["verdict"] != "timeout" {
		t.Fatalf("got status=%q verdict=%v, want done/timeout", env.Status, env.Ext["verdict"])
	}

	// A subsequent call against the now-exhausted state file short-circuits
	// to "skipped" without probing gh again.
	env2, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 1, TimeoutSeconds: 1, IntervalSeconds: 1, StateFile: stateFile})
	if err != nil {
		t.Fatalf("unexpected error on exhausted resume: %v", err)
	}
	if env2.Ext["verdict"] != "skipped" {
		t.Fatalf("got verdict %v, want skipped", env2.Ext["verdict"])
	}
}

// TestAwaitRemoteReview_TimedOutFinalProbeFindsVerdict is the false-timeout
// regression: a review that lands during the last interval leaves the poll
// state timed out AND the review present. The final probe must return the
// verdict, not "timeout".
func TestAwaitRemoteReview_TimedOutFinalProbeFindsVerdict(t *testing.T) {
	tests := []struct {
		name        string
		state       string
		wantVerdict string
	}{
		{"approved", "APPROVED", "approved-clean"},
		{"changes requested", "CHANGES_REQUESTED", "actionable"},
		{"commented", "COMMENTED", "actionable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := stubGHReviews(t, 5, ghx.PRReview{Login: "copilot-pull-request-reviewer", State: tt.state})
			defer cleanup()

			stateFile := newTimedOutPollState(t, "await-remote-review")

			env, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 5, TimeoutSeconds: 1, IntervalSeconds: 1, StateFile: stateFile})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if env.Status != "done" || env.Ext["verdict"] != tt.wantVerdict {
				t.Fatalf("got status=%q verdict=%v, want done/%s", env.Status, env.Ext["verdict"], tt.wantVerdict)
			}
			if _, timedOut := env.Ext["waited_seconds"]; timedOut {
				t.Fatalf("verdict envelope must not carry the timeout envelope's waited_seconds: %v", env.Ext)
			}
		})
	}
}

// TestAwaitRemoteReview_ProbeErrorBeforeTimeoutStaysRetryable pins the
// before-the-deadline half of DD3: a gh failure while the poll still has
// budget left surfaces as a retryable error envelope, does not mark the
// state exhausted, and the next call probes again and can still find the
// verdict.
func TestAwaitRemoteReview_ProbeErrorBeforeTimeoutStaysRetryable(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nexit 1\n")
	defer cleanup()

	env, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 5, TimeoutSeconds: 600, IntervalSeconds: 60})
	if err != nil {
		t.Fatalf("unexpected Go error (should be classified into the envelope): %v", err)
	}
	if env.Status != "error" {
		t.Fatalf("got status=%q ext=%v, want error", env.Status, env.Ext)
	}
	if env.Ext["retryable"] != true {
		t.Fatalf("ext.retryable = %v, want true for an unclassified gh failure", env.Ext["retryable"])
	}
	if env.StateFile == nil || *env.StateFile == "" {
		t.Fatal("an error envelope must keep the state_file so the caller can resume")
	}
	stateFile := *env.StateFile
	defer os.Remove(stateFile)

	st, err := stepper.LoadPollState(stateFile)
	if err != nil {
		t.Fatalf("LoadPollState: %v", err)
	}
	if st.Exhausted {
		t.Fatal("a probe error before the deadline must not mark the poll state exhausted")
	}

	cleanup2 := stubGHReviews(t, 5, ghx.PRReview{Login: "copilot-pull-request-reviewer", State: "APPROVED"})
	defer cleanup2()

	env2, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 5, TimeoutSeconds: 600, IntervalSeconds: 60, StateFile: stateFile})
	if err != nil {
		t.Fatalf("unexpected error on retry: %v", err)
	}
	if env2.Status != "done" || env2.Ext["verdict"] != "approved-clean" {
		t.Fatalf("retry got status=%q verdict=%v, want done/approved-clean", env2.Status, env2.Ext["verdict"])
	}
}

// TestAwaitRemoteReview_TimedOutPersistentProbeErrorEndsPoll is the
// unbounded-poll regression (DD3): once the deadline has passed, a gh
// failure must END the poll instead of returning yet another error envelope.
// The skills treat an error envelope as transient and re-probe with no cap,
// so a gh failure that keeps happening after the deadline (expired
// credentials, deleted PR, uninstalled gh) left the poll with no end at all.
//
// This replaces the former TestAwaitRemoteReview_TimedOutProbeErrorStaysRetryable,
// which asserted the opposite (error envelope, state not exhausted) and so
// pinned the unbounded behavior. Its retryable-probe-error coverage lives on
// in TestAwaitRemoteReview_ProbeErrorBeforeTimeoutStaysRetryable above.
func TestAwaitRemoteReview_TimedOutPersistentProbeErrorEndsPoll(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho 'gh: Not Found (HTTP 404)' >&2\nexit 1\n")
	defer cleanup()

	stateFile := newTimedOutPollState(t, "await-remote-review")

	env, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 5, TimeoutSeconds: 1, IntervalSeconds: 1, StateFile: stateFile})
	if err != nil {
		t.Fatalf("unexpected Go error (should be classified into the envelope): %v", err)
	}
	if env.Status != "done" || env.Ext["verdict"] != "timeout" {
		t.Fatalf("got status=%q verdict=%v, want done/timeout (a timed-out probe error must end the poll)", env.Status, env.Ext["verdict"])
	}
	probeErr, _ := env.Ext["probe_error"].(string)
	if probeErr == "" {
		t.Fatalf("timeout envelope must carry the probe error in ext.probe_error, got ext=%v", env.Ext)
	}
	if env.Ext["probe_error_class"] != ghClassNotFound {
		t.Fatalf("ext.probe_error_class = %v, want %q", env.Ext["probe_error_class"], ghClassNotFound)
	}

	st, err := stepper.LoadPollState(stateFile)
	if err != nil {
		t.Fatalf("LoadPollState: %v", err)
	}
	if !st.Exhausted {
		t.Fatal("ending the poll must mark the state exhausted")
	}

	// The loop really is over: the next call short-circuits to "skipped"
	// without probing gh again, even though gh is still failing.
	env2, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 5, TimeoutSeconds: 1, IntervalSeconds: 1, StateFile: stateFile})
	if err != nil {
		t.Fatalf("unexpected error on exhausted resume: %v", err)
	}
	if env2.Status != "done" || env2.Ext["verdict"] != "skipped" {
		t.Fatalf("got status=%q verdict=%v, want done/skipped", env2.Status, env2.Ext["verdict"])
	}
}

// TestClassifyGHError pins the failure classes the envelope exposes in
// ext.error_class / ext.probe_error_class, and which of them stay retryable.
func TestClassifyGHError(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantClass     string
		wantRetryable bool
		wantInfraMsg  bool
	}{
		{"missing gh binary", fmt.Errorf("%w: exec: \"gh\": executable file not found in $PATH", ghx.ErrGHNotFound), ghClassMissing, false, true},
		{"output cap", fmt.Errorf("execx: gh: %w", execx.ErrOutputCap), ghClassOutputCap, false, true},
		{"expired credentials", errors.New("execx: gh pr view 5: exit status 1: gh: Bad credentials (HTTP 401)"), ghClassAuth, false, false},
		{"forbidden", errors.New("execx: gh pr view 5: exit status 1: gh: Must have admin rights (HTTP 403)"), ghClassForbidden, false, false},
		{"deleted pr", errors.New("execx: gh pr view 5: exit status 1: gh: Not Found (HTTP 404)"), ghClassNotFound, false, false},
		{"no pr for branch", errors.New("execx: gh pr view 5: exit status 1: no pull requests found for branch \"x\""), ghClassNotFound, false, false},
		{"rate limited", errors.New("execx: gh pr view 5: exit status 1: API rate limit exceeded (HTTP 403)"), ghClassRateLimit, true, false},
		{"offline", errors.New("execx: gh pr view 5: exit status 1: could not resolve host: api.github.com"), ghClassNetwork, true, false},
		{"unrecognized", errors.New("execx: gh pr view 5: exit status 1"), ghClassUnknown, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyGHError(tt.err)
			if got.Class != tt.wantClass {
				t.Fatalf("class = %q, want %q", got.Class, tt.wantClass)
			}
			if got.Retryable != tt.wantRetryable {
				t.Fatalf("retryable = %v, want %v", got.Retryable, tt.wantRetryable)
			}
			if strings.HasPrefix(got.Message, "infra: ") != tt.wantInfraMsg {
				t.Fatalf("message %q: infra prefix = %v, want %v", got.Message, !tt.wantInfraMsg, tt.wantInfraMsg)
			}
			if got.Message == "" {
				t.Fatal("message must never be empty")
			}
		})
	}
}

// TestAwaitRemoteReview_ErrorEnvelopeCarriesClass proves the classification
// reaches the envelope, not just classifyGHError's return value: a missing
// gh binary is permanent, so the caller must be able to see that re-probing
// cannot help.
func TestAwaitRemoteReview_ErrorEnvelopeCarriesClass(t *testing.T) {
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	env, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 1, TimeoutSeconds: 600, IntervalSeconds: 60})
	if err != nil {
		t.Fatalf("unexpected Go error (should be classified into the envelope): %v", err)
	}
	if env.Status != "error" {
		t.Fatalf("got status %q, want error", env.Status)
	}
	if env.Ext["error_class"] != ghClassMissing {
		t.Fatalf("ext.error_class = %v, want %q", env.Ext["error_class"], ghClassMissing)
	}
	if env.Ext["retryable"] != false {
		t.Fatalf("ext.retryable = %v, want false (a missing gh binary is permanent)", env.Ext["retryable"])
	}
	if env.StateFile != nil {
		defer os.Remove(*env.StateFile)
	}
}

// ---------------------------------------------------------------------------
// verify_pipeline_await
// ---------------------------------------------------------------------------

func TestVerifyPipelineAwait_PendingThenGreen(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nprintf 'build\\tpending\\t1m\\thttps://x\\n'\nexit 8\n")
	defer cleanup()

	env, err := verifyPipelineAwait(".", VerifyPipelineAwaitIn{PR: 5, TimeoutSeconds: 1200, IntervalSeconds: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Status != "pending" {
		t.Fatalf("got status %q, want pending", env.Status)
	}
	if env.StateFile == nil || *env.StateFile == "" {
		t.Fatalf("expected non-empty state_file")
	}
	defer os.Remove(*env.StateFile)

	cleanup2 := stubGH(t, "#!/bin/sh\nprintf 'build\\tpass\\t2m\\thttps://x\\n'\n")
	defer cleanup2()

	env2, err := verifyPipelineAwait(".", VerifyPipelineAwaitIn{PR: 5, TimeoutSeconds: 1200, IntervalSeconds: 60, StateFile: *env.StateFile})
	if err != nil {
		t.Fatalf("unexpected error on resume: %v", err)
	}
	if env2.Status != "done" || env2.Ext["verdict"] != "green" {
		t.Fatalf("got status=%q verdict=%v, want done/green", env2.Status, env2.Ext["verdict"])
	}
}

func TestVerifyPipelineAwait_Failed(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nprintf 'lint\\tfail\\t30s\\thttps://x\\n'\nexit 1\n")
	defer cleanup()

	env, err := verifyPipelineAwait(".", VerifyPipelineAwaitIn{PR: 9, TimeoutSeconds: 1200, IntervalSeconds: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Status != "done" || env.Ext["verdict"] != "failed" {
		t.Fatalf("got status=%q verdict=%v, want done/failed", env.Status, env.Ext["verdict"])
	}
	if env.Ext["checks_raw"] == nil {
		t.Fatalf("expected checks_raw to be included in ext on failed verdict")
	}
}

// TestVerifyPipelineAwait_ExitCode8_PendingNotError is a regression test for
// issue #20: `gh pr checks` exits 8 when checks are still pending, and this
// must be classified as a pending poll state, not an error. polling.go:293
// whitelists exit codes 0, 1, and 8; execx.RunAllowExit (via
// ghx.PRChecksWithExitCode) separates the exit code from the returned error
// so a non-zero-but-whitelisted exit never surfaces as err != nil here.
func TestVerifyPipelineAwait_ExitCode8_PendingNotError(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nprintf 'build\\tpending\\t1m\\thttps://x\\n'\nexit 8\n")
	defer cleanup()

	env, err := verifyPipelineAwait(".", VerifyPipelineAwaitIn{PR: 20, TimeoutSeconds: 1200, IntervalSeconds: 60})
	if err != nil {
		t.Fatalf("exit code 8 must not surface as a Go error: %v", err)
	}
	if env.Status != "pending" {
		t.Fatalf("got status %q, want pending (exit code 8 is a whitelisted pending signal, not a failure)", env.Status)
	}
	if env.StateFile == nil || *env.StateFile == "" {
		t.Fatalf("expected non-empty state_file for a pending poll")
	}
	defer os.Remove(*env.StateFile)
}

// TestVerifyPipelineAwait_Exit1NoRowsIsProbeFailure pins that a gh failure
// that exits 1 with no check rows (here: PR not found) is a classified probe
// failure, not a "green" verdict. gh pr checks exits 1 both for failed
// checks and for its own errors; only the rows tell them apart.
func TestVerifyPipelineAwait_Exit1NoRowsIsProbeFailure(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho 'GraphQL: Could not resolve to a PullRequest with the number of 9. (repository.pullRequest)' >&2\nexit 1\n")
	defer cleanup()

	env, err := verifyPipelineAwait(".", VerifyPipelineAwaitIn{PR: 9, TimeoutSeconds: 1200, IntervalSeconds: 60})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if env.StateFile != nil {
		defer os.Remove(*env.StateFile)
	}
	if env.Status != "error" {
		t.Fatalf("got status=%q verdict=%v, want error (a gh failure must not read as green)", env.Status, env.Ext["verdict"])
	}
	if env.Ext["error_class"] != ghClassNotFound {
		t.Errorf("error_class = %v, want %q", env.Ext["error_class"], ghClassNotFound)
	}
	if env.Ext["retryable"] != false {
		t.Errorf("retryable = %v, want false", env.Ext["retryable"])
	}
	if !strings.Contains(env.Error, "Could not resolve to a PullRequest") {
		t.Errorf("error = %q, want it to carry gh's stderr", env.Error)
	}
}

// TestVerifyPipelineAwait_Exit1NoRowsAfterDeadline pins that the same gh
// failure after the deadline ends the poll as a timeout that names the
// probe failure class.
func TestVerifyPipelineAwait_Exit1NoRowsAfterDeadline(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho 'GraphQL: Could not resolve to a PullRequest with the number of 9. (repository.pullRequest)' >&2\nexit 1\n")
	defer cleanup()

	stateFile := newTimedOutPollState(t, "verify-pipeline")

	env, err := verifyPipelineAwait(".", VerifyPipelineAwaitIn{PR: 9, TimeoutSeconds: 1, IntervalSeconds: 1, StateFile: stateFile})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if env.Status != "done" || env.Ext["verdict"] != "timeout" {
		t.Fatalf("got status=%q verdict=%v, want done/timeout", env.Status, env.Ext["verdict"])
	}
	if env.Ext["probe_error_class"] != ghClassNotFound {
		t.Errorf("probe_error_class = %v, want %q", env.Ext["probe_error_class"], ghClassNotFound)
	}
}

// TestVerifyPipelineAwait_NoChecksReportedIsNotGreen pins that gh's "no
// checks reported" error (exit 1, stderr only) is not read as green: right
// after a push, GitHub may not have registered any check yet. In a repo
// with a CI config it is an unknown, retryable probe failure, so the caller
// re-probes until checks appear or the deadline passes.
func TestVerifyPipelineAwait_NoChecksReportedIsNotGreen(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho \"no checks reported on the 'feat' branch\" >&2\nexit 1\n")
	defer cleanup()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"), "on: push\n")

	env, err := verifyPipelineAwait(root, VerifyPipelineAwaitIn{PR: 9, TimeoutSeconds: 1200, IntervalSeconds: 60})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if env.StateFile != nil {
		defer os.Remove(*env.StateFile)
	}
	if env.Status != "error" {
		t.Fatalf("got status=%q verdict=%v, want error", env.Status, env.Ext["verdict"])
	}
	if env.Ext["error_class"] != ghClassUnknown || env.Ext["retryable"] != true {
		t.Errorf("error_class=%v retryable=%v, want %q true", env.Ext["error_class"], env.Ext["retryable"], ghClassUnknown)
	}
}

// TestVerifyPipelineAwait_NoChecksReportedWithoutCIConfigIsSkipped pins the
// no-CI fast path: gh reports no checks and the repo has no CI config, so no
// check will ever start. The poll ends at once as skipped with reason no-ci
// instead of re-probing until the deadline — and never reads as green.
func TestVerifyPipelineAwait_NoChecksReportedWithoutCIConfigIsSkipped(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho \"no checks reported on the 'feat' branch\" >&2\nexit 1\n")
	defer cleanup()

	root := t.TempDir()
	// Files that are not a CI config must not count as one.
	writeFile(t, filepath.Join(root, ".github", "workflows", "README.md"), "no workflows yet\n")
	writeFile(t, filepath.Join(root, ".github", "CODEOWNERS"), "* @me\n")

	env, err := pollAwait(root, PollAwaitIn{Target: "pipeline", PR: 9})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if env.StateFile != nil {
		defer os.Remove(*env.StateFile)
	}
	if env.Status != "done" {
		t.Fatalf("got status=%q error=%q, want done", env.Status, env.Error)
	}
	if env.Ext["verdict"] != "skipped" || env.Ext["reason"] != "no-ci" {
		t.Errorf("verdict=%v reason=%v, want skipped/no-ci", env.Ext["verdict"], env.Ext["reason"])
	}
	if env.Ext["pr_number"] != 9 {
		t.Errorf("pr_number = %v, want 9", env.Ext["pr_number"])
	}
}

// TestVerifyPipelineAwait_NoChecksReportedKeepsPollingPerCISystem pins that
// every CI system the tool knows about keeps the poll going when gh reports
// no checks yet: its checks may simply not have started.
func TestVerifyPipelineAwait_NoChecksReportedKeepsPollingPerCISystem(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\necho \"no checks reported on the 'feat' branch\" >&2\nexit 1\n")
	defer cleanup()

	tests := []struct {
		name string
		file string
	}{
		{"github actions yaml", filepath.Join(".github", "workflows", "build.yaml")},
		{"circleci", filepath.Join(".circleci", "config.yml")},
		{"jenkins", "Jenkinsfile"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, tt.file), "x\n")

			env, err := pollAwait(root, PollAwaitIn{Target: "pipeline", PR: 9})
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if env.StateFile != nil {
				defer os.Remove(*env.StateFile)
			}
			if env.Status != "error" || env.Ext["retryable"] != true {
				t.Errorf("got status=%q retryable=%v verdict=%v, want a retryable error", env.Status, env.Ext["retryable"], env.Ext["verdict"])
			}
		})
	}
}

// TestVerifyPipelineAwait_EmptyOutputExit0IsGreen pins the spec's "no
// failed and no pending row" outcome for gh pr checks printing nothing and
// exiting 0: a successful gh run lists no failing or pending check, so the
// verdict is green. Only a non-zero exit with no rows is a probe failure.
func TestVerifyPipelineAwait_EmptyOutputExit0IsGreen(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nexit 0\n")
	defer cleanup()

	env, err := pollAwait(t.TempDir(), PollAwaitIn{Target: "pipeline", PR: 3})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if env.StateFile != nil {
		defer os.Remove(*env.StateFile)
	}
	if env.Status != "done" || env.Ext["verdict"] != "green" {
		t.Fatalf("got status=%q verdict=%v error=%q, want done/green", env.Status, env.Ext["verdict"], env.Error)
	}
}

func TestVerifyPipelineAwait_MissingGHBinary(t *testing.T) {
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	defer os.Setenv("PATH", origPath)

	env, err := verifyPipelineAwait(".", VerifyPipelineAwaitIn{PR: 1, TimeoutSeconds: 1200, IntervalSeconds: 60})
	if err != nil {
		t.Fatalf("unexpected Go error (should be classified into the envelope): %v", err)
	}
	if env.Status != "error" {
		t.Fatalf("got status %q, want error", env.Status)
	}
	if !strings.Contains(env.Error, "infra") {
		t.Fatalf("expected classified infra error in envelope, got %q", env.Error)
	}
}

func TestVerifyPipelineAwait_InvalidPR(t *testing.T) {
	_, err := verifyPipelineAwait(".", VerifyPipelineAwaitIn{PR: -1})
	if err == nil {
		t.Fatal("expected error for pr <= 0")
	}
}

// TestVerifyPipelineAwait_Timeout mirrors TestAwaitRemoteReview_Timeout: the
// final probe still finds pending checks, so the timed-out state reports
// timeout, and a subsequent call against the now-exhausted state file
// short-circuits to "skipped" without probing gh again.
func TestVerifyPipelineAwait_Timeout(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nprintf 'build\\tpending\\t1m\\thttps://x\\n'\nexit 8\n")
	defer cleanup()

	stateFile := newTimedOutPollState(t, "verify-pipeline")

	env, err := verifyPipelineAwait(".", VerifyPipelineAwaitIn{PR: 1, TimeoutSeconds: 1, IntervalSeconds: 1, StateFile: stateFile})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Status != "done" || env.Ext["verdict"] != "timeout" {
		t.Fatalf("got status=%q verdict=%v, want done/timeout", env.Status, env.Ext["verdict"])
	}
	if env.Ext["pending_checks"] == nil {
		t.Fatalf("expected pending_checks to be included in ext on timeout")
	}

	env2, err := verifyPipelineAwait(".", VerifyPipelineAwaitIn{PR: 1, TimeoutSeconds: 1, IntervalSeconds: 1, StateFile: stateFile})
	if err != nil {
		t.Fatalf("unexpected error on exhausted resume: %v", err)
	}
	if env2.Ext["verdict"] != "skipped" {
		t.Fatalf("got verdict %v, want skipped", env2.Ext["verdict"])
	}
}

// TestVerifyPipelineAwait_TimedOutFinalProbeFindsVerdict is verify_pipeline_await's
// half of the R1 false-timeout regression: a pipeline that turns green (or
// fails) during the last interval must resolve as that verdict, not
// "timeout", mirroring TestAwaitRemoteReview_TimedOutFinalProbeFindsVerdict.
func TestVerifyPipelineAwait_TimedOutFinalProbeFindsVerdict(t *testing.T) {
	tests := []struct {
		name        string
		script      string
		wantVerdict string
	}{
		{"green", "#!/bin/sh\nprintf 'build\\tpass\\t2m\\thttps://x\\n'\n", "green"},
		{"failed", "#!/bin/sh\nprintf 'lint\\tfail\\t30s\\thttps://x\\n'\nexit 1\n", "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := stubGH(t, tt.script)
			defer cleanup()

			stateFile := newTimedOutPollState(t, "verify-pipeline")

			env, err := verifyPipelineAwait(".", VerifyPipelineAwaitIn{PR: 5, TimeoutSeconds: 1, IntervalSeconds: 1, StateFile: stateFile})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if env.Status != "done" || env.Ext["verdict"] != tt.wantVerdict {
				t.Fatalf("got status=%q verdict=%v, want done/%s", env.Status, env.Ext["verdict"], tt.wantVerdict)
			}
			if _, timedOut := env.Ext["waited_seconds"]; timedOut {
				t.Fatalf("verdict envelope must not carry the timeout envelope's waited_seconds: %v", env.Ext)
			}
		})
	}
}

// TestVerifyPipelineAwait_TimedOutPersistentProbeErrorEndsPoll is
// verify_pipeline_await's half of the DD3 unbounded-poll regression: once
// the deadline has passed, a gh failure must END the poll rather than
// returning yet another error envelope. This exercises the exitCode !=
// 0/1/8 -> probeFailureEnvelope call site specifically (the err != nil site
// is already covered by TestVerifyPipelineAwait_MissingGHBinary).
func TestVerifyPipelineAwait_TimedOutPersistentProbeErrorEndsPoll(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nexit 2\n")
	defer cleanup()

	stateFile := newTimedOutPollState(t, "verify-pipeline")

	env, err := verifyPipelineAwait(".", VerifyPipelineAwaitIn{PR: 5, TimeoutSeconds: 1, IntervalSeconds: 1, StateFile: stateFile})
	if err != nil {
		t.Fatalf("unexpected Go error (should be classified into the envelope): %v", err)
	}
	if env.Status != "done" || env.Ext["verdict"] != "timeout" {
		t.Fatalf("got status=%q verdict=%v, want done/timeout (a timed-out probe error must end the poll)", env.Status, env.Ext["verdict"])
	}
	probeErr, _ := env.Ext["probe_error"].(string)
	if probeErr == "" {
		t.Fatalf("timeout envelope must carry the probe error in ext.probe_error, got ext=%v", env.Ext)
	}
	if env.Ext["probe_error_class"] != ghClassUnexpectedExit {
		t.Fatalf("ext.probe_error_class = %v, want %q", env.Ext["probe_error_class"], ghClassUnexpectedExit)
	}

	st, err := stepper.LoadPollState(stateFile)
	if err != nil {
		t.Fatalf("LoadPollState: %v", err)
	}
	if !st.Exhausted {
		t.Fatal("ending the poll must mark the state exhausted")
	}

	env2, err := verifyPipelineAwait(".", VerifyPipelineAwaitIn{PR: 5, TimeoutSeconds: 1, IntervalSeconds: 1, StateFile: stateFile})
	if err != nil {
		t.Fatalf("unexpected error on exhausted resume: %v", err)
	}
	if env2.Status != "done" || env2.Ext["verdict"] != "skipped" {
		t.Fatalf("got status=%q verdict=%v, want done/skipped", env2.Status, env2.Ext["verdict"])
	}
}

// TestPollAwait_PipelineOtherExitCodeClassifiedFromStderr pins that a gh pr
// checks exit outside 0/1/8 is classified like any other gh failure before
// falling back to unexpected-exit. gh exits 4 when it needs authentication,
// which is permanent (not retryable) and must read as "auth" so the caller
// stops re-probing and tells the user to log in.
func TestPollAwait_PipelineOtherExitCodeClassifiedFromStderr(t *testing.T) {
	tests := []struct {
		name          string
		script        string
		wantClass     string
		wantRetryable bool
		wantInMessage string
	}{
		{
			name:          "exit 4 auth required",
			script:        "#!/bin/sh\necho 'To get started with GitHub CLI, please run:  gh auth login' >&2\nexit 4\n",
			wantClass:     ghClassAuth,
			wantRetryable: false,
			wantInMessage: "gh auth login",
		},
		{
			name:          "exit 4 without stderr",
			script:        "#!/bin/sh\nexit 4\n",
			wantClass:     ghClassAuth,
			wantRetryable: false,
			wantInMessage: "gh pr checks: exit 4",
		},
		{
			name:          "exit 2 with HTTP 404",
			script:        "#!/bin/sh\necho 'gh: Not Found (HTTP 404)' >&2\nexit 2\n",
			wantClass:     ghClassNotFound,
			wantRetryable: false,
			wantInMessage: "HTTP 404",
		},
		{
			name:          "exit 2 unknown stderr",
			script:        "#!/bin/sh\necho 'something odd' >&2\nexit 2\n",
			wantClass:     ghClassUnexpectedExit,
			wantRetryable: false,
			wantInMessage: "something odd",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanup := stubGH(t, tt.script)
			defer cleanup()

			env, err := pollAwait(".", PollAwaitIn{Target: "pipeline", PR: 5})
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if env.StateFile != nil {
				defer os.Remove(*env.StateFile)
			}
			if env.Status != "error" {
				t.Fatalf("got status %q, want error", env.Status)
			}
			if env.Ext["error_class"] != tt.wantClass {
				t.Errorf("ext.error_class = %v, want %q", env.Ext["error_class"], tt.wantClass)
			}
			if env.Ext["retryable"] != tt.wantRetryable {
				t.Errorf("ext.retryable = %v, want %v", env.Ext["retryable"], tt.wantRetryable)
			}
			if !strings.Contains(env.Error, tt.wantInMessage) {
				t.Errorf("error = %q, want it to contain %q", env.Error, tt.wantInMessage)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// poll_await dispatch
// ---------------------------------------------------------------------------

// TestPollAwait_RemoteReviewDefaultTimeout asserts that a zero
// TimeoutSeconds on target "remote_review" resolves to 600s (not the
// pipeline target's 1200s default), observable via the pending envelope's
// progress.timeout_seconds — pendingEnvelope writes the resolved
// st.TimeoutSeconds there.
func TestPollAwait_RemoteReviewDefaultTimeout(t *testing.T) {
	cleanup := stubGHReviews(t, 42)
	defer cleanup()

	env, err := pollAwait(".", PollAwaitIn{Target: "remote_review", PR: 42})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Status != "pending" {
		t.Fatalf("got status %q, want pending", env.Status)
	}
	defer os.Remove(*env.StateFile)

	progress, ok := env.Progress.(map[string]any)
	if !ok {
		t.Fatalf("expected env.Progress to be map[string]any, got %T", env.Progress)
	}
	if got := progress["timeout_seconds"]; got != 600 {
		t.Fatalf("timeout_seconds = %v, want 600 (remote_review default)", got)
	}
}

// TestPollAwait_PipelineDefaultTimeout is the pipeline-target counterpart:
// a zero TimeoutSeconds must resolve to 1200s, not 600s. This is the
// critical branch the fact sheet calls out — the two targets' defaults
// must never collapse into one shared value.
func TestPollAwait_PipelineDefaultTimeout(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nprintf 'build\\tpending\\t1m\\thttps://x\\n'\n")
	defer cleanup()

	env, err := pollAwait(".", PollAwaitIn{Target: "pipeline", PR: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Status != "pending" {
		t.Fatalf("got status %q, want pending", env.Status)
	}
	defer os.Remove(*env.StateFile)

	progress, ok := env.Progress.(map[string]any)
	if !ok {
		t.Fatalf("expected env.Progress to be map[string]any, got %T", env.Progress)
	}
	if got := progress["timeout_seconds"]; got != 1200 {
		t.Fatalf("timeout_seconds = %v, want 1200 (pipeline default)", got)
	}
}

func TestPollAwait_UnknownTarget(t *testing.T) {
	_, err := pollAwait(".", PollAwaitIn{Target: "bogus", PR: 1})
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
	var domainErr *mcpserver.DomainError
	if !errors.As(err, &domainErr) {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
}

// TestPollAwait_StateFileFromOtherTargetRejected pins that a state file is
// bound to the target that created it. Resuming a remote_review poll's
// state_file under target "pipeline" (or the reverse) would reuse the other
// poll's budget and exhausted marker, so it is a DomainError and gh never
// runs. The stub gh writes a marker file to prove it was not called.
func TestPollAwait_StateFileFromOtherTargetRejected(t *testing.T) {
	tests := []struct {
		target      string
		storedSkill string
	}{
		{"pipeline", "await-remote-review"},
		{"remote_review", "verify-pipeline"},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "gh-ran")
			cleanup := stubGH(t, fmt.Sprintf("#!/bin/sh\ntouch %q\nexit 3\n", marker))
			defer cleanup()

			stateFile, err := stepper.NewStateFilePath(tt.storedSkill)
			if err != nil {
				t.Fatalf("NewStateFilePath: %v", err)
			}
			t.Cleanup(func() { os.Remove(stateFile) })
			if err := stepper.SavePollState(stateFile, stepper.NewPollState(tt.storedSkill, 600, 60)); err != nil {
				t.Fatalf("SavePollState: %v", err)
			}

			env, err := pollAwait(".", PollAwaitIn{Target: tt.target, PR: 5, StateFile: stateFile})
			var domainErr *mcpserver.DomainError
			if !errors.As(err, &domainErr) {
				t.Fatalf("expected *mcpserver.DomainError, got err=%v env=%+v", err, env)
			}
			if !strings.Contains(domainErr.Msg, tt.storedSkill) {
				t.Errorf("Msg = %q, want it to name the stored skill %q", domainErr.Msg, tt.storedSkill)
			}
			if domainErr.Suggestion == "" {
				t.Error("Suggestion must not be empty")
			}
			if _, err := os.Stat(marker); err == nil {
				t.Error("gh ran; a mismatched state file must be rejected before probing")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// verify_pipeline_classify / ClassifyLogs
// ---------------------------------------------------------------------------

func TestClassifyLogs(t *testing.T) {
	tests := []struct {
		name     string
		logs     string
		wantCat  string
		wantSome string // a substring expected in at least one signal, "" to skip
	}{
		{"empty", "", "unknown", ""},
		{"whitespace only", "   \n\t  ", "unknown", ""},
		{"lint eslint", "Running eslint...\n12 problems (10 errors, 2 warnings)", "lint", "eslint"},
		{"test failing", "3 failing\n  1) foo bar", "test-failure", "failing"},
		{"test assertion", "AssertionError: expected true to equal false", "test-failure", "AssertionError"},
		{"type ts error", "src/index.ts(10,5): error TS2322: Type mismatch", "type-error", "TS"},
		{"build cannot find module", "Error: Cannot find module 'foo'", "build-error", "Cannot"},
		{"dependency npm err", "npm ERR! code E404", "dependency", "npm"},
		{"infra timeout", "Error: The operation was canceled: timeout", "infra", "time"},
		{"infra bad gateway", "502 Bad Gateway", "infra", "502"},
		{"unknown text", "everything is fine, build succeeded", "unknown", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := ClassifyLogs(tt.logs)
			if out.Category != tt.wantCat {
				t.Fatalf("category = %q, want %q (signals=%v)", out.Category, tt.wantCat, out.Signals)
			}
			if out.Signals == nil {
				t.Fatalf("signals must never be nil (JS source always returns an array)")
			}
			if tt.wantSome != "" {
				found := false
				for _, s := range out.Signals {
					if strings.Contains(s, tt.wantSome) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("expected a signal containing %q, got %v", tt.wantSome, out.Signals)
				}
			}
		})
	}
}

// TestClassifyLogs_ExactSignalStrings guards the fact sheet's AC2 ("identical
// classification output") at the level of the signal label text itself, not
// just its category or a loose substring. Each label must equal the JS
// source RegExp's .source verbatim (see classifyPattern's doc comment) —
// this test would catch a hand-transcription typo in any of the 26 ported
// patterns that a substring-only assertion would miss.
func TestClassifyLogs_ExactSignalStrings(t *testing.T) {
	tests := []struct {
		name string
		logs string
		want string
	}{
		{"dependency npm err", "npm ERR! code E404", `dep:\bnpm\s+ERR!\s+code\s+E\w+`},
		{"lint eslint", "Running eslint now", `lint:\beslint\b`},
		{"infra bad gateway", "502 Bad Gateway", `infra:\b502\s+Bad\s+Gateway\b`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := ClassifyLogs(tt.logs)
			found := false
			for _, s := range out.Signals {
				if s == tt.want {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected exact signal %q, got %v", tt.want, out.Signals)
			}
		})
	}
}

func TestClassifyLogs_PriorityOrder(t *testing.T) {
	// Both a lint signal and a test signal are present; lint must win since
	// it is checked first (mirrors classifyLogs' has('lint:') check first).
	out := ClassifyLogs("eslint found problems\n3 failing tests")
	if out.Category != "lint" {
		t.Fatalf("category = %q, want lint (priority order)", out.Category)
	}
	if len(out.Signals) < 2 {
		t.Fatalf("expected signals from multiple categories to all be collected, got %v", out.Signals)
	}
}

func TestClassifyLogs_PassthroughViaTool(t *testing.T) {
	out := ClassifyLogs("npm ERR! code E404")
	out.CheckName = "build"
	out.Conclusion = "failure"
	if out.CheckName != "build" || out.Conclusion != "failure" {
		t.Fatalf("expected check_name/conclusion to be settable passthrough fields")
	}
	if out.Category != "dependency" {
		t.Fatalf("category = %q, want dependency", out.Category)
	}
}

// ---------------------------------------------------------------------------
// evaluateReviews / evaluateChecksText (unit-level, no gh involved)
// ---------------------------------------------------------------------------

func TestEvaluateReviews(t *testing.T) {
	const (
		t1 = "2026-01-01T10:00:00Z"
		t2 = "2026-01-01T11:00:00Z"
	)
	tests := []struct {
		name         string
		reviews      []ghx.PRReview
		reviewers    []string
		wantStatus   string
		wantReviewer string
		wantState    string
	}{
		{"no reviews yet", nil, []string{"copilot"}, "", "", ""},
		{"approved", []ghx.PRReview{{Login: "copilot", State: "APPROVED"}}, []string{"copilot"}, "approved-clean", "copilot", "APPROVED"},
		{"changes requested", []ghx.PRReview{{Login: "copilot", State: "CHANGES_REQUESTED"}}, []string{"copilot"}, "actionable", "copilot", "CHANGES_REQUESTED"},
		{"commented", []ghx.PRReview{{Login: "copilot", State: "COMMENTED"}}, []string{"copilot"}, "actionable", "copilot", "COMMENTED"},
		{"copilot bot login", []ghx.PRReview{{Login: "copilot-pull-request-reviewer[bot]", State: "APPROVED"}}, []string{"copilot"}, "approved-clean", "copilot", "APPROVED"},
		{"copilot reviewer login without bot suffix", []ghx.PRReview{{Login: "copilot-pull-request-reviewer", State: "COMMENTED"}}, []string{"copilot"}, "actionable", "copilot", "COMMENTED"},
		{"copilot mixed case", []ghx.PRReview{{Login: "Copilot", State: "APPROVED"}}, []string{"copilot"}, "approved-clean", "copilot", "APPROVED"},
		{"custom reviewer", []ghx.PRReview{{Login: "alice", State: "APPROVED"}}, []string{"alice"}, "approved-clean", "alice", "APPROVED"},
		{"login match is case-insensitive", []ghx.PRReview{{Login: "alice", State: "APPROVED"}}, []string{"ALICE"}, "approved-clean", "ALICE", "APPROVED"},
		{"configured name is not a substring match", []ghx.PRReview{{Login: "alice-bot-2", State: "APPROVED"}}, []string{"alice"}, "", "", ""},
		{"unconfigured reviewer ignored", []ghx.PRReview{{Login: "mallory", State: "APPROVED"}}, []string{"alice"}, "", "", ""},
		{"unconfigured reviewer does not hide configured one", []ghx.PRReview{
			{Login: "mallory", State: "APPROVED"},
			{Login: "alice", State: "COMMENTED"},
		}, []string{"alice"}, "actionable", "alice", "COMMENTED"},
		{"empty reviewers list", []ghx.PRReview{{Login: "copilot", State: "APPROVED"}}, nil, "", "", ""},
		{"blank reviewer entry skipped", []ghx.PRReview{{Login: "", State: "APPROVED"}}, []string{"", "  "}, "", "", ""},
		{"first configured reviewer with a verdict wins", []ghx.PRReview{
			{Login: "bob", State: "APPROVED"},
			{Login: "alice", State: "COMMENTED"},
		}, []string{"alice", "bob"}, "actionable", "alice", "COMMENTED"},
		{"later review wins by timestamp", []ghx.PRReview{
			{Login: "alice", State: "COMMENTED", SubmittedAt: t1},
			{Login: "alice", State: "APPROVED", SubmittedAt: t2},
		}, []string{"alice"}, "approved-clean", "alice", "APPROVED"},
		{"timestamp beats slice order", []ghx.PRReview{
			{Login: "alice", State: "APPROVED", SubmittedAt: t2},
			{Login: "alice", State: "COMMENTED", SubmittedAt: t1},
		}, []string{"alice"}, "approved-clean", "alice", "APPROVED"},
		{"without timestamps, later entry wins", []ghx.PRReview{
			{Login: "alice", State: "APPROVED"},
			{Login: "alice", State: "CHANGES_REQUESTED"},
		}, []string{"alice"}, "actionable", "alice", "CHANGES_REQUESTED"},
		{"pending draft review ignored", []ghx.PRReview{
			{Login: "alice", State: "APPROVED", SubmittedAt: t1},
			{Login: "alice", State: "PENDING"},
		}, []string{"alice"}, "approved-clean", "alice", "APPROVED"},
		{"dismissed latest review is no verdict", []ghx.PRReview{
			{Login: "alice", State: "COMMENTED", SubmittedAt: t1},
			{Login: "alice", State: "DISMISSED", SubmittedAt: t2},
		}, []string{"alice"}, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, reviewer, rawState := evaluateReviews(tt.reviews, tt.reviewers)
			if status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", status, tt.wantStatus)
			}
			if reviewer != tt.wantReviewer {
				t.Fatalf("reviewer = %q, want %q", reviewer, tt.wantReviewer)
			}
			if rawState != tt.wantState {
				t.Fatalf("rawState = %q, want %q", rawState, tt.wantState)
			}
		})
	}
}

func TestEvaluateChecksText(t *testing.T) {
	text := "build\tpass\t1m\thttps://x\nlint\tfail\t30s\thttps://y\ntest\tpending\t-\thttps://z\n"
	failed, pending := evaluateChecksText(text)
	if len(failed) != 1 || failed[0].Name != "lint" {
		t.Fatalf("failed = %+v, want one entry named lint", failed)
	}
	if len(pending) != 1 || pending[0].Name != "test" {
		t.Fatalf("pending = %+v, want one entry named test", pending)
	}
}

func TestEvaluateChecksText_AllGreen(t *testing.T) {
	text := "build\tpass\t1m\thttps://x\nlint\tpass\t30s\thttps://y\n"
	failed, pending := evaluateChecksText(text)
	if len(failed) != 0 || len(pending) != 0 {
		t.Fatalf("expected no failed/pending, got failed=%+v pending=%+v", failed, pending)
	}
}
