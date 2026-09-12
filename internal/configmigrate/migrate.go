// Package configmigrate provides schema-version detection for the SDLC
// plugin's configuration files. The plugin ships config.toml/local.toml as
// of schema v1 (TOML era); there is no automated migration from the legacy
// JSON-era config.json/local.json format (schema v0). A project stuck on v0
// must re-run /setup — see Migrate and MigrateWithBackup below.
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
	// no .sdlc-v2 directory of any kind. Returned only by
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
// if config.toml exists (v1) or the project has no .sdlc-v2 directory at
// all (nothing to verify yet — /setup handles that case). Returns
// ErrVersionStale if a .sdlc-v2 directory exists without a config.toml
// (JSON-era v0, or an empty scaffold).
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
// than attempt a conversion. A fresh project (no .sdlc-v2 directory) is a
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
//   - No .sdlc-v2 directory at all: the project was never set up. Returns
//     ErrConfigMissing (wrapped with an actionable message naming /setup)
//     rather than fabricating a config from nothing.
//   - Current (config.toml and local.toml, wherever present, are both at
//     CurrentSchemaVersion): a no-op. Returns (nil, "", nil) without
//     touching the filesystem.
//   - Stale (a .sdlc-v2 directory exists but config.toml and/or
//     local.toml is missing — the JSON-era v0 layout, or an incomplete
//     scaffold): there is no automated JSON→TOML migration, so this
//     returns the same ErrVersionStale error as Migrate. No backup is
//     written and no file is touched — callers must treat a non-nil error
//     here as a hard stop pointing the user at /setup.
func MigrateWithBackup(projectRoot string) (changes []string, backupPath string, err error) {
	projectVer, projectExists := detectProjectVersion(projectRoot)
	localVer, localExists := detectLocalVersion(projectRoot)

	if !projectExists && !localExists {
		return nil, "", fmt.Errorf(
			"%w: no SDLC config found at %s; run /setup to initialize this project",
			ErrConfigMissing, filepath.Join(paths.DataDir, "config.toml"),
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
// (0, true) if the .sdlc-v2 directory exists but config.toml does not
// (JSON-era v0, or an incomplete scaffold — needs /setup). Returns
// (0, false) if there is no .sdlc-v2 directory at all (never set up).
func detectProjectVersion(mainRoot string) (int, bool) {
	tomlPath := filepath.Join(mainRoot, paths.DataDir, "config.toml")
	if _, err := os.Stat(tomlPath); err == nil {
		return CurrentSchemaVersion, true
	}

	sdlcDir := filepath.Join(mainRoot, paths.DataDir)
	if _, err := os.Stat(sdlcDir); err == nil {
		return 0, true
	}

	return 0, false
}

// detectLocalVersion determines the schema version of the local config,
// mirroring detectProjectVersion's file-existence rules for local.toml.
func detectLocalVersion(mainRoot string) (int, bool) {
	tomlPath := filepath.Join(mainRoot, paths.DataDir, "local.toml")
	if _, err := os.Stat(tomlPath); err == nil {
		return CurrentSchemaVersion, true
	}

	sdlcDir := filepath.Join(mainRoot, paths.DataDir)
	if _, err := os.Stat(sdlcDir); err == nil {
		return 0, true
	}

	return 0, false
}
