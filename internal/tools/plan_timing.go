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
	maxStepTimesStored = 64

	// planDoneNext is the next text of the done marker. Step 7 prints the
	// dispatch report and the style report before done, so next names only
	// the branch.
	planDoneNext = "Print timing verbatim. Then continue Step 7 with the plan-mode or normal-mode branch."

	planTimingTopAgents = 3
	planTimingSep       = " · "
)

// PlanStepTime is one stepTimes entry: the first checkpoint of a step and
// iteration. At is RFC 3339 UTC.
type PlanStepTime struct {
	Step      string `json:"step"`
	Iteration int    `json:"iteration"`
	At        string `json:"at"`
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

// stepTimeFields decodes one stepTimes entry. reason is "" for a valid entry
// and names the first bad field otherwise.
func stepTimeFields(v any) (step string, iteration int, at, reason string) {
	m, ok := v.(map[string]any)
	if !ok {
		return "", 0, "", "not an object"
	}
	step, ok = m["step"].(string)
	if !ok || step == "" {
		return "", 0, "", "step is not a non-empty string"
	}
	n, ok := m["iteration"].(float64)
	if !ok || n < 0 || n > math.MaxInt32 || n != math.Trunc(n) {
		return "", 0, "", "iteration is not a whole number of 0 or more"
	}
	at, _ = m["at"].(string)
	if _, ok := planParseTime(at); !ok {
		return "", 0, "", "at is not RFC 3339"
	}
	return step, int(n), at, ""
}

// upsertStepTime returns the stepTimes value to store and a warning ("" when
// none). An entry that matches step and iteration keeps the list as it is:
// the first checkpoint of a step and iteration owns its time. A new entry is
// added unless the list holds maxStepTimesStored entries. A bad entry stays
// in the list and never matches. A raw value that is not a list is returned
// unchanged with a warning.
func upsertStepTime(raw any, step string, iteration int, at string) (list any, warning string) {
	entries, ok := stepTimesList(raw)
	if !ok {
		return raw, "stepTimes is not a list: step time not recorded"
	}
	for _, e := range entries {
		s, i, _, reason := stepTimeFields(e)
		if reason == "" && s == step && i == iteration {
			return entries, ""
		}
	}
	if len(entries) >= maxStepTimesStored {
		return entries, fmt.Sprintf("step time not recorded: %d entries stored", len(entries))
	}
	return append(entries, PlanStepTime{Step: step, Iteration: iteration, At: at}), ""
}

// recordStepTime upserts the checkpoint into st's stepTimes list and returns
// the warnings for the plan_mark output. The caller sets cp.UpdatedAt first.
func recordStepTime(st *state.State, cp PlanCheckpoint) []string {
	list, warn := upsertStepTime(st.Data[planStepTimesKey], cp.Step, cp.Iteration, cp.UpdatedAt)
	st.Data[planStepTimesKey] = list
	if warn == "" {
		return nil
	}
	return []string{warn}
}

// ---------------------------------------------------------------------------
// done side: read the data, then build the report
// ---------------------------------------------------------------------------

// attachPlanDoneTiming adds the timing report, its warnings and the done
// next text to the output of the done marker.
func attachPlanDoneTiming(out *PlanMarkOut, mainRoot string, st *state.State) {
	timing, warns := planDoneTiming(mainRoot, st)
	out.Timing = timing
	out.Warnings = append(out.Warnings, warns...)
	out.Next = planDoneNext
}

// planDoneTiming builds the timing report for st and the warnings for the
// data it skipped. It reads planIntegrity, stepTimes and the run's evidence
// writer files, and finds the evidence folder as writeGuardrailsFile does.
// The done stamp is already in st.Data when it runs. It never fails: an
// evidence folder that cannot be listed adds a warning and the agents line
// shows unknown.
func planDoneTiming(mainRoot string, st *state.State) (timing string, warnings []string) {
	integrity, _ := st.Data["planIntegrity"].(map[string]any)
	startAt, _ := integrity["skillInvoked"].(string)
	doneAt, _ := integrity["done"].(string)
	steps := st.Data[planStepTimesKey]

	warnings = append(warnings, parseStepTimes(steps).warnings...)
	writers, writerWarns, err := planReadWriterSpans(st)
	if err != nil {
		warnings = append(warnings, "timing: cannot read evidence: "+planErrText(err))
	}
	warnings = append(warnings, writerWarns...)
	return planTimingText(startAt, doneAt, steps, writers, err == nil), warnings
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

// planReadWriterSpans lists the run's evidence writer files and returns the
// spans for the report plus one warning for each file it skips. Writer main
// is not an agent and is left out silently. The error is the listing error.
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
			warnings = append(warnings, fmt.Sprintf("timing: writer %s skipped: unreadable file", w.id))
			continue
		}
		span := planWriterSpan{WriterID: w.id, Status: w.file.Status, StartedAt: w.file.StartedAt, UpdatedAt: w.file.UpdatedAt}
		if _, _, reason := planWriterDuration(span); reason != "" {
			warnings = append(warnings, fmt.Sprintf("timing: writer %s skipped: %s", w.id, reason))
		}
		spans = append(spans, span)
	}
	return spans, warnings, nil
}

// ---------------------------------------------------------------------------
// report (pure)
// ---------------------------------------------------------------------------

// planTimingReport is the Step 7 timing report as plain text. startAt is
// planIntegrity.skillInvoked, doneAt is the done stamp, steps is the stored
// stepTimes value and writers are the run's evidence writer spans. Absent or
// bad data prints unknown in its own line; the report is never empty.
func planTimingReport(startAt, doneAt string, steps any, writers []planWriterSpan) string {
	return planTimingText(startAt, doneAt, steps, writers, true)
}

// planTimingText builds the report. writersKnown is false when the evidence
// folder could not be listed: the agents line then shows unknown, not none.
func planTimingText(startAt, doneAt string, steps any, writers []planWriterSpan, writersKnown bool) string {
	c := newPlanClock(startAt, doneAt)
	lines := []string{"Step 7: Timing (wall clock, UTC)", "  " + planTotalLine(c)}
	for _, l := range planStepsLines(parseStepTimes(steps), c) {
		lines = append(lines, "  "+l)
	}
	lines = append(lines, "  "+planAgentsLine(writers, writersKnown))
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
// minutes, else "<m>m".
func planFormatMinutes(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	if m >= 60 {
		return fmt.Sprintf("%dh %dm", m/60, m%60)
	}
	return fmt.Sprintf("%dm", m)
}

// planFormatSeconds rounds d to whole seconds: "<m>m <s>s".
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

// parseStepTimes reads the stepTimes value. A bad entry is skipped with a
// warning (n is its position in the list, from 0). Of several entries for one
// step and iteration the first is used. An entry with a time before the time
// of the entry before it is kept, marked not ordered, and warned about.
func parseStepTimes(raw any) planStepsParse {
	list, ok := stepTimesList(raw)
	if !ok {
		return planStepsParse{notList: true}
	}
	p := planStepsParse{stored: len(list)}
	seen := map[string]bool{}
	var last time.Time
	for n, e := range list {
		step, iter, at, reason := stepTimeFields(e)
		if reason != "" {
			p.warnings = append(p.warnings, fmt.Sprintf("timing: stepTimes entry %d skipped: %s", n, reason))
			continue
		}
		key := fmt.Sprintf("%s\x00%d", step, iter)
		if seen[key] {
			continue
		}
		seen[key] = true
		t, _ := planParseTime(at)
		ent := planStepEntry{label: planStepLabel(step, iter), step: step, iter: iter, at: t, ordered: !t.Before(last)}
		if ent.ordered {
			last = t
		} else {
			p.warnings = append(p.warnings, fmt.Sprintf("timing: step %s skipped: time before the previous step", ent.label))
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
// status running and a writer with no startedAt. reason is "bad time" or
// "end before start" for a writer whose times cannot give a span.
func planWriterDuration(w planWriterSpan) (d time.Duration, counted bool, reason string) {
	if w.WriterID == evidenceMainWriter || w.Status == evidenceStatusRunning || w.StartedAt == "" {
		return 0, false, ""
	}
	start, startOK := planParseTime(w.StartedAt)
	end, endOK := planParseTime(w.UpdatedAt)
	if !startOK || !endOK {
		return 0, false, "bad time"
	}
	if end.Before(start) {
		return 0, false, "end before start"
	}
	return end.Sub(start), true, ""
}

type planAgent struct {
	id string
	d  time.Duration
}

// planRankAgents returns the planTimingTopAgents longest agents, the longest
// first. Equal times sort by writer id.
func planRankAgents(writers []planWriterSpan) []planAgent {
	var ranked []planAgent
	for _, w := range writers {
		if d, counted, _ := planWriterDuration(w); counted {
			ranked = append(ranked, planAgent{id: w.WriterID, d: d})
		}
	}
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
func planAgentsLine(writers []planWriterSpan, known bool) string {
	if !known {
		return "Slowest agents: unknown"
	}
	ranked := planRankAgents(writers)
	if len(ranked) == 0 {
		return "Slowest agents: none"
	}
	parts := make([]string, 0, len(ranked))
	for _, a := range ranked {
		parts = append(parts, a.id+" "+planFormatSeconds(a.d))
	}
	return "Slowest agents: " + strings.Join(parts, planTimingSep)
}
