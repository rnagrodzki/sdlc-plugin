package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/difftrunc"
	"github.com/rnagrodzki/sdlc-plugin/internal/dimensions"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

// validScopes mirrors VALID_SCOPES from review.js.
var validScopes = []string{"all", "committed", "staged", "working", "worktree"}

// maxCommitsPerFile caps per-file commit history entries.
const maxCommitsPerFile = 5

// severityRank maps severity levels to priority values for refinePlan sorting.
var severityRank = map[string]int{
	"critical": 5,
	"high":     4,
	"medium":   3,
	"low":      2,
	"info":     1,
}

// maxActiveDimensions is the cap on simultaneously dispatched dimensions.
const maxActiveDimensions = 8

// pluginVersion is the plugin version embedded in manifests. Set by the build
// system via ldflags in cmd/sdlc/main.go; defaults to "unknown" when not wired.
var pluginVersion = "unknown"

// ---------------------------------------------------------------------------
// Input / Output types
// ---------------------------------------------------------------------------

// ReviewPrepareIn is the input for the review_prepare tool.
type ReviewPrepareIn struct {
	SkipConfigCheck bool   `json:"skipConfigCheck" jsonschema_description:"Skips the config-version auto-migration gate normally run before preflight checks. Set only when the caller has already verified or migrated the config."`
	Target          string `json:"target" jsonschema_description:"Base branch/ref to diff against, overriding the repo's detected default branch. Ignored when the configured review scope is \"staged\" or \"working\" (local, non-branch scopes)."`
}

// ReviewPrepareSummary mirrors the summary block of the JS manifest.
type ReviewPrepareSummary struct {
	TotalDimensions     int `json:"total_dimensions"`
	ActiveDimensions    int `json:"active_dimensions"`
	SkippedDimensions   int `json:"skipped_dimensions"`
	QueuedDimensions    int `json:"queued_dimensions"`
	TotalChangedFiles   int `json:"total_changed_files"`
	UncoveredFileCount  int `json:"uncovered_file_count"`
	SuggestedDimensions int `json:"suggested_dimensions"`
}

// ReviewPrepareOut is the output for the review_prepare tool.
type ReviewPrepareOut struct {
	ManifestPath string               `json:"manifestPath"`
	Summary      ReviewPrepareSummary `json:"summary"`
}

// ---------------------------------------------------------------------------
// Internal manifest types (written to JSON file, not returned inline)
// ---------------------------------------------------------------------------

type reviewManifest struct {
	Version            int                   `json:"version"`
	Timestamp          string                `json:"timestamp"`
	SubagentModel      string                `json:"subagent_model"`
	PluginVersion      string                `json:"plugin_version"`
	Scope              string                `json:"scope"`
	BaseBranch         *string               `json:"base_branch"`
	CurrentBranch      string                `json:"current_branch"`
	UncommittedChanges bool                  `json:"uncommitted_changes"`
	Git                reviewManifestGit     `json:"git"`
	PR                 reviewManifestPR      `json:"pr"`
	Dimensions         []reviewDimIndexEntry `json:"dimensions"`
	PlanCritique       reviewPlanCritique    `json:"plan_critique"`
	Summary            ReviewPrepareSummary  `json:"summary"`
	DiffDir            string                `json:"diff_dir"`
}

type reviewManifestGit struct {
	CommitCount       int `json:"commit_count"`
	ChangedFilesCount int `json:"changed_files_count"`
}

type reviewManifestPR struct {
	Exists bool    `json:"exists"`
	Number *int    `json:"number,omitempty"`
	Title  *string `json:"title,omitempty"`
	URL    *string `json:"url,omitempty"`
	State  *string `json:"state,omitempty"`
	Owner  *string `json:"owner,omitempty"`
	Repo   *string `json:"repo,omitempty"`
}

type reviewDimIndexEntry struct {
	Name             string  `json:"name"`
	Description      string  `json:"description"`
	Severity         string  `json:"severity"`
	Model            *string `json:"model"`
	Status           string  `json:"status"`
	RequiresFullDiff bool    `json:"requires_full_diff"`
	Truncated        bool    `json:"truncated"`
	MatchedCount     int     `json:"matched_count"`
	DiffFile         *string `json:"diff_file"`
	SliceFile        *string `json:"slice_file"`
}

type reviewPlanCritique struct {
	UncoveredFiles       []string              `json:"uncovered_files"`
	UncoveredSuggestions []uncoveredSuggestion `json:"uncovered_suggestions"`
	StillUncovered       []string              `json:"still_uncovered"`
	OverBroadDimensions  []string              `json:"over_broad_dimensions"`
	OverlappingPairs     [][]string            `json:"overlapping_pairs"`
	DimensionCapApplied  bool                  `json:"dimension_cap_applied"`
	QueuedDimensions     []string              `json:"queued_dimensions"`
}

type uncoveredSuggestion struct {
	Dimension string   `json:"dimension"`
	Files     []string `json:"files"`
	Reason    string   `json:"reason"`
}

// reviewDimWork is the internal working representation of a loaded dimension.
type reviewDimWork struct {
	name             string
	description      string
	severity         string
	requiresFullDiff bool
	model            *string
	status           string
	matchedFiles     []string
	matchedCount     int
	truncated        bool
	diffFile         *string
	sliceFile        *string
	body             string
	common           string
	fileContext      []fileContextEntry
	warnings         []string
}

type fileContextEntry struct {
	File    string        `json:"file"`
	Commits []commitEntry `json:"commits"`
}

type commitEntry struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
}

type dimSlice struct {
	Body         string             `json:"body"`
	MatchedFiles []string           `json:"matched_files"`
	FileContext  []fileContextEntry `json:"file_context"`
	Warnings     []string           `json:"warnings"`
}

// ---------------------------------------------------------------------------
// Glob matching (port of review.js globToRegex + matchFiles)
// ---------------------------------------------------------------------------

// globToRegex converts a glob pattern to an RE2-compatible regexp string.
func globToRegex(pattern string) string {
	var re strings.Builder
	i := 0
	n := len(pattern)

	for i < n {
		ch := pattern[i]
		switch {
		case ch == '*':
			if i+1 < n && pattern[i+1] == '*' {
				if i+2 < n && pattern[i+2] == '/' {
					re.WriteString("(?:[^/]+/)*")
					i += 3
				} else {
					re.WriteString(".*")
					i += 2
				}
			} else {
				re.WriteString("[^/]*")
				i++
			}
		case ch == '?':
			re.WriteString("[^/]")
			i++
		case ch == '[':
			close := strings.IndexByte(pattern[i+1:], ']')
			if close == -1 {
				re.WriteString("\\[")
				i++
			} else {
				re.WriteString(pattern[i : i+1+close+1])
				i = i + 1 + close + 1
			}
		case strings.ContainsRune(".+^${}()|\\", rune(ch)):
			re.WriteByte('\\')
			re.WriteByte(ch)
			i++
		default:
			re.WriteByte(ch)
			i++
		}
	}
	return "^" + re.String() + "$"
}

// matchFilesResult holds the matched files and whether the list was truncated.
type matchFilesResult struct {
	matched   []string
	truncated bool
}

// matchFiles filters changedFiles against a dimension's trigger/skip-when globs.
func matchFiles(meta map[string]any, changedFiles []string) matchFilesResult {
	triggersRaw, _ := meta["triggers"].([]any)
	skipWhenRaw, _ := meta["skip-when"].([]any)
	maxFiles := 100
	if mf, ok := meta["max-files"].(int); ok && mf > 0 {
		maxFiles = mf
	}

	var triggers []*regexp.Regexp
	for _, t := range triggersRaw {
		if s, ok := t.(string); ok {
			if re, err := regexp.Compile(globToRegex(s)); err == nil {
				triggers = append(triggers, re)
			}
		}
	}

	var skipWhen []*regexp.Regexp
	for _, s := range skipWhenRaw {
		if str, ok := s.(string); ok {
			if re, err := regexp.Compile(globToRegex(str)); err == nil {
				skipWhen = append(skipWhen, re)
			}
		}
	}

	var matched []string
	for _, f := range changedFiles {
		triggerMatch := false
		for _, re := range triggers {
			if re.MatchString(f) {
				triggerMatch = true
				break
			}
		}
		if !triggerMatch {
			continue
		}
		skipMatch := false
		for _, re := range skipWhen {
			if re.MatchString(f) {
				skipMatch = true
				break
			}
		}
		if !skipMatch {
			matched = append(matched, f)
		}
	}

	truncated := len(matched) > maxFiles
	if truncated {
		matched = matched[:maxFiles]
	}

	return matchFilesResult{matched: matched, truncated: truncated}
}

// ---------------------------------------------------------------------------
// Uncovered-file analysis (port of UNCOVERED_PATTERN_CATALOG)
// ---------------------------------------------------------------------------

type uncoveredPatternEntry struct {
	test      func(string) bool
	dimension string
	label     string
}

var uncoveredPatternCatalog = []uncoveredPatternEntry{
	{test: func(f string) bool {
		return regexp.MustCompile(`\.github/workflows/`).MatchString(f) ||
			(regexp.MustCompile(`\.(ya?ml)$`).MatchString(f) && regexp.MustCompile(`(?i)ci|deploy|pipeline`).MatchString(f))
	}, dimension: "ci-cd-pipeline-review", label: "CI/CD workflow"},
	{test: func(f string) bool {
		return regexp.MustCompile(`(?i)Jenkinsfile|\.circleci/`).MatchString(f)
	}, dimension: "ci-cd-pipeline-review", label: "CI/CD pipeline"},
	{test: func(f string) bool {
		return regexp.MustCompile(`(?i)migrations?/`).MatchString(f) || regexp.MustCompile(`\.sql$`).MatchString(f)
	}, dimension: "database-migrations-review", label: "database migration"},
	{test: func(f string) bool {
		return regexp.MustCompile(`(?i)i18n|locales?|translations?/`).MatchString(f)
	}, dimension: "internationalization-review", label: "internationalization"},
	{test: func(f string) bool {
		return regexp.MustCompile(`\.graphql$`).MatchString(f) ||
			regexp.MustCompile(`(?i)openapi|swagger`).MatchString(f) ||
			regexp.MustCompile(`\.proto$`).MatchString(f)
	}, dimension: "api-contract-review", label: "API contract/schema"},
	{test: func(f string) bool {
		return regexp.MustCompile(`\.md$`).MatchString(f) || regexp.MustCompile(`(?i)docs?/`).MatchString(f)
	}, dimension: "documentation-quality-review", label: "documentation"},
	{test: func(f string) bool {
		return regexp.MustCompile(`\.d\.ts$`).MatchString(f) || regexp.MustCompile(`(?i)(?:^|/)types?/`).MatchString(f)
	}, dimension: "type-safety-review", label: "type definition"},
	{test: func(f string) bool {
		return regexp.MustCompile(`(?i)store|state|redux|zustand|pinia`).MatchString(f)
	}, dimension: "state-management-review", label: "state management"},
	{test: func(f string) bool {
		return regexp.MustCompile(`(?i)Dockerfile|docker-compose|\.dockerfile$`).MatchString(f) ||
			regexp.MustCompile(`(?i)terraform|\.tf$|k8s|kubernetes`).MatchString(f)
	}, dimension: "infrastructure-review", label: "infrastructure"},
	{test: func(f string) bool {
		return regexp.MustCompile(`\.lock$`).MatchString(f) ||
			regexp.MustCompile(`package-lock|yarn\.lock|Gemfile\.lock|poetry\.lock`).MatchString(f)
	}, dimension: "dependency-management-review", label: "dependency lockfile"},
	{test: func(f string) bool {
		return regexp.MustCompile(`(?i)android/|ios/|\.swift$|\.kt$|\.dart$`).MatchString(f)
	}, dimension: "mobile-app-review", label: "mobile platform"},
	{test: func(f string) bool {
		return regexp.MustCompile(`\.env(?:\.|$)`).MatchString(f) || regexp.MustCompile(`(?i)(?:^|/)config/`).MatchString(f)
	}, dimension: "configuration-management-review", label: "configuration"},
}

func analyzeUncoveredFiles(uncoveredFiles []string) (suggestions []uncoveredSuggestion, stillUncovered []string) {
	if len(uncoveredFiles) == 0 {
		return nil, nil
	}

	type group struct {
		label string
		files []string
	}
	byDimension := map[string]*group{}
	var dimOrder []string

	for _, f := range uncoveredFiles {
		matched := false
		for _, entry := range uncoveredPatternCatalog {
			if entry.test(f) {
				g, exists := byDimension[entry.dimension]
				if !exists {
					g = &group{label: entry.label}
					byDimension[entry.dimension] = g
					dimOrder = append(dimOrder, entry.dimension)
				}
				g.files = append(g.files, f)
				matched = true
				break
			}
		}
		if !matched {
			stillUncovered = append(stillUncovered, f)
		}
	}

	for _, dim := range dimOrder {
		g := byDimension[dim]
		count := len(g.files)
		suffix := "s"
		if count == 1 {
			suffix = ""
		}
		suggestions = append(suggestions, uncoveredSuggestion{
			Dimension: dim,
			Files:     g.files,
			Reason:    fmt.Sprintf("%d %s file%s not covered by any dimension", count, g.label, suffix),
		})
	}

	return suggestions, stillUncovered
}

// ---------------------------------------------------------------------------
// Plan critique + refinement
// ---------------------------------------------------------------------------

func critiquePlan(dims []reviewDimWork, changedFiles []string) reviewPlanCritique {
	allMatched := map[string]bool{}
	for _, d := range dims {
		for _, f := range d.matchedFiles {
			allMatched[f] = true
		}
	}

	var uncoveredFiles []string
	for _, f := range changedFiles {
		if !allMatched[f] {
			uncoveredFiles = append(uncoveredFiles, f)
		}
	}

	totalCount := len(changedFiles)
	var overBroad []string
	var active []reviewDimWork
	for _, d := range dims {
		if d.status == "ACTIVE" {
			active = append(active, d)
			if totalCount > 0 && float64(len(d.matchedFiles))/float64(totalCount) > 0.8 {
				overBroad = append(overBroad, d.name)
			}
		}
	}

	var overlappingPairs [][]string
	for i := 0; i < len(active); i++ {
		for j := i + 1; j < len(active); j++ {
			a := toStringSet(active[i].matchedFiles)
			b := toStringSet(active[j].matchedFiles)
			if len(a) == len(b) && setsEqual(a, b) {
				overlappingPairs = append(overlappingPairs, []string{active[i].name, active[j].name})
			}
		}
	}

	suggestions, still := analyzeUncoveredFiles(uncoveredFiles)

	return reviewPlanCritique{
		UncoveredFiles:       emptyIfNil(uncoveredFiles),
		UncoveredSuggestions: emptySuggestionsIfNil(suggestions),
		StillUncovered:       emptyIfNil(still),
		OverBroadDimensions:  emptyIfNil(overBroad),
		OverlappingPairs:     emptyPairsIfNil(overlappingPairs),
		DimensionCapApplied:  len(active) > maxActiveDimensions,
	}
}

func refinePlan(dims []reviewDimWork) []string {
	var active []*reviewDimWork
	for i := range dims {
		if dims[i].status == "ACTIVE" {
			active = append(active, &dims[i])
		}
	}
	if len(active) <= maxActiveDimensions {
		return nil
	}

	sort.SliceStable(active, func(i, j int) bool {
		ri := severityRank[active[i].severity]
		if ri == 0 {
			ri = 3
		}
		rj := severityRank[active[j].severity]
		if rj == 0 {
			rj = 3
		}
		diff := rj - ri
		if diff != 0 {
			return diff > 0
		}
		return len(active[i].matchedFiles) < len(active[j].matchedFiles)
	})

	keep := map[string]bool{}
	for _, d := range active[:maxActiveDimensions] {
		keep[d.name] = true
	}

	var queued []string
	for i := range dims {
		if dims[i].status == "ACTIVE" && !keep[dims[i].name] {
			dims[i].status = "QUEUED"
			queued = append(queued, dims[i].name)
		}
	}
	return queued
}

// ---------------------------------------------------------------------------
// Commit context
// ---------------------------------------------------------------------------

func getCommitFileMap(base, dir string) map[string][]commitEntry {
	raw, err := execx.Run("git", []string{
		"log", "--format=COMMIT:%H %s", "--name-only", base + "..HEAD",
	}, execx.Options{Dir: dir})
	if err != nil || raw == "" {
		return nil
	}

	result := map[string][]commitEntry{}
	var current *commitEntry

	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "COMMIT:") {
			rest := line[7:]
			hash := rest
			subject := ""
			if idx := strings.IndexByte(rest, ' '); idx >= 0 {
				hash = rest[:8]
				subject = rest[idx+1:]
			} else if len(hash) > 8 {
				hash = hash[:8]
			}
			current = &commitEntry{Hash: hash, Subject: subject}
		} else if trimmed := strings.TrimSpace(line); trimmed != "" && current != nil {
			commits := result[trimmed]
			if len(commits) < maxCommitsPerFile {
				result[trimmed] = append(commits, *current)
			}
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// Diff fetching
// ---------------------------------------------------------------------------

func fetchDiff(base, dir, scope string) string {
	var args []string
	switch scope {
	case "committed":
		args = []string{"diff", base + "...HEAD"}
	case "staged":
		args = []string{"diff", "--cached"}
	case "working":
		args = []string{"diff", "HEAD"}
	case "worktree":
		args = []string{"diff", base}
	default: // "all"
		args = []string{"diff", base + "...HEAD"}
	}
	raw, err := execx.Run("git", args, execx.Options{Dir: dir})
	if err != nil {
		return ""
	}
	return raw
}

// ---------------------------------------------------------------------------
// Dimension loading and matching
// ---------------------------------------------------------------------------

func loadAndMatchDimensions(projectRoot string, changedFiles []string) []reviewDimWork {
	dimDir := filepath.Join(projectRoot, paths.DataDir, "review-dimensions")
	loaded, err := dimensions.Load(dimDir)
	if err != nil || len(loaded) == 0 {
		return nil
	}

	var result []reviewDimWork
	for _, d := range loaded {
		// Skip dimensions with structural errors.
		if d.Err != nil {
			continue
		}
		if d.Meta == nil {
			continue
		}
		name, _ := d.Meta["name"].(string)
		if name == "" {
			continue
		}
		// Triggers must be present and usable.
		triggersRaw, _ := d.Meta["triggers"].([]any)
		if len(triggersRaw) == 0 {
			continue
		}

		desc, _ := d.Meta["description"].(string)
		sev, _ := d.Meta["severity"].(string)
		if sev == "" {
			sev = "medium"
		}
		rfd, _ := d.Meta["requires-full-diff"].(bool)
		var model *string
		if m, ok := d.Meta["model"].(string); ok && m != "" {
			model = &m
		}

		mf := matchFiles(d.Meta, changedFiles)

		status := "ACTIVE"
		if len(mf.matched) == 0 {
			status = "SKIPPED"
		} else if mf.truncated {
			status = "TRUNCATED"
		}

		result = append(result, reviewDimWork{
			name:             name,
			description:      desc,
			severity:         sev,
			requiresFullDiff: rfd,
			model:            model,
			status:           status,
			matchedFiles:     mf.matched,
			matchedCount:     len(mf.matched),
			truncated:        mf.truncated,
			body:             d.Body,
			common:           d.Common,
			warnings:         dimensions.Validate(d),
		})
	}

	return result
}

// ---------------------------------------------------------------------------
// Core logic (separated from handler for testability)
// ---------------------------------------------------------------------------

func reviewPrepare(projectRoot, activeRoot string, in ReviewPrepareIn) (ReviewPrepareOut, error) {
	// KD5 gate: config version check.
	if !in.SkipConfigCheck {
		if err := configmigrate.Verify(projectRoot); err != nil {
			return ReviewPrepareOut{}, &mcpserver.DataError{
				Msg:   fmt.Sprintf("config-version: %s", err.Error()),
				Cause: err,
			}
		}
	}

	// Resolve scope from config. Target overrides base branch.
	scope := "all"
	reviewCfg, _ := config.ReadSection(projectRoot, "review")
	if reviewCfg != nil {
		if s, ok := reviewCfg["scope"].(string); ok && isValidScope(s) {
			scope = s
		}
	}

	isLocalScope := scope == "staged" || scope == "working"

	// Resolve base branch.
	var base string
	if in.Target != "" {
		base = in.Target
	} else if !isLocalScope {
		b, err := gitx.DefaultBranch(activeRoot)
		if err != nil {
			return ReviewPrepareOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("detect base branch: %s", err.Error()),
				Cause: err,
			}
		}
		base = b
	}

	// Git state.
	currentBranch, _ := gitx.CurrentBranch(activeRoot)
	statusOut, _ := gitx.Status(activeRoot)
	uncommittedChanges := statusOut != ""

	// Changed files.
	changedFiles := getChangedFilesList(base, activeRoot, scope)
	if len(changedFiles) == 0 {
		return ReviewPrepareOut{}, &mcpserver.DomainError{
			Msg: "No changed files found",
		}
	}

	// Load and match dimensions.
	dims := loadAndMatchDimensions(projectRoot, changedFiles)
	if len(dims) == 0 {
		return ReviewPrepareOut{}, &mcpserver.DomainError{
			Msg: "No review dimensions found in " + paths.DataDir + "/review-dimensions/",
		}
	}

	// Commit context (branch-based scopes only).
	if !isLocalScope && base != "" {
		commitFileMap := getCommitFileMap(base, activeRoot)
		for i := range dims {
			var fc []fileContextEntry
			for _, f := range dims[i].matchedFiles {
				fc = append(fc, fileContextEntry{
					File:    f,
					Commits: commitFileMap[f],
				})
			}
			dims[i].fileContext = fc
		}
	}

	// Fetch diff and split by file.
	rawDiff := fetchDiff(base, activeRoot, scope)
	fileDiffs := gitx.SplitDiffByFile(rawDiff)
	fileDiffMap := map[string]string{}
	for _, fd := range fileDiffs {
		fileDiffMap[fd.Path] = fd.Content
	}

	// Create temp directory for diff and slice files.
	tmpDir, err := os.MkdirTemp("", "sdlc-review-")
	if err != nil {
		return ReviewPrepareOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("create temp dir: %s", err.Error()),
			Cause: err,
		}
	}

	// Write .diff and .slice.json files for active/truncated dimensions.
	for i := range dims {
		d := &dims[i]
		if d.status != "ACTIVE" && d.status != "TRUNCATED" {
			continue
		}

		// Build concatenated diff for this dimension's matched files.
		var parts []string
		for _, f := range d.matchedFiles {
			if chunk, ok := fileDiffMap[f]; ok {
				parts = append(parts, chunk)
			}
		}
		dimDiff := strings.Join(parts, "\n")

		// Apply difftrunc.Truncate unless requires-full-diff is set.
		if !d.requiresFullDiff {
			dimDiff = difftrunc.Truncate(dimDiff, difftrunc.DefaultDiffMaxBytes, gitx.SplitDiffByFile)
		}

		// Write .diff file.
		diffPath := filepath.Join(tmpDir, d.name+".diff")
		if err := os.WriteFile(diffPath, []byte(dimDiff), 0644); err != nil {
			return ReviewPrepareOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("write diff %s: %s", diffPath, err.Error()),
				Cause: err,
			}
		}
		dp := diffPath
		d.diffFile = &dp

		// Build slice body with common prompt prepended (KD16 fold).
		sliceBody := d.body
		if d.common != "" {
			sliceBody = "## Common Review Instructions\n\n" + d.common + "\n\n" + d.body
		}

		// Write .slice.json file.
		slicePath := filepath.Join(tmpDir, d.name+".slice.json")
		sliceData := dimSlice{
			Body:         sliceBody,
			MatchedFiles: emptyIfNil(d.matchedFiles),
			FileContext:  emptyFileContextIfNil(d.fileContext),
			Warnings:     emptyIfNil(d.warnings),
		}
		sliceJSON, err := json.Marshal(sliceData)
		if err != nil {
			return ReviewPrepareOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("marshal slice %s: %s", slicePath, err.Error()),
				Cause: err,
			}
		}
		if err := os.WriteFile(slicePath, sliceJSON, 0644); err != nil {
			return ReviewPrepareOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("write slice %s: %s", slicePath, err.Error()),
				Cause: err,
			}
		}
		sp := slicePath
		d.sliceFile = &sp
	}

	// Plan critique and refinement.
	critique := critiquePlan(dims, changedFiles)
	queued := refinePlan(dims)
	critique.QueuedDimensions = emptyIfNil(queued)

	// Commit count (branch-based scopes).
	commitCount := 0
	if !isLocalScope && base != "" {
		if c, err := gitx.CommitCount(activeRoot, base); err == nil {
			commitCount = c
		}
	}

	// PR metadata (best effort).
	pr := reviewManifestPR{Exists: false}

	// Build index entries.
	var indexEntries []reviewDimIndexEntry
	for _, d := range dims {
		dispatched := d.status == "ACTIVE" || d.status == "TRUNCATED"
		var sliceFile *string
		if dispatched {
			sliceFile = d.sliceFile
		}
		indexEntries = append(indexEntries, reviewDimIndexEntry{
			Name:             d.name,
			Description:      d.description,
			Severity:         d.severity,
			Model:            d.model,
			Status:           d.status,
			RequiresFullDiff: d.requiresFullDiff,
			Truncated:        d.truncated,
			MatchedCount:     d.matchedCount,
			DiffFile:         d.diffFile,
			SliceFile:        sliceFile,
		})
	}

	// Summary.
	activeDimCount := 0
	skippedDimCount := 0
	for _, d := range dims {
		switch d.status {
		case "ACTIVE", "TRUNCATED":
			activeDimCount++
		case "SKIPPED":
			skippedDimCount++
		}
	}

	summary := ReviewPrepareSummary{
		TotalDimensions:     len(dims),
		ActiveDimensions:    activeDimCount,
		SkippedDimensions:   skippedDimCount,
		QueuedDimensions:    len(queued),
		TotalChangedFiles:   len(changedFiles),
		UncoveredFileCount:  len(critique.UncoveredFiles),
		SuggestedDimensions: len(critique.UncoveredSuggestions),
	}

	// Build and write manifest.
	var baseBranch *string
	if base != "" {
		baseBranch = &base
	}

	manifest := reviewManifest{
		Version:            1,
		Timestamp:          time.Now().UTC().Format(time.RFC3339),
		SubagentModel:      "sonnet",
		PluginVersion:      pluginVersion,
		Scope:              scope,
		BaseBranch:         baseBranch,
		CurrentBranch:      currentBranch,
		UncommittedChanges: uncommittedChanges,
		Git: reviewManifestGit{
			CommitCount:       commitCount,
			ChangedFilesCount: len(changedFiles),
		},
		PR:           pr,
		Dimensions:   indexEntries,
		PlanCritique: critique,
		Summary:      summary,
		DiffDir:      tmpDir,
	}

	manifestPath := filepath.Join(tmpDir, "manifest.json")
	if err := fsx.AtomicWriteJSON(manifestPath, manifest); err != nil {
		return ReviewPrepareOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("write manifest: %s", err.Error()),
			Cause: err,
		}
	}

	return ReviewPrepareOut{
		ManifestPath: manifestPath,
		Summary:      summary,
	}, nil
}

// ---------------------------------------------------------------------------
// Git helpers
// ---------------------------------------------------------------------------

func getChangedFilesList(base, dir, scope string) []string {
	var args []string
	switch scope {
	case "committed":
		args = []string{"diff", "--name-only", base + "...HEAD"}
	case "staged":
		args = []string{"diff", "--name-only", "--cached"}
	case "working":
		args = []string{"diff", "--name-only", "HEAD"}
	case "worktree":
		args = []string{"diff", "--name-only", base}
	default: // "all"
		args = []string{"diff", "--name-only", base + "...HEAD"}
	}

	raw, err := execx.Run("git", args, execx.Options{Dir: dir})
	if err != nil || raw == "" {
		return nil
	}

	var files []string
	for _, line := range strings.Split(raw, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return files
}

func isValidScope(s string) bool {
	for _, v := range validScopes {
		if v == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Utility helpers
// ---------------------------------------------------------------------------

func toStringSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func setsEqual(a, b map[string]bool) bool {
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func emptyIfNil(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

func emptySuggestionsIfNil(ss []uncoveredSuggestion) []uncoveredSuggestion {
	if ss == nil {
		return []uncoveredSuggestion{}
	}
	return ss
}

func emptyPairsIfNil(ss [][]string) [][]string {
	if ss == nil {
		return [][]string{}
	}
	return ss
}

func emptyFileContextIfNil(fc []fileContextEntry) []fileContextEntry {
	if fc == nil {
		return []fileContextEntry{}
	}
	return fc
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterReviewTools registers review_prepare on the server.
func RegisterReviewTools(s *mcpserver.Server) {
	mcpserver.Register(s, "review_prepare",
		"Pre-compute review manifest: git state, dimension matching, diff slicing, commit context. Writes manifest + per-dimension .diff and .slice.json files to a temp directory.",
		func(ctx mcpserver.Ctx, in ReviewPrepareIn) (ReviewPrepareOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return ReviewPrepareOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
					Cause: err,
				}
			}
			activeRoot, err := worktree.ActiveRoot()
			if err != nil {
				activeRoot = root
			}
			return reviewPrepare(root, activeRoot, in)
		},
	)
}
