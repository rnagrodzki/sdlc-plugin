package tools

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// dashQueueTotals returns review totals with the given found and unaccounted
// counts.
func dashQueueTotals(found, unaccounted int) *DashboardReviewTotals {
	return &DashboardReviewTotals{Found: found, Unaccounted: unaccounted}
}

// dashQueueDeferred returns one data.deferredFindings entry, as decoded JSON:
// line is a float64.
func dashQueueDeferred(title, reason, file string, line int) map[string]any {
	return map[string]any{
		"severity": "medium", "file": file, "line": float64(line),
		"title": title, "reason": reason, "description": "why " + title,
	}
}

// dashQueueFix returns one fix record, as dashboardReceivedReviewStep builds it.
func dashQueueFix(title, status, origin, file string, line int) DashboardFix {
	return DashboardFix{Title: title, Severity: "high", File: file, Line: line, Status: status, Origin: origin}
}

// dashQueueRowStatuses returns the status of each row of q, in order.
func dashQueueRowStatuses(q *DashboardReviewQueue) []string {
	out := []string{}
	for _, r := range q.Rows {
		out = append(out, r.Status)
	}
	return out
}

// dashQueueShip writes a ship state with the review ledger total, the fix
// records and the deferred findings given, plus the review dimensions of
// dashJoinReviewDims joined by ship run id, then returns its pipeline.
// total < 0 writes no reviewTotal, so the ship has no review ledger.
func dashQueueShip(t *testing.T, total int, fixes, deferred []any, flags map[string]any) DashboardPipeline {
	t.Helper()
	root := dashRoot(t)
	data := dashJoinShipData()
	healing := map[string]any{}
	if total >= 0 {
		healing["reviewTotal"] = map[string]any{"total": total}
	}
	if fixes != nil {
		healing["fixProgress"] = fixes
	}
	data["healing"] = healing
	if deferred != nil {
		data["deferredFindings"] = deferred
	}
	if flags != nil {
		data["flags"] = flags
	}
	dashWriteState(t, root, dashJoinShipFile, data, dashJoinFresh)
	dashJoinReviewDims(t, root, dashJoinReview)
	dashJoinRunMeta(t, root, dashJoinReview, reviewRunMeta{Branch: "feat/x", StartedAt: "2026-10-07T08:31:00Z", ShipRunID: dashJoinShipID})
	return dashOne(t, root)
}

// dashQueueOf returns the review queue of the review step of p, or fails the
// test.
func dashQueueOf(t *testing.T, p DashboardPipeline) *DashboardReviewQueue {
	t.Helper()
	d := dashJoinStep(t, p, "review").Detail
	if d == nil || d.ReviewQueue == nil {
		t.Fatalf("review detail = %+v, want a reviewQueue", d)
	}
	return d.ReviewQueue
}

// TestDashboardReviewQueue_StatusMapping checks every row of the status
// table: the fix statuses stay as they are, and the deferred reasons give
// below or deferred.
func TestDashboardReviewQueue_StatusMapping(t *testing.T) {
	t.Run("fix statuses", func(t *testing.T) {
		for _, status := range healingFixStatuses {
			t.Run(status, func(t *testing.T) {
				fixes := []DashboardFix{dashQueueFix("Nil map write", status, "local-review", "a.go", 42)}
				q := dashboardReviewQueue(map[string]any{}, dashQueueTotals(1, 0), fixes)
				want := []DashboardQueueRow{{
					Title: "Nil map write", Severity: "high", File: "a.go", Line: "42",
					Status: status, Origin: "local-review",
				}}
				if !reflect.DeepEqual(q.Rows, want) {
					t.Errorf("rows = %+v, want %+v", q.Rows, want)
				}
			})
		}
	})

	t.Run("deferred reasons", func(t *testing.T) {
		cases := []struct{ reason, want string }{
			{"below-threshold", "below"},
			{"", "below"},
			{"needs-direction", "deferred"},
			{"disagree", "deferred"},
			{"wont-fix", "deferred"},
		}
		for _, c := range cases {
			t.Run("reason "+c.reason, func(t *testing.T) {
				data := map[string]any{"deferredFindings": []any{dashQueueDeferred("Long loop", c.reason, "b.go", 7)}}
				q := dashboardReviewQueue(data, dashQueueTotals(1, 0), nil)
				if len(q.Rows) != 1 {
					t.Fatalf("rows = %+v, want 1", q.Rows)
				}
				if got := q.Rows[0].Status; got != c.want {
					t.Errorf("status = %q, want %q", got, c.want)
				}
			})
		}
	})

	t.Run("open rows when no fix and no deferred finding", func(t *testing.T) {
		q := dashQueueOf(t, dashQueueShip(t, 3, nil, nil, nil))
		if got, want := dashQueueRowStatuses(q), []string{"open", "open", "open"}; !reflect.DeepEqual(got, want) {
			t.Errorf("statuses = %v, want %v", got, want)
		}
	})

	t.Run("open rows stop when a deferred finding exists", func(t *testing.T) {
		deferred := []any{dashQueueDeferred("Long loop", "wont-fix", "b.go", 7)}
		q := dashQueueOf(t, dashQueueShip(t, 4, nil, deferred, nil))
		if got, want := dashQueueRowStatuses(q), []string{"deferred"}; !reflect.DeepEqual(got, want) {
			t.Errorf("statuses = %v, want %v", got, want)
		}
	})
}

// TestDashboardReviewQueue_RowSources checks the fields of each row source:
// the fix record, the deferred finding, and the review finding.
func TestDashboardReviewQueue_RowSources(t *testing.T) {
	t.Run("order is fix records, then deferred findings", func(t *testing.T) {
		data := map[string]any{"deferredFindings": []any{dashQueueDeferred("Long loop", "wont-fix", "b.go", 7)}}
		fixes := []DashboardFix{
			dashQueueFix("Nil map write", "fixed", "local-review", "a.go", 42),
			dashQueueFix("Missing check", "queued", "pr-comment", "c.go", 0),
		}
		q := dashboardReviewQueue(data, dashQueueTotals(3, 0), fixes)
		var titles []string
		for _, r := range q.Rows {
			titles = append(titles, r.Title)
		}
		if want := []string{"Nil map write", "Missing check", "Long loop"}; !reflect.DeepEqual(titles, want) {
			t.Errorf("titles = %v, want %v", titles, want)
		}
		if got := q.Rows[1]; got.Origin != "pr-comment" || got.Line != "" {
			t.Errorf("pr-comment fix row = %+v, want origin pr-comment and no line", got)
		}
	})

	t.Run("deferred finding row", func(t *testing.T) {
		entry := dashQueueDeferred("Long loop", "wont-fix", "b.go", 7)
		entry["severity"] = "low"
		q := dashboardReviewQueue(map[string]any{"deferredFindings": []any{entry}}, dashQueueTotals(1, 0), nil)
		want := DashboardQueueRow{
			Title: "Long loop", Severity: "low", File: "b.go", Line: "7",
			Status: "deferred", Origin: dashboardOriginLocal, Text: "why Long loop",
		}
		if len(q.Rows) != 1 || q.Rows[0] != want {
			t.Errorf("rows = %+v, want [%+v]", q.Rows, want)
		}
	})

	t.Run("deferred finding with no line has an empty line", func(t *testing.T) {
		entry := dashQueueDeferred("Long loop", "wont-fix", "b.go", 0)
		entry["line"] = nil
		q := dashboardReviewQueue(map[string]any{"deferredFindings": []any{entry}}, dashQueueTotals(1, 0), nil)
		if len(q.Rows) != 1 || q.Rows[0].Line != "" {
			t.Errorf("rows = %+v, want one row with line empty", q.Rows)
		}
	})

	t.Run("deferred finding with a bad entry or no title is skipped", func(t *testing.T) {
		data := map[string]any{"deferredFindings": []any{
			"not a map",
			map[string]any{"reason": "wont-fix"},
			dashQueueDeferred("Long loop", "wont-fix", "b.go", 7),
		}}
		q := dashboardReviewQueue(data, dashQueueTotals(3, 0), nil)
		if len(q.Rows) != 1 || q.Rows[0].Title != "Long loop" {
			t.Errorf("rows = %+v, want only Long loop", q.Rows)
		}
	})

	t.Run("deferred title is redacted and cut", func(t *testing.T) {
		long := strings.Repeat("x", 200)
		data := map[string]any{"deferredFindings": []any{dashQueueDeferred(long, "wont-fix", "b.go", 7)}}
		q := dashboardReviewQueue(data, dashQueueTotals(1, 0), nil)
		if n := len([]rune(q.Rows[0].Title)); n > dashboardPreviewTextMax+1 {
			t.Errorf("title runes = %d, want <= %d", n, dashboardPreviewTextMax+1)
		}
	})

	t.Run("deferred description is redacted", func(t *testing.T) {
		entry := dashQueueDeferred("Leaky log", "wont-fix", "b.go", 7)
		entry["description"] = "Mail dev@example.com about the leak"
		data := map[string]any{"deferredFindings": []any{entry}}
		q := dashboardReviewQueue(data, dashQueueTotals(1, 0), nil)
		if len(q.Rows) != 1 {
			t.Fatalf("rows = %+v, want 1 row", q.Rows)
		}
		if got := q.Rows[0].Text; strings.Contains(got, "dev@example.com") || got == "" {
			t.Errorf("text = %q, want the description with the email redacted", got)
		}
	})

	t.Run("open row from a review finding", func(t *testing.T) {
		q := dashQueueOf(t, dashQueueShip(t, 3, nil, nil, nil))
		// Highest severity first: security high, medium, then low.
		want := []DashboardQueueRow{
			{Title: "token check skips expiry", Severity: "high", File: "internal/x.go", Line: "42", Status: "open", Origin: dashboardOriginLocal, Dimension: "security", Text: "token check skips expiry"},
			{Title: "a", Severity: "medium", File: "internal/a.go", Line: "1", Status: "open", Origin: dashboardOriginLocal, Dimension: "security", Text: "a"},
			{Title: "b", Severity: "low", File: "internal/b.go", Line: "", Status: "open", Origin: dashboardOriginLocal, Dimension: "security", Text: "b"},
		}
		if !reflect.DeepEqual(q.Rows, want) {
			t.Errorf("rows = %+v, want %+v", q.Rows, want)
		}
	})

	t.Run("open row title is the first line, cut at 120 runes", func(t *testing.T) {
		long := strings.Repeat("y", 200)
		q := &DashboardReviewQueue{Rows: []DashboardQueueRow{}}
		dashboardQueueAddFindings(q, []DashboardDimension{{
			Name: "docs",
			FindingItems: []DashboardReviewFinding{
				{Text: "first line\nsecond line", Severity: "low", File: "a.go", Line: "1"},
				{Text: long, Severity: "low", File: "b.go", Line: "2"},
			},
		}})
		if len(q.Rows) != 2 {
			t.Fatalf("rows = %+v, want 2", q.Rows)
		}
		if got := q.Rows[0].Title; got != "first line" {
			t.Errorf("title = %q, want %q", got, "first line")
		}
		if got := q.Rows[0].Text; got != "first line\nsecond line" {
			t.Errorf("text = %q, want the whole finding text", got)
		}
		if n := len([]rune(q.Rows[1].Title)); n > dashboardPreviewTextMax+1 {
			t.Errorf("title runes = %d, want <= %d", n, dashboardPreviewTextMax+1)
		}
	})
}

// TestDashboardReviewQueue_FixTwin checks that a deferred finding with the
// same file, line and title as a fix record shows as the fix record only, and
// that a near miss keeps its own row.
func TestDashboardReviewQueue_FixTwin(t *testing.T) {
	fixes := []DashboardFix{dashQueueFix("Nil map write", "fixing", "local-review", "a.go", 42)}
	data := map[string]any{"deferredFindings": []any{
		dashQueueDeferred("Nil map write", "below-threshold", "a.go", 42),  // twin
		dashQueueDeferred("Nil map write", "below-threshold", "a.go", 43),  // other line
		dashQueueDeferred("Nil map write", "below-threshold", "z.go", 42),  // other file
		dashQueueDeferred("Nil map writes", "below-threshold", "a.go", 42), // other title
	}}
	q := dashboardReviewQueue(data, dashQueueTotals(5, 0), fixes)
	if got, want := dashQueueRowStatuses(q), []string{"fixing", "below", "below", "below"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	if got := q.Rows[0]; got.Origin != "local-review" || got.Text != "" {
		t.Errorf("fix row = %+v, want the fix record, not the deferred entry", got)
	}
}

// TestDashboardReviewQueue_Threshold checks that threshold is the
// reviewThreshold flag, and info when the flag is absent or not a severity.
func TestDashboardReviewQueue_Threshold(t *testing.T) {
	cases := []struct {
		name string
		data map[string]any
		want string
	}{
		{"no flags", map[string]any{}, "info"},
		{"flags without the key", map[string]any{"flags": map[string]any{"auto": true}}, "info"},
		{"not a severity", map[string]any{"flags": map[string]any{"reviewThreshold": "urgent"}}, "info"},
		{"not a string", map[string]any{"flags": map[string]any{"reviewThreshold": 3.0}}, "info"},
		{"high", map[string]any{"flags": map[string]any{"reviewThreshold": "high"}}, "high"},
		{"medium", map[string]any{"flags": map[string]any{"reviewThreshold": "medium"}}, "medium"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := dashboardReviewQueue(c.data, dashQueueTotals(0, 0), nil).Threshold; got != c.want {
				t.Errorf("threshold = %q, want %q", got, c.want)
			}
		})
	}

	t.Run("read from a ship state", func(t *testing.T) {
		q := dashQueueOf(t, dashQueueShip(t, 3, nil, nil, map[string]any{"reviewThreshold": "high"}))
		if q.Threshold != "high" {
			t.Errorf("threshold = %q, want high", q.Threshold)
		}
	})
}

// TestDashboardReviewQueue_Reason checks the reason table, and that the two
// reasons never apply together.
func TestDashboardReviewQueue_Reason(t *testing.T) {
	below := dashQueueDeferred("Long loop", "below-threshold", "b.go", 7)
	wontFix := dashQueueDeferred("Long loop", "wont-fix", "b.go", 7)
	cases := []struct {
		name     string
		found    int
		deferred []any
		fixes    []DashboardFix
		want     string
	}{
		{"no finding, no row", 0, nil, nil, dashboardQueueReasonNone},
		{"findings, no row yet", 2, nil, nil, ""},
		{"every row below", 2, []any{below, below}, nil, dashboardQueueReasonAllBelow},
		{"one row below", 1, []any{below}, nil, dashboardQueueReasonAllBelow},
		{"below and deferred", 2, []any{below, wontFix}, nil, ""},
		{"below and a fix", 2, []any{dashQueueDeferred("Other", "below-threshold", "c.go", 1)},
			[]DashboardFix{dashQueueFix("Nil map write", "fixed", "local-review", "a.go", 42)}, ""},
		{"total 0 but a row", 0, []any{below}, nil, dashboardQueueReasonAllBelow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := map[string]any{}
			if c.deferred != nil {
				data["deferredFindings"] = c.deferred
			}
			q := dashboardReviewQueue(data, dashQueueTotals(c.found, 0), c.fixes)
			if q.Reason != c.want {
				t.Errorf("reason = %q, want %q", q.Reason, c.want)
			}
			if q.Reason == dashboardQueueReasonNone && len(q.Rows) != 0 {
				t.Errorf("reason %q with %d rows", q.Reason, len(q.Rows))
			}
			if q.Reason == dashboardQueueReasonAllBelow && len(q.Rows) == 0 {
				t.Errorf("reason %q with no row", q.Reason)
			}
		})
	}

	t.Run("open rows clear the no-findings reason", func(t *testing.T) {
		q := dashboardReviewQueue(map[string]any{}, dashQueueTotals(0, 0), nil)
		dashboardQueueAddFindings(q, []DashboardDimension{{
			Name:         "docs",
			FindingItems: []DashboardReviewFinding{{Text: "a", Severity: "low", File: "a.go", Line: "1"}},
		}})
		if q.Reason != "" || len(q.Rows) != 1 {
			t.Errorf("queue = %+v, want one row and no reason", q)
		}
	})

	t.Run("no open row keeps the reason", func(t *testing.T) {
		q := dashboardReviewQueue(map[string]any{}, dashQueueTotals(0, 0), nil)
		dashboardQueueAddFindings(q, []DashboardDimension{{Name: "docs", FindingItems: []DashboardReviewFinding{}}})
		if q.Reason != dashboardQueueReasonNone || len(q.Rows) != 0 {
			t.Errorf("queue = %+v, want no row and reason %q", q, dashboardQueueReasonNone)
		}
	})
}

// TestDashboardReviewQueue_RowsListAndCap checks that rows is [] when empty
// and holds at most 200 rows.
func TestDashboardReviewQueue_RowsListAndCap(t *testing.T) {
	t.Run("empty rows encode as []", func(t *testing.T) {
		q := dashboardReviewQueue(map[string]any{}, dashQueueTotals(0, 0), nil)
		if q.Rows == nil {
			t.Fatal("rows = nil, want an empty list")
		}
		b, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}
		want := `{"threshold":"info","rows":[],"unrouted":0,"reason":"no-findings"}`
		if string(b) != want {
			t.Errorf("json = %s, want %s", b, want)
		}
	})

	t.Run("deferred findings stop at 200", func(t *testing.T) {
		var deferred []any
		for i := 0; i < 250; i++ {
			deferred = append(deferred, dashQueueDeferred(fmt.Sprintf("finding %d", i), "wont-fix", "a.go", i+1))
		}
		q := dashboardReviewQueue(map[string]any{"deferredFindings": deferred}, dashQueueTotals(250, 0), nil)
		if len(q.Rows) != 200 {
			t.Fatalf("rows = %d, want 200", len(q.Rows))
		}
		if q.Rows[0].Title != "finding 0" || q.Rows[199].Title != "finding 199" {
			t.Errorf("rows kept %q .. %q, want the first 200", q.Rows[0].Title, q.Rows[199].Title)
		}
	})

	t.Run("fix records come before the cut", func(t *testing.T) {
		var deferred []any
		for i := 0; i < 250; i++ {
			deferred = append(deferred, dashQueueDeferred(fmt.Sprintf("finding %d", i), "wont-fix", "a.go", i+1))
		}
		fixes := []DashboardFix{dashQueueFix("Nil map write", "fixed", "local-review", "z.go", 1)}
		q := dashboardReviewQueue(map[string]any{"deferredFindings": deferred}, dashQueueTotals(251, 0), fixes)
		if len(q.Rows) != 200 || q.Rows[0].Title != "Nil map write" {
			t.Errorf("rows = %d, first %q, want 200 with the fix first", len(q.Rows), q.Rows[0].Title)
		}
	})

	t.Run("open rows stop at 200", func(t *testing.T) {
		var items []DashboardReviewFinding
		for i := 0; i < 250; i++ {
			items = append(items, DashboardReviewFinding{Text: fmt.Sprintf("finding %d", i), Severity: "low", File: "a.go", Line: fmt.Sprint(i + 1)})
		}
		q := &DashboardReviewQueue{Rows: []DashboardQueueRow{}}
		dashboardQueueAddFindings(q, []DashboardDimension{{Name: "docs", FindingItems: items}})
		if len(q.Rows) != 200 {
			t.Errorf("rows = %d, want 200", len(q.Rows))
		}
	})
}

// TestDashboardReviewQueue_Unrouted checks that unrouted is the unaccounted
// count when fix rows exist, else 0.
func TestDashboardReviewQueue_Unrouted(t *testing.T) {
	fixes := []DashboardFix{dashQueueFix("Nil map write", "fixed", "local-review", "a.go", 42)}
	cases := []struct {
		name        string
		fixes       []DashboardFix
		unaccounted int
		want        int
	}{
		{"fix rows", fixes, 2, 2},
		{"no fix rows", nil, 2, 0},
		{"fix rows, nothing unaccounted", fixes, 0, 0},
		{"fix rows, records disagree", fixes, -1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := dashboardReviewQueue(map[string]any{}, dashQueueTotals(3, c.unaccounted), c.fixes)
			if q.Unrouted != c.want {
				t.Errorf("unrouted = %d, want %d", q.Unrouted, c.want)
			}
		})
	}
}

// TestDashboardReviewQueue_FindingMatch checks that a row gets dimension and
// text only from the one review finding with the same file and line.
func TestDashboardReviewQueue_FindingMatch(t *testing.T) {
	dims := []DashboardDimension{
		{Name: "security", FindingItems: []DashboardReviewFinding{
			{Text: "token check skips expiry", Severity: "high", File: "a.go", Line: "42"},
			{Text: "first of two", Severity: "low", File: "d.go", Line: "9"},
			{Text: "no line", Severity: "low", File: "e.go", Line: ""},
		}},
		{Name: "go-review", FindingItems: []DashboardReviewFinding{
			{Text: "second of two", Severity: "low", File: "d.go", Line: "9"},
			{Text: "other file", Severity: "low", File: "f.go", Line: "42"},
		}},
	}
	row := func(file string, line int) DashboardFix {
		return dashQueueFix("t "+file, "fixed", "local-review", file, line)
	}
	fixes := []DashboardFix{
		row("a.go", 42), // one match
		row("a.go", 43), // same file, other line
		row("d.go", 9),  // two matches in two dimensions
		row("e.go", 0),  // no line: no match
		row("g.go", 5),  // no finding
		row("", 0),      // no file
	}
	q := dashboardReviewQueue(map[string]any{}, dashQueueTotals(6, 0), fixes)
	dashboardQueueAddFindings(q, dims)

	type dimText struct{ dimension, text string }
	var got []dimText
	for _, r := range q.Rows {
		got = append(got, dimText{r.Dimension, r.Text})
	}
	want := []dimText{
		{"security", "token check skips expiry"},
		{"", ""},
		{"", ""},
		{"", ""},
		{"", ""},
		{"", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dimension and text = %+v, want %+v", got, want)
	}

	t.Run("two matches in one dimension give none", func(t *testing.T) {
		twice := []DashboardDimension{{Name: "security", FindingItems: []DashboardReviewFinding{
			{Text: "one", Severity: "low", File: "d.go", Line: "9"},
			{Text: "two", Severity: "low", File: "d.go", Line: "9"},
		}}}
		q := dashboardReviewQueue(map[string]any{}, dashQueueTotals(1, 0), []DashboardFix{row("d.go", 9)})
		dashboardQueueAddFindings(q, twice)
		if r := q.Rows[0]; r.Dimension != "" || r.Text != "" {
			t.Errorf("row = %+v, want no dimension and no text", r)
		}
	})

	t.Run("a deferred row keeps its description", func(t *testing.T) {
		entry := dashQueueDeferred("Long loop", "wont-fix", "a.go", 42)
		q := dashboardReviewQueue(map[string]any{"deferredFindings": []any{entry}}, dashQueueTotals(1, 0), nil)
		dashboardQueueAddFindings(q, dims)
		if r := q.Rows[0]; r.Dimension != "security" || r.Text != "why Long loop" {
			t.Errorf("row = %+v, want dimension security and the deferred description", r)
		}
	})

	t.Run("nil queue is a no-op", func(t *testing.T) {
		dashboardQueueAddFindings(nil, dims)
	})
}

// TestDashboardReviewQueue_Join checks the cases of the join table through a
// full snapshot: no ledger, ledger with no valid fix record, ledger with fix
// records.
func TestDashboardReviewQueue_Join(t *testing.T) {
	t.Run("no review ledger gives no queue", func(t *testing.T) {
		ship := dashQueueShip(t, -1, []any{dashRRFix("Nil map write", "fixed", "", "")}, nil, nil)
		d := dashJoinStep(t, ship, "review").Detail
		if d == nil {
			t.Fatal("review detail = nil, want the joined dimensions")
		}
		if d.ReviewQueue != nil {
			t.Errorf("reviewQueue = %+v, want absent", d.ReviewQueue)
		}
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "reviewQueue") {
			t.Errorf("review detail JSON holds reviewQueue: %s", b)
		}
	})

	t.Run("no ledger and no review gives no detail", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteState(t, root, dashJoinShipFile, dashJoinShipData(), dashJoinFresh)
		if d := dashJoinStep(t, dashOne(t, root), "review").Detail; d != nil {
			t.Errorf("review detail = %+v, want none", d)
		}
	})

	t.Run("ledger and no valid fix record builds from deferred findings", func(t *testing.T) {
		deferred := []any{dashQueueDeferred("Long loop", "wont-fix", "b.go", 7)}
		// A fixProgress list with no valid record: rr is nil.
		ship := dashQueueShip(t, 2, []any{map[string]any{"title": ""}}, deferred, nil)
		q := dashQueueOf(t, ship)
		if got, want := dashQueueRowStatuses(q), []string{"deferred"}; !reflect.DeepEqual(got, want) {
			t.Errorf("statuses = %v, want %v", got, want)
		}
		if q.Unrouted != 0 {
			t.Errorf("unrouted = %d, want 0 with no fix row", q.Unrouted)
		}
	})

	t.Run("ledger and fix records build from fixes then deferred findings", func(t *testing.T) {
		fixes := []any{
			dashRRFix("Token check", "fixed", "2026-10-07T08:42:00Z", "2026-10-07T08:43:00Z"),
			dashRRFix("Other", "fixing", "2026-10-07T08:42:00Z", "2026-10-07T08:43:00Z"),
		}
		fixes[1].(map[string]any)["file"] = "internal/other.go"
		deferred := []any{dashQueueDeferred("Long loop", "below-threshold", "b.go", 7)}
		ship := dashQueueShip(t, 4, fixes, deferred, nil)
		q := dashQueueOf(t, ship)
		if got, want := dashQueueRowStatuses(q), []string{"fixed", "fixing", "below"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("statuses = %v, want %v", got, want)
		}
		// 4 found, 0 fixed with local-review in healing.fixed, 1 deferred.
		if q.Unrouted != 3 {
			t.Errorf("unrouted = %d, want 3", q.Unrouted)
		}
		// The first fix has the file and line of the one security finding.
		if r := q.Rows[0]; r.Dimension != "security" || r.Text != "token check skips expiry" {
			t.Errorf("fix row = %+v, want dimension security and the review text", r)
		}
		if r := q.Rows[1]; r.Dimension != "" || r.Text != "" {
			t.Errorf("unmatched fix row = %+v, want no dimension and no text", r)
		}
	})

	t.Run("JSON shape", func(t *testing.T) {
		fixes := []any{dashRRFix("Nil map write", "fixed", "2026-10-07T08:42:00Z", "2026-10-07T08:43:00Z")}
		q := dashQueueOf(t, dashQueueShip(t, 1, fixes, nil, nil))
		b, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}
		want := `{"threshold":"info","rows":[{"title":"Nil map write","severity":"high","file":"internal/x.go","line":"42",` +
			`"status":"fixed","origin":"local-review","dimension":"security","text":"token check skips expiry"}],"unrouted":1,"reason":""}`
		if string(b) != want {
			t.Errorf("json = %s\nwant   %s", b, want)
		}
	})

	t.Run("ledger and no row yet with no review run keeps rows empty", func(t *testing.T) {
		root := dashRoot(t)
		data := dashJoinShipData()
		data["healing"] = map[string]any{"reviewTotal": map[string]any{"total": 2}}
		dashWriteState(t, root, dashJoinShipFile, data, dashJoinFresh)
		q := dashQueueOf(t, dashOne(t, root))
		if q.Rows == nil || len(q.Rows) != 0 || q.Reason != "" {
			t.Errorf("queue = %+v, want empty rows and no reason", q)
		}
	})
}
