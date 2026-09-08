package configmigrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// CurrentSchemaVersion is the current schema version. v5 uses a
// section-based layout without a schemaVersion marker in the config files.
const CurrentSchemaVersion = 5

// Sentinel errors.
var (
	ErrVersionTooNew   = errors.New("configmigrate: version too new")
	ErrVersionStale    = errors.New("configmigrate: version stale")
	ErrMigrationFailed = errors.New("configmigrate: migration failed")
	ErrMigrationLocked = errors.New("configmigrate: migration locked")

	// ErrConfigMissing indicates the project has no SDLC config at all —
	// neither a v5 config.json nor any pre-v5 legacy marker. Returned only
	// by MigrateWithBackup, which distinguishes "never set up" from "stale"
	// so callers can point the user at /setup instead of silently
	// proceeding on bare defaults.
	ErrConfigMissing = errors.New("configmigrate: config missing")
)

const (
	defaultLockRetries    = 30
	defaultLockRetryDelay = 100 * time.Millisecond
)

// legacyMarkers are relative paths whose existence signals a pre-v5 layout.
var legacyMarkers = []string{
	filepath.Join(".claude", "sdlc.json"),
	filepath.Join(".claude", "version.json"),
	filepath.Join(paths.LegacyDataDir, "jira-config.json"),
	filepath.Join(paths.LegacyDataDir, "ship-config.json"),
	filepath.Join(paths.LegacyDataDir, "review.json"),
	filepath.Join(".claude", "review.json"),
}

// Options configures the Migrate function.
type Options struct {
	LockRetries    int           // 0 uses default (30).
	LockRetryDelay time.Duration // 0 uses default (100ms).
}

func (o *Options) retries() int {
	if o != nil && o.LockRetries > 0 {
		return o.LockRetries
	}
	return defaultLockRetries
}

func (o *Options) retryDelay() time.Duration {
	if o != nil && o.LockRetryDelay > 0 {
		return o.LockRetryDelay
	}
	return defaultLockRetryDelay
}

// Report describes what the migration did.
type Report struct {
	Migrated       bool
	StepsApplied   []string
	LegacyIngested []string
}

// ---------------------------------------------------------------------------
// Verify
// ---------------------------------------------------------------------------

// Verify checks if config at mainRoot is current. Returns nil if v5,
// ErrVersionStale if older (with message naming "migrate" tool),
// ErrVersionTooNew if newer than CurrentSchemaVersion.
func Verify(mainRoot string) error {
	configPath := filepath.Join(mainRoot, paths.DataDir, "config.json")
	var raw map[string]any
	err := fsx.ReadJSON(configPath, &raw)
	if err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			if hasLegacy(mainRoot) {
				return fmt.Errorf(
					"%w: legacy config layout detected; run migrate to upgrade to v5 format",
					ErrVersionStale,
				)
			}
			return nil
		}
		return err
	}

	version, hasSV := extractSchemaVersion(raw)
	if !hasSV {
		return nil // no schemaVersion field → already v5
	}
	if version > CurrentSchemaVersion {
		return fmt.Errorf(
			"%w: schemaVersion %d exceeds max supported version %d; upgrade the sdlc plugin",
			ErrVersionTooNew, version, CurrentSchemaVersion,
		)
	}
	if version < CurrentSchemaVersion {
		return fmt.Errorf(
			"%w: schemaVersion %d; run migrate to upgrade to v5 format",
			ErrVersionStale, version,
		)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Migrate
// ---------------------------------------------------------------------------

// Migrate runs all needed migration steps. This is the ONLY migration entry
// point. Uses .sdlc/.migration.lock (O_EXCL create, retries with backoff)
// for concurrency safety.
func Migrate(mainRoot string, opt Options) (*Report, error) {
	lockPath, err := acquireLock(mainRoot, &opt)
	if err != nil {
		return nil, err
	}
	defer releaseLock(lockPath)

	report := &Report{}
	ctx := &migrationContext{
		mainRoot:   mainRoot,
		configPath: filepath.Join(mainRoot, paths.DataDir, "config.json"),
		localPath:  filepath.Join(mainRoot, paths.DataDir, "local.json"),
		legacyPath: filepath.Join(mainRoot, ".claude", "sdlc.json"),
	}

	projectVer, projectExists := detectProjectVersion(mainRoot)
	localVer, localExists := detectLocalVersion(mainRoot)

	// Nothing at all — check for legacy per-section files.
	if !projectExists && !localExists {
		if hasLegacy(mainRoot) {
			ingested, err := ingestLegacy(mainRoot)
			if err != nil {
				return nil, err
			}
			report.Migrated = len(ingested) > 0
			report.LegacyIngested = ingested
		}
		return report, nil
	}

	// Version-too-new guards.
	if projectExists && projectVer > CurrentSchemaVersion {
		return nil, fmt.Errorf(
			"%w: schemaVersion %d exceeds max supported version %d",
			ErrVersionTooNew, projectVer, CurrentSchemaVersion,
		)
	}
	if localExists && localVer > CurrentSchemaVersion {
		return nil, fmt.Errorf(
			"%w: local schemaVersion %d exceeds max supported version %d",
			ErrVersionTooNew, localVer, CurrentSchemaVersion,
		)
	}

	// Run project migrations.
	if projectExists && projectVer < CurrentSchemaVersion {
		steps, err := planSteps(projectMigrations, projectVer, CurrentSchemaVersion)
		if err != nil {
			return nil, err
		}
		applied, err := runSteps(ctx, steps, "project")
		if err != nil {
			return nil, err
		}
		report.StepsApplied = append(report.StepsApplied, applied...)
		report.Migrated = true
	}

	// Run local migrations.
	if localExists && localVer < CurrentSchemaVersion {
		steps, err := planSteps(localMigrations, localVer, CurrentSchemaVersion)
		if err != nil {
			return nil, err
		}
		applied, err := runSteps(ctx, steps, "local")
		if err != nil {
			return nil, err
		}
		report.StepsApplied = append(report.StepsApplied, applied...)
		report.Migrated = true
	}

	return report, nil
}

// ---------------------------------------------------------------------------
// MigrateWithBackup — auto-migrate gate for ship_prepare / execute's init
// ---------------------------------------------------------------------------

// MigrateWithBackup is the auto-migrate gate used by ship_prepare and
// execute_state's "init" action in place of a hard Verify failure. It
// three-way classifies projectRoot's config:
//
//   - No config.json and no legacy marker at all: the project was never set
//     up. Returns ErrConfigMissing (wrapped with an actionable message
//     naming /setup) rather than fabricating a config from nothing.
//   - Current (Verify returns nil): a no-op. Returns (nil, "", nil) without
//     any filesystem write — callers must not report a migration or touch
//     the file when nothing changed.
//   - Stale (legacy layout, or an old schemaVersion): backs up the existing
//     config.json to config.json.bak, then delegates to Migrate to bring it
//     (and local.json, if also stale) up to CurrentSchemaVersion. Returns
//     the combined StepsApplied+LegacyIngested labels as changes, plus the
//     backup file path.
//
// ErrVersionTooNew is returned unchanged: a config written by a newer
// plugin version cannot be auto-migrated backward, so this still hard-stops
// the caller.
func MigrateWithBackup(projectRoot string) (changes []string, backupPath string, err error) {
	configPath := filepath.Join(projectRoot, paths.DataDir, "config.json")
	_, statErr := os.Stat(configPath)
	configExists := statErr == nil

	if !configExists && !hasLegacy(projectRoot) {
		return nil, "", fmt.Errorf(
			"%w: no SDLC config found at %s; run /setup to initialize this project",
			ErrConfigMissing, filepath.Join(paths.DataDir, "config.json"),
		)
	}

	verifyErr := Verify(projectRoot)
	if verifyErr == nil {
		return nil, "", nil
	}
	if errors.Is(verifyErr, ErrVersionTooNew) {
		return nil, "", verifyErr
	}

	// Back up the existing config.json before Migrate rewrites it in place.
	// A purely-legacy project (config.json not yet created) has nothing to
	// back up here — ingestLegacy only ever writes a fresh config.json/
	// local.json, it never modifies the legacy source files it reads from.
	if configExists {
		data, readErr := os.ReadFile(configPath)
		if readErr != nil {
			return nil, "", fmt.Errorf("%w: read config.json for backup: %v", ErrMigrationFailed, readErr)
		}
		backupPath = configPath + ".bak"
		if writeErr := os.WriteFile(backupPath, data, 0o644); writeErr != nil {
			return nil, "", fmt.Errorf("%w: write config.json.bak: %v", ErrMigrationFailed, writeErr)
		}
	}

	report, migErr := Migrate(projectRoot, Options{})
	if migErr != nil {
		return nil, backupPath, migErr
	}

	changes = append(changes, report.StepsApplied...)
	changes = append(changes, report.LegacyIngested...)
	return changes, backupPath, nil
}

// ---------------------------------------------------------------------------
// Lock management
// ---------------------------------------------------------------------------

// acquireLock creates .sdlc/.migration.lock with O_EXCL. Retries on EEXIST
// up to retries times with retryDelay between attempts.
func acquireLock(mainRoot string, opt *Options) (string, error) {
	sdlcDir := filepath.Join(mainRoot, paths.DataDir)
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		return "", fmt.Errorf("%w: create %s directory: %v", ErrMigrationFailed, paths.DataDir, err)
	}

	lockPath := filepath.Join(sdlcDir, ".migration.lock")
	retries := opt.retries()
	delay := opt.retryDelay()

	for attempt := 0; attempt <= retries; attempt++ {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d", os.Getpid())
			f.Close()
			return lockPath, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("%w: lock create failed: %v", ErrMigrationFailed, err)
		}
		if attempt < retries {
			time.Sleep(delay)
		}
	}

	return "", fmt.Errorf(
		"%w: lock file %s held by another process; if stale, remove it and retry",
		ErrMigrationLocked, lockPath,
	)
}

func releaseLock(lockPath string) {
	os.Remove(lockPath) //nolint:errcheck // best-effort
}

// ---------------------------------------------------------------------------
// Version detection
// ---------------------------------------------------------------------------

// hasLegacy returns true if any pre-v5 legacy config files exist.
func hasLegacy(mainRoot string) bool {
	for _, marker := range legacyMarkers {
		if _, err := os.Stat(filepath.Join(mainRoot, marker)); err == nil {
			return true
		}
	}
	return false
}

// extractSchemaVersion extracts the schemaVersion integer from a raw config.
func extractSchemaVersion(raw map[string]any) (int, bool) {
	v, ok := raw["schemaVersion"]
	if !ok {
		return 0, false
	}
	if f, ok := v.(float64); ok {
		return int(f), true
	}
	return 0, false
}

// detectProjectVersion determines the schema version of the project config.
// Returns (version, true) if a config or legacy unified config exists.
func detectProjectVersion(mainRoot string) (int, bool) {
	configPath := filepath.Join(mainRoot, paths.DataDir, "config.json")
	var raw map[string]any
	if err := fsx.ReadJSON(configPath, &raw); err == nil {
		if v, ok := extractSchemaVersion(raw); ok {
			return v, true
		}
		return CurrentSchemaVersion, true // no schemaVersion → v5
	}
	// Check legacy unified config.
	legacyPath := filepath.Join(mainRoot, ".claude", "sdlc.json")
	if _, err := os.Stat(legacyPath); err == nil {
		return 0, true
	}
	return 0, false
}

// detectLocalVersion determines the schema version of the local config.
func detectLocalVersion(mainRoot string) (int, bool) {
	localPath := filepath.Join(mainRoot, paths.DataDir, "local.json")
	var raw map[string]any
	if err := fsx.ReadJSON(localPath, &raw); err != nil {
		return 0, false
	}
	if v, ok := extractSchemaVersion(raw); ok {
		return v, true
	}
	// Legacy "version" integer (pre-v3 local configs).
	if v, ok := raw["version"]; ok {
		if f, ok := v.(float64); ok {
			return int(f), true
		}
	}
	return 1, true // exists without version info → v1
}

// ---------------------------------------------------------------------------
// Step planning and execution
// ---------------------------------------------------------------------------

// planSteps finds the ordered migration steps from `from` to `to`.
func planSteps(registry []migrationStep, from, to int) ([]migrationStep, error) {
	var steps []migrationStep
	cursor := from
	for cursor < to {
		found := false
		for _, s := range registry {
			if s.from == cursor && s.to <= to {
				steps = append(steps, s)
				cursor = s.to
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf(
				"%w: no migration step from v%d toward v%d",
				ErrMigrationFailed, cursor, to,
			)
		}
	}
	return steps, nil
}

// runSteps executes migration steps sequentially, rolling back on failure.
func runSteps(ctx *migrationContext, steps []migrationStep, role string) ([]string, error) {
	var labels []string
	var applied []migrationStep
	for _, step := range steps {
		label := fmt.Sprintf("%s: v%d→v%d", role, step.from, step.to)
		if err := step.run(ctx); err != nil {
			// Roll back in reverse.
			for i := len(applied) - 1; i >= 0; i-- {
				if applied[i].rollback != nil {
					applied[i].rollback(ctx) //nolint:errcheck // best-effort
				}
			}
			return labels, fmt.Errorf("%w: %s: %v", ErrMigrationFailed, label, err)
		}
		labels = append(labels, label)
		applied = append(applied, step)
	}
	return labels, nil
}

// ---------------------------------------------------------------------------
// Legacy ingestion
// ---------------------------------------------------------------------------

// ingestLegacy reads all legacy per-section config files and writes v5
// output (no schemaVersion). Project sections go to config.json; local
// sections (ship, review) go to local.json.
func ingestLegacy(mainRoot string) ([]string, error) {
	if err := os.MkdirAll(filepath.Join(mainRoot, paths.DataDir), 0o755); err != nil {
		return nil, fmt.Errorf("%w: create %s dir: %v", ErrMigrationFailed, paths.DataDir, err)
	}

	var ingested []string
	projectCfg := make(map[string]any)
	localCfg := make(map[string]any)

	// .claude/sdlc.json — old unified config.
	sdlcPath := filepath.Join(mainRoot, ".claude", "sdlc.json")
	var sdlcData map[string]any
	if err := fsx.ReadJSON(sdlcPath, &sdlcData); err == nil {
		ingested = append(ingested, ".claude/sdlc.json")
		for _, key := range []string{"version", "jira", "commit", "pr", "plan", "execute"} {
			if v, ok := sdlcData[key]; ok {
				projectCfg[key] = v
			}
		}
		if v, ok := sdlcData["ship"]; ok {
			if m, ok := v.(map[string]any); ok {
				localCfg["ship"] = m
			}
		}
		if v, ok := sdlcData["review"]; ok {
			if m, ok := v.(map[string]any); ok {
				localCfg["review"] = m
			}
		}
	}

	// .claude/version.json — old version config.
	versionPath := filepath.Join(mainRoot, ".claude", "version.json")
	var versionData map[string]any
	if err := fsx.ReadJSON(versionPath, &versionData); err == nil {
		ingested = append(ingested, ".claude/version.json")
		if projectCfg["version"] == nil {
			delete(versionData, "$schema")
			projectCfg["version"] = versionData
		}
	}

	// .sdlc/jira-config.json — old jira config.
	jiraPath := filepath.Join(mainRoot, paths.LegacyDataDir, "jira-config.json")
	var jiraData map[string]any
	if err := fsx.ReadJSON(jiraPath, &jiraData); err == nil {
		ingested = append(ingested, ".sdlc/jira-config.json")
		if projectCfg["jira"] == nil {
			delete(jiraData, "$schema")
			projectCfg["jira"] = jiraData
		}
	}

	// .sdlc/ship-config.json → local.json ship section.
	shipPath := filepath.Join(mainRoot, paths.LegacyDataDir, "ship-config.json")
	var shipData map[string]any
	if err := fsx.ReadJSON(shipPath, &shipData); err == nil {
		ingested = append(ingested, ".sdlc/ship-config.json")
		if localCfg["ship"] == nil {
			delete(shipData, "$schema")
			delete(shipData, "version")
			applyShipPresetToSteps(shipData)
			applyShipAwaitReview(shipData)
			localCfg["ship"] = shipData
		}
	}

	// .sdlc/review.json or .claude/review.json → local.json review section.
	reviewPath := filepath.Join(mainRoot, paths.LegacyDataDir, "review.json")
	var reviewData map[string]any
	if err := fsx.ReadJSON(reviewPath, &reviewData); err == nil {
		ingested = append(ingested, ".sdlc/review.json")
		if localCfg["review"] == nil {
			localCfg["review"] = extractReviewDefaults(reviewData)
		}
	} else {
		claudeReviewPath := filepath.Join(mainRoot, ".claude", "review.json")
		var claudeReviewData map[string]any
		if err := fsx.ReadJSON(claudeReviewPath, &claudeReviewData); err == nil {
			ingested = append(ingested, ".claude/review.json")
			if localCfg["review"] == nil {
				localCfg["review"] = extractReviewDefaults(claudeReviewData)
			}
		}
	}

	// Write v5 config.json (project sections, no schemaVersion).
	configPath := filepath.Join(mainRoot, paths.DataDir, "config.json")
	if len(projectCfg) > 0 {
		if err := fsx.AtomicWriteJSON(configPath, projectCfg); err != nil {
			return ingested, fmt.Errorf("%w: write config.json: %v", ErrMigrationFailed, err)
		}
	}

	// Write v5 local.json (local sections, no schemaVersion).
	localPath := filepath.Join(mainRoot, paths.DataDir, "local.json")
	if len(localCfg) > 0 {
		if err := fsx.AtomicWriteJSON(localPath, localCfg); err != nil {
			return ingested, fmt.Errorf("%w: write local.json: %v", ErrMigrationFailed, err)
		}
	}

	return ingested, nil
}
