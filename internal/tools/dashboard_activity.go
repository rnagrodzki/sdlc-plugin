package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/telemetry"
)

// dashboardPreviewTextMax bounds a timeline event's text preview to this
// many runes (D3): the dashboard shows a short, redacted preview rather than
// a session's full stored prompt, command, or tool text.
const dashboardPreviewTextMax = 120

// dashboardSessionTimelineMax caps a session's timeline to its newest
// entries.
const dashboardSessionTimelineMax = 50

// dashboardSessionActiveWithin is how recently a session's newest evidence
// line must land for the session to show as active (D9). Same duration as
// dashboardStallAfter (dashboard_snapshot.go), but a session's "active" and
// a pipeline's "stalled" are different concepts, so this gets its own name.
const dashboardSessionActiveWithin = 30 * time.Minute

// dashboardLearningHeadingRe matches a "## <date> — <heading>" entry
// heading line, capturing everything after the em dash. Mirrors
// learningsSkillHeadingRe (learnings.go), which captures only the skill
// segment of the same line; the dashboard wants the full "<skill>: <title>"
// text instead.
var dashboardLearningHeadingRe = regexp.MustCompile(`(?m)^##\s+\S+\s+—\s*(.+)$`)

// dashboardLearningRunTagRe matches the "<!-- sdlc:run=X branch=Y -->"
// comment learningsAppend (learnings.go) prepends to a tagged entry,
// capturing both the run ID and the branch. Mirrors learningsRunTagRe
// (learnings.go), which captures only the branch.
var dashboardLearningRunTagRe = regexp.MustCompile(`(?m)^<!--[ \t]*sdlc:run=(\S+)[ \t]+branch=(\S*)[ \t]*-->`)

// dashboardHistoryLimit is the number of runs.jsonl rows the History tab shows.
const dashboardHistoryLimit = 50

// collectActivity returns the sessions, learnings, deferred items, and run
// history of the repo at root for the dashboard snapshot (see
// dashboard_snapshot.go for the full contract and the result types).
func collectActivity(root string, now time.Time) (sessions []DashboardSession, learnings []DashboardLearning, deferred []DashboardDeferred, history []DashboardRun) {
	return dashboardSessionsFromEvidence(root, now), dashboardRecentLearnings(root, now), dashboardOpenDeferred(root), dashboardRecentRuns(root)
}

// dashboardEvent is one evidence line reduced to what a session timeline
// needs.
type dashboardEvent struct {
	sessionID string
	at        time.Time
	kind      string // "prompt", "command", or "mcp"
	text      string
	branch    string // "" when the source entry carries none (e.g. MCP evidence)
	command   string // full raw command, kind "command" only. Never serialized
}

// dashboardSessionAgg accumulates the evidence lines of one session before
// its DashboardSession is built.
type dashboardSessionAgg struct {
	events []dashboardEvent
}

// dashboardSessionsFromEvidence groups the CLI, user-input, and MCP evidence
// lines of root by sessionId and returns one DashboardSession per non-empty
// id, newest session (by lastSeen) first.
func dashboardSessionsFromEvidence(root string, now time.Time) []DashboardSession {
	var events []dashboardEvent
	events = append(events, dashboardCLIEvents(root)...)
	events = append(events, dashboardPromptEvents(root)...)
	events = append(events, dashboardMCPEvents(root)...)

	var order []string
	byID := map[string]*dashboardSessionAgg{}
	for _, e := range events {
		if e.sessionID == "" {
			continue
		}
		a, ok := byID[e.sessionID]
		if !ok {
			a = &dashboardSessionAgg{}
			byID[e.sessionID] = a
			order = append(order, e.sessionID)
		}
		a.events = append(a.events, e)
	}

	sessions := make([]DashboardSession, 0, len(order))
	for _, id := range order {
		a := byID[id]
		sort.SliceStable(a.events, func(i, j int) bool { return a.events[i].at.Before(a.events[j].at) })

		s := DashboardSession{ID: id, Timeline: []DashboardEvent{}}
		for _, e := range a.events {
			if e.branch != "" {
				s.Branch = e.branch // latest non-empty branch wins (events are sorted ascending)
			}
			switch e.kind {
			case "prompt":
				s.Counts.Prompts++
			case "command":
				s.Counts.Commands++
			case "mcp":
				s.Counts.MCPCalls++
			}
		}

		first, last := a.events[0].at, a.events[len(a.events)-1].at
		s.FirstSeen = dashboardFormatTime(first)
		s.LastSeen = dashboardFormatTime(last)
		s.Active = now.Sub(last) < dashboardSessionActiveWithin

		// Group every command of the session, before the timeline cap drops
		// the oldest events.
		s.CommandGroups = groupSessionCommands(a.events)

		for i := len(a.events) - 1; i >= 0 && len(s.Timeline) < dashboardSessionTimelineMax; i-- {
			e := a.events[i]
			s.Timeline = append(s.Timeline, DashboardEvent{At: dashboardFormatTime(e.at), Kind: e.kind, Text: e.text})
		}
		sessions = append(sessions, s)
	}

	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].LastSeen > sessions[j].LastSeen })
	return sessions
}

// dashboardCLIEvents reads root's CLI evidence (current and rotated ".1"
// file) and reduces each entry with a sessionId and a parsable timestamp to
// a "command" dashboardEvent.
func dashboardCLIEvents(root string) []dashboardEvent {
	var out []dashboardEvent
	for _, f := range []string{cliEvidencePath(root), cliEvidencePath(root) + ".1"} {
		entries, _ := readJSONLEntries[CLIEvidenceEntry](f, "dashboard cli evidence")
		for _, e := range entries {
			t, ok := dashboardParseTime(e.Timestamp)
			if !ok || e.SessionID == "" {
				continue
			}
			out = append(out, dashboardEvent{
				sessionID: e.SessionID,
				at:        t,
				kind:      "command",
				text:      dashboardPreview(e.Command),
				branch:    e.Branch,
				command:   e.Command,
			})
		}
	}
	return out
}

// dashboardPromptEvents reads root's user-input evidence (current and
// rotated ".1" file) and reduces each entry with a sessionId and a parsable
// timestamp to a "prompt" dashboardEvent. Both UserInputKindPrompt and
// UserInputKindAnswer entries map to "prompt": the timeline's kind enum has
// no separate "answer" value.
func dashboardPromptEvents(root string) []dashboardEvent {
	var out []dashboardEvent
	for _, f := range []string{userInputPath(root), userInputPath(root) + ".1"} {
		entries, _ := readJSONLEntries[UserInputEntry](f, "dashboard user input evidence")
		for _, e := range entries {
			t, ok := dashboardParseTime(e.Timestamp)
			if !ok || e.SessionID == "" {
				continue
			}
			out = append(out, dashboardEvent{
				sessionID: e.SessionID,
				at:        t,
				kind:      "prompt",
				text:      dashboardPreview(e.Text),
				branch:    e.Branch,
			})
		}
	}
	return out
}

// dashboardMCPEvents reads root's MCP invocation evidence (current and
// rotated ".1" file) and reduces each entry with a sessionId and a parsable
// timestamp to an "mcp" dashboardEvent. MCP evidence carries no branch.
func dashboardMCPEvents(root string) []dashboardEvent {
	var out []dashboardEvent
	for _, f := range []string{mcpEvidencePath(root), mcpEvidencePath(root) + ".1"} {
		entries, _ := readJSONLEntries[MCPEvidenceEntry](f, "dashboard mcp evidence")
		for _, e := range entries {
			t, ok := dashboardParseTime(e.Timestamp)
			if !ok || e.SessionID == "" {
				continue
			}
			text := e.Tool
			if e.SkillContext != "" {
				text += " [" + e.SkillContext + "]"
			}
			out = append(out, dashboardEvent{
				sessionID: e.SessionID,
				at:        t,
				kind:      "mcp",
				text:      dashboardPreview(text),
			})
		}
	}
	return out
}

// dashboardPreview redacts (telemetry.Redact) and truncates (telemetry.TruncateRunes)
// s to dashboardPreviewTextMax runes, for the text of one timeline event.
// It applies the same pass to every event kind. User-input text is already
// redacted and truncated to 2000 runes when it is written (appendUserInput,
// user_input_evidence.go), and it is redacted again here. CLI command and
// MCP tool text are stored unredacted, so this pass is the only redaction
// they get before they reach the dashboard.
func dashboardPreview(s string) string {
	return telemetry.TruncateRunes(telemetry.Redact(s), dashboardPreviewTextMax)
}

// dashboardRecentLearnings reads root's learnings log and returns the
// entries dated within dashboardHistoryWindow (24 h) of now, newest first.
// An entry with no parsable ISO date anywhere in its text is skipped: there
// is no way to confirm it falls in the window.
//
// The entry date is day-granularity only (no time of day), so "within the
// window" is a date-string comparison against the inclusive range
// [today - window, today] rather than a duration check against a
// midnight-parsed timestamp — the latter would drop an entry from
// yesterday evening even though it is well inside the last 24h.
func dashboardRecentLearnings(root string, now time.Time) []DashboardLearning {
	data, err := os.ReadFile(learningsLogPath(root))
	if err != nil {
		return nil
	}
	_, entries := learningsSplitEntries(string(data))

	today := now.UTC().Format("2006-01-02")
	oldest := now.Add(-dashboardHistoryWindow).UTC().Format("2006-01-02")

	var out []DashboardLearning
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)

		dateStr := learningsDateRe.FindString(entry)
		if dateStr == "" || dateStr < oldest || dateStr > today {
			continue
		}

		var runID, branch string
		if m := dashboardLearningRunTagRe.FindStringSubmatch(entry); m != nil {
			runID, branch = m[1], m[2]
		}

		out = append(out, DashboardLearning{
			Date:    dateStr,
			Heading: dashboardLearningHeading(entry),
			RunID:   runID,
			Branch:  branch,
		})
	}

	// Log entries are appended oldest-last, so out is oldest-first here;
	// reverse it to match the timeline's newest-first convention.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// dashboardLearningHeading returns entry's "## <date> — <heading>" text when
// it has that line, else a short fallback built from entry's first line (an
// older entry logged before the heading convention, such as a bold-titled
// tooling-lesson block).
func dashboardLearningHeading(entry string) string {
	if m := dashboardLearningHeadingRe.FindStringSubmatch(entry); m != nil {
		return telemetry.TruncateRunes(strings.TrimSpace(m[1]), dashboardPreviewTextMax)
	}
	first := entry
	if i := strings.IndexByte(entry, '\n'); i >= 0 {
		first = entry[:i]
	}
	return telemetry.TruncateRunes(strings.Trim(strings.TrimSpace(first), "*"), dashboardPreviewTextMax)
}

// dashboardLearningBodyMax bounds the body that DashboardLearningBody returns
// to this many runes. A longer body is cut and DashboardLearningBodyOut.Truncated
// is true. The dashboard shows a learning body in a viewer, so it needs enough
// text to read an entry but not a whole oversized log block.
const dashboardLearningBodyMax = 8000

// DashboardLearningBodyOut is the result of DashboardLearningBody.
type DashboardLearningBodyOut struct {
	// Found is true when the log holds an entry with the requested date and
	// heading.
	Found bool `json:"found"`
	// Body is the redacted text of the entry. It is empty when Found is false.
	// When Truncated is true, it ends with the "…" marker of
	// telemetry.TruncateRunes.
	Body string `json:"body"`
	// Truncated is true when the entry held more than dashboardLearningBodyMax
	// runes and Body is the cut text.
	Truncated bool `json:"truncated"`
}

// DashboardLearningBody returns the text of the learning in root's log whose
// date is date and whose heading is heading. Both values are the ones that
// dashboardRecentLearnings puts in a DashboardLearning row, so the entry is
// found with the same date and heading parse. The pair is the key: a learning
// has no id. When two entries share the pair, the newest one (the last in the
// log) wins, because the snapshot lists the newest entry first.
//
// The body passes telemetry.Redact before it is cut, so a secret is never cut
// in half and left unredacted. A body of more than dashboardLearningBodyMax
// runes is cut to that length and Truncated is true.
//
// A missing log, an empty date or heading, and a pair that matches no entry
// return Found false and no error. A failure to read the log returns an
// InfraError.
func DashboardLearningBody(root, date, heading string) (DashboardLearningBodyOut, error) {
	if date == "" || heading == "" {
		return DashboardLearningBodyOut{}, nil
	}
	data, err := os.ReadFile(learningsLogPath(root))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return DashboardLearningBodyOut{}, nil
		}
		return DashboardLearningBodyOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read learnings log: %s", err),
			Suggestion: "Check that the learnings log of the repo is a regular file that you can read, then open the learning again.",
			Cause:      err,
		}
	}

	_, entries := learningsSplitEntries(string(data))
	for i := len(entries) - 1; i >= 0; i-- {
		entry := strings.TrimSpace(entries[i])
		if learningsDateRe.FindString(entry) != date || dashboardLearningHeading(entry) != heading {
			continue
		}
		body := telemetry.Redact(entry)
		return DashboardLearningBodyOut{
			Found:     true,
			Body:      telemetry.TruncateRunes(body, dashboardLearningBodyMax),
			Truncated: utf8.RuneCountInString(body) > dashboardLearningBodyMax,
		}, nil
	}
	return DashboardLearningBodyOut{}, nil
}

// dashboardOpenDeferred returns root's open deferred issues as
// DashboardDeferred rows, high priority first — same grouping and
// within-group order (Created ascending) as FormatDeferredSummary
// (internal/history/history.go). Uses DeferredByPriority rather than
// OpenDeferred directly: DeferredByPriority applies the identical
// StatusOpen filter internally and also does the priority grouping this
// function needs, so a separate OpenDeferred call first would be redundant.
func dashboardOpenDeferred(root string) []DashboardDeferred {
	issues, _ := history.NewFileWriter(paths.HistoryDir(root)).ListDeferred()
	groups := history.DeferredByPriority(issues)

	var out []DashboardDeferred
	for _, prio := range []string{history.PriorityHigh, history.PriorityMedium, history.PriorityLow} {
		items := groups[prio]
		sort.Slice(items, func(i, j int) bool { return items[i].Created < items[j].Created })
		for _, it := range items {
			out = append(out, DashboardDeferred{
				ID:          it.ID,
				Priority:    it.Priority,
				Description: it.Description,
				Created:     it.Created,
				Source:      it.Source,
				Severity:    it.Severity,
				File:        it.File,
				Line:        it.Line,
				Reason:      it.Reason,
			})
		}
	}
	return out
}

// dashboardRecentRuns returns the newest dashboardHistoryLimit rows of root's
// runs.jsonl as DashboardRun rows, newest first. runs.jsonl is appended
// oldest-last, so the rows are read in file order and reversed. A corrupt
// line is skipped and does not count toward the limit. A missing or empty
// file gives nil.
func dashboardRecentRuns(root string) []DashboardRun {
	path := history.NewFileWriter(paths.HistoryDir(root)).RunsPath()
	records, _ := readJSONLEntries[history.RunRecord](path, "dashboard run history")
	if len(records) > dashboardHistoryLimit {
		records = records[len(records)-dashboardHistoryLimit:]
	}

	var out []DashboardRun
	for i := len(records) - 1; i >= 0; i-- {
		out = append(out, dashboardRunFromRecord(records[i]))
	}
	return out
}

// dashboardRunFromRecord maps one runs.jsonl row to a DashboardRun. EndedAt
// is the row's ts. StartedAt is the row's started_at when present, else ts
// minus duration_ms when ts parses and duration_ms is positive, else "".
func dashboardRunFromRecord(r history.RunRecord) DashboardRun {
	started := r.StartedAt
	if started == "" && r.DurationMs > 0 {
		if end, ok := dashboardParseTime(r.Timestamp); ok {
			started = dashboardFormatTime(end.Add(-time.Duration(r.DurationMs) * time.Millisecond))
		}
	}
	return DashboardRun{
		Kind:       r.Skill,
		Branch:     r.Branch,
		Outcome:    r.Outcome,
		StartedAt:  started,
		EndedAt:    r.Timestamp,
		DurationMs: r.DurationMs,
	}
}
