package tools

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// slimTestIssues returns n issues of a run, each with its own text.
func slimTestIssues(run string, n int) []DashboardIssue {
	out := make([]DashboardIssue, 0, n)
	for i := range n {
		out = append(out, DashboardIssue{
			Source: "review", Severity: "high", Text: fmt.Sprintf("%s issue %d", run, i),
			File: "internal/tools/x.go", Line: fmt.Sprint(10 + i), Ref: "code-quality",
		})
	}
	return out
}

// slimTestPipeline returns a run with the given status and issues: one
// execute step with a detail of kind kind, and one commit step without
// detail.
func slimTestPipeline(id, status, kind string, issues []DashboardIssue) DashboardPipeline {
	return DashboardPipeline{
		ID: id, Kind: "ship", Branch: "feat/" + id, Status: status,
		Steps: []DashboardStep{
			{Name: "execute", Status: StepCompleted, Detail: &DashboardStepDetail{
				Kind:  kind,
				Waves: []DashboardWave{{Number: 1, Status: "completed", Tasks: []DashboardTask{{ID: "1", Name: id + " task", Status: "completed"}}}},
			}},
			{Name: "commit", Status: StepCompleted},
		},
		Issues: issues,
	}
}

// slimTestSession returns a session with one timeline event and one command
// group, each with text of the session id.
func slimTestSession(id string) DashboardSession {
	s := DashboardSession{ID: id, Active: true, Branch: "feat/" + id, FirstSeen: "2026-10-07T09:00:00Z", LastSeen: "2026-10-07T09:30:00Z"}
	s.Counts.Prompts, s.Counts.Commands, s.Counts.MCPCalls = 3, 4, 5
	s.Timeline = []DashboardEvent{{At: "2026-10-07T09:30:00Z", Kind: "command", Text: id + " go test ./..."}}
	s.CommandGroups = []DashboardCommandGroup{{Label: "go", Programs: []string{"go"}, Count: 4, Share: 1, Majority: true, LastAt: "2026-10-07T09:30:00Z"}}
	return s
}

// slimTestSnapshot builds the same snapshot on each call: one repo with a
// completed, failed, running and stalled run, a completed run with nil
// issues and one with an empty list, a session with lists and one without.
func slimTestSnapshot() DashboardSnapshot {
	return DashboardSnapshot{
		GeneratedAt: "2026-10-07T10:00:00Z", Version: "test",
		Repos: []DashboardRepo{{
			Root: "/repo/a", Name: "a",
			Pipelines: []DashboardPipeline{
				slimTestPipeline("completed", PipelineCompleted, dashboardKindWaves, slimTestIssues("completed", 12)),
				slimTestPipeline("failed", PipelineFailed, dashboardKindDimensions, slimTestIssues("failed", 3)),
				slimTestPipeline("running", PipelineRunning, dashboardKindRounds, slimTestIssues("running", 2)),
				slimTestPipeline("stalled", PipelineStalled, dashboardKindExplorers, slimTestIssues("stalled", 5)),
				slimTestPipeline("nil-issues", PipelineCompleted, dashboardKindFixes, nil),
				slimTestPipeline("empty-issues", PipelineFailed, dashboardKindHarden, []DashboardIssue{}),
			},
			Sessions: []DashboardSession{slimTestSession("with-lists"), {ID: "no-lists", Branch: "main"}},
		}},
	}
}

// TestDashboardSlim_Pipelines checks each run status: completed and failed
// runs lose step detail and issues and get detailDeferred; running and
// stalled runs keep both. Every run gets issueCount and the detail kind of
// the step with detail; the step without detail gets no kind.
func TestDashboardSlim_Pipelines(t *testing.T) {
	in := slimTestSnapshot()
	out := SlimDashboardSnapshot(in)
	if len(out.Repos) != 1 || len(out.Repos[0].Pipelines) != len(in.Repos[0].Pipelines) {
		t.Fatalf("repos = %+v, want one repo with %d pipelines", out.Repos, len(in.Repos[0].Pipelines))
	}
	if out.GeneratedAt != in.GeneratedAt || out.Version != in.Version {
		t.Errorf("generatedAt, version = %q, %q; want %q, %q", out.GeneratedAt, out.Version, in.GeneratedAt, in.Version)
	}
	for i, got := range out.Repos[0].Pipelines {
		src := in.Repos[0].Pipelines[i]
		t.Run(src.ID, func(t *testing.T) {
			finished := src.Status == PipelineCompleted || src.Status == PipelineFailed
			if got.ID != src.ID || got.Status != src.Status || got.Branch != src.Branch {
				t.Errorf("id, status, branch = %q, %q, %q; want %q, %q, %q", got.ID, got.Status, got.Branch, src.ID, src.Status, src.Branch)
			}
			if got.IssueCount != len(src.Issues) {
				t.Errorf("issueCount = %d, want %d", got.IssueCount, len(src.Issues))
			}
			if got.DetailDeferred != finished {
				t.Errorf("detailDeferred = %v, want %v", got.DetailDeferred, finished)
			}
			if len(got.Steps) != 2 {
				t.Fatalf("steps = %+v, want 2", got.Steps)
			}
			exec, commit := got.Steps[0], got.Steps[1]
			if exec.DetailKind != src.Steps[0].Detail.Kind {
				t.Errorf("execute detailKind = %q, want %q", exec.DetailKind, src.Steps[0].Detail.Kind)
			}
			if commit.DetailKind != "" || commit.Detail != nil {
				t.Errorf("commit detail, detailKind = %+v, %q; want nil, empty", commit.Detail, commit.DetailKind)
			}
			if finished {
				if exec.Detail != nil {
					t.Errorf("execute detail = %+v, want nil on a finished run", exec.Detail)
				}
				if got.Issues == nil || len(got.Issues) != 0 {
					t.Errorf("issues = %#v, want an empty non-nil slice", got.Issues)
				}
				return
			}
			if !reflect.DeepEqual(exec.Detail, src.Steps[0].Detail) {
				t.Errorf("execute detail = %+v, want %+v", exec.Detail, src.Steps[0].Detail)
			}
			if !reflect.DeepEqual(got.Issues, src.Issues) {
				t.Errorf("issues = %+v, want %+v", got.Issues, src.Issues)
			}
		})
	}
}

// TestDashboardSlim_PipelineJSON checks the JSON text of a slim run: a
// finished run with nil issues encodes "issues":[] (never null), the issue
// count, detailDeferred and the detail kind, and no detail; a running run
// keeps its issues and detail and has no detailDeferred key.
func TestDashboardSlim_PipelineJSON(t *testing.T) {
	out := SlimDashboardSnapshot(slimTestSnapshot())
	byID := map[string]string{}
	for _, p := range out.Repos[0].Pipelines {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		byID[p.ID] = string(b)
	}

	for _, id := range []string{"completed", "failed", "nil-issues", "empty-issues"} {
		got := byID[id]
		for _, want := range []string{`"issues":[]`, `"detailDeferred":true`, `"detailKind":"`} {
			if !strings.Contains(got, want) {
				t.Errorf("%s JSON = %s, want it to contain %s", id, got, want)
			}
		}
		if strings.Contains(got, `"detail":`) || strings.Contains(got, `"issues":null`) {
			t.Errorf("%s JSON = %s, want no detail and no null issues", id, got)
		}
	}
	if got := byID["completed"]; !strings.Contains(got, `"issueCount":12`) || !strings.Contains(got, `"detailKind":"waves"`) {
		t.Errorf("completed JSON = %s, want issueCount 12 and detailKind waves", got)
	}
	if got := byID["nil-issues"]; strings.Contains(got, `"issueCount"`) {
		t.Errorf("nil-issues JSON = %s, want no issueCount key", got)
	}
	running := byID["running"]
	for _, want := range []string{`"issueCount":2`, `"detailKind":"rounds"`, `"detail":{"kind":"rounds"`, `"text":"running issue 1"`} {
		if !strings.Contains(running, want) {
			t.Errorf("running JSON = %s, want it to contain %s", running, want)
		}
	}
	if strings.Contains(running, `"detailDeferred"`) {
		t.Errorf("running JSON = %s, want no detailDeferred key", running)
	}
}

// TestDashboardSlim_Sessions checks that each slim session has no timeline
// and no commandGroups key, and keeps its other fields.
func TestDashboardSlim_Sessions(t *testing.T) {
	in := slimTestSnapshot()
	out := SlimDashboardSnapshot(in)
	for i, got := range out.Repos[0].Sessions {
		src := in.Repos[0].Sessions[i]
		if got.Timeline != nil || got.CommandGroups != nil {
			t.Errorf("%s timeline, commandGroups = %#v, %#v; want nil", src.ID, got.Timeline, got.CommandGroups)
		}
		if got.ID != src.ID || got.Branch != src.Branch || got.Active != src.Active || got.Counts != src.Counts ||
			got.FirstSeen != src.FirstSeen || got.LastSeen != src.LastSeen {
			t.Errorf("session = %+v, want the fields of %+v", got, src)
		}
		b, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if s := string(b); strings.Contains(s, `"timeline"`) || strings.Contains(s, `"commandGroups"`) {
			t.Errorf("%s JSON = %s, want no timeline and no commandGroups key", src.ID, s)
		}
	}
}

// TestDashboardSlim_InputUnchanged checks that the function changes no part
// of its input: the input after the call equals a fresh build of it.
func TestDashboardSlim_InputUnchanged(t *testing.T) {
	in := slimTestSnapshot()
	_ = SlimDashboardSnapshot(in)
	if want := slimTestSnapshot(); !reflect.DeepEqual(in, want) {
		t.Errorf("input after the call = %+v, want %+v", in, want)
	}
}

// TestDashboardSlim_NilLists checks that nil lists stay nil: nil repos, and
// a repo with nil pipelines and sessions, and a run with nil steps.
func TestDashboardSlim_NilLists(t *testing.T) {
	if got := SlimDashboardSnapshot(DashboardSnapshot{Version: "v"}); got.Repos != nil || got.Version != "v" {
		t.Errorf("snapshot = %+v, want nil repos and version v", got)
	}
	in := DashboardSnapshot{Repos: []DashboardRepo{
		{Name: "empty"},
		{Name: "no-steps", Pipelines: []DashboardPipeline{{ID: "x", Status: PipelineCompleted}}},
	}}
	out := SlimDashboardSnapshot(in)
	if r := out.Repos[0]; r.Name != "empty" || r.Pipelines != nil || r.Sessions != nil {
		t.Errorf("repo = %+v, want nil pipelines and sessions", r)
	}
	p := out.Repos[1].Pipelines[0]
	if p.Steps != nil || !p.DetailDeferred || p.Issues == nil || p.IssueCount != 0 {
		t.Errorf("pipeline = %+v, want nil steps, detailDeferred, empty issues, issueCount 0", p)
	}
}

// slimTestBigPipeline returns a completed run with 5 waves of 6 tasks, 8
// review dimensions and 12 issues.
func slimTestBigPipeline(n int) DashboardPipeline {
	id := fmt.Sprintf("run-%02d", n)
	waves := make([]DashboardWave, 0, 5)
	for w := range 5 {
		tasks := make([]DashboardTask, 0, 6)
		for k := range 6 {
			tasks = append(tasks, DashboardTask{ID: fmt.Sprint(w*6 + k), Name: fmt.Sprintf("%s task %d of wave %d", id, k, w), Status: "completed"})
		}
		waves = append(waves, DashboardWave{Number: w + 1, Status: "completed", CommittedSHA: "abc1234", Tasks: tasks})
	}
	dims := make([]DashboardDimension, 0, 8)
	for d := range 8 {
		dims = append(dims, DashboardDimension{Name: fmt.Sprintf("dimension-%d", d), Status: "completed", Findings: 2, Worst: "medium", FindingItems: []DashboardReviewFinding{}})
	}
	return DashboardPipeline{
		ID: id, Kind: "ship", Branch: "feat/" + id, Status: PipelineCompleted,
		Steps: []DashboardStep{
			{Name: "execute", Status: StepCompleted, Detail: &DashboardStepDetail{Kind: dashboardKindWaves, Waves: waves}},
			{Name: "review", Status: StepCompleted, Detail: &DashboardStepDetail{Kind: dashboardKindDimensions, Dimensions: dims}},
		},
		Issues: slimTestIssues(id, 12),
	}
}

// slimTestLongSession returns a session with 50 timeline events and 10
// command groups.
func slimTestLongSession(n int) DashboardSession {
	s := DashboardSession{ID: fmt.Sprintf("session-%02d", n), Branch: "main"}
	for e := range 50 {
		s.Timeline = append(s.Timeline, DashboardEvent{At: "2026-10-07T09:30:00Z", Kind: "command", Text: fmt.Sprintf("go test ./internal/tools/ -run TestDashboard%d -count=1", e)})
	}
	for g := range 10 {
		s.CommandGroups = append(s.CommandGroups, DashboardCommandGroup{Label: fmt.Sprintf("prog%d", g), Programs: []string{fmt.Sprintf("prog%d", g)}, Count: 5, Share: 0.1, LastAt: "2026-10-07T09:30:00Z"})
	}
	return s
}

// TestDashboardSlim_Size checks that a built snapshot of 40 finished runs
// and 20 long sessions gives slim JSON below 25% of its full JSON.
func TestDashboardSlim_Size(t *testing.T) {
	repo := DashboardRepo{Root: "/repo/a", Name: "a"}
	for n := range 40 {
		repo.Pipelines = append(repo.Pipelines, slimTestBigPipeline(n))
	}
	for n := range 20 {
		repo.Sessions = append(repo.Sessions, slimTestLongSession(n))
	}
	full := DashboardSnapshot{Version: "test", Repos: []DashboardRepo{repo}}
	fullJSON, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	slimJSON, err := json.Marshal(SlimDashboardSnapshot(full))
	if err != nil {
		t.Fatal(err)
	}
	if len(slimJSON)*4 >= len(fullJSON) {
		t.Errorf("slim JSON = %d bytes, full JSON = %d bytes; want slim below 25%%", len(slimJSON), len(fullJSON))
	}
}
