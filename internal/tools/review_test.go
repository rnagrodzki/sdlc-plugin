package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
// refinePlan tests
// ---------------------------------------------------------------------------

func TestRefinePlanUnderCap(t *testing.T) {
	dims := make([]reviewDimWork, 5)
	for i := range dims {
		dims[i].name = "dim-" + string(rune('a'+i))
		dims[i].status = "ACTIVE"
		dims[i].severity = "medium"
	}

	queued := refinePlan(dims)
	if len(queued) != 0 {
		t.Errorf("expected no queued dims, got %v", queued)
	}
	for _, d := range dims {
		if d.status != "ACTIVE" {
			t.Errorf("dim %s should remain ACTIVE", d.name)
		}
	}
}

func TestRefinePlanOverCap(t *testing.T) {
	dims := make([]reviewDimWork, 10)
	severities := []string{"critical", "high", "medium", "low", "info", "medium", "medium", "medium", "low", "info"}
	for i := range dims {
		dims[i].name = "dim-" + string(rune('a'+i))
		dims[i].status = "ACTIVE"
		dims[i].severity = severities[i]
		dims[i].matchedFiles = make([]string, i) // ascending file count
	}

	queued := refinePlan(dims)

	if len(queued) != 2 {
		t.Errorf("expected 2 queued dims, got %d: %v", len(queued), queued)
	}

	activeCount := 0
	for _, d := range dims {
		if d.status == "ACTIVE" || d.status == "TRUNCATED" {
			activeCount++
		}
	}
	if activeCount != 8 {
		t.Errorf("expected 8 active dims after refinement, got %d", activeCount)
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
		{"QUEUED", false},
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
	dimDir := filepath.Join(root, ".sdlc", "review-dimensions")
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

	// Create config files needed by configmigrate.Verify.
	sdlcDir := filepath.Join(root, ".sdlc")
	writeFile(t, filepath.Join(sdlcDir, "config.json"), `{"schemaVersion": 5}`)

	// Run reviewPrepare.
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

	// Read and verify manifest.
	manifestBytes, err := os.ReadFile(out.ManifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
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
