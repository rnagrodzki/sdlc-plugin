// Package skillcheck cross-checks the MCP tool names and call-site
// parameters referenced by the ported plan-family skill files (plan,
// execute -- Task 45) against the tool names and input schemas
// actually registered on the Go MCP server, so skill prose cannot silently
// drift from the real tool surface.
//
// This file intentionally contains no production logic — per this task's
// ruling, no shared skillcheck.go production file exists, and this file
// builds its own in-process MCP registry independently rather than sharing
// one with the sibling *_test.go files in this package. Every unexported
// identifier below is prefixed planSkills, and every declared Test function
// is prefixed TestPlanSkills, to avoid name collisions with those files.
package skillcheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// planSkillsFiles lists the Task 45 deliverable skill files, relative to the
// repository root, that this test cross-checks against the registry.
var planSkillsFiles = []string{
	"skills/plan/SKILL.md",
	"skills/execute/SKILL.md",
}

// planSkillsCallRe matches this repo's documented tool-call pseudocode
// convention: an identifier immediately followed by "({", e.g.
// "execute_state({" or "plan_mark({".
var planSkillsCallRe = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\(\{`)

// planSkillsBuiltinTools are Claude Code's own built-in tools. Skill files
// legitimately call these in pseudocode (e.g. "Read({ ... })"); they are
// never registered on our MCP server and must be excluded from the
// cross-check.
var planSkillsBuiltinTools = map[string]bool{
	"Read": true, "Write": true, "Edit": true, "Bash": true,
	"Grep": true, "Glob": true, "AskUserQuestion": true,
	"Skill": true, "Agent": true, "WebFetch": true, "WebSearch": true,
}

// planSkillsRepoRoot resolves the repository root from this test file's own
// path (internal/skillcheck/skillcheck_plan_test.go), independent of the
// directory `go test` happens to be invoked from.
func planSkillsRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("planSkillsRepoRoot: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(file))), "plugins", "sdlc")
}

// planSkillsListTools constructs a fresh mcpserver.Server, registers every
// tool family in internal/tools (every Register*Tools function, not just
// the ones the two Task 45 skills happen to call), starts an in-process MCP
// client against it, and returns the tools reported by a real ListTools()
// call.
func planSkillsListTools(t *testing.T) []mcp.Tool {
	t.Helper()

	srv := mcpserver.New("skillcheck-plan-test", "0.0.0-test")

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
	tools.RegisterPlanSupportTools(srv)

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
	initReq.Params.ClientInfo = mcp.Implementation{Name: "skillcheck-plan-test", Version: "0.0.0"}
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

// planSkillsBuildRegistry returns the set of registered tool names, for the
// tool-existence cross-check (AC: every referenced tool is real).
func planSkillsBuildRegistry(t *testing.T) map[string]bool {
	t.Helper()
	toolList := planSkillsListTools(t)
	names := make(map[string]bool, len(toolList))
	for _, tl := range toolList {
		names[tl.Name] = true
	}
	return names
}

// planSkillsBuildSchemas returns, for each registered tool, the set of its
// input schema's top-level property names — the AC2 cross-check ("params
// match schema") is a membership test against this map, catching a
// call-site key that no longer exists on the tool's real Go input struct
// (e.g. a field renamed in internal/tools without the skill prose being
// updated to match).
func planSkillsBuildSchemas(t *testing.T) map[string]map[string]bool {
	t.Helper()
	toolList := planSkillsListTools(t)
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

// planSkillsCallSite is one "name({ ... })" tool-call reference found in a
// skill file, with its object-literal argument's top-level keys.
type planSkillsCallSite struct {
	name string
	keys []string
	line int
}

// planSkillsExtractArgKeys walks the object-literal argument of a
// "name({ ... })" call starting at the index of its opening "{" and returns
// the top-level (depth-1) keys: identifiers immediately followed by ":"
// (skipping whitespace), found while still inside the call's own outermost
// brace. String-literal contents are skipped whole (so a placeholder like
// "{runId}" inside a quoted value is never mistaken for a nested object),
// and any nested {}/()  — or a genuine nested array/object value inside
// "[...]" — pushes depth past 1, so keys inside are correctly excluded from
// the top-level result rather than misattributed.
//
// One "[" shape is special-cased: this repo's own optional-parameter
// notation "[, key: value]" (an opening "[" immediately followed, modulo
// whitespace, by a ",") documents an optional call argument, not a nested
// array value. Depth-1 occurrences of it are treated as transparent — the
// bracket is skipped without changing depth — so "key" stays a checkable
// top-level key instead of being hidden one level deep. A bracket stack
// records which "[" openings were treated this way, so each "]" undoes the
// right thing regardless of nesting.
func planSkillsExtractArgKeys(content string, openBrace int) []string {
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
			for j < n && isPlanSkillsSpace(content[j]) {
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
		case depth == 1 && isPlanSkillsIdentStart(c):
			start := i
			for i < n && isPlanSkillsIdentPart(content[i]) {
				i++
			}
			ident := content[start:i]
			j := i
			for j < n && isPlanSkillsSpace(content[j]) {
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

func isPlanSkillsIdentStart(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isPlanSkillsIdentPart(c byte) bool {
	return isPlanSkillsIdentStart(c) || (c >= '0' && c <= '9')
}

func isPlanSkillsSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// planSkillsExtractCalls extracts every call-shaped tool reference from a
// skill file's content, excluding Claude Code's own built-in tools and any
// external (non-registry) MCP namespace such as "mcp__atlassian__...", along
// with each call's top-level argument keys and source line number.
func planSkillsExtractCalls(content string) []planSkillsCallSite {
	var sites []planSkillsCallSite
	for _, m := range planSkillsCallRe.FindAllStringSubmatchIndex(content, -1) {
		name := content[m[2]:m[3]]
		if planSkillsBuiltinTools[name] || strings.HasPrefix(name, "mcp__") {
			continue
		}
		openBrace := m[1] - 1 // the match ends right after the call's opening "{"
		keys := planSkillsExtractArgKeys(content, openBrace)
		line := 1 + strings.Count(content[:m[0]], "\n")
		sites = append(sites, planSkillsCallSite{name: name, keys: keys, line: line})
	}
	return sites
}

// planSkillsToolNames returns the deduplicated set of tool names referenced
// across a slice of call sites.
func planSkillsToolNames(sites []planSkillsCallSite) []string {
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

// TestPlanSkillsToolReferencesAreRegistered asserts that every MCP tool name
// called out in the two Task 45 skill files (plan, execute)
// is actually registered on the Go MCP server — guarding against skill
// prose drifting from the real tool surface.
func TestPlanSkillsToolReferencesAreRegistered(t *testing.T) {
	repoRoot := planSkillsRepoRoot(t)
	registered := planSkillsBuildRegistry(t)

	for _, rel := range planSkillsFiles {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join(repoRoot, rel)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			sites := planSkillsExtractCalls(string(data))
			names := planSkillsToolNames(sites)
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

// TestPlanSkillsRegistryContainsExpectedTools pins the specific tool names
// the two Task 45 skills are documented to call, by exact name, so a future
// rename in internal/tools fails loudly here with a precise message instead
// of only in the broader scan above.
func TestPlanSkillsRegistryContainsExpectedTools(t *testing.T) {
	registered := planSkillsBuildRegistry(t)

	expected := []string{
		"plan_prepare",
		"plan_mark",
		"execute_state",
		"validate",
		"links_validate",
	}

	for _, name := range expected {
		if !registered[name] {
			t.Errorf("expected tool %q to be registered, but it is not", name)
		}
	}
}

// TestPlanSkillsParamsMatchSchema is the AC2 acceptance criterion: every
// key used in a "tool({ key: ... })" call site's top-level argument object
// must be a real property on that tool's registered input schema. This is
// a schema-membership check, not a per-action required/optional check (the
// registered schema is one flat struct covering every "action" value a
// multi-action tool like execute_state accepts), so it catches a renamed or
// misspelled field (e.g. "name" where the real field is "taskName") without
// needing to model each action's own sub-schema.
func TestPlanSkillsParamsMatchSchema(t *testing.T) {
	repoRoot := planSkillsRepoRoot(t)
	schemas := planSkillsBuildSchemas(t)

	for _, rel := range planSkillsFiles {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join(repoRoot, rel)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			sites := planSkillsExtractCalls(string(data))
			checked := 0
			for _, site := range sites {
				props, ok := schemas[site.name]
				if !ok {
					// Already reported by TestPlanSkillsToolReferencesAreRegistered.
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

// TestPlanSkillsNoOrchestratorReferences is the AC3 acceptance criterion:
// neither SKILL.md may reference the retired plan-explore-orchestrator /
// wave-runner agents by name in their own prose (Ruling Set 15 replaced
// both with flat background-agent dispatch and a file-based ledger). This
// check is deliberately scoped to the two SKILL.md files only — the lane,
// lens, and prompt reference files (see TestPlanSkillsReferenceFilesAreUnmodified)
// are required to stay byte-identical to the source plugin and some of them
// legitimately still say "orchestrator" or "none — orchestrator skipped" in
// their own untouched prose (Ruling Set 2: AC4 wins over AC3 there).
func TestPlanSkillsNoOrchestratorReferences(t *testing.T) {
	repoRoot := planSkillsRepoRoot(t)
	for _, rel := range planSkillsFiles {
		path := filepath.Join(repoRoot, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), "plan-explore-orchestrator") {
			t.Errorf("%s: still references plan-explore-orchestrator", rel)
		}
	}
}

// planSkillsReferenceFileHashes pins the SHA-256 hash of each lane/lens/
// reviewer/reference file that Ruling Set 3 requires to be ported as a
// verbatim, diff-empty copy of the source plugin file. These hashes were
// computed directly from this repo's own files at the moment byte-identity
// against /Users/rafal/repositories/sdlc-marketplace's source plugin was
// confirmed via `diff -q` (see Task 45's report); the test does not diff
// against that sibling repo at run time (it is an external checkout, not a
// module dependency, and is not guaranteed to exist wherever `go test` runs,
// e.g. CI) — it only guards against accidental local drift after this
// point. A deliberate future change to any of these files (e.g. a real
// content fix) must update its pinned hash here in the same commit.
var planSkillsReferenceFileHashes = map[string]string{
	"skills/plan/g17-dimension-coverage-prompt.md":    "f35a90fbc594eb27a02e1f9d7985a8ade0397f550a18f9698c68ed5a87a1a62c",
	"skills/plan/intake-verify-prompt.md":             "7b3c89f79be57cdd7ae237f2524356afe7567d1faa22f79fe0bcdf62bb4229c7",
	"skills/plan/lane-content-coverage-prompt.md":     "4b518a9f13a448bb481cbc87b427401aadf89999cd7ee04f0fdf3ed9b707e205",
	"skills/plan/lane-file-existence-prompt.md":       "ee76f039f92f757a3fc8a85122e3e217f73a4a63c41f2164ca688acf4ba68f35",
	"skills/plan/lane-guardrail-compliance-prompt.md": "34baba6c0432003966f780084d1f7a2d5654cc8cfd859602dc23ac2f1699f1db",
	"skills/plan/lane-static-structural-prompt.md":    "8216ae0f03d064ef9ddb2b37d5a6a4b2943f36d7b0cd54775d3fcf33f4a64920",
	"skills/plan/lens-architecture-prompt.md":         "0dc7e10fda43b576d7ddb223f5d46c0a37f0b03b14e8950c9207a147aed03508",
	"skills/plan/lens-requirements-prompt.md":         "f11079debb98beea1810ee281873f3c8a6024dd15006c5b63ed3933f900e582c",
	"skills/plan/lens-risk-prompt.md":                 "c07da38779172712157f8e06dfd8a3f59812a5eb2f4c0c370846b2aefc72c2e2",
	"skills/plan/plan-reviewer-prompt.md":             "b9e888dcf06960d9d9dc2c6039b01828d8c8e4ad5530cbf4d2d4872404d79e75",
	"skills/plan/plan-format-reference.md":            "83935c6c2086a1c2717e377ed6d1d4ced333e8461123c8c3dc0dc2cdab39bcbf",
	"skills/plan/plan-template-default.md":            "1fd3de86545ece6eb08e8736ef7dcc2c57ee534f8753a3c13ea2c60398be863b",
	"skills/execute/spec-compliance-reviewer.md":      "ff6b385e1fade868e4c06fa4c2c5990a1e087e4894b1d4395f46c50c2342023b",
}

// TestPlanSkillsReferenceFilesAreUnmodified is the AC4 acceptance criterion:
// the lane, lens, reviewer, and format/template reference files ported
// verbatim from the source plugin (Ruling Set 3) must remain byte-identical
// to what was ported. See planSkillsReferenceFileHashes for why this is a
// pinned-hash check rather than a live diff against the sibling source repo.
func TestPlanSkillsReferenceFilesAreUnmodified(t *testing.T) {
	repoRoot := planSkillsRepoRoot(t)
	if len(planSkillsReferenceFileHashes) == 0 {
		t.Fatal("planSkillsReferenceFileHashes is empty")
	}
	for rel, wantHash := range planSkillsReferenceFileHashes {
		rel, wantHash := rel, wantHash
		t.Run(rel, func(t *testing.T) {
			path := filepath.Join(repoRoot, rel)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			sum := sha256.Sum256(data)
			gotHash := hex.EncodeToString(sum[:])
			if gotHash != wantHash {
				t.Errorf("%s: sha256 %s does not match pinned %s -- this file must stay a "+
					"byte-identical copy of the source plugin file; if this is a deliberate "+
					"content change, update the pinned hash in planSkillsReferenceFileHashes",
					rel, gotHash, wantHash)
			}
		})
	}
}
