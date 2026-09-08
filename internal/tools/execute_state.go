package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/pipeline"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/wave"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// Input type
// ---------------------------------------------------------------------------

// ExecuteStateIn carries the merged input for the execute_state tool's
// actions. Each field is consumed by one or more actions (noted in comments).
type ExecuteStateIn struct {
	Action            string         `json:"action"`
	Branch            string         `json:"branch,omitempty"`
	Quality           string         `json:"quality,omitempty"`
	TotalTasks        int            `json:"totalTasks,omitempty"`
	PlannedTaskIds    []string       `json:"plannedTaskIds,omitempty"`
	PlanPath          string         `json:"planPath,omitempty"`
	PlanHash          string         `json:"planHash,omitempty"`
	ExtraDepsJSON     string         `json:"extraDepsJson,omitempty"`
	Wave              *int           `json:"wave,omitempty"`
	TasksJSON         string         `json:"tasksJson,omitempty"`
	RunID             string         `json:"runId,omitempty"`
	WorkerID          string         `json:"workerId,omitempty"`
	Decisions         string         `json:"decisions,omitempty"`
	Status            string         `json:"status,omitempty"`
	TimedOut          bool           `json:"timedOut,omitempty"`
	SHA               string         `json:"sha,omitempty"`
	TaskID            string         `json:"taskId,omitempty"`
	TaskName          string         `json:"taskName,omitempty"`
	Complexity        string         `json:"complexity,omitempty"`
	Risk              string         `json:"risk,omitempty"`
	FilesChanged      string         `json:"filesChanged,omitempty"`
	FilesAdded        string         `json:"filesAdded,omitempty"`
	VerifyToken       string         `json:"verifyToken,omitempty"`
	SkippedDep        bool           `json:"skippedDependency,omitempty"`
	ErrorText         string         `json:"error,omitempty"`
	Data              string         `json:"data,omitempty"`
	TTLDays           *int           `json:"ttlDays,omitempty"`
	DryRun            bool           `json:"dryRun,omitempty"`
	MaxFiles          int            `json:"maxFiles,omitempty"`
	MaxDecisions      int            `json:"maxDecisions,omitempty"`
	MaxInterfaces     int            `json:"maxInterfaces,omitempty"`
	MaxTaskIds        int            `json:"maxTaskIds,omitempty"`
	Dispatched        string         `json:"dispatched,omitempty"`
	MissingIds        string         `json:"missingIds,omitempty"`
	SplitDepth        int            `json:"splitDepth,omitempty"`
	MaxSplitDepth     int            `json:"maxSplitDepth,omitempty"`
	StateFile         string         `json:"stateFile,omitempty"`
	Phase             string         `json:"phase,omitempty"`
	ReadProgress      bool           `json:"readProgress,omitempty"`
	SessionID         string         `json:"sessionId,omitempty"`
	TimeoutSeconds    int            `json:"timeoutSeconds,omitempty"`
	Payload           map[string]any `json:"payload,omitempty"`
	StepID            string         `json:"stepId,omitempty"`
	Detail            string         `json:"detail,omitempty"`
	LastCompletedTask string         `json:"lastCompletedTask,omitempty"`
	Message           string         `json:"message,omitempty"`
}

// ---------------------------------------------------------------------------
// Narration output types
// ---------------------------------------------------------------------------

// ExecWaveNarrationOut is the narrated output for wave-level execute_state
// actions (wave-start, wave-done, wave-fail). It embeds pipeline.Narration
// at the top level (JSON: summary, display, timing, next) alongside the
// action-specific fields carried by wave-start and wave-done.
type ExecWaveNarrationOut struct {
	pipeline.Narration
	RunID           string   `json:"runId,omitempty"`
	FactSheets      []string `json:"factSheets,omitempty"`
	FactSheetErrors []string `json:"factSheetErrors,omitempty"`
	IssueCount      int      `json:"issueCount,omitempty"`
	IssueHighlights []string `json:"issueHighlights,omitempty"`
}

// ExecTaskNarrationOut is the narrated output for task-level execute_state
// actions (task-done, task-fail).
type ExecTaskNarrationOut struct {
	pipeline.Narration
}

// ExecWaveCommitOut is the narrated output for the wave-commit action.
// Committed reports whether a commit now exists for the wave (true both
// for a freshly-made commit and for an idempotent resume that found one
// already recorded). SHA and Idempotent are only meaningful when Committed
// is true; Reason explains a soft "no-op" (empty diff, or commits disabled
// via config).
type ExecWaveCommitOut struct {
	pipeline.Narration
	Committed  bool   `json:"committed"`
	SHA        string `json:"sha,omitempty"`
	Idempotent bool   `json:"idempotent,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// TaskContextOut is the returned payload for the task-context action: a
// single-call consolidation of what a dispatched per-task worker needs —
// its fact sheet (which already embeds the plan-task's Contract/Acceptance
// Criteria/Files block, see wave.Factsheet), a live prior-wave summary,
// verify guidance, and report-back instructions. Task 12 wires this into a
// two-line worker dispatch form in place of today's fully-inlined prompts.
type TaskContextOut struct {
	TaskID     string `json:"taskId"`
	FactSheet  string `json:"factSheet"`
	PriorWaves string `json:"priorWaves"`
	Verify     string `json:"verify"`
	ReportBack string `json:"reportBack"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// ---------------------------------------------------------------------------
// Narration helpers
// ---------------------------------------------------------------------------

// execDetailLevel returns "concise" or "full" from the input's Detail field.
func execDetailLevel(in ExecuteStateIn) string {
	if in.Detail == "concise" {
		return "concise"
	}
	return "full"
}

// execValidateDetail returns a DomainError if the detail value is invalid.
func execValidateDetail(in ExecuteStateIn) error {
	if in.Detail != "" && in.Detail != "concise" && in.Detail != "full" {
		return &mcpserver.DomainError{Msg: fmt.Sprintf("detail must be \"concise\" or \"full\", got %q", in.Detail)}
	}
	return nil
}

// waveComplexityBucket returns a TimingsStore key for wave duration based
// on the maximum task complexity in the wave. Complexity values follow the
// plan's bounded enum: Trivial, Standard, Complex. Unknown or empty values
// map to "wave:unknown".
func waveComplexityBucket(maxComplexity string) string {
	switch maxComplexity {
	case "Trivial", "Standard", "Complex":
		return "wave:" + strings.ToLower(maxComplexity)
	default:
		return "wave:unknown"
	}
}

// execMaxComplexityFromTasks extracts the highest complexity from a parsed
// tasksJson slice. Ordering: Complex > Standard > Trivial.
func execMaxComplexityFromTasks(tasks []any) string {
	rank := map[string]int{"Trivial": 1, "Standard": 2, "Complex": 3}
	best := ""
	bestRank := 0
	for _, t := range tasks {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		c, _ := tm["complexity"].(string)
		if r, ok := rank[c]; ok && r > bestRank {
			best = c
			bestRank = r
		}
	}
	return best
}

// execMaxComplexityFromWave extracts the highest complexity from the wave
// map's tasks[] array (populated by task-done/task-fail).
func execMaxComplexityFromWave(w map[string]any) string {
	tasks, _ := w["tasks"].([]any)
	return execMaxComplexityFromTasks(tasks)
}

// staticWaveETA maps complexity buckets to fallback ETA seconds when
// TimingsStore has no recorded history.
var staticWaveETA = map[string]int{
	"wave:trivial":  120, // 2 minutes
	"wave:standard": 300, // 5 minutes
	"wave:complex":  480, // 8 minutes
	"wave:unknown":  300, // 5 minutes (conservative default)
}

// execWaveETA returns an ETA in seconds and the basis string for a wave.
// Prefers TimingsStore history; falls back to the static table.
func execWaveETA(ts *pipeline.TimingsStore, bucket string) (int, string) {
	if ts != nil {
		if est, ok := ts.Estimate(bucket); ok {
			return est.Seconds, est.Basis
		}
	}
	if sec, ok := staticWaveETA[bucket]; ok {
		return sec, "static estimate"
	}
	return 300, "static estimate"
}

// execCountWaveOutcomes counts completed, failed, and total reported tasks
// from a wave map's tasks[] array.
func execCountWaveOutcomes(w map[string]any) (completed, failed, total int) {
	tasks, _ := w["tasks"].([]any)
	total = len(tasks)
	for _, t := range tasks {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		switch tm["status"] {
		case "completed":
			completed++
		case "failed", "skipped-dependency":
			failed++
		}
	}
	return
}

// execBuildWaveTasks converts a wave map's tasks[] to pipeline.WaveTask
// for use with WaveStartBlock/WaveEndBlock rendering. Model is filled from
// the task's complexity since model tier is unavailable in execute_state.
func execBuildWaveTasks(tasks []any) []pipeline.WaveTask {
	var out []pipeline.WaveTask
	for _, t := range tasks {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		idStr, _ := tm["id"].(string)
		id := 0
		if idStr != "" {
			fmt.Sscanf(idStr, "%d", &id)
		}
		name, _ := tm["name"].(string)
		model, _ := tm["complexity"].(string) // complexity stands in for model
		if model == "" {
			model = "?"
		}
		out = append(out, pipeline.WaveTask{ID: id, Name: name, Model: model})
	}
	return out
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

- wave-compute: Stateless — parses the plan file at planPath and computes the wave schedule (no state file read/write). Requires planPath. Optional: extraDepsJson (JSON array of {task, dependsOn, reason} merged with each task's explicit "Depends on" field). Returns {route, preWave, waves[{number, tasks[], expectedFiles[], verificationHint}]}.
- init: Create execution state. Runs the same config auto-migration gate as ship_prepare first (migrates and backs up an outdated config, or fails with a /setup pointer if none exists); result may include a "migration" report. Requires branch, quality. Optional: totalTasks, plannedTaskIds, planPath, planHash.
- wave-start: Begin a wave. Returns narration (summary, display with task list + ETA, next). Requires wave. Optional: branch, tasksJson, runId (for fact sheets), detail ("concise"|"full").
- wave-done: Complete a wave. Returns narration (summary, display with outcomes, timing, next wave preview + ETA). Records wave duration to TimingsStore. Requires wave. Optional: branch, decisions, status, detail ("concise"|"full").
- wave-fail: Fail a wave. Returns narration (summary, display with failure cause). Requires wave. Optional: branch, timedOut, error (failure cause, recorded as an issue and in failedWave), status, detail ("concise"|"full").
- wave-committed: Record a commit SHA for a completed wave. Requires wave. Optional: branch, sha.
- wave-commit: Stage and commit a completed wave's changes (git add -A + git commit -m message) and record the resulting sha on the wave, mirroring wave-committed's SHA-recording. Requires wave, message. Optional: branch, detail ("concise"|"full"). The wave must already be "completed" (call wave-done first). Empty diff: succeeds without committing ({committed:false, reason:"nothing to commit"}). When config execute.commitWaves is false, does not commit and instead returns an instruction to commit manually and call wave-committed. Idempotent on resume: an already-recorded committedSha that is still an ancestor of HEAD is reported ({idempotent:true}) rather than committed again.
- task-done: Record task completion. Returns narration (summary with running tally). Requires wave, taskId. Optional: branch, taskName, complexity, risk, filesChanged, filesAdded, verifyToken, status ("DONE_WITH_CONCERNS" records a warning issue), error (concern detail for DONE_WITH_CONCERNS).
- task-fail: Record task failure. Returns narration (summary with running tally). Requires wave, taskId. Optional: branch, error, skippedDependency (records an issue; only a non-skipped failure updates failedTask).
- task-context: Return everything a dispatched per-task worker needs in one call — fact-sheet content (embeds the plan-task's Contract/Acceptance Criteria/Files), a live prior-wave summary, verify guidance, and report-back instructions. Requires taskId. Optional: branch, runId (falls back the same way wave-start does, via startedAt/wave). The serialized payload is capped at 1 MiB; oversize content (fact sheet first, then prior-wave summary if still over cap) is truncated with truncated:true rather than erroring. Unknown taskId fails with an actionable error listing the valid IDs for that run.
- context: Read/write shared context keys. Requires data (JSON object with allowed keys: planSummary, completedTaskIds, filesAdded, filesModified, interfacesCreated, decisionsFromPriorWaves). Optional: branch, maxFiles, maxDecisions, maxInterfaces, maxTaskIds.
- read: Return the full execution state blob. Optional: branch. When the run is in flight (some recorded wave isn't "completed", or plannedTaskIds has IDs not yet in context.completedTaskIds), the blob also carries a "resumeBriefing" (resumable, wavesDone, wavesRemaining, gitCrossCheck, gitMismatches, willRedo, willSkip, summary, display, next) — a dry-run preview of what resume-reset would do. A committedSha that no longer checks out as a git ancestor is reported via gitCrossCheck/gitMismatches, never as a read failure.
- cleanup: Stamp a branch's execution state terminal (runStatus:"completed", runCompletedAt) instead of deleting it — the state file (and its issues[]) survives for later reads (e.g. /harden) until GC's TTL prunes it. Also removes the per-run working directory and ledger directory (working artifacts only, safe to delete) when the state carries a startedAt to derive the runID from; if startedAt is absent, directories are left untouched. Optional: branch.
- gc: Garbage-collect stale state files. Optional: ttlDays, dryRun, branch.
- summarize-prior-wave-context: Summarize context from prior waves. Optional: branch, maxFiles, maxDecisions, maxInterfaces, maxTaskIds.
- wave-split: Split remaining tasks into a new wave. Requires dispatched. Optional: wave, missingIds, branch, splitDepth, maxSplitDepth, stateFile.
- verify-completeness: Verify all planned tasks are accounted for. Optional: branch, stateFile.
- wave-progress: Read/write per-task progress. Requires runId. For reads: readProgress=true. For writes: taskId, phase. Optional: lastCompletedTask (recorded in the heartbeat entry).
- resume-reset: Reset in-progress waves for session resume. Optional: branch, stateFile. Returns {resetWaves, clearedTaskIds} as before; when the run is still in flight after the reset, the response also carries a "resumeBriefing" (same shape as read's) reflecting the sets it just cleared — resume-reset's willRedo always matches the task IDs in clearedTaskIds.
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
	case "wave-compute":
		return execActionWaveCompute(in)
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
	case "wave-commit":
		return execActionWaveCommit(root, workDir, in)
	case "task-done":
		return execActionTaskDone(root, workDir, in, now)
	case "task-fail":
		return execActionTaskFail(root, workDir, in, now)
	case "task-context":
		return execActionTaskContext(root, workDir, in)
	case "context":
		return execActionContext(root, workDir, in)
	case "read":
		return execActionRead(root, workDir, in)
	case "cleanup":
		return execActionCleanup(root, workDir, in, now)
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

// ---------------------------------------------------------------------------
// Issue accumulator (StateIssue)
// ---------------------------------------------------------------------------

// StateIssue is a structured entry in a state file's issues[] accumulator,
// recording task/wave/step failures and concerns for end-of-run summaries
// and harden analysis. Shared by execute-state and ship-state. Aliased to
// pipeline.StateIssue (rather than duplicated) so tools code can hand issue
// slices straight to pipeline.IssueSummaryBlock with no conversion step —
// internal/pipeline cannot import internal/tools (tools imports pipeline for
// Narration embedding), so pipeline holds the canonical definition and tools
// aliases it.
type StateIssue = pipeline.StateIssue

// execAppendIssue appends a StateIssue to data["issues"], round-tripping it
// through JSON so the stored representation is always a map[string]any —
// matching every other entry in data, whether freshly appended or loaded
// back from disk.
func execAppendIssue(data map[string]any, issue StateIssue) {
	raw, ok := data["issues"].([]any)
	if !ok {
		raw = []any{}
	}
	b, err := json.Marshal(issue)
	if err != nil {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return
	}
	data["issues"] = append(raw, m)
}

// execIssueSummary returns the total issue count and up to maxHighlights
// "[severity] summary" strings for the most recent issues in data["issues"],
// for inclusion in wave-done/complete-step responses. issueCount and
// issueHighlights are response-only — never persisted to the state file.
// Returns (0, nil) when there are no issues.
func execIssueSummary(data map[string]any, maxHighlights int) (int, []string) {
	raw, ok := data["issues"].([]any)
	if !ok || len(raw) == 0 {
		return 0, nil
	}
	start := 0
	if len(raw) > maxHighlights {
		start = len(raw) - maxHighlights
	}
	highlights := make([]string, 0, len(raw)-start)
	for _, v := range raw[start:] {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		sev, _ := m["severity"].(string)
		summary, _ := m["summary"].(string)
		highlights = append(highlights, fmt.Sprintf("[%s] %s", sev, summary))
	}
	return len(raw), highlights
}

// IssueSummary is the end-of-run grouped issue report returned by the
// completion actions (execute's cleanup, ship's cleanup-pipeline) once
// data["issues"] is non-empty. Response-only — never persisted to the state
// file. Display is pipeline.IssueSummaryBlock(items) rendered server-side so
// the calling SKILL.md can print it verbatim, matching this codebase's
// "render display fields verbatim" convention. HardenSuggestion is set only
// when at least one error-severity issue is present; it is advisory prose
// for the executing agent, not an automatic /harden invocation.
type IssueSummary struct {
	Total            int            `json:"total"`
	ByCategory       map[string]int `json:"byCategory"`
	Items            []StateIssue   `json:"items"`
	Display          string         `json:"display"`
	HardenSuggestion string         `json:"hardenSuggestion,omitempty"`
}

// execIssueSummaryFull builds the full end-of-run IssueSummary from
// data["issues"]. Returns nil when there are no issues — callers must omit
// the issueSummary key entirely in that case (no "0 issues" noise).
func execIssueSummaryFull(data map[string]any) *IssueSummary {
	raw, ok := data["issues"].([]any)
	if !ok || len(raw) == 0 {
		return nil
	}

	items := make([]StateIssue, 0, len(raw))
	byCategory := map[string]int{}
	var errSummaries []string
	for _, v := range raw {
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		var iss StateIssue
		if err := json.Unmarshal(b, &iss); err != nil {
			continue
		}
		items = append(items, iss)
		if iss.Category != "" {
			byCategory[iss.Category]++
		}
		if iss.Severity == "error" {
			errSummaries = append(errSummaries, iss.Summary)
		}
	}
	if len(items) == 0 {
		return nil
	}

	summary := &IssueSummary{
		Total:      len(items),
		ByCategory: byCategory,
		Items:      items,
		Display:    pipeline.IssueSummaryBlock(items),
	}
	if len(errSummaries) > 0 {
		if len(errSummaries) > 3 {
			errSummaries = errSummaries[:3]
		}
		summary.HardenSuggestion = fmt.Sprintf("Run /harden --failure-text '%s' to strengthen guardrails.", strings.Join(errSummaries, "; "))
	}
	return summary
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

	// KD5 gate: same auto-migrate-with-backup gate as ship_prepare
	// (configmigrate.MigrateWithBackup). Unlike ship_prepare's soft
	// errors-only style, execute_state has no equivalent partial-payload
	// convention for this action — a genuinely missing config (never ran
	// /setup) or a too-new schema is reported the same way as any other
	// execActionInit validation failure: a Go error mapped to the tool's
	// {"ok":false,...} envelope.
	changes, backupPath, err := configmigrate.MigrateWithBackup(root)
	if err != nil {
		return nil, &mcpserver.DataError{Msg: fmt.Sprintf("config-version: %s", err.Error()), Cause: err}
	}
	var migrationReport *MigrationReport
	if backupPath != "" {
		migrationReport = &MigrationReport{Changes: changes, BackupPath: backupPath}
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

	result := map[string]any{"filePath": st.Path}
	if migrationReport != nil {
		result["migration"] = migrationReport
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Action: wave-start
// ---------------------------------------------------------------------------

func execActionWaveStart(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.Wave == nil {
		return nil, &mcpserver.DomainError{Msg: "--wave is required"}
	}
	if err := execValidateDetail(in); err != nil {
		return nil, err
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
	w := execFindWave(st.Data, *in.Wave)
	if w != nil {
		w["status"] = "in_progress"
		if _, ok := w["startedAt"]; !ok {
			w["startedAt"] = now().UTC().Format(time.RFC3339)
		}
	} else {
		waves := execEnsureWaves(st.Data)
		w = map[string]any{
			"number":    *in.Wave,
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
	var parsedTasks []any
	result := ExecWaveNarrationOut{}

	if in.TasksJSON != "" {
		if err := json.Unmarshal([]byte(in.TasksJSON), &parsedTasks); err != nil {
			return nil, &mcpserver.DomainError{Msg: "tasksJson is not valid JSON: " + err.Error(), Cause: err}
		}

		runID := in.RunID
		if runID == "" {
			runID = execDeriveRunID(st.Data, *in.Wave)
		}

		summary := execSummarizePriorWaveCtx(st.Data, root, 0, 0, 0, 0)

		writtenPaths := []string{}
		var factSheetErrors []string
		for _, t := range parsedTasks {
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
				factSheetErrors = append(factSheetErrors, fmt.Sprintf("task %s: %s", id, err.Error()))
				continue
			}
			writtenPaths = append(writtenPaths, p)
		}

		result.RunID = runID
		result.FactSheets = writtenPaths
		if len(factSheetErrors) > 0 {
			result.FactSheetErrors = factSheetErrors
		}
	}

	// Build narration.
	taskCount := len(parsedTasks)
	result.Summary = fmt.Sprintf("Wave %d started with %d tasks.", *in.Wave, taskCount)

	if execDetailLevel(in) == "full" {
		waveTasks := execBuildWaveTasks(parsedTasks)
		wi := pipeline.WaveInfo{
			Number: *in.Wave,
			Tasks:  waveTasks,
		}
		// Pass nil for TimingsStore — ETA lives in Next, not in the block.
		result.Display = pipeline.WaveStartBlock(wi, nil)
	}

	// Build Next with ETA.
	maxC := execMaxComplexityFromTasks(parsedTasks)
	bucket := waveComplexityBucket(maxC)
	ts := pipeline.NewTimingsStore(root)
	etaSec, etaBasis := execWaveETA(ts, bucket)
	result.Next = &pipeline.NextAction{
		ID:          fmt.Sprintf("wave-%d", *in.Wave),
		Instruction: fmt.Sprintf("Execute the %d tasks in wave %d, then call task-done/task-fail for each.", taskCount, *in.Wave),
		EtaSeconds:  etaSec,
		EtaBasis:    etaBasis,
	}

	return result, nil
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
	if in.Wave == nil {
		return nil, &mcpserver.DomainError{Msg: "--wave is required"}
	}
	if err := execValidateDetail(in); err != nil {
		return nil, err
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

	w := execFindOrCreateWave(st.Data, *in.Wave, now)

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
	completedAt := now().UTC().Format(time.RFC3339)
	w["completedAt"] = completedAt

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

	// Build narration.
	completed, failed, total := execCountWaveOutcomes(w)
	startedAt, _ := w["startedAt"].(string)

	ts := pipeline.NewTimingsStore(root)

	// Record wave duration (only when wave pre-existed with a real startedAt
	// and duration > 0 — avoid polluting the store with 0s durations from
	// waves that were never properly started).
	maxC := execMaxComplexityFromWave(w)
	bucket := waveComplexityBucket(maxC)
	var waveDur time.Duration
	var waveDurOK bool
	if startedAt != "" {
		if d, ok := pipeline.Duration(startedAt, completedAt); ok && d > 0 {
			waveDur = d
			waveDurOK = true
			_ = ts.Record(bucket, d) // best-effort metric; failure does not affect state
		}
	}

	result := ExecWaveNarrationOut{}
	if count, highlights := execIssueSummary(st.Data, 5); count > 0 {
		result.IssueCount = count
		result.IssueHighlights = highlights
	}

	durStr := ""
	if waveDurOK {
		durStr = " in " + pipeline.Humanize(waveDur)
	}
	result.Summary = fmt.Sprintf("Wave %d done%s: %d/%d tasks succeeded, %d failed.",
		*in.Wave, durStr, completed, total, failed)

	if execDetailLevel(in) == "full" {
		waveTasks := execBuildWaveTasks(func() []any {
			t, _ := w["tasks"].([]any)
			return t
		}())
		wi := pipeline.WaveInfo{
			Number:      *in.Wave,
			Tasks:       waveTasks,
			StartedAt:   startedAt,
			CompletedAt: completedAt,
		}
		// Pass nil for TimingsStore — ETA lives in Next, not in the block.
		result.Display = pipeline.WaveEndBlock(wi, nil)
	}

	// Timing info.
	if waveDurOK {
		timing := &pipeline.TimingInfo{
			StepSeconds: int(waveDur.Round(time.Second).Seconds()),
			Human:       "wave " + pipeline.Humanize(waveDur),
		}
		if pipelineStartedAt, _ := st.Data["startedAt"].(string); pipelineStartedAt != "" {
			if pStart, parseErr := time.Parse(time.RFC3339, pipelineStartedAt); parseErr == nil {
				timing.PipelineSeconds = int(now().Sub(pStart).Round(time.Second).Seconds())
				timing.Human += ", pipeline " + pipeline.Humanize(time.Duration(timing.PipelineSeconds)*time.Second)
			}
		}
		result.Timing = timing
	}

	// Next action: suggest wave-commit then next wave.
	nextWave := *in.Wave + 1
	etaSec, etaBasis := execWaveETA(ts, bucket)
	result.Next = &pipeline.NextAction{
		ID:          fmt.Sprintf("wave-%d", nextWave),
		Instruction: fmt.Sprintf("Call wave-commit, then wave-start for wave %d.", nextWave),
		EtaSeconds:  etaSec,
		EtaBasis:    etaBasis,
	}

	return result, nil
}

// ---------------------------------------------------------------------------
// Action: wave-fail
// ---------------------------------------------------------------------------

func execActionWaveFail(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.Wave == nil {
		return nil, &mcpserver.DomainError{Msg: "--wave is required"}
	}
	if err := execValidateDetail(in); err != nil {
		return nil, err
	}

	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}

	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}

	w := execFindOrCreateWave(st.Data, *in.Wave, now)
	w["status"] = "failed"
	w["completedAt"] = now().UTC().Format(time.RFC3339)

	st.Data["failedWave"] = *in.Wave
	detail := in.ErrorText
	if detail == "" && in.TimedOut {
		detail = "timed out"
	}
	execAppendIssue(st.Data, StateIssue{
		Wave:      *in.Wave,
		Severity:  "error",
		Category:  "wave-fail",
		Summary:   fmt.Sprintf("Wave %d failed", *in.Wave),
		Detail:    detail,
		Timestamp: now().UTC().Format(time.RFC3339),
	})

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	// Narration.
	completed, failed, total := execCountWaveOutcomes(w)
	cause := "failure"
	if in.TimedOut {
		cause = "timeout"
	}
	summaryText := fmt.Sprintf("Wave %d failed (%s): %d/%d succeeded, %d failed.",
		*in.Wave, cause, completed, total, failed)
	if detail != "" {
		summaryText += " " + detail
	}

	result := ExecWaveNarrationOut{}
	result.Summary = summaryText
	if execDetailLevel(in) == "full" {
		result.Display = fmt.Sprintf("**Wave %d failed** (%s)\n\n%s", *in.Wave, cause, detail)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Action: wave-committed
// ---------------------------------------------------------------------------

func execActionWaveCommitted(root, workDir string, in ExecuteStateIn) (any, error) {
	if in.Wave == nil {
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

	w := execFindWave(st.Data, *in.Wave)
	if w == nil {
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("wave %d not found in state", *in.Wave),
		}
	}

	waveStatus, _ := w["status"].(string)
	if waveStatus != "completed" {
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("wave %d status is %q, expected \"completed\"", *in.Wave, waveStatus),
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
			Msg: fmt.Sprintf("wave %d already has committedSha %q — refusing to overwrite with %v", *in.Wave, existing, newSha),
		}
	}

	w["committedSha"] = newSha
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}
	return map[string]any{"committedSha": newSha, "idempotent": false}, nil
}

// ---------------------------------------------------------------------------
// Action: wave-commit
// ---------------------------------------------------------------------------

// execCommitWavesEnabled reads config.execute.commitWaves. It defaults to
// true (tool-side commits are opt-out, not opt-in) when the key is absent,
// the execute section itself is absent, or the config cannot be read —
// mirroring execSummarizePriorWaveCtx's tolerant config.ReadSection usage
// elsewhere in this file.
func execCommitWavesEnabled(root string) bool {
	execSection, err := config.ReadSection(root, "execute")
	if err != nil || execSection == nil {
		return true
	}
	if v, ok := execSection["commitWaves"].(bool); ok {
		return v
	}
	return true
}

// execIsAncestor reports whether sha is an ancestor of (or equal to) HEAD
// in the git repo at dir, via `git merge-base --is-ancestor`. It returns
// (false, nil) — not an error — when the check cleanly determines sha is
// NOT an ancestor (git exit code 1), distinguishing that clean "no" from a
// real git failure (bad sha, not a repo, etc.), which is returned as a
// non-nil error. Mirrors verifyTagAncestry's exit-code handling in
// scaffold.go.
func execIsAncestor(dir, sha string) (bool, error) {
	_, err := execx.Run("git", []string{"merge-base", "--is-ancestor", sha, "HEAD"}, execx.Options{Dir: dir})
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// shortSHA truncates a git commit sha to 7 characters for human-facing
// narration text. The full sha is still what gets stored in state and
// returned in the "sha" response field.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// execActionWaveCommit stages and commits a completed wave's changes
// (git add -A + git commit) and records the resulting sha on the wave,
// reusing wave-committed's completed-status and SHA-recording checks
// rather than diverging from them.
func execActionWaveCommit(root, workDir string, in ExecuteStateIn) (any, error) {
	if in.Wave == nil {
		return nil, &mcpserver.DomainError{Msg: "--wave is required"}
	}
	if err := execValidateDetail(in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Message) == "" {
		return nil, &mcpserver.DomainError{Msg: "message is required for wave-commit"}
	}

	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}

	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}

	w := execFindWave(st.Data, *in.Wave)
	if w == nil {
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("wave %d not found in state", *in.Wave),
		}
	}

	waveStatus, _ := w["status"].(string)
	if waveStatus != "completed" {
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("wave %d status is %q, expected \"completed\"", *in.Wave, waveStatus),
		}
	}

	nextWave := *in.Wave + 1
	nextInstruction := fmt.Sprintf("Call wave-start for wave %d.", nextWave)
	full := execDetailLevel(in) == "full"

	// Idempotency: a committedSha already recorded (e.g. this action ran
	// to completion once but the caller's session ended before it learned
	// the result, and is now resuming) is not re-committed as long as it
	// is still an ancestor of HEAD. A recorded sha that HEAD has diverged
	// from is a conflict this action refuses to paper over automatically.
	if existing, hasSha := w["committedSha"]; hasSha {
		if existingSha, ok := existing.(string); ok && existingSha != "" {
			isAncestor, ancErr := execIsAncestor(workDir, existingSha)
			if ancErr != nil {
				return nil, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("git merge-base --is-ancestor %s HEAD: %s", existingSha, ancErr.Error()),
					Cause: ancErr,
				}
			}
			if !isAncestor {
				return nil, &mcpserver.DomainError{
					Msg: fmt.Sprintf("wave %d already has committedSha %q which is not an ancestor of HEAD — refusing to commit again automatically", *in.Wave, existingSha),
				}
			}

			result := ExecWaveCommitOut{Committed: true, SHA: existingSha, Idempotent: true}
			result.Summary = fmt.Sprintf("Wave %d already committed as %s.", *in.Wave, shortSHA(existingSha))
			if full {
				result.Display = fmt.Sprintf("✔ wave %d → commit %s (already committed)", *in.Wave, shortSHA(existingSha))
			}
			result.Next = &pipeline.NextAction{ID: fmt.Sprintf("wave-%d", nextWave), Instruction: nextInstruction}
			return result, nil
		}
	}

	if !execCommitWavesEnabled(root) {
		result := ExecWaveCommitOut{Committed: false, Reason: "execute.commitWaves is false"}
		result.Summary = fmt.Sprintf("Wave %d not committed (execute.commitWaves is false).", *in.Wave)
		if full {
			result.Display = fmt.Sprintf("○ wave %d → commit manually (execute.commitWaves is false)", *in.Wave)
		}
		result.Next = &pipeline.NextAction{
			ID:          fmt.Sprintf("wave-%d-manual-commit", *in.Wave),
			Instruction: fmt.Sprintf("execute.commitWaves is false: commit wave %d manually (git add -A && git commit -m \"...\"), then call wave-committed with the resulting sha before wave-start for wave %d.", *in.Wave, nextWave),
		}
		return result, nil
	}

	if _, err := execx.Run("git", []string{"add", "-A"}, execx.Options{Dir: workDir}); err != nil {
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("git add: %s", err.Error()), Cause: err}
	}

	staged, err := execx.Run("git", []string{"diff", "--cached", "--name-only"}, execx.Options{Dir: workDir})
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("git diff --cached: %s", err.Error()), Cause: err}
	}
	staged = strings.TrimSpace(staged)
	if staged == "" {
		result := ExecWaveCommitOut{Committed: false, Reason: "nothing to commit"}
		result.Summary = fmt.Sprintf("Wave %d: nothing to commit.", *in.Wave)
		if full {
			result.Display = fmt.Sprintf("○ wave %d → nothing to commit", *in.Wave)
		}
		result.Next = &pipeline.NextAction{ID: fmt.Sprintf("wave-%d", nextWave), Instruction: nextInstruction}
		return result, nil
	}
	fileCount := len(strings.Split(staged, "\n"))

	// Commit message lands verbatim: no tool-added prefix.
	if _, err := execx.Run("git", []string{"commit", "-m", in.Message}, execx.Options{Dir: workDir}); err != nil {
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("git commit: %s", err.Error()), Cause: err}
	}

	sha, err := shipHeadSHA(workDir)
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("git rev-parse HEAD: %s", err.Error()), Cause: err}
	}

	w["committedSha"] = sha
	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	result := ExecWaveCommitOut{Committed: true, SHA: sha, Idempotent: false}
	result.Summary = fmt.Sprintf("Wave %d committed as %s (%d files).", *in.Wave, shortSHA(sha), fileCount)
	if full {
		result.Display = fmt.Sprintf("✔ wave %d → commit %s", *in.Wave, shortSHA(sha))
	}
	result.Next = &pipeline.NextAction{ID: fmt.Sprintf("wave-%d", nextWave), Instruction: nextInstruction}
	return result, nil
}

// ---------------------------------------------------------------------------
// Action: task-done
// ---------------------------------------------------------------------------

func execActionTaskDone(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.Wave == nil {
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

	w := execFindOrCreateWave(st.Data, *in.Wave, now)
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

	if in.Status == "DONE_WITH_CONCERNS" {
		execAppendIssue(st.Data, StateIssue{
			Wave:      *in.Wave,
			TaskID:    in.TaskID,
			Severity:  "warning",
			Category:  "done-with-concerns",
			Summary:   fmt.Sprintf("Task %s completed with concerns", in.TaskID),
			Detail:    in.ErrorText,
			Timestamp: now().UTC().Format(time.RFC3339),
		})
	}

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

	// Narration: running tally.
	completed, _, total := execCountWaveOutcomes(w)
	result := ExecTaskNarrationOut{}
	result.Summary = fmt.Sprintf("Task %s done (%d/%d reported).", in.TaskID, completed, total)
	return result, nil
}

// ---------------------------------------------------------------------------
// Action: task-fail
// ---------------------------------------------------------------------------

func execActionTaskFail(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.Wave == nil {
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

	w := execFindOrCreateWave(st.Data, *in.Wave, now)
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

	// failedTask records the root failure, not a dependent skipped as a
	// consequence of it — a skipped-dependency task-fail must not overwrite
	// an earlier real failure.
	if !in.SkippedDep {
		st.Data["failedTask"] = in.TaskID
	}

	summary := fmt.Sprintf("Task %s failed", in.TaskID)
	if in.SkippedDep {
		summary = fmt.Sprintf("Task %s skipped (dependency failed)", in.TaskID)
	}
	execAppendIssue(st.Data, StateIssue{
		Wave:      *in.Wave,
		TaskID:    in.TaskID,
		Severity:  "error",
		Category:  "task-fail",
		Summary:   summary,
		Detail:    in.ErrorText,
		Timestamp: now().UTC().Format(time.RFC3339),
	})

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	// Narration: running tally.
	completed, failed, total := execCountWaveOutcomes(w)
	result := ExecTaskNarrationOut{}
	if in.SkippedDep {
		result.Summary = fmt.Sprintf("Task %s skipped (%d/%d reported, %d failed).", in.TaskID, completed+failed, total, failed)
	} else {
		result.Summary = fmt.Sprintf("Task %s failed (%d/%d reported, %d failed).", in.TaskID, completed+failed, total, failed)
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Action: task-context
// ---------------------------------------------------------------------------

// execTaskContextMaxBytes caps the serialized TaskContextOut payload, not
// just the FactSheet field: FactSheet is the field most likely to be large
// in practice, but PriorWaves carries execSummarizePriorWaveCtx's
// planSummary verbatim (see execRenderPriorWaveSummary), which is itself
// unbounded — a pathological planSummary could otherwise push the payload
// over cap while FactSheet alone stayed within limits. Same 1 MiB magnitude
// as execReadMaxBytes, but this action truncates rather than hard-failing:
// task-context serves a live worker dispatch, where a usable truncated
// payload beats an outright error over a task the worker still has to
// attempt.
const execTaskContextMaxBytes = 1 << 20 // 1 MiB

const execTaskContextTruncationNote = "\n\n... [truncated to fit the 1 MiB task-context cap]"

// execTaskContextCapPayload enforces execTaskContextMaxBytes on the
// serialized result, truncating whichever of FactSheet/PriorWaves is
// currently larger (i.e. actually driving the overage) and re-measuring, so
// a single oversize field is fixed in one pass and two simultaneously
// oversize fields are both brought within cap.
func execTaskContextCapPayload(result *TaskContextOut) {
	trim := func(overage int) {
		if len(result.FactSheet) >= len(result.PriorWaves) {
			cut := len(result.FactSheet) - overage
			if cut < 0 {
				cut = 0
			}
			result.FactSheet = strings.ToValidUTF8(result.FactSheet[:cut], "") + execTaskContextTruncationNote
		} else {
			cut := len(result.PriorWaves) - overage
			if cut < 0 {
				cut = 0
			}
			result.PriorWaves = strings.ToValidUTF8(result.PriorWaves[:cut], "") + execTaskContextTruncationNote
		}
		result.Truncated = true
	}

	raw, err := json.Marshal(result)
	if err != nil || len(raw) <= execTaskContextMaxBytes {
		return
	}
	trim(len(raw) - execTaskContextMaxBytes)

	// Re-measure: a single trim pass can undershoot if both fields were
	// individually huge (a pathological planSummary alongside an oversize
	// fact sheet).
	raw, err = json.Marshal(result)
	if err == nil && len(raw) > execTaskContextMaxBytes {
		trim(len(raw) - execTaskContextMaxBytes)
	}
}

// execTaskContextVerify returns static verify guidance for a dispatched
// worker, naming taskID so the git-diff scope check and VERIFY: canary line
// it asks for are unambiguous when a worker is handling more than one task.
func execTaskContextVerify(taskID string) string {
	return fmt.Sprintf(
		"Run task %s's own build/tests as scoped by the Acceptance Criteria and Files list in "+
			"the fact sheet above. Confirm via `git diff` that only files from that Files list "+
			"changed. Your completion report MUST include a `VERIFY: <symbol> in <file>` canary "+
			"line naming a real symbol you added or changed for this task — it is the only proof "+
			"the main session has that the change is real, not phantom.",
		taskID,
	)
}

// execTaskContextReportBack returns static report-back instructions for a
// dispatched worker, mirroring the heartbeat/completion-block conventions
// documented in plugins/sdlc/skills/execute/SKILL.md and
// classifying-and-waving-tasks.md (wave-progress phases, task-done/task-fail
// recorded by the main session, not the worker itself).
func execTaskContextReportBack(taskID string) string {
	return fmt.Sprintf(
		"Emit a heartbeat as you enter each phase: execute_state({ action: \"wave-progress\", "+
			"runId: \"<RUN_ID>\", taskId: %q, phase: <phase> }) for phase in started, reading, "+
			"editing, verifying, reporting (each once). When finished, report status "+
			"SUCCESS | DONE_WITH_CONCERNS | FAILED with the COMPLETE:/VERIFY:/INTERFACES:/"+
			"DECISIONS:/STATUS: block — the main session records it via execute_state({ action: "+
			"\"task-done\" | \"task-fail\", taskId: %q, ... }). Do not call task-done/task-fail "+
			"yourself.",
		taskID, taskID,
	)
}

// execRenderPriorWaveSummary renders the bounded prior-wave context map
// returned by execSummarizePriorWaveCtx as compact text. This is a live
// on-demand render of the same context that renderFactSheet baked into the
// task's fact sheet as of wave-start, so task-context can reflect updates
// (e.g. sibling tasks in the same wave that have since completed) rather
// than only the wave-start-time snapshot on disk.
func execRenderPriorWaveSummary(summary map[string]any) string {
	var b strings.Builder
	if ps, _ := summary["planSummary"].(string); ps != "" {
		b.WriteString("Plan summary: ")
		b.WriteString(ps)
		b.WriteString("\n\n")
	}

	rows := []struct {
		label string
		key   string
	}{
		{"Completed task IDs", "completedTaskIds"},
		{"Files added", "filesAdded"},
		{"Files modified", "filesModified"},
		{"Interfaces", "interfacesCreated"},
		{"Decisions", "decisionsFromPriorWaves"},
	}
	for _, r := range rows {
		vals := anyToStringSlice(summary[r.key])
		if len(vals) == 0 {
			continue
		}
		b.WriteString(r.label)
		b.WriteString(":\n")
		for _, v := range vals {
			b.WriteString("- ")
			b.WriteString(v)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	if b.Len() == 0 {
		return "No prior-wave context recorded yet."
	}
	return strings.TrimRight(b.String(), "\n")
}

func execActionTaskContext(root, workDir string, in ExecuteStateIn) (any, error) {
	taskID := strings.TrimSpace(in.TaskID)
	if taskID == "" {
		return nil, &mcpserver.DomainError{Msg: "taskId is required for task-context"}
	}

	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}
	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}

	runID := in.RunID
	if runID == "" {
		waveNum := 0
		if in.Wave != nil {
			waveNum = *in.Wave
		}
		runID = execDeriveRunID(st.Data, waveNum)
	}

	_, content, err := wave.ReadFactsheet(root, runID, taskID)
	if err != nil {
		if errors.Is(err, wave.ErrFactsheetNotFound) {
			ids, listErr := wave.ListFactsheetIDs(root, runID)
			if listErr != nil {
				return nil, &mcpserver.InfraError{Msg: "list fact sheets: " + listErr.Error(), Cause: listErr}
			}
			msg := fmt.Sprintf("no fact sheet for task %q under run %q", taskID, runID)
			if len(ids) > 0 {
				msg += "; valid task IDs: " + strings.Join(ids, ", ")
			} else {
				msg += "; run has no fact sheets yet (call wave-start first)"
			}
			return nil, &mcpserver.DomainError{Msg: msg, Cause: err}
		}
		if errors.Is(err, wave.ErrBadRunID) {
			return nil, &mcpserver.DomainError{Msg: err.Error(), Cause: err}
		}
		return nil, &mcpserver.InfraError{Msg: "read fact sheet: " + err.Error(), Cause: err}
	}

	summary := execSummarizePriorWaveCtx(st.Data, root, 0, 0, 0, 0)

	result := TaskContextOut{
		TaskID:     taskID,
		FactSheet:  content,
		PriorWaves: execRenderPriorWaveSummary(summary),
		Verify:     execTaskContextVerify(taskID),
		ReportBack: execTaskContextReportBack(taskID),
	}

	// Enforce the payload cap — never silently return a blob larger than
	// the cap without flagging it (mirrors the "never a silent truncation"
	// posture of execReadMaxBytes, but truncates instead of erroring; see
	// the const doc comment above for why).
	execTaskContextCapPayload(&result)

	return result, nil
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

	if !execRunInFlight(st.Data) {
		return st.Data, nil
	}

	// In-flight run: attach a resume bearings briefing on a shallow copy so
	// the original st.Data map is untouched (read never mutates state).
	_, redoTaskIDs := execResumeResetCandidates(st.Data)
	out := make(map[string]any, len(st.Data)+1)
	for k, v := range st.Data {
		out[k] = v
	}
	out["resumeBriefing"] = execBuildResumeBriefing(workDir, st.Data, redoTaskIDs, true)
	return out, nil
}

// ---------------------------------------------------------------------------
// Action: cleanup
// ---------------------------------------------------------------------------

// execActionCleanup stamps the branch's execution state terminal instead of
// deleting it (state.Write, never os.Remove on the state file) so the state
// — including issues[] — survives for later reads (e.g. /harden) until GC's
// TTL prunes it via prune-on-write / GC, and separately reaps the per-run
// working directory and ledger directory, which hold only working artifacts
// and are safe to delete.
//
// CRITICAL SAFETY: directory removal only happens when the state carries a
// non-empty startedAt, from which the runID is derived (same derivation as
// execReapRunDirectories' live-run detection). An empty/undeterminable runID
// must never reach os.RemoveAll: filepath.Join(root, DataDir, "execution", "")
// resolves to the execution directory ITSELF (a trailing empty Join segment
// is a no-op), and RemoveAll-ing that would wipe every run's data at once.
func execActionCleanup(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}

	st, findErr := state.Find(root, "execute", branch)
	if findErr != nil {
		return nil, &mcpserver.InfraError{Msg: "find state: " + findErr.Error(), Cause: findErr}
	}
	if st == nil {
		// Nothing to clean up — success.
		return map[string]any{}, nil
	}

	completedAt := now().UTC().Format(time.RFC3339)
	st.Data["runStatus"] = "completed"
	st.Data["runCompletedAt"] = completedAt

	out := map[string]any{
		"runStatus":        "completed",
		"runCompletedAt":   completedAt,
		"runDirCleaned":    false,
		"ledgerDirCleaned": false,
	}

	if startedAt, _ := st.Data["startedAt"].(string); startedAt != "" {
		runID := execNonDigitTRE.ReplaceAllString(startedAt, "")
		if runID != "" {
			runDir := filepath.Join(root, paths.DataDir, "execution", runID)
			if rmErr := os.RemoveAll(runDir); rmErr != nil {
				out["runDirError"] = rmErr.Error()
			} else {
				out["runDirCleaned"] = true
			}

			ldgDir := ledgerDir(root, runID)
			if rmErr := os.RemoveAll(ldgDir); rmErr != nil {
				out["ledgerDirError"] = rmErr.Error()
			} else {
				out["ledgerDirCleaned"] = true
			}
		}
	}

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	if summary := execIssueSummaryFull(st.Data); summary != nil {
		out["issueSummary"] = summary
	}

	return out, nil
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

	result := map[string]any{
		deleteBucketKey: []any{},
		keepBucketKey:   []any{},
		"ledger":        map[string]any{deleteBucketKey: []any{}, keepBucketKey: []any{}},
	}

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
		if dirName == "ledger" {
			// ledger/ is not a single per-run directory — it holds one
			// subdirectory per run. Never RemoveAll it as a unit here;
			// its children are swept individually in the dedicated loop
			// below so a stale ledger/ mtime can't wipe live runs' data.
			continue
		}
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

	// Second loop: sweep ledger/'s children individually. Each child is a
	// per-run directory named like the top-level run directories, so the
	// same TTL and liveRunIDs checks apply — but deletion is scoped to one
	// run's ledger subdirectory at a time, never the whole ledger/ tree.
	ledgerResult := map[string]any{deleteBucketKey: []any{}, keepBucketKey: []any{}}
	ledgerPath := filepath.Join(stateDir, "ledger")
	ledgerEntries, ledgerErr := os.ReadDir(ledgerPath)
	if ledgerErr == nil {
		for _, e := range ledgerEntries {
			if !e.IsDir() {
				continue
			}
			dirName := e.Name()
			dirPath := filepath.Join(ledgerPath, dirName)
			info, infoErr := e.Info()
			if infoErr != nil {
				continue
			}

			age := nowTime.Sub(info.ModTime())

			if age < ttlDur {
				ledgerResult[keepBucketKey] = append(ledgerResult[keepBucketKey].([]any),
					map[string]any{"dir": dirName, "reason": "ttl-fresh"})
				continue
			}

			if liveRunIDs[dirName] {
				ledgerResult[keepBucketKey] = append(ledgerResult[keepBucketKey].([]any),
					map[string]any{"dir": dirName, "reason": "state-file-exists"})
				continue
			}

			if dryRun {
				ledgerResult[deleteBucketKey] = append(ledgerResult[deleteBucketKey].([]any),
					map[string]any{"dir": dirName, "reason": "stale+state-file-gone"})
			} else {
				if err := os.RemoveAll(dirPath); err != nil {
					ledgerResult[keepBucketKey] = append(ledgerResult[keepBucketKey].([]any),
						map[string]any{"dir": dirName, "reason": "rm-failed"})
				} else {
					ledgerResult[deleteBucketKey] = append(ledgerResult[deleteBucketKey].([]any),
						map[string]any{"dir": dirName, "reason": "stale+state-file-gone"})
				}
			}
		}
	}
	result["ledger"] = ledgerResult

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
			waveNum := 0
			if in.Wave != nil {
				waveNum = *in.Wave
			}
			w := execFindOrCreateWave(st.Data, waveNum, now)

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

	if err := wave.UpdateProgress(root, in.RunID, in.TaskID, in.Phase, in.LastCompletedTask); err != nil {
		if errors.Is(err, wave.ErrBadRunID) || errors.Is(err, wave.ErrBadPhase) {
			return nil, &mcpserver.DomainError{Msg: err.Error(), Cause: err}
		}
		return nil, &mcpserver.InfraError{Msg: "update progress: " + err.Error(), Cause: err}
	}
	return map[string]any{}, nil
}

// ---------------------------------------------------------------------------
// Resume bearings briefing (shared by read and resume-reset)
// ---------------------------------------------------------------------------

// ExecResumeBriefing is attached under the "resumeBriefing" key on an
// execute_state read/resume-reset response when execRunInFlight reports a
// run that has started but not finished. It never causes read or
// resume-reset to fail: even a git cross-check mismatch is reported here as
// data (GitCrossCheck/GitMismatches), never as an error — see
// execGitCrossCheckWaves. A crashed or interrupted run is always described
// as Resumable: true, never surfaced as a failure.
type ExecResumeBriefing struct {
	pipeline.Narration
	Resumable      bool     `json:"resumable"`
	WavesDone      int      `json:"wavesDone"`
	WavesRemaining int      `json:"wavesRemaining"`
	GitCrossCheck  string   `json:"gitCrossCheck,omitempty"`
	GitMismatches  []string `json:"gitMismatches,omitempty"`
	WillRedo       []string `json:"willRedo"`
	WillSkip       []string `json:"willSkip"`
}

// execRunInFlight reports whether an execute state represents a run that
// has started work but not finished. It reads only fields already
// persisted by init/wave-start/wave-done/task-done — it never re-parses
// the plan file or re-derives the "true" total wave count, so it cannot
// hard-fail on a stale or moved plan path. True when either:
//   - some recorded wave has a status other than "completed" (in_progress,
//     partial, or failed), or
//   - every recorded wave is "completed" but plannedTaskIds is known and
//     not every planned task ID appears in context.completedTaskIds.
func execRunInFlight(data map[string]any) bool {
	waves, _ := data["waves"].([]any)
	if len(waves) == 0 {
		return false
	}

	for _, w := range waves {
		wm, ok := w.(map[string]any)
		if !ok {
			continue
		}
		if status, _ := wm["status"].(string); status != "completed" {
			return true
		}
	}

	planned := anyToStringSlice(data["plannedTaskIds"])
	if len(planned) == 0 {
		return false
	}
	completedSet := map[string]bool{}
	if ctx, ok := data["context"].(map[string]any); ok {
		for _, id := range anyToStringSlice(ctx["completedTaskIds"]) {
			completedSet[execNormalizeTaskID(id)] = true
		}
	}
	for _, id := range planned {
		if !completedSet[execNormalizeTaskID(id)] {
			return true
		}
	}
	return false
}

// execResumeResetCandidates identifies which in-progress waves resume-reset
// would clear and which task IDs they would clear, without mutating state.
// execActionResumeReset calls this for its actual mutation so read's
// dry-run willRedo preview and resume-reset's real effect always agree.
func execResumeResetCandidates(data map[string]any) (waveNumbers []int, taskIDs []string) {
	waves, _ := data["waves"].([]any)
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
					taskIDs = append(taskIDs, id)
				}
			}
		}
		waveNumbers = append(waveNumbers, execToInt(wm["number"]))
	}
	return waveNumbers, taskIDs
}

// execComputeResumeSets splits a resume into willRedo (task IDs a resumed
// run will re-execute — the ones execResumeResetCandidates identified) and
// willSkip (task IDs already recorded in context.completedTaskIds that are
// not part of a reset wave, so a resumed run will not touch them again).
// A task recorded as completed inside a wave that is about to be reset is
// classified as willRedo, not willSkip: resume-reset clears a whole
// in-progress wave's task list, so that task will run again. Both return
// values are non-nil (possibly empty) so they serialize as JSON "[]", not
// "null".
func execComputeResumeSets(data map[string]any, redoTaskIDs []string) (willRedo, willSkip []string) {
	willRedo = append([]string{}, redoTaskIDs...)
	redoSet := map[string]bool{}
	for _, id := range willRedo {
		redoSet[execNormalizeTaskID(id)] = true
	}

	willSkip = []string{}
	if ctx, ok := data["context"].(map[string]any); ok {
		for _, id := range anyToStringSlice(ctx["completedTaskIds"]) {
			if !redoSet[execNormalizeTaskID(id)] {
				willSkip = append(willSkip, id)
			}
		}
	}

	sort.Strings(willRedo)
	sort.Strings(willSkip)
	return willRedo, willSkip
}

// execGitCrossCheckResult is the outcome of comparing every wave's recorded
// committedSha against actual git history. Status is one of "none" (no
// wave has a committedSha yet), "confirmed" (every recorded sha is an
// ancestor of HEAD), or "mismatch" (at least one is not, or could not be
// verified — both are collected in Mismatches). This never carries a Go
// error: a mismatch is data for the caller to act on, not a tool failure.
type execGitCrossCheckResult struct {
	Status     string
	Mismatches []string
}

// execGitCrossCheckWaves reuses execIsAncestor (the existing
// `git merge-base --is-ancestor` helper) rather than adding new gitx
// surface or shelling out again. A git-level failure to verify a sha (bad
// sha, not a repo, etc.) is recorded as a mismatch entry, same as a
// confirmed non-ancestor — either way this function returns a result, never
// an error, so a git cross-check problem can never hard-fail read or
// resume-reset.
func execGitCrossCheckWaves(workDir string, data map[string]any) execGitCrossCheckResult {
	waves, _ := data["waves"].([]any)
	var mismatches []string
	checked := 0

	for _, w := range waves {
		wm, ok := w.(map[string]any)
		if !ok {
			continue
		}
		sha, _ := wm["committedSha"].(string)
		if sha == "" {
			continue
		}
		num := execToInt(wm["number"])

		isAncestor, err := execIsAncestor(workDir, sha)
		if err != nil {
			mismatches = append(mismatches, fmt.Sprintf("wave %d: could not verify commit %s against git history (%s)", num, shortSHA(sha), err.Error()))
			continue
		}
		checked++
		if !isAncestor {
			mismatches = append(mismatches, fmt.Sprintf("wave %d: recorded commit %s not found in current git history", num, shortSHA(sha)))
		}
	}

	if len(mismatches) > 0 {
		return execGitCrossCheckResult{Status: "mismatch", Mismatches: mismatches}
	}
	if checked > 0 {
		return execGitCrossCheckResult{Status: "confirmed"}
	}
	return execGitCrossCheckResult{Status: "none"}
}

// execLastRecordedWave returns the highest-numbered wave entry in
// data["waves"], or nil if there are none. Mirrors buildExecuteRecovery's
// (hooks/pre_compact_save.go) "last touched wave" precedent.
func execLastRecordedWave(data map[string]any) map[string]any {
	waves, _ := data["waves"].([]any)
	var last map[string]any
	lastNum := -1
	for _, w := range waves {
		wm, ok := w.(map[string]any)
		if !ok {
			continue
		}
		if n := execToInt(wm["number"]); n >= lastNum {
			lastNum = n
			last = wm
		}
	}
	return last
}

// execResumeNextAction computes the ResumeBriefing's Next field from the
// last recorded wave's status. forRead distinguishes the read action
// (nothing mutated yet, so a reset must be instructed first when there is
// something to reset) from resume-reset (the reset already happened, so
// wave-start is the direct next step).
func execResumeNextAction(data map[string]any, forRead bool) *pipeline.NextAction {
	last := execLastRecordedWave(data)
	if last == nil {
		return nil
	}
	num := execToInt(last["number"])
	status, _ := last["status"].(string)

	switch status {
	case "in_progress":
		tasks, _ := last["tasks"].([]any)
		_, hasCompletedAt := last["completedAt"]
		hasCandidate := len(tasks) > 0 || hasCompletedAt
		if hasCandidate && forRead {
			return &pipeline.NextAction{
				ID:          "resume-reset",
				Instruction: fmt.Sprintf("Call resume-reset, then wave-start for wave %d.", num),
			}
		}
		return &pipeline.NextAction{
			ID:          fmt.Sprintf("wave-%d", num),
			Instruction: fmt.Sprintf("Call wave-start for wave %d.", num),
		}
	case "failed":
		return &pipeline.NextAction{
			ID:          fmt.Sprintf("wave-%d", num),
			Instruction: fmt.Sprintf("Wave %d failed — investigate the recorded issue, then call resume-reset and wave-start for wave %d to retry.", num, num),
		}
	case "partial":
		return &pipeline.NextAction{
			ID:          fmt.Sprintf("wave-%d", num+1),
			Instruction: fmt.Sprintf("Wave %d completed with partial failures — review issues, then call wave-commit and wave-start for wave %d.", num, num+1),
		}
	default: // "completed"
		return &pipeline.NextAction{
			ID:          fmt.Sprintf("wave-%d", num+1),
			Instruction: fmt.Sprintf("Call wave-commit, then wave-start for wave %d.", num+1),
		}
	}
}

// execBuildResumeBriefing composes the ResumeBriefing shared by read and
// resume-reset. redoTaskIDs is supplied by the caller: read passes a
// dry-run result from execResumeResetCandidates (nothing mutated yet),
// resume-reset passes the task IDs it actually just cleared — guaranteeing
// both actions describe identical resume semantics (the "mirror"
// requirement: resume-reset's real effect and read's preview must agree).
func execBuildResumeBriefing(workDir string, data map[string]any, redoTaskIDs []string, forRead bool) *ExecResumeBriefing {
	waves, _ := data["waves"].([]any)
	wavesDone, wavesRemaining := 0, 0
	for _, w := range waves {
		wm, ok := w.(map[string]any)
		if !ok {
			continue
		}
		if status, _ := wm["status"].(string); status == "completed" {
			wavesDone++
		} else {
			wavesRemaining++
		}
	}

	willRedo, willSkip := execComputeResumeSets(data, redoTaskIDs)
	git := execGitCrossCheckWaves(workDir, data)
	next := execResumeNextAction(data, forRead)

	b := &ExecResumeBriefing{
		Resumable:      true,
		WavesDone:      wavesDone,
		WavesRemaining: wavesRemaining,
		WillRedo:       willRedo,
		WillSkip:       willSkip,
	}
	if git.Status != "none" {
		b.GitCrossCheck = git.Status
	}
	if len(git.Mismatches) > 0 {
		b.GitMismatches = git.Mismatches
	}

	lines := []string{fmt.Sprintf("Run resumable: %d wave(s) done, %d recorded wave(s) still need attention.", wavesDone, wavesRemaining)}
	switch git.Status {
	case "confirmed":
		lines = append(lines, "Git: all recorded commits confirmed in history.")
	case "mismatch":
		lines = append(lines, fmt.Sprintf("Git: %d commit mismatch(es) found — see gitMismatches.", len(git.Mismatches)))
	}
	if len(willRedo) > 0 {
		lines = append(lines, "Will redo: "+strings.Join(willRedo, ", "))
	}
	if len(willSkip) > 0 {
		lines = append(lines, "Will skip (already completed): "+strings.Join(willSkip, ", "))
	}

	b.Summary = lines[0]
	b.Display = "**Resume briefing**\n- " + strings.Join(lines, "\n- ")
	b.Next = next
	return b
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
		resetWaves, clearedTaskIds = execResumeResetCandidates(st.Data)

		if len(resetWaves) > 0 {
			waveSet := map[int]bool{}
			for _, n := range resetWaves {
				waveSet[n] = true
			}
			waves, _ := st.Data["waves"].([]any)
			for _, w := range waves {
				wm, ok := w.(map[string]any)
				if !ok {
					continue
				}
				if waveSet[execToInt(wm["number"])] {
					wm["tasks"] = []any{}
					delete(wm, "completedAt")
				}
			}
			if err := state.Write(st); err != nil {
				return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
			}
		}
	}

	out := map[string]any{
		"resetWaves":     resetWaves,
		"clearedTaskIds": clearedTaskIds,
	}

	if st != nil && execRunInFlight(st.Data) {
		out["resumeBriefing"] = execBuildResumeBriefing(workDir, st.Data, clearedTaskIds, false)
	}

	return out, nil
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
			continue // skip corrupt/unreadable ledger entries gracefully
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
