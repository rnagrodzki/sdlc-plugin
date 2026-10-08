package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestDashboardContract_NewListsNeverNull marshals a pipeline with an empty
// step detail of each kind, plus one detail of each kind whose items hold
// empty lists. The JSON must hold no null, and nil omitempty fields must be
// absent.
func TestDashboardContract_NewListsNeverNull(t *testing.T) {
	kinds := []string{
		dashboardKindWaves,
		dashboardKindDimensions,
		dashboardKindExplorers,
		dashboardKindRounds,
		dashboardKindFindings,
	}
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
			Dimensions:   []DashboardDimension{{Name: "security", Status: StepCompleted}},
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
	if strings.Contains(s, "secret-join-key") || strings.Contains(s, `"join"`) {
		t.Errorf("join info is serialized: %s", s)
	}
	for _, key := range []string{
		`"sessionId":""`, `"detail":{"kind":"waves"}`, `"file":""`, `"line":""`, `"ref":""`,
		`"committedSha":""`, `"tasks":[]`, `"queued":[`, `"reviewTotals":{"found":0,"fixed":0,"deferred":0,"unaccounted":0}`,
		`"worst":""`, `"findings":[]`, `"lenses":[]`, `"maxRounds":3`,
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
