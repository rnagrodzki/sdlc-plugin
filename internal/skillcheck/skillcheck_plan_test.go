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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// planSkillsFiles lists the skill files (the Task 45 deliverables and the
// preplan skill), relative to the repository root, that this test
// cross-checks against the registry.
var planSkillsFiles = []string{
	"skills/plan/SKILL.md",
	"skills/execute/SKILL.md",
	"skills/preplan/SKILL.md",
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
	"SendMessage": true,
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
func planSkillsListTools(t *testing.T) []*mcp.Tool {
	t.Helper()

	srv := mcpserver.New("skillcheck-plan-test", "0.0.0-test")

	tools.RegisterCommitTools(srv)
	tools.RegisterPrepareOrchestratorTools(srv)
	tools.RegisterLinksTools(srv)
	tools.RegisterMCPFailureTools(srv)
	tools.RegisterExecuteStateTools(srv)
	tools.RegisterPlanExploreTools(srv)
	tools.RegisterJiraTools(srv)
	tools.RegisterLearningsTools(srv)
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
	tools.RegisterPlanSupportTools(srv)

	mcpSrv := srv.MCPServer()

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := mcpSrv.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "skillcheck-plan-test", Version: "0.0.0"}, nil)
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

// TestPlanSkillActionsAreRegistered checks that every name({ ... }) tool call
// in skills/plan/SKILL.md names a registered MCP tool and, when it sets an
// action, an action in that tool's registered enum. Step 3 calls validate
// with the plan_gates action, so the test also requires that call: a
// misspelled or dropped action name then fails here, not in a plan run. It
// reads SKILL.md only: plan-format-reference.md holds example code such as
// signToken(...) that is not an MCP call, so flagParityAssertToolsResolve
// (which reads every skills/plan/*.md file) does not fit this skill.
func TestPlanSkillActionsAreRegistered(t *testing.T) {
	reg := flagParityBuildRegistry(t)
	rel := "skills/plan/SKILL.md"
	data, err := os.ReadFile(filepath.Join(planSkillsRepoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}

	sawPlanGates := false
	for _, c := range flagParityToolCalls(string(data)) {
		if c.name == "validate" && c.action == "plan_gates" {
			sawPlanGates = true
		}
		enum := reg.actions[c.name]
		switch {
		case !reg.tools[c.name]:
			t.Errorf("%s:%d: references MCP tool %q, which is not registered by any Register*Tools function", rel, c.line, c.name)
		case c.action == "":
		case enum == nil:
			t.Errorf("%s:%d: %s call sets action %q, but the registered %s tool has no action enum", rel, c.line, c.name, c.action, c.name)
		case !enum[c.action]:
			t.Errorf("%s:%d: %s call references action %q, which is not in the registered %s action enum", rel, c.line, c.name, c.action, c.name)
		}
	}
	if !sawPlanGates {
		t.Errorf("%s: no validate call with action %q found — the skill dropped it, or the call-detection regex no longer matches its call style", rel, "plan_gates")
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
	"skills/plan/g17-dimension-coverage-prompt.md":    "5e5ce6093275c3b5e82c943fa3f6ed43c074cfababb3ebde287b8db3da599872",
	"skills/plan/intake-verify-prompt.md":             "eeae004f3a04a9382988fbf160b9e15d038300c50abb9c60ff2d168b6906b724",
	"skills/plan/lane-content-coverage-prompt.md":     "4edd956548e0d3b10b9da4c449b012af2ef8d79f5f25f3c51e7f1444bd36514b",
	"skills/plan/lane-guardrail-compliance-prompt.md": "40e29d72be6c960fd14f1ec0866a9166836cdce33b2a1298507fc2d97b8b2f15",
	"skills/plan/lane-static-structural-prompt.md":    "79ec35f1054923d4f272c165ce46f8de8a1642c8101fdec60722a126fd66399b",
	"skills/plan/lane-style-compliance-prompt.md":     "8e176210995de9741a3ab519f939089b921564952fcfb4071193f80c06be20d4",
	"skills/plan/lens-architecture-prompt.md":         "8acff25a20493ae7cb2b32f16751f45edc91e2d13cd6cd5dc4545007875b2df7",
	"skills/plan/lens-requirements-prompt.md":         "1cca1107f1bed8ad92753e305114225d71505750f114e4cdd4f0e12b2f083236",
	"skills/plan/lens-risk-prompt.md":                 "6e9bfc5711d0d14e7bba2293040f0184bc88911ce3c2cb5050ae1590648020fc",
	"skills/plan/plan-reviewer-prompt.md":             "9e9b0e938c57db33def969f19d818ac978fd397956fb7d5d5aaa39b8141ff3fe",
	"skills/plan/plan-format-reference.md":            "a5a8c6aa54c71dadc13c84b96030029246a3d91b905f558968741fddd7aa75d0",
	"skills/plan/plan-template-default.md":            "45af486fb7e93c36ba47ccd9a5ef832e96b294ff804b36144dad0d6f892a3e7a",
	"skills/execute/spec-compliance-reviewer.md":      "b163b798f92a9247eda48763c08c8f4ec43872fae31201427f204ebb4c99af1e",
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

// TestPlanSkillPreplanTopicMap checks that the Preplan topic file block of
// plan SKILL.md Step 1 names each new topic section and each new row kind.
// The block is the only reader of the section bodies of a topic file, so a
// section or row kind that preplan writes and the block does not name has no
// reader. The block also has to route an empty or absent new section to the
// Structured discovery question and to the --auto suppression rule, and to
// say that the Smallest cut: line is not a requirement. The routing claims
// are checked inside the one bullet that makes them, so a token that also
// occurs in another bullet cannot satisfy them. TestPlanSkillPreplanSectionParity
// (internal/tools) checks every name of the section list against the block,
// so a new section that this test does not name still fails there.
func TestPlanSkillPreplanTopicMap(t *testing.T) {
	rel := "skills/plan/SKILL.md"
	data, err := os.ReadFile(filepath.Join(planSkillsRepoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	content := string(data)

	const start = "**Preplan topic file:**"
	from := strings.Index(content, start)
	if from < 0 {
		t.Fatalf("%s: no %q block found", rel, start)
	}
	block := content[from:]
	// The block ends at the first blank line after its heading line.
	if end := strings.Index(block, "\n\n"); end >= 0 {
		block = block[:end]
	}

	for _, want := range []string{
		"`## Problem and benefit`",
		"`## Risk if wrong`",
		"`## Non-goals`",
		"`Smallest cut:`",
		"`Offered, not picked:`",
		"`None stated.`",
		// These two phrases occur only in the new bullets, so deleting the
		// non-goals bullet or the routing bullet fails the test. The tokens
		// below them also occur in older bullets.
		"and the `Smallest cut:` line under the Decisions table",
		"The `Smallest cut:` line under the table is not a row and not a requirement.",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("%s: Preplan topic file block does not name %s", rel, want)
		}
	}

	// bullet returns the top-level bullet of the block that starts with
	// start, up to the next top-level bullet. Its nested lines belong to it.
	bullet := func(start string) string {
		t.Helper()
		from := strings.Index(block, "\n- "+start)
		if from < 0 {
			t.Fatalf("%s: Preplan topic file block has no bullet that starts with %q", rel, start)
			return ""
		}
		b := block[from+1:]
		if end := strings.Index(b[2:], "\n- "); end >= 0 {
			b = b[:end+2]
		}
		return b
	}
	for start, wants := range map[string][]string{
		"An empty or absent new section": {
			"`## Problem and benefit`, `## Risk if wrong` or `## Non-goals`",
			"ask one **Structured discovery** question for each such section",
			"Under `--auto`, ask none of them: the **`--auto` suppression** rule applies",
			"`asked=no`",
			"A filled new section answers its question: do not ask it again.",
		},
		"`## Goal`, `## Users and effect`, `## Flows`": {
			"When one of them is empty or absent",
			"use **Structured discovery** below for it, and under `--auto` its **`--auto` suppression** rule",
		},
		"`## Non-goals` and the `Smallest cut:` line": {
			"not its `#<id>`",
			"With no `Smallest cut:` line",
			"the smallest cut is all live proposals",
		},
	} {
		b := bullet(start)
		for _, want := range wants {
			if !strings.Contains(b, want) {
				t.Errorf("%s: the bullet %q does not say %q:\n%s", rel, start, want, b)
			}
		}
	}
}

// planSkillsIsReuseCall reports whether site is a plan_prepare call that
// reuses an existing run: it has the top-level key resume or resolveTemplate.
// The first plan_prepare call has neither key: it creates the run.
func planSkillsIsReuseCall(site planSkillsCallSite) bool {
	return site.name == "plan_prepare" &&
		(slices.Contains(site.keys, "resume") || slices.Contains(site.keys, "resolveTemplate"))
}

// planSkillsRunIDProblems returns one message for each call in content that
// must pass runId and does not: every plan_mark call, and every plan_prepare
// call that planSkillsIsReuseCall accepts. The first plan_prepare call creates
// the run, so it has no run ID to pass. Only top-level keys count
// (planSkillsExtractArgKeys), so a runId inside data does not satisfy the
// check.
func planSkillsRunIDProblems(content string) []string {
	var problems []string
	for _, site := range planSkillsExtractCalls(content) {
		switch {
		case slices.Contains(site.keys, "runId"):
		case site.name == "plan_mark":
			problems = append(problems, fmt.Sprintf("plan_mark call at line %d has no runId", site.line))
		case planSkillsIsReuseCall(site):
			problems = append(problems, fmt.Sprintf("plan_prepare reuse call at line %d has no runId", site.line))
		}
	}
	return problems
}

// TestPlanSkillCallsPassRunID checks that every plan_mark call and every
// plan_prepare call with resume or resolveTemplate in plan/SKILL.md passes
// runId, so a second plan session on the same branch cannot receive this
// session's writes. Prose mentions count as calls. The match is on the
// literal "plan_mark({" and "plan_prepare({", with or without a space after
// the brace. planSkillsExtractArgKeys gives the top-level keys of each call.
func TestPlanSkillCallsPassRunID(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{"plan_mark with runId", `plan_mark({ marker: "done", runId: "<runId>" })`, nil},
		{"plan_mark without runId", `plan_mark({ marker: "done" })`, []string{"plan_mark call at line 1 has no runId"}},
		{"plan_mark without runId and no space after the brace", `plan_mark({marker: "done"})`, []string{"plan_mark call at line 1 has no runId"}},
		{"plan_prepare resume with runId", `plan_prepare({ resume: true, runId: "<runId>" })`, nil},
		{"plan_prepare resolveTemplate without runId", `plan_prepare({ ..., resolveTemplate: true, ... })`, []string{"plan_prepare reuse call at line 1 has no runId"}},
		{"plan_prepare resume without runId", `plan_prepare({ resume: true, resolveTemplate: true, skipConfigCheck: true })`, []string{"plan_prepare reuse call at line 1 has no runId"}},
		{"first plan_prepare call", `plan_prepare({ skipConfigCheck: true, userPrompt: USER_PROMPT })`, nil},
		{"runId only inside data", `plan_mark({ marker: "checkpoint", data: { runId: "x" } })`, []string{"plan_mark call at line 1 has no runId"}},
		{"nested braces", `plan_mark({ marker: "checkpoint", runId: "<runId>", data: { step: "1", expectedWriters: ["a"] } })`, nil},
		{"last line with no newline", "text\nplan_mark({ marker: \"done\", runId: \"<runId>\" })", nil},
		{"line number of a later call", "text\nplan_mark({ marker: \"done\" })", []string{"plan_mark call at line 2 has no runId"}},
		{"shorthand runId key", `plan_mark({ marker: "done", runId })`, []string{"plan_mark call at line 1 has no runId"}},
		{"no call", "plan_mark and plan_prepare in prose, with no call", nil},
		{"closing brace inside a string value", `plan_mark({ note: "}", marker: "done" })`, []string{"plan_mark call at line 1 has no runId"}},
		{"braces inside a string value, with runId", `plan_mark({ path: "{x}", marker: "done", runId: "r" })`, nil},
		{"two calls on one line", `plan_mark({ marker: "a", runId: "r" }) then plan_mark({ marker: "b" })`, []string{"plan_mark call at line 1 has no runId"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(planSkillsRunIDProblems(tc.content), "\n")
			if want := strings.Join(tc.want, "\n"); got != want {
				t.Errorf("planSkillsRunIDProblems(%q)\n got: %q\nwant: %q", tc.content, got, want)
			}
		})
	}

	rel := "skills/plan/SKILL.md"
	data, err := os.ReadFile(filepath.Join(planSkillsRepoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	content := string(data)
	marks, reuses := 0, 0
	for _, site := range planSkillsExtractCalls(content) {
		switch site.name {
		case "plan_mark":
			marks++
		case "plan_prepare":
			if planSkillsIsReuseCall(site) {
				reuses++
			}
		}
	}
	if marks == 0 || reuses == 0 {
		t.Fatalf("%s: found %d plan_mark calls and %d plan_prepare reuse calls -- the call detection no longer matches this file's call style", rel, marks, reuses)
	}
	for _, problem := range planSkillsRunIDProblems(content) {
		t.Errorf("%s: %s", rel, problem)
	}
}
