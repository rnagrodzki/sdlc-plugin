package tools

import (
	"errors"
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
	Display          string              `json:"display" render:"raw"` // pre-rendered report; emitted verbatim, never fenced
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

// shipPlanNoteReadFailed explains a null plan when the history store could
// not be read — distinct from shipPlanNote, so an unreadable runs.jsonl is
// never reported as "no plan".
const shipPlanNoteReadFailed = "plan history could not be read"

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
//
// A missing config uses the defaults (enabled, md) silently. Any other
// config read error also uses the defaults, but is surfaced as a warning in
// the report's issues, so a malformed config.toml is visible instead of
// indistinguishable from "no config".
func shipStateReport(root, workDir string, in ShipStateIn, now func() time.Time) (any, error) {
	enabled := true
	format := "md"
	cfg, cfgErr := config.Read(root)
	if cfgErr == nil && cfg.Automation != nil && cfg.Automation.Report != nil {
		enabled = cfg.Automation.Report.Enabled
		format = cfg.Automation.Report.Format
	}
	if errors.Is(cfgErr, config.ErrNotFound) {
		cfgErr = nil
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

	write, err := shipDetailBool(in.Detail, "report", "write",
		"write:true persists the report under .sdlc-v2/reports/; omit it to only render.")
	if err != nil {
		return nil, err
	}

	out := buildShipRunReport(root, branch, shipSt, format, now)
	if cfgErr != nil {
		out.Issues = append(out.Issues, map[string]any{
			"severity": "warning",
			"category": "cross-read",
			"summary":  "Config read failed, report used defaults (enabled, md): " + cfgErr.Error(),
		})
	}
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
			plan, perr := shipPlanTimingFor(root, execSt.Data)
			switch {
			case perr != nil:
				out.PlanNote = shipPlanNoteReadFailed
				out.Issues = append(out.Issues, map[string]any{
					"severity": "warning",
					"category": "cross-read",
					"summary":  "Plan history read failed: " + perr.Error(),
				})
			case plan != nil:
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
// returns (nil, nil) when there is no planPath, no history yet, or no match,
// and a non-nil error only when the history store exists but cannot be read.
func shipPlanTimingFor(root string, execData map[string]any) (*ShipPlanTiming, error) {
	planPath, _ := execData["planPath"].(string)
	if planPath == "" {
		return nil, nil
	}
	if !filepath.IsAbs(planPath) {
		worktree, _ := execData["worktree"].(string)
		planPath = filepath.Join(worktree, planPath)
	}
	planPath = filepath.Clean(planPath)

	runs, err := history.NewFileWriter(historyDir(root)).ReadRecentRuns(shipPlanHistoryWindow)
	if err != nil {
		return nil, err
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
		return nil, nil
	}
	return &ShipPlanTiming{
		PlanFile:       match.PlanFile,
		StartedAt:      match.StartedAt,
		LastModifiedAt: match.LastModifiedAt,
		DurationMs:     match.DurationMs,
	}, nil
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
// renders an explicit line, never an empty heading. Each section has its own
// render helper; this function only fixes their order.
func renderShipReportMarkdown(out ShipRunReportOut) string {
	w := &shipReportWriter{}
	renderShipReportHeader(w, out)
	renderShipReportPlan(w, out)
	renderShipReportSteps(w, out)
	renderShipReportReviewLedger(w, out)
	renderShipReportHealing(w, out)
	renderShipReportDeferred(w, out)
	renderShipReportExecution(w, out.Execution)
	renderShipReportGuardrailHits(w, out.GuardrailHits)
	renderShipReportCLIEvidence(w, out.CLIEvidence)
	renderShipReportDecisions(w, out.Decisions)
	renderShipReportLearnings(w, out.LinkedLearnings)
	renderShipReportNext(w, out.Next)
	return w.b.String()
}

// shipReportWriter accumulates the markdown report one line at a time.
type shipReportWriter struct {
	b strings.Builder
}

// line writes one formatted line followed by a newline.
func (w *shipReportWriter) line(format string, args ...any) {
	fmt.Fprintf(&w.b, format+"\n", args...)
}

// heading writes a blank line, a "## title" heading and another blank line —
// the opening every section after the header shares.
func (w *shipReportWriter) heading(title string) {
	w.line("")
	w.line("## %s", title)
	w.line("")
}

func renderShipReportHeader(w *shipReportWriter, out ShipRunReportOut) {
	w.line("# Ship run report — %s", out.Branch)
	w.line("")
	w.line("- Run: %s", out.RunID)
	if out.Bump != "" {
		w.line("- Bump: %s", out.Bump)
	}
	if out.Duration != "" {
		w.line("- Duration: %s", out.Duration)
	}
}

func renderShipReportPlan(w *shipReportWriter, out ShipRunReportOut) {
	w.heading("Plan")
	if out.Plan != nil {
		w.line("- File: %s", out.Plan.PlanFile)
		dur := pipeline.Humanize(time.Duration(out.Plan.DurationMs) * time.Millisecond)
		w.line("- Planning time: %s (%s → last edit %s)", dur, out.Plan.StartedAt, out.Plan.LastModifiedAt)
		return
	}
	note := out.PlanNote
	if note == "" {
		note = shipPlanNote
	}
	w.line("_Plan timing not available — %s._", note)
}

func renderShipReportSteps(w *shipReportWriter, out ShipRunReportOut) {
	w.heading("Steps")
	if len(out.Steps) == 0 {
		w.line("_No steps recorded._")
	}
	for _, s := range out.Steps {
		text := fmt.Sprintf("- %s: %s", s.Name, s.Status)
		if s.Duration != "" {
			text += " (" + s.Duration + ")"
		}
		if s.HumanWait {
			text += " — human wait"
		}
		w.line("%s", text)
	}
	if len(out.Issues) > 0 {
		w.line("")
		w.line("Issues:")
		for _, raw := range out.Issues {
			m, _ := raw.(map[string]any)
			severity, _ := m["severity"].(string)
			summary, _ := m["summary"].(string)
			w.line("- [%s] %s", severity, summary)
		}
	}
}

func renderShipReportReviewLedger(w *shipReportWriter, out ShipRunReportOut) {
	w.heading("Review ledger")
	l := out.ReviewLedger
	if l == nil {
		note := out.ReviewLedgerNote
		if note == "" {
			note = shipReviewLedgerNote
		}
		w.line("_Review ledger not available — %s._", note)
		return
	}
	w.line("- Total: %d", l.Total)
	w.line("- Fixed: %d", l.Fixed)
	if len(l.DeferredByReason) == 0 {
		w.line("- Deferred: 0")
	}
	reasons := make([]string, 0, len(l.DeferredByReason))
	for reason := range l.DeferredByReason {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		w.line("- Deferred (%s): %d", reason, l.DeferredByReason[reason])
	}
	w.line("- Unaccounted: %d", l.Unaccounted)
}

func renderShipReportHealing(w *shipReportWriter, out ShipRunReportOut) {
	w.heading("Self-healing")
	w.line("### Fixed")
	w.line("")
	fixed, _ := out.Healing["fixed"].([]any)
	if len(fixed) == 0 {
		w.line("_No findings fixed._")
	}
	for _, raw := range fixed {
		m, _ := raw.(map[string]any)
		origin, _ := m["origin"].(string)
		w.line("- [%s] %s — %s (%s)", shipReportStr(m["severity"]), shipReportLocation(m), shipReportStr(m["title"]), origin)
	}

	w.line("")
	w.line("### Hardened")
	w.line("")
	hardened, _ := out.Healing["hardened"].([]any)
	if len(hardened) == 0 {
		w.line("_No harden runs recorded._")
	}
	for _, raw := range hardened {
		m, _ := raw.(map[string]any)
		renderShipReportHardenedRun(w, m)
	}

	w.line("")
	w.line("### Harden commit")
	w.line("")
	if out.HardenCommit != "" {
		w.line("- %s", out.HardenCommit)
	} else {
		w.line("_No harden commit._")
	}
}

// renderShipReportHardenedRun renders one data.healing.hardened record: an
// interrupted run (phase not "done"), a run that applied nothing, or a run
// with its applied edits listed underneath.
func renderShipReportHardenedRun(w *shipReportWriter, m map[string]any) {
	head := fmt.Sprintf("- %s (%s)", shipReportStr(m["trigger"]), shipReportStr(m["classification"]))
	if m["phase"] != "done" {
		w.line("%s: %s", head, shipHardenInterrupted)
		return
	}
	applied, _ := m["applied"].([]any)
	skipped, _ := healingInt(m["skipped"])
	if len(applied) == 0 {
		w.line("%s: no changes applied, %d skipped", head, skipped)
		return
	}
	w.line("%s: %d applied, %d skipped", head, len(applied), skipped)
	for _, a := range applied {
		am, _ := a.(map[string]any)
		w.line("  - %s: %s → %s", shipReportStr(am["surface"]), shipReportStr(am["action"]), shipReportStr(am["targetFile"]))
	}
}

func renderShipReportDeferred(w *shipReportWriter, out ShipRunReportOut) {
	w.heading("Deferred")
	if len(out.Deferred) == 0 {
		w.line("_No findings deferred._")
	}
	for _, raw := range out.Deferred {
		m, _ := raw.(map[string]any)
		reason := shipReportStr(m["reason"])
		if reason == "" {
			reason = history.ReasonBelowThreshold
		}
		w.line("- [%s] %s — %s (%s)", shipReportStr(m["severity"]), shipReportLocation(m), shipReportStr(m["title"]), reason)
	}
}

// renderShipReportExecution renders the Execution section only when this
// run's execute step completed; e is nil otherwise and nothing is written.
func renderShipReportExecution(w *shipReportWriter, e *ExecutionReportOut) {
	if e == nil {
		return
	}
	w.heading("Execution")
	w.line("- Tasks: %d completed, %d failed, %d skipped of %d", e.CompletedTasks, e.FailedTasks, e.SkippedTasks, e.TotalTasks)
	if e.Duration != "" {
		w.line("- Duration: %s", e.Duration)
	}
	if len(e.Waves) == 0 {
		w.line("- _No waves recorded._")
	}
	for _, wave := range e.Waves {
		text := fmt.Sprintf("- Wave %d: %s, %d tasks", wave.Number, wave.Status, len(wave.Tasks))
		if wave.Duration != "" {
			text += " (" + wave.Duration + ")"
		}
		if wave.CommittedSHA != "" {
			text += " — " + wave.CommittedSHA
		}
		w.line("%s", text)
	}
	w.line("- Issues: %d drifts, %d errors, %d warnings, %d concerns", len(e.Drifts), len(e.Errors), len(e.Warnings), len(e.Concerns))
}

func renderShipReportGuardrailHits(w *shipReportWriter, hits []string) {
	w.heading("Guardrail hits")
	if len(hits) == 0 {
		w.line("_No guardrail hits._")
	}
	for _, id := range hits {
		w.line("- %s", id)
	}
}

func renderShipReportCLIEvidence(w *shipReportWriter, evidence []CLIEvidenceEntry) {
	w.heading("CLI evidence")
	if len(evidence) == 0 {
		w.line("_No CLI evidence recorded._")
	}
	for _, c := range evidence {
		where := c.Step
		if where == "" {
			where = c.Pipeline
		}
		w.line("- `%s` — exit %d (%s)", c.Command, c.ExitCode, where)
	}
}

func renderShipReportDecisions(w *shipReportWriter, decisions []string) {
	w.heading("Decisions")
	if len(decisions) == 0 {
		w.line("_No decisions recorded._")
	}
	for _, d := range decisions {
		w.line("- %s", d)
	}
}

func renderShipReportLearnings(w *shipReportWriter, linked int) {
	w.heading("Learnings")
	if linked == 0 {
		w.line("_No learnings linked to this run._")
	} else {
		w.line("- Linked learnings: %d", linked)
	}
}

func renderShipReportNext(w *shipReportWriter, next string) {
	w.heading("Next")
	if next != "" {
		w.line("%s", next)
	} else {
		w.line("_No next step._")
	}
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
