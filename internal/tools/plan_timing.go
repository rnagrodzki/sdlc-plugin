package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// Step times and the Step 7 timing report.
//
// The first plan_mark checkpoint of each step and iteration adds one
// {step, iteration, at} entry to the top-level stepTimes list of the plan
// state. The done marker reads that list, planIntegrity and the run's
// evidence writer files, and returns the timing report as plain text. The
// report is built by pure functions: the tests pass fixed times and need no
// clock seam.

const (
	// planStepTimesKey is the top-level plan state key of the step time list.
	// It is not inside checkpoint or planIntegrity.
	planStepTimesKey = "stepTimes"
	// maxStepTimesStored caps the list. A 5-round run writes about 20 entries.
	// The checkpoint adds no entry past it, and the report reads no entry past
	// it.
	maxStepTimesStored = 64

	// planDoneNext is the next text of the done marker. Step 7 prints the
	// dispatch report and the style report before done, so next names only
	// the branch.
	planDoneNext = "Print timing verbatim. Then continue Step 7 with the plan-mode or normal-mode branch."

	planTimingTopAgents = 3
	planTimingSep       = " · "

	// planTimeExample is an RFC 3339 example for the warnings.
	planTimeExample = "2026-10-23T12:00:00Z"
	// planStepTimesExample is the stepTimes shape for the warnings.
	planStepTimesExample = `[{"step":"2","iteration":0,"at":"` + planTimeExample + `"}]`

	// Status values of PlanStepTimeResult.
	stepTimeAdded       = "added"
	stepTimeKept        = "kept"
	stepTimeNotRecorded = "not recorded"
)

// PlanStepTime is one stepTimes entry: the first checkpoint of a step and
// iteration. At is RFC 3339 UTC.
type PlanStepTime struct {
	Step      string `json:"step"`
	Iteration int    `json:"iteration"`
	At        string `json:"at"`
}

// PlanStepTimeResult is the stepTime field of a checkpoint result: what the
// call did to stepTimes. Status is added (this call stored the entry), kept
// (an earlier entry for this step and iteration owns its time; At is that
// entry's time) or not recorded (see the warnings; At is empty).
type PlanStepTimeResult struct {
	Step      string `json:"step"`
	Iteration int    `json:"iteration"`
	At        string `json:"at"`
	Status    string `json:"status"`
}

// planWriterSpan is the part of one evidence writer file that the report
// reads.
type planWriterSpan struct{ WriterID, Status, StartedAt, UpdatedAt string }

// ---------------------------------------------------------------------------
// stepTimes list (checkpoint side)
// ---------------------------------------------------------------------------

// stepTimesList normalizes a stored stepTimes value to a list. raw is absent
// (nil, an empty list), the []any a state file decodes to, or a typed slice.
// ok is false when raw is not a list.
func stepTimesList(raw any) (list []any, ok bool) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, false
	}
	return list, true
}

// jsonTypeName names the JSON type of a decoded value, for the warnings.
func jsonTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64, int, int64:
		return "number"
	case map[string]any:
		return "object"
	case []any:
		return "list"
	}
	return fmt.Sprintf("%T", v)
}

// stepTimeFields decodes one stepTimes entry. reason is "" for a valid entry
// and names the first bad field otherwise.
func stepTimeFields(v any) (entry PlanStepTime, reason string) {
	m, ok := v.(map[string]any)
	if !ok {
		return PlanStepTime{}, "not an object"
	}
	step, ok := m["step"].(string)
	if !ok || step == "" {
		return PlanStepTime{}, "step is not a non-empty string"
	}
	n, ok := m["iteration"].(float64)
	if !ok || n < 0 || n > math.MaxInt32 || n != math.Trunc(n) {
		return PlanStepTime{}, "iteration is not a whole number of 0 or more"
	}
	at, _ := m["at"].(string)
	if _, ok := planParseTime(at); !ok {
		return PlanStepTime{}, "at is not RFC 3339"
	}
	return PlanStepTime{Step: step, Iteration: int(n), At: at}, ""
}

// upsertStepTime returns the stepTimes value to store, what the call did, and
// a warning ("" when none). statePath names the state file in the warnings.
// An entry that matches step and iteration keeps the list as it is: the first
// checkpoint of a step and iteration owns its time. A new entry is added
// unless the list holds maxStepTimesStored entries. A bad entry stays in the
// list and never matches. A raw value that is not a list is returned
// unchanged with a warning.
func upsertStepTime(raw any, step string, iteration int, at, statePath string) (list any, result PlanStepTimeResult, warning string) {
	result = PlanStepTimeResult{Step: step, Iteration: iteration, Status: stepTimeNotRecorded}
	entries, ok := stepTimesList(raw)
	if !ok {
		return raw, result, fmt.Sprintf("Step time not recorded for step %s iteration %d: stepTimes in %s is a %s, not a list. "+
			"Expected a list such as %s. The Step 7 timing report shows Steps: unknown. "+
			"To fix it, remove the stepTimes key from that file.", step, iteration, statePath, jsonTypeName(raw), planStepTimesExample)
	}
	for _, e := range entries {
		if s, reason := stepTimeFields(e); reason == "" && s.Step == step && s.Iteration == iteration {
			result.At, result.Status = s.At, stepTimeKept
			return entries, result, ""
		}
	}
	if len(entries) >= maxStepTimesStored {
		return entries, result, fmt.Sprintf("Step time not recorded for step %s iteration %d: stepTimes holds %d entries, the limit (%d). "+
			"The Steps line of the Step 7 timing report leaves this step out.", step, iteration, len(entries), maxStepTimesStored)
	}
	result.At, result.Status = at, stepTimeAdded
	return append(entries, PlanStepTime{Step: step, Iteration: iteration, At: at}), result, ""
}

// recordStepTime upserts the checkpoint into st's stepTimes list and returns
// the stepTime result and the warnings for the plan_mark output. The caller
// sets cp.UpdatedAt first.
func recordStepTime(st *state.State, cp PlanCheckpoint) (*PlanStepTimeResult, []string) {
	list, result, warn := upsertStepTime(st.Data[planStepTimesKey], cp.Step, cp.Iteration, cp.UpdatedAt, st.Path)
	st.Data[planStepTimesKey] = list
	if warn == "" {
		return &result, nil
	}
	return &result, []string{warn}
}

// ---------------------------------------------------------------------------
// done side: read the data, then build the report
// ---------------------------------------------------------------------------

// attachPlanDoneTiming adds the timing report, its warnings and the done
// next text to the output of the done marker.
func attachPlanDoneTiming(out *PlanMarkOut, st *state.State) {
	timing, warns := planDoneTiming(st)
	out.Timing = timing
	out.Warnings = append(out.Warnings, warns...)
	out.Next = planDoneNext
}

// planDoneTiming builds the timing report for st and the warnings for the
// data it skipped. It reads planIntegrity, stepTimes and the run's evidence
// writer files (in the evidence folder of st.Root). The done stamp is already
// in st.Data when it runs. It never fails: an evidence folder that cannot be
// listed adds a warning and the agents line shows unknown. Each input is
// parsed once.
func planDoneTiming(st *state.State) (timing string, warnings []string) {
	integrity, _ := st.Data["planIntegrity"].(map[string]any)
	startAt, _ := integrity["skillInvoked"].(string)
	doneAt, _ := integrity["done"].(string)

	steps := parseStepTimes(st.Data[planStepTimesKey])
	if steps.notList {
		warnings = append(warnings, fmt.Sprintf("Timing: stepTimes in %s is a %s, not a list, so the Steps line shows unknown. "+
			"Expected a list such as %s. To fix it, remove the stepTimes key from that file.",
			st.Path, jsonTypeName(st.Data[planStepTimesKey]), planStepTimesExample))
	}
	warnings = append(warnings, steps.warnings...)

	spans, readWarns, err := planReadWriterSpans(st)
	if err != nil {
		warnings = append(warnings, "Timing: cannot read evidence: "+planErrText(err))
	}
	warnings = append(warnings, readWarns...)
	agents, agentWarns := planWriterAgents(spans)
	warnings = append(warnings, agentWarns...)
	return planTimingText(startAt, doneAt, steps, agents, err == nil), warnings
}

// planErrText is err's message plus the message of its cause. The InfraError
// of the evidence store keeps the OS error in its cause only.
func planErrText(err error) string {
	msg := err.Error()
	if cause := errors.Unwrap(err); cause != nil {
		msg += ": " + cause.Error()
	}
	return msg
}

// planReadWriterSpans lists the run's evidence writer files and returns their
// spans plus one warning for each file it cannot read. Writer main is not an
// agent and is left out silently. The error is the listing error; a missing
// folder lists no writer and is not an error.
func planReadWriterSpans(st *state.State) (spans []planWriterSpan, warnings []string, err error) {
	runID := state.RunID(st)
	writers, err := evidenceListWriters(state.EvidenceDir(st.Root, runID), runID)
	if err != nil {
		return nil, nil, err
	}
	spans = []planWriterSpan{}
	for _, w := range writers {
		if w.id == evidenceMainWriter {
			continue
		}
		if w.unreadable {
			warnings = append(warnings, fmt.Sprintf("Timing: writer %s skipped: unreadable file", w.id))
			continue
		}
		spans = append(spans, planWriterSpan{WriterID: w.id, Status: w.file.Status, StartedAt: w.file.StartedAt, UpdatedAt: w.file.UpdatedAt})
	}
	return spans, warnings, nil
}

// planWriterAgents computes the run time of each writer once. It returns the
// writers that can rank and one warning for each writer whose times cannot
// give a span.
func planWriterAgents(spans []planWriterSpan) (agents []planAgent, warnings []string) {
	for _, w := range spans {
		d, counted, reason := planWriterDuration(w)
		if reason != "" {
			warnings = append(warnings, fmt.Sprintf("Timing: writer %s skipped: %s", w.WriterID, reason))
		}
		if counted {
			agents = append(agents, planAgent{id: w.WriterID, d: d})
		}
	}
	return agents, warnings
}

// ---------------------------------------------------------------------------
// report (pure)
// ---------------------------------------------------------------------------

// planTimingText is the Step 7 timing report as plain text. startAt is
// planIntegrity.skillInvoked, doneAt is the done stamp, steps is the parsed
// stepTimes value and agents are the writers that can rank. writersKnown is
// false when the evidence folder could not be listed: the agents line then
// shows unknown, not none. Absent or bad data prints unknown in its own line;
// the report is never empty.
func planTimingText(startAt, doneAt string, steps planStepsParse, agents []planAgent, writersKnown bool) string {
	c := newPlanClock(startAt, doneAt)
	lines := []string{"Step 7: Timing (wall clock, UTC)", "  " + planTotalLine(c)}
	for _, l := range planStepsLines(steps, c) {
		lines = append(lines, "  "+l)
	}
	lines = append(lines, "  "+planAgentsLine(agents, writersKnown))
	return strings.Join(lines, "\n")
}

// planClock holds the two run stamps. An ok flag is false for a stamp that
// is absent or not RFC 3339.
type planClock struct {
	start, done     time.Time
	startOK, doneOK bool
}

func newPlanClock(startAt, doneAt string) planClock {
	var c planClock
	c.start, c.startOK = planParseTime(startAt)
	c.done, c.doneOK = planParseTime(doneAt)
	return c
}

func planParseTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

// planTotalLine is the Total line: start to the done stamp.
func planTotalLine(c planClock) string {
	if !c.startOK || !c.doneOK || c.done.Before(c.start) {
		return "Total: unknown"
	}
	return fmt.Sprintf("Total: %s (%s to %s)", planFormatMinutes(c.done.Sub(c.start)),
		c.start.UTC().Format(time.RFC3339), c.done.UTC().Format(time.RFC3339))
}

// planFormatMinutes rounds d to the nearest minute: "<h>h <m>m" from 60
// minutes, else "<m>m". pipeline.Humanize is not used: it prints seconds
// below an hour ("12m 05s"), and a step line needs minutes only.
func planFormatMinutes(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	if m >= 60 {
		return fmt.Sprintf("%dh %dm", m/60, m%60)
	}
	return fmt.Sprintf("%dm", m)
}

// planFormatSeconds rounds d to whole seconds: "<m>m <s>s". pipeline.Humanize
// is not used: it switches to hours at 60 minutes and drops the seconds.
func planFormatSeconds(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%dm %ds", s/60, s%60)
}

// planStepEntry is one valid stepTimes entry of the report. ordered is false
// when its time is before the time of the entry before it.
type planStepEntry struct {
	label   string
	step    string
	iter    int
	at      time.Time
	ordered bool
}

// planStepsParse is the stepTimes value read for the report. notList is true
// when the value is not a list. stored is the length of the list.
type planStepsParse struct {
	entries  []planStepEntry
	warnings []string
	stored   int
	notList  bool
}

// planStepLabel is the step name for iteration 0 and "<step> r<iteration>"
// from iteration 1.
func planStepLabel(step string, iteration int) string {
	if iteration == 0 {
		return step
	}
	return fmt.Sprintf("%s r%d", step, iteration)
}

// parseStepTimes reads the stepTimes value. Only the first
// maxStepTimesStored entries are read, so a damaged list cannot make the
// report or its warnings unbounded. A bad entry is skipped with a warning (n
// is its position in the list, from 0). Of several entries for one step and
// iteration the first is used. An entry with a time before the time of the
// entry before it is kept, shown as unknown, and warned about.
func parseStepTimes(raw any) planStepsParse {
	list, ok := stepTimesList(raw)
	if !ok {
		return planStepsParse{notList: true}
	}
	p := planStepsParse{stored: len(list)}
	if len(list) > maxStepTimesStored {
		p.warnings = append(p.warnings, fmt.Sprintf("Timing: stepTimes holds %d entries; the report reads the first %d only.", len(list), maxStepTimesStored))
		list = list[:maxStepTimesStored]
	}
	seen := map[string]bool{}
	var last time.Time
	for n, e := range list {
		s, reason := stepTimeFields(e)
		if reason != "" {
			p.warnings = append(p.warnings, fmt.Sprintf("Timing: stepTimes entry %d skipped: %s", n, reason))
			continue
		}
		key := fmt.Sprintf("%s\x00%d", s.Step, s.Iteration)
		if seen[key] {
			continue
		}
		seen[key] = true
		t, _ := planParseTime(s.At)
		ent := planStepEntry{label: planStepLabel(s.Step, s.Iteration), step: s.Step, iter: s.Iteration, at: t, ordered: !t.Before(last)}
		if ent.ordered {
			last = t
		} else {
			p.warnings = append(p.warnings, fmt.Sprintf("Timing: step %s is shown as unknown: its time %s is before %s, the time of the step before it",
				ent.label, t.UTC().Format(time.RFC3339), last.UTC().Format(time.RFC3339)))
		}
		p.entries = append(p.entries, ent)
	}
	return p
}

// planStepsLines is the Steps line, plus a note line when the list is full.
func planStepsLines(p planStepsParse, c planClock) []string {
	if p.notList {
		return []string{"Steps: unknown (stepTimes is not a list)"}
	}
	lines := []string{"Steps: unknown (no step times recorded)"}
	if len(p.entries) > 0 {
		parts := make([]string, 0, len(p.entries))
		for i, e := range p.entries {
			text := "unknown"
			if d, ok := planStepSpan(p.entries, i, c); ok {
				text = planFormatMinutes(d)
			}
			parts = append(parts, e.label+" "+text)
		}
		lines[0] = "Steps: " + strings.Join(parts, planTimingSep)
	}
	if p.stored >= maxStepTimesStored {
		lines = append(lines, fmt.Sprintf("Steps: first %d checkpoints only", maxStepTimesStored))
	}
	return lines
}

// planStepSpan is the time of entries[i]. Step 0 writes its checkpoint at its
// end, so it runs from skillInvoked to its entry. Any other step runs from
// its entry to the next ordered entry, or to the done stamp for the last one.
// ok is false when a needed time is missing or the span is negative.
func planStepSpan(entries []planStepEntry, i int, c planClock) (time.Duration, bool) {
	e := entries[i]
	if !e.ordered {
		return 0, false
	}
	start, end, ok := e.at, c.done, c.doneOK
	if e.step == "0" && e.iter == 0 {
		start, end, ok = c.start, e.at, c.startOK
	} else {
		for _, next := range entries[i+1:] {
			if next.ordered {
				end, ok = next.at, true
				break
			}
		}
	}
	if !ok || end.Before(start) {
		return 0, false
	}
	return end.Sub(start), true
}

// planWriterDuration is the run time of one agent: startedAt to updatedAt.
// counted is false for a writer that never ranks: writer main, a writer with
// status running and a writer with no startedAt. reason names the bad field,
// its value and the expected format for a writer whose times cannot give a
// span. pipeline.Duration is not used: it reports a bad stamp without saying
// which one, and the warning must name the field.
func planWriterDuration(w planWriterSpan) (d time.Duration, counted bool, reason string) {
	if w.WriterID == evidenceMainWriter || w.Status == evidenceStatusRunning || w.StartedAt == "" {
		return 0, false, ""
	}
	start, ok := planParseTime(w.StartedAt)
	if !ok {
		return 0, false, fmt.Sprintf("startedAt %q is not RFC 3339 (example: %s)", w.StartedAt, planTimeExample)
	}
	end, ok := planParseTime(w.UpdatedAt)
	if !ok {
		return 0, false, fmt.Sprintf("updatedAt %q is not RFC 3339 (example: %s)", w.UpdatedAt, planTimeExample)
	}
	if end.Before(start) {
		return 0, false, fmt.Sprintf("updatedAt %s is before startedAt %s", w.UpdatedAt, w.StartedAt)
	}
	return end.Sub(start), true, ""
}

type planAgent struct {
	id string
	d  time.Duration
}

// planRankAgents returns the planTimingTopAgents longest agents, the longest
// first. Equal times sort by writer id.
func planRankAgents(agents []planAgent) []planAgent {
	ranked := append([]planAgent(nil), agents...)
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].d != ranked[j].d {
			return ranked[i].d > ranked[j].d
		}
		return ranked[i].id < ranked[j].id
	})
	if len(ranked) > planTimingTopAgents {
		ranked = ranked[:planTimingTopAgents]
	}
	return ranked
}

// planAgentsLine is the Slowest agents line.
func planAgentsLine(agents []planAgent, known bool) string {
	if !known {
		return "Slowest agents: unknown"
	}
	ranked := planRankAgents(agents)
	if len(ranked) == 0 {
		return "Slowest agents: none"
	}
	parts := make([]string, 0, len(ranked))
	for _, a := range ranked {
		parts = append(parts, a.id+" "+planFormatSeconds(a.d))
	}
	return "Slowest agents: " + strings.Join(parts, planTimingSep)
}
