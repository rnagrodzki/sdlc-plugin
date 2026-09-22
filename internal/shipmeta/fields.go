// Package shipmeta ports the ship-config field constants and pipeline-step
// todo metadata from scripts/lib/ship-fields.js and scripts/lib/ship-todos.js
// (sdlc-utilities plugin) for reuse by Go-native SDLC tooling.
package shipmeta

// MaxWaveTimeoutSeconds is the hard ceiling for ship.executeWaveTimeout
// (R57, R-WAVE-DEADLINE). It is the single enforcement point restated by the
// `maximum` on ship.executeWaveTimeout in schemas/sdlc-local.schema.json —
// kept in sync by test (see shipmeta_test.go), not by generation.
//
// Mirrors MAX_WAVE_TIMEOUT_SECONDS in scripts/lib/ship-fields.js:
//
//	const MAX_WAVE_TIMEOUT_SECONDS = 3600; // Monitor.timeout_ms caps at 3600000 ms
const MaxWaveTimeoutSeconds = 3600

// CanonicalSteps lists the pipeline step names that may appear in
// ship.steps[] / --steps, in canonical order. Mirrors CANONICAL_STEPS in
// scripts/lib/ship-fields.js. "cleanup" is a synthetic terminal step
// appended unconditionally by the pipeline and is never user-configurable —
// see ReservedSteps.
var CanonicalSteps = []string{
	"execute", "commit", "review", "verify-openspec",
	"archive-openspec", "pr", "verify-pipeline", "await-remote-review",
	"learnings-commit",
}

// ReservedSteps lists step names that must never appear in ship.steps[] or
// --steps because the pipeline appends them unconditionally. Mirrors
// RESERVED_STEPS in scripts/lib/ship-fields.js.
var ReservedSteps = []string{"cleanup"}

// ValidSteps is the accepted value set for ship.steps[] / --steps. Mirrors
// VALID_STEPS in scripts/lib/ship-fields.js (an alias of CANONICAL_STEPS).
var ValidSteps = CanonicalSteps

// shipBuiltInDefaults holds the runtime fallback values used when neither a
// CLI flag nor the project's ship config supplies one.
//
// The ShipBuiltInDefaults table below is the AUTHORITY for these values —
// the JS BUILT_IN_DEFAULTS it was ported from no longer exists in this repo.
// Two places restate them for readers and must not drift:
// plugins/sdlc/skills/ship/config-format.md (the Field Reference table, its
// Full Example JSON block and the surrounding prose) and
// plugins/sdlc/templates/local.toml (what /setup copies into a new project).
// internal/config's TestShippedReviewThresholdDefaultsAgree pins the
// reviewThreshold value across this table, the template, the setup wizard
// and the docs; the remaining fields are unguarded, so change them together
// by hand.
type shipBuiltInDefaultsT struct {
	Steps                       []string
	Bump                        string
	Draft                       bool
	Auto                        bool
	ReviewThreshold             string
	Rebase                      bool
	VerifyPipelineTimeout       int
	VerifyPipelineInterval      int
	VerifyPipelineMaxIterations int
	AwaitRemoteReviewTimeout    int
	AwaitRemoteReviewInterval   int
	AwaitRemoteReviewers        []string
	ExecuteWaveTimeout          int
	ExecuteWaveInterval         int
}

// ShipBuiltInDefaults holds the runtime fallback values (used when neither a
// CLI flag nor the ship config supplies a value). See shipBuiltInDefaultsT
// above for which files restate these values.
// NOTE: Steps here is the "balanced" preset step list (narrower than
// CanonicalSteps) — an intentional divergence between the questionnaire
// default and the runtime fallback, kept from the JS config-migrations this
// table was ported from.
var ShipBuiltInDefaults = shipBuiltInDefaultsT{
	Steps:                       []string{"execute", "commit", "review", "archive-openspec", "pr", "learnings-commit"},
	Bump:                        "patch",
	Draft:                       false,
	Auto:                        false,
	ReviewThreshold:             "low",
	Rebase:                      true,
	VerifyPipelineTimeout:       1200,
	VerifyPipelineInterval:      60,
	VerifyPipelineMaxIterations: 3,
	AwaitRemoteReviewTimeout:    600,
	AwaitRemoteReviewInterval:   60,
	AwaitRemoteReviewers:        []string{"copilot"},
	ExecuteWaveTimeout:          1800,
	ExecuteWaveInterval:         60,
}

// ShipStateStep is one entry of the ship-state step scaffold, mirroring
// cmdInit's steps[] in scripts/state/ship.js.
type ShipStateStep struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Condition string `json:"condition,omitempty"`
	// Kind classifies the step as "tracked" (has its own begin-step/
	// complete-step lifecycle via ship_state — see TrackedShipSteps) or
	// "inline" (recorded via the generic "decide" action, no dedicated
	// lifecycle). Only set by InitialShipStepsFromConfig — InitialShipSteps'
	// fixed 7-entry scaffold leaves this empty (omitempty) to keep its output
	// byte-identical for existing callers/tests.
	Kind string `json:"kind,omitempty"`
}

// TrackedShipSteps is the step-name set that gets a begin-step/complete-step
// tracked lifecycle entry — exactly the 6 names InitialShipSteps seeds.
// Every other known step name (see shipmeta_test.go's SubstepMap) is
// "inline": recorded via the generic "decide" action instead. See
// reference.md's tracked-vs-inline distinction.
var TrackedShipSteps = []string{
	"execute", "commit", "review", "received-review", "commit-fixes", "pr",
}

// IsTrackedShipStep reports whether name is one of TrackedShipSteps.
func IsTrackedShipStep(name string) bool {
	for _, s := range TrackedShipSteps {
		if s == name {
			return true
		}
	}
	return false
}

// InitialShipSteps returns the fixed 6-entry step scaffold used to
// initialize ship state, in source order. Every entry starts at status
// "pending". Independent of ship.steps[]/flags.steps (the pipeline
// configuration) — this scaffold is always the same regardless of config.
//
// Kept behaviorally and byte-shape identical to its pre-existing callers
// (internal/tools/ship_state.go's "init" action): InitialShipStepsFromConfig
// is the config-driven scaffold new callers (ship_prepare) should use.
func InitialShipSteps() []ShipStateStep {
	return []ShipStateStep{
		{Name: "execute", Status: "pending"},
		{Name: "commit", Status: "pending"},
		{Name: "review", Status: "pending"},
		{Name: "received-review", Status: "pending", Condition: "if critical/high findings"},
		{Name: "commit-fixes", Status: "pending", Condition: "if received-review made changes"},
		{Name: "pr", Status: "pending"},
	}
}

// InitialShipStepsFromConfig returns one ShipStateStep per entry in steps,
// in the given order, each starting at status "pending" and classified via
// Kind ("tracked" for TrackedShipSteps members, "inline" otherwise —
// including any config-sourced name outside ValidSteps, which reaches here
// only as a warning, not an error; see ship_prepare's step-name validation).
// Unlike InitialShipSteps, this scaffold tracks exactly the configured
// steps — nothing more, nothing less — so a pipeline with N configured
// steps seeds N entries.
func InitialShipStepsFromConfig(steps []string) []ShipStateStep {
	out := make([]ShipStateStep, 0, len(steps))
	for _, name := range steps {
		kind := "inline"
		if IsTrackedShipStep(name) {
			kind = "tracked"
		}
		out = append(out, ShipStateStep{Name: name, Status: "pending", Kind: kind})
	}
	return out
}
