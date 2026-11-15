package tools

import (
	"regexp"
	"strconv"
	"strings"
)

// Overview header values (also the poll result's ext.overview values).
const (
	overviewApprovalRecommended = "approval-recommended" // ### 🟢 Approval recommended
	overviewChangesRecommended  = "changes-recommended"  // ### 🟡 Changes recommended
	overviewNeedsCloserLook     = "needs-closer-look"    // ### 🔵 Needs a closer look
)

// CopilotOverview is the parsed body of a Copilot pull request review
// overview.
type CopilotOverview struct {
	Header   string         // one of the 3 overview* constants
	Open     []OverviewItem // "N open issue(s)"
	Missed   []OverviewItem // "N previously missed issue(s)"
	Resolved []OverviewItem // "N resolved since last review"
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
	overviewHeaderRe = regexp.MustCompile(`(?m)^###\s+(🟢 Approval recommended|🟡 Changes recommended|🔵 Needs a closer look)\s*$`)
	overviewBlockRe  = regexp.MustCompile(`<summary><strong>(\d+) (open issues?|previously missed issues?|resolved since last review)</strong></summary>`)
	overviewIconRe   = regexp.MustCompile(`alt="(High|Medium|Low) severity"`)
	overviewLinkRe   = regexp.MustCompile(`\[(.+?)\]\(https?://github\.com/[^)\s]*#discussion_r(\d+)\)`)
	overviewPathRe   = regexp.MustCompile("(?m)^`([^`]+):(\\d+)`\\s*$") // missed path line, after U+200B removal

	// overviewDetailsTagRe finds the opening and closing <details> tags that
	// are counted to locate the end of a block.
	overviewDetailsTagRe = regexp.MustCompile(`<details\b|</details>`)
	// overviewSummaryRe captures the summary of a nested missed item.
	overviewSummaryRe = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
)

const overviewZeroWidthSpace = "\u200b"

// parseCopilotOverview returns ok=false when no header line matches.
func parseCopilotOverview(body string) (CopilotOverview, bool) {
	h := overviewHeaderRe.FindStringSubmatch(body)
	if h == nil {
		return CopilotOverview{}, false
	}
	var ov CopilotOverview
	switch h[1] {
	case "🟢 Approval recommended":
		ov.Header = overviewApprovalRecommended
	case "🟡 Changes recommended":
		ov.Header = overviewChangesRecommended
	default:
		ov.Header = overviewNeedsCloserLook
	}

	for _, m := range overviewBlockRe.FindAllStringSubmatchIndex(body, -1) {
		title := body[m[4]:m[5]]
		content, _ := overviewBlockContent(body[m[1]:])
		switch {
		case strings.HasPrefix(title, "open"):
			ov.Open = append(ov.Open, parseOverviewListItems(content)...)
		case strings.HasPrefix(title, "previously missed"):
			ov.Missed = append(ov.Missed, parseOverviewMissedItems(content)...)
		default:
			ov.Resolved = append(ov.Resolved, parseOverviewListItems(content)...)
		}
	}
	return ov, true
}

// overviewBlockContent takes the text that follows the summary of an open
// <details> element. It returns the text up to the matching </details> and
// the text after it. Nested <details> tags are counted. An unclosed block
// returns all the text.
func overviewBlockContent(s string) (content, rest string) {
	depth := 1
	for _, loc := range overviewDetailsTagRe.FindAllStringIndex(s, -1) {
		if strings.HasPrefix(s[loc[0]:], "</") {
			depth--
		} else {
			depth++
		}
		if depth == 0 {
			return s[:loc[0]], s[loc[1]:]
		}
	}
	return s, ""
}

// overviewSeverity returns the lower-case level of the first severity icon.
func overviewSeverity(s string) string {
	if m := overviewIconRe.FindStringSubmatch(s); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

// parseOverviewListItems reads the open and resolved blocks: one item for
// each line that starts with "- " and holds a comment link.
func parseOverviewListItems(content string) []OverviewItem {
	var items []OverviewItem
	for _, line := range strings.Split(content, "\n") {
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
		inner, rest := overviewBlockContent(content[loc[1]+openEnd+1:])
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
		item.Line, _ = strconv.Atoi(rest[p[4]:p[5]])
		rest = rest[:p[0]] + rest[p[1]:]
	}
	item.Body = strings.TrimSpace(rest)
	return item, true
}
