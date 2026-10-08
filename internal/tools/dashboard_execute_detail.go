package tools

import (
	"os"
	"path/filepath"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// dashboardQueuedStep is the name of the last execute step. It holds the
// planned tasks that are in no wave yet.
const dashboardQueuedStep = "queued"

// dashboardExecuteSteps adds the wave detail of an execute run to its
// pipeline. It expects p.Steps to hold one step for each wave map of the
// state, in state order, as dashboardExecute builds them. Each wave step gets
// a detail with that one wave. Planned tasks that are in no wave go to a last
// "queued" step. p.join.execDetail gets all waves plus the queued tasks. The
// function also sets p.CommitWaves, p.SessionID, and p.join.startedAt.
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
