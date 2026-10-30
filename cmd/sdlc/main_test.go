package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
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
// learning, three delete, preplan status, History delete, wait dismiss, run
// detail and session detail routes call the tools functions, and the cache
// clear route passes the root, the time and dashboard.LogPath() to the clear
// function.
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
	if o.SetPreplanStatus == nil || reflect.ValueOf(o.SetPreplanStatus).Pointer() != reflect.ValueOf(tools.DashboardSetPreplanStatus).Pointer() {
		t.Error("SetPreplanStatus is not tools.DashboardSetPreplanStatus")
	}
	if o.DeleteRuns == nil || reflect.ValueOf(o.DeleteRuns).Pointer() != reflect.ValueOf(tools.DashboardDeleteRuns).Pointer() {
		t.Error("DeleteRuns is not tools.DashboardDeleteRuns")
	}
	if o.DismissWait == nil || reflect.ValueOf(o.DismissWait).Pointer() != reflect.ValueOf(tools.DashboardDismissWait).Pointer() {
		t.Error("DismissWait is not tools.DashboardDismissWait")
	}
	if o.RunDetail == nil || reflect.ValueOf(o.RunDetail).Pointer() != reflect.ValueOf(tools.DashboardRunDetail).Pointer() {
		t.Error("RunDetail is not tools.DashboardRunDetail")
	}
	if o.SessionDetail == nil || reflect.ValueOf(o.SessionDetail).Pointer() != reflect.ValueOf(tools.DashboardSessionDetail).Pointer() {
		t.Error("SessionDetail is not tools.DashboardSessionDetail")
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

// TestDashboardOptions_CollectSlim checks that the collect function of
// "sdlc dashboard serve" returns the page snapshot: a completed run has
// detailDeferred true, no step detail, the detail kind of its commit step,
// and an empty non-nil issue list.
func TestDashboardOptions_CollectSlim(t *testing.T) {
	t.Setenv("SDLC_CACHE_DIR", t.TempDir())
	root := t.TempDir()
	runs := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{
		"branch": "feat/x", "pipelineStatus": "completed", "pipelineCompletedAt": "2026-10-09T07:50:00Z",
		"steps": []any{
			map[string]any{"name": "execute", "status": "completed", "startedAt": "2026-10-09T07:00:00Z"},
			map[string]any{"name": "commit", "status": "completed", "startedAt": "2026-10-09T07:40:00Z",
				"result": "nothing to commit: the working tree is clean"},
		},
	}
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runs, "ship-feat-x-20261009T070000Z.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	mtime := now.Add(-5 * time.Minute)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}

	o := dashboardOptions(7385, "v-test", func() {}, nil)
	snap := o.Collect([]string{root}, now)
	if len(snap.Repos) != 1 || len(snap.Repos[0].Pipelines) != 1 {
		t.Fatalf("snapshot = %+v, want one repo with one pipeline", snap)
	}
	p := snap.Repos[0].Pipelines[0]
	if p.Status != tools.PipelineCompleted || !p.DetailDeferred {
		t.Errorf("status, detailDeferred = %q, %v; want completed, true", p.Status, p.DetailDeferred)
	}
	if p.Issues == nil || len(p.Issues) != 0 {
		t.Errorf("issues = %#v, want an empty non-nil slice", p.Issues)
	}
	if len(p.Steps) != 2 {
		t.Fatalf("steps = %+v, want 2", p.Steps)
	}
	if c := p.Steps[1]; c.Detail != nil || c.DetailKind != "result" {
		t.Errorf("commit step detail, detailKind = %+v, %q; want nil, result", c.Detail, c.DetailKind)
	}
}
