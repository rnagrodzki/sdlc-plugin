// Package skillcheck cross-checks the MCP tool names and call-site
// parameters referenced by the ported setup skill (main SKILL.md plus
// its nine companion sub-flow files -- Task 44) against the tool names and
// input schemas actually registered on the Go MCP server, so skill prose
// cannot silently drift from the real tool surface.
//
// This file intentionally contains no production logic — per this task's
// ruling, no shared skillcheck.go production file exists, and this file
// builds its own in-process MCP registry independently rather than sharing
// one with the sibling *_test.go files in this package. Every unexported
// identifier below is prefixed setupSkills, and every declared Test
// function is prefixed TestSetupSkills, to avoid name collisions with those
// files.
package skillcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/setupmeta"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// setupSkillsCallFiles lists the Task 44 setup files, relative to the
// repository root, that actually contain "name({ ... })" MCP tool-call
// pseudocode and are therefore cross-checked against the registry.
// setup-plan-template.md now calls setup_init({ writePlanTemplate: true })
// (Task 45 moved its scaffold write off a bare `cp` shell command and into
// that tool), so it belongs here rather than in setupSkillsReferenceOnlyFiles.
// Two of the nine companion files (setupSkillsReferenceOnlyFiles below) are
// pure reference tables with no tool calls at all and are checked separately
// for mere existence.
var setupSkillsCallFiles = []string{
	"skills/setup/SKILL.md",
	"skills/setup/setup-dimensions.md",
	"skills/setup/setup-pr-template.md",
	"skills/setup/setup-pr-labels.md",
	"skills/setup/setup-guardrails.md",
	"skills/setup/setup-execution-guardrails.md",
	"skills/setup/setup-openspec.md",
	"skills/setup/setup-plan-template.md",
}

// setupSkillsReferenceOnlyFiles are companion files that legitimately
// contain zero "name({ ... })" MCP tool-call sites: dimension-catalog.md and
// scan-patterns.md are static reference tables consumed by prose elsewhere.
// They are asserted to exist and be non-empty, not scanned for tool calls.
var setupSkillsReferenceOnlyFiles = []string{
	"skills/setup/dimension-catalog.md",
	"skills/setup/scan-patterns.md",
}

// setupSkillsCallRe matches this repo's documented tool-call pseudocode
// convention: an identifier immediately followed by "({", e.g.
// "setup_prepare({" or "migrate({".
var setupSkillsCallRe = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\(\{`)

// setupSkillsBuiltinTools are identifiers that legitimately match the
// "name({" call shape but are never registered on our MCP server, so they
// must be excluded from the cross-check:
//   - Claude Code's own built-in tools (skill prose calls these in
//     pseudocode too, e.g. "Read({ ... })")
//   - "stringify": setup's ported files use the literal expression
//     "JSON.stringify({ ... })" when building a `sectionsJson` argument for
//     setup_write_sections. The call-detection regex has no notion of the
//     "JSON." qualifier, so it matches "stringify({" as if it were its own
//     tool call. This is the one call-site shape unique to this task's
//     files (the Task 45 plan-family skills never inline a JSON literal
//     this way) — plan/error-report/harden skillcheck tests do not need
//     this exclusion.
var setupSkillsBuiltinTools = map[string]bool{
	"Read": true, "Write": true, "Edit": true, "Bash": true,
	"Grep": true, "Glob": true, "AskUserQuestion": true,
	"Skill": true, "Agent": true, "WebFetch": true, "WebSearch": true,
	"stringify": true,
}

// setupSkillsRepoRoot resolves the repository root from this test file's
// own path (internal/skillcheck/skillcheck_setup_test.go), independent of
// the directory `go test` happens to be invoked from.
func setupSkillsRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("setupSkillsRepoRoot: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(file))), "plugins", "sdlc")
}

// setupSkillsListTools constructs a fresh mcpserver.Server, registers every
// tool family in internal/tools (every Register*Tools function, not just
// the ones the Task 44 files happen to call), starts an in-process MCP
// client against it, and returns the tools reported by a real ListTools()
// call.
func setupSkillsListTools(t *testing.T) []*mcp.Tool {
	t.Helper()

	srv := mcpserver.New("skillcheck-setup-test", "0.0.0-test")

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
	tools.RegisterSetupWriteTools(srv)
	tools.RegisterShipStateTools(srv)
	tools.RegisterShipTools(srv)
	tools.RegisterValidateTools(srv)
	tools.RegisterDimensionsRenderTools(srv)
	tools.RegisterLearningsTools(srv)
	tools.RegisterInstructionsTools(srv)
	tools.RegisterDashboardTools(srv)

	mcpSrv := srv.MCPServer()

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := mcpSrv.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "skillcheck-setup-test", Version: "0.0.0"}, nil)
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
	if len(resp.Tools) == 0 {
		t.Fatal("registry is empty — Register*Tools calls above registered nothing")
	}
	return resp.Tools
}

// setupSkillsBuildRegistry returns the set of registered tool names, for the
// tool-existence cross-check.
func setupSkillsBuildRegistry(t *testing.T) map[string]bool {
	t.Helper()
	toolList := setupSkillsListTools(t)
	names := make(map[string]bool, len(toolList))
	for _, tl := range toolList {
		names[tl.Name] = true
	}
	return names
}

// setupSkillsBuildSchemas returns, for each registered tool, the set of its
// input schema's top-level property names — the params-match-schema
// cross-check is a membership test against this map, catching a call-site
// key that no longer exists on the tool's real Go input struct (e.g. a
// field renamed in internal/tools without the skill prose being updated to
// match).
func setupSkillsBuildSchemas(t *testing.T) map[string]map[string]bool {
	t.Helper()
	toolList := setupSkillsListTools(t)
	schemas := make(map[string]map[string]bool, len(toolList))
	for _, tl := range toolList {
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal input schema: %v", tl.Name, err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: unmarshal input schema: %v", tl.Name, err)
		}
		props := make(map[string]bool, len(schema.Properties))
		for k := range schema.Properties {
			props[k] = true
		}
		schemas[tl.Name] = props
	}
	return schemas
}

// setupSkillsCallSite is one "name({ ... })" tool-call reference found in a
// skill file, with its object-literal argument's top-level keys.
type setupSkillsCallSite struct {
	name string
	keys []string
	line int
}

// setupSkillsExtractArgKeys walks the object-literal argument of a
// "name({ ... })" call starting at the index of its opening "{" and returns
// the top-level (depth-1) keys: identifiers immediately followed by ":"
// (skipping whitespace), found while still inside the call's own outermost
// brace. String-literal contents are skipped whole, and any nested
// {}/() — or a genuine nested array/object value inside "[...]" — pushes
// depth past 1, so keys inside are correctly excluded from the top-level
// result rather than misattributed.
//
// One "[" shape is special-cased: this repo's own optional-parameter
// notation "[, key: value]" (an opening "[" immediately followed, modulo
// whitespace, by a ",") documents an optional call argument, not a nested
// array value. Depth-1 occurrences of it are treated as transparent — the
// bracket is skipped without changing depth — so "key" stays a checkable
// top-level key instead of being hidden one level deep. A bracket stack
// records which "[" openings were treated this way, so each "]" undoes the
// right thing regardless of nesting.
func setupSkillsExtractArgKeys(content string, openBrace int) []string {
	var keys []string
	depth := 0
	var transparent []bool // per open "[...]", true if it was the optional-param idiom
	n := len(content)
	i := openBrace
	for i < n {
		c := content[i]
		switch {
		case c == '"' || c == '\'' || c == '`':
			quote := c
			i++
			for i < n && content[i] != quote {
				if content[i] == '\\' && i+1 < n {
					i++
				}
				i++
			}
			if i < n {
				i++ // consume closing quote
			}
		case c == '[':
			j := i + 1
			for j < n && isSetupSkillsSpace(content[j]) {
				j++
			}
			isOptionalParam := depth == 1 && j < n && content[j] == ','
			transparent = append(transparent, isOptionalParam)
			if !isOptionalParam {
				depth++
			}
			i++
		case c == '{' || c == '(':
			depth++
			i++
		case c == ']':
			wasTransparent := false
			if len(transparent) > 0 {
				wasTransparent = transparent[len(transparent)-1]
				transparent = transparent[:len(transparent)-1]
			}
			i++
			if !wasTransparent {
				depth--
				if depth == 0 {
					return keys
				}
			}
		case c == '}' || c == ')':
			depth--
			i++
			if depth == 0 {
				return keys
			}
		case depth == 1 && isSetupSkillsIdentStart(c):
			start := i
			for i < n && isSetupSkillsIdentPart(content[i]) {
				i++
			}
			ident := content[start:i]
			j := i
			for j < n && isSetupSkillsSpace(content[j]) {
				j++
			}
			if j < n && content[j] == ':' {
				keys = append(keys, ident)
			}
		default:
			i++
		}
	}
	return keys
}

func isSetupSkillsIdentStart(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isSetupSkillsIdentPart(c byte) bool {
	return isSetupSkillsIdentStart(c) || (c >= '0' && c <= '9')
}

func isSetupSkillsSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// setupSkillsExtractCalls extracts every call-shaped tool reference from a
// skill file's content, excluding Claude Code's own built-in tools, the
// "stringify" false-positive documented on setupSkillsBuiltinTools, and any
// external (non-registry) MCP namespace such as "mcp__atlassian__...",
// along with each call's top-level argument keys and source line number.
func setupSkillsExtractCalls(content string) []setupSkillsCallSite {
	var sites []setupSkillsCallSite
	for _, m := range setupSkillsCallRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		if setupSkillsBuiltinTools[name] || strings.HasPrefix(name, "mcp__") {
			continue
		}
		// The match may start inside an external tool name that has a hyphen,
		// such as "mcp__t3-code__preview_open": \b matches after the hyphen.
		if m[2] > 0 && content[m[2]-1] == '-' {
			continue
		}
		openBrace := m[1] - 1 // the match ends right after the call's opening "{"
		keys := setupSkillsExtractArgKeys(content, openBrace)
		line := 1 + strings.Count(content[:m[0]], "\n")
		sites = append(sites, setupSkillsCallSite{name: name, keys: keys, line: line})
	}
	return sites
}

// setupSkillsToolNames returns the deduplicated set of tool names referenced
// across a slice of call sites.
func setupSkillsToolNames(sites []setupSkillsCallSite) []string {
	seen := map[string]bool{}
	var names []string
	for _, s := range sites {
		if !seen[s.name] {
			seen[s.name] = true
			names = append(names, s.name)
		}
	}
	return names
}

// TestSetupSkillsToolReferencesAreRegistered asserts that every MCP tool
// name called out in the Task 44 setup files is actually registered on
// the Go MCP server — guarding against skill prose drifting from the real
// tool surface.
func TestSetupSkillsToolReferencesAreRegistered(t *testing.T) {
	repoRoot := setupSkillsRepoRoot(t)
	registered := setupSkillsBuildRegistry(t)

	for _, rel := range setupSkillsCallFiles {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join(repoRoot, rel)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			sites := setupSkillsExtractCalls(string(data))
			names := setupSkillsToolNames(sites)
			if len(names) == 0 {
				t.Fatalf("%s: found zero tool references -- the call-detection regex likely "+
					"no longer matches this file's call style", rel)
			}

			for _, name := range names {
				if !registered[name] {
					t.Errorf("%s: references MCP tool %q, which is not registered by any "+
						"Register*Tools function in internal/tools", rel, name)
				}
			}
		})
	}
}

// TestSetupSkillsRegistryContainsExpectedTools pins the specific tool names
// the setup skill and its companions are documented to call, by exact
// name, so a future rename in internal/tools fails loudly here with a
// precise message instead of only in the broader scan above.
func TestSetupSkillsRegistryContainsExpectedTools(t *testing.T) {
	registered := setupSkillsBuildRegistry(t)

	expected := []string{
		"setup_prepare",
		"setup_init",
		"setup_write_sections",
		"migrate",
		"openspec_enrich",
		"validate",
		"dimensions_render_instructions",
		"scaffold_ci",
		"dashboard",
	}

	for _, name := range expected {
		if !registered[name] {
			t.Errorf("expected tool %q to be registered, but it is not", name)
		}
	}
}

// TestSetupSkillsParamsMatchSchema asserts that every key used in a
// "tool({ key: ... })" call site's top-level argument object is a real
// property on that tool's registered input schema. This is a
// schema-membership check, not a per-action required/optional check, so it
// catches a renamed or misspelled field without needing to model each
// action's own sub-schema.
func TestSetupSkillsParamsMatchSchema(t *testing.T) {
	repoRoot := setupSkillsRepoRoot(t)
	schemas := setupSkillsBuildSchemas(t)

	for _, rel := range setupSkillsCallFiles {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join(repoRoot, rel)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			sites := setupSkillsExtractCalls(string(data))
			checked := 0
			for _, site := range sites {
				props, ok := schemas[site.name]
				if !ok {
					// Already reported by TestSetupSkillsToolReferencesAreRegistered.
					continue
				}
				for _, key := range site.keys {
					checked++
					if !props[key] {
						t.Errorf("%s:%d: %s({...}) call uses param %q, which is not a "+
							"property of the %q tool's registered input schema",
							rel, site.line, site.name, key, site.name)
					}
				}
			}
			if checked == 0 {
				t.Fatalf("%s: extracted zero call-site parameter keys -- the bracket-depth "+
					"key extraction likely no longer matches this file's call style", rel)
			}
		})
	}
}

// TestSetupSkillsReferenceOnlyFilesExist is a light existence/non-empty
// check for the three companion files that legitimately contain zero MCP
// tool-call sites (see setupSkillsReferenceOnlyFiles) — they are still part
// of Task 44's nine-companion deliverable set and must exist even though
// they are out of scope for the tool-call cross-checks above.
func TestSetupSkillsReferenceOnlyFilesExist(t *testing.T) {
	repoRoot := setupSkillsRepoRoot(t)
	for _, rel := range setupSkillsReferenceOnlyFiles {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join(repoRoot, rel)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if len(strings.TrimSpace(string(data))) == 0 {
				t.Fatalf("%s: file is empty", rel)
			}
		})
	}
}

// TestSetupSkillsNoWorkspaceHooksDispatchRows is the Q1 acceptance
// criterion: the ported main SKILL.md must not still *dispatch* to
// workspace/hooks menu sections that internal/setupmeta's 14-id manifest
// has no ids for (Ruling Q1 -- issues #351/#370/#372). This deliberately
// checks only for a live table row naming those ids as a dispatch target
// (the "Legacy section reference" table's old "3g"/"3h" rows) — Port Notes
// prose that explains *why* those ids were dropped legitimately needs to
// name them, so a whole-file substring ban would incorrectly fail on that
// explanatory text.
func TestSetupSkillsNoWorkspaceHooksDispatchRows(t *testing.T) {
	repoRoot := setupSkillsRepoRoot(t)
	path := filepath.Join(repoRoot, "skills/setup/SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		for _, badRowStart := range []string{"| 3g |", "| 3h |"} {
			if strings.HasPrefix(trimmed, badRowStart) {
				t.Errorf("skills/setup/SKILL.md: Legacy section reference table still has "+
					"a live row %q (Ruling Q1 drops the workspace/hooks rows, keeping only 3a-3f)",
					badRowStart)
			}
		}
	}
}

// TestSetupSkillsOnlySkipIdsMatchManifest asserts that every canonical
// section id from internal/setupmeta.Sections() is documented as a valid
// --only value in the main SKILL.md's Arguments table, and that the two
// dropped legacy ids ("workspace", "hooks") are not listed there as valid
// values -- guarding the corrected id-list call out in Port Notes. The check
// is scoped to the Arguments-table row itself (identified by its
// "--only <ids>" flag cell) rather than the whole file, since Port Notes
// prose elsewhere must legitimately name "workspace"/"hooks" to explain why
// they were dropped. The --skip flag had no behavior and was removed, so it
// must not come back as an Arguments-table row.
func TestSetupSkillsOnlySkipIdsMatchManifest(t *testing.T) {
	repoRoot := setupSkillsRepoRoot(t)
	path := filepath.Join(repoRoot, "skills/setup/SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var idListLines []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "| `--skip") {
			t.Errorf("skills/setup/SKILL.md: Arguments table lists the removed --skip flag: %s", trimmed)
		}
		if strings.HasPrefix(trimmed, "| `--only <ids>`") {
			idListLines = append(idListLines, line)
		}
	}
	if len(idListLines) != 1 {
		t.Fatalf("expected exactly 1 Arguments-table row for --only, found %d -- "+
			"the Arguments table format may have changed", len(idListLines))
	}
	joined := strings.Join(idListLines, "\n")

	canonicalIDs := []string{
		"version", "ship", "jira", "review", "received-review", "commit",
		"pr", "github", "pr-labels", "review-dimensions", "pr-template", "plan-template",
		"plan-style", "plan-tasks", "plan-guardrails", "execution-guardrails", "openspec-block",
	}
	for _, id := range canonicalIDs {
		if !strings.Contains(joined, "`"+id+"`") {
			t.Errorf("skills/setup/SKILL.md: canonical section id %q is not listed in the "+
				"--only Arguments-table row", id)
		}
	}

	for _, dropped := range []string{"`workspace`", "`hooks`"} {
		if strings.Contains(joined, dropped) {
			t.Errorf("skills/setup/SKILL.md: --only Arguments-table row still lists "+
				"dropped id %s as a valid value (Ruling Q1)", dropped)
		}
	}
}

// setupSkillsManagedBeginRe matches the begin line of an openspec managed
// block, whatever owner name it carries. Submatch 1 is the owner name.
var setupSkillsManagedBeginRe = regexp.MustCompile(`# BEGIN MANAGED BY (\S+) \(v`)

// TestSetupSkillsOpenspecMarkerMatchesTool runs the real openspec_enrich
// tool against a fixture repo, reads the begin marker it wrote, and asserts
// that every managed-block marker the setup skill files search for names
// the same owner. A mismatch makes Step 0 miss the block, so the
// openspec-block menu row always shows "not set".
func TestSetupSkillsOpenspecMarkerMatchesTool(t *testing.T) {
	fixture := t.TempDir()
	if out, err := exec.Command("git", "-C", fixture, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	cfgPath := filepath.Join(fixture, "openspec", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("schema: spec-driven\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Resolve the skill directory before changing the working directory.
	skillFiles, err := filepath.Glob(filepath.Join(setupSkillsRepoRoot(t), "skills", "setup", "*.md"))
	if err != nil || len(skillFiles) == 0 {
		t.Fatalf("glob setup skill files: %v (found %d)", err, len(skillFiles))
	}
	t.Chdir(fixture)

	srv := mcpserver.New("skillcheck-setup-marker-test", "0.0.0-test")
	tools.RegisterOpenspecTools(srv)
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := srv.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "skillcheck-setup-marker-test", Version: "0.0.0"}, nil)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "openspec_enrich", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool openspec_enrich: %v", err)
	}
	if res.IsError {
		t.Fatalf("openspec_enrich returned a tool error: %v", res.Content)
	}
	written, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read enriched config: %v", err)
	}
	m := setupSkillsManagedBeginRe.FindStringSubmatch(string(written))
	if m == nil {
		t.Fatalf("openspec_enrich wrote no managed-block begin line:\n%s", written)
	}
	toolOwner := m[1]

	foundInSkill := false
	for _, path := range skillFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, sm := range setupSkillsManagedBeginRe.FindAllStringSubmatch(string(data), -1) {
			if filepath.Base(path) == "SKILL.md" {
				foundInSkill = true
			}
			if sm[1] != toolOwner {
				t.Errorf("%s: searches for marker owner %q, but openspec_enrich writes %q",
					filepath.Base(path), sm[1], toolOwner)
			}
		}
	}
	if !foundInSkill {
		t.Error("skills/setup/SKILL.md names no managed-block begin marker for Step 0 detection")
	}
}

// TestSetupSkillsStep3cRules asserts the Step 3c section of the setup skill
// holds each rule that keeps an unknown value, an absent field, a check error
// or a missing answer from causing a question or a write.
func TestSetupSkillsStep3cRules(t *testing.T) {
	repoRoot := setupSkillsRepoRoot(t)
	path := filepath.Join(repoRoot, "skills/setup/SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(data)
	start := strings.Index(text, "### Step 3c")
	end := strings.Index(text, "### Step 4")
	if start < 0 || end < start {
		t.Fatalf("skills/setup/SKILL.md: Step 3c section not found between %q and %q", "### Step 3c", "### Step 4")
	}
	section := text[start:end]
	for rule, want := range map[string]string{
		"unknown action gives no question and no write": "Any other `action` value counts as `current`.",
		"missing field gives no question":               "An absent `ciScriptDrift[]` counts as empty.",
		"drift check error gives no question":           "Do not print the error here: Step 4 prints it once.",
		"no question tool gives no write":               "When AskUserQuestion is not available, do not call the tool: treat the answer as Skip.",
		"unknown answer gives no write":                 "Any other answer (free text or a dismissed question)",
		"the only write call":                           "scaffold_ci({ updateOutdated: true })",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("skills/setup/SKILL.md Step 3c: missing rule (%s): %q", rule, want)
		}
	}
}

// TestSetupSkillsCIDriftErrorPrintsOnce asserts the setup skill prints the
// CI script drift error in one place only: the Step 4 summary.
func TestSetupSkillsCIDriftErrorPrintsOnce(t *testing.T) {
	repoRoot := setupSkillsRepoRoot(t)
	path := filepath.Join(repoRoot, "skills/setup/SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(data)
	const marker = "CI script check failed"
	if got := strings.Count(text, marker); got != 1 {
		t.Fatalf("skills/setup/SKILL.md: %q appears %d times, want 1 (the Step 4 summary)", marker, got)
	}
	step4 := strings.Index(text, "### Step 4")
	if step4 < 0 || strings.Index(text, marker) < step4 {
		t.Errorf("skills/setup/SKILL.md: %q must be in the Step 4 section", marker)
	}
}

// TestSetupSkillsRoutesReachStep3c asserts every route of the setup skill
// that names Step 2b -> Step 3 continues through Step 3b and Step 3c, so no
// route skips the CI script update step.
func TestSetupSkillsRoutesReachStep3c(t *testing.T) {
	repoRoot := setupSkillsRepoRoot(t)
	path := filepath.Join(repoRoot, "skills/setup/SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	flat := strings.Join(strings.Fields(string(data)), " ")
	route := regexp.MustCompile(`2b → (?:Step )?3(?: → (?:Step )?3b → (?:Step )?3c)?`)
	matches := route.FindAllString(flat, -1)
	if len(matches) == 0 {
		t.Fatal("skills/setup/SKILL.md: no route through Step 2b found")
	}
	for _, m := range matches {
		if !strings.HasSuffix(m, "3c") {
			t.Errorf("skills/setup/SKILL.md: route %q does not continue to Step 3b and Step 3c", m)
		}
	}
}

// setupSkillsSection returns the text of skills/setup/SKILL.md from the first
// occurrence of start up to the first occurrence of end after it.
func setupSkillsSection(t *testing.T, start, end string) string {
	t.Helper()
	path := filepath.Join(setupSkillsRepoRoot(t), "skills/setup/SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	section, err := setupSkillsTextSection(string(data), start, end)
	if err != nil {
		t.Fatalf("skills/setup/SKILL.md: %v", err)
	}
	return section
}

// setupSkillsTextSection returns text from the first occurrence of start up to the first
// occurrence of end after it, or an error that names the missing marker.
func setupSkillsTextSection(text, start, end string) (string, error) {
	from := strings.Index(text, start)
	if from < 0 {
		return "", fmt.Errorf("%q not found", start)
	}
	to := strings.Index(text[from+len(start):], end)
	if to < 0 {
		return "", fmt.Errorf("%q not found after %q", end, start)
	}
	return text[from : from+len(start)+to], nil
}

// TestSetupSkillsTextSection pins the section cut that the Step tests below use: the
// bounds, the first match of each marker and both failure branches.
func TestSetupSkillsTextSection(t *testing.T) {
	text := "intro\n### A\nbody a\n### B\nbody b\n### A\nlater\n"
	for _, tc := range []struct {
		name, start, end, want, wantErr string
	}{
		{"cut", "### A", "### B", "### A\nbody a\n", ""},
		{"end only after start", "### B", "### A", "### B\nbody b\n", ""},
		{"no start", "### C", "### B", "", `"### C" not found`},
		{"no end after start", "### B", "### B", "", `"### B" not found after "### B"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := setupSkillsTextSection(text, tc.start, tc.end)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if got != tc.want {
				t.Errorf("section = %q, want %q", got, tc.want)
			}
		})
	}
}

// setupSkillsAssertInOrder checks that each part appears in text, after the part before
// it. label names the text in a failure message.
func setupSkillsAssertInOrder(t *testing.T, label, text string, parts []string) {
	t.Helper()
	pos := 0
	for _, part := range parts {
		i := strings.Index(text[pos:], part)
		if i < 0 {
			if strings.Contains(text, part) {
				t.Errorf("%s: %q is out of order (it must come after the part before it)", label, part)
			} else {
				t.Errorf("%s: missing %q", label, part)
			}
			continue
		}
		pos += i + len(part)
	}
}

// TestSetupSkillsStep0SetupTabHandoff asserts the Step 0 section of the
// setup skill holds, in order, the item 3 Setup tab question with its 4
// conditions, the answer rows and the route to item 4, before the item 4
// config snapshot. Step 2 also names needsMigration and --migrate, so the
// check reads only the Step 0 body.
func TestSetupSkillsStep0SetupTabHandoff(t *testing.T) {
	section := setupSkillsSection(t, "### Step 0", "### Step 1")
	setupSkillsAssertInOrder(t, "skills/setup/SKILL.md Step 0", section, []string{
		"`formIds` — the ids of the `sections[]` rows",
		"3. **Form sections: dialogue or Setup tab.**",
		"all 4 conditions",
		"| no `--only`, `--force` or direct-entry flag | Arguments |",
		"| `mainWorktree` is true | item 2 |",
		"| `needsMigration` is false and `--migrate` is not passed | item 1, Arguments |",
		"| `formIds` is not empty (`<F>` is 1 or more) | item 2 |",
		"Where do you want to change the form sections?",
		"Options: `Setup tab (Recommended)`, `Dialogue`.",
		"| Setup tab, call returns, no `previewUrl` | `dashboard({ action: \"ensure\", open: true, tab: \"setup\" })` | set `formsDeferred` to true",
		"| Setup tab, call returns `previewUrl` | same, then `mcp__t3-code__preview_open({ url: \"<previewUrl>\" })`",
		"Warning: T3 preview tab did not open.",
		"`Tool error text (preview_open):`",
		"| Setup tab, call error | same | none |",
		"`Tool error text (dashboard):`",
		"The form sections continue here.",
		"| Dialogue, free text, dismissed | none | none |",
		"| the question tool is not available | none | none |",
		"`formsDeferred` is false on every other row",
		"Go to item 4.",
		"**Tool error text rule.**",
		"Cut the error text to its first 2000 characters.",
		"`(cut to 2000 characters)`",
		"4. **Snapshot current config**",
	})
	for _, gone := range []string{"This run created a config file.", "selected form sections"} {
		if strings.Contains(section, gone) {
			t.Errorf("skills/setup/SKILL.md Step 0: holds %q, the question runs on every plain run", gone)
		}
	}
}

// TestSetupSkillsStep1DeferredMenu asserts the Step 1 section of the setup
// skill lists only the rows that need the assistant after a Setup tab
// answer, that all and not-set select from the listed rows, and that the
// menu goes to Step 2 with no empty-list route.
func TestSetupSkillsStep1DeferredMenu(t *testing.T) {
	section := setupSkillsSection(t, "### Step 1", "### Step 2")
	setupSkillsAssertInOrder(t, "skills/setup/SKILL.md Step 1", section, []string{
		"**Listed rows** (read before Phase 1).",
		"When `formsDeferred` is true (Step 0 item 3), the status block, the menu and Phase 4 use only",
		"the rows whose id is not in `formIds`. N counts these rows from 1.",
		"this filter never gives an empty list.",
		"**Phase 1 — Render the status block.** Print the status block for the listed rows",
		"**Phase 2 — Print the numbered menu directly to chat.** One line per listed row",
		"all       — configure every listed section",
		"not-set   — configure only listed sections currently [not set]",
		"- `all` → every id of the listed rows.",
		"- `not-set` → the ids of the listed rows whose computed `state === 'not-set'`.",
		"Store the resolved section ids as `selectedIds`. Go to Step 2.",
	})
	for _, gone := range []string{"No sections left for this run.", "every id from the 19-id canonical list"} {
		if strings.Contains(section, gone) {
			t.Errorf("skills/setup/SKILL.md Step 1: holds %q", gone)
		}
	}
}

// TestSetupSkillsStep4SetupTabPointer asserts the Step 4 section of the setup
// skill holds, in order, the Deferred summary block, the pointer after the
// Step 0 item 3 Setup tab answer and the dashboard status pointer rows.
func TestSetupSkillsStep4SetupTabPointer(t *testing.T) {
	section := setupSkillsSection(t, "### Step 4", "## Idempotency")
	setupSkillsAssertInOrder(t, "skills/setup/SKILL.md Step 4", section, []string{
		"Deferred:",
		"deferred to the dashboard Setup tab; this run did not save them",
		"Show the `Deferred` block only when `formsDeferred` is true (Step 0 item 3).",
		"Print the Setup tab pointer last.",
		"When `formsDeferred` is true, call no tool.",
		"`Save the form sections in the dashboard Setup tab: <url>#setup`, with the `<url>` that Step 0 item 3",
		"When Step 0 item 3 kept a `previewUrl`",
		"Otherwise call `dashboard({ action: \"status\" })`",
		"| `running` true | `Change the form sections later in the dashboard Setup tab: <url>#setup` |",
		"| `running` false |",
		"/sdlc:dashboard",
		"| tool error | the error by the tool error text rule of Step 0 item 3, with the label `Tool error text (dashboard):`",
		"| any result, `mainWorktree` false |",
	})
}

// TestSetupSkillsStep0ValueRules asserts the Step 0 section of the setup skill
// defines mainWorktree and formIds, with the false or empty result for a
// missing value or a failed call.
func TestSetupSkillsStep0ValueRules(t *testing.T) {
	section := setupSkillsSection(t, "### Step 0", "### Step 1")
	for _, want := range []string{
		"--path-format=absolute",
		"exactly 2 lines",
		"`mainWorktree` is false",
		"`formIds` — the ids of the `sections[]` rows",
		"the `delegatedTo` key is absent or empty",
		"When no row matches, `formIds` is empty.",
		"`<FIELDS>` is the sum of the lengths of their `fields[]` arrays",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("skills/setup/SKILL.md Step 0: missing %q", want)
		}
	}
	if got := strings.Count(section, "else false"); got != 1 {
		t.Errorf("skills/setup/SKILL.md Step 0: %q appears %d times, want 1 (mainWorktree)", "else false", got)
	}
}

// TestSetupSkillsFormIdsMatchSetupTab checks the two facts that Step 0 item 2
// and Step 1 of the setup skill state about formIds: the dashboard Setup tab
// builds its form from the sections with no delegatedTo (the same rule as
// formIds), and setupmeta.Sections() holds at least one section of each
// kind, so the Setup tab question shows a form count above 0 and the
// filtered Step 1 menu is never empty.
func TestSetupSkillsFormIdsMatchSetupTab(t *testing.T) {
	form, delegated := 0, 0
	for _, sec := range setupmeta.Sections() {
		if sec.DelegatedTo == "" {
			form++
		} else {
			delegated++
		}
	}
	if form == 0 {
		t.Error("setupmeta.Sections(): no section without delegatedTo, so the Setup tab question would show 0 form sections")
	}
	if delegated == 0 {
		t.Error("setupmeta.Sections(): no section with delegatedTo, so the filtered Step 1 menu would be empty")
	}

	path := filepath.Join(filepath.Dir(filepath.Dir(setupSkillsRepoRoot(t))), "internal", "dashboard", "web", "static", "setup.js")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	want := "var formSections = data.sections.filter(function (sec) {\n    return !sec.delegatedTo;\n  });"
	if !strings.Contains(string(data), want) {
		t.Errorf("internal/dashboard/web/static/setup.js: the Setup tab form does not use the formIds rule (a section with no delegatedTo); want %q", want)
	}
}
