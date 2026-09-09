package tools

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

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
// RULING: ghx has no REST/GraphQL PR-reviews endpoint (internal/ghx/ghx.go
// only wraps `gh pr view`/`gh pr checks`/plain-text commands — see Task 29's
// received_review.go precedent for the same gap on review threads). Rather
// than reconstructing the JS source's structured review-state parsing via a
// new direct `gh api ... --jq ...` call (which would duplicate ghx's own
// binary-not-found classification outside of ghx), this port evaluates
// ghx.PRView's plain-text output with evaluateReviewText below. This is a
// heuristic over unstructured text, not the JS source's exact
// APPROVED/COMMENTED/CHANGES_REQUESTED/PENDING REST states — see
// evaluateReviewText's doc comment for the exact matching rule and its
// known limitations.
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

// reviewerPattern returns a regexp fragment matching the raw login forms
// gh's plain-text output may render for a configured reviewer, mirroring
// evaluateReviews' (R56) [bot]-suffix strip and copilot-variant
// canonicalization from the JS source — but applied as a text pattern
// instead of a structured-field comparison, since ghx has no structured
// reviews surface.
func reviewerPattern(login string) string {
	if strings.EqualFold(login, "copilot") {
		return `copilot(?:-pull-request-reviewer)?(?:\[bot\])?`
	}
	return regexp.QuoteMeta(login) + `(?:\[bot\])?`
}

// evaluateReviewText is a best-effort port of evaluateReviews (R51-R53,
// R56) that works over `gh pr view`'s plain-text output instead of the JS
// source's structured REST review list, because ghx.PRView is the only gh
// surface available for PR reviews (no --json/GraphQL counterpart — see
// this file's package doc comment above).
//
// It looks for "<reviewer login> (<state>)" — the shape gh pr view prints
// in its "reviewers:" summary line — and maps the parenthesized state to
// the JS source's verdict buckets:
//
//	(Approved)                          -> "approved-clean"
//	(Commented) / (Changes requested) /
//	  (Requested changes)               -> "actionable"
//	anything else (e.g. a bare pending
//	  reviewer with no parenthetical, or
//	  an unrecognized state)            -> not matched (still pending)
//
// Reviewers are checked in the order given; the first match wins. This
// does not reproduce the JS source's submittedAt-based "pick the latest
// review" tie-break (gh's plain-text summary only shows each reviewer's
// current state once, not per-review history), and does not enforce the
// JS source's authorType === 'Bot' guard on the copilot login (that field
// is not present in plain text either). Both are documented simplifications
// consistent with this tool's text-heuristic approach.
func evaluateReviewText(text string, reviewers []string) (status, reviewer, rawState string) {
	for _, r := range reviewers {
		if strings.TrimSpace(r) == "" {
			continue
		}
		re := regexp.MustCompile(`(?i)\b` + reviewerPattern(r) + `\s*\(([^)]*)\)`)
		m := re.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		state := strings.ToLower(strings.TrimSpace(m[1]))
		switch state {
		case "approved":
			return "approved-clean", r, m[1]
		case "commented", "changes requested", "requested changes":
			return "actionable", r, m[1]
		}
	}
	return "", "", ""
}

// awaitRemoteReview implements one KD8 probe of await_remote_review.
func awaitRemoteReview(activeRoot string, in AwaitRemoteReviewIn) (stepper.Envelope, error) {
	if in.PR <= 0 {
		return stepper.Envelope{}, &mcpserver.DomainError{Msg: "pr must be a positive integer"}
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

	if st.TimedOut() {
		return timeoutEnvelope(stateFile, st, map[string]any{
			"reviewers": reviewers,
			"pr_number": in.PR,
		})
	}

	view, err := ghx.PRView(activeRoot, in.PR)
	if err != nil {
		return stepper.NewError(stateFile, classifyGHError(err)), nil
	}

	status, reviewer, rawState := evaluateReviewText(view, reviewers)
	if status != "" {
		return stepper.Done(stateFile, "", map[string]any{
			"verdict":   status,
			"reviewer":  reviewer,
			"state":     rawState,
			"pr_number": in.PR,
		}), nil
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
	Target          string   `json:"target"`
	PR              int      `json:"pr"`
	TimeoutSeconds  int      `json:"timeout_seconds,omitempty"`
	IntervalSeconds int      `json:"interval_seconds,omitempty"`
	Reviewers       []string `json:"reviewers,omitempty"`
	StateFile       string   `json:"state_file,omitempty"`
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
	CheckName  string `json:"check_name,omitempty"`
	Conclusion string `json:"conclusion,omitempty"`
	Logs       string `json:"logs"`
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
		func(ctx mcpserver.Ctx, in VerifyPipelineClassifyIn) (VerifyPipelineClassifyOut, error) {
			out := ClassifyLogs(in.Logs)
			out.CheckName = in.CheckName
			out.Conclusion = in.Conclusion
			return out, nil
		},
	)
}
