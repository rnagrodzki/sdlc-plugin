package tools

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
)

// dashStateIssues returns the data.issues[] list that the state actions write
// for issues, built through the same append function.
func dashStateIssues(issues ...StateIssue) []any {
	data := map[string]any{}
	for _, is := range issues {
		execAppendIssue(data, is)
	}
	list, _ := data["issues"].([]any)
	return list
}

// dashLedgerFindings encodes findings the way a review ledger file stores
// them: a JSON string holding a list.
func dashLedgerFindings(t *testing.T, findings ...map[string]any) string {
	t.Helper()
	b, err := json.Marshal(findings)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestDashboardIssues_Severity checks every row of the severity table.
func TestDashboardIssues_Severity(t *testing.T) {
	cases := []struct{ in, want string }{
		{"error", "high"},
		{"warning", "medium"},
		{"fatal", "info"},
		{"", "info"},
	}
	// Every value of the review vocabulary stays as it is.
	for _, sev := range dimensions.ValidSeverities {
		cases = append(cases, struct{ in, want string }{sev, sev})
	}
	for _, tc := range cases {
		if got := dashboardSeverity(tc.in); got != tc.want {
			t.Errorf("dashboardSeverity(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestDashboardIssues_Sources checks the fields of the issue of each source,
// read through a full snapshot collection.
func TestDashboardIssues_Sources(t *testing.T) {
	fresh := dashNow.Add(-time.Minute)
	started := "2026-10-07T09:00:00Z"

	t.Run("review finding splits the location from the text", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteReviewDim(t, root, "review-2026-10-07T09-00-00Z", "security", map[string]any{
			"checkinAt": started, "checkoutAt": "2026-10-07T09:05:00Z",
			"findings": dashLedgerFindings(t,
				map[string]any{"severity": "high", "file": "internal/x.go", "line": 42, "rationale": "token check skips expiry"},
				map[string]any{"severity": "medium", "file": "internal/y.go", "line": "12-14", "rationale": "range as text"},
				map[string]any{"severity": "low", "file": "internal/z.go", "line": 0, "rationale": "line zero"},
				map[string]any{"severity": "blocker", "file": "internal/w.go", "rationale": "no line, unknown severity"},
				map[string]any{"severity": "", "file": "internal/v.go", "line": "", "rationale": "  padded  "},
			),
		}, fresh)
		got := dashOne(t, root).Issues
		want := []DashboardIssue{
			{Source: "review", Severity: "high", Text: "token check skips expiry", File: "internal/x.go", Line: "42", Ref: "security"},
			{Source: "review", Severity: "medium", Text: "range as text", File: "internal/y.go", Line: "12-14", Ref: "security"},
			{Source: "review", Severity: "low", Text: "line zero", File: "internal/z.go", Line: "", Ref: "security"},
			{Source: "review", Severity: "info", Text: "no line, unknown severity", File: "internal/w.go", Line: "", Ref: "security"},
			{Source: "review", Severity: "info", Text: "padded", File: "internal/v.go", Line: "", Ref: "security"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("issues = %+v, want %+v", got, want)
		}
	})

	t.Run("failed step gives a step issue with the step name as ref", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", map[string]any{
			"branch": "feat/x",
			"steps": []any{
				map[string]any{"name": "pr", "status": StepFailed, "error": "gh auth failed", "startedAt": started},
			},
		}, fresh)
		got := dashOne(t, root).Issues
		want := []DashboardIssue{{Source: "step", Severity: "high", Text: "gh auth failed", Ref: "pr"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("issues = %+v, want %+v", got, want)
		}
	})

	t.Run("failed wave gives a wave issue with the wave name as ref", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, "execute-feat-x-20261007T090000Z.json", map[string]any{
			"branch": "feat/x",
			"waves":  []any{map[string]any{"number": 4, "status": "failed"}},
		}, fresh)
		got := dashOne(t, root).Issues
		want := []DashboardIssue{{Source: "wave", Severity: "high", Text: "failed", Ref: "wave 4"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("issues = %+v, want %+v", got, want)
		}
	})

	t.Run("state issue takes its ref from task, else step, else wave", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, "execute-feat-x-20261007T090000Z.json", map[string]any{
			"branch": "feat/x",
			"waves":  []any{map[string]any{"number": 2, "status": "in_progress"}},
			"issues": dashStateIssues(
				StateIssue{Wave: 2, TaskID: "4", Step: "review", Severity: "warning", Category: "drift", Summary: "Task wins", Detail: "over step"},
				StateIssue{Wave: 2, Step: "review", Severity: "warning", Category: "drift", Summary: "Step wins"},
				StateIssue{Wave: 2, Severity: "info", Category: "drift", Summary: "Wave only"},
				StateIssue{Severity: "error", Category: "cross-read", Summary: "No ref"},
			),
		}, fresh)
		got := dashOne(t, root).Issues
		want := []DashboardIssue{
			{Source: "state", Severity: "medium", Text: "Task wins: over step", Ref: "4"},
			{Source: "state", Severity: "medium", Text: "Step wins", Ref: "review"},
			{Source: "state", Severity: "info", Text: "Wave only", Ref: "wave 2"},
			{Source: "state", Severity: "high", Text: "No ref", Ref: ""},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("issues = %+v, want %+v", got, want)
		}
	})

	t.Run("task row gives a task issue only when failed with an error", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, "execute-feat-x-20261007T090000Z.json", map[string]any{
			"branch": "feat/x",
			"waves": []any{map[string]any{"number": 1, "status": "in_progress", "tasks": []any{
				map[string]any{"id": "1", "status": StepFailed, "error": "build broke"},
				map[string]any{"id": "2", "status": StepFailed, "error": ""},
				map[string]any{"id": "3", "status": "skipped-dependency", "error": "dependency 1 failed"},
				map[string]any{"id": "4", "status": StepCompleted, "error": "concern text"},
				map[string]any{"id": "", "status": StepFailed, "error": "no id"},
			}}},
		}, fresh)
		got := dashOne(t, root).Issues
		want := []DashboardIssue{{Source: "task", Severity: "high", Text: "build broke", Ref: "1"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("issues = %+v, want %+v", got, want)
		}
	})
}

// TestDashboardIssues_Dedupe checks every row of the dedupe table: a state
// issue is dropped only when a step, wave, or task issue has its ref.
func TestDashboardIssues_Dedupe(t *testing.T) {
	fresh := dashNow.Add(-time.Minute)
	started := "2026-10-07T09:00:00Z"
	failedStep := func(name, errText string) map[string]any {
		return map[string]any{"name": name, "status": StepFailed, "error": errText, "startedAt": started}
	}

	cases := []struct {
		name string
		file string
		data map[string]any
		want []DashboardIssue
	}{
		{
			name: "ship-fail is dropped when a step issue has the step as ref",
			file: "ship-feat-x-20261007T090000Z.json",
			data: map[string]any{
				"steps":  []any{failedStep("pr", "gh auth failed")},
				"issues": dashStateIssues(StateIssue{Step: "pr", Severity: "error", Category: "ship-fail", Summary: "Step pr failed", Detail: "gh auth failed"}),
			},
			want: []DashboardIssue{{Source: "step", Severity: "high", Text: "gh auth failed", Ref: "pr"}},
		},
		{
			name: "ship-fail stays when no step issue has the step as ref",
			file: "ship-feat-x-20261007T090000Z.json",
			data: map[string]any{
				"steps": []any{
					failedStep("pr", "gh auth failed"),
					map[string]any{"name": "review", "status": StepInProgress, "startedAt": started},
				},
				"issues": dashStateIssues(
					StateIssue{Step: "pr", Severity: "error", Category: "ship-fail", Summary: "Step pr failed", Detail: "gh auth failed"},
					StateIssue{Step: "review", Severity: "error", Category: "ship-fail", Summary: "Step review failed", Detail: "boom"},
				),
			},
			want: []DashboardIssue{
				{Source: "step", Severity: "high", Text: "gh auth failed", Ref: "pr"},
				{Source: "state", Severity: "high", Text: "Step review failed: boom", Ref: "review"},
			},
		},
		{
			name: "wave-fail is dropped when a wave issue has the wave as ref",
			file: "execute-feat-x-20261007T090000Z.json",
			data: map[string]any{
				"waves":  []any{map[string]any{"number": 2, "status": "failed"}},
				"issues": dashStateIssues(StateIssue{Wave: 2, Severity: "error", Category: "wave-fail", Summary: "Wave 2 failed", Detail: "timed out"}),
			},
			want: []DashboardIssue{{Source: "wave", Severity: "high", Text: "failed", Ref: "wave 2"}},
		},
		{
			name: "wave-fail stays when no wave issue has the wave as ref",
			file: "execute-feat-x-20261007T090000Z.json",
			data: map[string]any{
				"waves":  []any{map[string]any{"number": 2, "status": "completed"}},
				"issues": dashStateIssues(StateIssue{Wave: 2, Severity: "error", Category: "wave-fail", Summary: "Wave 2 failed", Detail: "timed out"}),
			},
			want: []DashboardIssue{{Source: "state", Severity: "high", Text: "Wave 2 failed: timed out", Ref: "wave 2"}},
		},
		{
			name: "task-fail is dropped when a task issue has the task as ref",
			file: "execute-feat-x-20261007T090000Z.json",
			data: map[string]any{
				"waves": []any{map[string]any{"number": 1, "status": "in_progress", "tasks": []any{
					map[string]any{"id": "3", "status": StepFailed, "error": "build broke"},
				}}},
				"issues": dashStateIssues(StateIssue{Wave: 1, TaskID: "3", Severity: "error", Category: "task-fail", Summary: "Task 3 failed", Detail: "build broke"}),
			},
			want: []DashboardIssue{{Source: "task", Severity: "high", Text: "build broke", Ref: "3"}},
		},
		{
			name: "task-fail stays for a task skipped because of a failed dependency",
			file: "execute-feat-x-20261007T090000Z.json",
			data: map[string]any{
				"waves": []any{map[string]any{"number": 1, "status": "in_progress", "tasks": []any{
					map[string]any{"id": "5", "status": "skipped-dependency", "error": "dependency 3 failed"},
				}}},
				"issues": dashStateIssues(StateIssue{Wave: 1, TaskID: "5", Severity: "error", Category: "task-fail", Summary: "Task 5 skipped (dependency failed)", Detail: "dependency 3 failed"}),
			},
			want: []DashboardIssue{{Source: "state", Severity: "high", Text: "Task 5 skipped (dependency failed): dependency 3 failed", Ref: "5"}},
		},
		{
			name: "task-fail stays for a failed task row with no error text",
			file: "execute-feat-x-20261007T090000Z.json",
			data: map[string]any{
				"waves": []any{map[string]any{"number": 1, "status": "in_progress", "tasks": []any{
					map[string]any{"id": "6", "status": StepFailed, "error": ""},
				}}},
				"issues": dashStateIssues(StateIssue{Wave: 1, TaskID: "6", Severity: "error", Category: "task-fail", Summary: "Task 6 failed"}),
			},
			want: []DashboardIssue{{Source: "state", Severity: "high", Text: "Task 6 failed", Ref: "6"}},
		},
		{
			name: "failed wave keeps its drift, cross-read, and concern issues",
			file: "execute-feat-x-20261007T090000Z.json",
			data: map[string]any{
				"waves": []any{map[string]any{"number": 2, "status": "failed", "tasks": []any{
					map[string]any{"id": "3", "status": StepFailed, "error": "boom"},
				}}},
				"issues": dashStateIssues(
					StateIssue{Wave: 2, Severity: "error", Category: "wave-fail", Summary: "Wave 2 failed"},
					StateIssue{Wave: 2, TaskID: "3", Severity: "error", Category: "task-fail", Summary: "Task 3 failed", Detail: "boom"},
					StateIssue{Wave: 2, TaskID: "3", Severity: "warning", Category: "drift", Summary: "Drift"},
					StateIssue{Severity: "warning", Category: "cross-read", Summary: "CLI evidence read failed: x"},
					StateIssue{Wave: 2, TaskID: "3", Severity: "warning", Category: "done-with-concerns", Summary: "Task 3 completed with concerns", Detail: "slow"},
					StateIssue{Wave: 2, Severity: "info", Category: "drift", Summary: "Note"},
				),
			},
			want: []DashboardIssue{
				{Source: "wave", Severity: "high", Text: "failed", Ref: "wave 2"},
				{Source: "task", Severity: "high", Text: "boom", Ref: "3"},
				{Source: "state", Severity: "medium", Text: "Drift", Ref: "3"},
				{Source: "state", Severity: "medium", Text: "CLI evidence read failed: x", Ref: ""},
				{Source: "state", Severity: "medium", Text: "Task 3 completed with concerns: slow", Ref: "3"},
				{Source: "state", Severity: "info", Text: "Note", Ref: "wave 2"},
			},
		},
		{
			name: "failed ship step keeps a drift issue that names the same step",
			file: "ship-feat-x-20261007T090000Z.json",
			data: map[string]any{
				"steps":  []any{failedStep("review", "boom")},
				"issues": dashStateIssues(StateIssue{Step: "review", Severity: "info", Category: "drift", Summary: "Same step"}),
			},
			want: []DashboardIssue{
				{Source: "step", Severity: "high", Text: "boom", Ref: "review"},
				{Source: "state", Severity: "info", Text: "Same step", Ref: "review"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := dashRoot(t)
			tc.data["branch"] = "feat/x"
			dashWriteState(t, root, tc.file, tc.data, fresh)
			got := dashOne(t, root).Issues
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("issues = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestDashboardIssues_Stalled checks that a stalled pipeline of each kind gets
// one pipeline issue whose text does not change with the age of the pipeline.
func TestDashboardIssues_Stalled(t *testing.T) {
	const wantText = "no update for 30+ min"
	started := "2026-10-07T09:00:00Z"

	kinds := []struct {
		name  string
		setup func(t *testing.T, root string, mtime time.Time)
	}{
		{"ship", func(t *testing.T, root string, mtime time.Time) {
			dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", map[string]any{
				"branch": "feat/x", "steps": dashSteps(StepInProgress),
			}, mtime)
		}},
		{"execute", func(t *testing.T, root string, mtime time.Time) {
			dashWriteState(t, root, "execute-feat-x-20261007T090000Z.json", map[string]any{
				"branch": "feat/x", "waves": []any{map[string]any{"number": 1, "status": "in_progress"}},
			}, mtime)
		}},
		{"plan", func(t *testing.T, root string, mtime time.Time) {
			dashWriteState(t, root, "plan-main-20261007T090000Z.json", map[string]any{
				"planIntegrity": map[string]any{"skillInvoked": started},
				"checkpoint":    map[string]any{"step": "3"},
			}, mtime)
		}},
		{"review", func(t *testing.T, root string, mtime time.Time) {
			dashWriteReviewDim(t, root, "review-2026-10-07T09-00-00Z", "docs", map[string]any{"checkinAt": started}, mtime)
		}},
	}

	for _, k := range kinds {
		t.Run(k.name+" gives one pipeline issue with a text that holds no clock value", func(t *testing.T) {
			var texts []string
			for _, age := range []time.Duration{45 * time.Minute, 3 * time.Hour} {
				root := dashRoot(t)
				k.setup(t, root, dashNow.Add(-age))
				p := dashOne(t, root)
				if p.Status != PipelineStalled {
					t.Fatalf("status = %q, want %q", p.Status, PipelineStalled)
				}
				var found []DashboardIssue
				for _, is := range p.Issues {
					if is.Source == "pipeline" {
						found = append(found, is)
					}
				}
				want := []DashboardIssue{{Source: "pipeline", Severity: "medium", Text: wantText}}
				if !reflect.DeepEqual(found, want) {
					t.Errorf("age %v: pipeline issues = %+v, want %+v", age, found, want)
				}
				for _, is := range found {
					texts = append(texts, is.Text)
				}
			}
			if len(texts) == 2 && texts[0] != texts[1] {
				t.Errorf("text changes with age: %q then %q", texts[0], texts[1])
			}
		})
	}

	t.Run("running pipeline that is not stalled gives no pipeline issue", func(t *testing.T) {
		root := dashRoot(t)
		kinds[0].setup(t, root, dashNow.Add(-time.Minute))
		if got := dashOne(t, root).Issues; len(got) != 0 {
			t.Errorf("issues = %+v, want none", got)
		}
	})

	t.Run("stalled pipeline keeps its other issues", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, "ship-feat-x-20261007T090000Z.json", map[string]any{
			"branch": "feat/x",
			"steps": []any{
				map[string]any{"name": "pr", "status": StepFailed, "error": "gh auth failed", "startedAt": started},
				map[string]any{"name": "verify-pipeline", "status": StepInProgress, "startedAt": started},
			},
		}, dashNow.Add(-45*time.Minute))
		got := dashOne(t, root).Issues
		want := []DashboardIssue{
			{Source: "step", Severity: "high", Text: "gh auth failed", Ref: "pr"},
			{Source: "pipeline", Severity: "medium", Text: wantText},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("issues = %+v, want %+v", got, want)
		}
	})
}

// TestDashboardIssues_ReviewFindings checks the findings detail of the steps of
// a standalone review run.
func TestDashboardIssues_ReviewFindings(t *testing.T) {
	fresh := dashNow.Add(-time.Minute)
	run := "review-2026-10-07T09-00-00Z"

	t.Run("completed dimension lists the findings of its own ref", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteReviewDim(t, root, run, "a-correctness", map[string]any{
			"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:05:00Z",
			"findings": dashLedgerFindings(t,
				map[string]any{"severity": "medium", "file": "internal/payout/batch.go", "line": 88, "rationale": "off-by-one in batch cursor pagination"},
			),
		}, fresh)
		dashWriteReviewDim(t, root, run, "b-security", map[string]any{
			"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:05:00Z",
			"findings": dashLedgerFindings(t,
				map[string]any{"severity": "high", "file": "internal/x.go", "line": "12-14", "rationale": "token check skips expiry"},
				map[string]any{"severity": "low", "file": "internal/y.go", "rationale": "no line"},
			),
		}, fresh)
		p := dashOne(t, root)
		want := []DashboardStep{
			{Name: "a-correctness", Status: StepCompleted, Detail: &DashboardStepDetail{
				Kind:     dashboardKindFindings,
				Findings: []DashboardReviewFinding{{Text: "off-by-one in batch cursor pagination", Severity: "medium", File: "internal/payout/batch.go", Line: "88"}},
			}},
			{Name: "b-security", Status: StepCompleted, Detail: &DashboardStepDetail{
				Kind: dashboardKindFindings,
				Findings: []DashboardReviewFinding{
					{Text: "token check skips expiry", Severity: "high", File: "internal/x.go", Line: "12-14"},
					{Text: "no line", Severity: "low", File: "internal/y.go", Line: ""},
				},
			}},
		}
		if !reflect.DeepEqual(p.Steps, want) {
			t.Errorf("steps = %+v, want %+v", p.Steps, want)
		}
	})

	t.Run("completed dimension with no finding has the kind and no list", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteReviewDim(t, root, run, "docs", map[string]any{
			"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:05:00Z",
		}, fresh)
		p := dashOne(t, root)
		if len(p.Steps) != 1 || p.Steps[0].Detail == nil {
			t.Fatalf("steps = %+v, want one step with a detail", p.Steps)
		}
		if d := p.Steps[0].Detail; d.Kind != dashboardKindFindings || d.Findings != nil {
			t.Errorf("detail = %+v, want kind %q and no list", d, dashboardKindFindings)
		}
		b, err := json.Marshal(p.Steps[0])
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"name":"docs","status":"completed","detail":{"kind":"findings"}}`; string(b) != want {
			t.Errorf("step JSON = %s, want %s", b, want)
		}
	})

	t.Run("in-progress dimension has no detail but its findings stay issues", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteReviewDim(t, root, run, "docs", map[string]any{
			"checkinAt": "2026-10-07T09:00:00Z",
			"findings":  dashLedgerFindings(t, map[string]any{"severity": "low", "file": "a.md", "line": 3, "rationale": "typo"}),
		}, fresh)
		p := dashOne(t, root)
		if len(p.Steps) != 1 || p.Steps[0].Status != StepInProgress || p.Steps[0].Detail != nil {
			t.Errorf("steps = %+v, want one in_progress step with no detail", p.Steps)
		}
		want := []DashboardIssue{{Source: "review", Severity: "low", Text: "typo", File: "a.md", Line: "3", Ref: "docs"}}
		if !reflect.DeepEqual(p.Issues, want) {
			t.Errorf("issues = %+v, want %+v", p.Issues, want)
		}
	})

	t.Run("issue and step JSON follow the contract", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteReviewDim(t, root, run, "security", map[string]any{
			"checkinAt": "2026-10-07T09:00:00Z", "checkoutAt": "2026-10-07T09:05:00Z",
			"findings": dashLedgerFindings(t, map[string]any{"severity": "high", "file": "internal/x.go", "line": 42, "rationale": "token check skips expiry"}),
		}, fresh)
		p := dashOne(t, root)
		issue, err := json.Marshal(p.Issues[0])
		if err != nil {
			t.Fatal(err)
		}
		wantIssue := `{"source":"review","severity":"high","text":"token check skips expiry","file":"internal/x.go","line":"42","ref":"security"}`
		if string(issue) != wantIssue {
			t.Errorf("issue JSON = %s, want %s", issue, wantIssue)
		}
		step, err := json.Marshal(p.Steps[0])
		if err != nil {
			t.Fatal(err)
		}
		wantStep := `{"name":"security","status":"completed","detail":{"kind":"findings","findings":[{"text":"token check skips expiry","severity":"high","file":"internal/x.go","line":"42"}]}}`
		if string(step) != wantStep {
			t.Errorf("step JSON = %s, want %s", step, wantStep)
		}
	})
}
