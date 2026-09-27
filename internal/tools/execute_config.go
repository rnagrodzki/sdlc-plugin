package tools

import (
	"fmt"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// ---------------------------------------------------------------------------
// Helper: execPipelineAuto
//
// execPipelineAuto is stateless with respect to execute's own run state: it
// only cross-reads another tool's (ship's) state file for this branch. It
// has no execute-state precondition, so it can run before init creates the
// execution state file (init itself is the first caller) and can also be
// called by resolve-config, which runs even earlier, in the execute skill's
// Step 0 before any state file exists.
// ---------------------------------------------------------------------------

// execPipelineAuto cross-reads the branch's ship state for flags.auto.
// Returns (nil, false, nil) when no ship state exists — that is the normal
// standalone case, not an error. A genuine I/O or JSON-parse failure
// returns (nil, false, warnings) so the caller can surface why the
// auto-forward did not happen.
//
// The found ship state is also returned to the caller (rather than only the
// derived bool) because init has a second, independent use for it: resolving
// this run's wave-timeout/wave-interval defaults from
// flags.executeWaveTimeout/executeWaveInterval. Returning it here means both
// use sites share the single state.Find call instead of each issuing its
// own.
func execPipelineAuto(root, branch string) (shipSt *state.State, auto bool, warnings []string) {
	shipSt, shipErr := state.Find(root, "ship", branch)
	if shipErr != nil {
		warnings = append(warnings, fmt.Sprintf("ship state unreadable: %s", shipErr.Error()))
		return nil, false, warnings
	}
	if shipSt != nil {
		if flags, ok := shipSt.Data["flags"].(map[string]any); ok {
			if a, ok := flags["auto"].(bool); ok && a {
				auto = true
			}
		}
	}
	return shipSt, auto, warnings
}
