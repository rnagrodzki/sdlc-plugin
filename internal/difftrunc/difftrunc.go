// Package difftrunc provides file-aware diff truncation.
//
// It ports scripts/lib/diff-truncate.js (truncateDiff) from the Node.js
// SDLC utilities. The splitter function is injected so this package stays
// free of a direct git-splitting dependency (only the FileDiff type is
// imported).
package difftrunc

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
)

// DefaultDiffMaxBytes is the default cap for diff truncation,
// matching the historical value from commit.js (8 000 bytes).
const DefaultDiffMaxBytes = 8000

// Truncate performs file-aware diff truncation. It keeps whole-file slices
// within the byte budget (largest files first for maximum semantic signal)
// and appends a footer listing any omitted files.
//
// The split function is injected to keep this package free of a direct
// git-splitting dependency. Callers typically pass gitx.SplitDiffByFile.
//
// If max <= 0, DefaultDiffMaxBytes is used.
//
// Behavior mirrored from the Node.js truncateDiff:
//   - If the diff is within budget, it is returned unchanged.
//   - File chunks are sorted largest-first; at least one is always included.
//   - Smaller chunks that fit are still included even if a larger one was skipped.
//   - A footer lists the omitted files.
func Truncate(diff string, max int, split func(string) []gitx.FileDiff) string {
	if max <= 0 {
		max = DefaultDiffMaxBytes
	}

	if len(diff) <= max {
		return diff
	}

	chunks := split(diff)
	if len(chunks) == 0 {
		return diff
	}

	// Sort by content length descending (largest first = most signal).
	sorted := make([]gitx.FileDiff, len(chunks))
	copy(sorted, chunks)
	sort.SliceStable(sorted, func(i, j int) bool {
		return len(sorted[i].Content) > len(sorted[j].Content)
	})

	var included []string
	var truncatedFiles []string
	totalLen := 0

	for _, fd := range sorted {
		if len(included) == 0 {
			// Always include at least one file.
			included = append(included, fd.Content)
			totalLen += len(fd.Content)
		} else if totalLen+len(fd.Content) <= max {
			included = append(included, fd.Content)
			totalLen += len(fd.Content)
		} else {
			truncatedFiles = append(truncatedFiles, fd.Path)
		}
	}

	// Footer is always emitted when truncation was triggered, even if
	// truncatedFiles is empty (matches JS behavior).
	lines := []string{
		"# --- Truncated ---",
		fmt.Sprintf("# The following %d file(s) were omitted (see diffStat for summary):", len(truncatedFiles)),
	}
	for _, f := range truncatedFiles {
		lines = append(lines, fmt.Sprintf("# - %s", f))
	}
	footer := strings.Join(lines, "\n")

	return strings.Join(included, "") + "\n" + footer
}
