package tools

import (
	"fmt"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// dashboardDeleteWriteBytes is the seam for the learnings log write. Tests
// replace it to force a write error without a broken filesystem.
var dashboardDeleteWriteBytes = fsx.AtomicWriteBytes

// Suggestion texts of the learning delete errors.
const (
	dashboardDeleteLearningLargeSuggestion = "Remove old entries from .sdlc-v2/learnings/log.md by hand until it is smaller than " + dashboardDeleteReadMaxText + ", then retry."
	dashboardDeleteLearningReadSuggestion  = "Check that the learnings log is a regular file you can read, then retry."
	dashboardDeleteLearningWriteSuggestion = "Check write permission on .sdlc-v2/learnings/ and free disk space, then retry."
)

// dashboardLearningKeyError returns the DomainError for a date and heading
// pair that cannot name a learning, or nil for a good pair. The message
// names the field that is empty, or the date that is not YYYY-MM-DD.
func dashboardLearningKeyError(date, heading string) error {
	var msg string
	switch {
	case date == "" && heading == "":
		msg = "The date and heading fields are required"
	case date == "":
		msg = "The date field is required"
	case heading == "":
		msg = "The heading field is required"
	case learningsDateRe.FindString(date) != date:
		msg = fmt.Sprintf("The date %q is not a YYYY-MM-DD date", date)
	default:
		return nil
	}
	return &mcpserver.DomainError{Msg: msg, Suggestion: dashboardDeleteReloadSuggestion}
}

// DashboardDeleteLearning removes the newest entry of root's learnings log
// whose date and heading equal the snapshot row values. A learning has no
// id, so the pair is the key. dashboardFindLearning finds the entry, the
// same finder that DashboardLearningBody uses, so the delete removes the
// entry that the viewer shows. The header and the other entries are rebuilt
// with learningsJoin, as learningsRemove does, and the file is rewritten
// through a temp file and a rename.
//
// An absent or empty log and a pair that matches no entry give AlreadyGone
// and no error; the message tells an empty log from a pair with no match.
//
// Errors: an empty date or heading, and a date that is not YYYY-MM-DD, give
// a DomainError. A log of more than dashboardDeleteReadMax bytes gives a
// DataError, and the log keeps its bytes. A failed Stat, read or write gives
// an InfraError.
func DashboardDeleteLearning(root, date, heading string) (DashboardDeleteOut, error) {
	if err := dashboardLearningKeyError(date, heading); err != nil {
		return DashboardDeleteOut{}, err
	}

	path := learningsLogPath(root)
	data, missing, err := dashboardReadCapped(path, "the learnings log",
		dashboardDeleteLearningLargeSuggestion, dashboardDeleteLearningReadSuggestion)
	if err != nil {
		return DashboardDeleteOut{}, err
	}

	var header string
	var entries []string
	if !missing {
		header, entries = learningsSplitEntries(string(data))
	}
	if len(entries) == 0 {
		return DashboardDeleteOut{AlreadyGone: true, Message: fmt.Sprintf("The learnings log has no entries. The learning %q is already gone.", heading)}, nil
	}
	at := dashboardFindLearning(entries, date, heading)
	if at < 0 {
		return DashboardDeleteOut{AlreadyGone: true, Message: fmt.Sprintf("No learning has the date %s and the heading %q. Another session may have deleted it.", date, heading)}, nil
	}

	kept := make([]string, 0, len(entries)-1)
	for i, entry := range entries {
		if i == at {
			continue
		}
		kept = append(kept, strings.TrimRight(entry, "\n"))
	}

	if err := dashboardDeleteWriteBytes(path, []byte(learningsJoin(header, kept))); err != nil {
		return DashboardDeleteOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("Could not write the learnings log: %s", err),
			Suggestion: dashboardDeleteLearningWriteSuggestion,
			Cause:      err,
		}
	}
	return DashboardDeleteOut{Deleted: true, Message: fmt.Sprintf("Deleted the learning %q.", heading)}, nil
}
