package tools

import (
	"path/filepath"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/telemetry"
)

// UserInputEntry is one prompt the user typed while a ship or execute run
// was active on the branch.
type UserInputEntry struct {
	Timestamp string `json:"ts"`
	Pipeline  string `json:"pipeline"`       // "ship" or "execute"
	Step      string `json:"step,omitempty"` // ship step name
	Wave      *int   `json:"wave,omitempty"` // execute wave number
	Branch    string `json:"branch"`
	Text      string `json:"text"` // redacted, max userInputTextMax runes
}

const (
	// userInputTextMax bounds each stored prompt to this many runes, with a
	// trailing "…" marking truncation when the prompt is longer — mirrors
	// CLIEvidenceEntry.OutputHead's "first ~500 chars of output" cap, scaled
	// up since a user-typed prompt (not command output) is the payload here.
	userInputTextMax = 2000

	// maxUserInputInWindow is the default cap for readUserInputInWindow,
	// mirroring maxCLIEvidenceInWindow's caller-provided cap pattern.
	maxUserInputInWindow = 100
)

// userInputPath returns the path to the user-input evidence JSONL file.
func userInputPath(root string) string {
	return filepath.Join(root, paths.DataDir, paths.EvidenceSubdir, "user-inputs.jsonl")
}

// truncateRunes cuts s to at most max runes, appending a trailing "…" when
// truncation actually happens. Operates on runes (not bytes) so a multi-byte
// character is never split mid-sequence.
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// appendUserInput redacts entry.Text (telemetry.Redact) and caps it to
// userInputTextMax runes (truncateRunes), then appends it through
// appendJSONLBounded (cli_evidence.go) — the same size-capped,
// rotate-on-overflow JSONL append path CLI evidence uses.
func appendUserInput(root string, entry UserInputEntry) error {
	entry.Text = truncateRunes(telemetry.Redact(entry.Text), userInputTextMax)
	return appendJSONLBounded(userInputPath(root), entry)
}

// readAllUserInput reads and parses every entry from the user-input
// evidence JSONL file. Returns (nil, nil) when the file does not exist.
// Malformed lines are silently skipped.
func readAllUserInput(root string) ([]UserInputEntry, error) {
	return readJSONLEntries[UserInputEntry](userInputPath(root), "user input evidence")
}

// readUserInputInWindow reads entries from the user-input evidence JSONL
// file matching the given branch whose timestamp is at or after since. Same
// filter and tail rule as readCLIEvidenceInWindow (cli_evidence.go): entries
// are matched oldest-first in file order, then capped to the last n (tail)
// when more than n match. Returns an empty slice (never nil) and a nil error
// when the file is missing or empty, or when nothing matches.
func readUserInputInWindow(root, branch, since string, n int) ([]UserInputEntry, error) {
	entries, err := readAllUserInput(root)
	if err != nil {
		return nil, err
	}

	var matched []UserInputEntry
	for _, entry := range entries {
		if entry.Branch != branch {
			continue
		}
		if entry.Timestamp < since {
			continue
		}
		matched = append(matched, entry)
	}

	if matched == nil {
		return []UserInputEntry{}, nil
	}

	if len(matched) > n {
		matched = matched[len(matched)-n:]
	}

	return matched, nil
}

// ---------------------------------------------------------------------------
// Exported wrapper -- internal/hooks needs to call this directly from its
// UserPromptSubmit handler (record_user_input.go), but Go visibility makes
// the unexported appendUserInput above uncallable cross-package.
// ---------------------------------------------------------------------------

// AppendUserInput delegates to appendUserInput for internal/hooks.
func AppendUserInput(root string, entry UserInputEntry) error {
	return appendUserInput(root, entry)
}
