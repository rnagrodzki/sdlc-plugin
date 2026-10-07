package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// dashNow is the fixed "now" of every dashboard snapshot test.
var dashNow = time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)

// dashRoot returns a temp repo root with an empty .sdlc-v2/runs/ folder.
func dashRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, paths.DataDir, paths.RunsSubdir), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// dashWriteJSON writes v as JSON to path (creating parent folders) and sets
// the file modification time to mtime.
func dashWriteJSON(t *testing.T, path string, v any, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// dashWriteState writes a state file named name under root's runs/ folder.
func dashWriteState(t *testing.T, root, name string, data map[string]any, mtime time.Time) {
	t.Helper()
	dashWriteJSON(t, filepath.Join(root, paths.DataDir, paths.RunsSubdir, name), data, mtime)
}

// dashWriteReviewDim writes one review dimension ledger file.
func dashWriteReviewDim(t *testing.T, root, run, dim string, data map[string]any, mtime time.Time) {
	t.Helper()
	dashWriteJSON(t, filepath.Join(root, paths.DataDir, paths.RunsSubdir, "ledger", run, dim+".json"), data, mtime)
}

// dashCollect collects a snapshot of one root at dashNow.
func dashCollect(t *testing.T, root string) DashboardRepo {
	t.Helper()
	snap := CollectDashboardSnapshot([]string{root}, dashNow, "test")
	if len(snap.Repos) != 1 {
		t.Fatalf("repos = %d, want 1", len(snap.Repos))
	}
	return snap.Repos[0]
}

// dashOne collects a snapshot of one root and returns its only pipeline.
func dashOne(t *testing.T, root string) DashboardPipeline {
	t.Helper()
	repo := dashCollect(t, root)
	if len(repo.Pipelines) != 1 {
		t.Fatalf("pipelines = %d, want 1: %+v", len(repo.Pipelines), repo.Pipelines)
	}
	return repo.Pipelines[0]
}

func dashSteps(statuses ...string) []any {
	names := []string{"execute", "commit", "review", "pr", "verify-pipeline"}
	out := make([]any, 0, len(statuses))
	for i, s := range statuses {
		step := map[string]any{"name": names[i], "status": s}
		if s != StepPending {
			step["startedAt"] = "2026-10-07T09:00:00Z"
		}
		out = append(out, step)
	}
	return out
}

func dashStepStatuses(p DashboardPipeline) []string {
	out := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		out = append(out, s.Status)
	}
	return out
}

func TestDashboardSnapshot_PipelineStatus(t *testing.T) {
	fresh := dashNow.Add(-time.Minute)
	old := dashNow.Add(-45 * time.Minute)

	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
		want  string
	}{
		// ship
		{"ship/completed when pipelineStatus is completed", func(t *testing.T, root string) {
			dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", map[string]any{
				"branch": "feat/x", "pipelineStatus": "completed", "pipelineCompletedAt": "2026-10-07T09:50:00Z",
				"steps": dashSteps(StepCompleted, StepCompleted),
			}, fresh)
		}, PipelineCompleted},
		{"ship/failed when a step failed and the run is not in flight", func(t *testing.T, root string) {
			// No step was ever started, so shipRunInFlight is false.
			dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", map[string]any{
				"branch": "feat/x",
				"steps":  []any{map[string]any{"name": "execute", "status": StepFailed, "error": "boom"}},
			}, fresh)
		}, PipelineFailed},
		{"ship/running when a failed step is still in flight", func(t *testing.T, root string) {
			dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", map[string]any{
				"branch": "feat/x", "steps": dashSteps(StepFailed, StepPending),
			}, fresh)
		}, PipelineRunning},
		{"ship/running otherwise", func(t *testing.T, root string) {
			dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", map[string]any{
				"branch": "feat/x", "steps": dashSteps(StepCompleted, StepInProgress, StepPending),
			}, fresh)
		}, PipelineRunning},
		{"ship/stalled when updatedAt is older than 30 min", func(t *testing.T, root string) {
			dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", map[string]any{
				"branch": "feat/x", "steps": dashSteps(StepCompleted, StepInProgress, StepPending),
			}, old)
		}, PipelineStalled},

		// execute
		{"execute/completed when runStatus is completed", func(t *testing.T, root string) {
			dashWriteState(t, root, "execute-feat-x-20261007T090000Z.json", map[string]any{
				"branch": "feat/x", "runStatus": "completed", "runCompletedAt": "2026-10-07T09:50:00Z",
				"waves": []any{map[string]any{"number": 1, "status": "completed"}},
			}, fresh)
		}, PipelineCompleted},
		{"execute/failed when a wave failed and no wave is in progress", func(t *testing.T, root string) {
			dashWriteState(t, root, "execute-feat-x-20261007T090000Z.json", map[string]any{
				"branch": "feat/x",
				"waves": []any{
					map[string]any{"number": 1, "status": "completed"},
					map[string]any{"number": 2, "status": "failed"},
				},
			}, fresh)
		}, PipelineFailed},
		{"execute/running when a wave failed but another is in progress", func(t *testing.T, root string) {
			dashWriteState(t, root, "execute-feat-x-20261007T090000Z.json", map[string]any{
				"branch": "feat/x",
				"waves": []any{
					map[string]any{"number": 1, "status": "failed"},
					map[string]any{"number": 2, "status": "in_progress"},
				},
			}, fresh)
		}, PipelineRunning},
		{"execute/running when a wave is partial only", func(t *testing.T, root string) {
			dashWriteState(t, root, "execute-feat-x-20261007T090000Z.json", map[string]any{
				"branch": "feat/x",
				"waves":  []any{map[string]any{"number": 1, "status": "partial"}},
			}, fresh)
		}, PipelineRunning},
		{"execute/stalled when updatedAt is older than 30 min", func(t *testing.T, root string) {
			dashWriteState(t, root, "execute-feat-x-20261007T090000Z.json", map[string]any{
				"branch": "feat/x",
				"waves":  []any{map[string]any{"number": 1, "status": "in_progress"}},
			}, old)
		}, PipelineStalled},

		// plan
		{"plan/completed when planIntegrity.done is set", func(t *testing.T, root string) {
			dashWriteState(t, root, "plan-main-20261007T090000Z.json", map[string]any{
				"planIntegrity": map[string]any{"skillInvoked": "2026-10-07T09:00:00Z", "done": "2026-10-07T09:40:00Z"},
				"checkpoint":    map[string]any{"step": "7"},
			}, fresh)
		}, PipelineCompleted},
		{"plan/running otherwise (never failed)", func(t *testing.T, root string) {
			dashWriteState(t, root, "plan-main-20261007T090000Z.json", map[string]any{
				"planIntegrity": map[string]any{"skillInvoked": "2026-10-07T09:00:00Z"},
				"checkpoint":    map[string]any{"step": "3"},
			}, fresh)
		}, PipelineRunning},
		{"plan/stalled when updatedAt is older than 30 min", func(t *testing.T, root string) {
			dashWriteState(t, root, "plan-main-20261007T090000Z.json", map[string]any{
				"planIntegrity": map[string]any{"skillInvoked": "2026-10-07T09:00:00Z"},
				"checkpoint":    map[string]any{"step": "3"},
			}, old)
		}, PipelineStalled},

		// review
		{"review/completed when each file has checkoutAt", func(t *testing.T, root string) {
			run := "review-2026-10-07T09-00-00Z"
			dashWriteReviewDim(t, root, run, "security", map[string]any{"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:05:00Z"}, fresh)
			dashWriteReviewDim(t, root, run, "docs", map[string]any{"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:06:00Z"}, fresh)
		}, PipelineCompleted},
		{"review/running when a file has no checkoutAt (never failed)", func(t *testing.T, root string) {
			run := "review-2026-10-07T09-00-00Z"
			dashWriteReviewDim(t, root, run, "security", map[string]any{"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:05:00Z"}, fresh)
			dashWriteReviewDim(t, root, run, "docs", map[string]any{"checkinAt": "2026-10-07T09:00:00Z"}, fresh)
		}, PipelineRunning},
		{"review/stalled when updatedAt is older than 30 min", func(t *testing.T, root string) {
			run := "review-2026-10-07T09-00-00Z"
			dashWriteReviewDim(t, root, run, "docs", map[string]any{"checkinAt": "2026-10-07T09:00:00Z"}, old)
		}, PipelineStalled},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := dashRoot(t)
			tc.setup(t, root)
			p := dashOne(t, root)
			if p.Status != tc.want {
				t.Errorf("status = %q, want %q", p.Status, tc.want)
			}
			if tc.want == PipelineCompleted && p.CompletedAt == nil {
				t.Errorf("completedAt = nil for a completed pipeline")
			}
			if tc.want != PipelineCompleted && p.CompletedAt != nil {
				t.Errorf("completedAt = %q, want nil", *p.CompletedAt)
			}
		})
	}
}

func TestDashboardSnapshot_Progress(t *testing.T) {
	fresh := dashNow.Add(-time.Minute)

	t.Run("ship current comes from shipLastActiveStep", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", map[string]any{
			"branch": "feat/x", "steps": dashSteps(StepCompleted, StepSkipped, StepCompleted, StepInProgress, StepPending),
		}, fresh)
		got := dashOne(t, root).Progress
		want := DashboardProgress{Done: 3, Total: 5, Current: "pr", Label: "step 4 of 5"}
		if got != want {
			t.Errorf("progress = %+v, want %+v", got, want)
		}
	})

	t.Run("execute done and total count tasks", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, "execute-feat-x-20261007T090000Z.json", map[string]any{
			"branch": "feat/x", "totalTasks": 5,
			"waves": []any{
				map[string]any{"number": 1, "status": "completed", "tasks": []any{
					map[string]any{"id": "1", "status": "completed"},
					map[string]any{"id": "2", "status": "completed"},
				}},
				map[string]any{"number": 2, "status": "in_progress", "tasks": []any{
					map[string]any{"id": "3", "status": "completed"},
					map[string]any{"id": "4", "status": "in_progress"},
				}},
			},
		}, fresh)
		got := dashOne(t, root).Progress
		want := DashboardProgress{Done: 3, Total: 5, Current: "wave 2", Label: "3 of 5 tasks"}
		if got != want {
			t.Errorf("progress = %+v, want %+v", got, want)
		}
	})

	t.Run("plan position of checkpoint.step", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, "plan-main-20261007T090000Z.json", map[string]any{
			"planIntegrity": map[string]any{"skillInvoked": "2026-10-07T09:00:00Z"},
			"checkpoint":    map[string]any{"step": "6.5"},
		}, fresh)
		got := dashOne(t, root).Progress
		want := DashboardProgress{Done: 4, Total: 5, Current: "finalize", Label: "finalize (5 of 5)"}
		if got != want {
			t.Errorf("progress = %+v, want %+v", got, want)
		}
	})

	t.Run("review done and total count dimensions", func(t *testing.T) {
		root := dashRoot(t)
		run := "review-2026-10-07T09-00-00Z"
		dashWriteReviewDim(t, root, run, "a-docs", map[string]any{"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:05:00Z"}, fresh)
		dashWriteReviewDim(t, root, run, "b-security", map[string]any{"checkinAt": "2026-10-07T09:00:00Z"}, fresh)
		dashWriteReviewDim(t, root, run, "c-tests", map[string]any{"checkinAt": "2026-10-07T09:00:00Z"}, fresh)
		got := dashOne(t, root).Progress
		want := DashboardProgress{Done: 1, Total: 3, Current: "b-security", Label: "1 of 3 dimensions"}
		if got != want {
			t.Errorf("progress = %+v, want %+v", got, want)
		}
	})
}

func TestDashboardSnapshot_StepStatus(t *testing.T) {
	fresh := dashNow.Add(-time.Minute)

	t.Run("ship step keeps the schema value", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", map[string]any{
			"branch": "feat/x", "steps": dashSteps(StepCompleted, StepSkipped, StepInProgress, StepPending),
		}, fresh)
		got := dashStepStatuses(dashOne(t, root))
		want := []string{StepCompleted, StepSkipped, StepInProgress, StepPending}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("steps = %v, want %v", got, want)
		}
	})

	t.Run("ship failed step keeps the schema value", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", map[string]any{
			"branch": "feat/x", "steps": dashSteps(StepCompleted, StepFailed),
		}, fresh)
		got := dashStepStatuses(dashOne(t, root))
		want := []string{StepCompleted, StepFailed}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("steps = %v, want %v", got, want)
		}
	})

	t.Run("execute wave keeps its value and partial becomes failed", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, "execute-feat-x-20261007T090000Z.json", map[string]any{
			"branch": "feat/x",
			"waves": []any{
				map[string]any{"number": 1, "status": "completed"},
				map[string]any{"number": 2, "status": "partial"},
				map[string]any{"number": 3, "status": "failed"},
				map[string]any{"number": 4, "status": "in_progress"},
				map[string]any{"number": 5, "status": "pending"},
			},
		}, fresh)
		p := dashOne(t, root)
		got := dashStepStatuses(p)
		want := []string{StepCompleted, StepFailed, StepFailed, StepInProgress, StepPending}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("steps = %v, want %v", got, want)
		}
		if p.Steps[1].Name != "wave 2" {
			t.Errorf("step name = %q, want %q", p.Steps[1].Name, "wave 2")
		}
	})

	t.Run("plan step before, at, after the current step", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, "plan-main-20261007T090000Z.json", map[string]any{
			"planIntegrity": map[string]any{"skillInvoked": "2026-10-07T09:00:00Z"},
			"checkpoint":    map[string]any{"step": "2"},
		}, fresh)
		got := dashStepStatuses(dashOne(t, root))
		want := []string{StepCompleted, StepCompleted, StepInProgress, StepPending, StepPending}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("steps = %v, want %v", got, want)
		}
	})

	t.Run("review dimension with and without checkoutAt", func(t *testing.T) {
		root := dashRoot(t)
		run := "review-2026-10-07T09-00-00Z"
		dashWriteReviewDim(t, root, run, "a", map[string]any{"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:05:00Z"}, fresh)
		dashWriteReviewDim(t, root, run, "b", map[string]any{"checkinAt": "2026-10-07T09:00:00Z"}, fresh)
		p := dashOne(t, root)
		got := dashStepStatuses(p)
		want := []string{StepCompleted, StepInProgress}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("steps = %v, want %v", got, want)
		}
		wantDetail := []*DashboardStepDetail{{Kind: dashboardKindFindings}, nil}
		for i, s := range p.Steps {
			if !reflect.DeepEqual(s.Detail, wantDetail[i]) {
				t.Errorf("step %q detail = %+v, want %+v", s.Name, s.Detail, wantDetail[i])
			}
		}
	})
}

func TestDashboardSnapshot_IssueSources(t *testing.T) {
	fresh := dashNow.Add(-time.Minute)

	t.Run("ship step failed gives a step issue", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", map[string]any{
			"branch": "feat/x",
			"steps": []any{
				map[string]any{"name": "pr", "status": StepFailed, "error": "gh auth failed", "startedAt": "2026-10-07T09:00:00Z"},
				map[string]any{"name": "verify-pipeline", "status": StepFailed, "reason": "ci red", "startedAt": "2026-10-07T09:00:00Z"},
			},
		}, fresh)
		got := dashOne(t, root).Issues
		want := []DashboardIssue{
			{Source: "step", Severity: "high", Text: "gh auth failed", Ref: "pr"},
			{Source: "step", Severity: "high", Text: "ci red", Ref: "verify-pipeline"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("issues = %+v, want %+v", got, want)
		}
	})

	t.Run("execute wave failed or partial gives a wave issue", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, "execute-feat-x-20261007T090000Z.json", map[string]any{
			"branch": "feat/x",
			"waves": []any{
				map[string]any{"number": 1, "status": "completed"},
				map[string]any{"number": 2, "status": "partial"},
				map[string]any{"number": 3, "status": "failed"},
			},
		}, fresh)
		got := dashOne(t, root).Issues
		want := []DashboardIssue{
			{Source: "wave", Severity: "high", Text: "partial", Ref: "wave 2"},
			{Source: "wave", Severity: "high", Text: "failed", Ref: "wave 3"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("issues = %+v, want %+v", got, want)
		}
	})

	t.Run("review finding gives a review issue", func(t *testing.T) {
		root := dashRoot(t)
		findings, _ := json.Marshal([]map[string]any{
			{"severity": "medium", "file": "a.go", "line": 12, "rationale": "nil map write"},
			{"severity": "low", "file": "b.go", "line": 3, "rationale": "typo"},
		})
		run := "review-2026-10-07T09-00-00Z"
		dashWriteReviewDim(t, root, run, "code", map[string]any{
			"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:05:00Z", "findings": string(findings),
		}, fresh)
		dashWriteReviewDim(t, root, run, "docs", map[string]any{
			"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:05:00Z", "findings": "## markdown, not JSON",
		}, fresh)
		p := dashOne(t, root)
		want := []DashboardIssue{
			{Source: "review", Severity: "medium", Text: "nil map write", File: "a.go", Line: "12", Ref: "code"},
			{Source: "review", Severity: "low", Text: "typo", File: "b.go", Line: "3", Ref: "code"},
		}
		if !reflect.DeepEqual(p.Issues, want) {
			t.Errorf("issues = %+v, want %+v", p.Issues, want)
		}
		wantSteps := []DashboardStep{
			{Name: "code", Status: StepCompleted, Detail: &DashboardStepDetail{
				Kind: dashboardKindFindings,
				Findings: []DashboardReviewFinding{
					{Text: "nil map write", Severity: "medium", File: "a.go", Line: "12"},
					{Text: "typo", Severity: "low", File: "b.go", Line: "3"},
				},
			}},
			{Name: "docs", Status: StepCompleted, Detail: &DashboardStepDetail{Kind: dashboardKindFindings}},
		}
		if !reflect.DeepEqual(p.Steps, wantSteps) {
			t.Errorf("steps = %+v, want %+v", p.Steps, wantSteps)
		}
	})
}

func TestDashboardSnapshot_ExecuteProgressJoinsByWaveRunID(t *testing.T) {
	root := dashRoot(t)
	runs := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	stateTime := dashNow.Add(-2 * time.Hour)
	progressTime := dashNow.Add(-5 * time.Minute)
	decoyTime := dashNow.Add(-10 * time.Second)

	dashWriteState(t, root, "execute-feat-x-20261007T080000Z.json", map[string]any{
		"branch": "feat/x",
		"waves":  []any{map[string]any{"number": 1, "status": "in_progress", "runId": "run-abc"}},
	}, stateTime)
	dashWriteJSON(t, filepath.Join(runs, "run-abc", "progress", "1.json"), map[string]any{"phase": "editing"}, progressTime)
	// Decoys named after the state file timestamp: a join by file name would
	// pick these newer files.
	dashWriteJSON(t, filepath.Join(runs, "20261007T080000Z", "progress", "1.json"), map[string]any{}, decoyTime)
	dashWriteJSON(t, filepath.Join(runs, "20261007T080000", "progress", "1.json"), map[string]any{}, decoyTime)

	p := dashOne(t, root)
	if want := dashFormat(progressTime); p.UpdatedAt != want {
		t.Errorf("updatedAt = %q, want %q (from runs/run-abc/progress)", p.UpdatedAt, want)
	}
	if p.Status != PipelineRunning {
		t.Errorf("status = %q, want %q", p.Status, PipelineRunning)
	}
}

func TestDashboardSnapshot_EvidenceLinesExtendUpdatedAt(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, "ship-feat-x-20261007T080000Z.json", map[string]any{
		"branch": "feat/x", "steps": dashSteps(StepInProgress),
	}, dashNow.Add(-2*time.Hour))
	ev := filepath.Join(root, paths.DataDir, paths.EvidenceSubdir)
	if err := os.MkdirAll(ev, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"ts":"2026-10-07T09:58:00Z","branch":"feat/x","command":"go test"}` + "\n" +
		`{"ts":"2026-10-07T09:59:30Z","branch":"other","command":"ls"}` + "\n"
	if err := os.WriteFile(filepath.Join(ev, "cli-executions.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	p := dashOne(t, root)
	if p.UpdatedAt != "2026-10-07T09:58:00Z" {
		t.Errorf("updatedAt = %q, want the newest evidence line of the branch", p.UpdatedAt)
	}
	if p.Status != PipelineRunning {
		t.Errorf("status = %q, want %q", p.Status, PipelineRunning)
	}
}

func TestDashboardSnapshot_HistoryWindow(t *testing.T) {
	cases := []struct {
		name  string
		file  string
		data  map[string]any
		age   time.Duration
		shows bool
	}{
		{"completed within 24 h shows", "ship-feat-x-20261006T090000Z.json",
			map[string]any{"pipelineStatus": "completed", "steps": dashSteps(StepCompleted)}, 23 * time.Hour, true},
		{"completed older than 24 h is hidden", "ship-feat-x-20261006T090000Z.json",
			map[string]any{"pipelineStatus": "completed", "steps": dashSteps(StepCompleted)}, 25 * time.Hour, false},
		{"failed older than 24 h is hidden", "ship-feat-x-20261006T090000Z.json",
			map[string]any{"steps": []any{map[string]any{"name": "pr", "status": StepFailed}}}, 25 * time.Hour, false},
		{"running older than 24 h shows as stalled", "ship-feat-x-20261006T090000Z.json",
			map[string]any{"steps": dashSteps(StepInProgress)}, 25 * time.Hour, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := dashRoot(t)
			dashWriteState(t, root, tc.file, tc.data, dashNow.Add(-tc.age))
			repo := dashCollect(t, root)
			if got := len(repo.Pipelines) == 1; got != tc.shows {
				t.Errorf("shows = %v, want %v", got, tc.shows)
			}
		})
	}
}

func TestDashboardSnapshot_Worktree(t *testing.T) {
	fresh := dashNow.Add(-time.Minute)
	root := dashRoot(t)
	dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", map[string]any{
		"branch": "feat/x", "worktree": "/wt/ship", "steps": dashSteps(StepInProgress),
	}, fresh)
	dashWriteState(t, root, "execute-feat-y-20261007T090100Z.json", map[string]any{
		"branch": "feat/y", "worktree": "/wt/exec", "waves": []any{},
	}, fresh)
	dashWriteState(t, root, "plan-feat-x-20261007T090200Z.json", map[string]any{
		"worktree": "/wt/ignored", "checkpoint": map[string]any{"step": "1"},
	}, fresh)
	dashWriteReviewDim(t, root, "review-2026-10-07T09-00-00Z", "docs", map[string]any{"checkinAt": "2026-10-07T09:00:00Z"}, fresh)

	got := map[string]string{}
	for _, p := range dashCollect(t, root).Pipelines {
		got[p.Kind] = p.Worktree
	}
	want := map[string]string{"ship": "/wt/ship", "execute": "/wt/exec", "plan": "", "review": ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("worktree by kind = %v, want %v", got, want)
	}
}

func TestDashboardSnapshot_LinkedWorktreeShowsOnce(t *testing.T) {
	main := dashRoot(t)
	linked := t.TempDir()
	if err := os.MkdirAll(filepath.Join(linked, paths.DataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	// A linked worktree's runs/ is a symlink to the main worktree's runs/.
	if err := os.Symlink(filepath.Join(main, paths.DataDir, paths.RunsSubdir), filepath.Join(linked, paths.DataDir, paths.RunsSubdir)); err != nil {
		t.Fatal(err)
	}
	dashWriteState(t, main, "ship-feat-x-20261007T090000Z.json", map[string]any{
		"branch": "feat/x", "worktree": linked, "steps": dashSteps(StepInProgress),
	}, dashNow.Add(-time.Minute))

	t.Run("main and linked roots give one repo", func(t *testing.T) {
		snap := CollectDashboardSnapshot([]string{linked, main}, dashNow, "test")
		if len(snap.Repos) != 1 {
			t.Fatalf("repos = %d, want 1", len(snap.Repos))
		}
		repo := snap.Repos[0]
		if repo.Root != filepath.Clean(main) {
			t.Errorf("root = %q, want main root %q", repo.Root, main)
		}
		if len(repo.Pipelines) != 1 || repo.Pipelines[0].Worktree != linked {
			t.Errorf("pipelines = %+v, want one with worktree %q", repo.Pipelines, linked)
		}
	})

	t.Run("linked root alone maps to the main root", func(t *testing.T) {
		snap := CollectDashboardSnapshot([]string{linked}, dashNow, "test")
		if len(snap.Repos) != 1 {
			t.Fatalf("repos = %d, want 1", len(snap.Repos))
		}
		wantRoot, err := filepath.EvalSymlinks(main)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Repos[0].Root != wantRoot {
			t.Errorf("root = %q, want %q", snap.Repos[0].Root, wantRoot)
		}
		if len(snap.Repos[0].Pipelines) != 1 {
			t.Errorf("pipelines = %d, want 1", len(snap.Repos[0].Pipelines))
		}
	})
}

func TestDashboardSnapshot_ListsAreNeverNull(t *testing.T) {
	orig := dashboardActivity
	t.Cleanup(func() { dashboardActivity = orig })
	dashboardActivity = func(string, time.Time) ([]DashboardSession, []DashboardLearning, []DashboardDeferred) {
		return nil, nil, nil
	}

	empty := dashRoot(t)
	withRun := dashRoot(t)
	dashWriteState(t, withRun, "execute-feat-x-20261007T090000Z.json", map[string]any{"branch": "feat/x"}, dashNow.Add(-time.Minute))
	absent := filepath.Join(t.TempDir(), "gone")

	snap := CollectDashboardSnapshot([]string{empty, withRun, absent}, dashNow, "test")
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	// completedAt is the only field allowed to be null.
	s := strings.ReplaceAll(string(b), `"completedAt":null`, "")
	if strings.Contains(s, "null") {
		t.Errorf("snapshot JSON holds null: %s", b)
	}

	none := CollectDashboardSnapshot(nil, dashNow, "test")
	if b, _ := json.Marshal(none); !strings.Contains(string(b), `"repos":[]`) {
		t.Errorf("empty snapshot = %s, want repos []", b)
	}
}

func TestDashboardSnapshot_AbsentRepo(t *testing.T) {
	calls := 0
	orig := dashboardActivity
	t.Cleanup(func() { dashboardActivity = orig })
	dashboardActivity = func(root string, now time.Time) ([]DashboardSession, []DashboardLearning, []DashboardDeferred) {
		calls++
		return orig(root, now)
	}

	absent := filepath.Join(t.TempDir(), "gone")
	repo := dashCollect(t, absent)
	if repo.Error == "" {
		t.Error("error = \"\", want error text")
	}
	if repo.Root != absent || repo.Name != "gone" {
		t.Errorf("root, name = %q, %q", repo.Root, repo.Name)
	}
	if len(repo.Pipelines) != 0 || len(repo.Sessions) != 0 || len(repo.Learnings) != 0 || len(repo.Deferred) != 0 {
		t.Errorf("lists not empty: %+v", repo)
	}
	if repo.Pipelines == nil || repo.Sessions == nil || repo.Learnings == nil || repo.Deferred == nil {
		t.Errorf("a list is nil: %+v", repo)
	}
	if calls != 0 {
		t.Errorf("collectActivity calls = %d for an absent repo, want 0", calls)
	}
}

func TestDashboardSnapshot_CallsActivityOncePerRepo(t *testing.T) {
	calls := map[string]int{}
	orig := dashboardActivity
	t.Cleanup(func() { dashboardActivity = orig })
	dashboardActivity = func(root string, now time.Time) ([]DashboardSession, []DashboardLearning, []DashboardDeferred) {
		calls[root]++
		if !now.Equal(dashNow) {
			t.Errorf("now = %v, want %v", now, dashNow)
		}
		return []DashboardSession{{ID: "s-" + filepath.Base(root), Timeline: []DashboardEvent{}}},
			[]DashboardLearning{{Heading: "l-" + filepath.Base(root)}},
			[]DashboardDeferred{{ID: "d-" + filepath.Base(root)}}
	}

	a, b := dashRoot(t), dashRoot(t)
	snap := CollectDashboardSnapshot([]string{a, b, a}, dashNow, "test")
	if len(snap.Repos) != 2 {
		t.Fatalf("repos = %d, want 2", len(snap.Repos))
	}
	if calls[a] != 1 || calls[b] != 1 || len(calls) != 2 {
		t.Errorf("calls = %v, want one for each repo", calls)
	}
	for _, repo := range snap.Repos {
		base := filepath.Base(repo.Root)
		if len(repo.Sessions) != 1 || repo.Sessions[0].ID != "s-"+base ||
			len(repo.Learnings) != 1 || repo.Learnings[0].Heading != "l-"+base ||
			len(repo.Deferred) != 1 || repo.Deferred[0].ID != "d-"+base {
			t.Errorf("repo %s activity = %+v %+v %+v", repo.Root, repo.Sessions, repo.Learnings, repo.Deferred)
		}
	}
}

func TestDashboardSnapshot_StepConstantsMatchSchema(t *testing.T) {
	b, err := os.ReadFile("../../plugins/sdlc/schemas/ship-state.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Steps struct {
				Items struct {
					Properties struct {
						Status struct {
							Enum []string `json:"enum"`
						} `json:"status"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"steps"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatal(err)
	}
	got := []string{StepPending, StepInProgress, StepCompleted, StepSkipped, StepFailed}
	want := append([]string(nil), schema.Properties.Steps.Items.Properties.Status.Enum...)
	sort.Strings(got)
	sort.Strings(want)
	if len(want) == 0 || !reflect.DeepEqual(got, want) {
		t.Errorf("Step* constants = %v, schema steps[].status enum = %v", got, want)
	}
}

func dashFormat(t time.Time) string { return t.UTC().Format(time.RFC3339) }
