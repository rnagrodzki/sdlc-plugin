//go:build integration

package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestIntegration_ShipStateReport_WrittenResult calls the registered
// ship_state report action with detail.write true and pins the rendered short
// result: the path of the written file, the counts, guardrailHits with one
// hit, the one-line display emitted with no fence, and the next line.
func TestIntegration_ShipStateReport_WrittenResult(t *testing.T) {
	dir := shipStateIntegrationRepo(t, shipReportBranch)
	writeFile(t, filepath.Join(dir, ".sdlc-v2", "config.toml"), "")
	createExecState(t, dir, shipReportBranch, map[string]any{
		"branch":     shipReportBranch,
		"startedAt":  "2025-06-15T10:01:00Z",
		"totalTasks": 1,
		"guardrailDecisions": []any{
			map[string]any{"decideType": "guardrail", "id": "no-secrets"},
		},
	})
	createShipReportState(t, dir, map[string]any{
		"steps": shipReportSteps("completed"),
		"healing": map[string]any{"fixed": []any{
			map[string]any{"origin": "local-review", "severity": "high", "file": "a.go", "line": 3, "title": "fix a"},
		}},
		"deferredFindings": []any{
			map[string]any{"severity": "low", "file": "b.go", "title": "later", "reason": "wont-fix"},
		},
	})

	res, text := callRegisteredShipStateText(t, map[string]any{
		"action": "report",
		"detail": map[string]any{"branch": shipReportBranch, "write": true, "format": "json"},
	})
	if res.IsError {
		t.Fatalf("report returned a tool error:\n%s", text)
	}
	for _, want := range []string{
		"# ship_state — ok",
		"**Next:** " + shipReportNextWritten,
		"- format: json",
		"- stepCount: 2",
		"- fixedCount: 1",
		"- deferredCount: 1",
		"- issueCount: 0",
		"- written: true",
		"- guardrailHits:\n  - no-secrets",
		"- display: Ship run 20250615T100000 on feat/ship-report: 2/2 steps completed, 1 findings fixed, 1 deferred, 1 guardrail hits.\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "```") {
		t.Errorf("output holds a code fence, want the display line verbatim:\n%s", text)
	}
	m := regexp.MustCompile(`(?m)^- path: (\S+)$`).FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("output has no path line:\n%s", text)
	}
	if !strings.Contains(m[1], filepath.Join(".sdlc-v2", "reports", "ship-")) || !strings.HasSuffix(m[1], ".json") {
		t.Errorf("path = %q, want a ship-*.json file under .sdlc-v2/reports/", m[1])
	}
	if _, err := os.Stat(m[1]); err != nil {
		t.Errorf("written report file: %v", err)
	}
}
