package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/hardensurfaces"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/pipeline"
	"github.com/rnagrodzki/sdlc-plugin/internal/shipmeta"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// Input / Output types
// ---------------------------------------------------------------------------

// ShipStateIn is the merged input for the ship_state tool's 16 actions (14
// mirroring scripts/state/ship.js's subcommands: init, start, complete,
// begin-step, complete-step, skip, fail, decide, defer, read, cleanup,
// cleanup-pipeline, gc, migrate; plus the Go-native next and todos actions).
// Action-specific parameters travel in Detail rather than as named fields,
// per the task-35 contract — every JS opts field (branch, flags, stateFile,
// result, outcome, reason, error, text, severity, file, line, title,
// ttlDays, dryRun, force, from, to) is read out of Detail by the relevant
// action handler via key-presence checks. That is also how the *int-
// equivalent nil-vs-zero distinction for ttlDays, and the undefined-vs-
// empty-string distinction for reason/error/result, are preserved without
// literal typed struct fields (see detailIntPtr below).
type ShipStateIn struct {
	Action    string         `json:"action" jsonschema:"enum=init,enum=begin-step,enum=complete-step,enum=commit-check,enum=start,enum=complete,enum=skip,enum=fail,enum=decide,enum=defer,enum=read,enum=next,enum=todos,enum=cleanup,enum=cleanup-pipeline,enum=gc,enum=migrate,enum=history_record,enum=deferred_add,enum=deferred_list,enum=deferred_propose_followups,enum=deferred_resolve,enum=log-cli,enum=healing_record,enum=harden_clusters,enum=report" jsonschema_description:"Operation to perform: init, begin-step, complete-step, commit-check, start (legacy), complete (legacy), skip, fail, decide, defer, healing_record, harden_clusters, report, read, next, todos, cleanup, cleanup-pipeline, gc, migrate, history_record, deferred_add, deferred_list, deferred_propose_followups, deferred_resolve, or log-cli. Each action uses a subset of the other fields (unlisted fields are ignored)."`
	Step      string         `json:"step,omitempty" jsonschema_description:"Pipeline step name. Required by begin-step, complete-step, start, complete, skip, fail, decide; ignored by other actions."`
	Detail    map[string]any `json:"detail,omitempty" jsonschema_description:"Action-specific extra fields (e.g. branch, flags, outcome, result, reason, description, error, text, severity, file, title, line, force, ttlDays, dryRun, from, to, detail; log-cli reads branch, command, exitCode, outputHead, step; healing_record reads kind, total, dimensions, origin, severity, file, line, title, status, phase, trigger, classification, applied, skipped, branch; harden_clusters reads findings, branch; report reads write, format, branch). See the action list for which sub-fields each action reads."`
	SessionID string         `json:"sessionId,omitempty" jsonschema_description:"Session identifier used by init to stamp the created state's sessionId field, for correlating this run with the calling session."`
}

// ShipTodosOut is the output shape for the Go-native todos action. Mutating
// actions (begin-step, complete-step, skip, fail, decide, defer) now return
// ShipStepNarrationOut instead.
type ShipTodosOut struct {
	Todos []shipmeta.Todo `json:"todos"`
	// IssueCount and IssueHighlights are populated (complete-step only) when
	// state.Data["issues"] is nonempty. Response-only — never persisted.
	IssueCount      int      `json:"issueCount,omitempty"`
	IssueHighlights []string `json:"issueHighlights,omitempty"`
}

// ShipStepNarrationOut is the narrated output for mutating ship_state actions
// (begin-step, complete-step, skip, fail, decide, defer, and the legacy
// start/complete pair). It embeds pipeline.Narration at the top level (JSON
// fields: summary, display, timing, next) alongside the Todos/IssueCount/
// IssueHighlights fields carried by begin-step and complete-step, so existing
// consumers that read those fields see no change.
type ShipStepNarrationOut struct {
	pipeline.Narration
	Todos           []shipmeta.Todo `json:"todos,omitempty"`
	IssueCount      int             `json:"issueCount,omitempty"`
	IssueHighlights []string        `json:"issueHighlights,omitempty"`
	// AlreadyDone is true when begin-step found a verified sideEffects
	// journal entry for this step already (see ship.go's
	// shipVerifySideEffect/shipRecordSideEffect) — signaling a resumed
	// pipeline that the step's side effect landed before a crash/restart,
	// so its work need not be redone. omitempty: the common case (no prior
	// verification) should not clutter every begin-step response with
	// alreadyDone:false.
	AlreadyDone bool `json:"alreadyDone,omitempty"`
	// Warnings names a best-effort write that failed without failing the
	// action: fail sets it when the failure row could not be appended to
	// runs.jsonl. Response-only — never persisted. omitempty: most calls have
	// nothing to warn about.
	Warnings []string `json:"warnings,omitempty"`
}

// ShipNextOut is the output of the Go-native next action: the first step
// that still needs work (in_progress, failed, or a bare pending step with no
// condition key — the same predicate as begin-step's R-b1 proceed-gate,
// walked over the whole steps[] array instead of a slice up to some index)
// and its resolved automation mode (config.AutomationSection.StepMode).
// Step is "" when every step is terminal-OK (pipeline complete) — no error,
// since "nothing left to do" is an expected steady state for an executor
// loop, not a failure.
type ShipNextOut struct {
	Step       string `json:"step,omitempty"`
	Automation string `json:"automation,omitempty"`
}

// ShipStateGCReport mirrors cmdGc's real-run JSON output shape exactly:
// {ttlDays, ship, execute, plan, commit} — no exploreTempdirs field. This is
// deliberately NOT the same type as ship.go's ShipGCReport (used by
// ship_prepare's --gc branch), which additionally reports exploreTempdirs;
// cmdGc's own JSON output has no such field even though the underlying
// state.GC call still sweeps tempdirs as an unavoidable side effect — the
// same documented tension execActionGC already accepts (state.GC has no
// gcTempdirs-optional mode to suppress the sweep).
type ShipStateGCReport struct {
	TTLDays int          `json:"ttlDays"`
	Ship    ShipGCBucket `json:"ship"`
	Execute ShipGCBucket `json:"execute"`
	Plan    ShipGCBucket `json:"plan"`
	Commit  ShipGCBucket `json:"commit"`
}

// ---------------------------------------------------------------------------
// Detail extraction helpers
// ---------------------------------------------------------------------------

func detailStr(d map[string]any, key string) string {
	if v, _ := d[key].(string); v != "" {
		return v
	}
	return ""
}

func detailBool(d map[string]any, key string) bool {
	v, _ := d[key].(bool)
	return v
}

// shipDetailString reads an optional string out of Detail and fails loud on
// a wrong-typed value instead of silently reading it as absent. detailStr
// yields "" for any non-string, which for an optional field is
// indistinguishable from "omitted" — so `detail.reason: 5` would quietly
// record the omitted-reason default while the caller believes it passed a
// reason. A JSON null is treated as omitted (same convention as
// detailIntPtr), since that is how a caller spells "no value".
func shipDetailString(d map[string]any, action, key, suggestion string) (string, error) {
	v, ok := d[key]
	if !ok || v == nil {
		return "", nil
	}
	s, isStr := v.(string)
	if !isStr {
		return "", &mcpserver.DomainError{
			Msg: fmt.Sprintf("%s: detail.%s must be a string, got %T", action, key, v),
			// The literal prefix states the type rule that every caller
			// shares; the caller's own suggestion names the accepted
			// values. Keeping the prefix inline (not a parameter) is what
			// mcp-error-suggestion-coverage checks for.
			Suggestion: fmt.Sprintf("Pass detail.%s as a JSON string, or omit the key entirely. %s", key, suggestion),
		}
	}
	return s, nil
}

// shipDetailBool reads an optional boolean out of Detail and fails loud on
// a wrong-typed value, for the same reason shipDetailString does: detailBool
// yields false for any non-bool (the string "true" or the number 1
// included), which is indistinguishable from "omitted" — so `detail.write:
// "true"` would quietly skip the write while the caller believes it asked
// for one. A JSON null is treated as omitted.
func shipDetailBool(d map[string]any, action, key, suggestion string) (bool, error) {
	v, ok := d[key]
	if !ok || v == nil {
		return false, nil
	}
	b, isBool := v.(bool)
	if !isBool {
		return false, &mcpserver.DomainError{
			Msg: fmt.Sprintf("%s: detail.%s must be a boolean, got %T", action, key, v),
			// Literal prefix inline, caller's suggestion appended — same
			// shape as shipDetailString (mcp-error-suggestion-coverage).
			Suggestion: fmt.Sprintf("Pass detail.%s as the JSON boolean true or false (not a string or number), or omit the key entirely. %s", key, suggestion),
		}
	}
	return b, nil
}

// detailIntPtr extracts an optional integer from Detail, distinguishing
// "absent" (nil) from "explicitly present, including zero" (non-nil) — the
// *int-equivalent shape the ttlDays contract requires (pitfall #2: this is
// the third call site in this codebase needing this exact distinction,
// after state.GCOptions.TTL's own doc contract and execute_state's TTLDays
// *int field), expressed here as a map key-presence check instead of a
// struct tag. Detail values decode from JSON, so numbers surface as
// float64; a bare int is also accepted for direct (non-JSON-roundtripped)
// test fixtures.
func detailIntPtr(d map[string]any, key string) *int {
	v, ok := d[key]
	if !ok || v == nil {
		return nil
	}
	switch n := v.(type) {
	case float64:
		i := int(n)
		return &i
	case int:
		return &n
	}
	return nil
}

// ---------------------------------------------------------------------------
// Step-array helpers (data["steps"] is []any of map[string]any, per state.State.Data)
// ---------------------------------------------------------------------------

func shipStepsSlice(data map[string]any) []any {
	steps, _ := data["steps"].([]any)
	return steps
}

// shipFindStepIndex returns the steps[] index that an action on name
// targets, or -1 when no entry has that name. A name can occur more than
// once in a hand-built state (ship_prepare never writes one: table keys
// are unique and a duplicate --steps name is an error), so the target is
// the first entry with that name whose status is not completed or skipped.
// When every entry with that name is completed or skipped, the target is
// the first entry with that name.
func shipFindStepIndex(data map[string]any, name string) int {
	first := -1
	for i, s := range shipStepsSlice(data) {
		sm, ok := s.(map[string]any)
		if !ok || sm["name"] != name {
			continue
		}
		if first < 0 {
			first = i
		}
		if status, _ := sm["status"].(string); status != "completed" && status != "skipped" {
			return i
		}
	}
	return first
}

// shipFindStepEntry returns the steps[] entry that an action on name
// targets (see shipFindStepIndex), or nil when no entry has that name.
func shipFindStepEntry(data map[string]any, name string) map[string]any {
	i := shipFindStepIndex(data, name)
	if i < 0 {
		return nil
	}
	sm, _ := shipStepsSlice(data)[i].(map[string]any)
	return sm
}

// shipSideEffectKey returns the sideEffects journal key for the steps[]
// entry that an action on step targets: step for the first entry with
// that name, "<step>#<n>" for the n-th (n >= 2). With no steps[] entry for
// the name, it returns step.
func shipSideEffectKey(data map[string]any, step string) string {
	idx := shipFindStepIndex(data, step)
	if idx < 0 {
		return step
	}
	n := 0
	for _, s := range shipStepsSlice(data)[:idx+1] {
		if sm, ok := s.(map[string]any); ok && sm["name"] == step {
			n++
		}
	}
	if n <= 1 {
		return step
	}
	return fmt.Sprintf("%s#%d", step, n)
}

// shipStepAlreadyDone reports whether data["sideEffects"] already holds a
// verified journal entry for the steps[] entry that step targets (written
// by ship.go's shipVerifySideEffect/shipRecordSideEffect). Used by
// begin-step to signal a resumed pipeline that this step's side effect
// already landed.
func shipStepAlreadyDone(data map[string]any, step string) bool {
	_, ok := shipSideEffectEntry(data, shipSideEffectKey(data, step))
	return ok
}

// shipStepBlocksProceed implements the R-b1 proceed-gate predicate from
// ship.js (cmdBeginStep): a step blocks progress past it unless it is
// terminal-OK. `skipped` is terminal-OK. A `pending` step blocks UNLESS it
// carries a `condition` key (conditional-by-design steps like
// received-review/commit-fixes rest at pending when their trigger never
// fired — that is their expected steady state, not a stall). Any other
// non-terminal status (`in_progress`, `failed`) always blocks.
func shipStepBlocksProceed(step map[string]any) bool {
	status, _ := step["status"].(string)
	if status == "pending" {
		_, hasCondition := step["condition"]
		return !hasCondition
	}
	return status == "in_progress" || status == "failed"
}

// ---------------------------------------------------------------------------
// Narration helpers
// ---------------------------------------------------------------------------

// shipBuildStepRows converts the state's steps[] array into pipeline.StepRow
// slices for rendering by StepProgressBlock.
func shipBuildStepRows(data map[string]any) []pipeline.StepRow {
	steps := shipStepsSlice(data)
	rows := make([]pipeline.StepRow, 0, len(steps))
	for _, s := range steps {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		name, _ := sm["name"].(string)
		status, _ := sm["status"].(string)
		startedAt, _ := sm["startedAt"].(string)
		completedAt, _ := sm["completedAt"].(string)
		rows = append(rows, pipeline.StepRow{
			Name:        name,
			Status:      status,
			StartedAt:   startedAt,
			CompletedAt: completedAt,
		})
	}
	return rows
}

// shipStepPosition returns the 1-based index and total count of steps for
// the steps[] entry that an action on stepName targets (see
// shipFindStepIndex). Returns (0, total) if the step is not found.
func shipStepPosition(data map[string]any, stepName string) (pos, total int) {
	return shipFindStepIndex(data, stepName) + 1, len(shipStepsSlice(data))
}

// shipFirstBlockingStep returns the name of the first step in the pipeline
// that blocks progress (same predicate as the R-b1 proceed-gate in
// shipStateNext). Returns "" when no blocking step remains.
func shipFirstBlockingStep(data map[string]any) string {
	for _, s := range shipStepsSlice(data) {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if shipStepBlocksProceed(sm) {
			name, _ := sm["name"].(string)
			return name
		}
	}
	return ""
}

// shipPrevCompletedAt returns the completedAt timestamp of the last step
// before steps[idx] that has one, or "" if none or idx is out of range.
func shipPrevCompletedAt(data map[string]any, idx int) string {
	steps := shipStepsSlice(data)
	if idx < 0 || idx > len(steps) {
		return ""
	}
	var prev string
	for _, s := range steps[:idx] {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if ca, _ := sm["completedAt"].(string); ca != "" {
			prev = ca
		}
	}
	return prev
}

// shipDetailLevel returns the detail level from the input's Detail map.
// Must only be called after the dispatcher's detail-value validation.
func shipDetailLevel(in ShipStateIn) string {
	if detailStr(in.Detail, "detail") == "concise" {
		return "concise"
	}
	return "full"
}

// shipStepInstruction returns the dispatch instruction for a step.
func shipStepInstruction(step string) string {
	return fmt.Sprintf("Dispatch the %s sub-skill.", step)
}

// shipCompletionTiming computes timing info for the completed step at
// steps[idx], records the step duration to TimingsStore (except for
// HumanWaitSteps), and returns both the timing and the store for reuse in
// ETA lookups.
func shipCompletionTiming(root string, data map[string]any, idx int, stepName, startedAt, completedAt string, now time.Time) (*pipeline.TimingInfo, *pipeline.TimingsStore) {
	ts := pipeline.NewTimingsStore(root)
	stepDur, ok := pipeline.Duration(startedAt, completedAt)
	if !ok {
		return nil, ts
	}
	timing := &pipeline.TimingInfo{
		StepSeconds: int(stepDur.Round(time.Second).Seconds()),
	}
	if !pipeline.HumanWaitSteps[stepName] {
		_ = ts.Record("ship:"+stepName, stepDur)
	}
	if pipelineStartedAt, _ := data["startedAt"].(string); pipelineStartedAt != "" {
		if pStart, parseErr := time.Parse(time.RFC3339, pipelineStartedAt); parseErr == nil {
			timing.PipelineSeconds = int(now.Sub(pStart).Round(time.Second).Seconds())
		}
	}
	prevCA := shipPrevCompletedAt(data, idx)
	if idle, idleOK := pipeline.IdleGap(prevCA, startedAt); idleOK {
		timing.IdleSeconds = int(idle.Round(time.Second).Seconds())
	}
	timing.Human = "step " + pipeline.Humanize(time.Duration(timing.StepSeconds)*time.Second)
	if timing.PipelineSeconds > 0 {
		timing.Human += ", pipeline " + pipeline.Humanize(time.Duration(timing.PipelineSeconds)*time.Second)
	}
	return timing, ts
}

// shipBuildNextAction builds a NextAction for the first blocking step in
// the pipeline, or nil when no blocking step remains.
func shipBuildNextAction(data map[string]any, ts *pipeline.TimingsStore) *pipeline.NextAction {
	nextName := shipFirstBlockingStep(data)
	if nextName == "" {
		return nil
	}
	next := &pipeline.NextAction{
		ID:          nextName,
		Instruction: shipStepInstruction(nextName),
	}
	if ts != nil {
		if est, ok := ts.Estimate("ship:" + nextName); ok {
			next.EtaSeconds = est.Seconds
			next.EtaBasis = est.Basis
		}
	}
	return next
}

// ---------------------------------------------------------------------------
// Branch / state resolution helpers
// ---------------------------------------------------------------------------

// errNoShipState is the Cause behind shipFindState's "no ship state found"
// DataError. Wrapping it lets a caller detect this specific, expected case
// with errors.Is instead of matching on Msg text, which would break the
// moment the message is reworded.
var errNoShipState = errors.New("no ship state found for branch")

func shipFindState(root, branch string) (*state.State, error) {
	st, err := state.Find(root, "ship", branch)
	if err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("find ship state for branch %q: %s", branch, err.Error()),
			Suggestion: "Check read permission on " + paths.DataDir + "/runs/ship-<branch-slug>-*.json and that the directory exists, then retry.",
			Cause:      err,
		}
	}
	if st == nil {
		return nil, &mcpserver.DataError{
			Msg:        fmt.Sprintf("no ship state found for branch %q", branch),
			Suggestion: "Run ship_state init with detail.branch set to this branch before calling other ship_state actions.",
			Cause:      errNoShipState,
		}
	}
	return st, nil
}

func shipResolveAndFind(branchIn, workDir, root string) (*state.State, error) {
	branch, err := execResolveBranch(branchIn, workDir)
	if err != nil {
		return nil, err
	}
	return shipFindState(root, branch)
}

// shipLoadState implements loadShipStateOrExit's dual resolution path: a
// caller-supplied --state-file wins outright (read directly, no branch
// resolution at all), otherwise fall back to branch resolve + state.Find.
// The direct-file State value has an empty Prefix/BranchSlug; state.Write's
// prune-on-write loop only prunes files whose parsed prefix+slug equals the
// State's own (never true for an empty prefix, since parseStateFilename
// never yields one), so persisting through state.Write is safe either way.
func shipLoadState(root, workDir string, in ShipStateIn) (*state.State, error) {
	stateFile := detailStr(in.Detail, "stateFile")
	if stateFile == "" {
		return shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	}

	var data map[string]any
	if err := fsx.ReadJSON(stateFile, &data); err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			return nil, &mcpserver.DataError{
				Msg:        fmt.Sprintf("state file not found: %s", stateFile),
				Suggestion: "Pass an existing state file path in detail.stateFile, or omit stateFile to resolve by branch instead.",
				Cause:      err,
			}
		}
		if errors.Is(err, fsx.ErrParse) {
			return nil, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("failed to parse state file %s: %s", stateFile, err.Error()),
				Suggestion: "Fix the JSON syntax in the state file named above, or delete it and re-run ship_state init to regenerate it.",
				Cause:      err,
			}
		}
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read state file %s: %s", stateFile, err.Error()),
			Suggestion: "Check read permission on the state file path passed as detail.stateFile, then retry.",
			Cause:      err,
		}
	}
	return &state.State{Path: stateFile, Root: root, Data: data}, nil
}

// ---------------------------------------------------------------------------
// Step mutation cores (mirror startStepCore / completeStepCore in ship.js)
// ---------------------------------------------------------------------------

func shipStartStepCore(data map[string]any, stepName string, now func() time.Time) error {
	step := shipFindStepEntry(data, stepName)
	if step == nil {
		return &mcpserver.DataError{
			Msg:        fmt.Sprintf("step %q not found in state", stepName),
			Suggestion: "Run ship_state read and pass one of the step names listed under steps, then retry.",
		}
	}
	step["status"] = "in_progress"
	step["startedAt"] = now().UTC().Format(time.RFC3339)
	return nil
}

// shipCompleteStepCore mutates the named step to completed (outcome ==
// "success", the default) or failed (outcome == "failure"), matching
// completeStepCore in ship.js exactly, including the outcome=="failure"
// branch storing the result payload under step["error"] rather than
// step["result"]. hasResult/resultVal implement the "result !== undefined"
// check via Detail key-presence — an explicit empty string/zero/false is a
// legal value that must still be stored, so this cannot be a truthiness
// check on resultVal alone.
func shipCompleteStepCore(data map[string]any, stepName string, hasResult bool, resultVal any, outcome string, now func() time.Time) error {
	step := shipFindStepEntry(data, stepName)
	if step == nil {
		return &mcpserver.DataError{
			Msg:        fmt.Sprintf("step %q not found in state", stepName),
			Suggestion: "Run ship_state read to list the step names recorded in the state file, then pass one of those names.",
		}
	}
	if outcome == "failure" {
		step["status"] = "failed"
		if hasResult {
			step["error"] = resultVal
		}
		return nil
	}
	step["status"] = "completed"
	step["completedAt"] = now().UTC().Format(time.RFC3339)
	if hasResult {
		step["result"] = resultVal
	}
	return nil
}

// ---------------------------------------------------------------------------
// Pipeline contract validation (mirrors validatePipelineContract in ship.js)
// ---------------------------------------------------------------------------

type shipContractViolation struct {
	Step         string `json:"step"`
	ActualStatus string `json:"actualStatus"`
	Message      string `json:"message"`
}

// shipValidatePipelineContract ports validatePipelineContract from ship.js:
// in_progress is always a violation; pending is a violation only when the
// step has no condition key (a conditional step's pending is its expected
// resting state, not a violation). failed is never flagged — cleanup only
// blocks on steps that never reached ANY terminal state.
func shipValidatePipelineContract(data map[string]any) (bool, []shipContractViolation) {
	var violations []shipContractViolation
	for _, s := range shipStepsSlice(data) {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		status, _ := sm["status"].(string)
		name, _ := sm["name"].(string)
		_, hasCondition := sm["condition"]
		if status == "in_progress" || (status == "pending" && !hasCondition) {
			violations = append(violations, shipContractViolation{
				Step:         name,
				ActualStatus: status,
				Message:      fmt.Sprintf("step %q has status %q — expected completed, skipped, or failed", name, status),
			})
		}
	}
	return len(violations) == 0, violations
}

func shipFormatViolations(violations []shipContractViolation) string {
	parts := make([]string, len(violations))
	for i, v := range violations {
		parts[i] = fmt.Sprintf("%s=%s", v.Step, v.ActualStatus)
	}
	return strings.Join(parts, ", ")
}

// ---------------------------------------------------------------------------
// Dispatcher
// ---------------------------------------------------------------------------

// shipState validates detail.detail for the narrated actions, then routes
// in.Action to its handler. Ship state is read from and written to root;
// git commands and branch resolution run in workDir. An unknown action
// returns a DomainError that lists the valid actions.
func shipState(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	// Validate detail level for mutating actions.
	switch in.Action {
	case "begin-step", "complete-step", "start", "complete", "skip", "fail", "decide", "defer":
		if v := detailStr(in.Detail, "detail"); v != "" && v != "full" && v != "concise" {
			return nil, &mcpserver.DomainError{
				Msg:        fmt.Sprintf(`%s: detail must be "concise" or "full", got %q; pass detail.detail="concise" or omit for default "full"`, in.Action, v),
				Suggestion: "Pass detail.detail as the string \"concise\" or \"full\" only, or omit the field entirely to use the default.",
			}
		}
	}

	switch in.Action {
	case "init":
		return shipStateInit(root, workDir, in, now)
	case "start":
		return shipStateStart(root, workDir, in, now)
	case "complete":
		return shipStateComplete(root, workDir, in, now)
	case "begin-step":
		return shipStateBeginStep(root, workDir, in, now)
	case "complete-step":
		return shipStateCompleteStep(root, workDir, in, now)
	case "commit-check":
		return shipStateCommitCheck(root, workDir, in, now)
	case "skip":
		return shipStateSkip(root, workDir, in, now)
	case "fail":
		return shipStateFail(root, workDir, in, now)
	case "decide":
		return shipStateDecide(root, workDir, in, now)
	case "defer":
		return shipStateDefer(root, workDir, in, now)
	case "read":
		return shipStateRead(root, workDir, in, now)
	case "cleanup":
		return shipStateCleanup(root, workDir, in, now)
	case "cleanup-pipeline":
		return shipStateCleanupPipeline(root, workDir, in, now)
	case "gc":
		return shipStateGC(root, workDir, in, now)
	case "migrate":
		return shipStateMigrate(root, in)
	case "next":
		return shipStateNext(root, workDir, in)
	case "todos":
		return shipStateTodos(root, workDir, in)

	// History actions — persistent JSONL store that survives state-file GC.
	case "history_record":
		return shipStateHistoryRecord(root, in)
	case "deferred_add":
		return shipStateDeferredAdd(root, in)
	case "deferred_list":
		return shipStateDeferredList(root)
	case "deferred_propose_followups":
		return shipStateDeferredProposeFollowups(root)
	case "deferred_resolve":
		return shipStateDeferredResolve(root, in)

	// CLI evidence logging — persistent JSONL store that survives state-file GC.
	case "log-cli":
		return shipStateLogCLI(root, workDir, in)

	// Self-healing ledger — review total, fixed findings, harden results.
	case "healing_record":
		return shipStateHealingRecord(root, workDir, in, now)

	// Harden-cluster grouping — review findings clustered by file for harden's fix loop.
	case "harden_clusters":
		return shipStateHardenClusters(root, workDir, in)

	// End-of-run report — composed, rendered and optionally written by the tool.
	case "report":
		return shipStateReport(root, workDir, in, now)

	default:
		return nil, unknownActionError("ship_state action", in.Action, "",
			"pass one of: init, begin-step, complete-step, commit-check, start, complete, skip, fail, decide, defer, read, next, todos, cleanup, cleanup-pipeline, gc, migrate, history_record, deferred_add, deferred_list, deferred_propose_followups, deferred_resolve, log-cli, healing_record, harden_clusters, report (start and complete are legacy aliases of begin-step and complete-step)")
	}
}

// ---------------------------------------------------------------------------
// Action: init
// ---------------------------------------------------------------------------

// shipStateInit ports cmdInit: prune any existing ship state for this
// branch's slug, then create a fresh state file scaffolded with
// shipmeta.InitialShipSteps(). Unlike shipPrepare's own init sequence (which
// hard-requires --branch as part of a much larger flag-merge/validation
// flow), this mirrors cmdInit's simpler shape and auto-detects the current
// branch via execResolveBranch when Detail["branch"] is absent, consistent
// with every other action in this file.
func shipStateInit(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	branch, err := execResolveBranch(detailStr(in.Detail, "branch"), workDir)
	if err != nil {
		return nil, err
	}

	flags, _ := in.Detail["flags"].(map[string]any)
	if flags == nil {
		flags = map[string]any{}
	}

	branchSlug := state.SlugifyBranch(branch)
	pruned, err := existingShipStateFiles(root, branchSlug)
	if err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("scan existing ship state files for branch slug %q: %s", branchSlug, err.Error()),
			Suggestion: "Check read permission on " + paths.DataDir + "/runs/ and that ship-<branch-slug>-*.json files are not corrupted, then retry ship_state init.",
			Cause:      err,
		}
	}

	st, err := state.Init(root, "ship", branch, in.SessionID)
	if err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("init ship state for branch %q: %s", branch, err.Error()),
			Suggestion: "Check write permission on " + paths.DataDir + "/runs/ and available disk space on the project root, then retry ship_state init.",
			Cause:      err,
		}
	}

	st.Data["version"] = 1
	st.Data["startedAt"] = now().UTC().Format(time.RFC3339)
	st.Data["branch"] = branch
	st.Data["worktree"] = workDir
	st.Data["flags"] = flags
	st.Data["steps"] = shipmeta.InitialShipSteps()
	st.Data["decisions"] = []any{}
	st.Data["deferredFindings"] = []any{}

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write ship state to %s: %s", st.Path, err.Error()),
			Suggestion: "Check write permission on the ship state file path above and free disk space on the project root, then retry ship_state init.",
			Cause:      err,
		}
	}

	return map[string]any{
		"filePath":      st.Path,
		"prunedOrphans": pruned,
		"worktree":      workDir,
	}, nil
}

// ---------------------------------------------------------------------------
// Actions: start / complete (legacy always-success pair, predate
// begin-step/complete-step's outcome parameter)
// ---------------------------------------------------------------------------

func shipStateStart(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	if in.Step == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "start: step is required",
			Suggestion: "Pass the step field naming a pipeline step (e.g. \"execute\"), then retry ship_state start.",
		}
	}
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}
	if err := shipStartStepCore(st.Data, in.Step, now); err != nil {
		return nil, err
	}
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write ship state to %s: %s", st.Path, err.Error()),
			Suggestion: "Check write permission on the ship state file path above and free disk space on the project root, then retry ship_state start.",
			Cause:      err,
		}
	}

	ts := pipeline.NewTimingsStore(root)
	pos, total := shipStepPosition(st.Data, in.Step)
	out := ShipStepNarrationOut{
		Narration: pipeline.Narration{
			Summary: fmt.Sprintf("Step '%s' started (%d of %d).", in.Step, pos, total),
			Display: pipeline.StepProgressBlock(shipBuildStepRows(st.Data), ts),
			Next: &pipeline.NextAction{
				ID:          in.Step,
				Instruction: shipStepInstruction(in.Step),
			},
		},
	}
	if est, ok := ts.Estimate("ship:" + in.Step); ok {
		out.Next.EtaSeconds = est.Seconds
		out.Next.EtaBasis = est.Basis
	}
	return out, nil
}

func shipStateComplete(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	if in.Step == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "complete: step is required",
			Suggestion: "Pass the step field naming the pipeline step to complete, then retry ship_state complete.",
		}
	}
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}

	// Resolve the target entry before the mutation: once it is completed,
	// a lookup by name moves on to a later entry with the same name.
	stepIdx := shipFindStepIndex(st.Data, in.Step)
	stepEntry := shipFindStepEntry(st.Data, in.Step)
	var startedAtBefore string
	if stepEntry != nil {
		startedAtBefore, _ = stepEntry["startedAt"].(string)
	}

	resultVal, hasResult := in.Detail["result"]
	if err := shipCompleteStepCore(st.Data, in.Step, hasResult, resultVal, "success", now); err != nil {
		return nil, err
	}
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write ship state to %s: %s", st.Path, err.Error()),
			Suggestion: "Check write permission on the ship state file path above and free disk space on the project root, then retry ship_state complete.",
			Cause:      err,
		}
	}

	var completedAtAfter string
	if stepEntry != nil {
		completedAtAfter, _ = stepEntry["completedAt"].(string)
	}
	timing, ts := shipCompletionTiming(root, st.Data, stepIdx, in.Step, startedAtBefore, completedAtAfter, now())
	rows := shipBuildStepRows(st.Data)
	pos, total := stepIdx+1, len(shipStepsSlice(st.Data))

	summary := fmt.Sprintf("Step '%s' completed (%d of %d).", in.Step, pos, total)
	if timing != nil {
		summary = fmt.Sprintf("Step '%s' completed in %s (%d of %d).",
			in.Step, pipeline.Humanize(time.Duration(timing.StepSeconds)*time.Second), pos, total)
	}

	out := ShipStepNarrationOut{
		Narration: pipeline.Narration{
			Summary: summary,
			Display: pipeline.StepProgressBlock(rows, ts),
			Timing:  timing,
			Next:    shipBuildNextAction(st.Data, ts),
		},
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Action: begin-step (R-b1 proceed-gate)
// ---------------------------------------------------------------------------

func shipStateBeginStep(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	if in.Step == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "begin-step: step is required",
			Suggestion: "Pass the step field naming the pipeline step to begin, then retry ship_state begin-step.",
		}
	}
	st, err := shipLoadState(root, workDir, in)
	if err != nil {
		return nil, err
	}

	steps := shipStepsSlice(st.Data)
	idx := -1
	for i, s := range steps {
		if sm, ok := s.(map[string]any); ok && sm["name"] == in.Step {
			idx = i
			break
		}
	}

	if idx > 0 {
		var blocking []string
		for _, s := range steps[:idx] {
			sm, ok := s.(map[string]any)
			if !ok {
				continue
			}
			if shipStepBlocksProceed(sm) {
				name, _ := sm["name"].(string)
				status, _ := sm["status"].(string)
				blocking = append(blocking, fmt.Sprintf("%s=%s", name, status))
			}
		}
		if len(blocking) > 0 {
			// DomainError, not DataError: unlike the structurally similar
			// contract-violation check in cleanup/cleanup-pipeline (which
			// flags a *stored* state as bad data), this is a caller trying
			// an operation the current — valid — state doesn't yet permit.
			// The fact sheet calls out begin-step specifically for this
			// override; the rest of the proceed-gate call sites (cleanup,
			// cleanup-pipeline) keep DataError.
			return nil, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("cannot begin step %q — prior step(s) not terminal-OK: %s; complete or skip the blocking step(s) first", in.Step, strings.Join(blocking, ", ")),
				Suggestion: "Call ship_state complete-step or skip for each blocking step listed above, then retry begin-step.",
			}
		}
	}

	if err := shipStartStepCore(st.Data, in.Step, now); err != nil {
		return nil, err
	}
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write ship state to %s: %s", st.Path, err.Error()),
			Suggestion: "Check write permission on the ship state file path above and free disk space on the project root, then retry ship_state begin-step.",
			Cause:      err,
		}
	}

	ts := pipeline.NewTimingsStore(root)
	pos, total := shipStepPosition(st.Data, in.Step)
	out := ShipStepNarrationOut{
		Narration: pipeline.Narration{
			Summary: fmt.Sprintf("Step '%s' started (%d of %d).", in.Step, pos, total),
			Display: pipeline.StepProgressBlock(shipBuildStepRows(st.Data), ts),
			Next: &pipeline.NextAction{
				ID:          in.Step,
				Instruction: shipStepInstruction(in.Step),
			},
		},
		Todos:       shipmeta.TodosForStep(in.Step, st),
		AlreadyDone: shipStepAlreadyDone(st.Data, in.Step),
	}
	if est, ok := ts.Estimate("ship:" + in.Step); ok {
		out.Next.EtaSeconds = est.Seconds
		out.Next.EtaBasis = est.Basis
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Action: complete-step (outcome-aware; consolidates markCompleted and the
// failure-path renderTodos call into one shipmeta.TodosForStep render, since
// its status-priority switch already checks recorded terminal statuses
// before stepName==step and so renders correctly for either outcome as long
// as it runs after the mutation is persisted)
// ---------------------------------------------------------------------------

func shipStateCompleteStep(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	if in.Step == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "complete-step: step is required",
			Suggestion: "Pass the step field naming the pipeline step to complete, then retry complete-step.",
		}
	}

	outcome := "success"
	if raw, ok := in.Detail["outcome"]; ok {
		s, isStr := raw.(string)
		if !isStr || (s != "success" && s != "failure") {
			return nil, &mcpserver.DomainError{
				Msg:        fmt.Sprintf(`complete-step: outcome must be "success" or "failure", got %v`, raw),
				Suggestion: "Pass detail.outcome as the exact string \"success\" or \"failure\", then retry complete-step.",
			}
		}
		outcome = s
	}

	st, err := shipLoadState(root, workDir, in)
	if err != nil {
		return nil, err
	}

	// Capture startedAt and the target index before mutation: once the
	// entry is completed, a lookup by name moves on to a later entry with
	// the same name.
	stepIdx := shipFindStepIndex(st.Data, in.Step)
	stepEntry := shipFindStepEntry(st.Data, in.Step)
	var startedAtBefore string
	if stepEntry != nil {
		startedAtBefore, _ = stepEntry["startedAt"].(string)
	}

	resultVal, hasResult := in.Detail["result"]
	if err := shipCompleteStepCore(st.Data, in.Step, hasResult, resultVal, outcome, now); err != nil {
		return nil, err
	}
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write ship state to %s: %s", st.Path, err.Error()),
			Suggestion: "Check write permission on the ship state file path above and free disk space on the project root, then retry ship_state complete-step.",
			Cause:      err,
		}
	}

	var completedAtAfter string
	if stepEntry != nil {
		completedAtAfter, _ = stepEntry["completedAt"].(string)
	}
	timing, ts := shipCompletionTiming(root, st.Data, stepIdx, in.Step, startedAtBefore, completedAtAfter, now())
	rows := shipBuildStepRows(st.Data)
	pos, total := stepIdx+1, len(shipStepsSlice(st.Data))

	verb := "completed"
	if outcome == "failure" {
		verb = "failed"
	}
	var summary string
	if timing != nil {
		summary = fmt.Sprintf("Step '%s' %s in %s (%d of %d).",
			in.Step, verb, pipeline.Humanize(time.Duration(timing.StepSeconds)*time.Second), pos, total)
	} else {
		summary = fmt.Sprintf("Step '%s' %s (%d of %d).", in.Step, verb, pos, total)
	}

	out := ShipStepNarrationOut{
		Narration: pipeline.Narration{
			Summary: summary,
			Display: pipeline.StepProgressBlock(rows, ts),
			Timing:  timing,
			Next:    shipBuildNextAction(st.Data, ts),
		},
		Todos: shipmeta.TodosForStep(in.Step, st),
	}
	if count, highlights := execIssueSummary(st.Data, 5); count > 0 {
		out.IssueCount = count
		out.IssueHighlights = highlights
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Action: commit-check
// ---------------------------------------------------------------------------

// commitNothingPrefix starts the commit step result that commit-check stores
// when the commit step had nothing to commit.
const commitNothingPrefix = "nothing to commit"

// shipCommitBaseHeadKey is the root ship state key that holds the HEAD sha
// seen by the first commit-check call of a run.
const shipCommitBaseHeadKey = "commitBaseHead"

// shipCommitStep is the pipeline step name that commit-check acts on.
const shipCommitStep = "commit"

// shipCommitCheckGit runs one git command in dir and returns its stdout.
// Tests replace it to make one git call fail without a broken repository.
var shipCommitCheckGit = func(dir string, args ...string) (string, error) {
	return execx.Run("git", args, execx.Options{Dir: dir})
}

// ShipCommitCheckOut is the output of the commit-check action. Clean and
// StagedCount describe the working tree after staging. StepCompleted is true
// when commit-check completed the commit step itself; Result is then the
// result stored on the step, and Todos and Display come from the completed
// step. Next always names what the caller does next.
type ShipCommitCheckOut struct {
	Clean         bool            `json:"clean" jsonschema_description:"True when nothing is staged after git add."`
	StagedCount   int             `json:"stagedCount" jsonschema_description:"Number of staged paths that git diff --cached --name-only lists."`
	WaveCommits   int             `json:"waveCommits" jsonschema_description:"Number of execute waves with a non-empty committedSha; set only for a clean tree with HEAD equal to commitBaseHead."`
	StepCompleted bool            `json:"stepCompleted" jsonschema_description:"True when this call completed the commit step."`
	Result        string          `json:"result" jsonschema_description:"Result stored on the completed commit step; empty when the step was not completed."`
	Todos         []shipmeta.Todo `json:"todos,omitempty" jsonschema_description:"Pipeline todos after the commit step completed; absent when the step was not completed."`
	Display       string          `json:"display,omitempty" render:"raw" jsonschema_description:"Markdown step progress block after the commit step completed; absent when the step was not completed."`
	Warnings      []string        `json:"warnings,omitempty" jsonschema_description:"Problems that did not stop the call, such as an unreadable execute state."`
	Next          string          `json:"next" jsonschema_description:"What the caller does next."`
}

// shipCommitGitError wraps a failed git call in an InfraError.
func shipCommitGitError(args []string, err error) error {
	return &mcpserver.InfraError{
		Msg:        fmt.Sprintf("commit-check: git %s: %s", strings.Join(args, " "), err.Error()),
		Suggestion: "Check the repository state with git status, then call commit-check again.",
		Cause:      err,
	}
}

// shipCountWaveCommits counts the waves with a non-empty committedSha in the
// execute state of branch. No execute state gives 0. An unreadable execute
// state also gives 0, plus a warning that names the cause.
func shipCountWaveCommits(root, branch string) (int, []string) {
	st, err := state.Find(root, "execute", branch)
	if err != nil {
		return 0, []string{"execute state not read, wave commits counted as 0: " + err.Error()}
	}
	if st == nil {
		return 0, nil
	}
	waves, _ := st.Data["waves"].([]any)
	n := 0
	for _, w := range waves {
		wm, ok := w.(map[string]any)
		if !ok {
			continue
		}
		if sha, _ := wm["committedSha"].(string); sha != "" {
			n++
		}
	}
	return n, nil
}

// shipNothingToCommitResult returns the commit step result for a clean tree
// with no new commit since the first commit-check of the run.
func shipNothingToCommitResult(waveCommits int) string {
	if waveCommits > 0 {
		return fmt.Sprintf("%s: execute committed %d wave commit(s)", commitNothingPrefix, waveCommits)
	}
	return commitNothingPrefix + ": the working tree is clean"
}

// shipStateCommitCheck stages the working tree (except the data directory)
// in workDir and decides the commit step from the result. The ship state in
// root must have its commit step in_progress. The first call of a run stores
// HEAD under commitBaseHead; later calls keep the stored value. A dirty tree
// leaves the step for the commit agent. A clean tree with HEAD at
// commitBaseHead records one decision and completes the step with a
// "nothing to commit" result. A clean tree with HEAD past commitBaseHead
// journals HEAD as the commit side effect and completes the step with
// "committed <short sha>". All state changes go to disk in one write.
func shipStateCommitCheck(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	branch, err := execResolveBranch(detailStr(in.Detail, "branch"), workDir)
	if err != nil {
		return nil, err
	}
	st, err := shipFindState(root, branch)
	if err != nil {
		return nil, err
	}
	stepIdx := shipFindStepIndex(st.Data, shipCommitStep)
	step := shipFindStepEntry(st.Data, shipCommitStep)
	if step == nil {
		return nil, &mcpserver.DataError{
			Msg:        "commit-check: no commit step in the ship state",
			Suggestion: "Run ship_state read to list the steps. Call commit-check only for a pipeline that has a commit step.",
		}
	}
	if status, _ := step["status"].(string); status != "in_progress" {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("commit-check: commit step status is %q, want \"in_progress\"", status),
			Suggestion: "Call begin-step for commit first, then call commit-check again.",
		}
	}
	// An absent key or an empty string means no earlier commit-check call
	// stored HEAD. Any other non-string value is a broken state file: using
	// it as absent would overwrite it and hide a landed commit.
	baseHead := ""
	if raw, present := st.Data[shipCommitBaseHeadKey]; present && raw != nil {
		s, ok := raw.(string)
		if !ok {
			return nil, &mcpserver.DataError{
				Msg:        fmt.Sprintf("commit-check: ship state key %s is %T, want a string sha", shipCommitBaseHeadKey, raw),
				Suggestion: fmt.Sprintf("Set %s in %s to the HEAD sha seen at the start of the commit step, or remove the key, then call commit-check again.", shipCommitBaseHeadKey, st.Path),
			}
		}
		baseHead = s
	}

	addArgs := []string{"add", "-A", "--", ":!" + paths.DataDir + "/"}
	if _, err := shipCommitCheckGit(workDir, addArgs...); err != nil {
		return nil, shipCommitGitError(addArgs, err)
	}
	diffArgs := []string{"diff", "--cached", "--name-only"}
	staged, err := shipCommitCheckGit(workDir, diffArgs...)
	if err != nil {
		return nil, shipCommitGitError(diffArgs, err)
	}
	stagedCount := 0
	for _, line := range strings.Split(staged, "\n") {
		if strings.TrimSpace(line) != "" {
			stagedCount++
		}
	}
	headArgs := []string{"rev-parse", "HEAD"}
	headOut, err := shipCommitCheckGit(workDir, headArgs...)
	if err != nil {
		return nil, shipCommitGitError(headArgs, err)
	}
	head := strings.TrimSpace(headOut)

	baseChanged := false
	if baseHead == "" {
		baseHead = head
		st.Data[shipCommitBaseHeadKey] = head
		baseChanged = true
	}

	if stagedCount > 0 {
		if baseChanged {
			if err := shipStateWriteFunc(st); err != nil {
				return nil, shipCommitWriteError(st, err)
			}
		}
		return ShipCommitCheckOut{
			Clean:       false,
			StagedCount: stagedCount,
			Next:        "Changes are staged. Dispatch the commit agent, then 7c2, then complete-step.",
		}, nil
	}

	out := ShipCommitCheckOut{Clean: true, StepCompleted: true}
	// One instant for the decision, the side effect, completedAt and the
	// step timing, so the recorded times agree.
	at := now()
	atNow := func() time.Time { return at }
	if head == baseHead {
		out.WaveCommits, out.Warnings = shipCountWaveCommits(root, branch)
		out.Result = shipNothingToCommitResult(out.WaveCommits)
		decisions, _ := st.Data["decisions"].([]any)
		st.Data["decisions"] = append(decisions, map[string]any{
			"step":     shipCommitStep,
			"decision": out.Result,
			"at":       at.UTC().Format(time.RFC3339),
		})
		out.Next = "Commit step completed. Skip 7c2 and 7d. Go to the next step."
	} else {
		out.Result = "committed " + shortSHA(head)
		shipRecordSideEffect(st.Data, shipSideEffectKey(st.Data, shipCommitStep), shipStepSideEffects[shipCommitStep], head, at)
		out.Next = "Commit step completed from the landed commit. Skip 7c2 and 7d. Go to the next step."
	}

	startedAt, _ := step["startedAt"].(string)
	if err := shipCompleteStepCore(st.Data, shipCommitStep, true, out.Result, "success", atNow); err != nil {
		return nil, err
	}
	if err := shipStateWriteFunc(st); err != nil {
		return nil, shipCommitWriteError(st, err)
	}
	completedAt, _ := step["completedAt"].(string)
	_, ts := shipCompletionTiming(root, st.Data, stepIdx, shipCommitStep, startedAt, completedAt, at)
	out.Display = pipeline.StepProgressBlock(shipBuildStepRows(st.Data), ts)
	out.Todos = shipmeta.TodosForStep(shipCommitStep, st)
	return out, nil
}

// shipCommitWriteError wraps a failed ship state write in commit-check.
func shipCommitWriteError(st *state.State, err error) error {
	return &mcpserver.InfraError{
		Msg:        fmt.Sprintf("write ship state to %s: %s", st.Path, err.Error()),
		Suggestion: "Check write permission on the ship state file path above and free disk space on the project root, then call commit-check again.",
		Cause:      err,
	}
}

// ---------------------------------------------------------------------------
// Actions: skip / fail / decide / defer / read
// ---------------------------------------------------------------------------

func shipStateSkip(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	if in.Step == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "skip: step is required",
			Suggestion: "Pass the step field naming the pipeline step to skip, then retry ship_state skip.",
		}
	}
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}
	// Resolve the index before the mutation: once the entry is skipped, a
	// lookup by name moves on to a later entry with the same name.
	stepIdx := shipFindStepIndex(st.Data, in.Step)
	step := shipFindStepEntry(st.Data, in.Step)
	if step == nil {
		return nil, &mcpserver.DataError{
			Msg:        fmt.Sprintf("step %q not found in state", in.Step),
			Suggestion: "Run ship_state read and pass one of the step names listed under steps, then retry ship_state skip.",
		}
	}
	step["status"] = "skipped"
	step["completedAt"] = now().UTC().Format(time.RFC3339)
	if v, ok := in.Detail["reason"]; ok {
		step["reason"] = v
	}
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write ship state to %s: %s", st.Path, err.Error()),
			Suggestion: "Check write permission on the ship state file path above and free disk space on the project root, then retry ship_state skip.",
			Cause:      err,
		}
	}

	pos, total := stepIdx+1, len(shipStepsSlice(st.Data))
	out := ShipStepNarrationOut{
		Narration: pipeline.Narration{
			Summary: fmt.Sprintf("Step '%s' skipped (%d of %d).", in.Step, pos, total),
		},
	}
	if shipDetailLevel(in) == "full" {
		ts := pipeline.NewTimingsStore(root)
		out.Display = pipeline.StepProgressBlock(shipBuildStepRows(st.Data), ts)
	}
	return out, nil
}

// shipHistoryAppendFunc appends one run record to runs.jsonl. Tests replace it
// to force an append failure without touching file permissions.
var shipHistoryAppendFunc = func(root string, rec history.RunRecord) error {
	return history.NewFileWriter(historyDir(root)).AppendRun(rec)
}

// shipStateWriteFunc writes a ship state file. Tests replace it to force a
// write failure without touching file permissions.
var shipStateWriteFunc = state.Write

// shipFailDurationMs returns the milliseconds between startedAt and now. It
// returns 0 when startedAt does not parse as an RFC3339 timestamp.
func shipFailDurationMs(startedAt string, now time.Time) int64 {
	d, ok := pipeline.Duration(startedAt, now.UTC().Format(time.RFC3339))
	if !ok {
		return 0
	}
	return d.Milliseconds()
}

// shipStateFail marks a step failed and records the issue. The first fail of a
// run also appends one failure row to runs.jsonl, so the dashboard can show a
// run that never reached cleanup. It sets historyFailureRecorded before the
// state write, so a later fail in the same run appends no second row. If the
// state write fails, the flag is not persisted and no row exists, so a retry is
// safe. If the append fails, the action still succeeds and the response
// carries a warning that names the runs path.
func shipStateFail(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	if in.Step == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "fail: step is required",
			Suggestion: "Pass the step field naming the pipeline step to fail, then retry ship_state fail.",
		}
	}
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}
	step := shipFindStepEntry(st.Data, in.Step)
	if step == nil {
		return nil, &mcpserver.DataError{
			Msg:        fmt.Sprintf("step %q not found in state", in.Step),
			Suggestion: "Run ship_state read and pass one of the step names listed under steps, then retry ship_state fail.",
		}
	}
	step["status"] = "failed"
	var detail string
	if v, ok := in.Detail["error"]; ok {
		step["error"] = v
		if s, isStr := v.(string); isStr {
			detail = s
		} else {
			detail = fmt.Sprint(v)
		}
	}

	failedAt := now()
	st.Data["lastFailedStep"] = in.Step
	execAppendIssue(st.Data, StateIssue{
		Step:      in.Step,
		Severity:  "error",
		Category:  "ship-fail",
		Summary:   fmt.Sprintf("Step %s failed", in.Step),
		Detail:    detail,
		Timestamp: failedAt.UTC().Format(time.RFC3339),
	})

	recorded, _ := st.Data["historyFailureRecorded"].(bool)
	firstFail := !recorded
	if firstFail {
		st.Data["historyFailureRecorded"] = true
	}

	if err := shipStateWriteFunc(st); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write ship state to %s: %s", st.Path, err.Error()),
			Suggestion: "Check write permission on the ship state file path above and free disk space on the project root, then retry ship_state fail.",
			Cause:      err,
		}
	}

	// The flag is persisted before the append. A crash between the two loses
	// the row but never writes a second one.
	var warnings []string
	if firstFail {
		startedAt := dashboardStr(st.Data["startedAt"])
		rec := history.RunRecord{
			Timestamp:  failedAt.UTC().Format(time.RFC3339),
			Skill:      "ship",
			Branch:     dashboardStr(st.Data["branch"]),
			Outcome:    "failure",
			DurationMs: shipFailDurationMs(startedAt, failedAt),
			StartedAt:  startedAt,
		}
		if err := shipHistoryAppendFunc(root, rec); err != nil {
			warnings = append(warnings, "failure history row not written to "+paths.DataDir+"/history/runs.jsonl: "+err.Error()+
				`. To add it, call ship_state history_record with detail.skill "ship" and detail.outcome "failure".`)
		}
	}

	pos, total := shipStepPosition(st.Data, in.Step)
	out := ShipStepNarrationOut{
		Narration: pipeline.Narration{
			Summary: fmt.Sprintf("Step '%s' failed (%d of %d).", in.Step, pos, total),
		},
		Warnings: warnings,
	}
	if shipDetailLevel(in) == "full" {
		ts := pipeline.NewTimingsStore(root)
		out.Display = pipeline.StepProgressBlock(shipBuildStepRows(st.Data), ts)
	}
	return out, nil
}

func shipStateDecide(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	if in.Step == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "decide: step is required",
			Suggestion: "Pass the step field naming the pipeline step this decision applies to, then retry ship_state decide.",
		}
	}
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}
	decisions, _ := st.Data["decisions"].([]any)
	decisions = append(decisions, map[string]any{
		"step":     in.Step,
		"decision": detailStr(in.Detail, "text"),
		"at":       now().UTC().Format(time.RFC3339),
	})
	st.Data["decisions"] = decisions
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write ship state to %s: %s", st.Path, err.Error()),
			Suggestion: "Check write permission on the ship state file path above and free disk space on the project root, then retry ship_state decide.",
			Cause:      err,
		}
	}

	out := ShipStepNarrationOut{
		Narration: pipeline.Narration{
			Summary: fmt.Sprintf("Decision recorded for step '%s'.", in.Step),
		},
	}
	if shipDetailLevel(in) == "full" {
		ts := pipeline.NewTimingsStore(root)
		out.Display = pipeline.StepProgressBlock(shipBuildStepRows(st.Data), ts)
	}
	return out, nil
}

// shipStateDefer records a below-threshold review finding. It writes the
// finding twice: to the run-scoped ship state file (data["deferredFindings"],
// which GC eventually sweeps) and to .sdlc-v2/history/deferred.json, which
// survives that sweep (KD-1). Persistence happens here, at creation, so it
// cannot depend on the ship skill reaching step 10b — any earlier exit used
// to lose the finding outright.
func shipStateDefer(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	rawSeverity := detailStr(in.Detail, "severity")
	file := detailStr(in.Detail, "file")
	title := detailStr(in.Detail, "title")
	if rawSeverity == "" || file == "" || title == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "defer: severity, file, and title are required",
			Suggestion: "Pass detail.severity, detail.file, and detail.title all as non-empty strings, then retry ship_state defer.",
		}
	}
	// severity must name one of the review severities. It used to be stored
	// raw, so "HIGH" or a typo reached both stores while
	// priorityFromSeverity quietly bucketed the unknown value as medium —
	// a silent degraded write. The accepted set is dimensions.ValidSeverities
	// (the same vocabulary review findings are emitted with), not a literal
	// restated here; "info" is in it deliberately: the default
	// reviewThreshold is "info", so a default run defers nothing as
	// below-threshold, but a project on "low" defers exactly its info
	// findings. The normalized lowercase form is what both stores record.
	severity := strings.ToLower(strings.TrimSpace(rawSeverity))
	acceptedSeverities := strings.Join(dimensions.ValidSeverities, " | ")
	if !slices.Contains(dimensions.ValidSeverities, severity) {
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("defer: detail.severity %q is not a recognised review severity — accepted values are %s",
				rawSeverity, acceptedSeverities),
			Suggestion: "Pass detail.severity as one of " + acceptedSeverities +
				" (case-insensitive — the lowercase form is what gets recorded), then retry ship_state defer.",
		}
	}
	// reason is optional; when present it must be a string naming one of
	// the values history.ValidDeferredReason accepts. The set is defined
	// once, in internal/history, and is not restated here. The message
	// names the whole accepted set, so a caller reading only the error text
	// knows every value it may pass (mcp-error-actionable).
	accepted := strings.Join(history.DeferredReasons(), " | ")
	reasonSuggestion := "Pass detail.reason as one of " + accepted +
		", or omit it entirely to record " + history.ReasonBelowThreshold + ", then retry ship_state defer."
	reason, err := shipDetailString(in.Detail, "defer", "reason", reasonSuggestion)
	if err != nil {
		return nil, err
	}
	if reason != "" && !history.ValidDeferredReason(reason) {
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("defer: detail.reason %q is not a recognised deferral reason — accepted values are %s",
				reason, accepted),
			// Spelled out inline rather than reusing reasonSuggestion:
			// mcp-error-suggestion-coverage requires the Suggestion field
			// to carry its own literal text.
			Suggestion: "Pass detail.reason as one of " + accepted +
				", or omit it entirely to record " + history.ReasonBelowThreshold + ", then retry ship_state defer.",
		}
	}
	if reason == "" {
		// An omitted reason is the below-threshold case: the finding was
		// routed out of the fix loop by its severity, not by a judgment
		// call. Recording it explicitly keeps every deferred entry
		// attributable — no entry carries an empty reason.
		reason = history.ReasonBelowThreshold
	}
	// description is optional and carries the deferring agent's own
	// reasoning (for needs-direction: the candidate approaches and the
	// trade-off). It falls back to the title, which is what every caller
	// that predates the field records today.
	description, err := shipDetailString(in.Detail, "defer", "description",
		"Pass detail.description as a string carrying your reasoning for deferring, or omit it to default to detail.title, then retry ship_state defer.")
	if err != nil {
		return nil, err
	}
	if description == "" {
		description = title
	}
	// line is optional, but a present non-numeric value used to be written
	// raw into the state entry while deferred.json got detailIntPtr's
	// parse (0) — one bad input, two durable records that disagree. Reject
	// it instead, and write the one parsed value to both.
	var linePtr *int
	if v, ok := in.Detail["line"]; ok && v != nil {
		if linePtr = detailIntPtr(in.Detail, "line"); linePtr == nil {
			return nil, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("defer: detail.line must be an integer, got %T", v),
				Suggestion: "Pass detail.line as a JSON number naming the line of the finding, or omit it entirely, then retry ship_state defer.",
			}
		}
	}
	line := 0
	var lineValue any
	if linePtr != nil {
		line = *linePtr
		lineValue = line
	}

	// source names the tool actually recording this deferral. It defaults to
	// the review-below-threshold value every caller used to get hardcoded,
	// but received-review (and any future caller) can name itself instead.
	source, err := shipDetailString(in.Detail, "defer", "source",
		"Common values: \"review-below-threshold\" (the default) or \"received-review\".")
	if err != nil {
		return nil, err
	}
	if source == "" {
		source = history.SourceReviewBelowThreshold
	}

	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	stateless := errors.Is(err, errNoShipState)
	if err != nil && !stateless {
		return nil, err
	}

	var findingsCount int
	if stateless {
		// No ship state file for this branch — history is the only durable
		// store available, so skip the run-scoped write entirely and go
		// straight to persistDeferred below.
		existing, listErr := historyWriter(root).ListDeferred()
		if listErr != nil {
			return nil, &mcpserver.InfraError{
				Msg:        fmt.Sprintf("list deferred issues: %s", listErr.Error()),
				Suggestion: "Check read permission on " + paths.DataDir + "/history/deferred.json, then retry ship_state defer.",
				Cause:      listErr,
			}
		}
		findingsCount = len(existing) + 1
	} else {
		findings, _ := st.Data["deferredFindings"].([]any)
		// description rides along so a later reader of the run-scoped
		// entry (ship's harden step builds each finding's body from it)
		// sees the deferring agent's reasoning, not just the title.
		findings = append(findings, map[string]any{
			"severity":    severity,
			"file":        file,
			"line":        lineValue,
			"title":       title,
			"reason":      reason,
			"description": description,
		})
		st.Data["deferredFindings"] = findings
		if err := state.Write(st); err != nil {
			return nil, &mcpserver.InfraError{
				Msg:        fmt.Sprintf("write ship state to %s: %s", st.Path, err.Error()),
				Suggestion: "Check write permission on the ship state file path above and free disk space on the project root, then retry ship_state defer.",
				Cause:      err,
			}
		}
		findingsCount = len(findings)
	}

	timestamp := now().UTC().Format(time.RFC3339)
	// The id is echoed in the narration: it is the handle every later
	// deferred_* call needs, and a mutating call that does not name the
	// resource it created leaves the caller unable to refer to it.
	deferredID := fmt.Sprintf("review-deferred-%s-%d", timestamp, findingsCount)
	// The file name belongs to internal/history, so ask that package for it
	// instead of repeating the literal here. This value is only shown in the
	// narration; the write itself goes through persistDeferred.
	deferredPath := history.NewFileWriter(historyDir(root)).DeferredPath()
	persistErr := persistDeferred(root, history.DeferredIssue{
		ID:          deferredID,
		Created:     timestamp,
		Source:      source,
		Priority:    priorityFromSeverity(severity),
		Description: description,
		Status:      history.StatusOpen,
		Severity:    severity,
		File:        file,
		Line:        line,
		Reason:      reason,
	})

	summary := fmt.Sprintf("Deferred finding recorded: %s (id %s).", title, deferredID)
	if persistErr != nil {
		summary += " " + deferredPersistWarning(persistErr, deferredID, title)
	} else {
		summary += fmt.Sprintf(" Written to %s.", deferredPath)
	}
	out := ShipStepNarrationOut{
		Narration: pipeline.Narration{
			Summary: summary,
		},
	}
	if !stateless && shipDetailLevel(in) == "full" {
		ts := pipeline.NewTimingsStore(root)
		out.Display = pipeline.StepProgressBlock(shipBuildStepRows(st.Data), ts)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Action: healing_record
// ---------------------------------------------------------------------------

// healingKinds, healingOrigins and healingPhases are the accepted values for
// healing_record's detail.kind, detail.origin (kinds "fixed" and
// "fix-progress") and detail.phase (kind "hardened"). ship-state.schema.json
// carries the same sets.
var (
	healingKinds   = []string{"review-total", "fixed", "hardened", "fix-progress"}
	healingOrigins = []string{"local-review", "pr-comment"}
	healingPhases  = []string{"started", "done"}
)

// healingFixStatuses is the one list of fix statuses that a fix-progress
// record takes (detail.status). healingFixFinal holds the statuses that end
// the fix of one finding. ship-state.schema.json carries the same status set.
var (
	healingFixStatuses = []string{"queued", "fixing", "fixed", "failed", "deferred"}
	healingFixFinal    = []string{"fixed", "failed", "deferred"}
)

// healingFixProgressMax is the most records data.healing.fixProgress holds
// for one run. A fix-progress call for a new key past it is rejected.
const healingFixProgressMax = 200

// Narrations returned by healing_record. Callers and tests match on them.
const (
	healingNarrRecorded   = "recorded"
	healingNarrReplaced   = "replaced started record"
	healingNarrDuplicate  = "already recorded — no change"
	healingNarrNoLiveRun  = "no live ship run on this branch — healing not recorded"
	healingNarrKeptFailed = "kept failed"
)

// healingFixNextID is the next.id of every fix-progress response.
const healingFixNextID = "continue-fix-pass"

// healingSurfaceIDs returns the ids of hardensurfaces.List(), the only
// values an applied[].surface may take.
func healingSurfaceIDs() []string {
	surfaces := hardensurfaces.List()
	ids := make([]string, 0, len(surfaces))
	for _, s := range surfaces {
		ids = append(ids, s.ID)
	}
	return ids
}

// healingRequiredString reads a required string field out of d. A missing,
// null or empty value and a wrong-typed value are both rejected, and the
// error names the field.
func healingRequiredString(d map[string]any, field, kind string) (string, error) {
	v, ok := d[field]
	if !ok || v == nil {
		return "", &mcpserver.DomainError{
			Msg: fmt.Sprintf("healing_record: detail.%s is required for kind %q", field, kind),
			Suggestion: fmt.Sprintf("Pass detail.%s as a non-empty string — kind %q needs it — then retry ship_state healing_record.",
				field, kind),
		}
	}
	s, isStr := v.(string)
	if !isStr || strings.TrimSpace(s) == "" {
		return "", &mcpserver.DomainError{
			Msg: fmt.Sprintf("healing_record: detail.%s must be a non-empty string, got %T %v", field, v, v),
			Suggestion: fmt.Sprintf("Pass detail.%s as a non-empty JSON string — kind %q needs it — then retry ship_state healing_record.",
				field, kind),
		}
	}
	return s, nil
}

// healingNonNegInt reads a required non-negative integer field out of d.
// JSON numbers arrive as float64, so a fractional value is rejected here
// rather than truncated the way detailIntPtr would. A bare int is accepted
// for in-process callers.
func healingNonNegInt(d map[string]any, field, kind string) (int, error) {
	v, ok := d[field]
	if !ok || v == nil {
		return 0, &mcpserver.DomainError{
			Msg: fmt.Sprintf("healing_record: detail.%s is required for kind %q", field, kind),
			Suggestion: fmt.Sprintf("Pass detail.%s as a non-negative JSON integer — kind %q needs it — then retry ship_state healing_record.",
				field, kind),
		}
	}
	n, isInt := healingInt(v)
	if !isInt || n < 0 {
		return 0, &mcpserver.DomainError{
			Msg: fmt.Sprintf("healing_record: detail.%s must be a non-negative integer, got %T %v", field, v, v),
			Suggestion: fmt.Sprintf("Pass detail.%s as a whole number of 0 or more (for example 0 or 14), then retry ship_state healing_record.",
				field),
		}
	}
	return n, nil
}

// healingInt converts a decoded JSON number (float64) or a Go int to an int.
// It reports false for any other type and for a float64 with a fraction.
func healingInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}

// healingLineKey returns a stored or incoming line value in a form that
// compares equal across a JSON round trip: nil when absent, else the int.
func healingLineKey(v any) any {
	if n, ok := healingInt(v); ok {
		return n
	}
	return nil
}

// shipStateHealingRecord records one self-healing change in the live ship
// run's data.healing. Four kinds exist:
//
//   - review-total sets data.healing.reviewTotal, replacing any earlier value.
//   - fixed appends to data.healing.fixed; a record with the same
//     (origin, file, line, title) is a duplicate and is not written again.
//   - hardened upserts data.healing.hardened by trigger: a "done" record
//     replaces a "started" one in place, and every other repeat is a
//     duplicate.
//   - fix-progress upserts data.healing.fixProgress on (origin, file, line,
//     title): see healingUpsertFix. It never touches data.healing.fixed.
//
// Input is validated before the state lookup, so a bad call fails even when
// no run is live. With no ship state for the branch, or a state stamped
// pipelineCompletedAt, the call succeeds and writes nothing, so standalone
// /harden and /received-review keep working outside /ship.
func shipStateHealingRecord(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	kinds := strings.Join(healingKinds, " | ")
	kind, err := shipDetailString(in.Detail, "healing_record", "kind",
		"Accepted kinds: "+kinds+".")
	if err != nil {
		return nil, err
	}
	if kind == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "healing_record: detail.kind is required — accepted values are " + kinds,
			Suggestion: "Pass detail.kind as one of " + kinds + " with that kind's fields, then retry ship_state healing_record.",
		}
	}
	if !slices.Contains(healingKinds, kind) {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("healing_record: detail.kind %q is not a recognised healing kind — accepted values are %s", kind, kinds),
			Suggestion: "Pass detail.kind as one of " + kinds + " with that kind's fields, then retry ship_state healing_record.",
		}
	}

	timestamp := now().UTC().Format(time.RFC3339)
	var record map[string]any
	switch kind {
	case "review-total":
		record, err = healingReviewTotal(in.Detail, timestamp)
	case "fixed":
		record, err = healingFixed(in.Detail, timestamp)
	case "hardened":
		record, err = healingHardened(in.Detail, timestamp)
	case "fix-progress":
		record, err = healingFixProgress(in.Detail, timestamp)
	}
	if err != nil {
		return nil, err
	}

	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if errors.Is(err, errNoShipState) {
		return healingNarration(kind, healingNarrNoLiveRun, record, false), nil
	}
	if err != nil {
		return nil, err
	}
	if completedAt, _ := st.Data["pipelineCompletedAt"].(string); completedAt != "" {
		return healingNarration(kind, healingNarrNoLiveRun, record, false), nil
	}

	healing, _ := st.Data["healing"].(map[string]any)
	if healing == nil {
		healing = map[string]any{}
	}
	narration := healingNarrRecorded
	switch kind {
	case "review-total":
		healing["reviewTotal"] = record
	case "fixed":
		fixed, _ := healing["fixed"].([]any)
		if healingHasFixed(fixed, record) {
			return healingNarration(kind, healingNarrDuplicate, record, false), nil
		}
		healing["fixed"] = append(fixed, record)
	case "hardened":
		hardened, _ := healing["hardened"].([]any)
		var changed bool
		hardened, narration, changed = healingUpsertHardened(hardened, record)
		if !changed {
			return healingNarration(kind, narration, record, false), nil
		}
		healing["hardened"] = hardened
	case "fix-progress":
		var progress []any
		if raw, ok := healing["fixProgress"]; ok {
			list, isList := raw.([]any)
			if !isList {
				return nil, &mcpserver.DataError{
					Msg:        "healing_record: data.healing.fixProgress is not a list — the ship state is damaged",
					Suggestion: "Do not retry. Continue the fix pass without fix-progress calls. Tell the user that the ship state file is damaged.",
				}
			}
			progress = list
		}
		var changed bool
		progress, narration, changed, err = healingUpsertFix(progress, record)
		if err != nil {
			return nil, err
		}
		if !changed {
			return healingNarration(kind, narration, record, false), nil
		}
		healing["fixProgress"] = progress
	}
	st.Data["healing"] = healing
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write ship state to %s: %s", st.Path, err.Error()),
			Suggestion: "Check write permission on the ship state file path above and free disk space on the project root, then retry ship_state healing_record.",
			Cause:      err,
		}
	}
	return healingNarration(kind, narration, record, true), nil
}

// ShipHealingRecordOut is the response of a healing_record call. Record
// echoes the validated record exactly as it is (or would have been)
// persisted, recordedAt included, so a caller can check what was stored and
// match it against a later read's data.healing. Written is true only when
// this call changed the state file; a duplicate, or a call with no live
// run, leaves it false.
type ShipHealingRecordOut struct {
	pipeline.Narration
	Kind    string         `json:"kind"`
	Written bool           `json:"written"`
	Record  map[string]any `json:"record"`
}

// healingNarration builds the response for a healing_record call. Only the
// fix-progress kind sets next: received-review calls it inside its fix pass,
// so every outcome tells that pass how to go on.
func healingNarration(kind, narration string, record map[string]any, written bool) ShipHealingRecordOut {
	out := ShipHealingRecordOut{
		Narration: pipeline.Narration{
			Summary: fmt.Sprintf("healing_record %s: %s", kind, narration),
		},
		Kind:    kind,
		Written: written,
		Record:  record,
	}
	if kind == "fix-progress" {
		out.Next = &pipeline.NextAction{ID: healingFixNextID, Instruction: healingFixNextInstruction(narration)}
	}
	return out
}

// healingFixNextInstruction returns the next.instruction of a fix-progress
// response for its narration.
func healingFixNextInstruction(narration string) string {
	switch narration {
	case healingNarrNoLiveRun:
		return "No live ship run, so the status is not stored. Continue without fix-progress calls."
	case healingNarrDuplicate:
		return "No change needed. Continue the fix pass."
	case healingNarrKeptFailed:
		return "The fix keeps status failed. Continue the fix pass."
	}
	return "Status stored. Continue the fix pass."
}

// healingReviewTotal validates a review-total record and returns it as it
// is persisted.
func healingReviewTotal(d map[string]any, timestamp string) (map[string]any, error) {
	total, err := healingNonNegInt(d, "total", "review-total")
	if err != nil {
		return nil, err
	}
	dims, err := healingNonNegInt(d, "dimensions", "review-total")
	if err != nil {
		return nil, err
	}
	return map[string]any{"total": total, "dimensions": dims, "recordedAt": timestamp}, nil
}

// healingFixed validates a fixed record and returns it as it is persisted.
// severity is lowercased, as defer does.
func healingFixed(d map[string]any, timestamp string) (map[string]any, error) {
	record, err := healingFinding(d, "fixed")
	if err != nil {
		return nil, err
	}
	record["recordedAt"] = timestamp
	return record, nil
}

// healingFixProgress validates a fix-progress record and returns it as it is
// persisted for a new key: firstAt and updatedAt both hold timestamp. It
// takes the finding fields of kind "fixed" plus detail.status.
func healingFixProgress(d map[string]any, timestamp string) (map[string]any, error) {
	record, err := healingFinding(d, "fix-progress")
	if err != nil {
		return nil, err
	}
	status, _ := d["status"].(string)
	if !slices.Contains(healingFixStatuses, status) {
		accepted := strings.Join(healingFixStatuses, " | ")
		msg := `healing_record: detail.status is required for kind "fix-progress" — accepted values are ` + accepted
		if v, ok := d["status"]; ok && v != nil {
			msg = fmt.Sprintf("healing_record: detail.status %#v is not a recognised fix status — accepted values are %s", v, accepted)
		}
		return nil, &mcpserver.DomainError{
			Msg:        msg,
			Suggestion: "Pass detail.status as one of " + strings.Join(healingFixStatuses, ", ") + ", then retry ship_state healing_record.",
		}
	}
	record["status"] = status
	record["firstAt"] = timestamp
	record["updatedAt"] = timestamp
	return record, nil
}

// healingFinding validates the finding fields that kinds "fixed" and
// "fix-progress" share (origin, severity, file, line, title) and returns
// them as a record. severity is lowercased, as defer does; an absent line is
// stored as null.
func healingFinding(d map[string]any, kind string) (map[string]any, error) {
	origin, err := healingRequiredString(d, "origin", kind)
	if err != nil {
		return nil, err
	}
	origins := strings.Join(healingOrigins, " | ")
	if !slices.Contains(healingOrigins, origin) {
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("healing_record: detail.origin %q is not a recognised finding origin — accepted values are %s",
				origin, origins),
			Suggestion: "Pass detail.origin as one of " + origins +
				" (local-review for a finding from the local review, pr-comment for one from a PR comment), then retry ship_state healing_record.",
		}
	}
	rawSeverity, err := healingRequiredString(d, "severity", kind)
	if err != nil {
		return nil, err
	}
	severity := strings.ToLower(strings.TrimSpace(rawSeverity))
	severities := strings.Join(dimensions.ValidSeverities, " | ")
	if !slices.Contains(dimensions.ValidSeverities, severity) {
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("healing_record: detail.severity %q is not a recognised review severity — accepted values are %s",
				rawSeverity, severities),
			Suggestion: "Pass detail.severity as one of " + severities +
				" (case-insensitive — the lowercase form is what gets recorded), then retry ship_state healing_record.",
		}
	}
	file, err := healingRequiredString(d, "file", kind)
	if err != nil {
		return nil, err
	}
	title, err := healingRequiredString(d, "title", kind)
	if err != nil {
		return nil, err
	}
	var line any
	if v, ok := d["line"]; ok && v != nil {
		n, isInt := healingInt(v)
		if !isInt {
			return nil, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("healing_record: detail.line must be an integer, got %T %v", v, v),
				Suggestion: "Pass detail.line as a JSON integer naming the line of the finding, or omit it entirely, then retry ship_state healing_record.",
			}
		}
		line = n
	}
	return map[string]any{
		"origin":   origin,
		"severity": severity,
		"file":     file,
		"line":     line,
		"title":    title,
	}, nil
}

// healingHardened validates a hardened record and returns it as it is
// persisted. An empty applied list is valid: harden ran and changed nothing.
func healingHardened(d map[string]any, timestamp string) (map[string]any, error) {
	phase, err := healingRequiredString(d, "phase", "hardened")
	if err != nil {
		return nil, err
	}
	phases := strings.Join(healingPhases, " | ")
	if !slices.Contains(healingPhases, phase) {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("healing_record: detail.phase %q is not a recognised harden phase — accepted values are %s", phase, phases),
			Suggestion: "Pass detail.phase as one of " + phases + " (started before harden writes, done after), then retry ship_state healing_record.",
		}
	}
	trigger, err := healingRequiredString(d, "trigger", "hardened")
	if err != nil {
		return nil, err
	}
	classification, err := healingRequiredString(d, "classification", "hardened")
	if err != nil {
		return nil, err
	}
	rawApplied, ok := d["applied"]
	if !ok || rawApplied == nil {
		return nil, &mcpserver.DomainError{
			Msg:        `healing_record: detail.applied is required for kind "hardened"`,
			Suggestion: "Pass detail.applied as a JSON array of {surface, action, targetFile} objects (an empty array when harden applied nothing), then retry ship_state healing_record.",
		}
	}
	items, isList := rawApplied.([]any)
	if !isList {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("healing_record: detail.applied must be an array, got %T", rawApplied),
			Suggestion: "Pass detail.applied as a JSON array of {surface, action, targetFile} objects (an empty array when harden applied nothing), then retry ship_state healing_record.",
		}
	}
	surfaceIDs := healingSurfaceIDs()
	surfaces := strings.Join(surfaceIDs, " | ")
	applied := make([]any, 0, len(items))
	for i, item := range items {
		m, isObj := item.(map[string]any)
		if !isObj {
			return nil, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("healing_record: detail.applied[%d] must be an object, got %T", i, item),
				Suggestion: "Pass every detail.applied entry as a {surface, action, targetFile} object, then retry ship_state healing_record.",
			}
		}
		entry := map[string]any{}
		for _, field := range []string{"surface", "action", "targetFile"} {
			s, isStr := m[field].(string)
			if !isStr || strings.TrimSpace(s) == "" {
				return nil, &mcpserver.DomainError{
					Msg: fmt.Sprintf("healing_record: detail.applied[%d].%s is required and must be a non-empty string", i, field),
					Suggestion: fmt.Sprintf("Set detail.applied[%d].%s to a non-empty string — every applied entry needs surface, action and targetFile — then retry ship_state healing_record.",
						i, field),
				}
			}
			entry[field] = s
		}
		if !slices.Contains(surfaceIDs, entry["surface"].(string)) {
			return nil, &mcpserver.DomainError{
				Msg: fmt.Sprintf("healing_record: detail.applied[%d].surface %q is not a harden surface id — accepted values are %s",
					i, entry["surface"], surfaces),
				Suggestion: "Set detail.applied[].surface to one of " + surfaces + ", then retry ship_state healing_record.",
			}
		}
		applied = append(applied, entry)
	}
	skipped, err := healingNonNegInt(d, "skipped", "hardened")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"phase":          phase,
		"trigger":        trigger,
		"classification": classification,
		"applied":        applied,
		"skipped":        skipped,
		"recordedAt":     timestamp,
	}, nil
}

// healingHasFixed reports whether fixed already holds a record with the same
// (origin, file, line, title) as rec. An absent line and a stored null line
// compare equal.
func healingHasFixed(fixed []any, rec map[string]any) bool {
	for _, f := range fixed {
		m, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if m["origin"] == rec["origin"] && m["file"] == rec["file"] && m["title"] == rec["title"] &&
			healingLineKey(m["line"]) == healingLineKey(rec["line"]) {
			return true
		}
	}
	return false
}

// healingUpsertHardened applies rec to hardened by trigger and returns the
// new list, the narration and whether anything changed:
//
//	stored     incoming  result
//	none       any       appended          recorded
//	started    done      replaced in place replaced started record
//	started    started   unchanged         already recorded — no change
//	done       any       unchanged         already recorded — no change
func healingUpsertHardened(hardened []any, rec map[string]any) ([]any, string, bool) {
	for i, h := range hardened {
		m, ok := h.(map[string]any)
		if !ok || m["trigger"] != rec["trigger"] {
			continue
		}
		if m["phase"] == "started" && rec["phase"] == "done" {
			hardened[i] = rec
			return hardened, healingNarrReplaced, true
		}
		return hardened, healingNarrDuplicate, false
	}
	return append(hardened, rec), healingNarrRecorded, true
}

// healingUpsertFix applies rec to list on the key (origin, file, line, title)
// and returns the new list, the narration, and whether the list changed:
//
//	stored     incoming   result
//	none       any        appended               recorded
//	status A   status A   unchanged              already recorded — no change
//	failed     deferred   unchanged              kept failed
//	status A   status B   status, severity and   recorded
//	                      updatedAt replaced,
//	                      firstAt kept
//
// On a replace, rec takes the stored firstAt and becomes the stored entry, so
// the caller echoes the record as persisted. An entry that is not an object
// never matches a key but counts toward healingFixProgressMax. A new key when
// the list already holds healingFixProgressMax entries is a DomainError.
func healingUpsertFix(list []any, rec map[string]any) ([]any, string, bool, error) {
	for i, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok || m["origin"] != rec["origin"] || m["file"] != rec["file"] || m["title"] != rec["title"] ||
			healingLineKey(m["line"]) != healingLineKey(rec["line"]) {
			continue
		}
		switch {
		case m["status"] == rec["status"]:
			return list, healingNarrDuplicate, false, nil
		case m["status"] == "failed" && rec["status"] == "deferred":
			return list, healingNarrKeptFailed, false, nil
		}
		if firstAt, ok := m["firstAt"].(string); ok && firstAt != "" {
			rec["firstAt"] = firstAt
		}
		list[i] = rec
		return list, healingNarrRecorded, true, nil
	}
	if len(list) >= healingFixProgressMax {
		return list, "", false, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("healing_record: data.healing.fixProgress holds %d records — the cap for one run", healingFixProgressMax),
			Suggestion: "Do not retry. Continue the fix pass without more fix-progress calls for this run.",
		}
	}
	return append(list, rec), healingNarrRecorded, true, nil
}

// ---------------------------------------------------------------------------
// Resume bearings briefing (read)
// ---------------------------------------------------------------------------

// ShipResumeBriefing is attached under the "resumeBriefing" key on a
// ship_state read response when shipRunInFlight reports a pipeline that has
// started but not finished. A step left "failed" is still reported here as
// Resumable: true — a crashed or interrupted run is presented as
// resumable, never surfaced as a failure.
type ShipResumeBriefing struct {
	pipeline.Narration
	Resumable      bool     `json:"resumable"`
	LastStep       string   `json:"lastStep,omitempty"`
	LastStepStatus string   `json:"lastStepStatus,omitempty"`
	SideEffects    []string `json:"sideEffects"`
}

// shipRunInFlight reports whether a ship pipeline has been started but not
// finished: some step still blocks proceed (shipFirstBlockingStep is
// non-empty) AND at least one step has actually been touched (has a
// startedAt). The second condition distinguishes a freshly init'd pipeline
// — nothing has run yet, so there is nothing to resume or report an
// interruption for — from a genuinely interrupted one.
//
// A run that cleanup stamped pipelineStatus:"completed" is never in flight.
// "failed" is terminal for the cleanup contract, so a stamped run can still
// hold a failed step that blocks proceed; without this check read would
// offer to resume a finished run.
func shipRunInFlight(data map[string]any) bool {
	if status, _ := data["pipelineStatus"].(string); status == "completed" {
		return false
	}
	if shipFirstBlockingStep(data) == "" {
		return false
	}
	return shipLastActiveStep(data) != nil
}

// shipLastActiveStep returns the step most recently touched: the
// in_progress step if one exists (R-b1 leaves a crashed run's step sitting
// at in_progress, so this also covers the crash case), else the last step
// (by position, scanning forward — last match wins) that has a startedAt.
// Mirrors buildShipRecovery's (hooks/pre_compact_save.go) precedent for
// deriving "current step" from step statuses. Returns nil if no step has
// ever been started.
func shipLastActiveStep(data map[string]any) map[string]any {
	var last map[string]any
	for _, s := range shipStepsSlice(data) {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if status, _ := sm["status"].(string); status == "in_progress" {
			return sm
		}
		if startedAt, _ := sm["startedAt"].(string); startedAt != "" {
			last = sm
		}
	}
	return last
}

// shipLastActivityAt returns the timestamp of a step's most recent recorded
// activity: its completedAt if it finished, else its startedAt. Both
// shipStateFail and shipCompleteStepCore's outcome=="failure" path leave
// completedAt unset on a failed step, so this falls back to startedAt —
// exactly the "how long since anything happened" signal a resume briefing
// needs.
func shipLastActivityAt(step map[string]any) string {
	if completedAt, _ := step["completedAt"].(string); completedAt != "" {
		return completedAt
	}
	startedAt, _ := step["startedAt"].(string)
	return startedAt
}

// shipStepDuration returns how long step has been running (if it has no
// completedAt yet — in_progress or failed) or how long it took (if
// completed/skipped).
func shipStepDuration(step map[string]any, now time.Time) (time.Duration, bool) {
	startedAt, _ := step["startedAt"].(string)
	if startedAt == "" {
		return 0, false
	}
	if completedAt, _ := step["completedAt"].(string); completedAt != "" {
		return pipeline.Duration(startedAt, completedAt)
	}
	start, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return 0, false
	}
	if d := now.Sub(start); d > 0 {
		return d, true
	}
	return 0, true
}

// shipSideEffectSummary renders data["sideEffects"] (the verified-effect
// journal, step -> {kind, ref, verifiedAt} — see shipRecordSideEffect in
// ship.go) as a sorted list of "step (kind): ref" strings. Always returns a
// non-nil slice (empty when the journal is empty or absent) so the field
// serializes as JSON "[]", never "null".
func shipSideEffectSummary(data map[string]any) []string {
	journal, _ := data["sideEffects"].(map[string]any)
	out := []string{}
	if journal == nil {
		return out
	}
	steps := make([]string, 0, len(journal))
	for step := range journal {
		steps = append(steps, step)
	}
	sort.Strings(steps)
	for _, step := range steps {
		entry, _ := journal[step].(map[string]any)
		if entry == nil {
			continue
		}
		kind, _ := entry["kind"].(string)
		ref, _ := entry["ref"].(string)
		if kind == "sha" {
			ref = shortSHA(ref)
		}
		out = append(out, fmt.Sprintf("%s (%s): %s", step, kind, ref))
	}
	return out
}

// shipBuildResumeBriefing composes the ResumeBriefing attached to read's
// response when shipRunInFlight reports an in-flight pipeline. Timing
// carries the three figures the resume-bearings AC asks for: StepSeconds
// (the last step's own duration/elapsed-so-far), PipelineSeconds (elapsed
// time since the pipeline started), and IdleSeconds (time since the last
// recorded activity — the "interrupted N ago" signal). next reuses
// shipBuildNextAction, the same helper complete-step/skip already use, so
// the briefing's next step matches what the rest of the tool would compute.
func shipBuildResumeBriefing(root string, data map[string]any, now time.Time) *ShipResumeBriefing {
	step := shipLastActiveStep(data)
	name, _ := step["name"].(string)
	status, _ := step["status"].(string)

	timing := &pipeline.TimingInfo{}
	stepDur, stepDurOK := shipStepDuration(step, now)
	if stepDurOK {
		timing.StepSeconds = int(stepDur.Round(time.Second).Seconds())
	}
	if pipelineStartedAt, _ := data["startedAt"].(string); pipelineStartedAt != "" {
		if pStart, parseErr := time.Parse(time.RFC3339, pipelineStartedAt); parseErr == nil {
			timing.PipelineSeconds = int(now.Sub(pStart).Round(time.Second).Seconds())
		}
	}
	var idleDur time.Duration
	var idleOK bool
	if lastActivity := shipLastActivityAt(step); lastActivity != "" {
		if lastAt, parseErr := time.Parse(time.RFC3339, lastActivity); parseErr == nil {
			idleDur = now.Sub(lastAt)
			if idleDur < 0 {
				idleDur = 0
			}
			idleOK = true
			timing.IdleSeconds = int(idleDur.Round(time.Second).Seconds())
		}
	}

	var humanParts []string
	if stepDurOK {
		humanParts = append(humanParts, "step "+pipeline.Humanize(stepDur))
	}
	if timing.PipelineSeconds > 0 {
		humanParts = append(humanParts, "pipeline "+pipeline.Humanize(time.Duration(timing.PipelineSeconds)*time.Second))
	}
	if idleOK {
		humanParts = append(humanParts, "idle "+pipeline.Humanize(idleDur))
	}
	timing.Human = strings.Join(humanParts, ", ")

	sideEffects := shipSideEffectSummary(data)

	idleText := "an unknown time"
	if idleOK {
		idleText = pipeline.Humanize(idleDur) + " ago"
	}

	b := &ShipResumeBriefing{
		Resumable:      true,
		LastStep:       name,
		LastStepStatus: status,
		SideEffects:    sideEffects,
	}
	b.Summary = fmt.Sprintf("Run resumable: last step %q (%s), interrupted %s.", name, status, idleText)

	lines := []string{fmt.Sprintf("Last step: %s (%s)", name, status)}
	if timing.Human != "" {
		lines = append(lines, "Timing: "+timing.Human)
	}
	if len(sideEffects) > 0 {
		lines = append(lines, "Side effects: "+strings.Join(sideEffects, "; "))
	}
	b.Display = "**Resume briefing**\n- " + strings.Join(lines, "\n- ")
	b.Timing = timing
	b.Next = shipBuildNextAction(data, pipeline.NewTimingsStore(root))
	return b
}

func shipStateRead(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}

	nowVal := now()
	out := make(map[string]any, len(st.Data)+2)
	for k, v := range st.Data {
		out[k] = v
	}
	// reportData (R9): report-ready aggregates (step counts, duration, bump
	// provenance, decisions, deferred findings, binary version) so the
	// calling LLM can render the ship report without re-deriving it from
	// raw state. Always attached, in-flight or not — a completed pipeline's
	// final report needs this exactly as much as a resumed one's does.
	out["reportData"] = shipBuildReportData(st.Data, nowVal)
	out["style"] = chatStyleFor(root)
	if shipRunInFlight(st.Data) {
		out["resumeBriefing"] = shipBuildResumeBriefing(root, st.Data, nowVal)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Action: cleanup (single branch)
// ---------------------------------------------------------------------------

func shipStateCleanup(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	branch, err := execResolveBranch(detailStr(in.Detail, "branch"), workDir)
	if err != nil {
		return nil, err
	}
	st, findErr := state.Find(root, "ship", branch)
	if findErr != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("find ship state for branch %q: %s", branch, findErr.Error()),
			Suggestion: "Check read permission on " + paths.DataDir + "/runs/ship-<branch-slug>-*.json and that the directory exists, then retry ship_state cleanup.",
			Cause:      findErr,
		}
	}
	if st == nil {
		// Nothing to clean up — cmdCleanup exits 0 silently in this case.
		return map[string]any{}, nil
	}

	valid, violations := shipValidatePipelineContract(st.Data)
	if !valid {
		return nil, &mcpserver.DataError{
			Msg: fmt.Sprintf(
				"pipeline contract violation: %d step(s) not in terminal state (%s) — state file preserved",
				len(violations), shipFormatViolations(violations)),
			Suggestion: "Complete, skip, or fail each step listed above so it reaches a terminal state, then retry ship_state cleanup.",
		}
	}

	completedAt := now().UTC().Format(time.RFC3339)
	st.Data["pipelineStatus"] = "completed"
	st.Data["pipelineCompletedAt"] = completedAt
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write ship state to %s: %s", st.Path, err.Error()),
			Suggestion: "Check write permission on the ship state file path above and free disk space on the project root, then retry ship_state cleanup.",
			Cause:      err,
		}
	}
	return map[string]any{
		"valid":               true,
		"cleaned":             true,
		"pipelineStatus":      "completed",
		"pipelineCompletedAt": completedAt,
	}, nil
}

// ---------------------------------------------------------------------------
// Action: cleanup-pipeline (single branch's cleanup + a full GC sweep)
// ---------------------------------------------------------------------------

// shipGCFunc is the GC sweep entry point. Tests replace it to force a sweep
// failure: state.GC fails only on a read error that Find and Write hit first.
var shipGCFunc = state.GC

// ShipPlanRunCleanup is cleanup-pipeline's "planRun" output: whether the
// plan run linked to this ship run was deleted, and if not, why.
//
// ExploreSummaryCount and ReviewRoundsCount are set only after the ship state
// write that holds the copies succeeded. They give the number of entries the
// ship state now holds under planExploreSummary and planReviewRounds. They
// are nil (omitted) when the copy was not written.
type ShipPlanRunCleanup struct {
	Deleted             bool   `json:"deleted"`
	RunID               string `json:"runId,omitempty"`
	Reason              string `json:"reason,omitempty"`
	ExploreSummaryCount *int   `json:"exploreSummaryCount,omitempty"`
	ReviewRoundsCount   *int   `json:"reviewRoundsCount,omitempty"`
}

// shipExploreSummaryFunc reads the plan explorer summary of one plan run.
// Tests replace it to force a read failure: planExploreSummary fails only on
// an OS read error that file permissions would cause.
var shipExploreSummaryFunc = planExploreSummary

// shipRemoveEvidenceFunc deletes the evidence directory of a plan run. Tests
// replace it to force a delete failure without touching file permissions.
var shipRemoveEvidenceFunc = os.RemoveAll

// planRun.reason values. A failed remove reports "remove failed: <error>". A
// failed copy of the explorer summary and review rounds reports
// shipPlanRunReasonCopyFailed, then "<error>", then shipPlanRunCopyRetryHint.
const (
	shipPlanRunReasonNotStamped   = "run not stamped"
	shipPlanRunReasonNoLinked     = "no linked plan run"
	shipPlanRunReasonNoReport     = "report not written"
	shipPlanRunReasonRemoveFailed = "remove failed: "
	shipPlanRunReasonCopyFailed   = "explorer summary and review rounds not saved: "
	shipPlanRunCopyRetryHint      = ". Fix the cause and call cleanup-pipeline again."
)

// shipPlanExploreSummaryKey is the ship state data key that holds the plan
// explorer summary copied at cleanup.
const shipPlanExploreSummaryKey = "planExploreSummary"

// shipPlanReviewRoundsKey is the ship state data key that holds the plan
// review rounds copied at cleanup. The rows have the shape of the plan state
// planReviewRoundsKey list.
const shipPlanReviewRoundsKey = "planReviewRounds"

// shipDeleteReportedPlanRun deletes the plan run linked to this ship run —
// its plan-<slug>-<ts>.json state file and its .evidence directory — once
// the ship report for this ship run is on disk. The link is the one the
// ship report itself follows: the branch's execute state's planPath, looked
// up with state.FindPlanRunByPlanFile. The report gate is
// <root>/.sdlc-v2/reports/ship-<ship runId>-report.<md|json>, where the
// runId is derived from the ship state's startedAt exactly as the report
// action derives it.
//
// Before the delete, it copies the plan explorer summary into ship.Data
// under "planExploreSummary" and writes the ship state. It also copies the
// plan review rounds under "planReviewRounds" when the plan run has any. The
// write comes before the evidence delete, because the delete loses the
// explorer data. If the summary read or the ship state write fails, nothing
// is deleted and the reason starts with shipPlanRunReasonCopyFailed, so a
// retry finds the plan run again. After a successful write, the result
// carries the number of stored summary entries and review rounds. A retry
// after a failed delete reads an empty summary. It then keeps a stored
// non-empty list. A plan run with no rounds leaves a stored list of rounds as
// it is.
//
// It fails safe: any lookup error, a missing startedAt, or a stat error
// other than not-exist deletes nothing. It never returns an error, so the
// gc sweep after it still runs on a run that is already stamped. The
// evidence directory is removed before the state file; if that remove
// fails, the state file stays so a retry can find the run again.
func shipDeleteReportedPlanRun(root, branch string, ship *state.State) ShipPlanRunCleanup {
	execSt, err := state.Find(root, "execute", branch)
	if err != nil || execSt == nil {
		return ShipPlanRunCleanup{Reason: shipPlanRunReasonNoLinked}
	}
	planRun, err := state.FindPlanRunByPlanFile(root, shipExecPlanPath(execSt.Data))
	if err != nil || planRun == nil {
		return ShipPlanRunCleanup{Reason: shipPlanRunReasonNoLinked}
	}

	if !shipReportWritten(root, ship.Data) {
		return ShipPlanRunCleanup{Reason: shipPlanRunReasonNoReport}
	}

	runID := state.RunID(planRun)
	summary, err := shipExploreSummaryFunc(root, runID)
	if err != nil {
		return ShipPlanRunCleanup{Reason: shipPlanRunReasonCopyFailed + err.Error() + shipPlanRunCopyRetryHint}
	}
	// A retry after the evidence delete reads []. Keep a stored non-empty list.
	summaryCount := len(summary)
	if prev, _ := ship.Data[shipPlanExploreSummaryKey].([]any); len(summary) > 0 || len(prev) == 0 {
		ship.Data[shipPlanExploreSummaryKey] = summary
	} else {
		summaryCount = len(prev)
	}
	// A plan run with no rounds leaves a stored list as it is.
	rounds, _ := planRun.Data[planReviewRoundsKey].([]any)
	if len(rounds) > 0 {
		ship.Data[shipPlanReviewRoundsKey] = rounds
	} else {
		rounds, _ = ship.Data[shipPlanReviewRoundsKey].([]any)
	}
	roundsCount := len(rounds)
	if err := shipStateWriteFunc(ship); err != nil {
		return ShipPlanRunCleanup{Reason: shipPlanRunReasonCopyFailed + err.Error() + shipPlanRunCopyRetryHint}
	}
	copied := ShipPlanRunCleanup{ExploreSummaryCount: &summaryCount, ReviewRoundsCount: &roundsCount}
	if err := shipRemoveEvidenceFunc(state.EvidenceDir(root, runID)); err != nil {
		copied.Reason = shipPlanRunReasonRemoveFailed + err.Error()
		return copied
	}
	if err := os.Remove(planRun.Path); err != nil && !os.IsNotExist(err) {
		copied.Reason = shipPlanRunReasonRemoveFailed + err.Error()
		return copied
	}
	copied.Deleted, copied.RunID = true, runID
	return copied
}

// shipReportWritten reports whether the ship report for the ship run in
// shipData exists as a regular file, in either format. A ship state with no
// startedAt has no real runId (execDeriveRunID falls back to "wave-0"), so
// it never counts as reported.
func shipReportWritten(root string, shipData map[string]any) bool {
	if startedAt, _ := shipData["startedAt"].(string); startedAt == "" {
		return false
	}
	runID := execDeriveRunID(shipData, 0)
	dir := filepath.Join(root, paths.DataDir, paths.ReportsSubdir)
	for _, ext := range []string{"md", "json"} {
		fi, err := os.Stat(filepath.Join(dir, "ship-"+runID+"-report."+ext))
		if err == nil && fi.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// shipStateCleanupPipeline ports cmdCleanupPipeline: force and no-state-file
// both skip the contract check but still fall through to the GC sweep; only
// an actual contract violation returns early before the sweep runs. Both the
// force and successful-cleanup and no-state-file branches reach the same
// 4-prefix (including commit) GC sweep afterward — confirmed by a full read
// of scripts/state/ship.js's cmdCleanupPipeline, which pre-seeds its report
// object with only {ship,execute,plan} but always executes a further section
// after the branch that adds a 4th "commit" bucket on every non-violation
// path.
//
// Flow: stamp -> plan-run deletion (shipDeleteReportedPlanRun, only when the
// stamp landed) -> gc sweep -> reap run directories.
func shipStateCleanupPipeline(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	branch, err := execResolveBranch(detailStr(in.Detail, "branch"), workDir)
	if err != nil {
		return nil, err
	}
	st, findErr := state.Find(root, "ship", branch)
	if findErr != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("find ship state for branch %q: %s", branch, findErr.Error()),
			Suggestion: "Check read permission on " + paths.DataDir + "/runs/ship-<branch-slug>-*.json and that the directory exists, then retry ship_state cleanup-pipeline.",
			Cause:      findErr,
		}
	}

	force, err := shipDetailBool(in.Detail, "cleanup-pipeline", "force",
		"force:true skips the contract check and the stamp; omit it for a normal cleanup.")
	if err != nil {
		return nil, err
	}
	ttlDays := resolveGCTTLDays(root, detailIntPtr(in.Detail, "ttlDays"))

	var currentRun map[string]any
	var issueSummary *IssueSummary
	runStamped := false
	switch {
	case force:
		currentRun = map[string]any{"cleaned": false, "preservedReason": "force"}
	case st == nil:
		currentRun = map[string]any{"valid": true, "cleaned": false, "reason": "no-state-file"}
	default:
		valid, violations := shipValidatePipelineContract(st.Data)
		if !valid {
			return nil, &mcpserver.DataError{
				Msg: fmt.Sprintf(
					"pipeline contract violation: %d step(s) not in terminal state (%s) — state file preserved",
					len(violations), shipFormatViolations(violations)),
				Suggestion: "Complete, skip, or fail each step listed above so it reaches a terminal state, then retry ship_state cleanup-pipeline.",
			}
		}
		completedAt := now().UTC().Format(time.RFC3339)
		st.Data["pipelineStatus"] = "completed"
		st.Data["pipelineCompletedAt"] = completedAt
		if err := state.Write(st); err != nil {
			return nil, &mcpserver.InfraError{
				Msg:        fmt.Sprintf("write ship state to %s: %s", st.Path, err.Error()),
				Suggestion: "Check write permission on the ship state file path above and free disk space on the project root, then retry ship_state cleanup-pipeline.",
				Cause:      err,
			}
		}
		runStamped = true
		currentRun = map[string]any{
			"valid":               true,
			"cleaned":             true,
			"pipelineStatus":      "completed",
			"pipelineCompletedAt": completedAt,
		}
		issueSummary = execIssueSummaryFull(st.Data)
	}

	// Plan-run deletion runs only after a successful stamp: force and
	// no-state-file never stamp, and a contract violation or a failed stamp
	// write has already returned above.
	planRun := ShipPlanRunCleanup{Reason: shipPlanRunReasonNotStamped}
	if runStamped {
		planRun = shipDeleteReportedPlanRun(root, branch, st)
	}

	stateDir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	rpt, err := shipGCFunc(root, state.GCOptions{
		TTL:          time.Duration(ttlDays) * 24 * time.Hour,
		BranchExists: gcBranchExistsFunc(workDir),
		TempDir:      os.Getenv("SDLC_EXPLORE_TMPDIR_OVERRIDE"),
	})
	if err != nil {
		msg := fmt.Sprintf("gc sweep over %s: %s", stateDir, err.Error())
		if runStamped {
			msg = fmt.Sprintf("run is already marked completed; only the gc sweep over %s failed: %s", stateDir, err.Error())
		}
		return nil, &mcpserver.InfraError{
			Msg:        msg,
			Suggestion: "Check that no other process holds a lock on " + paths.DataDir + "/runs/ and that files there are not corrupted. Then call ship_state gc to retry only the sweep, with the same detail.ttlDays if you set one.",
			Cause:      err,
		}
	}

	reapResult := execReapRunDirectories(stateDir, ttlDays, false, now)

	out := map[string]any{
		"currentRun": currentRun,
		"gc": map[string]any{
			"ship":    bucketGCByPrefix(rpt, "ship"),
			"execute": bucketGCByPrefix(rpt, "execute"),
			"plan":    bucketGCByPrefix(rpt, "plan"),
			"commit":  bucketGCByPrefix(rpt, "commit"),
		},
		"directories": reapResult,
		"force":       force,
		"ttlDays":     ttlDays,
		"planRun":     planRun,
	}
	if issueSummary != nil {
		out["issueSummary"] = issueSummary
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Action: gc (standalone; dry-run classifier + real sweep)
// ---------------------------------------------------------------------------

// shipDryRunStateFileRE matches a ship/execute/plan/commit state file
// basename for gc's dry-run classifier. state.go's own equivalent regex is
// unexported, so this duplicates its 4-prefix grammar; the dry-run bucket
// map below narrows to the 3 prefixes cmdGc's --dry-run branch recognizes.
var shipDryRunStateFileRE = regexp.MustCompile(`^(ship|execute|plan|commit)-(.+)-\d{8}T\d{6}Z\.json$`)

func shipStateGC(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	ttlDays := resolveGCTTLDays(root, detailIntPtr(in.Detail, "ttlDays"))

	// gc deletes state files when dryRun is absent or false, so a mistyped
	// or misplaced flag must fail loud rather than fall through to the real
	// sweep: detailBool reports false for any non-bool value (the string
	// "true" included), and a top-level dryRun argument never reaches Detail
	// at all.
	dryRun, err := shipDetailBool(in.Detail, "gc", "dryRun",
		"Omit it to run the real sweep. dryRun is read from detail, never from the top level of the arguments.")
	if err != nil {
		return nil, err
	}

	if dryRun {
		return shipGCDryRun(filepath.Join(root, paths.DataDir, paths.RunsSubdir), ttlDays, gcBranchExistsFunc(workDir), now)
	}

	rpt, err := shipGCFunc(root, state.GCOptions{
		TTL:          time.Duration(ttlDays) * 24 * time.Hour,
		BranchExists: gcBranchExistsFunc(workDir),
		TempDir:      os.Getenv("SDLC_EXPLORE_TMPDIR_OVERRIDE"),
	})
	if err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("gc sweep over %s: %s", filepath.Join(root, paths.DataDir, paths.RunsSubdir), err.Error()),
			Suggestion: "Check that no other process holds a lock on " + paths.DataDir + "/runs/ and that files there are not corrupted, then retry ship_state gc.",
			Cause:      err,
		}
	}

	return ShipStateGCReport{
		TTLDays: ttlDays,
		Ship:    bucketGCByPrefix(rpt, "ship"),
		Execute: bucketGCByPrefix(rpt, "execute"),
		Plan:    bucketGCByPrefix(rpt, "plan"),
		Commit:  bucketGCByPrefix(rpt, "commit"),
	}, nil
}

// shipGCDryRun enumerates the state directory without deleting anything.
// Only ship/execute/plan are classified — a commit-prefixed file's bucket
// lookup misses and is silently skipped, matching JS's `if (!bucket)
// continue`. Each entry is classified by state.ClassifyGCFile, the same rule
// state.GC applies on a real run, so wouldDelete lists exactly what a real
// run deletes. Its reason is that function's reason string.
func shipGCDryRun(stateDir string, ttlDays int, branchExists func(string) bool, now func() time.Time) (any, error) {
	buckets := map[string]map[string]any{
		"ship":    {"wouldDelete": []any{}, "wouldKeep": []any{}},
		"execute": {"wouldDelete": []any{}, "wouldKeep": []any{}},
		"plan":    {"wouldDelete": []any{}, "wouldKeep": []any{}},
	}

	entries, err := os.ReadDir(stateDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read state directory %s: %s", stateDir, err.Error()),
			Suggestion: "Check read permission on " + paths.DataDir + "/runs/, then retry ship_state gc with detail.dryRun true.",
			Cause:      err,
		}
	}

	nowMs := now().UnixMilli()
	ttlMs := int64(ttlDays) * 86400000

	// First pass: stat every classified file and find the newest mtime of
	// each prefix+branch group; the rule needs it to spare a live branch's
	// newest file.
	type gcFile struct {
		name, prefix, slug string
		mtimeMs            int64
	}
	var files []gcFile
	newestMs := map[string]int64{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		m := shipDryRunStateFileRE.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		prefix, slug := m[1], m[2]
		if _, ok := buckets[prefix]; !ok {
			continue // "commit" (and anything unrecognized) is silently skipped, matching cmdGc's dry-run.
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}
		f := gcFile{name: name, prefix: prefix, slug: slug, mtimeMs: info.ModTime().UnixMilli()}
		files = append(files, f)
		key := prefix + "\x00" + slug
		if cur, seen := newestMs[key]; !seen || f.mtimeMs > cur {
			newestMs[key] = f.mtimeMs
		}
	}

	// Second pass: classify with the rule state.GC applies on a real run.
	// fresh uses <= to match state.GC's cutoff (mtime not before now-TTL).
	for _, f := range files {
		fresh := (nowMs - f.mtimeMs) <= ttlMs
		branchLive := branchExists != nil && branchExists(f.slug)
		newest := f.mtimeMs == newestMs[f.prefix+"\x00"+f.slug]
		del, reason := state.ClassifyGCFile(branchLive, newest, fresh)
		entry := map[string]any{"file": f.name, "branch": f.slug, "reason": reason}
		bucket := buckets[f.prefix]
		if del {
			bucket["wouldDelete"] = append(bucket["wouldDelete"].([]any), entry)
		} else {
			bucket["wouldKeep"] = append(bucket["wouldKeep"].([]any), entry)
		}
	}

	return map[string]any{
		"dryRun":  true,
		"ttlDays": ttlDays,
		"ship":    buckets["ship"],
		"execute": buckets["execute"],
		"plan":    buckets["plan"],
	}, nil
}

// ---------------------------------------------------------------------------
// Action: migrate
// ---------------------------------------------------------------------------

// shipStateMigrate ports cmdMigrate via the shared state.MigrateBranchSlug
// primitive, per "reuse, don't reimplement." Two disclosed gaps
// inherited from that primitive, neither patched here:
//  1. state.MigrateBranchSlug is a pure filename rename; it never rewrites
//     data["branch"] inside the JSON payload, unlike ship.js's richer
//     migrateBranchSlug library function.
//  2. state.MigrateBranchSlug returns only an error, not a count/bool of
//     files actually renamed, so this handler cannot distinguish "renamed N
//     files" from "found nothing to rename" the way cmdMigrate's
//     exit(migrated?0:1) does — a successful call always reports
//     migrated:true here, even when zero files matched oldSlug.
func shipStateMigrate(root string, in ShipStateIn) (any, error) {
	from := detailStr(in.Detail, "from")
	to := detailStr(in.Detail, "to")
	if from == "" || to == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "migrate: from and to are required",
			Suggestion: "Pass detail.from and detail.to as the old and new branch names, then retry ship_state migrate.",
		}
	}
	// from/to are branch names, but MigrateBranchSlug matches against
	// slug-shaped filename fragments — slugify both first. Safe even when
	// the caller already passed slugs: SlugifyBranch is idempotent on
	// already-slug input.
	oldSlug := state.SlugifyBranch(from)
	newSlug := state.SlugifyBranch(to)
	if err := state.MigrateBranchSlug(root, oldSlug, newSlug); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("migrate ship state files from slug %q to %q: %s", oldSlug, newSlug, err.Error()),
			Suggestion: "Check write permission on " + paths.DataDir + "/runs/ for both the old and new branch slugs above, then retry ship_state migrate.",
			Cause:      err,
		}
	}
	return map[string]any{"migrated": true}, nil
}

// ---------------------------------------------------------------------------
// Action: next (Go-native; drives the executor loop)
// ---------------------------------------------------------------------------

func shipStateNext(root, workDir string, in ShipStateIn) (any, error) {
	st, err := shipLoadState(root, workDir, in)
	if err != nil {
		return nil, err
	}

	var nextStep string
	for _, s := range shipStepsSlice(st.Data) {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if shipStepBlocksProceed(sm) {
			nextStep, _ = sm["name"].(string)
			break
		}
	}

	if nextStep == "" {
		return ShipNextOut{}, nil
	}

	automationMode := "confirm"
	if cfg, cfgErr := config.Read(root); cfgErr == nil && cfg.Automation != nil {
		automationMode = cfg.Automation.StepMode(nextStep)
	}

	return ShipNextOut{Step: nextStep, Automation: automationMode}, nil
}

// ---------------------------------------------------------------------------
// Action: todos (Go-native; TodoWrite fold)
// ---------------------------------------------------------------------------

func shipStateTodos(root, workDir string, in ShipStateIn) (any, error) {
	st, err := shipLoadState(root, workDir, in)
	if err != nil {
		return nil, err
	}
	return ShipTodosOut{Todos: shipmeta.TodosForStep(in.Step, st)}, nil
}

// ---------------------------------------------------------------------------
// Action: history_record — append a pipeline run record to .sdlc-v2/history/runs.jsonl
// ---------------------------------------------------------------------------

// historyDir is a thin alias for paths.HistoryDir so this file keeps one
// short local name for a path it joins in many places. The literal
// "history" subdirectory lives in internal/paths and is never repeated here.
func historyDir(root string) string {
	return paths.HistoryDir(root)
}

// historyWriter returns the history.Writer used by the durable deferred
// writes that shipStateDefer and execActionIssueDraft make at creation time
// (KD-1). It is a package-level var purely so tests can substitute
// history.MemWriter and never touch the real filesystem; the deferred_add /
// deferred_list / deferred_resolve / deferred_propose_followups actions keep
// constructing their own FileWriter directly, so this seam cannot change
// their behaviour.
var historyWriter = func(root string) history.Writer {
	return history.NewFileWriter(historyDir(root))
}

// persistDeferred writes issue to .sdlc-v2/history/deferred.json unless an
// entry with the same ID is already there.
//
// The store itself stays append-only with no dedup — deferred_add's
// contract depends on that. The skip lives here instead, for a caller that
// re-sends the same id directly (a direct persistDeferred call in a test).
// deferred_add does NOT go through this function: it calls AddDeferred
// directly and appends with no id check, so sending it the same id twice
// stores two entries.
//
// It does NOT protect shipStateDefer or execActionIssueDraft against their
// own retries: both mint id from timestamp+count at call time (fresh state
// on every invocation), so a caller that re-runs defer/issue-draft after a
// failed persist gets a brand-new id, not the one that just failed. That
// retry is not a recovery — it adds a second run-scoped entry while the
// first stays lost — which is exactly why deferredPersistWarning below
// points the caller at deferred_add instead of "try again".
func persistDeferred(root string, issue history.DeferredIssue) error {
	w := historyWriter(root)
	existing, err := w.ListDeferred()
	if err != nil {
		return err
	}
	for _, e := range existing {
		if e.ID == issue.ID {
			return nil
		}
	}
	return w.AddDeferred(issue)
}

// deferredPersistWarning renders the sentence a handler surfaces when
// persistDeferred failed. The write is best-effort — losing deferred.json
// must not fail the caller's actual work — but it is never swallowed: a
// silent failure of the fix for silent loss would be worse than the
// original bug.
//
// The sentence carries no leading space: every caller adds its own
// separator (shipStateDefer appends to a summary, execActionIssueDraft
// assigns it to a Warning field), and a helper with two spacing
// conventions is one the next caller gets wrong.
//
// id and title are the failed item's, so the text can name a concrete
// recovery instead of only naming the loss. Repeating the original call is
// NOT that recovery — it appends a second run-scoped entry under a fresh
// id rather than completing the first one — so the instruction points at
// deferred_add, which writes the durable store directly.
func deferredPersistWarning(err error, id, title string) string {
	return fmt.Sprintf(
		"WARNING: could not persist this item to %s/history/deferred.json (%s)."+
			" It is recorded on the run-scoped state file only, and will be lost when that file is garbage-collected."+
			" Recover it with ship_state action=deferred_add (detail.id=%q, detail.description=%q)."+
			" Do not repeat the original call — it records a second entry instead of recovering this one.",
		paths.DataDir, err.Error(), id, title)
}

// priorityFromSeverity maps a review finding's severity onto the three
// priority buckets DeferredByPriority groups on. The cases cover
// dimensions.ValidSeverities exactly — shipStateDefer rejects anything else
// before calling this, so the fallback is only reached by callers that do
// not validate first; it stays "medium", matching deferred_add's default.
func priorityFromSeverity(severity string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "high":
		return history.PriorityHigh
	case "medium":
		return history.PriorityMedium
	case "low", "info":
		return history.PriorityLow
	}
	return history.PriorityMedium
}

func shipStateHistoryRecord(root string, in ShipStateIn) (any, error) {
	d := in.Detail
	if d == nil {
		return nil, &mcpserver.DomainError{
			Msg:        "history_record: detail with run record fields is required",
			Suggestion: "Pass detail.skill and detail.outcome (plus optional ts, branch, duration_ms, version), then retry history_record.",
		}
	}

	rec := history.RunRecord{
		Timestamp:  detailStr(d, "ts"),
		Skill:      detailStr(d, "skill"),
		Branch:     detailStr(d, "branch"),
		Outcome:    detailStr(d, "outcome"),
		DurationMs: detailInt64(d, "duration_ms"),
		Version:    detailStr(d, "version"),
	}
	if rec.Timestamp == "" {
		rec.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if rec.Skill == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "history_record: detail.skill is required",
			Suggestion: "Pass detail.skill naming the skill that ran (e.g. \"ship\"), then retry history_record.",
		}
	}
	if rec.Outcome == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "history_record: detail.outcome is required",
			Suggestion: "Pass detail.outcome as one of \"success\", \"failure\" or \"partial\", then retry history_record.",
		}
	}
	switch rec.Outcome {
	case "success", "failure", "partial":
	default:
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf(`history_record: detail.outcome must be "success", "failure" or "partial", got %q`, rec.Outcome),
			Suggestion: "Pass detail.outcome as one of \"success\", \"failure\" or \"partial\", then retry history_record.",
		}
	}

	rec.Steps = detailStrSlice(d, "steps")
	rec.GuardrailHits = detailStrSlice(d, "guardrail_hits")
	rec.DeferredIssues = detailStrSlice(d, "deferred_issues")

	w := history.NewFileWriter(historyDir(root))
	if err := w.AppendRun(rec); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("append run record to %s: %s", w.RunsPath(), err.Error()),
			Suggestion: "Check write permission on " + paths.DataDir + "/history/runs.jsonl and free disk space on the project root, then retry history_record.",
			Cause:      err,
		}
	}
	return map[string]any{"ok": true, "ts": rec.Timestamp}, nil
}

// ---------------------------------------------------------------------------
// Action: deferred_add — add a deferred issue to .sdlc-v2/history/deferred.json
// ---------------------------------------------------------------------------

func shipStateDeferredAdd(root string, in ShipStateIn) (any, error) {
	d := in.Detail
	if d == nil {
		return nil, &mcpserver.DomainError{
			Msg:        "deferred_add: detail with issue fields is required",
			Suggestion: "Pass detail.id and detail.description (plus optional created, source, priority), then retry deferred_add.",
		}
	}

	issue := history.DeferredIssue{
		ID:          detailStr(d, "id"),
		Created:     detailStr(d, "created"),
		Source:      detailStr(d, "source"),
		Priority:    detailStr(d, "priority"),
		Description: detailStr(d, "description"),
		Status:      history.StatusOpen,
	}
	if issue.ID == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "deferred_add: detail.id is required",
			Suggestion: "Pass detail.id as a unique string identifying this deferred issue, then retry deferred_add.",
		}
	}
	if issue.Description == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "deferred_add: detail.description is required",
			Suggestion: "Pass detail.description describing the deferred issue, then retry deferred_add.",
		}
	}
	if issue.Created == "" {
		issue.Created = time.Now().UTC().Format(time.RFC3339)
	}
	if issue.Priority == "" {
		issue.Priority = history.PriorityMedium
	}

	w := history.NewFileWriter(historyDir(root))
	if err := w.AddDeferred(issue); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("add deferred issue to %s: %s", w.DeferredPath(), err.Error()),
			Suggestion: "Check write permission on " + paths.DataDir + "/history/deferred.json and free disk space on the project root, then retry deferred_add.",
			Cause:      err,
		}
	}
	return map[string]any{"ok": true, "id": issue.ID}, nil
}

// ---------------------------------------------------------------------------
// Action: deferred_resolve — mark a deferred issue as resolved by ID
// ---------------------------------------------------------------------------

func shipStateDeferredResolve(root string, in ShipStateIn) (any, error) {
	d := in.Detail
	if d == nil {
		return nil, &mcpserver.DomainError{
			Msg:        "deferred_resolve: detail with id field is required",
			Suggestion: "Pass detail.id naming the deferred issue to resolve, then retry deferred_resolve.",
		}
	}
	id := detailStr(d, "id")
	if id == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "deferred_resolve: detail.id is required",
			Suggestion: "Pass detail.id naming the deferred issue to resolve, then retry deferred_resolve.",
		}
	}
	w := history.NewFileWriter(historyDir(root))
	if err := w.ResolveDeferred(id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("deferred_resolve: %s", err.Error()),
				Suggestion: "Call ship_state deferred_list to see valid open issue ids, then retry deferred_resolve with a matching id.",
				Cause:      err,
			}
		}
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("resolve deferred issue %q in %s: %s", id, w.DeferredPath(), err.Error()),
			Suggestion: "Check write permission on " + paths.DataDir + "/history/deferred.json, then retry deferred_resolve.",
			Cause:      err,
		}
	}
	return map[string]any{"ok": true, "id": id}, nil
}

// ---------------------------------------------------------------------------
// Action: deferred_list — list all deferred issues
// ---------------------------------------------------------------------------

func shipStateDeferredList(root string) (any, error) {
	w := history.NewFileWriter(historyDir(root))
	issues, err := w.ListDeferred()
	if err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read deferred issues from %s: %s", w.DeferredPath(), err.Error()),
			Suggestion: "Check read permission on " + paths.DataDir + "/history/deferred.json and that it is not corrupted, then retry deferred_list.",
			Cause:      err,
		}
	}
	if issues == nil {
		issues = []history.DeferredIssue{}
	}
	open := history.OpenDeferred(issues)
	return map[string]any{
		"issues":    issues,
		"openCount": len(open),
	}, nil
}

// ---------------------------------------------------------------------------
// Action: deferred_propose_followups — return open issues grouped by priority
// ---------------------------------------------------------------------------

func shipStateDeferredProposeFollowups(root string) (any, error) {
	w := history.NewFileWriter(historyDir(root))
	issues, err := w.ListDeferred()
	if err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read deferred issues from %s: %s", w.DeferredPath(), err.Error()),
			Suggestion: "Check read permission on " + paths.DataDir + "/history/deferred.json and that it is not corrupted, then retry deferred_propose_followups.",
			Cause:      err,
		}
	}
	if issues == nil {
		issues = []history.DeferredIssue{}
	}
	open := history.OpenDeferred(issues)
	groups := history.DeferredByPriority(issues)
	summary := history.FormatDeferredSummary(issues)

	return map[string]any{
		"openCount": len(open),
		"groups":    groups,
		"display":   summary,
	}, nil
}

// ---------------------------------------------------------------------------
// Action: log-cli
// ---------------------------------------------------------------------------

// shipStateLogCLI logs a CLI execution to the evidence JSONL file.
func shipStateLogCLI(root, workDir string, in ShipStateIn) (any, error) {
	branch, err := execResolveBranch(detailStr(in.Detail, "branch"), workDir)
	if err != nil {
		return nil, err
	}

	exitCode := 0
	if ptr := detailIntPtr(in.Detail, "exitCode"); ptr != nil {
		exitCode = *ptr
	}

	entry := CLIEvidenceEntry{
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Pipeline:   "ship",
		Step:       detailStr(in.Detail, "step"),
		Branch:     branch,
		Command:    detailStr(in.Detail, "command"),
		ExitCode:   exitCode,
		OutputHead: detailStr(in.Detail, "outputHead"),
	}

	if err := appendCLIEvidence(root, entry); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("log-cli: append CLI evidence to %s: %s", cliEvidencePath(root), err.Error()),
			Suggestion: "Check write permission on the CLI evidence file named above and free disk space on the project root, then retry.",
			Cause:      err,
		}
	}

	return map[string]any{"ok": true, "action": "log-cli"}, nil
}

// detailInt64 reads a numeric value from the detail map as int64.
func detailInt64(d map[string]any, key string) int64 {
	v, ok := d[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}

// detailStrSlice reads a []string from the detail map.
func detailStrSlice(d map[string]any, key string) []string {
	v, ok := d[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterShipStateTools registers the ship_state tool. Registration only;
// runMCP wires it into the server.
func RegisterShipStateTools(s *mcpserver.Server) {
	mcpserver.Register(s, "ship_state",
		`Manage ship execution state.

Pass "action" to select an operation. Each action uses a subset of the input fields (unlisted fields are ignored).

Mutating actions (begin-step, complete-step, start, complete, skip, fail, decide, defer) return a narrated response: summary (short text), display (markdown progress block), timing (step/pipeline/idle durations when computable), next (the following pipeline step when one exists), and for begin-step/complete-step: todos, issueCount, issueHighlights. Pass detail.detail="concise" to omit the progress block on non-boundary actions (skip, fail, decide, defer); "full" (the default) always includes it. Step durations are recorded under key "ship:<step>" in the timings store; human-wait steps (await-remote-review) are never recorded.

- init: Create ship state. Optional: detail.branch, detail.flags, sessionId.
- begin-step: Begin execution of a step (preferred over start). Requires step. Returns narration with progress, ETA, dispatch instruction, todos, and alreadyDone (true when ship_verify_side_effect already recorded this step's side effect in the sideEffects journal — a resumed pipeline can skip redoing it). Optional: detail.branch, detail.stateFile, detail.detail.
- complete-step: Complete execution of a step (preferred over complete). Requires step. Returns narration with timing, next step, todos, and issue summary. Optional: detail.outcome ("success"|"failure"), detail.result, detail.branch, detail.stateFile, detail.detail.
- commit-check: Decide the commit step from the working tree. Requires: (none). Optional: detail.branch. Side effects: staging (git add -A -- ':!.sdlc-v2/' in the active worktree stages every change, untracked files that are not gitignored included; then git diff --cached --name-only counts the staged paths); the first call of a run stores HEAD in state key commitBaseHead, and later calls keep it. Dirty tree: returns {clean:false, stagedCount} and changes no step. Clean tree with HEAD equal to commitBaseHead: appends one decide entry and does what complete-step does for the commit step, with result "`+commitNothingPrefix+`: execute committed N wave commit(s)" (N = non-empty waves[].committedSha of this branch's execute state) or "`+commitNothingPrefix+`: the working tree is clean" (N = 0). Clean tree with HEAD not equal to commitBaseHead: records HEAD in the side-effect journal (sideEffects, kind sha) and does what complete-step does for the commit step, with result "committed <short sha>". Returns clean, stagedCount, waveCommits, stepCompleted, result, todos and display (when stepCompleted), warnings, next. Errors: a failed git call is a git InfraError; a commit step that is not in_progress is a step-state DomainError; a pipeline with no commit step, or a commitBaseHead that is not a string, is a DataError.
- start: (Legacy) Begin a step. Requires step. Returns narration. Optional: detail.branch, detail.detail.
- complete: (Legacy) Complete a step. Requires step. Returns narration with timing. Optional: detail.branch, detail.result, detail.detail.
- skip: Skip a step. Requires step. Returns narration. Optional: detail.branch, detail.reason, detail.detail.
- fail: Fail a step. Requires step. Returns narration. The first fail of a run also appends one failure row to .sdlc-v2/history/runs.jsonl (state key historyFailureRecorded stops a second row); a failed append does not fail the call but is named in warnings, with the history_record call that adds the row. Optional: detail.branch, detail.error (recorded as issue), detail.detail.
- decide: Record a decision. Requires step. Returns narration. Optional: detail.branch, detail.text, detail.detail.
- defer: Record a deferred finding. Writes it both to the run-scoped ship state file and durably to .sdlc-v2/history/deferred.json (with source detail.source, default "`+history.SourceReviewBelowThreshold+`"), so it survives state-file GC — no follow-up deferred_add is needed. Returns narration naming the generated deferred id (review-deferred-<timestamp>-<N>) and the file it was written to; a failed deferred.json write does not fail the call but is named in the summary, with the deferred_add call that recovers it. Requires detail.severity (one of `+strings.Join(dimensions.ValidSeverities, " | ")+`, case-insensitive; the lowercase form is recorded), detail.file, detail.title. Optional: detail.branch, detail.line (integer), detail.detail, detail.description (the deferring agent's own reasoning; defaults to detail.title), detail.reason (one of `+strings.Join(history.DeferredReasons(), " | ")+`; an omitted reason records `+history.ReasonBelowThreshold+`), detail.source (the tool recording the deferral, e.g. "received-review"; defaults to "`+history.SourceReviewBelowThreshold+`").
- healing_record: Record one self-healing change in the live ship run's data.healing. Requires detail.kind: "review-total" (Requires detail.total, detail.dimensions — non-negative integers; replaces the previous value) | "fixed" (Requires detail.origin "local-review"|"pr-comment", detail.severity (one of `+strings.Join(dimensions.ValidSeverities, " | ")+`), detail.file, detail.title; Optional detail.line) | "hardened" (Requires detail.phase "started"|"done", detail.trigger, detail.classification, detail.applied [{surface (one of `+strings.Join(healingSurfaceIDs(), " | ")+`), action, targetFile}], detail.skipped (non-negative integer); a "done" record replaces a "started" record with the same trigger) | "fix-progress" (Requires detail.origin, detail.severity, detail.file, detail.title, detail.status (`+strings.Join(healingFixStatuses, " | ")+`); Optional detail.line. Upserts data.healing.fixProgress[] on the key (origin, file, line, title), keeps firstAt, sets updatedAt; deferred never replaces failed; max `+fmt.Sprint(healingFixProgressMax)+` records; the response carries next with the instruction for the fix pass). Optional: detail.branch. Duplicates are ignored (narration "already recorded — no change"). With no live ship run (no state, or pipelineCompletedAt set) it returns ok and records nothing. Returns summary, kind, written (true only when this call changed the state file) and record (the validated record as persisted, recordedAt included).
- harden_clusters: Group review findings into harden clusters (key = file; lone-disagree files dropped; cap 5). Requires detail.findings [{file, severity, title, body, verdict: "agree-will-fix"|"agree-won't-fix"|"disagree"|"needs-direction", reason? (one of `+strings.Join(history.DeferredReasons(), " | ")+`)}]. Optional: detail.branch (the ship run whose healing.hardened triggers set alreadyHardened). failureText has every double quote replaced by a single quote and every backslash by a slash, so it is safe inside a quoted --failure-text argument. Returns clusters with failureText and alreadyHardened, suppressed, loneDisagree, and dirtySurfaces (harden surfaces with uncommitted edits in the active worktree). Works without ship state.
- read: Return the full ship state. Optional: detail.branch. The response also carries "reportData": report-ready aggregates, including healing (data.healing verbatim, {} when absent) and reviewLedger {total, fixed (local-review only), deferredByReason, unaccounted = total - fixed - deferred, never clamped} — reviewLedger is null, with reviewLedgerNote, when no review total was recorded. Also returns style: the plugin-wide communication style; follow style.guide in chat and questions. When the pipeline is in flight (not stamped pipelineStatus:"completed", some step still blocks proceed, and at least one step has been started), the state also carries a "resumeBriefing" (resumable, lastStep, lastStepStatus, sideEffects, summary, display, timing{stepSeconds,pipelineSeconds,idleSeconds,human}, next). A step left "failed" is still reported resumable:true, never as an error.
- report: Compose the end-of-run report from ship state, healing records, this run's execute state (only when the execute step completed) and its linked plan run, the review run ledger of this ship run (for the Review waves section), CLI evidence, user input and learnings, and render it. Optional: detail.write (true persists it under <main worktree>/.sdlc-v2/reports/), detail.format ("md"|"json", default from automation.report.format), detail.branch. Returns {skipped:true} when automation.report.enabled is false.
- cleanup: Stamp a branch's ship state terminal (pipelineStatus:"completed", pipelineCompletedAt) instead of deleting it, after validating every step is in a terminal state — the state survives for later reads until GC's TTL prunes it. Optional: detail.branch.
- cleanup-pipeline: Same stamp-instead-of-delete for the current branch's ship state (force/no-state-file skip the contract check). Only after a successful stamp, it deletes the plan run linked through this branch's execute state (its plan-<slug>-<ts>.json and .evidence directory) when the ship report ship-<runId>-report.<md|json> exists; force and no-state-file never delete it. Before it deletes the plan run, it copies the explorer summary into ship state planExploreSummary and the plan review rounds (when the plan run has any) into ship state planReviewRounds, in one ship state write. If the copy fails, the plan run stays and planRun.reason starts with "explorer summary and review rounds not saved: ". Fix the cause and call cleanup-pipeline again. The result's planRun is {deleted, runId?, reason?, exploreSummaryCount?, reviewRoundsCount?} with reason "run not stamped" | "no linked plan run" | "report not written" | "explorer summary and review rounds not saved: <error>. Fix the cause and call cleanup-pipeline again." | "remove failed: <error>". exploreSummaryCount and reviewRoundsCount are present only after the copy write succeeded: the number of entries ship state now holds in planExploreSummary and planReviewRounds. Then an unconditional GC + per-run-directory sweep. Optional: detail.branch, detail.force, detail.ttlDays.
- gc: Garbage-collect stale state files. Optional: detail.ttlDays, detail.dryRun.
- migrate: Migrate state between branches. Requires detail.from, detail.to.
- next: Return the next pending step. Optional: detail.branch, detail.stateFile.
- todos: List remaining todos for a step. Optional: step, detail.branch, detail.stateFile.
- history_record: Append a pipeline run record to .sdlc-v2/history/runs.jsonl (persistent, survives state-file GC). Requires detail.skill, detail.outcome ("success"|"failure"|"partial"). Optional: detail.ts (ISO timestamp, defaults to now), detail.branch, detail.duration_ms, detail.steps, detail.guardrail_hits, detail.deferred_issues, detail.version.
- deferred_add: Add a deferred issue to .sdlc-v2/history/deferred.json. Requires detail.id, detail.description. Optional: detail.created (defaults to now), detail.source, detail.priority ("high"|"medium"|"low", defaults to "medium").
- deferred_list: List all deferred issues. Returns {issues, openCount}.
- deferred_propose_followups: Return open deferred issues grouped by priority with a formatted display summary. Returns {openCount, groups, display}.
- deferred_resolve: Mark a deferred issue as resolved by ID. Requires detail.id. Returns {ok, id}. Errors if the ID is not found.`,
		mcpserver.Annotations{
			Title:       "Read or update ship run state",
			ReadOnly:    false,
			Destructive: true,
			Idempotent:  false,
			OpenWorld:   false,
		},
		func(ctx mcpserver.Ctx, in ShipStateIn) (any, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return nil, &mcpserver.InfraError{
					Msg:        fmt.Sprintf("resolve project root: %s", err.Error()),
					Suggestion: "Check that the current directory is inside a git worktree with a valid " + paths.DataDir + " project root, then retry.",
					Cause:      err,
				}
			}
			workDir, err := worktree.ActiveRoot()
			if err != nil {
				workDir = root
			}
			return shipState(root, workDir, in, time.Now)
		},
	)
}
