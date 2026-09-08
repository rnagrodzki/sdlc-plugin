package tools

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// ---------------------------------------------------------------------------
// wave-compute action tests
//
// wave-compute (internal/tools/execute_waves.go) is a stateless action: it
// parses a plan markdown file directly and calls wave.ComputeWaves
// (internal/wave/compute.go, not reimplemented here). These tests exercise
// the adapter's plan parsing and response shaping, not the wave-assignment
// algorithm itself (see internal/wave/compute_test.go for that).
// ---------------------------------------------------------------------------

// waveComputeWritePlan writes content to a temp file and returns its path.
func waveComputeWritePlan(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture plan: %v", err)
	}
	return path
}

// multiWaveFixture exercises, in one plan: an explicit dependency (Task 4 on
// Task 3), a same-file conflict between two independent tasks (1 and 2, both
// touching shared.go), a pre-wave trivial candidate (Task 3: Trivial, no
// deps, has a successor), and a shared verify hint ("tests") across the
// tasks that land in wave 1.
const multiWaveFixture = `# Plan

**Goal:** test fixture
**Architecture:** n/a
**Source:** n/a
**Verification:** n/a

### Task 1: Touch shared file A

**Complexity:** Standard
**Risk:** Low
**Depends on:** none
**Verify:** tests

**Files:**
- Modify: ` + "`shared.go`" + ` — add helper

**Acceptance criteria:**
- [ ] works

### Task 2: Touch shared file B

**Complexity:** Standard
**Risk:** Low
**Depends on:** none
**Verify:** tests

**Files:**
- Modify: ` + "`shared.go`" + ` — add another helper

**Acceptance criteria:**
- [ ] works

### Task 3: Trivial config bump

**Complexity:** Trivial
**Risk:** Low
**Depends on:** none
**Verify:** build

**Files:**
- Modify: ` + "`config.go`" + ` — bump version

**Acceptance criteria:**
- [ ] works

### Task 4: Consume the bump

**Complexity:** Standard
**Risk:** Medium
**Depends on:** Task 3
**Verify:** tests

**Files:**
- Create: ` + "`other.go`" + `

**Acceptance criteria:**
- [ ] works
`

func TestExecActionWaveCompute_MultiWave(t *testing.T) {
	path := waveComputeWritePlan(t, multiWaveFixture)

	res, err := execActionWaveCompute(ExecuteStateIn{PlanPath: path})
	if err != nil {
		t.Fatalf("execActionWaveCompute: %v", err)
	}
	out, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("result is %T, want map[string]any", res)
	}

	if got := out["route"]; got != "waves" {
		t.Fatalf("route = %v, want %q", got, "waves")
	}

	preWave, ok := out["preWave"].([]map[string]any)
	if !ok {
		t.Fatalf("preWave is %T, want []map[string]any", out["preWave"])
	}
	if len(preWave) != 1 || preWave[0]["id"] != "3" {
		t.Fatalf("preWave = %+v, want single task id 3", preWave)
	}

	waves, ok := out["waves"].([]map[string]any)
	if !ok {
		t.Fatalf("waves is %T, want []map[string]any", out["waves"])
	}
	if len(waves) != 2 {
		t.Fatalf("len(waves) = %d, want 2: %+v", len(waves), waves)
	}

	wave1 := waves[0]
	if wave1["number"] != 1 {
		t.Fatalf("waves[0].number = %v, want 1", wave1["number"])
	}
	wave1Tasks, _ := wave1["tasks"].([]map[string]any)
	if len(wave1Tasks) != 2 {
		t.Fatalf("wave1 tasks = %+v, want 2 (task 1 and task 4)", wave1Tasks)
	}
	wave1Ids := map[string]bool{}
	for _, tk := range wave1Tasks {
		wave1Ids[tk["id"].(string)] = true
	}
	if !wave1Ids["1"] || !wave1Ids["4"] {
		t.Fatalf("wave1 task ids = %v, want {1,4}", wave1Ids)
	}
	if hint := wave1["verificationHint"]; hint != "tests" {
		t.Fatalf("wave1.verificationHint = %v, want %q", hint, "tests")
	}
	expectedFiles1, _ := wave1["expectedFiles"].([]string)
	if !containsStr(expectedFiles1, "shared.go") || !containsStr(expectedFiles1, "other.go") {
		t.Fatalf("wave1.expectedFiles = %v, want to contain shared.go and other.go", expectedFiles1)
	}

	wave2 := waves[1]
	if wave2["number"] != 2 {
		t.Fatalf("waves[1].number = %v, want 2", wave2["number"])
	}
	wave2Tasks, _ := wave2["tasks"].([]map[string]any)
	if len(wave2Tasks) != 1 || wave2Tasks[0]["id"] != "2" {
		t.Fatalf("wave2 tasks = %+v, want single task id 2 (bumped for same-file conflict)", wave2Tasks)
	}
}

// independentTasksFixture has four tasks with no explicit dependencies and
// four distinct files, so absent extraDeps they all land in wave 1.
const independentTasksFixture = `### Task 1: A

**Complexity:** Standard
**Risk:** Low
**Depends on:** none
**Verify:** tests

**Files:**
- Create: ` + "`a.go`" + `

### Task 2: B

**Complexity:** Standard
**Risk:** Low
**Depends on:** none
**Verify:** tests

**Files:**
- Create: ` + "`b.go`" + `

### Task 3: C

**Complexity:** Standard
**Risk:** Low
**Depends on:** none
**Verify:** tests

**Files:**
- Create: ` + "`c.go`" + `

### Task 4: D

**Complexity:** Standard
**Risk:** Low
**Depends on:** none
**Verify:** tests

**Files:**
- Create: ` + "`d.go`" + `
`

func TestExecActionWaveCompute_ExtraDepsMergedWithDependsOn(t *testing.T) {
	path := waveComputeWritePlan(t, independentTasksFixture)

	// Baseline: no extraDeps, all four tasks land in wave 1.
	baseline, err := execActionWaveCompute(ExecuteStateIn{PlanPath: path})
	if err != nil {
		t.Fatalf("execActionWaveCompute (baseline): %v", err)
	}
	baseWaves := baseline.(map[string]any)["waves"].([]map[string]any)
	if len(baseWaves) != 1 {
		t.Fatalf("baseline waves = %+v, want 1 wave with all 4 tasks", baseWaves)
	}

	// extraDepsJson makes Task 4 depend on Task 1 -> Task 4 must move to a
	// later wave than Task 1, even though the plan's own "Depends on" field
	// says "none" for Task 4.
	withExtra, err := execActionWaveCompute(ExecuteStateIn{
		PlanPath:      path,
		ExtraDepsJSON: `[{"task":4,"dependsOn":1,"reason":"inferred"}]`,
	})
	if err != nil {
		t.Fatalf("execActionWaveCompute (extraDeps): %v", err)
	}
	out := withExtra.(map[string]any)
	if out["route"] != "waves" {
		t.Fatalf("route = %v, want waves once extraDeps forces a split", out["route"])
	}
	waves := out["waves"].([]map[string]any)
	if len(waves) < 2 {
		t.Fatalf("waves = %+v, want at least 2 waves (task 4 forced after task 1)", waves)
	}
	// Task 4 must not appear in the same wave as, or an earlier wave than, Task 1.
	waveOfID := map[string]int{}
	for _, w := range waves {
		num := w["number"].(int)
		for _, tk := range w["tasks"].([]map[string]any) {
			waveOfID[tk["id"].(string)] = num
		}
	}
	if waveOfID["4"] <= waveOfID["1"] {
		t.Fatalf("wave(task 4)=%d, wave(task 1)=%d; want task 4 strictly after task 1", waveOfID["4"], waveOfID["1"])
	}
}

func TestExecActionWaveCompute_DirectRoute(t *testing.T) {
	fixture := `### Task 1: A

**Complexity:** Trivial
**Risk:** Low
**Depends on:** none
**Verify:** build

**Files:**
- Create: ` + "`a.go`" + `

### Task 2: B

**Complexity:** Standard
**Risk:** Low
**Depends on:** Task 1
**Verify:** tests

**Files:**
- Create: ` + "`b.go`" + `
`
	path := waveComputeWritePlan(t, fixture)

	res, err := execActionWaveCompute(ExecuteStateIn{PlanPath: path})
	if err != nil {
		t.Fatalf("execActionWaveCompute: %v", err)
	}
	out := res.(map[string]any)
	if out["route"] != "direct" {
		t.Fatalf("route = %v, want direct", out["route"])
	}
	if preWave, ok := out["preWave"].([]map[string]any); !ok || len(preWave) != 0 {
		t.Fatalf("preWave = %+v, want empty slice", out["preWave"])
	}
	if waves, ok := out["waves"].([]map[string]any); !ok || len(waves) != 0 {
		t.Fatalf("waves = %+v, want empty slice", out["waves"])
	}
}

func TestExecActionWaveCompute_MissingPlanPath(t *testing.T) {
	_, err := execActionWaveCompute(ExecuteStateIn{})
	if err == nil {
		t.Fatal("want error for missing planPath, got nil")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Fatalf("err is %T, want *mcpserver.DomainError", err)
	}
}

func TestExecActionWaveCompute_InvalidExtraDepsJSON(t *testing.T) {
	path := waveComputeWritePlan(t, independentTasksFixture)
	_, err := execActionWaveCompute(ExecuteStateIn{PlanPath: path, ExtraDepsJSON: "{not json"})
	if err == nil {
		t.Fatal("want error for invalid extraDepsJson, got nil")
	}
}

func TestExecActionWaveCompute_PlanFileNotFound(t *testing.T) {
	_, err := execActionWaveCompute(ExecuteStateIn{PlanPath: filepath.Join(t.TempDir(), "missing.md")})
	if err == nil {
		t.Fatal("want error for missing plan file, got nil")
	}
}

func TestExecActionWaveCompute_NoTasksInPlan(t *testing.T) {
	path := waveComputeWritePlan(t, "# Just a heading, no ### Task sections\n")
	_, err := execActionWaveCompute(ExecuteStateIn{PlanPath: path})
	if err == nil {
		t.Fatal("want error for plan with no tasks, got nil")
	}
}

func TestExecActionWaveCompute_MissingComplexityField(t *testing.T) {
	fixture := `### Task 1: A

**Risk:** Low
**Depends on:** none
**Verify:** tests

**Files:**
- Create: ` + "`a.go`" + `
`
	path := waveComputeWritePlan(t, fixture)
	_, err := execActionWaveCompute(ExecuteStateIn{PlanPath: path})
	if err == nil {
		t.Fatal("want error for task missing **Complexity:**, got nil")
	}
}

func TestExecActionWaveCompute_CycleDetected(t *testing.T) {
	fixture := `### Task 1: A

**Complexity:** Standard
**Risk:** Low
**Depends on:** Task 2
**Verify:** tests

**Files:**
- Create: ` + "`a.go`" + `

### Task 2: B

**Complexity:** Standard
**Risk:** Low
**Depends on:** Task 1
**Verify:** tests

**Files:**
- Create: ` + "`b.go`" + `
`
	path := waveComputeWritePlan(t, fixture)
	_, err := execActionWaveCompute(ExecuteStateIn{PlanPath: path})
	if err == nil {
		t.Fatal("want error for circular dependency, got nil")
	}
}

// TestExecuteState_WaveComputeDispatch routes a call through the top-level
// executeState dispatcher (rather than calling execActionWaveCompute
// directly) to cover the "wave-compute" switch case wiring in
// execute_state.go, and confirms the action is stateless: it succeeds even
// when root points at an empty directory with no execution state file.
func TestExecuteState_WaveComputeDispatch(t *testing.T) {
	path := waveComputeWritePlan(t, independentTasksFixture)
	root := t.TempDir() // deliberately empty: no .sdlc/execution/ state file

	res, err := executeState(root, root, ExecuteStateIn{
		Action:   "wave-compute",
		PlanPath: path,
	}, time.Now)
	if err != nil {
		t.Fatalf("executeState(wave-compute): %v", err)
	}
	out, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("result is %T, want map[string]any", res)
	}
	if out["route"] != "direct" && out["route"] != "waves" {
		t.Fatalf("route = %v, want direct or waves", out["route"])
	}

	// No state file should have been created as a side effect: root is a
	// separate empty temp dir from the plan fixture's own temp dir, so any
	// entry here would mean wave-compute wrote something despite being
	// documented as stateless.
	entries, statErr := os.ReadDir(root)
	if statErr != nil {
		t.Fatalf("ReadDir(root): %v", statErr)
	}
	if len(entries) != 0 {
		t.Fatalf("root dir entries = %v, want none written by wave-compute", entries)
	}
}
