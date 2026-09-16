// Package skillcheck cross-checks the execute skill family's markdown
// (plugins/sdlc/skills/execute/*.md) against the live execute_state action
// registry, the "## Wave loop" section's coverage of the actions that drive
// it, and the client-side stall-classification symbols (stallCause,
// nudgedAt, STALLED_AFTER_NUDGE, ClassifyStall) deleted from internal/wave
// when the server-driven wave-await poll replaced them.
//
// This file intentionally contains no production logic — every unexported
// identifier below is prefixed executeSkills, and every declared Test
// function is prefixed TestExecuteSkills, to avoid name collisions with the
// sibling skillcheck_*_test.go files in this same package.
package skillcheck

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// executeSkillsRepoRoot resolves plugins/sdlc from this test file's own
// path (internal/skillcheck/skillcheck_execute_test.go), independent of the
// directory `go test` happens to be invoked from.
func executeSkillsRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("executeSkillsRepoRoot: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(file))), "plugins", "sdlc")
}

// executeSkillsFiles globs every execute skill markdown file, relative to
// the repo root, rather than hardcoding a list — a new support doc added
// under skills/execute/ is picked up automatically.
func executeSkillsFiles(t *testing.T, repoRoot string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(repoRoot, "skills", "execute", "*.md"))
	if err != nil {
		t.Fatalf("glob skills/execute/*.md: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("executeSkillsFiles: glob matched zero files")
	}
	sort.Strings(matches)
	return matches
}

// executeSkillsReadFile reads an absolute path, failing the test loudly on
// any read error.
func executeSkillsReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// executeSkillsListTools constructs a fresh mcpserver.Server, registers
// every tool family in internal/tools (every Register*Tools function, not
// just execute_state), starts an in-process MCP client against it, and
// returns the tools reported by a real ListTools() call.
func executeSkillsListTools(t *testing.T) []*mcp.Tool {
	t.Helper()

	srv := mcpserver.New("skillcheck-execute-test", "0.0.0-test")

	tools.RegisterCommitTools(srv)
	tools.RegisterDimensionsRenderTools(srv)
	tools.RegisterExecuteStateTools(srv)
	tools.RegisterJiraTools(srv)
	tools.RegisterLearningsTools(srv)
	tools.RegisterLinksTools(srv)
	tools.RegisterMCPFailureTools(srv)
	tools.RegisterMigrateTools(srv)
	tools.RegisterOpenspecTools(srv)
	tools.RegisterPRTools(srv)
	tools.RegisterPlanExploreTools(srv)
	tools.RegisterPlanSupportTools(srv)
	tools.RegisterPlanTools(srv)
	tools.RegisterPollingTools(srv)
	tools.RegisterPrepareOrchestratorTools(srv)
	tools.RegisterReceivedReviewTools(srv)
	tools.RegisterReviewTools(srv)
	tools.RegisterScaffoldTools(srv)
	tools.RegisterSetupTools(srv)
	tools.RegisterSetupWriteTools(srv)
	tools.RegisterShipStateTools(srv)
	tools.RegisterShipTools(srv)
	tools.RegisterValidateTools(srv)

	mcpSrv := srv.MCPServer()

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := mcpSrv.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "skillcheck-execute-test", Version: "0.0.0"}, nil)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	resp, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if resp == nil || len(resp.Tools) == 0 {
		t.Fatal("registry is empty — Register*Tools calls above registered nothing")
	}
	return resp.Tools
}

// executeSkillsActionEnum returns the live "action" enum values registered
// on the execute_state tool's own input schema — the same
// jsonschema:"enum=..." tag on ExecuteStateIn.Action in
// internal/tools/execute_state.go that the switch statement's case labels
// are written against (both lists were confirmed identical, 29 entries, by
// direct inspection while writing this test). Using the live schema
// (rather than regexing the Go source directly) matches this package's
// established convention (skillcheck_plan_test.go's planSkillsBuildSchemas)
// and is robust against unrelated case-label-shaped strings elsewhere in
// that 5000+ line file (e.g. inner switches on complexity/status/severity).
func executeSkillsActionEnum(t *testing.T) map[string]bool {
	t.Helper()

	for _, tl := range executeSkillsListTools(t) {
		if tl.Name != "execute_state" {
			continue
		}
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatalf("execute_state: marshal input schema: %v", err)
		}
		var schema struct {
			Properties struct {
				Action struct {
					Enum []string `json:"enum"`
				} `json:"action"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("execute_state: unmarshal input schema: %v", err)
		}
		if len(schema.Properties.Action.Enum) == 0 {
			t.Fatal("execute_state: action property has an empty enum — schema shape changed, update this test's extraction")
		}
		set := make(map[string]bool, len(schema.Properties.Action.Enum))
		for _, v := range schema.Properties.Action.Enum {
			set[v] = true
		}
		return set
	}
	t.Fatal("execute_state tool not found in registry")
	return nil
}

// executeSkillsActionCallRe matches an execute_state call's action value.
// Every execute_state({...}) call site under plugins/sdlc/skills/execute/
// lists "action" as its very first key (confirmed by inspection of every
// call site in this skill family), so anchoring directly on
// `execute_state({ action: "..."` scopes the match to execute_state calls
// only — unlike a bare `action:\s*"..."` scan, which would also catch other
// tools that happen to take an "action" field, e.g.
// learnings_log({action:"append", ...}) in SKILL.md's Learning Capture step.
var executeSkillsActionCallRe = regexp.MustCompile(`execute_state\(\{\s*action:\s*"([a-z0-9_-]+)"`)

// TestExecuteSkillsActionsAreRegistered is Acceptance Criterion (a): every
// execute_state action named in the execute skill markdown must exist in
// the live execute_state action enum (schema-code-sync-required with the
// execute_state.go switch's case labels).
func TestExecuteSkillsActionsAreRegistered(t *testing.T) {
	repoRoot := executeSkillsRepoRoot(t)
	files := executeSkillsFiles(t, repoRoot)
	enum := executeSkillsActionEnum(t)

	totalRefs := 0
	for _, path := range files {
		content := executeSkillsReadFile(t, path)
		matches := executeSkillsActionCallRe.FindAllStringSubmatch(content, -1)
		for _, m := range matches {
			totalRefs++
			action := m[1]
			if !enum[action] {
				t.Errorf("%s: execute_state call references action %q, which is not in the registered execute_state action enum", path, action)
			}
		}
	}
	if totalRefs == 0 {
		t.Fatal("found zero execute_state action references across execute skill files — the call-detection regex likely no longer matches this file's call style")
	}
}

// executeSkillsHeadingRe matches any Markdown ATX heading line ("## ..." or
// deeper), used to find the end boundary of the Wave loop section.
var executeSkillsHeadingRe = regexp.MustCompile(`(?m)^#{2,6} .+$`)

// executeSkillsSection extracts the block of content starting immediately
// after the line exactly equal to heading and ending at the next Markdown
// heading line, or end of file. Returns "" and false if heading does not
// appear as an exact line in content.
func executeSkillsSection(content, heading string) (string, bool) {
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
	loc := executeSkillsHeadingRe.FindStringIndex(rest)
	if loc == nil {
		return rest, true
	}
	return rest[:loc[0]], true
}

// TestExecuteSkillsWaveLoopNamesCoreActions is Acceptance Criterion (b):
// SKILL.md's "## Wave loop" section — the stage-by-stage
// WAVE-START/RECORD-ON-RETURN/AWAIT/ACT-ON-next/GATES loop that replaced
// the old client-side stall classification — must name all four of the
// actions that drive its stall/failure/retry path: wave-await (the bounded
// server-driven probe), task-done and task-fail (the only completion
// signals the server has), and task-redispatch (reopening a failed task
// for another attempt).
func TestExecuteSkillsWaveLoopNamesCoreActions(t *testing.T) {
	repoRoot := executeSkillsRepoRoot(t)
	content := executeSkillsReadFile(t, filepath.Join(repoRoot, "skills", "execute", "SKILL.md"))

	section, ok := executeSkillsSection(content, "## Wave loop")
	if !ok {
		t.Fatal(`skills/execute/SKILL.md: expected heading "## Wave loop" not found`)
	}

	for _, action := range []string{"wave-await", "task-done", "task-fail", "task-redispatch"} {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(action) + `\b`)
		if !re.MatchString(section) {
			t.Errorf(`skills/execute/SKILL.md: "## Wave loop" section does not name action %q`, action)
		}
	}
}

// executeSkillsRemovedSymbols are client-side stall-classification symbols
// deleted from internal/wave (commit cabe189, "refactor(wave): delete
// ClassifyStall/StallCause/NudgedAt from internal/wave") when wave-await's
// server-driven poll replaced them. No execute skill file may reference
// them by name — a lingering reference would mean the docs still describe a
// mechanism that no longer exists in the code.
var executeSkillsRemovedSymbols = []string{"stallCause", "nudgedAt", "STALLED_AFTER_NUDGE", "ClassifyStall"}

// TestExecuteSkillsNoRemovedSymbols is Acceptance Criterion (c).
func TestExecuteSkillsNoRemovedSymbols(t *testing.T) {
	repoRoot := executeSkillsRepoRoot(t)
	files := executeSkillsFiles(t, repoRoot)

	for _, path := range files {
		content := executeSkillsReadFile(t, path)
		for _, sym := range executeSkillsRemovedSymbols {
			if strings.Contains(content, sym) {
				t.Errorf("%s: references removed symbol %q, deleted from internal/wave when wave-await replaced client-side stall classification", path, sym)
			}
		}
	}
}
