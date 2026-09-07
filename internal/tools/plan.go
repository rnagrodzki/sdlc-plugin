package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/openspec"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// plan_prepare / plan_mark
//
// Ports scripts/skill/plan.js: OpenSpec detection, guardrail loading,
// explore-pack discovery, and G17/lane/lens dispatch metadata for
// plan (plan_prepare), plus the --mark checkpoint-marker CLI mode
// (plan_mark). scripts/skill/plan-handoff-advisory.js is out of scope (a
// permanent no-op wrapper around a dead sidecar).
//
// plan_explore_prepare (scripts/skill/plan-explore.js) lives in
// plan_explore.go; buildExplorePack there is called in-process from
// planPrepareCore below (KD4: prepare inline, explore manifest by file).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// plan_prepare types
// ---------------------------------------------------------------------------

// PlanPrepareIn is the input for the plan_prepare tool. This is the full
// contract shape: it deliberately has no user-prompt field, so the embedded
// explorePack always runs with an empty prompt (see RULING in the task
// report — keyword-scope and web-research-signal detection are inert here;
// only plan_explore_prepare's separate UserPrompt field can exercise them).
type PlanPrepareIn struct {
	SkipConfigCheck        bool   `json:"skipConfigCheck"`
	FromOpenspec           string `json:"fromOpenspec"`
	ResolveTemplate        bool   `json:"resolveTemplate"`
	FromOpenspecDirect     bool   `json:"fromOpenspecDirect"`
	OpenspecInlineGenerate bool   `json:"openspecInlineGenerate"`
	Lightweight            bool   `json:"lightweight"`
	FileCount              int    `json:"fileCount"`
}

// OpenspecChangeInfo, OpenspecAuthoritative, and OpenspecInfo used to be
// defined here; Task 37 Ruling A relocated them (verbatim, unexported
// functions and all) into internal/openspec/openspec.go, since
// internal/hooks' session-start handler needs the same filesystem-walking
// OpenSpec shape and could not reach unexported symbols in this package.
// These aliases keep every existing call site in this file (and the JSON
// output shape) unchanged.
type OpenspecChangeInfo = openspec.OpenspecChangeInfo
type OpenspecAuthoritative = openspec.OpenspecAuthoritative
type OpenspecInfo = openspec.OpenspecInfo

// FromOpenspecResult mirrors plan.js's hand-picked --from-openspec summary
// (a subset of validateChange()'s fields, keyed as "changeName" not "name",
// with no "errors" field — those get folded into the top-level Errors[]).
type FromOpenspecResult struct {
	Valid          bool    `json:"valid"`
	ChangeName     string  `json:"changeName"`
	HasProposal    bool    `json:"hasProposal"`
	DeltaSpecCount int     `json:"deltaSpecCount"`
	HasDesign      bool    `json:"hasDesign"`
	HasTasks       bool    `json:"hasTasks"`
	TasksDone      int     `json:"tasksDone"`
	TasksTotal     int     `json:"tasksTotal"`
	Stage          *string `json:"stage"`
}

// TaskEntry mirrors lib/openspec.js's parseTasks() entry shape.
type TaskEntry struct {
	Ref    string `json:"ref"`
	Line   int    `json:"line"`
	Title  string `json:"title"`
	Indent int    `json:"indent"`
	Done   bool   `json:"done"`
}

// RequirementEntry mirrors lib/openspec.js's getRequirementInventory() entry shape.
type RequirementEntry struct {
	ReqID         string `json:"reqId"`
	Capability    string `json:"capability"`
	Type          string `json:"type"`
	Name          string `json:"name"`
	ScenarioCount int    `json:"scenarioCount"`
}

// OpenspecContext mirrors plan.js's openspecContext accumulator. Requirements
// and RequirementsError are always present here (nil renders as JSON null)
// even though plan.js only adds those two keys inside the --from-openspec +
// valid-change branch; see RULING in the task report.
type OpenspecContext struct {
	Tasks             []TaskEntry        `json:"tasks"`
	TasksUpdated      int                `json:"tasksUpdated"`
	Requirements      []RequirementEntry `json:"requirements"`
	RequirementsError *string            `json:"requirementsError"`
}

// GuardrailItem is a single "plan" config-section guardrail entry.
// Guardrails are read as raw JSON objects (config.ReadSection has no fixed
// schema for them, and neither does plan.js — it passes planConfig.guardrails
// through untouched), so PlanPrepareOut.Guardrails is []map[string]any rather
// than a fixed struct.

// PlanTemplate mirrors plan.js's planTemplate = { path: string|null }.
type PlanTemplate struct {
	Path *string `json:"path"`
}

// GithubHosting mirrors plan.js's buildGithubHosting() result shape.
type GithubHosting struct {
	Detected bool    `json:"detected"`
	Host     *string `json:"host"`
}

// Dispatch mirrors plan.js's g17Dispatch/intakeAuditDispatch output shape
// (the internal-only "error" field used for stderr warnings is dropped, same
// as plan.js's own final output.g17Dispatch narrowing at line 638).
type Dispatch struct {
	SubagentType       string  `json:"subagentType"`
	Model              string  `json:"model"`
	PromptTemplatePath *string `json:"promptTemplatePath"`
}

// Lane mirrors plan.js's buildLanes() entry shape.
type Lane struct {
	Name               string   `json:"name"`
	SubagentType       string   `json:"subagentType"`
	Model              string   `json:"model"`
	PromptTemplatePath *string  `json:"promptTemplatePath"`
	GateIDs            []string `json:"gateIds"`
}

// LensReviewer mirrors plan.js's buildLensReviewers() entry shape.
type LensReviewer struct {
	Lens               string   `json:"lens"`
	SubagentType       string   `json:"subagentType"`
	Model              string   `json:"model"`
	PromptTemplatePath *string  `json:"promptTemplatePath"`
	FocusCategories    []string `json:"focusCategories"`
}

// TemplateResolution is the result of resolving the active plan template,
// parsing its sections/questions/patterns, building a plan skeleton, and
// computing complexity routing. Populated only when PlanPrepareIn.ResolveTemplate
// is true; nil otherwise.
type TemplateResolution struct {
	ActiveTemplatePath   string            `json:"activeTemplatePath"`
	Sections             []TemplateSection `json:"sections"`
	DiscoveryQuestions   []string          `json:"discoveryQuestions"`
	VerificationPatterns []string          `json:"verificationPatterns"`
	SkeletonMarkdown     string            `json:"skeletonMarkdown"`
	HeaderMarkdown       string            `json:"headerMarkdown"`
	PipelineMode         string            `json:"pipelineMode"`
	Routing              ComplexityRouting `json:"routing"`
	Summary              string            `json:"summary"`
	Next                 string            `json:"next"`
	Warnings             []string          `json:"warnings"`
}

// ComplexityRouting determines the pipeline mode from the file count.
type ComplexityRouting struct {
	FileCount    int    `json:"fileCount"`
	PipelineMode string `json:"pipelineMode"`
	Reason       string `json:"reason"`
}

// PlanPrepareOut is the output for the plan_prepare tool, mirroring
// plan.js's main() output object field-for-field (see line ~630).
type PlanPrepareOut struct {
	Openspec            OpenspecInfo        `json:"openspec"`
	FromOpenspec        *FromOpenspecResult `json:"fromOpenspec"`
	OpenspecContext     OpenspecContext     `json:"openspecContext"`
	Guardrails          []map[string]any    `json:"guardrails"`
	Style               PlanStyle           `json:"style"`
	Tasks               PlanTasks           `json:"tasks"`
	ExplorePack         ExplorePack         `json:"explorePack"`
	PlanTemplate        PlanTemplate        `json:"planTemplate"`
	GithubHosting       GithubHosting       `json:"githubHosting"`
	G17Dispatch         Dispatch            `json:"g17Dispatch"`
	IntakeAuditDispatch Dispatch            `json:"intakeAuditDispatch"`
	Lanes               []Lane              `json:"lanes"`
	LensReviewers       []LensReviewer      `json:"lensReviewers"`
	Template            *TemplateResolution `json:"template,omitempty"`
	Errors              []string            `json:"errors"`
}

// ---------------------------------------------------------------------------
// OpenSpec detection (native port of lib/openspec.js — plan.js imports only
// detectActiveChanges, validateChange, parseTasks, getRequirementInventory,
// but those depend internally on countMdFiles/deriveStage/analyzeChange).
// ---------------------------------------------------------------------------

// isSafeChangeName rejects path-traversal-unsafe OpenSpec change names. The
// logic now lives in openspec.IsSafeChangeName (Task 37 Ruling A
// relocation); this thin forwarder exists solely so plan_explore.go's
// getOpenSpecPaths call site keeps compiling against the unexported name
// without that file needing to import internal/openspec itself.
func isSafeChangeName(name string) bool {
	return openspec.IsSafeChangeName(name)
}

// countMdFiles, deriveStage, analyzeChange, branchPrefixRe,
// detectActiveChanges, and slugBoundaryMatch used to be defined here; Task
// 37 Ruling A moved them (verbatim) to openspec.CountMdFiles/DeriveStage/
// AnalyzeChange/BranchPrefixRe/DetectActiveChanges/slugBoundaryMatch in
// internal/openspec/openspec.go. Call sites below were updated in place.

// validateChange mirrors lib/openspec.js's validateChange.
func validateChange(contentRoot, changeName string) struct {
	Valid  bool
	Errors []string
	OpenspecChangeInfo
} {
	changeDir := filepath.Join(contentRoot, "openspec", "changes", changeName)

	if !migrateDirExists(changeDir) {
		return struct {
			Valid  bool
			Errors []string
			OpenspecChangeInfo
		}{
			Valid:  false,
			Errors: []string{fmt.Sprintf("Change directory not found: openspec/changes/%s/", changeName)},
			OpenspecChangeInfo: OpenspecChangeInfo{
				Name: changeName,
			},
		}
	}

	errs := []string{}
	if !fileExists(filepath.Join(changeDir, "proposal.md")) {
		errs = append(errs, fmt.Sprintf("Missing required file: openspec/changes/%s/proposal.md", changeName))
	}

	specsDir := filepath.Join(changeDir, "specs")
	if !migrateDirExists(specsDir) || openspec.CountMdFiles(specsDir) == 0 {
		errs = append(errs, fmt.Sprintf("Warning: openspec/changes/%s/specs/ is empty or missing", changeName))
	}

	info := openspec.AnalyzeChange(changeDir, changeName)

	valid := true
	for _, e := range errs {
		if !strings.HasPrefix(e, "Warning:") {
			valid = false
			break
		}
	}

	return struct {
		Valid  bool
		Errors []string
		OpenspecChangeInfo
	}{
		Valid:              valid,
		Errors:             errs,
		OpenspecChangeInfo: info,
	}
}

// ---------------------------------------------------------------------------
// Task parsing (lib/openspec.js's parseTasks/computeRef/extractInlineRef)
// ---------------------------------------------------------------------------

var (
	taskLineRe           = regexp.MustCompile(`^([ \t]*)- \[([ xX])\] (.*)$`)
	inlineRefRe          = regexp.MustCompile(`<!--\s*ref:([^\s]+?)\s*-->`)
	trailingCommentRe    = regexp.MustCompile(`(?s)\s*<!--.*?-->\s*$`)
	slugNonAlnumRe       = regexp.MustCompile(`[^a-z0-9]+`)
	slugCollapseHyphenRe = regexp.MustCompile(`-+`)
	refCommentPresenceRe = regexp.MustCompile(`<!--\s*ref:`)
)

// computeRef mirrors lib/openspec.js's computeRef: kebab-slug(title, <=40) +
// '-' + first 6 hex chars of sha256(title).
func computeRef(title string) string {
	slug := strings.ToLower(title)
	slug = slugNonAlnumRe.ReplaceAllString(slug, "-")
	slug = slugCollapseHyphenRe.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 40 {
		slug = slug[:40]
	}
	slug = strings.TrimRight(slug, "-")

	sum := sha256.Sum256([]byte(title))
	hash := hex.EncodeToString(sum[:])[:6]
	return slug + "-" + hash
}

// extractInlineRef mirrors lib/openspec.js's extractInlineRef.
func extractInlineRef(line string) string {
	m := inlineRefRe.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1]
}

// parseTasks mirrors lib/openspec.js's parseTasks.
func parseTasks(content string) []TaskEntry {
	lines := strings.Split(content, "\n")
	out := []TaskEntry{}
	for i, raw := range lines {
		m := taskLineRe.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		indent := len(m[1])
		done := strings.ToLower(m[2]) == "x"
		rawAfterBox := m[3]
		inlineRef := extractInlineRef(rawAfterBox)
		title := strings.TrimSpace(trailingCommentRe.ReplaceAllString(rawAfterBox, ""))
		ref := inlineRef
		if ref == "" {
			ref = computeRef(title)
		}
		out = append(out, TaskEntry{Ref: ref, Line: i + 1, Title: title, Indent: indent, Done: done})
	}
	return out
}

// ---------------------------------------------------------------------------
// Requirement inventory (lib/openspec.js's getRequirementInventory)
// ---------------------------------------------------------------------------

// requirementInventory is the internal result of getRequirementInventory,
// mirroring lib/openspec.js's { ok, cliAvailable, requirements, error }.
type requirementInventory struct {
	OK           bool
	CLIAvailable bool
	Requirements []RequirementEntry
	Error        *string
}

// isBinaryNotFound reports whether err indicates the executable was not
// found on PATH (mirrors internal/ghx's private isBinaryNotFound idiom;
// duplicated locally since that helper is unexported in another package).
func isBinaryNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "executable file not found")
}

// getRequirementInventory shells `openspec show <name> --json --deltas-only`
// and normalizes its output, mirroring lib/openspec.js's getRequirementInventory.
func getRequirementInventory(contentRoot, changeName string) requirementInventory {
	out, err := execx.Run("openspec", []string{"show", changeName, "--json", "--deltas-only"}, execx.Options{Dir: contentRoot})
	if err != nil {
		if isBinaryNotFound(err) {
			msg := "openspec CLI not found on PATH"
			return requirementInventory{CLIAvailable: false, Error: &msg}
		}
		msg := err.Error()
		return requirementInventory{CLIAvailable: true, Error: &msg}
	}

	var parsed any
	if jsonErr := json.Unmarshal([]byte(out), &parsed); jsonErr != nil {
		msg := fmt.Sprintf("JSON parse error: %s", jsonErr.Error())
		return requirementInventory{CLIAvailable: true, Error: &msg}
	}

	raw := extractRequirementsArray(parsed)
	if raw == nil {
		msg := "Unexpected JSON shape from openspec CLI"
		return requirementInventory{CLIAvailable: true, Error: &msg}
	}

	requirements := make([]RequirementEntry, 0, len(raw))
	for idx, entryRaw := range raw {
		entry, _ := entryRaw.(map[string]any)
		requirements = append(requirements, RequirementEntry{
			ReqID:         firstNonEmptyJSONString(fmt.Sprintf("req-%d", idx), entry["reqId"], entry["id"]),
			Capability:    firstNonEmptyJSONString("", entry["capability"], entry["description"], entry["summary"]),
			Type:          strings.ToUpper(firstNonEmptyJSONString("ADDED", entry["type"], entry["changeType"])),
			Name:          firstNonEmptyJSONString("", entry["name"], entry["title"], entry["capability"]),
			ScenarioCount: extractScenarioCount(entry["scenarioCount"], entry["scenarios"]),
		})
	}

	return requirementInventory{OK: true, CLIAvailable: true, Requirements: requirements}
}

// extractRequirementsArray tolerantly extracts an array of requirement
// objects from the openspec CLI's JSON output, mirroring
// lib/openspec.js's shape-fallback chain (array directly, .requirements,
// or .deltas).
func extractRequirementsArray(parsed any) []any {
	if arr, ok := parsed.([]any); ok {
		return arr
	}
	obj, ok := parsed.(map[string]any)
	if !ok {
		return nil
	}
	if arr, ok := obj["requirements"].([]any); ok {
		return arr
	}
	if arr, ok := obj["deltas"].([]any); ok {
		return arr
	}
	return nil
}

// firstNonEmptyJSONString mirrors JS's `String(a || b || c || fallback)`
// chain: candidates are tried in order, an empty/nil/absent value falls
// through to the next, and fallback is returned (as-is) if all are empty.
// fallback is given first so callers list JSON candidates in priority order
// while still supplying a literal Go string default.
func firstNonEmptyJSONString(fallback string, candidates ...any) string {
	for _, v := range candidates {
		if s := stringifyJSONValue(v); s != "" {
			return s
		}
	}
	return fallback
}

func stringifyJSONValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}

// extractScenarioCount mirrors lib/openspec.js's scenarioCount fallback
// chain: a numeric scenarioCount, else a numeric scenarios, else the length
// of an array scenarios, else 0.
func extractScenarioCount(scenarioCount, scenarios any) int {
	if n, ok := scenarioCount.(float64); ok {
		return int(n)
	}
	if n, ok := scenarios.(float64); ok {
		return int(n)
	}
	if arr, ok := scenarios.([]any); ok {
		return len(arr)
	}
	return 0
}

// ---------------------------------------------------------------------------
// Guardrail loading (config section "plan", key "guardrails")
// ---------------------------------------------------------------------------

// loadGuardrails reads the "plan" config section's guardrails array,
// mirroring plan.js's `readSection(projectRoot, 'plan')` + Array.isArray
// check. An absent config file or an absent "plan" section both map to
// config.ErrNotFound in Go (stricter than JS's readSection, which returns a
// benign null for either case without throwing) — both are treated here as
// the same benign "guardrails = [], no error" outcome as JS. Any other
// ReadSection error (e.g. malformed JSON) is surfaced as an error string,
// matching plan.js's catch block.
func loadGuardrails(mainRoot string) ([]map[string]any, string) {
	planConfig, err := config.ReadSection(mainRoot, "plan")
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return []map[string]any{}, ""
		}
		return []map[string]any{}, fmt.Sprintf("Failed to read plan config: %s", err.Error())
	}

	raw, ok := planConfig["guardrails"]
	if !ok {
		return []map[string]any{}, ""
	}
	arr, ok := raw.([]any)
	if !ok {
		return []map[string]any{}, ""
	}

	guardrails := make([]map[string]any, 0, len(arr))
	for _, el := range arr {
		if m, ok := el.(map[string]any); ok {
			guardrails = append(guardrails, m)
		}
	}
	return guardrails, ""
}

// ---------------------------------------------------------------------------
// PlanStyle / PlanTasks loading (sections "planStyle" and "plan" -> "tasks")
// ---------------------------------------------------------------------------

// PlanStyle configures personal plan narrative preferences: verbosity,
// audience, and custom narrative rules. Loaded from the "planStyle" config
// section, which is not a config.ProjectSections member and therefore
// routes to .sdlc-v2/local.json (per-developer, gitignored).
type PlanStyle struct {
	Verbosity      string   `json:"verbosity"`
	Audience       string   `json:"audience"`
	NarrativeRules []string `json:"narrativeRules"`
}

// PlanTasks is the team contract for plan task deliverables: which fields
// are required on every task, and the overall contract shape. Loaded from
// the "plan" config section's "tasks" sub-key; "plan" is a
// config.ProjectSections member, so it routes to .sdlc-v2/config.json
// (team-shared, committed).
type PlanTasks struct {
	RequiredFields []string `json:"requiredFields"`
	ContractShape  string   `json:"contractShape"`
}

// loadPlanStyle reads the "planStyle" config section, mirroring
// loadGuardrails' readSection + benign-absence handling: any error from
// config.ReadSection (missing file, missing section, or malformed JSON)
// falls back to defaults rather than surfacing an error, since PlanStyle
// has no error-string return channel. Defaults are "standard" verbosity,
// "technical" audience, and a nil NarrativeRules.
func loadPlanStyle(mainRoot string) PlanStyle {
	style := PlanStyle{Verbosity: "standard", Audience: "technical"}

	section, err := config.ReadSection(mainRoot, "planStyle")
	if err != nil {
		return style
	}

	if v, ok := section["verbosity"].(string); ok && v != "" {
		style.Verbosity = v
	}
	if v, ok := section["audience"].(string); ok && v != "" {
		style.Audience = v
	}
	if raw, ok := section["narrativeRules"].([]any); ok {
		rules := make([]string, 0, len(raw))
		for _, el := range raw {
			if s, ok := el.(string); ok {
				rules = append(rules, s)
			}
		}
		style.NarrativeRules = rules
	}

	return style
}

// loadPlanTasks reads the "plan" config section's "tasks" sub-key,
// mirroring loadGuardrails' readSection + benign-absence handling: any
// error from config.ReadSection or an absent/malformed "tasks" sub-key
// falls back to defaults. Applies the "full" default for ContractShape
// when absent, and silently drops any requiredFields entry that duplicates
// one of the five fields already guaranteed by the task contract's fixed
// shape (Complexity, Risk, Files, Verify, Depends on) -- KD5, additive-only
// enforced in Go.
func loadPlanTasks(mainRoot string) PlanTasks {
	tasks := PlanTasks{ContractShape: "full"}

	planConfig, err := config.ReadSection(mainRoot, "plan")
	if err != nil {
		return tasks
	}

	raw, ok := planConfig["tasks"].(map[string]any)
	if !ok {
		return tasks
	}

	if v, ok := raw["contractShape"].(string); ok && v != "" {
		tasks.ContractShape = v
	}
	if arr, ok := raw["requiredFields"].([]any); ok {
		coreFields := map[string]bool{
			"Complexity": true, "Risk": true, "Files": true,
			"Verify": true, "Depends on": true,
		}
		fields := make([]string, 0, len(arr))
		for _, el := range arr {
			s, ok := el.(string)
			if !ok || coreFields[s] {
				continue
			}
			fields = append(fields, s)
		}
		tasks.RequiredFields = fields
	}

	return tasks
}

// ---------------------------------------------------------------------------
// GitHub hosting signal (P14, R32)
// ---------------------------------------------------------------------------

// remoteOwner mirrors lib/git.js's parseRemoteOwner result shape. It is
// reimplemented locally (rather than reusing internal/ghx.ParseRemoteOwner)
// because that function parses a raw URL string and does not return a host
// field; lib/git.js's version shells `git remote get-url origin` itself and
// extracts host+owner+repo together, which is what buildGithubHosting needs.
type remoteOwner struct {
	Host  string
	Owner string
	Repo  string
}

var (
	sshRemoteRe   = regexp.MustCompile(`^git@([^:]+):([^/]+)/(.+?)(?:\.git)?$`)
	httpsRemoteRe = regexp.MustCompile(`^https?://([^/]+)/([^/]+)/(.+?)(?:\.git)?$`)
)

// parseRemoteOwner mirrors lib/git.js's parseRemoteOwner(projectRoot).
func parseRemoteOwner(projectRoot string) *remoteOwner {
	url, err := execx.Run("git", []string{"remote", "get-url", "origin"}, execx.Options{Dir: projectRoot})
	if err != nil || url == "" {
		return nil
	}
	if m := sshRemoteRe.FindStringSubmatch(url); m != nil {
		return &remoteOwner{Host: m[1], Owner: m[2], Repo: m[3]}
	}
	if m := httpsRemoteRe.FindStringSubmatch(url); m != nil {
		return &remoteOwner{Host: m[1], Owner: m[2], Repo: m[3]}
	}
	return nil
}

// buildGithubHosting mirrors plan.js's buildGithubHosting.
func buildGithubHosting(projectRoot string) GithubHosting {
	parsed := parseRemoteOwner(projectRoot)
	if parsed == nil {
		return GithubHosting{Detected: false, Host: nil}
	}
	host := parsed.Host
	return GithubHosting{Detected: host == "github.com", Host: &host}
}

// ---------------------------------------------------------------------------
// Skill template resolution (P15/P16/P17/P20)
//
// plan.js's resolveSkillTemplate/buildG17Dispatch try a workspace-relative
// path (__dirname-sibling skills/plan/<name>) before falling back to a
// find cascade over ~/.claude/plugins. The Go binary has no skills/ sibling
// directory (no such convention exists in this repo), so only the find
// cascade is implemented; a miss degrades to nil, matching plan.js's own
// null-degrade contract exactly.
// ---------------------------------------------------------------------------

// pluginVersionRe extracts a semver-looking path segment (".../N.N.N/skills")
// used to pick the highest-versioned match, mirroring plan.js's `ver()`.
var pluginVersionRe = regexp.MustCompile(`/(\d+)\.(\d+)\.(\d+)/skills`)

// skillWalkSkipDirs lists directory names that are pruned during the
// ~/.claude/plugins walk below. Installed plugins can vendor full package
// checkouts (observed in practice: a marketplace clone containing a
// devtools-frontend checkout with a multi-hundred-thousand-file
// node_modules tree), and a plan skill template markdown file can
// never live inside any of these — pruning them is a pure performance
// safeguard with no effect on which file is found.
var skillWalkSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".cache":       true,
	"dist":         true,
	"build":        true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
}

// skillTemplateIndex caches every discovered "<templateName> -> best (highest
// semver) match path" pair from a single walk of ~/.claude/plugins, keyed by
// templateName. All of buildG17Dispatch/buildIntakeAuditDispatch/buildLanes/
// buildLensReviewers resolve their templates within one plan_prepare call, so
// without this cache a single call would re-walk the (potentially huge)
// plugins tree once per template (9 times) instead of once.
var (
	skillTemplateIndex     map[string]string
	skillTemplateIndexOnce sync.Once
)

// buildSkillTemplateIndex walks ~/.claude/plugins once, recording for every
// filename found under a "/plan/" path segment the highest-semver match
// (mirroring plan.js's per-template find + version-sort cascade, but
// computed for all template names in a single pass).
func buildSkillTemplateIndex() map[string]string {
	index := map[string]string{}
	home, err := os.UserHomeDir()
	if err != nil {
		return index
	}
	pluginsRoot := filepath.Join(home, ".claude", "plugins")

	_ = filepath.WalkDir(pluginsRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil // skip unreadable entries, continue walking
		}
		if d.IsDir() {
			if skillWalkSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.Contains(filepath.ToSlash(p), "/plan/") {
			return nil
		}
		name := d.Name()
		existing, ok := index[name]
		if !ok || comparePathVersion(existing, p) < 0 {
			index[name] = p
		}
		return nil
	})

	return index
}

// resolveSkillTemplate finds a plan skill template file under
// ~/.claude/plugins/*/skills/.../<templateName>, mirroring plan.js's find
// cascade (`find ~/.claude/plugins -name <templateName> -path '*/plan/*'`)
// with the highest-semver match winning. Returns nil when not found. The
// underlying plugins-tree walk runs at most once per process (see
// skillTemplateIndexOnce).
func resolveSkillTemplate(templateName string) *string {
	skillTemplateIndexOnce.Do(func() {
		skillTemplateIndex = buildSkillTemplateIndex()
	})

	best, ok := skillTemplateIndex[templateName]
	if !ok {
		return nil
	}
	return &best
}

func comparePathVersion(a, b string) int {
	va := extractPathVersion(a)
	vb := extractPathVersion(b)
	for i := 0; i < 3; i++ {
		if va[i] != vb[i] {
			if va[i] < vb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func extractPathVersion(p string) [3]int {
	m := pluginVersionRe.FindStringSubmatch(filepath.ToSlash(p))
	if m == nil {
		return [3]int{0, 0, 0}
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	return [3]int{a, b, c}
}

// buildG17Dispatch mirrors plan.js's buildG17Dispatch (output-narrowed to
// {subagentType, model, promptTemplatePath}; the internal-only error field
// used for a stderr warning is dropped, matching plan.js's own output
// narrowing at line 638).
func buildG17Dispatch() Dispatch {
	return Dispatch{
		SubagentType:       "general-purpose",
		Model:              "sonnet",
		PromptTemplatePath: resolveSkillTemplate("g17-dimension-coverage-prompt.md"),
	}
}

// buildIntakeAuditDispatch mirrors plan.js's buildIntakeAuditDispatch.
func buildIntakeAuditDispatch() Dispatch {
	return Dispatch{
		SubagentType:       "general-purpose",
		Model:              "sonnet",
		PromptTemplatePath: resolveSkillTemplate("intake-verify-prompt.md"),
	}
}

// buildLanes mirrors plan.js's buildLanes (KD1 lane partitioning: four
// static lane definitions plus a fifth entry mirroring g17Dispatch verbatim).
func buildLanes(g17Dispatch Dispatch) []Lane {
	type laneDef struct {
		name         string
		model        string
		templateName string
		gateIDs      []string
	}
	defs := []laneDef{
		{"static-structural", "haiku", "lane-static-structural-prompt.md", []string{"G1", "G2", "G3", "G7", "G12"}},
		{"content-coverage", "sonnet", "lane-content-coverage-prompt.md", []string{"G5", "G6", "G8", "G9", "G11", "G13", "G15", "G16", "G18", "G19", "G20", "G21"}},
		{"file-existence", "haiku", "lane-file-existence-prompt.md", []string{"G4", "G10"}},
		{"guardrail-compliance", "sonnet", "lane-guardrail-compliance-prompt.md", []string{"G14"}},
	}

	lanes := make([]Lane, 0, len(defs)+1)
	for _, d := range defs {
		lanes = append(lanes, Lane{
			Name:               d.name,
			SubagentType:       "general-purpose",
			Model:              d.model,
			PromptTemplatePath: resolveSkillTemplate(d.templateName),
			GateIDs:            d.gateIDs,
		})
	}

	lanes = append(lanes, Lane{
		Name:               "dimension-coverage",
		SubagentType:       g17Dispatch.SubagentType,
		Model:              g17Dispatch.Model,
		PromptTemplatePath: g17Dispatch.PromptTemplatePath,
		GateIDs:            []string{"G17"},
	})

	return lanes
}

// buildLensReviewers mirrors plan.js's buildLensReviewers (KD3 lens
// partitioning: three lens definitions).
func buildLensReviewers() []LensReviewer {
	type lensDef struct {
		lens            string
		templateName    string
		focusCategories []string
	}
	defs := []lensDef{
		{"architecture", "lens-architecture-prompt.md", []string{"Buildability", "Task descriptions", "Decision documentation", "Dependency accuracy"}},
		{"requirements", "lens-requirements-prompt.md", []string{"Requirements coverage", "Metadata completeness", "Plan completeness", "OpenSpec G16", "Exploration provenance", "Best-practice traceability"}},
		{"risk", "lens-risk-prompt.md", []string{"File paths", "Verification strategy", "Scope discipline", "Guardrail compliance"}},
	}

	reviewers := make([]LensReviewer, 0, len(defs))
	for _, d := range defs {
		reviewers = append(reviewers, LensReviewer{
			Lens:               d.lens,
			SubagentType:       "general-purpose",
			Model:              "sonnet",
			PromptTemplatePath: resolveSkillTemplate(d.templateName),
			FocusCategories:    d.focusCategories,
		})
	}
	return reviewers
}

// ---------------------------------------------------------------------------
// Template resolution (plan_prepare resolveTemplate=true path)
//
// Resolves the active plan template (project override -> shipped default
// fallback), parses sections/discovery-questions/verification-patterns,
// builds the plan skeleton + header markdown, and computes complexity
// routing from the file count.
// ---------------------------------------------------------------------------

// step5OwnedSections lists sections whose content is produced exclusively
// by Step 5 (the multi-lens review). When lightweight routing skips Step 5,
// these get "Not applicable — lightweight plan" instead of "[TBD]".
var step5OwnedSections = map[string]bool{
	"Verification Scorecard": true,
}

// openspecConditionPrefix is the prefix that identifies an OpenSpec-conditional
// section. Both the legacy condition ("source matches openspec/changes/") and
// the current form ("source matches openspec/changes/ or openspecInlineGenerate")
// start with this prefix, so a HasPrefix check covers both.
const openspecConditionPrefix = "source matches openspec/changes/"

// computeComplexityRouting maps a file count to a pipeline mode.
func computeComplexityRouting(fileCount int, lightweight bool) ComplexityRouting {
	var mode, reason string
	switch {
	case fileCount <= 0:
		mode = "full"
		reason = "file count unknown — defaulting to full pipeline"
	case fileCount == 1:
		if lightweight {
			mode = "lightweight"
			reason = fmt.Sprintf("%d file detected — lightweight pipeline", fileCount)
		} else {
			mode = "skip"
			reason = fmt.Sprintf("%d file detected — skip pipeline", fileCount)
		}
	case fileCount <= 3:
		mode = "lightweight"
		reason = fmt.Sprintf("%d files detected — lightweight pipeline", fileCount)
	default:
		mode = "full"
		reason = fmt.Sprintf("%d files detected — full pipeline", fileCount)
	}
	return ComplexityRouting{
		FileCount:    fileCount,
		PipelineMode: mode,
		Reason:       reason,
	}
}

// extractTemplateBullets extracts a bullet list (lines starting with "- ")
// from the block under a given ## heading in a markdown template. Returns
// nil when the heading is absent.
func extractTemplateBullets(content, heading string) []string {
	locs := reqHeadingRe.FindAllStringSubmatchIndex(content, -1)
	blockStart := -1
	blockEnd := len(content)
	for _, loc := range locs {
		headingText := strings.TrimSpace(content[loc[2]:loc[3]])
		if blockStart == -1 {
			if headingText == heading {
				blockStart = loc[1]
			}
			continue
		}
		blockEnd = loc[0]
		break
	}
	if blockStart == -1 {
		return nil
	}
	block := content[blockStart:blockEnd]
	var items []string
	for _, m := range reqListItemRe.FindAllStringSubmatch(block, -1) {
		if text := strings.TrimSpace(m[1]); text != "" {
			items = append(items, text)
		}
	}
	return items
}

// buildSkeletonMarkdown builds the plan skeleton from the parsed template
// sections, evaluating conditions and applying lightweight adjustments.
func buildSkeletonMarkdown(sections []TemplateSection, openspecActive, lightweight bool) string {
	var b strings.Builder
	for _, sec := range sections {
		b.WriteString("## ")
		b.WriteString(sec.Name)
		b.WriteString("\n\n")

		body := "[TBD]"

		// Conditional section evaluation.
		if sec.Condition != nil {
			cond := *sec.Condition
			if strings.HasPrefix(cond, openspecConditionPrefix) {
				if !openspecActive {
					body = "Not applicable — no OpenSpec change"
				}
			} else {
				body = fmt.Sprintf("Not applicable — condition %q not recognized", cond)
			}
		}

		// Deviations table placeholder.
		if body == "[TBD]" && sec.Name == "Deviations & assumptions" {
			body = "| Item | asked | does | why |\n|---|---|---|---|\n| [TBD] | [TBD] | [TBD] | [TBD] |"
		}

		// Lightweight adjustment: step-5-owned sections.
		if body == "[TBD]" && lightweight && step5OwnedSections[sec.Name] {
			body = "Not applicable — lightweight plan"
		}

		b.WriteString(body)
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// buildHeaderMarkdown builds the plan document header with placeholder fields.
func buildHeaderMarkdown() string {
	return `# [Feature Name] Implementation Plan

**Goal:** [TBD]
**Architecture:** [TBD]
**Source:** [TBD]
**Verification:** [TBD]

---
`
}

// buildTemplateResolution resolves the active plan template and builds
// the full TemplateResolution struct. It is called from planPrepareCore
// only when in.ResolveTemplate is true.
func buildTemplateResolution(mainRoot string, in PlanPrepareIn, planTemplatePath *string) (*TemplateResolution, []string) {
	warnings := []string{}

	openspecActive := in.FromOpenspecDirect || in.OpenspecInlineGenerate
	routing := computeComplexityRouting(in.FileCount, in.Lightweight)

	// Resolve the active template path: project override -> shipped default.
	activePath := ""
	if planTemplatePath != nil {
		activePath = *planTemplatePath
	}

	// Try to parse the active template (project override).
	var sections []TemplateSection
	var templateContent string
	if activePath != "" {
		content, err := os.ReadFile(activePath)
		if err == nil {
			templateContent = string(content)
			parsed, parseErr := parseTemplateRequiredSectionsFull(activePath)
			if parseErr != nil || len(parsed) == 0 {
				// Malformed project override — fall back to shipped default.
				warnings = append(warnings, "Project template unreadable — fell back to shipped default")
				activePath = ""
			} else {
				sections = parsed
			}
		} else {
			warnings = append(warnings, "Project template unreadable — fell back to shipped default")
			activePath = ""
		}
	}

	// Fallback to shipped default template.
	if activePath == "" {
		defaultPath := resolveSkillTemplate("plan-template-default.md")
		if defaultPath == nil {
			// Both project override and shipped default unavailable.
			return nil, []string{"Active plan template and shipped default both unresolvable — run error-report"}
		}
		activePath = *defaultPath
		content, err := os.ReadFile(activePath)
		if err != nil {
			return nil, []string{fmt.Sprintf("Shipped default template unreadable: %s — run error-report", err.Error())}
		}
		templateContent = string(content)
		parsed, parseErr := parseTemplateRequiredSectionsFull(activePath)
		if parseErr != nil || len(parsed) == 0 {
			return nil, []string{"Shipped default template malformed (no Required Sections) — run error-report"}
		}
		sections = parsed
	}

	// Extract discovery questions and verification patterns from template content.
	discoveryQuestions := extractTemplateBullets(templateContent, "Discovery Questions")
	if discoveryQuestions == nil {
		discoveryQuestions = []string{}
	}
	verificationPatterns := extractTemplateBullets(templateContent, "Verification Patterns")
	if verificationPatterns == nil {
		verificationPatterns = []string{}
	}

	// Build skeleton and header markdown. The lightweight adjustment fires
	// when Step 5 (the multi-lens review) will be skipped — either explicitly
	// requested or inferred from routing (any mode other than "full" skips it).
	lightweightAdjust := in.Lightweight || routing.PipelineMode != "full"
	skeletonMarkdown := buildSkeletonMarkdown(sections, openspecActive, lightweightAdjust)
	headerMarkdown := buildHeaderMarkdown()

	// Populate summary and next hint.
	summary := fmt.Sprintf(
		"Resolved plan template at %s with %d sections, %d discovery questions. Routing: %s (%s).",
		activePath, len(sections), len(discoveryQuestions), routing.PipelineMode, routing.Reason,
	)
	next := "Write headerMarkdown + skeletonMarkdown to plan file. Use routing.pipelineMode to select full or lightweight path."

	return &TemplateResolution{
		ActiveTemplatePath:   activePath,
		Sections:             sections,
		DiscoveryQuestions:   discoveryQuestions,
		VerificationPatterns: verificationPatterns,
		SkeletonMarkdown:     skeletonMarkdown,
		HeaderMarkdown:       headerMarkdown,
		PipelineMode:         routing.PipelineMode,
		Routing:              routing,
		Summary:              summary,
		Next:                 next,
		Warnings:             warnings,
	}, nil
}

// ---------------------------------------------------------------------------
// skillInvoked marker (R20) — written eagerly at the start of plan_prepare
// ---------------------------------------------------------------------------

// writeSkillInvokedMarker mirrors plan.js's eager pruneStateFiles+initState
// call: state.Init creates a fresh plan-<slug>-<timestamp>.json, and the
// subsequent state.Write prunes all other plan-<slug>-*.json files (except
// the one just created), matching JS's explicit prune-then-init two-step
// with a single net effect: exactly one surviving file per branch. Failures
// are swallowed (non-fatal), mirroring plan.js's blanket try/catch — marker
// write failures must not block prepare output.
func writeSkillInvokedMarker(mainRoot, contentRoot string) {
	branch, err := gitx.CurrentBranch(contentRoot)
	if err != nil || branch == "" {
		return
	}
	st, err := state.Init(mainRoot, "plan", branch, "")
	if err != nil {
		return
	}
	st.Data["planIntegrity"] = map[string]any{
		"skillInvoked": time.Now().UTC().Format(time.RFC3339),
	}
	_ = state.Write(st)
}

// ---------------------------------------------------------------------------
// plan_prepare core logic
// ---------------------------------------------------------------------------

// planPrepareCore is the core logic, separated from the handler for
// testability. mainRoot anchors config/.sdlc/execution/ lookups; contentRoot
// anchors OpenSpec content and git-branch detection (issue #457: OpenSpec
// scans live on the active branch in the active worktree).
func planPrepareCore(mainRoot, contentRoot string, in PlanPrepareIn) (PlanPrepareOut, error) {
	errs := []string{}

	// KD5 gate: hard-abort with a minimal errors-only payload on config
	// migration failure, mirroring plan.js's ensureConfigVersion short-circuit
	// (main() returns before touching openspec/guardrails/lanes/etc). Go's
	// fixed output schema cannot reproduce JS's differently-shaped early
	// payload ({errors, flags, migration}); this returns the same
	// PlanPrepareOut type with only Errors populated.
	if !in.SkipConfigCheck {
		if err := configmigrate.Verify(mainRoot); err != nil {
			errs = append(errs, fmt.Sprintf("config-version: %s", err.Error()))
			return PlanPrepareOut{Errors: errs}, nil
		}
	}

	writeSkillInvokedMarker(mainRoot, contentRoot)

	// 1. OpenSpec detection.
	openspecInfo := openspec.DetectActiveChanges(contentRoot)
	if openspecInfo.Present {
		openspecInfo.Authoritative = &OpenspecAuthoritative{
			Path:       "openspec/config.yaml",
			SpecsCount: openspecInfo.SpecsCount,
		}
	}

	// 1a. Plan template detection.
	planTemplate := PlanTemplate{}
	planTemplatePath := filepath.Join(mainRoot, paths.DataDir, "plan-template.md")
	if fileExists(planTemplatePath) {
		p := planTemplatePath
		planTemplate.Path = &p
	}

	// 2. --from-openspec validation.
	var fromOpenspecResult *FromOpenspecResult
	openspecContext := OpenspecContext{}
	if in.FromOpenspec != "" {
		validation := validateChange(contentRoot, in.FromOpenspec)
		fromOpenspecResult = &FromOpenspecResult{
			Valid:          validation.Valid,
			ChangeName:     in.FromOpenspec,
			HasProposal:    validation.HasProposal,
			DeltaSpecCount: validation.DeltaSpecCount,
			HasDesign:      validation.HasDesign,
			HasTasks:       validation.HasTasks,
			TasksDone:      validation.TasksDone,
			TasksTotal:     validation.TasksTotal,
			Stage:          validation.Stage,
		}

		if !validation.Valid {
			for _, e := range validation.Errors {
				if !strings.HasPrefix(e, "Warning:") {
					errs = append(errs, e)
				}
			}
		}

		// 2a. Parse tasks.md, inject <!-- ref:<ref> --> comments (idempotent,
		// write-once — existing ref comments are left untouched).
		if validation.Valid && validation.HasTasks && isSafeChangeName(in.FromOpenspec) {
			tasksPath := filepath.Join(contentRoot, "openspec", "changes", in.FromOpenspec, "tasks.md")
			if original, err := os.ReadFile(tasksPath); err == nil {
				parsed := parseTasks(string(original))
				openspecContext.Tasks = parsed

				lines := strings.Split(string(original), "\n")
				updated := 0
				for _, entry := range parsed {
					idx := entry.Line - 1
					if idx < 0 || idx >= len(lines) {
						continue
					}
					if refCommentPresenceRe.MatchString(lines[idx]) {
						continue // write-once
					}
					trimmed := strings.TrimRightFunc(lines[idx], unicode.IsSpace)
					lines[idx] = trimmed + fmt.Sprintf(" <!-- ref:%s -->", entry.Ref)
					updated++
				}
				if updated > 0 {
					_ = os.WriteFile(tasksPath, []byte(strings.Join(lines, "\n")), 0o644)
				}
				openspecContext.TasksUpdated = updated
			}
			// Read/write failures are non-fatal in plan.js (stderr warning
			// only); silently absorbed here for the same "do not block
			// prepare" contract.
		}

		// 2b. Requirement inventory — only runs for a valid change.
		if validation.Valid {
			inv := getRequirementInventory(contentRoot, in.FromOpenspec)
			if inv.OK {
				openspecContext.Requirements = inv.Requirements
			} else {
				openspecContext.RequirementsError = inv.Error
			}
		}
	}

	// 3. Guardrails from the "plan" config section.
	guardrails, guardErr := loadGuardrails(mainRoot)
	if guardErr != "" {
		errs = append(errs, guardErr)
	}

	// 3b. Plan style (personal preference) and plan tasks (team contract).
	planStyle := loadPlanStyle(mainRoot)
	planTasks := loadPlanTasks(mainRoot)

	// 4. plan-explore discovery pack (KD4: in-process call, not subprocess).
	explorePack := buildExplorePack(mainRoot, contentRoot, in.FromOpenspec, "")

	// 5. G17 dispatch + githubHosting signals.
	githubHosting := buildGithubHosting(mainRoot)
	g17Dispatch := buildG17Dispatch()

	// 6. Lane fan-out + lens reviewer signals.
	lanes := buildLanes(g17Dispatch)
	lensReviewers := buildLensReviewers()

	// 7a. Intake audit dispatch.
	intakeAuditDispatch := buildIntakeAuditDispatch()

	// 8. Template resolution (only when resolveTemplate=true).
	var templateResolution *TemplateResolution
	if in.ResolveTemplate {
		tmpl, tmplErrs := buildTemplateResolution(mainRoot, in, planTemplate.Path)
		if tmplErrs != nil {
			errs = append(errs, tmplErrs...)
		}
		templateResolution = tmpl
	}

	return PlanPrepareOut{
		Openspec:            openspecInfo,
		FromOpenspec:        fromOpenspecResult,
		OpenspecContext:     openspecContext,
		Guardrails:          guardrails,
		Style:               planStyle,
		Tasks:               planTasks,
		ExplorePack:         explorePack,
		PlanTemplate:        planTemplate,
		GithubHosting:       githubHosting,
		G17Dispatch:         g17Dispatch,
		IntakeAuditDispatch: intakeAuditDispatch,
		Lanes:               lanes,
		LensReviewers:       lensReviewers,
		Template:            templateResolution,
		Errors:              errs,
	}, nil
}

// ---------------------------------------------------------------------------
// plan_mark
// ---------------------------------------------------------------------------

// validMarkers is the plan_mark marker enum. It is deliberately WIDER than
// plan.js's CLI-only VALID_MARK_NAMES (plan-file, guardrailsEvaluated,
// critiqueRan): the task's own binding Contract adds "skillInvoked" as a
// fourth valid value (skillInvoked is otherwise only ever written
// automatically during plan_prepare, never via an explicit CLI --mark in
// the JS original).
var validMarkers = map[string]bool{
	"plan-file":           true,
	"skillInvoked":        true,
	"guardrailsEvaluated": true,
	"critiqueRan":         true,
}

// markerKey maps a marker name to its planIntegrity JSON key, mirroring
// plan.js's markerKey ('plan-file' -> 'planFile'; others map identity).
func markerKey(marker string) string {
	if marker == "plan-file" {
		return "planFile"
	}
	return marker
}

// PlanMarkIn is the input for the plan_mark tool.
type PlanMarkIn struct {
	Marker string `json:"marker"`
	Path   string `json:"path"`
}

// PlanMarkOut is the output for the plan_mark tool.
type PlanMarkOut struct {
	OK     bool   `json:"ok"`
	Marker string `json:"marker"`
	Path   string `json:"path"`
}

// planMark is the core logic, separated from the handler for testability.
// mainRoot anchors the .sdlc/execution/ state directory (state files are
// always resolved relative to the main worktree, matching lib/state.js's
// resolveStateDir); contentRoot anchors current-branch detection (the
// branch actually being worked on, which may differ from main's branch in
// a linked worktree).
func planMark(mainRoot, contentRoot string, in PlanMarkIn) (PlanMarkOut, error) {
	if !validMarkers[in.Marker] {
		names := make([]string, 0, len(validMarkers))
		for k := range validMarkers {
			names = append(names, k)
		}
		sort.Strings(names)
		return PlanMarkOut{}, &mcpserver.DomainError{
			Msg: fmt.Sprintf("unknown marker %q; must be one of: %s", in.Marker, strings.Join(names, ", ")),
		}
	}
	if in.Marker == "plan-file" && strings.TrimSpace(in.Path) == "" {
		return PlanMarkOut{}, &mcpserver.DomainError{
			Msg: `marker "plan-file" requires a non-empty path`,
		}
	}

	branch, err := gitx.CurrentBranch(contentRoot)
	if err != nil || branch == "" {
		return PlanMarkOut{}, &mcpserver.InfraError{
			Msg:   "could not determine current branch",
			Cause: err,
		}
	}

	st, err := state.Find(mainRoot, "plan", branch)
	if err != nil {
		return PlanMarkOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("find plan state file: %s", err.Error()),
			Cause: err,
		}
	}
	if st == nil {
		return PlanMarkOut{}, &mcpserver.DomainError{
			Msg: fmt.Sprintf("no plan state file found for branch %q; run plan_prepare first", branch),
		}
	}

	integrity, ok := st.Data["planIntegrity"].(map[string]any)
	if !ok {
		integrity = map[string]any{}
	}
	key := markerKey(in.Marker)
	integrity[key] = time.Now().UTC().Format(time.RFC3339)
	st.Data["planIntegrity"] = integrity

	if in.Marker == "plan-file" {
		st.Data["planFilePath"] = in.Path
	}

	if err := state.Write(st); err != nil {
		return PlanMarkOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("write plan state file: %s", err.Error()),
			Cause: err,
		}
	}

	return PlanMarkOut{OK: true, Marker: in.Marker, Path: st.Path}, nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterPlanTools registers plan_prepare and plan_mark on the server.
// plan_explore_prepare is registered separately by RegisterPlanExploreTools
// in plan_explore.go. Registration only — wiring into runMCP/main.go is a
// later task.
func RegisterPlanTools(s *mcpserver.Server) {
	mcpserver.Register(s, "plan_prepare",
		"Prepare OpenSpec detection, guardrails, explore-pack discovery, and G17/lane/lens dispatch metadata for plan.",
		func(_ mcpserver.Ctx, in PlanPrepareIn) (PlanPrepareOut, error) {
			mainRoot, err := worktree.MainRoot()
			if err != nil {
				return PlanPrepareOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve main root: %s", err.Error()),
					Cause: err,
				}
			}
			contentRoot, err := worktree.ActiveRoot()
			if err != nil {
				return PlanPrepareOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve active root: %s", err.Error()),
					Cause: err,
				}
			}
			return planPrepareCore(mainRoot, contentRoot, in)
		},
	)

	mcpserver.Register(s, "plan_mark",
		"INTERNAL — called by sdlc skills only. Write a plan-integrity checkpoint marker (plan-file, skillInvoked, guardrailsEvaluated, critiqueRan) into the current branch's plan state file.",
		func(_ mcpserver.Ctx, in PlanMarkIn) (PlanMarkOut, error) {
			mainRoot, err := worktree.MainRoot()
			if err != nil {
				return PlanMarkOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve main root: %s", err.Error()),
					Cause: err,
				}
			}
			contentRoot, err := worktree.ActiveRoot()
			if err != nil {
				return PlanMarkOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve active root: %s", err.Error()),
					Cause: err,
				}
			}
			return planMark(mainRoot, contentRoot, in)
		},
	)
}
