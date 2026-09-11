package tools

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendCLIEvidence_CreatesDirectoryAndFile(t *testing.T) {
	root := t.TempDir()

	entry := CLIEvidenceEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Pipeline:  "ship",
		Step:      "commit",
		Branch:    "main",
		Command:   "git commit -m \"test\"",
		ExitCode:  0,
		OutputHead: "[main abc1234] test",
	}

	err := appendCLIEvidence(root, entry)
	if err != nil {
		t.Fatalf("appendCLIEvidence failed: %v", err)
	}

	path := filepath.Join(root, ".sdlc-v2", "evidence", "cli-executions.jsonl")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("evidence file not created: %v", err)
	}
}

func TestAppendCLIEvidence_AppendsMultipleLines(t *testing.T) {
	root := t.TempDir()

	entries := []CLIEvidenceEntry{
		{
			Timestamp:  "2026-09-11T00:00:00Z",
			Pipeline:   "ship",
			Step:       "commit",
			Branch:     "main",
			Command:    "git commit -m \"test1\"",
			ExitCode:   0,
			OutputHead: "[main abc1234] test1",
		},
		{
			Timestamp:  "2026-09-11T00:00:01Z",
			Pipeline:   "ship",
			Step:       "review",
			Branch:     "main",
			Command:    "gh pr create --draft",
			ExitCode:   0,
			OutputHead: "Created PR #123",
		},
		{
			Timestamp:  "2026-09-11T00:00:02Z",
			Pipeline:   "execute",
			Wave:       func() *int { v := 1; return &v }(),
			Branch:     "feature",
			Command:    "npm test",
			ExitCode:   0,
			OutputHead: "PASS  tests/unit.test.js",
		},
	}

	for _, entry := range entries {
		if err := appendCLIEvidence(root, entry); err != nil {
			t.Fatalf("appendCLIEvidence failed: %v", err)
		}
	}

	path := filepath.Join(root, ".sdlc-v2", "evidence", "cli-executions.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read evidence file: %v", err)
	}

	// Count lines
	lines := bytes.Split(b, []byte("\n"))
	// Last line is empty due to trailing newline
	var validLines int
	for _, line := range lines {
		if len(line) > 0 {
			validLines++
		}
	}

	if validLines != 3 {
		t.Fatalf("expected 3 valid JSONL lines, got %d", validLines)
	}

	// Parse and verify content
	var count int
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var entry CLIEvidenceEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("failed to parse JSONL line: %v", err)
		}
		count++
	}

	if count != 3 {
		t.Fatalf("expected 3 entries, got %d", count)
	}
}

func TestReadRecentCLIEvidence_EmptyFile(t *testing.T) {
	root := t.TempDir()

	entries, err := readRecentCLIEvidence(root, 10)
	if err != nil {
		t.Fatalf("readRecentCLIEvidence failed: %v", err)
	}

	if len(entries) != 0 {
		t.Fatalf("expected 0 entries for missing file, got %d", len(entries))
	}
}

func TestReadRecentCLIEvidence_ReturnsLastN(t *testing.T) {
	root := t.TempDir()

	// Append 5 entries
	for i := 0; i < 5; i++ {
		entry := CLIEvidenceEntry{
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
			Pipeline:   "ship",
			Step:       "commit",
			Branch:     "main",
			Command:    "git commit",
			ExitCode:   0,
			OutputHead: "test",
		}
		if err := appendCLIEvidence(root, entry); err != nil {
			t.Fatalf("appendCLIEvidence failed: %v", err)
		}
	}

	// Read last 3
	entries, err := readRecentCLIEvidence(root, 3)
	if err != nil {
		t.Fatalf("readRecentCLIEvidence failed: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
}

func TestReadRecentCLIEvidence_ReturnsAllIfLessThanN(t *testing.T) {
	root := t.TempDir()

	// Append 2 entries
	for i := 0; i < 2; i++ {
		entry := CLIEvidenceEntry{
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
			Pipeline:   "ship",
			Step:       "commit",
			Branch:     "main",
			Command:    "git commit",
			ExitCode:   0,
			OutputHead: "test",
		}
		if err := appendCLIEvidence(root, entry); err != nil {
			t.Fatalf("appendCLIEvidence failed: %v", err)
		}
	}

	// Read last 10
	entries, err := readRecentCLIEvidence(root, 10)
	if err != nil {
		t.Fatalf("readRecentCLIEvidence failed: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (less than requested 10), got %d", len(entries))
	}
}
