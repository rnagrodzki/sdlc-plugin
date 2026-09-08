package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/wave"
)

// ---------------------------------------------------------------------------
// Action: wave-compute
//
// wave-compute is stateless: it parses a plan markdown file directly and
// computes the wave schedule via wave.ComputeWaves. It never reads or writes
// execution state, so it can run before any state file exists (execute's
// Step 2 CLASSIFY runs ahead of Step 5's wave-1 state bootstrap).
// ---------------------------------------------------------------------------

// execActionWaveCompute parses the plan at in.PlanPath, extracts per-task
// Complexity/Risk/DependsOn/Files/Verify metadata, merges in.ExtraDepsJSON
// with each task's explicit "Depends on" field, and calls wave.ComputeWaves
// to produce the wave schedule.
func execActionWaveCompute(in ExecuteStateIn) (any, error) {
	if in.PlanPath == "" {
		return nil, &mcpserver.DomainError{Msg: "wave-compute: planPath is required"}
	}

	// Parse extraDepsJson before touching the filesystem: argument errors
	// win over file-read errors (matches wave-done/task-done's check order).
	var extraDeps []wave.ExtraDep
	if in.ExtraDepsJSON != "" {
		if err := json.Unmarshal([]byte(in.ExtraDepsJSON), &extraDeps); err != nil {
			return nil, &mcpserver.DomainError{Msg: "wave-compute: extraDepsJson is not valid JSON: " + err.Error(), Cause: err}
		}
	}

	content, err := os.ReadFile(in.PlanPath)
	if err != nil {
		return nil, &mcpserver.InfraError{Msg: fmt.Sprintf("wave-compute: read plan file %q: %s", in.PlanPath, err.Error()), Cause: err}
	}

	tasks, err := waveComputeParseTasks(string(content))
	if err != nil {
		return nil, err
	}

	out, err := wave.ComputeWaves(wave.ComputeInput{Tasks: tasks, ExtraDeps: extraDeps})
	if err != nil {
		return nil, &mcpserver.DomainError{Msg: "wave-compute: " + err.Error(), Cause: err}
	}

	return waveComputeRenderOutput(out), nil
}

// waveComputeParseTasks extracts wave.TaskInput values from raw plan
// markdown, reusing this package's existing plan-parsing helpers
// (extractTasks/extractField/extractDelimitedBlock, validators.go) rather
// than reimplementing plan parsing.
func waveComputeParseTasks(content string) ([]wave.TaskInput, error) {
	planTasks := extractTasks(content)
	if len(planTasks) == 0 {
		return nil, &mcpserver.DomainError{Msg: `wave-compute: no tasks found in plan (expected "### Task N: <title>" headings)`}
	}

	var issues []string
	tasks := make([]wave.TaskInput, 0, len(planTasks))

	for _, t := range planTasks {
		prefix := fmt.Sprintf("Task %d", t.Number)

		complexity, ok := extractField(t.Body, "Complexity")
		switch {
		case !ok || complexity == "":
			issues = append(issues, prefix+": missing **Complexity:**")
		case !containsStr(validComplexity, complexity):
			issues = append(issues, fmt.Sprintf("%s: invalid Complexity %q (expected one of: %s)", prefix, complexity, strings.Join(validComplexity, ", ")))
		}

		risk, ok := extractField(t.Body, "Risk")
		switch {
		case !ok || risk == "":
			issues = append(issues, prefix+": missing **Risk:**")
		case !containsStr(validRisk, risk):
			issues = append(issues, fmt.Sprintf("%s: invalid Risk %q (expected one of: %s)", prefix, risk, strings.Join(validRisk, ", ")))
		}

		// Verify is opaque to wave.ComputeWaves (used only for wave-level
		// hint equality) - a missing/empty value is not fatal here.
		verify, _ := extractField(t.Body, "Verify")

		tasks = append(tasks, wave.TaskInput{
			Number:     t.Number,
			Title:      t.Title,
			Complexity: complexity,
			Risk:       risk,
			DependsOn:  waveComputeParseDependsOn(t.Body),
			Files:      waveComputeParseFiles(t.Body),
			Verify:     verify,
		})
	}

	if len(issues) > 0 {
		return nil, &mcpserver.DomainError{Msg: "wave-compute: plan task metadata invalid: " + strings.Join(issues, "; ")}
	}

	return tasks, nil
}

// waveComputeParseDependsOn extracts task-number references from a task's
// **Depends on:** field (e.g. "Task 2, Task 3" or "none"), reusing pf4RefRe
// (validators.go) - the same pattern PF4 plan validation uses.
func waveComputeParseDependsOn(body string) []int {
	dependsOn, ok := extractField(body, "Depends on")
	if !ok || dependsOn == "" || strings.EqualFold(dependsOn, "none") {
		return nil
	}
	var refs []int
	for _, m := range pf4RefRe.FindAllStringSubmatch(dependsOn, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		refs = append(refs, n)
	}
	return refs
}

// waveComputeParseFiles extracts literal file paths from a task's
// **Files:** block (Create:/Modify:/Test: bullet lines). Only the first
// backtick-quoted path on each bullet line is taken: a Modify line's
// trailing "— [what changes]" note can itself contain backticked tokens
// that are not file paths, so a whole-block scan would over-capture.
func waveComputeParseFiles(body string) []string {
	block, found := extractDelimitedBlock(body, psFilesBlockStartRe, []string{"\n**", "\n### ", "\n---", "\n## "})
	if !found {
		return nil
	}
	var files []string
	for _, line := range psFileLineBulletRe.FindAllString(block, -1) {
		if m := backtickPathRe.FindStringSubmatch(line); m != nil {
			files = append(files, m[1])
		}
	}
	return files
}

// waveComputeRenderOutput converts a *wave.ComputeOutput into the
// wave-compute action's response shape:
//
//	{route, preWave, waves[{number, tasks[], expectedFiles[], verificationHint}]}
func waveComputeRenderOutput(out *wave.ComputeOutput) map[string]any {
	result := map[string]any{
		"route":   out.Route,
		"preWave": waveComputeRenderTasks(out.PreWave),
		"waves":   waveComputeRenderWaves(out.Waves),
	}
	if len(out.Errors) > 0 {
		result["errors"] = out.Errors
	}
	return result
}

func waveComputeRenderWaves(waves []wave.ComputedWave) []map[string]any {
	rendered := make([]map[string]any, 0, len(waves))
	for _, w := range waves {
		entry := map[string]any{
			"number":        w.Number,
			"tasks":         waveComputeRenderTasks(w.Tasks),
			"expectedFiles": waveComputeStrSlice(w.ExpectedFiles),
		}
		if w.VerificationHint != "" {
			entry["verificationHint"] = w.VerificationHint
		}
		rendered = append(rendered, entry)
	}
	return rendered
}

// waveComputeRenderTasks renders []wave.WaveTask into the response's task
// shape. task ids are stringified to match the tasksJson convention already
// consumed downstream by the wave-start action (task ids are strings there).
func waveComputeRenderTasks(tasks []wave.WaveTask) []map[string]any {
	rendered := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		rendered = append(rendered, map[string]any{
			"id":         strconv.Itoa(t.Number),
			"title":      t.Title,
			"complexity": t.Complexity,
			"risk":       t.Risk,
			"files":      waveComputeStrSlice(t.Files),
			"verify":     t.Verify,
		})
	}
	return rendered
}

// waveComputeStrSlice returns a non-nil empty slice for nil input so JSON
// serializes "[]" instead of "null".
func waveComputeStrSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
