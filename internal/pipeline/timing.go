// Package pipeline provides timing utilities shared by ETA and elapsed-time
// displays across the SDLC pipeline (ship, review, plan, etc).
package pipeline

import (
	"fmt"
	"time"
)

// HumanWaitSteps identifies step keys that represent waiting on a human
// (e.g. remote code review) rather than automated pipeline work. Elapsed
// time for these keys reflects human latency, not something worth
// recording or projecting, so TimingsStore excludes them from both Record
// and Estimate.
var HumanWaitSteps = map[string]bool{
	"await-remote-review": true,
}

// Estimate is a duration estimate derived from historical samples for a
// pipeline step key.
type Estimate struct {
	Seconds int    // estimated duration in seconds
	Samples int    // number of samples the estimate is based on
	Basis   string // human-readable description of how the estimate was derived
}

// Humanize renders a duration in a compact, human-readable form:
// under 60 seconds as "42s", under 1 hour as "6m 12s", and 1 hour or more
// as "1h 03m" (seconds are dropped once the value reaches the hour scale).
// Negative durations are treated as zero.
func Humanize(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second) / time.Second)

	if total < 60 {
		return fmt.Sprintf("%ds", total)
	}
	if total < 3600 {
		m := total / 60
		s := total % 60
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	h := total / 3600
	m := (total % 3600) / 60
	return fmt.Sprintf("%dh %02dm", h, m)
}

// Duration returns the elapsed time between startedAt and completedAt,
// both formatted as RFC3339 timestamps. ok is false if either timestamp
// fails to parse, in which case the returned duration is zero.
func Duration(startedAt, completedAt string) (time.Duration, bool) {
	start, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return 0, false
	}
	end, err := time.Parse(time.RFC3339, completedAt)
	if err != nil {
		return 0, false
	}
	return end.Sub(start), true
}

// IdleGap returns the elapsed time between the completion of a previous
// step (prevCompletedAt) and the start of the next step (nextStartedAt),
// both formatted as RFC3339 timestamps. ok is false if either timestamp
// fails to parse.
func IdleGap(prevCompletedAt, nextStartedAt string) (time.Duration, bool) {
	return Duration(prevCompletedAt, nextStartedAt)
}
