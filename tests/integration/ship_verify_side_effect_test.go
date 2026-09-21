//go:build integration

package integration

import (
	"slices"
	"strings"
	"testing"
)

func hasLine(body, want string) bool {
	return slices.Contains(strings.Split(body, "\n"), want)
}

// TestShipVerifySideEffect_RenderedFields drives ship_verify_side_effect
// through the real in-process MCP server and asserts on the rendered Markdown.
// Struct-level tests cannot catch a renderer that hides a field: an omitempty
// tag on ShipVerifySideEffectOut.Expected once made the "expected" line vanish
// whenever the caller passed no expected value.
func TestShipVerifySideEffect_RenderedFields(t *testing.T) {
	gitFixture(t, "feat/verify-side-effect")
	c := setupShipClient(t)

	t.Run("has side effect, no expected value renders (none)", func(t *testing.T) {
		res := callTool(t, c, "ship_verify_side_effect", map[string]any{"step": "commit", "expected": ""})
		if !res.OK {
			t.Fatalf("not ok: code=%s body=%s", res.Code, res.Body)
		}
		for _, line := range []string{"- step: commit", "- sideEffect: sha", "- landed: false", "- expected: (none)"} {
			if !hasLine(res.Body, line) {
				t.Errorf("missing line %q in body:\n%s", line, res.Body)
			}
		}
		if strings.Contains(res.Body, "null") {
			t.Errorf("body must not contain null:\n%s", res.Body)
		}
	})

	t.Run("has side effect, expected value is shown", func(t *testing.T) {
		const sha = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
		res := callTool(t, c, "ship_verify_side_effect", map[string]any{"step": "commit", "expected": sha})
		if !res.OK {
			t.Fatalf("not ok: code=%s body=%s", res.Code, res.Body)
		}
		for _, line := range []string{"- landed: false", "- expected: " + sha} {
			if !hasLine(res.Body, line) {
				t.Errorf("missing line %q in body:\n%s", line, res.Body)
			}
		}
	})

	t.Run("no side effect shows reason and hides sideEffect", func(t *testing.T) {
		res := callTool(t, c, "ship_verify_side_effect", map[string]any{"step": "review", "expected": ""})
		if !res.OK {
			t.Fatalf("not ok: code=%s body=%s", res.Code, res.Body)
		}
		for _, line := range []string{"- landed: true", "- reason: no-side-effect"} {
			if !hasLine(res.Body, line) {
				t.Errorf("missing line %q in body:\n%s", line, res.Body)
			}
		}
		if strings.Contains(res.Body, "- sideEffect:") {
			t.Errorf("sideEffect must be omitted when there is no side effect:\n%s", res.Body)
		}
	})
}
