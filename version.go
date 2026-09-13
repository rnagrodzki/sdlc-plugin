// Package version is the single source of truth for the sdlc plugin's
// version string. It embeds plugins/sdlc/.claude-plugin/plugin.json at
// compile time, so the version reported by the binary (MCP server,
// hooks.PluginVersion, `sdlc version`) can never drift from the manifest —
// there is nothing left to hand-sync after a release promotion bumps the
// manifest's "version" field.
package version

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed plugins/sdlc/.claude-plugin/plugin.json
var manifestJSON []byte

// Plugin is the sdlc plugin's version, read from plugin.json's "version"
// field at compile time.
var Plugin = mustParsePluginVersion(manifestJSON)

func mustParsePluginVersion(raw []byte) string {
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		panic(fmt.Sprintf("version: parse plugin.json: %v", err))
	}
	if manifest.Version == "" {
		panic("version: plugin.json has no \"version\" field")
	}
	return manifest.Version
}
