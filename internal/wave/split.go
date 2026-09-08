// Package wave ports the wave-runner's split and summary utilities from
// scripts/lib/wave-split.js and scripts/lib/wave-summary.js: deterministic
// partitioning of an overflowing wave's dispatched task set, and validation
// of a wave summary's per-task status against a closed enum.
package wave

import (
	"errors"
	"fmt"
	"sort"
)

// MaxSplitDepth is the ceiling on wave split recursion depth: Split refuses
// to run once opt.SplitDepth has reached this value. Mirrors wave-split.js's
// default maxSplitDepth (scripts/lib/wave-split.js:50).
const MaxSplitDepth = 3

// Task identifies a single dispatched task within a wave, by task ID.
type Task string

// Wave is one half of a Split result: the task IDs assigned to it, and the
// depth at which it was produced.
type Wave struct {
	Tasks []Task
	Depth int
}

// SplitOptions configures Split.
type SplitOptions struct {
	// SplitDepth is the current recursion depth (0 = first split). Split
	// returns a *MaxSplitDepthExceededError once SplitDepth >= MaxSplitDepth.
	SplitDepth int
	// MissingIDs are the task IDs absent from the wave summary that
	// triggered this split. Diagnostic only, mirroring wave-split.js's
	// missingIds parameter — it does not affect the split boundary.
	MissingIDs []Task
}

// ErrMaxSplitDepthExceeded is wrapped into the error Split returns once the
// split ceiling has been reached.
var ErrMaxSplitDepthExceeded = errors.New("wave: max split depth exceeded")

// MaxSplitDepthExceededError mirrors wave-split.js's
// MaxSplitDepthExceededError: returned when a further split would exceed
// MaxSplitDepth. Recovery is a manual escalation step owned by the caller
// (e.g. AskUserQuestion with the set of unresolved task IDs).
type MaxSplitDepthExceededError struct {
	Depth         int
	MaxSplitDepth int
}

func (e *MaxSplitDepthExceededError) Error() string {
	return fmt.Sprintf(
		"MaxSplitDepthExceededError: splitDepth %d exceeds maxSplitDepth %d. "+
			"Manual escalation required — call AskUserQuestion with the set of unresolved task IDs.",
		e.Depth, e.MaxSplitDepth,
	)
}

// Unwrap lets callers use errors.Is(err, ErrMaxSplitDepthExceeded) as an
// alternative to errors.As against the concrete type.
func (e *MaxSplitDepthExceededError) Unwrap() error { return ErrMaxSplitDepthExceeded }

// Split partitions tasks — the full dispatched set of a CONTEXT_OVERFLOW
// wave, not just the missing IDs — into two roughly-equal halves for
// independent re-dispatch. It mirrors wave-split.js's splitWave(): the split
// is deterministic (tasks are sorted lexicographically before being divided
// at the midpoint, so the same {tasks, opt.SplitDepth} always yields the
// same partition), and it refuses to run once opt.SplitDepth has already
// reached MaxSplitDepth, returning a *MaxSplitDepthExceededError.
//
// Splitting the full dispatched set rather than only the missing IDs is
// deliberate: splitting just the missing ones risks re-overflow if
// dependencies were needed in-wave.
func Split(tasks []Task, opt SplitOptions) ([]Wave, error) {
	if opt.SplitDepth >= MaxSplitDepth {
		return nil, &MaxSplitDepthExceededError{Depth: opt.SplitDepth, MaxSplitDepth: MaxSplitDepth}
	}

	nextDepth := opt.SplitDepth + 1

	if len(tasks) == 0 {
		return []Wave{
			{Tasks: []Task{}, Depth: nextDepth},
			{Tasks: []Task{}, Depth: nextDepth},
		}, nil
	}

	if len(tasks) == 1 {
		// Cannot split a single task — put it in the first half, second
		// half is empty.
		return []Wave{
			{Tasks: []Task{tasks[0]}, Depth: nextDepth},
			{Tasks: []Task{}, Depth: nextDepth},
		}, nil
	}

	sorted := make([]Task, len(tasks))
	copy(sorted, tasks)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	mid := (len(sorted) + 1) / 2 // ceil(len/2), matching Math.ceil(sorted.length / 2)
	firstHalf := append([]Task{}, sorted[:mid]...)
	secondHalf := append([]Task{}, sorted[mid:]...)

	return []Wave{
		{Tasks: firstHalf, Depth: nextDepth},
		{Tasks: secondHalf, Depth: nextDepth},
	}, nil
}
