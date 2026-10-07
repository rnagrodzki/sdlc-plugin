package tools

import "github.com/rnagrodzki/sdlc-plugin/internal/state"

// dashboardShipDetail adds the step detail and the join keys of a ship run to
// its pipeline. It is a no-op until the ship read work fills it.
func dashboardShipDetail(p *DashboardPipeline, st *state.State) {}

// dashboardReviewMeta reads the run.meta file of the review ledger folder dir
// into the join keys of p. It is a no-op until the review read work fills it.
func dashboardReviewMeta(dir string, p *DashboardPipeline) {}

// dashboardJoinRuns joins the execute and review runs of a repo into the ship
// runs that started them, through the join keys of each pipeline. It returns
// pipelines unchanged until the join work fills it.
func dashboardJoinRuns(pipelines []DashboardPipeline) []DashboardPipeline {
	return pipelines
}
