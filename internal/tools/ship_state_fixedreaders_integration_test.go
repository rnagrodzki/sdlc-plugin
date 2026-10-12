//go:build integration

package tools

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// TestIntegration_ShipStateReadersSkipFixedInRun defers three findings through
// the registered ship_state tool, fixes the first one with healing_record kind
// fixed and its deferredId, and then calls the registered read and report
// actions of ship_state and the registered report action of execute_state.
// Each output must count and list the two open findings only, and the ledger
// must read fixed 1, deferred 2, unaccounted 0.
func TestIntegration_ShipStateReadersSkipFixedInRun(t *testing.T) {
	const branch = "feat/readers-int"
	dir := shipStateIntegrationRepo(t, branch)
	writeFile(t, filepath.Join(dir, paths.DataDir, "config.toml"), "")
	createExecState(t, dir, branch, map[string]any{"branch": branch})
	if res, text := callRegisteredShipStateText(t, map[string]any{"action": "init"}); res.IsError {
		t.Fatalf("init: %s", text)
	}

	findings := []struct{ file, title, reason string }{
		{"a.go", "fixed by the fix pass", "below-threshold"},
		{"b.go", "open wont-fix finding", "wont-fix"},
		{"c.go", "open needs-direction finding", "needs-direction"},
	}
	for _, f := range findings {
		res, text := callRegisteredShipStateText(t, map[string]any{
			"action": "defer",
			"detail": map[string]any{"severity": "low", "file": f.file, "line": 3, "title": f.title, "reason": f.reason},
		})
		if res.IsError {
			t.Fatalf("defer %s: %s", f.title, text)
		}
	}
	entries := shipStateDeferredEntries(t, dir, branch)
	if len(entries) != 3 {
		t.Fatalf("deferredFindings = %v, want 3 entries", entries)
	}
	fixedID, _ := entries[0]["deferredId"].(string)

	for name, detail := range map[string]map[string]any{
		"review-total": {"kind": "review-total", "total": 3, "dimensions": 2},
		"fixed": {
			"kind": "fixed", "origin": "local-review", "severity": "low", "file": "a.go", "line": 3,
			"title": "fixed by the fix pass", "deferredId": fixedID,
		},
	} {
		if res, text := callRegisteredShipStateText(t, map[string]any{"action": "healing_record", "detail": detail}); res.IsError {
			t.Fatalf("healing_record %s: %s", name, text)
		}
	}
	if fixed := shipStateDeferredEntries(t, dir, branch)[0]; fixed["fixedInRun"] != true {
		t.Fatalf("deferredFindings[0] = %v, want fixedInRun true", fixed)
	}

	// mustContain fails the test for each want that text lacks.
	mustContain := func(t *testing.T, text string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("output missing %q:\n%s", want, text)
			}
		}
	}

	t.Run("ship_state read", func(t *testing.T) {
		res, text := callRegisteredShipStateText(t, map[string]any{"action": "read"})
		if res.IsError {
			t.Fatalf("read: %s", text)
		}
		// reportData counts the two open findings. The raw deferredFindings
		// list still holds all three entries, and only the first carries the flag.
		mustContain(t, text,
			"- deferredFindings: 2\n",
			"### reportData.reviewLedger\n- total: 3\n- fixed: 1\n- unaccounted: 0\n",
			"- needs-direction: 1\n- wont-fix: 1\n",
			"## deferredFindings[2]\n",
		)
		if n := strings.Count(text, "- fixedInRun: true\n"); n != 1 {
			t.Errorf("fixedInRun true appears %d times in the read output, want 1:\n%s", n, text)
		}
	})
	t.Run("ship_state report", func(t *testing.T) {
		res, text := callRegisteredShipStateText(t, map[string]any{"action": "report"})
		if res.IsError {
			t.Fatalf("report: %s", text)
		}
		mustContain(t, text,
			"| Review | total 3 · fixed 1 · deferred 2 · unaccounted 0 |",
			"| Deferred | 2 |",
			"- [low] b.go:3 — open wont-fix finding (wont-fix)",
			"- [low] c.go:3 — open needs-direction finding (needs-direction)",
		)
		_, afterDeferred, _ := strings.Cut(text, "## Deferred\n")
		deferredSection, _, _ := strings.Cut(afterDeferred, "\n## ")
		if strings.Contains(deferredSection, "fixed by the fix pass") {
			t.Errorf("Deferred section lists the fixed finding:\n%s", deferredSection)
		}
	})
	t.Run("execute_state report", func(t *testing.T) {
		res, text := callRegisteredExecuteState(t, map[string]any{"action": "report"})
		if res.IsError {
			t.Fatalf("report: %s", text)
		}
		mustContain(t, text,
			"## deferredFindings[0]\n",
			"## deferredFindings[1]\n",
			"- title: open wont-fix finding\n",
			"- title: open needs-direction finding\n",
		)
		if strings.Contains(text, "## deferredFindings[2]") || strings.Contains(text, "fixed by the fix pass") {
			t.Errorf("execute report lists the fixed finding:\n%s", text)
		}
	})
}
