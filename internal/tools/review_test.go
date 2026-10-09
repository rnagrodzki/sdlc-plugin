package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/setupmeta"
)

// ---------------------------------------------------------------------------
// globToRegex tests
// ---------------------------------------------------------------------------

func TestGlobToRegex(t *testing.T) {
	tests := []struct {
		glob    string
		match   []string
		noMatch []string
	}{
		{
			glob:    "*.go",
			match:   []string{"main.go", "test.go"},
			noMatch: []string{"src/main.go", "main.go.bak"},
		},
		{
			glob:    "**/*.go",
			match:   []string{"main.go", "src/main.go", "a/b/c/test.go"},
			noMatch: []string{"main.go.bak"},
		},
		{
			glob:    "src/**/*.ts",
			match:   []string{"src/index.ts", "src/lib/utils.ts", "src/a/b/c.ts"},
			noMatch: []string{"test/index.ts", "src.ts"},
		},
		{
			glob:    "**/test/**",
			match:   []string{"test/foo", "src/test/bar", "test/a/b"},
			noMatch: []string{"testing/foo"},
		},
		{
			glob:    "*.{ts,tsx}",
			noMatch: []string{"foo.ts"}, // not a supported syntax in this glob impl
		},
		{
			glob:    "src/?/file.go",
			match:   []string{"src/a/file.go"},
			noMatch: []string{"src/ab/file.go", "src//file.go"},
		},
		{
			glob:    "[abc].go",
			match:   []string{"a.go", "b.go", "c.go"},
			noMatch: []string{"d.go", "ab.go"},
		},
		{
			glob:    "file.name.ts",
			match:   []string{"file.name.ts"},
			noMatch: []string{"filexnamexts"},
		},
	}

	for _, tc := range tests {
		re := globToRegex(tc.glob)
		for _, m := range tc.match {
			if !regexpMatch(re, m) {
				t.Errorf("glob %q should match %q (regex: %s)", tc.glob, m, re)
			}
		}
		for _, nm := range tc.noMatch {
			if regexpMatch(re, nm) {
				t.Errorf("glob %q should NOT match %q (regex: %s)", tc.glob, nm, re)
			}
		}
	}
}

func regexpMatch(pattern, s string) bool {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

// ---------------------------------------------------------------------------
// matchFiles tests
// ---------------------------------------------------------------------------

func TestMatchFiles(t *testing.T) {
	meta := map[string]any{
		"triggers":  []any{"**/*.go", "**/*.ts"},
		"skip-when": []any{"**/vendor/**"},
		"max-files": 3,
	}
	files := []string{
		"main.go",
		"src/app.ts",
		"vendor/lib/dep.go",
		"README.md",
		"a.go",
		"b.go",
	}

	result := matchFiles(meta, files)

	// vendor/lib/dep.go should be excluded, README.md has no trigger match.
	// Remaining: main.go, src/app.ts, a.go, b.go = 4, but max-files=3 so truncated.
	if !result.truncated {
		t.Error("expected truncated=true with max-files=3")
	}
	if len(result.matched) != 3 {
		t.Errorf("expected 3 matched files, got %d", len(result.matched))
	}
}

func TestMatchFilesNoSkipWhen(t *testing.T) {
	meta := map[string]any{
		"triggers": []any{"*.md"},
	}
	files := []string{"README.md", "CHANGELOG.md", "main.go"}
	result := matchFiles(meta, files)

	if result.truncated {
		t.Error("should not be truncated")
	}
	if len(result.matched) != 2 {
		t.Errorf("expected 2 matched files, got %d: %v", len(result.matched), result.matched)
	}
}

// ---------------------------------------------------------------------------
// analyzeUncoveredFiles tests
// ---------------------------------------------------------------------------

func TestAnalyzeUncoveredFiles(t *testing.T) {
	files := []string{
		".github/workflows/ci.yml",
		"migrations/001_init.sql",
		"src/main.go",
	}

	suggestions, still := analyzeUncoveredFiles(files)

	if len(suggestions) != 2 {
		t.Fatalf("expected 2 suggestions, got %d", len(suggestions))
	}
	if suggestions[0].Dimension != "ci-cd-pipeline-review" {
		t.Errorf("first suggestion should be ci-cd-pipeline-review, got %s", suggestions[0].Dimension)
	}
	if suggestions[1].Dimension != "database-migrations-review" {
		t.Errorf("second suggestion should be database-migrations-review, got %s", suggestions[1].Dimension)
	}
	if len(still) != 1 || still[0] != "src/main.go" {
		t.Errorf("expected src/main.go in stillUncovered, got %v", still)
	}
}

func TestAnalyzeUncoveredFilesEmpty(t *testing.T) {
	suggestions, still := analyzeUncoveredFiles(nil)
	if suggestions != nil || still != nil {
		t.Error("expected nil results for empty input")
	}
}

// ---------------------------------------------------------------------------
// planWaves tests
// ---------------------------------------------------------------------------

// waveSizes returns the length of each wave.
func waveSizes(waves [][]string) []int {
	sizes := make([]int, 0, len(waves))
	for _, w := range waves {
		sizes = append(sizes, len(w))
	}
	return sizes
}

// flattenWaves returns the wave names in start order.
func flattenWaves(waves [][]string) []string {
	var names []string
	for _, w := range waves {
		names = append(names, w...)
	}
	return names
}

// TestPlanWaves_SeverityOrder pins the order: severity high to low, an
// unknown severity sorts as medium, and SKIPPED dimensions are left out.
// planWaves never changes a status.
func TestPlanWaves_SeverityOrder(t *testing.T) {
	dims := []reviewDimWork{
		{name: "info-dim", status: "ACTIVE", severity: "info"},
		{name: "skipped-dim", status: "SKIPPED", severity: "critical"},
		{name: "low-dim", status: "TRUNCATED", severity: "low"},
		{name: "unknown-dim", status: "ACTIVE", severity: "bogus", matchedFiles: make([]string, 1)},
		{name: "critical-dim", status: "ACTIVE", severity: "critical"},
		{name: "medium-dim", status: "ACTIVE", severity: "medium", matchedFiles: make([]string, 2)},
		{name: "high-dim", status: "TRUNCATED", severity: "high"},
	}

	waves := planWaves(dims, 3)

	got := flattenWaves(waves)
	want := []string{"critical-dim", "high-dim", "unknown-dim", "medium-dim", "low-dim", "info-dim"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("wave order = %v, want %v", got, want)
	}
	if fmt.Sprint(waveSizes(waves)) != "[3 3]" {
		t.Errorf("wave sizes = %v, want [3 3]", waveSizes(waves))
	}
	wantStatus := []string{"ACTIVE", "SKIPPED", "TRUNCATED", "ACTIVE", "ACTIVE", "ACTIVE", "TRUNCATED"}
	for i, d := range dims {
		if d.status != wantStatus[i] {
			t.Errorf("%s status = %q, want %q (planWaves must not change a status)", d.name, d.status, wantStatus[i])
		}
	}
}

// TestPlanWaves_TieFewerFiles pins the tiebreak rule: among equal
// severities, the dimension with fewer matched files starts first.
func TestPlanWaves_TieFewerFiles(t *testing.T) {
	dims := []reviewDimWork{
		{name: "dim-3files", status: "ACTIVE", severity: "medium", matchedFiles: make([]string, 3)},
		{name: "dim-1file", status: "ACTIVE", severity: "medium", matchedFiles: make([]string, 1)},
		{name: "dim-2files", status: "ACTIVE", severity: "medium", matchedFiles: make([]string, 2)},
	}

	waves := planWaves(dims, 2)

	want := "[[dim-1file dim-2files] [dim-3files]]"
	if fmt.Sprint(waves) != want {
		t.Errorf("waves = %v, want %s", waves, want)
	}
}

// TestPlanWaves_Sizes8_8_5 pins the split: 21 started dimensions with a
// limit of 8 give waves of sizes 8, 8 and 5, in severity order.
func TestPlanWaves_Sizes8_8_5(t *testing.T) {
	severities := []string{"info", "low", "medium", "high", "critical"}
	dims := make([]reviewDimWork, 21)
	for i := range dims {
		dims[i].name = fmt.Sprintf("dim-%02d", i)
		dims[i].status = "ACTIVE"
		dims[i].severity = severities[i%len(severities)]
	}

	waves := planWaves(dims, defaultMaxParallelDimensions)

	if fmt.Sprint(waveSizes(waves)) != "[8 8 5]" {
		t.Fatalf("wave sizes = %v, want [8 8 5]", waveSizes(waves))
	}
	byName := map[string]reviewDimWork{}
	for _, d := range dims {
		byName[d.name] = d
	}
	names := flattenWaves(waves)
	if len(names) != 21 {
		t.Fatalf("waves hold %d names, want 21", len(names))
	}
	for i := 1; i < len(names); i++ {
		prev := severityRank[byName[names[i-1]].severity]
		cur := severityRank[byName[names[i]].severity]
		if cur > prev {
			t.Errorf("%s (%s) starts after %s (%s): waves are not in severity order",
				names[i], byName[names[i]].severity, names[i-1], byName[names[i-1]].severity)
		}
	}
}

// TestPlanWaves_NonPositiveLimit pins the guard for a limit below 1: the
// split uses a limit of 1, so each wave holds one name and the loop ends.
func TestPlanWaves_NonPositiveLimit(t *testing.T) {
	for _, limit := range []int{0, -3} {
		dims := []reviewDimWork{
			{name: "a", status: "ACTIVE", severity: "high"},
			{name: "b", status: "ACTIVE", severity: "low"},
		}

		waves := planWaves(dims, limit)

		if fmt.Sprint(waves) != "[[a] [b]]" {
			t.Errorf("planWaves(limit %d) = %v, want [[a] [b]]", limit, waves)
		}
	}
}

// TestPlanWaves_EmptyNotNil pins that zero started dimensions give an
// empty, non-nil slice, so the manifest renders "waves": [].
func TestPlanWaves_EmptyNotNil(t *testing.T) {
	for _, dims := range [][]reviewDimWork{
		nil,
		{{name: "skipped", status: "SKIPPED", severity: "high"}},
	} {
		waves := planWaves(dims, defaultMaxParallelDimensions)
		if waves == nil {
			t.Fatal("planWaves returned nil, want an empty slice")
		}
		if len(waves) != 0 {
			t.Errorf("waves = %v, want []", waves)
		}
		raw, err := json.Marshal(waves)
		if err != nil {
			t.Fatalf("marshal waves: %v", err)
		}
		if string(raw) != "[]" {
			t.Errorf("waves JSON = %s, want []", raw)
		}
	}
}

// TestReviewWaveNext pins every manifest-mode next text. Zero waves wins
// over a dry run.
func TestReviewWaveNext(t *testing.T) {
	tests := []struct {
		name      string
		waveCount int
		dryRun    bool
		want      string
	}{
		{
			name:      "waves",
			waveCount: 3,
			want:      "Read the manifest at /tmp/sdlc-review-x/manifest.json. Start the agents of waves[0] in one message, poll until the wave ends, then start the next wave. Waves: 3.",
		},
		{
			name:      "zero waves",
			waveCount: 0,
			want:      "No dimension matches the diff. Report zero findings. No ledger exists.",
		},
		{
			name:      "dry run",
			waveCount: 3,
			dryRun:    true,
			want:      "Dry run: print the plan from the manifest and stop. No ledger exists.",
		},
		{
			name:      "dry run with zero waves",
			waveCount: 0,
			dryRun:    true,
			want:      "No dimension matches the diff. Report zero findings. No ledger exists.",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := reviewWaveNext("/tmp/sdlc-review-x/manifest.json", tc.waveCount, tc.dryRun); got != tc.want {
				t.Errorf("reviewWaveNext() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolveMaxParallelDimensions tests
// ---------------------------------------------------------------------------

// TestResolveMaxParallelDimensions pins the default, the valid range, the
// clamp and the error for each invalid type of maxParallelDimensions.
func TestResolveMaxParallelDimensions(t *testing.T) {
	tests := []struct {
		name      string
		reviewCfg map[string]any
		want      int
		wantErr   bool
	}{
		{name: "nil config", reviewCfg: nil, want: 8},
		{name: "no maxParallelDimensions key", reviewCfg: map[string]any{"scope": "all"}, want: 8},
		{name: "configured limit", reviewCfg: map[string]any{"maxParallelDimensions": float64(22)}, want: 22},
		{name: "minimum boundary", reviewCfg: map[string]any{"maxParallelDimensions": float64(1)}, want: 1},
		{name: "zero", reviewCfg: map[string]any{"maxParallelDimensions": float64(0)}, wantErr: true},
		{name: "negative", reviewCfg: map[string]any{"maxParallelDimensions": float64(-1)}, wantErr: true},
		{name: "fractional", reviewCfg: map[string]any{"maxParallelDimensions": float64(1.5)}, wantErr: true},
		{name: "string", reviewCfg: map[string]any{"maxParallelDimensions": "8"}, wantErr: true},
		{name: "positive infinity", reviewCfg: map[string]any{"maxParallelDimensions": math.Inf(1)}, wantErr: true},
		{name: "negative infinity", reviewCfg: map[string]any{"maxParallelDimensions": math.Inf(-1)}, wantErr: true},
		{name: "nan", reviewCfg: map[string]any{"maxParallelDimensions": math.NaN()}, wantErr: true},
		{name: "huge whole number is clamped", reviewCfg: map[string]any{"maxParallelDimensions": float64(1e300)}, want: math.MaxInt32},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveMaxParallelDimensions(tc.reviewCfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got limit %d", got)
				}
				var de *mcpserver.DomainError
				if !errors.As(err, &de) {
					t.Fatalf("expected a *mcpserver.DomainError, got %T: %v", err, err)
				}
				if de.Suggestion == "" {
					t.Error("expected a non-empty Suggestion")
				}
				if !strings.Contains(de.Msg, "maxParallelDimensions") {
					t.Errorf("message %q should name maxParallelDimensions", de.Msg)
				}
				if !strings.Contains(de.Msg, fmt.Sprintf("%v", tc.reviewCfg["maxParallelDimensions"])) {
					t.Errorf("message %q does not render the received value %v", de.Msg, tc.reviewCfg["maxParallelDimensions"])
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("resolveMaxParallelDimensions() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestResolveMaxParallelDimensions_OldKey pins that the old maxDimensions
// key returns the rename DomainError, also when maxParallelDimensions is set.
func TestResolveMaxParallelDimensions_OldKey(t *testing.T) {
	for _, cfg := range []map[string]any{
		{"maxDimensions": float64(8)},
		{"maxDimensions": float64(8), "maxParallelDimensions": float64(8)},
	} {
		_, err := resolveMaxParallelDimensions(cfg)
		var de *mcpserver.DomainError
		if !errors.As(err, &de) {
			t.Fatalf("cfg %v: expected a *mcpserver.DomainError, got %T: %v", cfg, err, err)
		}
		if !strings.Contains(de.Msg, "maxDimensions") || !strings.Contains(de.Msg, "renamed to maxParallelDimensions") {
			t.Errorf("cfg %v: message %q should name the old and the new key", cfg, de.Msg)
		}
		if !strings.Contains(de.Suggestion, "maxParallelDimensions") {
			t.Errorf("cfg %v: suggestion %q should name maxParallelDimensions", cfg, de.Suggestion)
		}
	}
}

// ---------------------------------------------------------------------------
// critiquePlan tests
// ---------------------------------------------------------------------------

func TestCritiquePlan(t *testing.T) {
	dims := []reviewDimWork{
		{name: "a", status: "ACTIVE", matchedFiles: []string{"f1.go", "f2.go"}},
		{name: "b", status: "ACTIVE", matchedFiles: []string{"f1.go", "f2.go"}},
		{name: "c", status: "SKIPPED"},
	}
	changedFiles := []string{"f1.go", "f2.go", "f3.go"}

	critique := critiquePlan(dims, changedFiles)

	if len(critique.UncoveredFiles) != 1 || critique.UncoveredFiles[0] != "f3.go" {
		t.Errorf("expected f3.go uncovered, got %v", critique.UncoveredFiles)
	}
	if len(critique.OverlappingPairs) != 1 {
		t.Errorf("expected 1 overlapping pair, got %d", len(critique.OverlappingPairs))
	}
	// Both dims cover 2/3 = 66%, which is < 80%.
	if len(critique.OverBroadDimensions) != 0 {
		t.Errorf("expected no over-broad dims, got %v", critique.OverBroadDimensions)
	}
}

func TestCritiquePlanOverBroad(t *testing.T) {
	dims := []reviewDimWork{
		{name: "a", status: "ACTIVE", matchedFiles: []string{"f1", "f2", "f3", "f4", "f5"}},
	}
	changedFiles := []string{"f1", "f2", "f3", "f4", "f5", "f6"}

	critique := critiquePlan(dims, changedFiles)

	// 5/6 = 83% > 80%
	if len(critique.OverBroadDimensions) != 1 || critique.OverBroadDimensions[0] != "a" {
		t.Errorf("expected dim 'a' to be over-broad, got %v", critique.OverBroadDimensions)
	}
}

// ---------------------------------------------------------------------------
// toIndexEntry gating tests
// ---------------------------------------------------------------------------

func TestIndexEntrySliceFileGating(t *testing.T) {
	slicePath := "/tmp/test.slice.json"

	tests := []struct {
		status      string
		expectSlice bool
	}{
		{"ACTIVE", true},
		{"TRUNCATED", true},
		{"SKIPPED", false},
	}

	for _, tc := range tests {
		d := reviewDimWork{
			name:      "test",
			status:    tc.status,
			sliceFile: &slicePath,
		}
		dispatched := d.status == "ACTIVE" || d.status == "TRUNCATED"
		var sf *string
		if dispatched {
			sf = d.sliceFile
		}
		entry := reviewDimIndexEntry{
			Name:      d.name,
			Status:    d.status,
			SliceFile: sf,
		}

		if tc.expectSlice && entry.SliceFile == nil {
			t.Errorf("status %s: expected slice_file, got nil", tc.status)
		}
		if !tc.expectSlice && entry.SliceFile != nil {
			t.Errorf("status %s: expected nil slice_file, got %v", tc.status, *entry.SliceFile)
		}
	}
}

// ---------------------------------------------------------------------------
// Integration-style test: reviewPrepare with fixture git repo
// ---------------------------------------------------------------------------

func TestReviewPrepareFixture(t *testing.T) {
	// reviewPrepare creates its manifest folder under TMPDIR: keep it inside t.TempDir.
	t.Setenv("TMPDIR", t.TempDir())
	// Build a minimal git repo with some files and a dimension.
	root := t.TempDir()

	// Initialize git repo.
	mustRun(t, root, "git", "init")
	mustRun(t, root, "git", "config", "user.email", "test@test.com")
	mustRun(t, root, "git", "config", "user.name", "Test")

	// Create initial commit on main.
	writeFile(t, filepath.Join(root, "README.md"), "# test\n")
	mustRun(t, root, "git", "add", ".")
	mustRun(t, root, "git", "commit", "-m", "init")
	mustRun(t, root, "git", "branch", "-M", "main")

	// Create feature branch.
	mustRun(t, root, "git", "checkout", "-b", "feature")

	// Add changed files.
	writeFile(t, filepath.Join(root, "src", "app.go"), "package main\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "src", "util.go"), "package main\nfunc helper() {}\n")
	mustRun(t, root, "git", "add", ".")
	mustRun(t, root, "git", "commit", "-m", "add go files")

	// Create dimension.
	dimDir := filepath.Join(root, paths.DataDir, "review-dimensions")
	writeFile(t, filepath.Join(dimDir, "code-quality.md"), `---
name: code-quality
description: General code quality review
triggers:
  - "**/*.go"
severity: medium
---
Review the code for quality issues, including readability, maintainability,
and adherence to best practices. Check for potential bugs and edge cases.
`)

	// A current (empty) config.toml. SkipConfigCheck below bypasses the KD5
	// gate anyway, and config.ReadSection(projectRoot, "review") reads
	// local.toml (review is a local section, not a project one), so this
	// file has no effect on the test outcome — it just keeps the fixture
	// looking like a real project layout.
	sdlcDir := filepath.Join(root, paths.DataDir)
	writeFile(t, filepath.Join(sdlcDir, "config.toml"), "")

	// Run reviewPrepare. The branch has no PR (hermetic fake gh).
	stubReviewGH(t, reviewGHNoPR)
	out, err := reviewPrepare(root, root, ReviewPrepareIn{
		SkipConfigCheck: true, // Skip config check since we have minimal config.
		Target:          "main",
	})
	if err != nil {
		t.Fatalf("reviewPrepare failed: %v", err)
	}

	if out.ManifestPath == "" {
		t.Fatal("ManifestPath is empty")
	}
	if out.Summary.TotalDimensions != 1 {
		t.Errorf("expected 1 dimension, got %d", out.Summary.TotalDimensions)
	}
	if out.Summary.ActiveDimensions != 1 {
		t.Errorf("expected 1 active dimension, got %d", out.Summary.ActiveDimensions)
	}
	if out.Summary.TotalChangedFiles != 2 {
		t.Errorf("expected 2 changed files, got %d", out.Summary.TotalChangedFiles)
	}

	// Task 20: the output carries the plugin-wide communication style,
	// defaulting to "functional" with no [style]/[planStyle] config present.
	if out.Style.Audience != "functional" {
		t.Errorf("Style.Audience: got %q, want %q", out.Style.Audience, "functional")
	}

	// Read and verify manifest.
	manifestBytes, err := os.ReadFile(out.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	// Task 20: style must never reach review subagents — the manifest file
	// is built from reviewManifest, a struct with no style field, so the
	// raw JSON on disk must carry no "style" key regardless of what the
	// in-memory reviewManifest struct happens to unmarshal into.
	var rawManifest map[string]any
	if err := json.Unmarshal(manifestBytes, &rawManifest); err != nil {
		t.Fatalf("unmarshal raw manifest: %v", err)
	}
	if _, present := rawManifest["style"]; present {
		t.Error("manifest.json must not contain a \"style\" key — it must not reach review subagents")
	}

	var manifest reviewManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}

	if manifest.Version != 1 {
		t.Errorf("manifest version: got %d, want 1", manifest.Version)
	}
	if manifest.Scope != "all" {
		t.Errorf("manifest scope: got %q, want all", manifest.Scope)
	}
	if len(manifest.Dimensions) != 1 {
		t.Fatalf("expected 1 dimension in manifest, got %d", len(manifest.Dimensions))
	}

	dim := manifest.Dimensions[0]
	if dim.Name != "code-quality" {
		t.Errorf("dimension name: got %q, want code-quality", dim.Name)
	}
	if dim.Status != "ACTIVE" {
		t.Errorf("dimension status: got %q, want ACTIVE", dim.Status)
	}
	if dim.MatchedCount != 2 {
		t.Errorf("dimension matched_count: got %d, want 2", dim.MatchedCount)
	}
	if dim.DiffFile == nil {
		t.Error("dimension diff_file should not be nil")
	}
	if dim.SliceFile == nil {
		t.Error("dimension slice_file should not be nil")
	}

	// Verify diff file contains actual diff content.
	if dim.DiffFile != nil {
		diffContent, err := os.ReadFile(*dim.DiffFile)
		if err != nil {
			t.Fatalf("read diff file: %v", err)
		}
		if !strings.Contains(string(diffContent), "diff --git") {
			t.Error("diff file should contain 'diff --git' header")
		}
	}

	// Verify slice file structure.
	if dim.SliceFile != nil {
		sliceContent, err := os.ReadFile(*dim.SliceFile)
		if err != nil {
			t.Fatalf("read slice file: %v", err)
		}
		var slice dimSlice
		if err := json.Unmarshal(sliceContent, &slice); err != nil {
			t.Fatalf("unmarshal slice: %v", err)
		}
		if slice.Body == "" {
			t.Error("slice body should not be empty")
		}
		if len(slice.MatchedFiles) != 2 {
			t.Errorf("slice matched_files: got %d, want 2", len(slice.MatchedFiles))
		}
	}
}

// TestReviewPrepareDiffByteCapTruncation covers
// review-drift-2026-09-13T-diff-truncation: a dimension can pass the
// matched-file-count cap yet still have its concatenated diff exceed
// difftrunc.DefaultDiffMaxBytes, silently dropping whole files from the
// worker's .diff file. The manifest must flag this via `truncated`/`status`
// rather than staying silent.
func TestReviewPrepareDiffByteCapTruncation(t *testing.T) {
	// reviewPrepare creates its manifest folder under TMPDIR: keep it inside t.TempDir.
	t.Setenv("TMPDIR", t.TempDir())
	root := t.TempDir()

	mustRun(t, root, "git", "init")
	mustRun(t, root, "git", "config", "user.email", "test@test.com")
	mustRun(t, root, "git", "config", "user.name", "Test")

	writeFile(t, filepath.Join(root, "README.md"), "# test\n")
	mustRun(t, root, "git", "add", ".")
	mustRun(t, root, "git", "commit", "-m", "init")
	mustRun(t, root, "git", "branch", "-M", "main")

	mustRun(t, root, "git", "checkout", "-b", "feature")

	// Three changed files, each comfortably under the 100-file matched-count
	// cap, but whose combined diff exceeds difftrunc.DefaultDiffMaxBytes
	// (8000 bytes) — only the content-size cap should trigger here.
	bigBody := strings.Repeat("x", 4000)
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		writeFile(t, filepath.Join(root, "src", name), fmt.Sprintf("package main\n// %s\n", bigBody))
	}
	mustRun(t, root, "git", "add", ".")
	mustRun(t, root, "git", "commit", "-m", "add large go files")

	dimDir := filepath.Join(root, paths.DataDir, "review-dimensions")
	writeFile(t, filepath.Join(dimDir, "code-quality.md"), `---
name: code-quality
description: General code quality review
triggers:
  - "**/*.go"
severity: medium
---
Review the code for quality issues.
`)

	sdlcDir := filepath.Join(root, paths.DataDir)
	writeFile(t, filepath.Join(sdlcDir, "config.toml"), "")

	stubReviewGH(t, reviewGHNoPR)
	out, err := reviewPrepare(root, root, ReviewPrepareIn{
		SkipConfigCheck: true,
		Target:          "main",
	})
	if err != nil {
		t.Fatalf("reviewPrepare failed: %v", err)
	}

	manifestBytes, err := os.ReadFile(out.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest reviewManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if len(manifest.Dimensions) != 1 {
		t.Fatalf("expected 1 dimension in manifest, got %d", len(manifest.Dimensions))
	}

	dim := manifest.Dimensions[0]
	if dim.MatchedCount != 3 {
		t.Errorf("dimension matched_count: got %d, want 3", dim.MatchedCount)
	}
	if !dim.Truncated {
		t.Error("dimension truncated: got false, want true (concatenated diff exceeds DefaultDiffMaxBytes)")
	}
	if dim.Status != "TRUNCATED" {
		t.Errorf("dimension status: got %q, want TRUNCATED", dim.Status)
	}
	if dim.DiffFile == nil {
		t.Fatal("dimension diff_file should not be nil")
	}

	diffContent, err := os.ReadFile(*dim.DiffFile)
	if err != nil {
		t.Fatalf("read diff file: %v", err)
	}
	if !strings.Contains(string(diffContent), "# --- Truncated ---") {
		t.Error("diff file should contain the difftrunc truncation footer")
	}
	if len(diffContent) >= 3*4000 {
		t.Errorf("diff file len = %d, want it capped well under the untruncated size", len(diffContent))
	}
}

// newReviewFixture builds a git repo with a main branch and a feature
// branch that adds files, then writes the given review dimensions (file
// name -> content) into the active worktree. It returns the repo root.
func newReviewFixture(t *testing.T, files, dims map[string]string) string {
	t.Helper()
	root := t.TempDir()

	mustRun(t, root, "git", "init")
	mustRun(t, root, "git", "config", "user.email", "test@test.com")
	mustRun(t, root, "git", "config", "user.name", "Test")
	writeFile(t, filepath.Join(root, "README.md"), "# test\n")
	mustRun(t, root, "git", "add", ".")
	mustRun(t, root, "git", "commit", "-m", "init")
	mustRun(t, root, "git", "branch", "-M", "main")
	mustRun(t, root, "git", "checkout", "-b", "feature")

	for name, content := range files {
		writeFile(t, filepath.Join(root, name), content)
	}
	mustRun(t, root, "git", "add", ".")
	mustRun(t, root, "git", "commit", "-m", "add files")

	dimDir := filepath.Join(root, paths.DataDir, "review-dimensions")
	for name, content := range dims {
		writeFile(t, filepath.Join(dimDir, name), content)
	}
	// Keep the PR lookup hermetic: by default the branch has no PR. A test
	// that needs a PR installs its own fake gh afterwards; it goes first on
	// PATH and wins.
	stubReviewGH(t, reviewGHNoPR)
	return root
}

// Fake gh scripts for review_prepare's PR lookup (gh pr view --json ...).
const (
	reviewGHNoPR = "#!/bin/sh\necho 'no pull requests found for branch \"feature\"' >&2\nexit 1\n"
	reviewGHAuth = "#!/bin/sh\necho 'HTTP 401: Bad credentials (https://api.github.com/graphql)' >&2\nexit 1\n"
)

// reviewGHPR returns a fake gh script that reports PR 42 of acme/widgets in
// the given state, and fails on any command other than `gh pr view`.
func reviewGHPR(state string) string {
	return "#!/bin/sh\n" +
		"[ \"$1 $2\" = \"pr view\" ] || { echo \"unexpected gh args: $*\" >&2; exit 3; }\n" +
		"printf '%s\\n' '{\"number\":42,\"title\":\"Add widgets\",\"url\":\"https://github.com/acme/widgets/pull/42\",\"state\":\"" + state + "\",\"labels\":[]}'\n"
}

// stubReviewGH installs a fake gh on PATH for the rest of the test.
func stubReviewGH(t *testing.T, script string) {
	t.Helper()
	t.Cleanup(stubGH(t, script))
}

// readReviewManifest runs reviewPrepare against root with target main and
// returns the decoded manifest.
func readReviewManifest(t *testing.T, root string) (ReviewPrepareOut, reviewManifest) {
	t.Helper()
	out, err := reviewPrepare(root, root, ReviewPrepareIn{SkipConfigCheck: true, Target: "main"})
	if err != nil {
		t.Fatalf("reviewPrepare failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(out.ManifestPath)) })
	raw, err := os.ReadFile(out.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m reviewManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	return out, m
}

// TestReviewPrepareWavesIncludeTruncated pins that TRUNCATED dimensions are
// started: ten dimensions that are all TRUNCATED by max-files are all in
// waves (sizes 8 and 2 with the default limit), and each has a .diff and a
// .slice.json file.
func TestReviewPrepareWavesIncludeTruncated(t *testing.T) {
	dims := map[string]string{}
	for i := 0; i < 10; i++ {
		dims[fmt.Sprintf("dim-%02d.md", i)] = fmt.Sprintf(`---
name: dim-%02d
description: Dimension %d
triggers:
  - "**/*.go"
severity: medium
max-files: 1
---
Review.
`, i, i)
	}
	root := newReviewFixture(t, map[string]string{
		"src/a.go": "package main\n",
		"src/b.go": "package main\n",
	}, dims)

	out, m := readReviewManifest(t, root)

	inWaves := map[string]bool{}
	for _, name := range flattenWaves(m.Waves) {
		inWaves[name] = true
	}
	if fmt.Sprint(waveSizes(m.Waves)) != "[8 2]" {
		t.Errorf("wave sizes = %v, want [8 2]", waveSizes(m.Waves))
	}
	for _, d := range m.Dimensions {
		if d.Status != "TRUNCATED" {
			t.Errorf("%s status = %q, want TRUNCATED", d.Name, d.Status)
		}
		if !inWaves[d.Name] {
			t.Errorf("%s is not in waves", d.Name)
		}
		if d.DiffFile == nil || d.SliceFile == nil {
			t.Errorf("%s: diff_file/slice_file = %v/%v, want both set", d.Name, d.DiffFile, d.SliceFile)
		}
		for _, ext := range []string{".diff", ".slice.json"} {
			if _, err := os.Stat(filepath.Join(m.DiffDir, d.Name+ext)); err != nil {
				t.Errorf("%s: %s file missing: %v", d.Name, ext, err)
			}
		}
	}
	if out.Summary.ActiveDimensions != 10 || out.Summary.WaveCount != 2 {
		t.Errorf("summary active/wave_count = %d/%d, want 10/2", out.Summary.ActiveDimensions, out.Summary.WaveCount)
	}
	if m.PlanCritique.MaxParallelDimensions != defaultMaxParallelDimensions {
		t.Errorf("plan_critique.max_parallel_dimensions = %d, want %d", m.PlanCritique.MaxParallelDimensions, defaultMaxParallelDimensions)
	}
	if want := reviewWaveNext(out.ManifestPath, 2, false); out.Next != want {
		t.Errorf("next = %q, want %q", out.Next, want)
	}
}

// TestReviewPrepareWavesSeverityFirst pins the start order across waves:
// with 1 critical and 8 info dimensions and a limit of 1, the critical
// dimension is waves[0][0] and every other dimension is in a later wave.
func TestReviewPrepareWavesSeverityFirst(t *testing.T) {
	files := map[string]string{"src/critical.ext": "package main\n"}
	dims := map[string]string{
		"critical.md": `---
name: critical-dim
description: Critical dimension
triggers:
  - "**/*.ext"
severity: critical
---
Review.
`,
	}
	for i := 0; i < 8; i++ {
		files[fmt.Sprintf("src/info%d.ext%d", i, i)] = "package main\n"
		dims[fmt.Sprintf("info%d.md", i)] = fmt.Sprintf(`---
name: info-dim-%d
description: Info dimension %d
triggers:
  - "**/*.ext%d"
severity: info
---
Review.
`, i, i, i)
	}
	root := newReviewFixture(t, files, dims)
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "[review]\nmaxParallelDimensions = 1\n")

	out, m := readReviewManifest(t, root)

	if len(m.Waves) != 9 {
		t.Fatalf("waves = %v, want 9 waves of 1", m.Waves)
	}
	if m.Waves[0][0] != "critical-dim" {
		t.Errorf("waves[0][0] = %q, want critical-dim", m.Waves[0][0])
	}
	for i, w := range m.Waves[1:] {
		if len(w) != 1 {
			t.Errorf("waves[%d] = %v, want 1 name", i+1, w)
		}
		for _, name := range w {
			if name == "critical-dim" {
				t.Errorf("critical-dim is also in waves[%d]", i+1)
			}
		}
	}
	for _, d := range m.Dimensions {
		if d.Status != "ACTIVE" {
			t.Errorf("%s status = %q, want ACTIVE", d.Name, d.Status)
		}
	}
	if out.Summary.WaveCount != 9 {
		t.Errorf("summary.wave_count = %d, want 9", out.Summary.WaveCount)
	}
}

// TestReviewPrepareMaxParallelDimensionsFromLocalToml pins that [review]
// maxParallelDimensions in .sdlc-v2/local.toml is read, echoed verbatim into
// plan_critique.max_parallel_dimensions, and sets the wave size.
func TestReviewPrepareMaxParallelDimensionsFromLocalToml(t *testing.T) {
	dims := map[string]string{}
	for i := 0; i < 5; i++ {
		dims[fmt.Sprintf("dim-%02d.md", i)] = fmt.Sprintf(`---
name: dim-%02d
description: Dimension %d
triggers:
  - "**/*.go"
severity: medium
---
Review.
`, i, i)
	}
	root := newReviewFixture(t, map[string]string{"src/a.go": "package main\n"}, dims)
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "[review]\nmaxParallelDimensions = 2\n")

	out, m := readReviewManifest(t, root)

	if m.PlanCritique.MaxParallelDimensions != 2 {
		t.Errorf("plan_critique.max_parallel_dimensions = %d, want 2", m.PlanCritique.MaxParallelDimensions)
	}
	if fmt.Sprint(waveSizes(m.Waves)) != "[2 2 1]" {
		t.Errorf("wave sizes = %v, want [2 2 1]", waveSizes(m.Waves))
	}
	if out.Summary.WaveCount != 3 || m.Summary.WaveCount != 3 {
		t.Errorf("wave_count out/manifest = %d/%d, want 3/3", out.Summary.WaveCount, m.Summary.WaveCount)
	}
}

// TestReviewPrepareDefaultMaxParallelDimensions pins that the default limit
// reaches plan_critique.max_parallel_dimensions when local.toml has no
// [review] section, or a [review] section with no maxParallelDimensions key.
func TestReviewPrepareDefaultMaxParallelDimensions(t *testing.T) {
	tests := []struct {
		name      string
		localToml string
	}{
		{name: "no review section", localToml: "[jira]\nproject = \"ABC\"\n"},
		{name: "no maxParallelDimensions key", localToml: "[review]\nscope = \"all\"\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := newReviewFixture(t, map[string]string{"src/a.go": "package main\n"}, map[string]string{
				"dim.md": `---
name: dim-a
description: Dimension
triggers:
  - "**/*.go"
severity: medium
---
Review.
`,
			})
			writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), tc.localToml)

			_, m := readReviewManifest(t, root)

			if m.PlanCritique.MaxParallelDimensions != defaultMaxParallelDimensions {
				t.Errorf("plan_critique.max_parallel_dimensions = %d, want %d", m.PlanCritique.MaxParallelDimensions, defaultMaxParallelDimensions)
			}
		})
	}
}

// TestReviewPrepareInfiniteMaxParallelDimensions pins that
// maxParallelDimensions = inf in local.toml returns a DomainError instead of
// reaching planWaves as an undefined float-to-int conversion.
func TestReviewPrepareInfiniteMaxParallelDimensions(t *testing.T) {
	root := newReviewFixture(t, map[string]string{"src/a.go": "package main\n"}, map[string]string{
		"dim.md": `---
name: dim-a
description: Dimension
triggers:
  - "**/*.go"
severity: medium
---
Review.
`,
	})
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "[review]\nmaxParallelDimensions = inf\n")

	_, err := reviewPrepare(root, root, ReviewPrepareIn{SkipConfigCheck: true, Target: "main"})
	if err == nil {
		t.Fatal("expected an error for maxParallelDimensions = inf")
	}
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("expected a *mcpserver.DomainError, got %T: %v", err, err)
	}
	if !strings.Contains(de.Msg, "maxParallelDimensions") {
		t.Errorf("message %q should name maxParallelDimensions", de.Msg)
	}
}

// TestDefaultMaxParallelDimensionsMatchesSetupField pins
// defaultMaxParallelDimensions to the review.maxParallelDimensions setup
// field default. setupmeta's TestReviewMaxParallelDimensionsSchemaMatchesField
// pins that field to the JSON schema default, so the three values cannot
// drift apart.
func TestDefaultMaxParallelDimensionsMatchesSetupField(t *testing.T) {
	var found bool
	for _, section := range setupmeta.Sections() {
		if section.ID != "review" {
			continue
		}
		for _, f := range section.Fields {
			if f.Name != "maxParallelDimensions" {
				continue
			}
			found = true
			if f.Default != any(defaultMaxParallelDimensions) {
				t.Errorf("setupmeta review.maxParallelDimensions default = %v, want defaultMaxParallelDimensions %d", f.Default, defaultMaxParallelDimensions)
			}
		}
	}
	if !found {
		t.Fatal("setupmeta review section has no maxParallelDimensions field")
	}
}

// TestReviewPrepareInvalidMaxParallelDimensions pins that an invalid
// [review] maxParallelDimensions value in .sdlc-v2/local.toml stops
// review_prepare with a DomainError, before any git work or file write.
func TestReviewPrepareInvalidMaxParallelDimensions(t *testing.T) {
	root := newReviewFixture(t, map[string]string{"src/a.go": "package main\n"}, map[string]string{
		"dim.md": `---
name: dim-a
description: Dimension
triggers:
  - "**/*.go"
severity: medium
---
Review.
`,
	})
	for _, value := range []string{"0", "1.5", "\"8\"", "inf"} {
		t.Run(value, func(t *testing.T) {
			writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "[review]\nmaxParallelDimensions = "+value+"\n")

			_, err := reviewPrepare(root, root, ReviewPrepareIn{SkipConfigCheck: true, Target: "main"})
			if err == nil {
				t.Fatal("expected an error for an invalid maxParallelDimensions value")
			}
			var de *mcpserver.DomainError
			if !errors.As(err, &de) {
				t.Fatalf("expected a *mcpserver.DomainError, got %T: %v", err, err)
			}
			if de.Suggestion == "" {
				t.Error("expected a non-empty Suggestion")
			}
			if !strings.Contains(de.Msg, "maxParallelDimensions") || !strings.Contains(de.Msg, "must be a whole number >= 1") {
				t.Errorf("message %q should name maxParallelDimensions and the whole-number rule", de.Msg)
			}
		})
	}
}

// TestReviewPrepareZeroStartedDimensions pins that a run where no dimension
// matches writes "waves": [] (never null), wave_count 0, and the zero-wave
// next text.
func TestReviewPrepareZeroStartedDimensions(t *testing.T) {
	root := newReviewFixture(t, map[string]string{"src/a.go": "package main\n"}, map[string]string{
		"dim.md": `---
name: py-only
description: Dimension
triggers:
  - "**/*.py"
severity: high
---
Review.
`,
	})

	out, m := readReviewManifest(t, root)

	raw, err := os.ReadFile(out.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	waves, ok := generic["waves"].([]any)
	if !ok || len(waves) != 0 {
		t.Errorf("manifest waves = %#v, want []", generic["waves"])
	}
	if out.Summary.WaveCount != 0 || m.Summary.WaveCount != 0 {
		t.Errorf("wave_count out/manifest = %d/%d, want 0/0", out.Summary.WaveCount, m.Summary.WaveCount)
	}
	if want := reviewWaveNext(out.ManifestPath, 0, false); out.Next != want {
		t.Errorf("next = %q, want %q", out.Next, want)
	}
	// Zero waves: no ledger, so no run id and no run.meta.
	if m.RunID != "" {
		t.Errorf("run_id = %q, want \"\"", m.RunID)
	}
	assertNoReviewLedger(t, root)
}

// TestReviewPrepareOldMaxDimensionsKey pins that the old [review]
// maxDimensions key stops review_prepare with the rename DomainError, also
// when maxParallelDimensions is set too.
func TestReviewPrepareOldMaxDimensionsKey(t *testing.T) {
	root := newReviewFixture(t, map[string]string{"src/a.go": "package main\n"}, map[string]string{
		"dim.md": `---
name: dim-a
description: Dimension
triggers:
  - "**/*.go"
severity: medium
---
Review.
`,
	})
	tests := []struct {
		name      string
		localToml string
	}{
		{name: "old key only", localToml: "[review]\nmaxDimensions = 8\n"},
		{name: "both keys", localToml: "[review]\nmaxDimensions = 8\nmaxParallelDimensions = 8\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), tc.localToml)

			_, err := reviewPrepare(root, root, ReviewPrepareIn{SkipConfigCheck: true, Target: "main"})
			var de *mcpserver.DomainError
			if !errors.As(err, &de) {
				t.Fatalf("expected a *mcpserver.DomainError, got %T: %v", err, err)
			}
			if !strings.HasPrefix(de.Msg, "[review] maxDimensions in ") || !strings.Contains(de.Msg, "was renamed to maxParallelDimensions. It now sets how many review agents run at the same time. Every dimension runs.") {
				t.Errorf("message = %q, want the rename message", de.Msg)
			}
			if de.Suggestion != "Rename the key to maxParallelDimensions (keep the value), then retry review_prepare." {
				t.Errorf("suggestion = %q, want the rename suggestion", de.Suggestion)
			}
		})
	}
}

// TestReviewPrepareMalformedLocalToml pins that an unparsable local.toml
// stops review_prepare with an InfraError rather than silently falling back
// to the default scope and cap.
func TestReviewPrepareMalformedLocalToml(t *testing.T) {
	root := newReviewFixture(t, map[string]string{"src/a.go": "package main\n"}, map[string]string{
		"dim.md": `---
name: dim-a
description: Dimension
triggers:
  - "**/*.go"
severity: medium
---
Review.
`,
	})
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "[review\nmaxParallelDimensions = 8\n")

	_, err := reviewPrepare(root, root, ReviewPrepareIn{SkipConfigCheck: true, Target: "main"})
	if err == nil {
		t.Fatal("expected an error for a malformed local.toml")
	}
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("expected a *mcpserver.InfraError, got %T: %v", err, err)
	}
	if ie.Suggestion == "" {
		t.Error("expected a non-empty Suggestion")
	}
}

// TestReviewPrepareEveryStartedDimensionGetsFiles pins that the parallel
// limit never withholds files: with a limit of 1 and three started
// dimensions, every dimension gets a .diff and a .slice.json file.
func TestReviewPrepareEveryStartedDimensionGetsFiles(t *testing.T) {
	dims := map[string]string{}
	for i := 0; i < 3; i++ {
		dims[fmt.Sprintf("dim-%02d.md", i)] = fmt.Sprintf(`---
name: dim-%02d
description: Dimension %d
triggers:
  - "**/*.go"
severity: medium
---
Review.
`, i, i)
	}
	root := newReviewFixture(t, map[string]string{"src/a.go": "package main\n"}, dims)
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "[review]\nmaxParallelDimensions = 1\n")

	_, m := readReviewManifest(t, root)

	if len(m.Dimensions) != 3 {
		t.Fatalf("dimensions = %d, want 3", len(m.Dimensions))
	}
	if fmt.Sprint(waveSizes(m.Waves)) != "[1 1 1]" {
		t.Errorf("wave sizes = %v, want [1 1 1]", waveSizes(m.Waves))
	}
	for _, d := range m.Dimensions {
		if d.Status != "ACTIVE" {
			t.Errorf("%s status = %q, want ACTIVE", d.Name, d.Status)
		}
		if d.DiffFile == nil {
			t.Errorf("%s: diff_file = null, want a path", d.Name)
		}
		if d.SliceFile == nil {
			t.Errorf("%s: slice_file = null, want a path", d.Name)
		}
		for _, ext := range []string{".diff", ".slice.json"} {
			p := filepath.Join(m.DiffDir, d.Name+ext)
			if _, err := os.Stat(p); err != nil {
				t.Errorf("%s: %s not written: %v", d.Name, p, err)
			}
		}
	}
}

// TestReviewPrepareCritiqueCoversTruncated pins that over_broad_dimensions
// and overlapping_pairs check TRUNCATED dimensions too. Two dimensions match
// every changed file, and the diff byte cap makes both TRUNCATED.
func TestReviewPrepareCritiqueCoversTruncated(t *testing.T) {
	bigBody := strings.Repeat("x", 4000)
	files := map[string]string{}
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		files["src/"+name] = fmt.Sprintf("package main\n// %s\n", bigBody)
	}
	dims := map[string]string{}
	for _, name := range []string{"alpha", "beta"} {
		dims[name+".md"] = fmt.Sprintf(`---
name: %s
description: %s review
triggers:
  - "**/*.go"
severity: medium
---
Review.
`, name, name)
	}
	root := newReviewFixture(t, files, dims)

	_, m := readReviewManifest(t, root)

	for _, d := range m.Dimensions {
		if d.Status != "TRUNCATED" {
			t.Fatalf("%s: status = %s, want TRUNCATED (fixture must trip the byte cap)", d.Name, d.Status)
		}
	}
	over := m.PlanCritique.OverBroadDimensions
	if len(over) != 2 {
		t.Errorf("over_broad_dimensions = %v, want [alpha beta]", over)
	}
	pairs := m.PlanCritique.OverlappingPairs
	if len(pairs) != 1 || len(pairs[0]) != 2 || pairs[0][0] != "alpha" || pairs[0][1] != "beta" {
		t.Errorf("overlapping_pairs = %v, want [[alpha beta]]", pairs)
	}
}

// TestReviewPrepareMaxFilesFooter pins that a dimension truncated by
// max-files gets a footer in its .diff file listing the dropped files, so
// the reviewer agent can tell its diff is partial.
func TestReviewPrepareMaxFilesFooter(t *testing.T) {
	root := newReviewFixture(t, map[string]string{
		"src/a.go": "package main\n",
		"src/b.go": "package main\n",
		"src/c.go": "package main\n",
	}, map[string]string{
		"code-quality.md": `---
name: code-quality
description: General code quality review
triggers:
  - "**/*.go"
severity: medium
max-files: 2
---
Review.
`,
	})

	_, m := readReviewManifest(t, root)
	if len(m.Dimensions) != 1 {
		t.Fatalf("dimensions = %d, want 1", len(m.Dimensions))
	}
	d := m.Dimensions[0]
	if d.Status != "TRUNCATED" || !d.Truncated || d.MatchedCount != 2 {
		t.Fatalf("status/truncated/matched = %s/%v/%d, want TRUNCATED/true/2", d.Status, d.Truncated, d.MatchedCount)
	}
	if d.DiffFile == nil {
		t.Fatal("diff_file is null")
	}
	raw, err := os.ReadFile(*d.DiffFile)
	if err != nil {
		t.Fatalf("read diff: %v", err)
	}
	diff := string(raw)
	if !strings.Contains(diff, "# --- Truncated (max-files) ---") {
		t.Errorf("diff has no max-files footer:\n%s", diff)
	}
	if !strings.Contains(diff, "# - src/c.go") {
		t.Errorf("footer does not list the dropped file src/c.go:\n%s", diff)
	}
	if strings.Contains(diff, "diff --git a/src/c.go") {
		t.Errorf("dropped file src/c.go still has hunks in the diff")
	}
}

// reviewPRFixture builds a one-dimension fixture for the PR lookup tests.
func reviewPRFixture(t *testing.T) string {
	t.Helper()
	return newReviewFixture(t, map[string]string{"src/a.go": "package main\n"}, map[string]string{
		"code-quality.md": "---\nname: code-quality\ndescription: Code quality\ntriggers:\n  - \"**/*.go\"\n---\nReview.\n",
	})
}

// TestReviewPrepareOpenPR pins that an open PR on the branch is detected,
// with the owner/repo/number the review skill needs to post a comment.
func TestReviewPrepareOpenPR(t *testing.T) {
	root := reviewPRFixture(t)
	stubReviewGH(t, reviewGHPR("OPEN"))

	out, m := readReviewManifest(t, root)

	if !m.PR.Exists {
		t.Fatal("pr.exists = false, want true for an open PR")
	}
	if m.PR.Number == nil || *m.PR.Number != 42 {
		t.Errorf("pr.number = %v, want 42", m.PR.Number)
	}
	if m.PR.Owner == nil || *m.PR.Owner != "acme" || m.PR.Repo == nil || *m.PR.Repo != "widgets" {
		t.Errorf("pr.owner/repo = %v/%v, want acme/widgets", m.PR.Owner, m.PR.Repo)
	}
	if m.PR.State == nil || *m.PR.State != "OPEN" {
		t.Errorf("pr.state = %v, want OPEN", m.PR.State)
	}
	if !out.Summary.HasPR || !m.Summary.HasPR {
		t.Error("summary.hasPR = false, want true")
	}
	if len(m.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", m.Warnings)
	}
}

// TestReviewPrepareClosedOrMergedPRIgnored pins that only an OPEN PR counts.
// With no open PR, gh pr view returns the branch's newest closed or merged
// PR; that one must not make the skill post to it.
func TestReviewPrepareClosedOrMergedPRIgnored(t *testing.T) {
	for _, state := range []string{"CLOSED", "MERGED"} {
		t.Run(state, func(t *testing.T) {
			root := reviewPRFixture(t)
			stubReviewGH(t, reviewGHPR(state))

			out, m := readReviewManifest(t, root)

			if m.PR.Exists || out.Summary.HasPR {
				t.Errorf("pr.exists/hasPR = %v/%v for a %s PR, want false/false", m.PR.Exists, out.Summary.HasPR, state)
			}
			if len(m.Warnings) != 0 {
				t.Errorf("warnings = %v, want none", m.Warnings)
			}
		})
	}
}

// TestReviewPrepareNoPR pins that a branch without any PR gives
// pr.exists false and no warning.
func TestReviewPrepareNoPR(t *testing.T) {
	root := reviewPRFixture(t)

	_, m := readReviewManifest(t, root)

	if m.PR.Exists {
		t.Error("pr.exists = true, want false")
	}
	if m.Warnings == nil || len(m.Warnings) != 0 {
		t.Errorf("warnings = %#v, want empty array", m.Warnings)
	}
}

// TestReviewPrepareGHFailureWarns pins that a failing gh (here: bad
// credentials) does not fail the tool; it keeps pr.exists false and adds a
// warning that carries gh's message.
func TestReviewPrepareGHFailureWarns(t *testing.T) {
	root := reviewPRFixture(t)
	stubReviewGH(t, reviewGHAuth)

	_, m := readReviewManifest(t, root)

	if m.PR.Exists {
		t.Error("pr.exists = true, want false")
	}
	if len(m.Warnings) != 1 || !strings.Contains(m.Warnings[0], "Bad credentials") {
		t.Errorf("warnings = %v, want one warning carrying gh's error", m.Warnings)
	}
}

func TestReviewPrepareNoChangedFiles(t *testing.T) {
	root := t.TempDir()

	mustRun(t, root, "git", "init")
	mustRun(t, root, "git", "config", "user.email", "test@test.com")
	mustRun(t, root, "git", "config", "user.name", "Test")
	writeFile(t, filepath.Join(root, "README.md"), "# test\n")
	mustRun(t, root, "git", "add", ".")
	mustRun(t, root, "git", "commit", "-m", "init")
	mustRun(t, root, "git", "branch", "-M", "main")

	// No feature branch — no changes relative to main.
	_, err := reviewPrepare(root, root, ReviewPrepareIn{
		SkipConfigCheck: true,
		Target:          "main",
	})
	if err == nil {
		t.Fatal("expected error for no changed files")
	}
	if !strings.Contains(err.Error(), "No changed files") {
		t.Errorf("expected 'No changed files' error, got: %s", err.Error())
	}
}

// TestReviewPrepareBadTargetRef pins that a target ref git cannot resolve
// surfaces git's own failure, naming the ref, instead of the misleading
// "No changed files found".
func TestReviewPrepareBadTargetRef(t *testing.T) {
	root := t.TempDir()

	mustRun(t, root, "git", "init")
	mustRun(t, root, "git", "config", "user.email", "test@test.com")
	mustRun(t, root, "git", "config", "user.name", "Test")
	writeFile(t, filepath.Join(root, "README.md"), "# test\n")
	mustRun(t, root, "git", "add", ".")
	mustRun(t, root, "git", "commit", "-m", "init")
	mustRun(t, root, "git", "branch", "-M", "main")

	_, err := reviewPrepare(root, root, ReviewPrepareIn{
		SkipConfigCheck: true,
		Target:          "no-such-ref",
	})
	if err == nil {
		t.Fatal("expected error for an unresolvable target ref")
	}
	var domErr *mcpserver.DomainError
	if !errors.As(err, &domErr) {
		t.Fatalf("error type = %T, want *mcpserver.DomainError", err)
	}
	msg := err.Error()
	if strings.Contains(msg, "No changed files") {
		t.Errorf("error hides the git failure: %s", msg)
	}
	if !strings.Contains(msg, `"no-such-ref"`) {
		t.Errorf("error does not name the bad ref: %s", msg)
	}
	if !strings.Contains(msg, "unknown revision") {
		t.Errorf("error does not carry git's message: %s", msg)
	}
}

func TestReviewPrepareNoDimensions(t *testing.T) {
	root := t.TempDir()

	mustRun(t, root, "git", "init")
	mustRun(t, root, "git", "config", "user.email", "test@test.com")
	mustRun(t, root, "git", "config", "user.name", "Test")
	writeFile(t, filepath.Join(root, "README.md"), "# test\n")
	mustRun(t, root, "git", "add", ".")
	mustRun(t, root, "git", "commit", "-m", "init")
	mustRun(t, root, "git", "branch", "-M", "main")
	mustRun(t, root, "git", "checkout", "-b", "feature")

	writeFile(t, filepath.Join(root, "new.go"), "package main\n")
	mustRun(t, root, "git", "add", ".")
	mustRun(t, root, "git", "commit", "-m", "add file")

	// No dimension files present.
	_, err := reviewPrepare(root, root, ReviewPrepareIn{
		SkipConfigCheck: true,
		Target:          "main",
	})
	if err == nil {
		t.Fatal("expected error for no dimensions")
	}
	if !strings.Contains(err.Error(), "No review dimensions") {
		t.Errorf("expected 'No review dimensions' error, got: %s", err.Error())
	}
}

// reviewLocalScopeFixture builds a one-dimension fixture whose review scope
// is set to scope in .sdlc-v2/local.toml, with one staged, uncommitted file.
func reviewLocalScopeFixture(t *testing.T, scope string) string {
	t.Helper()
	root := reviewPRFixture(t)
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "[review]\nscope = \""+scope+"\"\n")
	writeFile(t, filepath.Join(root, "src/b.go"), "package main\n")
	mustRun(t, root, "git", "add", "src/b.go")
	return root
}

// readReviewManifestIn runs reviewPrepare against root with in and returns
// the decoded manifest.
func readReviewManifestIn(t *testing.T, root string, in ReviewPrepareIn) reviewManifest {
	t.Helper()
	out, err := reviewPrepare(root, root, in)
	if err != nil {
		t.Fatalf("reviewPrepare failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(out.ManifestPath)) })
	raw, err := os.ReadFile(out.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m reviewManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	return m
}

// TestReviewPrepareLocalScopeSkipsPRLookup pins that the staged and working
// scopes do not look up a PR: a review of uncommitted changes must not be
// offered for posting to the branch's PR. The fake gh fails on every call,
// so a lookup that ran would leave a warning.
func TestReviewPrepareLocalScopeSkipsPRLookup(t *testing.T) {
	for _, scope := range []string{"staged", "working"} {
		t.Run(scope, func(t *testing.T) {
			root := reviewLocalScopeFixture(t, scope)
			stubReviewGH(t, "#!/bin/sh\necho \"gh must not run: $*\" >&2\nexit 3\n")

			m := readReviewManifestIn(t, root, ReviewPrepareIn{SkipConfigCheck: true})

			if m.Scope != scope {
				t.Fatalf("scope = %q, want %q", m.Scope, scope)
			}
			if m.PR.Exists || m.Summary.HasPR {
				t.Errorf("pr.exists/hasPR = %v/%v, want false/false", m.PR.Exists, m.Summary.HasPR)
			}
			if len(m.Warnings) != 0 {
				t.Errorf("warnings = %v, want none (gh must not run for scope %s)", m.Warnings, scope)
			}
		})
	}
}

// TestReviewPrepareWorktreeScopeSkipsPRLookup pins that the worktree scope,
// which diffs the base ref against the working tree and so includes
// uncommitted changes, does not look up a PR either. It still keeps its base
// ref. The fake gh fails on every call, so a lookup that ran would leave a
// warning.
func TestReviewPrepareWorktreeScopeSkipsPRLookup(t *testing.T) {
	root := reviewLocalScopeFixture(t, "worktree")
	stubReviewGH(t, "#!/bin/sh\necho \"gh must not run: $*\" >&2\nexit 3\n")

	m := readReviewManifestIn(t, root, ReviewPrepareIn{SkipConfigCheck: true, Target: "main"})

	if m.Scope != "worktree" {
		t.Fatalf("scope = %q, want worktree", m.Scope)
	}
	if m.BaseBranch == nil || *m.BaseBranch != "main" {
		t.Errorf("base_branch = %v, want main", m.BaseBranch)
	}
	if m.Git.ChangedFilesCount < 2 {
		t.Errorf("changed_files_count = %d, want the committed file plus the staged src/b.go", m.Git.ChangedFilesCount)
	}
	if m.PR.Exists || m.Summary.HasPR {
		t.Errorf("pr.exists/hasPR = %v/%v, want false/false", m.PR.Exists, m.Summary.HasPR)
	}
	if len(m.Warnings) != 0 {
		t.Errorf("warnings = %v, want none (gh must not run for scope worktree)", m.Warnings)
	}
}

// TestReviewPrepareLocalScopeIgnoresTarget pins that target is ignored for
// the staged and working scopes: those scopes diff against no base ref, so
// the manifest must not claim one was used.
func TestReviewPrepareLocalScopeIgnoresTarget(t *testing.T) {
	for _, scope := range []string{"staged", "working"} {
		t.Run(scope, func(t *testing.T) {
			root := reviewLocalScopeFixture(t, scope)

			m := readReviewManifestIn(t, root, ReviewPrepareIn{SkipConfigCheck: true, Target: "main"})

			if m.BaseBranch != nil {
				t.Errorf("base_branch = %q, want null for scope %s", *m.BaseBranch, scope)
			}
			if m.Git.ChangedFilesCount != 1 {
				t.Errorf("changed_files_count = %d, want 1 (the staged src/b.go only)", m.Git.ChangedFilesCount)
			}
		})
	}
}

// TestReviewPrepareUnreadableDimensionsDirIsInfraError pins that a
// review-dimensions folder that exists but cannot be listed is reported as
// an InfraError, not folded into "No review dimensions found".
func TestReviewPrepareUnreadableDimensionsDirIsInfraError(t *testing.T) {
	root := newReviewFixture(t, map[string]string{"src/a.go": "package main\n"}, nil)
	writeFile(t, filepath.Join(root, paths.DataDir, "review-dimensions"), "not a directory")

	_, err := reviewPrepare(root, root, ReviewPrepareIn{SkipConfigCheck: true, Target: "main"})
	var infra *mcpserver.InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("err = %v (%T), want *mcpserver.InfraError", err, err)
	}
	if !strings.HasPrefix(infra.Msg, "list ") {
		t.Errorf("Msg = %q, want it to start with \"list \"", infra.Msg)
	}
	if strings.Contains(err.Error(), "No review dimensions") {
		t.Errorf("error hides the read failure: %s", err.Error())
	}
}

// ---------------------------------------------------------------------------
// saveReviewComment
// ---------------------------------------------------------------------------

func TestSaveReviewComment_HappyPath(t *testing.T) {
	root := t.TempDir()

	mustRun(t, root, "git", "init")
	mustRun(t, root, "git", "config", "user.email", "test@test.com")
	mustRun(t, root, "git", "config", "user.name", "Test")
	writeFile(t, filepath.Join(root, "README.md"), "# test\n")
	mustRun(t, root, "git", "add", ".")
	mustRun(t, root, "git", "commit", "-m", "init")
	mustRun(t, root, "git", "checkout", "-b", "feature/foo.bar")

	out, err := saveReviewComment(root, root, ReviewPrepareIn{
		SaveReview: true,
		Content:    "## Review\n\nLooks fine.\n",
	})
	if err != nil {
		t.Fatalf("saveReviewComment: %v", err)
	}
	if !out.Saved {
		t.Error("expected Saved=true")
	}
	if out.Next == "" {
		t.Error("expected Next to be populated")
	}

	// Branch name "feature/foo.bar" must be sanitized to a bare filename
	// (reviewBranchUnsafeRe replaces every non [a-zA-Z0-9_-] char with "-").
	date := time.Now().UTC().Format("2006-01-02")
	wantPath := filepath.Join(root, paths.DataDir, "reviews", fmt.Sprintf("feature-foo-bar-%s.md", date))
	got, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("expected sanitized-branch file %s, read err: %v", wantPath, err)
	}
	if string(got) != "## Review\n\nLooks fine.\n" {
		t.Errorf("written content = %q, want the passed content verbatim", got)
	}
}

func TestSaveReviewComment_EmptyContent_Errors(t *testing.T) {
	root := t.TempDir()

	_, err := saveReviewComment(root, root, ReviewPrepareIn{SaveReview: true})
	if err == nil {
		t.Fatal("expected error when content is empty")
	}
	if _, ok := err.(*mcpserver.DomainError); !ok {
		t.Errorf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// Base branch resolution ([git] baseBranch in config.toml)
// ---------------------------------------------------------------------------

// newReviewBaseBranchFixture builds a repo where "develop" diverges from
// "main" by one file (src/dev.go), and "feature" branches off "develop"
// and adds a second file (src/app.go). Both main and develop are pushed to
// a bare "origin" remote; feature is never pushed. Because feature...HEAD
// is a three-dot (merge-base) diff, diffing against develop yields 1
// changed file (src/app.go) and diffing against main yields 2 (src/dev.go
// and src/app.go) -- the file count discriminates which base was actually
// used, not just the label recorded in the manifest. It returns the repo
// root.
func newReviewBaseBranchFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	mustRun(t, root, "git", "init")
	mustRun(t, root, "git", "config", "user.email", "test@test.com")
	mustRun(t, root, "git", "config", "user.name", "Test")
	writeFile(t, filepath.Join(root, "README.md"), "# test\n")
	mustRun(t, root, "git", "add", ".")
	mustRun(t, root, "git", "commit", "-m", "init")
	mustRun(t, root, "git", "branch", "-M", "main")

	mustRun(t, root, "git", "checkout", "-b", "develop")
	writeFile(t, filepath.Join(root, "src", "dev.go"), "package main\nfunc dev() {}\n")
	mustRun(t, root, "git", "add", ".")
	mustRun(t, root, "git", "commit", "-m", "add dev.go")

	// Bare "origin" carrying both main and develop, but not feature. Its
	// HEAD is repointed at main (the bare clone's HEAD otherwise follows
	// whatever branch was checked out in root at clone time, i.e. develop,
	// and a bare repo refuses to delete its own current branch -- which the
	// "missing on origin" subtest below needs to do to develop).
	bareOrigin := t.TempDir()
	mustRun(t, bareOrigin, "git", "clone", "--bare", root, ".")
	mustRun(t, bareOrigin, "git", "symbolic-ref", "HEAD", "refs/heads/main")
	mustRun(t, root, "git", "remote", "add", "origin", bareOrigin)

	mustRun(t, root, "git", "checkout", "-b", "feature")
	writeFile(t, filepath.Join(root, "src", "app.go"), "package main\nfunc main() {}\n")
	mustRun(t, root, "git", "add", ".")
	mustRun(t, root, "git", "commit", "-m", "add app.go")

	dimDir := filepath.Join(root, paths.DataDir, "review-dimensions")
	writeFile(t, filepath.Join(dimDir, "code-quality.md"), `---
name: code-quality
description: General code quality review
triggers:
  - "**/*.go"
severity: medium
---
Review the code for quality issues.
`)
	stubReviewGH(t, reviewGHNoPR)
	return root
}

// writeReviewConfigGitSection writes a .sdlc-v2/config.toml with a single
// [git] baseBranch key, the minimal config reviewPrepare's
// config.GitBaseBranch lookup needs.
func writeReviewConfigGitSection(t *testing.T, root, baseBranch string) {
	t.Helper()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"),
		fmt.Sprintf("[git]\nbaseBranch = %q\n", baseBranch))
}

// TestReviewBaseBranchConfig covers reviewPrepare's default-base-branch
// resolution: [git] baseBranch from config.toml wins over the repository
// default branch, an explicit target still wins over config, and a
// configured branch missing on origin fails loudly instead of silently
// falling back to the repository default.
func TestReviewBaseBranchConfig(t *testing.T) {
	t.Run("configured baseBranch used when no target", func(t *testing.T) {
		root := newReviewBaseBranchFixture(t)
		writeReviewConfigGitSection(t, root, "develop")

		out, err := reviewPrepare(root, root, ReviewPrepareIn{SkipConfigCheck: true})
		if err != nil {
			t.Fatalf("reviewPrepare failed: %v", err)
		}
		raw, err := os.ReadFile(out.ManifestPath)
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		var manifest reviewManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatalf("unmarshal manifest: %v", err)
		}
		if manifest.BaseBranch == nil || *manifest.BaseBranch != "develop" {
			t.Errorf("manifest base_branch = %v, want \"develop\"", manifest.BaseBranch)
		}
		// Diffing against develop sees only src/app.go (feature's own commit).
		if manifest.Summary.TotalChangedFiles != 1 {
			t.Errorf("TotalChangedFiles = %d, want 1 (diff against develop)", manifest.Summary.TotalChangedFiles)
		}
	})

	t.Run("explicit target wins over config", func(t *testing.T) {
		root := newReviewBaseBranchFixture(t)
		writeReviewConfigGitSection(t, root, "develop")

		out, err := reviewPrepare(root, root, ReviewPrepareIn{SkipConfigCheck: true, Target: "main"})
		if err != nil {
			t.Fatalf("reviewPrepare failed: %v", err)
		}
		raw, err := os.ReadFile(out.ManifestPath)
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		var manifest reviewManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatalf("unmarshal manifest: %v", err)
		}
		if manifest.BaseBranch == nil || *manifest.BaseBranch != "main" {
			t.Errorf("manifest base_branch = %v, want \"main\" (explicit target)", manifest.BaseBranch)
		}
		// Diffing against main sees both src/dev.go and src/app.go.
		if manifest.Summary.TotalChangedFiles != 2 {
			t.Errorf("TotalChangedFiles = %d, want 2 (diff against main)", manifest.Summary.TotalChangedFiles)
		}
	})

	t.Run("configured branch missing on origin fails loudly", func(t *testing.T) {
		root := newReviewBaseBranchFixture(t)
		// "develop" exists locally but is removed from origin below, so a
		// silent fallback to the local branch -- rather than the required
		// loud failure -- would make this diff silently succeed.
		writeReviewConfigGitSection(t, root, "develop")
		mustRun(t, root, "git", "push", "origin", "--delete", "develop")

		_, err := reviewPrepare(root, root, ReviewPrepareIn{SkipConfigCheck: true})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		domainErr, ok := err.(*mcpserver.DomainError)
		if !ok {
			t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
		}
		wantMsg := `base branch "develop" not found on origin`
		if domainErr.Msg != wantMsg {
			t.Errorf("error message = %q, want %q", domainErr.Msg, wantMsg)
		}
		wantSuggestion := "Push the branch, fix [git] baseBranch in .sdlc-v2/config.toml, or pass target explicitly."
		if domainErr.Suggestion != wantSuggestion {
			t.Errorf("suggestion = %q, want %q", domainErr.Suggestion, wantSuggestion)
		}
	})
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func mustRun(t *testing.T, dir, cmd string, args ...string) {
	t.Helper()
	if _, err := execRun(dir, cmd, args...); err != nil {
		t.Fatalf("%s %s: %v", cmd, strings.Join(args, " "), err)
	}
}

func execRun(dir, name string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %s", err, string(out))
	}
	return string(out), nil
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// Review run plan: run_id, worker_id, and run.meta
// ---------------------------------------------------------------------------

// assertNoReviewLedger fails when review_prepare created any ledger run
// folder under root.
func assertNoReviewLedger(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, paths.DataDir, paths.RunsSubdir, "ledger"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		t.Fatalf("read ledger dir: %v", err)
	}
	for _, e := range entries {
		t.Errorf("ledger run folder %q exists, want none", e.Name())
	}
}

// reviewRunPlanDims returns three dimension files: one critical and two
// medium, so maxParallelDimensions = 2 plans waves [[Security Review, docs],
// [code-quality]] (critical first, then dimension file order).
func reviewRunPlanDims() map[string]string {
	dim := func(name, severity string) string {
		return fmt.Sprintf("---\nname: %s\ndescription: Dimension\ntriggers:\n  - \"**/*.go\"\nseverity: %s\n---\nReview.\n", name, severity)
	}
	return map[string]string{
		"security.md": dim("Security Review", "critical"),
		"quality.md":  dim("code-quality", "medium"),
		"docs.md":     dim("docs", "medium"),
	}
}

// newReviewRunPlanFixture builds a fixture with reviewRunPlanDims and a
// limit of 2 agents at the same time. It points TMPDIR at a test folder, so
// the os.MkdirTemp review folder of a failed call cannot leak outside
// t.TempDir.
func newReviewRunPlanFixture(t *testing.T) string {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	root := newReviewFixture(t, map[string]string{"src/a.go": "package main\n"}, reviewRunPlanDims())
	writeFile(t, filepath.Join(root, paths.DataDir, "local.toml"), "[review]\nmaxParallelDimensions = 2\n")
	return root
}

// TestReviewWorkerID pins the worker id rule: lowercase, then each run of
// characters outside [a-z0-9_-] becomes one "-".
func TestReviewWorkerID(t *testing.T) {
	tests := map[string]string{
		"Security Review":       "security-review",
		"code-quality":          "code-quality",
		"API  /  Contracts!!":   "api-contracts-",
		"snake_case_Dim":        "snake_case_dim",
		"Perf.Review (backend)": "perf-review-backend-",
	}
	for name, want := range tests {
		if got := reviewWorkerID(name); got != want {
			t.Errorf("reviewWorkerID(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestReviewStopReasons pins the run.meta stop reason values.
func TestReviewStopReasons(t *testing.T) {
	got := []string{reviewStopStalled, reviewStopMissing, reviewStopUnstopped}
	want := []string{"stalled", "missing", "unstopped"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("stop reasons = %v, want %v", got, want)
	}
}

// TestReviewPrepareWritesRunMeta pins the run plan of a normal run: run_id
// comes from the manifest timestamp, each dimension has a worker_id, and
// run.meta lists the worker ids by wave, every planned dimension with its
// 1-based wave, the branch, startedAt, and the in_progress ship run id.
func TestReviewPrepareWritesRunMeta(t *testing.T) {
	root := newReviewRunPlanFixture(t)
	shipRunID := seedShipReviewStep(t, root, "feature", StepInProgress)

	out, m := readReviewManifest(t, root)

	wantRunID := "review-" + regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(m.Timestamp, "-")
	if m.RunID != wantRunID {
		t.Errorf("run_id = %q, want %q", m.RunID, wantRunID)
	}
	if !regexp.MustCompile(`^review-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}Z$`).MatchString(m.RunID) {
		t.Errorf("run_id = %q, want review-YYYY-MM-DDTHH-MM-SSZ", m.RunID)
	}
	for _, d := range m.Dimensions {
		if want := reviewWorkerID(d.Name); d.WorkerID != want {
			t.Errorf("%s worker_id = %q, want %q", d.Name, d.WorkerID, want)
		}
	}
	if want := reviewWaveNext(out.ManifestPath, 2, false); out.Next != want {
		t.Errorf("next = %q, want %q", out.Next, want)
	}

	// review_prepare writes only gitignored or untracked state: the tracked
	// tree stays clean.
	st, err := execRun(root, "git", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if st != "" {
		t.Errorf("tracked tree not clean after review_prepare:\n%s", st)
	}

	meta, _ := readLedgerRunMeta(t, root, m.RunID)
	if meta.Branch != "feature" {
		t.Errorf("run.meta branch = %q, want feature", meta.Branch)
	}
	if meta.StartedAt != m.Timestamp {
		t.Errorf("run.meta startedAt = %q, want the manifest timestamp %q", meta.StartedAt, m.Timestamp)
	}
	if meta.ShipRunID != shipRunID {
		t.Errorf("run.meta shipRunId = %q, want %q", meta.ShipRunID, shipRunID)
	}
	if got, want := fmt.Sprint(meta.Waves), "[[security-review docs] [code-quality]]"; got != want {
		t.Errorf("run.meta waves = %s, want %s", got, want)
	}
	wantDims := []reviewRunMetaDimension{
		{Name: "Security Review", WorkerID: "security-review", Wave: 1},
		{Name: "docs", WorkerID: "docs", Wave: 1},
		{Name: "code-quality", WorkerID: "code-quality", Wave: 2},
	}
	if fmt.Sprint(meta.Dimensions) != fmt.Sprint(wantDims) {
		t.Errorf("run.meta dimensions = %+v, want %+v", meta.Dimensions, wantDims)
	}
}

// TestReviewPrepareRunMetaNoShipRun pins that run.meta has no shipRunId when
// the ship review step is not in_progress.
func TestReviewPrepareRunMetaNoShipRun(t *testing.T) {
	root := newReviewRunPlanFixture(t)
	seedShipReviewStep(t, root, "feature", "pending")

	_, m := readReviewManifest(t, root)

	_, raw := readLedgerRunMeta(t, root, m.RunID)
	if strings.Contains(string(raw), "shipRunId") {
		t.Errorf("run.meta = %s, want no shipRunId", raw)
	}
}

// TestReviewPrepareShipStateUnreadable pins that a ship state lookup that
// fails does not stop the review: run.meta has no shipRunId, and the result
// warnings and the manifest warnings say so.
func TestReviewPrepareShipStateUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := newReviewRunPlanFixture(t)
	// No read permission on the state folder makes the ship state lookup
	// fail. Write and search permission keep the ledger write working.
	runs := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatalf("mkdir runs: %v", err)
	}
	if err := os.Chmod(runs, 0o300); err != nil {
		t.Fatalf("chmod runs: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(runs, 0o755) })

	out, m := readReviewManifest(t, root)

	const want = "the ship state could not be read"
	hasWarning := func(list []string) bool {
		for _, w := range list {
			if strings.Contains(w, want) {
				return true
			}
		}
		return false
	}
	if !hasWarning(out.Warnings) {
		t.Errorf("result warnings = %q, want one that contains %q", out.Warnings, want)
	}
	if !hasWarning(m.Warnings) {
		t.Errorf("manifest warnings = %q, want one that contains %q", m.Warnings, want)
	}
	_, raw := readLedgerRunMeta(t, root, m.RunID)
	if strings.Contains(string(raw), "shipRunId") {
		t.Errorf("run.meta = %s, want no shipRunId", raw)
	}
}

// TestReviewPrepareWritesNothingTracked pins that review_prepare leaves
// `git status --porcelain` empty in a repo that setup_init seeded, in
// manifest mode (which writes run.meta) and in save mode. Untracked files
// count too: run.meta and the saved review must land in ignored paths.
func TestReviewPrepareWritesNothingTracked(t *testing.T) {
	root := newReviewRunPlanFixture(t)
	if _, err := setupInit(root, SetupInitIn{}); err != nil {
		t.Fatalf("setupInit fixture seed: %v", err)
	}
	mustRun(t, root, "git", "add", "-A")
	mustRun(t, root, "git", "commit", "-m", "baseline")

	assertClean := func(when string) {
		t.Helper()
		st, err := execRun(root, "git", "status", "--porcelain")
		if err != nil {
			t.Fatalf("git status %s: %v", when, err)
		}
		if st != "" {
			t.Errorf("git status not empty %s:\n%s", when, st)
		}
	}
	assertClean("after the baseline commit")

	_, m := readReviewManifest(t, root)
	if m.RunID == "" {
		t.Fatal("run_id is empty: review_prepare wrote no run.meta, so this test checks nothing")
	}
	assertClean("after review_prepare in manifest mode")

	out, err := reviewPrepare(root, root, ReviewPrepareIn{SaveReview: true, Content: "test review comment"})
	if err != nil {
		t.Fatalf("reviewPrepare save mode: %v", err)
	}
	if !out.Saved {
		t.Fatal("save mode saved nothing, so this test checks nothing")
	}
	assertClean("after review_prepare in save mode")
}

// TestReviewPrepareRunMetaSurvivesCheckin pins that a later ledger_checkin
// does not change the run.meta that review_prepare wrote.
func TestReviewPrepareRunMetaSurvivesCheckin(t *testing.T) {
	root := newReviewRunPlanFixture(t)
	_, m := readReviewManifest(t, root)
	_, before := readLedgerRunMeta(t, root, m.RunID)

	ledgerCheckin(t, root, root, ExecuteStateIn{RunID: m.RunID, WorkerID: "security-review"}, time.Now().Add(time.Hour))

	_, after := readLedgerRunMeta(t, root, m.RunID)
	if string(after) != string(before) {
		t.Errorf("run.meta changed after ledger_checkin:\nbefore %s\nafter  %s", before, after)
	}
}

// TestReviewPrepareDryRun pins that a dry run writes no run.meta, has an
// empty run_id, and returns the dry-run next text.
func TestReviewPrepareDryRun(t *testing.T) {
	root := newReviewRunPlanFixture(t)

	out, err := reviewPrepare(root, root, ReviewPrepareIn{SkipConfigCheck: true, Target: "main", DryRun: true})
	if err != nil {
		t.Fatalf("reviewPrepare failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(out.ManifestPath)) })
	raw, err := os.ReadFile(out.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if runID, ok := generic["run_id"].(string); !ok || runID != "" {
		t.Errorf("manifest run_id = %#v, want \"\"", generic["run_id"])
	}
	if out.Summary.WaveCount != 2 {
		t.Errorf("wave_count = %d, want 2", out.Summary.WaveCount)
	}
	if want := "Dry run: print the plan from the manifest and stop. No ledger exists."; out.Next != want {
		t.Errorf("next = %q, want %q", out.Next, want)
	}
	assertNoReviewLedger(t, root)
}

// TestReviewPrepareSaveModeIgnoresDryRun pins that saveReview wins over
// dryRun: the call runs save mode and writes no run.meta.
func TestReviewPrepareSaveModeIgnoresDryRun(t *testing.T) {
	root := newReviewRunPlanFixture(t)

	out, err := reviewPrepare(root, root, ReviewPrepareIn{SaveReview: true, DryRun: true, Content: "review body"})
	if err != nil {
		t.Fatalf("reviewPrepare failed: %v", err)
	}
	if !out.Saved || out.ManifestPath != "" {
		t.Errorf("saved/manifestPath = %v/%q, want true/\"\"", out.Saved, out.ManifestPath)
	}
	assertNoReviewLedger(t, root)
}

// TestReviewPrepareRunMetaWriteFails pins that a failed run.meta write
// returns an InfraError with a Suggestion and no manifest path.
func TestReviewPrepareRunMetaWriteFails(t *testing.T) {
	root := newReviewRunPlanFixture(t)
	// A regular file where the ledger folder goes makes MkdirAll fail.
	ledgerPath := filepath.Join(root, paths.DataDir, paths.RunsSubdir, "ledger")
	writeFile(t, ledgerPath, "not a dir\n")

	out, err := reviewPrepare(root, root, ReviewPrepareIn{SkipConfigCheck: true, Target: "main"})
	var infra *mcpserver.InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("err = %v (%T), want *mcpserver.InfraError", err, err)
	}
	if want := "Check write access to .sdlc-v2/runs/ledger/ and call review_prepare again."; infra.Suggestion != want {
		t.Errorf("suggestion = %q, want %q", infra.Suggestion, want)
	}
	if out.ManifestPath != "" {
		t.Errorf("manifestPath = %q, want \"\"", out.ManifestPath)
	}
	// No ledger folder was created: the blocking file is still a file.
	if fi, err := os.Stat(ledgerPath); err != nil || fi.IsDir() {
		t.Errorf("ledger path stat = %v, %v; want the regular file to stay", fi, err)
	}
}

// useReviewWriteJSON replaces reviewWriteJSON for one test and restores it
// when the test ends.
func useReviewWriteJSON(t *testing.T, fn func(path string, v any) error) {
	t.Helper()
	prev := reviewWriteJSON
	reviewWriteJSON = fn
	t.Cleanup(func() { reviewWriteJSON = prev })
}

// useReviewCurrentBranch replaces reviewCurrentBranch for one test and
// restores it when the test ends.
func useReviewCurrentBranch(t *testing.T, fn func(dir string) (string, error)) {
	t.Helper()
	prev := reviewCurrentBranch
	reviewCurrentBranch = fn
	t.Cleanup(func() { reviewCurrentBranch = prev })
}

// useReviewGitStatus replaces reviewGitStatus for one test and restores it
// when the test ends.
func useReviewGitStatus(t *testing.T, fn func(dir string) (string, error)) {
	t.Helper()
	prev := reviewGitStatus
	reviewGitStatus = fn
	t.Cleanup(func() { reviewGitStatus = prev })
}

// TestReviewPrepareManifestWriteFails pins the state after a failed manifest
// write: an InfraError, no manifest path, and no ledger folder, because
// run.meta is written only after the manifest.
func TestReviewPrepareManifestWriteFails(t *testing.T) {
	root := newReviewRunPlanFixture(t)
	runMetaWrites := 0
	useReviewWriteJSON(t, func(path string, v any) error {
		if filepath.Base(path) == "manifest.json" {
			return errors.New("disk full")
		}
		runMetaWrites++
		return fsx.AtomicWriteJSON(path, v)
	})

	out, err := reviewPrepare(root, root, ReviewPrepareIn{SkipConfigCheck: true, Target: "main"})
	var infra *mcpserver.InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("err = %v (%T), want *mcpserver.InfraError", err, err)
	}
	if !strings.Contains(infra.Msg, "write manifest") || infra.Suggestion == "" {
		t.Errorf("msg/suggestion = %q/%q, want a write manifest error with a suggestion", infra.Msg, infra.Suggestion)
	}
	if out.ManifestPath != "" {
		t.Errorf("manifestPath = %q, want \"\"", out.ManifestPath)
	}
	if runMetaWrites != 0 {
		t.Errorf("run.meta writes = %d, want 0", runMetaWrites)
	}
	assertNoReviewLedger(t, root)
}

// TestReviewPrepareRunMetaAtomicWriteFails pins the state after the run.meta
// write fails once the ledger folder exists: an InfraError, no manifest
// path, the manifest file still on disk in the temp folder, and no ledger
// run folder, because the call removes the folder it created.
func TestReviewPrepareRunMetaAtomicWriteFails(t *testing.T) {
	root := newReviewRunPlanFixture(t)
	var manifestPath, runMetaPath string
	useReviewWriteJSON(t, func(path string, v any) error {
		if filepath.Base(path) == ledgerRunMetaFile {
			runMetaPath = path
			if _, err := os.Stat(filepath.Dir(path)); err != nil {
				t.Errorf("ledger run folder missing at the run.meta write: %v", err)
			}
			return errors.New("disk full")
		}
		manifestPath = path
		return fsx.AtomicWriteJSON(path, v)
	})

	out, err := reviewPrepare(root, root, ReviewPrepareIn{SkipConfigCheck: true, Target: "main"})
	var infra *mcpserver.InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("err = %v (%T), want *mcpserver.InfraError", err, err)
	}
	if want := "Check write access to .sdlc-v2/runs/ledger/ and call review_prepare again."; infra.Suggestion != want {
		t.Errorf("suggestion = %q, want %q", infra.Suggestion, want)
	}
	if out.ManifestPath != "" {
		t.Errorf("manifestPath = %q, want \"\"", out.ManifestPath)
	}
	if runMetaPath == "" {
		t.Fatal("run.meta write was not attempted")
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("manifest %q not on disk: %v", manifestPath, err)
	}
	if _, err := os.Stat(filepath.Dir(runMetaPath)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ledger run folder stat err = %v, want not exist", err)
	}
	assertNoReviewLedger(t, root)
}

// TestWriteReviewRunMetaKeepsExistingFolder pins that a failed run.meta write
// does not remove a ledger run folder that existed before the call.
func TestWriteReviewRunMetaKeepsExistingFolder(t *testing.T) {
	root := t.TempDir()
	runID := "review-2026-10-08T11-09-18Z"
	worker := ledgerFilePath(root, runID, "docs")
	writeFile(t, worker, "{}\n")
	useReviewWriteJSON(t, func(string, any) error { return errors.New("disk full") })

	if err := writeReviewRunMeta(root, "feature", runID, time.Now(), [][]string{{"docs"}}); err == nil {
		t.Fatal("writeReviewRunMeta err = nil, want the write error")
	}
	if _, err := os.Stat(worker); err != nil {
		t.Errorf("worker file of the existing folder is gone: %v", err)
	}
}

// TestWriteReviewRunMetaEmptyBranch pins the empty-branch arm: run.meta has
// an empty branch and no shipRunId.
func TestWriteReviewRunMetaEmptyBranch(t *testing.T) {
	root := t.TempDir()
	runID := "review-2026-10-08T11-09-18Z"
	startedAt := time.Date(2026, 10, 8, 11, 9, 18, 0, time.UTC)

	if err := writeReviewRunMeta(root, "", runID, startedAt, [][]string{{"Security Review"}}); err != nil {
		t.Fatalf("writeReviewRunMeta: %v", err)
	}
	meta, raw := readLedgerRunMeta(t, root, runID)
	if meta.Branch != "" {
		t.Errorf("branch = %q, want \"\"", meta.Branch)
	}
	if strings.Contains(string(raw), "shipRunId") {
		t.Errorf("run.meta = %s, want no shipRunId", raw)
	}
	if meta.StartedAt != "2026-10-08T11:09:18Z" {
		t.Errorf("startedAt = %q, want 2026-10-08T11:09:18Z", meta.StartedAt)
	}
	want := []reviewRunMetaDimension{{Name: "Security Review", WorkerID: "security-review", Wave: 1}}
	if fmt.Sprint(meta.Dimensions) != fmt.Sprint(want) {
		t.Errorf("dimensions = %+v, want %+v", meta.Dimensions, want)
	}
}

// TestReviewPrepareBranchReadFails pins that a failed branch read becomes a
// warning in the result and the manifest, and that run.meta then has an
// empty branch and no shipRunId, even when the ship review step is
// in_progress.
func TestReviewPrepareGitStatusReadFails(t *testing.T) {
	root := newReviewRunPlanFixture(t)
	useReviewGitStatus(t, func(string) (string, error) { return "", errors.New("status exploded") })

	out, m := readReviewManifest(t, root)

	found := false
	for _, w := range out.Warnings {
		if strings.Contains(w, "uncommitted_changes is reported as false") && strings.Contains(w, "status exploded") {
			found = true
		}
	}
	if !found {
		t.Errorf("result warnings = %q, want one that names the status error and uncommitted_changes", out.Warnings)
	}
	if m.UncommittedChanges {
		t.Errorf("uncommitted_changes = true, want false after a failed status read")
	}
	if m.RunID == "" {
		t.Errorf("run_id is empty: a failed status read must not stop the review")
	}
}

func TestReviewPrepareBranchReadFails(t *testing.T) {
	root := newReviewRunPlanFixture(t)
	seedShipReviewStep(t, root, "feature", StepInProgress)
	useReviewCurrentBranch(t, func(string) (string, error) { return "", errors.New("git exploded") })

	out, m := readReviewManifest(t, root)

	wantPart := "will not join its ship run"
	found := false
	for _, w := range out.Warnings {
		if strings.Contains(w, wantPart) && strings.Contains(w, "git exploded") {
			found = true
		}
	}
	if !found {
		t.Errorf("result warnings = %q, want one that contains %q and the git error", out.Warnings, wantPart)
	}
	if fmt.Sprint(m.Warnings) != fmt.Sprint(out.Warnings) {
		t.Errorf("manifest warnings = %q, want the result warnings %q", m.Warnings, out.Warnings)
	}
	if m.CurrentBranch != "" {
		t.Errorf("current_branch = %q, want \"\"", m.CurrentBranch)
	}
	meta, raw := readLedgerRunMeta(t, root, m.RunID)
	if meta.Branch != "" || strings.Contains(string(raw), "shipRunId") {
		t.Errorf("run.meta = %s, want an empty branch and no shipRunId", raw)
	}
}
