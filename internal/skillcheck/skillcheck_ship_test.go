// Package skillcheck cross-checks the MCP tool names and ship_state action
// names referenced by the ported ship-family skill files (ship,
// verify-pipeline -- Task 46) against the tool names actually
// registered on the Go MCP server, and guards against the pipeline
// documenting direct git/gh mutations in place of its executor tools.
//
// This file intentionally contains no production logic — every unexported
// identifier below is prefixed shipSkills, and every declared Test function
// is prefixed TestShipSkills, to avoid name collisions with the sibling
// skillcheck_test.go and skillcheck_review_test.go files in this same
// package.
package skillcheck

import (
	"context"
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

// shipSkillsFiles lists the Task 46 deliverable skill files, relative to the
// repository root, that this test cross-checks against the registry.
var shipSkillsFiles = []string{
	"skills/ship/SKILL.md",
	"skills/verify-pipeline/SKILL.md",
}

// shipSkillsCallRe matches this repo's documented tool-call pseudocode
// convention: an identifier immediately followed by "({", e.g.
// "ship_prepare({" or "ship_state({".
var shipSkillsCallRe = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\(\{`)

// shipSkillsBuiltinTools are Claude Code's own built-in tools. Skill files
// legitimately call these in pseudocode (e.g. "Read({ ... })"); they are
// never registered on our MCP server and must be excluded from the
// cross-check.
var shipSkillsBuiltinTools = map[string]bool{
	"Read": true, "Write": true, "Edit": true, "Bash": true,
	"Grep": true, "Glob": true, "AskUserQuestion": true,
	"Skill": true, "Agent": true, "WebFetch": true, "WebSearch": true,
}

// shipSkillsRepoRoot resolves the repository root from this test file's own
// path (internal/skillcheck/skillcheck_ship_test.go), independent of the
// directory `go test` happens to be invoked from.
func shipSkillsRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("shipSkillsRepoRoot: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(file))), "plugins", "sdlc")
}

// shipSkillsReadFile reads a skill file relative to the repo root, failing
// the test loudly on any read error.
func shipSkillsReadFile(t *testing.T, repoRoot, rel string) string {
	t.Helper()
	path := filepath.Join(repoRoot, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// shipSkillsBuildRegistry constructs a fresh mcpserver.Server, registers
// every tool family in internal/tools (every Register*Tools function, not
// just the ones the two Task 46 skills happen to call), starts an
// in-process MCP client against it, and returns the set of registered tool
// names as reported by a real ListTools() call.
func shipSkillsBuildRegistry(t *testing.T) map[string]bool {
	t.Helper()

	srv := mcpserver.New("skillcheck-ship-test", "0.0.0-test")

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

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := mcpSrv.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "skillcheck-ship-test", Version: "0.0.0"}, nil)
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

// shipSkillsToolRefs extracts every call-shaped tool reference from a skill
// file's content, excluding Claude Code's own built-in tools and any
// external (non-registry) MCP namespace such as "mcp__atlassian__...".
func shipSkillsToolRefs(content string) []string {
	seen := map[string]bool{}
	var refs []string
	for _, m := range shipSkillsCallRe.FindAllStringSubmatch(content, -1) {
		name := m[1]
		if shipSkillsBuiltinTools[name] || strings.HasPrefix(name, "mcp__") {
			continue
		}
		if !seen[name] {
			seen[name] = true
			refs = append(refs, name)
		}
	}
	return refs
}

// TestShipSkillsToolReferencesAreRegistered asserts that every MCP tool
// name called out in the two Task 46 skill files (ship,
// verify-pipeline) is actually registered on the Go MCP server —
// guarding against skill prose drifting from the real tool surface (e.g. a
// tool renamed in internal/tools without the skill being updated to
// match).
func TestShipSkillsToolReferencesAreRegistered(t *testing.T) {
	repoRoot := shipSkillsRepoRoot(t)
	registered := shipSkillsBuildRegistry(t)

	for _, rel := range shipSkillsFiles {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			content := shipSkillsReadFile(t, repoRoot, rel)

			refs := shipSkillsToolRefs(content)
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

// TestShipSkillsRegistryContainsExpectedTools pins the specific tool names
// the two Task 46 skills are documented to call, by exact name, so a future
// rename in internal/tools fails loudly here with a precise message
// instead of only in the broader scan above.
func TestShipSkillsRegistryContainsExpectedTools(t *testing.T) {
	registered := shipSkillsBuildRegistry(t)

	expected := []string{
		"ship_prepare",
		"ship_state",
		"ship_verify_side_effect",
		"verify_tag_ancestry",
		"poll_await",
		"verify_pipeline_classify",
		"execute_state",
		"version_apply",
		"commit_apply",
		"pr_apply",
		"review_prepare",
	}

	for _, name := range expected {
		if !registered[name] {
			t.Errorf("expected tool %q to be registered, but it is not", name)
		}
	}
}

// shipSkillsStepHeaders maps each of shipmeta's 12 canonical/lifecycle ship
// pipeline step names to the exact "### ..." (or "## ...") section heading
// that documents that step's execution in skills/ship/SKILL.md. Used
// by TestShipSkillsStepActionCrossCheck (AC1) to isolate each step's own
// prose block before checking which ship_state action it references.
var shipSkillsStepHeaders = map[string]string{
	"execute":             "### execute",
	"commit":              "### commit",
	"review":              "### review",
	"received-review":     "### received-review (conditional)",
	"commit-fixes":        "### commit-fixes (conditional)",
	"verify-openspec":     "### verify-openspec (inline, opt-in)",
	"archive-openspec":    "### archive-openspec (inline)",
	"pr":                  "### pr",
	"verify-pipeline":     "### verify-pipeline (inline, opt-in)",
	"await-remote-review": "### await-remote-review (inline, opt-in)",
	"learnings-commit":    "### learnings-commit (inline)",
	"cleanup":             "### Terminal cleanup",
}

// shipSkillsStepExpectedActions maps each canonical step name to the
// ship_state action name(s) at least one of which must appear within that
// step's own section. The six steps shipmeta.InitialShipSteps() scaffolds
// up front (execute, commit, review, received-review, commit-fixes,
// pr) route through the generic begin-step/complete-step pair; the
// remaining steps have no steps[] scaffold entry (state-format.md's
// scaffolding gap) and instead record their outcome via the generic
// "decide" action, except the terminal "cleanup" step, which is a
// shipmeta.ReservedSteps entry driven by the dedicated "cleanup-pipeline"
// action rather than a per-step one.
var shipSkillsStepExpectedActions = map[string][]string{
	"execute":             {"begin-step", "complete-step"},
	"commit":              {"begin-step", "complete-step"},
	"review":              {"begin-step", "complete-step"},
	"received-review":     {"begin-step", "complete-step"},
	"commit-fixes":        {"begin-step", "complete-step"},
	"verify-openspec":     {"decide"},
	"archive-openspec":    {"decide"},
	"pr":                  {"begin-step", "complete-step"},
	"verify-pipeline":     {"decide"},
	"await-remote-review": {"decide"},
	"learnings-commit":    {"decide"},
	"cleanup":             {"cleanup-pipeline"},
}

// shipSkillsHeadingRe matches any Markdown ATX heading line ("## ..." or
// "### ..."), used to find the end boundary of a step's own section.
var shipSkillsHeadingRe = regexp.MustCompile(`(?m)^#{2,3} .+$`)

// shipSkillsSection extracts the block of content starting immediately
// after the line exactly equal to heading and ending at the next Markdown
// heading line (any level from ## to ###), or end of file. Returns "" and
// false if heading does not appear as an exact line in content.
func shipSkillsSection(content, heading string) (string, bool) {
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
	loc := shipSkillsHeadingRe.FindStringIndex(rest)
	if loc == nil {
		return rest, true
	}
	return rest[:loc[0]], true
}

// TestShipSkillsStepActionCrossCheck is the Files-note / Acceptance
// Criterion 1 check: every one of shipmeta's 12 canonical ship pipeline
// step names must have its own documented section in ship/SKILL.md,
// and that section must reference the ship_state action(s) that step's
// lifecycle actually uses (begin-step/complete-step for the six
// scaffolded steps, decide for the five non-scaffolded inline steps, and
// cleanup-pipeline for the terminal cleanup step) -- guarding against a
// step's prose silently drifting onto the wrong generic action.
func TestShipSkillsStepActionCrossCheck(t *testing.T) {
	repoRoot := shipSkillsRepoRoot(t)
	content := shipSkillsReadFile(t, repoRoot, "skills/ship/SKILL.md")

	if len(shipSkillsStepHeaders) != 12 {
		t.Fatalf("shipSkillsStepHeaders has %d entries, want 12 (shipmeta's canonical step count)", len(shipSkillsStepHeaders))
	}
	if len(shipSkillsStepExpectedActions) != 12 {
		t.Fatalf("shipSkillsStepExpectedActions has %d entries, want 12", len(shipSkillsStepExpectedActions))
	}

	for step, heading := range shipSkillsStepHeaders {
		step, heading := step, heading
		t.Run(step, func(t *testing.T) {
			section, ok := shipSkillsSection(content, heading)
			if !ok {
				t.Fatalf("skills/ship/SKILL.md: expected heading %q for step %q not found", heading, step)
			}

			expected, known := shipSkillsStepExpectedActions[step]
			if !known {
				t.Fatalf("no expected ship_state action(s) recorded for step %q", step)
			}

			foundAny := false
			for _, action := range expected {
				if strings.Contains(section, `"`+action+`"`) {
					foundAny = true
					break
				}
			}
			if !foundAny {
				t.Errorf("skills/ship/SKILL.md: step %q's section (heading %q) references none of the expected ship_state action(s) %v",
					step, heading, expected)
			}
		})
	}
}

// shipSkillsBashBlockRe extracts the content of every ```bash ... ``` fenced
// code block, which is this repo's documented convention for a command the
// LLM executes directly via the Bash tool (as opposed to a plain, untagged
// ``` ... ``` fence, used throughout ship/SKILL.md for human-facing
// AskUserQuestion message text, such as the manual tag-and-push
// instructions -- which must NOT be flagged as an LLM-executed mutation).
var shipSkillsBashBlockRe = regexp.MustCompile("(?s)```bash\\n(.*?)```")

// shipSkillsForbiddenMutations are git/gh mutation verbs and version/
// changelog file names that Acceptance Criterion 2 requires never appear
// inside an LLM-executed ```bash block in the ship skill files: every
// deterministic mutation must route through its executor tool
// (commit_apply, pr_apply, version_apply, ship_verify_side_effect, plus the
// user-facing manual tag/push pause, which lives outside any ```bash
// block) rather than a direct git/gh invocation or a hand-edited version
// file.
var shipSkillsForbiddenMutations = []*regexp.Regexp{
	regexp.MustCompile(`\bgit\s+commit\b`),
	regexp.MustCompile(`\bgit\s+push\b`),
	regexp.MustCompile(`\bgit\s+tag\b`),
	regexp.MustCompile(`\bgh\s+pr\s+create\b`),
	regexp.MustCompile(`\bgh\s+pr\s+merge\b`),
	regexp.MustCompile(`\bgh\s+release\s+create\b`),
	regexp.MustCompile(`\bpackage\.json\b`),
	regexp.MustCompile(`\bCHANGELOG\.md\b`),
}

// TestShipSkillsNoDirectMutation is the Acceptance Criterion 2 check
// (analogous in spirit to TestReviewSkillsNoWriteGuardReferences): neither
// ship/SKILL.md nor verify-pipeline/SKILL.md may instruct the LLM
// to run a git/gh mutation, or hand-edit a version/changelog file, directly
// via an executable ```bash block -- every such mutation must instead
// route through an executor tool (commit_apply, pr_apply, version_apply,
// ship_verify_side_effect) or, for the two disclosed manual-only gaps (tag
// creation/push, post-PR-commit push), through a human-facing
// AskUserQuestion pause rather than a Bash-tool-executed command.
//
// Scoped to ```bash-tagged fences only, so it does not false-positive on:
//   - ship's own plain-fenced AskUserQuestion message text that tells
//     the *human* to run `git tag`/`git push` by hand (Q1's documented
//     exception);
//   - verify-pipeline's legitimate CI-fix Edit-tool instructions,
//     which edit source files but never commit or push (its own C1
//     prohibition already bans exactly that).
func TestShipSkillsNoDirectMutation(t *testing.T) {
	repoRoot := shipSkillsRepoRoot(t)

	for _, rel := range shipSkillsFiles {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			content := shipSkillsReadFile(t, repoRoot, rel)

			blocks := shipSkillsBashBlockRe.FindAllStringSubmatch(content, -1)
			for _, m := range blocks {
				block := m[1]
				for _, re := range shipSkillsForbiddenMutations {
					if re.MatchString(block) {
						t.Errorf("%s: a ```bash block instructs a direct git/gh mutation or version-file edit "+
							"(matched %q) -- route this through its executor tool or a human-facing pause instead:\n%s",
							rel, re.String(), block)
					}
				}
			}
		})
	}
}
