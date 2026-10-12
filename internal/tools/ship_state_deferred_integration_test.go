//go:build integration

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// callRegisteredShipStateText calls the registered ship_state tool over an
// in-memory MCP session and returns the result with the whole text of its
// first content block, which is the Markdown a caller reads.
func callRegisteredShipStateText(t *testing.T, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	s := mcpserver.New("test", "0.0.0-test")
	RegisterShipStateTools(s)

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

	res, err := c.CallTool(ctx, &mcp.CallToolParams{Name: "ship_state", Arguments: args})
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

// shipStateIntegrationRepo makes a git repository with one commit on branch
// under t.TempDir, makes it the working directory, and returns its path. The
// registered ship_state handler resolves its project root from that directory.
func shipStateIntegrationRepo(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	initGitFixture(t, dir)
	gitCommit(t, dir, "initial")
	checkoutBranch(t, dir, branch)
	t.Chdir(dir)
	return dir
}

// readDeferredFile reads .sdlc-v2/history/deferred.json under root straight
// from disk, so a test sees the persisted rows and not a store's view of them.
func readDeferredFile(t *testing.T, root string) []history.DeferredIssue {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".sdlc-v2", "history", "deferred.json"))
	if err != nil {
		t.Fatalf("read deferred.json: %v", err)
	}
	var rows []history.DeferredIssue
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode deferred.json %q: %v", raw, err)
	}
	return rows
}

// TestIntegration_ShipStateDefer_MergesNeedsDirectionRow calls the registered
// ship_state defer action three times on one file with the needs-direction
// reason. The second call must merge its line into the open row, and the third
// must find its text already there. The persisted file holds one row and the
// output names the row each time.
func TestIntegration_ShipStateDefer_MergesNeedsDirectionRow(t *testing.T) {
	dir := shipStateIntegrationRepo(t, "feat/defer-int")
	if res, text := callRegisteredShipStateText(t, map[string]any{"action": "init"}); res.IsError {
		t.Fatalf("init: %s", text)
	}
	call := func(title string) string {
		t.Helper()
		res, text := callRegisteredShipStateText(t, map[string]any{
			"action": "defer",
			"detail": map[string]any{
				"severity": "medium", "file": "internal/a.go", "title": title,
				"reason": history.ReasonNeedsDirection,
			},
		})
		if res.IsError {
			t.Fatalf("defer %q returned a tool error:\n%s", title, text)
		}
		return text
	}

	first := call("choose A or B")
	rows := readDeferredFile(t, dir)
	if len(rows) != 1 {
		t.Fatalf("rows after the first defer = %+v, want 1", rows)
	}
	id := rows[0].ID
	for _, want := range []string{"Deferred finding recorded", "(id " + id + ")", "added deferred item " + id + ". Continue."} {
		if !strings.Contains(first, want) {
			t.Errorf("first output missing %q:\n%s", want, first)
		}
	}

	second := call("also choose C or D")
	rows = readDeferredFile(t, dir)
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("rows after the second defer = %+v, want the one row %s", rows, id)
	}
	if rows[0].Status != history.StatusOpen || rows[0].File != "internal/a.go" || rows[0].Reason != history.ReasonNeedsDirection {
		t.Errorf("row = %+v, want an open needs-direction row for internal/a.go", rows[0])
	}
	if want := "choose A or B\n- also choose C or D"; rows[0].Description != want {
		t.Errorf("description = %q, want %q", rows[0].Description, want)
	}
	for _, want := range []string{"Merged into deferred item " + id + ".", "merged into deferred item " + id + ". Continue."} {
		if !strings.Contains(second, want) {
			t.Errorf("second output missing %q:\n%s", want, second)
		}
	}

	third := call("also choose C or D")
	rows = readDeferredFile(t, dir)
	if len(rows) != 1 || rows[0].Description != "choose A or B\n- also choose C or D" {
		t.Errorf("rows after the repeat = %+v, want the text once", rows)
	}
	if want := "deferred item " + id + " already holds this text. Continue."; !strings.Contains(third, want) {
		t.Errorf("third output missing %q:\n%s", want, third)
	}
}

// TestIntegration_ShipStateDeferredAdd_Merge calls the registered ship_state
// deferred_add action with detail.merge. A first call adds a row, a second
// call appends only the new line, a repeat changes nothing, and a call after
// the row is resolved adds the row <id>-2. The file and the output agree after
// each call.
func TestIntegration_ShipStateDeferredAdd_Merge(t *testing.T) {
	dir := shipStateIntegrationRepo(t, "feat/add-int")
	add := func(desc string) string {
		t.Helper()
		res, text := callRegisteredShipStateText(t, map[string]any{
			"action": "deferred_add",
			"detail": map[string]any{"id": "harden-pending-plan", "description": desc, "merge": true},
		})
		if res.IsError {
			t.Fatalf("deferred_add %q returned a tool error:\n%s", desc, text)
		}
		return text
	}

	out := add("plan A is pending")
	for _, want := range []string{"harden-pending-plan", "added deferred item harden-pending-plan. Continue."} {
		if !strings.Contains(out, want) {
			t.Errorf("first output missing %q:\n%s", want, out)
		}
	}
	if rows := readDeferredFile(t, dir); len(rows) != 1 || rows[0].Description != "plan A is pending" {
		t.Fatalf("rows after the first call = %+v, want one row with the text", rows)
	}

	out = add("plan A is pending\nplan B is pending")
	if want := "merged into deferred item harden-pending-plan. Continue."; !strings.Contains(out, want) {
		t.Errorf("second output missing %q:\n%s", want, out)
	}
	rows := readDeferredFile(t, dir)
	if len(rows) != 1 || rows[0].Description != "plan A is pending\n- plan B is pending" {
		t.Fatalf("rows after the merge = %+v, want the new line appended once", rows)
	}

	out = add("plan A is pending\nplan B is pending")
	if want := "deferred item harden-pending-plan already holds this text. Continue."; !strings.Contains(out, want) {
		t.Errorf("repeat output missing %q:\n%s", want, out)
	}
	if rows := readDeferredFile(t, dir); len(rows) != 1 || rows[0].Description != "plan A is pending\n- plan B is pending" {
		t.Errorf("rows after the repeat = %+v, want no change", rows)
	}

	if res, text := callRegisteredShipStateText(t, map[string]any{
		"action": "deferred_resolve",
		"detail": map[string]any{"id": "harden-pending-plan"},
	}); res.IsError {
		t.Fatalf("deferred_resolve returned a tool error:\n%s", text)
	}
	out = add("plan C is pending")
	if want := "added deferred item harden-pending-plan-2. Continue."; !strings.Contains(out, want) {
		t.Errorf("output after the resolve missing %q:\n%s", want, out)
	}
	rows = readDeferredFile(t, dir)
	if len(rows) != 2 || rows[0].Status != history.StatusResolved || rows[1].ID != "harden-pending-plan-2" || rows[1].Status != history.StatusOpen {
		t.Errorf("rows after the resolve = %+v, want the resolved row and an open harden-pending-plan-2", rows)
	}
}

// TestIntegration_ShipStateDeferred_UnusableStore calls the registered
// ship_state handler while deferred.json does not parse. defer still succeeds
// on the run state file and names the hand edit before the deferred_add call.
// deferred_add returns a tool error whose Do this text names the same hand
// edit. Neither call changes the file.
func TestIntegration_ShipStateDeferred_UnusableStore(t *testing.T) {
	dir := shipStateIntegrationRepo(t, "feat/unusable-int")
	if res, text := callRegisteredShipStateText(t, map[string]any{"action": "init"}); res.IsError {
		t.Fatalf("init: %s", text)
	}
	storePath := filepath.Join(dir, ".sdlc-v2", "history", "deferred.json")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(storePath, []byte("{garbage"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, text := callRegisteredShipStateText(t, map[string]any{
		"action": "defer",
		"detail": map[string]any{"severity": "medium", "file": "internal/a.go", "title": "unchecked error"},
	})
	if res.IsError {
		t.Fatalf("defer must not fail when deferred.json does not parse:\n%s", text)
	}
	for _, want := range []string{
		"WARNING: could not persist this item",
		dashboardDeleteDeferredParseSuggestion + " After that, recover it with ship_state action=deferred_add",
		"deferred item not written. " + dashboardDeleteDeferredParseSuggestion + " After that, add it with deferred_add, then continue.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("defer output missing %q:\n%s", want, text)
		}
	}

	res, text = callRegisteredShipStateText(t, map[string]any{
		"action": "deferred_add",
		"detail": map[string]any{"id": "x", "description": "y"},
	})
	if !res.IsError {
		t.Fatalf("deferred_add must fail when deferred.json does not parse:\n%s", text)
	}
	for _, want := range []string{"## Do this", dashboardDeleteDeferredParseSuggestion} {
		if !strings.Contains(text, want) {
			t.Errorf("deferred_add output missing %q:\n%s", want, text)
		}
	}

	if got, _ := os.ReadFile(storePath); string(got) != "{garbage" {
		t.Errorf("deferred.json = %q, want it unchanged", got)
	}
}
