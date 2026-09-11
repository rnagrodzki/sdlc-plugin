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
	// Action selects the migration to run: "config", "import", or "layout".
	Action string `json:"action" jsonschema:"enum=config,enum=import,enum=layout" jsonschema_description:"Selects the migration to run: \"config\" (schema migration via configmigrate engine), \"import\" (non-destructively imports config, templates, jira-templates, learnings, and review-dimensions from the legacy plugin directory), or \"layout\" (moves this plugin's own old state layout, execution/, into the current runs/ layout)."`
	// DryRun, when true, reports what would change without writing.
	DryRun bool `json:"dryRun" jsonschema_description:"When true, reports what would change without writing anything."`
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
	case "layout":
		return migrateLayout(root, in.DryRun)
	default:
		return MigrateOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("unknown migrate action %q; must be one of: config, import, layout", in.Action),
			Suggestion: "Set action to \"config\", \"import\", or \"layout\".",
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

// ---------------------------------------------------------------------------
// migrate layout action
// ---------------------------------------------------------------------------

// migrateLayout moves a user's old on-disk state layout, paths.DataDir +
// "/execution" (see state.legacyStateDir), into the current layout,
// paths.DataDir + "/" + paths.RunsSubdir (see state.stateDir — unexported,
// so this mirrors its construction rather than importing it). Unlike
// importFromOld's cross-plugin copy (which must tolerate a different
// mountpoint for the legacy plugin's data dir), execution/ and runs/ are
// always siblings under the same DataDir on the same filesystem, so entries
// are moved with os.Rename rather than copied.
//
// execution/ is not flat: besides top-level state JSON files, it holds
// per-runID working directories (fact sheets etc., mirroring the runID
// directories runs/ itself holds — see execActionCleanup's runDir) and a
// ledger/ directory that is itself a collection of per-runID
// subdirectories (see ledgerDir). Because new runs already write directly
// under runs/ledger/<runID>, runs/ledger/ is very likely to already exist
// and contain live entries by the time this migration runs, so
// execution/ledger/ is merged into runs/ledger/ child-by-child rather than
// renamed as a single directory (a whole-directory os.Rename would fail
// outright once the destination exists as a non-empty directory, and even
// if it didn't, os.Rename silently replacing an existing directory is not
// the "skip on conflict" behaviour this migration promises elsewhere).
//
// Every entry — top-level file, top-level runID directory, or ledger/
// child — is moved independently. A name that already exists at the
// destination is left untouched at the source and reported in Result as
// skipped, rather than overwritten or aborting the rest of the migration.
// A missing or empty execution/ directory is a successful no-op (the
// migration is idempotent: running it again after a successful run, or on
// a project that never had the old layout, is always OK:true).
func migrateLayout(root string, dryRun bool) (MigrateOut, error) {
	src := filepath.Join(root, paths.DataDir, "execution")
	dst := filepath.Join(root, paths.DataDir, paths.RunsSubdir)

	var changed []string
	var skipped []string

	srcExists, srcErr := migrateStatExists(src)
	if srcErr != nil {
		return MigrateOut{
			OK:     true,
			Action: "layout",
			Result: fmt.Sprintf("warning: cannot stat legacy %s: %s — skipping layout migration", src, srcErr.Error()),
		}, nil
	}
	if srcExists {
		entries, err := os.ReadDir(src)
		if err != nil {
			return MigrateOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("read %s: %s", src, err.Error()),
				Cause: err,
			}
		}

		for _, e := range entries {
			name := e.Name()

			if e.IsDir() && name == "ledger" {
				c, s, err := migrateLayoutMergeLedger(filepath.Join(src, name), filepath.Join(dst, name), dryRun)
				if err != nil {
					return MigrateOut{}, err
				}
				changed = append(changed, c...)
				skipped = append(skipped, s...)
				continue
			}

			label := paths.DataDir + "/" + paths.RunsSubdir + "/" + name
			c, s, err := migrateLayoutMoveEntry(filepath.Join(src, name), filepath.Join(dst, name), label, e.IsDir(), dryRun)
			if err != nil {
				return MigrateOut{}, err
			}
			if c != "" {
				changed = append(changed, c)
			}
			if s != "" {
				skipped = append(skipped, s)
			}
		}
	}

	verb := "migrated"
	if dryRun {
		verb = "would-migrate"
	}

	var result string
	switch {
	case len(changed) > 0 && len(skipped) > 0:
		result = fmt.Sprintf("%s: %v; skipped (name conflict): %v", verb, changed, skipped)
	case len(changed) > 0:
		result = fmt.Sprintf("%s: %v", verb, changed)
	case len(skipped) > 0:
		result = fmt.Sprintf("up-to-date: skipped (name conflict): %v", skipped)
	default:
		result = "up-to-date: no legacy execution/ layout to migrate"
	}

	return MigrateOut{
		OK:      true,
		Action:  "layout",
		DryRun:  dryRun,
		Result:  result,
		Changed: changed,
	}, nil
}

// migrateLayoutMergeLedger merges execution/ledger/'s children individually
// into runs/ledger/. Each child is a per-runID directory (see ledgerDir),
// so the same per-entry move-or-skip logic as migrateLayoutMoveEntry
// applies to each one; only the parent ledger/ directory itself is never
// renamed as a unit. A missing execution/ledger/ (e.g. a legacy layout
// predating the ledger feature) is a no-op, not an error.
func migrateLayoutMergeLedger(src, dst string, dryRun bool) (changed, skipped []string, err error) {
	entries, rdErr := os.ReadDir(src)
	if rdErr != nil {
		if os.IsNotExist(rdErr) {
			return nil, nil, nil
		}
		return nil, nil, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("read %s: %s", src, rdErr.Error()),
			Cause: rdErr,
		}
	}

	for _, e := range entries {
		name := e.Name()
		label := paths.DataDir + "/" + paths.RunsSubdir + "/ledger/" + name
		c, s, mErr := migrateLayoutMoveEntry(filepath.Join(src, name), filepath.Join(dst, name), label, e.IsDir(), dryRun)
		if mErr != nil {
			return nil, nil, mErr
		}
		if c != "" {
			changed = append(changed, c)
		}
		if s != "" {
			skipped = append(skipped, s)
		}
	}
	return changed, skipped, nil
}

// migrateLayoutMoveEntry moves one execution/ entry (file or directory) to
// its corresponding location under runs/, or reports it as skipped when
// the destination name is already taken.
//
// The destination is pre-checked with os.Stat (via migrateDirExists /
// migrateFileExists) before attempting any move — os.Rename does NOT
// reliably error on an existing destination: replacing an existing file,
// or an existing *empty* directory, succeeds silently on Unix. Relying on
// Rename's own error to detect conflicts would risk exactly the silent
// overwrite this migration must never do, so the pre-check is load-bearing,
// not just an optimization.
func migrateLayoutMoveEntry(src, dst, label string, isDir bool, dryRun bool) (changed, skipped string, err error) {
	dstExists, statErr := migrateStatExists(dst)
	if statErr != nil {
		return "", "", &mcpserver.InfraError{
			Msg:   fmt.Sprintf("cannot determine if destination %s exists: %s — refusing to move to avoid potential overwrite", dst, statErr.Error()),
			Cause: statErr,
		}
	}
	if dstExists {
		return "", label, nil
	}

	displayLabel := label
	if isDir {
		displayLabel += "/"
	}

	if dryRun {
		return displayLabel, "", nil
	}

	if mkErr := os.MkdirAll(filepath.Dir(dst), 0o755); mkErr != nil {
		return "", "", &mcpserver.InfraError{
			Msg:   fmt.Sprintf("create %s directory: %s", filepath.Dir(dst), mkErr.Error()),
			Cause: mkErr,
		}
	}
	if rnErr := os.Rename(src, dst); rnErr != nil {
		return "", "", &mcpserver.InfraError{
			Msg:   fmt.Sprintf("move %s: %s", src, rnErr.Error()),
			Cause: rnErr,
		}
	}
	return displayLabel, "", nil
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
// Non-IsNotExist stat errors are collapsed into false — acceptable for
// import-action callers where the consequence is a harmless skip, but NOT
// suitable for the layout-migration's load-bearing overwrite guard. Use
// migrateStatExists for that.
func migrateDirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// migrateFileExists returns true if path exists and is not a directory.
// Same caveat as migrateDirExists — not suitable for load-bearing pre-checks.
func migrateFileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// migrateStatExists returns (true, nil) when path exists, (false, nil) when
// it genuinely does not exist, and (false, err) on any other stat error
// (permission denied, I/O error, etc.). Used by load-bearing pre-checks
// where collapsing all errors into "does not exist" risks silent overwrites.
func migrateStatExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
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
		"Runs a legacy migration. Actions: config (schema migration via configmigrate engine), import (non-destructively imports config, templates, jira-templates, learnings, and review-dimensions from the old plugin's "+paths.LegacyDataDir+"/ directory into "+paths.DataDir+"/ — config.json and local.json merge per top-level key so already-scaffolded empty files still receive legacy sections, everything else is skipped whole-file when the destination already exists), layout (moves this plugin's own old state layout, "+paths.DataDir+"/execution/, into the current "+paths.DataDir+"/"+paths.RunsSubdir+"/ layout — state files, per-run directories, and ledger/ entries are each moved independently; a name conflict at the destination is skipped and reported rather than overwritten).",
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
