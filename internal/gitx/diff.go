package gitx

import (
	"regexp"
	"strings"
)

// FileDiff holds a single file's chunk from a unified diff.
type FileDiff struct {
	// Path is the file path from the b/ side of the diff header.
	Path string

	// Content is the raw diff content for this file, including
	// the "diff --git" header line and all hunks.
	Content string
}

// diffHeaderRe extracts the b-side path from a "diff --git" header line.
var diffHeaderRe = regexp.MustCompile(`^diff --git a/.+ b/(.+)`)

// SplitDiffByFile splits a raw unified diff string into per-file chunks,
// preserving the original content and ordering. Any preamble text before
// the first "diff --git" header is discarded (matching the source behavior
// in scripts/lib/git.js:splitDiffByFile).
//
// The implementation mirrors the JS regex split(/(?=^diff --git )/m) by
// scanning for "diff --git " at column 0 (start of line). Within diff
// hunks, body lines always start with +, -, space, or \, so a column-0
// prefix match is equivalent to the JS multiline lookahead.
func SplitDiffByFile(diff string) []FileDiff {
	if diff == "" {
		return nil
	}

	// Find all byte positions where "diff --git " starts at beginning of a line.
	var positions []int
	if strings.HasPrefix(diff, "diff --git ") {
		positions = append(positions, 0)
	}
	idx := 0
	for {
		pos := strings.Index(diff[idx:], "\ndiff --git ")
		if pos < 0 {
			break
		}
		positions = append(positions, idx+pos+1) // +1 to skip the \n
		idx = idx + pos + 1
	}

	if len(positions) == 0 {
		return nil
	}

	var result []FileDiff
	for i, start := range positions {
		var end int
		if i+1 < len(positions) {
			end = positions[i+1]
		} else {
			end = len(diff)
		}
		chunk := diff[start:end]
		m := diffHeaderRe.FindStringSubmatch(chunk)
		if m != nil {
			result = append(result, FileDiff{
				Path:    strings.TrimSpace(m[1]),
				Content: chunk,
			})
		}
	}

	return result
}
