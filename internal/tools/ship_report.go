package tools

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/pipeline"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ---------------------------------------------------------------------------
// Action: report
// ---------------------------------------------------------------------------

// ShipRunReportOut is the end-of-run ship report built by ship_state's
// report action. It covers every ship run, whether or not execute ran: the
// execute part (Execution) is present only when this run's execute step
// completed, so a stale execute state from an earlier run on the same
// branch never leaks in.
//
// Bump, Duration and Issues are not in the plan's contract struct. They
// carry ship state's flags.bump, the pipeline duration and data.issues,
// which the report must read and render.
type ShipRunReportOut struct {
	Branch           string              `json:"branch"`
	RunID            string              `json:"runId"`
	Format           string              `json:"format"`
	Bump             string              `json:"bump,omitempty"`
	Duration         string              `json:"duration,omitempty"`
	Plan             *ShipPlanTiming     `json:"plan"`               // null when no plan is linked
	PlanNote         string              `json:"planNote,omitempty"` // why plan is null
	Steps            []StepTiming        `json:"steps"`
	Issues           []any               `json:"issues"`
	ReviewLedger     *ShipReviewLedger   `json:"reviewLedger"`
	ReviewLedgerNote string              `json:"reviewLedgerNote,omitempty"`
	Healing          map[string]any      `json:"healing"`
	HardenCommit     string              `json:"hardenCommit,omitempty"` // harden step's complete-step result
	Deferred         []any               `json:"deferredFindings"`
	Execution        *ExecutionReportOut `json:"execution,omitempty"`
	GuardrailHits    []string            `json:"guardrailHits"`
	CLIEvidence      []CLIEvidenceEntry  `json:"cliEvidence"`
	Decisions        []string            `json:"decisions"`
	LinkedLearnings  int                 `json:"linkedLearnings"`
	Display          string              `json:"display"`
	Path             string              `json:"path,omitempty"`
	Written          bool                `json:"written"`
	Skipped          bool                `json:"skipped,omitempty"`
	Next             string              `json:"next"`
}

// ShipPlanTiming is the plan run's timing, read from the history store.
// DurationMs ends at the last plan-file edit, not at plan acceptance.
type ShipPlanTiming struct {
	PlanFile       string `json:"planFile"`
	StartedAt      string `json:"startedAt"`
	LastModifiedAt string `json:"lastModifiedAt"`
	DurationMs     int64  `json:"durationMs"`
}

// shipPlanNote explains a null plan on the report.
const shipPlanNote = "no plan linked to this run"

// shipPlanHistoryWindow is how many recent history records the plan-timing
// lookup scans.
const shipPlanHistoryWindow = 100

// shipHardenInterrupted is how a hardened record left at phase "started"
// renders: harden wrote and committed edits, but the run stopped before the
// "done" record listed them.
const shipHardenInterrupted = "interrupted — edits committed, surface list unknown"

const (
	shipReportNextWritten = "Report persisted. Show the path to the user; do not write it yourself."
	shipReportNextRead    = "Show display to the user. Pass detail.write:true to persist the report."
)

// shipStateReport composes, renders and optionally writes the end-of-run
// ship report. It mirrors execActionReport's config gate: detail.format is
// validated first, then automation.report.enabled=false returns
// ReportSkippedOut before any state is read. It works on a stamped state
// (pipelineCompletedAt set), since ship renders the report after cleanup.
// It never writes state; with detail.write it writes only the report file,
// <root>/.sdlc-v2/reports/ship-<runId>-report.<md|json>.
func shipStateReport(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	enabled := true
	format := "md"
	if cfg, err := config.Read(root); err == nil && cfg.Automation != nil && cfg.Automation.Report != nil {
		enabled = cfg.Automation.Report.Enabled
		format = cfg.Automation.Report.Format
	}
	override, err := shipDetailString(in.Detail, "report", "format", `Accepted values: "md" | "json".`)
	if err != nil {
		return nil, err
	}
	if override != "" {
		if override != "md" && override != "json" {
			return nil, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("report: unknown detail.format %q (want md or json)", override),
				Suggestion: "Pass detail.format as \"md\" or \"json\", or omit it to use automation.report.format.",
			}
		}
		format = override
	}
	if format != "json" {
		format = "md"
	}
	if !enabled {
		return ReportSkippedOut{Skipped: true}, nil
	}

	branch, err := execResolveBranch(detailStr(in.Detail, "branch"), workDir)
	if err != nil {
		return nil, err
	}
	shipSt, err := shipFindState(root, branch)
	if err != nil {
		return nil, err
	}

	out := buildShipRunReport(root, branch, shipSt, format, now)
	write := detailBool(in.Detail, "write")
	out.Next = shipReportNextRead
	if write {
		out.Next = shipReportNextWritten
	}
	if format == "md" {
		out.Display = renderShipReportMarkdown(out)
	} else {
		out.Display = shipReportSummaryLine(out)
	}

	if write {
		var content any = out
		if format == "md" {
			content = []byte(out.Display)
		}
		path, werr := execWriteReportFile(root, "ship-"+out.RunID, format, content)
		if werr != nil {
			return nil, werr
		}
		out.Path = path
		out.Written = true
	}
	return out, nil
}

// buildShipRunReport reads every report source for one ship run. All
// cross-reads are best-effort: a missing execute state, history file,
// evidence log or learnings log leaves its part empty, never fails the call.
func buildShipRunReport(root, branch string, shipSt *state.State, format string, now func() time.Time) ShipRunReportOut {
	data := shipSt.Data
	rd := shipBuildReportData(data, now())
	runID := execDeriveRunID(data, 0)

	out := ShipRunReportOut{
		Branch:           branch,
		RunID:            runID,
		Format:           format,
		Bump:             rd.Bump,
		Duration:         rd.Duration,
		PlanNote:         shipPlanNote,
		Steps:            extractStepTimings(data),
		Issues:           []any{},
		ReviewLedger:     rd.ReviewLedger,
		ReviewLedgerNote: rd.ReviewLedgerNote,
		Healing:          rd.Healing,
		Deferred:         []any{},
		GuardrailHits:    []string{},
		CLIEvidence:      []CLIEvidenceEntry{},
		Decisions:        rd.Decisions,
	}
	if raw, ok := data["issues"].([]any); ok {
		out.Issues = append(out.Issues, raw...)
	}
	if raw, ok := data["deferredFindings"].([]any); ok {
		out.Deferred = append(out.Deferred, raw...)
	}
	if step := shipFindStepEntry(data, "harden"); step != nil && step["status"] == "completed" {
		out.HardenCommit, _ = step["result"].(string)
	}

	if step := shipFindStepEntry(data, "execute"); step != nil && step["status"] == "completed" {
		if execSt, _ := state.Find(root, "execute", branch); execSt != nil {
			rep := buildExecutionReport(root, branch, execSt, format, execDeriveRunID(execSt.Data, 0), now)
			out.Execution = &rep
			out.GuardrailHits = extractGuardrailHits(execSt.Data)
			if plan := shipPlanTimingFor(root, execSt.Data); plan != nil {
				out.Plan = plan
				out.PlanNote = ""
			}
		}
	}

	since, _ := data["startedAt"].(string)
	if evidence, err := readCLIEvidenceInWindow(root, branch, since, maxCLIEvidenceInWindow); err != nil {
		out.Issues = append(out.Issues, map[string]any{
			"severity": "warning",
			"category": "cross-read",
			"summary":  "CLI evidence read failed: " + err.Error(),
		})
	} else if evidence != nil {
		out.CLIEvidence = evidence
	}

	linked, err := countLinkedLearnings(root, runID)
	if err != nil {
		out.Issues = append(out.Issues, map[string]any{
			"severity": "warning",
			"category": "cross-read",
			"summary":  "Learnings count failed: " + err.Error(),
		})
	}
	out.LinkedLearnings = linked
	return out
}

// shipPlanTimingFor finds the plan run that produced the execute run's
// plan: the latest history record with skill "plan" whose plan_file equals
// the execute state's planPath. A relative planPath is joined to the
// execute state's worktree first, and both sides are cleaned, because
// execute stores the path as given while plan stores it absolute. It
// returns nil when there is no planPath, no match, or the history read fails.
func shipPlanTimingFor(root string, execData map[string]any) *ShipPlanTiming {
	planPath, _ := execData["planPath"].(string)
	if planPath == "" {
		return nil
	}
	if !filepath.IsAbs(planPath) {
		worktree, _ := execData["worktree"].(string)
		planPath = filepath.Join(worktree, planPath)
	}
	planPath = filepath.Clean(planPath)

	runs, err := history.NewFileWriter(historyDir(root)).ReadRecentRuns(shipPlanHistoryWindow)
	if err != nil {
		return nil
	}
	var match *history.RunRecord
	for i := range runs {
		r := &runs[i]
		if r.Skill != "plan" || r.PlanFile == "" {
			continue
		}
		if filepath.Clean(r.PlanFile) == planPath {
			match = r // runs are oldest first, so the last match is the latest
		}
	}
	if match == nil {
		return nil
	}
	return &ShipPlanTiming{
		PlanFile:       match.PlanFile,
		StartedAt:      match.StartedAt,
		LastModifiedAt: match.LastModifiedAt,
		DurationMs:     match.DurationMs,
	}
}

// shipReportSummaryLine is the one-line display for a json-format report.
func shipReportSummaryLine(out ShipRunReportOut) string {
	completed := 0
	for _, s := range out.Steps {
		if s.Status == "completed" {
			completed++
		}
	}
	fixed, _ := out.Healing["fixed"].([]any)
	return fmt.Sprintf("Ship run %s on %s: %d/%d steps completed, %d findings fixed, %d deferred, %d guardrail hits.",
		out.RunID, out.Branch, completed, len(out.Steps), len(fixed), len(out.Deferred), len(out.GuardrailHits))
}

// renderShipReportMarkdown renders the report as markdown. Sections come in
// a fixed order: header, Plan, Steps, Review ledger, Self-healing (Fixed,
// Hardened, Harden commit), Deferred, Execution (only when included),
// Guardrail hits, CLI evidence, Decisions, Learnings, Next. Every empty list
// renders an explicit line, never an empty heading.
func renderShipReportMarkdown(out ShipRunReportOut) string {
	var b strings.Builder
	line := func(format string, args ...any) {
		fmt.Fprintf(&b, format+"\n", args...)
	}

	// Header
	line("# Ship run report — %s", out.Branch)
	line("")
	line("- Run: %s", out.RunID)
	if out.Bump != "" {
		line("- Bump: %s", out.Bump)
	}
	if out.Duration != "" {
		line("- Duration: %s", out.Duration)
	}

	// Plan
	line("")
	line("## Plan")
	line("")
	if out.Plan != nil {
		line("- File: %s", out.Plan.PlanFile)
		dur := pipeline.Humanize(time.Duration(out.Plan.DurationMs) * time.Millisecond)
		line("- Planning time: %s (%s → last edit %s)", dur, out.Plan.StartedAt, out.Plan.LastModifiedAt)
	} else {
		line("_Plan timing not available — %s._", shipPlanNote)
	}

	// Steps
	line("")
	line("## Steps")
	line("")
	if len(out.Steps) == 0 {
		line("_No steps recorded._")
	}
	for _, s := range out.Steps {
		text := fmt.Sprintf("- %s: %s", s.Name, s.Status)
		if s.Duration != "" {
			text += " (" + s.Duration + ")"
		}
		if s.HumanWait {
			text += " — human wait"
		}
		line("%s", text)
	}
	if len(out.Issues) > 0 {
		line("")
		line("Issues:")
		for _, raw := range out.Issues {
			m, _ := raw.(map[string]any)
			severity, _ := m["severity"].(string)
			summary, _ := m["summary"].(string)
			line("- [%s] %s", severity, summary)
		}
	}

	// Review ledger
	line("")
	line("## Review ledger")
	line("")
	if l := out.ReviewLedger; l != nil {
		line("- Total: %d", l.Total)
		line("- Fixed: %d", l.Fixed)
		if len(l.DeferredByReason) == 0 {
			line("- Deferred: 0")
		}
		reasons := make([]string, 0, len(l.DeferredByReason))
		for reason := range l.DeferredByReason {
			reasons = append(reasons, reason)
		}
		sort.Strings(reasons)
		for _, reason := range reasons {
			line("- Deferred (%s): %d", reason, l.DeferredByReason[reason])
		}
		line("- Unaccounted: %d", l.Unaccounted)
	} else {
		note := out.ReviewLedgerNote
		if note == "" {
			note = shipReviewLedgerNote
		}
		line("_Review ledger not available — %s._", note)
	}

	// Self-healing
	line("")
	line("## Self-healing")
	line("")
	line("### Fixed")
	line("")
	fixed, _ := out.Healing["fixed"].([]any)
	if len(fixed) == 0 {
		line("_No findings fixed._")
	}
	for _, raw := range fixed {
		m, _ := raw.(map[string]any)
		origin, _ := m["origin"].(string)
		line("- [%s] %s — %s (%s)", shipReportStr(m["severity"]), shipReportLocation(m), shipReportStr(m["title"]), origin)
	}
	line("")
	line("### Hardened")
	line("")
	hardened, _ := out.Healing["hardened"].([]any)
	if len(hardened) == 0 {
		line("_No harden runs recorded._")
	}
	for _, raw := range hardened {
		m, _ := raw.(map[string]any)
		head := fmt.Sprintf("- %s (%s)", shipReportStr(m["trigger"]), shipReportStr(m["classification"]))
		if m["phase"] != "done" {
			line("%s: %s", head, shipHardenInterrupted)
			continue
		}
		applied, _ := m["applied"].([]any)
		skipped, _ := healingInt(m["skipped"])
		if len(applied) == 0 {
			line("%s: no changes applied, %d skipped", head, skipped)
			continue
		}
		line("%s: %d applied, %d skipped", head, len(applied), skipped)
		for _, a := range applied {
			am, _ := a.(map[string]any)
			line("  - %s: %s → %s", shipReportStr(am["surface"]), shipReportStr(am["action"]), shipReportStr(am["targetFile"]))
		}
	}
	line("")
	line("### Harden commit")
	line("")
	if out.HardenCommit != "" {
		line("- %s", out.HardenCommit)
	} else {
		line("_No harden commit._")
	}

	// Deferred
	line("")
	line("## Deferred")
	line("")
	if len(out.Deferred) == 0 {
		line("_No findings deferred._")
	}
	for _, raw := range out.Deferred {
		m, _ := raw.(map[string]any)
		reason := shipReportStr(m["reason"])
		if reason == "" {
			reason = history.ReasonBelowThreshold
		}
		line("- [%s] %s — %s (%s)", shipReportStr(m["severity"]), shipReportLocation(m), shipReportStr(m["title"]), reason)
	}

	// Execution (only when included)
	if e := out.Execution; e != nil {
		line("")
		line("## Execution")
		line("")
		line("- Tasks: %d completed, %d failed, %d skipped of %d", e.CompletedTasks, e.FailedTasks, e.SkippedTasks, e.TotalTasks)
		if e.Duration != "" {
			line("- Duration: %s", e.Duration)
		}
		if len(e.Waves) == 0 {
			line("- _No waves recorded._")
		}
		for _, w := range e.Waves {
			text := fmt.Sprintf("- Wave %d: %s, %d tasks", w.Number, w.Status, len(w.Tasks))
			if w.Duration != "" {
				text += " (" + w.Duration + ")"
			}
			if w.CommittedSHA != "" {
				text += " — " + w.CommittedSHA
			}
			line("%s", text)
		}
		line("- Issues: %d drifts, %d errors, %d warnings, %d concerns", len(e.Drifts), len(e.Errors), len(e.Warnings), len(e.Concerns))
	}

	// Guardrail hits
	line("")
	line("## Guardrail hits")
	line("")
	if len(out.GuardrailHits) == 0 {
		line("_No guardrail hits._")
	}
	for _, id := range out.GuardrailHits {
		line("- %s", id)
	}

	// CLI evidence
	line("")
	line("## CLI evidence")
	line("")
	if len(out.CLIEvidence) == 0 {
		line("_No CLI evidence recorded._")
	}
	for _, c := range out.CLIEvidence {
		where := c.Step
		if where == "" {
			where = c.Pipeline
		}
		line("- `%s` — exit %d (%s)", c.Command, c.ExitCode, where)
	}

	// Decisions
	line("")
	line("## Decisions")
	line("")
	if len(out.Decisions) == 0 {
		line("_No decisions recorded._")
	}
	for _, d := range out.Decisions {
		line("- %s", d)
	}

	// Learnings
	line("")
	line("## Learnings")
	line("")
	if out.LinkedLearnings == 0 {
		line("_No learnings linked to this run._")
	} else {
		line("- Linked learnings: %d", out.LinkedLearnings)
	}

	// Next
	line("")
	line("## Next")
	line("")
	if out.Next != "" {
		line("%s", out.Next)
	} else {
		line("_No next step._")
	}
	return b.String()
}

// shipReportStr returns v when it is a string, else "".
func shipReportStr(v any) string {
	s, _ := v.(string)
	return s
}

// shipReportLocation renders a finding's file and optional line.
func shipReportLocation(m map[string]any) string {
	file := shipReportStr(m["file"])
	if n, ok := healingInt(m["line"]); ok {
		return fmt.Sprintf("%s:%d", file, n)
	}
	return file
}
