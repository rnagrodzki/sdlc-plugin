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
	"execute", "commit", "review", "version", "verify-openspec",
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

// shipBuiltInDefaults holds ship.js's BUILT_IN_DEFAULTS runtime fallback
// values. Mirrors BUILT_IN_DEFAULTS in scripts/lib/ship-fields.js.
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

// ShipBuiltInDefaults holds ship.js's BUILT_IN_DEFAULTS runtime fallback
// values (used when neither CLI flag nor ship config supplies a value).
// NOTE: Steps here is PRESET_TO_STEPS.balanced (narrower than
// CanonicalSteps) — an intentional, source-documented divergence between the
// questionnaire default and the runtime fallback (see
// scripts/lib/config-migrations.js).
var ShipBuiltInDefaults = shipBuiltInDefaultsT{
	Steps:                       []string{"execute", "commit", "review", "archive-openspec", "pr", "learnings-commit"},
	Bump:                        "patch",
	Draft:                       false,
	Auto:                        false,
	ReviewThreshold:             "high",
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
// tracked lifecycle entry — exactly the 7 names InitialShipSteps seeds.
// Every other known step name (see shipmeta_test.go's SubstepMap) is
// "inline": recorded via the generic "decide" action instead. Mirrors
// reference.md's "only 7 of the 13 known step names get a tracked entry".
var TrackedShipSteps = []string{
	"execute", "commit", "review", "received-review", "commit-fixes", "version", "pr",
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

// InitialShipSteps returns the fixed 7-entry step scaffold used to
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
		{Name: "version", Status: "pending"},
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
