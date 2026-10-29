package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/openspec"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
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
// equal {G1..G22} exactly"). G22 (style compliance) has its own lane.
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

// TestPlanSupportCore_UnusedFieldsAreIgnored verifies that merge_results
// merges the lane results and does not read filePath, a field that only the
// material actions use.
func TestPlanSupportCore_UnusedFieldsAreIgnored(t *testing.T) {
	prev := readFileFunc
	t.Cleanup(func() { readFileFunc = prev })
	readFileFunc = func(path string) ([]byte, error) {
		t.Errorf("merge_results read %q, want no file read", path)
		return nil, os.ErrNotExist
	}

	out, err := planSupportCore("", "", PlanSupportIn{
		Action:        "merge_results",
		FilePath:      "/nonexistent/plan.md",
		LaneResults:   fiveLanesCoveringAllGates(),
		ExpectedGates: allGates(),
	})
	if err != nil {
		t.Fatalf("planSupportCore: %v", err)
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
			{
				// A current spec over the size limit is a caller problem: the
				// spec must shrink, so it is a DomainError, not a read error.
				name: "target spec too large (stage)",
				setup: func(t *testing.T, root string) {
					t.Helper()
					p := filepath.Join(root, "openspec", "specs", "user-auth", "spec.md")
					if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
						t.Fatalf("mkdir spec dir: %v", err)
					}
					if err := os.WriteFile(p, []byte(strings.Repeat("a", 1<<20+1)), 0o644); err != nil {
						t.Fatalf("write spec: %v", err)
					}
				},
				in: PlanSupportIn{Action: "openspec_stage", ChangeName: "add-widget", Files: []openspec.StageFile{
					{Path: "proposal.md", Content: "# P\n"},
					{Path: "specs/user-auth/spec.md", Content: "## MODIFIED Requirements\n"},
				}},
				wantClass:      "domain",
				wantMsgPrefix:  "openspec_stage: openspec stage: target spec: too large: ",
				wantCause:      openspec.ErrTargetSpecTooLarge,
				wantSuggestion: "Shrink the named spec under openspec/specs/ below the size limit in the message (for example, split the capability into two specs), then call openspec_stage again.",
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

// TestPlanSupportOpenspecStage_OtherPlanNamesMainStaging verifies that, with a
// main root and an active root that differ, openspec_stage reads the owner
// check from the staging dir under the main root, leaves the active root
// alone, and names the main staging dir in the Suggestion.
func TestPlanSupportOpenspecStage_OtherPlanNamesMainStaging(t *testing.T) {
	stubOpenspecForStage(t, 0)
	mainRoot := newOpenspecStageFixture(t, "")
	activeRoot := newOpenspecStageFixture(t, "")
	mainStaging := filepath.Join(mainRoot, ".sdlc-v2", "openspec-staging", "add-widget")
	activeStaging := filepath.Join(activeRoot, ".sdlc-v2", "openspec-staging", "add-widget")
	writeFile(t, filepath.Join(mainStaging, "old.md"), "# old\n")
	writeFile(t, filepath.Join(mainStaging, openspec.StageManifestFile),
		`{"change":"add-widget","schema":"spec-driven","planPath":"/plans/a.md","files":[]}`)
	// The refusal must come before the first CLI call.
	pathWithoutOpenspec(t)

	_, err := planSupportCore(mainRoot, activeRoot, PlanSupportIn{
		Action:     "openspec_stage",
		ChangeName: "add-widget",
		Files:      []openspec.StageFile{{Path: "proposal.md", Content: "# new\n"}},
		PlanPath:   "/plans/b.md",
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !errors.Is(err, openspec.ErrStagedByOtherPlan) {
		t.Fatalf("errors.Is(err, ErrStagedByOtherPlan) = false (err %v)", err)
	}
	suggestion := suggestionOf(err)
	if !strings.Contains(suggestion, "delete "+mainStaging+" and call") {
		t.Errorf("Suggestion = %q, want it to name the main staging dir %q", suggestion, mainStaging)
	}
	if strings.Contains(suggestion, activeStaging) {
		t.Errorf("Suggestion = %q, must not name the active staging dir %q", suggestion, activeStaging)
	}
	if _, statErr := os.Stat(activeStaging); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("a refused call touched the active staging dir (stat err %v)", statErr)
	}
	if got, readErr := os.ReadFile(filepath.Join(mainStaging, "old.md")); readErr != nil || string(got) != "# old\n" {
		t.Errorf("old staged file = %q (err %v), want it unchanged", got, readErr)
	}
}

// TestPlanSupportOpenspecStage_OtherPlan verifies openspec_stage refuses a
// change name whose staging dir holds a stage.json with another planPath,
// leaves that staging untouched, and does so before it needs the openspec CLI.
// Every other combination of existing stage.json and planPath stages as before.
func TestPlanSupportOpenspecStage_OtherPlan(t *testing.T) {
	const (
		manifestA          = `{"change":"add-widget","schema":"spec-driven","planPath":"/plans/a.md","files":[]}`
		manifestASlash     = `{"change":"add-widget","schema":"spec-driven","planPath":"/plans/a.md/","files":[]}`
		manifestNoPlan     = `{"change":"add-widget","schema":"spec-driven","files":[]}`
		manifestNumberPlan = `{"change":"add-widget","schema":"spec-driven","planPath":123,"files":[]}`
		manifestNotJSON    = `not json`
		wantMsg            = `openspec_stage: change "add-widget" is staged by another plan: /plans/a.md. ` +
			`If this call names the same plan file under another path, call again with planPath set to /plans/a.md.`
		wantSuggestionFmt = "If /plans/a.md is the plan file of this call under another path, call openspec_stage again with planPath set to /plans/a.md. " +
			"If /plans/a.md is another plan, pick another change name: replace add-widget in every header line that holds it (**Source:**, **OpenSpec-Create:** and **OpenSpec-Staging:**), then run Create-flow authoring again. " +
			"If that other plan is abandoned, ask the user, then delete %s and call openspec_stage again."
	)

	for _, tc := range []struct {
		name        string
		existing    string // stage.json content; "" means no staging folder
		manifestDir bool   // stage.json is a directory, not a file
		planPath    string
		refused     bool
	}{
		{"no staging folder", "", false, "/plans/a.md", false},
		{"same plan", manifestA, false, "/plans/a.md", false},
		{"same plan after Clean", manifestA, false, "/plans/./a.md", false},
		{"stored path with a trailing slash", manifestASlash, false, "/plans/a.md", false},
		{"another plan", manifestA, false, "/plans/b.md", true},
		{"same file as a relative path", manifestA, false, "plans/a.md", true},
		{"empty planPath in the call", manifestA, false, "", false},
		{"no planPath in stage.json", manifestNoPlan, false, "/plans/b.md", false},
		{"planPath of the wrong JSON type", manifestNumberPlan, false, "/plans/b.md", false},
		{"stage.json not valid JSON", manifestNotJSON, false, "/plans/b.md", false},
		{"stage.json is a directory", "", true, "/plans/b.md", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubOpenspecForStage(t, 0)
			root := newOpenspecStageFixture(t, "")
			stagingDir := filepath.Join(root, ".sdlc-v2", "openspec-staging", "add-widget")
			wantSuggestion := fmt.Sprintf(wantSuggestionFmt, stagingDir)
			oldFile := filepath.Join(stagingDir, "old.md")
			manifestFile := filepath.Join(stagingDir, openspec.StageManifestFile)
			if tc.existing != "" {
				writeFile(t, oldFile, "# old\n")
				writeFile(t, manifestFile, tc.existing)
			}
			if tc.manifestDir {
				writeFile(t, oldFile, "# old\n")
				if err := os.MkdirAll(manifestFile, 0o755); err != nil {
					t.Fatalf("mkdir stage.json: %v", err)
				}
			}
			if tc.refused {
				// The refusal must come before the first CLI call.
				pathWithoutOpenspec(t)
			}

			out, err := planSupportCore(root, root, PlanSupportIn{
				Action:     "openspec_stage",
				ChangeName: "add-widget",
				Files:      []openspec.StageFile{{Path: "proposal.md", Content: "# new\n"}},
				PlanPath:   tc.planPath,
			})

			if tc.refused {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				if got := errorClassOf(err); got != "domain" {
					t.Errorf("error class = %q, want domain (err %v)", got, err)
				}
				if err.Error() != wantMsg {
					t.Errorf("err.Error() = %q, want %q", err.Error(), wantMsg)
				}
				if !errors.Is(err, openspec.ErrStagedByOtherPlan) {
					t.Errorf("errors.Is(err, ErrStagedByOtherPlan) = false (err %v)", err)
				}
				if got := suggestionOf(err); got != wantSuggestion {
					t.Errorf("Suggestion = %q, want %q", got, wantSuggestion)
				}
				if got, err := os.ReadFile(oldFile); err != nil || string(got) != "# old\n" {
					t.Errorf("old staged file = %q (err %v), want it unchanged", got, err)
				}
				if got, err := os.ReadFile(manifestFile); err != nil || string(got) != tc.existing {
					t.Errorf("stage.json = %q (err %v), want it unchanged", got, err)
				}
				if _, err := os.Stat(filepath.Join(stagingDir, "proposal.md")); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("a refused call wrote proposal.md (stat err %v)", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("openspec_stage: %v", err)
			}
			if out.Valid == nil || !*out.Valid {
				t.Errorf("Valid = %v, want true", out.Valid)
			}
			wantReplaced := tc.existing != "" || tc.manifestDir
			if out.StagingReplaced == nil || *out.StagingReplaced != wantReplaced {
				t.Errorf("StagingReplaced = %v, want %v", out.StagingReplaced, wantReplaced)
			}
			if out.StagedPlanPath != tc.planPath {
				t.Errorf("StagedPlanPath = %q, want %q", out.StagedPlanPath, tc.planPath)
			}
			if got, err := os.ReadFile(filepath.Join(stagingDir, "proposal.md")); err != nil || string(got) != "# new\n" {
				t.Errorf("proposal.md = %q (err %v), want the new content", got, err)
			}
			if _, err := os.Stat(oldFile); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("old.md still exists after the re-stage (stat err %v)", err)
			}
			raw, err := os.ReadFile(manifestFile)
			if err != nil {
				t.Fatalf("read stage.json: %v", err)
			}
			var manifest openspec.StageManifest
			if err := json.Unmarshal(raw, &manifest); err != nil {
				t.Fatalf("decode stage.json: %v", err)
			}
			if manifest.PlanPath != tc.planPath {
				t.Errorf("stage.json planPath = %q, want %q", manifest.PlanPath, tc.planPath)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// preplan_context
// ---------------------------------------------------------------------------

// setPreplanNow fixes the clock of preplan_context for one test.
func setPreplanNow(t *testing.T, at time.Time) {
	t.Helper()
	old := preplanNow
	preplanNow = func() time.Time { return at }
	t.Cleanup(func() { preplanNow = old })
}

// preplanTestNow is the clock the preplan_context tests set with
// setPreplanNow. preplanSkeletonFor writes its Started line.
var preplanTestNow = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

// preplanDirNames lists the names in dir, in name order. A missing dir gives
// no names.
func preplanDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("read %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// preplanSkeletonFor returns the exact text of a new topic file for topic,
// created at preplanTestNow.
func preplanSkeletonFor(topic string) string {
	return "# Preplan: " + topic + "\n" +
		"\n" +
		"**Status:** in progress\n" +
		"\n" +
		"**Started:** 2026-10-12T12:00:00Z\n" +
		"\n" +
		"## Goal\n" +
		"\n" +
		"## Problem and benefit\n" +
		"\n" +
		"## Users and effect\n" +
		"\n" +
		"## Risk if wrong\n" +
		"\n" +
		"## Non-goals\n" +
		"\n" +
		"## Flows\n" +
		"\n" +
		"## Decisions\n" +
		"\n" +
		"| # | Decision | Reason |\n" +
		"|---|---|---|\n"
}

// TestPreplanSkeletonMatchesSections pins the "## " headings of a new topic
// file to preplanSections, in order, and the block helper to the same text in
// LF and CRLF line ends.
func TestPreplanSkeletonMatchesSections(t *testing.T) {
	skeleton := preplanSkeleton("auth flow", preplanTestNow)

	var headings []string
	for _, line := range strings.Split(skeleton, "\n") {
		if strings.HasPrefix(line, preplanHeadingPrefix) {
			headings = append(headings, strings.TrimPrefix(line, preplanHeadingPrefix))
		}
	}
	if !reflect.DeepEqual(headings, preplanSections) {
		t.Errorf("skeleton headings = %q, want preplanSections %q", headings, preplanSections)
	}

	if skeleton != preplanSkeletonFor("auth flow") {
		t.Errorf("skeleton =\n%s\nwant the hand-typed text:\n%s", skeleton, preplanSkeletonFor("auth flow"))
	}
	if !strings.HasSuffix(skeleton, "## Decisions\n\n| # | Decision | Reason |\n|---|---|---|\n") {
		t.Errorf("skeleton does not end after the Decisions table header:\n%s", skeleton)
	}

	if got, want := preplanSectionBlock("Goal", "\r\n"), "## Goal\r\n\r\n"; got != want {
		t.Errorf("CRLF Goal block = %q, want %q", got, want)
	}
	if got, want := preplanSectionBlock(preplanDecisionsSection, "\r\n"),
		"## Decisions\r\n\r\n| # | Decision | Reason |\r\n|---|---|---|\r\n"; got != want {
		t.Errorf("CRLF Decisions block = %q, want %q", got, want)
	}
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
	setPreplanNow(t, preplanTestNow)
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
		wantFile := filepath.Join(root, ".sdlc-v2", "preplan", "2026-10-12-auth-flow.md")
		if out.PreplanFile != wantFile {
			t.Errorf("PreplanFile = %q, want %q", out.PreplanFile, wantFile)
		}
		if out.PreplanCreated == nil || !*out.PreplanCreated {
			t.Errorf("PreplanCreated = %v, want true for an absent file", out.PreplanCreated)
		}
		if out.AddedSections == nil || len(*out.AddedSections) != 0 {
			t.Errorf("AddedSections = %v, want an empty non-nil list for a new file", out.AddedSections)
		}
		if out.EmptySections != nil {
			t.Errorf("EmptySections = %v, want unset for preplan_context", out.EmptySections)
		}
		got, err := os.ReadFile(wantFile)
		if err != nil {
			t.Fatalf("read topic file: %v", err)
		}
		if string(got) != preplanSkeletonFor("auth flow") {
			t.Errorf("topic file =\n%s\nwant the skeleton:\n%s", got, preplanSkeletonFor("auth flow"))
		}
		if want := "2 guardrail(s) loaded. Created topic file 2026-10-12-auth-flow.md."; out.Summary != want {
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
		custom := "# Preplan: auth flow\n\nMy own notes.\n" + preplanSkeletonTail
		writeFile(t, file, custom)
		noPreplanCheckWrite(t)

		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		if out.PreplanCreated == nil || *out.PreplanCreated {
			t.Errorf("PreplanCreated = %v, want false (set) for an existing file", out.PreplanCreated)
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
		if want := "Read preplanFile, then continue with its first gap: the first empty section, then the open questions, if any."; out.Next != want {
			t.Errorf("Next = %q, want %q", out.Next, want)
		}
		if out.AddedSections == nil || len(*out.AddedSections) != 0 {
			t.Errorf("AddedSections = %v, want an empty non-nil list", out.AddedSections)
		}
	})

	t.Run("topics with the same slug share one file", func(t *testing.T) {
		root := newOpenspecStageFixture(t, "")
		first, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "  Auth Flow  "})
		if err != nil {
			t.Fatalf("first call: %v", err)
		}
		if first.PreplanCreated == nil || !*first.PreplanCreated {
			t.Errorf("first PreplanCreated = %v, want true", first.PreplanCreated)
		}
		if got, _ := os.ReadFile(first.PreplanFile); string(got) != preplanSkeletonFor("Auth Flow") {
			t.Errorf("topic file = %q, want the skeleton with the trimmed topic", got)
		}
		second, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth-flow"})
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		if second.PreplanCreated == nil || *second.PreplanCreated || second.PreplanFile != first.PreplanFile {
			t.Errorf("second = {created %v, file %q}, want {false, %q}", second.PreplanCreated, second.PreplanFile, first.PreplanFile)
		}
	})

	t.Run("a path-like topic stays inside the preplan dir", func(t *testing.T) {
		root := newOpenspecStageFixture(t, "")
		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "../../etc/passwd"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		if want := filepath.Join(root, ".sdlc-v2", "preplan", "2026-10-12-etc-passwd.md"); out.PreplanFile != want {
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
		if want := filepath.Join(root, ".sdlc-v2", "preplan", "2026-10-12-"+topic+".md"); out.PreplanFile != want {
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
		if want := "0 guardrail(s) loaded — none configured. Created topic file 2026-10-12-auth-flow.md."; out.Summary != want {
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
			!strings.HasSuffix(out.Summary, ". Created topic file 2026-10-12-auth-flow.md.") {
			t.Errorf("Summary = %q, want the read warning between the count and the file state", out.Summary)
		}
		if want := "Read preplanFile, then ask the first question. preplan_check writes the warning into the Guardrail check line at Step 4."; out.Next != want {
			t.Errorf("Next = %q, want %q", out.Next, want)
		}
	})

	t.Run("an existing file and a malformed config give the warning and the continue hint", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "[plan\nguardrails = [\n")
		file := writePreplanTopic(t, root, "2026-10-01-auth-flow.md", "# Preplan: auth flow\n")
		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		if out.PreplanFile != file || out.PreplanCreated == nil || *out.PreplanCreated {
			t.Errorf("PreplanFile = %q, PreplanCreated = %v, want %q and false", out.PreplanFile, out.PreplanCreated, file)
		}
		if !strings.HasPrefix(out.Summary, "0 guardrail(s) loaded. Warning: Failed to read plan config: ") ||
			!strings.HasSuffix(out.Summary, ". Topic file 2026-10-01-auth-flow.md exists. Added 7 section heading(s): ## Goal, ## Problem and benefit, ## Users and effect, ## Risk if wrong, ## Non-goals, ## Flows, ## Decisions.") {
			t.Errorf("Summary = %q, want the read warning, the existing file and the added headings", out.Summary)
		}
		if want := "Read preplanFile: preplan_context added 7 section heading(s). Then continue with its first gap: the first empty section, then the open questions, if any. preplan_check writes the warning into the Guardrail check line at Step 4."; out.Next != want {
			t.Errorf("Next = %q, want %q", out.Next, want)
		}
		wantPreplanBytes(t, file, "# Preplan: auth flow\n"+preplanSkeletonTail)
	})

	t.Run("a complete existing file and a malformed config give the warning and no added heading", func(t *testing.T) {
		noPreplanCheckWrite(t)
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "[plan\nguardrails = [\n")
		complete := preplanSkeletonFor("auth flow")
		file := writePreplanTopic(t, root, "2026-10-01-auth-flow.md", complete)
		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		_, warning := loadGuardrails(root)
		if warning == "" {
			t.Fatal("loadGuardrails gave no warning for the malformed config")
		}
		if want := "0 guardrail(s) loaded. Warning: " + warning + ". Topic file 2026-10-01-auth-flow.md exists."; out.Summary != want {
			t.Errorf("Summary = %q, want %q", out.Summary, want)
		}
		if want := "Read preplanFile, then continue with its first gap: the first empty section, then the open questions, if any. preplan_check writes the warning into the Guardrail check line at Step 4."; out.Next != want {
			t.Errorf("Next = %q, want %q", out.Next, want)
		}
		if out.AddedSections == nil || len(*out.AddedSections) != 0 {
			t.Errorf("AddedSections = %v, want an empty non-nil list", out.AddedSections)
		}
		wantPreplanBytes(t, file, complete)
	})

	t.Run("served over MCP", func(t *testing.T) {
		root := newOpenspecStageFixture(t, guardrailsToml)
		text := evidenceRender(t, root, map[string]any{"action": "preplan_context", "topic": "auth flow"})
		if !strings.Contains(text, "auth-flow.md") {
			t.Errorf("rendered result lacks the topic file name:\n%s", text)
		}
		if !strings.Contains(text, "- preplanCreated: true") {
			t.Errorf("rendered result lacks preplanCreated: true:\n%s", text)
		}
		if !strings.Contains(text, "- addedSections: (none)") {
			t.Errorf("rendered result lacks addedSections: (none):\n%s", text)
		}
		if _, err := os.Stat(filepath.Join(root, ".sdlc-v2", "preplan", "2026-10-12-auth-flow.md")); err != nil {
			t.Errorf("topic file missing after the MCP call: %v", err)
		}
		again := evidenceRender(t, root, map[string]any{"action": "preplan_context", "topic": "auth flow"})
		if !strings.Contains(again, "- preplanCreated: false") {
			t.Errorf("second call must render preplanCreated: false:\n%s", again)
		}
		if !strings.Contains(again, "- addedSections: (none)") {
			t.Errorf("second call must render addedSections: (none):\n%s", again)
		}

		// An old file with 4 sections gets the 3 absent headings, and the
		// rendered result lists them.
		file := filepath.Join(root, ".sdlc-v2", "preplan", "2026-10-12-auth-flow.md")
		writeFile(t, file, "**Status:** in progress\n\n## Goal\n\n## Users and effect\n\n## Flows\n\n## Decisions\n")
		repaired := evidenceRender(t, root, map[string]any{"action": "preplan_context", "topic": "auth flow"})
		if want := "- addedSections:\n  - ## Problem and benefit\n  - ## Risk if wrong\n  - ## Non-goals\n"; !strings.Contains(repaired, want) {
			t.Errorf("repair call must render the added headings %q:\n%s", want, repaired)
		}
	})

	t.Run("a new file takes the UTC date and the UTC Started time", func(t *testing.T) {
		setPreplanNow(t, time.Date(2026, 10, 12, 23, 30, 0, 0, time.FixedZone("UTC-5", -5*60*60)))
		root := newOpenspecStageFixture(t, "")
		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		wantFile := filepath.Join(root, ".sdlc-v2", "preplan", "2026-10-13-auth-flow.md")
		if out.PreplanFile != wantFile {
			t.Errorf("PreplanFile = %q, want %q", out.PreplanFile, wantFile)
		}
		if out.PreplanCreated == nil || !*out.PreplanCreated {
			t.Errorf("PreplanCreated = %v, want true", out.PreplanCreated)
		}
		got, err := os.ReadFile(wantFile)
		if err != nil {
			t.Fatalf("read topic file: %v", err)
		}
		if !strings.Contains(string(got), "\n**Started:** 2026-10-13T04:30:00Z\n") {
			t.Errorf("topic file lacks the line **Started:** 2026-10-13T04:30:00Z:\n%s", got)
		}
		if want := " Created topic file 2026-10-13-auth-flow.md."; !strings.HasSuffix(out.Summary, want) {
			t.Errorf("Summary = %q, want the suffix %q", out.Summary, want)
		}
	})

	t.Run("an existing dated file is continued", func(t *testing.T) {
		root := newOpenspecStageFixture(t, "")
		file := filepath.Join(root, ".sdlc-v2", "preplan", "2026-10-01-auth-flow.md")
		custom := "# Preplan: auth flow\n\nOlder notes.\n" + preplanSkeletonTail
		writeFile(t, file, custom)
		noPreplanCheckWrite(t)

		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		if out.PreplanCreated == nil || *out.PreplanCreated {
			t.Errorf("PreplanCreated = %v, want false (set) for an existing dated file", out.PreplanCreated)
		}
		if out.PreplanFile != file {
			t.Errorf("PreplanFile = %q, want %q", out.PreplanFile, file)
		}
		if got, _ := os.ReadFile(file); string(got) != custom {
			t.Errorf("topic file = %q, want it unchanged (%q)", got, custom)
		}
		if want := " Topic file 2026-10-01-auth-flow.md exists."; !strings.HasSuffix(out.Summary, want) {
			t.Errorf("Summary = %q, want the suffix %q", out.Summary, want)
		}
		if names := preplanDirNames(t, filepath.Dir(file)); !reflect.DeepEqual(names, []string{"2026-10-01-auth-flow.md"}) {
			t.Errorf("preplan dir entries = %v, want only [2026-10-01-auth-flow.md]", names)
		}
	})

	t.Run("names that only look like the topic file do not match", func(t *testing.T) {
		root := newOpenspecStageFixture(t, "")
		dir := filepath.Join(root, ".sdlc-v2", "preplan")
		// The last two have the length of a real match: one fails the date
		// check, and one has a valid date but another slug.
		others := []string{"2026-1-12-auth-flow.md", "auth-flow.md.bak", "other-auth-flow.md",
			"2026-1a-12-auth-flow.md", "2026-10-12-auth-flaw.md"}
		for _, n := range others {
			writeFile(t, filepath.Join(dir, n), "# Preplan: other\n")
		}

		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		if want := filepath.Join(dir, "2026-10-12-auth-flow.md"); out.PreplanFile != want {
			t.Errorf("PreplanFile = %q, want %q", out.PreplanFile, want)
		}
		if out.PreplanCreated == nil || !*out.PreplanCreated {
			t.Errorf("PreplanCreated = %v, want true", out.PreplanCreated)
		}
		want := append([]string{"2026-10-12-auth-flow.md"}, others...)
		sort.Strings(want)
		if names := preplanDirNames(t, dir); !reflect.DeepEqual(names, want) {
			t.Errorf("preplan dir entries = %v, want %v", names, want)
		}
	})

	t.Run("an empty preplan folder gets a dated file", func(t *testing.T) {
		root := newOpenspecStageFixture(t, "")
		dir := filepath.Join(root, ".sdlc-v2", "preplan")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		if want := filepath.Join(dir, "2026-10-12-auth-flow.md"); out.PreplanFile != want {
			t.Errorf("PreplanFile = %q, want %q", out.PreplanFile, want)
		}
		if out.PreplanCreated == nil || !*out.PreplanCreated {
			t.Errorf("PreplanCreated = %v, want true", out.PreplanCreated)
		}
		if got, _ := os.ReadFile(out.PreplanFile); string(got) != preplanSkeletonFor("auth flow") {
			t.Errorf("topic file = %q, want the skeleton", got)
		}
	})

	t.Run("other actions omit preplanCreated", func(t *testing.T) {
		root := t.TempDir()
		text := evidenceRender(t, root, map[string]any{
			"action":      "merge_results",
			"laneResults": []any{map[string]any{"name": "lane-a", "status": "pass"}},
		})
		if !strings.HasPrefix(text, "# plan_support — ok") {
			t.Fatalf("merge_results failed:\n%s", text)
		}
		if strings.Contains(text, "preplanCreated") {
			t.Errorf("merge_results output renders preplanCreated:\n%s", text)
		}
		if strings.Contains(text, "addedSections") {
			t.Errorf("merge_results output renders addedSections:\n%s", text)
		}
	})
}

// TestPlanSupportPreplanContextErrors maps every bad topic to its DomainError
// and Suggestion with nothing written, and a failed create to its InfraError.
func TestPlanSupportPreplanContextErrors(t *testing.T) {
	setPreplanNow(t, preplanTestNow)
	const (
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
		{"empty", "", `preplan_context: topic "" has no ASCII letter or digit`, asciiSuggestion},
		{"blank", " \t ", `preplan_context: topic "" has no ASCII letter or digit`, asciiSuggestion},
		{"punctuation only", "-- !! --", `preplan_context: topic "-- !! --" has no ASCII letter or digit`, asciiSuggestion},
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
		// The os error names the folder, so the message names it once.
		if msg := err.Error(); !strings.HasPrefix(msg, "preplan_context: list: ") || strings.Count(msg, blocker) != 1 {
			t.Errorf("err.Error() = %q, want prefix \"preplan_context: list: \" and %s named once", msg, blocker)
		}
		if want := "Make " + blocker + " a folder that you can read: remove or rename the file or link at that path, or give your user read permission. Then run /sdlc:preplan again."; suggestionOf(err) != want {
			t.Errorf("Suggestion = %q, want %q", suggestionOf(err), want)
		}
		if got, _ := os.ReadFile(blocker); string(got) != "not a directory\n" {
			t.Errorf("blocking file = %q, want it unchanged", got)
		}
	})

	t.Run("a dangling link at the preplan dir path", func(t *testing.T) {
		root := t.TempDir()
		dataDir := filepath.Join(root, ".sdlc-v2")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dataDir, "preplan")
		target := filepath.Join(t.TempDir(), "main", ".sdlc-v2", "preplan")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}

		_, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if got := errorClassOf(err); got != "infra" {
			t.Errorf("error class = %q, want infra (err %v)", got, err)
		}
		wantMsg := "preplan_context: create " + filepath.Join(link, "2026-10-12-auth-flow.md") + ": " +
			link + " is a link to " + target + ", which does not exist."
		if err.Error() != wantMsg {
			t.Errorf("err.Error() = %q, want %q", err.Error(), wantMsg)
		}
		wantSuggestion := "Start a new session so the session-start hook repairs the link, or run: mkdir -p " + target
		if got := suggestionOf(err); got != wantSuggestion {
			t.Errorf("Suggestion = %q, want %q", got, wantSuggestion)
		}
		if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
			t.Errorf("the call created the link target (lstat err %v)", statErr)
		}
		if got, readErr := os.Readlink(link); readErr != nil || got != target {
			t.Errorf("link = (%q, %v), want it unchanged and pointing at %q", got, readErr, target)
		}
		entries, readErr := os.ReadDir(dataDir)
		if readErr != nil || len(entries) != 1 {
			t.Errorf("data dir entries = %v (err %v), want only the link", entries, readErr)
		}
	})

	t.Run("a dangling link at the topic file", func(t *testing.T) {
		root := t.TempDir()
		preplanDir := filepath.Join(root, ".sdlc-v2", "preplan")
		if err := os.MkdirAll(preplanDir, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(preplanDir, "auth-flow.md")
		target := filepath.Join(t.TempDir(), "elsewhere", "auth-flow.md")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}

		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err == nil {
			t.Fatalf("expected an error, got nil (preplanCreated %v)", out.PreplanCreated)
		}
		if got := errorClassOf(err); got != "infra" {
			t.Errorf("error class = %q, want infra (err %v)", got, err)
		}
		// The recovery is only in the Suggestion, so the message ends with the problem.
		wantMsg := "preplan_context: create " + link + ": " + link + " is a link to " + target + ", which does not exist."
		if err.Error() != wantMsg {
			t.Errorf("err.Error() = %q, want %q", err.Error(), wantMsg)
		}
		if want := "Remove the link, then try again: rm " + link; suggestionOf(err) != want {
			t.Errorf("Suggestion = %q, want %q", suggestionOf(err), want)
		}
		if _, statErr := os.Lstat(target); !errors.Is(statErr, fs.ErrNotExist) {
			t.Errorf("the call created the link target (lstat err %v)", statErr)
		}
		if got, readErr := os.Readlink(link); readErr != nil || got != target {
			t.Errorf("link = (%q, %v), want it unchanged and pointing at %q", got, readErr, target)
		}
		entries, readErr := os.ReadDir(preplanDir)
		if readErr != nil || len(entries) != 1 {
			t.Errorf("preplan dir entries = %v (err %v), want only the link", entries, readErr)
		}
	})

	t.Run("a chain of dangling links at the topic file names the topic file", func(t *testing.T) {
		root := t.TempDir()
		preplanDir := filepath.Join(root, ".sdlc-v2", "preplan")
		if err := os.MkdirAll(preplanDir, 0o755); err != nil {
			t.Fatal(err)
		}
		elsewhere := t.TempDir()
		file := filepath.Join(preplanDir, "auth-flow.md")
		middle := filepath.Join(elsewhere, "middle.md")
		target := filepath.Join(elsewhere, "gone", "auth-flow.md")
		if err := os.Symlink(target, middle); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(middle, file); err != nil {
			t.Fatal(err)
		}

		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err == nil {
			t.Fatalf("expected an error, got nil (preplanCreated %v)", out.PreplanCreated)
		}
		if got := errorClassOf(err); got != "infra" {
			t.Errorf("error class = %q, want infra (err %v)", got, err)
		}
		wantMsg := "preplan_context: create " + file + ": " + file + " resolves through links to " + target + ", which does not exist."
		if err.Error() != wantMsg {
			t.Errorf("err.Error() = %q, want %q", err.Error(), wantMsg)
		}
		if want := "Remove the link, then try again: rm " + file; suggestionOf(err) != want {
			t.Errorf("Suggestion = %q, want %q", suggestionOf(err), want)
		}
		if got, readErr := os.Readlink(file); readErr != nil || got != middle {
			t.Errorf("topic link = (%q, %v), want it unchanged and pointing at %q", got, readErr, middle)
		}
		if got, readErr := os.Readlink(middle); readErr != nil || got != target {
			t.Errorf("middle link = (%q, %v), want it unchanged and pointing at %q", got, readErr, target)
		}
		if _, statErr := os.Lstat(filepath.Dir(target)); !errors.Is(statErr, fs.ErrNotExist) {
			t.Errorf("the call created the link target folder (lstat err %v)", statErr)
		}
	})

	t.Run("a preplan folder without read permission", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores directory modes")
		}
		root := t.TempDir()
		dir := filepath.Join(root, ".sdlc-v2", "preplan")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o300); err != nil {
			t.Fatalf("remove read permission: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

		_, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if got := errorClassOf(err); got != "infra" {
			t.Errorf("error class = %q, want infra (err %v)", got, err)
		}
		if !errors.Is(err, fs.ErrPermission) {
			t.Errorf("err = %v, want it to wrap fs.ErrPermission", err)
		}
		if msg := err.Error(); !strings.HasPrefix(msg, "preplan_context: list: ") || strings.Count(msg, dir) != 1 {
			t.Errorf("err.Error() = %q, want prefix \"preplan_context: list: \" and %s named once", msg, dir)
		}
		if want := "Make " + dir + " a folder that you can read: remove or rename the file or link at that path, or give your user read permission. Then run /sdlc:preplan again."; suggestionOf(err) != want {
			t.Errorf("Suggestion = %q, want %q", suggestionOf(err), want)
		}
		_ = os.Chmod(dir, 0o755)
		if names := preplanDirNames(t, dir); len(names) != 0 {
			t.Errorf("preplan dir entries = %v, want none", names)
		}
	})

	t.Run("a preplan folder with more than preplanDirMax names", func(t *testing.T) {
		old := preplanDirMax
		preplanDirMax = 2
		t.Cleanup(func() { preplanDirMax = old })
		root := t.TempDir()
		dir := filepath.Join(root, ".sdlc-v2", "preplan")
		for _, n := range []string{"a.md", "b.md", "c.md"} {
			writeFile(t, filepath.Join(dir, n), "# Preplan: x\n")
		}

		_, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if got := errorClassOf(err); got != "infra" {
			t.Errorf("error class = %q, want infra (err %v)", got, err)
		}
		if !errors.Is(err, errPreplanDirFull) {
			t.Errorf("err = %v, want it to wrap errPreplanDirFull", err)
		}
		if want := "preplan_context: list " + dir + ": the folder holds more than 2 names"; err.Error() != want {
			t.Errorf("err.Error() = %q, want %q", err.Error(), want)
		}
		if want := "Move old topic files out of " + dir + " until it holds at most 2 names. Then run /sdlc:preplan again."; suggestionOf(err) != want {
			t.Errorf("Suggestion = %q, want %q", suggestionOf(err), want)
		}
		if names := preplanDirNames(t, dir); !reflect.DeepEqual(names, []string{"a.md", "b.md", "c.md"}) {
			t.Errorf("preplan dir entries = %v, want only [a.md b.md c.md]", names)
		}
	})

	t.Run("a preplan folder with exactly preplanDirMax names", func(t *testing.T) {
		old := preplanDirMax
		preplanDirMax = 2
		t.Cleanup(func() { preplanDirMax = old })
		root := t.TempDir()
		dir := filepath.Join(root, ".sdlc-v2", "preplan")
		for _, n := range []string{"a.md", "b.md"} {
			writeFile(t, filepath.Join(dir, n), "# Preplan: x\n")
		}

		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		if out.PreplanCreated == nil || !*out.PreplanCreated {
			t.Errorf("PreplanCreated = %v, want true", out.PreplanCreated)
		}
		if names := preplanDirNames(t, dir); !reflect.DeepEqual(names, []string{"2026-10-12-auth-flow.md", "a.md", "b.md"}) {
			t.Errorf("preplan dir entries = %v, want the new topic file, a.md and b.md", names)
		}
	})

	// A regular file in place of the data folder makes the open of the
	// preplan folder fail with "not a directory". Unlike a folder mode, this
	// fails for root too, so the open error branch is covered on every runner.
	t.Run("a regular file in place of the data folder", func(t *testing.T) {
		root := t.TempDir()
		blocker := filepath.Join(root, ".sdlc-v2")
		writeFile(t, blocker, "not a directory\n")
		dir := filepath.Join(blocker, "preplan")

		_, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if got := errorClassOf(err); got != "infra" {
			t.Errorf("error class = %q, want infra (err %v)", got, err)
		}
		if msg := err.Error(); !strings.HasPrefix(msg, "preplan_context: list: open "+dir+": ") {
			t.Errorf("err.Error() = %q, want prefix %q", msg, "preplan_context: list: open "+dir+": ")
		}
		if want := "Make " + dir + " a folder that you can read: remove or rename the file or link at that path, or give your user read permission. Then run /sdlc:preplan again."; suggestionOf(err) != want {
			t.Errorf("Suggestion = %q, want %q", suggestionOf(err), want)
		}
		if got, _ := os.ReadFile(blocker); string(got) != "not a directory\n" {
			t.Errorf("blocking file = %q, want it unchanged", got)
		}
	})

	t.Run("two topic files for one slug", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, ".sdlc-v2", "preplan")
		writeFile(t, filepath.Join(dir, "auth-flow.md"), "# Preplan: auth flow\n")
		writeFile(t, filepath.Join(dir, "2026-10-01-auth-flow.md"), "# Preplan: auth flow\n")

		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err == nil {
			t.Fatalf("expected an error, got nil (preplanCreated %v)", out.PreplanCreated)
		}
		if got := errorClassOf(err); got != "data" {
			t.Errorf("error class = %q, want data (err %v)", got, err)
		}
		wantMsg := `preplan_context: topic "auth flow" matches 2 topic files in ` + dir + ": 2026-10-01-auth-flow.md, auth-flow.md"
		if err.Error() != wantMsg {
			t.Errorf("err.Error() = %q, want %q", err.Error(), wantMsg)
		}
		if want := "Rename or delete one of: 2026-10-01-auth-flow.md, auth-flow.md. Then run /sdlc:preplan again."; suggestionOf(err) != want {
			t.Errorf("Suggestion = %q, want %q", suggestionOf(err), want)
		}
		if names := preplanDirNames(t, dir); !reflect.DeepEqual(names, []string{"2026-10-01-auth-flow.md", "auth-flow.md"}) {
			t.Errorf("preplan dir entries = %v, want only the two topic files", names)
		}
	})

	notAFile := []struct {
		name    string
		setup   func(t *testing.T, file string)
		wantMsg func(file string) string
		// wantSuggestion is the same recovery text that preplan_check gives.
		wantSuggestion func(file string) string
	}{
		{"a symlink loop at the topic file", func(t *testing.T, file string) {
			other := filepath.Join(filepath.Dir(file), "other.md")
			if err := os.Symlink(other, file); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(file, other); err != nil {
				t.Fatal(err)
			}
		}, func(file string) string {
			return "preplan_context: " + file + " is a symlink whose target cannot be read: stat " + file + ": too many levels of symbolic links"
		}, func(file string) string {
			return "Remove the link with rm " + file + ", then run /sdlc:preplan again. preplan_context then creates a new topic file."
		}},
		{"a folder at the topic file", func(t *testing.T, file string) {
			if err := os.Mkdir(file, 0o755); err != nil {
				t.Fatal(err)
			}
		}, func(file string) string {
			return "preplan_context: " + file + " is a folder, not a regular file"
		}, func(file string) string {
			return "Remove or rename " + file + ", then run /sdlc:preplan again. preplan_context then creates a new topic file."
		}},
	}
	// Each case runs for the dated name (the default) and the undated name.
	for _, tc := range notAFile {
		for _, name := range []string{"2026-10-12-auth-flow.md", "auth-flow.md"} {
			t.Run(tc.name+" ("+name+") is a DomainError, not an existing topic file", func(t *testing.T) {
				root := t.TempDir()
				preplanDir := filepath.Join(root, ".sdlc-v2", "preplan")
				if err := os.MkdirAll(preplanDir, 0o755); err != nil {
					t.Fatal(err)
				}
				file := filepath.Join(preplanDir, name)
				tc.setup(t, file)

				out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
				if err == nil {
					t.Fatalf("expected an error, got nil (preplanCreated %v)", out.PreplanCreated)
				}
				if got := errorClassOf(err); got != "domain" {
					t.Errorf("error class = %q, want domain (err %v)", got, err)
				}
				if want := tc.wantMsg(file); err.Error() != want {
					t.Errorf("err.Error() = %q, want %q", err.Error(), want)
				}
				if want := tc.wantSuggestion(file); suggestionOf(err) != want {
					t.Errorf("Suggestion = %q, want %q", suggestionOf(err), want)
				}
			})
		}
	}
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
	setPreplanNow(t, preplanTestNow)
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
	if out.PreplanCreated != nil || out.PreplanFile != "" {
		t.Errorf("output = {created %v, file %q}, want {nil, \"\"}", out.PreplanCreated, out.PreplanFile)
	}
	if names := preplanDirNames(t, filepath.Join(root, ".sdlc-v2", "preplan")); len(names) != 0 {
		t.Errorf("preplan folder holds %v, want none (a topic file left behind)", names)
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

// ---------------------------------------------------------------------------
// preplan_check
// ---------------------------------------------------------------------------

// preplanCheckTestNow is the clock the preplan_check tests set with
// setPreplanNow.
var preplanCheckTestNow = time.Date(2026, 10, 21, 17, 5, 0, 0, time.UTC)

// preplanFakeInfo is an fs.FileInfo with a chosen mode, for the Lstat seam
// of preplan_check.
type preplanFakeInfo struct{ mode fs.FileMode }

func (f preplanFakeInfo) Name() string       { return "auth-flow.md" }
func (f preplanFakeInfo) Size() int64        { return 0 }
func (f preplanFakeInfo) Mode() fs.FileMode  { return f.mode }
func (f preplanFakeInfo) ModTime() time.Time { return time.Time{} }
func (f preplanFakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f preplanFakeInfo) Sys() any           { return nil }

// preplanCheckIn returns a preplan_check input for topic with the three
// counts set.
func preplanCheckIn(topic string, pass, kept, dropped int) PlanSupportIn {
	return PlanSupportIn{Action: "preplan_check", Topic: topic, PassCount: &pass, KeptCount: &kept, DroppedCount: &dropped}
}

// writePreplanTopic writes content to the topic file name in the preplan
// folder of root and returns the path of the file.
func writePreplanTopic(t *testing.T, root, name, content string) string {
	t.Helper()
	file := filepath.Join(root, ".sdlc-v2", "preplan", name)
	writeFile(t, file, content)
	return file
}

// wantPreplanBytes fails the test when file does not hold exactly want.
func wantPreplanBytes(t *testing.T, file, want string) {
	t.Helper()
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	if string(got) != want {
		t.Errorf("%s =\n%q\nwant\n%q", file, got, want)
	}
}

// swapPreplanSeam sets *seam to v for one test.
func swapPreplanSeam[T any](t *testing.T, seam *T, v T) {
	t.Helper()
	old := *seam
	*seam = v
	t.Cleanup(func() { *seam = old })
}

// noPreplanCheckWrite makes a call of preplanWriteBytes fail the test: a
// refused call must not write.
func noPreplanCheckWrite(t *testing.T) {
	t.Helper()
	swapPreplanSeam(t, &preplanWriteBytes, func(path string, _ []byte) error {
		t.Errorf("preplanWriteBytes(%s) was called, want no write", path)
		return nil
	})
}

// preplanGuardrailsToml returns a config.toml with n plan guardrails.
func preplanGuardrailsToml(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "[plan.guardrails.g%02d]\ndescription = \"rule %d\"\n\n", i, i)
	}
	return b.String()
}

// preplanFiller returns n bytes (n >= 1) that end in a line feed.
func preplanFiller(n int) string {
	return strings.Repeat("x", n-1) + "\n"
}

// TestSetGuardrailCheckLine covers each case of setGuardrailCheckLine: a
// replace keeps every other byte and returns the old line, an insert goes
// after the Started line, else after the Status line, and only the first
// dashboardPreplanHeadMax bytes are searched for each anchor.
func TestSetGuardrailCheckLine(t *testing.T) {
	const line = "**Guardrail check:** L"
	const top = "**Started:** T\n"
	const old = "**Guardrail check:** old"
	const status = "**Status:** s\n"
	const startedPrefix = "**Started:**"
	endsAt4096 := preplanFiller(dashboardPreplanHeadMax - len(top) - len(old))
	startsAt4000 := preplanFiller(4000 - len(top))
	longOld := "**Guardrail check:** " + strings.Repeat("o", 200)
	startsAt4096 := preplanFiller(dashboardPreplanHeadMax - len(top))
	// The Started prefix ends at byte 4096, or starts there, after a Status
	// line at the top.
	startedEndsAt4096 := preplanFiller(dashboardPreplanHeadMax - len(status) - len(startedPrefix))
	startedStartsAt4096 := preplanFiller(dashboardPreplanHeadMax - len(status))

	for _, tc := range []struct {
		name   string
		before string
		after  string
		prior  string
		ok     bool
	}{
		{"no check line, Started line",
			"**Started:** T\n\n## Goal",
			"**Started:** T\n\n**Guardrail check:** L\n\n## Goal", "", true},
		{"no check line, Started line, CR LF file",
			"**Started:** T\r\n\r\n## Goal",
			"**Started:** T\r\n\r\n**Guardrail check:** L\r\n\r\n## Goal", "", true},
		{"no check line, no Started line",
			"**Status:** s\n\n## Goal",
			"**Status:** s\n\n**Guardrail check:** L\n\n## Goal", "", true},
		{"no check line, no Started line, CR LF file",
			"**Status:** s\r\n\r\n## Goal",
			"**Status:** s\r\n\r\n**Guardrail check:** L\r\n\r\n## Goal", "", true},
		{"no check line, Status line is the last line with no line end",
			"**Status:** s",
			"**Status:** s\n\n**Guardrail check:** L\n", "", true},
		{"no check line, Started line after the Status line",
			"# Preplan: x\n\n**Status:** s\n\n**Started:** T\n\n## Goal\n",
			"# Preplan: x\n\n**Status:** s\n\n**Started:** T\n\n**Guardrail check:** L\n\n## Goal\n", "", true},
		{"no check line, Started line is the last line with no line end",
			"**Started:** T",
			"**Started:** T\n\n**Guardrail check:** L\n", "", true},
		{"Started prefix ends at byte 4096 exactly",
			status + startedEndsAt4096 + "**Started:** T\n## Goal\n",
			status + startedEndsAt4096 + "**Started:** T\n\n" + line + "\n## Goal\n", "", true},
		{"Started line starts at byte 4096, Status line in the head",
			status + startedStartsAt4096 + "**Started:** T\n",
			status + "\n" + line + "\n" + startedStartsAt4096 + "**Started:** T\n", "", true},
		{"check line exists",
			"**Guardrail check:** old\n",
			"**Guardrail check:** L\n", old, true},
		{"check line exists, no Status line",
			"# Preplan: x\n\n**Guardrail check:** old\n",
			"# Preplan: x\n\n**Guardrail check:** L\n", old, true},
		{"check line exists, CR LF file",
			"**Status:** s\r\n\r\n**Guardrail check:** old\r\n\r\n## Goal\r\n",
			"**Status:** s\r\n\r\n**Guardrail check:** L\r\n\r\n## Goal\r\n", old, true},
		{"check line is the last line with no line end",
			"**Status:** s\n\n**Guardrail check:** old",
			"**Status:** s\n\n**Guardrail check:** L", old, true},
		{"two check lines in the head",
			"**Status:** s\n\n**Guardrail check:** one\n**Guardrail check:** two\n",
			"**Status:** s\n\n**Guardrail check:** L\n**Guardrail check:** two\n", "**Guardrail check:** one", true},
		{"check line ends at byte 4096 exactly",
			top + endsAt4096 + old + "\n## Goal\n",
			top + endsAt4096 + line + "\n## Goal\n", old, true},
		{"check line starts before byte 4096 and ends after it",
			top + startsAt4000 + longOld + "\n## Goal\n",
			top + startsAt4000 + line + "\n## Goal\n", longOld, true},
		{"check line starts at byte 4096",
			top + startsAt4096 + old + "\n",
			top + "\n" + line + "\n" + startsAt4096 + old + "\n", "", true},
		{"no check line, no Started line, no Status line",
			"# Preplan: x\n\n## Goal\n", "", "", false},
		{"Status line starts after the head",
			"# Preplan: x\n" + preplanFiller(dashboardPreplanHeadMax) + "**Status:** s\n", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.before)
			next, prior, ok := setGuardrailCheckLine(data, line)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if string(next) != tc.after {
				t.Errorf("next =\n%q\nwant\n%q", next, tc.after)
			}
			if prior != tc.prior {
				t.Errorf("prior = %q, want %q", prior, tc.prior)
			}
			if string(data) != tc.before {
				t.Errorf("setGuardrailCheckLine changed its input")
			}
		})
	}

	// The 4096-byte cases depend on the filler lengths above.
	if i := strings.Index(top+endsAt4096+old, old); i+len(old) != dashboardPreplanHeadMax {
		t.Errorf("the old check line ends at byte %d, want %d", i+len(old), dashboardPreplanHeadMax)
	}
	if n := len(top + startsAt4096); n != dashboardPreplanHeadMax {
		t.Errorf("the late check line starts at byte %d, want %d", n, dashboardPreplanHeadMax)
	}
	if n := len(status + startedEndsAt4096 + startedPrefix); n != dashboardPreplanHeadMax {
		t.Errorf("the Started prefix ends at byte %d, want %d", n, dashboardPreplanHeadMax)
	}
	if n := len(status + startedStartsAt4096); n != dashboardPreplanHeadMax {
		t.Errorf("the late Started line starts at byte %d, want %d", n, dashboardPreplanHeadMax)
	}
}

// TestPlanSupportPreplanCheck verifies a valid preplan_check call writes the
// check line in the topic file and returns it.
func TestPlanSupportPreplanCheck(t *testing.T) {
	setPreplanNow(t, preplanCheckTestNow)
	const startedLine = "**Started:** 2026-10-12T12:00:00Z\n"
	withLine := func(line string) string {
		return strings.Replace(preplanSkeletonFor("auth flow"), startedLine, startedLine+"\n"+line+"\n", 1)
	}
	// A new topic file holds 7 empty sections: the summary and next name them.
	const (
		skeletonEmpty = " Empty sections: ## Goal, ## Problem and benefit, ## Users and effect, ## Risk if wrong, ## Non-goals, ## Flows, ## Decisions."
		skeletonNext  = "Read preplanFile again before your next edit: preplan_check rewrote it. Then follow the Step 4 route table." +
			" emptySections lists 7 empty section(s). The Step 4 route table of the skill picks the next step."
	)

	t.Run("writes the check line with 53 guardrails, then replaces it", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".sdlc-v2", "config.toml"), preplanGuardrailsToml(53))
		file := writePreplanTopic(t, root, "2026-10-12-auth-flow.md", preplanSkeletonFor("auth flow"))

		out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 6, 1, 0))
		if err != nil {
			t.Fatalf("preplan_check: %v", err)
		}
		const want = "**Guardrail check:** 53 guardrails, 7 proposals: 6 pass, 1 kept, 0 dropped. Last check: 2026-10-21T17:05:00Z."
		if out.GuardrailCheckLine != want {
			t.Errorf("GuardrailCheckLine = %q, want %q", out.GuardrailCheckLine, want)
		}
		if out.PreplanFile != file {
			t.Errorf("PreplanFile = %q, want %q", out.PreplanFile, file)
		}
		if want := "Added the Guardrail check line to 2026-10-12-auth-flow.md." + skeletonEmpty; out.Summary != want {
			t.Errorf("Summary = %q, want %q", out.Summary, want)
		}
		if out.Next != skeletonNext {
			t.Errorf("Next = %q, want %q", out.Next, skeletonNext)
		}
		if out.EmptySections == nil || !reflect.DeepEqual(*out.EmptySections, preplanHeadings(preplanSections...)) {
			t.Errorf("EmptySections = %v, want the 7 headings of a skeleton file", out.EmptySections)
		}
		if out.PreplanCreated != nil || out.Guardrails != nil {
			t.Errorf("PreplanCreated = %v, Guardrails = %v, want both unset", out.PreplanCreated, out.Guardrails)
		}
		wantPreplanBytes(t, file, withLine(want))

		out, err = planSupportCore(root, root, preplanCheckIn("auth flow", 2, 0, 1))
		if err != nil {
			t.Fatalf("second preplan_check: %v", err)
		}
		const second = "**Guardrail check:** 53 guardrails, 3 proposals: 2 pass, 0 kept, 1 dropped. Last check: 2026-10-21T17:05:00Z."
		if out.GuardrailCheckLine != second {
			t.Errorf("second GuardrailCheckLine = %q, want %q", out.GuardrailCheckLine, second)
		}
		if want := "Replaced the Guardrail check line of 2026-10-12-auth-flow.md. Old line: " + want + skeletonEmpty; out.Summary != want {
			t.Errorf("second Summary = %q, want %q", out.Summary, want)
		}
		wantPreplanBytes(t, file, withLine(second))
	})

	t.Run("no guardrails configured", func(t *testing.T) {
		root := t.TempDir()
		file := writePreplanTopic(t, root, "2026-10-12-auth-flow.md", preplanSkeletonFor("auth flow"))
		out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 2, 0, 0))
		if err != nil {
			t.Fatalf("preplan_check: %v", err)
		}
		const want = "**Guardrail check:** 0 guardrails, 2 proposals: 2 pass, 0 kept, 0 dropped. Last check: 2026-10-21T17:05:00Z."
		if out.GuardrailCheckLine != want {
			t.Errorf("GuardrailCheckLine = %q, want %q", out.GuardrailCheckLine, want)
		}
		wantPreplanBytes(t, file, withLine(want))
	})

	t.Run("malformed config writes the warning on one line", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "[plan\nguardrails = [\n")
		file := writePreplanTopic(t, root, "2026-10-12-auth-flow.md", preplanSkeletonFor("auth flow"))
		_, warning := loadGuardrails(root)
		if warning == "" {
			t.Fatal("loadGuardrails gave no warning for a malformed config")
		}
		w := preplanWarningText(warning)
		want := "**Guardrail check:** 0 guardrails, 2 proposals: 2 pass, 0 kept, 0 dropped. Last check: 2026-10-21T17:05:00Z. Guardrails unavailable: " + w + "."

		out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 2, 0, 0))
		if err != nil {
			t.Fatalf("preplan_check: %v", err)
		}
		if out.GuardrailCheckLine != want {
			t.Errorf("GuardrailCheckLine = %q, want %q", out.GuardrailCheckLine, want)
		}
		if !strings.HasPrefix(w, "Failed to read plan config: ") {
			t.Errorf("warning text = %q, want the loadGuardrails read warning", w)
		}
		if strings.ContainsAny(out.GuardrailCheckLine, "\r\n") || strings.HasSuffix(out.GuardrailCheckLine, "..") {
			t.Errorf("GuardrailCheckLine = %q, want one line with one final period", out.GuardrailCheckLine)
		}
		if want := "Added the Guardrail check line to 2026-10-12-auth-flow.md. Guardrails unavailable: " + w + "." + skeletonEmpty; out.Summary != want {
			t.Errorf("Summary = %q, want %q", out.Summary, want)
		}
		wantPreplanBytes(t, file, withLine(want))
	})

	t.Run("all three counts 0", func(t *testing.T) {
		root := t.TempDir()
		writePreplanTopic(t, root, "2026-10-12-auth-flow.md", preplanSkeletonFor("auth flow"))
		out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 0, 0, 0))
		if err != nil {
			t.Fatalf("preplan_check: %v", err)
		}
		if !strings.Contains(out.GuardrailCheckLine, " 0 proposals: 0 pass, 0 kept, 0 dropped.") {
			t.Errorf("GuardrailCheckLine = %q, want 0 proposals", out.GuardrailCheckLine)
		}
	})

	t.Run("a clock that is not in UTC gives a time in UTC", func(t *testing.T) {
		setPreplanNow(t, time.Date(2026, 10, 21, 19, 5, 0, 0, time.FixedZone("UTC+2", 2*60*60)))
		root := t.TempDir()
		writePreplanTopic(t, root, "2026-10-12-auth-flow.md", preplanSkeletonFor("auth flow"))
		out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
		if err != nil {
			t.Fatalf("preplan_check: %v", err)
		}
		if !strings.HasSuffix(out.GuardrailCheckLine, " Last check: 2026-10-21T17:05:00Z.") {
			t.Errorf("GuardrailCheckLine = %q, want the time 2026-10-21T17:05:00Z", out.GuardrailCheckLine)
		}
	})

	for _, name := range []string{"auth-flow.md", "2026-10-01-auth-flow.md"} {
		t.Run("finds the topic file "+name, func(t *testing.T) {
			root := t.TempDir()
			file := writePreplanTopic(t, root, name, preplanSkeletonFor("auth flow"))
			out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
			if err != nil {
				t.Fatalf("preplan_check: %v", err)
			}
			if out.PreplanFile != file {
				t.Errorf("PreplanFile = %q, want %q", out.PreplanFile, file)
			}
			wantPreplanBytes(t, file, withLine(out.GuardrailCheckLine))
		})
	}

	t.Run("the main root holds the topic file, not the content root", func(t *testing.T) {
		mainRoot, contentRoot := t.TempDir(), t.TempDir()
		file := writePreplanTopic(t, mainRoot, "2026-10-12-auth-flow.md", preplanSkeletonFor("auth flow"))
		out, err := planSupportCore(mainRoot, contentRoot, preplanCheckIn("auth flow", 1, 0, 0))
		if err != nil {
			t.Fatalf("preplan_check: %v", err)
		}
		if out.PreplanFile != file {
			t.Errorf("PreplanFile = %q, want %q", out.PreplanFile, file)
		}
		wantPreplanBytes(t, file, withLine(out.GuardrailCheckLine))
		if _, err := os.Stat(filepath.Join(contentRoot, ".sdlc-v2")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the call wrote under the content root (stat err %v)", err)
		}
	})

	t.Run("a preplan folder with exactly preplanDirMax names", func(t *testing.T) {
		swapPreplanSeam(t, &preplanDirMax, 2)
		root := t.TempDir()
		file := writePreplanTopic(t, root, "auth-flow.md", preplanSkeletonFor("auth flow"))
		writePreplanTopic(t, root, "other.md", "# Preplan: other\n")
		if _, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0)); err != nil {
			t.Fatalf("preplan_check: %v", err)
		}
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if !strings.Contains(string(got), "**Guardrail check:** ") {
			t.Errorf("topic file =\n%s\nwant a check line", got)
		}
	})

	t.Run("a topic file of exactly 8 MiB", func(t *testing.T) {
		root := t.TempDir()
		head := "**Status:** in progress\n"
		file := writePreplanTopic(t, root, "auth-flow.md", head+preplanFiller(dashboardDeleteReadMax-len(head)))
		out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
		if err != nil {
			t.Fatalf("preplan_check: %v", err)
		}
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if want := head + "\n" + out.GuardrailCheckLine + "\n"; !strings.HasPrefix(string(got), want) {
			t.Errorf("topic file starts with %q, want %q", got[:min(len(got), len(want))], want)
		}
	})

	t.Run("the largest counts are accepted", func(t *testing.T) {
		root := t.TempDir()
		writePreplanTopic(t, root, "auth-flow.md", preplanSkeletonFor("auth flow"))
		out, err := planSupportCore(root, root, preplanCheckIn("auth flow", preplanCountMax, preplanCountMax, preplanCountMax))
		if err != nil {
			t.Fatalf("preplan_check: %v", err)
		}
		if want := " 30000 proposals: 10000 pass, 10000 kept, 10000 dropped."; !strings.Contains(out.GuardrailCheckLine, want) {
			t.Errorf("GuardrailCheckLine = %q, want it to hold %q", out.GuardrailCheckLine, want)
		}
	})

	t.Run("other check lines that stay in the file are counted", func(t *testing.T) {
		root := t.TempDir()
		const late = "**Guardrail check:** late\n"
		content := withLine("**Guardrail check:** old") + preplanFiller(dashboardPreplanHeadMax) + late + late
		file := writePreplanTopic(t, root, "auth-flow.md", content)
		out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
		if err != nil {
			t.Fatalf("preplan_check: %v", err)
		}
		want := "Replaced the Guardrail check line of auth-flow.md. Old line: **Guardrail check:** old 2 more Guardrail check line(s) stay in the file. No preplan action removes them: tell the user to delete them by hand. Do not edit the file." + skeletonEmpty
		if out.Summary != want {
			t.Errorf("Summary = %q, want %q", out.Summary, want)
		}
		wantPreplanBytes(t, file, strings.Replace(content, "**Guardrail check:** old", out.GuardrailCheckLine, 1))
	})

	t.Run("every section has content: emptySections is none and next stays", func(t *testing.T) {
		root := t.TempDir()
		file := writePreplanTopic(t, root, "auth-flow.md", preplanTopicWithBodies(nil))
		out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
		if err != nil {
			t.Fatalf("preplan_check: %v", err)
		}
		if want := "Added the Guardrail check line to auth-flow.md. Empty sections: none."; out.Summary != want {
			t.Errorf("Summary = %q, want %q", out.Summary, want)
		}
		if want := "Read preplanFile again before your next edit: preplan_check rewrote it. Then follow the Step 4 route table."; out.Next != want {
			t.Errorf("Next = %q, want %q", out.Next, want)
		}
		if out.EmptySections == nil || len(*out.EmptySections) != 0 {
			t.Errorf("EmptySections = %v, want an empty non-nil list", out.EmptySections)
		}
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(raw), `"emptySections":[]`) {
			t.Errorf("output JSON = %s, want an emptySections field with an empty list", raw)
		}
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if strings.Count(string(got), "**Guardrail check:** ") != 1 {
			t.Errorf("topic file =\n%s\nwant one check line", got)
		}
	})

	t.Run("2 empty sections are named in the summary and counted in next", func(t *testing.T) {
		root := t.TempDir()
		writePreplanTopic(t, root, "auth-flow.md", preplanTopicWithBodies(map[string]string{"Risk if wrong": "\n", "Non-goals": " \t\n"}))
		out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
		if err != nil {
			t.Fatalf("preplan_check: %v", err)
		}
		if want := "Added the Guardrail check line to auth-flow.md. Empty sections: ## Risk if wrong, ## Non-goals."; out.Summary != want {
			t.Errorf("Summary = %q, want %q", out.Summary, want)
		}
		want := "Read preplanFile again before your next edit: preplan_check rewrote it. Then follow the Step 4 route table." +
			" emptySections lists 2 empty section(s). The Step 4 route table of the skill picks the next step."
		if out.Next != want {
			t.Errorf("Next = %q, want %q", out.Next, want)
		}
		if want := preplanHeadings("Risk if wrong", "Non-goals"); out.EmptySections == nil || !reflect.DeepEqual(*out.EmptySections, want) {
			t.Errorf("EmptySections = %v, want %q", out.EmptySections, want)
		}
	})

	t.Run("a heading that the file lacks is an empty section; the call adds no heading", func(t *testing.T) {
		root := t.TempDir()
		content := strings.Replace(preplanTopicWithBodies(nil), "## Non-goals\n\ntext\n\n", "", 1)
		file := writePreplanTopic(t, root, "auth-flow.md", content)
		out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
		if err != nil {
			t.Fatalf("preplan_check: %v", err)
		}
		if want := preplanHeadings("Non-goals"); out.EmptySections == nil || !reflect.DeepEqual(*out.EmptySections, want) {
			t.Errorf("EmptySections = %v, want %q", out.EmptySections, want)
		}
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if strings.Contains(string(got), "## Non-goals") {
			t.Errorf("topic file =\n%s\nwant no Non-goals heading: only preplan_context adds headings", got)
		}
	})
}

// preplanTopicWithBodies returns a topic file with every preplanSections
// section. Each body is "text", and the Decisions body is the table header
// and one row. A body in over replaces the body of that section.
func preplanTopicWithBodies(over map[string]string) string {
	var b strings.Builder
	b.WriteString("**Status:** in progress\n\n")
	for _, name := range preplanSections {
		body := "text\n"
		if name == preplanDecisionsSection {
			body = preplanDecisionsHeader + "| 1 | pick A | because |\n"
		}
		if v, ok := over[name]; ok {
			body = v
		}
		b.WriteString(preplanHeadingPrefix + name + "\n\n" + body + "\n")
	}
	return b.String()
}

// TestPreplanEmptySections covers each input of the empty-list test table of
// the plan and each rule of preplanEmptySections. Every case also runs in
// CRLF form and with the last line end removed: the list must stay the same.
func TestPreplanEmptySections(t *testing.T) {
	const (
		hdr    = preplanDecisionsHeader
		row    = "| 1 | pick A | because |\n"
		fenced = "```\n## Goal\n```\n"
	)
	all := preplanHeadings(preplanSections...)
	except := func(names ...string) []string {
		var out []string
		for _, h := range all {
			keep := true
			for _, n := range names {
				if h == preplanHeadingPrefix+n {
					keep = false
				}
			}
			if keep {
				out = append(out, h)
			}
		}
		return out
	}
	// without returns a full topic file with the heading and the body of one section removed.
	without := func(name string) string {
		text := preplanTopicWithBodies(nil)
		i := strings.Index(text, preplanHeadingPrefix+name+"\n")
		if i < 0 {
			t.Fatalf("no %s section in the test file", name)
		}
		if j := strings.Index(text[i+len(preplanHeadingPrefix):], "\n"+preplanHeadingPrefix); j >= 0 {
			return text[:i] + text[i+len(preplanHeadingPrefix)+j+1:]
		}
		return text[:i]
	}

	for _, tc := range []struct {
		name string
		in   string
		want []string
	}{
		{"an empty file", "", all},
		{"a new skeleton file", preplanSkeletonFor("auth flow"), all},
		{"content in every section and one Decisions row", preplanTopicWithBodies(nil), nil},
		{"absent heading", without("Risk if wrong"), preplanHeadings("Risk if wrong")},
		{"absent Decisions heading", without("Decisions"), preplanHeadings("Decisions")},
		{"body of blank lines and spaces", preplanTopicWithBodies(map[string]string{"Non-goals": " \t\n\n   \n"}), preplanHeadings("Non-goals")},
		{"text after the blank lines", preplanTopicWithBodies(map[string]string{"Flows": "\n\n  \nx\n"}), nil},
		{"Decisions table with only the header", preplanTopicWithBodies(map[string]string{"Decisions": hdr}), preplanHeadings("Decisions")},
		{"Decisions body with only the Smallest cut line", preplanTopicWithBodies(map[string]string{"Decisions": "Smallest cut: all of it.\n"}), preplanHeadings("Decisions")},
		{"Decisions header and a Smallest cut line", preplanTopicWithBodies(map[string]string{"Decisions": hdr + "\nSmallest cut: all of it.\n"}), preplanHeadings("Decisions")},
		{"Decisions with one data row", preplanTopicWithBodies(map[string]string{"Decisions": hdr + row}), nil},
		{"Decisions last with a data row and no final blank line", "## Decisions\n\n" + hdr + row, except("Decisions")},
		{"Decisions last with the header only and no final blank line", "## Decisions\n\n" + hdr, all},
		{"Decisions with a Smallest cut line and one data row", preplanTopicWithBodies(map[string]string{"Decisions": hdr + row + "\nSmallest cut: all of it.\n"}), nil},
		{"Decisions with only Dropped rows", preplanTopicWithBodies(map[string]string{"Decisions": hdr + "| 1 | Dropped: A | no |\n| 2 | Dropped: B | no |\n"}), nil},
		{"Decisions with the separator row only", preplanTopicWithBodies(map[string]string{"Decisions": "|---|---|---|\n"}), preplanHeadings("Decisions")},
		{"Decisions with a header and a row but no separator row", preplanTopicWithBodies(map[string]string{"Decisions": "| # | Decision | Reason |\n" + row}), nil},
		{"Decisions with an indented row", preplanTopicWithBodies(map[string]string{"Decisions": hdr + " " + row}), nil},
		{"Decisions with an indented header, separator and row", preplanTopicWithBodies(map[string]string{"Decisions": "  | # | Decision | Reason |\n  |---|---|---|\n\t" + row}), nil},
		{"Decisions with an indented header and separator only", preplanTopicWithBodies(map[string]string{"Decisions": "  | # | Decision | Reason |\n  | :--- | --- | ---: |\n"}), preplanHeadings("Decisions")},
		{"Decisions with a short separator counts it as a row", preplanTopicWithBodies(map[string]string{"Decisions": "| # | Decision | Reason |\n|-|-|-|\n"}), nil},
		{"## Goal at byte 0 with body text", "## Goal\ntext\n", except("Goal")},
		{"## Goal at byte 0 with no body", "## Goal\n", all},
		{"only ### lines under Flows", preplanTopicWithBodies(map[string]string{"Flows": "### Sub one\n\n### Sub two\n"}), nil},
		{"a fence under Flows that holds ## Goal", preplanTopicWithBodies(map[string]string{"Flows": fenced}), nil},
		{"a fenced ## Goal is not a heading", "## Flows\n\n" + fenced, except("Flows")},
		{"a ~~~ fence under Flows that holds ## Risk if wrong", preplanTopicWithBodies(map[string]string{"Flows": "~~~\n## Risk if wrong\n~~~\n"}), nil},
		{"an unclosed fence under Flows swallows the next heading", preplanTopicWithBodies(map[string]string{"Flows": "```\ncode\n"}), preplanHeadings("Decisions")},
		{"a ``` line does not close a ```` fence", preplanTopicWithBodies(map[string]string{"Flows": "````\n```\ncode\n"}), preplanHeadings("Decisions")},
		{"a ````` line closes a ```` fence", preplanTopicWithBodies(map[string]string{"Flows": "````\ncode\n`````\n"}), nil},
		{"the first heading of a name counts: text first", "## Goal\ntext\n## Goal\n\n", except("Goal")},
		{"the first heading of a name counts: blank first", "## Goal\n\n## Goal\ntext\n", all},
		{"## Open questions is not in the list", preplanTopicWithBodies(nil) + "## Open questions\n\n", nil},
		{"a heading with trailing spaces", strings.Replace(preplanTopicWithBodies(nil), "## Flows\n", "## Flows  \n", 1), nil},
		{"a longer heading is another name", strings.Replace(preplanTopicWithBodies(nil), "## Flows\n", "## Flows and more\n", 1), preplanHeadings("Flows")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.want
			if want == nil {
				want = []string{}
			}
			lf := tc.in
			crlf := strings.ReplaceAll(lf, "\n", "\r\n")
			for _, form := range []struct{ name, text string }{
				{"LF", lf},
				{"LF without the last line end", strings.TrimSuffix(lf, "\n")},
				{"CRLF", crlf},
				{"CRLF without the last line end", strings.TrimSuffix(crlf, "\r\n")},
			} {
				data := []byte(form.text)
				got, _ := preplanEmptySections(data)
				if got == nil {
					t.Errorf("%s: got a nil slice, want a non-nil slice", form.name)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: got %q, want %q", form.name, got, want)
				}
				if string(data) != form.text {
					t.Errorf("%s: preplanEmptySections changed its input", form.name)
				}
			}
		})
	}
}

// TestPreplanWarningText verifies the guardrail warning becomes one line with
// one final period removed. The real loadGuardrails warning holds no line
// break and no final period, so this table reaches those two branches.
func TestPreplanWarningText(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain text", "Failed to read plan config: bad", "Failed to read plan config: bad"},
		{"line break", "Failed to read plan config: bad\nline two", "Failed to read plan config: bad line two"},
		{"CR LF and tab", "a\r\nb\tc", "a b c"},
		{"final period", "Failed to read plan config: bad.", "Failed to read plan config: bad"},
		{"line break and final period", "Failed to read plan config: bad\nline two.\n", "Failed to read plan config: bad line two"},
		{"only one final period", "bad..", "bad."},
		{"outer white space", "  bad  ", "bad"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := preplanWarningText(tc.in); got != tc.want {
				t.Errorf("preplanWarningText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestPlanSupportPreplanCheckErrors verifies each refused preplan_check call
// returns its error class and Suggestion and leaves the topic file as it was.
func TestPlanSupportPreplanCheckErrors(t *testing.T) {
	setPreplanNow(t, preplanCheckTestNow)
	skeleton := preplanSkeletonFor("auth flow")
	const countSuggestion = "Pass passCount, keptCount and droppedCount as whole numbers from 0 to 10000, counted from the ## Decisions table."

	// wantErr checks the class, the message and the Suggestion of err.
	wantErr := func(t *testing.T, err error, class, msg, suggestion string) {
		t.Helper()
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if got := errorClassOf(err); got != class {
			t.Errorf("error class = %q, want %s (err %v)", got, class, err)
		}
		if msg != "" && err.Error() != msg {
			t.Errorf("err.Error() = %q, want %q", err.Error(), msg)
		}
		if got := suggestionOf(err); got != suggestion {
			t.Errorf("Suggestion = %q, want %q", got, suggestion)
		}
	}

	one := 1
	minus := -1
	over := preplanCountMax + 1
	for _, tc := range []struct {
		name    string
		in      PlanSupportIn
		wantMsg string
	}{
		{"passCount absent", PlanSupportIn{KeptCount: &one, DroppedCount: &one}, "preplan_check: passCount is required"},
		{"keptCount absent", PlanSupportIn{PassCount: &one, DroppedCount: &one}, "preplan_check: keptCount is required"},
		{"droppedCount absent", PlanSupportIn{PassCount: &one, KeptCount: &one}, "preplan_check: droppedCount is required"},
		{"passCount -1", PlanSupportIn{PassCount: &minus, KeptCount: &one, DroppedCount: &one}, "preplan_check: passCount is -1, must be 0 to 10000"},
		{"keptCount -1", PlanSupportIn{PassCount: &one, KeptCount: &minus, DroppedCount: &one}, "preplan_check: keptCount is -1, must be 0 to 10000"},
		{"droppedCount -1", PlanSupportIn{PassCount: &one, KeptCount: &one, DroppedCount: &minus}, "preplan_check: droppedCount is -1, must be 0 to 10000"},
		{"passCount 10001", PlanSupportIn{PassCount: &over, KeptCount: &one, DroppedCount: &one}, "preplan_check: passCount is 10001, must be 0 to 10000"},
		{"keptCount 10001", PlanSupportIn{PassCount: &one, KeptCount: &over, DroppedCount: &one}, "preplan_check: keptCount is 10001, must be 0 to 10000"},
		{"droppedCount 10001", PlanSupportIn{PassCount: &one, KeptCount: &one, DroppedCount: &over}, "preplan_check: droppedCount is 10001, must be 0 to 10000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			noPreplanCheckWrite(t)
			root := t.TempDir()
			file := writePreplanTopic(t, root, "auth-flow.md", skeleton)
			tc.in.Action, tc.in.Topic = "preplan_check", "auth flow"
			_, err := planSupportCore(root, root, tc.in)
			wantErr(t, err, "domain", tc.wantMsg, countSuggestion)
			wantPreplanBytes(t, file, skeleton)
		})
	}

	for _, tc := range []struct {
		name, topic, wantMsg, wantSuggestion string
	}{
		{"empty topic", "", `preplan_check: topic "" has no ASCII letter or digit`, preplanTopicASCIISuggestion},
		{"51 characters", strings.Repeat("a", 51), "preplan_check: topic has 51 characters, max 50", preplanTopicLongSuggestion},
		{"line break", "auth\nflow", "preplan_check: topic has a line break", preplanTopicLineSuggestion},
	} {
		t.Run("bad topic: "+tc.name, func(t *testing.T) {
			noPreplanCheckWrite(t)
			root := t.TempDir()
			_, err := planSupportCore(root, root, preplanCheckIn(tc.topic, 1, 0, 0))
			wantErr(t, err, "domain", tc.wantMsg, tc.wantSuggestion)
		})
	}

	t.Run("no topic file", func(t *testing.T) {
		noPreplanCheckWrite(t)
		root := t.TempDir()
		other := writePreplanTopic(t, root, "other.md", skeleton)
		dir := filepath.Dir(other)
		_, err := planSupportCore(root, root, preplanCheckIn("  auth flow  ", 1, 0, 0))
		wantErr(t, err, "domain", `preplan_check: no topic file for topic "auth flow" in `+dir,
			"Run plan_support with action preplan_context and this topic first.")
		if names := preplanDirNames(t, dir); !reflect.DeepEqual(names, []string{"other.md"}) {
			t.Errorf("preplan dir entries = %v, want only other.md", names)
		}
	})

	t.Run("no preplan folder", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, ".sdlc-v2", "preplan")
		_, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
		wantErr(t, err, "domain", `preplan_check: no topic file for topic "auth flow" in `+dir,
			"Run plan_support with action preplan_context and this topic first.")
		if _, err := os.Stat(filepath.Join(root, ".sdlc-v2")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("a refused call created the data dir (stat err %v)", err)
		}
	})

	t.Run("two topic files for one slug", func(t *testing.T) {
		noPreplanCheckWrite(t)
		root := t.TempDir()
		a := writePreplanTopic(t, root, "auth-flow.md", skeleton)
		b := writePreplanTopic(t, root, "2026-10-01-auth-flow.md", skeleton)
		dir := filepath.Dir(a)
		_, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
		wantErr(t, err, "data",
			`preplan_check: topic "auth flow" matches 2 topic files in `+dir+": 2026-10-01-auth-flow.md, auth-flow.md",
			"Rename or delete one of: 2026-10-01-auth-flow.md, auth-flow.md. Then run /sdlc:preplan again.")
		wantPreplanBytes(t, a, skeleton)
		wantPreplanBytes(t, b, skeleton)
	})

	t.Run("a regular file in place of the data folder", func(t *testing.T) {
		noPreplanCheckWrite(t)
		root := t.TempDir()
		blocker := filepath.Join(root, ".sdlc-v2")
		writeFile(t, blocker, "not a directory\n")
		dir := filepath.Join(blocker, "preplan")
		_, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
		wantErr(t, err, "infra", "",
			"Make "+dir+" a folder that you can read: remove or rename the file or link at that path, or give your user read permission. Then run /sdlc:preplan again.")
		if msg := err.Error(); !strings.HasPrefix(msg, "preplan_check: list: ") {
			t.Errorf("err.Error() = %q, want prefix %q", msg, "preplan_check: list: ")
		}
		var pathErr *fs.PathError
		if !errors.As(err, &pathErr) {
			t.Errorf("err = %v, want it to wrap the list error (*fs.PathError)", err)
		}
		wantPreplanBytes(t, blocker, "not a directory\n")
	})

	t.Run("a preplan folder with more than preplanDirMax names", func(t *testing.T) {
		noPreplanCheckWrite(t)
		swapPreplanSeam(t, &preplanDirMax, 2)
		root := t.TempDir()
		file := writePreplanTopic(t, root, "auth-flow.md", skeleton)
		writePreplanTopic(t, root, "b.md", "# Preplan: b\n")
		writePreplanTopic(t, root, "c.md", "# Preplan: c\n")
		dir := filepath.Dir(file)
		_, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
		wantErr(t, err, "infra", "preplan_check: list "+dir+": the folder holds more than 2 names",
			"Move old topic files out of "+dir+" until it holds at most 2 names. Then run /sdlc:preplan again.")
		if !errors.Is(err, errPreplanDirFull) {
			t.Errorf("err = %v, want it to wrap errPreplanDirFull", err)
		}
		wantPreplanBytes(t, file, skeleton)
	})

	t.Run("no check line, no Started line and no Status line in the head", func(t *testing.T) {
		noPreplanCheckWrite(t)
		root := t.TempDir()
		const content = "# Preplan: auth flow\n\n## Goal\n"
		file := writePreplanTopic(t, root, "auth-flow.md", content)
		_, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
		wantErr(t, err, "data", "preplan_check: "+file+" has no **Started:** line and no **Status:** line in its first 4096 bytes",
			"Add a **Status:** line near the top of the topic file by hand, then call preplan_check again.")
		wantPreplanBytes(t, file, content)
	})

	t.Run("a topic file of 8 MiB + 1 byte", func(t *testing.T) {
		noPreplanCheckWrite(t)
		swapPreplanSeam(t, &preplanReadFile, func(path string) ([]byte, error) {
			t.Errorf("preplanReadFile(%s) was called, want no read of a big file", path)
			return nil, errInjectedPreplan
		})
		root := t.TempDir()
		head := "**Status:** in progress\n"
		content := head + preplanFiller(dashboardDeleteReadMax+1-len(head))
		file := writePreplanTopic(t, root, "auth-flow.md", content)
		_, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
		wantErr(t, err, "data",
			fmt.Sprintf("preplan_check: %s is %d bytes, more than the %d bytes (8 MiB) that preplan_check reads", file, dashboardDeleteReadMax+1, dashboardDeleteReadMax),
			"Make the topic file smaller than 8 MiB by hand, then call preplan_check again.")
		if info, err := os.Stat(file); err != nil || info.Size() != int64(len(content)) {
			t.Errorf("topic file size changed (stat %v, err %v)", info, err)
		}
	})

	const rerun = ", then run /sdlc:preplan again. preplan_context then creates a new topic file."
	// Each setup makes the topic path at file and returns the message and the
	// Suggestion that the call must give.
	notRegular := []struct {
		name  string
		setup func(t *testing.T, file string) (msg, suggestion string)
	}{
		{"a symlink to a regular file", func(t *testing.T, file string) (string, string) {
			target := filepath.Join(t.TempDir(), "target.md")
			writeFile(t, target, skeleton)
			if err := os.Symlink(target, file); err != nil {
				t.Fatal(err)
			}
			return "preplan_check: " + file + " is a symlink, not a regular file",
				"Replace the link " + file + " with a copy of the file it points to, then call preplan_check again."
		}},
		{"a dangling symlink", func(t *testing.T, file string) (string, string) {
			target := filepath.Join(t.TempDir(), "gone.md")
			if err := os.Symlink(target, file); err != nil {
				t.Fatal(err)
			}
			return "preplan_check: " + file + " is a link to " + target + ", which does not exist.",
				"Remove the link with rm " + file + rerun
		}},
		{"a folder", func(t *testing.T, file string) (string, string) {
			if err := os.Mkdir(file, 0o755); err != nil {
				t.Fatal(err)
			}
			return "preplan_check: " + file + " is a folder, not a regular file", "Remove or rename " + file + rerun
		}},
		{"a named pipe", func(t *testing.T, file string) (string, string) {
			// The Lstat seam reports a pipe, so the case needs no Mkfifo and
			// runs on every platform. The file on disk stays regular.
			writeFile(t, file, skeleton)
			swapPreplanSeam(t, &preplanLstat, func(string) (fs.FileInfo, error) {
				return preplanFakeInfo{mode: fs.ModeNamedPipe | 0o644}, nil
			})
			return "preplan_check: " + file + " is not a regular file", "Remove or rename " + file + rerun
		}},
	}
	for _, tc := range notRegular {
		t.Run(tc.name+" at the topic file", func(t *testing.T) {
			noPreplanCheckWrite(t)
			root := t.TempDir()
			dir := filepath.Join(root, ".sdlc-v2", "preplan")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "auth-flow.md")
			wantMsg, wantSuggestion := tc.setup(t, file)
			before, _ := os.Lstat(file)

			_, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
			wantErr(t, err, "domain", wantMsg, wantSuggestion)
			after, _ := os.Lstat(file)
			if before == nil || after == nil || before.Mode() != after.Mode() {
				t.Errorf("topic path mode = %v, want %v unchanged", after, before)
			}
		})
	}

	t.Run("symlink target stays unchanged", func(t *testing.T) {
		noPreplanCheckWrite(t)
		root := t.TempDir()
		target := filepath.Join(t.TempDir(), "target.md")
		writeFile(t, target, skeleton)
		dir := filepath.Join(root, ".sdlc-v2", "preplan")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "auth-flow.md")); err != nil {
			t.Fatal(err)
		}
		if _, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0)); err == nil {
			t.Fatal("expected an error, got nil")
		}
		wantPreplanBytes(t, target, skeleton)
	})

	// An Lstat or a read failure gets a Suggestion that fits its error.
	errPermission := fmt.Errorf("%w: injected", fs.ErrPermission)
	errNotExist := fmt.Errorf("%w: injected", fs.ErrNotExist)
	for _, op := range []string{"Lstat", "read"} {
		for _, tc := range []struct {
			name           string
			err            error
			class          string
			wantSuggestion func(file string) string
		}{
			{"an opaque error", errInjectedPreplan, "infra", func(string) string {
				return "Fix the cause that the message names, then call preplan_check again."
			}},
			{"a permission error", errPermission, "infra", func(file string) string {
				if op == "read" {
					return "Give your user read permission on " + file + ", then call preplan_check again."
				}
				return "Give your user search permission on " + filepath.Dir(file) + ", then call preplan_check again."
			}},
			{"a file that is gone", errNotExist, "domain", func(string) string {
				return "Run plan_support with action preplan_context and this topic first."
			}},
		} {
			t.Run(op+" failure: "+tc.name, func(t *testing.T) {
				noPreplanCheckWrite(t)
				if op == "read" {
					swapPreplanSeam(t, &preplanReadFile, func(string) ([]byte, error) { return nil, tc.err })
				} else {
					swapPreplanSeam(t, &preplanLstat, func(string) (fs.FileInfo, error) { return nil, tc.err })
				}
				root := t.TempDir()
				file := writePreplanTopic(t, root, "auth-flow.md", skeleton)
				_, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
				wantErr(t, err, tc.class, "preplan_check: "+tc.err.Error(), tc.wantSuggestion(file))
				if !errors.Is(err, tc.err) {
					t.Errorf("err = %v, want it to wrap the %s error", err, op)
				}
				wantPreplanBytes(t, file, skeleton)
			})
		}
	}

	t.Run("a write failure", func(t *testing.T) {
		swapPreplanSeam(t, &preplanWriteBytes, func(string, []byte) error { return errInjectedPreplan })
		root := t.TempDir()
		file := writePreplanTopic(t, root, "auth-flow.md", skeleton)
		out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
		wantErr(t, err, "infra", "preplan_check: "+errInjectedPreplan.Error(),
			"Check write permission on "+filepath.Dir(file)+" and free disk space, then call preplan_check again.")
		if !errors.Is(err, errInjectedPreplan) {
			t.Errorf("err = %v, want it to wrap the write error", err)
		}
		if out.GuardrailCheckLine != "" || out.PreplanFile != "" {
			t.Errorf("output = %+v, want empty on a failed write", out)
		}
		wantPreplanBytes(t, file, skeleton)
	})
}

// preplanErrWant is the class, the full message and the Suggestion that
// wantPreplanErr checks.
type preplanErrWant struct{ class, msg, suggestion string }

// wantPreplanErr checks the class, the message and the Suggestion of err.
func wantPreplanErr(t *testing.T, err error, want preplanErrWant) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if got := errorClassOf(err); got != want.class {
		t.Errorf("error class = %q, want %s (err %v)", got, want.class, err)
	}
	if err.Error() != want.msg {
		t.Errorf("err.Error() = %q, want %q", err.Error(), want.msg)
	}
	if got := suggestionOf(err); got != want.suggestion {
		t.Errorf("Suggestion = %q, want %q", got, want.suggestion)
	}
}

// preplanActionText is the retry text and the no-file Suggestion that
// readPreplanTopic gives for one action.
type preplanActionText struct{ action, again, gone string }

// preplanActionTexts are the texts of each preplan action that reads a topic
// file.
var preplanActionTexts = []preplanActionText{
	{"preplan_check", ", then call preplan_check again.", "Run plan_support with action preplan_context and this topic first."},
	{"preplan_write", ", then call preplan_write again.", "Run plan_support with action preplan_context and this topic first."},
	{"preplan_context", ", then run /sdlc:preplan again.", "Run /sdlc:preplan again: the topic file went away after the folder listing, and the next run creates a new one."},
}

// preplanTopicContent is the topic file of the readPreplanTopic tests.
const preplanTopicContent = "**Status:** in progress\n"

// preplanTopicRerun ends the Suggestion of a path that is not a regular file.
const preplanTopicRerun = ", then run /sdlc:preplan again. preplan_context then creates a new topic file."

// TestReadPreplanTopicActionText checks that the retry text and the no-file
// Suggestion of readPreplanTopic follow the calling action: preplan_check and
// preplan_write keep their own text, and preplan_context tells the user to
// run /sdlc:preplan again. Each state checks the class, the message and the
// Suggestion for each action.
func TestReadPreplanTopicActionText(t *testing.T) {
	// The two big files hold the bytes of the size boundary: the limit, and
	// the limit + 1 byte. Every action reads the same files.
	bigDir := t.TempDir()
	atLimit := filepath.Join(bigDir, "at-limit.md")
	writeFile(t, atLimit, preplanTopicContent+preplanFiller(dashboardDeleteReadMax-len(preplanTopicContent)))
	overLimit := filepath.Join(bigDir, "over-limit.md")
	writeFile(t, overLimit, preplanTopicContent+preplanFiller(dashboardDeleteReadMax+1-len(preplanTopicContent)))

	for _, a := range preplanActionTexts {
		t.Run(a.action+": a file of dashboardDeleteReadMax bytes", func(t *testing.T) {
			data, err := readPreplanTopic(a.action, atLimit)
			if err != nil {
				t.Fatalf("readPreplanTopic: %v, want no error at the limit", err)
			}
			if len(data) != dashboardDeleteReadMax {
				t.Errorf("read %d bytes, want %d", len(data), dashboardDeleteReadMax)
			}
		})
		t.Run(a.action+": a file of dashboardDeleteReadMax + 1 bytes", func(t *testing.T) {
			swapPreplanSeam(t, &preplanReadFile, func(path string) ([]byte, error) {
				t.Errorf("preplanReadFile(%s) was called, want no read of a big file", path)
				return nil, errInjectedPreplan
			})
			_, err := readPreplanTopic(a.action, overLimit)
			wantPreplanErr(t, err, preplanErrWant{"data",
				fmt.Sprintf("%s: %s is %d bytes, more than the %d bytes (8 MiB) that %s reads",
					a.action, overLimit, dashboardDeleteReadMax+1, dashboardDeleteReadMax, a.action),
				"Make the topic file smaller than 8 MiB by hand" + a.again})
		})
		checkReadPreplanIOErrors(t, a)
		checkReadPreplanNotRegular(t, a)
	}
}

// checkReadPreplanIOErrors runs readPreplanTopic for action a with each kind
// of Lstat and read failure.
func checkReadPreplanIOErrors(t *testing.T, a preplanActionText) {
	errPermission := fmt.Errorf("%w: injected", fs.ErrPermission)
	errNotExist := fmt.Errorf("%w: injected", fs.ErrNotExist)
	for _, op := range []string{"Lstat", "read"} {
		for _, tc := range []struct {
			name           string
			err            error
			class          string
			wantSuggestion func(file string) string
		}{
			{"an opaque error", errInjectedPreplan, "infra", func(string) string {
				return "Fix the cause that the message names" + a.again
			}},
			{"a permission error", errPermission, "infra", func(file string) string {
				if op == "read" {
					return "Give your user read permission on " + file + a.again
				}
				return "Give your user search permission on " + filepath.Dir(file) + a.again
			}},
			{"a file that is gone", errNotExist, "domain", func(string) string { return a.gone }},
		} {
			t.Run(a.action+": "+op+" failure: "+tc.name, func(t *testing.T) {
				file := filepath.Join(t.TempDir(), "auth-flow.md")
				writeFile(t, file, preplanTopicContent)
				if op == "read" {
					swapPreplanSeam(t, &preplanReadFile, func(string) ([]byte, error) { return nil, tc.err })
				} else {
					swapPreplanSeam(t, &preplanLstat, func(string) (fs.FileInfo, error) { return nil, tc.err })
				}
				_, err := readPreplanTopic(a.action, file)
				wantPreplanErr(t, err, preplanErrWant{tc.class, a.action + ": " + tc.err.Error(), tc.wantSuggestion(file)})
				if !errors.Is(err, tc.err) {
					t.Errorf("err = %v, want it to wrap the %s error", err, op)
				}
			})
		}
	}
}

// checkReadPreplanNotRegular runs readPreplanTopic for action a on a live
// link, a dangling link and a folder.
func checkReadPreplanNotRegular(t *testing.T, a preplanActionText) {
	t.Run(a.action+": a symlink to a regular file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "auth-flow.md")
		target := filepath.Join(t.TempDir(), "target.md")
		writeFile(t, target, preplanTopicContent)
		if err := os.Symlink(target, file); err != nil {
			t.Fatal(err)
		}
		_, err := readPreplanTopic(a.action, file)
		wantPreplanErr(t, err, preplanErrWant{"domain", a.action + ": " + file + " is a symlink, not a regular file",
			"Replace the link " + file + " with a copy of the file it points to" + a.again})
	})

	// The other not-regular states do not depend on the action: their
	// Suggestion already names /sdlc:preplan.
	t.Run(a.action+": a dangling symlink", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "auth-flow.md")
		target := filepath.Join(t.TempDir(), "gone.md")
		if err := os.Symlink(target, file); err != nil {
			t.Fatal(err)
		}
		_, err := readPreplanTopic(a.action, file)
		wantPreplanErr(t, err, preplanErrWant{"domain", a.action + ": " + file + " is a link to " + target + ", which does not exist.",
			"Remove the link with rm " + file + preplanTopicRerun})
	})

	t.Run(a.action+": a folder", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "auth-flow.md")
		if err := os.Mkdir(file, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := readPreplanTopic(a.action, file)
		wantPreplanErr(t, err, preplanErrWant{"domain", a.action + ": " + file + " is a folder, not a regular file",
			"Remove or rename " + file + preplanTopicRerun})
	})
}

// ---------------------------------------------------------------------------
// preplan_context heading repair
// ---------------------------------------------------------------------------

// preplanBlocks joins the empty section blocks of names with eol line ends.
func preplanBlocks(eol string, names ...string) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString(preplanSectionBlock(n, eol))
	}
	return b.String()
}

// preplanHeadings returns "## <name>" for each name.
func preplanHeadings(names ...string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, preplanHeadingPrefix+n)
	}
	return out
}

// TestPreplanSectionSpans pins the offsets of each span, the fence rule and
// the open fence at the end of the text.
func TestPreplanSectionSpans(t *testing.T) {
	const data = "# T\n## A\n\nx\n### sub\n```\n## B\n```\n## C  \r\n~~~\n## D\n"
	spans, open := preplanSectionSpans([]byte(data))
	a, c := strings.Index(data, "## A"), strings.Index(data, "## C")
	tilde := strings.Index(data, "~~~")
	want := []preplanSpan{
		{name: "A", lineStart: a, bodyStart: a + len("## A\n"), bodyEnd: c},
		{name: "C", lineStart: c, bodyStart: tilde, bodyEnd: len(data)},
	}
	if !reflect.DeepEqual(spans, want) {
		t.Errorf("spans = %+v, want %+v", spans, want)
	}
	if open != "~~~" {
		t.Errorf("openFence = %q, want ~~~", open)
	}

	// A heading on the last line with no line end has its body start at the end.
	spans, open = preplanSectionSpans([]byte("## Goal"))
	if want := []preplanSpan{{name: "Goal", lineStart: 0, bodyStart: 7, bodyEnd: 7}}; !reflect.DeepEqual(spans, want) || open != "" {
		t.Errorf("spans = %+v, open %q, want %+v and no open fence", spans, open, want)
	}
	if spans, open := preplanSectionSpans(nil); spans != nil || open != "" {
		t.Errorf("empty data gives spans %+v, open %q, want none", spans, open)
	}
}

// TestAddPreplanHeadings covers each rule of addPreplanHeadings and each input
// of the repair test table of the plan: the place of each absent name, the
// head guard, fences, line ends, spacing, and the cases with no change.
func TestAddPreplanHeadings(t *testing.T) {
	const (
		goal, problem, users, risk = "Goal", "Problem and benefit", "Users and effect", "Risk if wrong"
		nonGoals, flows, decisions = "Non-goals", "Flows", "Decisions"
		hdr                        = "| # | Decision | Reason |\n|---|---|---|\n"
	)
	lf := func(names ...string) string { return preplanBlocks("\n", names...) }
	complete := "**Status:** s\n\n" + lf(preplanSections...)
	prefix := "## Users and effect\n\n"
	statusAt4096 := prefix + preplanFiller(dashboardPreplanHeadMax-len(prefix)) + "**Status:** s\n"
	const status = "**Status:** s"
	statusEndsAt4096 := prefix + preplanFiller(dashboardPreplanHeadMax-len(prefix)-len(status)) + status + "\n"
	if n := strings.Index(statusAt4096, "**Status:**"); n != dashboardPreplanHeadMax {
		t.Fatalf("the late Status line starts at byte %d, want %d", n, dashboardPreplanHeadMax)
	}
	if n := strings.Index(statusEndsAt4096, status) + len(status); n != dashboardPreplanHeadMax {
		t.Fatalf("the Status line ends at byte %d, want %d", n, dashboardPreplanHeadMax)
	}
	// statusStraddles4096 has a Status line whose "**Status:** " is the last
	// text of the head window and whose value is past it. The blocks go after
	// its line end in the full data, not at the window edge.
	statusStraddles4096 := prefix + preplanFiller(dashboardPreplanHeadMax-len(prefix)-len("**Status:** ")) + status + "\n"
	if n := strings.Index(statusStraddles4096, status); n >= dashboardPreplanHeadMax || n+len(status) <= dashboardPreplanHeadMax {
		t.Fatalf("the Status line spans bytes %d to %d, want it across byte %d", n, n+len(status), dashboardPreplanHeadMax)
	}
	// closedFences names the fence run of the closing line that each case
	// adds. A case that is not listed adds none.
	closedFences := map[string]string{
		"an unclosed fence at the end":                      "```",
		"an unclosed fence with no line end":                "~~~",
		"an unclosed four-backtick fence with a ``` line":   "````",
		"an unclosed fence in a CRLF file":                  "```",
		"an unclosed fence in a CRLF file with no line end": "~~~",
	}

	for _, tc := range []struct {
		name  string
		in    string
		want  string   // "" with no added names: no change
		added []string // names, not headings
	}{
		{"no heading at all", "My own notes.",
			"My own notes.\n\n" + lf(preplanSections...), preplanSections},
		{"an empty file", "", lf(preplanSections...), preplanSections},
		{"a heading at byte 0 and no head line", "## Users and effect\n\nbody\n",
			lf(goal, problem) + "## Users and effect\n\nbody\n\n" + lf(risk, nonGoals, flows, decisions),
			[]string{goal, problem, risk, nonGoals, flows, decisions}},
		{"the last heading on the last line with no line end", "**Status:** s\n\n## Goal",
			"**Status:** s\n\n## Goal\n\n" + lf(preplanSections[1:]...), preplanSections[1:]},
		{"a CRLF file", "**Status:** s\r\n\r\n## Goal\r\n\r\ntext\r\n",
			"**Status:** s\r\n\r\n## Goal\r\n\r\ntext\r\n\r\n" + preplanBlocks("\r\n", preplanSections[1:]...), preplanSections[1:]},
		{"## Goal in a backtick fence under Flows", "## Flows\n\n```\n## Goal\n```\n",
			lf(goal, problem, users, risk, nonGoals) + "## Flows\n\n```\n## Goal\n```\n\n" + lf(decisions),
			[]string{goal, problem, users, risk, nonGoals, decisions}},
		{"## Goal in a ~~~ fence under Flows", "## Flows\n\n~~~\n## Goal\n~~~\n",
			lf(goal, problem, users, risk, nonGoals) + "## Flows\n\n~~~\n## Goal\n~~~\n\n" + lf(decisions),
			[]string{goal, problem, users, risk, nonGoals, decisions}},
		{"a ``` line does not close a ~~~ fence", "## Flows\n\n~~~\n```\n## Goal\n~~~\n",
			lf(goal, problem, users, risk, nonGoals) + "## Flows\n\n~~~\n```\n## Goal\n~~~\n\n" + lf(decisions),
			[]string{goal, problem, users, risk, nonGoals, decisions}},
		{"a real ## Goal and a fenced one", "## Goal\n\n## Flows\n\n```\n## Goal\n```\n",
			"## Goal\n\n" + lf(problem, users, risk, nonGoals) + "## Flows\n\n```\n## Goal\n```\n\n" + lf(decisions),
			[]string{problem, users, risk, nonGoals, decisions}},
		{"### lines under Flows", strings.Replace(complete, "## Flows\n\n", "## Flows\n\n### Sub one\n\n### Sub two\n\n", 1), "", nil},
		{"headings out of list order", "## Flows\n\n## Goal\n\n",
			"## Flows\n\n" + lf(decisions) + "## Goal\n\n" + lf(problem, users, risk, nonGoals),
			[]string{problem, users, risk, nonGoals, decisions}},
		{"## Open questions after Decisions", "**Status:** s\n\n## Goal\n\n## Decisions\n\n" + hdr + "\n## Open questions\n\n- q?\n",
			"**Status:** s\n\n## Goal\n\n" + lf(problem, users, risk, nonGoals, flows) + "## Decisions\n\n" + hdr + "\n## Open questions\n\n- q?\n",
			[]string{problem, users, risk, nonGoals, flows}},
		{"a ## line above the status line", "## Users and effect\n\n**Status:** s\n",
			"## Users and effect\n\n**Status:** s\n\n" + lf(risk, nonGoals, flows, decisions) + lf(goal, problem),
			[]string{goal, problem, risk, nonGoals, flows, decisions}},
		{"the head guard reads the Started and Guardrail check lines", "## Users and effect\n**Started:** t\n**Guardrail check:** x\n",
			"## Users and effect\n**Started:** t\n**Guardrail check:** x\n\n" + lf(risk, nonGoals, flows, decisions) + lf(goal, problem),
			[]string{goal, problem, risk, nonGoals, flows, decisions}},
		{"two ## Goal lines", "## Goal\n\nA\n\n## Goal\n\nB\n",
			"## Goal\n\nA\n\n" + lf(preplanSections[1:]...) + "## Goal\n\nB\n", preplanSections[1:]},
		{"## Flows with trailing spaces", strings.Replace(complete, "## Flows\n", "## Flows   \n", 1), "", nil},
		{"## Flows with a tab", strings.Replace(complete, "## Flows\n", "## Flows\t\n", 1), "", nil},
		{"## Flows with more text is another heading", strings.Replace(complete, "## Flows\n", "## Flows and more\n", 1),
			strings.Replace(complete, "## Flows\n\n", "## Flows\n\n## Flows and more\n\n", 1), []string{flows}},
		{"an unclosed fence at the end", "## Goal\n\n```\ncode\n",
			"## Goal\n\n```\ncode\n```\n\n" + lf(preplanSections[1:]...), preplanSections[1:]},
		{"an unclosed fence with no line end", "## Goal\n\n~~~\ncode",
			"## Goal\n\n~~~\ncode\n~~~\n\n" + lf(preplanSections[1:]...), preplanSections[1:]},
		{"an unclosed four-backtick fence with a ``` line", "## Goal\n\n````\n```\n## Flows\n",
			"## Goal\n\n````\n```\n## Flows\n````\n\n" + lf(preplanSections[1:]...), preplanSections[1:]},
		{"a ``` line with an info string does not close a fence", "## Goal\n\n```\n```go\n```\n",
			"## Goal\n\n```\n```go\n```\n\n" + lf(preplanSections[1:]...), preplanSections[1:]},
		{"an unclosed fence in a CRLF file", "## Goal\r\n\r\n```\r\ncode\r\n",
			"## Goal\r\n\r\n```\r\ncode\r\n```\r\n\r\n" + preplanBlocks("\r\n", preplanSections[1:]...), preplanSections[1:]},
		{"an unclosed fence in a CRLF file with no line end", "## Goal\r\n\r\n~~~\r\ncode",
			"## Goal\r\n\r\n~~~\r\ncode\r\n~~~\r\n\r\n" + preplanBlocks("\r\n", preplanSections[1:]...), preplanSections[1:]},
		{"a CRLF file with no final line end", "**Status:** s\r\n\r\n## Goal\r\n\r\ntext",
			"**Status:** s\r\n\r\n## Goal\r\n\r\ntext\r\n\r\n" + preplanBlocks("\r\n", preplanSections[1:]...), preplanSections[1:]},
		{"a Status line that starts in the head window and ends past it", statusStraddles4096,
			statusStraddles4096 + "\n" + lf(risk, nonGoals, flows, decisions) + lf(goal, problem),
			[]string{goal, problem, risk, nonGoals, flows, decisions}},
		{"a Status line that starts at byte dashboardPreplanHeadMax", statusAt4096,
			lf(goal, problem) + statusAt4096 + "\n" + lf(risk, nonGoals, flows, decisions),
			[]string{goal, problem, risk, nonGoals, flows, decisions}},
		{"a Status line that ends at byte dashboardPreplanHeadMax", statusEndsAt4096,
			statusEndsAt4096 + "\n" + lf(risk, nonGoals, flows, decisions) + lf(goal, problem),
			[]string{goal, problem, risk, nonGoals, flows, decisions}},
		{"an old-form file with a ## Guardrail check section",
			"**Status:** s\n\n## Goal\n\ng\n\n## Guardrail check\n\n| a |\n\n## Users and effect\n\n## Flows\n\n## Decisions\n\n" + hdr,
			"**Status:** s\n\n## Goal\n\ng\n\n" + lf(problem) + "## Guardrail check\n\n| a |\n\n## Users and effect\n\n" + lf(risk, nonGoals) + "## Flows\n\n## Decisions\n\n" + hdr,
			[]string{problem, risk, nonGoals}},
		{"a complete file", complete, "", nil},
		{"a complete CRLF file", strings.ReplaceAll(complete, "\n", "\r\n"), "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := []byte(tc.in)
			orig := append([]byte(nil), in...)
			next, added, closed := addPreplanHeadings(in)
			if !bytes.Equal(in, orig) {
				t.Errorf("addPreplanHeadings changed its input to %q", in)
			}
			if want := closedFences[tc.name]; closed != want {
				t.Errorf("closed = %q, want %q", closed, want)
			}
			if tc.added == nil {
				if added != nil || string(next) != tc.in {
					t.Errorf("addPreplanHeadings = (%q, %q), want the input and nil", next, added)
				}
				if len(in) > 0 && &next[0] != &in[0] {
					t.Error("addPreplanHeadings returned a copy, want data itself when no name is absent")
				}
				return
			}
			if string(next) != tc.want {
				t.Errorf("next =\n%q\nwant\n%q", next, tc.want)
			}
			if want := preplanHeadings(tc.added...); !reflect.DeepEqual(added, want) {
				t.Errorf("added = %q, want %q", added, want)
			}
			// The repaired text has every heading, so a second call adds none.
			if again, more, _ := addPreplanHeadings(next); more != nil || !bytes.Equal(again, next) {
				t.Errorf("a second call added %q, want none", more)
			}
		})
	}
}

// TestPlanPreplanContextRepair runs the heading repair through preplan_context:
// the place of each heading, the result texts, the file mode, no write for a
// complete file, a failed write, and a file of exactly the size limit.
func TestPlanPreplanContextRepair(t *testing.T) {
	setPreplanNow(t, preplanTestNow)
	const hdr = "| # | Decision | Reason |\n|---|---|---|\n"

	t.Run("a 4-section file gets the 3 absent headings", func(t *testing.T) {
		root := t.TempDir()
		const before = "# Preplan: auth flow\n\n**Status:** in progress\n\n## Goal\n\nShip faster.\n\n## Users and effect\n\n## Flows\n\n## Decisions\n\n" + hdr
		file := writePreplanTopic(t, root, "auth-flow.md", before)
		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		wantPreplanBytes(t, file, "# Preplan: auth flow\n\n**Status:** in progress\n\n## Goal\n\nShip faster.\n\n"+
			"## Problem and benefit\n\n## Users and effect\n\n## Risk if wrong\n\n## Non-goals\n\n## Flows\n\n## Decisions\n\n"+hdr)
		if want := preplanHeadings("Problem and benefit", "Risk if wrong", "Non-goals"); out.AddedSections == nil || !reflect.DeepEqual(*out.AddedSections, want) {
			t.Errorf("AddedSections = %v, want %q", out.AddedSections, want)
		}
		if want := "0 guardrail(s) loaded — none configured. Topic file auth-flow.md exists. Added 3 section heading(s): ## Problem and benefit, ## Risk if wrong, ## Non-goals."; out.Summary != want {
			t.Errorf("Summary = %q, want %q", out.Summary, want)
		}
		if want := "Read preplanFile: preplan_context added 3 section heading(s). Then continue with its first gap: the first empty section, then the open questions, if any."; out.Next != want {
			t.Errorf("Next = %q, want %q", out.Next, want)
		}
		if out.PreplanCreated == nil || *out.PreplanCreated {
			t.Errorf("PreplanCreated = %v, want false", out.PreplanCreated)
		}
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o644 {
			t.Errorf("file mode = %v, want 0644: the rewrite keeps the mode of the old file", mode)
		}
	})

	t.Run("the rewrite keeps a mode that the user set", func(t *testing.T) {
		root := t.TempDir()
		file := writePreplanTopic(t, root, "auth-flow.md", "# Preplan: auth flow\n\n**Status:** in progress\n")
		if err := os.Chmod(file, 0o640); err != nil {
			t.Fatal(err)
		}
		if _, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"}); err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o640 {
			t.Errorf("file mode = %v, want 0640", mode)
		}
	})

	t.Run("an open fence at the end gets a closing line that the summary names", func(t *testing.T) {
		root := t.TempDir()
		const before = "# Preplan: auth flow\n\n**Status:** in progress\n\n## Goal\n\n```mermaid\nflowchart LR\n"
		file := writePreplanTopic(t, root, "auth-flow.md", before)
		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		wantPreplanBytes(t, file, before+"```\n\n"+preplanBlocks("\n", preplanSections[1:]...))
		if want := "0 guardrail(s) loaded — none configured. Topic file auth-flow.md exists. Added 6 section heading(s): ## Problem and benefit, ## Users and effect, ## Risk if wrong, ## Non-goals, ## Flows, ## Decisions." +
			" Added a ``` line at the end of the file to close the fence that was still open, so the added headings are not code."; out.Summary != want {
			t.Errorf("Summary = %q, want %q", out.Summary, want)
		}
		// A second call finds every heading and writes nothing.
		noPreplanCheckWrite(t)
		again, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("second preplan_context: %v", err)
		}
		if again.AddedSections == nil || len(*again.AddedSections) != 0 || strings.Contains(again.Summary, "fence") {
			t.Errorf("second call: AddedSections = %v, Summary = %q, want no heading and no fence text", again.AddedSections, again.Summary)
		}
	})

	t.Run("a file with all 7 headings gets no write", func(t *testing.T) {
		noPreplanCheckWrite(t)
		root := t.TempDir()
		file := writePreplanTopic(t, root, "auth-flow.md", preplanSkeletonFor("auth flow"))
		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		if out.AddedSections == nil || len(*out.AddedSections) != 0 {
			t.Errorf("AddedSections = %v, want an empty non-nil list", out.AddedSections)
		}
		wantPreplanBytes(t, file, preplanSkeletonFor("auth flow"))
	})

	t.Run("a write failure leaves the file as it was", func(t *testing.T) {
		swapPreplanSeam(t, &preplanWriteBytes, func(string, []byte) error { return errInjectedPreplan })
		root := t.TempDir()
		const before = "# Preplan: auth flow\n\n**Status:** in progress\n"
		file := writePreplanTopic(t, root, "auth-flow.md", before)
		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if got := errorClassOf(err); got != "infra" {
			t.Fatalf("error class = %q, want infra (err %v)", got, err)
		}
		if want := "preplan_context: " + errInjectedPreplan.Error(); err.Error() != want {
			t.Errorf("err.Error() = %q, want %q", err.Error(), want)
		}
		if want := "Check write permission on " + filepath.Dir(file) + " and free disk space, then run /sdlc:preplan again."; suggestionOf(err) != want {
			t.Errorf("Suggestion = %q, want %q", suggestionOf(err), want)
		}
		if !errors.Is(err, errInjectedPreplan) {
			t.Errorf("err = %v, want it to wrap the write error", err)
		}
		if out.PreplanFile != "" || out.AddedSections != nil {
			t.Errorf("output = %+v, want empty on a failed write", out)
		}
		wantPreplanBytes(t, file, before)
	})

	t.Run("a repair to exactly dashboardDeleteReadMax bytes is written and read again", func(t *testing.T) {
		root := t.TempDir()
		head := "**Status:** in progress\n"
		addition := "\n" + preplanBlocks("\n", preplanSections...)
		content := head + preplanFiller(dashboardDeleteReadMax-len(head)-len(addition))
		file := writePreplanTopic(t, root, "auth-flow.md", content)
		out, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		if out.AddedSections == nil || len(*out.AddedSections) != len(preplanSections) {
			t.Errorf("AddedSections = %v, want the 7 headings", out.AddedSections)
		}
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if want := content + addition; string(got) != want || len(got) != dashboardDeleteReadMax {
			t.Errorf("file has %d bytes, want %d: the content then a blank line and the 7 blocks", len(got), dashboardDeleteReadMax)
		}
		// The next calls read the repaired file: it is not over the limit.
		if _, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"}); err != nil {
			t.Errorf("second preplan_context: %v, want the repaired file read", err)
		}
		if _, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0)); err != nil {
			t.Errorf("preplan_check: %v, want the repaired file read", err)
		}
	})

	t.Run("a repair past dashboardDeleteReadMax bytes is a DataError and writes nothing", func(t *testing.T) {
		noPreplanCheckWrite(t)
		root := t.TempDir()
		head := "**Status:** in progress\n"
		content := head + preplanFiller(dashboardDeleteReadMax-len(head))
		file := writePreplanTopic(t, root, "auth-flow.md", content)
		_, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"})
		if got := errorClassOf(err); got != "data" {
			t.Fatalf("error class = %q, want data (err %v)", got, err)
		}
		size := len(content) + len("\n"+preplanBlocks("\n", preplanSections...))
		if want := fmt.Sprintf("preplan_context: adding 7 section heading(s) makes %s %d bytes, more than the %d bytes (%s) that preplan_context, preplan_check and preplan_write read; nothing was written",
			file, size, dashboardDeleteReadMax, dashboardDeleteReadMaxText); err.Error() != want {
			t.Errorf("err.Error() = %q, want %q", err.Error(), want)
		}
		if want := "Make the topic file smaller than " + dashboardDeleteReadMaxText + " by hand, with room for the absent headings, then run /sdlc:preplan again."; suggestionOf(err) != want {
			t.Errorf("Suggestion = %q, want %q", suggestionOf(err), want)
		}
		wantPreplanBytes(t, file, content)
	})
}

// preplanParityNoHeadings is a topic file of TestPreplanContextCheckParity
// with a Status line and no section heading.
const preplanParityNoHeadings = "# Preplan: auth flow\n\n**Status:** in progress\n"

// The texts of TestPreplanContextCheckParity: the file after the heading
// repair of preplanParityNoHeadings, and a new topic file.
var (
	// preplanParityRepaired is preplanParityNoHeadings after the heading
	// repair of preplan_context.
	preplanParityRepaired = preplanParityNoHeadings + "\n" + preplanBlocks("\n", preplanSections...)
	// preplanParityComplete is a new topic file with all 7 headings.
	preplanParityComplete = preplanSkeletonFor("auth flow")
)

// The Suggestions of TestPreplanContextCheckParity. {file}, {dir} and {root}
// stand for the topic file path, its folder and the root of the call.
const (
	// preplanParityRemoveOrRename is the Suggestion for a folder or a pipe.
	preplanParityRemoveOrRename = "Remove or rename {file}, then run /sdlc:preplan again. preplan_context then creates a new topic file."
	// preplanParityRemoveLink is the Suggestion for a link loop.
	preplanParityRemoveLink = "Remove the link with rm {file}, then run /sdlc:preplan again. preplan_context then creates a new topic file."
	// preplanParityFolderLink is the Suggestion for a preplan folder that is
	// a dangling link to {root}/missing.
	preplanParityFolderLink = "Start a new session so the session-start hook repairs the link, or run: mkdir -p {root}/missing"
)

// preplanParityWant is the result of one action in
// TestPreplanContextCheckParity. class "" is success, and check runs more
// checks on it. stem starts the error message. suggestion, when set, is the
// full Suggestion of the error. skip marks an action that the row does not
// run.
type preplanParityWant struct {
	class, stem, suggestion string
	skip                    bool
	check                   func(t *testing.T, root, file string, out PlanSupportOut)
}

// preplanParityRow is one file state of TestPreplanContextCheckParity and the
// result of each action on it. setup makes the state at file, in the preplan
// folder of root. wrIn, when set, replaces preplanParityWriteIn for the
// preplan_write call of the row.
type preplanParityRow struct {
	name         string
	setup        func(t *testing.T, root, file string)
	ctx, chk, wr preplanParityWant
	wrIn         *PlanSupportIn
}

// preplanParityWriteIn is the preplan_write call of a parity row: a status
// write that leaves a file with this status unchanged.
var preplanParityWriteIn = PlanSupportIn{Action: preplanWriteAction, Topic: "auth flow", TopicStatus: preplanStatusInProgress}

// preplanParityExpand puts the paths of one parity case into text.
func preplanParityExpand(text, root, file string) string {
	return strings.NewReplacer("{file}", file, "{dir}", filepath.Dir(file), "{root}", root).Replace(text)
}

// preplanParityAdded returns a check of AddedSections (n headings) and of
// the file bytes (content).
func preplanParityAdded(n int, content string) func(t *testing.T, root, file string, out PlanSupportOut) {
	return func(t *testing.T, _, file string, out PlanSupportOut) {
		if out.AddedSections == nil || len(*out.AddedSections) != n {
			t.Errorf("AddedSections = %v, want %d heading(s)", out.AddedSections, n)
		}
		wantPreplanBytes(t, file, content)
	}
}

// preplanParityBytes returns a check that the file holds content and that
// Written is ["Status"].
func preplanParityBytes(content string) func(t *testing.T, root, file string, out PlanSupportOut) {
	return func(t *testing.T, _, file string, out PlanSupportOut) {
		if out.Written == nil || !reflect.DeepEqual(*out.Written, []string{preplanWriteStatusPart}) {
			t.Errorf("Written = %v, want [Status]", out.Written)
		}
		wantPreplanBytes(t, file, content)
	}
}

// preplanParityCheckLine checks that preplan_check wrote its line into the
// file.
func preplanParityCheckLine(t *testing.T, _, file string, out PlanSupportOut) {
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if out.GuardrailCheckLine == "" || !strings.Contains(string(got), out.GuardrailCheckLine+"\n") {
		t.Errorf("file lacks the check line %q:\n%s", out.GuardrailCheckLine, got)
	}
}

// preplanParitySummary returns a check that the summary holds want.
func preplanParitySummary(want string) func(t *testing.T, root, file string, out PlanSupportOut) {
	return func(t *testing.T, _, _ string, out PlanSupportOut) {
		if !strings.Contains(out.Summary, want) {
			t.Errorf("Summary = %q, want it to hold %q", out.Summary, want)
		}
	}
}

// preplanParityFenceFile is a topic file whose Flows body opens a fence that
// never closes, so the Decisions heading after it is code.
var preplanParityFenceFile = strings.Replace(preplanSkeletonFor("auth flow"), "## Flows\n\n", "## Flows\n\n```mermaid\nflowchart LR\n", 1)

// preplanParityFenceWarning is the summary warning of an open fence.
const preplanParityFenceWarning = "A ``` fence is still open at the end of the file"

// preplanParityConfigRow is the row of a plan config that cannot be read:
// preplan_context and preplan_check name the warning, preplan_write reads no
// config.
var preplanParityConfigRow = preplanParityRow{
	name: "a regular file and a plan config that cannot be read",
	setup: func(t *testing.T, root, file string) {
		writeFile(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "[plan\nguardrails = [\n")
		writeFile(t, file, preplanParityNoHeadings)
	},
	ctx: preplanParityWant{check: func(t *testing.T, root, file string, out PlanSupportOut) {
		preplanParityAdded(7, preplanParityRepaired)(t, root, file, out)
		_, warning := loadGuardrails(root)
		if warning == "" || !strings.Contains(out.Summary, "Warning: "+warning+".") {
			t.Errorf("Summary = %q, want the warning %q", out.Summary, warning)
		}
		if !strings.HasSuffix(out.Next, " preplan_check writes the warning into the Guardrail check line at Step 4.") {
			t.Errorf("Next = %q, want the warning sentence at the end", out.Next)
		}
	}},
	chk: preplanParityWant{check: func(t *testing.T, root, file string, out PlanSupportOut) {
		preplanParityCheckLine(t, root, file, out)
		_, warning := loadGuardrails(root)
		if want := " Guardrails unavailable: " + preplanWarningText(warning) + "."; warning == "" || !strings.HasSuffix(out.GuardrailCheckLine, want) {
			t.Errorf("GuardrailCheckLine = %q, want the suffix %q", out.GuardrailCheckLine, want)
		}
	}},
	wr: preplanParityWant{check: preplanParityBytes(preplanParityNoHeadings)},
}

// preplanParityRows are the file states of TestPreplanContextCheckParity, one
// for each file state that the three preplan actions can meet.
var preplanParityRows = []preplanParityRow{
	{name: "no file", setup: func(*testing.T, string, string) {},
		ctx: preplanParityWant{check: func(t *testing.T, root, _ string, out PlanSupportOut) {
			if out.PreplanCreated == nil || !*out.PreplanCreated {
				t.Errorf("PreplanCreated = %v, want true", out.PreplanCreated)
			}
			if out.AddedSections == nil || len(*out.AddedSections) != 0 {
				t.Errorf("AddedSections = %v, want an empty non-nil list", out.AddedSections)
			}
			wantPreplanBytes(t, filepath.Join(root, ".sdlc-v2", "preplan", "2026-10-12-auth-flow.md"), preplanParityComplete)
		}},
		chk: preplanParityWant{class: "domain", stem: `preplan_check: no topic file for topic "auth flow"`, suggestion: preplanNoFileSuggestion},
		wr:  preplanParityWant{class: "domain", stem: `preplan_write: no topic file for topic "auth flow"`, suggestion: preplanNoFileSuggestion}},
	{name: "two files for one slug", setup: func(t *testing.T, root, file string) {
		noPreplanCheckWrite(t)
		writeFile(t, file, preplanParityNoHeadings)
		writePreplanTopic(t, root, "2026-10-01-auth-flow.md", preplanParityNoHeadings)
	},
		ctx: preplanParityWant{class: "data", stem: `preplan_context: topic "auth flow" matches 2 topic files`},
		chk: preplanParityWant{class: "data", stem: `preplan_check: topic "auth flow" matches 2 topic files`},
		wr:  preplanParityWant{class: "data", stem: `preplan_write: topic "auth flow" matches 2 topic files`}},
	{name: "a regular file with no Status and no Started line", setup: func(t *testing.T, _, file string) {
		writeFile(t, file, "# Preplan: auth flow\n\nnotes\n")
	},
		ctx: preplanParityWant{check: preplanParityAdded(7, "# Preplan: auth flow\n\nnotes\n\n"+preplanBlocks("\n", preplanSections...))},
		chk: preplanParityWant{class: "data", stem: "preplan_check: {file} has no **Started:** line"},
		wr:  preplanParityWant{check: preplanParityBytes("# Preplan: auth flow\n\n**Status:** in progress\n\nnotes\n")}},
	preplanParityConfigRow,
	{name: "a regular file with all 7 headings", setup: func(t *testing.T, _, file string) {
		writeFile(t, file, preplanParityComplete)
	},
		ctx: preplanParityWant{check: preplanParityAdded(0, preplanParityComplete)},
		chk: preplanParityWant{check: preplanParityCheckLine},
		wr:  preplanParityWant{check: preplanParityBytes(preplanParityComplete)}},
	{name: "a regular file with absent headings", setup: func(t *testing.T, _, file string) {
		writeFile(t, file, preplanParityNoHeadings)
	},
		ctx: preplanParityWant{check: func(t *testing.T, root, file string, out PlanSupportOut) {
			preplanParityAdded(7, preplanParityRepaired)(t, root, file, out)
			if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o644 {
				t.Errorf("file mode = %v (err %v), want 0644: the rewrite keeps the mode", info, err)
			}
		}},
		chk: preplanParityWant{check: preplanParityCheckLine},
		wr:  preplanParityWant{check: preplanParityBytes(preplanParityNoHeadings)}},
	{name: "a code fence open at the end, the target heading before it", setup: func(t *testing.T, _, file string) {
		writeFile(t, file, preplanParityFenceFile)
	},
		ctx:  preplanParityWant{check: preplanParitySummary("Added a ``` line at the end of the file to close the fence")},
		chk:  preplanParityWant{check: preplanParitySummary(preplanParityFenceWarning)},
		wr:   preplanParityWant{check: preplanParitySummary(preplanParityFenceWarning)},
		wrIn: &PlanSupportIn{Action: preplanWriteAction, Topic: "auth flow", Section: "Goal", Content: preplanContentPtr("Ship it.")}},
	{name: "a code fence open at the end hides the target heading", setup: func(t *testing.T, _, file string) {
		noPreplanCheckWrite(t)
		writeFile(t, file, preplanParityFenceFile)
	},
		ctx: preplanParityWant{skip: true}, chk: preplanParityWant{skip: true},
		wr: preplanParityWant{class: "data", stem: `preplan_write: section "Decisions" not found in {file}`,
			suggestion: preplanNotFoundSuggestion},
		wrIn: &PlanSupportIn{Action: preplanWriteAction, Topic: "auth flow", Section: "Decisions", Content: preplanContentPtr("")}},
	{name: "a live link to a regular file", setup: func(t *testing.T, _, file string) {
		noPreplanCheckWrite(t)
		target := filepath.Join(t.TempDir(), "target.md")
		writeFile(t, target, preplanParityNoHeadings)
		if err := os.Symlink(target, file); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { wantPreplanBytes(t, target, preplanParityNoHeadings) })
	},
		ctx: preplanParityWant{class: "domain", stem: "preplan_context: {file} is a symlink, not a regular file"},
		chk: preplanParityWant{class: "domain", stem: "preplan_check: {file} is a symlink, not a regular file"},
		wr:  preplanParityWant{class: "domain", stem: "preplan_write: {file} is a symlink, not a regular file"}},
	{name: "a named pipe", setup: func(t *testing.T, _, file string) {
		noPreplanCheckWrite(t)
		// A regular file stands in for the pipe, and the preplanLstat seam
		// reports the pipe mode, so the row runs on every platform.
		writeFile(t, file, preplanParityNoHeadings)
		swapPreplanSeam(t, &preplanLstat, func(string) (fs.FileInfo, error) {
			return preplanFakeInfo{mode: fs.ModeNamedPipe | 0o644}, nil
		})
	},
		ctx: preplanParityWant{class: "domain", stem: "preplan_context: {file} is not a regular file", suggestion: preplanParityRemoveOrRename},
		chk: preplanParityWant{class: "domain", stem: "preplan_check: {file} is not a regular file", suggestion: preplanParityRemoveOrRename},
		wr:  preplanParityWant{class: "domain", stem: "preplan_write: {file} is not a regular file", suggestion: preplanParityRemoveOrRename}},
	{name: "a file over 8 MiB", setup: func(t *testing.T, _, file string) {
		noPreplanCheckWrite(t)
		swapPreplanSeam(t, &preplanReadFile, func(path string) ([]byte, error) {
			t.Errorf("preplanReadFile(%s) was called, want no read of a big file", path)
			return nil, errInjectedPreplan
		})
		writeFile(t, file, preplanParityNoHeadings+preplanFiller(dashboardDeleteReadMax+1-len(preplanParityNoHeadings)))
	},
		ctx: preplanParityWant{class: "data", stem: fmt.Sprintf("preplan_context: {file} is %d bytes, more than", dashboardDeleteReadMax+1)},
		chk: preplanParityWant{class: "data", stem: fmt.Sprintf("preplan_check: {file} is %d bytes, more than", dashboardDeleteReadMax+1)},
		wr:  preplanParityWant{class: "data", stem: fmt.Sprintf("preplan_write: {file} is %d bytes, more than", dashboardDeleteReadMax+1)}},
	{name: "a write that passes 8 MiB", setup: func(t *testing.T, _, file string) {
		noPreplanCheckWrite(t)
		writeFile(t, file, preplanParityNoHeadings+preplanFiller(dashboardDeleteReadMax-1-len(preplanParityNoHeadings)))
	},
		ctx: preplanParityWant{class: "data", stem: "preplan_context: adding 7 section heading(s) makes {file}"},
		chk: preplanParityWant{skip: true},
		wr: preplanParityWant{class: "data", stem: fmt.Sprintf("preplan_write: {file} would be %d bytes, more than %d", dashboardDeleteReadMax+2, dashboardDeleteReadMax),
			suggestion: "Make the topic file smaller than 8 MiB by hand, then call preplan_write again."},
		wrIn: &PlanSupportIn{Action: preplanWriteAction, Topic: "auth flow", TopicStatus: preplanStatusReadyForPlan}},
	{name: "a dangling link", setup: func(t *testing.T, _, file string) {
		noPreplanCheckWrite(t)
		if err := os.Symlink(filepath.Join(t.TempDir(), "gone.md"), file); err != nil {
			t.Fatal(err)
		}
	},
		ctx: preplanParityWant{class: "infra", stem: "preplan_context: create {file}: "},
		chk: preplanParityWant{class: "domain", stem: "preplan_check: {file} is a link to "},
		wr:  preplanParityWant{class: "domain", stem: "preplan_write: {file} is a link to "}},
	{name: "the preplan folder is a dangling link", setup: func(t *testing.T, root, file string) {
		dir := filepath.Dir(file)
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, "missing"), dir); err != nil {
			t.Fatal(err)
		}
	},
		ctx: preplanParityWant{class: "infra", stem: "preplan_context: create {dir}/2026-10-12-auth-flow.md: {dir} is a link to {root}/missing", suggestion: preplanParityFolderLink},
		chk: preplanParityWant{class: "infra", stem: "preplan_check: list {dir}: {dir} is a link to {root}/missing, which does not exist.", suggestion: preplanParityFolderLink},
		wr:  preplanParityWant{class: "infra", stem: "preplan_write: list {dir}: {dir} is a link to {root}/missing, which does not exist.", suggestion: preplanParityFolderLink}},
	{name: "a file that went away after the folder list", setup: func(t *testing.T, _, file string) {
		noPreplanCheckWrite(t)
		writeFile(t, file, preplanParityNoHeadings)
		swapPreplanSeam(t, &preplanLstat, func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist })
	},
		ctx: preplanParityWant{class: "domain", stem: "preplan_context: ", suggestion: preplanGoneSuggestion},
		chk: preplanParityWant{class: "domain", stem: "preplan_check: ", suggestion: preplanNoFileSuggestion},
		wr:  preplanParityWant{class: "domain", stem: "preplan_write: ", suggestion: preplanNoFileSuggestion}},
	{name: "a folder", setup: func(t *testing.T, _, file string) {
		noPreplanCheckWrite(t)
		if err := os.Mkdir(file, 0o755); err != nil {
			t.Fatal(err)
		}
	},
		ctx: preplanParityWant{class: "domain", stem: "preplan_context: {file} is a folder, not a regular file", suggestion: preplanParityRemoveOrRename},
		chk: preplanParityWant{class: "domain", stem: "preplan_check: {file} is a folder, not a regular file", suggestion: preplanParityRemoveOrRename},
		wr:  preplanParityWant{class: "domain", stem: "preplan_write: {file} is a folder, not a regular file", suggestion: preplanParityRemoveOrRename}},
	{name: "a link loop", setup: func(t *testing.T, _, file string) {
		noPreplanCheckWrite(t)
		other := filepath.Join(filepath.Dir(file), "other.md")
		if err := os.Symlink(other, file); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(file, other); err != nil {
			t.Fatal(err)
		}
	},
		ctx: preplanParityWant{class: "domain", stem: "preplan_context: {file} is a symlink whose target cannot be read: stat ", suggestion: preplanParityRemoveLink},
		chk: preplanParityWant{class: "domain", stem: "preplan_check: {file} is a symlink whose target cannot be read: stat ", suggestion: preplanParityRemoveLink},
		wr:  preplanParityWant{class: "domain", stem: "preplan_write: {file} is a symlink whose target cannot be read: stat ", suggestion: preplanParityRemoveLink}},
	{name: "an Lstat error", setup: func(t *testing.T, _, file string) {
		noPreplanCheckWrite(t)
		writeFile(t, file, preplanParityNoHeadings)
		swapPreplanSeam(t, &preplanLstat, func(string) (fs.FileInfo, error) { return nil, errInjectedPreplan })
	},
		ctx: preplanParityWant{class: "infra", stem: "preplan_context: " + errInjectedPreplan.Error()},
		chk: preplanParityWant{class: "infra", stem: "preplan_check: " + errInjectedPreplan.Error()},
		wr: preplanParityWant{class: "infra", stem: "preplan_write: " + errInjectedPreplan.Error(),
			suggestion: "Fix the cause that the message names, then call preplan_write again."}},
	{name: "a read error", setup: func(t *testing.T, _, file string) {
		noPreplanCheckWrite(t)
		writeFile(t, file, preplanParityNoHeadings)
		swapPreplanSeam(t, &preplanReadFile, func(string) ([]byte, error) { return nil, errInjectedPreplan })
	},
		ctx: preplanParityWant{class: "infra", stem: "preplan_context: " + errInjectedPreplan.Error()},
		chk: preplanParityWant{class: "infra", stem: "preplan_check: " + errInjectedPreplan.Error()},
		wr: preplanParityWant{class: "infra", stem: "preplan_write: " + errInjectedPreplan.Error(),
			suggestion: "Fix the cause that the message names, then call preplan_write again."}},
	{name: "a write error", setup: func(t *testing.T, _, file string) {
		writeFile(t, file, preplanParityNoHeadings)
		swapPreplanSeam(t, &preplanWriteBytes, func(string, []byte) error { return errInjectedPreplan })
		t.Cleanup(func() { wantPreplanBytes(t, file, preplanParityNoHeadings) })
	},
		ctx: preplanParityWant{class: "infra", stem: "preplan_context: " + errInjectedPreplan.Error()},
		chk: preplanParityWant{class: "infra", stem: "preplan_check: " + errInjectedPreplan.Error()},
		wr: preplanParityWant{class: "infra", stem: "preplan_write: " + errInjectedPreplan.Error(),
			suggestion: "Check write permission on {dir} and free disk space, then call preplan_write again."}},
}

// TestPreplanContextCheckParity runs each state of the topic path through
// preplan_context, preplan_check and preplan_write, each in a new root, and
// checks the error class, the message stem and the Suggestion of each action
// (sibling-action-input-policy-parity).
func TestPreplanContextCheckParity(t *testing.T) {
	setPreplanNow(t, preplanTestNow)
	for _, row := range preplanParityRows {
		wrIn := preplanParityWriteIn
		if row.wrIn != nil {
			wrIn = *row.wrIn
		}
		for _, a := range []struct {
			in   PlanSupportIn
			want preplanParityWant
		}{
			{PlanSupportIn{Action: preplanContextAction, Topic: "auth flow"}, row.ctx},
			{preplanCheckIn("auth flow", 1, 0, 0), row.chk},
			{wrIn, row.wr},
		} {
			if a.want.skip {
				continue
			}
			t.Run(row.name+": "+a.in.Action, func(t *testing.T) {
				runPreplanParityCase(t, row, a.in, a.want)
			})
		}
	}
}

// runPreplanParityCase makes the state of row in a new root, calls
// plan_support with in and checks the result against want.
func runPreplanParityCase(t *testing.T, row preplanParityRow, in PlanSupportIn, want preplanParityWant) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".sdlc-v2", "preplan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "auth-flow.md")
	row.setup(t, root, file)

	out, err := planSupportCore(root, root, in)
	if want.class == "" {
		if err != nil {
			t.Fatalf("%s: %v, want success", in.Action, err)
		}
		want.check(t, root, file, out)
		return
	}
	if got := errorClassOf(err); got != want.class {
		t.Fatalf("error class = %q, want %s (err %v)", got, want.class, err)
	}
	if stem := preplanParityExpand(want.stem, root, file); !strings.HasPrefix(err.Error(), stem) {
		t.Errorf("err.Error() = %q, want the stem %q", err.Error(), stem)
	}
	if suggestionOf(err) == "" {
		t.Error("Suggestion is empty, want recovery text")
	}
	if want.suggestion != "" {
		if s := preplanParityExpand(want.suggestion, root, file); suggestionOf(err) != s {
			t.Errorf("Suggestion = %q, want %q", suggestionOf(err), s)
		}
	}
}

// TestPreplanCheckOpenFence checks that preplan_check names a fence that is
// still open at the end of the file in its summary and its next text, and
// that preplanEmptySections returns the open fence run, so the sections after
// the fence are not reported as empty with no warning.
func TestPreplanCheckOpenFence(t *testing.T) {
	setPreplanNow(t, preplanCheckTestNow)
	topic := strings.Replace(preplanTopicWithBodies(nil), "## Flows\n\ntext\n", "## Flows\n\n```mermaid\nflowchart LR\n", 1)

	if empty, open := preplanEmptySections([]byte(topic)); open != "```" || !reflect.DeepEqual(empty, preplanHeadings("Decisions")) {
		t.Errorf("preplanEmptySections = %q, %q, want [## Decisions] and ```", empty, open)
	}
	if _, open := preplanEmptySections([]byte(preplanTopicWithBodies(nil))); open != "" {
		t.Errorf("a file with no open fence gives openFence %q, want empty", open)
	}

	root := t.TempDir()
	writePreplanTopic(t, root, "auth-flow.md", topic)
	out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
	if err != nil {
		t.Fatalf("preplan_check: %v", err)
	}
	if want := "Added the Guardrail check line to auth-flow.md. Empty sections: ## Decisions." +
		" A ``` fence is still open at the end of the file, so each ## heading after it counts as absent."; out.Summary != want {
		t.Errorf("Summary = %q, want %q", out.Summary, want)
	}
	if want := "Read preplanFile again before your next edit: preplan_check rewrote it. Then follow the Step 4 route table." +
		" emptySections lists 1 empty section(s). The Step 4 route table of the skill picks the next step." +
		" First close the open ``` fence with tool calls, not a file edit: call plan_support preplan_context with the same topic; when its summary names no closing fence line, send the full body of the last section with preplan_write, with the fence closed. Each ## heading after the fence counts as absent until then."; out.Next != want {
		t.Errorf("Next = %q, want %q", out.Next, want)
	}
}

// TestPreplanCheckEmptySectionsOverMCP pins how the MCP renderer shows
// emptySections: "(none)" for an empty list (the reason for the pointer),
// and one bullet for each heading otherwise.
func TestPreplanCheckEmptySectionsOverMCP(t *testing.T) {
	setPreplanNow(t, preplanCheckTestNow)
	root := newOpenspecStageFixture(t, "")
	args := map[string]any{"action": "preplan_check", "topic": "auth flow", "passCount": 1, "keptCount": 0, "droppedCount": 0}
	file := filepath.Join(root, ".sdlc-v2", "preplan", "auth-flow.md")

	writeFile(t, file, preplanTopicWithBodies(nil))
	if text := evidenceRender(t, root, args); !strings.Contains(text, "- emptySections: (none)\n") {
		t.Errorf("a full file must render emptySections: (none):\n%s", text)
	}

	writeFile(t, file, preplanTopicWithBodies(map[string]string{"Risk if wrong": "", "Non-goals": ""}))
	if text := evidenceRender(t, root, args); !strings.Contains(text, "- emptySections:\n  - ## Risk if wrong\n  - ## Non-goals\n") {
		t.Errorf("a file with 2 empty sections must list them:\n%s", text)
	}
}

// preplanWriteDescriptionPinned is the preplan_write line of the tool
// description, word for word.
const preplanWriteDescriptionPinned = "- preplan_write: Replace one section body or the Status line of an existing topic file. Requires topic, and section with content, or topicStatus. content is required with section and not allowed without it; an empty string counts as content. section, content and topicStatus sent with another action return DomainError. It finds and reads the file as preplan_check does and never creates one. It checks content against the topic-file format rules and refuses a body that breaks one, with the rule id. Before the check it changes each CR LF in content to LF and removes the final line ends; the rules, the 65536-byte limit of rule P1 included, check that value, and the file stores it with the line ends of the file. It replaces the body under the first matching section heading, adds an absent Open questions section after Decisions, and deletes the Open questions or Guardrail check section when content is empty. When section is the last section and a code fence in its body never closes, the replace or the delete runs to the end of the file: it also removes the text after the fence, each ## heading that the fence hides included. summary gives the line count of the old body. It replaces the first Status line in the first 4096 bytes, else inserts one before the Started line, else after the title line, else as the first line. summary names each extra heading or Status line that stays. It writes a temp file and renames it, and the file keeps its mode. topicStatus ready for plan also lists the preplan folder and reads the first 4096 bytes of each other topic file there (the newest 100). It returns readyTopicFiles (this file first, then up to 3 other ready files, newest first) and readyLeftOut. A failed list or head read of another file does not fail the call: summary names it. Returns preplanFile, written, readyTopicFiles, readyLeftOut, summary, next. A bad input or a broken rule returns DomainError and writes nothing. A missing file, a link or a file that is not regular returns DomainError. Two matching files, a file over 8 MiB, a write that would make it larger than 8 MiB, or a section heading that is absent or hidden by an open fence returns DataError. A list failure of the preplan folder, or a preplan folder that holds more than 10000 names, returns InfraError. An Lstat, read or write failure of the topic file returns InfraError and leaves the file as it was. A dangling link at the preplan folder returns InfraError."

// preplanDescriptionIdents are the Go identifiers that the preplan lines of
// the tool description must not name: the calling model cannot resolve them.
var preplanDescriptionIdents = []string{
	"preplanSections", "preplanWriteSections", "PreplanStatuses", "preplanReadyMax",
	"dashboardPreplans", "preplanReadyRows", "dashboardPreplanHeadMax", "dashboardDeleteReadMax",
	"preplanCheckSection", "dashboardReplaceStatusLine", "readPreplanTopic", "preplanWriteBytes",
}

// TestPreplanToolDescription checks that the preplan lines of the tool
// description and the jsonschema descriptions of addedSections and
// emptySections carry the numbers and the section order that the code uses,
// that the preplan_write line is the pinned text, and that the description
// names no Go identifier the calling model cannot resolve.
func TestPreplanToolDescription(t *testing.T) {
	desc := preplanToolDescription()
	order := strings.Join(preplanSections, ", ")
	for _, want := range []string{
		strconv.Itoa(len(preplanSections)) + "-section skeleton",
		"(the ## sections " + order + ", in this skeleton order)",
		"(at most " + strconv.Itoa(preplanDirMax) + ")",
		"A file over " + dashboardDeleteReadMaxText + ", or a repair that would make it larger than " + dashboardDeleteReadMaxText,
		"in the first " + strconv.Itoa(dashboardPreplanHeadMax) + " bytes",
		"from 0 to " + strconv.Itoa(preplanCountMax),
		"it first adds a closing fence line there",
		"a second call writes nothing",
		"so it refuses such a file with DataError",
		"with the same DomainError and Suggestion as preplan_check",
		"the file keeps its mode",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("preplanToolDescription lacks %q", want)
		}
	}
	if !strings.HasSuffix(desc, "\n"+preplanWriteDescriptionPinned) {
		t.Errorf("preplanToolDescription does not end with the pinned preplan_write line:\n%s", desc)
	}
	for _, ident := range preplanDescriptionIdents {
		if strings.Contains(desc, ident) {
			t.Errorf("preplanToolDescription names the Go identifier %s", ident)
		}
	}

	typ := reflect.TypeOf(PlanSupportOut{})
	for _, field := range []string{"AddedSections", "EmptySections"} {
		f, ok := typ.FieldByName(field)
		if !ok {
			t.Fatalf("PlanSupportOut has no field %s", field)
		}
		tag := f.Tag.Get("jsonschema_description")
		if !strings.Contains(tag, "skeleton order ("+order+")") {
			t.Errorf("%s description %q does not write out the skeleton order %q", field, tag, order)
		}
		if strings.Contains(tag, "preplanSections") {
			t.Errorf("%s description names the Go identifier preplanSections", field)
		}
	}
}

// ---------------------------------------------------------------------------
// preplan_write
// ---------------------------------------------------------------------------

// preplanWriteIn returns a preplan_write input for the topic "auth flow".
// content is sent when section or content is set, so an empty content with a
// section is an explicit empty string; with neither, content is omitted.
func preplanWriteIn(section, content, status string) PlanSupportIn {
	in := PlanSupportIn{Action: preplanWriteAction, Topic: "auth flow", Section: section, TopicStatus: status}
	if section != "" || content != "" {
		in.Content = preplanContentPtr(content)
	}
	return in
}

// preplanContentPtr returns a pointer to content, for PlanSupportIn.Content.
func preplanContentPtr(content string) *string { return &content }

// countPreplanWrites wraps preplanWriteBytes for one test, so each write
// still happens, and returns the count of the writes.
func countPreplanWrites(t *testing.T) *int {
	t.Helper()
	n := new(int)
	write := preplanWriteBytes
	swapPreplanSeam(t, &preplanWriteBytes, func(path string, data []byte) error {
		*n++
		return write(path, data)
	})
	return n
}

// The Decisions bodies of the preplan_write tests: row 1, rows 1 and 2, and
// row 2 only.
const (
	// preplanWriteRowsA is the Decisions table with row 1.
	preplanWriteRowsA = "| # | Decision | Reason |\n|---|---|---|\n| 1 | Add a summary box | Readers compare topic files |"
	// preplanWriteRowsAB is the Decisions table with rows 1 and 2.
	preplanWriteRowsAB = preplanWriteRowsA + "\n| 2 | Add a handoff option | Users draft many topic files |"
	// preplanWriteRowsB is the Decisions table with row 2 only.
	preplanWriteRowsB = "| # | Decision | Reason |\n|---|---|---|\n| 2 | Add a handoff option | Users draft many topic files |"
)

// The topic files of the preplan_write tests.
var (
	// preplanWriteSkeleton is a new topic file.
	preplanWriteSkeleton = preplanSkeletonFor("auth flow")
	// preplanWriteSkeletonHead is preplanWriteSkeleton up to the blank line
	// after the Decisions heading.
	preplanWriteSkeletonHead = strings.TrimSuffix(preplanWriteSkeleton, preplanDecisionsHeader)
)

// preplanWriteNoEOL is a topic file with no final line feed.
const preplanWriteNoEOL = "# Preplan: auth flow\n\n**Status:** in progress\n\n## Goal\n\nold\n\n## Decisions\n\nold rows"

// preplanWriteSectionCase is one row of TestPlanSupportPreplanWriteSections:
// the file before, the calls in order, and the file and the output of the
// last call after them. writes is the count of file writes of all calls.
type preplanWriteSectionCase struct {
	name, before string
	calls        []PlanSupportIn
	after        string
	written      []string
	summaryHas   string
	next         string
	writes       int
}

// preplanWriteSectionCases are the section edits of preplan_write.
var preplanWriteSectionCases = []preplanWriteSectionCase{
	{"two calls: Decisions with row 1, then rows 1 and 2", preplanWriteSkeleton,
		[]PlanSupportIn{preplanWriteIn("Decisions", preplanWriteRowsA, ""), preplanWriteIn("Decisions", preplanWriteRowsAB, "")},
		preplanWriteSkeletonHead + preplanWriteRowsAB + "\n", []string{"Decisions"}, "Replaced the body of ## Decisions.", preplanWriteNextWritten, 2},
	{"third call with row 2 only", preplanWriteSkeleton,
		[]PlanSupportIn{preplanWriteIn("Decisions", preplanWriteRowsA, ""), preplanWriteIn("Decisions", preplanWriteRowsAB, ""), preplanWriteIn("Decisions", preplanWriteRowsB, "")},
		preplanWriteSkeletonHead + preplanWriteRowsB + "\n", []string{"Decisions"}, "Replaced the body of ## Decisions.", preplanWriteNextWritten, 3},
	{"the same call twice gives the same file", preplanWriteSkeleton,
		[]PlanSupportIn{preplanWriteIn("Decisions", preplanWriteRowsAB, ""), preplanWriteIn("Decisions", preplanWriteRowsAB, "")},
		preplanWriteSkeletonHead + preplanWriteRowsAB + "\n", []string{"Decisions"}, "Replaced the body of ## Decisions.", preplanWriteNextWritten, 2},
	{"Open questions on an absent section", preplanWriteSkeleton,
		[]PlanSupportIn{preplanWriteIn("Open questions", "- Which tab?", "")},
		preplanWriteSkeleton + "\n## Open questions\n\n- Which tab?\n", []string{"Open questions"}, "Added the ## Open questions section after ## Decisions.", preplanWriteNextWritten, 1},
	{"Open questions on an absent section, a later section stays after it", preplanWriteSkeleton + "\n## Notes\n\nx\n",
		[]PlanSupportIn{preplanWriteIn("Open questions", "- Which tab?", "")},
		preplanWriteSkeleton + "\n## Open questions\n\n- Which tab?\n\n## Notes\n\nx\n", []string{"Open questions"}, "Added the ## Open questions section after ## Decisions.", preplanWriteNextWritten, 1},
	{"empty content on Open questions", preplanWriteSkeleton + "\n## Open questions\n\n- Which tab?\n",
		[]PlanSupportIn{preplanWriteIn("Open questions", "", "")},
		preplanWriteSkeleton + "\n", []string{"Open questions"}, "Deleted the ## Open questions section.", preplanWriteNextWritten, 1},
	{"empty content on an absent Open questions", preplanWriteSkeleton,
		[]PlanSupportIn{preplanWriteIn("Open questions", "", "")},
		preplanWriteSkeleton, []string{}, "Nothing changed in ## Open questions: the section is absent and content is empty.", preplanWriteNextNoop, 0},
	{"empty content on a present legacy Guardrail check section", strings.Replace(preplanWriteSkeleton, "## Goal\n", "## Guardrail check\n\n3 pass, 0 kept\n\n## Goal\n", 1),
		[]PlanSupportIn{preplanWriteIn("Guardrail check", "", "")},
		preplanWriteSkeleton, []string{"Guardrail check"}, "Deleted the ## Guardrail check section.", preplanWriteNextWritten, 1},
	{"content of line ends only on a legacy Guardrail check section deletes it", strings.Replace(preplanWriteSkeleton, "## Goal\n", "## Guardrail check\n\n3 pass, 0 kept\n\n## Goal\n", 1),
		[]PlanSupportIn{preplanWriteIn("Guardrail check", "\n\n", "")},
		preplanWriteSkeleton, []string{"Guardrail check"}, "Deleted the ## Guardrail check section.", preplanWriteNextWritten, 1},
	{"empty content on an absent Guardrail check section", preplanWriteSkeleton,
		[]PlanSupportIn{preplanWriteIn("Guardrail check", "", "")},
		preplanWriteSkeleton, []string{}, "Nothing changed in ## Guardrail check: the section is absent and content is empty.", preplanWriteNextNoop, 0},
	{"a section before the last one, the file has no final line feed", preplanWriteNoEOL,
		[]PlanSupportIn{preplanWriteIn("Goal", "Ship it.", "")},
		strings.Replace(preplanWriteNoEOL, "## Goal\n\nold\n\n", "## Goal\n\nShip it.\n\n", 1), []string{"Goal"}, "Replaced the body of ## Goal.", preplanWriteNextWritten, 1},
	{"last section replaced, the file has no final line feed", preplanWriteNoEOL,
		[]PlanSupportIn{preplanWriteIn("Decisions", preplanWriteRowsA, "")},
		strings.TrimSuffix(preplanWriteNoEOL, "old rows") + preplanWriteRowsA + "\n", []string{"Decisions"}, "Replaced the body of ## Decisions.", preplanWriteNextWritten, 1},
	{"last heading line with no line end", "# Preplan: auth flow\n\n## Goal",
		[]PlanSupportIn{preplanWriteIn("Goal", "Ship it.", "")},
		"# Preplan: auth flow\n\n## Goal\n\nShip it.\n", []string{"Goal"}, "Replaced the body of ## Goal.", preplanWriteNextWritten, 1},
	{"two ## Decisions headings", preplanWriteSkeleton + "\n## Decisions\n\nold\n",
		[]PlanSupportIn{preplanWriteIn("Decisions", preplanWriteRowsA, "")},
		preplanWriteSkeletonHead + preplanWriteRowsA + "\n\n## Decisions\n\nold\n", []string{"Decisions"}, `1 more "## Decisions" headings stay in the file.`, preplanWriteNextWritten, 1},
	{"a CR LF file keeps CR LF line ends", strings.ReplaceAll(preplanWriteSkeleton, "\n", "\r\n"),
		[]PlanSupportIn{preplanWriteIn("Goal", "Line one\nline two", "")},
		strings.Replace(strings.ReplaceAll(preplanWriteSkeleton, "\n", "\r\n"), "## Goal\r\n\r\n", "## Goal\r\n\r\nLine one\r\nline two\r\n\r\n", 1), []string{"Goal"}, "Replaced the body of ## Goal.", preplanWriteNextWritten, 1},
	{"empty content clears a skeleton section body", strings.Replace(preplanWriteSkeleton, "## Goal\n\n", "## Goal\n\nold goal\n\n", 1),
		[]PlanSupportIn{preplanWriteIn("Goal", "", "")},
		preplanWriteSkeleton, []string{"Goal"}, "Replaced the body of ## Goal.", preplanWriteNextWritten, 1},
	{"the target body holds the fence that never closes", preplanParityFenceFile,
		[]PlanSupportIn{preplanWriteIn("Flows", "```mermaid\nflowchart LR\n```", "")},
		preplanWriteSkeleton[:strings.Index(preplanWriteSkeleton, "## Flows\n")] + "## Flows\n\n```mermaid\nflowchart LR\n```\n", []string{"Flows"},
		"The old body held a code fence that never closed, so the write replaced the text up to the end of the file.", preplanWriteNextWritten, 1},
	{"the target heading is before a fence that never closes", preplanParityFenceFile,
		[]PlanSupportIn{preplanWriteIn("Goal", "Ship it.", "")},
		strings.Replace(preplanParityFenceFile, "## Goal\n\n", "## Goal\n\nShip it.\n\n", 1), []string{"Goal"}, preplanParityFenceWarning, preplanWriteNextWritten, 1},
	{"a section heading on the first line of the file", "## Goal\n\nold\n\n## Decisions\n\nold rows\n",
		[]PlanSupportIn{preplanWriteIn("Goal", "Ship it.", "")},
		"## Goal\n\nShip it.\n\n## Decisions\n\nold rows\n", []string{"Goal"}, "Replaced the body of ## Goal.", preplanWriteNextWritten, 1},
	{"a CR LF file with a fence that never closes keeps CR LF line ends", strings.ReplaceAll(preplanParityFenceFile, "\n", "\r\n"),
		[]PlanSupportIn{preplanWriteIn("Goal", "Ship it.", "")},
		strings.Replace(strings.ReplaceAll(preplanParityFenceFile, "\n", "\r\n"), "## Goal\r\n\r\n", "## Goal\r\n\r\nShip it.\r\n\r\n", 1), []string{"Goal"}, preplanParityFenceWarning, preplanWriteNextWritten, 1},
	{"empty content on Open questions whose body holds a fence that never closes", preplanWriteSkeleton + "\n## Open questions\n\n```\nopen\n## Notes\n",
		[]PlanSupportIn{preplanWriteIn("Open questions", "", "")},
		preplanWriteSkeleton + "\n", []string{"Open questions"},
		"The old body held a code fence that never closed, so the write deleted the text up to the end of the file.", preplanWriteNextWritten, 1},
	{"a section and the status in one write", preplanWriteSkeleton,
		[]PlanSupportIn{preplanWriteIn("Decisions", preplanWriteRowsA, preplanStatusPaused)},
		strings.Replace(preplanWriteSkeletonHead, "in progress", "paused", 1) + preplanWriteRowsA + "\n", []string{"Decisions", "Status"}, "Set the Status line to paused. Old status: in progress.", preplanWriteNextPaused, 1},
}

// TestPlanSupportPreplanWriteSections runs each section edit of
// preplan_write. Each row checks the file bytes, written, summary, next, the
// count of writes, and that the file keeps its mode.
func TestPlanSupportPreplanWriteSections(t *testing.T) {
	for _, tc := range preplanWriteSectionCases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := writePreplanTopic(t, root, "auth-flow.md", tc.before)
			if err := os.Chmod(file, 0o640); err != nil {
				t.Fatal(err)
			}
			writes := countPreplanWrites(t)
			var out PlanSupportOut
			for i, in := range tc.calls {
				var err error
				if out, err = planSupportCore(root, root, in); err != nil {
					t.Fatalf("call %d: %v", i+1, err)
				}
			}
			wantPreplanBytes(t, file, tc.after)
			if out.Written == nil || !reflect.DeepEqual(*out.Written, tc.written) {
				t.Errorf("Written = %v, want %q", out.Written, tc.written)
			}
			if !strings.Contains(out.Summary, tc.summaryHas) {
				t.Errorf("Summary = %q, want it to hold %q", out.Summary, tc.summaryHas)
			}
			if out.Next != tc.next {
				t.Errorf("Next = %q, want %q", out.Next, tc.next)
			}
			if *writes != tc.writes {
				t.Errorf("writes = %d, want %d", *writes, tc.writes)
			}
			if out.PreplanFile != file {
				t.Errorf("PreplanFile = %q, want %q", out.PreplanFile, file)
			}
			if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o640 {
				t.Errorf("file mode = %v (err %v), want 0640: the write keeps the mode", info, err)
			}
		})
	}
}

// preplanWriteStatusCase is one row of TestPlanSupportPreplanWriteStatus: the
// file before and after a write of the status in progress.
type preplanWriteStatusCase struct{ name, before, after, summaryHas string }

// preplanWriteStatusLine and preplanWriteStartedLine are the head lines of
// the Status rows.
const (
	// preplanWriteStatusLine is a Status line with no line end.
	preplanWriteStatusLine = "**Status:** paused"
	// preplanWriteStartedLine is a Started line with its line end.
	preplanWriteStartedLine = "**Started:** 2026-10-12T12:00:00Z\n"
)

// preplanWriteStatusCases are the Status line edits of preplan_write.
var preplanWriteStatusCases = []preplanWriteStatusCase{
	{"Status line that starts at byte dashboardPreplanHeadMax - len(line)",
		preplanFiller(dashboardPreplanHeadMax-len(preplanWriteStatusLine)) + preplanWriteStatusLine + "\n",
		preplanFiller(dashboardPreplanHeadMax-len(preplanWriteStatusLine)) + "**Status:** in progress\n",
		"Set the Status line to in progress. Old status: paused."},
	{"Status line that starts at byte dashboardPreplanHeadMax",
		preplanWriteStartedLine + preplanFiller(dashboardPreplanHeadMax-len(preplanWriteStartedLine)) + preplanWriteStatusLine + "\n",
		"**Status:** in progress\n\n" + preplanWriteStartedLine + preplanFiller(dashboardPreplanHeadMax-len(preplanWriteStartedLine)) + preplanWriteStatusLine + "\n",
		"Inserted the Status line in progress before the Started line."},
	{"a Status line whose prefix runs over byte dashboardPreplanHeadMax is not in the head: a new line goes before Started",
		preplanWriteStartedLine + preplanFiller(dashboardPreplanHeadMax-len(preplanWriteStartedLine)-5) + preplanWriteStatusLine + "\n",
		"**Status:** in progress\n\n" + preplanWriteStartedLine + preplanFiller(dashboardPreplanHeadMax-len(preplanWriteStartedLine)-5) + preplanWriteStatusLine + "\n",
		"Inserted the Status line in progress before the Started line."},
	{"a Status line whose prefix ends in the head and whose value runs past it is replaced whole",
		preplanFiller(dashboardPreplanHeadMax-len("**Status:**")) + preplanWriteStatusLine + "\n",
		preplanFiller(dashboardPreplanHeadMax-len("**Status:**")) + "**Status:** in progress\n",
		"Set the Status line to in progress. Old status: paused."},
	{"no Status line, Started line present",
		"# Preplan: auth flow\n\n" + preplanWriteStartedLine + "\n## Goal\n",
		"# Preplan: auth flow\n\n**Status:** in progress\n\n" + preplanWriteStartedLine + "\n## Goal\n",
		"Inserted the Status line in progress before the Started line."},
	{"no Status and no Started line, title present",
		"# Preplan: auth flow\n\n## Goal\n", "# Preplan: auth flow\n\n**Status:** in progress\n\n## Goal\n",
		"Inserted the Status line in progress after the title line."},
	{"no Status and no Started line, the title is the last line with no line end",
		"# Preplan: auth flow", "# Preplan: auth flow\n\n**Status:** in progress\n",
		"Inserted the Status line in progress after the title line."},
	{"no Status and no Started line, title in a CR LF file",
		"# Preplan: auth flow\r\n\r\n## Goal\r\n", "# Preplan: auth flow\r\n\r\n**Status:** in progress\r\n\r\n## Goal\r\n",
		"Inserted the Status line in progress after the title line."},
	{"no Status, no Started, no title", "## Goal\n\ntext\n", "**Status:** in progress\n\n## Goal\n\ntext\n",
		"Inserted the Status line in progress as the first line."},
	{"an empty file", "", "**Status:** in progress\n", "Inserted the Status line in progress as the first line."},
	{"two Status lines", preplanWriteStatusLine + "\n" + preplanWriteStatusLine + "\n", "**Status:** in progress\n" + preplanWriteStatusLine + "\n",
		"1 more Status lines stay in the file."},
	{"the same status as now", preplanWriteSkeleton, preplanWriteSkeleton, "Set the Status line to in progress. Old status: in progress."},
}

// TestPlanSupportPreplanWriteStatus runs each Status line edit of
// preplan_write: each status write is one write, and written holds Status only.
func TestPlanSupportPreplanWriteStatus(t *testing.T) {
	for _, tc := range preplanWriteStatusCases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := writePreplanTopic(t, root, "auth-flow.md", tc.before)
			writes := countPreplanWrites(t)
			out, err := planSupportCore(root, root, preplanWriteIn("", "", preplanStatusInProgress))
			if err != nil {
				t.Fatalf("preplan_write: %v", err)
			}
			wantPreplanBytes(t, file, tc.after)
			if !strings.Contains(out.Summary, tc.summaryHas) {
				t.Errorf("Summary = %q, want it to hold %q", out.Summary, tc.summaryHas)
			}
			if out.Written == nil || !reflect.DeepEqual(*out.Written, []string{"Status"}) {
				t.Errorf("Written = %v, want [Status]", out.Written)
			}
			if out.Next != preplanWriteNextWritten || *writes != 1 {
				t.Errorf("Next = %q, writes = %d, want %q and 1", out.Next, *writes, preplanWriteNextWritten)
			}
			if out.ReadyTopicFiles != nil || out.ReadyLeftOut != nil {
				t.Errorf("ready fields = %v, %v, want both absent for in progress", out.ReadyTopicFiles, out.ReadyLeftOut)
			}
		})
	}
}

// preplanReadyRow returns a row of the preplan list with the given slug and
// status. Its Path names the file <slug>-path.md, not <slug>.md, so a test
// can tell an output path that comes from Path from one made of Slug, and a
// de-dup by Slug from one by Path.
func preplanReadyRow(slug, status string) DashboardPreplan {
	return DashboardPreplan{Slug: slug, Status: status, Path: ".sdlc-v2/preplan/" + slug + "-path.md"}
}

// preplanWriteReadyCase is one case of the ready list of preplan_write: what
// the preplanReadyRows seam returns, and the names of readyTopicFiles in the
// preplan folder, readyLeftOut and a summary text after the call.
type preplanWriteReadyCase struct {
	name       string
	rows       []DashboardPreplan
	warnings   []dashboardPreplanWarning
	files      []string
	leftOut    int
	summaryHas string
	// summaryLacks is a text that the summary must not hold, or "".
	summaryLacks string
}

// preplanWriteReadyCases are the cases of the ready list of preplan_write.
// The topic file of the call is auth-flow.md. preplanReadyMax is 4: the
// first row has this file and 3 other ready files. Each other entry is the
// Path of its row (<slug>-path.md), and the row of this file goes by its Slug
// auth-flow, whose Path auth-flow-path.md differs from this file.
var preplanWriteReadyCases = []preplanWriteReadyCase{
	{"preplanReadyMax ready files",
		[]DashboardPreplan{preplanReadyRow("auth-flow", preplanStatusReadyForPlan), preplanReadyRow("a", preplanStatusReadyForPlan),
			preplanReadyRow("b", preplanStatusInProgress), preplanReadyRow("c", preplanStatusReadyForPlan), preplanReadyRow("d", preplanStatusReadyForPlan)},
		nil, []string{"auth-flow.md", "a-path.md", "c-path.md", "d-path.md"}, 0, "Topic file auth-flow.md.", ""},
	{"preplanReadyMax + 1 ready files",
		[]DashboardPreplan{preplanReadyRow("a", preplanStatusReadyForPlan), preplanReadyRow("auth-flow", preplanStatusReadyForPlan),
			preplanReadyRow("c", preplanStatusReadyForPlan), preplanReadyRow("d", preplanStatusReadyForPlan), preplanReadyRow("e", preplanStatusReadyForPlan)},
		nil, []string{"auth-flow.md", "a-path.md", "c-path.md", "d-path.md"}, 1, "Topic file auth-flow.md.", ""},
	{"folder list fails after the write", []DashboardPreplan{},
		[]dashboardPreplanWarning{{kind: dashboardPreplanWarnFolder, text: preplanFolderNotReadPrefix + "open x: permission denied", detail: "open x: permission denied"}},
		[]string{"auth-flow.md"}, 0, "Ready topic files not listed: open x: permission denied.", ""},
	{"the seam returns a head-read warning for another topic file", []DashboardPreplan{preplanReadyRow("a", preplanStatusReadyForPlan)},
		[]dashboardPreplanWarning{{kind: dashboardPreplanWarnFile, text: "Preplan b not read: injected"}},
		[]string{"auth-flow.md", "a-path.md"}, 0, "Ready topic files may be incomplete: Preplan b not read: injected.", ""},
	{"older files than the list keeps", []DashboardPreplan{preplanReadyRow("a", preplanStatusReadyForPlan)},
		[]dashboardPreplanWarning{{kind: dashboardPreplanWarnOlder, text: "3 older preplan topic files not shown"}},
		[]string{"auth-flow.md", "a-path.md"}, 0,
		"Ready topic files may be incomplete: 3 older preplan topic files not shown, and readyLeftOut does not count them.", ""},
	{"a start time warning changes no ready file and gives no note", []DashboardPreplan{preplanReadyRow("a", preplanStatusReadyForPlan)},
		[]dashboardPreplanWarning{{kind: dashboardPreplanWarnStart, text: "Preplan b start time not read: \"x\" (expected RFC 3339; duration shown as 0)"}},
		[]string{"auth-flow.md", "a-path.md"}, 0, "Topic file auth-flow.md.", "start time"},
	{"the seam drops the row of this file", []DashboardPreplan{preplanReadyRow("a", preplanStatusReadyForPlan), preplanReadyRow("c", preplanStatusReadyForPlan)},
		nil, []string{"auth-flow.md", "a-path.md", "c-path.md"}, 0, "Topic file auth-flow.md.", ""},
	{"a row whose Slug is this file is not added again, whatever its Path",
		[]DashboardPreplan{preplanReadyRow("auth-flow", preplanStatusReadyForPlan), preplanReadyRow("a", preplanStatusReadyForPlan)},
		nil, []string{"auth-flow.md", "a-path.md"}, 0, "Topic file auth-flow.md.", ""},
}

// TestPlanSupportPreplanWriteReadyList runs each ready-list case through the preplanReadyRows seam. The status write succeeds in
// each row.
func TestPlanSupportPreplanWriteReadyList(t *testing.T) {
	for _, tc := range preplanWriteReadyCases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := writePreplanTopic(t, root, "auth-flow.md", preplanWriteSkeleton)
			swapPreplanSeam(t, &preplanReadyRows, func(got string) ([]DashboardPreplan, []dashboardPreplanWarning) {
				if got != root {
					t.Errorf("preplanReadyRows(%q), want the main root %q", got, root)
				}
				return tc.rows, tc.warnings
			})
			out, err := planSupportCore(root, root, preplanWriteIn("", "", preplanStatusReadyForPlan))
			if err != nil {
				t.Fatalf("preplan_write: %v", err)
			}
			var want []string
			for _, name := range tc.files {
				want = append(want, filepath.Join(filepath.Dir(file), name))
			}
			if out.ReadyTopicFiles == nil || !reflect.DeepEqual(*out.ReadyTopicFiles, want) {
				t.Errorf("ReadyTopicFiles = %v, want %v", out.ReadyTopicFiles, want)
			}
			if out.ReadyLeftOut == nil || *out.ReadyLeftOut != tc.leftOut {
				t.Errorf("ReadyLeftOut = %v, want %d", out.ReadyLeftOut, tc.leftOut)
			}
			if !strings.Contains(out.Summary, tc.summaryHas) {
				t.Errorf("Summary = %q, want it to hold %q", out.Summary, tc.summaryHas)
			}
			if tc.summaryLacks != "" && strings.Contains(out.Summary, tc.summaryLacks) {
				t.Errorf("Summary = %q, want it without %q", out.Summary, tc.summaryLacks)
			}
			if out.Next != preplanWriteNextReady {
				t.Errorf("Next = %q, want %q", out.Next, preplanWriteNextReady)
			}
			wantPreplanBytes(t, file, strings.Replace(preplanWriteSkeleton, "in progress", preplanStatusReadyForPlan, 1))
		})
	}
}

// TestPlanSupportPreplanWriteFolderWarningPrefix pins
// preplanFolderNotReadPrefix to the warning that dashboardPreplans gives for
// a preplan folder that cannot be listed.
func TestPlanSupportPreplanWriteFolderWarningPrefix(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".sdlc-v2", "preplan"), "not a folder\n")
	list, warnings := dashboardPreplans(root)
	if len(list) != 0 || len(warnings) != 1 || !strings.HasPrefix(warnings[0], preplanFolderNotReadPrefix) {
		t.Fatalf("dashboardPreplans = %v, %q, want no row and one warning that starts with %q", list, warnings, preplanFolderNotReadPrefix)
	}
	files, leftOut, notes := preplanReadyList(root, filepath.Join(root, ".sdlc-v2", "preplan", "auth-flow.md"))
	if len(files) != 1 || leftOut != 0 || len(notes) != 1 || !strings.HasPrefix(notes[0], "Ready topic files not listed: ") {
		t.Errorf("preplanReadyList = %v, %d, %q, want this file only, 0 and the not-listed note", files, leftOut, notes)
	}
}

// TestPlanSupportPreplanWriteLinkedWorktree checks that a call from a linked
// worktree writes the topic file of the main worktree and lists the preplan
// folder of the main worktree (the real dashboardPreplans, no seam).
func TestPlanSupportPreplanWriteLinkedWorktree(t *testing.T) {
	mainRoot, linked := t.TempDir(), t.TempDir()
	file := writePreplanTopic(t, mainRoot, "auth-flow.md", preplanWriteSkeleton)
	other := writePreplanTopic(t, mainRoot, "2026-10-01-other.md", "# Preplan: other\n\n**Status:** ready for plan\n")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(other, old, old); err != nil {
		t.Fatal(err)
	}
	writePreplanTopic(t, linked, "2026-10-01-other.md", "# Preplan: other\n\n**Status:** ready for plan\n")

	out, err := planSupportCore(mainRoot, linked, preplanWriteIn("", "", preplanStatusReadyForPlan))
	if err != nil {
		t.Fatalf("preplan_write: %v", err)
	}
	if out.PreplanFile != file {
		t.Errorf("PreplanFile = %q, want the main-worktree file %q", out.PreplanFile, file)
	}
	if want := []string{file, other}; out.ReadyTopicFiles == nil || !reflect.DeepEqual(*out.ReadyTopicFiles, want) {
		t.Errorf("ReadyTopicFiles = %v, want %v", out.ReadyTopicFiles, want)
	}
	wantPreplanBytes(t, file, strings.Replace(preplanWriteSkeleton, "in progress", preplanStatusReadyForPlan, 1))
	if _, err := os.Stat(filepath.Join(linked, ".sdlc-v2", "preplan", "auth-flow.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the linked worktree holds a topic file (stat err %v), want none", err)
	}
}

// TestPlanSupportPreplanWriteSizeLimit checks the size boundary of the new
// file: exactly dashboardDeleteReadMax bytes is written, one byte more is a
// DataError that states both numbers and writes nothing.
func TestPlanSupportPreplanWriteSizeLimit(t *testing.T) {
	const prefix = "**Status:** in progress\n\n## Notes\n\n"
	const goal = "## Goal\n"
	// The write of content c gives before + "\n" + c + "\n".
	filler := preplanFiller(dashboardDeleteReadMax - len(prefix) - len(goal) - len("\ngoal\n"))
	before := prefix + filler + goal

	root := t.TempDir()
	file := writePreplanTopic(t, root, "auth-flow.md", before)
	if _, err := planSupportCore(root, root, preplanWriteIn("Goal", "goal", "")); err != nil {
		t.Fatalf("a file of exactly %d bytes: %v, want success", dashboardDeleteReadMax, err)
	}
	if info, err := os.Stat(file); err != nil || info.Size() != dashboardDeleteReadMax {
		t.Fatalf("file size = %v (err %v), want %d", info, err, dashboardDeleteReadMax)
	}

	writeFile(t, file, before)
	noPreplanCheckWrite(t)
	_, err := planSupportCore(root, root, preplanWriteIn("Goal", "goal!", ""))
	wantPreplanErr(t, err, preplanErrWant{"data",
		fmt.Sprintf("preplan_write: %s would be %d bytes, more than %d", file, dashboardDeleteReadMax+1, dashboardDeleteReadMax),
		"Shorten the content, then call preplan_write again."})
	wantPreplanBytes(t, file, before)
}

// preplanWriteComboCase is one input combination of preplan_write: the call, and the written list or the error message.
type preplanWriteComboCase struct {
	name    string
	in      PlanSupportIn
	written []string
	errMsg  string
}

// preplanWriteComboCases are the input combinations of preplan_write.
var preplanWriteComboCases = []preplanWriteComboCase{
	{"section set, topicStatus empty: writes the section", preplanWriteIn("Goal", "Ship it.", ""), []string{"Goal"}, ""},
	{"section empty, content empty, topicStatus set: writes the Status line", preplanWriteIn("", "", preplanStatusPaused), []string{"Status"}, ""},
	{"section set, topicStatus set: writes both in one file write", preplanWriteIn("Goal", "Ship it.", preplanStatusPaused), []string{"Goal", "Status"}, ""},
	{"section empty, content set: content needs section", preplanWriteIn("", "Ship it.", preplanStatusPaused), nil, "preplan_write: content needs section"},
	{"section, content and topicStatus empty: nothing to write", preplanWriteIn("", "", ""), nil, "preplan_write: nothing to write"},
}

// TestPlanSupportPreplanWriteInputCombinations runs each case of
// preplanWriteComboCases. A success is one file write, and an error writes
// nothing.
func TestPlanSupportPreplanWriteInputCombinations(t *testing.T) {
	for _, tc := range preplanWriteComboCases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := writePreplanTopic(t, root, "auth-flow.md", preplanWriteSkeleton)
			writes := countPreplanWrites(t)
			out, err := planSupportCore(root, root, tc.in)
			if tc.errMsg != "" {
				if errorClassOf(err) != "domain" || err.Error() != tc.errMsg {
					t.Errorf("err = %v (%s), want DomainError %q", err, errorClassOf(err), tc.errMsg)
				}
				if *writes != 0 {
					t.Errorf("writes = %d, want 0", *writes)
				}
				wantPreplanBytes(t, file, preplanWriteSkeleton)
				return
			}
			if err != nil {
				t.Fatalf("preplan_write: %v", err)
			}
			if out.Written == nil || !reflect.DeepEqual(*out.Written, tc.written) || *writes != 1 {
				t.Errorf("Written = %v, writes = %d, want %q and 1", out.Written, *writes, tc.written)
			}
		})
	}
}

// TestPlanSupportPreplanWriteFieldsOnOtherActions checks that section, content or topicStatus sent with another
// action is a DomainError that names the field, and nothing runs.
func TestPlanSupportPreplanWriteFieldsOnOtherActions(t *testing.T) {
	for _, field := range []string{"section", "content", "topicStatus"} {
		for _, action := range []string{preplanContextAction, preplanCheckAction, "merge_results"} {
			t.Run(action+" with "+field, func(t *testing.T) {
				root := t.TempDir()
				in := preplanCheckIn("auth flow", 1, 0, 0)
				in.Action = action
				switch field {
				case "section":
					in.Section = "Goal"
				case "content":
					in.Content = preplanContentPtr("Ship it.")
				default:
					in.TopicStatus = preplanStatusPaused
				}
				_, err := planSupportCore(root, root, in)
				wantPreplanErr(t, err, preplanErrWant{"domain", action + ": " + field + " is a preplan_write field",
					"Remove " + field + ", or use action preplan_write."})
				if names := preplanDirNames(t, filepath.Join(root, ".sdlc-v2", "preplan")); len(names) != 0 {
					t.Errorf("preplan folder holds %v, want nothing: the action must not run", names)
				}
			})
		}
	}
	if err := preplanWriteFieldError(PlanSupportIn{Action: preplanCheckAction}); err != nil {
		t.Errorf("preplanWriteFieldError with no write field = %v, want nil", err)
	}
}

// preplanWriteNotFoundSuggestion is the Suggestion of a section that is not
// found.
const preplanWriteNotFoundSuggestion = "Call plan_support preplan_context with the same topic. It adds each absent section heading, and closes a fence that is still open at the end of the file. Then call preplan_write again with the same section and content."

// preplanWriteErrorCase is one error of preplan_write: the file
// before, the call, and the error. {file} in msg stands for the topic file.
type preplanWriteErrorCase struct {
	name, before string
	in           PlanSupportIn
	want         preplanErrWant
}

// preplanWriteErrorCases are the errors of preplan_write that a call can
// reach with a topic file in place. The size row and the other-action row
// have their own tests.
var preplanWriteErrorCases = []preplanWriteErrorCase{
	{"rule P4 broken", preplanWriteSkeleton, preplanWriteIn("Decisions", "| 1 | a | b |", ""), preplanErrWant{"domain",
		`preplan_write: Section "Decisions" breaks rule P4: the table header is missing: a body that is not blank starts with "| # | Decision | Reason |" and "|---|---|---|"`,
		`Start the body with the two lines "| # | Decision | Reason |" and "|---|---|---|", or send an empty string, then call preplan_write again.`}},
	{"rule P2 broken", preplanWriteSkeleton, preplanWriteIn("Goal", "## Sub", preplanStatusPaused), preplanErrWant{"domain",
		`preplan_write: Section "Goal" breaks rule P2: line 1 starts a heading`, `Remove each line that starts with "# " or "## " outside a code fence: a section body holds no heading, then call preplan_write again.`}},
	{"rule P11 broken", preplanWriteSkeleton, preplanWriteIn("Guardrail check", "3 pass", ""), preplanErrWant{"domain",
		`preplan_write: Section "Guardrail check" breaks rule P11: only an empty body deletes this section`, "Call preplan_write again with section Guardrail check and content as an empty string: it deletes the section. No other body is valid."}},
	{"content with no section", preplanWriteSkeleton, preplanWriteIn("", "Ship it.", ""), preplanErrWant{"domain",
		"preplan_write: content needs section", "Add section with the name of the section to write, or remove content."}},
	{"no section and no topicStatus", preplanWriteSkeleton, preplanWriteIn("", "", ""), preplanErrWant{"domain",
		"preplan_write: nothing to write", "Add section and content, or add topicStatus."}},
	{"unknown section", preplanWriteSkeleton, preplanWriteIn("Notes", "x", ""), preplanErrWant{"domain",
		`preplan_write: section "Notes" is not a topic-file section`, "Use one of: Goal, Problem and benefit, Users and effect, Risk if wrong, Non-goals, Flows, Decisions, Open questions, Guardrail check."}},
	{"section with no content", preplanWriteSkeleton, PlanSupportIn{Action: preplanWriteAction, Topic: "auth flow", Section: "Decisions"}, preplanErrWant{"domain",
		"preplan_write: section needs content",
		"Add content with the full new body of section. Send content as an empty string only to clear the body, or to delete the Open questions or Guardrail check section."}},
	{"an empty content with no section", preplanWriteSkeleton, PlanSupportIn{Action: preplanWriteAction, Topic: "auth flow", Content: preplanContentPtr(""), TopicStatus: preplanStatusPaused}, preplanErrWant{"domain",
		"preplan_write: content needs section", "Add section with the name of the section to write, or remove content."}},
	{"topicStatus not in the 3 values", preplanWriteSkeleton, preplanWriteIn("", "", "done"), preplanErrWant{"domain",
		`preplan_write: topicStatus "done" is not a preplan status`, "Use in progress, ready for plan or paused."}},
	{"a bad topic", preplanWriteSkeleton, PlanSupportIn{Action: preplanWriteAction, Topic: "!!!", TopicStatus: preplanStatusPaused}, preplanErrWant{"domain",
		`preplan_write: topic "!!!" has no ASCII letter or digit`, preplanTopicASCIISuggestion}},
	{"skeleton heading absent", preplanParityNoHeadings, preplanWriteIn("Goal", "Ship it.", preplanStatusPaused), preplanErrWant{"data",
		`preplan_write: section "Goal" not found in {file}`, preplanWriteNotFoundSuggestion}},
	{"skeleton heading hidden by an open fence", preplanParityFenceFile, preplanWriteIn("Decisions", "", ""), preplanErrWant{"data",
		`preplan_write: section "Decisions" not found in {file}`, preplanWriteNotFoundSuggestion}},
	{"Open questions with no Decisions section", preplanParityNoHeadings, preplanWriteIn("Open questions", "- q", ""), preplanErrWant{"data",
		`preplan_write: section "Decisions" not found in {file}`, preplanWriteNotFoundSuggestion}},
	{"Open questions after a last Decisions section that holds an open fence", preplanWriteSkeleton + "```\nopen\n", preplanWriteIn("Open questions", "- q", ""), preplanErrWant{"data",
		`preplan_write: section "Open questions" not found in {file}`,
		"Send the full body of ## Decisions with preplan_write, with the fence closed. The write replaces the text up to the end of the file. Then call preplan_write again with section Open questions and the same content."}},
}

// TestPlanSupportPreplanWriteErrors runs each error of preplan_write. A
// refused write leaves the file bytes unchanged and calls no write.
func TestPlanSupportPreplanWriteErrors(t *testing.T) {
	for _, tc := range preplanWriteErrorCases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := writePreplanTopic(t, root, "auth-flow.md", tc.before)
			noPreplanCheckWrite(t)
			_, err := planSupportCore(root, root, tc.in)
			want := tc.want
			want.msg = preplanParityExpand(want.msg, root, file)
			wantPreplanErr(t, err, want)
			wantPreplanBytes(t, file, tc.before)
		})
	}
}

// preplanWriteNextCase is one call of TestPlanSupportPreplanWriteNext and its
// next text.
type preplanWriteNextCase struct {
	name string
	in   PlanSupportIn
	next string
}

// preplanWriteNextCases are the precedence cases of the next text, and the
// paused row.
var preplanWriteNextCases = []preplanWriteNextCase{
	{"Decisions with ready for plan", preplanWriteIn("Decisions", preplanWriteRowsA, preplanStatusReadyForPlan), preplanWriteNextReady},
	{"Goal with paused", preplanWriteIn("Goal", "Ship it.", preplanStatusPaused), preplanWriteNextPaused},
	{"paused only", preplanWriteIn("", "", preplanStatusPaused), preplanWriteNextPaused},
	{"empty content on an absent Open questions, with in progress", preplanWriteIn("Open questions", "", preplanStatusInProgress), preplanWriteNextWritten},
}

// TestPlanSupportPreplanWriteNext checks each outcome of the next text and
// its precedence: through a call, and on preplanWriteNext for each arm.
func TestPlanSupportPreplanWriteNext(t *testing.T) {
	for _, tc := range preplanWriteNextCases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writePreplanTopic(t, root, "auth-flow.md", preplanWriteSkeleton)
			out, err := planSupportCore(root, root, tc.in)
			if err != nil {
				t.Fatalf("preplan_write: %v", err)
			}
			if out.Next != tc.next {
				t.Errorf("Next = %q, want %q", out.Next, tc.next)
			}
			if tc.in.Section == "Open questions" && !reflect.DeepEqual(*out.Written, []string{"Status"}) {
				t.Errorf("Written = %v, want [Status] only", *out.Written)
			}
		})
	}
	for _, tc := range []struct {
		status  string
		written []string
		want    string
	}{
		{preplanStatusReadyForPlan, []string{}, preplanWriteNextReady},
		{preplanStatusPaused, []string{"Goal"}, preplanWriteNextPaused},
		{preplanStatusInProgress, []string{"Status"}, preplanWriteNextWritten},
		{"", []string{"Goal"}, preplanWriteNextWritten},
		{"", []string{}, preplanWriteNextNoop},
	} {
		if got := preplanWriteNext(tc.status, tc.written); got != tc.want {
			t.Errorf("preplanWriteNext(%q, %q) = %q, want %q", tc.status, tc.written, got, tc.want)
		}
	}
}

// TestPlanSupportPreplanWriteOverMCP pins how the MCP renderer shows the
// preplan_write fields: "(none)" for an empty written list, one bullet for
// each written part, and readyLeftOut 0 on a ready call.
func TestPlanSupportPreplanWriteOverMCP(t *testing.T) {
	root := newOpenspecStageFixture(t, "")
	writeFile(t, filepath.Join(root, ".sdlc-v2", "preplan", "auth-flow.md"), preplanWriteSkeleton)

	noop := map[string]any{"action": "preplan_write", "topic": "auth flow", "section": "Open questions", "content": ""}
	if text := evidenceRender(t, root, noop); !strings.Contains(text, "- written: (none)\n") || !strings.Contains(text, preplanWriteNextNoop) {
		t.Errorf("a no-op must render written: (none) and the no-op next:\n%s", text)
	}
	write := map[string]any{"action": "preplan_write", "topic": "auth flow", "section": "Decisions", "content": preplanWriteRowsAB}
	if text := evidenceRender(t, root, write); !strings.Contains(text, "- written:\n  - Decisions\n") || !strings.Contains(text, preplanWriteNextWritten) {
		t.Errorf("a section write must list Decisions under written:\n%s", text)
	}
	ready := map[string]any{"action": "preplan_write", "topic": "auth flow", "topicStatus": "ready for plan"}
	if text := evidenceRender(t, root, ready); !strings.Contains(text, "- readyLeftOut: 0\n") || !strings.Contains(text, "- readyTopicFiles:\n  - ") {
		t.Errorf("a ready call must render readyTopicFiles and readyLeftOut: 0:\n%s", text)
	}
}

// TestPreplanWriteFieldDescriptions checks that the section and topicStatus
// descriptions name each value of preplanWriteSections and PreplanStatuses,
// that the section and topicStatus enum tags equal those lists, and that the content
// description states preplanWriteContentMax.
func TestPreplanWriteFieldDescriptions(t *testing.T) {
	typ := reflect.TypeOf(PlanSupportIn{})
	field := func(name string) reflect.StructField {
		f, ok := typ.FieldByName(name)
		if !ok {
			t.Fatalf("PlanSupportIn has no field %s", name)
		}
		return f
	}
	section := field("Section").Tag.Get("jsonschema_description")
	if want := "One of: " + strings.Join(preplanWriteSections, ", ") + "."; !strings.Contains(section, want) {
		t.Errorf("section description %q lacks %q", section, want)
	}
	status := field("TopicStatus")
	desc := status.Tag.Get("jsonschema_description")
	for _, s := range PreplanStatuses {
		if !strings.Contains(desc, s) {
			t.Errorf("topicStatus description %q does not name %q", desc, s)
		}
	}
	if got, want := status.Tag.Get("jsonschema"), "enum="+strings.Join(PreplanStatuses, ",enum="); got != want {
		t.Errorf("topicStatus enum tag = %q, want %q", got, want)
	}
	if got, want := field("Section").Tag.Get("jsonschema"), "enum="+strings.Join(preplanWriteSections, ",enum="); got != want {
		t.Errorf("section enum tag = %q, want %q", got, want)
	}
	if content := field("Content").Tag.Get("jsonschema_description"); !strings.Contains(content, "at most "+strconv.Itoa(preplanWriteContentMax)+" bytes") {
		t.Errorf("content description %q does not state %d bytes", content, preplanWriteContentMax)
	}
	if action := field("Action").Tag.Get("jsonschema"); !strings.HasSuffix(action, ",enum="+preplanWriteAction) {
		t.Errorf("action enum tag %q lacks %s", action, preplanWriteAction)
	}
}

// preplanStatusParityResult is the class and a text of one result of
// TestPlanSupportPreplanWriteStatusParityWithDashboard. class "" is success,
// and has is then a text of the dashboard message or the preplan_write
// summary.
type preplanStatusParityResult struct{ class, has string }

// preplanStatusParityCase is one file state of the parity test with the
// dashboard status writer: the state that setup makes at the topic file, the status of
// both calls, and the result of each writer.
type preplanStatusParityCase struct {
	name      string
	setup     func(t *testing.T, file string)
	status    string
	dash, wr  preplanStatusParityResult
	wantAfter string // the file after a preplan_write that succeeds
}

// preplanStatusParityWrite returns a setup that writes content to the topic
// file.
func preplanStatusParityWrite(content string) func(t *testing.T, file string) {
	return func(t *testing.T, file string) { writeFile(t, file, content) }
}

// preplanStatusParityCases are the file states of the parity test with the
// dashboard status writer.
var preplanStatusParityCases = []preplanStatusParityCase{
	{"status not in PreplanStatuses", preplanStatusParityWrite(preplanWriteSkeleton), "done",
		preplanStatusParityResult{"domain", "is not a preplan status"}, preplanStatusParityResult{"domain", `topicStatus "done" is not a preplan status`}, ""},
	{"a folder", func(t *testing.T, file string) {
		if err := os.MkdirAll(file, 0o755); err != nil {
			t.Fatal(err)
		}
	}, preplanStatusPaused,
		preplanStatusParityResult{"domain", "not a topic file that the dashboard can rewrite"}, preplanStatusParityResult{"domain", "is a folder, not a regular file"}, ""},
	{"a link", func(t *testing.T, file string) {
		target := filepath.Join(filepath.Dir(file), "target.txt")
		writeFile(t, target, preplanWriteSkeleton)
		if err := os.Symlink(target, file); err != nil {
			t.Fatal(err)
		}
	}, preplanStatusPaused,
		preplanStatusParityResult{"domain", "not a topic file that the dashboard can rewrite"}, preplanStatusParityResult{"domain", "is a symlink, not a regular file"}, ""},
	{"file gone", func(t *testing.T, file string) {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
	}, preplanStatusPaused,
		preplanStatusParityResult{"domain", "is gone"}, preplanStatusParityResult{"domain", "no topic file"}, ""},
	{"file over 8 MiB", preplanStatusParityWrite(preplanWriteSkeleton + preplanFiller(dashboardDeleteReadMax)), preplanStatusPaused,
		preplanStatusParityResult{"data", "more than the 8388608 bytes (8 MiB) that a dashboard delete reads"},
		preplanStatusParityResult{"data", "more than the 8388608 bytes (8 MiB) that preplan_write reads"}, ""},
	{"Status line in the first 4096 bytes", preplanStatusParityWrite(preplanWriteSkeleton), preplanStatusPaused,
		preplanStatusParityResult{"", "Set the status"}, preplanStatusParityResult{"", "Old status: in progress."},
		strings.Replace(preplanWriteSkeleton, "in progress", preplanStatusPaused, 1)},
	{"no Status line in the first 4096 bytes", preplanStatusParityWrite("# Preplan: auth flow\n\n" + preplanWriteStartedLine), preplanStatusPaused,
		preplanStatusParityResult{"data", "has no **Status:** line in its first 4 KiB"}, preplanStatusParityResult{"", "before the Started line"},
		"# Preplan: auth flow\n\n**Status:** paused\n\n" + preplanWriteStartedLine},
	{"same status as now", preplanStatusParityWrite(preplanWriteSkeleton), preplanStatusInProgress,
		preplanStatusParityResult{"", "already has the status"}, preplanStatusParityResult{"", "Old status: in progress."}, preplanWriteSkeleton},
}

// TestPlanSupportPreplanWriteStatusParityWithDashboard runs each file state
// through DashboardSetPreplanStatus, the other writer of the Status
// line: both writers get the same state in two roots. When both succeed, the
// two files hold the same bytes.
func TestPlanSupportPreplanWriteStatusParityWithDashboard(t *testing.T) {
	for _, tc := range preplanStatusParityCases {
		t.Run(tc.name, func(t *testing.T) {
			dashRoot, wrRoot := t.TempDir(), t.TempDir()
			dashFile := filepath.Join(dashRoot, ".sdlc-v2", "preplan", "auth-flow.md")
			wrFile := filepath.Join(wrRoot, ".sdlc-v2", "preplan", "auth-flow.md")
			tc.setup(t, dashFile)
			tc.setup(t, wrFile)

			dashOut, dashErr := DashboardSetPreplanStatus(dashRoot, "auth-flow", tc.status)
			wantPreplanStatusParity(t, "dashboard", dashErr, dashOut.Message, tc.dash)
			wrOut, wrErr := planSupportCore(wrRoot, wrRoot, preplanWriteIn("", "", tc.status))
			wantPreplanStatusParity(t, "preplan_write", wrErr, wrOut.Summary, tc.wr)
			if wrErr != nil {
				return
			}
			wantPreplanBytes(t, wrFile, tc.wantAfter)
			if !reflect.DeepEqual(*wrOut.Written, []string{"Status"}) {
				t.Errorf("Written = %v, want [Status]", *wrOut.Written)
			}
			if dashErr == nil {
				dashBytes, err := os.ReadFile(dashFile)
				if err != nil {
					t.Fatalf("read the dashboard file: %v", err)
				}
				wantPreplanBytes(t, wrFile, string(dashBytes))
			}
		})
	}
}

// wantPreplanStatusParity checks one result of the parity test with the
// dashboard: the class of err, and that the error text, or text on a
// success, holds want.has.
func wantPreplanStatusParity(t *testing.T, writer string, err error, text string, want preplanStatusParityResult) {
	t.Helper()
	if got := errorClassOf(err); got != want.class || (want.class == "" && err != nil) {
		t.Errorf("%s: class = %q (err %v), want %q", writer, got, err, want.class)
		return
	}
	if err != nil {
		text = err.Error()
	}
	if !strings.Contains(text, want.has) {
		t.Errorf("%s: %q does not hold %q", writer, text, want.has)
	}
}

// TestPlanSupportPreplanWriteContentMax checks the P1 limit on the content
// that preplan_write stores (preplanWriteContent): preplanWriteContentMax
// bytes plus final line ends, which are not counted, are written. One byte
// more after the final line ends go is a DomainError that states both
// numbers, wraps the *preplanFormatError of P1, and writes nothing.
func TestPlanSupportPreplanWriteContentMax(t *testing.T) {
	root := t.TempDir()
	file := writePreplanTopic(t, root, "auth-flow.md", preplanWriteSkeleton)
	atMax := strings.Repeat("x", preplanWriteContentMax)
	out, err := planSupportCore(root, root, preplanWriteIn("Goal", atMax+"\r\n\n", ""))
	if err != nil {
		t.Fatalf("content of %d bytes: %v, want success", preplanWriteContentMax, err)
	}
	if !reflect.DeepEqual(*out.Written, []string{"Goal"}) {
		t.Errorf("Written = %v, want [Goal]", *out.Written)
	}
	wantPreplanBytes(t, file, strings.Replace(preplanWriteSkeleton, "## Goal\n\n", "## Goal\n\n"+atMax+"\n\n", 1))

	writeFile(t, file, preplanWriteSkeleton)
	noPreplanCheckWrite(t)
	_, err = planSupportCore(root, root, preplanWriteIn("Goal", atMax+"x\r\n", ""))
	wantPreplanErr(t, err, preplanErrWant{"domain",
		fmt.Sprintf(`preplan_write: Section "Goal" breaks rule P1: the body is %d bytes, the limit is %d bytes`, preplanWriteContentMax+1, preplanWriteContentMax),
		"Shorten the body to at most 65536 bytes, then call preplan_write again."})
	var ferr *preplanFormatError
	if !errors.As(err, &ferr) || ferr.Rule != "P1" || ferr.Section != "Goal" {
		t.Errorf("errors.As(*preplanFormatError) = %v (%+v), want the P1 error of Goal as the Cause", errors.As(err, &ferr), ferr)
	}
	wantPreplanBytes(t, file, preplanWriteSkeleton)
}

// preplanSiblingCase is one write kind of
// TestPlanSupportPreplanWriteSiblingReadsOutput: the file before, the call,
// and the status that the file holds after it.
type preplanSiblingCase struct {
	name, before string
	in           PlanSupportIn
	status       string
}

// preplanSiblingCases are the write kinds whose output the sibling readers
// must accept.
var preplanSiblingCases = []preplanSiblingCase{
	{"section write", preplanWriteSkeleton, preplanWriteIn("Decisions", preplanWriteRowsAB+"\n\nSmallest cut: all.", ""), preplanStatusInProgress},
	{"Open questions add", preplanWriteSkeleton, preplanWriteIn("Open questions", "- Which tab?", ""), preplanStatusInProgress},
	{"Open questions delete", preplanWriteSkeleton + "\n## Open questions\n\n- Which tab?\n", preplanWriteIn("Open questions", "", ""), preplanStatusInProgress},
	{"Status insert", strings.Replace(preplanWriteSkeleton, "**Status:** in progress\n\n", "", 1), preplanWriteIn("", "", preplanStatusPaused), preplanStatusPaused},
}

// TestPlanSupportPreplanWriteSiblingReadsOutput runs dashboardPreplans and
// preplan_check on the file that each write kind leaves. Both accept it: no
// error, no warning, and the topic and the status read back.
func TestPlanSupportPreplanWriteSiblingReadsOutput(t *testing.T) {
	setPreplanNow(t, preplanCheckTestNow)
	for _, tc := range preplanSiblingCases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writePreplanTopic(t, root, "auth-flow.md", tc.before)
			if _, err := planSupportCore(root, root, tc.in); err != nil {
				t.Fatalf("preplan_write: %v", err)
			}
			list, warnings := dashboardPreplans(root)
			if len(warnings) != 0 || len(list) != 1 || list[0].Topic != "auth flow" || list[0].Status != tc.status {
				t.Errorf("dashboardPreplans = %+v, %q, want one row with topic auth flow and status %q, no warning", list, warnings, tc.status)
			}
			out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 1, 0, 0))
			if err != nil {
				t.Fatalf("preplan_check on the written file: %v", err)
			}
			if strings.Contains(out.Summary, "fence is still open") {
				t.Errorf("preplan_check summary %q names an open fence, want none", out.Summary)
			}
		})
	}
}

// preplanLineEndCase is one case of TestPlanSupportPreplanWriteContentLineEnds:
// the file before, the Goal content as sent, and the file after.
type preplanLineEndCase struct{ name, before, content, after string }

// preplanLineEndCases are the line-end forms of content and of the file.
var preplanLineEndCases = []preplanLineEndCase{
	{"LF content into a CR LF file is stored with CR LF", strings.ReplaceAll(preplanWriteSkeleton, "\n", "\r\n"), "one\ntwo\n",
		strings.Replace(strings.ReplaceAll(preplanWriteSkeleton, "\n", "\r\n"), "## Goal\r\n\r\n", "## Goal\r\n\r\none\r\ntwo\r\n\r\n", 1)},
	{"CR LF content into an LF file is stored with LF", preplanWriteSkeleton, "one\r\ntwo\r\n\r\n",
		strings.Replace(preplanWriteSkeleton, "## Goal\n\n", "## Goal\n\none\ntwo\n\n", 1)},
	{"content of line ends only clears the body", strings.Replace(preplanWriteSkeleton, "## Goal\n\n", "## Goal\n\nold\n\n", 1), "\r\n\n",
		preplanWriteSkeleton},
}

// TestPlanSupportPreplanWriteContentLineEnds checks that the stored body
// takes the line ends of the file whatever the line ends of content, and
// that every byte outside the body stays the same.
func TestPlanSupportPreplanWriteContentLineEnds(t *testing.T) {
	for _, tc := range preplanLineEndCases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := writePreplanTopic(t, root, "auth-flow.md", tc.before)
			if _, err := planSupportCore(root, root, preplanWriteIn("Goal", tc.content, "")); err != nil {
				t.Fatalf("preplan_write: %v", err)
			}
			wantPreplanBytes(t, file, tc.after)
		})
	}
	if got := preplanWriteContent("a\r\nb\r\n\n"); got != "a\nb" {
		t.Errorf("preplanWriteContent = %q, want %q", got, "a\nb")
	}
}

// TestPlanSupportActionList pins the action enum tag and the action
// description of PlanSupportIn to planSupportActions, and checks that the
// unknown-action error names each action of the list.
func TestPlanSupportActionList(t *testing.T) {
	f, ok := reflect.TypeOf(PlanSupportIn{}).FieldByName("Action")
	if !ok {
		t.Fatal("PlanSupportIn has no field Action")
	}
	if got, want := f.Tag.Get("jsonschema"), "enum="+strings.Join(planSupportActions, ",enum="); got != want {
		t.Errorf("action enum tag = %q, want %q", got, want)
	}
	desc := f.Tag.Get("jsonschema_description")
	_, err := planSupportCore(t.TempDir(), t.TempDir(), PlanSupportIn{Action: "no_such_action"})
	if err == nil {
		t.Fatal("unknown action: want an error")
	}
	for _, a := range planSupportActions {
		if !strings.Contains(desc, strconv.Quote(a)) {
			t.Errorf("action description does not name %q", a)
		}
		if !strings.Contains(err.Error(), a) || !strings.Contains(suggestionOf(err), a) {
			t.Errorf("unknown-action error does not name %q: %v / %s", a, err, suggestionOf(err))
		}
	}
}

// TestPreplanWriteContractDescriptions checks the descriptions that state the
// preplan_write contract: the action exception for the three preplan_write
// fields, content required with section, the actions of preplanFile, and the
// numbers and the folder of readyTopicFiles.
func TestPreplanWriteContractDescriptions(t *testing.T) {
	tag := func(typ reflect.Type, name string) string {
		f, ok := typ.FieldByName(name)
		if !ok {
			t.Fatalf("%s has no field %s", typ.Name(), name)
		}
		return f.Tag.Get("jsonschema_description")
	}
	in, out := reflect.TypeOf(PlanSupportIn{}), reflect.TypeOf(PlanSupportOut{})
	if got, want := tag(in, "Action"), "except section, content and topicStatus: an action other than preplan_write that gets one of them returns DomainError"; !strings.Contains(got, want) {
		t.Errorf("action description %q lacks %q", got, want)
	}
	if got := tag(in, "Content"); !strings.Contains(got, "Required with section") || strings.Contains(got, "Example: | # |") {
		t.Errorf("content description %q must say Required with section and must not give a table header as the example", got)
	}
	file := tag(out, "PreplanFile")
	for _, a := range []string{preplanContextAction, preplanCheckAction, preplanWriteAction} {
		if !strings.Contains(file, a) {
			t.Errorf("preplanFile description %q does not name %s", file, a)
		}
	}
	ready := tag(out, "ReadyTopicFiles")
	if want := "up to " + strconv.Itoa(preplanReadyMax-1) + " other ready topic files"; !strings.Contains(ready, want) {
		t.Errorf("readyTopicFiles description %q lacks %q (preplanReadyMax %d)", ready, want, preplanReadyMax)
	}
	if want := "/" + paths.DataDir + "/" + paths.PreplanSubdir + "/"; !strings.Contains(ready, want) {
		t.Errorf("readyTopicFiles description %q does not show the folder %q", ready, want)
	}
}

// TestPreplanNoTopicFileDanglingFolderCause checks that a dangling link at the
// preplan folder gives preplan_check and preplan_write an InfraError that
// wraps the *fsx.DanglingLinkError, so fsx.RecoveryOr gives its recovery.
func TestPreplanNoTopicFileDanglingFolderCause(t *testing.T) {
	for _, in := range []PlanSupportIn{preplanCheckIn("auth flow", 1, 0, 0), preplanWriteIn("", "", preplanStatusPaused)} {
		t.Run(in.Action, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, paths.DataDir, paths.PreplanSubdir)
			if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, "missing"), dir); err != nil {
				t.Fatal(err)
			}
			_, err := planSupportCore(root, root, in)
			if got := errorClassOf(err); got != "infra" {
				t.Fatalf("error class = %q, want infra (err %v)", got, err)
			}
			var dl *fsx.DanglingLinkError
			if !errors.As(err, &dl) {
				t.Fatalf("err = %v, want it to wrap a *fsx.DanglingLinkError", err)
			}
			if got := fsx.RecoveryOr(err, "fallback"); got != dl.Recovery() || got != suggestionOf(err) {
				t.Errorf("RecoveryOr = %q, want the link recovery %q, which is also the Suggestion %q", got, dl.Recovery(), suggestionOf(err))
			}
		})
	}
}

// TestPreplanWriteFailureSuggestions checks the Suggestion of a failed write
// of the topic file for each action that writes it: a file that went away
// gets the missing-file text, a permission error gets the write permission
// text, and the error stays an InfraError that wraps the cause.
func TestPreplanWriteFailureSuggestions(t *testing.T) {
	gone := fmt.Errorf("stat x: %w", fs.ErrNotExist)
	denied := fmt.Errorf("open x: %w", fs.ErrPermission)
	for _, tc := range []struct {
		in             PlanSupportIn
		content, again string
		goneText       string
	}{
		{PlanSupportIn{Action: preplanContextAction, Topic: "auth flow"}, preplanParityNoHeadings, ", then run /sdlc:preplan again.",
			"Run /sdlc:preplan again: the topic file went away after the folder listing, and the next run creates a new one."},
		{preplanCheckIn("auth flow", 1, 0, 0), preplanWriteSkeleton, ", then call preplan_check again.",
			"Run plan_support with action preplan_context and this topic first."},
		{preplanWriteIn("Goal", "Ship it.", ""), preplanWriteSkeleton, ", then call preplan_write again.",
			"Run plan_support with action preplan_context and this topic first."},
	} {
		for _, fail := range []struct {
			name string
			err  error
		}{{"gone", gone}, {"denied", denied}} {
			t.Run(tc.in.Action+" "+fail.name, func(t *testing.T) {
				root := t.TempDir()
				file := writePreplanTopic(t, root, "auth-flow.md", tc.content)
				swapPreplanSeam(t, &preplanWriteBytes, func(string, []byte) error { return fail.err })
				_, err := planSupportCore(root, root, tc.in)
				want := tc.goneText
				if fail.err == denied {
					want = "Give your user write permission on " + filepath.Dir(file) + tc.again
				}
				if got := errorClassOf(err); got != "infra" {
					t.Errorf("error class = %q, want infra (err %v)", got, err)
				}
				if got := suggestionOf(err); got != want {
					t.Errorf("Suggestion = %q, want %q", got, want)
				}
				if !errors.Is(err, fail.err) {
					t.Errorf("err = %v, want it to wrap %v", err, fail.err)
				}
				wantPreplanBytes(t, file, tc.content)
			})
		}
	}
}

// TestPreplanRuleSuggestions checks that each rule of the format checker has
// its own Suggestion with the form that the rule accepts, and that a body
// sent in that form passes the rule.
func TestPreplanRuleSuggestions(t *testing.T) {
	for _, tc := range []struct{ rule, has string }{
		{"P1", "at most " + strconv.Itoa(preplanWriteContentMax) + " bytes"},
		{"P2", `"## "`},
		{"P3", "Close each code fence"},
		{"P4", strconv.Quote(preplanDecisionsHeaderLines[1])},
		{"P5", "positive whole number"},
		{"P6", strconv.Quote(preplanDroppedByNote)},
		{"P7", `"Smallest cut: all."`},
		{"P8", strconv.Quote(preplanNoneStated)},
		{"P9", `"- "`},
		{"P10", preplanAllowedClassDefs[1]},
		{"P11", "content as an empty string"},
	} {
		got := preplanRuleSuggestion(tc.rule)
		if !strings.Contains(got, tc.has) {
			t.Errorf("%s Suggestion %q lacks %q", tc.rule, got, tc.has)
		}
		if tc.rule != "P11" && !strings.HasSuffix(got, preplanWriteAgain) {
			t.Errorf("%s Suggestion %q does not end with %q", tc.rule, got, preplanWriteAgain)
		}
	}
	// The forms that the Suggestions name pass their rules.
	for _, ok := range []struct{ section, body string }{
		{preplanNonGoalsSection, preplanNoneStated},
		{preplanFlowsSection, preplanNoFlowChange},
		{preplanDecisionsSection, preplanDecisionsHeader + "| 4 | A | B |\n\nSmallest cut: all."},
		{preplanGuardrailCheckSection, ""},
	} {
		if err := preplanCheckSection(ok.section, ok.body); err != nil {
			t.Errorf("preplanCheckSection(%q, %q) = %v, want nil", ok.section, ok.body, err)
		}
	}
	if got := preplanRuleSuggestion("P99"); got != "Fix the content"+preplanWriteAgain {
		t.Errorf("unknown rule Suggestion = %q", got)
	}
}

// TestPreplanWriteOldBodyNote checks the summary sentence of the size of the
// old body on a replace and a delete, with the singular form for one line.
func TestPreplanWriteOldBodyNote(t *testing.T) {
	root := t.TempDir()
	writePreplanTopic(t, root, "auth-flow.md", preplanWriteSkeleton)
	for _, step := range []struct {
		in   PlanSupportIn
		want string
	}{
		{preplanWriteIn("Goal", "Ship it.", ""), "Replaced the body of ## Goal. The old body had 0 lines of text."},
		{preplanWriteIn("Goal", "Ship it.\n\nThen more.", ""), "Replaced the body of ## Goal. The old body had 1 line of text."},
		{preplanWriteIn("Goal", "x", ""), "Replaced the body of ## Goal. The old body had 2 lines of text."},
		{preplanWriteIn("Decisions", preplanWriteRowsAB, ""), "Replaced the body of ## Decisions. The old body had 2 lines of text."},
		{preplanWriteIn("Open questions", "- q1\n- q2", ""), "Added the ## Open questions section after ## Decisions."},
		{preplanWriteIn("Open questions", "", ""), "Deleted the ## Open questions section. The old body had 2 lines of text."},
	} {
		out, err := planSupportCore(root, root, step.in)
		if err != nil {
			t.Fatalf("%s %s: %v", step.in.Section, *step.in.Content, err)
		}
		if !strings.Contains(out.Summary, step.want) {
			t.Errorf("Summary = %q, want it to hold %q", out.Summary, step.want)
		}
	}
}

// TestPreplanWriteNotFoundFollowOnce follows the Suggestion of each "section
// not found" error once and checks that the retry then succeeds.
func TestPreplanWriteNotFoundFollowOnce(t *testing.T) {
	t.Run("open fence in a last Decisions section", func(t *testing.T) {
		root := t.TempDir()
		file := writePreplanTopic(t, root, "auth-flow.md", preplanWriteSkeleton+"```\nopen\n")
		_, err := planSupportCore(root, root, preplanWriteIn("Open questions", "- q", ""))
		wantPreplanErr(t, err, preplanErrWant{"data", `preplan_write: section "Open questions" not found in ` + file, preplanFencedSuggestion})
		// preplan_context cannot fix this file: every skeleton heading is there.
		ctx, err := planSupportCore(root, root, PlanSupportIn{Action: preplanContextAction, Topic: "auth flow"})
		if err != nil || ctx.AddedSections == nil || len(*ctx.AddedSections) != 0 {
			t.Fatalf("preplan_context = %v, %v, want no added heading", ctx.AddedSections, err)
		}
		// The Suggestion: the full Decisions body, then the same Open questions call.
		if _, err := planSupportCore(root, root, preplanWriteIn("Decisions", preplanWriteRowsAB, "")); err != nil {
			t.Fatalf("Decisions write: %v", err)
		}
		if _, err := planSupportCore(root, root, preplanWriteIn("Open questions", "- q", "")); err != nil {
			t.Fatalf("Open questions retry: %v", err)
		}
		wantPreplanBytes(t, file, preplanWriteSkeletonHead+preplanWriteRowsAB+"\n\n## Open questions\n\n- q\n")
	})
	t.Run("absent skeleton heading", func(t *testing.T) {
		root := t.TempDir()
		file := writePreplanTopic(t, root, "auth-flow.md", preplanParityNoHeadings)
		_, err := planSupportCore(root, root, preplanWriteIn("Goal", "Ship it.", ""))
		wantPreplanErr(t, err, preplanErrWant{"data", `preplan_write: section "Goal" not found in ` + file, preplanNotFoundSuggestion})
		if _, err := planSupportCore(root, root, PlanSupportIn{Action: preplanContextAction, Topic: "auth flow"}); err != nil {
			t.Fatalf("preplan_context: %v", err)
		}
		if _, err := planSupportCore(root, root, preplanWriteIn("Goal", "Ship it.", "")); err != nil {
			t.Fatalf("Goal retry: %v", err)
		}
	})
}

// TestDashboardPreplanScanWarningKinds checks the kind of each warning of
// dashboardPreplanScan on real files, that dashboardPreplans gives the same
// texts, and the ready-list notes of each kind: a file that is not read and
// the older files give a note, a bad Started time gives none.
func TestDashboardPreplanScanWarningKinds(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, paths.PreplanSubdir)
	file := writePreplanTopic(t, root, "auth-flow.md", preplanWriteSkeleton)
	old := time.Now().Add(-time.Hour)
	for i := 0; i < dashboardPreplanLimit; i++ {
		p := writePreplanTopic(t, root, fmt.Sprintf("old-%03d.md", i), "# Preplan: old\n\n**Status:** in progress\n")
		at := old.Add(-time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "missing.md"), filepath.Join(dir, "gone.md")); err != nil {
		t.Fatal(err)
	}
	writePreplanTopic(t, root, "bad-start.md", "# Preplan: bad\n\n**Status:** in progress\n\n**Started:** nope\n")

	_, warnings := dashboardPreplanScan(root)
	_, texts := dashboardPreplans(root)
	var kinds []dashboardPreplanWarnKind
	for i, w := range warnings {
		kinds = append(kinds, w.kind)
		if i >= len(texts) || texts[i] != w.text {
			t.Errorf("warning %d = %q, dashboardPreplans gives %q", i, w.text, texts)
		}
	}
	want := []dashboardPreplanWarnKind{dashboardPreplanWarnFile, dashboardPreplanWarnStart, dashboardPreplanWarnOlder}
	if !reflect.DeepEqual(kinds, want) || len(texts) != len(warnings) {
		t.Fatalf("kinds = %v, want %v (warnings %q)", kinds, want, texts)
	}

	_, _, notes := preplanReadyList(root, file)
	wantNotes := []string{
		preplanReadyIncomplete + "Preplan gone not read: " + strings.TrimPrefix(warnings[0].text, "Preplan gone not read: ") + ".",
		preplanReadyIncomplete + "2 older preplan topic files not shown, and readyLeftOut does not count them.",
	}
	if !reflect.DeepEqual(notes, wantNotes) {
		t.Errorf("notes = %q, want %q", notes, wantNotes)
	}
}
