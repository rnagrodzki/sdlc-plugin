package wave

import (
	"path"
	"strings"
)

// normalizeTaskFilePath converts backslashes to slashes, then applies
// path.Clean, so "./internal/x.go", "internal\\x.go", and
// "internal//x.go" all normalize to "internal/x.go" before comparison.
func normalizeTaskFilePath(p string) string {
	return path.Clean(strings.ReplaceAll(p, "\\", "/"))
}

// ResolveTaskForFile returns the single task ID in filesByTask whose file
// list contains rel, or "" when no task or more than one task claims it.
//
// rel and every recorded file are both normalized before comparison:
// backslashes become slashes, then path.Clean collapses "./", "//" and
// trailing separators. Nothing validates the strings written into
// planned[].files, so "./internal/x.go" is a shape a real manifest can
// carry; matching it here is the only place a miss would be noticed.
//
// Ambiguity resolves to "" rather than to a guess: stamping the wrong task
// would reset the wrong stall clock.
func ResolveTaskForFile(filesByTask map[string][]string, rel string) string {
	if len(filesByTask) == 0 {
		return ""
	}

	target := normalizeTaskFilePath(rel)

	match := ""
	matches := 0
	for taskID, files := range filesByTask {
		for _, f := range files {
			if normalizeTaskFilePath(f) == target {
				match = taskID
				matches++
				break
			}
		}
	}

	if matches != 1 {
		return ""
	}
	return match
}
