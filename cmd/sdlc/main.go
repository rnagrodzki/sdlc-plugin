// Command sdlc is the single entry point for the sdlc Claude Code plugin.
//
// main() only dispatches on the requested mode; all real behavior lives in
// testable internal/ packages so this file stays trivial to reason about.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	version "github.com/rnagrodzki/sdlc-plugin"
	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard"
	"github.com/rnagrodzki/sdlc-plugin/internal/dashboard/web"
	"github.com/rnagrodzki/sdlc-plugin/internal/hooks"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// pluginVersion is read from plugins/sdlc/.claude-plugin/plugin.json at
// compile time (see version.go) — there is nothing left to hand-sync after
// a release bumps the manifest's "version" field.
var pluginVersion = version.Plugin

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "mcp":
		runMCP()
	case "hook":
		runHook()
	case "version":
		runVersion()
	case "dashboard":
		os.Exit(runDashboard(os.Args[2:]))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: sdlc <mcp|hook|version|dashboard>")
}

func runVersion() {
	info := version.GetBuildInfo()
	fmt.Printf("sdlc v%s (commit %s, built %s)\n", info.PluginVersion, info.Commit, info.Time)
}

// runMCP builds the MCP server, registers every tool group, and serves it
// over stdio until the client disconnects or an error occurs.
func runMCP() {
	s := mcpserver.New("sdlc", pluginVersion)

	tools.RegisterPlanTools(s)
	tools.RegisterPlanExploreTools(s)
	tools.RegisterLinksTools(s)
	tools.RegisterMCPFailureTools(s)
	tools.RegisterReviewTools(s)
	tools.RegisterExecuteStateTools(s)
	tools.RegisterReceivedReviewTools(s)
	tools.RegisterCommitTools(s)
	tools.RegisterScaffoldTools(s)
	tools.RegisterSetupTools(s)
	tools.RegisterSetupWriteTools(s)
	tools.RegisterPRTools(s)
	tools.RegisterPrepareOrchestratorTools(s)
	tools.RegisterOpenspecTools(s)
	tools.RegisterDimensionsRenderTools(s)
	tools.RegisterValidateTools(s)
	tools.RegisterShipStateTools(s)
	tools.RegisterJiraTools(s)
	tools.RegisterPollingTools(s)
	tools.RegisterMigrateTools(s)
	tools.RegisterShipTools(s)
	tools.RegisterPlanSupportTools(s)
	tools.RegisterLearningsTools(s)
	tools.RegisterDashboardTools(s)

	if err := s.ServeStdio(); err != nil {
		fmt.Fprintf(os.Stderr, "sdlc mcp: %v\n", err)
		os.Exit(1)
	}
}

// runHook dispatches "sdlc hook <name>" to the internal/hooks registry,
// wiring the plugin's own version into the hooks package first.
func runHook() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: sdlc hook <name>")
		os.Exit(2)
	}

	hooks.PluginVersion = pluginVersion
	info := version.GetBuildInfo()
	hooks.BuildCommit = info.Commit
	hooks.BuildTime = info.Time
	os.Exit(hooks.Run(os.Args[2], os.Stdin, os.Stdout))
}

// runDashboard runs "sdlc dashboard serve [--port N]": the local dashboard
// web server, in the foreground until POST /api/stop, SIGTERM, or SIGINT.
// It returns the process exit code (see web.Serve).
func runDashboard(args []string) int {
	if len(args) == 0 || args[0] != "serve" {
		fmt.Fprintln(os.Stderr, web.UsageError)
		return 2
	}
	port, err := web.ParseServeArgs(args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v := pluginVersion
	// logPath is the file that receives the output of this server when it
	// runs in the background. A cache clear truncates it.
	logPath := filepath.Join(dashboard.Dir(), "server.log")
	return web.Serve(ctx, web.Options{
		Port:    port,
		Version: v,
		Token:   web.NewToken(),
		Stop:    cancel,
		Roots:   dashboard.Roots,
		Collect: func(roots []string, now time.Time) tools.DashboardSnapshot {
			return tools.CollectDashboardSnapshot(roots, now, v)
		},
		Listen:  net.Listen,
		Health:  dashboard.DefaultDeps().Health,
		Archive: tools.ArchiveRun,
		ClearCache: func(root string, now time.Time) (tools.ClearCacheOut, error) {
			return tools.ClearCache(root, logPath, now)
		},
		LearningBody: tools.DashboardLearningBody,
	})
}
