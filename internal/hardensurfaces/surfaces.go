// Package hardensurfaces exposes the static catalogue of surfaces the harden
// pipeline can inspect and propose improvements for.  Each entry matches a
// loader in the prepare pipeline; the IDs are stable and referenced by the
// harden orchestrator's per-surface proposals.
//
// Ported from scripts/lib/harden-surfaces.js.  Only the metadata list is
// exported here; the runtime loaders (loadGuardrails, loadReviewDimensions,
// etc.) belong to the tool layer (Task 30 scope).
package hardensurfaces

// Surface describes a single hardening surface.
type Surface struct {
	ID          string
	Label       string
	Description string
}

// List returns the static catalogue of hardening surfaces.
func List() []Surface {
	return []Surface{
		{
			ID:          "plan-guardrails",
			Label:       "Plan guardrails",
			Description: "Guardrail rules from the plan section of .sdlc-v2/config.json",
		},
		{
			ID:          "execute-guardrails",
			Label:       "Execute guardrails",
			Description: "Guardrail rules from the execute section of .sdlc-v2/config.json",
		},
		{
			ID:          "review-dimensions",
			Label:       "Review dimensions",
			Description: "Custom review dimension definitions from .sdlc-v2/review-dimensions/*.md",
		},
		{
			ID:          "copilot-instructions",
			Label:       "Copilot instructions",
			Description: "GitHub Copilot instruction files from .github/instructions/*.instructions.md",
		},
		{
			ID:          "error-report-skill",
			Label:       "Error report skill",
			Description: "Sibling error-report skill REFERENCE.md template",
		},
	}
}
