package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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

// ---------------------------------------------------------------------------
// await_remote_review
// ---------------------------------------------------------------------------

func TestAwaitRemoteReview_PendingThenResume(t *testing.T) {
	// gh pr view prints a reviewers summary with no parenthesized state for
	// copilot yet (still pending review).
	cleanup := stubGH(t, "#!/bin/sh\necho \"reviewers: copilot\"\n")
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
	cleanup2 := stubGH(t, "#!/bin/sh\necho \"reviewers: copilot (Approved)\"\n")
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
	cleanup := stubGH(t, "#!/bin/sh\necho \"reviewers: copilot (Changes requested)\"\n")
	defer cleanup()

	env, err := awaitRemoteReview(".", AwaitRemoteReviewIn{PR: 7, TimeoutSeconds: 600, IntervalSeconds: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Status != "done" || env.Ext["verdict"] != "actionable" {
		t.Fatalf("got status=%q verdict=%v, want done/actionable", env.Status, env.Ext["verdict"])
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
	cleanup := stubGH(t, "#!/bin/sh\necho \"reviewers: copilot\"\n")
	defer cleanup()

	stateFile, err := stepper.NewStateFilePath("await-remote-review")
	if err != nil {
		t.Fatalf("NewStateFilePath: %v", err)
	}
	defer os.Remove(stateFile)

	st := stepper.NewPollState("await-remote-review", 1, 1)
	st.StartedAt -= 1000 // force TimedOut()
	if err := stepper.SavePollState(stateFile, st); err != nil {
		t.Fatalf("SavePollState: %v", err)
	}

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

// ---------------------------------------------------------------------------
// verify_pipeline_await
// ---------------------------------------------------------------------------

func TestVerifyPipelineAwait_PendingThenGreen(t *testing.T) {
	cleanup := stubGH(t, "#!/bin/sh\nprintf 'build\\tpending\\t1m\\thttps://x\\n'\n")
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
	cleanup := stubGH(t, "#!/bin/sh\nprintf 'lint\\tfail\\t30s\\thttps://x\\n'\n")
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
// evaluateReviewText / evaluateChecksText (unit-level, no gh involved)
// ---------------------------------------------------------------------------

func TestEvaluateReviewText(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		reviewers    []string
		wantStatus   string
		wantReviewer string
	}{
		{"no match yet", "reviewers: copilot", []string{"copilot"}, "", ""},
		{"approved", "reviewers: copilot (Approved)", []string{"copilot"}, "approved-clean", "copilot"},
		{"changes requested", "reviewers: copilot (Changes requested)", []string{"copilot"}, "actionable", "copilot"},
		{"commented", "reviewers: copilot (Commented)", []string{"copilot"}, "actionable", "copilot"},
		{"bot suffix", "reviewers: copilot-pull-request-reviewer[bot] (Approved)", []string{"copilot"}, "approved-clean", "copilot"},
		{"custom reviewer", "reviewers: alice (Approved)", []string{"alice"}, "approved-clean", "alice"},
		{"empty reviewers list", "reviewers: copilot (Approved)", nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, reviewer, _ := evaluateReviewText(tt.text, tt.reviewers)
			if status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", status, tt.wantStatus)
			}
			if reviewer != tt.wantReviewer {
				t.Fatalf("reviewer = %q, want %q", reviewer, tt.wantReviewer)
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
