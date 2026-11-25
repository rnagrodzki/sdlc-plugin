//go:build integration

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// preplanIntegrationNonGoals is a Non-goals bullet that no write of the
// integration tests supplies, so each test can check that it stays.
const preplanIntegrationNonGoals = "- Full topic-file viewer in the dashboard"

// preplanIntegrationSeed is the topic file of the integration tests: a new
// topic file with a Non-goals bullet.
var preplanIntegrationSeed = strings.Replace(preplanSkeletonFor("auth flow"),
	"## Non-goals\n\n", "## Non-goals\n\n"+preplanIntegrationNonGoals+"\n\n", 1)

// preplanIntegrationRepo returns a git repo under t.TempDir, set up by
// setup_init, that holds the seed topic file with mode 0640, and the path of
// that file. root has its links resolved.
func preplanIntegrationRepo(t *testing.T) (root, file string) {
	t.Helper()
	// The tool resolves the main root through git, which gives the path with
	// its links resolved (/var is a link to /private/var on macOS).
	root, err := filepath.EvalSymlinks(newOpenspecStageFixture(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	file = filepath.Join(root, ".sdlc-v2", "preplan", "2026-10-29-auth-flow.md")
	writeFile(t, file, preplanIntegrationSeed)
	if err := os.Chmod(file, 0o640); err != nil {
		t.Fatal(err)
	}
	return root, file
}

// preplanIntegrationCall calls the registered plan_support tool over an
// in-memory MCP session from inside root, through the real handler, and
// returns the rendered text and whether the result is an error.
func preplanIntegrationCall(t *testing.T, root string, args map[string]any) (text string, isError bool) {
	t.Helper()
	t.Chdir(root)
	s := mcpserver.New("test", "0.0.0-test")
	RegisterPlanSupportTools(s)
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
	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "plan_support", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("CallTool: no content")
	}
	content, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	return content.Text, res.IsError
}

// wantPreplanIntegrationFile checks the bytes and the mode 0640 of file, and
// that the seeded Non-goals bullet stays.
func wantPreplanIntegrationFile(t *testing.T, file, want string) {
	t.Helper()
	wantPreplanBytes(t, file, want)
	if !strings.Contains(want, "## Non-goals\n\n"+preplanIntegrationNonGoals+"\n") {
		t.Errorf("the expected text lost the Non-goals bullet: the test is wrong")
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("file mode = %v (err %v), want 0640", info, err)
	}
}

// wantPreplanIntegrationText checks that the rendered text holds each of
// parts.
func wantPreplanIntegrationText(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Errorf("rendered text lacks %q:\n%s", part, text)
		}
	}
}

// TestPlanSupportPreplanWriteIntegrationSection writes the Decisions body
// through the MCP tool and checks the file and the rendered output.
func TestPlanSupportPreplanWriteIntegrationSection(t *testing.T) {
	root, file := preplanIntegrationRepo(t)
	text, isError := preplanIntegrationCall(t, root, map[string]any{
		"action": "preplan_write", "topic": "auth flow", "section": "Decisions", "content": preplanWriteRowsAB + "\n\nSmallest cut: all.",
	})
	if isError {
		t.Fatalf("preplan_write returned an error:\n%s", text)
	}
	want := strings.TrimSuffix(preplanIntegrationSeed, preplanDecisionsHeader) + preplanWriteRowsAB + "\n\nSmallest cut: all.\n"
	wantPreplanIntegrationFile(t, file, want)
	wantPreplanIntegrationText(t, text, "# plan_support — ok", "- preplanFile: "+file, "- written:\n  - Decisions\n", preplanWriteNextWritten)
}

// TestPlanSupportPreplanWriteIntegrationReady sets the status ready for plan
// through the MCP tool with one other ready topic file in the folder, and
// checks the file and the ready list in the rendered output.
func TestPlanSupportPreplanWriteIntegrationReady(t *testing.T) {
	root, file := preplanIntegrationRepo(t)
	other := filepath.Join(root, ".sdlc-v2", "preplan", "2026-10-01-other.md")
	writeFile(t, other, "# Preplan: other\n\n**Status:** ready for plan\n")
	text, isError := preplanIntegrationCall(t, root, map[string]any{
		"action": "preplan_write", "topic": "auth flow", "topicStatus": "ready for plan",
	})
	if isError {
		t.Fatalf("preplan_write returned an error:\n%s", text)
	}
	wantPreplanIntegrationFile(t, file, strings.Replace(preplanIntegrationSeed, "**Status:** in progress", "**Status:** ready for plan", 1))
	wantPreplanIntegrationText(t, text, "- written:\n  - Status\n", "- readyTopicFiles:\n  - "+file+"\n  - "+other+"\n",
		"- readyLeftOut: 0\n", preplanWriteNextReady)
}

// TestPlanSupportPreplanWriteIntegrationNoop sends empty content for an
// absent Open questions section through the MCP tool: nothing is written.
func TestPlanSupportPreplanWriteIntegrationNoop(t *testing.T) {
	root, file := preplanIntegrationRepo(t)
	text, isError := preplanIntegrationCall(t, root, map[string]any{
		"action": "preplan_write", "topic": "auth flow", "section": "Open questions", "content": "",
	})
	if isError {
		t.Fatalf("preplan_write returned an error:\n%s", text)
	}
	wantPreplanIntegrationFile(t, file, preplanIntegrationSeed)
	wantPreplanIntegrationText(t, text, "- written: (none)\n", preplanWriteNextNoop)
}

// TestPlanSupportPreplanWriteIntegrationRefused sends a Decisions body with
// no table header through the MCP tool: the result is an error that names
// rule P4, and the file bytes stay the same.
func TestPlanSupportPreplanWriteIntegrationRefused(t *testing.T) {
	root, file := preplanIntegrationRepo(t)
	text, isError := preplanIntegrationCall(t, root, map[string]any{
		"action": "preplan_write", "topic": "auth flow", "section": "Decisions", "content": "| 1 | a | b |",
	})
	if !isError {
		t.Fatalf("preplan_write returned no error for a body that breaks P4:\n%s", text)
	}
	wantPreplanIntegrationFile(t, file, preplanIntegrationSeed)
	wantPreplanIntegrationText(t, text, `Section "Decisions" breaks rule P4`, `Start the body with the two lines "| # | Decision | Reason |" and "|---|---|---|", or send an empty string, then call preplan_write again.`)
}

// TestPlanSupportPreplanWriteIntegrationUnknownSection sends a section name
// that is not in the enum of the section field through the MCP tool: the
// result is the DomainError of the handler, and the file bytes stay the same.
func TestPlanSupportPreplanWriteIntegrationUnknownSection(t *testing.T) {
	root, file := preplanIntegrationRepo(t)
	text, isError := preplanIntegrationCall(t, root, map[string]any{
		"action": "preplan_write", "topic": "auth flow", "section": "Notes", "content": "x",
	})
	if !isError {
		t.Fatalf("preplan_write returned no error for an unknown section:\n%s", text)
	}
	wantPreplanIntegrationFile(t, file, preplanIntegrationSeed)
	// The SDK does not check the enum of an input field, so the handler
	// refuses the value with its own DomainError.
	wantPreplanIntegrationText(t, text, "# plan_support — error (domain)", `section "Notes" is not a topic-file section`)
}

// preplanIntegrationContractBlock1 and preplanIntegrationContractBlock2 are
// two valid blocks of a ## Contracts body: a label line and a table, and a
// label line and a code fence.
const (
	preplanIntegrationContractBlock1 = "**#1 Data: topics table**\n\n| Field | Type |\n|---|---|\n| id | int |"
	preplanIntegrationContractBlock2 = "**#2 Config: feature flag**\n\n```\nfeature.enabled = true\n```"
)

// withPreplanIntegrationContracts returns the seed with body as the body of
// ## Contracts.
func withPreplanIntegrationContracts(body string) string {
	return strings.Replace(preplanIntegrationSeed, "## Contracts\n\n", "## Contracts\n\n"+body+"\n\n", 1)
}

// TestPlanSupportPreplanWriteIntegrationContractsBlank sends empty content for
// ## Contracts through the MCP tool, with a valid Contracts body in the file:
// the result is an error that names rule P12, and the file bytes stay the same.
func TestPlanSupportPreplanWriteIntegrationContractsBlank(t *testing.T) {
	root, file := preplanIntegrationRepo(t)
	seed := withPreplanIntegrationContracts(preplanIntegrationContractBlock1)
	writeFile(t, file, seed)
	if err := os.Chmod(file, 0o640); err != nil {
		t.Fatal(err)
	}
	text, isError := preplanIntegrationCall(t, root, map[string]any{
		"action": "preplan_write", "topic": "auth flow", "section": "Contracts", "content": "",
	})
	if !isError {
		t.Fatalf("preplan_write returned no error for a blank Contracts body:\n%s", text)
	}
	wantPreplanIntegrationFile(t, file, seed)
	wantPreplanIntegrationText(t, text, "# plan_support — error (domain)",
		`preplan_write: Section "Contracts" breaks rule P12: the body is blank: send a labelled block or a No contract line`,
		"Send one labelled block for each contract: a label line **#<id>[, #<id>] <Kind>: <name>**, for example **#2 Endpoint: POST /api/topics**, "+
			"where Kind is one of Endpoint, Data, Config, Message, then a code fence or a table right below it. "+
			"Put each live proposal with no contract on one line such as No contract: #3, #5. The error detail names the line to fix"+preplanWriteAgain)
}

// TestPlanSupportPreplanWriteIntegrationContractsFullBody sends 3 calls for
// ## Contracts through the MCP tool. Each call replaces the whole body: after
// call 2 the file holds both blocks, and after call 3 only block 2.
func TestPlanSupportPreplanWriteIntegrationContractsFullBody(t *testing.T) {
	root, file := preplanIntegrationRepo(t)
	for _, step := range []struct {
		name, content string
	}{
		{"call 1: block #1", preplanIntegrationContractBlock1},
		{"call 2: blocks #1 and #2", preplanIntegrationContractBlock1 + "\n\n" + preplanIntegrationContractBlock2},
		{"call 3: block #2 only", preplanIntegrationContractBlock2},
	} {
		text, isError := preplanIntegrationCall(t, root, map[string]any{
			"action": "preplan_write", "topic": "auth flow", "section": "Contracts", "content": step.content,
		})
		if isError {
			t.Fatalf("%s: preplan_write returned an error:\n%s", step.name, text)
		}
		wantPreplanIntegrationFile(t, file, withPreplanIntegrationContracts(step.content))
		wantPreplanIntegrationText(t, text, "- written:\n  - Contracts\n")
	}
}
