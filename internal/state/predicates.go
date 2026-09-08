package state

// ---------------------------------------------------------------------------
// Pipeline predicates
// ---------------------------------------------------------------------------
// These are the single shared implementation for hooks and tools.
// Tasks 34, 35 (state tools) and Tasks 37-39 (hooks) must import these
// predicates — never re-implement them.

// PipelineAdvancingResult mirrors the JS { advancing, step, index } return.
type PipelineAdvancingResult struct {
	// Advancing is true when a step is in_progress, OR a non-terminal pending
	// step remains and either nothing failed or that pending step is the
	// terminal "cleanup" step.
	Advancing bool

	// Step is the map representing the active step (in_progress if present,
	// else the first pending step); nil when not advancing.
	Step map[string]any

	// Index is the index of Step in the steps slice, or -1.
	Index int
}

// PipelineAdvancing determines whether the pipeline described by data is
// currently advancing (has an actionable step). Mirrors
// src:scripts/lib/state.js:848-870 exactly.
//
// Pure: no I/O, no mutation, cannot panic on nil/malformed input.
func PipelineAdvancing(data map[string]any) PipelineAdvancingResult {
	empty := PipelineAdvancingResult{Advancing: false, Step: nil, Index: -1}

	if data == nil {
		return empty
	}

	stepsRaw, ok := data["steps"]
	if !ok {
		return empty
	}
	stepsSlice, ok := stepsRaw.([]any)
	if !ok {
		return empty
	}

	// Find in_progress step.
	inProgressIndex := -1
	var inProgress map[string]any
	failed := false

	for i, raw := range stepsSlice {
		s, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		status, _ := s["status"].(string)
		if status == "in_progress" && inProgressIndex < 0 {
			inProgressIndex = i
			inProgress = s
		}
		if status == "failed" {
			failed = true
		}
	}

	// Find first pending step. "Pending" = status is NOT completed, skipped,
	// or failed — matching the JS findIndex exactly.
	pendingIndex := -1
	var pending map[string]any
	for i, raw := range stepsSlice {
		s, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		status, _ := s["status"].(string)
		if status == "completed" || status == "skipped" || status == "failed" {
			continue
		}
		pendingIndex = i
		pending = s
		break
	}

	pendingIsCleanup := false
	if pending != nil {
		name, _ := pending["name"].(string)
		pendingIsCleanup = name == "cleanup"
	}

	advancing := inProgress != nil || (pending != nil && (!failed || pendingIsCleanup))

	if !advancing {
		return empty
	}

	if inProgress != nil {
		return PipelineAdvancingResult{Advancing: true, Step: inProgress, Index: inProgressIndex}
	}
	return PipelineAdvancingResult{Advancing: true, Step: pending, Index: pendingIndex}
}

// HookEnforcementResult mirrors the JS { allowed, reason } return.
type HookEnforcementResult struct {
	Allowed bool
	Reason  string
}

// HookEnforcementAllowed determines whether the session identified by
// sessionID is allowed to enforce (inject continuation reminders for) the
// pipeline described by data. Mirrors src:scripts/lib/state.js:935-941.
//
// Pure: no I/O, no mutation, cannot panic on nil/malformed input.
func HookEnforcementAllowed(data map[string]any, sessionID string) HookEnforcementResult {
	var stateSessionID string
	if data != nil {
		if v, ok := data["sessionId"].(string); ok && v != "" {
			stateSessionID = v
		}
	}

	if stateSessionID == "" {
		return HookEnforcementResult{Allowed: false, Reason: "state sessionId absent"}
	}
	if sessionID == "" {
		return HookEnforcementResult{Allowed: false, Reason: "payload session_id absent"}
	}
	if stateSessionID != sessionID {
		return HookEnforcementResult{Allowed: false, Reason: "session id mismatch"}
	}
	return HookEnforcementResult{Allowed: true, Reason: "session id match"}
}
