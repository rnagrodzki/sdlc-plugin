// Package budget provides byte-budget-aware wave dispatch allocation.
//
// It ports scripts/lib/dispatch-budget.js (computeWaveBudget) from the
// Node.js SDLC utilities. The API is reshaped to Go idioms: instead of
// returning a count, Allocate returns the actual items that fit.
package budget

import (
	"math"
	"sort"
)

// DefaultMaxInputBytes is the conservative byte ceiling for model context.
// Computed as: 200K tokens * 4 bytes/token * 75% = 600,000 bytes.
const DefaultMaxInputBytes = 600_000

// Item represents a task candidate with a byte size.
type Item struct {
	Label string // human-readable identifier
	Size  int    // byte size of the fact-sheet
}

// Budget holds the configuration for wave budget allocation.
type Budget struct {
	TemplateBytes         int // bytes for prompt template scaffolding
	GuardrailsBytes       int // bytes for rendered guardrails block
	PriorWaveContextBytes int // bytes for prior-wave context summary
	MaxInputBytes         int // override model limit; 0 = DefaultMaxInputBytes
	TotalRemainingTasks   int // used for static-cap lookup; 0 = len(items)
}

// staticCapTable defines the wave-size cap per remaining-task range.
// Mirrors STATIC_CAP_TABLE from dispatch-budget.js.
var staticCapTable = []struct {
	Min, Max, Cap int
}{
	{1, 3, math.MaxInt},
	{4, 8, 4},
	{9, 15, 5},
	{16, math.MaxInt, 6},
}

// StaticCap returns the max concurrent tasks for a given total remaining count.
func StaticCap(totalRemainingTasks int) int {
	for _, row := range staticCapTable {
		if totalRemainingTasks >= row.Min && totalRemainingTasks <= row.Max {
			return row.Cap
		}
	}
	return 6 // defensive fallback (unreachable with current table)
}

// Allocate computes which items fit within the byte budget for a wave
// dispatch. It returns the subset of items that can run concurrently,
// sorted ascending by size (smallest first, to maximize count within
// the budget).
//
// The algorithm mirrors computeWaveBudget from dispatch-budget.js:
//  1. Subtract fixed overhead (template + guardrails + prior-wave context).
//  2. Apply the static cap for the total remaining task count.
//  3. Sort candidate items ascending by size.
//  4. Greedily pack items until the budget or static cap is exhausted.
//  5. Guarantee at least one item if any candidates exist.
func Allocate(items []Item, cfg Budget) ([]Item, error) {
	if len(items) == 0 {
		return nil, nil
	}

	maxInput := cfg.MaxInputBytes
	if maxInput <= 0 {
		maxInput = DefaultMaxInputBytes
	}

	totalRemaining := cfg.TotalRemainingTasks
	if totalRemaining <= 0 {
		totalRemaining = len(items)
	}

	capVal := StaticCap(totalRemaining)
	effectiveCap := capVal
	if effectiveCap > len(items) {
		effectiveCap = len(items)
	}

	fixedBytes := cfg.TemplateBytes + cfg.GuardrailsBytes + cfg.PriorWaveContextBytes

	// Sort ascending by size to pack as many as possible.
	sorted := make([]Item, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Size < sorted[j].Size
	})

	available := maxInput - fixedBytes
	if available <= 0 {
		// No budget — return smallest item (minimum viable).
		return []Item{sorted[0]}, nil
	}

	var result []Item
	usedBytes := 0

	for _, item := range sorted {
		if len(result) >= effectiveCap {
			break
		}
		if usedBytes+item.Size <= available {
			result = append(result, item)
			usedBytes += item.Size
		} else {
			break
		}
	}

	// Guarantee at least 1 if there are candidates.
	if len(result) == 0 {
		result = []Item{sorted[0]}
	}

	return result, nil
}
