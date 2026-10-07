package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// dashPlanRunID is the run ID of the plan state file every test below
// writes; the evidence store of the run is keyed by it.
const dashPlanRunID = "plan-main-20261007T090000Z"

// dashPlanNames are the station names in order.
var dashPlanNames = []string{"setup", "explore", "draft", "review", "finalize"}

// dashPlanState writes a plan state file with checkpoint step and the extra
// keys of extra, and returns its only pipeline.
func dashPlanState(t *testing.T, root, step string, extra map[string]any) DashboardPipeline {
	t.Helper()
	data := map[string]any{
		"planIntegrity": map[string]any{"skillInvoked": "2026-10-07T09:00:00Z"},
		"checkpoint":    map[string]any{"step": step},
	}
	for k, v := range extra {
		data[k] = v
	}
	dashWriteState(t, root, dashPlanRunID+".json", data, dashNow.Add(-time.Minute))
	return dashOne(t, root)
}

// dashPlanStep returns the step named name of p.
func dashPlanStep(t *testing.T, p DashboardPipeline, name string) DashboardStep {
	t.Helper()
	for _, s := range p.Steps {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no step %q in %+v", name, p.Steps)
	return DashboardStep{}
}

func TestDashboardPlan_StationsCoverEachCheckpointStepOnce(t *testing.T) {
	seen := map[string]int{}
	var flat []string
	for _, s := range dashboardPlanStations {
		for _, id := range s.steps {
			seen[id]++
			flat = append(flat, id)
		}
	}
	if !reflect.DeepEqual(flat, validCheckpointSteps) {
		t.Errorf("station steps in order = %v, want validCheckpointSteps %v", flat, validCheckpointSteps)
	}
	for _, id := range validCheckpointSteps {
		if seen[id] != 1 {
			t.Errorf("checkpoint step %q is in %d stations, want 1", id, seen[id])
		}
	}
	var names []string
	for _, s := range dashboardPlanStations {
		names = append(names, s.name)
	}
	if !reflect.DeepEqual(names, dashPlanNames) {
		t.Errorf("station names = %v, want %v", names, dashPlanNames)
	}
}

func TestDashboardPlan_StationStatusAndProgress(t *testing.T) {
	C, I, P := StepCompleted, StepInProgress, StepPending
	cases := []struct {
		name     string
		step     string
		done     bool
		statuses []string
		progress DashboardProgress
	}{
		{"checkpoint 0", "0", false, []string{I, P, P, P, P},
			DashboardProgress{Done: 0, Total: 5, Current: "setup", Label: "setup (1 of 5)"}},
		{"checkpoint 1", "1", false, []string{C, I, P, P, P},
			DashboardProgress{Done: 1, Total: 5, Current: "explore", Label: "explore (2 of 5)"}},
		{"checkpoint 2", "2", false, []string{C, C, I, P, P},
			DashboardProgress{Done: 2, Total: 5, Current: "draft", Label: "draft (3 of 5)"}},
		{"checkpoint 4", "4", false, []string{C, C, C, I, P},
			DashboardProgress{Done: 3, Total: 5, Current: "review", Label: "round 1 of 5"}},
		{"checkpoint 6.5", "6.5", false, []string{C, C, C, C, I},
			DashboardProgress{Done: 4, Total: 5, Current: "finalize", Label: "finalize (5 of 5)"}},
		{"checkpoint 7", "7", false, []string{C, C, C, C, I},
			DashboardProgress{Done: 4, Total: 5, Current: "finalize", Label: "finalize (5 of 5)"}},
		{"done marker set", "7", true, []string{C, C, C, C, C},
			DashboardProgress{Done: 5, Total: 5, Current: "", Label: "5 of 5 steps"}},
		{"unknown checkpoint step", "9", false, []string{P, P, P, P, P},
			DashboardProgress{Done: 0, Total: 5, Current: "", Label: "0 of 5 steps"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := dashRoot(t)
			extra := map[string]any{}
			if tc.done {
				extra["planIntegrity"] = map[string]any{"skillInvoked": "2026-10-07T09:00:00Z", "done": "2026-10-07T09:40:00Z"}
			}
			p := dashPlanState(t, root, tc.step, extra)
			var names []string
			for _, s := range p.Steps {
				names = append(names, s.Name)
			}
			if !reflect.DeepEqual(names, dashPlanNames) {
				t.Errorf("step names = %v, want %v", names, dashPlanNames)
			}
			if got := dashStepStatuses(p); !reflect.DeepEqual(got, tc.statuses) {
				t.Errorf("statuses = %v, want %v", got, tc.statuses)
			}
			if p.Progress != tc.progress {
				t.Errorf("progress = %+v, want %+v", p.Progress, tc.progress)
			}
			wantStatus := PipelineRunning
			if tc.done {
				wantStatus = PipelineCompleted
			}
			if p.Status != wantStatus {
				t.Errorf("pipeline status = %q, want %q", p.Status, wantStatus)
			}
		})
	}
}

func TestDashboardPlan_ReviewLabelCountsRounds(t *testing.T) {
	round := func(n int) map[string]any {
		return map[string]any{"round": n, "mergedStatus": planStatusIssuesFound, "found": 1, "fixed": 1, "lenses": []any{}}
	}
	cases := []struct {
		name   string
		rounds []any
		want   string
	}{
		{"no rounds", nil, "round 1 of 5"},
		{"one round", []any{round(1)}, "round 2 of 5"},
		{"max rounds clamps", []any{round(1), round(2), round(3), round(4), round(5)}, "round 5 of 5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := dashRoot(t)
			extra := map[string]any{}
			if tc.rounds != nil {
				extra["reviewRounds"] = tc.rounds
			}
			p := dashPlanState(t, root, "5", extra)
			if p.Progress.Label != tc.want {
				t.Errorf("label = %q, want %q", p.Progress.Label, tc.want)
			}
			if p.Progress.Current != "review" {
				t.Errorf("current = %q, want review", p.Progress.Current)
			}
		})
	}
}

func TestDashboardPlan_ReviewDetailListsRounds(t *testing.T) {
	root := dashRoot(t)
	p := dashPlanState(t, root, "6", map[string]any{
		"reviewRounds": []any{
			map[string]any{"round": 1, "mergedStatus": planStatusIssuesFound, "found": 4, "fixed": 3,
				"lenses": []any{map[string]any{"name": "risk", "verdict": planStatusIssuesFound}}},
			map[string]any{"round": 2, "mergedStatus": planStatusApproved, "found": 0, "fixed": 0, "lenses": []any{}},
		},
	})
	d := dashPlanStep(t, p, "review").Detail
	if d == nil {
		t.Fatal("review detail = nil, want rounds")
	}
	if d.Kind != dashboardKindRounds {
		t.Errorf("kind = %q, want %q", d.Kind, dashboardKindRounds)
	}
	if d.MaxRounds != maxReviewRounds {
		t.Errorf("maxRounds = %d, want %d", d.MaxRounds, maxReviewRounds)
	}
	want := []DashboardRound{
		{N: 1, Status: planStatusIssuesFound, Found: 4, Fixed: 3, Lenses: []DashboardLens{{Name: "risk", Verdict: planStatusIssuesFound}}},
		{N: 2, Status: planStatusApproved, Found: 0, Fixed: 0, Lenses: []DashboardLens{}},
	}
	if !reflect.DeepEqual(d.Rounds, want) {
		t.Errorf("rounds = %+v, want %+v", d.Rounds, want)
	}
	for _, other := range []string{"setup", "draft", "finalize"} {
		if dashPlanStep(t, p, other).Detail != nil {
			t.Errorf("%s detail is set, want nil", other)
		}
	}
}

func TestDashboardPlan_MalformedReviewRounds_NoDetail(t *testing.T) {
	root := dashRoot(t)
	p := dashPlanState(t, root, "4", map[string]any{"reviewRounds": "not a list"})
	if d := dashPlanStep(t, p, "review").Detail; d != nil {
		t.Errorf("review detail = %+v, want nil", d)
	}
	if p.Progress.Label != "round 1 of 5" {
		t.Errorf("label = %q, want %q", p.Progress.Label, "round 1 of 5")
	}
}

func TestDashboardPlan_ExploreDetailListsExplorers(t *testing.T) {
	root := dashRoot(t)
	evidenceWriteRaw(t, root, dashPlanRunID, "explore-auth-flow", evidenceWriterFile{
		WriterID: "explore-auth-flow", Status: evidenceStatusDone, Items: exploreSummaryItems(7),
	})
	evidenceWriteRaw(t, root, dashPlanRunID, "explore-empty", evidenceWriterFile{
		WriterID: "explore-empty", Status: evidenceStatusRunning,
	})
	evidenceWriteRaw(t, root, dashPlanRunID, "lens-risk", evidenceWriterFile{
		WriterID: "lens-risk", Status: evidenceStatusDone, Items: exploreSummaryItems(1),
	})
	p := dashPlanState(t, root, "2", nil)

	d := dashPlanStep(t, p, "explore").Detail
	if d == nil {
		t.Fatal("explore detail = nil, want explorers")
	}
	if d.Kind != dashboardKindExplorers {
		t.Errorf("kind = %q, want %q", d.Kind, dashboardKindExplorers)
	}
	if len(d.Explorers) != 2 {
		t.Fatalf("explorers = %+v, want 2 (lens writer excluded)", d.Explorers)
	}
	auth := d.Explorers[0]
	if auth.Name != "auth-flow" || auth.Status != evidenceStatusDone || auth.Total != 7 || len(auth.Findings) != exploreSummaryTop {
		t.Errorf("explorer[0] = %+v, want auth-flow done total 7 with %d findings", auth, exploreSummaryTop)
	}
	if auth.Findings[0] != (DashboardFinding{Summary: "finding 1", Ref: "pkg/file.go:1"}) {
		t.Errorf("findings[0] = %+v, want finding 1 at pkg/file.go:1", auth.Findings[0])
	}
	empty := d.Explorers[1]
	if empty.Name != "empty" || empty.Status != evidenceStatusRunning || empty.Total != 0 || empty.Findings == nil || len(empty.Findings) != 0 {
		t.Errorf("explorer[1] = %+v, want empty running with [] findings", empty)
	}
	if dashPlanStep(t, p, "review").Detail != nil {
		t.Error("review detail is set with no reviewRounds, want nil")
	}
}

func TestDashboardPlan_ExploreReadError_NoDetailSnapshotOK(t *testing.T) {
	root := dashRoot(t)
	// A regular file at the evidence path makes the evidence read fail with
	// an error that is not "not exist".
	dir := state.EvidenceDir(root, dashPlanRunID)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := planExploreSummary(root, dashPlanRunID); err == nil {
		t.Fatal("planExploreSummary err = nil, want a read error for this fixture")
	}
	p := dashPlanState(t, root, "3", nil)
	if d := dashPlanStep(t, p, "explore").Detail; d != nil {
		t.Errorf("explore detail = %+v, want nil", d)
	}
	if len(p.Steps) != len(dashPlanNames) {
		t.Errorf("steps = %d, want %d", len(p.Steps), len(dashPlanNames))
	}
}

func TestDashboardPlan_NoEvidenceNoRounds_NoDetail(t *testing.T) {
	root := dashRoot(t)
	p := dashPlanState(t, root, "6.6", nil)
	if len(p.Steps) != len(dashPlanNames) {
		t.Fatalf("steps = %d, want %d", len(p.Steps), len(dashPlanNames))
	}
	for _, s := range p.Steps {
		if s.Detail != nil {
			t.Errorf("%s detail = %+v, want nil", s.Name, s.Detail)
		}
	}
}

func TestDashboardPlan_SessionID(t *testing.T) {
	t.Run("from plan state", func(t *testing.T) {
		root := dashRoot(t)
		p := dashPlanState(t, root, "0", map[string]any{"sessionId": "sess-plan"})
		if p.SessionID != "sess-plan" {
			t.Errorf("sessionId = %q, want sess-plan", p.SessionID)
		}
	})
	t.Run("absent is empty", func(t *testing.T) {
		root := dashRoot(t)
		p := dashPlanState(t, root, "0", nil)
		if p.SessionID != "" {
			t.Errorf("sessionId = %q, want empty", p.SessionID)
		}
	})
}

func TestDashboardPlan_EmptyListsMarshalAsArrays(t *testing.T) {
	explorers := dashboardExplorers([]ExploreSummaryEntry{{Name: "a", Status: evidenceStatusRunning}})
	rounds := dashboardPlanRounds([]any{map[string]any{"round": 1, "mergedStatus": planStatusApproved}})
	if len(rounds) != 1 {
		t.Fatalf("rounds = %+v, want 1", rounds)
	}
	d := DashboardStepDetail{Kind: dashboardKindRounds, Explorers: explorers, Rounds: rounds}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{`"findings":[]`, `"lenses":[]`} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON = %s, want it to contain %s", got, want)
		}
	}
	if strings.Contains(got, "null") {
		t.Errorf("JSON = %s, want no null", got)
	}
	if b, _ := json.Marshal(dashboardExplorers(nil)); string(b) != "[]" {
		t.Errorf("dashboardExplorers(nil) JSON = %s, want []", b)
	}
}
