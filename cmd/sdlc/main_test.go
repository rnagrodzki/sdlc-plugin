package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

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

// TestDashboardOptions pins the wiring of "sdlc dashboard serve": the archive,
// learning and three delete routes call the tools functions, and the cache clear route
// passes the root, the time and dashboard.LogPath() to the clear function.
func TestDashboardOptions(t *testing.T) {
	t.Setenv("SDLC_CACHE_DIR", t.TempDir())

	var gotRoot, gotLog string
	var gotNow time.Time
	clearFn := func(root, serverLog string, now time.Time) (tools.ClearCacheOut, error) {
		gotRoot, gotLog, gotNow = root, serverLog, now
		return tools.ClearCacheOut{FreedBytes: 7}, nil
	}
	stopped := false
	o := dashboardOptions(7385, "v-test", func() { stopped = true }, clearFn)

	if o.Port != 7385 || o.Version != "v-test" || o.Token == "" {
		t.Errorf("Port, Version, Token = %d, %q, %q; want 7385, v-test and a token", o.Port, o.Version, o.Token)
	}
	if o.Archive == nil || reflect.ValueOf(o.Archive).Pointer() != reflect.ValueOf(tools.ArchiveRun).Pointer() {
		t.Error("Archive is not tools.ArchiveRun")
	}
	if o.LearningBody == nil || reflect.ValueOf(o.LearningBody).Pointer() != reflect.ValueOf(tools.DashboardLearningBody).Pointer() {
		t.Error("LearningBody is not tools.DashboardLearningBody")
	}
	if o.DeletePreplan == nil || reflect.ValueOf(o.DeletePreplan).Pointer() != reflect.ValueOf(tools.DashboardDeletePreplan).Pointer() {
		t.Error("DeletePreplan is not tools.DashboardDeletePreplan")
	}
	if o.DeleteDeferred == nil || reflect.ValueOf(o.DeleteDeferred).Pointer() != reflect.ValueOf(tools.DashboardDeleteDeferred).Pointer() {
		t.Error("DeleteDeferred is not tools.DashboardDeleteDeferred")
	}
	if o.DeleteLearning == nil || reflect.ValueOf(o.DeleteLearning).Pointer() != reflect.ValueOf(tools.DashboardDeleteLearning).Pointer() {
		t.Error("DeleteLearning is not tools.DashboardDeleteLearning")
	}
	if o.ClearCache == nil {
		t.Fatal("ClearCache is nil")
	}
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	out, err := o.ClearCache("/repo/a", now)
	if err != nil || out.FreedBytes != 7 {
		t.Errorf("ClearCache = %+v, %v; want the result of the clear function", out, err)
	}
	if gotRoot != "/repo/a" || !gotNow.Equal(now) {
		t.Errorf("clear function got root %q at %v; want /repo/a at %v", gotRoot, gotNow, now)
	}
	if want := dashboard.LogPath(); gotLog != want {
		t.Errorf("clear function got server log %q; want dashboard.LogPath() %q", gotLog, want)
	}
	if o.Stop == nil {
		t.Fatal("Stop is nil")
	}
	o.Stop()
	if !stopped {
		t.Error("Stop does not call the stop function")
	}
}
