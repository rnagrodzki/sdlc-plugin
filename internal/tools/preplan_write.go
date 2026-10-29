package tools

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// This file holds the preplan_write action of plan_support: it replaces one
// section body or the Status line of an existing topic file. The find, read
// and write steps are the ones of preplan_check (preplanReadTopicFile,
// preplanWriteBytes), and the content check is preplanCheckSection
// (preplan_format.go).

// preplanReadyMax is the most entries of readyTopicFiles: this topic file and
// up to 3 other ready files. A question offers 4 options at most.
const preplanReadyMax = 4

// preplanReadyRows lists the topic files of a root for the ready list. It is
// dashboardPreplans, which reads only the first dashboardPreplanHeadMax bytes
// of each file. Tests replace it to make the list fail or warn.
var preplanReadyRows = dashboardPreplans

// preplanFolderNotReadPrefix starts the warning that dashboardPreplans gives
// for a preplan folder that cannot be listed.
// TestPlanSupportPreplanWriteFolderWarningPrefix pins it to dashboardPreplans.
const preplanFolderNotReadPrefix = "Preplan folder not read: "

// The next texts of preplan_write, one for each outcome. preplanWriteNext
// picks one.
const (
	// preplanWriteNextWritten is the next text when a section or the Status
	// line was written (also for the status in progress).
	preplanWriteNextWritten = "Continue the current preplan step. Read preplanFile only when you need its new state."
	// preplanWriteNextNoop is the next text of a call that wrote nothing.
	preplanWriteNextNoop = "Nothing changed: the section is absent and content is empty. Continue the current preplan step."
	// preplanWriteNextReady is the next text of topicStatus ready for plan.
	preplanWriteNextReady = "Go to the Step 5 handoff. Offer readyTopicFiles as the plan files. readyLeftOut more ready files are not in the list."
	// preplanWriteNextPaused is the next text of topicStatus paused.
	preplanWriteNextPaused = "The topic file is paused. End the preplan run. Run /sdlc:preplan with the same topic to continue."
)

// preplanWriteStatusPart is the entry of written for the Status line.
const preplanWriteStatusPart = "Status"

// preplanTitleRe matches the "# " title line of a topic file.
var preplanTitleRe = regexp.MustCompile(`(?m)^# `)

// preplanWriteDescription returns the preplan_write line of the plan_support
// tool description. The numbers come from the constants that the code uses,
// so the text cannot go stale; TestPreplanToolDescription pins the text.
func preplanWriteDescription() string {
	head := strconv.Itoa(dashboardPreplanHeadMax)
	return "- preplan_write: Replace one section body or the Status line of an existing topic file. " +
		"Requires topic, and section or topicStatus. Optional: content (with section only). " +
		"section, content and topicStatus sent with another action return DomainError. " +
		"It finds and reads the file as preplan_check does and never creates one. " +
		"It checks content against the topic-file format rules and refuses a body that breaks one, with the rule id. " +
		"Before the check it changes each CR LF in content to LF and removes the final line ends; the rules, the " + strconv.Itoa(preplanWriteContentMax) + "-byte limit of rule P1 included, check that value, and the file stores it with the line ends of the file. " +
		"It replaces the body under the first matching section heading, adds an absent Open questions section after Decisions, and deletes the Open questions or Guardrail check section when content is empty. " +
		"It replaces the first Status line in the first " + head + " bytes, else inserts one before the Started line, else after the title line, else as the first line. " +
		"summary names each extra heading or Status line that stays. " +
		"It writes a temp file and renames it, and the file keeps its mode. " +
		"topicStatus ready for plan also lists the preplan folder and reads the first " + head + " bytes of each other topic file there (the newest " + strconv.Itoa(dashboardPreplanLimit) + "). " +
		"It returns readyTopicFiles (this file first, then up to " + strconv.Itoa(preplanReadyMax-1) + " other ready files, newest first) and readyLeftOut. " +
		"A failed list or head read of another file does not fail the call: summary names it. " +
		"Returns preplanFile, written, readyTopicFiles, readyLeftOut, summary, next. " +
		"A bad input or a broken rule returns DomainError and writes nothing. " +
		"A missing file, a link or a file that is not regular returns DomainError. " +
		"Two matching files, a file over " + dashboardDeleteReadMaxText + ", a write that would make it larger than " + dashboardDeleteReadMaxText + ", or a section heading that is absent or hidden by an open fence returns DataError. " +
		"A list failure of the preplan folder, or a preplan folder that holds more than " + strconv.Itoa(preplanDirMax) + " names, returns InfraError. " +
		"An Lstat, read or write failure of the topic file returns InfraError and leaves the file as it was. " +
		"A dangling link at the preplan folder returns InfraError."
}

// preplanWriteFieldError returns a DomainError when an action other than
// preplan_write gets section, content or topicStatus, in that order, so that
// nothing runs. It returns nil for preplan_write and for a call with none of
// the three fields.
func preplanWriteFieldError(in PlanSupportIn) error {
	if in.Action == preplanWriteAction {
		return nil
	}
	for _, f := range []struct{ name, value string }{
		{"section", in.Section},
		{"content", in.Content},
		{"topicStatus", in.TopicStatus},
	} {
		if f.value != "" {
			return &mcpserver.DomainError{
				Msg:        fmt.Sprintf("%s: %s is a preplan_write field", in.Action, f.name),
				Suggestion: fmt.Sprintf("Remove %s, or use action preplan_write.", f.name),
			}
		}
	}
	return nil
}

// preplanWriteInputError checks the fields of a preplan_write call before
// any file is read: content needs section, the call writes something, the
// section and the status are known values, and content passes the format
// rules of the section (preplanCheckSection). The content rules, the P1 size
// limit (preplanWriteContentMax) included, check preplanWriteContent of
// content: the same value that the write stores. The combination rules
// (content needs section) read content as sent. Each error is a
// DomainError whose message starts with "preplan_write:"; a broken rule wraps
// the *preplanFormatError as its Cause.
func preplanWriteInputError(in PlanSupportIn) error {
	const action = preplanWriteAction
	var msg, suggestion string
	switch {
	case in.Section == "" && in.Content != "":
		msg, suggestion = "content needs section", "Add section with the name of the section to write, or remove content."
	case in.Section == "" && in.TopicStatus == "":
		msg, suggestion = "nothing to write", "Add section and content, or add topicStatus."
	case in.Section != "" && !slices.Contains(preplanWriteSections, in.Section):
		msg = fmt.Sprintf("section %q is not a topic-file section", in.Section)
		suggestion = fmt.Sprintf("Use one of the %d names in the section field description.", len(preplanWriteSections))
	case in.TopicStatus != "" && !slices.Contains(PreplanStatuses, in.TopicStatus):
		msg = fmt.Sprintf("topicStatus %q is not a preplan status", in.TopicStatus)
		suggestion = "Use " + preplanStatusInProgress + ", " + preplanStatusReadyForPlan + " or " + preplanStatusPaused + "."
	case in.Section != "":
		if ferr := preplanCheckSection(in.Section, preplanWriteContent(in.Content)); ferr != nil {
			return &mcpserver.DomainError{
				Msg:        action + ": " + ferr.Error(),
				Suggestion: "Fix the content and call preplan_write again with the full section body.",
				Cause:      ferr,
			}
		}
	}
	if msg == "" {
		return nil
	}
	return &mcpserver.DomainError{Msg: action + ": " + msg, Suggestion: suggestion}
}

// preplanEdit is the topic file text that preplan_write builds: the bytes,
// the parts that the call writes (a section name and/or "Status"), and the
// sentences of the summary.
type preplanEdit struct {
	data    []byte
	written []string
	notes   []string
}

// planPreplanWrite replaces one section body and/or the Status line of the
// existing topic file for in.Topic. The steps run in this order, and no step
// before step 4 writes. Every error message starts with "preplan_write:".
//
//  1. preplanWriteInputError: the fields and the content rules (DomainError).
//  2. preplanReadTopicFile: the slug, the one topic file, its checks and its
//     bytes, as for preplan_check.
//  3. preplanEdit.setSection, then preplanEdit.setStatus, on the bytes in
//     memory (a section that is not found: DataError).
//  4. preplanSaveTopic: the size check (DataError) and one write through
//     preplanWriteBytes (InfraError; the old file stays as it was). A call
//     whose written list is empty writes nothing.
//  5. For topicStatus ready for plan, preplanReadyList lists the ready topic
//     files. A failed list does not fail the call.
func planPreplanWrite(mainRoot string, in PlanSupportIn) (PlanSupportOut, error) {
	const action = preplanWriteAction
	if err := preplanWriteInputError(in); err != nil {
		return PlanSupportOut{}, err
	}
	topic, err := preplanReadTopicFile(action, mainRoot, in.Topic)
	if err != nil {
		return PlanSupportOut{}, err
	}
	content := preplanWriteContent(in.Content)
	edit := &preplanEdit{data: topic.data, written: []string{}}
	if in.Section != "" {
		if err := edit.setSection(topic.file, in.Section, content); err != nil {
			return PlanSupportOut{}, err
		}
	}
	if in.TopicStatus != "" {
		edit.setStatus(in.TopicStatus)
	}
	if len(edit.written) > 0 {
		if err := preplanSaveTopic(action, topic, edit.data, preplanWriteLargeSuggestion(content)); err != nil {
			return PlanSupportOut{}, err
		}
	}
	if _, openFence := preplanSectionSpans(edit.data); openFence != "" {
		edit.notes = append(edit.notes, fmt.Sprintf("A %s fence is still open at the end of the file, so each ## heading after it counts as absent.", openFence))
	}
	out := PlanSupportOut{PreplanFile: topic.file, Written: &edit.written}
	if in.TopicStatus == preplanStatusReadyForPlan {
		files, leftOut, notes := preplanReadyList(mainRoot, topic.file)
		out.ReadyTopicFiles, out.ReadyLeftOut = &files, &leftOut
		edit.notes = append(edit.notes, notes...)
	}
	out.Summary = strings.Join(append([]string{"Topic file " + topic.name + "."}, edit.notes...), " ")
	out.Next = preplanWriteNext(in.TopicStatus, edit.written)
	return out, nil
}

// preplanWriteContent returns content in the form that preplan_write checks
// and stores: each CR LF changed to LF, and the final line ends removed.
// preplanBodyText then gives each line the line end of the file.
func preplanWriteContent(content string) string {
	return strings.TrimRight(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
}

// preplanSaveTopic writes data over the topic file through preplanWriteBytes.
// data larger than dashboardDeleteReadMax is a DataError with the Suggestion
// large, and nothing is written: the next read would refuse the file. A write
// failure is the InfraError of preplanWriteFailure.
func preplanSaveTopic(action string, topic preplanFoundTopic, data []byte, large string) error {
	if len(data) > dashboardDeleteReadMax {
		return &mcpserver.DataError{
			Msg:        fmt.Sprintf("%s: %s would be %d bytes, more than %d", action, topic.file, len(data), dashboardDeleteReadMax),
			Suggestion: large,
		}
	}
	if err := preplanWriteBytes(topic.file, data); err != nil {
		return preplanWriteFailure(action, topic.dir, err)
	}
	return nil
}

// preplanWriteLargeSuggestion returns the Suggestion of a write that would
// make the topic file larger than dashboardDeleteReadMax. With content, a
// shorter content fixes it. Without content (a Status write, or an empty
// content), only a smaller file fixes it.
func preplanWriteLargeSuggestion(content string) string {
	if content != "" {
		return "Shorten the content" + preplanWriteAgain
	}
	return "Make the topic file smaller than " + dashboardDeleteReadMaxText + " by hand" + preplanWriteAgain
}

// preplanWriteNext returns the next text of a successful preplan_write call.
// The first match wins: topicStatus ready for plan, topicStatus paused, a
// written list that is not empty, else the no-op text.
func preplanWriteNext(status string, written []string) string {
	switch {
	case status == preplanStatusReadyForPlan:
		return preplanWriteNextReady
	case status == preplanStatusPaused:
		return preplanWriteNextPaused
	case len(written) > 0:
		return preplanWriteNextWritten
	}
	return preplanWriteNextNoop
}

// setSection sets the body of section to content, in the form of
// preplanWriteContent, in e.data. It replaces the
// body under the first "## <section>" heading outside a fence. An empty
// content deletes an Open questions or Guardrail check section, and on such a
// section that is absent it changes nothing. An absent Open questions section
// goes after the Decisions section. Any other absent section, or an insert
// place inside a fence that is still open, is a DataError that names file.
// The section name goes into e.written when it replaces, adds or deletes a
// section: a replace with the same body still counts as written. When the
// target is the last section and a fence is still open in its body, the
// replace or the delete runs to the end of the file, and a note says so.
func (e *preplanEdit) setSection(file, section, content string) error {
	spans, openFence := preplanSectionSpans(e.data)
	eol := preplanEOL(e.data, preplanLineEnd(e.data, 0))
	deletable := section == preplanOpenQuestionsSection || section == preplanGuardrailCheckSection
	i := preplanFindSpan(spans, section)
	// The last span holds the fence that is still open: its body runs to the
	// end of the file, over every heading that the fence hides.
	fenceInBody := i >= 0 && openFence != "" && spans[i].bodyEnd == len(e.data)
	switch {
	case i < 0 && deletable && content == "":
		e.notes = append(e.notes, fmt.Sprintf("Nothing changed in ## %s: the section is absent and content is empty.", section))
		return nil
	case i < 0 && section == preplanOpenQuestionsSection:
		return e.addOpenQuestions(file, spans, openFence, content)
	case i < 0:
		return preplanSectionNotFound(file, section, preplanNotFoundSuggestion)
	case deletable && content == "":
		e.data = slices.Concat(e.data[:spans[i].lineStart], e.data[spans[i].bodyEnd:])
		e.notes = append(e.notes, fmt.Sprintf("Deleted the ## %s section.", section))
		if fenceInBody {
			e.notes = append(e.notes, "The old body held a code fence that never closed, so the write deleted the text up to the end of the file.")
		}
	default:
		s := spans[i]
		body := preplanBodyText(e.data[:s.bodyStart], content, eol, s.bodyEnd < len(e.data))
		e.data = slices.Concat(e.data[:s.bodyStart], []byte(body), e.data[s.bodyEnd:])
		e.notes = append(e.notes, fmt.Sprintf("Replaced the body of ## %s.", section))
		if fenceInBody {
			e.notes = append(e.notes, "The old body held a code fence that never closed, so the write replaced the text up to the end of the file.")
		}
	}
	if extra := preplanSpanCount(spans, section) - 1; extra > 0 {
		e.notes = append(e.notes, fmt.Sprintf("%d more \"## %s\" headings stay in the file.", extra, section))
	}
	e.written = append(e.written, section)
	return nil
}

// addOpenQuestions inserts an Open questions section with content after the
// body of the first Decisions section. No Decisions section, or a Decisions
// section that is the last one while a fence is still open (the insert would
// be code), is the DataError of preplanSectionNotFound.
func (e *preplanEdit) addOpenQuestions(file string, spans []preplanSpan, openFence, content string) error {
	k := preplanFindSpan(spans, preplanDecisionsSection)
	if k < 0 {
		return preplanSectionNotFound(file, preplanDecisionsSection, preplanNotFoundSuggestion)
	}
	at := spans[k].bodyEnd
	if openFence != "" && at == len(e.data) {
		return preplanSectionNotFound(file, preplanOpenQuestionsSection, preplanFencedSuggestion)
	}
	eol := preplanEOL(e.data, preplanLineEnd(e.data, 0))
	before := string(e.data[:at])
	heading := preplanSpacing(before, eol) + preplanHeadingPrefix + preplanOpenQuestionsSection + eol
	insert := heading + preplanBodyText([]byte(before+heading), content, eol, at < len(e.data))
	e.data = slices.Concat(e.data[:at], []byte(insert), e.data[at:])
	e.notes = append(e.notes, fmt.Sprintf("Added the ## %s section after ## %s.", preplanOpenQuestionsSection, preplanDecisionsSection))
	e.written = append(e.written, preplanOpenQuestionsSection)
	return nil
}

// preplanBodyText returns the text that goes after a heading line: a blank
// line, content (already in the form of preplanWriteContent) with each line
// end made eol, one line end, and one more blank line when more follows
// (more). An empty content gives one blank line only. before is the text up to the body: when
// it does not end with a line feed (a heading with no line end), the text
// starts with eol to end the heading line.
func preplanBodyText(before []byte, content, eol string, more bool) string {
	var b strings.Builder
	if len(before) > 0 && !bytes.HasSuffix(before, []byte("\n")) {
		b.WriteString(eol)
	}
	b.WriteString(eol)
	if content == "" {
		return b.String()
	}
	b.WriteString(strings.ReplaceAll(content, "\n", eol) + eol)
	if more {
		b.WriteString(eol)
	}
	return b.String()
}

// preplanSpanCount returns the number of spans named name.
func preplanSpanCount(spans []preplanSpan, name string) int {
	n := 0
	for _, s := range spans {
		if s.name == name {
			n++
		}
	}
	return n
}

// The Suggestions of a section that preplan_write cannot place.
const (
	// preplanNotFoundSuggestion is the Suggestion of a skeleton heading that
	// is absent or hidden by a fence that is still open.
	preplanNotFoundSuggestion = "Call plan_support preplan_context with the same topic. It adds the heading and closes the fence."
	// preplanFencedSuggestion is the Suggestion of an Open questions section
	// whose place, after the last section Decisions, is inside a fence that
	// is still open.
	preplanFencedSuggestion = "Call plan_support preplan_context with the same topic. It adds an absent skeleton heading and closes an open fence. Then call preplan_write again with the same section and content."
)

// preplanSectionNotFound returns the DataError of a section that is absent
// from file or that has no place outside a fence that is still open, with
// the given Suggestion.
func preplanSectionNotFound(file, section, suggestion string) error {
	return &mcpserver.DataError{
		Msg:        fmt.Sprintf("%s: section %q not found in %s", preplanWriteAction, section, file),
		Suggestion: suggestion,
	}
}

// setStatus sets the Status line of e.data to status. It replaces the first
// Status line in the first dashboardPreplanHeadMax bytes
// (dashboardReplaceStatusLine), else inserts one (preplanInsertStatus). The
// same status as now still counts as written: the write is a full-value
// write, so a repeated call gives the same file.
func (e *preplanEdit) setStatus(status string) {
	e.written = append(e.written, preplanWriteStatusPart)
	extra := len(dashboardPreplanStatusRe.FindAllIndex(preplanHead(e.data), -1)) - 1
	next, current, found := dashboardReplaceStatusLine(e.data, status)
	if !found {
		var note string
		e.data, note = preplanInsertStatus(e.data, status)
		e.notes = append(e.notes, note)
		return
	}
	e.data = next
	e.notes = append(e.notes, fmt.Sprintf("Set the Status line to %s. Old status: %s.", status, current))
	if extra > 0 {
		e.notes = append(e.notes, fmt.Sprintf("%d more Status lines stay in the file.", extra))
	}
}

// preplanInsertStatus returns data with a new "**Status:** <status>" line and
// the summary sentence that names its place: before the Started line in the
// first dashboardPreplanHeadMax bytes, else after the first "# " title line
// there, else as the first line. The new line uses the first line end of
// data. Every other byte stays the same.
func preplanInsertStatus(data []byte, status string) (next []byte, note string) {
	eol := preplanEOL(data, preplanLineEnd(data, 0))
	line := dashboardPreplanStatusPrefix + " " + status
	head := preplanHead(data)
	if loc := dashboardPreplanStartedRe.FindIndex(head); loc != nil {
		return slices.Concat(data[:loc[0]], []byte(line+eol+eol), data[loc[0]:]),
			"Inserted the Status line " + status + " before the Started line."
	}
	if loc := preplanTitleRe.FindIndex(head); loc != nil {
		at, insert := len(data), eol+eol+line+eol
		if nl := preplanLineEnd(data, loc[0]); nl < len(data) {
			at, insert = nl+1, eol+line+eol
		}
		return slices.Concat(data[:at], []byte(insert), data[at:]),
			"Inserted the Status line " + status + " after the title line."
	}
	insert := line + eol
	if len(data) > 0 {
		insert += eol
	}
	return slices.Concat([]byte(insert), data), "Inserted the Status line " + status + " as the first line."
}

// preplanReadyList returns readyTopicFiles and readyLeftOut for file, the
// topic file under mainRoot that the call set to ready for plan, and the
// summary sentences of the list. file is always first, also when the list
// has no row for it. The other rows with status ready for plan follow in the
// newest-first order of preplanReadyRows, up to preplanReadyMax entries in
// all; leftOut counts the ready rows that do not fit. A folder that cannot be
// listed gives "Ready topic files not listed: <error>." and every other
// warning of preplanReadyRows goes into the notes as it is.
func preplanReadyList(mainRoot, file string) (files []string, leftOut int, notes []string) {
	files = []string{file}
	self := strings.TrimSuffix(filepath.Base(file), ".md")
	rows, warnings := preplanReadyRows(mainRoot)
	for _, w := range warnings {
		if rest, ok := strings.CutPrefix(w, preplanFolderNotReadPrefix); ok {
			notes = append(notes, "Ready topic files not listed: "+rest+".")
			continue
		}
		notes = append(notes, w+".")
	}
	for _, row := range rows {
		if row.Status != preplanStatusReadyForPlan || row.Slug == self {
			continue
		}
		if len(files) < preplanReadyMax {
			files = append(files, filepath.Join(mainRoot, filepath.FromSlash(row.Path)))
			continue
		}
		leftOut++
	}
	return files, leftOut, notes
}
