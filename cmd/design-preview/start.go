package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
)

// shippedPath is the shipped dashboard page, relative to the repo root.
const shippedPath = "internal/dashboard/web/static"

// draftDir is the draft folder, relative to the repo root.
const draftDir = "design/dashboard"

// maxBaseBytes is the largest base.json StartRule accepts.
const maxBaseBytes = 4 << 10

// baseCommitPattern is the shape of a full commit sha in base.json.
var baseCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

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

// Outcome is the result of StartRule.
type Outcome struct {
	Case  string   // "a" | "b" | "c" | "d" | "fresh" | "continue"
	Lines []string // marker lines, printed by run()
	Serve bool     // false only for case d
}

// baseRecord is the content of design/dashboard/base.json.
type baseRecord struct {
	BaseCommit  string `json:"baseCommit"`  // ^[0-9a-f]{40}$
	ShippedPath string `json:"shippedPath"` // always shippedPath
}

// errMissingBase is returned when the draft has static/ but no base.json.
var errMissingBase = errors.New("design/dashboard/base.json is missing. Run task design:fresh (discard the draft) or task design:continue (keep the draft).")

// errBadBase is returned when base.json does not name a known commit.
var errBadBase = errors.New("design/dashboard/base.json: baseCommit is not a known commit. Run task design:fresh or task design:continue.")

// errDirty is returned when the shipped page has uncommitted changes.
var errDirty = errors.New("the shipped page has uncommitted changes. Commit or stash them, then run task design again.")

// errNoDraft is returned by the continue mode when there is no draft.
var errNoDraft = errors.New("no draft in design/dashboard/static/. Run task design first.")

// nextLine is the last marker line of case d.
const nextLine = "design preview: next — run task design:fresh (discard the draft) or task design:continue (keep the draft)."

// StartRule runs the checks in order, then at most one change. repoRoot is
// the git top level. No file changes before every check passes.
func StartRule(repoRoot string, mode Mode) (Outcome, error) {
	switch mode {
	case ModeAuto, ModeFresh, ModeContinue:
	default:
		return Outcome{}, fmt.Errorf("unknown mode %q", mode)
	}

	status, err := startGit(repoRoot, "status", "--porcelain", "--", shippedPath)
	if err != nil {
		return Outcome{}, err
	}
	if status != "" {
		return Outcome{}, errDirty
	}

	head, err := startGit(repoRoot, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return Outcome{}, err
	}

	staticRel := draftDir + "/static"
	staticExists, err := startIsDir(repoRoot, staticRel)
	if err != nil {
		return Outcome{}, err
	}

	switch mode {
	case ModeFresh:
		return startCopy(repoRoot, head, "fresh",
			fmt.Sprintf("design preview: fresh — copied the shipped page. Base commit %s.", head))
	case ModeContinue:
		if !staticExists {
			return Outcome{}, errNoDraft
		}
		baseRel := draftDir + "/base.json"
		if err := fsx.AtomicWriteJSON(filepath.Join(repoRoot, baseRel), baseRecord{BaseCommit: head, ShippedPath: shippedPath}); err != nil {
			return Outcome{}, fmt.Errorf("%s: %w", baseRel, err)
		}
		return Outcome{
			Case:  "continue",
			Lines: []string{fmt.Sprintf("design preview: continue — kept the draft. Base commit %s.", head)},
			Serve: true,
		}, nil
	}

	if !staticExists {
		return startCopy(repoRoot, head, "a",
			fmt.Sprintf("design preview: case a — no draft. Copied the shipped page. Base commit %s.", head))
	}

	base, err := startReadBase(repoRoot)
	if err != nil {
		return Outcome{}, err
	}
	short := base[:7]

	_, stderr, code, err := execx.RunAllowExit("git", []string{"diff", "--quiet", base, "HEAD", "--", shippedPath}, execx.Options{Dir: repoRoot})
	if err != nil {
		return Outcome{}, fmt.Errorf("%s: %w", shippedPath, err)
	}
	switch code {
	case 0:
		return Outcome{
			Case:  "b",
			Lines: []string{fmt.Sprintf("design preview: case b — shipped page unchanged since %s. Serving the draft.", short)},
			Serve: true,
		}, nil
	case 1:
	default:
		return Outcome{}, fmt.Errorf("%s: git diff exit %d: %s", shippedPath, code, stderr)
	}

	own, err := startOwnWork(repoRoot, base)
	if err != nil {
		return Outcome{}, err
	}
	if !own {
		return startCopy(repoRoot, head, "c",
			fmt.Sprintf("design preview: case c — shipped page changed, the draft has no own work. Copied the shipped page. Base commit %s.", head))
	}

	log, err := startGit(repoRoot, "log", "--oneline", "--no-color", base+"..HEAD", "--", shippedPath)
	if err != nil {
		return Outcome{}, err
	}
	lines := []string{fmt.Sprintf("design preview: case d — shipped page changed since %s, and the draft has own work. Nothing changed.", short)}
	if log != "" {
		lines = append(lines, strings.Split(log, "\n")...)
	}
	lines = append(lines, nextLine)

	return Outcome{Case: "d", Lines: lines, Serve: false}, nil
}

// startGit runs git in repoRoot and returns its trimmed output. A failure
// names the shipped path, because every git call of StartRule is about it.
func startGit(repoRoot string, args ...string) (string, error) {
	out, err := execx.Run("git", args, execx.Options{Dir: repoRoot})
	if err != nil {
		return "", fmt.Errorf("%s: %w", shippedPath, err)
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
		return false, startPathError(rel, err)
	}

	return info.IsDir(), nil
}

// startPathError gives "<rel>: <cause>". An *os.PathError repeats the
// absolute path, so only its cause is kept.
func startPathError(rel string, err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}

	return fmt.Errorf("%s: %w", rel, err)
}

// startReadBase reads base.json and returns its baseCommit. It returns
// errMissingBase for a missing file and errBadBase for a file over
// maxBaseBytes, bad JSON, a bad sha or a sha that is not a commit.
func startReadBase(repoRoot string) (string, error) {
	baseRel := draftDir + "/base.json"
	f, err := os.Open(filepath.Join(repoRoot, baseRel))
	if errors.Is(err, fs.ErrNotExist) {
		return "", errMissingBase
	}
	if err != nil {
		return "", startPathError(baseRel, err)
	}
	defer f.Close()

	// Read one byte more than the limit: a longer result means the file is too large.
	data, err := io.ReadAll(io.LimitReader(f, maxBaseBytes+1))
	if err != nil {
		return "", startPathError(baseRel, err)
	}
	if len(data) > maxBaseBytes {
		return "", errBadBase
	}

	var rec baseRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return "", errBadBase
	}
	if !baseCommitPattern.MatchString(rec.BaseCommit) {
		return "", errBadBase
	}

	_, _, code, err := execx.RunAllowExit("git", []string{"cat-file", "-e", rec.BaseCommit + "^{commit}"}, execx.Options{Dir: repoRoot})
	if err != nil {
		return "", fmt.Errorf("%s: %w", baseRel, err)
	}
	if code != 0 {
		return "", errBadBase
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
			args = append(args, draftDir+"/static/"+rel)
		}
		out, err := startGit(repoRoot, args...)
		if err != nil {
			return false, err
		}
		ids := strings.Split(out, "\n")
		if len(ids) != len(draft) {
			return false, fmt.Errorf("%s: git hash-object gave %d ids for %d files", draftDir+"/static", len(ids), len(draft))
		}
		for i, rel := range draft {
			if ids[i] != shipped[shippedPath+"/"+rel] {
				return true, nil
			}
		}
	}

	deps, err := LoadDependencies(filepath.Join(repoRoot, draftDir, "dependencies.json"))
	if err != nil {
		return false, err
	}

	return len(deps) > 0, nil
}

// startShippedBlobs maps each blob path under shippedPath at commit to its
// blob id.
func startShippedBlobs(repoRoot, commit string) (map[string]string, error) {
	out, err := startGit(repoRoot, "ls-tree", "-r", "-z", commit, "--", shippedPath)
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

// startDraftFiles lists the files under design/dashboard/static/, relative
// to that folder with slash separators and sorted. It skips each file that
// git ignores, for example .DS_Store.
func startDraftFiles(repoRoot string) ([]string, error) {
	staticRel := draftDir + "/static"
	root := filepath.Join(repoRoot, staticRel)

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
		return nil, startPathError(staticRel, err)
	}
	if len(files) == 0 {
		return files, nil
	}

	// check-ignore prints each ignored path. Exit 1 means no path is ignored.
	args := []string{"check-ignore", "--"}
	for _, rel := range files {
		args = append(args, staticRel+"/"+rel)
	}
	out, stderr, code, err := execx.RunAllowExit("git", args, execx.Options{Dir: repoRoot})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", staticRel, err)
	}
	if code != 0 && code != 1 {
		return nil, fmt.Errorf("%s: git check-ignore exit %d: %s", staticRel, code, stderr)
	}

	ignored := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if line != "" {
			ignored[line] = true
		}
	}

	kept := files[:0]
	for _, rel := range files {
		if !ignored[staticRel+"/"+rel] {
			kept = append(kept, rel)
		}
	}
	sort.Strings(kept)

	return kept, nil
}

// startCopy replaces the draft with the shipped page at HEAD and returns the
// outcome for caseName with line as its marker line. base.json goes first
// and comes back last, so an interrupted copy leaves no base.json.
func startCopy(repoRoot, head, caseName, line string) (Outcome, error) {
	baseRel := draftDir + "/base.json"
	tmpRel := draftDir + "/.static.tmp"
	staticRel := draftDir + "/static"
	depsRel := draftDir + "/dependencies.json"
	reqRel := draftDir + "/requirements.md"

	names, err := startGit(repoRoot, "ls-tree", "-r", "-z", "--name-only", "HEAD", "--", shippedPath)
	if err != nil {
		return Outcome{}, err
	}

	if err := os.Remove(filepath.Join(repoRoot, baseRel)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Outcome{}, startPathError(baseRel, err)
	}
	if err := os.RemoveAll(filepath.Join(repoRoot, tmpRel)); err != nil {
		return Outcome{}, startPathError(tmpRel, err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, tmpRel), 0o755); err != nil {
		return Outcome{}, startPathError(tmpRel, err)
	}
	for _, name := range strings.Split(names, "\x00") {
		if name == "" {
			continue
		}
		rel := strings.TrimPrefix(name, shippedPath+"/")
		// The source is the working tree: the dirty check proved it equals HEAD.
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(name)))
		if err != nil {
			return Outcome{}, startPathError(name, err)
		}
		// Plain writes are enough here: .static.tmp/ becomes visible only
		// through the rename below.
		dst := filepath.Join(repoRoot, filepath.FromSlash(tmpRel+"/"+rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return Outcome{}, startPathError(tmpRel+"/"+rel, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return Outcome{}, startPathError(tmpRel+"/"+rel, err)
		}
	}

	if err := os.RemoveAll(filepath.Join(repoRoot, staticRel)); err != nil {
		return Outcome{}, startPathError(staticRel, err)
	}
	if err := os.Rename(filepath.Join(repoRoot, tmpRel), filepath.Join(repoRoot, staticRel)); err != nil {
		return Outcome{}, startPathError(staticRel, err)
	}
	if err := fsx.AtomicWriteBytes(filepath.Join(repoRoot, depsRel), []byte("[]\n")); err != nil {
		return Outcome{}, fmt.Errorf("%s: %w", depsRel, err)
	}
	if err := os.Remove(filepath.Join(repoRoot, reqRel)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Outcome{}, startPathError(reqRel, err)
	}
	if err := fsx.AtomicWriteJSON(filepath.Join(repoRoot, baseRel), baseRecord{BaseCommit: head, ShippedPath: shippedPath}); err != nil {
		return Outcome{}, fmt.Errorf("%s: %w", baseRel, err)
	}

	return Outcome{Case: caseName, Lines: []string{line}, Serve: true}, nil
}
