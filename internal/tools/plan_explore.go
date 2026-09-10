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

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// plan_explore_prepare
//
// Ports scripts/skill/plan-explore.js: a manifest-producing discovery pass
// for plan's dynamic-dimension orchestrator. The original is a
// standalone script invoked via subprocess that writes a manifest.json into
// a fresh tempdir and prints {manifestPath, ...} to stdout.
//
// KD4: plan_prepare calls buildExplorePack in-process (no subprocess spawn);
// plan_explore_prepare is the standalone tool wrapping the same builder.
// ---------------------------------------------------------------------------

const (
	maxScopeHintFiles = 30
	maxKeywordTokens  = 8
	maxSkillSamples   = 12
	maxRecentPlans    = 20
)

// PlanExploreIn is the input for the plan_explore_prepare tool.
type PlanExploreIn struct {
	FromOpenspec string `json:"fromOpenspec" jsonschema_description:"Name of the openspec change to scope dynamic-dimension discovery to. Empty when not planning from an openspec change."`
	UserPrompt   string `json:"userPrompt" jsonschema_description:"The user's original planning prompt/request text, used as a keyword-grep and web-research signal source for dimension discovery."`
}

// PlanExploreOut is the output for the plan_explore_prepare tool.
type PlanExploreOut struct {
	ManifestPath string `json:"manifestPath"`
}

// SkillSample holds a sampled skill's frontmatter block.
type SkillSample struct {
	Skill       string `json:"skill"`
	Frontmatter string `json:"frontmatter"`
}

// ExplorePack is the embedded discovery-manifest summary produced by
// buildExplorePack, used both as plan_prepare's inline "explorePack" field
// and as the basis for plan_explore_prepare's output.
type ExplorePack struct {
	ManifestPath      *string `json:"manifestPath"`
	OutDir            *string `json:"outDir"`
	ScopeHintCount    int     `json:"scopeHintCount"`
	WebResearchSignal bool    `json:"webResearchSignal"`
	Error             *string `json:"error"`
}

// exploreManifest is the JSON document written to <outDir>/manifest.json,
// mirroring plan-explore.js's manifest shape field-for-field.
type exploreManifest struct {
	Version           int           `json:"version"`
	Timestamp         string        `json:"timestamp"`
	ProjectRoot       string        `json:"projectRoot"`
	FromOpenspec      *string       `json:"fromOpenspec"`
	UserPromptLength  int           `json:"userPromptLength"`
	WebResearchSignal bool          `json:"webResearchSignal"`
	ScopeHintCount    int           `json:"scopeHintCount"`
	ScopeHintFiles    []string      `json:"scopeHintFiles"`
	SkillRegistry     []SkillSample `json:"skillRegistry"`
	RecentPlans       []string      `json:"recentPlans"`
	OutDir            string        `json:"outDir"`
}

// buildExplorePack runs the full discovery pass (git scope, OpenSpec paths,
// keyword grep, web-research signal, skill registry sample, recent plans
// sample), writes a manifest.json into a fresh sdlc-explore-<slug>-XXXXXX
// tempdir, and returns a summary. Never returns an error to the caller;
// failures degrade into ExplorePack.Error (mirrors plan-explore.js's R28
// fallback contract: always exits 0, errors surface via output.error only).
func buildExplorePack(mainRoot, contentRoot, fromOpenspec, userPrompt string) ExplorePack {
	branchSlug := "unknown"
	if branch, err := gitx.CurrentBranch(contentRoot); err == nil && branch != "" {
		branchSlug = state.SlugifyBranch(branch)
	}

	outDir, err := os.MkdirTemp(os.TempDir(), fmt.Sprintf("sdlc-explore-%s-", branchSlug))
	if err != nil {
		msg := err.Error()
		return ExplorePack{Error: &msg}
	}

	scopeHintFiles := mergeScopeHints(
		getGitScopeFiles(contentRoot),
		getOpenSpecPaths(contentRoot, fromOpenspec),
		getKeywordScopeFiles(userPrompt, contentRoot),
	)

	manifest := exploreManifest{
		Version:           1,
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		ProjectRoot:       mainRoot,
		UserPromptLength:  len(userPrompt),
		WebResearchSignal: computeWebResearchSignal(userPrompt),
		ScopeHintCount:    len(scopeHintFiles),
		ScopeHintFiles:    scopeHintFiles,
		SkillRegistry:     sampleSkillRegistry(),
		RecentPlans:       sampleRecentPlans(mainRoot),
		OutDir:            outDir,
	}
	if fromOpenspec != "" {
		manifest.FromOpenspec = &fromOpenspec
	}

	manifestPath := filepath.Join(outDir, "manifest.json")
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		msg := err.Error()
		return ExplorePack{OutDir: &outDir, Error: &msg}
	}
	data = append(data, '\n')

	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		msg := err.Error()
		return ExplorePack{OutDir: &outDir, Error: &msg}
	}

	return ExplorePack{
		ManifestPath:      &manifestPath,
		OutDir:            &outDir,
		ScopeHintCount:    manifest.ScopeHintCount,
		WebResearchSignal: manifest.WebResearchSignal,
	}
}

// mergeScopeHints dedupes and concatenates scope-hint file lists (git scope,
// OpenSpec backtick paths, keyword grep matches, in that priority order),
// capping the result at maxScopeHintFiles.
func mergeScopeHints(lists ...[]string) []string {
	seen := make(map[string]bool)
	out := []string{}
	for _, list := range lists {
		for _, f := range list {
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
			if len(out) >= maxScopeHintFiles {
				return out
			}
		}
	}
	return out
}

// getGitScopeFiles returns the set of files changed relative to the default
// branch (name-only), mirroring plan-explore.js's getGitScopeFiles. JS's
// default 'all' diff scope is identical to 'committed' scope (both build the
// same three-dot diff command), so gitx.Diff with Base+NameOnly is exact
// parity with no special-casing needed.
func getGitScopeFiles(contentRoot string) []string {
	base, err := gitx.DefaultBranch(contentRoot)
	if err != nil || base == "" {
		return []string{}
	}
	out, err := gitx.Diff(contentRoot, gitx.DiffOpts{Base: base, NameOnly: true})
	if err != nil {
		return []string{}
	}
	return nonEmptyLines(out)
}

// backtickPathRe matches inline-code file paths in markdown, e.g. `src/foo.go`.
var backtickPathRe = regexp.MustCompile("`([a-zA-Z0-9_\\-./]+\\.[a-zA-Z]{1,10})`")

// getOpenSpecPaths scans an OpenSpec change's proposal.md and specs/*.md for
// backtick-quoted, relative-looking file paths, mirroring
// plan-explore.js's getOpenSpecPaths.
func getOpenSpecPaths(contentRoot, changeName string) []string {
	if changeName == "" || !isSafeChangeName(changeName) {
		return []string{}
	}

	changeDir := filepath.Join(contentRoot, "openspec", "changes", changeName)
	if !migrateDirExists(changeDir) {
		return []string{}
	}

	var filesToScan []string
	proposalPath := filepath.Join(changeDir, "proposal.md")
	if fileExists(proposalPath) {
		filesToScan = append(filesToScan, proposalPath)
	}
	specsDir := filepath.Join(changeDir, "specs")
	if entries, err := os.ReadDir(specsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				filesToScan = append(filesToScan, filepath.Join(specsDir, e.Name()))
			}
		}
	}

	seen := make(map[string]bool)
	paths := []string{}
	for _, fp := range filesToScan {
		content, err := os.ReadFile(fp)
		if err != nil {
			continue
		}
		for _, m := range backtickPathRe.FindAllStringSubmatch(string(content), -1) {
			p := m[1]
			if strings.HasPrefix(p, "/") || seen[p] {
				continue
			}
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths
}

// tokenSplitRe splits free text into lowercase alphanumeric tokens.
var tokenSplitRe = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// stopwords mirrors plan-explore.js's STOPWORDS set: common English words
// excluded from keyword-scope grep tokenization.
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "that": true, "this": true,
	"with": true, "from": true, "have": true, "will": true, "should": true,
	"would": true, "could": true, "when": true, "where": true, "what": true,
	"which": true, "there": true, "their": true, "about": true, "into": true,
	"then": true, "than": true, "them": true, "these": true, "those": true,
	"been": true, "being": true, "does": true, "doing": true, "each": true,
	"more": true, "most": true, "other": true, "some": true, "such": true,
	"only": true, "same": true, "very": true, "just": true, "also": true,
	"make": true, "need": true, "want": true, "like": true, "using": true,
	"used": true, "user": true, "must": true, "shall": true, "can": true,
	"add": true, "new": true, "all": true, "any": true, "not": true,
	"but": true, "are": true, "was": true, "were": true, "has": true,
	"had": true, "you": true, "your": true, "our": true, "its": true,
}

// getKeywordScopeFiles tokenizes the user prompt (capped at maxKeywordTokens
// unique, stopword-filtered tokens) and runs `git grep -l -i <token>` per
// token, mirroring plan-explore.js's getKeywordScopeFiles. A non-zero exit
// from git grep means "no matches", not an error.
func getKeywordScopeFiles(userPrompt, contentRoot string) []string {
	if strings.TrimSpace(userPrompt) == "" {
		return []string{}
	}

	seen := make(map[string]bool)
	var tokens []string
	for _, t := range tokenSplitRe.Split(strings.ToLower(userPrompt), -1) {
		if len(t) <= 2 || stopwords[t] || seen[t] {
			continue
		}
		seen[t] = true
		tokens = append(tokens, t)
		if len(tokens) >= maxKeywordTokens {
			break
		}
	}

	fileSeen := make(map[string]bool)
	files := []string{}
	for _, token := range tokens {
		out, err := execx.Run("git", []string{"grep", "-l", "-i", token}, execx.Options{Dir: contentRoot})
		if err != nil {
			continue // non-zero exit (no matches) is not an error
		}
		for _, f := range nonEmptyLines(out) {
			if !fileSeen[f] {
				fileSeen[f] = true
				files = append(files, f)
			}
		}
	}
	return files
}

// webPhraseRe matches phrases that signal a need for external/web research.
var webPhraseRe = regexp.MustCompile(`(?i)best practice|recommended|industry standard|state of the art|compare alternatives|alternatives to`)

// externalTechVocab mirrors plan-explore.js's EXTERNAL_TECH_VOCAB set.
var externalTechVocab = map[string]bool{
	"oauth": true, "jwt": true, "kafka": true, "redis": true,
	"kubernetes": true, "terraform": true, "react": true, "vue": true,
	"angular": true, "postgres": true, "mongodb": true, "graphql": true,
	"grpc": true, "websocket": true, "oauth2": true, "openid": true,
	"saml": true,
}

// computeWebResearchSignal reports whether the user prompt suggests web
// research is warranted, mirroring plan-explore.js's computeWebResearchSignal.
func computeWebResearchSignal(userPrompt string) bool {
	if strings.TrimSpace(userPrompt) == "" {
		return false
	}
	if webPhraseRe.MatchString(userPrompt) {
		return true
	}
	for _, t := range tokenSplitRe.Split(strings.ToLower(userPrompt), -1) {
		if externalTechVocab[t] {
			return true
		}
	}
	return false
}

// skillFrontmatterRe extracts a leading YAML frontmatter block from a
// SKILL.md file.
var skillFrontmatterRe = regexp.MustCompile(`(?s)^---\n(.*?)\n---`)

// sampleSkillRegistry walks ~/.claude/plugins/*/skills/*/SKILL.md and
// extracts each skill's frontmatter block, capped at maxSkillSamples,
// mirroring plan-explore.js's sampleSkillRegistry.
func sampleSkillRegistry() []SkillSample {
	skills := []SkillSample{}
	home, err := os.UserHomeDir()
	if err != nil {
		return skills
	}
	pluginsDir := filepath.Join(home, ".claude", "plugins")
	pluginEntries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return skills
	}

	for _, pe := range pluginEntries {
		if len(skills) >= maxSkillSamples {
			break
		}
		if !pe.IsDir() {
			continue
		}
		skillsDir := filepath.Join(pluginsDir, pe.Name(), "skills")
		skillEntries, err := os.ReadDir(skillsDir)
		if err != nil {
			continue
		}
		for _, se := range skillEntries {
			if len(skills) >= maxSkillSamples {
				break
			}
			if !se.IsDir() {
				continue
			}
			content, err := os.ReadFile(filepath.Join(skillsDir, se.Name(), "SKILL.md"))
			if err != nil {
				continue
			}
			if m := skillFrontmatterRe.FindStringSubmatch(string(content)); m != nil {
				skills = append(skills, SkillSample{
					Skill:       se.Name(),
					Frontmatter: strings.TrimSpace(m[1]),
				})
			}
		}
	}
	return skills
}

// planSettingsFile is the subset of a settings.json used to locate a
// user- or project-configured plans directory.
type planSettingsFile struct {
	PlansDirectory string `json:"plansDirectory"`
}

// sampleRecentPlans resolves a plans directory (project .claude/settings.json
// first, then global ~/.claude/settings.json, then ~/.claude/plans as a
// hardcoded fallback) and returns up to maxRecentPlans .md filenames sorted
// by mtime descending, mirroring plan-explore.js's sampleRecentPlans.
// Directories are not merged: the function returns on the first candidate
// directory that exists, even if it yields zero files.
func sampleRecentPlans(mainRoot string) []string {
	var candidateDirs []string

	home, homeErr := os.UserHomeDir()

	if data, err := os.ReadFile(filepath.Join(mainRoot, ".claude", "settings.json")); err == nil {
		var s planSettingsFile
		if json.Unmarshal(data, &s) == nil && s.PlansDirectory != "" {
			candidateDirs = append(candidateDirs, s.PlansDirectory)
		}
	}

	if homeErr == nil {
		if data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json")); err == nil {
			var s planSettingsFile
			if json.Unmarshal(data, &s) == nil && s.PlansDirectory != "" {
				candidateDirs = append(candidateDirs, s.PlansDirectory)
			}
		}
		candidateDirs = append(candidateDirs, filepath.Join(home, ".claude", "plans"))
	}

	for _, dir := range candidateDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		type mdFile struct {
			name  string
			mtime time.Time
		}
		var mdFiles []mdFile
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			mdFiles = append(mdFiles, mdFile{name: e.Name(), mtime: info.ModTime()})
		}
		sort.Slice(mdFiles, func(i, j int) bool { return mdFiles[i].mtime.After(mdFiles[j].mtime) })
		if len(mdFiles) > maxRecentPlans {
			mdFiles = mdFiles[:maxRecentPlans]
		}

		names := make([]string, len(mdFiles))
		for i, f := range mdFiles {
			names[i] = f.name
		}
		return names // first existing dir wins, even if empty after filtering
	}

	return []string{}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterPlanExploreTools registers plan_explore_prepare on the server.
// plan_prepare and plan_mark are registered by RegisterPlanTools in plan.go.
func RegisterPlanExploreTools(s *mcpserver.Server) {
	mcpserver.Register(s, "plan_explore_prepare",
		"INTERNAL — called by sdlc skills only. Run dynamic-dimension discovery (git scope, OpenSpec paths, keyword grep, web-research signal, skill registry, recent plans) and write a manifest.json into a fresh tempdir for plan's explore orchestrator.",
		func(_ mcpserver.Ctx, in PlanExploreIn) (PlanExploreOut, error) {
			mainRoot, err := worktree.MainRoot()
			if err != nil {
				return PlanExploreOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve main root: %s", err.Error()),
					Cause: err,
				}
			}
			contentRoot, err := worktree.ActiveRoot()
			if err != nil {
				return PlanExploreOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve active root: %s", err.Error()),
					Cause: err,
				}
			}

			pack := buildExplorePack(mainRoot, contentRoot, in.FromOpenspec, in.UserPrompt)
			if pack.ManifestPath == nil {
				msg := "plan-explore: failed to produce manifest"
				if pack.Error != nil {
					msg = fmt.Sprintf("plan-explore: %s", *pack.Error)
				}
				return PlanExploreOut{}, &mcpserver.InfraError{Msg: msg}
			}
			return PlanExploreOut{ManifestPath: *pack.ManifestPath}, nil
		},
	)
}
