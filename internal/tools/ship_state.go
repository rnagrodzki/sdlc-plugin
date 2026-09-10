package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
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
	Action    string         `json:"action" jsonschema_description:"Operation to perform: init, begin-step, complete-step, start (legacy), complete (legacy), skip, fail, decide, defer, read, cleanup, cleanup-pipeline, gc, or migrate. Each action uses a subset of the other fields (unlisted fields are ignored)."`
	Step      string         `json:"step,omitempty" jsonschema_description:"Pipeline step name. Required by begin-step, complete-step, start, complete, skip, fail, decide; ignored by other actions."`
	Detail    map[string]any `json:"detail,omitempty" jsonschema_description:"Action-specific extra fields (e.g. branch, flags, outcome, result, reason, error, text, severity, file, title, line, force, ttlDays, dryRun, from, to, detail). See the action list for which sub-fields each action reads."`
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
}

// ShipNextOut is the output of the Go-native next action: the first step
// that still needs work (in_progress, failed, or a bare pending step with no
// condition key — the same predicate as begin-step's R-b1 proceed-gate,
// walked over the whole steps[] array instead of a slice up to some index)
// and its resolved automation mode (config.AutomationSection.StepMode).
// Step is "" when every step is terminal-OK (pipeline complete) — no error,
// since "nothing left to do" is an expected steady state for an executor
// loop (KD14), not a failure.
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

func shipFindStepEntry(data map[string]any, name string) map[string]any {
	for _, s := range shipStepsSlice(data) {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if sm["name"] == name {
			return sm
		}
	}
	return nil
}

// shipStepAlreadyDone reports whether data["sideEffects"] already holds a
// verified journal entry for step (written by ship.go's
// shipVerifySideEffect/shipRecordSideEffect). Used by begin-step to signal a
// resumed pipeline that this step's side effect already landed.
func shipStepAlreadyDone(data map[string]any, step string) bool {
	_, ok := shipSideEffectEntry(data, step)
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
// the named step. Returns (0, total) if the step is not found.
func shipStepPosition(data map[string]any, stepName string) (pos, total int) {
	steps := shipStepsSlice(data)
	total = len(steps)
	for i, s := range steps {
		sm, ok := s.(map[string]any)
		if ok && sm["name"] == stepName {
			return i + 1, total
		}
	}
	return 0, total
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
// before the named step that has one, or "" if none.
func shipPrevCompletedAt(data map[string]any, stepName string) string {
	var prev string
	for _, s := range shipStepsSlice(data) {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := sm["name"].(string); name == stepName {
			return prev
		}
		if ca, _ := sm["completedAt"].(string); ca != "" {
			prev = ca
		}
	}
	return ""
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

// shipCompletionTiming computes timing info for a completed step, records
// the step duration to TimingsStore (except for HumanWaitSteps), and
// returns both the timing and the store for reuse in ETA lookups.
func shipCompletionTiming(root string, data map[string]any, stepName, startedAt, completedAt string, now time.Time) (*pipeline.TimingInfo, *pipeline.TimingsStore) {
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
	prevCA := shipPrevCompletedAt(data, stepName)
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

func shipFindState(root, branch string) (*state.State, error) {
	st, err := state.Find(root, "ship", branch)
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: "find state: " + err.Error(), Cause: err}
	}
	if st == nil {
		return nil, &mcpserver.DataError{Msg: fmt.Sprintf("no ship state found for branch %q", branch)}
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
			return nil, &mcpserver.DataError{Msg: fmt.Sprintf("state file not found: %s", stateFile)}
		}
		if errors.Is(err, fsx.ErrParse) {
			return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("failed to parse state file %s: %s", stateFile, err.Error()), Cause: err}
		}
		return nil, &mcpserver.InfraError{Msg: "read state file: " + err.Error(), Cause: err}
	}
	return &state.State{Path: stateFile, Root: root, Data: data}, nil
}

// ---------------------------------------------------------------------------
// Step mutation cores (mirror startStepCore / completeStepCore in ship.js)
// ---------------------------------------------------------------------------

func shipStartStepCore(data map[string]any, stepName string, now func() time.Time) error {
	step := shipFindStepEntry(data, stepName)
	if step == nil {
		return &mcpserver.DataError{Msg: fmt.Sprintf("step %q not found in state", stepName)}
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
		return &mcpserver.DataError{Msg: fmt.Sprintf("step %q not found in state", stepName)}
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

func shipState(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	// Validate detail level for mutating actions.
	switch in.Action {
	case "begin-step", "complete-step", "start", "complete", "skip", "fail", "decide", "defer":
		if v := detailStr(in.Detail, "detail"); v != "" && v != "full" && v != "concise" {
			return nil, &mcpserver.DomainError{
				Msg: fmt.Sprintf(`detail must be "concise" or "full", got %q; pass detail.detail="concise" or omit for default "full"`, v),
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
	case "skip":
		return shipStateSkip(root, workDir, in, now)
	case "fail":
		return shipStateFail(root, workDir, in, now)
	case "decide":
		return shipStateDecide(root, workDir, in)
	case "defer":
		return shipStateDefer(root, workDir, in)
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

	default:
		return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("unknown ship_state action %q", in.Action)}
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
		return nil, &mcpserver.InfraError{Msg: "scan existing ship state files: " + err.Error(), Cause: err}
	}

	st, err := state.Init(root, "ship", branch, in.SessionID)
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: "init ship state: " + err.Error(), Cause: err}
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
		return nil, &mcpserver.InfraError{Msg: "write ship state: " + err.Error(), Cause: err}
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
		return nil, &mcpserver.DomainError{Msg: "step is required"}
	}
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}
	if err := shipStartStepCore(st.Data, in.Step, now); err != nil {
		return nil, err
	}
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
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
		return nil, &mcpserver.DomainError{Msg: "step is required"}
	}
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}

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
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	var completedAtAfter string
	if stepEntry != nil {
		completedAtAfter, _ = stepEntry["completedAt"].(string)
	}
	timing, ts := shipCompletionTiming(root, st.Data, in.Step, startedAtBefore, completedAtAfter, now())
	rows := shipBuildStepRows(st.Data)
	pos, total := shipStepPosition(st.Data, in.Step)

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
		return nil, &mcpserver.DomainError{Msg: "step is required"}
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
				Msg: fmt.Sprintf("cannot begin step %q — prior step(s) not terminal-OK: %s; complete or skip the blocking step(s) first", in.Step, strings.Join(blocking, ", ")),
			}
		}
	}

	if err := shipStartStepCore(st.Data, in.Step, now); err != nil {
		return nil, err
	}
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
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
		return nil, &mcpserver.DomainError{Msg: "step is required"}
	}

	outcome := "success"
	if raw, ok := in.Detail["outcome"]; ok {
		s, isStr := raw.(string)
		if !isStr || (s != "success" && s != "failure") {
			return nil, &mcpserver.DomainError{Msg: fmt.Sprintf(`outcome must be "success" or "failure", got %v`, raw)}
		}
		outcome = s
	}

	st, err := shipLoadState(root, workDir, in)
	if err != nil {
		return nil, err
	}

	// Capture startedAt before mutation for timing calculations.
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
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	var completedAtAfter string
	if stepEntry != nil {
		completedAtAfter, _ = stepEntry["completedAt"].(string)
	}
	timing, ts := shipCompletionTiming(root, st.Data, in.Step, startedAtBefore, completedAtAfter, now())
	rows := shipBuildStepRows(st.Data)
	pos, total := shipStepPosition(st.Data, in.Step)

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
// Actions: skip / fail / decide / defer / read
// ---------------------------------------------------------------------------

func shipStateSkip(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	if in.Step == "" {
		return nil, &mcpserver.DomainError{Msg: "step is required"}
	}
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}
	step := shipFindStepEntry(st.Data, in.Step)
	if step == nil {
		return nil, &mcpserver.DataError{Msg: fmt.Sprintf("step %q not found in state", in.Step)}
	}
	step["status"] = "skipped"
	step["completedAt"] = now().UTC().Format(time.RFC3339)
	if v, ok := in.Detail["reason"]; ok {
		step["reason"] = v
	}
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	pos, total := shipStepPosition(st.Data, in.Step)
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

func shipStateFail(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	if in.Step == "" {
		return nil, &mcpserver.DomainError{Msg: "step is required"}
	}
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}
	step := shipFindStepEntry(st.Data, in.Step)
	if step == nil {
		return nil, &mcpserver.DataError{Msg: fmt.Sprintf("step %q not found in state", in.Step)}
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

	st.Data["lastFailedStep"] = in.Step
	execAppendIssue(st.Data, StateIssue{
		Step:      in.Step,
		Severity:  "error",
		Category:  "ship-fail",
		Summary:   fmt.Sprintf("Step %s failed", in.Step),
		Detail:    detail,
		Timestamp: now().UTC().Format(time.RFC3339),
	})

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	pos, total := shipStepPosition(st.Data, in.Step)
	out := ShipStepNarrationOut{
		Narration: pipeline.Narration{
			Summary: fmt.Sprintf("Step '%s' failed (%d of %d).", in.Step, pos, total),
		},
	}
	if shipDetailLevel(in) == "full" {
		ts := pipeline.NewTimingsStore(root)
		out.Display = pipeline.StepProgressBlock(shipBuildStepRows(st.Data), ts)
	}
	return out, nil
}

func shipStateDecide(root, workDir string, in ShipStateIn) (any, error) {
	if in.Step == "" {
		return nil, &mcpserver.DomainError{Msg: "step is required"}
	}
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}
	decisions, _ := st.Data["decisions"].([]any)
	decisions = append(decisions, map[string]any{
		"step":     in.Step,
		"decision": detailStr(in.Detail, "text"),
	})
	st.Data["decisions"] = decisions
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
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

func shipStateDefer(root, workDir string, in ShipStateIn) (any, error) {
	severity := detailStr(in.Detail, "severity")
	file := detailStr(in.Detail, "file")
	title := detailStr(in.Detail, "title")
	if severity == "" || file == "" || title == "" {
		return nil, &mcpserver.DomainError{Msg: "severity, file, and title are required for defer"}
	}
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}
	findings, _ := st.Data["deferredFindings"].([]any)
	findings = append(findings, map[string]any{
		"severity": severity,
		"file":     file,
		"line":     in.Detail["line"],
		"title":    title,
	})
	st.Data["deferredFindings"] = findings
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	out := ShipStepNarrationOut{
		Narration: pipeline.Narration{
			Summary: fmt.Sprintf("Deferred finding recorded: %s.", title),
		},
	}
	if shipDetailLevel(in) == "full" {
		ts := pipeline.NewTimingsStore(root)
		out.Display = pipeline.StepProgressBlock(shipBuildStepRows(st.Data), ts)
	}
	return out, nil
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
func shipRunInFlight(data map[string]any) bool {
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

	if !shipRunInFlight(st.Data) {
		return st.Data, nil
	}

	out := make(map[string]any, len(st.Data)+1)
	for k, v := range st.Data {
		out[k] = v
	}
	out["resumeBriefing"] = shipBuildResumeBriefing(root, st.Data, now())
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
		return nil, &mcpserver.InfraError{Msg: "find state: " + findErr.Error(), Cause: findErr}
	}
	if st == nil {
		// Nothing to clean up — cmdCleanup exits 0 silently in this case.
		return map[string]any{}, nil
	}

	valid, violations := shipValidatePipelineContract(st.Data)
	if !valid {
		return nil, &mcpserver.DataError{Msg: fmt.Sprintf(
			"pipeline contract violation: %d step(s) not in terminal state (%s) — state file preserved",
			len(violations), shipFormatViolations(violations))}
	}

	completedAt := now().UTC().Format(time.RFC3339)
	st.Data["pipelineStatus"] = "completed"
	st.Data["pipelineCompletedAt"] = completedAt
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
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

// shipStateCleanupPipeline ports cmdCleanupPipeline: force and no-state-file
// both skip the contract check but still fall through to the GC sweep; only
// an actual contract violation returns early before the sweep runs. Both the
// force and successful-cleanup and no-state-file branches reach the same
// 4-prefix (including commit) GC sweep afterward — confirmed by a full read
// of scripts/state/ship.js's cmdCleanupPipeline, which pre-seeds its report
// object with only {ship,execute,plan} but always executes a further section
// after the branch that adds a 4th "commit" bucket on every non-violation
// path.
func shipStateCleanupPipeline(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	branch, err := execResolveBranch(detailStr(in.Detail, "branch"), workDir)
	if err != nil {
		return nil, err
	}
	st, findErr := state.Find(root, "ship", branch)
	if findErr != nil {
		return nil, &mcpserver.InfraError{Msg: "find state: " + findErr.Error(), Cause: findErr}
	}

	force := detailBool(in.Detail, "force")
	ttlDays := resolveGCTTLDays(root, detailIntPtr(in.Detail, "ttlDays"))

	var currentRun map[string]any
	var issueSummary *IssueSummary
	switch {
	case force:
		currentRun = map[string]any{"cleaned": false, "preservedReason": "force"}
	case st == nil:
		currentRun = map[string]any{"valid": true, "cleaned": false, "reason": "no-state-file"}
	default:
		valid, violations := shipValidatePipelineContract(st.Data)
		if !valid {
			return nil, &mcpserver.DataError{Msg: fmt.Sprintf(
				"pipeline contract violation: %d step(s) not in terminal state (%s) — state file preserved",
				len(violations), shipFormatViolations(violations))}
		}
		completedAt := now().UTC().Format(time.RFC3339)
		st.Data["pipelineStatus"] = "completed"
		st.Data["pipelineCompletedAt"] = completedAt
		if err := state.Write(st); err != nil {
			return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
		}
		currentRun = map[string]any{
			"valid":               true,
			"cleaned":             true,
			"pipelineStatus":      "completed",
			"pipelineCompletedAt": completedAt,
		}
		issueSummary = execIssueSummaryFull(st.Data)
	}

	rpt, err := state.GC(root, state.GCOptions{
		TTL:          time.Duration(ttlDays) * 24 * time.Hour,
		BranchExists: gcBranchExistsFunc(workDir),
		TempDir:      os.Getenv("SDLC_EXPLORE_TMPDIR_OVERRIDE"),
	})
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: "gc sweep: " + err.Error(), Cause: err}
	}

	stateDir := filepath.Join(root, paths.DataDir, "execution")
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

	if detailBool(in.Detail, "dryRun") {
		return shipGCDryRun(filepath.Join(root, paths.DataDir, "execution"), ttlDays, gcBranchExistsFunc(workDir), now)
	}

	rpt, err := state.GC(root, state.GCOptions{
		TTL:          time.Duration(ttlDays) * 24 * time.Hour,
		BranchExists: gcBranchExistsFunc(workDir),
		TempDir:      os.Getenv("SDLC_EXPLORE_TMPDIR_OVERRIDE"),
	})
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: "gc: " + err.Error(), Cause: err}
	}

	return ShipStateGCReport{
		TTLDays: ttlDays,
		Ship:    bucketGCByPrefix(rpt, "ship"),
		Execute: bucketGCByPrefix(rpt, "execute"),
		Plan:    bucketGCByPrefix(rpt, "plan"),
		Commit:  bucketGCByPrefix(rpt, "commit"),
	}, nil
}

// shipGCDryRun enumerates the state directory without deleting anything,
// mirroring cmdGc's --dry-run branch: only ship/execute/plan are classified
// — a commit-prefixed file's bucket lookup misses and is silently skipped,
// matching JS's `if (!bucket) continue`. Each entry is tagged with one of
// "ttl-fresh" / "branch-exists" / "stale+branch-gone".
func shipGCDryRun(stateDir string, ttlDays int, branchExists func(string) bool, now func() time.Time) (any, error) {
	buckets := map[string]map[string]any{
		"ship":    {"wouldDelete": []any{}, "wouldKeep": []any{}},
		"execute": {"wouldDelete": []any{}, "wouldKeep": []any{}},
		"plan":    {"wouldDelete": []any{}, "wouldKeep": []any{}},
	}

	entries, err := os.ReadDir(stateDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, &mcpserver.InfraError{Msg: "gc readdir: " + err.Error(), Cause: err}
	}

	nowMs := now().UnixMilli()
	ttlMs := int64(ttlDays) * 86400000

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
		bucket, ok := buckets[prefix]
		if !ok {
			continue // "commit" (and anything unrecognized) is silently skipped, matching cmdGc's dry-run.
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}

		fresh := (nowMs - info.ModTime().UnixMilli()) < ttlMs
		branchLive := branchExists != nil && branchExists(slug)
		entry := map[string]any{"file": name, "branch": slug}
		switch {
		case fresh:
			entry["reason"] = "ttl-fresh"
			bucket["wouldKeep"] = append(bucket["wouldKeep"].([]any), entry)
		case branchLive:
			entry["reason"] = "branch-exists"
			bucket["wouldKeep"] = append(bucket["wouldKeep"].([]any), entry)
		default:
			entry["reason"] = "stale+branch-gone"
			bucket["wouldDelete"] = append(bucket["wouldDelete"].([]any), entry)
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
// primitive (Task 10), per "reuse, don't reimplement." Two disclosed gaps
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
		return nil, &mcpserver.DomainError{Msg: "from and to are required for migrate"}
	}
	// from/to are branch names, but MigrateBranchSlug matches against
	// slug-shaped filename fragments — slugify both first. Safe even when
	// the caller already passed slugs: SlugifyBranch is idempotent on
	// already-slug input.
	oldSlug := state.SlugifyBranch(from)
	newSlug := state.SlugifyBranch(to)
	if err := state.MigrateBranchSlug(root, oldSlug, newSlug); err != nil {
		return nil, &mcpserver.InfraError{Msg: "migrate: " + err.Error(), Cause: err}
	}
	return map[string]any{"migrated": true}, nil
}

// ---------------------------------------------------------------------------
// Action: next (Go-native; KD14 — drives the executor loop)
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
// Action: todos (Go-native; KD16 — TodoWrite fold)
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

func historyDir(root string) string {
	return filepath.Join(root, paths.DataDir, "history")
}

func shipStateHistoryRecord(root string, in ShipStateIn) (any, error) {
	d := in.Detail
	if d == nil {
		return nil, &mcpserver.DomainError{Msg: "history_record requires detail with run record fields"}
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
		return nil, &mcpserver.DomainError{Msg: "history_record: detail.skill is required"}
	}
	if rec.Outcome == "" {
		return nil, &mcpserver.DomainError{Msg: "history_record: detail.outcome is required"}
	}

	rec.Steps = detailStrSlice(d, "steps")
	rec.GuardrailHits = detailStrSlice(d, "guardrail_hits")
	rec.DeferredIssues = detailStrSlice(d, "deferred_issues")

	w := history.NewFileWriter(historyDir(root))
	if err := w.AppendRun(rec); err != nil {
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("history_record: %s", err.Error()), Cause: err}
	}
	return map[string]any{"ok": true, "ts": rec.Timestamp}, nil
}

// ---------------------------------------------------------------------------
// Action: deferred_add — add a deferred issue to .sdlc-v2/history/deferred.json
// ---------------------------------------------------------------------------

func shipStateDeferredAdd(root string, in ShipStateIn) (any, error) {
	d := in.Detail
	if d == nil {
		return nil, &mcpserver.DomainError{Msg: "deferred_add requires detail with issue fields"}
	}

	issue := history.DeferredIssue{
		ID:          detailStr(d, "id"),
		Created:     detailStr(d, "created"),
		Source:      detailStr(d, "source"),
		Priority:    detailStr(d, "priority"),
		Description: detailStr(d, "description"),
		Status:      "open",
	}
	if issue.ID == "" {
		return nil, &mcpserver.DomainError{Msg: "deferred_add: detail.id is required"}
	}
	if issue.Description == "" {
		return nil, &mcpserver.DomainError{Msg: "deferred_add: detail.description is required"}
	}
	if issue.Created == "" {
		issue.Created = time.Now().UTC().Format(time.RFC3339)
	}
	if issue.Priority == "" {
		issue.Priority = "medium"
	}

	w := history.NewFileWriter(historyDir(root))
	if err := w.AddDeferred(issue); err != nil {
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("deferred_add: %s", err.Error()), Cause: err}
	}
	return map[string]any{"ok": true, "id": issue.ID}, nil
}

// ---------------------------------------------------------------------------
// Action: deferred_resolve — mark a deferred issue as resolved by ID
// ---------------------------------------------------------------------------

func shipStateDeferredResolve(root string, in ShipStateIn) (any, error) {
	d := in.Detail
	if d == nil {
		return nil, &mcpserver.DomainError{Msg: "deferred_resolve requires detail with id field"}
	}
	id := detailStr(d, "id")
	if id == "" {
		return nil, &mcpserver.DomainError{Msg: "deferred_resolve: detail.id is required"}
	}
	w := history.NewFileWriter(historyDir(root))
	if err := w.ResolveDeferred(id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("deferred_resolve: %s", err.Error())}
		}
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("deferred_resolve: %s", err.Error()), Cause: err}
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
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("deferred_list: %s", err.Error()), Cause: err}
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
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("deferred_propose_followups: %s", err.Error()), Cause: err}
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

// RegisterShipStateTools registers the ship_state tool. Registration only —
// wiring into runMCP's dispatch is Task 40's responsibility.
func RegisterShipStateTools(s *mcpserver.Server) {
	mcpserver.Register(s, "ship_state",
		`Manage ship execution state.

Pass "action" to select an operation. Each action uses a subset of the input fields (unlisted fields are ignored).

Mutating actions (begin-step, complete-step, start, complete, skip, fail, decide, defer) return a narrated response: summary (short text), display (markdown progress block), timing (step/pipeline/idle durations when computable), next (the following pipeline step when one exists), and for begin-step/complete-step: todos, issueCount, issueHighlights. Pass detail.detail="concise" to omit the progress block on non-boundary actions (skip, fail, decide, defer); "full" (the default) always includes it. Step durations are recorded under key "ship:<step>" in the timings store; human-wait steps (await-remote-review) are never recorded.

- init: Create ship state. Optional: detail.branch, detail.flags, sessionId.
- begin-step: Begin execution of a step (preferred over start). Requires step. Returns narration with progress, ETA, dispatch instruction, todos, and alreadyDone (true when ship_verify_side_effect already recorded this step's side effect in the sideEffects journal — a resumed pipeline can skip redoing it). Optional: detail.branch, detail.stateFile, detail.detail.
- complete-step: Complete execution of a step (preferred over complete). Requires step. Returns narration with timing, next step, todos, and issue summary. Optional: detail.outcome ("success"|"failure"), detail.result, detail.branch, detail.stateFile, detail.detail.
- start: (Legacy) Begin a step. Requires step. Returns narration. Optional: detail.branch, detail.detail.
- complete: (Legacy) Complete a step. Requires step. Returns narration with timing. Optional: detail.branch, detail.result, detail.detail.
- skip: Skip a step. Requires step. Returns narration. Optional: detail.branch, detail.reason, detail.detail.
- fail: Fail a step. Requires step. Returns narration. Optional: detail.branch, detail.error (recorded as issue), detail.detail.
- decide: Record a decision. Requires step. Returns narration. Optional: detail.branch, detail.text, detail.detail.
- defer: Record a deferred finding. Returns narration. Requires detail.severity, detail.file, detail.title. Optional: detail.branch, detail.line, detail.detail.
- read: Return the full ship state. Optional: detail.branch. When the pipeline is in flight (some step still blocks proceed and at least one step has been started), the state also carries a "resumeBriefing" (resumable, lastStep, lastStepStatus, sideEffects, summary, display, timing{stepSeconds,pipelineSeconds,idleSeconds,human}, next). A step left "failed" is still reported resumable:true, never as an error.
- cleanup: Stamp a branch's ship state terminal (pipelineStatus:"completed", pipelineCompletedAt) instead of deleting it, after validating every step is in a terminal state — the state survives for later reads until GC's TTL prunes it. Optional: detail.branch.
- cleanup-pipeline: Same stamp-instead-of-delete for the current branch's ship state (force/no-state-file skip the contract check), followed by an unconditional GC + per-run-directory sweep. Optional: detail.branch, detail.force, detail.ttlDays.
- gc: Garbage-collect stale state files. Optional: detail.ttlDays, detail.dryRun.
- migrate: Migrate state between branches. Requires detail.from, detail.to.
- next: Return the next pending step. Optional: detail.branch, detail.stateFile.
- todos: List remaining todos for a step. Optional: step, detail.branch, detail.stateFile.
- history_record: Append a pipeline run record to .sdlc-v2/history/runs.jsonl (persistent, survives state-file GC). Requires detail.skill, detail.outcome ("success"|"failure"|"partial"). Optional: detail.ts (ISO timestamp, defaults to now), detail.branch, detail.duration_ms, detail.steps, detail.guardrail_hits, detail.deferred_issues, detail.version.
- deferred_add: Add a deferred issue to .sdlc-v2/history/deferred.json. Requires detail.id, detail.description. Optional: detail.created (defaults to now), detail.source, detail.priority ("high"|"medium"|"low", defaults to "medium").
- deferred_list: List all deferred issues. Returns {issues, openCount}.
- deferred_propose_followups: Return open deferred issues grouped by priority with a formatted display summary. Returns {openCount, groups, display}.
- deferred_resolve: Mark a deferred issue as resolved by ID. Requires detail.id. Returns {ok, id}. Errors if the ID is not found.`,
		func(ctx mcpserver.Ctx, in ShipStateIn) (any, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("resolve project root: %s", err.Error()), Cause: err}
			}
			workDir, err := worktree.ActiveRoot()
			if err != nil {
				workDir = root
			}
			return shipState(root, workDir, in, time.Now)
		},
	)
}
