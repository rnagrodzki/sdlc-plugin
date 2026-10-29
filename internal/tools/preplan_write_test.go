package tools

import (
	"errors"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// This file holds preplan_write tests that the tables of plan_support_test.go
// do not cover: the folder list errors, full summary texts, three repeats of
// a heading or a Status line, the Open questions insert on edge files, the
// cause of a write error, and the order of the write fields on other actions.

// preplanCRLF returns s with each LF changed to CR LF.
func preplanCRLF(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

// TestPreplanWriteFolderListErrors checks that preplan_write returns the
// InfraError of preplanMatches for a preplan folder that cannot be listed and
// for a folder with more than preplanDirMax names, and writes nothing. A
// folder with exactly preplanDirMax names is listed.
func TestPreplanWriteFolderListErrors(t *testing.T) {
	t.Run("a regular file in place of the data folder", func(t *testing.T) {
		noPreplanCheckWrite(t)
		root := t.TempDir()
		blocker := filepath.Join(root, ".sdlc-v2")
		writeFile(t, blocker, "not a directory\n")
		dir := filepath.Join(blocker, "preplan")
		_, err := planSupportCore(root, root, preplanWriteIn("Goal", "Ship it.", ""))
		if got := errorClassOf(err); got != "infra" {
			t.Fatalf("error class = %q, want infra (err %v)", got, err)
		}
		// The text of the os error differs by platform, so only the prefix
		// is pinned.
		if msg := err.Error(); !strings.HasPrefix(msg, "preplan_write: list: ") {
			t.Errorf("err.Error() = %q, want prefix %q", msg, "preplan_write: list: ")
		}
		want := "Make " + dir + " a folder that you can read: remove or rename the file or link at that path, or give your user read permission. Then run /sdlc:preplan again."
		if got := suggestionOf(err); got != want {
			t.Errorf("Suggestion = %q, want %q", got, want)
		}
		var pathErr *fs.PathError
		if !errors.As(err, &pathErr) {
			t.Errorf("err = %v, want it to wrap the list error (*fs.PathError)", err)
		}
		wantPreplanBytes(t, blocker, "not a directory\n")
	})

	t.Run("a preplan folder with more than preplanDirMax names", func(t *testing.T) {
		noPreplanCheckWrite(t)
		swapPreplanSeam(t, &preplanDirMax, 2)
		root := t.TempDir()
		file := writePreplanTopic(t, root, "auth-flow.md", preplanWriteSkeleton)
		writePreplanTopic(t, root, "b.md", "# Preplan: b\n")
		writePreplanTopic(t, root, "c.md", "# Preplan: c\n")
		dir := filepath.Dir(file)
		_, err := planSupportCore(root, root, preplanWriteIn("Goal", "Ship it.", ""))
		wantPreplanErr(t, err, preplanErrWant{"infra",
			"preplan_write: list " + dir + ": the folder holds more than 2 names",
			"Move old topic files out of " + dir + " until it holds at most 2 names. Then run /sdlc:preplan again."})
		if !errors.Is(err, errPreplanDirFull) {
			t.Errorf("err = %v, want it to wrap errPreplanDirFull", err)
		}
		wantPreplanBytes(t, file, preplanWriteSkeleton)
	})

	t.Run("a preplan folder with exactly preplanDirMax names", func(t *testing.T) {
		swapPreplanSeam(t, &preplanDirMax, 2)
		root := t.TempDir()
		file := writePreplanTopic(t, root, "auth-flow.md", preplanWriteSkeleton)
		writePreplanTopic(t, root, "b.md", "# Preplan: b\n")
		out, err := planSupportCore(root, root, preplanWriteIn("Goal", "Ship it.", ""))
		if err != nil {
			t.Fatalf("preplan_write: %v, want success", err)
		}
		if out.Written == nil || !reflect.DeepEqual(*out.Written, []string{"Goal"}) {
			t.Errorf("Written = %v, want [Goal]", out.Written)
		}
		wantPreplanBytes(t, file, strings.Replace(preplanWriteSkeleton, "## Goal\n\n", "## Goal\n\nShip it.\n\n", 1))
	})
}

// preplanWriteFullCase is one call of preplan_write on a topic file and the
// exact file and summary after it. ready, when set, is what the
// preplanReadyRows seam returns.
type preplanWriteFullCase struct {
	name, before string
	in           PlanSupportIn
	after        string
	written      []string
	summary      string
	ready        *[]dashboardPreplanWarning
}

// runPreplanWriteFullCases runs each case in a new root and checks the file
// bytes, written and the full summary.
func runPreplanWriteFullCases(t *testing.T, cases []preplanWriteFullCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := writePreplanTopic(t, root, "auth-flow.md", tc.before)
			if tc.ready != nil {
				warnings := *tc.ready
				swapPreplanSeam(t, &preplanReadyRows, func(string) ([]DashboardPreplan, []dashboardPreplanWarning) {
					return []DashboardPreplan{}, warnings
				})
			}
			out, err := planSupportCore(root, root, tc.in)
			if err != nil {
				t.Fatalf("preplan_write: %v", err)
			}
			wantPreplanBytes(t, file, tc.after)
			if out.Written == nil || !reflect.DeepEqual(*out.Written, tc.written) {
				t.Errorf("Written = %v, want %q", out.Written, tc.written)
			}
			if out.Summary != tc.summary {
				t.Errorf("Summary =\n%q\nwant\n%q", out.Summary, tc.summary)
			}
		})
	}
}

// preplanWriteFolderWarning is a folder warning of the preplanReadyRows seam.
var preplanWriteFolderWarning = []dashboardPreplanWarning{{kind: dashboardPreplanWarnFolder,
	text: preplanFolderNotReadPrefix + "open x: permission denied", detail: "open x: permission denied"}}

// TestPreplanWriteSummaryFullText checks the full summary of a section edit,
// a Status edit and a ready list, also where an outcome comes with a warning
// or a second outcome.
func TestPreplanWriteSummaryFullText(t *testing.T) {
	addedOpen := preplanWriteSkeleton + "\n## Open questions\n\n- Which tab?\n"
	runPreplanWriteFullCases(t, []preplanWriteFullCase{
		{name: "section: Open questions added", before: preplanWriteSkeleton,
			in: preplanWriteIn("Open questions", "- Which tab?", ""), after: addedOpen, written: []string{"Open questions"},
			summary: "Topic file auth-flow.md. Added the ## Open questions section after ## Decisions."},
		{name: "section: Open questions deleted", before: addedOpen,
			in: preplanWriteIn("Open questions", "", ""), after: preplanWriteSkeleton + "\n", written: []string{"Open questions"},
			summary: "Topic file auth-flow.md. Deleted the ## Open questions section. The old body had 1 line of text."},
		{name: "section and status: Decisions replaced and status paused", before: preplanWriteSkeleton,
			in:    preplanWriteIn("Decisions", preplanWriteRowsA, preplanStatusPaused),
			after: strings.Replace(preplanWriteSkeletonHead, "in progress", "paused", 1) + preplanWriteRowsA + "\n", written: []string{"Decisions", "Status"},
			summary: "Topic file auth-flow.md. Replaced the body of ## Decisions. The old body had 2 lines of text. " +
				"Set the Status line to paused. Old status: in progress."},
		{name: "section: Goal replaced before a fence that never closes", before: preplanParityFenceFile,
			in:    preplanWriteIn("Goal", "Ship it.", ""),
			after: strings.Replace(preplanParityFenceFile, "## Goal\n\n", "## Goal\n\nShip it.\n\n", 1), written: []string{"Goal"},
			summary: "Topic file auth-flow.md. Replaced the body of ## Goal. The old body had 0 lines of text. " +
				"A ``` fence is still open at the end of the file, so each ## heading after it counts as absent."},
		{name: "status: two Status lines", before: preplanWriteStatusLine + "\n" + preplanWriteStatusLine + "\n",
			in: preplanWriteIn("", "", preplanStatusInProgress), after: "**Status:** in progress\n" + preplanWriteStatusLine + "\n", written: []string{"Status"},
			summary: "Topic file auth-flow.md. Set the Status line to in progress. Old status: paused. 1 more Status lines stay in the file."},
		{name: "status: inserted before the Started line", before: "# Preplan: auth flow\n\n" + preplanWriteStartedLine + "\n## Goal\n",
			in:    preplanWriteIn("", "", preplanStatusInProgress),
			after: "# Preplan: auth flow\n\n**Status:** in progress\n\n" + preplanWriteStartedLine + "\n## Goal\n", written: []string{"Status"},
			summary: "Topic file auth-flow.md. Inserted the Status line in progress before the Started line."},
		{name: "ready list: the folder list fails after the write", before: preplanWriteSkeleton,
			in:    preplanWriteIn("", "", preplanStatusReadyForPlan),
			after: strings.Replace(preplanWriteSkeleton, "in progress", preplanStatusReadyForPlan, 1), written: []string{"Status"},
			summary: "Topic file auth-flow.md. Set the Status line to ready for plan. Old status: in progress. Ready topic files not listed: open x: permission denied.",
			ready:   &preplanWriteFolderWarning},
	})
}

// TestPreplanWriteThreeRepeats checks the "N more stay" counts with three
// copies: three ## Decisions headings far apart, with one more copy in a
// closed fence that does not count, and three Status lines in the head.
func TestPreplanWriteThreeRepeats(t *testing.T) {
	const rest = "\n## Decisions\n\nold2\n\n## Notes\n\n```\n## Decisions\n```\n\n## Decisions\n\nold3\n"
	runPreplanWriteFullCases(t, []preplanWriteFullCase{
		{name: "three ## Decisions headings far apart", before: preplanWriteSkeleton + rest,
			in:    preplanWriteIn("Decisions", preplanWriteRowsA, ""),
			after: preplanWriteSkeletonHead + preplanWriteRowsA + "\n" + rest, written: []string{"Decisions"},
			summary: "Topic file auth-flow.md. Replaced the body of ## Decisions. The old body had 2 lines of text. " +
				`2 more "## Decisions" headings stay in the file.`},
		{name: "three Status lines in the head",
			before: "**Status:** paused\n\n## Goal\n\nx\n\n**Status:** paused\n\n## Notes\n\n**Status:** paused\n",
			in:     preplanWriteIn("", "", preplanStatusInProgress),
			after:  "**Status:** in progress\n\n## Goal\n\nx\n\n**Status:** paused\n\n## Notes\n\n**Status:** paused\n", written: []string{"Status"},
			summary: "Topic file auth-flow.md. Set the Status line to in progress. Old status: paused. 2 more Status lines stay in the file."},
	})
}

// TestPreplanWriteOpenQuestionsEdgeFiles checks the insert of an absent Open
// questions section in a file with no final line feed, in a CR LF file, and
// after a Decisions heading that is the last line with no line end.
func TestPreplanWriteOpenQuestionsEdgeFiles(t *testing.T) {
	const added = "Topic file auth-flow.md. Added the ## Open questions section after ## Decisions."
	written := []string{"Open questions"}
	runPreplanWriteFullCases(t, []preplanWriteFullCase{
		{name: "the file has no final line feed", before: preplanWriteNoEOL,
			in:    preplanWriteIn("Open questions", "- Which tab?", ""),
			after: preplanWriteNoEOL + "\n\n## Open questions\n\n- Which tab?\n", written: written, summary: added},
		{name: "a CR LF file", before: preplanCRLF(preplanWriteSkeleton),
			in:    preplanWriteIn("Open questions", "- a\n- b", ""),
			after: preplanCRLF(preplanWriteSkeleton) + "\r\n## Open questions\r\n\r\n- a\r\n- b\r\n", written: written, summary: added},
		{name: "a CR LF file with a section after Decisions", before: preplanCRLF(preplanWriteSkeleton + "\n## Notes\n\nx\n"),
			in:    preplanWriteIn("Open questions", "- a", ""),
			after: preplanCRLF(preplanWriteSkeleton + "\n## Open questions\n\n- a\n\n## Notes\n\nx\n"), written: written, summary: added},
		{name: "the Decisions heading is the last line with no line end", before: "# Preplan: auth flow\n\n## Decisions",
			in:    preplanWriteIn("Open questions", "- a", ""),
			after: "# Preplan: auth flow\n\n## Decisions\n\n## Open questions\n\n- a\n", written: written, summary: added},
	})
}

// TestPreplanWriteWriteErrorCause checks that a failed write of the topic
// file is an InfraError that wraps the write error, and that the file and
// the output stay as they were.
func TestPreplanWriteWriteErrorCause(t *testing.T) {
	root := t.TempDir()
	file := writePreplanTopic(t, root, "auth-flow.md", preplanWriteSkeleton)
	swapPreplanSeam(t, &preplanWriteBytes, func(string, []byte) error { return errInjectedPreplan })
	out, err := planSupportCore(root, root, preplanWriteIn("Goal", "Ship it.", ""))
	if got := errorClassOf(err); got != "infra" {
		t.Fatalf("error class = %q, want infra (err %v)", got, err)
	}
	if want := "preplan_write: " + errInjectedPreplan.Error(); err.Error() != want {
		t.Errorf("err.Error() = %q, want %q", err.Error(), want)
	}
	if !errors.Is(err, errInjectedPreplan) {
		t.Errorf("err = %v, want it to wrap errInjectedPreplan", err)
	}
	if !reflect.DeepEqual(out, PlanSupportOut{}) {
		t.Errorf("output = %+v, want empty on a failed write", out)
	}
	wantPreplanBytes(t, file, preplanWriteSkeleton)
}

// TestPreplanWriteFieldOrder checks the order of the write fields on another
// action (section, then content, then topicStatus), that content of line
// ends only or an empty content counts as sent there, and that content of
// line ends only with no section is "content needs section" on preplan_write.
func TestPreplanWriteFieldOrder(t *testing.T) {
	// Each row sends content, also as an empty string.
	for _, tc := range []struct {
		name, field                  string
		section, content, topicState string
	}{
		{"all three fields: section first", "section", "Goal", "x", preplanStatusPaused},
		{"content and topicStatus: content first", "content", "", "x", preplanStatusPaused},
		{"content of line ends only", "content", "", "\n\r\n", ""},
		{"an empty content", "content", "", "", ""},
	} {
		t.Run("preplan_check: "+tc.name, func(t *testing.T) {
			root := t.TempDir()
			in := preplanCheckIn("auth flow", 1, 0, 0)
			in.Section, in.Content, in.TopicStatus = tc.section, preplanContentPtr(tc.content), tc.topicState
			_, err := planSupportCore(root, root, in)
			wantPreplanErr(t, err, preplanErrWant{"domain", preplanCheckAction + ": " + tc.field + " is a preplan_write field",
				"Remove " + tc.field + ", or use action preplan_write."})
			if names := preplanDirNames(t, filepath.Join(root, ".sdlc-v2", "preplan")); len(names) != 0 {
				t.Errorf("preplan folder holds %v, want nothing: the action must not run", names)
			}
		})
	}
	for _, tc := range []struct{ name, content, status string }{
		{"line ends only, no section, no topicStatus", "\n\n", ""},
		{"CR LF only, no section, with topicStatus", "\r\n", preplanStatusPaused},
	} {
		t.Run("preplan_write: "+tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := writePreplanTopic(t, root, "auth-flow.md", preplanWriteSkeleton)
			writes := countPreplanWrites(t)
			_, err := planSupportCore(root, root, preplanWriteIn("", tc.content, tc.status))
			wantPreplanErr(t, err, preplanErrWant{"domain", "preplan_write: content needs section",
				"Add section with the name of the section to write, or remove content."})
			if *writes != 0 {
				t.Errorf("writes = %d, want 0", *writes)
			}
			wantPreplanBytes(t, file, preplanWriteSkeleton)
		})
	}
}
