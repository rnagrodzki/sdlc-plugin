package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
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
	Action    string         `json:"action"`
	Step      string         `json:"step,omitempty"`
	Detail    map[string]any `json:"detail,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
}

// ShipTodosOut is the shared output shape for begin-step, complete-step, and
// the Go-native todos action — all three render the same TodoWrite-shaped
// list via shipmeta.TodosForStep. JS's begin-step/complete-step also return a
// "marker" summary string (stepTransition/markCompleted/renderTodos); that is
// dropped here, matching the divergence already disclosed in
// shipmeta.TodosForStep's own doc comment (contract is []Todo only).
type ShipTodosOut struct {
	Todos []shipmeta.Todo `json:"todos"`
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
			return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("failed to parse state file %s: %s", stateFile, err.Error())}
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
		return shipStateFail(root, workDir, in)
	case "decide":
		return shipStateDecide(root, workDir, in)
	case "defer":
		return shipStateDefer(root, workDir, in)
	case "read":
		return shipStateRead(root, workDir, in)
	case "cleanup":
		return shipStateCleanup(root, workDir, in)
	case "cleanup-pipeline":
		return shipStateCleanupPipeline(root, workDir, in)
	case "gc":
		return shipStateGC(root, workDir, in, now)
	case "migrate":
		return shipStateMigrate(root, in)
	case "next":
		return shipStateNext(root, workDir, in)
	case "todos":
		return shipStateTodos(root, workDir, in)
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
	return map[string]any{}, nil
}

func shipStateComplete(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	if in.Step == "" {
		return nil, &mcpserver.DomainError{Msg: "step is required"}
	}
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}
	resultVal, hasResult := in.Detail["result"]
	if err := shipCompleteStepCore(st.Data, in.Step, hasResult, resultVal, "success", now); err != nil {
		return nil, err
	}
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}
	return map[string]any{}, nil
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
				Msg: fmt.Sprintf("cannot begin step %q — prior step(s) not terminal-OK: %s", in.Step, strings.Join(blocking, ", ")),
			}
		}
	}

	if err := shipStartStepCore(st.Data, in.Step, now); err != nil {
		return nil, err
	}
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	return ShipTodosOut{Todos: shipmeta.TodosForStep(in.Step, st)}, nil
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

	resultVal, hasResult := in.Detail["result"]
	if err := shipCompleteStepCore(st.Data, in.Step, hasResult, resultVal, outcome, now); err != nil {
		return nil, err
	}
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	return ShipTodosOut{Todos: shipmeta.TodosForStep(in.Step, st)}, nil
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
	return map[string]any{}, nil
}

func shipStateFail(root, workDir string, in ShipStateIn) (any, error) {
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
	if v, ok := in.Detail["error"]; ok {
		step["error"] = v
	}
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}
	return map[string]any{}, nil
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
	return map[string]any{}, nil
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
	return map[string]any{}, nil
}

func shipStateRead(root, workDir string, in ShipStateIn) (any, error) {
	st, err := shipResolveAndFind(detailStr(in.Detail, "branch"), workDir, root)
	if err != nil {
		return nil, err
	}
	return st.Data, nil
}

// ---------------------------------------------------------------------------
// Action: cleanup (single branch)
// ---------------------------------------------------------------------------

func shipStateCleanup(root, workDir string, in ShipStateIn) (any, error) {
	branch, err := execResolveBranch(detailStr(in.Detail, "branch"), workDir)
	if err != nil {
		return nil, err
	}
	st, findErr := state.Find(root, "ship", branch)
	if findErr != nil {
		return nil, &mcpserver.InfraError{Msg: "find state: " + findErr.Error(), Cause: findErr}
	}
	if st == nil {
		// Nothing to delete — cmdCleanup exits 0 silently in this case.
		return map[string]any{}, nil
	}

	valid, violations := shipValidatePipelineContract(st.Data)
	if !valid {
		return nil, &mcpserver.DataError{Msg: fmt.Sprintf(
			"pipeline contract violation: %d step(s) not in terminal state (%s) — state file preserved",
			len(violations), shipFormatViolations(violations))}
	}

	if err := os.Remove(st.Path); err != nil && !os.IsNotExist(err) {
		return nil, &mcpserver.InfraError{Msg: "delete state file: " + err.Error(), Cause: err}
	}
	return map[string]any{"valid": true, "cleaned": true}, nil
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
func shipStateCleanupPipeline(root, workDir string, in ShipStateIn) (any, error) {
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
		if err := os.Remove(st.Path); err != nil && !os.IsNotExist(err) {
			return nil, &mcpserver.InfraError{Msg: "delete state file: " + err.Error(), Cause: err}
		}
		currentRun = map[string]any{"valid": true, "cleaned": true}
	}

	rpt, err := state.GC(root, state.GCOptions{
		TTL:          time.Duration(ttlDays) * 24 * time.Hour,
		BranchExists: gcBranchExistsFunc(workDir),
		TempDir:      os.Getenv("SDLC_EXPLORE_TMPDIR_OVERRIDE"),
	})
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: "gc sweep: " + err.Error(), Cause: err}
	}

	return map[string]any{
		"currentRun": currentRun,
		"gc": map[string]any{
			"ship":    bucketGCByPrefix(rpt, "ship"),
			"execute": bucketGCByPrefix(rpt, "execute"),
			"plan":    bucketGCByPrefix(rpt, "plan"),
			"commit":  bucketGCByPrefix(rpt, "commit"),
		},
		"force":   force,
		"ttlDays": ttlDays,
	}, nil
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
// Registration
// ---------------------------------------------------------------------------

// RegisterShipStateTools registers the ship_state tool. Registration only —
// wiring into runMCP's dispatch is Task 40's responsibility.
func RegisterShipStateTools(s *mcpserver.Server) {
	mcpserver.Register(s, "ship_state",
		`Manage ship execution state.

Pass "action" to select an operation. Each action uses a subset of the input fields (unlisted fields are ignored):

- init: Create ship state. Optional: detail.branch, detail.flags, sessionId.
- start: Begin a ship step. Requires step. Optional: detail.branch.
- complete: Complete a ship step. Requires step. Optional: detail.branch, detail.result.
- begin-step: Begin execution of a step. Requires step. Optional: detail.branch, detail.stateFile.
- complete-step: Complete execution of a step. Requires step. Optional: detail.outcome, detail.result, detail.branch, detail.stateFile.
- skip: Skip a step. Requires step. Optional: detail.branch, detail.reason.
- fail: Fail a step. Requires step. Optional: detail.branch, detail.error.
- decide: Record a decision for a step. Requires step. Optional: detail.branch, detail.text.
- defer: Record a deferred finding. Requires detail.severity, detail.file, detail.title. Optional: detail.branch, detail.line.
- read: Return the full ship state. Optional: detail.branch.
- cleanup: Delete ship state for a branch. Optional: detail.branch.
- cleanup-pipeline: Clean up pipeline state. Optional: detail.branch, detail.force, detail.ttlDays.
- gc: Garbage-collect stale state files. Optional: detail.ttlDays, detail.dryRun.
- migrate: Migrate state between branches. Requires detail.from, detail.to.
- next: Return the next pending step. Optional: detail.branch, detail.stateFile.
- todos: List remaining todos for a step. Optional: step, detail.branch, detail.stateFile.`,
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
