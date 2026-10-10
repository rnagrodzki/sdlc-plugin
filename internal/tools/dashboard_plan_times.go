package tools

import (
	"fmt"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// dashboardAttachPlanTimes gives the ship pipeline p the times of the plan
// run that came before it: p.PlanStartedAt, and the times of the "plan"
// station. st is the ship state of p. It is the only owner of plan times, so
// the station of data.planExploreSummary and the timed station stay one
// station: an existing "plan" step gets the times, and with no such step a
// completed "plan" step is added through dashboardInsertPlanStation.
//
// The sources, first match wins:
//  1. data.linkedPlan of the ship state (startedAt, completedAt);
//  2. the plan row of runs.jsonl that shipPlanTimingFor finds for the
//     planPath of the execute state of the same branch (StartedAt,
//     LastModifiedAt).
//
// A plan time is never guessed. A start time that does not parse as RFC 3339
// gives no times, no station and no PlanStartedAt, and a source with such a
// start time falls through to the next one. A completedAt that is absent,
// does not parse or is before the start gives a station with a start time
// only. A read error of the execute state or of runs.jsonl leaves p as it
// is and returns the warning text for the repo; a missing execute state or
// a missing plan row is not a warning.
func dashboardAttachPlanTimes(root string, p *DashboardPipeline, st *state.State) (warning string) {
	startedAt, completedAt, err := dashboardPlanTimes(root, st)
	if err != nil {
		return fmt.Sprintf("plan times of ship run %s not read: %v", p.ID, err)
	}
	if startedAt == "" {
		return ""
	}
	step := dashboardStepNamed(p, dashboardShipStepPlan)
	if step == nil {
		dashboardInsertPlanStation(p, DashboardStep{Name: dashboardShipStepPlan, Status: StepCompleted})
		step = &p.Steps[0]
	}
	step.StartedAt, step.CompletedAt = startedAt, completedAt
	p.PlanStartedAt = startedAt
	return ""
}

// dashboardPlanTimes returns the plan start and end of the ship state st, as
// RFC 3339 texts. startedAt is "" when no source holds a plan start. See
// dashboardAttachPlanTimes for the sources and the rules.
func dashboardPlanTimes(root string, st *state.State) (startedAt, completedAt string, err error) {
	if linked, _ := st.Data[shipLinkedPlanKey].(map[string]any); linked != nil {
		if start := dashboardStepTime(dashboardStr(linked["startedAt"])); start != "" {
			return start, dashboardPlanEnd(start, dashboardStr(linked["completedAt"])), nil
		}
	}

	branch := dashboardStr(st.Data["branch"])
	if branch == "" {
		return "", "", nil
	}
	execSt, err := state.Find(root, "execute", branch)
	if err != nil || execSt == nil {
		return "", "", err
	}
	timing, err := shipPlanTimingFor(root, execSt.Data)
	if err != nil || timing == nil {
		return "", "", err
	}
	start := dashboardStepTime(timing.StartedAt)
	if start == "" {
		return "", "", nil
	}
	return start, dashboardPlanEnd(start, timing.LastModifiedAt), nil
}

// dashboardPlanEnd returns end when it parses and is not before start, else
// "". Both are RFC 3339 texts; start has parsed already.
func dashboardPlanEnd(start, end string) string {
	end = dashboardStepTime(end)
	if end == "" {
		return ""
	}
	s, _ := dashboardParseTime(start)
	if e, _ := dashboardParseTime(end); e.Before(s) {
		return ""
	}
	return end
}
