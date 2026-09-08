package version

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// prependChangelog reads an existing CHANGELOG.md (or creates it) and
// prepends a new section with the version header and notes.
//
// Format:
//
//	## [version] - YYYY-MM-DD
//
//	notes
func prependChangelog(path, ver, notes string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	date := time.Now().Format("2006-01-02")
	header := fmt.Sprintf("## [%s] - %s", ver, date)

	// Build the new section.
	section := header + "\n\n" + strings.TrimRight(notes, "\n") + "\n"

	var content string
	if len(existing) == 0 {
		// New changelog: add a top-level heading.
		content = "# Changelog\n\n" + section
	} else {
		// Prepend after the first line (assumed to be "# Changelog" or
		// similar top-level heading). If the file doesn't start with a
		// heading, just prepend.
		old := string(existing)
		if strings.HasPrefix(old, "# ") {
			// Find end of first line.
			idx := strings.Index(old, "\n")
			if idx < 0 {
				content = old + "\n\n" + section
			} else {
				// Insert after the heading and a blank line.
				rest := strings.TrimLeft(old[idx:], "\n")
				content = old[:idx] + "\n\n" + section + "\n" + rest
			}
		} else {
			content = section + "\n" + old
		}
	}

	return os.WriteFile(path, []byte(content), 0o644)
}
