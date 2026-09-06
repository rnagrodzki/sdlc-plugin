// schema.go holds the v5 config schema validation logic.
//
// Instead of embedding the full JSON schema (which lives at
// schemas/sdlc-config.schema.json and cannot be reached via go:embed from
// this package directory), we extract the top-level property whitelist and
// validate structurally. TestSchemaSync in config_test.go verifies the
// whitelist stays byte-synchronised with the schema file.
package config

import (
	"fmt"
	"sort"
)

// allowedProjectKeys is the set of top-level property names permitted in
// .sdlc/config.json, extracted from schemas/sdlc-config.schema.json.
// TestSchemaSync verifies this list stays in sync with the schema file.
var allowedProjectKeys = map[string]bool{
	"$schema": true,
	"version": true,
	"jira":    true,
	"commit":  true,
	"pr":      true,
	"plan":    true,
	"execute": true,
}

// validateProjectKeys checks that every top-level key in raw belongs to
// allowedProjectKeys. Returns an error listing any unknown keys.
func validateProjectKeys(raw map[string]any) error {
	var unknown []string
	for k := range raw {
		if !allowedProjectKeys[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("config: unknown top-level keys in .sdlc/config.json: %v", unknown)
}
