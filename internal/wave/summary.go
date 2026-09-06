package wave

import "fmt"

// ValidStatuses is the closed set of task statuses accepted in a wave
// summary. Mirrors wave-summary.js's VALID_STATUSES exactly; these enum
// values are frozen — the execute-plan skill text references them directly,
// so they must not drift.
var ValidStatuses = map[string]bool{
	"DONE":               true,
	"DONE_WITH_CONCERNS": true,
	"NEEDS_CONTEXT":      true,
	"BLOCKED":            true,
	"FAILED":             true,
}

// StepResult is one task's reported outcome, as parsed from a wave
// summary's per-task entry (mirrors wave-summary.js's task entry, narrowed
// to the field this package validates).
type StepResult struct {
	ID     string
	Status string
}

// Summary is the aggregate result of Summarize.
type Summary struct {
	// Results is the input, passed through unchanged.
	Results []StepResult
	// Violations lists one message per StepResult whose Status is not in
	// ValidStatuses. Empty means every result validated cleanly.
	Violations []string
}

// Summarize validates each result's Status against the closed ValidStatuses
// enum, mirroring wave-summary.js's validateTaskEntry status check. Invalid
// statuses are accumulated in Summary.Violations rather than raising an
// error, matching the source's non-throwing, violation-accumulating
// validation style; Summarize's own error return is reserved for malformed
// input.
func Summarize(results []StepResult) (*Summary, error) {
	s := &Summary{Results: results}
	for _, r := range results {
		if !ValidStatuses[r.Status] {
			s.Violations = append(s.Violations, fmt.Sprintf("task %q: status %q not in bounded enum", r.ID, r.Status))
		}
	}
	return s, nil
}
