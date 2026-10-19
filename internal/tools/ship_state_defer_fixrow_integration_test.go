//go:build integration

package tools

import (
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// TestIntegration_ShipStateDeferSetsFixRow calls the registered ship_state
// tool for init, for healing_record kind fix-progress with status queued, and
// then for defer on the same finding. The fix row on disk must move from
// queued to deferred and carry the deferredId of the run entry, which is the
// id of the row in deferred.json. The output of the defer call must echo the
// row write with the prior status.
func TestIntegration_ShipStateDeferSetsFixRow(t *testing.T) {
	const branch = "feat/defer-row-int"
	dir := shipStateIntegrationRepo(t, branch)
	if res, text := callRegisteredShipStateText(t, map[string]any{"action": "init"}); res.IsError {
		t.Fatalf("init: %s", text)
	}

	res, text := callRegisteredShipStateText(t, map[string]any{
		"action": "healing_record",
		"step":   "received-review",
		"detail": map[string]any{
			"kind": "fix-progress", "origin": "local-review", "severity": "low",
			"file": "a.go", "line": 12, "title": "t", "status": "queued",
		},
	})
	if res.IsError {
		t.Fatalf("healing_record returned a tool error:\n%s", text)
	}

	res, text = callRegisteredShipStateText(t, map[string]any{
		"action": "defer",
		"detail": map[string]any{"severity": "low", "file": "a.go", "line": 12, "title": "t"},
	})
	if res.IsError {
		t.Fatalf("defer returned a tool error:\n%s", text)
	}
	if want := "Fix-progress row set to deferred (was queued)."; !strings.Contains(text, want) {
		t.Errorf("defer output missing %q:\n%s", want, text)
	}

	st, err := state.Find(dir, "ship", branch)
	if err != nil || st == nil {
		t.Fatalf("state.Find = %v, %v — want the ship state", st, err)
	}
	findings, _ := st.Data["deferredFindings"].([]any)
	if len(findings) != 1 {
		t.Fatalf("deferredFindings = %v, want one entry", findings)
	}
	entry, _ := findings[0].(map[string]any)
	entryID, _ := entry["deferredId"].(string)
	rows := readDeferredFile(t, dir)
	if len(rows) != 1 || entryID == "" || rows[0].ID != entryID {
		t.Fatalf("deferredFindings[0].deferredId = %q, deferred.json rows = %+v, want one row with that id", entryID, rows)
	}

	healing, _ := st.Data["healing"].(map[string]any)
	fixRows, _ := healing["fixProgress"].([]any)
	if len(fixRows) != 1 {
		t.Fatalf("fixProgress = %v, want one row", fixRows)
	}
	row, _ := fixRows[0].(map[string]any)
	if row["status"] != "deferred" || row["deferredId"] != entryID {
		t.Errorf("fix row = %v, want status deferred and deferredId %s", row, entryID)
	}
}
