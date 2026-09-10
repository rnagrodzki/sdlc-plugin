package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// forbiddenMCPImport is the import path of the MCP Go SDK this project
// migrated off of in favor of the official github.com/modelcontextprotocol/go-sdk
// (see internal/mcpserver). Built by concatenation so this guard test's own
// source does not itself match the pattern it scans for.
var forbiddenMCPImport = "\"github.com/" + "mark3labs/mcp-go"

// TestForbidMark3LabsMCPGo fails the build if github.com/mark3labs/mcp-go is
// reintroduced anywhere: as a go.mod requirement, or as an import in any .go
// file in the repository. The project deliberately migrated every mcpserver
// caller to the official SDK, including re-deriving jsonschema_description /
// jsonschema:"enum=..." tag support (internal/mcpserver/schema.go) rather
// than keep the old dependency around — this test is the tripwire that keeps
// it from creeping back in.
func TestForbidMark3LabsMCPGo(t *testing.T) {
	_, selfFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot, err := filepath.Abs(filepath.Join(filepath.Dir(selfFile), "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	goModPath := filepath.Join(repoRoot, "go.mod")
	goModRaw, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if strings.Contains(string(goModRaw), "mark3labs/mcp-go") {
		t.Error("go.mod requires github.com/mark3labs/mcp-go — this project migrated to github.com/modelcontextprotocol/go-sdk; do not reintroduce it")
	}

	err = filepath.Walk(repoRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || path == selfFile {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), forbiddenMCPImport) {
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				rel = path
			}
			t.Errorf("%s imports github.com/mark3labs/mcp-go — this project migrated to github.com/modelcontextprotocol/go-sdk; do not reintroduce it", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo for .go files: %v", err)
	}
}
