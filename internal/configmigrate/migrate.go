// Package configmigrate provides schema-version detection for the SDLC
// plugin's configuration files. The plugin ships config.toml/local.toml as
// of schema v1 (TOML era); there is no automated migration from the legacy
// JSON-era config.json/local.json format (schema v0). A project stuck on v0
// must re-run /setup — see Migrate and MigrateWithBackup below.
//
// The package also moves personal keys (pr.expectedAccount and the
// execute.auto/quality/highRiskAutoApprove preferences) from the committed
// config.toml to the gitignored local.toml — see MigrateMovedKeys in
// movedkeys.go. That is a key move inside the TOML era, not a JSON-to-TOML
// migration.
package configmigrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// CurrentSchemaVersion is the current schema version. TOML era — fresh
// start. JSON-era schema versions (0-6) are history; see git log for the
// migration-step machinery that used to bridge them.
const CurrentSchemaVersion = 1

// Sentinel errors.
var (
	// ErrVersionStale indicates a v0 (JSON-era) config was found. There is
	// no automated migration path from JSON to TOML; the caller must be
	// pointed at /setup.
	ErrVersionStale = errors.New("configmigrate: version stale")

	// ErrConfigMissing indicates the project has no SDLC config at all —
	// no config.toml, config.json, local.toml, or local.json (the .sdlc-v2
	// directory may still exist holding tool data). Returned only by
	// MigrateWithBackup, which distinguishes "never set up" from "stale"
	// so callers can point the user at /setup instead of silently
	// proceeding on bare defaults.
	ErrConfigMissing = errors.New("configmigrate: config missing")
)

// staleMsg is the AC-mandated error text for a v0 (JSON-era) project: no
// migration is attempted, just a clear pointer at the fix.
const staleMsg = "TOML config required. Run /setup to initialize."

// Options configures the Migrate function. Retained as an empty struct for
// signature compatibility with existing callers (internal/tools/migrate.go
// constructs configmigrate.Options{}); the TOML era Migrate performs no
// filesystem writes, so there is nothing left to configure.
type Options struct{}

// Report describes what the migration did. TOML-era Migrate never mutates
// anything, so a returned *Report is always zero-valued; the fields are
// retained for signature compatibility with callers that inspect them
// (internal/tools/migrate.go, internal/tools/ship.go).
type Report struct {
	Migrated       bool
	StepsApplied   []string
	LegacyIngested []string
}

// ---------------------------------------------------------------------------
// Verify
// ---------------------------------------------------------------------------

// Verify checks if the project config at mainRoot is current. Returns nil
// if config.toml exists (v1) or the project has no project config at all
// (nothing to verify yet — /setup handles that case). A .sdlc-v2 directory
// that holds only tool data (state, caches, jira templates) and no config
// file is "no config", not stale. Returns ErrVersionStale only if a JSON-era
// .sdlc-v2/config.json exists without a config.toml.
func Verify(mainRoot string) error {
	ver, exists := detectProjectVersion(mainRoot)
	if !exists || ver == CurrentSchemaVersion {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrVersionStale, staleMsg)
}

// ---------------------------------------------------------------------------
// Migrate
// ---------------------------------------------------------------------------

// Migrate is the only migration entry point. There is no JSON→TOML
// migration path (clean break, per the TOML config migration plan): a
// v0 (JSON-era) project or local config makes Migrate fail outright rather
// than attempt a conversion. A fresh project (no config file) is a
// no-op — /setup is responsible for creating the initial TOML config, not
// Migrate.
func Migrate(mainRoot string, opt Options) (*Report, error) {
	projectVer, projectExists := detectProjectVersion(mainRoot)
	localVer, localExists := detectLocalVersion(mainRoot)

	stale := (projectExists && projectVer < CurrentSchemaVersion) ||
		(localExists && localVer < CurrentSchemaVersion)
	if stale {
		return nil, fmt.Errorf("%w: %s", ErrVersionStale, staleMsg)
	}

	return &Report{}, nil
}

// ---------------------------------------------------------------------------
// MigrateWithBackup — auto-migrate gate for ship_prepare / execute's init
// ---------------------------------------------------------------------------

// MigrateWithBackup is the auto-migrate gate used by ship_prepare and
// execute_state's "init" action in place of a hard Verify failure. It
// three-way classifies projectRoot's config:
//
//   - No project or local config file at all (no .sdlc-v2 directory, or
//     one holding only tool data): the project was never set up. Returns
//     ErrConfigMissing (wrapped with an actionable message naming /setup)
//     rather than fabricating a config from nothing.
//   - Current (config.toml and local.toml, wherever present, are both at
//     CurrentSchemaVersion): a no-op. Returns (nil, "", nil) without
//     touching the filesystem.
//   - Stale (a JSON-era config.json or local.json exists without its
//     TOML replacement): there is no automated JSON→TOML migration, so this
//     returns the same ErrVersionStale error as Migrate. No backup is
//     written and no file is touched — callers must treat a non-nil error
//     here as a hard stop pointing the user at /setup.
func MigrateWithBackup(projectRoot string) (changes []string, backupPath string, err error) {
	projectVer, projectExists := detectProjectVersion(projectRoot)
	localVer, localExists := detectLocalVersion(projectRoot)

	if !projectExists && !localExists {
		return nil, "", fmt.Errorf(
			"%w: no SDLC config found at %s; run /setup to initialize this project",
			ErrConfigMissing, filepath.Join(paths.DataDir, paths.ConfigFile),
		)
	}

	stale := (projectExists && projectVer < CurrentSchemaVersion) ||
		(localExists && localVer < CurrentSchemaVersion)
	if stale {
		return nil, "", fmt.Errorf("%w: %s", ErrVersionStale, staleMsg)
	}

	return nil, "", nil
}

// ---------------------------------------------------------------------------
// Version detection
// ---------------------------------------------------------------------------

// detectProjectVersion determines the schema version of the project config.
// Returns (1, true) if config.toml exists (current, TOML era). Returns
// (0, true) if a JSON-era config.json exists without config.toml (v0 —
// needs /setup). Returns (0, false) if neither file exists (never set up).
// The .sdlc-v2 directory alone is not a config: tools such as jira write
// data there (templates, state artifacts) in projects that have no config,
// and that must not make every later call fail the config-version gate.
func detectProjectVersion(mainRoot string) (int, bool) {
	tomlPath := filepath.Join(mainRoot, paths.DataDir, paths.ConfigFile)
	if _, err := os.Stat(tomlPath); err == nil {
		return CurrentSchemaVersion, true
	}

	jsonPath := filepath.Join(mainRoot, paths.DataDir, paths.LegacyConfigJSONFile)
	if _, err := os.Stat(jsonPath); err == nil {
		return 0, true
	}

	return 0, false
}

// detectLocalVersion determines the schema version of the local config.
// As with detectProjectVersion, mere existence of the .sdlc-v2 directory does
// NOT imply staleness: local.toml/local.json is documented as optional, so a
// project with a current config.toml but no local override ever created
// must not be reported as stale. Returns (1, true) if local.toml exists.
// Returns (0, true) if a legacy local.json exists (JSON-era v0, genuinely
// stale). Returns (0, false) if neither file exists, regardless of whether
// .sdlc-v2 itself exists -- no local override was ever created.
func detectLocalVersion(mainRoot string) (int, bool) {
	tomlPath := filepath.Join(mainRoot, paths.DataDir, paths.LocalConfigFile)
	if _, err := os.Stat(tomlPath); err == nil {
		return CurrentSchemaVersion, true
	}

	jsonPath := filepath.Join(mainRoot, paths.DataDir, paths.LegacyLocalJSONFile)
	if _, err := os.Stat(jsonPath); err == nil {
		return 0, true
	}

	return 0, false
}
