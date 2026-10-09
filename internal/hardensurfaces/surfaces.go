// Package hardensurfaces exposes the static catalogue of surfaces the harden
// pipeline can inspect and propose improvements for.  Each entry matches a
// loader in the prepare pipeline; the IDs are stable and referenced by the
// harden orchestrator's per-surface proposals.
//
// Ported from scripts/lib/harden-surfaces.js.  Only the metadata list is
// exported here; the runtime loaders (loadGuardrails, loadReviewDimensions,
// etc.) belong to the tool layer.
package hardensurfaces

// Surface describes a single hardening surface.
type Surface struct {
	ID          string
	Label       string
	Description string
	// Proposal is true when harden writes proposals for the surface. A
	// surface with Proposal false is a read-only input.
	Proposal bool
}

// List returns the static catalogue of hardening surfaces.
func List() []Surface {
	return []Surface{
		{
			ID:          "plan-guardrails",
			Label:       "Plan guardrails",
			Description: "Guardrail rules from the plan section of .sdlc-v2/config.toml",
			Proposal:    true,
		},
		{
			ID:          "execute-guardrails",
			Label:       "Execute guardrails",
			Description: "Guardrail rules from the execute section of .sdlc-v2/config.toml",
			Proposal:    true,
		},
		{
			ID:          "review-dimensions",
			Label:       "Review dimensions",
			Description: "Custom review dimension definitions from .sdlc-v2/review-dimensions/*.md",
			Proposal:    true,
		},
		{
			ID:          "copilot-instructions",
			Label:       "Copilot instructions",
			Description: "GitHub Copilot instruction files from .github/instructions/*.instructions.md",
			Proposal:    true,
		},
		{
			ID:          "error-report-skill",
			Label:       "Error report skill",
			Description: "The plugin's shipped error-report skill (skills/error-report/SKILL.md)",
		},
		{
			ID:          "skill-recommendation",
			Label:       "Skill recommendation",
			Description: "Recommend new skills/agents based on learnings and run stats",
		},
	}
}

// ProposalIDs returns the ids of the List surfaces with Proposal set, in List
// order: plan-guardrails, execute-guardrails, review-dimensions,
// copilot-instructions. Each call returns a new slice, so a caller may change
// it.
func ProposalIDs() []string {
	var ids []string
	for _, s := range List() {
		if s.Proposal {
			ids = append(ids, s.ID)
		}
	}
	return ids
}
