// configtemplates.go embeds the setup scaffold templates
// (plugins/sdlc/templates/config.toml and local.toml) so internal/tools/setup.go
// can write them verbatim without keeping a second, driftable copy as a Go
// string constant. It also embeds the error-report issue template for
// internal/tools/error_report.go.
//
// go:embed patterns can only reach files inside the embedding source file's
// own package directory subtree (they may not use "." or ".." to climb out).
// internal/tools sits alongside plugins/, not above it, so a go:embed
// directive there cannot reach plugins/sdlc/templates/*.toml. This package
// lives at the repo root, which is an ancestor of plugins/, so it can — the
// same reason version.Plugin embeds plugins/sdlc/.claude-plugin/plugin.json
// from here instead of from internal/version.

package version

import _ "embed"

// ConfigTemplate is the default config.toml scaffold written by /setup.
//
//go:embed plugins/sdlc/templates/config.toml
var ConfigTemplate string

// LocalTemplate is the default local.toml scaffold written by /setup.
//
//go:embed plugins/sdlc/templates/local.toml
var LocalTemplate string

// ToolingErrorTemplate is the error-report issue body template. The
// error_report manifest carries its text so the error-report-orchestrator
// agent never has to locate the plugin's skills/ tree on disk.
//
//go:embed plugins/sdlc/skills/error-report/templates/ToolingError.md
var ToolingErrorTemplate string
