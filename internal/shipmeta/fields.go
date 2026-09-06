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

// ShipStateStep is one entry of the fixed ship-state step scaffold,
// mirroring cmdInit's steps[] in scripts/state/ship.js.
type ShipStateStep struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Condition string `json:"condition,omitempty"`
}

// InitialShipSteps returns the fixed 7-entry step scaffold used to
// initialize ship state, in source order. Every entry starts at status
// "pending". Independent of ship.steps[]/flags.steps (the pipeline
// configuration) — this scaffold is always the same regardless of config.
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
