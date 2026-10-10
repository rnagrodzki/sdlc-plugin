package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// DeleteOut is the 200 body of each delete request.
type DeleteOut struct {
	Deleted     bool   `json:"deleted"`     // true when this call removed the item
	AlreadyGone bool   `json:"alreadyGone"` // true when no item matched
	Message     string `json:"message"`     // one sentence for the dialog
}

// Seams for injected write failures. Tests replace them to force a remove
// error or a write error without a broken filesystem.
var (
	dashboardDeleteRemove    = os.Remove
	dashboardDeleteWriteJSON = fsx.AtomicWriteJSON
)

// dashboardDeleteReadMax is the size limit, in bytes, of a store file that a
// dashboard delete reads. A bigger file is refused before it is read.
const dashboardDeleteReadMax = 8 << 20

// Suggestion texts of the delete errors, one for each condition.
const (
	dashboardDeleteReloadSuggestion        = "Reload the page and try again."
	dashboardDeletePreplanFolderSuggestion = "Remove the folder by hand."
	dashboardDeletePreplanReadSuggestion   = "Check read permission on .sdlc-v2/preplan/ and retry."
	dashboardDeletePreplanWriteSuggestion  = "Check write permission on .sdlc-v2/preplan/ and retry."
	dashboardDeleteDeferredReadSuggestion  = "Check read permission on .sdlc-v2/history/deferred.json and retry."
	dashboardDeleteDeferredParseSuggestion = "Fix the JSON syntax in .sdlc-v2/history/deferred.json by hand, then retry."
	dashboardDeleteDeferredLargeSuggestion = "Remove resolved items from .sdlc-v2/history/deferred.json by hand, then retry."
	dashboardDeleteDeferredWriteSuggestion = "Check write permission on .sdlc-v2/history/ and free disk space, then retry."
	dashboardDeletePreplanSlugMessage      = "The slug field must be a preplan file name"
	dashboardDeleteDeferredIDMessage       = "The id field is required"
	dashboardDeleteDeferredTooLargeMessage = "deferred.json is too large to edit from the dashboard"
)

// DeletePreplanTopic deletes the topic file <slug>.md in the preplan folder
// of root. Any status can be deleted. The slug is a file name without the
// ".md" suffix: it has no path separator and is not "." or "..". A topic
// file that is already gone gives AlreadyGone and no error. A symlink is
// removed as a link: its target stays.
func DeletePreplanTopic(root, slug string) (DeleteOut, error) {
	if slug == "" || slug != filepath.Base(slug) || slug == "." || slug == ".." {
		return DeleteOut{}, &mcpserver.DomainError{
			Msg:        dashboardDeletePreplanSlugMessage,
			Suggestion: dashboardDeleteReloadSuggestion,
		}
	}

	rel := filepath.ToSlash(filepath.Join(paths.DataDir, paths.PreplanSubdir, slug+".md"))
	path := filepath.Join(root, paths.DataDir, paths.PreplanSubdir, slug+".md")
	gone := DeleteOut{AlreadyGone: true, Message: fmt.Sprintf("The preplan topic %s is already gone.", slug)}

	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return gone, nil
		}
		return DeleteOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read preplan topic %s: %s", rel, err),
			Suggestion: dashboardDeletePreplanReadSuggestion,
			Cause:      err,
		}
	}
	if info.IsDir() {
		return DeleteOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s is a folder, not a topic file", rel),
			Suggestion: dashboardDeletePreplanFolderSuggestion,
		}
	}

	if err := dashboardDeleteRemove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return gone, nil
		}
		return DeleteOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("delete preplan topic %s: %s", rel, err),
			Suggestion: dashboardDeletePreplanWriteSuggestion,
			Cause:      err,
		}
	}
	return DeleteOut{Deleted: true, Message: fmt.Sprintf("Deleted the preplan topic %s.", slug)}, nil
}

// DeleteDeferredItem deletes the first item with the given id from
// deferred.json of root, whatever its status. The file is read with a size
// check and rewritten through a temp file and a rename, with the same bytes
// as the history writer. An unknown id, an absent file and an empty file give
// AlreadyGone and no error. A file that is too large or does not parse gives
// a DataError, and the file keeps its bytes.
func DeleteDeferredItem(root, id string) (DeleteOut, error) {
	if id == "" {
		return DeleteOut{}, &mcpserver.DomainError{
			Msg:        dashboardDeleteDeferredIDMessage,
			Suggestion: dashboardDeleteReloadSuggestion,
		}
	}

	path := history.NewFileWriter(paths.HistoryDir(root)).DeferredPath()
	gone := DeleteOut{AlreadyGone: true, Message: fmt.Sprintf("The deferred item %s is already gone.", id)}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return gone, nil
		}
		return DeleteOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read deferred.json: %s", err),
			Suggestion: dashboardDeleteDeferredReadSuggestion,
			Cause:      err,
		}
	}
	if info.Size() > dashboardDeleteReadMax {
		return DeleteOut{}, &mcpserver.DataError{
			Msg:        dashboardDeleteDeferredTooLargeMessage,
			Suggestion: dashboardDeleteDeferredLargeSuggestion,
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return DeleteOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read deferred.json: %s", err),
			Suggestion: dashboardDeleteDeferredReadSuggestion,
			Cause:      err,
		}
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return gone, nil
	}

	var items []history.DeferredIssue
	if err := json.Unmarshal(data, &items); err != nil {
		return DeleteOut{}, &mcpserver.DataError{
			Msg:        fmt.Sprintf("parse deferred.json: %s", err),
			Suggestion: dashboardDeleteDeferredParseSuggestion,
			Cause:      err,
		}
	}

	at := -1
	for i := range items {
		if items[i].ID == id {
			at = i
			break
		}
	}
	if at < 0 {
		return gone, nil
	}

	// A new non-nil slice, so that the last item removed writes [] and not null.
	kept := make([]history.DeferredIssue, 0, len(items)-1)
	kept = append(kept, items[:at]...)
	kept = append(kept, items[at+1:]...)

	if err := dashboardDeleteWriteJSON(path, kept); err != nil {
		return DeleteOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write deferred.json: %s", err),
			Suggestion: dashboardDeleteDeferredWriteSuggestion,
			Cause:      err,
		}
	}
	return DeleteOut{Deleted: true, Message: fmt.Sprintf("Deleted the deferred item %s.", id)}, nil
}
