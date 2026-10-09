package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ClearCacheOut is the result of one cache clear. Classes always lists the
// four classes in a fixed order, so a class that found nothing shows files 0.
// Both slices are non-nil, so they encode as [] and never as null.
type ClearCacheOut struct {
	// FreedBytes is the sum of the Bytes of every class.
	FreedBytes int64 `json:"freedBytes"`
	// Classes holds one row for each cache class.
	Classes []ClearCacheClass `json:"classes"`
	// Skipped holds one row for each path that Clear kept or could not clear.
	Skipped []ClearCacheSkip `json:"skipped"`
}

// ClearCacheClass is the tally of one cache class.
type ClearCacheClass struct {
	// Name is the class name: evidence-rotations, temp-dirs, orphan-reports
	// or server-log.
	Name string `json:"name"`
	// Files counts the top-level entries that Clear deleted (a temp dir counts
	// as one) or, for server-log, 1 when it truncated a non-empty log.
	Files int `json:"files"`
	// Bytes is the size of those entries before Clear removed them.
	Bytes int64 `json:"bytes"`
}

// ClearCacheSkip names one path that Clear did not clear, with the reason.
type ClearCacheSkip struct {
	// Path is the absolute path of the entry.
	Path string `json:"path"`
	// Reason says why Clear kept the entry or could not clear it.
	Reason string `json:"reason"`
}

// Names of the cache classes in the order ClearCache reports them.
const (
	// clearClassEvidence is the class of rotated evidence files (*.jsonl.1).
	clearClassEvidence = "evidence-rotations"
	// clearClassTempDirs is the class of old sdlc-* temp dirs.
	clearClassTempDirs = "temp-dirs"
	// clearClassReports is the class of reports that no run state owns.
	clearClassReports = "orphan-reports"
	// clearClassServerLog is the class of the dashboard server log.
	clearClassServerLog = "server-log"
)

// clearTempDirPrefix is the name prefix of the temp dirs that the plugin tools
// create. Clear sweeps the dirs whose name starts with it.
const clearTempDirPrefix = "sdlc-"

// clearTempDirMinAge is the age a temp dir needs before Clear deletes it.
const clearTempDirMinAge = 24 * time.Hour

// clearRotatedSuffix ends the name of an evidence file after a size-cap
// rotation (see appendJSONLBounded).
const clearRotatedSuffix = ".jsonl.1"

// clearTempDir returns the directory that holds the sdlc-* temp dirs. Tests
// replace it so that they never touch the real temp directory.
var clearTempDir = os.TempDir

// clearRemove deletes a file or a directory tree. Tests replace it to force a
// delete error that a real filesystem cannot produce on demand.
var clearRemove = os.RemoveAll

// ClearCache deletes the regenerable cache data and reports what it did.
//
//	evidence-rotations  root evidence/*.jsonl.1 older than dashboardStallAfter
//	temp-dirs           sdlc-* dirs in the temp dir older than 24 hours,
//	                    except sdlc-explore-* (state.GC owns those)
//	orphan-reports      root reports/* whose run id matches no ship or execute
//	                    state in runs/
//	server-log          serverLog, truncated to 0 bytes (not deleted)
//
// An entry whose age equals the limit is kept. Clear never touches a live
// evidence file, history, learnings, timings, runs, run-archive or the binary
// cache. Clear does not rewrite a live evidence file, because a line that
// another process appends during the rewrite would be lost. The server keeps
// the log open with O_APPEND, so a write after the truncation goes to the new
// end of the file.
//
// Only a failure to read root itself returns an error (an InfraError). Any
// other failure, and every entry that Clear keeps on purpose, adds a Skipped
// row, and Clear continues with the next entry. When the run states cannot all
// be read, Clear deletes no report, because it cannot tell which reports a run
// owns.
func ClearCache(root, serverLog string, now time.Time) (ClearCacheOut, error) {
	if _, err := os.ReadDir(root); err != nil {
		return ClearCacheOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("clear cache: read repo root %q: %s", root, err),
			Suggestion: "Check that the repo folder exists and that you can read it, then clear the cache again.",
			Cause:      err,
		}
	}

	sweep := &clearSweep{now: now, skipped: []ClearCacheSkip{}}
	classes := []ClearCacheClass{
		sweep.evidenceRotations(root),
		sweep.tempDirs(clearTempDir()),
		sweep.orphanReports(root),
		sweep.serverLog(serverLog),
	}

	out := ClearCacheOut{Classes: classes, Skipped: sweep.skipped}
	for _, c := range classes {
		out.FreedBytes += c.Bytes
	}
	return out, nil
}

// clearSweep carries the clock and the Skipped rows through one ClearCache run.
type clearSweep struct {
	now     time.Time
	skipped []ClearCacheSkip
}

// skip adds a Skipped row.
func (s *clearSweep) skip(path, reason string) {
	s.skipped = append(s.skipped, ClearCacheSkip{Path: path, Reason: reason})
}

// skipErr adds a Skipped row for a filesystem error.
func (s *clearSweep) skipErr(path string, err error) {
	s.skip(path, "delete failed: "+err.Error())
}

// readDir lists dir. A dir that does not exist is an empty class and adds no
// row. Any other read error adds a Skipped row. The bool is false when the
// caller has nothing to sweep.
func (s *clearSweep) readDir(dir string) ([]fs.DirEntry, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.skipErr(dir, err)
		}
		return nil, false
	}
	return entries, true
}

// remove deletes path through clearRemove and adds it to class. A delete error
// adds a Skipped row and leaves class unchanged.
func (s *clearSweep) remove(class *ClearCacheClass, path string, size int64) {
	if err := clearRemove(path); err != nil {
		s.skipErr(path, err)
		return
	}
	class.Files++
	class.Bytes += size
}

// removeIfOlder deletes the entry at path when its mtime is more than minAge
// before s.now. A newer entry adds a Skipped row that says so.
func (s *clearSweep) removeIfOlder(class *ClearCacheClass, path string, e fs.DirEntry, minAge time.Duration) {
	info, err := e.Info()
	if err != nil {
		// The entry left the directory between the listing and this call (for
		// example a rotation replaced it). Nothing is left to clear.
		if !errors.Is(err, fs.ErrNotExist) {
			s.skipErr(path, err)
		}
		return
	}
	if s.now.Sub(info.ModTime()) <= minAge {
		s.skip(path, clearTooNewReason(minAge))
		return
	}
	size, err := clearEntrySize(path, info)
	if err != nil {
		s.skipErr(path, err)
		return
	}
	s.remove(class, path, size)
}

// evidenceRotations deletes the rotated evidence files of root that are older
// than dashboardStallAfter.
func (s *clearSweep) evidenceRotations(root string) ClearCacheClass {
	class := ClearCacheClass{Name: clearClassEvidence}
	dir := filepath.Join(root, paths.DataDir, paths.EvidenceSubdir)
	entries, ok := s.readDir(dir)
	if !ok {
		return class
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), clearRotatedSuffix) {
			continue
		}
		s.removeIfOlder(&class, filepath.Join(dir, e.Name()), e, dashboardStallAfter)
	}
	return class
}

// tempDirs deletes the sdlc-* dirs of dir that are older than
// clearTempDirMinAge. It leaves sdlc-explore-* dirs to state.GC.
func (s *clearSweep) tempDirs(dir string) ClearCacheClass {
	class := ClearCacheClass{Name: clearClassTempDirs}
	entries, ok := s.readDir(dir)
	if !ok {
		return class
	}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsDir() || !strings.HasPrefix(name, clearTempDirPrefix) || strings.HasPrefix(name, exploreDirPrefix) {
			continue
		}
		s.removeIfOlder(&class, filepath.Join(dir, name), e, clearTempDirMinAge)
	}
	return class
}

// orphanReports deletes the files of reports/ whose run id matches the
// execRunID of no ship or execute state in runs/. It keeps a report with an
// unknown name. It deletes nothing when it cannot list every run state.
func (s *clearSweep) orphanReports(root string) ClearCacheClass {
	class := ClearCacheClass{Name: clearClassReports}
	dir := filepath.Join(root, paths.DataDir, paths.ReportsSubdir)
	entries, ok := s.readDir(dir)
	if !ok || len(entries) == 0 {
		return class
	}

	owners, err := clearReportOwners(root)
	if err != nil {
		s.skipErr(dir, err)
		return class
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		runID, ok := ReportOwner(e.Name())
		if !ok {
			s.skip(path, "report name has no run id")
			continue
		}
		if owners[runID] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			// The report left the directory after the listing. Nothing to clear.
			if !errors.Is(err, fs.ErrNotExist) {
				s.skipErr(path, err)
			}
			continue
		}
		s.remove(&class, path, info.Size())
	}
	return class
}

// serverLog truncates the file at path to 0 bytes. A missing file and an empty
// file are an empty class. An empty path means no server log is known. A path
// that is not a regular file adds a Skipped row and is not truncated.
func (s *clearSweep) serverLog(path string) ClearCacheClass {
	class := ClearCacheClass{Name: clearClassServerLog}
	if path == "" {
		return class
	}
	info, err := os.Stat(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.skipErr(path, err)
		}
		return class
	}
	if !info.Mode().IsRegular() {
		s.skipErr(path, errors.New("not a regular file"))
		return class
	}
	if info.Size() == 0 {
		return class
	}
	if err := os.Truncate(path, 0); err != nil {
		s.skipErr(path, err)
		return class
	}
	class.Files = 1
	class.Bytes = info.Size()
	return class
}

// clearReportOwners returns the set of execRunID values of the ship and
// execute states in runs/. It returns an error when the states cannot all be
// read: a run whose state is missing from the set would lose its reports.
func clearReportOwners(root string) (map[string]bool, error) {
	list, err := state.List(root)
	if err != nil {
		return nil, fmt.Errorf("cannot list the run states, so no report is deleted: %w", err)
	}
	if list.Skipped > 0 {
		return nil, fmt.Errorf("%d run state files are unreadable, so no report is deleted", list.Skipped)
	}
	owners := map[string]bool{}
	for _, st := range list.States {
		if st.Prefix != "ship" && st.Prefix != "execute" {
			continue
		}
		if id := execRunID(st.Data); id != "" {
			owners[id] = true
		}
	}
	return owners, nil
}

// clearTooNewReason returns the Skipped reason for an entry that is newer than
// minAge: "changed less than 24 hours ago" or "changed less than 30 minutes
// ago".
func clearTooNewReason(minAge time.Duration) string {
	if minAge >= time.Hour {
		return fmt.Sprintf("changed less than %d hours ago", int(minAge/time.Hour))
	}
	return fmt.Sprintf("changed less than %d minutes ago", int(minAge/time.Minute))
}

// clearEntrySize returns the size of a file, or the summed size of the regular
// files below a directory. Any error stops the walk and is returned.
func clearEntrySize(path string, info fs.FileInfo) (int64, error) {
	if !info.IsDir() {
		return info.Size(), nil
	}
	var total int64
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		total += fi.Size()
		return nil
	})
	return total, err
}
