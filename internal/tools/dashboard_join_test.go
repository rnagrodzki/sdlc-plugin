package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// Fixed names and times of the join tests. The ship runs from 08:00; its
// review step starts at 08:30.
const (
	dashJoinShipFile = "ship-feat-x-20261007T080000Z.json"
	dashJoinShipID   = "ship-feat-x-20261007T080000Z"
	dashJoinReview   = "review-2026-10-07T08-31-00Z"
)

// dashJoinFresh is the modification time of every join test file: recent
// enough that no pipeline is stalled or out of the history window.
var dashJoinFresh = dashNow.Add(-time.Minute)

// dashJoinShipData returns a running ship state on feat/x: execute and
// commit completed, review in progress since 08:30.
func dashJoinShipData() map[string]any {
	return map[string]any{
		"branch":    "feat/x",
		"startedAt": "2026-10-07T08:00:00Z",
		"sessionId": "s-ship",
		"steps": []any{
			map[string]any{"name": "execute", "status": StepCompleted, "startedAt": "2026-10-07T08:00:00Z", "completedAt": "2026-10-07T08:20:00Z"},
			map[string]any{"name": "commit", "status": StepCompleted, "startedAt": "2026-10-07T08:20:00Z", "completedAt": "2026-10-07T08:21:00Z"},
			map[string]any{"name": "review", "status": StepInProgress, "startedAt": "2026-10-07T08:30:00Z"},
		},
	}
}

// dashJoinExecData returns an execute state on branch that started at
// startedAt, with one completed wave of sha and one failed task "4".
func dashJoinExecData(branch, startedAt, sha string) map[string]any {
	return map[string]any{
		"branch":      branch,
		"startedAt":   startedAt,
		"commitWaves": false,
		"waves": []any{map[string]any{
			"number": 1, "status": StepCompleted, "committedSha": sha,
			"tasks": []any{map[string]any{"id": "4", "status": StepFailed, "error": "go vet failed"}},
		}},
	}
}

// dashJoinExecFile returns the execute state file name of a run on feat/x
// with the given filename timestamp.
func dashJoinExecFile(ts string) string {
	return "execute-feat-x-" + ts + ".json"
}

// dashJoinRunMeta writes run.meta of review ledger folder run.
func dashJoinRunMeta(t *testing.T, root, run string, meta reviewRunMeta) {
	t.Helper()
	dashWriteJSON(t, filepath.Join(root, paths.DataDir, paths.RunsSubdir, "ledger", run, ledgerRunMetaFile), meta, dashJoinFresh)
}

// dashJoinReviewDims writes a completed "security" dimension with three
// findings (medium, high, low) and an in-progress "docs" dimension with no
// finding into review ledger folder run.
func dashJoinReviewDims(t *testing.T, root, run string) {
	t.Helper()
	dashWriteReviewDim(t, root, run, "security", map[string]any{
		"checkinAt": "2026-10-07T08:31:00Z", "checkoutAt": "2026-10-07T08:35:00Z",
		"findings": dashLedgerFindings(t,
			map[string]any{"severity": "medium", "file": "internal/a.go", "line": 1, "rationale": "a"},
			map[string]any{"severity": "high", "file": "internal/x.go", "line": 42, "rationale": "token check skips expiry"},
			map[string]any{"severity": "low", "file": "internal/b.go", "rationale": "b"},
		),
	}, dashJoinFresh)
	dashWriteReviewDim(t, root, run, "docs", map[string]any{"checkinAt": "2026-10-07T08:31:00Z"}, dashJoinFresh)
}

// dashJoinKinds returns "kind:id" of each pipeline, in list order.
func dashJoinKinds(ps []DashboardPipeline) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Kind+":"+p.ID)
	}
	return out
}

// dashJoinFind returns the pipeline with id, or fails the test.
func dashJoinFind(t *testing.T, ps []DashboardPipeline, id string) DashboardPipeline {
	t.Helper()
	for _, p := range ps {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("no pipeline %q in %v", id, dashJoinKinds(ps))
	return DashboardPipeline{}
}

// dashJoinStep returns the step of p named name, or fails the test.
func dashJoinStep(t *testing.T, p DashboardPipeline, name string) DashboardStep {
	t.Helper()
	for _, s := range p.Steps {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no step %q in %+v", name, p.Steps)
	return DashboardStep{}
}

// dashJoinIssueRefs returns "source:ref" of each issue of p.
func dashJoinIssueRefs(p DashboardPipeline) []string {
	out := []string{}
	for _, is := range p.Issues {
		out = append(out, is.Source+":"+is.Ref)
	}
	return out
}

// TestDashboardJoin_ExecuteNests checks the rule row: same branch slug,
// execute start inside the ship window, so the execute nests into the ship.
func TestDashboardJoin_ExecuteNests(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)
	dashWriteState(t, root, dashJoinExecFile("20261007T080100Z"), dashJoinExecData("feat/x", "2026-10-07T08:01:00Z", "a1b2c3d"), dashJoinFresh)

	ship := dashOne(t, root)
	if ship.Kind != "ship" {
		t.Fatalf("kind = %q, want ship", ship.Kind)
	}
	d := dashJoinStep(t, ship, "execute").Detail
	if d == nil || d.Kind != dashboardKindWaves || len(d.Waves) != 1 || d.Waves[0].CommittedSHA != "a1b2c3d" {
		t.Errorf("execute detail = %+v, want the execute run waves", d)
	}
	if ship.CommitWaves == nil || *ship.CommitWaves {
		t.Errorf("commitWaves = %v, want false from the execute run", ship.CommitWaves)
	}
	if ship.SessionID != "s-ship" {
		t.Errorf("sessionId = %q, want the ship id for an execute run without one", ship.SessionID)
	}
	if got, want := dashJoinIssueRefs(ship), []string{"task:execute:4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("issues = %v, want %v", got, want)
	}
	if ship.Issues[0].Text != "go vet failed" || ship.Issues[0].Severity != "high" {
		t.Errorf("issue = %+v, want the execute task issue", ship.Issues[0])
	}
}

// TestDashboardJoin_TwoExecutesNewestNests checks the rule row: two execute
// runs match one ship, the newest nests and the other stays a row.
func TestDashboardJoin_TwoExecutesNewestNests(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)
	dashWriteState(t, root, dashJoinExecFile("20261007T080100Z"), dashJoinExecData("feat/x", "2026-10-07T08:01:00Z", "old"), dashJoinFresh)
	dashWriteState(t, root, dashJoinExecFile("20261007T081000Z"), dashJoinExecData("feat/x", "2026-10-07T08:10:00Z", "new"), dashJoinFresh)

	repo := dashCollect(t, root)
	if len(repo.Pipelines) != 2 {
		t.Fatalf("pipelines = %v, want the ship and the older execute", dashJoinKinds(repo.Pipelines))
	}
	dashJoinFind(t, repo.Pipelines, "execute-feat-x-20261007T080100Z")
	ship := dashJoinFind(t, repo.Pipelines, dashJoinShipID)
	if d := dashJoinStep(t, ship, "execute").Detail; d == nil || len(d.Waves) != 1 || d.Waves[0].CommittedSHA != "new" {
		t.Errorf("execute detail = %+v, want the newest execute run", d)
	}
}

// TestDashboardJoin_ExecuteOutsideWindow checks the rule row: an execute
// start outside the ship window keeps the execute as a row.
func TestDashboardJoin_ExecuteOutsideWindow(t *testing.T) {
	cases := []struct {
		name, file, startedAt string
	}{
		{"after the ship completed", dashJoinExecFile("20261007T090000Z"), "2026-10-07T09:00:00Z"},
		{"before the ship started", dashJoinExecFile("20261007T070000Z"), "2026-10-07T07:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := dashRoot(t)
			ship := dashJoinShipData()
			ship["pipelineStatus"] = "completed"
			ship["pipelineCompletedAt"] = "2026-10-07T08:45:00Z"
			dashWriteState(t, root, dashJoinShipFile, ship, dashJoinFresh)
			dashWriteState(t, root, tc.file, dashJoinExecData("feat/x", tc.startedAt, "a1b2c3d"), dashJoinFresh)

			repo := dashCollect(t, root)
			if len(repo.Pipelines) != 2 {
				t.Fatalf("pipelines = %v, want both rows", dashJoinKinds(repo.Pipelines))
			}
			got := dashJoinFind(t, repo.Pipelines, dashJoinShipID)
			if d := dashJoinStep(t, got, "execute").Detail; d != nil {
				t.Errorf("ship execute detail = %+v, want none", d)
			}
			if got.CommitWaves != nil {
				t.Errorf("ship commitWaves = %v, want absent", *got.CommitWaves)
			}
		})
	}
}

// TestDashboardJoin_ExecuteNoShip checks the rule row: an execute run with no
// ship on its branch stays a row.
func TestDashboardJoin_ExecuteNoShip(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)
	dashWriteState(t, root, "execute-feat-y-20261007T080100Z.json", dashJoinExecData("feat/y", "2026-10-07T08:01:00Z", "a1b2c3d"), dashJoinFresh)

	repo := dashCollect(t, root)
	if len(repo.Pipelines) != 2 {
		t.Fatalf("pipelines = %v, want both rows", dashJoinKinds(repo.Pipelines))
	}
	exec := dashJoinFind(t, repo.Pipelines, "execute-feat-y-20261007T080100Z")
	if exec.Branch != "feat/y" {
		t.Errorf("execute branch = %q, want feat/y", exec.Branch)
	}
}

// TestDashboardJoin_ReviewByShipRunID checks the rule row: run.meta shipRunId
// equal to the ship run id nests the review, even on another branch and
// outside the review step window. It also checks the dimension table and that
// a review with N findings gives the ship N review issues.
func TestDashboardJoin_ReviewByShipRunID(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)
	dashJoinReviewDims(t, root, dashJoinReview)
	dashJoinRunMeta(t, root, dashJoinReview, reviewRunMeta{Branch: "other", StartedAt: "2026-10-07T07:00:00Z", ShipRunID: dashJoinShipID})

	ship := dashOne(t, root)
	d := dashJoinStep(t, ship, "review").Detail
	if d == nil || d.Kind != dashboardKindDimensions {
		t.Fatalf("review detail = %+v, want kind %q", d, dashboardKindDimensions)
	}
	want := []DashboardDimension{
		{Name: "docs", Status: StepInProgress, Findings: 0, Worst: "", FindingItems: []DashboardReviewFinding{}},
		{Name: "security", Status: StepCompleted, Findings: 3, Worst: "high", FindingItems: []DashboardReviewFinding{
			{Text: "a", Severity: "medium", File: "internal/a.go", Line: "1"},
			{Text: "token check skips expiry", Severity: "high", File: "internal/x.go", Line: "42"},
			{Text: "b", Severity: "low", File: "internal/b.go", Line: ""},
		}},
	}
	if !reflect.DeepEqual(d.Dimensions, want) {
		t.Errorf("dimensions = %+v, want %+v", d.Dimensions, want)
	}
	if d.ReviewTotals != nil {
		t.Errorf("reviewTotals = %+v, want absent with no ledger", d.ReviewTotals)
	}

	review := 0
	for _, is := range ship.Issues {
		if is.Source == dashboardSourceReview {
			review++
			if is.Ref != "security" {
				t.Errorf("review issue ref = %q, want the dimension name", is.Ref)
			}
		}
	}
	if review != 3 {
		t.Errorf("ship review issues = %d, want 3: %+v", review, ship.Issues)
	}
}

// TestDashboardJoin_ShipFindingItemsEqualStandalone checks that each dimension
// of the ship review step lists the same finding rows as the same dimension
// of the standalone review, and that a dimension with no finding lists an
// empty, non-nil list that encodes as [].
func TestDashboardJoin_ShipFindingItemsEqualStandalone(t *testing.T) {
	joined := dashRoot(t)
	dashWriteState(t, joined, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)
	dashJoinReviewDims(t, joined, dashJoinReview)
	dashJoinRunMeta(t, joined, dashJoinReview, reviewRunMeta{Branch: "feat/x", StartedAt: "2026-10-07T08:31:00Z", ShipRunID: dashJoinShipID})

	alone := dashRoot(t)
	dashJoinReviewDims(t, alone, dashJoinReview)

	d := dashJoinStep(t, dashOne(t, joined), "review").Detail
	if d == nil || len(d.Dimensions) != 2 {
		t.Fatalf("ship review detail = %+v, want two dimensions", d)
	}
	standalone := dashOne(t, alone)

	for _, dim := range d.Dimensions {
		if dim.FindingItems == nil {
			t.Errorf("dimension %q findingItems = nil, want a non-nil list", dim.Name)
		}
		if len(dim.FindingItems) != dim.Findings {
			t.Errorf("dimension %q lists %d rows, want its %d findings", dim.Name, len(dim.FindingItems), dim.Findings)
		}
		// A standalone step with no finding detail lists no row.
		want := []DashboardReviewFinding{}
		if sd := dashJoinStep(t, standalone, dim.Name).Detail; sd != nil {
			if sd.Kind != dashboardKindFindings {
				t.Fatalf("standalone %q detail kind = %q, want %q", dim.Name, sd.Kind, dashboardKindFindings)
			}
			want = append(want, sd.Findings...)
		}
		if !reflect.DeepEqual(dim.FindingItems, want) {
			t.Errorf("dimension %q rows = %+v, want the standalone rows %+v", dim.Name, dim.FindingItems, want)
		}
	}

	b, err := json.Marshal(d.Dimensions[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); !strings.Contains(got, `"findingItems":[]`) {
		t.Errorf("dimension with no finding encodes as %s, want findingItems:[]", got)
	}
}

// TestDashboardJoin_ReviewByBranchWindow checks the rule row: no shipRunId,
// same branch, review start inside the ship review step window nests the
// review; a start before that window keeps it a row.
func TestDashboardJoin_ReviewByBranchWindow(t *testing.T) {
	t.Run("inside the review step window nests", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)
		dashJoinReviewDims(t, root, dashJoinReview)
		dashJoinRunMeta(t, root, dashJoinReview, reviewRunMeta{Branch: "feat/x", StartedAt: "2026-10-07T08:31:00Z"})

		ship := dashOne(t, root)
		if d := dashJoinStep(t, ship, "review").Detail; d == nil || len(d.Dimensions) != 2 {
			t.Errorf("review detail = %+v, want two dimensions", d)
		}
	})

	t.Run("before the review step window stays a row", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)
		dashJoinReviewDims(t, root, dashJoinReview)
		dashJoinRunMeta(t, root, dashJoinReview, reviewRunMeta{Branch: "feat/x", StartedAt: "2026-10-07T08:10:00Z"})

		repo := dashCollect(t, root)
		if len(repo.Pipelines) != 2 {
			t.Fatalf("pipelines = %v, want both rows", dashJoinKinds(repo.Pipelines))
		}
		review := dashJoinFind(t, repo.Pipelines, dashJoinReview)
		if review.Branch != "feat/x" {
			t.Errorf("review branch = %q, want the run.meta branch", review.Branch)
		}
	})
}

// TestDashboardJoin_TwoReviewsNewestNests checks the rule row: two review
// runs match one ship, the newest nests and the other stays a row.
func TestDashboardJoin_TwoReviewsNewestNests(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)
	older, newer := "review-2026-10-07T08-31-00Z", "review-2026-10-07T08-40-00Z"
	dashJoinReviewDims(t, root, older)
	dashJoinRunMeta(t, root, older, reviewRunMeta{Branch: "feat/x", StartedAt: "2026-10-07T08:31:00Z", ShipRunID: dashJoinShipID})
	dashWriteReviewDim(t, root, newer, "perf", map[string]any{"checkinAt": "2026-10-07T08:40:00Z"}, dashJoinFresh)
	dashJoinRunMeta(t, root, newer, reviewRunMeta{Branch: "feat/x", StartedAt: "2026-10-07T08:40:00Z"})

	repo := dashCollect(t, root)
	if len(repo.Pipelines) != 2 {
		t.Fatalf("pipelines = %v, want the ship and the older review", dashJoinKinds(repo.Pipelines))
	}
	dashJoinFind(t, repo.Pipelines, older)
	ship := dashJoinFind(t, repo.Pipelines, dashJoinShipID)
	want := []DashboardDimension{{Name: "perf", Status: StepInProgress, FindingItems: []DashboardReviewFinding{}}}
	if d := dashJoinStep(t, ship, "review").Detail; d == nil || !reflect.DeepEqual(d.Dimensions, want) {
		t.Errorf("review detail = %+v, want the newest review run", d)
	}
}

// TestDashboardJoin_ReviewWithoutMeta checks the rule row: a review folder
// with no run.meta, or a run.meta that does not parse, stays a row.
func TestDashboardJoin_ReviewWithoutMeta(t *testing.T) {
	cases := []struct {
		name string
		meta []byte // nil: no run.meta file
	}{
		{"no run.meta", nil},
		{"run.meta does not parse", []byte("{not json")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := dashRoot(t)
			dashWriteState(t, root, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)
			dashJoinReviewDims(t, root, dashJoinReview)
			if tc.meta != nil {
				path := filepath.Join(root, paths.DataDir, paths.RunsSubdir, "ledger", dashJoinReview, ledgerRunMetaFile)
				if err := os.WriteFile(path, tc.meta, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			repo := dashCollect(t, root)
			if repo.Error != "" {
				t.Errorf("repo error = %q, want none", repo.Error)
			}
			if len(repo.Pipelines) != 2 {
				t.Fatalf("pipelines = %v, want both rows", dashJoinKinds(repo.Pipelines))
			}
			review := dashJoinFind(t, repo.Pipelines, dashJoinReview)
			if review.Branch != "" {
				t.Errorf("review branch = %q, want none", review.Branch)
			}
			ship := dashJoinFind(t, repo.Pipelines, dashJoinShipID)
			if d := dashJoinStep(t, ship, "review").Detail; d != nil {
				t.Errorf("ship review detail = %+v, want none", d)
			}
		})
	}
}

// TestDashboardJoin_PlanStation checks the plan station: a summary gives a
// first completed plan step with the explorers and one more done and total
// step; an empty list gives the station with no explorers; no key gives no
// station.
func TestDashboardJoin_PlanStation(t *testing.T) {
	entries := []ExploreSummaryEntry{{
		Name: "auth-flow", Status: "done", Total: 12,
		Top: []ExploreSummaryItem{{Summary: "token parsed twice", Ref: "internal/auth.go:10"}},
	}}

	t.Run("summary gives the plan station", func(t *testing.T) {
		root := dashRoot(t)
		data := dashJoinShipData()
		data["planExploreSummary"] = entries
		dashWriteState(t, root, dashJoinShipFile, data, dashJoinFresh)

		ship := dashOne(t, root)
		if len(ship.Steps) != 4 || ship.Steps[0].Name != "plan" || ship.Steps[0].Status != StepCompleted {
			t.Fatalf("steps = %+v, want a first completed plan step", ship.Steps)
		}
		want := &DashboardStepDetail{Kind: dashboardKindExplorers, Explorers: dashboardExplorers(entries)}
		if !reflect.DeepEqual(ship.Steps[0].Detail, want) {
			t.Errorf("plan detail = %+v, want %+v", ship.Steps[0].Detail, want)
		}
		wantProgress := DashboardProgress{Done: 3, Total: 4, Current: "review", Label: "step 4 of 4"}
		if ship.Progress != wantProgress {
			t.Errorf("progress = %+v, want %+v", ship.Progress, wantProgress)
		}
	})

	t.Run("empty summary gives the station with no explorers", func(t *testing.T) {
		root := dashRoot(t)
		data := dashJoinShipData()
		data["planExploreSummary"] = []any{}
		dashWriteState(t, root, dashJoinShipFile, data, dashJoinFresh)

		ship := dashOne(t, root)
		d := ship.Steps[0].Detail
		if ship.Steps[0].Name != "plan" || d == nil || d.Kind != dashboardKindExplorers || len(d.Explorers) != 0 {
			t.Fatalf("first step = %+v, want an empty plan station", ship.Steps[0])
		}
		b, err := json.Marshal(ship.Steps[0])
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"name":"plan","status":"completed","detail":{"kind":"explorers"}}`; string(b) != want {
			t.Errorf("plan step JSON = %s, want %s", b, want)
		}
		if ship.Progress.Done != 3 || ship.Progress.Total != 4 {
			t.Errorf("progress = %+v, want done 3 of 4", ship.Progress)
		}
	})

	t.Run("stored rounds join the explorers", func(t *testing.T) {
		root := dashRoot(t)
		data := dashJoinShipData()
		data["planExploreSummary"] = entries
		data["planReviewRounds"] = []any{
			map[string]any{
				"round": 1, "mergedStatus": planStatusIssuesFound, "found": 3, "fixed": 2,
				"lenses": []any{map[string]any{"name": "structure", "verdict": planStatusIssuesFound}},
			},
			map[string]any{"round": 2, "mergedStatus": planStatusApproved, "found": 0, "fixed": 0, "lenses": []any{}},
		}
		dashWriteState(t, root, dashJoinShipFile, data, dashJoinFresh)

		ship := dashOne(t, root)
		if ship.Steps[0].Name != "plan" {
			t.Fatalf("steps = %+v, want a first plan step", ship.Steps)
		}
		want := &DashboardStepDetail{
			Kind:      dashboardKindExplorers,
			Explorers: dashboardExplorers(entries),
			Rounds: []DashboardRound{
				{N: 1, Status: planStatusIssuesFound, Found: 3, Fixed: 2, Lenses: []DashboardLens{{Name: "structure", Verdict: planStatusIssuesFound}}},
				{N: 2, Status: planStatusApproved, Lenses: []DashboardLens{}},
			},
			MaxRounds: maxReviewRounds,
		}
		if !reflect.DeepEqual(ship.Steps[0].Detail, want) {
			t.Errorf("plan detail = %+v, want %+v", ship.Steps[0].Detail, want)
		}
	})

	t.Run("empty or undecodable rounds show explorers only", func(t *testing.T) {
		cases := map[string]any{
			"empty list": []any{},
			"not a list": "round 1",
		}
		for name, rounds := range cases {
			t.Run(name, func(t *testing.T) {
				root := dashRoot(t)
				data := dashJoinShipData()
				data["planExploreSummary"] = entries
				data["planReviewRounds"] = rounds
				dashWriteState(t, root, dashJoinShipFile, data, dashJoinFresh)

				ship := dashOne(t, root)
				want := &DashboardStepDetail{Kind: dashboardKindExplorers, Explorers: dashboardExplorers(entries)}
				if !reflect.DeepEqual(ship.Steps[0].Detail, want) {
					t.Errorf("plan detail = %+v, want %+v", ship.Steps[0].Detail, want)
				}
			})
		}
	})

	t.Run("no key gives no station", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)

		ship := dashOne(t, root)
		if len(ship.Steps) != 3 || ship.Steps[0].Name != "execute" {
			t.Errorf("steps = %+v, want no plan station", ship.Steps)
		}
		wantProgress := DashboardProgress{Done: 2, Total: 3, Current: "review", Label: "step 3 of 3"}
		if ship.Progress != wantProgress {
			t.Errorf("progress = %+v, want %+v", ship.Progress, wantProgress)
		}
	})
}

// TestDashboardJoin_ReviewTotals checks the review totals of the ship review
// step: they come from shipBuildReviewLedger, Dimensions is [] with no joined
// review, and a nil ledger gives no detail.
func TestDashboardJoin_ReviewTotals(t *testing.T) {
	t.Run("ledger gives totals and an empty dimension list", func(t *testing.T) {
		root := dashRoot(t)
		data := dashJoinShipData()
		data["healing"] = map[string]any{
			"reviewTotal": map[string]any{"total": 5},
			"fixed": []any{
				map[string]any{"origin": "local-review"},
				map[string]any{"origin": "pr-comment"},
			},
		}
		data["deferredFindings"] = []any{
			map[string]any{"reason": "below-threshold"},
			map[string]any{"reason": "out-of-scope"},
		}
		dashWriteState(t, root, dashJoinShipFile, data, dashJoinFresh)

		ship := dashOne(t, root)
		d := dashJoinStep(t, ship, "review").Detail
		if d == nil || d.Kind != dashboardKindDimensions {
			t.Fatalf("review detail = %+v, want kind %q", d, dashboardKindDimensions)
		}
		want := &DashboardReviewTotals{Found: 5, Fixed: 1, Deferred: 2, Unaccounted: 2}
		if !reflect.DeepEqual(d.ReviewTotals, want) {
			t.Errorf("reviewTotals = %+v, want %+v", d.ReviewTotals, want)
		}
		if d.Dimensions == nil || len(d.Dimensions) != 0 {
			t.Errorf("dimensions = %#v, want an empty non-nil list", d.Dimensions)
		}
	})

	t.Run("no ledger gives no detail", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)

		ship := dashOne(t, root)
		if d := dashJoinStep(t, ship, "review").Detail; d != nil {
			t.Errorf("review detail = %+v, want none", d)
		}
	})
}

// TestDashboardJoin_SessionID checks that the ship session id comes from
// data.sessionId and stays "" when the state has none.
func TestDashboardJoin_SessionID(t *testing.T) {
	root := dashRoot(t)
	data := dashJoinShipData()
	delete(data, "sessionId")
	dashWriteState(t, root, dashJoinShipFile, data, dashJoinFresh)
	if got := dashOne(t, root).SessionID; got != "" {
		t.Errorf("sessionId = %q, want empty", got)
	}
}

// TestDashboardJoin_KeepsOrder checks that dashboardJoinRuns drops the
// joined runs and keeps the order of the rest, and that a joined review with
// no steps gives an empty non-nil Dimensions list.
func TestDashboardJoin_KeepsOrder(t *testing.T) {
	start := time.Date(2026, 10, 7, 8, 0, 0, 0, time.UTC)
	ship := DashboardPipeline{
		ID: dashJoinShipID, Kind: "ship", Branch: "feat/x",
		Steps:  []DashboardStep{{Name: "execute"}, {Name: "review"}},
		Issues: []DashboardIssue{},
	}
	ship.join.startedAt = start
	ship.join.stepWindows = map[string][2]time.Time{"review": {start.Add(30 * time.Minute), {}}}

	execX := DashboardPipeline{ID: "execute-feat-x", Kind: "execute", Branch: "feat-x"} // a slug matches its branch
	execX.join.startedAt = start.Add(time.Minute)
	execX.join.execDetail = &DashboardStepDetail{Kind: dashboardKindWaves, Waves: []DashboardWave{}}
	execY := DashboardPipeline{ID: "execute-feat-y", Kind: "execute", Branch: "feat/y"}
	execY.join.startedAt = start.Add(time.Minute)
	review := DashboardPipeline{ID: dashJoinReview, Kind: "review", Steps: []DashboardStep{}}
	review.join.shipRunID = dashJoinShipID
	plan := DashboardPipeline{ID: "plan-feat-x", Kind: "plan", Branch: "feat/x"}

	got := dashboardJoinRuns([]DashboardPipeline{execY, ship, execX, review, plan})
	if want := []string{"execute:execute-feat-y", "ship:" + dashJoinShipID, "plan:plan-feat-x"}; !reflect.DeepEqual(dashJoinKinds(got), want) {
		t.Fatalf("pipelines = %v, want %v", dashJoinKinds(got), want)
	}
	if d := got[1].Steps[0].Detail; d != execX.join.execDetail {
		t.Errorf("execute detail = %+v, want the execute run detail", d)
	}
	d := got[1].Steps[1].Detail
	if d == nil || d.Kind != dashboardKindDimensions || d.Dimensions == nil || len(d.Dimensions) != 0 {
		t.Errorf("review detail = %+v, want kind %q and an empty non-nil list", d, dashboardKindDimensions)
	}
}

// dashRRFix returns one data.healing.fixProgress record, as decoded JSON:
// line is a float64.
func dashRRFix(title, status, firstAt, updatedAt string) map[string]any {
	return map[string]any{
		"origin": "local-review", "severity": "high", "file": "internal/x.go", "line": float64(42),
		"title": title, "status": status, "firstAt": firstAt, "updatedAt": updatedAt,
	}
}

// dashRRShip writes a ship state built from dashJoinShipData, with fixes as
// data.healing.fixProgress (no key when fixes is nil), then returns its
// pipeline.
func dashRRShip(t *testing.T, fixes any, edit func(map[string]any)) DashboardPipeline {
	t.Helper()
	root := dashRoot(t)
	data := dashJoinShipData()
	if fixes != nil {
		data["healing"] = map[string]any{"fixProgress": fixes}
	}
	if edit != nil {
		edit(data)
	}
	dashWriteState(t, root, dashJoinShipFile, data, dashJoinFresh)
	return dashOne(t, root)
}

// dashRRNames returns the step names of p, in order.
func dashRRNames(p DashboardPipeline) []string {
	out := []string{}
	for _, s := range p.Steps {
		out = append(out, s.Name)
	}
	return out
}

// dashRRStepJSON returns the JSON of the step of p named name.
func dashRRStepJSON(t *testing.T, p DashboardPipeline, name string) string {
	t.Helper()
	b, err := json.Marshal(dashJoinStep(t, p, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestDashboardReceivedReview_StepStatus checks the step status table: no
// list gives no step; an open fix in a live run gives in_progress with no
// completedAt; all fixes final, or a completed run, give completed with the
// earliest firstAt and the latest updatedAt.
func TestDashboardReceivedReview_StepStatus(t *testing.T) {
	t.Run("no list gives no step", func(t *testing.T) {
		for name, fixes := range map[string]any{"absent": nil, "not a list": "fix 1"} {
			t.Run(name, func(t *testing.T) {
				ship := dashRRShip(t, fixes, nil)
				if got, want := dashRRNames(ship), []string{"execute", "commit", "review"}; !reflect.DeepEqual(got, want) {
					t.Errorf("steps = %v, want %v", got, want)
				}
			})
		}
	})

	t.Run("open fix in a live run is in_progress", func(t *testing.T) {
		ship := dashRRShip(t, []any{
			dashRRFix("a", "fixed", "2026-10-07T08:41:00Z", "2026-10-07T08:45:00Z"),
			dashRRFix("b", "fixing", "2026-10-07T08:40:00Z", "2026-10-07T08:50:00Z"),
		}, nil)
		s := dashJoinStep(t, ship, "received-review")
		if s.Status != StepInProgress || s.StartedAt != "2026-10-07T08:40:00Z" || s.CompletedAt != "" {
			t.Errorf("step = %+v, want in_progress from 08:40 with no completedAt", s)
		}
		if strings.Contains(dashRRStepJSON(t, ship, "received-review"), "completedAt") {
			t.Errorf("step JSON has a completedAt key")
		}
	})

	t.Run("all fixes final is completed", func(t *testing.T) {
		ship := dashRRShip(t, []any{
			dashRRFix("a", "fixed", "2026-10-07T08:41:00Z", "2026-10-07T08:55:00Z"),
			dashRRFix("b", "failed", "2026-10-07T08:40:00Z", "2026-10-07T08:50:00Z"),
			dashRRFix("c", "deferred", "2026-10-07T08:42:00Z", "2026-10-07T08:43:00Z"),
		}, nil)
		s := dashJoinStep(t, ship, "received-review")
		if s.Status != StepCompleted || s.StartedAt != "2026-10-07T08:40:00Z" || s.CompletedAt != "2026-10-07T08:55:00Z" {
			t.Errorf("step = %+v, want completed 08:40 to 08:55", s)
		}
	})

	t.Run("open fix in a completed run is completed", func(t *testing.T) {
		ship := dashRRShip(t, []any{
			dashRRFix("a", "queued", "2026-10-07T08:40:00Z", "2026-10-07T08:40:00Z"),
			dashRRFix("b", "fixing", "2026-10-07T08:41:00Z", "2026-10-07T08:52:00Z"),
		}, func(d map[string]any) { d["pipelineCompletedAt"] = "2026-10-07T09:00:00Z" })
		s := dashJoinStep(t, ship, "received-review")
		if s.Status != StepCompleted || s.StartedAt != "2026-10-07T08:40:00Z" || s.CompletedAt != "2026-10-07T08:52:00Z" {
			t.Errorf("step = %+v, want completed 08:40 to 08:52", s)
		}
		// A stopped fix pass leaves its rows as they were: the station is
		// completed, a queued or fixing row keeps its last status.
		var statuses []string
		for _, f := range s.Detail.Fixes {
			statuses = append(statuses, f.Status)
		}
		if want := []string{"queued", "fixing"}; !reflect.DeepEqual(statuses, want) {
			t.Errorf("row statuses = %v, want %v", statuses, want)
		}
	})
}

// TestDashboardReceivedReview_Records checks the record table: bad records
// are skipped, line 0 gives no line key, every stored record is read, a bad
// time is skipped alone, and valid records keep their list order.
func TestDashboardReceivedReview_Records(t *testing.T) {
	fixTitles := func(t *testing.T, p DashboardPipeline) []string {
		t.Helper()
		out := []string{}
		for _, f := range dashJoinStep(t, p, "received-review").Detail.Fixes {
			out = append(out, f.Title)
		}
		return out
	}

	t.Run("not a map or no title is skipped", func(t *testing.T) {
		noTitle := dashRRFix("", "fixed", "2026-10-07T08:40:00Z", "2026-10-07T08:41:00Z")
		delete(noTitle, "title")
		ship := dashRRShip(t, []any{"x", noTitle, dashRRFix("ok", "fixed", "2026-10-07T08:40:00Z", "2026-10-07T08:41:00Z")}, nil)
		if got := fixTitles(t, ship); !reflect.DeepEqual(got, []string{"ok"}) {
			t.Errorf("fixes = %v, want [ok]", got)
		}
	})

	t.Run("unknown status or severity is skipped", func(t *testing.T) {
		badSeverity := dashRRFix("bad severity", "fixed", "2026-10-07T08:40:00Z", "2026-10-07T08:41:00Z")
		badSeverity["severity"] = "huge x"
		ship := dashRRShip(t, []any{
			dashRRFix("bad status", "done now", "2026-10-07T08:40:00Z", "2026-10-07T08:41:00Z"),
			badSeverity,
			dashRRFix("ok", "fixed", "2026-10-07T08:40:00Z", "2026-10-07T08:41:00Z"),
		}, nil)
		if got := fixTitles(t, ship); !reflect.DeepEqual(got, []string{"ok"}) {
			t.Errorf("fixes = %v, want [ok]", got)
		}
	})

	t.Run("every record skipped gives no station", func(t *testing.T) {
		ship := dashRRShip(t, []any{"x", dashRRFix("bad", "done now", "", "")}, nil)
		if got, want := dashRRNames(ship), []string{"execute", "commit", "review"}; !reflect.DeepEqual(got, want) {
			t.Errorf("steps = %v, want %v", got, want)
		}
	})

	t.Run("line is read and 0 gives no key", func(t *testing.T) {
		noLine := dashRRFix("no line", "fixed", "2026-10-07T08:40:00Z", "2026-10-07T08:41:00Z")
		noLine["line"] = float64(0)
		ship := dashRRShip(t, []any{dashRRFix("line", "fixed", "2026-10-07T08:40:00Z", "2026-10-07T08:41:00Z"), noLine}, nil)
		fixes := dashJoinStep(t, ship, "received-review").Detail.Fixes
		b, err := json.Marshal(fixes)
		if err != nil {
			t.Fatal(err)
		}
		want := `[{"title":"line","severity":"high","file":"internal/x.go","line":42,"status":"fixed"},` +
			`{"title":"no line","severity":"high","file":"internal/x.go","status":"fixed"}]`
		if string(b) != want {
			t.Errorf("fixes JSON = %s, want %s", b, want)
		}
	})

	t.Run("more than the writer cap is read in full", func(t *testing.T) {
		list := []any{}
		for range healingFixProgressMax + 1 {
			list = append(list, dashRRFix("f", "fixed", "2026-10-07T08:40:00Z", "2026-10-07T08:41:00Z"))
		}
		ship := dashRRShip(t, list, nil)
		if n := len(dashJoinStep(t, ship, "received-review").Detail.Fixes); n != healingFixProgressMax+1 {
			t.Errorf("fixes = %d, want %d", n, healingFixProgressMax+1)
		}
	})

	t.Run("bad time is skipped alone", func(t *testing.T) {
		ship := dashRRShip(t, []any{dashRRFix("a", "fixed", "yesterday", "2026-10-07T08:41:00Z")}, nil)
		got := dashRRStepJSON(t, ship, "received-review")
		if strings.Contains(got, "startedAt") || !strings.Contains(got, `"completedAt":"2026-10-07T08:41:00Z"`) {
			t.Errorf("step JSON = %s, want no startedAt key and completedAt 08:41", got)
		}
		ship = dashRRShip(t, []any{dashRRFix("a", "fixed", "2026-10-07T08:40:00Z", "later")}, nil)
		got = dashRRStepJSON(t, ship, "received-review")
		if strings.Contains(got, "completedAt") || !strings.Contains(got, `"startedAt":"2026-10-07T08:40:00Z"`) {
			t.Errorf("step JSON = %s, want startedAt 08:40 and no completedAt key", got)
		}
	})

	t.Run("valid records keep list order", func(t *testing.T) {
		ship := dashRRShip(t, []any{
			dashRRFix("second by time", "fixed", "2026-10-07T08:45:00Z", "2026-10-07T08:46:00Z"),
			dashRRFix("first by time", "deferred", "2026-10-07T08:40:00Z", "2026-10-07T08:41:00Z"),
		}, nil)
		d := dashJoinStep(t, ship, "received-review").Detail
		if d.Kind != dashboardKindFixes {
			t.Errorf("kind = %q, want %q", d.Kind, dashboardKindFixes)
		}
		if got := fixTitles(t, ship); !reflect.DeepEqual(got, []string{"second by time", "first by time"}) {
			t.Errorf("fixes = %v, want list order", got)
		}
	})

	t.Run("title is redacted and truncated", func(t *testing.T) {
		long := strings.Repeat("y", 300)
		ship := dashRRShip(t, []any{dashRRFix(long, "fixed", "2026-10-07T08:40:00Z", "2026-10-07T08:41:00Z")}, nil)
		if got := fixTitles(t, ship); got[0] != dashboardPreview(long) || got[0] == long {
			t.Errorf("title = %q, want dashboardPreview of the stored title", got[0])
		}
	})
}

// TestDashboardReceivedReview_Place checks the place table: an existing
// received-review step gets the detail and keeps its status and times; a
// review step gets the station inserted after it; neither gives no station.
func TestDashboardReceivedReview_Place(t *testing.T) {
	fixes := []any{dashRRFix("a", "fixed", "2026-10-07T08:40:00Z", "2026-10-07T08:41:00Z")}

	t.Run("existing step gets the detail", func(t *testing.T) {
		ship := dashRRShip(t, fixes, func(d map[string]any) {
			d["steps"] = append(d["steps"].([]any),
				map[string]any{"name": "received-review", "status": StepInProgress, "startedAt": "2026-10-07T08:39:00Z"})
		})
		if got, want := dashRRNames(ship), []string{"execute", "commit", "review", "received-review"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("steps = %v, want %v", got, want)
		}
		s := dashJoinStep(t, ship, "received-review")
		if s.Status != StepInProgress || s.StartedAt != "2026-10-07T08:39:00Z" || s.CompletedAt != "" {
			t.Errorf("step = %+v, want the stored status and times", s)
		}
		if s.Detail == nil || s.Detail.Kind != dashboardKindFixes || len(s.Detail.Fixes) != 1 {
			t.Errorf("detail = %+v, want one fix", s.Detail)
		}
		wantProgress := DashboardProgress{Done: 2, Total: 4, Current: "review", Label: "step 3 of 4"}
		if ship.Progress != wantProgress {
			t.Errorf("progress = %+v, want %+v", ship.Progress, wantProgress)
		}
	})

	t.Run("review step gets the station after it", func(t *testing.T) {
		ship := dashRRShip(t, fixes, func(d map[string]any) {
			d["steps"] = append(d["steps"].([]any), map[string]any{"name": "pr", "status": StepPending})
		})
		if got, want := dashRRNames(ship), []string{"execute", "commit", "review", "received-review", "pr"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("steps = %v, want %v", got, want)
		}
		wantProgress := DashboardProgress{Done: 3, Total: 5, Current: "review", Label: "step 4 of 5"}
		if ship.Progress != wantProgress {
			t.Errorf("progress = %+v, want %+v", ship.Progress, wantProgress)
		}
	})

	t.Run("neither step gives no station", func(t *testing.T) {
		ship := dashRRShip(t, fixes, func(d map[string]any) {
			d["steps"] = d["steps"].([]any)[:2]
		})
		if got, want := dashRRNames(ship), []string{"execute", "commit"}; !reflect.DeepEqual(got, want) {
			t.Errorf("steps = %v, want %v", got, want)
		}
		wantProgress := DashboardProgress{Done: 2, Total: 2, Current: "commit", Label: "step 2 of 2"}
		if ship.Progress != wantProgress {
			t.Errorf("progress = %+v, want %+v", ship.Progress, wantProgress)
		}
	})
}

// TestDashboardReceivedReview_BeforeAfter checks the step list and progress
// of a ship run with a plan station, before and after the received-review
// station: all fixes final, one fix still fixing, and no current step.
func TestDashboardReceivedReview_BeforeAfter(t *testing.T) {
	steps := func(started bool) []any {
		at := func(s string) string {
			if started {
				return s
			}
			return ""
		}
		harden := map[string]any{"name": "harden", "status": StepPending}
		if started {
			harden["status"], harden["startedAt"] = StepInProgress, "2026-10-07T08:50:00Z"
		}
		return []any{
			map[string]any{"name": "execute", "status": StepCompleted, "startedAt": at("2026-10-07T08:00:00Z")},
			map[string]any{"name": "commit", "status": StepCompleted, "startedAt": at("2026-10-07T08:20:00Z")},
			map[string]any{"name": "review", "status": StepCompleted, "startedAt": at("2026-10-07T08:30:00Z")},
			harden,
			map[string]any{"name": "pr", "status": StepPending},
		}
	}
	run := func(fixes any, started bool) DashboardPipeline {
		return dashRRShip(t, fixes, func(d map[string]any) {
			d["steps"] = steps(started)
			d["planExploreSummary"] = []any{}
		})
	}
	final := []any{dashRRFix("a", "fixed", "2026-10-07T08:40:00Z", "2026-10-07T08:41:00Z")}
	open := []any{
		dashRRFix("a", "fixed", "2026-10-07T08:40:00Z", "2026-10-07T08:41:00Z"),
		dashRRFix("b", "fixing", "2026-10-07T08:42:00Z", "2026-10-07T08:43:00Z"),
	}
	without := []string{"plan", "execute", "commit", "review", "harden", "pr"}
	with := []string{"plan", "execute", "commit", "review", "received-review", "harden", "pr"}

	cases := []struct {
		name      string
		fixes     any
		started   bool
		wantSteps []string
		want      DashboardProgress
	}{
		{"before", nil, true, without, DashboardProgress{Done: 4, Total: 6, Current: "harden", Label: "step 5 of 6"}},
		{"all fixes final", final, true, with, DashboardProgress{Done: 5, Total: 7, Current: "harden", Label: "step 6 of 7"}},
		{"one fix still fixing", open, true, with, DashboardProgress{Done: 4, Total: 7, Current: "harden", Label: "step 5 of 7"}},
		{"no current, all fixes final", final, false, with, DashboardProgress{Done: 5, Total: 7, Label: "5 of 7 steps"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ship := run(c.fixes, c.started)
			if got := dashRRNames(ship); !reflect.DeepEqual(got, c.wantSteps) {
				t.Errorf("steps = %v, want %v", got, c.wantSteps)
			}
			if ship.Progress != c.want {
				t.Errorf("progress = %+v, want %+v", ship.Progress, c.want)
			}
		})
	}
	if s := dashJoinStep(t, run(open, true), "received-review"); s.Status != StepInProgress {
		t.Errorf("received-review status = %q, want %q", s.Status, StepInProgress)
	}
}
