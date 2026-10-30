package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
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

// dashboardActivityOut is the result of collectActivity.
type dashboardActivityOut struct {
	sessions  []DashboardSession
	learnings []DashboardLearning
	deferred  []DashboardDeferred
	history   []DashboardRun
	// sessionsErr is the first read error of the session evidence files. The
	// sessions then hold the lines that could be read. A detail route reads it,
	// so that a session it cannot read is not reported as gone.
	sessionsErr error
	// warnings holds one text for each source that could not be read. The text
	// names the source and the file.
	warnings []string
}

// collectActivity returns the sessions, learnings, deferred items, and run
// history of the repo at root for the dashboard snapshot (see
// dashboard_snapshot.go for the full contract and the result types). A source
// that cannot be read adds one warning and gives the rows that could be read,
// or none.
func collectActivity(root string, now time.Time) dashboardActivityOut {
	var out dashboardActivityOut
	out.sessions, out.sessionsErr = dashboardSessionsFromEvidence(root, now)
	if out.sessionsErr != nil {
		out.warnings = append(out.warnings, "Session evidence unread: "+out.sessionsErr.Error())
	}
	var err error
	if out.learnings, err = dashboardRecentLearnings(root, now); err != nil {
		out.warnings = append(out.warnings, "Learnings log unread: "+err.Error())
	}
	if out.deferred, err = dashboardOpenDeferred(root); err != nil {
		out.warnings = append(out.warnings, dashboardDeferredWarning(root, err))
	}
	if out.history, err = dashboardRecentRuns(root); err != nil {
		out.warnings = append(out.warnings, "Run history unread: "+err.Error())
	}
	return out
}

// dashboardDeferredWarning returns the repo warning for err, the read error of
// the deferred.json file of root. A file over the size limit gets its own text
// with the fix, because the read error does not name the file path.
func dashboardDeferredWarning(root string, err error) string {
	if errors.Is(err, history.ErrDeferredTooLarge) {
		path := history.NewFileWriter(paths.HistoryDir(root)).DeferredPath()
		return fmt.Sprintf("Deferred items unread: %s is larger than %s. Remove resolved items from the file by hand.", path, dashboardDeleteReadMaxText)
	}
	return "Deferred items unread: " + err.Error()
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

// dashboardSessionsFromEvidence returns one DashboardSession per non-empty
// sessionId in the CLI, user-input, and MCP evidence lines of root, newest
// session (by lastSeen) first. The sessions come from the parse cache, which
// is built again when one of the evidence files changes. Active is set for
// each call from now, on a copy of the cached slice.
//
// A session whose evidence cannot be read in full is built from the lines
// that could be read, and the cache does not keep it. The error is then the
// first read error of an evidence file.
func dashboardSessionsFromEvidence(root string, now time.Time) ([]DashboardSession, error) {
	cached, err := dashboardCached(dashboardCacheKey(root, "sessions"), dashboardSessionFiles(root), func() ([]DashboardSession, error) {
		return dashboardBuildSessions(root)
	})
	sessions := slices.Clone(cached)
	for i := range sessions {
		if last, ok := dashboardParseTime(sessions[i].LastSeen); ok {
			sessions[i].Active = now.Sub(last) < dashboardSessionActiveWithin
		}
	}
	return sessions, err
}

// dashboardSessionReaders is the evidence sources of dashboardBuildSessions:
// for each, the path of its current file and the reader of its files.
// dashboardSessionFiles builds the cache signature from the same list, so a
// source that is added here is also in the signature.
var dashboardSessionReaders = []struct {
	base func(root string) string
	read func(files []string) ([]dashboardEvent, error)
}{
	{cliEvidencePath, dashboardCLIEvents},
	{userInputPath, dashboardPromptEvents},
	{mcpEvidencePath, dashboardMCPEvents},
}

// dashboardSessionFiles returns the evidence files that dashboardBuildSessions
// reads: the files of each source of dashboardSessionReaders.
func dashboardSessionFiles(root string) []string {
	var files []string
	for _, r := range dashboardSessionReaders {
		files = append(files, dashboardRotatedFiles(r.base(root))...)
	}
	return files
}

// dashboardRotatedFiles returns base and its rotated ".1" file, the files of
// one evidence log.
func dashboardRotatedFiles(base string) []string {
	return []string{base, base + ".1"}
}

// dashboardReadJSONLFiles reads each of files with readJSONLEntries and
// returns their entries in file order. The error is the first read error of a
// file; the entries then hold the lines of the other files.
func dashboardReadJSONLFiles[T any](files []string, label string) ([]T, error) {
	var out []T
	var firstErr error
	for _, f := range files {
		entries, err := readJSONLEntries[T](f, label)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		out = append(out, entries...)
	}
	return out, firstErr
}

// dashboardBuildSessions groups the CLI, user-input, and MCP evidence lines
// of root by sessionId and returns one DashboardSession per non-empty id,
// newest session (by lastSeen) first. Active is not set: it depends on the
// time of each call. The error is the first read error of an evidence file;
// the sessions then hold the lines that could be read.
func dashboardBuildSessions(root string) ([]DashboardSession, error) {
	var events []dashboardEvent
	var firstErr error
	for _, r := range dashboardSessionReaders {
		got, err := r.read(dashboardRotatedFiles(r.base(root)))
		if err != nil && firstErr == nil {
			firstErr = err
		}
		events = append(events, got...)
	}

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
	return sessions, firstErr
}

// dashboardCLIEvents reads the CLI evidence files and reduces each entry with
// a sessionId and a parsable timestamp to a "command" dashboardEvent. The
// error is the first read error of a file; the events then hold the lines of
// the other files.
func dashboardCLIEvents(files []string) ([]dashboardEvent, error) {
	entries, err := dashboardReadJSONLFiles[CLIEvidenceEntry](files, "dashboard cli evidence")
	var out []dashboardEvent
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
	return out, err
}

// dashboardPromptEvents reads the user-input evidence files and reduces each
// entry with a sessionId and a parsable timestamp to a "prompt"
// dashboardEvent. Both UserInputKindPrompt and UserInputKindAnswer entries
// map to "prompt": the timeline's kind enum has no separate "answer" value.
// The error is the first read error of a file; the events then hold the lines
// of the other files.
func dashboardPromptEvents(files []string) ([]dashboardEvent, error) {
	entries, err := dashboardReadJSONLFiles[UserInputEntry](files, "dashboard user input evidence")
	var out []dashboardEvent
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
	return out, err
}

// dashboardMCPEvents reads the MCP invocation evidence files and reduces each
// entry with a sessionId and a parsable timestamp to an "mcp"
// dashboardEvent. MCP evidence carries no branch. The error is the first read
// error of a file; the events then hold the lines of the other files.
func dashboardMCPEvents(files []string) ([]dashboardEvent, error) {
	entries, err := dashboardReadJSONLFiles[MCPEvidenceEntry](files, "dashboard mcp evidence")
	var out []dashboardEvent
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
	return out, err
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
// The split entries of the log come from the parse cache, which is built
// again when the log changes. The window rule uses the now of each call. A
// missing log gives nil and no error. A log that cannot be read gives nil and
// the read error.
//
// The entry date is day-granularity only (no time of day), so "within the
// window" is a date-string comparison against the inclusive range
// [today - window, today] rather than a duration check against a
// midnight-parsed timestamp — the latter would drop an entry from
// yesterday evening even though it is well inside the last 24h.
func dashboardRecentLearnings(root string, now time.Time) ([]DashboardLearning, error) {
	logPath := learningsLogPath(root)
	entries, err := dashboardCached(dashboardCacheKey(root, "learnings"), []string{logPath}, func() ([]string, error) {
		return dashboardLearningEntries(logPath)
	})
	if err != nil {
		return nil, err
	}

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
	return out, nil
}

// dashboardLearningEntries reads the learnings log at path and returns its
// entries, split as learningsSplitEntries splits them. A log that does not
// exist gives no entries and no error.
func dashboardLearningEntries(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	_, entries := learningsSplitEntries(string(data))
	return entries, nil
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
	at := dashboardFindLearning(entries, date, heading)
	if at < 0 {
		return DashboardLearningBodyOut{}, nil
	}
	body := telemetry.Redact(strings.TrimSpace(entries[at]))
	return DashboardLearningBodyOut{
		Found:     true,
		Body:      telemetry.TruncateRunes(body, dashboardLearningBodyMax),
		Truncated: utf8.RuneCountInString(body) > dashboardLearningBodyMax,
	}, nil
}

// dashboardFindLearning returns the index in entries of the newest entry
// whose date and snapshot heading (dashboardLearningHeading) equal date and
// heading, or -1 when none does. It scans from the last entry back, so the
// newest of two entries with the same key wins. DashboardLearningBody and
// DashboardDeleteLearning both use it, so the delete removes the entry that
// the viewer shows.
func dashboardFindLearning(entries []string, date, heading string) int {
	for i := len(entries) - 1; i >= 0; i-- {
		e := strings.TrimSpace(entries[i])
		if learningsDateRe.FindString(e) == date && dashboardLearningHeading(e) == heading {
			return i
		}
	}
	return -1
}

// dashboardOpenDeferred returns root's open deferred issues as
// DashboardDeferred rows, high priority first — same grouping and
// within-group order (Created ascending) as FormatDeferredSummary
// (internal/history/history.go). Uses DeferredByPriority rather than
// OpenDeferred directly: DeferredByPriority applies the identical
// StatusOpen filter internally and also does the priority grouping this
// function needs, so a separate OpenDeferred call first would be redundant.
//
// The rows come from the parse cache, which is built again when
// deferred.json changes. A file that cannot be read or parsed gives nil and
// the error of ListDeferred, and the cache does not keep that result.
func dashboardOpenDeferred(root string) ([]DashboardDeferred, error) {
	w := history.NewFileWriter(paths.HistoryDir(root))
	return dashboardCached(dashboardCacheKey(root, "deferred"), []string{w.DeferredPath()}, func() ([]DashboardDeferred, error) {
		return dashboardBuildDeferred(w)
	})
}

// dashboardBuildDeferred lists the open deferred issues of w as
// DashboardDeferred rows, high priority first. The error is the error of
// ListDeferred; the rows are then nil.
func dashboardBuildDeferred(w *history.FileWriter) ([]DashboardDeferred, error) {
	issues, err := w.ListDeferred()
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
	return out, err
}

// dashboardRecentRuns returns the newest dashboardHistoryLimit rows of root's
// runs.jsonl as DashboardRun rows, newest first. runs.jsonl is appended
// oldest-last, so the rows are read in file order and reversed. A corrupt
// line is skipped and does not count toward the limit. A missing or empty
// file gives nil. All rows go through dashboardPairPlans before the cut, so
// a plan row outside the newest dashboardHistoryLimit rows still pairs with
// a ship row inside them. A row with an empty skill or ts (for example a
// null line or {}) is skipped like a corrupt line: DashboardDeleteRuns cannot
// name it, so the page must not show it.
//
// The rows come from the parse cache, which is built again when runs.jsonl
// changes. A file that cannot be read gives nil and the read error, and the
// cache does not keep that result.
func dashboardRecentRuns(root string) ([]DashboardRun, error) {
	path := history.NewFileWriter(paths.HistoryDir(root)).RunsPath()
	return dashboardCached(dashboardCacheKey(root, "history"), []string{path}, func() ([]DashboardRun, error) {
		return dashboardBuildRuns(path)
	})
}

// dashboardBuildRuns reads the runs.jsonl file at path and returns the rows
// that dashboardRecentRuns returns. The error is the read error of the file;
// the rows are then nil.
func dashboardBuildRuns(path string) ([]DashboardRun, error) {
	records, err := readJSONLEntries[history.RunRecord](path, "dashboard run history")
	runs := make([]DashboardRun, 0, len(records))
	for _, r := range records {
		if r.Skill == "" || r.Timestamp == "" {
			continue
		}
		runs = append(runs, dashboardRunFromRecord(r))
	}
	dashboardPairPlans(runs)
	if len(runs) > dashboardHistoryLimit {
		runs = runs[len(runs)-dashboardHistoryLimit:]
	}

	var out []DashboardRun
	for i := len(runs) - 1; i >= 0; i-- {
		out = append(out, runs[i])
	}
	return out, err
}

// dashboardPairPlans fills PlanStartedAt, PlanDurationMs, TotalMs,
// PlanPaired and PlanEndedAt of each ship row with no plan fields from the
// plan row of the same branch with the latest EndedAt before the ship
// EndedAt and after the EndedAt of the previous ship row of that branch.
// It walks the rows in EndedAt order and keeps the last plan row of each
// branch. Each ship row clears that plan row, paired or not.
//
// The walk order is the order of the parsed EndedAt values, not the file
// order. A row whose EndedAt does not parse sorts first, and the sort is
// stable, so rows with equal EndedAt keep the file order. The slice order of
// runs does not change.
//
// A ship row with a PlanStartedAt already still uses up the plan row. A ship
// row stays unchanged when it has no branch or when no plan row of its branch
// is waiting. A plan row whose EndedAt does not parse is never a waiting plan
// row. dashboardPairShip makes the pairing decision for a ship row and its
// waiting plan row.
func dashboardPairPlans(runs []DashboardRun) {
	ends := make([]time.Time, len(runs))
	order := make([]int, len(runs))
	for i := range runs {
		ends[i], _ = dashboardParseTime(runs[i].EndedAt)
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return ends[order[a]].Before(ends[order[b]])
	})

	lastPlan := map[string]int{}
	for _, i := range order {
		run := &runs[i]
		if run.Branch == "" {
			continue
		}
		switch run.Kind {
		case "plan":
			if !ends[i].IsZero() {
				lastPlan[run.Branch] = i
			}
		case "ship":
			p, ok := lastPlan[run.Branch]
			if !ok {
				continue
			}
			delete(lastPlan, run.Branch)
			dashboardPairShip(run, runs[p], ends[i])
		}
	}
}

// dashboardPairShip fills PlanStartedAt, PlanDurationMs, TotalMs, PlanPaired
// and PlanEndedAt of the ship row run, which ends at end, from plan. TotalMs
// becomes the time from the plan start to end. run stays unchanged when it
// has a PlanStartedAt already (it then gets no PlanPaired), when plan has a
// DurationMs of 0 or less or a StartedAt that does not parse, when end is the
// zero time, or when the plan window fails planWindowOK for end.
func dashboardPairShip(run *DashboardRun, plan DashboardRun, end time.Time) {
	if run.PlanStartedAt != "" {
		return
	}
	planStart, okStart := dashboardParseTime(plan.StartedAt)
	if plan.DurationMs <= 0 || !okStart || end.IsZero() || !planWindowOK(planStart, end) {
		return
	}
	run.PlanStartedAt = plan.StartedAt
	run.PlanDurationMs = plan.DurationMs
	run.TotalMs = end.Sub(planStart).Milliseconds()
	run.PlanPaired = true
	run.PlanEndedAt = plan.EndedAt
}

// dashboardRunFromRecord maps one runs.jsonl row to a DashboardRun. EndedAt
// is the row's ts. StartedAt is the row's started_at when present, else ts
// minus duration_ms when ts parses and duration_ms is positive, else "".
// TotalMs is duration_ms. A ship row whose plan_started_at parses and does not
// come after ts also copies the plan fields and counts TotalMs from the plan
// start to ts.
func dashboardRunFromRecord(r history.RunRecord) DashboardRun {
	end, okEnd := dashboardParseTime(r.Timestamp)
	started := r.StartedAt
	if started == "" && r.DurationMs > 0 && okEnd {
		started = dashboardFormatTime(end.Add(-time.Duration(r.DurationMs) * time.Millisecond))
	}
	run := DashboardRun{
		Kind:       r.Skill,
		Branch:     r.Branch,
		Outcome:    r.Outcome,
		StartedAt:  started,
		EndedAt:    r.Timestamp,
		DurationMs: r.DurationMs,
		TotalMs:    r.DurationMs,
	}
	if r.Skill == "ship" && r.PlanStartedAt != "" {
		planStart, okStart := dashboardParseTime(r.PlanStartedAt)
		if okStart && okEnd && planWindowOK(planStart, end) {
			run.PlanStartedAt = r.PlanStartedAt
			run.PlanDurationMs = r.PlanDurationMs
			run.TotalMs = end.Sub(planStart).Milliseconds()
		}
	}
	return run
}
