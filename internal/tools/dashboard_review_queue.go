package tools

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/shipmeta"
	"github.com/rnagrodzki/sdlc-plugin/internal/telemetry"
)

// dashboardQueueRowsMax caps the rows of a review queue. The defer action of
// ship_state appends to data.deferredFindings with no cap, so the queue cuts
// the list here.
const dashboardQueueRowsMax = 200

// dashboardReviewQueue builds the queue of one ship run from ship state:
// fix records, then deferredFindings. fixes may be nil. Called at the end
// of dashboardShipDetail, after the received-review step is built.
//
// A fix record gives one row with its own status and origin. A deferred
// finding gives one row with status "below" (reason "below-threshold" or no
// reason) or "deferred" (any other reason), origin healingOriginLocal, and
// its description as text. A deferred finding with the same file, line and
// title as a fix record gives no row: the fix record stands for it. So does a
// deferred finding with the deferredId of a fix record, and a deferred finding
// on a file and line that hold exactly one fix record, whatever its title. A
// deferred finding with no line pairs the same way with the only fix record
// with no line on its file. The rows stop at dashboardQueueRowsMax. Unrouted
// is totals.Unaccounted, never below 0: the findings that have no fix record
// and no deferred entry. Reason comes from dashboardQueueReason. The rows of
// review findings join later, in dashboardQueueAddFindings.
//
// issues holds one state issue for each problem of the ship state that the
// queue cannot show: a deferredFindings value that is not a list, deferred
// entries that are not an object or have no title, a reviewThreshold flag
// that is not a severity, and rows cut at dashboardQueueRowsMax.
func dashboardReviewQueue(data map[string]any, totals *DashboardReviewTotals, fixes []DashboardFix) (q *DashboardReviewQueue, issues []DashboardIssue) {
	if totals == nil {
		totals = &DashboardReviewTotals{}
	}
	threshold, problem := dashboardQueueThreshold(data)
	if problem != "" {
		issues = append(issues, dashboardQueueIssue("medium", problem))
	}
	q = &DashboardReviewQueue{
		Threshold: threshold,
		Rows:      []DashboardQueueRow{},
	}

	fixKeys := map[string]bool{}
	fixIDs := map[string]bool{}
	fixLines := map[string]int{}
	for _, f := range fixes {
		line := dashboardQueueLine(f.Line)
		fixKeys[dashboardQueueKey(f.File, line, f.Title)] = true
		if f.DeferredID != "" {
			fixIDs[f.DeferredID] = true
		}
		fixLines[f.File+"\x00"+line]++
		q.Rows = append(q.Rows, DashboardQueueRow{
			Title:    f.Title,
			Severity: f.Severity,
			File:     f.File,
			Line:     line,
			Status:   f.Status,
			Origin:   f.Origin,
		})
	}

	deferred, problem := dashboardQueueDeferredList(data)
	if problem != "" {
		issues = append(issues, dashboardQueueIssue("medium", problem))
	}
	bad := 0
	for _, raw := range deferred {
		m, ok := raw.(map[string]any)
		if !ok {
			bad++
			continue
		}
		title := dashboardPreview(dashboardStr(m["title"]))
		if title == "" {
			bad++
			continue
		}
		file, line := dashboardStr(m["file"]), dashboardQueueLine(dashboardInt(m["line"]))
		if fixIDs[dashboardStr(m["deferredId"])] || fixKeys[dashboardQueueKey(file, line, title)] ||
			fixLines[file+"\x00"+line] == 1 {
			continue
		}
		status := dashboardQueueStatusBelow
		if reason := dashboardStr(m["reason"]); reason != "" && reason != history.ReasonBelowThreshold {
			status = dashboardQueueStatusDeferred
		}
		q.Rows = append(q.Rows, DashboardQueueRow{
			Title:    title,
			Severity: dashboardSeverity(dashboardStr(m["severity"])),
			File:     file,
			Line:     line,
			Status:   status,
			Origin:   healingOriginLocal,
			Text:     telemetry.Redact(dashboardStr(m["description"])),
		})
	}
	if bad > 0 {
		issues = append(issues, dashboardQueueIssue("medium", fmt.Sprintf(
			"data.deferredFindings of the ship state holds %d entries that are not an object or have no title — the review queue does not show them", bad)))
	}
	var cut []DashboardIssue
	q.Rows, cut = dashboardQueueCut(q.Rows)
	issues = append(issues, cut...)

	q.Unrouted = max(totals.Unaccounted, 0)
	q.Reason = dashboardQueueReason(q.Rows, totals.Found, q.Unrouted)
	return q, issues
}

// dashboardQueueDeferredList returns the data.deferredFindings list. problem
// is the text of a state issue when the key is present and its value is not a
// list (null included), else empty. An absent key gives no list and no problem.
func dashboardQueueDeferredList(data map[string]any) (list []any, problem string) {
	raw, present := data["deferredFindings"]
	if !present {
		return nil, ""
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, "data.deferredFindings of the ship state is not a list — the review queue shows no deferred finding"
	}
	return list, ""
}

// dashboardQueueIssue returns a state issue of the review queue with the
// severity and text given.
func dashboardQueueIssue(severity, text string) DashboardIssue {
	return DashboardIssue{Source: dashboardSourceState, Severity: severity, Text: text}
}

// dashboardQueueCut returns rows cut at dashboardQueueRowsMax. When it cuts
// rows, issues holds one info issue that tells how many rows are not shown.
func dashboardQueueCut(rows []DashboardQueueRow) ([]DashboardQueueRow, []DashboardIssue) {
	if len(rows) <= dashboardQueueRowsMax {
		return rows, nil
	}
	issue := dashboardQueueIssue("info", fmt.Sprintf(
		"%d review queue rows not shown: the queue shows the first %d of %d", len(rows)-dashboardQueueRowsMax, dashboardQueueRowsMax, len(rows)))
	return rows[:dashboardQueueRowsMax], []DashboardIssue{issue}
}

// dashboardQueueAddFindings joins the review findings of dims into q. Called
// in dashboardNestReview after dims is built. A queue with no row gets the
// open rows of dashboardQueueOpenRows. A queue with rows gets the dimension
// and text of its rows from dashboardQueueMatchFindings. issues holds the
// issue of open rows cut at dashboardQueueRowsMax.
func dashboardQueueAddFindings(q *DashboardReviewQueue, dims []DashboardDimension) (issues []DashboardIssue) {
	if q == nil {
		return nil
	}
	if len(q.Rows) == 0 {
		return dashboardQueueOpenRows(q, dims)
	}
	dashboardQueueMatchFindings(q, dims)
	return nil
}

// dashboardQueueOpenRows fills the empty queue q with one row for each
// finding of dims: status "open", origin healingOriginLocal, and the
// dimension name. The rows go highest severity first, then in dimension
// order, and stop at dashboardQueueRowsMax. When dims has a finding, Reason
// becomes "" and Unrouted becomes 0, because every finding now has a row.
// issues holds the issue of rows cut at dashboardQueueRowsMax.
func dashboardQueueOpenRows(q *DashboardReviewQueue, dims []DashboardDimension) (issues []DashboardIssue) {
	var rows []DashboardQueueRow
	for _, d := range dims {
		for _, f := range d.FindingItems {
			rows = append(rows, DashboardQueueRow{
				Title:     dashboardPreview(dashboardFirstLine(f.Text)),
				Severity:  dashboardSeverity(f.Severity),
				File:      f.File,
				Line:      f.Line,
				Status:    dashboardQueueStatusOpen,
				Origin:    healingOriginLocal,
				Dimension: d.Name,
				Text:      f.Text,
			})
		}
	}
	if len(rows) == 0 {
		return nil
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return severityRank[rows[i].Severity] > severityRank[rows[j].Severity]
	})
	q.Rows, issues = dashboardQueueCut(rows)
	q.Reason, q.Unrouted = "", 0
	return issues
}

// dashboardQueueMatchFindings sets the dimension of each row of q from the
// finding of dims that has the file and line of the row, only when exactly
// one finding has them. A row with no file or no line gets none. A row gets
// the text of that finding only when its own Text is empty, so the
// description of a deferred finding stays.
func dashboardQueueMatchFindings(q *DashboardReviewQueue, dims []DashboardDimension) {
	type found struct {
		dimension, text string
		count           int
	}
	byLocation := map[string]*found{}
	for _, d := range dims {
		for _, f := range d.FindingItems {
			if f.File == "" || f.Line == "" {
				continue
			}
			key := dashboardQueueKey(f.File, f.Line, "")
			if m := byLocation[key]; m != nil {
				m.count++
				continue
			}
			byLocation[key] = &found{dimension: d.Name, text: f.Text, count: 1}
		}
	}
	for i := range q.Rows {
		row := &q.Rows[i]
		if row.File == "" || row.Line == "" {
			continue
		}
		m := byLocation[dashboardQueueKey(row.File, row.Line, "")]
		if m == nil || m.count != 1 {
			continue
		}
		row.Dimension = m.dimension
		if row.Text == "" {
			row.Text = m.text
		}
	}
}

// dashboardQueueThreshold returns data.flags.reviewThreshold when it is a
// severity of dimensions.ValidSeverities, else the ship default
// shipmeta.ShipBuiltInDefaults.ReviewThreshold. problem is the text of a
// state issue when data.flags is present and not an object, or
// flags.reviewThreshold is present and not a severity, else empty.
func dashboardQueueThreshold(data map[string]any) (threshold, problem string) {
	threshold = shipmeta.ShipBuiltInDefaults.ReviewThreshold
	rawFlags, present := data["flags"]
	if !present {
		return threshold, ""
	}
	flags, ok := rawFlags.(map[string]any)
	if !ok {
		return threshold, "data.flags of the ship state is not an object — the review queue shows the threshold " + threshold
	}
	raw, present := flags["reviewThreshold"]
	if !present {
		return threshold, ""
	}
	if t := dashboardStr(raw); slices.Contains(dimensions.ValidSeverities, t) {
		return t, ""
	}
	return threshold, "data.flags.reviewThreshold of the ship state is not a severity — the review queue shows the threshold " + threshold
}

// dashboardQueueReason returns the reason of a queue: no-findings when found
// is 0 and rows is empty, all-below-threshold when rows is not empty, every
// row has status below and unrouted is 0, else "". The two reasons exclude
// each other: the first needs no row and the second needs one. An open
// finding with no row counts in unrouted, so it stops all-below-threshold.
func dashboardQueueReason(rows []DashboardQueueRow, found, unrouted int) string {
	if len(rows) == 0 {
		if found == 0 {
			return dashboardQueueReasonNone
		}
		return ""
	}
	if unrouted > 0 {
		return ""
	}
	for _, r := range rows {
		if r.Status != dashboardQueueStatusBelow {
			return ""
		}
	}
	return dashboardQueueReasonAllBelow
}

// dashboardQueueLine returns line as text, or "" when line is 0 or less.
func dashboardQueueLine(line int) string {
	if line <= 0 {
		return ""
	}
	return strconv.Itoa(line)
}

// dashboardQueueKey is the key that tells whether a deferred finding and a
// fix record are the same finding: file, line text and title.
func dashboardQueueKey(file, line, title string) string {
	return file + "\x00" + line + "\x00" + title
}

// dashboardFirstLine returns s up to its first line break, with the space at
// both ends removed.
func dashboardFirstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}
