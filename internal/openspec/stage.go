// Staging of OpenSpec artifacts outside the repository's tracked tree.
//
// Plan mode forbids tracked-file writes, so plan authors a change's artifacts
// into <main-worktree>/.sdlc-v2/openspec-staging/<change>/ (gitignored) and
// a later run start in any worktree of the repo materializes them into
// openspec/changes/<change>/ of that worktree.
//
// Both entry points here need the CLI to describe a change that does not
// exist yet, and the CLI can only describe an existing change. So each one
// works in a throwaway temp directory that holds a copy of
// openspec/config.yaml, runs `openspec new change <change>` there, and asks
// the CLI about that temp change. The repository itself is never touched by
// the CLI, and the temp directory is removed on every return path.
package openspec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ErrInvalidChangeName is returned by Stage and PrepareInstructions when the
// change name is not a bare kebab-case name (see ValidChangeName).
var ErrInvalidChangeName = errors.New("invalid change name")

// ErrPathNotAllowed is returned by Stage when a file path is absolute, is not
// a clean relative path (contains "..", ".", or empty segments), repeats an
// earlier path, or matches none of the artifact outputPath patterns the CLI
// reports for the project's schema. The wrapping error names the path.
var ErrPathNotAllowed = errors.New("path not allowed")

// ErrTargetSpec is returned by Stage when the current spec of a capability
// that a staged delta names cannot be copied into the temp directory: the
// file is over maxTargetSpecBytes (ErrTargetSpecTooLarge), or reading it
// fails for a reason other than the file being absent. The wrapping error
// names the spec path.
var ErrTargetSpec = errors.New("openspec stage: target spec")

// ErrTargetSpecTooLarge is returned by Stage when a current spec is over
// maxTargetSpecBytes. It wraps ErrTargetSpec, so errors.Is(err, ErrTargetSpec)
// is true for it too.
var ErrTargetSpecTooLarge = fmt.Errorf("%w: too large", ErrTargetSpec)

// maxTargetSpecBytes is the size limit for one current spec that Stage copies
// into the temp directory (1 MiB).
const maxTargetSpecBytes = 1 << 20

// ErrStagedByOtherPlan is returned by Stage when the staging dir of the
// change holds a stage.json whose planPath names another plan file. Stage
// then writes nothing, so the staging of the other plan stays.
var ErrStagedByOtherPlan = errors.New("openspec stage: staged by another plan")

// StagedByOtherPlanError is the error Stage returns for ErrStagedByOtherPlan.
// PlanPath is the planPath stored in the stage.json of the other plan.
// errors.Is(err, ErrStagedByOtherPlan) is true for it; read PlanPath with
// errors.As.
type StagedByOtherPlanError struct {
	PlanPath string
}

func (e *StagedByOtherPlanError) Error() string {
	return ErrStagedByOtherPlan.Error() + ": " + e.PlanPath
}

func (e *StagedByOtherPlanError) Unwrap() error { return ErrStagedByOtherPlan }

// ErrStagingLocked is returned by Stage when the lock file
// <stagingRoot>/.sdlc-v2/openspec-staging/<change>.lock exists and is younger
// than stagingLockStaleAfter: another Stage call for the same change is
// running. The wrapping error names the lock path (see StagingLockPath).
// Stage then writes nothing.
var ErrStagingLocked = errors.New("openspec stage: staging is locked by another stage")

// stagingLockStaleAfter is the lock age from which Stage treats a lock as
// left by a crashed stage: it deletes the lock and takes it again.
const stagingLockStaleAfter = 10 * time.Minute

// ErrStageManifestTooLarge is returned by ReadStageManifest (and so by every
// reader of stage.json that uses it) when stage.json is over
// stageManifestMaxBytes.
var ErrStageManifestTooLarge = errors.New("stage.json too large")

// stageManifestMaxBytes is the size limit of a stage.json that
// ReadStageManifest reads (1 MiB).
const stageManifestMaxBytes = 1 << 20

// Test seams for the persisted IO of Stage. Production code uses the os
// functions.
var (
	stageRename     = os.Rename    // the two renames and the move-back of the old staging
	stageRemoveLock = os.Remove    // the lock delete at the end of a stage
	stageRemoveAll  = os.RemoveAll // the crash-leftover delete and RemoveAll(old)
	stageWriteFile  = os.WriteFile // the file and stage.json writes into the temp folder
	stageOpenLock   = os.OpenFile  // the O_CREATE|O_EXCL lock create
)

// StageIOError is the error Stage returns when a persisted IO step under the
// staging root fails. Op is "lock" (the lock create or its Stat), "read" (the
// list of the openspec-staging folder), "remove" (the delete of a crash
// leftover), "write" (a write into the temp folder) or "rename". Path is the file or folder of the failed step; To is the rename
// target (rename only). Dir is the openspec-staging folder. When Stage
// returns a StageIOError, the staging of the change is unchanged and the lock
// that this call took is deleted.
type StageIOError struct {
	Op   string
	Path string
	To   string
	Dir  string
	Err  error
}

func (e *StageIOError) Error() string {
	if e.Op == "rename" {
		return fmt.Sprintf("openspec stage: rename %s to %s: %v", e.Path, e.To, e.Err)
	}
	return fmt.Sprintf("openspec stage: %s %s: %v", e.Op, e.Path, e.Err)
}

func (e *StageIOError) Unwrap() error { return e.Err }

// StagingLockPath returns the lock file that Stage uses for change:
// <stagingRoot>/.sdlc-v2/openspec-staging/<change>.lock.
func StagingLockPath(stagingRoot, change string) string {
	return filepath.Join(stagingParentDir(stagingRoot), change+".lock")
}

// stagingParentDir returns <stagingRoot>/.sdlc-v2/openspec-staging, the
// folder that holds the staging folders, the lock files and the temp folders.
func stagingParentDir(stagingRoot string) string {
	return filepath.Join(stagingRoot, paths.DataDir, paths.OpenspecStagingSubdir)
}

// StageManifestFile is the manifest file name written at the root of a
// change's staging directory.
const StageManifestFile = "stage.json"

// StageFile is one artifact file to stage, with its path relative to the
// change directory.
type StageFile struct {
	Path    string `json:"path" jsonschema_description:"Plain text path relative to the change dir. Example: specs/user-auth/spec.md"`
	Content string `json:"content" jsonschema_description:"Plain text file content (Markdown). Example: # Proposal"`
}

// StagedFile is one staged artifact file as recorded in stage.json: its path
// relative to the change directory and the hex SHA-256 of its content.
type StagedFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// StageManifest is the stage.json manifest. ValidatedAt is set only when
// `openspec validate <change> --strict` exited 0 on a temp copy of the
// staged change.
type StageManifest struct {
	Change      string       `json:"change"`
	Schema      string       `json:"schema"`
	PlanPath    string       `json:"planPath,omitempty"`
	Files       []StagedFile `json:"files"`
	ValidatedAt string       `json:"validatedAt,omitempty"`
}

// StageResult is the outcome of Stage. StagingDir is the staging directory
// relative to the staging root (the main worktree), exactly as StagingDirRel returns it
// (the form the plan's **OpenSpec-Staging:** header uses). ValidateOutput is
// the combined stdout/stderr of `openspec validate <change> --strict`.
// Replaced is true when a staging directory of the change existed before
// this call and Stage replaced it, false when Stage created it. PlanPath is
// the planPath written to stage.json (the owner key of the staging).
// Warnings holds one line for each cleanup step that failed after the new
// staging was in place: "old staging <path> not deleted: <err>" and
// "lock <path> not deleted: <err>". The stage itself succeeded.
type StageResult struct {
	StagingDir     string
	Files          []StagedFile
	Valid          bool
	ValidateOutput string
	Replaced       bool
	PlanPath       string
	Warnings       []string
}

// ArtifactGuide is the authoring guidance for one artifact of a change,
// merging `openspec status` (ID, OutputPath, Requires) with `openspec
// instructions` (Template, Instruction, Context, Rules).
type ArtifactGuide struct {
	ID          string   `json:"id"`
	OutputPath  string   `json:"outputPath"`
	Requires    []string `json:"requires"`
	Template    string   `json:"template"`
	Instruction string   `json:"instruction"`
	Context     string   `json:"context"`
	Rules       []string `json:"rules"`
}

// StagingDirRel returns the staging directory for change, relative to the
// staging root (the main worktree), with a trailing slash:
// ".sdlc-v2/openspec-staging/<change>/".
func StagingDirRel(change string) string {
	return paths.DataDir + "/" + paths.OpenspecStagingSubdir + "/" + change + "/"
}

// PrepareInstructions returns the schema name and the authoring guidance for
// every artifact of change, in the CLI's `status` artifacts order. It runs
// the CLI only in a temp directory (see the file comment); nothing under
// activeRoot is written.
func PrepareInstructions(activeRoot, change string) (schema string, guides []ArtifactGuide, err error) {
	if !ValidChangeName(change) {
		return "", nil, fmt.Errorf("%w: %q", ErrInvalidChangeName, change)
	}
	tmp, cleanup, err := newTempChange(activeRoot, change)
	if err != nil {
		return "", nil, err
	}
	defer cleanup()

	status, err := Status(tmp, change)
	if err != nil {
		return "", nil, err
	}
	for _, a := range status.Artifacts {
		ins, err := Instructions(tmp, a.ID, change)
		if err != nil {
			return "", nil, err
		}
		guides = append(guides, ArtifactGuide{
			ID:          a.ID,
			OutputPath:  a.OutputPath,
			Requires:    a.Requires,
			Template:    ins.Template,
			Instruction: ins.Instruction,
			Context:     ins.Context,
			Rules:       ins.Rules,
		})
	}
	return status.SchemaName, guides, nil
}

// Stage writes files into <stagingRoot>/.sdlc-v2/openspec-staging/<change>/,
// replacing any earlier staging of the same change, writes stage.json, and
// validates a temp copy of the staged change with `openspec validate
// <change> --strict`. It reads openspec/config.yaml and the current specs from
// activeRoot.
//
// Before validating, Stage copies the current spec of each capability that a
// staged specs/<capability>/spec.md names into the temp directory, so a
// MODIFIED delta is checked against the current spec the same way as at ship.
// A capability with no current spec is a new capability and copies nothing.
//
// Every check runs before the first write under stagingRoot: the change name
// (ErrInvalidChangeName), each file path against the outputPath patterns the
// CLI reports for a temp change (ErrPathNotAllowed), the planPath of an
// existing stage.json of the change (ErrStagedByOtherPlan), and the copy of
// the current specs (ErrTargetSpec).
//
// Each artifact that the CLI lists for the schema must match a staged file.
// A missing artifact, like a failed validation, is not an error: the result
// has Valid false, ValidateOutput starts with one "missing artifact
// <outputPath>" line for each gap, and stage.json has no validatedAt.
//
// The write claims the staging atomically. Stage creates the lock file
// <change>.lock next to the staging folders with O_CREATE|O_EXCL
// (ErrStagingLocked when a lock younger than stagingLockStaleAfter exists;
// an older lock is deleted and taken again). Under the lock it checks the
// owner again, deletes the .tmp-<change>-<n> and .old-<change>-<n> folders
// that a crashed stage left, writes the files and stage.json into a new
// .tmp-<change>-<n> folder, and renames that folder into place (an existing
// staging folder moves aside to .old-<change>-<n> first and is deleted
// after). A failed IO step returns a *StageIOError, keeps the staging of the
// change unchanged and deletes the lock. now defaults to time.Now when nil;
// it is also the clock of the lock age.
func Stage(activeRoot, stagingRoot, change string, files []StageFile, planPath string, now func() time.Time) (StageResult, error) {
	if now == nil {
		now = time.Now
	}
	if err := checkStageInput(stagingRoot, change, files, planPath); err != nil {
		return StageResult{}, err
	}

	tmp, cleanup, err := newTempChange(activeRoot, change)
	if err != nil {
		return StageResult{}, err
	}
	defer cleanup()

	status, err := Status(tmp, change)
	if err != nil {
		return StageResult{}, err
	}
	var patterns []string
	for _, a := range status.Artifacts {
		if a.OutputPath != "" {
			patterns = append(patterns, a.OutputPath)
		}
	}
	for _, f := range files {
		if !matchesAny(patterns, f.Path) {
			return StageResult{}, fmt.Errorf("%w: %q matches no artifact outputPath (%s)", ErrPathNotAllowed, f.Path, strings.Join(patterns, ", "))
		}
	}

	if err := copyTargetSpecs(activeRoot, tmp, files); err != nil {
		return StageResult{}, err
	}

	// Validate a copy of the staged change inside the temp change dir that
	// `openspec new change` created (it keeps its .openspec.yaml). This
	// writes nothing under stagingRoot, so it runs before the lock.
	tmpChangeDir := filepath.Join(tmp, "openspec", "changes", change)
	for _, f := range files {
		if err := writeFile(filepath.Join(tmpChangeDir, filepath.FromSlash(f.Path)), f.Content); err != nil {
			return StageResult{}, err
		}
	}
	stdout, stderr, exit, err := runCLI(tmp, "validate", change, "--strict")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return StageResult{}, ErrCLINotFound
		}
		return StageResult{}, fmt.Errorf("openspec validate %s --strict: %w", change, err)
	}
	var lines []string
	missing := missingArtifacts(status.Artifacts, files)
	for _, m := range missing {
		lines = append(lines, "missing artifact "+m)
	}
	if out := strings.TrimSpace(strings.Join([]string{stdout, stderr}, "\n")); out != "" {
		lines = append(lines, out)
	}

	staged := make([]StagedFile, 0, len(files))
	for _, f := range files {
		sum := sha256.Sum256([]byte(f.Content))
		staged = append(staged, StagedFile{Path: f.Path, SHA256: hex.EncodeToString(sum[:])})
	}
	manifest := StageManifest{
		Change:   change,
		Schema:   status.SchemaName,
		PlanPath: planPath,
		Files:    staged,
	}
	valid := exit == 0 && len(missing) == 0
	if valid {
		manifest.ValidatedAt = now().UTC().Format(time.RFC3339)
	}

	lock, err := acquireStagingLock(stagingRoot, change, now)
	if err != nil {
		return StageResult{}, err
	}
	replaced, warnings, err := writeStagingLocked(stagingRoot, change, files, manifest)
	if rmErr := stageRemoveLock(lock); rmErr != nil && err == nil {
		warnings = append(warnings, fmt.Sprintf("lock %s not deleted: %v", lock, rmErr))
	}
	if err != nil {
		return StageResult{}, err
	}
	return StageResult{
		StagingDir:     StagingDirRel(change),
		Files:          staged,
		Valid:          valid,
		ValidateOutput: strings.Join(lines, "\n"),
		Replaced:       replaced,
		PlanPath:       planPath,
		Warnings:       warnings,
	}, nil
}

// missingArtifacts returns the outputPath of each artifact that no staged
// file matches, in the CLI's artifacts order. An artifact with no outputPath
// is skipped.
func missingArtifacts(artifacts []ArtifactStatus, files []StageFile) []string {
	var missing []string
	for _, a := range artifacts {
		if a.OutputPath == "" {
			continue
		}
		found := false
		for _, f := range files {
			if matchOutputPath(a.OutputPath, f.Path) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, a.OutputPath)
		}
	}
	return missing
}

// acquireStagingLock creates the openspec-staging folder when it is missing
// and creates the lock file of change there with O_CREATE|O_EXCL. A lock
// that exists and is younger than stagingLockStaleAfter (by now) returns an
// error that wraps ErrStagingLocked and names the lock. An older lock is
// deleted and created once more; a second clash returns ErrStagingLocked
// too. Any other failure returns a *StageIOError with Op "lock".
func acquireStagingLock(stagingRoot, change string, now func() time.Time) (string, error) {
	dir := stagingParentDir(stagingRoot)
	lock := StagingLockPath(stagingRoot, change)
	lockErr := func(p string, err error) error {
		return &StageIOError{Op: "lock", Path: p, Dir: dir, Err: err}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", lockErr(dir, err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		f, err := stageOpenLock(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, werr := fmt.Fprintf(f, "pid %d\n", os.Getpid())
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				_ = os.Remove(lock)
				return "", lockErr(lock, werr)
			}
			return lock, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", lockErr(lock, err)
		}
		info, err := os.Stat(lock)
		if errors.Is(err, fs.ErrNotExist) {
			continue // the other stage released the lock meanwhile
		}
		if err != nil {
			return "", lockErr(lock, err)
		}
		if attempt > 0 || now().Sub(info.ModTime()) < stagingLockStaleAfter {
			break
		}
		if err := os.Remove(lock); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", lockErr(lock, err)
		}
	}
	return "", fmt.Errorf("%w: %s", ErrStagingLocked, lock)
}

// writeStagingLocked runs the steps of Stage that need the lock: the owner
// check (*StagedByOtherPlanError), the delete of crash leftovers, the write
// of files and manifest into a new temp folder, and the renames that put the
// temp folder into place. It returns whether a staging folder existed before
// and the warnings of a failed delete of the old staging. On error the
// staging of the change is unchanged and the temp folder is removed.
func writeStagingLocked(stagingRoot, change string, files []StageFile, manifest StageManifest) (replaced bool, warnings []string, err error) {
	if other := stagedPlanPath(stagingRoot, change); other != "" && manifest.PlanPath != "" &&
		!SamePlanPath(other, manifest.PlanPath) {
		return false, nil, &StagedByOtherPlanError{PlanPath: other}
	}
	dir := stagingParentDir(stagingRoot)
	if err := removeStagingLeftovers(dir, change); err != nil {
		return false, nil, err
	}
	stagingDir := filepath.Join(stagingRoot, filepath.FromSlash(StagingDirRel(change)))
	replaced, err = dirExists(stagingDir)
	if err != nil {
		return false, nil, err
	}

	tmpDir, err := writeStagingTemp(dir, change, files, manifest)
	if err != nil {
		return false, nil, err
	}
	old := ""
	if replaced {
		old = filepath.Join(dir, ".old-"+change+"-"+stageSuffix())
		if err := stageRename(stagingDir, old); err != nil {
			_ = os.RemoveAll(tmpDir)
			return false, nil, &StageIOError{Op: "rename", Path: stagingDir, To: old, Dir: dir, Err: err}
		}
	}
	if err := stageRename(tmpDir, stagingDir); err != nil {
		ioErr := error(&StageIOError{Op: "rename", Path: tmpDir, To: stagingDir, Dir: dir, Err: err})
		if old != "" {
			if backErr := stageRename(old, stagingDir); backErr != nil {
				ioErr = errors.Join(ioErr, &StageIOError{Op: "rename", Path: old, To: stagingDir, Dir: dir, Err: backErr})
			}
		}
		_ = os.RemoveAll(tmpDir)
		return false, nil, ioErr
	}
	if old != "" {
		if err := stageRemoveAll(old); err != nil {
			warnings = append(warnings, fmt.Sprintf("old staging %s not deleted: %v", old, err))
		}
	}
	return replaced, warnings, nil
}

// writeStagingTemp creates a new .tmp-<change>-<n> folder in dir and writes
// each file and stage.json (indented JSON plus a newline) into it. On error
// the temp folder is removed and a *StageIOError with Op "write" is returned.
func writeStagingTemp(dir, change string, files []StageFile, manifest StageManifest) (string, error) {
	var tmpDir string
	for attempt := 0; ; attempt++ {
		tmpDir = filepath.Join(dir, ".tmp-"+change+"-"+stageSuffix())
		err := os.Mkdir(tmpDir, 0o755)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) || attempt >= 9 {
			return "", &StageIOError{Op: "write", Path: tmpDir, Dir: dir, Err: err}
		}
	}
	fail := func(p string, err error) (string, error) {
		_ = os.RemoveAll(tmpDir)
		return "", &StageIOError{Op: "write", Path: p, Dir: dir, Err: err}
	}
	for _, f := range files {
		p := filepath.Join(tmpDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fail(filepath.Dir(p), err)
		}
		if err := stageWriteFile(p, []byte(f.Content), 0o644); err != nil {
			return fail(p, err)
		}
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fail(filepath.Join(tmpDir, StageManifestFile), err)
	}
	p := filepath.Join(tmpDir, StageManifestFile)
	if err := stageWriteFile(p, append(data, '\n'), 0o644); err != nil {
		return fail(p, err)
	}
	return tmpDir, nil
}

// removeStagingLeftovers deletes each .tmp-<change>-<n> and .old-<change>-<n>
// folder in dir, where <n> is all digits: the temp and old folders of a
// stage of change that crashed. A folder of another change never matches,
// because a change name holds a letter or a hyphen after the prefix. A missing
// dir deletes nothing. A failed list returns a *StageIOError with Op "read";
// a failed delete returns one with Op "remove".
func removeStagingLeftovers(dir, change string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return &StageIOError{Op: "read", Path: dir, Dir: dir, Err: err}
	}
	for _, e := range entries {
		if !isStagingLeftover(e.Name(), change) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if err := stageRemoveAll(p); err != nil {
			return &StageIOError{Op: "remove", Path: p, Dir: dir, Err: err}
		}
	}
	return nil
}

// isStagingLeftover reports whether name is ".tmp-<change>-<n>" or
// ".old-<change>-<n>" with <n> one or more digits.
func isStagingLeftover(name, change string) bool {
	for _, prefix := range []string{".tmp-" + change + "-", ".old-" + change + "-"} {
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok || rest == "" {
			continue
		}
		if strings.Trim(rest, "0123456789") == "" {
			return true
		}
	}
	return false
}

// stageSuffix returns a random decimal suffix for a temp or old folder name.
func stageSuffix() string {
	return strconv.FormatUint(rand.Uint64(), 10)
}

// checkStageInput runs the checks of Stage that need no openspec CLI, in
// this order: the change name (ErrInvalidChangeName), a non-empty file list,
// the syntax of each file path (ErrPathNotAllowed), and the owner of an
// existing staging (*StagedByOtherPlanError, compared with SamePlanPath).
// The owner check is skipped when planPath is empty or the existing
// stage.json names no owner (see stagedPlanPath). It writes nothing and takes
// no lock; Stage checks the owner again under the lock.
func checkStageInput(stagingRoot, change string, files []StageFile, planPath string) error {
	if !ValidChangeName(change) {
		return fmt.Errorf("%w: %q", ErrInvalidChangeName, change)
	}
	if len(files) == 0 {
		return errors.New("openspec stage: no files to stage")
	}
	if err := checkPathSyntax(files); err != nil {
		return err
	}
	if other := stagedPlanPath(stagingRoot, change); other != "" && planPath != "" &&
		!SamePlanPath(other, planPath) {
		return &StagedByOtherPlanError{PlanPath: other}
	}
	return nil
}

// stagedPlanPath returns the planPath of the existing stage.json of
// change, read with ReadStageManifest. It returns "" when the file is
// missing, cannot be read, is over stageManifestMaxBytes, cannot be parsed,
// lists a bad file path, or has no planPath. Such a staging has no owner to
// compare, so a new stage replaces it.
func stagedPlanPath(stagingRoot, change string) string {
	stagingDir := filepath.Join(stagingRoot, filepath.FromSlash(StagingDirRel(change)))
	manifest, err := ReadStageManifest(stagingDir)
	if err != nil {
		return ""
	}
	return manifest.PlanPath
}

// ReadStageManifest reads <stagingDir>/stage.json with the checks of
// readStageManifest. A stage.json over stageManifestMaxBytes returns an
// error that wraps ErrStageManifestTooLarge, with the text "stage.json is
// <n> bytes, limit 1048576 bytes"; nothing else is read then.
func ReadStageManifest(stagingDir string) (StageManifest, error) {
	p := filepath.Join(stagingDir, StageManifestFile)
	if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() && info.Size() > stageManifestMaxBytes {
		return StageManifest{}, fmt.Errorf("%w: stage.json is %d bytes, limit %d bytes", ErrStageManifestTooLarge, info.Size(), stageManifestMaxBytes)
	}
	return readStageManifest(stagingDir)
}

// MissingFiles returns the path of each manifest file that is not a regular
// file under dir (missing, a folder, or not readable by Stat), in manifest
// order. It returns nil when every file exists.
func MissingFiles(manifest StageManifest, dir string) []string {
	var missing []string
	for _, f := range manifest.Files {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil || !info.Mode().IsRegular() {
			missing = append(missing, f.Path)
		}
	}
	return missing
}

// SamePlanPath reports whether a and b name the same plan file. Each path is
// made absolute and its symlinks are resolved; when that fails (for example,
// the file does not exist), the absolute cleaned path is used. Stage and
// Materialize use it for the owner check of a staging.
func SamePlanPath(a, b string) bool {
	return resolvePlanPath(a) == resolvePlanPath(b)
}

func resolvePlanPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

// newTempChange creates a temp directory holding a copy of
// <activeRoot>/openspec/config.yaml, runs `openspec new change <change>`
// there, and returns the temp directory and a cleanup func that removes it.
// On error the temp directory is already removed.
func newTempChange(activeRoot, change string) (string, func(), error) {
	config, err := os.ReadFile(filepath.Join(activeRoot, "openspec", "config.yaml"))
	if err != nil {
		return "", nil, fmt.Errorf("openspec: read openspec/config.yaml: %w", err)
	}
	tmp, err := os.MkdirTemp("", "sdlc-openspec-*")
	if err != nil {
		return "", nil, fmt.Errorf("openspec: create temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmp) }
	if err := writeFile(filepath.Join(tmp, "openspec", "config.yaml"), string(config)); err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := cliResult(tmp, "new", "change", change); err != nil {
		cleanup()
		return "", nil, err
	}
	return tmp, cleanup, nil
}

// copyTargetSpecs copies <activeRoot>/openspec/specs/<cap>/spec.md into
// <tmp>/openspec/specs/<cap>/spec.md for each staged specs/<cap>/spec.md,
// so validate --strict checks a MODIFIED delta the same way ship does. A
// staged path of any other shape is skipped. A capability with no current
// spec is skipped too: a new capability has only ADDED requirements.
//
// A current spec over maxTargetSpecBytes returns an error that wraps
// ErrTargetSpecTooLarge. One whose read fails for a reason other than
// not-exist returns an error that wraps ErrTargetSpec. A failed
// write into tmp returns the writeFile error. Nothing under activeRoot is
// written.
func copyTargetSpecs(activeRoot, tmp string, files []StageFile) error {
	for _, f := range files {
		segs := strings.Split(f.Path, "/")
		if len(segs) != 3 || segs[0] != "specs" || segs[2] != "spec.md" {
			continue
		}
		rel := filepath.Join("openspec", "specs", segs[1], "spec.md")
		src := filepath.Join(activeRoot, rel)
		data, err := readTargetSpec(src)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(tmp, rel), string(data)); err != nil {
			return err
		}
	}
	return nil
}

// readTargetSpec reads the current spec at p. It returns an error that wraps
// os.ErrNotExist when the file is absent, an error that wraps
// ErrTargetSpecTooLarge when the file is over maxTargetSpecBytes, and an error
// that wraps ErrTargetSpec when the file cannot be read.
func readTargetSpec(p string) ([]byte, error) {
	file, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: read %s: %w", ErrTargetSpec, p, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxTargetSpecBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %w", ErrTargetSpec, p, err)
	}
	if len(data) > maxTargetSpecBytes {
		return nil, fmt.Errorf("%w: %s is over %d MiB", ErrTargetSpecTooLarge, p, maxTargetSpecBytes>>20)
	}
	return data, nil
}

// checkPathSyntax rejects any file path that is empty, absolute, not a clean
// slash-separated relative path, or a repeat of an earlier path.
func checkPathSyntax(files []StageFile) error {
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		p := f.Path
		bad := p == "" ||
			strings.HasPrefix(p, "/") ||
			filepath.IsAbs(p) ||
			strings.Contains(p, `\`) ||
			path.Clean(p) != p
		if !bad {
			for _, seg := range strings.Split(p, "/") {
				if seg == ".." || seg == "." || seg == "" {
					bad = true
					break
				}
			}
		}
		if bad {
			return fmt.Errorf("%w: %q must be a clean relative path inside the change dir", ErrPathNotAllowed, p)
		}
		if seen[p] {
			return fmt.Errorf("%w: %q is listed more than once", ErrPathNotAllowed, p)
		}
		seen[p] = true
	}
	return nil
}

func matchesAny(patterns []string, p string) bool {
	for _, pattern := range patterns {
		if matchOutputPath(pattern, p) {
			return true
		}
	}
	return false
}

// matchOutputPath reports whether the slash-separated relative path p
// matches an OpenSpec outputPath pattern. A "**" segment matches zero or
// more whole path segments; every other segment is matched with path.Match,
// so "*" never crosses a "/". Example: "specs/**/*.md" matches
// "specs/spec.md", "specs/user-auth/spec.md" and "specs/a/b/spec.md".
func matchOutputPath(pattern, p string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			for i := 0; i <= len(segs); i++ {
				if matchSegments(rest, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], segs[0])
		if err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// writeFile creates p's parent directories and writes content to p
// atomically.
func writeFile(p, content string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("openspec: create dir for %s: %w", p, err)
	}
	return fsx.AtomicWriteBytes(p, []byte(content))
}
