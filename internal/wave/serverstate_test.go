package wave

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
)

func ts(now time.Time, ago time.Duration) string {
	return now.Add(-ago).Format(serverStateTimeLayout)
}

// ---------------------------------------------------------------------------
// LoadServerState / StoreServerState / DeleteServerState
// ---------------------------------------------------------------------------

func TestServerState_RoundTrip(t *testing.T) {
	root := t.TempDir()
	runID := "run1"

	want := ServerTaskState{
		DispatchedAt: "2026-01-01T00:00:00.000Z",
		WorkerName:   "worker-a",
		BatchID:      "b1",
		BatchIndex:   2,
		Attempt:      1,
	}
	if err := StoreServerState(root, runID, "3", want); err != nil {
		t.Fatalf("StoreServerState: %v", err)
	}

	got, found, err := LoadServerState(root, runID, "3")
	if err != nil {
		t.Fatalf("LoadServerState: unexpected error: %v", err)
	}
	if !found {
		t.Fatalf("LoadServerState: expected found=true after Store")
	}
	if got != want {
		t.Fatalf("LoadServerState: got %+v, want %+v", got, want)
	}

	if err := DeleteServerState(root, runID, "3"); err != nil {
		t.Fatalf("DeleteServerState: %v", err)
	}
	_, found, err = LoadServerState(root, runID, "3")
	if err != nil {
		t.Fatalf("LoadServerState after delete: unexpected error: %v", err)
	}
	if found {
		t.Fatalf("LoadServerState after delete: expected found=false")
	}
}

func TestLoadServerState_MissingFileIsNotAnError(t *testing.T) {
	root := t.TempDir()

	got, found, err := LoadServerState(root, "run1", "no-such-task")
	if err != nil {
		t.Fatalf("LoadServerState: expected nil error for missing file, got %v", err)
	}
	if found {
		t.Fatalf("LoadServerState: expected found=false for missing file")
	}
	if got != (ServerTaskState{}) {
		t.Fatalf("LoadServerState: expected zero value for missing file, got %+v", got)
	}
}

func TestDeleteServerState_MissingFileIsNotAnError(t *testing.T) {
	root := t.TempDir()
	if err := DeleteServerState(root, "run1", "no-such-task"); err != nil {
		t.Fatalf("DeleteServerState: expected nil error for missing file, got %v", err)
	}
}

func TestLoadServerState_CorruptFileReturnsErrorNamingPath(t *testing.T) {
	root := t.TempDir()
	runID := "run1"

	dir := progressDir(root, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := serverStatePath(root, runID, "3")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	_, found, err := LoadServerState(root, runID, "3")
	if err == nil {
		t.Fatalf("LoadServerState: expected error for corrupt file, got nil")
	}
	if found {
		t.Fatalf("LoadServerState: expected found=false for corrupt file")
	}
	if errors.Is(err, fsx.ErrNotFound) {
		t.Fatalf("LoadServerState: corrupt file must not classify as ErrNotFound: %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("LoadServerState error %q does not name the path %q", err.Error(), path)
	}
}

// ---------------------------------------------------------------------------
// BuildSubjects
// ---------------------------------------------------------------------------

func TestBuildSubjects_FoldsBatchIntoOneSubject(t *testing.T) {
	now := time.Now().UTC()

	states := map[string]ServerTaskState{
		"1": {BatchID: "b1", BatchIndex: 0, DispatchedAt: ts(now, 5*time.Minute), ContextFetchedAt: ts(now, 4*time.Minute), WorkerName: "worker-a"},
		"2": {BatchID: "b1", BatchIndex: 1, DispatchedAt: ts(now, 99*time.Minute), ContextFetchedAt: ts(now, 99*time.Minute), WorkerName: "worker-a"},
		"3": {BatchID: "b1", BatchIndex: 2, DispatchedAt: ts(now, 99*time.Minute), ContextFetchedAt: ts(now, 99*time.Minute), WorkerName: "worker-a"},
	}
	progress := map[string]TaskProgress{
		"1": {UpdatedAt: ts(now, 3*time.Minute)},
		"2": {UpdatedAt: ts(now, 2*time.Minute)},
		"3": {UpdatedAt: ts(now, 30*time.Second)}, // newest
	}
	openIDs := []string{"1", "2", "3"}

	subjects := BuildSubjects(states, progress, openIDs)
	if len(subjects) != 1 {
		t.Fatalf("expected exactly 1 folded subject, got %d: %+v", len(subjects), subjects)
	}
	subj := subjects[0]

	if got, want := subj.TaskIDs, []string{"1", "2", "3"}; !stringSlicesEqual(got, want) {
		t.Errorf("TaskIDs = %v, want %v (ordered by BatchIndex)", got, want)
	}
	if got, want := subj.OpenTaskIDs, []string{"1", "2", "3"}; !stringSlicesEqual(got, want) {
		t.Errorf("OpenTaskIDs = %v, want %v", got, want)
	}
	if subj.DispatchedAt != states["1"].DispatchedAt {
		t.Errorf("DispatchedAt = %q, want batchIndex 0's %q", subj.DispatchedAt, states["1"].DispatchedAt)
	}
	if subj.ContextFetchedAt != states["1"].ContextFetchedAt {
		t.Errorf("ContextFetchedAt = %q, want batchIndex 0's %q", subj.ContextFetchedAt, states["1"].ContextFetchedAt)
	}
	if subj.Liveness != progress["3"].UpdatedAt {
		t.Errorf("Liveness = %q, want newest UpdatedAt %q", subj.Liveness, progress["3"].UpdatedAt)
	}
}

func TestBuildSubjects_DropsSubjectWithNoOpenMember(t *testing.T) {
	now := time.Now().UTC()
	states := map[string]ServerTaskState{
		"1": {DispatchedAt: ts(now, 1*time.Minute), WorkerName: "worker-a"},
	}
	progress := map[string]TaskProgress{
		"1": {UpdatedAt: ts(now, 10*time.Second)},
	}

	subjects := BuildSubjects(states, progress, nil)
	if len(subjects) != 0 {
		t.Fatalf("expected no subjects when no task is open, got %+v", subjects)
	}
}

func TestBuildSubjects_BatchOneMemberFinishedOneStillLive(t *testing.T) {
	now := time.Now().UTC()

	states := map[string]ServerTaskState{
		"1": {BatchID: "b1", BatchIndex: 0, DispatchedAt: ts(now, 15*time.Minute), ContextFetchedAt: ts(now, 14*time.Minute), WorkerName: "worker-a"},
		"2": {BatchID: "b1", BatchIndex: 1, DispatchedAt: ts(now, 15*time.Minute), ContextFetchedAt: ts(now, 14*time.Minute), WorkerName: "worker-a"},
	}
	progress := map[string]TaskProgress{
		"1": {UpdatedAt: ts(now, 10*time.Minute)}, // finished 10 minutes ago
		"2": {UpdatedAt: ts(now, 10*time.Second)}, // heartbeat 10 seconds ago
	}
	openIDs := []string{"1", "2"} // both rows still open

	subjects := BuildSubjects(states, progress, openIDs)
	if len(subjects) != 1 {
		t.Fatalf("expected 1 subject, got %d", len(subjects))
	}

	verdict := ClassifyTask(subjects[0], now, 3*time.Minute, 30*time.Minute)
	if verdict != VerdictNone {
		t.Errorf("verdict = %q, want none — a live member must protect a finished one", verdict)
	}
}

func TestBuildSubjects_BatchAllStaleReturnsOneStalledVerdict(t *testing.T) {
	now := time.Now().UTC()

	states := map[string]ServerTaskState{
		"1": {BatchID: "b1", BatchIndex: 0, DispatchedAt: ts(now, 15*time.Minute), ContextFetchedAt: ts(now, 14*time.Minute), WorkerName: "worker-a"},
		"2": {BatchID: "b1", BatchIndex: 1, DispatchedAt: ts(now, 15*time.Minute), ContextFetchedAt: ts(now, 14*time.Minute), WorkerName: "worker-a"},
		"3": {BatchID: "b1", BatchIndex: 2, DispatchedAt: ts(now, 15*time.Minute), ContextFetchedAt: ts(now, 14*time.Minute), WorkerName: "worker-a"},
	}
	progress := map[string]TaskProgress{
		"1": {UpdatedAt: ts(now, 10*time.Minute)},
		"2": {UpdatedAt: ts(now, 9*time.Minute)},
		"3": {UpdatedAt: ts(now, 8*time.Minute)},
	}
	openIDs := []string{"1", "2", "3"}

	subjects := BuildSubjects(states, progress, openIDs)
	if len(subjects) != 1 {
		t.Fatalf("expected 1 subject, got %d", len(subjects))
	}
	subj := subjects[0]
	if len(subj.OpenTaskIDs) != 3 {
		t.Fatalf("expected all 3 members open on the single subject, got %v", subj.OpenTaskIDs)
	}

	verdict := ClassifyTask(subj, now, 3*time.Minute, 30*time.Minute)
	if verdict != VerdictStalled {
		t.Fatalf("verdict = %q, want stalled (one verdict covering every open member)", verdict)
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// ClassifyTask
// ---------------------------------------------------------------------------

func TestClassifyTask_NeverStarted(t *testing.T) {
	now := time.Now().UTC()
	subj := Subject{
		DispatchedAt:     ts(now, 10*time.Minute),
		ContextFetchedAt: "",
	}
	verdict := ClassifyTask(subj, now, 3*time.Minute, 30*time.Minute)
	if verdict != VerdictNeverStarted {
		t.Errorf("verdict = %q, want never-started", verdict)
	}
}

func TestClassifyTask_TimeoutEvenWithFreshHeartbeat(t *testing.T) {
	now := time.Now().UTC()
	subj := Subject{
		DispatchedAt:     ts(now, 40*time.Minute),
		ContextFetchedAt: ts(now, 39*time.Minute),
		Liveness:         ts(now, 5*time.Second), // fresh
	}
	verdict := ClassifyTask(subj, now, 3*time.Minute, 30*time.Minute)
	if verdict != VerdictTimeout {
		t.Errorf("verdict = %q, want timeout even with a fresh heartbeat", verdict)
	}
}

// TestClassifyTask_PrecedenceOrder covers one row per adjacent precedence
// pair from the Final Shape classification diagram:
//  1. timeout            vs 2. never-started
//  2. never-started      vs 3. stalled
//  3. stalled             vs 4. none
func TestClassifyTask_PrecedenceOrder(t *testing.T) {
	now := time.Now().UTC()

	cases := []struct {
		name string
		subj Subject
		want TaskVerdict
	}{
		{
			// Both "past totalTimeout" and "never fetched past heartbeatTimeout"
			// hold; timeout (rule 1) must win over never-started (rule 2).
			name: "timeout beats never-started",
			subj: Subject{
				DispatchedAt:     ts(now, 20*time.Minute),
				ContextFetchedAt: "",
			},
			want: VerdictTimeout,
		},
		{
			// Both "never fetched past heartbeatTimeout" and "stale liveness
			// past heartbeatTimeout" hold, but not timeout; never-started
			// (rule 2) must win over stalled (rule 3).
			name: "never-started beats stalled",
			subj: Subject{
				DispatchedAt:     ts(now, 10*time.Minute),
				ContextFetchedAt: "",
				Liveness:         ts(now, 10*time.Minute),
			},
			want: VerdictNeverStarted,
		},
		{
			// Fetched promptly and dispatched recently (no timeout, no
			// never-started), but liveness is stale; stalled (rule 3) must
			// win over none (rule 4).
			name: "stalled beats none",
			subj: Subject{
				DispatchedAt:     ts(now, 1*time.Minute),
				ContextFetchedAt: ts(now, 55*time.Second),
				Liveness:         ts(now, 5*time.Minute),
			},
			want: VerdictStalled,
		},
		{
			// Baseline: everything fresh -> none.
			name: "healthy falls through to none",
			subj: Subject{
				DispatchedAt:     ts(now, 1*time.Minute),
				ContextFetchedAt: ts(now, 55*time.Second),
				Liveness:         ts(now, 5*time.Second),
			},
			want: VerdictNone,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyTask(tc.subj, now, 3*time.Minute, 15*time.Minute)
			if got != tc.want {
				t.Errorf("ClassifyTask() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyTask_DeterministicOnExplicitNow(t *testing.T) {
	// ClassifyTask must be pure: calling it twice with the same explicit
	// `now` (even after a real delay) yields the same verdict.
	now := time.Now().UTC()
	subj := Subject{
		DispatchedAt:     ts(now, 10*time.Minute),
		ContextFetchedAt: "",
	}
	first := ClassifyTask(subj, now, 3*time.Minute, 30*time.Minute)
	time.Sleep(5 * time.Millisecond)
	second := ClassifyTask(subj, now, 3*time.Minute, 30*time.Minute)
	if first != second {
		t.Fatalf("ClassifyTask not deterministic on fixed now: %q vs %q", first, second)
	}
}

// Sanity check that serverStatePath and progressDir agree on directory
// layout, so LoadServerState/StoreServerState/DeleteServerState share the
// same progress/ directory as TaskProgress files.
func TestServerStatePath_SharesProgressDirWithTaskProgress(t *testing.T) {
	root := "/tmp/root"
	runID := "run1"
	got := serverStatePath(root, runID, "3")
	want := filepath.Join(progressDir(root, runID), "3.server.json")
	if got != want {
		t.Fatalf("serverStatePath = %q, want %q", got, want)
	}
}
