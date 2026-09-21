package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// execErrParts returns the mcpserver error class, message and Suggestion of err.
func execErrParts(err error) (class, msg, suggestion string) {
	var de *mcpserver.DomainError
	var ie *mcpserver.InfraError
	var dae *mcpserver.DataError
	switch {
	case errors.As(err, &de):
		return "domain", de.Msg, de.Suggestion
	case errors.As(err, &ie):
		return "infra", ie.Msg, ie.Suggestion
	case errors.As(err, &dae):
		return "data", dae.Msg, dae.Suggestion
	}
	return "unclassified", err.Error(), ""
}

// TestExecState_InputErrors_ClassAndRecoveryText drives execute_state input
// errors through the real action handler and pins each error's class
// (domain vs infra), its message, and the recovery text a caller reads to fix
// the call. Infrastructure failures that need a failing disk (state writes,
// fact-sheet listing, server-state stores) cannot be triggered from a test and
// are not covered here.
func TestExecState_InputErrors_ClassAndRecoveryText(t *testing.T) {
	runState := func(t *testing.T, root string) {
		t.Helper()
		createExecState(t, root, "feat/test", map[string]any{
			"waves":   []any{},
			"context": map[string]any{},
		})
	}
	// startWave writes the fact sheets and server state for tasks 1 and 2 of run-1.
	startWave := func(t *testing.T, root string) {
		t.Helper()
		runState(t, root)
		_, err := executeState(root, root, ExecuteStateIn{
			Action:    "wave-start",
			Branch:    "feat/test",
			Wave:      intPtr(1),
			TasksJSON: `[{"id":"1","name":"First","description":"d1"},{"id":"2","name":"Second","description":"d2"}]`,
			RunID:     "run-1",
		}, fixedClock(testNow))
		if err != nil {
			t.Fatalf("wave-start: %v", err)
		}
	}
	makeRunsDir := func(t *testing.T, root string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, paths.DataDir, paths.RunsSubdir), 0o755); err != nil {
			t.Fatalf("mkdir runs dir: %v", err)
		}
	}

	tests := []struct {
		name        string
		setup       func(t *testing.T, root string)
		in          ExecuteStateIn
		wantClass   string
		wantMsg     string // substring of the message
		wantSuggest string // substring of the Suggestion
	}{
		// task-context
		{
			name:        "task-context without taskId",
			in:          ExecuteStateIn{Action: "task-context"},
			wantClass:   "domain",
			wantMsg:     "taskId is required for task-context",
			wantSuggest: `action:"task-context"`,
		},
		{
			name:        "task-context with a bad run id",
			setup:       runState,
			in:          ExecuteStateIn{Action: "task-context", Branch: "feat/test", RunID: "bad/id", TaskID: "1"},
			wantClass:   "domain",
			wantMsg:     "invalid runID",
			wantSuggest: "only letters, digits, underscore, hyphen",
		},
		{
			name:        "task-context for a run without fact sheets",
			setup:       runState,
			in:          ExecuteStateIn{Action: "task-context", Branch: "feat/test", RunID: "never-started", TaskID: "1"},
			wantClass:   "domain",
			wantMsg:     "run has no fact sheets yet",
			wantSuggest: `wave-start for run "never-started"`,
		},
		{
			name:        "task-context for an id missing from the run",
			setup:       startWave,
			in:          ExecuteStateIn{Action: "task-context", Branch: "feat/test", RunID: "run-1", TaskID: "99"},
			wantClass:   "domain",
			wantMsg:     "valid task IDs: 1, 2",
			wantSuggest: "one of: 1, 2",
		},
		{
			name: "task-context with a corrupt server state file",
			setup: func(t *testing.T, root string) {
				startWave(t, root)
				p := filepath.Join(root, paths.DataDir, paths.RunsSubdir, "run-1", "progress", "1.server.json")
				if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
					t.Fatalf("corrupt server state: %v", err)
				}
			},
			in:          ExecuteStateIn{Action: "task-context", Branch: "feat/test", RunID: "run-1", TaskID: "1"},
			wantClass:   "infra",
			wantMsg:     "load server state for task 1",
			wantSuggest: "1.server.json is readable and holds valid JSON",
		},

		// wave-split
		{
			name:        "wave-split without dispatched",
			in:          ExecuteStateIn{Action: "wave-split"},
			wantClass:   "domain",
			wantMsg:     "--dispatched is required",
			wantSuggest: `["1","2","3"]`,
		},
		{
			name:        "wave-split with dispatched that is not JSON",
			in:          ExecuteStateIn{Action: "wave-split", Dispatched: "1,2,3"},
			wantClass:   "domain",
			wantMsg:     "dispatched is not valid JSON",
			wantSuggest: "Put double quotes around each ID",
		},
		{
			name:        "wave-split with missingIds that is not JSON",
			in:          ExecuteStateIn{Action: "wave-split", Dispatched: `["1","2"]`, MissingIds: "three"},
			wantClass:   "domain",
			wantMsg:     "missingIds is not valid JSON",
			wantSuggest: `["3"]`,
		},
		{
			name:        "wave-split past a custom maxSplitDepth",
			in:          ExecuteStateIn{Action: "wave-split", Dispatched: `["1","2"]`, SplitDepth: 2, MaxSplitDepth: 2},
			wantClass:   "domain",
			wantMsg:     "manual escalation required",
			wantSuggest: "Do not call wave-split again",
		},
		{
			name:        "wave-split past the built-in depth limit",
			in:          ExecuteStateIn{Action: "wave-split", Dispatched: `["1","2"]`, SplitDepth: 3, MaxSplitDepth: 5},
			wantClass:   "domain",
			wantMsg:     "MaxSplitDepthExceededError",
			wantSuggest: "instead of retrying",
		},

		// wave-progress
		{
			name:        "wave-progress without runId",
			in:          ExecuteStateIn{Action: "wave-progress"},
			wantClass:   "domain",
			wantMsg:     "runId is required",
			wantSuggest: "runId exactly as returned by execute_state wave-start",
		},
		{
			name:        "wave-progress read with a bad run id",
			in:          ExecuteStateIn{Action: "wave-progress", RunID: "bad/id", ReadProgress: true},
			wantClass:   "domain",
			wantMsg:     "read progress:",
			wantSuggest: "only letters, digits, underscore, hyphen",
		},
		{
			name:        "wave-progress write without taskId",
			in:          ExecuteStateIn{Action: "wave-progress", RunID: "run-1"},
			wantClass:   "domain",
			wantMsg:     "taskId is required (write mode)",
			wantSuggest: "readProgress:true",
		},
		{
			name:        "wave-progress write with a bad phase",
			setup:       makeRunsDir,
			in:          ExecuteStateIn{Action: "wave-progress", RunID: "run-1", TaskID: "1", Phase: "napping"},
			wantClass:   "domain",
			wantMsg:     "invalid phase",
			wantSuggest: "started|reading|editing|verifying|reporting",
		},
		{
			name:        "wave-progress write with a bad run id",
			setup:       makeRunsDir,
			in:          ExecuteStateIn{Action: "wave-progress", RunID: "bad/id", TaskID: "1", Phase: "started"},
			wantClass:   "domain",
			wantMsg:     "invalid runID",
			wantSuggest: "[A-Za-z0-9_-]",
		},

		// wave-done
		{
			name:        "wave-done without wave",
			in:          ExecuteStateIn{Action: "wave-done"},
			wantClass:   "domain",
			wantMsg:     "--wave is required",
			wantSuggest: `action:"wave-done"`,
		},
		{
			name:        "wave-done with decisions that are not JSON",
			in:          ExecuteStateIn{Action: "wave-done", Wave: intPtr(1), Decisions: "Chose sqlite over postgres"},
			wantClass:   "domain",
			wantMsg:     "decisions is not valid JSON",
			wantSuggest: `["Chose sqlite over postgres"]`,
		},
		{
			name:        "wave-done with an unknown status",
			setup:       runState,
			in:          ExecuteStateIn{Action: "wave-done", Branch: "feat/test", Wave: intPtr(1), Status: "finished"},
			wantClass:   "domain",
			wantMsg:     "--status must be one of completed, partial",
			wantSuggest: `"completed" or "partial"`,
		},

		// task-done
		{
			name:        "task-done without wave",
			in:          ExecuteStateIn{Action: "task-done", TaskID: "1"},
			wantClass:   "domain",
			wantMsg:     "--wave is required",
			wantSuggest: `action:"task-done"`,
		},
		{
			name:        "task-done without taskId",
			in:          ExecuteStateIn{Action: "task-done", Wave: intPtr(1)},
			wantClass:   "domain",
			wantMsg:     "taskId is required",
			wantSuggest: `action:"task-done"`,
		},
		{
			name:        "task-done with filesChanged that is not JSON",
			in:          ExecuteStateIn{Action: "task-done", Wave: intPtr(1), TaskID: "1", FilesChanged: "src/a.go"},
			wantClass:   "domain",
			wantMsg:     "filesChanged is not valid JSON",
			wantSuggest: `["internal/tools/foo.go","internal/tools/bar.go"]`,
		},
		{
			name:        "task-done with filesAdded that is not JSON",
			in:          ExecuteStateIn{Action: "task-done", Wave: intPtr(1), TaskID: "1", FilesAdded: "src/a.go"},
			wantClass:   "domain",
			wantMsg:     "filesAdded is not valid JSON",
			wantSuggest: "Each path must also be in filesChanged",
		},
		{
			name:        "task-done with verifyToken that is not JSON",
			in:          ExecuteStateIn{Action: "task-done", Wave: intPtr(1), TaskID: "1", VerifyToken: "tests-pass"},
			wantClass:   "domain",
			wantMsg:     "verifyToken is not valid JSON",
			wantSuggest: "A bare word without quotes is not valid JSON",
		},
		{
			name:        "task-done with verifyToken of the wrong type",
			in:          ExecuteStateIn{Action: "task-done", Wave: intPtr(1), TaskID: "1", VerifyToken: "123"},
			wantClass:   "domain",
			wantMsg:     "verifyToken must be a JSON array or string",
			wantSuggest: `["go test ./... ok"]`,
		},
		{
			name:        "task-done with filesAdded outside filesChanged",
			in:          ExecuteStateIn{Action: "task-done", Wave: intPtr(1), TaskID: "1", FilesChanged: `["a.go"]`, FilesAdded: `["b.go"]`},
			wantClass:   "domain",
			wantMsg:     `filesAdded entry "b.go" is not present in filesChanged`,
			wantSuggest: `Add "b.go" to filesChanged`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.setup != nil {
				tt.setup(t, root)
			}

			_, err := executeState(root, root, tt.in, fixedClock(testNow))
			if err == nil {
				t.Fatal("expected an error, got nil")
			}

			class, msg, suggestion := execErrParts(err)
			if class != tt.wantClass {
				t.Errorf("class = %q, want %q (err: %v)", class, tt.wantClass, err)
			}
			if !strings.Contains(msg, tt.wantMsg) {
				t.Errorf("Msg = %q, want it to contain %q", msg, tt.wantMsg)
			}
			if !strings.Contains(suggestion, tt.wantSuggest) {
				t.Errorf("Suggestion = %q, want it to contain %q", suggestion, tt.wantSuggest)
			}
		})
	}
}
