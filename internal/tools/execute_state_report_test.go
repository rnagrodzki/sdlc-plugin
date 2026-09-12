package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// report action
// ---------------------------------------------------------------------------

func TestExecState_Report_Disabled(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	writeFile(t, filepath.Join(root, paths.DataDir, "local.json"), `{
		"automation": {
			"report": {"enabled": false}
		}
	}`)
	createExecState(t, root, "feat/report", map[string]any{
		"branch": "feat/report",
	})
	clock := fixedClock(testNow)

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "report",
		Branch: "feat/report",
	}, clock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := result.(ReportSkippedOut)
	if !ok {
		t.Fatalf("expected ReportSkippedOut, got %T", result)
	}
	if !out.Skipped {
		t.Error("expected Skipped=true")
	}
}

func TestExecState_Report_DisabledSkipsBeforeStateLookup(t *testing.T) {
	// No execute state exists for this branch at all — report must still
	// return {skipped:true} rather than a "no state file found" error, since
	// disabled reporting is checked before the state file is loaded.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	writeFile(t, filepath.Join(root, paths.DataDir, "local.json"), `{
		"automation": {
			"report": {"enabled": false}
		}
	}`)
	clock := fixedClock(testNow)

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "report",
		Branch: "feat/does-not-exist",
	}, clock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := result.(ReportSkippedOut)
	if !ok || !out.Skipped {
		t.Fatalf("expected ReportSkippedOut{Skipped:true}, got %#v (err=%v)", result, err)
	}
}

func TestExecState_Report_DefaultsToMDWhenNoConfig(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	createExecState(t, root, "feat/report", map[string]any{
		"branch":     "feat/report",
		"planPath":   "/plans/x.md",
		"startedAt":  "2025-06-15T10:00:00Z",
		"totalTasks": 0,
	})
	clock := fixedClock(testNow)

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "report",
		Branch: "feat/report",
	}, clock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := result.(ExecutionReportOut)
	if !ok {
		t.Fatalf("expected ExecutionReportOut, got %T", result)
	}
	if out.Format != "md" {
		t.Errorf("expected default format md, got %q", out.Format)
	}
	if out.Branch != "feat/report" {
		t.Errorf("expected branch feat/report, got %q", out.Branch)
	}
	if out.PlanPath != "/plans/x.md" {
		t.Errorf("expected planPath /plans/x.md, got %q", out.PlanPath)
	}
	if out.StartedAt != "2025-06-15T10:00:00Z" {
		t.Errorf("expected startedAt echoed, got %q", out.StartedAt)
	}
	// testNow is 2025-06-15T12:00:00Z, startedAt is 10:00:00Z -> 2h duration.
	if out.Duration == "" {
		t.Error("expected non-empty duration")
	}
	if out.RunID == "" {
		t.Error("expected non-empty runId derived from startedAt")
	}
}

func TestExecState_Report_JSONFormatFromConfig(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	writeFile(t, filepath.Join(root, paths.DataDir, "local.json"), `{
		"automation": {
			"report": {"format": "json"}
		}
	}`)
	createExecState(t, root, "feat/report", map[string]any{
		"branch": "feat/report",
	})
	clock := fixedClock(testNow)

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "report",
		Branch: "feat/report",
	}, clock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := result.(ExecutionReportOut)
	if !ok {
		t.Fatalf("expected ExecutionReportOut, got %T", result)
	}
	if out.Format != "json" {
		t.Errorf("expected format json, got %q", out.Format)
	}
}

func TestExecState_Report_WavesTasksAndAggregates(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	createExecState(t, root, "feat/report", map[string]any{
		"branch":     "feat/report",
		"startedAt":  "2025-06-15T09:00:00Z",
		"totalTasks": 3,
		"waves": []any{
			map[string]any{
				"number":       float64(1),
				"status":       "completed",
				"startedAt":    "2025-06-15T09:00:00Z",
				"completedAt":  "2025-06-15T09:30:00Z",
				"committedSha": "abc123",
				"tasks": []any{
					map[string]any{
						"id":           "1",
						"name":         "Task One",
						"status":       "completed",
						"complexity":   "Standard",
						"risk":         "Low",
						"filesChanged": []any{"a.go", "b.go"},
					},
					map[string]any{
						"id":     "2",
						"name":   "Task Two",
						"status": "failed",
					},
				},
			},
			map[string]any{
				"number":      float64(2),
				"status":      "completed",
				"startedAt":   "2025-06-15T09:30:00Z",
				"completedAt": "2025-06-15T09:45:00Z",
				"tasks": []any{
					map[string]any{
						"id":     "3",
						"name":   "Task Three",
						"status": "skipped-dependency",
					},
				},
			},
		},
	})
	clock := fixedClock(testNow)

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "report",
		Branch: "feat/report",
	}, clock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := result.(ExecutionReportOut)

	if out.TotalTasks != 3 {
		t.Errorf("expected totalTasks=3, got %d", out.TotalTasks)
	}
	if out.CompletedTasks != 1 {
		t.Errorf("expected completedTasks=1, got %d", out.CompletedTasks)
	}
	if out.FailedTasks != 1 {
		t.Errorf("expected failedTasks=1, got %d", out.FailedTasks)
	}
	if out.SkippedTasks != 1 {
		t.Errorf("expected skippedTasks=1, got %d", out.SkippedTasks)
	}
	if len(out.Waves) != 2 {
		t.Fatalf("expected 2 waves, got %d", len(out.Waves))
	}

	w1 := out.Waves[0]
	if w1.Number != 1 || w1.Status != "completed" || w1.CommittedSHA != "abc123" {
		t.Errorf("unexpected wave 1: %+v", w1)
	}
	if w1.Duration == "" {
		t.Error("expected wave 1 duration to be computed")
	}
	if len(w1.Tasks) != 2 {
		t.Fatalf("expected 2 tasks in wave 1, got %d", len(w1.Tasks))
	}
	t1 := w1.Tasks[0]
	if t1.ID != "1" || t1.Name != "Task One" || t1.Status != "completed" || t1.Complexity != "Standard" || t1.Risk != "Low" {
		t.Errorf("unexpected task 1: %+v", t1)
	}
	if t1.Files != "a.go, b.go" {
		t.Errorf("expected files 'a.go, b.go', got %q", t1.Files)
	}
}

func TestExecState_Report_IssueBuckets(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	createExecState(t, root, "feat/report", map[string]any{
		"branch": "feat/report",
		"issues": []any{
			map[string]any{"category": "drift", "severity": "error", "summary": "drift error"},
			map[string]any{"category": "drift", "severity": "warning", "summary": "drift warning"},
			map[string]any{"category": "drift", "severity": "info", "summary": "drift info"},
			map[string]any{"category": "wave-fail", "severity": "error", "summary": "wave failed"},
			map[string]any{"category": "task-fail", "severity": "error", "summary": "task failed"},
			map[string]any{"category": "done-with-concerns", "severity": "warning", "summary": "concern"},
		},
	})
	clock := fixedClock(testNow)

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "report",
		Branch: "feat/report",
	}, clock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := result.(ExecutionReportOut)

	if len(out.Drifts) != 3 {
		t.Errorf("expected 3 drift issues (all severities), got %d: %+v", len(out.Drifts), out.Drifts)
	}
	if len(out.Errors) != 2 {
		t.Errorf("expected 2 error issues (wave-fail, task-fail), got %d: %+v", len(out.Errors), out.Errors)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("expected 0 bare warnings (done-with-concerns is bucketed separately), got %d: %+v", len(out.Warnings), out.Warnings)
	}
	if len(out.Concerns) != 1 {
		t.Errorf("expected 1 concern issue, got %d: %+v", len(out.Concerns), out.Concerns)
	}
}

func TestExecState_Report_DecisionsAndFollowUps(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	createExecState(t, root, "feat/report", map[string]any{
		"branch": "feat/report",
		"context": map[string]any{
			"decisionsFromPriorWaves": []any{"decision A", "decision B"},
		},
		"pendingIssueDrafts": []any{
			map[string]any{"title": "draft 1", "body": "body 1"},
		},
	})
	clock := fixedClock(testNow)

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "report",
		Branch: "feat/report",
	}, clock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := result.(ExecutionReportOut)

	if len(out.Decisions) != 2 || out.Decisions[0] != "decision A" || out.Decisions[1] != "decision B" {
		t.Errorf("unexpected decisions: %v", out.Decisions)
	}
	if len(out.PendingIssueDrafts) != 1 {
		t.Errorf("expected 1 pending issue draft, got %d", len(out.PendingIssueDrafts))
	}
	if out.DeferredFindings != nil {
		t.Errorf("expected nil deferredFindings (not stamped on execute state), got %v", out.DeferredFindings)
	}
}

func TestExecState_Report_StepTimings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	createExecState(t, root, "feat/report", map[string]any{
		"branch": "feat/report",
	})
	createShipState(t, root, "feat/report", map[string]any{
		"branch": "feat/report",
		"steps": []any{
			map[string]any{
				"name":        "execute",
				"status":      "completed",
				"startedAt":   "2025-06-15T09:00:00Z",
				"completedAt": "2025-06-15T09:30:00Z",
			},
			map[string]any{
				"name":        "await-remote-review",
				"status":      "completed",
				"startedAt":   "2025-06-15T10:00:00Z",
				"completedAt": "2025-06-15T10:05:00Z",
			},
			map[string]any{
				"name":   "pr",
				"status": "pending",
			},
		},
	})
	clock := fixedClock(testNow)

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "report",
		Branch: "feat/report",
	}, clock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := result.(ExecutionReportOut)

	if len(out.StepTimings) != 3 {
		t.Fatalf("expected 3 step timings, got %d: %+v", len(out.StepTimings), out.StepTimings)
	}

	execStep := out.StepTimings[0]
	if execStep.Name != "execute" || execStep.Status != "completed" {
		t.Errorf("unexpected execute step: %+v", execStep)
	}
	if execStep.Duration != "30m 00s" {
		t.Errorf("expected execute step duration '30m 00s', got %q", execStep.Duration)
	}
	if execStep.HumanWait {
		t.Error("expected execute step HumanWait=false")
	}

	reviewStep := out.StepTimings[1]
	if !reviewStep.HumanWait {
		t.Error("expected await-remote-review step HumanWait=true")
	}
	if reviewStep.Duration != "5m 00s" {
		t.Errorf("expected await-remote-review duration '5m 00s', got %q", reviewStep.Duration)
	}

	prStep := out.StepTimings[2]
	if prStep.StartedAt != "" || prStep.Duration != "" {
		t.Errorf("expected pending pr step to have no startedAt/duration, got %+v", prStep)
	}
}

func TestExecState_Report_CLIEvidence_FiltersByBranchAndShipStartedAt(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	createExecState(t, root, "feat/report", map[string]any{
		"branch":    "feat/report",
		"startedAt": "2025-06-15T09:00:00Z",
	})
	// Ship state's startedAt predates the execute state's startedAt — since
	// must come from shipSt.Data["startedAt"], not out.StartedAt, so the
	// earlier entry below (08:30) is included.
	createShipState(t, root, "feat/report", map[string]any{
		"branch":    "feat/report",
		"startedAt": "2025-06-15T08:00:00Z",
	})

	if err := appendCLIEvidence(root, CLIEvidenceEntry{
		Timestamp: "2025-06-15T07:00:00Z", // before since — excluded
		Pipeline:  "ship",
		Branch:    "feat/report",
		Command:   "too-early",
	}); err != nil {
		t.Fatalf("appendCLIEvidence failed: %v", err)
	}
	if err := appendCLIEvidence(root, CLIEvidenceEntry{
		Timestamp: "2025-06-15T08:30:00Z", // after ship startedAt — included
		Pipeline:  "ship",
		Branch:    "feat/report",
		Command:   "in-window",
	}); err != nil {
		t.Fatalf("appendCLIEvidence failed: %v", err)
	}
	if err := appendCLIEvidence(root, CLIEvidenceEntry{
		Timestamp: "2025-06-15T08:30:00Z",
		Pipeline:  "ship",
		Branch:    "other-branch", // different branch — excluded
		Command:   "wrong-branch",
	}); err != nil {
		t.Fatalf("appendCLIEvidence failed: %v", err)
	}
	clock := fixedClock(testNow)

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "report",
		Branch: "feat/report",
	}, clock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := result.(ExecutionReportOut)

	if len(out.CLIEvidence) != 1 {
		t.Fatalf("expected 1 CLI evidence entry, got %d: %+v", len(out.CLIEvidence), out.CLIEvidence)
	}
	if out.CLIEvidence[0].Command != "in-window" {
		t.Errorf("expected 'in-window' entry, got %q", out.CLIEvidence[0].Command)
	}
}

func TestExecState_Report_CLIEvidenceAndStepTimings_EmptyWhenNoShipState(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	createExecState(t, root, "feat/report", map[string]any{
		"branch": "feat/report",
	})
	clock := fixedClock(testNow)

	result, err := executeState(root, root, ExecuteStateIn{
		Action: "report",
		Branch: "feat/report",
	}, clock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := result.(ExecutionReportOut)

	if out.CLIEvidence == nil || len(out.CLIEvidence) != 0 {
		t.Errorf("expected empty non-nil CLIEvidence, got %#v", out.CLIEvidence)
	}
	if out.StepTimings == nil || len(out.StepTimings) != 0 {
		t.Errorf("expected empty non-nil StepTimings, got %#v", out.StepTimings)
	}
}

func TestExecState_Report_UnknownBranch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	clock := fixedClock(testNow)

	_, err := executeState(root, root, ExecuteStateIn{
		Action: "report",
		Branch: "feat/does-not-exist",
	}, clock)
	if err == nil {
		t.Fatal("expected error for missing state file")
	}
	if _, ok := err.(*mcpserver.DataError); !ok {
		t.Fatalf("expected DataError, got %T: %v", err, err)
	}
}

func TestExecState_Report_ReadOnly(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.json"), `{}`)
	createExecState(t, root, "feat/report", map[string]any{
		"branch": "feat/report",
		"waves":  []any{},
	})
	clock := fixedClock(testNow)

	stPath := findExecStatePath(t, root, "feat/report")
	before, err := os.ReadFile(stPath)
	if err != nil {
		t.Fatalf("read state file before call: %v", err)
	}

	if _, err := executeState(root, root, ExecuteStateIn{
		Action: "report",
		Branch: "feat/report",
	}, clock); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	after, err := os.ReadFile(stPath)
	if err != nil {
		t.Fatalf("read state file after call: %v", err)
	}
	if string(before) != string(after) {
		t.Error("report action must not mutate the state file on disk")
	}
}

// findExecStatePath locates the on-disk execute state file for a branch,
// for byte-for-byte before/after comparison in TestExecState_Report_ReadOnly.
func findExecStatePath(t *testing.T, root, branch string) string {
	t.Helper()
	dir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read execution dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath := e.Name(); len(filepath) > 0 {
			return dirJoin(dir, filepath)
		}
	}
	t.Fatalf("no execute state file found under %s", dir)
	return ""
}

func dirJoin(dir, name string) string {
	return filepath.Join(dir, name)
}
