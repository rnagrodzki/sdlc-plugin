package tools

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/telemetry"
)

// dashboardQueueRowsMax caps the rows of a review queue. The defer action of
// ship_state appends to data.deferredFindings with no cap, so the queue cuts
// the list here.
const dashboardQueueRowsMax = 200

// dashboardQueueThresholdDefault is the queue threshold when
// data.flags.reviewThreshold is absent or not a severity.
const dashboardQueueThresholdDefault = "info"

// dashboardReviewQueue builds the queue of one ship run from ship state:
// fix records, then deferredFindings. fixes may be nil. Called at the end
// of dashboardShipDetail, after the received-review step is built.
//
// A fix record gives one row with its own status and origin. A deferred
// finding gives one row with status "below" (reason "below-threshold" or no
// reason) or "deferred" (any other reason), origin dashboardOriginLocal, and
// its description as text. A deferred finding with the same file, line and
// title as a fix record gives no row: the fix record stands for it. The rows
// stop at dashboardQueueRowsMax. Unrouted is totals.Unaccounted when fixes is
// not empty (never below 0), else 0. Reason is dashboardQueueReasonNone when
// totals.Found is 0 and there is no row, dashboardQueueReasonAllBelow when
// every row has status "below", else "". The rows of review findings join
// later, in dashboardQueueAddFindings.
func dashboardReviewQueue(data map[string]any, totals *DashboardReviewTotals, fixes []DashboardFix) *DashboardReviewQueue {
	if totals == nil {
		totals = &DashboardReviewTotals{}
	}
	q := &DashboardReviewQueue{
		Threshold: dashboardQueueThreshold(data),
		Rows:      []DashboardQueueRow{},
	}

	fixKeys := map[string]bool{}
	for _, f := range fixes {
		line := dashboardQueueLine(f.Line)
		fixKeys[dashboardQueueKey(f.File, line, f.Title)] = true
		q.Rows = append(q.Rows, DashboardQueueRow{
			Title:    f.Title,
			Severity: f.Severity,
			File:     f.File,
			Line:     line,
			Status:   f.Status,
			Origin:   f.Origin,
		})
	}

	deferred, _ := data["deferredFindings"].([]any)
	for _, raw := range deferred {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		title := dashboardPreview(dashboardStr(m["title"]))
		if title == "" {
			continue
		}
		file, line := dashboardStr(m["file"]), dashboardQueueLine(dashboardInt(m["line"]))
		if fixKeys[dashboardQueueKey(file, line, title)] {
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
			Origin:   dashboardOriginLocal,
			Text:     telemetry.Redact(dashboardStr(m["description"])),
		})
	}
	if len(q.Rows) > dashboardQueueRowsMax {
		q.Rows = q.Rows[:dashboardQueueRowsMax]
	}

	if len(fixes) > 0 {
		q.Unrouted = max(totals.Unaccounted, 0)
	}
	q.Reason = dashboardQueueReason(q.Rows, totals.Found)
	return q
}

// dashboardQueueStatusDeferred is the queue row status of a deferred finding
// that is not below the threshold. It is also a member of healingFixStatuses.
const dashboardQueueStatusDeferred = "deferred"

// dashboardQueueAddFindings adds open rows when q has no rows, and the
// dimension and text of each row with exactly one same file and line
// review finding. Called in dashboardNestReview after dims is built.
//
// With no row, each finding of dims gives one row with status "open", origin
// dashboardOriginLocal, and the dimension name. The rows go highest severity
// first, then in dimension order, and stop at dashboardQueueRowsMax. Reason
// is "" after that, since the queue has rows. With rows, a row gets
// Dimension from the finding that has its file and line, only when exactly
// one finding has them; a row with no file or no line gets none. A row gets
// Text from that finding only when its own Text is empty, so the description
// of a deferred finding stays.
func dashboardQueueAddFindings(q *DashboardReviewQueue, dims []DashboardDimension) {
	if q == nil {
		return
	}
	if len(q.Rows) == 0 {
		var rows []DashboardQueueRow
		for _, d := range dims {
			for _, f := range d.FindingItems {
				rows = append(rows, DashboardQueueRow{
					Title:     dashboardPreview(dashboardFirstLine(f.Text)),
					Severity:  dashboardSeverity(f.Severity),
					File:      f.File,
					Line:      f.Line,
					Status:    dashboardQueueStatusOpen,
					Origin:    dashboardOriginLocal,
					Dimension: d.Name,
					Text:      f.Text,
				})
			}
		}
		sort.SliceStable(rows, func(i, j int) bool {
			return severityRank[rows[i].Severity] > severityRank[rows[j].Severity]
		})
		if len(rows) > dashboardQueueRowsMax {
			rows = rows[:dashboardQueueRowsMax]
		}
		if len(rows) > 0 {
			q.Rows, q.Reason = rows, ""
		}
		return
	}

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
// severity of dimensions.ValidSeverities, else dashboardQueueThresholdDefault.
func dashboardQueueThreshold(data map[string]any) string {
	flags, _ := data["flags"].(map[string]any)
	if t := dashboardStr(flags["reviewThreshold"]); slices.Contains(dimensions.ValidSeverities, t) {
		return t
	}
	return dashboardQueueThresholdDefault
}

// dashboardQueueReason returns the reason of a queue: no-findings when found
// is 0 and rows is empty, all-below-threshold when rows is not empty and
// every row has status below, else "". The two reasons exclude each other: the
// first needs no row and the second needs one.
func dashboardQueueReason(rows []DashboardQueueRow, found int) string {
	if len(rows) == 0 {
		if found == 0 {
			return dashboardQueueReasonNone
		}
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
