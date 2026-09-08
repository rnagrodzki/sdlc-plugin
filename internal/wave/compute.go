package wave

import (
	"fmt"
	"sort"

	"github.com/rnagrodzki/sdlc-plugin/internal/budget"
)

// ---------------------------------------------------------------------------
// Input / output types
// ---------------------------------------------------------------------------

// TaskInput describes one plan task fed into the wave computation.
type TaskInput struct {
	Number     int
	Title      string
	Complexity string // "Trivial" | "Standard" | "Complex"
	Risk       string // "Low" | "Medium" | "High"
	DependsOn  []int
	Files      []string
	Verify     string
}

// ExtraDep is an additional dependency edge injected at classification time.
type ExtraDep struct {
	Task      int    `json:"task"`
	DependsOn int    `json:"dependsOn"`
	Reason    string `json:"reason"`
}

// ComputeInput is the full input to ComputeWaves.
type ComputeInput struct {
	Tasks     []TaskInput
	ExtraDeps []ExtraDep
}

// WaveTask is one task within a computed wave, carrying enough metadata for
// the caller to build dispatch manifests.
type WaveTask struct {
	Number     int
	Title      string
	Complexity string
	Risk       string
	Files      []string
	Verify     string
}

// ComputedWave is one wave in the output schedule.
type ComputedWave struct {
	Number           int
	Tasks            []WaveTask
	ExpectedFiles    []string
	VerificationHint string
}

// ComputeOutput is the result of ComputeWaves.
type ComputeOutput struct {
	Route   string // "direct" | "waves"
	PreWave []WaveTask
	Waves   []ComputedWave
	Errors  []string
}

// ---------------------------------------------------------------------------
// ComputeWaves — pure, deterministic wave scheduler
// ---------------------------------------------------------------------------

// ComputeWaves assigns plan tasks to execution waves using Kahn's topological
// sort, critical-path ordering, same-file conflict resolution, adaptive size
// caps (via budget.StaticCap), and risk spreading. Every map iteration uses
// sorted-key order to guarantee deterministic output for any input permutation.
func ComputeWaves(in ComputeInput) (*ComputeOutput, error) {
	// --- 0. Empty input ---------------------------------------------------
	if len(in.Tasks) == 0 {
		return &ComputeOutput{Route: "direct"}, nil
	}

	// --- 1. Validate & build adjacency ------------------------------------
	taskByNum := make(map[int]*TaskInput, len(in.Tasks))
	for i := range in.Tasks {
		t := &in.Tasks[i]
		if _, dup := taskByNum[t.Number]; dup {
			return nil, fmt.Errorf("duplicate task number %d", t.Number)
		}
		taskByNum[t.Number] = t
	}

	// Collect all task numbers sorted for deterministic iteration.
	allNums := sortedKeys(taskByNum)

	// Build dependency sets (successors and predecessors) with dedup.
	// deps[t] = set of tasks t depends on; succs[t] = set of tasks that depend on t.
	deps := make(map[int]map[int]bool, len(allNums))
	succs := make(map[int]map[int]bool, len(allNums))
	for _, n := range allNums {
		deps[n] = make(map[int]bool)
		succs[n] = make(map[int]bool)
	}

	var softErrors []string

	// Merge DependsOn edges.
	for _, n := range allNums {
		t := taskByNum[n]
		for _, d := range t.DependsOn {
			if _, ok := taskByNum[d]; !ok {
				return nil, fmt.Errorf("task %d depends on unknown task %d", n, d)
			}
			if d == n {
				return nil, fmt.Errorf("task %d has self-dependency", n)
			}
			deps[n][d] = true
			succs[d][n] = true
		}
	}

	// Merge ExtraDeps edges.
	for _, ed := range in.ExtraDeps {
		if _, ok := taskByNum[ed.Task]; !ok {
			softErrors = append(softErrors, fmt.Sprintf("ExtraDep references unknown task %d", ed.Task))
			continue
		}
		if _, ok := taskByNum[ed.DependsOn]; !ok {
			softErrors = append(softErrors, fmt.Sprintf("ExtraDep references unknown dependency %d", ed.DependsOn))
			continue
		}
		if ed.Task == ed.DependsOn {
			return nil, fmt.Errorf("ExtraDep creates self-dependency on task %d", ed.Task)
		}
		deps[ed.Task][ed.DependsOn] = true
		succs[ed.DependsOn][ed.Task] = true
	}

	// --- 2. Kahn's topo sort with wave-level assignment -------------------
	waveOf := make(map[int]int, len(allNums))
	inDeg := make(map[int]int, len(allNums))
	for _, n := range allNums {
		inDeg[n] = len(deps[n])
	}

	// Seed: tasks with no dependencies → wave 1.
	var queue []int
	for _, n := range allNums {
		if inDeg[n] == 0 {
			queue = append(queue, n)
			waveOf[n] = 1
		}
	}

	processed := 0
	for len(queue) > 0 {
		// Process current frontier in sorted order for determinism.
		sort.Ints(queue)
		next := queue
		queue = nil
		for _, n := range next {
			processed++
			for _, s := range sortedBoolKeys(succs[n]) {
				inDeg[s]--
				// Successor's wave = max(current, dep_wave + 1).
				if w := waveOf[n] + 1; w > waveOf[s] {
					waveOf[s] = w
				}
				if inDeg[s] == 0 {
					queue = append(queue, s)
				}
			}
		}
	}

	if processed < len(allNums) {
		// Cycle detected — list remaining nodes sorted.
		var cycleNodes []int
		for _, n := range allNums {
			if inDeg[n] > 0 {
				cycleNodes = append(cycleNodes, n)
			}
		}
		return nil, fmt.Errorf("dependency cycle involving tasks %v", cycleNodes)
	}

	// --- 3. Critical path (reverse-topo DP) -------------------------------
	// cp(t) = 1 + max(cp(successors)); sinks = 1.
	cp := make(map[int]int, len(allNums))
	// Process in reverse topological order (highest wave first, then highest number first).
	revOrder := make([]int, len(allNums))
	copy(revOrder, allNums)
	sort.Slice(revOrder, func(i, j int) bool {
		if waveOf[revOrder[i]] != waveOf[revOrder[j]] {
			return waveOf[revOrder[i]] > waveOf[revOrder[j]]
		}
		return revOrder[i] > revOrder[j]
	})
	for _, n := range revOrder {
		maxSucc := 0
		for _, s := range sortedBoolKeys(succs[n]) {
			if cp[s] > maxSucc {
				maxSucc = cp[s]
			}
		}
		cp[n] = 1 + maxSucc
	}

	// --- 4. Pre-wave extraction -------------------------------------------
	// Candidates: Trivial, no deps (wave 1), has ≥1 successor.
	preWaveSet := make(map[int]bool)
	for _, n := range allNums {
		t := taskByNum[n]
		if t.Complexity == "Trivial" && len(deps[n]) == 0 && len(succs[n]) > 0 {
			preWaveSet[n] = true
		}
	}

	// Recompute wave levels treating pre-wave tasks as already satisfied.
	if len(preWaveSet) > 0 {
		// Rebuild in-degree without pre-wave tasks.
		for _, n := range allNums {
			if preWaveSet[n] {
				waveOf[n] = 0 // sentinel: not in any wave
				continue
			}
			// Recount deps excluding pre-wave.
			effectiveDeps := 0
			maxDepWave := 0
			for _, d := range sortedBoolKeys(deps[n]) {
				if preWaveSet[d] {
					continue
				}
				effectiveDeps++
				if waveOf[d] > maxDepWave {
					maxDepWave = waveOf[d]
				}
			}
			if effectiveDeps == 0 {
				waveOf[n] = 1
			} else {
				waveOf[n] = maxDepWave + 1
			}
		}

		// Re-run BFS to propagate correct wave levels after pre-wave removal.
		recomputeWaveLevels(allNums, deps, preWaveSet, waveOf)
	}

	// --- 5. Small-plan routing check (after validation, before constraints) --
	// ≤3 total tasks, all Trivial/Standard, no High-risk → direct.
	if len(allNums) <= 3 {
		allSimple := true
		for _, n := range allNums {
			t := taskByNum[n]
			if t.Complexity != "Trivial" && t.Complexity != "Standard" {
				allSimple = false
				break
			}
			if t.Risk == "High" {
				allSimple = false
				break
			}
		}
		if allSimple {
			out := &ComputeOutput{
				Route:  "direct",
				Errors: softErrors,
			}
			return out, nil
		}
	}

	// --- 6–8. Fixpoint: same-file, cap, risk spreading --------------------
	// Outer loop: repeat until stable.
	for iter := 0; iter < 1000; iter++ {
		changed := false

		// 6a. Dependency invariant: wave(t) > wave(dep) for all deps.
		if depChanged := enforceDependencyInvariant(allNums, deps, preWaveSet, waveOf); depChanged {
			changed = true
		}

		// 6b. Same-file constraint: within a wave, per file, keep lowest task
		// number, bump the rest +1.
		if sfChanged := enforceSameFile(allNums, taskByNum, preWaveSet, waveOf); sfChanged {
			changed = true
		}

		// 6c. Adaptive size cap via budget.StaticCap.
		if capChanged := enforceCapLimit(allNums, taskByNum, preWaveSet, waveOf, cp); capChanged {
			changed = true
		}

		// 6d. Risk spreading: max 1 high-risk task per wave.
		if riskChanged := enforceRiskSpreading(allNums, taskByNum, preWaveSet, waveOf, cp); riskChanged {
			changed = true
		}

		if !changed {
			break
		}
	}

	// --- 9. Assemble output -----------------------------------------------
	return assembleOutput(allNums, taskByNum, preWaveSet, waveOf, cp, softErrors), nil
}

// ---------------------------------------------------------------------------
// Helper: recompute wave levels via BFS after pre-wave extraction
// ---------------------------------------------------------------------------

func recomputeWaveLevels(allNums []int, deps map[int]map[int]bool, preWaveSet map[int]bool, waveOf map[int]int) {
	// Iterative relaxation until stable.
	for iter := 0; iter < 1000; iter++ {
		changed := false
		for _, n := range allNums {
			if preWaveSet[n] {
				continue
			}
			maxDep := 0
			hasDep := false
			for _, d := range sortedBoolKeys(deps[n]) {
				if preWaveSet[d] {
					continue
				}
				hasDep = true
				if waveOf[d] > maxDep {
					maxDep = waveOf[d]
				}
			}
			want := 1
			if hasDep {
				want = maxDep + 1
			}
			if waveOf[n] != want {
				waveOf[n] = want
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

// ---------------------------------------------------------------------------
// Constraint enforcement helpers
// ---------------------------------------------------------------------------

func enforceDependencyInvariant(allNums []int, deps map[int]map[int]bool, preWaveSet map[int]bool, waveOf map[int]int) bool {
	changed := false
	for _, n := range allNums {
		if preWaveSet[n] {
			continue
		}
		maxDep := 0
		for _, d := range sortedBoolKeys(deps[n]) {
			if preWaveSet[d] {
				continue
			}
			if waveOf[d] > maxDep {
				maxDep = waveOf[d]
			}
		}
		if maxDep > 0 && waveOf[n] <= maxDep {
			waveOf[n] = maxDep + 1
			changed = true
		}
	}
	return changed
}

func enforceSameFile(allNums []int, taskByNum map[int]*TaskInput, preWaveSet map[int]bool, waveOf map[int]int) bool {
	changed := false
	// Group tasks by wave.
	waveGroups := groupByWave(allNums, preWaveSet, waveOf)
	waves := sortedKeys(waveGroups)

	for _, w := range waves {
		tasks := waveGroups[w]
		// For each file, track which tasks touch it.
		fileToTasks := make(map[string][]int)
		for _, n := range tasks {
			for _, f := range taskByNum[n].Files {
				fileToTasks[f] = append(fileToTasks[f], n)
			}
		}
		// For each file with >1 task, keep lowest number, bump rest.
		for _, f := range sortedStringKeys(fileToTasks) {
			ts := fileToTasks[f]
			if len(ts) <= 1 {
				continue
			}
			sort.Ints(ts)
			// Keep ts[0] (lowest number), bump the rest.
			for _, n := range ts[1:] {
				if waveOf[n] == w { // still in this wave
					waveOf[n] = w + 1
					changed = true
				}
			}
		}
	}
	return changed
}

func enforceCapLimit(allNums []int, taskByNum map[int]*TaskInput, preWaveSet map[int]bool, waveOf map[int]int, cp map[int]int) bool {
	changed := false
	waveGroups := groupByWave(allNums, preWaveSet, waveOf)
	waves := sortedKeys(waveGroups)

	// Count total non-pre-wave tasks for remaining calculation.
	totalNonPre := 0
	for _, n := range allNums {
		if !preWaveSet[n] {
			totalNonPre++
		}
	}

	dispatched := 0
	for _, w := range waves {
		remaining := totalNonPre - dispatched
		capVal := budget.StaticCap(remaining)

		tasks := waveGroups[w]
		// Compute weighted count.
		type entry struct {
			num int
			wt  int
			cp  int
		}
		var entries []entry
		for _, n := range tasks {
			wt := 1
			if taskByNum[n].Complexity == "Complex" {
				wt = 2
			}
			entries = append(entries, entry{num: n, wt: wt, cp: cp[n]})
		}

		// Sort by critical-path DESC, then number ASC (keep highest CP).
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].cp != entries[j].cp {
				return entries[i].cp > entries[j].cp
			}
			return entries[i].num < entries[j].num
		})

		// Greedily keep tasks up to cap.
		used := 0
		kept := 0
		for i, e := range entries {
			if used+e.wt > capVal {
				// Defer this and all remaining tasks.
				for _, deferred := range entries[i:] {
					if waveOf[deferred.num] == w {
						waveOf[deferred.num] = w + 1
						changed = true
					}
				}
				break
			}
			used += e.wt
			kept++
		}

		dispatched += kept
	}
	return changed
}

func enforceRiskSpreading(allNums []int, taskByNum map[int]*TaskInput, preWaveSet map[int]bool, waveOf map[int]int, cp map[int]int) bool {
	changed := false
	waveGroups := groupByWave(allNums, preWaveSet, waveOf)
	waves := sortedKeys(waveGroups)

	for _, w := range waves {
		tasks := waveGroups[w]
		// Find high-risk tasks.
		var highRisk []int
		for _, n := range tasks {
			if taskByNum[n].Risk == "High" {
				highRisk = append(highRisk, n)
			}
		}
		if len(highRisk) <= 1 {
			continue
		}
		// Sort by CP DESC, number ASC — keep first, bump rest.
		sort.Slice(highRisk, func(i, j int) bool {
			if cp[highRisk[i]] != cp[highRisk[j]] {
				return cp[highRisk[i]] > cp[highRisk[j]]
			}
			return highRisk[i] < highRisk[j]
		})
		for _, n := range highRisk[1:] {
			if waveOf[n] == w {
				waveOf[n] = w + 1
				changed = true
			}
		}
	}
	return changed
}

// ---------------------------------------------------------------------------
// Output assembly
// ---------------------------------------------------------------------------

func assembleOutput(allNums []int, taskByNum map[int]*TaskInput, preWaveSet map[int]bool, waveOf map[int]int, cp map[int]int, softErrors []string) *ComputeOutput {
	out := &ComputeOutput{
		Route:  "waves",
		Errors: softErrors,
	}

	// Pre-wave tasks, sorted by CP DESC then number ASC.
	var preNums []int
	for _, n := range allNums {
		if preWaveSet[n] {
			preNums = append(preNums, n)
		}
	}
	sortByCPThenNum(preNums, cp)
	for _, n := range preNums {
		out.PreWave = append(out.PreWave, makeWaveTask(taskByNum[n]))
	}

	// Build waves.
	waveGroups := groupByWave(allNums, preWaveSet, waveOf)
	waveNums := sortedKeys(waveGroups)

	for i, w := range waveNums {
		tasks := waveGroups[w]
		sortByCPThenNum(tasks, cp)

		cw := ComputedWave{
			Number: i + 1, // renumber sequentially from 1
		}
		fileSet := make(map[string]bool)
		verifySet := make(map[string]bool)
		for _, n := range tasks {
			t := taskByNum[n]
			cw.Tasks = append(cw.Tasks, makeWaveTask(t))
			for _, f := range t.Files {
				fileSet[f] = true
			}
			verifySet[t.Verify] = true
		}

		// ExpectedFiles: sorted, deduped.
		cw.ExpectedFiles = sortedBoolStringKeys(fileSet)

		// VerificationHint: only when every task shares the same non-empty Verify.
		if len(verifySet) == 1 {
			for v := range verifySet {
				if v != "" {
					cw.VerificationHint = v
				}
			}
		}

		out.Waves = append(out.Waves, cw)
	}

	return out
}

func makeWaveTask(t *TaskInput) WaveTask {
	files := make([]string, len(t.Files))
	copy(files, t.Files)
	sort.Strings(files)
	return WaveTask{
		Number:     t.Number,
		Title:      t.Title,
		Complexity: t.Complexity,
		Risk:       t.Risk,
		Files:      files,
		Verify:     t.Verify,
	}
}

// ---------------------------------------------------------------------------
// Deterministic iteration helpers
// ---------------------------------------------------------------------------

func sortedKeys[V any](m map[int]V) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

func sortedBoolKeys(m map[int]bool) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedBoolStringKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func groupByWave(allNums []int, preWaveSet map[int]bool, waveOf map[int]int) map[int][]int {
	groups := make(map[int][]int)
	for _, n := range allNums {
		if preWaveSet[n] {
			continue
		}
		w := waveOf[n]
		groups[w] = append(groups[w], n)
	}
	// Ensure deterministic order within each group.
	for w := range groups {
		sort.Ints(groups[w])
	}
	return groups
}

func sortByCPThenNum(nums []int, cp map[int]int) {
	sort.Slice(nums, func(i, j int) bool {
		if cp[nums[i]] != cp[nums[j]] {
			return cp[nums[i]] > cp[nums[j]]
		}
		return nums[i] < nums[j]
	})
}
