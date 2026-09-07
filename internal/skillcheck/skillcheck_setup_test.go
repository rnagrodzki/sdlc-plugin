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

// setupSkillsCallFiles lists the Task 44 setup files, relative to the
// repository root, that actually contain "name({ ... })" MCP tool-call
// pseudocode and are therefore cross-checked against the registry. Three of
// the nine companion files (setupSkillsReferenceOnlyFiles below) are pure
// reference/copy sub-flows with no tool calls at all and are checked
// separately for mere existence.
var setupSkillsCallFiles = []string{
	"skills/setup/SKILL.md",
	"skills/setup/setup-dimensions.md",
	"skills/setup/setup-pr-template.md",
	"skills/setup/setup-pr-labels.md",
	"skills/setup/setup-guardrails.md",
	"skills/setup/setup-execution-guardrails.md",
	"skills/setup/setup-openspec.md",
}

// setupSkillsReferenceOnlyFiles are companion files that legitimately
// contain zero "name({ ... })" MCP tool-call sites: dimension-catalog.md and
// scan-patterns.md are static reference tables consumed by prose elsewhere,
// and setup-plan-template.md scaffolds its output via a plain `cp` shell
// command rather than any MCP tool. They are asserted to exist and be
// non-empty, not scanned for tool calls.
var setupSkillsReferenceOnlyFiles = []string{
	"skills/setup/dimension-catalog.md",
	"skills/setup/scan-patterns.md",
	"skills/setup/setup-plan-template.md",
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
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// setupSkillsListTools constructs a fresh mcpserver.Server, registers every
// tool family in internal/tools (every Register*Tools function, not just
// the ones the Task 44 files happen to call), starts an in-process MCP
// client against it, and returns the tools reported by a real ListTools()
// call.
func setupSkillsListTools(t *testing.T) []mcp.Tool {
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
	tools.RegisterVersionTools(srv)
	tools.RegisterValidateTools(srv)
	tools.RegisterDimensionsRenderTools(srv)

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
	initReq.Params.ClientInfo = mcp.Implementation{Name: "skillcheck-setup-test", Version: "0.0.0"}
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
		props := make(map[string]bool, len(tl.InputSchema.Properties))
		for k := range tl.InputSchema.Properties {
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

// TestSetupSkillsOnlySkipIdsMatchManifest asserts that every one of the 14
// canonical section ids from internal/setupmeta.Sections() is documented as
// a valid --only/--skip value in the main SKILL.md's Arguments table, and
// that the two dropped legacy ids ("workspace", "hooks") are not listed
// there as valid values -- guarding the corrected id-list call out in Port
// Notes. The check is scoped to the two Arguments-table rows themselves
// (identified by their "--skip <section>" / "--only <ids>" flag cell)
// rather than the whole file, since Port Notes prose elsewhere must
// legitimately name "workspace"/"hooks" to explain why they were dropped.
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
		if strings.HasPrefix(trimmed, "| `--skip <section>`") || strings.HasPrefix(trimmed, "| `--only <ids>`") {
			idListLines = append(idListLines, line)
		}
	}
	if len(idListLines) != 2 {
		t.Fatalf("expected exactly 2 Arguments-table rows for --skip/--only, found %d -- "+
			"the Arguments table format may have changed", len(idListLines))
	}
	joined := strings.Join(idListLines, "\n")

	canonicalIDs := []string{
		"version", "ship", "jira", "review", "received-review", "commit",
		"pr", "pr-labels", "review-dimensions", "pr-template", "plan-template",
		"plan-guardrails", "execution-guardrails", "openspec-block",
	}
	for _, id := range canonicalIDs {
		if !strings.Contains(joined, "`"+id+"`") {
			t.Errorf("skills/setup/SKILL.md: canonical section id %q is not listed in the "+
				"--skip/--only Arguments-table rows", id)
		}
	}

	for _, dropped := range []string{"`workspace`", "`hooks`"} {
		if strings.Contains(joined, dropped) {
			t.Errorf("skills/setup/SKILL.md: --skip/--only Arguments-table rows still list "+
				"dropped id %s as a valid value (Ruling Q1)", dropped)
		}
	}
}
