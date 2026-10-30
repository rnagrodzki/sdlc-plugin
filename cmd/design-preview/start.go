package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
)

// Paths of the start rule, relative to the repo root, with slash separators.
const (
	// shippedPath is the shipped dashboard page.
	shippedPath = "internal/dashboard/web/static"
	// draftDir is the draft folder.
	draftDir = "design/dashboard"
	// baseFile records the commit the draft started from.
	baseFile = draftDir + "/base.json"
	// staticDir is the draft copy of the page.
	staticDir = draftDir + "/static"
	// tmpStaticDir holds a copy until it replaces staticDir.
	tmpStaticDir = draftDir + "/.static.tmp"
	// depsFile is the dependency file of the draft.
	depsFile = draftDir + "/dependencies.json"
	// requirementsFile is the requirements file that an approved design writes.
	requirementsFile = draftDir + "/requirements.md"
	// requestLogFile is the request log of the draft. The dashboard-design skill writes it.
	requestLogFile = draftDir + "/requests.md"
	// oldStaticDir holds the old draft during the swap.
	oldStaticDir = draftDir + "/.static.old"
	// mergeTmpDir holds the base and HEAD copies of the page files.
	mergeTmpDir = draftDir + "/.merge.tmp"
)

// Limits of the merge and of the marker check.
const (
	// mergeBinarySniff is how many leading bytes merge reads for a NUL byte.
	mergeBinarySniff = 8000
	// maxMergeFileBytes is the largest draft, base or HEAD file that merge reads.
	maxMergeFileBytes = 8 << 20
)

// Modes of the files and folders that the start rule creates.
const (
	// newFileMode is the mode of a new page file.
	newFileMode fs.FileMode = 0o644
	// newDirMode is the mode of a new folder.
	newDirMode fs.FileMode = 0o755
)

// maxBaseBytes is the largest base.json StartRule accepts.
const maxBaseBytes = 4 << 10

// maxLogBytes is the largest request log StartRule reads. A bigger file is a
// mistake, not a design record.
const maxLogBytes = 256 << 10

// baseCommitPattern is the shape of a full commit sha in base.json.
var baseCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// requestRowPattern is the start of a request row in the log: "| R<number> |".
// The header rows of the table do not match it.
var requestRowPattern = regexp.MustCompile(`(?m)^\|\s*R[0-9]+\s*\|`)

// gitRun and gitRunAllowExit run git for the start rule. Tests replace them
// to make one git call fail.
var (
	gitRun          = execx.Run
	gitRunAllowExit = execx.RunAllowExit
)

// File system calls of startCopy that tests replace to make one step fail.
var (
	copyReadFile  = os.ReadFile
	copyMkdirAll  = os.MkdirAll
	copyWriteFile = os.WriteFile
	copyRename    = os.Rename
	writeBaseJSON = fsx.AtomicWriteJSON
)

// gitBlob returns the raw bytes of path at rev. execx.Run trims its output
// with strings.TrimSpace, so merge reads blobs through this seam. A failure
// holds the stderr text of git.
var gitBlob = func(root, rev, path string) ([]byte, error) {
	data, err := exec.Command("git", "-C", root, "cat-file", "blob", rev+":"+path).Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
			return nil, fmt.Errorf("%w: %s", err, stderr)
		}
	}

	return data, err
}

// mergeRemoveAll is os.RemoveAll. Tests replace it to fail the clear of stale
// work folders, the delete in takeHead, and the cleanup after a merge.
var mergeRemoveAll = os.RemoveAll

// mergeMkdirTemp is os.MkdirTemp. Tests replace it to fail the backup folder.
var mergeMkdirTemp = os.MkdirTemp

// draftReadFile is os.ReadFile. Tests replace it to fail the read of a
// draft file.
var draftReadFile = os.ReadFile

// Mode selects how StartRule treats an existing draft.
type Mode string

// The four modes of StartRule.
const (
	// ModeAuto picks the outcome from the state of the draft and the shipped page.
	ModeAuto Mode = "auto"
	// ModeFresh discards the draft and copies the shipped page.
	ModeFresh Mode = "fresh"
	// ModeContinue keeps the draft and moves its base commit to HEAD.
	ModeContinue Mode = "continue"
	// ModeMerge merges the shipped page changes since the base commit into
	// the draft, keeps the own work, and moves the base to HEAD when clean.
	ModeMerge Mode = "merge"
)

// modes is the set of modes, in the order the texts list them. validate
// accepts only these, and a test compares usageText and modeFlagUsage with it.
var modes = []Mode{ModeAuto, ModeFresh, ModeContinue, ModeMerge}

// validate returns nil for a mode in modes and an error that names m and
// lists modes for any other value.
func (m Mode) validate() error {
	if slices.Contains(modes, m) {
		return nil
	}
	names := make([]string, len(modes))
	for i, mode := range modes {
		names[i] = string(mode)
	}

	return fmt.Errorf("unknown mode %q. Allowed: %s.", string(m), strings.Join(names, ", "))
}

// CaseID names the outcome of StartRule.
type CaseID string

// The outcomes of StartRule. Auto mode gives a, b, c or d. The other
// modes give their own name.
const (
	// CaseA: no draft. The shipped page is copied.
	CaseA CaseID = "a"
	// CaseB: the shipped page is unchanged since the base commit. The draft is served.
	CaseB CaseID = "b"
	// CaseC: the shipped page changed and the draft has no own work. The shipped page is copied.
	CaseC CaseID = "c"
	// CaseD: the shipped page changed and the draft has own work. Nothing changes.
	CaseD CaseID = "d"
	// CaseFresh: fresh mode copied the shipped page.
	CaseFresh CaseID = "fresh"
	// CaseContinue: continue mode kept the draft.
	CaseContinue CaseID = "continue"
	// CaseMerge: merge mode merged the shipped page into the draft.
	CaseMerge CaseID = "merge"
)

// marker returns the start of the marker line of c, for example
// "design preview: case a" or "design preview: fresh".
func (c CaseID) marker() string {
	switch c {
	case CaseFresh, CaseContinue, CaseMerge:
		return "design preview: " + string(c)
	}

	return "design preview: case " + string(c)
}

// Outcome is the result of StartRule.
type Outcome struct {
	Case      CaseID
	Lines     []string // marker lines, printed by run()
	Serve     bool     // false for case d and for a merge with conflicts
	Conflicts bool     // true only for a merge with conflicts
}

// baseRecord is the content of base.json.
type baseRecord struct {
	BaseCommit  string `json:"baseCommit"`  // ^[0-9a-f]{40}$
	ShippedPath string `json:"shippedPath"` // always shippedPath
}

// recoverBase is the recovery text of a missing or bad base.json.
const recoverBase = "Run task design:fresh (discard the draft) or task design:continue (keep the draft)."

// errMissingBase is returned when the draft has static/ but no base.json.
var errMissingBase = errors.New(baseFile + " is missing. " + recoverBase)

// errBadBase marks a base.json that does not name a known commit. badBase
// wraps it with the cause and the recovery text.
var errBadBase = errors.New(baseFile + ": baseCommit is not a known commit")

// errDirty is returned when the shipped page has uncommitted changes.
var errDirty = errors.New("the shipped page has uncommitted changes. Commit or stash them, then run task design again.")

// errNoDraft is returned by the continue mode when there is no draft.
var errNoDraft = errors.New("no draft in " + staticDir + "/. Run task design first.")

// errDraftSymlink: merge refuses a static/ folder that is a symlink or
// holds a symlink, because the swap moves the link and not the draft.
var errDraftSymlink = errors.New("the draft holds a symlink. Merge cannot keep it. Replace it with a file, then run task design:merge again.")

// nextLine is the last marker line of case d.
const nextLine = "design preview: next — run task design:merge (merge the shipped changes into the draft), task design:fresh (discard the draft) or task design:continue (keep the draft)."

// badBase returns errBadBase with cause and the recovery text.
func badBase(cause string) error {
	return fmt.Errorf("%w (%s). %s", errBadBase, cause, recoverBase)
}

// StartRule runs the checks in order, then at most one change. repoRoot is
// the git top level. No file changes before every check passes.
func StartRule(repoRoot string, mode Mode) (Outcome, error) {
	if err := mode.validate(); err != nil {
		return Outcome{}, err
	}

	status, err := startGit(repoRoot, shippedPath, "status", "--porcelain", "--", shippedPath)
	if err != nil {
		return Outcome{}, err
	}
	if status != "" {
		return Outcome{}, errDirty
	}

	head, err := startGit(repoRoot, "HEAD", "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return Outcome{}, err
	}

	staticExists, err := startIsDir(repoRoot, staticDir)
	if err != nil {
		return Outcome{}, err
	}

	switch {
	case mode == ModeFresh:
		return startCopy(repoRoot, head, CaseFresh, "copied the shipped page")
	case mode == ModeContinue:
		return startContinue(repoRoot, head, staticExists)
	case mode == ModeMerge:
		return startMerge(repoRoot, head, staticExists)
	case !staticExists:
		return startCopy(repoRoot, head, CaseA, "no draft. Copied the shipped page")
	}
	if err := refuseConflictMarkers(repoRoot); err != nil {
		return Outcome{}, err
	}

	return startAuto(repoRoot, head)
}

// startContinue keeps the draft and writes head as its base commit.
func startContinue(repoRoot, head string, staticExists bool) (Outcome, error) {
	if !staticExists {
		return Outcome{}, errNoDraft
	}
	if err := refuseConflictMarkers(repoRoot); err != nil {
		return Outcome{}, err
	}
	if err := writeBaseJSON(filepath.Join(repoRoot, baseFile), baseRecord{BaseCommit: head, ShippedPath: shippedPath}); err != nil {
		return Outcome{}, fmt.Errorf("%s: %w", baseFile, err)
	}

	return Outcome{
		Case:  CaseContinue,
		Lines: []string{fmt.Sprintf("%s — kept the draft. Base commit %s.", CaseContinue.marker(), head)},
		Serve: true,
	}, nil
}

// startAuto classifies an existing draft in auto mode: case b when the
// shipped page is unchanged since the base commit, case c when it changed
// and the draft has no own work, and case d otherwise.
func startAuto(repoRoot, head string) (Outcome, error) {
	base, err := startReadBase(repoRoot)
	if err != nil {
		return Outcome{}, err
	}

	changed, err := startShippedChanged(repoRoot, base)
	if err != nil {
		return Outcome{}, err
	}
	if !changed {
		return Outcome{
			Case:  CaseB,
			Lines: []string{fmt.Sprintf("%s — shipped page unchanged since %s. Serving the draft.", CaseB.marker(), base[:7])},
			Serve: true,
		}, nil
	}

	own, err := startOwnWork(repoRoot, base)
	if err != nil {
		return Outcome{}, err
	}
	if !own {
		return startCopy(repoRoot, head, CaseC, "shipped page changed, the draft has no own work. Copied the shipped page")
	}

	return startCaseD(repoRoot, base)
}

// startShippedChanged reports whether the shipped page differs between base
// and HEAD.
func startShippedChanged(repoRoot, base string) (bool, error) {
	_, stderr, code, err := gitRunAllowExit("git", []string{"diff", "--quiet", base, "HEAD", "--", shippedPath}, execx.Options{Dir: repoRoot})
	if err != nil {
		return false, fmt.Errorf("%s: %w", shippedPath, err)
	}
	switch code {
	case 0:
		return false, nil
	case 1:
		return true, nil
	}

	return false, fmt.Errorf("%s: git diff exit %d: %s", shippedPath, code, stderr)
}

// startCaseD returns case d: the marker line, the commits that changed the
// shipped page since base, and the next line. Nothing changes.
func startCaseD(repoRoot, base string) (Outcome, error) {
	log, err := startGit(repoRoot, shippedPath, "log", "--oneline", "--no-color", base+"..HEAD", "--", shippedPath)
	if err != nil {
		return Outcome{}, err
	}
	lines := []string{fmt.Sprintf("%s — shipped page changed since %s, and the draft has own work. Nothing changed.", CaseD.marker(), base[:7])}
	if log != "" {
		lines = append(lines, strings.Split(log, "\n")...)
	}
	lines = append(lines, nextLine)

	return Outcome{Case: CaseD, Lines: lines, Serve: false}, nil
}

// startGit runs git in repoRoot and returns its trimmed output. A failure is
// prefixed with label, the path or ref that the call is about.
func startGit(repoRoot, label string, args ...string) (string, error) {
	out, err := gitRun("git", args, execx.Options{Dir: repoRoot})
	if err != nil {
		return "", fmt.Errorf("%s: %w", label, err)
	}

	return out, nil
}

// startIsDir reports whether rel (relative to repoRoot) is a directory. A
// missing path is false. Any other stat failure is an error that names rel.
func startIsDir(repoRoot, rel string) (bool, error) {
	info, err := os.Stat(filepath.Join(repoRoot, rel))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, pathError(rel, err)
	}

	return info.IsDir(), nil
}

// startReadBase reads base.json and returns its baseCommit. It returns
// errMissingBase for a missing file. A file over maxBaseBytes, bad JSON, a
// bad sha or a sha that is not a commit gives errBadBase wrapped with the
// cause (see badBase). Any other open or read failure is an error that names
// base.json, and a failure to run git is an error that names base.json too.
func startReadBase(repoRoot string) (string, error) {
	data, err := readBounded(filepath.Join(repoRoot, baseFile), maxBaseBytes)
	var tooLarge *tooLargeError
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", errMissingBase
	case errors.As(err, &tooLarge):
		return "", badBase(tooLarge.Error())
	case err != nil:
		return "", pathError(baseFile, err)
	}

	var rec baseRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return "", badBase(err.Error())
	}
	if !baseCommitPattern.MatchString(rec.BaseCommit) {
		return "", badBase(fmt.Sprintf("%q is not a 40-character lowercase sha", rec.BaseCommit))
	}

	_, stderr, code, err := gitRunAllowExit("git", []string{"cat-file", "-e", rec.BaseCommit + "^{commit}"}, execx.Options{Dir: repoRoot})
	if err != nil {
		return "", fmt.Errorf("%s: %w", baseFile, err)
	}
	if code != 0 {
		cause := fmt.Sprintf("git cat-file exit %d", code)
		if stderr != "" {
			cause += ": " + stderr
		}
		return "", badBase(cause)
	}

	return rec.BaseCommit, nil
}

// startOwnWork reports whether the draft differs from the shipped page at
// base: an extra or missing file, a file with another blob id, one or more
// dependency records, or one or more rows in the request log. It compares
// blob ids, never file text, because execx.Run trims its output.
func startOwnWork(repoRoot, base string) (bool, error) {
	shipped, err := startShippedBlobs(repoRoot, base)
	if err != nil {
		return false, err
	}

	draft, err := startUnignoredFiles(repoRoot)
	if err != nil {
		return false, err
	}

	if len(draft) != len(shipped) {
		return true, nil
	}
	for _, rel := range draft {
		if _, ok := shipped[shippedPath+"/"+rel]; !ok {
			return true, nil
		}
	}

	if len(draft) > 0 {
		args := []string{"hash-object", "--"}
		for _, rel := range draft {
			args = append(args, staticDir+"/"+rel)
		}
		out, err := startGit(repoRoot, staticDir, args...)
		if err != nil {
			return false, err
		}
		ids := strings.Split(out, "\n")
		if len(ids) != len(draft) {
			return false, fmt.Errorf("%s: git hash-object gave %d ids for %d files", staticDir, len(ids), len(draft))
		}
		for i, rel := range draft {
			if ids[i] != shipped[shippedPath+"/"+rel] {
				return true, nil
			}
		}
	}

	deps, err := LoadDependencies(filepath.Join(repoRoot, filepath.FromSlash(depsFile)))
	if err != nil {
		return false, err
	}

	if len(deps) > 0 {
		return true, nil
	}

	return startHasRequests(repoRoot)
}

// startHasRequests reports whether the request log holds one or more request
// rows. A missing log has none. A log that cannot be read is an error.
func startHasRequests(repoRoot string) (bool, error) {
	data, err := readBounded(filepath.Join(repoRoot, filepath.FromSlash(requestLogFile)), maxLogBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, pathError(requestLogFile, err)
	}

	return requestRowPattern.Match(data), nil
}

// startShippedBlobs maps each blob path under shippedPath at commit to its
// blob id. An entry of another type, such as a submodule commit, is skipped.
func startShippedBlobs(repoRoot, commit string) (map[string]string, error) {
	out, err := startGit(repoRoot, shippedPath, "ls-tree", "-r", "-z", commit, "--", shippedPath)
	if err != nil {
		return nil, err
	}

	blobs := map[string]string{}
	for _, entry := range strings.Split(out, "\x00") {
		if entry == "" {
			continue
		}
		// An entry is "<mode> <type> <id>\t<path>".
		meta, path, ok := strings.Cut(entry, "\t")
		if !ok {
			return nil, fmt.Errorf("%s: unexpected git ls-tree entry %q", shippedPath, entry)
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		blobs[path] = fields[2]
	}

	return blobs, nil
}

// startUnignoredFiles lists the files under staticDir that git does not
// ignore, relative to that folder with slash separators and sorted. It skips
// each file that git ignores, for example .DS_Store, so that such a file is
// not own work. readDraftFiles is the other walk of staticDir: it keeps the
// ignored files, because a merge replaces the whole folder.
func startUnignoredFiles(repoRoot string) ([]string, error) {
	root := filepath.Join(repoRoot, filepath.FromSlash(staticDir))

	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, pathError(staticDir, err)
	}
	if len(files) == 0 {
		return files, nil
	}

	// check-ignore prints each ignored path. Exit 1 means no path is ignored.
	args := []string{"check-ignore", "--"}
	for _, rel := range files {
		args = append(args, staticDir+"/"+rel)
	}
	out, stderr, code, err := gitRunAllowExit("git", args, execx.Options{Dir: repoRoot})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", staticDir, err)
	}
	if code != 0 && code != 1 {
		return nil, fmt.Errorf("%s: git check-ignore exit %d: %s", staticDir, code, stderr)
	}

	ignored := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if line != "" {
			ignored[line] = true
		}
	}

	kept := files[:0]
	for _, rel := range files {
		if !ignored[staticDir+"/"+rel] {
			kept = append(kept, rel)
		}
	}
	sort.Strings(kept)

	return kept, nil
}

// startCopy replaces the draft with the shipped page at head and returns the
// outcome c with the marker line "<marker> — <summary>. Base commit <head>.".
// base.json goes first and comes back last, so an interrupted copy leaves no
// base.json.
func startCopy(repoRoot, head string, c CaseID, summary string) (Outcome, error) {
	// List the files before the first change, so a git failure changes nothing.
	names, err := startGit(repoRoot, shippedPath, "ls-tree", "-r", "-z", "--name-only", head, "--", shippedPath)
	if err != nil {
		return Outcome{}, err
	}

	if err := os.Remove(filepath.Join(repoRoot, baseFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Outcome{}, pathError(baseFile, err)
	}
	if err := copyShippedToTmp(repoRoot, names); err != nil {
		return Outcome{}, err
	}
	if err := swapDraft(repoRoot, head); err != nil {
		return Outcome{}, err
	}

	return Outcome{
		Case:  c,
		Lines: []string{fmt.Sprintf("%s — %s. Base commit %s.", c.marker(), summary, head)},
		Serve: true,
	}, nil
}

// copyShippedToTmp copies each shipped file in names (the NUL-separated
// output of git ls-tree --name-only) to tmpStaticDir, which it first empties.
func copyShippedToTmp(repoRoot, names string) error {
	if err := os.RemoveAll(filepath.Join(repoRoot, tmpStaticDir)); err != nil {
		return pathError(tmpStaticDir, err)
	}
	if err := copyMkdirAll(filepath.Join(repoRoot, tmpStaticDir), newDirMode); err != nil {
		return pathError(tmpStaticDir, err)
	}
	for _, name := range strings.Split(names, "\x00") {
		if name == "" {
			continue
		}
		rel := strings.TrimPrefix(name, shippedPath+"/")
		// The source is the working tree: the dirty check proved it equals HEAD.
		data, err := copyReadFile(filepath.Join(repoRoot, filepath.FromSlash(name)))
		if err != nil {
			return pathError(name, err)
		}
		// Plain writes are enough here: tmpStaticDir becomes visible only
		// through the rename in swapDraft.
		if err := writeMergeFile(repoRoot, tmpStaticDir+"/"+rel, data, newFileMode); err != nil {
			return err
		}
	}

	return nil
}

// swapDraft moves tmpStaticDir to staticDir, resets the dependency file,
// removes the requirements file and the request log, and writes head to
// base.json last.
func swapDraft(repoRoot, head string) error {
	if err := os.RemoveAll(filepath.Join(repoRoot, staticDir)); err != nil {
		return pathError(staticDir, err)
	}
	if err := copyRename(filepath.Join(repoRoot, tmpStaticDir), filepath.Join(repoRoot, staticDir)); err != nil {
		return pathError(staticDir, err)
	}
	if err := fsx.AtomicWriteBytes(filepath.Join(repoRoot, depsFile), []byte("[]\n")); err != nil {
		return fmt.Errorf("%s: %w", depsFile, err)
	}
	if err := os.Remove(filepath.Join(repoRoot, requirementsFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return pathError(requirementsFile, err)
	}
	if err := os.Remove(filepath.Join(repoRoot, requestLogFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return pathError(requestLogFile, err)
	}
	if err := writeBaseJSON(filepath.Join(repoRoot, baseFile), baseRecord{BaseCommit: head, ShippedPath: shippedPath}); err != nil {
		return fmt.Errorf("%s: %w", baseFile, err)
	}

	return nil
}

// draftFile is one regular file of the draft, with its bytes.
type draftFile struct {
	rel  string      // relative to staticDir, with slash separators
	mode fs.FileMode // permission bits
	data []byte      // the full content of the file
}

// readDraftFiles walks staticDir and reads each regular file once, in full,
// in walk order. The marker check, the backup, the merge copy and the
// per-file merge all use these bytes, so a file that changes during a merge
// cannot pass the check with one content and be copied with another. It
// keeps the files that git ignores, because a merge replaces the whole
// folder (startUnignoredFiles is the walk that skips them). It does not
// follow a symlink: with refuseSymlinks it returns errDraftSymlink wrapped
// with the link name, otherwise it skips the link. A staticDir that is a
// file, and an entry that is not a file, a folder or a symlink, is an error
// that names it.
func readDraftFiles(repoRoot string, refuseSymlinks bool) ([]draftFile, error) {
	root := mergePath(repoRoot, staticDir)

	var files []draftFile
	err := filepath.Walk(root, func(path string, info fs.FileInfo, err error) error {
		name := staticDir
		if path != root {
			name += "/" + filepath.ToSlash(path[len(root)+1:])
		}
		if err != nil {
			return pathError(name, err)
		}
		mode := info.Mode()
		switch {
		case mode&fs.ModeSymlink != 0 && refuseSymlinks:
			return fmt.Errorf("%s: %w", name, errDraftSymlink)
		case mode&fs.ModeSymlink != 0, mode.IsDir():
			return nil
		case path == root:
			return fmt.Errorf("%s is not a folder. The draft must be a folder.", name)
		case !mode.IsRegular():
			return fmt.Errorf("%s is not a regular file. The draft may hold only files and folders.", name)
		}
		data, err := draftReadFile(path)
		if err != nil {
			return pathError(name, err)
		}
		files = append(files, draftFile{rel: strings.TrimPrefix(name, staticDir+"/"), mode: mode.Perm(), data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

// isBinary reports whether data holds a NUL byte in its first
// mergeBinarySniff bytes.
func isBinary(data []byte) bool {
	if len(data) > mergeBinarySniff {
		data = data[:mergeBinarySniff]
	}

	return bytes.IndexByte(data, 0) >= 0
}

// hasConflictMarker reports whether a line of data starts with "<<<<<<< "
// or ">>>>>>> ".
func hasConflictMarker(data []byte) bool {
	for _, line := range bytes.Split(data, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("<<<<<<< ")) || bytes.HasPrefix(line, []byte(">>>>>>> ")) {
			return true
		}
	}

	return false
}

// draftConflictFiles returns the files of files, relative to staticDir and
// sorted, that hold a line that starts with "<<<<<<< " or ">>>>>>> ". It
// skips a binary file. A text file over maxMergeFileBytes is an error.
func draftConflictFiles(files []draftFile) ([]string, error) {
	found := []string{}
	for _, f := range files {
		if isBinary(f.data) {
			continue
		}
		if len(f.data) > maxMergeFileBytes {
			tooLarge := &tooLargeError{max: maxMergeFileBytes}
			return nil, fmt.Errorf("%s/%s is %s. The marker check cannot read it.", staticDir, f.rel, tooLarge.Error())
		}
		if hasConflictMarker(f.data) {
			found = append(found, f.rel)
		}
	}
	sort.Strings(found)

	return found, nil
}

// refuseConflictMarkers reads the draft without following a symlink and
// returns the error of refuseMarkersIn for it.
func refuseConflictMarkers(repoRoot string) error {
	files, err := readDraftFiles(repoRoot, false)
	if err != nil {
		return err
	}

	return refuseMarkersIn(files)
}

// refuseMarkersIn returns conflictMarkersError when a file of files holds
// conflict markers, and the error of draftConflictFiles when the check
// cannot run.
func refuseMarkersIn(draft []draftFile) error {
	files, err := draftConflictFiles(draft)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}

	names := make([]string, len(files))
	for i, rel := range files {
		names[i] = staticDir + "/" + rel
	}

	return conflictMarkersError(names)
}

// conflictMarkersError returns: the draft has conflict markers in <files,
// joined by ", ">. Fix them, then run task design:continue.
func conflictMarkersError(files []string) error {
	return fmt.Errorf("the draft has conflict markers in %s. Fix them, then run task design:continue.", strings.Join(files, ", "))
}

// mergeBackupDir creates design/dashboard/static.bak-<UTC 20060102T150405Z>-<random>
// with os.MkdirTemp, so two merges never get the same folder. It returns the
// folder relative to repoRoot, with slash separators.
func mergeBackupDir(repoRoot string, now time.Time) (string, error) {
	dir, err := mergeMkdirTemp(mergePath(repoRoot, draftDir), "static.bak-"+now.UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		return "", pathError(draftDir, err)
	}

	return draftDir + "/" + filepath.Base(dir), nil
}

// mergePath joins rel, a slash path relative to repoRoot, to repoRoot.
func mergePath(repoRoot, rel string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(rel))
}

// mergeInput is what the write steps of a merge need.
type mergeInput struct {
	base  string      // the base commit of the draft
	head  string      // the HEAD commit
	files []draftFile // the draft files
}

// mergeResult is the result of the per-file merge.
type mergeResult struct {
	merged    int      // files that took HEAD or merged clean
	conflicts []string // files with a conflict, relative to staticDir, sorted
}

// startMerge merges the shipped page changes between the base commit and
// head into the draft. It checks first that the draft exists, that base.json
// names a known commit, that the draft holds no symlink and no conflict
// marker, and that the shipped page changed. No file changes before every
// check passes.
func startMerge(repoRoot, head string, staticExists bool) (Outcome, error) {
	if !staticExists {
		return Outcome{}, errNoDraft
	}
	base, err := startReadBase(repoRoot)
	if err != nil {
		return Outcome{}, err
	}
	files, err := readDraftFiles(repoRoot, true)
	if err != nil {
		return Outcome{}, err
	}
	if err := refuseMarkersIn(files); err != nil {
		return Outcome{}, err
	}
	needed, err := mergeNeeded(repoRoot, base, head)
	if err != nil {
		return Outcome{}, err
	}
	if !needed {
		return Outcome{Case: CaseMerge, Lines: []string{CaseMerge.marker() + " — nothing to merge"}, Serve: true}, nil
	}

	return mergeWrite(repoRoot, mergeInput{base: base, head: head, files: files})
}

// mergeNeeded reports whether the shipped page changed between base and head.
func mergeNeeded(repoRoot, base, head string) (bool, error) {
	if head == base {
		return false, nil
	}

	return startShippedChanged(repoRoot, base)
}

// mergeWrite runs the write steps of a merge: remove stale work folders,
// back up the draft, merge into a copy, swap the copy in, write the base
// when clean, and remove the work folders. A failed step returns its error
// with the backup path when the backup exists.
func mergeWrite(repoRoot string, in mergeInput) (Outcome, error) {
	if err := mergeClearStale(repoRoot); err != nil {
		return Outcome{}, err
	}
	backup, err := mergeBackupDir(repoRoot, time.Now())
	if err != nil {
		return Outcome{}, err
	}
	if err := mergeCopyTree(repoRoot, in.files, backup); err != nil {
		return Outcome{}, withBackup(err, backup)
	}
	res, err := mergeBuild(repoRoot, in)
	if err != nil {
		return Outcome{}, withBackup(err, backup)
	}
	if err := mergeSwap(repoRoot, backup, in.head, len(res.conflicts) == 0); err != nil {
		return Outcome{}, err
	}

	out := mergeOutcome(res, in.head, backup)
	out.Lines = append(out.Lines, mergeCleanup(repoRoot)...)

	return out, nil
}

// withBackup adds the backup path to err.
func withBackup(err error, backup string) error {
	return fmt.Errorf("%w. Backup %s.", err, backup)
}

// mergeClearStale removes the work folders that an earlier merge left.
// static/ exists here, so .static.old/ is not the only copy of the draft.
func mergeClearStale(repoRoot string) error {
	for _, rel := range []string{mergeTmpDir, tmpStaticDir, oldStaticDir} {
		if err := mergeRemoveAll(mergePath(repoRoot, rel)); err != nil {
			return pathError(rel, err)
		}
	}

	return nil
}

// mergeCopyTree writes the bytes of the draft files to dstRel, with the mode
// of each draft file.
func mergeCopyTree(repoRoot string, files []draftFile, dstRel string) error {
	if err := copyMkdirAll(mergePath(repoRoot, dstRel), newDirMode); err != nil {
		return pathError(dstRel, err)
	}
	for _, f := range files {
		if err := writeMergeFile(repoRoot, dstRel+"/"+f.rel, f.data, f.mode); err != nil {
			return err
		}
	}

	return nil
}

// writeMergeFile writes data to name, a slash path relative to repoRoot,
// and creates its parent folders. An existing file keeps its mode; a new
// file gets mode.
func writeMergeFile(repoRoot, name string, data []byte, mode fs.FileMode) error {
	path := mergePath(repoRoot, name)
	if err := copyMkdirAll(filepath.Dir(path), newDirMode); err != nil {
		return pathError(name, err)
	}
	if err := copyWriteFile(path, data, mode); err != nil {
		return pathError(name, err)
	}

	return nil
}

// mergeBuild copies the draft to tmpStaticDir, then merges into that copy
// each shipped file that changed between the base commit and HEAD.
func mergeBuild(repoRoot string, in mergeInput) (mergeResult, error) {
	baseBlobs, err := startShippedBlobs(repoRoot, in.base)
	if err != nil {
		return mergeResult{}, err
	}
	headBlobs, err := startShippedBlobs(repoRoot, in.head)
	if err != nil {
		return mergeResult{}, err
	}
	if err := mergeCopyTree(repoRoot, in.files, tmpStaticDir); err != nil {
		return mergeResult{}, err
	}

	job := mergeJob{repoRoot: repoRoot, in: in, baseBlobs: baseBlobs, headBlobs: headBlobs, draft: map[string][]byte{}}
	for _, f := range in.files {
		job.draft[f.rel] = f.data
	}

	var res mergeResult
	for _, rel := range mergeChangedFiles(baseBlobs, headBlobs) {
		action, err := job.file(rel)
		if err != nil {
			return mergeResult{}, err
		}
		switch action {
		case mergeConflict:
			res.conflicts = append(res.conflicts, rel)
		case mergeTakeHead, mergeClean:
			res.merged++
		}
	}

	return res, nil
}

// mergeChangedFiles returns the shipped files, relative to shippedPath and
// sorted, that were changed, added or deleted between the two blob maps.
func mergeChangedFiles(baseBlobs, headBlobs map[string]string) []string {
	changed := map[string]bool{}
	for path, id := range baseBlobs {
		if headBlobs[path] != id {
			changed[path] = true
		}
	}
	for path, id := range headBlobs {
		if baseBlobs[path] != id {
			changed[path] = true
		}
	}

	rels := make([]string, 0, len(changed))
	for path := range changed {
		rels = append(rels, strings.TrimPrefix(path, shippedPath+"/"))
	}
	sort.Strings(rels)

	return rels
}

// mergeAction is what merge does with one file.
type mergeAction int

// The actions of the per-file merge.
const (
	// mergeKeep keeps the draft file.
	mergeKeep mergeAction = iota
	// mergeTakeHead writes, adds or deletes the file as in HEAD.
	mergeTakeHead
	// mergeRun runs git merge-file.
	mergeRun
	// mergeClean is a git merge-file run with no conflict.
	mergeClean
	// mergeConflict keeps the draft file, or the merge-file result with
	// markers, and reports the file.
	mergeConflict
)

// mergeVersion is one version of a page file: absent, or its bytes.
type mergeVersion struct {
	data    []byte
	present bool
}

// over reports whether v is over maxMergeFileBytes.
func (v mergeVersion) over() bool {
	return len(v.data) > maxMergeFileBytes
}

// same reports whether v and o are both absent, or both present with the
// same bytes. A version over maxMergeFileBytes is never the same.
func (v mergeVersion) same(o mergeVersion) bool {
	if v.present != o.present {
		return false
	}

	return !v.over() && bytes.Equal(v.data, o.data)
}

// mergeDecide picks the action for a file that HEAD changed, added or
// deleted since the base commit, from its draft, base and HEAD versions.
func mergeDecide(draft, base, head mergeVersion) mergeAction {
	switch {
	case draft.same(base):
		return mergeTakeHead
	case draft.same(head):
		return mergeKeep
	case !draft.present || !head.present:
		return mergeConflict
	case draft.over() || base.over() || head.over():
		return mergeConflict
	case isBinary(draft.data) || isBinary(base.data) || isBinary(head.data):
		return mergeConflict
	}

	return mergeRun
}

// mergeJob holds the inputs of the per-file merge.
type mergeJob struct {
	repoRoot  string
	in        mergeInput
	baseBlobs map[string]string // shipped blob ids at the base commit, by path
	headBlobs map[string]string // shipped blob ids at HEAD, by path
	draft     map[string][]byte // bytes of the draft files, by path relative to staticDir
}

// file merges one shipped file, rel relative to shippedPath, into the copy
// in tmpStaticDir and returns the action it took.
func (j mergeJob) file(rel string) (mergeAction, error) {
	data, ok := j.draft[rel]
	draft := mergeVersion{data: data, present: ok}
	base, err := j.blobVersion(j.in.base, j.baseBlobs, rel)
	if err != nil {
		return 0, err
	}
	head, err := j.blobVersion(j.in.head, j.headBlobs, rel)
	if err != nil {
		return 0, err
	}

	action := mergeDecide(draft, base, head)
	switch action {
	case mergeTakeHead:
		return action, j.takeHead(rel, head)
	case mergeRun:
		return j.run(rel, base, head)
	}

	return action, nil
}

// blobVersion reads the version of rel at rev, whose blob ids are blobs.
func (j mergeJob) blobVersion(rev string, blobs map[string]string, rel string) (mergeVersion, error) {
	path := shippedPath + "/" + rel
	if _, ok := blobs[path]; !ok {
		return mergeVersion{}, nil
	}
	data, err := gitBlob(j.repoRoot, rev, path)
	if err != nil {
		return mergeVersion{}, fmt.Errorf("%s: %w", path, err)
	}

	return mergeVersion{data: data, present: true}, nil
}

// takeHead writes or deletes the copy of rel in tmpStaticDir as in HEAD.
func (j mergeJob) takeHead(rel string, head mergeVersion) error {
	name := tmpStaticDir + "/" + rel
	if head.present {
		return writeMergeFile(j.repoRoot, name, head.data, newFileMode)
	}
	if err := mergeRemoveAll(mergePath(j.repoRoot, name)); err != nil {
		return pathError(name, err)
	}

	return nil
}

// run writes the base and HEAD versions of rel to mergeTmpDir, then runs
// git merge-file on the copy in tmpStaticDir. Exit 0 is clean, exit 1 to
// 127 is the conflict count, and any other exit is an error.
func (j mergeJob) run(rel string, base, head mergeVersion) (mergeAction, error) {
	baseName, headName := mergeTmpDir+"/base/"+rel, mergeTmpDir+"/head/"+rel
	if err := writeMergeFile(j.repoRoot, baseName, base.data, newFileMode); err != nil {
		return 0, err
	}
	if err := writeMergeFile(j.repoRoot, headName, head.data, newFileMode); err != nil {
		return 0, err
	}

	name := staticDir + "/" + rel
	args := []string{"merge-file", "-q", "-L", "draft", "-L", "base", "-L", "HEAD",
		mergePath(j.repoRoot, tmpStaticDir+"/"+rel), mergePath(j.repoRoot, baseName), mergePath(j.repoRoot, headName)}
	_, stderr, code, err := gitRunAllowExit("git", args, execx.Options{Dir: j.repoRoot})
	switch {
	case err != nil:
		return 0, fmt.Errorf("%s: %w", name, err)
	case code == 0:
		return mergeClean, nil
	case code >= 1 && code <= 127:
		return mergeConflict, nil
	}

	return 0, fmt.Errorf("%s: git merge-file exit %d: %s", name, code, stderr)
}

// mergeSwap renames static/ to .static.old/ and .static.tmp/ to static/,
// then, when clean, writes head to base.json. A failed step after the first
// rename moves the old draft back (see mergeRestore).
func mergeSwap(repoRoot, backup, head string, clean bool) error {
	static, old, tmp := mergePath(repoRoot, staticDir), mergePath(repoRoot, oldStaticDir), mergePath(repoRoot, tmpStaticDir)
	if err := copyRename(static, old); err != nil {
		return withBackup(pathError(staticDir, err), backup)
	}
	if err := copyRename(tmp, static); err != nil {
		return mergeRestore(pathError(staticDir, err), backup, []rename{{from: old, to: static}})
	}
	if !clean {
		return nil
	}
	if err := writeBaseJSON(mergePath(repoRoot, baseFile), baseRecord{BaseCommit: head, ShippedPath: shippedPath}); err != nil {
		return mergeRestore(fmt.Errorf("%s: %w", baseFile, err), backup, []rename{{from: static, to: tmp}, {from: old, to: static}})
	}

	return nil
}

// rename is one folder move of mergeRestore.
type rename struct {
	from, to string
}

// mergeRestore runs the renames that move the old draft back after stepErr.
// It returns stepErr with the backup path, or, when a rename fails, both
// errors and the text that tells how to restore the draft.
func mergeRestore(stepErr error, backup string, renames []rename) error {
	for _, r := range renames {
		if err := copyRename(r.from, r.to); err != nil {
			return fmt.Errorf("%w. The restore failed too: %w. Restore the draft: copy %s to %s.", stepErr, err, backup, staticDir)
		}
	}

	return withBackup(stepErr, backup)
}

// mergeOutcome gives the outcome of a finished merge: the clean line, or
// the conflict count line and one line for each file with a conflict.
func mergeOutcome(res mergeResult, head, backup string) Outcome {
	if len(res.conflicts) == 0 {
		return Outcome{
			Case:  CaseMerge,
			Lines: []string{fmt.Sprintf("%s — merged %d files. Base commit %s. Backup %s.", CaseMerge.marker(), res.merged, head, backup)},
			Serve: true,
		}
	}

	lines := []string{fmt.Sprintf("%s — %d conflicts. Backup %s.", CaseMerge.marker(), len(res.conflicts), backup)}
	for _, rel := range res.conflicts {
		lines = append(lines, "design preview: conflict — "+staticDir+"/"+rel)
	}

	return Outcome{Case: CaseMerge, Lines: lines, Conflicts: true}
}

// mergeCleanup removes .static.old/ and .merge.tmp/ after a merge. A failed
// remove gives one warning line; the next merge removes the rest.
func mergeCleanup(repoRoot string) []string {
	var failed []string
	for _, rel := range []string{oldStaticDir, mergeTmpDir} {
		if err := mergeRemoveAll(mergePath(repoRoot, rel)); err != nil {
			failed = append(failed, pathError(rel, err).Error())
		}
	}
	if len(failed) == 0 {
		return nil
	}

	return []string{"design preview: warning — could not remove " + strings.Join(failed, "; ") + ". The next merge removes the rest."}
}
