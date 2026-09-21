package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
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
// a plan markdown file via the fsseam (mkdirTempFunc/writeFileFunc/
// readFileFunc, internal/tools/fsseam.go), so each test below seeds a
// fixture plan into installFakeFS's in-memory map rather than the real
// filesystem, and rather than calling a pure in-memory function directly.
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
