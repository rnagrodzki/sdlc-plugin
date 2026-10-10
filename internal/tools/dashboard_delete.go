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

// DashboardDeleteOut is the 200 body of each delete request.
type DashboardDeleteOut struct {
	Deleted     bool   `json:"deleted"`     // true when this call removed the item
	AlreadyGone bool   `json:"alreadyGone"` // true when no item matched
	Message     string `json:"message"`     // one sentence for the dialog
}

// Seams for injected read and write failures. Tests replace them to force a
// remove error, a read error or a write error without a broken filesystem.
var (
	dashboardDeleteRemove    = os.Remove
	dashboardDeleteReadFile  = os.ReadFile
	dashboardDeleteWriteJSON = fsx.AtomicWriteJSON
)

// dashboardDeleteReadMax is the size limit, in bytes, of a store file that a
// dashboard delete reads. A bigger file is refused before it is read.
const dashboardDeleteReadMax = 8 << 20

// dashboardDeleteReadMaxText is dashboardDeleteReadMax for a message.
const dashboardDeleteReadMaxText = "8 MiB"

// Suggestion texts of the delete errors, one for each condition.
const (
	dashboardDeleteReloadSuggestion        = "Reload the page and try again."
	dashboardDeletePreplanFolderSuggestion = "Remove the folder by hand."
	dashboardDeletePreplanReadSuggestion   = "Check read permission on .sdlc-v2/preplan/ and retry."
	dashboardDeletePreplanWriteSuggestion  = "Check write permission on .sdlc-v2/preplan/ and retry."
	dashboardDeleteDeferredReadSuggestion  = "Check read permission on .sdlc-v2/history/deferred.json and retry."
	dashboardDeleteDeferredParseSuggestion = "Fix the JSON syntax in .sdlc-v2/history/deferred.json by hand, then retry."
	dashboardDeleteDeferredLargeSuggestion = "Remove resolved items from .sdlc-v2/history/deferred.json by hand until it is smaller than " + dashboardDeleteReadMaxText + ", then retry."
	dashboardDeleteDeferredWriteSuggestion = "Check write permission on .sdlc-v2/history/ and free disk space, then retry."
	dashboardDeleteDeferredIDMessage       = "The id field is required"
)

// dashboardReadCapped reads the store file at path for a dashboard delete.
// name names the file in a message, in lowercase, for example "the
// learnings log". missing is true when the file does not
// exist, also when it goes away between the size check and the read. A file
// of more than dashboardDeleteReadMax bytes gives a DataError that names the
// size and the limit, and the file is not read. Any other Stat or read
// failure gives an InfraError with readSuggestion.
func dashboardReadCapped(path, name, largeSuggestion, readSuggestion string) (data []byte, missing bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, true, nil
		}
		return nil, false, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("Could not read %s: %s", name, err),
			Suggestion: readSuggestion,
			Cause:      err,
		}
	}
	if info.Size() > dashboardDeleteReadMax {
		return nil, false, &mcpserver.DataError{
			Msg: fmt.Sprintf("%s is %d bytes, more than the %d bytes (%s) that a dashboard delete reads",
				strings.ToUpper(name[:1])+name[1:], info.Size(), dashboardDeleteReadMax, dashboardDeleteReadMaxText),
			Suggestion: largeSuggestion,
		}
	}
	data, err = dashboardDeleteReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, true, nil
		}
		return nil, false, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("Could not read %s: %s", name, err),
			Suggestion: readSuggestion,
			Cause:      err,
		}
	}
	return data, false, nil
}

// dashboardPreplanSlugError returns the DomainError for a slug that is not a
// preplan file name without the ".md" suffix, or nil for a good slug. The
// message names the slug and the rule that it breaks.
func dashboardPreplanSlugError(slug string) error {
	var msg string
	switch {
	case slug == "":
		msg = "The slug field is required"
	case slug == "." || slug == "..":
		msg = fmt.Sprintf("The slug %q is not a file name", slug)
	case slug != filepath.Base(slug) || strings.ContainsAny(slug, `/\`):
		msg = fmt.Sprintf("The slug %q has a path separator. A slug is a file name in .sdlc-v2/preplan/", slug)
	case strings.HasSuffix(slug, ".md"):
		msg = fmt.Sprintf("The slug %q ends in .md. Send the file name without .md", slug)
	default:
		return nil
	}
	return &mcpserver.DomainError{Msg: msg, Suggestion: dashboardDeleteReloadSuggestion}
}

// DashboardDeletePreplan deletes the topic file <slug>.md in the preplan folder
// of root. Any status can be deleted. The slug is a file name without the
// ".md" suffix: it is not empty, has no path separator, is not "." or "..",
// and does not end in ".md". A topic file that is already gone gives
// AlreadyGone and no error. A symlink is removed as a link: its target stays.
//
// Errors: a bad slug and a folder in the place of the topic file give a
// DomainError. A failed Lstat or remove gives an InfraError.
func DashboardDeletePreplan(root, slug string) (DashboardDeleteOut, error) {
	if err := dashboardPreplanSlugError(slug); err != nil {
		return DashboardDeleteOut{}, err
	}

	rel := filepath.ToSlash(filepath.Join(paths.DataDir, paths.PreplanSubdir, slug+".md"))
	path := filepath.Join(root, paths.DataDir, paths.PreplanSubdir, slug+".md")
	gone := DashboardDeleteOut{AlreadyGone: true, Message: fmt.Sprintf("The preplan topic %s is already gone.", slug)}

	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return gone, nil
		}
		return DashboardDeleteOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("Could not read the preplan topic %s: %s", rel, err),
			Suggestion: dashboardDeletePreplanReadSuggestion,
			Cause:      err,
		}
	}
	if info.IsDir() {
		return DashboardDeleteOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("The path %s is a folder, not a topic file", rel),
			Suggestion: dashboardDeletePreplanFolderSuggestion,
		}
	}

	if err := dashboardDeleteRemove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return gone, nil
		}
		return DashboardDeleteOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("Could not delete the preplan topic %s: %s", rel, err),
			Suggestion: dashboardDeletePreplanWriteSuggestion,
			Cause:      err,
		}
	}
	return DashboardDeleteOut{Deleted: true, Message: fmt.Sprintf("Deleted the preplan topic %s.", slug)}, nil
}

// DashboardDeleteDeferred deletes the first item with the given id from
// deferred.json of root, whatever its status. The file is read through
// dashboardReadCapped and rewritten through a temp file and a rename, as
// indented JSON with a final newline. An absent file, an empty file and an
// unknown id give AlreadyGone and no error; the message tells an empty store
// from an unknown id.
//
// Errors: an empty id gives a DomainError. A file that is too large or does
// not parse gives a DataError, and the file keeps its bytes. A failed Stat,
// read or write gives an InfraError.
func DashboardDeleteDeferred(root, id string) (DashboardDeleteOut, error) {
	if id == "" {
		return DashboardDeleteOut{}, &mcpserver.DomainError{
			Msg:        dashboardDeleteDeferredIDMessage,
			Suggestion: dashboardDeleteReloadSuggestion,
		}
	}

	path := history.NewFileWriter(paths.HistoryDir(root)).DeferredPath()
	empty := DashboardDeleteOut{AlreadyGone: true, Message: fmt.Sprintf("The deferred store has no items. The deferred item %s is already gone.", id)}

	data, missing, err := dashboardReadCapped(path, "the file deferred.json",
		dashboardDeleteDeferredLargeSuggestion, dashboardDeleteDeferredReadSuggestion)
	if err != nil {
		return DashboardDeleteOut{}, err
	}
	if missing || len(strings.TrimSpace(string(data))) == 0 {
		return empty, nil
	}

	var items []history.DeferredIssue
	if err := json.Unmarshal(data, &items); err != nil {
		return DashboardDeleteOut{}, &mcpserver.DataError{
			Msg:        fmt.Sprintf("Could not parse deferred.json: %s", err),
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
		return DashboardDeleteOut{AlreadyGone: true, Message: fmt.Sprintf("No deferred item has the id %s. Another session may have deleted it.", id)}, nil
	}

	// A new non-nil slice, so that the last item removed writes [] and not null.
	kept := make([]history.DeferredIssue, 0, len(items)-1)
	kept = append(kept, items[:at]...)
	kept = append(kept, items[at+1:]...)

	if err := dashboardDeleteWriteJSON(path, kept); err != nil {
		return DashboardDeleteOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("Could not write deferred.json: %s", err),
			Suggestion: dashboardDeleteDeferredWriteSuggestion,
			Cause:      err,
		}
	}
	return DashboardDeleteOut{Deleted: true, Message: fmt.Sprintf("Deleted the deferred item %s.", id)}, nil
}
