// Materialization of a staged OpenSpec change at run start.
//
// Plan stages a change's artifacts under
// <active-worktree>/.sdlc-v2/openspec-staging/<change>/ (see stage.go) and
// records the staging dir in the plan's **OpenSpec-Staging:** header. When a
// run starts, Materialize moves the staged files into
// openspec/changes/<change>/ by the first matching rule of the spec's rule
// table (openspec-staging spec, "Materialize when a run starts").
package openspec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
)

// ErrMaterialize wraps every rule failure of Materialize: an invalid change
// name (rule 2, which also wraps ErrInvalidChangeName), a target that
// differs from staging (rule 5), a missing target and staging dir (rule 6),
// a staged file changed after validation (rule 7), an unsafe stage.json
// path, and a failed `openspec validate` (rule 8). Callers map it to a
// DomainError. Infrastructure failures (CLI missing — ErrCLINotFound —,
// filesystem errors, `git add` failures) do not wrap it.
var ErrMaterialize = errors.New("openspec materialize")

// stagingHeaderRe matches the whole plan header line written after a
// successful stage:
// **OpenSpec-Staging:** .sdlc-v2/openspec-staging/<change>/
// The line is matched on its own: the padding is spaces and tabs only, so a
// match never spans a line break. A trailing carriage return of a CRLF plan
// is part of the match.
var stagingHeaderRe = regexp.MustCompile(`(?m)^\*\*OpenSpec-Staging:\*\*[ \t]*\.sdlc-v2/openspec-staging/([^\s/]+)/?[ \t\r]*$`)

// Values of MaterializeResult.Materialized.
const (
	MaterializedCreated = "created" // this call created openspec/changes/<change>/
	MaterializedAlready = "already" // an earlier call created it
)

// openspecMetaFile is the per-change metadata file `openspec new change`
// creates. Materialize never overwrites it.
const openspecMetaFile = ".openspec.yaml"

// StagedChangeFromPlan returns the change name from the plan's
// **OpenSpec-Staging:** header. ok is false when the plan has no such
// header. The name is returned as written; it is not checked against
// ValidChangeName here.
func StagedChangeFromPlan(planContent string) (change string, ok bool) {
	m := stagingHeaderRe.FindStringSubmatch(planContent)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// ReplaceStagingHeader replaces each **OpenSpec-Staging:** header line of
// planContent with line, which is inserted as written. It reads the header
// line in the same shape as StagedChangeFromPlan, and it keeps every other
// line, blank lines included. ok is false when the plan has no such header;
// the plan is then returned unchanged.
func ReplaceStagingHeader(planContent, line string) (replaced string, ok bool) {
	if !stagingHeaderRe.MatchString(planContent) {
		return planContent, false
	}
	return stagingHeaderRe.ReplaceAllLiteralString(planContent, line), true
}

// MaterializeResult is the outcome of Materialize. Materialized is
// "created" when this call created openspec/changes/<change>/, "already"
// when an earlier start did, and "" when the plan has no
// **OpenSpec-Staging:** header (Change is then "" too).
type MaterializeResult struct {
	Change       string `json:"change"`
	Materialized string `json:"materialized"` // "created" | "already"; "" when rule 1
}

// Materialize applies the first matching rule for the change staged by
// planContent, relative to the active worktree root activeRoot:
//
//  1. no **OpenSpec-Staging:** header: zero result, nil error
//  2. invalid change name: ErrInvalidChangeName; nothing written
//  3. target exists, staging dir missing: "already"
//  4. target and staging exist, every stage.json file matches the target by
//     SHA-256: staging dir deleted; "already"
//  5. target and staging exist, any file differs: error; nothing changed
//  6. target and staging both missing: error naming the staging path
//  7. a staged file's SHA-256 differs from stage.json: error; nothing written
//  8. otherwise: `openspec new change`, copy each stage.json file, `openspec
//     validate <change> --strict`, `git add openspec/changes/<change>/`,
//     delete the staging dir; "created"
//
// On any rule-8 failure the target dir is removed and the staging dir is
// kept. A failed validation returns an ErrMaterialize error carrying the CLI
// output. The .openspec.yaml file `openspec new change` creates is never
// overwritten. See ErrMaterialize for which errors wrap it.
func Materialize(activeRoot, planContent string) (MaterializeResult, error) {
	change, ok := StagedChangeFromPlan(planContent)
	if !ok {
		return MaterializeResult{}, nil // rule 1
	}
	if !ValidChangeName(change) {
		return MaterializeResult{}, fmt.Errorf("%w: %w: %q", ErrMaterialize, ErrInvalidChangeName, change) // rule 2
	}

	targetRel := filepath.Join("openspec", "changes", change)
	targetDir := filepath.Join(activeRoot, targetRel)
	stagingDir := filepath.Join(activeRoot, filepath.FromSlash(StagingDirRel(change)))

	targetExists, err := dirExists(targetDir)
	if err != nil {
		return MaterializeResult{}, err
	}
	stagingExists, err := dirExists(stagingDir)
	if err != nil {
		return MaterializeResult{}, err
	}
	result := MaterializeResult{Change: change, Materialized: MaterializedAlready}

	if targetExists && !stagingExists {
		return result, nil // rule 3
	}
	if !targetExists && !stagingExists {
		return MaterializeResult{}, fmt.Errorf("%w: openspec/changes/%s/ does not exist and the staging dir %s is missing", // rule 6
			ErrMaterialize, change, stagingDir+string(filepath.Separator))
	}

	manifest, err := readStageManifest(stagingDir)
	if err != nil {
		return MaterializeResult{}, err
	}

	if targetExists {
		for _, f := range manifest.Files {
			got, err := fileSHA256(filepath.Join(targetDir, filepath.FromSlash(f.Path)))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return MaterializeResult{}, err
			}
			if got != f.SHA256 {
				return MaterializeResult{}, fmt.Errorf("%w: openspec change %s already exists and differs from staging", ErrMaterialize, change) // rule 5
			}
		}
		if err := os.RemoveAll(stagingDir); err != nil { // rule 4
			return MaterializeResult{}, fmt.Errorf("openspec materialize: delete %s: %w", stagingDir, err)
		}
		return result, nil
	}

	for _, f := range manifest.Files { // rule 7
		got, err := fileSHA256(filepath.Join(stagingDir, filepath.FromSlash(f.Path)))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return MaterializeResult{}, err
		}
		if got != f.SHA256 {
			return MaterializeResult{}, fmt.Errorf("%w: staged file %s changed after validation", ErrMaterialize, f.Path)
		}
	}

	// Rule 8. The target did not exist before this point, so removing it on
	// any failure below only undoes this call's own work.
	if err := createChange(activeRoot, change, targetDir, targetRel, stagingDir, manifest); err != nil {
		if rmErr := os.RemoveAll(targetDir); rmErr != nil {
			return MaterializeResult{}, errors.Join(err, fmt.Errorf("openspec materialize: remove %s: %w", targetDir, rmErr))
		}
		return MaterializeResult{}, err
	}
	if err := os.RemoveAll(stagingDir); err != nil {
		return MaterializeResult{}, fmt.Errorf("openspec materialize: delete %s: %w", stagingDir, err)
	}
	result.Materialized = MaterializedCreated
	return result, nil
}

// createChange runs rule 8 up to and including `git add`: create the change
// with the CLI, copy each staged file, validate, and stage the target dir.
func createChange(activeRoot, change, targetDir, targetRel, stagingDir string, manifest StageManifest) error {
	if _, err := cliResult(activeRoot, "new", "change", change); err != nil {
		return err
	}
	for _, f := range manifest.Files {
		data, err := os.ReadFile(filepath.Join(stagingDir, filepath.FromSlash(f.Path)))
		if err != nil {
			return fmt.Errorf("openspec materialize: read staged %s: %w", f.Path, err)
		}
		if err := writeFile(filepath.Join(targetDir, filepath.FromSlash(f.Path)), string(data)); err != nil {
			return err
		}
	}
	stdout, stderr, exit, err := runCLI(activeRoot, "validate", change, "--strict")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrCLINotFound
		}
		return fmt.Errorf("openspec validate %s --strict: %w", change, err)
	}
	if exit != 0 {
		output := strings.TrimSpace(strings.Join([]string{stdout, stderr}, "\n"))
		return fmt.Errorf("%w: openspec validate %s --strict failed (exit %d): %s", ErrMaterialize, change, exit, output)
	}
	if _, err := execx.Run("git", []string{"add", "--", filepath.ToSlash(targetRel) + "/"}, execx.Options{Dir: activeRoot}); err != nil {
		return fmt.Errorf("openspec materialize: git add %s/: %w", filepath.ToSlash(targetRel), err)
	}
	return nil
}

// readStageManifest reads <stagingDir>/stage.json and rejects any file path
// that is not a clean relative path, is repeated, or names .openspec.yaml:
// stage.json lives in a gitignored dir and may have been edited by hand.
func readStageManifest(stagingDir string) (StageManifest, error) {
	var m StageManifest
	p := filepath.Join(stagingDir, StageManifestFile)
	if err := fsx.ReadJSON(p, &m); err != nil {
		return StageManifest{}, fmt.Errorf("openspec materialize: read %s: %w", p, err)
	}
	files := make([]StageFile, 0, len(m.Files))
	for _, f := range m.Files {
		if f.Path == openspecMetaFile {
			return StageManifest{}, fmt.Errorf("%w: %w: stage.json lists %s, which openspec new change owns", ErrMaterialize, ErrPathNotAllowed, openspecMetaFile)
		}
		files = append(files, StageFile{Path: f.Path})
	}
	if err := checkPathSyntax(files); err != nil {
		return StageManifest{}, fmt.Errorf("%w: stage.json: %w", ErrMaterialize, err)
	}
	return m, nil
}

// dirExists reports whether p exists. Any stat error other than "not
// exist" is returned.
func dirExists(p string) (bool, error) {
	_, err := os.Stat(p)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("openspec materialize: stat %s: %w", p, err)
}

// fileSHA256 returns the hex SHA-256 of the file at p. A missing file
// returns an error that matches os.ErrNotExist.
func fileSHA256(p string) (string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("openspec materialize: read %s: %w", p, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
