package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// dashboardDeleteWriteBytes is the seam for the learnings log write. Tests
// replace it to force a write error without a broken filesystem.
var dashboardDeleteWriteBytes = fsx.AtomicWriteBytes

// Messages and Suggestion texts of the learning delete errors.
const (
	dashboardDeleteLearningFieldsMessage   = "The date and heading fields are required"
	dashboardDeleteLearningTooLargeMessage = "The learnings log is too large to edit from the dashboard"
	dashboardDeleteLearningLargeSuggestion = "Remove old entries from .sdlc-v2/learnings/log.md by hand, then retry."
	dashboardDeleteLearningReadSuggestion  = "Check that the learnings log is a regular file you can read, then retry."
	dashboardDeleteLearningWriteSuggestion = "Check write permission on .sdlc-v2/learnings/ and free disk space, then retry."
)

// DeleteDashboardLearning removes the newest entry of root's learnings log
// whose date and heading equal the snapshot row values. A learning has no
// id, so the pair is the key, and the match is the one that
// DashboardLearningBody uses: it scans from the last entry back and compares
// the same truncated heading text that the snapshot sends. The header and the
// other entries are rebuilt with learningsJoin, as learningsRemove does, and
// the file is rewritten through a temp file and a rename.
//
// An absent log and a pair that matches no entry give AlreadyGone and no
// error. An empty date or heading gives a DomainError. A log of more than
// dashboardDeleteReadMax bytes gives a DataError, and the log keeps its bytes.
func DeleteDashboardLearning(root, date, heading string) (DeleteOut, error) {
	if date == "" || heading == "" {
		return DeleteOut{}, &mcpserver.DomainError{
			Msg:        dashboardDeleteLearningFieldsMessage,
			Suggestion: dashboardDeleteReloadSuggestion,
		}
	}

	path := learningsLogPath(root)
	gone := DeleteOut{AlreadyGone: true, Message: fmt.Sprintf("The learning %q is already gone.", heading)}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return gone, nil
		}
		return DeleteOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read learnings log: %s", err),
			Suggestion: dashboardDeleteLearningReadSuggestion,
			Cause:      err,
		}
	}
	if info.Size() > dashboardDeleteReadMax {
		return DeleteOut{}, &mcpserver.DataError{
			Msg:        dashboardDeleteLearningTooLargeMessage,
			Suggestion: dashboardDeleteLearningLargeSuggestion,
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return DeleteOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read learnings log: %s", err),
			Suggestion: dashboardDeleteLearningReadSuggestion,
			Cause:      err,
		}
	}

	header, entries := learningsSplitEntries(string(data))
	at := -1
	for i := len(entries) - 1; i >= 0; i-- {
		e := strings.TrimSpace(entries[i])
		if learningsDateRe.FindString(e) == date && dashboardLearningHeading(e) == heading {
			at = i
			break
		}
	}
	if at < 0 {
		return gone, nil
	}

	kept := make([]string, 0, len(entries)-1)
	for i, entry := range entries {
		if i == at {
			continue
		}
		kept = append(kept, strings.TrimRight(entry, "\n"))
	}

	if err := dashboardDeleteWriteBytes(path, []byte(learningsJoin(header, kept))); err != nil {
		return DeleteOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write learnings log: %s", err),
			Suggestion: dashboardDeleteLearningWriteSuggestion,
			Cause:      err,
		}
	}
	return DeleteOut{Deleted: true, Message: fmt.Sprintf("Deleted the learning %q.", heading)}, nil
}
