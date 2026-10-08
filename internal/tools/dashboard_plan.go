package tools

import (
	"encoding/json"
	"fmt"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// Station names of a plan pipeline. The explore and review stations carry
// a step detail; the other stations never do.
const (
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
	{"setup", []string{"0"}},
	{dashboardPlanStationExplore, []string{"1"}},
	{"draft", []string{"2"}},
	{dashboardPlanStationReview, []string{"3", "4", "5", "6"}},
	{"finalize", []string{"6.5", "6.6", "7"}},
}

// dashboardPlan fills a plan pipeline: one step for each station of
// dashboardPlanStations. The station that holds checkpoint.step is in
// progress, the stations before it are completed. A plan whose
// planIntegrity.done is set is completed and shows every station completed.
// The explore station lists the run's explorers and the review station lists
// data.reviewRounds; a station with nothing to list has no detail.
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
	rounds := dashboardPlanRounds(data["reviewRounds"])

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
		case dashboardPlanStationExplore:
			step.Detail = dashboardPlanExploreDetail(st)
		case dashboardPlanStationReview:
			if len(rounds) > 0 {
				step.Detail = &DashboardStepDetail{Kind: dashboardKindRounds, Rounds: rounds, MaxRounds: maxReviewRounds}
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

// dashboardPlanRounds maps the plan state value data.reviewRounds to
// dashboard rounds, in stored order. raw is absent, or the []any a state
// file decodes to; a value that is not a list of review rounds gives no
// rounds. Each Lenses list is non-nil, so it marshals as [].
func dashboardPlanRounds(raw any) []DashboardRound {
	if raw == nil {
		return nil
	}
	var stored []PlanReviewRound
	b, err := json.Marshal(raw)
	if err == nil {
		err = json.Unmarshal(b, &stored)
	}
	if err != nil {
		return nil
	}
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
