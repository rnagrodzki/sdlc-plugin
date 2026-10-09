// Staging of OpenSpec artifacts outside the repository's tracked tree.
//
// Plan mode forbids tracked-file writes, so plan authors a change's artifacts
// into <active-worktree>/.sdlc-v2/openspec-staging/<change>/ (gitignored) and
// a later run start materializes them into openspec/changes/<change>/.
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
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
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
// file is over maxTargetSpecBytes, or reading it fails for a reason other
// than the file being absent. The wrapping error names the spec path.
var ErrTargetSpec = errors.New("openspec stage: target spec")

// maxTargetSpecBytes is the size limit for one current spec that Stage copies
// into the temp directory (1 MiB).
const maxTargetSpecBytes = 1 << 20

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
// relative to the active worktree root, exactly as StagingDirRel returns it
// (the form the plan's **OpenSpec-Staging:** header uses). ValidateOutput is
// the combined stdout/stderr of `openspec validate <change> --strict`.
type StageResult struct {
	StagingDir     string
	Files          []StagedFile
	Valid          bool
	ValidateOutput string
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
// active worktree root, with a trailing slash:
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

// Stage writes files into <activeRoot>/.sdlc-v2/openspec-staging/<change>/,
// replacing any earlier staging of the same change, writes stage.json, and
// validates a temp copy of the staged change with `openspec validate
// <change> --strict`.
//
// Before validating, Stage copies the current spec of each capability that a
// staged specs/<capability>/spec.md names into the temp directory, so a
// MODIFIED delta is checked against the current spec the same way as at ship.
// A capability with no current spec is a new capability and copies nothing.
//
// Every check runs before the first write under activeRoot: the change name
// (ErrInvalidChangeName), each file path against the outputPath patterns the
// CLI reports for a temp change (ErrPathNotAllowed), and the copy of the
// current specs (ErrTargetSpec). A failed validation is not an error: the
// result has Valid false and the CLI output, and stage.json has no
// validatedAt. now defaults to time.Now when nil.
func Stage(activeRoot, change string, files []StageFile, planPath string, now func() time.Time) (StageResult, error) {
	if now == nil {
		now = time.Now
	}
	if !ValidChangeName(change) {
		return StageResult{}, fmt.Errorf("%w: %q", ErrInvalidChangeName, change)
	}
	if len(files) == 0 {
		return StageResult{}, errors.New("openspec stage: no files to stage")
	}
	if err := checkPathSyntax(files); err != nil {
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

	// All checks passed: replace the staging dir as a whole, so a file
	// dropped since the previous staging disappears.
	stagingRel := StagingDirRel(change)
	stagingDir := filepath.Join(activeRoot, filepath.FromSlash(stagingRel))
	if err := os.RemoveAll(stagingDir); err != nil {
		return StageResult{}, fmt.Errorf("openspec stage: clear %s: %w", stagingDir, err)
	}
	staged := make([]StagedFile, 0, len(files))
	for _, f := range files {
		if err := writeFile(filepath.Join(stagingDir, filepath.FromSlash(f.Path)), f.Content); err != nil {
			return StageResult{}, err
		}
		sum := sha256.Sum256([]byte(f.Content))
		staged = append(staged, StagedFile{Path: f.Path, SHA256: hex.EncodeToString(sum[:])})
	}
	manifest := StageManifest{
		Change:   change,
		Schema:   status.SchemaName,
		PlanPath: planPath,
		Files:    staged,
	}
	manifestPath := filepath.Join(stagingDir, StageManifestFile)
	if err := fsx.AtomicWriteJSON(manifestPath, manifest); err != nil {
		return StageResult{}, err
	}

	// Validate a copy of the staged change inside the temp change dir that
	// `openspec new change` created (it keeps its .openspec.yaml).
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
	output := strings.TrimSpace(strings.Join([]string{stdout, stderr}, "\n"))

	result := StageResult{StagingDir: stagingRel, Files: staged, Valid: exit == 0, ValidateOutput: output}
	if result.Valid {
		manifest.ValidatedAt = now().UTC().Format(time.RFC3339)
		if err := fsx.AtomicWriteJSON(manifestPath, manifest); err != nil {
			return StageResult{}, err
		}
	}
	return result, nil
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
// A current spec over maxTargetSpecBytes, or one whose read fails for a reason
// other than not-exist, returns an error that wraps ErrTargetSpec. A failed
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
// os.ErrNotExist when the file is absent, and an error that wraps ErrTargetSpec
// when the file is over maxTargetSpecBytes or cannot be read.
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
		return nil, fmt.Errorf("%w: %s is over 1 MiB", ErrTargetSpec, p)
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
