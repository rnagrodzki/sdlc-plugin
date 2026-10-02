package tools

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
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
	Plan             *ShipPlanTiming     `json:"plan"`                   // null when no plan is linked
	PlanNote         string              `json:"planNote,omitempty"`     // why plan is null
	Planning         *ShipPlanning       `json:"planning"`               // null when no plan run state is found
	PlanningNote     string              `json:"planningNote,omitempty"` // why planning is null
	Steps            []StepTiming        `json:"steps"`
	Timeline         []TimelineEvent     `json:"timeline"` // never null; sorted by at
	Issues           []any               `json:"issues"`
	ReviewLedger     *ShipReviewLedger   `json:"reviewLedger"`
	ReviewLedgerNote string              `json:"reviewLedgerNote,omitempty"`
	Healing          map[string]any      `json:"healing"`
	HardenCommit     string              `json:"hardenCommit,omitempty"` // harden step's complete-step result
	Deferred         []any               `json:"deferredFindings"`
	Execution        *ExecutionReportOut `json:"execution,omitempty"`
	GuardrailHits    []string            `json:"guardrailHits"`
	CLIEvidence      []CLIEvidenceEntry  `json:"cliEvidence"`
	UserInputs       []UserInputEntry    `json:"userInputs" jsonschema_description:"Prompts the user typed while this run was active, oldest first, redacted, latest 100. Empty array when none."`
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

// ShipPlanning is the linked plan run's decisions and milestones, read from
// the plan run state file whose planFilePath equals the execute run's
// planPath.
type ShipPlanning struct {
	PlanFile   string           `json:"planFile"`
	Decisions  []map[string]any `json:"decisions"`  // criticalDecisions entries: {key, choice, rejected, reason, at}
	Milestones []PlanMilestone  `json:"milestones"` // planIntegrity markers, time order
}

// PlanMilestone is one planIntegrity marker and the time it was stamped.
type PlanMilestone struct {
	Name string `json:"name"`
	At   string `json:"at"`
}

// TimelineEvent is one timed event of the plan → execute → ship run.
type TimelineEvent struct {
	At    string `json:"at"`
	Phase string `json:"phase"` // plan | execute | ship
	Event string `json:"event"` // e.g. "wave 1 started", "base-sync wave 1: merged", "pr completed"
}

// shipPlanningNote explains a null planning on the report.
const shipPlanningNote = "plan run state not found"

// shipPlanningNoteReadFailed explains a null planning when the runs
// directory could not be read — distinct from shipPlanningNote.
const shipPlanningNoteReadFailed = "plan run state could not be read"

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
// or an unstamped one: ship writes the report before cleanup-pipeline, so
// the linked plan run still exists and feeds Planning and Timeline.
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
		PlanningNote:     shipPlanningNote,
		Steps:            extractStepTimings(data),
		Issues:           []any{},
		ReviewLedger:     rd.ReviewLedger,
		ReviewLedgerNote: rd.ReviewLedgerNote,
		Healing:          rd.Healing,
		Deferred:         []any{},
		GuardrailHits:    []string{},
		CLIEvidence:      []CLIEvidenceEntry{},
		UserInputs:       []UserInputEntry{},
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

	var events []TimelineEvent
	if step := shipFindStepEntry(data, "execute"); step != nil && step["status"] == "completed" {
		if execSt, _ := state.Find(root, "execute", branch); execSt != nil {
			planRun, prErr := state.FindPlanRunByPlanFile(root, shipExecPlanPath(execSt.Data))
			switch {
			case prErr != nil:
				out.PlanningNote = shipPlanningNoteReadFailed
				out.Issues = append(out.Issues, map[string]any{
					"severity": "warning",
					"category": "cross-read",
					"summary":  "Plan run state read failed: " + prErr.Error(),
				})
			case planRun != nil:
				out.Planning = shipPlanningFrom(planRun.Data)
				out.PlanningNote = ""
				events = append(events, shipPlanEvents(out.Planning)...)
			}
			events = append(events, shipExecuteEvents(execSt.Data)...)
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
	events = append(events, shipShipEvents(data)...)
	out.Timeline = sortTimeline(events)

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

	if inputs, err := readUserInputInWindow(root, branch, since, maxUserInputInWindow); err != nil {
		out.Issues = append(out.Issues, map[string]any{
			"severity": "warning",
			"category": "cross-read",
			"summary":  "user input read failed: " + err.Error(),
		})
	} else if inputs != nil {
		out.UserInputs = inputs
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
	planPath := shipExecPlanPath(execData)
	if planPath == "" {
		return nil, nil
	}

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

// shipExecPlanPath returns the execute state's planPath, absolute and
// cleaned: a relative planPath is joined to the execute state's worktree,
// because execute stores the path as given while plan stores it absolute.
// It returns "" when the execute state has no planPath.
func shipExecPlanPath(execData map[string]any) string {
	planPath, _ := execData["planPath"].(string)
	if planPath == "" {
		return ""
	}
	if !filepath.IsAbs(planPath) {
		worktree, _ := execData["worktree"].(string)
		planPath = filepath.Join(worktree, planPath)
	}
	return filepath.Clean(planPath)
}

// shipPlanningFrom builds the Planning part from a plan run's state data:
// its planFilePath, its criticalDecisions entries (maps only) and its
// planIntegrity markers that carry a string timestamp, in time order.
// Decisions and Milestones are never nil.
func shipPlanningFrom(planData map[string]any) *ShipPlanning {
	p := &ShipPlanning{Decisions: []map[string]any{}, Milestones: []PlanMilestone{}}
	p.PlanFile, _ = planData["planFilePath"].(string)
	if raw, ok := planData["criticalDecisions"].([]any); ok {
		for _, d := range raw {
			if m, ok := d.(map[string]any); ok {
				p.Decisions = append(p.Decisions, m)
			}
		}
	}
	integrity, _ := planData["planIntegrity"].(map[string]any)
	for name, v := range integrity {
		if at, ok := v.(string); ok && at != "" {
			p.Milestones = append(p.Milestones, PlanMilestone{Name: name, At: at})
		}
	}
	sort.Slice(p.Milestones, func(i, j int) bool {
		a, b := p.Milestones[i], p.Milestones[j]
		if a.At != b.At {
			return timelineLess(a.At, b.At)
		}
		return a.Name < b.Name // map order is random; keep ties stable
	})
	return p
}

// shipPlanEvents turns the plan milestones and decisions into plan-phase
// timeline events. A decision with a blank choice adds no event.
func shipPlanEvents(p *ShipPlanning) []TimelineEvent {
	var events []TimelineEvent
	for _, m := range p.Milestones {
		events = append(events, TimelineEvent{At: m.At, Phase: "plan", Event: m.Name})
	}
	for _, d := range p.Decisions {
		if strings.TrimSpace(shipReportStr(d["choice"])) == "" {
			continue
		}
		events = append(events, TimelineEvent{
			At:    shipReportStr(d["at"]),
			Phase: "plan",
			Event: fmt.Sprintf("decision %s: %s", shipReportStr(d["key"]), shipReportStr(d["choice"])),
		})
	}
	return events
}

// shipExecuteEvents turns the execute state's wave start/complete times and
// baseSyncs[] entries ({wave, status, base, behind, sha, at}) into
// execute-phase timeline events.
func shipExecuteEvents(execData map[string]any) []TimelineEvent {
	var events []TimelineEvent
	waves, _ := execData["waves"].([]any)
	for _, raw := range waves {
		w, _ := raw.(map[string]any)
		n, _ := healingInt(w["number"])
		events = append(events, TimelineEvent{At: shipReportStr(w["startedAt"]), Phase: "execute", Event: fmt.Sprintf("wave %d started", n)})
		status := shipReportStr(w["status"])
		if status == "" {
			status = "completed"
		}
		events = append(events, TimelineEvent{At: shipReportStr(w["completedAt"]), Phase: "execute", Event: fmt.Sprintf("wave %d %s", n, status)})
	}
	syncs, _ := execData["baseSyncs"].([]any)
	for _, raw := range syncs {
		s, _ := raw.(map[string]any)
		n, _ := healingInt(s["wave"])
		events = append(events, TimelineEvent{At: shipReportStr(s["at"]), Phase: "execute", Event: fmt.Sprintf("base-sync wave %d: %s", n, shipReportStr(s["status"]))})
	}
	return events
}

// shipShipEvents turns the ship state's step start/end times and
// decisions[] entries ({step, decision, at}) into ship-phase timeline events.
// A decision with blank text adds no event.
func shipShipEvents(data map[string]any) []TimelineEvent {
	var events []TimelineEvent
	for _, raw := range shipStepsSlice(data) {
		s, _ := raw.(map[string]any)
		name := shipReportStr(s["name"])
		events = append(events, TimelineEvent{At: shipReportStr(s["startedAt"]), Phase: "ship", Event: name + " started"})
		status := shipReportStr(s["status"])
		if status == "" {
			status = "completed"
		}
		events = append(events, TimelineEvent{At: shipReportStr(s["completedAt"]), Phase: "ship", Event: name + " " + status})
	}
	decisions, _ := data["decisions"].([]any)
	for _, raw := range decisions {
		d, _ := raw.(map[string]any)
		if strings.TrimSpace(shipReportStr(d["decision"])) == "" {
			continue
		}
		events = append(events, TimelineEvent{
			At:    shipReportStr(d["at"]),
			Phase: "ship",
			Event: fmt.Sprintf("decision %s: %s", shipReportStr(d["step"]), shipReportStr(d["decision"])),
		})
	}
	return events
}

// sortTimeline drops events without a timestamp and sorts the rest by at,
// keeping the input order (plan, execute, ship) for equal times. It never
// returns nil.
func sortTimeline(events []TimelineEvent) []TimelineEvent {
	out := []TimelineEvent{}
	for _, e := range events {
		if strings.TrimSpace(e.At) != "" {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return timelineLess(out[i].At, out[j].At) })
	return out
}

// timelineLess orders two timestamps: by time when both parse as RFC 3339,
// else by string.
func timelineLess(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339, a)
	tb, errB := time.Parse(time.RFC3339, b)
	if errA == nil && errB == nil {
		return ta.Before(tb)
	}
	return a < b
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
// a fixed order: title, Summary, Plan (timing and critical decisions),
// Steps, Timeline, Review ledger, Self-healing (Fixed, Hardened, Harden
// commit), Deferred, Execution (only when included), Guardrail hits, CLI
// evidence, Decisions, Learnings. Every empty list renders an explicit line,
// never an empty heading or a header-only table. No stored string reaches
// the display raw: list text goes through shipReportShort, table cells
// through shipReportCell, commands through shipReportCode. The tool's next
// instruction is not rendered; it stays in the output's next field. Each
// section has its own render helper; this function only fixes their order.
func renderShipReportMarkdown(out ShipRunReportOut) string {
	w := &shipReportWriter{}
	renderShipReportHeader(w, out)
	renderShipReportSummary(w, out)
	renderShipReportPlan(w, out)
	renderShipReportSteps(w, out)
	renderShipReportUserInput(w, out.UserInputs)
	renderShipReportTimeline(w, out.Timeline)
	renderShipReportReviewLedger(w, out)
	renderShipReportHealing(w, out)
	renderShipReportDeferred(w, out)
	renderShipReportExecution(w, out.Execution)
	renderShipReportGuardrailHits(w, out.GuardrailHits)
	renderShipReportCLIEvidence(w, out.CLIEvidence)
	renderShipReportDecisions(w, out.Decisions)
	renderShipReportLearnings(w, out.LinkedLearnings)
	return w.b.String()
}

const (
	shipReportTextMax    = 200 // decisions, timeline events
	shipReportCommandMax = 120 // commands, harden triggers
	shipReportFailedCap  = 20
)

const shipReportCommandRows = 15 // Command table rows before the "other" row

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

// ---------------------------------------------------------------------------
// Short-form and sanitizing helpers
// ---------------------------------------------------------------------------

// shipReportShort returns the first non-blank line of s with whitespace runs
// collapsed, cut to max runes. It appends "…" when it cut the line or
// dropped later lines (" …" with a space for dropped lines only). Empty in →
// "".
func shipReportShort(s string, max int) string {
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)
	first, dropped := "", false
	for _, l := range strings.Split(s, "\n") {
		f := strings.Join(strings.Fields(l), " ")
		if f == "" {
			continue
		}
		if first != "" {
			dropped = true
			break
		}
		first = f
	}
	if r := []rune(first); max > 0 && len(r) > max {
		return strings.TrimRight(string(r[:max]), " ") + "…"
	}
	if dropped {
		return first + " …"
	}
	return first
}

// shipReportCode renders s as one safe inline code span: shipReportShort(s,
// shipReportCommandMax), then fenced with one backtick more than the longest
// backtick run inside it, with a space inside the fence when the text starts
// or ends with a backtick (CommonMark code-span rule). An empty command
// renders as `(empty)`.
func shipReportCode(s string) string {
	t := shipReportShort(s, shipReportCommandMax)
	if t == "" {
		t = "(empty)"
	}
	longest, run := 0, 0
	for _, c := range t {
		if c != '`' {
			run = 0
			continue
		}
		run++
		if run > longest {
			longest = run
		}
	}
	fence := strings.Repeat("`", longest+1)
	if strings.HasPrefix(t, "`") || strings.HasSuffix(t, "`") {
		t = " " + t + " "
	}
	return fence + t + fence
}

// shipReportCell makes s safe inside a markdown table cell: pipes are
// escaped and line breaks become spaces.
func shipReportCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
}

// shipReportRelPath returns p from its first ".sdlc-v2/" or ".github/"
// segment, else p unchanged.
func shipReportRelPath(p string) string {
	best := -1
	for _, seg := range []string{paths.DataDir + "/", ".github/"} {
		i := -1
		if strings.HasPrefix(p, seg) {
			i = 0
		} else if j := strings.Index(p, "/"+seg); j >= 0 {
			i = j + 1
		}
		if i >= 0 && (best < 0 || i < best) {
			best = i
		}
	}
	if best < 0 {
		return p
	}
	return p[best:]
}

// shipReportSubcommandPrograms are the programs whose command group also
// names the subcommand (git diff, go test, task check).
var shipReportSubcommandPrograms = map[string]bool{
	"git": true, "gh": true, "go": true, "task": true, "npm": true, "pnpm": true, "openspec": true,
}

var (
	shipReportSubcommandRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	shipReportEnvAssignRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

// shipReportCommandName returns the command group of one evidence command:
// skip leading VAR=value words and leading "cd <dir> &&" pairs, take the
// program's base name, and for git, gh, go, task, npm, pnpm and openspec add
// the first word after the flags when it matches ^[a-z][a-z0-9-]*$ (git -C
// <dir> skips its value). Empty command → "(empty)".
func shipReportCommandName(cmd string) string {
	words := strings.Fields(cmd)
	if len(words) == 0 {
		return "(empty)"
	}
	i := 0
	for i < len(words)-1 {
		if shipReportEnvAssignRe.MatchString(words[i]) {
			i++
			continue
		}
		if words[i] == "cd" && i+3 < len(words) && words[i+2] == "&&" {
			i += 3
			continue
		}
		break
	}
	prog := filepath.Base(words[i])
	if !shipReportSubcommandPrograms[prog] {
		return prog
	}
	for j := i + 1; j < len(words); j++ {
		word := words[j]
		if prog == "git" && word == "-C" {
			j++ // skip the directory value
			continue
		}
		if strings.HasPrefix(word, "-") {
			continue
		}
		if shipReportSubcommandRe.MatchString(word) {
			return prog + " " + word
		}
		break
	}
	return prog
}

// shipReportJoin joins the non-empty parts with " · ", or returns "—" when
// there are none.
func shipReportJoin(parts []string) string {
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, " · ")
}

// ---------------------------------------------------------------------------
// Counting helpers — shared by the Summary table and the sections, so the
// two can never disagree.
// ---------------------------------------------------------------------------

// shipReportSeverities is the display order of finding severities.
var shipReportSeverities = []string{"critical", "high", "medium", "low", "info", "unknown"}

// shipReportSeverity normalizes a finding's severity to one of
// shipReportSeverities; anything else is "unknown".
func shipReportSeverity(v any) string {
	s := strings.ToLower(strings.TrimSpace(shipReportStr(v)))
	for _, known := range shipReportSeverities {
		if s == known {
			return s
		}
	}
	return "unknown"
}

// shipReportOrigin returns a fixed finding's origin, or "unknown" when blank.
func shipReportOrigin(m map[string]any) string {
	if o := strings.TrimSpace(shipReportStr(m["origin"])); o != "" {
		return shipReportShort(o, shipReportCommandMax)
	}
	return "unknown"
}

// shipReportFixedCounts is the severity × origin count of healing.fixed.
type shipReportFixedCounts struct {
	total      int
	origins    []string                  // first-seen order
	cells      map[string]map[string]int // severity → origin → count
	bySeverity map[string]int
}

func shipReportCountFixed(fixed []any) shipReportFixedCounts {
	c := shipReportFixedCounts{cells: map[string]map[string]int{}, bySeverity: map[string]int{}}
	for _, raw := range fixed {
		m, _ := raw.(map[string]any)
		sev, origin := shipReportSeverity(m["severity"]), shipReportOrigin(m)
		if c.cells[sev] == nil {
			c.cells[sev] = map[string]int{}
		}
		seen := false
		for _, o := range c.origins {
			if o == origin {
				seen = true
				break
			}
		}
		if !seen {
			c.origins = append(c.origins, origin)
		}
		c.cells[sev][origin]++
		c.bySeverity[sev]++
		c.total++
	}
	return c
}

// shipReportHardenTotals sums healing.hardened: one run per record, every
// applied edit, and every skipped count.
type shipReportHardenTotals struct {
	runs, applied, skipped int
}

func shipReportCountHardened(hardened []any) shipReportHardenTotals {
	var t shipReportHardenTotals
	for _, raw := range hardened {
		m, _ := raw.(map[string]any)
		applied, _ := m["applied"].([]any)
		skipped, _ := healingInt(m["skipped"])
		t.runs++
		t.applied += len(applied)
		t.skipped += skipped
	}
	return t
}

// shipReportGroup is one row of a CLI count table.
type shipReportGroup struct {
	name         string
	runs, failed int
}

// shipReportGroupEvidence counts evidence entries per key, in first-seen
// order.
func shipReportGroupEvidence(evidence []CLIEvidenceEntry, key func(CLIEvidenceEntry) string) []shipReportGroup {
	var groups []shipReportGroup
	index := map[string]int{}
	for _, c := range evidence {
		k := key(c)
		i, ok := index[k]
		if !ok {
			i = len(groups)
			index[k] = i
			groups = append(groups, shipReportGroup{name: k})
		}
		groups[i].runs++
		if c.ExitCode != 0 {
			groups[i].failed++
		}
	}
	return groups
}

// shipReportEvidenceWhere names where an evidence entry ran: its ship step,
// else its pipeline.
func shipReportEvidenceWhere(c CLIEvidenceEntry) string {
	if c.Step != "" {
		return c.Step
	}
	return c.Pipeline
}

// shipReportFailedEvidence returns the entries with a non-zero exit code.
func shipReportFailedEvidence(evidence []CLIEvidenceEntry) []CLIEvidenceEntry {
	var failed []CLIEvidenceEntry
	for _, c := range evidence {
		if c.ExitCode != 0 {
			failed = append(failed, c)
		}
	}
	return failed
}

// shipReportDecisionLines renders out.Decisions ("<step>: <decision>") in
// short form, dropping entries whose decision text is blank.
func shipReportDecisionLines(decisions []string) []string {
	lines := []string{}
	for _, d := range decisions {
		step, text, ok := strings.Cut(d, ": ")
		if !ok {
			if strings.TrimSpace(d) != "" {
				lines = append(lines, shipReportShort(d, shipReportTextMax))
			}
			continue
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		lines = append(lines, shipReportShort(step, shipReportCommandMax)+": "+shipReportShort(text, shipReportTextMax))
	}
	return lines
}

// shipReportLedgerDeferred sums the ledger's deferrals over every reason.
func shipReportLedgerDeferred(l *ShipReviewLedger) int {
	n := 0
	for _, v := range l.DeferredByReason {
		n += v
	}
	return n
}

// shipReportPlanDuration renders the plan's planning time.
func shipReportPlanDuration(p *ShipPlanTiming) string {
	return pipeline.Humanize(time.Duration(p.DurationMs) * time.Millisecond)
}

// ---------------------------------------------------------------------------
// Section renderers
// ---------------------------------------------------------------------------

func renderShipReportHeader(w *shipReportWriter, out ShipRunReportOut) {
	w.line("# Ship run report — %s", out.Branch)
}

// renderShipReportSummary renders the Area | Result table that holds every
// run metric in one place. Rows always come in the same order; a row whose
// source data is missing shows "—" (or "not run" for Execution, "none" for
// the healing rows).
func renderShipReportSummary(w *shipReportWriter, out ShipRunReportOut) {
	w.heading("Summary")
	w.line("| Area | Result |")
	w.line("|---|---|")
	row := func(area, result string) {
		w.line("| %s | %s |", area, shipReportCell(result))
	}

	var run []string
	if out.RunID != "" {
		run = append(run, out.RunID)
	}
	if out.Bump != "" {
		run = append(run, "bump "+out.Bump)
	}
	if out.Duration != "" {
		run = append(run, out.Duration)
	}
	row("Run", shipReportJoin(run))

	var plan []string
	if out.Plan != nil {
		plan = append(plan, "time "+shipReportPlanDuration(out.Plan))
	}
	if out.Planning != nil {
		plan = append(plan, fmt.Sprintf("decisions %d", len(out.Planning.Decisions)))
	}
	row("Plan", shipReportJoin(plan))

	completed, waits := 0, 0
	for _, s := range out.Steps {
		if s.Status == "completed" {
			completed++
		}
		if s.HumanWait {
			waits++
		}
	}
	steps := fmt.Sprintf("completed %d of %d", completed, len(out.Steps))
	if waits > 0 {
		steps += fmt.Sprintf(" · human waits %d", waits)
	}
	row("Steps", steps)

	if n := len(out.UserInputs); n == 0 {
		row("User input", "none")
	} else {
		row("User input", fmt.Sprintf("prompts %d", n))
	}

	if e := out.Execution; e == nil {
		row("Execution", "not run")
	} else {
		parts := []string{fmt.Sprintf("tasks %d of %d completed", e.CompletedTasks, e.TotalTasks), fmt.Sprintf("waves %d", len(e.Waves))}
		if e.Duration != "" {
			parts = append(parts, e.Duration)
		}
		parts = append(parts, fmt.Sprintf("issues %d", len(e.Drifts)+len(e.Errors)+len(e.Warnings)+len(e.Concerns)))
		row("Execution", shipReportJoin(parts))
	}

	if l := out.ReviewLedger; l == nil {
		row("Review", "—")
	} else {
		row("Review", fmt.Sprintf("total %d · fixed %d · deferred %d · unaccounted %d", l.Total, l.Fixed, shipReportLedgerDeferred(l), l.Unaccounted))
	}

	fixedList, _ := out.Healing["fixed"].([]any)
	if fc := shipReportCountFixed(fixedList); fc.total == 0 {
		row("Fixed by severity", "none")
	} else {
		var parts []string
		for _, sev := range shipReportSeverities {
			n := fc.bySeverity[sev]
			if n == 0 && (sev == "info" || sev == "unknown") {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s %d", sev, n))
		}
		row("Fixed by severity", shipReportJoin(parts))
	}

	hardened, _ := out.Healing["hardened"].([]any)
	if ht := shipReportCountHardened(hardened); ht.runs == 0 {
		row("Hardened", "none")
	} else {
		row("Hardened", fmt.Sprintf("runs %d · edits applied %d · skipped %d", ht.runs, ht.applied, ht.skipped))
	}

	row("Deferred", fmt.Sprintf("%d", len(out.Deferred)))
	row("Guardrail hits", fmt.Sprintf("%d", len(out.GuardrailHits)))

	cli := fmt.Sprintf("total %d · failed %d", len(out.CLIEvidence), len(shipReportFailedEvidence(out.CLIEvidence)))
	if len(out.CLIEvidence) >= maxCLIEvidenceInWindow {
		cli += fmt.Sprintf(" · latest %d only", maxCLIEvidenceInWindow)
	}
	row("CLI commands", cli)
	row("Decisions", fmt.Sprintf("%d", len(shipReportDecisionLines(out.Decisions))))
	row("Learnings", fmt.Sprintf("%d", out.LinkedLearnings))
}

// renderShipReportPlan renders the one Plan section: the plan file and its
// planning time (or why timing is missing), then the critical-decision
// table.
func renderShipReportPlan(w *shipReportWriter, out ShipRunReportOut) {
	w.heading("Plan")
	if out.Plan != nil {
		w.line("- File: %s", out.Plan.PlanFile)
		w.line("- Planning time: %s (%s → last edit %s)", shipReportPlanDuration(out.Plan), out.Plan.StartedAt, out.Plan.LastModifiedAt)
	} else {
		if out.Planning != nil && out.Planning.PlanFile != "" {
			w.line("- File: %s", out.Planning.PlanFile)
			w.line("")
		}
		note := out.PlanNote
		if note == "" {
			note = shipPlanNote
		}
		w.line("_Plan timing not available — %s._", note)
	}
	renderShipReportPlanDecisions(w, out)
}

// renderShipReportPlanDecisions renders the linked plan run's
// critical-decision table, cells in short form. A null planning renders one
// explanatory line; zero decisions render "_No critical decisions
// recorded._" in place of the table. Plan milestones are not listed here:
// they appear as plan rows in the Timeline.
func renderShipReportPlanDecisions(w *shipReportWriter, out ShipRunReportOut) {
	w.line("")
	p := out.Planning
	if p == nil {
		if out.PlanningNote == shipPlanningNoteReadFailed {
			w.line("_Plan run state could not be read — no planning data._")
		} else {
			w.line("_Plan run state not found — no planning data._")
		}
		return
	}
	if len(p.Decisions) == 0 {
		w.line("_No critical decisions recorded._")
		return
	}
	cell := func(s string) string { return shipReportCell(shipReportShort(s, shipReportCommandMax)) }
	w.line("| Decision | Chosen | Rejected | Reason |")
	w.line("|---|---|---|---|")
	for _, d := range p.Decisions {
		w.line("| %s | %s | %s | %s |",
			cell(shipReportStr(d["key"])),
			cell(shipReportStr(d["choice"])),
			cell(shipReportRejected(d["rejected"])),
			cell(shipReportStr(d["reason"])))
	}
}

// shipReportRejected renders a decision's rejected list ([{option, why}])
// as "option: why" entries joined by "; ", or "—" when the list is empty.
func shipReportRejected(v any) string {
	raw, _ := v.([]any)
	parts := make([]string, 0, len(raw))
	for _, r := range raw {
		m, _ := r.(map[string]any)
		option, why := shipReportStr(m["option"]), shipReportStr(m["why"])
		switch {
		case option != "" && why != "":
			parts = append(parts, option+": "+why)
		case option != "":
			parts = append(parts, option)
		case why != "":
			parts = append(parts, why)
		}
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, "; ")
}

// renderShipReportTimeline renders the merged plan → execute → ship events
// as a table, or "_No timed events._" when there are none.
func renderShipReportTimeline(w *shipReportWriter, events []TimelineEvent) {
	w.heading("Timeline")
	if len(events) == 0 {
		w.line("_No timed events._")
		return
	}
	w.line("| At | Phase | Event |")
	w.line("|---|---|---|")
	for _, e := range events {
		w.line("| %s | %s | %s |", shipReportCell(e.At), e.Phase, shipReportCell(shipReportShort(e.Event, shipReportTextMax)))
	}
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
			w.line("- [%s] %s", severity, shipReportShort(summary, shipReportTextMax))
		}
	}
}

// shipReportUserInputStep names where a user-input entry was typed: its ship
// step name, else "wave <n>" for an execute entry, else "—" when both are
// empty.
func shipReportUserInputStep(e UserInputEntry) string {
	if e.Step != "" {
		return e.Step
	}
	if e.Wave != nil {
		return fmt.Sprintf("wave %d", *e.Wave)
	}
	return "—"
}

// renderShipReportUserInput renders the prompts the user typed while the run
// was active: a count line (plus the read-cap note when the window was
// full), then a table At | Step | Text, oldest first. Zero entries render
// "_No user input during the run._" in place of the table.
func renderShipReportUserInput(w *shipReportWriter, inputs []UserInputEntry) {
	w.heading("User input")
	if len(inputs) == 0 {
		w.line("_No user input during the run._")
		return
	}
	count := fmt.Sprintf("%d prompts typed during the run.", len(inputs))
	if len(inputs) >= maxUserInputInWindow {
		count += " Shows the latest 100 prompts only."
	}
	w.line("%s", count)
	w.line("")
	w.line("| At | Step | Text |")
	w.line("|---|---|---|")
	for _, e := range inputs {
		w.line("| %s | %s | %s |", shipReportCell(e.Timestamp), shipReportCell(shipReportUserInputStep(e)), shipReportCell(shipReportShort(e.Text, shipReportTextMax)))
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
	if l.Unaccounted < 0 {
		w.line("- Unaccounted: %d — ledger mismatch: fixes plus deferrals exceed the review total", l.Unaccounted)
	} else {
		w.line("- Unaccounted: %d", l.Unaccounted)
	}
}

func renderShipReportHealing(w *shipReportWriter, out ShipRunReportOut) {
	w.heading("Self-healing")
	w.line("### Fixed")
	w.line("")
	fixed, _ := out.Healing["fixed"].([]any)
	renderShipReportFixed(w, fixed)

	w.line("")
	w.line("### Hardened")
	w.line("")
	hardened, _ := out.Healing["hardened"].([]any)
	renderShipReportHardened(w, hardened)

	w.line("")
	w.line("### Harden commit")
	w.line("")
	if out.HardenCommit != "" {
		w.line("- %s", shipReportShort(out.HardenCommit, shipReportTextMax))
	} else {
		w.line("_No harden commit._")
	}
}

// renderShipReportFixed renders the fixed-findings count line, the
// Severity × origin table (zero rows omitted) and one line per critical,
// high and medium finding — the ones a person must look at.
func renderShipReportFixed(w *shipReportWriter, fixed []any) {
	c := shipReportCountFixed(fixed)
	if c.total == 0 {
		w.line("_No findings fixed._")
		return
	}
	w.line("%d findings fixed.", c.total)
	w.line("")
	header, sep := "| Severity |", "|---|"
	for _, o := range c.origins {
		header += " " + shipReportCell(o) + " |"
		sep += "---|"
	}
	w.line("%s Total |", header)
	w.line("%s---|", sep)
	for _, sev := range shipReportSeverities {
		if c.bySeverity[sev] == 0 {
			continue
		}
		text := "| " + sev + " |"
		for _, o := range c.origins {
			text += fmt.Sprintf(" %d |", c.cells[sev][o])
		}
		w.line("%s %d |", text, c.bySeverity[sev])
	}

	var urgent []string
	for _, sev := range []string{"critical", "high", "medium"} {
		for _, raw := range fixed {
			m, _ := raw.(map[string]any)
			if shipReportSeverity(m["severity"]) != sev {
				continue
			}
			urgent = append(urgent, fmt.Sprintf("- [%s] %s — %s (%s)", sev,
				shipReportShort(shipReportLocation(m), shipReportTextMax),
				shipReportShort(shipReportStr(m["title"]), shipReportTextMax),
				shipReportOrigin(m)))
		}
	}
	if len(urgent) == 0 {
		return
	}
	w.line("")
	w.line("Critical, high and medium:")
	for _, l := range urgent {
		w.line("%s", l)
	}
}

// renderShipReportHardened renders the harden summary line, one table row
// per harden run, and one row per surface with its edit count and the
// distinct files it touched. A run that stopped before its "done" record
// shows shipHardenInterrupted in its Applied cell.
func renderShipReportHardened(w *shipReportWriter, hardened []any) {
	t := shipReportCountHardened(hardened)
	if t.runs == 0 {
		w.line("_No harden runs recorded._")
		return
	}
	w.line("%d runs, %d edits applied, %d skipped.", t.runs, t.applied, t.skipped)
	w.line("")
	w.line("| Trigger | Class | Applied | Skipped |")
	w.line("|---|---|---|---|")
	type surface struct {
		name  string
		edits int
		files []string
	}
	var surfaces []surface
	index := map[string]int{}
	for _, raw := range hardened {
		m, _ := raw.(map[string]any)
		applied, _ := m["applied"].([]any)
		skipped, _ := healingInt(m["skipped"])
		appliedCell := fmt.Sprintf("%d", len(applied))
		if m["phase"] != "done" {
			appliedCell = shipHardenInterrupted
		}
		w.line("| %s | %s | %s | %d |",
			shipReportCell(shipReportShort(shipReportStr(m["trigger"]), shipReportCommandMax)),
			shipReportCell(shipReportShort(shipReportStr(m["classification"]), shipReportCommandMax)),
			appliedCell, skipped)
		for _, a := range applied {
			am, _ := a.(map[string]any)
			name := shipReportShort(shipReportStr(am["surface"]), shipReportCommandMax)
			if name == "" {
				name = "unknown"
			}
			i, ok := index[name]
			if !ok {
				i = len(surfaces)
				index[name] = i
				surfaces = append(surfaces, surface{name: name})
			}
			surfaces[i].edits++
			file := shipReportShort(shipReportRelPath(shipReportStr(am["targetFile"])), shipReportCommandMax)
			if file != "" && !slices.Contains(surfaces[i].files, file) {
				surfaces[i].files = append(surfaces[i].files, file)
			}
		}
	}
	if len(surfaces) == 0 {
		return
	}
	sort.SliceStable(surfaces, func(i, j int) bool { return surfaces[i].edits > surfaces[j].edits })
	w.line("")
	w.line("| Surface | Edits | Files |")
	w.line("|---|---|---|")
	for _, s := range surfaces {
		files := "—"
		if len(s.files) > 0 {
			files = strings.Join(s.files, ", ")
		}
		w.line("| %s | %d | %s |", shipReportCell(s.name), s.edits, shipReportCell(files))
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
		w.line("- [%s] %s — %s (%s)", shipReportShort(shipReportStr(m["severity"]), shipReportCommandMax),
			shipReportShort(shipReportLocation(m), shipReportTextMax),
			shipReportShort(shipReportStr(m["title"]), shipReportTextMax),
			shipReportShort(reason, shipReportCommandMax))
	}
}

// renderShipReportExecution renders the Execution section only when this
// run's execute step completed; e is nil otherwise and nothing is written.
// Waves render as a table with a 7-character commit SHA, or "—" without one.
func renderShipReportExecution(w *shipReportWriter, e *ExecutionReportOut) {
	if e == nil {
		return
	}
	w.heading("Execution")
	w.line("- Tasks: %d completed, %d failed, %d skipped of %d", e.CompletedTasks, e.FailedTasks, e.SkippedTasks, e.TotalTasks)
	if e.Duration != "" {
		w.line("- Duration: %s", e.Duration)
	}
	w.line("")
	if len(e.Waves) == 0 {
		w.line("_No waves recorded._")
	} else {
		w.line("| Wave | Status | Tasks | Duration | Commit |")
		w.line("|---|---|---|---|---|")
		for _, wave := range e.Waves {
			dur, sha := wave.Duration, wave.CommittedSHA
			if dur == "" {
				dur = "—"
			}
			if sha == "" {
				sha = "—"
			} else if len(sha) > 7 {
				sha = sha[:7]
			}
			w.line("| %d | %s | %d | %s | %s |", wave.Number, shipReportCell(wave.Status), len(wave.Tasks), dur, shipReportCell(sha))
		}
	}
	w.line("")
	w.line("- Issues: %d drifts, %d errors, %d warnings, %d concerns", len(e.Drifts), len(e.Errors), len(e.Warnings), len(e.Concerns))
}

func renderShipReportGuardrailHits(w *shipReportWriter, hits []string) {
	w.heading("Guardrail hits")
	if len(hits) == 0 {
		w.line("_No guardrail hits._")
	}
	for _, id := range hits {
		w.line("- %s", shipReportShort(id, shipReportCommandMax))
	}
}

// renderShipReportCLIEvidence renders counts, not the command log: a count
// line, a per-step table (first-seen order), a per-command-group table (most
// runs first, top shipReportCommandRows then one "other" row), and the
// failed commands only (at most shipReportFailedCap). Successful command
// text never appears; the full log path closes the section.
func renderShipReportCLIEvidence(w *shipReportWriter, evidence []CLIEvidenceEntry) {
	w.heading("CLI evidence")
	if len(evidence) == 0 {
		w.line("_No CLI evidence recorded._")
		return
	}
	failed := shipReportFailedEvidence(evidence)
	count := fmt.Sprintf("%d commands, %d failed.", len(evidence), len(failed))
	if len(evidence) >= maxCLIEvidenceInWindow {
		count += fmt.Sprintf(" Counts cover the latest %d commands only (evidence read limit).", maxCLIEvidenceInWindow)
	}
	w.line("%s", count)

	w.line("")
	w.line("| Step | Commands | Failed |")
	w.line("|---|---|---|")
	for _, g := range shipReportGroupEvidence(evidence, shipReportEvidenceWhere) {
		w.line("| %s | %d | %d |", shipReportCell(shipReportShort(g.name, shipReportCommandMax)), g.runs, g.failed)
	}

	groups := shipReportGroupEvidence(evidence, func(c CLIEvidenceEntry) string { return shipReportCommandName(c.Command) })
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].runs > groups[j].runs })
	w.line("")
	w.line("| Command | Runs | Failed |")
	w.line("|---|---|---|")
	for i, g := range groups {
		if i == shipReportCommandRows {
			rest := shipReportGroup{}
			for _, o := range groups[i:] {
				rest.runs += o.runs
				rest.failed += o.failed
			}
			w.line("| other (%d kinds) | %d | %d |", len(groups)-i, rest.runs, rest.failed)
			break
		}
		w.line("| %s | %d | %d |", shipReportCell(shipReportShort(g.name, shipReportCommandMax)), g.runs, g.failed)
	}

	w.line("")
	if len(failed) == 0 {
		w.line("_No failed commands._")
	} else {
		w.line("Failed commands:")
		for i, c := range failed {
			if i == shipReportFailedCap {
				w.line("- … %d more", len(failed)-i)
				break
			}
			w.line("- %s — exit %d (%s)", shipReportCode(c.Command), c.ExitCode, shipReportShort(shipReportEvidenceWhere(c), shipReportCommandMax))
		}
	}

	w.line("")
	w.line("Full log: %s", filepath.ToSlash(filepath.Join(paths.DataDir, paths.EvidenceSubdir, "cli-executions.jsonl")))
}

// renderShipReportDecisions renders one short-form line per decision;
// decisions with blank text are dropped.
func renderShipReportDecisions(w *shipReportWriter, decisions []string) {
	w.heading("Decisions")
	lines := shipReportDecisionLines(decisions)
	if len(lines) == 0 {
		w.line("_No decisions recorded._")
	}
	for _, l := range lines {
		w.line("- %s", l)
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
