package tools

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
)

// Source values of a DashboardIssue.
const (
	dashboardSourceStep     = "step"
	dashboardSourceWave     = "wave"
	dashboardSourceReview   = "review"
	dashboardSourceState    = "state"
	dashboardSourceTask     = "task"
	dashboardSourcePipeline = "pipeline"
)

// dashboardStalledText is the text of the issue of a stalled pipeline. It
// holds no clock value: the page shows the real age from updatedAt, and a text
// that changed each minute would send a new snapshot to every client.
const dashboardStalledText = "no update for 30+ min"

// dashboardSeverity maps a severity of a state file or a review ledger to the
// severity values of the dashboard. A value of dimensions.ValidSeverities stays
// as it is. "error" gives "high" and "warning" gives "medium". Any other value,
// and an empty value, gives "info".
func dashboardSeverity(s string) string {
	switch {
	case slices.Contains(dimensions.ValidSeverities, s):
		return s
	case s == "error":
		return "high"
	case s == "warning":
		return "medium"
	}
	return "info"
}

// dashboardStepIssue is the issue of a failed ship step. Its ref is the name
// of the step.
func dashboardStepIssue(name, text string) DashboardIssue {
	return DashboardIssue{Source: dashboardSourceStep, Severity: "high", Text: text, Ref: name}
}

// dashboardWaveIssue is the issue of a failed or partial execute wave. Its ref
// is the name of the wave.
func dashboardWaveIssue(name, status string) DashboardIssue {
	return DashboardIssue{Source: dashboardSourceWave, Severity: "high", Text: status, Ref: name}
}

// dashboardTaskIssue is the issue of a failed execute task. Its ref is the id
// of the task.
func dashboardTaskIssue(id, text string) DashboardIssue {
	return DashboardIssue{Source: dashboardSourceTask, Severity: "high", Text: text, Ref: id}
}

// dashboardReviewIssue is the issue of one finding of review dimension dim.
// The text is the rationale of the finding. The location is in File and Line.
func dashboardReviewIssue(dim string, f dashboardFinding) DashboardIssue {
	return DashboardIssue{
		Source:   dashboardSourceReview,
		Severity: dashboardSeverity(f.Severity),
		Text:     strings.TrimSpace(f.Rationale),
		File:     f.File,
		Line:     dashboardFindingLine(f),
		Ref:      dim,
	}
}

// dashboardFindingLine returns the line of a finding as a string: "" when the
// ledger holds no line, "42" for a number, and a text such as "12-14" as it
// is.
func dashboardFindingLine(f dashboardFinding) string {
	switch line := f.Line.(type) {
	case float64:
		if line > 0 {
			return strconv.Itoa(int(line))
		}
	case string:
		return strings.TrimSpace(line)
	}
	return ""
}

// dashboardIssueKey names an issue by its source and its ref.
type dashboardIssueKey struct {
	source string
	ref    string
}

// dashboardStateIssues adds the issues that a ship or execute state file
// records to p: first one task issue for each failed task row that has an
// error, then one state issue for each entry of data.issues[]. A state issue
// that repeats a step, wave, or task issue already in p is dropped. All other
// state issues stay.
func dashboardStateIssues(p *DashboardPipeline, data map[string]any) {
	dashboardTaskIssues(p, data)

	have := map[dashboardIssueKey]bool{}
	for _, is := range p.Issues {
		have[dashboardIssueKey{is.Source, is.Ref}] = true
	}

	list, _ := data["issues"].([]any)
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		step, taskID := dashboardStr(m["step"]), dashboardStr(m["taskId"])
		waveRef := ""
		if n := dashboardInt(m["wave"]); n > 0 {
			waveRef = fmt.Sprintf("wave %d", n)
		}

		switch dashboardStr(m["category"]) {
		case "ship-fail":
			if have[dashboardIssueKey{dashboardSourceStep, step}] {
				continue
			}
		case "wave-fail":
			if have[dashboardIssueKey{dashboardSourceWave, waveRef}] {
				continue
			}
		case "task-fail":
			if have[dashboardIssueKey{dashboardSourceTask, taskID}] {
				continue
			}
		}

		ref := waveRef
		switch {
		case taskID != "":
			ref = taskID
		case step != "":
			ref = step
		}
		text := dashboardStr(m["summary"])
		if detail := dashboardStr(m["detail"]); detail != "" {
			text += ": " + detail
		}
		p.Issues = append(p.Issues, DashboardIssue{
			Source:   dashboardSourceState,
			Severity: dashboardSeverity(dashboardStr(m["severity"])),
			Text:     text,
			Ref:      ref,
		})
	}
}

// dashboardTaskIssues adds one task issue to p for each task row of the
// waves of data that has the status failed and an error text. A row that was
// skipped because of a failed dependency gives no issue.
func dashboardTaskIssues(p *DashboardPipeline, data map[string]any) {
	waves, _ := data["waves"].([]any)
	for _, rw := range waves {
		w, ok := rw.(map[string]any)
		if !ok {
			continue
		}
		tasks, _ := w["tasks"].([]any)
		for _, rt := range tasks {
			t, ok := rt.(map[string]any)
			if !ok {
				continue
			}
			id, text := dashboardStr(t["id"]), dashboardStr(t["error"])
			if id == "" || text == "" || dashboardStr(t["status"]) != StepFailed {
				continue
			}
			p.Issues = append(p.Issues, dashboardTaskIssue(id, text))
		}
	}
}

// dashboardAddStalledIssue adds the issue of a stalled pipeline to p.
func dashboardAddStalledIssue(p *DashboardPipeline) {
	p.Issues = append(p.Issues, DashboardIssue{
		Source:   dashboardSourcePipeline,
		Severity: "medium",
		Text:     dashboardStalledText,
	})
}

// dashboardReviewFindings gives each completed step of the review pipeline p
// a findings detail. The findings are the review issues of p whose ref is the
// name of the step, in issue order, as dashboardDimensionFindings builds them.
// A step with no finding gets the detail with a nil list. A step that is not
// completed gets no detail.
func dashboardReviewFindings(p *DashboardPipeline) {
	for i := range p.Steps {
		step := &p.Steps[i]
		if step.Status != StepCompleted {
			continue
		}
		detail := &DashboardStepDetail{Kind: dashboardKindFindings}
		if rows := dashboardDimensionFindings(p.Issues, step.Name); len(rows) > 0 {
			detail.Findings = rows
		}
		step.Detail = detail
	}
}
