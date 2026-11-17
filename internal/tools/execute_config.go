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

// execShipCross is what execShipCrossRead reads from the ship state of a
// branch.
type execShipCross struct {
	pipelineAuto   bool     // flags.auto of the ship state
	shipDispatched bool     // ship dispatched the current execute run
	warnings       []string // why a value could not be read
}

// execShipCrossRead reads the ship state of branch once. With no branch it
// reads nothing and returns the zero value.
func execShipCrossRead(root, branch string) execShipCross {
	if branch == "" {
		return execShipCross{}
	}
	shipSt, pipelineAuto, warnings := execPipelineAuto(root, branch)
	dispatched, stepWarnings := shipExecuteStepRunning(shipSt)
	return execShipCross{
		pipelineAuto:   pipelineAuto,
		shipDispatched: dispatched,
		warnings:       append(warnings, stepWarnings...),
	}
}

// shipExecuteStepRunning reports whether ship dispatched the current execute
// run: the ship state is not completed and its execute step is in_progress.
// A nil state (no ship state for the branch) is false. A stale ship state with
// an execute step left in_progress also returns true; that is accepted.
// A pipelineStatus or execute step status that is not a string gives false
// and one warning that names the key and the type found.
func shipExecuteStepRunning(shipSt *state.State) (bool, []string) {
	if shipSt == nil {
		return false, nil
	}
	if raw := shipSt.Data["pipelineStatus"]; raw != nil {
		status, isString := raw.(string)
		if !isString {
			return false, []string{fmt.Sprintf("ship state pipelineStatus has type %T, expected string; shipDispatched is false", raw)}
		}
		if status == PipelineCompleted {
			return false, nil
		}
	}
	step := shipFindStepEntry(shipSt.Data, dashboardShipStepExecute)
	if step == nil {
		return false, nil
	}
	raw := step["status"]
	status, isString := raw.(string)
	if raw != nil && !isString {
		return false, []string{fmt.Sprintf("ship state status of step %s has type %T, expected string; shipDispatched is false", dashboardShipStepExecute, raw)}
	}
	return status == StepInProgress, nil
}
