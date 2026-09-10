// Package skillcheck is a documentation/tool-surface consistency check for
// Task 42's rewritten SKILL.md files (commit, version, pr).
// It builds a real MCP tool registry by calling every Register*Tools
// function in internal/tools against a fresh mcpserver.Server, then
// verifies every MCP tool name referenced by the rewritten skill docs is
// actually registered.
//
// This file intentionally contains no production logic — per this task's
// ruling, no shared skillcheck.go production file exists. A second,
// independently developed test file (skillcheck_review_test.go, from a
// parallel task covering review) may land in this same package. Every
// unexported identifier below is prefixed commitSkills, and every declared
// Test function is prefixed TestCommitSkills, specifically to avoid name
// collisions with that parallel file.
package skillcheck

import (
	"context"
	"os"
	"regexp"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// commitSkillsFiles lists the SKILL.md files this task rewrote to call
// Go-backed MCP tools instead of shelling out to node scripts. Paths are
// relative to this package directory (internal/skillcheck).
var commitSkillsFiles = []string{
	"../../plugins/sdlc/skills/commit/SKILL.md",
	"../../plugins/sdlc/skills/version/SKILL.md",
	"../../plugins/sdlc/skills/pr/SKILL.md",
}

// commitSkillsToolCallPattern matches this repo's documented tool-call
// pseudocode convention: a snake_case identifier (containing at least one
// underscore, to avoid matching plain English words) immediately followed
// by "(" and an object-literal "{", e.g. `commit_prepare({ ... })` or
// `links_validate({ file: ... })`.
var commitSkillsToolCallPattern = regexp.MustCompile(`\b([a-z][a-z0-9]*(?:_[a-z0-9]+)+)\s*\(\s*\{`)

// commitSkillsBuildRegistry constructs a fresh mcpserver.Server, registers
// every tool family that exists in internal/tools (every Register*Tools
// function, confirmed via `grep -rn '^func Register.*Tools' internal/tools`
// — not just the tool families the three rewritten skills happen to use),
// starts an in-process MCP client against it, and returns the set of
// registered tool names.
func commitSkillsBuildRegistry(t *testing.T) map[string]bool {
	t.Helper()

	srv := mcpserver.New("skillcheck-test", "0.0.0-test")

	tools.RegisterCommitTools(srv)
	tools.RegisterExecuteStateTools(srv)
	tools.RegisterJiraTools(srv)
	tools.RegisterPrepareOrchestratorTools(srv)
	tools.RegisterMigrateTools(srv)
	tools.RegisterOpenspecTools(srv)
	tools.RegisterLinksTools(srv)
	tools.RegisterMCPFailureTools(srv)
	tools.RegisterPollingTools(srv)
	tools.RegisterPlanExploreTools(srv)
	tools.RegisterPlanTools(srv)
	tools.RegisterReceivedReviewTools(srv)
	tools.RegisterPRTools(srv)
	tools.RegisterScaffoldTools(srv)
	tools.RegisterReviewTools(srv)
	tools.RegisterShipTools(srv)
	tools.RegisterSetupTools(srv)
	tools.RegisterShipStateTools(srv)
	tools.RegisterVersionTools(srv)
	tools.RegisterValidateTools(srv)
	tools.RegisterLearningsTools(srv)
	tools.RegisterDimensionsRenderTools(srv)
	tools.RegisterPlanSupportTools(srv)
	tools.RegisterSetupWriteTools(srv)

	mcpSrv := srv.MCPServer()

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := mcpSrv.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "skillcheck-test", Version: "0.0.0"}, nil)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	resp, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if resp == nil {
		t.Fatal("ListTools: nil response")
	}

	names := make(map[string]bool, len(resp.Tools))
	for _, tl := range resp.Tools {
		names[tl.Name] = true
	}
	if len(names) == 0 {
		t.Fatal("registry is empty — Register*Tools calls above registered nothing")
	}
	return names
}

// TestCommitSkillsToolReferencesExistInRegistry scans the three SKILL.md
// files rewritten by Task 42 for every MCP tool call reference and asserts
// each one names a tool that is actually registered somewhere in
// internal/tools. This catches typos in tool names and references to tools
// that don't exist (or were renamed) in the Go port.
func TestCommitSkillsToolReferencesExistInRegistry(t *testing.T) {
	registry := commitSkillsBuildRegistry(t)

	for _, path := range commitSkillsFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		matches := commitSkillsToolCallPattern.FindAllStringSubmatch(string(data), -1)
		if len(matches) == 0 {
			t.Fatalf("%s: no tool-call references found — pattern may be stale or the file lost its tool calls", path)
		}

		seen := make(map[string]bool, len(matches))
		for _, m := range matches {
			seen[m[1]] = true
		}
		for name := range seen {
			if !registry[name] {
				t.Errorf("%s: references MCP tool %q, which is not registered by any Register*Tools function", path, name)
			}
		}
	}
}

// TestCommitSkillsRegistryContainsExpectedTools is a narrower sanity check:
// every tool this task's rewritten skills are documented to call must be
// present in the registry, by exact name. This pins the specific tool names
// used across commit, version, and pr so a future rename in
// internal/tools fails loudly here instead of only in the broader scan
// above (which would also catch it, but this gives a more precise failure).
func TestCommitSkillsRegistryContainsExpectedTools(t *testing.T) {
	registry := commitSkillsBuildRegistry(t)

	expected := []string{
		"commit_prepare",
		"commit_apply",
		"links_validate",
		"version_prepare",
		"version_apply",
		"scaffold_ci",
		"pr_prepare",
		"pr_apply",
	}

	for _, name := range expected {
		if !registry[name] {
			t.Errorf("expected tool %q to be registered, but it is not", name)
		}
	}
}
