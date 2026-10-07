package tools

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// dashExecFile is the execute state file name of the execute detail tests.
const dashExecFile = "execute-feat-x-20261007T080000Z.json"

// dashExecOne writes one execute state and returns its collected pipeline.
func dashExecOne(t *testing.T, root string, data map[string]any) DashboardPipeline {
	t.Helper()
	if _, ok := data["branch"]; !ok {
		data["branch"] = "feat/x"
	}
	dashWriteState(t, root, dashExecFile, data, dashNow.Add(-time.Minute))
	return dashOne(t, root)
}

// dashServerState writes an empty <taskID>.server.json for runID under root.
func dashServerState(t *testing.T, root, runID, taskID string) {
	t.Helper()
	path := filepath.Join(root, paths.DataDir, paths.RunsSubdir, runID, "progress", taskID+".server.json")
	dashWriteJSON(t, path, map[string]any{}, dashNow.Add(-time.Minute))
}

// dashStep returns the step called name, or fails the test.
func dashStep(t *testing.T, p DashboardPipeline, name string) DashboardStep {
	t.Helper()
	for _, s := range p.Steps {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no step %q in %+v", name, p.Steps)
	return DashboardStep{}
}

// dashNoNull fails the test when the JSON of v holds a null value.
func dashNoNull(t *testing.T, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "null") {
		t.Errorf("JSON holds null: %s", b)
	}
}

func TestDashboardExecuteDetail_StartedWave(t *testing.T) {
	root := dashRoot(t)
	dashServerState(t, root, "run-1", "T2")
	p := dashExecOne(t, root, map[string]any{
		"startedAt": "2026-10-07T08:00:00Z",
		"plannedTasks": []any{
			map[string]any{"id": "1", "name": "Plan one"},
			map[string]any{"id": "2", "name": "Plan two"},
			map[string]any{"id": "3", "name": "Plan three"},
		},
		"waves": []any{map[string]any{
			"number": 1, "status": "in_progress", "runId": "run-1", "committedSha": nil,
			"planned": []any{
				map[string]any{"id": "1", "name": "Wave one"},
				map[string]any{"id": "T2", "name": ""},
			},
			"tasks": []any{map[string]any{"id": "1", "name": "Row one", "status": "completed"}},
		}},
	})

	wantWave := DashboardWave{Number: 1, Status: "in_progress", CommittedSHA: "", Tasks: []DashboardTask{
		{ID: "1", Name: "Row one", Status: "completed"},
		{ID: "T2", Name: "Plan two", Status: StepInProgress},
	}}
	step := dashStep(t, p, "wave 1")
	if step.Detail == nil || step.Detail.Kind != dashboardKindWaves {
		t.Fatalf("wave step detail = %+v, want kind %q", step.Detail, dashboardKindWaves)
	}
	if !reflect.DeepEqual(step.Detail.Waves, []DashboardWave{wantWave}) {
		t.Errorf("wave step waves = %+v, want %+v", step.Detail.Waves, wantWave)
	}
	if step.Detail.Queued != nil {
		t.Errorf("wave step queued = %+v, want absent", step.Detail.Queued)
	}

	wantQueued := []DashboardTask{{ID: "3", Name: "Plan three", Status: StepPending}}
	if len(p.Steps) != 2 || p.Steps[1].Name != dashboardQueuedStep {
		t.Fatalf("steps = %+v, want wave 1 then queued", p.Steps)
	}
	q := p.Steps[1]
	if q.Status != StepPending || q.Detail == nil || q.Detail.Kind != dashboardKindWaves {
		t.Fatalf("queued step = %+v, want pending with kind waves", q)
	}
	if q.Detail.Waves != nil {
		t.Errorf("queued step waves = %+v, want absent", q.Detail.Waves)
	}
	if !reflect.DeepEqual(q.Detail.Queued, wantQueued) {
		t.Errorf("queued = %+v, want %+v", q.Detail.Queued, wantQueued)
	}

	all := p.join.execDetail
	if all == nil || all.Kind != dashboardKindWaves {
		t.Fatalf("join.execDetail = %+v, want kind waves", all)
	}
	if !reflect.DeepEqual(all.Waves, []DashboardWave{wantWave}) || !reflect.DeepEqual(all.Queued, wantQueued) {
		t.Errorf("join.execDetail = %+v, want all waves and queued", all)
	}
	if want := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC); !p.join.startedAt.Equal(want) {
		t.Errorf("join.startedAt = %v, want %v", p.join.startedAt, want)
	}
	dashNoNull(t, p.Steps)
	dashNoNull(t, all)
}

func TestDashboardExecuteDetail_WaveZeroCommitted(t *testing.T) {
	root := dashRoot(t)
	p := dashExecOne(t, root, map[string]any{
		"waves": []any{
			map[string]any{"number": 0, "status": "completed", "committedSha": "abc123", "tasks": []any{
				map[string]any{"id": "1", "status": "completed"},
			}},
			map[string]any{"number": 1, "status": "pending"},
		},
	})
	step := dashStep(t, p, "wave 0")
	want := []DashboardWave{{Number: 0, Status: "completed", CommittedSHA: "abc123", Tasks: []DashboardTask{
		{ID: "1", Name: "", Status: "completed"},
	}}}
	if step.Detail == nil || !reflect.DeepEqual(step.Detail.Waves, want) {
		t.Errorf("wave 0 detail = %+v, want %+v", step.Detail, want)
	}
	empty := dashStep(t, p, "wave 1")
	if empty.Detail == nil || len(empty.Detail.Waves) != 1 || empty.Detail.Waves[0].Tasks == nil {
		t.Fatalf("wave 1 detail = %+v, want one wave with [] tasks", empty.Detail)
	}
	if empty.Detail.Waves[0].CommittedSHA != "" {
		t.Errorf("absent sha = %q, want \"\"", empty.Detail.Waves[0].CommittedSHA)
	}
	if !strings.Contains(string(dashMarshal(t, empty.Detail)), `"tasks":[]`) {
		t.Errorf("wave 1 JSON = %s, want \"tasks\":[]", dashMarshal(t, empty.Detail))
	}
	dashNoNull(t, p.Steps)
}

// dashMarshal returns the JSON of v.
func dashMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDashboardExecuteDetail_TaskNameSources(t *testing.T) {
	root := dashRoot(t)
	p := dashExecOne(t, root, map[string]any{
		"plannedTasks": []any{
			map[string]any{"id": "1", "name": "plan 1"},
			map[string]any{"id": "2", "name": "plan 2"},
			map[string]any{"id": "3", "name": "plan 3"},
		},
		"waves": []any{map[string]any{
			"number": 1, "status": "in_progress",
			"planned": []any{
				map[string]any{"id": "1", "name": "wave 1 name"},
				map[string]any{"id": "2", "name": "wave 2 name"},
				map[string]any{"id": "3", "name": ""},
				map[string]any{"id": "4", "name": ""},
			},
			"tasks": []any{
				map[string]any{"id": "1", "name": "row 1", "status": "completed"},
				map[string]any{"id": "2", "status": "failed"},
			},
		}},
	})
	got := dashStep(t, p, "wave 1").Detail.Waves[0].Tasks
	want := []DashboardTask{
		{ID: "1", Name: "row 1", Status: "completed"},    // task row
		{ID: "2", Name: "wave 2 name", Status: "failed"}, // wave planned list
		{ID: "3", Name: "plan 3", Status: StepPending},   // plannedTasks
		{ID: "4", Name: "", Status: StepPending},         // no source: id only
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %+v, want %+v", got, want)
	}
	for _, s := range p.Steps {
		if s.Name == dashboardQueuedStep {
			t.Errorf("queued step present, want none: every planned task is in a wave")
		}
	}
}

func TestDashboardExecuteDetail_TaskStatus(t *testing.T) {
	root := dashRoot(t)
	dashServerState(t, root, "run-1", "2")
	dashServerState(t, root, "run-1", "3")
	dashWriteJSON(t, filepath.Join(root, paths.DataDir, "progress", "4.server.json"), map[string]any{}, dashNow)

	cases := []struct {
		name   string
		row    map[string]any
		runID  string
		taskID string
		want   string
	}{
		{"row status wins over a server file", map[string]any{"status": "failed"}, "run-1", "2", "failed"},
		{"no row, server file exists", nil, "run-1", "2", StepInProgress},
		{"row without status, server file exists", map[string]any{"id": "3"}, "run-1", "3", StepInProgress},
		{"no row, no server file", nil, "run-1", "9", StepPending},
		{"run id that is not a bare name", nil, "..", "4", StepPending},
		{"task id that is not a bare name", nil, "run-1", "../run-1/progress/2", StepPending},
		{"empty run id", nil, "", "2", StepPending},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := dashboardTaskStatus(c.row, root, c.runID, c.taskID); got != c.want {
				t.Errorf("dashboardTaskStatus = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDashboardExecuteDetail_OldRunWithoutPlannedTasks(t *testing.T) {
	root := dashRoot(t)
	p := dashExecOne(t, root, map[string]any{
		"plannedTaskIds": []any{"1", "2", "3"},
		"waves": []any{map[string]any{
			"number": 1, "status": "completed",
			"tasks": []any{
				map[string]any{"id": "T1", "status": "completed"},
				map[string]any{"id": "2", "status": "completed"},
			},
		}},
	})
	if len(p.Steps) != 1 {
		t.Fatalf("steps = %+v, want only wave 1 (no queued step without plannedTasks)", p.Steps)
	}
	want := []DashboardTask{
		{ID: "T1", Name: "", Status: "completed"},
		{ID: "2", Name: "", Status: "completed"},
	}
	if got := p.Steps[0].Detail.Waves[0].Tasks; !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %+v, want %+v", got, want)
	}
	if p.join.execDetail == nil || p.join.execDetail.Queued != nil {
		t.Errorf("join.execDetail = %+v, want no queued", p.join.execDetail)
	}
	dashNoNull(t, p.Steps)
}

func TestDashboardExecuteDetail_QueuedNormalizesIDs(t *testing.T) {
	root := dashRoot(t)
	p := dashExecOne(t, root, map[string]any{
		"plannedTasks": []any{
			map[string]any{"id": "1", "name": "one"},
			map[string]any{"id": "2", "name": "two"},
		},
		"waves": []any{map[string]any{
			"number": 1, "status": "in_progress",
			"tasks": []any{map[string]any{"id": "T1", "status": "completed"}},
		}},
	})
	want := []DashboardTask{{ID: "2", Name: "two", Status: StepPending}}
	q := dashStep(t, p, dashboardQueuedStep)
	if !reflect.DeepEqual(q.Detail.Queued, want) {
		t.Errorf("queued = %+v, want %+v (T1 matches plan id 1)", q.Detail.Queued, want)
	}
	if got := dashStep(t, p, "wave 1").Detail.Waves[0].Tasks[0].Name; got != "one" {
		t.Errorf("T1 name = %q, want the plannedTasks name of id 1", got)
	}
}

func TestDashboardExecuteDetail_NoWavesAllQueued(t *testing.T) {
	root := dashRoot(t)
	p := dashExecOne(t, root, map[string]any{
		"waves":        []any{},
		"plannedTasks": []any{map[string]any{"id": "1", "name": "one"}},
	})
	if len(p.Steps) != 1 || p.Steps[0].Name != dashboardQueuedStep {
		t.Fatalf("steps = %+v, want only the queued step", p.Steps)
	}
	if p.join.execDetail.Waves != nil {
		t.Errorf("join waves = %+v, want absent", p.join.execDetail.Waves)
	}
	dashNoNull(t, p.Steps)
	dashNoNull(t, p.join.execDetail)
}

func TestDashboardExecuteDetail_CommitWavesAndSession(t *testing.T) {
	cases := []struct {
		name        string
		commitWaves any
		session     any
		wantCommit  bool
		wantSession string
	}{
		{"commitWaves absent, no session", nil, nil, true, ""},
		{"commitWaves true", "true", "sess-1", true, "sess-1"},
		{"commitWaves false", "false", "sess-2", false, "sess-2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := dashRoot(t)
			data := map[string]any{"waves": []any{}}
			if c.commitWaves != nil {
				data["commitWaves"] = c.commitWaves
			}
			if c.session != nil {
				data["sessionId"] = c.session
			}
			p := dashExecOne(t, root, data)
			if p.CommitWaves == nil || *p.CommitWaves != c.wantCommit {
				t.Errorf("commitWaves = %v, want %v", p.CommitWaves, c.wantCommit)
			}
			if p.SessionID != c.wantSession {
				t.Errorf("sessionId = %q, want %q", p.SessionID, c.wantSession)
			}
		})
	}
}
