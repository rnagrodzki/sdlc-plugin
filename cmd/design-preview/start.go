package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

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
	// reqFile is the requirements file that an approved design writes.
	reqFile = draftDir + "/requirements.md"
)

// maxBaseBytes is the largest base.json StartRule accepts.
const maxBaseBytes = 4 << 10

// baseCommitPattern is the shape of a full commit sha in base.json.
var baseCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

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

// Mode selects how StartRule treats an existing draft.
type Mode string

// The three modes of StartRule.
const (
	// ModeAuto picks the outcome from the state of the draft and the shipped page.
	ModeAuto Mode = "auto"
	// ModeFresh discards the draft and copies the shipped page.
	ModeFresh Mode = "fresh"
	// ModeContinue keeps the draft and moves its base commit to HEAD.
	ModeContinue Mode = "continue"
)

// validate returns nil for the three modes and an error that names m for any
// other value. parseMode and StartRule both call it, so the set and the text
// are in one place.
func (m Mode) validate() error {
	switch m {
	case ModeAuto, ModeFresh, ModeContinue:
		return nil
	}

	return fmt.Errorf("unknown mode %q. Allowed: auto, fresh, continue.", string(m))
}

// CaseID names the outcome of StartRule.
type CaseID string

// The outcomes of StartRule. Auto mode gives a, b, c or d. The other two
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
)

// marker returns the start of the marker line of c, for example
// "design preview: case a" or "design preview: fresh".
func (c CaseID) marker() string {
	switch c {
	case CaseFresh, CaseContinue:
		return "design preview: " + string(c)
	}

	return "design preview: case " + string(c)
}

// Outcome is the result of StartRule.
type Outcome struct {
	Case  CaseID
	Lines []string // marker lines, printed by run()
	Serve bool     // false only for case d
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

// nextLine is the last marker line of case d.
const nextLine = "design preview: next — run task design:fresh (discard the draft) or task design:continue (keep the draft)."

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
	case !staticExists:
		return startCopy(repoRoot, head, CaseA, "no draft. Copied the shipped page")
	}

	return startAuto(repoRoot, head)
}

// startContinue keeps the draft and writes head as its base commit.
func startContinue(repoRoot, head string, staticExists bool) (Outcome, error) {
	if !staticExists {
		return Outcome{}, errNoDraft
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
// base: an extra or missing file, a file with another blob id, or one or
// more dependency records. It compares blob ids, never file text, because
// execx.Run trims its output.
func startOwnWork(repoRoot, base string) (bool, error) {
	shipped, err := startShippedBlobs(repoRoot, base)
	if err != nil {
		return false, err
	}

	draft, err := startDraftFiles(repoRoot)
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

	return len(deps) > 0, nil
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

// startDraftFiles lists the files under staticDir, relative to that folder
// with slash separators and sorted. It skips each file that git ignores, for
// example .DS_Store.
func startDraftFiles(repoRoot string) ([]string, error) {
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
	if err := copyMkdirAll(filepath.Join(repoRoot, tmpStaticDir), 0o755); err != nil {
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
		dstRel := tmpStaticDir + "/" + rel
		dst := filepath.Join(repoRoot, filepath.FromSlash(dstRel))
		if err := copyMkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return pathError(dstRel, err)
		}
		if err := copyWriteFile(dst, data, 0o644); err != nil {
			return pathError(dstRel, err)
		}
	}

	return nil
}

// swapDraft moves tmpStaticDir to staticDir, resets the dependency file,
// removes the requirements file, and writes head to base.json last.
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
	if err := os.Remove(filepath.Join(repoRoot, reqFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return pathError(reqFile, err)
	}
	if err := writeBaseJSON(filepath.Join(repoRoot, baseFile), baseRecord{BaseCommit: head, ShippedPath: shippedPath}); err != nil {
		return fmt.Errorf("%s: %w", baseFile, err)
	}

	return nil
}
