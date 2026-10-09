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

// TestDashboardPlan_EmptyListsMarshalAsArrays checks that the empty lists of
// explorers and rounds marshal as [] and never as null.
func TestDashboardPlan_EmptyListsMarshalAsArrays(t *testing.T) {
	explorers := dashboardExplorers([]ExploreSummaryEntry{{Name: "a", Status: evidenceStatusRunning}})
	rounds := dashboardPlanRounds(dashboardPlanStoredRounds([]any{map[string]any{"round": 1, "mergedStatus": planStatusApproved}}))
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

// dashPlanFindings returns a findings list for a PlanReviewRound. Each
// argument is a finding ID; an ID that starts with "!" is fixed and the "!"
// is dropped. The list is non-nil, so a call with no argument gives the
// empty list that a stored "findings": [] decodes to.
func dashPlanFindings(ids ...string) *[]PlanRoundFinding {
	out := make([]PlanRoundFinding, 0, len(ids))
	for _, id := range ids {
		fixed := strings.HasPrefix(id, "!")
		out = append(out, PlanRoundFinding{ID: strings.TrimPrefix(id, "!"), Fixed: fixed})
	}
	return &out
}

// TestDashboardPlanGuardrails_ReadsCounts checks the setup station detail for
// each shape of the guardrailCounts value that a state file can hold.
func TestDashboardPlanGuardrails_ReadsCounts(t *testing.T) {
	counts := func(total, errs, warns any) map[string]any {
		return map[string]any{"total": total, "error": errs, "warning": warns}
	}
	cases := []struct {
		name string
		raw  any
		want *DashboardGuardrailCounts // nil: no detail
	}{
		{"counts from a state file", counts(float64(5), float64(3), float64(1)), &DashboardGuardrailCounts{Total: 5, Error: 3, Warning: 1}},
		{"counts as ints", counts(2, 1, 1), &DashboardGuardrailCounts{Total: 2, Error: 1, Warning: 1}},
		{"zero counts are a detail", counts(float64(0), float64(0), float64(0)), &DashboardGuardrailCounts{}},
		{"total above error plus warning", counts(float64(4), float64(1), float64(1)), &DashboardGuardrailCounts{Total: 4, Error: 1, Warning: 1}},
		{"key absent", nil, nil},
		{"string", "3 guardrails", nil},
		{"list", []any{float64(3), float64(2), float64(1)}, nil},
		{"missing warning", map[string]any{"total": float64(3), "error": float64(2)}, nil},
		{"missing total", map[string]any{"error": float64(2), "warning": float64(1)}, nil},
		{"count as string", counts("3", float64(2), float64(1)), nil},
		{"count as null", counts(float64(3), nil, float64(1)), nil},
		{"fractional count", counts(float64(3.5), float64(2), float64(1)), nil},
		{"negative count", counts(float64(3), float64(-2), float64(1)), nil},
		{"negative total", counts(float64(-3), float64(2), float64(1)), nil},
		{"negative warning", counts(float64(3), float64(2), float64(-1)), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dashboardPlanGuardrails(tc.raw)
			if tc.want == nil {
				if got != nil {
					t.Errorf("detail = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("detail = nil, want counts %+v", *tc.want)
			}
			if got.Kind != dashboardKindGuardrails {
				t.Errorf("kind = %q, want %q", got.Kind, dashboardKindGuardrails)
			}
			if got.Guardrails == nil || *got.Guardrails != *tc.want {
				t.Errorf("guardrails = %+v, want %+v", got.Guardrails, *tc.want)
			}
		})
	}
}

// TestDashboardPlan_SetupDetailShowsGuardrailCounts reads the setup station
// detail from a plan state file: it holds the stored counts, and only the
// setup station has it.
func TestDashboardPlan_SetupDetailShowsGuardrailCounts(t *testing.T) {
	t.Run("counts stored", func(t *testing.T) {
		root := dashRoot(t)
		p := dashPlanState(t, root, "1", map[string]any{
			"guardrailCounts": map[string]any{"total": 4, "error": 3, "warning": 1},
		})
		d := dashPlanStep(t, p, "setup").Detail
		if d == nil {
			t.Fatal("setup detail = nil, want guardrails")
		}
		if d.Kind != dashboardKindGuardrails {
			t.Errorf("kind = %q, want %q", d.Kind, dashboardKindGuardrails)
		}
		if want := (DashboardGuardrailCounts{Total: 4, Error: 3, Warning: 1}); d.Guardrails == nil || *d.Guardrails != want {
			t.Errorf("guardrails = %+v, want %+v", d.Guardrails, want)
		}
		for _, other := range []string{"explore", "draft", "review", "finalize"} {
			if dashPlanStep(t, p, other).Detail != nil {
				t.Errorf("%s detail is set, want nil", other)
			}
		}
	})
	t.Run("counts malformed", func(t *testing.T) {
		root := dashRoot(t)
		p := dashPlanState(t, root, "1", map[string]any{"guardrailCounts": "none"})
		if d := dashPlanStep(t, p, "setup").Detail; d != nil {
			t.Errorf("setup detail = %+v, want nil", d)
		}
	})
	t.Run("counts absent", func(t *testing.T) {
		root := dashRoot(t)
		p := dashPlanState(t, root, "1", nil)
		if d := dashPlanStep(t, p, "setup").Detail; d != nil {
			t.Errorf("setup detail = %+v, want nil", d)
		}
	})
}

// TestDashboardPlanTotals_CountsDistinctOrSums checks the round totals: the
// distinct count when every round has a findings list, and the sum of the
// per-round counts when any round has none.
func TestDashboardPlanTotals_CountsDistinctOrSums(t *testing.T) {
	cases := []struct {
		name   string
		rounds []PlanReviewRound
		want   DashboardRoundTotals
	}{
		{
			name: "every round has ids",
			rounds: []PlanReviewRound{
				{Round: 1, Found: 3, Fixed: 2, Findings: dashPlanFindings("!a", "b", "!g1")},
				{Round: 2, Found: 1, Fixed: 1, Findings: dashPlanFindings("!b")},
			},
			want: DashboardRoundTotals{Iterations: 2, Violations: 3, Fixes: 3, Distinct: true},
		},
		{
			name: "no round has ids",
			rounds: []PlanReviewRound{
				{Round: 1, Found: 3, Fixed: 2},
				{Round: 2, Found: 1, Fixed: 1},
			},
			want: DashboardRoundTotals{Iterations: 2, Violations: 4, Fixes: 3, Distinct: false},
		},
		{
			name: "one round without ids sums every round",
			rounds: []PlanReviewRound{
				{Round: 1, Found: 2, Fixed: 1, Findings: dashPlanFindings("!a", "b")},
				{Round: 2, Found: 3, Fixed: 2},
			},
			want: DashboardRoundTotals{Iterations: 2, Violations: 5, Fixes: 3, Distinct: false},
		},
		{
			name: "empty findings list counts as a round with ids",
			rounds: []PlanReviewRound{
				{Round: 1, Found: 2, Fixed: 1, Findings: dashPlanFindings()},
				{Round: 2, Found: 1, Fixed: 0, Findings: dashPlanFindings("a")},
			},
			want: DashboardRoundTotals{Iterations: 2, Violations: 1, Fixes: 0, Distinct: true},
		},
		{
			name: "same id in two rounds counts once",
			rounds: []PlanReviewRound{
				{Round: 1, Found: 1, Fixed: 0, Findings: dashPlanFindings("a")},
				{Round: 2, Found: 1, Fixed: 1, Findings: dashPlanFindings("!a")},
			},
			want: DashboardRoundTotals{Iterations: 2, Violations: 1, Fixes: 1, Distinct: true},
		},
		{
			name: "id fixed in one round counts as fixed",
			rounds: []PlanReviewRound{
				{Round: 1, Found: 1, Fixed: 1, Findings: dashPlanFindings("!a")},
				{Round: 2, Found: 1, Fixed: 0, Findings: dashPlanFindings("a")},
			},
			want: DashboardRoundTotals{Iterations: 2, Violations: 1, Fixes: 1, Distinct: true},
		},
		{
			name:   "one round",
			rounds: []PlanReviewRound{{Round: 1, Found: 4, Fixed: 4}},
			want:   DashboardRoundTotals{Iterations: 1, Violations: 4, Fixes: 4, Distinct: false},
		},
		{
			name:   "no rounds",
			rounds: nil,
			want:   DashboardRoundTotals{Distinct: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dashboardPlanTotals(tc.rounds)
			if got == nil {
				t.Fatal("totals = nil, want a value")
			}
			if *got != tc.want {
				t.Errorf("totals = %+v, want %+v", *got, tc.want)
			}
		})
	}
}

// TestDashboardPlanRepairLimit_LastRoundAtLimit checks that the flag is set
// only when the last round has reached maxReviewRounds with issues found.
func TestDashboardPlanRepairLimit_LastRoundAtLimit(t *testing.T) {
	round := func(n int, status string) PlanReviewRound { return PlanReviewRound{Round: n, MergedStatus: status} }
	cases := []struct {
		name   string
		rounds []PlanReviewRound
		want   bool
	}{
		{"limit round with issues", []PlanReviewRound{round(4, planStatusIssuesFound), round(maxReviewRounds, planStatusIssuesFound)}, true},
		{"round past the limit with issues", []PlanReviewRound{round(maxReviewRounds, planStatusIssuesFound), round(maxReviewRounds+1, planStatusIssuesFound)}, true},
		{"limit round approved", []PlanReviewRound{round(4, planStatusIssuesFound), round(maxReviewRounds, planStatusApproved)}, false},
		{"issues before the limit", []PlanReviewRound{round(3, planStatusIssuesFound), round(maxReviewRounds-1, planStatusIssuesFound)}, false},
		{"limit round with issues is not last", []PlanReviewRound{round(maxReviewRounds, planStatusIssuesFound), round(maxReviewRounds+1, planStatusApproved)}, false},
		{"no rounds", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dashboardPlanRepairLimit(tc.rounds); got != tc.want {
				t.Errorf("repair limit = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDashboardPlanOutcomes_CopiesStoredFindings checks that the outcomes
// copy the stored review outcome in order, and that every shape without a
// finding gives no outcomes.
func TestDashboardPlanOutcomes_CopiesStoredFindings(t *testing.T) {
	t.Run("copies findings in order", func(t *testing.T) {
		raw := map[string]any{"findings": []any{
			map[string]any{"id": "f-0000000a", "text": "Wave 2 has no verify step", "choice": outcomeChoiceAccepted, "reason": "covered by the final check"},
			map[string]any{"id": "f-0000000b", "text": "Task 4 lists no files", "choice": outcomeChoiceStop, "reason": ""},
		}}
		want := []DashboardFindingOutcome{
			{ID: "f-0000000a", Text: "Wave 2 has no verify step", Choice: outcomeChoiceAccepted, Reason: "covered by the final check"},
			{ID: "f-0000000b", Text: "Task 4 lists no files", Choice: outcomeChoiceStop, Reason: ""},
		}
		if got := dashboardPlanOutcomes(raw); !reflect.DeepEqual(got, want) {
			t.Errorf("outcomes = %+v, want %+v", got, want)
		}
	})
	for _, tc := range []struct {
		name string
		raw  any
	}{
		{"key absent", nil},
		{"empty findings", map[string]any{"findings": []any{}}},
		{"findings key absent", map[string]any{}},
		{"findings null", map[string]any{"findings": nil}},
		{"not an object", "accepted"},
		{"findings not a list", map[string]any{"findings": "f-0000000a"}},
		{"finding field of wrong type", map[string]any{"findings": []any{map[string]any{"id": float64(7)}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dashboardPlanOutcomes(tc.raw); got != nil {
				t.Errorf("outcomes = %+v, want nil", got)
			}
		})
	}
}

// TestDashboardPlan_ReviewDetailHasTotalsLimitAndOutcomes reads the review
// station detail from plan state files: the totals, the repair-limit flag and
// the outcomes.
func TestDashboardPlan_ReviewDetailHasTotalsLimitAndOutcomes(t *testing.T) {
	round := func(n int, status string, found, fixed int, findings any) map[string]any {
		r := map[string]any{"round": n, "mergedStatus": status, "found": found, "fixed": fixed, "lenses": []any{}}
		if findings != nil {
			r["findings"] = findings
		}
		return r
	}
	finding := func(id string, fixed bool) map[string]any { return map[string]any{"id": id, "fixed": fixed} }

	t.Run("distinct totals and no limit", func(t *testing.T) {
		root := dashRoot(t)
		p := dashPlanState(t, root, "6", map[string]any{"reviewRounds": []any{
			round(1, planStatusIssuesFound, 3, 2, []any{finding("f-0000000a", true), finding("f-0000000b", false), finding("f-0000000c", true)}),
			round(2, planStatusApproved, 1, 1, []any{finding("f-0000000b", true)}),
		}})
		d := dashPlanStep(t, p, "review").Detail
		if d == nil {
			t.Fatal("review detail = nil, want rounds")
		}
		if want := (DashboardRoundTotals{Iterations: 2, Violations: 3, Fixes: 3, Distinct: true}); d.RoundTotals == nil || *d.RoundTotals != want {
			t.Errorf("roundTotals = %+v, want %+v", d.RoundTotals, want)
		}
		if d.RepairLimit {
			t.Error("repairLimit = true, want false")
		}
		if d.Outcomes != nil {
			t.Errorf("outcomes = %+v, want nil", d.Outcomes)
		}
	})

	t.Run("summed totals when a round has no ids", func(t *testing.T) {
		root := dashRoot(t)
		p := dashPlanState(t, root, "6", map[string]any{"reviewRounds": []any{
			round(1, planStatusIssuesFound, 3, 2, nil),
			round(2, planStatusApproved, 1, 1, []any{finding("f-0000000b", true)}),
		}})
		d := dashPlanStep(t, p, "review").Detail
		if d == nil {
			t.Fatal("review detail = nil, want rounds")
		}
		if want := (DashboardRoundTotals{Iterations: 2, Violations: 4, Fixes: 3, Distinct: false}); d.RoundTotals == nil || *d.RoundTotals != want {
			t.Errorf("roundTotals = %+v, want %+v", d.RoundTotals, want)
		}
	})

	t.Run("empty findings list keeps distinct totals", func(t *testing.T) {
		root := dashRoot(t)
		p := dashPlanState(t, root, "6", map[string]any{"reviewRounds": []any{
			round(1, planStatusApproved, 2, 2, []any{}),
		}})
		d := dashPlanStep(t, p, "review").Detail
		if d == nil {
			t.Fatal("review detail = nil, want rounds")
		}
		if want := (DashboardRoundTotals{Iterations: 1, Violations: 0, Fixes: 0, Distinct: true}); d.RoundTotals == nil || *d.RoundTotals != want {
			t.Errorf("roundTotals = %+v, want %+v", d.RoundTotals, want)
		}
	})

	t.Run("repair limit with outcomes", func(t *testing.T) {
		root := dashRoot(t)
		rounds := []any{}
		for n := 1; n <= maxReviewRounds; n++ {
			rounds = append(rounds, round(n, planStatusIssuesFound, 1, 0, []any{finding("f-0000000a", false)}))
		}
		p := dashPlanState(t, root, "6", map[string]any{
			"reviewRounds": rounds,
			"reviewOutcome": map[string]any{"findings": []any{
				map[string]any{"id": "f-0000000a", "text": "Task 3 depends on itself", "choice": outcomeChoiceRejected, "reason": "plan is still wrong"},
			}},
		})
		d := dashPlanStep(t, p, "review").Detail
		if d == nil {
			t.Fatal("review detail = nil, want rounds")
		}
		if !d.RepairLimit {
			t.Error("repairLimit = false, want true")
		}
		if want := (DashboardRoundTotals{Iterations: maxReviewRounds, Violations: 1, Fixes: 0, Distinct: true}); d.RoundTotals == nil || *d.RoundTotals != want {
			t.Errorf("roundTotals = %+v, want %+v", d.RoundTotals, want)
		}
		wantOutcomes := []DashboardFindingOutcome{{ID: "f-0000000a", Text: "Task 3 depends on itself", Choice: outcomeChoiceRejected, Reason: "plan is still wrong"}}
		if !reflect.DeepEqual(d.Outcomes, wantOutcomes) {
			t.Errorf("outcomes = %+v, want %+v", d.Outcomes, wantOutcomes)
		}
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"repairLimit":true`, `"roundTotals":{"iterations":5,"violations":1,"fixes":0,"distinct":true}`, `"outcomes":[{`} {
			if !strings.Contains(string(b), want) {
				t.Errorf("JSON = %s, want it to contain %s", b, want)
			}
		}
	})

	t.Run("no outcome key leaves outcomes out of the JSON", func(t *testing.T) {
		root := dashRoot(t)
		p := dashPlanState(t, root, "6", map[string]any{"reviewRounds": []any{
			round(1, planStatusApproved, 0, 0, []any{}),
		}, "reviewOutcome": map[string]any{"findings": []any{}}})
		d := dashPlanStep(t, p, "review").Detail
		if d == nil {
			t.Fatal("review detail = nil, want rounds")
		}
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, absent := range []string{`"outcomes"`, `"repairLimit"`} {
			if strings.Contains(string(b), absent) {
				t.Errorf("JSON = %s, want it to omit %s", b, absent)
			}
		}
	})

	t.Run("outcome without rounds gives no review detail", func(t *testing.T) {
		root := dashRoot(t)
		p := dashPlanState(t, root, "6", map[string]any{
			"reviewOutcome": map[string]any{"findings": []any{
				map[string]any{"id": "f-0000000a", "text": "t", "choice": outcomeChoiceAccepted, "reason": "r"},
			}},
		})
		if d := dashPlanStep(t, p, "review").Detail; d != nil {
			t.Errorf("review detail = %+v, want nil", d)
		}
	})
}
