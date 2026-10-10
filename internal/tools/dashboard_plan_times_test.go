package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// The plan of the tests ran from 06:20 to 07:40, before the ship run that
// starts at 08:00 (dashJoinShipData).
const (
	dashPlanStart = "2026-10-07T06:20:00Z"
	dashPlanEnd   = "2026-10-07T07:40:00Z"
	dashPlanFile  = "/work/tree/plans/feature.md"
)

// dashPlanLinked returns a ship state linkedPlan that holds both times.
func dashPlanLinked() map[string]any {
	return map[string]any{"planFile": dashPlanFile, "startedAt": dashPlanStart, "completedAt": dashPlanEnd}
}

// dashPlanShip writes the running ship state of dashJoinShipData, after edit
// changed its data, and returns the repo and its ship pipeline.
func dashPlanShip(t *testing.T, root string, edit func(map[string]any)) (DashboardRepo, DashboardPipeline) {
	t.Helper()
	data := dashJoinShipData()
	if edit != nil {
		edit(data)
	}
	dashWriteState(t, root, dashJoinShipFile, data, dashJoinFresh)
	repo := dashCollect(t, root)
	return repo, dashJoinFind(t, repo.Pipelines, dashJoinShipID)
}

// dashPlanExecute writes an execute state of feat/x that names dashPlanFile
// as its plan. The state starts inside the ship window.
func dashPlanExecute(t *testing.T, root string) {
	t.Helper()
	data := dashJoinExecData("feat/x", "2026-10-07T08:10:00Z", "abc1234")
	data["planPath"] = dashPlanFile
	dashWriteState(t, root, dashJoinExecFile("20261007T081000Z"), data, dashJoinFresh)
}

// dashPlanRow appends a plan row for dashPlanFile to runs.jsonl.
func dashPlanRow(t *testing.T, root, startedAt, lastModifiedAt string) {
	t.Helper()
	rec := history.RunRecord{
		Timestamp: "2026-10-07T07:41:00Z", Skill: "plan", Branch: "feat/x", Outcome: "success",
		PlanFile: dashPlanFile, StartedAt: startedAt, LastModifiedAt: lastModifiedAt,
	}
	if err := history.NewFileWriter(historyDir(root)).AppendRun(rec); err != nil {
		t.Fatal(err)
	}
}

// dashPlanNoTimes fails the test when p carries a plan time or a plan step
// with a time.
func dashPlanNoTimes(t *testing.T, p DashboardPipeline) {
	t.Helper()
	if p.PlanStartedAt != "" {
		t.Errorf("planStartedAt = %q, want none", p.PlanStartedAt)
	}
	for _, s := range p.Steps {
		if s.Name == dashboardShipStepPlan {
			t.Errorf("plan step %+v, want none", s)
		}
	}
}

// dashPlanCount returns how many steps of p are named name.
func dashPlanCount(p DashboardPipeline, name string) int {
	n := 0
	for _, s := range p.Steps {
		if s.Name == name {
			n++
		}
	}
	return n
}

// TestDashboardPlanTimes_LinkedPlanGivesStationTimes checks that a saved linkedPlan gives the plan station its times and the ship pipeline its planStartedAt.
func TestDashboardPlanTimes_LinkedPlanGivesStationTimes(t *testing.T) {
	root := dashRoot(t)
	repo, ship := dashPlanShip(t, root, func(d map[string]any) { d[shipLinkedPlanKey] = dashPlanLinked() })

	if ship.PlanStartedAt != dashPlanStart {
		t.Errorf("planStartedAt = %q, want %q", ship.PlanStartedAt, dashPlanStart)
	}
	want := DashboardStep{Name: "plan", Status: StepCompleted, StartedAt: dashPlanStart, CompletedAt: dashPlanEnd}
	if len(ship.Steps) == 0 || !reflect.DeepEqual(ship.Steps[0], want) {
		t.Errorf("first step = %+v, want %+v", ship.Steps, want)
	}
	if len(repo.Warnings) != 0 || repo.Error != "" {
		t.Errorf("warnings = %v, error = %q, want none", repo.Warnings, repo.Error)
	}
}

// TestDashboardPlanTimes_RunningShipGetsOnePlanStep checks that a running ship run with a linkedPlan and no explorer station gets one plan step and one more done step.
func TestDashboardPlanTimes_RunningShipGetsOnePlanStep(t *testing.T) {
	root := dashRoot(t)
	_, before := dashPlanShip(t, root, nil)
	_, ship := dashPlanShip(t, root, func(d map[string]any) { d[shipLinkedPlanKey] = dashPlanLinked() })

	if n := dashPlanCount(ship, "plan"); n != 1 || ship.Steps[0].Name != "plan" {
		t.Fatalf("steps = %+v, want one plan step first", ship.Steps)
	}
	if len(ship.Steps) != len(before.Steps)+1 {
		t.Errorf("steps = %d, want %d", len(ship.Steps), len(before.Steps)+1)
	}
	want := DashboardProgress{Done: before.Progress.Done + 1, Total: before.Progress.Total + 1, Current: "review", Label: "step 4 of 4"}
	if ship.Progress != want {
		t.Errorf("progress = %+v, want %+v (before %+v)", ship.Progress, want, before.Progress)
	}
	if ship.Progress.Label == before.Progress.Label {
		t.Errorf("label = %q, want a new label", ship.Progress.Label)
	}
}

// TestDashboardPlanTimes_ExplorerStationAndLinkedPlanAreOneStation checks that an explorer station and a linkedPlan give exactly one plan step, with the explorers and the times.
func TestDashboardPlanTimes_ExplorerStationAndLinkedPlanAreOneStation(t *testing.T) {
	entries := []ExploreSummaryEntry{{Name: "auth-flow", Status: "done", Total: 12}}
	root := dashRoot(t)
	_, ship := dashPlanShip(t, root, func(d map[string]any) {
		d["planExploreSummary"] = entries
		d[shipLinkedPlanKey] = dashPlanLinked()
	})

	if n := dashPlanCount(ship, "plan"); n != 1 {
		t.Fatalf("plan steps = %d, want 1: %+v", n, ship.Steps)
	}
	plan := ship.Steps[0]
	if plan.Name != "plan" || plan.StartedAt != dashPlanStart || plan.CompletedAt != dashPlanEnd {
		t.Errorf("plan step = %+v, want times %s to %s", plan, dashPlanStart, dashPlanEnd)
	}
	wantDetail := &DashboardStepDetail{Kind: dashboardKindExplorers, Explorers: dashboardExplorers(entries)}
	if !reflect.DeepEqual(plan.Detail, wantDetail) {
		t.Errorf("plan detail = %+v, want %+v", plan.Detail, wantDetail)
	}
	// The explorer station counted once: 3 own steps + 1 plan.
	if ship.Progress.Done != 3 || ship.Progress.Total != 4 {
		t.Errorf("progress = %+v, want done 3 of 4", ship.Progress)
	}
}

// TestDashboardPlanTimes_ExplorerStationWithoutTimesStaysUntimed checks that an explorer station with no linkedPlan and no join hit has no times and no planStartedAt.
func TestDashboardPlanTimes_ExplorerStationWithoutTimesStaysUntimed(t *testing.T) {
	root := dashRoot(t)
	_, ship := dashPlanShip(t, root, func(d map[string]any) {
		d["planExploreSummary"] = []ExploreSummaryEntry{{Name: "auth-flow", Status: "done", Total: 1}}
	})

	if ship.Steps[0].Name != "plan" || ship.Steps[0].StartedAt != "" || ship.Steps[0].CompletedAt != "" {
		t.Errorf("plan step = %+v, want a station with no times", ship.Steps[0])
	}
	if ship.PlanStartedAt != "" {
		t.Errorf("planStartedAt = %q, want none", ship.PlanStartedAt)
	}
}

// TestDashboardPlanTimes_JoinGivesTimesOfOldRun checks that an old ship run with no linkedPlan takes its times from the runs.jsonl join.
func TestDashboardPlanTimes_JoinGivesTimesOfOldRun(t *testing.T) {
	root := dashRoot(t)
	dashPlanExecute(t, root)
	dashPlanRow(t, root, dashPlanStart, dashPlanEnd)
	repo, ship := dashPlanShip(t, root, nil)

	if ship.PlanStartedAt != dashPlanStart {
		t.Errorf("planStartedAt = %q, want %q", ship.PlanStartedAt, dashPlanStart)
	}
	want := DashboardStep{Name: "plan", Status: StepCompleted, StartedAt: dashPlanStart, CompletedAt: dashPlanEnd}
	if n := dashPlanCount(ship, "plan"); n != 1 || !reflect.DeepEqual(ship.Steps[0], want) {
		t.Errorf("steps = %+v, want one first step %+v", ship.Steps, want)
	}
	if len(repo.Warnings) != 0 || repo.Error != "" {
		t.Errorf("warnings = %v, error = %q, want none", repo.Warnings, repo.Error)
	}
}

// TestDashboardPlanTimes_JoinTimesGoToExplorerStation checks that the join times go to the explorer station, so the plan step stays one station.
func TestDashboardPlanTimes_JoinTimesGoToExplorerStation(t *testing.T) {
	root := dashRoot(t)
	dashPlanExecute(t, root)
	dashPlanRow(t, root, dashPlanStart, dashPlanEnd)
	_, ship := dashPlanShip(t, root, func(d map[string]any) {
		d["planExploreSummary"] = []ExploreSummaryEntry{{Name: "auth-flow", Status: "done", Total: 1}}
	})

	if n := dashPlanCount(ship, "plan"); n != 1 {
		t.Fatalf("plan steps = %d, want 1: %+v", n, ship.Steps)
	}
	plan := ship.Steps[0]
	if plan.StartedAt != dashPlanStart || plan.CompletedAt != dashPlanEnd || plan.Detail == nil || plan.Detail.Kind != dashboardKindExplorers {
		t.Errorf("plan step = %+v, want explorers and times", plan)
	}
}

// TestDashboardPlanTimes_LinkedPlanWinsOverJoin checks that a linkedPlan takes priority over a matching runs.jsonl plan row.
func TestDashboardPlanTimes_LinkedPlanWinsOverJoin(t *testing.T) {
	root := dashRoot(t)
	dashPlanExecute(t, root)
	dashPlanRow(t, root, "2026-10-07T05:00:00Z", "2026-10-07T05:30:00Z")
	_, ship := dashPlanShip(t, root, func(d map[string]any) { d[shipLinkedPlanKey] = dashPlanLinked() })

	if ship.PlanStartedAt != dashPlanStart || ship.Steps[0].CompletedAt != dashPlanEnd {
		t.Errorf("planStartedAt = %q, step = %+v, want the linkedPlan times", ship.PlanStartedAt, ship.Steps[0])
	}
}

// TestDashboardPlanTimes_NoLinkedPlanGivesNoTime checks that a ship run with no execute state, no planPath or no matching plan row gets no time.
func TestDashboardPlanTimes_NoLinkedPlanGivesNoTime(t *testing.T) {
	t.Run("no execute state", func(t *testing.T) {
		root := dashRoot(t)
		repo, ship := dashPlanShip(t, root, nil)
		dashPlanNoTimes(t, ship)
		if len(repo.Warnings) != 0 {
			t.Errorf("warnings = %v, want none", repo.Warnings)
		}
	})

	t.Run("execute state without planPath", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, dashJoinExecFile("20261007T081000Z"),
			dashJoinExecData("feat/x", "2026-10-07T08:10:00Z", "abc1234"), dashJoinFresh)
		dashPlanRow(t, root, dashPlanStart, dashPlanEnd)
		repo, ship := dashPlanShip(t, root, nil)
		dashPlanNoTimes(t, ship)
		if len(repo.Warnings) != 0 {
			t.Errorf("warnings = %v, want none", repo.Warnings)
		}
	})

	t.Run("no plan row in runs.jsonl", func(t *testing.T) {
		root := dashRoot(t)
		dashPlanExecute(t, root)
		repo, ship := dashPlanShip(t, root, nil)
		dashPlanNoTimes(t, ship)
		if len(repo.Warnings) != 0 {
			t.Errorf("warnings = %v, want none", repo.Warnings)
		}
	})

	t.Run("plan row of another plan file", func(t *testing.T) {
		root := dashRoot(t)
		dashPlanExecute(t, root)
		rec := history.RunRecord{Skill: "plan", PlanFile: "/work/tree/plans/other.md", StartedAt: dashPlanStart, LastModifiedAt: dashPlanEnd}
		if err := history.NewFileWriter(historyDir(root)).AppendRun(rec); err != nil {
			t.Fatal(err)
		}
		_, ship := dashPlanShip(t, root, nil)
		dashPlanNoTimes(t, ship)
	})

	t.Run("join row without a start time", func(t *testing.T) {
		root := dashRoot(t)
		dashPlanExecute(t, root)
		dashPlanRow(t, root, "", dashPlanEnd)
		_, ship := dashPlanShip(t, root, nil)
		dashPlanNoTimes(t, ship)
	})

	t.Run("join row with a start time that does not parse", func(t *testing.T) {
		root := dashRoot(t)
		dashPlanExecute(t, root)
		dashPlanRow(t, root, "yesterday", dashPlanEnd)
		_, ship := dashPlanShip(t, root, nil)
		dashPlanNoTimes(t, ship)
	})
}

// TestDashboardPlanTimes_NoGuessedEnd checks that a completedAt that is absent, does not parse or is before the start gives a station with a start time only.
func TestDashboardPlanTimes_NoGuessedEnd(t *testing.T) {
	cases := []struct {
		name        string
		completedAt any
	}{
		{"absent", nil},
		{"does not parse", "later"},
		{"before the start", "2026-10-07T05:00:00Z"},
	}
	for _, tc := range cases {
		t.Run("linkedPlan completedAt "+tc.name, func(t *testing.T) {
			root := dashRoot(t)
			_, ship := dashPlanShip(t, root, func(d map[string]any) {
				linked := map[string]any{"planFile": dashPlanFile, "startedAt": dashPlanStart}
				if tc.completedAt != nil {
					linked["completedAt"] = tc.completedAt
				}
				d[shipLinkedPlanKey] = linked
			})
			if ship.PlanStartedAt != dashPlanStart {
				t.Errorf("planStartedAt = %q, want %q", ship.PlanStartedAt, dashPlanStart)
			}
			if got := ship.Steps[0]; got.StartedAt != dashPlanStart || got.CompletedAt != "" {
				t.Errorf("plan step = %+v, want a start time and no end time", got)
			}
		})
	}

	t.Run("join row with LastModifiedAt before the start", func(t *testing.T) {
		root := dashRoot(t)
		dashPlanExecute(t, root)
		dashPlanRow(t, root, dashPlanStart, "2026-10-07T05:00:00Z")
		_, ship := dashPlanShip(t, root, nil)
		if got := ship.Steps[0]; got.StartedAt != dashPlanStart || got.CompletedAt != "" {
			t.Errorf("plan step = %+v, want a start time and no end time", got)
		}
	})
}

// TestDashboardPlanTimes_LinkedPlanWithoutValidStartFallsThrough checks that a linkedPlan with no valid startedAt counts as absent, so the lookup falls to the join.
func TestDashboardPlanTimes_LinkedPlanWithoutValidStartFallsThrough(t *testing.T) {
	for name, linked := range map[string]any{
		"start absent":       map[string]any{"planFile": dashPlanFile},
		"start not RFC 3339": map[string]any{"planFile": dashPlanFile, "startedAt": "yesterday"},
		"not an object":      "plan",
	} {
		t.Run(name+", no join", func(t *testing.T) {
			root := dashRoot(t)
			_, ship := dashPlanShip(t, root, func(d map[string]any) { d[shipLinkedPlanKey] = linked })
			dashPlanNoTimes(t, ship)
		})
		t.Run(name+", join hit", func(t *testing.T) {
			root := dashRoot(t)
			dashPlanExecute(t, root)
			dashPlanRow(t, root, dashPlanStart, dashPlanEnd)
			_, ship := dashPlanShip(t, root, func(d map[string]any) { d[shipLinkedPlanKey] = linked })
			if ship.PlanStartedAt != dashPlanStart {
				t.Errorf("planStartedAt = %q, want the join time %q", ship.PlanStartedAt, dashPlanStart)
			}
		})
	}
}

// TestDashboardPlanTimes_RunsJSONLFolderGivesOneWarning checks that a runs.jsonl path that is a folder gives one repo warning and leaves repo.Error empty.
func TestDashboardPlanTimes_RunsJSONLFolderGivesOneWarning(t *testing.T) {
	root := dashRoot(t)
	dashPlanExecute(t, root)
	if err := os.MkdirAll(history.NewFileWriter(historyDir(root)).RunsPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	repo, ship := dashPlanShip(t, root, nil)

	if repo.Error != "" {
		t.Errorf("repo.Error = %q, want none", repo.Error)
	}
	if len(repo.Warnings) != 1 || !strings.HasPrefix(repo.Warnings[0], "plan times of ship run "+dashJoinShipID+" not read: ") {
		t.Fatalf("warnings = %v, want one plan times warning", repo.Warnings)
	}
	dashPlanNoTimes(t, ship)
}

// TestDashboardPlanTimes_CorruptExecuteStateGivesOneWarning checks that a corrupt execute state file gives one repo warning and leaves repo.Error empty.
func TestDashboardPlanTimes_CorruptExecuteStateGivesOneWarning(t *testing.T) {
	root := dashRoot(t)
	path := filepath.Join(root, paths.DataDir, paths.RunsSubdir, dashJoinExecFile("20261007T081000Z"))
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, dashJoinFresh, dashJoinFresh); err != nil {
		t.Fatal(err)
	}
	repo, ship := dashPlanShip(t, root, nil)

	if repo.Error != "" {
		t.Errorf("repo.Error = %q, want none", repo.Error)
	}
	if len(repo.Warnings) != 1 || !strings.HasPrefix(repo.Warnings[0], "plan times of ship run "+dashJoinShipID+" not read: ") {
		t.Fatalf("warnings = %v, want one plan times warning", repo.Warnings)
	}
	dashPlanNoTimes(t, ship)
}

// TestDashboardPlanTimes_LinkedPlanReadsNoFile checks that a ship run with a linkedPlan reads neither the execute state nor runs.jsonl.
func TestDashboardPlanTimes_LinkedPlanReadsNoFile(t *testing.T) {
	root := dashRoot(t)
	// Both sources are broken: a linkedPlan state must not touch them.
	if err := os.MkdirAll(history.NewFileWriter(historyDir(root)).RunsPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, paths.DataDir, paths.RunsSubdir, dashJoinExecFile("20261007T081000Z"))
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, ship := dashPlanShip(t, root, func(d map[string]any) { d[shipLinkedPlanKey] = dashPlanLinked() })

	if len(repo.Warnings) != 0 || ship.PlanStartedAt != dashPlanStart {
		t.Errorf("warnings = %v, planStartedAt = %q, want no warning and the linkedPlan start", repo.Warnings, ship.PlanStartedAt)
	}
}

// TestDashboardPlanTimes_HiddenShipReadsNoFile checks that a finished ship run outside the history window reads no file and adds no warning.
func TestDashboardPlanTimes_HiddenShipReadsNoFile(t *testing.T) {
	root := dashRoot(t)
	dashWriteState(t, root, dashJoinShipFile, map[string]any{
		"branch": "feat/x", "startedAt": "2026-10-04T08:00:00Z", "pipelineStatus": "completed",
		"pipelineCompletedAt": "2026-10-04T09:00:00Z", "steps": dashSteps(StepCompleted),
	}, dashNow.Add(-3*dashboardHistoryWindow))
	path := filepath.Join(root, paths.DataDir, paths.RunsSubdir, dashJoinExecFile("20261004T081000Z"))
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := dashCollect(t, root)

	if len(repo.Pipelines) != 0 {
		t.Fatalf("pipelines = %+v, want the old finished ship hidden", repo.Pipelines)
	}
	if len(repo.Warnings) != 0 {
		t.Errorf("warnings = %v, want none: a hidden ship reads no file", repo.Warnings)
	}
}

// TestDashboardPlanTimes_OnlyShipGetsPlanStartedAt checks that execute, plan and review pipelines never get planStartedAt.
func TestDashboardPlanTimes_OnlyShipGetsPlanStartedAt(t *testing.T) {
	root := dashRoot(t)
	withLinked := func(d map[string]any) map[string]any {
		d[shipLinkedPlanKey] = dashPlanLinked()
		return d
	}
	// The other kinds carry a linkedPlan key too: they must not read it.
	dashWriteState(t, root, "execute-feat-y-20261007T090000Z.json",
		withLinked(dashJoinExecData("feat/y", "2026-10-07T09:00:00Z", "abc1234")), dashJoinFresh)
	dashWriteState(t, root, "plan-feat-z-20261007T070000Z.json", withLinked(map[string]any{
		"branch": "feat/z", "planIntegrity": map[string]any{"skillInvoked": "2026-10-07T07:00:00Z"},
	}), dashJoinFresh)
	dashJoinRunMeta(t, root, dashJoinReview, reviewRunMeta{Branch: "feat/w", StartedAt: "2026-10-07T08:31:00Z"})
	dashJoinReviewDims(t, root, dashJoinReview)
	repo, _ := dashPlanShip(t, root, func(d map[string]any) { d[shipLinkedPlanKey] = dashPlanLinked() })

	kinds := map[string]bool{}
	for _, p := range repo.Pipelines {
		kinds[p.Kind] = true
		if p.Kind == "ship" {
			if p.PlanStartedAt != dashPlanStart {
				t.Errorf("ship planStartedAt = %q, want %q", p.PlanStartedAt, dashPlanStart)
			}
			continue
		}
		if p.PlanStartedAt != "" {
			t.Errorf("%s pipeline %s planStartedAt = %q, want none", p.Kind, p.ID, p.PlanStartedAt)
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "planStartedAt") {
			t.Errorf("%s pipeline JSON holds planStartedAt: %s", p.Kind, b)
		}
		if dashPlanCount(p, dashboardShipStepPlan) != 0 && p.Kind != "plan" {
			t.Errorf("%s pipeline got a ship plan station: %+v", p.Kind, p.Steps)
		}
	}
	for _, kind := range []string{"ship", "execute", "plan", "review"} {
		if !kinds[kind] {
			t.Errorf("no %s pipeline in the snapshot: %v", kind, dashJoinKinds(repo.Pipelines))
		}
	}
}

// TestDashboardPlanTimes_ShipJSONShape checks the JSON keys of planStartedAt and of the plan step times.
func TestDashboardPlanTimes_ShipJSONShape(t *testing.T) {
	root := dashRoot(t)
	_, ship := dashPlanShip(t, root, func(d map[string]any) { d[shipLinkedPlanKey] = dashPlanLinked() })

	b, err := json.Marshal(ship)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"planStartedAt":"` + dashPlanStart + `"`; !strings.Contains(string(b), want) {
		t.Errorf("ship JSON lacks %s: %s", want, b)
	}
	step, err := json.Marshal(ship.Steps[0])
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"name":"plan","status":"completed","startedAt":"` + dashPlanStart + `","completedAt":"` + dashPlanEnd + `"}`; string(step) != want {
		t.Errorf("plan step JSON = %s, want %s", step, want)
	}
}

// TestDashboardInsertPlanStation checks that the plan station goes first and the progress gains one done step and a new label.
func TestDashboardInsertPlanStation(t *testing.T) {
	p := &DashboardPipeline{
		Steps:    []DashboardStep{{Name: "execute", Status: StepCompleted}, {Name: "review", Status: StepInProgress}},
		Progress: DashboardProgress{Done: 1, Total: 2, Current: "review", Label: "step 2 of 2"},
	}
	dashboardInsertPlanStation(p, DashboardStep{Name: "plan", Status: StepCompleted, StartedAt: dashPlanStart})

	if got := []string{p.Steps[0].Name, p.Steps[1].Name, p.Steps[2].Name}; !reflect.DeepEqual(got, []string{"plan", "execute", "review"}) {
		t.Errorf("step order = %v", got)
	}
	want := DashboardProgress{Done: 2, Total: 3, Current: "review", Label: "step 3 of 3"}
	if p.Progress != want {
		t.Errorf("progress = %+v, want %+v", p.Progress, want)
	}
}
