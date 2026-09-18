package tools

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/stepper"
	"github.com/rnagrodzki/sdlc-plugin/internal/wave"
)

// waveAwaitTimeLayout mirrors the unexported serverStateTimeLayout used by
// package wave (internal/wave/serverstate.go) for ServerTaskState and
// TaskProgress timestamps. It must stay byte-identical to that layout so
// string-lexical fallback comparisons (waveAwaitTimeAfter below) stay valid
// across both packages.
const waveAwaitTimeLayout = "2006-01-02T15:04:05.000Z"

// ---------------------------------------------------------------------------
// Contract (Final Shape, plan i-want-to-simplify-magical-fox.md)
// ---------------------------------------------------------------------------

// ResumeFrom carries what a failed worker had recorded in its own progress
// file at the moment task-fail harvested it. It is advisory
// only: it never reduces a retry's scope, it only tells the redispatched
// attempt (or the human escalated to) what was already done.
type ResumeFrom struct {
	AcceptanceDone    []int    `json:"acceptanceDone"`
	FilesTouched      []string `json:"filesTouched"`
	LastCompletedTask string   `json:"lastCompletedTask,omitempty"`
	Blocker           string   `json:"blocker,omitempty"`
}

// WaveAwaitFailure is one still-open task row that wave-await has just
// classified as unable to make further progress on its own: it timed out,
// or it went quiet and never answered its reclaim. A worker that DOES
// answer is not a failure and never appears here.
type WaveAwaitFailure struct {
	TaskID      string `json:"taskId"`
	WorkerName  string `json:"workerName"`
	Cause       string `json:"cause"`       // TIMEOUT | STALLED_NO_REPLY
	Attempt     int    `json:"attempt"`     // the attempt that just failed
	RetriesLeft int    `json:"retriesLeft"` // 0 means terminal, escalate to the user
}

// WaveAwaitOut is the result of one non-blocking wave-await probe.
type WaveAwaitOut struct {
	stepper.Envelope        // status | step | llm_decision | state_file | progress | ext | error
	Next             string `json:"next"` // no omitempty — guardrail mcp-output-drives-behavior
}

// waveAwaitReclaimRequest is one ext.reclaimRequests entry. RULING: the
// plan's illustrative JSONC shows a singular "taskId" field, but the same
// section's prose is explicit that reclaim emits "ONE reclaimRequests entry
// per subject ... naming every open task the agent still owes" — a batch
// subject can owe several open task IDs to one worker, which a singular
// field cannot represent. ext is untyped (map[string]any) in the Contract,
// so only WaveAwaitFailure/ResumeFrom/WaveAwaitOut are fixed by "exactly as
// declared"; this shape is this handler's own choice, kept plural
// (TaskIDs) to satisfy the "one entry per subject, naming every open task"
// requirement literally rather than the illustrative single-task example.
type waveAwaitReclaimRequest struct {
	TaskIDs    []string `json:"taskIds"`
	WorkerName string   `json:"workerName"`
	Message    string   `json:"message"`
}

// waveAwaitProgress is the Envelope.Progress payload shape for this action,
// matching the plan's illustrative JSONC fields. RULING: "queued" is not
// defined anywhere in the authoritative Classification-and-reclaim diagram
// (Design correction 2 explicitly rejects per-member queued rules), so this
// handler defines it itself: open, planned task IDs that have no server
// state file at all, i.e. never dispatched. BuildSubjects silently drops
// exactly these IDs ("nothing to classify"), which would otherwise be a
// hole in KD10's termination argument (no dispatchedAt means no
// totalTimeout bound) — "queued" is the visibility signal that closes it,
// and a non-empty queued bucket drives wave-await's "dispatch" next
// instruction.
type waveAwaitProgress struct {
	Iteration       int      `json:"iteration"`
	WaitedSeconds   int      `json:"waitedSeconds"`
	TimeoutSeconds  int      `json:"timeoutSeconds"`
	IntervalSeconds int      `json:"intervalSeconds"`
	Open            []string `json:"open"`
	Queued          []string `json:"queued"`
	Stalled         []string `json:"stalled"`
	Recorded        []string `json:"recorded"`
}

// waveAwaitPollState is the resume-state persisted at in.StateFile across
// bounded-poll calls (KD8). Unlike stepper.PollState, it carries no timeout
// budget of its own — KD10 forbids a wall-clock budget on the wave-await
// loop itself, so this only threads the iteration counter and the first
// call's timestamp through for the progress block. It is keyed by
// runId+wave so a stale state_file left over from a prior wave (or a
// mismatched runId) is detected and discarded rather than silently
// misapplied to this call.
type waveAwaitPollState struct {
	RunID     string `json:"runId"`
	Wave      int    `json:"wave"`
	StartedAt string `json:"startedAt"`
	Iteration int    `json:"iteration"`
}

// ---------------------------------------------------------------------------
// execActionWaveAwait
// ---------------------------------------------------------------------------

// execActionWaveAwait is one non-blocking probe of a wave's still-open task
// rows. It replaces the old dual stall-detection (ClassifyStall/StallCause
// plus ledger_status) with a single server-driven classification pass
// (wave.BuildSubjects / wave.ClassifyTask) and a bounded reclaim state
// machine for stalled or never-started workers, returning an explicit
// "next" instruction the calling skill must follow verbatim rather than
// re-deriving its own supervision logic.
//
// There is no independent poll budget: status is "done" exactly when the
// wave has no task row still blocking progress (see the blocking-count
// computation below), never on a wall-clock deadline. Every open row is
// independently bounded by totalTimeout from its own dispatchedAt (KD10).
func execActionWaveAwait(root string, in ExecuteStateIn, now func() time.Time) (WaveAwaitOut, error) {
	if in.RunID == "" {
		return WaveAwaitOut{}, &mcpserver.DomainError{
			Msg:        "wave-await requires runId",
			Suggestion: `Pass runId, e.g. execute_state {action:"wave-await", runId:"<runId>", wave:<n>}.`,
		}
	}
	if in.Wave == nil {
		return WaveAwaitOut{}, &mcpserver.DomainError{
			Msg:        "wave-await requires wave",
			Suggestion: `Pass wave, e.g. execute_state {action:"wave-await", runId:"<runId>", wave:<n>}.`,
		}
	}
	waveNum := *in.Wave

	branch, err := execResolveBranch(in.Branch, root)
	if err != nil {
		return WaveAwaitOut{}, err
	}
	st, err := execFindState(root, branch)
	if err != nil {
		return WaveAwaitOut{}, err
	}
	if err := execAssertBranch(st, branch); err != nil {
		return WaveAwaitOut{}, err
	}

	w := execFindWave(st.Data, waveNum)
	if w == nil {
		return WaveAwaitOut{}, &mcpserver.DataError{
			Msg:        fmt.Sprintf("wave %d not found for branch %q", waveNum, branch),
			Suggestion: fmt.Sprintf(`Call execute_state {action:"wave-start", wave:%d} first.`, waveNum),
		}
	}

	// Still-open row detection (Design correction 1): planned is the
	// complete manifest seeded once at wave-start; tasks holds only closed
	// rows, upserted by task-done/task-fail. A planned ID absent from tasks,
	// or present with status "in_progress" (a task-redispatch reopening),
	// is open. Everything else is recorded.
	plannedIDs := waveAwaitPlannedIDs(w)
	closedStatus := waveAwaitClosedStatus(w)
	openIDs := []string{}
	recordedIDs := []string{}
	for _, id := range plannedIDs {
		status, present := closedStatus[id]
		if !present || status == "in_progress" {
			openIDs = append(openIDs, id)
		} else {
			recordedIDs = append(recordedIDs, id)
		}
	}

	rawInterval, totalTimeout := execWaveStallTimeouts(root, branch)
	heartbeatTimeout := 10 * rawInterval
	reclaimGrace := 5 * rawInterval
	if reclaimGrace < 300*time.Second {
		reclaimGrace = 300 * time.Second
	}
	intervalSeconds := int(rawInterval / time.Second)
	if intervalSeconds <= 0 {
		intervalSeconds = 60
	}

	nowT := now()

	// Server state is loaded for every planned task, not just the open
	// ones: BuildSubjects derives a batch's dispatchedAt/contextFetchedAt/
	// reclaimRequestedAt/attempt from batchIndex 0's file and folds every
	// member's progress into Liveness, and batchIndex 0 (or a live later
	// member) may already be a closed/recorded row. Loading only open IDs
	// would silently drop those closed members from the group, corrupting
	// both the reclaim-stamp round-trip (see waveAwaitStampReclaim) and the
	// batch-liveness protection Design correction 2 requires.
	states := map[string]wave.ServerTaskState{}
	for _, id := range plannedIDs {
		s, found, lerr := wave.LoadServerState(root, in.RunID, id)
		if lerr != nil {
			return WaveAwaitOut{}, &mcpserver.InfraError{Msg: "load server state for task " + id + ": " + lerr.Error(), Cause: lerr}
		}
		if found {
			states[id] = s
		}
	}
	queuedIDs := []string{}
	for _, id := range openIDs {
		if _, ok := states[id]; !ok {
			queuedIDs = append(queuedIDs, id)
		}
	}

	progress, err := wave.ReadProgress(root, in.RunID)
	if err != nil {
		return WaveAwaitOut{}, &mcpserver.InfraError{Msg: "read progress: " + err.Error(), Cause: err}
	}

	subjects := wave.BuildSubjects(states, progress.Tasks, openIDs)

	openBucket := []string{}
	stalledBucket := []string{}
	failures := []WaveAwaitFailure{}
	reclaimRequests := []waveAwaitReclaimRequest{}
	failureClauses := []string{}
	reclaimClauses := []string{}
	blocking := false

	nowStr := waveAwaitFormat(nowT)

	for _, subj := range subjects {
		verdict := wave.ClassifyTask(subj, nowT, heartbeatTimeout, totalTimeout)

		switch {
		case verdict == wave.VerdictTimeout:
			// Bullet 4: timeout wins outright, no reclaim attempt, even if a
			// reclaim was already in flight for this subject.
			stalledBucket = append(stalledBucket, subj.OpenTaskIDs...)
			for _, id := range subj.OpenTaskIDs {
				f := WaveAwaitFailure{
					TaskID:      id,
					WorkerName:  subj.WorkerName,
					Cause:       "TIMEOUT",
					Attempt:     subj.Attempt,
					RetriesLeft: waveAwaitRetriesLeft(subj.Attempt),
				}
				failures = append(failures, f)
				failureClauses = append(failureClauses, waveAwaitFailureClause(f, in.RunID, waveNum, intervalSeconds))
				if f.RetriesLeft > 0 {
					blocking = true
				}
			}

		case subj.ReclaimRequested != "":
			// Reclaim already in flight for this subject (evaluated
			// regardless of the current verdict — a worker that answers
			// becomes healthy again, and the harvest must still run).
			replied := subj.Liveness != "" && waveAwaitTimeAfter(subj.Liveness, subj.ReclaimRequested)
			if replied {
				// The reply IS the liveness proof. Clear the stamp and let the
				// SAME attempt continue: no failure, no TaskStop, no retry
				// consumed.
				if err := waveAwaitClearReclaim(root, in.RunID, subj.TaskIDs); err != nil {
					return WaveAwaitOut{}, &mcpserver.InfraError{Msg: "clear reclaim stamp: " + err.Error(), Cause: err}
				}
				openBucket = append(openBucket, subj.OpenTaskIDs...)
				blocking = true
			} else if waveAwaitAge(nowT, subj.ReclaimRequested) > reclaimGrace {
				stalledBucket = append(stalledBucket, subj.OpenTaskIDs...)
				for _, id := range subj.OpenTaskIDs {
					f := WaveAwaitFailure{
						TaskID:      id,
						WorkerName:  subj.WorkerName,
						Cause:       "STALLED_NO_REPLY",
						Attempt:     subj.Attempt,
						RetriesLeft: waveAwaitRetriesLeft(subj.Attempt),
					}
					failures = append(failures, f)
					failureClauses = append(failureClauses, waveAwaitFailureClause(f, in.RunID, waveNum, intervalSeconds))
					if f.RetriesLeft > 0 {
						blocking = true
					}
				}
				// Stamp is deliberately NOT cleared here: clearing it would
				// let the next call re-stamp and restart the grace window,
				// which is exactly the double-consume the "duplicate calls
				// don't double-consume reclaimGrace" requirement forbids.
				// It is left in place until Task 4's task-redispatch or
				// task-fail resets the task's server state.
			} else {
				// Still within grace, no reply yet: stay blocking, do not
				// re-relay the SendMessage (it was already relayed the
				// call that wrote the stamp).
				stalledBucket = append(stalledBucket, subj.OpenTaskIDs...)
				blocking = true
			}

		case verdict == wave.VerdictNeverStarted || verdict == wave.VerdictStalled:
			// Bullet 1: first time this subject is seen unhealthy — stamp
			// the reclaim request and relay it. Stamped on every member
			// (open or already closed), not only TaskIDs[0]: BuildSubjects
			// always reads ReclaimRequestedAt back from batchIndex 0's
			// server file, which may already be a closed/recorded member
			// for a batch mid-sequence. Stamping only the open members
			// would leave that field permanently empty and the reclaim
			// would never be detected on the next call.
			if err := waveAwaitStampReclaim(root, in.RunID, subj.TaskIDs, nowStr); err != nil {
				return WaveAwaitOut{}, &mcpserver.InfraError{Msg: "stamp reclaim request: " + err.Error(), Cause: err}
			}
			stalledBucket = append(stalledBucket, subj.OpenTaskIDs...)
			rr := waveAwaitReclaimRequest{
				TaskIDs:    append([]string{}, subj.OpenTaskIDs...),
				WorkerName: subj.WorkerName,
				Message:    waveAwaitReclaimMessage(in.RunID, subj.OpenTaskIDs),
			}
			reclaimRequests = append(reclaimRequests, rr)
			reclaimClauses = append(reclaimClauses, waveAwaitReclaimClause(rr, intervalSeconds))
			blocking = true

		default:
			openBucket = append(openBucket, subj.OpenTaskIDs...)
			blocking = true
		}
	}

	if len(queuedIDs) > 0 {
		blocking = true
	}

	sort.Strings(openBucket)
	sort.Strings(stalledBucket)
	sort.Strings(recordedIDs)
	sort.Strings(queuedIDs)

	status := "pending"
	if !blocking {
		status = "done"
	}

	ps, statePath, err := waveAwaitPollFile(in, in.RunID, waveNum, nowT)
	if err != nil {
		return WaveAwaitOut{}, err
	}

	startedAt, ok := waveAwaitParse(ps.StartedAt)
	waitedSeconds := 0
	if ok {
		waitedSeconds = int(nowT.Sub(startedAt).Seconds())
	}

	progressPayload := waveAwaitProgress{
		Iteration:       ps.Iteration,
		WaitedSeconds:   waitedSeconds,
		TimeoutSeconds:  int(totalTimeout / time.Second),
		IntervalSeconds: intervalSeconds,
		Open:            openBucket,
		Queued:          queuedIDs,
		Stalled:         stalledBucket,
		Recorded:        recordedIDs,
	}

	ext := map[string]any{
		"failed":          failures,
		"reclaimRequests": reclaimRequests,
	}

	next := waveAwaitBuildNext(status, waveNum, in.RunID, statePath, intervalSeconds, failureClauses, reclaimClauses, queuedIDs)

	step := "wave-await"
	env := stepper.Envelope{
		Status:    status,
		Step:      &step,
		StateFile: waveAwaitStrPtr(statePath),
		Progress:  progressPayload,
		Ext:       ext,
	}

	return WaveAwaitOut{Envelope: env, Next: next}, nil
}

// ---------------------------------------------------------------------------
// Manifest helpers
// ---------------------------------------------------------------------------

// waveAwaitPlannedIDs reads w["planned"] — the complete task manifest seeded
// once at wave-start — and returns every task ID in seeding order.
func waveAwaitPlannedIDs(w map[string]any) []string {
	planned, _ := w["planned"].([]any)
	ids := make([]string, 0, len(planned))
	for _, p := range planned {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		id, _ := pm["id"].(string)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// waveAwaitClosedStatus reads w["tasks"] — closed rows only, upserted by
// task-done/task-fail — into a taskID -> status lookup.
func waveAwaitClosedStatus(w map[string]any) map[string]string {
	tasks, _ := w["tasks"].([]any)
	out := map[string]string{}
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
		out[id] = status
	}
	return out
}

// ---------------------------------------------------------------------------
// Classification support
// ---------------------------------------------------------------------------

// waveAwaitRetriesLeft implements the 2-retry ceiling: attempt 1 is the
// initial dispatch, 2 and 3 are retries, so at most 3 attempts total.
// attempt is the attempt number that just failed.
func waveAwaitRetriesLeft(attempt int) int {
	left := 3 - attempt
	if left < 0 {
		return 0
	}
	return left
}

// waveAwaitHarvest copies a worker's own progress record into a ResumeFrom,
// guaranteeing non-nil slices (guardrail handler-data-contracts).
func waveAwaitHarvest(tp wave.TaskProgress) ResumeFrom {
	rf := ResumeFrom{
		AcceptanceDone:    tp.AcceptanceDone,
		FilesTouched:      tp.FilesTouched,
		LastCompletedTask: tp.LastCompletedTask,
		Blocker:           tp.Blocker,
	}
	if rf.AcceptanceDone == nil {
		rf.AcceptanceDone = []int{}
	}
	if rf.FilesTouched == nil {
		rf.FilesTouched = []string{}
	}
	return rf
}

// waveAwaitStampReclaim writes reclaimRequestedAt = ts to every named
// task's server-state file.
func waveAwaitStampReclaim(root, runID string, taskIDs []string, ts string) error {
	for _, id := range taskIDs {
		s, found, err := wave.LoadServerState(root, runID, id)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		s.ReclaimRequestedAt = ts
		if err := wave.StoreServerState(root, runID, id, s); err != nil {
			return err
		}
	}
	return nil
}

// waveAwaitClearReclaim clears reclaimRequestedAt on every named task's
// server-state file (called once a reclaim harvest has completed).
func waveAwaitClearReclaim(root, runID string, taskIDs []string) error {
	for _, id := range taskIDs {
		s, found, err := wave.LoadServerState(root, runID, id)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		s.ReclaimRequestedAt = ""
		if err := wave.StoreServerState(root, runID, id, s); err != nil {
			return err
		}
	}
	return nil
}

// waveAwaitParse parses a timestamp in waveAwaitTimeLayout.
func waveAwaitParse(s string) (time.Time, bool) {
	t, err := time.Parse(waveAwaitTimeLayout, s)
	return t, err == nil
}

// waveAwaitFormat formats t in waveAwaitTimeLayout (UTC), matching every
// other writer of ServerTaskState/TaskProgress timestamps.
func waveAwaitFormat(t time.Time) string {
	return t.UTC().Format(waveAwaitTimeLayout)
}

// waveAwaitTimeAfter reports whether a is chronologically after b. Mirrors
// wave.newerTimestamp's parse-then-lexical-fallback strategy (that helper
// is unexported so it cannot be called directly from this package) — safe
// because every writer uses the same fixed-width, zero-padded, UTC layout,
// which sorts lexicographically in time order.
func waveAwaitTimeAfter(a, b string) bool {
	if a == "" {
		return false
	}
	if b == "" {
		return true
	}
	ta, errA := time.Parse(waveAwaitTimeLayout, a)
	tb, errB := time.Parse(waveAwaitTimeLayout, b)
	if errA != nil || errB != nil {
		return a > b
	}
	return ta.After(tb)
}

// waveAwaitAge returns now - ts. An unparseable ts returns 0 (treated as
// "just happened") rather than risking a spurious grace-exceeded verdict
// from a corrupt timestamp.
func waveAwaitAge(now time.Time, ts string) time.Duration {
	t, ok := waveAwaitParse(ts)
	if !ok {
		return 0
	}
	d := now.Sub(t)
	if d < 0 {
		return 0
	}
	return d
}

// ---------------------------------------------------------------------------
// next-text construction
// ---------------------------------------------------------------------------

// waveAwaitReclaimMessage is the verbatim SendMessage body for a subject
// that has just been stamped for reclaim.
func waveAwaitReclaimMessage(runID string, taskIDs []string) string {
	return fmt.Sprintf(
		"Liveness check for task(s) %s. You have been quiet past the heartbeat threshold. "+
			"Do NOT stop and do NOT abandon your work. Immediately call execute_state "+
			"{action:\"wave-progress\", runId:%q, taskId:\"<taskId>\", phase:<phase, one of %s>, "+
			"acceptanceDone:[...], filesTouched:[...], lastCompletedTask:\"...\"} for each task "+
			"listed, then continue exactly where you left off.",
		strings.Join(taskIDs, ", "), runID, strings.Join(execHeartbeatPhases, ", "),
	)
}

// waveAwaitReclaimClause builds the next-text instruction for a freshly
// stamped reclaim request.
func waveAwaitReclaimClause(rr waveAwaitReclaimRequest, intervalSeconds int) string {
	return fmt.Sprintf(
		"SendMessage %s the message in ext.reclaimRequests for task(s) %s verbatim. "+
			"Then call wave-await again after %ds.",
		rr.WorkerName, strings.Join(rr.TaskIDs, ", "), intervalSeconds,
	)
}

// waveAwaitFailureClause builds the next-text instruction for one failed
// task. Order is always TaskStop -> task-fail -> (task-redispatch ->
// dispatch -> wave-await) when retries remain. This ordering is unchanged
// by Task 1's spike finding that TaskStop against a nested background
// agent can fail with a hard ownership/authorization error in this repo's
// topology: the JSON instruction still literally orders TaskStop first, but
// the added clause tells the calling skill that an ownership-error failure
// from TaskStop is an expected fallback here, not a fatal condition.
func waveAwaitFailureClause(f WaveAwaitFailure, runID string, waveNum, intervalSeconds int) string {
	stop := fmt.Sprintf(
		"TaskStop %s and confirm it stopped (if TaskStop fails with an ownership/authorization "+
			"error because the worker is a nested background agent, treat that as an expected "+
			"fallback and proceed anyway). Then execute_state {action:\"task-fail\", wave:%d, "+
			"taskId:%q, error:%q}.",
		f.WorkerName, waveNum, f.TaskID, f.Cause,
	)
	if f.RetriesLeft > 0 {
		return stop + fmt.Sprintf(
			" Then execute_state {action:\"task-redispatch\", runId:%q, taskId:%q} which "+
				"re-opens the row, then dispatch task %s again. Then call wave-await again "+
				"after %ds — the retry is supervised like any other worker. Do NOT run the "+
				"wave gates yet.",
			runID, f.TaskID, f.TaskID, intervalSeconds,
		)
	}
	return stop + fmt.Sprintf(
		" Task %s is out of retries — escalate to the user per recovering-from-failures.md.",
		f.TaskID,
	)
}

// waveAwaitBuildNext assembles the full "next" instruction from every
// applicable clause, in priority order: failures, reclaim relays, queued
// dispatches, then either the generic resume call (pending) or the
// wave-gates instruction (done).
func waveAwaitBuildNext(status string, waveNum int, runID, statePath string, intervalSeconds int, failureClauses, reclaimClauses []string, queuedIDs []string) string {
	var parts []string
	parts = append(parts, failureClauses...)
	parts = append(parts, reclaimClauses...)

	if len(queuedIDs) > 0 {
		parts = append(parts, fmt.Sprintf(
			"Dispatch task(s) %s — not yet dispatched. Then call wave-await again after %ds.",
			strings.Join(queuedIDs, ", "), intervalSeconds,
		))
	}

	if status == "done" {
		parts = append(parts, fmt.Sprintf(
			"Every task in wave %d is recorded. Run the wave gates (spec reviewer, guardrail "+
				"gate) and call execute_state {action:\"wave-done\", wave:%d}.",
			waveNum, waveNum,
		))
		return strings.Join(parts, " ")
	}

	parts = append(parts, fmt.Sprintf(
		"Call execute_state {action:\"wave-await\", runId:%q, wave:%d, stateFile:%q} again "+
			"after %ds. Do not compute elapsed time yourself.",
		runID, waveNum, statePath, intervalSeconds,
	))
	return strings.Join(parts, " ")
}

// waveAwaitStrPtr returns nil for an empty string, or a pointer to s —
// mirrors stepper's own unexported strPtr, duplicated here because that
// helper is not exported outside package stepper.
func waveAwaitStrPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ---------------------------------------------------------------------------
// Bounded-poll resume file (KD8, KD10: no timeout budget of its own)
// ---------------------------------------------------------------------------

// waveAwaitPollFile loads (or starts fresh) the resume-state persisted at
// in.StateFile, keyed by runId+wave so a state file left over from a
// different run or an earlier wave is detected and discarded rather than
// silently reused. now is only used to seed startedAt on a fresh state; it
// is never used to gate status (KD10).
func waveAwaitPollFile(in ExecuteStateIn, runID string, waveNum int, nowT time.Time) (waveAwaitPollState, string, error) {
	statePath := in.StateFile
	if statePath == "" {
		p, err := stepper.NewStateFilePath("wave-await")
		if err != nil {
			return waveAwaitPollState{}, "", &mcpserver.InfraError{Msg: "create wave-await state file: " + err.Error(), Cause: err}
		}
		statePath = p
	}

	var ps waveAwaitPollState
	loaded := false
	if in.StateFile != "" {
		if err := fsx.ReadJSON(statePath, &ps); err == nil {
			loaded = true
		}
	}
	if !loaded || ps.RunID != runID || ps.Wave != waveNum {
		ps = waveAwaitPollState{
			RunID:     runID,
			Wave:      waveNum,
			StartedAt: waveAwaitFormat(nowT),
			Iteration: 0,
		}
	}
	ps.Iteration++

	if err := fsx.AtomicWriteJSON(statePath, ps); err != nil {
		return waveAwaitPollState{}, "", &mcpserver.InfraError{Msg: "save wave-await state file: " + err.Error(), Cause: err}
	}
	return ps, statePath, nil
}
