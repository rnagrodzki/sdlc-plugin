package tools

import (
	"encoding/json"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// Step names of a ship pipeline that get a step detail from the join.
const (
	dashboardShipStepPlan    = "plan"
	dashboardShipStepExecute = "execute"
	dashboardShipStepReview  = "review"
)

// dashboardExecuteRefPrefix goes before the Ref of each issue that a joined
// execute run gives to its ship, so the ship can tell them from its own.
const dashboardExecuteRefPrefix = "execute:"

// dashboardShipDetail adds the ship-only data of a ship state to its
// pipeline: the session id, the join keys (run start and the window of each
// step), the review totals on the review step, and a first "plan" step when
// the state holds data.planExploreSummary. The plan step also lists the
// review rounds of data.planReviewRounds, with maxRounds, when that list
// holds rounds. The review step gets a dimensions detail only when
// shipBuildReviewLedger returns a ledger. A planExploreSummary value that
// does not decode gives no plan step.
func dashboardShipDetail(p *DashboardPipeline, st *state.State) {
	data := st.Data
	p.SessionID = dashboardStr(data["sessionId"])
	if t, ok := dashboardParseTime(p.StartedAt); ok {
		p.join.startedAt = t
	}

	p.join.stepWindows = map[string][2]time.Time{}
	for _, raw := range shipStepsSlice(data) {
		s, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := dashboardStr(s["name"])
		if _, seen := p.join.stepWindows[name]; seen || name == "" {
			continue // a repeated name keeps the window of its first step
		}
		start, _ := dashboardParseTime(dashboardStr(s["startedAt"]))
		end, _ := dashboardParseTime(dashboardStr(s["completedAt"]))
		p.join.stepWindows[name] = [2]time.Time{start, end}
	}

	healing, _ := data["healing"].(map[string]any)
	if healing == nil {
		healing = map[string]any{}
	}
	deferredFindings, _ := data["deferredFindings"].([]any)
	if ledger := shipBuildReviewLedger(healing, deferredFindings); ledger != nil {
		if step := dashboardStepNamed(p, dashboardShipStepReview); step != nil {
			deferred := 0
			for _, n := range ledger.DeferredByReason {
				deferred += n
			}
			step.Detail = &DashboardStepDetail{
				Kind:       dashboardKindDimensions,
				Dimensions: []DashboardDimension{},
				ReviewTotals: &DashboardReviewTotals{
					Found:       ledger.Total,
					Fixed:       ledger.Fixed,
					Deferred:    deferred,
					Unaccounted: ledger.Unaccounted,
				},
			}
		}
	}

	if entries, ok := dashboardDecodeExploreSummary(data["planExploreSummary"]); ok {
		plan := DashboardStep{
			Name:   dashboardShipStepPlan,
			Status: StepCompleted,
			Detail: &DashboardStepDetail{Kind: dashboardKindExplorers, Explorers: dashboardExplorers(entries)},
		}
		if rounds := dashboardPlanRounds(dashboardPlanStoredRounds(data[shipPlanReviewRoundsKey])); len(rounds) > 0 {
			plan.Detail.Rounds, plan.Detail.MaxRounds = rounds, maxReviewRounds
		}
		p.Steps = append([]DashboardStep{plan}, p.Steps...)
		p.Progress.Done++
		p.Progress.Total++
		p.Progress.Label = dashboardStepLabel(p.Progress)
	}
}

// dashboardDecodeExploreSummary decodes the ship state value
// data.planExploreSummary. ok is false when raw is absent or null, or is not
// a list of explore summary entries.
func dashboardDecodeExploreSummary(raw any) ([]ExploreSummaryEntry, bool) {
	if raw == nil {
		return nil, false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var entries []ExploreSummaryEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, false
	}
	return entries, true
}

// dashboardReviewMeta reads the run.meta file of the review ledger folder dir
// into p: the branch, the join start time, and the ship run id. The start time
// is the run.meta startedAt, else p.StartedAt. A missing or unparseable
// run.meta leaves p unchanged, so the review stays its own row; it never fails
// the snapshot.
func dashboardReviewMeta(dir string, p *DashboardPipeline) {
	meta, ok := readReviewRunMeta(dir)
	if !ok {
		return
	}
	p.Branch = meta.Branch
	p.join.shipRunID = meta.ShipRunID
	t, ok := dashboardParseTime(meta.StartedAt)
	if !ok {
		t, _ = dashboardParseTime(p.StartedAt)
	}
	p.join.startedAt = t
}

// dashboardJoinRuns nests execute and review runs into the ship run that
// started them and drops the nested runs from the list. The other pipelines
// keep their order. An execute run joins the ship of the same branch slug
// whose run window holds the execute start. A review run joins the ship whose
// id is its run.meta shipRunId; with no shipRunId, it joins the ship of the
// same branch slug whose review step window holds the review start. When two
// ships match, the newest ship wins. When two runs of one kind match one
// ship, the newest run nests and the other stays a row. Ship pipelines are
// changed in place.
func dashboardJoinRuns(in []DashboardPipeline) []DashboardPipeline {
	var ships []int
	for i := range in {
		if in[i].Kind == "ship" {
			ships = append(ships, i)
		}
	}
	if len(ships) == 0 {
		return in
	}

	execOf := map[int]int{}   // ship index -> nested execute index
	reviewOf := map[int]int{} // ship index -> nested review index
	for i := range in {
		var ship int
		var pick map[int]int
		switch in[i].Kind {
		case "execute":
			ship, pick = dashboardExecuteShip(in, ships, i), execOf
		case "review":
			ship, pick = dashboardReviewShip(in, ships, i), reviewOf
		default:
			continue
		}
		if ship < 0 {
			continue
		}
		if cur, ok := pick[ship]; !ok || dashboardNewer(in[i], in[cur]) {
			pick[ship] = i
		}
	}

	joined := map[int]bool{}
	for _, s := range ships {
		if r, ok := reviewOf[s]; ok {
			dashboardNestReview(&in[s], in[r])
			joined[r] = true
		}
		if e, ok := execOf[s]; ok {
			dashboardNestExecute(&in[s], in[e])
			joined[e] = true
		}
	}

	out := make([]DashboardPipeline, 0, len(in)-len(joined))
	for i := range in {
		if !joined[i] {
			out = append(out, in[i])
		}
	}
	return out
}

// dashboardExecuteShip returns the index in ps of the ship that the execute
// run ps[i] joins, or -1. The ship has the same branch slug and its run
// window holds the execute start; of several such ships, the newest wins.
func dashboardExecuteShip(ps []DashboardPipeline, ships []int, i int) int {
	slug := state.SlugifyBranch(ps[i].Branch)
	start := dashboardPipelineStart(ps[i])
	if slug == "" || start.IsZero() {
		return -1
	}
	best := -1
	for _, s := range ships {
		if state.SlugifyBranch(ps[s].Branch) != slug || !dashboardShipWindowHolds(ps[s], start) {
			continue
		}
		if best < 0 || dashboardPipelineStart(ps[s]).After(dashboardPipelineStart(ps[best])) {
			best = s
		}
	}
	return best
}

// dashboardReviewShip returns the index in ps of the ship that the review
// run ps[i] joins, or -1. A review with a shipRunId joins only the ship with
// that id. A review without one joins the newest ship of the same branch slug
// whose review step window holds the review start. A review with no run.meta
// has no branch and no start, so it joins no ship.
func dashboardReviewShip(ps []DashboardPipeline, ships []int, i int) int {
	r := ps[i]
	if r.join.shipRunID != "" {
		for _, s := range ships {
			if ps[s].ID == r.join.shipRunID {
				return s
			}
		}
		return -1
	}
	slug := state.SlugifyBranch(r.Branch)
	if slug == "" || r.join.startedAt.IsZero() {
		return -1
	}
	best := -1
	for _, s := range ships {
		if state.SlugifyBranch(ps[s].Branch) != slug {
			continue
		}
		w, ok := ps[s].join.stepWindows[dashboardShipStepReview]
		if !ok || !dashboardWindowHolds(w[0], w[1], r.join.startedAt) {
			continue
		}
		if best < 0 || dashboardPipelineStart(ps[s]).After(dashboardPipelineStart(ps[best])) {
			best = s
		}
	}
	return best
}

// dashboardPipelineStart returns the join start time of p: p.join.startedAt,
// else p.StartedAt parsed, else the zero time.
func dashboardPipelineStart(p DashboardPipeline) time.Time {
	if !p.join.startedAt.IsZero() {
		return p.join.startedAt
	}
	t, _ := dashboardParseTime(p.StartedAt)
	return t
}

// dashboardShipWindowHolds reports whether t is inside the run window of the
// ship pipeline p: from its start to its completedAt. With no completedAt,
// the window stays open.
func dashboardShipWindowHolds(p DashboardPipeline, t time.Time) bool {
	var end time.Time
	if p.CompletedAt != nil {
		end, _ = dashboardParseTime(*p.CompletedAt)
	}
	return dashboardWindowHolds(dashboardPipelineStart(p), end, t)
}

// dashboardWindowHolds reports whether t is in [start, end], both ends
// included. A zero start or a zero t never holds; a zero end leaves the
// window open.
func dashboardWindowHolds(start, end, t time.Time) bool {
	if start.IsZero() || t.IsZero() || t.Before(start) {
		return false
	}
	return end.IsZero() || !t.After(end)
}

// dashboardNewer reports whether pipeline a started after pipeline b. Equal
// start times compare by id, so the result does not depend on list order.
func dashboardNewer(a, b DashboardPipeline) bool {
	ta, tb := dashboardPipelineStart(a), dashboardPipelineStart(b)
	if !ta.Equal(tb) {
		return ta.After(tb)
	}
	return a.ID > b.ID
}

// dashboardStepNamed returns the first step of p named name, or nil.
func dashboardStepNamed(p *DashboardPipeline, name string) *DashboardStep {
	for i := range p.Steps {
		if p.Steps[i].Name == name {
			return &p.Steps[i]
		}
	}
	return nil
}

// dashboardNestExecute copies the joined execute run exec into ship: its
// full wave detail goes to the ship execute step, its CommitWaves to the
// ship, and its issues to the ship issues with dashboardExecuteRefPrefix
// before each Ref. The ship keeps its own session id.
func dashboardNestExecute(ship *DashboardPipeline, exec DashboardPipeline) {
	if step := dashboardStepNamed(ship, dashboardShipStepExecute); step != nil && exec.join.execDetail != nil {
		step.Detail = exec.join.execDetail
	}
	if exec.CommitWaves != nil {
		ship.CommitWaves = exec.CommitWaves
	}
	for _, is := range exec.Issues {
		is.Ref = dashboardExecuteRefPrefix + is.Ref
		ship.Issues = append(ship.Issues, is)
	}
}

// dashboardNestReview copies the joined review run review into ship: the
// review rows that readReviewLedgerDir built (review.join.reviewDims) become
// the dimensions of the ship review step, with their wave, stop reason,
// finding count, and worst severity. Each dimension also gets its finding
// rows (FindingItems): the review issues whose Ref is the dimension name, in
// issue order, the same rows the standalone review lists; a dimension with no
// finding gets an empty, non-nil list. The plan totals go to the step
// reviewPlan; with no planned dimension it stays absent. The review issues go
// to the ship issues with their Ref unchanged. A review with no rows gives an
// empty, non-nil dimension list.
func dashboardNestReview(ship *DashboardPipeline, review DashboardPipeline) {
	dims := make([]DashboardDimension, 0, len(review.join.reviewDims))
	dims = append(dims, review.join.reviewDims...)
	for i := range dims {
		dims[i].FindingItems = dashboardDimensionFindings(review.Issues, dims[i].Name)
	}
	if step := dashboardStepNamed(ship, dashboardShipStepReview); step != nil {
		if step.Detail == nil {
			step.Detail = &DashboardStepDetail{Kind: dashboardKindDimensions}
		}
		step.Detail.Dimensions = dims
		step.Detail.ReviewPlan = review.join.reviewPlan
	}
	ship.Issues = append(ship.Issues, review.Issues...)
}

// dashboardDimensionFindings returns the finding rows of the dimension name:
// the review issues whose Ref is name, in issue order. It builds the rows as
// dashboardReviewFindings does for the standalone review. It returns an
// empty, non-nil list when the dimension has no finding.
func dashboardDimensionFindings(issues []DashboardIssue, name string) []DashboardReviewFinding {
	rows := []DashboardReviewFinding{}
	for _, is := range issues {
		if is.Source == dashboardSourceReview && is.Ref == name {
			rows = append(rows, DashboardReviewFinding{
				Text: is.Text, Severity: is.Severity, File: is.File, Line: is.Line,
			})
		}
	}
	return rows
}
