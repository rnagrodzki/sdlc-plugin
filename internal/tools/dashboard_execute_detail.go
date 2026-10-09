package tools

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// dashboardQueuedStep is the name of the last execute step. It holds the
// planned tasks that are in no wave and in no planned wave.
const dashboardQueuedStep = "queued"

// dashboardExecuteSteps adds the wave detail of an execute run to its
// pipeline. It expects p.Steps to hold one step for each wave map of the
// state, in state order, then one step for each planned wave that has not
// started, in ascending number order, as dashboardExecute builds them. Each
// wave step gets a detail with that one wave. A planned wave that has not
// started is a pending wave that lists its tasks. Planned tasks that are in
// no wave and in no such planned wave go to a last "queued" step.
// p.join.execDetail gets all waves plus the queued tasks. The function also
// sets p.CommitWaves, p.SessionID, and p.join.startedAt.
func dashboardExecuteSteps(p *DashboardPipeline, st *state.State) {
	data := st.Data

	commitWaves := true
	switch v := data["commitWaves"].(type) {
	case string:
		commitWaves = v != "false"
	case bool:
		commitWaves = v
	}
	p.CommitWaves = &commitWaves
	p.SessionID = dashboardStr(data["sessionId"])
	if t, ok := dashboardParseTime(p.StartedAt); ok {
		p.join.startedAt = t
	}

	// plannedTasks holds the plan names. Older runs have no such key: their
	// tasks keep empty names unless a wave stores one.
	var planOrder []DashboardTask
	planNames := map[string]string{}
	plannedTasks, _ := data["plannedTasks"].([]any)
	for _, raw := range plannedTasks {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := dashboardStr(m["id"])
		if id == "" {
			continue
		}
		name := dashboardStr(m["name"])
		planOrder = append(planOrder, DashboardTask{ID: id, Name: name, Status: StepPending})
		if key := execNormalizeTaskID(id); planNames[key] == "" {
			planNames[key] = name
		}
	}

	all := &DashboardStepDetail{Kind: dashboardKindWaves}
	inWave := map[string]bool{}
	stepIdx := 0
	waves, _ := data["waves"].([]any)
	for _, raw := range waves {
		w, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		wave := dashboardExecuteWave(w, st.Root, planNames, inWave)
		all.Waves = append(all.Waves, wave)
		if stepIdx < len(p.Steps) {
			p.Steps[stepIdx].Detail = &DashboardStepDetail{Kind: dashboardKindWaves, Waves: []DashboardWave{wave}}
		}
		stepIdx++
	}
	for _, wave := range dashboardPendingWaveDetails(data, planNames, inWave) {
		all.Waves = append(all.Waves, wave)
		if stepIdx < len(p.Steps) {
			p.Steps[stepIdx].Detail = &DashboardStepDetail{Kind: dashboardKindWaves, Waves: []DashboardWave{wave}}
		}
		stepIdx++
	}

	queued := []DashboardTask{}
	for _, t := range planOrder {
		key := execNormalizeTaskID(t.ID)
		if inWave[key] {
			continue
		}
		inWave[key] = true // a duplicate plan id is queued once
		queued = append(queued, t)
	}
	if len(queued) > 0 {
		all.Queued = queued
		p.Steps = append(p.Steps, DashboardStep{
			Name:   dashboardQueuedStep,
			Status: StepPending,
			Detail: &DashboardStepDetail{Kind: dashboardKindWaves, Queued: queued},
		})
	}
	p.join.execDetail = all
}

// dashboardPlannedWave is one entry of the plannedWaves key of an execute
// state: a wave number and the IDs of the tasks the plan puts in that wave.
type dashboardPlannedWave struct {
	number  int
	taskIDs []string
}

// dashboardParsePendingWaves returns the plannedWaves entries of data whose
// number no waves entry has, in ascending number order. It leaves out an
// entry that is not an object, has no number or a negative one, repeats an
// earlier number, or has no task ID. A task ID that is not a non-empty string
// is dropped from its entry. A wave number counts as started when a waves
// entry has it, whatever the status of that entry.
func dashboardParsePendingWaves(data map[string]any) []dashboardPlannedWave {
	started := map[int]bool{}
	waves, _ := data["waves"].([]any)
	for _, raw := range waves {
		if w, ok := raw.(map[string]any); ok {
			started[dashboardInt(w["number"])] = true
		}
	}

	var out []dashboardPlannedWave
	seen := map[int]bool{}
	entries, _ := data["plannedWaves"].([]any)
	for _, raw := range entries {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		f, ok := m["number"].(float64)
		if !ok || f < 0 {
			continue
		}
		n := int(f)
		if seen[n] {
			continue
		}
		seen[n] = true
		if started[n] {
			continue
		}
		var ids []string
		rawIDs, _ := m["taskIds"].([]any)
		for _, rawID := range rawIDs {
			if id := dashboardStr(rawID); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			continue
		}
		out = append(out, dashboardPlannedWave{number: n, taskIDs: ids})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].number < out[j].number })
	return out
}

// dashboardPendingWaveNumbers returns the numbers of the planned waves that no
// waves entry covers yet, in ascending order. It gives one number for each
// wave that dashboardPendingWaveDetails returns, in the same order.
func dashboardPendingWaveNumbers(data map[string]any) []int {
	var nums []int
	for _, pw := range dashboardParsePendingWaves(data) {
		nums = append(nums, pw.number)
	}
	return nums
}

// dashboardPendingWaveDetails returns one pending wave for each planned wave that no
// waves entry covers yet, in ascending number order. A task of such a wave
// takes its name from planNames and has the status pending. A task that is
// already in inWave stays out of the wave, so a task shows once. The
// function adds the normalized id of each task that it puts in a wave to
// inWave.
func dashboardPendingWaveDetails(data map[string]any, planNames map[string]string, inWave map[string]bool) []DashboardWave {
	var out []DashboardWave
	for _, pw := range dashboardParsePendingWaves(data) {
		wave := DashboardWave{Number: pw.number, Status: StepPending, Tasks: []DashboardTask{}}
		for _, id := range pw.taskIDs {
			key := execNormalizeTaskID(id)
			if inWave[key] {
				continue
			}
			inWave[key] = true
			wave.Tasks = append(wave.Tasks, DashboardTask{ID: id, Name: planNames[key], Status: StepPending})
		}
		out = append(out, wave)
	}
	return out
}

// dashboardExecuteWave builds one wave of an execute state. Task order is the
// wave's planned list, then task rows that are not in it. The name of a task
// is the first non-empty one of: the task row, the wave's planned list,
// planNames. Each task id, normalized, is added to inWave.
func dashboardExecuteWave(w map[string]any, root string, planNames map[string]string, inWave map[string]bool) DashboardWave {
	wave := DashboardWave{
		Number:       dashboardInt(w["number"]),
		Status:       dashboardStr(w["status"]),
		CommittedSHA: dashboardStr(w["committedSha"]),
		Tasks:        []DashboardTask{},
	}
	runID := dashboardStr(w["runId"])

	rows := map[string]map[string]any{}
	var rowOrder []string
	rawRows, _ := w["tasks"].([]any)
	for _, raw := range rawRows {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := dashboardStr(row["id"])
		if id == "" {
			continue
		}
		key := execNormalizeTaskID(id)
		if _, seen := rows[key]; !seen {
			rowOrder = append(rowOrder, id)
		}
		rows[key] = row // the newest row of a task wins
	}

	seen := map[string]bool{}
	add := func(id, plannedName string) {
		key := execNormalizeTaskID(id)
		if seen[key] {
			return
		}
		seen[key] = true
		inWave[key] = true
		row := rows[key]
		name := dashboardStr(row["name"])
		if name == "" {
			name = plannedName
		}
		if name == "" {
			name = planNames[key]
		}
		wave.Tasks = append(wave.Tasks, DashboardTask{
			ID:     id,
			Name:   name,
			Status: dashboardTaskStatus(row, root, runID, id),
		})
	}

	planned, _ := w["planned"].([]any)
	for _, raw := range planned {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if id := dashboardStr(m["id"]); id != "" {
			add(id, dashboardStr(m["name"]))
		}
	}
	for _, id := range rowOrder {
		add(id, "")
	}
	return wave
}

// dashboardTaskStatus returns the status of one execute task. A task row
// with a status gives that status. With no row status, an existing
// <root>/.sdlc-v2/runs/<runID>/progress/<taskID>.server.json gives
// in_progress, because a worker was sent. Otherwise the task is pending. A
// runID or taskID that is not a bare file name never matches a file.
func dashboardTaskStatus(row map[string]any, root, runID, taskID string) string {
	if s := dashboardStr(row["status"]); s != "" {
		return s
	}
	if dashboardBareName(runID) && dashboardBareName(taskID) {
		path := filepath.Join(root, paths.DataDir, paths.RunsSubdir, runID, "progress", taskID+".server.json")
		if _, err := os.Stat(path); err == nil {
			return StepInProgress
		}
	}
	return StepPending
}

// dashboardBareName reports whether s is a non-empty single path element.
func dashboardBareName(s string) bool {
	return s != "" && s != "." && s != ".." && filepath.Base(s) == s
}
