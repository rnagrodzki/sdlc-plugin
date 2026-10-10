package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
)

// dashboardFixturePath is the shared snapshot fixture, relative to this
// package folder. The page tests read the same file.
const dashboardFixturePath = "../dashboard/web/testdata/snapshot.fixture.json"

// dashboardFixtureRead returns the bytes of the shared snapshot fixture.
func dashboardFixtureRead(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(dashboardFixturePath)
	if err != nil {
		t.Fatalf("read fixture %s: %v", dashboardFixturePath, err)
	}
	return raw
}

// dashboardFixtureDecode decodes raw into a DashboardSnapshot. A key that no
// field of the snapshot types names, and any text after the JSON value, fail
// the test.
func dashboardFixtureDecode(t *testing.T, raw []byte) DashboardSnapshot {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var snap DashboardSnapshot
	if err := dec.Decode(&snap); err != nil {
		t.Fatalf("strict decode of fixture: %v", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("fixture has data after the JSON value: %v", err)
	}
	return snap
}

// dashboardFixtureWalk calls visit for every node of a decoded JSON value,
// the root included. The path of a node reads like repos[0].pipelines[1].id.
// Map keys are visited in sorted order, so a failure reads the same on every
// run.
func dashboardFixtureWalk(v any, path string, visit func(path string, v any)) {
	visit(path, v)
	switch n := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(n))
		for k := range n {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			dashboardFixtureWalk(n[k], path+"."+k, visit)
		}
	case []any:
		for i, c := range n {
			dashboardFixtureWalk(c, fmt.Sprintf("%s[%d]", path, i), visit)
		}
	}
}

// dashboardFixtureDiff returns the path of the first difference between two
// decoded JSON values, or "" when they are equal.
func dashboardFixtureDiff(a, b any, path string) string {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			return path
		}
		keys := make([]string, 0, len(av)+len(bv))
		for k := range av {
			keys = append(keys, k)
		}
		for k := range bv {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range slices.Compact(keys) {
			x, inA := av[k]
			y, inB := bv[k]
			if !inA || !inB {
				return path + "." + k
			}
			if d := dashboardFixtureDiff(x, y, path+"."+k); d != "" {
				return d
			}
		}
		return ""
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return path
		}
		for i := range av {
			if d := dashboardFixtureDiff(av[i], bv[i], fmt.Sprintf("%s[%d]", path, i)); d != "" {
				return d
			}
		}
		return ""
	}
	if !reflect.DeepEqual(a, b) {
		return path
	}
	return ""
}

// TestDashboardFixture_MatchesContract decodes the shared snapshot fixture
// strictly into DashboardSnapshot, encodes it again, and compares both sides
// as JSON, so every key of the fixture survives the round trip. It also
// checks that no string holds an HTML marker, that no value is null except
// the completedAt of a pipeline, and that each step detail has a kind of the
// closed set.
func TestDashboardFixture_MatchesContract(t *testing.T) {
	raw := dashboardFixtureRead(t)
	snap := dashboardFixtureDecode(t, raw)

	encoded, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	var want, got any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal encoded snapshot: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("fixture and encoded snapshot differ first at %s", dashboardFixtureDiff(want, got, "$"))
	}

	dashboardFixtureWalk(want, "$", func(path string, v any) {
		switch n := v.(type) {
		case string:
			if strings.ContainsAny(n, "<>") {
				t.Errorf("%s holds an HTML marker: %q", path, n)
			}
		case nil:
			if !dashboardFixtureNullAllowed(path) {
				t.Errorf("%s is null: lists are [] and text is a string", path)
			}
		}
	})

	kinds := dashboardKinds
	for _, repo := range snap.Repos {
		for _, p := range repo.Pipelines {
			for _, s := range p.Steps {
				if s.Detail != nil && !slices.Contains(kinds, s.Detail.Kind) {
					t.Errorf("%s %s step %s: detail kind %q is not in %v", repo.Name, p.ID, s.Name, s.Detail.Kind, kinds)
				}
			}
		}
	}
}

// dashboardFixtureNullAllowed reports whether a null at path is legal. Only
// the completedAt of a pipeline may be null: a step time is absent, never
// null.
func dashboardFixtureNullAllowed(path string) bool {
	return strings.HasSuffix(path, ".completedAt") && !strings.Contains(path, ".steps[")
}

// TestDashboardFixture_NullAllowed pins the null rule of the fixture walk.
func TestDashboardFixture_NullAllowed(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"$.repos[0].pipelines[0].completedAt", true},
		{"$.repos[0].pipelines[0].steps[2].completedAt", false},
		{"$.repos[0].pipelines[0].steps[2].startedAt", false},
	}
	for _, c := range cases {
		if got := dashboardFixtureNullAllowed(c.path); got != c.want {
			t.Errorf("dashboardFixtureNullAllowed(%q) = %t, want %t", c.path, got, c.want)
		}
	}
}

// TestDashboardFixture_CoversContentTable checks that the shared snapshot
// fixture holds the repos, runs, detail kinds, issue sources, severities, and
// session matches that the page tests rely on. It fails when an edit of the
// fixture drops one of them.
func TestDashboardFixture_CoversContentTable(t *testing.T) {
	snap := dashboardFixtureDecode(t, dashboardFixtureRead(t))

	var names []string
	seen := map[string]bool{}
	reposWith := map[string]int{}
	for _, repo := range snap.Repos {
		names = append(names, repo.Name)
		for key, n := range map[string]int{
			"history":   len(repo.History),
			"deferred":  len(repo.Deferred),
			"learnings": len(repo.Learnings),
		} {
			if n > 0 {
				reposWith[key]++
			}
		}
		for _, h := range repo.History {
			seen["outcome:"+h.Outcome] = true
		}
		sessionIDs, sessionBranches := map[string]bool{}, map[string]bool{}
		for _, s := range repo.Sessions {
			sessionIDs[s.ID] = true
			sessionBranches[s.Branch] = true
		}
		for _, p := range repo.Pipelines {
			seen["pipeline:"+p.Kind] = true
			seen["status:"+p.Status] = true
			switch {
			case p.SessionID != "" && sessionIDs[p.SessionID]:
				seen["session:id match"] = true
			case p.SessionID == "" && sessionBranches[p.Branch]:
				seen["session:branch fallback"] = true
			}
			if p.CommitWaves != nil {
				seen[fmt.Sprintf("commitWaves:%t", *p.CommitWaves)] = true
			}
			if p.Attention != nil && p.Status == PipelineRunning && p.Attention.Text != "" {
				seen["running pipeline with attention"] = true
			}
			for _, is := range p.Issues {
				seen["source:"+is.Source] = true
				seen["severity:"+is.Severity] = true
				if p.Status == PipelineStalled && is.Source == dashboardSourcePipeline {
					seen["stalled run with a pipeline issue"] = true
				}
			}
			dashboardFixtureCoverSteps(p, seen)
		}
	}

	if want := []string{"sdlc-plugin", "identity-service", "payments-service"}; !slices.Equal(names, want) {
		t.Errorf("repos = %v, want %v", names, want)
	}
	for _, key := range []string{"history", "deferred"} {
		if reposWith[key] < 2 {
			t.Errorf("%s is filled in %d repos, want at least 2", key, reposWith[key])
		}
	}
	if reposWith["learnings"] < 1 {
		t.Errorf("learnings is empty in every repo")
	}

	required := []string{
		"pipeline:ship", "pipeline:plan", "pipeline:execute", "pipeline:review",
		"status:" + PipelineRunning, "status:" + PipelineStalled, "status:" + PipelineCompleted, "status:" + PipelineFailed,
		"outcome:success", "outcome:failure", "outcome:partial",
		"session:id match", "session:branch fallback", "stalled run with a pipeline issue",
		"commitWaves:true", "commitWaves:false",
		"ship step plan with explorers", "ship step execute with waves", "ship step review with unaccounted totals",
		"plan with five stations", "plan explore with explorers", "plan review with rounds and maxRounds",
		"execute with queued tasks", "wave with a commit sha", "wave without a commit sha", "task without a name",
		"review findings tile with a finding", "review findings tile without findings", "review dimension in progress",
		"harden step without detail",
		"step with both times", "step with a start time only",
		"running pipeline with attention", "guardrail counts", "result line", "round totals", "repair limit",
		"finding outcome", "review plan", "dimension with a wave", "skipped dimension with a reason",
	}
	for _, step := range []string{StepPending, StepInProgress, StepCompleted, StepSkipped, StepFailed} {
		required = append(required, "step:"+step)
	}
	for _, kind := range dashboardKinds {
		required = append(required, "kind:"+kind)
	}
	for _, src := range []string{
		dashboardSourceStep, dashboardSourceWave, dashboardSourceReview,
		dashboardSourceState, dashboardSourceTask, dashboardSourcePipeline,
	} {
		required = append(required, "source:"+src)
	}
	for _, sev := range dimensions.ValidSeverities {
		required = append(required, "severity:"+sev)
	}
	for _, key := range required {
		if !seen[key] {
			t.Errorf("fixture has no %q", key)
		}
	}
}

// dashboardFixtureCoverSteps records in seen which step and detail features
// the pipeline p holds.
func dashboardFixtureCoverSteps(p DashboardPipeline, seen map[string]bool) {
	if p.Kind == "plan" && len(p.Steps) == 5 && p.Status == PipelineCompleted {
		seen["plan with five stations"] = true
	}
	for _, s := range p.Steps {
		seen["step:"+s.Status] = true
		if s.StartedAt != "" && s.CompletedAt != "" {
			seen["step with both times"] = true
		}
		if s.StartedAt != "" && s.CompletedAt == "" {
			seen["step with a start time only"] = true
		}
		if p.Kind == "review" && s.Status == StepInProgress {
			seen["review dimension in progress"] = true
		}
		if s.Name == "harden" && s.Detail == nil {
			seen["harden step without detail"] = true
		}
		d := s.Detail
		if d == nil {
			continue
		}
		seen["kind:"+d.Kind] = true

		if d.Kind == dashboardKindGuardrails && d.Guardrails != nil && d.Guardrails.Total > 0 {
			seen["guardrail counts"] = true
		}
		if d.Kind == dashboardKindResult && d.Result != "" {
			seen["result line"] = true
		}
		if d.Kind == dashboardKindRounds && d.RoundTotals != nil && d.RoundTotals.Iterations > 0 {
			seen["round totals"] = true
		}
		if d.Kind == dashboardKindRounds && d.RepairLimit {
			seen["repair limit"] = true
		}
		if d.Kind == dashboardKindRounds && len(d.Outcomes) > 0 {
			seen["finding outcome"] = true
		}
		if d.Kind == dashboardKindDimensions && d.ReviewPlan != nil && d.ReviewPlan.DimensionsPlanned > 0 {
			seen["review plan"] = true
		}
		for _, dim := range d.Dimensions {
			if dim.Wave > 0 {
				seen["dimension with a wave"] = true
			}
			if dim.Status == StepSkipped && dim.Reason != "" {
				seen["skipped dimension with a reason"] = true
			}
		}

		switch {
		case p.Kind == "ship" && s.Name == "plan" && len(d.Explorers) > 0:
			seen["ship step plan with explorers"] = true
		case p.Kind == "ship" && s.Name == "execute" && len(d.Waves) > 0:
			seen["ship step execute with waves"] = true
		case p.Kind == "ship" && s.Name == "review" && d.ReviewTotals != nil && d.ReviewTotals.Unaccounted > 0 && len(d.Dimensions) > 0:
			seen["ship step review with unaccounted totals"] = true
		case p.Kind == "plan" && s.Name == dashboardPlanStationExplore && len(d.Explorers) > 0:
			seen["plan explore with explorers"] = true
		case p.Kind == "plan" && s.Name == dashboardPlanStationReview && len(d.Rounds) > 0 && d.MaxRounds > 0:
			seen["plan review with rounds and maxRounds"] = true
		case p.Kind == "execute" && len(d.Queued) > 0:
			seen["execute with queued tasks"] = true
		case p.Kind == "review" && d.Kind == dashboardKindFindings && len(d.Findings) > 0:
			seen["review findings tile with a finding"] = true
		case p.Kind == "review" && d.Kind == dashboardKindFindings:
			seen["review findings tile without findings"] = true
		}

		for _, w := range d.Waves {
			if w.CommittedSHA != "" {
				seen["wave with a commit sha"] = true
			} else {
				seen["wave without a commit sha"] = true
			}
			for _, task := range w.Tasks {
				if task.Name == "" {
					seen["task without a name"] = true
				}
			}
		}
	}
}
