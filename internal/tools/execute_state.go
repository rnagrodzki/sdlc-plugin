package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/wave"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// Input type
// ---------------------------------------------------------------------------

// ExecuteStateIn carries the merged input for the execute_state tool's 19
// actions. Each field is consumed by one or more actions (noted in comments).
type ExecuteStateIn struct {
	Action         string         `json:"action"`
	Branch         string         `json:"branch,omitempty"`
	Quality        string         `json:"quality,omitempty"`
	TotalTasks     int            `json:"totalTasks,omitempty"`
	PlannedTaskIds []string       `json:"plannedTaskIds,omitempty"`
	PlanPath       string         `json:"planPath,omitempty"`
	PlanHash       string         `json:"planHash,omitempty"`
	Wave           int            `json:"wave,omitempty"`
	TasksJSON      string         `json:"tasksJson,omitempty"`
	RunID          string         `json:"runId,omitempty"`
	WorkerID       string         `json:"workerId,omitempty"`
	Decisions      string         `json:"decisions,omitempty"`
	Status         string         `json:"status,omitempty"`
	TimedOut       bool           `json:"timedOut,omitempty"`
	SHA            string         `json:"sha,omitempty"`
	TaskID         string         `json:"taskId,omitempty"`
	TaskName       string         `json:"taskName,omitempty"`
	Complexity     string         `json:"complexity,omitempty"`
	Risk           string         `json:"risk,omitempty"`
	FilesChanged   string         `json:"filesChanged,omitempty"`
	FilesAdded     string         `json:"filesAdded,omitempty"`
	VerifyToken    string         `json:"verifyToken,omitempty"`
	SkippedDep     bool           `json:"skippedDependency,omitempty"`
	ErrorText      string         `json:"error,omitempty"`
	Data           string         `json:"data,omitempty"`
	TTLDays        *int           `json:"ttlDays,omitempty"`
	DryRun         bool           `json:"dryRun,omitempty"`
	MaxFiles       int            `json:"maxFiles,omitempty"`
	MaxDecisions   int            `json:"maxDecisions,omitempty"`
	MaxInterfaces  int            `json:"maxInterfaces,omitempty"`
	MaxTaskIds     int            `json:"maxTaskIds,omitempty"`
	Dispatched     string         `json:"dispatched,omitempty"`
	MissingIds     string         `json:"missingIds,omitempty"`
	SplitDepth     int            `json:"splitDepth,omitempty"`
	MaxSplitDepth  int            `json:"maxSplitDepth,omitempty"`
	StateFile      string         `json:"stateFile,omitempty"`
	Phase          string         `json:"phase,omitempty"`
	ReadProgress   bool           `json:"readProgress,omitempty"`
	SessionID      string         `json:"sessionId,omitempty"`
	TimeoutSeconds int            `json:"timeoutSeconds,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
	StepID         string         `json:"stepId,omitempty"`
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

// execSafeIDRE validates runId and workerId for path-traversal defense.
var execSafeIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// execNonDigitTRE strips everything except digits and 'T' from a timestamp
// to derive a run ID from startedAt (matches JS's replace(/[^0-9T]/g, ”)).
var execNonDigitTRE = regexp.MustCompile(`[^0-9T]`)

// execContextKeys is the whitelist of allowed context keys for the "context"
// action, matching JS's CONTEXT_KEYS.
var execContextKeys = map[string]bool{
	"planSummary":             true,
	"completedTaskIds":        true,
	"filesAdded":              true,
	"filesModified":           true,
	"interfacesCreated":       true,
	"decisionsFromPriorWaves": true,
}

// execAccountedStatuses are the task statuses that count as "accounted" for
// verify-completeness, matching JS's ACCOUNTED_STATUSES.
var execAccountedStatuses = map[string]bool{
	"completed":          true,
	"failed":             true,
	"skipped-dependency": true,
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterExecuteStateTools registers the execute_state tool.
func RegisterExecuteStateTools(s *mcpserver.Server) {
	mcpserver.Register(s, "execute_state",
		`Manage execution state for the wave-based task runner.

Pass "action" to select an operation. Each action uses a subset of the input fields (unlisted fields are ignored):

- init: Create execution state. Requires branch, quality. Optional: totalTasks, plannedTaskIds, planPath, planHash.
- wave-start: Begin a wave. Requires wave. Optional: branch, tasksJson, runId (for fact sheets).
- wave-done: Complete a wave. Requires wave. Optional: branch, decisions, status.
- wave-fail: Fail a wave. Requires wave. Optional: branch, timedOut, status.
- wave-committed: Record a commit SHA for a completed wave. Requires wave. Optional: branch, sha.
- task-done: Record task completion. Requires wave, taskId. Optional: branch, taskName, complexity, risk, filesChanged, filesAdded, verifyToken.
- task-fail: Record task failure. Requires wave, taskId. Optional: branch, error, skippedDependency.
- context: Read/write shared context keys. Requires data (JSON object with allowed keys: planSummary, completedTaskIds, filesAdded, filesModified, interfacesCreated, decisionsFromPriorWaves). Optional: branch, maxFiles, maxDecisions, maxInterfaces, maxTaskIds.
- read: Return the full execution state blob. Optional: branch.
- cleanup: Delete execution state for a branch. Optional: branch.
- gc: Garbage-collect stale state files. Optional: ttlDays, dryRun, branch.
- summarize-prior-wave-context: Summarize context from prior waves. Optional: branch, maxFiles, maxDecisions, maxInterfaces, maxTaskIds.
- wave-split: Split remaining tasks into a new wave. Requires dispatched. Optional: wave, missingIds, branch, splitDepth, maxSplitDepth, stateFile.
- verify-completeness: Verify all planned tasks are accounted for. Optional: branch, stateFile.
- wave-progress: Read/write per-task progress. Requires runId. For reads: readProgress=true. For writes: taskId, phase.
- resume-reset: Reset in-progress waves for session resume. Optional: branch, stateFile.
- ledger_checkin: Register a worker as active. Requires runId, workerId. Optional: stepId.
- ledger_checkout: Mark a worker as done. Requires runId, workerId.
- ledger_status: List worker statuses for a run. Requires runId. Optional: timeoutSeconds.

Returns a JSON envelope: {"ok":true, "data":{...}} on success, {"ok":false, "code":"...", "error":"..."} on failure.`,
		func(ctx mcpserver.Ctx, in ExecuteStateIn) (any, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				cwd, cwdErr := os.Getwd()
				if cwdErr != nil {
					return nil, &mcpserver.InfraError{Msg: "resolve root: " + err.Error(), Cause: err}
				}
				root = cwd
			}
			workDir, err := worktree.ActiveRoot()
			if err != nil {
				workDir = root
			}
			return executeState(root, workDir, in, time.Now)
		},
	)
}

// ---------------------------------------------------------------------------
// Core dispatcher
// ---------------------------------------------------------------------------

// executeState dispatches to the correct action handler. root anchors
// .sdlc/execution/ lookups; workDir anchors git-branch detection.
// now is injected for testability (ledger stall detection, timestamps).
func executeState(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	switch in.Action {
	case "init":
		return execActionInit(root, workDir, in, now)
	case "wave-start":
		return execActionWaveStart(root, workDir, in, now)
	case "wave-done":
		return execActionWaveDone(root, workDir, in, now)
	case "wave-fail":
		return execActionWaveFail(root, workDir, in, now)
	case "wave-committed":
		return execActionWaveCommitted(root, workDir, in)
	case "task-done":
		return execActionTaskDone(root, workDir, in, now)
	case "task-fail":
		return execActionTaskFail(root, workDir, in, now)
	case "context":
		return execActionContext(root, workDir, in)
	case "read":
		return execActionRead(root, workDir, in)
	case "cleanup":
		return execActionCleanup(root, workDir, in)
	case "gc":
		return execActionGC(root, workDir, in, now)
	case "summarize-prior-wave-context":
		return execActionSummarizePriorWaveContext(root, workDir, in)
	case "wave-split":
		return execActionWaveSplit(root, workDir, in, now)
	case "verify-completeness":
		return execActionVerifyCompleteness(root, workDir, in)
	case "wave-progress":
		return execActionWaveProgress(root, in)
	case "resume-reset":
		return execActionResumeReset(root, workDir, in)
	case "ledger_checkin":
		return execActionLedgerCheckin(root, in, now)
	case "ledger_checkout":
		return execActionLedgerCheckout(root, in, now)
	case "ledger_status":
		return execActionLedgerStatus(root, in, now)
	default:
		return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("unknown action %q", in.Action)}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// execResolveBranch returns the provided branch or falls back to the current
// git branch, matching JS's resolveBranch(argBranch).
func execResolveBranch(branch, workDir string) (string, error) {
	if branch != "" {
		return branch, nil
	}
	b, err := gitx.CurrentBranch(workDir)
	if err != nil {
		return "", &mcpserver.DomainError{Msg: "could not determine branch: " + err.Error(), Cause: err}
	}
	return b, nil
}

// execFindState locates the execute state file for the given branch.
// Returns a DataError when no state file exists.
func execFindState(root, branch string) (*state.State, error) {
	st, err := state.Find(root, "execute", branch)
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: "find state: " + err.Error(), Cause: err}
	}
	if st == nil {
		return nil, &mcpserver.DataError{
			Msg: fmt.Sprintf("no state file found for branch %q", branch),
		}
	}
	return st, nil
}

// execEnsureWaves ensures data["waves"] is a []any and returns it.
func execEnsureWaves(data map[string]any) []any {
	raw, ok := data["waves"].([]any)
	if !ok {
		raw = []any{}
		data["waves"] = raw
	}
	return raw
}

// execFindWave locates a wave by number. Returns nil if not found.
func execFindWave(data map[string]any, waveNumber int) map[string]any {
	waves := execEnsureWaves(data)
	for _, w := range waves {
		wm, ok := w.(map[string]any)
		if !ok {
			continue
		}
		if execToInt(wm["number"]) == waveNumber {
			return wm
		}
	}
	return nil
}

// execFindOrCreateWave locates or creates a wave entry.
func execFindOrCreateWave(data map[string]any, waveNumber int, now func() time.Time) map[string]any {
	waves := execEnsureWaves(data)

	for _, w := range waves {
		wm, ok := w.(map[string]any)
		if !ok {
			continue
		}
		if execToInt(wm["number"]) == waveNumber {
			return wm
		}
	}

	wm := map[string]any{
		"number":    waveNumber,
		"status":    "in_progress",
		"startedAt": now().UTC().Format(time.RFC3339),
		"tasks":     []any{},
	}
	data["waves"] = append(waves, wm)
	return wm
}

// execEnsureContext ensures data["context"] is a map and returns it.
func execEnsureContext(data map[string]any) map[string]any {
	raw, ok := data["context"].(map[string]any)
	if !ok {
		raw = map[string]any{}
		data["context"] = raw
	}
	return raw
}

// execToInt converts a JSON number to int.
func execToInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

// nilIfEmptyStr returns nil for an empty string, or the string itself.
func nilIfEmptyStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// execPushUnique appends val to slice if not already present.
func execPushUnique(slice []any, val string) []any {
	for _, v := range slice {
		if s, ok := v.(string); ok && s == val {
			return slice
		}
	}
	return append(slice, val)
}

// execEnsureStringArray ensures ctx[key] is a []any and returns it.
func execEnsureStringArray(ctx map[string]any, key string) []any {
	arr, ok := ctx[key].([]any)
	if !ok {
		arr = []any{}
		ctx[key] = arr
	}
	return arr
}

// anyToStringSlice extracts a []string from any (handles []string, []any).
func anyToStringSlice(v any) []string {
	switch arr := v.(type) {
	case []string:
		return arr
	case []any:
		out := make([]string, 0, len(arr))
		for _, el := range arr {
			if s, ok := el.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// execNormalizeTaskID strips a leading T/t before a digit, matching
// wave/factsheet.go's unexported normalizeTaskID.
func execNormalizeTaskID(id string) string {
	s := strings.TrimSpace(id)
	if len(s) >= 2 && (s[0] == 'T' || s[0] == 't') && s[1] >= '0' && s[1] <= '9' {
		return s[1:]
	}
	return s
}

// execValidateSafeID validates that an ID matches the safe pattern.
func execValidateSafeID(id, label string) error {
	if !execSafeIDRE.MatchString(id) {
		return &mcpserver.DomainError{
			Msg: fmt.Sprintf("%s contains invalid characters (expected only [A-Za-z0-9_-]): %q", label, id),
		}
	}
	return nil
}

// execDeriveRunID derives a default runId from state data's startedAt field,
// matching JS's startedAt.replace(/[^0-9T]/g, ”).
func execDeriveRunID(data map[string]any, waveNum int) string {
	if startedAt, ok := data["startedAt"].(string); ok && startedAt != "" {
		return execNonDigitTRE.ReplaceAllString(startedAt, "")
	}
	return fmt.Sprintf("wave-%d", waveNum)
}

// execTailStrings returns the last n elements of a string slice.
func execTailStrings(arr []string, n int) []string {
	if n <= 0 || len(arr) == 0 {
		return []string{}
	}
	if len(arr) <= n {
		return arr
	}
	return arr[len(arr)-n:]
}

// execDeepMerge implements the JS deepMerge: arrays concatenate, objects
// merge recursively, scalars overwrite.
func execDeepMerge(target, source any) any {
	srcMap, srcIsMap := source.(map[string]any)
	srcArr, srcIsArr := source.([]any)

	if !srcIsMap && !srcIsArr {
		return source
	}

	if srcIsArr {
		if tgtArr, ok := target.([]any); ok {
			merged := make([]any, 0, len(tgtArr)+len(srcArr))
			merged = append(merged, tgtArr...)
			merged = append(merged, srcArr...)
			return merged
		}
		cp := make([]any, len(srcArr))
		copy(cp, srcArr)
		return cp
	}

	result := map[string]any{}
	if tgtMap, ok := target.(map[string]any); ok {
		for k, v := range tgtMap {
			result[k] = v
		}
	}

	for k, sv := range srcMap {
		tv, exists := result[k]
		if !exists {
			result[k] = sv
			continue
		}

		_, tvIsMap := tv.(map[string]any)
		_, svIsMap := sv.(map[string]any)
		tvArr, tvIsArr := tv.([]any)
		svArr, svIsArr := sv.([]any)

		switch {
		case tvIsMap && svIsMap && tv != nil && sv != nil:
			result[k] = execDeepMerge(tv, sv)
		case tvIsArr && svIsArr:
			merged := make([]any, 0, len(tvArr)+len(svArr))
			merged = append(merged, tvArr...)
			merged = append(merged, svArr...)
			result[k] = merged
		default:
			result[k] = sv
		}
	}

	return result
}

// execSummarizePriorWaveCtx returns a bounded context summary from state data,
// matching JS state.js's summarizePriorWaveContext.
func execSummarizePriorWaveCtx(data map[string]any, root string, maxFiles, maxDecisions, maxInterfaces, maxTaskIds int) map[string]any {
	ctx := map[string]any{}
	if raw, ok := data["context"].(map[string]any); ok {
		ctx = raw
	}

	// Load caps from config if available; fall back to parameter then defaults.
	var configCaps map[string]any
	if execSection, err := config.ReadSection(root, "execute"); err == nil && execSection != nil {
		if caps, ok := execSection["priorWaveContextCaps"].(map[string]any); ok {
			configCaps = caps
		}
	}

	resolveMax := func(input int, cfgKey string, def int) int {
		if input > 0 {
			return input
		}
		if configCaps != nil {
			if v := execToInt(configCaps[cfgKey]); v > 0 {
				return v
			}
		}
		return def
	}

	mf := resolveMax(maxFiles, "maxFiles", 20)
	md := resolveMax(maxDecisions, "maxDecisions", 10)
	mi := resolveMax(maxInterfaces, "maxInterfaces", 15)
	mt := resolveMax(maxTaskIds, "maxTaskIds", 50)

	planSummary := ""
	if ps, ok := ctx["planSummary"].(string); ok {
		planSummary = ps
	}

	return map[string]any{
		"planSummary":             planSummary,
		"completedTaskIds":        execTailStrings(anyToStringSlice(ctx["completedTaskIds"]), mt),
		"filesAdded":              execTailStrings(anyToStringSlice(ctx["filesAdded"]), mf),
		"filesModified":           execTailStrings(anyToStringSlice(ctx["filesModified"]), mf),
		"interfacesCreated":       execTailStrings(anyToStringSlice(ctx["interfacesCreated"]), mi),
		"decisionsFromPriorWaves": execTailStrings(anyToStringSlice(ctx["decisionsFromPriorWaves"]), md),
	}
}

// ledgerDir returns the ledger directory for a run.
func ledgerDir(root, runID string) string {
	return filepath.Join(root, paths.DataDir, "execution", "ledger", runID)
}

// ledgerFilePath returns the per-worker ledger file path.
func ledgerFilePath(root, runID, workerID string) string {
	return filepath.Join(ledgerDir(root, runID), workerID+".json")
}

// ---------------------------------------------------------------------------
// Action: init
// ---------------------------------------------------------------------------

func execActionInit(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.Branch == "" {
		return nil, &mcpserver.DomainError{Msg: "--branch is required for init"}
	}
	if in.Quality == "" {
		return nil, &mcpserver.DomainError{Msg: "--quality is required for init"}
	}

	st, err := state.Init(root, "execute", in.Branch, in.SessionID)
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: "init state: " + err.Error(), Cause: err}
	}

	st.Data["version"] = 1
	st.Data["skill"] = "execute"
	st.Data["startedAt"] = now().UTC().Format(time.RFC3339)
	st.Data["branch"] = in.Branch
	st.Data["worktree"] = workDir
	st.Data["planPath"] = nilIfEmptyStr(in.PlanPath)
	st.Data["planHash"] = nilIfEmptyStr(in.PlanHash)
	st.Data["quality"] = in.Quality
	st.Data["totalTasks"] = in.TotalTasks
	if in.PlannedTaskIds != nil {
		st.Data["plannedTaskIds"] = in.PlannedTaskIds
	} else {
		st.Data["plannedTaskIds"] = nil
	}
	st.Data["waves"] = []any{}
	st.Data["context"] = map[string]any{}

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	return map[string]any{"filePath": st.Path}, nil
}

// ---------------------------------------------------------------------------
// Action: wave-start
// ---------------------------------------------------------------------------

func execActionWaveStart(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.Wave < 1 {
		return nil, &mcpserver.DomainError{Msg: "--wave is required"}
	}

	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}

	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}

	// Find existing wave or create new one.
	w := execFindWave(st.Data, in.Wave)
	if w != nil {
		w["status"] = "in_progress"
		if _, ok := w["startedAt"]; !ok {
			w["startedAt"] = now().UTC().Format(time.RFC3339)
		}
	} else {
		waves := execEnsureWaves(st.Data)
		w = map[string]any{
			"number":    in.Wave,
			"status":    "in_progress",
			"startedAt": now().UTC().Format(time.RFC3339),
			"tasks":     []any{},
		}
		st.Data["waves"] = append(waves, w)
	}

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	// Write per-task fact sheets when tasksJson is provided.
	if in.TasksJSON != "" {
		var tasks []any
		if err := json.Unmarshal([]byte(in.TasksJSON), &tasks); err != nil {
			return nil, &mcpserver.DomainError{Msg: "tasksJson is not valid JSON: " + err.Error(), Cause: err}
		}

		runID := in.RunID
		if runID == "" {
			runID = execDeriveRunID(st.Data, in.Wave)
		}

		summary := execSummarizePriorWaveCtx(st.Data, root, 0, 0, 0, 0)

		writtenPaths := []string{}
		var factSheetErrors []string
		for _, t := range tasks {
			tm, ok := t.(map[string]any)
			if !ok {
				continue
			}
			id, _ := tm["id"].(string)
			if id == "" {
				continue
			}

			fs := wave.Factsheet{
				ID:          id,
				Name:        stringOrEmpty(tm["name"]),
				Description: stringOrEmpty(tm["description"]),
				Contract:    stringOrEmpty(tm["contract"]),
			}
			if ac, ok := tm["acceptanceCriteria"].([]any); ok {
				for _, v := range ac {
					if s, ok := v.(string); ok {
						fs.AcceptanceCriteria = append(fs.AcceptanceCriteria, s)
					}
				}
			}
			if files, ok := tm["files"].([]any); ok {
				for _, v := range files {
					if s, ok := v.(string); ok {
						fs.Files = append(fs.Files, s)
					}
				}
			}

			upstream := &wave.UpstreamSurfaces{
				FilesAdded:    anyToStringSlice(summary["filesAdded"]),
				FilesModified: anyToStringSlice(summary["filesModified"]),
				Interfaces:    anyToStringSlice(summary["interfacesCreated"]),
				Decisions:     anyToStringSlice(summary["decisionsFromPriorWaves"]),
			}
			if len(upstream.FilesAdded)+len(upstream.FilesModified)+len(upstream.Interfaces)+len(upstream.Decisions) > 0 {
				fs.Upstream = upstream
			}

			p, err := wave.WriteFactsheet(root, runID, fs)
			if err != nil {
				// Non-fatal: accumulate errors so the caller sees them.
				factSheetErrors = append(factSheetErrors, fmt.Sprintf("task %s: %s", id, err.Error()))
				continue
			}
			writtenPaths = append(writtenPaths, p)
		}

		result := map[string]any{
			"runId":      runID,
			"factSheets": writtenPaths,
		}
		if len(factSheetErrors) > 0 {
			result["factSheetErrors"] = factSheetErrors
		}
		return result, nil
	}

	return map[string]any{}, nil
}

// stringOrEmpty extracts a string from any, defaulting to empty.
func stringOrEmpty(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ---------------------------------------------------------------------------
// Action: wave-done
// ---------------------------------------------------------------------------

func execActionWaveDone(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.Wave < 1 {
		return nil, &mcpserver.DomainError{Msg: "--wave is required"}
	}

	// Parse decisions BEFORE state lookup (arg error wins over missing state,
	// matching JS check order).
	var decisions []any
	if in.Decisions != "" {
		if err := json.Unmarshal([]byte(in.Decisions), &decisions); err != nil {
			return nil, &mcpserver.DomainError{Msg: "decisions is not valid JSON: " + err.Error(), Cause: err}
		}
	}

	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}

	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}

	w := execFindOrCreateWave(st.Data, in.Wave, now)

	// Validate status.
	status := in.Status
	if status == "" {
		status = "completed"
	}
	if status != "completed" && status != "partial" {
		return nil, &mcpserver.DomainError{Msg: "--status must be one of completed, partial"}
	}
	w["status"] = status
	if in.TimedOut {
		w["timedOut"] = true
	}
	w["completedAt"] = now().UTC().Format(time.RFC3339)

	// Append decisions to context (unique).
	ctx := execEnsureContext(st.Data)
	decArr := execEnsureStringArray(ctx, "decisionsFromPriorWaves")
	for _, d := range decisions {
		if s, ok := d.(string); ok {
			decArr = execPushUnique(decArr, s)
		}
	}
	ctx["decisionsFromPriorWaves"] = decArr

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}
	return map[string]any{}, nil
}

// ---------------------------------------------------------------------------
// Action: wave-fail
// ---------------------------------------------------------------------------

func execActionWaveFail(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.Wave < 1 {
		return nil, &mcpserver.DomainError{Msg: "--wave is required"}
	}

	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}

	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}

	w := execFindOrCreateWave(st.Data, in.Wave, now)
	w["status"] = "failed"
	w["completedAt"] = now().UTC().Format(time.RFC3339)

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}
	return map[string]any{}, nil
}

// ---------------------------------------------------------------------------
// Action: wave-committed
// ---------------------------------------------------------------------------

func execActionWaveCommitted(root, workDir string, in ExecuteStateIn) (any, error) {
	if in.Wave < 1 {
		return nil, &mcpserver.DomainError{Msg: "--wave is required"}
	}

	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}

	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}

	w := execFindWave(st.Data, in.Wave)
	if w == nil {
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("wave %d not found in state", in.Wave),
		}
	}

	waveStatus, _ := w["status"].(string)
	if waveStatus != "completed" {
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("wave %d status is %q, expected \"completed\"", in.Wave, waveStatus),
		}
	}

	// Normalize sha: empty → nil (soft-success "no diff").
	var newSha any
	if in.SHA != "" {
		newSha = in.SHA
	}

	// Idempotency / conflict.
	if existing, hasSha := w["committedSha"]; hasSha {
		if existing == newSha {
			return map[string]any{"committedSha": newSha, "idempotent": true}, nil
		}
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("wave %d already has committedSha %q — refusing to overwrite with %v", in.Wave, existing, newSha),
		}
	}

	w["committedSha"] = newSha
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}
	return map[string]any{"committedSha": newSha, "idempotent": false}, nil
}

// ---------------------------------------------------------------------------
// Action: task-done
// ---------------------------------------------------------------------------

func execActionTaskDone(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.Wave < 1 {
		return nil, &mcpserver.DomainError{Msg: "--wave is required"}
	}
	if in.TaskID == "" {
		return nil, &mcpserver.DomainError{Msg: "taskId is required"}
	}

	// Parse filesChanged.
	var filesChanged []any
	if in.FilesChanged != "" {
		if err := json.Unmarshal([]byte(in.FilesChanged), &filesChanged); err != nil {
			return nil, &mcpserver.DomainError{Msg: "filesChanged is not valid JSON: " + err.Error(), Cause: err}
		}
	}

	// Parse filesAdded.
	var filesAdded []any
	if in.FilesAdded != "" {
		if err := json.Unmarshal([]byte(in.FilesAdded), &filesAdded); err != nil {
			return nil, &mcpserver.DomainError{Msg: "filesAdded is not valid JSON: " + err.Error(), Cause: err}
		}
	}

	// Parse verifyToken.
	var verifyTokens []any
	if in.VerifyToken != "" {
		var raw any
		if err := json.Unmarshal([]byte(in.VerifyToken), &raw); err != nil {
			return nil, &mcpserver.DomainError{Msg: "verifyToken is not valid JSON: " + err.Error(), Cause: err}
		}
		switch v := raw.(type) {
		case string:
			verifyTokens = []any{v}
		case []any:
			verifyTokens = v
		default:
			return nil, &mcpserver.DomainError{Msg: "verifyToken must be a JSON array or string"}
		}
	}

	// Validate filesAdded ⊆ filesChanged BEFORE state lookup (arg error wins).
	changedSet := map[string]bool{}
	for _, f := range filesChanged {
		if s, ok := f.(string); ok {
			changedSet[s] = true
		}
	}
	for _, f := range filesAdded {
		if s, ok := f.(string); ok {
			if !changedSet[s] {
				return nil, &mcpserver.DomainError{
					Msg: fmt.Sprintf("filesAdded entry %q is not present in filesChanged (filesAdded must be a subset of filesChanged)", s),
				}
			}
		}
	}

	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}
	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}

	w := execFindOrCreateWave(st.Data, in.Wave, now)
	tasks, _ := w["tasks"].([]any)
	if tasks == nil {
		tasks = []any{}
	}

	taskEntry := map[string]any{
		"id":           in.TaskID,
		"name":         in.TaskName,
		"complexity":   in.Complexity,
		"risk":         in.Risk,
		"status":       "completed",
		"filesChanged": filesChanged,
		"completedAt":  now().UTC().Format(time.RFC3339),
	}

	// Upsert: replace existing or append.
	found := false
	for i, t := range tasks {
		tm, ok := t.(map[string]any)
		if ok {
			if tid, _ := tm["id"].(string); tid == in.TaskID {
				tasks[i] = taskEntry
				found = true
				break
			}
		}
	}
	if !found {
		tasks = append(tasks, taskEntry)
	}
	w["tasks"] = tasks

	// Update context with filesAdded/filesModified/interfacesCreated/completedTaskIds.
	ctx := execEnsureContext(st.Data)
	for _, key := range []string{"filesAdded", "filesModified", "interfacesCreated", "completedTaskIds"} {
		execEnsureStringArray(ctx, key)
	}

	addedSet := map[string]bool{}
	for _, f := range filesAdded {
		if s, ok := f.(string); ok {
			addedSet[s] = true
		}
	}

	faArr := execEnsureStringArray(ctx, "filesAdded")
	fmArr := execEnsureStringArray(ctx, "filesModified")
	for _, f := range filesChanged {
		if s, ok := f.(string); ok {
			if addedSet[s] {
				faArr = execPushUnique(faArr, s)
			} else {
				fmArr = execPushUnique(fmArr, s)
			}
		}
	}
	ctx["filesAdded"] = faArr
	ctx["filesModified"] = fmArr

	icArr := execEnsureStringArray(ctx, "interfacesCreated")
	for _, t := range verifyTokens {
		if s, ok := t.(string); ok {
			icArr = execPushUnique(icArr, s)
		}
	}
	ctx["interfacesCreated"] = icArr

	ctArr := execEnsureStringArray(ctx, "completedTaskIds")
	ctArr = execPushUnique(ctArr, fmt.Sprintf("%v", in.TaskID))
	ctx["completedTaskIds"] = ctArr

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}
	return map[string]any{}, nil
}

// ---------------------------------------------------------------------------
// Action: task-fail
// ---------------------------------------------------------------------------

func execActionTaskFail(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.Wave < 1 {
		return nil, &mcpserver.DomainError{Msg: "--wave is required"}
	}
	if in.TaskID == "" {
		return nil, &mcpserver.DomainError{Msg: "taskId is required"}
	}

	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}
	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}

	w := execFindOrCreateWave(st.Data, in.Wave, now)
	tasks, _ := w["tasks"].([]any)
	if tasks == nil {
		tasks = []any{}
	}

	taskStatus := "failed"
	if in.SkippedDep {
		taskStatus = "skipped-dependency"
	}

	taskEntry := map[string]any{
		"id":           in.TaskID,
		"name":         in.TaskName,
		"complexity":   in.Complexity,
		"risk":         in.Risk,
		"status":       taskStatus,
		"filesChanged": []any{},
		"error":        in.ErrorText,
		"completedAt":  now().UTC().Format(time.RFC3339),
	}

	found := false
	for i, t := range tasks {
		tm, ok := t.(map[string]any)
		if ok {
			if tid, _ := tm["id"].(string); tid == in.TaskID {
				tasks[i] = taskEntry
				found = true
				break
			}
		}
	}
	if !found {
		tasks = append(tasks, taskEntry)
	}
	w["tasks"] = tasks

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}
	return map[string]any{}, nil
}

// ---------------------------------------------------------------------------
// Action: context
// ---------------------------------------------------------------------------

func execActionContext(root, workDir string, in ExecuteStateIn) (any, error) {
	if in.Data == "" {
		return nil, &mcpserver.DomainError{Msg: "--data is required"}
	}

	var incoming any
	if err := json.Unmarshal([]byte(in.Data), &incoming); err != nil {
		return nil, &mcpserver.DomainError{Msg: "data is not valid JSON: " + err.Error(), Cause: err}
	}

	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}
	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}

	incomingMap, ok := incoming.(map[string]any)
	if !ok {
		return nil, &mcpserver.DomainError{Msg: "--data must be a JSON object"}
	}

	// Validate keys against whitelist.
	var unknown []string
	for k := range incomingMap {
		if !execContextKeys[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("data contains unknown context keys: %s", strings.Join(unknown, ", ")),
		}
	}
	if len(incomingMap) == 0 {
		return nil, &mcpserver.DomainError{Msg: "data is an empty object — nothing to merge"}
	}

	// Validate types.
	for key, value := range incomingMap {
		if key == "planSummary" {
			if _, ok := value.(string); !ok {
				return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("data.%s must be a string", key)}
			}
		} else {
			arr, arrOk := value.([]any)
			if !arrOk {
				return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("data.%s must be an array of strings", key)}
			}
			for _, v := range arr {
				if _, strOk := v.(string); !strOk {
					return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("data.%s must be an array of strings", key)}
				}
			}
		}
	}

	ctx := execEnsureContext(st.Data)
	merged := execDeepMerge(ctx, incomingMap)
	st.Data["context"] = merged

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}
	return map[string]any{}, nil
}

// ---------------------------------------------------------------------------
// Action: read
// ---------------------------------------------------------------------------

// execReadMaxBytes is the maximum serialized size the read action will return.
// Mirrors the execx.ErrOutputCap precedent: overflow is a distinct error, never
// a silent truncation.
const execReadMaxBytes = 1 << 20 // 1 MiB

func execActionRead(root, workDir string, in ExecuteStateIn) (any, error) {
	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}
	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}

	// Enforce output cap — never return a silently truncated blob.
	raw, marshalErr := json.Marshal(st.Data)
	if marshalErr != nil {
		return nil, &mcpserver.InfraError{Msg: "marshal state: " + marshalErr.Error(), Cause: marshalErr}
	}
	if len(raw) > execReadMaxBytes {
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("state blob is %d bytes, exceeds read cap of %d bytes", len(raw), execReadMaxBytes),
		}
	}

	return st.Data, nil
}

// ---------------------------------------------------------------------------
// Action: cleanup
// ---------------------------------------------------------------------------

func execActionCleanup(root, workDir string, in ExecuteStateIn) (any, error) {
	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}

	st, findErr := state.Find(root, "execute", branch)
	if findErr != nil {
		return nil, &mcpserver.InfraError{Msg: "find state: " + findErr.Error(), Cause: findErr}
	}
	if st == nil {
		// Nothing to delete — success.
		return map[string]any{}, nil
	}

	if err := os.Remove(st.Path); err != nil && !os.IsNotExist(err) {
		return nil, &mcpserver.InfraError{Msg: "delete state: " + err.Error(), Cause: err}
	}
	return map[string]any{}, nil
}

// ---------------------------------------------------------------------------
// Action: gc
// ---------------------------------------------------------------------------

func execActionGC(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	// Resolve TTL: *int distinguishes "not provided" (nil → config/default)
	// from "explicitly 0" (immediate cutoff). The fact sheet mandates:
	// "state.GC's TTL:0 now means immediate cutoff — pass it through
	// literally, never re-add a zero-value guard."
	var ttlDays int
	if in.TTLDays != nil {
		ttlDays = *in.TTLDays
	} else {
		ttlDays = execResolveGCTTLDays(root)
	}

	branchExists := gcBranchExistsFunc(workDir)
	stateDir := filepath.Join(root, paths.DataDir, "execution")

	if in.DryRun {
		return execGCDryRun(stateDir, ttlDays, branchExists, now)
	}

	// NOTE: state.GC sweeps ALL prefixes (execute, plan, ship, scaffold, ...)
	// and also prunes sdlc-explore-* tempdirs — broader than the JS execute GC
	// which only touches execute+plan files. The report is filtered below to
	// expose only execute+plan buckets. Dry-run (above) classifies per-file
	// with the same TTL/branch-exists rule, but the real run additionally
	// deletes non-newest files for live branches when TTL-expired — so dry-run
	// under-predicts what a real run deletes. This asymmetry is inherited from
	// the Go state.GC consolidation, not a bug.
	rpt, err := state.GC(root, state.GCOptions{
		TTL:          time.Duration(ttlDays) * 24 * time.Hour,
		BranchExists: branchExists,
	})
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: "gc failed: " + err.Error(), Cause: err}
	}

	dirResult := execReapRunDirectories(stateDir, ttlDays, false, now)

	return map[string]any{
		"ttlDays":     ttlDays,
		"execute":     execBucketGCByPrefix(rpt, "execute"),
		"plan":        execBucketGCByPrefix(rpt, "plan"),
		"directories": dirResult,
	}, nil
}

// execResolveGCTTLDays resolves the TTL for GC from config or default.
func execResolveGCTTLDays(root string) int {
	if stateCfg, _ := config.ReadSection(root, "state"); stateCfg != nil {
		if gcCfg, ok := stateCfg["gc"].(map[string]any); ok {
			if v := execToInt(gcCfg["ttlDays"]); v >= 0 {
				// Only use config value if it was actually set (not zero default).
				if _, exists := gcCfg["ttlDays"]; exists {
					return v
				}
			}
		}
	}
	return 7
}

// execBucketGCByPrefix filters a GCReport to a specific prefix.
func execBucketGCByPrefix(rpt *state.GCReport, prefix string) map[string]any {
	return map[string]any{
		"deleted": filterPathsByPrefix(rpt.Deleted, prefix),
		"kept":    filterPathsByPrefix(rpt.Kept, prefix),
	}
}

// execGCDryRun classifies state files without deleting them.
func execGCDryRun(stateDir string, ttlDays int, branchExists func(string) bool, now func() time.Time) (any, error) {
	out := map[string]any{
		"dryRun":  true,
		"ttlDays": ttlDays,
	}

	executeResult := map[string]any{"deleted": []any{}, "kept": []any{}}
	planResult := map[string]any{"deleted": []any{}, "kept": []any{}}

	entries, err := os.ReadDir(stateDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, &mcpserver.InfraError{Msg: "gc readdir: " + err.Error(), Cause: err}
	}

	nowTime := now()
	ttlMs := int64(ttlDays) * 86400000
	nowMs := nowTime.UnixMilli()

	// Match the state filename regex pattern.
	stateFileRE := regexp.MustCompile(`^(execute|plan)-(.+)-\d{8}T\d{6}Z\.json$`)

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		m := stateFileRE.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		prefix := m[1]
		slug := m[2]

		var bucket map[string]any
		if prefix == "execute" {
			bucket = executeResult
		} else {
			bucket = planResult
		}

		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}

		fresh := (nowMs - info.ModTime().UnixMilli()) < ttlMs
		branchLive := branchExists != nil && branchExists(slug)

		entry := map[string]any{"file": name, "branch": slug}
		if fresh {
			entry["reason"] = "ttl-fresh"
			bucket["kept"] = append(bucket["kept"].([]any), entry)
		} else if branchLive {
			entry["reason"] = "branch-exists"
			bucket["kept"] = append(bucket["kept"].([]any), entry)
		} else {
			entry["reason"] = "stale+branch-gone"
			bucket["deleted"] = append(bucket["deleted"].([]any), entry)
		}
	}

	out["execute"] = executeResult
	out["plan"] = planResult
	out["directories"] = execReapRunDirectories(stateDir, ttlDays, true, now)

	return out, nil
}

// execReapRunDirectories sweeps stale per-run directories.
// Both live and dry-run modes use "deleted"/"kept" keys; the top-level
// "dryRun" field distinguishes the two modes.
func execReapRunDirectories(stateDir string, ttlDays int, dryRun bool, now func() time.Time) map[string]any {
	deleteBucketKey := "deleted"
	keepBucketKey := "kept"

	result := map[string]any{deleteBucketKey: []any{}, keepBucketKey: []any{}}

	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return result
	}

	// Collect live run IDs from execute-*.json files.
	liveRunIDs := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if !strings.HasPrefix(e.Name(), "execute-") {
			continue
		}
		fp := filepath.Join(stateDir, e.Name())
		var data map[string]any
		if err := fsx.ReadJSON(fp, &data); err == nil {
			if startedAt, ok := data["startedAt"].(string); ok {
				liveRunIDs[execNonDigitTRE.ReplaceAllString(startedAt, "")] = true
			}
		}
	}

	nowTime := now()
	ttlDur := time.Duration(ttlDays) * 24 * time.Hour

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirName := e.Name()
		dirPath := filepath.Join(stateDir, dirName)
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}

		age := nowTime.Sub(info.ModTime())

		if age < ttlDur {
			result[keepBucketKey] = append(result[keepBucketKey].([]any),
				map[string]any{"dir": dirName, "reason": "ttl-fresh"})
			continue
		}

		if liveRunIDs[dirName] {
			result[keepBucketKey] = append(result[keepBucketKey].([]any),
				map[string]any{"dir": dirName, "reason": "state-file-exists"})
			continue
		}

		if dryRun {
			result[deleteBucketKey] = append(result[deleteBucketKey].([]any),
				map[string]any{"dir": dirName, "reason": "stale+state-file-gone"})
		} else {
			if err := os.RemoveAll(dirPath); err != nil {
				result[keepBucketKey] = append(result[keepBucketKey].([]any),
					map[string]any{"dir": dirName, "reason": "rm-failed"})
			} else {
				result[deleteBucketKey] = append(result[deleteBucketKey].([]any),
					map[string]any{"dir": dirName, "reason": "stale+state-file-gone"})
			}
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// Action: summarize-prior-wave-context
// ---------------------------------------------------------------------------

func execActionSummarizePriorWaveContext(root, workDir string, in ExecuteStateIn) (any, error) {
	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}
	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}

	return execSummarizePriorWaveCtx(st.Data, root, in.MaxFiles, in.MaxDecisions, in.MaxInterfaces, in.MaxTaskIds), nil
}

// ---------------------------------------------------------------------------
// Action: wave-split
// ---------------------------------------------------------------------------

func execActionWaveSplit(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.Dispatched == "" {
		return nil, &mcpserver.DomainError{Msg: "--dispatched is required (JSON array of task ID strings)"}
	}

	var dispatched []any
	if err := json.Unmarshal([]byte(in.Dispatched), &dispatched); err != nil {
		return nil, &mcpserver.DomainError{Msg: "dispatched is not valid JSON: " + err.Error(), Cause: err}
	}

	var missingIds []any
	if in.MissingIds != "" {
		if err := json.Unmarshal([]byte(in.MissingIds), &missingIds); err != nil {
			return nil, &mcpserver.DomainError{Msg: "missingIds is not valid JSON: " + err.Error(), Cause: err}
		}
	}

	splitDepth := in.SplitDepth
	maxSplitDepth := in.MaxSplitDepth
	if maxSplitDepth <= 0 {
		maxSplitDepth = wave.MaxSplitDepth
	}

	// Convert dispatched to wave.Task slice.
	tasks := make([]wave.Task, 0, len(dispatched))
	for _, d := range dispatched {
		if s, ok := d.(string); ok {
			tasks = append(tasks, wave.Task(s))
		}
	}

	missingTasks := make([]wave.Task, 0, len(missingIds))
	for _, m := range missingIds {
		if s, ok := m.(string); ok {
			missingTasks = append(missingTasks, wave.Task(s))
		}
	}

	// Enforce custom maxSplitDepth before calling Split (which uses its own
	// constant MaxSplitDepth=3). If the user's max is lower, gate here.
	if splitDepth >= maxSplitDepth {
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("splitDepth %d exceeds maxSplitDepth %d — manual escalation required", splitDepth, maxSplitDepth),
		}
	}

	halves, err := wave.Split(tasks, wave.SplitOptions{
		SplitDepth: splitDepth,
		MissingIDs: missingTasks,
	})
	if err != nil {
		var maxErr *wave.MaxSplitDepthExceededError
		if errors.As(err, &maxErr) {
			return nil, &mcpserver.DomainError{Msg: err.Error(), Cause: err}
		}
		return nil, &mcpserver.InfraError{Msg: "wave split: " + err.Error(), Cause: err}
	}

	// Build result matching JS shape.
	halvesOut := make([]map[string]any, len(halves))
	for i, h := range halves {
		ids := make([]string, len(h.Tasks))
		for j, t := range h.Tasks {
			ids[j] = string(t)
		}
		halvesOut[i] = map[string]any{
			"tasks": ids,
			"depth": h.Depth,
		}
	}

	result := map[string]any{
		"halves": halvesOut,
	}

	// Persist split tree to state (best effort).
	func() {
		defer func() { recover() }() //nolint:errcheck // best-effort

		var st *state.State
		if in.StateFile != "" {
			var data map[string]any
			if err := fsx.ReadJSON(in.StateFile, &data); err == nil {
				st = &state.State{
					Path: in.StateFile,
					Root: root,
					Data: data,
				}
			}
		} else {
			branch, brErr := execResolveBranch(in.Branch, workDir)
			if brErr == nil {
				st, _ = state.Find(root, "execute", branch)
			}
		}

		if st != nil {
			w := execFindOrCreateWave(st.Data, in.Wave, now)

			// Idempotency: skip write if same splitDepth already recorded.
			if existingTree, ok := w["splitTree"].(map[string]any); ok {
				if execToInt(existingTree["splitDepth"]) == splitDepth {
					return
				}
			}

			dispatchedStrs := make([]string, len(tasks))
			for i, t := range tasks {
				dispatchedStrs[i] = string(t)
			}
			missingStrs := make([]string, len(missingTasks))
			for i, t := range missingTasks {
				missingStrs[i] = string(t)
			}

			w["splitTree"] = map[string]any{
				"splitDepth":    splitDepth,
				"maxSplitDepth": maxSplitDepth,
				"dispatched":    dispatchedStrs,
				"missingIds":    missingStrs,
				"halves":        halvesOut,
				"computedAt":    now().UTC().Format(time.RFC3339),
			}
			if err := state.Write(st); err != nil {
				result["writeWarning"] = "state persistence failed: " + err.Error()
			}
		}
	}()

	return result, nil
}

// ---------------------------------------------------------------------------
// Action: verify-completeness
// ---------------------------------------------------------------------------

func execActionVerifyCompleteness(root, workDir string, in ExecuteStateIn) (any, error) {
	var data map[string]any

	if in.StateFile != "" {
		if err := fsx.ReadJSON(in.StateFile, &data); err != nil {
			return nil, &mcpserver.DomainError{
				Msg:   fmt.Sprintf("cannot read state file %q: %s", in.StateFile, err.Error()),
				Cause: err,
			}
		}
	} else {
		branch, err := execResolveBranch(in.Branch, workDir)
		if err != nil {
			return nil, err
		}
		st, err := execFindState(root, branch)
		if err != nil {
			return nil, err
		}
		data = st.Data
	}

	// Collect all task entries across waves.
	waves, _ := data["waves"].([]any)
	accountedByID := map[string]string{} // id → status

	for _, w := range waves {
		wm, ok := w.(map[string]any)
		if !ok {
			continue
		}
		tasks, _ := wm["tasks"].([]any)
		for _, t := range tasks {
			tm, ok := t.(map[string]any)
			if !ok {
				continue
			}
			id, _ := tm["id"].(string)
			if id == "" {
				continue
			}
			status, _ := tm["status"].(string)
			// Last write wins.
			if _, exists := accountedByID[id]; !exists || execAccountedStatuses[status] {
				accountedByID[id] = status
			}
		}
	}

	// Determine planned task IDs.
	plannedIDs := anyToStringSlice(data["plannedTaskIds"])
	if plannedIDs == nil {
		if ctx, ok := data["context"].(map[string]any); ok {
			plannedIDs = anyToStringSlice(ctx["plannedTaskIds"])
		}
	}

	if plannedIDs == nil {
		return nil, &mcpserver.DomainError{
			Msg: "verify-completeness cannot find plannedTaskIds in state — invariant check cannot run",
		}
	}

	// Build normalized lookup.
	accountedByNormID := map[string]string{}
	for id, status := range accountedByID {
		accountedByNormID[execNormalizeTaskID(id)] = status
	}

	var accountedIDs, missingIDs []string
	for _, id := range plannedIDs {
		normID := execNormalizeTaskID(id)
		status, found := accountedByNormID[normID]
		if found && execAccountedStatuses[status] {
			accountedIDs = append(accountedIDs, id)
		} else {
			missingIDs = append(missingIDs, id)
		}
	}

	totalPlanned := len(plannedIDs)
	totalAccounted := len(accountedIDs)

	if len(missingIDs) == 0 {
		return map[string]any{
			"ok":             true,
			"totalPlanned":   totalPlanned,
			"totalAccounted": totalAccounted,
		}, nil
	}

	// Incomplete — DataError (JS exit 65).
	return nil, &mcpserver.DataError{
		Msg: fmt.Sprintf("incomplete: %d of %d planned tasks unaccounted (missingIds: %s)",
			len(missingIDs), totalPlanned, strings.Join(missingIDs, ", ")),
	}
}

// ---------------------------------------------------------------------------
// Action: wave-progress
// ---------------------------------------------------------------------------

func execActionWaveProgress(root string, in ExecuteStateIn) (any, error) {
	if in.RunID == "" {
		return nil, &mcpserver.DomainError{Msg: "runId is required"}
	}

	if in.ReadProgress {
		p, err := wave.ReadProgress(root, in.RunID)
		if err != nil {
			return nil, &mcpserver.DomainError{Msg: "read progress: " + err.Error(), Cause: err}
		}
		return p, nil
	}

	if in.TaskID == "" {
		return nil, &mcpserver.DomainError{Msg: "taskId is required (write mode)"}
	}

	if err := wave.UpdateProgress(root, in.RunID, in.TaskID, in.Phase); err != nil {
		if errors.Is(err, wave.ErrBadRunID) || errors.Is(err, wave.ErrBadPhase) {
			return nil, &mcpserver.DomainError{Msg: err.Error(), Cause: err}
		}
		return nil, &mcpserver.InfraError{Msg: "update progress: " + err.Error(), Cause: err}
	}
	return map[string]any{}, nil
}

// ---------------------------------------------------------------------------
// Action: resume-reset
// ---------------------------------------------------------------------------

func execActionResumeReset(root, workDir string, in ExecuteStateIn) (any, error) {
	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}

	st, findErr := state.Find(root, "execute", branch)
	if findErr != nil {
		return nil, &mcpserver.InfraError{Msg: "find state: " + findErr.Error(), Cause: findErr}
	}

	resetWaves := []int{}
	clearedTaskIds := []string{}

	if st != nil {
		waves, _ := st.Data["waves"].([]any)
		changed := false

		for _, w := range waves {
			wm, ok := w.(map[string]any)
			if !ok {
				continue
			}
			status, _ := wm["status"].(string)
			if status != "in_progress" {
				continue
			}

			tasks, _ := wm["tasks"].([]any)
			_, hasCompletedAt := wm["completedAt"]
			if len(tasks) == 0 && !hasCompletedAt {
				continue // nothing to reset
			}

			for _, t := range tasks {
				tm, ok := t.(map[string]any)
				if ok {
					if id, ok := tm["id"].(string); ok {
						clearedTaskIds = append(clearedTaskIds, id)
					}
				}
			}
			wm["tasks"] = []any{}
			delete(wm, "completedAt")
			resetWaves = append(resetWaves, execToInt(wm["number"]))
			changed = true
		}

		if changed {
			if err := state.Write(st); err != nil {
				return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
			}
		}
	}

	return map[string]any{
		"resetWaves":     resetWaves,
		"clearedTaskIds": clearedTaskIds,
	}, nil
}

// ---------------------------------------------------------------------------
// Action: ledger_checkin
// ---------------------------------------------------------------------------

func execActionLedgerCheckin(root string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.RunID == "" {
		return nil, &mcpserver.DomainError{Msg: "runId is required"}
	}
	if in.WorkerID == "" {
		return nil, &mcpserver.DomainError{Msg: "workerId is required"}
	}
	if err := execValidateSafeID(in.RunID, "runId"); err != nil {
		return nil, err
	}
	if err := execValidateSafeID(in.WorkerID, "workerId"); err != nil {
		return nil, err
	}

	dir := ledgerDir(root, in.RunID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &mcpserver.InfraError{Msg: "mkdir ledger: " + err.Error(), Cause: err}
	}

	fp := ledgerFilePath(root, in.RunID, in.WorkerID)
	data := map[string]any{
		"status":    "active",
		"checkinAt": now().UTC().Format(time.RFC3339),
	}
	if in.StepID != "" {
		data["stepId"] = in.StepID
	}

	if err := fsx.AtomicWriteJSON(fp, data); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write ledger: " + err.Error(), Cause: err}
	}
	confirmation := map[string]any{
		"runId":     in.RunID,
		"workerId":  in.WorkerID,
		"status":    "active",
		"checkinAt": data["checkinAt"],
	}
	if in.StepID != "" {
		confirmation["stepId"] = in.StepID
	}
	return confirmation, nil
}

// ---------------------------------------------------------------------------
// Action: ledger_checkout
// ---------------------------------------------------------------------------

func execActionLedgerCheckout(root string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.RunID == "" {
		return nil, &mcpserver.DomainError{Msg: "runId is required"}
	}
	if in.WorkerID == "" {
		return nil, &mcpserver.DomainError{Msg: "workerId is required"}
	}
	if err := execValidateSafeID(in.RunID, "runId"); err != nil {
		return nil, err
	}
	if err := execValidateSafeID(in.WorkerID, "workerId"); err != nil {
		return nil, err
	}

	dir := ledgerDir(root, in.RunID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &mcpserver.InfraError{Msg: "mkdir ledger: " + err.Error(), Cause: err}
	}

	fp := ledgerFilePath(root, in.RunID, in.WorkerID)

	// Read existing file to preserve checkinAt/stepId.
	existing := map[string]any{}
	_ = fsx.ReadJSON(fp, &existing)

	existing["status"] = "done"
	existing["checkoutAt"] = now().UTC().Format(time.RFC3339)

	if err := fsx.AtomicWriteJSON(fp, existing); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write ledger: " + err.Error(), Cause: err}
	}
	return map[string]any{
		"runId":      in.RunID,
		"workerId":   in.WorkerID,
		"status":     "done",
		"checkoutAt": existing["checkoutAt"],
	}, nil
}

// ---------------------------------------------------------------------------
// Action: ledger_status
// ---------------------------------------------------------------------------

func execActionLedgerStatus(root string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.RunID == "" {
		return nil, &mcpserver.DomainError{Msg: "runId is required"}
	}
	if err := execValidateSafeID(in.RunID, "runId"); err != nil {
		return nil, err
	}

	dir := ledgerDir(root, in.RunID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{
				"runId":          in.RunID,
				"workers":        []any{},
				"stalledWorkers": []string{},
			}, nil
		}
		return nil, &mcpserver.InfraError{Msg: "read ledger dir: " + err.Error(), Cause: err}
	}

	nowTime := now()
	var workers []any
	var stalledWorkers []string

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}

		workerID := strings.TrimSuffix(name, ".json")
		fp := filepath.Join(dir, name)
		var data map[string]any
		if err := fsx.ReadJSON(fp, &data); err != nil {
			continue
		}

		status, _ := data["status"].(string)
		checkinAt, _ := data["checkinAt"].(string)
		checkoutAt, _ := data["checkoutAt"].(string)
		stepID, _ := data["stepId"].(string)

		stalled := false
		if status == "active" && in.TimeoutSeconds > 0 && checkinAt != "" {
			if t, err := time.Parse(time.RFC3339, checkinAt); err == nil {
				if nowTime.Sub(t) > time.Duration(in.TimeoutSeconds)*time.Second {
					stalled = true
					stalledWorkers = append(stalledWorkers, workerID)
				}
			}
		}

		entry := map[string]any{
			"workerId":   workerID,
			"status":     status,
			"checkinAt":  checkinAt,
			"checkoutAt": checkoutAt,
			"stalled":    stalled,
		}
		if stepID != "" {
			entry["stepId"] = stepID
		}
		workers = append(workers, entry)
	}

	if workers == nil {
		workers = []any{}
	}
	if stalledWorkers == nil {
		stalledWorkers = []string{}
	}

	return map[string]any{
		"runId":          in.RunID,
		"workers":        workers,
		"stalledWorkers": stalledWorkers,
	}, nil
}
