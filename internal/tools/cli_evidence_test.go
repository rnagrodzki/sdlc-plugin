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
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Pipeline:   "ship",
		Step:       "commit",
		Branch:     "main",
		Command:    "git commit -m \"test\"",
		ExitCode:   0,
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

// TestReadRecentCLIEvidence_SkipsMalformedLines confirms a corrupted/partial
// JSONL line (e.g. from a crashed write) is silently skipped while
// well-formed entries on either side of it are still parsed.
func TestReadRecentCLIEvidence_SkipsMalformedLines(t *testing.T) {
	root := t.TempDir()

	path := filepath.Join(root, ".sdlc-v2", "evidence", "cli-executions.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	good1 := `{"ts":"2026-09-11T00:00:00Z","pipeline":"ship","step":"commit","branch":"main","command":"git commit","exitCode":0,"outputHead":"ok1"}`
	malformed := `{"ts":"2026-09-11T00:00:01Z", not valid json`
	good2 := `{"ts":"2026-09-11T00:00:02Z","pipeline":"ship","step":"review","branch":"main","command":"gh pr create","exitCode":0,"outputHead":"ok2"}`

	content := good1 + "\n" + malformed + "\n" + good2 + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture failed: %v", err)
	}

	entries, err := readRecentCLIEvidence(root, 10)
	if err != nil {
		t.Fatalf("readRecentCLIEvidence failed: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (malformed line skipped), got %d: %+v", len(entries), entries)
	}
	if entries[0].OutputHead != "ok1" || entries[1].OutputHead != "ok2" {
		t.Errorf("unexpected entries around skipped malformed line: %+v", entries)
	}
}

// TestReadCLIEvidenceInWindow_FiltersByBranchAndTime confirms the window
// filter matches the contract example: entries from other branches and
// entries before `since` are excluded, while matching entries preserve
// file order.
func TestReadCLIEvidenceInWindow_FiltersByBranchAndTime(t *testing.T) {
	root := t.TempDir()

	entries := []CLIEvidenceEntry{
		{
			Timestamp:  "2026-09-12T10:00:00Z",
			Pipeline:   "ship",
			Step:       "commit",
			Branch:     "main",
			Command:    "git commit",
			ExitCode:   0,
			OutputHead: "match",
		},
		{
			Timestamp:  "2026-09-12T10:01:00Z",
			Pipeline:   "ship",
			Step:       "commit",
			Branch:     "feat/x",
			Command:    "git commit",
			ExitCode:   1,
			OutputHead: "wrong branch",
		},
		{
			Timestamp:  "2026-09-12T09:00:00Z",
			Pipeline:   "ship",
			Step:       "review",
			Branch:     "main",
			Command:    "gh pr create",
			ExitCode:   0,
			OutputHead: "before since",
		},
	}

	for _, e := range entries {
		if err := appendCLIEvidence(root, e); err != nil {
			t.Fatalf("appendCLIEvidence failed: %v", err)
		}
	}

	got, err := readCLIEvidenceInWindow(root, "main", "2026-09-12T09:30:00Z", 1000)
	if err != nil {
		t.Fatalf("readCLIEvidenceInWindow failed: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 matching entry, got %d: %+v", len(got), got)
	}
	if got[0].OutputHead != "match" {
		t.Fatalf("expected the 'main' branch entry at/after since, got %+v", got[0])
	}
}

// TestReadCLIEvidenceInWindow_PreservesFileOrder confirms multiple matches
// come back in the order they appear in the file.
func TestReadCLIEvidenceInWindow_PreservesFileOrder(t *testing.T) {
	root := t.TempDir()

	entries := []CLIEvidenceEntry{
		{Timestamp: "2026-09-12T10:00:00Z", Branch: "main", Command: "first"},
		{Timestamp: "2026-09-12T10:01:00Z", Branch: "other", Command: "skipped"},
		{Timestamp: "2026-09-12T10:02:00Z", Branch: "main", Command: "second"},
		{Timestamp: "2026-09-12T10:03:00Z", Branch: "main", Command: "third"},
	}

	for _, e := range entries {
		if err := appendCLIEvidence(root, e); err != nil {
			t.Fatalf("appendCLIEvidence failed: %v", err)
		}
	}

	got, err := readCLIEvidenceInWindow(root, "main", "2026-09-12T00:00:00Z", 1000)
	if err != nil {
		t.Fatalf("readCLIEvidenceInWindow failed: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 matching entries, got %d: %+v", len(got), got)
	}
	wantOrder := []string{"first", "second", "third"}
	for i, w := range wantOrder {
		if got[i].Command != w {
			t.Errorf("entry %d: expected command %q, got %q", i, w, got[i].Command)
		}
	}
}

// TestReadCLIEvidenceInWindow_MissingFile confirms a missing evidence file
// returns an empty (non-nil) slice and no error.
func TestReadCLIEvidenceInWindow_MissingFile(t *testing.T) {
	root := t.TempDir()

	got, err := readCLIEvidenceInWindow(root, "main", "2026-09-12T00:00:00Z", 1000)
	if err != nil {
		t.Fatalf("readCLIEvidenceInWindow failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 entries for missing file, got %d", len(got))
	}
}

// TestReadCLIEvidenceInWindow_EmptyFile confirms an existing-but-empty
// evidence file returns an empty (non-nil) slice and no error.
func TestReadCLIEvidenceInWindow_EmptyFile(t *testing.T) {
	root := t.TempDir()

	path := filepath.Join(root, ".sdlc-v2", "evidence", "cli-executions.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("write empty fixture failed: %v", err)
	}

	got, err := readCLIEvidenceInWindow(root, "main", "2026-09-12T00:00:00Z", 1000)
	if err != nil {
		t.Fatalf("readCLIEvidenceInWindow failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 entries for empty file, got %d", len(got))
	}
}

// TestReadCLIEvidenceInWindow_CapReturnsTail confirms that when more entries
// match than the cap n, only the last n (tail) are returned.
func TestReadCLIEvidenceInWindow_CapReturnsTail(t *testing.T) {
	root := t.TempDir()

	entries := []CLIEvidenceEntry{
		{Timestamp: "2026-09-12T10:00:00Z", Branch: "main", Command: "first"},
		{Timestamp: "2026-09-12T10:01:00Z", Branch: "main", Command: "second"},
		{Timestamp: "2026-09-12T10:02:00Z", Branch: "main", Command: "third"},
		{Timestamp: "2026-09-12T10:03:00Z", Branch: "main", Command: "fourth"},
		{Timestamp: "2026-09-12T10:04:00Z", Branch: "main", Command: "fifth"},
	}

	for _, e := range entries {
		if err := appendCLIEvidence(root, e); err != nil {
			t.Fatalf("appendCLIEvidence failed: %v", err)
		}
	}

	got, err := readCLIEvidenceInWindow(root, "main", "2026-09-12T00:00:00Z", 3)
	if err != nil {
		t.Fatalf("readCLIEvidenceInWindow failed: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 entries (capped), got %d: %+v", len(got), got)
	}
	// Should be the last 3 (tail)
	wantOrder := []string{"third", "fourth", "fifth"}
	for i, w := range wantOrder {
		if got[i].Command != w {
			t.Errorf("entry %d: expected command %q, got %q", i, w, got[i].Command)
		}
	}
}
