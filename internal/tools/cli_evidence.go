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

// maxCLIEvidenceInWindow is the default cap for readCLIEvidenceInWindow,
// mirroring readRecentCLIEvidence's caller-provided cap pattern.
const maxCLIEvidenceInWindow = 200

// maxEvidenceFileBytes bounds each evidence JSONL file (Task 10). When
// appending would land in a file already at or over this cap, the file is
// rotated to a ".1" sibling (best-effort, overwriting any previous one)
// before the new line is written to a fresh file — a single-generation
// size-cap rotation, not a full logrotate scheme. This became necessary
// once PostToolUse hooks started appending automatically on every Bash/MCP
// tool call instead of only on explicit, comparatively rare log-cli calls:
// cli-executions.jsonl was previously unbounded.
const maxEvidenceFileBytes = 5 * 1024 * 1024 // 5 MiB

// appendJSONLBounded appends one JSON-marshaled entry as a line to path,
// creating the parent directory if absent, and rotating path to path+".1"
// first when the file is already at or over maxEvidenceFileBytes. Rotation
// failure is not fatal — the append still proceeds against the (possibly
// still-oversized) existing file rather than dropping the entry.
func appendJSONLBounded(path string, entry any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("evidence: mkdir: %w", err)
	}

	if info, statErr := os.Stat(path); statErr == nil && info.Size() >= maxEvidenceFileBytes {
		_ = os.Rename(path, path+".1")
	}

	b, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("evidence: marshal: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("evidence: open: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("evidence: write: %w", err)
	}

	return nil
}

// appendCLIEvidence appends one JSONL line to .sdlc-v2/evidence/cli-executions.jsonl.
// Creates directory and file if absent. Append-only, bounded by
// appendJSONLBounded (Task 10) — never truncated by pipeline lifecycle.
//
// This does NOT dedup: readRecentCLIEvidence tests below (pre-dating Task
// 10) call this in a tight loop with identical branch/command/exitCode and
// expect every call to land as its own line — a caller-side dedup guard
// belongs in the specific caller that needs it (see internal/hooks'
// isDuplicateCLIEvidence, used only by the automatic pipeline-continue
// hook), not centrally here, or those legitimate repeated-identical-command
// sequences would be silently dropped.
func appendCLIEvidence(root string, entry CLIEvidenceEntry) error {
	return appendJSONLBounded(cliEvidencePath(root), entry)
}

// readAllCLIEvidence reads and parses all entries from the JSONL file.
// Returns (nil, nil) when the file does not exist. Malformed lines are
// silently skipped.
func readAllCLIEvidence(root string) ([]CLIEvidenceEntry, error) {
	path := cliEvidencePath(root)

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
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
	return entries, nil
}

// readRecentCLIEvidence reads the last n entries from the JSONL file.
// Returns empty slice if file doesn't exist. Reads the entire file and returns
// the last n entries in order.
func readRecentCLIEvidence(root string, n int) ([]CLIEvidenceEntry, error) {
	entries, err := readAllCLIEvidence(root)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		return []CLIEvidenceEntry{}, nil
	}

	// Return last n entries
	if len(entries) <= n {
		return entries, nil
	}
	return entries[len(entries)-n:], nil
}

// readCLIEvidenceInWindow reads entries from the JSONL file matching the
// given branch whose timestamp is at or after since. Unlike
// readRecentCLIEvidence (which returns the last n entries regardless of
// branch), this filters by branch and time window so concurrent runs on
// other branches sharing the same JSONL file don't pollute the result.
// Timestamps are RFC3339 (time.Now().UTC().Format(time.RFC3339)), which
// sort lexicographically, so string comparison is sufficient. Returns the
// last n matching entries (tail) in file order. Returns an empty slice
// (never nil) and a nil error when the file is missing or empty, or when
// nothing matches.
func readCLIEvidenceInWindow(root, branch, since string, n int) ([]CLIEvidenceEntry, error) {
	entries, err := readAllCLIEvidence(root)
	if err != nil {
		return nil, err
	}

	var matched []CLIEvidenceEntry
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
		return []CLIEvidenceEntry{}, nil
	}

	// Cap to last n entries (tail)
	if len(matched) > n {
		matched = matched[len(matched)-n:]
	}

	return matched, nil
}

// ---------------------------------------------------------------------------
// Exported wrappers (Task 10) -- internal/hooks needs to call these
// directly from its PostToolUse handlers (pipeline_continue.go's automatic
// Bash CLI recording, mcp_invocation_record.go's MCP invocation recording),
// but Go visibility makes the unexported functions above (and
// execute_state.go's execLastRecordedWave) uncallable cross-package. Each
// wrapper is a pure one-line delegate; the unexported functions themselves
// are unchanged.
// ---------------------------------------------------------------------------

// AppendCLIEvidence delegates to appendCLIEvidence for internal/hooks.
func AppendCLIEvidence(root string, entry CLIEvidenceEntry) error {
	return appendCLIEvidence(root, entry)
}

// LastCLIEvidenceEntry returns the most recently appended CLI evidence
// entry (across all branches/pipelines — the file is a flat append log),
// and false if the file is absent or empty. Used by internal/hooks' dedup
// guard to detect an immediate duplicate recording of the same Bash
// execution.
func LastCLIEvidenceEntry(root string) (CLIEvidenceEntry, bool, error) {
	entries, err := readRecentCLIEvidence(root, 1)
	if err != nil || len(entries) == 0 {
		return CLIEvidenceEntry{}, false, err
	}
	return entries[len(entries)-1], true, nil
}

// ExecLastRecordedWaveNumber returns the highest wave number recorded in an
// execute state's data["waves"], or nil when there are none. Delegates to
// execLastRecordedWave (execute_state.go) so internal/hooks' automatic CLI
// evidence recording can populate CLIEvidenceEntry.Wave for an execute
// pipeline the same way explicit log-cli callers already do.
func ExecLastRecordedWaveNumber(data map[string]any) *int {
	w := execLastRecordedWave(data)
	if w == nil {
		return nil
	}
	n := execToInt(w["number"])
	return &n
}
