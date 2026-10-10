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

	"github.com/rnagrodzki/sdlc-plugin/internal/attention"
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
			// Markdown findings count as none, and a state issue says so.
			{Source: "state", Severity: "medium", Text: "the findings of worker file docs.json are not a JSON list: the dimension shows no findings"},
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
	dashboardActivity = func(string, time.Time) ([]DashboardSession, []DashboardLearning, []DashboardDeferred, []DashboardRun) {
		return nil, nil, nil, nil
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
	dashboardActivity = func(root string, now time.Time) ([]DashboardSession, []DashboardLearning, []DashboardDeferred, []DashboardRun) {
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
	dashboardActivity = func(root string, now time.Time) ([]DashboardSession, []DashboardLearning, []DashboardDeferred, []DashboardRun) {
		calls[root]++
		if !now.Equal(dashNow) {
			t.Errorf("now = %v, want %v", now, dashNow)
		}
		return []DashboardSession{{ID: "s-" + filepath.Base(root), Timeline: []DashboardEvent{}}},
			[]DashboardLearning{{Heading: "l-" + filepath.Base(root)}},
			[]DashboardDeferred{{ID: "d-" + filepath.Base(root)}},
			[]DashboardRun{{Kind: "r-" + filepath.Base(root)}}
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
			len(repo.Deferred) != 1 || repo.Deferred[0].ID != "d-"+base ||
			len(repo.History) != 1 || repo.History[0].Kind != "r-"+base {
			t.Errorf("repo %s activity = %+v %+v %+v %+v", repo.Root, repo.Sessions, repo.Learnings, repo.Deferred, repo.History)
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

// dashQuestionRecord returns a question wait record of session sid on branch,
// asked at the given time, with the fixed header "Approach".
func dashQuestionRecord(sid, toolUseID, branch, text string, asked time.Time) attention.Record {
	return attention.Record{
		Kind:      attention.KindQuestion,
		SessionID: sid,
		ToolUseID: toolUseID,
		Branch:    branch,
		Header:    "Approach",
		Text:      text,
		AskedAt:   dashFormat(asked),
	}
}

// dashWriteAttention stores r under root with attention.Write and fails the
// test when the write fails.
func dashWriteAttention(t *testing.T, root string, r attention.Record) {
	t.Helper()
	if err := attention.Write(root, r); err != nil {
		t.Fatalf("attention.Write: %v", err)
	}
}

// dashRunningShip writes a running ship state on feat/x for session sid and
// sets the file modification time to mtime. An empty sid leaves the state
// without a session ID.
func dashRunningShip(t *testing.T, root, sid string, mtime time.Time) {
	t.Helper()
	data := map[string]any{"branch": "feat/x", "steps": dashSteps(StepCompleted, StepInProgress)}
	if sid != "" {
		data["sessionId"] = sid
	}
	dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", data, mtime)
}

// TestDashboardSnapshot_AttentionJoin checks which wait records the snapshot
// joins to a running pipeline: the session ID must match, the branch slugs
// must match, and the record must be inside the history window.
func TestDashboardSnapshot_AttentionJoin(t *testing.T) {
	fresh := dashNow.Add(-time.Minute)
	asked := dashNow.Add(-5 * time.Minute)
	perm := attention.Record{
		Kind: attention.KindPermission, SessionID: "s1", Branch: "feat/x",
		Header: "Permission", Text: "Bash: go test ./...", AskedAt: dashFormat(asked),
	}

	cases := []struct {
		name string
		rec  attention.Record
		sid  string // session ID of the pipeline; "" for none
		want bool
	}{
		{"same session and branch", dashQuestionRecord("s1", "t1", "feat/x", "Which approach?", asked), "s1", true},
		{"branch spelled as its slug", dashQuestionRecord("s1", "t1", "feat-x", "Which approach?", asked), "s1", true},
		{"permission record", perm, "s1", true},
		{"other branch", dashQuestionRecord("s1", "t1", "main", "Which approach?", asked), "s1", false},
		{"other session", dashQuestionRecord("s2", "t1", "feat/x", "Which approach?", asked), "s1", false},
		{"pipeline without a session ID", dashQuestionRecord("s1", "t1", "feat/x", "Which approach?", asked), "", false},
		{"record older than the history window", dashQuestionRecord("s1", "t1", "feat/x", "Which approach?", dashNow.Add(-25*time.Hour)), "s1", false},
		{"record inside the history window", dashQuestionRecord("s1", "t1", "feat/x", "Which approach?", dashNow.Add(-23*time.Hour)), "s1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := dashRoot(t)
			dashRunningShip(t, root, tc.sid, fresh)
			dashWriteAttention(t, root, tc.rec)

			p := dashOne(t, root)
			if !tc.want {
				if p.Attention != nil {
					t.Errorf("attention = %+v, want nil", *p.Attention)
				}
				return
			}
			if p.Attention == nil {
				t.Fatalf("attention = nil, want the record %+v", tc.rec)
			}
			want := DashboardAttention{Kind: tc.rec.Kind, AskedAt: tc.rec.AskedAt, Header: tc.rec.Header, Text: tc.rec.Text}
			if *p.Attention != want {
				t.Errorf("attention = %+v, want %+v", *p.Attention, want)
			}
		})
	}
}

// TestDashboardSnapshot_AttentionNewestWins checks that, with two records for
// one pipeline, the record with the newest askedAt is the one shown. The newer
// record is written first, so the file order cannot decide the result.
func TestDashboardSnapshot_AttentionNewestWins(t *testing.T) {
	root := dashRoot(t)
	dashRunningShip(t, root, "s1", dashNow.Add(-time.Minute))
	dashWriteAttention(t, root, dashQuestionRecord("s1", "t-new", "feat/x", "new question", dashNow.Add(-2*time.Minute)))
	dashWriteAttention(t, root, dashQuestionRecord("s1", "t-old", "feat/x", "old question", dashNow.Add(-20*time.Minute)))

	p := dashOne(t, root)
	if p.Attention == nil || p.Attention.Text != "new question" {
		t.Errorf("attention = %+v, want the record with text %q", p.Attention, "new question")
	}
}

// TestDashboardSnapshot_AttentionKeepsRunning checks that a running pipeline
// with an open wait stays running after the stall limit, with no stalled
// issue, and that a pipeline with no wait of its own still stalls.
func TestDashboardSnapshot_AttentionKeepsRunning(t *testing.T) {
	idle := dashNow.Add(-31 * time.Minute)
	asked := dashNow.Add(-30 * time.Minute)

	cases := []struct {
		name       string
		rec        *attention.Record
		wantStatus string
	}{
		{"matching record keeps running", &attention.Record{
			Kind: attention.KindQuestion, SessionID: "s1", ToolUseID: "t1", Branch: "feat/x",
			Header: "Approach", Text: "Which approach?", AskedAt: dashFormat(asked),
		}, PipelineRunning},
		{"no record stalls", nil, PipelineStalled},
		{"record of another session stalls", &attention.Record{
			Kind: attention.KindQuestion, SessionID: "s2", ToolUseID: "t1", Branch: "feat/x",
			Header: "Approach", Text: "Which approach?", AskedAt: dashFormat(asked),
		}, PipelineStalled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := dashRoot(t)
			dashRunningShip(t, root, "s1", idle)
			if tc.rec != nil {
				dashWriteAttention(t, root, *tc.rec)
			}

			p := dashOne(t, root)
			if p.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", p.Status, tc.wantStatus)
			}
			stalledIssues := 0
			for _, is := range p.Issues {
				if is.Source == dashboardSourcePipeline && is.Text == dashboardStalledText {
					stalledIssues++
				}
			}
			if wantIssues := map[string]int{PipelineRunning: 0, PipelineStalled: 1}[tc.wantStatus]; stalledIssues != wantIssues {
				t.Errorf("stalled issues = %d, want %d", stalledIssues, wantIssues)
			}
		})
	}
}

// TestDashboardSnapshot_AttentionShipNestedExecute checks that a ship run and
// its nested execute run share a session and a branch, so the record joins
// the ship and the snapshot shows one attention mark, on the ship only.
func TestDashboardSnapshot_AttentionShipNestedExecute(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)
	exec := dashJoinExecData("feat/x", "2026-10-07T08:01:00Z", "a1b2c3d")
	exec["sessionId"] = "s-ship"
	dashWriteState(t, root, dashJoinExecFile("20261007T080100Z"), exec, dashJoinFresh)
	dashWriteAttention(t, root, dashQuestionRecord("s-ship", "t1", "feat/x", "Which approach?", dashNow.Add(-5*time.Minute)))

	repo := dashCollect(t, root)
	if len(repo.Pipelines) != 1 || repo.Pipelines[0].Kind != "ship" {
		t.Fatalf("pipelines = %v, want the ship only", dashJoinKinds(repo.Pipelines))
	}
	ship := repo.Pipelines[0]
	if ship.Attention == nil || ship.Attention.Text != "Which approach?" {
		t.Errorf("ship attention = %+v, want the record", ship.Attention)
	}
	if d := dashJoinStep(t, ship, "execute").Detail; d == nil || d.Kind != dashboardKindWaves {
		t.Errorf("execute detail = %+v, want the nested execute waves", d)
	}
	b, err := json.Marshal(repo)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), `"attention":`); n != 1 {
		t.Errorf("attention marks in the repo JSON = %d, want 1", n)
	}
}

// TestDashboardSnapshot_AttentionRunningOnly checks that a pipeline whose
// status is not running never gets attention, even when a record has the same
// session and branch, and that a running plan does get it.
func TestDashboardSnapshot_AttentionRunningOnly(t *testing.T) {
	fresh := dashNow.Add(-time.Minute)
	cases := []struct {
		name  string
		file  string
		data  map[string]any
		wants bool
	}{
		{"completed ship", "ship-feat-x-20261007T090000Z.json", map[string]any{
			"branch": "feat/x", "sessionId": "s1", "pipelineStatus": "completed",
			"pipelineCompletedAt": "2026-10-07T09:50:00Z", "steps": dashSteps(StepCompleted, StepCompleted),
		}, false},
		{"failed ship", "ship-feat-x-20261007T090000Z.json", map[string]any{
			"branch": "feat/x", "sessionId": "s1",
			"steps": []any{map[string]any{"name": "execute", "status": StepFailed, "error": "boom"}},
		}, false},
		{"completed plan", "plan-feat-x-20261007T090000Z.json", map[string]any{
			"sessionId":     "s1",
			"planIntegrity": map[string]any{"skillInvoked": "2026-10-07T09:00:00Z", "done": "2026-10-07T09:40:00Z"},
			"checkpoint":    map[string]any{"step": "7"},
		}, false},
		{"running plan", "plan-feat-x-20261007T090000Z.json", map[string]any{
			"sessionId":     "s1",
			"planIntegrity": map[string]any{"skillInvoked": "2026-10-07T09:00:00Z"},
			"checkpoint":    map[string]any{"step": "3"},
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := dashRoot(t)
			dashWriteState(t, root, tc.file, tc.data, fresh)
			dashWriteAttention(t, root, dashQuestionRecord("s1", "t1", "feat/x", "Which approach?", dashNow.Add(-5*time.Minute)))

			p := dashOne(t, root)
			if got := p.Attention != nil; got != tc.wants {
				t.Errorf("attention set = %v, want %v (status %q)", got, tc.wants, p.Status)
			}
		})
	}
}

// TestDashboardSnapshot_AttentionUnreadableFolder checks that a record folder
// that cannot be read gives no attention and no repo error. A regular file in
// place of the folder makes the read fail.
func TestDashboardSnapshot_AttentionUnreadableFolder(t *testing.T) {
	root := dashRoot(t)
	dashRunningShip(t, root, "s1", dashNow.Add(-time.Minute))
	dir := attention.Dir(root)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := attention.List(root, dashNow, dashboardHistoryWindow); err == nil {
		t.Fatal("attention.List error = nil, want a read error for the setup to be valid")
	}

	repo := dashCollect(t, root)
	if repo.Error != "" {
		t.Errorf("repo error = %q, want empty", repo.Error)
	}
	if len(repo.Pipelines) != 1 {
		t.Fatalf("pipelines = %d, want 1", len(repo.Pipelines))
	}
	if repo.Pipelines[0].Attention != nil {
		t.Errorf("attention = %+v, want nil", *repo.Pipelines[0].Attention)
	}
}

// TestDashboardAttachAttention checks the branches of dashboardAttachAttention
// that the snapshot tests do not reach: the guards, a record whose time does
// not parse, equal times, and the UTC form of askedAt.
func TestDashboardAttachAttention(t *testing.T) {
	rec := func(sid, text, asked string) attention.Record {
		return attention.Record{
			Kind: attention.KindQuestion, SessionID: sid, ToolUseID: "t-" + text, Branch: "feat/x",
			Header: "Approach", Text: text, AskedAt: asked,
		}
	}
	pipeline := func(status, sid string) DashboardPipeline {
		return DashboardPipeline{Kind: "ship", Branch: "feat/x", Status: status, SessionID: sid}
	}

	cases := []struct {
		name     string
		p        DashboardPipeline
		recs     []attention.Record
		wantText string // "" means attention stays nil
		wantAsk  string
	}{
		{"no records", pipeline(PipelineRunning, "s1"), nil, "", ""},
		{"empty session never matches an empty record session", pipeline(PipelineRunning, ""),
			[]attention.Record{rec("", "q", "2026-10-07T09:55:00Z")}, "", ""},
		{"completed pipeline", pipeline(PipelineCompleted, "s1"),
			[]attention.Record{rec("s1", "q", "2026-10-07T09:55:00Z")}, "", ""},
		{"stalled pipeline", pipeline(PipelineStalled, "s1"),
			[]attention.Record{rec("s1", "q", "2026-10-07T09:55:00Z")}, "", ""},
		{"record with an unparseable time is skipped", pipeline(PipelineRunning, "s1"),
			[]attention.Record{rec("s1", "valid", "2026-10-07T09:50:00Z"), rec("s1", "broken", "yesterday")}, "valid", "2026-10-07T09:50:00Z"},
		{"equal times: the later record wins", pipeline(PipelineRunning, "s1"),
			[]attention.Record{rec("s1", "first", "2026-10-07T09:50:00Z"), rec("s1", "second", "2026-10-07T09:50:00Z")}, "second", "2026-10-07T09:50:00Z"},
		{"newest wins whatever the list order", pipeline(PipelineRunning, "s1"),
			[]attention.Record{rec("s1", "newer", "2026-10-07T09:55:00Z"), rec("s1", "older", "2026-10-07T09:40:00Z")}, "newer", "2026-10-07T09:55:00Z"},
		{"askedAt is written in UTC", pipeline(PipelineRunning, "s1"),
			[]attention.Record{rec("s1", "zoned", "2026-10-07T11:00:00+01:00")}, "zoned", "2026-10-07T10:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.p
			dashboardAttachAttention(&p, tc.recs)
			if tc.wantText == "" {
				if p.Attention != nil {
					t.Errorf("attention = %+v, want nil", *p.Attention)
				}
				return
			}
			if p.Attention == nil {
				t.Fatalf("attention = nil, want text %q", tc.wantText)
			}
			if p.Attention.Text != tc.wantText || p.Attention.AskedAt != tc.wantAsk {
				t.Errorf("attention = %+v, want text %q asked at %q", *p.Attention, tc.wantText, tc.wantAsk)
			}
		})
	}
}

// dashPlanMeta returns a review run plan of three dimensions: security and
// docs in wave 1, perf in wave 2. docs carries stopReason missing.
func dashPlanMeta(shipRunID string) reviewRunMeta {
	return reviewRunMeta{
		Branch: "feat/x", StartedAt: "2026-10-07T08:31:00Z", ShipRunID: shipRunID,
		Waves: [][]string{{"security", "docs"}, {"perf"}},
		Dimensions: []reviewRunMetaDimension{
			{Name: "security", WorkerID: "security", Wave: 1},
			{Name: "docs", WorkerID: "docs", Wave: 1, StopReason: reviewStopMissing},
			{Name: "perf", WorkerID: "perf", Wave: 2},
		},
	}
}

// dashWritePlanRun writes the dashPlanMeta run.meta and a checked-out
// security worker file with one high finding into review ledger folder run.
func dashWritePlanRun(t *testing.T, root, run, shipRunID string, mtime time.Time) {
	t.Helper()
	dashWriteJSON(t, ledgerRunMetaPath(root, run), dashPlanMeta(shipRunID), mtime)
	dashWriteReviewDim(t, root, run, "security", map[string]any{
		"checkinAt": "2026-10-07T08:31:00Z", "checkoutAt": "2026-10-07T08:35:00Z",
		"findings": dashLedgerFindings(t, map[string]any{"severity": "high", "file": "x.go", "line": 4, "rationale": "r"}),
	}, mtime)
}

// TestDashboardSnapshot_ReviewPlanRows checks a standalone review run with a
// plan: one step per planned dimension, a planned dimension with no worker
// file is pending, a stopped one is skipped, the progress total is the
// planned count, and done counts the completed and skipped rows.
func TestDashboardSnapshot_ReviewPlanRows(t *testing.T) {
	root := dashRoot(t)
	dashWritePlanRun(t, root, "review-2026-10-07T08-31-00Z", "", dashNow.Add(-time.Minute))

	p := dashOne(t, root)
	if got, want := dashStepStatuses(p), []string{StepCompleted, StepSkipped, StepPending}; !reflect.DeepEqual(got, want) {
		t.Errorf("step statuses = %v, want %v", got, want)
	}
	wantProgress := DashboardProgress{Done: 2, Total: 3, Current: "perf", Label: "2 of 3 dimensions"}
	if p.Progress != wantProgress {
		t.Errorf("progress = %+v, want %+v", p.Progress, wantProgress)
	}
	if p.Status != PipelineRunning {
		t.Errorf("status = %q, want %q", p.Status, PipelineRunning)
	}
	if len(p.Issues) != 1 || p.Issues[0].Ref != "security" {
		t.Errorf("issues = %+v, want one security finding", p.Issues)
	}
}

// TestDashboardSnapshot_ReviewPlanNested checks a review run with a plan
// nested into its ship: each dimension has its wave and stop reason, and the
// review step detail has the plan totals.
func TestDashboardSnapshot_ReviewPlanNested(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)
	dashWritePlanRun(t, root, dashJoinReview, dashJoinShipID, dashJoinFresh)

	ship := dashOne(t, root)
	d := dashJoinStep(t, ship, "review").Detail
	if d == nil {
		t.Fatal("review detail = nil, want dimensions")
	}
	wantDims := []DashboardDimension{
		{Name: "security", Status: StepCompleted, Findings: 1, Worst: "high", Wave: 1, FindingItems: []DashboardReviewFinding{
			{Text: "r", Severity: "high", File: "x.go", Line: "4"},
		}},
		{Name: "docs", Status: StepSkipped, Wave: 1, Reason: reviewStopMissing, FindingItems: []DashboardReviewFinding{}},
		{Name: "perf", Status: StepPending, Wave: 2, FindingItems: []DashboardReviewFinding{}},
	}
	if !reflect.DeepEqual(d.Dimensions, wantDims) {
		t.Errorf("dimensions = %+v, want %+v", d.Dimensions, wantDims)
	}
	wantPlan := &DashboardReviewPlan{WavesPlanned: 2, WavesRun: 1, DimensionsPlanned: 3, DimensionsRun: 1, NeverStarted: 2}
	if !reflect.DeepEqual(d.ReviewPlan, wantPlan) {
		t.Errorf("reviewPlan = %+v, want %+v", d.ReviewPlan, wantPlan)
	}
}

// TestDashboardSnapshot_ReviewNoPlanHasNoTotals checks that a nested review
// whose run.meta plans no dimension gives dimensions without a wave and no
// reviewPlan totals.
func TestDashboardSnapshot_ReviewNoPlanHasNoTotals(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)
	dashJoinReviewDims(t, root, dashJoinReview)
	dashJoinRunMeta(t, root, dashJoinReview, reviewRunMeta{Branch: "feat/x", StartedAt: "2026-10-07T08:31:00Z", ShipRunID: dashJoinShipID})

	d := dashJoinStep(t, dashOne(t, root), "review").Detail
	if d == nil || d.ReviewPlan != nil {
		t.Fatalf("review detail = %+v, want no reviewPlan", d)
	}
	for _, dim := range d.Dimensions {
		if dim.Wave != 0 || dim.Reason != "" {
			t.Errorf("dimension %+v, want no wave and no reason", dim)
		}
	}
}

// TestDashboardSnapshot_ReviewOnlyRunMetaStall checks the stall rule of a
// review folder: only run.meta written 31 min ago is stalled, only run.meta
// written 1 min ago is running, and an old run.meta with a worker file
// updated 5 min ago is running. It also checks the current step: the first
// pending dimension when no worker runs (security; docs is skipped), and the
// running worker (perf) even though the earlier security dimension is still
// pending.
func TestDashboardSnapshot_ReviewOnlyRunMetaStall(t *testing.T) {
	run := "review-2026-10-07T09-00-00Z"
	cases := []struct {
		name        string
		setup       func(t *testing.T, root string)
		want        string
		wantCurrent string
	}{
		{"only run.meta, 31 min old", func(t *testing.T, root string) {
			dashWriteJSON(t, ledgerRunMetaPath(root, run), dashPlanMeta(""), dashNow.Add(-31*time.Minute))
		}, PipelineStalled, "security"},
		{"only run.meta, 1 min old", func(t *testing.T, root string) {
			dashWriteJSON(t, ledgerRunMetaPath(root, run), dashPlanMeta(""), dashNow.Add(-time.Minute))
		}, PipelineRunning, "security"},
		{"old run.meta, worker file 5 min old", func(t *testing.T, root string) {
			dashWriteJSON(t, ledgerRunMetaPath(root, run), dashPlanMeta(""), dashNow.Add(-31*time.Minute))
			dashWriteReviewDim(t, root, run, "perf", map[string]any{"checkinAt": "2026-10-07T09:50:00Z"}, dashNow.Add(-5*time.Minute))
		}, PipelineRunning, "perf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := dashRoot(t)
			tc.setup(t, root)
			p := dashOne(t, root)
			if p.Status != tc.want {
				t.Errorf("status = %q, want %q", p.Status, tc.want)
			}
			if p.Progress.Current != tc.wantCurrent {
				t.Errorf("progress current = %q, want %q", p.Progress.Current, tc.wantCurrent)
			}
			if p.Progress.Total != 3 {
				t.Errorf("progress total = %d, want the planned count 3", p.Progress.Total)
			}
		})
	}
}

// TestDashboardSnapshot_ReviewRunMetaNoDimensionsNoFiles checks that a
// review folder with a run.meta that plans no dimension and no worker file
// gives no pipeline.
func TestDashboardSnapshot_ReviewRunMetaNoDimensionsNoFiles(t *testing.T) {
	root := dashRoot(t)
	dashJoinRunMeta(t, root, dashJoinReview, reviewRunMeta{Branch: "feat/x", StartedAt: "2026-10-07T08:31:00Z"})
	if repo := dashCollect(t, root); len(repo.Pipelines) != 0 {
		t.Errorf("pipelines = %v, want none", dashJoinKinds(repo.Pipelines))
	}
}

// TestDashboardSnapshot_ReviewAllSkippedCompletes checks that a review run
// whose every planned dimension is skipped is completed, and that its
// completedAt is the folder update time when no worker checked out.
func TestDashboardSnapshot_ReviewAllSkippedCompletes(t *testing.T) {
	root := dashRoot(t)
	run := "review-2026-10-07T09-00-00Z"
	mtime := dashNow.Add(-2 * time.Minute)
	dashWriteJSON(t, ledgerRunMetaPath(root, run), reviewRunMeta{
		Waves:      [][]string{{"docs"}},
		Dimensions: []reviewRunMetaDimension{{Name: "docs", WorkerID: "docs", Wave: 1, StopReason: reviewStopStalled}},
	}, mtime)

	p := dashOne(t, root)
	if p.Status != PipelineCompleted {
		t.Fatalf("status = %q, want %q", p.Status, PipelineCompleted)
	}
	if p.CompletedAt == nil || *p.CompletedAt != dashFormat(mtime) {
		t.Errorf("completedAt = %v, want %s", p.CompletedAt, dashFormat(mtime))
	}
}

// TestDashboardReviewPipeline_UnreadableFolder checks that a ledger folder
// path that is not a folder gives no review pipeline.
func TestDashboardReviewPipeline_UnreadableFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review-2026-10-07T09-00-00Z")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := dashboardReviewPipeline(path, filepath.Base(path)); ok {
		t.Error("ok = true, want false for a path that is not a folder")
	}
}

// TestDashboardSnapshot_ShipCommitResult checks which ship steps carry a
// detail of kind result: only a completed commit step whose result starts
// with commitNothingPrefix. Every other step, status, or result gives none.
func TestDashboardSnapshot_ShipCommitResult(t *testing.T) {
	const cleanTree = commitNothingPrefix + ": the working tree is clean"
	const waveCommits = commitNothingPrefix + ": execute committed 2 wave commit(s)"

	tests := []struct {
		name   string
		step   map[string]any // the step under test, at index 1
		want   *DashboardStepDetail
		stepAt string // name of the step under test
	}{
		{
			name:   "clean tree result",
			step:   map[string]any{"name": "commit", "status": StepCompleted, "result": cleanTree},
			want:   &DashboardStepDetail{Kind: dashboardKindResult, Result: cleanTree},
			stepAt: "commit",
		},
		{
			name:   "wave commits result",
			step:   map[string]any{"name": "commit", "status": StepCompleted, "result": waveCommits},
			want:   &DashboardStepDetail{Kind: dashboardKindResult, Result: waveCommits},
			stepAt: "commit",
		},
		{
			name:   "committed sha result",
			step:   map[string]any{"name": "commit", "status": StepCompleted, "result": "committed abc1234"},
			stepAt: "commit",
		},
		{
			name:   "no result field",
			step:   map[string]any{"name": "commit", "status": StepCompleted},
			stepAt: "commit",
		},
		{
			name:   "result is not a string",
			step:   map[string]any{"name": "commit", "status": StepCompleted, "result": 7},
			stepAt: "commit",
		},
		{
			name:   "prefix not at the start",
			step:   map[string]any{"name": "commit", "status": StepCompleted, "result": "skip: " + cleanTree},
			stepAt: "commit",
		},
		{
			name:   "commit step still in progress",
			step:   map[string]any{"name": "commit", "status": StepInProgress, "result": cleanTree},
			stepAt: "commit",
		},
		{
			name:   "commit step skipped",
			step:   map[string]any{"name": "commit", "status": StepSkipped, "result": cleanTree},
			stepAt: "commit",
		},
		{
			name:   "other step with the same result",
			step:   map[string]any{"name": "pr", "status": StepCompleted, "result": cleanTree},
			stepAt: "pr",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := dashRoot(t)
			dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", map[string]any{
				"branch": "feat/x",
				"steps": []any{
					map[string]any{"name": "execute", "status": StepCompleted, "result": cleanTree},
					tc.step,
					map[string]any{"name": "review", "status": StepPending},
				},
			}, dashNow.Add(-time.Minute))

			p := dashOne(t, root)
			if len(p.Steps) != 3 {
				t.Fatalf("steps = %d, want 3", len(p.Steps))
			}
			if p.Steps[1].Name != tc.stepAt {
				t.Fatalf("step 1 = %q, want %q", p.Steps[1].Name, tc.stepAt)
			}
			if !reflect.DeepEqual(p.Steps[1].Detail, tc.want) {
				t.Errorf("detail = %+v, want %+v", p.Steps[1].Detail, tc.want)
			}
			if p.Steps[0].Detail != nil || p.Steps[2].Detail != nil {
				t.Errorf("other steps carry a detail: execute=%+v review=%+v", p.Steps[0].Detail, p.Steps[2].Detail)
			}
		})
	}
}

// dashStepJSON marshals one step the way the snapshot goes on the wire.
func dashStepJSON(t *testing.T, s DashboardStep) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDashboardStepTimes_Ship(t *testing.T) {
	const started = "2026-10-07T09:00:00Z"
	const completed = "2026-10-07T09:05:30.250Z"

	tests := []struct {
		name          string
		step          map[string]any
		wantStarted   string
		wantCompleted string
	}{
		{
			name:          "both times",
			step:          map[string]any{"name": "execute", "status": StepCompleted, "startedAt": started, "completedAt": completed},
			wantStarted:   started,
			wantCompleted: completed,
		},
		{
			name:        "running step has startedAt only",
			step:        map[string]any{"name": "execute", "status": StepInProgress, "startedAt": started},
			wantStarted: started,
		},
		{
			name: "skipped step with no times",
			step: map[string]any{"name": "execute", "status": StepSkipped},
		},
		{
			name: "empty strings",
			step: map[string]any{"name": "execute", "status": StepCompleted, "startedAt": "", "completedAt": ""},
		},
		{
			name:        "bad completedAt is dropped",
			step:        map[string]any{"name": "execute", "status": StepCompleted, "startedAt": started, "completedAt": "yesterday"},
			wantStarted: started,
		},
		{
			name:          "bad startedAt is dropped",
			step:          map[string]any{"name": "execute", "status": StepCompleted, "startedAt": "2026-10-07 09:00:00", "completedAt": completed},
			wantCompleted: completed,
		},
		{
			name: "time that is not a string",
			step: map[string]any{"name": "execute", "status": StepCompleted, "startedAt": 1791363600, "completedAt": true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := dashRoot(t)
			dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", map[string]any{
				"branch": "feat/x",
				"steps":  []any{tc.step},
			}, dashNow.Add(-time.Minute))

			p := dashOne(t, root)
			if len(p.Steps) != 1 {
				t.Fatalf("steps = %d, want 1", len(p.Steps))
			}
			got := p.Steps[0]
			if got.StartedAt != tc.wantStarted || got.CompletedAt != tc.wantCompleted {
				t.Errorf("times = (%q, %q), want (%q, %q)", got.StartedAt, got.CompletedAt, tc.wantStarted, tc.wantCompleted)
			}
			assertStepTimeKeys(t, got, tc.wantStarted != "", tc.wantCompleted != "")
		})
	}
}

func TestDashboardStepTimes_Execute(t *testing.T) {
	const started = "2026-10-07T09:00:00Z"
	const completed = "2026-10-07T09:20:00Z"

	root := dashRoot(t)
	dashWriteState(t, root, "execute-feat-x-20261007T090000Z.json", map[string]any{
		"branch": "feat/x",
		"waves": []any{
			map[string]any{"number": 1, "status": "completed", "startedAt": started, "completedAt": completed},
			map[string]any{"number": 2, "status": "in_progress", "startedAt": completed},
			map[string]any{"number": 3, "status": "failed", "startedAt": started, "completedAt": "not a time"},
			map[string]any{"number": 4, "status": "pending"},
		},
		"plannedWaves": dashPlannedWaves([2]any{1.0, []any{"1"}}, [2]any{2.0, []any{"2"}}, [2]any{3.0, []any{"3"}}, [2]any{4.0, []any{"4"}}, [2]any{5.0, []any{"5"}}),
	}, dashNow.Add(-time.Minute))

	p := dashOne(t, root)
	if got, want := dashStepNames(p), []string{"wave 1", "wave 2", "wave 3", "wave 4", "wave 5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("step names = %v, want %v", got, want)
	}
	want := []struct{ started, completed string }{
		{started, completed}, // completed wave: both keys
		{completed, ""},      // running wave: startedAt only
		{started, ""},        // bad completedAt is dropped
		{"", ""},             // waves entry with status pending and no times
		{"", ""},             // planned wave with no waves entry: pending step
	}
	for i, w := range want {
		s := p.Steps[i]
		if s.StartedAt != w.started || s.CompletedAt != w.completed {
			t.Errorf("%s times = (%q, %q), want (%q, %q)", s.Name, s.StartedAt, s.CompletedAt, w.started, w.completed)
		}
		assertStepTimeKeys(t, s, w.started != "", w.completed != "")
	}
}

// assertStepTimeKeys checks the JSON text of s: a key is present only when
// wantStarted or wantCompleted is true, and never as null.
func assertStepTimeKeys(t *testing.T, s DashboardStep, wantStarted, wantCompleted bool) {
	t.Helper()
	js := dashStepJSON(t, s)
	if has := strings.Contains(js, `"startedAt"`); has != wantStarted {
		t.Errorf("%s: startedAt key present = %v, want %v in %s", s.Name, has, wantStarted, js)
	}
	if has := strings.Contains(js, `"completedAt"`); has != wantCompleted {
		t.Errorf("%s: completedAt key present = %v, want %v in %s", s.Name, has, wantCompleted, js)
	}
	if strings.Contains(js, "null") {
		t.Errorf("%s: JSON carries null: %s", s.Name, js)
	}
}

func TestDashboardStepTimes_StepTime(t *testing.T) {
	tests := []struct{ in, want string }{
		{"2026-10-07T09:00:00Z", "2026-10-07T09:00:00Z"},
		{"2026-10-07T09:00:00.123456789+02:00", "2026-10-07T09:00:00.123456789+02:00"},
		{"", ""},
		{"yesterday", ""},
		{"2026-10-07", ""},
		{" 2026-10-07T09:00:00Z", ""},
	}
	for _, tc := range tests {
		if got := dashboardStepTime(tc.in); got != tc.want {
			t.Errorf("dashboardStepTime(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
