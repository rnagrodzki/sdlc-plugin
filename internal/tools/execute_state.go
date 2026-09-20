package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
	"github.com/rnagrodzki/sdlc-plugin/internal/shipmeta"
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
	Action              string         `json:"action" jsonschema:"enum=wave-compute,enum=init,enum=wave-start,enum=wave-done,enum=wave-fail,enum=wave-committed,enum=wave-commit,enum=task-done,enum=task-fail,enum=task-context,enum=context,enum=read,enum=cleanup,enum=gc,enum=summarize-prior-wave-context,enum=wave-split,enum=verify-completeness,enum=wave-progress,enum=wave-await,enum=task-redispatch,enum=resume-reset,enum=ledger_checkin,enum=ledger_checkout,enum=ledger_status,enum=ledger_cleanup,enum=log-cli,enum=drift-log,enum=issue-draft,enum=decide,enum=report" jsonschema_description:"Selects the operation. Each action reads only the subset of fields listed in the tool description; unlisted fields are ignored."`
	Branch              string         `json:"branch,omitempty" jsonschema_description:"Git branch the execution state belongs to. Most actions accept it to scope the state file; falls back to the current branch when omitted."`
	Quality             string         `json:"quality,omitempty" jsonschema_description:"Quality level to stamp on a newly initialized run (init only). Required — no config fallback exists for this field."`
	TotalTasks          int            `json:"totalTasks,omitempty" jsonschema_description:"Total planned task count for a newly initialized run (init only)."`
	WaveTimeoutSeconds  int            `json:"waveTimeoutSeconds,omitempty" jsonschema_description:"init only: this run's wave wall-clock deadline in seconds (the invoking CLI's --wave-timeout). Recorded on init and later read back by wave-await to size its reclaim/timeout window. When omitted, falls back to a ship-state cross-read of flags.executeWaveTimeout, then internal/shipmeta.ShipBuiltInDefaults.ExecuteWaveTimeout (1800s)."`
	WaveIntervalSeconds int            `json:"waveIntervalSeconds,omitempty" jsonschema_description:"init only: this run's heartbeat liveness cadence in seconds (the invoking CLI's --wave-interval). Recorded on init and later read back by wave-await to size its heartbeat/reclaim-grace window. When omitted, falls back to a ship-state cross-read of flags.executeWaveInterval, then internal/shipmeta.ShipBuiltInDefaults.ExecuteWaveInterval (60s)."`
	PlannedTaskIds      []string       `json:"plannedTaskIds,omitempty" jsonschema_description:"IDs of every task planned for this run (init only), used later to detect run completeness."`
	PlanPath            string         `json:"planPath,omitempty" jsonschema_description:"Path to the plan file to parse into a wave schedule (wave-compute), or to record on a newly initialized run (init)."`
	PlanHash            string         `json:"planHash,omitempty" jsonschema_description:"Hash of the plan file content, recorded on a newly initialized run (init only) to detect later plan drift."`
	ExtraDepsJSON       string         `json:"extraDepsJson,omitempty" jsonschema_description:"wave-compute only: JSON array of {task, dependsOn, reason} objects merged with each task's explicit \"Depends on\" field before the wave schedule is computed."`
	Wave                *int           `json:"wave,omitempty" jsonschema_description:"Wave number the action applies to (wave-start, wave-done, wave-fail, wave-committed, wave-commit, task-done, task-fail, wave-split, wave-await; also task-redispatch, optional, to scope the row search to one wave instead of scanning every wave)."`
	TasksJSON           string         `json:"tasksJson,omitempty" jsonschema_description:"wave-start: JSON array of task objects. Each entry: {id: string, name: string, description: string, complexity: string (optional — Trivial|Standard|Complex), contract: string (optional), acceptanceCriteria: string[] (optional — array of strings), files: string[] (optional), workerName: string (optional — caller-supplied dispatch identity, never invented; falls back to a generated template when omitted), batchId: string (optional — shared by every task in one batch dispatch; omit for a solo task), batchIndex: number (optional — this task's 0-based position within its batch)}. Entries missing required string fields (id, name, description) are dropped with a warning; if zero valid entries remain after filtering, the call fails with an error. Seeds server-owned dispatch state (dispatchedAt, workerName, batchId/batchIndex, attempt:1) for every valid task."`
	RunID               string         `json:"runId,omitempty" jsonschema_description:"Execution run identifier. Required by task-context, wave-await, ledger_checkin, ledger_checkout, and ledger_status; optional elsewhere (e.g. wave-start for fact sheets, task-redispatch) where it falls back to the value derived from the state's startedAt/wave."`
	WorkerID            string         `json:"workerId,omitempty" jsonschema_description:"Identifier of the per-task worker registering or clearing its ledger entry (ledger_checkin, ledger_checkout)."`
	Decisions           string         `json:"decisions,omitempty" jsonschema_description:"wave-done only: JSON array of decisions made while completing the wave, encoded as a string; surfaced in later summaries. Plain prose is rejected. Example: \"[\\\"Chose sqlite over postgres for the local cache\\\"]\"."`
	Status              string         `json:"status,omitempty" jsonschema_description:"Outcome status to record: for wave-done/wave-fail, the wave's terminal status; for task-done, \"DONE_WITH_CONCERNS\" records a warning issue alongside the completion."`
	TimedOut            bool           `json:"timedOut,omitempty" jsonschema_description:"wave-fail only: true when the wave failed because it timed out, rather than erroring outright."`
	SHA                 string         `json:"sha,omitempty" jsonschema_description:"wave-committed only: the git commit SHA to record for the completed wave."`
	TaskID              string         `json:"taskId,omitempty" jsonschema_description:"Task identifier the action applies to (task-done, task-fail, task-context, wave-progress writes; required by task-redispatch)."`
	TaskName            string         `json:"taskName,omitempty" jsonschema_description:"task-done only: human-readable name of the completed task, surfaced in the running-tally narration."`
	Complexity          string         `json:"complexity,omitempty" jsonschema_description:"task-done only: complexity rating recorded for the completed task."`
	Risk                string         `json:"risk,omitempty" jsonschema_description:"task-done only: risk rating recorded for the completed task."`
	FilesChanged        string         `json:"filesChanged,omitempty" jsonschema_description:"task-done only: JSON array of file paths the task changed, encoded as a string. Example: \"[\\\"src/auth/jwt.ts\\\",\\\"src/auth/oauth.ts\\\"]\"."`
	FilesAdded          string         `json:"filesAdded,omitempty" jsonschema_description:"task-done only: JSON array of file paths the task created, encoded as a string. List only newly created files; each one belongs in filesChanged as well. Example: \"[\\\"src/auth/oauth.ts\\\"]\"."`
	VerifyToken         string         `json:"verifyToken,omitempty" jsonschema_description:"task-done only: verification evidence for the completed task, encoded as a JSON string or a JSON array of strings. A bare token is rejected as invalid JSON. Example: \"[\\\"go test ./... ok\\\"]\" or \"\\\"go test ./... ok\\\"\"."`
	SkippedDep          bool           `json:"skippedDependency,omitempty" jsonschema_description:"task-fail only: true when the failure is a skipped dependency rather than a real failure; only a non-skipped failure updates the wave's failedTask."`
	ErrorText           string         `json:"error,omitempty" jsonschema_description:"Failure or concern detail text: the failure cause for wave-fail (recorded as an issue and in failedWave), the concern detail for task-done's DONE_WITH_CONCERNS status, or the failure detail for task-fail."`
	Data                string         `json:"data,omitempty" jsonschema_description:"context action only: JSON object of shared context keys to write, encoded as a string (allowed keys: planSummary, completedTaskIds, filesAdded, filesModified, interfacesCreated, decisionsFromPriorWaves). Example: \"{\\\"planSummary\\\":\\\"Add OAuth login\\\"}\"."`
	TTLDays             *int           `json:"ttlDays,omitempty" sdlcconfig:"state.gc.ttlDays" jsonschema_description:"gc only: age threshold in days beyond which stale state files are garbage-collected. Optional. Defaults to config state.gc.ttlDays. Pass only to override."`
	DryRun              bool           `json:"dryRun,omitempty" jsonschema_description:"gc only: when true, reports what would be garbage-collected without deleting anything."`
	MaxFiles            int            `json:"maxFiles,omitempty" sdlcconfig:"execute.priorWaveContextCaps.maxFiles" jsonschema_description:"Cap on the number of files summarized in prior-wave context (context, summarize-prior-wave-context). Optional. Defaults to config execute.priorWaveContextCaps.maxFiles. Pass only to override."`
	MaxDecisions        int            `json:"maxDecisions,omitempty" sdlcconfig:"execute.priorWaveContextCaps.maxDecisions" jsonschema_description:"Cap on the number of decisions summarized in prior-wave context (context, summarize-prior-wave-context). Optional. Defaults to config execute.priorWaveContextCaps.maxDecisions. Pass only to override."`
	MaxInterfaces       int            `json:"maxInterfaces,omitempty" sdlcconfig:"execute.priorWaveContextCaps.maxInterfaces" jsonschema_description:"Cap on the number of interfaces summarized in prior-wave context (context, summarize-prior-wave-context). Optional. Defaults to config execute.priorWaveContextCaps.maxInterfaces. Pass only to override."`
	MaxTaskIds          int            `json:"maxTaskIds,omitempty" sdlcconfig:"execute.priorWaveContextCaps.maxTaskIds" jsonschema_description:"Cap on the number of task IDs summarized in prior-wave context (context, summarize-prior-wave-context). Optional. Defaults to config execute.priorWaveContextCaps.maxTaskIds. Pass only to override."`
	Dispatched          string         `json:"dispatched,omitempty" jsonschema_description:"wave-split only: JSON array of task ID strings already dispatched, encoded as a string; used to compute which remaining tasks form the new wave. Required by wave-split. Example: \"[\\\"1\\\",\\\"2\\\"]\"."`
	MissingIds          string         `json:"missingIds,omitempty" jsonschema_description:"wave-split only: JSON array of task ID strings missing from the current wave, encoded as a string; these are folded into the new split wave. Example: \"[\\\"3\\\"]\"."`
	SplitDepth          int            `json:"splitDepth,omitempty" jsonschema_description:"wave-split only: current recursive split depth, used together with maxSplitDepth to bound repeated splitting."`
	MaxSplitDepth       int            `json:"maxSplitDepth,omitempty" jsonschema_description:"wave-split only: maximum recursive split depth allowed before wave-split refuses to split further."`
	StateFile           string         `json:"stateFile,omitempty" jsonschema_description:"Overrides the execution state file path to read/write, instead of the one derived from branch (wave-split, verify-completeness, resume-reset). wave-await also reads this to persist its own resume-state (iteration counter) across bounded-poll calls, keyed by runId+wave."`
	Phase               string         `json:"phase,omitempty" jsonschema_description:"wave-progress write only: the phase name to stamp on the task's heartbeat entry."`
	ReadProgress        bool           `json:"readProgress,omitempty" jsonschema_description:"wave-progress only: true to read the current per-task progress instead of writing a new heartbeat entry."`
	SessionID           string         `json:"sessionId,omitempty" jsonschema_description:"init only: Claude Code session ID stamped into the newly initialized execution state."`
	TimeoutSeconds      int            `json:"timeoutSeconds,omitempty" jsonschema_description:"ledger_status only: age threshold in seconds beyond which a checked-in worker with no checkout is reported as timed out."`
	ExpectedWorkers     []string       `json:"expectedWorkers,omitempty" jsonschema_description:"ledger_status only: worker IDs expected to be registered for this run; any not found on disk are returned in missingWorkers."`
	Payload             map[string]any `json:"payload,omitempty" jsonschema_description:"Reserved for future use; not currently read by any action."`
	StepID              string         `json:"stepId,omitempty" jsonschema_description:"ledger_checkin only: identifier of the pipeline step the worker is registering activity for."`
	Findings            string         `json:"findings,omitempty" jsonschema_description:"ledger_checkout only: free-text findings payload to persist alongside this worker's checkout record, returned later by ledger_status. Not schema-validated, but review workers conventionally pass a JSON array of objects shaped {severity, file, line, rationale} (a markdown block is also accepted). Capped at 64 KiB; larger payloads should be persisted to a file under .sdlc-v2/ and referenced by path instead."`
	Detail              string         `json:"detail,omitempty" jsonschema_description:"Narration verbosity for wave-start/wave-done/wave-fail/wave-commit: \"concise\" or \"full\"."`
	LastCompletedTask   string         `json:"lastCompletedTask,omitempty" jsonschema_description:"wave-progress write only: ID of the most recently completed task, recorded in the heartbeat entry."`
	AcceptanceDone      []int          `json:"acceptanceDone,omitempty" jsonschema_description:"wave-progress write only: 0-based indices, into the task's fact-sheet acceptance criteria, that the worker has completed so far (e.g. [0,2,3]). Replaces the previously recorded list; omit to leave it unchanged."`
	FilesTouched        []string       `json:"filesTouched,omitempty" jsonschema_description:"wave-progress write only: files the worker has modified so far. Replaces the previously recorded list; omit to leave it unchanged."`
	Blocker             string         `json:"blocker,omitempty" jsonschema_description:"wave-progress write only: free-text reason the worker is currently blocked. Omit to leave the previously recorded value unchanged."`
	Message             string         `json:"message,omitempty" jsonschema_description:"wave-commit only: commit message to use for 'git commit -m' when staging and committing the wave's changes."`
	CLICommand          string         `json:"cliCommand,omitempty" jsonschema_description:"log-cli only: the Bash command that was executed."`
	CLIExitCode         int            `json:"cliExitCode,omitempty" jsonschema_description:"log-cli only: the exit code of the command."`
	CLIOutput           string         `json:"cliOutput,omitempty" jsonschema_description:"log-cli only: first ~500 characters of command output."`
	DriftSeverity       string         `json:"driftSeverity,omitempty" jsonschema:"enum=error,enum=warning,enum=info" jsonschema_description:"drift-log only: severity of the drift issue — one of error, warning, or info."`
	DriftSummary        string         `json:"driftSummary,omitempty" jsonschema_description:"drift-log only: one-line summary of the drift issue."`
	DriftDetail         string         `json:"driftDetail,omitempty" jsonschema_description:"drift-log only: optional longer description of the drift issue."`
	IssueDraftTitle     string         `json:"issueDraftTitle,omitempty" jsonschema_description:"issue-draft only: GH issue title (required)."`
	IssueDraftBody      string         `json:"issueDraftBody,omitempty" jsonschema_description:"issue-draft only: GH issue body markdown (required)."`
	IssueDraftLabels    []string       `json:"issueDraftLabels,omitempty" jsonschema_description:"issue-draft only: labels to apply (optional)."`
	DecideType          string         `json:"decideType,omitempty" jsonschema:"enum=guardrail" jsonschema_description:"decide only: decision category. Currently: guardrail."`
	DecideID            string         `json:"decideId,omitempty" jsonschema_description:"decide only: identifier of the item decided on (e.g. a guardrail slug)."`
	DecideDecision      string         `json:"decideDecision,omitempty" jsonschema:"enum=override,enum=harden,enum=cancel,enum=fix" jsonschema_description:"decide only: choice made — override, harden, cancel, or fix."`
	DecideReason        string         `json:"decideReason,omitempty" jsonschema_description:"decide only: optional free-text reason why this choice was made."`
	Write               bool           `json:"write,omitempty" jsonschema_description:"report only: persist the report under <main worktree>/.sdlc-v2/reports/ instead of only returning it. Default false (read-only)."`
	Format              string         `json:"format,omitempty" jsonschema:"enum=json,enum=md" jsonschema_description:"report only: overrides the format normally sourced from config.automation.report.format (\"json\" or \"md\"). Required alongside write=true so the caller's second (body-carrying) call and the tool agree on which file extension to persist."`
	Body                string         `json:"body,omitempty" jsonschema_description:"report only: rendered markdown body to persist. Required when write=true and format=md (the caller renders markdown itself and hands the tool the exact text to write); ignored for format=json, where the tool recomputes and persists the report struct itself."`
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
	Warnings        []string `json:"warnings,omitempty"`
}

// DriftLogOut is the output for a plan-drift check. wave-start returns it
// (in place of ExecWaveNarrationOut) when the plan file's sha256 no longer
// matches the planHash recorded at init, halting the wave before it starts.
// The struct is shared with the drift-log action (KD-5 follow-up). Both
// code paths populate Logged and DriftCount so callers always get a
// consistent view of drift state regardless of which path produced the
// output. When Halt is true, Next tells the caller what to do.
type DriftLogOut struct {
	Logged     bool           `json:"logged"`
	Halt       bool           `json:"halt"`
	Reason     string         `json:"reason,omitempty"`
	DriftCount map[string]int `json:"driftCount"`
	Threshold  int            `json:"threshold"`
	Next       string         `json:"next,omitempty"`
}

// IssueDraftOut is the output for the issue-draft action.
type IssueDraftOut struct {
	Added       bool   `json:"added"`
	TotalDrafts int    `json:"totalDrafts"`
	Next        string `json:"next,omitempty"`
}

// ExecDecideOut is the output for the decide action.
type ExecDecideOut struct {
	OK     bool   `json:"ok"`
	Action string `json:"action"`
	Next   string `json:"next"`
}

// ExecutionReportOut is the read-only end-of-run report returned by the
// report action (KD-11): everything ship step 10d needs to render (or
// forward as JSON) a full account of the run. Format tells the caller how
// to render it — "json" (write the struct verbatim) or "md" (render as
// markdown) — sourced from config.Automation.Report.Format, defaulting to
// "md". RunID is derived the same way wave-start's runId is (from the
// state file's startedAt — see execDeriveRunID) so the caller never has to
// invent a filename on its own.
type ExecutionReportOut struct {
	// Metadata
	Branch    string `json:"branch"`
	RunID     string `json:"runId,omitempty"`
	PlanPath  string `json:"planPath,omitempty"`
	StartedAt string `json:"startedAt"`
	Duration  string `json:"duration"`
	Format    string `json:"format"`

	// Waves + Tasks
	Waves []WaveReport `json:"waves"`

	// Aggregates
	TotalTasks     int `json:"totalTasks"`
	CompletedTasks int `json:"completedTasks"`
	FailedTasks    int `json:"failedTasks"`
	SkippedTasks   int `json:"skippedTasks"`

	// Issues by category — see execReportBucketIssues for the exact
	// partitioning rule.
	Drifts   []StateIssue `json:"drifts"`
	Errors   []StateIssue `json:"errors"`
	Warnings []StateIssue `json:"warnings"`
	Concerns []StateIssue `json:"concerns"`

	// Follow-ups
	PendingIssueDrafts []any `json:"pendingIssueDrafts,omitempty"`
	DeferredFindings   []any `json:"deferredFindings,omitempty"`

	// Decisions
	Decisions []string `json:"decisions,omitempty"`

	// CLI evidence + step timings — both best-effort cross-reads from ship
	// state (see execActionReport); normalized to empty (never nil) slices.
	CLIEvidence []CLIEvidenceEntry `json:"cliEvidence"`
	StepTimings []StepTiming       `json:"stepTimings"`

	// GuardrailHits lists guardrail IDs decided during this run (KD-2),
	// extracted from this state file's own data["guardrailDecisions"];
	// normalized to empty (never nil). LinkedLearnings counts learnings log
	// lines tagged with this run's ID (KD-3).
	GuardrailHits   []string `json:"guardrailHits"`
	LinkedLearnings int      `json:"linkedLearnings"`

	// Next step guidance (empty string is valid "no next step", never
	// omitted — callers must be able to tell that apart from field absent).
	Next string `json:"next"`

	// Path and Written are populated only when the caller passed write:true
	// (see execActionReport's write branch). Path is the absolute path the
	// report was persisted to under <main worktree>/.sdlc-v2/reports/;
	// Written is false on every read-only call (the default).
	Path    string `json:"path,omitempty"`
	Written bool   `json:"written"`
}

// StepTiming is one ship-pipeline step's timing entry in
// ExecutionReportOut.StepTimings, derived from ship state's data["steps"].
type StepTiming struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	StartedAt string `json:"startedAt,omitempty"`
	Duration  string `json:"duration,omitempty"`
	HumanWait bool   `json:"humanWait,omitempty"`
}

// WaveReport is one wave's entry in ExecutionReportOut.Waves.
type WaveReport struct {
	Number       int          `json:"number"`
	Status       string       `json:"status"`
	StartedAt    string       `json:"startedAt,omitempty"`
	CompletedAt  string       `json:"completedAt,omitempty"`
	Duration     string       `json:"duration,omitempty"`
	Tasks        []TaskReport `json:"tasks"`
	CommittedSHA string       `json:"committedSha,omitempty"`
}

// TaskReport is one task's entry in WaveReport.Tasks. Duration is left
// empty (omitted) — task-done records only completedAt, not a per-task
// startedAt, so no duration is derivable from the data execute_state
// already stores.
type TaskReport struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Complexity string `json:"complexity,omitempty"`
	Risk       string `json:"risk,omitempty"`
	Duration   string `json:"duration,omitempty"`
	Files      string `json:"filesChanged,omitempty"`
}

// ReportSkippedOut is returned by the report action when
// config.Automation.Report.Enabled is false. Written is always false here
// (the zero value) — reporting being disabled means nothing is ever
// persisted, whether or not the caller asked for write:true.
type ReportSkippedOut struct {
	Skipped bool `json:"skipped"`
	Written bool `json:"written"`
	// Next is never omitted (empty string is valid "no next step", not
	// field absent) — same repo-wide Next-field contract as ExecutionReportOut.
	Next string `json:"next"`
}

// ExecTaskNarrationOut is the narrated output for task-level execute_state
// actions (task-done, task-fail). Warnings mirrors ExecWaveNarrationOut's
// field of the same name: task-done still succeeds when populated — these
// are phantom-success heuristics (duplicate verifyToken across sibling
// tasks, or a completion with no filesChanged), not hard failures.
type ExecTaskNarrationOut struct {
	pipeline.Narration
	Warnings []string `json:"warnings,omitempty"`
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
	TaskID   string        `json:"taskId"`
	RunID    string        `json:"runId"`
	Wave     int           `json:"wave"`
	Quality  string        `json:"quality,omitempty"`
	Siblings []TaskSibling `json:"siblings,omitempty"`
	// SiblingsUnknown is true when this wave has no "planned" task list to
	// derive Siblings from (wave-start was never called with tasksJson for
	// this wave), so an empty Siblings here means "sibling data was never
	// captured", not "this task has no siblings". Lets callers distinguish
	// the two cases instead of silently treating both as "alone in wave".
	SiblingsUnknown bool   `json:"siblingsUnknown,omitempty"`
	FactSheet       string `json:"factSheet"`
	// ResumeFrom carries the task's last recorded failure's harvested
	// partial-work claim (set by task-fail when the worker had reported one
	// via wave-progress before being reclaimed). The same data is also
	// rendered into FactSheet's "Resume from a reclaimed attempt" section
	// near the top, ahead of the tail-trim in execTaskContextCapPayload.
	// Omitted entirely when the task's last failure recorded none.
	ResumeFrom     *ResumeFrom     `json:"resumeFrom,omitempty"`
	PriorWaves     string          `json:"priorWaves"`
	Verify         string          `json:"verify"`
	ReportBack     string          `json:"reportBack"`
	ExecutionRules *ExecutionRules `json:"executionRules,omitempty"`
	Truncated      bool            `json:"truncated,omitempty"`
}

// TaskSibling describes another task in the same wave, giving the worker
// awareness of its peers without requiring per-task file reads.
type TaskSibling struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Files []string `json:"files,omitempty"`
}

// ExecutionRules is the machine-readable equivalent of the prose Verify and
// ReportBack fields. Workers can consume either form; the structured version
// enables tooling that needs to parse scope or phases programmatically.
type ExecutionRules struct {
	FileScope       []string `json:"fileScope,omitempty"`
	VerifyMethod    string   `json:"verifyMethod"`
	HeartbeatPhases []string `json:"heartbeatPhases"`
	ReportFormat    string   `json:"reportFormat"`
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
- init: Create execution state. Runs the same config auto-migration gate as ship_prepare first (migrates and backs up an outdated config, or fails with a /setup pointer if none exists); result may include a "migration" report. Returns {filePath, pipelineAuto (true when this branch's ship state has flags.auto=true — forwarded so the execute SKILL.md high-risk gate can skip a second approval), warnings? (e.g. this branch's ship state exists but is unreadable), migration?}. Requires branch, quality. Optional: totalTasks, plannedTaskIds, planPath, planHash.
- wave-start: Begin a wave. Returns narration (summary, display with task list + ETA, next). Requires wave. Optional: branch, tasksJson, runId (for fact sheets), detail ("concise"|"full"). If the run recorded a planHash at init, the plan file's current sha256 is compared against it first; a mismatch returns {halt:true, reason:"plan hash mismatch"} instead of narration and does not start the wave. An unreadable/missing plan file does not halt — it proceeds with a warning in the response's "warnings" field. Seeds server-owned dispatch state (dispatchedAt, workerName, batchId/batchIndex, attempt:1) for every valid tasksJson entry that doesn't already have one — a task that already has server state (wave-start called again on resume) is left untouched. Seeding failure is non-fatal and appends to "warnings".
- wave-done: Complete a wave. Returns narration (summary, display with outcomes, timing, next wave preview + ETA). Records wave duration to TimingsStore. Requires wave. Optional: branch, decisions, status, detail ("concise"|"full").
- wave-fail: Fail a wave. Returns narration (summary, display with failure cause). Requires wave. Optional: branch, timedOut, error (failure cause, recorded as an issue and in failedWave), status, detail ("concise"|"full").
- wave-committed: Record a commit SHA for a completed wave. Requires wave. Optional: branch, sha.
- wave-commit: Stage and commit a completed wave's changes (git add -A + git commit -m message) and record the resulting sha on the wave, mirroring wave-committed's SHA-recording. Requires wave, message. Optional: branch, detail ("concise"|"full"). The wave must already be "completed" (call wave-done first). Empty diff: succeeds without committing ({committed:false, reason:"nothing to commit"}). When config execute.commitWaves is false, does not commit and instead returns an instruction to commit manually and call wave-committed. Idempotent on resume: an already-recorded committedSha that is still an ancestor of HEAD is reported ({idempotent:true}) rather than committed again.
- task-done: Record task completion. Returns narration (summary with running tally, warnings[] when phantom-success heuristics fire). Requires wave, taskId. Optional: branch, taskName, complexity, risk, filesChanged, filesAdded, verifyToken, status ("DONE_WITH_CONCERNS" records a warning issue), error (concern detail for DONE_WITH_CONCERNS).
- task-fail: Record task failure. Returns narration (summary with running tally). Requires wave, taskId. Optional: branch, error, skippedDependency (records an issue; only a non-skipped failure updates failedTask). Idempotent: a repeat call for a task already recorded as failed/skipped at the same attempt is a no-op — it does not duplicate the issue log or move completedAt forward.
- task-redispatch: Reopen a failed task for another attempt. Requires taskId. Optional: branch, runId, wave (searches every wave for the task's closed row when omitted). Re-opens the task's wave-manifest row to "in_progress", then deletes and re-seeds the task's server state with a fresh dispatchedAt and attempt+1 — contextFetchedAt, reclaimRequestedAt, and batchId all come back empty, since a redispatch is always solo even if the failed attempt was batched. Refuses with a DomainError (Suggestion names user escalation) at the 2-retry ceiling (attempt already at 3) instead of seeding a 4th attempt.
- task-context: Return everything a dispatched per-task worker needs in one call — fact-sheet content (embeds the plan-task's Contract/Acceptance Criteria/Files), a live prior-wave summary, verify guidance, and report-back instructions. Requires taskId. Optional: branch, runId (falls back the same way wave-start does, via startedAt/wave). The serialized payload is capped at 1 MiB; oversize content (fact sheet first, then prior-wave summary if still over cap) is truncated with truncated:true rather than erroring. Unknown taskId fails with an actionable error listing the valid IDs for that run. Stamps the task's server-state contextFetchedAt the first time it's called for that task; never overwrites it on later calls.
- context: Read/write shared context keys. Requires data (JSON object with allowed keys: planSummary, completedTaskIds, filesAdded, filesModified, interfacesCreated, decisionsFromPriorWaves). Optional: branch, maxFiles, maxDecisions, maxInterfaces, maxTaskIds.
- read: Return the full execution state blob. Optional: branch. When the run is in flight (some recorded wave isn't "completed", or plannedTaskIds has IDs not yet in context.completedTaskIds), the blob also carries a "resumeBriefing" (resumable, wavesDone, wavesRemaining, gitCrossCheck, gitMismatches, willRedo, willSkip, summary, display, next) — a dry-run preview of what resume-reset would do. A committedSha that no longer checks out as a git ancestor is reported via gitCrossCheck/gitMismatches, never as a read failure.
- cleanup: Stamp a branch's execution state terminal (runStatus:"completed", runCompletedAt) instead of deleting it — the state file (and its issues[]) survives for later reads (e.g. /harden) until GC's TTL prunes it. Also removes the per-run working directory and ledger directory (working artifacts only, safe to delete) when the state carries a startedAt to derive the runID from; if startedAt is absent, directories are left untouched. Optional: branch.
- gc: Garbage-collect stale state files. Optional: ttlDays, dryRun, branch.
- summarize-prior-wave-context: Summarize context from prior waves. Optional: branch, maxFiles, maxDecisions, maxInterfaces, maxTaskIds.
- wave-split: Split remaining tasks into a new wave. Requires dispatched. Optional: wave, missingIds, branch, splitDepth, maxSplitDepth, stateFile.
- verify-completeness: Verify all planned tasks are accounted for. Optional: branch, stateFile.
- wave-progress: Read/write per-task progress. Requires runId. For reads: readProgress=true. For writes: taskId, phase. Optional: lastCompletedTask (recorded in the heartbeat entry).
- wave-await: Bounded, non-blocking poll of a wave's still-open tasks, classifying each against its server-owned dispatch state (never-started/stalled/timeout/none) and returning explicit next-instructions (including the exact task-fail/task-redispatch call shape) for whatever it finds. Requires runId, wave. Optional: branch, stateFile (also used to persist wave-await's own resume-state, i.e. the iteration counter, across bounded-poll calls).
- resume-reset: Reset in-progress waves for session resume. Optional: branch, stateFile. Returns {resetWaves, clearedTaskIds} as before; when the run is still in flight after the reset, the response also carries a "resumeBriefing" (same shape as read's) reflecting the sets it just cleared — resume-reset's willRedo always matches the task IDs in clearedTaskIds. Reseeds fresh server-owned dispatch state (attempt reset to 1) for every cleared task ID; seeding failure is non-fatal and appends to a "warnings" field.
- ledger_checkin: Register a worker as active. Requires runId, workerId. Optional: stepId.
- ledger_checkout: Mark a worker as done. Requires runId, workerId. Optional: findings (free-text payload — e.g. a JSON array or markdown block — persisted alongside this worker's checkout record and returned later by ledger_status).
- ledger_status: List worker statuses for a run. Requires runId. Optional: timeoutSeconds, expectedWorkers (worker IDs expected to have checked in; any missing from the ledger are returned as missingWorkers). Each entry in the returned workers[] carries a "findings" field when that worker's ledger_checkout call set one; omitted when absent.
- ledger_cleanup: Remove a run's entire ledger directory (all per-worker checkin/checkout/findings files). Requires runId. Returns {ok, runId, removed} where removed is false when the directory didn't exist.
- log-cli: Append a CLI-captured output block to the run's evidence log. Requires cliCommand. Optional: cliExitCode, cliOutput, branch, wave.
- drift-log: Append a drift issue and evaluate the server-side stop condition. When accumulated error-severity drift issues exceed the threshold (max(minErrorFloor, ceil(maxErrorRate * totalTasks))), returns {halt:true}. Requires driftSeverity (error|warning|info), driftSummary. Optional: driftDetail, wave, taskId, branch.
- issue-draft: Append a pending GH issue draft to the state file's pendingIssueDrafts list (append-only — never goes through the context action, never overwrites). Requires issueDraftTitle, issueDraftBody. Optional: issueDraftLabels, taskId, branch. Returns {added:true, totalDrafts:N}.
- decide: Record a guardrail decision (append-only — never goes through the context action, never overwrites; distinct from ship state's own "decide" action, which writes a differently-shaped {step, decision} entry under a different key). Appends {decideType, id, decision, reason} to the state file's guardrailDecisions list. Requires decideType, decideId. Optional: decideDecision, decideReason, branch. Returns {ok:true, action:"decide", next:"..."}.
- report: Assemble the end-of-run execution report (KD-11). With write omitted or false, this is read-only (never writes state or any file). Gated by config automation.report: {enabled:false} returns {skipped:true, written:false} immediately and nothing else — regardless of write. Otherwise returns {branch, runId, planPath, startedAt, duration, format, waves[{number, status, startedAt, completedAt, duration, tasks[{id, name, status, complexity, risk, filesChanged}], committedSha}], totalTasks, completedTasks, failedTasks, skippedTasks, drifts, errors, warnings, concerns, pendingIssueDrafts, deferredFindings, decisions, path, written, next}. format is "json" or "md" (default) from config, or overridden by the format input field. write:true persists the report under <main worktree>/.sdlc-v2/reports/<runId>-report.<ext> and sets path/written on the response instead of leaving the caller to construct that path itself. For format=json, write:true alone is enough — the tool recomputes and writes the full struct. For format=md, write:true additionally requires body (the caller's own rendered markdown) — the tool persists that exact text rather than rendering it again. Optional: branch, write, format, body.

Returns Markdown: a "# execute_state — ok" heading, a **Next:** line, then the fields above. Failures return "# execute_state — error (<code>)" with a "## What happened" and a "## Do this" section.`,
		mcpserver.Annotations{
			Title:       "Read or update execute run state",
			ReadOnly:    false,
			Destructive: true,
			Idempotent:  false,
			OpenWorld:   false,
		},
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
// .sdlc-v2/runs/ lookups; workDir anchors git-branch detection.
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
		return execActionTaskContext(root, workDir, in, now)
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
		return execActionWaveProgress(root, in, now)
	case "wave-await":
		return execActionWaveAwait(root, in, now)
	case "task-redispatch":
		return execActionTaskRedispatch(root, workDir, in, now)
	case "resume-reset":
		return execActionResumeReset(root, workDir, in, now)
	case "ledger_checkin":
		return execActionLedgerCheckin(root, in, now)
	case "ledger_checkout":
		return execActionLedgerCheckout(root, in, now)
	case "ledger_status":
		return execActionLedgerStatus(root, in, now)
	case "ledger_cleanup":
		return execActionLedgerCleanup(root, in)
	case "log-cli":
		return execActionLogCLI(root, workDir, in)
	case "drift-log":
		return execActionDriftLog(root, workDir, in, now)
	case "issue-draft":
		return execActionIssueDraft(root, workDir, in, now)
	case "decide":
		return execActionDecide(root, workDir, in)
	case "report":
		return execActionReport(root, workDir, in, now)
	default:
		return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("unknown action %q", in.Action), Suggestion: "Pass one of the actions listed in execute_state's tool description (e.g. \"wave-start\", \"task-done\", \"ledger_status\")."}
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

// execAssertBranch enforces branch immutability for an in-flight run (KD-4):
// once init has recorded a branch on the state file, every later action
// resolving to a different branch string that nonetheless locates the same
// state file is rejected instead of silently reading/writing under the
// wrong branch identity. This only fires when execFindState's underlying
// state.Find(root, "execute", branch) still resolves to the recorded run —
// e.g. two raw branch strings that collide under SlugifyBranch (mid-session
// rename or ref reformatting, "feat/x" vs "feat.x"), or one branch name that
// is a filename prefix of another ("feat" vs "feat/x"). A branch string that
// resolves to a genuinely different (or missing) state file fails earlier,
// at execFindState, with a DataError — it never reaches this assertion. A
// state file with no recorded branch (pre-KD-4 state, or branch stamped
// nil) is not asserted against — recorded == "" is treated as "nothing to
// compare".
func execAssertBranch(st *state.State, resolved string) error {
	recorded, _ := st.Data["branch"].(string)
	if recorded != "" && recorded != resolved {
		return &mcpserver.DomainError{
			Msg:        fmt.Sprintf("branch changed mid-session: init recorded %q, current is %q", recorded, resolved),
			Suggestion: fmt.Sprintf("Switch back to branch %q or start a new run with execute_state({action:\"init\"}) on the current branch.", recorded),
		}
	}
	return nil
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

// execCurrentWaveNum derives the current wave number from state's waves[]
// array (there is no top-level scalar wave counter in the state shape).
// It prefers the highest-numbered wave with status "in_progress"; if none
// is in progress, it falls back to the highest wave number recorded; if no
// waves exist yet, it returns 0.
func execCurrentWaveNum(data map[string]any) int {
	waves := execEnsureWaves(data)
	highest := 0
	inProgress := -1
	for _, w := range waves {
		wm, ok := w.(map[string]any)
		if !ok {
			continue
		}
		n := execToInt(wm["number"])
		if n > highest {
			highest = n
		}
		if status, _ := wm["status"].(string); status == "in_progress" && n > inProgress {
			inProgress = n
		}
	}
	if inProgress >= 0 {
		return inProgress
	}
	return highest
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

// anyToIntSlice extracts a []int from any (handles []int, []any of
// float64/int -- the shape a JSON-round-tripped state field takes, e.g. a
// resumeFrom.acceptanceDone array reloaded from a state file on disk).
func anyToIntSlice(v any) []int {
	switch arr := v.(type) {
	case []int:
		return arr
	case []any:
		out := make([]int, 0, len(arr))
		for _, el := range arr {
			out = append(out, execToInt(el))
		}
		return out
	default:
		return nil
	}
}

// execParseResumeFrom converts a wave-manifest task row's "resumeFrom"
// value back into a *ResumeFrom. The value round-trips through the state
// file's JSON on disk, so by the time task-context reads it back it is a
// generic map[string]any, not a *ResumeFrom -- this reassembles it. Returns
// nil for anything that isn't a well-formed resumeFrom object (including a
// missing key), so an absent or corrupt record reads as "no resumeFrom"
// rather than failing a live worker dispatch.
func execParseResumeFrom(v any) *ResumeFrom {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	rf := &ResumeFrom{
		AcceptanceDone:    anyToIntSlice(m["acceptanceDone"]),
		FilesTouched:      anyToStringSlice(m["filesTouched"]),
		LastCompletedTask: stringOrEmpty(m["lastCompletedTask"]),
		Blocker:           stringOrEmpty(m["blocker"]),
	}
	if rf.AcceptanceDone == nil {
		rf.AcceptanceDone = []int{}
	}
	if rf.FilesTouched == nil {
		rf.FilesTouched = []string{}
	}
	return rf
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

// IssueDraft is a single pending GH issue draft accumulated on the state
// file's data["pendingIssueDrafts"] list by the issue-draft action.
type IssueDraft struct {
	TaskID    string   `json:"taskId,omitempty"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Labels    []string `json:"labels,omitempty"`
	Timestamp string   `json:"timestamp,omitempty"`
}

// execAppendIssueDraft appends an IssueDraft to data["pendingIssueDrafts"],
// round-tripping it through JSON so the stored representation is always a
// map[string]any — mirroring execAppendIssue's pattern for data["issues"].
// Returns an error on marshal/unmarshal failure so callers never misreport
// success on a silently dropped draft.
func execAppendIssueDraft(data map[string]any, draft IssueDraft) error {
	raw, ok := data["pendingIssueDrafts"].([]any)
	if !ok {
		raw = []any{}
	}
	b, err := json.Marshal(draft)
	if err != nil {
		return fmt.Errorf("marshal issue draft: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("unmarshal issue draft: %w", err)
	}
	data["pendingIssueDrafts"] = append(raw, m)
	return nil
}

// GuardrailDecision is a single guardrail decision accumulated on the state
// file's data["guardrailDecisions"] list by the decide action (KD-2). A
// distinct key from ship state's data["decisions"] (shipStateDecide,
// ship_state.go:927) — that key holds a differently-shaped {step, decision}
// entry, so the two never collide.
type GuardrailDecision struct {
	DecideType string `json:"decideType"`
	ID         string `json:"id"`
	Decision   string `json:"decision,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// execAppendGuardrailDecision appends a GuardrailDecision to
// data["guardrailDecisions"], round-tripping it through JSON so the stored
// representation is always a map[string]any — mirroring
// execAppendIssueDraft's pattern for data["pendingIssueDrafts"].
func execAppendGuardrailDecision(data map[string]any, decision GuardrailDecision) error {
	raw, ok := data["guardrailDecisions"].([]any)
	if !ok {
		raw = []any{}
	}
	b, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("marshal guardrail decision: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("unmarshal guardrail decision: %w", err)
	}
	data["guardrailDecisions"] = append(raw, m)
	return nil
}

// countDriftIssues counts issues with category "drift" grouped by severity.
// All three severity keys (error, warning, info) are always present in the
// returned map so callers never see a nil or partial map.
func countDriftIssues(data map[string]any) map[string]int {
	counts := map[string]int{"error": 0, "warning": 0, "info": 0}
	raw, ok := data["issues"].([]any)
	if !ok {
		return counts
	}
	for _, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if cat, _ := m["category"].(string); cat != "drift" {
			continue
		}
		if sev, _ := m["severity"].(string); sev != "" {
			counts[sev]++
		}
	}
	return counts
}

// ---------------------------------------------------------------------------
// Action: drift-log
// ---------------------------------------------------------------------------

// execActionDriftLog appends a drift issue to the state file and evaluates
// the server-side stop condition. When the accumulated error-severity drift
// count exceeds the threshold (max(minErrorFloor, ceil(maxErrorRate *
// totalTasks))), it returns halt:true so the caller can abort the run.
//
// Decision: config.Read may fail in environments with no config file. In
// that case the handler falls back to the compiled defaults defined in
// internal/config (MaxErrorRate 0.15, MaxWarningRate 0.40, MinErrorFloor 2).
// This duplicates the default values — accepted trade-off to keep drift-log
// usable in bare repos and test fixtures.
func execActionDriftLog(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	// Validate required fields.
	switch in.DriftSeverity {
	case "error", "warning", "info":
	default:
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("driftSeverity must be one of error, warning, info; got %q", in.DriftSeverity),
			Suggestion: "Set driftSeverity to \"error\", \"warning\", or \"info\".",
		}
	}
	if strings.TrimSpace(in.DriftSummary) == "" {
		return nil, &mcpserver.DomainError{Msg: "driftSummary is required", Suggestion: "Provide a driftSummary describing what changed."}
	}

	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}
	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}
	if err := execAssertBranch(st, branch); err != nil {
		return nil, err
	}

	// Determine wave — default to 0 when not supplied.
	waveNum := 0
	if in.Wave != nil {
		waveNum = *in.Wave
	}

	// Append drift issue.
	execAppendIssue(st.Data, StateIssue{
		Wave:      waveNum,
		Step:      "execute",
		TaskID:    in.TaskID,
		Severity:  in.DriftSeverity,
		Category:  "drift",
		Summary:   in.DriftSummary,
		Detail:    in.DriftDetail,
		Timestamp: now().UTC().Format(time.RFC3339),
	})

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	// Load drift config — fall back to compiled defaults on any error.
	maxErrorRate := 0.15
	minErrorFloor := 2
	if cfg, cfgErr := config.Read(root); cfgErr == nil && cfg.Automation.Drift != nil {
		maxErrorRate = cfg.Automation.Drift.MaxErrorRate
		minErrorFloor = cfg.Automation.Drift.MinErrorFloor
	}

	// Resolve totalTasks from state data. JSON round-trip stores numbers as
	// float64, so handle both int and float64.
	totalTasks := 0
	switch v := st.Data["totalTasks"].(type) {
	case float64:
		totalTasks = int(v)
	case int:
		totalTasks = v
	}

	// threshold = max(minErrorFloor, ceil(maxErrorRate * totalTasks))
	rateTerm := int(math.Ceil(maxErrorRate * float64(totalTasks)))
	threshold := minErrorFloor
	if rateTerm > threshold {
		threshold = rateTerm
	}

	counts := countDriftIssues(st.Data)

	// Halt when error count exceeds threshold (strictly greater than).
	if counts["error"] > threshold {
		return DriftLogOut{
			Logged:     true,
			Halt:       true,
			Reason:     fmt.Sprintf("drift error count %d exceeds threshold %d", counts["error"], threshold),
			DriftCount: counts,
			Threshold:  threshold,
		}, nil
	}

	return DriftLogOut{
		Logged:     true,
		DriftCount: counts,
		Threshold:  threshold,
	}, nil
}

// ---------------------------------------------------------------------------
// Action: issue-draft
// ---------------------------------------------------------------------------

// execActionIssueDraft appends a pending GH issue draft to the state file's
// data["pendingIssueDrafts"] list. Decision KD-6: this is append-only and
// deliberately bypasses the "context" action's allowed-key merge semantics —
// every call accumulates a new entry, never overwrites a prior one. Ship
// step 10b (Task 11) later reads the accumulated list for one batch
// approval question under --auto.
func execActionIssueDraft(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if strings.TrimSpace(in.IssueDraftTitle) == "" {
		return nil, &mcpserver.DomainError{Msg: "issueDraftTitle is required", Suggestion: "Provide an issueDraftTitle for the GitHub issue."}
	}
	if strings.TrimSpace(in.IssueDraftBody) == "" {
		return nil, &mcpserver.DomainError{Msg: "issueDraftBody is required", Suggestion: "Provide an issueDraftBody with the issue description."}
	}

	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}
	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}
	if err := execAssertBranch(st, branch); err != nil {
		return nil, err
	}

	if appendErr := execAppendIssueDraft(st.Data, IssueDraft{
		TaskID:    in.TaskID,
		Title:     in.IssueDraftTitle,
		Body:      in.IssueDraftBody,
		Labels:    in.IssueDraftLabels,
		Timestamp: now().UTC().Format(time.RFC3339),
	}); appendErr != nil {
		return nil, &mcpserver.InfraError{Msg: "append issue draft: " + appendErr.Error(), Cause: appendErr}
	}

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	total := 0
	if raw, ok := st.Data["pendingIssueDrafts"].([]any); ok {
		total = len(raw)
	}

	return IssueDraftOut{Added: true, TotalDrafts: total}, nil
}

// ---------------------------------------------------------------------------
// Action: decide
// ---------------------------------------------------------------------------

// execActionDecide records a guardrail decision (KD-2): appends
// {decideType, id, decision, reason} to data["guardrailDecisions"] so the
// end-of-run report can later extract guardrail hits from it. Mirrors
// execActionIssueDraft's validate/resolve/append/write shape.
func execActionDecide(root, workDir string, in ExecuteStateIn) (any, error) {
	if strings.TrimSpace(in.DecideType) == "" {
		return nil, &mcpserver.DomainError{Msg: "decideType is required", Suggestion: "Pass decideType (e.g. \"guardrail\")."}
	}
	if strings.TrimSpace(in.DecideID) == "" {
		return nil, &mcpserver.DomainError{Msg: "decideId is required", Suggestion: "Pass the decideId of the item decided on (e.g. the guardrail slug)."}
	}

	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}
	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}
	if err := execAssertBranch(st, branch); err != nil {
		return nil, err
	}

	if appendErr := execAppendGuardrailDecision(st.Data, GuardrailDecision{
		DecideType: in.DecideType,
		ID:         in.DecideID,
		Decision:   in.DecideDecision,
		Reason:     in.DecideReason,
	}); appendErr != nil {
		return nil, &mcpserver.InfraError{Msg: "append guardrail decision: " + appendErr.Error(), Cause: appendErr}
	}

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	return ExecDecideOut{
		OK:     true,
		Action: "decide",
		Next:   execDecideNextGuidance(in.DecideID, in.DecideDecision),
	}, nil
}

// execDecideNextGuidance builds the Next guidance string for a decide action.
// When a decision is recorded, it names the decision; when only the ID is
// recorded (decision is empty), it omits the "as <decision>" clause.
func execDecideNextGuidance(id, decision string) string {
	if decision == "" {
		return fmt.Sprintf("Guardrail %s recorded. Continue wave execution.", id)
	}
	return fmt.Sprintf("Guardrail %s recorded as %s. Continue wave execution.", id, decision)
}

// ---------------------------------------------------------------------------
// Action: report
// ---------------------------------------------------------------------------

// execActionReport assembles the end-of-run execution report (KD-11):
// waves+tasks, aggregate counts, issues bucketed into drifts/errors/
// warnings/concerns, pending issue drafts, deferred findings, and
// decisions. Mirrors execActionRead's state-loading pattern — read-only,
// never calls state.Write.
//
// Behavior is gated by config.Automation.Report: Enabled == false returns
// ReportSkippedOut{Skipped: true} immediately, before any state is loaded.
// Format ("md", default, or "json") is echoed on the result so the caller
// (ship step 10d) knows whether to render markdown itself or write the
// JSON verbatim. config.Read may fail in bare repos/tests — falls back to
// the compiled defaults (Enabled: true, Format: "md"), matching
// execActionDriftLog's tolerance for a missing/unreadable config.
func execActionReport(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	enabled := true
	format := "md"
	if cfg, err := config.Read(root); err == nil && cfg.Automation != nil && cfg.Automation.Report != nil {
		enabled = cfg.Automation.Report.Enabled
		format = cfg.Automation.Report.Format
	}
	if in.Format != "" {
		if in.Format != "json" && in.Format != "md" {
			return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("report: unknown format %q (want json or md)", in.Format), Suggestion: "Pass format as \"json\" or \"md\", or omit it to use config.automation.report.format."}
		}
		format = in.Format
	}
	if !enabled {
		return ReportSkippedOut{Skipped: true}, nil
	}

	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}
	st, err := execFindState(root, branch)
	if err != nil {
		return nil, err
	}
	if err := execAssertBranch(st, branch); err != nil {
		return nil, err
	}
	runID := execDeriveRunID(st.Data, 0)

	// Write mode with format=md: the caller (ship SKILL.md) already fetched
	// the read-only report in a prior call, rendered it to markdown itself,
	// and now hands the tool that exact text to persist — recomputing the
	// full report here would be wasted work the caller already did.
	if in.Write && format == "md" {
		if strings.TrimSpace(in.Body) == "" {
			return nil, &mcpserver.DomainError{Msg: "report: write=true with format=md requires body (the rendered markdown text to persist)", Suggestion: "Call report read-only first, render the markdown yourself, then call again with write:true, format:\"md\", body:\"<rendered markdown>\"."}
		}
		path, err := execWriteReportFile(root, runID, "md", []byte(in.Body))
		if err != nil {
			return nil, err
		}
		return ExecutionReportOut{
			Branch:  branch,
			RunID:   runID,
			Format:  "md",
			Path:    path,
			Written: true,
			Next:    "Report persisted. Show the path to the user; do not write it yourself.",
		}, nil
	}

	out := ExecutionReportOut{
		Branch: branch,
		Format: format,
		RunID:  runID,
		Waves:  make([]WaveReport, 0),
	}
	out.PlanPath, _ = st.Data["planPath"].(string)
	out.StartedAt, _ = st.Data["startedAt"].(string)
	out.TotalTasks = execToInt(st.Data["totalTasks"])
	if out.StartedAt != "" {
		if d, ok := pipeline.Duration(out.StartedAt, now().UTC().Format(time.RFC3339)); ok {
			out.Duration = pipeline.Humanize(d)
		}
	}

	for _, w := range execEnsureWaves(st.Data) {
		wm, ok := w.(map[string]any)
		if !ok {
			continue
		}
		wr := WaveReport{Number: execToInt(wm["number"])}
		wr.Status, _ = wm["status"].(string)
		wr.StartedAt, _ = wm["startedAt"].(string)
		wr.CompletedAt, _ = wm["completedAt"].(string)
		wr.CommittedSHA, _ = wm["committedSha"].(string)
		if wr.StartedAt != "" && wr.CompletedAt != "" {
			if d, ok := pipeline.Duration(wr.StartedAt, wr.CompletedAt); ok {
				wr.Duration = pipeline.Humanize(d)
			}
		}

		tasks, _ := wm["tasks"].([]any)
		wr.Tasks = make([]TaskReport, 0, len(tasks))
		for _, t := range tasks {
			tm, ok := t.(map[string]any)
			if !ok {
				continue
			}
			tr := TaskReport{}
			tr.ID, _ = tm["id"].(string)
			tr.Name, _ = tm["name"].(string)
			tr.Status, _ = tm["status"].(string)
			tr.Complexity, _ = tm["complexity"].(string)
			tr.Risk, _ = tm["risk"].(string)
			if files, ok := tm["filesChanged"].([]any); ok {
				parts := make([]string, 0, len(files))
				for _, f := range files {
					if s, ok := f.(string); ok {
						parts = append(parts, s)
					}
				}
				tr.Files = strings.Join(parts, ", ")
			}
			switch tr.Status {
			case "completed":
				out.CompletedTasks++
			case "failed":
				out.FailedTasks++
			case "skipped-dependency":
				out.SkippedTasks++
			}
			wr.Tasks = append(wr.Tasks, tr)
		}
		out.Waves = append(out.Waves, wr)
	}

	out.Drifts, out.Errors, out.Warnings, out.Concerns = execReportBucketIssues(st.Data)

	if raw, ok := st.Data["pendingIssueDrafts"].([]any); ok && len(raw) > 0 {
		out.PendingIssueDrafts = raw
	}
	// deferredFindings, step timings, and the CLI-evidence since-time all
	// live on ship_state's own state file, a distinct state.Find(root,
	// "ship", branch) from this execute-run state — not on st.Data.
	// Best-effort cross-read: a missing/unreadable ship state file (e.g.
	// execute ran standalone, never dispatched via /ship) just leaves these
	// fields unset rather than failing this read-only report.
	shipSt, _ := state.Find(root, "ship", branch)
	if shipSt != nil {
		if raw, ok := shipSt.Data["deferredFindings"].([]any); ok && len(raw) > 0 {
			out.DeferredFindings = raw
		}
		out.StepTimings = extractStepTimings(shipSt.Data)
	}
	if out.StepTimings == nil {
		out.StepTimings = []StepTiming{}
	}

	since := out.StartedAt
	if shipSt != nil {
		if shipStarted, ok := shipSt.Data["startedAt"].(string); ok && shipStarted != "" {
			since = shipStarted
		}
	}
	if evidence, err := readCLIEvidenceInWindow(root, branch, since, maxCLIEvidenceInWindow); err != nil {
		out.Warnings = append(out.Warnings, StateIssue{
			Severity: "warning",
			Category: "cross-read",
			Summary:  "CLI evidence read failed: " + err.Error(),
		})
	} else {
		out.CLIEvidence = evidence
	}
	if out.CLIEvidence == nil {
		out.CLIEvidence = []CLIEvidenceEntry{}
	}

	// GuardrailHits and LinkedLearnings live on this run's own state file
	// (data["guardrailDecisions"], appended by the decide action) and the
	// shared learnings log respectively — neither depends on shipSt.
	out.GuardrailHits = extractGuardrailHits(st.Data)
	linkedCount, linkedErr := countLinkedLearnings(root, out.RunID)
	if linkedErr != nil {
		out.Warnings = append(out.Warnings, StateIssue{
			Severity: "warning",
			Category: "cross-read",
			Summary:  "Learnings count failed: " + linkedErr.Error(),
		})
	}
	out.LinkedLearnings = linkedCount

	if ctxMap, ok := st.Data["context"].(map[string]any); ok {
		if raw, ok := ctxMap["decisionsFromPriorWaves"].([]any); ok {
			for _, d := range raw {
				if s, ok := d.(string); ok {
					out.Decisions = append(out.Decisions, s)
				}
			}
		}
	}

	// Write mode with format=json: the tool has just recomputed the full
	// report struct above, so persist that same struct verbatim rather than
	// asking the caller to write it (and risk it landing in a linked
	// worktree instead of the main one — see execWriteReportFile).
	if in.Write && format == "json" {
		path, werr := execWriteReportFile(root, runID, "json", out)
		if werr != nil {
			return nil, werr
		}
		out.Path = path
		out.Written = true
		out.Next = "Report persisted. Show the path to the user; do not write it yourself."
	}

	return out, nil
}

// execWriteReportFile persists an execution report under <root>/.sdlc-v2/
// reports/<runID>-report.<ext>, main-worktree-rooted via the same root
// callers already resolve through worktree.MainRoot() (see
// RegisterExecuteStateTools) rather than the session's possibly-linked-
// worktree cwd. It creates the reports/ directory if needed and writes
// atomically (temp file + rename) so a reader never observes a partial
// file. content is either an ExecutionReportOut (ext "json", marshaled via
// fsx.AtomicWriteJSON) or raw markdown bytes (ext "md", written as-is).
// Returns the absolute path written.
func execWriteReportFile(root, runID, ext string, content any) (string, error) {
	dir := filepath.Join(root, paths.DataDir, "reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", &mcpserver.InfraError{Msg: "mkdir reports dir: " + err.Error(), Cause: err, Suggestion: "Check that .sdlc-v2/ is writable and there is no file named reports/ blocking directory creation."}
	}
	path := filepath.Join(dir, runID+"-report."+ext)

	if ext == "json" {
		if err := fsx.AtomicWriteJSON(path, content); err != nil {
			return "", &mcpserver.InfraError{Msg: "write report: " + err.Error(), Cause: err, Suggestion: "Check disk space and write permissions on .sdlc-v2/reports/, then retry."}
		}
		return path, nil
	}

	data, ok := content.([]byte)
	if !ok {
		return "", &mcpserver.InfraError{Msg: fmt.Sprintf("write report: unsupported content type %T for ext %q", content, ext), Suggestion: "Pass format \"json\" with a struct body, or format \"md\" with a []byte body — no other content/ext combination is supported."}
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", &mcpserver.InfraError{Msg: "create temp report file: " + err.Error(), Cause: err, Suggestion: "Check disk space and write permissions on .sdlc-v2/reports/, then retry."}
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", &mcpserver.InfraError{Msg: "write temp report file: " + err.Error(), Cause: err, Suggestion: "Check disk space on the .sdlc-v2/reports/ volume, then retry."}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", &mcpserver.InfraError{Msg: "close temp report file: " + err.Error(), Cause: err, Suggestion: "Retry the write; if this persists, check for a filesystem or disk issue on .sdlc-v2/reports/."}
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return "", &mcpserver.InfraError{Msg: "rename temp report file into place: " + err.Error(), Cause: err, Suggestion: "Check that .sdlc-v2/reports/ is on a single filesystem and writable, then retry."}
	}
	return path, nil
}

// extractStepTimings reads ship state's data["steps"] (see
// shipStepsSlice/ShipStateStep) and derives one StepTiming per entry.
// Duration is computed via pipeline.Duration/Humanize when both startedAt
// and completedAt are present and parse; otherwise Duration stays empty.
// HumanWait is true for step names in pipeline.HumanWaitSteps (their
// elapsed time reflects human latency, not pipeline work). Returns an empty
// (never nil) slice when data["steps"] is absent or empty.
func extractStepTimings(data map[string]any) []StepTiming {
	out := []StepTiming{}
	for _, s := range shipStepsSlice(data) {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		name, _ := sm["name"].(string)
		timing := StepTiming{
			Name:      name,
			HumanWait: pipeline.HumanWaitSteps[name],
		}
		timing.Status, _ = sm["status"].(string)
		timing.StartedAt, _ = sm["startedAt"].(string)
		completedAt, _ := sm["completedAt"].(string)
		if timing.StartedAt != "" && completedAt != "" {
			if d, ok := pipeline.Duration(timing.StartedAt, completedAt); ok {
				timing.Duration = pipeline.Humanize(d)
			}
		}
		out = append(out, timing)
	}
	return out
}

// extractGuardrailHits extracts guardrail IDs from data["guardrailDecisions"]
// (appended by the decide action, KD-2) where decideType == "guardrail".
// Returns an empty (never nil) slice when the key is absent or holds no
// guardrail-type decisions.
func extractGuardrailHits(data map[string]any) []string {
	hits := []string{}
	raw, ok := data["guardrailDecisions"].([]any)
	if !ok {
		return hits
	}
	for _, d := range raw {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}
		if decideType, _ := dm["decideType"].(string); decideType != "guardrail" {
			continue
		}
		if id, _ := dm["id"].(string); id != "" {
			hits = append(hits, id)
		}
	}
	return hits
}

// countLinkedLearnings counts learnings log lines tagged with this run's ID
// (KD-3: learningsAppend prepends "<!-- sdlc:run=<runId> branch=<branch>
// -->"). Matches by substring on "sdlc:run=<runId> " (trailing space
// prevents prefix collisions), not the full tag, since the tag also
// carries "branch=<branch>". Returns (0, nil) when runID is empty or the
// log file does not exist. Returns a non-nil error only for unexpected
// read failures (e.g. permission denied).
func countLinkedLearnings(root, runID string) (int, error) {
	if runID == "" {
		return 0, nil
	}
	data, err := os.ReadFile(learningsLogPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	needle := "sdlc:run=" + runID + " "
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, needle) {
			count++
		}
	}
	return count, nil
}

// execReportBucketIssues partitions data["issues"] into the four buckets
// ExecutionReportOut surfaces separately. Category "drift" always buckets
// as Drifts, regardless of severity (drift-log accepts error/warning/info
// severities); category "done-with-concerns" always buckets as Concerns.
// Everything else buckets by severity: "error" -> Errors (today: wave-fail,
// task-fail categories), "warning" -> Warnings. An issue that is neither a
// recognized category nor error/warning severity (an "info"-severity
// non-drift issue — none exist today) is dropped from all four buckets
// rather than guessed at.
func execReportBucketIssues(data map[string]any) (drifts, errs, warnings, concerns []StateIssue) {
	drifts = make([]StateIssue, 0)
	errs = make([]StateIssue, 0)
	warnings = make([]StateIssue, 0)
	concerns = make([]StateIssue, 0)
	raw, ok := data["issues"].([]any)
	if !ok {
		return drifts, errs, warnings, concerns
	}
	for _, v := range raw {
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		var iss StateIssue
		if err := json.Unmarshal(b, &iss); err != nil {
			continue
		}
		switch {
		case iss.Category == "drift":
			drifts = append(drifts, iss)
		case iss.Category == "done-with-concerns":
			concerns = append(concerns, iss)
		case iss.Severity == "error":
			errs = append(errs, iss)
		case iss.Severity == "warning":
			warnings = append(warnings, iss)
		}
	}
	return drifts, errs, warnings, concerns
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

// sha256File returns the hex-encoded sha256 digest of the file at path, for
// comparison against a planHash recorded at init (KD-5 drift detection).
// The file is streamed through the hash rather than read fully into memory.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
	return filepath.Join(root, paths.DataDir, paths.RunsSubdir, "ledger", runID)
}

// ledgerFilePath returns the per-worker ledger file path.
func ledgerFilePath(root, runID, workerID string) string {
	return filepath.Join(ledgerDir(root, runID), workerID+".json")
}

// ---------------------------------------------------------------------------
// Action: init
// ---------------------------------------------------------------------------

// openspecSourceRe matches a plan document's "**Source:** openspec/changes/<name>/"
// header line, written by the plan skill's Step 0 (and left as "[TBD]" or
// something else, e.g. "conversation context", for a non-openspec plan).
var openspecSourceRe = regexp.MustCompile(`(?m)^\*\*Source:\*\*\s*openspec/changes/([^\s/]+)/?\s*$`)

// openspecChangeFromPlan extracts the OpenSpec change name from a plan
// document's "**Source:**" header. Returns "" when the header is absent,
// still the "[TBD]" placeholder, or names anything other than an openspec
// change. It returns the raw captured segment as-is — including a
// path-traversal shape like ".." — with no safety filtering; callers must
// gate the result through isSafeChangeName before using it as a path
// component (see execActionInit).
func openspecChangeFromPlan(planContent string) string {
	m := openspecSourceRe.FindStringSubmatch(planContent)
	if m == nil {
		return ""
	}
	return m[1]
}

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
	// rendered "error (data)" result.
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

	// Cross-read ship state for pipeline auto-mode: when execute was
	// dispatched from /ship and the user already approved --auto there,
	// forward that into pipelineAuto so the high-risk gate (execute
	// SKILL.md) doesn't force a second approval.
	//
	// state.Find returns (nil, nil) when no matching file exists, and a
	// non-nil error only on I/O or JSON-parse failures. We distinguish:
	//   - (nil, nil): no ship state → pipelineAuto stays false, silently.
	//   - (st, nil):  ship state found → read flags.auto.
	//   - (_, err):   genuine I/O/parse failure → pipelineAuto stays false,
	//                 but the error is surfaced as a warning so the caller
	//                 can diagnose why auto-forward didn't happen.
	st.Data["pipelineAuto"] = false
	var initWarnings []string
	shipSt, shipErr := state.Find(root, "ship", in.Branch)
	if shipErr != nil {
		initWarnings = append(initWarnings, fmt.Sprintf("ship state unreadable: %s", shipErr.Error()))
	} else if shipSt != nil {
		if flags, ok := shipSt.Data["flags"].(map[string]any); ok {
			if auto, ok := flags["auto"].(bool); ok && auto {
				st.Data["pipelineAuto"] = true
			}
		}
	}

	// Apply the openspec ref stamps plan_prepare deferred (see plan.go's
	// pendingTaskRefs/stampTaskRefs): plan_prepare runs inside plan mode and
	// must not touch git-tracked files, so it only computed which tasks.md
	// lines were pending a ref comment. Now that the plan is approved, write
	// them for real. Warning-only: a standalone execute has no plan file,
	// and a missing or non-openspec plan is not an error.
	if in.PlanPath != "" {
		content, readErr := os.ReadFile(in.PlanPath)
		if readErr != nil {
			// A caller-supplied planPath that cannot be read is worth
			// surfacing: the ref stamp is silently skipped, and without a
			// warning the caller has no way to tell that from "the plan was
			// not an openspec plan". Still non-fatal — init must succeed.
			initWarnings = append(initWarnings,
				fmt.Sprintf("init: openspec ref stamp skipped: plan unreadable: %v", readErr))
		} else if change := openspecChangeFromPlan(string(content)); change != "" && isSafeChangeName(change) {
			tasksPath := filepath.Join(workDir, "openspec", "changes", change, "tasks.md")
			if _, stampErr := stampTaskRefs(tasksPath); stampErr != nil {
				initWarnings = append(initWarnings,
					fmt.Sprintf("init: openspec ref stamp skipped: %v", stampErr))
			}
		}
	}

	// Wave stall-timeout params, resolved once here so wave-await never
	// re-derives them per call (see execWaveStallTimeouts). Source order:
	// explicit init input (this run's
	// own --wave-timeout/--wave-interval, forwarded by the invoking CLI) >
	// a ship-state cross-read of the same run's flags.executeWaveTimeout /
	// flags.executeWaveInterval (set when ship dispatched this run) >
	// shipmeta.ShipBuiltInDefaults (the standalone-execute default).
	waveTimeoutSec := shipmeta.ShipBuiltInDefaults.ExecuteWaveTimeout
	waveIntervalSec := shipmeta.ShipBuiltInDefaults.ExecuteWaveInterval
	if shipSt != nil {
		if flags, ok := shipSt.Data["flags"].(map[string]any); ok {
			if v := execToInt(flags["executeWaveTimeout"]); v > 0 {
				waveTimeoutSec = v
			}
			if v := execToInt(flags["executeWaveInterval"]); v > 0 {
				waveIntervalSec = v
			}
		}
	}
	if in.WaveTimeoutSeconds > 0 {
		waveTimeoutSec = in.WaveTimeoutSeconds
	}
	if in.WaveIntervalSeconds > 0 {
		waveIntervalSec = in.WaveIntervalSeconds
	}
	st.Data["waveTimeoutSeconds"] = waveTimeoutSec
	st.Data["waveIntervalSeconds"] = waveIntervalSec

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	result := map[string]any{"filePath": st.Path, "pipelineAuto": st.Data["pipelineAuto"]}
	if len(initWarnings) > 0 {
		result["warnings"] = initWarnings
	}
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
	if err := execAssertBranch(st, branch); err != nil {
		return nil, err
	}

	// KD-5: server-side plan-drift check. Compares the plan file's current
	// sha256 against the hash recorded at init — before the wave (or any
	// state mutation below) exists, so a halt here leaves the wave untouched.
	// Empty/absent planHash (pre-KD-5 state, or init without a plan file)
	// skips the comparison entirely. An unreadable/absent planPath is not
	// treated as drift — filesystem hiccups shouldn't halt a run — but is
	// surfaced as a warning in the normal response instead of silently
	// swallowed.
	var planHashWarnings []string
	if storedHash, _ := st.Data["planHash"].(string); storedHash != "" {
		planPath, _ := st.Data["planPath"].(string)
		if planPath == "" {
			planHashWarnings = append(planHashWarnings, "plan drift check skipped: no planPath recorded on this run")
		} else if computed, hashErr := sha256File(planPath); hashErr != nil {
			planHashWarnings = append(planHashWarnings, fmt.Sprintf("plan drift check skipped: could not read planPath %q: %s", planPath, hashErr.Error()))
		} else if computed != storedHash {
			execAppendIssue(st.Data, StateIssue{
				Wave:      *in.Wave,
				Severity:  "error",
				Category:  "drift",
				Summary:   "plan content changed since init",
				Detail:    fmt.Sprintf("planPath %q sha256 is now %s, expected %s recorded at init", planPath, computed, storedHash),
				Timestamp: now().UTC().Format(time.RFC3339),
			})
			if err := state.Write(st); err != nil {
				return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
			}
			return DriftLogOut{
				Logged:     true,
				Halt:       true,
				Reason:     "plan hash mismatch",
				DriftCount: countDriftIssues(st.Data),
				Next:       "Plan content has changed since init. Re-run execute_state({action:\"init\"}) to acknowledge the new plan, or investigate the drift.",
			}, nil
		}
	}

	// Parse and validate tasksJson BEFORE wave creation / state.Write
	// so that invalid JSON never leaves a half-written wave on disk.
	var validTasks []map[string]any
	var validTasksAsAny []any // built in-line during validation to avoid a second copy loop
	var dropped int
	result := ExecWaveNarrationOut{}
	if len(planHashWarnings) > 0 {
		result.Warnings = planHashWarnings
	}

	if in.TasksJSON != "" {
		var parsedTasks []any
		if err := json.Unmarshal([]byte(in.TasksJSON), &parsedTasks); err != nil {
			return nil, &mcpserver.DomainError{Msg: "tasksJson is not valid JSON: " + err.Error(), Cause: err, Suggestion: "tasksJson must be a JSON array of task objects, e.g. [{\"id\":\"T1\",\"name\":\"...\",\"description\":\"...\"}]"}
		}

		// Pre-write validation: filter out entries that are not maps or lack
		// non-empty string id, name, or description — including numeric IDs
		// which would otherwise cause a type-assertion miss.
		for _, t := range parsedTasks {
			tm, ok := t.(map[string]any)
			if !ok {
				dropped++
				continue
			}
			if !isValidTaskEntry(tm) {
				dropped++
				continue
			}
			validTasks = append(validTasks, tm)
			validTasksAsAny = append(validTasksAsAny, tm)
		}
		if dropped > 0 {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("wave-start: dropped %d entries from tasksJson (not map or missing/non-string id, name, or description)", dropped))
		}

		if len(validTasks) == 0 {
			return nil, &mcpserver.DomainError{
				Msg:        "tasksJson contains no valid task entries",
				Suggestion: "Each entry must be an object with non-empty string fields: id, name, description",
			}
		}
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
	if in.TasksJSON != "" {
		// Plan cross-check: warn when a task's name in tasksJson diverges
		// from the plan heading. Warning-only — plan file may not exist
		// (standalone execute without ship), so a missing plan silently skips.
		if planPath, _ := st.Data["planPath"].(string); planPath != "" {
			planContent, planErr := os.ReadFile(planPath)
			if planErr != nil && !os.IsNotExist(planErr) {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("wave-start: plan cross-check skipped: %v", planErr))
			}
			if planErr == nil {
				planTasks := extractTasks(string(planContent))
				planNames := map[int]string{}
				for _, pt := range planTasks {
					planNames[pt.Number] = pt.Title
				}
				for _, tm := range validTasks {
					id, _ := tm["id"].(string)
					name := stringOrEmpty(tm["name"])
					if n, err := strconv.Atoi(id); err == nil {
						if expected, ok := planNames[n]; ok && name != expected {
							result.Warnings = append(result.Warnings,
								fmt.Sprintf("task %s: name %q does not match plan heading %q", id, name, expected))
						}
					}
				}
			}
		}

		runID := in.RunID
		if runID == "" {
			runID = execDeriveRunID(st.Data, *in.Wave)
		}

		summary := execSummarizePriorWaveCtx(st.Data, root, 0, 0, 0, 0)

		writtenPaths := []string{}
		var factSheetErrors []string
		for _, tm := range validTasks {
			id, _ := tm["id"].(string)

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

		// Seed server-owned dispatch state for every task in this wave-start
		// call. A dispatched task must always have server state, even if its
		// agent never actually runs -- wave-await's classification depends on
		// it. A task that already has server state (wave-start called again
		// on resume) is left untouched: dispatchedAt must never reset on an
		// in-flight task. Seeding failure is non-fatal -- it is recorded as a
		// warning, never fails the wave.
		nowStamp := waveAwaitFormat(now())
		for _, tm := range validTasks {
			id, _ := tm["id"].(string)
			if id == "" {
				continue
			}
			if _, found, lerr := wave.LoadServerState(root, runID, id); lerr != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("wave-start: seed server state for task %s: %s", id, lerr.Error()))
				continue
			} else if found {
				continue
			}

			workerName := stringOrEmpty(tm["workerName"])
			if workerName == "" {
				workerName = execDefaultWorkerName(id)
			}
			if err := wave.StoreServerState(root, runID, id, wave.ServerTaskState{
				DispatchedAt: nowStamp,
				WorkerName:   workerName,
				BatchID:      stringOrEmpty(tm["batchId"]),
				BatchIndex:   execToInt(tm["batchIndex"]),
				Attempt:      1,
			}); err != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("wave-start: seed server state for task %s: %s", id, err.Error()))
			}
		}

		// Store planned task list on the wave for task-context sibling lookup.
		// This persists the validated task entries so that any worker calling
		// task-context can discover its siblings without per-task file reads.
		planned := make([]any, 0, len(validTasks))
		for _, tm := range validTasks {
			files, _ := tm["files"].([]any)
			if files == nil {
				files = []any{}
			}
			planned = append(planned, map[string]any{
				"id":    tm["id"],
				"name":  stringOrEmpty(tm["name"]),
				"files": files,
			})
		}
		w["planned"] = planned
		// Persist the run ID authoritatively: the PostToolUse liveness hook
		// has no runId input and must not re-derive it from startedAt via
		// execDeriveRunID.
		w["runId"] = runID
		if err := state.Write(st); err != nil {
			return nil, &mcpserver.InfraError{Msg: "write state (planned): " + err.Error(), Cause: err}
		}

		result.RunID = runID
		result.FactSheets = writtenPaths
		if len(factSheetErrors) > 0 {
			result.FactSheetErrors = factSheetErrors
		}
	}

	// Build narration — taskCount reflects valid tasks (post-validation),
	// not the raw parsedTasks slice which may have contained invalid entries.
	taskCount := len(validTasks)
	result.Summary = fmt.Sprintf("Wave %d started with %d tasks.", *in.Wave, taskCount)

	if execDetailLevel(in) == "full" {
		waveTasks := execBuildWaveTasks(validTasksAsAny)
		wi := pipeline.WaveInfo{
			Number: *in.Wave,
			Tasks:  waveTasks,
		}
		// Pass nil for TimingsStore — ETA lives in Next, not in the block.
		result.Display = pipeline.WaveStartBlock(wi, nil)
	}

	// Build Next with ETA.
	maxC := execMaxComplexityFromTasks(validTasksAsAny)
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

// execDefaultWorkerName is the fallback workerName template used when a
// wave-start tasksJson entry omits one. workerName is never invented beyond
// this deterministic placeholder -- a caller-supplied name always wins.
func execDefaultWorkerName(taskID string) string {
	return "worker-" + taskID
}

// isValidTaskEntry checks that a task map has non-empty string id, name, and
// description. Numeric IDs (e.g. {"id": 42}) are rejected — the value must be
// a string, not merely truthy.
func isValidTaskEntry(tm map[string]any) bool {
	for _, key := range []string{"id", "name", "description"} {
		v, ok := tm[key]
		if !ok {
			return false
		}
		s, isStr := v.(string)
		if !isStr || s == "" {
			return false
		}
	}
	return true
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
	if err := execAssertBranch(st, branch); err != nil {
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
	if err := execAssertBranch(st, branch); err != nil {
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
	if err := execAssertBranch(st, branch); err != nil {
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
	if err := execAssertBranch(st, branch); err != nil {
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

	// Parse verifyToken — default to empty slice (not nil) so the persisted
	// JSON contains [] rather than null, matching downstream type assertions.
	verifyTokens := []any{}
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
	if err := execAssertBranch(st, branch); err != nil {
		return nil, err
	}

	w := execFindOrCreateWave(st.Data, *in.Wave, now)
	tasks, _ := w["tasks"].([]any)
	if tasks == nil {
		tasks = []any{}
	}

	// Phantom-success detection (KD, task 2): computed against the wave's
	// existing tasks BEFORE this task's own entry is appended below, so a
	// re-submission of the same task (upsert) never matches itself.
	var warnings []string
	if len(verifyTokens) > 0 {
		warnedSiblings := map[string]bool{}
		for _, sibling := range tasks {
			sm, ok := sibling.(map[string]any)
			if !ok {
				continue
			}
			if sid, _ := sm["id"].(string); sid == in.TaskID {
				continue // skip self on re-submission (upsert case)
			}
			sibID := fmt.Sprint(sm["id"])
			if warnedSiblings[sibID] {
				continue
			}
			sibTokens, _ := sm["verifyTokens"].([]any)
			for _, tok := range sibTokens {
				for _, vt := range verifyTokens {
					if fmt.Sprint(tok) == fmt.Sprint(vt) {
						warnings = append(warnings, fmt.Sprintf(
							"verifyToken duplicates task %v — possible phantom success",
							sm["id"]))
						warnedSiblings[sibID] = true
						break
					}
				}
				if warnedSiblings[sibID] {
					break
				}
			}
		}
	}
	if len(filesChanged) == 0 && in.Status != "FAILED" {
		warnings = append(warnings, "no files reported changed — verify task produced real output")
	}

	taskEntry := map[string]any{
		"id":           in.TaskID,
		"name":         in.TaskName,
		"complexity":   in.Complexity,
		"risk":         in.Risk,
		"status":       "completed",
		"filesChanged": filesChanged,
		"verifyTokens": verifyTokens,
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
	result.Warnings = warnings
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
	if err := execAssertBranch(st, branch); err != nil {
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

	runID := in.RunID
	if runID == "" {
		runID = execDeriveRunID(st.Data, *in.Wave)
	}
	currentAttempt := 1
	if s, found, lerr := wave.LoadServerState(root, runID, in.TaskID); lerr != nil {
		return nil, &mcpserver.InfraError{Msg: "load server state for task " + in.TaskID + ": " + lerr.Error(), Cause: lerr}
	} else if found {
		currentAttempt = s.Attempt
	}

	// Idempotency: a repeat task-fail for the same task, at the same
	// attempt, with no intervening task-redispatch, is a no-op -- not an
	// error and not a re-record. It must not duplicate the issue log or
	// move completedAt forward.
	for _, t := range tasks {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if tid, _ := tm["id"].(string); tid != in.TaskID {
			continue
		}
		existingStatus, _ := tm["status"].(string)
		existingAttempt := execToInt(tm["attempt"])
		if existingStatus == taskStatus && existingAttempt == currentAttempt {
			completed, failed, total := execCountWaveOutcomes(w)
			result := ExecTaskNarrationOut{}
			if in.SkippedDep {
				result.Summary = fmt.Sprintf("Task %s already skipped at attempt %d (no-op) (%d/%d reported, %d failed).", in.TaskID, currentAttempt, completed+failed, total, failed)
			} else {
				result.Summary = fmt.Sprintf("Task %s already failed at attempt %d (no-op) (%d/%d reported, %d failed).", in.TaskID, currentAttempt, completed+failed, total, failed)
			}
			return result, nil
		}
		break
	}

	// Harvest whatever partial-work claim the worker last reported via
	// wave-progress (if any) before this failure, so a later task-context
	// call for a redispatched retry can surface it as a re-verify-first
	// block (KD5 mitigation) instead of losing it. Advisory only -- stored
	// on the fresh taskEntry below, never merged with an older record, so a
	// second failure's harvest fully replaces the first's rather than
	// accumulating stale data.
	var resumeFrom *ResumeFrom
	if prog, perr := wave.ReadProgress(root, runID); perr == nil {
		if tp, ok := prog.Tasks[in.TaskID]; ok &&
			(len(tp.AcceptanceDone) > 0 || len(tp.FilesTouched) > 0 || tp.LastCompletedTask != "" || tp.Blocker != "") {
			rf := waveAwaitHarvest(tp)
			resumeFrom = &rf
		}
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
		"attempt":      currentAttempt,
	}
	if resumeFrom != nil {
		taskEntry["resumeFrom"] = resumeFrom
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
// Action: task-redispatch
// ---------------------------------------------------------------------------

// execFindWaveTaskRow locates the wave manifest and the tasks[] row for
// taskID. If waveNum is non-nil, only that wave is searched; otherwise every
// recorded wave is scanned. task-redispatch's documented call shape passes
// only runId/taskId, never wave, so the row must be locatable by task ID
// alone. Returns nil, nil, 0 if no matching row is found.
func execFindWaveTaskRow(data map[string]any, taskID string, waveNum *int) (wm, tm map[string]any, foundWaveNum int) {
	waves := execEnsureWaves(data)
	for _, w := range waves {
		wmCandidate, ok := w.(map[string]any)
		if !ok {
			continue
		}
		n := execToInt(wmCandidate["number"])
		if waveNum != nil && n != *waveNum {
			continue
		}
		tasks, _ := wmCandidate["tasks"].([]any)
		for _, t := range tasks {
			tmCandidate, ok := t.(map[string]any)
			if !ok {
				continue
			}
			if tid, _ := tmCandidate["id"].(string); tid == taskID {
				return wmCandidate, tmCandidate, n
			}
		}
	}
	return nil, nil, 0
}

// TaskRedispatchOut is the returned payload for the task-redispatch action:
// confirms the fresh, solo server state seeded for the reopened task and
// tells the caller what to do next. Next is never empty -- guardrail
// mcp-output-drives-behavior.
type TaskRedispatchOut struct {
	TaskID       string `json:"taskId"`
	Wave         int    `json:"wave"`
	RunID        string `json:"runId"`
	DispatchedAt string `json:"dispatchedAt"`
	WorkerName   string `json:"workerName"`
	Attempt      int    `json:"attempt"`
	RetriesLeft  int    `json:"retriesLeft"`
	Next         string `json:"next"`
}

// execActionTaskRedispatch reopens a failed task for another attempt:
//  1. re-opens the task's wave manifest row to "in_progress" -- task-fail
//     closed it, and wave-await only classifies rows that are still open.
//  2. deletes then re-stores the task's server state with a fresh
//     dispatchedAt and attempt+1; contextFetchedAt, reclaimRequestedAt, and
//     batchId all come back empty -- a redispatch is always solo, even when
//     the failed attempt was part of a batch.
//  3. refuses at the 2-retry ceiling (attempt already at 3) with a
//     DomainError naming user escalation, rather than seeding a 4th
//     attempt.
func execActionTaskRedispatch(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.TaskID == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "taskId is required for task-redispatch",
			Suggestion: "Pass taskId, e.g. execute_state {action:\"task-redispatch\", runId:\"<runId>\", taskId:\"<taskId>\"}.",
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
	if err := execAssertBranch(st, branch); err != nil {
		return nil, err
	}

	wm, tm, waveNum := execFindWaveTaskRow(st.Data, in.TaskID, in.Wave)
	if wm == nil || tm == nil {
		return nil, &mcpserver.DataError{
			Msg:        fmt.Sprintf("no task-fail record found for task %q; task-redispatch requires a prior task-fail", in.TaskID),
			Suggestion: "Call task-fail for this task before task-redispatch.",
		}
	}
	if status, _ := tm["status"].(string); status == "completed" {
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("task %q is already completed; task-redispatch only applies to a failed task", in.TaskID),
			Suggestion: fmt.Sprintf("Task %s already succeeded -- do not redispatch it. If this is unexpected, check task-fail was called for the right taskId.",
				in.TaskID),
		}
	}

	runID := in.RunID
	if runID == "" {
		runID = execDeriveRunID(st.Data, waveNum)
	}

	prev, found, lerr := wave.LoadServerState(root, runID, in.TaskID)
	if lerr != nil {
		return nil, &mcpserver.InfraError{Msg: "load server state for task " + in.TaskID + ": " + lerr.Error(), Cause: lerr}
	}
	attempt := 1
	if found {
		attempt = prev.Attempt
	}
	if waveAwaitRetriesLeft(attempt) <= 0 {
		return nil, &mcpserver.DomainError{
			Msg: fmt.Sprintf("task %q has exhausted its retries at attempt %d", in.TaskID, attempt),
			Suggestion: fmt.Sprintf("Escalate task %s to the user instead of redispatching again — it has already used its 2 retries.",
				in.TaskID),
		}
	}

	// 1) Re-open the wave manifest row.
	tm["status"] = "in_progress"
	delete(tm, "completedAt")
	delete(tm, "error")

	// 2) Fresh, solo server state: new dispatchedAt, attempt+1, no
	// contextFetchedAt/reclaimRequestedAt/batchId.
	workerName := prev.WorkerName
	if workerName == "" {
		workerName = execDefaultWorkerName(in.TaskID)
	}
	if err := wave.DeleteServerState(root, runID, in.TaskID); err != nil {
		return nil, &mcpserver.InfraError{Msg: "clear server state for task " + in.TaskID + ": " + err.Error(), Cause: err}
	}
	next := wave.ServerTaskState{
		DispatchedAt: waveAwaitFormat(now()),
		WorkerName:   workerName,
		Attempt:      attempt + 1,
	}
	if err := wave.StoreServerState(root, runID, in.TaskID, next); err != nil {
		return nil, &mcpserver.InfraError{Msg: "seed server state for task " + in.TaskID + ": " + err.Error(), Cause: err}
	}

	if err := state.Write(st); err != nil {
		return nil, &mcpserver.InfraError{Msg: "write state: " + err.Error(), Cause: err}
	}

	return TaskRedispatchOut{
		TaskID:       in.TaskID,
		Wave:         waveNum,
		RunID:        runID,
		DispatchedAt: next.DispatchedAt,
		WorkerName:   next.WorkerName,
		Attempt:      next.Attempt,
		RetriesLeft:  waveAwaitRetriesLeft(next.Attempt),
		Next: fmt.Sprintf(
			"Dispatch task %s again (worker %s, attempt %d), then call execute_state {action:\"wave-await\", runId:%q, wave:%d} to resume supervision. Do NOT run the wave gates yet.",
			in.TaskID, next.WorkerName, next.Attempt, runID, waveNum,
		),
	}, nil
}

// ---------------------------------------------------------------------------
// Action: task-context
// ---------------------------------------------------------------------------

// execHeartbeatPhases is the canonical list of progress phases a dispatched
// worker reports via wave-progress. Both the prose reportBack and the
// structured ExecutionRules.HeartbeatPhases reference this single source so
// they cannot diverge.
var execHeartbeatPhases = []string{"started", "reading", "editing", "verifying", "reporting"}

// execReportFormat is the completion-block template workers emit at the end
// of their response. Referenced by both the prose reportBack and the
// structured ExecutionRules.ReportFormat.
const execReportFormat = "Summary: <one line>\n\nFiles created: <paths or none>\nFiles modified: <paths or none>\nTests: added=<yes|no|n/a> pass=<yes|no|n/a>\nBuild: pass=<yes|no|n/a>\n\nVERIFY: <symbol_name> in <file_path>\n\nConcerns:\n- <one bullet per concern, 3 max; omit if none>\n\nInterfaces:\n- <exported symbol; omit if none>\n\nDecisions:\n- <decision and why; omit if none>\n\nSTATUS: SUCCESS | DONE_WITH_CONCERNS | FAILED"

// execVerifyMethod is a short description of the verification strategy
// workers are expected to follow for each task.
const execVerifyMethod = "build-and-test + git-diff-scope + VERIFY canary"

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

// execInsertResumeFromSection splices a rendered "Resume from a reclaimed
// attempt" section (wave.RenderResumeFromSection) into fact-sheet markdown
// immediately after the "# Task <id>: <name>" header line, ahead of
// execTaskContextCapPayload's tail-trim below. The on-disk fact-sheet file
// itself is never rewritten: resumeFrom only becomes known after
// WriteFactsheet originally wrote that file, at redispatch time, not at the
// task's first dispatch -- this only augments the copy returned to the
// caller.
func execInsertResumeFromSection(content, section string) string {
	nl := strings.Index(content, "\n")
	if nl == -1 {
		return strings.TrimRight(content, "\n") + "\n\n" + section
	}
	header := content[:nl+1]
	rest := strings.TrimPrefix(content[nl+1:], "\n")
	return header + "\n" + section + rest
}

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

// execTaskContextReportBack returns report-back instructions for a
// dispatched worker, mirroring the heartbeat/completion-block conventions
// documented in plugins/sdlc/skills/execute/SKILL.md and
// classifying-and-waving-tasks.md (wave-progress phases, task-done/task-fail
// recorded by the main session, not the worker itself). The heartbeat
// snippet interpolates the actual runID so workers can copy it verbatim
// into their own wave-progress calls.
func execTaskContextReportBack(taskID, runID string) string {
	return fmt.Sprintf(
		"Emit a heartbeat as you enter each phase: execute_state({ action: \"wave-progress\", "+
			"runId: %q, taskId: %q, phase: <phase> }) for phase in %s (each once).\n\n"+
			"When finished, end your response with this completion block (blank line between each section):\n\n"+
			"```\n"+
			"Summary: <one line: what this task delivered, not how you worked>\n"+
			"\n"+
			"Files created: <comma-separated paths, or none>\n"+
			"Files modified: <comma-separated paths, or none>\n"+
			"Tests: added=<yes|no|n/a> pass=<yes|no|n/a>\n"+
			"Build: pass=<yes|no|n/a>\n"+
			"\n"+
			"VERIFY: <symbol_name> in <file_path>\n"+
			"\n"+
			"Concerns:\n"+
			"- <one bullet per concern, 3 max; omit section if none>\n"+
			"\n"+
			"Interfaces:\n"+
			"- <exported symbol added or changed; omit section if none>\n"+
			"\n"+
			"Decisions:\n"+
			"- <decision and why, one bullet each; omit section if none>\n"+
			"\n"+
			"STATUS: SUCCESS | DONE_WITH_CONCERNS | FAILED\n"+
			"```\n\n"+
			"Rules: Summary is 1 line max. Concerns/Interfaces/Decisions: omit entire section when empty. "+
			"No process narrative — do not describe how you worked, investigated, or verified. "+
			"The main session records completion via execute_state({ action: "+
			"\"task-done\" | \"task-fail\", taskId: %q, ... }). Do not call task-done/task-fail "+
			"yourself.",
		runID, taskID, strings.Join(execHeartbeatPhases, ", "), taskID,
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

func execActionTaskContext(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
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
	if err := execAssertBranch(st, branch); err != nil {
		return nil, err
	}

	// Compute waveNum unconditionally — it feeds result.Wave, sibling
	// lookup, and (when runID is empty) execDeriveRunID.
	waveNum := 0
	if in.Wave != nil {
		waveNum = *in.Wave
	} else {
		waveNum = execCurrentWaveNum(st.Data)
	}

	runID := in.RunID
	if runID == "" {
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
			return nil, &mcpserver.DomainError{
				Msg:        err.Error(),
				Suggestion: "Pass runId exactly as returned by execute_state wave-start (runId field; only letters, digits, underscore, hyphen), then retry task-context.",
				Cause:      err,
			}
		}
		return nil, &mcpserver.InfraError{Msg: "read fact sheet: " + err.Error(), Cause: err}
	}

	// Stamp contextFetchedAt on server state exactly once. A missing server
	// state (task-context called before wave-start seeded it) is not an
	// error -- a live worker's dispatch call must never fail over
	// bookkeeping absence. Once set, contextFetchedAt is never overwritten
	// by a later call.
	if s, found, lerr := wave.LoadServerState(root, runID, taskID); lerr != nil {
		return nil, &mcpserver.InfraError{Msg: "load server state for task " + taskID + ": " + lerr.Error(), Cause: lerr}
	} else if found && s.ContextFetchedAt == "" {
		s.ContextFetchedAt = waveAwaitFormat(now())
		if err := wave.StoreServerState(root, runID, taskID, s); err != nil {
			return nil, &mcpserver.InfraError{Msg: "stamp contextFetchedAt for task " + taskID + ": " + err.Error(), Cause: err}
		}
	}

	summary := execSummarizePriorWaveCtx(st.Data, root, 0, 0, 0, 0)

	// Build siblings and own file scope from the wave's planned list.
	var siblings []TaskSibling
	var ownFiles []string
	siblingsUnknown := true
	if w := execFindWave(st.Data, waveNum); w != nil {
		if planned, ok := w["planned"].([]any); ok {
			siblingsUnknown = false
			for _, entry := range planned {
				em, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				eid, _ := em["id"].(string)
				if eid == "" {
					continue
				}
				files := anyToStringSlice(em["files"])
				if eid == taskID {
					ownFiles = files
					continue // exclude self from siblings
				}
				siblings = append(siblings, TaskSibling{
					ID:    eid,
					Name:  stringOrEmpty(em["name"]),
					Files: files,
				})
			}
		}
	}

	quality, _ := st.Data["quality"].(string)

	// Carry the task's last recorded failure's resumeFrom (if any) forward:
	// into the structured field, and rendered into FactSheet immediately
	// after the header, ahead of the tail-trim below. Absent, not empty,
	// when the task never failed or failed without a harvested claim.
	var resumeFrom *ResumeFrom
	if _, tm, _ := execFindWaveTaskRow(st.Data, taskID, nil); tm != nil {
		resumeFrom = execParseResumeFrom(tm["resumeFrom"])
	}
	if resumeFrom != nil {
		section := wave.RenderResumeFromSection(resumeFrom.AcceptanceDone, resumeFrom.FilesTouched, resumeFrom.LastCompletedTask, resumeFrom.Blocker)
		content = execInsertResumeFromSection(content, section)
	}

	result := TaskContextOut{
		TaskID:          taskID,
		RunID:           runID,
		Wave:            waveNum,
		Quality:         quality,
		Siblings:        siblings,
		SiblingsUnknown: siblingsUnknown,
		FactSheet:       content,
		ResumeFrom:      resumeFrom,
		PriorWaves:      execRenderPriorWaveSummary(summary),
		Verify:          execTaskContextVerify(taskID),
		ReportBack:      execTaskContextReportBack(taskID, runID),
		ExecutionRules: &ExecutionRules{
			FileScope:       ownFiles,
			VerifyMethod:    execVerifyMethod,
			HeartbeatPhases: execHeartbeatPhases,
			ReportFormat:    execReportFormat,
		},
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
	if err := execAssertBranch(st, branch); err != nil {
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
	if err := execAssertBranch(st, branch); err != nil {
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
// must never reach os.RemoveAll: filepath.Join(root, DataDir, RunsSubdir, "")
// resolves to the runs directory ITSELF (a trailing empty Join segment
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
	if err := execAssertBranch(st, branch); err != nil {
		return nil, err
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
			runDir := filepath.Join(root, paths.DataDir, paths.RunsSubdir, runID)
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
	stateDir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)

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
	if err := execAssertBranch(st, branch); err != nil {
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
			return nil, &mcpserver.DomainError{
				Msg:        err.Error(),
				Suggestion: "Escalate the unresolved task IDs from missingIds instead of retrying: call AskUserQuestion when running at top level; when running nested or under pipelineAuto, halt the wave and return missingIds to the parent orchestrator.",
				Cause:      err,
			}
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
				if st != nil && execAssertBranch(st, branch) != nil {
					// Branch changed mid-session: skip best-effort persistence
					// rather than write the split tree under the wrong run.
					st = nil
				}
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
		if err := execAssertBranch(st, branch); err != nil {
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

// TaskProgressWithStall wraps a task's raw progress entry. StallCause is no
// longer computed here — stall/timeout classification now lives in
// wave-await (wave.ClassifyTask, server-state driven) — so the field is
// always empty and omitted from JSON. The wrapper type is kept so
// ReadProgressOut's shape is unchanged for existing callers.
type TaskProgressWithStall struct {
	wave.TaskProgress
	StallCause string `json:"stallCause,omitempty"`
}

// ReadProgressOut is the readProgress-mode response shape for the
// wave-progress action: the same top-level "tasks" key wave.Progress has
// always returned, with each entry now wrapped in TaskProgressWithStall.
type ReadProgressOut struct {
	Tasks map[string]TaskProgressWithStall `json:"tasks"`
}

// execWaveStallTimeouts resolves the heartbeat/total timeout durations fed
// into wave-await's server-state reclaim/timeout classification (see
// execute_wave_await.go). Source order: this run's
// own execute state (recorded once at init — see execActionInit) > the
// standalone-execute built-in defaults. A missing/unreadable execute state
// file is not an error here — readProgress must answer the same "never
// throw on read" contract wave.ReadProgress itself already has; it just
// falls back to the built-in defaults.
func execWaveStallTimeouts(root, branch string) (rawInterval, totalTimeout time.Duration) {
	totalSec := shipmeta.ShipBuiltInDefaults.ExecuteWaveTimeout
	intervalSec := shipmeta.ShipBuiltInDefaults.ExecuteWaveInterval
	if st, err := state.Find(root, "execute", branch); err == nil && st != nil {
		if v := execToInt(st.Data["waveTimeoutSeconds"]); v > 0 {
			totalSec = v
		}
		if v := execToInt(st.Data["waveIntervalSeconds"]); v > 0 {
			intervalSec = v
		}
	}
	return time.Duration(intervalSec) * time.Second, time.Duration(totalSec) * time.Second
}

func execActionWaveProgress(root string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.RunID == "" {
		return nil, &mcpserver.DomainError{Msg: "runId is required"}
	}

	if in.ReadProgress {
		p, err := wave.ReadProgress(root, in.RunID)
		if err != nil {
			return nil, &mcpserver.DomainError{Msg: "read progress: " + err.Error(), Cause: err}
		}

		tasks := make(map[string]TaskProgressWithStall, len(p.Tasks))
		for taskID, tp := range p.Tasks {
			tasks[taskID] = TaskProgressWithStall{TaskProgress: tp}
		}
		return ReadProgressOut{Tasks: tasks}, nil
	}

	if in.TaskID == "" {
		return nil, &mcpserver.DomainError{Msg: "taskId is required (write mode)"}
	}

	fields := wave.ProgressFields{
		AcceptanceDone: in.AcceptanceDone,
		FilesTouched:   in.FilesTouched,
		Blocker:        in.Blocker,
	}
	if err := wave.UpdateProgress(root, in.RunID, in.TaskID, in.Phase, in.LastCompletedTask, fields); err != nil {
		if errors.Is(err, wave.ErrBadRunID) || errors.Is(err, wave.ErrBadPhase) {
			return nil, &mcpserver.DomainError{
				Msg:        err.Error(),
				Suggestion: "Pass a runId matching [A-Za-z0-9_-] and a phase from started|reading|editing|verifying|reporting, then retry wave-progress.",
				Cause:      err,
			}
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

func execActionResumeReset(root, workDir string, in ExecuteStateIn, now func() time.Time) (any, error) {
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
	var seedWarnings []string

	if st != nil {
		if err := execAssertBranch(st, branch); err != nil {
			return nil, err
		}
		// Candidate task IDs are captured here, before wm["tasks"] is
		// cleared below, so the reseed loop still has each cleared task's
		// pre-reset id to work from.
		resetWaves, clearedTaskIds = execResumeResetCandidates(st.Data)

		if len(resetWaves) > 0 {
			waveSet := map[int]bool{}
			for _, n := range resetWaves {
				waveSet[n] = true
			}
			nowStamp := waveAwaitFormat(now())
			waves, _ := st.Data["waves"].([]any)
			for _, w := range waves {
				wm, ok := w.(map[string]any)
				if !ok {
					continue
				}
				waveNum := execToInt(wm["number"])
				if !waveSet[waveNum] {
					continue
				}
				preClearTasks, _ := wm["tasks"].([]any)
				wm["tasks"] = []any{}
				delete(wm, "completedAt")

				runID := in.RunID
				if runID == "" {
					runID = execDeriveRunID(st.Data, waveNum)
				}

				// Reseed fresh server state for every task this reset just
				// cleared -- a resumed run must dispatch each of them again,
				// and a dispatched task always has server state. Attempt
				// resets to 1: this is a fresh session resume, not a
				// stalled-attempt retry. Seeding failure is non-fatal.
				for _, t := range preClearTasks {
					tm, ok := t.(map[string]any)
					if !ok {
						continue
					}
					id, _ := tm["id"].(string)
					if id == "" {
						continue
					}
					workerName := execDefaultWorkerName(id)
					if prev, found, _ := wave.LoadServerState(root, runID, id); found && prev.WorkerName != "" {
						workerName = prev.WorkerName
					}
					if err := wave.DeleteServerState(root, runID, id); err != nil {
						seedWarnings = append(seedWarnings, fmt.Sprintf("resume-reset: clear server state for task %s: %s", id, err.Error()))
						continue
					}
					if err := wave.StoreServerState(root, runID, id, wave.ServerTaskState{
						DispatchedAt: nowStamp,
						WorkerName:   workerName,
						Attempt:      1,
					}); err != nil {
						seedWarnings = append(seedWarnings, fmt.Sprintf("resume-reset: seed server state for task %s: %s", id, err.Error()))
					}
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
	if len(seedWarnings) > 0 {
		out["warnings"] = seedWarnings
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

// execFindingsMaxBytes caps the size of a single worker's findings payload
// accepted by ledger_checkout. Enforced here (not at ledger_status/read
// time) so an oversized payload is rejected while still recoverable,
// instead of being persisted and then unboundedly echoed back by
// ledger_status across many workers.
const execFindingsMaxBytes = 64 * 1024 // 64 KiB

func execActionLedgerCheckout(root string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.RunID == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "runId is required",
			Suggestion: "Pass the runId of the execute run whose worker is checking out.",
		}
	}
	if in.WorkerID == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "workerId is required",
			Suggestion: "Pass the workerId that was used for ledger_checkin.",
		}
	}
	if err := execValidateSafeID(in.RunID, "runId"); err != nil {
		return nil, err
	}
	if err := execValidateSafeID(in.WorkerID, "workerId"); err != nil {
		return nil, err
	}

	dir := ledgerDir(root, in.RunID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        "mkdir ledger: " + err.Error(),
			Suggestion: "Check filesystem permissions on the project root, then retry.",
			Cause:      err,
		}
	}

	// Enforce a size cap on the findings payload at checkout time — the
	// point where it is still recoverable (the caller can trim and retry)
	// — rather than at ledger_status/read time, where it would already be
	// persisted and unreadable through the tool. Mirrors the
	// execReadMaxBytes/execx.ErrOutputCap precedent: a distinct error,
	// never a silent truncation. Cap is well above the small findings
	// payloads used by TestExecState_Ledger_FindingsRoundTrip.
	if len(in.Findings) > execFindingsMaxBytes {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("findings is %d bytes, exceeds cap of %d bytes", len(in.Findings), execFindingsMaxBytes),
			Suggestion: "Trim the findings payload, or persist it to a file under .sdlc-v2/ and reference that path instead of inlining the full content.",
		}
	}

	fp := ledgerFilePath(root, in.RunID, in.WorkerID)

	// Read existing file to preserve checkinAt/stepId. A missing file is
	// expected (first checkout for this worker); anything else (corrupt
	// JSON, permission error) must surface instead of silently becoming an
	// empty map and reporting success over lost data.
	existing := map[string]any{}
	if err := fsx.ReadJSON(fp, &existing); err != nil && !errors.Is(err, fsx.ErrNotFound) {
		return nil, &mcpserver.DomainError{
			Msg:        "read existing ledger entry: " + err.Error(),
			Suggestion: "The ledger file for this worker may be corrupted. Inspect or remove it under .sdlc-v2/, then retry checkout.",
		}
	}

	existing["status"] = "done"
	existing["checkoutAt"] = now().UTC().Format(time.RFC3339)
	if in.Findings != "" {
		existing["findings"] = in.Findings
	}

	if err := fsx.AtomicWriteJSON(fp, existing); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        "write ledger: " + err.Error(),
			Suggestion: "Check filesystem permissions on the project root, then retry.",
			Cause:      err,
		}
	}
	confirmation := map[string]any{
		"runId":      in.RunID,
		"workerId":   in.WorkerID,
		"status":     "done",
		"checkoutAt": existing["checkoutAt"],
	}
	if in.Findings != "" {
		confirmation["findings"] = in.Findings
	}
	return confirmation, nil
}

// ---------------------------------------------------------------------------
// Action: ledger_status
// ---------------------------------------------------------------------------

func execActionLedgerStatus(root string, in ExecuteStateIn, now func() time.Time) (any, error) {
	if in.RunID == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "runId is required",
			Suggestion: "Pass the runId of the execute run whose ledger status to check.",
		}
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
				"missingWorkers": missingWorkersOf(in.ExpectedWorkers, nil),
			}, nil
		}
		return nil, &mcpserver.InfraError{
			Msg:        "read ledger dir: " + err.Error(),
			Suggestion: "Check filesystem permissions on the project root, then retry.",
			Cause:      err,
		}
	}

	nowTime := now()
	var workers []any
	var stalledWorkers []string
	registered := map[string]bool{}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}

		workerID := strings.TrimSuffix(name, ".json")
		registered[workerID] = true
		fp := filepath.Join(dir, name)
		var data map[string]any
		if err := fsx.ReadJSON(fp, &data); err != nil {
			continue // skip corrupt/unreadable ledger entries gracefully
		}

		status, _ := data["status"].(string)
		checkinAt, _ := data["checkinAt"].(string)
		checkoutAt, _ := data["checkoutAt"].(string)
		stepID, _ := data["stepId"].(string)
		findings, _ := data["findings"].(string)

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
		if findings != "" {
			entry["findings"] = findings
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
		"missingWorkers": missingWorkersOf(in.ExpectedWorkers, registered),
	}, nil
}

// missingWorkersOf returns the subset of expectedWorkers not present as keys
// in registered, in the order they appear in expectedWorkers. It always
// returns a non-nil slice so callers marshal "missingWorkers" as [] rather
// than null when nothing is missing or no workers were expected.
func missingWorkersOf(expectedWorkers []string, registered map[string]bool) []string {
	missingWorkers := []string{}
	for _, ew := range expectedWorkers {
		if !registered[ew] {
			missingWorkers = append(missingWorkers, ew)
		}
	}
	return missingWorkers
}

// ---------------------------------------------------------------------------
// Action: ledger_cleanup
// ---------------------------------------------------------------------------

// execActionLedgerCleanup removes a run's entire ledger directory (every
// per-worker checkin/checkout/findings file). Callers (plan, review) invoke
// this once a run's findings have already been read out of ledger_status's
// response — the on-disk ledger files have no further use after that point.
func execActionLedgerCleanup(root string, in ExecuteStateIn) (any, error) {
	if in.RunID == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "runId is required",
			Suggestion: "Pass the runId whose ledger directory should be removed.",
		}
	}
	if err := execValidateSafeID(in.RunID, "runId"); err != nil {
		return nil, err
	}

	dir := ledgerDir(root, in.RunID)
	removed := true
	// Workers echoes which worker ids existed before deletion — per the
	// repo's mutation-contract convention that destructive operations echo
	// affected state. Deliberately IDs only, never findings payloads: a
	// findings echo here would reintroduce the unbounded-output problem
	// capped at ledger_checkout (see execFindingsMaxBytes).
	workers := []string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, &mcpserver.InfraError{
				Msg:        "stat ledger dir: " + err.Error(),
				Cause:      err,
				Suggestion: "Check filesystem permissions on .sdlc-v2/runs/ledger/ and retry.",
			}
		}
		removed = false
	} else {
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".json") {
				continue
			}
			workers = append(workers, strings.TrimSuffix(name, ".json"))
		}
		sort.Strings(workers)
	}

	if err := os.RemoveAll(dir); err != nil {
		return nil, &mcpserver.InfraError{
			Msg:        "remove ledger dir: " + err.Error(),
			Cause:      err,
			Suggestion: "Check filesystem permissions on .sdlc-v2/runs/ledger/ and retry.",
		}
	}

	return map[string]any{
		"ok":      true,
		"runId":   in.RunID,
		"removed": removed,
		"workers": workers,
	}, nil
}

// ---------------------------------------------------------------------------
// Action: log-cli
// ---------------------------------------------------------------------------

// execActionLogCLI logs a CLI execution to the evidence JSONL file.
func execActionLogCLI(root, workDir string, in ExecuteStateIn) (any, error) {
	if in.CLICommand == "" {
		return nil, &mcpserver.DomainError{
			Msg:        "cliCommand is required for log-cli",
			Suggestion: "Pass the Bash command that was executed as the cliCommand field.",
		}
	}
	branch, err := execResolveBranch(in.Branch, workDir)
	if err != nil {
		return nil, err
	}

	entry := CLIEvidenceEntry{
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Pipeline:   "execute",
		Wave:       in.Wave,
		Branch:     branch,
		Command:    in.CLICommand,
		ExitCode:   in.CLIExitCode,
		OutputHead: in.CLIOutput,
	}

	if err := appendCLIEvidence(root, entry); err != nil {
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("log-cli: %s", err.Error()), Cause: err}
	}

	return map[string]any{"ok": true, "action": "log-cli"}, nil
}
