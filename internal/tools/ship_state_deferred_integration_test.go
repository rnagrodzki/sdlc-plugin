//go:build integration

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		deferredParseFixStep + " After that, recover it with ship_state action=deferred_add",
		"deferred item not written. " + deferredParseFixStep + " After that, add it with deferred_add, then continue.",
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

// seedDeferredFile writes rows to .sdlc-v2/history/deferred.json under root.
func seedDeferredFile(t *testing.T, root string, rows []history.DeferredIssue) {
	t.Helper()
	dir := filepath.Join(root, ".sdlc-v2", "history")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deferred.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// deferredRows builds n open rows d1..dn with descriptions of descLen runes.
func deferredRows(n, descLen int) []history.DeferredIssue {
	rows := make([]history.DeferredIssue, 0, n)
	for i := 1; i <= n; i++ {
		prio := []string{history.PriorityHigh, history.PriorityMedium, history.PriorityLow}[i%3]
		rows = append(rows, history.DeferredIssue{
			ID: fmt.Sprintf("d%d", i), Created: fmt.Sprintf("2026-01-01T00:00:%02dZ", i%60), Source: "ship:review",
			Priority: prio, Status: history.StatusOpen,
			Description: strings.Repeat("word ", descLen/5),
		})
	}
	return rows
}

// TestIntegration_ShipStateDeferredProposeFollowups_Compact calls the
// registered tool with 130 open rows of 600-character descriptions. The
// output holds titles only and a next line, and the display text stays under
// 20,000 characters.
func TestIntegration_ShipStateDeferredProposeFollowups_Compact(t *testing.T) {
	dir := shipStateIntegrationRepo(t, "feat/compact-int")
	seedDeferredFile(t, dir, deferredRows(130, 600))

	res, text := callRegisteredShipStateText(t, map[string]any{"action": "deferred_propose_followups"})
	if res.IsError {
		t.Fatalf("deferred_propose_followups returned a tool error:\n%s", text)
	}
	for _, want := range []string{
		"openCount: 130",
		`**Next:** To file items, call ship_state({action:"deferred_list", detail:{ids:["<id>", …]}}) for their full text.`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q", want)
		}
	}
	if strings.Contains(text, "- description:") {
		t.Error("output holds a description field, want titles only")
	}

	out, err := shipState(dir, dir, ShipStateIn{Action: "deferred_propose_followups"}, fixedNow(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(out.(map[string]any)["display"].(string)); n >= 20000 {
		t.Errorf("display has %d characters, want under 20000", n)
	}
}

// TestShipState_DeferredProposeFollowups_Groups checks the group keys and the
// item type on the direct call: every priority key is present, an empty one
// is [] and never null, and an item holds a title and no description.
func TestShipState_DeferredProposeFollowups_Groups(t *testing.T) {
	root := t.TempDir()
	seedDeferredFile(t, root, []history.DeferredIssue{
		{ID: "h1", Created: "2026-01-01", Source: "s", Priority: history.PriorityHigh, Status: history.StatusOpen, Description: strings.Repeat("x", 200)},
	})
	out, err := shipState(root, root, ShipStateIn{Action: "deferred_propose_followups"}, fixedNow(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	groups := m["groups"].(map[string][]DeferredFollowupItem)
	for _, k := range []string{"high", "medium", "low"} {
		if groups[k] == nil {
			t.Errorf("groups[%q] is nil, want a slice", k)
		}
	}
	if len(groups["high"]) != 1 || len(groups["medium"]) != 0 || len(groups["low"]) != 0 {
		t.Fatalf("groups = %+v, want 1 high item only", groups)
	}
	if got := groups["high"][0]; got.ID != "h1" || got.Title != history.DeferredIssueTitle(history.DeferredIssue{ID: "h1", Description: strings.Repeat("x", 200)}) {
		t.Errorf("item = %+v", got)
	}
	raw, _ := json.Marshal(groups)
	if strings.Contains(string(raw), "null") {
		t.Errorf("groups JSON %s holds null", raw)
	}
	if m["next"] == "" || !strings.Contains(m["next"].(string), "deferred_list") {
		t.Errorf("next = %q, want the deferred_list call", m["next"])
	}

	// No open item: groups are still [], next says nothing to file.
	empty := t.TempDir()
	out, err = shipState(empty, empty, ShipStateIn{Action: "deferred_propose_followups"}, fixedNow(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	m = out.(map[string]any)
	if want := "No open deferred items. Nothing to file."; m["next"] != want {
		t.Errorf("next = %q, want %q", m["next"], want)
	}
	if raw, _ := json.Marshal(m["groups"]); strings.Contains(string(raw), "null") {
		t.Errorf("empty groups JSON %s holds null", raw)
	}
}

// TestShipState_DeferredList_IDs covers detail.ids: only the named items, in
// input order, in full; a repeated id gives one item; no ids gives every item.
func TestShipState_DeferredList_IDs(t *testing.T) {
	root := t.TempDir()
	rows := deferredRows(5, 100)
	seedDeferredFile(t, root, rows)
	call := func(d map[string]any) map[string]any {
		t.Helper()
		out, err := shipState(root, root, ShipStateIn{Action: "deferred_list", Detail: d}, fixedNow(time.Now()))
		if err != nil {
			t.Fatalf("deferred_list %v: %v", d, err)
		}
		return out.(map[string]any)
	}

	m := call(map[string]any{"ids": []any{"d4", "d2", "d4"}})
	got := m["issues"].([]history.DeferredIssue)
	if len(got) != 2 || got[0].ID != "d4" || got[1].ID != "d2" {
		t.Fatalf("issues = %+v, want d4 then d2 once", got)
	}
	if got[0].Description != rows[3].Description {
		t.Errorf("description = %q, want the full text", got[0].Description)
	}
	if want := "Draft one issue per item. Use the item description as the body."; m["next"] != want {
		t.Errorf("next = %q, want %q", m["next"], want)
	}

	all := call(nil)
	if n := len(all["issues"].([]history.DeferredIssue)); n != 5 {
		t.Errorf("without ids: %d issues, want 5", n)
	}
	if want := "Each item is in full. To file items, draft one issue per chosen item."; all["next"] != want {
		t.Errorf("without ids: next = %q, want %q", all["next"], want)
	}
}

// TestShipState_DeferredList_IDLimit builds 50 and 51 ids from
// deferredListMaxIDs. 50 passes the limit check, 51 fails it.
func TestShipState_DeferredList_IDLimit(t *testing.T) {
	root := t.TempDir()
	seedDeferredFile(t, root, deferredRows(deferredListMaxIDs+1, 10))
	ids := func(n int) []any {
		out := make([]any, 0, n)
		for i := 1; i <= n; i++ {
			out = append(out, fmt.Sprintf("d%d", i))
		}
		return out
	}
	out, err := shipState(root, root, ShipStateIn{Action: "deferred_list", Detail: map[string]any{"ids": ids(deferredListMaxIDs)}}, fixedNow(time.Now()))
	if err != nil {
		t.Fatalf("%d ids: %v", deferredListMaxIDs, err)
	}
	if n := len(out.(map[string]any)["issues"].([]history.DeferredIssue)); n != deferredListMaxIDs {
		t.Errorf("%d ids returned %d issues", deferredListMaxIDs, n)
	}
	_, err = shipState(root, root, ShipStateIn{Action: "deferred_list", Detail: map[string]any{"ids": ids(deferredListMaxIDs + 1)}}, fixedNow(time.Now()))
	assertDeferredListError(t, err, "deferred_list: detail.ids has 51 ids, the limit is 50", "Split the ids into calls of 50 or fewer. Nothing was read.")
}

// TestShipState_DeferredList_IDErrors pins each detail.ids input error. The
// store is unreadable (a directory sits where deferred.json belongs), so a
// call that read the file would fail with another error.
func TestShipState_DeferredList_IDErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".sdlc-v2", "history", "deferred.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	shape := "Pass detail.ids as [\"<id>\", …] with each id a non-empty string, or omit it to list every item. Nothing was read."
	for _, tc := range []struct {
		name      string
		ids       any
		msg, sugg string
	}{
		{"string", "d1", "deferred_list: detail.ids must be an array of strings, got string", shape},
		{"object", map[string]any{"a": "d1"}, "deferred_list: detail.ids must be an array of strings, got map[string]interface {}", shape},
		{"null", nil, "deferred_list: detail.ids must be an array of strings, got null", shape},
		{"number in array", []any{"d1", float64(2)}, "deferred_list: detail.ids[1] must be a non-empty string, got float64", shape},
		{"null in array", []any{nil}, "deferred_list: detail.ids[0] must be a non-empty string, got null", shape},
		{"empty string in array", []any{"d1", ""}, "deferred_list: detail.ids[1] must be a non-empty string, got a blank string", shape},
		{"blank string in array", []any{"  \t"}, "deferred_list: detail.ids[0] must be a non-empty string, got a blank string", shape},
		{"blank in a []string", []string{"d1", "d2", " "}, "deferred_list: detail.ids[2] must be a non-empty string, got a blank string", shape},
		{"empty", []any{}, "deferred_list: detail.ids is empty", "Pass at least one id, or omit detail.ids to list every item. Nothing was read."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := shipState(root, root, ShipStateIn{Action: "deferred_list", Detail: map[string]any{"ids": tc.ids}}, fixedNow(time.Now()))
			assertDeferredListError(t, err, tc.msg, tc.sugg)
		})
	}
}

// deferredListUnknownSugg is the Suggestion of the unknown-id error.
const deferredListUnknownSugg = "Call deferred_list without detail.ids to see every id, open and resolved, then retry with ids from that list. Nothing was written."

// TestShipState_DeferredList_UnknownID returns the unknown-id error and no
// issues, even when other ids in the call exist. The error names every
// unknown id once, in input order.
func TestShipState_DeferredList_UnknownID(t *testing.T) {
	root := t.TempDir()
	seedDeferredFile(t, root, deferredRows(2, 10))
	path := filepath.Join(root, ".sdlc-v2", "history", "deferred.json")
	_, err := shipState(root, root, ShipStateIn{Action: "deferred_list", Detail: map[string]any{"ids": []any{"d1", "nope"}}}, fixedNow(time.Now()))
	assertDeferredListError(t, err, `deferred_list: 1 unknown id(s), no item in `+path+` has them: "nope"`, deferredListUnknownSugg)

	_, err = shipState(root, root, ShipStateIn{Action: "deferred_list", Detail: map[string]any{"ids": []any{"x2", "d2", "x1", "x2"}}}, fixedNow(time.Now()))
	assertDeferredListError(t, err, `deferred_list: 2 unknown id(s), no item in `+path+` has them: "x2", "x1"`, deferredListUnknownSugg)
}

// TestShipState_DeferredList_ResolvedIDAndOpenCount pins that detail.ids
// also picks a resolved item, and that openCount counts every open item of
// the file, not only the picked ones.
func TestShipState_DeferredList_ResolvedIDAndOpenCount(t *testing.T) {
	root := t.TempDir()
	rows := deferredRows(3, 10)
	rows[2].Status = history.StatusResolved
	seedDeferredFile(t, root, rows)
	out, err := shipState(root, root, ShipStateIn{Action: "deferred_list", Detail: map[string]any{"ids": []any{"d3"}}}, fixedNow(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if got := m["issues"].([]history.DeferredIssue); len(got) != 1 || got[0].ID != "d3" {
		t.Errorf("issues = %+v, want the resolved d3", got)
	}
	if m["openCount"] != 2 {
		t.Errorf("openCount = %v, want 2 (every open item of the file)", m["openCount"])
	}
}

// TestIntegration_ShipStateDeferredList_IDs pins deferred_list with
// detail.ids through the registered tool: the rendered next line of a good
// call, and the rendered error of a bad, empty, too long and unknown list.
func TestIntegration_ShipStateDeferredList_IDs(t *testing.T) {
	dir := shipStateIntegrationRepo(t, "feat/deferred-list-ids-int")
	seedDeferredFile(t, dir, deferredRows(deferredListMaxIDs+1, 10))

	res, text := callRegisteredShipStateText(t, map[string]any{"action": "deferred_list", "detail": map[string]any{"ids": []any{"d2"}}})
	if res.IsError {
		t.Fatalf("deferred_list returned a tool error:\n%s", text)
	}
	for _, want := range []string{
		"# ship_state — ok",
		"**Next:** Draft one issue per item. Use the item description as the body.",
		"openCount: 51",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}

	many := make([]any, 0, deferredListMaxIDs+1)
	for i := 1; i <= deferredListMaxIDs+1; i++ {
		many = append(many, fmt.Sprintf("d%d", i))
	}
	shape := "Pass detail.ids as [\"<id>\", …] with each id a non-empty string, or omit it to list every item. Nothing was read."
	for _, tc := range []struct {
		name      string
		ids       any
		msg, sugg string
	}{
		{"bad element", []any{"d1", float64(7)}, "deferred_list: detail.ids[1] must be a non-empty string, got float64", shape},
		{"blank", []any{" "}, "deferred_list: detail.ids[0] must be a non-empty string, got a blank string", shape},
		{"empty", []any{}, "deferred_list: detail.ids is empty", "Pass at least one id, or omit detail.ids to list every item. Nothing was read."},
		{"over the cap", many, fmt.Sprintf("deferred_list: detail.ids has %d ids, the limit is %d", deferredListMaxIDs+1, deferredListMaxIDs), fmt.Sprintf("Split the ids into calls of %d or fewer. Nothing was read.", deferredListMaxIDs)},
		{"unknown", []any{"d1", "nope"}, `deferred_list: 1 unknown id(s), no item in `, deferredListUnknownSugg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, text := callRegisteredShipStateText(t, map[string]any{"action": "deferred_list", "detail": map[string]any{"ids": tc.ids}})
			if !res.IsError {
				t.Fatalf("deferred_list returned ok, want an error:\n%s", text)
			}
			for _, want := range []string{
				"# ship_state — error (domain)",
				"## What happened\n" + tc.msg,
				"## Do this\n" + tc.sugg,
			} {
				if !strings.Contains(text, want) {
					t.Errorf("output missing %q:\n%s", want, text)
				}
			}
		})
	}
}

func assertDeferredListError(t *testing.T, err error, msg, sugg string) {
	t.Helper()
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v (%T), want *mcpserver.DomainError", err, err)
	}
	if de.Msg != msg || de.Suggestion != sugg {
		t.Errorf("error = {%q, %q}, want {%q, %q}", de.Msg, de.Suggestion, msg, sugg)
	}
}
