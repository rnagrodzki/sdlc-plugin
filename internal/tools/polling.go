package tools

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/stepper"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// classifyGHError formats a gh invocation error for the stepper envelope's
// error path, per this task's dependency note: "Missing gh binary yields a
// classified infra error, not a panic — propagate that classification into
// the stepper envelope's error path rather than swallowing it." A missing
// gh binary (ghx.ErrGHNotFound) is labeled distinctly from any other gh
// failure (auth, network, bad PR number) so a caller can tell "gh isn't
// installed" apart from "gh ran and failed."
func classifyGHError(err error) string {
	if errors.Is(err, ghx.ErrGHNotFound) {
		return "infra: " + err.Error()
	}
	return err.Error()
}

// ---------------------------------------------------------------------------
// await_remote_review
//
// Ports scripts/skill/await-remote-review.js (R50-R56 of
// docs/specs/ship.md). The JS source blocks synchronously for up to
// `timeout` seconds, polling `gh api repos/{owner}/{repo}/pulls/{pr}/reviews`
// (REST, structured JSON) on an internal Atomics.wait loop and emitting one
// final JSON line.
//
// KD8 bounded polling replaces that internal loop: this tool performs
// exactly one non-blocking probe per call and returns a stepper.Envelope —
// "pending" with a state_file to resume, or a terminal "done"/"error".
//
// Review state comes from ghx.PRReviewsJSON (`gh pr view <n> --json
// reviews`) and is evaluated by evaluateReviews over the structured
// reviewer login and review state — not by pattern-matching `gh pr view`'s
// plain-text output.
//
// The probe runs even when the poll state is already timed out, and the
// timeout verdict is only reported if that final probe also finds nothing. A
// review that lands during the last interval therefore resolves as a verdict,
// not as a false timeout.
// ---------------------------------------------------------------------------

// AwaitRemoteReviewIn is the input for the await_remote_review tool.
// Zero-value TimeoutSeconds/IntervalSeconds/Reviewers fall back to the JS
// source's parseArgs defaults (600s timeout, 60s interval, ["copilot"]).
// StateFile is the positional --state-file CLI flag from the JS source,
// now a typed field: pass back the state_file value from a prior "pending"
// envelope to resume that same bounded poll.
type AwaitRemoteReviewIn struct {
	PR              int      `json:"pr"`
	TimeoutSeconds  int      `json:"timeout_seconds,omitempty"`
	IntervalSeconds int      `json:"interval_seconds,omitempty"`
	Reviewers       []string `json:"reviewers,omitempty"`
	StateFile       string   `json:"state_file,omitempty"`
}

// canonicalReviewer normalizes a GitHub login for comparison: trimmed,
// lower-cased, without a "[bot]" suffix, with Copilot's reviewer-bot login
// ("copilot-pull-request-reviewer") folded into "copilot" — the name the
// default reviewer list uses — mirroring the JS source's evaluateReviews
// (R56) [bot]-suffix strip and copilot-variant canonicalization.
func canonicalReviewer(login string) string {
	l := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(login)), "[bot]")
	if l == "copilot-pull-request-reviewer" {
		return "copilot"
	}
	return l
}

// submittedBefore reports whether RFC3339 timestamp a is strictly earlier
// than b. A missing or unparseable timestamp on either side reports false,
// which makes the caller fall back to gh's own oldest-first review order.
func submittedBefore(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339, a)
	tb, errB := time.Parse(time.RFC3339, b)
	return errA == nil && errB == nil && ta.Before(tb)
}

// evaluateReviews ports evaluateReviews (R51-R53, R56) over the structured
// review list from ghx.PRReviewsJSON. For each configured reviewer, in the
// order given, it takes that reviewer's most recent submitted review and maps
// its state to the JS source's verdict buckets:
//
//	APPROVED                          -> "approved-clean"
//	COMMENTED / CHANGES_REQUESTED     -> "actionable"
//	anything else (DISMISSED)         -> no verdict (keep waiting)
//
// The first configured reviewer with a verdict wins. Reviewer logins are
// compared case-insensitively via canonicalReviewer. A review by anyone not
// in the configured list is ignored, as is a PENDING review (an unsubmitted
// draft). "Most recent" is by SubmittedAt; when a timestamp is missing or
// unparseable, later entries in gh's oldest-first order win.
//
// The returned reviewer is the configured name that matched (as passed in,
// not the login gh reported) and rawState is gh's raw review state.
func evaluateReviews(reviews []ghx.PRReview, reviewers []string) (status, reviewer, rawState string) {
	for _, r := range reviewers {
		want := canonicalReviewer(r)
		if want == "" {
			continue
		}
		var latest *ghx.PRReview
		for i := range reviews {
			rv := &reviews[i]
			if canonicalReviewer(rv.Login) != want || strings.EqualFold(rv.State, "PENDING") {
				continue
			}
			if latest == nil || !submittedBefore(rv.SubmittedAt, latest.SubmittedAt) {
				latest = rv
			}
		}
		if latest == nil {
			continue
		}
		switch strings.ToUpper(latest.State) {
		case "APPROVED":
			return "approved-clean", r, latest.State
		case "COMMENTED", "CHANGES_REQUESTED":
			return "actionable", r, latest.State
		}
	}
	return "", "", ""
}

// awaitRemoteReview implements one KD8 probe of await_remote_review.
func awaitRemoteReview(activeRoot string, in AwaitRemoteReviewIn) (stepper.Envelope, error) {
	if in.PR <= 0 {
		return stepper.Envelope{}, &mcpserver.DomainError{
			Msg:        "pr must be a positive integer",
			Suggestion: "Pass pr as the pull request number (a positive integer), then call the tool again.",
		}
	}

	timeoutSeconds := in.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 600
	}
	intervalSeconds := in.IntervalSeconds
	if intervalSeconds <= 0 {
		intervalSeconds = 60
	}
	reviewers := in.Reviewers
	if len(reviewers) == 0 {
		reviewers = []string{"copilot"}
	}

	stateFile := in.StateFile
	st, err := loadOrInitPollState(stateFile, "await-remote-review", timeoutSeconds, intervalSeconds, &stateFile)
	if err != nil {
		return stepper.Envelope{}, err
	}

	if st.Exhausted {
		return stepper.Done(stateFile, "", map[string]any{
			"verdict":   "skipped",
			"reason":    "exhausted",
			"pr_number": in.PR,
		}), nil
	}

	// Final probe: probe even when the poll state is already timed out. A
	// review that landed during the last interval must resolve as a verdict,
	// not as a false timeout. A probe error returns before the timeout branch,
	// so a transient gh failure never marks the state exhausted and stays
	// retryable.
	timedOut := st.TimedOut()

	reviews, err := ghx.PRReviewsJSON(activeRoot, in.PR)
	if err != nil {
		return stepper.NewError(stateFile, classifyGHError(err)), nil
	}

	status, reviewer, rawState := evaluateReviews(reviews, reviewers)
	if status != "" {
		return stepper.Done(stateFile, "", map[string]any{
			"verdict":   status,
			"reviewer":  reviewer,
			"state":     rawState,
			"pr_number": in.PR,
		}), nil
	}

	if timedOut {
		return timeoutEnvelope(stateFile, st, map[string]any{
			"reviewers": reviewers,
			"pr_number": in.PR,
		})
	}

	return pendingEnvelope(stateFile, st, map[string]any{
		"pr_number": in.PR,
		"reviewers": reviewers,
	})
}

// ---------------------------------------------------------------------------
// verify_pipeline_await
//
// Ports scripts/skill/verify-pipeline.js (R41-R44, R47-R49). Same KD8
// bounded-polling shape as await_remote_review above.
//
// RULING: ghx.PRChecksWithExitCode wraps plain `gh pr checks <n>`
// (tab-separated name/state/elapsed/link columns), not `--json`.
// evaluateChecksText below buckets on the tab-separated state column
// instead of the JS source's structured `bucket` field. gh pr checks exits
// 0/1/8 for pass/some-failed/some-pending respectively, so this tool uses
// PRChecksWithExitCode (which preserves stdout across all three) rather than
// PRChecks (which discards stdout on any non-zero exit, masking the
// failed/pending cases behind a generic error). RULING: the JS source's
// fetchFailedCheckLogs (`gh run view <runId> --log-failed`) is NOT ported —
// like received_review.go's documented non-port of fetchPrReviewThreads,
// this is a gh capability ghx does not expose beyond the raw checks text.
// Instead, on a "failed" verdict, this tool includes the raw checks text in
// ext.checks_raw for downstream (LLM) consumers to read the failing check
// names from, without a fetched log excerpt.
// ---------------------------------------------------------------------------

// VerifyPipelineAwaitIn is the input for the verify_pipeline_await tool.
// Zero-value TimeoutSeconds/IntervalSeconds fall back to the JS source's
// parseArgs defaults (1200s timeout, 60s interval).
type VerifyPipelineAwaitIn struct {
	PR              int    `json:"pr"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
	StateFile       string `json:"state_file,omitempty"`
}

// checkResult is one row of ghx.PRChecks' plain-text output, bucketed into
// failed/pending by evaluateChecksText.
type checkResult struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// evaluateChecksText buckets ghx.PRChecks' tab-separated
// "<name>\t<state>\t<elapsed>\t<link>" lines into failed/pending, mirroring
// evaluateChecks' fail/pending/else-green priority (JS source) over gh's
// plain-text state column instead of its structured `bucket` field (see
// this file's verify_pipeline_await doc comment above for why).
func evaluateChecksText(text string) (failed, pending []checkResult) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		state := strings.ToLower(strings.TrimSpace(fields[1]))
		switch state {
		case "fail", "failure", "cancelled", "action_required", "timed_out":
			failed = append(failed, checkResult{Name: name, State: state})
		case "pending", "in_progress", "queued", "requested", "waiting":
			pending = append(pending, checkResult{Name: name, State: state})
		}
	}
	return failed, pending
}

// verifyPipelineAwait implements one KD8 probe of verify_pipeline_await.
func verifyPipelineAwait(activeRoot string, in VerifyPipelineAwaitIn) (stepper.Envelope, error) {
	if in.PR <= 0 {
		return stepper.Envelope{}, &mcpserver.DomainError{Msg: "pr must be a positive integer"}
	}

	timeoutSeconds := in.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 1200
	}
	intervalSeconds := in.IntervalSeconds
	if intervalSeconds <= 0 {
		intervalSeconds = 60
	}

	stateFile := in.StateFile
	st, err := loadOrInitPollState(stateFile, "verify-pipeline", timeoutSeconds, intervalSeconds, &stateFile)
	if err != nil {
		return stepper.Envelope{}, err
	}

	if st.Exhausted {
		return stepper.Done(stateFile, "", map[string]any{
			"verdict":   "skipped",
			"reason":    "exhausted",
			"pr_number": in.PR,
		}), nil
	}

	if st.TimedOut() {
		return timeoutEnvelope(stateFile, st, map[string]any{
			"pr_number": in.PR,
		})
	}

	checksText, exitCode, err := ghx.PRChecksWithExitCode(activeRoot, in.PR)
	if err != nil {
		return stepper.NewError(stateFile, classifyGHError(err)), nil
	}
	if exitCode != 0 && exitCode != 1 && exitCode != 8 {
		return stepper.NewError(stateFile, fmt.Sprintf("gh pr checks: unexpected exit code %d", exitCode)), nil
	}

	failed, pending := evaluateChecksText(checksText)
	if len(failed) > 0 {
		return stepper.Done(stateFile, "", map[string]any{
			"verdict":       "failed",
			"pr_number":     in.PR,
			"failed_checks": failed,
			"checks_raw":    checksText,
		}), nil
	}
	if len(pending) == 0 {
		return stepper.Done(stateFile, "", map[string]any{
			"verdict":   "green",
			"pr_number": in.PR,
		}), nil
	}

	return pendingEnvelope(stateFile, st, map[string]any{
		"pr_number":      in.PR,
		"pending_checks": pending,
	})
}

// ---------------------------------------------------------------------------
// poll_await
//
// Unifies await_remote_review and verify_pipeline_await behind one tool with
// a Target discriminator, since both are KD8 bounded-polling probes that
// return a stepper.Envelope and differ only in what they poll and their
// input/default shape.
// ---------------------------------------------------------------------------

// PollAwaitIn is the input for the unified poll_await tool. Target selects
// which underlying probe runs: "remote_review" (await_remote_review) or
// "pipeline" (verify_pipeline_await). Reviewers is meaningful only for
// "remote_review" and is ignored for "pipeline".
//
// TimeoutSeconds/IntervalSeconds/Reviewers/StateFile are forwarded verbatim
// into the target's own input struct (AwaitRemoteReviewIn or
// VerifyPipelineAwaitIn) rather than defaulted here. This is deliberate:
// the two targets have DIFFERENT zero-value timeout defaults (600s for
// remote_review, 1200s for pipeline), and each one's own core function
// already applies its own default when TimeoutSeconds is zero. Branching
// on Target to pick which core function runs *is* the per-target default
// branch the fact sheet requires — duplicating that fallback logic here
// would risk the two defaults drifting apart.
type PollAwaitIn struct {
	Target          string   `json:"target" jsonschema_description:"Polling target: \"remote_review\" (polls gh for a remote reviewer's verdict) or \"pipeline\" (polls gh PR checks for green/failed/pending)."`
	PR              int      `json:"pr" jsonschema_description:"Pull request number to poll."`
	TimeoutSeconds  int      `json:"timeout_seconds,omitempty" jsonschema_description:"Overall timeout in seconds for the polling operation to be considered done rather than still pending."`
	IntervalSeconds int      `json:"interval_seconds,omitempty" jsonschema_description:"Minimum interval in seconds to wait between probes before reporting pending again."`
	Reviewers       []string `json:"reviewers,omitempty" jsonschema_description:"target=remote_review only: GitHub usernames whose review verdict is being polled for."`
	StateFile       string   `json:"state_file,omitempty" jsonschema_description:"Path to the stepper state file to resume polling from, as returned by a prior pending call."`
}

// pollAwait dispatches a poll_await call to the target's existing core
// probe function, unchanged. Extracted as a standalone function (rather
// than inlined in the Register closure) so it is directly unit-testable.
func pollAwait(activeRoot string, in PollAwaitIn) (stepper.Envelope, error) {
	switch in.Target {
	case "remote_review":
		return awaitRemoteReview(activeRoot, AwaitRemoteReviewIn{
			PR:              in.PR,
			TimeoutSeconds:  in.TimeoutSeconds,
			IntervalSeconds: in.IntervalSeconds,
			Reviewers:       in.Reviewers,
			StateFile:       in.StateFile,
		})
	case "pipeline":
		return verifyPipelineAwait(activeRoot, VerifyPipelineAwaitIn{
			PR:              in.PR,
			TimeoutSeconds:  in.TimeoutSeconds,
			IntervalSeconds: in.IntervalSeconds,
			StateFile:       in.StateFile,
		})
	default:
		return stepper.Envelope{}, &mcpserver.DomainError{
			Msg: fmt.Sprintf(`target must be "remote_review" or "pipeline", got %q`, in.Target),
		}
	}
}

// ---------------------------------------------------------------------------
// Shared KD8 bounded-poll state helpers
// ---------------------------------------------------------------------------

// loadOrInitPollState resolves the resume state for one bounded-poll call:
// if stateFile is empty or fails to load (mirroring the JS source's
// readStateFile, which fails open on any read/parse error and starts over),
// a fresh PollState and a new state file path are created. *stateFile is
// updated in place to the resolved path either way.
func loadOrInitPollState(stateFile, skill string, timeoutSeconds, intervalSeconds int, out *string) (stepper.PollState, error) {
	if stateFile != "" {
		st, err := stepper.LoadPollState(stateFile)
		if err == nil {
			return st, nil
		}
	}
	newPath, err := stepper.NewStateFilePath(skill)
	if err != nil {
		return stepper.PollState{}, &mcpserver.InfraError{
			Msg:   "create resume state file: " + err.Error(),
			Cause: err,
		}
	}
	*out = newPath
	return stepper.NewPollState(skill, timeoutSeconds, intervalSeconds), nil
}

// timeoutEnvelope marks st exhausted, persists it, and returns the "done"
// timeout envelope, mirroring the JS source's R48/R54 timeout branch
// (which sets the *Exhausted state marker before emitting the timeout
// verdict, so a later call short-circuits to "skipped").
func timeoutEnvelope(stateFile string, st stepper.PollState, ext map[string]any) (stepper.Envelope, error) {
	st.Exhausted = true
	if err := stepper.SavePollState(stateFile, st); err != nil {
		return stepper.Envelope{}, &mcpserver.InfraError{Msg: "persist resume state: " + err.Error(), Cause: err}
	}
	ext["verdict"] = "timeout"
	ext["waited_seconds"] = st.WaitedSeconds()
	return stepper.Done(stateFile, "", ext), nil
}

// pendingEnvelope increments st's iteration, persists it, and returns the
// "pending" envelope with the resume state_file for the next call.
func pendingEnvelope(stateFile string, st stepper.PollState, ext map[string]any) (stepper.Envelope, error) {
	st.Iteration++
	if err := stepper.SavePollState(stateFile, st); err != nil {
		return stepper.Envelope{}, &mcpserver.InfraError{Msg: "persist resume state: " + err.Error(), Cause: err}
	}
	progress := map[string]any{
		"iteration":        st.Iteration,
		"waited_seconds":   st.WaitedSeconds(),
		"timeout_seconds":  st.TimeoutSeconds,
		"interval_seconds": st.IntervalSeconds,
	}
	return stepper.Pending(stateFile, progress, ext), nil
}

// ---------------------------------------------------------------------------
// verify_pipeline_classify
//
// Ports scripts/skill/verify-pipeline-classify.js's classifyLogs. This
// tool returns a plain classification payload, not a stepper envelope — the
// Contract in the task fact sheet specifies "-> classification payload" for
// this tool specifically (unlike the two polling tools above).
//
// RULING: the fact sheet's stated contract shape,
// VerifyPipelineClassifyIn{CheckName, Conclusion string, ...}, does not
// match the JS source at all — classifyLogs takes raw log text, and has no
// concept of a check name or conclusion. This port adds a Logs field to
// carry the actual text to classify (the JS source's real input, whether
// piped via stdin or --logs-file), and keeps CheckName/Conclusion as
// passthrough context fields — they match the shape of a
// verify_pipeline_await failed_checks entry ({name, state}), so a caller
// can classify each failed check by feeding its logs text through
// unchanged alongside its identifying fields, and get them echoed back on
// the output for correlation.
// ---------------------------------------------------------------------------

// VerifyPipelineClassifyIn is the input for the verify_pipeline_classify
// tool. See the RULING above for why this differs from the fact sheet's
// stated {CheckName, Conclusion} contract.
type VerifyPipelineClassifyIn struct {
	CheckName  string `json:"check_name,omitempty" jsonschema_description:"Name of the failed CI check, echoed back on the classification result."`
	Conclusion string `json:"conclusion,omitempty" jsonschema_description:"Conclusion reported by the failed CI check (e.g. \"failure\", \"timed_out\"), echoed back on the classification result."`
	Logs       string `json:"logs" jsonschema_description:"Log text from the failed check to classify into lint|test-failure|type-error|build-error|dependency|infra|unknown."`
}

// VerifyPipelineClassifyOut is the classification payload. Category and
// Signals mirror classifyLogs' return value exactly; CheckName/Conclusion
// are passed through from the input unchanged for correlation.
type VerifyPipelineClassifyOut struct {
	CheckName  string   `json:"check_name,omitempty"`
	Conclusion string   `json:"conclusion,omitempty"`
	Category   string   `json:"category"`
	Signals    []string `json:"signals"`
}

// classifyPattern pairs a compiled Go regexp with a label copied verbatim
// from the corresponding JS RegExp's .source (not derived from the Go
// pattern's own String(), which would corrupt the label with Go-specific
// (?i)/(?m) flag prefixes the JS output never had).
type classifyPattern struct {
	re    *regexp.Regexp
	label string
}

var lintPatterns = []classifyPattern{
	{regexp.MustCompile(`(?i)\beslint\b`), `\beslint\b`},
	{regexp.MustCompile(`(?i)\bprettier\b`), `\bprettier\b`},
	{regexp.MustCompile(`(?i)\brubocop\b`), `\brubocop\b`},
	{regexp.MustCompile(`(?i)\bgolangci-lint\b`), `\bgolangci-lint\b`},
	{regexp.MustCompile(`(?i)\bflake8\b`), `\bflake8\b`},
	{regexp.MustCompile(`(?i)\bpylint\b`), `\bpylint\b`},
	{regexp.MustCompile(`(?i)\bproblems?\s+\(?\d+\s+errors?,\s+\d+\s+warnings?\)?`), `\bproblems?\s+\(?\d+\s+errors?,\s+\d+\s+warnings?\)?`},
}

var testFailurePatterns = []classifyPattern{
	{regexp.MustCompile(`(?i)\b\d+\s+failing\b`), `\b\d+\s+failing\b`},
	{regexp.MustCompile(`\bAssertionError\b`), `\bAssertionError\b`},
	{regexp.MustCompile(`(?i)\bexpected\b.*\breceived\b`), `\bexpected\b.*\breceived\b`},
	{regexp.MustCompile(`FAIL\s+[\w./-]+\.(test|spec)\.[jt]sx?`), `FAIL\s+[\w./-]+\.(test|spec)\.[jt]sx?`},
	{regexp.MustCompile(`(?i)Tests?:\s*\d+\s+failed`), `Tests?:\s*\d+\s+failed`},
	{regexp.MustCompile(`(?m)^\s*FAILED\s+tests`), `^\s*FAILED\s+tests`},
	{regexp.MustCompile(`(?i)pytest:.*failed`), `pytest:.*failed`},
}

var typePatterns = []classifyPattern{
	{regexp.MustCompile(`\bTS\d{4}\b`), `\bTS\d{4}\b`},
	{regexp.MustCompile(`(?i)Type\s+'.*'\s+is\s+not\s+assignable`), `Type\s+'.*'\s+is\s+not\s+assignable`},
	{regexp.MustCompile(`(?i)Property\s+'.*'\s+does\s+not\s+exist\s+on\s+type`), `Property\s+'.*'\s+does\s+not\s+exist\s+on\s+type`},
	{regexp.MustCompile(`(?i)\bmypy\b`), `\bmypy\b`},
	{regexp.MustCompile(`(?i)\btsc\b.*\berror\b`), `\btsc\b.*\berror\b`},
}

var buildPatterns = []classifyPattern{
	{regexp.MustCompile(`(?i)Cannot\s+find\s+module\b`), `Cannot\s+find\s+module\b`},
	{regexp.MustCompile(`(?i)Module\s+not\s+found`), `Module\s+not\s+found`},
	{regexp.MustCompile(`SyntaxError:`), `SyntaxError:`},
	{regexp.MustCompile(`(?i)webpack\s+\d+\s+errors`), `webpack\s+\d+\s+errors`},
	{regexp.MustCompile(`(?i)rollup\s+failed`), `rollup\s+failed`},
	{regexp.MustCompile(`(?i)esbuild.*error`), `esbuild.*error`},
}

var depPatterns = []classifyPattern{
	{regexp.MustCompile(`(?i)\bnpm\s+ERR!\s+code\s+E\w+`), `\bnpm\s+ERR!\s+code\s+E\w+`},
	{regexp.MustCompile(`(?i)\bENOENT\b.*node_modules`), `\bENOENT\b.*node_modules`},
	{regexp.MustCompile(`(?i)\bpeer\s+dep\b`), `\bpeer\s+dep\b`},
	{regexp.MustCompile(`(?i)\bunable\s+to\s+resolve\s+dependency\b`), `\bunable\s+to\s+resolve\s+dependency\b`},
	{regexp.MustCompile(`(?i)\byarn\s+install.*failed`), `\byarn\s+install.*failed`},
	{regexp.MustCompile(`(?i)\bpip\s+install.*ERROR\b`), `\bpip\s+install.*ERROR\b`},
}

var infraPatterns = []classifyPattern{
	{regexp.MustCompile(`(?i)\bRunner\s+lost\s+communication\b`), `\bRunner\s+lost\s+communication\b`},
	{regexp.MustCompile(`(?i)\btime[ -]?out(ed)?\b`), `\btime[ -]?out(ed)?\b`},
	{regexp.MustCompile(`(?i)\bunable\s+to\s+access\s+'https?://`), `\bunable\s+to\s+access\s+'https?:\/\/`},
	{regexp.MustCompile(`(?i)\b502\s+Bad\s+Gateway\b`), `\b502\s+Bad\s+Gateway\b`},
	{regexp.MustCompile(`(?i)\b503\s+Service\s+Unavailable\b`), `\b503\s+Service\s+Unavailable\b`},
	{regexp.MustCompile(`\bExitCode:\s+143\b`), `\bExitCode:\s+143\b`},
}

// ClassifyLogs is the direct port of classifyLogs (verify-pipeline-classify.js).
// It collects every matching signal across all six pattern groups
// unconditionally (not just the winning category's group — matching the JS
// source, whose `signals` array reflects every pattern that matched
// regardless of which category is ultimately chosen), then resolves the
// category by priority: lint > test-failure > type-error > build-error >
// dependency > infra > unknown.
func ClassifyLogs(text string) VerifyPipelineClassifyOut {
	signals := []string{}
	if strings.TrimSpace(text) == "" {
		return VerifyPipelineClassifyOut{Category: "unknown", Signals: signals}
	}

	collect := func(patterns []classifyPattern, prefix string) {
		for _, p := range patterns {
			if p.re.MatchString(text) {
				signals = append(signals, prefix+":"+p.label)
			}
		}
	}
	collect(lintPatterns, "lint")
	collect(testFailurePatterns, "test")
	collect(typePatterns, "type")
	collect(buildPatterns, "build")
	collect(depPatterns, "dep")
	collect(infraPatterns, "infra")

	has := func(prefix string) bool {
		for _, s := range signals {
			if strings.HasPrefix(s, prefix) {
				return true
			}
		}
		return false
	}

	category := "unknown"
	switch {
	case has("lint:"):
		category = "lint"
	case has("test:"):
		category = "test-failure"
	case has("type:"):
		category = "type-error"
	case has("build:"):
		category = "build-error"
	case has("dep:"):
		category = "dependency"
	case has("infra:"):
		category = "infra"
	}

	return VerifyPipelineClassifyOut{Category: category, Signals: signals}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterPollingTools registers poll_await and verify_pipeline_classify on
// the server. Registration-only: wiring these into runMCP's live server is
// Task 40's job, matching the pattern already established for every tool
// registered so far.
func RegisterPollingTools(s *mcpserver.Server) {
	mcpserver.Register(s, "poll_await",
		`INTERNAL — called by sdlc skills only. Run one bounded KD8 probe for a polling target: target: "remote_review" polls gh for a remote reviewer's verdict on a PR; target: "pipeline" polls gh PR checks for green/failed/pending. One non-blocking probe per call. Returns a stepper envelope (status pending + state_file to resume, or status done/error with the verdict in ext).`,
		mcpserver.Annotations{
			Title:       "Await CI or PR completion",
			ReadOnly:    false,
			Destructive: true,
			Idempotent:  false,
			OpenWorld:   true,
		},
		func(ctx mcpserver.Ctx, in PollAwaitIn) (stepper.Envelope, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return stepper.Envelope{}, &mcpserver.InfraError{Msg: "resolve project root: " + err.Error(), Cause: err}
			}
			activeRoot, err := worktree.ActiveRoot()
			if err != nil {
				activeRoot = root
			}
			return pollAwait(activeRoot, in)
		},
	)

	mcpserver.Register(s, "verify_pipeline_classify",
		"INTERNAL — called by sdlc skills only. Classify failed-check log text into lint|test-failure|type-error|build-error|dependency|infra|unknown.",
		mcpserver.Annotations{
			Title:      "Classify CI failure logs",
			ReadOnly:   true,
			Idempotent: true,
			OpenWorld:  false,
		},
		func(ctx mcpserver.Ctx, in VerifyPipelineClassifyIn) (VerifyPipelineClassifyOut, error) {
			out := ClassifyLogs(in.Logs)
			out.CheckName = in.CheckName
			out.Conclusion = in.Conclusion
			return out, nil
		},
	)
}
