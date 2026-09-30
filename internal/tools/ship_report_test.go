package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
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
	if !strings.Contains(out.Display, "- Bump: minor") {
		t.Errorf("display missing bump:\n%s", out.Display)
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
	if !strings.Contains(string(body), "Report persisted.") {
		t.Errorf("written report's Next section should say it was persisted:\n%s", body)
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

	headings := []string{"# Ship run report — " + shipReportBranch, "## Plan", "## Steps", "## Review ledger", "## Self-healing",
		"### Fixed", "### Hardened", "### Harden commit", "## Deferred", "## Guardrail hits",
		"## CLI evidence", "## Decisions", "## Learnings", "## Next"}
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
		"- t1 (user-code): 1 applied, 1 skipped",
		"  - guardrails: add → .sdlc-v2/config.toml",
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
	if !strings.Contains(out.Display, "- t1 (user-code): interrupted — edits committed, surface list unknown") {
		t.Errorf("started record must render as interrupted:\n%s", out.Display)
	}
	if strings.Contains(out.Display, "no changes applied") || strings.Contains(out.Display, "_No harden runs recorded._") {
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
