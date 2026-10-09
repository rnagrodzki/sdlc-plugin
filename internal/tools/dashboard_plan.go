package tools

import (
	"encoding/json"
	"fmt"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// Station names of a plan pipeline. The setup, explore and review stations
// carry a step detail; the other stations never do.
const (
	dashboardPlanStationSetup   = "setup"
	dashboardPlanStationExplore = "explore"
	dashboardPlanStationReview  = "review"
)

// dashboardPlanStations groups the checkpoint step ids of validCheckpointSteps
// into the five stations the dashboard shows for a plan pipeline. The
// stations keep the order of validCheckpointSteps, and each id belongs to
// exactly one station.
var dashboardPlanStations = []struct {
	name  string
	steps []string
}{
	{dashboardPlanStationSetup, []string{"0"}},
	{dashboardPlanStationExplore, []string{"1"}},
	{"draft", []string{"2"}},
	{dashboardPlanStationReview, []string{"3", "4", "5", "6"}},
	{"finalize", []string{"6.5", "6.6", "7"}},
}

// dashboardPlan fills a plan pipeline: one step for each station of
// dashboardPlanStations. The station that holds checkpoint.step is in
// progress, the stations before it are completed. A plan whose
// planIntegrity.done is set is completed and shows every station completed.
// The setup station shows data.guardrailCounts, the explore station lists the
// run's explorers, and the review station lists data.reviewRounds with their
// totals, the repair-limit flag and data.reviewOutcome; a station with
// nothing to show has no detail.
func dashboardPlan(p *DashboardPipeline, st *state.State) {
	data := st.Data
	integrity, _ := data["planIntegrity"].(map[string]any)
	p.StartedAt = dashboardStr(integrity["skillInvoked"])
	if p.StartedAt == "" {
		intent, _ := data["creationIntent"].(map[string]any)
		p.StartedAt = dashboardStr(intent["timestamp"])
	}
	p.SessionID = dashboardStr(data["sessionId"])
	doneValue, isDone := integrity["done"]

	checkpoint, _ := data["checkpoint"].(map[string]any)
	cur := dashboardPlanStationIndex(dashboardStr(checkpoint["step"]))
	stored := dashboardPlanStoredRounds(data[planReviewRoundsKey])
	rounds := dashboardPlanRounds(stored)

	for i, s := range dashboardPlanStations {
		status := StepPending
		switch {
		case isDone || i < cur:
			status = StepCompleted
		case i == cur:
			status = StepInProgress
		}
		step := DashboardStep{Name: s.name, Status: status}
		switch s.name {
		case dashboardPlanStationSetup:
			step.Detail = dashboardPlanGuardrails(data["guardrailCounts"])
		case dashboardPlanStationExplore:
			step.Detail = dashboardPlanExploreDetail(st)
		case dashboardPlanStationReview:
			if len(rounds) > 0 {
				step.Detail = &DashboardStepDetail{
					Kind:        dashboardKindRounds,
					Rounds:      rounds,
					MaxRounds:   maxReviewRounds,
					RoundTotals: dashboardPlanTotals(stored),
					RepairLimit: dashboardPlanRepairLimit(stored),
					Outcomes:    dashboardPlanOutcomes(data["reviewOutcome"]),
				}
			}
		}
		p.Steps = append(p.Steps, step)
	}

	p.Progress.Total = len(dashboardPlanStations)
	if isDone {
		p.Status = PipelineCompleted
		p.CompletedAt = dashboardStrPtr(dashboardStr(doneValue))
		p.Progress.Done = p.Progress.Total
		p.Progress.Label = dashboardStepLabel(p.Progress)
		return
	}
	p.Status = PipelineRunning
	if cur < 0 {
		p.Progress.Label = dashboardStepLabel(p.Progress)
		return
	}
	name := dashboardPlanStations[cur].name
	p.Progress.Done = cur
	p.Progress.Current = name
	if name == dashboardPlanStationReview {
		p.Progress.Label = fmt.Sprintf("round %d of %d", min(len(rounds)+1, maxReviewRounds), maxReviewRounds)
	} else {
		p.Progress.Label = fmt.Sprintf("%s (%d of %d)", name, cur+1, p.Progress.Total)
	}
}

// dashboardPlanStationIndex returns the index in dashboardPlanStations of
// the station that holds checkpoint step id, or -1 for an unknown id.
func dashboardPlanStationIndex(id string) int {
	for i, s := range dashboardPlanStations {
		for _, step := range s.steps {
			if step == id {
				return i
			}
		}
	}
	return -1
}

// dashboardPlanExploreDetail returns the explore station detail: the
// explorers of the run's evidence store. It returns nil when the run has no
// explorers or when the evidence store cannot be read; a read error never
// fails the snapshot.
func dashboardPlanExploreDetail(st *state.State) *DashboardStepDetail {
	entries, err := planExploreSummary(st.Root, state.RunID(st))
	if err != nil || len(entries) == 0 {
		return nil
	}
	return &DashboardStepDetail{Kind: dashboardKindExplorers, Explorers: dashboardExplorers(entries)}
}

// dashboardExplorers maps explore summary entries to dashboard explorers.
// The result and each Findings list are non-nil, so they marshal as [].
func dashboardExplorers(entries []ExploreSummaryEntry) []DashboardExplorer {
	out := make([]DashboardExplorer, 0, len(entries))
	for _, e := range entries {
		findings := make([]DashboardFinding, 0, len(e.Top))
		for _, it := range e.Top {
			findings = append(findings, DashboardFinding{Summary: it.Summary, Ref: it.Ref})
		}
		out = append(out, DashboardExplorer{Name: e.Name, Status: e.Status, Total: e.Total, Findings: findings})
	}
	return out
}

// dashboardRecode copies the state value raw into dst through JSON and
// reports whether the copy worked. A state file decodes to maps and lists, so
// this is how the dashboard reads a state value as a typed struct. After a
// false result dst can be partly filled and the caller must not use it.
func dashboardRecode(raw any, dst any) bool {
	b, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, dst) == nil
}

// dashboardPlanStoredRounds reads the plan state value data.reviewRounds as
// review rounds, in stored order. raw is absent, or the []any a state file
// decodes to; a value that is not a list of review rounds gives no rounds.
func dashboardPlanStoredRounds(raw any) []PlanReviewRound {
	if raw == nil {
		return nil
	}
	var stored []PlanReviewRound
	if !dashboardRecode(raw, &stored) {
		return nil
	}
	return stored
}

// dashboardPlanRounds maps stored review rounds to dashboard rounds, in the
// same order. Each Lenses list is non-nil, so it marshals as [].
func dashboardPlanRounds(stored []PlanReviewRound) []DashboardRound {
	out := make([]DashboardRound, 0, len(stored))
	for _, r := range stored {
		lenses := make([]DashboardLens, 0, len(r.Lenses))
		for _, l := range r.Lenses {
			lenses = append(lenses, DashboardLens{Name: l.Name, Verdict: l.Verdict})
		}
		out = append(out, DashboardRound{N: r.Round, Status: r.MergedStatus, Found: r.Found, Fixed: r.Fixed, Lenses: lenses})
	}
	return out
}

// dashboardPlanGuardrails returns the setup station detail: the guardrail
// counts of the plan state value data.guardrailCounts. It returns nil when
// raw is absent or is not an object that holds total, error and warning as
// whole numbers of zero or more.
func dashboardPlanGuardrails(raw any) *DashboardStepDetail {
	var counts struct {
		Total   *int `json:"total"`
		Error   *int `json:"error"`
		Warning *int `json:"warning"`
	}
	if raw == nil || !dashboardRecode(raw, &counts) {
		return nil
	}
	if counts.Total == nil || counts.Error == nil || counts.Warning == nil {
		return nil
	}
	if *counts.Total < 0 || *counts.Error < 0 || *counts.Warning < 0 {
		return nil
	}
	return &DashboardStepDetail{
		Kind:       dashboardKindGuardrails,
		Guardrails: &DashboardGuardrailCounts{Total: *counts.Total, Error: *counts.Error, Warning: *counts.Warning},
	}
}

// dashboardPlanTotals totals stored review rounds. When every round has a
// findings list (an empty list counts), Violations and Fixes count distinct
// finding IDs across all rounds, a finding counts as fixed when any round
// fixed it, and Distinct is true. When any round has no findings list,
// Violations and Fixes are the sums of the Found and Fixed counts of all
// rounds and Distinct is false.
func dashboardPlanTotals(stored []PlanReviewRound) *DashboardRoundTotals {
	totals := DashboardRoundTotals{Iterations: len(stored), Distinct: true}
	found := map[string]bool{}
	fixed := map[string]bool{}
	for _, r := range stored {
		totals.Violations += r.Found
		totals.Fixes += r.Fixed
		if r.Findings == nil {
			totals.Distinct = false
			continue
		}
		for _, f := range *r.Findings {
			found[f.ID] = true
			if f.Fixed {
				fixed[f.ID] = true
			}
		}
	}
	if totals.Distinct {
		totals.Violations = len(found)
		totals.Fixes = len(fixed)
	}
	return &totals
}

// dashboardPlanRepairLimit reports whether the review loop stopped at its
// limit: the last stored round has a number of at least maxReviewRounds and
// its status is planStatusIssuesFound. Stored rounds are sorted by round
// number, so the last element is the latest round.
func dashboardPlanRepairLimit(stored []PlanReviewRound) bool {
	if len(stored) == 0 {
		return false
	}
	last := stored[len(stored)-1]
	return last.Round >= maxReviewRounds && last.MergedStatus == planStatusIssuesFound
}

// dashboardPlanOutcomes maps the plan state value data.reviewOutcome to
// dashboard finding outcomes, in stored order. It returns nil when raw is
// absent, is not a review outcome, or holds no findings, so the outcomes key
// is absent from the snapshot.
func dashboardPlanOutcomes(raw any) []DashboardFindingOutcome {
	if raw == nil {
		return nil
	}
	var stored PlanReviewOutcome
	if !dashboardRecode(raw, &stored) || len(stored.Findings) == 0 {
		return nil
	}
	out := make([]DashboardFindingOutcome, 0, len(stored.Findings))
	for _, f := range stored.Findings {
		out = append(out, DashboardFindingOutcome{ID: f.ID, Text: f.Text, Choice: f.Choice, Reason: f.Reason})
	}
	return out
}
