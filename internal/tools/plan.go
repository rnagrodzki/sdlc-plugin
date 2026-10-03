package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/rnagrodzki/sdlc-plugin/internal/commstyle"
	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/history"
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

// PlanPrepareIn is the input for the plan_prepare tool. UserPrompt is
// forwarded into the embedded explorePack (see buildExplorePack below) so
// keyword-scope and web-research-signal detection can run on it — the prior
// deliberate-empty-prompt ruling predates those two features, which degrade
// to no-ops without a prompt. plan_explore_prepare's separate UserPrompt
// field remains the primary way to exercise them explicitly.
type PlanPrepareIn struct {
	SkipConfigCheck    bool   `json:"skipConfigCheck" jsonschema_description:"Skips the config-version auto-migration gate normally run before preparing plan metadata. Set only when the caller has already verified or migrated the config."`
	FromOpenspec       string `json:"fromOpenspec" jsonschema_description:"Name of the openspec change to prepare plan metadata from (change validation, tasks inventory, explore-pack discovery). Empty when not planning from an openspec change."`
	ResolveTemplate    bool   `json:"resolveTemplate" jsonschema_description:"When true, resolves the active plan template (project override, else shipped default) and includes the full template resolution in the output."`
	FromOpenspecDirect bool   `json:"fromOpenspecDirect" jsonschema_description:"True when the plan is being generated directly from an openspec change (no inline generation step). Combined with openspecStage to determine whether openspec routing is active."`
	OpenspecStage      bool   `json:"openspecStage,omitempty" jsonschema_description:"Plain JSON bool. True when the plan authors a new OpenSpec change and stages it. Example: true"`
	Lightweight        bool   `json:"lightweight" jsonschema_description:"Requests the lightweight complexity-routing path regardless of file count, adjusting dispatch metadata accordingly."`
	FileCount          int    `json:"fileCount" jsonschema_description:"Number of files the change is expected to touch, used with lightweight to compute complexity routing (pipeline mode)."`
	UserPrompt         string `json:"userPrompt" jsonschema_description:"User's plan request text, forwarded to buildExplorePack for keyword-scope and web-research-signal detection. Empty behaves identically to prior versions."`
	Resume             bool   `json:"resume,omitempty" jsonschema_description:"Boolean. Post-compact recovery: true reuses the active plan run of this branch without resetting it, and always resolves the template (as if resolveTemplate were true). The saved userPrompt and routing flags replace the input values. Fails when no active run exists. Example: true after the session context shows 'Active plan (post-compact):'."`
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
	// DeltaSpecPaths lists every delta spec file of the change at any depth
	// under specs/, repo-relative (e.g. openspec/changes/x/specs/a/b/spec.md),
	// as reported by `openspec status`. Empty when validation failed.
	DeltaSpecPaths []string `json:"deltaSpecPaths"`
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
//
// Next is hoisted to the rendered "**Next:**" line. RunID is the plan state
// file stem, GuardrailsFile is <runId>.evidence/guardrails.md, and
// StyleGuideFile is <runId>.evidence/style-guide.md; all three are empty
// (rendered "(none)") outside git, where no plan run is tracked.
type PlanPrepareOut struct {
	Next                string              `json:"next,omitempty"`
	RunID               string              `json:"runId"`
	GuardrailsFile      string              `json:"guardrailsFile"`
	StyleGuideFile      string              `json:"styleGuideFile"`
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

// changeValidation is validateChange's result: the change's artifact info
// plus its repo-relative delta spec paths, validity, and error/warning texts
// (warnings carry a "Warning:" prefix and never make Valid false).
type changeValidation struct {
	Valid          bool
	Errors         []string
	DeltaSpecPaths []string
	OpenspecChangeInfo
}

// openspecCLIUnavailablePrefix starts the error plan_prepare reports when an
// `openspec` CLI call fails (binary missing, or the CLI itself errors).
const openspecCLIUnavailablePrefix = "openspec CLI unavailable: "

// validateChange validates a --from-openspec change name through `openspec
// status --change <name> --json` (never by globbing the change directory).
// A grouped name ("grp/demo") is rejected before the CLI runs: OpenSpec does
// not support grouped changes (design.md D4).
func validateChange(contentRoot, changeName string) changeValidation {
	invalid := func(msg string) changeValidation {
		return changeValidation{
			Valid:              false,
			Errors:             []string{msg},
			DeltaSpecPaths:     []string{},
			OpenspecChangeInfo: OpenspecChangeInfo{Name: changeName},
		}
	}

	if strings.ContainsAny(changeName, `/\`) {
		return invalid(fmt.Sprintf("Invalid change name '%s': grouped changes are not supported — rename to a flat name", changeName))
	}

	info, deltaSpecPaths, err := openspec.StatusChange(contentRoot, changeName)
	if err != nil {
		if isOpenspecChangeNotFound(err) {
			return invalid(fmt.Sprintf("Change directory not found: openspec/changes/%s/", changeName))
		}
		return invalid(openspecCLIUnavailablePrefix + err.Error())
	}

	errs := []string{}
	if !info.HasProposal {
		errs = append(errs, fmt.Sprintf("Missing required file: openspec/changes/%s/proposal.md", changeName))
	}
	if len(deltaSpecPaths) == 0 {
		errs = append(errs, fmt.Sprintf("Warning: openspec/changes/%s/specs/ is empty or missing", changeName))
	}

	valid := true
	for _, e := range errs {
		if !strings.HasPrefix(e, "Warning:") {
			valid = false
			break
		}
	}

	return changeValidation{
		Valid:              valid,
		Errors:             errs,
		DeltaSpecPaths:     deltaSpecPaths,
		OpenspecChangeInfo: info,
	}
}

// isOpenspecChangeNotFound reports whether err is `openspec status`'s
// "Change '<name>' not found" failure. The CLI reports it as an error-severity
// status entry with code change_error, which openspec.Status embeds in the
// error text (there is no typed sentinel for it).
func isOpenspecChangeNotFound(err error) bool {
	if errors.Is(err, openspec.ErrCLINotFound) {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "change_error") && strings.Contains(msg, "not found")
}

// appendUnique appends s to list unless list already holds it.
func appendUnique(list []string, s string) []string {
	for _, existing := range list {
		if existing == s {
			return list
		}
	}
	return append(list, s)
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

// pendingTaskRefs computes what ref-stamping a tasks.md file's content would
// do, without writing anything: parsed is what planPrepareCore assigns to
// openspecContext.Tasks; lines is the content with every missing ref stamped
// in (write-once — a task line that already carries an inline ref comment is
// left untouched); updated is how many lines gained a ref (0 when every task
// line was already stamped). The actual write is stampTaskRefs's job.
func pendingTaskRefs(original string) (lines []string, parsed []TaskEntry, updated int) {
	parsed = parseTasks(original)
	lines = strings.Split(original, "\n")
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
	return lines, parsed, updated
}

// stampTaskRefs reads tasksPath, applies pendingTaskRefs, and writes the
// result back only when at least one line gained a ref comment. Called only
// from execute_state's init handler (after plan approval) — plan_prepare
// itself must not write git-tracked files, since it runs inside plan mode.
func stampTaskRefs(tasksPath string) (updated int, err error) {
	original, err := os.ReadFile(tasksPath)
	if err != nil {
		return 0, err
	}
	lines, _, updated := pendingTaskRefs(string(original))
	if updated > 0 {
		if err := os.WriteFile(tasksPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return 0, err
		}
	}
	return updated, nil
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
	if err == nil {
		return false
	}
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	return strings.Contains(err.Error(), "executable file not found")
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

// PlanStyle is the resolved communication style plan_prepare returns:
// commstyle.Style, the [style] and [planStyle] config sections merged,
// validated, and defaulted by commstyle.FromSections, plus the derived
// numeric Limits and the rendered WritingGuide. [style] holds the keys
// every sdlc skill shares (audience, writingStandard, tone, language,
// technicalTerms); [planStyle] holds the plan-only keys (visualDensity,
// narrativeRules, instructions). Neither is a config.ProjectSections
// member, so both route to .sdlc-v2/local.toml (per-developer, gitignored).
type PlanStyle = commstyle.Style

// PlanTasks is the team contract for plan task deliverables: which fields
// are required on every task, and the overall contract shape. Loaded from
// the "plan" config section's "tasks" sub-key; "plan" is a
// config.ProjectSections member, so it routes to .sdlc-v2/config.toml
// (team-shared, committed).
type PlanTasks struct {
	RequiredFields []string `json:"requiredFields"`
	ContractShape  string   `json:"contractShape"`
}

// readStyleSection reads one style config section ("style" or "planStyle"),
// mirroring loadGuardrails: a missing file or section (config.ErrNotFound)
// is benign and returns nil, "". Any other ReadSection error (malformed
// TOML, unreadable file) also returns nil, plus an error string the caller
// must surface — otherwise a broken local.toml would silently drop the
// custom plan instructions.
func readStyleSection(mainRoot, name string) (map[string]any, string) {
	section, err := config.ReadSection(mainRoot, name)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return nil, ""
		}
		return nil, fmt.Sprintf("Failed to read %s config: %s", name, err.Error())
	}
	return section, ""
}

// withGuide fills in the derived fields of a commstyle.Style built by
// FromSections: the numeric Limits and the rendered plan writing guide.
func withGuide(s commstyle.Style) commstyle.Style {
	s.Limits = commstyle.LimitsFor(s)
	s.WritingGuide = commstyle.Guide(s)
	return s
}

// loadPlanStyle reads the "style" and "planStyle" config sections and
// merges them with commstyle.FromSections, then fills in the derived
// Limits and WritingGuide with withGuide. A read error on either section
// is reported in the returned string (both are surfaced, space-joined,
// when both fail); the Style itself always falls back to commstyle's
// defaults on a read error, mirroring loadGuardrails' benign-absence
// handling.
func loadPlanStyle(mainRoot string) (PlanStyle, string) {
	shared, sErr := readStyleSection(mainRoot, "style")
	plan, pErr := readStyleSection(mainRoot, "planStyle")
	s := withGuide(commstyle.FromSections(shared, plan))
	if sErr != "" || pErr != "" {
		return s, strings.TrimSpace(sErr + " " + pErr)
	}
	return s, ""
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
// find cascade over ~/.claude/plugins. The Go binary mirrors that first step
// with CLAUDE_PLUGIN_ROOT (see buildSkillTemplateIndex) and falls back to
// the same find cascade; a miss on both degrades to nil, matching plan.js's
// own null-degrade contract exactly.
//
// CLAUDE_PLUGIN_ROOT must be checked directly: a plugin installed in
// dev/path mode (a marketplace entry whose "path" points at a local
// checkout) is loaded straight from that path and never copied into
// ~/.claude/plugins, so the find cascade alone can never see its templates.
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
// computed for all template names in a single pass). Entries served
// directly from CLAUDE_PLUGIN_ROOT (this plugin's own skills/plan/
// directory, when running in dev/path mode) always win over anything the
// walk finds, since they name the exact templates shipped with the
// binary that is currently running.
func buildSkillTemplateIndex() map[string]string {
	index := map[string]string{}

	home, err := os.UserHomeDir()
	if err == nil {
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
	}

	if pluginRoot := os.Getenv("CLAUDE_PLUGIN_ROOT"); pluginRoot != "" {
		planDir := filepath.Join(pluginRoot, "skills", "plan")
		entries, err := os.ReadDir(planDir)
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					index[e.Name()] = filepath.Join(planDir, e.Name())
				}
			}
		}
	}

	return index
}

// resetSkillTemplateIndex clears the cached skill template index and resets
// the sync.Once guard so the next resolveSkillTemplate/buildSkillTemplateIndex
// call re-walks and rebuilds it from scratch. Test seam only: production
// code relies on the index being built at most once per process
// (skillTemplateIndexOnce); tests that vary CLAUDE_PLUGIN_ROOT or HOME
// between cases need a way to invalidate that cache between them.
func resetSkillTemplateIndex() {
	skillTemplateIndexOnce = sync.Once{}
	skillTemplateIndex = nil
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
		{"guardrail-compliance", "sonnet", "lane-guardrail-compliance-prompt.md", []string{"G14", "G22"}},
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
// section. A HasPrefix check (not an exact match) is used deliberately: the
// condition text after the prefix is never parsed — only openspecActive
// (fromOpenspecDirect || openspecStage) decides the section body. This lets
// the prefix match the bare form ("source matches openspec/changes/", used
// in tests) and any suffixed form, including the legacy "... or
// openspecInlineGenerate" that may still appear in project template
// overrides and in the shipped default template on disk until a later task
// renames it to "... or openspecStage".
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

	openspecActive := in.FromOpenspecDirect || in.OpenspecStage
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
// Plan run selection — stable run ID, resume mode, guardrails file
// ---------------------------------------------------------------------------

// styleGuideReminder is appended to every plan_prepare root "next" hint, so
// the plan author always sees the style reminder, not just at the first
// call.
const styleGuideReminder = " Follow style.writingGuide for every narrative section."

// Root "next" hints returned by plan_prepare, one per run-selection row.
const (
	planPrepareNextFirst = "Print the context detection summary. Then run the gate check and complexity routing, and call plan_prepare again with resolveTemplate:true and the same userPrompt." + styleGuideReminder
	planPrepareNextTmpl  = "Write template.headerMarkdown + template.skeletonMarkdown to the plan file, then call plan_mark with marker \"plan-file\" and the plan path." + styleGuideReminder
)

// planPrepareResumeNext is the root next (and template.next) of a resume call.
func planPrepareResumeNext(runID string) string {
	return fmt.Sprintf("Resume mode: run %s reused. Write template.headerMarkdown + template.skeletonMarkdown to the plan file only if the plan file is empty. Then call plan_support with action \"evidence_digest\" and runId \"%s\".", runID, runID) + styleGuideReminder
}

// planRun is the plan run selected for this plan_prepare call. st is nil
// outside git (no branch), where no run is tracked.
type planRun struct {
	st   *state.State
	next string
}

// selectPlanRun applies plan_prepare's run-selection table:
//
//	first call (no resolveTemplate, no resume) -> new run: skillInvoked +
//	    creationIntent {userPrompt, timestamp}; older evidence dirs pruned.
//	resolveTemplate, active run                -> reuse it; full creationIntent.
//	resolveTemplate, no active run             -> new run; full creationIntent.
//	resume, active run                         -> reuse it; no state write.
//	resume, no active run                      -> DomainError.
//
// It returns the effective input: on resume, the saved userPrompt (when not
// empty) and saved flags (when present) replace the input values, and
// ResolveTemplate is forced on. When the current branch cannot be read
// (outside git, or a failed git call) a resume call returns an InfraError
// carrying the git error; the other calls return a nil run.
func selectPlanRun(mainRoot, contentRoot string, in PlanPrepareIn) (planRun, PlanPrepareIn, error) {
	if in.Resume {
		in.ResolveTemplate = true
	}

	branch, err := gitx.CurrentBranch(contentRoot)
	if err != nil {
		if in.Resume {
			// A resume must not tell the caller to start a new run when the
			// real problem is a failed git call: surface it with its cause.
			return planRun{}, in, &mcpserver.InfraError{
				Msg:        "could not determine current branch for resume",
				Suggestion: "Run plan_prepare from a git worktree on a named branch; if git itself failed, fix that and retry the resume.",
				Cause:      err,
			}
		}
		branch = ""
	}
	if branch == "" {
		next := planPrepareNextFirst
		if in.ResolveTemplate {
			next = planPrepareNextTmpl
		}
		return planRun{next: next}, in, nil
	}

	runsDir := filepath.Join(mainRoot, paths.DataDir, paths.RunsSubdir)

	// First call: always a new run, no read of the old one.
	if !in.ResolveTemplate {
		st, err := newPlanRun(mainRoot, branch, runsDir, map[string]any{
			"userPrompt": in.UserPrompt,
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			return planRun{}, in, err
		}
		return planRun{st: st, next: planPrepareNextFirst}, in, nil
	}

	active, err := state.ActivePlanRun(mainRoot, branch)
	if err != nil {
		p := planStateReadPath(runsDir, branch)
		return planRun{}, in, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("plan state read failed: %s", p),
			Suggestion: fmt.Sprintf("if %s is a file, delete it (the plan state file is corrupt); if it is the runs directory, make it a readable directory; then call plan_prepare again", p),
			Cause:      err,
		}
	}

	if in.Resume {
		if active == nil {
			return planRun{}, in, noActivePlanRunError(branch)
		}
		in = applySavedIntent(active, in)
		return planRun{st: active, next: planPrepareResumeNext(state.RunID(active))}, in, nil
	}

	if active == nil {
		st, err := newPlanRun(mainRoot, branch, runsDir, fullCreationIntent(in))
		if err != nil {
			return planRun{}, in, err
		}
		return planRun{st: st, next: planPrepareNextTmpl}, in, nil
	}

	writeCreationIntent(active, in)
	if err := state.Write(active); err != nil {
		return planRun{}, in, planStateWriteError(active.Path, runsDir, err)
	}
	return planRun{st: active, next: planPrepareNextTmpl}, in, nil
}

// newPlanRun creates a fresh plan run for branch: state.Init, then
// skillInvoked and creationIntent, then state.Write (which prunes the older
// state files of the branch), then a best-effort prune of older evidence
// directories.
func newPlanRun(mainRoot, branch, runsDir string, intent map[string]any) (*state.State, error) {
	st, err := state.Init(mainRoot, "plan", branch, "")
	if err != nil {
		return nil, planStateWriteError(runsDir, runsDir, err)
	}
	st.Data["planIntegrity"] = map[string]any{
		"skillInvoked": time.Now().UTC().Format(time.RFC3339),
	}
	st.Data["creationIntent"] = intent
	if err := state.Write(st); err != nil {
		return nil, planStateWriteError(st.Path, runsDir, err)
	}
	state.PruneEvidenceDirs(st)
	return st, nil
}

// writeCreationIntent records the full creation intent — the originating
// user prompt, the computed complexity routing and the routing flags — into
// st.Data. The caller writes st.
func writeCreationIntent(st *state.State, in PlanPrepareIn) {
	st.Data["creationIntent"] = fullCreationIntent(in)
}

// fullCreationIntent builds the creationIntent written by a resolveTemplate
// call. flags is what a later resume call restores.
func fullCreationIntent(in PlanPrepareIn) map[string]any {
	routing := computeComplexityRouting(in.FileCount, in.Lightweight)
	return map[string]any{
		"userPrompt": in.UserPrompt,
		"scope":      routing.PipelineMode,
		"routing":    routing.Reason,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"flags": map[string]any{
			"fromOpenspec":       in.FromOpenspec,
			"fromOpenspecDirect": in.FromOpenspecDirect,
			"openspecStage":      in.OpenspecStage,
			"lightweight":        in.Lightweight,
			"fileCount":          in.FileCount,
		},
	}
}

// applySavedIntent replaces input values with the ones saved in st's
// creationIntent: userPrompt when not empty, and each flag when present.
// Missing values leave the input unchanged.
func applySavedIntent(st *state.State, in PlanPrepareIn) PlanPrepareIn {
	intent, ok := st.Data["creationIntent"].(map[string]any)
	if !ok {
		return in
	}
	if p, ok := intent["userPrompt"].(string); ok && p != "" {
		in.UserPrompt = p
	}
	flags, ok := intent["flags"].(map[string]any)
	if !ok {
		return in
	}
	if v, ok := flags["fromOpenspec"].(string); ok {
		in.FromOpenspec = v
	}
	if v, ok := flags["fromOpenspecDirect"].(bool); ok {
		in.FromOpenspecDirect = v
	}
	if v, ok := flags["openspecStage"].(bool); ok {
		in.OpenspecStage = v
	}
	if v, ok := flags["lightweight"].(bool); ok {
		in.Lightweight = v
	}
	if v, ok := flags["fileCount"].(float64); ok {
		in.FileCount = int(v)
	}
	return in
}

func noActivePlanRunError(branch string) error {
	return &mcpserver.DomainError{
		Msg:        fmt.Sprintf("no active plan run on branch %s", branch),
		Suggestion: "call plan_prepare without resume to start a new plan run",
	}
}

// planStateWriteError reports a failed state.Init or state.Write. path is
// the file (or runs directory) that failed; runsDir is
// <main-worktree>/.sdlc-v2/runs.
func planStateWriteError(path, runsDir string, cause error) error {
	return &mcpserver.InfraError{
		Msg:        fmt.Sprintf("plan state write failed: %s", path),
		Suggestion: fmt.Sprintf("make sure %s/ is a writable directory, then call plan_prepare again", runsDir),
		Cause:      cause,
	}
}

// planStateTimestampRe is the timestamp part of a state file name.
var planStateTimestampRe = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

// planStateReadPath names the path behind a failed state.ActivePlanRun: the
// runs directory when it is not a directory, else the newest plan state file
// of the branch (exact slug match), else the runs directory.
func planStateReadPath(runsDir, branch string) string {
	info, err := os.Stat(runsDir)
	if err != nil || !info.IsDir() {
		return runsDir
	}
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return runsDir
	}
	prefix := "plan-" + state.SlugifyBranch(branch) + "-"
	best := ""
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		ts := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".json")
		if !planStateTimestampRe.MatchString(ts) {
			continue
		}
		if name > best {
			best = name
		}
	}
	if best == "" {
		return runsDir
	}
	return filepath.Join(runsDir, best)
}

// renderGuardrailsMarkdown renders guardrails as the guardrails.md body.
// Newlines in id and severity become spaces; every description line is
// prefixed with "> ".
func renderGuardrailsMarkdown(guardrails []map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Active plan guardrails (%d)\n", len(guardrails))
	if len(guardrails) == 0 {
		b.WriteString("\nNo plan guardrails configured.\n")
		return b.String()
	}
	for _, g := range guardrails {
		fmt.Fprintf(&b, "\n## %s (%s)\n", guardrailLine(g["id"]), guardrailLine(g["severity"]))
		desc := strings.ReplaceAll(guardrailString(g["description"]), "\r\n", "\n")
		for _, line := range strings.Split(strings.TrimRight(desc, "\n"), "\n") {
			b.WriteString("> " + line + "\n")
		}
	}
	return b.String()
}

func guardrailString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func guardrailLine(v any) string {
	s := strings.ReplaceAll(guardrailString(v), "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "\r", " ")
}

// writeGuardrailsFile writes <runId>.evidence/guardrails.md for st and
// returns its path.
func writeGuardrailsFile(st *state.State, guardrails []map[string]any) (string, error) {
	dir := state.EvidenceDir(st.Root, state.RunID(st))
	path := filepath.Join(dir, evidenceGuardrails)
	werr := os.MkdirAll(dir, 0o755)
	if werr == nil {
		werr = fsx.AtomicWriteBytes(path, []byte(renderGuardrailsMarkdown(guardrails)))
	}
	if werr != nil {
		return "", &mcpserver.InfraError{
			Msg:        fmt.Sprintf("guardrails file write failed: %s", path),
			Suggestion: fmt.Sprintf("make sure %s.evidence/ is a writable directory and guardrails.md is a file, then call plan_prepare again", state.RunID(st)),
			Cause:      werr,
		}
	}
	return path, nil
}

const evidenceStyleGuide = "style-guide.md"

// writeStyleGuideFile writes <runId>.evidence/style-guide.md for st and
// returns its path.
func writeStyleGuideFile(st *state.State, guide string) (string, error) {
	dir := state.EvidenceDir(st.Root, state.RunID(st))
	path := filepath.Join(dir, evidenceStyleGuide)
	werr := os.MkdirAll(dir, 0o755)
	if werr == nil {
		werr = fsx.AtomicWriteBytes(path, []byte(guide))
	}
	if werr != nil {
		return "", &mcpserver.InfraError{
			Msg:        fmt.Sprintf("style guide file write failed: %s", path),
			Suggestion: fmt.Sprintf("make sure %s.evidence/ is a writable directory and style-guide.md is a file, then call plan_prepare again", state.RunID(st)),
			Cause:      werr,
		}
	}
	return path, nil
}

// ---------------------------------------------------------------------------
// plan_prepare core logic
// ---------------------------------------------------------------------------

// planPrepareCore is the core logic, separated from the handler for
// testability. mainRoot anchors config/.sdlc-v2/runs/ lookups;
// contentRoot anchors OpenSpec content and git-branch detection (OpenSpec
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

	// Run selection: stable run ID, resume mode. On resume, in now carries
	// the saved userPrompt/flags and ResolveTemplate is forced on.
	run, in, err := selectPlanRun(mainRoot, contentRoot, in)
	if err != nil {
		return PlanPrepareOut{}, err
	}

	// 1. OpenSpec detection.
	openspecInfo, cliErr := openspec.DetectActiveChangesCLI(contentRoot)
	if cliErr != nil {
		errs = append(errs, openspecCLIUnavailablePrefix+cliErr.Error())
	}
	if openspecInfo.Present {
		openspecInfo.Authoritative = &OpenspecAuthoritative{
			Path:       "openspec/config.yaml",
			SpecsCount: openspecInfo.SpecsCount,
		}
	}

	// 1a. Plan template detection.
	planTemplate := PlanTemplate{}
	planTemplatePath := filepath.Join(mainRoot, paths.DataDir, paths.PlanTemplateFile)
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
			DeltaSpecPaths: validation.DeltaSpecPaths,
		}

		if !validation.Valid {
			for _, e := range validation.Errors {
				if !strings.HasPrefix(e, "Warning:") {
					// appendUnique: a missing CLI fails both detection
					// and validation with the same text.
					errs = appendUnique(errs, e)
				}
			}
		}

		// 2a. Parse tasks.md and compute which lines are still missing an
		// inline <!-- ref:<ref> --> comment. plan_prepare runs inside plan
		// mode and must not write git-tracked files — the actual stamp is
		// applied later by execute_state({action:"init"}), once the plan is
		// approved (see stampTaskRefs). TasksUpdated here is a PENDING count,
		// not a record of a write plan_prepare itself performed.
		if validation.Valid && validation.HasTasks && isSafeChangeName(in.FromOpenspec) {
			tasksPath := filepath.Join(contentRoot, "openspec", "changes", in.FromOpenspec, "tasks.md")
			if original, err := os.ReadFile(tasksPath); err == nil {
				_, parsed, updated := pendingTaskRefs(string(original))
				openspecContext.Tasks = parsed
				openspecContext.TasksUpdated = updated
			}
			// Read failures are non-fatal in plan.js (stderr warning only);
			// silently absorbed here for the same "do not block prepare"
			// contract.
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
	planStyle, styleErr := loadPlanStyle(mainRoot)
	if styleErr != "" {
		errs = append(errs, styleErr)
	}
	planTasks := loadPlanTasks(mainRoot)

	// 3c. guardrails.md and style-guide.md in the run's evidence directory.
	runID, guardrailsFile, styleGuideFile := "", "", ""
	if run.st != nil {
		runID = state.RunID(run.st)
		p, err := writeGuardrailsFile(run.st, guardrails)
		if err != nil {
			return PlanPrepareOut{}, err
		}
		guardrailsFile = p
		sp, err := writeStyleGuideFile(run.st, planStyle.WritingGuide)
		if err != nil {
			return PlanPrepareOut{}, err
		}
		styleGuideFile = sp
	}

	// 4. plan-explore discovery pack (KD4: in-process call, not subprocess).
	// A resume call keeps the zero value: the run's research already exists,
	// so no new explore tempdir is created.
	var explorePack ExplorePack
	if !in.Resume {
		explorePack = buildExplorePack(mainRoot, contentRoot, in.FromOpenspec, in.UserPrompt)
	}

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
		if in.Resume && templateResolution != nil {
			templateResolution.Next = run.next
		}
	}

	return PlanPrepareOut{
		Next:                run.next,
		RunID:               runID,
		GuardrailsFile:      guardrailsFile,
		StyleGuideFile:      styleGuideFile,
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
// the JS original), and "done" as a fifth: a terminal marker the plan
// SKILL.md stamps right before ExitPlanMode to signal the plan is finished.
// "done" is deliberately excluded from stop_hooks.go's requiredPlanMarkers —
// it gates *whether* those four markers are checked at all (see
// planIntegrityFromState), it is not itself one of the checked markers.
//
// "guardrailResults" and "criticalDecisions" are a sixth/seventh kind:
// structured-data markers (see PlanMarkIn.Data) that append to their own
// top-level st.Data key instead of stamping a timestamp into planIntegrity —
// they never participate in the requiredPlanMarkers check.
//
// "checkpoint" is an eighth kind: it also owns its own top-level st.Data
// key ("checkpoint"), but unlike guardrailResults/criticalDecisions it
// REPLACES that key's value on every call rather than appending to it — see
// PlanCheckpoint. It is deliberately absent from structuredDataMarkers below
// (that map is append-only) and is handled by its own branch in planMark.
var validMarkers = map[string]bool{
	"plan-file":           true,
	"skillInvoked":        true,
	"guardrailsEvaluated": true,
	"critiqueRan":         true,
	"done":                true,
	"guardrailResults":    true,
	"criticalDecisions":   true,
	"checkpoint":          true,
}

// structuredDataMarkers maps a plan_mark structured-data marker name to the
// key inside PlanMarkIn.Data holding its array payload
// (plan_mark({marker:"guardrailResults", data:{results:[...]}}) and
// plan_mark({marker:"criticalDecisions", data:{decisions:[...]}})).
var structuredDataMarkers = map[string]string{
	"guardrailResults":  "results",
	"criticalDecisions": "decisions",
}

// normalizeCriticalDecisions stamps the call-time "at" timestamp onto every
// "criticalDecisions" entry — overwriting any caller-supplied "at" — and
// defaults a missing "rejected" field to an empty list. An entry that
// already carries "rejected" (expected shape [{option,why}]) is otherwise
// stored unchanged. It never touches "guardrailResults", which appends its
// raw payload as-is (byte-identical to input) via the caller's separate
// branch. A non-object entry (not map[string]any) passes through unchanged,
// since it has no "at"/"rejected" fields to normalize.
func normalizeCriticalDecisions(entries []any, at string) []any {
	normalized := make([]any, len(entries))
	for i, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			normalized[i] = e
			continue
		}
		copied := make(map[string]any, len(m)+2)
		for k, v := range m {
			copied[k] = v
		}
		if _, hasRejected := copied["rejected"]; !hasRejected {
			copied["rejected"] = []any{}
		}
		copied["at"] = at
		normalized[i] = copied
	}
	return normalized
}

// markerKey maps a marker name to its planIntegrity JSON key, mirroring
// plan.js's markerKey ('plan-file' -> 'planFile'; others map identity).
func markerKey(marker string) string {
	if marker == "plan-file" {
		return "planFile"
	}
	return marker
}

// PlanCheckpoint is the "checkpoint" marker's payload: the plan run's
// current SKILL.md step, its retry/iteration counter within that step, and
// the writer IDs the current fan-out is waiting on. Stored whole at
// st.Data["checkpoint"], replacing any previous value (see validMarkers).
type PlanCheckpoint struct {
	Step            string   `json:"step"`
	Iteration       int      `json:"iteration"`
	ExpectedWriters []string `json:"expectedWriters"`
	UpdatedAt       string   `json:"updatedAt"`
}

// writerIDRe validates a single PlanCheckpoint.ExpectedWriters entry: it
// must start with a letter or digit, followed by up to 63 more letters,
// digits, dots, underscores or hyphens (max 64 total). The evidence store
// (plan_support evidence_* actions) validates writer IDs with it too.
var writerIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// validCheckpointSteps lists the plan SKILL.md step identifiers a
// "checkpoint" marker may record, including the two review sub-steps 6.5
// (lane fan-out) and 6.6 (lens reviewers).
var validCheckpointSteps = []string{"0", "1", "2", "3", "4", "5", "6", "6.5", "6.6", "7"}

// validateCheckpointData validates and parses the "checkpoint" marker's
// data payload into a PlanCheckpoint (UpdatedAt is left zero; the caller
// stamps it at write time). Every failure is a DomainError with a non-empty
// Suggestion; nothing is written to state by this function.
func validateCheckpointData(data map[string]any) (PlanCheckpoint, error) {
	if len(data) == 0 {
		return PlanCheckpoint{}, &mcpserver.DomainError{
			Msg:        `checkpoint needs data {step, iteration, expectedWriters}`,
			Suggestion: `call plan_mark with data {step:"3", iteration:1}`,
		}
	}

	for k := range data {
		switch k {
		case "step", "iteration", "expectedWriters":
		default:
			return PlanCheckpoint{}, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("checkpoint data has unknown key %q", k),
				Suggestion: fmt.Sprintf("remove %q; allowed keys: step, iteration, expectedWriters", k),
			}
		}
	}

	step, _ := data["step"].(string)
	if !slices.Contains(validCheckpointSteps, step) {
		return PlanCheckpoint{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("checkpoint step %q is not valid — valid steps: %s", step, strings.Join(validCheckpointSteps, ", ")),
			Suggestion: `pass step as one of these strings, e.g. data {step:"3"}`,
		}
	}

	iteration := 0
	if raw, present := data["iteration"]; present {
		// JSON numbers always decode to float64 (matching intField's
		// convention elsewhere in this package); anything else, or a
		// non-whole or negative value, is rejected.
		f, ok := raw.(float64)
		if !ok || f < 0 || f != math.Trunc(f) {
			return PlanCheckpoint{}, &mcpserver.DomainError{
				Msg:        "checkpoint iteration must be an integer >= 0",
				Suggestion: `pass iteration as a whole number, e.g. data {step:"3", iteration:1}`,
			}
		}
		iteration = int(f)
	}

	var expectedWriters []string
	if raw, present := data["expectedWriters"]; present {
		arr, ok := raw.([]any)
		if !ok {
			return PlanCheckpoint{}, &mcpserver.DomainError{
				Msg:        "checkpoint expectedWriters must be a JSON array of writer IDs",
				Suggestion: `pass expectedWriters as an array, e.g. data {step:"3", expectedWriters:["lane-static-structural-r1"]}`,
			}
		}
		if len(arr) > 32 {
			return PlanCheckpoint{}, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("checkpoint expectedWriters has %d entries, max 32", len(arr)),
				Suggestion: "pass only the writers of the current fan-out",
			}
		}
		expectedWriters = make([]string, 0, len(arr))
		for i, el := range arr {
			s, _ := el.(string)
			if !writerIDRe.MatchString(s) {
				return PlanCheckpoint{}, &mcpserver.DomainError{
					Msg:        fmt.Sprintf("checkpoint expectedWriters[%d] %q is not a valid writer ID", i, s),
					Suggestion: "use letters, digits, '.', '_' or '-' (max 64), e.g. lane-static-structural-r1",
				}
			}
			expectedWriters = append(expectedWriters, s)
		}
	}

	return PlanCheckpoint{Step: step, Iteration: iteration, ExpectedWriters: expectedWriters}, nil
}

// checkpointNext builds the "checkpoint" marker's Next instruction: the
// step-continuation sentence, plus a custom-instructions reminder when the
// "planStyle" config section has any. The section is read fresh on every
// call, never cached, so an edit to local.toml takes effect at the next
// checkpoint without restarting the MCP server. Mirrors the phrasing loadPlanStyle's doc comment describes
// for style.instructions.
func checkpointNext(mainRoot string, cp PlanCheckpoint) string {
	next := fmt.Sprintf("Checkpoint saved at step %s. Continue step %s.", cp.Step, cp.Step)
	style, styleErr := loadPlanStyle(mainRoot)
	if n := len(style.Instructions); n > 0 {
		next += fmt.Sprintf(" Follow the %d custom plan instructions (style.instructions).", n)
	}
	if styleErr != "" {
		next += fmt.Sprintf(" Warning: %s — custom plan instructions could not be loaded; fix local.toml.", styleErr)
	}
	return next
}

// ---------------------------------------------------------------------------
// Plan timing (start to last plan-file edit)
//
// Every plan_mark call refreshes st.Data["planTiming"] from the run's
// skillInvoked timestamp and the plan file's current mtime, so the run's
// duration always reflects the last edit to the plan document — never the
// time of the marker call itself (a "done" call minutes after the last edit
// still reports the edit time). The "done" marker additionally appends a
// durable history record, since the Stop hook deletes the plan state file
// once "done" is observed (see docs/plan-architecture.md's Lifecycle
// section) — without this, the timing would be lost the moment the run ends.
// ---------------------------------------------------------------------------

// refreshPlanTiming updates st.Data["planTiming"] ({startedAt,
// lastModifiedAt, durationMs}) from st.Data["planIntegrity"]["skillInvoked"]
// and the plan file's mtime. A relative st.Data["planFilePath"] is resolved
// against contentRoot and normalized in place (filepath.Join + Clean) so
// every later reader — this run's own history record, and ship_report.go's
// shipPlanTimingFor plan_file comparison — sees an absolute, cleaned path.
//
// Any missing or unusable input (no skillInvoked, no planFilePath, or a
// plan file that cannot be stat'ed) leaves planTiming and planFilePath
// exactly as they were — absent on a fresh run, or the previous value on a
// later call whose plan file momentarily disappeared. The caller's marker
// write still proceeds either way; timing is best-effort, never a reason to
// fail plan_mark.
func refreshPlanTiming(st *state.State, contentRoot string) {
	integrity, _ := st.Data["planIntegrity"].(map[string]any)
	skillInvoked, _ := integrity["skillInvoked"].(string)
	if skillInvoked == "" {
		return
	}
	startedAt, err := time.Parse(time.RFC3339, skillInvoked)
	if err != nil {
		return
	}

	planFilePath, _ := st.Data["planFilePath"].(string)
	if strings.TrimSpace(planFilePath) == "" {
		return
	}
	resolved := planFilePath
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(contentRoot, resolved)
	}
	resolved = filepath.Clean(resolved)

	info, err := os.Stat(resolved)
	if err != nil {
		return
	}

	lastModifiedAt := info.ModTime().UTC()
	st.Data["planFilePath"] = resolved
	st.Data["planTiming"] = map[string]any{
		"startedAt":      startedAt.UTC().Format(time.RFC3339),
		"lastModifiedAt": lastModifiedAt.Format(time.RFC3339),
		"durationMs":     lastModifiedAt.Sub(startedAt.UTC()).Milliseconds(),
	}
}

// planTimingInt64 reads an integer field out of a planTiming map that may
// hold either an int64 (set in-process by refreshPlanTiming earlier in the
// same call) or a float64 (decoded from a plan state file previously read
// back off disk, where every JSON number is a float64).
func planTimingInt64(timing map[string]any, key string) int64 {
	switch v := timing[key].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		return 0
	}
}

// appendPlanRunRecord appends one history.RunRecord for this plan run's
// "done" marker: skill "plan", outcome "done", branch, and the
// duration_ms/started_at/last_modified_at/plan_file fields that
// shipPlanTimingFor (ship_report.go) reads back from the latest skill:"plan"
// record. It mirrors ship_state.go's
// shipStateHistoryRecord (same history.NewFileWriter(historyDir(...)).AppendRun
// call), scoped to plan's own fixed field set.
//
// Returns nil without writing when there is nothing meaningful to record:
// planFilePath was never set, or planTiming was never successfully computed
// (the plan file was never stat-able during this run). Otherwise, any
// AppendRun error is returned to the caller, which reports it as a
// non-fatal warning — the marker write itself already succeeded.
func appendPlanRunRecord(mainRoot, branch string, st *state.State) error {
	planFilePath, _ := st.Data["planFilePath"].(string)
	if strings.TrimSpace(planFilePath) == "" {
		return nil
	}
	timing, ok := st.Data["planTiming"].(map[string]any)
	if !ok {
		return nil
	}
	startedAt, _ := timing["startedAt"].(string)
	lastModifiedAt, _ := timing["lastModifiedAt"].(string)
	if startedAt == "" || lastModifiedAt == "" {
		return nil
	}

	rec := history.RunRecord{
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		Skill:          "plan",
		Branch:         branch,
		Outcome:        "done",
		DurationMs:     planTimingInt64(timing, "durationMs"),
		PlanFile:       planFilePath,
		StartedAt:      startedAt,
		LastModifiedAt: lastModifiedAt,
	}

	w := history.NewFileWriter(historyDir(mainRoot))
	return w.AppendRun(rec)
}

// PlanMarkIn is the input for the plan_mark tool.
type PlanMarkIn struct {
	Marker string         `json:"marker" jsonschema:"enum=plan-file,enum=skillInvoked,enum=guardrailsEvaluated,enum=critiqueRan,enum=done,enum=guardrailResults,enum=criticalDecisions,enum=checkpoint" jsonschema_description:"Checkpoint marker: \"plan-file\", \"skillInvoked\", \"guardrailsEvaluated\", \"critiqueRan\", or the terminal \"done\" marker stamp the current timestamp into planIntegrity; \"guardrailResults\" and \"criticalDecisions\" instead append data's array payload to their own state key. \"checkpoint\" replaces the progress checkpoint (requires data)."`
	Path   string         `json:"path" jsonschema_description:"Plan file path to record. Only used (and required) when marker is \"plan-file\"."`
	Data   map[string]any `json:"data,omitempty" jsonschema_description:"Structured payload for the \"guardrailResults\" marker ({results:[{id,status,detail}]}) or the \"criticalDecisions\" marker ({decisions:[{key,choice,rejected,reason}]}; rejected is [{option,why}], defaults to [] when omitted; the tool always sets at to the call time, overwriting any caller-supplied value). For \"checkpoint\": JSON object {step: string, one of \"0\", \"1\", \"2\", \"3\", \"4\", \"5\", \"6\", \"6.5\", \"6.6\", \"7\"; iteration: integer >= 0; expectedWriters: JSON array of writer IDs (max 32)}. Example: {\"step\":\"3\",\"iteration\":1,\"expectedWriters\":[\"lane-static-structural-r1\"]}. Replaced, not appended. Ignored for every other marker."`
}

// PlanMarkOut is the output for the plan_mark tool.
type PlanMarkOut struct {
	OK       bool     `json:"ok"`
	Marker   string   `json:"marker"`
	Path     string   `json:"path"`
	Next     string   `json:"next,omitempty"`     // checkpoint only
	Warnings []string `json:"warnings,omitempty"` // done only: history write failed; the marker itself was saved
}

// planMark is the core logic, separated from the handler for testability.
// mainRoot anchors the .sdlc-v2/runs/ state directory (state files are
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
			Msg:        fmt.Sprintf("unknown marker %q; must be one of: %s", in.Marker, strings.Join(names, ", ")),
			Suggestion: "Pass one of the marker names listed above exactly as spelled (case-sensitive) in the marker field, then retry plan_mark.",
		}
	}
	if in.Marker == "plan-file" && strings.TrimSpace(in.Path) == "" {
		return PlanMarkOut{}, &mcpserver.DomainError{
			Msg:        `marker "plan-file" requires a non-empty path`,
			Suggestion: "Pass the plan document's file path in the path field — it is stored as planFilePath in the plan state file for later steps to read.",
		}
	}

	// "checkpoint" data is validated up front, before any state lookup, so
	// a malformed payload fails fast without needing a plan state file.
	var checkpoint PlanCheckpoint
	if in.Marker == "checkpoint" {
		cp, verr := validateCheckpointData(in.Data)
		if verr != nil {
			return PlanMarkOut{}, verr
		}
		checkpoint = cp
	}

	branch, err := gitx.CurrentBranch(contentRoot)
	if err != nil || branch == "" {
		return PlanMarkOut{}, &mcpserver.InfraError{
			Msg:        "could not determine current branch",
			Suggestion: "Run plan_mark from inside a git repository or worktree (git branch --show-current must succeed there), then retry plan_mark.",
			Cause:      err,
		}
	}

	// Every marker's lookup resolves the branch's most recent plan run by
	// filename timestamp (LatestPlanRun), not by state.Find's file mtime.
	st, err := state.LatestPlanRun(mainRoot, branch)
	if err != nil {
		return PlanMarkOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("find plan state file: %s", err.Error()),
			Suggestion: "Check read permission on " + paths.DataDir + "/runs/plan-<branch-slug>-*.json and that the file is not corrupted, then retry plan_mark.",
			Cause:      err,
		}
	}
	if st == nil {
		return PlanMarkOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("no plan state file found for branch %q; run plan_prepare first", branch),
			Suggestion: "Confirm this is the same branch plan_prepare was run on — the plan state file is looked up by branch name, so switching branches loses it.",
		}
	}

	// "checkpoint" replaces its own top-level state key on every call (no
	// append, unlike the structured-data markers below) and is the only
	// marker that returns a non-empty Next.
	if in.Marker == "checkpoint" {
		checkpoint.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		st.Data["checkpoint"] = checkpoint

		refreshPlanTiming(st, contentRoot)
		if err := state.Write(st); err != nil {
			return PlanMarkOut{}, &mcpserver.InfraError{
				Msg:        fmt.Sprintf("write plan state file: %s", err.Error()),
				Suggestion: "Check write permission on the plan state file path above and free disk space on the project root, then retry plan_mark with the same marker and data.",
				Cause:      err,
			}
		}
		return PlanMarkOut{OK: true, Marker: in.Marker, Path: st.Path, Next: checkpointNext(mainRoot, checkpoint)}, nil
	}

	// Structured-data markers append to their own top-level state key and
	// never touch planIntegrity — the existing timestamp-marker flow below
	// is entirely unaffected by this branch.
	if dataKey, isStructured := structuredDataMarkers[in.Marker]; isStructured {
		var newEntries []any
		if in.Data != nil {
			if arr, ok := in.Data[dataKey].([]any); ok {
				newEntries = arr
			}
		}
		if in.Marker == "criticalDecisions" {
			newEntries = normalizeCriticalDecisions(newEntries, time.Now().UTC().Format(time.RFC3339))
		}
		existing, _ := st.Data[in.Marker].([]any)
		st.Data[in.Marker] = append(existing, newEntries...)

		refreshPlanTiming(st, contentRoot)
		if err := state.Write(st); err != nil {
			return PlanMarkOut{}, &mcpserver.InfraError{
				Msg:        fmt.Sprintf("write plan state file: %s", err.Error()),
				Suggestion: "Check write permission on the plan state file path above and free disk space on the project root, then retry plan_mark with the same marker and data.",
				Cause:      err,
			}
		}
		return PlanMarkOut{OK: true, Marker: in.Marker, Path: st.Path}, nil
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

	refreshPlanTiming(st, contentRoot)
	if err := state.Write(st); err != nil {
		return PlanMarkOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("write plan state file: %s", err.Error()),
			Suggestion: "Check write permission on the plan state file path above and free disk space on the project root, then retry plan_mark with the same marker (and path, if marker is \"plan-file\").",
			Cause:      err,
		}
	}

	out := PlanMarkOut{OK: true, Marker: in.Marker, Path: st.Path}
	if in.Marker == "done" {
		if herr := appendPlanRunRecord(mainRoot, branch, st); herr != nil {
			out.Warnings = append(out.Warnings, "plan timing not saved to history: "+herr.Error())
		}
	}
	return out, nil
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
		"Prepare OpenSpec detection, guardrails, explore-pack discovery, and G17/lane/lens dispatch metadata for plan. "+
			"Writes the plan state file and <runId>.evidence/guardrails.md and style-guide.md under gitignored .sdlc-v2/runs/. A call without resume or resolveTemplate starts a new run and deletes older runs' evidence for the branch. Optional: resume (post-compact recovery; reuses the active run, implies resolveTemplate, and fails when none exists). Returns runId, guardrailsFile, styleGuideFile and next. A failed state read, state write, guardrails write or style guide write returns an infrastructure error; the evidence cleanup is best-effort.",
		mcpserver.Annotations{
			Title:      "Prepare plan state and template",
			ReadOnly:   true,
			Idempotent: false,
			OpenWorld:  false,
		},
		func(_ mcpserver.Ctx, in PlanPrepareIn) (PlanPrepareOut, error) {
			mainRoot, err := worktree.MainRoot()
			if err != nil {
				return PlanPrepareOut{}, &mcpserver.InfraError{
					Msg:        fmt.Sprintf("resolve main root: %s", err.Error()),
					Suggestion: "Run plan_prepare from inside a git repository (or one of its worktrees) so the main root can be resolved, then retry.",
					Cause:      err,
				}
			}
			contentRoot, err := worktree.ActiveRoot()
			if err != nil {
				return PlanPrepareOut{}, &mcpserver.InfraError{
					Msg:        fmt.Sprintf("resolve active root: %s", err.Error()),
					Suggestion: "Run plan_prepare from inside a git working tree — the current directory is not one. Change into the repository, then retry.",
					Cause:      err,
				}
			}
			return planPrepareCore(mainRoot, contentRoot, in)
		},
	)

	mcpserver.Register(s, "plan_mark",
		"INTERNAL — called by sdlc skills only. Write a plan-integrity checkpoint marker (plan-file, skillInvoked, guardrailsEvaluated, critiqueRan, done) into the current branch's plan state file, append structured data (guardrailResults, criticalDecisions) to it, or replace the progress checkpoint (checkpoint). "+
			"checkpoint: replace the progress checkpoint. Requires data.step (one of \"0\", \"1\", \"2\", \"3\", \"4\", \"5\", \"6\", \"6.5\", \"6.6\", \"7\"). Optional: data.iteration, data.expectedWriters. Returns next. Invalid input or a limit breach returns DomainError and writes nothing; an OS read/write failure returns InfraError. "+
			"Markers other than checkpoint return no next: the call only records state; continue the current SKILL.md step. "+
			"The done marker also appends the plan's timing (start to last plan-file edit) to .sdlc-v2/history/runs.jsonl; a failed append returns ok with warnings set. Repeating done appends another history record.",
		mcpserver.Annotations{
			Title:      "Record plan progress marker",
			ReadOnly:   true,
			Idempotent: false,
			OpenWorld:  false,
		},
		func(_ mcpserver.Ctx, in PlanMarkIn) (PlanMarkOut, error) {
			mainRoot, err := worktree.MainRoot()
			if err != nil {
				return PlanMarkOut{}, &mcpserver.InfraError{
					Msg:        fmt.Sprintf("resolve main root: %s", err.Error()),
					Suggestion: "Run plan_mark from inside a git repository (or one of its worktrees) so the main root can be resolved, then retry.",
					Cause:      err,
				}
			}
			contentRoot, err := worktree.ActiveRoot()
			if err != nil {
				return PlanMarkOut{}, &mcpserver.InfraError{
					Msg:        fmt.Sprintf("resolve active root: %s", err.Error()),
					Suggestion: "Run plan_mark from inside a git working tree — the current directory is not one. Change into the repository, then retry.",
					Cause:      err,
				}
			}
			return planMark(mainRoot, contentRoot, in)
		},
	)
}
