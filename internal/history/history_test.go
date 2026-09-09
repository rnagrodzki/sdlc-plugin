package history

import (
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
