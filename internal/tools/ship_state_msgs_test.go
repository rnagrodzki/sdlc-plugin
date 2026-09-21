package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// TestShipState_InputErrorMsgsStartWithAction pins the Msg shape for a missing
// or invalid input: "<action>: <what is wrong>". The rendered error heading
// already names the tool, so a leading "ship_state " would only repeat it.
func TestShipState_InputErrorMsgsStartWithAction(t *testing.T) {
	cases := []struct {
		name string
		in   ShipStateIn
		want string
	}{
		{"start", ShipStateIn{Action: "start"}, "start: step is required"},
		{"complete", ShipStateIn{Action: "complete"}, "complete: step is required"},
		{"begin-step", ShipStateIn{Action: "begin-step"}, "begin-step: step is required"},
		{"complete-step", ShipStateIn{Action: "complete-step"}, "complete-step: step is required"},
		{"skip", ShipStateIn{Action: "skip"}, "skip: step is required"},
		{"fail", ShipStateIn{Action: "fail"}, "fail: step is required"},
		{"decide", ShipStateIn{Action: "decide"}, "decide: step is required"},
		{"defer", ShipStateIn{Action: "defer"}, "defer: severity, file, and title are required"},
		{"migrate", ShipStateIn{Action: "migrate"}, "migrate: from and to are required"},
		{"history_record no detail", ShipStateIn{Action: "history_record"}, "history_record: detail with run record fields is required"},
		{"history_record no skill", ShipStateIn{Action: "history_record", Detail: map[string]any{"outcome": "success"}}, "history_record: detail.skill is required"},
		{"deferred_add no detail", ShipStateIn{Action: "deferred_add"}, "deferred_add: detail with issue fields is required"},
		{"deferred_resolve no detail", ShipStateIn{Action: "deferred_resolve"}, "deferred_resolve: detail with id field is required"},
		{
			"complete-step bad outcome",
			ShipStateIn{Action: "complete-step", Step: "execute", Detail: map[string]any{"outcome": "bogus"}},
			`complete-step: outcome must be "success" or "failure"`,
		},
		{
			"gc bad dryRun",
			ShipStateIn{Action: "gc", Detail: map[string]any{"dryRun": "true"}},
			"gc: detail.dryRun must be a boolean",
		},
		{
			"begin-step bad detail level",
			ShipStateIn{Action: "begin-step", Step: "execute", Detail: map[string]any{"detail": "loud"}},
			`begin-step: detail must be "concise" or "full"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			_, err := shipState(root, root, tc.in, fixedNow(time.Now()))
			var domainErr *mcpserver.DomainError
			if !errors.As(err, &domainErr) {
				t.Fatalf("error = %v (%T), want *mcpserver.DomainError", err, err)
			}
			if !strings.HasPrefix(domainErr.Msg, tc.want) {
				t.Errorf("Msg = %q, want prefix %q", domainErr.Msg, tc.want)
			}
		})
	}
}

// failShipGC replaces the GC sweep with one that always fails and returns the
// injected cause. The filesystem cannot fail the sweep on its own here: state.GC
// errors only on a directory read error, and Find and Write read the same
// directory first and fail before the sweep starts.
func failShipGC(t *testing.T) error {
	t.Helper()
	cause := errors.New("simulated readdir failure")
	orig := shipGCFunc
	shipGCFunc = func(string, state.GCOptions) (*state.GCReport, error) { return nil, cause }
	t.Cleanup(func() { shipGCFunc = orig })
	return cause
}

// TestShipState_CleanupPipeline_GCFailureAfterStamp proves the InfraError says
// the run is already completed when only the sweep fails, and that the claim
// is true: the stamp is on disk. The Suggestion must name the action that
// retries just the sweep, because repeating cleanup-pipeline stamps again.
func TestShipState_CleanupPipeline_GCFailureAfterStamp(t *testing.T) {
	const branch = "feat/gc-fails-after-stamp"
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, branch)
	path := shipStateInitFixture(t, dir, branch)
	terminalStatus := map[string]string{
		"execute": "completed", "commit": "completed", "review": "completed",
		"received-review": "skipped", "commit-fixes": "skipped",
		"version": "completed", "pr": "completed",
	}
	for name, status := range terminalStatus {
		setStepStatus(t, path, name, status, map[string]any{"completedAt": "2026-01-01T00:00:00Z"})
	}
	cause := failShipGC(t)

	fixedTime := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	_, err := shipState(dir, dir, ShipStateIn{
		Action: "cleanup-pipeline",
		Detail: map[string]any{"branch": branch},
	}, fixedNow(fixedTime))

	var infraErr *mcpserver.InfraError
	if !errors.As(err, &infraErr) {
		t.Fatalf("error = %v (%T), want *mcpserver.InfraError", err, err)
	}
	if !errors.Is(err, cause) {
		t.Errorf("error does not wrap the sweep failure: %v", err)
	}
	if !strings.Contains(infraErr.Msg, "already marked completed") {
		t.Errorf("Msg = %q, want it to say the run is already marked completed", infraErr.Msg)
	}
	if !strings.Contains(infraErr.Msg, "gc sweep") {
		t.Errorf("Msg = %q, want it to name the failed gc sweep", infraErr.Msg)
	}
	if !strings.Contains(infraErr.Suggestion, "ship_state gc") {
		t.Errorf("Suggestion = %q, want it to name the ship_state gc action", infraErr.Suggestion)
	}

	st, findErr := state.Find(dir, "ship", branch)
	if findErr != nil || st == nil {
		t.Fatalf("state should still be findable, findErr=%v st=%v", findErr, st)
	}
	if st.Data["pipelineStatus"] != "completed" {
		t.Errorf("persisted pipelineStatus = %v, want completed", st.Data["pipelineStatus"])
	}
	if want := fixedTime.Format(time.RFC3339); st.Data["pipelineCompletedAt"] != want {
		t.Errorf("persisted pipelineCompletedAt = %v, want %s", st.Data["pipelineCompletedAt"], want)
	}
}

// TestShipState_CleanupPipeline_GCFailureWithoutStamp guards the other side:
// force and no-state-file runs never write the completed stamp, so a sweep
// failure there must not claim the run is already completed.
func TestShipState_CleanupPipeline_GCFailureWithoutStamp(t *testing.T) {
	cases := []struct {
		name      string
		withState bool
		detail    map[string]any
	}{
		{"force", true, map[string]any{"force": true}},
		{"no state file", false, map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const branch = "feat/gc-fails-no-stamp"
			dir := t.TempDir()
			initGitFixture(t, dir)
			gitCommit(t, dir, "initial")
			checkoutBranch(t, dir, branch)
			if tc.withState {
				shipStateInitFixture(t, dir, branch)
			}
			failShipGC(t)
			tc.detail["branch"] = branch

			_, err := shipState(dir, dir, ShipStateIn{Action: "cleanup-pipeline", Detail: tc.detail}, fixedNow(time.Now()))

			var infraErr *mcpserver.InfraError
			if !errors.As(err, &infraErr) {
				t.Fatalf("error = %v (%T), want *mcpserver.InfraError", err, err)
			}
			if strings.Contains(infraErr.Msg, "already marked completed") {
				t.Errorf("Msg = %q, must not claim a completed stamp that was never written", infraErr.Msg)
			}
			if !strings.Contains(infraErr.Msg, "gc sweep over") {
				t.Errorf("Msg = %q, want it to name the failed gc sweep", infraErr.Msg)
			}
			if !strings.Contains(infraErr.Suggestion, "ship_state gc") {
				t.Errorf("Suggestion = %q, want it to name the ship_state gc action", infraErr.Suggestion)
			}
			if tc.withState {
				st, findErr := state.Find(dir, "ship", branch)
				if findErr != nil || st == nil {
					t.Fatalf("state should still be findable, findErr=%v st=%v", findErr, st)
				}
				if _, stamped := st.Data["pipelineStatus"]; stamped {
					t.Errorf("pipelineStatus = %v, want key absent on the %s path", st.Data["pipelineStatus"], tc.name)
				}
			}
		})
	}
}

// TestShipState_HistoryErrorMsgsNameWriterPath pins the file path printed in
// each history InfraError. The path comes from the FileWriter accessors, so it
// must equal where the writer puts the file.
func TestShipState_HistoryErrorMsgsNameWriterPath(t *testing.T) {
	cases := []struct {
		name string
		in   ShipStateIn
		file string
	}{
		{"history_record", ShipStateIn{Action: "history_record", Detail: map[string]any{"skill": "ship", "outcome": "success"}}, "runs.jsonl"},
		{"deferred_add", ShipStateIn{Action: "deferred_add", Detail: map[string]any{"id": "d1", "description": "x"}}, "deferred.json"},
		{"deferred_resolve", ShipStateIn{Action: "deferred_resolve", Detail: map[string]any{"id": "d1"}}, "deferred.json"},
		{"deferred_list", ShipStateIn{Action: "deferred_list"}, "deferred.json"},
		{"deferred_propose_followups", ShipStateIn{Action: "deferred_propose_followups"}, "deferred.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, paths.DataDir), 0o755); err != nil {
				t.Fatalf("setup: %v", err)
			}
			// A regular file where the history directory belongs makes every
			// history read and write fail, on any platform.
			historyPath := filepath.Join(root, paths.DataDir, "history")
			if err := os.WriteFile(historyPath, []byte("x"), 0o644); err != nil {
				t.Fatalf("setup: %v", err)
			}

			_, err := shipState(root, root, tc.in, fixedNow(time.Now()))

			var infraErr *mcpserver.InfraError
			if !errors.As(err, &infraErr) {
				t.Fatalf("error = %v (%T), want *mcpserver.InfraError", err, err)
			}
			if want := filepath.Join(historyPath, tc.file); !strings.Contains(infraErr.Msg, want) {
				t.Errorf("Msg = %q, want it to contain %q", infraErr.Msg, want)
			}
		})
	}
}
