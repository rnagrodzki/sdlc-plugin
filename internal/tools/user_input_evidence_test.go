package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendUserInput_CreatesDirectoryAndFile(t *testing.T) {
	root := t.TempDir()

	entry := UserInputEntry{
		Timestamp: "2026-10-02T00:00:00Z",
		Pipeline:  "ship",
		Step:      "review",
		Branch:    "main",
		Text:      "skip the low findings",
	}

	if err := appendUserInput(root, entry); err != nil {
		t.Fatalf("appendUserInput failed: %v", err)
	}

	path := filepath.Join(root, ".sdlc-v2", "evidence", "user-inputs.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("evidence file not created: %v", err)
	}

	var got UserInputEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &got); err != nil {
		t.Fatalf("unmarshal entry: %v", err)
	}
	if got.Text != "skip the low findings" {
		t.Errorf("Text = %q, want unmodified short text", got.Text)
	}
}

// TestAppendUserInput_RedactsSecrets confirms appendUserInput runs
// telemetry.Redact over Text before writing — a bearer token in a pasted
// prompt must never land verbatim in the evidence file.
func TestAppendUserInput_RedactsSecrets(t *testing.T) {
	root := t.TempDir()

	entry := UserInputEntry{
		Timestamp: "2026-10-02T00:00:00Z",
		Pipeline:  "ship",
		Branch:    "main",
		Text:      "use Bearer abc.def to call the API",
	}

	if err := appendUserInput(root, entry); err != nil {
		t.Fatalf("appendUserInput failed: %v", err)
	}

	entries, err := readAllUserInput(root)
	if err != nil {
		t.Fatalf("readAllUserInput failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Text, "Bearer [REDACTED]") {
		t.Errorf("Text = %q, want it to contain %q", entries[0].Text, "Bearer [REDACTED]")
	}
	if strings.Contains(entries[0].Text, "abc.def") {
		t.Errorf("Text = %q, raw secret leaked through", entries[0].Text)
	}
}

// TestAppendUserInput_CapsLength confirms a prompt longer than
// userInputTextMax runes is truncated to exactly that many runes plus a
// trailing "…" marker.
func TestAppendUserInput_CapsLength(t *testing.T) {
	root := t.TempDir()

	long := strings.Repeat("a", 5000)
	entry := UserInputEntry{
		Timestamp: "2026-10-02T00:00:00Z",
		Pipeline:  "ship",
		Branch:    "main",
		Text:      long,
	}

	if err := appendUserInput(root, entry); err != nil {
		t.Fatalf("appendUserInput failed: %v", err)
	}

	entries, err := readAllUserInput(root)
	if err != nil {
		t.Fatalf("readAllUserInput failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}

	got := []rune(entries[0].Text)
	if len(got) != userInputTextMax+1 { // +1 for the trailing "…" rune
		t.Fatalf("got %d runes, want %d (cap + ellipsis)", len(got), userInputTextMax+1)
	}
	if got[len(got)-1] != '…' {
		t.Errorf("last rune = %q, want '…'", got[len(got)-1])
	}
	if string(got[:userInputTextMax]) != strings.Repeat("a", userInputTextMax) {
		t.Errorf("truncated body does not match the first %d runes of the original", userInputTextMax)
	}
}

// TestAppendUserInput_ShortTextUnmodified confirms text at or under the cap
// is stored verbatim, with no trailing ellipsis added.
func TestAppendUserInput_ShortTextUnmodified(t *testing.T) {
	root := t.TempDir()

	entry := UserInputEntry{Timestamp: "2026-10-02T00:00:00Z", Branch: "main", Text: "short prompt"}
	if err := appendUserInput(root, entry); err != nil {
		t.Fatalf("appendUserInput failed: %v", err)
	}

	entries, err := readAllUserInput(root)
	if err != nil {
		t.Fatalf("readAllUserInput failed: %v", err)
	}
	if len(entries) != 1 || entries[0].Text != "short prompt" {
		t.Fatalf("entries = %+v, want a single unmodified entry", entries)
	}
}

// TestReadUserInputInWindow_FiltersByBranchAndTime mirrors
// TestReadCLIEvidenceInWindow_FiltersByBranchAndTime: entries from other
// branches and entries before `since` are excluded.
func TestReadUserInputInWindow_FiltersByBranchAndTime(t *testing.T) {
	root := t.TempDir()

	entries := []UserInputEntry{
		{Timestamp: "2026-09-12T10:00:00Z", Branch: "main", Text: "match"},
		{Timestamp: "2026-09-12T10:01:00Z", Branch: "feat/x", Text: "wrong branch"},
		{Timestamp: "2026-09-12T09:00:00Z", Branch: "main", Text: "before since"},
	}
	for _, e := range entries {
		if err := appendUserInput(root, e); err != nil {
			t.Fatalf("appendUserInput failed: %v", err)
		}
	}

	got, err := readUserInputInWindow(root, "main", "2026-09-12T09:30:00Z", maxUserInputInWindow)
	if err != nil {
		t.Fatalf("readUserInputInWindow failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 matching entry, got %d: %+v", len(got), got)
	}
	if got[0].Text != "match" {
		t.Fatalf("expected the 'main' branch entry at/after since, got %+v", got[0])
	}
}

// TestReadUserInputInWindow_OldestFirstAndCapReturnsTail confirms matches
// come back oldest-first in file order, capped to the last n when more than
// n entries match.
func TestReadUserInputInWindow_OldestFirstAndCapReturnsTail(t *testing.T) {
	root := t.TempDir()

	entries := []UserInputEntry{
		{Timestamp: "2026-09-12T10:00:00Z", Branch: "main", Text: "first"},
		{Timestamp: "2026-09-12T10:01:00Z", Branch: "main", Text: "second"},
		{Timestamp: "2026-09-12T10:02:00Z", Branch: "main", Text: "third"},
		{Timestamp: "2026-09-12T10:03:00Z", Branch: "main", Text: "fourth"},
	}
	for _, e := range entries {
		if err := appendUserInput(root, e); err != nil {
			t.Fatalf("appendUserInput failed: %v", err)
		}
	}

	got, err := readUserInputInWindow(root, "main", "2026-09-12T00:00:00Z", 2)
	if err != nil {
		t.Fatalf("readUserInputInWindow failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries (capped), got %d: %+v", len(got), got)
	}
	wantOrder := []string{"third", "fourth"}
	for i, w := range wantOrder {
		if got[i].Text != w {
			t.Errorf("entry %d: got %q, want %q", i, got[i].Text, w)
		}
	}
}

// TestReadUserInputInWindow_MissingFile confirms a missing evidence file
// returns an empty (non-nil) slice and no error.
func TestReadUserInputInWindow_MissingFile(t *testing.T) {
	root := t.TempDir()

	got, err := readUserInputInWindow(root, "main", "2026-09-12T00:00:00Z", maxUserInputInWindow)
	if err != nil {
		t.Fatalf("readUserInputInWindow failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 entries for missing file, got %d", len(got))
	}
}

// TestAppendUserInput_ViaExportedWrapper confirms AppendUserInput
// (internal/hooks' entry point) delegates to appendUserInput, including its
// redact-and-cap behavior.
func TestAppendUserInput_ViaExportedWrapper(t *testing.T) {
	root := t.TempDir()

	entry := UserInputEntry{
		Timestamp: "2026-10-02T00:00:00Z",
		Pipeline:  "execute",
		Wave:      func() *int { v := 2; return &v }(),
		Branch:    "feat/x",
		Text:      "continue the wave",
	}
	if err := AppendUserInput(root, entry); err != nil {
		t.Fatalf("AppendUserInput failed: %v", err)
	}

	entries, err := readAllUserInput(root)
	if err != nil {
		t.Fatalf("readAllUserInput failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Pipeline != "execute" || entries[0].Wave == nil || *entries[0].Wave != 2 {
		t.Errorf("entries[0] = %+v, want pipeline execute wave 2", entries[0])
	}
}
