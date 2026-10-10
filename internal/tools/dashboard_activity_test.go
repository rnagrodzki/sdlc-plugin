package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// dashWriteJSONL writes lines (each a raw JSON object, no trailing newline)
// to path as a JSONL file, creating parent folders as needed.
func dashWriteJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardActivity_Sessions(t *testing.T) {
	t.Run("groups by sessionId, counts, firstSeen/lastSeen, branch, active", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteJSONL(t, cliEvidencePath(root),
			`{"ts":"2026-10-07T09:00:00Z","branch":"feat/x","command":"go build","sessionId":"s1"}`,
			`{"ts":"2026-10-07T09:50:00Z","branch":"feat/y","command":"go test","sessionId":"s1"}`,
			`{"ts":"2026-10-07T08:00:00Z","branch":"main","command":"ls","sessionId":"s2"}`,
		)
		dashWriteJSONL(t, userInputPath(root),
			`{"ts":"2026-10-07T09:10:00Z","branch":"feat/x","text":"skip the low findings","sessionId":"s1","kind":"prompt"}`,
			`{"ts":"2026-10-07T08:05:00Z","text":"yes","sessionId":"s2","kind":"answer"}`,
		)
		dashWriteJSONL(t, mcpEvidencePath(root),
			`{"ts":"2026-10-07T09:55:00Z","tool":"execute_state","sessionId":"s1"}`,
			`{"ts":"2026-10-07T08:10:00Z","tool":"ship_prepare","sessionId":""}`, // empty sessionId: dropped
		)

		sessions := dashboardSessionsFromEvidence(root, dashNow)
		if len(sessions) != 2 {
			t.Fatalf("sessions = %d, want 2: %+v", len(sessions), sessions)
		}

		// Newest lastSeen first: s1 (09:55) before s2 (08:10).
		s1, s2 := sessions[0], sessions[1]
		if s1.ID != "s1" || s2.ID != "s2" {
			t.Fatalf("session order = [%s, %s], want [s1, s2]", s1.ID, s2.ID)
		}

		if s1.Counts.Commands != 2 || s1.Counts.Prompts != 1 || s1.Counts.MCPCalls != 1 {
			t.Errorf("s1 counts = %+v, want {2,1,1}", s1.Counts)
		}
		if s1.FirstSeen != "2026-10-07T09:00:00Z" || s1.LastSeen != "2026-10-07T09:55:00Z" {
			t.Errorf("s1 first/last = %s/%s", s1.FirstSeen, s1.LastSeen)
		}
		// Branch is the chronologically-latest non-empty branch: 09:50 "feat/y"
		// beats the 09:00 "feat/x" command and the 09:10 "feat/x" prompt.
		if s1.Branch != "feat/y" {
			t.Errorf("s1 branch = %q, want %q", s1.Branch, "feat/y")
		}
		if !s1.Active {
			t.Errorf("s1 active = false, want true (lastSeen 5 min before dashNow)")
		}

		if s2.Counts.Commands != 1 || s2.Counts.Prompts != 1 || s2.Counts.MCPCalls != 0 {
			t.Errorf("s2 counts = %+v, want {1,1,0}", s2.Counts)
		}
		if s2.Active {
			t.Errorf("s2 active = true, want false (lastSeen ~2h before dashNow)")
		}
	})

	t.Run("an mcp event with no matching branch leaves session branch empty", func(t *testing.T) {
		root := dashRoot(t)
		dashWriteJSONL(t, mcpEvidencePath(root), `{"ts":"2026-10-07T09:55:00Z","tool":"execute_state","sessionId":"solo"}`)
		sessions := dashboardSessionsFromEvidence(root, dashNow)
		if len(sessions) != 1 || sessions[0].Branch != "" {
			t.Fatalf("sessions = %+v, want one session with empty branch", sessions)
		}
	})

	t.Run("timeline holds the newest 50 events, newest first", func(t *testing.T) {
		root := dashRoot(t)
		lines := make([]string, 60)
		for i := 0; i < 60; i++ {
			ts := dashNow.Add(-time.Duration(60-i) * time.Minute).Format(time.RFC3339)
			lines[i] = fmt.Sprintf(`{"ts":"%s","branch":"feat/x","command":"cmd%d","sessionId":"s1"}`, ts, i)
		}
		dashWriteJSONL(t, cliEvidencePath(root), lines...)

		sessions := dashboardSessionsFromEvidence(root, dashNow)
		if len(sessions) != 1 {
			t.Fatalf("sessions = %d, want 1", len(sessions))
		}
		tl := sessions[0].Timeline
		if len(tl) != dashboardSessionTimelineMax {
			t.Fatalf("timeline len = %d, want %d", len(tl), dashboardSessionTimelineMax)
		}
		// Newest first: index 0 should be the 59th (last) written line's time.
		wantNewest := dashNow.Add(-time.Duration(60-59) * time.Minute).Format(time.RFC3339)
		if tl[0].At != wantNewest {
			t.Errorf("tl[0].At = %q, want %q (newest first)", tl[0].At, wantNewest)
		}
		for i := 1; i < len(tl); i++ {
			if tl[i].At > tl[i-1].At {
				t.Fatalf("timeline not newest-first at index %d: %q after %q", i, tl[i].At, tl[i-1].At)
			}
		}
	})

	t.Run("prompt text is redacted and truncated to 120 runes", func(t *testing.T) {
		root := dashRoot(t)
		long := strings.Repeat("x", 200)
		dashWriteJSONL(t, userInputPath(root),
			`{"ts":"2026-10-07T09:00:00Z","text":"Bearer verysecrettoken123 `+long+`","sessionId":"s1","kind":"prompt"}`,
		)
		sessions := dashboardSessionsFromEvidence(root, dashNow)
		if len(sessions) != 1 || len(sessions[0].Timeline) != 1 {
			t.Fatalf("sessions = %+v", sessions)
		}
		text := sessions[0].Timeline[0].Text
		if strings.Contains(text, "verysecrettoken123") {
			t.Errorf("text not redacted: %q", text)
		}
		if !strings.Contains(text, "Bearer [REDACTED]") {
			t.Errorf("text = %q, want it to contain the redactor's replacement", text)
		}
		if !strings.HasSuffix(text, "…") {
			t.Errorf("text = %q, want a truncation marker", text)
		}
		if n := len([]rune(text)); n > dashboardPreviewTextMax+1 {
			t.Errorf("text rune length = %d, want <= %d", n, dashboardPreviewTextMax+1)
		}
	})

	t.Run("no evidence files gives no sessions", func(t *testing.T) {
		root := dashRoot(t)
		sessions := dashboardSessionsFromEvidence(root, dashNow)
		if len(sessions) != 0 {
			t.Errorf("sessions = %+v, want none", sessions)
		}
	})
}

func TestDashboardActivity_Learnings(t *testing.T) {
	t.Run("filters to the last 24h, extracts heading/runId/branch, skips undated entries", func(t *testing.T) {
		root := dashRoot(t)
		content := "# SDLC Execution Learnings\n\n" +
			"<!-- sdlc:run=20261007T070000 branch=feat/x -->\n" +
			"## 2026-10-07 — plan: line-keyed skillcheck maps\n- detail one\n- detail two\n\n" +
			"## 2026-10-06 — fix: yesterday evening's entry\n- still within the last 24h\n\n" +
			"## 2026-10-05 — old: two days back\n- outside the last 24h\n\n" +
			"## 2026-09-01 — old: entry from last month\n- stale\n\n" +
			"**Some bug found.** No date anywhere in this entry's text.\n\n" +
			"**Investigated the session cache on 2026-10-07.**\nRoot cause was a stale pointer.\n"
		path := learningsLogPath(root)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		got := dashboardRecentLearnings(root, dashNow)
		if len(got) != 3 {
			t.Fatalf("learnings = %d, want 3: %+v", len(got), got)
		}

		byHeading := map[string]DashboardLearning{}
		for _, l := range got {
			byHeading[l.Heading] = l
		}

		tagged, ok := byHeading["plan: line-keyed skillcheck maps"]
		if !ok {
			t.Fatalf("missing tagged entry, got %+v", got)
		}
		if tagged.Date != "2026-10-07" || tagged.RunID != "20261007T070000" || tagged.Branch != "feat/x" {
			t.Errorf("tagged entry = %+v, want date 2026-10-07, runId 20261007T070000, branch feat/x", tagged)
		}

		fallback, ok := byHeading["Investigated the session cache on 2026-10-07."]
		if !ok {
			t.Fatalf("missing fallback-heading entry, got %+v", got)
		}
		if fallback.Date != "2026-10-07" || fallback.RunID != "" || fallback.Branch != "" {
			t.Errorf("fallback entry = %+v, want date 2026-10-07, no run tag", fallback)
		}

		yesterday, ok := byHeading["fix: yesterday evening's entry"]
		if !ok {
			t.Fatalf("missing yesterday's entry (date-string window should include it), got %+v", got)
		}
		if yesterday.Date != "2026-10-06" {
			t.Errorf("yesterday entry date = %q, want 2026-10-06", yesterday.Date)
		}
		if _, ok := byHeading["old: two days back"]; ok {
			t.Errorf("entry dated 2026-10-05 should be outside the last-24h window, got %+v", got)
		}
	})

	t.Run("missing learnings log gives no entries", func(t *testing.T) {
		root := dashRoot(t)
		got := dashboardRecentLearnings(root, dashNow)
		if got != nil {
			t.Errorf("learnings = %+v, want nil", got)
		}
	})

	t.Run("a long heading line is truncated to 120 runes", func(t *testing.T) {
		root := dashRoot(t)
		longTitle := strings.Repeat("y", 200)
		content := "# SDLC Execution Learnings\n\n" +
			"## 2026-10-07 — execute: " + longTitle + "\n- detail\n"
		path := learningsLogPath(root)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		got := dashboardRecentLearnings(root, dashNow)
		if len(got) != 1 {
			t.Fatalf("learnings = %d, want 1: %+v", len(got), got)
		}
		if n := len([]rune(got[0].Heading)); n > dashboardPreviewTextMax+1 {
			t.Errorf("heading rune length = %d, want <= %d", n, dashboardPreviewTextMax+1)
		}
		if !strings.HasSuffix(got[0].Heading, "…") {
			t.Errorf("heading = %q, want a truncation marker", got[0].Heading)
		}
	})
}

// TestDashboardLearningBody_MatchReturnsRedactedEntry asserts that a matching
// date and heading return the text of that entry only, and that the body
// passes the redactor.
func TestDashboardLearningBody_MatchReturnsRedactedEntry(t *testing.T) {
	root := dashRoot(t)
	writeFile(t, learningsLogPath(root), "# SDLC Execution Learnings\n\n"+
		"<!-- sdlc:run=20261007T070000 branch=feat/x -->\n"+
		"## 2026-10-07 — plan: line-keyed skillcheck maps\n- detail one\n- mail owner@example.com for access\n\n"+
		"## 2026-10-06 — fix: another entry\n- other detail\n")

	got, err := DashboardLearningBody(root, "2026-10-07", "plan: line-keyed skillcheck maps")
	if err != nil {
		t.Fatalf("DashboardLearningBody error = %v, want nil", err)
	}
	if !got.Found || got.Truncated {
		t.Fatalf("result = found %v truncated %v, want found true truncated false", got.Found, got.Truncated)
	}
	if !strings.Contains(got.Body, "- detail one") {
		t.Errorf("body = %q, want the entry detail", got.Body)
	}
	if strings.Contains(got.Body, "owner@example.com") {
		t.Errorf("body = %q, want the e-mail address redacted", got.Body)
	}
	if strings.Contains(got.Body, "other detail") {
		t.Errorf("body = %q, want only the matched entry", got.Body)
	}
}

// TestDashboardLearningBody_FallbackHeadingMatches asserts that an entry with
// no "## <date> — <heading>" line is found by the fallback heading that the
// snapshot shows for it.
func TestDashboardLearningBody_FallbackHeadingMatches(t *testing.T) {
	root := dashRoot(t)
	writeFile(t, learningsLogPath(root), "# SDLC Execution Learnings\n\n"+
		"**Investigated the session cache on 2026-10-07.**\nRoot cause was a stale pointer.\n")

	got, err := DashboardLearningBody(root, "2026-10-07", "Investigated the session cache on 2026-10-07.")
	if err != nil {
		t.Fatalf("DashboardLearningBody error = %v, want nil", err)
	}
	if !got.Found || !strings.Contains(got.Body, "Root cause was a stale pointer.") {
		t.Errorf("result = %+v, want the fallback entry", got)
	}
}

// TestDashboardLearningBody_LongHeadingMatchesSnapshotHeading asserts that the
// heading the snapshot shows for a heading longer than 120 runes finds the
// entry.
func TestDashboardLearningBody_LongHeadingMatchesSnapshotHeading(t *testing.T) {
	root := dashRoot(t)
	writeFile(t, learningsLogPath(root), "# SDLC Execution Learnings\n\n"+
		"## 2026-10-07 — execute: "+strings.Repeat("y", 200)+"\n- long heading detail\n")

	rows := dashboardRecentLearnings(root, dashNow)
	if len(rows) != 1 {
		t.Fatalf("learnings = %d, want 1: %+v", len(rows), rows)
	}
	got, err := DashboardLearningBody(root, rows[0].Date, rows[0].Heading)
	if err != nil {
		t.Fatalf("DashboardLearningBody error = %v, want nil", err)
	}
	if !got.Found || !strings.Contains(got.Body, "long heading detail") {
		t.Errorf("result = %+v, want the entry of the snapshot row", got)
	}
}

// TestDashboardLearningBody_DuplicateKeyReturnsNewest asserts that two entries
// with the same date and heading give the body of the one that the log holds
// last.
func TestDashboardLearningBody_DuplicateKeyReturnsNewest(t *testing.T) {
	root := dashRoot(t)
	writeFile(t, learningsLogPath(root), "# SDLC Execution Learnings\n\n"+
		"## 2026-10-07 — plan: same title\n- first body\n\n"+
		"## 2026-10-07 — plan: same title\n- second body\n")

	got, err := DashboardLearningBody(root, "2026-10-07", "plan: same title")
	if err != nil {
		t.Fatalf("DashboardLearningBody error = %v, want nil", err)
	}
	if !got.Found || !strings.Contains(got.Body, "second body") || strings.Contains(got.Body, "first body") {
		t.Errorf("body = %q, want the newest entry", got.Body)
	}
}

// TestDashboardLearningBody_LongBodyIsCut asserts that a body of more than
// dashboardLearningBodyMax runes is cut with the truncation marker and that
// Truncated is true, while a body of exactly the limit is kept whole.
func TestDashboardLearningBody_LongBodyIsCut(t *testing.T) {
	const prefix = "## 2026-10-07 — plan: big entry\n"

	t.Run("over the limit", func(t *testing.T) {
		root := dashRoot(t)
		filler := strings.Repeat("é", dashboardLearningBodyMax+500)
		writeFile(t, learningsLogPath(root), "# SDLC Execution Learnings\n\n"+prefix+filler+"\n")

		got, err := DashboardLearningBody(root, "2026-10-07", "plan: big entry")
		if err != nil {
			t.Fatalf("DashboardLearningBody error = %v, want nil", err)
		}
		if !got.Found || !got.Truncated {
			t.Fatalf("result = found %v truncated %v, want both true", got.Found, got.Truncated)
		}
		if n := len([]rune(got.Body)); n != dashboardLearningBodyMax+1 {
			t.Errorf("body rune length = %d, want %d", n, dashboardLearningBodyMax+1)
		}
		if !strings.HasSuffix(got.Body, "…") {
			t.Errorf("body does not end with the truncation marker")
		}
	})

	t.Run("exactly at the limit", func(t *testing.T) {
		root := dashRoot(t)
		filler := strings.Repeat("x", dashboardLearningBodyMax-len([]rune(prefix)))
		writeFile(t, learningsLogPath(root), "# SDLC Execution Learnings\n\n"+prefix+filler+"\n")

		got, err := DashboardLearningBody(root, "2026-10-07", "plan: big entry")
		if err != nil {
			t.Fatalf("DashboardLearningBody error = %v, want nil", err)
		}
		if !got.Found || got.Truncated {
			t.Fatalf("result = found %v truncated %v, want found true truncated false", got.Found, got.Truncated)
		}
		if n := len([]rune(got.Body)); n != dashboardLearningBodyMax {
			t.Errorf("body rune length = %d, want %d", n, dashboardLearningBodyMax)
		}
	})
}

// TestDashboardLearningBody_NoMatchIsNotAnError asserts that a missing log, an
// unknown date, an unknown heading and an empty key each return found false
// and no error.
func TestDashboardLearningBody_NoMatchIsNotAnError(t *testing.T) {
	root := dashRoot(t)
	missing := dashRoot(t)
	writeFile(t, learningsLogPath(root), "# SDLC Execution Learnings\n\n"+
		"## 2026-10-07 — plan: only entry\n- detail\n")

	cases := []struct {
		name          string
		root          string
		date, heading string
	}{
		{"missing log", missing, "2026-10-07", "plan: only entry"},
		{"other date", root, "2026-10-06", "plan: only entry"},
		{"other heading", root, "2026-10-07", "plan: another entry"},
		{"empty date", root, "", "plan: only entry"},
		{"empty heading", root, "2026-10-07", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := DashboardLearningBody(c.root, c.date, c.heading)
			if err != nil {
				t.Fatalf("DashboardLearningBody error = %v, want nil", err)
			}
			if got != (DashboardLearningBodyOut{}) {
				t.Errorf("result = %+v, want the zero value", got)
			}
		})
	}
}

// TestDashboardLearningBody_ReadErrorIsInfraError asserts that a log that
// cannot be read returns an InfraError with a Suggestion and a Cause. A
// directory in place of the log file gives a read error that is not "not found".
func TestDashboardLearningBody_ReadErrorIsInfraError(t *testing.T) {
	root := dashRoot(t)
	if err := os.MkdirAll(learningsLogPath(root), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := DashboardLearningBody(root, "2026-10-07", "plan: only entry")
	var infra *mcpserver.InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("error = %v (%T), want *mcpserver.InfraError", err, err)
	}
	if infra.Suggestion == "" {
		t.Error("InfraError.Suggestion is empty")
	}
	if infra.Cause == nil {
		t.Error("InfraError.Cause is nil")
	}
	if got != (DashboardLearningBodyOut{}) {
		t.Errorf("result = %+v, want the zero value", got)
	}
}

// TestDashboardLearningBody_EmptyKeySkipsUnreadableLog asserts that an empty
// date or heading returns found false and no error before the log is read, so
// a log that cannot be read gives no InfraError.
func TestDashboardLearningBody_EmptyKeySkipsUnreadableLog(t *testing.T) {
	root := dashRoot(t)
	if err := os.MkdirAll(learningsLogPath(root), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, date, heading string }{
		{"empty date", "", "plan: only entry"},
		{"empty heading", "2026-10-07", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := DashboardLearningBody(root, c.date, c.heading)
			if err != nil {
				t.Fatalf("DashboardLearningBody error = %v, want nil", err)
			}
			if got != (DashboardLearningBodyOut{}) {
				t.Errorf("result = %+v, want the zero value", got)
			}
		})
	}
}

func TestDashboardActivity_Deferred(t *testing.T) {
	root := dashRoot(t)
	w := history.NewFileWriter(paths.HistoryDir(root))
	for _, issue := range []history.DeferredIssue{
		{ID: "d-low", Created: "2026-10-01T00:00:00Z", Priority: history.PriorityLow, Description: "low one", Status: history.StatusOpen},
		{ID: "d-high-2", Created: "2026-10-03T00:00:00Z", Priority: history.PriorityHigh, Description: "high two", Status: history.StatusOpen},
		{ID: "d-high-1", Created: "2026-10-02T00:00:00Z", Priority: history.PriorityHigh, Description: "high one", Status: history.StatusOpen},
		{ID: "d-medium", Created: "2026-10-01T00:00:00Z", Priority: history.PriorityMedium, Description: "medium one", Status: history.StatusOpen},
		{ID: "d-resolved", Created: "2026-10-01T00:00:00Z", Priority: history.PriorityHigh, Description: "resolved", Status: history.StatusResolved},
	} {
		if err := w.AddDeferred(issue); err != nil {
			t.Fatal(err)
		}
	}

	got := dashboardOpenDeferred(root)
	var ids []string
	for _, d := range got {
		ids = append(ids, d.ID)
	}
	want := []string{"d-high-1", "d-high-2", "d-medium", "d-low"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("deferred order = %v, want %v (high first, Created ascending within priority, resolved excluded)", ids, want)
	}
}

// TestDashboardActivity_Deferred_CarriesMetadata checks that an open deferred
// item keeps its created, source, severity, file, line and reason values, and
// that an item without the optional values serializes them as "" or 0, never
// null or a missing key.
func TestDashboardActivity_Deferred_CarriesMetadata(t *testing.T) {
	root := dashRoot(t)
	w := history.NewFileWriter(paths.HistoryDir(root))
	for _, issue := range []history.DeferredIssue{
		{
			ID: "d-full", Created: "2026-10-02T00:00:00Z", Source: "review", Priority: history.PriorityHigh,
			Description: "full item", Status: history.StatusOpen,
			Severity: "high", File: "internal/tools/x.go", Line: 42, Reason: history.ReasonNeedsDirection,
		},
		{
			ID: "d-bare", Created: "2026-10-03T00:00:00Z", Source: "ship", Priority: history.PriorityHigh,
			Description: "bare item", Status: history.StatusOpen,
		},
	} {
		if err := w.AddDeferred(issue); err != nil {
			t.Fatal(err)
		}
	}

	got := dashboardOpenDeferred(root)
	want := []DashboardDeferred{
		{
			ID: "d-full", Priority: history.PriorityHigh, Description: "full item",
			Created: "2026-10-02T00:00:00Z", Source: "review",
			Severity: "high", File: "internal/tools/x.go", Line: 42, Reason: history.ReasonNeedsDirection,
		},
		{
			ID: "d-bare", Priority: history.PriorityHigh, Description: "bare item",
			Created: "2026-10-03T00:00:00Z", Source: "ship",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dashboardOpenDeferred = %+v, want %+v", got, want)
	}

	raw, err := json.Marshal(got[1])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	wantAbsent := map[string]any{"severity": "", "file": "", "line": float64(0), "reason": ""}
	for key, zero := range wantAbsent {
		v, ok := fields[key]
		if !ok {
			t.Errorf("key %q missing from %s, want %v", key, raw, zero)
			continue
		}
		if v != zero {
			t.Errorf("key %q = %#v in %s, want %#v", key, v, raw, zero)
		}
	}
}

func TestDashboardActivity_NoEvidenceNoLearningsNoDeferred(t *testing.T) {
	root := dashRoot(t)
	sessions, learnings, deferred, history := collectActivity(root, dashNow)
	if len(sessions) != 0 || len(learnings) != 0 || len(deferred) != 0 || len(history) != 0 {
		t.Errorf("collectActivity on an empty repo = %+v %+v %+v %+v, want all empty", sessions, learnings, deferred, history)
	}
}

// dashWriteRuns writes lines (each a raw JSON object or corrupt text, no
// trailing newline) to root's runs.jsonl, oldest first.
func dashWriteRuns(t *testing.T, root string, lines ...string) {
	t.Helper()
	dashWriteJSONL(t, history.NewFileWriter(paths.HistoryDir(root)).RunsPath(), lines...)
}

// TestDashboardActivity_History_MapsFields checks how a runs.jsonl row maps to
// a DashboardRun, including the start time fallbacks.
func TestDashboardActivity_History_MapsFields(t *testing.T) {
	root := dashRoot(t)
	dashWriteRuns(t, root,
		// started_at present: it wins over ts - duration_ms.
		`{"ts":"2026-10-07T09:30:00Z","skill":"ship","branch":"feat/a","outcome":"success","duration_ms":60000,"started_at":"2026-10-07T09:00:00Z"}`,
		// no started_at: ts - duration_ms.
		`{"ts":"2026-10-07T10:00:00Z","skill":"execute","branch":"feat/b","outcome":"failure","duration_ms":90000}`,
		// no started_at and no duration: startedAt is empty.
		`{"ts":"2026-10-07T11:00:00Z","skill":"plan","branch":"feat/c","outcome":"partial","duration_ms":0}`,
		// no started_at, duration set, ts unparsable: startedAt is empty and ts passes through.
		`{"ts":"not-a-time","skill":"review","branch":"feat/d","outcome":"success","duration_ms":5000}`,
	)

	got := dashboardRecentRuns(root)
	want := []DashboardRun{
		{Kind: "review", Branch: "feat/d", Outcome: "success", StartedAt: "", EndedAt: "not-a-time", DurationMs: 5000, TotalMs: 5000},
		{Kind: "plan", Branch: "feat/c", Outcome: "partial", StartedAt: "", EndedAt: "2026-10-07T11:00:00Z", DurationMs: 0, TotalMs: 0},
		{Kind: "execute", Branch: "feat/b", Outcome: "failure", StartedAt: "2026-10-07T09:58:30Z", EndedAt: "2026-10-07T10:00:00Z", DurationMs: 90000, TotalMs: 90000},
		{Kind: "ship", Branch: "feat/a", Outcome: "success", StartedAt: "2026-10-07T09:00:00Z", EndedAt: "2026-10-07T09:30:00Z", DurationMs: 60000, TotalMs: 60000},
	}
	if len(got) != len(want) {
		t.Fatalf("history rows = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("history[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestDashboardActivity_History_PlanAndTotalTimes checks the plan fields and
// totalMs of one history row for each row kind: ship with and without plan
// fields, plan, execute, and the time edge cases.
func TestDashboardActivity_History_PlanAndTotalTimes(t *testing.T) {
	cases := []struct {
		name string
		line string
		want DashboardRun
	}{
		{
			name: "ship with plan fields counts total from the plan start to ts",
			line: `{"ts":"2026-10-10T09:40:00Z","skill":"ship","branch":"feat/a","outcome":"success","duration_ms":3600000,"plan_started_at":"2026-10-10T08:20:00Z","plan_duration_ms":4800000}`,
			want: DashboardRun{
				Kind: "ship", Branch: "feat/a", Outcome: "success",
				StartedAt: "2026-10-10T08:40:00Z", EndedAt: "2026-10-10T09:40:00Z", DurationMs: 3600000,
				PlanStartedAt: "2026-10-10T08:20:00Z", PlanDurationMs: 4800000, TotalMs: 4800000,
			},
		},
		{
			name: "ship without plan fields has total equal to duration",
			line: `{"ts":"2026-10-10T09:40:00Z","skill":"ship","branch":"feat/b","outcome":"success","duration_ms":3600000}`,
			want: DashboardRun{
				Kind: "ship", Branch: "feat/b", Outcome: "success",
				StartedAt: "2026-10-10T08:40:00Z", EndedAt: "2026-10-10T09:40:00Z", DurationMs: 3600000, TotalMs: 3600000,
			},
		},
		{
			name: "plan row ignores plan fields",
			line: `{"ts":"2026-10-10T09:40:00Z","skill":"plan","branch":"feat/c","outcome":"success","duration_ms":120000,"plan_started_at":"2026-10-10T08:20:00Z","plan_duration_ms":4800000}`,
			want: DashboardRun{
				Kind: "plan", Branch: "feat/c", Outcome: "success",
				StartedAt: "2026-10-10T09:38:00Z", EndedAt: "2026-10-10T09:40:00Z", DurationMs: 120000, TotalMs: 120000,
			},
		},
		{
			name: "execute row ignores plan fields",
			line: `{"ts":"2026-10-10T09:40:00Z","skill":"execute","branch":"feat/d","outcome":"success","duration_ms":900000,"plan_started_at":"2026-10-10T08:20:00Z","plan_duration_ms":4800000}`,
			want: DashboardRun{
				Kind: "execute", Branch: "feat/d", Outcome: "success",
				StartedAt: "2026-10-10T09:25:00Z", EndedAt: "2026-10-10T09:40:00Z", DurationMs: 900000, TotalMs: 900000,
			},
		},
		{
			name: "plan start after ts copies no plan field",
			line: `{"ts":"2026-10-10T09:40:00Z","skill":"ship","branch":"feat/e","outcome":"success","duration_ms":3600000,"plan_started_at":"2026-10-10T10:00:00Z","plan_duration_ms":4800000}`,
			want: DashboardRun{
				Kind: "ship", Branch: "feat/e", Outcome: "success",
				StartedAt: "2026-10-10T08:40:00Z", EndedAt: "2026-10-10T09:40:00Z", DurationMs: 3600000, TotalMs: 3600000,
			},
		},
		{
			name: "plan start that does not parse copies no plan field",
			line: `{"ts":"2026-10-10T09:40:00Z","skill":"ship","branch":"feat/f","outcome":"success","duration_ms":3600000,"plan_started_at":"not-a-time","plan_duration_ms":4800000}`,
			want: DashboardRun{
				Kind: "ship", Branch: "feat/f", Outcome: "success",
				StartedAt: "2026-10-10T08:40:00Z", EndedAt: "2026-10-10T09:40:00Z", DurationMs: 3600000, TotalMs: 3600000,
			},
		},
		{
			name: "ts that does not parse copies no plan field",
			line: `{"ts":"not-a-time","skill":"ship","branch":"feat/g","outcome":"success","duration_ms":3600000,"plan_started_at":"2026-10-10T08:20:00Z","plan_duration_ms":4800000}`,
			want: DashboardRun{
				Kind: "ship", Branch: "feat/g", Outcome: "success",
				EndedAt: "not-a-time", DurationMs: 3600000, TotalMs: 3600000,
			},
		},
		{
			name: "no duration and no plan start gives total 0",
			line: `{"ts":"2026-10-10T09:40:00Z","skill":"ship","branch":"feat/h","outcome":"failure","duration_ms":0}`,
			want: DashboardRun{
				Kind: "ship", Branch: "feat/h", Outcome: "failure",
				EndedAt: "2026-10-10T09:40:00Z",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := dashRoot(t)
			dashWriteRuns(t, root, tc.line)
			got := dashboardRecentRuns(root)
			if len(got) != 1 {
				t.Fatalf("history rows = %d, want 1: %+v", len(got), got)
			}
			if got[0] != tc.want {
				t.Errorf("history row = %+v, want %+v", got[0], tc.want)
			}
		})
	}
}

// TestDashboardActivity_History_PlanFieldsEncode checks the JSON of a ship row
// with plan fields and of a row without them.
func TestDashboardActivity_History_PlanFieldsEncode(t *testing.T) {
	root := dashRoot(t)
	dashWriteRuns(t, root,
		`{"ts":"2026-10-10T09:40:00Z","skill":"ship","branch":"feat/a","outcome":"success","duration_ms":3600000,"plan_started_at":"2026-10-10T08:20:00Z","plan_duration_ms":4800000}`,
		`{"ts":"2026-10-10T10:00:00Z","skill":"plan","branch":"feat/b","outcome":"done","duration_ms":60000}`,
	)
	got := dashboardRecentRuns(root)
	if len(got) != 2 {
		t.Fatalf("history rows = %d, want 2: %+v", len(got), got)
	}

	decode := func(run DashboardRun) map[string]any {
		raw, err := json.Marshal(run)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		return fields
	}

	plan := decode(got[0])
	for _, key := range []string{"planStartedAt", "planDurationMs"} {
		if _, ok := plan[key]; ok {
			t.Errorf("plan row has key %q, want it absent: %v", key, plan)
		}
	}
	if plan["totalMs"] != float64(60000) {
		t.Errorf("plan row totalMs = %v, want 60000", plan["totalMs"])
	}

	ship := decode(got[1])
	if ship["planStartedAt"] != "2026-10-10T08:20:00Z" || ship["planDurationMs"] != float64(4800000) || ship["totalMs"] != float64(4800000) {
		t.Errorf("ship row = %v, want planStartedAt 2026-10-10T08:20:00Z, planDurationMs 4800000, totalMs 4800000", ship)
	}
}

// TestDashboardActivity_History_CapsAtLimitNewestFirst checks that the list
// keeps only dashboardHistoryLimit rows and puts the newest row first.
func TestDashboardActivity_History_CapsAtLimitNewestFirst(t *testing.T) {
	root := dashRoot(t)
	total := dashboardHistoryLimit + 7
	lines := make([]string, 0, total)
	for i := 0; i < total; i++ {
		lines = append(lines, fmt.Sprintf(`{"ts":"2026-10-07T09:%02d:00Z","skill":"ship","branch":"run-%03d","outcome":"success","duration_ms":1000}`, i%60, i))
	}
	dashWriteRuns(t, root, lines...)

	got := dashboardRecentRuns(root)
	if len(got) != dashboardHistoryLimit {
		t.Fatalf("history rows = %d, want %d", len(got), dashboardHistoryLimit)
	}
	// Newest first: the last line written is first; the oldest kept row is
	// line index total-limit (the 7 oldest lines are dropped).
	if got[0].Branch != fmt.Sprintf("run-%03d", total-1) {
		t.Errorf("first row branch = %s, want run-%03d", got[0].Branch, total-1)
	}
	if last := got[len(got)-1]; last.Branch != fmt.Sprintf("run-%03d", total-dashboardHistoryLimit) {
		t.Errorf("last row branch = %s, want run-%03d", last.Branch, total-dashboardHistoryLimit)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Branch <= got[i].Branch {
			t.Fatalf("rows %d and %d are not newest first: %s then %s", i-1, i, got[i-1].Branch, got[i].Branch)
		}
	}
}

// TestDashboardActivity_History_FailureAndLaterSuccessBothShow checks that a
// failed run stays in the list after a later run of the same branch succeeds.
func TestDashboardActivity_History_FailureAndLaterSuccessBothShow(t *testing.T) {
	root := dashRoot(t)
	dashWriteRuns(t, root,
		`{"ts":"2026-10-07T09:00:00Z","skill":"ship","branch":"feat/x","outcome":"failure","duration_ms":1000}`,
		`{"ts":"2026-10-07T10:00:00Z","skill":"ship","branch":"feat/x","outcome":"success","duration_ms":2000}`,
	)

	got := dashboardRecentRuns(root)
	if len(got) != 2 || got[0].Outcome != "success" || got[1].Outcome != "failure" {
		t.Errorf("history = %+v, want the later success first and the earlier failure second", got)
	}
}

// TestDashboardActivity_History_SkipsCorruptLine checks that a corrupt line is
// skipped and does not count toward dashboardHistoryLimit.
func TestDashboardActivity_History_SkipsCorruptLine(t *testing.T) {
	root := dashRoot(t)
	// The file holds exactly dashboardHistoryLimit valid rows plus one corrupt
	// line among the newest ones. The limit counts valid rows only, so every
	// valid row shows, including the oldest.
	lines := []string{`{"ts":"2026-10-07T08:00:00Z","skill":"ship","branch":"old","outcome":"success","duration_ms":1}`}
	for i := 0; i < dashboardHistoryLimit-2; i++ {
		lines = append(lines, fmt.Sprintf(`{"ts":"2026-10-07T09:00:00Z","skill":"ship","branch":"mid-%02d","outcome":"success","duration_ms":1}`, i))
	}
	lines = append(lines, `{"ts":"2026-10-07T09:30:00Z","skill":`, `{"ts":"2026-10-07T10:00:00Z","skill":"ship","branch":"newest","outcome":"success","duration_ms":1}`)
	dashWriteRuns(t, root, lines...)

	got := dashboardRecentRuns(root)
	if len(got) != dashboardHistoryLimit {
		t.Fatalf("history rows = %d, want %d (corrupt line skipped, not counted)", len(got), dashboardHistoryLimit)
	}
	if got[0].Branch != "newest" {
		t.Errorf("first row branch = %s, want newest", got[0].Branch)
	}
	if last := got[len(got)-1]; last.Branch != "old" {
		t.Errorf("last row branch = %s, want old (the oldest valid row inside the limit)", last.Branch)
	}
}

// TestDashboardActivity_History_MissingOrEmptyFile checks that a missing or
// empty runs.jsonl gives an empty list that encodes as [] in the snapshot.
func TestDashboardActivity_History_MissingOrEmptyFile(t *testing.T) {
	t.Run("missing runs.jsonl", func(t *testing.T) {
		root := dashRoot(t)
		if got := dashboardRecentRuns(root); len(got) != 0 {
			t.Errorf("history = %+v, want none", got)
		}
		repo := dashCollect(t, root)
		if repo.History == nil || len(repo.History) != 0 {
			t.Errorf("repo.History = %#v, want an empty non-nil list", repo.History)
		}
	})

	t.Run("empty runs.jsonl", func(t *testing.T) {
		root := dashRoot(t)
		path := history.NewFileWriter(paths.HistoryDir(root)).RunsPath()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if got := dashboardRecentRuns(root); len(got) != 0 {
			t.Errorf("history = %+v, want none", got)
		}
		b, err := json.Marshal(dashCollect(t, root))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"history":[]`) {
			t.Errorf("repo JSON = %s, want history []", b)
		}
	})
}

// TestDashboardActivity_History_ReachesSnapshot checks that the collected repo
// carries the history rows, newest first.
func TestDashboardActivity_History_ReachesSnapshot(t *testing.T) {
	root := dashRoot(t)
	dashWriteRuns(t, root,
		`{"ts":"2026-10-07T09:00:00Z","skill":"ship","branch":"feat/x","outcome":"failure","duration_ms":1000}`,
		`{"ts":"2026-10-07T10:00:00Z","skill":"execute","branch":"feat/y","outcome":"success","duration_ms":2000}`,
	)

	repo := dashCollect(t, root)
	if len(repo.History) != 2 || repo.History[0].Kind != "execute" || repo.History[1].Kind != "ship" {
		t.Errorf("repo.History = %+v, want execute then ship", repo.History)
	}
}
