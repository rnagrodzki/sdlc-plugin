package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// stepEntry builds one stepTimes entry as a state file decodes it.
func stepEntry(step string, iteration int, at string) map[string]any {
	return map[string]any{"step": step, "iteration": float64(iteration), "at": at}
}

// goldenSteps are the stepTimes entries of the sample report in the plan.
func goldenSteps() []any {
	return []any{
		stepEntry("0", 0, "2026-10-23T12:09:41Z"),
		stepEntry("1", 0, "2026-10-23T12:23:30Z"),
		stepEntry("2", 0, "2026-10-23T12:45:30Z"),
		stepEntry("3", 1, "2026-10-23T12:54:30Z"),
		stepEntry("4", 1, "2026-10-23T13:00:30Z"),
		stepEntry("5", 1, "2026-10-23T13:04:30Z"),
		stepEntry("6", 1, "2026-10-23T13:11:30Z"),
		stepEntry("6.5", 0, "2026-10-23T13:14:30Z"),
		stepEntry("6.6", 0, "2026-10-23T13:15:20Z"),
		stepEntry("7", 0, "2026-10-23T13:15:40Z"),
	}
}

const (
	goldenStart = "2026-10-23T11:54:41Z"
	goldenDone  = "2026-10-23T13:17:40Z"
)

// span builds a done writer span that runs for d from a fixed start.
func span(id string, d time.Duration) planWriterSpan {
	start := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	return planWriterSpan{WriterID: id, Status: evidenceStatusDone,
		StartedAt: start.Format(time.RFC3339), UpdatedAt: start.Add(d).Format(time.RFC3339)}
}

// planTimingReport builds the report from raw stepTimes and writer spans,
// the way the done marker does with an evidence folder it could list.
func planTimingReport(startAt, doneAt string, steps any, writers []planWriterSpan) string {
	agents, _ := planWriterAgents(writers)
	return planTimingText(startAt, doneAt, parseStepTimes(steps), agents, true)
}

// fullStepTimes returns n valid entries for step 1, iterations 0..n-1.
func fullStepTimes(n int) []any {
	raw := make([]any, 0, n)
	for i := 0; i < n; i++ {
		raw = append(raw, stepEntry("1", i, "2026-10-23T12:00:00Z"))
	}
	return raw
}

// notListWarning is the checkpoint warning for a stepTimes value of the
// given JSON type in the state file at path.
func notListWarning(step string, iteration int, path, typ string) string {
	return fmt.Sprintf("Step time not recorded for step %s iteration %d: stepTimes in %s is a %s, not a list. "+
		"Expected a list such as %s. The Step 7 timing report shows Steps: unknown. "+
		"To fix it, remove the stepTimes key from that file.", step, iteration, path, typ, planStepTimesExample)
}

// capWarning is the checkpoint warning at the stepTimes limit.
func capWarning(step string, iteration int) string {
	return fmt.Sprintf("Step time not recorded for step %s iteration %d: stepTimes holds %d entries, the limit (%d). "+
		"The Steps line of the Step 7 timing report leaves this step out.", step, iteration, maxStepTimesStored, maxStepTimesStored)
}

// reportLine returns the report line that starts with prefix (after the
// two-space indent), or "" when there is none.
func reportLine(report, prefix string) string {
	for _, l := range strings.Split(report, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// seedDoneRun seeds a plan run in a git fixture and returns the project root
// and the run's evidence folder.
func seedDoneRun(t *testing.T) (dir, evidenceDir string) {
	t.Helper()
	dir = t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	seedPlanTimingRun(t, dir)
	files := listStateFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("state files = %v, want exactly 1", files)
	}
	return dir, state.EvidenceDir(dir, strings.TrimSuffix(files[0], ".json"))
}

// patchPlanState loads the branch's plan state, lets fn change its data and
// writes it back, the way the next plan_mark call will read it.
func patchPlanState(t *testing.T, dir string, fn func(data map[string]any)) {
	t.Helper()
	st, err := state.LatestPlanRun(dir, "main")
	if err != nil || st == nil {
		t.Fatalf("LatestPlanRun: %v, %v", st, err)
	}
	fn(st.Data)
	if err := state.Write(st); err != nil {
		t.Fatalf("state.Write: %v", err)
	}
}

// writeTimingWriter writes one evidence writer file with the given times.
func writeTimingWriter(t *testing.T, evidenceDir, id, status, startedAt, updatedAt string) {
	t.Helper()
	wf := evidenceWriterFile{WriterID: id, Status: status, StartedAt: startedAt, UpdatedAt: updatedAt, Items: []EvidenceItem{}}
	b, err := json.Marshal(wf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidenceDir, id+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// checkpointIn is a valid checkpoint input for step and iteration.
func checkpointIn(step string, iteration int) PlanMarkIn {
	return PlanMarkIn{Marker: "checkpoint", Data: map[string]any{
		"step": step, "iteration": float64(iteration), "expectedWriters": []any{},
	}}
}

// storedStepTimes returns the stepTimes list of the sole state file.
func storedStepTimes(t *testing.T, dir string) []any {
	t.Helper()
	list, ok := readSoleStateDoc(t, dir)[planStepTimesKey].([]any)
	if !ok {
		t.Fatalf("stepTimes missing or not a list: %v", readSoleStateDoc(t, dir)[planStepTimesKey])
	}
	return list
}

// ---------------------------------------------------------------------------
// upsertStepTime
// ---------------------------------------------------------------------------

func TestPlanTiming_UpsertStepTime_Absent_NewListWithOneEntry(t *testing.T) {
	list, res, warn := upsertStepTime(nil, "2", 0, "2026-10-23T12:00:00Z", "/s.json")
	if warn != "" {
		t.Errorf("warning = %q, want none", warn)
	}
	if want := (PlanStepTimeResult{Step: "2", Iteration: 0, At: "2026-10-23T12:00:00Z", Status: stepTimeAdded}); res != want {
		t.Errorf("result = %+v, want %+v", res, want)
	}
	b, _ := json.Marshal(list)
	if want := `[{"step":"2","iteration":0,"at":"2026-10-23T12:00:00Z"}]`; string(b) != want {
		t.Errorf("list = %s, want %s", b, want)
	}
}

func TestPlanTiming_UpsertStepTime_NotAList_KeptWithWarning(t *testing.T) {
	for raw, typ := range map[any]string{"oops": "string", float64(7): "number", true: "boolean"} {
		list, res, warn := upsertStepTime(raw, "2", 0, "2026-10-23T12:00:00Z", "/s.json")
		if want := notListWarning("2", 0, "/s.json", typ); warn != want {
			t.Errorf("raw %v: warning = %q, want %q", raw, warn, want)
		}
		if res.Status != stepTimeNotRecorded || res.At != "" {
			t.Errorf("raw %v: result = %+v, want not recorded with no at", raw, res)
		}
		if !reflect.DeepEqual(list, raw) {
			t.Errorf("raw %v: list = %v, want the value unchanged", raw, list)
		}
	}
	obj := map[string]any{"step": "1"}
	if _, _, warn := upsertStepTime(obj, "2", 0, "2026-10-23T12:00:00Z", "/s.json"); warn != notListWarning("2", 0, "/s.json", "object") {
		t.Errorf("object: warning = %q", warn)
	}
}

func TestPlanTiming_UpsertStepTime_BadEntryKept_NeverMatches(t *testing.T) {
	raw := []any{"junk", map[string]any{"step": "2", "iteration": float64(0), "at": "not a time"}}
	list, _, warn := upsertStepTime(raw, "2", 0, "2026-10-23T12:00:00Z", "/s.json")
	if warn != "" {
		t.Errorf("warning = %q, want none", warn)
	}
	got, _ := list.([]any)
	if len(got) != 3 || got[0] != "junk" {
		t.Fatalf("list = %v, want the 2 bad entries kept and the new entry added", list)
	}
}

func TestPlanTiming_UpsertStepTime_Match_KeepsFirstEntry(t *testing.T) {
	raw := []any{
		stepEntry("1", 0, "2026-10-23T11:00:00Z"),
		stepEntry("2", 0, "2026-10-23T12:00:00Z"),
		stepEntry("2", 0, "2026-10-23T12:30:00Z"), // a second entry for the same step
	}
	list, res, warn := upsertStepTime(raw, "2", 0, "2026-10-23T13:00:00Z", "/s.json")
	if warn != "" {
		t.Errorf("warning = %q, want none", warn)
	}
	if !reflect.DeepEqual(list, raw) {
		t.Errorf("list = %v, want the list unchanged (first entry and its at kept)", list)
	}
	if want := (PlanStepTimeResult{Step: "2", Iteration: 0, At: "2026-10-23T12:00:00Z", Status: stepTimeKept}); res != want {
		t.Errorf("result = %+v, want %+v (the stored time)", res, want)
	}
	// Same step, other iteration: not a match.
	list, _, _ = upsertStepTime(raw, "2", 1, "2026-10-23T13:00:00Z", "/s.json")
	if got, _ := list.([]any); len(got) != 4 {
		t.Errorf("len = %d after a new iteration, want 4", len(got))
	}
}

func TestPlanTiming_UpsertStepTime_Cap(t *testing.T) {
	list, _, warn := upsertStepTime(fullStepTimes(maxStepTimesStored-1), "2", 0, "2026-10-23T13:00:00Z", "/s.json")
	if got, _ := list.([]any); warn != "" || len(got) != maxStepTimesStored {
		t.Errorf("one below the limit: len = %d, warning = %q, want %d and none", len(got), warn, maxStepTimesStored)
	}

	list, res, warn := upsertStepTime(fullStepTimes(maxStepTimesStored), "2", 0, "2026-10-23T13:00:00Z", "/s.json")
	if want := capWarning("2", 0); warn != want {
		t.Errorf("at the limit: warning = %q, want %q", warn, want)
	}
	if res.Status != stepTimeNotRecorded {
		t.Errorf("at the limit: status = %q, want %q", res.Status, stepTimeNotRecorded)
	}
	if got, _ := list.([]any); len(got) != maxStepTimesStored {
		t.Errorf("at the limit: len = %d, want %d (no entry added)", len(got), maxStepTimesStored)
	}

	// A match at the cap is not a new entry, so it needs no warning.
	if _, _, warn = upsertStepTime(fullStepTimes(maxStepTimesStored), "1", 5, "2026-10-23T13:00:00Z", "/s.json"); warn != "" {
		t.Errorf("at the limit, matching step: warning = %q, want none", warn)
	}
}

func TestPlanTiming_UpsertStepTime_TypedList(t *testing.T) {
	raw := []PlanStepTime{{Step: "1", Iteration: 0, At: "2026-10-23T11:00:00Z"}}
	list, _, warn := upsertStepTime(raw, "2", 0, "2026-10-23T12:00:00Z", "/s.json")
	got, _ := list.([]any)
	if warn != "" || len(got) != 2 {
		t.Errorf("len = %d, warning = %q, want 2 and none", len(got), warn)
	}
}

// ---------------------------------------------------------------------------
// planTimingReport
// ---------------------------------------------------------------------------

func TestPlanTiming_Report_MatchesPlanSample(t *testing.T) {
	writers := []planWriterSpan{
		span("explore-skill-docs-spec-sync", 10*time.Minute+8*time.Second),
		span("lens-risk-r1", 8*time.Minute+12*time.Second),
		span("lane-content-coverage-r1", 6*time.Minute+30*time.Second),
		span("lane-static-structural-r1", 5*time.Minute),
		{WriterID: "main", Status: evidenceStatusDone, StartedAt: "2026-10-23T11:00:00Z", UpdatedAt: "2026-10-23T13:00:00Z"},
		{WriterID: "lens-running", Status: evidenceStatusRunning, StartedAt: "2026-10-23T11:00:00Z", UpdatedAt: "2026-10-23T13:00:00Z"},
		{WriterID: "lens-no-start", Status: evidenceStatusDone, UpdatedAt: "2026-10-23T13:00:00Z"},
	}
	want := strings.Join([]string{
		"Step 7: Timing (wall clock, UTC)",
		"  Total: 1h 23m (2026-10-23T11:54:41Z to 2026-10-23T13:17:40Z)",
		"  Steps: 0 15m · 1 22m · 2 9m · 3 r1 6m · 4 r1 4m · 5 r1 7m · 6 r1 3m · 6.5 1m · 6.6 0m · 7 2m",
		"  Slowest agents: explore-skill-docs-spec-sync 10m 8s · lens-risk-r1 8m 12s · lane-content-coverage-r1 6m 30s",
	}, "\n")
	if got := planTimingReport(goldenStart, goldenDone, goldenSteps(), writers); got != want {
		t.Errorf("report =\n%s\nwant\n%s", got, want)
	}
}

func TestPlanTiming_Report_Format(t *testing.T) {
	minutes := map[time.Duration]string{
		0:                                     "0m",
		29*time.Second + 999*time.Millisecond: "0m",
		30 * time.Second:                      "1m",
		59 * time.Minute:                      "59m",
		59*time.Minute + 30*time.Second:       "1h 0m",
		60 * time.Minute:                      "1h 0m",
		83 * time.Minute:                      "1h 23m",
		125 * time.Minute:                     "2h 5m",
	}
	for d, want := range minutes {
		if got := planFormatMinutes(d); got != want {
			t.Errorf("planFormatMinutes(%v) = %q, want %q", d, got, want)
		}
	}
	seconds := map[time.Duration]string{
		0:                              "0m 0s",
		45 * time.Second:               "0m 45s",
		10*time.Minute + 8*time.Second: "10m 8s",
		75*time.Minute + 3*time.Second: "75m 3s",
	}
	for d, want := range seconds {
		if got := planFormatSeconds(d); got != want {
			t.Errorf("planFormatSeconds(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestPlanTiming_Report_StepsLine(t *testing.T) {
	full := fullStepTimes(maxStepTimesStored)
	note := fmt.Sprintf("Steps: first %d checkpoints only", maxStepTimesStored)
	cases := []struct {
		name  string
		steps any
		want  string // the Steps line
	}{
		{"absent", nil, "Steps: unknown (no step times recorded)"},
		{"empty list", []any{}, "Steps: unknown (no step times recorded)"},
		{"not a list", "oops", "Steps: unknown (stepTimes is not a list)"},
		{"bad entries skipped", []any{
			"junk",
			map[string]any{"step": "1", "iteration": float64(0), "at": "bad"},
			stepEntry("2", 0, "2026-10-23T12:00:00Z"),
		}, "Steps: 2 1h 18m"},
		{"first entry of a step is used", []any{
			stepEntry("2", 0, "2026-10-23T12:00:00Z"),
			stepEntry("3", 0, "2026-10-23T12:10:00Z"),
			stepEntry("2", 0, "2026-10-23T12:50:00Z"),
		}, "Steps: 2 10m · 3 1h 8m"},
		{"entry before the previous one shows unknown", []any{
			stepEntry("2", 0, "2026-10-23T12:00:00Z"),
			stepEntry("3", 0, "2026-10-23T11:00:00Z"),
			stepEntry("4", 0, "2026-10-23T12:20:00Z"),
		}, "Steps: 2 20m · 3 unknown · 4 58m"},
		{"equal times are in order", []any{
			stepEntry("6.5", 0, "2026-10-23T12:00:00Z"),
			stepEntry("6.6", 0, "2026-10-23T12:00:00Z"),
		}, "Steps: 6.5 0m · 6.6 1h 18m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reportLine(planTimingReport(goldenStart, "2026-10-23T13:18:00Z", tc.steps, nil), "Steps:")
			if got != tc.want {
				t.Errorf("Steps line = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("a full list adds the limit note", func(t *testing.T) {
		report := planTimingReport(goldenStart, goldenDone, full, nil)
		if !strings.Contains(report, "\n  "+note) {
			t.Errorf("report lacks the limit note:\n%s", report)
		}
		short := planTimingReport(goldenStart, goldenDone, full[:maxStepTimesStored-1], nil)
		if strings.Contains(short, note) {
			t.Errorf("one entry below the limit must not add the note:\n%s", short)
		}
	})

	t.Run("a list past the limit is read up to the limit only", func(t *testing.T) {
		long := append(fullStepTimes(maxStepTimesStored), "junk", stepEntry("9", 0, "2026-10-23T12:30:00Z"))
		p := parseStepTimes(long)
		if len(p.entries) != maxStepTimesStored {
			t.Errorf("entries = %d, want %d", len(p.entries), maxStepTimesStored)
		}
		want := []string{fmt.Sprintf("Timing: stepTimes holds %d entries; the report reads the first %d only.", maxStepTimesStored+2, maxStepTimesStored)}
		if !reflect.DeepEqual(p.warnings, want) {
			t.Errorf("warnings = %q, want %q (no warning for an entry past the limit)", p.warnings, want)
		}
	})
}

// A step whose span ends before it starts shows unknown, never a negative
// time: step 0 with a checkpoint before skillInvoked, and the last step with
// a checkpoint after the done stamp.
func TestPlanTiming_Report_NegativeSpanIsUnknown(t *testing.T) {
	steps := []any{stepEntry("0", 0, "2026-10-23T11:00:00Z"), stepEntry("1", 0, "2026-10-23T12:00:00Z")}
	if got := reportLine(planTimingReport(goldenStart, goldenDone, steps, nil), "Steps:"); got != "Steps: 0 unknown · 1 1h 18m" {
		t.Errorf("step 0 before start: Steps line = %q", got)
	}
	steps = []any{stepEntry("1", 0, "2026-10-23T12:00:00Z"), stepEntry("2", 0, "2026-10-23T14:00:00Z")}
	if got := reportLine(planTimingReport(goldenStart, goldenDone, steps, nil), "Steps:"); got != "Steps: 1 2h 0m · 2 unknown" {
		t.Errorf("last step after done: Steps line = %q", got)
	}
}

func TestPlanTiming_StepTimeFields_Reasons(t *testing.T) {
	const at = "2026-10-23T12:00:00Z"
	cases := []struct {
		name  string
		entry any
		want  string
	}{
		{"not an object", "junk", "not an object"},
		{"step not a string", map[string]any{"step": float64(2), "iteration": float64(0), "at": at}, "step is not a non-empty string"},
		{"step empty", map[string]any{"step": "", "iteration": float64(0), "at": at}, "step is not a non-empty string"},
		{"iteration missing", map[string]any{"step": "1", "at": at}, "iteration is not a whole number of 0 or more"},
		{"iteration a string", map[string]any{"step": "1", "iteration": "1", "at": at}, "iteration is not a whole number of 0 or more"},
		{"iteration below 0", map[string]any{"step": "1", "iteration": float64(-1), "at": at}, "iteration is not a whole number of 0 or more"},
		{"iteration not whole", map[string]any{"step": "1", "iteration": 1.5, "at": at}, "iteration is not a whole number of 0 or more"},
		{"iteration above MaxInt32", map[string]any{"step": "1", "iteration": float64(math.MaxInt32) + 1, "at": at}, "iteration is not a whole number of 0 or more"},
		{"at missing", map[string]any{"step": "1", "iteration": float64(0)}, "at is not RFC 3339"},
		{"at bad", map[string]any{"step": "1", "iteration": float64(0), "at": "noon"}, "at is not RFC 3339"},
		{"valid at MaxInt32", map[string]any{"step": "1", "iteration": float64(math.MaxInt32), "at": at}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, reason := stepTimeFields(tc.entry); reason != tc.want {
				t.Errorf("reason = %q, want %q", reason, tc.want)
			}
		})
	}
}

func TestPlanTiming_Report_StartStampBad_TotalAndStepZeroUnknown(t *testing.T) {
	for _, start := range []string{"", "yesterday"} {
		report := planTimingReport(start, goldenDone, goldenSteps(), nil)
		if got := reportLine(report, "Total:"); got != "Total: unknown" {
			t.Errorf("start %q: Total line = %q, want unknown", start, got)
		}
		steps := reportLine(report, "Steps:")
		if !strings.HasPrefix(steps, "Steps: 0 unknown · 1 22m · ") {
			t.Errorf("start %q: Steps line = %q, want step 0 unknown and step 1 known", start, steps)
		}
	}
}

func TestPlanTiming_Report_DoneStampBad_TotalAndLastStepUnknown(t *testing.T) {
	report := planTimingReport(goldenStart, "", goldenSteps(), nil)
	if got := reportLine(report, "Total:"); got != "Total: unknown" {
		t.Errorf("Total line = %q, want unknown", got)
	}
	if steps := reportLine(report, "Steps:"); !strings.HasSuffix(steps, " · 6.6 0m · 7 unknown") {
		t.Errorf("Steps line = %q, want the last step unknown", steps)
	}
	// A done stamp before the start is not a duration.
	if got := reportLine(planTimingReport(goldenStart, "2026-10-23T10:00:00Z", nil, nil), "Total:"); got != "Total: unknown" {
		t.Errorf("done before start: Total line = %q, want unknown", got)
	}
}

func TestPlanTiming_Report_AgentsLine(t *testing.T) {
	t.Run("no finished writer", func(t *testing.T) {
		for _, writers := range [][]planWriterSpan{nil, {}} {
			if got := reportLine(planTimingReport(goldenStart, goldenDone, nil, writers), "Slowest agents:"); got != "Slowest agents: none" {
				t.Errorf("Agents line = %q, want none", got)
			}
		}
	})

	t.Run("3 writers are all listed", func(t *testing.T) {
		writers := []planWriterSpan{span("a", time.Minute), span("b", 3*time.Minute), span("c", 2*time.Minute)}
		want := "Slowest agents: b 3m 0s · c 2m 0s · a 1m 0s"
		if got := reportLine(planTimingReport(goldenStart, goldenDone, nil, writers), "Slowest agents:"); got != want {
			t.Errorf("Agents line = %q, want %q", got, want)
		}
	})

	t.Run("4 writers leave the shortest out", func(t *testing.T) {
		writers := []planWriterSpan{span("a", time.Minute), span("b", 3*time.Minute), span("c", 2*time.Minute), span("d", 4*time.Minute)}
		want := "Slowest agents: d 4m 0s · b 3m 0s · c 2m 0s"
		if got := reportLine(planTimingReport(goldenStart, goldenDone, nil, writers), "Slowest agents:"); got != want {
			t.Errorf("Agents line = %q, want %q", got, want)
		}
	})

	t.Run("40 writers, the longest listed last", func(t *testing.T) {
		writers := make([]planWriterSpan, 0, 40)
		for i := 1; i <= 40; i++ {
			writers = append(writers, span(fmt.Sprintf("w%02d", i), time.Duration(i)*time.Minute))
		}
		want := "Slowest agents: w40 40m 0s · w39 39m 0s · w38 38m 0s"
		if got := reportLine(planTimingReport(goldenStart, goldenDone, nil, writers), "Slowest agents:"); got != want {
			t.Errorf("Agents line = %q, want %q", got, want)
		}
	})

	t.Run("equal times sort by writer id", func(t *testing.T) {
		writers := []planWriterSpan{span("zeta", time.Minute), span("alpha", time.Minute), span("mid", time.Minute), span("beta", time.Minute)}
		want := "Slowest agents: alpha 1m 0s · beta 1m 0s · mid 1m 0s"
		if got := reportLine(planTimingReport(goldenStart, goldenDone, nil, writers), "Slowest agents:"); got != want {
			t.Errorf("Agents line = %q, want %q", got, want)
		}
	})

	t.Run("main, running, no startedAt and bad times are not ranked", func(t *testing.T) {
		writers := []planWriterSpan{
			{WriterID: "main", Status: evidenceStatusDone, StartedAt: "2026-10-23T10:00:00Z", UpdatedAt: "2026-10-23T13:00:00Z"},
			{WriterID: "running", Status: evidenceStatusRunning, StartedAt: "2026-10-23T10:00:00Z", UpdatedAt: "2026-10-23T13:00:00Z"},
			{WriterID: "no-start", Status: evidenceStatusDone, UpdatedAt: "2026-10-23T13:00:00Z"},
			{WriterID: "bad-time", Status: evidenceStatusDone, StartedAt: "x", UpdatedAt: "2026-10-23T13:00:00Z"},
			{WriterID: "bad-end", Status: evidenceStatusDone, StartedAt: "2026-10-23T12:00:00Z", UpdatedAt: ""},
			{WriterID: "bad-end-2", Status: evidenceStatusDone, StartedAt: "2026-10-23T12:00:00Z", UpdatedAt: "later"},
			{WriterID: "backwards", Status: evidenceStatusDone, StartedAt: "2026-10-23T13:00:00Z", UpdatedAt: "2026-10-23T12:00:00Z"},
			span("only", 90*time.Second),
		}
		want := "Slowest agents: only 1m 30s"
		if got := reportLine(planTimingReport(goldenStart, goldenDone, nil, writers), "Slowest agents:"); got != want {
			t.Errorf("Agents line = %q, want %q", got, want)
		}
	})

	t.Run("a writer list that could not be read shows unknown", func(t *testing.T) {
		report := planTimingText(goldenStart, goldenDone, parseStepTimes(nil), nil, false)
		if got := reportLine(report, "Slowest agents:"); got != "Slowest agents: unknown" {
			t.Errorf("Agents line = %q, want unknown", got)
		}
	})
}

func TestPlanTiming_Report_NeverEmpty(t *testing.T) {
	report := planTimingReport("", "", nil, nil)
	want := strings.Join([]string{
		"Step 7: Timing (wall clock, UTC)",
		"  Total: unknown",
		"  Steps: unknown (no step times recorded)",
		"  Slowest agents: none",
	}, "\n")
	if report != want {
		t.Errorf("report =\n%s\nwant\n%s", report, want)
	}
}

// ---------------------------------------------------------------------------
// plan_mark checkpoint
// ---------------------------------------------------------------------------

func TestPlanMark_CheckpointStepTimes_FirstCheckpointOfAStepWins(t *testing.T) {
	dir, _ := seedDoneRun(t)

	out, err := planMark(dir, dir, checkpointIn("2", 0))
	if err != nil {
		t.Fatalf("planMark(checkpoint): %v", err)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("Warnings = %q, want none", out.Warnings)
	}
	if out.StepTime == nil || out.StepTime.Status != stepTimeAdded || out.StepTime.Step != "2" || out.StepTime.Iteration != 0 {
		t.Fatalf("StepTime = %+v, want step 2 iteration 0 added", out.StepTime)
	}
	list := storedStepTimes(t, dir)
	if len(list) != 1 {
		t.Fatalf("stepTimes = %v, want 1 entry", list)
	}
	first, _ := list[0].(map[string]any)
	if first["step"] != "2" || first["iteration"] != float64(0) {
		t.Fatalf("stepTimes[0] = %v, want step 2 iteration 0", first)
	}
	if _, err := time.Parse(time.RFC3339, first["at"].(string)); err != nil {
		t.Errorf("stepTimes[0].at = %v, want RFC 3339: %v", first["at"], err)
	}
	if out.StepTime.At != first["at"] {
		t.Errorf("StepTime.At = %q, want the stored at %v", out.StepTime.At, first["at"])
	}

	// Move the stored time into the past: a second checkpoint of the same
	// step and iteration must leave the entry and its time alone.
	const old = "2026-10-23T10:00:00Z"
	patchPlanState(t, dir, func(data map[string]any) {
		data[planStepTimesKey] = []any{stepEntry("2", 0, old)}
	})
	again, err := planMark(dir, dir, checkpointIn("2", 0))
	if err != nil {
		t.Fatalf("planMark(checkpoint) again: %v", err)
	}
	if want := (PlanStepTimeResult{Step: "2", Iteration: 0, At: old, Status: stepTimeKept}); again.StepTime == nil || *again.StepTime != want {
		t.Errorf("StepTime = %+v, want %+v", again.StepTime, want)
	}
	list = storedStepTimes(t, dir)
	if len(list) != 1 || list[0].(map[string]any)["at"] != old {
		t.Errorf("stepTimes = %v, want the first entry with at %s", list, old)
	}

	// A new iteration and a new step each add an entry.
	for _, in := range []PlanMarkIn{checkpointIn("2", 1), checkpointIn("3", 0)} {
		if _, err := planMark(dir, dir, in); err != nil {
			t.Fatalf("planMark(checkpoint): %v", err)
		}
	}
	if list = storedStepTimes(t, dir); len(list) != 3 {
		t.Errorf("stepTimes = %v, want 3 entries", list)
	}
}

func TestPlanMark_CheckpointStepTimes_NotAList_KeptWithWarning(t *testing.T) {
	dir, _ := seedDoneRun(t)
	patchPlanState(t, dir, func(data map[string]any) { data[planStepTimesKey] = "oops" })

	out, err := planMark(dir, dir, checkpointIn("2", 0))
	if err != nil {
		t.Fatalf("planMark(checkpoint): %v", err)
	}
	if !out.OK {
		t.Error("OK = false, want true: the checkpoint is still written")
	}
	if want := []string{notListWarning("2", 0, out.Path, "string")}; !reflect.DeepEqual(out.Warnings, want) {
		t.Errorf("Warnings = %q, want %q", out.Warnings, want)
	}
	doc := readSoleStateDoc(t, dir)
	if doc[planStepTimesKey] != "oops" {
		t.Errorf("stepTimes = %v, want the value kept", doc[planStepTimesKey])
	}
	if cp, _ := doc["checkpoint"].(map[string]any); cp["step"] != "2" {
		t.Errorf("checkpoint = %v, want step 2 written", doc["checkpoint"])
	}

	done, err := planMark(dir, dir, PlanMarkIn{Marker: "done"})
	if err != nil {
		t.Fatalf("planMark(done): %v", err)
	}
	if got := reportLine(done.Timing, "Steps:"); got != "Steps: unknown (stepTimes is not a list)" {
		t.Errorf("Steps line = %q, want the not-a-list text", got)
	}
	wantDone := fmt.Sprintf("Timing: stepTimes in %s is a string, not a list, so the Steps line shows unknown. "+
		"Expected a list such as %s. To fix it, remove the stepTimes key from that file.", done.Path, planStepTimesExample)
	if !reflect.DeepEqual(done.Warnings, []string{wantDone}) {
		t.Errorf("done Warnings = %q, want %q", done.Warnings, wantDone)
	}
}

func TestPlanMark_CheckpointStepTimes_CapAtLimit(t *testing.T) {
	dir, _ := seedDoneRun(t)
	base := time.Now().UTC().Add(-48 * time.Hour)
	patchPlanState(t, dir, func(data map[string]any) {
		list := make([]any, 0, maxStepTimesStored-1)
		for i := 0; i < maxStepTimesStored-1; i++ {
			list = append(list, stepEntry("1", i, base.Add(time.Duration(i)*time.Minute).Format(time.RFC3339)))
		}
		data[planStepTimesKey] = list
	})

	out, err := planMark(dir, dir, checkpointIn("2", 0))
	if err != nil || len(out.Warnings) != 0 {
		t.Fatalf("one below the limit: err = %v, warnings = %q, want the entry added with none", err, out.Warnings)
	}
	if n := len(storedStepTimes(t, dir)); n != maxStepTimesStored {
		t.Fatalf("stepTimes has %d entries, want %d", n, maxStepTimesStored)
	}

	out, err = planMark(dir, dir, checkpointIn("3", 0))
	if err != nil {
		t.Fatalf("planMark(checkpoint) at the cap: %v", err)
	}
	if want := []string{capWarning("3", 0)}; !reflect.DeepEqual(out.Warnings, want) {
		t.Errorf("Warnings = %q, want %q", out.Warnings, want)
	}
	if !out.OK || out.Next == "" {
		t.Errorf("OK = %v, Next = %q, want the checkpoint saved with its next text", out.OK, out.Next)
	}
	if out.StepTime == nil || out.StepTime.Status != stepTimeNotRecorded {
		t.Errorf("StepTime = %+v, want status not recorded", out.StepTime)
	}
	if n := len(storedStepTimes(t, dir)); n != maxStepTimesStored {
		t.Errorf("stepTimes has %d entries, want %d (no entry added)", n, maxStepTimesStored)
	}

	done, err := planMark(dir, dir, PlanMarkIn{Marker: "done"})
	if err != nil {
		t.Fatalf("planMark(done): %v", err)
	}
	if note := fmt.Sprintf("\n  Steps: first %d checkpoints only", maxStepTimesStored); !strings.Contains(done.Timing, note) {
		t.Errorf("timing lacks the limit note:\n%s", done.Timing)
	}
}

func TestPlanMark_CheckpointKeepsOtherKeys(t *testing.T) {
	dir, _ := seedDoneRun(t)
	if _, err := planMark(dir, dir, PlanMarkIn{Marker: "guardrailsEvaluated"}); err != nil {
		t.Fatal(err)
	}
	if _, err := planMark(dir, dir, PlanMarkIn{Marker: "review-round", Data: reviewRoundData(1, 2, 2, "Issues Found")}); err != nil {
		t.Fatal(err)
	}
	patchPlanState(t, dir, func(data map[string]any) {
		data["futureKey"] = map[string]any{"a": float64(1), "b": []any{"x"}}
		data["planFilePath"] = "docs/plan.md"
	})
	before := readSoleStateDoc(t, dir)

	if _, err := planMark(dir, dir, checkpointIn("2", 0)); err != nil {
		t.Fatalf("planMark(checkpoint): %v", err)
	}
	after := readSoleStateDoc(t, dir)

	changed := map[string]bool{"checkpoint": true, planStepTimesKey: true, "planTiming": true}
	for k, v := range before {
		if changed[k] {
			continue
		}
		if !reflect.DeepEqual(after[k], v) {
			t.Errorf("top-level key %q changed: %v -> %v", k, v, after[k])
		}
	}
	for _, k := range []string{"checkpoint", planStepTimesKey} {
		if _, ok := after[k]; !ok {
			t.Errorf("top-level key %q missing after the checkpoint", k)
		}
	}
	if cp, _ := after["checkpoint"].(map[string]any); cp != nil {
		if _, has := cp[planStepTimesKey]; has {
			t.Error("stepTimes must be a top-level key, not inside checkpoint")
		}
	}
	if integrity, _ := after["planIntegrity"].(map[string]any); integrity != nil {
		if _, has := integrity[planStepTimesKey]; has {
			t.Error("stepTimes must be a top-level key, not inside planIntegrity")
		}
	}
}

// ---------------------------------------------------------------------------
// plan_mark done
// ---------------------------------------------------------------------------

func TestPlanTiming_Done_ReturnsReportAndNext(t *testing.T) {
	dir, evidenceDir := seedDoneRun(t)
	for _, in := range []PlanMarkIn{checkpointIn("0", 0), checkpointIn("1", 0)} {
		if _, err := planMark(dir, dir, in); err != nil {
			t.Fatalf("planMark(checkpoint): %v", err)
		}
	}
	writeTimingWriter(t, evidenceDir, "explore-a", evidenceStatusDone, "2026-10-23T12:00:00Z", "2026-10-23T12:10:08Z")
	writeTimingWriter(t, evidenceDir, "lens-b", evidenceStatusDone, "2026-10-23T12:00:00Z", "2026-10-23T12:08:12Z")
	writeTimingWriter(t, evidenceDir, "lens-running", evidenceStatusRunning, "2026-10-23T11:00:00Z", "2026-10-23T12:59:00Z")
	writeTimingWriter(t, evidenceDir, "lens-old", evidenceStatusDone, "", "2026-10-23T12:30:00Z")
	writeTimingWriter(t, evidenceDir, evidenceMainWriter, evidenceStatusDone, "2026-10-23T11:00:00Z", "2026-10-23T12:59:00Z")

	out, err := planMark(dir, dir, PlanMarkIn{Marker: "done"})
	if err != nil {
		t.Fatalf("planMark(done): %v", err)
	}
	if !out.OK {
		t.Error("OK = false, want true")
	}
	if out.Next != planDoneNext {
		t.Errorf("Next = %q, want %q", out.Next, planDoneNext)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("Warnings = %q, want none", out.Warnings)
	}
	for _, prefix := range []string{"Step 7: Timing (wall clock, UTC)", "Total: ", "Steps: 0 ", "Slowest agents: explore-a 10m 8s · lens-b 8m 12s"} {
		if !strings.Contains(out.Timing, prefix) {
			t.Errorf("Timing lacks %q:\n%s", prefix, out.Timing)
		}
	}
	for _, id := range []string{"lens-running", "lens-old", "main"} {
		if strings.Contains(out.Timing, id) {
			t.Errorf("Timing lists %q, which must not rank:\n%s", id, out.Timing)
		}
	}
	if !strings.Contains(planDoneNext, "Print timing verbatim.") {
		t.Errorf("planDoneNext = %q", planDoneNext)
	}
}

func TestPlanTiming_Done_NoDataStillReturnsReport(t *testing.T) {
	dir, _ := seedDoneRun(t)
	out, err := planMark(dir, dir, PlanMarkIn{Marker: "done"})
	if err != nil {
		t.Fatalf("planMark(done): %v", err)
	}
	if out.Timing == "" {
		t.Fatal("Timing is empty, want a report on every done")
	}
	if got := reportLine(out.Timing, "Steps:"); got != "Steps: unknown (no step times recorded)" {
		t.Errorf("Steps line = %q", got)
	}
	if got := reportLine(out.Timing, "Slowest agents:"); got != "Slowest agents: none" {
		t.Errorf("Agents line = %q", got)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("Warnings = %q, want none", out.Warnings)
	}
}

func TestPlanTiming_Done_EvidenceFolderNotListable_WarnsAndReturnsOK(t *testing.T) {
	dir, evidenceDir := seedDoneRun(t)
	// A regular file where the folder should be: ReadDir fails with ENOTDIR,
	// an OS error. A chmod would not fail when the tests run as root.
	if err := os.RemoveAll(evidenceDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evidenceDir, []byte("not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := planMark(dir, dir, PlanMarkIn{Marker: "done"})
	if err != nil {
		t.Fatalf("planMark(done): %v, want ok", err)
	}
	if !out.OK || out.Next != planDoneNext {
		t.Errorf("OK = %v, Next = %q, want ok with the done next text", out.OK, out.Next)
	}
	if len(out.Warnings) != 1 || !strings.HasPrefix(out.Warnings[0], "Timing: cannot read evidence: ") {
		t.Fatalf("Warnings = %q, want one \"Timing: cannot read evidence: <error>\"", out.Warnings)
	}
	if !strings.HasSuffix(out.Warnings[0], ": "+syscall.ENOTDIR.Error()) {
		t.Errorf("Warning = %q, want the OS error in it", out.Warnings[0])
	}
	st, err := state.LatestPlanRun(dir, "main")
	if err != nil || st == nil {
		t.Fatalf("LatestPlanRun: %v", err)
	}
	if _, _, lerr := planReadWriterSpans(st); !errors.Is(lerr, syscall.ENOTDIR) {
		t.Errorf("listing error = %v, want ENOTDIR as its cause", lerr)
	}
	if got := reportLine(out.Timing, "Slowest agents:"); got != "Slowest agents: unknown" {
		t.Errorf("Agents line = %q, want unknown", got)
	}
}

// In a linked worktree, mainRoot (state and evidence) and contentRoot
// (branch) differ. The done marker reads the writers from the evidence folder
// under mainRoot.
func TestPlanTiming_Done_LinkedWorktree_ReadsMainEvidence(t *testing.T) {
	base := t.TempDir()
	mainDir, linkedDir := filepath.Join(base, "main"), filepath.Join(base, "linked")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitFixture(t, mainDir)
	gitCommit(t, mainDir, "initial")
	runGit(t, mainDir, "worktree", "add", "-b", "feat/linked", linkedDir)
	if _, err := runPlanPrepare(t, mainDir, linkedDir, PlanPrepareIn{SkipConfigCheck: true}); err != nil {
		t.Fatalf("planPrepareCore: %v", err)
	}
	st, err := state.LatestPlanRun(mainDir, "feat/linked")
	if err != nil || st == nil {
		t.Fatalf("LatestPlanRun: %v, %v", st, err)
	}
	writeTimingWriter(t, state.EvidenceDir(mainDir, state.RunID(st)), "lens-a", evidenceStatusDone, "2026-10-23T12:00:00Z", "2026-10-23T12:02:00Z")

	out, err := planMark(mainDir, linkedDir, PlanMarkIn{Marker: "done"})
	if err != nil {
		t.Fatalf("planMark(done): %v", err)
	}
	if got := reportLine(out.Timing, "Slowest agents:"); got != "Slowest agents: lens-a 2m 0s" {
		t.Errorf("Agents line = %q, want the writer from the main evidence folder", got)
	}
}

// A run whose evidence folder is gone has no writer to rank: the agents line
// shows none, with no warning.
func TestPlanTiming_Done_EvidenceFolderMissing_NoneAndNoWarning(t *testing.T) {
	dir, evidenceDir := seedDoneRun(t)
	if err := os.RemoveAll(evidenceDir); err != nil {
		t.Fatal(err)
	}
	out, err := planMark(dir, dir, PlanMarkIn{Marker: "done"})
	if err != nil || !out.OK {
		t.Fatalf("planMark(done): %v, OK = %v, want ok", err, out.OK)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("Warnings = %q, want none", out.Warnings)
	}
	if got := reportLine(out.Timing, "Slowest agents:"); got != "Slowest agents: none" {
		t.Errorf("Agents line = %q, want none", got)
	}
}

func TestPlanTiming_Done_SkippedWriterFiles_WarnOnePerFile(t *testing.T) {
	dir, evidenceDir := seedDoneRun(t)
	if err := os.WriteFile(filepath.Join(evidenceDir, "bad-json.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTimingWriter(t, evidenceDir, "bad-time", evidenceStatusDone, "yesterday", "2026-10-23T12:00:00Z")
	writeTimingWriter(t, evidenceDir, "backwards", evidenceStatusDone, "2026-10-23T12:10:00Z", "2026-10-23T12:00:00Z")
	writeTimingWriter(t, evidenceDir, "bad-end", evidenceStatusDone, "2026-10-23T12:00:00Z", "soon")
	writeTimingWriter(t, evidenceDir, "good-1", evidenceStatusDone, "2026-10-23T12:00:00Z", "2026-10-23T12:05:00Z")
	// A running writer and a writer with no startedAt never rank, so their
	// times are not checked and they add no warning.
	writeTimingWriter(t, evidenceDir, "running-1", evidenceStatusRunning, "yesterday", "tomorrow")
	writeTimingWriter(t, evidenceDir, "no-start", evidenceStatusDone, "", "tomorrow")

	out, err := planMark(dir, dir, PlanMarkIn{Marker: "done"})
	if err != nil {
		t.Fatalf("planMark(done): %v", err)
	}
	want := []string{
		"Timing: writer bad-json skipped: unreadable file",
		"Timing: writer backwards skipped: updatedAt 2026-10-23T12:00:00Z is before startedAt 2026-10-23T12:10:00Z",
		`Timing: writer bad-end skipped: updatedAt "soon" is not RFC 3339 (example: 2026-10-23T12:00:00Z)`,
		`Timing: writer bad-time skipped: startedAt "yesterday" is not RFC 3339 (example: 2026-10-23T12:00:00Z)`,
	}
	if !reflect.DeepEqual(out.Warnings, want) {
		t.Errorf("Warnings = %q, want %q", out.Warnings, want)
	}
	if got := reportLine(out.Timing, "Slowest agents:"); got != "Slowest agents: good-1 5m 0s" {
		t.Errorf("Agents line = %q, want only good-1", got)
	}
}

func TestPlanTiming_Done_BadStepTimesEntries_WarnAndKeepGoing(t *testing.T) {
	dir, _ := seedDoneRun(t)
	patchPlanState(t, dir, func(data map[string]any) {
		data[planStepTimesKey] = []any{
			"junk",
			map[string]any{"step": "", "iteration": float64(0), "at": "2026-10-23T12:00:00Z"},
			map[string]any{"step": "1", "iteration": float64(-1), "at": "2026-10-23T12:00:00Z"},
			map[string]any{"step": "1", "iteration": float64(0), "at": "bad"},
			stepEntry("2", 0, "2026-10-23T12:00:00Z"),
			stepEntry("3", 0, "2026-10-23T11:00:00Z"),
		}
	})

	out, err := planMark(dir, dir, PlanMarkIn{Marker: "done"})
	if err != nil {
		t.Fatalf("planMark(done): %v", err)
	}
	want := []string{
		"Timing: stepTimes entry 0 skipped: not an object",
		"Timing: stepTimes entry 1 skipped: step is not a non-empty string",
		"Timing: stepTimes entry 2 skipped: iteration is not a whole number of 0 or more",
		"Timing: stepTimes entry 3 skipped: at is not RFC 3339",
		"Timing: step 3 is shown as unknown: its time 2026-10-23T11:00:00Z is before 2026-10-23T12:00:00Z, the time of the step before it",
	}
	if !reflect.DeepEqual(out.Warnings, want) {
		t.Errorf("Warnings = %q, want %q", out.Warnings, want)
	}
	if steps := reportLine(out.Timing, "Steps:"); !strings.HasPrefix(steps, "Steps: 2 ") || !strings.HasSuffix(steps, " · 3 unknown") {
		t.Errorf("Steps line = %q, want step 2 timed and step 3 unknown", steps)
	}
}

func TestPlanTiming_Done_CalledAgain_BuildsReportAgain(t *testing.T) {
	dir, evidenceDir := seedDoneRun(t)
	first, err := planMark(dir, dir, PlanMarkIn{Marker: "done"})
	if err != nil {
		t.Fatalf("planMark(done): %v", err)
	}
	if got := reportLine(first.Timing, "Slowest agents:"); got != "Slowest agents: none" {
		t.Fatalf("first Agents line = %q", got)
	}

	// The second call reads the data as it is then: the new skillInvoked and
	// the new writer file show in its report.
	patchPlanState(t, dir, func(data map[string]any) {
		integrity, _ := data["planIntegrity"].(map[string]any)
		integrity["skillInvoked"] = time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	})
	writeTimingWriter(t, evidenceDir, "late-agent", evidenceStatusDone, "2026-10-23T12:00:00Z", "2026-10-23T12:01:00Z")

	second, err := planMark(dir, dir, PlanMarkIn{Marker: "done"})
	if err != nil {
		t.Fatalf("planMark(done) again: %v", err)
	}
	if second.Next != planDoneNext || len(second.Warnings) != 0 {
		t.Errorf("Next = %q, Warnings = %q, want the done next text and none", second.Next, second.Warnings)
	}
	if got := reportLine(second.Timing, "Total:"); !strings.HasPrefix(got, "Total: 2h 0m (") {
		t.Errorf("Total line = %q, want 2h 0m from the patched start", got)
	}
	if got := reportLine(second.Timing, "Slowest agents:"); got != "Slowest agents: late-agent 1m 0s" {
		t.Errorf("Agents line = %q", got)
	}
}

// ---------------------------------------------------------------------------
// readers and registration
// ---------------------------------------------------------------------------

func TestPlanTiming_ReadersIgnoreStepTimes(t *testing.T) {
	root := t.TempDir()
	newData := func() map[string]any {
		return map[string]any{
			"planIntegrity": map[string]any{"skillInvoked": "2026-10-23T11:54:41Z", "done": "2026-10-23T13:17:40Z"},
			"checkpoint":    map[string]any{"step": "6", "iteration": float64(1)},
			"planFilePath":  "docs/plan.md",
			"criticalDecisions": []any{
				map[string]any{"key": "k", "choice": "c", "at": "2026-10-23T12:00:00Z"},
			},
			"reviewRounds": []any{
				map[string]any{"round": float64(1), "mergedStatus": "Approved", "found": float64(0), "fixed": float64(0), "lenses": []any{}},
			},
		}
	}
	stateOf := func(data map[string]any) *state.State {
		return &state.State{Root: root, Path: filepath.Join(root, ".sdlc-v2", "runs", dashPlanRunID+".json"), Prefix: "plan", Data: data}
	}

	without := newData()
	for name, value := range map[string]any{
		"a list":     goldenSteps(),
		"not a list": "oops",
		"empty":      []any{},
	} {
		with := newData()
		with[planStepTimesKey] = value

		var a, b DashboardPipeline
		dashboardPlan(&a, stateOf(without))
		dashboardPlan(&b, stateOf(with))
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: dashboardPlan differs with stepTimes:\n%+v\n%+v", name, a, b)
		}
		if got, want := shipPlanningFrom(with), shipPlanningFrom(without); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: shipPlanningFrom differs with stepTimes:\n%+v\n%+v", name, got, want)
		}
	}
}

func TestPlanTiming_PlanMarkDescription_MatchesContract(t *testing.T) {
	s := mcpserver.New("test", "0.0.0-test")
	RegisterPlanTools(s)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	c, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	resp, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	var desc string
	for _, tool := range resp.Tools {
		if tool.Name == "plan_mark" {
			desc = tool.Description
		}
	}
	if desc == "" {
		t.Fatal("plan_mark is not registered")
	}
	for _, want := range []string{
		"The first checkpoint of each step and iteration also adds {step, iteration, at} to the top-level stepTimes list, and the checkpoint returns stepTime {step, iteration, at, status}",
		fmt.Sprintf("When stepTimes already holds %d entries, the checkpoint is still saved, no entry is added, status is not recorded and the checkpoint returns a warning.", maxStepTimesStored),
		"Markers other than checkpoint, review-outcome and done return no next",
		fmt.Sprintf("The done marker also returns timing: the Step 7 timing report (total, the time of each step, the %d slowest agents).", planTimingTopAgents),
		"It reads planIntegrity, stepTimes and the run's evidence writer files (<runId>.evidence/*.json). Absent data shows as unknown in its line. An evidence folder that cannot be listed adds the warning \"Timing: cannot read evidence: <error>\"; each skipped writer file or stepTimes entry adds one warning; done still returns ok.",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("plan_mark description lacks %q", want)
		}
	}
	if strings.Contains(desc, "Markers other than checkpoint and review-outcome return no next") {
		t.Error("plan_mark description still has the old no-next sentence")
	}
}
