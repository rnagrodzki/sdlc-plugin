package tools

import "github.com/rnagrodzki/sdlc-plugin/internal/state"

// dashboardPlan fills a plan pipeline: one step for each checkpoint step in
// validCheckpointSteps. A plan whose planIntegrity.done is set is completed
// and shows every step completed.
func dashboardPlan(p *DashboardPipeline, st *state.State) {
	data := st.Data
	integrity, _ := data["planIntegrity"].(map[string]any)
	p.StartedAt = dashboardStr(integrity["skillInvoked"])
	if p.StartedAt == "" {
		intent, _ := data["creationIntent"].(map[string]any)
		p.StartedAt = dashboardStr(intent["timestamp"])
	}
	doneValue, isDone := integrity["done"]

	checkpoint, _ := data["checkpoint"].(map[string]any)
	stepID := dashboardStr(checkpoint["step"])
	cur := -1
	for i, s := range validCheckpointSteps {
		if s == stepID {
			cur = i
		}
	}

	for i, s := range validCheckpointSteps {
		status := StepPending
		switch {
		case isDone || i < cur:
			status = StepCompleted
		case i == cur:
			status = StepInProgress
		}
		p.Steps = append(p.Steps, DashboardStep{Name: "step " + s, Status: status})
	}

	p.Progress.Total = len(validCheckpointSteps)
	if isDone {
		p.Status = PipelineCompleted
		p.CompletedAt = dashboardStrPtr(dashboardStr(doneValue))
		p.Progress.Done = p.Progress.Total
	} else {
		p.Status = PipelineRunning
		if cur >= 0 {
			p.Progress.Done = cur
			p.Progress.Current = "step " + stepID
		}
	}
	p.Progress.Label = dashboardStepLabel(p.Progress)
}
