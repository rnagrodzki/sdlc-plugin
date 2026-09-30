package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
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

// TestReviewPrepareDiffByteCapTruncation covers
// review-drift-2026-09-13T-diff-truncation: a dimension can pass the
// matched-file-count cap yet still have its concatenated diff exceed
// difftrunc.DefaultDiffMaxBytes, silently dropping whole files from the
// worker's .diff file. The manifest must flag this via `truncated`/`status`
// rather than staying silent.
func TestReviewPrepareDiffByteCapTruncation(t *testing.T) {
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

// TestReviewPrepareCapCountsTruncated pins that the 8-dimension cap counts
// every dispatched dimension, TRUNCATED included. Ten dimensions that are
// all TRUNCATED by max-files must still leave only 8 to dispatch.
func TestReviewPrepareCapCountsTruncated(t *testing.T) {
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

	dispatched, queued := 0, 0
	for _, d := range m.Dimensions {
		switch d.Status {
		case "ACTIVE", "TRUNCATED":
			dispatched++
		case "QUEUED":
			queued++
		}
	}
	if dispatched != 8 {
		t.Errorf("dispatched dimensions = %d, want 8", dispatched)
	}
	if queued != 2 {
		t.Errorf("queued dimensions = %d, want 2", queued)
	}
	if out.Summary.ActiveDimensions != 8 || out.Summary.QueuedDimensions != 2 {
		t.Errorf("summary active/queued = %d/%d, want 8/2", out.Summary.ActiveDimensions, out.Summary.QueuedDimensions)
	}
	if !m.PlanCritique.DimensionCapApplied {
		t.Error("plan_critique.dimension_cap_applied = false, want true")
	}
	if len(m.PlanCritique.QueuedDimensions) != 2 {
		t.Errorf("plan_critique.queued_dimensions = %v, want 2 names", m.PlanCritique.QueuedDimensions)
	}
}

// TestReviewPrepareQueuedGetsNoFiles pins that a QUEUED dimension is never
// dispatched, so it gets no .diff or .slice.json file and a null diff_file.
func TestReviewPrepareQueuedGetsNoFiles(t *testing.T) {
	dims := map[string]string{}
	for i := 0; i < 10; i++ {
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

	_, m := readReviewManifest(t, root)

	queued := 0
	for _, d := range m.Dimensions {
		if d.Status != "QUEUED" {
			continue
		}
		queued++
		if d.DiffFile != nil {
			t.Errorf("%s: diff_file = %q, want null", d.Name, *d.DiffFile)
		}
		if d.SliceFile != nil {
			t.Errorf("%s: slice_file = %q, want null", d.Name, *d.SliceFile)
		}
		for _, ext := range []string{".diff", ".slice.json"} {
			p := filepath.Join(m.DiffDir, d.Name+ext)
			if _, err := os.Stat(p); err == nil {
				t.Errorf("%s: %s written for a QUEUED dimension", d.Name, p)
			}
		}
	}
	if queued != 2 {
		t.Fatalf("queued dimensions = %d, want 2", queued)
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
