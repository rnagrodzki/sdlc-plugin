package difftrunc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
)

// fakeSplitter returns a splitter function that returns the given FileDiff slices.
func fakeSplitter(chunks []gitx.FileDiff) func(string) []gitx.FileDiff {
	return func(_ string) []gitx.FileDiff {
		return chunks
	}
}

func TestDefaultDiffMaxBytes(t *testing.T) {
	if DefaultDiffMaxBytes != 8000 {
		t.Fatalf("DefaultDiffMaxBytes = %d, want 8000", DefaultDiffMaxBytes)
	}
}

func TestTruncate_UnderBudget(t *testing.T) {
	diff := "diff --git a/foo.go b/foo.go\n+hello\n"
	split := fakeSplitter(nil) // should not be called
	got := Truncate(diff, 10000, split)
	if got != diff {
		t.Errorf("expected unchanged diff, got %q", got)
	}
}

func TestTruncate_SplitterReturnsNil(t *testing.T) {
	diff := strings.Repeat("x", 100)
	split := fakeSplitter(nil)
	got := Truncate(diff, 50, split)
	if got != diff {
		t.Errorf("expected unchanged diff when splitter returns nil, got %q", got)
	}
}

func TestTruncate_SplitterReturnsEmpty(t *testing.T) {
	diff := strings.Repeat("x", 100)
	split := fakeSplitter([]gitx.FileDiff{})
	got := Truncate(diff, 50, split)
	if got != diff {
		t.Errorf("expected unchanged diff when splitter returns empty, got %q", got)
	}
}

func TestTruncate_AlwaysIncludesAtLeastOneFile(t *testing.T) {
	bigContent := strings.Repeat("A", 200)
	chunks := []gitx.FileDiff{
		{Path: "big.go", Content: bigContent},
	}
	diff := strings.Repeat("x", 300) // over any reasonable budget
	got := Truncate(diff, 50, fakeSplitter(chunks))
	if !strings.Contains(got, bigContent) {
		t.Error("expected at least one file to be included even when it exceeds budget")
	}
	if !strings.Contains(got, "# --- Truncated ---") {
		t.Error("expected truncation footer")
	}
	if !strings.Contains(got, "0 file(s) were omitted") {
		t.Error("expected 0 files omitted (the one file was included)")
	}
}

func TestTruncate_WholeFilePreservation(t *testing.T) {
	// Three files: 50, 30, 20 bytes of content.
	// Budget of 90 allows all three after the first (50).
	// Sorted descending: 50, 30, 20.
	fileA := gitx.FileDiff{Path: "a.go", Content: strings.Repeat("A", 50)}
	fileB := gitx.FileDiff{Path: "b.go", Content: strings.Repeat("B", 30)}
	fileC := gitx.FileDiff{Path: "c.go", Content: strings.Repeat("C", 20)}
	chunks := []gitx.FileDiff{fileA, fileB, fileC}

	diff := strings.Repeat("x", 200) // over budget
	got := Truncate(diff, 90, fakeSplitter(chunks))

	// All files should be included (50 + 30 = 80, 80 + 20 = 100 > 90 => C excluded).
	// Actually: first file always included (50). Then 50+30=80 <= 90 => B included.
	// Then 80+20=100 > 90 => C excluded.
	if !strings.Contains(got, fileA.Content) {
		t.Error("expected file A to be included")
	}
	if !strings.Contains(got, fileB.Content) {
		t.Error("expected file B to be included")
	}
	if strings.Contains(got, fileC.Content) {
		t.Error("expected file C to be excluded")
	}
	if !strings.Contains(got, "# - c.go") {
		t.Error("expected c.go in truncated files footer")
	}
	if !strings.Contains(got, "1 file(s) were omitted") {
		t.Error("expected 1 file omitted")
	}

	// Verify no mid-hunk cutting: the content of included files must appear
	// as complete substrings, not partial.
	idx := strings.Index(got, fileA.Content)
	if idx < 0 || got[idx:idx+len(fileA.Content)] != fileA.Content {
		t.Error("file A content was cut mid-hunk")
	}
	idx = strings.Index(got, fileB.Content)
	if idx < 0 || got[idx:idx+len(fileB.Content)] != fileB.Content {
		t.Error("file B content was cut mid-hunk")
	}
}

func TestTruncate_SkipAndContinue(t *testing.T) {
	// Sorted descending: big(5000), medium(4000), small(500).
	// Budget = 6000.
	// First: big(5000) always included. total=5000.
	// medium: 5000+4000=9000 > 6000 => skip, add to truncated.
	// small: 5000+500=5500 <= 6000 => include.
	big := gitx.FileDiff{Path: "big.go", Content: strings.Repeat("B", 5000)}
	medium := gitx.FileDiff{Path: "medium.go", Content: strings.Repeat("M", 4000)}
	small := gitx.FileDiff{Path: "small.go", Content: strings.Repeat("S", 500)}
	chunks := []gitx.FileDiff{big, medium, small}

	diff := strings.Repeat("x", 10000)
	got := Truncate(diff, 6000, fakeSplitter(chunks))

	if !strings.Contains(got, big.Content) {
		t.Error("big file should be included")
	}
	if !strings.Contains(got, small.Content) {
		t.Error("small file should be included (skip-and-continue)")
	}
	if strings.Contains(got, medium.Content) {
		t.Error("medium file should be excluded")
	}
	if !strings.Contains(got, "# - medium.go") {
		t.Error("medium.go should be in the truncated footer")
	}
	if !strings.Contains(got, "1 file(s) were omitted") {
		t.Error("expected 1 file omitted")
	}
}

func TestTruncate_MaxZeroUsesDefault(t *testing.T) {
	// max=0 should use DefaultDiffMaxBytes (8000).
	// A diff under 8000 should be returned unchanged.
	diff := strings.Repeat("x", 7999)
	got := Truncate(diff, 0, fakeSplitter(nil))
	if got != diff {
		t.Error("max=0 should use DefaultDiffMaxBytes; diff under 8000 should be unchanged")
	}

	// A diff over 8000 should trigger truncation.
	diff = strings.Repeat("x", 8001)
	chunks := []gitx.FileDiff{
		{Path: "f.go", Content: strings.Repeat("F", 100)},
	}
	got = Truncate(diff, 0, fakeSplitter(chunks))
	if !strings.Contains(got, "# --- Truncated ---") {
		t.Error("max=0 should use DefaultDiffMaxBytes; diff over 8000 should be truncated")
	}
}

func TestTruncate_CustomMax(t *testing.T) {
	// Custom max = 50.
	diff := strings.Repeat("x", 100)
	chunks := []gitx.FileDiff{
		{Path: "f.go", Content: strings.Repeat("F", 30)},
		{Path: "g.go", Content: strings.Repeat("G", 20)},
	}
	got := Truncate(diff, 50, fakeSplitter(chunks))
	if !strings.Contains(got, "# --- Truncated ---") {
		t.Error("expected truncation with custom max=50")
	}
	// Sorted descending: f(30), g(20). First always included.
	// 30+20=50 <= 50 => both included.
	if !strings.Contains(got, strings.Repeat("F", 30)) {
		t.Error("f.go should be included")
	}
	if !strings.Contains(got, strings.Repeat("G", 20)) {
		t.Error("g.go should be included")
	}
}

func TestTruncate_FooterFormat(t *testing.T) {
	// Verify exact footer format matches JS output.
	chunks := []gitx.FileDiff{
		{Path: "big.go", Content: strings.Repeat("A", 100)},
		{Path: "small1.go", Content: strings.Repeat("B", 30)},
		{Path: "small2.go", Content: strings.Repeat("C", 20)},
	}
	diff := strings.Repeat("x", 200)
	got := Truncate(diff, 110, fakeSplitter(chunks))

	// big(100) always included. 100+30=130 > 110 => small1 skipped.
	// 100+20=120 > 110 => small2 skipped.
	expectedFooter := strings.Join([]string{
		"# --- Truncated ---",
		"# The following 2 file(s) were omitted (see diffStat for summary):",
		"# - small1.go",
		"# - small2.go",
	}, "\n")
	if !strings.Contains(got, expectedFooter) {
		t.Errorf("footer mismatch.\nwant:\n%s\n\ngot:\n%s", expectedFooter, got)
	}
}

func TestTruncate_GoldenOutput(t *testing.T) {
	// Golden test: exact output matching the JS truncateDiff behavior.
	chunkA := "diff --git a/large.go b/large.go\n" + strings.Repeat("+line\n", 20) // 5*21 = 105 + 33 header = ~133
	chunkB := "diff --git a/medium.go b/medium.go\n" + strings.Repeat("+mod\n", 5)
	chunkC := "diff --git a/small.go b/small.go\n" + strings.Repeat("+s\n", 2)

	chunks := []gitx.FileDiff{
		{Path: "large.go", Content: chunkA},
		{Path: "medium.go", Content: chunkB},
		{Path: "small.go", Content: chunkC},
	}

	diff := chunkA + chunkB + chunkC
	max := len(chunkA) + len(chunkC) + 5 // fits large + small, not medium

	got := Truncate(diff, max, fakeSplitter(chunks))

	// Sorted descending: large, medium, small.
	// large always included.
	// large + medium > max => medium skipped.
	// large + small <= max => small included.
	if !strings.Contains(got, chunkA) {
		t.Fatal("large.go chunk must be present in full")
	}
	if !strings.Contains(got, chunkC) {
		t.Fatal("small.go chunk must be present (skip-and-continue)")
	}
	if strings.Contains(got, chunkB) {
		t.Fatal("medium.go chunk must be excluded")
	}

	// Verify whole-file integrity: each included chunk appears as a
	// contiguous substring (no mid-hunk cutting).
	for _, want := range []string{chunkA, chunkC} {
		idx := strings.Index(got, want)
		if idx < 0 {
			t.Fatal("included chunk not found as contiguous substring")
		}
	}

	// Verify footer.
	wantFooter := fmt.Sprintf(
		"# --- Truncated ---\n# The following 1 file(s) were omitted (see diffStat for summary):\n# - medium.go",
	)
	if !strings.HasSuffix(got, wantFooter) {
		t.Errorf("golden output suffix mismatch.\nwant suffix:\n%s\n\ngot suffix:\n%s",
			wantFooter, got[len(got)-len(wantFooter)-20:])
	}
}
