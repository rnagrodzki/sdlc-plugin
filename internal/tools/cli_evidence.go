package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// CLIEvidenceEntry is one CLI execution record in the evidence log.
type CLIEvidenceEntry struct {
	Timestamp  string `json:"ts"`
	Pipeline   string `json:"pipeline"`       // "ship" or "execute"
	Step       string `json:"step,omitempty"` // ship step name
	Wave       *int   `json:"wave,omitempty"` // execute wave number
	Branch     string `json:"branch"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exitCode"`
	OutputHead string `json:"outputHead"` // first ~500 chars of output
}

// cliEvidencePath returns the path to the CLI evidence JSONL file.
func cliEvidencePath(root string) string {
	return filepath.Join(root, paths.DataDir, "evidence", "cli-executions.jsonl")
}

// appendCLIEvidence appends one JSONL line to .sdlc-v2/evidence/cli-executions.jsonl.
// Creates directory and file if absent. Append-only, never truncated by pipeline lifecycle.
func appendCLIEvidence(root string, entry CLIEvidenceEntry) error {
	path := cliEvidencePath(root)

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("cli evidence: mkdir: %w", err)
	}

	// Marshal entry to JSON
	b, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("cli evidence: marshal: %w", err)
	}

	// Append with newline
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("cli evidence: open: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli evidence: write: %w", err)
	}

	return nil
}

// readRecentCLIEvidence reads the last N entries from the JSONL file.
// Returns empty slice if file doesn't exist. Reads the entire file and returns
// the last N entries in order.
func readRecentCLIEvidence(root string, n int) ([]CLIEvidenceEntry, error) {
	path := cliEvidencePath(root)

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []CLIEvidenceEntry{}, nil
		}
		return nil, fmt.Errorf("cli evidence: read: %w", err)
	}

	var entries []CLIEvidenceEntry
	lines := bytes.Split(b, []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var entry CLIEvidenceEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, entry)
	}

	// Return last n entries
	if len(entries) <= n {
		return entries, nil
	}
	return entries[len(entries)-n:], nil
}

// readCLIEvidenceInWindow reads entries from the JSONL file matching the
// given branch whose timestamp is at or after since. Unlike
// readRecentCLIEvidence (which returns the last N entries regardless of
// branch), this filters by branch and time window so concurrent runs on
// other branches sharing the same JSONL file don't pollute the result.
// Timestamps are RFC3339 (time.Now().UTC().Format(time.RFC3339)), which
// sort lexicographically, so string comparison is sufficient. Returns an
// empty slice (never nil) and a nil error when the file is missing or
// empty, or when nothing matches. Entries are returned in file order.
func readCLIEvidenceInWindow(root, branch, since string) ([]CLIEvidenceEntry, error) {
	path := cliEvidencePath(root)

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []CLIEvidenceEntry{}, nil
		}
		return nil, fmt.Errorf("cli evidence: read: %w", err)
	}

	entries := []CLIEvidenceEntry{}
	lines := bytes.Split(b, []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var entry CLIEvidenceEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip malformed lines
		}
		if entry.Branch != branch {
			continue
		}
		if entry.Timestamp < since {
			continue
		}
		entries = append(entries, entry)
	}

	return entries, nil
}
