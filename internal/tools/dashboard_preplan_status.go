package tools

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// DashboardPreplanStatusOut is the 200 body of POST /api/preplan-status.
type DashboardPreplanStatusOut struct {
	Changed bool   `json:"changed"` // true when this call rewrote the Status line
	Status  string `json:"status"`  // the status that the topic file has after the call
	Message string `json:"message"` // one sentence for the page
}

// Suggestion texts of the preplan status errors.
const (
	dashboardPreplanStatusGoneSuggestion   = "Reload the page. The topic file is gone."
	dashboardPreplanStatusLinkSuggestion   = "Change the status by hand in the target file."
	dashboardPreplanStatusNoLineSuggestion = "Add a **Status:** line near the top of the topic file by hand."
	dashboardPreplanStatusLargeSuggestion  = "Make the topic file smaller than " + dashboardDeleteReadMaxText + " by hand, then retry."
	dashboardPreplanStatusWriteSuggestion  = "Check write permission on .sdlc-v2/preplan/ and free disk space, then retry."
)

// dashboardPreplanStatusPrefix is the text that starts the Status line.
const dashboardPreplanStatusPrefix = "**Status:**"

// DashboardSetPreplanStatus replaces the "**Status:**" line of the topic file
// <slug>.md in the preplan folder of root with the given status. The slug is a
// file name without the ".md" suffix (see dashboardPreplanSlugError). The
// status is one of PreplanStatuses. Only the first Status line, found in the
// first dashboardPreplanHeadMax bytes, changes: every other byte of the file
// stays the same, and a line that ends in CR LF keeps its CR.
//
// The same status as the file has gives Changed false and no write. Else the
// new bytes go through dashboardDeleteWriteBytes, a temp file and a rename, so
// the topic file has mode 0600 after the call. The rename replaces a regular
// file only. A symlink is refused, so the link and its target stay. No version
// check runs: when two writes race, the last write wins.
//
// Errors: a bad slug, a status that is not in PreplanStatuses, a missing topic
// file, and a topic file that is a symlink, a folder or another non-regular
// file give a DomainError. A file of more than dashboardDeleteReadMax bytes,
// and a file with no Status line in its first 4 KiB, give a DataError. A
// failed Lstat, read or write gives an InfraError.
func DashboardSetPreplanStatus(root, slug, status string) (DashboardPreplanStatusOut, error) {
	if err := dashboardPreplanSlugError(slug); err != nil {
		return DashboardPreplanStatusOut{}, err
	}
	if !slices.Contains(PreplanStatuses, status) {
		return DashboardPreplanStatusOut{}, &mcpserver.DomainError{
			Msg: fmt.Sprintf("The status %q is not a preplan status. The values are: %s",
				status, strings.Join(PreplanStatuses, ", ")),
			Suggestion: "Pick one of: " + strings.Join(PreplanStatuses, ", ") + ".",
		}
	}

	rel := filepath.ToSlash(filepath.Join(paths.DataDir, paths.PreplanSubdir, slug+".md"))
	path := filepath.Join(root, paths.DataDir, paths.PreplanSubdir, slug+".md")
	readSuggestion := "Check read permission on " + rel + " and retry."
	gone := &mcpserver.DomainError{
		Msg:        fmt.Sprintf("The preplan topic %s is gone", rel),
		Suggestion: dashboardPreplanStatusGoneSuggestion,
	}

	if err := dashboardPreplanTopicFileError(path, rel, readSuggestion, gone); err != nil {
		return DashboardPreplanStatusOut{}, err
	}
	data, missing, err := dashboardReadCapped(path, "the topic file",
		dashboardPreplanStatusLargeSuggestion, readSuggestion)
	if err != nil {
		return DashboardPreplanStatusOut{}, err
	}
	if missing {
		return DashboardPreplanStatusOut{}, gone
	}

	next, current, found := dashboardReplaceStatusLine(data, status)
	if !found {
		return DashboardPreplanStatusOut{}, &mcpserver.DataError{
			Msg:        fmt.Sprintf("The topic file %s has no **Status:** line in its first %d KiB", rel, dashboardPreplanHeadMax>>10),
			Suggestion: dashboardPreplanStatusNoLineSuggestion,
		}
	}
	if current == status {
		return DashboardPreplanStatusOut{
			Status:  status,
			Message: fmt.Sprintf("The preplan topic %s already has the status %s.", slug, status),
		}, nil
	}

	if err := dashboardDeleteWriteBytes(path, next); err != nil {
		return DashboardPreplanStatusOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("Could not write the preplan topic %s: %s", rel, err),
			Suggestion: dashboardPreplanStatusWriteSuggestion,
			Cause:      err,
		}
	}
	return DashboardPreplanStatusOut{
		Changed: true,
		Status:  status,
		Message: fmt.Sprintf("Set the status of the preplan topic %s to %s.", slug, status),
	}, nil
}

// dashboardPreplanTopicFileError returns nil when path, named rel in a
// message, is a regular file. A missing file gives gone. A symlink, a folder
// or another non-regular file gives a DomainError that names the kind. A
// failed Lstat gives an InfraError with readSuggestion.
func dashboardPreplanTopicFileError(path, rel, readSuggestion string, gone error) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return gone
		}
		return &mcpserver.InfraError{
			Msg:        fmt.Sprintf("Could not read the preplan topic %s: %s", rel, err),
			Suggestion: readSuggestion,
			Cause:      err,
		}
	}
	if info.Mode().IsRegular() {
		return nil
	}
	return &mcpserver.DomainError{
		Msg:        fmt.Sprintf("The path %s is %s, not a topic file that the dashboard can rewrite", rel, preplanFileKind(info.Mode())),
		Suggestion: dashboardPreplanStatusLinkSuggestion,
	}
}

// preplanFileKind names the kind of a topic file path that is not a regular
// file: "a symlink", "a folder", or "not a regular file" for any other mode.
// The dashboard and preplan_check use it, so both name a kind the same way.
func preplanFileKind(mode fs.FileMode) string {
	switch {
	case mode&fs.ModeSymlink != 0:
		return "a symlink"
	case mode.IsDir():
		return "a folder"
	}
	return "not a regular file"
}

// dashboardReplaceStatusLine replaces the first "**Status:**" line in the
// first dashboardPreplanHeadMax bytes of data with "**Status:** <status>".
// current is the old status, trimmed. found is false when the head has no
// Status line; then next and current are empty. See replacePreplanHeadLine.
func dashboardReplaceStatusLine(data []byte, status string) (next []byte, current string, found bool) {
	next, old, found := replacePreplanHeadLine(data, dashboardPreplanStatusRe, dashboardPreplanStatusPrefix+" "+status)
	if !found {
		return nil, "", false
	}
	return next, strings.TrimSpace(strings.TrimPrefix(old, dashboardPreplanStatusPrefix)), true
}

// preplanHead returns the first dashboardPreplanHeadMax bytes of data, the
// part of a topic file where its anchor lines are searched.
func preplanHead(data []byte) []byte {
	if len(data) > dashboardPreplanHeadMax {
		return data[:dashboardPreplanHeadMax]
	}
	return data
}

// preplanLineEnd returns the offset of the line feed that ends the line that
// starts at start, or len(data) when that line has no line feed. The topic
// file helpers (replacePreplanHeadLine, setGuardrailCheckLine,
// preplanSectionSpans, preplanHeadEnd, addPreplanHeadings) share it, so each
// one treats a last line with no line end the same way.
func preplanLineEnd(data []byte, start int) int {
	if i := bytes.IndexByte(data[start:], '\n'); i >= 0 {
		return start + i
	}
	return len(data)
}

// preplanEOL returns the line end of the line whose line feed is at nl, an
// offset from preplanLineEnd: "\r\n" when a CR comes before the line feed,
// else "\n". It returns "\n" when nl is len(data), a line with no line end.
func preplanEOL(data []byte, nl int) string {
	if nl > 0 && nl < len(data) && data[nl-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// replacePreplanHeadLine replaces the first line that re matches in the first
// dashboardPreplanHeadMax bytes of data with replacement, a line with no line
// end. old is the replaced line with no line end. found is false when the
// head has no match; then next and old are empty. The whole old line goes,
// also when it runs past the head. A line that ends in CR LF keeps its CR.
// Every other byte stays the same. next is a new slice: data does not change.
// re must match at the start of a line.
func replacePreplanHeadLine(data []byte, re *regexp.Regexp, replacement string) (next []byte, old string, found bool) {
	loc := re.FindIndex(preplanHead(data))
	if loc == nil {
		return nil, "", false
	}

	// The match can stop at the end of head, inside a long line. Take the whole
	// line from the full data, so no tail of the old line stays.
	start := loc[0]
	end := preplanLineEnd(data, start)
	line := data[start:end]
	if bytes.HasSuffix(line, []byte("\r")) {
		replacement += "\r"
		line = line[:len(line)-1]
	}
	next = make([]byte, 0, len(data)-(end-start)+len(replacement))
	next = append(next, data[:start]...)
	next = append(next, replacement...)
	next = append(next, data[end:]...)
	return next, string(line), true
}
