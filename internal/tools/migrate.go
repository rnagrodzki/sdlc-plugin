package tools

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// migrate tool
// ---------------------------------------------------------------------------

// MigrateIn is the input for the migrate tool.
type MigrateIn struct {
	// Action selects the migration to run: "config" or "import".
	Action string `json:"action"`
	// DryRun, when true, reports what would change without writing.
	DryRun bool `json:"dryRun"`
}

// MigrateOut is the output for the migrate tool.
type MigrateOut struct {
	OK      bool     `json:"ok"`
	Action  string   `json:"action"`
	DryRun  bool     `json:"dryRun"`
	Result  string   `json:"result"`
	Changed []string `json:"changed"`
	Errors  []string `json:"errors,omitempty"`
}

// migrate is the core logic, separated from the handler for testability.
func migrate(root string, in MigrateIn) (MigrateOut, error) {
	switch in.Action {
	case "config":
		return migrateConfig(root, in.DryRun)
	case "import":
		return importFromOld(root, in.DryRun)
	default:
		return MigrateOut{}, &mcpserver.DomainError{
			Msg: fmt.Sprintf("unknown migrate action %q; must be one of: config, import", in.Action),
		}
	}
}

// migrateConfig delegates to the configmigrate engine.
func migrateConfig(root string, dryRun bool) (MigrateOut, error) {
	if dryRun {
		// DryRun was removed from configmigrate.Options (Task 8 decision).
		// Resolve at the tool layer: report would-migrate based on Verify.
		err := configmigrate.Verify(root)
		if err == nil {
			return MigrateOut{
				OK:      true,
				Action:  "config",
				DryRun:  true,
				Result:  "up-to-date",
				Changed: []string{},
			}, nil
		}
		return MigrateOut{
			OK:      true,
			Action:  "config",
			DryRun:  true,
			Result:  fmt.Sprintf("would-migrate: %s", err.Error()),
			Changed: []string{},
		}, nil
	}

	report, err := configmigrate.Migrate(root, configmigrate.Options{})
	if err != nil {
		return MigrateOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("config migration failed: %s", err.Error()),
			Cause: err,
		}
	}

	changed := []string{}
	if report.Migrated {
		changed = append(changed, paths.DataDir+"/config.json")
		// local.json may also have been written.
		localPath := filepath.Join(root, paths.DataDir, "local.json")
		if _, err := os.Stat(localPath); err == nil {
			changed = append(changed, paths.DataDir+"/local.json")
		}
	}

	result := "up-to-date"
	if report.Migrated {
		result = fmt.Sprintf("migrated (steps: %v, legacy ingested: %v)",
			report.StepsApplied, report.LegacyIngested)
	}

	return MigrateOut{
		OK:      true,
		Action:  "config",
		DryRun:  false,
		Result:  result,
		Changed: changed,
	}, nil
}

// legacyImportJSONFiles are JSON-object files imported from
// paths.LegacyDataDir into paths.DataDir by the "import" action using a
// top-level key merge rather than a whole-file skip. setup's own scaffolding
// (setup_init) always creates an empty {} config.json and local.json before
// migrate ever runs, so a whole-file "skip if destination exists" check made
// these two entries permanently unreachable in practice. Merging per key
// lets each already-scaffolded file still receive the legacy sections
// (ship, version, plan.guardrails, ...), while never overwriting a key the
// new config already holds a real value for.
var legacyImportJSONFiles = []string{"config.json", "local.json"}

// legacyImportFiles are non-JSON files copied verbatim from
// paths.LegacyDataDir into paths.DataDir by the "import" action, skipped
// whole-file when the destination already exists.
var legacyImportFiles = []string{"pr-template.md", "plan-template.md"}

// legacyImportDirs are directories copied recursively from
// paths.LegacyDataDir into paths.DataDir by the "import" action.
var legacyImportDirs = []string{"jira-templates", "learnings", "review-dimensions"}

// importFromOld non-destructively copies plugin data from the old plugin's
// data directory (paths.LegacyDataDir, ".sdlc") into the new plugin's data
// directory (paths.DataDir, ".sdlc-v2"). It never deletes or modifies the
// source, and never overwrites a destination key/path that already carries
// real content.
func importFromOld(root string, dryRun bool) (MigrateOut, error) {
	var changed []string

	for _, name := range legacyImportJSONFiles {
		rel, didChange, err := importJSONFileMerge(root, name, dryRun)
		if err != nil {
			return MigrateOut{}, err
		}
		if didChange {
			changed = append(changed, rel)
		}
	}

	for _, name := range legacyImportFiles {
		src := filepath.Join(root, paths.LegacyDataDir, name)
		dst := filepath.Join(root, paths.DataDir, name)
		if !migrateFileExists(src) || migrateFileExists(dst) {
			continue
		}
		rel := paths.DataDir + "/" + name
		if dryRun {
			changed = append(changed, rel)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return MigrateOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("create %s directory: %s", paths.DataDir, err.Error()),
				Cause: err,
			}
		}
		if err := copyFile(src, dst); err != nil {
			return MigrateOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("copy %s: %s", name, err.Error()),
				Cause: err,
			}
		}
		changed = append(changed, rel)
	}

	for _, name := range legacyImportDirs {
		src := filepath.Join(root, paths.LegacyDataDir, name)
		dst := filepath.Join(root, paths.DataDir, name)
		if !migrateDirExists(src) || migrateDirExists(dst) {
			continue
		}
		rel := paths.DataDir + "/" + name + "/"
		if dryRun {
			changed = append(changed, rel)
			continue
		}
		if err := copyDir(src, dst); err != nil {
			return MigrateOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("copy %s: %s", name, err.Error()),
				Cause: err,
			}
		}
		changed = append(changed, rel)
	}

	result := "up-to-date: nothing to import"
	if len(changed) > 0 {
		verb := "imported"
		if dryRun {
			verb = "would-import"
		}
		result = fmt.Sprintf("%s: %v", verb, changed)
	}

	return MigrateOut{
		OK:      true,
		Action:  "import",
		DryRun:  dryRun,
		Result:  result,
		Changed: changed,
	}, nil
}

// importJSONFileMerge imports one JSON-object file (config.json or
// local.json) from paths.LegacyDataDir into paths.DataDir by merging
// top-level keys: any key present in the legacy source but absent from the
// destination is added; any key already present in the destination (even in
// an otherwise-empty-looking file) is left untouched. Returns the changed
// relative path and whether anything changed. A missing source, or a source
// with no keys the destination lacks, is a no-op.
func importJSONFileMerge(root, name string, dryRun bool) (string, bool, error) {
	src := filepath.Join(root, paths.LegacyDataDir, name)
	if !migrateFileExists(src) {
		return "", false, nil
	}

	var srcMap map[string]any
	if err := fsx.ReadJSON(src, &srcMap); err != nil {
		return "", false, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("read legacy %s: %s", name, err.Error()),
			Cause: err,
		}
	}

	dst := filepath.Join(root, paths.DataDir, name)
	var dstMap map[string]any
	if err := fsx.ReadJSON(dst, &dstMap); err != nil && !errors.Is(err, fsx.ErrNotFound) {
		return "", false, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("read %s: %s", name, err.Error()),
			Cause: err,
		}
	}
	if dstMap == nil {
		dstMap = make(map[string]any)
	}

	added := false
	for k, v := range srcMap {
		if _, exists := dstMap[k]; exists {
			continue
		}
		dstMap[k] = v
		added = true
	}
	if !added {
		return "", false, nil
	}

	rel := paths.DataDir + "/" + name
	if dryRun {
		return rel, true, nil
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", false, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("create %s directory: %s", paths.DataDir, err.Error()),
			Cause: err,
		}
	}
	if err := fsx.AtomicWriteJSON(dst, dstMap); err != nil {
		return "", false, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("merge %s: %s", name, err.Error()),
			Cause: err,
		}
	}
	return rel, true, nil
}

// copyDir recursively copies the src directory tree to dst, creating dst
// and any needed subdirectories. Used by importFromOld for directory-shaped
// legacy data (jira-templates/, learnings/, review-dimensions/).
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(path, target)
	})
}

// migrateDirExists returns true if path exists and is a directory.
func migrateDirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// migrateFileExists returns true if path exists and is not a directory.
func migrateFileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// copyFile copies src to dst by reading and writing content. Used instead
// of os.Rename for learnings log because src and dst may be on different
// filesystems (different mount points).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterMigrateTools registers the migrate tool on the server.
func RegisterMigrateTools(s *mcpserver.Server) {
	mcpserver.Register(s, "migrate",
		"Runs a legacy migration. Actions: config (schema migration via configmigrate engine), import (non-destructively imports config, templates, jira-templates, learnings, and review-dimensions from the old plugin's "+paths.LegacyDataDir+"/ directory into "+paths.DataDir+"/ — config.json and local.json merge per top-level key so already-scaffolded empty files still receive legacy sections, everything else is skipped whole-file when the destination already exists).",
		func(ctx mcpserver.Ctx, in MigrateIn) (MigrateOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				root, err = os.Getwd()
				if err != nil {
					return MigrateOut{}, &mcpserver.InfraError{
						Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
						Cause: err,
					}
				}
			}
			return migrate(root, in)
		},
	)
}
