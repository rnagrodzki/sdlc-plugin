package pipeline

import (
	"fmt"
	"strings"
	"time"
)

// StepRow is one pipeline step's status/timing data, as rendered by
// StepProgressBlock. Name doubles as the TimingsStore lookup key (e.g.
// "execute", "review", "await-remote-review") — it mirrors the "name"
// field of ship-state.schema.json's steps[] array, which uses the same
// step identifiers TimingsStore.Record is called with elsewhere in the
// pipeline. Status is one of "pending", "in_progress", "completed",
// "skipped", "failed" (the same enum ship-state.schema.json uses for
// steps[].status).
type StepRow struct {
	Name        string
	Status      string
	StartedAt   string // RFC3339; empty if not yet started
	CompletedAt string // RFC3339; empty if not yet completed
}

// WaveTask is one task's summary line within a wave, as rendered by
// WaveStartBlock and WaveEndBlock.
type WaveTask struct {
	ID    int
	Name  string
	Model string // model tier the task ran under, e.g. "haiku", "sonnet", "opus"
}

// WaveInfo is the wave-level data rendered by WaveStartBlock and
// WaveEndBlock.
type WaveInfo struct {
	Number      int
	Tasks       []WaveTask
	StartedAt   string // RFC3339; empty before the wave starts
	CompletedAt string // RFC3339; empty until the wave finishes
}

// Bearings is the resume-time state rendered by ResumeBriefing: what
// already finished, how long execution has been running, and what a
// --resume invocation intends to redo or skip.
type Bearings struct {
	LastCompletedWave int
	LastCompletedStep string
	ElapsedSeconds    int // wall-clock seconds since execution started; <= 0 means unknown and is omitted from the render
	GitCrossCheck     string
	WillRedo          []string
	WillSkip          []string
}

// ConfigStep is one row of a pipeline's static configuration, as rendered
// by PipelineTable — the ordered list of steps a pipeline will run,
// independent of any particular execution's status. Name matches the
// enum ship-state.schema.json uses for steps[].name (e.g. "execute",
// "review", "await-remote-review").
type ConfigStep struct {
	Name        string
	Description string
	Model       string // empty when the step is not model-driven
	Optional    bool   // true if the step can be skipped (e.g. via --skip)
}

// waveTimingKey is the single TimingsStore key WaveStartBlock and
// WaveEndBlock use for wave-duration ETA lookups. All waves share this one
// key rather than one key per wave number, because wave composition (and
// therefore count) varies per plan. A caller that wants wave ETAs to
// populate MUST record wave durations via ts.Record(waveTimingKey, d)
// using this exact key.
const waveTimingKey = "wave"

// stepGlyph maps a StepRow.Status to its display glyph. Unrecognized
// statuses render as "?" rather than panicking or being silently dropped.
func stepGlyph(status string) string {
	switch status {
	case "completed":
		return "✓"
	case "failed":
		return "✗"
	case "in_progress":
		return "▶"
	case "skipped":
		return "⊘"
	case "pending":
		return "○"
	default:
		return "?"
	}
}

// nextPendingStep returns the first step whose status is "pending" or
// "in_progress" — the step StepProgressBlock's footer treats as "next".
// Returns nil when every step is in a terminal status (completed, failed,
// skipped) or steps is empty.
func nextPendingStep(steps []StepRow) *StepRow {
	for i := range steps {
		switch steps[i].Status {
		case "pending", "in_progress":
			return &steps[i]
		}
	}
	return nil
}

// StepProgressBlock renders one row per step (glyph, name, duration when
// both timestamps are known) followed by a footer naming the next
// non-terminal step and its ETA, when TimingsStore has a recorded
// estimate for it. A nil ts, a step with no recorded estimate, or missing
// timestamps are all handled by omitting the corresponding piece — never
// by rendering a fabricated "0s".
func StepProgressBlock(steps []StepRow, ts *TimingsStore) string {
	var b strings.Builder
	b.WriteString("## Pipeline Steps\n\n")

	for _, s := range steps {
		b.WriteString(fmt.Sprintf("%s %s", stepGlyph(s.Status), s.Name))
		if d, ok := Duration(s.StartedAt, s.CompletedAt); ok {
			b.WriteString(fmt.Sprintf(" (%s)", Humanize(d)))
		}
		b.WriteByte('\n')
	}

	if next := nextPendingStep(steps); next != nil {
		b.WriteString(fmt.Sprintf("\nNext: %s", next.Name))
		if ts != nil {
			if est, ok := ts.Estimate(next.Name); ok {
				b.WriteString(fmt.Sprintf(" — ETA ~%s (%s)", Humanize(time.Duration(est.Seconds)*time.Second), est.Basis))
			}
		}
		b.WriteByte('\n')
	}

	return b.String()
}

// waveTaskLines renders the "- Task <id>: <name> (<model>)" list shared by
// WaveStartBlock and WaveEndBlock.
func waveTaskLines(tasks []WaveTask) string {
	var b strings.Builder
	for _, t := range tasks {
		b.WriteString(fmt.Sprintf("- Task %d: %s (%s)\n", t.ID, t.Name, t.Model))
	}
	return b.String()
}

// WaveStartBlock renders a wave's number and task list (with the model
// each task runs under), followed by an ETA for the wave's total duration
// when TimingsStore has a recorded estimate under waveTimingKey. A nil ts
// or a missing estimate omits the ETA line entirely rather than showing
// "0s".
func WaveStartBlock(w WaveInfo, ts *TimingsStore) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Wave %d\n\n", w.Number))
	b.WriteString(waveTaskLines(w.Tasks))

	if ts != nil {
		if est, ok := ts.Estimate(waveTimingKey); ok {
			b.WriteString(fmt.Sprintf("\nETA: ~%s (%s)\n", Humanize(time.Duration(est.Seconds)*time.Second), est.Basis))
		}
	}

	return b.String()
}

// WaveEndBlock renders a wave's number and task list (with the model each
// task ran under), followed by the wave's actual duration — and, when
// TimingsStore has a recorded estimate under waveTimingKey, that estimate
// alongside it for comparison. Missing timestamps omit the duration line
// entirely rather than showing "0s"; a nil ts or missing estimate omits
// just the estimate parenthetical.
func WaveEndBlock(w WaveInfo, ts *TimingsStore) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Wave %d\n\n", w.Number))
	b.WriteString(waveTaskLines(w.Tasks))

	if d, ok := Duration(w.StartedAt, w.CompletedAt); ok {
		b.WriteString(fmt.Sprintf("\nDuration: %s", Humanize(d)))
		if ts != nil {
			if est, ok := ts.Estimate(waveTimingKey); ok {
				b.WriteString(fmt.Sprintf(" (estimated ~%s, %s)", Humanize(time.Duration(est.Seconds)*time.Second), est.Basis))
			}
		}
		b.WriteByte('\n')
	}

	return b.String()
}

// ResumeBriefing renders the last completed step/wave, elapsed time since
// execution started, a git cross-check status line, and the sets of steps
// a --resume invocation will redo vs. skip. ElapsedSeconds <= 0 omits the
// elapsed line rather than showing "0s"; an empty GitCrossCheck renders as
// "unknown"; empty WillRedo/WillSkip sets omit their section entirely.
func ResumeBriefing(b Bearings) string {
	var out strings.Builder
	out.WriteString("## Resuming Execution\n\n")

	if b.LastCompletedStep != "" {
		out.WriteString(fmt.Sprintf("Last completed: Wave %d, step %q\n", b.LastCompletedWave, b.LastCompletedStep))
	} else {
		out.WriteString("Last completed: none\n")
	}

	if b.ElapsedSeconds > 0 {
		out.WriteString(fmt.Sprintf("Elapsed: %s\n", Humanize(time.Duration(b.ElapsedSeconds)*time.Second)))
	}

	gitStatus := b.GitCrossCheck
	if gitStatus == "" {
		gitStatus = "unknown"
	}
	out.WriteString(fmt.Sprintf("Git cross-check: %s\n", gitStatus))

	if len(b.WillRedo) > 0 {
		out.WriteString("\nWill redo:\n")
		for _, item := range b.WillRedo {
			out.WriteString(fmt.Sprintf("- %s\n", item))
		}
	}

	if len(b.WillSkip) > 0 {
		out.WriteString("\nWill skip:\n")
		for _, item := range b.WillSkip {
			out.WriteString(fmt.Sprintf("- %s\n", item))
		}
	}

	return out.String()
}

// PipelineTable renders a pipeline's static step configuration as a
// markdown table: step name, description, model (or "—" when the step is
// not model-driven), and whether the step is optional.
func PipelineTable(steps []ConfigStep) string {
	var b strings.Builder
	b.WriteString("| Step | Description | Model | Optional |\n")
	b.WriteString("|---|---|---|---|\n")

	for _, s := range steps {
		model := s.Model
		if model == "" {
			model = "—"
		}
		optional := "no"
		if s.Optional {
			optional = "yes"
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", s.Name, s.Description, model, optional))
	}

	return b.String()
}

// MigrationReport renders one line per applied change, followed by the
// backup path. An empty changes list renders a "no changes" line instead
// of an empty bullet section; an empty backupPath omits the backup line.
func MigrationReport(changes []string, backupPath string) string {
	var b strings.Builder
	b.WriteString("## Migration Report\n\n")

	if len(changes) == 0 {
		b.WriteString("No changes applied.\n")
	} else {
		for _, c := range changes {
			b.WriteString(fmt.Sprintf("- %s\n", c))
		}
	}

	if backupPath != "" {
		b.WriteString(fmt.Sprintf("\nBackup: %s\n", backupPath))
	}

	return b.String()
}

// IssueSummaryBlock renders one line per issue as "[severity] summary",
// with wave/task/step context appended in parentheses when present. An
// empty issues slice renders a single "no issues" line.
func IssueSummaryBlock(issues []StateIssue) string {
	if len(issues) == 0 {
		return "No issues recorded.\n"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Issues (%d)\n\n", len(issues)))

	for _, iss := range issues {
		b.WriteString(fmt.Sprintf("- [%s] %s", iss.Severity, iss.Summary))

		var ctx []string
		if iss.Wave != 0 {
			ctx = append(ctx, fmt.Sprintf("wave %d", iss.Wave))
		}
		if iss.TaskID != "" {
			ctx = append(ctx, fmt.Sprintf("task %s", iss.TaskID))
		}
		if iss.Step != "" {
			ctx = append(ctx, fmt.Sprintf("step %s", iss.Step))
		}
		if len(ctx) > 0 {
			b.WriteString(fmt.Sprintf(" (%s)", strings.Join(ctx, ", ")))
		}

		b.WriteByte('\n')
	}

	return b.String()
}
