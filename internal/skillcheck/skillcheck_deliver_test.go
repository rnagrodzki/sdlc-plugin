// Package skillcheck cross-checks the MCP tool names and execute_state
// action names referenced by skills/deliver-sdlc/SKILL.md (Task 51) against
// the tool names actually registered on the Go MCP server, and guards
// against the skill's config-sourcing prose drifting from the config v5
// `automation` field names it must read directly (Finding 5, AC2).
//
// This file intentionally contains no production logic — every unexported
// identifier below is prefixed deliverSkills, and every declared Test
// function is prefixed TestDeliverSkills, to avoid name collisions with the
// sibling skillcheck_test.go, skillcheck_review_test.go, and
// skillcheck_ship_test.go files in this same package.
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

// deliverSkillsFiles lists the Task 51 deliverable skill file, relative to
// the repository root, that this test cross-checks against the registry.
var deliverSkillsFiles = []string{
	"skills/deliver-sdlc/SKILL.md",
}

// deliverSkillsCallRe matches this repo's documented tool-call pseudocode
// convention: an identifier immediately followed by "({", e.g.
// "execute_state({".
var deliverSkillsCallRe = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\(\{`)

// deliverSkillsBuiltinTools are Claude Code's own built-in tools. Skill
// files legitimately call these in pseudocode (e.g. "Read({ ... })"); they
// are never registered on our MCP server and must be excluded from the
// cross-check.
var deliverSkillsBuiltinTools = map[string]bool{
	"Read": true, "Write": true, "Edit": true, "Bash": true,
	"Grep": true, "Glob": true, "AskUserQuestion": true,
	"Skill": true, "Agent": true, "WebFetch": true, "WebSearch": true,
}

// deliverSkillsRepoRoot resolves the repository root from this test file's
// own path (internal/skillcheck/skillcheck_deliver_test.go), independent of
// the directory `go test` happens to be invoked from.
func deliverSkillsRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("deliverSkillsRepoRoot: runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// deliverSkillsReadFile reads a skill file relative to the repo root,
// failing the test loudly on any read error.
func deliverSkillsReadFile(t *testing.T, repoRoot, rel string) string {
	t.Helper()
	path := filepath.Join(repoRoot, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// deliverSkillsBuildRegistry constructs a fresh mcpserver.Server, registers
// every tool family currently in internal/tools (every Register*Tools
// function, not just the one deliver-sdlc happens to call directly), starts
// an in-process MCP client against it, and returns the set of registered
// tool names as reported by a real ListTools() call.
//
// This registers more families than the sibling ship/review/commit
// skillcheck files' own *BuildRegistry helpers do — internal/tools has
// grown new Register*Tools functions (dimensions render, setup-write) since
// those sibling files were written. Building fresh here, against the
// current source, is deliberate: this test should assert against the real
// tool surface as it exists today, not a snapshot frozen at a sibling
// task's completion.
func deliverSkillsBuildRegistry(t *testing.T) map[string]bool {
	t.Helper()

	srv := mcpserver.New("skillcheck-deliver-test", "0.0.0-test")

	tools.RegisterCommitTools(srv)
	tools.RegisterDimensionsRenderTools(srv)
	tools.RegisterErrorReportTools(srv)
	tools.RegisterExecuteStateTools(srv)
	tools.RegisterHardenTools(srv)
	tools.RegisterJiraTools(srv)
	tools.RegisterLinksTools(srv)
	tools.RegisterMCPFailureTools(srv)
	tools.RegisterMigrateTools(srv)
	tools.RegisterOpenspecTools(srv)
	tools.RegisterPlanExploreTools(srv)
	tools.RegisterPlanTools(srv)
	tools.RegisterPollingTools(srv)
	tools.RegisterPRTools(srv)
	tools.RegisterReceivedReviewTools(srv)
	tools.RegisterReviewTools(srv)
	tools.RegisterScaffoldTools(srv)
	tools.RegisterSetupTools(srv)
	tools.RegisterSetupWriteTools(srv)
	tools.RegisterShipStateTools(srv)
	tools.RegisterShipTools(srv)
	tools.RegisterValidateTools(srv)
	tools.RegisterVersionTools(srv)

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
	initReq.Params.ClientInfo = mcp.Implementation{Name: "skillcheck-deliver-test", Version: "0.0.0"}
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

// deliverSkillsToolRefs extracts every call-shaped tool reference from a
// skill file's content, excluding Claude Code's own built-in tools and any
// external (non-registry) MCP namespace such as "mcp__atlassian__...".
func deliverSkillsToolRefs(content string) []string {
	seen := map[string]bool{}
	var refs []string
	for _, m := range deliverSkillsCallRe.FindAllStringSubmatch(content, -1) {
		name := m[1]
		if deliverSkillsBuiltinTools[name] || strings.HasPrefix(name, "mcp__") {
			continue
		}
		if !seen[name] {
			seen[name] = true
			refs = append(refs, name)
		}
	}
	return refs
}

// TestDeliverSkillsToolReferencesAreRegistered asserts that every MCP tool
// name called out in skills/deliver-sdlc/SKILL.md is actually registered on
// the Go MCP server — guarding against skill prose drifting from the real
// tool surface. deliver-sdlc is deliberately thin here: per its own design
// (Findings 1 and 6), it never calls a dispatched sub-skill's own MCP tools
// directly, so the only tool it references at all is execute_state (its one
// legitimate direct call, action "verify-completeness", mirroring
// ship-sdlc's own post-execute sanity check).
func TestDeliverSkillsToolReferencesAreRegistered(t *testing.T) {
	repoRoot := deliverSkillsRepoRoot(t)
	registered := deliverSkillsBuildRegistry(t)

	for _, rel := range deliverSkillsFiles {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			content := deliverSkillsReadFile(t, repoRoot, rel)

			refs := deliverSkillsToolRefs(content)
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

// TestDeliverSkillsRegistryContainsExpectedTools pins the specific tool
// name deliver-sdlc is documented to call directly, by exact name, so a
// future rename in internal/tools fails loudly here with a precise message
// instead of only in the broader scan above.
func TestDeliverSkillsRegistryContainsExpectedTools(t *testing.T) {
	registered := deliverSkillsBuildRegistry(t)

	expected := []string{
		"execute_state",
	}

	for _, name := range expected {
		if !registered[name] {
			t.Errorf("expected tool %q to be registered, but it is not", name)
		}
	}
}

// deliverSkillsStepHeaders maps each of deliver-sdlc's six canonical phase
// names to the exact "### ..." section heading that documents that phase in
// skills/deliver-sdlc/SKILL.md. Used by TestDeliverSkillsStepActionCrossCheck
// to isolate each phase's own prose block before checking which dispatch
// call, config field, or execute_state action it references.
var deliverSkillsStepHeaders = map[string]string{
	"plan":            "### plan",
	"execute":         "### execute",
	"review":          "### review",
	"fix-loop":        "### fix-loop",
	"verify-pipeline": "### verify-pipeline",
	"ship":            "### ship",
	"terminal":        "### terminal",
}

// deliverSkillsStepExpectedRefs maps each canonical phase name to substring(s)
// at least one of which must appear within that phase's own section --
// either the black-box dispatch call for the sub-skill that phase invokes
// (Finding 1), the execute_state action it legitimately calls (Finding 3),
// or the config v5 automation field names it sources directly (Finding 5).
// deliver-sdlc never calls ship_state/execute_state's step-shaped actions
// (none exist on execute_state; ship_state's are ship-sdlc-internal), so
// unlike shipSkillsStepExpectedActions this map is not action-name-only.
var deliverSkillsStepExpectedRefs = map[string][]string{
	"plan":            {"planPath"},
	"execute":         {"execute-plan-sdlc", "verify-completeness"},
	"review":          {"review-sdlc", "saved-review"},
	"fix-loop":        {"reviewFixIterations", "reviewFixSeverityThreshold"},
	"verify-pipeline": {"ship-sdlc"},
	"ship":            {"ship-sdlc", "manual-gate-pending"},
	"terminal":        {"delivered", "failed("},
}

// deliverSkillsHeadingRe matches any Markdown ATX heading line ("## ..." or
// "### ..."), used to find the end boundary of a phase's own section.
var deliverSkillsHeadingRe = regexp.MustCompile(`(?m)^#{2,3} .+$`)

// deliverSkillsSection extracts the block of content starting immediately
// after the line exactly equal to heading and ending at the next Markdown
// heading line (any level from ## to ###), or end of file. Returns "" and
// false if heading does not appear as an exact line in content.
func deliverSkillsSection(content, heading string) (string, bool) {
	lines := strings.Split(content, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimRight(line, " \t") == heading {
			start = i + 1
			break
		}
	}
	if start == -1 {
		return "", false
	}
	rest := strings.Join(lines[start:], "\n")
	loc := deliverSkillsHeadingRe.FindStringIndex(rest)
	if loc == nil {
		return rest, true
	}
	return rest[:loc[0]], true
}

// TestDeliverSkillsStepActionCrossCheck is the step/heading cross-check
// (AC1-equivalent, mirroring TestShipSkillsStepActionCrossCheck's shape):
// every one of deliver-sdlc's six canonical phase names must have its own
// documented section in skills/deliver-sdlc/SKILL.md, and that section must
// reference at least one of the dispatch calls, execute_state actions, or
// automation config field names that phase's own prose depends on --
// guarding against a phase's documentation silently drifting away from what
// it actually does.
func TestDeliverSkillsStepActionCrossCheck(t *testing.T) {
	repoRoot := deliverSkillsRepoRoot(t)
	content := deliverSkillsReadFile(t, repoRoot, "skills/deliver-sdlc/SKILL.md")

	if len(deliverSkillsStepHeaders) != 7 {
		t.Fatalf("deliverSkillsStepHeaders has %d entries, want 7 (plan/execute/review/fix-loop/verify-pipeline/ship/terminal)", len(deliverSkillsStepHeaders))
	}
	if len(deliverSkillsStepExpectedRefs) != 7 {
		t.Fatalf("deliverSkillsStepExpectedRefs has %d entries, want 7", len(deliverSkillsStepExpectedRefs))
	}

	for step, heading := range deliverSkillsStepHeaders {
		step, heading := step, heading
		t.Run(step, func(t *testing.T) {
			section, ok := deliverSkillsSection(content, heading)
			if !ok {
				t.Fatalf("skills/deliver-sdlc/SKILL.md: expected heading %q for step %q not found", heading, step)
			}

			expected, known := deliverSkillsStepExpectedRefs[step]
			if !known {
				t.Fatalf("no expected reference(s) recorded for step %q", step)
			}

			foundAny := false
			for _, ref := range expected {
				if strings.Contains(section, ref) {
					foundAny = true
					break
				}
			}
			if !foundAny {
				t.Errorf("skills/deliver-sdlc/SKILL.md: step %q's section (heading %q) references none of the expected substring(s) %v",
					step, heading, expected)
			}
		})
	}
}

// TestDeliverSkillsFixLoopConfigFieldsPresent is Task 51's AC2 grep test:
// skills/deliver-sdlc/SKILL.md must source its fix-loop bounds from config
// v5's `automation` section field names by name, in its own body text, not
// via ship-sdlc-internal StepMode (Finding 5). This is the first grep-based
// guard test of this kind in the skillcheck package -- prior sibling guard
// tests (TestReviewSkillsNoOrchestratorReferences,
// TestReviewSkillsNoWriteGuardReferences) assert the *absence* of a retired
// string; this one asserts the *presence* of two required config field
// names, using the same plain strings.Contains mechanics.
func TestDeliverSkillsFixLoopConfigFieldsPresent(t *testing.T) {
	repoRoot := deliverSkillsRepoRoot(t)
	content := deliverSkillsReadFile(t, repoRoot, "skills/deliver-sdlc/SKILL.md")

	for _, field := range []string{"reviewFixIterations", "reviewFixSeverityThreshold"} {
		if !strings.Contains(content, field) {
			t.Errorf("skills/deliver-sdlc/SKILL.md: missing required config v5 automation field name %q", field)
		}
	}
}
