//go:build integration

package tools

import (
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// shipStateFixRowStatus reads the status of the one data.healing.fixProgress
// row of the ship state of branch under dir straight from disk.
func shipStateFixRowStatus(t *testing.T, dir, branch string) string {
	t.Helper()
	st, err := state.Find(dir, "ship", branch)
	if err != nil || st == nil {
		t.Fatalf("state.Find = %v, %v — want the ship state", st, err)
	}
	healing, _ := st.Data["healing"].(map[string]any)
	rows, _ := healing["fixProgress"].([]any)
	if len(rows) != 1 {
		t.Fatalf("fixProgress = %v, want one row", rows)
	}
	row, _ := rows[0].(map[string]any)
	status, _ := row["status"].(string)
	return status
}

// TestIntegration_ShipStateFixedSetsFixRow calls the registered ship_state
// tool for init, for healing_record kind fix-progress with status queued, and
// then for healing_record kind fixed on the same finding. The fix row on disk
// must move from queued to fixed, and the output of the fixed call must hold
// the next id and the instruction. A repeat call must leave the row fixed and
// say that the fix is already recorded.
func TestIntegration_ShipStateFixedSetsFixRow(t *testing.T) {
	const branch = "feat/fixed-row-int"
	dir := shipStateIntegrationRepo(t, branch)
	if res, text := callRegisteredShipStateText(t, map[string]any{"action": "init"}); res.IsError {
		t.Fatalf("init: %s", text)
	}
	finding := func(kind, status string) map[string]any {
		d := map[string]any{
			"kind": kind, "origin": "local-review", "severity": "low",
			"file": "a.go", "line": 12, "title": "t",
		}
		if status != "" {
			d["status"] = status
		}
		return d
	}
	call := func(detail map[string]any) string {
		t.Helper()
		res, text := callRegisteredShipStateText(t, map[string]any{
			"action": "healing_record",
			"step":   "received-review",
			"detail": detail,
		})
		if res.IsError {
			t.Fatalf("healing_record %v returned a tool error:\n%s", detail["kind"], text)
		}
		return text
	}

	call(finding("fix-progress", "queued"))
	if got := shipStateFixRowStatus(t, dir, branch); got != "queued" {
		t.Fatalf("fix row status after fix-progress = %q, want queued", got)
	}

	first := call(finding("fixed", ""))
	if got := shipStateFixRowStatus(t, dir, branch); got != "fixed" {
		t.Errorf("fix row status after the fixed call = %q, want fixed", got)
	}
	for _, want := range []string{"continue-fix-pass",
		"Fix recorded. Fix-progress row set to fixed (was queued). Continue the fix pass.",
		"## fixRow", "- prior: queued", "- status: fixed"} {
		if !strings.Contains(first, want) {
			t.Errorf("fixed output missing %q:\n%s", want, first)
		}
	}

	second := call(finding("fixed", ""))
	if got := shipStateFixRowStatus(t, dir, branch); got != "fixed" {
		t.Errorf("fix row status after the repeat call = %q, want fixed", got)
	}
	for _, want := range []string{"continue-fix-pass", "Fix already recorded. Continue the fix pass."} {
		if !strings.Contains(second, want) {
			t.Errorf("repeat output missing %q:\n%s", want, second)
		}
	}
}
