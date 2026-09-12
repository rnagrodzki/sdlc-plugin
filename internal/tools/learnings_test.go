package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

func TestLearningsAppendCreatesFileWithHeader(t *testing.T) {
	root := t.TempDir()

	out, err := learningsLog(root, LearningsLogIn{Action: "append", Entry: "## 2026-09-07 — setup: first entry"})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if !out.OK || !out.Changed || out.Path != paths.DataDir+"/learnings/log.md" {
		t.Fatalf("unexpected output: %+v", out)
	}

	data, err := os.ReadFile(filepath.Join(root, paths.DataDir, "learnings", "log.md"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)
	if !strings.HasPrefix(content, "# SDLC Execution Learnings\n") {
		t.Fatalf("expected header, got %q", content)
	}
	if !strings.Contains(content, "## 2026-09-07 — setup: first entry") {
		t.Fatalf("expected entry present, got %q", content)
	}
}

func TestLearningsAppendAddsSecondEntryWithBlankLineSeparator(t *testing.T) {
	root := t.TempDir()

	if _, err := learningsLog(root, LearningsLogIn{Action: "append", Entry: "## first"}); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if _, err := learningsLog(root, LearningsLogIn{Action: "append", Entry: "## second"}); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, paths.DataDir, "learnings", "log.md"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "## first\n\n## second\n") {
		t.Fatalf("expected single blank line between entries, got %q", content)
	}
}

func TestLearningsAppendWithRunIDPrependsTag(t *testing.T) {
	root := t.TempDir()

	if _, err := learningsLog(root, LearningsLogIn{
		Action: "append",
		Entry:  "## entry",
		RunID:  "20260912T100024",
		Branch: "feat/ship-report-content-enrichment",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, paths.DataDir, "learnings", "log.md"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "<!-- sdlc:run=20260912T100024 branch=feat/ship-report-content-enrichment -->\n## entry") {
		t.Fatalf("expected tagged entry, got %q", content)
	}
}

func TestLearningsAppendWithoutRunIDUnchanged(t *testing.T) {
	root := t.TempDir()

	if _, err := learningsLog(root, LearningsLogIn{Action: "append", Entry: "## entry"}); err != nil {
		t.Fatalf("append: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, paths.DataDir, "learnings", "log.md"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "<!-- sdlc:run=") {
		t.Fatalf("expected no run tag when RunID is empty, got %q", content)
	}
	if !strings.Contains(content, "## entry") {
		t.Fatalf("expected entry present, got %q", content)
	}
}

func TestLearningsAppendRejectsEmptyEntry(t *testing.T) {
	root := t.TempDir()
	if _, err := learningsLog(root, LearningsLogIn{Action: "append", Entry: "   "}); err == nil {
		t.Fatal("expected error for empty entry, got nil")
	}
}

func TestLearningsReadMissingFileReturnsExistsFalse(t *testing.T) {
	root := t.TempDir()

	out, err := learningsLog(root, LearningsLogIn{Action: "read"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !out.OK || out.Exists || out.Content != "" {
		t.Fatalf("expected exists=false empty content, got %+v", out)
	}
}

func TestLearningsReadReturnsFullContent(t *testing.T) {
	root := t.TempDir()
	if _, err := learningsLog(root, LearningsLogIn{Action: "append", Entry: "## entry"}); err != nil {
		t.Fatalf("append: %v", err)
	}

	out, err := learningsLog(root, LearningsLogIn{Action: "read"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !out.Exists || !strings.Contains(out.Content, "## entry") {
		t.Fatalf("expected content to contain entry, got %+v", out)
	}
}

func TestLearningsReadTailLinesLimitsOutput(t *testing.T) {
	root := t.TempDir()
	if _, err := learningsLog(root, LearningsLogIn{Action: "append", Entry: "## one"}); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if _, err := learningsLog(root, LearningsLogIn{Action: "append", Entry: "## two"}); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	out, err := learningsLog(root, LearningsLogIn{Action: "read", TailLines: 1})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(out.Content, "## one") {
		t.Fatalf("expected tail to exclude earlier entry, got %q", out.Content)
	}
	if !strings.Contains(out.Content, "## two") {
		t.Fatalf("expected tail to include last line, got %q", out.Content)
	}
}

func TestLearningsLogUnknownActionRejected(t *testing.T) {
	root := t.TempDir()
	if _, err := learningsLog(root, LearningsLogIn{Action: "delete"}); err == nil {
		t.Fatal("expected error for unknown action, got nil")
	}
}

// appendLearningsEntries appends each entry in order, failing the test on the
// first error.
func appendLearningsEntries(t *testing.T, root string, entries ...string) {
	t.Helper()
	for _, e := range entries {
		if _, err := learningsLog(root, LearningsLogIn{Action: "append", Entry: e}); err != nil {
			t.Fatalf("append %q: %v", e, err)
		}
	}
}

// readLearningsLog returns the raw content of the learnings log file.
func readLearningsLog(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, paths.DataDir, "learnings", "log.md"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return string(data)
}

func TestLearningsLog_Remove(t *testing.T) {
	t.Run("removes single entry and preserves header", func(t *testing.T) {
		root := t.TempDir()
		appendLearningsEntries(t, root, "## one", "## two", "## three")

		out, err := learningsLog(root, LearningsLogIn{Action: "remove", Indices: []int{2}})
		if err != nil {
			t.Fatalf("remove: %v", err)
		}
		if !out.OK || !out.Changed || out.Action != "remove" {
			t.Fatalf("unexpected output: %+v", out)
		}

		content := readLearningsLog(t, root)
		if !strings.HasPrefix(content, "# SDLC Execution Learnings\n") {
			t.Fatalf("expected header preserved, got %q", content)
		}
		if strings.Contains(content, "## two") {
			t.Fatalf("expected entry 2 removed, got %q", content)
		}
		if !strings.Contains(content, "## one") || !strings.Contains(content, "## three") {
			t.Fatalf("expected entries 1 and 3 to remain, got %q", content)
		}
	})

	t.Run("nonexistent log returns error", func(t *testing.T) {
		root := t.TempDir()
		if _, err := learningsLog(root, LearningsLogIn{Action: "remove", Indices: []int{1}}); err == nil {
			t.Fatal("expected error removing from nonexistent log, got nil")
		}
	})

	t.Run("empty log returns error", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, paths.DataDir, "learnings", "log.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatalf("write empty log: %v", err)
		}
		if _, err := learningsLog(root, LearningsLogIn{Action: "remove", Indices: []int{1}}); err == nil {
			t.Fatal("expected error removing from empty log, got nil")
		}
	})
}

func TestLearningsLog_RemoveMultiple(t *testing.T) {
	cases := []struct {
		name    string
		indices []int
	}{
		{"ascending order", []int{1, 3}},
		{"descending order", []int{3, 1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			appendLearningsEntries(t, root, "## one", "## two", "## three")

			out, err := learningsLog(root, LearningsLogIn{Action: "remove", Indices: tc.indices})
			if err != nil {
				t.Fatalf("remove: %v", err)
			}
			if !out.Changed {
				t.Fatalf("unexpected output: %+v", out)
			}

			content := readLearningsLog(t, root)
			if strings.Contains(content, "## one") || strings.Contains(content, "## three") {
				t.Fatalf("expected entries 1 and 3 removed, got %q", content)
			}
			if !strings.Contains(content, "## two") {
				t.Fatalf("expected entry 2 to remain, got %q", content)
			}
		})
	}
}

func TestLearningsLog_RemoveOutOfBounds(t *testing.T) {
	cases := []struct {
		name    string
		indices []int
	}{
		{"zero index", []int{0}},
		{"negative index", []int{-1}},
		{"index beyond entry count", []int{3}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			appendLearningsEntries(t, root, "## one", "## two")

			if _, err := learningsLog(root, LearningsLogIn{Action: "remove", Indices: tc.indices}); err == nil {
				t.Fatalf("expected out-of-bounds error for indices %v, got nil", tc.indices)
			}
		})
	}
}

func TestLearningsLog_RemoveEmptyIndices(t *testing.T) {
	root := t.TempDir()
	appendLearningsEntries(t, root, "## one")

	_, err := learningsLog(root, LearningsLogIn{Action: "remove", Indices: []int{}})
	if err == nil {
		t.Fatal("expected error for empty indices, got nil")
	}
}

func TestLearningsLog_RemoveAllEntries(t *testing.T) {
	root := t.TempDir()
	appendLearningsEntries(t, root, "## one", "## two", "## three")

	out, err := learningsLog(root, LearningsLogIn{Action: "remove", Indices: []int{1, 2, 3}})
	if err != nil {
		t.Fatalf("remove all: %v", err)
	}
	if !out.OK || !out.Changed {
		t.Fatalf("unexpected output: %+v", out)
	}

	content := readLearningsLog(t, root)
	// File should be header-only with no trailing blank-entries block.
	if content != "# SDLC Execution Learnings\n" {
		t.Fatalf("expected header-only file, got %q", content)
	}

	// A subsequent append should produce the same layout as a fresh file.
	if _, err := learningsLog(root, LearningsLogIn{Action: "append", Entry: "## after-remove"}); err != nil {
		t.Fatalf("append after remove-all: %v", err)
	}
	content = readLearningsLog(t, root)
	if !strings.HasPrefix(content, "# SDLC Execution Learnings\n\n## after-remove\n") {
		t.Fatalf("expected clean header+entry layout after remove-all+append, got %q", content)
	}
}

func TestLearningsLog_RemoveEchoesContent(t *testing.T) {
	root := t.TempDir()
	appendLearningsEntries(t, root, "## entry-alpha", "## entry-beta", "## entry-gamma")

	out, err := learningsLog(root, LearningsLogIn{Action: "remove", Indices: []int{1, 3}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}

	// Content should echo the removed entries.
	if !strings.Contains(out.Content, "## entry-alpha") {
		t.Fatalf("expected removed entry 1 in Content, got %q", out.Content)
	}
	if !strings.Contains(out.Content, "## entry-gamma") {
		t.Fatalf("expected removed entry 3 in Content, got %q", out.Content)
	}
	if strings.Contains(out.Content, "## entry-beta") {
		t.Fatalf("Content should not contain kept entry, got %q", out.Content)
	}
}

func TestLearningsLog_AppendRejectsBlankLine(t *testing.T) {
	root := t.TempDir()

	_, err := learningsLog(root, LearningsLogIn{
		Action: "append",
		Entry:  "## heading\n\nSecond paragraph",
	})
	if err == nil {
		t.Fatal("expected error for entry containing blank line, got nil")
	}
	if !strings.Contains(err.Error(), "blank line") {
		t.Fatalf("expected blank-line error, got %q", err.Error())
	}
}
