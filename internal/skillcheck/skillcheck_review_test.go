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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
	tools.RegisterValidateTools(srv)
	tools.RegisterDimensionsRenderTools(srv)
	tools.RegisterSetupWriteTools(srv)
	tools.RegisterLearningsTools(srv)

	mcpSrv := srv.MCPServer()

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := mcpSrv.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "skillcheck-review-test", Version: "0.0.0"}, nil)
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

// reviewSkillsReasonRe matches a stop reason value written as reason: "x".
var reviewSkillsReasonRe = regexp.MustCompile(`reason:\s*"([a-z]+)"`)

// reviewSkillsForbiddenRe matches the retired wording of the run ID and
// worker ID rules, which Go now owns.
var reviewSkillsForbiddenRe = regexp.MustCompile("(?i)sanitize|slugify|mint a `?runId")

// reviewSkillsExecuteStateProblems returns one message for each execute_state
// call in content whose action value is not in enum. It skips a call to
// another tool and a call that sets no action value.
func reviewSkillsExecuteStateProblems(content string, enum map[string]bool) []string {
	var problems []string
	for _, c := range flagParityToolCalls(content) {
		if c.name != "execute_state" || c.action == "" || enum[c.action] {
			continue
		}
		problems = append(problems, fmt.Sprintf("line %d: execute_state action %q is not in the registered action enum", c.line, c.action))
	}
	return problems
}

// reviewSkillsEnumOf returns the enum values of the top-level property prop
// in a tool input schema. It returns an error when the schema cannot be
// encoded or decoded, when it has no such property, or when the property has
// no enum.
func reviewSkillsEnumOf(schema any, prop string) (map[string]bool, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	var parsed struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}
	p, ok := parsed.Properties[prop]
	if !ok {
		return nil, fmt.Errorf("schema has no property %q", prop)
	}
	if len(p.Enum) == 0 {
		return nil, fmt.Errorf("property %q has no enum", prop)
	}
	set := make(map[string]bool, len(p.Enum))
	for _, v := range p.Enum {
		set[v] = true
	}
	return set, nil
}

// reviewSkillsReadSkill reads the review skill file under the plugin root.
func reviewSkillsReadSkill(t *testing.T) string {
	t.Helper()
	return executeSkillsReadFile(t, filepath.Join(reviewSkillsRepoRoot(t), "skills", "review", "SKILL.md"))
}

// TestReviewSkillsExecuteStateProblems pins every branch of
// reviewSkillsExecuteStateProblems with the exact messages it returns.
func TestReviewSkillsExecuteStateProblems(t *testing.T) {
	enum := map[string]bool{"ledger_skip": true, "ledger_status": true}
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "unknown action is reported with its line",
			content: "intro\nexecute_state({ action: \"ledger_skipp\", runId })\n",
			want:    []string{`line 2: execute_state action "ledger_skipp" is not in the registered action enum`},
		},
		{
			name:    "known action is accepted",
			content: "execute_state({ action: \"ledger_skip\", runId })\n",
		},
		{
			name:    "call to another tool is skipped",
			content: "learnings_log({ action: \"bogus\" })\n",
		},
		{
			name:    "call without an action value is skipped",
			content: "execute_state({ runId })\n",
		},
		{
			name:    "each unknown action is reported",
			content: "execute_state({ action: \"nope\" })\nexecute_state({ action: \"ledger_status\" })\nexecute_state({ action: \"gone\" })\n",
			want: []string{
				`line 1: execute_state action "nope" is not in the registered action enum`,
				`line 3: execute_state action "gone" is not in the registered action enum`,
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := reviewSkillsExecuteStateProblems(tc.content, enum)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("problems = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestReviewSkillsEnumOf pins every branch of reviewSkillsEnumOf.
func TestReviewSkillsEnumOf(t *testing.T) {
	t.Run("returns the enum of the property", func(t *testing.T) {
		schema := map[string]any{"properties": map[string]any{"reason": map[string]any{"enum": []string{"a", "b"}}}}
		got, err := reviewSkillsEnumOf(schema, "reason")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 || !got["a"] || !got["b"] {
			t.Errorf("enum = %v, want a and b", got)
		}
	})
	t.Run("missing property is an error", func(t *testing.T) {
		_, err := reviewSkillsEnumOf(map[string]any{"properties": map[string]any{}}, "reason")
		if err == nil || !strings.Contains(err.Error(), `no property "reason"`) {
			t.Errorf("error = %v, want a missing-property error", err)
		}
	})
	t.Run("property without enum is an error", func(t *testing.T) {
		schema := map[string]any{"properties": map[string]any{"reason": map[string]any{"type": "string"}}}
		_, err := reviewSkillsEnumOf(schema, "reason")
		if err == nil || !strings.Contains(err.Error(), "has no enum") {
			t.Errorf("error = %v, want a no-enum error", err)
		}
	})
	t.Run("schema that cannot be encoded is an error", func(t *testing.T) {
		_, err := reviewSkillsEnumOf(make(chan int), "reason")
		if err == nil || !strings.Contains(err.Error(), "marshal schema") {
			t.Errorf("error = %v, want a marshal error", err)
		}
	})
	t.Run("schema that is not an object is an error", func(t *testing.T) {
		_, err := reviewSkillsEnumOf("not an object", "reason")
		if err == nil || !strings.Contains(err.Error(), "unmarshal schema") {
			t.Errorf("error = %v, want an unmarshal error", err)
		}
	})
}

// TestReviewSkillActionsAreRegistered asserts that every execute_state action
// in the review skill is in the registered action enum, that the skill calls
// the ledger actions it relies on, and that an unknown action name in the
// skill text is caught.
func TestReviewSkillActionsAreRegistered(t *testing.T) {
	content := reviewSkillsReadSkill(t)
	enum := flagParityBuildRegistry(t).actions["execute_state"]
	if len(enum) == 0 {
		t.Fatal("execute_state has no registered action enum")
	}

	if problems := reviewSkillsExecuteStateProblems(content, enum); len(problems) > 0 {
		t.Errorf("skills/review/SKILL.md: %s", strings.Join(problems, "; "))
	}

	called := map[string]bool{}
	for _, c := range flagParityToolCalls(content) {
		if c.name == "execute_state" {
			called[c.action] = true
		}
	}
	for _, want := range []string{"ledger_checkin", "ledger_checkout", "ledger_status", "ledger_skip"} {
		if !called[want] {
			t.Errorf("skills/review/SKILL.md: no execute_state call with action %q", want)
		}
	}

	broken := strings.Replace(content, `action: "ledger_skip"`, `action: "ledger_skipp"`, 1)
	if broken == content {
		t.Fatal("skills/review/SKILL.md has no ledger_skip call to alter")
	}
	got := reviewSkillsExecuteStateProblems(broken, enum)
	if len(got) != 1 || !strings.Contains(got[0], `"ledger_skipp"`) {
		t.Errorf("an unknown action in the skill text gave problems %q, want one problem naming ledger_skipp", got)
	}
}

// TestReviewSkillStopReasonsMatchSchema asserts that the stop reasons written
// in the review skill are exactly the values of the ledger_skip reason enum.
func TestReviewSkillStopReasonsMatchSchema(t *testing.T) {
	var schema any
	for _, tl := range executeSkillsListTools(t) {
		if tl.Name == "execute_state" {
			schema = tl.InputSchema
		}
	}
	if schema == nil {
		t.Fatal("execute_state tool not found in registry")
	}
	enum, err := reviewSkillsEnumOf(schema, "reason")
	if err != nil {
		t.Fatalf("execute_state reason enum: %v", err)
	}

	found := map[string]bool{}
	for _, m := range reviewSkillsReasonRe.FindAllStringSubmatch(reviewSkillsReadSkill(t), -1) {
		found[m[1]] = true
	}
	flagParityAssertSameSet(t, "review skill stop reasons (got) vs ledger_skip reason enum (want)", found, enum)
}

// TestReviewSkillCallKeysMatchSchema asserts that every "key: value" argument
// key of every registered-tool call in the review skill is a property of that
// tool's input schema, and that the review_prepare call block passes dryRun. A
// shorthand key such as runId in the ledger_skip calls is not extracted:
// TestReviewSkillRunPlanText pins the text of those calls.
func TestReviewSkillCallKeysMatchSchema(t *testing.T) {
	schemas := planSkillsBuildSchemas(t)
	dryRunCalls := 0
	for _, site := range planSkillsExtractCalls(reviewSkillsReadSkill(t)) {
		props, ok := schemas[site.name]
		if !ok {
			continue // an unregistered tool name is reported by TestReviewSkillsToolReferencesAreRegistered
		}
		for _, key := range site.keys {
			if !props[key] {
				t.Errorf("skills/review/SKILL.md:%d: %s call passes key %q, which is not in the tool input schema", site.line, site.name, key)
			}
			if site.name == "review_prepare" && key == "dryRun" {
				dryRunCalls++
			}
		}
	}
	if dryRunCalls == 0 {
		t.Error("skills/review/SKILL.md: no review_prepare call passes dryRun")
	}
}

// TestReviewSkillRunPlanText asserts that the review skill reads the run ID
// and the worker ID from the manifest, forwards --dry-run, passes the exact
// ledger_skip calls for a stalled and a missing worker, and that neither the
// skill nor its docs page keeps the retired ID rules.
func TestReviewSkillRunPlanText(t *testing.T) {
	skill := reviewSkillsReadSkill(t)
	docsPath := filepath.Join(reviewSkillsRepoRoot(t), "..", "..", "docs", "skills", "review.md")
	docs := executeSkillsReadFile(t, docsPath)

	for _, want := range []string{
		"runId := manifest.run_id",
		"workerId := dimension.worker_id",
		"dryRun: <true when --dry-run>",
		"forwarded as `dryRun: true`",
		"`target`, `skipConfigCheck`, and `dryRun`",
		`execute_state({ action: "ledger_skip", runId, workerId, reason: "stalled" })`,
		`execute_state({ action: "ledger_skip", runId, workerId, reason: "missing" })`,
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("skills/review/SKILL.md: missing %q", want)
		}
	}
	for name, text := range map[string]string{"skills/review/SKILL.md": skill, "docs/skills/review.md": docs} {
		if hit := reviewSkillsForbiddenRe.FindString(text); hit != "" {
			t.Errorf("%s: still holds the retired ID rule wording %q", name, hit)
		}
	}
}
