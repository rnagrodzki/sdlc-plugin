package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

const shipReportBranch = "feat/ship-report"

// shipReportRoot returns a temp project root with an empty config.
func shipReportRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "")
	return root
}

// shipReportSteps builds a steps[] list with execute at executeStatus.
func shipReportSteps(executeStatus string) []any {
	return []any{
		map[string]any{"name": "execute", "status": executeStatus, "startedAt": "2025-06-15T10:00:00Z", "completedAt": "2025-06-15T10:30:00Z"},
		map[string]any{"name": "commit", "status": "completed", "startedAt": "2025-06-15T10:30:00Z", "completedAt": "2025-06-15T10:31:00Z"},
	}
}

// createShipReportState writes a ship state with a fixed startedAt plus extra.
func createShipReportState(t *testing.T, root string, extra map[string]any) {
	t.Helper()
	data := map[string]any{
		"branch":    shipReportBranch,
		"startedAt": "2025-06-15T10:00:00Z",
		"steps":     shipReportSteps("skipped"),
	}
	for k, v := range extra {
		data[k] = v
	}
	createShipState(t, root, shipReportBranch, data)
}

func runShipReport(t *testing.T, root string, detail map[string]any) ShipRunReportOut {
	t.Helper()
	d := map[string]any{"branch": shipReportBranch}
	for k, v := range detail {
		d[k] = v
	}
	result, err := shipState(root, root, ShipStateIn{Action: "report", Detail: d}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("report: unexpected error: %v", err)
	}
	out, ok := result.(ShipRunReportOut)
	if !ok {
		t.Fatalf("report: expected ShipRunReportOut, got %T", result)
	}
	return out
}

func appendPlanRun(t *testing.T, root string, rec history.RunRecord) {
	t.Helper()
	if err := history.NewFileWriter(historyDir(root)).AppendRun(rec); err != nil {
		t.Fatalf("append run: %v", err)
	}
}

func TestShipStateReport_ExecuteAbsent(t *testing.T) {
	root := shipReportRoot(t)
	createShipReportState(t, root, map[string]any{"steps": shipReportSteps("completed")})

	out := runShipReport(t, root, nil)
	if out.Execution != nil {
		t.Errorf("expected no execution section without execute state, got %+v", out.Execution)
	}
	if out.GuardrailHits == nil || len(out.GuardrailHits) != 0 {
		t.Errorf("guardrailHits must be an empty non-nil slice, got %#v", out.GuardrailHits)
	}
	if out.Plan != nil || out.PlanNote != "no plan linked to this run" {
		t.Errorf("expected null plan with note, got plan=%+v note=%q", out.Plan, out.PlanNote)
	}
	if out.Written || out.Path != "" {
		t.Errorf("read-only call must not write, got written=%v path=%q", out.Written, out.Path)
	}
	if len(out.Steps) != 2 || out.Steps[0].Name != "execute" || out.Steps[0].Duration != "30m 00s" {
		t.Errorf("unexpected steps: %+v", out.Steps)
	}
	if out.Format != "md" || !strings.HasPrefix(out.Display, "# Ship run report — "+shipReportBranch) {
		t.Errorf("expected md display, got format=%q display=%q", out.Format, out.Display)
	}
	if out.RunID != execDeriveRunID(map[string]any{"startedAt": "2025-06-15T10:00:00Z"}, 0) {
		t.Errorf("unexpected runId %q", out.RunID)
	}
	if strings.Contains(out.Display, "## Execution") {
		t.Error("display must not carry an Execution section when execute is absent")
	}

	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{`"plan":null`, `"guardrailHits":[]`, `"reviewLedger":null`, `"written":false`, `"next":`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("marshaled report missing %s: %s", key, raw)
		}
	}
}

func TestShipStateReport_ExecuteIncludedWhenStepCompleted(t *testing.T) {
	root := shipReportRoot(t)
	createExecState(t, root, shipReportBranch, map[string]any{
		"branch":     shipReportBranch,
		"startedAt":  "2025-06-15T10:01:00Z",
		"totalTasks": 1,
		"guardrailDecisions": []any{
			map[string]any{"decideType": "guardrail", "id": "no-secrets"},
		},
	})
	createShipReportState(t, root, map[string]any{"steps": shipReportSteps("completed")})

	out := runShipReport(t, root, nil)
	if out.Execution == nil {
		t.Fatal("expected execution section when execute step completed")
	}
	if out.Execution.TotalTasks != 1 || out.Execution.Branch != shipReportBranch {
		t.Errorf("unexpected execution report: %+v", out.Execution)
	}
	if len(out.GuardrailHits) != 1 || out.GuardrailHits[0] != "no-secrets" {
		t.Errorf("expected guardrail hit no-secrets, got %v", out.GuardrailHits)
	}
	if !strings.Contains(out.Display, "## Execution") || !strings.Contains(out.Display, "- no-secrets") {
		t.Errorf("display missing execution or guardrail hit:\n%s", out.Display)
	}
}

func TestShipStateReport_StaleExecuteStateExcluded(t *testing.T) {
	root := shipReportRoot(t)
	createExecState(t, root, shipReportBranch, map[string]any{
		"branch":   shipReportBranch,
		"planPath": "/abs/plans/old.md",
		"guardrailDecisions": []any{
			map[string]any{"decideType": "guardrail", "id": "stale-hit"},
		},
	})
	appendPlanRun(t, root, history.RunRecord{Skill: "plan", PlanFile: "/abs/plans/old.md", StartedAt: "2025-06-14T09:00:00Z", DurationMs: 1000})
	createShipReportState(t, root, map[string]any{"steps": shipReportSteps("skipped")})

	out := runShipReport(t, root, nil)
	if out.Execution != nil {
		t.Errorf("stale execute state must be excluded, got %+v", out.Execution)
	}
	if len(out.GuardrailHits) != 0 {
		t.Errorf("stale guardrail hits must be excluded, got %v", out.GuardrailHits)
	}
	if out.Plan != nil {
		t.Errorf("plan must be null without a completed execute step, got %+v", out.Plan)
	}
}

func TestShipStateReport_StampedState(t *testing.T) {
	root := shipReportRoot(t)
	createShipReportState(t, root, map[string]any{
		"pipelineStatus":      "completed",
		"pipelineCompletedAt": "2025-06-15T11:00:00Z",
		"flags":               map[string]any{"bump": "minor"},
	})

	out := runShipReport(t, root, map[string]any{"write": true})
	if !out.Written || out.Path == "" {
		t.Fatalf("expected report written on a stamped state, got written=%v path=%q", out.Written, out.Path)
	}
	if out.Bump != "minor" || out.Duration != "1h 00m" {
		t.Errorf("expected bump minor and duration 1h 00m, got bump=%q duration=%q", out.Bump, out.Duration)
	}
	if !strings.Contains(out.Display, "| Run | "+out.RunID+" · bump minor · 1h 00m |") {
		t.Errorf("display missing the Summary run row with bump and duration:\n%s", out.Display)
	}
	if strings.Contains(out.Display, "- Bump:") || strings.Contains(out.Display, "- Run:") {
		t.Errorf("run bullets must move into the Summary table:\n%s", out.Display)
	}
}

func TestShipStateReport_Disabled(t *testing.T) {
	root := shipReportRoot(t)
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "[automation.report]\nenabled = false\n")
	createShipReportState(t, root, nil)

	result, err := shipState(root, root, ShipStateIn{Action: "report", Detail: map[string]any{
		"branch": shipReportBranch, "write": true,
	}}, fixedClock(testNow))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	skipped, ok := result.(ReportSkippedOut)
	if !ok || !skipped.Skipped || skipped.Written {
		t.Fatalf("expected {skipped:true, written:false}, got %#v", result)
	}
	if _, err := os.Stat(filepath.Join(root, paths.DataDir, "reports")); !os.IsNotExist(err) {
		t.Errorf("disabled report must not create the reports dir, stat err=%v", err)
	}
}

func TestShipStateReport_NoShipStateFails(t *testing.T) {
	root := shipReportRoot(t)
	_, err := shipState(root, root, ShipStateIn{Action: "report", Detail: map[string]any{"branch": shipReportBranch}}, fixedClock(testNow))
	if err == nil {
		t.Fatal("expected an error without ship state")
	}
}

func TestShipStateReport_WriteMD(t *testing.T) {
	root := shipReportRoot(t)
	createShipReportState(t, root, nil)

	out := runShipReport(t, root, map[string]any{"write": true})
	want := filepath.Join(root, paths.DataDir, "reports", "ship-"+out.RunID+"-report.md")
	if out.Path != want || !out.Written {
		t.Fatalf("expected written to %s, got path=%q written=%v", want, out.Path, out.Written)
	}
	body, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if string(body) != out.Display {
		t.Errorf("written file must equal display\nfile:\n%s\ndisplay:\n%s", body, out.Display)
	}
	if !strings.HasPrefix(out.Next, "Report persisted.") {
		t.Errorf("next = %q, want the persisted instruction", out.Next)
	}
	if strings.Contains(string(body), "## Next") || strings.Contains(string(body), "Report persisted.") {
		t.Errorf("written report must not carry the next instruction:\n%s", body)
	}
}

func TestShipStateReport_WriteJSON(t *testing.T) {
	root := shipReportRoot(t)
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "[automation.report]\nformat = \"json\"\n")
	createShipReportState(t, root, nil)

	out := runShipReport(t, root, map[string]any{"write": true})
	if out.Format != "json" {
		t.Fatalf("expected json format from config, got %q", out.Format)
	}
	want := filepath.Join(root, paths.DataDir, "reports", "ship-"+out.RunID+"-report.json")
	if out.Path != want || !out.Written {
		t.Fatalf("expected written to %s, got path=%q written=%v", want, out.Path, out.Written)
	}
	if strings.Contains(out.Display, "\n") || !strings.HasPrefix(out.Display, "Ship run ") {
		t.Errorf("json display must be a one-line summary, got %q", out.Display)
	}
	raw, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var decoded ShipRunReportOut
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("report file is not valid JSON: %v", err)
	}
	if decoded.Branch != shipReportBranch || decoded.RunID != out.RunID || decoded.GuardrailHits == nil {
		t.Errorf("unexpected persisted report: %+v", decoded)
	}

	// detail.format overrides config.
	md := runShipReport(t, root, map[string]any{"format": "md"})
	if md.Format != "md" || !strings.HasPrefix(md.Display, "# Ship run report") {
		t.Errorf("detail.format md must override config json, got format=%q", md.Format)
	}
}

func TestShipStateReport_InvalidFormat(t *testing.T) {
	root := shipReportRoot(t)
	createShipReportState(t, root, nil)
	for name, format := range map[string]any{"unknown": "yaml", "wrong type": 5} {
		t.Run(name, func(t *testing.T) {
			_, err := shipState(root, root, ShipStateIn{Action: "report", Detail: map[string]any{
				"branch": shipReportBranch, "format": format,
			}}, fixedClock(testNow))
			if _, ok := err.(*mcpserver.DomainError); !ok {
				t.Fatalf("expected DomainError, got %T: %v", err, err)
			}
		})
	}
}

func TestShipStateReport_EmptyHealingMarkdownLines(t *testing.T) {
	root := shipReportRoot(t)
	createShipReportState(t, root, nil)

	out := runShipReport(t, root, nil)
	for _, want := range []string{
		"_Plan timing not available — no plan linked to this run._",
		"_No user input during the run._",
		"_Review ledger not available — review did not run or its total was not recorded._",
		"_No findings fixed._",
		"_No harden runs recorded._",
		"_No harden commit._",
		"_No findings deferred._",
		"_No guardrail hits._",
		"_No CLI evidence recorded._",
		"_No decisions recorded._",
		"_No learnings linked to this run._",
	} {
		if !strings.Contains(out.Display, want) {
			t.Errorf("display missing %q:\n%s", want, out.Display)
		}
	}

	headings := []string{"# Ship run report — " + shipReportBranch, "## Summary", "## Plan", "## Steps", "## User input", "## Review ledger", "## Self-healing",
		"### Fixed", "### Hardened", "### Harden commit", "## Deferred", "## Guardrail hits",
		"## CLI evidence", "## Decisions", "## Learnings"}
	last := -1
	for _, h := range headings {
		idx := strings.Index(out.Display, h+"\n")
		if idx < 0 {
			t.Fatalf("display missing heading %q:\n%s", h, out.Display)
		}
		if idx <= last {
			t.Errorf("heading %q out of order", h)
		}
		last = idx
	}
	if strings.Contains(out.Display, "## Next") || strings.Contains(out.Display, "## Planning") {
		t.Errorf("display must not carry ## Next or ## Planning:\n%s", out.Display)
	}
	if out.Next == "" {
		t.Error("out.Next must still be set")
	}
}

func TestShipStateReport_HealingRendered(t *testing.T) {
	root := shipReportRoot(t)
	steps := append(shipReportSteps("skipped"),
		map[string]any{"name": "harden", "status": "completed", "result": "abc1234 chore(harden): add guardrail"})
	createShipReportState(t, root, map[string]any{
		"steps": steps,
		"healing": map[string]any{
			"reviewTotal": map[string]any{"total": 3, "dimensions": 2},
			"fixed": []any{
				map[string]any{"origin": "local-review", "severity": "high", "file": "a.go", "line": 12, "title": "nil deref"},
			},
			"hardened": []any{
				map[string]any{"phase": "done", "trigger": "t1", "classification": "user-code", "skipped": 1,
					"applied": []any{map[string]any{"surface": "guardrails", "action": "add", "targetFile": ".sdlc-v2/config.toml"}}},
			},
		},
		"deferredFindings": []any{
			map[string]any{"severity": "low", "file": "b.go", "line": nil, "title": "naming", "reason": "out-of-scope"},
		},
		"decisions": []any{map[string]any{"step": "review", "decision": "fix all"}},
	})

	out := runShipReport(t, root, nil)
	if out.ReviewLedger == nil || out.ReviewLedger.Total != 3 || out.ReviewLedger.Fixed != 1 || out.ReviewLedger.Unaccounted != 1 {
		t.Errorf("unexpected review ledger: %+v", out.ReviewLedger)
	}
	if out.HardenCommit != "abc1234 chore(harden): add guardrail" {
		t.Errorf("unexpected harden commit %q", out.HardenCommit)
	}
	for _, want := range []string{
		"- [high] a.go:12 — nil deref (local-review)",
		"1 runs, 1 edits applied, 1 skipped.",
		"| t1 | user-code | 1 | 1 |",
		"| guardrails | 1 | .sdlc-v2/config.toml |",
		"- abc1234 chore(harden): add guardrail",
		"- [low] b.go — naming (out-of-scope)",
		"- Deferred (out-of-scope): 1",
		"- review: fix all",
	} {
		if !strings.Contains(out.Display, want) {
			t.Errorf("display missing %q:\n%s", want, out.Display)
		}
	}
}

func TestShipStateReport_InterruptedHardenedRecord(t *testing.T) {
	root := shipReportRoot(t)
	createShipReportState(t, root, map[string]any{
		"healing": map[string]any{
			"hardened": []any{
				map[string]any{"phase": "started", "trigger": "t1", "classification": "user-code", "applied": []any{}, "skipped": 0},
			},
		},
	})

	out := runShipReport(t, root, nil)
	if !strings.Contains(out.Display, "| t1 | user-code | interrupted — edits committed, surface list unknown | 0 |") {
		t.Errorf("started record must render as interrupted:\n%s", out.Display)
	}
	if strings.Contains(out.Display, "| Surface |") || strings.Contains(out.Display, "_No harden runs recorded._") {
		t.Errorf("started record must not render as an empty change list:\n%s", out.Display)
	}
}

func TestShipStateReport_PlanTiming(t *testing.T) {
	absPlan := "/work/tree/plans/feature.md"
	cases := []struct {
		name     string
		planPath string
		worktree string
		wantPlan bool
	}{
		{"absolute match, latest of two wins", absPlan, "/elsewhere", true},
		{"relative planPath joined to worktree", "plans/feature.md", "/work/tree", true},
		{"dot-slash planPath joined to worktree", "./plans/feature.md", "/work/tree", true},
		{"unmatched path", "/work/tree/plans/other.md", "/work/tree", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := shipReportRoot(t)
			createExecState(t, root, shipReportBranch, map[string]any{
				"branch":   shipReportBranch,
				"planPath": tc.planPath,
				"worktree": tc.worktree,
			})
			createShipReportState(t, root, map[string]any{"steps": shipReportSteps("completed")})
			appendPlanRun(t, root, history.RunRecord{Skill: "plan", PlanFile: absPlan,
				StartedAt: "2025-06-14T08:00:00Z", LastModifiedAt: "2025-06-14T08:10:00Z", DurationMs: 600000})
			appendPlanRun(t, root, history.RunRecord{Skill: "ship", Branch: shipReportBranch, DurationMs: 1})
			appendPlanRun(t, root, history.RunRecord{Skill: "plan", PlanFile: absPlan,
				StartedAt: "2025-06-15T08:00:00Z", LastModifiedAt: "2025-06-15T09:30:00Z", DurationMs: 5400000})

			out := runShipReport(t, root, nil)
			if !tc.wantPlan {
				if out.Plan != nil || out.PlanNote != "no plan linked to this run" {
					t.Fatalf("expected null plan with note, got plan=%+v note=%q", out.Plan, out.PlanNote)
				}
				if !strings.Contains(out.Display, "_Plan timing not available — no plan linked to this run._") {
					t.Errorf("display missing unlinked-plan line:\n%s", out.Display)
				}
				return
			}
			want := ShipPlanTiming{PlanFile: absPlan, StartedAt: "2025-06-15T08:00:00Z", LastModifiedAt: "2025-06-15T09:30:00Z", DurationMs: 5400000}
			if out.Plan == nil || *out.Plan != want {
				t.Fatalf("expected latest plan record %+v, got %+v", want, out.Plan)
			}
			if out.PlanNote != "" {
				t.Errorf("planNote must be empty when plan is set, got %q", out.PlanNote)
			}
			for _, line := range []string{
				"- File: " + absPlan,
				"- Planning time: 1h 30m (2025-06-15T08:00:00Z → last edit 2025-06-15T09:30:00Z)",
				"| Plan | time 1h 30m |",
			} {
				if !strings.Contains(out.Display, line) {
					t.Errorf("display missing %q:\n%s", line, out.Display)
				}
			}
		})
	}
}

func TestShipStateReport_WriteMustBeBool(t *testing.T) {
	root := shipReportRoot(t)
	createShipReportState(t, root, nil)
	for _, v := range []any{"true", float64(1)} {
		_, err := shipState(root, root, ShipStateIn{Action: "report", Detail: map[string]any{"branch": shipReportBranch, "write": v}}, fixedClock(testNow))
		msg, _ := requireShipErr(t, err, shipErrDomain)
		if !strings.Contains(msg, "report: detail.write must be a boolean") {
			t.Errorf("write=%#v: message = %q, want the boolean type error", v, msg)
		}
	}
	if _, err := os.Stat(filepath.Join(root, paths.DataDir, "reports")); !os.IsNotExist(err) {
		t.Errorf("a rejected call must write nothing, reports dir stat err = %v", err)
	}
}

func TestShipStateReport_PlanHistoryUnreadable(t *testing.T) {
	root := shipReportRoot(t)
	createExecState(t, root, shipReportBranch, map[string]any{
		"branch": shipReportBranch, "planPath": "/work/tree/plans/feature.md", "worktree": "/work/tree",
	})
	createShipReportState(t, root, map[string]any{"steps": shipReportSteps("completed")})
	// A directory where runs.jsonl should be makes the history read fail
	// with an I/O error, not "no history yet".
	if err := os.MkdirAll(history.NewFileWriter(historyDir(root)).RunsPath(), 0o755); err != nil {
		t.Fatal(err)
	}

	out := runShipReport(t, root, nil)
	if out.Plan != nil || out.PlanNote != shipPlanNoteReadFailed {
		t.Fatalf("plan=%+v note=%q, want nil plan with note %q", out.Plan, out.PlanNote, shipPlanNoteReadFailed)
	}
	if !strings.Contains(out.Display, "_Plan timing not available — "+shipPlanNoteReadFailed+"._") {
		t.Errorf("display must name the read failure, not 'no plan linked':\n%s", out.Display)
	}
	found := false
	for _, raw := range out.Issues {
		m, _ := raw.(map[string]any)
		if s, _ := m["summary"].(string); strings.HasPrefix(s, "Plan history read failed: ") {
			found = true
		}
	}
	if !found {
		t.Errorf("issues = %v, want a 'Plan history read failed' warning", out.Issues)
	}
}

func TestShipStateReport_ConfigReadErrorSurfaced(t *testing.T) {
	configIssue := func(out ShipRunReportOut) bool {
		for _, raw := range out.Issues {
			m, _ := raw.(map[string]any)
			if s, _ := m["summary"].(string); strings.HasPrefix(s, "Config read failed") {
				return true
			}
		}
		return false
	}

	t.Run("valid config adds no issue", func(t *testing.T) {
		root := shipReportRoot(t)
		createShipReportState(t, root, nil)
		if out := runShipReport(t, root, nil); configIssue(out) {
			t.Errorf("issues = %v, want no config issue", out.Issues)
		}
	})
	t.Run("missing config adds no issue", func(t *testing.T) {
		root := t.TempDir()
		createShipReportState(t, root, nil)
		if out := runShipReport(t, root, nil); configIssue(out) {
			t.Errorf("issues = %v, want no config issue when config.toml is absent", out.Issues)
		}
	})
	t.Run("malformed config is surfaced and defaults apply", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), "[[[not toml\n")
		createShipReportState(t, root, nil)
		out := runShipReport(t, root, nil)
		if !configIssue(out) {
			t.Errorf("issues = %v, want a 'Config read failed' warning", out.Issues)
		}
		if out.Format != "md" || out.Skipped {
			t.Errorf("format=%q skipped=%v, want the md default and a rendered report", out.Format, out.Skipped)
		}
	})
}

// TestShipStateReport_WriteFailure pins the I/O failure path of detail.write:
// a file named reports/ blocks the reports directory, so the write fails and
// the call returns an infrastructure error instead of a report.
func TestShipStateReport_WriteFailure(t *testing.T) {
	for _, format := range []string{"md", "json"} {
		t.Run(format, func(t *testing.T) {
			root := shipReportRoot(t)
			createShipReportState(t, root, nil)
			writeFile(t, filepath.Join(root, paths.DataDir, "reports"), "not a directory")
			_, err := shipState(root, root, ShipStateIn{Action: "report", Detail: map[string]any{
				"branch": shipReportBranch, "write": true, "format": format,
			}}, fixedClock(testNow))
			msg, _ := requireShipErr(t, err, shipErrInfra)
			if !strings.Contains(msg, "mkdir reports dir") {
				t.Errorf("message = %q, want the reports-dir failure named", msg)
			}
		})
	}
}

// TestShipStateReport_DisplayRenderedRaw calls report through the registered
// tool, so the result passes the real Markdown renderer: the pre-rendered
// display must appear verbatim (its own "# Ship run report" heading at
// column 0), not wrapped in a code fence.
func TestShipStateReport_DisplayRenderedRaw(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, "feat/raw-report")
	t.Chdir(dir)

	if res, head := callRegisteredShipState(t, map[string]any{"action": "init"}); res.IsError {
		t.Fatalf("init failed: %s", head)
	}
	res, _ := callRegisteredShipState(t, map[string]any{"action": "report"})
	if res.IsError {
		t.Fatalf("report returned an error result")
	}
	text := res.Content[0].(*mcp.TextContent).Text
	const heading = "# Ship run report — feat/raw-report"
	idx := strings.Index(text, "\n"+heading+"\n")
	if idx < 0 {
		t.Fatalf("rendered result has no column-0 %q line:\n%s", heading, text)
	}
	if before := text[:idx]; strings.HasSuffix(strings.TrimRight(before, "\n"), "```") {
		t.Errorf("display is fenced, want it emitted raw:\n%s", text)
	}
}

// shipReportPlanFile is the plan file the planning/timeline fixtures link.
const shipReportPlanFile = "/work/tree/plans/feature.md"

// createPlanRunState writes a plan run state file with data.
func createPlanRunState(t *testing.T, root string, data map[string]any) {
	t.Helper()
	st, err := state.Init(root, "plan", shipReportBranch, "")
	if err != nil {
		t.Fatalf("create plan state: %v", err)
	}
	for k, v := range data {
		st.Data[k] = v
	}
	if err := state.Write(st); err != nil {
		t.Fatalf("write plan state: %v", err)
	}
}

// createLinkedExecState writes an execute state whose planPath is
// shipReportPlanFile, plus extra.
func createLinkedExecState(t *testing.T, root string, extra map[string]any) {
	t.Helper()
	data := map[string]any{"branch": shipReportBranch, "planPath": shipReportPlanFile}
	for k, v := range extra {
		data[k] = v
	}
	createExecState(t, root, shipReportBranch, data)
}

func TestShipReportPlanning(t *testing.T) {
	baseSync := map[string]any{
		"key": "base-sync-method", "choice": "merge", "reason": "keeps wave SHAs",
		"rejected": []any{map[string]any{"option": "rebase", "why": "rewrites SHAs"}},
		"at":       "2026-10-01T09:50:00Z",
	}
	integrity := map[string]any{"done": "2026-10-01T10:00:00Z", "skillInvoked": "2026-10-01T09:00:00Z"}
	cases := []struct {
		name         string
		planRun      map[string]any // nil = no plan run state
		wantNil      bool
		wantNote     string
		wantDecision int
		wantLines    []string
		notLines     []string
	}{
		{
			name:     "no plan run state",
			wantNil:  true,
			wantNote: "plan run state not found",
			wantLines: []string{
				"## Plan\n\n_Plan timing not available — no plan linked to this run._\n\n_Plan run state not found — no planning data._\n",
			},
			notLines: []string{"| Decision |"},
		},
		{
			name: "decision with rejected alternative",
			planRun: map[string]any{
				"planFilePath":      shipReportPlanFile,
				"planIntegrity":     integrity,
				"criticalDecisions": []any{baseSync},
			},
			wantDecision: 1,
			wantLines: []string{
				"## Plan\n\n- File: " + shipReportPlanFile + "\n\n_Plan timing not available — no plan linked to this run._\n\n| Decision | Chosen | Rejected | Reason |\n|---|---|---|---|\n",
				"| base-sync-method | merge | rebase: rewrites SHAs | keeps wave SHAs |",
				"| Plan | decisions 1 |",
			},
		},
		{
			name: "several rejected joined, empty rejected renders dash",
			planRun: map[string]any{
				"planFilePath": shipReportPlanFile,
				"criticalDecisions": []any{
					map[string]any{"key": "store", "choice": "sqlite", "reason": "local", "rejected": []any{
						map[string]any{"option": "postgres", "why": "needs a server"},
						map[string]any{"option": "files", "why": "no a|b queries"},
					}},
					map[string]any{"key": "lang", "choice": "go", "reason": "repo", "rejected": []any{}},
				},
			},
			wantDecision: 2,
			wantLines: []string{
				`| store | sqlite | postgres: needs a server; files: no a\|b queries | local |`,
				"| lang | go | — | repo |",
			},
		},
		{
			name:         "zero decisions keeps milestones",
			planRun:      map[string]any{"planFilePath": shipReportPlanFile, "planIntegrity": integrity},
			wantDecision: 0,
			wantLines: []string{
				"_No critical decisions recorded._",
			},
			notLines: []string{"| Decision |", "- done:", "- skillInvoked:"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := shipReportRoot(t)
			createLinkedExecState(t, root, nil)
			if tc.planRun != nil {
				createPlanRunState(t, root, tc.planRun)
			}
			createShipReportState(t, root, map[string]any{"steps": shipReportSteps("completed")})

			out := runShipReport(t, root, nil)
			if tc.wantNil {
				if out.Planning != nil || out.PlanningNote != tc.wantNote {
					t.Fatalf("expected null planning with note %q, got planning=%+v note=%q", tc.wantNote, out.Planning, out.PlanningNote)
				}
				raw, err := json.Marshal(out)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				if !strings.Contains(string(raw), `"planning":null`) {
					t.Errorf("marshaled report missing \"planning\":null: %s", raw)
				}
			} else {
				if out.Planning == nil || out.PlanningNote != "" {
					t.Fatalf("expected planning with no note, got planning=%+v note=%q", out.Planning, out.PlanningNote)
				}
				if out.Planning.PlanFile != shipReportPlanFile || len(out.Planning.Decisions) != tc.wantDecision {
					t.Errorf("unexpected planning: %+v", out.Planning)
				}
				if out.Planning.Decisions == nil || out.Planning.Milestones == nil {
					t.Errorf("decisions and milestones must be non-nil: %+v", out.Planning)
				}
			}
			for _, line := range tc.wantLines {
				if !strings.Contains(out.Display, line) {
					t.Errorf("display missing %q:\n%s", line, out.Display)
				}
			}
			for _, line := range tc.notLines {
				if strings.Contains(out.Display, line) {
					t.Errorf("display must not contain %q:\n%s", line, out.Display)
				}
			}
			if strings.Contains(out.Display, "## Planning") || strings.Count(out.Display, "- File: ") > 1 {
				t.Errorf("planning must merge into one ## Plan section with one file line:\n%s", out.Display)
			}
		})
	}

	t.Run("planning null when execute step not completed", func(t *testing.T) {
		root := shipReportRoot(t)
		createLinkedExecState(t, root, nil)
		createPlanRunState(t, root, map[string]any{"planFilePath": shipReportPlanFile, "criticalDecisions": []any{baseSync}})
		createShipReportState(t, root, map[string]any{"steps": shipReportSteps("skipped")})
		if out := runShipReport(t, root, nil); out.Planning != nil || out.PlanningNote != "plan run state not found" {
			t.Errorf("expected null planning, got planning=%+v note=%q", out.Planning, out.PlanningNote)
		}
	})
}

func TestShipReportTimeline(t *testing.T) {
	t.Run("spec scenario: timeline order", func(t *testing.T) {
		root := shipReportRoot(t)
		createPlanRunState(t, root, map[string]any{
			"planFilePath":  shipReportPlanFile,
			"planIntegrity": map[string]any{"done": "2026-10-01T10:00:00Z"},
		})
		createLinkedExecState(t, root, map[string]any{"waves": []any{
			map[string]any{"number": float64(1), "status": "running", "startedAt": "2026-10-01T10:05:00Z"},
		}})
		createShipReportState(t, root, map[string]any{"steps": []any{
			map[string]any{"name": "execute", "status": "completed"},
			map[string]any{"name": "pr", "status": "in_progress", "startedAt": "2026-10-01T10:30:00Z"},
		}})

		out := runShipReport(t, root, nil)
		want := []TimelineEvent{
			{At: "2026-10-01T10:00:00Z", Phase: "plan", Event: "done"},
			{At: "2026-10-01T10:05:00Z", Phase: "execute", Event: "wave 1 started"},
			{At: "2026-10-01T10:30:00Z", Phase: "ship", Event: "pr started"},
		}
		if len(out.Timeline) != len(want) {
			t.Fatalf("timeline = %+v, want %+v", out.Timeline, want)
		}
		for i := range want {
			if out.Timeline[i] != want[i] {
				t.Errorf("timeline[%d] = %+v, want %+v", i, out.Timeline[i], want[i])
			}
		}
		if !strings.Contains(out.Display, "## Timeline\n\n| At | Phase | Event |\n|---|---|---|\n| 2026-10-01T10:00:00Z | plan | done |\n") {
			t.Errorf("display missing timeline table:\n%s", out.Display)
		}
		if st, tl := strings.Index(out.Display, "## Steps"), strings.Index(out.Display, "## Timeline"); st < 0 || tl < st {
			t.Errorf("## Timeline must follow ## Steps:\n%s", out.Display)
		}
	})

	t.Run("merges every source sorted by at", func(t *testing.T) {
		root := shipReportRoot(t)
		createPlanRunState(t, root, map[string]any{
			"planFilePath":  shipReportPlanFile,
			"planIntegrity": map[string]any{"skillInvoked": "2026-10-01T09:00:00Z", "done": "2026-10-01T10:00:00Z"},
			"criticalDecisions": []any{
				map[string]any{"key": "base-sync-method", "choice": "merge", "at": "2026-10-01T09:30:00Z"},
				map[string]any{"key": "untimed", "choice": "x"},
			},
		})
		createLinkedExecState(t, root, map[string]any{
			"waves": []any{
				map[string]any{"number": float64(1), "status": "completed", "startedAt": "2026-10-01T10:05:00Z", "completedAt": "2026-10-01T10:15:00Z"},
			},
			"baseSyncs": []any{
				map[string]any{"wave": float64(1), "status": "merged", "base": "main", "behind": float64(2), "sha": "ab12", "at": "2026-10-01T10:16:00Z"},
			},
		})
		createShipReportState(t, root, map[string]any{
			"steps": []any{
				map[string]any{"name": "execute", "status": "completed", "startedAt": "2026-10-01T10:04:00Z", "completedAt": "2026-10-01T10:20:00Z"},
				map[string]any{"name": "pr", "status": "completed", "startedAt": "2026-10-01T10:30:00Z", "completedAt": "2026-10-01T10:32:00Z"},
			},
			"decisions": []any{
				map[string]any{"step": "review", "decision": "defer nits", "at": "2026-10-01T10:25:00Z"},
			},
		})

		out := runShipReport(t, root, nil)
		want := []string{
			"2026-10-01T09:00:00Z plan skillInvoked",
			"2026-10-01T09:30:00Z plan decision base-sync-method: merge",
			"2026-10-01T10:00:00Z plan done",
			"2026-10-01T10:04:00Z ship execute started",
			"2026-10-01T10:05:00Z execute wave 1 started",
			"2026-10-01T10:15:00Z execute wave 1 completed",
			"2026-10-01T10:16:00Z execute base-sync wave 1: merged",
			"2026-10-01T10:20:00Z ship execute completed",
			"2026-10-01T10:25:00Z ship decision review: defer nits",
			"2026-10-01T10:30:00Z ship pr started",
			"2026-10-01T10:32:00Z ship pr completed",
		}
		got := make([]string, len(out.Timeline))
		for i, e := range out.Timeline {
			got[i] = e.At + " " + e.Phase + " " + e.Event
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("timeline:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})

	t.Run("no timed events", func(t *testing.T) {
		root := shipReportRoot(t)
		createShipReportState(t, root, map[string]any{"steps": []any{
			map[string]any{"name": "execute", "status": "skipped"},
		}})

		out := runShipReport(t, root, nil)
		if out.Timeline == nil || len(out.Timeline) != 0 {
			t.Fatalf("timeline must be an empty non-nil slice, got %#v", out.Timeline)
		}
		if !strings.Contains(out.Display, "## Timeline\n\n_No timed events._\n") {
			t.Errorf("display missing empty timeline line:\n%s", out.Display)
		}
		if strings.Contains(out.Display, "| At | Phase | Event |") {
			t.Errorf("display must not render a header-only timeline table:\n%s", out.Display)
		}
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(raw), `"timeline":[]`) {
			t.Errorf("marshaled report missing \"timeline\":[]: %s", raw)
		}
	})
}

func TestShipReportShort(t *testing.T) {
	cases := []struct {
		name, in string
		max      int
		want     string
	}{
		{"empty", "", 200, ""},
		{"blank only", " \n\t\n", 200, ""},
		{"whitespace runs collapse", "  a \t  b  ", 200, "a b"},
		{"later lines dropped", "first\nsecond\nthird", 200, "first …"},
		{"leading and trailing blank lines are not dropped text", "\n\n  first  \n\n", 200, "first"},
		{"CRLF lines", "a\r\nb", 200, "a …"},
		{"cut at max runes", strings.Repeat("x", 250), 200, strings.Repeat("x", 200) + "…"},
		{"cut drops trailing space", "ab cd", 3, "ab…"},
		{"cut counts runes not bytes", "ąęść", 2, "ąę…"},
		{"cut wins over dropped lines", "abcdef\nmore", 3, "abc…"},
		{"exact max not cut", "abc", 3, "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shipReportShort(tc.in, tc.max); got != tc.want {
				t.Errorf("shipReportShort(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestShipReportCode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a", "`a`"},
		{"echo `date`", "`` echo `date` ``"},
		{"`x` y", "`` `x` y ``"},
		{"a ``b`` c", "```a ``b`` c```"},
		{"line one\nline two\nline three", "`line one …`"},
		{"", "`(empty)`"},
		{strings.Repeat("y", 130), "`" + strings.Repeat("y", 120) + "…`"},
	}
	for _, tc := range cases {
		if got := shipReportCode(tc.in); got != tc.want {
			t.Errorf("shipReportCode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShipReportCommandName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"grep -n x f", "grep"},
		{"git -C /r diff --stat", "git diff"},
		{"cd /r && go test ./...", "go test"},
		{"FOO=1 task check", "task check"},
		{"task 2>&1", "task"},
		{"task 2>&1 | grep x", "task"},
		{"/usr/bin/sed -n 1p", "sed"},
		{"", "(empty)"},
		{"   ", "(empty)"},
		{"A=1 B=2 cd /r && git --no-pager log -5", "git log"},
		{"gh pr view 5", "gh pr"},
		{"npm run build", "npm run"},
		{"openspec validate x", "openspec validate"},
		{"go", "go"},
		{"cd /r", "cd"},
		{"FOO=1", "FOO=1"},
		{"cat > /tmp/t.sh << 'SCRIPT'\n#!/bin/bash\nSCRIPT", "cat"},
	}
	for _, tc := range cases {
		if got := shipReportCommandName(tc.in); got != tc.want {
			t.Errorf("shipReportCommandName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShipReportRelPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/r/repo/.sdlc-v2/review-dimensions/a.md", ".sdlc-v2/review-dimensions/a.md"},
		{"/Users/r/repo/.github/instructions/a.md", ".github/instructions/a.md"},
		{".sdlc-v2/config.toml", ".sdlc-v2/config.toml"},
		{"/a/.github/b/.sdlc-v2/c", ".github/b/.sdlc-v2/c"},
		{"/a/foo.sdlc-v2/x", "/a/foo.sdlc-v2/x"},
		{"internal/tools/x.go", "internal/tools/x.go"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := shipReportRelPath(tc.in); got != tc.want {
			t.Errorf("shipReportRelPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// shipReportUserInputEntry appends one user-input evidence entry inside the
// report window.
func shipReportUserInputEntry(t *testing.T, root, ts, step, pipelineName, text string, wave *int) {
	t.Helper()
	if err := appendUserInput(root, UserInputEntry{
		Timestamp: ts, Pipeline: pipelineName, Step: step, Wave: wave,
		Branch: shipReportBranch, Text: text,
	}); err != nil {
		t.Fatalf("append user input: %v", err)
	}
}

// shipReportEvidence appends one CLI evidence entry inside the report window.
func shipReportEvidence(t *testing.T, root, step, pipelineName, command string, exit int) {
	t.Helper()
	if err := appendCLIEvidence(root, CLIEvidenceEntry{
		Timestamp: "2025-06-15T10:05:00Z", Pipeline: pipelineName, Step: step,
		Branch: shipReportBranch, Command: command, ExitCode: exit,
	}); err != nil {
		t.Fatalf("append evidence: %v", err)
	}
}

// shipReportSection returns the lines of the "## title" section, without
// the heading itself, up to the next "## " heading.
func shipReportSection(display, title string) []string {
	var out []string
	in := false
	for _, l := range strings.Split(display, "\n") {
		if strings.HasPrefix(l, "## ") {
			if in {
				break
			}
			in = l == "## "+title
			continue
		}
		if in {
			out = append(out, l)
		}
	}
	return out
}

// shipReportListLines counts the lines of a section that start with "- ".
func shipReportListLines(lines []string) int {
	n := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "- ") {
			n++
		}
	}
	return n
}

func TestShipReportCLIEvidence(t *testing.T) {
	t.Run("sanitized failures, counts only, no successful command text", func(t *testing.T) {
		root := shipReportRoot(t)
		var heredoc strings.Builder
		heredoc.WriteString("echo `pwd` | tee out.txt <<'SCRIPT'\n")
		for i := 0; i < 50; i++ {
			fmt.Fprintf(&heredoc, "# heading %d\n- item `x` | y\n", i)
		}
		heredoc.WriteString("SCRIPT")
		shipReportEvidence(t, root, "review", "ship", "go test ./...", 1)
		shipReportEvidence(t, root, "pr", "ship", heredoc.String(), 1)
		shipReportEvidence(t, root, "", "ship", "echo SUCCESS_MARKER_xyz", 0)
		shipReportEvidence(t, root, "pr", "ship", "git -C /r diff --stat", 0)
		createShipReportState(t, root, map[string]any{
			"issues": []any{map[string]any{"severity": "warning", "summary": "first issue line\n## not a heading"}},
			"healing": map[string]any{"fixed": []any{
				map[string]any{"origin": "local-review", "severity": "high", "file": "a.go", "line": 1, "title": "first line\n# second line"},
			}},
			"deferredFindings": []any{
				map[string]any{"severity": "low", "file": "b.go", "title": "deferred one\n- not a list item", "reason": "wont-fix"},
			},
		})

		out := runShipReport(t, root, nil)
		section := strings.Join(shipReportSection(out.Display, "CLI evidence"), "\n")
		want := "\n4 commands, 2 failed.\n\n" +
			"| Step | Commands | Failed |\n|---|---|---|\n| review | 1 | 1 |\n| pr | 2 | 1 |\n| ship | 1 | 0 |\n\n" +
			"| Command | Runs | Failed |\n|---|---|---|\n| echo | 2 | 1 |\n| go test | 1 | 1 |\n| git diff | 1 | 0 |\n\n" +
			"Failed commands:\n" +
			"- `go test ./...` — exit 1 (review)\n" +
			"- ``echo `pwd` | tee out.txt <<'SCRIPT' …`` — exit 1 (pr)\n\n" +
			"Full log: .sdlc-v2/evidence/cli-executions.jsonl\n"
		if section != want {
			t.Errorf("CLI evidence section:\n%s\nwant:\n%s", section, want)
		}
		if strings.Contains(out.Display, "SUCCESS_MARKER_xyz") || strings.Contains(out.Display, "--stat") {
			t.Errorf("successful command text must not appear in display:\n%s", out.Display)
		}
		for _, line := range []string{
			"- [warning] first issue line …",
			"- [high] a.go:1 — first line … (local-review)",
			"- [low] b.go — deferred one … (wont-fix)",
			"| CLI commands | total 4 · failed 2 |",
		} {
			if !strings.Contains(out.Display, line+"\n") {
				t.Errorf("display missing line %q:\n%s", line, out.Display)
			}
		}

		// Every line stays in its section: only the report's own headings
		// start with "#", and no stored text adds list lines.
		var headings []string
		for _, l := range strings.Split(out.Display, "\n") {
			if strings.HasPrefix(l, "#") {
				headings = append(headings, l)
			}
		}
		wantHeadings := []string{"# Ship run report — " + shipReportBranch, "## Summary", "## Plan", "## Steps", "## User input", "## Timeline",
			"## Review ledger", "## Self-healing", "### Fixed", "### Hardened", "### Harden commit", "## Deferred",
			"## Guardrail hits", "## CLI evidence", "## Decisions", "## Learnings"}
		if strings.Join(headings, "\n") != strings.Join(wantHeadings, "\n") {
			t.Errorf("headings:\n%s\nwant:\n%s", strings.Join(headings, "\n"), strings.Join(wantHeadings, "\n"))
		}
		for title, n := range map[string]int{"CLI evidence": 2, "Deferred": 1, "Steps": 3} {
			if got := shipReportListLines(shipReportSection(out.Display, title)); got != n {
				t.Errorf("## %s has %d list lines, want %d", title, got, n)
			}
		}
	})

	t.Run("no failures", func(t *testing.T) {
		root := shipReportRoot(t)
		shipReportEvidence(t, root, "pr", "ship", "ls -la", 0)
		createShipReportState(t, root, nil)
		out := runShipReport(t, root, nil)
		section := strings.Join(shipReportSection(out.Display, "CLI evidence"), "\n")
		if !strings.Contains(section, "1 commands, 0 failed.\n") || !strings.Contains(section, "\n_No failed commands._\n") {
			t.Errorf("section missing count line or empty failed line:\n%s", section)
		}
		if strings.Contains(section, "Failed commands:") || strings.Contains(section, "evidence read limit") {
			t.Errorf("section must not list failures or the cap note:\n%s", section)
		}
	})

	t.Run("cap note, other row and failed overflow", func(t *testing.T) {
		root := shipReportRoot(t)
		for i := 0; i < maxCLIEvidenceInWindow; i++ {
			exit := 0
			if i < 30 {
				exit = 1
			}
			shipReportEvidence(t, root, "review", "ship", fmt.Sprintf("tool%02d arg", i%20), exit)
		}
		createShipReportState(t, root, nil)
		out := runShipReport(t, root, nil)
		lines := shipReportSection(out.Display, "CLI evidence")
		section := strings.Join(lines, "\n")
		for _, want := range []string{
			"200 commands, 30 failed. Counts cover the latest 200 commands only (evidence read limit).\n",
			"| review | 200 | 30 |\n",
			"| tool00 | 10 | 2 |\n",
			"| tool14 | 10 | 1 |\n| other (5 kinds) | 50 | 5 |\n",
			"- `tool19 arg` — exit 1 (review)\n- … 10 more\n",
		} {
			if !strings.Contains(section, want) {
				t.Errorf("section missing %q:\n%s", want, section)
			}
		}
		if strings.Contains(section, "| tool15 |") {
			t.Errorf("groups past the first %d must fold into the other row:\n%s", shipReportCommandRows, section)
		}
		if got := shipReportListLines(lines); got != shipReportFailedCap+1 {
			t.Errorf("failed list has %d lines, want %d + the overflow line", got, shipReportFailedCap)
		}
		if !strings.Contains(out.Display, "| CLI commands | total 200 · failed 30 · latest 200 only |") {
			t.Errorf("summary missing the capped CLI row:\n%s", out.Display)
		}
	})
}

func TestShipReportSummary(t *testing.T) {
	summaryRows := func(t *testing.T, display string) map[string]string {
		t.Helper()
		rows := map[string]string{}
		var order []string
		for _, l := range shipReportSection(display, "Summary") {
			if !strings.HasPrefix(l, "| ") || l == "| Area | Result |" {
				continue
			}
			cells := strings.SplitN(strings.Trim(l, "| "), " | ", 2)
			rows[cells[0]] = cells[1]
			order = append(order, cells[0])
		}
		want := []string{"Run", "Plan", "Steps", "User input", "Execution", "Review", "Fixed by severity", "Hardened",
			"Deferred", "Guardrail hits", "CLI commands", "Decisions", "Learnings"}
		if strings.Join(order, ",") != strings.Join(want, ",") {
			t.Fatalf("summary rows %v, want %v", order, want)
		}
		if !strings.HasPrefix(display, "# Ship run report — "+shipReportBranch+"\n\n## Summary\n\n| Area | Result |\n|---|---|\n") {
			t.Errorf("## Summary must follow the title:\n%s", display)
		}
		return rows
	}

	t.Run("missing data", func(t *testing.T) {
		root := shipReportRoot(t)
		createShipReportState(t, root, nil)
		out := runShipReport(t, root, nil)
		rows := summaryRows(t, out.Display)
		want := map[string]string{
			"Plan": "—", "Steps": "completed 1 of 2", "User input": "none", "Execution": "not run", "Review": "—",
			"Fixed by severity": "none", "Hardened": "none", "Deferred": "0", "Guardrail hits": "0",
			"CLI commands": "total 0 · failed 0", "Decisions": "0", "Learnings": "0",
		}
		for area, result := range want {
			if rows[area] != result {
				t.Errorf("row %s = %q, want %q", area, rows[area], result)
			}
		}
		if !strings.HasPrefix(rows["Run"], out.RunID) {
			t.Errorf("run row %q must start with the run id %q", rows["Run"], out.RunID)
		}
	})

	t.Run("numbers match the sections", func(t *testing.T) {
		root := shipReportRoot(t)
		shipReportEvidence(t, root, "pr", "ship", "go build ./...", 2)
		shipReportEvidence(t, root, "pr", "ship", "go vet ./...", 0)
		steps := append(shipReportSteps("skipped"), map[string]any{"name": "await-remote-review", "status": "completed"})
		createShipReportState(t, root, map[string]any{
			"steps": steps,
			"healing": map[string]any{
				"reviewTotal": map[string]any{"total": 4},
				"fixed": []any{
					map[string]any{"origin": "local-review", "severity": "low", "file": "l.go", "title": "low one"},
					map[string]any{"origin": "local-review", "severity": "high", "file": "h.go", "line": 3, "title": "high one"},
					map[string]any{"origin": "remote-review", "severity": "medium", "file": "m.go", "title": "medium one"},
					map[string]any{"origin": "remote-review", "severity": "High", "file": "h2.go", "title": "high two"},
					map[string]any{"origin": "local-review", "severity": "critical", "file": "c.go", "title": "critical one"},
					map[string]any{"origin": "local-review", "severity": "info", "file": "i.go", "title": "info one"},
					map[string]any{"origin": "local-review", "severity": "bogus", "file": "u.go", "title": "unknown one"},
				},
				"hardened": []any{
					map[string]any{"phase": "done", "trigger": "[high] first\nsecond", "classification": "user-code", "skipped": 1, "applied": []any{
						map[string]any{"surface": "review-dimensions", "action": "strengthen", "targetFile": "/Users/r/repo/.sdlc-v2/review-dimensions/a.md"},
						map[string]any{"surface": "review-dimensions", "action": "strengthen", "targetFile": "/Users/r/repo/.sdlc-v2/review-dimensions/a.md"},
						map[string]any{"surface": "copilot-instructions", "action": "add", "targetFile": "/Users/r/repo/.github/instructions/b.md"},
					}},
					map[string]any{"phase": "done", "trigger": "t2", "classification": "plugin-defect", "skipped": 2, "applied": []any{}},
				},
			},
			"deferredFindings": []any{map[string]any{"severity": "low", "file": "d.go", "title": "later", "reason": "wont-fix"}},
			"decisions": []any{
				map[string]any{"step": "review", "decision": "fix all"},
				map[string]any{"step": "await-remote-review", "decision": "  "},
			},
		})

		out := runShipReport(t, root, nil)
		rows := summaryRows(t, out.Display)
		want := map[string]string{
			"Steps":             "completed 2 of 3 · human waits 1",
			"Review":            "total 4 · fixed 5 · deferred 1 · unaccounted -2",
			"Fixed by severity": "critical 1 · high 2 · medium 1 · low 1 · info 1 · unknown 1",
			"Hardened":          "runs 2 · edits applied 3 · skipped 3",
			"Deferred":          "1",
			"CLI commands":      "total 2 · failed 1",
			"Decisions":         "1",
		}
		for area, result := range want {
			if rows[area] != result {
				t.Errorf("row %s = %q, want %q", area, rows[area], result)
			}
		}
		for _, block := range []string{
			"### Fixed\n\n7 findings fixed.\n\n" +
				"| Severity | local-review | remote-review | Total |\n|---|---|---|---|\n" +
				"| critical | 1 | 0 | 1 |\n| high | 1 | 1 | 2 |\n| medium | 0 | 1 | 1 |\n| low | 1 | 0 | 1 |\n| info | 1 | 0 | 1 |\n| unknown | 1 | 0 | 1 |\n\n" +
				"Critical, high and medium:\n" +
				"- [critical] c.go — critical one (local-review)\n" +
				"- [high] h.go:3 — high one (local-review)\n" +
				"- [high] h2.go — high two (remote-review)\n" +
				"- [medium] m.go — medium one (remote-review)\n\n### Hardened",
			"### Hardened\n\n2 runs, 3 edits applied, 3 skipped.\n\n" +
				"| Trigger | Class | Applied | Skipped |\n|---|---|---|---|\n" +
				"| [high] first … | user-code | 3 | 1 |\n| t2 | plugin-defect | 0 | 2 |\n\n" +
				"| Surface | Edits | Files |\n|---|---|---|\n" +
				"| review-dimensions | 2 | .sdlc-v2/review-dimensions/a.md |\n" +
				"| copilot-instructions | 1 | .github/instructions/b.md |\n",
			"- Unaccounted: -2 — ledger mismatch: fixes plus deferrals exceed the review total\n",
		} {
			if !strings.Contains(out.Display, block) {
				t.Errorf("display missing block:\n%s\n--- display:\n%s", block, out.Display)
			}
		}
		if !strings.Contains(out.Display, "\n2 commands, 1 failed.\n") {
			t.Errorf("CLI count line must match the summary row:\n%s", out.Display)
		}
		if strings.Contains(out.Display, "- [low] l.go") || strings.Contains(out.Display, "- [info]") {
			t.Errorf("low and info findings must stay in the count table only:\n%s", out.Display)
		}
	})
}

func TestShipReportExecutionWaves(t *testing.T) {
	root := shipReportRoot(t)
	createExecState(t, root, shipReportBranch, map[string]any{
		"branch": shipReportBranch, "totalTasks": 3,
		"waves": []any{
			map[string]any{"number": float64(1), "status": "completed", "startedAt": "2025-06-15T10:01:00Z", "completedAt": "2025-06-15T10:16:37Z",
				"committedSha": "4eca939bb6836f614ab5b09e4e1e0cff11ae8974",
				"tasks":        []any{map[string]any{"id": "1"}, map[string]any{"id": "2"}}},
			map[string]any{"number": float64(2), "status": "failed", "tasks": []any{map[string]any{"id": "3"}}},
		},
	})
	createShipReportState(t, root, map[string]any{"steps": shipReportSteps("completed")})

	out := runShipReport(t, root, nil)
	want := "| Wave | Status | Tasks | Duration | Commit |\n|---|---|---|---|---|\n" +
		"| 1 | completed | 2 | 15m 37s | 4eca939 |\n| 2 | failed | 1 | — | — |\n\n- Issues: "
	if !strings.Contains(out.Display, want) {
		t.Errorf("display missing wave table:\n%s", out.Display)
	}
	if !strings.Contains(out.Display, "| Execution | tasks 0 of 3 completed · waves 2 · ") {
		t.Errorf("summary missing the execution row:\n%s", out.Display)
	}

	t.Run("no waves", func(t *testing.T) {
		root := shipReportRoot(t)
		createExecState(t, root, shipReportBranch, map[string]any{"branch": shipReportBranch})
		createShipReportState(t, root, map[string]any{"steps": shipReportSteps("completed")})
		out := runShipReport(t, root, nil)
		if !strings.Contains(out.Display, "\n\n_No waves recorded._\n\n- Issues: ") || strings.Contains(out.Display, "| Wave |") {
			t.Errorf("display missing the empty-wave line:\n%s", out.Display)
		}
	})
}

func TestShipReportDecisions(t *testing.T) {
	long := strings.Repeat("d", 250)
	root := shipReportRoot(t)
	createPlanRunState(t, root, map[string]any{
		"planFilePath": shipReportPlanFile,
		"criticalDecisions": []any{
			map[string]any{"key": "blank", "choice": " ", "at": "2026-10-01T09:10:00Z"},
			map[string]any{"key": "store", "choice": strings.Repeat("c", 130) + "\nsecond", "reason": "r", "at": "2026-10-01T09:20:00Z"},
		},
	})
	createLinkedExecState(t, root, nil)
	createShipReportState(t, root, map[string]any{
		"steps": []any{map[string]any{"name": "execute", "status": "completed"}},
		"decisions": []any{
			map[string]any{"step": "review", "decision": long, "at": "2026-10-01T10:00:00Z"},
			map[string]any{"step": "await-remote-review", "decision": "", "at": "2026-10-01T10:01:00Z"},
			map[string]any{"step": "pr", "decision": "multi\nline", "at": "2026-10-01T10:02:00Z"},
		},
	})

	out := runShipReport(t, root, nil)
	section := strings.Join(shipReportSection(out.Display, "Decisions"), "\n")
	wantSection := "\n- review: " + strings.Repeat("d", 200) + "…\n- pr: multi …\n"
	if section != wantSection {
		t.Errorf("Decisions section:\n%q\nwant:\n%q", section, wantSection)
	}
	if len(out.Decisions) != 3 {
		t.Errorf("out.Decisions must keep every entry, got %d", len(out.Decisions))
	}
	for _, e := range out.Timeline {
		if strings.HasPrefix(e.Event, "decision blank") || strings.HasPrefix(e.Event, "decision await-remote-review") {
			t.Errorf("blank decision must not become a timeline event: %+v", e)
		}
	}
	for _, line := range []string{
		"| 2026-10-01T10:00:00Z | ship | decision review: " + strings.Repeat("d", shipReportTextMax-len("decision review: ")) + "… |",
		"| store | " + strings.Repeat("c", 120) + "… | — | r |",
		"| Plan | decisions 2 |",
		"| Decisions | 2 |",
	} {
		if !strings.Contains(out.Display, line+"\n") {
			t.Errorf("display missing %q:\n%s", line, out.Display)
		}
	}
}

func TestShipReportUserInput(t *testing.T) {
	t.Run("no entries", func(t *testing.T) {
		root := shipReportRoot(t)
		createShipReportState(t, root, nil)

		out := runShipReport(t, root, nil)
		if out.UserInputs == nil || len(out.UserInputs) != 0 {
			t.Fatalf("userInputs must be an empty non-nil slice, got %#v", out.UserInputs)
		}
		if !strings.Contains(out.Display, "## User input\n\n_No user input during the run._\n") {
			t.Errorf("display missing empty user-input line:\n%s", out.Display)
		}
		if !strings.Contains(out.Display, "| User input | none |") {
			t.Errorf("summary missing the none row:\n%s", out.Display)
		}
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(raw), `"userInputs":[]`) {
			t.Errorf("marshaled report missing \"userInputs\":[]: %s", raw)
		}
	})

	t.Run("rendered rows, step cell forms, oldest first, multi-line truncation", func(t *testing.T) {
		root := shipReportRoot(t)
		wave1 := 1
		shipReportUserInputEntry(t, root, "2025-06-15T10:05:00Z", "review", "ship", "fix the thing", nil)
		shipReportUserInputEntry(t, root, "2025-06-15T10:06:00Z", "", "execute", "go ahead | proceed\nsecond line\nthird line", &wave1)
		shipReportUserInputEntry(t, root, "2025-06-15T10:07:00Z", "", "ship", "no step no wave", nil)
		createShipReportState(t, root, nil)

		out := runShipReport(t, root, nil)
		if len(out.UserInputs) != 3 {
			t.Fatalf("expected 3 user inputs, got %d: %+v", len(out.UserInputs), out.UserInputs)
		}
		want := "## User input\n\n3 prompts typed during the run.\n\n" +
			"| At | Step | Text |\n|---|---|---|\n" +
			"| 2025-06-15T10:05:00Z | review | fix the thing |\n" +
			`| 2025-06-15T10:06:00Z | wave 1 | go ahead \| proceed … |` + "\n" +
			"| 2025-06-15T10:07:00Z | — | no step no wave |\n"
		if !strings.Contains(out.Display, want) {
			t.Errorf("display missing the User input section:\nwant:\n%s\ngot:\n%s", want, out.Display)
		}
		if !strings.Contains(out.Display, "| User input | prompts 3 |") {
			t.Errorf("summary missing the prompts row:\n%s", out.Display)
		}
	})

	t.Run("read window cap note", func(t *testing.T) {
		root := shipReportRoot(t)
		for i := 0; i < maxUserInputInWindow; i++ {
			ts := fmt.Sprintf("2025-06-15T10:%02d:00Z", i%60)
			shipReportUserInputEntry(t, root, ts, "pr", "ship", fmt.Sprintf("prompt %d", i), nil)
		}
		createShipReportState(t, root, nil)

		out := runShipReport(t, root, nil)
		if len(out.UserInputs) != maxUserInputInWindow {
			t.Fatalf("expected %d user inputs, got %d", maxUserInputInWindow, len(out.UserInputs))
		}
		if !strings.Contains(out.Display, "100 prompts typed during the run. Shows the latest 100 prompts only.\n") {
			t.Errorf("display missing the read-window cap note:\n%s", out.Display)
		}
	})

	t.Run("read error surfaced as a cross-read warning", func(t *testing.T) {
		root := shipReportRoot(t)
		// A directory where user-inputs.jsonl should be makes the read fail
		// with an I/O error, not "file not found".
		if err := os.MkdirAll(userInputPath(root), 0o755); err != nil {
			t.Fatal(err)
		}
		createShipReportState(t, root, nil)

		out := runShipReport(t, root, nil)
		found := false
		for _, raw := range out.Issues {
			m, _ := raw.(map[string]any)
			if s, _ := m["summary"].(string); strings.HasPrefix(s, "User input read failed: ") {
				found = true
			}
		}
		if !found {
			t.Errorf("issues = %v, want a 'User input read failed' warning", out.Issues)
		}
		if len(out.UserInputs) != 0 {
			t.Errorf("userInputs must stay empty on a read failure, got %#v", out.UserInputs)
		}
		if !strings.Contains(out.Display, "## User input\n\n_No user input during the run._\n") {
			t.Errorf("display must still show the empty user-input line on a read failure:\n%s", out.Display)
		}
	})

	t.Run("summary row order", func(t *testing.T) {
		root := shipReportRoot(t)
		createShipReportState(t, root, nil)

		out := runShipReport(t, root, nil)
		var order []string
		for _, l := range shipReportSection(out.Display, "Summary") {
			if !strings.HasPrefix(l, "| ") || l == "| Area | Result |" {
				continue
			}
			cells := strings.SplitN(strings.Trim(l, "| "), " | ", 2)
			order = append(order, cells[0])
		}
		want := []string{"Run", "Plan", "Steps", "User input", "Execution", "Review", "Fixed by severity", "Hardened",
			"Deferred", "Guardrail hits", "CLI commands", "Decisions", "Learnings"}
		if strings.Join(order, ",") != strings.Join(want, ",") {
			t.Fatalf("summary rows %v, want %v (13 rows, User input right after Steps)", order, want)
		}
	})

	t.Run("## User input follows ## Steps", func(t *testing.T) {
		root := shipReportRoot(t)
		createShipReportState(t, root, nil)

		out := runShipReport(t, root, nil)
		steps, ui := strings.Index(out.Display, "## Steps"), strings.Index(out.Display, "## User input")
		if steps < 0 || ui < 0 || ui < steps {
			t.Errorf("## User input must come right after ## Steps:\n%s", out.Display)
		}
	})
}
