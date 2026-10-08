// Package shipmeta ports the ship-config field constants and pipeline-step
// todo metadata from scripts/lib/ship-fields.js and scripts/lib/ship-todos.js
// (sdlc-utilities plugin) for reuse by Go-native SDLC tooling.
package shipmeta

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
)

// MaxWaveTimeoutSeconds is the hard ceiling for ship.executeWaveTimeout
// (R57, R-WAVE-DEADLINE). It is the single enforcement point restated by the
// `maximum` on ship.executeWaveTimeout in schemas/sdlc-local.schema.json —
// kept in sync by test (see shipmeta_test.go), not by generation.
//
// Mirrors MAX_WAVE_TIMEOUT_SECONDS in scripts/lib/ship-fields.js:
//
//	const MAX_WAVE_TIMEOUT_SECONDS = 3600; // Monitor.timeout_ms caps at 3600000 ms
const MaxWaveTimeoutSeconds = 3600

// CanonicalSteps lists the pipeline step names that may appear as keys of
// the ship.steps / ship.quick tables or in --steps, in the fixed pipeline
// order (harden runs after archive-openspec). The plugin owns this
// order: OrderSteps sorts every resolved step list into it, whatever order
// the user wrote. Restated (and pinned by test) in
// internal/setupmeta.CanonicalSteps and the step enums of
// plugins/sdlc/schemas/sdlc-local.schema.json and ship-state.schema.json.
// "cleanup" is a synthetic terminal step appended unconditionally by the
// pipeline and is never user-configurable — see ReservedSteps.
var CanonicalSteps = []string{
	"execute", "commit", "review", "verify-openspec", "archive-openspec",
	"harden", "pr", "verify-pipeline", "await-remote-review",
	"learnings-commit",
}

// ReservedSteps lists step names that must never appear in ship.steps or
// --steps because the pipeline appends them unconditionally. Mirrors
// RESERVED_STEPS in scripts/lib/ship-fields.js.
var ReservedSteps = []string{"cleanup"}

// ValidSteps is the accepted value set for ship.steps / --steps. Mirrors
// VALID_STEPS in scripts/lib/ship-fields.js (an alias of CANONICAL_STEPS).
var ValidSteps = CanonicalSteps

// OrderSteps returns names in CanonicalSteps order. Names not in
// CanonicalSteps come last, in input order, so callers can still report
// them. A nil input returns an empty, non-nil slice.
func OrderSteps(names []string) []string {
	out := append([]string{}, names...)
	rank := func(name string) int {
		if i := slices.Index(CanonicalSteps, name); i >= 0 {
			return i
		}
		return len(CanonicalSteps)
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	return out
}

// ResolveStepTable reads a [ship.steps]-style table (step name → bool).
// It reads the keys in sorted order, so the result is stable.
// A step not in the table is on when defaults contains it.
// A conditional step name (IsConditionalShipStep) set to true is kept,
// so the existing conditional-step check rejects it. Set to false, it is
// ignored with no problem.
// Any other key outside CanonicalSteps gives an "unknown key" problem,
// whatever its value. It returns the enabled steps via OrderSteps, plus
// one problem per non-bool value or unknown key. The label argument
// ("ship.steps" or "ship.quick") names the table in each problem.
func ResolveStepTable(table map[string]any, defaults []string, label string) (steps []string, problems []string) {
	enabled := map[string]bool{}
	var names []string
	enable := func(name string) {
		if !enabled[name] {
			enabled[name] = true
			names = append(names, name)
		}
	}
	for _, name := range defaults {
		enable(name)
	}

	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		known := slices.Contains(CanonicalSteps, key)
		if !known && !IsConditionalShipStep(key) {
			problems = append(problems, fmt.Sprintf(
				"%s has an unknown key %q. Every key below the [%s] header belongs to that table. Move other [ship] keys above the header.",
				label, key, label))
			continue
		}
		on, isBool := table[key].(bool)
		if !isBool {
			problems = append(problems, fmt.Sprintf(
				"%s.%s must be true or false, got %s. Set it to true or false in [%s].",
				label, key, formatStepValue(table[key]), label))
			continue
		}
		if on {
			enable(key)
		} else {
			delete(enabled, key)
		}
	}

	kept := make([]string, 0, len(names))
	for _, name := range names {
		if enabled[name] {
			kept = append(kept, name)
		}
	}
	return OrderSteps(kept), problems
}

// formatStepValue renders a non-bool step table value for a problem
// message: strings are quoted, other values print as-is.
func formatStepValue(v any) string {
	if s, ok := v.(string); ok {
		return strconv.Quote(s)
	}
	return fmt.Sprintf("%v", v)
}

// shipBuiltInDefaults holds the runtime fallback values used when neither a
// CLI flag nor the project's ship config supplies one.
//
// The ShipBuiltInDefaults table below is the AUTHORITY for these values —
// the JS BUILT_IN_DEFAULTS it was ported from no longer exists in this repo.
// Two places restate them for readers and must not drift:
// plugins/sdlc/skills/ship/config-format.md (the Field Reference table, its
// Full Example JSON block and the surrounding prose) and
// plugins/sdlc/templates/local.toml (what /setup copies into a new project).
// internal/tools' TestLocalTemplate_ShipCommentedDefaultsMatchBuiltIns pins
// every field of this table against its commented example in the template.
// internal/config's TestShippedReviewThresholdDefaultsAgree also pins the
// reviewThreshold value across the setup wizard and the docs. No test pins
// the other fields in config-format.md, so change those docs by hand.
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
	ReviewThreshold:             "info",
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

// IsConditionalShipStep reports whether name is a tracked step that is not
// user-configurable ("received-review", "commit-fixes"): the pipeline
// dispatches it itself when review findings need fixing, so it must never
// be enabled in ship.steps / ship.quick or listed in --steps. Derived from
// TrackedShipSteps and ValidSteps so the set cannot drift from either.
func IsConditionalShipStep(name string) bool {
	return IsTrackedShipStep(name) && !slices.Contains(ValidSteps, name)
}

// InitialShipSteps returns the fixed 6-entry step scaffold used to
// initialize ship state, in source order. Every entry starts at status
// "pending". Independent of ship.steps/flags.steps (the pipeline
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
// only as a warning, not an error; see ship_prepare's step-name validation.
// The conditional steps — IsConditionalShipStep — never reach here: they
// are rejected with an error from every source).
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
