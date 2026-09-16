package wave

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
)

// serverStateTimeLayout is the timestamp layout used for every timestamp
// field on ServerTaskState (DispatchedAt, ContextFetchedAt,
// ReclaimRequestedAt) and for the progress.TaskProgress.UpdatedAt values
// BuildSubjects folds into Subject.Liveness — the same layout UpdateProgress
// writes in progress.go.
const serverStateTimeLayout = "2006-01-02T15:04:05.000Z"

// ServerTaskState is one task's server-owned dispatch and classification
// state. It is stored separately from the worker-owned TaskProgress file
// (<taskID>.json) as <taskID>.server.json in the same progress directory,
// so a stalled or misbehaving worker can never corrupt the bookkeeping the
// server relies on to classify it. The server is this file's sole writer.
type ServerTaskState struct {
	DispatchedAt       string `json:"dispatchedAt"`
	WorkerName         string `json:"workerName"`
	BatchID            string `json:"batchId,omitempty"`
	BatchIndex         int    `json:"batchIndex,omitempty"`
	ContextFetchedAt   string `json:"contextFetchedAt,omitempty"`
	ReclaimRequestedAt string `json:"reclaimRequestedAt,omitempty"`
	Attempt            int    `json:"attempt"`
}

// serverStatePath returns the absolute path of one task's server-state
// file: <root>/.sdlc-v2/runs/<runID>/progress/<taskID>.server.json.
func serverStatePath(root, runID, taskID string) string {
	return filepath.Join(progressDir(root, runID), taskID+".server.json")
}

// LoadServerState reads one task's server-state file. A missing file is not
// an error: it returns a zero ServerTaskState, found=false, and a nil
// error. Any other read or parse failure (including a corrupt file) is
// returned as a non-nil error naming the path — callers must not treat that
// case as "missing".
func LoadServerState(root, runID, taskID string) (ServerTaskState, bool, error) {
	if err := validateRunID(runID); err != nil {
		return ServerTaskState{}, false, err
	}

	var s ServerTaskState
	err := fsx.ReadJSON(serverStatePath(root, runID, taskID), &s)
	if err == nil {
		return s, true, nil
	}
	if errors.Is(err, fsx.ErrNotFound) {
		return ServerTaskState{}, false, nil
	}
	return ServerTaskState{}, false, err
}

// StoreServerState writes one task's server-state file atomically, creating
// the progress directory if needed.
func StoreServerState(root, runID, taskID string, s ServerTaskState) error {
	if err := validateRunID(runID); err != nil {
		return err
	}

	dir := progressDir(root, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("wave: mkdir %s: %w", dir, err)
	}
	return fsx.AtomicWriteJSON(serverStatePath(root, runID, taskID), s)
}

// DeleteServerState removes one task's server-state file. A missing file is
// not an error.
func DeleteServerState(root, runID, taskID string) error {
	if err := validateRunID(runID); err != nil {
		return err
	}

	path := serverStatePath(root, runID, taskID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("wave: remove %s: %w", path, err)
	}
	return nil
}

// TaskVerdict is ClassifyTask's classification result.
type TaskVerdict string

const (
	// VerdictNone means the subject's heartbeat and dispatch timing are
	// healthy — no action needed.
	VerdictNone TaskVerdict = ""
	// VerdictNeverStarted means the subject was dispatched but never
	// fetched its fact sheet within heartbeatTimeout — it may have never
	// actually started running.
	VerdictNeverStarted TaskVerdict = "never-started"
	// VerdictStalled means the subject started but has gone silent for
	// longer than heartbeatTimeout.
	VerdictStalled TaskVerdict = "stalled"
	// VerdictTimeout means the subject has been dispatched for longer
	// than totalTimeout, regardless of heartbeat freshness.
	VerdictTimeout TaskVerdict = "timeout"
)

// Subject is what ClassifyTask judges: one solo task, or one whole batch. A
// batch agent returns once, after every member, so its members are never
// independently alive and must not be judged independently — BuildSubjects
// groups every open row sharing a batchId into a single Subject before
// classification.
type Subject struct {
	// TaskIDs lists every member (one element for a solo task), ordered by
	// BatchIndex.
	TaskIDs []string
	// OpenTaskIDs is the subset of TaskIDs whose manifest row is still
	// open. The verdict ClassifyTask returns applies to every ID here.
	OpenTaskIDs []string
	// WorkerName, DispatchedAt, and ContextFetchedAt come from batchIndex
	// 0's server state — every member of a batch shares one dispatch.
	WorkerName       string
	DispatchedAt     string
	ContextFetchedAt string
	// Liveness is the newest TaskProgress.UpdatedAt across every member,
	// open or already recorded — a live later member protects an earlier,
	// already-finished one from being misclassified as stalled.
	Liveness string
	// ReclaimRequested and Attempt come from batchIndex 0's server state.
	ReclaimRequested string
	Attempt          int
}

// newerTimestamp returns whichever of a, b is chronologically later,
// parsing with serverStateTimeLayout. An empty argument loses to a
// non-empty one. If either value fails to parse, it falls back to a plain
// string comparison — safe because every writer uses the same fixed-width,
// zero-padded, UTC layout, which sorts lexicographically in time order.
func newerTimestamp(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	ta, errA := time.Parse(serverStateTimeLayout, a)
	tb, errB := time.Parse(serverStateTimeLayout, b)
	if errA != nil || errB != nil {
		if a > b {
			return a
		}
		return b
	}
	if ta.After(tb) {
		return a
	}
	return b
}

// BuildSubjects groups server states by BatchID (solo tasks, whose BatchID
// is empty, each form their own single-member group) and folds in progress
// records to compute each group's Liveness. openIDs is the wave's still-open
// manifest rows; a group with no open member is dropped and never appears
// in the result, so it never reaches ClassifyTask.
//
// Only task IDs present in states are considered — a task with no recorded
// server state has never been dispatched and has nothing to classify.
func BuildSubjects(states map[string]ServerTaskState, progress map[string]TaskProgress, openIDs []string) []Subject {
	openSet := make(map[string]bool, len(openIDs))
	for _, id := range openIDs {
		openSet[id] = true
	}

	taskIDs := make([]string, 0, len(states))
	for id := range states {
		taskIDs = append(taskIDs, id)
	}
	sort.Strings(taskIDs)

	groupKeyOf := func(id string) string {
		if bid := states[id].BatchID; bid != "" {
			return "batch:" + bid
		}
		return "solo:" + id
	}

	var groupOrder []string
	members := map[string][]string{}
	for _, id := range taskIDs {
		key := groupKeyOf(id)
		if _, seen := members[key]; !seen {
			groupOrder = append(groupOrder, key)
		}
		members[key] = append(members[key], id)
	}

	subjects := make([]Subject, 0, len(groupOrder))
	for _, key := range groupOrder {
		ids := members[key]
		sort.Slice(ids, func(i, j int) bool {
			return states[ids[i]].BatchIndex < states[ids[j]].BatchIndex
		})

		var openIDsInGroup []string
		liveness := ""
		for _, id := range ids {
			if openSet[id] {
				openIDsInGroup = append(openIDsInGroup, id)
			}
			liveness = newerTimestamp(liveness, progress[id].UpdatedAt)
		}
		if len(openIDsInGroup) == 0 {
			continue
		}

		first := states[ids[0]]
		subjects = append(subjects, Subject{
			TaskIDs:          ids,
			OpenTaskIDs:      openIDsInGroup,
			WorkerName:       first.WorkerName,
			DispatchedAt:     first.DispatchedAt,
			ContextFetchedAt: first.ContextFetchedAt,
			Liveness:         liveness,
			ReclaimRequested: first.ReclaimRequestedAt,
			Attempt:          first.Attempt,
		})
	}

	return subjects
}

// ClassifyTask replaces ClassifyStall. now is passed in explicitly rather
// than read via time.Now(), keeping classification pure and deterministic
// for tests. Precedence is strict and the order below is the order
// evaluated; the first matching condition wins, and the returned verdict
// applies to every ID in subj.OpenTaskIDs. A zero timeout (heartbeatTimeout
// or totalTimeout) disables the check it would otherwise gate, mirroring
// ClassifyStall's zero-timeout semantics.
//
//  1. age(subj.DispatchedAt) > totalTimeout                       -> timeout
//  2. subj.ContextFetchedAt == "" && age(subj.DispatchedAt) > heartbeatTimeout -> never-started
//  3. subj.Liveness != "" && age(subj.Liveness) > heartbeatTimeout -> stalled
//  4. otherwise                                                    -> none
func ClassifyTask(subj Subject, now time.Time, heartbeatTimeout, totalTimeout time.Duration) TaskVerdict {
	dispatched, dispatchedErr := time.Parse(serverStateTimeLayout, subj.DispatchedAt)

	if totalTimeout > 0 && dispatchedErr == nil && now.Sub(dispatched) > totalTimeout {
		return VerdictTimeout
	}

	if subj.ContextFetchedAt == "" && heartbeatTimeout > 0 && dispatchedErr == nil && now.Sub(dispatched) > heartbeatTimeout {
		return VerdictNeverStarted
	}

	if subj.Liveness != "" && heartbeatTimeout > 0 {
		if liveness, err := time.Parse(serverStateTimeLayout, subj.Liveness); err == nil && now.Sub(liveness) > heartbeatTimeout {
			return VerdictStalled
		}
	}

	return VerdictNone
}
