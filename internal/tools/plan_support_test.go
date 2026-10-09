package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/openspec"
)

// errorClassOf returns "domain", "infra" or "data" for the three mcpserver
// typed errors, or "" if err is nil or untyped. suggestionOf below reports
// only the recovery text, which is identical in shape across all three
// classes — tests that care which class an error path produces assert on this
// instead, so a domain error silently becoming an infra error fails.
func errorClassOf(err error) string {
	var de *mcpserver.DomainError
	if errors.As(err, &de) {
		return "domain"
	}
	var ie *mcpserver.InfraError
	if errors.As(err, &ie) {
		return "infra"
	}
	var dte *mcpserver.DataError
	if errors.As(err, &dte) {
		return "data"
	}
	return ""
}

// suggestionOf returns the Suggestion field of err when it is one of the
// three mcpserver typed errors, or "" if err is nil or untyped. It
// deliberately does not report the class — pair it with errorClassOf when the
// class matters.
func suggestionOf(err error) string {
	var de *mcpserver.DomainError
	if errors.As(err, &de) {
		return de.Suggestion
	}
	var ie *mcpserver.InfraError
	if errors.As(err, &ie) {
		return ie.Suggestion
	}
	var dte *mcpserver.DataError
	if errors.As(err, &dte) {
		return dte.Suggestion
	}
	return ""
}

// ---------------------------------------------------------------------------
// plan_support merge_results tests
//
// mergeResults (internal/tools/plan_support.go) is a pure function — no
// filesystem or git fixtures are needed here, unlike ship_test.go's
// shipPrepare tests. Each test below calls mergeResults directly with a
// PlanSupportIn and asserts on the returned PlanSupportOut fields.
//
// Later waves extend this same file with sibling test functions: Task 11
// adds material_snapshot/material_compare tests, Task 12 adds
// openspec_appendix tests. Do not add stubs for those here — this file only
// covers merge_results.
// ---------------------------------------------------------------------------

// gateRange returns ["G<from>", ..., "G<to>"] inclusive, used to build
// coverage-check fixtures spanning the G1..G22 gate space.
func gateRange(from, to int) []string {
	var gates []string
	for i := from; i <= to; i++ {
		gates = append(gates, fmt.Sprintf("G%d", i))
	}
	return gates
}

// allGates returns G1..G22, the full expected coverage set (SKILL.md Step 3
// coverageCheck: "the union of all gateIds[] arrays returned by lanes MUST
// equal {G1..G22} exactly"). G22 (style compliance) joined the set in the
// same change that added the guardrail-compliance lane's second gate.
func allGates() []string {
	return gateRange(1, 22)
}

// fiveLanesCoveringAllGates returns 5 passing LaneResult fixtures whose
// GateIDs partition G1..G22 exactly (5+5+6+2+4 = 22 gates).
func fiveLanesCoveringAllGates() []LaneResult {
	return []LaneResult{
		{Name: "lane-1", Status: "pass", GateIDs: gateRange(1, 5)},
		{Name: "lane-2", Status: "pass", GateIDs: gateRange(6, 10)},
		{Name: "lane-3", Status: "pass", GateIDs: gateRange(11, 16)},
		{Name: "lane-4", Status: "pass", GateIDs: gateRange(17, 18)},
		{Name: "lane-5", Status: "pass", GateIDs: gateRange(19, 22)},
	}
}

// TestPlanMergeResults_CoveragePass verifies that when 5 lanes' GateIDs union
// covers G1..G22 exactly, the coverage check finds no gaps.
func TestPlanMergeResults_CoveragePass(t *testing.T) {
	out, err := mergeResults(PlanSupportIn{
		LaneResults:   fiveLanesCoveringAllGates(),
		ExpectedGates: allGates(),
	})
	if err != nil {
		t.Fatalf("mergeResults: %v", err)
	}
	if len(out.CoverageGaps) != 0 {
		t.Errorf("CoverageGaps = %v, want empty", out.CoverageGaps)
	}
}

// TestPlanMergeResults_CoverageFail verifies that a gate missing from every
// lane's GateIDs is reported in CoverageGaps and synthesized as a blocking
// coverage-check issue (Acceptance Criteria: "missing gates become blocking
// issues").
func TestPlanMergeResults_CoverageFail(t *testing.T) {
	lanes := fiveLanesCoveringAllGates()
	// Drop G14 from lane-3 (which otherwise covers G11-G16) so no lane
	// reports it.
	lanes[2].GateIDs = []string{"G11", "G12", "G13", "G15", "G16"}

	out, err := mergeResults(PlanSupportIn{
		LaneResults:   lanes,
		ExpectedGates: allGates(),
	})
	if err != nil {
		t.Fatalf("mergeResults: %v", err)
	}
	if len(out.CoverageGaps) != 1 || out.CoverageGaps[0] != "G14" {
		t.Errorf("CoverageGaps = %v, want [G14]", out.CoverageGaps)
	}

	found := false
	for _, iss := range out.AllIssues {
		if iss.GateID == "G14" && iss.Severity == "blocking" && iss.Source == "coverage-check" {
			found = true
		}
	}
	if !found {
		t.Errorf("AllIssues = %+v, want a blocking coverage-check issue for G14", out.AllIssues)
	}
}

// TestPlanMergeResults_G17Advisory verifies a failed lane whose GateIDs are
// G17-only is advisory, not blocking: it is excluded from LaneFailures and
// does not reject the merge.
func TestPlanMergeResults_G17Advisory(t *testing.T) {
	out, err := mergeResults(PlanSupportIn{
		LaneResults: []LaneResult{
			{Name: "g17-lane", Status: "fail", GateIDs: []string{"G17"}},
		},
	})
	if err != nil {
		t.Fatalf("mergeResults: %v", err)
	}
	if len(out.LaneFailures) != 0 {
		t.Errorf("LaneFailures = %v, want empty (G17-only lane failure is advisory)", out.LaneFailures)
	}
	if out.MergedStatus != "Approved" {
		t.Errorf("MergedStatus = %q, want %q (advisory, not blocking)", out.MergedStatus, "Approved")
	}
}

// TestPlanMergeResults_LaneFailBlocking verifies a failed lane covering a
// non-G17 gate is blocking: its name is reported in LaneFailures.
func TestPlanMergeResults_LaneFailBlocking(t *testing.T) {
	out, err := mergeResults(PlanSupportIn{
		LaneResults: []LaneResult{
			{Name: "static-structural", Status: "fail", GateIDs: []string{"G1"}},
		},
	})
	if err != nil {
		t.Fatalf("mergeResults: %v", err)
	}
	if len(out.LaneFailures) != 1 || out.LaneFailures[0] != "static-structural" {
		t.Errorf("LaneFailures = %v, want [static-structural]", out.LaneFailures)
	}
}

// mergeCall runs merge_results through planSupportCore, the dispatcher the
// registered plan_support handler calls.
func mergeCall(t *testing.T, in PlanSupportIn) PlanSupportOut {
	t.Helper()
	in.Action = "merge_results"
	out, err := planSupportCore("", "", in)
	if err != nil {
		t.Fatalf("merge_results: %v", err)
	}
	return out
}

// TestPlanMergeResults_BlockingCount verifies merge_results always returns
// blockingCount (0 too), that it equals the blocking count in the summary
// string, and that the field is absent from the JSON of other actions.
func TestPlanMergeResults_BlockingCount(t *testing.T) {
	cases := []struct {
		name string
		in   PlanSupportIn
		want int
	}{
		{
			name: "no issues",
			in:   PlanSupportIn{LensResults: []LensResult{{Name: "risk", Status: planStatusApproved}}},
			want: 0,
		},
		{
			name: "blocking and advisory issues",
			in: PlanSupportIn{
				LaneResults: []LaneResult{{Name: "lane-a", Status: "pass", GateIDs: []string{"G1"}, Issues: []Issue{
					{GateID: "G1", Severity: "blocking", Summary: "missing test"},
					{GateID: "G1", Severity: "advisory", Summary: "naming"},
				}}},
				LensResults: []LensResult{{Name: "risk", Status: planStatusIssuesFound, Issues: []Issue{
					{Severity: "blocking", Summary: "no rollback"},
					{Severity: "blocking", Summary: "no owner"},
				}}},
			},
			want: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := mergeCall(t, tc.in)
			if out.BlockingCount == nil {
				t.Fatal("BlockingCount = nil, want a value on merge_results")
			}
			if *out.BlockingCount != tc.want {
				t.Errorf("BlockingCount = %d, want %d", *out.BlockingCount, tc.want)
			}
			if want := fmt.Sprintf("%d blocking issue(s)", *out.BlockingCount); !strings.Contains(out.Summary, want) {
				t.Errorf("Summary = %q, want substring %q (same count as blockingCount)", out.Summary, want)
			}
			b, err := json.Marshal(out)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			if want := fmt.Sprintf(`"blockingCount":%d`, tc.want); !strings.Contains(string(b), want) {
				t.Errorf("merge_results JSON = %s, want %s", b, want)
			}
		})
	}

	b, err := json.Marshal(PlanSupportOut{Summary: "snapshot written"})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(b), "blockingCount") {
		t.Errorf("non-merge output JSON = %s, want no blockingCount", b)
	}
}

// TestPlanMergeResults_FailedLaneNoGates verifies a failed lane with empty
// gateIds is a lane failure. "Every gate is G17" is vacuously true for an
// empty list, so such a lane must not be treated as G17-only.
func TestPlanMergeResults_FailedLaneNoGates(t *testing.T) {
	out := mergeCall(t, PlanSupportIn{
		LaneResults: []LaneResult{{Name: "broken-lane", Status: "fail"}},
	})
	if len(out.LaneFailures) != 1 || out.LaneFailures[0] != "broken-lane" {
		t.Errorf("LaneFailures = %v, want [broken-lane]", out.LaneFailures)
	}
	for _, iss := range out.AllIssues {
		if strings.Contains(iss.Summary, "covers only G17") {
			t.Errorf("AllIssues has G17-only advisory %+v for a lane with no gates", iss)
		}
	}
}

// TestPlanMergeResults_FailedLaneWithoutIssues verifies a failed non-G17 lane
// yields Issues Found even when it reports no issues: a lane that could not
// evaluate its gates must not approve the plan.
func TestPlanMergeResults_FailedLaneWithoutIssues(t *testing.T) {
	out := mergeCall(t, PlanSupportIn{
		LaneResults: []LaneResult{{Name: "static-structural", Status: "fail", GateIDs: []string{"G1"}}},
	})
	if out.MergedStatus != "Issues Found" {
		t.Errorf("MergedStatus = %q, want %q", out.MergedStatus, "Issues Found")
	}
}

// TestPlanMergeResults_RedispatchFailedG17Lane verifies the status agrees
// with allIssues on a redispatch: a failed G17-only lane whose G17 issue was
// submitted as blocking shows that issue as advisory, so the merge is
// Approved. The status must not read the pre-downgrade severity.
func TestPlanMergeResults_RedispatchFailedG17Lane(t *testing.T) {
	out := mergeCall(t, PlanSupportIn{
		LaneResults: []LaneResult{{Name: "g17-lane", Status: "fail", GateIDs: []string{"G17"}, Issues: []Issue{
			{GateID: "G17", Severity: "blocking", Summary: "dimension gap"},
		}}},
		IsRedispatch: true,
	})
	for _, iss := range out.AllIssues {
		if iss.Severity == "blocking" {
			t.Errorf("AllIssues has blocking issue %+v, want every G17 issue advisory", iss)
		}
	}
	if out.MergedStatus != "Approved" {
		t.Errorf("MergedStatus = %q, want %q (no blocking issue and no non-G17 lane failure)", out.MergedStatus, "Approved")
	}
}

// TestPlanMergeResults_RejectedLensWithLanes verifies a rejected lens yields
// Issues Found in the mixed call (laneResults and lensResults together, the
// redispatch path), not only in a lens-only call.
func TestPlanMergeResults_RejectedLensWithLanes(t *testing.T) {
	out := mergeCall(t, PlanSupportIn{
		LaneResults: []LaneResult{{Name: "lane-1", Status: "pass", GateIDs: []string{"G1"}}},
		LensResults: []LensResult{
			{Name: "lens-risk", Status: "rejected"},
			{Name: "lens-architecture", Status: "approved"},
		},
		ExpectedGates: []string{"G1"},
		IsRedispatch:  true,
	})
	if out.MergedStatus != "Issues Found" {
		t.Errorf("MergedStatus = %q, want %q", out.MergedStatus, "Issues Found")
	}
}

// TestPlanMergeResults_IssueDedup verifies AllIssues dedups by
// (gateId, lowercased-trimmed summary) — including across sources: two lanes
// and one lens each report the same (gateId, summary) pair with differing
// case/whitespace, and only the first occurrence (from lane-a) survives.
// This exercises the "mixed" call pattern (both laneResults and lensResults
// supplied in one call) and the cross-source dedup acceptance criterion.
func TestPlanMergeResults_IssueDedup(t *testing.T) {
	out, err := mergeResults(PlanSupportIn{
		LaneResults: []LaneResult{
			{Name: "lane-a", Status: "pass", GateIDs: []string{"G5"}, Issues: []Issue{
				{GateID: "G5", Severity: "blocking", Summary: "Missing acceptance criteria for G5"},
			}},
			{Name: "lane-b", Status: "pass", GateIDs: []string{"G6"}, Issues: []Issue{
				{GateID: "G5", Severity: "blocking", Summary: "  missing acceptance criteria for g5  "},
			}},
		},
		LensResults: []LensResult{
			{Name: "lens-a", Status: "approved", Issues: []Issue{
				{GateID: "G5", Severity: "blocking", Summary: "MISSING ACCEPTANCE CRITERIA FOR G5"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("mergeResults: %v", err)
	}
	if len(out.AllIssues) != 1 {
		t.Fatalf("AllIssues = %+v, want exactly 1 (3 duplicates across 2 lanes + 1 lens must collapse to 1)", out.AllIssues)
	}
	if out.AllIssues[0].Source != "lane-a" {
		t.Errorf("AllIssues[0].Source = %q, want %q (first occurrence wins)", out.AllIssues[0].Source, "lane-a")
	}
}

// TestPlanMergeResults_StableFindingIDs verifies that every issue in
// AllIssues carries "f-" plus the first 8 hex chars of
// sha256(gateId|lower(trim(summary))), that the ID is stable across calls and
// across letter case and outer spaces, and that an ID sent in the input is
// ignored. The two literals were computed with `shasum -a 256` on the key
// strings "G5|missing acceptance criteria for g5" and "G7|other finding".
func TestPlanMergeResults_StableFindingIDs(t *testing.T) {
	idPattern := regexp.MustCompile(`^f-[0-9a-f]{8}$`)

	first, err := mergeResults(PlanSupportIn{
		LaneResults: []LaneResult{
			{Name: "lane-a", Status: "pass", GateIDs: []string{"G5"}, Issues: []Issue{
				{GateID: "G5", Severity: "blocking", Summary: "Missing acceptance criteria for G5"},
			}},
		},
		LensResults: []LensResult{
			{Name: "lens-a", Status: "approved", Issues: []Issue{
				{GateID: "G7", Severity: "advisory", Summary: "Other finding"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("mergeResults: %v", err)
	}
	if len(first.AllIssues) != 2 {
		t.Fatalf("AllIssues = %+v, want 2 issues", first.AllIssues)
	}
	for _, iss := range first.AllIssues {
		if !idPattern.MatchString(iss.ID) {
			t.Errorf("issue %q: ID = %q, want match %s", iss.Summary, iss.ID, idPattern)
		}
	}
	if got, want := first.AllIssues[0].ID, "f-19d6a497"; got != want {
		t.Errorf("AllIssues[0].ID = %q, want %q", got, want)
	}
	if got, want := first.AllIssues[1].ID, "f-e5fb5e6a"; got != want {
		t.Errorf("AllIssues[1].ID = %q, want %q", got, want)
	}

	// Second call: same gate, different case and outer spaces, and a wrong
	// ID in the input. The computed ID must win and match the first call.
	second, err := mergeResults(PlanSupportIn{
		LaneResults: []LaneResult{
			{Name: "lane-b", Status: "pass", GateIDs: []string{"G5"}, Issues: []Issue{
				{ID: "f-deadbeef", GateID: "G5", Severity: "blocking", Summary: "  MISSING ACCEPTANCE CRITERIA FOR g5  "},
			}},
		},
	})
	if err != nil {
		t.Fatalf("mergeResults: %v", err)
	}
	if len(second.AllIssues) != 1 {
		t.Fatalf("AllIssues = %+v, want 1 issue", second.AllIssues)
	}
	if got, want := second.AllIssues[0].ID, first.AllIssues[0].ID; got != want {
		t.Errorf("ID with different case/spaces = %q, want %q (same as first call; input ID must be ignored)", got, want)
	}
}

// TestPlanMergeResults_FindingIDsOnSynthesizedIssues verifies that issues the
// merge creates itself (G17-only lane failure, coverage gap) also get an ID.
func TestPlanMergeResults_FindingIDsOnSynthesizedIssues(t *testing.T) {
	out, err := mergeResults(PlanSupportIn{
		LaneResults: []LaneResult{
			{Name: "lane-g17", Status: "fail", GateIDs: []string{"G17"}},
		},
		ExpectedGates: []string{"G17", "G9"},
	})
	if err != nil {
		t.Fatalf("mergeResults: %v", err)
	}
	if len(out.AllIssues) != 2 {
		t.Fatalf("AllIssues = %+v, want 2 synthesized issues", out.AllIssues)
	}
	for _, iss := range out.AllIssues {
		if want := findingID(iss); iss.ID == "" || iss.ID != want {
			t.Errorf("issue %q: ID = %q, want %q", iss.Summary, iss.ID, want)
		}
	}
}

// TestPlanMergeResults_LensMergeApproved verifies the lenses-only call
// pattern: when all lenses report Status "approved", MergedStatus is
// "Approved".
func TestPlanMergeResults_LensMergeApproved(t *testing.T) {
	out, err := mergeResults(PlanSupportIn{
		LensResults: []LensResult{
			{Name: "lens-1", Status: "approved"},
			{Name: "lens-2", Status: "approved"},
			{Name: "lens-3", Status: "approved"},
		},
	})
	if err != nil {
		t.Fatalf("mergeResults: %v", err)
	}
	if out.MergedStatus != "Approved" {
		t.Errorf("MergedStatus = %q, want %q", out.MergedStatus, "Approved")
	}
}

// TestPlanMergeResults_LensMergeMixed verifies the lenses-only call pattern:
// when any lens does not report Status "approved", MergedStatus is
// "Issues Found" (SKILL.md Step 5: Approved only when every lens approved).
func TestPlanMergeResults_LensMergeMixed(t *testing.T) {
	out, err := mergeResults(PlanSupportIn{
		LensResults: []LensResult{
			{Name: "lens-1", Status: "approved"},
			{Name: "lens-2", Status: "rejected"},
			{Name: "lens-3", Status: "approved"},
		},
	})
	if err != nil {
		t.Fatalf("mergeResults: %v", err)
	}
	if out.MergedStatus != "Issues Found" {
		t.Errorf("MergedStatus = %q, want %q", out.MergedStatus, "Issues Found")
	}
}

// TestPlanMergeResults_LensStatusAsWritten verifies merge_results reads the
// lens status the way the lens prompts write it: "**Status:** Approved" or
// "**Status:** Issues Found". The compare ignores letter case and outer
// spaces, so "Approved" counts as approved.
func TestPlanMergeResults_LensStatusAsWritten(t *testing.T) {
	t.Run("all Approved", func(t *testing.T) {
		out := mergeCall(t, PlanSupportIn{
			LensResults: []LensResult{
				{Name: "architecture", Status: "Approved"},
				{Name: "requirements", Status: " APPROVED "},
				{Name: "risk", Status: "approved"},
			},
		})
		if out.MergedStatus != "Approved" {
			t.Errorf("MergedStatus = %q, want %q", out.MergedStatus, "Approved")
		}
	})
	t.Run("one Issues Found", func(t *testing.T) {
		out := mergeCall(t, PlanSupportIn{
			LensResults: []LensResult{
				{Name: "architecture", Status: "Approved"},
				{Name: "risk", Status: "Issues Found", Issues: []Issue{
					{Severity: "blocking", Summary: "Task 2: wrong file path"},
				}},
			},
		})
		if out.MergedStatus != "Issues Found" {
			t.Errorf("MergedStatus = %q, want %q", out.MergedStatus, "Issues Found")
		}
	})
}

// TestPlanMergeResults_Redispatch verifies isRedispatch=true downgrades G17
// findings to advisory-only, even when submitted as blocking.
func TestPlanMergeResults_Redispatch(t *testing.T) {
	out, err := mergeResults(PlanSupportIn{
		LaneResults: []LaneResult{
			{Name: "g17-lane", Status: "pass", GateIDs: []string{"G17"}, Issues: []Issue{
				{GateID: "G17", Severity: "blocking", Summary: "G17 finding submitted as blocking"},
			}},
		},
		IsRedispatch: true,
	})
	if err != nil {
		t.Fatalf("mergeResults: %v", err)
	}
	if len(out.AllIssues) != 1 {
		t.Fatalf("AllIssues = %+v, want exactly 1", out.AllIssues)
	}
	if out.AllIssues[0].Severity != "advisory" {
		t.Errorf("AllIssues[0].Severity = %q, want %q (isRedispatch downgrades G17 findings)", out.AllIssues[0].Severity, "advisory")
	}
	if out.MergedStatus != "Approved" {
		t.Errorf("MergedStatus = %q, want %q (no blocking issues remain after G17 downgrade)", out.MergedStatus, "Approved")
	}
}

// TestPlanMergeResults_RejectsUnknownEnums verifies that an unmapped lane
// status or issue severity is a DomainError naming the item and the allowed
// values. Before the fix, status "ok" counted as a pass and severity "error"
// as advisory, so a caller's mapping mistake hid findings silently.
func TestPlanMergeResults_RejectsUnknownEnums(t *testing.T) {
	cases := []struct {
		name string
		in   PlanSupportIn
		want []string // substrings the error message must contain
	}{
		{
			name: "lane status",
			in: PlanSupportIn{LaneResults: []LaneResult{
				{Name: "static-structural", Status: "ok", GateIDs: []string{"G1"}},
			}},
			want: []string{`laneResults[0] ("static-structural")`, `status "ok"`, `"pass"`, `"fail"`},
		},
		{
			name: "empty lane status",
			in: PlanSupportIn{LaneResults: []LaneResult{
				{Name: "lane-a", Status: "pass", GateIDs: []string{"G1"}},
				{Name: "lane-b", GateIDs: []string{"G2"}},
			}},
			want: []string{`laneResults[1] ("lane-b")`, `status ""`},
		},
		{
			name: "lane issue severity",
			in: PlanSupportIn{LaneResults: []LaneResult{
				{Name: "static-structural", Status: "pass", GateIDs: []string{"G1"}, Issues: []Issue{
					{GateID: "G1", Severity: "error", Summary: "Task 3: Depends on missing Task 9"},
				}},
			}},
			want: []string{`laneResults[0].issues[0] ("Task 3: Depends on missing Task 9")`, `severity "error"`, `"blocking"`, `"advisory"`},
		},
		{
			name: "lens issue severity",
			in: PlanSupportIn{LensResults: []LensResult{
				{Name: "risk", Status: "Approved", Issues: []Issue{{Summary: "No rollback step"}}},
			}},
			want: []string{`lensResults[0].issues[0] ("No rollback step")`, `severity ""`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Action = "merge_results"
			out, err := planSupportCore("", "", tc.in)
			if err == nil {
				t.Fatalf("merge_results succeeded with %+v, want DomainError", out)
			}
			if got := errorClassOf(err); got != "domain" {
				t.Fatalf("error class = %q, want domain (err: %v)", got, err)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err.Error(), w)
				}
			}
			if suggestionOf(err) == "" {
				t.Error("DomainError has an empty Suggestion")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// plan_support material_snapshot / material_compare tests
//
// materialSnapshot and materialCompare (internal/tools/plan_support.go) read
// a plan markdown file via the fsseam (mkdirTempFunc/writeFileFunc/
// readFileFunc, internal/tools/fsseam.go), so each test below seeds a
// fixture plan into installFakeFS's in-memory map rather than the real
// filesystem, and rather than calling a pure in-memory function directly.
//
// Fixture construction note: every task fixture below places **Contract:**
// as the LAST field in its body. snapshotPlan ends a **Contract:** block at
// the next "**<Field>:**" line, so field order does not matter;
// TestPlanMaterialChange_FieldAfterContract pins that.
// ---------------------------------------------------------------------------

const materialHeader = `**Goal:** Build the thing
**Architecture:** Some arch
**Source:** Some source
**Verification:** Some verification

`

const materialTask1 = `### Task 1: First task
**Complexity:** Standard
**Risk:** Low
**Depends on:** none
**Verify:** tests

**Files:**
- internal/tools/foo.go
- internal/tools/foo_test.go

**Acceptance criteria:**
- [ ] it works

**openspec-task:**
- ref: add-foo-feature/tasks.md#1

**Contract:**
- shape: does X
- names: Foo
- mirror: existing pattern in bar.go
- decisions: none
- sync: none

`

const materialTask2 = `### Task 2: Second task
**Complexity:** Trivial
**Risk:** Low
**Depends on:** Task 1
**Verify:** build, lint

**Files:**
- internal/tools/bar.go

**Notes:**
- second task rationale line

**Acceptance criteria:**
- [ ] works too

**openspec-task:**
- ref: add-foo-feature/tasks.md#2

**Contract:**
- shape: does Y
- names: Bar
- mirror: none
- decisions: none
- sync: none

`

const materialTask3 = `### Task 3: Third task
**Complexity:** Trivial
**Risk:** Low
**Verify:** tests

**Acceptance criteria:**
- [ ] third works

`

const materialTask4 = `### Task 4: Fourth task
**Complexity:** Trivial
**Risk:** Low
**Verify:** tests

**Acceptance criteria:**
- [ ] fourth works

`

const materialTask5 = `### Task 5: Fifth task
**Complexity:** Trivial
**Risk:** Low
**Verify:** tests

**Acceptance criteria:**
- [ ] fifth works

`

const materialTail = `## Deviations & assumptions

| Area | Note |
|---|---|
| Task 2 verify | Uses build+lint instead of tests |

## Key Decisions

- **Use bullet format for decisions** — chosen for simplicity across tasks.

## Verification Scorecard

All good.
`

// materialBasePlan returns the 4-task fixture plan used as the "before"
// state for every material_compare test below. Task 3 and Task 4 carry no
// Files/Contract/Depends-on/openspec-task fields so adding/removing a task
// can be tested without incidentally touching those other dimensions.
func materialBasePlan() string {
	return materialHeader + materialTask1 + materialTask2 + materialTask3 + materialTask4 + materialTail
}

// fakeFS is a minimal in-memory filesystem substituted for the fsseam vars
// (mkdirTempFunc, writeFileFunc, readFileFunc) in tests, per the
// no-real-fs-git-in-tests guardrail and this task's AC1: no test below calls
// os.MkdirTemp, os.WriteFile, os.ReadFile, or t.TempDir. Its map is the
// shared "disk" a snapshot-then-compare two-call scenario reads and writes
// against, entirely in memory.
type fakeFS struct {
	mu       sync.Mutex
	files    map[string][]byte
	tmpCount int
}

func newFakeFS() *fakeFS {
	return &fakeFS{files: map[string][]byte{}}
}

func (f *fakeFS) mkdirTemp(_, pattern string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tmpCount++
	return fmt.Sprintf("/fake-tmp/%s%d", strings.TrimSuffix(pattern, "*"), f.tmpCount), nil
}

func (f *fakeFS) writeFile(path string, data []byte, _ os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = append([]byte(nil), data...)
	return nil
}

func (f *fakeFS) readFile(path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.files[path]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
	return append([]byte(nil), data...), nil
}

// put seeds path directly into the fake's map, standing in for a fixture
// plan/snapshot file the tool under test will read back via readFileFunc.
func (f *fakeFS) put(path, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = []byte(content)
}

// installFakeFS points mkdirTempFunc, writeFileFunc and readFileFunc at a
// fresh fakeFS for the duration of t, restoring the real os.* functions on
// cleanup, and returns the fake so the test can seed files directly.
func installFakeFS(t *testing.T) *fakeFS {
	t.Helper()
	fake := newFakeFS()
	origMkdirTemp, origWriteFile, origReadFile := mkdirTempFunc, writeFileFunc, readFileFunc
	mkdirTempFunc = fake.mkdirTemp
	writeFileFunc = fake.writeFile
	readFileFunc = fake.readFile
	t.Cleanup(func() {
		mkdirTempFunc = origMkdirTemp
		writeFileFunc = origWriteFile
		readFileFunc = origReadFile
	})
	return fake
}

// snapshotPathOf seeds content at a virtual path in fake and returns the
// snapshotPath from the material_snapshot action.
func snapshotPathOf(t *testing.T, fake *fakeFS, name, content string) string {
	t.Helper()
	path := "/fake-plans/" + name
	fake.put(path, content)
	out, err := materialSnapshot(PlanSupportIn{FilePath: path})
	if err != nil {
		t.Fatalf("materialSnapshot: %v", err)
	}
	if out.SnapshotPath == "" {
		t.Fatalf("materialSnapshot returned empty SnapshotPath")
	}
	return out.SnapshotPath
}

// readSnapshotFile reads and decodes the snapshot file at path via the
// fsseam, for tests that assert on the snapshot's structural content
// directly.
func readSnapshotFile(t *testing.T, path string) PlanSnapshot {
	t.Helper()
	raw, err := readFileFunc(path)
	if err != nil {
		t.Fatalf("read snapshot file %q: %v", path, err)
	}
	var snap PlanSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("decode snapshot file %q: %v", path, err)
	}
	return snap
}

// materialCompareCheck snapshots `before`, seeds `after` at its own virtual
// path in the same fake, and returns the material_compare result comparing
// them.
func materialCompareCheck(t *testing.T, before, after string) PlanSupportOut {
	t.Helper()
	fake := installFakeFS(t)
	snapshotPath := snapshotPathOf(t, fake, "before.md", before)

	afterPath := "/fake-plans/after.md"
	fake.put(afterPath, after)

	out, err := materialCompare(PlanSupportIn{FilePath: afterPath, SnapshotPath: snapshotPath})
	if err != nil {
		t.Fatalf("materialCompare: %v", err)
	}
	return out
}

// assertNoMaterialChange asserts Material=false and an empty Triggers slice.
func assertNoMaterialChange(t *testing.T, out PlanSupportOut) {
	t.Helper()
	if out.Material {
		t.Errorf("Material = true, want false; Triggers = %v", out.Triggers)
	}
	if len(out.Triggers) != 0 {
		t.Errorf("Triggers = %v, want empty", out.Triggers)
	}
}

// assertSingleTrigger asserts Material=true and Triggers contains exactly
// one entry equal to want.
func assertSingleTrigger(t *testing.T, out PlanSupportOut, want string) {
	t.Helper()
	if !out.Material {
		t.Fatalf("Material = false, want true (expected trigger %q)", want)
	}
	if len(out.Triggers) != 1 || out.Triggers[0] != want {
		t.Fatalf("Triggers = %v, want exactly [%q]", out.Triggers, want)
	}
}

// TestPlanMaterialChange_Snapshot verifies material_snapshot populates all 7
// structural dimensions of PlanSnapshot from a fixture plan (Contract row
// `_Snapshot`).
func TestPlanMaterialChange_Snapshot(t *testing.T) {
	fake := installFakeFS(t)
	snapshotPath := snapshotPathOf(t, fake, "plan.md", materialBasePlan())
	snap := readSnapshotFile(t, snapshotPath)

	if snap.TaskCount != 4 {
		t.Errorf("TaskCount = %d, want 4", snap.TaskCount)
	}
	if len(snap.DeviationsRows) != 1 || snap.DeviationsRows[0] != "Task 2 verify" {
		t.Errorf("DeviationsRows = %v, want [Task 2 verify]", snap.DeviationsRows)
	}
	if len(snap.FilesSet) != 2 {
		t.Errorf("FilesSet = %v, want 2 entries (Task 1, Task 2)", snap.FilesSet)
	}
	if len(snap.FilesSet["Task 1"]) != 2 {
		t.Errorf("FilesSet[Task 1] = %v, want 2 paths", snap.FilesSet["Task 1"])
	}
	if len(snap.FilesSet["Task 2"]) != 1 {
		t.Errorf("FilesSet[Task 2] = %v, want 1 path", snap.FilesSet["Task 2"])
	}
	if len(snap.Contracts) != 2 {
		t.Errorf("Contracts = %v, want 2 entries (Task 1, Task 2)", snap.Contracts)
	}
	if !strings.Contains(snap.Contracts["Task 1"], "shape: does X") {
		t.Errorf("Contracts[Task 1] = %q, want to contain %q", snap.Contracts["Task 1"], "shape: does X")
	}
	if snap.DependsOn["Task 1"] != "none" {
		t.Errorf("DependsOn[Task 1] = %q, want %q", snap.DependsOn["Task 1"], "none")
	}
	if snap.DependsOn["Task 2"] != "Task 1" {
		t.Errorf("DependsOn[Task 2] = %q, want %q", snap.DependsOn["Task 2"], "Task 1")
	}
	if len(snap.KeyDecisions) != 1 || snap.KeyDecisions[0] != "Use bullet format for decisions" {
		t.Errorf("KeyDecisions = %v, want [Use bullet format for decisions]", snap.KeyDecisions)
	}
	if snap.OpenspecTaskMapping["Task 1"] != "add-foo-feature/tasks.md#1" {
		t.Errorf("OpenspecTaskMapping[Task 1] = %q, want %q", snap.OpenspecTaskMapping["Task 1"], "add-foo-feature/tasks.md#1")
	}
	if snap.OpenspecTaskMapping["Task 2"] != "add-foo-feature/tasks.md#2" {
		t.Errorf("OpenspecTaskMapping[Task 2] = %q, want %q", snap.OpenspecTaskMapping["Task 2"], "add-foo-feature/tasks.md#2")
	}
}

// TestPlanMaterialCompare_SamePathTwoCall is the literal two-call scenario
// from this task's acceptance criteria: material_snapshot(filePath=P) then,
// after P is mutated in place, material_compare(filePath=P, snapshotPath=
// <returned>) must report exactly that mutation and nothing else. Both
// calls read/write the SAME virtual path P in the fake's map, proving the
// map is the shared "disk" between the two calls -- not two independent
// fixtures like materialCompareCheck's before.md/after.md.
func TestPlanMaterialCompare_SamePathTwoCall(t *testing.T) {
	fake := installFakeFS(t)
	const planPath = "/fake-plans/plan.md"

	fake.put(planPath, materialBasePlan())
	snapOut, err := materialSnapshot(PlanSupportIn{FilePath: planPath})
	if err != nil {
		t.Fatalf("materialSnapshot: %v", err)
	}

	// Mutate P in place: add a new task.
	fake.put(planPath, materialBasePlan()+materialTask5)

	out, err := materialCompare(PlanSupportIn{FilePath: planPath, SnapshotPath: snapOut.SnapshotPath})
	if err != nil {
		t.Fatalf("materialCompare: %v", err)
	}
	assertSingleTrigger(t, out, "Task count changed: 4 -> 5")
}

// TestPlanMaterialChange_NoChange verifies comparing a plan against an
// identical copy of itself reports no material change (Contract row
// `_NoChange`).
func TestPlanMaterialChange_NoChange(t *testing.T) {
	out := materialCompareCheck(t, materialBasePlan(), materialBasePlan())
	assertNoMaterialChange(t, out)
}

// TestPlanMaterialChange_TaskAdded verifies adding a new "### Task 5:"
// section fires only the TaskCount trigger (Contract row `_TaskAdded`).
func TestPlanMaterialChange_TaskAdded(t *testing.T) {
	after := materialHeader + materialTask1 + materialTask2 + materialTask3 + materialTask4 + materialTask5 + materialTail
	out := materialCompareCheck(t, materialBasePlan(), after)
	assertSingleTrigger(t, out, "Task count changed: 4 -> 5")
}

// TestPlanMaterialChange_TaskRemoved verifies removing "### Task 3:" fires
// only the TaskCount trigger (Contract row `_TaskRemoved`).
func TestPlanMaterialChange_TaskRemoved(t *testing.T) {
	after := materialHeader + materialTask1 + materialTask2 + materialTask4 + materialTail
	out := materialCompareCheck(t, materialBasePlan(), after)
	assertSingleTrigger(t, out, "Task count changed: 4 -> 3")
}

// TestPlanMaterialChange_FilesChanged verifies changing a task's **Files:**
// bullet path fires only the FilesSet trigger (Contract row `_FilesChanged`).
func TestPlanMaterialChange_FilesChanged(t *testing.T) {
	changedTask1 := strings.Replace(materialTask1, "- internal/tools/foo.go\n", "- internal/tools/foo-renamed.go\n", 1)
	after := materialHeader + changedTask1 + materialTask2 + materialTask3 + materialTask4 + materialTail
	out := materialCompareCheck(t, materialBasePlan(), after)
	assertSingleTrigger(t, out, "Files changed in: Task 1")
}

// TestPlanMaterialChange_ContractChanged verifies changing a task's
// **Contract:** block text fires only the Contracts trigger (Contract row
// `_ContractChanged`).
func TestPlanMaterialChange_ContractChanged(t *testing.T) {
	changedTask1 := strings.Replace(materialTask1, "- shape: does X\n", "- shape: does X, revised\n", 1)
	after := materialHeader + changedTask1 + materialTask2 + materialTask3 + materialTask4 + materialTail
	out := materialCompareCheck(t, materialBasePlan(), after)
	assertSingleTrigger(t, out, "Contract changed in: Task 1")
}

// TestPlanMaterialChange_DependsChanged verifies changing a task's
// **Depends on:** field fires only the DependsOn trigger (Contract row
// `_DependsChanged`).
func TestPlanMaterialChange_DependsChanged(t *testing.T) {
	changedTask2 := strings.Replace(materialTask2, "**Depends on:** Task 1\n", "**Depends on:** Task 1, Task 4\n", 1)
	after := materialHeader + materialTask1 + changedTask2 + materialTask3 + materialTask4 + materialTail
	out := materialCompareCheck(t, materialBasePlan(), after)
	assertSingleTrigger(t, out, "Depends on changed in: Task 2")
}

// TestPlanMaterialChange_KeyDecisionChanged verifies adding a new "## Key
// Decisions" entry fires only the KeyDecisions trigger (Contract row
// `_KeyDecisionChanged`).
func TestPlanMaterialChange_KeyDecisionChanged(t *testing.T) {
	changedTail := strings.Replace(materialTail,
		"- **Use bullet format for decisions** — chosen for simplicity across tasks.\n",
		"- **Use bullet format for decisions** — chosen for simplicity across tasks.\n- **Second decision entry** — added for the test.\n",
		1)
	after := materialHeader + materialTask1 + materialTask2 + materialTask3 + materialTask4 + changedTail
	out := materialCompareCheck(t, materialBasePlan(), after)
	assertSingleTrigger(t, out, "Key Decisions modified")
}

// TestPlanMaterialChange_WordingOnly verifies changing prose in a task's
// **Notes:** block (a field PlanSnapshot does not track) does NOT trigger a
// material change (Contract row `_WordingOnly`).
func TestPlanMaterialChange_WordingOnly(t *testing.T) {
	changedTask2 := strings.Replace(materialTask2, "- second task rationale line\n", "- an updated rationale sentence, still just prose\n", 1)
	after := materialHeader + materialTask1 + changedTask2 + materialTask3 + materialTask4 + materialTail
	out := materialCompareCheck(t, materialBasePlan(), after)
	assertNoMaterialChange(t, out)
}

// materialTaskContractFirst is a task whose **Contract:** comes before its
// **Notes:** and **Files:** fields, as plan-format-reference.md allows.
const materialTaskContractFirst = `### Task 1: Contract first
**Complexity:** Standard
**Risk:** Low
**Verify:** tests

**Contract:**
- shape: does X
- names: Foo

**Notes:**
- first rationale line

**Files:**
- internal/tools/foo.go

**Acceptance criteria:**
- [ ] it works

`

// TestPlanMaterialChange_FieldAfterContract verifies the **Contract:** block
// ends at the next **<Field>:** line: edits to fields placed after it do not
// count as a contract change.
func TestPlanMaterialChange_FieldAfterContract(t *testing.T) {
	before := materialHeader + materialTaskContractFirst + materialTail

	fake := installFakeFS(t)
	snap := readSnapshotFile(t, snapshotPathOf(t, fake, "contract-first.md", before))
	if got := snap.Contracts["Task 1"]; got != "- shape: does X\n- names: Foo" {
		t.Errorf("Contracts[Task 1] = %q, want only the contract bullets", got)
	}

	notesEdited := strings.Replace(before, "- first rationale line\n", "- reworded rationale line\n", 1)
	assertNoMaterialChange(t, materialCompareCheck(t, before, notesEdited))

	filesEdited := strings.Replace(before, "- internal/tools/foo.go\n", "- internal/tools/bar.go\n", 1)
	assertSingleTrigger(t, materialCompareCheck(t, before, filesEdited), "Files changed in: Task 1")
}

// TestPlanMaterialChange_DeviationsRowChanged verifies adding a new row to
// the "## Deviations & assumptions" table fires a material change. Extra
// coverage beyond the Contract's 9-row matrix, per Acceptance Criteria:
// "deviations row changed".
func TestPlanMaterialChange_DeviationsRowChanged(t *testing.T) {
	changedTail := strings.Replace(materialTail,
		"| Task 2 verify | Uses build+lint instead of tests |\n",
		"| Task 2 verify | Uses build+lint instead of tests |\n| Task 4 test | Manual verification only |\n",
		1)
	after := materialHeader + materialTask1 + materialTask2 + materialTask3 + materialTask4 + changedTail
	out := materialCompareCheck(t, materialBasePlan(), after)
	assertSingleTrigger(t, out, "Deviations & assumptions table modified")
}

// TestPlanMaterialChange_OpenspecMappingChanged verifies changing a task's
// **openspec-task:** ref value fires a material change. Extra coverage
// beyond the Contract's 9-row matrix, per Acceptance Criteria: "openspec-task
// mapping change triggers material change".
func TestPlanMaterialChange_OpenspecMappingChanged(t *testing.T) {
	changedTask1 := strings.Replace(materialTask1, "- ref: add-foo-feature/tasks.md#1\n", "- ref: add-foo-feature/tasks.md#1-updated\n", 1)
	after := materialHeader + changedTask1 + materialTask2 + materialTask3 + materialTask4 + materialTail
	out := materialCompareCheck(t, materialBasePlan(), after)
	assertSingleTrigger(t, out, "OpenSpec task mapping changed in: Task 1")
}

// ---------------------------------------------------------------------------
// plan_support material_snapshot / material_compare fsseam & validation
// tests
//
// material_snapshot now writes the snapshot to disk via fsseam.go instead of
// returning it verbatim, and material_compare reads it back by path. These
// tests cover: (1) material_snapshot's own write failure path, soft-wrapped
// as an InfraError, and (2)-(6) the 5 distinct ways a caller-supplied
// snapshotPath can fail to be a usable snapshot in material_compare — empty,
// missing, unreadable for another reason, not JSON, and well-formed JSON that
// isn't a PlanSnapshot. Every recovery text either warns that the pre-edit
// baseline is lost or limits re-snapshotting to an unedited plan, because
// re-snapshotting after the plan rewrite would report material:false and skip
// the R64 gate.
//
// A snapshot that decodes cleanly is never rejected for being empty — see
// TestPlanMaterialCompare_ZeroTaskSnapshotRoundTrips.
// ---------------------------------------------------------------------------

// TestPlanMaterialSnapshot_MkdirTempFailure verifies that when the fsseam's
// mkdirTempFunc fails, material_snapshot surfaces an error instead of
// silently returning an empty SnapshotPath. The sibling writeFileFunc
// failure is covered by TestPlanMaterialErrorPaths/"snapshot file write
// failure" — this test only fails the temp-dir step.
func TestPlanMaterialSnapshot_MkdirTempFailure(t *testing.T) {
	fake := installFakeFS(t)
	path := "/fake-plans/plan.md"
	fake.put(path, materialBasePlan())

	origMkdirTemp := mkdirTempFunc
	mkdirTempFunc = func(string, string) (string, error) {
		return "", fmt.Errorf("simulated mkdir failure")
	}
	defer func() { mkdirTempFunc = origMkdirTemp }()

	_, err := materialSnapshot(PlanSupportIn{FilePath: path})
	if err == nil {
		t.Fatal("expected error when mkdirTempFunc fails")
	}
	if !strings.Contains(err.Error(), "simulated mkdir failure") {
		t.Errorf("error = %q, want it to wrap the underlying mkdir failure", err.Error())
	}
}

// TestPlanMaterialSnapshot_WriteFailureRemovesTempDir runs the failed snapshot
// write against a real temp root: the sdlc-plan-snapshot-* dir that was created
// must be removed, and the error must keep its InfraError shape.
func TestPlanMaterialSnapshot_WriteFailureRemovesTempDir(t *testing.T) {
	root := redirectTempManifests(t)
	planPath := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(planPath, []byte(materialBasePlan()), 0o644); err != nil {
		t.Fatal(err)
	}

	origWriteFile := writeFileFunc
	writeFileFunc = func(string, []byte, os.FileMode) error { return os.ErrPermission }
	t.Cleanup(func() { writeFileFunc = origWriteFile })

	_, err := materialSnapshot(PlanSupportIn{FilePath: planPath})
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("error = %T (%v), want *mcpserver.InfraError", err, err)
	}
	if !strings.Contains(ie.Msg, "write snapshot file") {
		t.Errorf("Msg = %q, want it to name the failed snapshot file write", ie.Msg)
	}
	if !strings.Contains(ie.Suggestion, "material_snapshot") {
		t.Errorf("Suggestion = %q, want it to name the call to retry", ie.Suggestion)
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Errorf("error does not wrap the write failure: %v", err)
	}
	if left := tempEntries(t, root); len(left) != 0 {
		t.Errorf("temp root still holds %v after a failed snapshot write, want it empty", left)
	}
}

// TestWriteTempJSON_FailureRemovesDir verifies every failure after the temp
// dir exists removes it, so a failed write does not leak an empty sdlc-* dir.
func TestWriteTempJSON_FailureRemovesDir(t *testing.T) {
	cases := []struct {
		name     string
		payload  any
		writeErr error
		wantMsg  string
	}{
		{"file write fails", map[string]string{"k": "v"}, os.ErrPermission, "write thing file"},
		{"marshal fails", func() {}, nil, "marshal thing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := redirectTempManifests(t)
			if tc.writeErr != nil {
				origWriteFile := writeFileFunc
				writeFileFunc = func(string, []byte, os.FileMode) error { return tc.writeErr }
				t.Cleanup(func() { writeFileFunc = origWriteFile })
			}

			path, err := writeTempJSON("sdlc-test-", "thing", func(string) any { return tc.payload })
			if err == nil {
				t.Fatal("expected an error")
			}
			if path != "" {
				t.Errorf("path = %q, want empty on failure", path)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantMsg)
			}
			if tc.writeErr != nil && !errors.Is(err, tc.writeErr) {
				t.Errorf("error does not wrap %v: %v", tc.writeErr, err)
			}
			if left := tempEntries(t, root); len(left) != 0 {
				t.Errorf("temp root still holds %v after a failed write, want it empty", left)
			}
		})
	}
}

// TestWriteTempJSON_SuccessLeavesReadableFile verifies a successful write
// keeps its dir (the calling agent reads the file later), hands payload the
// same path it returns, and leaves valid JSON there.
func TestWriteTempJSON_SuccessLeavesReadableFile(t *testing.T) {
	root := redirectTempManifests(t)

	var seen string
	path, err := writeTempJSON("sdlc-test-", "thing", func(p string) any {
		seen = p
		return map[string]string{"selfPath": p}
	})
	if err != nil {
		t.Fatalf("writeTempJSON: %v", err)
	}
	if seen != path {
		t.Errorf("payload saw path %q, want the returned path %q", seen, path)
	}
	if filepath.Base(path) != "thing.json" || filepath.Dir(filepath.Dir(path)) != root {
		t.Errorf("path = %q, want <root>/sdlc-test-*/thing.json under %q", path, root)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode %q: %v", path, err)
	}
	if got["selfPath"] != path {
		t.Errorf("selfPath = %q, want %q", got["selfPath"], path)
	}

	left := tempEntries(t, root)
	if len(left) != 1 || !strings.HasPrefix(left[0], "sdlc-test-") {
		t.Errorf("temp root holds %v, want exactly one sdlc-test-* dir", left)
	}
}

// TestPlanMaterialCompare_EmptySnapshotPath verifies an empty snapshotPath is
// rejected before any file I/O.
func TestPlanMaterialCompare_EmptySnapshotPath(t *testing.T) {
	fake := installFakeFS(t)
	planPath := "/fake-plans/plan.md"
	fake.put(planPath, materialBasePlan())

	_, err := materialCompare(PlanSupportIn{FilePath: planPath, SnapshotPath: ""})
	if err == nil {
		t.Fatal("expected error for empty snapshotPath")
	}
	if !strings.Contains(err.Error(), "snapshotPath") {
		t.Errorf("error = %q, want it to mention snapshotPath", err.Error())
	}
	if !strings.Contains(suggestionOf(err), "material_snapshot") {
		t.Errorf("Suggestion = %q, want it to name the material_snapshot call to run", suggestionOf(err))
	}
}

// TestPlanMaterialCompare_UnreadableSnapshotPath verifies a snapshotPath
// that cannot be read (here: does not exist) is rejected with an error
// pointing back at material_snapshot.
func TestPlanMaterialCompare_UnreadableSnapshotPath(t *testing.T) {
	fake := installFakeFS(t)
	planPath := "/fake-plans/plan.md"
	fake.put(planPath, materialBasePlan())

	_, err := materialCompare(PlanSupportIn{
		FilePath:     planPath,
		SnapshotPath: "/fake-plans/does-not-exist.json",
	})
	if err == nil {
		t.Fatal("expected error for unreadable snapshotPath")
	}
	if !strings.Contains(suggestionOf(err), "material_snapshot") {
		t.Errorf("Suggestion = %q, want it to point back at material_snapshot", suggestionOf(err))
	}
}

// TestPlanMaterialCompare_SnapshotReadFailure verifies readPlanSnapshot words
// a missing snapshot file differently from any other read failure, because
// the next step differs: a missing file may be regenerated (only while the
// plan is unedited), an unreadable one needs its permissions fixed. The
// missing-file case uses a real path in t.TempDir; the seam only forces the
// permission failure.
func TestPlanMaterialCompare_SnapshotReadFailure(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(planPath, []byte(materialBasePlan()), 0o644); err != nil {
		t.Fatal(err)
	}
	missingPath := filepath.Join(dir, "gone", "snapshot.json")
	deniedPath := filepath.Join(dir, "denied.json")

	origRead := readFileFunc
	readFileFunc = func(p string) ([]byte, error) {
		if p == deniedPath {
			return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrPermission}
		}
		return origRead(p)
	}
	t.Cleanup(func() { readFileFunc = origRead })

	infraErr := func(t *testing.T, snapshotPath string) *mcpserver.InfraError {
		t.Helper()
		_, err := materialCompare(PlanSupportIn{FilePath: planPath, SnapshotPath: snapshotPath})
		var ie *mcpserver.InfraError
		if !errors.As(err, &ie) {
			t.Fatalf("error = %T (%v), want *mcpserver.InfraError", err, err)
		}
		return ie
	}

	missing := infraErr(t, missingPath)
	if !errors.Is(missing, fs.ErrNotExist) {
		t.Errorf("missing-file error does not wrap fs.ErrNotExist: %v", missing)
	}
	for _, want := range []string{"snapshotPath", missingPath, "missing"} {
		if !strings.Contains(missing.Msg, want) {
			t.Errorf("missing-file Msg = %q, want it to contain %q", missing.Msg, want)
		}
	}
	for _, want := range []string{`action="material_snapshot"`, "new snapshotPath", "re-snapshot"} {
		if !strings.Contains(missing.Suggestion, want) {
			t.Errorf("missing-file Suggestion = %q, want it to contain %q", missing.Suggestion, want)
		}
	}

	denied := infraErr(t, deniedPath)
	if !errors.Is(denied, fs.ErrPermission) {
		t.Errorf("permission error does not wrap fs.ErrPermission: %v", denied)
	}
	for _, want := range []string{deniedPath, "permission denied"} {
		if !strings.Contains(denied.Msg, want) {
			t.Errorf("permission Msg = %q, want it to contain %q", denied.Msg, want)
		}
	}
	for _, want := range []string{deniedPath, "readable", "material_compare"} {
		if !strings.Contains(denied.Suggestion, want) {
			t.Errorf("permission Suggestion = %q, want it to contain %q", denied.Suggestion, want)
		}
	}
	if strings.Contains(denied.Suggestion, "new snapshotPath") {
		t.Errorf("permission Suggestion = %q, must not tell the caller to make a new snapshot", denied.Suggestion)
	}

	if missing.Msg == denied.Msg || missing.Suggestion == denied.Suggestion {
		t.Errorf("missing and permission failures share text:\n  missing: %q / %q\n  denied:  %q / %q",
			missing.Msg, missing.Suggestion, denied.Msg, denied.Suggestion)
	}
}

// TestPlanMaterialCompare_BothPathsUnreadable pins the read order: the plan
// file is read before the snapshot, so when both paths are bad the caller
// sees the plan-file error and fixes filePath first.
func TestPlanMaterialCompare_BothPathsUnreadable(t *testing.T) {
	installFakeFS(t)

	_, err := materialCompare(PlanSupportIn{
		FilePath:     "/fake-plans/missing-plan.md",
		SnapshotPath: "/fake-plans/missing-snapshot.json",
	})
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("error = %T (%v), want *mcpserver.InfraError", err, err)
	}
	if !strings.Contains(ie.Msg, "read plan file") || !strings.Contains(ie.Msg, "missing-plan.md") {
		t.Errorf("Msg = %q, want the plan-file read error", ie.Msg)
	}
	if strings.Contains(ie.Msg, "snapshot") {
		t.Errorf("Msg = %q, must not mention the snapshot when the plan file is unreadable", ie.Msg)
	}
	if !strings.Contains(ie.Suggestion, "corrected filePath") {
		t.Errorf("Suggestion = %q, want it to point at filePath", ie.Suggestion)
	}
}

// TestPlanMaterialCompare_NonJSONSnapshotContent verifies a snapshotPath
// pointing at non-JSON content is rejected.
func TestPlanMaterialCompare_NonJSONSnapshotContent(t *testing.T) {
	fake := installFakeFS(t)
	planPath := "/fake-plans/plan.md"
	fake.put(planPath, materialBasePlan())

	snapshotPath := "/fake-plans/snapshot.json"
	fake.put(snapshotPath, "this is not json")

	_, err := materialCompare(PlanSupportIn{FilePath: planPath, SnapshotPath: snapshotPath})
	if err == nil {
		t.Fatal("expected error for non-JSON snapshot content")
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("error = %q, want it to mention invalid JSON", err.Error())
	}
	if !strings.Contains(suggestionOf(err), "material_snapshot") {
		t.Errorf("Suggestion = %q, want it to point back at material_snapshot", suggestionOf(err))
	}
}

// TestPlanMaterialCompare_WellFormedNonSnapshotJSON verifies a snapshotPath
// pointing at well-formed JSON that isn't a PlanSnapshot (missing the
// required taskCount field) is rejected rather than silently decoded as a
// zero-value snapshot.
func TestPlanMaterialCompare_WellFormedNonSnapshotJSON(t *testing.T) {
	fake := installFakeFS(t)
	planPath := "/fake-plans/plan.md"
	fake.put(planPath, materialBasePlan())

	snapshotPath := "/fake-plans/snapshot.json"
	fake.put(snapshotPath, `{"foo":"bar"}`)

	_, err := materialCompare(PlanSupportIn{FilePath: planPath, SnapshotPath: snapshotPath})
	if err == nil {
		t.Fatal("expected error for well-formed JSON that is not a snapshot")
	}
	if !strings.Contains(err.Error(), "taskCount") {
		t.Errorf("error = %q, want it to mention the missing taskCount field", err.Error())
	}
	if !strings.Contains(suggestionOf(err), "material_snapshot") {
		t.Errorf("Suggestion = %q, want it to point back at material_snapshot", suggestionOf(err))
	}
}

// materialZeroTaskPlan returns a plan with no "### Task N:" headings but a
// populated Deviations & assumptions table and Key Decisions section. It is
// the fixture for the zero-task round trip below: snapshotPlan gives it
// TaskCount 0 and four empty maps, and every one of those maps is dropped on
// marshal by omitempty, so the snapshot file it produces carries taskCount 0
// and no filesSet/contracts/dependsOn/openspecTaskMapping keys at all.
func materialZeroTaskPlan() string {
	return materialHeader + materialTail
}

// TestPlanMaterialCompare_ZeroTaskSnapshotRoundTrips verifies material_compare
// accepts a snapshot that material_snapshot itself wrote for a plan with no
// tasks. A guard used to reject `taskCount == 0 && filesSet == nil`, which is
// precisely the shape of a legitimate zero-task snapshot after the omitempty
// maps are dropped — so the tool rejected its own output, and the R64
// re-validation gate was skipped because material_compare never returned.
//
// Both halves of the round trip are asserted: comparing the zero-task plan
// against itself reports no material change, and comparing it against a plan
// that has tasks fires the task-count trigger.
func TestPlanMaterialCompare_ZeroTaskSnapshotRoundTrips(t *testing.T) {
	t.Run("unchanged", func(t *testing.T) {
		out := materialCompareCheck(t, materialZeroTaskPlan(), materialZeroTaskPlan())
		assertNoMaterialChange(t, out)
	})

	t.Run("tasks added", func(t *testing.T) {
		out := materialCompareCheck(t, materialZeroTaskPlan(), materialBasePlan())
		if !out.Material {
			t.Fatalf("Material = false, want true; Triggers = %v", out.Triggers)
		}
		want := "Task count changed: 0 -> 4"
		found := false
		for _, trigger := range out.Triggers {
			if trigger == want {
				found = true
			}
		}
		if !found {
			t.Errorf("Triggers = %v, want one of them to be %q", out.Triggers, want)
		}
	})
}

// TestPlanMaterialCompare_MinimalZeroTaskSnapshotAccepted pins the exact
// on-disk shape the deleted guard rejected: a snapshot file holding taskCount
// 0 and nothing else. The taskCount presence probe in readPlanSnapshot is what
// separates a non-snapshot JSON document from this legitimately empty one (see
// TestPlanMaterialCompare_WellFormedNonSnapshotJSON), so no further guard is
// needed and this input must compare cleanly.
func TestPlanMaterialCompare_MinimalZeroTaskSnapshotAccepted(t *testing.T) {
	fake := installFakeFS(t)
	// materialHeader alone: no tasks, no deviations table, no key decisions,
	// so every snapshot dimension is empty and the marshalled snapshot is
	// exactly {"taskCount":0}.
	planPath := "/fake-plans/plan.md"
	fake.put(planPath, materialHeader)

	snapshotPath := "/fake-plans/snapshot.json"
	fake.put(snapshotPath, `{"taskCount":0}`)

	out, err := materialCompare(PlanSupportIn{FilePath: planPath, SnapshotPath: snapshotPath})
	if err != nil {
		t.Fatalf("materialCompare on a minimal zero-task snapshot: %v", err)
	}
	if out.Material {
		t.Errorf("Material = true, want false; Triggers = %v", out.Triggers)
	}
}

// TestPlanMaterialErrorPaths covers the material_snapshot / material_compare
// failure modes that carry recovery text but had no test: empty and
// unreadable filePath on both actions, a snapshot file write failure, and a
// snapshot document whose taskCount key is present (so the probe passes) but
// whose value makes the PlanSnapshot decode fail. Each case asserts the error
// class as well as the message, so a path silently changing class is caught.
func TestPlanMaterialErrorPaths(t *testing.T) {
	const planPath = "/fake-plans/plan.md"
	const snapshotPath = "/fake-plans/snapshot.json"

	cases := []struct {
		name      string
		setup     func(t *testing.T, fake *fakeFS)
		call      func() (PlanSupportOut, error)
		wantClass string
		wantMsg   string
	}{
		{
			name:      "snapshot empty filePath",
			call:      func() (PlanSupportOut, error) { return materialSnapshot(PlanSupportIn{}) },
			wantClass: "domain",
			wantMsg:   "requires filePath",
		},
		{
			name: "snapshot unreadable plan file",
			call: func() (PlanSupportOut, error) {
				return materialSnapshot(PlanSupportIn{FilePath: "/fake-plans/missing.md"})
			},
			wantClass: "infra",
			wantMsg:   "read plan file",
		},
		{
			name: "snapshot file write failure",
			setup: func(t *testing.T, fake *fakeFS) {
				fake.put(planPath, materialBasePlan())
				orig := writeFileFunc
				writeFileFunc = func(string, []byte, os.FileMode) error { return os.ErrPermission }
				t.Cleanup(func() { writeFileFunc = orig })
			},
			call:      func() (PlanSupportOut, error) { return materialSnapshot(PlanSupportIn{FilePath: planPath}) },
			wantClass: "infra",
			wantMsg:   "write snapshot file",
		},
		{
			name:      "compare empty filePath",
			call:      func() (PlanSupportOut, error) { return materialCompare(PlanSupportIn{SnapshotPath: snapshotPath}) },
			wantClass: "domain",
			wantMsg:   "requires filePath",
		},
		{
			name: "compare unreadable plan file",
			setup: func(t *testing.T, fake *fakeFS) {
				fake.put(snapshotPath, `{"taskCount":4}`)
			},
			call: func() (PlanSupportOut, error) {
				return materialCompare(PlanSupportIn{FilePath: "/fake-plans/missing.md", SnapshotPath: snapshotPath})
			},
			wantClass: "infra",
			wantMsg:   "read plan file",
		},
		{
			name: "compare undecodable snapshot past the taskCount probe",
			setup: func(t *testing.T, fake *fakeFS) {
				fake.put(planPath, materialBasePlan())
				fake.put(snapshotPath, `{"taskCount":"four"}`)
			},
			call: func() (PlanSupportOut, error) {
				return materialCompare(PlanSupportIn{FilePath: planPath, SnapshotPath: snapshotPath})
			},
			wantClass: "data",
			wantMsg:   "could not be decoded",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := installFakeFS(t)
			if tc.setup != nil {
				tc.setup(t, fake)
			}
			_, err := tc.call()
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := errorClassOf(err); got != tc.wantClass {
				t.Errorf("error class = %q, want %q (err: %v)", got, tc.wantClass, err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantMsg)
			}
			if suggestionOf(err) == "" {
				t.Error("Suggestion is empty, want recovery text")
			}
		})
	}
}

// oldShapeTriggers reproduces materialCompare's trigger derivation directly
// against two in-memory PlanSnapshot values — the shape callers used before
// this task replaced the inline *PlanSnapshot field with a snapshotPath —
// so it can be compared against the file-path-based (new-shape) result for
// the same fixture pair.
func oldShapeTriggers(before, after PlanSnapshot) (material bool, triggers []string) {
	if after.TaskCount != before.TaskCount {
		triggers = append(triggers, fmt.Sprintf("Task count changed: %d -> %d", before.TaskCount, after.TaskCount))
	}
	if !sortedStringSliceEqual(before.DeviationsRows, after.DeviationsRows) {
		triggers = append(triggers, "Deviations & assumptions table modified")
	}
	if diffs := diffStringSliceMaps(before.FilesSet, after.FilesSet); len(diffs) > 0 {
		triggers = append(triggers, fmt.Sprintf("Files changed in: %s", strings.Join(diffs, ", ")))
	}
	if diffs := diffStringMaps(before.Contracts, after.Contracts); len(diffs) > 0 {
		triggers = append(triggers, fmt.Sprintf("Contract changed in: %s", strings.Join(diffs, ", ")))
	}
	if diffs := diffStringMaps(before.DependsOn, after.DependsOn); len(diffs) > 0 {
		triggers = append(triggers, fmt.Sprintf("Depends on changed in: %s", strings.Join(diffs, ", ")))
	}
	if !sortedStringSliceEqual(before.KeyDecisions, after.KeyDecisions) {
		triggers = append(triggers, "Key Decisions modified")
	}
	if diffs := diffStringMaps(before.OpenspecTaskMapping, after.OpenspecTaskMapping); len(diffs) > 0 {
		triggers = append(triggers, fmt.Sprintf("OpenSpec task mapping changed in: %s", strings.Join(diffs, ", ")))
	}
	return len(triggers) > 0, triggers
}

// TestPlanMaterialCompare_OldShapeAgreesWithNewShape is the regression test
// required when material_compare moved from taking an inline *PlanSnapshot
// (old shape) to taking a snapshotPath read from disk (new shape): for the
// same before/after fixture pair, computing triggers directly from two
// in-memory PlanSnapshot values (old shape) must agree exactly with
// materialCompare's file-path-based result (new shape).
//
// Each case also pins the literal triggers it expects. oldShapeTriggers is a
// copy of the production derivation, so agreement alone would pass even if
// both copies were wrong; the literal list is the independent check. The
// second case starts from a zero-task plan, where every omitempty map is
// dropped by the JSON round trip — the input class the deleted
// "empty snapshot" guard used to reject outright.
func TestPlanMaterialCompare_OldShapeAgreesWithNewShape(t *testing.T) {
	changedTask1 := strings.Replace(materialTask1, "- internal/tools/foo.go\n", "- internal/tools/foo-renamed.go\n", 1)
	changedTask1 = strings.Replace(changedTask1, "- shape: does X\n", "- shape: does X, revised\n", 1)

	cases := []struct {
		name         string
		before       string
		after        string
		wantTriggers []string
	}{
		{
			name:   "files, contract and task count",
			before: materialBasePlan(),
			after:  materialHeader + changedTask1 + materialTask2 + materialTask3 + materialTask4 + materialTask5 + materialTail,
			wantTriggers: []string{
				"Task count changed: 4 -> 5",
				"Files changed in: Task 1",
				"Contract changed in: Task 1",
			},
		},
		{
			name:   "zero-task snapshot against the full plan",
			before: materialZeroTaskPlan(),
			after:  materialBasePlan(),
			wantTriggers: []string{
				"Task count changed: 0 -> 4",
				"Files changed in: Task 1, Task 2",
				"Contract changed in: Task 1, Task 2",
				"Depends on changed in: Task 1, Task 2",
				"OpenSpec task mapping changed in: Task 1, Task 2",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldMaterial, oldTriggers := oldShapeTriggers(snapshotPlan(tc.before), snapshotPlan(tc.after))
			newOut := materialCompareCheck(t, tc.before, tc.after)

			if oldMaterial != newOut.Material {
				t.Fatalf("old-shape Material = %v, new-shape Material = %v, want equal", oldMaterial, newOut.Material)
			}
			if !newOut.Material {
				t.Fatal("Material = false, want true for a changed fixture pair")
			}
			if !reflect.DeepEqual(oldTriggers, newOut.Triggers) {
				t.Fatalf("old-shape Triggers = %v, new-shape Triggers = %v, want equal", oldTriggers, newOut.Triggers)
			}
			if !reflect.DeepEqual(newOut.Triggers, tc.wantTriggers) {
				t.Errorf("Triggers = %v, want %v", newOut.Triggers, tc.wantTriggers)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// plan_support openspec_appendix tests
//
// openspecAppendix (internal/tools/plan_support.go) is a pure formatting
// function over PlanSupportIn's openspec_appendix fields (ChangeName,
// ProposalPath, DesignPath, SpecPaths, PlanTasks) plus real file reads for
// ProposalPath/DesignPath. Note: PlanSupportIn.PlanTasks is a flat,
// caller-formatted []string — the Go layer does not itself compute an
// OpenSpec-task-to-plan-task mapping (there is no separate "OpenSpec tasks"
// input field to match against). Any task <-> task correlation is expected
// to be pre-formatted by the caller (SKILL.md) before being passed in; these
// tests validate that openspecAppendix renders the supplied PlanTasks
// entries verbatim as a bulleted "Task mapping" list, not that it performs
// mapping logic that does not exist in this function.
// ---------------------------------------------------------------------------

// TestPlanOpenspecAppendix_Basic verifies basic appendix generation with a
// proposal file, 2 spec deltas, and plan tasks produces markdown containing
// the proposal summary, the spec delta list, and the task mapping section.
func TestPlanOpenspecAppendix_Basic(t *testing.T) {
	dir := t.TempDir()
	proposalPath := filepath.Join(dir, "proposal.md")
	if err := os.WriteFile(proposalPath, []byte("# Add Foo Feature\n\nThis proposal adds the Foo feature to support bar workflows.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := openspecAppendix(dir, PlanSupportIn{
		Action:       "openspec_appendix",
		ChangeName:   "add-foo-feature",
		ProposalPath: proposalPath,
		SpecPaths:    []string{"specs/foo/spec.md", "specs/bar/spec.md"},
		PlanTasks:    []string{"Task 1: Add foo config (openspec task 1)", "Task 3: Wire bar workflow (openspec task 2)"},
	})
	if err != nil {
		t.Fatalf("openspecAppendix: %v", err)
	}
	if !strings.Contains(out.AppendixMarkdown, "## OpenSpec Appendix") {
		t.Errorf("AppendixMarkdown missing heading: %s", out.AppendixMarkdown)
	}
	if !strings.Contains(out.AppendixMarkdown, "**Change:** add-foo-feature") {
		t.Errorf("AppendixMarkdown missing change name: %s", out.AppendixMarkdown)
	}
	if !strings.Contains(out.AppendixMarkdown, "**Proposal summary:** This proposal adds the Foo feature to support bar workflows.") {
		t.Errorf("AppendixMarkdown missing proposal summary: %s", out.AppendixMarkdown)
	}
	if !strings.Contains(out.AppendixMarkdown, "- `specs/foo/spec.md`") || !strings.Contains(out.AppendixMarkdown, "- `specs/bar/spec.md`") {
		t.Errorf("AppendixMarkdown missing spec delta bullets: %s", out.AppendixMarkdown)
	}
	if !strings.Contains(out.AppendixMarkdown, "**Task mapping:**") {
		t.Errorf("AppendixMarkdown missing task mapping heading: %s", out.AppendixMarkdown)
	}
	if !strings.Contains(out.AppendixMarkdown, "- Task 1: Add foo config (openspec task 1)") || !strings.Contains(out.AppendixMarkdown, "- Task 3: Wire bar workflow (openspec task 2)") {
		t.Errorf("AppendixMarkdown missing task mapping bullets: %s", out.AppendixMarkdown)
	}
}

// TestPlanOpenspecAppendix_NoDesign verifies a missing design.md (DesignPath
// set but the file does not exist on disk) is handled gracefully: no error,
// and the design section reports "not provided" rather than crashing or
// omitting required output.
func TestPlanOpenspecAppendix_NoDesign(t *testing.T) {
	dir := t.TempDir()
	out, err := openspecAppendix(dir, PlanSupportIn{
		Action:     "openspec_appendix",
		ChangeName: "add-foo-feature",
		DesignPath: filepath.Join(dir, "does-not-exist-design.md"),
	})
	if err != nil {
		t.Fatalf("openspecAppendix: %v", err)
	}
	if !strings.Contains(out.AppendixMarkdown, "**Design:** _(not provided)_") {
		t.Errorf("AppendixMarkdown missing graceful design-missing text: %s", out.AppendixMarkdown)
	}
}

// TestPlanOpenspecAppendix_EmptySpecs verifies an empty SpecPaths produces
// an appendix without a delta spec list — the "none" placeholder is shown
// instead of an empty bulleted section.
func TestPlanOpenspecAppendix_EmptySpecs(t *testing.T) {
	dir := t.TempDir()
	out, err := openspecAppendix(dir, PlanSupportIn{
		Action:     "openspec_appendix",
		ChangeName: "add-foo-feature",
	})
	if err != nil {
		t.Fatalf("openspecAppendix: %v", err)
	}
	if !strings.Contains(out.AppendixMarkdown, "**Spec deltas:** _(none)_") {
		t.Errorf("AppendixMarkdown missing empty spec deltas placeholder: %s", out.AppendixMarkdown)
	}
	if strings.Contains(out.AppendixMarkdown, "- `") {
		t.Errorf("AppendixMarkdown should have no spec delta bullets: %s", out.AppendixMarkdown)
	}
}

// TestPlanOpenspecAppendix_TaskMapping verifies caller-supplied task mapping
// entries (already formatted as plan-task <-> openspec-task correlations by
// the caller) are all rendered verbatim, in order, as bullets under the
// "Task mapping" section — including an entry the caller has marked
// unmapped.
func TestPlanOpenspecAppendix_TaskMapping(t *testing.T) {
	dir := t.TempDir()
	tasks := []string{
		"Task 1: Add foo config (openspec task 1)",
		"Task 3: Wire bar workflow (openspec task 2)",
		"(unmapped) openspec task 3: Add telemetry",
	}
	out, err := openspecAppendix(dir, PlanSupportIn{
		Action:     "openspec_appendix",
		ChangeName: "add-foo-feature",
		PlanTasks:  tasks,
	})
	if err != nil {
		t.Fatalf("openspecAppendix: %v", err)
	}
	for _, task := range tasks {
		if !strings.Contains(out.AppendixMarkdown, "- "+task) {
			t.Errorf("AppendixMarkdown missing task mapping bullet %q: %s", task, out.AppendixMarkdown)
		}
	}
	mappingIdx := strings.Index(out.AppendixMarkdown, "**Task mapping:**")
	if mappingIdx == -1 {
		t.Fatalf("AppendixMarkdown missing task mapping heading: %s", out.AppendixMarkdown)
	}
	firstIdx := strings.Index(out.AppendixMarkdown, "- "+tasks[0])
	lastIdx := strings.Index(out.AppendixMarkdown, "- "+tasks[2])
	if firstIdx < mappingIdx || lastIdx < firstIdx {
		t.Errorf("task mapping bullets out of expected order: %s", out.AppendixMarkdown)
	}
}

// ---------------------------------------------------------------------------
// plan_support openspec_instructions / openspec_stage tests
// ---------------------------------------------------------------------------

// psStageStatusJSON is the `openspec status --change add-widget --json`
// payload the stub prints, in the spec-driven schema's artifact order.
const psStageStatusJSON = `{"changeName":"add-widget","schemaName":"spec-driven","artifactPaths":{` +
	`"proposal":{"outputPath":"proposal.md","existingOutputPaths":[]},` +
	`"specs":{"outputPath":"specs/**/*.md","existingOutputPaths":[]},` +
	`"design":{"outputPath":"design.md","existingOutputPaths":[]},` +
	`"tasks":{"outputPath":"tasks.md","existingOutputPaths":[]}},` +
	`"artifacts":[` +
	`{"id":"proposal","outputPath":"proposal.md","status":"ready","requires":[]},` +
	`{"id":"specs","outputPath":"specs/**/*.md","status":"blocked","requires":["proposal"]},` +
	`{"id":"design","outputPath":"design.md","status":"blocked","requires":["proposal"]},` +
	`{"id":"tasks","outputPath":"tasks.md","status":"blocked","requires":["specs","design"]}]}`

// stubOpenspecForStage installs an `openspec` PATH stub answering every call
// openspec_instructions and openspec_stage make for change add-widget.
// validateExit is the exit code of `openspec validate add-widget --strict`.
func stubOpenspecForStage(t *testing.T, validateExit int) {
	t.Helper()
	cases := map[string]openspecCLIStub{
		"new change add-widget":             {stdout: "Created change 'add-widget'"},
		"status --change add-widget --json": {stdout: psStageStatusJSON},
	}
	for _, id := range []string{"proposal", "specs", "design", "tasks"} {
		cases["instructions "+id+" --change add-widget --json"] = openspecCLIStub{
			stdout: fmt.Sprintf(`{"artifactId":"%s","outputPath":"x","template":"T-%s","instruction":"I-%s","context":"ctx","rules":["r-%s"]}`, id, id, id, id),
		}
	}
	if validateExit == 0 {
		cases["validate add-widget --strict"] = openspecCLIStub{stdout: "Change 'add-widget' is valid"}
	} else {
		cases["validate add-widget --strict"] = openspecCLIStub{stdout: "Change 'add-widget' has issues: proposal.md missing Why section", exit: validateExit}
	}
	stubOpenspecCLI(t, cases)
}

// newOpenspecStageFixture creates a git repo seeded by setup_init (the
// managed .gitignore blocks), with openspec/config.yaml, all committed.
// Optional configToml replaces .sdlc-v2/config.toml before the commit.
func newOpenspecStageFixture(t *testing.T, configToml string) string {
	t.Helper()
	root := t.TempDir()
	initGitFixture(t, root)
	if _, err := setupInit(root, SetupInitIn{}); err != nil {
		t.Fatalf("setupInit: %v", err)
	}
	if configToml != "" {
		writeFile(t, filepath.Join(root, ".sdlc-v2", "config.toml"), configToml)
	}
	writeFile(t, filepath.Join(root, "openspec", "config.yaml"), "schema: spec-driven\n")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-m", "baseline")
	return root
}

// gitStatusPorcelain returns `git status --porcelain` for root.
func gitStatusPorcelain(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	return string(out)
}

// TestPlanSupportOpenspecInstructions verifies openspec_instructions returns
// the schema, the artifacts in status order with their CLI guidance, and the
// same guardrails plan_prepare returns, without touching the repository.
func TestPlanSupportOpenspecInstructions(t *testing.T) {
	t.Run("artifacts and guardrails", func(t *testing.T) {
		stubOpenspecForStage(t, 0)
		root := newOpenspecStageFixture(t, ""+
			"[plan.guardrails.no-secrets]\n"+
			"description = \"Never commit secrets\"\n"+
			"\n"+
			"[plan.guardrails.test-coverage]\n"+
			"description = \"Cover new branches\"\n")

		out, err := planSupportCore(root, root, PlanSupportIn{Action: "openspec_instructions", ChangeName: "add-widget"})
		if err != nil {
			t.Fatalf("openspec_instructions: %v", err)
		}
		if out.SchemaName != "spec-driven" {
			t.Errorf("SchemaName = %q, want spec-driven", out.SchemaName)
		}
		var ids []string
		for _, a := range out.Artifacts {
			ids = append(ids, a.ID)
		}
		if want := []string{"proposal", "specs", "design", "tasks"}; !reflect.DeepEqual(ids, want) {
			t.Errorf("artifact ids = %v, want %v", ids, want)
		}
		if a := out.Artifacts[1]; a.OutputPath != "specs/**/*.md" || a.Template != "T-specs" || a.Instruction != "I-specs" ||
			a.Context != "ctx" || !reflect.DeepEqual(a.Rules, []string{"r-specs"}) || !reflect.DeepEqual(a.Requires, []string{"proposal"}) {
			t.Errorf("Artifacts[1] = %+v, want merged status+instructions for specs", a)
		}
		wantNext := "Author each artifact in order from template, instruction, context and rules; follow guardrails in design and tasks; then call openspec_stage."
		if out.Next != wantNext {
			t.Errorf("Next = %q, want %q", out.Next, wantNext)
		}

		// Regression guard: ArtifactGuide must carry json tags, or the wire
		// response uses Go's capitalized field names (ID, OutputPath, ...)
		// instead of the documented lowerCamelCase keys (id, outputPath,
		// ...) that the plan skill and the delta spec both depend on.
		raw, err := json.Marshal(out.Artifacts[0])
		if err != nil {
			t.Fatalf("marshal artifact: %v", err)
		}
		var asMap map[string]any
		if err := json.Unmarshal(raw, &asMap); err != nil {
			t.Fatalf("unmarshal artifact: %v", err)
		}
		for _, key := range []string{"id", "outputPath", "requires", "template", "instruction", "context", "rules"} {
			if _, ok := asMap[key]; !ok {
				t.Errorf("artifact JSON missing lowerCamelCase key %q; got keys %v", key, raw)
			}
		}

		prep, err := runPlanPrepare(t, root, root, PlanPrepareIn{SkipConfigCheck: true})
		if err != nil {
			t.Fatalf("plan_prepare: %v", err)
		}
		if len(out.Guardrails) != 2 || !reflect.DeepEqual(out.Guardrails, prep.Guardrails) {
			t.Errorf("Guardrails = %+v, want plan_prepare's %+v (2 entries)", out.Guardrails, prep.Guardrails)
		}
		if !strings.Contains(out.Summary, "2 guardrail(s)") {
			t.Errorf("Summary = %q, want the guardrail count", out.Summary)
		}
	})

	t.Run("no guardrails and repo unchanged", func(t *testing.T) {
		stubOpenspecForStage(t, 0)
		// Replace setup_init's seeded guardrails with a config that has none.
		root := newOpenspecStageFixture(t, "# no plan guardrails\n")

		out, err := planSupportCore(root, root, PlanSupportIn{Action: "openspec_instructions", ChangeName: "add-widget"})
		if err != nil {
			t.Fatalf("openspec_instructions: %v", err)
		}
		if out.Guardrails == nil || len(out.Guardrails) != 0 {
			t.Errorf("Guardrails = %#v, want empty non-nil slice", out.Guardrails)
		}
		if s := gitStatusPorcelain(t, root); s != "" {
			t.Errorf("repo changed after openspec_instructions:\n%s", s)
		}
		if _, err := os.Stat(filepath.Join(root, ".sdlc-v2", "openspec-staging")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("openspec_instructions created the staging dir (stat err %v)", err)
		}
		if _, err := os.Stat(filepath.Join(root, "openspec", "changes")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("openspec_instructions created openspec/changes (stat err %v)", err)
		}
	})
}

// TestPlanSupportOpenspecStage verifies openspec_stage writes the files from
// openspec_instructions' outputPaths into the staging dir, reports the
// validation result with the matching next text, and maps every error in the
// contract table to its kind and suggestion.
func TestPlanSupportOpenspecStage(t *testing.T) {
	// filesFor turns openspec_instructions' outputPaths into concrete files;
	// a glob pattern gets a capability path.
	filesFor := func(guides []openspec.ArtifactGuide) []openspec.StageFile {
		var files []openspec.StageFile
		for _, g := range guides {
			p := g.OutputPath
			if strings.Contains(p, "*") {
				p = "specs/widget/spec.md"
			}
			files = append(files, openspec.StageFile{Path: p, Content: "# " + g.ID + "\n"})
		}
		return files
	}

	for _, tc := range []struct {
		name         string
		validateExit int
		wantValid    bool
		wantNext     string
		wantOutput   string
	}{
		{"valid", 0, true, "Staged and valid. Add the **OpenSpec-Staging:** header to the plan.", "is valid"},
		{"invalid", 1, false, "Fix the artifacts using validateOutput and call openspec_stage again.", "missing Why section"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubOpenspecForStage(t, tc.validateExit)
			root := newOpenspecStageFixture(t, "")

			ins, err := planSupportCore(root, root, PlanSupportIn{Action: "openspec_instructions", ChangeName: "add-widget"})
			if err != nil {
				t.Fatalf("openspec_instructions: %v", err)
			}
			files := filesFor(ins.Artifacts)
			out, err := planSupportCore(root, root, PlanSupportIn{
				Action:     "openspec_stage",
				ChangeName: "add-widget",
				Files:      files,
				PlanPath:   "/tmp/plans/add-widget.md",
			})
			if err != nil {
				t.Fatalf("openspec_stage: %v", err)
			}
			if out.StagingDir != ".sdlc-v2/openspec-staging/add-widget/" {
				t.Errorf("StagingDir = %q", out.StagingDir)
			}
			if out.Valid == nil || *out.Valid != tc.wantValid {
				t.Errorf("Valid = %v, want %v", out.Valid, tc.wantValid)
			}
			if !strings.Contains(out.ValidateOutput, tc.wantOutput) {
				t.Errorf("ValidateOutput = %q, want it to contain %q", out.ValidateOutput, tc.wantOutput)
			}
			if out.Next != tc.wantNext {
				t.Errorf("Next = %q, want %q", out.Next, tc.wantNext)
			}
			if len(out.StagedFiles) != len(files) {
				t.Fatalf("StagedFiles = %+v, want %d entries", out.StagedFiles, len(files))
			}
			for i, f := range files {
				if out.StagedFiles[i].Path != f.Path || out.StagedFiles[i].SHA256 == "" {
					t.Errorf("StagedFiles[%d] = %+v, want path %q with a sha256", i, out.StagedFiles[i], f.Path)
				}
				got, err := os.ReadFile(filepath.Join(root, ".sdlc-v2", "openspec-staging", "add-widget", filepath.FromSlash(f.Path)))
				if err != nil || string(got) != f.Content {
					t.Errorf("staged %s = %q (err %v), want %q", f.Path, got, err, f.Content)
				}
			}
			raw, err := os.ReadFile(filepath.Join(root, ".sdlc-v2", "openspec-staging", "add-widget", openspec.StageManifestFile))
			if err != nil {
				t.Fatalf("read stage.json: %v", err)
			}
			var manifest openspec.StageManifest
			if err := json.Unmarshal(raw, &manifest); err != nil {
				t.Fatalf("decode stage.json: %v", err)
			}
			if manifest.PlanPath != "/tmp/plans/add-widget.md" || (manifest.ValidatedAt != "") != tc.wantValid {
				t.Errorf("stage.json = %+v, want planPath set and validatedAt only when valid", manifest)
			}
			if s := gitStatusPorcelain(t, root); s != "" {
				t.Errorf("openspec_stage left tracked changes:\n%s", s)
			}
		})
	}

	t.Run("diagram contrast", func(t *testing.T) {
		stubOpenspecForStage(t, 0) // CLI validation passes; the contrast hit alone must flip valid to false
		root := newOpenspecStageFixture(t, "")

		ins, err := planSupportCore(root, root, PlanSupportIn{Action: "openspec_instructions", ChangeName: "add-widget"})
		if err != nil {
			t.Fatalf("openspec_instructions: %v", err)
		}
		files := filesFor(ins.Artifacts)
		pastel := "classDef new fill:#d4f7d4,stroke:#2a7a2a"
		// Line 1 "# doc", 2 blank, 3 fence open, 4 flowchart, 5 the pastel classDef, 6 fence close.
		files[0].Content = "# doc\n\n```mermaid\nflowchart TD\n" + pastel + "\n```\n"

		out, err := planSupportCore(root, root, PlanSupportIn{
			Action:     "openspec_stage",
			ChangeName: "add-widget",
			Files:      files,
			PlanPath:   "/tmp/plans/add-widget.md",
		})
		if err != nil {
			t.Fatalf("openspec_stage: %v", err)
		}
		if out.Valid == nil || *out.Valid {
			t.Errorf("Valid = %v, want false (CLI passed, but the staged file has a pastel classDef)", out.Valid)
		}
		wantLine := fmt.Sprintf("diagram contrast: %s:5: no text color: %s", files[0].Path, pastel)
		if !strings.Contains(out.ValidateOutput, wantLine) {
			t.Errorf("ValidateOutput = %q, want it to contain %q", out.ValidateOutput, wantLine)
		}
		if !strings.Contains(out.ValidateOutput, "is valid") {
			t.Errorf("ValidateOutput = %q, want it to still contain the CLI's own passing output", out.ValidateOutput)
		}
		if out.Next != "Fix the artifacts using validateOutput and call openspec_stage again." {
			t.Errorf("Next = %q", out.Next)
		}
	})

	t.Run("no diagram contrast hit leaves valid/validateOutput unchanged", func(t *testing.T) {
		stubOpenspecForStage(t, 0)
		root := newOpenspecStageFixture(t, "")

		ins, err := planSupportCore(root, root, PlanSupportIn{Action: "openspec_instructions", ChangeName: "add-widget"})
		if err != nil {
			t.Fatalf("openspec_instructions: %v", err)
		}
		files := filesFor(ins.Artifacts)
		out, err := planSupportCore(root, root, PlanSupportIn{
			Action:     "openspec_stage",
			ChangeName: "add-widget",
			Files:      files,
			PlanPath:   "/tmp/plans/add-widget.md",
		})
		if err != nil {
			t.Fatalf("openspec_stage: %v", err)
		}
		if out.Valid == nil || !*out.Valid {
			t.Errorf("Valid = %v, want true", out.Valid)
		}
		if out.ValidateOutput != "Change 'add-widget' is valid" {
			t.Errorf("ValidateOutput = %q, want the CLI output unchanged", out.ValidateOutput)
		}
	})

	t.Run("errors", func(t *testing.T) {
		proposal := []openspec.StageFile{{Path: "proposal.md", Content: "# P\n"}}
		for _, tc := range []struct {
			name           string
			noCLI          bool
			unresolved     bool // the active worktree could not be resolved
			setup          func(t *testing.T, root string)
			in             PlanSupportIn
			wantClass      string
			wantMsg        string
			wantMsgPrefix  string // when set, the message must start with it instead of equal wantMsg
			wantCause      error  // when set, errors.Is must find it in the returned chain
			wantSuggestion string
		}{
			{
				name:           "invalid changeName",
				in:             PlanSupportIn{Action: "openspec_stage", ChangeName: "Add_Widget", Files: proposal},
				wantClass:      "domain",
				wantMsg:        `openspec_stage: invalid changeName "Add_Widget"`,
				wantSuggestion: "Use lowercase letters, digits and single hyphens, for example add-widget.",
			},
			{
				name:           "path escapes change dir",
				in:             PlanSupportIn{Action: "openspec_stage", ChangeName: "add-widget", Files: []openspec.StageFile{{Path: "../evil.md", Content: "x"}}},
				wantClass:      "domain",
				wantMsg:        `openspec_stage: path "../evil.md" not allowed`,
				wantSuggestion: "Use a path relative to the change dir that matches an outputPath from openspec_instructions, for example specs/<capability>/spec.md.",
			},
			{
				name:           "path matches no outputPath",
				in:             PlanSupportIn{Action: "openspec_stage", ChangeName: "add-widget", Files: []openspec.StageFile{{Path: "notes.txt", Content: "x"}}},
				wantClass:      "domain",
				wantMsg:        `openspec_stage: path "notes.txt" not allowed`,
				wantSuggestion: "Use a path relative to the change dir that matches an outputPath from openspec_instructions, for example specs/<capability>/spec.md.",
			},
			{
				name:           "openspec CLI missing (stage)",
				noCLI:          true,
				in:             PlanSupportIn{Action: "openspec_stage", ChangeName: "add-widget", Files: proposal},
				wantClass:      "infra",
				wantMsg:        "openspec CLI not found on PATH",
				wantSuggestion: "Install the OpenSpec CLI (npm i -g @fission-ai/openspec) and retry, or choose Skip OpenSpec.",
			},
			{
				name:           "openspec CLI missing (instructions)",
				noCLI:          true,
				in:             PlanSupportIn{Action: "openspec_instructions", ChangeName: "add-widget"},
				wantClass:      "infra",
				wantMsg:        "openspec CLI not found on PATH",
				wantSuggestion: "Install the OpenSpec CLI (npm i -g @fission-ai/openspec) and retry, or choose Skip OpenSpec.",
			},
			{
				name:           "active worktree unresolved (stage)",
				unresolved:     true,
				in:             PlanSupportIn{Action: "openspec_stage", ChangeName: "add-widget", Files: proposal},
				wantClass:      "domain",
				wantMsg:        "openspec_stage: active worktree not resolved",
				wantSuggestion: "Run the call from inside the git worktree that holds the plan.",
			},
			{
				name:           "active worktree unresolved (instructions)",
				unresolved:     true,
				in:             PlanSupportIn{Action: "openspec_instructions", ChangeName: "add-widget"},
				wantClass:      "domain",
				wantMsg:        "openspec_instructions: active worktree not resolved",
				wantSuggestion: "Run the call from inside the git worktree that holds the plan.",
			},
			{
				// A directory where the current spec file is expected: the open
				// succeeds and the read fails with an error that is not not-exist.
				name: "target spec unreadable (stage)",
				setup: func(t *testing.T, root string) {
					t.Helper()
					if err := os.MkdirAll(filepath.Join(root, "openspec", "specs", "user-auth", "spec.md"), 0o755); err != nil {
						t.Fatalf("mkdir spec dir: %v", err)
					}
				},
				in: PlanSupportIn{Action: "openspec_stage", ChangeName: "add-widget", Files: []openspec.StageFile{
					{Path: "proposal.md", Content: "# P\n"},
					{Path: "specs/user-auth/spec.md", Content: "## ADDED Requirements\n"},
				}},
				wantClass:      "infra",
				wantMsgPrefix:  "openspec_stage: openspec stage: target spec: ",
				wantCause:      openspec.ErrTargetSpec,
				wantSuggestion: "Check read permission on the named spec under openspec/specs/, then call openspec_stage again.",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				stubOpenspecForStage(t, 0)
				root := newOpenspecStageFixture(t, "")
				if tc.setup != nil {
					tc.setup(t, root)
				}
				if tc.noCLI {
					pathWithoutOpenspec(t)
				}
				contentRoot := root
				if tc.unresolved {
					contentRoot = ""
				}
				_, err := planSupportCore(root, contentRoot, tc.in)
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				if got := errorClassOf(err); got != tc.wantClass {
					t.Errorf("error class = %q, want %q (err %v)", got, tc.wantClass, err)
				}
				if tc.wantMsgPrefix != "" {
					if !strings.HasPrefix(err.Error(), tc.wantMsgPrefix) {
						t.Errorf("err.Error() = %q, want prefix %q", err.Error(), tc.wantMsgPrefix)
					}
				} else if err.Error() != tc.wantMsg {
					t.Errorf("err.Error() = %q, want %q", err.Error(), tc.wantMsg)
				}
				if tc.wantCause != nil && !errors.Is(err, tc.wantCause) {
					t.Errorf("errors.Is(err, %v) = false, want true (err %v)", tc.wantCause, err)
				}
				if got := suggestionOf(err); got != tc.wantSuggestion {
					t.Errorf("Suggestion = %q, want %q", got, tc.wantSuggestion)
				}
				if _, err := os.Stat(filepath.Join(root, ".sdlc-v2", "openspec-staging")); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("a refused call created the staging dir (stat err %v)", err)
				}
			})
		}
	})
}

// ---------------------------------------------------------------------------
// preplan_context
// ---------------------------------------------------------------------------

// preplanSkeletonFor returns the exact text of a new topic file for topic.
func preplanSkeletonFor(topic string) string {
	return "# Preplan: " + topic + "\n" +
		"\n" +
		"**Status:** in progress\n" +
		"\n" +
		"## Goal\n" +
		"\n" +
		"## Users and effect\n" +
		"\n" +
		"## Flows\n" +
		"\n" +
		"## Decisions\n" +
		"\n" +
		"| # | Decision | Reason |\n" +
		"|---|---|---|\n" +
		"\n" +
		"## Open questions\n" +
		"\n" +
		"## Guardrail check\n" +
		"\n" +
		"| Proposal | Guardrail | Severity | Result |\n" +
		"|---|---|---|---|\n"
}

// dataDirEntries lists the names directly under root/.sdlc-v2.
func dataDirEntries(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".sdlc-v2"))
	if err != nil {
		t.Fatalf("read data dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestPlanSupportPreplanContext verifies preplan_context returns the same
// guardrails as plan_prepare, creates the topic file once with the skeleton,
// never changes an existing file, and starts no plan run.
func TestPlanSupportPreplanContext(t *testing.T) {
	const guardrailsToml = "" +
		"[plan.guardrails.no-secrets]\n" +
		"description = \"Never commit secrets\"\n" +
		"\n" +
		"[plan.guardrails.test-coverage]\n" +
		"description = \"Cover new branches\"\n"

	t.Run("creates the topic file and returns the plan_prepare guardrails", func(t *testing.T) {
		root := newOpenspecStageFixture(t, guardrailsToml)
		before := dataDirEntries(t, root)

		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		wantFile := filepath.Join(root, ".sdlc-v2", "preplan", "auth-flow.md")
		if out.PreplanFile != wantFile {
			t.Errorf("PreplanFile = %q, want %q", out.PreplanFile, wantFile)
		}
		if !out.PreplanCreated {
			t.Error("PreplanCreated = false, want true for an absent file")
		}
		got, err := os.ReadFile(wantFile)
		if err != nil {
			t.Fatalf("read topic file: %v", err)
		}
		if string(got) != preplanSkeletonFor("auth flow") {
			t.Errorf("topic file =\n%s\nwant the skeleton:\n%s", got, preplanSkeletonFor("auth flow"))
		}
		if want := "2 guardrail(s) loaded. Created topic file auth-flow.md."; out.Summary != want {
			t.Errorf("Summary = %q, want %q", out.Summary, want)
		}
		if want := "Read preplanFile, then ask the first question."; out.Next != want {
			t.Errorf("Next = %q, want %q", out.Next, want)
		}

		// No run, no state write: only the preplan dir is new.
		after := dataDirEntries(t, root)
		var added []string
		seen := map[string]bool{}
		for _, n := range before {
			seen[n] = true
		}
		for _, n := range after {
			if !seen[n] {
				added = append(added, n)
			}
		}
		if !reflect.DeepEqual(added, []string{"preplan"}) {
			t.Errorf("new entries under .sdlc-v2 = %v, want [preplan]", added)
		}
		if s := gitStatusPorcelain(t, root); s != "" {
			t.Errorf("topic file shows in git status:\n%s", s)
		}

		prep, err := runPlanPrepare(t, root, root, PlanPrepareIn{SkipConfigCheck: true})
		if err != nil {
			t.Fatalf("plan_prepare: %v", err)
		}
		if len(out.Guardrails) != 2 || !reflect.DeepEqual(out.Guardrails, prep.Guardrails) {
			t.Errorf("Guardrails = %+v, want plan_prepare's %+v (2 entries)", out.Guardrails, prep.Guardrails)
		}
	})

	t.Run("an existing file stays unchanged", func(t *testing.T) {
		root := newOpenspecStageFixture(t, guardrailsToml)
		file := filepath.Join(root, ".sdlc-v2", "preplan", "auth-flow.md")
		const custom = "# Preplan: auth flow\n\nMy own notes.\n"
		writeFile(t, file, custom)

		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		if out.PreplanCreated {
			t.Error("PreplanCreated = true, want false for an existing file")
		}
		if out.PreplanFile != file {
			t.Errorf("PreplanFile = %q, want %q", out.PreplanFile, file)
		}
		if got, _ := os.ReadFile(file); string(got) != custom {
			t.Errorf("topic file = %q, want it unchanged (%q)", got, custom)
		}
		if want := "2 guardrail(s) loaded. Topic file auth-flow.md exists."; out.Summary != want {
			t.Errorf("Summary = %q, want %q", out.Summary, want)
		}
		if want := "Read preplanFile, then continue with its open questions."; out.Next != want {
			t.Errorf("Next = %q, want %q", out.Next, want)
		}
	})

	t.Run("topics with the same slug share one file", func(t *testing.T) {
		root := newOpenspecStageFixture(t, "")
		first, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "  Auth Flow  "})
		if err != nil {
			t.Fatalf("first call: %v", err)
		}
		if !first.PreplanCreated {
			t.Error("first PreplanCreated = false, want true")
		}
		if got, _ := os.ReadFile(first.PreplanFile); string(got) != preplanSkeletonFor("Auth Flow") {
			t.Errorf("topic file = %q, want the skeleton with the trimmed topic", got)
		}
		second, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth-flow"})
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		if second.PreplanCreated || second.PreplanFile != first.PreplanFile {
			t.Errorf("second = {created %v, file %q}, want {false, %q}", second.PreplanCreated, second.PreplanFile, first.PreplanFile)
		}
	})

	t.Run("a path-like topic stays inside the preplan dir", func(t *testing.T) {
		root := newOpenspecStageFixture(t, "")
		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "../../etc/passwd"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		if want := filepath.Join(root, ".sdlc-v2", "preplan", "etc-passwd.md"); out.PreplanFile != want {
			t.Errorf("PreplanFile = %q, want %q", out.PreplanFile, want)
		}
	})

	t.Run("a topic of 50 characters is accepted", func(t *testing.T) {
		root := newOpenspecStageFixture(t, "")
		topic := strings.Repeat("a", 50)
		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: topic})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		if want := filepath.Join(root, ".sdlc-v2", "preplan", topic+".md"); out.PreplanFile != want {
			t.Errorf("PreplanFile = %q, want %q", out.PreplanFile, want)
		}
	})

	t.Run("no config returns an empty non-nil list", func(t *testing.T) {
		root := t.TempDir()
		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		if out.Guardrails == nil || len(out.Guardrails) != 0 {
			t.Errorf("Guardrails = %#v, want empty non-nil slice", out.Guardrails)
		}
		if want := "0 guardrail(s) loaded — none configured. Created topic file auth-flow.md."; out.Summary != want {
			t.Errorf("Summary = %q, want %q", out.Summary, want)
		}
		if want := "Read preplanFile, then ask the first question."; out.Next != want {
			t.Errorf("Next = %q, want %q", out.Next, want)
		}
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var asMap map[string]any
		if err := json.Unmarshal(raw, &asMap); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, has := asMap["guardrails"]; has {
			t.Errorf("JSON keeps an empty guardrails field: %s", raw)
		}
		if v, has := asMap["preplanCreated"]; !has || v != true {
			t.Errorf("JSON preplanCreated = %v (present %v), want true", v, has)
		}
	})

	t.Run("malformed config returns an empty list and the warning", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "[plan\nguardrails = [\n")
		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		if out.Guardrails == nil || len(out.Guardrails) != 0 {
			t.Errorf("Guardrails = %#v, want empty non-nil slice", out.Guardrails)
		}
		if !strings.HasPrefix(out.Summary, "0 guardrail(s) loaded. Warning: Failed to read plan config: ") ||
			!strings.HasSuffix(out.Summary, ". Created topic file auth-flow.md.") {
			t.Errorf("Summary = %q, want the read warning between the count and the file state", out.Summary)
		}
		if want := "Read preplanFile, then ask the first question. Record the warning in the Guardrail check section."; out.Next != want {
			t.Errorf("Next = %q, want %q", out.Next, want)
		}
	})

	t.Run("served over MCP", func(t *testing.T) {
		root := newOpenspecStageFixture(t, guardrailsToml)
		text := evidenceRender(t, root, map[string]any{"action": "preplan_context", "topic": "auth flow"})
		if !strings.Contains(text, "auth-flow.md") {
			t.Errorf("rendered result lacks the topic file name:\n%s", text)
		}
		if _, err := os.Stat(filepath.Join(root, ".sdlc-v2", "preplan", "auth-flow.md")); err != nil {
			t.Errorf("topic file missing after the MCP call: %v", err)
		}
	})
}

// TestPlanSupportPreplanContextErrors maps every bad topic to its DomainError
// and Suggestion with nothing written, and a failed create to its InfraError.
func TestPlanSupportPreplanContextErrors(t *testing.T) {
	const (
		wordsSuggestion = "Pass a short topic name with letters or digits, for example \"auth flow\"."
		asciiSuggestion = "Pass a topic name with ASCII letters or digits, for example \"auth flow\"."
		longSuggestion  = "Pass a shorter topic name. Put the detail in the first answer."
		lineSuggestion  = "Pass the topic name on one line."
	)
	for _, tc := range []struct {
		name           string
		topic          string
		wantMsg        string
		wantSuggestion string
	}{
		{"empty", "", `preplan_context: topic "" has no letter or digit`, wordsSuggestion},
		{"blank", " \t ", `preplan_context: topic "" has no letter or digit`, wordsSuggestion},
		{"punctuation only", "-- !! --", `preplan_context: topic "-- !! --" has no letter or digit`, wordsSuggestion},
		{"non-ASCII letters only", "日本語", `preplan_context: topic "日本語" has no ASCII letter or digit`, asciiSuggestion},
		{"51 characters", strings.Repeat("a", 51), "preplan_context: topic has 51 characters, max 50", longSuggestion},
		{"51 multi-byte characters", "a" + strings.Repeat("é", 50), "preplan_context: topic has 51 characters, max 50", longSuggestion},
		{"line feed", "auth\nflow", "preplan_context: topic has a line break", lineSuggestion},
		{"carriage return", "auth\rflow", "preplan_context: topic has a line break", lineSuggestion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			_, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: tc.topic})
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if got := errorClassOf(err); got != "domain" {
				t.Errorf("error class = %q, want domain (err %v)", got, err)
			}
			if err.Error() != tc.wantMsg {
				t.Errorf("err.Error() = %q, want %q", err.Error(), tc.wantMsg)
			}
			if got := suggestionOf(err); got != tc.wantSuggestion {
				t.Errorf("Suggestion = %q, want %q", got, tc.wantSuggestion)
			}
			if _, err := os.Stat(filepath.Join(root, ".sdlc-v2")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("a refused call created the data dir (stat err %v)", err)
			}
		})
	}

	t.Run("a regular file at the preplan dir path", func(t *testing.T) {
		root := t.TempDir()
		blocker := filepath.Join(root, ".sdlc-v2", "preplan")
		writeFile(t, blocker, "not a directory\n")

		_, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if got := errorClassOf(err); got != "infra" {
			t.Errorf("error class = %q, want infra (err %v)", got, err)
		}
		wantPrefix := "preplan_context: create " + filepath.Join(blocker, "auth-flow.md") + ": "
		if !strings.HasPrefix(err.Error(), wantPrefix) {
			t.Errorf("err.Error() = %q, want prefix %q", err.Error(), wantPrefix)
		}
		if want := "Check write permission on .sdlc-v2/preplan/, then run the skill again."; suggestionOf(err) != want {
			t.Errorf("Suggestion = %q, want %q", suggestionOf(err), want)
		}
		if got, _ := os.ReadFile(blocker); string(got) != "not a directory\n" {
			t.Errorf("blocking file = %q, want it unchanged", got)
		}
	})
}

// errInjectedPreplan is the error the preplan write and close seams return.
var errInjectedPreplan = errors.New("injected preplan failure")

// savePreplanSeams saves the real write and close functions of
// createPreplanFile. It registers a cleanup that puts them back when the test
// ends, and it returns a function that puts them back at once.
func savePreplanSeams(t *testing.T) (restore func()) {
	t.Helper()
	origWrite, origClose := preplanWriteString, preplanCloseFile
	restore = func() {
		preplanWriteString, preplanCloseFile = origWrite, origClose
	}
	t.Cleanup(restore)
	return restore
}

// failPreplanWrite makes the preplan write fail after the create. It returns
// the function that restores the real seams.
func failPreplanWrite(t *testing.T) (restore func()) {
	t.Helper()
	restore = savePreplanSeams(t)
	preplanWriteString = func(*os.File, string) error { return errInjectedPreplan }
	return restore
}

// failPreplanClose makes the preplan close fail after a successful write. The
// replacement still closes the file, so the test leaks no descriptor. It
// returns the function that restores the real seams.
func failPreplanClose(t *testing.T) (restore func()) {
	t.Helper()
	restore = savePreplanSeams(t)
	preplanCloseFile = func(f *os.File) error {
		_ = f.Close()
		return errInjectedPreplan
	}
	return restore
}

// TestCreatePreplanFileWriteStepFailures covers each failure point after the
// create succeeded. A failed write and a failed close both return the error,
// leave no file, and let a later call create the file.
func TestCreatePreplanFileWriteStepFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inject func(*testing.T) func()
	}{
		{"write fails after the create", failPreplanWrite},
		{"close fails after a successful write", failPreplanClose},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "preplan", "auth-flow.md")
			const content = "# Preplan: auth flow\n"

			restore := tc.inject(t)
			created, err := createPreplanFile(file, content)
			if created || !errors.Is(err, errInjectedPreplan) {
				t.Fatalf("createPreplanFile = (%v, %v), want (false, %v)", created, err, errInjectedPreplan)
			}
			if _, statErr := os.Stat(file); !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("partial file left behind (stat err %v)", statErr)
			}

			restore()
			created, err = createPreplanFile(file, content)
			if !created || err != nil {
				t.Fatalf("retry createPreplanFile = (%v, %v), want (true, nil)", created, err)
			}
			if got, _ := os.ReadFile(file); string(got) != content {
				t.Errorf("file after retry = %q, want %q", got, content)
			}
		})
	}
}

// TestPlanPreplanContextWriteFailure verifies a write failure after the create
// reaches the caller as an InfraError with a Suggestion, reports no created
// file, and leaves no topic file.
func TestPlanPreplanContextWriteFailure(t *testing.T) {
	root := t.TempDir()
	failPreplanWrite(t)

	out, err := planPreplanContext(root, "auth flow")
	var infra *mcpserver.InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("error = %v (%T), want *mcpserver.InfraError", err, err)
	}
	if infra.Suggestion == "" {
		t.Error("InfraError.Suggestion is empty, want recovery text")
	}
	if !errors.Is(err, errInjectedPreplan) {
		t.Errorf("error = %v, want it to wrap the write error", err)
	}
	if out.PreplanCreated || out.PreplanFile != "" {
		t.Errorf("output = {created %v, file %q}, want {false, \"\"}", out.PreplanCreated, out.PreplanFile)
	}
	topicFile := filepath.Join(root, ".sdlc-v2", "preplan", "auth-flow.md")
	if _, statErr := os.Stat(topicFile); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("topic file left behind (stat err %v)", statErr)
	}
}

// TestCreatePreplanFileOpenFailure verifies a create that fails for a reason
// other than "exists" returns the error and leaves no file. A read-only
// directory makes the open fail.
func TestCreatePreplanFileOpenFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	dir := filepath.Join(t.TempDir(), "preplan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make preplan dir: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("make preplan dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	file := filepath.Join(dir, "auth-flow.md")
	created, err := createPreplanFile(file, "# Preplan: auth flow\n")
	if created || err == nil {
		t.Fatalf("createPreplanFile = (%v, %v), want (false, non-nil error)", created, err)
	}
	if errors.Is(err, fs.ErrExist) {
		t.Errorf("error = %v, want a failure other than \"exists\"", err)
	}
	if _, statErr := os.Stat(file); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("topic file exists after a failed open (stat err %v)", statErr)
	}
}
