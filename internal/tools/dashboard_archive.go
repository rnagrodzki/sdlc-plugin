package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ArchiveRunIn is the request of one run archive. Root is the repo root that
// the dashboard shows. RunID is the id of a pipeline row. ConfirmStalled is
// true when the person confirmed the archive of a stalled run.
type ArchiveRunIn struct {
	Root, RunID    string
	ConfirmStalled bool
}

// ArchiveRunOut is the result of one run archive. Dir is the absolute path of
// run-archive/<runId>/. Moved and Deleted list source paths relative to
// .sdlc-v2/, with forward slashes. Both are [] when empty, never null.
type ArchiveRunOut struct {
	RunID   string   `json:"runId"`
	Dir     string   `json:"dir"`
	Moved   []string `json:"moved"`
	Deleted []string `json:"deleted"`
}

// archiveRecord is the content of run-archive/<runId>/archive.json. Moved
// lists what moved before the file was written, so it never holds the last
// move (the root state file, or the ledger folder of a review run).
type archiveRecord struct {
	RunID      string   `json:"runId"`
	ArchivedAt string   `json:"archivedAt"`
	Moved      []string `json:"moved"`
	Deleted    []string `json:"deleted"`
}

// archiveRecordFile is the name of the record file in the archive folder.
const archiveRecordFile = "archive.json"

// archiveEvidenceSuffix ends the folder name of a plan run evidence folder in
// runs/. Its content goes to evidence/ in the archive folder.
const archiveEvidenceSuffix = ".evidence"

// archiveStatePrefixes are the id prefixes of the pipeline rows that come
// from a state file in runs/.
var archiveStatePrefixes = []string{"ship-", "execute-", "plan-"}

// archiveRename moves a file or a folder. Tests replace it to force a move
// error in the middle of an archive.
var archiveRename = os.Rename

// archiveRemoveAll deletes a file or a folder tree. Tests replace it to force
// a delete error in the middle of an archive.
var archiveRemoveAll = os.RemoveAll

// Archive error codes. The HTTP handler maps each constant to an HTTP status.
// No package types the code text again.
const (
	// ArchiveBadRunID is the code for an id that is not a bare run name.
	ArchiveBadRunID = "BAD_RUN_ID"
	// ArchiveRunNotFound is the code for an id with no run on disk or no row.
	ArchiveRunNotFound = "RUN_NOT_FOUND"
	// ArchiveRunActive is the code for a run whose row status is running.
	ArchiveRunActive = "RUN_ACTIVE"
	// ArchiveConfirmStalled is the code for a stalled run with no confirm.
	ArchiveConfirmStalled = "CONFIRM_STALLED"
	// ArchiveFailed is the code for a read, move or delete failure.
	ArchiveFailed = "ARCHIVE_FAILED"
)

// Suggestions of the archive errors, one for each condition.
const (
	// archiveSuggestBadID is the suggestion for ArchiveBadRunID.
	archiveSuggestBadID = "Pass the run id from the pipeline row."
	// archiveSuggestNotFound is the suggestion for ArchiveRunNotFound.
	archiveSuggestNotFound = "Reload the page. The run is gone."
	// archiveSuggestActive is the suggestion for ArchiveRunActive.
	archiveSuggestActive = "Wait until the run ends or stalls."
	// archiveSuggestStalled is the suggestion for ArchiveConfirmStalled.
	archiveSuggestStalled = "Confirm. Archive removes the resume point."
	// archiveSuggestFSFailed is the suggestion for a failed move or delete.
	archiveSuggestFSFailed = "Fix the permission of the named file, then archive again."
	// archiveSuggestReadFailed is the suggestion for a run that the archive
	// cannot read.
	archiveSuggestReadFailed = "Fix the run files named in the message, then archive again."
)

// ArchiveError is the error of ArchiveRun. Code is one of the Archive* code
// constants. Message is in sentence case, because the page shows it to the
// person. Suggestion is never empty. Cause is the error that made the archive
// fail, or nil when no other error caused it.
type ArchiveError struct {
	Code, Message, Suggestion string
	Cause                     error
}

// Error returns the code and the message of the archive error.
func (e *ArchiveError) Error() string { return e.Code + ": " + e.Message }

// Unwrap returns the cause of the archive error, so errors.Is and errors.As
// reach it.
func (e *ArchiveError) Unwrap() error { return e.Cause }

// archiveErr returns an *ArchiveError with the given code, suggestion and
// cause. The message is msg, then ": " and the cause text when cause is not
// nil, or the cause text alone when msg is empty. Its first letter is upper
// case.
func archiveErr(code, msg, suggestion string, cause error) error {
	switch {
	case cause != nil && msg == "":
		msg = cause.Error()
	case cause != nil:
		msg += ": " + cause.Error()
	}
	if r, size := utf8.DecodeRuneInString(msg); size > 0 {
		msg = string(unicode.ToUpper(r)) + msg[size:]
	}
	return &ArchiveError{Code: code, Message: msg, Suggestion: suggestion, Cause: cause}
}

// archiveRunFiles lists the files of one run that the archive handles. final
// is the path that moves last: the state file, or the ledger folder of a
// review run.
type archiveRunFiles struct {
	keep    []string
	working []string
	final   string
}

// ArchiveRun moves the files of one run into .sdlc-v2/run-archive/<runId>/,
// so the run leaves the dashboard and its data stays on disk.
//
//	ship, execute, plan row  state file, Keep paths, member paths
//	review-<ts> row          the ledger folder only
//
// Eligibility comes from the row status of a fresh collectDashboardRepo call,
// never from client data: running gives ArchiveRunActive, stalled without
// ConfirmStalled gives ArchiveConfirmStalled, completed and failed archive.
// The id must be the id of a row that the dashboard shows now; a run nested
// in a ship moves only with its ship, and a run past the history window is
// not a row, so both give ArchiveRunNotFound. Every check runs before the
// first write, so an error of a check changes no file.
//
// Move order:
//  1. Create run-archive/<runId>/.
//  2. Move each Keep path of the members, then of the root run.
//  3. Delete each Working path of the members and of the root run.
//  4. Move the member state files.
//  5. Write archive.json.
//  6. Move the root state file (or the review ledger folder) last.
//
// A failure at any step returns ArchiveFailed and stops. The root state file
// is then still in runs/, so the run is still a row and the person can archive
// it again: the retry handles only the files that are still in place.
func ArchiveRun(in ArchiveRunIn, now time.Time) (ArchiveRunOut, error) {
	out := ArchiveRunOut{RunID: in.RunID, Moved: []string{}, Deleted: []string{}}
	if err := archiveCheckID(in.RunID); err != nil {
		return out, err
	}

	repo := collectDashboardRepo(in.Root, now)
	if repo.Error != "" {
		return out, archiveErr(ArchiveFailed, "Read the runs of repo "+in.Root+": "+repo.Error, archiveSuggestReadFailed, nil)
	}
	row, ok := archiveFindRow(repo.Pipelines, in.RunID)
	if !ok {
		return out, archiveErr(ArchiveRunNotFound, fmt.Sprintf("No pipeline row with id %q", in.RunID), archiveSuggestNotFound, nil)
	}
	switch {
	case row.Status == PipelineRunning:
		return out, archiveErr(ArchiveRunActive, fmt.Sprintf("Run %q is running", in.RunID), archiveSuggestActive, nil)
	case row.Status == PipelineStalled && !in.ConfirmStalled:
		return out, archiveErr(ArchiveConfirmStalled, fmt.Sprintf("Run %q is stalled and the archive is not confirmed", in.RunID), archiveSuggestStalled, nil)
	}

	list, err := state.List(in.Root)
	if err != nil {
		return out, archiveErr(ArchiveFailed, "List the run states", archiveSuggestReadFailed, err)
	}
	rootFiles, err := archiveResolve(in.Root, list.States, row.ID)
	if err != nil {
		return out, err
	}
	var members []archiveRunFiles
	for _, id := range row.join.members {
		m, err := archiveResolve(in.Root, list.States, id)
		var ae *ArchiveError
		if errors.As(err, &ae) && ae.Code == ArchiveRunNotFound {
			continue // the member left runs/ after the collect: nothing to move
		}
		if err != nil {
			return out, err
		}
		members = append(members, m)
	}

	mv := &archiveMover{
		data:    filepath.Join(in.Root, paths.DataDir),
		dir:     filepath.Join(in.Root, paths.DataDir, paths.RunArchiveSubdir, row.ID),
		moved:   []string{},
		deleted: []string{},
	}
	out.Dir = mv.dir
	err = mv.run(row.ID, members, rootFiles, now)
	out.Moved, out.Deleted = mv.moved, mv.deleted
	return out, err
}

// archiveCheckID returns an ArchiveBadRunID error when id is not a bare run
// name, or is neither a review-<ts> name nor a name with a state row prefix.
// It reads no file.
func archiveCheckID(id string) error {
	if err := bareRunName(id); err != nil {
		return archiveErr(ArchiveBadRunID, "", archiveSuggestBadID, err)
	}
	if strings.HasPrefix(id, dashboardReviewPrefix) {
		if _, err := ReviewLedgerDir("", id); err != nil {
			return archiveErr(ArchiveBadRunID, "", archiveSuggestBadID, err)
		}
		return nil
	}
	for _, p := range archiveStatePrefixes {
		if strings.HasPrefix(id, p) && len(id) > len(p) {
			return nil
		}
	}
	return archiveErr(ArchiveBadRunID, fmt.Sprintf("Invalid run name %q: not a state run or review run name", id), archiveSuggestBadID, nil)
}

// archiveFindRow returns the pipeline row of ps whose id is id.
func archiveFindRow(ps []DashboardPipeline, id string) (DashboardPipeline, bool) {
	for _, p := range ps {
		if p.ID == id {
			return p, true
		}
	}
	return DashboardPipeline{}, false
}

// archiveResolve returns the files of the run id. A review-<ts> id gives its
// ledger folder as the final move. Any other id must match the state.RunID of
// one state in states; its files come from ResolveRunArtifacts. The function
// joins no path with id before that match. A run with no files on disk gives
// ArchiveRunNotFound; a read failure gives ArchiveFailed.
func archiveResolve(root string, states []*state.State, id string) (archiveRunFiles, error) {
	if strings.HasPrefix(id, dashboardReviewPrefix) {
		dir, err := ReviewLedgerDir(root, id)
		if err != nil {
			return archiveRunFiles{}, archiveErr(ArchiveBadRunID, "", archiveSuggestBadID, err)
		}
		found, err := existingPath(dir)
		if err != nil {
			return archiveRunFiles{}, archiveErr(ArchiveFailed, "", archiveSuggestReadFailed, err)
		}
		if found == "" {
			return archiveRunFiles{}, archiveErr(ArchiveRunNotFound, fmt.Sprintf("No review ledger folder for %q", id), archiveSuggestNotFound, nil)
		}
		return archiveRunFiles{final: found}, nil
	}

	var st *state.State
	for _, s := range states {
		if state.RunID(s) == id {
			st = s
			break
		}
	}
	if st == nil {
		return archiveRunFiles{}, archiveErr(ArchiveRunNotFound, fmt.Sprintf("No state file for %q", id), archiveSuggestNotFound, nil)
	}
	arts, err := ResolveRunArtifacts(root, st)
	if err != nil {
		return archiveRunFiles{}, archiveErr(ArchiveFailed, "", archiveSuggestReadFailed, err)
	}
	if arts.StateFile == "" {
		return archiveRunFiles{}, archiveErr(ArchiveRunNotFound, fmt.Sprintf("No state file for %q", id), archiveSuggestNotFound, nil)
	}
	return archiveRunFiles{keep: arts.Keep, working: arts.Working, final: arts.StateFile}, nil
}

// archiveMover runs the moves of one archive and records each path it moved
// or deleted, relative to data.
type archiveMover struct {
	data    string // <root>/.sdlc-v2
	dir     string // <root>/.sdlc-v2/run-archive/<runId>
	moved   []string
	deleted []string
}

// run applies the six steps of the move order that ArchiveRun documents.
func (m *archiveMover) run(runID string, members []archiveRunFiles, root archiveRunFiles, now time.Time) error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return archiveErr(ArchiveFailed, "Create archive folder "+m.dir, archiveSuggestFSFailed, err)
	}
	all := append(append([]archiveRunFiles{}, members...), root)
	for _, f := range all {
		for _, p := range f.keep {
			if err := m.move(p); err != nil {
				return err
			}
		}
	}
	for _, f := range all {
		for _, p := range f.working {
			if err := m.remove(p); err != nil {
				return err
			}
		}
	}
	for _, f := range members {
		if err := m.move(f.final); err != nil {
			return err
		}
	}
	rec := archiveRecord{
		RunID:      runID,
		ArchivedAt: now.UTC().Format(time.RFC3339),
		Moved:      append([]string{}, m.moved...),
		Deleted:    append([]string{}, m.deleted...),
	}
	recPath := filepath.Join(m.dir, archiveRecordFile)
	if err := fsx.AtomicWriteJSON(recPath, rec); err != nil {
		return archiveErr(ArchiveFailed, "Write "+recPath, archiveSuggestFSFailed, err)
	}
	return m.move(root.final)
}

// move renames src to its place in the archive folder and records it.
func (m *archiveMover) move(src string) error {
	rel, err := m.rel(src)
	if err != nil {
		return err
	}
	dst := filepath.Join(m.dir, archiveDestRel(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return archiveErr(ArchiveFailed, "Create folder "+filepath.Dir(dst), archiveSuggestFSFailed, err)
	}
	if err := archiveRename(src, dst); err != nil {
		return archiveErr(ArchiveFailed, "Move "+src, archiveSuggestFSFailed, err)
	}
	m.moved = append(m.moved, filepath.ToSlash(rel))
	return nil
}

// remove deletes src and records it.
func (m *archiveMover) remove(src string) error {
	rel, err := m.rel(src)
	if err != nil {
		return err
	}
	if err := archiveRemoveAll(src); err != nil {
		return archiveErr(ArchiveFailed, "Delete "+src, archiveSuggestFSFailed, err)
	}
	m.deleted = append(m.deleted, filepath.ToSlash(rel))
	return nil
}

// rel returns src relative to the .sdlc-v2 folder. A path outside that folder
// gives ArchiveFailed, so the archive never moves a file it does not own.
func (m *archiveMover) rel(src string) (string, error) {
	rel, err := filepath.Rel(m.data, src)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", archiveErr(ArchiveFailed, "Path "+src+" is outside "+m.data, archiveSuggestReadFailed, nil)
	}
	return rel, nil
}

// archiveDestRel maps a path relative to .sdlc-v2/ to its path in the
// archive folder.
//
//	runs/<state>.json                 <state>.json
//	runs/ledger/<id>                  ledger/<id>
//	runs/<plan state>.evidence/<f>    evidence/<f>
//	reports/<file>                    reports/<file>
func archiveDestRel(rel string) string {
	slash := filepath.ToSlash(rel)
	rest, ok := strings.CutPrefix(slash, paths.RunsSubdir+"/")
	if !ok {
		return rel
	}
	first, tail, hasTail := strings.Cut(rest, "/")
	if hasTail && strings.HasSuffix(first, archiveEvidenceSuffix) {
		return filepath.FromSlash("evidence/" + tail)
	}
	return filepath.FromSlash(rest)
}
