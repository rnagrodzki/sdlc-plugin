package main

import (
	"encoding/json"
	"os"
	"testing"
)

// TestPluginVersionMatchesManifest guards against the hardcoded pluginVersion
// const silently drifting from plugins/sdlc/.claude-plugin/plugin.json's
// "version" field — the two must be kept in sync by hand, and a mismatch
// here previously caused hooks.PluginVersion (and so the session-start
// "sdlc: v..." announcement) to report a stale version.
func TestPluginVersionMatchesManifest(t *testing.T) {
	raw, err := os.ReadFile("../../plugins/sdlc/.claude-plugin/plugin.json")
	if err != nil {
		t.Fatalf("os.ReadFile(plugin.json): %v", err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("json.Unmarshal(plugin.json): %v", err)
	}
	if manifest.Version != pluginVersion {
		t.Errorf("pluginVersion const = %q, plugin.json version = %q — keep these in sync", pluginVersion, manifest.Version)
	}
}
