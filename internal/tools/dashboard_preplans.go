package tools

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

const (
	// dashboardPreplanLimit is the number of topic files the list keeps,
	// newest first.
	dashboardPreplanLimit = 100
	// dashboardPreplanHeadMax is the number of bytes read from the start of
	// each topic file. The topic line and the status line sit at the top.
	dashboardPreplanHeadMax = 4 << 10
)

// dashboardPreplanTopicRe matches the "# Preplan: <topic>" heading line of a
// topic file, capturing the topic text.
var dashboardPreplanTopicRe = regexp.MustCompile(`(?m)^# Preplan:[ \t]*(.*)$`)

// dashboardPreplanStatusRe matches the "**Status:** <status>" line of a topic
// file, capturing the status text.
var dashboardPreplanStatusRe = regexp.MustCompile(`(?m)^\*\*Status:\*\*[ \t]*(.*)$`)

// dashboardPreplanStartedRe matches the "**Started:** <time>" line of a topic
// file, capturing the time text. preplan_context writes the time in RFC 3339.
var dashboardPreplanStartedRe = regexp.MustCompile(`(?m)^\*\*Started:\*\*[ \t]*(\S*)`)

// dashboardPreplanFile is one topic file that passed the Stat check.
type dashboardPreplanFile struct {
	slug string
	path string // absolute path of the file
	mod  time.Time
}

// preplanFolderNotReadPrefix starts the warning of a preplan folder that
// cannot be listed. TestPlanSupportPreplanWriteFolderWarningPrefix pins it.
const preplanFolderNotReadPrefix = "Preplan folder not read: "

// dashboardPreplanWarnKind names the cause of one warning of
// dashboardPreplanScan. The preplan_write ready list (preplanReadyList) reads
// the kind, not the text.
type dashboardPreplanWarnKind int

const (
	// dashboardPreplanWarnFolder: the preplan folder cannot be listed.
	dashboardPreplanWarnFolder dashboardPreplanWarnKind = iota
	// dashboardPreplanWarnFile: the Stat or the head read of one topic file
	// failed, so the file has no row.
	dashboardPreplanWarnFile
	// dashboardPreplanWarnOlder: the files past dashboardPreplanLimit are not
	// read and have no row.
	dashboardPreplanWarnOlder
	// dashboardPreplanWarnStart: the Started time of a row does not parse.
	dashboardPreplanWarnStart
)

// dashboardPreplanWarning is one warning of dashboardPreplanScan. text is the
// warning as dashboardPreplans returns it. detail is the error text of a
// dashboardPreplanWarnFolder warning, and "" for the other kinds.
type dashboardPreplanWarning struct {
	kind   dashboardPreplanWarnKind
	text   string
	detail string
}

// dashboardPreplans lists the preplan topic files of root, newest first and
// ties by slug, so the snapshot hash stays the same between two polls. A read
// problem never fails the list: it adds one text to warnings and the rest of
// the list stays right. A missing preplan folder (or a dangling link in its
// place) is not a problem: it gives an empty list and no warning.
//
// list is never nil. The text of a topic and of a status goes through
// dashboardPreview, like the text of a timeline event. It is
// dashboardPreplanScan with the text of each warning.
func dashboardPreplans(root string) (list []DashboardPreplan, warnings []string) {
	list, scanned := dashboardPreplanScan(root)
	for _, w := range scanned {
		warnings = append(warnings, w.text)
	}
	return list, warnings
}

// dashboardPreplanScan does the work of dashboardPreplans and gives each
// warning its kind, in the same order.
func dashboardPreplanScan(root string) (list []DashboardPreplan, warnings []dashboardPreplanWarning) {
	list = []DashboardPreplan{}
	warn := func(kind dashboardPreplanWarnKind, text string) {
		warnings = append(warnings, dashboardPreplanWarning{kind: kind, text: text})
	}
	dir := filepath.Join(root, paths.DataDir, paths.PreplanSubdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			warnings = append(warnings, dashboardPreplanWarning{
				kind: dashboardPreplanWarnFolder, text: preplanFolderNotReadPrefix + err.Error(), detail: err.Error(),
			})
		}
		return list, warnings
	}

	var files []dashboardPreplanFile
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".md") || name == ".md" {
			continue
		}
		slug := strings.TrimSuffix(name, ".md")
		full := filepath.Join(dir, name)
		// Stat follows a link, so a link to a file counts as that file and a
		// dangling link gives the file error. A file that is gone since
		// ReadDir (a delete during the snapshot) is skipped with no warning.
		fi, err := os.Stat(full)
		if err != nil {
			if !dashboardPreplanGone(full, err) {
				warn(dashboardPreplanWarnFile, fmt.Sprintf("Preplan %s not read: %v", slug, err))
			}
			continue
		}
		// A sub-folder is skipped. So is a pipe or a device: opening one
		// could block the snapshot.
		if !fi.Mode().IsRegular() {
			continue
		}
		files = append(files, dashboardPreplanFile{slug: slug, path: full, mod: fi.ModTime()})
	}

	sort.Slice(files, func(i, j int) bool {
		if !files[i].mod.Equal(files[j].mod) {
			return files[i].mod.After(files[j].mod)
		}
		return files[i].slug < files[j].slug
	})
	var older int
	if len(files) > dashboardPreplanLimit {
		older = len(files) - dashboardPreplanLimit
		files = files[:dashboardPreplanLimit]
	}

	for _, f := range files {
		head, err := dashboardPreplanHead(f.path)
		if err != nil {
			if !dashboardPreplanGone(f.path, err) {
				warn(dashboardPreplanWarnFile, fmt.Sprintf("Preplan %s not read: %v", f.slug, err))
			}
			continue
		}
		topic := f.slug
		if m := dashboardPreplanTopicRe.FindStringSubmatch(head); m != nil {
			if t := dashboardPreview(strings.TrimSpace(m[1])); t != "" {
				topic = t
			}
		}
		status := ""
		if m := dashboardPreplanStatusRe.FindStringSubmatch(head); m != nil {
			status = dashboardPreview(strings.TrimSpace(m[1]))
		}
		durationMs, warning := dashboardPreplanDuration(head, f.slug, f.mod)
		if warning != "" {
			warn(dashboardPreplanWarnStart, warning)
		}
		list = append(list, DashboardPreplan{
			Slug:       f.slug,
			Topic:      topic,
			Status:     status,
			Path:       path.Join(paths.DataDir, paths.PreplanSubdir, f.slug+".md"),
			UpdatedAt:  dashboardFormatTime(f.mod),
			DurationMs: durationMs,
		})
	}
	if older > 0 {
		warn(dashboardPreplanWarnOlder, fmt.Sprintf("%d older preplan topic files not shown", older))
	}
	return list, warnings
}

// dashboardPreplanDuration returns the time from the "**Started:**" line of
// head to mod, in milliseconds, and a warning text ("" for none). A missing
// Started line gives 0 with no warning: every topic file from before the line
// existed has none. A time that does not parse gives 0 and one warning. A
// start at or after mod gives 0, like an unknown duration in History.
func dashboardPreplanDuration(head, slug string, mod time.Time) (int64, string) {
	m := dashboardPreplanStartedRe.FindStringSubmatch(head)
	if m == nil {
		return 0, ""
	}
	started, ok := dashboardParseTime(m[1])
	if !ok {
		return 0, fmt.Sprintf("Preplan %s start time not read: %q (expected RFC 3339; duration shown as 0)", slug, dashboardPreview(m[1]))
	}
	if !mod.After(started) {
		return 0, ""
	}
	return mod.Sub(started).Milliseconds(), ""
}

// dashboardPreplanGone reports whether err, from a Stat or an open of the
// topic file at p, means that the file itself is gone: err is
// fs.ErrNotExist and an Lstat of p finds nothing either. A dangling link
// gives false: its Lstat finds the link, so it keeps its warning.
func dashboardPreplanGone(p string, err error) bool {
	if !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	_, lerr := os.Lstat(p)
	return errors.Is(lerr, fs.ErrNotExist)
}

// dashboardPreplanHead returns the first dashboardPreplanHeadMax bytes of the
// file at p as valid UTF-8 text. A cut inside a multi-byte character drops the
// broken bytes. It is a package var so that a test can make the head read
// fail for every user, the root user included.
var dashboardPreplanHead = func(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, dashboardPreplanHeadMax))
	if err != nil {
		return "", err
	}
	return strings.ToValidUTF8(string(buf), ""), nil
}

// dashboardPreplanSummaryMax bounds the full read of one topic file for the
// summary fields of its row. A topic file of more than this many bytes is not
// read: every summary field of the row is unread, one repo warning names the
// size and the limit, and the row stays in the list. It is separate from
// dashboardPreplanHeadMax (the head read) and from dashboardDeleteReadMax
// (the read limit of preplan_check).
const dashboardPreplanSummaryMax = 256 << 10

// The names that DashboardPreplan.Unread holds. Each one stands for the
// summary fields that one part of the topic file gives.
const (
	dashboardPreplanUnreadGoal          = "goal"          // Goal, from ## Goal
	dashboardPreplanUnreadProposals     = "proposals"     // Live, Kept, Dropped, from ## Decisions
	dashboardPreplanUnreadSmallestCut   = "smallestCut"   // SmallestCut, from ## Decisions
	dashboardPreplanUnreadLastCheck     = "lastCheck"     // LastCheckAt, from the Guardrail check line
	dashboardPreplanUnreadOpenQuestions = "openQuestions" // OpenQuestions, from ## Open questions
	dashboardPreplanUnreadGaps          = "gaps"          // Gaps, from the section headings
)

// The values of DashboardPreplan.SmallestCut.
const (
	dashboardPreplanCutSet    = "set"     // ## Decisions holds a valid "Smallest cut:" line
	dashboardPreplanCutNotSet = "not set" // no such line, no ## Decisions, or the section is unread
)

// dashboardPreplanUnreadAll lists every unread name in the order that a row
// gives them. A row whose topic file is not read gets all six.
var dashboardPreplanUnreadAll = []string{
	dashboardPreplanUnreadGoal,
	dashboardPreplanUnreadProposals,
	dashboardPreplanUnreadSmallestCut,
	dashboardPreplanUnreadLastCheck,
	dashboardPreplanUnreadOpenQuestions,
	dashboardPreplanUnreadGaps,
}

// errPreplanSummaryNotRegular is the error of dashboardPreplanReadSummary
// for a link (live or dangling), a folder, a pipe or a device at the topic
// file path. In the snapshot flow, dashboardPreplans lists only entries that
// are regular after a Stat that follows links, so only a link to a regular
// file reaches this error there; a direct call reaches it for every kind
// named above.
var errPreplanSummaryNotRegular = errors.New("not a regular file")

// preplanSummaryTooBigError is the error of dashboardPreplanReadSummary for
// a topic file of more than max bytes. size is the Stat size of the file.
type preplanSummaryTooBigError struct{ size, max int64 }

// Error returns "the file is <size> bytes, the limit is <max> bytes".
func (e *preplanSummaryTooBigError) Error() string {
	return fmt.Sprintf("the file is %d bytes, the limit is %d bytes", e.size, e.max)
}

// dashboardPreplanReadSummary reads the topic file at p for the summary. It
// checks the Stat size against limit before the read and returns
// *preplanSummaryTooBigError with the size when the file is larger. It calls
// os.Lstat first, so a link (live or dangling), a folder, a pipe or a device
// gives errPreplanSummaryNotRegular and is never opened. A failed Lstat or
// read returns the os error. The Lstat and the read go through the seams of
// preplan_check (preplanLstat, preplanReadFile), so one injected error
// reaches both readers. Tests replace it to force a read failure.
// dashboardReadCapped (dashboard_delete.go) is a style mirror only: its cap
// is dashboardDeleteReadMax.
var dashboardPreplanReadSummary = func(p string, limit int64) ([]byte, error) {
	info, err := preplanLstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errPreplanSummaryNotRegular
	}
	if info.Size() > limit {
		return nil, &preplanSummaryTooBigError{size: info.Size(), max: limit}
	}
	return preplanReadFile(p)
}

// dashboardPreplanSummaries fills the summary fields of each row of list and
// returns the rows that stay, in the same order, and the repo warnings. It
// reads root/row.Path and reuses the array of list. dashboardPreplans stays
// head-only: the preplan_write ready list reads no full file. Section bodies
// come from preplanSectionSpans.
//
// A topic file that went away after the head read (dashboardPreplanGone)
// loses its row and adds no warning, as in the head read of
// dashboardPreplans. A topic file that is not read for another cause
// (dashboardPreplanReadSummary fails) keeps its row: every name of
// dashboardPreplanUnreadAll is unread, and one warning
// "Preplan <slug> summary not read: <error>" names the cause. A section that
// fails the checker adds its unread names and no warning. Gaps and Unread
// are never nil after this call.
func dashboardPreplanSummaries(root string, list []DashboardPreplan) (kept []DashboardPreplan, warnings []string) {
	kept = list[:0]
	for _, row := range list {
		row.Gaps, row.Unread, row.SmallestCut = []string{}, []string{}, dashboardPreplanCutNotSet
		file := filepath.Join(root, filepath.FromSlash(row.Path))
		data, err := dashboardPreplanReadSummary(file, dashboardPreplanSummaryMax)
		switch {
		case err == nil:
			dashboardPreplanSummarize(&row, data)
		case dashboardPreplanGone(file, err):
			continue
		default:
			row.Unread = slices.Clone(dashboardPreplanUnreadAll)
			warnings = append(warnings, fmt.Sprintf("Preplan %s summary not read: %v", row.Slug, err))
		}
		kept = append(kept, row)
	}
	return kept, warnings
}

// dashboardPreplanSummarize fills the summary fields of row from data, the
// whole topic file. Each part adds its unread names in the order of
// dashboardPreplanUnreadAll.
//
// A section heading that is found shows its fields when its body passes the
// checker, else its names are unread. A heading that is not found depends on
// the fences: with no fence open at the end of the file the section is
// absent and not unread (its fields keep their zero values, and Gaps names
// it when it is a skeleton section); with a fence open at the end, the
// heading can be hidden inside the fence, so its names are unread.
func dashboardPreplanSummarize(row *DashboardPreplan, data []byte) {
	spans, openFence := preplanSectionSpans(data)
	hidden := openFence != "" // a heading that is not found can be inside the fence
	dashboardPreplanSection(row, data, spans, hidden, preplanGoalSection, func(body string) {
		row.Goal = dashboardPreview(preplanGoalSentence(body))
	}, dashboardPreplanUnreadGoal)
	dashboardPreplanSection(row, data, spans, hidden, preplanDecisionsSection, func(body string) {
		sum := preplanSummarizeDecisions(body)
		row.Live, row.Kept, row.Dropped = sum.Live, sum.Kept, sum.Dropped
		if sum.CutSet {
			row.SmallestCut = dashboardPreplanCutSet
		}
	}, dashboardPreplanUnreadProposals, dashboardPreplanUnreadSmallestCut)
	// The check line sits in the head, where preplan_check writes it. A
	// legacy ## Guardrail check section is the old check record: the row
	// shows no time for it, even when a check line is there too, and so does
	// a check line whose time does not parse. A head with no check line and
	// no legacy section is a topic that was never checked: lastCheck is not
	// unread and LastCheckAt stays "". A check line that starts past the head
	// counts as no line, as it does for preplan_check, which then adds a new
	// line in the head.
	head := string(preplanHead(data))
	legacy := preplanFindSpan(spans, preplanGuardrailCheckSection) >= 0
	at, ok := preplanLastCheck(head)
	switch {
	case ok && !legacy:
		row.LastCheckAt = dashboardFormatTime(at)
	case legacy || preplanGuardrailCheckRe.MatchString(head):
		row.Unread = append(row.Unread, dashboardPreplanUnreadLastCheck)
	}
	dashboardPreplanSection(row, data, spans, hidden, preplanOpenQuestionsSection, func(body string) {
		row.OpenQuestions = preplanOpenQuestionCount(body)
	}, dashboardPreplanUnreadOpenQuestions)
	// A fence open at the end of the file makes each heading after it count
	// as absent, so the list is not shown.
	if hidden {
		row.Unread = append(row.Unread, dashboardPreplanUnreadGaps)
		return
	}
	for _, heading := range preplanEmptySectionsOf(data, spans) {
		row.Gaps = append(row.Gaps, strings.TrimPrefix(heading, preplanHeadingPrefix))
	}
}

// dashboardPreplanSection reads the section of data named name, from spans
// (preplanSectionSpans of data). A heading that is found and a body that
// passes the checker go to fill. A heading that is found with a body that
// fails the checker, or a heading that is not found while hidden is true,
// adds names to row.Unread. A heading that is not found while hidden is
// false is an absent section and changes nothing.
func dashboardPreplanSection(row *DashboardPreplan, data []byte, spans []preplanSpan, hidden bool, name string, fill func(body string), names ...string) {
	body, found := dashboardPreplanSectionBody(data, spans, name)
	switch {
	case found && preplanCheckSection(name, body) == nil:
		fill(body)
	case found || hidden:
		row.Unread = append(row.Unread, names...)
	}
}

// dashboardPreplanSectionBody returns the body of the first section of data
// named name, from spans (preplanSectionSpans of data). found is false when
// no such heading is outside a fence.
func dashboardPreplanSectionBody(data []byte, spans []preplanSpan, name string) (body string, found bool) {
	i := preplanFindSpan(spans, name)
	if i < 0 {
		return "", false
	}
	return string(data[spans[i].bodyStart:spans[i].bodyEnd]), true
}
