// Package skillcheck cross-checks the MCP tool names referenced by the
// ported review-family skill files (review, received-review,
// jira -- Task 43) against the tool names actually registered on the
// Go MCP server, so skill prose cannot silently drift from the real tool
// surface.
//
// This file intentionally contains no production logic — per this task's
// ruling, no shared skillcheck.go production file exists. A sibling test
// file (skillcheck_test.go, from a parallel task covering commit,
// version, and pr) already lands in this same package and
// anticipates this one arriving alongside it. Every unexported identifier
// below is prefixed reviewSkills, and every declared Test function is
// prefixed TestReviewSkills, to avoid name collisions with that file.
//
// Registry construction mirrors that sibling file's approach rather than
// inventing a second one: mcpserver.Server exposes its MCPServer() accessor
// for exactly this purpose, so a genuine
// client.NewInProcessClient(...) -> Start -> Initialize -> ListTools
// round trip (the literal Ruling Set 4 recipe) is reachable directly from
// this sibling package.
package skillcheck

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// reviewSkillsFiles lists the Task 43 deliverable skill files, relative to
// the repository root, that this test cross-checks against the registry.
var reviewSkillsFiles = []string{
	"skills/review/SKILL.md",
	"skills/received-review/SKILL.md",
	"skills/jira/SKILL.md",
}

// reviewSkillsCallRe matches this repo's documented tool-call pseudocode
// convention: an identifier immediately followed by "({", e.g.
// "jira({" or "review_prepare({".
var reviewSkillsCallRe = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\(\{`)

// reviewSkillsBuiltinTools are Claude Code's own built-in tools. Skill
// files legitimately call these in pseudocode (e.g. "Read({ ... })"); they
// are never registered on our MCP server and must be excluded from the
// cross-check.
var reviewSkillsBuiltinTools = map[string]bool{
	"Read": true, "Write": true, "Edit": true, "Bash": true,
	"Grep": true, "Glob": true, "AskUserQuestion": true,
	"Skill": true, "Agent": true, "WebFetch": true, "WebSearch": true,
}

// reviewSkillsRepoRoot resolves the repository root from this test file's
// own path (internal/skillcheck/skillcheck_review_test.go), independent of
// the directory `go test` happens to be invoked from.
func reviewSkillsRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("reviewSkillsRepoRoot: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(file))), "plugins", "sdlc")
}

// reviewSkillsBuildRegistry constructs a fresh mcpserver.Server, registers
// every tool family in internal/tools (every Register*Tools function, not
// just the ones the three Task 43 skills happen to call), starts an
// in-process MCP client against it, and returns the set of registered tool
// names as reported by a real ListTools() call.
func reviewSkillsBuildRegistry(t *testing.T) map[string]bool {
	t.Helper()

	srv := mcpserver.New("skillcheck-review-test", "0.0.0-test")

	tools.RegisterCommitTools(srv)
	tools.RegisterPrepareOrchestratorTools(srv)
	tools.RegisterLinksTools(srv)
	tools.RegisterMCPFailureTools(srv)
	tools.RegisterExecuteStateTools(srv)
	tools.RegisterPlanExploreTools(srv)
	tools.RegisterJiraTools(srv)
	tools.RegisterPlanTools(srv)
	tools.RegisterMigrateTools(srv)
	tools.RegisterPRTools(srv)
	tools.RegisterOpenspecTools(srv)
	tools.RegisterPollingTools(srv)
	tools.RegisterReceivedReviewTools(srv)
	tools.RegisterReviewTools(srv)
	tools.RegisterScaffoldTools(srv)
	tools.RegisterSetupTools(srv)
	tools.RegisterShipStateTools(srv)
	tools.RegisterShipTools(srv)
	tools.RegisterVersionTools(srv)
	tools.RegisterValidateTools(srv)
	tools.RegisterDimensionsRenderTools(srv)
	tools.RegisterSetupWriteTools(srv)
	tools.RegisterLearningsTools(srv)

	mcpSrv := srv.MCPServer()

	c, err := client.NewInProcessClient(mcpSrv)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "skillcheck-review-test", Version: "0.0.0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	resp, err := c.ListTools(ctx, mcp.ListToolsRequest{})
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

// reviewSkillsToolRefs extracts every call-shaped tool reference from a
// skill file's content, excluding Claude Code's own built-in tools and any
// external (non-registry) MCP namespace such as "mcp__atlassian__...".
func reviewSkillsToolRefs(content string) []string {
	seen := map[string]bool{}
	var refs []string
	for _, m := range reviewSkillsCallRe.FindAllStringSubmatch(content, -1) {
		name := m[1]
		if reviewSkillsBuiltinTools[name] || strings.HasPrefix(name, "mcp__") {
			continue
		}
		if !seen[name] {
			seen[name] = true
			refs = append(refs, name)
		}
	}
	return refs
}

// TestReviewSkillsToolReferencesAreRegistered asserts that every MCP tool
// name called out in the three Task 43 skill files (review,
// received-review, jira) is actually registered on the Go MCP
// server — guarding against skill prose drifting from the real tool
// surface (e.g. a tool renamed in internal/tools without the skill being
// updated to match).
func TestReviewSkillsToolReferencesAreRegistered(t *testing.T) {
	repoRoot := reviewSkillsRepoRoot(t)
	registered := reviewSkillsBuildRegistry(t)

	for _, rel := range reviewSkillsFiles {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join(repoRoot, rel)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			refs := reviewSkillsToolRefs(string(data))
			if len(refs) == 0 {
				t.Fatalf("%s: found zero tool references -- the call-detection regex likely "+
					"no longer matches this file's call style", rel)
			}

			for _, name := range refs {
				if !registered[name] {
					t.Errorf("%s: references MCP tool %q, which is not registered by any "+
						"Register*Tools function in internal/tools", rel, name)
				}
			}
		})
	}
}

// TestReviewSkillsRegistryContainsExpectedTools pins the specific tool
// names the three Task 43 skills are documented to call, by exact name, so
// a future rename in internal/tools fails loudly here with a precise
// message instead of only in the broader scan above.
func TestReviewSkillsRegistryContainsExpectedTools(t *testing.T) {
	registered := reviewSkillsBuildRegistry(t)

	expected := []string{
		"review_prepare",
		"execute_state",
		"links_validate",
		"received_review_prepare",
		"jira",
		"mcp_failure_record",
	}

	for _, name := range expected {
		if !registered[name] {
			t.Errorf("expected tool %q to be registered, but it is not", name)
		}
	}
}

// TestReviewSkillsNoOrchestratorReferences is the Files-note acceptance
// criterion: none of the three skill files may reference the retired
// review-orchestrator agent (Ruling Set 1 replaced it with flat
// background-agent dispatch and a file-based ledger).
func TestReviewSkillsNoOrchestratorReferences(t *testing.T) {
	repoRoot := reviewSkillsRepoRoot(t)
	for _, rel := range reviewSkillsFiles {
		path := filepath.Join(repoRoot, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(strings.ToLower(string(data)), "review-orchestrator") {
			t.Errorf("%s: still references review-orchestrator", rel)
		}
	}
}

// TestReviewSkillsNoWriteGuardReferences is the jira acceptance
// criterion (Ruling Set 3): the retired pre-tool-jira-write-guard.js hook
// must not be referenced — AskUserQuestion (Step 2.6) is the sole
// enforcement boundary for write dispatch in this port.
func TestReviewSkillsNoWriteGuardReferences(t *testing.T) {
	repoRoot := reviewSkillsRepoRoot(t)
	path := filepath.Join(repoRoot, "skills/jira/SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if strings.Contains(string(data), "pre-tool-jira-write-guard") {
		t.Error("skills/jira/SKILL.md: still references pre-tool-jira-write-guard")
	}
}
