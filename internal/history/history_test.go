package history

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// MemWriter tests — pure unit tests, no filesystem
// ---------------------------------------------------------------------------

func TestMemWriter_AppendRun(t *testing.T) {
	m := &MemWriter{}

	r1 := RunRecord{Timestamp: "2026-01-01T00:00:00Z", Skill: "ship", Branch: "main", Outcome: "success", DurationMs: 1234}
	r2 := RunRecord{Timestamp: "2026-01-02T00:00:00Z", Skill: "execute", Branch: "feat-x", Outcome: "failure", DurationMs: 5678}

	if err := m.AppendRun(r1); err != nil {
		t.Fatalf("AppendRun r1: %v", err)
	}
	if err := m.AppendRun(r2); err != nil {
		t.Fatalf("AppendRun r2: %v", err)
	}

	if len(m.Runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(m.Runs))
	}
	if m.Runs[0].Skill != "ship" {
		t.Errorf("expected first run skill=ship, got %q", m.Runs[0].Skill)
	}
	if m.Runs[1].Outcome != "failure" {
		t.Errorf("expected second run outcome=failure, got %q", m.Runs[1].Outcome)
	}
}

func TestMemWriter_AddDeferred(t *testing.T) {
	m := &MemWriter{}

	d1 := DeferredIssue{ID: "d1", Created: "2026-01-01", Source: "plan:pr-improvements", Priority: "high", Description: "Migrate test files", Status: "open"}
	d2 := DeferredIssue{ID: "d2", Created: "2026-01-02", Source: "ship:review", Priority: "low", Description: "Add lint step", Status: "open"}

	if err := m.AddDeferred(d1); err != nil {
		t.Fatalf("AddDeferred d1: %v", err)
	}
	if err := m.AddDeferred(d2); err != nil {
		t.Fatalf("AddDeferred d2: %v", err)
	}

	issues, err := m.ListDeferred()
	if err != nil {
		t.Fatalf("ListDeferred: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 deferred issues, got %d", len(issues))
	}
	if issues[0].ID != "d1" {
		t.Errorf("expected first issue id=d1, got %q", issues[0].ID)
	}
}

func TestMemWriter_ListDeferred_Empty(t *testing.T) {
	m := &MemWriter{}
	issues, err := m.ListDeferred()
	if err != nil {
		t.Fatalf("ListDeferred: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected 0 deferred issues, got %d", len(issues))
	}
}

func TestMemWriter_ResolveDeferred(t *testing.T) {
	m := &MemWriter{}

	d1 := DeferredIssue{ID: "d1", Created: "2026-01-01", Source: "plan:pr", Priority: "high", Description: "Fix labels", Status: "open"}
	d2 := DeferredIssue{ID: "d2", Created: "2026-01-02", Source: "ship:ci", Priority: "medium", Description: "Pin actions", Status: "open"}

	_ = m.AddDeferred(d1)
	_ = m.AddDeferred(d2)

	if err := m.ResolveDeferred("d1"); err != nil {
		t.Fatalf("ResolveDeferred d1: %v", err)
	}

	issues, _ := m.ListDeferred()
	if issues[0].Status != "resolved" {
		t.Errorf("expected d1 status=resolved, got %q", issues[0].Status)
	}
	if issues[1].Status != "open" {
		t.Errorf("expected d2 status=open, got %q", issues[1].Status)
	}
}

func TestMemWriter_ResolveDeferred_NotFound(t *testing.T) {
	m := &MemWriter{}

	err := m.ResolveDeferred("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent ID, got nil")
	}
}

func TestOpenDeferred(t *testing.T) {
	issues := []DeferredIssue{
		{ID: "d1", Status: "open", Priority: "high"},
		{ID: "d2", Status: "resolved", Priority: "low"},
		{ID: "d3", Status: "open", Priority: "medium"},
		{ID: "d4", Status: "wont-fix", Priority: "low"},
	}

	open := OpenDeferred(issues)
	if len(open) != 2 {
		t.Fatalf("expected 2 open issues, got %d", len(open))
	}
	if open[0].ID != "d1" || open[1].ID != "d3" {
		t.Errorf("unexpected open issue IDs: %q, %q", open[0].ID, open[1].ID)
	}
}

func TestDeferredByPriority(t *testing.T) {
	issues := []DeferredIssue{
		{ID: "d1", Status: "open", Priority: "high"},
		{ID: "d2", Status: "open", Priority: "low"},
		{ID: "d3", Status: "resolved", Priority: "high"},
		{ID: "d4", Status: "open", Priority: "high"},
		{ID: "d5", Status: "open", Priority: "medium"},
	}

	groups := DeferredByPriority(issues)
	if len(groups["high"]) != 2 {
		t.Errorf("expected 2 high-priority open issues, got %d", len(groups["high"]))
	}
	if len(groups["medium"]) != 1 {
		t.Errorf("expected 1 medium-priority open issue, got %d", len(groups["medium"]))
	}
	if len(groups["low"]) != 1 {
		t.Errorf("expected 1 low-priority open issue, got %d", len(groups["low"]))
	}
}

func TestFormatDeferredSummary_NoOpen(t *testing.T) {
	issues := []DeferredIssue{
		{ID: "d1", Status: "resolved", Priority: "high"},
	}
	summary := FormatDeferredSummary(issues)
	if summary != "" {
		t.Errorf("expected empty summary for no open issues, got %q", summary)
	}
}

func TestFormatDeferredSummary_WithOpen(t *testing.T) {
	issues := []DeferredIssue{
		{ID: "d1", Created: "2026-01-01", Source: "plan:pr", Priority: "high", Description: "Fix labels", Status: "open"},
		{ID: "d2", Created: "2026-01-02", Source: "ship:ci", Priority: "low", Description: "Pin actions", Status: "open"},
		{ID: "d3", Created: "2026-01-03", Source: "ship:review", Priority: "high", Description: "Add lint", Status: "resolved"},
	}

	summary := FormatDeferredSummary(issues)
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}

	// Should mention count
	if !contains(summary, "2 deferred issue(s)") {
		t.Errorf("expected summary to contain issue count, got:\n%s", summary)
	}
	// Should have High section before Low
	highIdx := indexOf(summary, "High")
	lowIdx := indexOf(summary, "Low")
	if highIdx < 0 || lowIdx < 0 || highIdx > lowIdx {
		t.Errorf("expected High before Low in summary, got:\n%s", summary)
	}
}

func TestMemWriter_ListDeferred_IsCopy(t *testing.T) {
	m := &MemWriter{}
	_ = m.AddDeferred(DeferredIssue{ID: "d1", Status: "open"})

	issues, _ := m.ListDeferred()
	issues[0].Status = "mutated"

	original, _ := m.ListDeferred()
	if original[0].Status != "open" {
		t.Error("ListDeferred should return a copy, not a reference")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func contains(s, substr string) bool {
	return indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// FileWriter tests — filesystem-backed, each test uses t.TempDir()
// ---------------------------------------------------------------------------

func TestFileWriter_AppendRun_ReadRecentRuns(t *testing.T) {
	dir := t.TempDir()
	w := NewFileWriter(dir)

	runs := []RunRecord{
		{Timestamp: "2026-01-01T00:00:00Z", Skill: "ship", Branch: "main", Outcome: "success", DurationMs: 100},
		{Timestamp: "2026-01-02T00:00:00Z", Skill: "execute", Branch: "feat-a", Outcome: "failure", DurationMs: 200},
		{Timestamp: "2026-01-03T00:00:00Z", Skill: "plan", Branch: "feat-b", Outcome: "success", DurationMs: 300},
	}
	for i, r := range runs {
		if err := w.AppendRun(r); err != nil {
			t.Fatalf("AppendRun #%d: %v", i, err)
		}
	}

	// Read last 2 — should return only the two most recent records.
	recent, err := w.ReadRecentRuns(2)
	if err != nil {
		t.Fatalf("ReadRecentRuns: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent runs, got %d", len(recent))
	}
	if recent[0].Skill != "execute" {
		t.Errorf("expected first recent run skill=execute, got %q", recent[0].Skill)
	}
	if recent[1].Skill != "plan" {
		t.Errorf("expected second recent run skill=plan, got %q", recent[1].Skill)
	}

	// Read all 3.
	all, err := w.ReadRecentRuns(10)
	if err != nil {
		t.Fatalf("ReadRecentRuns(10): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 runs total, got %d", len(all))
	}
}

func TestFileWriter_ReadRecentRuns_MissingFile(t *testing.T) {
	dir := t.TempDir()
	w := NewFileWriter(filepath.Join(dir, "nonexistent"))

	runs, err := w.ReadRecentRuns(5)
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if runs != nil {
		t.Fatalf("expected nil runs for missing file, got %v", runs)
	}
}

func TestFileWriter_AddDeferred_ListDeferred(t *testing.T) {
	dir := t.TempDir()
	w := NewFileWriter(dir)

	d1 := DeferredIssue{ID: "d1", Created: "2026-01-01", Source: "plan:pr", Priority: "high", Description: "Fix labels", Status: "open"}
	d2 := DeferredIssue{ID: "d2", Created: "2026-01-02", Source: "ship:ci", Priority: "low", Description: "Pin actions", Status: "open"}

	if err := w.AddDeferred(d1); err != nil {
		t.Fatalf("AddDeferred d1: %v", err)
	}
	if err := w.AddDeferred(d2); err != nil {
		t.Fatalf("AddDeferred d2: %v", err)
	}

	issues, err := w.ListDeferred()
	if err != nil {
		t.Fatalf("ListDeferred: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 deferred issues, got %d", len(issues))
	}
	if issues[0].ID != "d1" {
		t.Errorf("expected first issue id=d1, got %q", issues[0].ID)
	}
	if issues[1].ID != "d2" {
		t.Errorf("expected second issue id=d2, got %q", issues[1].ID)
	}
}

func TestFileWriter_ResolveDeferred(t *testing.T) {
	dir := t.TempDir()
	w := NewFileWriter(dir)

	d1 := DeferredIssue{ID: "d1", Created: "2026-01-01", Source: "plan:pr", Priority: "high", Description: "Fix labels", Status: "open"}
	d2 := DeferredIssue{ID: "d2", Created: "2026-01-02", Source: "ship:ci", Priority: "medium", Description: "Pin actions", Status: "open"}

	_ = w.AddDeferred(d1)
	_ = w.AddDeferred(d2)

	if err := w.ResolveDeferred("d1"); err != nil {
		t.Fatalf("ResolveDeferred d1: %v", err)
	}

	issues, err := w.ListDeferred()
	if err != nil {
		t.Fatalf("ListDeferred: %v", err)
	}
	if issues[0].Status != "resolved" {
		t.Errorf("expected d1 status=resolved, got %q", issues[0].Status)
	}
	if issues[1].Status != "open" {
		t.Errorf("expected d2 status=open, got %q", issues[1].Status)
	}
}

func TestFileWriter_ResolveDeferred_NotFound(t *testing.T) {
	dir := t.TempDir()
	w := NewFileWriter(dir)

	// Add one issue so the file exists, then try to resolve a non-existent ID.
	_ = w.AddDeferred(DeferredIssue{ID: "d1", Status: "open"})

	err := w.ResolveDeferred("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent ID, got nil")
	}
}

func TestFileWriter_AddDeferred_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	w := NewFileWriter(dir)

	// Write garbage to deferred.json before calling AddDeferred.
	if err := os.WriteFile(filepath.Join(dir, "deferred.json"), []byte("not valid json!!!"), 0o644); err != nil {
		t.Fatalf("setup: write corrupt file: %v", err)
	}

	err := w.AddDeferred(DeferredIssue{ID: "d1", Status: "open"})
	if err == nil {
		t.Fatal("expected error when deferred.json is corrupt, got nil")
	}
}

// TestFileWriter_PathAccessors_MatchWrittenFiles pins that RunsPath and
// DeferredPath name the files the writer really creates. Callers print these
// paths in error messages, so a drift would point users at the wrong file.
func TestFileWriter_PathAccessors_MatchWrittenFiles(t *testing.T) {
	dir := t.TempDir()
	w := NewFileWriter(dir)

	if err := w.AppendRun(RunRecord{Timestamp: "2026-01-01T00:00:00Z", Skill: "ship", Outcome: "success"}); err != nil {
		t.Fatalf("AppendRun: %v", err)
	}
	if err := w.AddDeferred(DeferredIssue{ID: "d1", Status: "open"}); err != nil {
		t.Fatalf("AddDeferred: %v", err)
	}

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"RunsPath", w.RunsPath(), filepath.Join(dir, "runs.jsonl")},
		{"DeferredPath", w.DeferredPath(), filepath.Join(dir, "deferred.json")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("path = %q, want %q", tc.got, tc.want)
			}
			info, err := os.Stat(tc.got)
			if err != nil {
				t.Fatalf("file not found at accessor path: %v", err)
			}
			if info.Size() == 0 {
				t.Errorf("file at %q is empty, want the written record", tc.got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DeferredIssue optional fields and the Reason enum — no filesystem
// ---------------------------------------------------------------------------

// A deferred.json written before Severity/File/Line/Reason existed must
// still parse, leaving the new fields at their zero values.
func TestDeferredIssue_UnmarshalLegacyEntryLeavesNewFieldsZero(t *testing.T) {
	legacy := `[{"id":"old-1","created":"2025-01-01T00:00:00Z","source":"manual",` +
		`"priority":"medium","description":"an older entry","status":"open"}]`

	var issues []DeferredIssue
	if err := json.Unmarshal([]byte(legacy), &issues); err != nil {
		t.Fatalf("unmarshal legacy deferred.json: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %d, want 1", len(issues))
	}
	got := issues[0]
	if got.ID != "old-1" || got.Description != "an older entry" || got.Status != "open" {
		t.Errorf("existing fields lost: %+v", got)
	}
	if got.Severity != "" || got.File != "" || got.Line != 0 || got.Reason != "" {
		t.Errorf("new fields = %q/%q/%d/%q, want all zero", got.Severity, got.File, got.Line, got.Reason)
	}
}

// The new fields are omitempty, so an entry that does not set them
// serializes exactly as it did before they were added.
func TestDeferredIssue_MarshalOmitsEmptyOptionalFields(t *testing.T) {
	b, err := json.Marshal(DeferredIssue{
		ID: "x-1", Created: "2025-01-01T00:00:00Z", Source: "manual",
		Priority: "medium", Description: "d", Status: "open",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"severity", "file", "line", "reason"} {
		if strings.Contains(string(b), `"`+key+`"`) {
			t.Errorf("marshalled entry %s contains empty optional key %q", b, key)
		}
	}
}

func TestDeferredIssue_RoundTripsOptionalFields(t *testing.T) {
	want := DeferredIssue{
		ID: "x-2", Created: "2025-01-01T00:00:00Z", Source: "review-below-threshold",
		Priority: "high", Description: "d", Status: "open",
		Severity: "high", File: "internal/foo.go", Line: 42, Reason: ReasonBelowThreshold,
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got DeferredIssue
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

func TestValidDeferredReason(t *testing.T) {
	valid := []string{"below-threshold", "needs-direction", "disagree", "wont-fix"}
	for _, r := range valid {
		if !ValidDeferredReason(r) {
			t.Errorf("ValidDeferredReason(%q) = false, want true", r)
		}
	}
	for _, r := range []string{"", "below threshold", "Below-Threshold", "won't-fix", "other"} {
		if ValidDeferredReason(r) {
			t.Errorf("ValidDeferredReason(%q) = true, want false", r)
		}
	}
}

// DeferredReasons is the rendering half of the same single definition, so
// it must list exactly the values ValidDeferredReason accepts.
func TestDeferredReasons_MatchesValidator(t *testing.T) {
	got := DeferredReasons()
	want := []string{ReasonBelowThreshold, ReasonNeedsDirection, ReasonDisagree, ReasonWontFix}
	if len(got) != len(want) {
		t.Fatalf("DeferredReasons() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DeferredReasons() = %v, want %v", got, want)
		}
		if !ValidDeferredReason(got[i]) {
			t.Errorf("DeferredReasons() lists %q, which ValidDeferredReason rejects", got[i])
		}
	}
}

// ---------------------------------------------------------------------------
// RunRecord plan-fields tests
// ---------------------------------------------------------------------------

// TestRunRecord_PlanFieldsRoundTrip verifies PlanFile, StartedAt,
// LastModifiedAt, PlanStartedAt and PlanDurationMs survive a
// marshal/unmarshal round trip alongside the pre-existing fields. The first
// three match the shape plan.go's appendPlanRunRecord writes for the "done"
// marker; the last two are the plan fields of a ship row.
func TestRunRecord_PlanFieldsRoundTrip(t *testing.T) {
	want := RunRecord{
		Timestamp:      "2026-09-30T12:00:00Z",
		Skill:          "plan",
		Branch:         "feat-x",
		Outcome:        "done",
		DurationMs:     300000,
		PlanFile:       "/repo/plans/my-plan.md",
		StartedAt:      "2026-09-30T11:55:00Z",
		LastModifiedAt: "2026-09-30T12:00:00Z",
		PlanStartedAt:  "2026-09-30T08:20:00Z",
		PlanDurationMs: 4800000,
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got RunRecord
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

// TestRunRecord_PlanFieldsOmittedWhenEmpty verifies the five plan fields are
// omitempty, so a record that never sets them serializes exactly as it did
// before the fields existed.
func TestRunRecord_PlanFieldsOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(RunRecord{
		Timestamp:  "2026-01-01T00:00:00Z",
		Skill:      "ship",
		Branch:     "main",
		Outcome:    "success",
		DurationMs: 1234,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"plan_file", "started_at", "last_modified_at", "plan_started_at", "plan_duration_ms"} {
		if strings.Contains(string(b), `"`+key+`"`) {
			t.Errorf("marshalled record %s contains empty optional key %q", b, key)
		}
	}
}

// TestRunRecord_LegacyLineWithoutPlanFieldsParses verifies a runs.jsonl line
// written before PlanFile/StartedAt/LastModifiedAt existed still parses,
// leaving the new fields at their zero value — existing readers of
// runs.jsonl (e.g. ship's history summary) must keep working unchanged.
func TestRunRecord_LegacyLineWithoutPlanFieldsParses(t *testing.T) {
	legacy := `{"ts":"2026-01-01T00:00:00Z","skill":"ship","branch":"main","outcome":"success","duration_ms":100}`

	var got RunRecord
	if err := json.Unmarshal([]byte(legacy), &got); err != nil {
		t.Fatalf("unmarshal legacy runs.jsonl line: %v", err)
	}
	if got.Skill != "ship" || got.Branch != "main" || got.Outcome != "success" || got.DurationMs != 100 {
		t.Errorf("existing fields lost: %+v", got)
	}
	if got.PlanFile != "" || got.StartedAt != "" || got.LastModifiedAt != "" {
		t.Errorf("new fields = %q/%q/%q, want all empty", got.PlanFile, got.StartedAt, got.LastModifiedAt)
	}
	if got.PlanStartedAt != "" || got.PlanDurationMs != 0 {
		t.Errorf("ship plan fields = %q/%d, want empty and 0", got.PlanStartedAt, got.PlanDurationMs)
	}
}

// TestRunRecord_NoPlanFieldsRoundTripsByteForByte verifies a runs.jsonl line
// without any plan field parses and serializes back to the same bytes, so an
// old row is never rewritten with new keys.
func TestRunRecord_NoPlanFieldsRoundTripsByteForByte(t *testing.T) {
	line := `{"ts":"2026-10-10T11:00:00Z","skill":"ship","branch":"feat/x","outcome":"success","duration_ms":3600000,"version":"0.4.2"}`

	var rec RunRecord
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != line {
		t.Errorf("round trip =\n %s\nwant\n %s", b, line)
	}
}

// TestRunRecord_ShipPlanFieldsKeyOrder verifies the ship row layout: the plan
// keys follow plan_file and sit before the other optional keys.
func TestRunRecord_ShipPlanFieldsKeyOrder(t *testing.T) {
	b, err := json.Marshal(RunRecord{
		Timestamp:      "2026-10-10T11:00:00Z",
		Skill:          "ship",
		Branch:         "feat/x",
		Outcome:        "success",
		DurationMs:     3600000,
		Version:        "0.4.2",
		PlanFile:       "/plans/x.md",
		PlanStartedAt:  "2026-10-10T08:20:00Z",
		PlanDurationMs: 4800000,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"ts":"2026-10-10T11:00:00Z","skill":"ship","branch":"feat/x","outcome":"success","duration_ms":3600000,"version":"0.4.2","plan_file":"/plans/x.md","plan_started_at":"2026-10-10T08:20:00Z","plan_duration_ms":4800000}`
	if string(b) != want {
		t.Errorf("row =\n %s\nwant\n %s", b, want)
	}
}

// ---------------------------------------------------------------------------
// MergeOrAddDeferred
// ---------------------------------------------------------------------------

// deferredRow builds an open row for the merge tests.
func deferredRow(id, file, reason, description string) DeferredIssue {
	return DeferredIssue{
		ID: id, Created: "2026-01-01T00:00:00Z", Source: SourceReceivedReview,
		Priority: PriorityMedium, Description: description, Status: StatusOpen,
		File: file, Reason: reason,
	}
}

// seedDeferred writes rows to a new store and returns the writer.
func seedDeferred(t *testing.T, rows ...DeferredIssue) *FileWriter {
	t.Helper()
	w := NewFileWriter(filepath.Join(t.TempDir(), "history"))
	for _, r := range rows {
		if err := w.AddDeferred(r); err != nil {
			t.Fatalf("seed AddDeferred %s: %v", r.ID, err)
		}
	}
	return w
}

// listDeferred returns the rows of w and fails the test on an error.
func listDeferred(t *testing.T, w Writer) []DeferredIssue {
	t.Helper()
	rows, err := w.ListDeferred()
	if err != nil {
		t.Fatalf("ListDeferred: %v", err)
	}
	return rows
}

// writers runs fn once for the file store and once for the in-memory store,
// so both implementations keep the same merge rules.
func writers(t *testing.T, rows []DeferredIssue, fn func(t *testing.T, w Writer)) {
	t.Helper()
	t.Run("file", func(t *testing.T) { fn(t, seedDeferred(t, rows...)) })
	t.Run("mem", func(t *testing.T) {
		m := &MemWriter{}
		for _, r := range rows {
			if err := m.AddDeferred(r); err != nil {
				t.Fatalf("seed AddDeferred %s: %v", r.ID, err)
			}
		}
		fn(t, m)
	})
}

// TestDeferredMerge_NeedsDirectionSameFileMerges verifies that a needs-direction
// row for a file with an open needs-direction row of that file appends its
// text to that row, for the file store and the in-memory store.
func TestDeferredMerge_NeedsDirectionSameFileMerges(t *testing.T) {
	writers(t, []DeferredIssue{deferredRow("review-deferred-A", "a.go", ReasonNeedsDirection, "- x")}, func(t *testing.T, w Writer) {
		in := deferredRow("review-deferred-B", "a.go", ReasonNeedsDirection, "y")
		stored, merged, err := w.MergeOrAddDeferred(in, false)
		if err != nil {
			t.Fatalf("MergeOrAddDeferred: %v", err)
		}
		if !merged || stored.ID != "review-deferred-A" {
			t.Errorf("stored.ID = %q merged = %v, want review-deferred-A true", stored.ID, merged)
		}
		want := []DeferredIssue{deferredRow("review-deferred-A", "a.go", ReasonNeedsDirection, "- x\n- y")}
		if got := listDeferred(t, w); !reflect.DeepEqual(got, want) {
			t.Errorf("rows =\n %+v\nwant\n %+v", got, want)
		}
	})
}

// TestDeferredMerge_NeedsDirectionOtherFileOrReasonAddsRow verifies that a row
// with another file, another reason or no file is added as its own row.
func TestDeferredMerge_NeedsDirectionOtherFileOrReasonAddsRow(t *testing.T) {
	existing := deferredRow("review-deferred-A", "a.go", ReasonNeedsDirection, "- x")
	tests := []struct {
		name string
		in   DeferredIssue
	}{
		{"other file", deferredRow("review-deferred-B", "b.go", ReasonNeedsDirection, "y")},
		{"other reason", deferredRow("review-deferred-B", "a.go", ReasonDisagree, "y")},
		{"no file", deferredRow("review-deferred-B", "", ReasonNeedsDirection, "y")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writers(t, []DeferredIssue{existing}, func(t *testing.T, w Writer) {
				stored, merged, err := w.MergeOrAddDeferred(tt.in, false)
				if err != nil {
					t.Fatalf("MergeOrAddDeferred: %v", err)
				}
				if merged || stored.ID != "review-deferred-B" {
					t.Errorf("stored.ID = %q merged = %v, want review-deferred-B false", stored.ID, merged)
				}
				want := []DeferredIssue{existing, tt.in}
				if got := listDeferred(t, w); !reflect.DeepEqual(got, want) {
					t.Errorf("rows =\n %+v\nwant\n %+v", got, want)
				}
			})
		})
	}
}

// TestDeferredMerge_NeedsDirectionIgnoresResolvedRow verifies that a resolved
// row of the same file and reason does not receive the text; a new row is added.
func TestDeferredMerge_NeedsDirectionIgnoresResolvedRow(t *testing.T) {
	resolved := deferredRow("review-deferred-A", "a.go", ReasonNeedsDirection, "- x")
	resolved.Status = StatusResolved
	writers(t, []DeferredIssue{resolved}, func(t *testing.T, w Writer) {
		in := deferredRow("review-deferred-B", "a.go", ReasonNeedsDirection, "y")
		if _, merged, err := w.MergeOrAddDeferred(in, false); err != nil || merged {
			t.Fatalf("MergeOrAddDeferred merged = %v err = %v, want a new row", merged, err)
		}
		if got := listDeferred(t, w); len(got) != 2 || got[0].Description != "- x" {
			t.Errorf("rows = %+v, want the resolved row unchanged and a second row", got)
		}
	})
}

// TestDeferredMerge_BelowThresholdSameFileStaysSeparate verifies that two
// below-threshold rows for one file stay two rows.
func TestDeferredMerge_BelowThresholdSameFileStaysSeparate(t *testing.T) {
	a := deferredRow("review-deferred-A", "a.go", ReasonBelowThreshold, "nit one")
	writers(t, []DeferredIssue{a}, func(t *testing.T, w Writer) {
		b := deferredRow("review-deferred-B", "a.go", ReasonBelowThreshold, "nit two")
		stored, merged, err := w.MergeOrAddDeferred(b, false)
		if err != nil || merged || stored.ID != "review-deferred-B" {
			t.Fatalf("stored.ID = %q merged = %v err = %v, want review-deferred-B false nil", stored.ID, merged, err)
		}
		if got, want := listDeferred(t, w), []DeferredIssue{a, b}; !reflect.DeepEqual(got, want) {
			t.Errorf("rows =\n %+v\nwant\n %+v", got, want)
		}
	})
}

// TestDeferredMerge_ByIDAppendsOnlyNewLines verifies that a merge by id into an
// open row appends only the lines the row does not hold, and that a repeat
// call adds nothing.
func TestDeferredMerge_ByIDAppendsOnlyNewLines(t *testing.T) {
	writers(t, []DeferredIssue{deferredRow("pending", "", "", "- keep")}, func(t *testing.T, w Writer) {
		in := deferredRow("pending", "", "", "keep\nnew one\n\n- new two")
		stored, merged, err := w.MergeOrAddDeferred(in, true)
		if err != nil || !merged {
			t.Fatalf("first call merged = %v err = %v, want true nil", merged, err)
		}
		if want := "- keep\n- new one\n- new two"; stored.Description != want {
			t.Errorf("description = %q, want %q", stored.Description, want)
		}

		// A repeat call holds the same text: nothing is added.
		stored, merged, err = w.MergeOrAddDeferred(in, true)
		if err != nil || merged {
			t.Fatalf("repeat call merged = %v err = %v, want false nil", merged, err)
		}
		if stored.ID != "pending" || stored.Description != "- keep\n- new one\n- new two" {
			t.Errorf("repeat stored = %+v, want the unchanged row", stored)
		}
		if got := listDeferred(t, w); len(got) != 1 || got[0].Description != "- keep\n- new one\n- new two" {
			t.Errorf("rows = %+v, want one row with three lines", got)
		}
	})
}

// TestDeferredMerge_ByIDWithNoMatchAddsRow verifies that a merge by id with no
// row of that id adds the row as it is.
func TestDeferredMerge_ByIDWithNoMatchAddsRow(t *testing.T) {
	writers(t, nil, func(t *testing.T, w Writer) {
		in := deferredRow("pending", "", "", "first")
		stored, merged, err := w.MergeOrAddDeferred(in, true)
		if err != nil || merged || stored != in {
			t.Fatalf("stored = %+v merged = %v err = %v, want the new row, false, nil", stored, merged, err)
		}
		if got := listDeferred(t, w); !reflect.DeepEqual(got, []DeferredIssue{in}) {
			t.Errorf("rows = %+v, want the new row", got)
		}
	})
}

// TestDeferredMerge_ByIDResolvedOnlyAddsNextFreeSuffix verifies that a merge by
// id, when only resolved rows have the id, adds a row with the next free
// <id>-<n> suffix, counting from 2.
func TestDeferredMerge_ByIDResolvedOnlyAddsNextFreeSuffix(t *testing.T) {
	resolved := func(id string) DeferredIssue {
		r := deferredRow(id, "", "", "old")
		r.Status = StatusResolved
		return r
	}
	tests := []struct {
		name string
		rows []DeferredIssue
		want string
	}{
		{"id resolved", []DeferredIssue{resolved("pending")}, "pending-2"},
		{"id and -2 resolved", []DeferredIssue{resolved("pending"), resolved("pending-2")}, "pending-3"},
		{"id, -2 and -3 resolved", []DeferredIssue{resolved("pending"), resolved("pending-2"), resolved("pending-3")}, "pending-4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writers(t, tt.rows, func(t *testing.T, w Writer) {
				in := deferredRow("pending", "", "", "fresh")
				stored, merged, err := w.MergeOrAddDeferred(in, true)
				if err != nil || merged {
					t.Fatalf("merged = %v err = %v, want false nil", merged, err)
				}
				if stored.ID != tt.want || stored.Description != "fresh" || stored.Status != StatusOpen {
					t.Errorf("stored = %+v, want an open row %q with the new text", stored, tt.want)
				}
				if got := listDeferred(t, w); len(got) != len(tt.rows)+1 || got[len(got)-1].ID != tt.want {
					t.Errorf("rows = %+v, want one more row %q", got, tt.want)
				}
			})
		})
	}
}

// TestDeferredMerge_ByIDMergesIntoOpenSuffixedRow verifies that a merge by id
// appends to an open <id>-<n> row when the plain id is resolved, and does not
// touch a row whose id only starts with the same letters.
func TestDeferredMerge_ByIDMergesIntoOpenSuffixedRow(t *testing.T) {
	resolved := deferredRow("pending", "", "", "old")
	resolved.Status = StatusResolved
	open2 := deferredRow("pending-2", "", "", "- second run")
	other := deferredRow("pending-x", "", "", "unrelated")
	writers(t, []DeferredIssue{resolved, open2, other}, func(t *testing.T, w Writer) {
		stored, merged, err := w.MergeOrAddDeferred(deferredRow("pending", "", "", "third run"), true)
		if err != nil || !merged || stored.ID != "pending-2" {
			t.Fatalf("stored.ID = %q merged = %v err = %v, want pending-2 true nil", stored.ID, merged, err)
		}
		if got := listDeferred(t, w); len(got) != 3 || got[1].Description != "- second run\n- third run" {
			t.Errorf("rows = %+v, want three rows and the lines in pending-2", got)
		}
	})
}

// TestDeferredMerge_SameIDWithoutMergeChangesNothing verifies that a call
// without merge by id, with the id of an existing open or resolved row,
// returns that row and changes nothing.
func TestDeferredMerge_SameIDWithoutMergeChangesNothing(t *testing.T) {
	resolved := deferredRow("review-deferred-A", "a.go", ReasonNeedsDirection, "- x")
	resolved.Status = StatusResolved
	for name, existing := range map[string]DeferredIssue{
		"open":     deferredRow("review-deferred-A", "a.go", ReasonNeedsDirection, "- x"),
		"resolved": resolved,
	} {
		t.Run(name, func(t *testing.T) {
			writers(t, []DeferredIssue{existing}, func(t *testing.T, w Writer) {
				in := deferredRow("review-deferred-A", "a.go", ReasonNeedsDirection, "something else")
				for i := 0; i < 3; i++ {
					stored, merged, err := w.MergeOrAddDeferred(in, false)
					if err != nil || merged || stored != existing {
						t.Fatalf("call %d: stored = %+v merged = %v err = %v, want the existing row, false, nil", i, stored, merged, err)
					}
				}
				if got := listDeferred(t, w); !reflect.DeepEqual(got, []DeferredIssue{existing}) {
					t.Errorf("rows = %+v, want the existing row only", got)
				}
			})
		})
	}
}

// TestDeferredMerge_UnchangedCallLeavesFileBytes verifies that a call that adds
// no line does not rewrite deferred.json.
func TestDeferredMerge_UnchangedCallLeavesFileBytes(t *testing.T) {
	w := seedDeferred(t, deferredRow("pending", "", "", "- keep"))
	before, err := os.ReadFile(w.DeferredPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.MergeOrAddDeferred(deferredRow("pending", "", "", "keep"), true); err != nil {
		t.Fatalf("MergeOrAddDeferred: %v", err)
	}
	after, err := os.ReadFile(w.DeferredPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("file changed by a call that adds nothing:\nbefore %q\nafter  %q", before, after)
	}
}

// TestDeferredMerge_CreatesStoreWhenMissing verifies that a merge into a store
// with no history directory creates it and stores the row.
func TestDeferredMerge_CreatesStoreWhenMissing(t *testing.T) {
	w := NewFileWriter(filepath.Join(t.TempDir(), "history"))
	in := deferredRow("pending", "", "", "first")
	if _, _, err := w.MergeOrAddDeferred(in, true); err != nil {
		t.Fatalf("MergeOrAddDeferred: %v", err)
	}
	if got := listDeferred(t, w); !reflect.DeepEqual(got, []DeferredIssue{in}) {
		t.Errorf("rows = %+v, want the new row", got)
	}
}

// ---------------------------------------------------------------------------
// deferred.json failure modes: size bound, parse error, read error, write error
// ---------------------------------------------------------------------------

// writeDeferredFile writes raw content to a store and returns the writer.
func writeDeferredFile(t *testing.T, content []byte) *FileWriter {
	t.Helper()
	w := NewFileWriter(filepath.Join(t.TempDir(), "history"))
	if err := os.MkdirAll(w.root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.DeferredPath(), content, 0o644); err != nil {
		t.Fatal(err)
	}
	return w
}

// paddedEmptyList returns a valid empty JSON list that is exactly size bytes.
func paddedEmptyList(size int) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = ' '
	}
	copy(b, "[]")
	return b
}

// TestDeferredFile_SizeBound verifies that a file of exactly DeferredFileMax
// bytes is read and that a file of one byte more is refused with
// ErrDeferredTooLarge and left unchanged.
func TestDeferredFile_SizeBound(t *testing.T) {
	t.Run("exactly 8 MiB is read", func(t *testing.T) {
		w := writeDeferredFile(t, paddedEmptyList(DeferredFileMax))
		if rows, err := w.ListDeferred(); err != nil || len(rows) != 0 {
			t.Fatalf("ListDeferred rows = %v err = %v, want none, nil", rows, err)
		}
		if _, _, err := w.MergeOrAddDeferred(deferredRow("pending", "", "", "x"), true); err != nil {
			t.Fatalf("MergeOrAddDeferred on a file of exactly 8 MiB: %v", err)
		}
	})
	t.Run("8 MiB plus 1 byte is refused", func(t *testing.T) {
		content := paddedEmptyList(DeferredFileMax + 1)
		w := writeDeferredFile(t, content)
		_, _, err := w.MergeOrAddDeferred(deferredRow("pending", "", "", "x"), true)
		if !errors.Is(err, ErrDeferredTooLarge) {
			t.Fatalf("MergeOrAddDeferred error = %v, want ErrDeferredTooLarge", err)
		}
		if _, err := w.ListDeferred(); !errors.Is(err, ErrDeferredTooLarge) {
			t.Errorf("ListDeferred error = %v, want ErrDeferredTooLarge", err)
		}
		if err := w.AddDeferred(deferredRow("pending", "", "", "x")); !errors.Is(err, ErrDeferredTooLarge) {
			t.Errorf("AddDeferred error = %v, want ErrDeferredTooLarge", err)
		}
		if info, err := os.Stat(w.DeferredPath()); err != nil || info.Size() != int64(len(content)) {
			t.Errorf("file size after refusal = %v err = %v, want %d unchanged", info, err, len(content))
		}
	})
}

// TestDeferredFile_ParseFailure verifies that a file that does not parse fails
// with ErrDeferredParse only, and that the file is left unchanged.
func TestDeferredFile_ParseFailure(t *testing.T) {
	content := []byte("{garbage")
	w := writeDeferredFile(t, content)
	_, _, err := w.MergeOrAddDeferred(deferredRow("pending", "", "", "x"), true)
	if !errors.Is(err, ErrDeferredParse) {
		t.Fatalf("MergeOrAddDeferred error = %v, want ErrDeferredParse", err)
	}
	if errors.Is(err, ErrDeferredTooLarge) || errors.Is(err, ErrDeferredWrite) {
		t.Errorf("error %v also matches another cause", err)
	}
	if got, _ := os.ReadFile(w.DeferredPath()); string(got) != string(content) {
		t.Errorf("file after a parse failure = %q, want %q", got, content)
	}
}

// TestDeferredFile_ReadFailure verifies that a failed read, with a directory at
// the file path, returns an error that matches none of the store sentinels.
func TestDeferredFile_ReadFailure(t *testing.T) {
	// A directory at the file path: the size check passes, the read fails.
	w := NewFileWriter(filepath.Join(t.TempDir(), "history"))
	if err := os.MkdirAll(w.DeferredPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := w.MergeOrAddDeferred(deferredRow("pending", "", "", "x"), true)
	if err == nil {
		t.Fatal("MergeOrAddDeferred on an unreadable store: want an error, got nil")
	}
	for _, cause := range []error{ErrDeferredParse, ErrDeferredTooLarge, ErrDeferredWrite} {
		if errors.Is(err, cause) {
			t.Errorf("read failure %v matches %v, want no sentinel", err, cause)
		}
	}
	if info, statErr := os.Stat(w.DeferredPath()); statErr != nil || !info.IsDir() {
		t.Errorf("store path after a read failure = %v err = %v, want the directory untouched", info, statErr)
	}
}

// TestDeferredFile_WriteFailureLeavesFileUnchanged verifies that a failed write,
// with a read-only history directory, returns ErrDeferredWrite and keeps the
// old file.
func TestDeferredFile_WriteFailureLeavesFileUnchanged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a root user can write into a read-only directory")
	}
	w := seedDeferred(t, deferredRow("pending", "", "", "- keep"))
	before, err := os.ReadFile(w.DeferredPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(w.root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(w.root, 0o755) })

	_, _, err = w.MergeOrAddDeferred(deferredRow("pending", "", "", "new line"), true)
	if !errors.Is(err, ErrDeferredWrite) {
		t.Fatalf("MergeOrAddDeferred error = %v, want ErrDeferredWrite", err)
	}
	after, readErr := os.ReadFile(w.DeferredPath())
	if readErr != nil || string(after) != string(before) {
		t.Errorf("file after a write failure = %q err = %v, want %q", after, readErr, before)
	}
	entries, _ := os.ReadDir(w.root)
	for _, e := range entries {
		if e.Name() != "deferred.json" {
			t.Errorf("stray file %q left in the store directory", e.Name())
		}
	}
}

// TestDeferredFile_CreateDirFailureIsWriteError verifies that a history path
// that cannot be made into a directory gives ErrDeferredWrite for a merge and
// for an add.
func TestDeferredFile_CreateDirFailureIsWriteError(t *testing.T) {
	// The history path is a regular file, so the directory cannot be made.
	blocker := filepath.Join(t.TempDir(), "history")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := NewFileWriter(blocker)
	if _, _, err := w.MergeOrAddDeferred(deferredRow("pending", "", "", "x"), true); !errors.Is(err, ErrDeferredWrite) {
		t.Errorf("MergeOrAddDeferred error = %v, want ErrDeferredWrite", err)
	}
	if err := w.AddDeferred(deferredRow("pending", "", "", "x")); !errors.Is(err, ErrDeferredWrite) {
		t.Errorf("AddDeferred error = %v, want ErrDeferredWrite", err)
	}
}
