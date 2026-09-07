package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
// coverage-check fixtures spanning the G1..G21 gate space.
func gateRange(from, to int) []string {
	var gates []string
	for i := from; i <= to; i++ {
		gates = append(gates, fmt.Sprintf("G%d", i))
	}
	return gates
}

// allGates returns G1..G21, the full expected coverage set (SKILL.md Step 3
// coverageCheck: "the union of all gateIds[] arrays returned by lanes MUST
// equal {G1..G21} exactly").
func allGates() []string {
	return gateRange(1, 21)
}

// fiveLanesCoveringAllGates returns 5 passing LaneResult fixtures whose
// GateIDs partition G1..G21 exactly (5+5+6+2+3 = 21 gates).
func fiveLanesCoveringAllGates() []LaneResult {
	return []LaneResult{
		{Name: "lane-1", Status: "pass", GateIDs: gateRange(1, 5)},
		{Name: "lane-2", Status: "pass", GateIDs: gateRange(6, 10)},
		{Name: "lane-3", Status: "pass", GateIDs: gateRange(11, 16)},
		{Name: "lane-4", Status: "pass", GateIDs: gateRange(17, 18)},
		{Name: "lane-5", Status: "pass", GateIDs: gateRange(19, 21)},
	}
}

// TestPlanMergeResults_CoveragePass verifies that when 5 lanes' GateIDs union
// covers G1..G21 exactly, the coverage check finds no gaps.
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
// "Issues Found" — plugins/sdlc/skills/plan/SKILL.md Step 5 (line 690):
// "Status: Approved iff ALL lens reviewers returned Approved; otherwise
// Issues Found".
//
// KNOWN FAILING: internal/tools/plan_support.go's mergeResults currently
// sets mergedStatus = "Rejected" for this branch instead of "Issues Found",
// diverging from the SKILL.md spec this test's Contract is pinned to (see
// task fact sheet Contract row `_LensMergeMixed`). This test intentionally
// asserts the spec-correct value and is expected to fail (red) until
// plan_support.go's mergeResults is fixed — that fix is out of scope for
// this test-only task (Files You May Touch = plan_support_test.go only).
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

// ---------------------------------------------------------------------------
// plan_support material_snapshot / material_compare tests
//
// materialSnapshot and materialCompare (internal/tools/plan_support.go) read
// a plan markdown file from disk, so each test below writes a fixture plan
// to a t.TempDir() file (mirroring validators_test.go's writeFile pattern)
// rather than calling a pure in-memory function directly.
//
// Fixture construction note: snapshotPlan's **Contract:** extraction
// (extractDelimitedBlock in plan_support.go) captures everything from right
// after the "**Contract:**" marker forward to the next "### "/"---"/"## "
// boundary — it has no "**" boundary, unlike **Files:** and
// **openspec-task:**. So any field placed AFTER **Contract:** within the
// same task body would bleed into the Contracts map on edit. To keep each
// trigger dimension independently testable, every task fixture below places
// **Contract:** as the LAST field in its body, with **openspec-task:** and
// **Notes:** placed BEFORE it.
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

// snapshotOf writes content to <dir>/<name> and returns its material
// snapshot via the material_snapshot action.
func snapshotOf(t *testing.T, dir, name, content string) *PlanSnapshot {
	t.Helper()
	path := filepath.Join(dir, name)
	writeFile(t, path, content)
	out, err := materialSnapshot(PlanSupportIn{FilePath: path})
	if err != nil {
		t.Fatalf("materialSnapshot: %v", err)
	}
	if out.Snapshot == nil {
		t.Fatalf("materialSnapshot returned nil Snapshot")
	}
	return out.Snapshot
}

// materialCompareCheck snapshots `before`, writes `after` to its own file in
// the same temp dir, and returns the material_compare result comparing them.
func materialCompareCheck(t *testing.T, before, after string) PlanSupportOut {
	t.Helper()
	dir := t.TempDir()
	snap := snapshotOf(t, dir, "before.md", before)

	afterPath := filepath.Join(dir, "after.md")
	writeFile(t, afterPath, after)

	out, err := materialCompare(PlanSupportIn{FilePath: afterPath, Snapshot: snap})
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
	dir := t.TempDir()
	snap := snapshotOf(t, dir, "plan.md", materialBasePlan())

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
