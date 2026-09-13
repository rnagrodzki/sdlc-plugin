package configmigrate

// ---------------------------------------------------------------------------
// Migration step type
// ---------------------------------------------------------------------------
//
// migrationStep and the registries below are retained as empty scaffolding
// per the TOML config migration plan's contract shape: the gap from schema
// v0 (JSON era) to v1 (TOML era) is intentional and has no migration steps.
// Migrate/MigrateWithBackup in migrate.go fail outright on a v0 config
// instead of consulting these registries — see their doc comments.

type migrationStep struct {
	from int
	to   int
	run  func(ctx *migrationContext) error
}

type migrationContext struct {
	mainRoot string
}

// Migration registries: empty. No auto-migration from JSON.
var projectMigrations = []migrationStep{}
var localMigrations = []migrationStep{}
