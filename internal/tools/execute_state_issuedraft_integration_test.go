//go:build integration

package tools

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// callRegisteredExecuteState calls the registered execute_state tool over an
// in-memory MCP session and returns the result with the whole text of its
// first content block, which is the Markdown a caller reads.
func callRegisteredExecuteState(t *testing.T, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	s := mcpserver.New("test", "0.0.0-test")
	RegisterExecuteStateTools(s)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "execute_state", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("CallTool: no content")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	return res, text.Text
}

// issueDraftIntegrationRepo makes a git repository under t.TempDir with an
// execute run state on branch, makes it the working directory, and returns its
// path. The registered execute_state handler resolves its project root from
// that directory.
func issueDraftIntegrationRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, branch)
	writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), "")
	createExecState(t, dir, branch, map[string]any{"branch": branch})
	t.Chdir(dir)
	return dir
}

// TestIntegration_ExecuteStateIssueDraft_RecordsDraftAndDeferredRow calls the
// registered execute_state issue-draft action twice. Each call must append the
// draft to the run state file and store one open execute-drift row in
// deferred.json. The output echoes the id of the stored row, and the second
// call gets another id.
func TestIntegration_ExecuteStateIssueDraft_RecordsDraftAndDeferredRow(t *testing.T) {
	dir := issueDraftIntegrationRepo(t, "feat/drafts-int")
	call := func(title string) string {
		t.Helper()
		res, text := callRegisteredExecuteState(t, map[string]any{
			"action":          "issue-draft",
			"branch":          "feat/drafts-int",
			"issueDraftTitle": title,
			"issueDraftBody":  "Body of " + title,
		})
		if res.IsError {
			t.Fatalf("issue-draft %q returned a tool error:\n%s", title, text)
		}
		return text
	}

	first := call("Fix flaky test")
	rows := readDeferredFile(t, dir)
	if len(rows) != 1 {
		t.Fatalf("rows after the first draft = %+v, want 1", rows)
	}
	row := rows[0]
	if row.Source != history.SourceExecuteDrift || row.Status != history.StatusOpen || row.Description != "Fix flaky test" || !strings.HasSuffix(row.ID, "-1") {
		t.Errorf("row = %+v, want an open %s row for the title with an id ending in -1", row, history.SourceExecuteDrift)
	}
	for _, want := range []string{"# execute_state — ok", "- added: true", "- totalDrafts: 1", "Nothing more is needed for this draft."} {
		if !strings.Contains(first, want) {
			t.Errorf("first output missing %q:\n%s", want, first)
		}
	}
	if !slices.Contains(strings.Split(first, "\n"), "- deferredId: "+row.ID) {
		t.Errorf("first output has no line that echoes the stored id %q:\n%s", row.ID, first)
	}

	second := call("Remove dead code")
	rows = readDeferredFile(t, dir)
	if len(rows) != 2 || rows[1].Description != "Remove dead code" || rows[1].ID == rows[0].ID || !strings.HasSuffix(rows[1].ID, "-2") {
		t.Fatalf("rows after the second draft = %+v, want a second row with its own id ending in -2", rows)
	}
	if !strings.Contains(second, "- totalDrafts: 2") {
		t.Errorf("second output missing the draft count:\n%s", second)
	}
	if !slices.Contains(strings.Split(second, "\n"), "- deferredId: "+rows[1].ID) {
		t.Errorf("second output has no line that echoes the stored id %q:\n%s", rows[1].ID, second)
	}

	drafts, _ := readExecState(t, dir, "feat/drafts-int")["pendingIssueDrafts"].([]any)
	if len(drafts) != 2 {
		t.Fatalf("pendingIssueDrafts = %v, want 2 entries", drafts)
	}
	if d, _ := drafts[0].(map[string]any); d["title"] != "Fix flaky test" || d["body"] != "Body of Fix flaky test" {
		t.Errorf("pendingIssueDrafts[0] = %v, want the first draft title and body", drafts[0])
	}
}

// TestIntegration_ExecuteStateIssueDraft_UnusableStore calls the registered
// execute_state issue-draft action while deferred.json does not parse. The
// draft is still recorded on the run state file. The output echoes no
// deferredId, carries the warning, and names the hand edit before the
// deferred_add call. deferred.json is not changed.
func TestIntegration_ExecuteStateIssueDraft_UnusableStore(t *testing.T) {
	dir := issueDraftIntegrationRepo(t, "feat/drafts-bad")
	storePath := filepath.Join(dir, paths.DataDir, "history", "deferred.json")
	writeFile(t, storePath, "{garbage")

	res, text := callRegisteredExecuteState(t, map[string]any{
		"action":          "issue-draft",
		"branch":          "feat/drafts-bad",
		"issueDraftTitle": "Fix flaky test",
		"issueDraftBody":  "The test fails intermittently.",
	})
	if res.IsError {
		t.Fatalf("issue-draft must not fail when deferred.json does not parse:\n%s", text)
	}
	for _, want := range []string{
		"- added: true",
		"WARNING: could not persist this item",
		dashboardDeleteDeferredParseSuggestion + " After that, record it durably with ship_state action=deferred_add",
		"Do NOT retry issue-draft",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "deferredId") {
		t.Errorf("output echoes a deferredId although nothing was stored:\n%s", text)
	}
	if got, _ := os.ReadFile(storePath); string(got) != "{garbage" {
		t.Errorf("deferred.json = %q, want it unchanged", got)
	}
	if drafts, _ := readExecState(t, dir, "feat/drafts-bad")["pendingIssueDrafts"].([]any); len(drafts) != 1 {
		t.Errorf("pendingIssueDrafts = %v, want the draft on the run state file", drafts)
	}
}
