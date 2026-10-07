package tools

// dashboardStepIssue is the issue of a failed ship step.
func dashboardStepIssue(name, text string) DashboardIssue {
	return DashboardIssue{Source: "step", Severity: "high", Text: name + ": " + text}
}

// dashboardWaveIssue is the issue of a failed or partial execute wave.
func dashboardWaveIssue(name, status string) DashboardIssue {
	return DashboardIssue{Source: "wave", Severity: "high", Text: name + ": " + status}
}

// dashboardReviewIssue is the issue of one finding of review dimension dim.
func dashboardReviewIssue(dim string, f dashboardFinding) DashboardIssue {
	return DashboardIssue{Source: "review", Severity: f.Severity, Text: dashboardFindingText(f)}
}

// dashboardStateIssues adds the issues that a ship or execute state file
// records to p. It is a no-op until the issue read work fills it.
func dashboardStateIssues(p *DashboardPipeline, data map[string]any) {}

// dashboardAddStalledIssue adds the issue of a stalled pipeline to p. It is a
// no-op until the issue read work fills it.
func dashboardAddStalledIssue(p *DashboardPipeline) {}

// dashboardReviewFindings sorts and groups the review issues of p. It is a
// no-op until the issue read work fills it.
func dashboardReviewFindings(p *DashboardPipeline) {}
