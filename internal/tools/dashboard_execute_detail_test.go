package tools

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
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

// dashPlannedWaves builds a plannedWaves state value from {number, taskIds}
// pairs, in the shape execute_state init stores.
func dashPlannedWaves(pairs ...[2]any) []any {
	out := []any{}
	for _, p := range pairs {
		out = append(out, map[string]any{"number": p[0], "taskIds": p[1]})
	}
	return out
}

// dashStepNames returns the step names of p in order.
func dashStepNames(p DashboardPipeline) []string {
	names := []string{}
	for _, s := range p.Steps {
		names = append(names, s.Name)
	}
	return names
}

// TestDashboardExecuteDetail_PlannedWavesBeforeFirstStart checks that an
// execute run with no started wave shows each planned wave as a pending step
// with its task names, and no queued step.
func TestDashboardExecuteDetail_PlannedWavesBeforeFirstStart(t *testing.T) {
	root := dashRoot(t)
	p := dashExecOne(t, root, map[string]any{
		"waves": []any{},
		"plannedTasks": []any{
			map[string]any{"id": "1", "name": "one"},
			map[string]any{"id": "2", "name": "two"},
			map[string]any{"id": "3", "name": "three"},
		},
		"plannedWaves": dashPlannedWaves(
			[2]any{1, []any{"1", "2"}},
			[2]any{2, []any{"3"}},
		),
	})

	if want := []string{"wave 1", "wave 2"}; !reflect.DeepEqual(dashStepNames(p), want) {
		t.Fatalf("steps = %v, want %v and no queued step", dashStepNames(p), want)
	}
	want := []DashboardWave{
		{Number: 1, Status: StepPending, Tasks: []DashboardTask{
			{ID: "1", Name: "one", Status: StepPending},
			{ID: "2", Name: "two", Status: StepPending},
		}},
		{Number: 2, Status: StepPending, Tasks: []DashboardTask{
			{ID: "3", Name: "three", Status: StepPending},
		}},
	}
	for i, s := range p.Steps {
		if s.Status != StepPending {
			t.Errorf("step %q status = %q, want pending", s.Name, s.Status)
		}
		if s.Detail == nil || s.Detail.Kind != dashboardKindWaves || !reflect.DeepEqual(s.Detail.Waves, want[i:i+1]) {
			t.Errorf("step %q detail = %+v, want wave %+v", s.Name, s.Detail, want[i])
		}
	}
	if all := p.join.execDetail; all == nil || !reflect.DeepEqual(all.Waves, want) || all.Queued != nil {
		t.Errorf("join.execDetail = %+v, want both planned waves and no queued", all)
	}
	if p.Progress.Done != 0 || p.Progress.Total != 0 || p.Status != PipelineRunning {
		t.Errorf("progress = %+v status = %q, want planned waves to leave task counts alone", p.Progress, p.Status)
	}
	dashNoNull(t, p.Steps)
	dashNoNull(t, p.join.execDetail)
}

// TestDashboardExecuteDetail_StartedWaveKeepsStateStatus checks that a wave
// in waves[] keeps its state status and tasks, the planned waves after it stay
// pending in ascending order, and a task in no planned wave is queued.
func TestDashboardExecuteDetail_StartedWaveKeepsStateStatus(t *testing.T) {
	root := dashRoot(t)
	p := dashExecOne(t, root, map[string]any{
		"plannedTasks": []any{
			map[string]any{"id": "1", "name": "one"},
			map[string]any{"id": "2", "name": "two"},
			map[string]any{"id": "3", "name": "three"},
			map[string]any{"id": "4", "name": "four"},
			map[string]any{"id": "5", "name": "five"},
		},
		"waves": []any{map[string]any{
			"number": 1, "status": "in_progress",
			"planned": []any{map[string]any{"id": "1", "name": "one"}, map[string]any{"id": "2", "name": "two"}},
			"tasks":   []any{map[string]any{"id": "1", "status": "completed"}},
		}},
		// Out of order on purpose: the page needs ascending wave numbers.
		"plannedWaves": dashPlannedWaves(
			[2]any{3, []any{"4"}},
			[2]any{1, []any{"1", "2"}},
			[2]any{2, []any{"3"}},
		),
	})

	if want := []string{"wave 1", "wave 2", "wave 3", dashboardQueuedStep}; !reflect.DeepEqual(dashStepNames(p), want) {
		t.Fatalf("steps = %v, want %v", dashStepNames(p), want)
	}
	if got := dashStepStatuses(p); !reflect.DeepEqual(got, []string{StepInProgress, StepPending, StepPending, StepPending}) {
		t.Errorf("step statuses = %v, want in_progress then pending", got)
	}
	started := dashStep(t, p, "wave 1").Detail.Waves[0]
	wantStarted := DashboardWave{Number: 1, Status: "in_progress", Tasks: []DashboardTask{
		{ID: "1", Name: "one", Status: "completed"},
		{ID: "2", Name: "two", Status: StepPending},
	}}
	if !reflect.DeepEqual(started, wantStarted) {
		t.Errorf("wave 1 = %+v, want %+v", started, wantStarted)
	}
	if got := dashStep(t, p, "wave 2").Detail.Waves[0].Tasks; !reflect.DeepEqual(got, []DashboardTask{{ID: "3", Name: "three", Status: StepPending}}) {
		t.Errorf("wave 2 tasks = %+v, want task 3", got)
	}
	if got := dashStep(t, p, "wave 3").Detail.Waves[0].Tasks; !reflect.DeepEqual(got, []DashboardTask{{ID: "4", Name: "four", Status: StepPending}}) {
		t.Errorf("wave 3 tasks = %+v, want task 4", got)
	}
	if got := dashStep(t, p, dashboardQueuedStep).Detail.Queued; !reflect.DeepEqual(got, []DashboardTask{{ID: "5", Name: "five", Status: StepPending}}) {
		t.Errorf("queued = %+v, want only task 5, which is in no planned wave", got)
	}
	all := p.join.execDetail
	if all == nil || len(all.Waves) != 3 || all.Waves[0].Number != 1 || all.Waves[1].Number != 2 || all.Waves[2].Number != 3 {
		t.Errorf("join.execDetail waves = %+v, want waves 1, 2, 3", all)
	}
}

// TestDashboardExecuteDetail_PlannedWaveHiddenOnceStarted checks that a
// planned wave whose number is in waves[] never shows twice, whatever the
// status of that wave entry.
func TestDashboardExecuteDetail_PlannedWaveHiddenOnceStarted(t *testing.T) {
	for _, status := range []string{"in_progress", "completed", "partial", "failed", ""} {
		t.Run("status "+status, func(t *testing.T) {
			root := dashRoot(t)
			p := dashExecOne(t, root, map[string]any{
				"waves":        []any{map[string]any{"number": 1, "status": status}},
				"plannedWaves": dashPlannedWaves([2]any{1, []any{"1"}}),
			})
			if want := []string{"wave 1"}; !reflect.DeepEqual(dashStepNames(p), want) {
				t.Errorf("steps = %v, want %v", dashStepNames(p), want)
			}
			if got := p.join.execDetail.Waves; len(got) != 1 || len(got[0].Tasks) != 0 {
				t.Errorf("join waves = %+v, want only the state wave", got)
			}
		})
	}
}

// TestDashboardExecuteDetail_PlannedWaveZero checks that the pre-wave tasks
// of plannedWaves show as a pending "wave 0" step.
func TestDashboardExecuteDetail_PlannedWaveZero(t *testing.T) {
	root := dashRoot(t)
	p := dashExecOne(t, root, map[string]any{
		"plannedTasks": []any{map[string]any{"id": "1", "name": "one"}, map[string]any{"id": "2", "name": "two"}},
		"plannedWaves": dashPlannedWaves([2]any{0, []any{"1"}}, [2]any{1, []any{"2"}}),
	})
	if want := []string{"wave 0", "wave 1"}; !reflect.DeepEqual(dashStepNames(p), want) {
		t.Fatalf("steps = %v, want %v", dashStepNames(p), want)
	}
	if w := dashStep(t, p, "wave 0").Detail.Waves[0]; w.Number != 0 || w.Status != StepPending || len(w.Tasks) != 1 || w.Tasks[0].Name != "one" {
		t.Errorf("wave 0 = %+v, want one pending task named one", w)
	}
}

// TestDashboardExecuteDetail_PlannedWaveTaskShownOnce checks that a task that
// a started wave or an earlier planned wave already shows is left out of a
// later planned wave, that T-prefixed ids match plannedTasks names, and that a
// planned wave left with no task still shows as a step with "tasks":[].
func TestDashboardExecuteDetail_PlannedWaveTaskShownOnce(t *testing.T) {
	root := dashRoot(t)
	p := dashExecOne(t, root, map[string]any{
		"plannedTasks": []any{
			map[string]any{"id": "1", "name": "one"},
			map[string]any{"id": "2", "name": "two"},
			map[string]any{"id": "3", "name": "three"},
		},
		"waves": []any{map[string]any{"number": 1, "status": "completed", "tasks": []any{
			map[string]any{"id": "1", "status": "completed"},
		}}},
		"plannedWaves": dashPlannedWaves(
			[2]any{2, []any{"T1", "T2"}},
			[2]any{3, []any{"2", "T3"}},
			[2]any{4, []any{"1"}},
		),
	})
	if want := []string{"wave 1", "wave 2", "wave 3", "wave 4"}; !reflect.DeepEqual(dashStepNames(p), want) {
		t.Fatalf("steps = %v, want %v", dashStepNames(p), want)
	}
	if got := dashStep(t, p, "wave 2").Detail.Waves[0].Tasks; !reflect.DeepEqual(got, []DashboardTask{{ID: "T2", Name: "two", Status: StepPending}}) {
		t.Errorf("wave 2 tasks = %+v, want only T2: task 1 is in wave 1", got)
	}
	if got := dashStep(t, p, "wave 3").Detail.Waves[0].Tasks; !reflect.DeepEqual(got, []DashboardTask{{ID: "T3", Name: "three", Status: StepPending}}) {
		t.Errorf("wave 3 tasks = %+v, want only T3: task 2 is in wave 2", got)
	}
	empty := dashStep(t, p, "wave 4")
	if !strings.Contains(string(dashMarshal(t, empty.Detail)), `"tasks":[]`) {
		t.Errorf("wave 4 detail = %s, want \"tasks\":[]", dashMarshal(t, empty.Detail))
	}
	for _, s := range p.Steps {
		if s.Name == dashboardQueuedStep {
			t.Errorf("queued step present, want none: every planned task is in a wave")
		}
	}
	dashNoNull(t, p.Steps)
}

// TestDashboardExecuteDetail_WithoutPlannedWaves checks that a run with no
// plannedWaves key, or an empty one, keeps today's steps: one for each wave
// and a queued step for the unstarted planned tasks.
func TestDashboardExecuteDetail_WithoutPlannedWaves(t *testing.T) {
	cases := []struct {
		name string
		key  any
	}{
		{"key absent", nil},
		{"empty list", []any{}},
		{"not a list", "1,2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := dashRoot(t)
			data := map[string]any{
				"plannedTasks": []any{map[string]any{"id": "1", "name": "one"}, map[string]any{"id": "2", "name": "two"}},
				"waves":        []any{map[string]any{"number": 1, "status": "in_progress", "planned": []any{map[string]any{"id": "1"}}}},
			}
			if c.key != nil {
				data["plannedWaves"] = c.key
			}
			p := dashExecOne(t, root, data)
			if want := []string{"wave 1", dashboardQueuedStep}; !reflect.DeepEqual(dashStepNames(p), want) {
				t.Errorf("steps = %v, want %v", dashStepNames(p), want)
			}
			if got := dashStep(t, p, dashboardQueuedStep).Detail.Queued; !reflect.DeepEqual(got, []DashboardTask{{ID: "2", Name: "two", Status: StepPending}}) {
				t.Errorf("queued = %+v, want task 2", got)
			}
		})
	}
}

// TestDashboardExecuteDetail_PendingPlannedWavesSkipsBadEntries checks the
// entries that dashboardPendingPlannedWaves leaves out or trims. Number values
// are float64, as a state file decoded from JSON holds them.
func TestDashboardExecuteDetail_PendingPlannedWavesSkipsBadEntries(t *testing.T) {
	data := map[string]any{
		"waves": []any{"not an object", map[string]any{"number": 1.0}},
		"plannedWaves": []any{
			"not an object",
			map[string]any{"taskIds": []any{"1"}},                    // no number
			map[string]any{"number": "2", "taskIds": []any{"1"}},     // number is a string
			map[string]any{"number": -1.0, "taskIds": []any{"1"}},    // negative number
			map[string]any{"number": 1.0, "taskIds": []any{"1"}},     // started
			map[string]any{"number": 3.0},                            // no taskIds
			map[string]any{"number": 4.0, "taskIds": []any{"", 7.0}}, // no usable task ID
			map[string]any{"number": 5.0, "taskIds": []any{"a", 7.0, "", "b"}},
			map[string]any{"number": 5.0, "taskIds": []any{"x"}}, // repeats number 5
		},
	}
	want := []dashboardPlannedWave{{number: 5, taskIDs: []string{"a", "b"}}}
	if got := dashboardPendingPlannedWaves(data); !reflect.DeepEqual(got, want) {
		t.Errorf("dashboardPendingPlannedWaves = %+v, want %+v", got, want)
	}
	if got := dashboardPendingPlannedWaves(map[string]any{}); got != nil {
		t.Errorf("dashboardPendingPlannedWaves(empty) = %+v, want nil", got)
	}
}

// TestDashboardExecuteDetail_PlannedWaveNumbersMatchWaves checks that
// dashboardPlannedWaveNumbers and dashboardPlannedWaves give the same waves
// in the same order, because dashboardExecuteSteps pairs them by position.
func TestDashboardExecuteDetail_PlannedWaveNumbersMatchWaves(t *testing.T) {
	data := map[string]any{
		"waves":        []any{map[string]any{"number": 2.0}},
		"plannedWaves": dashPlannedWaves([2]any{4.0, []any{"4"}}, [2]any{2.0, []any{"2"}}, [2]any{3.0, []any{"3"}}, [2]any{1.0, []any{"1"}}),
	}
	nums := dashboardPlannedWaveNumbers(data)
	if want := []int{1, 3, 4}; !reflect.DeepEqual(nums, want) {
		t.Fatalf("dashboardPlannedWaveNumbers = %v, want %v", nums, want)
	}
	waves := dashboardPlannedWaves(data, map[string]string{}, map[string]bool{})
	if len(waves) != len(nums) {
		t.Fatalf("dashboardPlannedWaves gave %d waves, want %d", len(waves), len(nums))
	}
	for i, w := range waves {
		if w.Number != nums[i] {
			t.Errorf("wave %d number = %d, want %d", i, w.Number, nums[i])
		}
	}
	if got := dashboardPlannedWaveNumbers(map[string]any{}); got != nil {
		t.Errorf("dashboardPlannedWaveNumbers(empty) = %v, want nil", got)
	}
}

// TestDashboardExecuteDetail_PlannedWavesNestInShip checks that a ship run
// that nests an execute run shows the planned waves in its one execute step.
func TestDashboardExecuteDetail_PlannedWavesNestInShip(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)
	exec := map[string]any{
		"branch":       "feat/x",
		"startedAt":    "2026-10-07T08:01:00Z",
		"plannedTasks": []any{map[string]any{"id": "1", "name": "one"}, map[string]any{"id": "2", "name": "two"}},
		"waves":        []any{},
		"plannedWaves": dashPlannedWaves([2]any{1, []any{"1"}}, [2]any{2, []any{"2"}}),
	}
	dashWriteState(t, root, dashJoinExecFile("20261007T080100Z"), exec, dashJoinFresh)

	ship := dashOne(t, root)
	if ship.Kind != "ship" {
		t.Fatalf("kind = %q, want ship", ship.Kind)
	}
	d := dashJoinStep(t, ship, "execute").Detail
	if d == nil || d.Kind != dashboardKindWaves || len(d.Waves) != 2 {
		t.Fatalf("execute detail = %+v, want kind waves with two planned waves", d)
	}
	for i, w := range d.Waves {
		if w.Number != i+1 || w.Status != StepPending || len(w.Tasks) != 1 {
			t.Errorf("wave %d = %+v, want a pending wave with one task", i, w)
		}
	}
	if d.Waves[1].Tasks[0].Name != "two" || d.Queued != nil {
		t.Errorf("execute detail = %+v, want task 2 named two and no queued", d)
	}
}

// TestDashboardExecuteSteps_ShortStepList checks that dashboardExecuteSteps
// does not index past p.Steps when the caller gives fewer steps than the
// state has started and planned waves. The waves reach join.execDetail, no
// wave step gets a detail, and the queued step is still added.
func TestDashboardExecuteSteps_ShortStepList(t *testing.T) {
	st := &state.State{Data: map[string]any{
		"plannedTasks": []any{
			map[string]any{"id": "1", "name": "one"},
			map[string]any{"id": "2", "name": "two"},
			map[string]any{"id": "3", "name": "three"},
		},
		// Numbers are float64 because the state file is JSON.
		"waves": []any{map[string]any{
			"number": float64(1), "status": "completed",
			"tasks": []any{map[string]any{"id": "1", "status": "completed"}},
		}},
		"plannedWaves": dashPlannedWaves([2]any{float64(1), []any{"1"}}, [2]any{float64(2), []any{"2"}}),
	}}
	p := DashboardPipeline{}

	dashboardExecuteSteps(&p, st)

	if want := []string{dashboardQueuedStep}; !reflect.DeepEqual(dashStepNames(p), want) {
		t.Errorf("steps = %v, want %v", dashStepNames(p), want)
	}
	all := p.join.execDetail
	if all == nil || len(all.Waves) != 2 || len(all.Queued) != 1 || all.Queued[0].ID != "3" {
		t.Fatalf("join.execDetail = %+v, want two waves and task 3 queued", all)
	}
	if all.Waves[0].Number != 1 || all.Waves[1].Number != 2 || all.Waves[1].Status != StepPending {
		t.Errorf("waves = %+v, want started wave 1 then pending wave 2", all.Waves)
	}
}
