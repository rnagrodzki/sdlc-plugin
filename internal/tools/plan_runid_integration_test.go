//go:build integration

package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// callRegisteredPlanTool calls the registered plan tool name (plan_prepare
// or plan_mark) over an in-memory MCP session from dir, so main-root and
// branch resolution run as in production. GIT_CEILING_DIRECTORIES stops git
// from walking up past dir's parent. It returns the result and the text of
// its first content block.
func callRegisteredPlanTool(t *testing.T, dir, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)

	s := mcpserver.New("test", "0.0.0-test")
	RegisterPlanTools(s)
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	c, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("CallTool %s: no content", name)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	return res, text.Text
}

// planIntegrationIntegrity decodes the planIntegrity object of the state
// file of runID under dir.
func planIntegrationIntegrity(t *testing.T, dir, runID string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(planTestReadRun(t, dir, runID), &doc); err != nil {
		t.Fatalf("decode %s: %v", runID, err)
	}
	pi, _ := doc["planIntegrity"].(map[string]any)
	return pi
}

// TestPlanRunIDIntegration_MarkTargetsOwnRun calls the registered plan_mark
// with run A's runId while the newer run B exists on the same branch. Run A
// gets the marker and keeps its seeded skillInvoked; run B is unchanged.
func TestPlanRunIDIntegration_MarkTargetsOwnRun(t *testing.T) {
	dir := planTestGitRepo(t, "main")
	planTestSeedRun(t, dir, planTestRunA, planTestActiveData())
	planTestSeedRun(t, dir, planTestRunB, planTestActiveData())
	beforeB := planTestReadRun(t, dir, planTestRunB)

	res, text := callRegisteredPlanTool(t, dir, "plan_mark", map[string]any{
		"marker": "critiqueRan",
		"runId":  planTestRunA,
	})
	if res.IsError {
		t.Fatalf("plan_mark with runId A returned an error:\n%s", text)
	}
	if !strings.Contains(text, "# plan_mark — ok") || !strings.Contains(text, planTestRunA+".json") {
		t.Errorf("plan_mark text must be ok and name run A's file:\n%s", text)
	}
	if strings.Contains(text, planTestRunB) {
		t.Errorf("plan_mark text names run B:\n%s", text)
	}

	piA := planIntegrationIntegrity(t, dir, planTestRunA)
	if _, ok := piA["critiqueRan"]; !ok {
		t.Errorf("run A planIntegrity has no critiqueRan: %v", piA)
	}
	if piA["skillInvoked"] != "2020-01-01T00:00:00Z" {
		t.Errorf("run A skillInvoked = %v, want the seeded 2020-01-01T00:00:00Z", piA["skillInvoked"])
	}
	if afterB := planTestReadRun(t, dir, planTestRunB); string(afterB) != string(beforeB) {
		t.Errorf("run B changed:\nbefore=%s\nafter=%s", beforeB, afterB)
	}
}

// TestPlanRunIDIntegration_PrepareReusesOwnRun calls the registered
// plan_prepare (resume) with run A's runId while the newer run B exists.
// The result names run A; neither state file changes.
func TestPlanRunIDIntegration_PrepareReusesOwnRun(t *testing.T) {
	dir := planTestTemplateRepo(t)
	planTestSeedRun(t, dir, planTestRunA, planTestActiveData())
	planTestSeedRun(t, dir, planTestRunB, planTestActiveData())
	beforeA := planTestReadRun(t, dir, planTestRunA)
	beforeB := planTestReadRun(t, dir, planTestRunB)

	res, text := callRegisteredPlanTool(t, dir, "plan_prepare", map[string]any{
		"skipConfigCheck": true,
		"resume":          true,
		"runId":           planTestRunA,
	})
	if res.IsError {
		t.Fatalf("plan_prepare resume with runId A returned an error:\n%s", text)
	}
	if !strings.Contains(text, "- runId: "+planTestRunA) {
		t.Errorf("plan_prepare text must report runId %s:\n%s", planTestRunA, text)
	}
	if !strings.Contains(text, "Resume mode: run "+planTestRunA+" reused.") {
		t.Errorf("plan_prepare next must name run A:\n%s", text)
	}
	if afterA := planTestReadRun(t, dir, planTestRunA); string(afterA) != string(beforeA) {
		t.Errorf("run A changed on resume:\nbefore=%s\nafter=%s", beforeA, afterA)
	}
	if afterB := planTestReadRun(t, dir, planTestRunB); string(afterB) != string(beforeB) {
		t.Errorf("run B changed:\nbefore=%s\nafter=%s", beforeB, afterB)
	}
}

// TestPlanRunIDIntegration_Errors verifies the done-run and not-found
// errors through the registered tools, and that no state file changes.
func TestPlanRunIDIntegration_Errors(t *testing.T) {
	const doneRun = "plan-main-20260103T000000Z"
	const missing = "plan-main-20260105T000000Z"
	dir := planTestTemplateRepo(t)
	planTestSeedRun(t, dir, doneRun, map[string]any{
		"planIntegrity": map[string]any{"skillInvoked": "x", "done": "y"},
	})
	beforeDone := planTestReadRun(t, dir, doneRun)

	cases := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"plan_prepare resume, done run", "plan_prepare",
			map[string]any{"skipConfigCheck": true, "resume": true, "runId": doneRun},
			"plan run " + doneRun + " is done"},
		{"plan_prepare resolveTemplate, done run", "plan_prepare",
			map[string]any{"skipConfigCheck": true, "resolveTemplate": true, "runId": doneRun},
			"plan run " + doneRun + " is done"},
		{"plan_prepare resume, missing run", "plan_prepare",
			map[string]any{"skipConfigCheck": true, "resume": true, "runId": missing},
			"plan run " + missing + " not found"},
		{"plan_mark, missing run", "plan_mark",
			map[string]any{"marker": "critiqueRan", "runId": missing},
			"plan run " + missing + " not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, text := callRegisteredPlanTool(t, dir, tc.tool, tc.args)
			if !res.IsError {
				t.Fatalf("%s must be an error result:\n%s", tc.tool, text)
			}
			if !strings.Contains(text, "error (domain)") || !strings.Contains(text, tc.want) {
				t.Errorf("error text must be a domain error with %q:\n%s", tc.want, text)
			}
			if !strings.Contains(text, "no runId, no resume and no resolveTemplate") {
				t.Errorf("error text must name the new-run recovery:\n%s", text)
			}
		})
	}
	if afterDone := planTestReadRun(t, dir, doneRun); string(afterDone) != string(beforeDone) {
		t.Errorf("done run changed:\nbefore=%s\nafter=%s", beforeDone, afterDone)
	}
	if files := listStateFiles(t, dir); len(files) != 1 {
		t.Errorf("state files = %v, want only the done run (no new run)", files)
	}
}
