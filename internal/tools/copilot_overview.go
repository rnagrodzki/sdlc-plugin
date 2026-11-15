package tools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/telemetry"
)

// Overview header values (also the poll result's ext.overview values).
const (
	overviewApprovalRecommended = "approval-recommended"
	overviewChangesRecommended  = "changes-recommended"
	overviewNeedsCloserLook     = "needs-closer-look"
)

// overviewHeaders maps the text of each overview header line to its value.
// overviewHeaderRe is built from this table, so a new header is added here
// only.
var overviewHeaders = []struct{ text, value string }{
	{"🟢 Approval recommended", overviewApprovalRecommended},
	{"🟡 Changes recommended", overviewChangesRecommended},
	{"🔵 Needs a closer look", overviewNeedsCloserLook},
}

// overviewValues returns the header values in table order.
func overviewValues() []string {
	vals := make([]string, len(overviewHeaders))
	for i, h := range overviewHeaders {
		vals[i] = h.value
	}
	return vals
}

// Block titles of the overview, as the <summary> line shows them after the
// item count. overviewBlockRe is built from them.
const (
	overviewBlockOpen     = "open"              // "N open issue(s)"
	overviewBlockMissed   = "previously missed" // "N previously missed issue(s)"
	overviewBlockResolved = "resolved since last review"
)

// Section values of CommentThread.OverviewSection.
const (
	overviewSectionOpen     = "open"
	overviewSectionResolved = "resolved"
)

// CopilotOverview is the parsed body of a Copilot pull request review
// overview.
type CopilotOverview struct {
	Header   string         // one of the overviewHeaders values
	Open     []OverviewItem // "N open issue(s)"
	Missed   []OverviewItem // "N previously missed issue(s)"
	Resolved []OverviewItem // "N resolved since last review"
	// Problems names each block that was not read in full: an item count
	// that differs from the count in the block summary, or a block with no
	// closing </details>. A non-empty list means the item lists can be
	// incomplete. nil when every block was read in full.
	Problems []string
}

// OverviewItem is one finding listed in an overview block.
type OverviewItem struct {
	Title     string
	Severity  string // "high" | "medium" | "low" | "" from alt="<Level> severity"
	CommentID int    // from #discussion_r<id>; 0 for missed items
	Path      string // missed items only, U+200B removed
	Line      int    // missed items only
	Body      string // missed items only, paragraph text
}

var (
	// The header line decides that a review body is an overview. The
	// <!-- ccr-overview-v2 --> marker comment is optional and not read.
	// Up to 3 leading spaces are allowed, as in Markdown.
	overviewHeaderRe = regexp.MustCompile(`(?m)^ {0,3}###\s+(` + overviewHeaderAlternatives() + `)\s*$`)
	overviewBlockRe  = regexp.MustCompile(`<summary><strong>(\d+) (` +
		regexp.QuoteMeta(overviewBlockOpen) + ` issues?|` +
		regexp.QuoteMeta(overviewBlockMissed) + ` issues?|` +
		regexp.QuoteMeta(overviewBlockResolved) + `)</strong></summary>`)
	overviewIconRe = regexp.MustCompile(`alt="(High|Medium|Low) severity"`)
	overviewLinkRe = regexp.MustCompile(`\[(.+?)\]\(https?://github\.com/[^)\s]*#discussion_r(\d+)\)`)
	overviewPathRe = regexp.MustCompile("(?m)^\\s*`([^`]+):(\\d+)`\\s*$") // missed path line, after U+200B removal

	// overviewDetailsTagRe finds the opening and closing <details> tags that
	// are counted to locate the end of a block.
	overviewDetailsTagRe = regexp.MustCompile(`<details\b|</details>`)
	// overviewSummaryRe captures the summary of a nested missed item.
	overviewSummaryRe = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
)

// overviewHeaderAlternatives joins the quoted header texts with "|".
func overviewHeaderAlternatives() string {
	alts := make([]string, len(overviewHeaders))
	for i, h := range overviewHeaders {
		alts[i] = regexp.QuoteMeta(h.text)
	}
	return strings.Join(alts, "|")
}

const overviewZeroWidthSpace = "\u200b"

// parseCopilotOverview returns ok=false when no header line matches. CRLF
// line ends are read as LF.
func parseCopilotOverview(body string) (CopilotOverview, bool) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	h := overviewHeaderRe.FindStringSubmatch(body)
	if h == nil {
		return CopilotOverview{}, false
	}
	var ov CopilotOverview
	for _, hd := range overviewHeaders {
		if hd.text == h[1] {
			ov.Header = hd.value
		}
	}

	for _, m := range overviewBlockRe.FindAllStringSubmatchIndex(body, -1) {
		declared := body[m[2]:m[3]]
		title := body[m[4]:m[5]]
		content, _, closed := overviewBlockContent(body[m[1]:])
		if !closed {
			ov.Problems = append(ov.Problems, fmt.Sprintf("the %q block has no closing </details>", title))
		}
		var items []OverviewItem
		switch {
		case strings.HasPrefix(title, overviewBlockOpen):
			items = parseOverviewListItems(content)
			ov.Open = append(ov.Open, items...)
		case strings.HasPrefix(title, overviewBlockMissed):
			items = parseOverviewMissedItems(content)
			ov.Missed = append(ov.Missed, items...)
		default:
			items = parseOverviewListItems(content)
			ov.Resolved = append(ov.Resolved, items...)
		}
		// A count too large for an int gives an error, which also counts as
		// a mismatch.
		if n, err := strconv.Atoi(declared); err != nil || n != len(items) {
			ov.Problems = append(ov.Problems, fmt.Sprintf("the %q block declares %s item(s), %d read", title, declared, len(items)))
		}
	}
	return ov, true
}

// overviewBlockContent takes the text that follows the summary of an open
// <details> element. It returns the text up to the matching </details>, the
// text after it and closed=true. Nested <details> tags are counted. An
// unclosed block returns all the text and closed=false.
func overviewBlockContent(s string) (content, rest string, closed bool) {
	depth := 1
	for _, loc := range overviewDetailsTagRe.FindAllStringIndex(s, -1) {
		if strings.HasPrefix(s[loc[0]:], "</") {
			depth--
		} else {
			depth++
		}
		if depth == 0 {
			return s[:loc[0]], s[loc[1]:], true
		}
	}
	return s, "", false
}

// overviewSeverity returns the lower-case level of the first severity icon.
func overviewSeverity(s string) string {
	if m := overviewIconRe.FindStringSubmatch(s); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

// parseOverviewListItems reads the open and resolved blocks: one item for
// each line that starts with "- " (after leading spaces) and holds a comment
// link. A line that does not match gives no item; parseCopilotOverview
// reports the missing items through its count check.
func parseOverviewListItems(content string) []OverviewItem {
	var items []OverviewItem
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		link := overviewLinkRe.FindStringSubmatch(line)
		if link == nil {
			continue
		}
		id, err := strconv.Atoi(link[2])
		if err != nil {
			continue
		}
		items = append(items, OverviewItem{
			Title:     strings.TrimSpace(link[1]),
			Severity:  overviewSeverity(line),
			CommentID: id,
		})
	}
	return items
}

// parseOverviewMissedItems reads the missed block: one item for each nested
// <details> element.
func parseOverviewMissedItems(content string) []OverviewItem {
	var items []OverviewItem
	for {
		loc := overviewDetailsTagRe.FindStringIndex(content)
		if loc == nil {
			return items
		}
		if strings.HasPrefix(content[loc[0]:], "</") {
			content = content[loc[1]:]
			continue
		}
		openEnd := strings.Index(content[loc[1]:], ">")
		if openEnd < 0 {
			return items
		}
		// An unclosed nested element takes the rest of the block. The
		// caller's count check reports the items lost that way.
		inner, rest, _ := overviewBlockContent(content[loc[1]+openEnd+1:])
		content = rest
		if item, ok := parseOverviewMissedItem(inner); ok {
			items = append(items, item)
		}
	}
}

// parseOverviewMissedItem reads one nested <details> element: the summary
// holds the icon and the title, and the path line and the paragraph follow.
func parseOverviewMissedItem(inner string) (OverviewItem, bool) {
	sm := overviewSummaryRe.FindStringSubmatchIndex(inner)
	if sm == nil {
		return OverviewItem{}, false
	}
	summary := inner[sm[2]:sm[3]]
	title := summary
	if i := strings.LastIndex(summary, "</picture>"); i >= 0 {
		title = summary[i+len("</picture>"):]
	}
	item := OverviewItem{
		Title:    strings.TrimSpace(title),
		Severity: overviewSeverity(summary),
	}

	rest := strings.ReplaceAll(inner[sm[1]:], overviewZeroWidthSpace, "")
	if p := overviewPathRe.FindStringSubmatchIndex(rest); p != nil {
		item.Path = rest[p[2]:p[3]]
		// The capture is digits only, so Atoi fails only on a number too
		// large for an int. Line then stays 0, which the output shows as
		// "no line", and the path is kept.
		if n, err := strconv.Atoi(rest[p[4]:p[5]]); err == nil {
			item.Line = n
		}
		rest = rest[:p[0]] + rest[p[1]:]
	}
	item.Body = strings.TrimSpace(rest)
	return item, true
}

// latestCopilotOverview returns the parsed overview of the newest Copilot
// review that has an overview header. It skips PENDING reviews and reviews
// with no header. Ties and missing timestamps keep gh's oldest-first order
// (latestReviewBy).
func latestCopilotOverview(reviews []ghx.PRReview) (CopilotOverview, bool) {
	latest := latestReviewBy(reviews, copilotReviewer, func(rv *ghx.PRReview) bool {
		_, ok := parseCopilotOverview(rv.Body)
		return ok
	})
	if latest == nil {
		return CopilotOverview{}, false
	}
	return parseCopilotOverview(latest.Body)
}

// joinCopilotOverview adds overview data to threads. It makes no gh call.
// A thread is "by Copilot" when isCopilotReviewer(t.Reviewer). It returns a
// copy of threads, the header of the latest overview ("" when none), the
// missed findings (capped by capOverviewFindings; empty, never nil) and one
// warnings line for each overview problem, for the open items that match no
// thread, and for each cap that cut the findings.
func joinCopilotOverview(threads []CommentThread, reviews []ghx.PRReview) (out []CommentThread, overview string, findings []OverviewFinding, warnings []string) {
	out = make([]CommentThread, len(threads))
	copy(out, threads)
	findings = []OverviewFinding{}

	ov, hasOverview := latestCopilotOverview(reviews)

	type listed struct{ section, icon string }
	byID := map[int]listed{}
	if hasOverview {
		overview = ov.Header
		for _, p := range ov.Problems {
			warnings = append(warnings, fmt.Sprintf(overviewProblemFmt, p))
		}
		for _, it := range ov.Open {
			byID[it.CommentID] = listed{overviewSectionOpen, it.Severity}
		}
		for _, it := range ov.Resolved {
			byID[it.CommentID] = listed{overviewSectionResolved, it.Severity}
		}
		var capWarnings []string
		findings, capWarnings = capOverviewFindings(ov.Missed)
		warnings = append(warnings, capWarnings...)
	}

	matched := map[int]bool{}
	for i := range out {
		t := &out[i]
		if isCopilotReviewer(t.Reviewer) {
			t.SeverityPrefix = inlineSeverityPrefix(t.Body)
		}
		if l, ok := byID[t.ID]; ok {
			t.OverviewSection = l.section
			t.SeverityIcon = l.icon
			matched[t.ID] = true
		}
		t.Severity = higherSeverity(t.SeverityPrefix, t.SeverityIcon)
	}

	var orphans []int
	for _, it := range ov.Open {
		if !matched[it.CommentID] {
			orphans = append(orphans, it.CommentID)
		}
	}
	if len(orphans) > 0 {
		warnings = append(warnings, fmt.Sprintf(overviewOrphanFmt, len(orphans), joinIDs(orphans)))
	}
	return out, overview, findings, warnings
}

// capOverviewFindings turns the missed items into findings: at most
// maxOverviewFindings of them, each body cut to maxOverviewFindingBodyRunes
// runes. It returns one warnings line for each cap that cut something.
func capOverviewFindings(missed []OverviewItem) (findings []OverviewFinding, warnings []string) {
	findings = make([]OverviewFinding, 0, min(len(missed), maxOverviewFindings))
	cutBodies := 0
	for i, it := range missed {
		if i == maxOverviewFindings {
			break
		}
		body := it.Body
		if utf8.RuneCountInString(body) > maxOverviewFindingBodyRunes {
			body = telemetry.TruncateRunes(body, maxOverviewFindingBodyRunes)
			cutBodies++
		}
		findings = append(findings, OverviewFinding{
			Title:    it.Title,
			Path:     it.Path,
			Line:     it.Line,
			Severity: it.Severity,
			Body:     body,
		})
	}
	if n := len(missed) - maxOverviewFindings; n > 0 {
		warnings = append(warnings, fmt.Sprintf(overviewFindingsCountFmt, n, maxOverviewFindings))
	}
	if cutBodies > 0 {
		warnings = append(warnings, fmt.Sprintf(overviewFindingsBodyFmt, cutBodies, maxOverviewFindingBodyRunes))
	}
	return findings, warnings
}
