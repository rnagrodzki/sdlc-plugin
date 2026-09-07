// Command sdlc is the single entry point for the sdlc Claude Code plugin.
//
// main() only dispatches on the requested mode; all real behavior lives in
// testable internal/ packages so this file stays trivial to reason about.
package main

import (
	"fmt"
	"os"

	"github.com/rnagrodzki/sdlc-plugin/internal/hooks"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// pluginVersion mirrors the "version" field in plugins/sdlc/.claude-plugin/plugin.json.
const pluginVersion = "1.0.0"

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
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: sdlc <mcp|hook|version>")
}

func runVersion() {
	fmt.Println(pluginVersion)
}

// runMCP builds the MCP server, registers every tool group, and serves it
// over stdio until the client disconnects or an error occurs.
func runMCP() {
	s := mcpserver.New("sdlc", pluginVersion)

	tools.RegisterVersionTools(s)
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
	os.Exit(hooks.Run(os.Args[2], os.Stdin, os.Stdout))
}
