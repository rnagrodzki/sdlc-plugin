// schema.go holds the v5 config schema validation logic.
//
// Instead of embedding the full JSON schema (which lives at
// plugins/sdlc/schemas/sdlc-config.schema.json, a directory a go:embed
// directive cannot reach from this package directory), we extract the
// top-level property whitelist and validate structurally. TestSchemaSync in
// config_test.go verifies the whitelist stays byte-synchronised with the
// schema file.
package config

import (
	"fmt"
	"sort"
)

// AllowedProjectKeys is the set of top-level property names permitted in
// .sdlc-v2/config.toml, extracted from plugins/sdlc/schemas/sdlc-config.schema.json.
// TestSchemaSync verifies this list stays in sync with the schema file.
// Exported so downstream tests (e.g. setup_test.go) can verify coverage.
var AllowedProjectKeys = map[string]bool{
	"version": true,
	"jira":    true,
	"commit":  true,
	"pr":      true,
	"plan":    true,
	"execute": true,
}

// allowedLocalOnlyKeys is the set of top-level schema properties that are
// personal preference, not team contract: they route to .sdlc-v2/local.toml
// (see ProjectSections in config.go) and must never appear in
// .sdlc-v2/config.toml, so they are deliberately excluded from
// AllowedProjectKeys. TestSchemaSync checks schema top-level properties
// against the union of AllowedProjectKeys and this map.
var allowedLocalOnlyKeys = map[string]bool{
	"planStyle": true,
}

// validateProjectKeys checks that every top-level key in raw belongs to
// AllowedProjectKeys. Returns an error listing any unknown keys.
func validateProjectKeys(raw map[string]any) error {
	var unknown []string
	for k := range raw {
		if !AllowedProjectKeys[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("config: unknown top-level keys in .sdlc-v2/config.toml: %v", unknown)
}
