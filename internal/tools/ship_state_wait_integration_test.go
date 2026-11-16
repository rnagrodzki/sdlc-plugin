//go:build integration

package tools

import (
	"regexp"
	"strings"
	"testing"
)

// TestIntegration_ShipStateWait_Rendered calls the registered ship_state
// wait action on an in_progress step and pins the rendered result. The
// handler reads the real clock, so the waitingUntil line is pinned by its
// RFC 3339 UTC shape, and the next line must name that same time.
func TestIntegration_ShipStateWait_Rendered(t *testing.T) {
	shipStateIntegrationRepo(t, "feat/wait-int")
	if res, text := callRegisteredShipStateText(t, map[string]any{"action": "init"}); res.IsError {
		t.Fatalf("init returned a tool error:\n%s", text)
	}
	if res, text := callRegisteredShipStateText(t, map[string]any{"action": "begin-step", "step": "execute"}); res.IsError {
		t.Fatalf("begin-step returned a tool error:\n%s", text)
	}

	res, text := callRegisteredShipStateText(t, map[string]any{"action": "wait", "step": "execute", "detail": map[string]any{"seconds": 120}})
	if res.IsError {
		t.Fatalf("wait returned a tool error:\n%s", text)
	}
	if !strings.HasPrefix(text, "# ship_state — ok\n") {
		t.Errorf("first line is not the ok line:\n%s", text)
	}
	m := regexp.MustCompile(`(?m)^- waitingUntil: (\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z)$`).FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("output has no RFC 3339 UTC waitingUntil line:\n%s", text)
	}
	until := m[1]
	for _, want := range []string{
		"- step: execute",
		"- narration: Wait recorded for step 'execute' until " + until + ".",
		`**Next:** End the turn. The Stop hook does not count turn ends for step "execute" before ` + until + ". When the wait ends, continue the step.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
}
