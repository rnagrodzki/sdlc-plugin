package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// dashboardKinds is the closed set of step detail kinds. Every test of the
// set reads this list, so a new kind fails them until it is added here.
var dashboardKinds = []string{
	dashboardKindWaves,
	dashboardKindDimensions,
	dashboardKindExplorers,
	dashboardKindRounds,
	dashboardKindFindings,
	dashboardKindGuardrails,
	dashboardKindResult,
	dashboardKindFixes,
}

// TestDashboardContract_KindSet pins the closed set of step detail kinds to
// the 8 values the page reads.
func TestDashboardContract_KindSet(t *testing.T) {
	want := []string{"waves", "dimensions", "explorers", "rounds", "findings", "guardrails", "result", "fixes"}
	if !slices.Equal(dashboardKinds, want) {
		t.Errorf("dashboardKinds = %v, want %v", dashboardKinds, want)
	}
}

// TestDashboardContract_NewListsNeverNull marshals a pipeline with an empty
// step detail of each kind, plus one detail of each kind whose items hold
// empty lists. The JSON must hold no null, and nil omitempty fields must be
// absent.
func TestDashboardContract_NewListsNeverNull(t *testing.T) {
	kinds := dashboardKinds
	p := DashboardPipeline{
		ID:     "execute-feat-x-20261007T090000Z",
		Kind:   "execute",
		Steps:  []DashboardStep{},
		Issues: []DashboardIssue{{Source: "step", Severity: "high", Text: "x"}},
		join: dashboardJoinInfo{
			shipRunID: "secret-join-key",
			startedAt: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC),
		},
	}
	for _, k := range kinds {
		p.Steps = append(p.Steps, DashboardStep{Name: "empty " + k, Status: StepPending, Detail: &DashboardStepDetail{Kind: k}})
	}
	p.Steps = append(p.Steps,
		DashboardStep{Name: "waves", Status: StepInProgress, Detail: &DashboardStepDetail{
			Kind:   dashboardKindWaves,
			Waves:  []DashboardWave{{Number: 1, Status: StepPending, Tasks: []DashboardTask{}}},
			Queued: []DashboardTask{{ID: "3"}},
		}},
		DashboardStep{Name: "dimensions", Status: StepCompleted, Detail: &DashboardStepDetail{
			Kind:         dashboardKindDimensions,
			Dimensions:   []DashboardDimension{{Name: "security", Status: StepCompleted, FindingItems: []DashboardReviewFinding{}}},
			ReviewTotals: &DashboardReviewTotals{},
		}},
		DashboardStep{Name: "explorers", Status: StepCompleted, Detail: &DashboardStepDetail{
			Kind:      dashboardKindExplorers,
			Explorers: []DashboardExplorer{{Name: "code", Status: StepCompleted, Findings: []DashboardFinding{}}},
		}},
		DashboardStep{Name: "rounds", Status: StepCompleted, Detail: &DashboardStepDetail{
			Kind:      dashboardKindRounds,
			Rounds:    []DashboardRound{{N: 1, Lenses: []DashboardLens{}}},
			MaxRounds: 3,
		}},
		DashboardStep{Name: "findings", Status: StepCompleted, Detail: &DashboardStepDetail{
			Kind:     dashboardKindFindings,
			Findings: []DashboardReviewFinding{{Text: "x", Severity: "low"}},
		}},
		DashboardStep{Name: "setup", Status: StepCompleted, Detail: &DashboardStepDetail{
			Kind:       dashboardKindGuardrails,
			Guardrails: &DashboardGuardrailCounts{},
		}},
		DashboardStep{Name: "commit", Status: StepCompleted, Detail: &DashboardStepDetail{
			Kind:   dashboardKindResult,
			Result: "nothing to commit",
		}},
		DashboardStep{Name: "plan rounds", Status: StepCompleted, Detail: &DashboardStepDetail{
			Kind:        dashboardKindRounds,
			Rounds:      []DashboardRound{{N: 1, Lenses: []DashboardLens{}}},
			RoundTotals: &DashboardRoundTotals{},
			RepairLimit: true,
			Outcomes:    []DashboardFindingOutcome{{ID: "f-1", Choice: "stop"}},
		}},
		DashboardStep{Name: "ship review", Status: StepCompleted, Detail: &DashboardStepDetail{
			Kind:       dashboardKindDimensions,
			Dimensions: []DashboardDimension{{Name: "docs", Status: StepSkipped, Wave: 2, Reason: "missing", FindingItems: []DashboardReviewFinding{}}},
			ReviewPlan: &DashboardReviewPlan{},
		}},
	)

	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(strings.ReplaceAll(s, `"completedAt":null`, ""), "null") {
		t.Errorf("pipeline JSON holds null: %s", s)
	}
	if strings.Contains(s, "commitWaves") {
		t.Errorf("nil commitWaves is in the JSON, want absent: %s", s)
	}
	if strings.Contains(s, "attention") {
		t.Errorf("nil attention is in the JSON, want absent: %s", s)
	}
	if strings.Contains(s, "secret-join-key") || strings.Contains(s, `"join"`) {
		t.Errorf("join info is serialized: %s", s)
	}
	for _, key := range []string{
		`"sessionId":""`, `"detail":{"kind":"waves"}`, `"file":""`, `"line":""`, `"ref":""`,
		`"committedSha":""`, `"tasks":[]`, `"queued":[`, `"reviewTotals":{"found":0,"fixed":0,"deferred":0,"unaccounted":0}`,
		`"worst":""`, `"findings":[]`, `"lenses":[]`, `"maxRounds":3`,
		`"detail":{"kind":"guardrails"}`, `"detail":{"kind":"result"}`, // empty: no new field is in the JSON
		`"guardrails":{"total":0,"error":0,"warning":0}`,
		`"result":"nothing to commit"`,
		`"roundTotals":{"iterations":0,"violations":0,"fixes":0,"distinct":false}`,
		`"repairLimit":true`,
		`"outcomes":[{"id":"f-1","text":"","choice":"stop","reason":""}]`,
		`"reviewPlan":{"wavesPlanned":0,"wavesRun":0,"dimensionsPlanned":0,"dimensionsRun":0,"neverStarted":0}`,
		`"wave":2`, `"reason":"missing"`, `"findingItems":[]`,
	} {
		if !strings.Contains(s, key) {
			t.Errorf("pipeline JSON lacks %s: %s", key, s)
		}
	}

	off := false
	p.CommitWaves = &off
	if b, _ := json.Marshal(p); !strings.Contains(string(b), `"commitWaves":false`) {
		t.Errorf("commitWaves false is not in the JSON: %s", b)
	}

	p.Attention = &DashboardAttention{Kind: "question", AskedAt: "2026-10-07T09:00:00Z", Header: "Harden", Text: "Apply?"}
	want := `"attention":{"kind":"question","askedAt":"2026-10-07T09:00:00Z","header":"Harden","text":"Apply?"}`
	if b, _ := json.Marshal(p); !strings.Contains(string(b), want) {
		t.Errorf("attention is not in the JSON as %s: %s", want, b)
	}
}

// TestDashboardContract_HistoryIsEmptyList checks that a repo entry holds an
// empty history list, for a readable root and for an absent one.
func TestDashboardContract_HistoryIsEmptyList(t *testing.T) {
	orig := dashboardActivity
	t.Cleanup(func() { dashboardActivity = orig })
	dashboardActivity = func(string, time.Time) ([]DashboardSession, []DashboardLearning, []DashboardDeferred, []DashboardRun) {
		return nil, nil, nil, nil
	}

	for name, root := range map[string]string{
		"readable": dashRoot(t),
		"absent":   t.TempDir() + "/gone",
	} {
		b, err := json.Marshal(collectDashboardRepo(root, dashNow))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"history":[]`) {
			t.Errorf("%s repo JSON = %s, want history []", name, b)
		}
	}
}

// TestDashboardContract_RepoListsNeverNull checks that a repo entry encodes
// warnings and preplans as empty lists, never null, for a readable root, an
// absent root, a root that is a file, and a root whose runs folder cannot be
// read.
func TestDashboardContract_RepoListsNeverNull(t *testing.T) {
	orig := dashboardActivity
	t.Cleanup(func() { dashboardActivity = orig })
	dashboardActivity = func(string, time.Time) ([]DashboardSession, []DashboardLearning, []DashboardDeferred, []DashboardRun) {
		return nil, nil, nil, nil
	}

	notAFolder := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notAFolder, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	roots := map[string]string{
		"readable":     dashRoot(t),
		"absent":       t.TempDir() + "/gone",
		"not a folder": notAFolder,
	}
	if os.Geteuid() != 0 {
		// A folder that exists but cannot be listed: state.List fails and the
		// collector sets repo.Error, but the lists stay [].
		locked := dashRoot(t)
		runs := filepath.Join(locked, paths.DataDir, paths.RunsSubdir)
		if err := os.Chmod(runs, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(runs, 0o755) })
		roots["unreadable runs folder"] = locked
	}

	for name, root := range roots {
		repo := collectDashboardRepo(root, dashNow)
		if name == "unreadable runs folder" && repo.Error == "" {
			t.Errorf("%s: repo.Error is empty, want a read error", name)
		}
		b, err := json.Marshal(repo)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"warnings":[]`, `"preplans":[]`} {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s repo JSON = %s, want %s", name, b, want)
			}
		}
	}
}

// TestDashboardContract_PlanFieldsOmitted checks the encoding of the plan
// fields. planStartedAt of a pipeline, and planStartedAt and planDurationMs
// of a history row, are absent when empty. totalMs of a history row is always
// there, and 0 means unknown.
func TestDashboardContract_PlanFieldsOmitted(t *testing.T) {
	b, err := json.Marshal(DashboardPipeline{ID: "ship-feat-x-20261007T090000Z", Kind: "ship"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "planStartedAt") {
		t.Errorf("empty PlanStartedAt is in the pipeline JSON, want absent: %s", b)
	}
	b, err = json.Marshal(DashboardPipeline{Kind: "ship", PlanStartedAt: "2026-10-07T08:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"planStartedAt":"2026-10-07T08:00:00Z"`) {
		t.Errorf("PlanStartedAt is not in the pipeline JSON: %s", b)
	}

	b, err = json.Marshal(DashboardRun{Kind: "plan", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "planStartedAt") || strings.Contains(s, "planDurationMs") {
		t.Errorf("empty plan fields are in the history row JSON, want absent: %s", s)
	}
	if !strings.Contains(s, `"totalMs":0`) {
		t.Errorf("history row JSON lacks totalMs 0: %s", s)
	}

	b, err = json.Marshal(DashboardRun{
		Kind: "ship", Outcome: "success", DurationMs: 3600000,
		PlanStartedAt: "2026-10-10T08:20:00Z", PlanDurationMs: 4800000, TotalMs: 9600000,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"planStartedAt":"2026-10-10T08:20:00Z"`, `"planDurationMs":4800000`, `"totalMs":9600000`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("history row JSON lacks %s: %s", want, b)
		}
	}
}

// TestDashboardContract_FixtureHoldsLifeCycleFields checks that the shared
// snapshot fixture keeps one sample of each field the page tests rely on: a
// ship pipeline with plan times, three preplans, and a history row with plan
// fields.
func TestDashboardContract_FixtureHoldsLifeCycleFields(t *testing.T) {
	snap := dashboardFixtureDecode(t, dashboardFixtureRead(t))

	var preplans, shipWithPlanTimes, rowsWithPlan int
	for _, repo := range snap.Repos {
		if repo.Warnings == nil || repo.Preplans == nil {
			t.Errorf("repo %s: warnings or preplans is null, want a list", repo.Name)
		}
		preplans += len(repo.Preplans)
		for _, p := range repo.Pipelines {
			if p.Kind != "ship" || p.PlanStartedAt == "" {
				continue
			}
			for _, s := range p.Steps {
				if s.Name == "plan" && s.StartedAt != "" && s.CompletedAt != "" {
					shipWithPlanTimes++
				}
			}
		}
		for _, h := range repo.History {
			if h.PlanStartedAt != "" && h.PlanDurationMs > 0 && h.TotalMs > h.DurationMs {
				rowsWithPlan++
			}
			if h.TotalMs <= 0 {
				t.Errorf("repo %s: history row %s %s has totalMs %d, want a value", repo.Name, h.Kind, h.Branch, h.TotalMs)
			}
		}
	}
	if preplans != 3 {
		t.Errorf("fixture has %d preplans, want 3", preplans)
	}
	if shipWithPlanTimes == 0 {
		t.Error("fixture has no ship pipeline with planStartedAt and a plan step with both times")
	}
	if rowsWithPlan == 0 {
		t.Error("fixture has no history row with plan fields and a total longer than the ship duration")
	}
}
