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
