package tools

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return DashboardPreplanStatusOut{}, gone
		}
		return DashboardPreplanStatusOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("Could not read the preplan topic %s: %s", rel, err),
			Suggestion: readSuggestion,
			Cause:      err,
		}
	}
	if !info.Mode().IsRegular() {
		kind := "not a regular file"
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			kind = "a symlink"
		case info.IsDir():
			kind = "a folder"
		}
		return DashboardPreplanStatusOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("The path %s is %s, not a topic file that the dashboard can rewrite", rel, kind),
			Suggestion: dashboardPreplanStatusLinkSuggestion,
		}
	}

	data, missing, err := dashboardReadCapped(path, "the topic file",
		dashboardPreplanStatusLargeSuggestion, readSuggestion)
	if err != nil {
		return DashboardPreplanStatusOut{}, err
	}
	if missing {
		return DashboardPreplanStatusOut{}, gone
	}

	head := data
	if len(head) > dashboardPreplanHeadMax {
		head = head[:dashboardPreplanHeadMax]
	}
	loc := dashboardPreplanStatusRe.FindIndex(head)
	if loc == nil {
		return DashboardPreplanStatusOut{}, &mcpserver.DataError{
			Msg:        fmt.Sprintf("The topic file %s has no **Status:** line in its first %d KiB", rel, dashboardPreplanHeadMax>>10),
			Suggestion: dashboardPreplanStatusNoLineSuggestion,
		}
	}

	// The match can stop at the end of head, inside a long line. Take the whole
	// line from the full data, so no tail of the old line stays.
	start, end := loc[0], len(data)
	if i := bytes.IndexByte(data[start:], '\n'); i >= 0 {
		end = start + i
	}
	line := data[start:end]
	cr := bytes.HasSuffix(line, []byte("\r"))
	current := strings.TrimSpace(strings.TrimPrefix(string(line), dashboardPreplanStatusPrefix))
	if current == status {
		return DashboardPreplanStatusOut{
			Status:  status,
			Message: fmt.Sprintf("The preplan topic %s already has the status %s.", slug, status),
		}, nil
	}

	replacement := dashboardPreplanStatusPrefix + " " + status
	if cr {
		replacement += "\r"
	}
	next := make([]byte, 0, len(data)-len(line)+len(replacement))
	next = append(next, data[:start]...)
	next = append(next, replacement...)
	next = append(next, data[end:]...)

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
